package e2e

import (
	"context"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// Public SDK calls, registered CoreNode admission, real SatoshiNet HTTP/P2P
// processes and SDK databases. No production wallet or browser is used.
func TestSDKDKVSActiveState(t *testing.T) {
	f := newTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t))
	client := dkvsClientForNode(t, f.Network.Core)
	other := dkvsClientForNode(t, f.Network.Bootstrap)
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	ownerWriter := client.WithWriteSigner(owner.Wallet)
	manager, _ := newWalletManagerForNode(t, f.Network.Core, dkvsClientMnemonic)
	require.NoError(t, manager.InitializeAccountManagement("123456"))
	require.NoError(t, manager.BindAccountToCurrentCoreNode())
	manager.StopDKVSSync()
	config, err := client.GetDKVSClientConfig()
	require.NoError(t, err)
	ctx := context.Background()
	key := func(t *testing.T, path string) string {
		t.Helper()
		k, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), path)
		require.NoError(t, err)
		return k
	}
	scopeFor := func(t *testing.T, key string) dkvs.ActiveScope {
		t.Helper()
		prefix, err := dkvs.CollectionPathForKey(key)
		require.NoError(t, err)
		return dkvs.ActiveScope{Prefix: prefix}
	}
	newStore := func(t *testing.T) *dkvscore.ReplicaStore {
		t.Helper()
		db := indexerdb.NewKVDB(t.TempDir())
		require.NotNil(t, db)
		t.Cleanup(func() { db.Close() })
		return dkvscore.NewReplicaStore(db)
	}

	t.Run("BasicCreateReadUpdateAndLocalIsolation", func(t *testing.T) {
		k := key(t, "active-basic/value")
		first := sdkDKVSReviewPutFree(t, client, owner, k, []byte("initial"))
		store, scope := newStore(t), scopeFor(t, k)
		_, err := client.SyncActiveScope(ctx, store, "active-basic", scope, false)
		require.NoError(t, err)
		loaded, err := store.LoadSubscriptionRecord("active-basic", k)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(first), dkvs.RecordHash(loaded))
		second := sdkDKVSReviewPutFree(t, client, owner, k, []byte("updated"))
		_, err = client.SyncActiveScope(ctx, store, "active-basic", scope, false)
		require.NoError(t, err)
		loaded, err = store.LoadSubscriptionRecord("active-basic", k)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(second), dkvs.RecordHash(loaded))
		_, err = other.GetRecordDirect(k)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
	})

	t.Run("OnlineWatchFiltersAndDeliversCurrentValue", func(t *testing.T) {
		watched, irrelevant := key(t, "active-watch/watched"), key(t, "active-watch/other")
		sdkDKVSReviewPutFree(t, client, owner, watched, []byte("one"))
		scope := scopeFor(t, watched)
		scope.Keys = []string{watched}
		page, err := client.GetActivePage(ctx, dkvs.ActiveSyncRequest{Scope: scope, EndpointID: config.EndpointID, Full: true})
		require.NoError(t, err)
		watchCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		type answer struct {
			result *dkvs.ActiveWatchResult
			err    error
		}
		answers := make(chan answer, 1)
		go func() {
			result, err := client.WatchActive(watchCtx, dkvs.ActiveWatchRequest{EndpointID: config.EndpointID,
				Scopes: []dkvs.ActiveWatchScope{{Scope: scope, Generation: page.Meta.Generation, Root: page.Meta.Root}}})
			answers <- answer{result, err}
		}()
		sdkDKVSReviewPutFree(t, client, owner, irrelevant, []byte("must-not-be-delivered"))
		select {
		case answer := <-answers:
			t.Fatalf("unrelated key woke exact-key subscriber: %+v", answer)
		case <-time.After(150 * time.Millisecond):
		}
		updated := sdkDKVSReviewPutFree(t, client, owner, watched, []byte("two"))
		select {
		case answer := <-answers:
			require.NoError(t, answer.err)
			require.NotNil(t, answer.result)
			require.NotNil(t, answer.result.Page)
			require.Len(t, answer.result.Page.Records, 1)
			require.Equal(t, dkvs.RecordHash(updated), dkvs.RecordHash(answer.result.Page.Records[0]))
		case <-watchCtx.Done():
			t.Fatal("connected SDK did not receive the update")
		}
	})

	t.Run("OfflineDeleteReconcilesWithoutFloorAndPreservesOutbox", func(t *testing.T) {
		k, sibling := key(t, "active-delete/value"), key(t, "active-delete/sibling")
		original := sdkDKVSReviewPutFree(t, client, owner, k, []byte("delete-me"))
		sdkDKVSReviewPutFree(t, client, owner, sibling, []byte("keep"))
		store, scope := newStore(t), scopeFor(t, k)
		const ns = "active-delete"
		_, err := client.SyncActiveScope(ctx, store, ns, scope, false)
		require.NoError(t, err)
		pending := sdkDKVSReviewFreeRecord(t, client, owner, key(t, "active-delete/pending"), []byte("not-submitted"), 1)
		entry, err := dkvscore.NewBatchOutboxEntry(ns, []dkvs.CASMutation{sdkDKVSReviewAbsent(pending)}, config.EndpointID, dkvscore.OutboxOrigin{})
		require.NoError(t, err)
		require.NoError(t, store.QueueOutbox(entry))
		command, err := client.DeleteCurrentRecord(owner.Wallet, k, sdkDKVSReviewHeight(t, client))
		require.NoError(t, err)
		state, err := client.GetKeyState(k)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateNeverSeen, state.Status)
		require.Zero(t, state.Seq)
		_, err = client.SyncActiveScope(ctx, store, ns, scope, false)
		require.NoError(t, err)
		_, err = store.LoadSubscriptionRecord(ns, k)
		require.ErrorIs(t, err, indexercommon.ErrKeyNotFound)
		_, err = store.LoadLocalKeyState(ns, k)
		require.ErrorIs(t, err, indexercommon.ErrKeyNotFound)
		outbox, err := store.LoadOutbox(ns)
		require.NoError(t, err)
		require.Len(t, outbox, 1)
		require.Equal(t, entry.RequestID, outbox[0].RequestID)
		result, err := ownerWriter.PutRecordBatchCAS([]dkvs.CASMutation{sdkDKVSReviewExpected(command, original)})
		require.NoError(t, err)
		require.Zero(t, result.Applied)
		recreated := sdkDKVSReviewPutFree(t, client, owner, k, []byte("new-lifetime"))
		require.Equal(t, uint64(1), recreated.Seq)
		_, err = ownerWriter.PutRecordBatchCAS([]dkvs.CASMutation{sdkDKVSReviewExpected(command, original)})
		require.ErrorIs(t, err, dkvs.ErrWriteConflict)
		_, err = client.SyncActiveScope(ctx, store, ns, scope, false)
		require.NoError(t, err)
		loaded, err := store.LoadSubscriptionRecord(ns, k)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(recreated), dkvs.RecordHash(loaded))
	})

	t.Run("BatchGenerationExclusivePagingAndConcurrentInvalidation", func(t *testing.T) {
		var mutations []dkvs.CASMutation
		for n := 0; n < 17; n++ {
			k := key(t, "active-pages/"+string(rune('a'+n)))
			record := sdkDKVSReviewFreeRecord(t, client, owner, k, []byte("page"), 1)
			mutations = append(mutations, sdkDKVSReviewAbsent(record))
		}
		result, err := ownerWriter.PutRecordBatchCAS(mutations)
		require.NoError(t, err)
		require.Len(t, result.PrefixStates, 1)
		scope := scopeFor(t, mutations[0].Record.Key)
		request := dkvs.ActiveSyncRequest{Scope: scope, EndpointID: config.EndpointID,
			After: result.PrefixStates[0].Generation - 1, PageSize: 4}
		seen := make(map[string]bool)
		for {
			page, err := client.GetActivePage(ctx, request)
			require.NoError(t, err)
			for _, record := range page.Records {
				require.False(t, seen[record.Key])
				seen[record.Key] = true
			}
			if page.Complete {
				break
			}
			request.Cursor = page.Next
		}
		require.Len(t, seen, 17, "every key in a newer-generation batch must appear exactly once")
		boundary := request
		boundary.After, boundary.Cursor = result.PrefixStates[0].Generation, nil
		empty, err := client.GetActivePage(ctx, boundary)
		require.NoError(t, err)
		require.True(t, empty.Complete)
		require.Empty(t, empty.Records, "the completed generation must not be returned again")
		request.Full, request.After, request.Cursor = true, 0, nil
		page, err := client.GetActivePage(ctx, request)
		require.NoError(t, err)
		require.False(t, page.Complete)
		sdkDKVSReviewPutFree(t, client, owner, mutations[0].Record.Key, []byte("concurrent"))
		request.Cursor = page.Next
		_, err = client.GetActivePage(ctx, request)
		require.ErrorIs(t, err, dkvs.ErrStaleGeneration)
		_, err = client.SyncActiveScope(ctx, newStore(t), "active-pages", scope, true)
		require.NoError(t, err)
	})

	t.Run("DatabaseReopenRetainsSourceCursor", func(t *testing.T) {
		k := key(t, "active-reopen/value")
		sdkDKVSReviewPutFree(t, client, owner, k, []byte("before-close"))
		directory := t.TempDir()
		db := indexerdb.NewKVDB(directory)
		require.NotNil(t, db)
		t.Cleanup(func() { db.Close() })
		store, scope := dkvscore.NewReplicaStore(db), scopeFor(t, k)
		_, err := client.SyncActiveScope(ctx, store, "active-reopen", scope, false)
		require.NoError(t, err)
		before, err := store.LoadActiveMeta("active-reopen", scope)
		require.NoError(t, err)
		db.Close()
		db = indexerdb.NewKVDB(directory)
		store = dkvscore.NewReplicaStore(db)
		after, err := store.LoadActiveMeta("active-reopen", scope)
		require.NoError(t, err)
		require.Equal(t, before.Generation, after.Generation)
		updated := sdkDKVSReviewPutFree(t, client, owner, k, []byte("while-offline"))
		_, err = client.SyncActiveScope(ctx, store, "active-reopen", scope, false)
		require.NoError(t, err)
		loaded, err := store.LoadSubscriptionRecord("active-reopen", k)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(updated), dkvs.RecordHash(loaded))
	})
}
