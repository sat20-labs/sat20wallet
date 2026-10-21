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
	"github.com/sat20-labs/rgb11/rejectlist"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/btcec"
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
	mu             sync.Mutex
	utxos          map[string]*rgb11wallet.BitcoinUTXO
	rawTx          map[string][]byte
	txStatuses     map[string]*rgb11wallet.BitcoinTxStatus
	spendingTx     map[string]string
	broadcasted    []byte
	broadcastCount int
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
	if status := e.txStatuses[txid]; status != nil {
		copy := *status
		return &copy, nil
	}
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
	e.broadcastCount++
	e.mu.Unlock()
	return tx.TxHash().String(), nil
}

func newRGB11FlowManager(t *testing.T, wallet common.Wallet, rpc IndexerRPCClient,
	evidence rgb11wallet.BitcoinEvidenceProvider, localWalletID int64) *Manager {
	return newRGB11FlowManagerAt(t, wallet, rpc, evidence, localWalletID, t.TempDir())
}

func newRGB11FlowManagerAt(t *testing.T, wallet common.Wallet, rpc IndexerRPCClient,
	evidence rgb11wallet.BitcoinEvidenceProvider, localWalletID int64, dbPath string) *Manager {
	t.Helper()
	database := indexerdb.NewKVDB(dbPath)
	locker := NewUtxoLocker(database, rpc, L1_NETWORK_BITCOIN)
	l1 := NewIndexerRPCClientMgr()
	l1.Set(rpc)
	accountIndex := wallet.GetSubAccount()
	manager := &Manager{
		db: database, wallet: wallet,
		status: &Status{CurrentWallet: localWalletID, CurrentAccount: accountIndex},
		walletInfoMap: map[int64]*WalletInfo{localWalletID: {
			WalletInDB: WalletInDB{Id: localWalletID, Accounts: int(accountIndex) + 1, Type: WALLET_TYPE_MNEMONIC},
			Wallet:     wallet,
		}},
		tickerInfoMap: make(map[string]*indexer.TickerInfo), utxoLockerL1: locker,
		l1IndexerClient: l1,
	}
	t.Cleanup(func() {
		if manager.db != nil {
			_ = manager.db.Close()
		}
	})
	rgbManager, err := newRGB11Manager(manager, database, locker, evidence)
	if err != nil {
		t.Fatal(err)
	}
	rgbManager.consistencyStatus = "ok"
	manager.rgbManager = rgbManager
	if err := manager.rgbManager.selectRGB11Scope(); err != nil {
		t.Fatal(err)
	}
	initialRGBManager := rgbManager
	t.Cleanup(func() {
		// Tests may replace manager.rgbManager to simulate an SDK restart. Stop
		// both the original runtime and the final runtime before the DB cleanup.
		if initialRGBManager != nil && initialRGBManager.scopeStates != nil {
			initialRGBManager.scopeStates.stopReconciliations()
		}
		if current := manager.rgbManager; current != nil && current != initialRGBManager &&
			current.scopeStates != nil {
			current.scopeStates.stopReconciliations()
		}
	})
	return manager
}

func assertRGB11PreparedConsignmentUsesUnsignedWitness(t *testing.T, raw string) {
	t.Helper()
	container, err := coreconsignment.DecodeArmor(raw)
	if err != nil {
		t.Fatal(err)
	}
	bundles, ok := container.Value.Field("bundles")
	bundles = bundles.Unwrap()
	if !ok || len(bundles.Items) == 0 {
		t.Fatal("prepared consignment has no witness bundle")
	}
	for _, bundle := range bundles.Items {
		publicWitness, ok := bundle.Unwrap().Field("pubWitness")
		publicWitness = publicWitness.Unwrap()
		if !ok || publicWitness.Name != "tx" || publicWitness.Inner == nil {
			t.Fatalf("prepared consignment public witness is not an embedded transaction: %+v", publicWitness)
		}
		inputs, ok := publicWitness.Inner.Unwrap().Field("inputs")
		inputs = inputs.Unwrap()
		if !ok || len(inputs.Items) == 0 {
			t.Fatal("prepared public witness has no transaction inputs")
		}
		for _, item := range inputs.Items {
			input := item.Unwrap()
			sigScript, sigOK := input.Field("sigScript")
			witness, witnessOK := input.Field("witness")
			sigBytes, bytesOK := sigScript.Bytes()
			witness = witness.Unwrap()
			if !sigOK || !witnessOK || !bytesOK || len(sigBytes) != 0 || len(witness.Items) != 0 {
				t.Fatal("standard acknowledged consignment exposed a signed transaction input")
			}
		}
	}
}

