package wallet

import (
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func finalDKVSTestManager(t *testing.T) (*Manager, *rgb11MemoryDKVSHTTP) {
	t.Helper()
	priv, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	manager := newRGB11MultiDeviceManager(t, priv, 700)
	remote := newRGB11MemoryDKVSHTTP()
	configureRGB11DKVSTestManager(manager, remote)
	return manager, remote
}
func accountTestKey(t *testing.T, manager *Manager, path string) string {
	t.Helper()
	accountID, err := dkvsAccountID(manager.wallet)
	require.NoError(t, err)
	key, err := dkvsindexer.AccountPersonalKey(accountID, path)
	require.NoError(t, err)
	return key
}
func freeLocalRecord(t *testing.T, manager *Manager, key string, seq uint64, value string) *dkvsindexer.Record {
	t.Helper()
	record, err := newDKVSAccountSignedRecordWithFreeLocal(manager.wallet, key, []byte(value), dkvsindexer.RecordOptions{Seq: seq, IssueHeight: 1, TTL: testRGB11FreeLocalTTL})
	require.NoError(t, err)
	return record
}
func canonicalTestRecord(t *testing.T, manager *Manager, key string, seq uint64, value string) *dkvsindexer.Record {
	t.Helper()
	record, err := NewDKVSSignedRecord(manager.wallet, key, []byte(value), dkvsindexer.RecordOptions{Seq: seq, IssueHeight: 1})
	require.NoError(t, err)
	return record
}
func currentTransportCounts(remote *rgb11MemoryDKVSHTTP) (int, int, int) {
	remote.mu.Lock()
	defer remote.mu.Unlock()
	return remote.statusCalls, remote.snapshotCalls, remote.deltaCalls
}

// Seed legitimate remote writes with an explicit wallet signer. Public raw
// clients intentionally do not acquire signing authority from record bytes.
func finalDKVSSeedClient(manager *Manager, remote *rgb11MemoryDKVSHTTP) *SatsNetDKVSClient {
	return NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote).WithWriteSigner(manager.wallet)
}

func TestDKVSSignedHelpersRebuildDeletedCurrentState(t *testing.T) {
	for _, mode := range []string{"plain", "free-local", "autopay"} {
		t.Run(mode, func(t *testing.T) {
			manager, remote := finalDKVSTestManager(t)
			client, err := manager.ensureDKVSManager().primaryClient()
			require.NoError(t, err)
			key := accountTestKey(t, manager, "wallet/recreated")
			seed := finalDKVSSeedClient(manager, remote)
			original := freeLocalRecord(t, manager, key, 1, "old")
			_, err = seed.PutRecord(original)
			require.NoError(t, err)
			prefix, _, err := dkvsManagedPathForKey(key)
			require.NoError(t, err)
			_, _, err = client.SubscribePrefix(prefix)
			require.NoError(t, err)
			_, err = seed.DeleteCurrentRecord(manager.wallet, key, 1)
			require.NoError(t, err)
			var recreated *swire.DKVSRecord
			switch mode {
			case "plain":
				recreated, err = client.PutSignedRecord(manager.wallet, key, []byte("new"), dkvsindexer.RecordOptions{})
			case "free-local":
				recreated, err = client.PutSignedRecordFreeLocal(manager.wallet, key, []byte("new"), dkvsindexer.RecordOptions{TTL: testRGB11FreeLocalTTL})
			case "autopay":
				recreated, err = client.PutSignedRecordWithAutopay(manager.wallet, key, []byte("new"), dkvsindexer.RecordOptions{}, DKVSAutopayOptions{})
			}
			require.NoError(t, err)
			require.Equal(t, uint64(1), recreated.Seq)
			require.Equal(t, "new", string(recreated.Value))
			// The ACK still does not install confirmed data. Only sync does.
			local, err := client.GetRecord(key)
			require.NoError(t, err)
			require.Equal(t, dkvsindexer.RecordHash(original), dkvsindexer.RecordHash(local))
			require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
			local, err = client.GetRecord(key)
			require.NoError(t, err)
			require.Equal(t, dkvsindexer.RecordHash(recreated), dkvsindexer.RecordHash(local))
		})
	}
}

