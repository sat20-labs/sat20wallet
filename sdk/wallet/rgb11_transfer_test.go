package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/rgb11/consensus"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/invoicing"
	coreissuance "github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/operations"
	"github.com/sat20-labs/rgb11/rejectlist"
	corerelay "github.com/sat20-labs/rgb11/relay"
	"github.com/sat20-labs/rgb11/seals"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type rgb11FlowIndexer struct {
	IndexerRPCClient
	outputs map[string]*TxOutput
	plain   []*indexerwire.TxOutputInfo
}

func (f *rgb11FlowIndexer) GetTxOutput(outpoint string) (*TxOutput, error) {
	output := f.outputs[outpoint]
	if output == nil {
		return nil, fmt.Errorf("unknown test outpoint %s", outpoint)
	}
	return output.Clone(), nil
}

func (f *rgb11FlowIndexer) GetUtxoListWithTicker(string, *indexer.AssetName) []*indexerwire.TxOutputInfo {
	result := make([]*indexerwire.TxOutputInfo, 0, len(f.plain))
	for _, output := range f.plain {
		copy := *output
		copy.PkScript = append([]byte(nil), output.PkScript...)
		result = append(result, &copy)
	}
	return result
}

type rgb11FlowEvidence struct {
	mu          sync.Mutex
	utxos       map[string]*rgb11wallet.BitcoinUTXO
	rawTx       map[string][]byte
	spendingTx  map[string]string
	broadcasted []byte
}

func (e *rgb11FlowEvidence) GetUTXO(outpoint string) (*rgb11wallet.BitcoinUTXO, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	utxo := e.utxos[outpoint]
	if utxo == nil {
		return nil, fmt.Errorf("unknown test UTXO %s", outpoint)
	}
	copy := *utxo
	copy.PkScript = append([]byte(nil), utxo.PkScript...)
	return &copy, nil
}

func (e *rgb11FlowEvidence) GetRawTx(txid string) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	raw := e.rawTx[txid]
	if len(raw) == 0 {
		return nil, fmt.Errorf("unknown test transaction %s", txid)
	}
	return append([]byte(nil), raw...), nil
}

func (e *rgb11FlowEvidence) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.rawTx[txid]) == 0 {
		return nil, fmt.Errorf("unknown test transaction %s", txid)
	}
	return &rgb11wallet.BitcoinTxStatus{TxID: txid, InMempool: true}, nil
}

func (e *rgb11FlowEvidence) GetOutspend(outpoint string) (*rgb11wallet.BitcoinOutspend, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	spendingTx := e.spendingTx[outpoint]
	return &rgb11wallet.BitcoinOutspend{Spent: spendingTx != "", SpendingTx: spendingTx}, nil
}

func (e *rgb11FlowEvidence) GetTip() (*rgb11wallet.BitcoinTip, error) {
	return &rgb11wallet.BitcoinTip{Height: 1, BlockHash: "test-tip"}, nil
}

func (e *rgb11FlowEvidence) Broadcast(raw []byte) (string, error) {
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		return "", err
	}
	e.mu.Lock()
	e.broadcasted = append([]byte(nil), raw...)
	e.mu.Unlock()
	return tx.TxHash().String(), nil
}

