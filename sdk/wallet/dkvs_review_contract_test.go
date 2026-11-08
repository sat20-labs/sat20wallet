package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	core "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func reviewContractFixture(t *testing.T) (*Manager, *rgb11MemoryDKVSHTTP, *reviewAcceptanceHTTP, *dkvsStore, []string) {
	t.Helper()
	m, r := finalDKVSTestManager(t)
	h := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: r}
	configureRGB11DKVSTestManager(m, h)
	keys := []string{accountTestKey(t, m, "review-a/value"), accountTestKey(t, m, "review-b/value")}
	for _, key := range keys {
		_, e := finalDKVSSeedClient(m, r).PutRecord(freeLocalRecord(t, m, key, 1, "old"))
		require.NoError(t, e)
		p, e := dkvs.CollectionPathForKey(key)
		require.NoError(t, e)
		require.NoError(t, m.SubscribeDKVSPrefix(p))
	}
	_, e := m.syncDKVSOnce()
	require.NoError(t, e)
	s, e := m.dkvs.primaryStore()
	require.NoError(t, e)
	return m, r, h, s, keys
}

func TestDKVSReviewContract(t *testing.T) {
	t.Run("ack_install_includes_other_changed_collection", func(t *testing.T) {
		m, r, h, s, keys := reviewContractFixture(t)
		injected := false
		h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
			response, e := r.SendDKVSPostContext(ctx, path, body)
			if e == nil && strings.Contains(path, "batch-cas") && !injected {
				injected = true
				var batch []dkvs.CASMutation
				for _, key := range keys {
					previous, e := finalDKVSSeedClient(m, r).GetRecordDirect(key)
					require.NoError(t, e)
					hash := dkvs.RecordHash(previous)
					batch = append(batch, dkvs.CASMutation{Record: freeLocalRecord(t, m, key, previous.Seq+1, "remote-pair"), Precondition: dkvs.WritePrecondition{ExpectedHash: &hash}})
				}
				_, e = finalDKVSSeedClient(m, r).PutRecordBatchCAS(batch)
				require.NoError(t, e)
			}
			return response, e
		}
		_, e := s.Put(dkvsValueMutation{Key: keys[0], Value: []byte("own"), Owner: m.wallet, Signature: dkvsSignatureAccount, Policy: dkvsStoragePolicy{FreeLocal: true, TTL: testRGB11FreeLocalTTL}})
		require.NoError(t, e)
		pair, e := core.NewReplicaStore(m.db).ReadSnapshot(s.client.replicaNamespace, keys)
		require.NoError(t, e)
		for _, key := range keys {
			require.Equal(t, "remote-pair", string(pair[key].Value), "a remote atomic pair must not be partially installed")
		}
	})
	t.Run("background_send_failure_revokes_all_write_qualification", func(t *testing.T) {
		m, r, h, s, keys := reviewContractFixture(t)
		r.postCommitErr = &net.OpError{Op: "read", Net: "tcp", Err: errors.New("lost response")}
		_, e := s.Put(dkvsValueMutation{Key: keys[0], Value: []byte("unknown"), Owner: m.wallet, Signature: dkvsSignatureAccount})
		require.Error(t, e)
		h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
			if strings.Contains(path, "batch-cas") {
				return nil, &net.DNSError{Err: "offline", IsTimeout: true}
			}
			return r.SendDKVSPostContext(ctx, path, body)
		}
		_, sendErr, receiveErr := m.syncDKVSOnceResult()
		require.NoError(t, receiveErr)
		require.Error(t, sendErr)
		require.Error(t, m.dkvs.requireWritePaths(s.client, []string{keys[1]}), "observed send failure must revoke unrelated new writes")
	})
	t.Run("coordinator_revokes_before_release", func(t *testing.T) {
		m, _, _, s, keys := reviewContractFixture(t)
		entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			done <- m.dkvs.runTransport(func() error { close(entered); <-release; return &net.DNSError{Err: "offline", IsTimeout: true} })
		}()
		<-entered
		m.dkvs.runMu.Lock()
		slot := m.dkvs.runDone
		m.dkvs.runMu.Unlock()
		m.dkvs.mu.Lock()
		close(release)
		early := false
		select {
		case <-slot:
			early = true
		case <-time.After(25 * time.Millisecond):
		}
		m.dkvs.mu.Unlock()
		require.Error(t, <-done)
		require.False(t, early, "transport slot released while qualification revocation is blocked")
		require.Error(t, m.dkvs.requireWritePaths(s.client, keys))
	})
	t.Run("invalid_ack_is_not_confirmed", func(t *testing.T) {
		m, r, h, s, keys := reviewContractFixture(t)
		h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
			response, e := r.SendDKVSPostContext(ctx, path, body)
			if e == nil && strings.Contains(path, "batch-cas") {
				var env map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(response, &env))
				var ack dkvs.WriteResult
				require.NoError(t, json.Unmarshal(env["data"], &ack))
				ack.EndpointID = "unexpected-source"
				env["data"], e = json.Marshal(ack)
				require.NoError(t, e)
				return json.Marshal(env)
			}
			return response, e
		}
		_, e := s.Put(dkvsValueMutation{Key: keys[0], Value: []byte("test"), Owner: m.wallet, Signature: dkvsSignatureAccount})
		require.Error(t, e)
		var pending *DKVSRequestPendingError
		require.ErrorAs(t, e, &pending)
		require.False(t, pending.Committed, "unverified source is not a confirmed commit")
		outbox, e := core.NewReplicaStore(m.db).LoadOutbox(s.client.replicaNamespace)
		require.NoError(t, e)
		require.Len(t, outbox, 1)
		require.NotEqual(t, DKVSOutboxAcknowledged, outbox[0].State)
	})
	t.Run("offline_cache_detects_missing_record", func(t *testing.T) {
		m, r, h, s, keys := reviewContractFixture(t)
		var storageKey []byte
		require.NoError(t, m.db.BatchRead([]byte("dkvs:subscription-record:"), false, func(k, v []byte) error {
			if strings.HasSuffix(string(k), keys[0]) {
				storageKey = append([]byte(nil), k...)
			}
			return nil
		}))
		require.NotEmpty(t, storageKey)
		require.NoError(t, m.db.Delete(storageKey))
		releaseDKVSManagerRuntime(m.dkvs)
		m.dkvs = nil
		h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
			if strings.Contains(path, "active/sync") {
				return nil, &net.DNSError{Err: "offline", IsTimeout: true}
			}
			return r.SendDKVSPostContext(ctx, path, body)
		}
		s, e := m.ensureDKVSManager().primaryStore()
		require.NoError(t, e)
		_, e = s.Get(keys[0])
		require.Error(t, e)
		require.False(t, errors.Is(e, ErrDKVSRecordNotFound), "corrupt collection cannot claim verified absence")
	})
	t.Run("overlap_candidates_publish_nothing", func(t *testing.T) {
		m, r, _, s, keys := reviewContractFixture(t)
		store := core.NewReplicaStore(m.db)
		prefix, e := dkvs.CollectionPathForKey(keys[0])
		require.NoError(t, e)
		before, e := store.ActiveBaseline(s.client.replicaNamespace, dkvs.ActiveScope{Prefix: prefix})
		require.NoError(t, e)
		_, e = finalDKVSSeedClient(m, r).PutRecord(freeLocalRecord(t, m, keys[0], 2, "new"))
		require.NoError(t, e)
		var installs []core.ActiveInstall
		for _, scope := range []dkvs.ActiveScope{{Prefix: prefix}, {Prefix: prefix, Keys: []string{keys[0]}}} {
			baseline, e := store.ActiveBaseline(s.client.replicaNamespace, scope)
			require.NoError(t, e)
			meta, records, e := finalDKVSSeedClient(m, r).collectActivePages(context.Background(), dkvs.ActiveSyncRequest{Scope: scope, EndpointID: r.endpointID, Full: true}, nil)
			require.NoError(t, e)
			installs = append(installs, core.ActiveInstall{Baseline: baseline, Meta: meta, Records: records, Full: true})
		}
		_, e = store.InstallActiveStates(s.client.replicaNamespace, installs)
		require.Error(t, e, "overlapping scopes must be rejected even when individually valid")
		after, e := store.ActiveBaseline(s.client.replicaNamespace, dkvs.ActiveScope{Prefix: prefix})
		require.NoError(t, e)
		require.Equal(t, before, after, "rejected overlap published records or metadata")
		value, e := store.LoadSubscriptionRecord(s.client.replicaNamespace, keys[0])
		require.NoError(t, e)
		require.Equal(t, "old", string(value.Value))
	})
	t.Run("single_get_uses_point_read_multi_key_uses_view", func(t *testing.T) {
		m, _, _, s, keys := reviewContractFixture(t)
		audit := &reviewReadViewDB{KVDB: m.db}
		m.db = audit
		_, e := s.Get(keys[0])
		require.NoError(t, e)
		require.Zero(t, audit.views, "single-key get must not snapshot unrelated database contents")
		_, e = core.NewReplicaStore(m.db).ReadSnapshot(s.client.replicaNamespace, keys)
		require.NoError(t, e)
		require.Equal(t, 1, audit.views)
	})
}

