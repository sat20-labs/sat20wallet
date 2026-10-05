package wallet

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	core "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDKVSReviewFailedRefreshKeepsConfirmedOfflineReplicaReadable(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	configureRGB11DKVSTestManager(manager, http)
	client, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	key := accountTestKey(t, manager, "fifth-offline/value")
	prefix, err := dkvs.CollectionPathForKey(key)
	require.NoError(t, err)
	confirmed, err := finalDKVSSeedClient(manager, remote).PutRecord(freeLocalRecord(t, manager, key, 1, "confirmed"))
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	_, err = client.GetRecord(key)
	require.NoError(t, err)
	_, total, err := client.ListRecords(prefix, 0, 0)
	require.NoError(t, err)
	require.Equal(t, 1, total)

	http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if path == "/v3/dkvs/active/sync" {
			return nil, &net.DNSError{Err: "offline", IsTimeout: true}
		}
		return remote.SendDKVSPostContext(ctx, path, body)
	}
	_, _, receiveErr := manager.syncDKVSOnceResult()
	require.Error(t, receiveErr, "the production refresh must actually encounter the offline transport")
	var networkErr net.Error
	require.ErrorAs(t, receiveErr, &networkErr)
	// The complete confirmed replica remains intact; only the refresh failed.
	local, err := core.NewReplicaStore(manager.db).LoadSubscriptionRecord(client.replicaNamespace, key)
	require.NoError(t, err)
	require.Equal(t, dkvs.RecordHash(confirmed), dkvs.RecordHash(local))
	state, err := manager.GetDKVSSubscriptionStatus()
	require.NoError(t, err)
	t.Logf("failed refresh status=%s receive_error=%v", state.Status, receiveErr)

	read, readErr := client.GetRecord(key)
	if assert.NoError(t, readErr, "a failed refresh must preserve offline reads of confirmed data") {
		assert.Equal(t, dkvs.RecordHash(confirmed), dkvs.RecordHash(read))
	}
	records, total, listErr := client.ListRecords(prefix, 0, 0)
	if assert.NoError(t, listErr, "a failed refresh must preserve offline listing of confirmed data") {
		assert.Equal(t, 1, total)
		assert.Len(t, records, 1)
	}
	state, err = manager.GetDKVSSubscriptionStatus()
	require.NoError(t, err)
	assert.Equal(t, DKVSSubscriptionOfflineReady, state.Status)
	http.post = nil
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	state, err = manager.GetDKVSSubscriptionStatus()
	require.NoError(t, err)
	assert.Equal(t, DKVSSubscriptionReady, state.Status, "successful refresh must restore online readiness")
}

func TestDKVSReviewIncompleteReplicaCannotBecomeOfflineReady(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "first-sync", true: "added-prefix"}[existing], func(t *testing.T) {
			manager, remote := finalDKVSTestManager(t)
			http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
			configureRGB11DKVSTestManager(manager, http)
			client, err := manager.ensureDKVSManager().primaryClient()
			require.NoError(t, err)
			oldKey := accountTestKey(t, manager, "fifth-incomplete-old/value")
			oldPrefix, err := dkvs.CollectionPathForKey(oldKey)
			require.NoError(t, err)
			if existing {
				_, err = finalDKVSSeedClient(manager, remote).PutRecord(freeLocalRecord(t, manager, oldKey, 1, "confirmed"))
				require.NoError(t, err)
				require.NoError(t, manager.SubscribeDKVSPrefix(oldPrefix))
				require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{oldPrefix}))
			}
			newKey := accountTestKey(t, manager, "fifth-incomplete-new/value")
			prefix, err := dkvs.CollectionPathForKey(newKey)
			require.NoError(t, err)
			require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
			http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
				if path == "/v3/dkvs/active/sync" {
					return nil, &net.DNSError{Err: "offline", IsTimeout: true}
				}
				return remote.SendDKVSPostContext(ctx, path, body)
			}
			_, _, err = manager.syncDKVSOnceResult()
			require.Error(t, err)
			state, err := manager.GetDKVSSubscriptionStatus()
			require.NoError(t, err)
			assert.Equal(t, DKVSSubscriptionSyncing, state.Status)
			err = core.NewReplicaStore(manager.db).MarkOfflineReady(client.replicaNamespace)
			assert.ErrorIs(t, err, core.ErrReplicaNotReady, "offline readiness cannot be granted before a complete sync")
			_, err = client.GetRecord(newKey)
			assert.Error(t, err, "an incomplete directory must not return a confirmed not-found answer")
			assert.NotErrorIs(t, err, dkvs.ErrRecordNotFound)
			_, _, err = client.ListRecords(prefix, 0, 0)
			assert.Error(t, err, "an incomplete directory must not return an empty offline listing")
		})
	}
}

