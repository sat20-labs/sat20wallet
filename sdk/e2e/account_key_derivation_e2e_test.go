package e2e

import (
	"bytes"
	"context"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

// Compare independently initialized devices before activation, then recover
// the published account into another empty database with a new local password.
// All secret observations come from the public recovery-package operations.
func TestSDKAccountBackupKeyRecreatedFromRoot(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	runSDKAccountBackupKeyRecreatedFromRoot(t, network, accountSyncMnemonic(t, 219))
}

func runSDKAccountBackupKeyRecreatedFromRoot(t *testing.T, network *realSatoshiNet, mnemonic string) {
	t.Helper()
	t.Run("PrivateKeyCannotActivate", func(t *testing.T) {
		manager, _ := accountReviewDevice(t, network, "")
		_, err := manager.ImportWalletWithPrivateKey("0000000000000000000000000000000000000000000000000000000000000001", accountReviewPassword)
		require.NoError(t, err)
		before := manager.GetWalletCatalog()
		require.Len(t, before, 1)
		require.ErrorIs(t, manager.InitializeAccountManagement(accountReviewPassword), wallet.ErrAccountRootRequired)
		require.ErrorIs(t, manager.ActivateAccountManagement(make([]byte, 32), accountReviewPassword,
			wallet.AccountStorageAuthorization{Mode: wallet.AccountStorageTemporary}, account.Locator{}, ""), wallet.ErrAccountRootRequired)
		view, err := manager.GetAccountView(context.Background())
		require.NoError(t, err)
		require.False(t, view.Active)
		require.Empty(t, view.AccountID)
		require.Empty(t, view.Locator)
		require.Equal(t, before, manager.GetWalletCatalog())
	})
	first := prepareAccountReviewWithMnemonic(t, network, mnemonic, true)
	second := prepareAccountReviewWithMnemonic(t, network, mnemonic, true)
	require.Equal(t, first.pkg.Envelope.Locator.AccountID, second.pkg.Envelope.Locator.AccountID)
	require.True(t, bytes.Equal(first.secret, second.secret), "same user root must recreate the same backup secret")
	require.NotEqual(t, first.pkg.Envelope.Locator.PackageID, second.pkg.Envelope.Locator.PackageID)
	second.manager.Close()
	first.activate(t)
	first.manager.Close()
	fresh, _ := accountReviewDevice(t, network, "")
	require.Empty(t, fresh.GetWalletCatalog())
	const newPassword = "key-policy-new-device-password"
	requireAccountPWARootRecovery(t, fresh, mnemonic, newPassword)
	// This device restores the already-activated account before its worker starts.
	require.NoError(t, fresh.StartDKVSSync())
	backup, err := fresh.ExportAccountBackupForPWA(newPassword, nil)
	require.NoError(t, err)
	pkg, err := fresh.CreateAccountRecoveryPackage(account.CreateOptions{
		AccountID: fresh.GetAccountManagementStatus().AccountID, Backup: backup,
		RecoveryMode: account.RecoveryMode2Of2, Questions: e2eKnowledgeQuestions(),
	})
	require.NoError(t, err)
	dkvsShare, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, e2eKnowledgeAnswers())
	require.NoError(t, err)
	_, secret, err := account.RecoverAccount(pkg.Envelope, pkg.UserShare, dkvsShare)
	require.NoError(t, err)
	defer clearBytes(secret)
	require.True(t, bytes.Equal(first.secret, secret), "new password and recovery material changes must retain the account backup key")
	require.NoError(t, fresh.SyncAccountManagementState(context.Background()))
	_, err = fresh.ExportAccountBackupForPWA(accountReviewPassword, nil)
	require.Error(t, err, "old device password must not unlock the restored account")
}
