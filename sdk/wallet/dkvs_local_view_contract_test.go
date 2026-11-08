package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	core "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestDKVSLocalViewContract(t *testing.T) {
	t.Run("write_success_is_locally_visible", func(t *testing.T) {
		m, remote := finalDKVSTestManager(t)
		key := accountTestKey(t, m, "contract-write/value")
		prefix, err := dkvs.CollectionPathForKey(key)
		require.NoError(t, err)
		require.NoError(t, m.SubscribeDKVSPrefix(prefix))
		_, err = m.syncDKVSOnce()
		require.NoError(t, err)
		s, err := m.dkvs.primaryStore()
		require.NoError(t, err)
		written, err := s.Put(dkvsValueMutation{Key: key, Value: []byte("committed"), Owner: m.wallet, Signature: dkvsSignatureAccount})
		require.NoError(t, err)
		read, err := s.Get(key)
		require.NoError(t, err, "successful managed write must already be readable")
		require.Equal(t, written.Hash, read.Hash)
		_ = remote
	})
	t.Run("restart_and_offline_forbid_new_write", func(t *testing.T) {
		m, remote := finalDKVSTestManager(t)
		h := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
		configureRGB11DKVSTestManager(m, h)
		key := accountTestKey(t, m, "contract-offline/value")
		prefix, err := dkvs.CollectionPathForKey(key)
		require.NoError(t, err)
		_, err = finalDKVSSeedClient(m, remote).PutRecord(freeLocalRecord(t, m, key, 1, "cached"))
		require.NoError(t, err)
		require.NoError(t, m.SubscribeDKVSPrefix(prefix))
		_, err = m.syncDKVSOnce()
		require.NoError(t, err)
		releaseDKVSManagerRuntime(m.dkvs)
		m.dkvs = nil
		sends := 0
		h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
			if strings.Contains(path, "batch-cas") {
				sends++
			}
			if strings.Contains(path, "active/sync") {
				return nil, &net.DNSError{Err: "offline", IsTimeout: true}
			}
			return remote.SendDKVSPostContext(ctx, path, body)
		}
		s, err := m.ensureDKVSManager().primaryStore()
		require.NoError(t, err)
		_, err = m.syncDKVSOnce()
		require.Error(t, err)
		cached, err := s.Get(key)
		require.NoError(t, err)
		require.Equal(t, "cached", string(cached.Value))
		_, err = s.Put(dkvsValueMutation{Key: key, Value: []byte("forbidden"), Owner: m.wallet, Signature: dkvsSignatureAccount})
		require.Error(t, err, "offline cache readiness is not write permission")
		require.Zero(t, sends, "offline write must not reach transport")
		entries, err := core.NewReplicaStore(m.db).LoadOutbox(s.client.replicaNamespace)
		require.NoError(t, err)
		require.Empty(t, entries)
	})
	t.Run("unknown_request_syncs_before_exact_replay", func(t *testing.T) {
		m, remote := finalDKVSTestManager(t)
		h := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
		configureRGB11DKVSTestManager(m, h)
		key := accountTestKey(t, m, "contract-unknown/value")
		prefix, err := dkvs.CollectionPathForKey(key)
		require.NoError(t, err)
		require.NoError(t, m.SubscribeDKVSPrefix(prefix))
		_, err = m.syncDKVSOnce()
		require.NoError(t, err)
		s, err := m.dkvs.primaryStore()
		require.NoError(t, err)
		var originalEnvelope []byte
		h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
			if strings.Contains(path, "batch-cas") {
				originalEnvelope = bytes.Clone(body)
			}
			return remote.SendDKVSPostContext(ctx, path, body)
		}
		remote.postCommitErr = &net.OpError{Op: "read", Net: "tcp", Err: errors.New("lost response")}
		_, err = s.Put(dkvsValueMutation{Key: key, Value: []byte("once"), Owner: m.wallet, Signature: dkvsSignatureAccount})
		require.Error(t, err)
		before, err := core.NewReplicaStore(m.db).LoadOutbox(s.client.replicaNamespace)
		require.NoError(t, err)
		require.Len(t, before, 1)
		var calls []string
		replays := 0
		h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
			calls = append(calls, path)
			if strings.Contains(path, "batch-cas") {
				replays++
				var original, replayed DKVSBatchCASRequest
				require.NoError(t, json.Unmarshal(originalEnvelope, &original))
				require.NoError(t, json.Unmarshal(body, &replayed))
				require.Equal(t, original.RequestID, replayed.RequestID)
				require.Equal(t, original.EndpointID, replayed.EndpointID)
				require.Len(t, replayed.Mutations, len(original.Mutations))
				for i, item := range original.Mutations {
					oldWire, e := dkvs.MarshalRecord(item.Record)
					require.NoError(t, e)
					newWire, e := dkvs.MarshalRecord(replayed.Mutations[i].Record)
					require.NoError(t, e)
					require.True(t, bytes.Equal(oldWire, newWire), "replay changed signed record wire bytes")
					require.Equal(t, item.ExpectedETag, replayed.Mutations[i].ExpectedETag)
					require.Equal(t, item.ExpectAbsent, replayed.Mutations[i].ExpectAbsent)
					// Account-signed records intentionally omit PubKey. Wire
					// decoding normalizes all zero-length byte fields to empty
					// slices; normalize only their JSON null/empty spelling.
					for _, record := range []*dkvs.Record{item.Record, replayed.Mutations[i].Record} {
						if len(record.Value) == 0 {
							record.Value = nil
						}
						if len(record.PubKey) == 0 {
							record.PubKey = nil
						}
						if len(record.Signature) == 0 {
							record.Signature = nil
						}
						if len(record.FeeProof) == 0 {
							record.FeeProof = nil
						}
					}
				}
				originalJSON, e := json.Marshal(original)
				require.NoError(t, e)
				replayJSON, e := json.Marshal(replayed)
				require.NoError(t, e)
				require.True(t, bytes.Equal(originalJSON, replayJSON), "replay changed request identity, preconditions or original authorization")
				if !bytes.Equal(originalEnvelope, body) {
					t.Log("zero-length byte-field JSON representation differs; signed wire bytes and original authorization are identical")
				}

			}

			return remote.SendDKVSPostContext(ctx, path, body)
		}
		_, err = m.syncDKVSOnce()
		require.NoError(t, err)
		require.NotEmpty(t, calls)
		require.Equal(t, "/v3/dkvs/active/sync", calls[0], "reconnect must receive before replaying original request")
		require.Equal(t, 1, replays, "unknown delivery must replay only the original request")
		got, err := s.Get(key)
		require.NoError(t, err)
		require.Equal(t, uint64(1), got.Seq)
	})
	t.Run("failed_second_collection_keeps_complete_old_view", func(t *testing.T) {
		m, remote := finalDKVSTestManager(t)
		h := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
		configureRGB11DKVSTestManager(m, h)
		keys := []string{accountTestKey(t, m, "contract-a/value"), accountTestKey(t, m, "contract-b/value")}
		prefixes := make([]string, 2)
		seed := finalDKVSSeedClient(m, remote)
		for i, key := range keys {
			var err error
			prefixes[i], err = dkvs.CollectionPathForKey(key)
			require.NoError(t, err)
			_, err = seed.PutRecord(freeLocalRecord(t, m, key, 1, "old"))
			require.NoError(t, err)
			require.NoError(t, m.SubscribeDKVSPrefix(prefixes[i]))
		}
		_, err := m.syncDKVSOnce()
		require.NoError(t, err)
		for _, key := range keys {
			_, err = seed.PutRecord(freeLocalRecord(t, m, key, 2, "new"))
			require.NoError(t, err)
		}
		h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
			if path == "/v3/dkvs/active/sync" {
				var req dkvs.ActiveSyncRequest
				require.NoError(t, json.Unmarshal(body, &req))
				if req.Scope.Prefix == prefixes[1] {
					return nil, &net.DNSError{Err: "second collection unavailable", IsTimeout: true}
				}
			}
			return remote.SendDKVSPostContext(ctx, path, body)
		}
		_, err = m.syncDKVSOnce()
		require.Error(t, err)
		replica := core.NewReplicaStore(m.db)
		for _, key := range keys {
			v, e := replica.LoadSubscriptionRecord(m.dkvs.owner.dkvsReplicaNamespace(), key)
			require.NoError(t, e)
			require.Equal(t, "old", string(v.Value), "a failed group must not publish its first collection")
		}
	})
	t.Run("acknowledged_request_survives_install_failure", func(t *testing.T) {
		m, remote := finalDKVSTestManager(t)
		h := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
		configureRGB11DKVSTestManager(m, h)
		key := accountTestKey(t, m, "contract-ack/value")
		prefix, err := dkvs.CollectionPathForKey(key)
		require.NoError(t, err)
		require.NoError(t, m.SubscribeDKVSPrefix(prefix))
		_, err = m.syncDKVSOnce()
		require.NoError(t, err)
		h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
			if path == "/v3/dkvs/active/sync" {
				return nil, &net.DNSError{Err: "ACK received; install unavailable", IsTimeout: true}
			}
			return remote.SendDKVSPostContext(ctx, path, body)
		}
		s, err := m.dkvs.primaryStore()
		require.NoError(t, err)
		_, err = s.Put(dkvsValueMutation{Key: key, Value: []byte("accepted"), Owner: m.wallet, Signature: dkvsSignatureAccount})
		require.Error(t, err, "must not report locally visible success before install")
		putErr := err
		entries, err := core.NewReplicaStore(m.db).LoadOutbox(s.client.replicaNamespace)
		require.NoError(t, err)
		require.Len(t, entries, 1, "accepted request must remain durably identifiable")
		var pending *DKVSRequestPendingError
		require.ErrorAs(t, putErr, &pending)
		require.True(t, pending.Committed)
		require.Equal(t, DKVSOutboxAcknowledged, entries[0].State)
		requestID := entries[0].RequestID
		sends := 0
		h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
			if strings.Contains(path, "batch-cas") {
				sends++
			}
			return remote.SendDKVSPostContext(ctx, path, body)
		}
		releaseDKVSManagerRuntime(m.dkvs)
		m.dkvs = nil
		s, err = m.ensureDKVSManager().primaryStore()
		require.NoError(t, err)
		_, err = m.syncDKVSOnce()
		require.NoError(t, err)
		require.Zero(t, sends, "a durable ACK must not be resent after restart")
		read, err := s.Get(key)
		require.NoError(t, err)
		require.Equal(t, "accepted", string(read.Value))
		remaining, err := core.NewReplicaStore(m.db).LoadOutbox(s.client.replicaNamespace)
		require.NoError(t, err)
		require.Empty(t, remaining)
		require.NotEmpty(t, requestID)

	})
}

