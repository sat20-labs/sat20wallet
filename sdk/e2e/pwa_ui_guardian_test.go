//go:build pwa && guardian

package e2e

import "testing"

func TestPWAGuardianUIBridge(t *testing.T) {
	runSDKUICases(t, []sdkUICase{{"GuardianDisabled", "SDK UI: Guardian entry points are disabled"}})
}