func TestDKVSFinalManagedPrefixesUseCurrentGenerationSync(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	client, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	keyA, keyB := accountTestKey(t, manager, "wallet/catalog"), accountTestKey(t, manager, "settings/ui")
	prefixA, _, err := dkvsManagedPathForKey(keyA)
	require.NoError(t, err)
	prefixB, _, err := dkvsManagedPathForKey(keyB)
	require.NoError(t, err)
	seed := finalDKVSSeedClient(manager, remote)
	_, err = seed.PutRecord(freeLocalRecord(t, manager, keyA, 1, "a1"))
	require.NoError(t, err)
	_, err = seed.PutRecord(canonicalTestRecord(t, manager, keyB, 1, "b1"))
	require.NoError(t, err)
	baseStatus, baseSnapshots, baseDeltas := currentTransportCounts(remote)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefixA))
	require.NoError(t, manager.SubscribeDKVSPrefix(prefixB))
	_, err = manager.syncDKVSOnce()
	require.NoError(t, err)
	expected, err := normalizeWalletSubscriptionPrefixes([]string{prefixA, prefixB})
	require.NoError(t, err)
	state, err := manager.GetDKVSSubscriptionStatus()
	require.NoError(t, err)
	require.True(t, subscriptionReadyForPrefixes(state, expected))
	status, snapshots, deltas := currentTransportCounts(remote)
	require.Equal(t, []int{baseStatus, baseSnapshots + 2, baseDeltas}, []int{status, snapshots, deltas})
	store := &dkvsStore{manager: manager.dkvs, client: client}
	value, err := store.Get(keyA)
	require.NoError(t, err)
	require.Equal(t, "a1", string(value.Value))
	_, err = seed.PutRecord(freeLocalRecord(t, manager, keyA, 2, "a2"))
	require.NoError(t, err)
	beforeStatus, beforeSnapshots, beforeDeltas := currentTransportCounts(remote)
	_, err = manager.syncDKVSOnce()
	require.NoError(t, err)
	value, err = store.Get(keyA)
	require.NoError(t, err)
	require.Equal(t, uint64(2), value.Seq)
	require.Equal(t, "a2", string(value.Value))
	status, snapshots, deltas = currentTransportCounts(remote)
	require.Equal(t, []int{beforeStatus, beforeSnapshots, beforeDeltas + 2}, []int{status, snapshots, deltas})
	remote.mu.Lock()
	remote.endpointID = "offline-simulated-endpoint"
	remote.mu.Unlock()
	value, err = store.Get(keyA)
	require.NoError(t, err)
	require.Equal(t, uint64(2), value.Seq)
	prefixes, err := manager.ListSubscribedDKVSPrefixes()
	require.NoError(t, err)
	require.Len(t, prefixes, 2)
}

func TestDKVSFinalManagedExplicitDeleteInvalidatesReplica(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	client, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	key := accountTestKey(t, manager, "wallet/explicit-delete")
	prefix, _, err := dkvsManagedPathForKey(key)
	require.NoError(t, err)
	seed := finalDKVSSeedClient(manager, remote)
	first := freeLocalRecord(t, manager, key, 1, "temporary")
	_, err = seed.PutRecord(first)
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	_, err = manager.syncDKVSOnce()
	require.NoError(t, err)
	store := &dkvsStore{manager: manager.dkvs, client: client}
	value, err := store.Get(key)
	require.NoError(t, err)
	require.Equal(t, "temporary", string(value.Value))
	command, err := NewDKVSDeleteCommand(manager.wallet, first, 1)
	require.NoError(t, err)
	_, err = seed.PutRecord(command)
	require.NoError(t, err)
	beforeStatus, beforeSnapshots, beforeDeltas := currentTransportCounts(remote)
	_, err = manager.syncDKVSOnce()
	require.NoError(t, err)
	_, err = store.Get(key)
	require.ErrorIs(t, err, ErrDKVSRecordNotFound)
	local, err := newDKVSReplicaStore(manager.db).LoadLocalKeyState(client.replicaNamespace, key)
	if err != nil {
		require.ErrorIs(t, err, indexercommon.ErrKeyNotFound)
	}
	require.Nil(t, local, "physical deletion must not leave a replica floor")
	status, snapshots, deltas := currentTransportCounts(remote)
	require.Equal(t, beforeStatus, status)
	require.Equal(t, beforeSnapshots+1, snapshots)
	require.Equal(t, beforeDeltas+1, deltas)
}