func TestDKVSReviewFullSyncHeightAndAuthoritativeReorg(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	client, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	key := accountTestKey(t, manager, "fifth-full-height/value")
	prefix, err := dkvs.CollectionPathForKey(key)
	require.NoError(t, err)
	_, err = finalDKVSSeedClient(manager, remote).PutRecord(freeLocalRecord(t, manager, key, 1, "confirmed"))
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	manager.dkvs.setEndpointVerificationHeight(5, true)
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	height, known := manager.dkvs.endpointVerificationHeight()
	assert.True(t, known)
	assert.Equal(t, uint64(5), height, "full synchronization must not lower a known endpoint expiry height")
	remote.mu.Lock()
	remote.bestHeight = 2
	remote.mu.Unlock()
	height, err = manager.dkvs.refreshVerificationBestHeight(client)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), height, "an authoritative best-height refresh must still represent a real chain rollback")
	height, known = manager.dkvs.endpointVerificationHeight()
	assert.True(t, known)
	assert.Equal(t, uint64(2), height)
}

func TestDKVSReviewCompletedWatchCannotReviveExpiredSiblingScope(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	configureRGB11DKVSTestManager(manager, http)
	client, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	keyA := accountTestKey(t, manager, "fifth-height-a/value")
	keyB := accountTestKey(t, manager, "fifth-height-b/value")
	prefixA, err := dkvs.CollectionPathForKey(keyA)
	require.NoError(t, err)
	prefixB, err := dkvs.CollectionPathForKey(keyB)
	require.NoError(t, err)
	seed := finalDKVSSeedClient(manager, remote)
	_, err = seed.PutRecord(freeLocalRecord(t, manager, keyA, 1, "a1"))
	require.NoError(t, err)
	lease, err := newDKVSAccountSignedRecordWithFreeLocal(manager.wallet, keyB, []byte("lease"),
		dkvs.RecordOptions{Seq: 1, IssueHeight: 1, TTL: 2})
	require.NoError(t, err)
	_, err = seed.PutRecord(lease)
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefixA))
	require.NoError(t, manager.SubscribeDKVSPrefix(prefixB))
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefixA, prefixB}))
	_, err = client.GetRecord(keyB)
	require.NoError(t, err)
	updated, err := seed.PutRecord(freeLocalRecord(t, manager, keyA, 2, "a2"))
	require.NoError(t, err)

	captured, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		payload, err := remote.SendDKVSPostContext(ctx, path, body)
		if err != nil || path != "/v3/dkvs/active/watch" {
			return payload, err
		}
		// Delay delivery of an already captured height-1 update for A. B is
		// unchanged in this Watch response, and remains a separate local scope.
		close(captured)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return payload, nil
	}
	watchDone := make(chan error, 1)
	go func() {
		watchDone <- manager.dkvs.watchManagedActive(make(chan struct{}), make(chan struct{}), time.Time{})
	}()
	select {
	case <-captured:
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not capture the remote A update")
	}
	remote.mu.Lock()
	remote.bestHeight = 3
	remote.mu.Unlock()
	height, err := manager.dkvs.refreshVerificationBestHeight(client)
	require.NoError(t, err)
	require.Equal(t, uint64(3), height)
	_, err = client.GetRecord(keyB)
	require.ErrorIs(t, err, dkvs.ErrExpiredRecord, "control: B expires at the newly confirmed endpoint height")
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-watchDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not finish installing the A update")
	}
	local, err := core.NewReplicaStore(manager.db).LoadSubscriptionRecord(client.replicaNamespace, keyA)
	require.NoError(t, err)
	require.Equal(t, dkvs.RecordHash(updated), dkvs.RecordHash(local))
	height, known := manager.dkvs.endpointVerificationHeight()
	t.Logf("verification height after delayed A Watch=%d known=%v", height, known)
	assert.GreaterOrEqual(t, height, uint64(3), "a delayed collection response must not roll back endpoint expiry verification")
	_, err = client.GetRecord(keyB)
	assert.ErrorIs(t, err, dkvs.ErrExpiredRecord, "an expired record in another scope must stay expired")
}