type reviewReadViewDB struct {
	indexer.KVDB
	views int
}

func (d *reviewReadViewDB) View(fn func(indexer.ReadBatch) error) error {
	d.views++
	return d.KVDB.View(fn)
}
func (d *reviewReadViewDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return d.KVDB.(indexer.AtomicWriteBatchDB).NewAtomicWriteBatch()
}

// Account verification follows the authenticated root and each current
// module baseline. The SDK no longer promises that independent module values
// are read in one database View.
func TestDKVSReviewAccountPairUsesOneView(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	m, _, store, _ := accountTTLNoopFixture(t)
	profile, secret, root, generation, err := m.accountManagementVerifiedSnapshot(store)
	zeroBytes(secret)
	require.NoError(t, err)
	rootID, err := dkvsAccountID(root)
	require.NoError(t, err)
	require.Equal(t, profile.AccountID, rootID)
	require.Equal(t, walletFingerprint(root), profile.RootFingerprint)
	require.NotZero(t, generation)
	require.True(t, m.GetAccountManagementStatus().DKVSWriteAllowed)
	modules, err := m.listAccountLocal(profile.AccountID)
	require.NoError(t, err)
	require.NotEmpty(t, modules)
	indexKey, err := account.ManagedIndexKey(profile.AccountID)
	require.NoError(t, err)
	var foundIndex, foundWallet bool
	for _, module := range modules {
		if module.Provider == "account" && module.Key == indexKey {
			foundIndex = true
		}
		if module.Provider == "wallets" && module.Scope == AccountManagedDataGlobalScope {
			foundWallet = true
		}
		if module.Generation != module.Confirmed || module.Baseline == nil ||
			module.Baseline.Key != module.Key || !bytes.Equal(module.Value, module.Baseline.Value) {
			t.Fatalf("module baseline is not confirmed for provider=%s scope=%s", module.Provider, module.Scope)
		}
	}
	require.True(t, foundIndex, "fresh account index is missing for the verified root")
	require.True(t, foundWallet, "fresh wallet module is missing for the verified root")
}
func TestDKVSReviewOfflineBinding(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	m, _, store, _ := accountTTLNoopFixture(t)
	root, err := m.accountManagementRootWallet()
	require.NoError(t, err)
	bind, err := m.prepareAccountCoreNodeBinding(root, store)
	require.NoError(t, err)
	key, err := dkvs.AccountMappingKey(GetChainParam().Name, root.GetAddress())
	require.NoError(t, err)
	m.dkvs.invalidateOnlineSession()
	t.Run("mapping_write_qualification", func(t *testing.T) { require.Error(t, m.dkvs.prepareManagedWrite(store.client, []string{key})) })
	t.Run("binding_no_offline_commit", func(t *testing.T) { require.Error(t, bind(), "binding must not send while this session is offline") })
}

