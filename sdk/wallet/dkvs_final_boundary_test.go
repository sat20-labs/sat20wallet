package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	core "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func finalBoundaryBatchContainsKey(body []byte, key string) (bool, error) {
	var request DKVSBatchCASRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return false, err
	}
	for _, mutation := range request.Mutations {
		if mutation.Record != nil && mutation.Record.Key == key {
			return true, nil
		}
	}
	return false, nil
}

// Installed before fixture startup; faults affect real DB reads, not the
// production result or its ordering. Atomic publication remains the real DB's.
type finalBoundaryReadDB struct {
	indexer.KVDB
	mu        sync.Mutex
	readFault func([]byte) error
}

type finalBoundaryCleanupDB struct {
	indexer.KVDB
	mu                  sync.Mutex
	mode                string
	fault               error
	moduleValueDigest   atomic.Value
	moduleTarget        atomic.Value
	outboxTarget        atomic.Value
	hits                atomic.Uint32
	atomicModulePutHits atomic.Uint32
}

func (d *finalBoundaryCleanupDB) attach(db indexer.KVDB) indexer.KVDB { d.KVDB = db; return d }
func (d *finalBoundaryCleanupDB) arm(mode string, fault error) {
	d.mu.Lock()
	d.mode, d.fault = mode, fault
	d.mu.Unlock()
}
func (d *finalBoundaryCleanupDB) failOnce(mode string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.mode != mode {
		return nil
	}
	d.mode = ""
	d.hits.Add(1)
	return d.fault
}
func (d *finalBoundaryCleanupDB) Write(key, value []byte) error {
	return d.KVDB.Write(key, value)
}
func (d *finalBoundaryCleanupDB) Delete(key []byte) error {
	target, _ := d.outboxTarget.Load().([]byte)
	if bytes.HasPrefix(key, dkvsOutboxPrefix) && (len(target) == 0 || bytes.Equal(key, target)) {
		if err := d.failOnce("direct-delete"); err != nil {
			return err
		}
	}
	return d.KVDB.Delete(key)
}
func (d *finalBoundaryCleanupDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return &finalBoundaryCleanupBatch{WriteBatch: d.KVDB.(indexer.AtomicWriteBatchDB).NewAtomicWriteBatch(), owner: d}
}

type finalBoundaryCleanupBatch struct {
	indexer.WriteBatch
	owner        *finalBoundaryCleanupDB
	outboxDelete bool
}

func (b *finalBoundaryCleanupBatch) Put(key, value []byte) error {
	target, _ := b.owner.moduleValueDigest.Load().(string)
	targetModule, _ := b.owner.moduleTarget.Load().(string)
	var module accountLocalKV
	if target != "" && targetModule != "" && DecodeFromBytes(value, &module) == nil &&
		bytes.Equal(key, accountLocalKey(module.AccountID, module.Provider, module.Scope)) &&
		module.Provider == "wallets" && module.Scope == AccountManagedDataGlobalScope &&
		module.Key == targetModule &&
		accountStateDigest(module.Value) == target {
		b.owner.atomicModulePutHits.Add(1)
		if err := b.owner.failOnce("module-row-write"); err != nil {
			return err
		}
	}
	return b.WriteBatch.Put(key, value)
}

func (b *finalBoundaryCleanupBatch) Delete(key []byte) error {
	if bytes.HasPrefix(key, dkvsOutboxPrefix) {
		if err := b.owner.failOnce("batch-delete"); err != nil {
			return err
		}
		b.outboxDelete = true
	}
	return b.WriteBatch.Delete(key)
}
func (b *finalBoundaryCleanupBatch) Flush() error {
	if b.outboxDelete {
		if err := b.owner.failOnce("batch-flush"); err != nil {
			return err
		}
	}
	return b.WriteBatch.Flush()
}

func (d *finalBoundaryReadDB) attach(db indexer.KVDB) indexer.KVDB { d.KVDB = db; return d }
func (d *finalBoundaryReadDB) arm(fn func([]byte) error) {
	d.mu.Lock()
	d.readFault = fn
	d.mu.Unlock()
}
func (d *finalBoundaryReadDB) BatchRead(prefix []byte, reverse bool, fn func([]byte, []byte) error) error {
	d.mu.Lock()
	fault := d.readFault
	d.mu.Unlock()
	if fault != nil {
		if err := fault(prefix); err != nil {
			return err
		}
	}
	return d.KVDB.BatchRead(prefix, reverse, fn)
}
func (d *finalBoundaryReadDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return d.KVDB.(indexer.AtomicWriteBatchDB).NewAtomicWriteBatch()
}

