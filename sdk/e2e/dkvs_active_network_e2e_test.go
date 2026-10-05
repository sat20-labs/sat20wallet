package e2e

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	dkvsp2p "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs/p2p"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Public SDK mutations, captured committed P2P envelopes, production handlers
// and signed snapshots. Delivery is stepped to verify concurrency boundaries.
func TestSDKDKVSActiveNetwork(t *testing.T) {
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	key := func(t *testing.T, suffix string) string {
		t.Helper()
		key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), suffix)
		require.NoError(t, err)
		return key
	}
	put := func(t *testing.T, store *releaseReviewStore, key, value string) *wire.DKVSRecord {
		t.Helper()
		r, err := store.client.PutSignedRecordWithAutopay(owner.Wallet, key, []byte(value), dkvs.RecordOptions{IssueHeight: store.height.Load()}, releaseReviewAutopay())
		require.NoError(t, err)
		return r
	}

	t.Run("OnlinePutUpdateDeleteUsesSingleKeyNotSnapshot", func(t *testing.T) {
		p := newGenerationPeerPair(t)
		key := key(t, "active-network-online/value")
		first := put(t, p.source, key, "v1")
		p.receiver.OnNotify(p.notification(t, first))
		p.requireValue(t, first)
		second := put(t, p.source, key, "v2")
		p.receiver.OnNotify(p.notification(t, second))
		p.requireValue(t, second)
		deleted, err := p.source.client.DeleteCurrentRecord(owner.Wallet, key, 100)
		require.NoError(t, err)
		oldDelete := p.notification(t, deleted)
		p.receiver.OnNotify(oldDelete)
		_, err = p.target.client.GetRecord(key)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		state, err := p.target.client.GetKeyState(key)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateNeverSeen, state.Status)
		p.source.height.Store(101)
		p.target.height.Store(101)
		recreated := put(t, p.source, key, "new-life")
		require.Equal(t, uint64(1), recreated.Seq)
		p.receiver.OnNotify(p.notification(t, recreated))
		p.requireValue(t, recreated)
		require.Empty(t, p.requests, "normal online mutations must not trigger directory snapshots")
		p.targetPeer.Close()
		p.targetPeer = &dkvsp2p.PeerState{}
		t.Cleanup(p.targetPeer.Close)
		p.receiver.Peer = p.targetPeer
		p.receiver.OnNotify(oldDelete)
		p.requireValue(t, recreated)
	})

	t.Run("ReconnectRepairsMissedDeletionWithoutDeleteHistory", func(t *testing.T) {
		p := newGenerationPeerPair(t)
		a := put(t, p.source, key(t, "active-network-reconnect/a"), "a")
		b := put(t, p.source, key(t, "active-network-reconnect/b"), "b")
		p.receiver.OnNotify(p.notification(t, a))
		p.receiver.OnNotify(p.notification(t, b))
		_, err := p.source.client.DeleteCurrentRecord(owner.Wallet, a.Key, 100)
		require.NoError(t, err)
		b2 := put(t, p.source, b.Key, "b2")
		p.receiver.OnTrustedConnected()
		p.deliver(t)
		_, err = p.target.client.GetRecord(a.Key)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		p.requireValue(t, b2)
		prefix, err := dkvs.CollectionPathForKey(a.Key)
		require.NoError(t, err)
		snapshot, err := p.target.backend.GetPathSnapshot(prefix)
		require.NoError(t, err)
		require.Len(t, snapshot.Records, 1)
		require.Zero(t, snapshot.Records[0].Flags)
		encoded, err := json.Marshal(snapshot)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "delete_floors")
		_, err = p.source.client.DeleteCurrentRecord(owner.Wallet, b.Key, 100)
		require.NoError(t, err)
		p.receiver.OnTrustedConnected()
		p.deliver(t)
		_, err = p.target.client.GetRecord(b.Key)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound, "empty directories must be reconciled")
	})

	t.Run("ChosenPrefixSyncSourceDefinesCompletedView", func(t *testing.T) {
		p := newGenerationPeerPair(t)
		a := put(t, p.source, key(t, "active-network-stale/a"), "a1")
		p.receiver.OnNotify(p.notification(t, a))
		_ = put(t, p.target, a.Key, "locally-a2")
		extra := put(t, p.target, key(t, "active-network-stale/b"), "locally-b")
		prefix, err := dkvs.CollectionPathForKey(a.Key)
		require.NoError(t, err)
		p.receiver.QueuePathSync(prefix)
		p.deliver(t)
		p.requireValue(t, a)
		_, err = p.target.client.GetRecord(extra.Key)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
	})

	t.Run("SnapshotCommitRechecksLocalBaseline", func(t *testing.T) {
		p := newGenerationPeerPair(t)
		a := put(t, p.source, key(t, "active-network-race/a"), "a1")
		p.receiver.OnNotify(p.notification(t, a))
		put(t, p.source, a.Key, "remote-a2")
		prefix, err := dkvs.CollectionPathForKey(a.Key)
		require.NoError(t, err)
		p.receiver.QueuePathSync(prefix)
		require.Len(t, p.requests, 1)
		request := p.requests[0]
		p.requests = nil
		p.serving.OnSyncRequest(request)
		require.Len(t, p.responses, 1)
		local := put(t, p.target, a.Key, "local-during-download")
		p.receiver.OnSyncResponse(p.responses[0])
		p.responses = nil
		p.requireValue(t, local)
	})

	t.Run("NetworkSnapshotPreservesUnrelatedEndpointLocalData", func(t *testing.T) {
		p := newGenerationPeerPair(t)
		a := put(t, p.source, key(t, "active-network-scope/a"), "paid")
		localKey := key(t, "active-network-scope/private-cache")
		local, err := p.target.client.PutSignedRecordFreeLocal(owner.Wallet, localKey, []byte("local-only"), dkvs.RecordOptions{IssueHeight: 100, TTL: 10})
		require.NoError(t, err)
		p.receiver.OnNotify(p.notification(t, a))
		p.requireValue(t, a)
		prefix, err := dkvs.CollectionPathForKey(a.Key)
		require.NoError(t, err)
		p.receiver.QueuePathSync(prefix)
		p.deliver(t)
		p.requireValue(t, local)
		snapshot, err := p.target.backend.GetPathSnapshot(prefix)
		require.NoError(t, err)
		require.Len(t, snapshot.Records, 1)
		require.Equal(t, a.Key, snapshot.Records[0].Key)
	})
}

