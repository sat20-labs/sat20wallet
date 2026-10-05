package e2e

import (
	"context"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	core "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// Real HTTP and the production bound-wallet admission path. ACK handling and
// current-set synchronization are deliberately delivered separately, exactly
// as they can arrive on a PWA's write and long-poll connections.
func TestSDKDKVSConfirmedReplica(t *testing.T) {
	for _, operation := range []string{"create", "update", "delete"} {
		t.Run(operation+"AckDoesNotChangeConfirmedKV", func(t *testing.T) {
			f := newBoundRPCFixture(t)
			owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
			f.bind(t, owner, f.coreID)
			key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "ack-only/value")
			require.NoError(t, err)
			prefix, err := dkvs.CollectionPathForKey(key)
			require.NoError(t, err)
			var previous *dkvs.Record
			if operation != "create" {
				previous, err = f.client.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("before"), dkvs.RecordOptions{IssueHeight: 100, TTL: 10})
				require.NoError(t, err)
			}
			db := indexerdb.NewKVDB(t.TempDir())
			require.NotNil(t, db)
			t.Cleanup(func() { _ = db.Close() })
			store := core.NewReplicaStore(db)
			const namespace = "ack-only"
			meta := sdkDKVSReviewInstallActive(t, f.client, store, namespace, prefix)
			capture := &sdkDKVSReviewAckCapture{inner: f.client.Http}
			writer := wallet.NewSatsNetDKVSClient(f.client.Scheme, f.client.Host, f.client.Proxy, capture)
			var submitted *dkvs.Record
			if operation == "delete" {
				submitted, err = writer.DeleteCurrentRecord(owner.Wallet, key, 100)
			} else {
				submitted, err = writer.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("after"), dkvs.RecordOptions{IssueHeight: 100, TTL: 10})
			}
			require.NoError(t, err)
			require.NotNil(t, capture.receipt)
			mutation := sdkDKVSReviewAbsent(submitted)
			if previous != nil {
				mutation = sdkDKVSReviewExpected(submitted, previous)
			}
			entry, err := core.NewBatchOutboxEntry(namespace, []dkvs.CASMutation{mutation}, meta.EndpointID, core.OutboxOrigin{})
			require.NoError(t, err)
			entry.RequestID = capture.receipt.RequestID
			require.NoError(t, store.QueueOutbox(entry))
			require.NoError(t, store.ApplyWriteResultAndAck(entry, capture.receipt))
			actual, readErr := store.LoadSubscriptionRecord(namespace, key)
			if previous == nil {
				require.ErrorIs(t, readErr, indexercommon.ErrKeyNotFound, "ACK must not install newly created KV")
			} else {
				require.NoError(t, readErr, "delete ACK must not bypass the confirmed-state installer")
				require.Equal(t, dkvs.RecordHash(previous), dkvs.RecordHash(actual), "update ACK must not write a second replica path")
			}
			unchanged, err := store.LoadActiveMeta(namespace, meta.Scope)
			require.NoError(t, err)
			require.Equal(t, meta.Generation, unchanged.Generation)
			pending, err := store.HasPendingOutbox(namespace)
			require.NoError(t, err)
			require.False(t, pending)
			_, err = writer.SyncActiveScope(context.Background(), store, namespace, meta.Scope, false)
			require.NoError(t, err)
			actual, readErr = store.LoadSubscriptionRecord(namespace, key)
			if operation == "delete" {
				require.ErrorIs(t, readErr, indexercommon.ErrKeyNotFound)
			} else {
				require.NoError(t, readErr)
				require.Equal(t, dkvs.RecordHash(submitted), dkvs.RecordHash(actual))
			}
		})
	}
}
