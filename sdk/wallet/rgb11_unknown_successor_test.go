package wallet

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type unknownSuccessorEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	target, kind string
}

func (e *unknownSuccessorEvidence) GetRawTx(txid string) ([]byte, error) {
	if txid == e.target {
		switch e.kind {
		case "missing_raw":
			return nil, errors.New("raw transaction unavailable")
		case "malformed_raw":
			return []byte{1}, nil
		case "hash_mismatch":
			raw, err := e.BitcoinEvidenceProvider.GetRawTx(txid)
			raw = append([]byte(nil), raw...)
			if len(raw) > 0 {
				raw[len(raw)-1] ^= 1
			}
			return raw, err
		}
	}
	return e.BitcoinEvidenceProvider.GetRawTx(txid)
}

func (e *unknownSuccessorEvidence) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if txid == e.target {
		switch e.kind {
		case "unconfirmed":
			return &rgb11wallet.BitcoinTxStatus{TxID: txid, InMempool: true}, nil
		case "insufficient_confirmations":
			return &rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 1}, nil
		}
	}
	return e.BitcoinEvidenceProvider.GetTxStatus(txid)
}

func testRGB11UnknownHistoricalSuccessor(t *testing.T, source *Manager, rpc IndexerRPCClient,
	evidence rgb11wallet.BitcoinEvidenceProvider, first, second *rgb11wallet.PendingTransfer, spentChange string) {
	t.Helper()
	engine, err := source.rgbManager.engineStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := source.rgbManager.projectionStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"missing_raw", "malformed_raw", "hash_mismatch", "unconfirmed", "insufficient_confirmations", "no_candidate", "ambiguous", "explicit_conflict", "same_witness_recipients", "unknown_literal"} {
		t.Run(kind, func(t *testing.T) {
			fault := &unknownSuccessorEvidence{BitcoinEvidenceProvider: evidence, target: second.State.WitnessTxID, kind: kind}
			manager := newRGB11FlowManager(t, source.wallet, rpc, fault, source.status.CurrentWallet)
			if err := manager.rgbManager.importRGB11ReservationSnapshot(&RGB11WalletSnapshot{EngineRecords: engine, ProjectionRecords: projection}); err != nil {
				t.Fatal(err)
			}
			pending, err := manager.rgbManager.projectionStore.LoadPendingTransfer(second.State.TransferID)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "no_candidate":
				pending.State.Status = "pending"
				if err := manager.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
					t.Fatal(err)
				}
			case "insufficient_confirmations":
				pending.State.MinConfirmations = 2
				if err := manager.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
					t.Fatal(err)
				}
			case "ambiguous", "same_witness_recipients":
				// An additional recipient journal can share the same witness. A distinct
				// witness claiming this input must instead make the history ambiguous.
				extra := pending.State
				extra.TransferID = "zz-additional-recipient"
				if kind == "ambiguous" {
					extra.WitnessTxID = strings.Repeat("ab", 32)
				}
				if err := manager.rgbManager.projectionStore.SaveTransferState(&extra); err != nil {
					t.Fatal(err)
				}
			}
			receipt, err := manager.rgbManager.loadRGB11HistoricalReceipt(first)
			if err != nil {
				t.Fatal(err)
			}
			var allocation *rgb11wallet.ValidatedAllocation
			for i := range receipt.Allocations {
				if receipt.Allocations[i].OutPoint == spentChange {
					allocation = &receipt.Allocations[i]
				}
			}
			if allocation == nil {
				t.Fatal("historical change allocation missing")
			}
			spender := ""
			if kind == "unknown_literal" {
				spender = "unknown"
			}
			if kind == "explicit_conflict" {
				spender = strings.Repeat("ab", 32)
			}
			err = manager.rgbManager.validateRGB11SpentChangeHistory(context.Background(), first, receipt, *allocation, spender)
			wantSuccess := kind == "same_witness_recipients" || kind == "unknown_literal"
			if (err == nil) != wantSuccess {
				t.Fatalf("success=%v want=%v err=%v", err == nil, wantSuccess, err)
			}
			if kind == "ambiguous" && !strings.Contains(err.Error(), "ambiguous") {
				t.Fatalf("wrong ambiguity rejection: %v", err)
			}
		})
	}
}

func TestRGB11ExpectedSpendRejectsDuplicateInput(t *testing.T) {
	outpoint := wire.OutPoint{Index: 1}
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(wire.NewTxIn(&outpoint, nil, nil))
	tx.AddTxIn(wire.NewTxIn(&outpoint, nil, nil))
	tx.AddTxOut(wire.NewTxOut(1, []byte{0x51}))
	var raw bytes.Buffer
	if err := tx.Serialize(&raw); err != nil {
		t.Fatal(err)
	}
	txid := tx.TxHash().String()
	evidence := expectedSpendEvidence{
		status: &rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 6}, raw: raw.Bytes(),
	}
	if _, verified := verifyRGB11ExpectedSpend(evidence, outpoint.String(), txid); verified {
		t.Fatal("duplicate input was accepted as a unique spend proof")
	}
}