type activePeerStore struct{ releaseReviewPeerStore }

func (s activePeerStore) AcceptDKVSCurrentRecord(r *wire.DKVSRecord) (bool, error) {
	return s.backend.AcceptCurrentRecord(r)
}
func (s activePeerStore) DKVSNetworkSyncBaseline(path string) (dkvs.ActiveMeta, error) {
	return s.backend.NetworkSyncBaseline(path)
}
func (s activePeerStore) ApplyDKVSPathSnapshotFrom(snapshot *dkvs.PathSnapshot, baseline dkvs.ActiveMeta) (int, error) {
	return s.backend.ApplyPathSnapshotFrom(snapshot, baseline)
}
func (s activePeerStore) DKVSNetworkPaths() ([]string, error) { return s.backend.NetworkPaths() }

var _ dkvsp2p.Store = activePeerStore{}

type activePeerPair struct {
	source, target    *releaseReviewStore
	receiver, serving dkvsp2p.Handler
	targetPeer        *dkvsp2p.PeerState
	requests          []*wire.MsgDKVSSyncRequest
	responses         []*wire.MsgDKVSSyncResponse
}

func newActivePeerPair(t *testing.T) *activePeerPair {
	t.Helper()
	p := &activePeerPair{source: newReleaseReviewStore(t), target: newReleaseReviewStore(t), targetPeer: &dkvsp2p.PeerState{}}
	validator, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	p.serving = dkvsp2p.Handler{
		Store: activePeerStore{releaseReviewPeerStore{p.source.backend}}, Peer: &dkvsp2p.PeerState{}, Node: &dkvsp2p.NodeState{},
		Net: chaincfg.TestNetParams.Net, LocalServices: wire.SFNodeMiner, RemoteServices: wire.SFNodeMiner, MirrorAuthority: true,
		Sign: func(payload []byte) ([]byte, error) {
			return ecdsa.Sign(validator, chainhash.HashB(payload)).Serialize(), nil
		},
		Send: func(msg wire.Message) {
			if r, ok := msg.(*wire.MsgDKVSSyncResponse); ok {
				p.responses = append(p.responses, r)
			}
		}, Warnf: t.Logf,
	}
	receiverNode := &dkvsp2p.NodeState{}
	receiverNode.SetReady(true)
	p.receiver = dkvsp2p.Handler{
		Store: activePeerStore{releaseReviewPeerStore{p.target.backend}}, Peer: p.targetPeer, Node: receiverNode,
		Net: chaincfg.TestNetParams.Net, LocalServices: wire.SFNodeMiner, RemoteServices: wire.SFNodeMiner,
		TrustedSource: true, ValidatorID: hex.EncodeToString(validator.PubKey().SerializeCompressed()),
		Send: func(msg wire.Message) {
			if r, ok := msg.(*wire.MsgDKVSSyncRequest); ok {
				p.requests = append(p.requests, r)
			}
		}, Warnf: t.Logf,
	}
	t.Cleanup(p.serving.Peer.Close)
	t.Cleanup(p.targetPeer.Close)
	return p
}
func (p *activePeerPair) requireValue(t *testing.T, want *wire.DKVSRecord) {
	t.Helper()
	actual, err := p.target.client.GetRecord(want.Key)
	require.NoError(t, err)
	require.Equal(t, dkvs.RecordHash(want), dkvs.RecordHash(actual))
}
func (p *activePeerPair) deliver(t *testing.T) {
	t.Helper()
	for n := 0; len(p.requests)+len(p.responses) != 0; n++ {
		require.Less(t, n, 128, "P2P synchronization must terminate without looping")
		requests := p.requests
		p.requests = nil
		for _, request := range requests {
			p.serving.OnSyncRequest(request)
		}
		responses := p.responses
		p.responses = nil
		for _, response := range responses {
			p.receiver.OnSyncResponse(response)
		}
	}
}
