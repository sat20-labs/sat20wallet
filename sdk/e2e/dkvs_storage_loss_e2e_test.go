package e2e

import (
	"context"
	"testing"
	"time"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	core "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// A successful remote write cannot finish its local ACK transaction while
// storage is full. The next replay must report another local write failure,
// preserve the same signed request, and recover after storage is available.
func TestSDKOutboxWriteFailure(t *testing.T) {
	network := newDKVSNoPluginUnfundedNetwork(t)
	waitForDKVSPeerReady(t, network)
	f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 215), true)
	f.activate(t)
	config, _ := accountReviewConfig(t, network)
	database := indexerdb.NewKVDB(t.TempDir())
	require.NotNil(t, database)
	fault := &accountE2EProfileWriteDB{KVDB: database, failFlush: true}
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
	hold := newAccountStateHold(true)
	t.Cleanup(hold.unblock)
	transport := &accountBackupHTTP{inner: wallet.NewHTTPClient(), holds: []*accountStateHold{hold}}
	manager.SetDKVSHttpClient(transport)
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	transport.armed.Store(true)
	require.NoError(t, manager.UpdateWalletName(manager.GetAccountManagementStatus().RootWalletID,
		"Rename before local storage fills"))
	select {
	case <-hold.entered:
	case <-time.After(20 * time.Second):
		t.Fatal("real commit did not reach the held response")
	}
	entries, err := core.NewReplicaStore(database).LoadOutbox("test:testnet")
	require.NoError(t, err)
	var pending *core.BatchOutboxEntry
	for _, entry := range entries {
		if entry.OriginDomain == "account" && entry.State == core.DKVSOutboxInflight {
			pending = entry
			break
		}
	}
	require.NotNil(t, pending)
	mutations, err := pending.DecodeMutations()
	require.NoError(t, err)
	require.Len(t, mutations, 1)
	committed := waitForDKVSRecord(t, network.Core, mutations[0].Record.Key)
	require.Equal(t, dkvs.RecordHash(mutations[0].Record), dkvs.RecordHash(committed))
	// The exact existing request key comes from the actual public operation.
	// Fail transaction commit and later standalone Write, without altering bytes.
	fault.key = string(core.OutboxKey("test:testnet", pending.RequestID))
	fault.fail.Store(true)
	hold.unblock()
	require.Eventually(t, func() bool { return fault.failures.Load() >= 2 }, 15*time.Second,
		50*time.Millisecond, "exercise both ACK persistence and replay-state persistence failures")
	entries, err = core.NewReplicaStore(database).LoadOutbox("test:testnet")
	require.NoError(t, err)
	var retained *core.BatchOutboxEntry
	for _, entry := range entries {
		if entry.RequestID == pending.RequestID {
			retained = entry
		}
	}
	require.NotNil(t, retained, "storage failure must preserve the original signed request")
	require.Equal(t, pending.Authorization, retained.Authorization)
	require.Equal(t, pending.Mutations, retained.Mutations)
	require.False(t, manager.GetAccountManagementStatus().BackupState == "backed_up")
	fault.fail.Store(false)
	require.NoError(t, manager.RetryDKVSSync())
	if !waitBackupRecovery(t, manager) {
		return
	}
	current := waitForDKVSRecord(t, network.Core, mutations[0].Record.Key)
	require.Equal(t, dkvs.RecordHash(committed), dkvs.RecordHash(current),
		"recovering local persistence must not create another remote revision")
}
