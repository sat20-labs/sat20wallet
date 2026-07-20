package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type rgb11AddressEvidence struct {
	*rgb11FlowEvidence
	statusMu sync.Mutex
	statuses map[string]*rgb11wallet.BitcoinTxStatus
}

func (e *rgb11AddressEvidence) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	e.statusMu.Lock()
	status := e.statuses[txid]
	if status != nil {
		copy := *status
		e.statusMu.Unlock()
		return &copy, nil
	}
	e.statusMu.Unlock()
	return e.rgb11FlowEvidence.GetTxStatus(txid)
}

func (e *rgb11AddressEvidence) setStatus(txid string, status rgb11wallet.BitcoinTxStatus) {
	e.statusMu.Lock()
	copy := status
	e.statuses[txid] = &copy
	e.statusMu.Unlock()
}

func TestRGB11AddressCodecsAndAccountEncryption(t *testing.T) {
	inline, err := encodeRGB11AddressEnvelope(rgb11AddressEnvelopeInline, []byte("ciphertext"))
	if err != nil || len(inline) != 2+len("ciphertext") {
		t.Fatalf("inline envelope len=%d err=%v", len(inline), err)
	}
	mode, payload, err := decodeRGB11AddressEnvelope(inline)
	if err != nil || mode != rgb11AddressEnvelopeInline || string(payload) != "ciphertext" {
		t.Fatalf("inline decode mode=%d payload=%q err=%v", mode, payload, err)
	}
	blob, err := encodeRGB11AddressEnvelope(rgb11AddressEnvelopeBlob, nil)
	if err != nil || len(blob) != 2 {
		t.Fatalf("blob envelope len=%d err=%v", len(blob), err)
	}
	if _, _, err := decodeRGB11AddressEnvelope([]byte{9, 9}); err == nil {
		t.Fatal("invalid envelope accepted")
	}

	encodedACK, err := encodeRGB11AddressACK(RGB11AddressACK{Status: RGB11AddressACKAccepted, Code: 7})
	if err != nil || len(encodedACK) != 4 {
		t.Fatalf("ACK len=%d err=%v", len(encodedACK), err)
	}
	decodedACK, err := decodeRGB11AddressACK(encodedACK)
	if err != nil || decodedACK.Status != RGB11AddressACKAccepted || decodedACK.Code != 7 {
		t.Fatalf("ACK decode=%+v err=%v", decodedACK, err)
	}

	var odd, other *btcec.PrivateKey
	for odd == nil || other == nil {
		priv, err := btcec.NewPrivateKey()
		if err != nil {
			t.Fatal(err)
		}
		if priv.PubKey().SerializeCompressed()[0] == 0x03 && odd == nil {
			odd = priv
		} else if other == nil {
			other = priv
		}
	}
	oddWallet := dkvsTestWalletFromPriv(t, odd)
	otherWallet := dkvsTestWalletFromPriv(t, other)
	oddID, err := dkvsAccountID(oddWallet)
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := dkvsAccountID(otherWallet)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := oddWallet.EncryptToAccount(otherID, []byte("rgb11 mailbox payload"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := otherWallet.DecryptFromAccount(oddID, ciphertext)
	if err != nil || string(plaintext) != "rgb11 mailbox payload" {
		t.Fatalf("account decrypt=%q err=%v", plaintext, err)
	}
	wrong, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	wrongWallet := dkvsTestWalletFromPriv(t, wrong)
	if _, err := wrongWallet.DecryptFromAccount(oddID, ciphertext); err == nil {
		t.Fatal("unrelated account decrypted mailbox payload")
	}
}

func TestRGB11AddressMailboxSequenceAdvances(t *testing.T) {
	senderPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	receiverPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	sender := dkvsTestWalletFromPriv(t, senderPriv)
	receiverID, err := dkvsindexer.CanonicalAccountID(receiverPriv.PubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	remote := newRGB11MemoryDKVSHTTP()
	client := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	transferID := strings.Repeat("1", 64)
	key, err := dkvsindexer.MailMsgKey(receiverID, dkvsindexer.AccountID(senderPriv.PubKey().SerializeCompressed()), transferID)
	if err != nil {
		t.Fatal(err)
	}
	opts := nextRGB11AddressRecordOptions(client, []string{key}, dkvsindexer.RecordOptions{TTL: 60_000})
	first, err := client.SendAccountMailboxMessage(sender, receiverID, transferID, []byte{1}, opts, nil)
	if err != nil || first.Seq != 1 {
		t.Fatalf("first seq=%d err=%v", first.Seq, err)
	}
	opts = nextRGB11AddressRecordOptions(client, []string{key}, dkvsindexer.RecordOptions{TTL: 60_000})
	second, err := client.SendAccountMailboxMessage(sender, receiverID, transferID, []byte{2}, opts, nil)
	if err != nil || second.Seq != first.Seq+1 {
		t.Fatalf("second seq=%d first=%d err=%v", second.Seq, first.Seq, err)
	}
}

func TestRGB11AddressTransferSchemeA(t *testing.T) {
	senderWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	recipientWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
	)
	unregisteredWallet := NewInternalWalletWithMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "", &chaincfg.TestNet4Params,
	)
	if senderWallet == nil || recipientWallet == nil || unregisteredWallet == nil {
		t.Fatal("create RGB11 address-mode wallets")
	}
	senderScript, err := AddrToPkScript(senderWallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}

	const sourceOutpoint = "14295d5bb1a191cdb6286dc0944df938421e3dfcbf0811353ccac4100c2068c5:1"
	const plainOutpoint = "3333333333333333333333333333333333333333333333333333333333333333:0"
	baseEvidence := &rgb11FlowEvidence{
		utxos: map[string]*rgb11wallet.BitcoinUTXO{
			sourceOutpoint: {OutPoint: sourceOutpoint, Value: 10_000, PkScript: senderScript, Confirmations: 6},
		},
		rawTx:      make(map[string][]byte),
		spendingTx: make(map[string]string),
	}
	evidence := &rgb11AddressEvidence{rgb11FlowEvidence: baseEvidence, statuses: make(map[string]*rgb11wallet.BitcoinTxStatus)}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	sourceOutput := indexer.NewTxOutput(10_000)
	sourceOutput.OutPointStr = sourceOutpoint
	sourceOutput.OutValue.PkScript = senderScript
	rpc.outputs[sourceOutpoint] = sourceOutput
	plainOutput := indexer.NewTxOutput(100_000)
	plainOutput.OutPointStr = plainOutpoint
	plainOutput.OutValue.PkScript = senderScript
	rpc.outputs[plainOutpoint] = plainOutput
	rpc.plain = []*indexerwire.TxOutputInfo{
		{OutPoint: sourceOutpoint, Value: 10_000, PkScript: senderScript},
		{OutPoint: plainOutpoint, Value: 100_000, PkScript: senderScript},
	}

	sender := newRGB11FlowManager(t, senderWallet, rpc, evidence, 101)
	recipient := newRGB11FlowManager(t, recipientWallet, rpc, evidence, 102)
	contract, err := os.ReadFile("../../../rgb11/testvectors/rc11/nia-example.rgba")
	if err != nil {
		t.Fatal(err)
	}
	imported, err := sender.ImportRGB11Contract(context.Background(), contract)
	if err != nil {
		t.Fatal(err)
	}
	remote := newRGB11MemoryDKVSHTTP()
	client := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	recordOptions := dkvsindexer.RecordOptions{TTL: uint64((24 * time.Hour) / time.Millisecond)}

	request := RGB11AddressSendRequest{
		ReceiverAddress:  unregisteredWallet.GetAddress(),
		AssetName:        imported.AssetName,
		AmountRaw:        "20000",
		FeeRate:          2,
		MinConfirmations: 1,
	}
	if _, _, err := sender.PrepareRGB11AddressTransfer(context.Background(), client, request,
		dkvsindexer.RecordVerificationOptions{}); !errors.Is(err, ErrRGB11TraditionalReceiveRequired) {
		t.Fatalf("unregistered receiver err=%v", err)
	}

	endpoint, err := recipient.EnableRGB11AddressReceive(client, RGB11ReceiveCapabilityOptions{
		RecordOptions: recordOptions,
	})
	if err != nil {
		t.Fatal(err)
	}
	capabilityRecord, err := client.GetRecord(endpoint.CapabilityRecordKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(capabilityRecord.Value) != 2 || len(capabilityRecord.PubKey) != 0 || capabilityRecord.Version != dkvsindexer.Version {
		t.Fatalf("capability value=%x pubkey=%x version=%d", capabilityRecord.Value, capabilityRecord.PubKey, capabilityRecord.Version)
	}

	request.ReceiverAddress = recipientWallet.GetAddress()
	prepared, resolved, err := sender.PrepareRGB11AddressTransfer(context.Background(), client, request,
		dkvsindexer.RecordVerificationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.State == nil || !prepared.State.AddressMode || prepared.State.Invoice != "" ||
		!prepared.State.SyntheticInvoiceRemoved || resolved.AccountID != endpoint.AccountID {
		t.Fatalf("prepared address transfer=%+v endpoint=%+v", prepared.State, resolved)
	}
	if _, err := sender.BroadcastRGB11AddressTransfer(prepared.State.TransferID); !errors.Is(err, ErrRGB11AddressDeliveryRequired) {
		t.Fatalf("broadcast before delivery err=%v", err)
	}

	deliveryOptions := RGB11AddressDeliveryOptions{RecordOptions: recordOptions, InlineLimit: 1}
	firstDelivery, err := sender.DeliverRGB11AddressTransfer(client, prepared.State.TransferID, deliveryOptions)
	if err != nil {
		t.Fatal(err)
	}
	if firstDelivery.Mode != "blob" || !firstDelivery.Temporary {
		t.Fatalf("first delivery=%+v", firstDelivery)
	}
	firstRecord, err := client.GetRecord(firstDelivery.RecordKey)
	if err != nil {
		t.Fatal(err)
	}
	secondDelivery, err := sender.DeliverRGB11AddressTransfer(client, prepared.State.TransferID, deliveryOptions)
	if err != nil {
		t.Fatal(err)
	}
	secondRecord, err := client.GetRecord(secondDelivery.RecordKey)
	if err != nil {
		t.Fatal(err)
	}
	if secondRecord.Seq != firstRecord.Seq+1 {
		t.Fatalf("delivery seq first=%d second=%d", firstRecord.Seq, secondRecord.Seq)
	}

	snapshot, err := sender.rgbManager.projectionStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	foundPending := false
	for _, record := range snapshot {
		if record.Key != "pending-"+prepared.State.TransferID {
			continue
		}
		foundPending = true
		var pending rgb11wallet.PendingTransfer
		if err := decode(record.Value, &pending); err != nil {
			t.Fatal(err)
		}
		if len(pending.RecipientConsignment) != 0 || len(pending.LocalConsignment) == 0 {
			t.Fatalf("snapshot delivery=%d local=%d", len(pending.RecipientConsignment), len(pending.LocalConsignment))
		}
	}
	if !foundPending {
		t.Fatal("address pending transfer absent from snapshot")
	}

	witnessTxID, err := sender.BroadcastRGB11AddressTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	witness := wire.NewMsgTx(wire.TxVersion)
	if err := witness.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	if witness.TxHash().String() != witnessTxID || len(baseEvidence.broadcasted) == 0 {
		t.Fatalf("broadcast witness=%s returned=%s", witness.TxHash(), witnessTxID)
	}
	recipientOutpoint := fmt.Sprintf("%s:%d", witnessTxID, pending.State.RecipientVout)
	changeOutpoint := fmt.Sprintf("%s:%d", witnessTxID, len(witness.TxOut)-1)
	baseEvidence.mu.Lock()
	baseEvidence.rawTx[witnessTxID] = append([]byte(nil), pending.SignedTx...)
	baseEvidence.spendingTx[sourceOutpoint] = witnessTxID
	baseEvidence.utxos[recipientOutpoint] = &rgb11wallet.BitcoinUTXO{
		OutPoint: recipientOutpoint, Value: witness.TxOut[pending.State.RecipientVout].Value,
		PkScript: append([]byte(nil), witness.TxOut[pending.State.RecipientVout].PkScript...),
	}
	baseEvidence.utxos[changeOutpoint] = &rgb11wallet.BitcoinUTXO{
		OutPoint: changeOutpoint, Value: witness.TxOut[len(witness.TxOut)-1].Value,
		PkScript: append([]byte(nil), witness.TxOut[len(witness.TxOut)-1].PkScript...),
	}
	baseEvidence.mu.Unlock()
	evidence.setStatus(witnessTxID, rgb11wallet.BitcoinTxStatus{TxID: witnessTxID, InMempool: true})

	deliveryRecord, err := client.GetRecord(secondDelivery.RecordKey)
	if err != nil {
		t.Fatal(err)
	}
	senderID, err := dkvsAccountID(senderWallet)
	if err != nil {
		t.Fatal(err)
	}
	forgedTransferID := strings.Repeat("a", 64)
	if forgedTransferID == prepared.State.TransferID {
		forgedTransferID = strings.Repeat("b", 64)
	}
	forgedKey, err := dkvsindexer.MailMsgKey(endpoint.AccountID, senderID, forgedTransferID)
	if err != nil {
		t.Fatal(err)
	}
	forged, err := NewDKVSAccountSignedRecord(senderWallet, forgedKey, deliveryRecord.Value,
		dkvsindexer.RecordOptions{Seq: 1, TTL: recordOptions.TTL})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := recipient.AcceptRGB11AddressMailbox(context.Background(), client, forged,
		dkvsindexer.RecordVerificationOptions{Now: forged.IssueTime}, deliveryOptions); !errors.Is(err, ErrRGB11AddressMailbox) {
		t.Fatalf("replayed consignment err=%v", err)
	}

	receipt, ackRecord, err := recipient.AcceptRGB11AddressMailbox(context.Background(), client, deliveryRecord,
		dkvsindexer.RecordVerificationOptions{Now: deliveryRecord.IssueTime}, deliveryOptions)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.TransferID != prepared.State.TransferID || ackRecord == nil {
		t.Fatalf("receipt=%+v ack=%+v", receipt, ackRecord)
	}
	locked := recipient.utxoLockerL1.GetLockedUtxoList()
	if locked[recipientOutpoint] == nil || locked[recipientOutpoint].Reason != rgb11wallet.LockReasonPending {
		t.Fatalf("mempool lock=%+v", locked[recipientOutpoint])
	}
	if _, err := sender.AcceptRGB11AddressACK(ackRecord,
		dkvsindexer.RecordVerificationOptions{Now: ackRecord.IssueTime}); err != nil {
		t.Fatal(err)
	}
	pending, err = sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if !pending.State.DeliveryAcknowledged || len(pending.RecipientConsignment) == 0 || pending.State.DeliveryCacheCompacted {
		t.Fatalf("pre-confirmation sender state=%+v consignment=%d", pending.State, len(pending.RecipientConsignment))
	}

	evidence.setStatus(witnessTxID, rgb11wallet.BitcoinTxStatus{
		TxID: witnessTxID, Confirmed: true, Confirmations: 1,
	})
	baseEvidence.mu.Lock()
	baseEvidence.utxos[recipientOutpoint].Confirmations = 1
	baseEvidence.utxos[changeOutpoint].Confirmations = 1
	baseEvidence.mu.Unlock()
	if _, err := recipient.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := sender.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	locked = recipient.utxoLockerL1.GetLockedUtxoList()
	if locked[recipientOutpoint] == nil || locked[recipientOutpoint].Reason != rgb11wallet.LockReasonRGB {
		t.Fatalf("confirmed lock=%+v", locked[recipientOutpoint])
	}
	pending, err = sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if !pending.State.DeliveryCacheCompacted || len(pending.RecipientConsignment) != 0 || len(pending.LocalConsignment) == 0 {
		t.Fatalf("final sender state=%+v recipient=%d local=%d", pending.State,
			len(pending.RecipientConsignment), len(pending.LocalConsignment))
	}

	ackValue, err := encodeRGB11AddressACK(RGB11AddressACK{Status: RGB11AddressACKNeedResend})
	if err != nil {
		t.Fatal(err)
	}
	ackKey := pending.State.AckRecordKey
	previousACK, err := client.GetRecord(ackKey)
	if err != nil {
		t.Fatal(err)
	}
	updatedACK, err := client.SendAccountMailboxMessage(recipientWallet, senderID, prepared.State.TransferID,
		ackValue, nextRGB11AddressRecordOptions(client, []string{ackKey}, recordOptions), nil)
	if err != nil {
		t.Fatal(err)
	}
	if updatedACK.Seq != previousACK.Seq+1 {
		t.Fatalf("ACK seq previous=%d updated=%d", previousACK.Seq, updatedACK.Seq)
	}

	if _, err := hex.DecodeString(prepared.State.TransferID); err != nil {
		t.Fatalf("transfer ID is not canonical hex: %v", err)
	}
}
