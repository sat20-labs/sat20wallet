package wallet

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	core "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	"github.com/sat20-labs/satoshinet/btcec"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestDKVSReviewTransportReleasesAfterInternalPanic(t *testing.T) {
	manager, _ := finalDKVSTestManager(t)
	coordinator := manager.ensureDKVSManager()
	var done chan struct{}
	recovered := captureDKVSOutboxPanic(func() {
		_ = coordinator.runTransport(func() error {
			coordinator.runMu.Lock()
			done = coordinator.runDone
			coordinator.runMu.Unlock()
			panic("fixture internal failure")
		})
	})
	require.Equal(t, "fixture internal failure", recovered, "coordinator must preserve internal fail-fast")
	coordinator.runMu.Lock()
	busy := coordinator.runActive
	coordinator.runMu.Unlock()
	require.False(t, busy, "a recovered internal panic must release the coordinator")
	select {
	case <-done:
	default:
		t.Fatal("previous operation's waiters were not released")
	}
	require.NoError(t, coordinator.runTransport(func() error { return nil }))
}

func TestDKVSReviewExpiredOutboxStillReceives(t *testing.T) {
	for _, test := range []struct {
		name      string
		ttl       uint64
		rejection error
	}{
		{name: "signing-height", ttl: 100, rejection: dkvs.ErrStaleEndpoint},
		{name: "record-ttl", ttl: 3, rejection: dkvs.ErrExpiredRecord},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, remote := finalDKVSTestManager(t)
			coreKey, err := btcec.NewPrivateKey()
			require.NoError(t, err)
			remote.endpointID = hex.EncodeToString(coreKey.PubKey().SerializeCompressed())
			height := uint64(1)
			node := dkvs.New(newMemoryKVDB(), dkvs.Config{EndpointID: remote.endpointID,
				AllowFreeLocal: true, FreeLocalCache: remote.freeLocal, CurrentHeight: func() uint64 { return height }})
			account, err := dkvsAccountID(manager.wallet)
			require.NoError(t, err)
			bindingKey, err := dkvs.AccountMappingKey(GetChainParam().Name, manager.wallet.GetAddress())
			require.NoError(t, err)
			value, err := dkvs.EncodeAccountServiceDescriptor(dkvs.AccountServiceDescriptor{AccountID: account, CoreNodeID: remote.endpointID})
			require.NoError(t, err)
			binding, err := NewDKVSAccountSignedRecord(manager.wallet, bindingKey, value, dkvs.RecordOptions{Seq: 1, IssueHeight: 1})
			require.NoError(t, err)
			_, err = node.PutLocal(binding)
			require.NoError(t, err)
			admission := &dkvs.WalletRPCAdmission{Indexer: node, IsCoreNode: func() bool { return true },
				CurrentBinding: func(string) (*wire.DKVSRecord, error) { return node.Get(bindingKey) }}
			// Reuse the existing endpoint transport; only write admission goes through
			// the real node boundary to prove this is a height rejection, not a CAS race.
			http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
			var rejected error
			http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
				if path != "/v3/dkvs/records/batch-cas" {
					return remote.SendDKVSPostContext(ctx, path, body)
				}
				var request DKVSBatchCASRequest
				if err := json.Unmarshal(body, &request); err != nil {
					return nil, err
				}
				// This case creates one absent key and preserves its original request.
				require.Len(t, request.Mutations, 1)
				require.True(t, request.Mutations[0].ExpectAbsent)
				result, err := admission.PutRecords([]dkvs.CASMutation{{Record: request.Mutations[0].Record,
					Precondition: dkvs.WritePrecondition{ExpectAbsent: true}}},
					dkvs.BatchCASOptions{EndpointID: request.EndpointID, RequestID: request.RequestID}, request.Authorization)
				rejected = err
				return satoshinetTestResponse(result, err)
			}
			configureRGB11DKVSTestManager(manager, http)
			client, err := manager.ensureDKVSManager().primaryClient()
			require.NoError(t, err)
			key := accountTestKey(t, manager, "repeat-receive/value")
			prefix, err := dkvs.CollectionPathForKey(key)
			require.NoError(t, err)
			seed := finalDKVSSeedClient(manager, remote)
			_, err = seed.PutRecord(freeLocalRecord(t, manager, key, 1, "confirmed-v1"))
			require.NoError(t, err)
			require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
			require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
			pendingKey := accountTestKey(t, manager, "repeat-unsent/value")
			pending, err := newDKVSAccountSignedRecordWithFreeLocal(manager.wallet, pendingKey, []byte("original-intent"),
				dkvs.RecordOptions{Seq: 1, IssueHeight: 1, TTL: test.ttl})
			require.NoError(t, err)
			mutations := []dkvs.CASMutation{{Record: pending, Precondition: dkvs.WritePrecondition{ExpectAbsent: true}}}
			entry, err := core.NewBatchOutboxEntry(client.replicaNamespace, mutations, remote.endpointID, core.OutboxOrigin{})
			require.NoError(t, err)
			entry.Authorization, err = client.WithWriteSigner(manager.wallet).prepareWriteAuthorization(mutations, entry.EndpointID, entry.RequestID)
			require.NoError(t, err)
			replica := core.NewReplicaStore(manager.db)
			require.NoError(t, replica.QueueOutbox(entry))
			// No mutation changed the pending collection. Only the height window closes.
			height = 4
			remote.bestHeight = 4
			updated, err := newDKVSAccountSignedRecordWithFreeLocal(manager.wallet, key, []byte("remote-v2"),
				dkvs.RecordOptions{Seq: 2, IssueHeight: height, TTL: 100})
			require.NoError(t, err)
			_, err = seed.PutRecord(updated)
			require.NoError(t, err)
			var syncErr error
			panicked := captureDKVSOutboxPanic(func() { _, syncErr = manager.syncDKVSOnce() })
			require.ErrorIs(t, rejected, test.rejection, "real wallet admission must reject the original expired request")
			require.Nil(t, panicked, "ordinary offline expiry must not crash the synchronization worker")
			require.True(t, IsDKVSErrorCode(syncErr, dkvs.ErrorCodeOf(test.rejection)))
			_, err = node.Get(pendingKey)
			require.ErrorIs(t, err, dkvs.ErrRecordNotFound, "the old intent must not be reauthorized")
			local, err := replica.LoadSubscriptionRecord(client.replicaNamespace, key)
			require.NoError(t, err)
			require.Equal(t, dkvs.RecordHash(updated), dkvs.RecordHash(local), "a rejected write must not prevent receiving current subscribed state")
		})
	}
}

