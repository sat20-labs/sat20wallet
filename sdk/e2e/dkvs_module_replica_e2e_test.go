package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	sdkdkvs "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// Public replica/outbox APIs consume real node replies. The separate Manager
// suites cover automatic worker scheduling; this file controls ACK interleaving.
func TestSDKDKVSModuleReplica(t *testing.T) {
	f := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t))
	bindDKVSReviewWallet(t, f.Network.Core, dkvsClientMnemonic)
	client := dkvsClientForNode(t, f.Network.Core)
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	key := func(t *testing.T, path string) string {
		t.Helper()
		k, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), path)
		require.NoError(t, err)
		return k
	}
	ctx := context.Background()

	t.Run("SnapshotDeltaAndDiskReopen", func(t *testing.T) {
		record := sdkDKVSReviewPutFree(t, client, owner, key(t, "review-reopen/a"), []byte("initial"))
		prefix, err := dkvs.CollectionPathForKey(record.Key)
		require.NoError(t, err)
		directory := t.TempDir()
		database := indexerdb.NewKVDB(directory)
		require.NotNil(t, database)
		t.Cleanup(func() { database.Close() })
		store := sdkdkvs.NewReplicaStore(database)
		const namespace = "review-reopen"
		meta := sdkDKVSReviewInstallActive(t, client, store, namespace, prefix)
		updated := sdkDKVSReviewPutFree(t, client, owner, record.Key, []byte("after-delta"))
		_, err = client.SyncActiveScope(ctx, store, namespace, meta.Scope, false)
		require.NoError(t, err)
		current, err := store.LoadActiveMeta(namespace, meta.Scope)
		require.NoError(t, err)
		require.Greater(t, current.Generation, meta.Generation)
		pending := sdkDKVSReviewFreeRecord(t, client, owner, key(t, "review-reopen/pending"), []byte("offline"), 1)
		entry, err := sdkdkvs.NewBatchOutboxEntry(namespace, []dkvs.CASMutation{sdkDKVSReviewAbsent(pending)}, meta.EndpointID, sdkdkvs.OutboxOrigin{})
		require.NoError(t, err)
		require.NoError(t, store.QueueOutbox(entry))
		require.NoError(t, store.UpdateOutboxState(entry, sdkdkvs.DKVSOutboxInflight, nil))
		database.Close()
		database = indexerdb.NewKVDB(directory)
		require.NotNil(t, database)
		store = sdkdkvs.NewReplicaStore(database)
		loaded, err := store.LoadSubscriptionRecord(namespace, record.Key)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(updated), dkvs.RecordHash(loaded))
		reopened, err := store.LoadActiveMeta(namespace, meta.Scope)
		require.NoError(t, err)
		require.Equal(t, *current, *reopened)
		state, err := store.LoadSubscriptionState(namespace)
		require.NoError(t, err)
		require.Equal(t, current.Generation, state.Generations[prefix])
		outbox, err := store.LoadOutbox(namespace)
		require.NoError(t, err)
		require.Len(t, outbox, 1)
		require.Equal(t, entry.RequestID, outbox[0].RequestID)
		require.Equal(t, sdkdkvs.DKVSOutboxInflight, outbox[0].State)
		mutations, err := outbox[0].DecodeMutations()
		require.NoError(t, err)
		require.Len(t, mutations, 1)
		require.Equal(t, dkvs.RecordHash(pending), dkvs.RecordHash(mutations[0].Record))
		require.True(t, mutations[0].Precondition.ExpectAbsent)
	})

	t.Run("RejectUntrustedDeltaWithoutAdvancingCursor", func(t *testing.T) {
		record := sdkDKVSReviewPutFree(t, client, owner, key(t, "review-replica-verify/a"), []byte("original"))
		prefix, err := dkvs.CollectionPathForKey(record.Key)
		require.NoError(t, err)
		database := indexerdb.NewKVDB(t.TempDir())
		require.NotNil(t, database)
		t.Cleanup(func() { database.Close() })
		store := sdkdkvs.NewReplicaStore(database)
		const namespace = "review-verify"
		meta := sdkDKVSReviewInstallActive(t, client, store, namespace, prefix)
		baseline, err := store.ActiveBaseline(namespace, meta.Scope)
		require.NoError(t, err)
		updated := sdkDKVSReviewPutFree(t, client, owner, record.Key, []byte("signed-update"))
		delta, err := client.GetActivePage(ctx, dkvs.ActiveSyncRequest{Scope: meta.Scope, EndpointID: meta.EndpointID, After: meta.Generation})
		require.NoError(t, err)
		require.True(t, delta.Complete)
		require.Len(t, delta.Records, 1)
		originalValue := append([]byte(nil), delta.Records[0].Value...)
		delta.Records[0].Value = []byte("transport-tampered")
		_, err = store.InstallActiveState(namespace, baseline, delta.Meta, delta.Records, false)
		require.Error(t, err)
		state, err := store.LoadActiveMeta(namespace, meta.Scope)
		require.NoError(t, err)
		require.Equal(t, meta.Generation, state.Generation)
		actual, err := store.LoadSubscriptionRecord(namespace, record.Key)
		require.NoError(t, err)
		require.Equal(t, record.Value, actual.Value)
		delta.Records[0].Value = originalValue
		foreignMeta := delta.Meta
		foreignMeta.EndpointID = "another-endpoint"
		_, err = store.InstallActiveState(namespace, baseline, foreignMeta, delta.Records, false)
		require.ErrorIs(t, err, dkvs.ErrEndpointMismatch)
		wrongBaseline := baseline
		wrongBaseline[0] ^= 1
		_, err = store.InstallActiveState(namespace, wrongBaseline, delta.Meta, delta.Records, false)
		require.ErrorIs(t, err, dkvs.ErrConcurrentUpdate)
		_, err = client.GetActivePage(ctx, dkvs.ActiveSyncRequest{Scope: meta.Scope, EndpointID: "another-endpoint", After: meta.Generation})
		require.ErrorIs(t, err, dkvs.ErrEndpointMismatch)
		_, err = store.InstallActiveState(namespace, baseline, delta.Meta, delta.Records, false)
		require.NoError(t, err)
		actual, err = store.LoadSubscriptionRecord(namespace, record.Key)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(updated), dkvs.RecordHash(actual))
	})

	t.Run("ExplicitDeleteInvalidatesOtherReplica", func(t *testing.T) {
		original := sdkDKVSReviewPutFree(t, client, owner, key(t, "review-replica-delete/a"), []byte("must-disappear"))
		prefix, err := dkvs.CollectionPathForKey(original.Key)
		require.NoError(t, err)
		database := indexerdb.NewKVDB(t.TempDir())
		require.NotNil(t, database)
		t.Cleanup(func() { database.Close() })
		store := sdkdkvs.NewReplicaStore(database)
		const namespace = "review-delete"
		meta := sdkDKVSReviewInstallActive(t, client, store, namespace, prefix)
		baseline, err := store.ActiveBaseline(namespace, meta.Scope)
		require.NoError(t, err)
		_, err = client.DeleteCurrentRecord(owner.Wallet, original.Key, sdkDKVSReviewHeight(t, client))
		require.NoError(t, err)
		delta, err := client.GetActivePage(ctx, dkvs.ActiveSyncRequest{Scope: meta.Scope, EndpointID: meta.EndpointID, After: meta.Generation})
		require.NoError(t, err)
		require.Empty(t, delta.Records, "no tombstone or history row may be returned")
		_, err = store.InstallActiveState(namespace, baseline, delta.Meta, delta.Records, false)
		require.ErrorIs(t, err, dkvs.ErrPathDiverged, "missing deletion requires current-set calibration")
		unchanged, err := store.LoadActiveMeta(namespace, meta.Scope)
		require.NoError(t, err)
		require.Equal(t, meta.Generation, unchanged.Generation)
		// The public coordinator must automatically fetch the full current set
		// after detecting the incomplete delta; no manual DB clearing is used.
		_, err = client.SyncActiveScope(ctx, store, namespace, meta.Scope, false)
		require.NoError(t, err)
		_, err = store.LoadSubscriptionRecord(namespace, original.Key)
		require.ErrorIs(t, err, indexercommon.ErrKeyNotFound)
		_, err = store.LoadLocalKeyState(namespace, original.Key)
		require.ErrorIs(t, err, indexercommon.ErrKeyNotFound)
		_, err = client.SyncActiveScope(ctx, store, namespace, meta.Scope, true)
		require.NoError(t, err)
		_, err = store.LoadSubscriptionRecord(namespace, original.Key)
		require.ErrorIs(t, err, indexercommon.ErrKeyNotFound)
	})

	t.Run("OwnWriteAckCannotSkipOtherWritersChange", func(t *testing.T) {
		a := sdkDKVSReviewPutFree(t, client, owner, key(t, "review-ack-gap/a"), []byte("a1"))
		b := sdkDKVSReviewPutFree(t, client, owner, key(t, "review-ack-gap/b"), []byte("b1"))
		prefix, err := dkvs.CollectionPathForKey(a.Key)
		require.NoError(t, err)
		database := indexerdb.NewKVDB(t.TempDir())
		require.NotNil(t, database)
		t.Cleanup(func() { database.Close() })
		store := sdkdkvs.NewReplicaStore(database)
		const namespace = "review-ack-gap"
		meta := sdkDKVSReviewInstallActive(t, client, store, namespace, prefix)
		b2 := sdkDKVSReviewPutFree(t, client, owner, b.Key, []byte("b2-remote"))
		a2 := sdkDKVSReviewFreeRecord(t, client, owner, a.Key, []byte("a2-local"), 2)
		mutations := []dkvs.CASMutation{sdkDKVSReviewExpected(a2, a)}
		capture := &sdkDKVSReviewAckCapture{inner: client.Http}
		writer := wallet.NewSatsNetDKVSClient(client.Scheme, client.Host, client.Proxy, capture).WithWriteSigner(owner.Wallet)
		apiResult, err := writer.PutRecordBatchCAS(mutations)
		require.NoError(t, err)
		result := capture.receipt
		require.NotNil(t, result)
		require.Equal(t, apiResult.RequestID, result.RequestID)
		entry, err := sdkdkvs.NewBatchOutboxEntry(namespace, mutations, meta.EndpointID, sdkdkvs.OutboxOrigin{})
		require.NoError(t, err)
		entry.RequestID = result.RequestID
		require.NotEmpty(t, entry.RequestID)
		require.NoError(t, store.QueueOutbox(entry))
		require.NoError(t, store.ApplyWriteResultAndAck(entry, result))
		localA, err := store.LoadSubscriptionRecord(namespace, a.Key)
		require.NoError(t, err)
		require.Equal(t, a.Value, localA.Value, "ACK confirms the request but does not materialize a2 into the confirmed replica")
		localB, err := store.LoadSubscriptionRecord(namespace, b.Key)
		require.NoError(t, err)
		require.Equal(t, b.Value, localB.Value)
		state, err := store.LoadSubscriptionState(namespace)
		require.NoError(t, err)
		require.Equal(t, meta.Generation, state.Generations[prefix], "own ACK must not advance prefix cursor")
		active, err := store.LoadActiveMeta(namespace, meta.Scope)
		require.NoError(t, err)
		require.Equal(t, meta.Generation, active.Generation)
		_, err = client.SyncActiveScope(ctx, store, namespace, meta.Scope, false)
		require.NoError(t, err)
		localB, err = store.LoadSubscriptionRecord(namespace, b.Key)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(b2), dkvs.RecordHash(localB))
	})
}