func TestDKVSCurrentSnapshotRemovesMissingConfirmedKey(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	client, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	first, removed := accountTestKey(t, manager, "wallet/first"), accountTestKey(t, manager, "wallet/history")
	prefix, _, err := dkvsManagedPathForKey(first)
	require.NoError(t, err)
	seed := finalDKVSSeedClient(manager, remote)
	for _, key := range []string{first, removed} {
		_, err := seed.PutRecord(canonicalTestRecord(t, manager, key, 1, key))
		require.NoError(t, err)
	}
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	store := newDKVSReplicaStore(manager.db)
	_, err = store.LoadSubscriptionRecord(client.replicaNamespace, removed)
	require.NoError(t, err)
	remote.mu.Lock()
	delete(remote.records, removed)
	delete(remote.changedAt, removed)
	remote.generations[prefix]++
	remote.mu.Unlock()
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	_, err = store.LoadSubscriptionRecord(client.replicaNamespace, removed)
	require.ErrorIs(t, err, indexercommon.ErrKeyNotFound)
	remaining, err := store.LoadSubscriptionRecord(client.replicaNamespace, first)
	require.NoError(t, err)
	require.Equal(t, first, string(remaining.Value))
}

func TestDKVSCurrentDeltaRepairsMissingKeyThroughSnapshot(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	client, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	key := accountTestKey(t, manager, "wallet/history")
	prefix, _, err := dkvsManagedPathForKey(key)
	require.NoError(t, err)
	seed := finalDKVSSeedClient(manager, remote)
	_, err = seed.PutRecord(canonicalTestRecord(t, manager, key, 1, "history"))
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	store := newDKVSReplicaStore(manager.db)
	before, err := store.LoadSubscriptionState(client.replicaNamespace)
	require.NoError(t, err)
	remote.mu.Lock()
	delete(remote.records, key)
	delete(remote.changedAt, key)
	remote.generations[prefix]++
	remote.mu.Unlock()
	beforeStatus, beforeSnapshots, beforeDeltas := currentTransportCounts(remote)
	_, err = manager.syncDKVSOnce()
	require.NoError(t, err)
	after, err := store.LoadSubscriptionState(client.replicaNamespace)
	require.NoError(t, err)
	require.Greater(t, after.Generations[prefix], before.Generations[prefix])
	_, err = store.LoadSubscriptionRecord(client.replicaNamespace, key)
	require.ErrorIs(t, err, indexercommon.ErrKeyNotFound)
	status, snapshots, deltas := currentTransportCounts(remote)
	require.Equal(t, beforeStatus, status)
	require.Equal(t, beforeSnapshots+1, snapshots)
	require.Equal(t, beforeDeltas+1, deltas)
}

func TestDKVSCurrentSyncDoesNotUseObsoleteDeltaEndpoint(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	client, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	first, second := accountTestKey(t, manager, "wallet/first"), accountTestKey(t, manager, "wallet/second")
	prefix, _, err := dkvsManagedPathForKey(first)
	require.NoError(t, err)
	seed := finalDKVSSeedClient(manager, remote)
	_, err = seed.PutRecord(canonicalTestRecord(t, manager, first, 1, "first"))
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	remote.mu.Lock()
	remote.deltaUnavailable = true
	remote.mu.Unlock()
	_, err = seed.PutRecord(canonicalTestRecord(t, manager, second, 1, "second"))
	require.NoError(t, err)
	beforeStatus, beforeSnapshots, beforeDeltas := currentTransportCounts(remote)
	_, err = manager.syncDKVSOnce()
	require.NoError(t, err)
	status, snapshots, deltas := currentTransportCounts(remote)
	require.Equal(t, beforeStatus, status)
	require.Equal(t, beforeSnapshots, snapshots)
	require.Equal(t, beforeDeltas+1, deltas)
	value, err := newDKVSReplicaStore(manager.db).LoadSubscriptionRecord(client.replicaNamespace, second)
	require.NoError(t, err)
	require.Equal(t, "second", string(value.Value))
}

