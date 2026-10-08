package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

// The Guardian is itself an account owner. Restore it into empty databases;
// neither the original database nor any device-only identity key is copied.
func TestSDKAccountGuardianIdentitySurvivesAccountRecovery(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	g := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 217), true)
	identity, err := g.manager.GetOrCreateAccountGuardianIdentity(accountReviewPassword)
	require.NoError(t, err)
	g.activate(t)
	u := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 218), true)
	publicKey, err := base64.RawURLEncoding.DecodeString(identity.PublicKey)
	require.NoError(t, err)
	backup, err := u.manager.ExportAccountBackupForPWA(accountReviewPassword, nil)
	require.NoError(t, err)
	pkg, err := u.manager.CreateAccountRecoveryPackage(account.CreateOptions{
		AccountID: u.manager.GetAccountManagementStatus().AccountID, Backup: backup,
		RecoveryMode: account.RecoveryMode2Of3, Questions: e2eKnowledgeQuestions(),
		GuardianMailboxID: identity.MailboxID, GuardianPublicKey: publicKey,
	})
	require.NoError(t, err)
	_, err = g.manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
	require.NoError(t, err)
	require.NoError(t, g.manager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeGuardian,
		func(auth *wallet.AccountStorageAuthorization) error {
			return g.manager.PutGuardianCapsuleForStorage(*auth, identity.MailboxID, *pkg.GuardianCapsule)
		}))
	require.NoError(t, g.manager.SyncAccountManagementState(context.Background()))
	g.manager.Close()
	u.manager.Close()

	for _, mode := range []string{"root-mnemonic", "recovery-material"} {
		t.Run(mode, func(t *testing.T) {
			fresh, _ := accountReviewDevice(t, network, "")
			require.Empty(t, fresh.GetWalletCatalog())
			const newPassword = "guardian-new-device-password"
			if mode == "root-mnemonic" {
				require.NoError(t, fresh.StartDKVSSync())
				requireAccountPWARootRecovery(t, fresh, g.rootMnemonic, newPassword)
			} else {
				loaded, err := fresh.LoadAccountRecoveryPackage(g.location, g.pkg.Envelope.Locator)
				require.NoError(t, err)
				knowledge, err := account.RecoverDKVSShare(loaded.DKVSShareCapsule, loaded.KnowledgeBundle, e2eKnowledgeAnswers())
				require.NoError(t, err)
				backup, secret, err := account.RecoverAccount(loaded.Envelope, g.pkg.UserShare, knowledge)
				require.NoError(t, err)
				defer clearBytes(secret)
				state, err := fresh.LoadAccountManagementStateForRecovery(g.location, loaded.Envelope.Locator, secret, backup.Wallets[0].Mnemonic)
				require.NoError(t, err)
				_, err = fresh.RestoreAccountManagementState(*state, secret, newPassword, loaded.Envelope.Locator, g.restoreOptions())
				require.NoError(t, err)
			}
			// Derive directly after restoring the root; no identity-generation step.
			privateKey, err := fresh.LoadAccountGuardianPrivateKey(newPassword)
			if err == nil {
				defer clearBytes(privateKey)
			}
			require.NoError(t, err)
			stable, err := fresh.GetOrCreateAccountGuardianIdentity(newPassword)
			require.NoError(t, err)
			require.Equal(t, identity, stable)
			_, err = fresh.LoadAccountGuardianPrivateKey(accountReviewPassword)
			require.Error(t, err, "derivation must authenticate the restored wallet password")
			encoded, err := fresh.LoadAccountGuardianCapsule(g.location, identity.MailboxID,
				pkg.Envelope.Locator.PackageID, pkg.Manifest.Guardian.ShareID)
			require.NoError(t, err)
			var capsule account.GuardianShareCapsule
			require.NoError(t, json.Unmarshal(encoded, &capsule))
			share, err := account.DecryptGuardianShare(capsule, privateKey)
			require.NoError(t, err)
			requestKey, requestPub, err := account.GenerateGuardianKey(nil)
			require.NoError(t, err)
			defer clearBytes(requestKey)
			response, err := account.EncryptGuardianShare(share, requestPub, nil)
			require.NoError(t, err)
			returned, err := account.DecryptGuardianShare(response, requestKey)
			require.NoError(t, err)
			knowledge, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, e2eKnowledgeAnswers())
			require.NoError(t, err)
			for _, companion := range []account.RecoveryShare{knowledge, pkg.UserShare} {
				_, secret, err := account.RecoverAccount(pkg.Envelope, companion, returned)
				if err == nil {
					defer clearBytes(secret)
				}
				require.NoError(t, err)
				require.Equal(t, u.secret, secret)
			}
		})
	}
}