func TestRGB11StandardOutOfBandPrepareBroadcastAccept(t *testing.T) {
	sender, recipient, imported, evidence, _ := newRGB11GenericSendFixture(t)
	sourceOutpoint := rgb11GenericFixtureSource(t, sender, imported)
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
	assertRGB11PreparedConsignmentUsesUnsignedWitness(t, prepared.RecipientConsignment)
	witness := wire.NewMsgTx(wire.TxVersion)
	if err := witness.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	hasSignature := false
	for _, input := range witness.TxIn {
		if len(input.SignatureScript) != 0 || len(input.Witness) != 0 {
			hasSignature = true
			break
		}
	}
	if !hasSignature {
		t.Fatal("sender did not retain the signed witness transaction")
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

	_, err = recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "unsupported", ContractID: imported.ContractID,
		AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err == nil {
		t.Fatal("unsupported invoice transport was accepted")
	}
	assertRGB11FrameworkRecovery(t, sender, recipient)
}

func TestRGB11StandardBlindOutOfBandPrepareWithoutBroadcast(t *testing.T) {
	sender, recipient, imported, evidence, rpc := newRGB11GenericSendFixture(t)
	recipientScript, err := AddrToPkScript(recipient.wallet.GetAddress(), GetChainParam())
	if err != nil {
		t.Fatal(err)
	}
	recipientSealOutpoint := seedRGB11FlowEvidenceUTXO(t, evidence.rgb11FlowEvidence, recipientScript, 100_000, 93)
	recipientSeal := indexer.NewTxOutput(100_000)
	recipientSeal.OutPointStr, recipientSeal.OutValue.PkScript = recipientSealOutpoint, append([]byte(nil), recipientScript...)
	rpc.outputs[recipientSealOutpoint] = recipientSeal
	rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{
		OutPoint: recipientSealOutpoint, Value: 100_000, PkScript: append([]byte(nil), recipientScript...),
	})
	request, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "blind", TransportMode: "out-of-band", ContractID: imported.ContractID,
		AmountRaw: "1000", Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := invoicing.Parse(request.Invoice)
	if err != nil || parsed.Beneficiary.Kind != invoicing.BeneficiaryBlindedSeal || len(parsed.Transports) != 0 {
		t.Fatalf("unexpected standard blind invoice: %+v err=%v", parsed, err)
	}
	invalid := *parsed
	invalid.Beneficiary.Kind = 0
	if _, _, _, _, _, _, err := validateRGB11SendInvoice(&invalid, nil, nil); !errors.Is(err, invoicing.ErrInvalidInvoice) {
		t.Fatalf("invalid beneficiary accepted: %v", err)
	}
	reservations, err := recipient.rgbManager.projectionStore.ListReceiveReservations()
	if err != nil || len(reservations) != 1 || reservations[0].RequestID != request.RequestID ||
		reservations[0].OutPoint != recipientSealOutpoint {
		t.Fatalf("unexpected blind receive reservation: %+v err=%v", reservations, err)
	}

	if _, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoices: []string{request.Invoice, request.Invoice}, FeeRate: 2, MinConfirmations: 1,
	}); err == nil || !strings.Contains(err.Error(), "batch send requires witness invoices") {
		t.Fatalf("standard blind batch was not rejected: %v", err)
	}
	prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: request.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.State.TransportMode != "out-of-band" || prepared.State.RecipientVout != 0 {
		t.Fatalf("unexpected standard blind prepared state: %+v", prepared.State)
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
		t.Fatalf("standard blind transfer created a recipient witness output: %+v", witness.TxOut)
	}
	if len(evidence.broadcasted) != 0 {
		t.Fatal("prepare unexpectedly broadcast the blind transfer")
	}
	receipt, err := recipient.PrepareRGB11Consignment(
		context.Background(), request.RequestID, []byte(prepared.RecipientConsignment),
	)
	if err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, allocation := range receipt.Allocations {
		if allocation.OutPoint == recipientSealOutpoint && allocation.Amount.Value != nil &&
			allocation.Amount.Value.Uint64() == 1000 {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("prepared blind consignment does not target reserved seal: %+v", receipt.Allocations)
	}
	state, err := recipient.GetRGB11State()
	if err != nil || len(state.Transfers) != 1 || state.Transfers[0].Status != "awaiting_broadcast" ||
		state.Transfers[0].TransportMode != "out-of-band" ||
		!slices.Equal(state.Transfers[0].OutputOutPoints, []string{recipientSealOutpoint}) {
		t.Fatalf("unexpected prepared blind receive state: %+v err=%v", state, err)
	}
	if len(evidence.broadcasted) != 0 {
		t.Fatal("recipient prepare unexpectedly broadcast the blind transfer")
	}
	assertRGB11FrameworkRecovery(t, sender, recipient)
}