func TestDKVSReviewSendFailureKeepsWatching(t *testing.T) {
	for _, mode := range []string{"conflict", "transient"} {
		t.Run(mode, func(t *testing.T) {
			manager, remote := finalDKVSTestManager(t)
			http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
			configureRGB11DKVSTestManager(manager, http)
			client, err := manager.ensureDKVSManager().primaryClient()
			require.NoError(t, err)
			key := accountTestKey(t, manager, "repeat-worker/value")
			prefix, err := dkvs.CollectionPathForKey(key)
			require.NoError(t, err)
			seed := finalDKVSSeedClient(manager, remote)
			_, err = seed.PutRecord(freeLocalRecord(t, manager, key, 1, "v1"))
			require.NoError(t, err)
			require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
			require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
			pendingKey := accountTestKey(t, manager, "repeat-worker-pending/value")
			mutations := []dkvs.CASMutation{{Record: freeLocalRecord(t, manager, pendingKey, 1, "pending"),
				Precondition: dkvs.WritePrecondition{ExpectAbsent: true}}}
			entry, err := core.NewBatchOutboxEntry(client.replicaNamespace, mutations, remote.endpointID, core.OutboxOrigin{})
			require.NoError(t, err)
			entry.Authorization, err = client.WithWriteSigner(manager.wallet).prepareWriteAuthorization(mutations, entry.EndpointID, entry.RequestID)
			require.NoError(t, err)
			replica := core.NewReplicaStore(manager.db)
			require.NoError(t, replica.QueueOutbox(entry))
			var attempts, watches atomic.Int32
			http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
				if path == "/v3/dkvs/records/batch-cas" {
					var request DKVSBatchCASRequest
					if e := json.Unmarshal(body, &request); e != nil {
						return nil, e
					}
					if request.RequestID != entry.RequestID {
						return nil, dkvs.ErrInvalidRecord
					}
					attempts.Add(1)
					if mode == "conflict" {
						return satoshinetTestResponse(nil, dkvs.ErrStaleEndpoint)
					}
					return nil, &HTTPResponseError{StatusCode: 503}
				}
				if path == "/v3/dkvs/active/watch" {
					watches.Add(1)
				}
				return remote.SendDKVSPostContext(ctx, path, body)
			}
			manager.dkvs.start()
			defer manager.dkvs.stopAndWait()
			require.Eventually(t, func() bool { return attempts.Load() > 0 && watches.Load() > 0 }, 2*time.Second, 10*time.Millisecond)
			for seq := uint64(2); seq <= 3; seq++ {
				updated, e := seed.PutRecord(freeLocalRecord(t, manager, key, seq, "remote"))
				require.NoError(t, e)
				require.Eventually(t, func() bool {
					local, e := replica.LoadSubscriptionRecord(client.replicaNamespace, key)
					return e == nil && dkvs.RecordHash(local) == dkvs.RecordHash(updated)
				}, 2*time.Second, 10*time.Millisecond)
			}
			var entries []*core.BatchOutboxEntry
			require.NoError(t, manager.dkvs.runTransport(func() error { entries, err = replica.LoadOutbox(client.replicaNamespace); return err }))
			require.Len(t, entries, 1)
			require.Equal(t, entry.Authorization, entries[0].Authorization)
			if mode == "conflict" {
				require.Equal(t, core.DKVSOutboxConflict, entries[0].State)
				require.Equal(t, int32(1), attempts.Load(), "blocked original intent must not be resubmitted")
			} else {
				require.Eventually(t, func() bool { return attempts.Load() >= 2 }, 7*time.Second, 10*time.Millisecond, "transient send failure must retain original-request retries")
			}
		})
	}
}
