package wallet

import (
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	core "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	p2p "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs/p2p"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Cross-layer review regressions. SDK operations use the existing in-process
// transport and real Indexer. P2P tests step production Handler requests and
// signed responses, not TCP. Fee/chain identity are fixtures; these tests do
// not establish live contract or production network readiness.
func TestDKVSPWAFinalReview(t *testing.T) {
	newClient := func(t *testing.T) (*Manager, *SatsNetDKVSClient, *satoshinetDKVSTestTransport) {
		t.Helper()
		privateKey, err := btcec.NewPrivateKey()
		require.NoError(t, err)
		manager := newRGB11MultiDeviceManager(t, privateKey, 907)
		transport := newSatoshiNetDKVSTestTransport()
		configureRGB11DKVSTestManager(manager, transport)
		client, err := manager.ensureDKVSManager().primaryClient()
		require.NoError(t, err)
		return manager, client, transport
	}

	t.Run("UnmanagedDirectoryShowsOwnNewKey", func(t *testing.T) {
		manager, client, _ := newClient(t)
		key := accountTestKey(t, manager, "pwa-final-cache/new")
		prefix, err := dkvs.CollectionPathForKey(key)
		require.NoError(t, err)
		before, total, err := client.ListRecords(prefix, 0, 0)
		require.NoError(t, err)
		require.Empty(t, before)
		require.Zero(t, total)
		created, err := client.PutSignedRecordFreeLocal(manager.wallet, key, []byte("created"),
			dkvs.RecordOptions{IssueHeight: 1, TTL: 100})
		require.NoError(t, err)
		remote, err := client.GetRecordDirect(key)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(created), dkvs.RecordHash(remote))
		after, total, err := client.ListRecords(prefix, 0, 0)
		require.NoError(t, err)
		require.Equal(t, 1, total, "a successful own create must appear in the next directory read")
		require.Len(t, after, 1)
	})

	t.Run("RejectedOutboxMustNotStopReceivingRemoteState", func(t *testing.T) {
		manager, client, transport := newClient(t)
		key := accountTestKey(t, manager, "pwa-final-outbox/value")
		prefix, err := dkvs.CollectionPathForKey(key)
		require.NoError(t, err)
		first, err := client.PutSignedRecordFreeLocal(manager.wallet, key, []byte("v1"),
			dkvs.RecordOptions{IssueHeight: 1, TTL: 100})
		require.NoError(t, err)
		require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
		require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))

		pending, err := newDKVSAccountSignedRecordWithFreeLocal(manager.wallet, key, []byte("unsent-local-v2"),
			dkvs.RecordOptions{Seq: 2, IssueHeight: 1, TTL: 100})
		require.NoError(t, err)
		hash := dkvs.RecordHash(first)
		mutations := []dkvs.CASMutation{{Record: pending, Precondition: dkvs.WritePrecondition{ExpectedHash: &hash}}}
		entry, err := core.NewBatchOutboxEntry(client.replicaNamespace, mutations, transport.indexer.EndpointID(), core.OutboxOrigin{})
		require.NoError(t, err)
		entry.Authorization, err = client.WithWriteSigner(manager.wallet).prepareWriteAuthorization(mutations, entry.EndpointID, entry.RequestID)
		require.NoError(t, err)
		replica := core.NewReplicaStore(manager.db)
		require.NoError(t, replica.QueueOutbox(entry))

		// Another device wins before the original signed request can be delivered.
		remoteClient := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", transport).WithWriteSigner(manager.wallet)
		winner, err := remoteClient.PutSignedRecordFreeLocal(manager.wallet, key, []byte("remote-v2"),
			dkvs.RecordOptions{IssueHeight: 1, TTL: 100})
		require.NoError(t, err)
		manager.dkvs.start()
		defer manager.dkvs.stopAndWait()
		require.Eventually(t, func() bool {
			entries, readErr := replica.LoadOutbox(client.replicaNamespace)
			return readErr == nil && len(entries) == 0
		}, 3*time.Second, 10*time.Millisecond, "the stale operation must be rejected, not reauthorized")
		require.Eventually(t, func() bool {
			current, readErr := client.GetRecord(key)
			return readErr == nil && dkvs.RecordHash(current) == dkvs.RecordHash(winner)
		}, 2*time.Second, 10*time.Millisecond, "discarding a conflicting request must not leave the sync worker waiting indefinitely for an unrelated wake")
		next, err := remoteClient.PutSignedRecordFreeLocal(manager.wallet, key, []byte("remote-v3"),
			dkvs.RecordOptions{IssueHeight: 1, TTL: 100})
		require.NoError(t, err)
		defer func() {
			if !t.Failed() {
				return
			}
			manager.dkvs.mu.Lock()
			syncError := manager.dkvs.lastSyncError
			manager.dkvs.mu.Unlock()
			meta, metaErr := replica.LoadActiveMeta(client.replicaNamespace, dkvs.ActiveScope{Prefix: prefix})
			t.Logf("v3 receive failure: sync error=%q, meta=%+v, meta error=%v", syncError, meta, metaErr)
		}()
		require.Eventually(t, func() bool {
			current, readErr := client.GetRecord(key)
			return readErr == nil && dkvs.RecordHash(current) == dkvs.RecordHash(next)
		}, 2*time.Second, 10*time.Millisecond, "the resumed worker must also receive subsequent remote changes")
	})

	t.Run("OnePeerCannotFinishAnotherPeersSync", func(t *testing.T) {
		manager, _, target := newClient(t)
		source := newSatoshiNetDKVSTestTransport()
		store := pwaReviewPeerStore{target.indexer}
		node := &p2p.NodeState{}
		keyA := accountTestKey(t, manager, "pwa-final-a/value")
		keyB := accountTestKey(t, manager, "pwa-final-b/value")
		prefixA, err := dkvs.CollectionPathForKey(keyA)
		require.NoError(t, err)
		prefixB, err := dkvs.CollectionPathForKey(keyB)
		require.NoError(t, err)
		privateKey, err := btcec.NewPrivateKey()
		require.NoError(t, err)
		var requestA, requestB *wire.MsgDKVSSyncRequest
		peerA, peerB, servingPeer := &p2p.PeerState{}, &p2p.PeerState{}, &p2p.PeerState{}
		t.Cleanup(peerA.Close)
		t.Cleanup(peerB.Close)
		t.Cleanup(servingPeer.Close)
		receiver := p2p.Handler{
			Store: store, Node: node, Peer: peerA, TrustedSource: true,
			ValidatorID: hex.EncodeToString(privateKey.PubKey().SerializeCompressed()),
			Net:         chaincfg.TestNetParams.Net, LocalServices: wire.SFNodeMiner,
			Send: func(message wire.Message) { requestA, _ = message.(*wire.MsgDKVSSyncRequest) },
		}
		other := receiver
		other.Peer = peerB
		other.Send = func(message wire.Message) { requestB, _ = message.(*wire.MsgDKVSSyncRequest) }
		receiver.QueuePathSync(prefixA)
		other.QueuePathSync(prefixB)
		require.NotNil(t, requestA)
		require.Nil(t, requestB, "another connection must join the node queue, not start a concurrent snapshot")
		require.False(t, node.Ready())
		var response *wire.MsgDKVSSyncResponse
		serving := p2p.Handler{
			Store: pwaReviewPeerStore{source.indexer}, Peer: servingPeer, Node: &p2p.NodeState{},
			Net: chaincfg.TestNetParams.Net, MirrorAuthority: true,
			Sign: func(payload []byte) ([]byte, error) {
				return ecdsa.Sign(privateKey, chainhash.HashB(payload)).Serialize(), nil
			},
			Send: func(message wire.Message) { response, _ = message.(*wire.MsgDKVSSyncResponse) },
		}
		// An unrelated peer cannot finish this node's synchronization.
		other.OnSyncResponse(&wire.MsgDKVSSyncResponse{SessionID: 999, Done: true})
		// Capture A before its live update, so replay must wait for B as well.
		serving.OnSyncRequest(requestA)
		require.NotNil(t, response)
		require.True(t, response.Done)
		_, pending := peerA.ActivePathSync()
		require.True(t, pending)
		// Verify the production consequence, not only the shared ready flag.
		source.indexer.SetFeeVerifier(pwaReviewFeeVerifier{JSONFeeVerifier: dkvs.JSONFeeVerifier{AllowFreeLocal: true}, height: &source.height})
		target.indexer.SetFeeVerifier(pwaReviewFeeVerifier{JSONFeeVerifier: dkvs.JSONFeeVerifier{AllowFreeLocal: true}, height: &target.height})
		sourceClient := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", source).WithWriteSigner(manager.wallet)
		created, err := sourceClient.PutSignedRecordWithAutopay(manager.wallet, keyA, []byte("notify during A sync"),
			dkvs.RecordOptions{IssueHeight: 1}, DKVSAutopayOptions{PoolContract: "review-fixture-pool", AddressParams: &chaincfg.TestNetParams})
		require.NoError(t, err)
		receiver.OnNotify(p2p.NotifyForRecord(created))
		premature, readErr := target.indexer.GetForRelay(keyA)
		t.Logf("A syncing=%t, node READY=%t, A notify stored=%t", pending, node.Ready(), premature != nil)
		require.ErrorIs(t, readErr, dkvs.ErrRecordNotFound, "A's Notify must stay buffered until A's snapshot is installed")
		require.False(t, node.Ready(), "prefix A is still downloading; finishing B must not open the shared Notify gate")
		receiver.OnSyncResponse(response)
		require.False(t, node.Ready(), "A is installed but B is still outstanding")
		_, readErr = target.indexer.GetForRelay(keyA)
		require.ErrorIs(t, readErr, dkvs.ErrRecordNotFound, "Notify must remain buffered until every directory is installed")
		require.Equal(t, prefixB, requestA.Filters[0].Target, "the same connection executes the next node-wide job")
		require.Nil(t, requestB)
		serving.OnSyncRequest(requestA)
		require.NotNil(t, response)
		require.True(t, response.Done)
		receiver.OnSyncResponse(response)
		installed, err := target.indexer.GetForRelay(keyA)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(created), dkvs.RecordHash(installed))
		require.True(t, node.Ready())
	})

	for _, changeSource := range []bool{false, true} {
		name := "P2PPagingSurvivesHeightAdvanceWithoutContentChange"
		if changeSource {
			name = "P2PPagingUsesCapturedSnapshotWhileSourceChanges"
		}
		t.Run(name, func(t *testing.T) {
			manager, _, source := newClient(t)
			source.indexer.SetFeeVerifier(pwaReviewFeeVerifier{JSONFeeVerifier: dkvs.JSONFeeVerifier{AllowFreeLocal: true}, height: &source.height})
			client := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", source).WithWriteSigner(manager.wallet)
			key := accountTestKey(t, manager, "pwa-final-paging/value")
			for _, suffix := range []string{"a", "b", "c", "d"} {
				recordKey := accountTestKey(t, manager, "pwa-final-paging/"+suffix)
				_, err := client.PutSignedRecordWithAutopay(manager.wallet, recordKey, []byte("unchanged"),
					dkvs.RecordOptions{IssueHeight: 1}, DKVSAutopayOptions{PoolContract: "review-fixture-pool", AddressParams: &chaincfg.TestNetParams})
				require.NoError(t, err)
			}
			prefix, err := dkvs.CollectionPathForKey(key)
			require.NoError(t, err)
			before, err := source.indexer.GetPathSnapshot(prefix)
			require.NoError(t, err)
			require.Len(t, before.Records, 4)
			privateKey, err := btcec.NewPrivateKey()
			require.NoError(t, err)
			peer := &p2p.PeerState{}
			t.Cleanup(peer.Close)
			var responses []*wire.MsgDKVSSyncResponse
			serving := p2p.Handler{
				Store: pwaReviewPeerStore{source.indexer}, Peer: peer, Node: &p2p.NodeState{},
				Net: chaincfg.TestNetParams.Net, MirrorAuthority: true,
				Sign: func(payload []byte) ([]byte, error) {
					return ecdsa.Sign(privateKey, chainhash.HashB(payload)).Serialize(), nil
				},
				Send: func(message wire.Message) {
					if response, ok := message.(*wire.MsgDKVSSyncResponse); ok {
						responses = append(responses, response)
					}
				},
			}
			request := &wire.MsgDKVSSyncRequest{SessionID: 1, Limit: 2, Filters: []wire.DKVSSyncFilter{{Type: "path", Target: prefix}}}
			serving.OnSyncRequest(request)
			require.Len(t, responses, 1)
			require.Len(t, responses[0].Records, 2, "the first page includes metadata and a real record")
			require.True(t, p2p.VerifySyncSignature(serving.Net, hex.EncodeToString(privateKey.PubKey().SerializeCompressed()), nil, request.Filters, responses[0]))
			require.False(t, responses[0].Done)
			request.Cursor = responses[0].NextCursor
			source.height++
			require.NoError(t, source.indexer.RefreshPaidRetentionAt(source.height))
			if changeSource {
				_, err := client.PutSignedRecordWithAutopay(manager.wallet,
					accountTestKey(t, manager, "pwa-final-paging/d"), []byte("changed after first page"),
					dkvs.RecordOptions{IssueHeight: source.height},
					DKVSAutopayOptions{PoolContract: "review-fixture-pool", AddressParams: &chaincfg.TestNetParams})
				require.NoError(t, err)
			}
			after, err := source.indexer.GetPathSnapshot(prefix)
			require.NoError(t, err)
			if changeSource {
				require.NotEqual(t, before.PathMeta.StateRoot, after.PathMeta.StateRoot)
			} else {
				require.Equal(t, before.PathMeta.StateRoot, after.PathMeta.StateRoot, "only the block height changed")
			}
			serving.OnSyncRequest(request)
			require.Len(t, responses, 2, "the captured snapshot must keep paging while the live source advances")
			require.True(t, p2p.VerifySyncSignature(serving.Net, hex.EncodeToString(privateKey.PubKey().SerializeCompressed()), request.Cursor, request.Filters, responses[1]))
			require.Equal(t, before.PathMeta.StateRoot, responses[1].CheckpointRoot)
			require.Len(t, responses[1].Records, 2)
			require.False(t, responses[1].Done)
			request.Cursor = responses[1].NextCursor
			source.height++
			require.NoError(t, source.indexer.RefreshPaidRetentionAt(source.height))
			serving.OnSyncRequest(request)
			require.Len(t, responses, 3)
			require.True(t, p2p.VerifySyncSignature(serving.Net, hex.EncodeToString(privateKey.PubKey().SerializeCompressed()), request.Cursor, request.Filters, responses[2]))
			require.Equal(t, before.PathMeta.StateRoot, responses[2].CheckpointRoot)
			require.Len(t, responses[2].Records, 1)
			require.True(t, responses[2].Done)
			assembled := append([]*wire.DKVSRecord(nil), responses[0].Records[1:]...)
			assembled = append(assembled, responses[1].Records...)
			assembled = append(assembled, responses[2].Records...)
			require.Len(t, assembled, len(before.Records))
			for index, record := range before.Records {
				require.Equal(t, dkvs.RecordHash(record), dkvs.RecordHash(assembled[index]))
			}
		})
	}
}

