package wallet

import (
	"context"
	"errors"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"reflect"
	"testing"
)

type rgb11ConflictFaultEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	txid, mode string
}

func (e *rgb11ConflictFaultEvidence) GetRawTx(txid string) ([]byte, error) {
	if txid == e.txid {
		switch e.mode {
		case "missing_raw":
			return nil, errors.New("injected rival raw unavailable")
		case "wrong_hash":
			return []byte{1, 2, 3}, nil
		case "wrong_input":
			return e.BitcoinEvidenceProvider.GetRawTx(e.txid + "-not-a-tx")
		}
	}
	return e.BitcoinEvidenceProvider.GetRawTx(txid)
}
func (e *rgb11ConflictFaultEvidence) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if txid == e.txid {
		switch e.mode {
		case "status_error":
			return nil, errors.New("injected rival status unavailable")
		case "contradictory_status":
			return &rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 6, InMempool: true}, nil
		case "zero_confirmed":
			return &rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true}, nil
		}
	}
	return e.BitcoinEvidenceProvider.GetTxStatus(txid)
}
func TestRGB11ConflictCandidateRejectsBadEvidence(t *testing.T) {
	for _, mode := range []string{"missing_raw", "wrong_hash", "status_error", "contradictory_status", "zero_confirmed"} {
		t.Run(mode, func(t *testing.T) {
			f := newRGB11ConflictFixture(t, false)
			f.publish(t, true)
			fault := &rgb11ConflictFaultEvidence{BitcoinEvidenceProvider: f.evidence, txid: f.rival.TxHash().String(), mode: mode}
			f.sender.rgbManager.evidence = fault
			_, err := f.sender.RefreshRGB11State(context.Background())
			if err == nil {
				t.Fatal("bad conflict evidence accepted")
			}
			p := f.journal(t)
			if p.State.Status != "broadcast" || p.State.AckStatus != "accepted" {
				t.Fatal("invalid evidence committed terminal state")
			}
			f.assertProtected(t)
			f.assertImmutable(t)
		})
	}
}
func TestRGB11ConflictCandidateFeeEvidencePrecommit(t *testing.T) {
	f := newRGB11ConflictFixture(t, true)
	f.publish(t, true)
	ctx := context.Background()
	current := f.snapshot(t)
	candidate, _, err := f.sender.rgbManager.buildRGB11RefreshCandidate(ctx, current, 0)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := f.sender.rgbManager.resolveRGB11RecoveryProjection(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.sender.rgbManager.rgb11SnapshotLockPlan(current, resolved.snapshot, resolved.lockReasons)
	if err != nil {
		t.Fatal(err)
	}
	// Same tip, only the fee-input outspend changes after resolution. The
	// atomic importer must reject before DB or lock publication.
	f.restoreInputs(t)
	err = f.sender.rgbManager.importRGB11ReservationSnapshotWithBaseline(ctx, resolved.snapshot, resolved.tickerInfos, resolved.lockReasons, &plan, resolved.checkEvidence, current)
	if err == nil {
		t.Fatal("stale fee-only conflict retired its owner")
	}
	if !reflect.DeepEqual(f.snapshot(t).ProjectionRecords, current.ProjectionRecords) {
		t.Fatal("stale candidate partially changed projection")
	}
	f.assertProtected(t)
}