func TestDKVSFinalACKCleanupReadFailureRetainsRecovery(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	db := &finalBoundaryReadDB{}
	m, r, s, _ := accountTTLNoopFixture(t, db.attach)
	key := accountTestKey(t, m, "final-ack/value")
	h := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: r}
	s.client.Http = h
	sends := 0
	h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if strings.Contains(path, "batch-cas") {
			matches, err := finalBoundaryBatchContainsKey(body, key)
			if err != nil {
				return nil, err
			}
			if matches {
				sends++
			}
		}
		return r.SendDKVSPostContext(ctx, path, body)
	}
	injected := errors.New("injected reconciliation outbox read failure")
	var once sync.Once
	fired := false
	db.arm(func(prefix []byte) error {
		if !bytes.HasPrefix(prefix, dkvsOutboxPrefix) {
			return nil
		}
		record, err := core.NewReplicaStore(db.KVDB).LoadSubscriptionRecord(s.client.replicaNamespace, key)
		if err != nil || string(record.Value) != "installed" {
			return nil
		}
		var fault error
		once.Do(func() { fired = true; fault = injected })
		return fault
	})
	_, err := s.Put(dkvsValueMutation{Key: key, Value: []byte("installed"), Owner: m.wallet, Signature: dkvsSignatureAccount, Policy: dkvsStoragePolicy{FreeLocal: true, TTL: testRGB11FreeLocalTTL}})
	require.True(t, fired, "fault must occur after actual local publication")
	require.ErrorIs(t, err, injected)
	var pending *DKVSRequestPendingError
	require.ErrorAs(t, err, &pending)
	require.True(t, pending.Committed)
	require.True(t, pending.Durable)
	replica := core.NewReplicaStore(m.db)
	entries, err := replica.LoadOutbox(s.client.replicaNamespace)
	require.NoError(t, err)
	require.Empty(t, entries, "this fault occurs after the ACK and outbox deletion commit")
	local, err := s.Get(key)
	require.NoError(t, err)
	require.Equal(t, "installed", string(local.Value))
	_, err = m.syncDKVSOnce()
	require.NoError(t, err)
	require.Equal(t, 1, sends, "recovery must install/clean ACK without another submission")
	entries, err = replica.LoadOutbox(s.client.replicaNamespace)
	require.NoError(t, err)
	require.Empty(t, entries)
	require.NoError(t, m.dkvs.requireWritePaths(s.client, []string{key}))
}

func TestDKVSFinalACKCleanupReadFailureBackgroundRetriesWithoutWatchChange(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	db := &finalBoundaryReadDB{}
	m, remote, store, _ := accountTTLNoopFixture(t, db.attach)
	key := accountTestKey(t, m, "final-ack-background/value")
	remote.postCommitErr = &net.OpError{Op: "read", Net: "tcp", Err: errors.New("lost first ACK")}
	_, err := store.Put(dkvsValueMutation{Key: key, Value: []byte("installed"), Owner: m.wallet,
		Signature: dkvsSignatureAccount, Policy: dkvsStoragePolicy{FreeLocal: true, TTL: testRGB11FreeLocalTTL}})
	var pending *DKVSRequestPendingError
	require.ErrorAs(t, err, &pending)

	injected := errors.New("one-shot local ACK cleanup read failure")
	var once sync.Once
	var fired atomic.Bool
	db.arm(func(prefix []byte) error {
		if !bytes.HasPrefix(prefix, dkvsOutboxPrefix) {
			return nil
		}
		replica := core.NewReplicaStore(db.KVDB)
		record, readErr := replica.LoadSubscriptionRecord(store.client.replicaNamespace, key)
		if readErr != nil || string(record.Value) != "installed" {
			return nil
		}
		entries, readErr := replica.LoadOutbox(store.client.replicaNamespace)
		if readErr != nil || len(entries) != 0 {
			return nil
		}
		var fault error
		once.Do(func() { fired.Store(true); fault = injected })
		return fault
	})
	require.NoError(t, m.StartDKVSSync())
	t.Cleanup(m.StopDKVSSync)
	require.Eventually(t, fired.Load, 5*time.Second, 25*time.Millisecond,
		"the local cleanup fault must occur in the background worker")
	require.Eventually(t, func() bool {
		entries, readErr := core.NewReplicaStore(m.db).LoadOutbox(store.client.replicaNamespace)
		return readErr == nil && len(entries) == 0 && m.dkvs.requireWritePaths(store.client, []string{key}) == nil
	}, 12*time.Second, 25*time.Millisecond, "the worker must finish the same durable ACK after a local failure without a watch event")
}

