package wallet

import (
	"strings"
	"testing"
)

// Invalid amounts must fail at the public batch boundary before a wallet,
// indexer, signer or broadcaster is accessed. Nil dependencies detect leakage.
func TestSendAssetsBTCAmountBoundary(t *testing.T) {
	manager := &Manager{}
	for _, item := range []struct{ amount, want string }{
		{"1e+24", "invalid integer format"},
		{"1.5", "maximum precision"},
		{"0", "invalid amt"},
		{"-1", "invalid amt"},
		{"9223372036854775808", "exceeds int64"},
		{"18446744073709552116", "exceeds int64"}, // 2^64+500 must not become 500 sats.
		{"1000000000000000000000000", "exceeds int64"},
	} {
		t.Run(item.amount, func(t *testing.T) {
			_, _, err := manager.BatchSendAssetsWithWallet(nil, "unused", "::", item.amount, 1, 1, nil)
			if err == nil || !strings.Contains(err.Error(), item.want) {
				t.Fatalf("got %v, want %s", err, item.want)
			}
		})
	}
	// Valid decimal boundaries progress to the next independent validation,
	// without needing transaction construction or any live dependency.
	for _, amount := range []string{"1", "330", "9223372036854775807"} {
		_, _, err := manager.BatchSendAssetsWithWallet(nil, "unused", "::", amount, 1, 1, make([]byte, 100000))
		if err == nil || !strings.Contains(err.Error(), "invalid length of null data") {
			t.Fatalf("valid amount %s rejected before memo boundary: %v", amount, err)
		}
	}
}