func TestDKVSReviewAckBoundaries(t *testing.T) {
	t.Run("invalid_echo_retains_unknown_original", func(t *testing.T) {
		m, r, h, s, keys := reviewContractFixture(t)
		h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
			data, err := r.SendDKVSPostContext(ctx, path, body)
			if err == nil && strings.Contains(path, "batch-cas") {
				var env map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(data, &env))
				var ack dkvs.WriteResult
				require.NoError(t, json.Unmarshal(env["data"], &ack))
				ack.RequestID = "wrong-request"
				env["data"], err = json.Marshal(ack)
				require.NoError(t, err)
				return json.Marshal(env)
			}
			return data, err
		}
		var err error
		require.NotPanics(t, func() {
			_, err = s.Put(dkvsValueMutation{Key: keys[0], Value: []byte("accepted"), Owner: m.wallet, Signature: dkvsSignatureAccount})
		})
		var pending *DKVSRequestPendingError
		require.ErrorAs(t, err, &pending)
		require.False(t, pending.Committed)
		require.False(t, pending.Durable)
		entries, e := core.NewReplicaStore(m.db).LoadOutbox(s.client.replicaNamespace)
		require.NoError(t, e)
		require.Len(t, entries, 1)
		require.NotEqual(t, DKVSOutboxAcknowledged, entries[0].State)
		require.NotEqual(t, DKVSOutboxTerminal, entries[0].State)
	})
	t.Run("verified_ack_persistence_failure_retains_original", func(t *testing.T) {
		m, r, h, s, keys := reviewContractFixture(t)
		original := m.db
		m.db = &dkvsAtomicFlushFaultDB{KVDB: original}
		_, err := s.Put(dkvsValueMutation{Key: keys[0], Value: []byte("accepted"), Owner: m.wallet, Signature: dkvsSignatureAccount})
		var pending *DKVSRequestPendingError
		require.ErrorAs(t, err, &pending)
		require.True(t, pending.Committed)
		require.False(t, pending.Durable)
		entries, e := core.NewReplicaStore(original).LoadOutbox(s.client.replicaNamespace)
		require.NoError(t, e)
		require.Len(t, entries, 1)
		require.Equal(t, DKVSOutboxInflight, entries[0].State)
		requestID := entries[0].RequestID
		mutations, e := entries[0].DecodeMutations()
		require.NoError(t, e)
		replayed := 0
		h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
			if strings.Contains(path, "batch-cas") {
				var req DKVSBatchCASRequest
				require.NoError(t, json.Unmarshal(body, &req))
				require.Equal(t, requestID, req.RequestID)
				require.Equal(t, dkvs.RecordHash(mutations[0].Record), dkvs.RecordHash(req.Mutations[0].Record))
				replayed++
			}
			return r.SendDKVSPostContext(ctx, path, body)
		}
		m.db = original
		releaseDKVSManagerRuntime(m.dkvs)
		m.dkvs = nil
		m.ensureDKVSManager()
		_, e = m.syncDKVSOnce()
		require.NoError(t, e)
		require.Equal(t, 1, replayed)
		entries, e = core.NewReplicaStore(original).LoadOutbox(s.client.replicaNamespace)
		require.NoError(t, e)
		require.Empty(t, entries)
	})
}