func newRGB11FlowManager(t *testing.T, wallet common.Wallet, rpc IndexerRPCClient,
	evidence rgb11wallet.BitcoinEvidenceProvider, localWalletID int64) *Manager {
	t.Helper()
	database := indexerdb.NewKVDB(t.TempDir())
	t.Cleanup(func() { database.Close() })
	locker := NewUtxoLocker(database, rpc, L1_NETWORK_BITCOIN)
	l1 := NewIndexerRPCClientMgr()
	l1.Set(rpc)
	manager := &Manager{
		db: database, wallet: wallet, status: &Status{CurrentWallet: localWalletID, CurrentAccount: 0},
		tickerInfoMap: make(map[string]*indexer.TickerInfo), utxoLockerL1: locker,
		l1IndexerClient: l1,
	}
	rgbManager, err := newRGB11Manager(manager, database, locker, evidence)
	if err != nil {
		t.Fatal(err)
	}
	rgbManager.consistencyStatus = "ok"
	manager.rgbManager = rgbManager
	if err := manager.rgbManager.selectRGB11Scope(); err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestRGB11OfficialContractOpretRelayAckSendReceive(t *testing.T) {
	senderWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	recipientWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
	)
	if senderWallet == nil || recipientWallet == nil {
		t.Fatal("create RGB11 test wallets")
	}
	senderScript, err := AddrToPkScript(senderWallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	recipientScript, err := AddrToPkScript(recipientWallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}

	const sourceOutpoint = "14295d5bb1a191cdb6286dc0944df938421e3dfcbf0811353ccac4100c2068c5:1"
	const plainOutpoint = "1111111111111111111111111111111111111111111111111111111111111111:0"
	evidence := &rgb11FlowEvidence{
		utxos: map[string]*rgb11wallet.BitcoinUTXO{
			sourceOutpoint: {OutPoint: sourceOutpoint, Value: 10_000, PkScript: senderScript, Confirmations: 6},
		},
		rawTx:      make(map[string][]byte),
		spendingTx: make(map[string]string),
	}
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

	sender := newRGB11FlowManager(t, senderWallet, rpc, evidence, 1)
	recipient := newRGB11FlowManager(t, recipientWallet, rpc, evidence, 2)
	contract, err := os.ReadFile("../../../rgb11/testvectors/rc11/nia-example.rgba")
	if err != nil {
		t.Fatal(err)
	}
	imported, err := sender.ImportRGB11Contract(context.Background(), contract)
	if err != nil {
		t.Fatal(err)
	}
	if imported.Projected != 1 {
		t.Fatalf("official contract projected allocations=%d", imported.Projected)
	}
	feeInputs, err := sender.GetUtxosForFee(senderWallet.GetAddress(), 50_000, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(feeInputs) != 1 || feeInputs[0] != plainOutpoint {
		t.Fatalf("ordinary fee selection consumed RGB-locked UTXO: %v", feeInputs)
	}

	amount := uint64(20_000)
	proofs, err := sender.rgbManager.projectionStore.ListProofs()
	if err != nil || len(proofs) != 1 {
		t.Fatalf("sender proof inventory=%+v err=%v", proofs, err)
	}
	if _, err := seals.DecodeGraphBlindSeal(proofs[0].SealDisclosure); err != nil {
		t.Fatalf("decode imported source seal: %v proof=%+v", err, proofs[0])
	}
	request, err := recipient.rgbManager.engine.CreateReceive(corewallet.ReceiveParams{
		ContractID: imported.ContractID, Network: invoicing.BitcoinTestnet4, Amount: &amount,
		RecipientID: hex.EncodeToString(recipientWallet.GetPubKey().SerializeCompressed()),
		WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: request.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.State.Status != "prepared" || prepared.State.AckStatus != "awaiting" {
		t.Fatalf("prepared transfer state=%+v", prepared.State)
	}
	remote := newRGB11MemoryDKVSHTTP()
	sender.cfg = &common.Config{IndexerL2: &common.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"}}
	sender.http = remote
	if _, err := sender.SyncRGB11WalletState("", dkvsindexer.RecordOptions{
		TTL: uint64((24 * time.Hour) / time.Millisecond),
	}); err != nil {
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
	if len(witness.TxOut) != 3 || len(witness.TxOut[0].PkScript) != 34 || witness.TxOut[0].PkScript[0] != 0x6a ||
		!bytes.Equal(witness.TxOut[1].PkScript, recipientScript) {
		t.Fatalf("unexpected RGB11 witness transaction %s", witness.TxID())
	}
	witnessTxID := witness.TxHash().String()
	recipientOutpoint := witnessTxID + ":1"

	relayOptions := dkvsindexer.RecordOptions{TTL: uint64((24 * time.Hour) / time.Millisecond)}
	relayRecord, _, err := sender.PublishRGB11RelayRecord(prepared.State.TransferID, "sender-test", relayOptions)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sender.SyncRGB11WalletState("", relayOptions); err != nil {
		t.Fatal(err)
	}
	if _, err := sender.BroadcastRGB11Transfer(prepared.State.TransferID, relayRecord, nil); err != ErrRGB11AckRequired {
		t.Fatalf("broadcast without ACK error=%v", err)
	}
	receipt, ack, err := recipient.AcceptRGB11RelayConsignment(
		context.Background(), request.RequestID, relayRecord, []byte(prepared.RecipientConsignment),
	)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ContractID != imported.ContractID || !ack.Accepted {
		t.Fatalf("recipient validation result receipt=%+v ack=%+v", receipt, ack)
	}
	balance, err := recipient.GetRGB11AssetBalance(&imported.AssetName)
	if err != nil {
		t.Fatal(err)
	}
	if balance.Sign() != 0 {
		t.Fatalf("prepared recipient RGB11 balance=%s", balance.Value)
	}
	recipientState, err := recipient.GetRGB11State()
	if err != nil || len(recipientState.Transfers) != 1 || recipientState.Transfers[0].Direction != "receive" ||
		recipientState.Transfers[0].WitnessTxID != witnessTxID ||
		recipientState.Transfers[0].Status != "awaiting_broadcast" ||
		recipientState.Transfers[0].TransportMode != "sat20-dkvs" {
		t.Fatalf("recipient transfer history=%+v err=%v", recipientState, err)
	}
	recipientWalletID, err := recipient.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	recipientSnapshot, _, err := recipient.rgbManager.exportRGB11WalletSnapshot(recipientWalletID)
	if err != nil {
		t.Fatal(err)
	}
	restoredRecipientWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
	)
	restoredRecipient := newRGB11FlowManager(t, restoredRecipientWallet, rpc, evidence, 3)
	if err := restoredRecipient.rgbManager.importRGB11WalletSnapshot(recipientSnapshot); err != nil {
		t.Fatalf("restore pre-broadcast recipient snapshot: %v", err)
	}
	recipient = restoredRecipient
	recipient.cfg = &common.Config{IndexerL2: &common.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"}}
	recipient.http = remote
	if _, err := recipient.SyncRGB11WalletState("", relayOptions); err != nil {
		t.Fatal(err)
	}
	if _, err := recipient.PublishRGB11AckRecord(prepared.State.AckRecordKey, ack, relayOptions); err != nil {
		t.Fatal(err)
	}
	fetchedAck, _, err := sender.FetchRGB11AckRecord(prepared.State.TransferID,
		dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())})
	if err != nil {
		t.Fatal(err)
	}
	broadcastTxID, err := sender.BroadcastRGB11Transfer(prepared.State.TransferID, relayRecord, fetchedAck)
	if err != nil {
		t.Fatal(err)
	}
	if broadcastTxID != witnessTxID || len(evidence.broadcasted) == 0 {
		t.Fatalf("broadcast txid=%s witness=%s", broadcastTxID, witnessTxID)
	}
	evidence.mu.Lock()
	evidence.rawTx[witnessTxID] = append([]byte(nil), pending.SignedTx...)
	evidence.spendingTx[sourceOutpoint] = witnessTxID
	evidence.utxos[recipientOutpoint] = &rgb11wallet.BitcoinUTXO{
		OutPoint: recipientOutpoint, Value: witness.TxOut[1].Value,
		PkScript: append([]byte(nil), witness.TxOut[1].PkScript...), Confirmations: 0,
	}
	evidence.mu.Unlock()
	if _, err := recipient.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	balance, err = recipient.GetRGB11AssetBalance(&imported.AssetName)
	if err != nil || balance.Value.Uint64() != amount {
		t.Fatalf("recipient RGB11 balance=%v err=%v", balance, err)
	}
	recipientState, err = recipient.GetRGB11State()
	if err != nil || len(recipientState.Transfers) != 1 || recipientState.Transfers[0].Status != "pending" {
		t.Fatalf("post-broadcast recipient transfer history=%+v err=%v", recipientState, err)
	}
}

func TestRGB11StandardOutOfBandPrepareBroadcastAccept(t *testing.T) {
	senderWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	recipientWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
	)
	if senderWallet == nil || recipientWallet == nil {
		t.Fatal("create RGB11 out-of-band wallets")
	}
	senderScript, err := AddrToPkScript(senderWallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	const sourceOutpoint = "14295d5bb1a191cdb6286dc0944df938421e3dfcbf0811353ccac4100c2068c5:1"
	const plainOutpoint = "2222222222222222222222222222222222222222222222222222222222222222:0"
	evidence := &rgb11FlowEvidence{
		utxos: map[string]*rgb11wallet.BitcoinUTXO{
			sourceOutpoint: {OutPoint: sourceOutpoint, Value: 10_000, PkScript: senderScript, Confirmations: 6},
		},
		rawTx: make(map[string][]byte), spendingTx: make(map[string]string),
	}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	sourceOutput := indexer.NewTxOutput(10_000)
	sourceOutput.OutPointStr, sourceOutput.OutValue.PkScript = sourceOutpoint, senderScript
	rpc.outputs[sourceOutpoint] = sourceOutput
	plainOutput := indexer.NewTxOutput(100_000)
	plainOutput.OutPointStr, plainOutput.OutValue.PkScript = plainOutpoint, senderScript
	rpc.outputs[plainOutpoint] = plainOutput
	rpc.plain = []*indexerwire.TxOutputInfo{
		{OutPoint: sourceOutpoint, Value: 10_000, PkScript: senderScript},
		{OutPoint: plainOutpoint, Value: 100_000, PkScript: senderScript},
	}
	sender := newRGB11FlowManager(t, senderWallet, rpc, evidence, 4)
	recipient := newRGB11FlowManager(t, recipientWallet, rpc, evidence, 5)
	contract, err := os.ReadFile("../../../rgb11/testvectors/rc11/nia-example.rgba")
	if err != nil {
		t.Fatal(err)
	}
	imported, err := sender.ImportRGB11Contract(context.Background(), contract)
	if err != nil {
		t.Fatal(err)
	}
	const amount = uint64(20_000)
	request, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID,
		AmountRaw: fmt.Sprint(amount), WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	parsedInvoice, err := invoicing.Parse(request.Invoice)
	if err != nil || len(parsedInvoice.UnknownQuery) != 0 || len(parsedInvoice.Transports) != 0 {
		t.Fatalf("out-of-band invoice contains SAT20/proxy transport data: %+v err=%v", parsedInvoice, err)
	}
	prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: request.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.State.TransportMode != "out-of-band" {
		t.Fatalf("prepared transport=%s", prepared.State.TransportMode)
	}
	if _, err := sender.BuildRGB11RelayRecord(prepared.State.TransferID, "cross-transport-test"); !errors.Is(err, ErrRGB11SAT20RelayRequired) {
		t.Fatalf("SAT20 relay accepted out-of-band transfer: %v", err)
	}
	pendingForModeTest, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	pendingForModeTest.State.TransportMode = RGB11ProxyTransport
	if err := sender.rgbManager.projectionStore.SavePendingTransferState(pendingForModeTest); err != nil {
		t.Fatal(err)
	}
	if _, err := sender.BuildRGB11RelayRecord(prepared.State.TransferID, "cross-transport-test"); !errors.Is(err, ErrRGB11SAT20RelayRequired) {
		t.Fatalf("SAT20 relay accepted standard proxy transfer: %v", err)
	}
	pendingForModeTest.State.TransportMode = "out-of-band"
	if err := sender.rgbManager.projectionStore.SavePendingTransferState(pendingForModeTest); err != nil {
		t.Fatal(err)
	}
	if _, _, err := recipient.AcceptRGB11RelayConsignment(
		context.Background(), request.RequestID, &corerelay.RelayRecord{},
		[]byte(prepared.RecipientConsignment),
	); !errors.Is(err, ErrRGB11SAT20RelayRequired) {
		t.Fatalf("SAT20 relay accepted out-of-band receive request: %v", err)
	}
	if _, _, err := sender.FetchRGB11AckRecord(prepared.State.TransferID, dkvsindexer.RecordVerificationOptions{}); !errors.Is(err, ErrRGB11SAT20RelayRequired) {
		t.Fatalf("SAT20 ACK fetch accepted out-of-band transfer: %v", err)
	}
	if _, err := sender.BroadcastRGB11Batch(
		[]string{prepared.State.TransferID}, []*corerelay.RelayRecord{{}}, []*corerelay.AckRecord{{}},
	); !errors.Is(err, ErrRGB11SAT20RelayRequired) {
		t.Fatalf("SAT20 ACK broadcast accepted out-of-band transfer: %v", err)
	}
	if err := sender.CancelRGB11BatchByNack(
		prepared.State.TransferID, &corerelay.RelayRecord{}, &corerelay.AckRecord{},
	); !errors.Is(err, ErrRGB11SAT20RelayRequired) {
		t.Fatalf("SAT20 NACK accepted out-of-band transfer: %v", err)
	}
	if _, err := recipient.PrepareRGB11Consignment(
		context.Background(), request.RequestID, []byte(prepared.RecipientConsignment),
	); err != nil {
		t.Fatal(err)
	}
	balance, err := recipient.GetRGB11AssetBalance(&imported.AssetName)
	if err != nil || balance.Sign() != 0 {
		t.Fatalf("pre-broadcast balance=%v err=%v", balance, err)
	}
	recipientState, err := recipient.GetRGB11State()
	if err != nil || len(recipientState.Transfers) != 1 ||
		recipientState.Transfers[0].Status != "awaiting_broadcast" ||
		recipientState.Transfers[0].RelayDurability != "LOCAL_ONLY" ||
		recipientState.Transfers[0].TransportMode != "out-of-band" {
		var transfer any
		if recipientState != nil && len(recipientState.Transfers) > 0 {
			transfer = *recipientState.Transfers[0]
		}
		t.Fatalf("pre-broadcast transfer=%+v err=%v", transfer, err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	witness := wire.NewMsgTx(wire.TxVersion)
	if err := witness.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	witnessTxID, err := sender.BroadcastRGB11OutOfBand([]string{prepared.State.TransferID})
	if err != nil {
		t.Fatal(err)
	}
	recipientOutpoint := witnessTxID + ":1"
	evidence.mu.Lock()
	evidence.rawTx[witnessTxID] = append([]byte(nil), pending.SignedTx...)
	evidence.spendingTx[sourceOutpoint] = witnessTxID
	evidence.utxos[recipientOutpoint] = &rgb11wallet.BitcoinUTXO{
		OutPoint: recipientOutpoint, Value: witness.TxOut[1].Value,
		PkScript: append([]byte(nil), witness.TxOut[1].PkScript...), Confirmations: 0,
	}
	evidence.mu.Unlock()
	if _, err := recipient.AcceptRGB11Consignment(
		context.Background(), request.RequestID, []byte(prepared.RecipientConsignment),
	); err != nil {
		t.Fatal(err)
	}
	balance, err = recipient.GetRGB11AssetBalance(&imported.AssetName)
	if err != nil || balance.Value.Uint64() != amount {
		t.Fatalf("post-broadcast balance=%v err=%v", balance, err)
	}

	sat20Request, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "sat20", ContractID: imported.ContractID,
		AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recipient.PrepareRGB11Consignment(
		context.Background(), sat20Request.RequestID, []byte(prepared.RecipientConsignment),
	); !errors.Is(err, ErrRGB11OutOfBandRequired) {
		t.Fatalf("standard preflight accepted SAT20 invoice: %v", err)
	}
}

func TestRGB11CancelPreparedOutOfBandTransfer(t *testing.T) {
	wallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	evidence := &rgb11FlowEvidence{
		utxos: make(map[string]*rgb11wallet.BitcoinUTXO), rawTx: make(map[string][]byte),
		spendingTx: make(map[string]string),
	}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	manager := newRGB11FlowManager(t, wallet, rpc, evidence, 6)
	const rgbInput = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:0"
	const feeInput = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb:1"
	newPending := func(id, txid string) *rgb11wallet.PendingTransfer {
		recipient := []byte("out-of-band-recipient-" + id)
		hash := sha256.Sum256(recipient)
		return &rgb11wallet.PendingTransfer{
			State: rgb11wallet.TransferState{
				TransferID: id, BatchTransferIDs: []string{id}, BatchSize: 1,
				TransportMode: "out-of-band", Direction: "send", Status: "prepared",
				AckStatus: "awaiting", InputOutPoints: []string{rgbInput, feeInput},
				ConsignmentHash: hex.EncodeToString(hash[:]), WitnessTxID: txid,
			},
			RecipientConsignment: recipient, LocalConsignment: []byte("local-" + id),
			SignedTx: []byte{1}, SignedPSBT: []byte{2}, CreatedAt: time.Now().Unix(),
		}
	}
	const firstTxID = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if err := manager.rgbManager.projectionStore.SavePendingTransfer(newPending("cancel-oob", firstTxID)); err != nil {
		t.Fatal(err)
	}
	if err := manager.utxoLockerL1.LockUtxo(rgbInput, rgb11wallet.LockReasonRGB); err != nil {
		t.Fatal(err)
	}
	if err := manager.utxoLockerL1.LockUtxo(feeInput, rgb11wallet.LockReasonPending); err != nil {
		t.Fatal(err)
	}
	if err := manager.CancelRGB11OutOfBandTransfer("cancel-oob"); err != nil {
		t.Fatal(err)
	}
	stored, err := manager.rgbManager.projectionStore.LoadPendingTransfer("cancel-oob")
	if err != nil || stored.State.Status != "rejected" || len(stored.SignedTx) != 0 {
		t.Fatalf("cancelled transfer=%+v err=%v", stored, err)
	}
	locks := manager.utxoLockerL1.GetLockedUtxoList()
	if locks[rgbInput] == nil || locks[rgbInput].Reason != rgb11wallet.LockReasonRGB || locks[feeInput] != nil {
		t.Fatalf("unexpected locks after cancellation: %+v", locks)
	}

	const visibleTxID = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	if err := manager.rgbManager.projectionStore.SavePendingTransfer(newPending("visible-oob", visibleTxID)); err != nil {
		t.Fatal(err)
	}
	evidence.rawTx[visibleTxID] = []byte{1}
	if err := manager.CancelRGB11OutOfBandTransfer("visible-oob"); !errors.Is(err, ErrRGB11AlreadyBroadcast) {
		t.Fatalf("visible transfer cancellation error=%v", err)
	}
}

func TestRGB11GenericBlindedProxyInvoiceUsesExplicitAssetAndNoRecipientOutput(t *testing.T) {
	senderWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	if senderWallet == nil {
		t.Fatal("create RGB11 test wallet")
	}
	senderScript, err := AddrToPkScript(senderWallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	const sourceOutpoint = "14295d5bb1a191cdb6286dc0944df938421e3dfcbf0811353ccac4100c2068c5:1"
	const plainOutpoint = "3333333333333333333333333333333333333333333333333333333333333333:0"
	evidence := &rgb11FlowEvidence{
		utxos: map[string]*rgb11wallet.BitcoinUTXO{
			sourceOutpoint: {OutPoint: sourceOutpoint, Value: 10_000, PkScript: senderScript, Confirmations: 6},
		},
		rawTx: make(map[string][]byte), spendingTx: make(map[string]string),
	}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	sourceOutput := indexer.NewTxOutput(10_000)
	sourceOutput.OutPointStr, sourceOutput.OutValue.PkScript = sourceOutpoint, senderScript
	rpc.outputs[sourceOutpoint] = sourceOutput
	plainOutput := indexer.NewTxOutput(100_000)
	plainOutput.OutPointStr, plainOutput.OutValue.PkScript = plainOutpoint, senderScript
	rpc.outputs[plainOutpoint] = plainOutput
	rpc.plain = []*indexerwire.TxOutputInfo{
		{OutPoint: sourceOutpoint, Value: 10_000, PkScript: senderScript},
		{OutPoint: plainOutpoint, Value: 100_000, PkScript: senderScript},
	}
	sender := newRGB11FlowManager(t, senderWallet, rpc, evidence, 34)
	contract, err := os.ReadFile("../../../rgb11/testvectors/rc11/nia-example.rgba")
	if err != nil {
		t.Fatal(err)
	}
	imported, err := sender.ImportRGB11Contract(context.Background(), contract)
	if err != nil {
		t.Fatal(err)
	}

	generic, err := invoicing.Parse(
		"rgb:~/~/~/tb4:utxob:0p7Vez5g-71fjxwc-cM1U8q8-aPdDoj5-rhHwhlc-7lHZlZy-YcXFX" +
			"?expiry=1785485187&endpoints=rpcs://proxy.iriswallet.com/0.2/json-rpc",
	)
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Hour).Unix()
	generic.Expiry = &expiry
	if _, _, _, _, _, _, err := validateRGB11SendInvoice(generic, nil, nil); !errors.Is(err, invoicing.ErrInvalidInvoice) {
		t.Fatalf("generic invoice without explicit asset should fail: %v", err)
	}
	contractID, err := consensus.ParseContractID(imported.ContractID)
	if err != nil {
		t.Fatal(err)
	}
	wrongContract := contractID
	wrongContract[0] ^= 1
	genericWithWrongContract := *generic
	genericWithWrongContract.Contract = &wrongContract
	amount := uint64(1_000)
	if _, _, _, _, _, _, err := validateRGB11SendInvoice(
		&genericWithWrongContract, &contractID, &amount,
	); !errors.Is(err, invoicing.ErrInvalidInvoice) {
		t.Fatalf("conflicting explicit contract should fail: %v", err)
	}

	prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: generic.String(), ContractID: imported.ContractID, AmountRaw: "1000",
		FeeRate: 2, MinConfirmations: 1,
	})
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
	if len(witness.TxOut) != 2 || len(witness.TxOut[0].PkScript) != 34 || witness.TxOut[0].PkScript[0] != txscript.OP_RETURN {
		t.Fatalf("generic blinded transfer must contain only OP_RETURN and change outputs: %+v", witness.TxOut)
	}
	wantChangeOutpoint := fmt.Sprintf("%s:1", witness.TxHash())
	if prepared.State.TransportMode != RGB11ProxyTransport || prepared.State.RecipientVout != 0 ||
		prepared.State.RecipientID != generic.Beneficiary.String() ||
		!slices.Equal(prepared.State.OutputOutPoints, []string{wantChangeOutpoint}) {
		t.Fatalf("unexpected generic blinded transfer state: %+v", prepared.State)
	}
	if prepared.State.Asset.Amount.Value == nil || prepared.State.Asset.Amount.Value.Uint64() != amount {
		t.Fatalf("unexpected generic blinded transfer amount: %+v", prepared.State.Asset.Amount)
	}
	if _, err := coreconsignment.DecodeArmor(prepared.RecipientConsignment); err != nil {
		t.Fatalf("generic blinded recipient consignment is invalid: %v", err)
	}
}

func TestRGB11WitnessBatchRequiresEveryRecipientAck(t *testing.T) {
	senderWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	recipientWallets := []common.Wallet{
		NewInternalWalletWithMnemonic("comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params),
		NewInternalWalletWithMnemonic("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "", &chaincfg.TestNet4Params),
	}
	if senderWallet == nil || recipientWallets[0] == nil || recipientWallets[1] == nil {
		t.Fatal("create RGB11 batch wallets")
	}
	senderScript, err := AddrToPkScript(senderWallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	const sourceOutpoint = "14295d5bb1a191cdb6286dc0944df938421e3dfcbf0811353ccac4100c2068c5:1"
	const plainOutpoint = "2222222222222222222222222222222222222222222222222222222222222222:0"
	evidence := &rgb11FlowEvidence{
		utxos: map[string]*rgb11wallet.BitcoinUTXO{
			sourceOutpoint: {OutPoint: sourceOutpoint, Value: 10_000, PkScript: senderScript, Confirmations: 6},
		},
		rawTx: make(map[string][]byte), spendingTx: make(map[string]string),
	}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	sourceOutput := indexer.NewTxOutput(10_000)
	sourceOutput.OutPointStr, sourceOutput.OutValue.PkScript = sourceOutpoint, senderScript
	rpc.outputs[sourceOutpoint] = sourceOutput
	plainOutput := indexer.NewTxOutput(100_000)
	plainOutput.OutPointStr, plainOutput.OutValue.PkScript = plainOutpoint, senderScript
	rpc.outputs[plainOutpoint] = plainOutput
	rpc.plain = []*indexerwire.TxOutputInfo{
		{OutPoint: sourceOutpoint, Value: 10_000, PkScript: senderScript},
		{OutPoint: plainOutpoint, Value: 100_000, PkScript: senderScript},
	}
	sender := newRGB11FlowManager(t, senderWallet, rpc, evidence, 31)
	recipients := []*Manager{
		newRGB11FlowManager(t, recipientWallets[0], rpc, evidence, 32),
		newRGB11FlowManager(t, recipientWallets[1], rpc, evidence, 33),
	}
	contract, err := os.ReadFile("../../../rgb11/testvectors/rc11/nia-example.rgba")
	if err != nil {
		t.Fatal(err)
	}
	imported, err := sender.ImportRGB11Contract(context.Background(), contract)
	if err != nil {
		t.Fatal(err)
	}
	amounts := []uint64{7_000, 13_000}
	requests := make([]*corewallet.ReceiveRequest, 0, len(recipients))
	invoices := make([]string, 0, len(recipients))
	for index, recipient := range recipients {
		request, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
			Mode: "witness", ContractID: imported.ContractID, AmountRaw: fmt.Sprint(amounts[index]),
			WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
		})
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, request)
		invoices = append(invoices, request.Invoice)
	}
	officialInvoice, err := invoicing.Parse(invoices[0])
	if err != nil {
		t.Fatal(err)
	}
	officialInvoice.UnknownQuery = nil
	_, _, externalRecipientID, externalRelay, externalAck, externalMode, err :=
		validateRGB11SendInvoice(officialInvoice, nil, nil)
	if err != nil || externalMode != "out-of-band" || externalRecipientID != officialInvoice.Beneficiary.String() ||
		externalRelay != "" || externalAck != "" {
		t.Fatalf("official out-of-band invoice validation: mode=%s recipient=%s relay=%s ack=%s err=%v",
			externalMode, externalRecipientID, externalRelay, externalAck, err)
	}
	prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoices: invoices, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.States) != 2 || prepared.State != prepared.States[0] || prepared.State.BatchID == "" ||
		prepared.States[0].TransferID == prepared.States[1].TransferID {
		t.Fatalf("unexpected batch result: %+v", prepared)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.States[0].TransferID)
	if err != nil {
		t.Fatal(err)
	}
	witness := wire.NewMsgTx(wire.TxVersion)
	if err := witness.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	if len(witness.TxOut) != 4 {
		t.Fatalf("batch witness outputs=%d", len(witness.TxOut))
	}
	witnessTxID := witness.TxHash().String()
	evidence.mu.Lock()
	evidence.rawTx[witnessTxID] = append([]byte(nil), pending.SignedTx...)
	evidence.spendingTx[sourceOutpoint] = witnessTxID
	for index := range recipients {
		outpoint := fmt.Sprintf("%s:%d", witnessTxID, index+1)
		evidence.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: witness.TxOut[index+1].Value,
			PkScript: append([]byte(nil), witness.TxOut[index+1].PkScript...),
		}
	}
	evidence.mu.Unlock()
	relayRecords := make([]*corerelay.RelayRecord, 0, len(recipients))
	acks := make([]*corerelay.AckRecord, 0, len(recipients))
	transferIDs := make([]string, 0, len(recipients))
	for index, recipient := range recipients {
		state := prepared.States[index]
		record, err := sender.BuildRGB11RelayRecord(state.TransferID, "batch-test")
		if err != nil {
			t.Fatal(err)
		}
		receipt, ack, err := recipient.AcceptRGB11RelayConsignment(
			context.Background(), requests[index].RequestID, record, []byte(prepared.RecipientConsignment),
		)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.ContractID != imported.ContractID || !ack.Accepted {
			t.Fatalf("recipient %d receipt=%+v ack=%+v", index, receipt, ack)
		}
		balance, err := recipient.GetRGB11AssetBalance(&imported.AssetName)
		if err != nil || balance.Value.Uint64() != amounts[index] {
			t.Fatalf("recipient %d balance=%v err=%v", index, balance, err)
		}
		relayRecords = append(relayRecords, record)
		acks = append(acks, ack)
		transferIDs = append(transferIDs, state.TransferID)
	}
	if _, err := sender.BroadcastRGB11Transfer(transferIDs[0], relayRecords[0], acks[0]); err != ErrRGB11BatchAckRequired {
		t.Fatalf("partial batch broadcast error=%v", err)
	}
	if _, err := sender.BroadcastRGB11Batch(transferIDs[:1], relayRecords[:1], acks[:1]); err != ErrRGB11BatchAckRequired {
		t.Fatalf("incomplete batch ACK error=%v", err)
	}
	externalSender := newRGB11FlowManager(t, senderWallet, rpc, evidence, 34)
	externalPending := make([]*rgb11wallet.PendingTransfer, 0, len(transferIDs))
	for _, transferID := range transferIDs {
		item, err := sender.rgbManager.projectionStore.LoadPendingTransfer(transferID)
		if err != nil {
			t.Fatal(err)
		}
		item.State.TransportMode = "out-of-band"
		item.State.RelayRecordKey, item.State.AckRecordKey = "", ""
		externalPending = append(externalPending, item)
	}
	if err := externalSender.rgbManager.projectionStore.SavePendingTransfers(externalPending); err != nil {
		t.Fatal(err)
	}
	if outOfBandTxID, err := externalSender.BroadcastRGB11OutOfBand(transferIDs); err != nil || outOfBandTxID != witnessTxID {
		t.Fatalf("official out-of-band ACK broadcast txid=%s err=%v", outOfBandTxID, err)
	}
	broadcastTxID, err := sender.BroadcastRGB11Batch(transferIDs, relayRecords, acks)
	if err != nil {
		t.Fatal(err)
	}
	if broadcastTxID != witnessTxID || len(evidence.broadcasted) == 0 {
		t.Fatalf("batch broadcast txid=%s witness=%s", broadcastTxID, witnessTxID)
	}
	for _, transferID := range transferIDs {
		stored, err := sender.rgbManager.projectionStore.LoadPendingTransfer(transferID)
		if err != nil || stored.State.Status != "broadcast" || stored.State.AckStatus != "accepted" {
			t.Fatalf("batch state %s=%+v err=%v", transferID, stored, err)
		}
	}
}

func TestRGB11IssueFirstReleaseSchemas(t *testing.T) {
	wallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	if wallet == nil {
		t.Fatal("create RGB11 issuer wallet")
	}
	walletScript, err := AddrToPkScript(wallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		request RGB11IssueRequest
		count   int
		balance uint64
	}{
		{"NIA", RGB11IssueRequest{Schema: "NIA", Ticker: "SNIA", Name: "SAT20 NIA", Precision: 8, Amounts: []uint64{40, 60}}, 2, 100},
		{"IFA", RGB11IssueRequest{Schema: "IFA", Ticker: "SIFA", Name: "SAT20 IFA", Precision: 2, Amounts: []uint64{100}, InflationAmounts: []uint64{900}, RejectListURL: "https://example.com/reject.txt"}, 2, 100},
		{"UDA", RGB11IssueRequest{Schema: "UDA", Ticker: "SUDA", Name: "SAT20 UDA"}, 1, 1},
	}
	for testIndex, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
			evidence := &rgb11FlowEvidence{
				utxos: make(map[string]*rgb11wallet.BitcoinUTXO), rawTx: make(map[string][]byte), spendingTx: make(map[string]string),
			}
			for index := 0; index < 3; index++ {
				txid := fmt.Sprintf("%064x", 1000+testIndex*10+index)
				outpoint := txid + ":0"
				rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{OutPoint: outpoint, Value: 10_000, PkScript: walletScript})
				evidence.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
					OutPoint: outpoint, Value: 10_000, PkScript: walletScript, Confirmations: 6,
				}
			}
			manager := newRGB11FlowManager(t, wallet, rpc, evidence, int64(10+testIndex))
			issued, err := manager.IssueRGB11Asset(context.Background(), test.request)
			if err != nil {
				t.Fatal(err)
			}
			if issued.Projected != test.count || len(issued.OutPoints) != test.count || issued.ContractID == "" ||
				issued.Armor == "" || issued.ContractConsignmentBase64 == "" {
				t.Fatalf("unexpected issuance result: %+v", issued)
			}
			contractFile, err := base64.StdEncoding.DecodeString(issued.ContractConsignmentBase64)
			if err != nil {
				t.Fatal(err)
			}
			decodedContract, err := coreconsignment.DecodeFile(contractFile)
			if err != nil {
				t.Fatal(err)
			}
			if decodedContract.ContractID != issued.ContractID || decodedContract.Armor == nil ||
				!bytes.HasPrefix(contractFile, []byte("RGB\x00CON")) {
				t.Fatalf("standard contract export does not match issued contract")
			}
			imported, err := manager.ImportRGB11ContractFile(context.Background(), contractFile)
			if err != nil {
				t.Fatal(err)
			}
			if imported.ContractID != issued.ContractID {
				t.Fatalf("standard contract import=%s want=%s", imported.ContractID, issued.ContractID)
			}
			expectedName, err := rgb11wallet.NewCanonicalAssetName(issued.ContractID, test.request.Ticker, issued.AssetName.Type)
			if err != nil {
				t.Fatal(err)
			}
			if issued.AssetName != expectedName {
				t.Fatalf("canonical asset name=%s want=%s", issued.AssetName.String(), expectedName.String())
			}
			balance, err := manager.GetRGB11AssetBalance(&issued.AssetName)
			if err != nil {
				t.Fatal(err)
			}
			if balance.Value.Uint64() != test.balance {
				t.Fatalf("balance=%s want=%d", balance.Value, test.balance)
			}
			state, err := manager.GetRGB11State()
			if err != nil {
				t.Fatal(err)
			}
			if len(state.Assets) == 0 || len(state.Outputs) != test.count {
				t.Fatalf("unexpected RGB11 state after issuance: assets=%d outputs=%d", len(state.Assets), len(state.Outputs))
			}
			if len(state.TickerInfos) != 1 || state.TickerInfos[0].CanonicalName != issued.AssetName.String() ||
				state.TickerInfos[0].ContractID != issued.ContractID || state.TickerInfos[0].Ticker != issued.AssetName.Ticker ||
				state.TickerInfos[0].Verified {
				t.Fatalf("unexpected RGB11 naming presentation: %+v", state.TickerInfos)
			}
			if _, err := json.Marshal(state); err != nil {
				t.Fatalf("RGB11 state is not JSON serializable: %v", err)
			}
			locked := manager.utxoLockerL1.GetLockedUtxoList()
			for _, outpoint := range issued.OutPoints {
				if locked[outpoint] == nil || locked[outpoint].Reason != rgb11wallet.LockReasonRGB {
					t.Fatalf("issued carrier %s is not RGB-locked: %+v", outpoint, locked[outpoint])
				}
			}
		})
	}
}