func TestRGB11SelfOutOfBandListsSenderAndReceiverStates(t *testing.T) {
	manager, _, imported, _, _ := newRGB11GenericSendFixture(t)
	const amount = uint64(20_000)
	request, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID,
		AmountRaw: fmt.Sprint(amount), WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := manager.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: request.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := manager.PrepareRGB11Consignment(
		context.Background(), request.RequestID, []byte(prepared.RecipientConsignment),
	)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.TransferID != prepared.State.TransferID {
		t.Fatalf("self-transfer receipt id=%s sender id=%s", receipt.TransferID, prepared.State.TransferID)
	}

	state, err := manager.GetRGB11State()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Transfers) != 2 {
		t.Fatalf("self-transfer states=%+v", state.Transfers)
	}
	receive, send := state.Transfers[0], state.Transfers[1]
	if receive.TransferID != send.TransferID || receive.TransferID != prepared.State.TransferID ||
		receive.Direction != "receive" || send.Direction != "send" {
		t.Fatalf("self-transfer order or identity receive=%+v send=%+v", receive, send)
	}
	if receive.Status != "awaiting_broadcast" || len(receive.OutputOutPoints) != 1 ||
		receive.OutputOutPoints[0] != fmt.Sprintf("%s:%d", prepared.State.WitnessTxID, prepared.State.RecipientVout) {
		t.Fatalf("self-transfer receive state=%+v", receive)
	}
	if send.Status != "prepared" || len(send.OutputOutPoints) < 2 || send.OutputOutPoints[0] != receive.OutputOutPoints[0] {
		t.Fatalf("self-transfer send state=%+v", send)
	}
	if receive.Invoice != send.Invoice || receive.ConsignmentHash != send.ConsignmentHash ||
		receive.WitnessTxID != send.WitnessTxID || receive.Asset.Name != send.Asset.Name ||
		receive.Asset.Amount.Cmp(&send.Asset.Amount) != 0 {
		t.Fatalf("self-transfer binding receive=%+v send=%+v", receive, send)
	}
	if _, err := manager.rgbManager.projectionStore.LoadObject(receive.ConsignmentHash); err != nil {
		t.Fatalf("load prepared receive object before sender cancellation: %v", err)
	}
	if err := manager.CancelRGB11OutOfBandTransfer(send.TransferID); err != nil {
		t.Fatal(err)
	}
	compacted, err := manager.rgbManager.projectionStore.LoadPendingTransfer(send.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if compacted.State.Status != "rejected" || compacted.State.RejectReason != RGB11RejectReasonUser ||
		len(compacted.SignedTx) != 0 || len(compacted.SignedPSBT) != 0 ||
		len(compacted.RecipientConsignment) != 0 || len(compacted.LocalConsignment) != 0 {
		t.Fatalf("sender cancellation was not compacted: %+v", compacted)
	}
	if _, err := manager.rgbManager.projectionStore.LoadObject(receive.ConsignmentHash); err != nil {
		t.Fatalf("sender cancellation deleted live receive object: %v", err)
	}
	refresh, err := manager.RefreshRGB11State(context.Background())
	if err != nil {
		t.Fatalf("refresh live self-receive after sender cancellation: %v", err)
	}
	if refresh.Pending != 1 || manager.GetRGB11ConsistencyStatus() != "ok" {
		t.Fatalf("refresh=%+v consistency=%s", refresh, manager.GetRGB11ConsistencyStatus())
	}
}

func TestRGB11CancelPreparedOutOfBandTransfer(t *testing.T) {
	manager, recipient, imported, evidence, _ := newRGB11GenericSendFixtureWithCarrier(t, 330)
	rgbInput := rgb11GenericFixtureSource(t, manager, imported)
	prepare := func() *rgb11wallet.PendingTransfer {
		t.Helper()
		invoice, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
			Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID,
			AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
		})
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := manager.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
			Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		pending, err := manager.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending.State.InputOutPoints) != 2 || !slices.Contains(pending.State.InputOutPoints, rgbInput) {
			t.Fatalf("cancellation fixture needs RGB and plain fee inputs: %+v", pending.State)
		}
		return pending
	}
	firstPending := prepare()
	feeInput := firstPending.State.InputOutPoints[0]
	if feeInput == rgbInput {
		feeInput = firstPending.State.InputOutPoints[1]
	}
	if err := manager.CancelRGB11OutOfBandTransfer(firstPending.State.TransferID); err != nil {
		t.Fatal(err)
	}
	stored, err := manager.rgbManager.projectionStore.LoadPendingTransfer(firstPending.State.TransferID)
	if err != nil || stored.State.Status != "rejected" || len(stored.SignedTx) != 0 {
		t.Fatalf("cancelled transfer=%+v err=%v", stored, err)
	}
	locks := manager.utxoLockerL1.GetLockedUtxoList()
	if locks[rgbInput] == nil || locks[rgbInput].Reason != rgb11wallet.LockReasonRGB || locks[feeInput] != nil {
		t.Fatalf("unexpected locks after cancellation: %+v", locks)
	}

	localPending := prepare()
	// Even misleading or stale network evidence must not be consulted for a
	// locally prepared transfer: the durable broadcast marker is the boundary.
	evidence.mu.Lock()
	evidence.rawTx[localPending.State.WitnessTxID] = []byte{1}
	evidence.mu.Unlock()
	if err := manager.CancelRGB11OutOfBandTransfer(localPending.State.TransferID); err != nil {
		t.Fatalf("local prepared transfer cancellation error=%v", err)
	}
	stored, err = manager.rgbManager.projectionStore.LoadPendingTransfer(localPending.State.TransferID)
	if err != nil || stored.State.Status != "rejected" {
		t.Fatalf("locally cancelled transfer=%+v err=%v", stored, err)
	}
}