func TestDKVSReviewOfflineCacheIntegrity(t *testing.T) {
	for _, mode := range []string{"valid", "validly_signed_wrong_root"} {
		t.Run(mode, func(t *testing.T) {
			m, r, h, s, keys := reviewContractFixture(t)
			if mode != "valid" {
				var storageKey []byte
				require.NoError(t, m.db.BatchRead([]byte("dkvs:subscription-record:"), false, func(k, v []byte) error {
					if strings.HasSuffix(string(k), keys[0]) {
						storageKey = append([]byte(nil), k...)
					}
					return nil
				}))
				data, e := dkvs.MarshalRecord(freeLocalRecord(t, m, keys[0], 1, "different-valid-history"))
				require.NoError(t, e)
				require.NoError(t, m.db.Write(storageKey, data))
			}
			releaseDKVSManagerRuntime(m.dkvs)
			m.dkvs = nil
			h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
				if strings.Contains(path, "active/sync") {
					return nil, &net.DNSError{Err: "offline", IsTimeout: true}
				}
				return r.SendDKVSPostContext(ctx, path, body)
			}
			s, e := m.ensureDKVSManager().primaryStore()
			require.NoError(t, e)
			v, e := s.Get(keys[0])
			if mode == "valid" {
				require.NoError(t, e)
				require.Equal(t, "old", string(v.Value))
				require.Error(t, m.dkvs.requireWritePaths(s.client, keys))
			} else {
				require.Error(t, e)
				require.Nil(t, v)
			}
		})
	}
}
func TestDKVSReviewLateWatchErrorPreservesNewQualification(t *testing.T) {
	m, r, h, s, keys := reviewContractFixture(t)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if strings.Contains(path, "active/watch") {
			close(entered)
			<-release
			return nil, &net.DNSError{Err: "old watch connection", IsTimeout: true}
		}
		return r.SendDKVSPostContext(ctx, path, body)
	}
	go func() { done <- m.dkvs.watchManagedActive(make(chan struct{}), make(chan struct{}), time.Time{}) }()
	<-entered
	_, e := m.syncDKVSOnce()
	require.NoError(t, e)
	close(release)
	require.Error(t, <-done)
	require.NoError(t, m.dkvs.requireWritePaths(s.client, keys), "late error from the old long-poll revoked a newer successful sync")
}

