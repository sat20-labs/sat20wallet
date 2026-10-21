package wallet

import (
	"bytes"
	"context"
	"errors"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func TestRGB11ConflictCandidateOlderBundleReevaluation(t *testing.T) {
	f := newRGB11ConflictFixture(t, false)
	old, err := rgb11wallet.RecoveryPackageFromSnapshot(f.before, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.publish(t, true)
	// Crash boundary: only the pre-conflict complete account bundle survived.
	// No proof state or new journal marker may be borrowed from live RAM.
	snap, err := old.WalletSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	cold := newRGB11FlowManager(t, f.sender.wallet.Clone(), f.rpc, f.evidence, 9910)
	cold.rgbManager.scopeStates.stopReconciliations()
	if err = cold.rgbManager.importSelectedRGB11AccountRecoverySnapshot(snap); err != nil {
		t.Fatalf("old complete bundle safe import: %v", err)
	}
	p, err := cold.rgbManager.projectionStore.LoadPendingTransfer(f.pending.State.TransferID)
	if err != nil || p.State.Status != "conflicted" || p.State.AckStatus != "invalidated" {
		t.Fatal("cold import did not derive journal conflict from old bundle")
	}
	proof, err := cold.rgbManager.projectionStore.LoadProof(f.carrier, f.asset)
	if err != nil || proof.Status != "inconsistent" || cold.GetRGB11ConsistencyStatus() != "broken" {
		t.Fatal("cold import did not derive quarantine")
	}
}
func TestRGB11ConflictCandidateRGBMempool(t *testing.T) {
	f := newRGB11ConflictFixture(t, false)
	f.publish(t, false)
	_, _ = f.sender.RefreshRGB11State(context.Background())
	if f.journal(t).State.Status == "conflicted" {
		t.Fatal("unconfirmed RGB spender retired journal")
	}
	f.assertProtected(t)
	f.restoreInputs(t)
	if _, err := f.sender.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.assertProtected(t)
}
func TestRGB11ConflictCandidateWrongRawInput(t *testing.T) {
	f := newRGB11ConflictFixture(t, true)
	f.publish(t, true)
	// Rival is a real signed fee-only transaction, but indexer falsely claims
	// that it also consumed the RGB carrier. Exact raw/input binding must fail.
	f.evidence.mu.Lock()
	f.evidence.spendingTx[f.carrier] = f.rival.TxHash().String()
	delete(f.evidence.utxos, f.carrier)
	delete(f.rpc.outputs, f.carrier)
	f.evidence.mu.Unlock()
	if _, err := f.sender.RefreshRGB11State(context.Background()); err == nil {
		t.Fatal("raw that omits actual carrier was accepted")
	}
	if f.journal(t).State.Status != "broadcast" {
		t.Fatal("bad outspend committed conflict")
	}
	f.assertProtected(t)
	f.assertImmutable(t)
}

var errRGB11ConflictMarkerFailure = errors.New("injected account marker write failure")

type rgb11ConflictMarkerFailureDB struct {
	indexer.KVDB
	fixture *rgb11ConflictFixture
	failed  bool
}

func (db *rgb11ConflictMarkerFailureDB) Write(key, value []byte) error {
	if !db.failed && bytes.Equal(key, accountManagementProfileKey()) {
		p, err := db.fixture.sender.rgbManager.projectionStore.LoadPendingTransfer(db.fixture.pending.State.TransferID)
		if err == nil && p.State.Status == "conflicted" {
			db.failed = true
			return errRGB11ConflictMarkerFailure
		}
	}
	return db.KVDB.Write(key, value)
}
func TestRGB11ConflictCandidateMarkerFailure(t *testing.T) {
	f := newRGB11ConflictFixture(t, false)
	f.publish(t, true)
	db := &rgb11ConflictMarkerFailureDB{KVDB: f.sender.db, fixture: f}
	f.sender.db = db
	defer func() { f.sender.db = db.KVDB }()
	result, err := f.sender.RefreshRGB11State(context.Background())
	if !db.failed {
		t.Fatal("post-commit marker failure was not reached")
	}
	if !errors.Is(err, errRGB11ConflictMarkerFailure) || !errors.Is(err, ErrRGB11Inconsistent) || result == nil || len(result.Inconsistent) != 1 {
		t.Fatalf("lost committed business/backup results: result=%+v err=%v", result, err)
	}
	if f.journal(t).State.Status != "conflicted" {
		t.Fatal("marker error rolled back committed conflict")
	}
	// The existing managed wrapper still performs its final barrier even when
	// the business callback reports error; this one-shot failure is recoverable.
	if verifyErr := f.sender.verifyCurrentRGB11RecoveryClosure(); verifyErr != nil {
		t.Fatalf("final barrier skipped after business error: %v", verifyErr)
	}
}