func TestRGB11IssueRejectsCFAInFirstRelease(t *testing.T) {
	manager := &Manager{}
	_, err := manager.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "CFA", Name: "SAT20 CFA", Amounts: []uint64{1},
	})
	if !errors.Is(err, ErrRGB11Inconsistent) {
		t.Fatalf("uninitialized manager error=%v", err)
	}

	wallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	if wallet == nil {
		t.Fatal("create RGB11 issuer wallet")
	}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	evidence := &rgb11FlowEvidence{
		utxos: make(map[string]*rgb11wallet.BitcoinUTXO), rawTx: make(map[string][]byte), spendingTx: make(map[string]string),
	}
	manager = newRGB11FlowManager(t, wallet, rpc, evidence, 19)
	_, err = manager.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "CFA", Name: "SAT20 CFA", Amounts: []uint64{1},
	})
	if !errors.Is(err, coreissuance.ErrUnsupportedSchema) {
		t.Fatalf("CFA issuance error=%v, want %v", err, coreissuance.ErrUnsupportedSchema)
	}
}

func TestRGB11IssuedUDASendReceive(t *testing.T) {
	senderWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	recipientWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
	)
	if senderWallet == nil || recipientWallet == nil {
		t.Fatal("create UDA flow wallets")
	}
	senderScript, _ := AddrToPkScript(senderWallet.GetAddress(), &chaincfg.TestNet4Params)
	recipientScript, _ := AddrToPkScript(recipientWallet.GetAddress(), &chaincfg.TestNet4Params)
	sourceOutpoint := fmt.Sprintf("%064x:0", 2001)
	feeOutpoint := fmt.Sprintf("%064x:0", 2002)
	evidence := &rgb11FlowEvidence{
		utxos: map[string]*rgb11wallet.BitcoinUTXO{
			sourceOutpoint: {OutPoint: sourceOutpoint, Value: 10_000, PkScript: senderScript, Confirmations: 6},
			feeOutpoint:    {OutPoint: feeOutpoint, Value: 100_000, PkScript: senderScript, Confirmations: 6},
		},
		rawTx: make(map[string][]byte), spendingTx: make(map[string]string),
	}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput), plain: []*indexerwire.TxOutputInfo{
		{OutPoint: sourceOutpoint, Value: 10_000, PkScript: senderScript},
		{OutPoint: feeOutpoint, Value: 100_000, PkScript: senderScript},
	}}
	for outpoint, utxo := range evidence.utxos {
		output := indexer.NewTxOutput(utxo.Value)
		output.OutPointStr = outpoint
		output.OutValue.PkScript = append([]byte(nil), utxo.PkScript...)
		rpc.outputs[outpoint] = output
	}
	sender := newRGB11FlowManager(t, senderWallet, rpc, evidence, 30)
	recipient := newRGB11FlowManager(t, recipientWallet, rpc, evidence, 31)
	issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "UDA", Ticker: "SUDA", Name: "SAT20 Unique", Amounts: []uint64{1},
	})
	if err != nil {
		t.Fatal(err)
	}
	amount := uint64(1)
	receive, err := recipient.rgbManager.engine.CreateReceive(corewallet.ReceiveParams{
		ContractID: issued.ContractID, Network: invoicing.BitcoinTestnet4, Amount: &amount,
		RecipientID: hex.EncodeToString(recipientWallet.GetPubKey().SerializeCompressed()),
		WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: receive.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	transferFile, err := base64.StdEncoding.DecodeString(prepared.RecipientConsignmentBase64)
	if err != nil {
		t.Fatal(err)
	}
	decodedTransfer, err := coreconsignment.DecodeFile(transferFile)
	if err != nil {
		t.Fatal(err)
	}
	if decodedTransfer.Armor == nil || decodedTransfer.Armor.Type != "transfer" ||
		!bytes.HasPrefix(transferFile, []byte("RGB\x00TFR")) {
		t.Fatal("standard transfer export does not match prepared consignment")
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	witness := wire.NewMsgTx(wire.TxVersion)
	if err := witness.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	witnessTxID := witness.TxHash().String()
	recipientOutpoint := witnessTxID + ":1"
	evidence.mu.Lock()
	evidence.rawTx[witnessTxID] = append([]byte(nil), pending.SignedTx...)
	evidence.spendingTx[sourceOutpoint] = witnessTxID
	evidence.utxos[recipientOutpoint] = &rgb11wallet.BitcoinUTXO{
		OutPoint: recipientOutpoint, Value: witness.TxOut[1].Value,
		PkScript: append([]byte(nil), recipientScript...), Confirmations: 0,
	}
	evidence.mu.Unlock()
	receipt, err := recipient.AcceptRGB11Consignment(context.Background(), receive.RequestID, []byte(prepared.RecipientConsignment))
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ContractID != issued.ContractID || len(receipt.Allocations) != 1 || receipt.Allocations[0].StateClass != "structured" {
		t.Fatalf("unexpected UDA receipt: %+v", receipt)
	}
	balance, err := recipient.GetRGB11AssetBalance(&issued.AssetName)
	if err != nil || balance.Value.Uint64() != 1 {
		t.Fatalf("recipient UDA balance=%+v err=%v", balance, err)
	}
}

func TestRGB11IssuedFungibleFirstReleaseSchemasSendReceive(t *testing.T) {
	tests := []struct {
		name        string
		request     RGB11IssueRequest
		sendAmount  uint64
		walletScope int64
		receiveMode corewallet.ReceiveMode
	}{
		{"NIA", RGB11IssueRequest{Schema: "NIA", Ticker: "TNIA", Name: "Transfer NIA", Precision: 2, Amounts: []uint64{100}}, 40, 40, corewallet.ReceiveBlind},
		{"IFA", RGB11IssueRequest{Schema: "IFA", Ticker: "TIFA", Name: "Transfer IFA", Precision: 2, Amounts: []uint64{100}, InflationAmounts: []uint64{900}}, 40, 50, corewallet.ReceiveWitness},
	}
	for testIndex, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			senderWallet := NewInternalWalletWithMnemonic(
				"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
			)
			recipientWallet := NewInternalWalletWithMnemonic(
				"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
			)
			if senderWallet == nil || recipientWallet == nil {
				t.Fatal("create fungible schema flow wallets")
			}
			senderScript, err := AddrToPkScript(senderWallet.GetAddress(), &chaincfg.TestNet4Params)
			if err != nil {
				t.Fatal(err)
			}
			evidence := &rgb11FlowEvidence{
				utxos: make(map[string]*rgb11wallet.BitcoinUTXO), rawTx: make(map[string][]byte), spendingTx: make(map[string]string),
			}
			rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
			for index := 0; index < 4; index++ {
				outpoint := fmt.Sprintf("%064x:0", 3000+testIndex*10+index)
				value := int64(100_000)
				evidence.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
					OutPoint: outpoint, Value: value, PkScript: senderScript, Confirmations: 6,
				}
				rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{OutPoint: outpoint, Value: value, PkScript: senderScript})
				output := indexer.NewTxOutput(value)
				output.OutPointStr = outpoint
				output.OutValue.PkScript = append([]byte(nil), senderScript...)
				rpc.outputs[outpoint] = output
			}
			sender := newRGB11FlowManager(t, senderWallet, rpc, evidence, test.walletScope)
			recipient := newRGB11FlowManager(t, recipientWallet, rpc, evidence, test.walletScope+1)
			issued, err := sender.IssueRGB11Asset(context.Background(), test.request)
			if err != nil {
				t.Fatal(err)
			}
			receive, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
				Mode: string(test.receiveMode), ContractID: issued.ContractID,
				AmountRaw: fmt.Sprint(test.sendAmount), WitnessVout: 1,
				Expiry: time.Now().Add(time.Hour).Unix(),
			})
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
				Invoice: receive.Invoice, FeeRate: 2, MinConfirmations: 1,
			})
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
			witnessTxID := witness.TxHash().String()
			recipientOutpoint := witnessTxID + ":1"
			evidence.mu.Lock()
			evidence.rawTx[witnessTxID] = append([]byte(nil), pending.SignedTx...)
			for _, input := range pending.State.InputOutPoints {
				evidence.spendingTx[input] = witnessTxID
			}
			evidence.utxos[recipientOutpoint] = &rgb11wallet.BitcoinUTXO{
				OutPoint: recipientOutpoint, Value: witness.TxOut[1].Value,
				PkScript: append([]byte(nil), witness.TxOut[1].PkScript...), Confirmations: 0,
			}
			for _, changeSeal := range pending.ChangeSeals {
				if int(changeSeal.Vout) >= len(witness.TxOut) {
					t.Fatalf("RGB11 change vout %d outside witness outputs", changeSeal.Vout)
				}
				changeOutpoint := fmt.Sprintf("%s:%d", witnessTxID, changeSeal.Vout)
				evidence.utxos[changeOutpoint] = &rgb11wallet.BitcoinUTXO{
					OutPoint: changeOutpoint, Value: witness.TxOut[changeSeal.Vout].Value,
					PkScript: append([]byte(nil), witness.TxOut[changeSeal.Vout].PkScript...), Confirmations: 0,
				}
			}
			evidence.mu.Unlock()
			receipt, err := recipient.AcceptRGB11Consignment(context.Background(), receive.RequestID, []byte(prepared.RecipientConsignment))
			if err != nil {
				t.Fatal(err)
			}
			matched := false
			for _, allocation := range receipt.Allocations {
				if allocation.OutPoint == recipientOutpoint && allocation.AssignmentType == 4000 &&
					allocation.StateClass == "fungible" && allocation.Amount.Value.Uint64() == test.sendAmount {
					matched = true
					break
				}
			}
			if receipt.ContractID != issued.ContractID || !matched {
				t.Fatalf("unexpected %s receipt: %+v", test.name, receipt)
			}
			balance, err := recipient.GetRGB11AssetBalance(&issued.AssetName)
			if err != nil || balance.Value.Uint64() != test.sendAmount {
				t.Fatalf("recipient %s balance=%+v err=%v", test.name, balance, err)
			}
			pending.State.Status = "broadcast"
			pending.State.AckStatus = "accepted"
			if err := sender.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
				t.Fatal(err)
			}
			refresh, err := sender.RefreshRGB11State(context.Background())
			if err != nil || refresh.Pending == 0 {
				t.Fatalf("refresh broadcast %s transfer result=%+v err=%v", test.name, refresh, err)
			}
			proofs, err := sender.rgbManager.projectionStore.ListProofs()
			if err != nil {
				t.Fatal(err)
			}
			sourceSpending := false
			stagedChange := false
			for _, proof := range proofs {
				if slices.Contains(pending.State.InputOutPoints, proof.OutPoint) {
					sourceSpending = sourceSpending || proof.Status == "spending"
				}
				if strings.HasPrefix(proof.OutPoint, witnessTxID+":") {
					stagedChange = stagedChange || proof.Status == "valid" && proof.Confirmations == 0
				}
			}
			if !sourceSpending || !stagedChange {
				t.Fatalf("%s broadcast projections sourceSpending=%t stagedChange=%t proofs=%+v",
					test.name, sourceSpending, stagedChange, proofs)
			}

			evidence.mu.Lock()
			delete(evidence.rawTx, witnessTxID)
			for _, input := range pending.State.InputOutPoints {
				delete(evidence.spendingTx, input)
			}
			for output := range evidence.utxos {
				if strings.HasPrefix(output, witnessTxID+":") {
					delete(evidence.utxos, output)
				}
			}
			evidence.mu.Unlock()
			refresh, err = sender.RefreshRGB11State(context.Background())
			if err != nil || refresh.Reorged != 1 {
				t.Fatalf("refresh evicted %s transfer result=%+v err=%v", test.name, refresh, err)
			}
			proofs, err = sender.rgbManager.projectionStore.ListProofs()
			if err != nil {
				t.Fatal(err)
			}
			sourceRestored := false
			for _, proof := range proofs {
				if strings.HasPrefix(proof.OutPoint, witnessTxID+":") {
					t.Fatalf("%s evicted staged projection retained: %+v", test.name, proof)
				}
				if slices.Contains(pending.State.InputOutPoints, proof.OutPoint) {
					sourceRestored = sourceRestored || proof.Status != "spending"
				}
			}
			if !sourceRestored {
				t.Fatalf("%s evicted source projection was not restored: %+v", test.name, proofs)
			}
		})
	}
}

