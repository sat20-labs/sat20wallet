package wallet

import (
	"context"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"reflect"
	"strings"
	"testing"
)

func TestRGB11ConflictCandidateRejectsJournalInputTampering(t *testing.T) {
	for _, mode := range []string{"duplicate_listed_input", "omitted_fee_input"} {
		t.Run(mode, func(t *testing.T) {
			f := newRGB11ConflictFixture(t, false)
			f.publish(t, true)
			p := f.journal(t)
			if len(p.State.InputOutPoints) < 2 {
				t.Fatal("need actual fee input")
			}
			if mode == "duplicate_listed_input" {
				p.State.InputOutPoints = []string{p.State.InputOutPoints[0], p.State.InputOutPoints[0]}
			} else {
				p.State.InputOutPoints = p.State.InputOutPoints[:1]
			}
			if err := f.sender.rgbManager.projectionStore.SavePendingTransferState(p); err != nil {
				t.Fatal(err)
			}
			before := f.snapshot(t)
			if _, err := f.sender.RefreshRGB11State(context.Background()); err == nil {
				t.Fatal("incomplete signed input set was accepted")
			}
			if !reflect.DeepEqual(before.ProjectionRecords, f.snapshot(t).ProjectionRecords) {
				t.Fatal("bad input set changed persistent projection")
			}
			f.assertProtected(t)
		})
	}
}
func TestRGB11ConflictCandidateRejectsChangedMaterial(t *testing.T) {
	for _, prefix := range []string{"object-", "validation-"} {
		t.Run(prefix, func(t *testing.T) {
			f := newRGB11ConflictFixture(t, false)
			f.publish(t, true)
			ctx := context.Background()
			current := f.snapshot(t)
			candidate, _, err := f.sender.rgbManager.buildRGB11RefreshCandidate(ctx, current, 0)
			if err != nil {
				t.Fatal(err)
			}
			candidate.ProjectionRecords = append([]rgb11wallet.SnapshotRecord(nil), candidate.ProjectionRecords...)
			changed := false
			for i, r := range candidate.ProjectionRecords {
				if strings.HasPrefix(r.Key, prefix) && len(r.Value) > 0 {
					candidate.ProjectionRecords[i].Value = append([]byte(nil), r.Value...)
					candidate.ProjectionRecords[i].Value[0] ^= 0x80
					changed = true
					break
				}
			}
			if !changed {
				t.Fatal("no actual material targeted")
			}
			if _, err = f.sender.rgbManager.applyRGB11CandidateSnapshot(ctx, current, candidate); err == nil {
				t.Fatal("changed authenticated material bypassed quarantine validation")
			}
			if !reflect.DeepEqual(current.ProjectionRecords, f.snapshot(t).ProjectionRecords) {
				t.Fatal("bad material changed persistent projection")
			}
			f.assertProtected(t)
		})
	}
}
