package wallet

import (
	"encoding/hex"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	p2p "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs/p2p"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Real Indexers and signed production Handler messages. Reducing request Limit
// exercises ordinary inventory paging without creating hundreds of records.
func TestDKVSReviewDiscoveryCompletesWhileSourceWrites(t *testing.T) {
	manager, _ := finalDKVSTestManager(t)
	source, target := newSatoshiNetDKVSTestTransport(), newSatoshiNetDKVSTestTransport()
	for _, transport := range []*satoshinetDKVSTestTransport{source, target} {
		transport.indexer.SetFeeVerifier(pwaReviewFeeVerifier{JSONFeeVerifier: dkvs.JSONFeeVerifier{AllowFreeLocal: true}, height: &transport.height})
	}
	client := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", source).WithWriteSigner(manager.wallet)
	options := DKVSAutopayOptions{PoolContract: "review-fixture-pool", AddressParams: &chaincfg.TestNetParams}
	for _, suffix := range []string{"a", "b", "c", "d"} {
		_, err := client.PutSignedRecordWithAutopay(manager.wallet, accountTestKey(t, manager, "review-discovery/"+suffix),
			[]byte("before"), dkvs.RecordOptions{IssueHeight: 1}, options)
		require.NoError(t, err)
	}
	key := accountTestKey(t, manager, "review-discovery/d")
	prefix, err := dkvs.CollectionPathForKey(key)
	require.NoError(t, err)
	before, err := source.indexer.GetPathSnapshot(prefix)
	require.NoError(t, err)
	identity, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	peer, servingPeer := &p2p.PeerState{}, &p2p.PeerState{}
	defer peer.Close()
	defer servingPeer.Close()
	requests := make(chan *wire.MsgDKVSSyncRequest, 16)
	responses := make(chan *wire.MsgDKVSSyncResponse, 16)
	node := &p2p.NodeState{}
	receiver := p2p.Handler{Store: pwaReviewPeerStore{target.indexer}, Node: node, Peer: peer,
		LocalServices: wire.SFNodeMiner, TrustedSource: true, Net: chaincfg.TestNetParams.Net,
		ValidatorID: hex.EncodeToString(identity.PubKey().SerializeCompressed()),
		Send: func(m wire.Message) {
			if r, ok := m.(*wire.MsgDKVSSyncRequest); ok {
				requests <- r
			}
		}}
	serving := p2p.Handler{Store: pwaReviewPeerStore{source.indexer}, Node: &p2p.NodeState{}, Peer: servingPeer,
		MirrorAuthority: true, Net: receiver.Net,
		Sign: func(payload []byte) ([]byte, error) {
			return ecdsa.Sign(identity, chainhash.HashB(payload)).Serialize(), nil
		},
		Send: func(m wire.Message) {
			if r, ok := m.(*wire.MsgDKVSSyncResponse); ok {
				responses <- r
			}
		}}
	receiver.QueueSync(nil)
	first := <-requests
	first.Limit = 2
	require.Empty(t, first.Filters, "exercise directory discovery, not the already fixed prefix snapshot")
	serving.OnSyncRequest(first)
	pageOne := <-responses
	require.False(t, pageOne.Done)
	require.Len(t, pageOne.Records, 2)
	require.True(t, p2p.VerifySyncSignature(receiver.Net, receiver.ValidatorID, first.Cursor, first.Filters, pageOne))
	receiver.OnSyncResponse(pageOne)
	second := <-requests
	second.Limit = 2
	updated, err := client.PutSignedRecordWithAutopay(manager.wallet, key, []byte("after page one"), dkvs.RecordOptions{IssueHeight: 1}, options)
	require.NoError(t, err)
	after, err := source.indexer.GetPathSnapshot(prefix)
	require.NoError(t, err)
	require.NotEqual(t, before.PathMeta.StateRoot, after.PathMeta.StateRoot, "the live source really changed")
	receiver.OnNotify(p2p.NotifyForRecord(updated))
	serving.OnSyncRequest(second)
	pageTwo := <-responses
	require.True(t, p2p.VerifySyncSignature(receiver.Net, receiver.ValidatorID, second.Cursor, second.Filters, pageTwo))
	t.Logf("discovery page roots: first=%s second=%s", pageOne.CheckpointRoot, pageTwo.CheckpointRoot)
	receiver.OnSyncResponse(pageTwo)
	// Drain the optional final empty inventory page, then expect prefix work.
	for n := 0; n < 3; n++ {
		select {
		case next := <-requests:
			if len(next.Filters) == 1 && next.Filters[0].Type == "path" && next.Filters[0].Target == prefix {
				require.False(t, node.Ready(), "discovery cannot make the node READY before installation")
				serving.OnSyncRequest(next)
				receiver.OnSyncResponse(<-responses)
				require.True(t, node.Ready(), "validated snapshots and pending Notify must complete the node sync")
				installed, err := (pwaReviewPeerStore{target.indexer}).GetDKVSRecordForRelay(key)
				require.NoError(t, err)
				require.Equal(t, dkvs.RecordHash(updated), dkvs.RecordHash(installed))
				return
			}
			if len(next.Cursor) == 0 {
				t.Fatal("directory discovery restarted after an unrelated live write")
			}
			serving.OnSyncRequest(next)
			receiver.OnSyncResponse(<-responses)
		default:
			t.Fatalf("discovery discarded accepted pages after the live root changed; no snapshot request for %s, READY=%t", prefix, node.Ready())
		}
	}
	t.Fatal("discovery did not advance to the discovered collection")
}
