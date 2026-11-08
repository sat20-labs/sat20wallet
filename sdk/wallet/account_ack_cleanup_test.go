package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

var errAccountACKDelete = errors.New("injected exact ACK outbox Delete failure")

type accountACKDeleteDB struct {
	indexer.KVDB
	target atomic.Value
	fail   atomic.Bool
	hits   atomic.Uint32
}

func (d *accountACKDeleteDB) Delete(key []byte) error {
	target, _ := d.target.Load().([]byte)
	if d.fail.Load() && bytes.Equal(key, target) {
		d.hits.Add(1)
		return errAccountACKDelete
	}
	return d.KVDB.Delete(key)
}
func (d *accountACKDeleteDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return d.KVDB.(indexer.AtomicWriteBatchDB).NewAtomicWriteBatch()
}

func TestAccountACKDeleteKeepsWrite(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	remote := newRGB11MemoryDKVSHTTP()
	database := &accountACKDeleteDB{KVDB: indexerdb.NewKVDB(t.TempDir())}
	defer database.fail.Store(false)
	transport := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	var arm, holdReceive atomic.Bool
	var moduleKey, originalID, namespace atomic.Value
	var originalAttempts atomic.Int32
	accepted := make(chan DKVSBatchCASRequest, 1)
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	transport.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if path == "/v3/dkvs/active/sync" && holdReceive.Load() {
			enterOnce.Do(func() { close(entered) })
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		var request DKVSBatchCASRequest
		selected := false
		if path == "/v3/dkvs/records/batch-cas" {
			if err := json.Unmarshal(body, &request); err != nil {
				return nil, err
			}
			key, _ := moduleKey.Load().(string)
			for _, mutation := range request.Mutations {
				if mutation.Record != nil && mutation.Record.Key == key && arm.CompareAndSwap(true, false) {
					selected = true
					originalID.Store(request.RequestID)
					break
				}
			}
			if id, _ := originalID.Load().(string); id != "" && request.RequestID == id {
				originalAttempts.Add(1)
			}
		}
		response, err := remote.SendDKVSPostContext(ctx, path, body)
		if selected && err == nil {
			ns, _ := namespace.Load().(string)
			database.target.Store(dkvsOutboxKey(ns, request.RequestID))
			database.fail.Store(true)
			accepted <- request
		}
		return response, err
	}
	manager := coreNewE2EManagerWithDB(t, coreE2EConfig{
		Core: AccountIndexerLocation{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"},
	}, newCoreE2EChain(), database, "", transport)
	useRGB11TestCoreAuthority(t, manager)
	node := newRGB11MessageNodeClient(remote)
	manager.serverNode = NewNode(node, "message.test", SERVER_NODE, node.CoreNodePubKey(), node.CoreNodePubKey())
	t.Cleanup(func() { manager.Close(); require.NoError(t, database.Close()) })
	defer unblock()
	activateRGB11AccountScopeRecovery(t, manager, coreE2ESenderMnemonic)
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 15*time.Second)
	require.NoError(t, manager.WaitAccountManagedDataReady(readyCtx))
	readyCancel()
	accountID := manager.GetAccountManagementStatus().AccountID
	entry, err := accountEntry(accountID, "wallets", AccountManagedDataGlobalScope)
	require.NoError(t, err)
	require.NotEmpty(t, entry.Key, "the actual first backup must publish a canonical wallet module key")
	module, err := manager.readAccountLocal(accountID, "wallets", AccountManagedDataGlobalScope)
	require.NoError(t, err)
	require.NotNil(t, module)
	require.Equal(t, entry.Key, module.Key)
	require.NotNil(t, module.Baseline)
	require.Equal(t, entry.Key, module.Baseline.Key)
	moduleKey.Store(entry.Key)
	client, err := manager.dkvs.primaryClient()
	require.NoError(t, err)
	namespace.Store(client.replicaNamespace)
	require.Eventually(t, func() bool {
		entries, err := newDKVSReplicaStore(manager.db).LoadOutbox(client.replicaNamespace)
		return err == nil && len(entries) == 0
	}, 5*time.Second, 10*time.Millisecond)
	catalog := manager.GetWalletCatalog()
	require.NotEmpty(t, catalog)
	arm.Store(true)
	require.NoError(t, manager.UpdateWalletName(catalog[0].ID, "published N"))
	var request DKVSBatchCASRequest
	select {
	case request = <-accepted:
	case <-time.After(accountManagedBackupQuiet + 5*time.Second):
		t.Fatal("selected account module request was not accepted")
	}
	require.Equal(t, request.RequestID, originalID.Load())
	require.Len(t, request.Mutations, 1)
	require.NotNil(t, request.Mutations[0].Record)
	require.Equal(t, module.Key, request.Mutations[0].Record.Key)
	remoteN, err := manager.accountDKVSStore()
	require.NoError(t, err)
	confirmedN, err := remoteN.GetAuthoritative(module.Key)
	require.NoError(t, err)
	require.Equal(t, request.Mutations[0].Record.Seq, confirmedN.Seq)
	require.Equal(t, request.Mutations[0].Record.Value, confirmedN.Value)
	require.Eventually(t, func() bool {
		if database.hits.Load() == 0 {
			return false
		}
		current, err := manager.readAccountLocal(accountID, "wallets", AccountManagedDataGlobalScope)
		return err == nil && current != nil && current.Baseline != nil && current.Confirmed != 0 &&
			current.Baseline.Seq == request.Mutations[0].Record.Seq &&
			bytes.Equal(current.Baseline.Value, request.Mutations[0].Record.Value)
	}, 5*time.Second, 10*time.Millisecond, "module receipt and exact outbox-delete fault must both complete")
	// The test request's captured generation is recorded on its real outbox row.
	replica := newDKVSReplicaStore(manager.db)
	entries, err := replica.LoadOutbox(client.replicaNamespace)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	ack := entries[0]
	require.Equal(t, request.RequestID, ack.RequestID)
	require.Equal(t, accountKVOutboxDomain, ack.OriginDomain)
	require.Equal(t, module.Key, ack.OriginKey)
	require.Equal(t, DKVSOutboxAcknowledged, ack.State)
	originalGeneration := ack.OriginGeneration
	require.NotZero(t, originalGeneration)
	localN, err := manager.readAccountLocal(accountID, "wallets", AccountManagedDataGlobalScope)
	require.NoError(t, err)
	require.Equal(t, originalGeneration, localN.Confirmed)
	require.Equal(t, confirmedN.Hash, dkvsindexer.RecordHash(localN.Baseline).String())
	require.Greater(t, database.hits.Load(), uint32(0))
	require.True(t, manager.GetAccountManagementStatus().DKVSWriteAllowed,
		"exact ACK cleanup failure must not revoke a confirmed account write")
	require.True(t, retainedAccountACK(t, replica, client.replicaNamespace, request.RequestID))

	// Hold the normal receiver while newer local edits commit. A stale cleanup
	// attempt must neither change the confirmed baseline nor remove those edits.
	holdReceive.Store(true)
	manager.dkvs.wakeSync()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not enter controlled receive")
	}
	require.NoError(t, manager.UpdateWalletName(catalog[0].ID, "new local generation"))
	localN1, err := manager.readAccountLocal(accountID, "wallets", AccountManagedDataGlobalScope)
	require.NoError(t, err)
	require.Greater(t, localN1.Generation, originalGeneration)
	require.Equal(t, originalGeneration, localN1.Confirmed)
	require.Equal(t, confirmedN.Hash, dkvsindexer.RecordHash(localN1.Baseline).String())
	require.True(t, retainedAccountACK(t, replica, client.replicaNamespace, request.RequestID))
	require.Equal(t, int32(1), originalAttempts.Load(), "the acknowledged module request was resubmitted")
	database.fail.Store(false)
	done := make(chan error, 1)
	go func() { done <- manager.UpdateWalletName(catalog[0].ID, "after local cleanup") }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		unblock()
		<-done
		t.Fatal("local wallet update waited for network receiving")
	}
	localN2, err := manager.readAccountLocal(accountID, "wallets", AccountManagedDataGlobalScope)
	require.NoError(t, err)
	require.Greater(t, localN2.Generation, localN1.Generation)
	require.Equal(t, originalGeneration, localN2.Confirmed)
	require.Equal(t, confirmedN.Hash, dkvsindexer.RecordHash(localN2.Baseline).String())
	unblock()
	syncCtx, syncCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer syncCancel()
	deadline, _ := syncCtx.Deadline()
	require.Eventually(t, func() bool {
		return !retainedAccountACK(t, replica, client.replicaNamespace, request.RequestID)
	}, time.Until(deadline), 20*time.Millisecond, "the real worker must finish ACK cleanup after receiving resumes")
	require.NoError(t, manager.SyncAccountManagementState(syncCtx))
	require.NoError(t, manager.WaitAccountManagedDataReady(syncCtx))
	current, err := remoteN.GetAuthoritative(module.Key)
	require.NoError(t, err)
	state, err := account.OpenManagedState(manager.accountSecret, accountID, current.Value)
	require.NoError(t, err)
	require.Equal(t, "after local cleanup", state.Wallets[0].Name)
	require.False(t, retainedAccountACK(t, replica, client.replicaNamespace, request.RequestID))
	require.Equal(t, int32(1), originalAttempts.Load(), "cleanup retry republished the original business request")
}

func retainedAccountACK(t *testing.T, replica *dkvsReplicaStore, namespace, requestID string) bool {
	t.Helper()
	entries, err := replica.LoadOutbox(namespace)
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.RequestID == requestID {
			return entry.State == DKVSOutboxAcknowledged && entry.OriginDomain == accountKVOutboxDomain
		}
	}
	return false
}
