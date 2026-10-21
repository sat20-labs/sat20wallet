package wallet

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	coreissuance "github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/schemas"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// A contract is transferable metadata even after its genesis seal was spent.
// This reproduces Export on the issuer followed by Import on another account;
// no historical genesis allocation may become spendable in the importing wallet.
func TestRGB11ContractAfterSpend(t *testing.T) {
	previousChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previousChain })
	sender, viewer, imported, evidence, rpc := newRGB11ValidContractSpendFixture(t)
	sender.rgbManager.scopeStates.stopReconciliations()
	originalViewerAddress := viewer.wallet.GetAddress()
	remote := newRGB11MemoryDKVSHTTP()
	viewer = newRGB11ActivatedFlowAccount(t, "comfort very add tuition senior run eight snap burst appear exile dutch", remote, newRGB11MessageNodeClient(remote), evidence, rpc)
	if viewer.wallet.GetAddress() != originalViewerAddress {
		t.Fatal("activated viewer changed contract import wallet")
	}
	viewer.rgbManager.scopeStates.stopReconciliations()
	ctx := context.Background()
	exported, err := sender.ExportRGB11Contract(imported.ContractID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(exported.ContractConsignmentBase64)
	if err != nil {
		t.Fatal(err)
	}
	container, err := coreconsignment.DecodeFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	bundles, _ := container.Value.Field("bundles")
	if container.Armor.Type != "contract" || len(bundles.Unwrap().Items) != 0 {
		t.Fatal("fixture must export a genesis-only contract")
	}
	t.Run("unspent_control", func(t *testing.T) {
		result, e := viewer.ImportRGB11ContractFile(ctx, raw)
		if e != nil {
			t.Fatal(e)
		}
		if result.Projected != 0 {
			t.Fatal("viewer must not own issuer allocations")
		}
	})
	t.Run("unknown_stays_rejected", func(t *testing.T) {
		evidence.mu.Lock()
		saved := evidence.utxos
		evidence.utxos = make(map[string]*rgb11wallet.BitcoinUTXO)
		evidence.mu.Unlock()
		defer func() { evidence.mu.Lock(); evidence.utxos = saved; evidence.mu.Unlock() }()
		if _, e := viewer.ImportRGB11ContractFile(ctx, raw); !errors.Is(e, coreconsignment.ErrOutpointUnknown) {
			t.Fatalf("unknown genesis must remain rejected, got %v", e)
		}
	})
	invoice, err := viewer.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID,
		AmountRaw: "20000", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := sender.rgbManager.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	txid := tx.TxHash().String()
	evidence.mu.Lock()
	evidence.rawTx[txid] = append([]byte(nil), pending.SignedTx...)
	for _, input := range tx.TxIn {
		op := input.PreviousOutPoint.String()
		evidence.spendingTx[op] = txid
		delete(evidence.utxos, op)
	}
	evidence.mu.Unlock()
	evidence.setStatus(txid, rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 6})
	after, err := sender.ExportRGB11Contract(imported.ContractID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ContractConsignmentBase64 != exported.ContractConsignmentBase64 {
		t.Fatal("canonical contract changed after spending its genesis allocation")
	}
	t.Run("spent_genesis_ticker_from_exact_genesis", func(t *testing.T) {
		receipt, e := rgb11wallet.ValidateWith(ctx, rgb11wallet.NewNativeConsensusValidator(), raw, evidence)
		if e != nil || len(receipt.Allocations) != 0 {
			t.Fatalf("validate spent genesis metadata: allocations=%d err=%v", len(receipt.Allocations), e)
		}
		records, e := rgb11wallet.AddValidatedConsignmentProjectionSnapshot(nil, raw, receipt, nil)
		if e != nil {
			t.Fatalf("retain validated zero-allocation contract: %v", e)
		}
		refs, e := rgb11wallet.TickerRefsFromProjectionSnapshot(records)
		if e != nil || len(refs) != 1 || refs[0].ContractID != imported.ContractID {
			t.Fatalf("derive ticker from exact genesis: refs=%+v err=%v", refs, e)
		}

		corrupt := append([]byte(nil), raw...)
		corrupt[len(corrupt)-1] ^= 0x01
		if _, e := rgb11wallet.AddValidatedConsignmentProjectionSnapshot(nil, corrupt, receipt, nil); e == nil {
			t.Fatal("damaged contract bytes were accepted with the original validation receipt")
		}
		if _, e := rgb11wallet.AddContractObjectSnapshotReference(nil, "not-the-contract-id", raw); e == nil {
			t.Fatal("contract reference accepted a different contract id")
		}
	})
	t.Run("spent_genesis_same_viewer_metadata", func(t *testing.T) {
		result, e := viewer.rgbManager.ImportRGB11ContractFile(ctx, raw)
		if e != nil {
			t.Fatalf("same raw contract could not be reimported after its genesis allocation was spent: %v", e)
		}
		if result.Projected != 0 {
			t.Fatal("spent genesis must not be reprojected on the same receiver")
		}
		state, e := viewer.GetRGB11State()
		if e != nil {
			t.Fatal(e)
		}
		if len(state.TickerInfos) != 1 || len(state.Proofs) != 0 || len(state.Outputs) != 0 {
			t.Fatal("reimport must retain metadata without restoring spent allocations")
		}
	})
	t.Run("spent_genesis_metadata", func(t *testing.T) {
		freshRemote := newRGB11MemoryDKVSHTTP()
		fresh := newRGB11ActivatedFlowAccount(t, "comfort very add tuition senior run eight snap burst appear exile dutch", freshRemote, newRGB11MessageNodeClient(freshRemote), evidence, rpc)
		if fresh.wallet.GetAddress() != viewer.wallet.GetAddress() {
			t.Fatal("fresh import fixture changed viewer wallet")
		}
		fresh.rgbManager.scopeStates.stopReconciliations()
		result, e := fresh.ImportRGB11ContractFile(ctx, raw)
		if e != nil {
			_, coreErr := rgb11wallet.ValidateWith(ctx, rgb11wallet.NewNativeConsensusValidator(), raw, evidence)
			t.Fatalf("valid contract metadata rejected after real prepared input was confirmed spent: import=%v core_validate=%v",
				e, coreErr)
		}
		if result.Projected != 0 {
			t.Fatal("spent genesis must never be projected")
		}
		state, e := fresh.GetRGB11State()
		if e != nil {
			t.Fatal(e)
		}
		if len(state.TickerInfos) != 1 || len(state.Proofs) != 0 || len(state.Outputs) != 0 {
			t.Fatal("import must register metadata without restoring spent allocations")
		}
	})
}

