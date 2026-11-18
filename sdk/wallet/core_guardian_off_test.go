//go:build !guardian

package wallet

import "testing"

func coreGuardianCases(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain, sender, receiver *Manager, senderMaterial *coreRecoveryMaterial) {
}
