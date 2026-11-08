package wallet

import (
	"context"
	"errors"
	"fmt"
	"testing"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func TestNativeProjectionClassify(t *testing.T) {
	for _, item := range []struct {
		name  string
		cause error
		want  bool
	}{
		{"nil", nil, false},
		{"account", errAccountSnapshotChanged, true},
		{"engine", rgb11wallet.ErrLocalSnapshotChanged, true},
		{"local-cas", errRGB11StaleLocal, true},
		{"wrapped-local-cas", fmt.Errorf("atomic publication: %w", errRGB11StaleLocal), true},
		{"other", errors.New("publication disk failure"), false},
	} {
		t.Run(item.name, func(t *testing.T) {
			err := nativeProjectionPublicationError(item.cause)
			var conflict *nativeProjectionConflict
			if got := errors.As(err, &conflict); got != item.want {
				t.Errorf("publication conflict classification=%t want=%t: %v", got, item.want, err)
			}
			if !errors.Is(err, item.cause) {
				t.Fatalf("classification lost original cause: got=%v want=%v", err, item.cause)
			}
		})
	}
}

func TestNativeProjectionRebuild(t *testing.T) {
	var candidates []*RGB11ImportResult
	commits := 0
	result, err := retryNativeProjection(context.Background(), func() (*RGB11ImportResult, error) {
		candidate := &RGB11ImportResult{ContractID: "same-contract", Projected: len(candidates) + 1}
		candidates = append(candidates, candidate)
		if len(candidates) == 1 {
			return nil, nativeProjectionPublicationError(fmt.Errorf("atomic publication: %w", errRGB11StaleLocal))
		}
		if len(candidates) > 2 {
			t.Fatal("rebuilt again after successful publication")
		}
		commits++
		return candidate, nil
	})
	if err != nil || len(candidates) != 2 || commits != 1 {
		t.Fatalf("local CAS rejection must rebuild once: builds=%d commits=%d err=%v", len(candidates), commits, err)
	}
	if candidates[0] == candidates[1] || result != candidates[1] || result.Projected != 2 {
		t.Fatal("retry did not return the newly rebuilt candidate")
	}
}

func TestNativeProjectionCommitted(t *testing.T) {
	// Post-commit errors are unmarked, even if a backup layer wraps a snapshot
	// sentinel. Retrying them would repeat an already successful publication.
	for _, item := range []struct {
		name  string
		cause error
	}{
		{"backup", errors.New("recovery backup failed after commit")},
		{"account", fmt.Errorf("backup after commit: %w", errAccountSnapshotChanged)},
		{"engine", fmt.Errorf("backup after commit: %w", rgb11wallet.ErrLocalSnapshotChanged)},
		{"local-cas", fmt.Errorf("backup after commit: %w", errRGB11StaleLocal)},
	} {
		t.Run(item.name, func(t *testing.T) {
			published := &RGB11ImportResult{ContractID: "published-contract", Projected: 1}
			calls := 0
			result, err := retryNativeProjection(context.Background(), func() (*RGB11ImportResult, error) {
				calls++
				if calls > 1 {
					t.Fatal("retried an error after publication already committed")
				}
				return published, item.cause
			})
			if calls != 1 || result != published || err != item.cause {
				t.Fatalf("post-commit result/error changed: calls=%d same_result=%t err=%v", calls, result == published, err)
			}
		})
	}
}

func TestNativeProjectionBound(t *testing.T) {
	for _, item := range []struct {
		name  string
		cause error
	}{
		{"account", errAccountSnapshotChanged},
		{"engine", rgb11wallet.ErrLocalSnapshotChanged},
		{"local-cas", errRGB11StaleLocal},
	} {
		t.Run(item.name, func(t *testing.T) {
			calls := 0
			result, err := retryNativeProjection(context.Background(), func() (*RGB11ImportResult, error) {
				calls++
				if calls > 3 {
					t.Fatal("publication conflict exceeded three rebuild attempts")
				}
				return nil, nativeProjectionPublicationError(fmt.Errorf("atomic publication attempt %d: %w", calls, item.cause))
			})
			if calls != 3 || result != nil || !errors.Is(err, item.cause) {
				t.Fatalf("bounded conflict retry: calls=%d want=3 result_nil=%t err=%v", calls, result == nil, err)
			}
		})
	}
}
