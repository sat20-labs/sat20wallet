package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type expectedSpendEvidence struct {
	status    *rgb11wallet.BitcoinTxStatus
	statusErr error
	raw       []byte
	rawErr    error
}

func (e expectedSpendEvidence) GetUTXO(string) (*rgb11wallet.BitcoinUTXO, error) {
	return nil, nil
}
func (e expectedSpendEvidence) GetRawTx(string) ([]byte, error) { return e.raw, e.rawErr }
func (e expectedSpendEvidence) GetTxStatus(string) (*rgb11wallet.BitcoinTxStatus, error) {
	return e.status, e.statusErr
}
func (e expectedSpendEvidence) GetOutspend(string) (*rgb11wallet.BitcoinOutspend, error) {
	return &rgb11wallet.BitcoinOutspend{Spent: true}, nil
}
func (e expectedSpendEvidence) GetTip() (*rgb11wallet.BitcoinTip, error) { return nil, nil }
func (e expectedSpendEvidence) Broadcast([]byte) (string, error)         { return "", nil }

type delayedSpendEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	txid, outpoint string
	statusCalls    int
	hiddenOutputs  map[string]bool
}

func (e *delayedSpendEvidence) GetUTXO(outpoint string) (*rgb11wallet.BitcoinUTXO, error) {
	if e.hiddenOutputs[outpoint] {
		delete(e.hiddenOutputs, outpoint)
		return nil, nil
	}
	return e.BitcoinEvidenceProvider.GetUTXO(outpoint)
}

func (e *delayedSpendEvidence) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if txid == e.txid {
		e.statusCalls++
		if e.statusCalls == 1 {
			return &rgb11wallet.BitcoinTxStatus{TxID: txid}, nil
		}
	}
	return e.BitcoinEvidenceProvider.GetTxStatus(txid)
}

func (e *delayedSpendEvidence) GetOutspend(outpoint string) (*rgb11wallet.BitcoinOutspend, error) {
	if outpoint == e.outpoint {
		return &rgb11wallet.BitcoinOutspend{Spent: true}, nil
	}
	return e.BitcoinEvidenceProvider.GetOutspend(outpoint)
}

func TestRGB11ExpectedRawSpend(t *testing.T) {
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
	pending.State.Status, pending.State.AckStatus = "broadcast", "accepted"
	if err := sender.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	txid := tx.TxHash().String()
	target := pending.State.InputOutPoints[0]
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
	evidence.setStatus(txid, rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 6})
	hiddenOutputs := make(map[string]bool, len(pending.State.OutputOutPoints))
	for _, outpoint := range pending.State.OutputOutPoints {
		hiddenOutputs[outpoint] = true
	}
	fault := &delayedSpendEvidence{
		BitcoinEvidenceProvider: evidence, txid: txid, outpoint: target,
		hiddenOutputs: hiddenOutputs,
	}
	sender.rgbManager.evidence = fault

	result, refreshErr := sender.RefreshRGB11State(ctx)
	if refreshErr != nil || result.Settled != 1 || sender.GetRGB11ConsistencyStatus() != "ok" {
		t.Fatalf("verified expected spend unresolved: result=%+v err=%v consistency=%s",
			result, refreshErr, sender.GetRGB11ConsistencyStatus())
	}
	if fault.statusCalls < 2 {
		t.Fatalf("expected status fallback was not exercised: calls=%d", fault.statusCalls)
	}
	stored, err := sender.rgbManager.projectionStore.LoadPendingTransfer(pending.State.TransferID)
	if err != nil || stored.State.Status != "settled" {
		t.Fatalf("transfer=%+v err=%v", stored, err)
	}
}

func TestRGB11ExpectedSpendChecks(t *testing.T) {
	inputHash := chainhash.Hash{1}
	outpoint := wire.OutPoint{Hash: inputHash, Index: 1}
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(wire.NewTxIn(&outpoint, nil, nil))
	tx.AddTxOut(wire.NewTxOut(1, []byte{0x51}))
	var encoded bytes.Buffer
	if err := tx.Serialize(&encoded); err != nil {
		t.Fatal(err)
	}
	txid := tx.TxHash().String()
	confirmed := &rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 6}
	valid := expectedSpendEvidence{status: confirmed, raw: encoded.Bytes()}
	if status, ok := verifyRGB11ExpectedSpend(valid, outpoint.String(), txid); !ok || status.TxID != txid {
		t.Fatal("confirmed exact raw spend was rejected")
	}
	tests := []struct {
		name     string
		evidence expectedSpendEvidence
		point    string
	}{
		{name: "unconfirmed", evidence: expectedSpendEvidence{
			status: &rgb11wallet.BitcoinTxStatus{TxID: txid, InMempool: true}, raw: encoded.Bytes()}},
		{name: "status_error", evidence: expectedSpendEvidence{statusErr: errors.New("offline"), raw: encoded.Bytes()}},
		{name: "raw_error", evidence: expectedSpendEvidence{status: confirmed, rawErr: errors.New("offline")}},
		{name: "raw_mismatch", evidence: expectedSpendEvidence{status: confirmed, raw: []byte{0x01}}},
		{name: "wrong_input", evidence: valid, point: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			point := test.point
			if point == "" {
				point = outpoint.String()
			}
			if _, ok := verifyRGB11ExpectedSpend(test.evidence, point, txid); ok {
				t.Fatal("unverified expected spend was accepted")
			}
		})
	}
}
