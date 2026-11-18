package e2e

import "testing"

// Retained for explicitly tagged PWA transport fixtures only.
type sdkUICase struct{ name, journey string }

func TestSDKAccountFlows(t *testing.T) {
	fixture := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, fixture.Network)
	config, _ := accountReviewConfig(t, fixture.Network)
	t.Run("TwoFactorRecovery", func(t *testing.T) { runNativeTwoFactor(t, config) })
	t.Run("NoACK", func(t *testing.T) {
		t.Run("ExplicitRetry", func(t *testing.T) { runNativeUINoACK(t, config, true) })
		t.Run("AutomaticReconnect", func(t *testing.T) { runNativeUINoACK(t, config, false) })
	})
}

func TestSDKPaidAccountFlow(t *testing.T) {
	runNativePaidUI(t)
}