func TestDKVSFinalACKDeleteFailureBackgroundRetriesWithoutWatchChange(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	db := &finalBoundaryCleanupDB{}
	m, _, store, _ := accountTTLNoopFixture(t, db.attach)
	key := accountTestKey(t, m, "final-ack-delete/value")
	fault := errors.New("one-shot outbox deletion failure")
	db.arm("direct-delete", fault)
	_, err := store.Put(dkvsValueMutation{Key: key, Value: []byte("installed"), Owner: m.wallet,
		Signature: dkvsSignatureAccount, Policy: dkvsStoragePolicy{FreeLocal: true, TTL: testRGB11FreeLocalTTL}})
	require.ErrorIs(t, err, fault)
	var pending *DKVSRequestPendingError
	require.ErrorAs(t, err, &pending)
	require.True(t, pending.Committed)
	require.True(t, pending.Durable)
	replica := core.NewReplicaStore(m.db)
	entries, err := replica.LoadOutbox(store.client.replicaNamespace)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, DKVSOutboxAcknowledged, entries[0].State)
	require.NoError(t, m.StartDKVSSync())
	t.Cleanup(m.StopDKVSSync)
	require.Eventually(t, func() bool {
		entries, readErr := replica.LoadOutbox(store.client.replicaNamespace)
		return readErr == nil && len(entries) == 0 && m.dkvs.requireWritePaths(store.client, []string{key}) == nil
	}, 12*time.Second, 25*time.Millisecond, "a durable ACK needs a bounded local cleanup retry without a new watch event")
}