func TestRGB11GenericBlindedProxyInvoiceUsesExplicitAssetAndNoRecipientOutput(t *testing.T) {
	sender, _, imported, _, _ := newRGB11GenericSendFixture(t)

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

func TestRGB11IssueFirstReleaseSchemas(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
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
				outpoint := seedRGB11FlowEvidenceUTXO(t, evidence, walletScript, 10_000, byte(110+testIndex*10+index))
				output := indexer.NewTxOutput(10_000)
				output.OutPointStr = outpoint
				output.OutValue.PkScript = append([]byte(nil), walletScript...)
				rpc.outputs[outpoint] = output
				rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{OutPoint: outpoint, Value: 10_000, PkScript: walletScript})
				evidence.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
					OutPoint: outpoint, Value: 10_000, PkScript: walletScript, Confirmations: 6,
				}
			}
			remote := newRGB11MemoryDKVSHTTP()
			client := newRGB11MessageNodeClient(remote)
			manager := newRGB11ActivatedFlowAccount(t, "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", remote, client, evidence, rpc)
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
			exported, err := manager.ExportRGB11Contract(issued.ContractID)
			if err != nil {
				t.Fatal(err)
			}
			exportedFile, err := base64.StdEncoding.DecodeString(exported.ContractConsignmentBase64)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(exportedFile, contractFile) || !bytes.HasPrefix(exportedFile, []byte("RGB\x00CON")) ||
				exported.ContractID != issued.ContractID || exported.SchemaID != issued.SchemaID || exported.Armor == "" {
				t.Fatalf("wallet contract export mismatch: %+v", exported)
			}
			digest := sha256.Sum256(exportedFile)
			if exported.SHA256 != hex.EncodeToString(digest[:]) {
				t.Fatalf("wallet contract export digest=%s want=%x", exported.SHA256, digest)
			}
			imported, err := manager.ImportRGB11ContractFile(context.Background(), contractFile)
			if err != nil {
				t.Fatal(err)
			}
			if imported.ContractID != issued.ContractID {
				t.Fatalf("standard contract import=%s want=%s", imported.ContractID, issued.ContractID)
			}
			for _, armor := range []string{issued.Armor, exported.Armor} {
				armoredImport, err := manager.ImportRGB11Contract(context.Background(), []byte(armor))
				if err != nil {
					t.Fatalf("repeat official Armor import: %v", err)
				}
				if armoredImport.ContractID != issued.ContractID ||
					armoredImport.Receipt.ConsignmentHash != hex.EncodeToString(digest[:]) {
					t.Fatal("binary and Armor imports did not preserve the same material identity")
				}
			}
			viewer := newRGB11ActivatedFlowAccount(t, "comfort very add tuition senior run eight snap burst appear exile dutch", remote, client, evidence, rpc)
			if _, err := viewer.ImportRGB11ContractFile(context.Background(), contractFile); err != nil {
				t.Fatal(err)
			}
			viewerState, err := viewer.GetRGB11State()
			if err != nil {
				t.Fatal(err)
			}
			if len(viewerState.Assets) != 0 || len(viewerState.TickerInfos) != 1 ||
				viewerState.TickerInfos[0].ContractID != issued.ContractID {
				t.Fatalf("zero-balance imported contract missing from state: %+v", viewerState)
			}
			expectedName, err := rgb11wallet.NewContractAssetKey(issued.ContractID, issued.AssetName.Type)
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
			expectedLabel, err := rgb11wallet.BuildLocalDisplayName(test.request.Ticker, wallet.GetAddress(), "")
			if err != nil {
				t.Fatal(err)
			}
			if len(state.TickerInfos) != 1 || state.TickerInfos[0].AssetKey != issued.AssetName.String() ||
				state.TickerInfos[0].ContractID != issued.ContractID || state.TickerInfos[0].Ticker != expectedLabel ||
				state.TickerInfos[0].CanonicalName != "" || state.TickerInfos[0].Verified {
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
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
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
	remote := newRGB11MemoryDKVSHTTP()
	client := newRGB11MessageNodeClient(remote)
	manager = newRGB11ActivatedFlowAccount(t,
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		remote, client, evidence, rpc)
	if manager.wallet.GetAddress() != wallet.GetAddress() {
		t.Fatal("activated fixture changed the original issuer identity")
	}
	_, err = manager.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "CFA", Name: "SAT20 CFA", Amounts: []uint64{1},
	})
	if !errors.Is(err, coreissuance.ErrUnsupportedSchema) {
		t.Fatalf("CFA issuance error=%v, want %v", err, coreissuance.ErrUnsupportedSchema)
	}
}

