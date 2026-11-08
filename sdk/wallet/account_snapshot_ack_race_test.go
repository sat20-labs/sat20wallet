package wallet

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Empty provider: gates snapshot export without introducing backup data.
type accountSnapshotExportGate struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *accountSnapshotExportGate) ID() string { return "zz-snapshot-gate" }
func (g *accountSnapshotExportGate) Export(AccountManagedDataCatalog) ([]AccountManagedDataPayload, error) {
	g.once.Do(func() {
		close(g.entered)
		<-g.release
	})
	return nil, nil
}
func (g *accountSnapshotExportGate) Validate(AccountManagedDataCatalog, []AccountManagedDataPayload) error {
	return nil
}
func (g *accountSnapshotExportGate) Import(AccountManagedDataCatalog, []AccountManagedDataPayload) error {
	return nil
}

func TestAccountOwnACKDuringSnapshotCaptureDoesNotConflict(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, remote, store, keys := accountTTLNoopFixture(t)
	// No TTL change and no third-party write: only a legal, online name edit.
	require.NoError(t, manager.UpdateWalletName(manager.GetCurrentWalletId(), "Own confirmed edit"))
	manager.SetDKVSHttpClient(&reviewAcceptanceHTTP{
		rgb11MemoryDKVSHTTP: remote,
		post: func(ctx context.Context, path string, body []byte) ([]byte, error) {
			raw, err := remote.SendDKVSPostContext(ctx, path, body)
			if path == "/v3/dkvs/records/batch-cas" && err == nil {
				return nil, &HTTPResponseError{StatusCode: 503}
			}
			return raw, err
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	require.Error(t, manager.SyncAccountManagementState(ctx))
	cancel()
	entries, err := newDKVSReplicaStore(manager.db).LoadOutbox(store.client.replicaNamespace)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	ack := entries[0]
	require.Equal(t, accountKVOutboxDomain, ack.OriginDomain)
	require.NotEmpty(t, ack.OriginKey)
	require.NotZero(t, ack.OriginGeneration)
	remoteHashes := make(map[string]string)
	for _, key := range keys {
		value, err := store.GetAuthoritative(key)
		require.NoError(t, err)
		remoteHashes[key] = value.Hash
	}

	reopened := newAccountManagementAutoTestManager(t)
	reopened.db = manager.db
	configureRGB11DKVSTestManager(reopened, remote)
	reopened.serverNode = manager.serverNode
	require.NoError(t, reopened.initDB())
	_, err = reopened.UnlockWallet("password")
	require.NoError(t, err)
	gate := &accountSnapshotExportGate{entered: make(chan struct{}), release: make(chan struct{})}
	require.NoError(t, reopened.RegisterAccountManagedDataProvider(gate))
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate.release) }) }
	defer release()
	accountID := reopened.GetAccountManagementStatus().AccountID
	module, err := reopened.readAccountLocal(accountID, "wallets", AccountManagedDataGlobalScope)
	require.NoError(t, err)
	require.NotNil(t, module)
	oldGeneration := module.Generation
	require.Equal(t, ack.OriginGeneration, oldGeneration)
	require.Less(t, module.Confirmed, oldGeneration, "the lost ACK must leave this real module generation pending")

	firstDone := make(chan error, 1)
	go func() { firstDone <- reopened.SyncAccountManagementState(context.Background()) }()
	select {
	case <-gate.entered:
	case err := <-firstDone:
		t.Fatalf("sync exited before captured snapshot export: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("snapshot export gate not reached")
	}
	// The foreground has captured the current wallets module. Now allow only
	// original durable request reconciliation to advance its confirmed baseline.
	reopened.ensureDKVSManager().start()
	defer func() {
		release()
		reopened.dkvs.stopAndWait()
	}()
	require.Eventually(t, func() bool {
		entries, loadErr := newDKVSReplicaStore(reopened.db).LoadOutbox(store.client.replicaNamespace)
		if loadErr != nil || len(entries) != 0 {
			return false
		}
		current, readErr := reopened.readAccountLocal(accountID, "wallets", AccountManagedDataGlobalScope)
		return readErr == nil && current != nil && current.Generation == oldGeneration &&
			current.Confirmed == ack.OriginGeneration && current.Baseline != nil &&
			current.Baseline.Key == ack.OriginKey
	}, 10*time.Second, 20*time.Millisecond)
	confirmed, err := reopened.readAccountLocal(accountID, "wallets", AccountManagedDataGlobalScope)
	require.NoError(t, err)
	require.Equal(t, confirmed.Generation, confirmed.Confirmed)
	require.NotNil(t, confirmed.Baseline)
	reopened.mutex.RLock()
	recordTTL := reopened.accountProfile.RecordTTL
	reopened.mutex.RUnlock()
	t.Logf("own ACK confirmed module generation=%d baseline_seq=%d, account TTL=%d",
		confirmed.Confirmed, confirmed.Baseline.Seq, recordTTL)
	release()
	var firstErr error
	select {
	case firstErr = <-firstDone:
	case <-time.After(10 * time.Second):
		t.Fatal("sync did not finish after snapshot release")
	}
	// Even on the old implementation, a fresh snapshot succeeds. Preserve
	// this fact while retaining the failing first-call assertion below.
	require.NoError(t, reopened.SyncAccountManagementState(context.Background()))
	for _, key := range keys {
		value, err := store.GetAuthoritative(key)
		require.NoError(t, err)
		require.Equal(t, remoteHashes[key], value.Hash, "own ACK recovery must not republish data")
	}
	require.Equal(t, "Own confirmed edit", reopened.GetWalletCatalog()[0].Name)
	require.NoError(t, firstErr, "own ACK must not be classified as a competing remote edit")
}
