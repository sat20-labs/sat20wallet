//go:build guardian

package wallet

import (
	"context"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"testing"
)

func coreGuardianCases(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain, sender, receiver *Manager, senderMaterial *coreRecoveryMaterial) {
	t.Run("guardian_two_of_three_and_wrong_knowledge", func(t *testing.T) {
		// The browser may have advanced the shared account even if its gate failed.
		coreRequire(t, "sync current account before guardian reconfiguration", sender.SyncAccountManagementState(context.Background()))
		corePrepRGBFailureScopes(t, sender, cfg, chain)
		material := coreActivateE2E(t, sender, account.RecoveryMode2Of3, receiver)
		coreCheckpoint(t, "guardian_and_knowledge_restore_catalog_and_RGB", sender, cfg, chain, material)
		blank := coreNewE2EManager(t, cfg, chain, "")
		pkg, err := blank.LoadAccountRecoveryPackage(material.auth.Location, material.locator)
		coreRequire(t, "load package for negative test", err)
		questions, err := account.OpenRecoveryKnowledge(*pkg, material.userShare)
		coreRequire(t, "decrypt recovery questions with user share", err)
		_, err = account.RecoverDKVSShare(pkg.DKVSShareCapsule, questions,
			[]account.AnswerAttempt{{QuestionID: "one", Answer: "incorrect"}, {QuestionID: "two", Answer: "incorrect"}})
		coreAssert(t, err != nil && len(blank.GetWalletCatalog()) == 0, "wrong knowledge changed empty wallet state")
	})
}
