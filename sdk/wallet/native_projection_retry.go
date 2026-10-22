package wallet

import (
	"context"
	"errors"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Only a rejected atomic projection publication can be retried here. Errors
// after a successful publication (including recovery backup failures) must
// remain visible to the caller.
type nativeProjectionConflict struct{ error }

func (e *nativeProjectionConflict) Unwrap() error { return e.error }

func nativeProjectionPublicationError(err error) error {
	if errors.Is(err, errAccountSnapshotChanged) || errors.Is(err, rgb11wallet.ErrLocalSnapshotChanged) {
		return &nativeProjectionConflict{err}
	}
	return err
}

// Concurrent account recovery or another confirmed native settlement can win
// the projection CAS while evidence is being resolved. Rebuild the complete
// candidate from a freshly exported baseline and revalidate its chain evidence;
// never retry the stale snapshot or relax the account/scope comparison.
func retryNativeProjection(ctx context.Context, rebuild func() (*RGB11ImportResult, error)) (*RGB11ImportResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, err := rebuild()
		var conflict *nativeProjectionConflict
		if attempt >= 2 || !errors.As(err, &conflict) {
			return result, err
		}
	}
}