func TestDKVSReviewAccountLoadUsesOneView(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	m, _, _, _ := accountTTLNoopFixture(t)
	status := m.GetAccountManagementStatus()
	require.True(t, status.RecoveryConfigured)
	require.NotEmpty(t, status.PackageID)
	location, err := m.AccountIndexerLocation()
	require.NoError(t, err)
	locator := account.Locator{Version: account.Version, AccountID: status.AccountID,
		PackageID: status.PackageID, RecoveryMode: account.RecoveryMode(status.RecoveryMode)}
	m.mutex.RLock()
	secret := append([]byte(nil), m.accountSecret...)
	m.mutex.RUnlock()
	defer zeroBytes(secret)
	recovered, err := m.LoadAccountManagementStateForRecovery(location, locator, secret, accountRootWrapperTestMnemonic)
	require.NoError(t, err)
	require.NotNil(t, recovered)
	require.Equal(t, status.AccountID, locator.AccountID)
	require.Equal(t, status.RootFingerprint, recovered.State.RootFingerprint)
	entry, err := accountEntry(status.AccountID, "wallets", AccountManagedDataGlobalScope)
	require.NoError(t, err)
	var walletEntryFound bool
	for _, current := range recovered.Index.Entries {
		if current.Provider == entry.Provider && current.Scope == entry.Scope {
			walletEntryFound = true
			require.False(t, current.Deleted)
			require.Equal(t, entry.Key, current.Key)
		}
	}
	require.True(t, walletEntryFound, "recovered index must identify the same canonical wallet module")
	require.NotEmpty(t, recovered.Copies)
	for _, copy := range recovered.Copies {
		require.NotNil(t, copy.Record)
		require.Equal(t, copy.Entry.Key, copy.Record.Key)
	}
}
func TestDKVSReviewAccountSyncAndWrapperUseOneView(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	m, _, store, _ := accountTTLNoopFixture(t)
	require.NoError(t, m.SyncAccountManagementState(context.Background()))
	require.NoError(t, m.syncAccountRootWrapper(store))
	status := m.GetAccountManagementStatus()
	accountID := status.AccountID
	secret := append([]byte(nil), m.accountSecret...)
	defer zeroBytes(secret)
	modules, err := m.listAccountLocal(accountID)
	require.NoError(t, err)
	for _, module := range modules {
		require.NotNil(t, module.Baseline)
		require.Equal(t, module.Generation, module.Confirmed)
		require.Equal(t, module.Key, module.Baseline.Key)
		require.Equal(t, module.Value, module.Baseline.Value)
	}
	indexKey, err := account.ManagedIndexKey(accountID)
	require.NoError(t, err)
	indexRecord, err := store.GetAuthoritative(indexKey)
	require.NoError(t, err)
	require.NotNil(t, indexRecord)
	index, err := account.OpenManagedIndex(secret, accountID, m.cfg.Chain, indexRecord.Value)
	require.NoError(t, err)
	walletEntry, err := accountEntry(accountID, "wallets", AccountManagedDataGlobalScope)
	require.NoError(t, err)
	var indexed bool
	for _, candidate := range index.Entries {
		if candidate.Provider == walletEntry.Provider && candidate.Scope == walletEntry.Scope {
			indexed = !candidate.Deleted && candidate.Key == walletEntry.Key
		}
	}
	require.True(t, indexed, "confirmed index must point to the active wallet module")
	walletRecord, err := store.GetAuthoritative(walletEntry.Key)
	require.NoError(t, err)
	require.NotNil(t, walletRecord)
	walletState, err := account.OpenManagedState(secret, accountID, walletRecord.Value)
	require.NoError(t, err)
	require.Equal(t, status.RootFingerprint, walletState.RootFingerprint)
	root, err := m.accountManagementRootWallet()
	require.NoError(t, err)
	wrapperKey, err := accountRootWrapperKey(root)
	require.NoError(t, err)
	wrapper, err := store.GetAuthoritative(wrapperKey)
	require.NoError(t, err)
	require.NotNil(t, wrapper)
	payload, err := openAccountRootWrapper(root, m.cfg.Chain, accountID, wrapper.Value)
	require.NoError(t, err)
	defer zeroBytes(payload.Secret)
	require.Equal(t, secret, payload.Secret)
	require.Equal(t, status.PackageID, payload.PackageID)
	require.Equal(t, status.StorageMode, payload.StorageMode)
}

