//go:build guardian

package e2e

import "testing"

func runGuardianRecoveryCase(t *testing.T, network *realSatoshiNet) {
	guardianMnemonic := accountSyncMnemonic(t, 217)
	userMnemonic := accountSyncMnemonic(t, 218)
	runAccountReviewCaseWithBindingDelta(t, network, guardianMnemonic, func(t *testing.T,
		network *realSatoshiNet, _ string) {
		runSDKAccountGuardianIdentitySurvivesAccountRecovery(t, network, guardianMnemonic, userMnemonic)
	})
}
