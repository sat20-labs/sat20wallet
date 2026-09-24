package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type rgb11AddressEvidence struct {
	*rgb11FlowEvidence
	statusMu sync.Mutex
	statuses map[string]*rgb11wallet.BitcoinTxStatus
}

type rgb11ReadFault struct {
	rgb11wallet.BitcoinEvidenceProvider
	txid, outpoint string
	err            error
}

func (e *rgb11ReadFault) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if txid == e.txid {
		return nil, e.err
	}
	return e.BitcoinEvidenceProvider.GetTxStatus(txid)
}

func (e *rgb11ReadFault) GetUTXO(outpoint string) (*rgb11wallet.BitcoinUTXO, error) {
	if outpoint == e.outpoint {
		return nil, e.err
	}
	return e.BitcoinEvidenceProvider.GetUTXO(outpoint)
}

func mustRGB11ConfiguredStore(t *testing.T, manager *Manager) *dkvsStore {
	t.Helper()
	store, err := manager.rgbManager.configuredRGB11Store()
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func countRGB11Receives(t *testing.T, db indexer.KVDB) int {
	t.Helper()
	count := 0
	err := db.BatchRead([]byte("rgb11-engine-"), false, func(key, _ []byte) error {
		if bytes.Contains(key, []byte("-wallet/receive/")) {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func nextRGB11AddressRecordOptions(client *SatsNetDKVSClient, keys []string,
	opts dkvsindexer.RecordOptions) dkvsindexer.RecordOptions {

	if opts.IssueHeight == 0 {
		opts.IssueHeight = uint64(time.Now().UnixMilli())
	}
	if opts.Seq != 0 {
		return opts
	}
	var maxSeq uint64
	for _, key := range keys {
		if key == "" || client == nil {
			continue
		}
		existing, err := client.GetRecord(key)
		if err == nil && existing != nil && existing.Seq > maxSeq {
			maxSeq = existing.Seq
		}
	}
	opts.Seq = maxSeq + 1
	return opts
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
	inline, err := rgb11wallet.EncodeAddressEnvelope(rgb11AddressEnvelopeInline, []byte("ciphertext"))
	if err != nil || len(inline) != 2+len("ciphertext") {
		t.Fatalf("inline envelope len=%d err=%v", len(inline), err)
	}
	mode, payload, err := rgb11wallet.DecodeAddressEnvelope(inline)
	if err != nil || mode != rgb11AddressEnvelopeInline || string(payload) != "ciphertext" {
		t.Fatalf("inline decode mode=%d payload=%q err=%v", mode, payload, err)
	}
	blob, err := rgb11wallet.EncodeAddressEnvelope(rgb11AddressEnvelopeBlob, nil)
	if err != nil || len(blob) != 2 {
		t.Fatalf("blob envelope len=%d err=%v", len(blob), err)
	}
	if _, _, err := rgb11wallet.DecodeAddressEnvelope([]byte{9, 9}); err == nil {
		t.Fatal("invalid envelope accepted")
	}

	encodedACK, err := rgb11wallet.EncodeAddressACK(RGB11AddressACK{Status: RGB11AddressACKAccepted, Code: 7})
	if err != nil || len(encodedACK) != 4 {
		t.Fatalf("ACK len=%d err=%v", len(encodedACK), err)
	}
	decodedACK, err := rgb11wallet.DecodeAddressACK(encodedACK)
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

func TestRGB11AddressMailboxUsesMessageManagerSenderSequence(t *testing.T) {
	senderWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	receiverWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
	)
	if senderWallet == nil || receiverWallet == nil {
		t.Fatal("create message wallets")
	}
	remote := newRGB11MemoryDKVSHTTP()
	messageClient := newRGB11MessageNodeClient(remote)
	newManager := func(wallet *InternalWallet) *Manager {
		database := indexerdb.NewKVDB(t.TempDir())
		if database == nil {
			t.Fatal("create test database")
		}
		t.Cleanup(func() { _ = database.Close() })
		manager := &Manager{
			db: database, wallet: wallet,
			walletInfoMap: map[int64]*WalletInfo{wallet.GetId(): {
				WalletInDB: WalletInDB{Id: wallet.GetId(), Accounts: 1, Type: WALLET_TYPE_MNEMONIC}, Wallet: wallet,
			}},
			cfg: &sdkcommon.Config{Env: "test", Chain: "testnet", IndexerL2: &sdkcommon.Indexer{
				Scheme: "http", Host: "dkvs.test", Proxy: "testnet",
			}},
			http: remote,
		}
		manager.serverNode = NewNode(messageClient, "message.test", SERVER_NODE,
			messageClient.CoreNodePubKey(), messageClient.CoreNodePubKey())
		return manager
	}
	sender := newManager(senderWallet)
	receiver := newManager(receiverWallet)
	receiverID, err := dkvsAccountID(receiverWallet)
	if err != nil {
		t.Fatal(err)
	}
	if err := receiver.bindAccountToCurrentCoreNode(receiverWallet); err != nil {
		t.Fatal(err)
	}
	first, err := sender.sendWalletDirectMessage(senderWallet, strings.Repeat("1", 64),
		AccountMessageKindRGB11Consignment, receiverID, []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := sender.sendWalletDirectMessage(senderWallet, strings.Repeat("2", 64),
		AccountMessageKindRGB11Consignment, receiverID, []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	if first.SenderMsgID != 0 || second.SenderMsgID != 1 {
		t.Fatalf("sender sequence first=%d second=%d", first.SenderMsgID, second.SenderMsgID)
	}
	firstKey, _ := dkvsindexer.MailMsgKey(receiverID, first.SenderAccount, first.MessageID)
	secondKey, _ := dkvsindexer.MailMsgKey(receiverID, second.SenderAccount, second.MessageID)
	firstRecord, err := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote).GetRecord(firstKey)
	if err != nil {
		t.Fatal(err)
	}
	secondRecord, err := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote).GetRecord(secondKey)
	if err != nil {
		t.Fatal(err)
	}
	if firstRecord.Seq != 1 || secondRecord.Seq != 1 || firstRecord.Key == secondRecord.Key {
		t.Fatalf("immutable mailbox records first=%+v second=%+v", firstRecord, secondRecord)
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
	messageClient := newRGB11MessageNodeClient(remote)
	configure := func(manager *Manager) {
		manager.cfg = &sdkcommon.Config{
			Env: "test", Chain: "testnet",
			IndexerL2: &sdkcommon.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"},
		}
		manager.http = remote
		manager.serverNode = NewNode(
			messageClient, "message.test", SERVER_NODE,
			messageClient.CoreNodePubKey(), messageClient.CoreNodePubKey(),
		)
	}
	configure(sender)
	configure(recipient)
	recordOptions := dkvsindexer.RecordOptions{TTL: testRGB11FreeLocalTTL}

	request := RGB11AddressSendRequest{
		ReceiverAddress:  unregisteredWallet.GetAddress(),
		AssetName:        imported.AssetName,
		AmountRaw:        "20000",
		FeeRate:          2,
		MinConfirmations: 1,
	}
	if _, _, err := sender.PrepareConfiguredRGB11AddressTransfer(context.Background(), request,
		dkvsindexer.RecordVerificationOptions{}); !errors.Is(err, ErrRGB11TraditionalReceiveRequired) {
		t.Fatalf("unregistered receiver err=%v", err)
	}

	endpoint, err := recipient.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{
		RecordOptions: recordOptions,
	})
	if err != nil {
		t.Fatal(err)
	}
	mailboxTarget, err := mailboxSubscriptionTarget(endpoint.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := recipient.SubscribeDKVSPrefix(mailboxTarget); err != nil {
		t.Fatal(err)
	}
	capabilityRecord, err := client.GetRecord(endpoint.CapabilityRecordKey)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, decodeErr := dkvsindexer.DecodeAccountServiceDescriptor(capabilityRecord.Value)
	if decodeErr != nil || descriptor.AccountID != endpoint.AccountID ||
		descriptor.Capabilities&dkvsindexer.AccountServiceCapabilityRGB11Direct == 0 ||
		len(capabilityRecord.PubKey) != 0 || capabilityRecord.Version != dkvsindexer.Version {
		t.Fatalf("capability value=%x pubkey=%x version=%d", capabilityRecord.Value, capabilityRecord.PubKey, capabilityRecord.Version)
	}

	request.ReceiverAddress = recipientWallet.GetAddress()
	prepared, resolved, err := sender.PrepareConfiguredRGB11AddressTransfer(context.Background(), request,
		dkvsindexer.RecordVerificationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.State == nil || !prepared.State.AddressMode || prepared.State.Invoice != "" ||
		!prepared.State.SyntheticInvoiceRemoved || resolved.AccountID != endpoint.AccountID {
		t.Fatalf("prepared address transfer=%+v endpoint=%+v", prepared.State, resolved)
	}
	if decoded, err := hex.DecodeString(prepared.State.AddressMessageID); err != nil || len(decoded) != 32 {
		t.Fatalf("address message ID=%q err=%v", prepared.State.AddressMessageID, err)
	}
	if _, err := sender.BroadcastRGB11AddressTransfer(prepared.State.TransferID); !errors.Is(err, ErrRGB11AddressDeliveryRequired) {
		t.Fatalf("broadcast before delivery err=%v", err)
	}

	senderID, err := dkvsAccountID(senderWallet)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < rgb11AddressMailboxPageSize; i++ {
		messageID := fmt.Sprintf("%064x", i)
		key, err := dkvsindexer.MailMsgKey(endpoint.AccountID, senderID, messageID)
		if err != nil {
			t.Fatal(err)
		}
		record := &swire.DKVSRecord{
			Version: dkvsindexer.Version, Key: key, Value: []byte("invalid"),
			Seq: 1, IssueHeight: 1, TTL: testRGB11FreeLocalTTL,
		}
		proof, err := dkvsindexer.NewFreeLocalFeeProof(
			record.Key, "mail", uint32(dkvsindexer.RecordSize(record)),
			record.IssueHeight+record.TTL,
		)
		if err != nil {
			t.Fatal(err)
		}
		record.FeeProof, err = dkvsindexer.EncodeFeeProof(proof)
		if err != nil {
			t.Fatal(err)
		}
		remote.seedInternalMailboxRecord(record)
	}
	deliveryOptions := RGB11AddressDeliveryOptions{RecordOptions: recordOptions, InlineLimit: 1}
	firstDelivery, err := sender.rgbManager.deliverRGB11AddressTransferStore(
		mustRGB11ConfiguredStore(t, sender), prepared.State.TransferID, deliveryOptions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if firstDelivery.Mode != "direct" || !firstDelivery.Temporary {
		t.Fatalf("first delivery=%+v", firstDelivery)
	}
	firstRecord, err := client.GetRecord(firstDelivery.RecordKey)
	if err != nil {
		t.Fatal(err)
	}
	secondDelivery, err := sender.rgbManager.deliverRGB11AddressTransferStore(
		mustRGB11ConfiguredStore(t, sender), prepared.State.TransferID, deliveryOptions,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondRecord, err := client.GetRecord(secondDelivery.RecordKey)
	if err != nil {
		t.Fatalf("second delivery=%+v first=%+v err=%v", secondDelivery, firstDelivery, err)
	}
	if secondRecord.Seq != firstRecord.Seq ||
		dkvsindexer.RecordHash(secondRecord) != dkvsindexer.RecordHash(firstRecord) {
		t.Fatalf("idempotent delivery changed record: first=%d second=%d",
			firstRecord.Seq, secondRecord.Seq)
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
		if err := sender.rgbManager.projectionStore.ImportSnapshot(snapshot); err != nil {
			t.Fatal(err)
		}
		pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending.RecipientConsignment) != 0 || len(pending.LocalConsignment) == 0 {
			t.Fatalf("snapshot delivery=%d local=%d", len(pending.RecipientConsignment), len(pending.LocalConsignment))
		}
	}
	if !foundPending {
		t.Fatal("address pending transfer absent from snapshot")
	}

	if _, err := sender.BroadcastRGB11AddressTransfer(prepared.State.TransferID); !errors.Is(err, ErrRGB11AddressDeliveryRequired) {
		t.Fatalf("address transfer broadcast before ACK: %v", err)
	}
	deliveryRecord, err := client.GetRecord(secondDelivery.RecordKey)
	if err != nil {
		t.Fatalf("post-broadcast delivery=%+v err=%v", secondDelivery, err)
	}
	forgedTransferID := strings.Repeat("a", 64)
	if forgedTransferID == prepared.State.AddressMessageID {
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
	recipientStore := mustRGB11ConfiguredStore(t, recipient)
	if _, _, err := recipient.rgbManager.acceptRGB11AddressMailboxStore(
		context.Background(), recipientStore, cloneDKVSValue(forged),
		deliveryOptions,
	); !errors.Is(err, ErrRGB11AddressMailbox) {
		t.Fatalf("replayed consignment err=%v", err)
	}

	firstSync, err := recipient.SyncConfiguredRGB11AddressMailbox(
		context.Background(), dkvsindexer.RecordVerificationOptions{}, deliveryOptions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if firstSync.Scanned != rgb11AddressMailboxPageSize || firstSync.Received != 0 ||
		firstSync.Invalid != rgb11AddressMailboxPageSize {
		t.Fatalf("first mailbox page=%+v", firstSync)
	}
	savedNode := recipient.serverNode
	recipient.serverNode = nil
	failedSync, err := recipient.SyncConfiguredRGB11AddressMailbox(
		context.Background(), dkvsindexer.RecordVerificationOptions{}, deliveryOptions,
	)
	recipient.serverNode = savedNode
	if err != nil {
		t.Fatal(err)
	}
	if failedSync.Received != 0 || failedSync.Invalid == 0 {
		t.Fatalf("failed ACK sync=%+v", failedSync)
	}
	receivesBeforeRetry := countRGB11Receives(t, recipient.db)
	if receivesBeforeRetry != 1 {
		t.Fatalf("receive records after failed ACK=%d", receivesBeforeRetry)
	}
	syncResult, err := recipient.SyncConfiguredRGB11AddressMailbox(
		context.Background(), dkvsindexer.RecordVerificationOptions{}, deliveryOptions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if syncResult.Received != 1 || syncResult.ACKs != 0 {
		t.Fatalf("mailbox sync=%+v", syncResult)
	}
	if receivesAfterRetry := countRGB11Receives(t, recipient.db); receivesAfterRetry != receivesBeforeRetry {
		t.Fatalf("retry created receive records: before=%d after=%d", receivesBeforeRetry, receivesAfterRetry)
	}
	stagedReceive, err := recipient.rgbManager.projectionStore.LoadTransferState(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stagedReceive.TransferID) == 64 || stagedReceive.Status != "awaiting_broadcast" ||
		stagedReceive.AckStatus != "ack-sent" || !stagedReceive.AddressMode ||
		!stagedReceive.DeliveryAcknowledged || stagedReceive.Invoice != "" ||
		!stagedReceive.SyntheticInvoiceRemoved || len(stagedReceive.OutputOutPoints) != 1 {
		t.Fatalf("invalid staged Direct receive: %+v", stagedReceive)
	}
	requestID, err := recipient.rgbManager.projectionStore.LoadPreparedReceive(stagedReceive.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	privateReceive, err := recipient.rgbManager.engine.LoadReceive(requestID)
	if err != nil {
		t.Fatal(err)
	}
	if privateReceive.TransferID != stagedReceive.TransferID || privateReceive.Invoice == "" ||
		privateReceive.ObjectHash != stagedReceive.ConsignmentHash ||
		privateReceive.WitnessTxID != stagedReceive.WitnessTxID {
		t.Fatalf("invalid private Direct receive: %+v", privateReceive)
	}
	directMessages, err := sender.readWalletDirectMessages(senderWallet)
	if err != nil {
		t.Fatal(err)
	}
	var ackRecord *swire.DKVSRecord
	for _, item := range directMessages {
		if item != nil && item.Payload != nil &&
			item.Payload.Kind == AccountMessageKindRGB11ACK &&
			item.Payload.ApplicationID == prepared.State.AddressMessageID {
			ackRecord = item.Record
			break
		}
	}
	if ackRecord == nil {
		t.Fatalf("mailbox sync did not emit ACK: messages=%d", len(directMessages))
	}
	preparedOutpoint := fmt.Sprintf("%s:%d", prepared.State.WitnessTxID, prepared.State.RecipientVout)
	locked := recipient.utxoLockerL1.GetLockedUtxoList()
	if locked[preparedOutpoint] != nil {
		t.Fatalf("prepared output was locked before broadcast: %+v", locked[preparedOutpoint])
	}
	if _, err := sender.AcceptRGB11AddressACK(ackRecord,
		dkvsindexer.RecordVerificationOptions{}); err != nil {
		t.Fatal(err)
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
	pending, err = sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if !pending.State.DeliveryAcknowledged || pending.State.DeliveryCacheCompacted {
		t.Fatalf("pre-confirmation sender state=%+v", pending.State)
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
	received, err := recipient.rgbManager.projectionStore.LoadTransferState(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if !received.AddressMode || received.TransportMode != RGB11AddressTransport ||
		received.AddressMessageID != prepared.State.AddressMessageID ||
		received.SenderAccountID != senderID || received.ReceiverAccountID != endpoint.AccountID ||
		!received.SyntheticInvoiceRemoved {
		t.Fatalf("receiver identity lost after promotion: %+v", received)
	}
	if _, err := sender.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lock := sender.utxoLockerL1.GetLockedUtxoList()[sourceOutpoint]; lock != nil {
		t.Fatalf("settled address transfer retained spent-input lock: %+v", lock)
	}
	expectedSpends, err := sender.rgbManager.rgb11ExpectedInputs()
	if err != nil || expectedSpends[sourceOutpoint] != witnessTxID {
		t.Fatalf("settled address transfer lost expected spend: txid=%q err=%v",
			expectedSpends[sourceOutpoint], err)
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

	replayRemote := newRGB11MemoryDKVSHTTP()
	for key, record := range remote.records {
		if strings.HasPrefix(key, "/account/") {
			replayRemote.records[key] = cloneRGB11DKVSRecord(record)
			continue
		}
		direct, decodeErr := decodeWalletDirectRecord(recipientWallet, record)
		if decodeErr == nil && direct.Payload.Kind == AccountMessageKindRGB11Consignment &&
			direct.Payload.ApplicationID == prepared.State.AddressMessageID {
			replayRemote.records[key] = cloneRGB11DKVSRecord(record)
		}
	}
	mailboxRecords := 0
	for key := range replayRemote.records {
		if strings.HasPrefix(key, "/mail/") {
			mailboxRecords++
		}
	}
	if mailboxRecords != 1 {
		t.Fatalf("replay mailbox records=%d", mailboxRecords)
	}
	restoredRecipient := newRGB11FlowManager(t, recipientWallet.Clone(), rpc, evidence, 103)
	configureRGB11DKVSTestManager(restoredRecipient, replayRemote)
	replayClient := newRGB11MessageNodeClient(replayRemote)
	restoredRecipient.serverNode = NewNode(
		replayClient, "message.test", SERVER_NODE,
		replayClient.CoreNodePubKey(), replayClient.CoreNodePubKey(),
	)
	if err := restoredRecipient.bindAccountToCurrentCoreNode(restoredRecipient.wallet); err != nil {
		t.Fatal(err)
	}
	if err := restoredRecipient.SubscribeDKVSPrefix(mailboxTarget); err != nil {
		t.Fatal(err)
	}
	recoveryResult, err := restoredRecipient.SyncConfiguredRGB11AddressMailbox(
		context.Background(), dkvsindexer.RecordVerificationOptions{}, deliveryOptions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if recoveryResult.Received != 1 {
		t.Fatalf("mnemonic replay result=%+v", recoveryResult)
	}
	if _, err := restoredRecipient.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	recoveredState, err := restoredRecipient.rgbManager.projectionStore.LoadTransferState(
		prepared.State.TransferID)
	if err != nil || recoveredState.Status != "settled" {
		t.Fatalf("mnemonic replay state=%+v err=%v", recoveredState, err)
	}

	beforeState, err := recipient.rgbManager.projectionStore.LoadTransferState(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	beforeProof, err := recipient.rgbManager.projectionStore.LoadProof(recipientOutpoint, imported.AssetName)
	if err != nil {
		t.Fatal(err)
	}
	beforeLock := recipient.utxoLockerL1.GetLockedUtxoList()[recipientOutpoint]
	readErr := errors.New("RGB11 evidence unavailable")
	recipient.rgbManager.evidence = &rgb11ReadFault{
		BitcoinEvidenceProvider: evidence, txid: witnessTxID, outpoint: recipientOutpoint, err: readErr,
	}
	result, refreshErr := recipient.RefreshRGB11State(context.Background())
	recipient.rgbManager.evidence = evidence
	if !errors.Is(refreshErr, readErr) || result == nil || result.Reorged != 0 || result.Unresolved == 0 {
		t.Fatalf("read fault result=%+v err=%v", result, refreshErr)
	}
	afterState, err := recipient.rgbManager.projectionStore.LoadTransferState(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	afterProof, err := recipient.rgbManager.projectionStore.LoadProof(recipientOutpoint, imported.AssetName)
	if err != nil {
		t.Fatal(err)
	}
	afterLock := recipient.utxoLockerL1.GetLockedUtxoList()[recipientOutpoint]
	if !reflect.DeepEqual(beforeState, afterState) || !reflect.DeepEqual(beforeProof, afterProof) ||
		!reflect.DeepEqual(beforeLock, afterLock) {
		t.Fatal("read fault changed settled receive state")
	}

	// ACK is now a normal MessageManager Direct message. The transport sender
	// sequence is independent from the RGB11 transfer/application ID.
	ackDirect, err := verifyAccountDirectRecord(senderID, ackRecord)
	if err != nil {
		t.Fatal(err)
	}
	recipientID, err := dkvsAccountID(recipientWallet)
	if err != nil {
		t.Fatal(err)
	}
	if ackDirect.SenderAccount != recipientID || ackDirect.RecipientAccount != senderID ||
		ackDirect.SenderMsgID != 0 {
		t.Fatalf("unexpected MessageManager ACK envelope: %+v", ackDirect)
	}

	if _, err := hex.DecodeString(prepared.State.AddressMessageID); err != nil {
		t.Fatalf("address message ID is not canonical hex: %v", err)
	}
}

func TestRGB11AddressNACKCancelsBeforeBroadcast(t *testing.T) {
	fixture := newExpiredCancelFixture(t, 1)
	pending, err := fixture.manager.rgbManager.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	receiverWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch",
		"", &chaincfg.TestNet4Params,
	)
	senderID, err := dkvsAccountID(fixture.manager.wallet)
	if err != nil {
		t.Fatal(err)
	}
	receiverID, err := dkvsAccountID(receiverWallet)
	if err != nil {
		t.Fatal(err)
	}
	messageID := strings.Repeat("ab", 32)
	pending.State.AddressMode = true
	pending.State.TransportMode = RGB11AddressTransport
	pending.State.Status = "delivered"
	pending.State.AckStatus = "awaiting-persistence"
	pending.State.SenderAccountID = senderID
	pending.State.ReceiverAccountID = receiverID
	pending.State.AddressMessageID = messageID
	if err := fixture.manager.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}
	ack, err := fixture.manager.rgbManager.acceptRGB11AddressACKDecoded(
		senderID, receiverID, messageID,
		RGB11AddressACK{Status: RGB11AddressACKRejected},
	)
	if err != nil || ack == nil || ack.Status != RGB11AddressACKRejected {
		t.Fatalf("reject ACK result=%+v err=%v", ack, err)
	}
	stored, err := fixture.manager.rgbManager.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
	if err != nil || stored.State.Status != "rejected" || stored.State.RejectReason != "recipient-rejected" {
		t.Fatalf("address NACK state=%+v err=%v", stored, err)
	}
	if lock := fixture.manager.utxoLockerL1.GetLockedUtxoList()[fixture.input]; lock != nil {
		t.Fatalf("address NACK left input locked: %+v", lock)
	}
}