func TestDKVSReviewWatchFailureRevokesCurrentQualification(t *testing.T) {
	m, _, h, s, keys := reviewContractFixture(t)
	h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		return nil, &net.DNSError{Err: "current connection failed", IsTimeout: true}
	}
	err := m.dkvs.watchManagedActive(make(chan struct{}), make(chan struct{}), time.Time{})
	require.Error(t, err)
	require.Error(t, m.dkvs.requireWritePaths(s.client, keys))
	_, err = s.Get(keys[0])
	require.NoError(t, err, "an existing verified cache remains readable")
}
func TestDKVSReviewUnchangedCollectionIsMetadataOnly(t *testing.T) {
	m, r, h, s, keys := reviewContractFixture(t)
	prefix, e := dkvs.CollectionPathForKey(keys[1])
	require.NoError(t, e)
	metadataReads := 0
	h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if strings.Contains(path, "active/sync") {
			var req dkvs.ActiveSyncRequest
			require.NoError(t, json.Unmarshal(body, &req))
			if req.Scope.Prefix == prefix {
				metadataReads++
				require.False(t, req.Full)
				require.NotZero(t, req.After)
			}
		}
		return r.SendDKVSPostContext(ctx, path, body)
	}
	audit := &reviewRecordWritesDB{KVDB: m.db, key: keys[1]}
	m.db = audit
	_, e = s.Put(dkvsValueMutation{Key: keys[0], Value: []byte("own"), Owner: m.wallet, Signature: dkvsSignatureAccount, Policy: dkvsStoragePolicy{FreeLocal: true, TTL: testRGB11FreeLocalTTL}})
	require.NoError(t, e)
	require.Positive(t, metadataReads)
	require.Zero(t, audit.recordWrites, "unchanged values must not be rewritten")
}

type reviewRecordWritesDB struct {
	indexer.KVDB
	key          string
	recordWrites int
}
type reviewRecordWritesBatch struct {
	indexer.WriteBatch
	owner *reviewRecordWritesDB
}

func (d *reviewRecordWritesDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return &reviewRecordWritesBatch{WriteBatch: d.KVDB.(indexer.AtomicWriteBatchDB).NewAtomicWriteBatch(), owner: d}
}
func (b *reviewRecordWritesBatch) Put(k, v []byte) error {
	if strings.HasPrefix(string(k), "dkvs:subscription-record:") && strings.HasSuffix(string(k), b.owner.key) {
		b.owner.recordWrites++
	}
	return b.WriteBatch.Put(k, v)
}

// Preserve existing fault semantics while forwarding the backend capability.
func (d *managedImportFaultDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return &managedImportFaultBatch{WriteBatch: d.KVDB.(indexer.AtomicWriteBatchDB).NewAtomicWriteBatch(), db: d}
}

type managedImportFaultBatch struct {
	indexer.WriteBatch
	db *managedImportFaultDB
}