// The module row owns ACK confirmation. This injects a real atomic Put failure
// before the module's confirmed watermark can move. That storage session must
// close; a fresh Manager finalizes the durable ACK without retransmission.
// An outbox-only Delete failure retains its existing automatic retry behavior.
func TestAccountManagedACKBatchFailureBackgroundRetriesWithoutWatchChange(t *testing.T) {
	for _, mode := range []string{"module-row-write", "direct-delete"} {
		t.Run(mode, func(t *testing.T) {
			old := _chain
			_chain = "testnet"
			defer func() { _chain = old }()
			db := &finalBoundaryCleanupDB{}
			m, remote, store, keys := accountTTLNoopFixture(t, db.attach)
			const name = "Pending ACK cleanup"
			require.NoError(t, m.UpdateWalletName(m.GetCurrentWalletId(), name))
			accountID := m.GetAccountManagementStatus().AccountID
			localModule, err := m.readAccountLocal(accountID, "wallets", AccountManagedDataGlobalScope)
			require.NoError(t, err)
			require.NotNil(t, localModule)
			require.NotNil(t, localModule.Baseline)
			moduleGeneration := localModule.Generation
			previousConfirmed := localModule.Confirmed
			previousBaseline := cloneDKVSRecord(localModule.Baseline)
			fault := errors.New("one-shot account ACK " + mode + " failure")
			var arm atomic.Bool
			arm.Store(true)
			var requestID, originalBody, targetHash atomic.Value
			var attempts atomic.Int32
			var moduleRequests, casRequests atomic.Int32
			var changed atomic.Bool
			http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
			http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
				if path != "/v3/dkvs/records/batch-cas" {
					return remote.SendDKVSPostContext(ctx, path, body)
				}
				casRequests.Add(1)
				var request DKVSBatchCASRequest
				if err := json.Unmarshal(body, &request); err != nil {
					return nil, err
				}
				selected := false
				hasModule := false
				for _, mutation := range request.Mutations {
					if mutation.Record != nil && mutation.Record.Key == keys[0] {
						hasModule = true
					}
					if mutation.Record != nil && mutation.Record.Key == keys[0] && arm.CompareAndSwap(true, false) {
						selected = true
						requestID.Store(request.RequestID)
						originalBody.Store(bytes.Clone(body))
						targetHash.Store(accountStateDigest(mutation.Record.Value))
					}
				}
				if hasModule {
					moduleRequests.Add(1)
				}
				if id, _ := requestID.Load().(string); id != "" && request.RequestID == id {
					attempts.Add(1)
					original, _ := originalBody.Load().([]byte)
					if !bytes.Equal(original, body) {
						changed.Store(true)
					}
				}
				response, err := remote.SendDKVSPostContext(ctx, path, body)
				if selected && err == nil {
					hash, _ := targetHash.Load().(string)
					db.moduleValueDigest.Store(hash)
					db.moduleTarget.Store(keys[0])
					db.outboxTarget.Store(dkvsOutboxKey(store.client.replicaNamespace, request.RequestID))
					// Arm only after the real server accepted this exact N.
					db.arm(mode, fault)
				}
				return response, err
			}
			store.client.Http = http
			err = m.SyncAccountManagementState(context.Background())
			require.Equal(t, uint32(1), db.hits.Load(), "the actual ACK persistence boundary must hit its one-shot fault")
			require.ErrorIs(t, err, fault)
			var pending *DKVSRequestPendingError
			require.ErrorAs(t, err, &pending)
			require.True(t, pending.Committed)
			require.True(t, pending.Durable)
			client, err := m.dkvs.primaryClient()
			require.NoError(t, err)
			replica := core.NewReplicaStore(m.db)
			entries, err := replica.LoadOutbox(client.replicaNamespace)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.Equal(t, DKVSOutboxAcknowledged, entries[0].State)
			id, _ := requestID.Load().(string)
			hash, _ := targetHash.Load().(string)
			require.NotEmpty(t, id)
			require.True(t, entries[0].RequestID == id, "the retained ACK is not the exact original N")
			require.Equal(t, accountKVOutboxDomain, entries[0].OriginDomain)
			require.Equal(t, keys[0], entries[0].OriginKey)
			require.Equal(t, moduleGeneration, entries[0].OriginGeneration)
			require.Equal(t, int32(1), attempts.Load())
			require.False(t, changed.Load())
			moduleAfterFault, err := m.readAccountLocal(accountID, localModule.Provider, localModule.Scope)
			require.NoError(t, err)
			require.NotNil(t, moduleAfterFault)
			if mode == "module-row-write" {
				require.Equal(t, uint32(1), db.atomicModulePutHits.Load(),
					"the one-shot fault must be reached by the actual atomic module-row Put")
				require.Equal(t, moduleGeneration, moduleAfterFault.Generation)
				require.Equal(t, moduleGeneration, moduleAfterFault.Captured)
				require.Equal(t, previousConfirmed, moduleAfterFault.Confirmed,
					"failed atomic receipt Put must not advance the module's confirmed watermark")
				require.Equal(t, dkvs.RecordHash(previousBaseline), dkvs.RecordHash(moduleAfterFault.Baseline),
					"failed atomic receipt Put must retain the last confirmed remote baseline")
				require.Equal(t, hash, accountStateDigest(moduleAfterFault.Value),
					"the local captured module N must remain available for retry")
			} else {
				require.Equal(t, moduleGeneration, moduleAfterFault.Generation)
				require.Equal(t, moduleGeneration, moduleAfterFault.Confirmed,
					"durable ACK cleanup failure must not roll back the module receipt")
				require.NotNil(t, moduleAfterFault.Baseline)
				require.Equal(t, hash, accountStateDigest(moduleAfterFault.Baseline.Value))
			}
			require.Equal(t, name, m.GetWalletCatalog()[0].Name, "ACK persistence lost the local business state N")
			accepted := snapshotRGB11MemoryRecords(remote)
			require.NotNil(t, accepted[keys[0]])
			require.True(t, accountStateDigest(accepted[keys[0]].Value) == hash)
			if mode == "module-row-write" {
				require.ErrorIs(t, m.StartDKVSSync(), ErrAccountDBStopped)
				database, service := m.db, m.serverNode
				m.Close()
				m = newAccountManagementAutoTestManager(t)
				m.db, m.serverNode = database, service
				configureRGB11DKVSTestManager(m, http)
				require.NoError(t, m.initDB())
				_, err = m.UnlockWallet("password")
				require.NoError(t, err)
				client, err = m.dkvs.primaryClient()
				require.NoError(t, err)
				replica = core.NewReplicaStore(m.db)
			}
			require.NoError(t, m.StartDKVSSync())
			t.Cleanup(m.StopDKVSSync)
			require.Eventually(t, func() bool {
				entries, readErr := replica.LoadOutbox(client.replicaNamespace)
				status := m.GetAccountManagementStatus()
				module, moduleErr := m.readAccountLocal(accountID, localModule.Provider, localModule.Scope)
				return readErr == nil && len(entries) == 0 && status.DKVSWriteAllowed && moduleErr == nil &&
					module != nil && module.Generation == module.Confirmed && module.Baseline != nil &&
					accountStateDigest(module.Baseline.Value) == hash
			}, 12*time.Second, 25*time.Millisecond, "the worker must finalize the module receipt and remove only the already durable ACK")
			moduleAfterCleanup, err := m.readAccountLocal(accountID, localModule.Provider, localModule.Scope)
			require.NoError(t, err)
			require.Equal(t, moduleGeneration, moduleAfterCleanup.Generation)
			require.Equal(t, moduleGeneration, moduleAfterCleanup.Confirmed)
			require.Equal(t, hash, accountStateDigest(moduleAfterCleanup.Baseline.Value))
			t.Logf("ACK module recovery: mode=%s generation=%d confirmed=%d captured=%d baseline_seq=%d original_attempts=%d original_changed=%t module_requests=%d extra_module_requests=%d batch_cas_requests=%d fault_hits=%d",
				mode, moduleAfterCleanup.Generation, moduleAfterCleanup.Confirmed, moduleAfterCleanup.Captured,
				moduleAfterCleanup.Baseline.Seq, attempts.Load(), changed.Load(), moduleRequests.Load(), moduleRequests.Load()-attempts.Load(), casRequests.Load(), db.hits.Load())
			serverNow := snapshotRGB11MemoryRecords(remote)
			for i, role := range []string{"wallet-module", "index"} {
				n, now := accepted[keys[i]], serverNow[keys[i]]
				var acceptedSeq, currentSeq uint64
				sameRecord, sameValue := false, false
				if n != nil {
					acceptedSeq = n.Seq
				}
				if now != nil {
					currentSeq = now.Seq
				}
				if n != nil && now != nil {
					sameRecord = dkvs.RecordHash(n) == dkvs.RecordHash(now)
					sameValue = bytes.Equal(n.Value, now.Value)
				}
				t.Logf("ACK server diagnostic: mode=%s role=%s accepted_present=%t current_present=%t accepted_seq=%d current_seq=%d hash_is_accepted_N=%t value_is_accepted_N=%t",
					mode, role, n != nil, now != nil, acceptedSeq, currentSeq, sameRecord, sameValue)
			}
			require.Equal(t, moduleAfterCleanup.Generation, moduleAfterCleanup.Confirmed)
			require.Equal(t, name, m.GetWalletCatalog()[0].Name)
			current := snapshotRGB11MemoryRecords(remote)
			for _, key := range keys[:2] {
				require.NotNil(t, accepted[key])
				require.NotNil(t, current[key])
				require.True(t, dkvs.RecordHash(accepted[key]) == dkvs.RecordHash(current[key]), "ACK cleanup rewrote the accepted remote N")
			}
			require.Equal(t, uint32(1), db.hits.Load())
			require.Equal(t, int32(1), attempts.Load(), "the original acknowledged request was resubmitted")
			require.False(t, changed.Load(), "the original signed request bytes changed")
		})
	}
}

