package wallet

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountColdSyncGrantsWrite(t *testing.T) {
	manager, remote, service, closeManager := newAccountGroup(t)
	id, _, err := manager.CreateWallet("password")
	require.NoError(t, err)
	discoverInactiveAccount(t, manager)
	activateSyncFixture(t, manager, remote, "password")
	require.NoError(t, manager.StartDKVSSync())
	warmCtx, warmCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer warmCancel()
	require.NoError(t, manager.SyncAccountManagementState(warmCtx))
	require.NoError(t, manager.WaitAccountManagedDataReady(warmCtx))
	warmCancel()
	before := manager.GetAccountManagementStatus()
	require.True(t, before.RecoveryConfigured && before.DKVSWriteAllowed)
	require.False(t, before.ManagedDataDirty)
	require.Zero(t, before.PendingChanges)
	cfg := manager.cfg
	closeManager() // Manager.Close, database.Close, then a fresh production open.
	cold, _ := openAccountGroup(t, cfg, remote, service)
	_, err = cold.UnlockWallet("password")
	require.NoError(t, err)
	client, err := cold.GetDKVSClient()
	require.NoError(t, err)
	client.endpointMu.RLock()
	unknown := client.endpointID == ""
	client.endpointMu.RUnlock()
	require.True(t, unknown, "cold transport unexpectedly learned its endpoint before the public sync")
	require.False(t, cold.GetAccountManagementStatus().DKVSWriteAllowed)

	// Exactly one public sync on the reopened DB must initialize qualification.
	// No copied profile, ready flags, second sync, or hidden worker start.
	coldCtx, coldCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer coldCancel()
	require.NoError(t, cold.SyncAccountManagementState(coldCtx))
	after := cold.GetAccountManagementStatus()
	require.True(t, after.DKVSWriteAllowed, "successful first cold sync did not publish write qualification")
	require.True(t, after.AccountID == before.AccountID, "cold sync changed account identity")
	require.True(t, after.RootFingerprint == before.RootFingerprint, "cold sync changed the root identity")
	require.True(t, after.ManagedDataRevision == before.ManagedDataRevision, "clean cold sync republished account data")
	require.False(t, after.ManagedDataDirty)
	require.Zero(t, after.PendingChanges)
	entries, err := newDKVSReplicaStore(cold.db).LoadOutbox(client.replicaNamespace)
	require.NoError(t, err)
	require.Empty(t, entries)
	require.NoError(t, cold.UpdateWalletName(id, "Updated after one cold sync"))
}
