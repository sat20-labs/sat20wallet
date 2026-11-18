//go:build !pwa

package wallet

import "testing"

func coreBrowserCases(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain, sender, receiver *Manager, senderMaterial *coreRecoveryMaterial) {
}
