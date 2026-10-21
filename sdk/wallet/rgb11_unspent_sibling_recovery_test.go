package wallet

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Two real self-send outputs remain independently owned. Spending either one
// must not invalidate the other output's original immutable consignment.
func TestRGB11RefreshUnspentSelfSendSibling(t *testing.T) {
	self, other, imported, evidence, rpc := newRGB11GenericSendFixture(t)
	ctx := context.Background()
	confirm := func(pending *rgb11wallet.PendingTransfer) {
		t.Helper()
		var tx wire.MsgTx
		reader := bytes.NewReader(pending.SignedTx)
		if err := tx.Deserialize(reader); err != nil || reader.Len() != 0 || tx.TxHash().String() != pending.State.WitnessTxID {
			t.Fatalf("invalid actual witness: %v", err)
		}
		txid := tx.TxHash().String()
		evidence.mu.Lock()
		evidence.rawTx[txid] = append([]byte(nil), pending.SignedTx...)
		for _, input := range tx.TxIn {
			point := input.PreviousOutPoint.String()
			evidence.spendingTx[point] = txid
			delete(evidence.utxos, point)
			delete(rpc.outputs, point)
		}
		for vout, output := range tx.TxOut {
			point := fmt.Sprintf("%s:%d", txid, vout)
			evidence.utxos[point] = &rgb11wallet.BitcoinUTXO{
				OutPoint: point, Value: output.Value, PkScript: append([]byte(nil), output.PkScript...), Confirmations: 6,
			}
			view := indexer.NewTxOutput(output.Value)
			view.OutPointStr, view.OutValue.PkScript = point, append([]byte(nil), output.PkScript...)
			rpc.outputs[point] = view
		}
		rpc.plain = nil
		for point, output := range rpc.outputs {
			rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{
				OutPoint: point, Value: output.OutValue.Value, PkScript: append([]byte(nil), output.OutValue.PkScript...),
			})
		}
		evidence.mu.Unlock()
		evidence.setStatus(txid, rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 6})
	}
	prepare := func(to *Manager, amount string) *rgb11wallet.RGB11PreparedTransfer {
		t.Helper()
		invoice, err := to.CreateRGB11Invoice(RGB11InvoiceRequest{
			Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID,
			AmountRaw: amount, WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
		})
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := self.PrepareRGB11Transfer(ctx, RGB11SendRequest{
			Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := to.PrepareRGB11Consignment(ctx, invoice.RequestID, []byte(prepared.RecipientConsignment)); err != nil {
			t.Fatal(err)
		}
		return prepared
	}
	first := prepare(self, "20000")
	if _, err := self.BroadcastRGB11OutOfBand([]string{first.State.TransferID}); err != nil {
		t.Fatal(err)
	}
	pending, err := self.rgbManager.projectionStore.LoadPendingTransfer(first.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	changes := rgb11PendingChangeOutpoints(pending)
	if len(changes) != 1 {
		t.Fatalf("self-send change count=%d", len(changes))
	}
	originalSiblings := []string{fmt.Sprintf("%s:%d", first.TxID, first.State.RecipientVout), changes[0]}
	confirm(pending)
	if _, err := self.RefreshRGB11State(ctx); err != nil {
		t.Fatalf("settle original self-send: %v", err)
	}
	if balance, err := self.GetRGB11AssetBalance(&imported.AssetName); err != nil || balance == nil || balance.Value.Uint64() != 50000 {
		t.Fatalf("self-send balance=%v err=%v", balance, err)
	}

	// A real additional fee carrier avoids changing either RGB carrier's value.
	script, err := AddrToPkScript(self.wallet.GetAddress(), GetChainParam())
	if err != nil {
		t.Fatal(err)
	}
	feePoint := seedRGB11FlowEvidenceUTXO(t, evidence.rgb11FlowEvidence, script, 100000, 93)
	fee := indexer.NewTxOutput(100000)
	fee.OutPointStr, fee.OutValue.PkScript = feePoint, script
	rpc.outputs[feePoint] = fee
	rpc.plain = []*indexerwire.TxOutputInfo{{OutPoint: feePoint, Value: 100000, PkScript: script}}
	second := prepare(other, "1")
	if _, err := self.BroadcastRGB11OutOfBand([]string{second.State.TransferID}); err != nil {
		t.Fatal(err)
	}
	pending, err = self.rgbManager.projectionStore.LoadPendingTransfer(second.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	confirm(pending)
	spent, unspent := "", ""
	for _, point := range originalSiblings {
		outspend, err := evidence.GetOutspend(point)
		if err != nil {
			t.Fatal(err)
		}
		if outspend.Spent {
			if spent != "" || outspend.SpendingTx != second.TxID {
				t.Fatal("respend did not consume exactly one self-send sibling")
			}
			spent = point
		} else {
			unspent = point
		}
	}
	if spent == "" || unspent == "" {
		t.Fatal("fixture has no independently spent and unspent siblings")
	}
	if _, err := self.RefreshRGB11State(ctx); err != nil {
		t.Fatalf("refresh independently unspent sibling %s: %v", unspent, err)
	}
	if !rgb11ProjectionTestHasSettledProof(t, self, unspent) {
		t.Fatal("unspent sibling lost settled ownership")
	}
	if rgb11ProjectionTestHasSettledProof(t, self, spent) {
		t.Fatal("spent sibling regained settled ownership")
	}
	if balance, err := self.GetRGB11AssetBalance(&imported.AssetName); err != nil || balance == nil || balance.Value.Uint64() != 49999 {
		t.Fatalf("respend balance=%v err=%v", balance, err)
	}
	if _, err := other.RefreshRGB11State(ctx); err != nil {
		t.Fatalf("settle actual receiver: %v", err)
	}
	if balance, err := other.GetRGB11AssetBalance(&imported.AssetName); err != nil || balance == nil || balance.Value.Uint64() != 1 {
		t.Fatalf("receiver balance=%v err=%v", balance, err)
	}
}
