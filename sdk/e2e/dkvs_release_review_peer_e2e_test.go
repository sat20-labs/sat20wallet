package e2e

import (
	"encoding/hex"
	"fmt"
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

// Actual signed P2P pages and response state machine, with stepped in-process
// delivery to reproduce stale replies deterministically. This is not a TCP test.
func TestSDKDKVSReleaseReviewPeer(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		name := "UntrustedPeerCannotReplaceState"
		if trusted {
			name = "ChosenTrustedSyncSourceDefinesCurrentView"
		}
		t.Run(name, func(t *testing.T) {
			source, target := newReleaseReviewStore(t), newReleaseReviewStore(t)
			owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
			validator, err := btcec.NewPrivateKey()
			require.NoError(t, err)
			sourceID := hex.EncodeToString(validator.PubKey().SerializeCompressed())
			key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "release-peer/value")
			require.NoError(t, err)
			first, err := source.client.PutSignedRecordWithAutopay(owner.Wallet, key, []byte("v1"),
				dkvs.RecordOptions{IssueHeight: 100}, releaseReviewAutopay())
			require.NoError(t, err)
			updated, err := target.backend.AcceptCurrentRecord(first)
			require.NoError(t, err)
			require.True(t, updated)
			latest, err := target.client.PutSignedRecordWithAutopay(owner.Wallet, key, []byte("acknowledged-v2"),
				dkvs.RecordOptions{IssueHeight: 100}, releaseReviewAutopay())
			require.NoError(t, err)
			prefix, err := dkvs.CollectionPathForKey(key)
			require.NoError(t, err)
			extra, err := target.client.PutSignedRecordWithAutopay(owner.Wallet, prefix+"/sibling", []byte("new-local-key"),
				dkvs.RecordOptions{IssueHeight: 100}, releaseReviewAutopay())
			require.NoError(t, err)
			var response *wire.MsgDKVSSyncResponse
			var sourcePeer, targetPeer dkvsp2p.PeerState
			var sourceNode, targetNode dkvsp2p.NodeState
			sourceNode.SetReady(true)
			targetNode.SetReady(true)
			serving := dkvsp2p.Handler{
				Store: activePeerStore{releaseReviewPeerStore{source.backend}}, Peer: &sourcePeer, Node: &sourceNode,
				Net: chaincfg.TestNetParams.Net, LocalServices: wire.SFNodeMiner, RemoteServices: wire.SFNodeMiner,
				MirrorAuthority: true,
				Sign: func(payload []byte) ([]byte, error) {
					return ecdsa.Sign(validator, chainhash.HashB(payload)).Serialize(), nil
				},
				Send: func(msg wire.Message) {
					if r, ok := msg.(*wire.MsgDKVSSyncResponse); ok {
						response = r
					}
				},
			}
			receiving := dkvsp2p.Handler{
				Store: activePeerStore{releaseReviewPeerStore{target.backend}}, Peer: &targetPeer, Node: &targetNode,
				Net: chaincfg.TestNetParams.Net, LocalServices: wire.SFNodeMiner, RemoteServices: wire.SFNodeMiner,
				TrustedSource: trusted, ValidatorID: sourceID, Warnf: t.Logf,
			}
			// Start through the node scheduler, capturing the production fence.
			// The untrusted control then revokes source eligibility before the
			// signed response is received, exercising response admission too.
			var request *wire.MsgDKVSSyncRequest
			receiving.Send = func(msg wire.Message) { request, _ = msg.(*wire.MsgDKVSSyncRequest) }
			initiating := receiving
			initiating.TrustedSource = true
			initiating.QueuePathSync(prefix)
			require.NotNil(t, request)
			t.Cleanup(sourcePeer.Close)
			t.Cleanup(targetPeer.Close)
			serving.OnSyncRequest(request)
			require.NotNil(t, response)
			require.True(t, response.Done)
			require.True(t, dkvsp2p.VerifySyncSignature(receiving.Net, sourceID, request.Cursor, request.Filters, response))
			receiving.OnSyncResponse(response)
			actual, err := target.client.GetRecord(key)
			require.NoError(t, err)
			if trusted {
				require.Equal(t, dkvs.RecordHash(first), dkvs.RecordHash(actual))
				_, err = target.client.GetRecord(extra.Key)
				require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
			} else {
				require.Equal(t, dkvs.RecordHash(latest), dkvs.RecordHash(actual))
				actual, err = target.client.GetRecord(extra.Key)
				require.NoError(t, err)
				require.Equal(t, dkvs.RecordHash(extra), dkvs.RecordHash(actual))
			}
		})
	}
}

type releaseReviewPeerStore struct{ backend *dkvs.Indexer }

func (s releaseReviewPeerStore) PutRemoteDKVSRecord(r *wire.DKVSRecord) (bool, error) {
	return s.backend.AcceptRemoteRecord(r, "release-review-peer")
}
func (s releaseReviewPeerStore) GetDKVSRecordForRelay(k string) (*wire.DKVSRecord, error) {
	return s.backend.GetForRelay(k)
}
func (s releaseReviewPeerStore) GetDKVSRecordByHashForRelay(h chainhash.Hash) (*wire.DKVSRecord, error) {
	return s.backend.GetByHashForRelay(h)
}
func (s releaseReviewPeerStore) SyncFilteredDKVSRecords(cursor []byte, limit uint32, filters []dkvs.Subscription) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	return s.backend.SyncFiltered(cursor, limit, filters)
}
func (s releaseReviewPeerStore) ApplyDKVSMirror([]dkvs.Subscription, []*wire.DKVSRecord, chainhash.Hash) (int, error) {
	return 0, fmt.Errorf("unexpected generic mirror in path-snapshot test")
}
func (s releaseReviewPeerStore) GetDKVSPathSnapshot(path string) (*dkvs.PathSnapshot, error) {
	return s.backend.GetPathSnapshot(path)
}
func (s releaseReviewPeerStore) ApplyDKVSPathSnapshot(snapshot *dkvs.PathSnapshot) (int, error) {
	return s.backend.ApplyPathSnapshot(snapshot)
}
func (s releaseReviewPeerStore) ListDKVSSubscriptions() []dkvs.Subscription {
	return s.backend.Subscriptions()
}
func (s releaseReviewPeerStore) IsDKVSSubscribed(key string) bool { return s.backend.IsSubscribed(key) }

func (s releaseReviewPeerStore) AcceptDKVSCurrentRecord(record *wire.DKVSRecord) (bool, error) {
	return s.backend.AcceptCurrentRecord(record)
}
func (s releaseReviewPeerStore) DKVSNetworkSyncBaseline(path string) (dkvs.ActiveMeta, error) {
	return s.backend.NetworkSyncBaseline(path)
}
func (s releaseReviewPeerStore) ApplyDKVSPathSnapshotFrom(snapshot *dkvs.PathSnapshot, baseline dkvs.ActiveMeta) (int, error) {
	return s.backend.ApplyPathSnapshotFrom(snapshot, baseline)
}
func (s releaseReviewPeerStore) DKVSNetworkPaths() ([]string, error) { return s.backend.NetworkPaths() }
