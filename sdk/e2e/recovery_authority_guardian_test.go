//go:build guardian

package e2e

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

func runImmediateRecoveryCase(t *testing.T, fixture *templateFixture) {
	network := fixture.Network
	runSDKAccountDKVSSyncGateCase(t, fixture, "10_RecoveryAndGuardianAreImmediatelyAuthoritative", []byte{10, 240}, func(t *testing.T) {
		// This case tests new recovery objects for already activated accounts,
		// not the first-activation ordering of a 2of3 recovery configuration.
		owner := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 10), true)
		owner.activate(t)
		guardianOwner := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 240), true)
		guardianOwner.activate(t)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for i, fixture := range []*accountReviewFixture{owner, guardianOwner} {
			role := []string{"owner", "guardian"}[i]
			require.NoError(t, fixture.manager.SyncAccountManagementState(ctx), role+" must finish real account synchronization")
			status := fixture.manager.GetAccountManagementStatus()
			t.Logf("H1 participant=%s discovery=%s dkvs_status=%s recovery_configured=%t activation_pending=%t dkvs_write_allowed=%t",
				role, status.DiscoveryStatus, status.DKVSStatus, status.RecoveryConfigured, status.ActivationPending, status.DKVSWriteAllowed)
			require.True(t, status.RecoveryConfigured, role+" must have actually activated account management")
			require.False(t, status.ActivationPending)
			require.Equal(t, "active", status.DiscoveryStatus)
			require.True(t, status.DKVSWriteAllowed)
		}
		manager, location := owner.manager, owner.location
		guardianManager, guardianLocation := guardianOwner.manager, guardianOwner.location
		auth, err := manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
		require.NoError(t, err)
		backup, err := manager.ExportAccountBackupForPWA(accountReviewPassword, nil)
		require.NoError(t, err)
		identity, err := guardianManager.GetOrCreateAccountGuardianIdentity(accountReviewPassword)
		require.NoError(t, err)
		guardianPublic, err := base64.RawURLEncoding.DecodeString(identity.PublicKey)
		require.NoError(t, err)
		guardianPrivate, err := guardianManager.LoadAccountGuardianPrivateKey(accountReviewPassword)
		require.NoError(t, err)
		defer clearBytes(guardianPrivate)
		accountID := manager.GetAccountManagementStatus().AccountID
		require.NotEqual(t, accountID, identity.MailboxID)
		pkg, err := manager.CreateAccountRecoveryPackage(account.CreateOptions{
			AccountID: accountID, Backup: backup, RecoveryMode: account.RecoveryMode2Of3,
			Questions: e2eKnowledgeQuestions(), GuardianMailboxID: identity.MailboxID,
			GuardianPublicKey: guardianPublic,
		})
		require.NoError(t, err)
		require.NotNil(t, pkg.GuardianCapsule)
		repository, err := manager.NewAccountRepositoryForStorage(*auth)
		require.NoError(t, err)
		require.NoError(t, account.NewManager(repository).Publish(context.Background(), *pkg))
		_, err = guardianManager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
		require.NoError(t, err)
		require.NoError(t, guardianManager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeGuardian,
			func(authorization *wallet.AccountStorageAuthorization) error {
				return guardianManager.PutGuardianCapsuleForStorage(*authorization, identity.MailboxID, *pkg.GuardianCapsule)
			}))

		loaded, err := manager.LoadAccountRecoveryPackage(location, pkg.Envelope.Locator)
		require.NoError(t, err, "recovery read must not wait for the local active replica")
		guardianRaw, err := guardianManager.LoadAccountGuardianCapsule(guardianLocation, identity.MailboxID,
			pkg.GuardianCapsule.PackageID, pkg.GuardianCapsule.ShareID)
		require.NoError(t, err, "guardian read must not wait for the local active replica")

		var guardian account.GuardianShareCapsule
		require.NoError(t, json.Unmarshal(guardianRaw, &guardian))
		guardianShare, err := account.DecryptGuardianShare(guardian, guardianPrivate)
		require.NoError(t, err)
		dkvsShare, err := account.RecoverDKVSShare(loaded.DKVSShareCapsule,
			loaded.KnowledgeBundle, e2eKnowledgeAnswers())
		require.NoError(t, err)
		restored, secret, err := account.RecoverAccount(loaded.Envelope, dkvsShare, guardianShare)
		require.NoError(t, err)
		defer clearBytes(secret)
		require.Equal(t, backup.Wallets, restored.Wallets)
		require.True(t, bytes.Equal(owner.secret, secret), "the new 2of3 material must recover the active owner's existing account secret")
	})

}