func sdkDKVSReviewInstallActive(t *testing.T, client *wallet.SatsNetDKVSClient, store *sdkdkvs.ReplicaStore, namespace, prefix string) dkvs.ActiveMeta {
	t.Helper()
	config, err := client.GetDKVSClientConfig()
	require.NoError(t, err)
	require.NoError(t, store.AddRegisteredPrefix(namespace, prefix))
	require.NoError(t, store.PreparePrefixSync(namespace, config.EndpointID, []string{prefix}, true))
	scope := dkvs.ActiveScope{Prefix: prefix}
	_, err = client.SyncActiveScope(context.Background(), store, namespace, scope, true)
	require.NoError(t, err)
	require.NoError(t, store.CompletePrefixSync(namespace, config.EndpointID, []string{prefix}))
	meta, err := store.LoadActiveMeta(namespace, scope)
	require.NoError(t, err)
	return *meta
}

type sdkDKVSReviewAckCapture struct {
	inner   wallet.HttpClient
	receipt *dkvs.WriteResult
}

func (c *sdkDKVSReviewAckCapture) SendGetRequest(url *wallet.URL) ([]byte, error) {
	return c.inner.SendGetRequest(url)
}
func (c *sdkDKVSReviewAckCapture) SendPostRequest(url *wallet.URL, body []byte) ([]byte, error) {
	raw, err := c.inner.SendPostRequest(url, body)
	if err == nil && strings.HasSuffix(url.Path, "/records/batch-cas") {
		var response struct {
			Code int               `json:"code"`
			Data *dkvs.WriteResult `json:"data"`
		}
		if json.Unmarshal(raw, &response) == nil && response.Code == 0 {
			c.receipt = response.Data
		}
	}
	return raw, err
}

func sdkDKVSReviewActivePage(client *wallet.SatsNetDKVSClient, prefix string) (*dkvs.ActivePage, error) {
	config, err := client.GetDKVSClientConfig()
	if err != nil {
		return nil, err
	}
	return client.GetActivePage(context.Background(), dkvs.ActiveSyncRequest{Scope: dkvs.ActiveScope{Prefix: prefix}, EndpointID: config.EndpointID, Full: true})
}
