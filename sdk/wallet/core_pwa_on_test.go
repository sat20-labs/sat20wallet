//go:build pwa

package wallet

import (
	"context"
	"testing"
)

func coreBrowserCases(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain, sender, receiver *Manager, senderMaterial *coreRecoveryMaterial) {
	rootID := sender.GetAccountManagementStatus().RootWalletID
	t.Run("PWA_RGB11_nonempty_recovery_and_failure_retry", func(t *testing.T) {
		var rootName string
		for _, item := range sender.GetWalletCatalog() {
			if item.ID == rootID {
				rootName = item.Name
			}
		}
		coreRGBBrowserE2E(t, sender, cfg, chain, senderMaterial)
		// The browser's import-failure journey restores its metadata afterward.
		// Adopt that final generation before later cases reconfigure the backup.
		coreRequire(t, "sync browser final catalog", sender.SyncAccountManagementState(context.Background()))
		for _, item := range sender.GetWalletCatalog() {
			if item.ID == rootID {
				coreAssert(t, item.Name == rootName, "browser did not restore the original root wallet name")
				return
			}
		}
		t.Fatal("browser catalog lost the root wallet")
	})
}
