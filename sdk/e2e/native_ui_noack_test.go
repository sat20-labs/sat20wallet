package e2e

import (
	"context"
	"testing"
	"time"

	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ExplicitRetry preserves account-management-usage-e2e.mjs:304-323: the
// PWA Retry now action calls RefreshAccount, then an entirely new local rename
// must succeed and reach backup settlement. Page/dialog behavior stays PWA.
func runNativeUINoACK(t *testing.T, cfg *sdkcommon.Config, explicit bool) {
	t.Helper()
	ctx := context.Background()
	seed := byte(245)
	freshName := "Online rename after automatic reconnect"
	if explicit {
		seed = 246
		freshName = "Online rename after retry"
	}
	owner := newNativeUIInactive(t, cfg, accountSyncMnemonic(t, seed))
	created := nativeUILocalBackup(t, owner)
	baseline := nativeUINoACKSettled(t, owner)
	require.Positive(t, baseline.StateSeq)
	before := owner.mgr.GetWalletCatalog()
	require.Len(t, before, 1)
	root := before[0].ID
	require.NotEqual(t, freshName, before[0].Name)
	refused := owner.http.refused.Load()
	owner.http.dkvsOffline.Store(true)

	// Like the PWA, explicitly observe the failed receive while disconnected.
	// No local edit or pending upload exists that could supply a repairing ACK.
	_, err := owner.mgr.RefreshAccount(ctx)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		status := owner.mgr.GetAccountManagementStatus()
		return owner.http.refused.Load() > refused && status.LastDKVSSyncErrorCode != "" &&
			status.LastDKVSSyncErrorAt != 0
	}, 90*time.Second, 100*time.Millisecond, "the SDK must publish the actual disconnected receive failure")
	view, err := owner.mgr.GetAccountView(ctx)
	require.NoError(t, err)
	// Do not pre-wait for CanWrite=false: a stale writable view is a failure.
	require.ErrorIs(t, owner.mgr.UpdateWalletName(root, "Offline rename must be rejected"), wallet.ErrDKVSPathNotSynced)
	require.Equal(t, before, owner.mgr.GetWalletCatalog())
	require.NotEmpty(t, view.Modules)
	for _, module := range view.Modules {
		require.False(t, module.CanWrite, "offline module %s must not advertise write qualification", module.Name)
	}
	failed := owner.mgr.GetAccountManagementStatus()
	require.False(t, failed.ManagedDataDirty, "rejected rename must not create a pending local change")
	require.Zero(t, failed.PendingChanges)
	require.Equal(t, baseline.StateSeq, failed.StateSeq)
	entries, err := dkvscore.NewReplicaStore(owner.db).LoadOutbox(cfg.Env + ":" + cfg.Chain)
	require.NoError(t, err)
	require.Zero(t, len(entries), "this recovery must not be driven by an old upload ACK")

	owner.http.dkvsOffline.Store(false)
	if explicit {
		// This is the same public entry as the PWA's Retry now button, once.
		_, err = owner.mgr.RefreshAccount(ctx)
		require.NoError(t, err)
	}
	// AutomaticReconnect is a contract test, not a claim of existing success.
	// dkvsManager.run already retries network failures after five seconds. It
	// must restore fresh-write qualification with no RefreshAccount, Retry,
	// Sync, WaitAccountBackup, restart, or remote edit helping this observation.
	// If that coordination is incomplete, the bounded read-only wait fails.
	reconnected := nativeUINoACKSettled(t, owner)
	require.Equal(t, baseline.StateSeq, reconnected.StateSeq)
	require.Equal(t, before, owner.mgr.GetWalletCatalog())
	require.NoError(t, owner.mgr.UpdateWalletName(root, freshName),
		"recovered transport alone is insufficient: a new local rename must be authorized")
	expected := append([]wallet.WalletCatalogEntry(nil), before...)
	expected[0].Name = freshName
	require.Equal(t, expected, owner.mgr.GetWalletCatalog())
	settled := nativeUINoACKSettled(t, owner)
	require.Greater(t, settled.StateSeq, baseline.StateSeq, "the fresh rename needs its own confirmed backup")

	// Only after the new edit settled, test its independent persistence and
	// remote recoverability. Offline reopening cannot fetch a replacement name.
	owner.http.dkvsOffline.Store(true)
	owner.reopen(t)
	require.Equal(t, expected, owner.mgr.GetWalletCatalog())
	owner.close()
	restored := newNativeUIDevice(t, cfg, "")
	result := nativeUIRestore(t, restored, created)
	require.True(t, result.Complete)
	require.Empty(t, result.FailedModules)
	require.Equal(t, baseline.AccountID, result.AccountID)
	nativeUICatalog(t, restored.mgr, expected)
	restored.http.dkvsOffline.Store(true)
	restored.reopen(t)
	nativeUICatalog(t, restored.mgr, expected)
}

// Observation only. For this activated temporary account, GetAccountView reads
// local status/catalog/storage state and has no paid funding intent to query.
// In particular, WaitAccountBackup is deliberately absent: it requests sync.
func nativeUINoACKSettled(t *testing.T, d *nativeUIDevice) wallet.AccountManagementStatus {
	t.Helper()
	var status wallet.AccountManagementStatus
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		view, err := d.mgr.GetAccountView(context.Background())
		if !assert.NoError(c, err) {
			return
		}
		assert.Equal(c, "ready", view.State)
		assert.Empty(c, view.ErrorCode)
		assert.Equal(c, "backed_up", view.BackupState)
		assert.Empty(c, view.BackupRisk)
		assert.NotEmpty(c, view.Modules)
		for _, module := range view.Modules {
			assert.True(c, module.CanWrite, "module %s must allow a fresh local write", module.Name)
		}
		status = d.mgr.GetAccountManagementStatus()
		assert.True(c, status.Active && status.RecoveryConfigured && status.DKVSWriteAllowed)
		assert.False(c, status.ActivationPending || status.ManagedDataDirty)
		assert.Zero(c, status.PendingChanges)
		entries, err := dkvscore.NewReplicaStore(d.db).LoadOutbox(d.cfg.Env + ":" + d.cfg.Chain)
		assert.NoError(c, err)
		assert.Zero(c, len(entries), "the real outbox must have no older ACK awaiting cleanup")
	}, 90*time.Second, 100*time.Millisecond, "read-only observation of writable, fully backed-up account state")
	return status
}
