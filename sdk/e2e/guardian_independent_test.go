//go:build guardian

package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestSDKAccountPWAIndependentGuardian(t *testing.T) {
	network := newDKVSNoPluginUnfundedNetwork(t)
	waitForDKVSPeerReady(t, network)
	f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 204), true)
	guardian, guardianLocation := accountReviewDevice(t, network, accountSyncMnemonic(t, 205))
	require.NoError(t, guardian.InitializeAccountManagement(accountReviewPassword))
	require.NoError(t, guardian.BindAccountToCurrentCoreNode())
	identity, err := guardian.GetOrCreateAccountGuardianIdentity(accountReviewPassword)
	require.NoError(t, err)
	require.NotEqual(t, f.manager.GetAccountManagementStatus().AccountID, identity.MailboxID)
	publicKey, err := base64.RawURLEncoding.DecodeString(identity.PublicKey)
	require.NoError(t, err)
	backup, err := f.manager.ExportAccountBackupForPWA(accountReviewPassword, nil)
	require.NoError(t, err)
	pkg, err := f.manager.CreateAccountRecoveryPackage(account.CreateOptions{
		AccountID: f.manager.GetAccountManagementStatus().AccountID, Backup: backup,
		RecoveryMode: account.RecoveryMode2Of3, Questions: e2eKnowledgeQuestions(),
		GuardianMailboxID: identity.MailboxID, GuardianPublicKey: publicKey})
	require.NoError(t, err)
	repo, err := f.manager.NewAccountRepositoryForStorage(*f.authorization)
	require.NoError(t, err)
	require.NoError(t, account.NewManager(repo).Publish(context.Background(), *pkg))
	auth, err := guardian.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
	require.NoError(t, err)
	require.NoError(t, guardian.UseAccountStorageAuthorization(wallet.AccountStoragePurposeGuardian,
		func(value *wallet.AccountStorageAuthorization) error {
			return guardian.PutGuardianCapsuleForStorage(*value, identity.MailboxID, *pkg.GuardianCapsule)
		}))
	require.Equal(t, guardianLocation, auth.Location)
	_, err = guardian.GetOrCreateAccountGuardianIdentity("wrong-password")
	require.Error(t, err)
	stable, err := guardian.GetOrCreateAccountGuardianIdentity(accountReviewPassword)
	require.NoError(t, err)
	require.Equal(t, identity, stable)
	encoded, err := guardian.LoadAccountGuardianCapsule(guardianLocation, identity.MailboxID, pkg.Envelope.Locator.PackageID, pkg.Manifest.Guardian.ShareID)
	require.NoError(t, err)
	var capsule account.GuardianShareCapsule
	require.NoError(t, json.Unmarshal(encoded, &capsule))
	privateKey, err := guardian.LoadAccountGuardianPrivateKey(accountReviewPassword)
	require.NoError(t, err)
	defer clearBytes(privateKey)
	guardianShare, err := account.DecryptGuardianShare(capsule, privateKey)
	require.NoError(t, err)
	knowledge, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, e2eKnowledgeAnswers())
	require.NoError(t, err)
	_, secret, err := account.RecoverAccount(pkg.Envelope, knowledge, guardianShare)
	require.NoError(t, err)
	defer clearBytes(secret)
	require.Equal(t, f.secret, secret)
	require.NoError(t, f.manager.ActivateAccountManagement(secret, accountReviewPassword, *f.authorization, pkg.Envelope.Locator, ""))
	requireE2EFirstBackup(t, f.manager)
	fresh, _ := accountReviewDevice(t, network, "")
	state, err := fresh.LoadAccountManagementStateForRecovery(f.location, pkg.Envelope.Locator, secret, f.rootMnemonic)
	require.NoError(t, err)
	_, err = fresh.RestoreAccountManagementState(*state, secret, accountReviewPassword, pkg.Envelope.Locator, f.restoreOptions())
	require.NoError(t, err)
	require.Equal(t, f.manager.GetWalletCatalog()[0].Accounts, fresh.GetWalletCatalog()[0].Accounts)
	key, err := dkvs.MailShareKey(identity.MailboxID, pkg.Envelope.Locator.PackageID, pkg.Manifest.Guardian.ShareID)
	require.NoError(t, err)
	requireDKVSAbsent(t, network.Bootstrap, key)
}
