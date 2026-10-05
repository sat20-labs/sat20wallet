package e2e

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	dkvsp2p "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs/p2p"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// SDK writes, real Indexer/Pebble and production P2P handlers. Realtime
// notifications carry only signed KV operations; prefix synchronization is the
// convergence mechanism for missed or ambiguous realtime state.
func TestSDKDKVSLaunchReviewNetwork(t *testing.T) {
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	key := func(t *testing.T, suffix string) string {
		t.Helper()
		k, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "launch-review/"+suffix)
		require.NoError(t, err)
		return k
	}
	put := func(t *testing.T, p *generationPeerPair, k, value string) *wire.DKVSRecord {
		t.Helper()
		r, err := p.source.client.PutSignedRecordWithAutopay(owner.Wallet, k, []byte(value), dkvs.RecordOptions{IssueHeight: p.source.height.Load()}, releaseReviewAutopay())
		require.NoError(t, err)
		return r
	}
	pathOf := func(t *testing.T, k string) string {
		t.Helper()
		path, err := dkvs.CollectionPathForKey(k)
		require.NoError(t, err)
		return path
	}
	freshPeer := func(t *testing.T, p *generationPeerPair) {
		t.Helper()
		p.targetPeer.Close()
		p.targetPeer = &dkvsp2p.PeerState{}
		t.Cleanup(p.targetPeer.Close)
		p.receiver.Peer = p.targetPeer
	}
	assertAbsent := func(t *testing.T, p *generationPeerPair, k string) {
		t.Helper()
		got, err := p.target.client.GetRecord(k)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound, "deleted state was resurrected: record=%+v", got)
	}

	t.Run("ControlOnlineSameSourceUpdatesAndDeletes", func(t *testing.T) {
		p := newGenerationPeerPair(t)
		first := put(t, p, key(t, "control"), "one")
		p.receiver.OnNotify(p.notification(t, first))
		second := put(t, p, first.Key, "two")
		p.receiver.OnNotify(p.notification(t, second))
		p.requireValue(t, second)
		deleted, err := p.source.client.DeleteCurrentRecord(owner.Wallet, first.Key, 100)
		require.NoError(t, err)
		p.receiver.OnNotify(p.notification(t, deleted))
		assertAbsent(t, p, first.Key)
		require.Empty(t, p.requests, "ordinary notifications should not require snapshots")
	})

	t.Run("OfflinePaidDeleteAndSameBlockRecreateConverges", func(t *testing.T) {
		p := newGenerationPeerPair(t)
		first := put(t, p, key(t, "recreate-same-block"), "old-one")
		p.receiver.OnNotify(p.notification(t, first))
		old := put(t, p, first.Key, "old-two")
		p.receiver.OnNotify(p.notification(t, old))
		p.requireValue(t, old)
		_, err := p.source.client.DeleteCurrentRecord(owner.Wallet, first.Key, 100)
		require.NoError(t, err)
		fresh := put(t, p, first.Key, "new-incarnation")
		require.Equal(t, uint64(1), fresh.Seq)
		require.Equal(t, uint64(2), old.Seq)
		require.Equal(t, old.IssueHeight, fresh.IssueHeight)
		p.receiver.QueuePathSync(pathOf(t, first.Key))
		p.deliver(t)
		actual, err := p.target.client.GetRecord(first.Key)
		require.NoError(t, err)
		t.Logf("launch-review: old_seq=%d new_seq=%d source_value=%q target_value=%q", old.Seq, fresh.Seq, fresh.Value, actual.Value)
		require.Equal(t, dkvs.RecordHash(fresh), dkvs.RecordHash(actual), "current source generation must allow a legitimate recreated lifecycle")
	})

	t.Run("OfflinePaidRecreateAtLaterHeightConverges", func(t *testing.T) {
		p := newGenerationPeerPair(t)
		first := put(t, p, key(t, "recreate-later-block"), "old-one")
		p.receiver.OnNotify(p.notification(t, first))
		old := put(t, p, first.Key, "old-two")
		p.receiver.OnNotify(p.notification(t, old))
		_, err := p.source.client.DeleteCurrentRecord(owner.Wallet, first.Key, 100)
		require.NoError(t, err)
		p.source.height.Store(101)
		p.target.height.Store(101)
		fresh := put(t, p, first.Key, "newer-height-new-life")
		require.NoError(t, p.target.backend.RefreshPaidRetentionAt(101))
		require.Greater(t, fresh.IssueHeight, old.IssueHeight)
		p.receiver.QueuePathSync(pathOf(t, first.Key))
		p.deliver(t)
		p.requireValue(t, fresh)
	})

	t.Run("ValidSeqOneNotifyAfterEmptySyncIsAcceptedThenReconciled", func(t *testing.T) {
		p := newGenerationPeerPair(t)
		old := put(t, p, key(t, "notify-replay"), "valid-seq-one")
		captured := p.notification(t, old)
		p.receiver.OnNotify(captured)
		_, err := p.source.client.DeleteCurrentRecord(owner.Wallet, old.Key, 100)
		require.NoError(t, err)
		p.receiver.QueuePathSync(pathOf(t, old.Key))
		p.deliver(t)
		assertAbsent(t, p, old.Key)

		freshPeer(t, p)
		p.receiver.TrustedSource = false
		p.receiver.OnNotify(captured)
		assertAbsent(t, p, old.Key)

		freshPeer(t, p)
		p.receiver.TrustedSource = true
		p.receiver.OnNotify(captured)
		p.requireValue(t, old)

		// The source's complete current view is still empty, so normal
		// anti-entropy/current-prefix synchronization removes the stale value.
		p.receiver.QueuePathSync(pathOf(t, old.Key))
		p.deliver(t)
		assertAbsent(t, p, old.Key)
	})

	t.Run("DelayedRequestedDataCannotResurrectAfterDelete", func(t *testing.T) {
		p := newGenerationPeerPair(t)
		old := put(t, p, key(t, "data-replay"), "requested-before-delete")
		p.receiver.OnNotify(p.notification(t, old))
		request := &wire.MsgDKVSGet{RecordHashes: []chainhash.Hash{dkvs.RecordHash(old)}}
		p.targetPeer.TrackRequest(request, time.Now())
		var pending []*wire.MsgDKVSData
		p.serving.Send = func(message wire.Message) { if response, ok := message.(*wire.MsgDKVSData); ok { pending = append(pending, response) } }
		p.serving.OnGet(request)
		require.Len(t, pending, 1)
		require.Len(t, pending[0].Records, 1)
		deleted, err := p.source.client.DeleteCurrentRecord(owner.Wallet, old.Key, 100)
		require.NoError(t, err)
		p.receiver.OnNotify(p.notification(t, deleted))
		assertAbsent(t, p, old.Key)
		p.receiver.OnData(pending[0])
		assertAbsent(t, p, old.Key)
	})

	t.Run("VerifiedSameRootAlternativePeerCanContinueReplication", func(t *testing.T) {
		p := newGenerationPeerPair(t)
		first := put(t, p, key(t, "alternative-source"), "old")
		p.receiver.OnNotify(p.notification(t, first))
		p.requireValue(t, first)
		alternate := newReleaseReviewStore(t)
		updated, err := alternate.backend.AcceptCurrentRecord(first)
		require.NoError(t, err)
		require.True(t, updated)
		validator, err := btcec.NewPrivateKey()
		require.NoError(t, err)
		p.serving.Peer.Close()
		p.serving.Peer = &dkvsp2p.PeerState{}
		t.Cleanup(p.serving.Peer.Close)
		p.serving.Store = activePeerStore{releaseReviewPeerStore{alternate.backend}}
		p.serving.Sign = func(payload []byte) ([]byte, error) { return ecdsa.Sign(validator, chainhash.HashB(payload)).Serialize(), nil }
		freshPeer(t, p)
		p.receiver.ValidatorID = hex.EncodeToString(validator.PubKey().SerializeCompressed())
		path := pathOf(t, first.Key)
		p.receiver.QueuePathSync(path)
		p.deliver(t)
		p.requireValue(t, first)
		second := put(t, p, first.Key, "new-through-alternative")
		updated, err = alternate.backend.AcceptCurrentRecord(second)
		require.NoError(t, err)
		require.True(t, updated)
		// This relay only has content, not the original peer's commit position.
		// Its hint must trigger a signed current view from the alternative.
		p.receiver.OnNotify(dkvsp2p.NotifyForRecord(second))
		p.deliver(t)
		p.receiver.QueuePathSync(path)
		p.deliver(t)
		p.requireValue(t, second)
	})
}
