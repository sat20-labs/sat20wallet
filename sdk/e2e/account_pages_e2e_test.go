//go:build pwa

package e2e

import "testing"

// Actual settings and recovery pages use the production WASM on a real Core.
// Fresh local identities need no payment, L1 transaction or shared wallet.
func TestPWAAccountViews(t *testing.T) {
	runSDKUICases(t, []sdkUICase{
		{"TwoFactor", "PWA account: existing recovery file rehearsal abort and cold restore"},
		{"GuardianDisabled", "PWA account: root-only backup activates with temporary storage"},
		{"PrivateKey", "PWA account: private-key cold unlock and activation refusal"},
	})
}
