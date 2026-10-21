package wallet

import (
	"context"
	"errors"
	"testing"
)

func TestRGB11ConflictCandidateReconfirmationResult(t *testing.T) {
	f := newRGB11ConflictFixture(t, false)
	f.publish(t, true)
	if _, err := f.sender.RefreshRGB11State(context.Background()); !errors.Is(err, ErrRGB11Inconsistent) {
		t.Fatal("conflict setup")
	}
	f.restoreInputs(t)
	f.exposeSignedWitness(t, f.pending.SignedTx, 6)
	result, err := f.sender.RefreshRGB11State(context.Background())
	if err != nil || result == nil || result.Settled != 1 || result.Conflicted != 0 {
		t.Fatalf("committed reconfirmation result=%+v err=%v", result, err)
	}
}
