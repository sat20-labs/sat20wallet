package e2e

import (
	"context"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

// Stable fingerprints, not process-local wallet IDs, identify entries across
// devices. All replicas are created by public recovery APIs from real nodes.
func accountReviewFind(t *testing.T, manager *wallet.Manager, fingerprint string) wallet.WalletCatalogEntry {
	t.Helper()
	for _, entry := range manager.GetWalletCatalog() {
		if entry.Fingerprint == fingerprint { return entry }
	}
	t.Fatalf("account-review: missing wallet fingerprint %s", fingerprint)
	return wallet.WalletCatalogEntry{}
}

func TestSDKAccountReviewMultiDevice(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	f := prepareAccountReview(t, network, true)
	secondaryID, err := f.manager.ImportWallet(coreMnemonic, accountReviewPassword)
	require.NoError(t, err)
	require.NoError(t, f.manager.EnsureAccount(secondaryID, 1, "Spending", "did:review:spending"))
	f.activate(t)
	rootFingerprint := f.manager.GetAccountManagementStatus().RootFingerprint
	secondary := f.manager.GetWalletCatalog()[1]
	device, recovered := f.recover(t)
	_, err = device.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword, f.pkg.Envelope.Locator, f.restoreOptions())
	require.NoError(t, err)
	require.Len(t, device.GetWalletCatalog(), 2)

	t.Run("IndependentPendingChangesRebaseWithoutLoss", func(t *testing.T) {
		rootA := accountReviewFind(t, f.manager, rootFingerprint)
		rootB := accountReviewFind(t, device, rootFingerprint)
		require.NoError(t, f.manager.UpdateWalletName(rootA.ID, "Concurrent Root"))
		require.NoError(t, device.EnsureAccount(rootB.ID, 3, "Travel", "did:review:travel"))
		require.NoError(t, device.SyncAccountManagementState(context.Background()))
		require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
		require.NoError(t, device.SyncAccountManagementState(context.Background()))
		for _, manager := range []*wallet.Manager{f.manager, device} {
			root := accountReviewFind(t, manager, rootFingerprint)
			require.Equal(t, "Concurrent Root", root.Name)
			require.Len(t, root.Accounts, 4)
			require.Equal(t, "Travel", root.Accounts[3].Name)
			require.Equal(t, "did:review:travel", root.Accounts[3].DID)
			require.Zero(t, manager.GetAccountManagementStatus().PendingChanges)
			require.Equal(t, f.pkg.Envelope.Locator.AccountID, manager.GetAccountManagementStatus().AccountID)
		}
		require.Equal(t, f.manager.GetAccountManagementStatus().StateSeq, device.GetAccountManagementStatus().StateSeq)
	})

	t.Run("AlternatingReadOnlySyncDoesNotEchoRevisions", func(t *testing.T) {
		before := f.manager.GetAccountManagementStatus().StateSeq
		for i := 0; i < 3; i++ {
			require.NoError(t, device.SyncAccountManagementState(context.Background()))
			require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
		}
		require.Equal(t, before, f.manager.GetAccountManagementStatus().StateSeq)
		require.Equal(t, before, device.GetAccountManagementStatus().StateSeq)
	})

	t.Run("DeletingSelectedWalletRemotelyFallsBackToRootZero", func(t *testing.T) {
		localSecondary := accountReviewFind(t, device, secondary.Fingerprint)
		require.NoError(t, device.SwitchWallet(localSecondary.ID, ""))
		device.SwitchAccount(1)
		require.Equal(t, uint32(1), device.GetCurrentAccountId())
		require.NoError(t, f.manager.DeleteWallet(secondary.ID))
		require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
		require.NoError(t, device.SyncAccountManagementState(context.Background()))
		require.Len(t, device.GetWalletCatalog(), 1)
		root := accountReviewFind(t, device, rootFingerprint)
		require.Equal(t, root.ID, device.GetCurrentWalletId())
		require.Zero(t, device.GetCurrentAccountId())
		require.Equal(t, root.Accounts[0].Address, device.GetWallet().GetAddress())
		require.Zero(t, device.GetAccountManagementStatus().PendingChanges)
		// A newly recovered third device must not resurrect the removed wallet.
		third, newest := f.recover(t)
		_, err := third.RestoreAccountManagementState(*newest, f.secret, accountReviewPassword, f.pkg.Envelope.Locator, f.restoreOptions())
		require.NoError(t, err)
		require.Len(t, third.GetWalletCatalog(), 1)
		require.Equal(t, rootFingerprint, third.GetWalletCatalog()[0].Fingerprint)
	})
}