func TestDKVSCurrentSyncRejectsSourceGenerationRollback(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	client, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	key := accountTestKey(t, manager, "wallet/state")
	prefix, _, err := dkvsManagedPathForKey(key)
	require.NoError(t, err)
	seed := finalDKVSSeedClient(manager, remote)
	_, err = seed.PutRecord(canonicalTestRecord(t, manager, key, 1, "value"))
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	store := newDKVSReplicaStore(manager.db)
	before, err := store.LoadActiveMeta(client.replicaNamespace, dkvsindexer.ActiveScope{Prefix: prefix})
	require.NoError(t, err)
	remote.mu.Lock()
	remote.generations[prefix] = 0
	remote.mu.Unlock()
	_, err = manager.syncDKVSOnce()
	require.ErrorIs(t, err, dkvsindexer.ErrConcurrentUpdate, "bounded retries must not bless a reset source cursor")
	after, err := store.LoadActiveMeta(client.replicaNamespace, dkvsindexer.ActiveScope{Prefix: prefix})
	require.NoError(t, err)
	require.Equal(t, before, after)
	value, err := store.LoadSubscriptionRecord(client.replicaNamespace, key)
	require.NoError(t, err)
	require.Equal(t, "value", string(value.Value))
}

func TestDKVSFinalLocalPutRequiresDeltaBeforeAdvancingToken(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	key := accountTestKey(t, manager, "wallet/local-put")
	prefix, _, err := dkvsManagedPathForKey(key)
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	_, err = manager.syncDKVSOnce()
	require.NoError(t, err)
	store, err := manager.ensureDKVSManager().primaryStore()
	require.NoError(t, err)
	value, err := store.Put(dkvsValueMutation{Key: key, Value: []byte("local-v1"), Owner: manager.wallet, Signature: dkvsSignatureAccount})
	require.NoError(t, err)
	require.Equal(t, uint64(1), value.Seq)
	require.NoError(t, store.WaitReady(key))
	_, err = store.Get(key)
	require.ErrorIs(t, err, ErrDKVSRecordNotFound, "ACK must not materialize the newly created value")
	state, err := manager.GetDKVSSubscriptionStatus()
	require.NoError(t, err)
	require.Zero(t, state.Generations[prefix], "ACK must not advance the completed cursor")
	require.NoError(t, store.SyncCurrent(key))
	local, err := store.Get(key)
	require.NoError(t, err)
	require.Equal(t, "local-v1", string(local.Value))
	status, snapshots, deltas := currentTransportCounts(remote)
	require.Equal(t, []int{0, 1, 1}, []int{status, snapshots, deltas})
	state, err = manager.GetDKVSSubscriptionStatus()
	require.NoError(t, err)
	require.Equal(t, uint64(1), state.Generations[prefix])
	seed := finalDKVSSeedClient(manager, remote)
	_, err = seed.PutRecord(canonicalTestRecord(t, manager, key, 2, "remote-v2"))
	require.NoError(t, err)
	beforeStatus, beforeSnapshots, beforeDeltas := currentTransportCounts(remote)
	require.NoError(t, store.SyncCurrent(key))
	status, snapshots, deltas = currentTransportCounts(remote)
	require.Equal(t, []int{beforeStatus, beforeSnapshots, beforeDeltas + 1}, []int{status, snapshots, deltas})
	current, err := store.Get(key)
	require.NoError(t, err)
	require.Equal(t, uint64(2), current.Seq)
	require.Equal(t, "remote-v2", string(current.Value))
}