func TestRGB11IssuedUDASendReceive(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
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
	evidence := &rgb11FlowEvidence{utxos: make(map[string]*rgb11wallet.BitcoinUTXO), rawTx: make(map[string][]byte), spendingTx: make(map[string]string)}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	sourceOutpoint := seedRGB11FlowEvidenceUTXO(t, evidence, senderScript, 10_000, 151)
	sourceOutput := indexer.NewTxOutput(10_000)
	sourceOutput.OutPointStr, sourceOutput.OutValue.PkScript = sourceOutpoint, senderScript
	rpc.outputs[sourceOutpoint] = sourceOutput
	rpc.plain = []*indexerwire.TxOutputInfo{{OutPoint: sourceOutpoint, Value: 10_000, PkScript: senderScript}}
	remote := newRGB11MemoryDKVSHTTP()
	client := newRGB11MessageNodeClient(remote)
	sender := newRGB11ActivatedFlowAccount(t, "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", remote, client, evidence, rpc)
	recipient := newRGB11ActivatedFlowAccount(t, "comfort very add tuition senior run eight snap burst appear exile dutch", remote, client, evidence, rpc)
	issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "UDA", Ticker: "SUDA", Name: "SAT20 Unique", Amounts: []uint64{1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(issued.OutPoints) != 1 || issued.OutPoints[0] != sourceOutpoint {
		t.Fatal("UDA issuance selected a different carrier")
	}
	feeOutpoint := seedRGB11FlowEvidenceUTXO(t, evidence, senderScript, 100_000, 152)
	feeOutput := indexer.NewTxOutput(100_000)
	feeOutput.OutPointStr, feeOutput.OutValue.PkScript = feeOutpoint, senderScript
	rpc.outputs[feeOutpoint] = feeOutput
	rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{OutPoint: feeOutpoint, Value: 100_000, PkScript: senderScript})
	receive, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
		AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
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
	if _, err := recipient.PrepareRGB11Consignment(context.Background(), receive.RequestID, []byte(prepared.RecipientConsignment)); err != nil {
		t.Fatal(err)
	}
	if _, err := sender.BroadcastRGB11OutOfBand([]string{prepared.State.TransferID}); err != nil {
		t.Fatal(err)
	}
	witnessTxID := witness.TxHash().String()
	evidence.mu.Lock()
	evidence.rawTx[witnessTxID] = append([]byte(nil), pending.SignedTx...)
	for _, input := range witness.TxIn {
		point := input.PreviousOutPoint.String()
		evidence.spendingTx[point] = witnessTxID
		delete(evidence.utxos, point)
		delete(rpc.outputs, point)
	}
	rpc.plain = nil
	for vout, txOut := range witness.TxOut {
		point := fmt.Sprintf("%s:%d", witnessTxID, vout)
		evidence.utxos[point] = &rgb11wallet.BitcoinUTXO{OutPoint: point, Value: txOut.Value, PkScript: append([]byte(nil), txOut.PkScript...), Confirmations: 0}
		output := indexer.NewTxOutput(txOut.Value)
		output.OutPointStr, output.OutValue.PkScript = point, append([]byte(nil), txOut.PkScript...)
		rpc.outputs[point] = output
	}
	evidence.mu.Unlock()
	receipt, err := recipient.AcceptRGB11Consignment(context.Background(), receive.RequestID, []byte(prepared.RecipientConsignment))
	if err != nil {
		t.Fatal(err)
	}
	recipientState, err := recipient.GetRGB11State()
	if err != nil {
		t.Fatal(err)
	}
	if len(recipientState.TickerInfos) != 1 || recipientState.TickerInfos[0].ContractID != issued.ContractID {
		t.Fatalf("received contract metadata missing: %+v", recipientState.TickerInfos)
	}
	if receipt.ContractID != issued.ContractID || len(receipt.Allocations) != 1 || receipt.Allocations[0].StateClass != "structured" {
		t.Fatalf("unexpected UDA receipt: %+v", receipt)
	}
	balance, err := recipient.GetRGB11AssetBalance(&issued.AssetName)
	if err != nil || balance.Value.Uint64() != 1 {
		t.Fatalf("recipient UDA balance=%+v err=%v", balance, err)
	}
	exported, err := recipient.ExportRGB11Contract(issued.ContractID)
	if err != nil {
		t.Fatalf("recipient must export the contract from received validated history: %v", err)
	}
	exportedFile, err := base64.StdEncoding.DecodeString(exported.ContractConsignmentBase64)
	if err != nil {
		t.Fatal(err)
	}
	exportedContract, err := coreconsignment.DecodeFile(exportedFile)
	if err != nil {
		t.Fatal(err)
	}
	if exportedContract.Armor == nil || exportedContract.Armor.Type != "contract" ||
		exportedContract.ContractID != issued.ContractID || !bytes.HasPrefix(exportedFile, []byte("RGB\x00CON")) {
		t.Fatalf("recipient contract export is not canonical: %+v", exported)
	}
	issuedFile, err := base64.StdEncoding.DecodeString(issued.ContractConsignmentBase64)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(exportedFile, issuedFile) {
		t.Fatal("recipient export differs from the canonical issued contract")
	}

	// Stable recovery preserves the imported contract identity and its complete
	// referenced object, including zero-balance contract intent. Ownership still
	// comes from the validated transfer and fresh Bitcoin evidence.
	evidence.mu.Lock()
	evidence.txStatuses[witnessTxID] = &rgb11wallet.BitcoinTxStatus{TxID: witnessTxID, Confirmed: true, Confirmations: 1, BlockHeight: 1, BlockHash: "test-tip"}
	for point, utxo := range evidence.utxos {
		if strings.HasPrefix(point, witnessTxID+":") {
			utxo.Confirmations = 1
		}
	}
	evidence.mu.Unlock()
	if _, err := recipient.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	proof, err := recipient.rgbManager.projectionStore.LoadProof(receipt.Allocations[0].OutPoint, receipt.Allocations[0].AssetName)
	if err != nil || proof.Status != "settled" || proof.Confirmations != 1 {
		t.Fatalf("UDA confirmation was not projected: proof=%+v err=%v", proof, err)
	}
	contractObjectHash, err := recipient.rgbManager.projectionStore.LoadContractObjectReference(issued.ContractID)
	if err != nil || contractObjectHash == "" {
		t.Fatalf("recipient canonical contract reference missing before recovery: hash=%s err=%v", contractObjectHash, err)
	}
	walletID, err := recipient.rgbManager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	fullSnapshot, _, err := recipient.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := rgb11wallet.RecoveryPackageFromSnapshot(fullSnapshot, 0)
	if err != nil {
		t.Fatal(err)
	}
	hasContractReference, hasContractObject := false, false
	for _, record := range recovery.ProjectionRecords {
		hasContractReference = hasContractReference || strings.HasPrefix(record.Key, "contract-object-")
		hasContractObject = hasContractObject || record.Key == "object-"+contractObjectHash
	}
	if !hasContractReference || !hasContractObject {
		t.Fatal("stable recovery lost the contract reference or its object")
	}
	recoverySnapshot, err := recovery.WalletSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	recoveredWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
	)
	if recoveredWallet == nil {
		t.Fatal("create recovered recipient wallet")
	}
	recovered := newRGB11FlowManager(t, recoveredWallet, rpc, evidence, 33)
	if err := recovered.rgbManager.importRGB11WalletSnapshot(recoverySnapshot); err != nil {
		t.Fatal(err)
	}
	if ref, err := recovered.rgbManager.projectionStore.LoadContractObjectReference(issued.ContractID); err != nil || ref != contractObjectHash {
		t.Fatalf("recovery lost the retained contract reference: err=%v", err)
	}
	recoveredExport, err := recovered.ExportRGB11Contract(issued.ContractID)
	if err != nil {
		t.Fatalf("recovered wallet must export its canonical contract: %v", err)
	}
	recoveredFile, err := base64.StdEncoding.DecodeString(recoveredExport.ContractConsignmentBase64)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(recoveredFile, exportedFile) {
		t.Fatal("recovered wallet exported a different canonical contract")
	}
}

