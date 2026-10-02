package wallet

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/btcsuite/btcd/wire"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// A contract is transferable metadata even after its genesis seal was spent.
// This reproduces Export on the issuer followed by Import on another account;
// no historical genesis allocation may become spendable in the importing wallet.
func TestRGB11ContractAfterSpend(t *testing.T) {
	sender, viewer, imported, evidence, rpc := newRGB11GenericSendFixture(t)
	sender.rgbManager.scopeStates.stopReconciliations()
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
	t.Run("spent_genesis_metadata", func(t *testing.T) {
		fresh := newRGB11FlowManager(t, viewer.wallet, rpc, evidence, 103)
		fresh.rgbManager.scopeStates.stopReconciliations()
		result, e := fresh.ImportRGB11ContractFile(ctx, raw)
		if e != nil {
			t.Fatalf("valid contract metadata rejected after real prepared input was confirmed spent: %v", e)
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