func TestDKVSOfflineBusinessMutationsPreserveProfile(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	m, remote, store, _ := accountTTLNoopFixture(t)
	h := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if path == "/v3/dkvs/active/sync" {
			return nil, &net.DNSError{Err: "offline", IsTimeout: true}
		}
		return remote.SendDKVSPostContext(ctx, path, body)
	}
	store.client.Http = h
	_, err := m.syncDKVSOnce()
	require.Error(t, err)
	before, err := m.db.Read(accountManagementProfileKey())
	require.NoError(t, err)
	id := m.GetCurrentWalletId()
	previousName := m.walletInfoMap[id].Name
	err = m.UpdateWalletName(id, "offline must reject this")
	require.Error(t, err, "offline wallet metadata mutation must fail before local side effects")
	after, err := m.db.Read(accountManagementProfileKey())
	require.NoError(t, err)
	require.True(t, string(before) == string(after), "offline mutation changed persisted profile")
	require.True(t, previousName == m.walletInfoMap[id].Name, "offline mutation changed live catalog")
}

// This wrapper retains the backend's actual atomic batch, failing only Flush.
type dkvsAtomicFlushFaultDB struct{ indexer.KVDB }
type dkvsAtomicFlushFaultBatch struct{ indexer.WriteBatch }

func (d *dkvsAtomicFlushFaultDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return &dkvsAtomicFlushFaultBatch{d.KVDB.(indexer.AtomicWriteBatchDB).NewAtomicWriteBatch()}
}
func (b *dkvsAtomicFlushFaultBatch) Flush() error {
	return errors.New("injected atomic publication failure")
}
func TestDKVSAtomicPublicationFailurePreservesAllCollections(t *testing.T) {
	m, remote := finalDKVSTestManager(t)
	keys := []string{accountTestKey(t, m, "atomic-a/value"), accountTestKey(t, m, "atomic-b/value")}
	seed := finalDKVSSeedClient(m, remote)
	for _, key := range keys {
		prefix, err := dkvs.CollectionPathForKey(key)
		require.NoError(t, err)
		_, err = seed.PutRecord(freeLocalRecord(t, m, key, 1, "old"))
		require.NoError(t, err)
		require.NoError(t, m.SubscribeDKVSPrefix(prefix))
	}
	_, err := m.syncDKVSOnce()
	require.NoError(t, err)
	for _, key := range keys {
		_, err = seed.PutRecord(freeLocalRecord(t, m, key, 2, "new"))
		require.NoError(t, err)
	}
	m.db = &dkvsAtomicFlushFaultDB{m.db}
	_, err = m.syncDKVSOnce()
	require.ErrorContains(t, err, "injected atomic publication failure")
	snapshot, err := core.NewReplicaStore(m.db).ReadSnapshot(m.dkvsReplicaNamespace(), keys)
	require.NoError(t, err)
	for _, key := range keys {
		require.Equal(t, "old", string(snapshot[key].Value))
		require.Equal(t, uint64(1), snapshot[key].Seq)
	}
}

