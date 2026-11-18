//go:build pwa

package e2e

import "testing"

// Actual settings and recovery pages use the production WASM on a real Core.
// Fresh local identities need no payment, L1 transaction or shared wallet.
func TestPWAAccountViews(t *testing.T) {
	runSDKUICases(t, []sdkUICase{
		{"TwoFactor", "PWA account: two-factor setup downloads rehearsal abort and cold restore"},
		{"GuardianDisabled", "PWA account: Guardian is unavailable and own storage quotes remain usable"},
		{"PrivateKey", "PWA account: private-key cold unlock and activation refusal"},
	})
}