func TestDKVSFinalACKReadAfterOutboxDeletionRetries(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	db := &finalBoundaryReadDB{}
	m, remote, store, _ := accountTTLNoopFixture(t, db.attach)
	key := accountTestKey(t, m, "final-ack-after-delete/value")
	http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	store.client.Http = http
	sends := 0
	http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if strings.Contains(path, "batch-cas") {
			matches, err := finalBoundaryBatchContainsKey(body, key)
			if err != nil {
				return nil, err
			}
			if matches {
				sends++
			}
		}
		return remote.SendDKVSPostContext(ctx, path, body)
	}
	fault := errors.New("one-shot post-deletion outbox read failure")
	var once sync.Once
	fired := false
	db.arm(func(prefix []byte) error {
		if !bytes.HasPrefix(prefix, dkvsOutboxPrefix) {
			return nil
		}
		entries, readErr := core.NewReplicaStore(db.KVDB).LoadOutbox(store.client.replicaNamespace)
		if readErr != nil || len(entries) != 0 || sends == 0 {
			return nil
		}
		var err error
		once.Do(func() { fired = true; err = fault })
		return err
	})
	_, err := store.Put(dkvsValueMutation{Key: key, Value: []byte("installed"), Owner: m.wallet,
		Signature: dkvsSignatureAccount, Policy: dkvsStoragePolicy{FreeLocal: true, TTL: testRGB11FreeLocalTTL}})
	require.True(t, fired)
	var pending *DKVSRequestPendingError
	require.ErrorAs(t, err, &pending)
	require.True(t, pending.Durable)
	entries, err := core.NewReplicaStore(m.db).LoadOutbox(store.client.replicaNamespace)
	require.NoError(t, err)
	require.Empty(t, entries, "ACK and outbox deletion have already committed")
	m.dkvs.markNotReady(m.dkvs.sessionKey(store.client, "write"))
	_, err = m.syncDKVSOnce()
	require.NoError(t, err)
	require.NoError(t, m.dkvs.requireWritePaths(store.client, []string{key}))
	require.Equal(t, 1, sends, "the original remote write must not be replayed")
}

func TestDKVSOutboxTransientLocalReadKeepsExactRequestsForRetry(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	db := &finalBoundaryReadDB{}
	m, _, store, _ := accountTTLNoopFixture(t, db.attach)
	fault := errors.New("one-shot local outbox read failure")
	var once sync.Once
	db.arm(func(prefix []byte) error {
		if !bytes.HasPrefix(prefix, dkvsOutboxPrefix) {
			return nil
		}
		var err error
		once.Do(func() { err = fault })
		return err
	})
	replica := core.NewReplicaStore(m.db)
	var readErr error
	require.NotPanics(t, func() { _, readErr = m.flushDKVSBatchOutbox(store.client, replica) })
	var localRead *dkvsLocalOutboxReadError
	require.ErrorAs(t, readErr, &localRead)
	require.ErrorIs(t, readErr, fault)
	_, readErr = m.flushDKVSBatchOutbox(store.client, replica)
	require.NoError(t, readErr)
}

