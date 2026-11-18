package rgb11wallet

import (
	"context"

	"github.com/sat20-labs/rgb11/consignment"
)

// RGB11BitcoinResolver adapts existing Bitcoin facts to the shared RGB11
// proof validator. It exposes no wallet balances or projection authority.
func RGB11BitcoinResolver(ctx context.Context, evidence BitcoinEvidenceProvider) consignment.BitcoinResolver {
	if ctx == nil {
		ctx = context.Background()
	}
	return coreEvidenceResolver{ctx: ctx, provider: ScopedBitcoinEvidence(evidence)}
}