func TestDKVSNormalWatchWakePreservesWriteQualification(t *testing.T) {
	m, remote := finalDKVSTestManager(t)
	h := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	configureRGB11DKVSTestManager(m, h)
	key := accountTestKey(t, m, "watch-wake/value")
	prefix, err := dkvs.CollectionPathForKey(key)
	require.NoError(t, err)
	require.NoError(t, m.SubscribeDKVSPrefix(prefix))
	_, err = m.syncDKVSOnce()
	require.NoError(t, err)
	client, err := m.dkvs.primaryClient()
	require.NoError(t, err)
	require.NoError(t, m.dkvs.requireWritePaths(client, []string{key}))
	entered := make(chan struct{})
	wake := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan error, 1)
	h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if path == "/v3/dkvs/active/watch" {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return remote.SendDKVSPostContext(ctx, path, body)
	}
	go func() { done <- m.dkvs.watchManagedActive(stop, wake, time.Time{}) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("watch did not enter controlled request")
	}
	close(wake)
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("watch did not cancel")
	}
	require.NoError(t, m.dkvs.requireWritePaths(client, []string{key}), "an owned wake cancellation is not an offline observation")
}

func TestDKVSRestartOnlineReadWaitsForSessionSync(t *testing.T) {
	m, remote := finalDKVSTestManager(t)
	h := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	configureRGB11DKVSTestManager(m, h)
	key := accountTestKey(t, m, "restart-online/value")
	prefix, err := dkvs.CollectionPathForKey(key)
	require.NoError(t, err)
	seed := finalDKVSSeedClient(m, remote)
	_, err = seed.PutRecord(freeLocalRecord(t, m, key, 1, "cached"))
	require.NoError(t, err)
	require.NoError(t, m.SubscribeDKVSPrefix(prefix))
	_, err = m.syncDKVSOnce()
	require.NoError(t, err)
	_, err = seed.PutRecord(freeLocalRecord(t, m, key, 2, "current"))
	require.NoError(t, err)
	releaseDKVSManagerRuntime(m.dkvs)
	m.dkvs = nil
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if path == "/v3/dkvs/active/sync" {
			select {
			case <-entered:
			default:
				close(entered)
			}
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return remote.SendDKVSPostContext(ctx, path, body)
	}
	defer close(release)
	store, err := m.ensureDKVSManager().primaryStore()
	require.NoError(t, err)
	var got *dkvsValue
	go func() { var err error; got, err = store.Get(key); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("read bypassed this session synchronization: %v", err)
	case <-time.After(time.Second):
		t.Fatal("session synchronization did not start")
	}
	select {
	case <-done:
		t.Fatal("persisted READY escaped the active sync barrier")
	default:
	}
	release <- struct{}{}
	select {
	case err := <-done:
		require.NoError(t, err)
		require.Equal(t, "current", string(got.Value))
	case <-time.After(time.Second):
		t.Fatal("synchronized read did not finish")
	}
}

