package e2e

import "testing"

func TestSDKAccountReview(t *testing.T) {
	runNativeAccountReview(t, newNativeIfaceChain(t))
}

func runNativeAccountReview(t *testing.T, chain *nativeIfaceChain) {
	t.Helper()
	for _, item := range []struct {
		name    string
		pending bool
	}{
		{"WalletReimportPending", true}, {"WalletReimportConfirmed", false},
	} {
		t.Run(item.name, func(t *testing.T) { runNativeReimport(t, chain, item.pending) })
	}
}