type rgb11StaticRejectLists struct {
	list rejectlist.List
	err  error
}

func (s rgb11StaticRejectLists) Fetch(string) (rejectlist.List, error) { return s.list, s.err }

func TestRGB11ManualNackCancelsBatchAndReleasesOnlyFeeLocks(t *testing.T) {
	senderWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	recipientWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
	)
	if senderWallet == nil || recipientWallet == nil {
		t.Fatal("create reject flow wallets")
	}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	evidence := &rgb11FlowEvidence{utxos: make(map[string]*rgb11wallet.BitcoinUTXO), rawTx: make(map[string][]byte), spendingTx: make(map[string]string)}
	sender := newRGB11FlowManager(t, senderWallet, rpc, evidence, 80)
	recipient := newRGB11FlowManager(t, recipientWallet, rpc, evidence, 81)
	request, err := recipient.rgbManager.engine.CreateReceive(corewallet.ReceiveParams{
		Mode: corewallet.ReceiveBlind, Network: invoicing.BitcoinTestnet4,
		RecipientID: hex.EncodeToString(recipientWallet.GetPubKey().SerializeCompressed()),
		WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	const (
		transferID = "reject-transfer"
		rgbInput   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:0"
		feeInput   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb:1"
	)
	recipientConsignment := []byte("temporary recipient consignment")
	localConsignment := []byte("temporary local consignment")
	objectHash := sha256.Sum256(recipientConsignment)
	pending := &rgb11wallet.PendingTransfer{
		State: rgb11wallet.TransferState{
			TransferID: transferID, BatchID: "reject-batch", BatchTransferIDs: []string{transferID}, BatchSize: 1,
			Direction: "send", RecipientID: request.RecipientID, Invoice: request.Invoice,
			InputOutPoints: []string{rgbInput, feeInput}, ConsignmentHash: hex.EncodeToString(objectHash[:]),
			WitnessTxID: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			AckStatus:   "awaiting", Status: "prepared", RelayRecordKey: request.RelayKey,
			AckRecordKey: request.AckKey, Expiry: request.Expiry, TransportMode: "sat20-dkvs",
		},
		RecipientConsignment: recipientConsignment, LocalConsignment: localConsignment,
		SignedTx: []byte{1}, SignedPSBT: []byte{2}, CreatedAt: time.Now().Unix(),
	}
	if err := sender.rgbManager.projectionStore.SavePendingTransfer(pending); err != nil {
		t.Fatal(err)
	}
	if err := sender.utxoLockerL1.LockUtxo(rgbInput, rgb11wallet.LockReasonRGB); err != nil {
		t.Fatal(err)
	}
	if err := sender.utxoLockerL1.LockUtxo(feeInput, rgb11wallet.LockReasonPending); err != nil {
		t.Fatal(err)
	}
	record, err := sender.BuildRGB11RelayRecord(transferID, "reject-test")
	if err != nil {
		t.Fatal(err)
	}
	nack, err := recipient.RejectRGB11RelayConsignment(request.RequestID, record)
	if err != nil {
		t.Fatal(err)
	}
	if nack.Accepted || nack.ReasonCode != RGB11RejectReasonUser {
		t.Fatalf("unexpected NACK: %+v", nack)
	}
	if err := sender.CancelRGB11BatchByNack(transferID, record, nack); err != nil {
		t.Fatal(err)
	}
	stored, err := sender.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State.Status != "rejected" || stored.State.AckStatus != "rejected" ||
		stored.State.RejectReason != RGB11RejectReasonUser {
		t.Fatalf("unexpected cancelled state: %+v", stored.State)
	}
	if len(stored.RecipientConsignment) != 0 || len(stored.LocalConsignment) != 0 ||
		len(stored.SignedTx) != 0 || len(stored.SignedPSBT) != 0 {
		t.Fatal("rejected transfer retained private delivery payload")
	}
	locks := sender.utxoLockerL1.GetLockedUtxoList()
	if locks[rgbInput] == nil || locks[rgbInput].Reason != rgb11wallet.LockReasonRGB {
		t.Fatal("RGB carrier lock was released")
	}
	if locks[feeInput] != nil {
		t.Fatal("pending fee lock was not released")
	}
}

func TestRGB11RejectListUnavailableIsDistinctFromRejection(t *testing.T) {
	provider := rgb11StaticRejectLists{err: errors.New("offline")}
	if _, err := provider.Fetch("https://issuer.example/reject-list"); err == nil {
		t.Fatal("expected unavailable provider error")
	}
	if errors.Is(provider.err, ErrRGB11Rejected) {
		t.Fatal("service failure must not be treated as issuer rejection")
	}
}

func TestRGB11AutomaticRejectListNackDoesNotProjectBalance(t *testing.T) {
	senderWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	recipientWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
	)
	if senderWallet == nil || recipientWallet == nil {
		t.Fatal("create automatic reject wallets")
	}
	senderScript, _ := AddrToPkScript(senderWallet.GetAddress(), &chaincfg.TestNet4Params)
	evidence := &rgb11FlowEvidence{
		utxos: make(map[string]*rgb11wallet.BitcoinUTXO), rawTx: make(map[string][]byte), spendingTx: make(map[string]string),
	}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	for index := 0; index < 3; index++ {
		outpoint := fmt.Sprintf("%064x:0", 9000+index)
		evidence.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: 100_000, PkScript: senderScript, Confirmations: 6,
		}
		rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{OutPoint: outpoint, Value: 100_000, PkScript: senderScript})
		output := indexer.NewTxOutput(100_000)
		output.OutPointStr = outpoint
		output.OutValue.PkScript = append([]byte(nil), senderScript...)
		rpc.outputs[outpoint] = output
	}
	sender := newRGB11FlowManager(t, senderWallet, rpc, evidence, 90)
	recipient := newRGB11FlowManager(t, recipientWallet, rpc, evidence, 91)
	allowed := rejectlist.List{Reject: make(map[operations.Opout]struct{}), Allow: make(map[operations.Opout]struct{})}
	sender.rgbManager.rejectLists = rgb11StaticRejectLists{list: allowed}
	issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "IFA", Ticker: "RIFA", Name: "Rejectable IFA", Amounts: []uint64{100},
		InflationAmounts: []uint64{900}, RejectListURL: "https://issuer.example/reject-list",
	})
	if err != nil {
		t.Fatal(err)
	}
	receive, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", ContractID: issued.ContractID, AmountRaw: "40",
		WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: receive.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
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
	witnessTxID := witness.TxHash().String()
	recipientOutpoint := witnessTxID + ":1"
	evidence.mu.Lock()
	evidence.rawTx[witnessTxID] = append([]byte(nil), pending.SignedTx...)
	for _, input := range pending.State.InputOutPoints {
		evidence.spendingTx[input] = witnessTxID
	}
	evidence.utxos[recipientOutpoint] = &rgb11wallet.BitcoinUTXO{
		OutPoint: recipientOutpoint, Value: witness.TxOut[1].Value,
		PkScript: append([]byte(nil), witness.TxOut[1].PkScript...), Confirmations: 0,
	}
	evidence.mu.Unlock()

	diagnostic, err := recipient.ValidateRGB11Consignment(context.Background(), []byte(prepared.RecipientConsignment))
	if err != nil {
		t.Fatal(err)
	}
	var rejected operations.Opout
	for _, allocation := range diagnostic.Allocations {
		if allocation.OutPoint != recipientOutpoint {
			continue
		}
		rejected, err = operations.ParseOpout(fmt.Sprintf("%s/%d/%d",
			allocation.OperationID, allocation.AssignmentType, allocation.AssignmentIndex))
		if err != nil {
			t.Fatal(err)
		}
	}
	if rejected == (operations.Opout{}) {
		t.Fatal("recipient allocation opout not found")
	}
	recipient.rgbManager.rejectLists = rgb11StaticRejectLists{list: rejectlist.List{
		Reject: map[operations.Opout]struct{}{rejected: {}}, Allow: make(map[operations.Opout]struct{}),
	}}
	record, err := sender.BuildRGB11RelayRecord(prepared.State.TransferID, "reject-list-test")
	if err != nil {
		t.Fatal(err)
	}
	receipt, nack, err := recipient.AcceptRGB11RelayConsignment(
		context.Background(), receive.RequestID, record, []byte(prepared.RecipientConsignment),
	)
	if err != nil {
		t.Fatal(err)
	}
	if receipt != nil || nack == nil || nack.Accepted || nack.ReasonCode != RGB11RejectReasonList {
		t.Fatalf("unexpected automatic reject result: receipt=%+v nack=%+v", receipt, nack)
	}
	state, err := recipient.rgbManager.projectionStore.LoadTransferState(record.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "rejected" || state.RejectReason != RGB11RejectReasonList ||
		len(state.RejectedOpouts) != 1 || state.RejectedOpouts[0] != rejected.String() {
		t.Fatalf("unexpected receiver reject state: %+v", state)
	}
	balance, err := recipient.GetRGB11AssetBalance(&issued.AssetName)
	if err != nil || balance.Sign() != 0 {
		t.Fatalf("rejected balance=%v err=%v", balance, err)
	}
	if _, err := recipient.rgbManager.projectionStore.LoadObject(diagnostic.ConsignmentHash); err == nil {
		t.Fatal("rejected consignment was retained")
	}
}

