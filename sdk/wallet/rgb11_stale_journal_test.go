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
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type rgb11StaleOwnerFixture struct {
	manager *Manager
	pending *rgb11wallet.PendingTransfer
	input   string
}

func seedRGB11StaleOwner(t *testing.T) *rgb11StaleOwnerFixture {
	t.Helper()
	senderWallet, _, err := NewInteralWallet(&chaincfg.TestNet4Params)
	if err != nil {
		t.Fatalf("create stale-owner sender wallet: %v", err)
	}
	recipientWallet, _, err := NewInteralWallet(&chaincfg.TestNet4Params)
	if err != nil {
		t.Fatalf("create stale-owner receiver wallet: %v", err)
	}
	senderScript, err := AddrToPkScript(senderWallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	evidenceBase := &rgb11FlowEvidence{
		utxos: make(map[string]*rgb11wallet.BitcoinUTXO),
		rawTx: make(map[string][]byte), txStatuses: make(map[string]*rgb11wallet.BitcoinTxStatus),
		spendingTx: make(map[string]string),
	}
	evidence := &rgb11AddressEvidence{
		rgb11FlowEvidence: evidenceBase, statuses: make(map[string]*rgb11wallet.BitcoinTxStatus),
	}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	for i := 0; i < 4; i++ {
		seedTx := wire.NewMsgTx(wire.TxVersion)
		seedTx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: wire.MaxPrevOutIndex}, []byte{1, byte(i + 1)}, nil))
		seedTx.AddTxOut(wire.NewTxOut(100_000, senderScript))
		var rawSeed bytes.Buffer
		if err := seedTx.Serialize(&rawSeed); err != nil {
			t.Fatal(err)
		}
		producerTxID := seedTx.TxHash().String()
		outpoint := fmt.Sprintf("%s:0", producerTxID)
		evidenceBase.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: 100_000,
			PkScript: append([]byte(nil), senderScript...), Confirmations: 6,
		}
		evidenceBase.rawTx[producerTxID] = append([]byte(nil), rawSeed.Bytes()...)
		evidence.setStatus(producerTxID, rgb11wallet.BitcoinTxStatus{
			TxID: producerTxID, Confirmed: true, Confirmations: 6, BlockHeight: 123,
			BlockHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		})
		output := indexer.NewTxOutput(100_000)
		output.OutPointStr = outpoint
		output.OutValue.PkScript = append([]byte(nil), senderScript...)
		rpc.outputs[outpoint] = output
		rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{
			OutPoint: outpoint, Value: 100_000, PkScript: append([]byte(nil), senderScript...),
		})
	}
	sender := newRGB11FlowManager(t, senderWallet, rpc, evidence, 201)
	recipient := newRGB11FlowManager(t, recipientWallet, rpc, evidence, 202)
	ctx := context.Background()
	issued, err := sender.rgbManager.IssueRGB11Asset(ctx, RGB11IssueRequest{
		Schema: "NIA", Ticker: "OWN", Name: "Stale owner evidence fixture", Amounts: []uint64{1},
	})
	if err != nil || issued == nil {
		t.Fatalf("seed stale-owner asset: result=%v err=%v", issued, err)
	}
	if _, err := sender.RefreshRGB11State(ctx); err != nil {
		t.Fatalf("refresh seed stale-owner allocation: %v", err)
	}
	balance, err := sender.rgbManager.GetRGB11AssetBalance(&issued.AssetName)
	if err != nil || balance == nil || balance.Value.Sign() <= 0 {
		t.Fatalf("seed balance: balance=%v err=%v", balance, err)
	}
	invoice, err := recipient.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
		AmountRaw: balance.Value.String(), WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	prepared, err := sender.rgbManager.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatalf("seed prepare: %v", err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatalf("seed pending: %v", err)
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
	if err != nil {
		t.Fatalf("atomic cleanup of confirmed historical input: result=%+v err=%v", result, err)
	}
	if result == nil || result.Unresolved != 0 || result.Settled != 0 ||
		result.Pending != 0 || result.Reorged != 0 || result.Conflicted != 0 {
		t.Fatalf("unexpected refresh result: %+v", result)
	}
	stored, loadErr := fixture.manager.rgbManager.projectionStore.LoadPendingTransfer(
		fixture.pending.State.TransferID,
	)
	if loadErr != nil || stored.State.Status != "settled" ||
		stored.ReservationID != fixture.pending.ReservationID {
		t.Fatalf("stale journal=%+v err=%v", stored, loadErr)
	}
	lock := fixture.manager.utxoLockerL1.GetLockedUtxoList()[fixture.input]
	if lock != nil {
		t.Fatalf("consumed input was relocked: %+v", lock)
	}
	proof, err := fixture.manager.rgbManager.projectionStore.LoadProof(
		fixture.input, fixture.pending.State.Asset.Name)
	if err != nil || proof.Status != "spending" {
		t.Fatalf("confirmed historical proof changed: %v", err)
	}
	restarted := NewUtxoLocker(fixture.manager.db, nil, L1_NETWORK_BITCOIN)
	restarted.Init()
	if restarted.GetLockedUtxoList()[fixture.input] != nil {
		t.Fatal("consumed input lock persisted after atomic cleanup")
	}
}