func (b *managedImportFaultBatch) Put(key, value []byte) error {
	if bytes.Equal(key, b.db.key) && b.db.writeErr != nil {
		b.db.hits.Add(1)
		return b.db.writeErr
	}
	return b.WriteBatch.Put(key, value)
}
func (d *accountProfileWriteCountDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return d.KVDB.(indexer.AtomicWriteBatchDB).NewAtomicWriteBatch()
}
func (d *accountPersistenceReviewDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return &accountPersistenceReviewBatch{WriteBatch: d.KVDB.(indexer.AtomicWriteBatchDB).NewAtomicWriteBatch(), db: d}
}

// Startup/recovery may expose verified reads before old requests finish, but
// cannot grant a fresh intent for an unresolved key. Independent keys remain writable after verified online receiving.
func TestDKVSThreeFixRestartPendingReadDoesNotGrantWrites(t *testing.T) {
	m, r, _, s, keys := reviewContractFixture(t)
	r.postCommitErr = &net.OpError{Op: "read", Net: "tcp", Err: errors.New("lost ACK")}
	_, err := s.Put(dkvsValueMutation{Key: keys[0], Value: []byte("unknown"), Owner: m.wallet, Signature: dkvsSignatureAccount})
	require.Error(t, err)
	entries, err := core.NewReplicaStore(m.db).LoadOutbox(s.client.replicaNamespace)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	id := entries[0].RequestID
	releaseDKVSManagerRuntime(m.dkvs)
	m.dkvs = nil
	s, err = m.ensureDKVSManager().primaryStore()
	require.NoError(t, err)
	v, err := s.Get(keys[0])
	require.NoError(t, err)
	require.Equal(t, "unknown", string(v.Value))
	require.Error(t, m.dkvs.requireWritePaths(s.client, []string{keys[0]}), "the unresolved key must not accept a fresh intent")
	_, err = s.Put(dkvsValueMutation{Key: keys[0], Value: []byte("must not submit"), Owner: m.wallet, Signature: dkvsSignatureAccount})
	require.Error(t, err)
	require.NoError(t, m.dkvs.requireWritePaths(s.client, []string{keys[1]}), "verified receiving permits an independent key")
	_, err = s.Put(dkvsValueMutation{Key: keys[1], Value: []byte("independent"), Owner: m.wallet, Signature: dkvsSignatureAccount})
	require.NoError(t, err)
	entries, err = core.NewReplicaStore(m.db).LoadOutbox(s.client.replicaNamespace)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, id, entries[0].RequestID)
	_, err = m.syncDKVSOnce()
	require.NoError(t, err)
	require.NoError(t, m.dkvs.requireWritePaths(s.client, keys))
}

func TestDKVSThreeFixOfflineRecoveryBlocksLocalEdits(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	m, r, s, keys := accountTTLNoopFixture(t)
	key := accountTestKey(t, m, "pending-recovery/value")
	r.mu.Lock()
	r.postCommitErr = &net.OpError{Op: "read", Net: "tcp", Err: errors.New("lost ACK")}
	r.mu.Unlock()
	_, err := s.Put(dkvsValueMutation{Key: key, Value: []byte("unknown"), Owner: m.wallet, Signature: dkvsSignatureAccount})
	require.Error(t, err)
	// The offline premise covers verified receiving as well as replaying CAS;
	// rejecting only one submission still permits a healthy online baseline.
	offline := &net.DNSError{Err: "recovery unavailable", IsTimeout: true}
	h := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: r}
	h.get = func(context.Context, string, map[string]string) ([]byte, error) { return nil, offline }
	h.post = func(context.Context, string, []byte) ([]byte, error) { return nil, offline }
	s.client.Http = h
	_, _, receiveErr := m.syncDKVSOnceResult()
	require.ErrorIs(t, receiveErr, offline, "the actual receiver must observe the outage")
	require.ErrorIs(t, m.dkvs.requireOnlinePaths(s.client, keys), ErrDKVSPathNotSynced)
	before, err := m.db.Read(accountManagementProfileKey())
	require.NoError(t, err)
	id := m.GetCurrentWalletId()
	name := m.walletInfoMap[id].Name
	t.Run("rename", func(t *testing.T) { require.Error(t, m.UpdateWalletName(id, "blocked")) })
	t.Run("password", func(t *testing.T) { require.Error(t, m.ChangePassword("password", "must-not-change")) })
	after, err := m.db.Read(accountManagementProfileKey())
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
	require.Equal(t, name, m.walletInfoMap[id].Name)

	// Restore the same endpoint and reconcile the original signed request;
	// a normal successful account sync must permit local edits again.
	s.client.Http = r
	_, err = m.syncDKVSOnce()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, m.SyncAccountManagementState(ctx))
	require.NoError(t, m.dkvs.requireOnlinePaths(s.client, keys))
	require.NoError(t, m.UpdateWalletName(id, "recovered"))
	require.Equal(t, "recovered", m.walletInfoMap[id].Name)
}

