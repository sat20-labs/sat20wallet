package wallet

import (
	"context"
	"errors"
	"testing"
)

func TestRGB11ConflictCandidateLocalBaseline(t *testing.T) {
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
	p := f.journal(t)
	p.State.MinConfirmations = 2
	if err = f.sender.rgbManager.projectionStore.SavePendingTransferState(p); err != nil {
		t.Fatal(err)
	}
	err = f.sender.rgbManager.importRGB11ReservationSnapshotWithBaseline(ctx, resolved.snapshot, resolved.tickerInfos, resolved.lockReasons, &plan, resolved.checkEvidence, current)
	if !errors.Is(err, errAccountSnapshotChanged) {
		t.Fatalf("old conflict candidate overwrote new local baseline: %v", err)
	}
	p = f.journal(t)
	if p.State.Status != "broadcast" || p.State.MinConfirmations != 2 {
		t.Fatal("concurrent local obligation lost")
	}
	f.assertProtected(t)
}