type rgb11StatusEvidence struct {
	statuses  map[string]*rgb11wallet.BitcoinTxStatus
	outspends map[string]*rgb11wallet.BitcoinOutspend
}

func (e *rgb11StatusEvidence) GetUTXO(string) (*rgb11wallet.BitcoinUTXO, error) { return nil, nil }

func (e *rgb11StatusEvidence) GetRawTx(string) ([]byte, error) { return nil, nil }

func (e *rgb11StatusEvidence) GetTip() (*rgb11wallet.BitcoinTip, error) {
	return &rgb11wallet.BitcoinTip{}, nil
}

func (e *rgb11StatusEvidence) Broadcast([]byte) (string, error) { return "", nil }

func (e *rgb11StatusEvidence) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if status := e.statuses[txid]; status != nil {
		copy := *status
		return &copy, nil
	}
	return &rgb11wallet.BitcoinTxStatus{TxID: txid}, nil
}

func (e *rgb11StatusEvidence) GetOutspend(outpoint string) (*rgb11wallet.BitcoinOutspend, error) {
	if outspend := e.outspends[outpoint]; outspend != nil {
		copy := *outspend
		return &copy, nil
	}
	return &rgb11wallet.BitcoinOutspend{}, nil
}

func saveRGB11StatusTransfer(t *testing.T, manager *Manager, transferID, status string) string {
	t.Helper()
	recipient := []byte("recipient-consignment-" + transferID)
	hash := sha256.Sum256(recipient)
	inputHash := chainhash.DoubleHashH([]byte("input-" + transferID))
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&inputHash, 0), nil, nil))
	tx.AddTxOut(wire.NewTxOut(330, []byte{txscript.OP_TRUE}))
	var signed bytes.Buffer
	if err := tx.Serialize(&signed); err != nil {
		t.Fatal(err)
	}
	witnessTxID := tx.TxHash().String()
	inputOutpoint := tx.TxIn[0].PreviousOutPoint.String()
	pending := &rgb11wallet.PendingTransfer{
		State: rgb11wallet.TransferState{
			TransferID: transferID, Direction: "send", Status: status, AckStatus: "accepted",
			WitnessTxID: witnessTxID, InputOutPoints: []string{inputOutpoint},
			OutputOutPoints: []string{fmt.Sprintf("%s:0", witnessTxID)},
			ConsignmentHash: hex.EncodeToString(hash[:]),
		},
		RecipientConsignment: recipient,
		LocalConsignment:     []byte("local-consignment-" + transferID),
		SignedTx:             signed.Bytes(),
		SignedPSBT:           []byte{2},
	}
	if err := manager.rgbManager.projectionStore.SavePendingTransfer(pending); err != nil {
		t.Fatal(err)
	}
	return witnessTxID
}

