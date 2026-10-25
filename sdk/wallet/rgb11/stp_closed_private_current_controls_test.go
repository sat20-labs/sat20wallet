package rgb11wallet

import (
	"context"
	"strings"
	"testing"

	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
)

// The private exception must still enter through Current validation. Exercise
// that unchanged real-history boundary with a normal local issuance fixture.
func TestSTPClosedPrivateFundingStillRejectsSpentCarrier(t *testing.T) {
	sources, facts := issuedChannelFunding(t)
	source := sources[0]
	if _, err := ValidateChannelFunding(context.Background(), source, facts, nativeproof.Current, "regtest"); err != nil {
		t.Fatalf("ordinary unspent fixture rejected: %v", err)
	}
	facts.spent[source.OutPoint] = strings.Repeat("d", 64)
	if _, err := ValidateChannelFunding(context.Background(), source, facts, nativeproof.Current, "regtest"); err == nil {
		t.Fatal("spent private carrier accepted as current funding")
	}
}
