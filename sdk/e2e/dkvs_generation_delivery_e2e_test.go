package e2e

import (
	"bytes"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Realtime P2P carries only signed KV operations. Generation is deliberately
// absent from every scenario in this file.
func TestSDKDKVSGenerationDelivery(t *testing.T) {
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	key := func(t *testing.T, path string) string {
		t.Helper()
		k, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), path)
		require.NoError(t, err)
		return k
	}
	put := func(t *testing.T, p *generationPeerPair, k, value string) *wire.DKVSRecord {
		t.Helper()
		record, err := p.source.client.PutSignedRecordWithAutopay(
			owner.Wallet, k, []byte(value),
			dkvs.RecordOptions{IssueHeight: 100}, releaseReviewAutopay(),
		)
		require.NoError(t, err)
		return record
	}

	t.Run("MultipleKeysFromOneBatchAreNotDropped", func(t *testing.T) {
		p := newGenerationPeerPair(t)
		var records []*wire.DKVSRecord
		var mutations []dkvs.CASMutation
		for _, suffix := range []string{"a", "b", "c"} {
			k := key(t, "p2p-batch/"+suffix)
			record, err := wallet.NewDKVSSignedRecord(
				owner.Wallet, k, []byte(suffix),
				dkvs.RecordOptions{Seq: 1, IssueHeight: 100},
			)
			require.NoError(t, err)
			proof, err := dkvs.NewAutopayFeeProof(
				k, "personal", wire.MaxDKVSRecordSize, 0,
				releaseReviewAutopay().PoolContract, "",
			)
			require.NoError(t, err)
			require.NoError(t, wallet.AttachDKVSFeeProof(record, proof))
			require.NoError(t, wallet.SignDKVSRecord(owner.Wallet, record))
			records = append(records, record)
			mutations = append(mutations, sdkDKVSReviewAbsent(record))
		}
		result, err := p.source.client.WithWriteSigner(owner.Wallet).PutRecordBatchCAS(mutations)
		require.NoError(t, err)
		require.Equal(t, 3, result.Applied)

		for n := len(records)-1; n >= 0; n-- {
			p.receiver.OnNotify(p.notification(t, records[n]))
		}
		for _, record := range records {
			p.requireValue(t, record)
		}

		prefix, err := dkvs.CollectionPathForKey(records[0].Key)
		require.NoError(t, err)
		before, err := p.target.backend.GetPathMeta(prefix)
		require.NoError(t, err)
		for _, record := range records {
			p.receiver.OnNotify(p.notification(t, record))
		}
		after, err := p.target.backend.GetPathMeta(prefix)
		require.NoError(t, err)
		require.Equal(t, before.Generation, after.Generation)
	})

	t.Run("MultiHopRelaysPlainSignedKV", func(t *testing.T) {
		p := newGenerationPeerPair(t)
		downstream := newActivePeerPair(t)
		k := key(t, "p2p-relay/value")
		var forwarded []*wire.MsgDKVSNotify
		p.receiver.Broadcast = func(msg *wire.MsgDKVSNotify) {
			copyMsg := &wire.MsgDKVSNotify{EventType: msg.EventType, Data: bytes.Clone(msg.Data)}
			forwarded = append(forwarded, copyMsg)
			downstream.receiver.OnNotify(copyMsg)
		}

		first := put(t, p, k, "v1")
		p.receiver.OnNotify(p.notification(t, first))
		require.Len(t, forwarded, 1)
		p.requireValue(t, first)
		downstream.requireValue(t, first)

		second := put(t, p, k, "v2")
		p.receiver.OnNotify(p.notification(t, second))
		require.Len(t, forwarded, 2)
		downstream.requireValue(t, second)

		deleted, err := p.source.client.DeleteCurrentRecord(owner.Wallet, k, 100)
		require.NoError(t, err)
		p.receiver.OnNotify(p.notification(t, deleted))
		require.Len(t, forwarded, 3)
		_, err = downstream.target.client.GetRecord(k)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)

		// A delayed higher-Seq update cannot recreate an absent key. It is
		// treated as evidence that this peer needs a current prefix view.
		downstream.receiver.OnNotify(forwarded[1])
		_, err = downstream.target.client.GetRecord(k)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		require.NotEmpty(t, downstream.requests)
	})

	t.Run("NotifyIsBufferedUntilInitialSyncCompletes", func(t *testing.T) {
		p := newGenerationPeerPair(t)
		p.receiver.Node.SetReady(false)
		k := key(t, "p2p-startup/value")
		current := put(t, p, k, "during-sync")
		p.receiver.OnNotify(p.notification(t, current))
		_, err := p.target.client.GetRecord(k)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)

		prefix, err := dkvs.CollectionPathForKey(k)
		require.NoError(t, err)
		p.receiver.QueuePathSync(prefix)
		p.deliver(t)
		require.True(t, p.receiver.Node.Ready())
		p.requireValue(t, current)
	})

	t.Run("ValidRelayCanContinueRealtimeUpdates", func(t *testing.T) {
		p := newGenerationPeerPair(t)
		relay := newActivePeerPair(t)
		relayID := relay.receiver.ValidatorID
		first := put(t, p, key(t, "p2p-alternate/value"), "v1")
		p.receiver.OnNotify(p.notification(t, first))
		p.requireValue(t, first)

		var forwarded []*wire.MsgDKVSNotify
		relay.receiver.ValidatorID = p.receiver.ValidatorID
		relay.receiver.Broadcast = func(msg *wire.MsgDKVSNotify) {
			forwarded = append(forwarded, &wire.MsgDKVSNotify{
				EventType: msg.EventType, Data: bytes.Clone(msg.Data),
			})
		}
		relay.receiver.OnNotify(p.notification(t, first))
		relay.requireValue(t, first)
		forwarded = nil // v1 established; observe only the subsequent relay update.

		second := put(t, p, first.Key, "v2-via-relay")
		relay.receiver.OnNotify(p.notification(t, second))
		require.Len(t, forwarded, 1)
		relay.requireValue(t, second)

		// The receiving node already synchronized and the relay is a valid
		// CoreNode source. There is no persistent "first source" ownership.
		p.receiver.ValidatorID = relayID
		p.receiver.OnNotify(forwarded[0])
		p.requireValue(t, second)
	})

	t.Run("DelayedSiblingAfterLaterKeyIsStillAccepted", func(t *testing.T) {
		p := newGenerationPeerPair(t)
		var siblings []*wire.DKVSRecord
		var mutations []dkvs.CASMutation
		for _, suffix := range []string{"a", "b"} {
			k := key(t, "p2p-delayed-sibling/"+suffix)
			record, err := wallet.NewDKVSSignedRecord(
				owner.Wallet, k, []byte(suffix),
				dkvs.RecordOptions{Seq: 1, IssueHeight: 100},
			)
			require.NoError(t, err)
			proof, err := dkvs.NewAutopayFeeProof(
				k, "personal", wire.MaxDKVSRecordSize, 0,
				releaseReviewAutopay().PoolContract, "",
			)
			require.NoError(t, err)
			require.NoError(t, wallet.AttachDKVSFeeProof(record, proof))
			require.NoError(t, wallet.SignDKVSRecord(owner.Wallet, record))
			siblings = append(siblings, record)
			mutations = append(mutations, sdkDKVSReviewAbsent(record))
		}
		result, err := p.source.client.WithWriteSigner(owner.Wallet).PutRecordBatchCAS(mutations)
		require.NoError(t, err)
		require.Equal(t, 2, result.Applied)

		p.receiver.OnNotify(p.notification(t, siblings[0]))
		later := put(t, p, key(t, "p2p-delayed-sibling/c"), "later")
		p.receiver.OnNotify(p.notification(t, later))
		p.receiver.OnNotify(p.notification(t, siblings[1]))

		p.requireValue(t, siblings[0])
		p.requireValue(t, siblings[1])
		p.requireValue(t, later)
		require.Empty(t, p.requests, "an absent Seq=1 key is valid realtime data, not a prefix-generation replay")
	})
}
