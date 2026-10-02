package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/operations"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Regression: a genuine partial send and return have an unrelated
// change branch. Only Bitcoin evidence for that branch changes between cases.
func TestRGB11SpentSibling(t *testing.T) {
	sender, receiver, imported, evidence, rpc := newRGB11GenericSendFixture(t)
	sender.rgbManager.scopeStates.stopReconciliations()
	receiver.rgbManager.scopeStates.stopReconciliations()
	ctx := context.Background()
	invoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID, AmountRaw: "20000", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	sent, err := sender.rgbManager.PrepareRGB11Transfer(ctx, RGB11SendRequest{Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(sent.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	siblings := rgb11PendingChangeOutpoints(pending)
	if len(siblings) != 1 {
		t.Fatalf("need one unrelated change branch, got %d", len(siblings))
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err = tx.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	txid := tx.TxHash().String()
	evidence.mu.Lock()
	evidence.rawTx[txid] = append([]byte(nil), pending.SignedTx...)
	for _, in := range tx.TxIn {
		op := in.PreviousOutPoint.String()
		evidence.spendingTx[op] = txid
		delete(evidence.utxos, op)
		delete(rpc.outputs, op)
	}
	for i, out := range tx.TxOut {
		op := fmt.Sprintf("%s:%d", txid, i)
		evidence.utxos[op] = &rgb11wallet.BitcoinUTXO{OutPoint: op, Value: out.Value, PkScript: out.PkScript, Confirmations: 6}
		view := indexer.NewTxOutput(out.Value)
		view.OutPointStr = op
		view.OutValue.PkScript = out.PkScript
		rpc.outputs[op] = view
	}
	evidence.mu.Unlock()
	evidence.setStatus(txid, rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 6})
	if _, err = receiver.AcceptRGB11Consignment(ctx, invoice.RequestID, []byte(sent.RecipientConsignment)); err != nil {
		t.Fatal(err)
	}
	if _, err = receiver.rgbManager.RefreshRGB11State(ctx); err != nil {
		t.Fatal(err)
	}
	script, err := AddrToPkScript(receiver.wallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	feeOP := fmt.Sprintf("%064x:0", 9191)
	fee := indexer.NewTxOutput(100000)
	fee.OutPointStr = feeOP
	fee.OutValue.PkScript = script
	rpc.outputs[feeOP] = fee
	rpc.plain = []*indexerwire.TxOutputInfo{{OutPoint: feeOP, Value: 100000, PkScript: script}}
	evidence.mu.Lock()
	evidence.utxos[feeOP] = &rgb11wallet.BitcoinUTXO{OutPoint: feeOP, Value: 100000, PkScript: script, Confirmations: 6}
	evidence.mu.Unlock()
	back, err := sender.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID, AmountRaw: "20000", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	returned, err := receiver.rgbManager.PrepareRGB11Transfer(ctx, RGB11SendRequest{Invoice: back.Invoice, FeeRate: 2, MinConfirmations: 1})
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(returned.RecipientConsignment)
	rgb11AssertGraphTips(t, raw, 1)
	// Prior sender owns this change seal; revealing it does not alter any RGB
	// operation, witness, signature or current return input.
	validate := func() (*rgb11wallet.PreparedValidation, error) {
		return rgb11wallet.ValidatePreparedWith(ctx, rgb11wallet.NewNativeConsensusValidatorWithReveals(pending.ChangeSeals...), raw, evidence)
	}
	t.Run("clean_roundtrip", func(t *testing.T) {
		receipt, e := validate()
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("valid return allocations=%d", len(receipt.Receipt.Allocations))
	})
	t.Run("unrelated_spent_sibling", func(t *testing.T) {
		op := siblings[0]
		for _, input := range returned.State.InputOutPoints {
			if input == op {
				t.Fatal("sibling is a return input")
			}
		}
		evidence.mu.Lock()
		evidence.spendingTx[op] = fmt.Sprintf("%064x", 8181)
		evidence.mu.Unlock()
		defer func() { evidence.mu.Lock(); delete(evidence.spendingTx, op); evidence.mu.Unlock() }()
		result, e := validate()
		if errors.Is(e, coreconsignment.ErrOutpointSpend) {
			t.Fatalf("unrelated historical sibling rejects the valid return: %v", e)
		}
		if e != nil {
			t.Fatal(e)
		}
		if len(result.Receipt.Allocations) != 1 || result.Receipt.Allocations[0].OutPoint == op {
			t.Fatal("spent historical sibling must not enter the valid return projection")
		}
	})
	t.Run("hidden_spent_sibling", func(t *testing.T) {
		op := siblings[0]
		evidence.mu.Lock()
		evidence.spendingTx[op] = fmt.Sprintf("%064x", 8181)
		evidence.mu.Unlock()
		defer func() { evidence.mu.Lock(); delete(evidence.spendingTx, op); evidence.mu.Unlock() }()
		result, e := rgb11wallet.ValidatePreparedWith(ctx, rgb11wallet.NewNativeConsensusValidator(), raw, evidence)
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("unrevealed sibling does not enter current state: allocations=%d", len(result.Receipt.Allocations))
	})
	t.Run("unknown_sibling", func(t *testing.T) {
		op := siblings[0]
		evidence.mu.Lock()
		utxo := evidence.utxos[op]
		delete(evidence.utxos, op)
		evidence.mu.Unlock()
		defer func() { evidence.mu.Lock(); evidence.utxos[op] = utxo; evidence.mu.Unlock() }()
		if _, e := validate(); !errors.Is(e, coreconsignment.ErrOutpointUnknown) {
			t.Fatalf("unknown historical sibling must fail closed, got %v", e)
		}
	})
	t.Run("return_input_spent", func(t *testing.T) {
		op := returned.State.InputOutPoints[0]
		evidence.mu.Lock()
		evidence.spendingTx[op] = fmt.Sprintf("%064x", 8282)
		evidence.mu.Unlock()
		defer func() { evidence.mu.Lock(); delete(evidence.spendingTx, op); evidence.mu.Unlock() }()
		if _, e := validate(); !errors.Is(e, coreconsignment.ErrOutpointSpend) {
			t.Fatalf("current input double spend must fail, got %v", e)
		}
	})
	t.Run("confirmed_terminal_spent", func(t *testing.T) {
		backPending, e := receiver.rgbManager.projectionStore.LoadPendingTransfer(returned.State.TransferID)
		if e != nil {
			t.Fatal(e)
		}
		backTx := wire.NewMsgTx(wire.TxVersion)
		if e = backTx.Deserialize(bytes.NewReader(backPending.SignedTx)); e != nil {
			t.Fatal(e)
		}
		backID := backTx.TxHash().String()
		evidence.mu.Lock()
		evidence.rawTx[backID] = append([]byte(nil), backPending.SignedTx...)
		for _, input := range backTx.TxIn {
			evidence.spendingTx[input.PreviousOutPoint.String()] = backID
		}
		for i, output := range backTx.TxOut {
			op := fmt.Sprintf("%s:%d", backID, i)
			evidence.utxos[op] = &rgb11wallet.BitcoinUTXO{OutPoint: op, Value: output.Value, PkScript: output.PkScript, Confirmations: 6}
		}
		evidence.mu.Unlock()
		evidence.setStatus(backID, rgb11wallet.BitcoinTxStatus{TxID: backID, Confirmed: true, Confirmations: 6})
		validator := rgb11wallet.NewNativeConsensusValidatorWithReveals(pending.ChangeSeals...)
		receipt, e := rgb11wallet.ValidateWith(ctx, validator, raw, evidence)
		if e != nil {
			t.Fatalf("confirmed terminal control: %v", e)
		}
		terminal := fmt.Sprintf("%s:%d", backID, returned.State.RecipientVout)
		found := 0
		for _, allocation := range receipt.Allocations {
			if allocation.OutPoint == terminal {
				found++
			}
		}
		if found != 1 {
			t.Fatalf("need exactly one return terminal allocation, got %d", found)
		}
		evidence.mu.Lock()
		evidence.spendingTx[terminal] = fmt.Sprintf("%064x", 8383)
		evidence.mu.Unlock()
		if _, e = rgb11wallet.ValidateWith(ctx, validator, raw, evidence); !errors.Is(e, coreconsignment.ErrOutpointSpend) {
			t.Fatalf("externally spent terminal must fail, got %v", e)
		}
	})

}

// Assert fixture topology only; production validation below must independently
// verify all anchors, operations and inputs before any tip can be trusted.
func rgb11AssertGraphTips(t *testing.T, raw []byte, want int) {
	t.Helper()
	c, err := coreconsignment.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	terminals, ok := c.Value.Field("terminals")
	if !ok || len(terminals.Unwrap().Entries) != 0 {
		t.Fatal("witness fixture must have empty terminals")
	}
	tips := make(map[[32]byte]bool)
	parents := make(map[[32]byte]bool)
	bundles, _ := c.Value.Field("bundles")
	for _, wb := range bundles.Unwrap().Items {
		value, _ := wb.Unwrap().Field("bundle")
		bundle, e := operations.CommitBundle(value)
		if e != nil {
			t.Fatal(e)
		}
		for _, transition := range bundle.Transitions {
			committed, e := operations.CommitTransition(transition)
			if e != nil {
				t.Fatal(e)
			}
			tips[committed.OperationID] = true
			inputs, _ := transition.Field("inputs")
			for _, input := range inputs.Unwrap().Items {
				op, _ := input.Unwrap().Field("op")
				id, ok := op.Bytes()
				if !ok || len(id) != 32 {
					t.Fatal("invalid fixture input")
				}
				var parent [32]byte
				copy(parent[:], id)
				parents[parent] = true
			}
		}
	}
	for parent := range parents {
		delete(tips, parent)
	}
	if len(tips) != want {
		t.Fatalf("fixture tips=%d, want %d", len(tips), want)
	}
}

// Two real independent successor branches leave a disclosed allocation on an
// earlier operation. Empty terminals cannot identify a unique receiving tip.
func TestRGB11SiblingMultiTip(t *testing.T) {
	a, b, imported, evidence, rpc := newRGB11GenericSendFixture(t)
	a.rgbManager.scopeStates.stopReconciliations()
	b.rgbManager.scopeStates.stopReconciliations()
	ctx := context.Background()
	feeSeq := 9290
	prepare := func(from, to *Manager, amount string) (*rgb11wallet.RGB11PreparedTransfer, string) {
		invoice, err := to.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID, AmountRaw: amount, WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix()})
		if err != nil {
			t.Fatal(err)
		}
		script, err := AddrToPkScript(from.wallet.GetAddress(), &chaincfg.TestNet4Params)
		if err != nil {
			t.Fatal(err)
		}
		feeSeq++
		op := fmt.Sprintf("%064x:0", feeSeq)
		output := indexer.NewTxOutput(100000)
		output.OutPointStr, output.OutValue.PkScript = op, script
		rpc.outputs[op] = output
		rpc.plain = []*indexerwire.TxOutputInfo{{OutPoint: op, Value: 100000, PkScript: script}}
		evidence.mu.Lock()
		evidence.utxos[op] = &rgb11wallet.BitcoinUTXO{OutPoint: op, Value: 100000, PkScript: script, Confirmations: 6}
		evidence.mu.Unlock()
		prepared, err := from.rgbManager.PrepareRGB11Transfer(ctx, RGB11SendRequest{Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1})
		if err != nil {
			t.Fatal(err)
		}
		return prepared, invoice.RequestID
	}
	confirm := func(from *Manager, prepared *rgb11wallet.RGB11PreparedTransfer) {
		if _, err := from.BroadcastRGB11OutOfBand([]string{prepared.State.TransferID}); err != nil {
			t.Fatal(err)
		}
		pending, err := from.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
		if err != nil {
			t.Fatal(err)
		}
		tx := wire.NewMsgTx(wire.TxVersion)
		if err = tx.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
			t.Fatal(err)
		}
		txid := tx.TxHash().String()
		evidence.mu.Lock()
		evidence.rawTx[txid] = append([]byte(nil), pending.SignedTx...)
		for _, input := range tx.TxIn {
			op := input.PreviousOutPoint.String()
			evidence.spendingTx[op] = txid
			delete(evidence.utxos, op)
			delete(rpc.outputs, op)
		}
		for vout, output := range tx.TxOut {
			op := fmt.Sprintf("%s:%d", txid, vout)
			evidence.utxos[op] = &rgb11wallet.BitcoinUTXO{OutPoint: op, Value: output.Value, PkScript: output.PkScript, Confirmations: 6}
			view := indexer.NewTxOutput(output.Value)
			view.OutPointStr, view.OutValue.PkScript = op, output.PkScript
			rpc.outputs[op] = view
		}
		evidence.mu.Unlock()
		evidence.setStatus(txid, rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 6})
		if _, err = from.rgbManager.RefreshRGB11State(ctx); err != nil {
			t.Fatal(err)
		}
	}
	first, request := prepare(a, b, "20000")
	confirm(a, first)
	if _, err := b.AcceptRGB11Consignment(ctx, request, []byte(first.RecipientConsignment)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.rgbManager.RefreshRGB11State(ctx); err != nil {
		t.Fatal(err)
	}
	second, _ := prepare(b, a, "10000")
	confirm(b, second)
	// A has not accepted second, so A's next send selects only its original
	// 80000 change. B separately spends the second operation's 10000 change.
	left, _ := prepare(b, a, "10000")
	right, _ := prepare(a, b, "80000")
	lc, err := coreconsignment.Decode([]byte(left.RecipientConsignment))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := coreconsignment.Decode([]byte(right.RecipientConsignment))
	if err != nil {
		t.Fatal(err)
	}
	merged, err := coreconsignment.MergeHistories(lc, rc)
	if err != nil {
		t.Fatal(err)
	}
	armor, err := coreconsignment.EncodeArmor(merged.Value)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(armor)
	rgb11AssertGraphTips(t, raw, 2)
	validate := func() (*rgb11wallet.PreparedValidation, error) {
		// Exercise the core validator directly: the SDK receive wrapper also
		// requires exactly one prepared witness and is not this boundary.
		return rgb11wallet.NewNativeConsensusValidator().ValidatePreparedConsignment(ctx, raw, evidence)
	}
	if result, err := validate(); err != nil || len(result.Receipt.Allocations) != 3 || len(result.WitnessTxIDs) != 2 {
		t.Fatalf("valid independent-branch control failed: result=%v error=%v", result != nil, err)
	}
	op := fmt.Sprintf("%s:%d", second.State.WitnessTxID, second.State.RecipientVout)
	for _, prepared := range []*rgb11wallet.RGB11PreparedTransfer{left, right} {
		for _, input := range prepared.State.InputOutPoints {
			if input == op {
				t.Fatal("historical sibling is a current branch input")
			}
		}
	}
	evidence.mu.Lock()
	evidence.spendingTx[op] = fmt.Sprintf("%064x", 9494)
	evidence.mu.Unlock()
	if _, err = validate(); !errors.Is(err, coreconsignment.ErrOutpointSpend) {
		t.Fatalf("ambiguous empty-terminal history must not skip a spent sibling: %v", err)
	}
}
