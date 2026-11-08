package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

// Stable fingerprints, not process-local wallet IDs, identify entries across
// devices. All replicas are created by public recovery APIs from real nodes.
func accountReviewFind(t *testing.T, manager *wallet.Manager, fingerprint string) wallet.WalletCatalogEntry {
	t.Helper()
	for _, entry := range manager.GetWalletCatalog() {
		if entry.Fingerprint == fingerprint {
			return entry
		}
	}
	t.Fatalf("account-review: missing wallet fingerprint %s", fingerprint)
	return wallet.WalletCatalogEntry{}
}

func TestSDKAccountReviewMultiDevice(t *testing.T) {
	network := newDKVSNoPluginUnfundedNetwork(t)
	waitForDKVSPeerReady(t, network)
	t.Run("IndependentPendingChangesRebaseWithoutLoss", func(t *testing.T) {
		t.Skip("not tested: simultaneous edits from multiple devices are unsupported")
	})

	t.Run("AlternatingReadOnlySyncDoesNotEchoRevisions", func(t *testing.T) {
		f, device, _ := accountReviewDevicePair(t, network)
		before := f.manager.GetAccountManagementStatus().StateSeq
		for i := 0; i < 3; i++ {
			require.NoError(t, device.SyncAccountManagementState(context.Background()))
			require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
		}
		require.Equal(t, before, f.manager.GetAccountManagementStatus().StateSeq)
		require.Equal(t, before, device.GetAccountManagementStatus().StateSeq)
	})

	t.Run("DeletingSelectedWalletRemotelyFallsBackToRootZero", func(t *testing.T) {
		f, device, secondary := accountReviewDevicePair(t, network)
		rootFingerprint := f.manager.GetAccountManagementStatus().RootFingerprint
		localSecondary := accountReviewFind(t, device, secondary.Fingerprint)
		require.NoError(t, device.SwitchWallet(localSecondary.ID, ""))
		require.NoError(t, device.SwitchAccount(1))
		require.Equal(t, uint32(1), device.GetCurrentAccountId())
		require.NoError(t, f.manager.DeleteWallet(secondary.ID))
		require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		require.NoError(t, f.manager.WaitAccountBackup(ctx, "", ""), "confirmed deletion must not wait for physical blob reclamation")
		require.False(t, f.manager.GetAccountManagementStatus().ManagedDataDirty)
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

func accountReviewDevicePair(t *testing.T, network *realSatoshiNet) (*accountReviewFixture, *wallet.Manager, wallet.WalletCatalogEntry) {
	t.Helper()
	f := prepareAccountReviewWithMnemonic(t, network, "", true)
	id, err := f.manager.ImportWallet(coreMnemonic, accountReviewPassword)
	require.NoError(t, err)
	require.NoError(t, f.manager.EnsureAccount(id, 1, "Spending", "did:review:spending"))
	f.activate(t)
	secondary := f.manager.GetWalletCatalog()[1]
	device, recovered := f.recover(t)
	_, err = device.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword, f.pkg.Envelope.Locator, f.restoreOptions())
	require.NoError(t, err)
	require.Len(t, device.GetWalletCatalog(), 2)
	return f, device, secondary
}