func TestDKVSFinalResponseLossReplaysOutboxBeforeCurrentSync(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	key := accountTestKey(t, manager, "wallet/response-loss")
	prefix, _, err := dkvsManagedPathForKey(key)
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	_, err = manager.syncDKVSOnce()
	require.NoError(t, err)
	store, err := manager.ensureDKVSManager().primaryStore()
	require.NoError(t, err)
	remote.mu.Lock()
	remote.postCommitErr = &net.OpError{Op: "read", Net: "tcp", Err: errors.New("response lost")}
	remote.mu.Unlock()
	_, err = store.Put(dkvsValueMutation{Key: key, Value: []byte("committed"), Owner: manager.wallet, Signature: dkvsSignatureAccount})
	require.Error(t, err)
	_, err = manager.syncDKVSOnce()
	require.NoError(t, err)
	local, err := store.Get(key)
	require.NoError(t, err)
	require.Equal(t, uint64(1), local.Seq)
	require.Equal(t, "committed", string(local.Value))
	status, snapshots, deltas := currentTransportCounts(remote)
	require.Equal(t, []int{0, 1, 1}, []int{status, snapshots, deltas})
	entries, err := newDKVSReplicaStore(manager.db).LoadOutbox(store.client.replicaNamespace)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestDKVSFinalNewEmptyPrefixStillGetsSnapshotAndBecomesReady(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	seededKey, emptyKey := accountTestKey(t, manager, "seeded/value"), accountTestKey(t, manager, "empty/value")
	seededPrefix, _, err := dkvsManagedPathForKey(seededKey)
	require.NoError(t, err)
	emptyPrefix, _, err := dkvsManagedPathForKey(emptyKey)
	require.NoError(t, err)
	seed := finalDKVSSeedClient(manager, remote)
	_, err = seed.PutRecord(canonicalTestRecord(t, manager, seededKey, 1, "one"))
	require.NoError(t, err)
	_, baseSnapshots, _ := currentTransportCounts(remote)
	require.NoError(t, manager.SubscribeDKVSPrefix(seededPrefix))
	_, err = manager.syncDKVSOnce()
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(emptyPrefix))
	_, err = manager.syncDKVSOnce()
	require.NoError(t, err)
	expected, err := normalizeWalletSubscriptionPrefixes([]string{emptyPrefix, seededPrefix})
	require.NoError(t, err)
	state, err := manager.GetDKVSSubscriptionStatus()
	require.NoError(t, err)
	require.True(t, subscriptionReadyForPrefixes(state, expected))
	_, snapshots, _ := currentTransportCounts(remote)
	require.Equal(t, baseSnapshots+2, snapshots)
}

func TestDKVSFinalRejectsUnapprovedEndpointSwitch(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	firstKey, secondKey := accountTestKey(t, manager, "first/value"), accountTestKey(t, manager, "second/value")
	firstPrefix, _, err := dkvsManagedPathForKey(firstKey)
	require.NoError(t, err)
	secondPrefix, _, err := dkvsManagedPathForKey(secondKey)
	require.NoError(t, err)
	seed := finalDKVSSeedClient(manager, remote)
	_, err = seed.PutRecord(canonicalTestRecord(t, manager, firstKey, 1, "one"))
	require.NoError(t, err)
	_, err = seed.PutRecord(canonicalTestRecord(t, manager, secondKey, 1, "two"))
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(firstPrefix))
	require.NoError(t, manager.SubscribeDKVSPrefix(secondPrefix))
	_, err = manager.syncDKVSOnce()
	require.NoError(t, err)
	before, err := manager.GetDKVSSubscriptionStatus()
	require.NoError(t, err)
	_, beforeSnapshots, _ := currentTransportCounts(remote)
	remote.mu.Lock()
	remote.endpointID = "replacement-endpoint"
	remote.mu.Unlock()
	_, err = manager.syncDKVSOnce()
	require.ErrorIs(t, err, dkvsindexer.ErrEndpointMismatch)
	_, snapshots, _ := currentTransportCounts(remote)
	require.Equal(t, beforeSnapshots, snapshots)
	after, err := manager.GetDKVSSubscriptionStatus()
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestDKVSFinalUnsubscribedGetDoesNotRegisterPrefix(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	key := accountTestKey(t, manager, "direct/value")
	seed := finalDKVSSeedClient(manager, remote)
	_, err := seed.PutRecord(freeLocalRecord(t, manager, key, 1, "direct"))
	require.NoError(t, err)
	beforeStatus, beforeSnapshots, beforeDeltas := currentTransportCounts(remote)
	store, err := manager.ensureDKVSManager().primaryStore()
	require.NoError(t, err)
	value, err := store.Get(key)
	require.NoError(t, err)
	require.Equal(t, "direct", string(value.Value))
	prefixes, err := manager.ListSubscribedDKVSPrefixes()
	require.NoError(t, err)
	require.Empty(t, prefixes)
	status, snapshots, deltas := currentTransportCounts(remote)
	require.Equal(t, []int{beforeStatus, beforeSnapshots, beforeDeltas}, []int{status, snapshots, deltas})
}