func TestDKVSOfflineUnconfiguredProfileRejectsMetadata(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	m := newAccountManagementAutoTestManager(t)
	remote := m.http.(*rgb11MemoryDKVSHTTP)
	_, err := m.ImportWallet(accountRootWrapperTestMnemonic, "password")
	require.NoError(t, err)
	require.NoError(t, m.InitializeAccountManagement("password"))
	beforeRemote := snapshotRGB11MemoryRecords(remote)
	before := m.GetWalletCatalog()
	require.ErrorIs(t, m.UpdateWalletName(m.GetCurrentWalletId(), "unknown edit"), ErrDKVSPathNotSynced)
	require.Equal(t, before, m.GetWalletCatalog())
	require.Equal(t, "unknown", m.GetAccountManagementStatus().DiscoveryStatus)
	require.NoError(t, m.refreshDKVSRegistrations())
	_, err = m.syncDKVSOnce()
	require.NoError(t, err)
	require.True(t, m.GetAccountManagementStatus().LocalOnly)
	require.NoError(t, m.UpdateWalletName(m.GetCurrentWalletId(), "verified inactive local edit"))
	require.True(t, m.accountProfile.ManagedDataDirty)
	require.False(t, m.accountProfile.ManagedEntered)
	require.NoError(t, m.refreshAccountService())
	require.Equal(t, beforeRemote, snapshotRGB11MemoryRecords(remote), "inactive discovery/edit must not publish")
	h := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if path == "/v3/dkvs/active/sync" {
			return nil, &net.DNSError{Err: "offline", IsTimeout: true}
		}
		return remote.SendDKVSPostContext(ctx, path, body)
	}
	m.SetDKVSHttpClient(h)
	_, err = m.syncDKVSOnce()
	require.Error(t, err)
	saved, err := m.db.Read(accountManagementProfileKey())
	require.NoError(t, err)
	require.ErrorIs(t, m.UpdateWalletName(m.GetCurrentWalletId(), "offline edit"), ErrDKVSPathNotSynced)
	unchanged, err := m.db.Read(accountManagementProfileKey())
	require.NoError(t, err)
	require.Equal(t, saved, unchanged)
	reopened := newAccountManagementAutoTestManager(t)
	reopened.db = m.db
	require.NoError(t, reopened.initDB())
	_, err = reopened.UnlockWallet("password")
	require.NoError(t, err)
	require.Equal(t, "verified inactive local edit", reopened.GetWalletCatalog()[0].Name)
	require.False(t, reopened.GetAccountManagementStatus().LocalOnly, "restart must not reuse cached absence as online permission")
	require.ErrorIs(t, reopened.UpdateWalletName(reopened.GetCurrentWalletId(), "cached absence edit"), ErrDKVSPathNotSynced)
	require.True(t, reopened.accountProfile.ManagedDataDirty)
	require.Equal(t, beforeRemote, snapshotRGB11MemoryRecords(remote))
}
