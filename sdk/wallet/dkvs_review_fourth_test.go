package wallet

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	core "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestDKVSReviewCompletedWatchBeforeForegroundAckKeepsReceiving(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	configureRGB11DKVSTestManager(manager, http)
	client, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	key := accountTestKey(t, manager, "fourth-watch/value")
	prefix, err := dkvs.CollectionPathForKey(key)
	require.NoError(t, err)
	seed := finalDKVSSeedClient(manager, remote)
	_, err = seed.PutRecord(freeLocalRecord(t, manager, key, 1, "v1"))
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	watchStarted, pageCaptured := make(chan struct{}), make(chan struct{})
	var watches atomic.Int32
	http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if path != "/v3/dkvs/active/watch" || watches.Add(1) != 1 {
			return remote.SendDKVSPostContext(ctx, path, body)
		}
		close(watchStarted)
		payload, err := remote.SendDKVSPostContext(ctx, path, body)
		if err != nil {
			return nil, err
		}
		close(pageCaptured)
		// The response is already complete when the foreground operation wakes
		// and cancels Watch. Cancellation cannot retract a completed response.
		<-ctx.Done()
		return payload, nil
	}
	manager.dkvs.start()
	defer manager.dkvs.stopAndWait()
	defer func() {
		code, message, _ := manager.dkvs.lastSyncErrorStatus()
		meta, metaErr := core.NewReplicaStore(manager.db).LoadActiveMeta(client.replicaNamespace, dkvs.ActiveScope{Prefix: prefix})
		t.Logf("Watch calls=%d sync_error=%s: %s active_meta=%+v meta_error=%v", watches.Load(), code, message, meta, metaErr)
	}()
	select {
	case <-watchStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start Watch")
	}
	_, err = seed.PutRecord(freeLocalRecord(t, manager, key, 2, "remote-v2"))
	require.NoError(t, err)
	select {
	case <-pageCaptured:
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not complete its old response")
	}
	remote.mu.Lock()
	remote.bestHeight = 2
	remote.mu.Unlock()
	// A successful write in the same collection advances the ACK watermark
	// beyond the completed Watch page; its wake is consumed by cancellation.
	sibling := accountTestKey(t, manager, "fourth-watch/sibling")
	_, err = client.PutSignedRecordFreeLocal(manager.wallet, sibling, []byte("foreground"),
		dkvs.RecordOptions{IssueHeight: 1, TTL: 100})
	require.NoError(t, err)
	updated, err := seed.PutRecord(freeLocalRecord(t, manager, key, 3, "remote-v3"))
	require.NoError(t, err)
	replica := core.NewReplicaStore(manager.db)
	require.Eventually(t, func() bool {
		local, err := replica.LoadSubscriptionRecord(client.replicaNamespace, key)
		height, known := manager.dkvs.endpointVerificationHeight()
		return err == nil && dkvs.RecordHash(local) == dkvs.RecordHash(updated) &&
			watches.Load() >= 2 && known && height == 2
	}, 3*time.Second, 10*time.Millisecond, "a completed stale Watch page must not strand receiving after a foreground ACK")
	code, message, _ := manager.dkvs.lastSyncErrorStatus()
	t.Logf("Watch calls=%d sync_error=%s: %s", watches.Load(), code, message)
}