func TestRGB11RefreshRollsSettledTransferBackAfterReorg(t *testing.T) {
	manager := newRGB11MultiDeviceManager(t, newRGB11StatusPrivateKey(t), 51)
	manager.rgbManager.evidence = &rgb11StatusEvidence{statuses: make(map[string]*rgb11wallet.BitcoinTxStatus), outspends: make(map[string]*rgb11wallet.BitcoinOutspend)}
	saveRGB11StatusTransfer(t, manager, "reorg", "settled")

	result, err := manager.RefreshRGB11State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Reorged != 1 || result.Conflicted != 0 || len(result.Inconsistent) != 0 {
		t.Fatalf("unexpected reorg result: %+v", result)
	}
	pending, err := manager.rgbManager.projectionStore.LoadPendingTransfer("reorg")
	if err != nil {
		t.Fatal(err)
	}
	if pending.State.Status != "broadcast" {
		t.Fatalf("reorged transfer status=%s", pending.State.Status)
	}
}

func TestRGB11RefreshFailsClosedOnConflictingSpend(t *testing.T) {
	manager := newRGB11MultiDeviceManager(t, newRGB11StatusPrivateKey(t), 52)
	evidence := &rgb11StatusEvidence{statuses: make(map[string]*rgb11wallet.BitcoinTxStatus), outspends: make(map[string]*rgb11wallet.BitcoinOutspend)}
	manager.rgbManager.evidence = evidence
	witnessTxID := saveRGB11StatusTransfer(t, manager, "conflict", "broadcast")
	pending, err := manager.rgbManager.projectionStore.LoadPendingTransfer("conflict")
	if err != nil {
		t.Fatal(err)
	}
	evidence.outspends[pending.State.InputOutPoints[0]] = &rgb11wallet.BitcoinOutspend{
		Spent: true, SpendingTx: witnessTxID + "-conflict",
	}

	result, err := manager.RefreshRGB11State(context.Background())
	if !errors.Is(err, ErrRGB11Inconsistent) {
		t.Fatalf("conflicting spend error=%v", err)
	}
	if result == nil || result.Conflicted != 1 || len(result.Inconsistent) != 1 || manager.GetRGB11ConsistencyStatus() != "broken" {
		t.Fatalf("unexpected conflict result=%+v consistency=%s", result, manager.GetRGB11ConsistencyStatus())
	}
	pending, err = manager.rgbManager.projectionStore.LoadPendingTransfer("conflict")
	if err != nil {
		t.Fatal(err)
	}
	if pending.State.Status != "conflicted" || pending.State.AckStatus != "invalidated" {
		t.Fatalf("conflicted transfer state=%+v", pending.State)
	}
}