func TestDKVSFinalUnmanagedReadFetchesCurrentState(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	key := accountTestKey(t, manager, "direct/cache")
	seed := finalDKVSSeedClient(manager, remote)
	_, err := seed.PutRecord(canonicalTestRecord(t, manager, key, 1, "one"))
	require.NoError(t, err)
	store, err := manager.ensureDKVSManager().primaryStore()
	require.NoError(t, err)
	first, err := store.Get(key)
	require.NoError(t, err)
	require.Equal(t, "one", string(first.Value))
	_, err = seed.PutRecord(canonicalTestRecord(t, manager, key, 2, "two"))
	require.NoError(t, err)
	current, err := store.Get(key)
	require.NoError(t, err)
	require.Equal(t, uint64(2), current.Seq)
	require.Equal(t, "two", string(current.Value))
}

func TestDKVSFinalCASConflictRefreshesRebuildsAndRetries(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	key := accountTestKey(t, manager, "direct/rebase")
	seed := finalDKVSSeedClient(manager, remote)
	_, err := seed.PutRecord(canonicalTestRecord(t, manager, key, 1, "one"))
	require.NoError(t, err)
	store, err := manager.ensureDKVSManager().primaryStore()
	require.NoError(t, err)
	builds := 0
	values, err := store.Update([]string{key}, func(current map[string]*dkvsValue, next map[string]uint64) ([]dkvsValueMutation, error) {
		builds++
		if builds == 1 {
			if _, err := seed.PutRecord(canonicalTestRecord(t, manager, key, 2, "concurrent")); err != nil {
				return nil, err
			}
		}
		return []dkvsValueMutation{{Key: key, Value: []byte(fmt.Sprintf("rebuilt-%d-%d", builds, next[key])), Owner: manager.wallet, Signature: dkvsSignatureAccount}}, nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, builds)
	require.Len(t, values, 1)
	require.Equal(t, uint64(3), values[0].Seq)
	require.Equal(t, "rebuilt-2-3", string(values[0].Value))
	client, err := manager.dkvs.primaryClient()
	require.NoError(t, err)
	entries, err := newDKVSReplicaStore(manager.db).LoadOutbox(client.replicaNamespace)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestDKVSFinalExplicitRefreshForcesCurrentSnapshot(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	key := accountTestKey(t, manager, "wallet/refresh-current")
	prefix, _, err := dkvsManagedPathForKey(key)
	require.NoError(t, err)
	seed := finalDKVSSeedClient(manager, remote)
	_, err = seed.PutRecord(canonicalTestRecord(t, manager, key, 1, "one"))
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	store, err := manager.ensureDKVSManager().primaryStore()
	require.NoError(t, err)
	require.NoError(t, store.Refresh(key))
	first, err := store.Get(key)
	require.NoError(t, err)
	require.Equal(t, "one", string(first.Value))
	_, err = seed.PutRecord(canonicalTestRecord(t, manager, key, 2, "two"))
	require.NoError(t, err)
	stale, err := store.Get(key)
	require.NoError(t, err)
	require.Equal(t, uint64(1), stale.Seq)
	require.NoError(t, store.Refresh(key))
	current, err := store.Get(key)
	require.NoError(t, err)
	require.Equal(t, uint64(2), current.Seq)
	require.Equal(t, "two", string(current.Value))
}

func TestDKVSFinalMailboxReadDoesNotPersistManagedPrefix(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	sender, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	mailboxID := dkvsindexer.AccountID(manager.wallet.GetPubKey().SerializeCompressed())
	senderID := dkvsindexer.AccountID(sender.PubKey().SerializeCompressed())
	key, err := dkvsindexer.MailMsgKey(mailboxID, senderID, "0")
	require.NoError(t, err)
	remote.seedInternalMailboxRecord(&swire.DKVSRecord{Version: dkvsindexer.Version, Key: key, Value: []byte("message-manager-inner"), Seq: 1, IssueHeight: 1, TTL: 100})
	client, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	records, total, err := client.SubscribeMailbox(mailboxID)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, records, 1)
	prefixes, err := manager.ListSubscribedDKVSPrefixes()
	require.NoError(t, err)
	require.Empty(t, prefixes)
}
func TestDKVSFinalManagedMailboxUsesCurrentGeneration(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	sender, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	mailboxID := dkvsindexer.AccountID(manager.wallet.GetPubKey().SerializeCompressed())
	senderID := dkvsindexer.AccountID(sender.PubKey().SerializeCompressed())
	key, err := dkvsindexer.MailMsgKey(mailboxID, senderID, "managed-0")
	require.NoError(t, err)
	prefix, err := mailboxSubscriptionTarget(mailboxID)
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	_, err = manager.syncDKVSOnce()
	require.NoError(t, err)
	remote.seedInternalMailboxRecord(&swire.DKVSRecord{Version: dkvsindexer.Version, Key: key, Value: []byte("message-manager-inner"), Seq: 1, IssueHeight: 1, TTL: 100})
	_, err = manager.syncDKVSOnce()
	require.NoError(t, err)
	client, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	records, total, err := client.ReadMailboxMessages(mailboxID, 0, 10)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, records, 1)
	require.Equal(t, key, records[0].Key)
	store, err := manager.ensureDKVSManager().primaryStore()
	require.NoError(t, err)
	values, err := store.ListMailboxVerified(mailboxID, dkvsindexer.RecordVerificationOptions{})
	require.NoError(t, err)
	require.Len(t, values, 1)
	require.Equal(t, key, values[0].Key)
	_, initialSnapshots, initialDeltas := currentTransportCounts(remote)
	_, err = manager.syncDKVSOnce()
	require.NoError(t, err)
	status, snapshots, deltas := currentTransportCounts(remote)
	require.Zero(t, status)
	require.Equal(t, initialSnapshots, snapshots)
	require.Equal(t, initialDeltas+1, deltas)
}
func TestDKVSFinalAccountActivationRegistersReceiveAndMailboxPrefixes(t *testing.T) {
	manager, _ := finalDKVSTestManager(t)
	require.NoError(t, manager.refreshDKVSRegistrations())
	accountID, err := dkvsAccountID(manager.wallet)
	require.NoError(t, err)
	mailboxPrefix, err := mailboxSubscriptionTarget(accountID)
	require.NoError(t, err)
	mappingKey, err := dkvsindexer.AccountMappingKey(GetChainParam().Name, manager.wallet.GetAddress())
	require.NoError(t, err)
	mappingPrefix, _, err := dkvsManagedPathForKey(mappingKey)
	require.NoError(t, err)
	prefixes, err := manager.ListSubscribedDKVSPrefixes()
	require.NoError(t, err)
	require.Contains(t, prefixes, mailboxPrefix)
	require.Contains(t, prefixes, mappingPrefix)
	manager.dkvs.mu.Lock()
	_, scheduled := manager.dkvs.jobs[dkvsAccountServiceJobPrefix+accountID]
	manager.dkvs.mu.Unlock()
	require.True(t, scheduled)
	manager.SwitchAccount(1)
	prefixes, err = manager.ListSubscribedDKVSPrefixes()
	require.NoError(t, err)
	require.Equal(t, accountID, manager.rootDKVSAccountID())
	require.Contains(t, prefixes, mailboxPrefix)
}

