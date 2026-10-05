package e2e

import (
	"sync/atomic"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Height/payment are fixture inputs. Expiry commits, real notification
// envelopes and signed P2P current-set installation use production code.
func TestSDKDKVSLaunchReviewMaintenance(t *testing.T) {
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	key := func(t *testing.T, suffix string) string {
		t.Helper()
		k, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "launch-maintenance/"+suffix)
		require.NoError(t, err)
		return k
	}
	paid := func(t *testing.T, p *generationPeerPair, k, value string) *wire.DKVSRecord {
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

	t.Run("ControlEndpointLocalExpiryDoesNotBreakNetworkSource", func(t *testing.T) {
		p := newGenerationPeerPair(t)
		first := paid(t, p, key(t, "local-control/paid"), "before")
		p.receiver.OnNotify(p.notification(t, first))
		p.requireValue(t, first)
		local, err := p.target.client.PutSignedRecordFreeLocal(owner.Wallet, key(t, "local-control/cache"), []byte("endpoint-only"), dkvs.RecordOptions{IssueHeight: 100, TTL: 2})
		require.NoError(t, err)
		p.source.height.Store(102)
		p.target.height.Store(102)
		require.NoError(t, p.source.backend.RefreshPaidRetentionAt(102))
		require.NoError(t, p.target.backend.RefreshPaidRetentionAt(102))
		pruned, err := p.target.backend.PruneExpiredAt(102)
		require.NoError(t, err)
		require.Equal(t, 1, pruned)
		_, err = p.target.client.GetRecord(local.Key)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		second := paid(t, p, first.Key, "after-local-expiry")
		p.receiver.OnNotify(p.notification(t, second))
		p.requireValue(t, second)
	})

	t.Run("ExpiredNetworkLeaseCanReconcileThenReceiveFreshKey", func(t *testing.T) {
		p := newGenerationPeerPair(t)
		oldKey := key(t, "network-expiry/old")
		proof, err := dkvs.NewOneshotFeeProof(oldKey, "personal", wire.MaxDKVSRecordSize, 102, "release-review-pool", "fixture-payer", "fixture-payment", "1")
		require.NoError(t, err)
		encoded, err := dkvs.EncodeFeeProof(proof)
		require.NoError(t, err)
		old, err := wallet.NewDKVSSignedRecord(owner.Wallet, oldKey, []byte("short-network-lease"), dkvs.RecordOptions{Seq: 1, IssueHeight: 100, TTL: 2, FeeProof: encoded})
		require.NoError(t, err)
		_, err = p.source.client.WithWriteSigner(owner.Wallet).PutRecordCAS(old, dkvs.WritePrecondition{ExpectAbsent: true})
		require.NoError(t, err)
		p.receiver.OnNotify(p.notification(t, old))
		p.requireValue(t, old)
		p.source.height.Store(102)
		p.target.height.Store(102)
		for _, s := range []*releaseReviewStore{p.source, p.target} {
			_, err := s.backend.PruneExpiredAt(102)
			require.NoError(t, err)
			_, err = s.client.GetRecord(oldKey)
			require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		}
		path := pathOf(t, oldKey)
		p.receiver.QueuePathSync(path)
		p.deliver(t)
		fresh := paid(t, p, key(t, "network-expiry/new"), "new-key-after-expiry")
		p.receiver.OnNotify(p.notification(t, fresh))
		p.deliver(t)
		p.receiver.QueuePathSync(path)
		p.deliver(t)
		p.requireValue(t, fresh)
	})

	t.Run("AutopayGracePruneMustNotPermanentlyDivergeReplica", func(t *testing.T) {
		p := newGenerationPeerPair(t)
		var lastPaid atomic.Uint64
		lastPaid.Store(100)
		for _, s := range []*releaseReviewStore{p.source, p.target} {
			s.backend.SetFeeVerifier(launchReviewPaymentClock{JSONFeeVerifier: dkvs.JSONFeeVerifier{AllowFreeLocal: true}, height: &s.height, lastPaid: &lastPaid})
		}
		old := paid(t, p, key(t, "autopay-expiry/old"), "before-funding-stops")
		p.receiver.OnNotify(p.notification(t, old))
		p.requireValue(t, old)
		grace := p.target.backend.FreeLocalCachePolicy().MaxTTL
		height := uint64(101)+grace
		p.source.height.Store(height)
		p.target.height.Store(height)
		for _, s := range []*releaseReviewStore{p.source, p.target} {
			require.NoError(t, s.backend.RefreshPaidRetentionAt(height))
			pruned, err := s.backend.PruneExpiredAutopayAt(height)
			require.NoError(t, err)
			require.Equal(t, 1, pruned)
			_, err = s.client.GetRecord(old.Key)
			require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		}
		path := pathOf(t, old.Key)
		p.receiver.QueuePathSync(path)
		p.deliver(t)
		lastPaid.Store(height)
		for _, s := range []*releaseReviewStore{p.source, p.target} { require.NoError(t, s.backend.RefreshPaidRetentionAt(height)) }
		fresh := paid(t, p, key(t, "autopay-expiry/new"), "funding-resumed")
		p.receiver.OnNotify(p.notification(t, fresh))
		p.deliver(t)
		p.receiver.QueuePathSync(path)
		p.deliver(t)
		p.requireValue(t, fresh)
	})
}

type launchReviewPaymentClock struct {
	dkvs.JSONFeeVerifier
	height *atomic.Uint64
	lastPaid *atomic.Uint64
}
func (v launchReviewPaymentClock) PaidRecordRetention(*wire.DKVSRecord, dkvs.ParsedKey) (dkvs.PaidRecordRetention, error) {
	return dkvs.PaidRecordRetention{CurrentBlock: v.height.Load(), LastPayHeight: v.lastPaid.Load()}, nil
}