func TestRGB11IssuedFungibleFirstReleaseSchemasSendReceive(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	tests := []struct {
		name        string
		request     RGB11IssueRequest
		sendAmount  uint64
		walletScope int64
		receiveMode corewallet.ReceiveMode
	}{
		{"NIA", RGB11IssueRequest{Schema: "NIA", Ticker: "TNIA", Name: "Transfer NIA", Precision: 2, Amounts: []uint64{100}}, 40, 40, corewallet.ReceiveWitness},
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
				value := int64(100_000)
				outpoint := seedRGB11FlowEvidenceUTXO(t, evidence, senderScript, value, byte(170+testIndex*10+index))
				evidence.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
					OutPoint: outpoint, Value: value, PkScript: senderScript, Confirmations: 6,
				}
				rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{OutPoint: outpoint, Value: value, PkScript: senderScript})
				output := indexer.NewTxOutput(value)
				output.OutPointStr = outpoint
				output.OutValue.PkScript = append([]byte(nil), senderScript...)
				rpc.outputs[outpoint] = output
			}
			remote := newRGB11MemoryDKVSHTTP()
			client := newRGB11MessageNodeClient(remote)
			sender := newRGB11ActivatedFlowAccount(t, "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", remote, client, evidence, rpc)
			recipient := newRGB11ActivatedFlowAccount(t, "comfort very add tuition senior run eight snap burst appear exile dutch", remote, client, evidence, rpc)
			issued, err := sender.IssueRGB11Asset(context.Background(), test.request)
			if err != nil {
				t.Fatal(err)
			}
			receive, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
				Mode: string(test.receiveMode), TransportMode: "out-of-band", ContractID: issued.ContractID,
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
			if _, err := recipient.PrepareRGB11Consignment(context.Background(), receive.RequestID, []byte(prepared.RecipientConsignment)); err != nil {
				t.Fatal(err)
			}
			if _, err := sender.BroadcastRGB11OutOfBand([]string{prepared.State.TransferID}); err != nil {
				t.Fatal(err)
			}
			witnessTxID := witness.TxHash().String()
			recipientOutpoint := witnessTxID + ":1"
			originalInputs := make(map[string]*rgb11wallet.BitcoinUTXO)
			for _, input := range pending.State.InputOutPoints {
				previous, err := evidence.GetUTXO(input)
				if err != nil || previous == nil {
					t.Fatalf("original input evidence missing: %v", err)
				}
				originalInputs[input] = previous
			}
			evidence.mu.Lock()
			evidence.rawTx[witnessTxID] = append([]byte(nil), pending.SignedTx...)
			for _, input := range witness.TxIn {
				point := input.PreviousOutPoint.String()
				evidence.spendingTx[point] = witnessTxID
				delete(evidence.utxos, point)
				delete(rpc.outputs, point)
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

			t.Run("exception_evicted_witness", func(t *testing.T) {
				evidence.mu.Lock()
				delete(evidence.rawTx, witnessTxID)
				// A positive absent-transaction response is different from a read error.
				evidence.txStatuses[witnessTxID] = &rgb11wallet.BitcoinTxStatus{TxID: witnessTxID}
				for _, input := range pending.State.InputOutPoints {
					delete(evidence.spendingTx, input)
					previous := originalInputs[input]
					evidence.utxos[input] = previous
					view := indexer.NewTxOutput(previous.Value)
					view.OutPointStr, view.OutValue.PkScript = input, append([]byte(nil), previous.PkScript...)
					rpc.outputs[input] = view
				}
				for output := range evidence.utxos {
					if strings.HasPrefix(output, witnessTxID+":") {
						delete(evidence.utxos, output)
					}
				}
				evidence.mu.Unlock()
				refresh, err = sender.RefreshRGB11State(context.Background())
				// Mempool eviction of an unconfirmed transfer is not a chain reorg.
				if err != nil || refresh == nil || refresh.Settled != 0 || refresh.Conflicted != 0 || refresh.Unresolved != 1 {
					t.Fatalf("refresh evicted %s transfer result=%+v err=%v", test.name, refresh, err)
				}
				proofs, err = sender.rgbManager.projectionStore.ListProofs()
				if err != nil {
					t.Fatal(err)
				}
				reservedInputs := make(map[string]bool, len(pending.State.InputOutPoints))
				for _, proof := range proofs {
					if strings.HasPrefix(proof.OutPoint, witnessTxID+":") && proof.Status != "orphaned" {
						t.Fatalf("%s evicted staged projection remains current: %+v", test.name, proof)
					}
					if slices.Contains(pending.State.InputOutPoints, proof.OutPoint) {
						if proof.Status != "spending" && proof.Status != "valid" && proof.Status != "settled" {
							t.Fatalf("%s evicted signed transfer lost original proof %+v", test.name, proof)
						}
						reservedInputs[proof.OutPoint] = true
					}
				}
				state, stateErr := sender.GetRGB11State()
				if stateErr != nil {
					t.Fatal(stateErr)
				}
				for _, available := range state.AvailableAssets {
					if available.Name == issued.AssetName && available.Amount.Value.Sign() > 0 {
						t.Fatal("evicted signed transfer made RGB available for a competing spend")
					}
				}
				for _, input := range pending.State.InputOutPoints {
					lock := sender.utxoLockerL1.GetLockedUtxoList()[input]
					if !reservedInputs[input] || lock == nil || lock.Reason != rgb11wallet.LockReasonPending {
						t.Fatalf("%s evicted signed transfer lost input reservation %s: proof=%t lock=%+v",
							test.name, input, reservedInputs[input], lock)
					}
				}
			})
		})
	}
}

