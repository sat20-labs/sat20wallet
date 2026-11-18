//go:build pwa && guardian

package e2e

import "testing"

func TestSDKPWARealRegtestGuardianRecoveryBrowser(t *testing.T) {
	t.Setenv("SAT20_REAL_PWA_USAGE_CASE", sdkPWARegtestGuardianRecoveryCase)
	runSDKPWARealRegtestBrowser(t, "account-usage")
}
