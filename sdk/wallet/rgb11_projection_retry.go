package wallet

import (
	"context"
	"errors"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Only a rejected atomic projection publication can be retried here. Errors
// after a successful publication (including recovery backup failures) must
// remain visible to the caller.
type rgb11ProjectionConflict struct{ error }

func (e *rgb11ProjectionConflict) Unwrap() error { return e.error }

func rgb11ProjectionPublicationError(err error) error {
	if errors.Is(err, errAccountSnapshotChanged) || errors.Is(err, rgb11wallet.ErrLocalSnapshotChanged) || errors.Is(err, errRGB11StaleLocal) {
		return &rgb11ProjectionConflict{err}
	}
	return err
}

// Concurrent account recovery or another confirmed RGB11 settlement can win
// the projection CAS while evidence is being resolved. Rebuild the complete
// candidate from a freshly exported baseline and revalidate its chain evidence;
// never retry the stale snapshot or relax the account/scope comparison.
func retryRGB11Projection(ctx context.Context, rebuild func() (*RGB11ImportResult, error)) (*RGB11ImportResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, err := rebuild()
		var conflict *rgb11ProjectionConflict
		if attempt >= 2 || !errors.As(err, &conflict) {
			return result, err
		}
	}
}