func TestDKVSThreeFixMappingWriteVisibility(t *testing.T) {
	for _, mode := range []string{"record", "cas", "batch", "install_failure"} {
		t.Run(mode, func(t *testing.T) {
			m, r := finalDKVSTestManager(t)
			r.endpointID = hex.EncodeToString(m.wallet.GetPubKey().SerializeCompressed())
			h := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: r}
			configureRGB11DKVSTestManager(m, h)
			key, err := dkvs.AccountMappingKey(GetChainParam().Name, m.wallet.GetAddress())
			require.NoError(t, err)
			accountID, err := dkvsAccountID(m.wallet)
			require.NoError(t, err)
			value, err := dkvs.EncodeAccountServiceDescriptor(dkvs.AccountServiceDescriptor{AccountID: accountID, CoreNodeID: r.endpointID})
			require.NoError(t, err)
			record, err := NewDKVSAccountSignedRecord(m.wallet, key, value, dkvs.RecordOptions{Seq: 1, IssueHeight: 1})
			require.NoError(t, err)
			prefix, err := dkvs.CollectionPathForKey(key)
			require.NoError(t, err)
			require.NoError(t, m.SubscribeDKVSPrefix(prefix))
			_, err = m.syncDKVSOnce()
			require.NoError(t, err)
			c, err := m.GetDKVSClient()
			require.NoError(t, err)
			failInstall := mode == "install_failure"
			sends := 0
			h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
				if strings.Contains(path, "batch-cas") {
					sends++
				}
				if failInstall && strings.Contains(path, "active/sync") {
					return nil, &net.DNSError{Err: "mapping install unavailable", IsTimeout: true}
				}
				return r.SendDKVSPostContext(ctx, path, body)
			}
			switch mode {
			case "record":
				_, err = c.PutRecord(record)
			case "cas":
				_, err = c.PutRecordCAS(record, dkvs.WritePrecondition{ExpectAbsent: true})
			default:
				_, err = c.PutRecordBatchCAS([]dkvs.CASMutation{{Record: record, Precondition: dkvs.WritePrecondition{ExpectAbsent: true}}})
			}
			replica := core.NewReplicaStore(m.db)
			if failInstall {
				require.Error(t, err, "mapping ACK must wait for local installation")
				var pending *DKVSRequestPendingError
				require.ErrorAs(t, err, &pending)
				require.True(t, pending.Committed)
				require.True(t, pending.Durable)
				entries, e := replica.LoadOutbox(c.replicaNamespace)
				require.NoError(t, e)
				require.Len(t, entries, 1)
				require.Equal(t, DKVSOutboxAcknowledged, entries[0].State)
				failInstall = false
				_, err = m.syncDKVSOnce()
				require.NoError(t, err)
				require.Equal(t, 1, sends, "persisted mapping ACK cannot resend")
			} else {
				require.NoError(t, err)
			}
			local, err := c.GetRecord(key)
			require.NoError(t, err)
			require.Equal(t, dkvs.RecordHash(record), dkvs.RecordHash(local))
			entries, err := replica.LoadOutbox(c.replicaNamespace)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

// Retired TestDKVSThreeFixAccountStorageUsesLocalPair: a monolithic state/blob
// pair is not a 1.1 read transaction; each indexed module owns its baseline.