func TestDKVSConflictOutboxKeepsIndependentKeysWritable(t *testing.T) {
	m, remote, _, store, keys := reviewContractFixture(t)
	replica := core.NewReplicaStore(m.db)
	previous, err := finalDKVSSeedClient(m, remote).GetRecordDirect(keys[0])
	require.NoError(t, err)
	previousHash := dkvs.RecordHash(previous)
	record := freeLocalRecord(t, m, keys[0], 2, "blocked")
	entry, err := newDKVSBatchOutboxEntryFinal(store.client.replicaNamespace,
		[]dkvs.CASMutation{{Record: record, Precondition: dkvs.WritePrecondition{ExpectedHash: &previousHash}}},
		remote.endpointID, dkvsOutboxOrigin{})
	require.NoError(t, err)
	require.NoError(t, replica.QueueOutbox(entry))
	require.NoError(t, replica.UpdateOutboxState(entry, DKVSOutboxConflict, dkvs.ErrWriteConflict))

	remote.postCommitErr = &net.OpError{Op: "read", Net: "tcp", Err: errors.New("lost independent ACK")}
	_, err = store.Put(dkvsValueMutation{Key: keys[1], Value: []byte("independent"),
		Owner: m.wallet, Signature: dkvsSignatureAccount, Policy: dkvsStoragePolicy{FreeLocal: true, TTL: testRGB11FreeLocalTTL}})
	var pending *DKVSRequestPendingError
	require.ErrorAs(t, err, &pending)
	submitted, err := m.flushDKVSBatchOutbox(store.client, replica)
	require.True(t, submitted)
	require.ErrorIs(t, err, dkvs.ErrWriteConflict, "the blocked key must remain visible")
	entries, err := replica.LoadOutbox(store.client.replicaNamespace)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, entry.RequestID, entries[0].RequestID)
	require.Error(t, m.dkvs.requireWritePaths(store.client, []string{keys[0]}))
	require.NoError(t, m.dkvs.requireWritePaths(store.client, []string{keys[1]}))
	sameCollectionKey := accountTestKey(t, m, "review-a/other")
	require.NoError(t, m.dkvs.requireWritePaths(store.client, []string{sameCollectionKey}),
		"a different key in the same collection is independent")
}

func TestDKVSUnresolvedOutboxQualifiesOnlyIndependentKeys(t *testing.T) {
	m, remote, _, store, keys := reviewContractFixture(t)
	replica := core.NewReplicaStore(m.db)
	previous, err := finalDKVSSeedClient(m, remote).GetRecordDirect(keys[0])
	require.NoError(t, err)
	hash := dkvs.RecordHash(previous)
	entry, err := newDKVSBatchOutboxEntryFinal(store.client.replicaNamespace,
		[]dkvs.CASMutation{{Record: freeLocalRecord(t, m, keys[0], previous.Seq+1, "unresolved"),
			Precondition: dkvs.WritePrecondition{ExpectedHash: &hash}}}, remote.endpointID, dkvsOutboxOrigin{})
	require.NoError(t, err)
	for _, state := range []string{DKVSOutboxPending, DKVSOutboxInflight, DKVSOutboxAcknowledged} {
		t.Run(state, func(t *testing.T) {
			require.NoError(t, replica.QueueOutbox(entry))
			require.NoError(t, replica.UpdateOutboxState(entry, state, nil))
			m.dkvs.markNotReady(m.dkvs.sessionKey(store.client, "write"))
			require.NoError(t, m.dkvs.grantReconciledWriteSession(store.client, replica))
			require.Error(t, m.dkvs.requireWritePaths(store.client, []string{keys[0]}))
			require.NoError(t, m.dkvs.requireWritePaths(store.client, []string{keys[1]}))
			require.NoError(t, replica.DiscardOutbox(entry))
		})
	}
}