func newRGB11ValidContractSpendFixture(t *testing.T) (*Manager, *Manager, *RGB11ImportResult,
	*rgb11AddressEvidence, *rgb11FlowIndexer) {
	t.Helper()
	ctx := context.Background()
	senderWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	viewerWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
	)
	senderScript, err := AddrToPkScript(senderWallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	sourceTx := wire.NewMsgTx(wire.TxVersion)
	sourceTx.AddTxIn(&wire.TxIn{
		PreviousOutPoint: wire.OutPoint{Index: wire.MaxPrevOutIndex},
		SignatureScript:  []byte{0x01, 0x01},
	})
	sourceTx.AddTxOut(wire.NewTxOut(100_000, senderScript))
	sourceTx.AddTxOut(wire.NewTxOut(100_000, senderScript))
	var rawTx bytes.Buffer
	if err := sourceTx.Serialize(&rawTx); err != nil {
		t.Fatal(err)
	}
	txid := sourceTx.TxHash().String()
	carrier := txid + ":0"
	plain := txid + ":1"
	outputs := make(map[string]*TxOutput, 2)
	plainInfo := make([]*indexerwire.TxOutputInfo, 0, 2)
	for _, item := range []struct {
		outpoint string
		value    int64
	}{{carrier, 100_000}, {plain, 100_000}} {
		output := indexer.NewTxOutput(item.value)
		output.OutPointStr = item.outpoint
		output.OutValue.PkScript = append([]byte(nil), senderScript...)
		outputs[item.outpoint] = output
		plainInfo = append(plainInfo, &indexerwire.TxOutputInfo{
			OutPoint: item.outpoint, Value: item.value, PkScript: append([]byte(nil), senderScript...),
		})
	}
	rpc := &rgb11FlowIndexer{outputs: outputs, plain: plainInfo}
	baseEvidence := &rgb11FlowEvidence{
		utxos: map[string]*rgb11wallet.BitcoinUTXO{
			carrier: {OutPoint: carrier, Value: 100_000, PkScript: append([]byte(nil), senderScript...), Confirmations: 6},
			plain:   {OutPoint: plain, Value: 100_000, PkScript: append([]byte(nil), senderScript...), Confirmations: 6},
		},
		rawTx: map[string][]byte{txid: append([]byte(nil), rawTx.Bytes()...)},
		txStatuses: map[string]*rgb11wallet.BitcoinTxStatus{
			txid: {TxID: txid, Confirmed: true, Confirmations: 6},
		},
		spendingTx: make(map[string]string),
	}
	evidence := &rgb11AddressEvidence{rgb11FlowEvidence: baseEvidence, statuses: make(map[string]*rgb11wallet.BitcoinTxStatus)}
	sender := newRGB11FlowManager(t, senderWallet, rpc, evidence, 911)
	viewer := newRGB11FlowManager(t, viewerWallet, rpc, evidence, 912)
	allocations, err := rgb11IssueAllocations([]string{carrier}, []uint64{50_000})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := coreissuance.Issue(coreissuance.Spec{
		Kind: schemas.NIA, Network: rgb11IssuanceNetwork(&chaincfg.TestNet4Params),
		Ticker: "SPENT", Name: "Spent contract metadata", Allocations: allocations,
	})
	if err != nil {
		t.Fatal(err)
	}
	imported, err := sender.rgbManager.ImportRGB11Contract(ctx, []byte(issued.Armor))
	if err != nil || imported == nil || imported.Projected != 1 {
		t.Fatalf("import valid synthetic issued contract: result=%+v err=%v", imported, err)
	}
	if _, err := viewer.rgbManager.ImportRGB11Contract(ctx, []byte(issued.Armor)); err != nil {
		t.Fatalf("import issuer metadata into viewer: %v", err)
	}
	return sender, viewer, imported, evidence, rpc
}