func TestRGB11RefreshKeepsUnknownExpectedSpendUnresolved(t *testing.T) {
	manager := newRGB11MultiDeviceManager(t, newRGB11StatusPrivateKey(t), 54)
	evidence := &rgb11StatusEvidence{
		statuses:  make(map[string]*rgb11wallet.BitcoinTxStatus),
		outspends: make(map[string]*rgb11wallet.BitcoinOutspend),
	}
	manager.rgbManager.evidence = evidence
	saveRGB11StatusTransfer(t, manager, "unresolved", "broadcast")
	pending, err := manager.rgbManager.projectionStore.LoadPendingTransfer("unresolved")
	if err != nil {
		t.Fatal(err)
	}
	evidence.outspends[pending.State.InputOutPoints[0]] = &rgb11wallet.BitcoinOutspend{Spent: true}

	result, err := manager.RefreshRGB11State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Unresolved != 1 || result.Conflicted != 0 || len(result.Inconsistent) != 0 ||
		manager.GetRGB11ConsistencyStatus() != "warning" {
		t.Fatalf("unexpected unresolved result=%+v consistency=%s", result, manager.GetRGB11ConsistencyStatus())
	}
	stored, err := manager.rgbManager.projectionStore.LoadPendingTransfer("unresolved")
	if err != nil || stored.State.Status != "broadcast" {
		t.Fatalf("unresolved transfer state=%+v err=%v", stored, err)
	}
}