func TestDKVSOutboxTransientResponseContinuesIndependentRequest(t *testing.T) {
	m, remote, http, store, keys := reviewContractFixture(t)
	replica := core.NewReplicaStore(m.db)
	for i, key := range keys {
		previous, err := finalDKVSSeedClient(m, remote).GetRecordDirect(key)
		require.NoError(t, err)
		hash := dkvs.RecordHash(previous)
		mutations := []dkvs.CASMutation{{Record: freeLocalRecord(t, m, key, previous.Seq+1, "updated"),
			Precondition: dkvs.WritePrecondition{ExpectedHash: &hash}}}
		entry, err := newDKVSBatchOutboxEntryFinal(store.client.replicaNamespace, mutations, remote.endpointID, dkvsOutboxOrigin{})
		require.NoError(t, err)
		entry.CreatedAtMS = uint64(i + 1)
		entry.Authorization, err = store.client.WithWriteSigner(m.wallet).prepareWriteAuthorization(mutations, entry.EndpointID, entry.RequestID)
		require.NoError(t, err)
		require.NoError(t, replica.QueueOutbox(entry))
	}
	requestCount := 0
	http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if strings.Contains(path, "batch-cas") {
			requestCount++
			if requestCount == 1 {
				return nil, &HTTPResponseError{StatusCode: 503}
			}
		}
		return remote.SendDKVSPostContext(ctx, path, body)
	}
	submitted, err := m.flushDKVSBatchOutbox(store.client, replica)
	require.True(t, submitted)
	require.Error(t, err)
	require.Equal(t, 2, requestCount)
	entries, err := replica.LoadOutbox(store.client.replicaNamespace)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	mutations, err := entries[0].DecodeMutations()
	require.NoError(t, err)
	require.Equal(t, keys[0], mutations[0].Record.Key)
	actual, err := finalDKVSSeedClient(m, remote).GetRecordDirect(keys[1])
	require.NoError(t, err)
	require.Equal(t, "updated", string(actual.Value))
}

func TestDKVSAccountKeyACKIsolation(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	_, _, _, keys := accountTTLNoopFixture(t)
	require.GreaterOrEqual(t, len(keys), 3)
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		dependency := dkvsOutboxDependencyKey(key)
		require.Equal(t, key, dependency)
		require.False(t, seen[dependency], "independent account keys must retain separate ACK dependencies")
		seen[dependency] = true
	}
}

func TestDKVSAccountBackupJobCoalescesWithinBoundedDeadline(t *testing.T) {
	m := &dkvsManager{jobs: make(map[string]func(*dkvsStore) error),
		jobDue: make(map[string]time.Time), jobDeadline: make(map[string]time.Time)}
	runs := 0
	job := func(*dkvsStore) error { runs++; return nil }
	m.scheduleCoalesced(accountManagedStateJobID, accountManagedBackupQuiet, accountManagedBackupMaximum, job)
	firstDue, deadline := m.jobDue[accountManagedStateJobID], m.jobDeadline[accountManagedStateJobID]
	require.False(t, firstDue.IsZero())
	require.False(t, deadline.IsZero())
	require.True(t, firstDue.Before(deadline))
	m.scheduleCoalesced(accountManagedStateJobID, accountManagedBackupQuiet, accountManagedBackupMaximum, job)
	require.Equal(t, deadline, m.jobDeadline[accountManagedStateJobID], "new edits must not extend the maximum delay")
	require.False(t, m.jobDue[accountManagedStateJobID].Before(firstDue))
	require.False(t, m.jobDue[accountManagedStateJobID].After(deadline))
	require.NoError(t, m.runPendingJobs(&dkvsStore{}))
	require.Zero(t, runs, "a dirty local commit must not start immediate network publication")
	m.mu.Lock()
	m.jobDue[accountManagedStateJobID] = time.Now().Add(-time.Second)
	m.mu.Unlock()
	require.NoError(t, m.runPendingJobs(&dkvsStore{}))
	require.Equal(t, 1, runs)
	require.Empty(t, m.jobs)
}