func TestDKVSFinalOutboxUsesUniqueRequestIDAndPinsFreeLocalEndpoint(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	client, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	key := accountTestKey(t, manager, "wallet/outbox")
	prefix, _, err := dkvsManagedPathForKey(key)
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	gate := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate) }) }
	defer release()
	remote.mu.Lock()
	remote.postGate = gate
	remote.mu.Unlock()
	done := make(chan error, 1)
	record := freeLocalRecord(t, manager, key, 1, "queued")
	go func() { _, err := client.PutRecord(record); done <- err }()
	store := newDKVSReplicaStore(manager.db)
	deadline := time.Now().Add(time.Second)
	var entry *DKVSBatchOutboxEntry
	for time.Now().Before(deadline) {
		entries, err := store.LoadOutbox(client.replicaNamespace)
		require.NoError(t, err)
		if len(entries) == 1 {
			entry = entries[0]
			break
		}
		time.Sleep(time.Millisecond)
	}
	require.NotNil(t, entry, "outbox must be persisted before submission")
	require.NotEmpty(t, entry.RequestID)
	require.NotEqual(t, key, entry.RequestID)
	require.Equal(t, "test-endpoint", entry.EndpointID)
	require.NotNil(t, entry.Authorization, "exact operation authorization must survive a lost response")
	_, err = manager.db.Read(dkvsOutboxKey(client.replicaNamespace, entry.RequestID))
	require.NoError(t, err)
	remote.mu.Lock()
	remote.endpointID = "different-endpoint"
	remote.mu.Unlock()
	release()
	err = <-done
	require.True(t, IsDKVSErrorCode(err, dkvsindexer.ErrorCodeLocalOnlyEndpointMismatch), "error=%v", err)
	entries, err := store.LoadOutbox(client.replicaNamespace)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, DKVSOutboxConflict, entries[0].State)
}