func TestRGB11LockRebuildDoesNotRequireBitcoinEvidence(t *testing.T) {
	manager := newRGB11MultiDeviceManager(t, newRGB11StatusPrivateKey(t), 55)
	manager.utxoLockerL1 = NewUtxoLocker(manager.db, nil, L1_NETWORK_BITCOIN)
	saveRGB11StatusTransfer(t, manager, "offline-restore", "broadcast")
	pending, err := manager.rgbManager.projectionStore.LoadPendingTransfer("offline-restore")
	if err != nil {
		t.Fatal(err)
	}
	manager.rgbManager.evidence = nil

	if err := manager.rgbManager.rebuildRGB11Locks(); err != nil {
		t.Fatal(err)
	}
	lock := manager.utxoLockerL1.GetLockedUtxoList()[pending.State.InputOutPoints[0]]
	if lock == nil || lock.Reason != rgb11wallet.LockReasonPending {
		t.Fatalf("offline restored input lock=%+v", lock)
	}
}

func TestRGB11RefreshTracksIncomingTransferConfirmationAndReorg(t *testing.T) {
	manager := newRGB11MultiDeviceManager(t, newRGB11StatusPrivateKey(t), 53)
	evidence := &rgb11StatusEvidence{statuses: make(map[string]*rgb11wallet.BitcoinTxStatus), outspends: make(map[string]*rgb11wallet.BitcoinOutspend)}
	manager.rgbManager.evidence = evidence
	state := &rgb11wallet.TransferState{
		TransferID: "incoming", Direction: "receive", Status: "pending", AckStatus: "accepted",
		WitnessTxID: "incoming-witness", OutputOutPoints: []string{"incoming-witness:1"}, MinConfirmations: 2,
	}
	if err := manager.rgbManager.projectionStore.SaveTransferState(state); err != nil {
		t.Fatal(err)
	}
	evidence.statuses[state.WitnessTxID] = &rgb11wallet.BitcoinTxStatus{TxID: state.WitnessTxID, Confirmed: true, Confirmations: 1}
	result, err := manager.RefreshRGB11State(context.Background())
	if err != nil || result.Pending != 1 || result.Settled != 0 {
		t.Fatalf("incoming minimum-confirmation result=%+v err=%v", result, err)
	}
	evidence.statuses[state.WitnessTxID] = &rgb11wallet.BitcoinTxStatus{TxID: state.WitnessTxID, Confirmed: true, Confirmations: 2}
	result, err = manager.RefreshRGB11State(context.Background())
	if err != nil || result.Settled != 1 {
		t.Fatalf("incoming settlement result=%+v err=%v", result, err)
	}
	stored, err := manager.rgbManager.projectionStore.LoadTransferState(state.TransferID)
	if err != nil || stored.Status != "settled" {
		t.Fatalf("incoming settled state=%+v err=%v", stored, err)
	}
	evidence.statuses[state.WitnessTxID] = &rgb11wallet.BitcoinTxStatus{TxID: state.WitnessTxID}
	result, err = manager.RefreshRGB11State(context.Background())
	if err != nil || result.Reorged != 1 || result.Pending != 1 {
		t.Fatalf("incoming reorg result=%+v err=%v", result, err)
	}
	stored, err = manager.rgbManager.projectionStore.LoadTransferState(state.TransferID)
	if err != nil || stored.Status != "pending" {
		t.Fatalf("incoming reorged state=%+v err=%v", stored, err)
	}
}

func newRGB11StatusPrivateKey(t *testing.T) *btcec.PrivateKey {
	t.Helper()
	privateKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return privateKey
}
