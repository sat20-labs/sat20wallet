package e2e

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	core "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Keep the real database and its atomic transactions. Only the selected local
// read fails, as an IndexedDB read transaction or native database scan can.
type accountStatusReadDB struct {
	*accountE2EProfileWriteDB
	mode atomic.Int32
	hits atomic.Int32
}

func (d *accountStatusReadDB) BatchRead(prefix []byte, reverse bool, visit func([]byte, []byte) error) error {
	mode := d.mode.Load()
	if mode == 1 && bytes.HasPrefix(prefix, []byte(wallet.GetDBKeyPrefix()+"account/")) ||
		mode == 2 && bytes.HasPrefix(prefix, core.OutboxPrefix()) {
		d.hits.Add(1)
		return errors.New("controlled account status database read failure")
	}
	return d.KVDB.BatchRead(prefix, reverse, visit)
}

func TestSDKBackupStatusReadFailure(t *testing.T) {
	network := newDKVSNoPluginUnfundedNetwork(t)
	waitForDKVSPeerReady(t, network)
	f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 216), true)
	f.activate(t)
	config, _ := accountReviewConfig(t, network)
	database := indexerdb.NewKVDB(t.TempDir())
	require.NotNil(t, database)
	fault := &accountStatusReadDB{accountE2EProfileWriteDB: &accountE2EProfileWriteDB{KVDB: database}}
	manager := wallet.NewManager(config, fault)
	require.NotNil(t, manager)
	t.Cleanup(func() { manager.Close(); database.Close() })
	recovered, err := manager.LoadAccountManagementStateForRecovery(f.location, f.pkg.Envelope.Locator,
		f.secret, f.rootMnemonic)
	require.NoError(t, err)
	_, err = manager.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword,
		f.pkg.Envelope.Locator, f.restoreOptions())
	require.NoError(t, err)
	accountFailureReady(t, manager)
	hold := newAccountStateHold(false)
	t.Cleanup(hold.unblock)
	transport := &accountBackupHTTP{inner: wallet.NewHTTPClient(), holds: []*accountStateHold{hold}}
	manager.SetDKVSHttpClient(transport)
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	state := manager.GetAccountManagementStatus()
	key, err := dkvs.AccountPersonalKey(state.AccountID, "account/state")
	require.NoError(t, err)
	remote := waitForDKVSRecord(t, network.Core, key)
	transport.armed.Store(true)
	require.NoError(t, manager.UpdateWalletName(state.RootWalletID, "Locally saved pending rename"))
	select {
	case <-hold.entered:
	case <-time.After(20 * time.Second):
		t.Fatal("the ordinary rename did not reach its delayed upload")
	}
	before := manager.GetAccountManagementStatus()
	require.True(t, before.ManagedDataDirty)
	require.Positive(t, before.PendingChanges)
	require.NotEqual(t, "backed_up", before.BackupState)
	for _, scenario := range []struct {
		name string
		mode int32
	}{{"account_rows", 1}, {"outbox", 2}} {
		t.Run(scenario.name, func(t *testing.T) {
			fault.hits.Store(0)
			fault.mode.Store(scenario.mode)
			defer fault.mode.Store(0)
			status := manager.GetAccountManagementStatus()
			require.Positive(t, fault.hits.Load(), "the real status call must encounter the storage fault")
			assert.Equal(t, "error", status.AccountState)
			assert.False(t, status.WriteAllowed)
			assert.NotEqual(t, "backed_up", status.BackupState,
				"a failed local read cannot establish successful backup")
			assert.True(t, status.ManagedDataDirty || status.PendingChanges > 0,
				"the PWA pending-backup warning must survive a failed status read")
			t.Logf("status after read failure: account=%s backup=%s dirty=%t pending=%d read_faults=%d",
				status.AccountState, status.BackupState, status.ManagedDataDirty, status.PendingChanges, fault.hits.Load())
		})
	}
	// Neither the probe nor its read errors may publish or undo the local edit.
	after := manager.GetAccountManagementStatus()
	require.True(t, after.ManagedDataDirty)
	require.Positive(t, after.PendingChanges)
	current := waitForDKVSRecord(t, network.Core, key)
	require.Equal(t, dkvs.RecordHash(remote), dkvs.RecordHash(current))
}
