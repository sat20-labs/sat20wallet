package e2e

import (
	"bytes"
	"sync"
	"testing"
	"time"

	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	dkvsp2p "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs/p2p"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Real bound-wallet HTTP commits with deliberately delayed post-commit
// callbacks. P2P carries no generation; a delayed higher-Seq body for an
// absent key must trigger current-prefix synchronization instead of restoring
// the body directly.
func TestSDKDKVSLaunchReviewHTTP(t *testing.T) {
	t.Run("DelayedUpdateAfterDeleteCannotResurrectData", func(t *testing.T) {
		owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
		source, target := newBoundRPCFixture(t), newBoundRPCFixture(t)
		source.bind(t, owner, source.coreID)
		target.core.Store(false)

		p := newActivePeerPair(t)
		p.source.backend, p.source.client = source.store, source.client
		p.target.backend, p.target.client = target.store, target.client
		p.serving.Store = activePeerStore{releaseReviewPeerStore{source.store}}
		p.receiver.Store = activePeerStore{releaseReviewPeerStore{target.store}}

		key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "launch-http/reordered-commit")
		require.NoError(t, err)
		notifications := make(chan *wire.MsgDKVSNotify, 4)
		callbackErrors := make(chan error, 4)
		capture := func(event *dkvs.NotifyEvent) {
			notifications <- &wire.MsgDKVSNotify{EventType: event.EventType, Data: bytes.Clone(event.Data)}
		}
		source.store.SetNotify(capture)

		first, err := source.client.PutSignedRecordWithAutopay(
			owner.Wallet, key, []byte("v1"), dkvs.RecordOptions{IssueHeight: 100}, releaseReviewAutopay(),
		)
		require.NoError(t, err)
		initial := <-notifications
		p.receiver.OnNotify(initial)
		p.requireValue(t, first)

		entered, release := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock()

		source.store.SetNotify(func(event *dkvs.NotifyEvent) {
			record, err := dkvs.RecordFromNotifyEvent(event)
			if err != nil {
				callbackErrors <- err
				return
			}
			if record.Key != key {
				return
			}
			if !dkvs.IsTombstone(record.Flags) && record.Seq == 2 {
				close(entered)
				<-release
			}
			capture(event)
		})

		updated := make(chan error, 1)
		go func() {
			_, err := source.client.PutSignedRecordWithAutopay(
				owner.Wallet, key, []byte("v2"), dkvs.RecordOptions{IssueHeight: 100}, releaseReviewAutopay(),
			)
			updated <- err
		}()

		select {
		case <-entered:
		case err := <-updated:
			t.Fatalf("update finished before delayed callback: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("update never reached delayed callback")
		}

		committed, err := source.client.GetRecord(key)
		require.NoError(t, err)
		require.Equal(t, uint64(2), committed.Seq)

		_, err = source.client.DeleteCurrentRecord(owner.Wallet, key, 100)
		require.NoError(t, err)
		var deletion *wire.MsgDKVSNotify
		select {
		case deletion = <-notifications:
		case err := <-callbackErrors:
			t.Fatal(err)
		case <-time.After(5 * time.Second):
			t.Fatal("delete notification missing")
		}
		deletedRecord, err := dkvsp2p.RecordFromNotify(deletion)
		require.NoError(t, err)
		require.True(t, dkvs.IsTombstone(deletedRecord.Flags))
		p.receiver.OnNotify(deletion)

		path, err := dkvs.CollectionPathForKey(key)
		require.NoError(t, err)
		p.receiver.QueuePathSync(path)
		p.deliver(t)
		_, err = target.client.GetRecord(key)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)

		unblock()
		select {
		case err := <-updated:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("update did not finish after callback release")
		}

		var delayed *wire.MsgDKVSNotify
		select {
		case delayed = <-notifications:
		case err := <-callbackErrors:
			t.Fatal(err)
		case <-time.After(5 * time.Second):
			t.Fatal("delayed update missing")
		}
		delayedRecord, err := dkvsp2p.RecordFromNotify(delayed)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(committed), dkvs.RecordHash(delayedRecord))

		p.receiver.OnNotify(delayed)
		require.NotEmpty(t, p.requests, "absent key plus Seq>1 must request current prefix state")
		p.deliver(t)

		_, sourceErr := source.client.GetRecord(key)
		require.ErrorIs(t, sourceErr, dkvs.ErrRecordNotFound)
		_, targetErr := target.client.GetRecord(key)
		require.ErrorIs(t, targetErr, dkvs.ErrRecordNotFound)
		require.Zero(t, source.unguarded.Load())
	})
}
