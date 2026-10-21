package wallet

import (
	"context"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"reflect"
	"testing"
)

type rgb11ConflictValidWrongRaw struct {
	rgb11wallet.BitcoinEvidenceProvider
	txid string
	raw  []byte
}

func (e *rgb11ConflictValidWrongRaw) GetRawTx(txid string) ([]byte, error) {
	if txid == e.txid {
		return append([]byte(nil), e.raw...), nil
	}
	return e.BitcoinEvidenceProvider.GetRawTx(txid)
}
func TestRGB11ConflictCandidateRejectsValidWrongHash(t *testing.T) {
	f := newRGB11ConflictFixture(t, false)
	f.publish(t, true)
	f.sender.rgbManager.evidence = &rgb11ConflictValidWrongRaw{BitcoinEvidenceProvider: f.evidence, txid: f.rival.TxHash().String(), raw: f.pending.SignedTx}
	if _, err := f.sender.RefreshRGB11State(context.Background()); err == nil {
		t.Fatal("valid different raw transaction accepted for rival hash")
	}
	if !reflect.DeepEqual(f.before.ProjectionRecords, f.snapshot(t).ProjectionRecords) {
		t.Fatal("wrong hash changed projection")
	}
	f.assertProtected(t)
}
func TestRGB11ConflictCandidateOwnWitnessPrecommit(t *testing.T) {
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
	// Same tip and same fee outspend; only the original witness changes.
	f.evidence.setStatus(f.pending.State.WitnessTxID, rgb11wallet.BitcoinTxStatus{TxID: f.pending.State.WitnessTxID, InMempool: true})
	err = f.sender.rgbManager.importRGB11ReservationSnapshotWithBaseline(ctx, resolved.snapshot, resolved.tickerInfos, resolved.lockReasons, &plan, resolved.checkEvidence, current)
	if err == nil {
		t.Fatal("changed original witness did not invalidate terminal candidate")
	}
	if !reflect.DeepEqual(current.ProjectionRecords, f.snapshot(t).ProjectionRecords) {
		t.Fatal("stale witness changed projection")
	}
	f.assertProtected(t)
}
func TestRGB11ConflictCandidateRejectsIncompleteAuthoritativeRecovery(t *testing.T) {
	f := newRGB11ConflictFixture(t, false)
	pack, err := rgb11wallet.RecoveryPackageFromSnapshot(f.beforePrepare, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := pack.WalletSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err = f.sender.rgbManager.importSelectedRGB11AccountRecoverySnapshot(snapshot); err == nil {
		t.Fatal("older recovery erased signed local responsibility")
	}
	if !reflect.DeepEqual(f.before.ProjectionRecords, f.snapshot(t).ProjectionRecords) {
		t.Fatal("incomplete recovery changed projection")
	}
	f.assertProtected(t)
}
func TestRGB11ConflictCandidateInterruptedBeforeDirtyReload(t *testing.T) {
	f := newRGB11ConflictFixture(t, false)
	// Confirm the genuine current account closure before reproducing the exact
	// prefix of public refresh ending after apply and before dirty persistence.
	if err := f.sender.syncRGB11RecoveryBarrier(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.publish(t, true)
	current := f.snapshot(t)
	candidate, _, err := f.sender.rgbManager.buildRGB11RefreshCandidate(context.Background(), current, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.sender.rgbManager.applyRGB11CandidateSnapshot(context.Background(), current, candidate); err != nil {
		t.Fatal(err)
	}
	f.sender.rgbManager.scopeStates.stopReconciliations()
	runtime, err := newRGB11Manager(f.sender, f.sender.db, f.sender.utxoLockerL1, f.evidence)
	if err != nil {
		t.Fatal(err)
	}
	f.sender.rgbManager = runtime
	runtime.scopeStates.stopReconciliations()
	if err = runtime.selectRGB11Scope(); err != nil {
		t.Fatal(err)
	}
	f.sender.mutex.Lock()
	f.sender.accountProfile = nil
	err = f.sender.loadAccountManagementProfileLocked()
	f.sender.mutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = f.sender.verifyCurrentRGB11RecoveryClosure(); err == nil {
		t.Fatal("interrupted dirty write falsely reported synchronized")
	}
	if f.journal(t).State.Status != "conflicted" {
		t.Fatal("local committed journal lost on runtime reload")
	}
	if err = f.sender.syncRGB11RecoveryBarrier(context.Background()); err != nil {
		t.Fatalf("existing restart barrier cannot reconcile local closure: %v", err)
	}
	if err = f.sender.verifyCurrentRGB11RecoveryClosure(); err != nil {
		t.Fatalf("reconciled closure remains unverified: %v", err)
	}
	proof, err := f.sender.rgbManager.projectionStore.LoadProof(f.carrier, f.asset)
	if err != nil || proof.Status != "inconsistent" || f.journal(t).State.Status != "conflicted" {
		t.Fatal("barrier discarded committed quarantine")
	}
	f.assertImmutable(t)
}