type rgb11StatusEvidence struct {
	base      rgb11wallet.BitcoinEvidenceProvider
	statuses  map[string]*rgb11wallet.BitcoinTxStatus
	outspends map[string]*rgb11wallet.BitcoinOutspend
}

func (e *rgb11StatusEvidence) GetUTXO(outpoint string) (*rgb11wallet.BitcoinUTXO, error) {
	if e.base != nil {
		return e.base.GetUTXO(outpoint)
	}
	return nil, nil
}

func (e *rgb11StatusEvidence) GetRawTx(txid string) ([]byte, error) {
	if e.base != nil {
		return e.base.GetRawTx(txid)
	}
	return nil, nil
}

func (e *rgb11StatusEvidence) GetTip() (*rgb11wallet.BitcoinTip, error) {
	return &rgb11wallet.BitcoinTip{Height: 100, BlockHash: strings.Repeat("1", 64)}, nil
}

func (e *rgb11StatusEvidence) Broadcast([]byte) (string, error) { return "", nil }

func (e *rgb11StatusEvidence) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if status := e.statuses[txid]; status != nil {
		copyValue := *status
		return &copyValue, nil
	}
	if e.base != nil {
		return e.base.GetTxStatus(txid)
	}
	return &rgb11wallet.BitcoinTxStatus{TxID: txid}, nil
}

func (e *rgb11StatusEvidence) GetOutspend(outpoint string) (*rgb11wallet.BitcoinOutspend, error) {
	if outspend := e.outspends[outpoint]; outspend != nil {
		copyValue := *outspend
		return &copyValue, nil
	}
	if e.base != nil {
		return e.base.GetOutspend(outpoint)
	}
	return &rgb11wallet.BitcoinOutspend{}, nil
}

func saveRGB11StatusTransfer(t *testing.T, manager *Manager, transferID, status string,
	inputOutpoints ...string) string {
	t.Helper()
	recipient := []byte("recipient-consignment-" + transferID)
	hash := sha256.Sum256(recipient)
	tx := wire.NewMsgTx(wire.TxVersion)
	var input *wire.OutPoint
	if len(inputOutpoints) > 0 {
		parsed, err := wire.NewOutPointFromString(inputOutpoints[0])
		if err != nil || parsed == nil {
			t.Fatalf("parse test input outpoint: %v", err)
		}
		input = parsed
	} else {
		inputHash := chainhash.DoubleHashH([]byte("input-" + transferID))
		input = wire.NewOutPoint(&inputHash, 0)
	}
	tx.AddTxIn(wire.NewTxIn(input, nil, nil))
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
		ReservationID:        transferID + "-reservation",
	}
	if err := manager.rgbManager.projectionStore.SavePendingTransfer(pending); err != nil {
		t.Fatal(err)
	}
	claimableReasons := []string(nil)
	if len(inputOutpoints) > 0 {
		claimableReasons = append(claimableReasons, rgb11wallet.LockReasonRGB)
	}
	if err := manager.utxoLockerL1.TryReserve([]string{inputOutpoint},
		rgb11wallet.LockReasonPending, pending.ReservationID, claimableReasons...); err != nil {
		t.Fatal(err)
	}
	return witnessTxID
}

type rgb11StaticRejectLists struct {
	list rejectlist.List
	err  error
}

func (s rgb11StaticRejectLists) Fetch(string) (rejectlist.List, error) { return s.list, s.err }

func TestRGB11RejectListUnavailableIsDistinctFromRejection(t *testing.T) {
	provider := rgb11StaticRejectLists{err: errors.New("offline")}
	if _, err := provider.Fetch("https://issuer.example/reject-list"); err == nil {
		t.Fatal("expected unavailable provider error")
	}
	if errors.Is(provider.err, ErrRGB11Rejected) {
		t.Fatal("service failure must not be treated as issuer rejection")
	}
}