func TestDKVSReviewWatchRetryRejectsFreshSourceRollback(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	configureRGB11DKVSTestManager(manager, http)
	client, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	key := accountTestKey(t, manager, "fourth-rollback/value")
	prefix, err := dkvs.CollectionPathForKey(key)
	require.NoError(t, err)
	_, err = finalDKVSSeedClient(manager, remote).PutRecord(freeLocalRecord(t, manager, key, 1, "v1"))
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	scope := dkvs.ActiveScope{Prefix: prefix}
	oldPage, err := client.GetActivePage(context.Background(), dkvs.ActiveSyncRequest{Scope: scope, EndpointID: remote.endpointID})
	require.NoError(t, err)
	_, err = client.PutSignedRecordFreeLocal(manager.wallet, accountTestKey(t, manager, "fourth-rollback/sibling"),
		[]byte("ack"), dkvs.RecordOptions{IssueHeight: 1, TTL: 100})
	require.NoError(t, err)
	var freshRequests atomic.Int32
	http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if path == "/v3/dkvs/active/sync" {
			freshRequests.Add(1)
			return json.Marshal(map[string]interface{}{"code": 0, "data": oldPage})
		}
		return remote.SendDKVSPostContext(ctx, path, body)
	}
	_, err = client.syncActiveScope(context.Background(), core.NewReplicaStore(manager.db), client.replicaNamespace, scope, false, oldPage)
	require.ErrorIs(t, err, dkvs.ErrStaleEndpoint, "a fresh source rollback must still be rejected after discarding an old Watch page")
	require.Positive(t, freshRequests.Load(), "the completed page must be retried against the endpoint")
}

func TestDKVSReviewRootWrapperWriteConflictKeepsReceiving(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, published, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	remote := manager.http.(*rgb11MemoryDKVSHTTP)
	http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	configureRGB11DKVSTestManager(manager, http)
	client, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	client.Http = http
	seed := finalDKVSSeedClient(manager, remote)
	for key, value := range published.records {
		_, err = seed.PutRecord(freeLocalRecord(t, manager, key, 1, string(value.Value)))
		require.NoError(t, err)
		prefix, err := dkvs.CollectionPathForKey(key)
		require.NoError(t, err)
		require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	}
	root, err := manager.accountManagementRootWallet()
	require.NoError(t, err)
	wrapperKey, err := accountRootWrapperKey(root)
	require.NoError(t, err)
	wrapperPrefix, err := dkvs.CollectionPathForKey(wrapperKey)
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(wrapperPrefix))
	key := accountTestKey(t, manager, "fourth-job/value")
	prefix, err := dkvs.CollectionPathForKey(key)
	require.NoError(t, err)
	_, err = seed.PutRecord(freeLocalRecord(t, manager, key, 1, "v1"))
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	var rejected atomic.Int32
	http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if path == "/v3/dkvs/records/batch-cas" {
			rejected.Add(1)
			return nil, dkvs.ErrWriteConflict
		}
		return remote.SendDKVSPostContext(ctx, path, body)
	}
	// Run the production root-wrapper task in isolation from unrelated account
	// service jobs, using the real managed store and background worker.
	manager.dkvs.mu.Lock()
	manager.dkvs.jobs = make(map[string]func(*dkvsStore) error)
	manager.dkvs.mu.Unlock()
	var delivered atomic.Bool
	manager.dkvs.addObserver(func(_ []string) {
		local, err := core.NewReplicaStore(manager.db).LoadSubscriptionRecord(client.replicaNamespace, key)
		if err == nil && local.Seq == 2 && string(local.Value) == "remote-v2" {
			delivered.Store(true)
		}
	})
	manager.scheduleAccountRootWrapperSync()
	manager.dkvs.start()
	defer manager.dkvs.stopAndWait()
	defer func() {
		code, message, _ := manager.dkvs.lastSyncErrorStatus()
		t.Logf("root-wrapper rejections=%d sync_error=%s: %s", rejected.Load(), code, message)
	}()
	require.Eventually(t, func() bool {
		code, _, _ := manager.dkvs.lastSyncErrorStatus()
		return rejected.Load() > 0 && code == string(dkvs.ErrorCodeWriteConflict)
	}, 3*time.Second, 10*time.Millisecond, "the actual account job must reach a rejected PUT")
	updated, err := seed.PutRecord(freeLocalRecord(t, manager, key, 2, "remote-v2"))
	require.NoError(t, err)
	replica := core.NewReplicaStore(manager.db)
	require.Eventually(t, func() bool {
		local, err := replica.LoadSubscriptionRecord(client.replicaNamespace, key)
		return err == nil && dkvs.RecordHash(local) == dkvs.RecordHash(updated) && delivered.Load()
	}, 3*time.Second, 10*time.Millisecond, "a rejected account job PUT must not stop receiving and notifying another subscribed collection")
}
