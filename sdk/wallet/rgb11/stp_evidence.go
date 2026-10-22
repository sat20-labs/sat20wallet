package rgb11wallet

import (
	"context"

	"github.com/sat20-labs/rgb11/consignment"
)

// NativeBitcoinResolver adapts existing Bitcoin facts to the shared native
// proof validator. It exposes no wallet balances or projection authority.
func NativeBitcoinResolver(ctx context.Context, evidence BitcoinEvidenceProvider) consignment.BitcoinResolver {
	if ctx == nil {
		ctx = context.Background()
	}
	return coreEvidenceResolver{ctx: ctx, provider: evidence}
}