func TestRGB11RefreshRollsSettledTransferBackAfterReorg(t *testing.T) {
	manager, _, _, chainEvidence, _ := newRGB11ValidContractSpendFixture(t)
	manager.rgbManager.scopeStates.stopReconciliations()
	proofs, err := manager.rgbManager.projectionStore.ListProofs()
	if err != nil || len(proofs) != 1 {
		t.Fatalf("load real parent proof: count=%d err=%v", len(proofs), err)
	}
	parentOutpoint := proofs[0].OutPoint
	evidence := &rgb11StatusEvidence{
		base:     chainEvidence,
		statuses: map[string]*rgb11wallet.BitcoinTxStatus{},
		outspends: map[string]*rgb11wallet.BitcoinOutspend{
			parentOutpoint: &rgb11wallet.BitcoinOutspend{}, // Explicitly report the parent carrier as unspent.
		},
	}
	manager.rgbManager.evidence = evidence
	witnessTxID := saveRGB11StatusTransfer(t, manager, "reorg", "settled", parentOutpoint)
	pending, err := manager.rgbManager.projectionStore.LoadPendingTransfer("reorg")
	if err != nil {
		t.Fatal(err)
	}
	// Keep the locally held signed bytes, while the status override records that
	// the transaction is no longer visible after the simulated reorg.
	chainEvidence.mu.Lock()
	chainEvidence.rawTx[witnessTxID] = append([]byte(nil), pending.SignedTx...)
	chainEvidence.mu.Unlock()
	evidence.statuses[witnessTxID] = &rgb11wallet.BitcoinTxStatus{TxID: witnessTxID}

	result, err := manager.rgbManager.RefreshRGB11State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Reorged != 1 || result.Conflicted != 0 || result.Unresolved != 0 || len(result.Inconsistent) != 0 {
		t.Fatalf("unexpected reorg result: %+v", result)
	}
	pending, err = manager.rgbManager.projectionStore.LoadPendingTransfer("reorg")
	if err != nil {
		t.Fatal(err)
	}
	if pending.State.Status != "orphaned" {
		t.Fatalf("reorged transfer status=%s", pending.State.Status)
	}
	if len(pending.State.InputOutPoints) != 1 {
		t.Fatalf("reorged transfer input count=%d", len(pending.State.InputOutPoints))
	}
	lock := manager.utxoLockerL1.GetLockedUtxoList()[pending.State.InputOutPoints[0]]
	if lock != nil && (lock.Reason == rgb11wallet.LockReasonPending || lock.ReservationID != "") {
		t.Fatalf("positively reorged spend retained operation reservation: %+v", lock)
	}
}

func TestRGB11RefreshFailsClosedOnConflictingSpend(t *testing.T) {
	f := newRGB11ConflictFixture(t, false)
	f.publish(t, true)
	manager := f.sender
	result, err := manager.RefreshRGB11State(context.Background())
	if !errors.Is(err, ErrRGB11Inconsistent) {
		t.Fatalf("conflicting spend error=%v", err)
	}
	if result == nil || result.Conflicted != 1 || len(result.Inconsistent) != 1 || result.Inconsistent[0] != f.carrier || manager.GetRGB11ConsistencyStatus() != "broken" {
		t.Fatal("confirmed conflict result or exact carrier classification is incorrect")
	}
	pending, err := manager.rgbManager.projectionStore.LoadPendingTransfer(f.pending.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.State.Status != "conflicted" || pending.State.AckStatus != "invalidated" {
		t.Fatal("confirmed conflict journal was not preserved")
	}
	f.assertImmutable(t)
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
	evidence.statuses[state.WitnessTxID] = &rgb11wallet.BitcoinTxStatus{TxID: state.WitnessTxID, InMempool: true}
	result, err = manager.RefreshRGB11State(context.Background())
	if err != nil || result.Reorged != 1 || result.Pending != 1 {
		t.Fatalf("incoming reorg result=%+v err=%v", result, err)
	}
	stored, err = manager.rgbManager.projectionStore.LoadTransferState(state.TransferID)
	if err != nil || stored.Status != "pending" {
		t.Fatalf("incoming reorged state=%+v err=%v", stored, err)
	}
}

func TestRGB11RejectedStaysTerminal(t *testing.T) {
	manager := newRGB11MultiDeviceManager(t, newRGB11StatusPrivateKey(t), 56)
	evidence := &rgb11StatusEvidence{
		statuses:  make(map[string]*rgb11wallet.BitcoinTxStatus),
		outspends: make(map[string]*rgb11wallet.BitcoinOutspend),
	}
	manager.rgbManager.evidence = evidence
	state := &rgb11wallet.TransferState{
		TransferID: "rejected-receive", Direction: "receive", Status: "rejected",
		AckStatus: "rejected", RejectReason: "validation-failed",
		WitnessTxID: "rejected-witness", OutputOutPoints: []string{"rejected-witness:1"},
		MinConfirmations: 1,
	}
	if err := manager.rgbManager.projectionStore.SaveTransferState(state); err != nil {
		t.Fatal(err)
	}
	evidence.statuses[state.WitnessTxID] = &rgb11wallet.BitcoinTxStatus{
		TxID: state.WitnessTxID, Confirmed: true, Confirmations: 2,
	}

	result, err := manager.RefreshRGB11State(context.Background())
	// Rejected candidates publish no result; terminal rejection must remain durable.
	if !errors.Is(err, ErrRGB11Inconsistent) || result != nil {
		t.Fatalf("terminal witness result=%+v err=%v", result, err)
	}
	stored, loadErr := manager.rgbManager.projectionStore.LoadTransferState(state.TransferID)
	if loadErr != nil || stored.Status != "rejected" || stored.AckStatus != "rejected" ||
		stored.RejectReason != "validation-failed" || stored.WitnessTxID != state.WitnessTxID {
		t.Fatalf("rejected receive revived: state=%+v err=%v", stored, loadErr)
	}
	if lock := manager.utxoLockerL1.GetLockedUtxoList()[state.OutputOutPoints[0]]; lock != nil {
		t.Fatalf("rejected receive relocked output: %+v", lock)
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