func TestDKVSFinalRGBFreshness(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	for _, mode := range []string{"sync_unavailable", "remote_changed", "profile_changed_during_sync", "endpoint_changed_during_sync"} {
		t.Run(mode, func(t *testing.T) {
			db := &finalBoundaryReadDB{}
			m, r, s, keys := accountTTLNoopFixture(t, db.attach)
			m.accountBackgroundWG.Wait()
			require.NoError(t, m.verifyCurrentRGB11RecoveryClosure(), "baseline must be genuinely confirmed")
			h := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: r}
			s.client.Http = h
			hooked := false
			h.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
				if strings.Contains(path, "active/sync") && mode == "sync_unavailable" {
					hooked = true
					return nil, &net.DNSError{Err: "active receive unavailable; config remains available", IsTimeout: true}
				}
				hooked = true
				return r.SendDKVSPostContext(ctx, path, body)
			}
			rgbKey := ""
			// Profile/endpoint races occur on the actual verified local read,
			// not an HTTP refresh which the business gate no longer performs.
			if mode == "profile_changed_during_sync" || mode == "endpoint_changed_during_sync" {
				db.arm(func([]byte) error {
					stack := make([]byte, 16<<10)
					if !strings.Contains(string(stack[:runtime.Stack(stack, false)]), ".verifyCurrentRGB11RecoveryClosure(") || hooked {
						return nil
					}
					hooked = true
					if mode == "profile_changed_during_sync" {
						m.mutex.Lock()
						m.bumpAccountGenerationLocked()
						m.mutex.Unlock()
					} else {
						r.mu.Lock()
						r.endpointID = "changed-source"
						r.mu.Unlock()
					}
					return nil
				})
			}
			if mode == "remote_changed" {
				seed := finalDKVSSeedClient(m, r)
				modules, err := m.listAccountLocal(m.GetAccountManagementStatus().AccountID)
				require.NoError(t, err)
				var rgbModule *accountLocalKV
				for _, module := range modules {
					if module.Provider == rgb11AccountManagedProviderID && !module.Deleted {
						rgbModule = module
						break
					}
				}
				require.NotNil(t, rgbModule, "freshness mutation must target an actual RGB11 module")
				require.NotNil(t, rgbModule.Baseline)
				rgbKey = rgbModule.Key
				previous, err := seed.GetRecordDirect(rgbModule.Key)
				require.NoError(t, err)
				hash := dkvs.RecordHash(previous)
				updated, err := seed.PutRecordCAS(freeLocalRecord(t, m, rgbModule.Key, previous.Seq+1, "different remote state"), dkvs.WritePrecondition{ExpectedHash: &hash})
				require.NoError(t, err)
				require.NotEqual(t, dkvs.RecordHash(rgbModule.Baseline).String(), dkvs.RecordHash(updated).String(),
					"the selected RGB11 module must have a different authoritative baseline")
			}
			if mode == "remote_changed" {
				require.NotEmpty(t, rgbKey)
				require.NoError(t, s.SyncCurrent(rgbKey))
			}
			if mode == "sync_unavailable" {
				// The background receive discovers offline state before business.
				require.Error(t, s.SyncCurrent(keys...))
				externalCalls := 0
				_, err := runRGB11ManagedOperationWithManager(m, context.Background(), rgb11ManagedOperationNew,
					func() (*rgb11Manager, error) { return m.rgbManager, nil },
					func(*rgb11Manager) (int, error) { externalCalls++; return 1, nil })
				require.Error(t, err, "unverified remote closure admitted %d business callbacks", externalCalls)
				require.Zero(t, externalCalls)
				require.True(t, hooked)
				require.Error(t, m.dkvs.requireOnlinePaths(s.client, keys))
				values, err := m.listAccountLocal(m.accountProfile.AccountID)
				require.NoError(t, err)
				require.NotEmpty(t, values)
				for _, value := range values {
					if value.Provider == "account" || value.Deleted {
						continue
					}
					entry, err := accountEntry(value.AccountID, value.Provider, value.Scope)
					require.NoError(t, err)
					payload, err := openAccountPayload(m.accountSecret, value.AccountID, m.cfg.Chain, entry, value.Value)
					require.NoError(t, err, "offline important local copy remains readable")
					zeroBytes(payload)
				}
			} else {
				require.Error(t, m.verifyCurrentRGB11RecoveryClosure(), "changed freshness/scope must be rejected")
				require.True(t, hooked, "change must occur on the real synchronization path")
			}
		})
	}
}

func TestDKVSFinalRGBMailboxFailureDoesNotBlockLocalBusiness(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	db := &finalBoundaryReadDB{}
	m, _, _, _ := accountTTLNoopFixture(t, db.attach)
	require.NoError(t, m.RegisterAccountManagedDataProvider(&accountManagedActiveDataProviderStub{accountManagedDataProviderStub: accountManagedDataProviderStub{id: "final-empty"}}))
	require.NoError(t, m.verifyCurrentRGB11RecoveryClosure())
	maintenanceErr := errors.New("injected optional mailbox cache read failure")
	reads := 0
	db.arm(func(prefix []byte) error {
		stack := make([]byte, 16<<10)
		if bytes.HasPrefix(prefix, []byte("dkvs:subscription-record:")) &&
			strings.Contains(string(stack[:runtime.Stack(stack, false)]), ".prepareAccountManagedActiveDataImport(") {
			reads++
			return maintenanceErr
		}
		return nil
	})
	// Report the failed mailbox import honestly, but don't make unrelated
	// local RGB business depend on this optional provider's cache.
	require.ErrorIs(t, m.importAccountManagedActiveDataForSync(false), maintenanceErr)
	require.Positive(t, reads, "must reach real trailing mailbox maintenance")
	require.NoError(t, m.verifyCurrentRGB11RecoveryClosure())
	calls := 0
	_, err := runRGB11ManagedOperationWithManager(m, context.Background(), rgb11ManagedOperationNew,
		func() (*rgb11Manager, error) { return m.rgbManager, nil },
		func(*rgb11Manager) (int, error) { calls++; return 1, nil })
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}
