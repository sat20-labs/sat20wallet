package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type rgb11StaleOwnerFixture struct {
	manager *Manager
	pending *rgb11wallet.PendingTransfer
	input   string
}

func seedRGB11StaleOwner(t *testing.T) *rgb11StaleOwnerFixture {
	t.Helper()
	sender, recipient, imported, evidence, rpc := newRGB11GenericSendFixture(t)
	ctx := context.Background()
	balance, err := sender.GetRGB11AssetBalance(&imported.AssetName)
	if err != nil || balance == nil || balance.Value.Sign() <= 0 {
		t.Fatalf("balance=%v err=%v", balance, err)
	}
	invoice, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID,
		AmountRaw: balance.Value.String(), WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := sender.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	pending.State.Status, pending.State.AckStatus = "settled", "accepted"
	if err := sender.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
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
		outpoint := input.PreviousOutPoint.String()
		evidence.spendingTx[outpoint] = "unknown"
		delete(evidence.utxos, outpoint)
		delete(rpc.outputs, outpoint)
	}
	rpc.plain = nil
	for vout, output := range tx.TxOut {
		outpoint := fmt.Sprintf("%s:%d", txid, vout)
		evidence.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: output.Value,
			PkScript: append([]byte(nil), output.PkScript...), Confirmations: 6,
		}
		view := indexer.NewTxOutput(output.Value)
		view.OutPointStr = outpoint
		view.OutValue.PkScript = append([]byte(nil), output.PkScript...)
		rpc.outputs[outpoint] = view
		rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{
			OutPoint: outpoint, Value: output.Value, PkScript: output.PkScript,
		})
	}
	evidence.mu.Unlock()
	evidence.setStatus(txid, rgb11wallet.BitcoinTxStatus{
		TxID: txid, Confirmed: true, Confirmations: 6,
	})
	input := pending.State.InputOutPoints[0]
	if err := sender.utxoLockerL1.UnlockUtxo(input); err != nil {
		t.Fatal(err)
	}
	if lock := sender.utxoLockerL1.GetLockedUtxoList()[input]; lock != nil {
		t.Fatalf("input lock still present: %+v", lock)
	}
	return &rgb11StaleOwnerFixture{manager: sender, pending: pending, input: input}
}

func TestRGB11StaleOwnerMismatch(t *testing.T) {
	fixture := seedRGB11StaleOwner(t)
	result, err := fixture.manager.RefreshRGB11State(context.Background())
	if !errors.Is(err, ErrUtxoReservationOwner) {
		t.Fatalf("stale owner was not reproduced: result=%+v err=%v", result, err)
	}
	if result == nil || result.Unresolved != 0 || result.Settled != 0 ||
		result.Pending != 0 || result.Reorged != 0 || result.Conflicted != 0 {
		t.Fatalf("unexpected refresh result: %+v", result)
	}
	stored, loadErr := fixture.manager.rgbManager.projectionStore.LoadPendingTransfer(
		fixture.pending.State.TransferID,
	)
	if loadErr != nil || stored.State.Status != "settled" || stored.ReservationID == "" {
		t.Fatalf("stale journal=%+v err=%v", stored, loadErr)
	}
	lock := fixture.manager.utxoLockerL1.GetLockedUtxoList()[fixture.input]
	if lock == nil || lock.ReservationID != "" || lock.Reason != rgb11wallet.LockReasonPending {
		t.Fatalf("ownerless input lock was not recreated: %+v", lock)
	}
}