// Test-only interface adapter; all storage and sync behavior is delegated to
// the production Indexer. No alternate KV algorithm or event log is supplied.
type pwaReviewPeerStore struct{ backend *dkvs.Indexer }

func (s pwaReviewPeerStore) PutRemoteDKVSRecord(r *wire.DKVSRecord) (bool, error) {
	return s.backend.AcceptCurrentRecord(r)
}
func (s pwaReviewPeerStore) AcceptDKVSCurrentRecord(r *wire.DKVSRecord) (bool, error) {
	return s.backend.AcceptCurrentRecord(r)
}
func (s pwaReviewPeerStore) GetDKVSRecordForRelay(k string) (*wire.DKVSRecord, error) {
	return s.backend.GetForRelay(k)
}
func (s pwaReviewPeerStore) GetDKVSRecordByHashForRelay(h chainhash.Hash) (*wire.DKVSRecord, error) {
	return s.backend.GetByHashForRelay(h)
}
func (s pwaReviewPeerStore) SyncFilteredDKVSRecords(cursor []byte, limit uint32, filters []dkvs.Subscription) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	return s.backend.SyncFiltered(cursor, limit, filters)
}
func (s pwaReviewPeerStore) ApplyDKVSMirror([]dkvs.Subscription, []*wire.DKVSRecord, chainhash.Hash) (int, error) {
	return 0, fmt.Errorf("unexpected generic mirror")
}
func (s pwaReviewPeerStore) GetDKVSPathSnapshot(path string) (*dkvs.PathSnapshot, error) {
	return s.backend.GetPathSnapshot(path)
}
func (s pwaReviewPeerStore) ApplyDKVSPathSnapshot(snapshot *dkvs.PathSnapshot) (int, error) {
	return s.backend.ApplyPathSnapshot(snapshot)
}
func (s pwaReviewPeerStore) DKVSNetworkSyncBaseline(path string) (dkvs.ActiveMeta, error) {
	return s.backend.NetworkSyncBaseline(path)
}
func (s pwaReviewPeerStore) ApplyDKVSPathSnapshotFrom(snapshot *dkvs.PathSnapshot, baseline dkvs.ActiveMeta) (int, error) {
	return s.backend.ApplyPathSnapshotFrom(snapshot, baseline)
}
func (s pwaReviewPeerStore) DKVSNetworkPaths() ([]string, error) { return s.backend.NetworkPaths() }
func (s pwaReviewPeerStore) ListDKVSSubscriptions() []dkvs.Subscription {
	return s.backend.Subscriptions()
}
func (s pwaReviewPeerStore) IsDKVSSubscribed(key string) bool { return s.backend.IsSubscribed(key) }

type pwaReviewFeeVerifier struct {
	dkvs.JSONFeeVerifier
	height *uint64
}

func (v pwaReviewFeeVerifier) PaidRecordRetention(*wire.DKVSRecord, dkvs.ParsedKey) (dkvs.PaidRecordRetention, error) {
	return dkvs.PaidRecordRetention{CurrentBlock: *v.height, LastPayHeight: *v.height}, nil
}
