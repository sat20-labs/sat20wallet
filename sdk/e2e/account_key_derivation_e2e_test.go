package e2e

import (
	"bytes"
	"context"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/stretchr/testify/require"
)

// Compare independently initialized devices before activation, then recover
// the published account into another empty database with a new local password.
// All secret observations come from the public recovery-package operations.
func TestSDKAccountBackupKeyRecreatedFromRoot(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	mnemonic := accountSyncMnemonic(t, 219)
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
	require.NoError(t, fresh.StartDKVSSync())
	const newPassword = "key-policy-new-device-password"
	requireAccountPWARootRecovery(t, fresh, mnemonic, newPassword)
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
