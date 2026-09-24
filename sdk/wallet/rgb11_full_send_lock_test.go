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

func TestRGB11FullSendRefresh(t *testing.T) {
	sender, recipient, imported, evidence, rpc := newRGB11GenericSendFixture(t)
	ctx := context.Background()
	balance, err := sender.GetRGB11AssetBalance(&imported.AssetName)
	if err != nil || balance.Value.Sign() <= 0 {
		t.Fatalf("balance=%v err=%v", balance, err)
	}
	request, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID,
		AmountRaw: balance.Value.String(), WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := sender.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice: request.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending.ChangeSeals) != 0 {
		t.Fatalf("full send has change seals: %d", len(pending.ChangeSeals))
	}
	pending.State.Status = "broadcast"
	pending.State.AckStatus = "accepted"
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
		evidence.spendingTx[outpoint] = txid
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
			OutPoint: outpoint, Value: output.Value,
			PkScript: append([]byte(nil), output.PkScript...),
		})
	}
	evidence.mu.Unlock()
	evidence.setStatus(txid, rgb11wallet.BitcoinTxStatus{
		TxID: txid, Confirmed: true, Confirmations: 6,
	})

	result, err := sender.RefreshRGB11State(ctx)
	if err != nil {
		t.Fatalf("refresh full send: result=%+v err=%v", result, err)
	}
	stored, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil || stored.State.Status != "settled" {
		t.Fatalf("state=%+v err=%v", stored, err)
	}
	locks := sender.utxoLockerL1.GetLockedUtxoList()
	for _, outpoint := range pending.State.InputOutPoints {
		if lock := locks[outpoint]; lock != nil {
			t.Fatalf("settled input remains locked: %s %+v", outpoint, lock)
		}
	}
	if sender.GetRGB11ConsistencyStatus() != "ok" {
		t.Fatalf("consistency=%s", sender.GetRGB11ConsistencyStatus())
	}
}
