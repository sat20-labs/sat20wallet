package wallet

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	coreissuance "github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/schemas"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func TestRGB11HiddenFeeHistory(t *testing.T) {
	sender, recipient, imported, evidence, rpc := newRGB11GenericSendFixture(t)
	const source = "14295d5bb1a191cdb6286dc0944df938421e3dfcbf0811353ccac4100c2068c5:1"
	const protected = "c57fb2b8b73f2ee5febb0a593eb681c90eb813c6f7d3500f04d46b711a28589d:1"
	const hidden = "0bbc6051be126edb43d04bd390967c19a9f95b4aa8fb67edf12bca1f64d3dfe6:1"
	script := append([]byte(nil), rpc.outputs[source].OutValue.PkScript...)
	for _, outpoint := range []string{protected, hidden} {
		evidence.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: 100_000, PkScript: script, Confirmations: 6,
		}
		output := indexer.NewTxOutput(100_000)
		output.OutPointStr, output.OutValue.PkScript = outpoint, script
		rpc.outputs[outpoint] = output
	}
	allocations, err := rgb11IssueAllocations([]string{protected, hidden}, []uint64{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := coreissuance.Issue(coreissuance.Spec{
		Kind: schemas.NIA, Network: rgb11IssuanceNetwork(&chaincfg.TestNet4Params),
		Ticker: "R2PAID", Name: "Hidden fee history", Allocations: allocations,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := sender.ImportRGB11Contract(context.Background(), []byte(issued.Armor))
	if err != nil || other.Projected != 2 {
		t.Fatalf("import second contract: %+v err=%v", other, err)
	}
	if err := sender.rgbManager.projectionStore.DeleteProjections([]string{hidden}); err != nil {
		t.Fatal(err)
	}
	if err := sender.utxoLockerL1.UnlockUtxo(hidden); err != nil {
		t.Fatal(err)
	}
	before, err := sender.GetRGB11AssetBalance(&other.AssetName)
	if err != nil || before == nil || before.Value.Uint64() != 1 {
		t.Fatalf("remaining second asset=%v err=%v", before, err)
	}

	evidence.utxos[source].Value = 330
	rpc.outputs[source].OutValue.Value = 330
	rpc.plain = []*indexerwire.TxOutputInfo{
		{OutPoint: hidden, Value: 100_000, PkScript: script},
		{OutPoint: protected, Value: 100_000, PkScript: script},
	}
	amount, err := sender.GetRGB11AssetBalance(&imported.AssetName)
	if err != nil || amount == nil || amount.Value.Sign() <= 0 {
		t.Fatalf("send balance=%v err=%v", amount, err)
	}
	invoice, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID,
		AmountRaw: amount.Value.String(), WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	usedHidden, usedProtected := false, false
	for _, input := range pending.State.InputOutPoints {
		usedHidden = usedHidden || input == hidden
		usedProtected = usedProtected || input == protected
	}
	if !usedHidden || usedProtected {
		t.Fatalf("inputs=%v hidden=%v protected=%v", pending.State.InputOutPoints, usedHidden, usedProtected)
	}
	pending.State.Status, pending.State.AckStatus = "broadcast", "accepted"
	if err := sender.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	txid := tx.TxHash().String()
	evidence.rawTx[txid] = append([]byte(nil), pending.SignedTx...)
	for _, input := range tx.TxIn {
		outpoint := input.PreviousOutPoint.String()
		evidence.spendingTx[outpoint] = txid
		delete(evidence.utxos, outpoint)
		delete(rpc.outputs, outpoint)
	}
	for vout, output := range tx.TxOut {
		outpoint := fmt.Sprintf("%s:%d", txid, vout)
		evidence.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: output.Value,
			PkScript: append([]byte(nil), output.PkScript...), Confirmations: 6,
		}
	}
	evidence.setStatus(txid, rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 6})

	result, refreshErr := sender.RefreshRGB11State(context.Background())
	if refreshErr != nil || sender.GetRGB11ConsistencyStatus() != "ok" {
		t.Fatalf("hidden fee history corrupted remaining state: result=%+v err=%v consistency=%s",
			result, refreshErr, sender.GetRGB11ConsistencyStatus())
	}
	after, err := sender.GetRGB11AssetBalance(&other.AssetName)
	if err != nil || after == nil || after.Value.Uint64() != 1 {
		t.Fatalf("protected allocation changed: before=%v after=%v err=%v", before, after, err)
	}
	proof, err := sender.rgbManager.projectionStore.LoadProof(protected, other.AssetName)
	if err != nil || proof.Status != "settled" {
		t.Fatalf("protected proof=%+v err=%v", proof, err)
	}
}