func TestDKVSFinalEndpointSwitchBlockedByActiveFreeLocal(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	client, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	key := accountTestKey(t, manager, "wallet/local-switch")
	prefix, _, err := dkvsManagedPathForKey(key)
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	_, err = client.PutRecord(freeLocalRecord(t, manager, key, 1, "local"))
	require.NoError(t, err)
	// ACK alone does not populate the replica; synchronize before testing an
	// endpoint switch blocked by an actually installed FREE_LOCAL value.
	_, err = manager.syncDKVSOnce()
	require.NoError(t, err)
	remote.mu.Lock()
	remote.endpointID = "replacement-endpoint"
	remote.mu.Unlock()
	_, err = manager.syncDKVSOnce()
	require.ErrorIs(t, err, dkvsindexer.ErrLocalOnlyEndpointMismatch)
}
func TestDKVSFinalUnsubscribePrunesSubscriptionReplica(t *testing.T) {
	manager, remote := finalDKVSTestManager(t)
	client, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	key := accountTestKey(t, manager, "wallet/unsubscribe")
	prefix, _, err := dkvsManagedPathForKey(key)
	require.NoError(t, err)
	seed := finalDKVSSeedClient(manager, remote)
	_, err = seed.PutRecord(canonicalTestRecord(t, manager, key, 1, "value"))
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	store := newDKVSReplicaStore(manager.db)
	_, err = store.LoadSubscriptionRecord(client.replicaNamespace, key)
	require.NoError(t, err)
	require.NoError(t, manager.UnsubscribeDKVSPrefix(prefix))
	_, err = store.LoadSubscriptionRecord(client.replicaNamespace, key)
	require.ErrorIs(t, err, indexercommon.ErrKeyNotFound)
}

func TestDKVSFinalArchitectureGuard(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	versionedProductionFile := regexp.MustCompile(`_v[0-9]+\.go$`)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		content, err := os.ReadFile(name)
		require.NoError(t, err)
		text := string(content)
		require.False(t, strings.HasPrefix(text, "//go:build ignore"), "build-ignore placeholder: %s", name)
		if !strings.HasSuffix(name, "_test.go") {
			require.False(t, versionedProductionFile.MatchString(name), "versioned production file: %s", name)
		}
		if strings.HasSuffix(name, "_test.go") || !strings.Contains(name, "dkvs") {
			continue
		}
		for _, forbidden := range []string{
			"PathWritePrecondition", "EndpointPathState", "EndpointGeneration", "GetClientConfigV1", "PutRecordBatchCASV1", "WatchPath(",
			"/v3/dkvs/pathmeta", "/v3/dkvs/sync/path", "/v3/dkvs/watch/path", "/v3/dkvs/sync/directory", "/v3/dkvs/watch/directory",
			"/v3/dkvs/subscriptions/snapshot", "/v3/dkvs/subscriptions/watch", "dkvs-path-state-v1:", "dkvs-replica-confirmed-v1:", "dkvs-replica-root-v1:",
		} {
			require.NotContains(t, text, forbidden, "file=%s", name)
		}
	}
}
