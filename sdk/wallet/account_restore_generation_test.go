package wallet

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// A cold restore starts a new local generation namespace. Zero is not a valid
// origin generation for any subsequent account-managed acknowledgement.
func TestAccountColdRestoreInitializesLocalGeneration(t *testing.T) {
	old := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = old })
	source, remote, _, _ := accountTTLNoopFixture(t)
	source.accountBackgroundWG.Wait()
	target, _, _, _ := newAccountGroup(t, remote)
	locator, location := importGroupMaterial(t, source)
	_, err := target.RestoreAccountManagementState(managedImportRecoveryValue(t, source),
		source.accountSecret, "password", locator,
		AccountManagementRestoreOptions{Location: location, StorageMode: source.accountProfile.StorageMode, RecordTTL: source.accountProfile.RecordTTL})
	require.NoError(t, err)
	target.accountBackgroundWG.Wait()
	values, err := target.listAccountLocal(locator.AccountID)
	require.NoError(t, err)
	require.NotEmpty(t, values)
	for _, value := range values {
		require.Positive(t, value.Generation, "cold restore must establish a local origin generation for every module")
	}
	require.NoError(t, target.SyncAccountManagementState(context.Background()))
	store, err := target.accountDKVSStore()
	require.NoError(t, err)
	entries, err := newDKVSReplicaStore(target.db).LoadOutbox(store.client.replicaNamespace)
	require.NoError(t, err)
	require.Empty(t, entries, "an acknowledged request must not remain permanently unfinalizable")
	require.False(t, target.GetAccountManagementStatus().ManagedDataDirty)
	values, err = target.listAccountLocal(locator.AccountID)
	require.NoError(t, err)
	for _, value := range values {
		require.Equal(t, value.Generation, value.Confirmed)
		require.NotNil(t, value.Baseline)
		require.NotZero(t, value.Baseline.Seq)
	}
}
