package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	sdkdkvs "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Deterministic cross-layer regressions: public SDK, real SatoshiNet Indexer
// and Pebble. The transport only dispatches; height, payment and DID ownership
// are controlled fixtures. RPC binding is covered by separate real HTTP tests.
func TestSDKDKVSReleaseReviewState(t *testing.T) {
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	ctx := context.Background()
	key := func(t *testing.T, suffix string) string {
		t.Helper()
		k, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), suffix)
		require.NoError(t, err)
		return k
	}
	prefix := func(t *testing.T, k string) string {
		t.Helper()
		p, err := dkvs.CollectionPathForKey(k)
		require.NoError(t, err)
		return p
	}
	free := func(t *testing.T, s *releaseReviewStore, k, value string) *wire.DKVSRecord {
		t.Helper()
		r, err := s.client.PutSignedRecordFreeLocal(owner.Wallet, k, []byte(value), dkvs.RecordOptions{IssueHeight: s.height.Load(), TTL: 10})
		require.NoError(t, err)
		return r
	}
	paid := func(t *testing.T, s *releaseReviewStore, k, value string) *wire.DKVSRecord {
		t.Helper()
		r, err := s.client.PutSignedRecordWithAutopay(owner.Wallet, k, []byte(value), dkvs.RecordOptions{IssueHeight: s.height.Load()}, releaseReviewAutopay())
		require.NoError(t, err)
		return r
	}
	newReplica := func(t *testing.T) *sdkdkvs.ReplicaStore {
		t.Helper()
		db := indexerdb.NewKVDB(t.TempDir())
		require.NotNil(t, db)
		t.Cleanup(func() { db.Close() })
		return sdkdkvs.NewReplicaStore(db)
	}

	t.Run("BasicRenewalAndStaleCASProtection", func(t *testing.T) {
		s := newReleaseReviewStore(t)
		original := free(t, s, key(t, "release-baseline/a"), "v1")
		s.height.Store(105)
		renewed, err := s.client.RenewRecord(owner.Wallet, original, dkvs.RecordOptions{IssueHeight: 105, TTL: 20})
		require.NoError(t, err)
		require.Equal(t, original.Seq+1, renewed.Seq)
		require.NoError(t, dkvs.VerifySignature(renewed))
		_, err = s.client.RenewRecord(owner.Wallet, original, dkvs.RecordOptions{IssueHeight: 105, TTL: 30})
		require.ErrorIs(t, err, dkvs.ErrWriteConflict)
		s.height.Store(111)
		actual, err := s.client.GetRecord(original.Key)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(renewed), dkvs.RecordHash(actual))
	})

	t.Run("OfflineReplicaAcceptsRecreationAfterNaturalExpiry", func(t *testing.T) {
		s := newReleaseReviewStore(t)
		k := key(t, "release-expiry-recreate/a")
		free(t, s, k, "first")
		old := free(t, s, k, "second")
		require.Equal(t, uint64(2), old.Seq)
		replica := newReplica(t)
		scope := dkvs.ActiveScope{Prefix: prefix(t, k)}
		_, err := s.client.SyncActiveScope(ctx, replica, "release-review", scope, true)
		require.NoError(t, err)
		s.height.Store(dkvs.RecordExpiryHeight(old))
		pruned, err := s.backend.PruneExpiredAt(s.height.Load())
		require.NoError(t, err)
		require.Equal(t, 1, pruned)
		absent, err := s.client.GetKeyState(k)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateNeverSeen, absent.Status)
		recreated := free(t, s, k, "new-incarnation")
		require.Equal(t, uint64(1), recreated.Seq)
		require.Greater(t, recreated.IssueHeight, old.IssueHeight)
		_, err = s.client.SyncActiveScope(ctx, replica, "release-review", scope, false)
		require.NoError(t, err, "incremental current state must admit a new FREE_LOCAL lifecycle")
		_, err = s.client.SyncActiveScope(ctx, replica, "release-review", scope, true)
		require.NoError(t, err, "full refresh must recover without clearing the wallet database")
		actual, err := replica.LoadSubscriptionRecord("release-review", k)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(recreated), dkvs.RecordHash(actual))
	})

	t.Run("DelayedPathRepairCannotUndoAcknowledgedWrite", func(t *testing.T) {
		source, target := newReleaseReviewStore(t), newReleaseReviewStore(t)
		k := key(t, "release-stale-repair/a")
		paid(t, source, k, "v1")
		p := prefix(t, k)
		oldSnapshot, err := source.backend.GetPathSnapshot(p)
		require.NoError(t, err)
		baseline, err := target.backend.NetworkSyncBaseline(p)
		require.NoError(t, err)
		_, err = target.backend.ApplyPathSnapshotFrom(oldSnapshot, baseline)
		require.NoError(t, err)
		// Capture receiver state when the next download starts. Writes that
		// commit during that download must invalidate its installation.
		baseline, err = target.backend.NetworkSyncBaseline(p)
		require.NoError(t, err)
		latest := paid(t, target, k, "acknowledged-v2")
		extra := paid(t, target, key(t, "release-stale-repair/b"), "acknowledged-new-key")
		_, err = target.backend.ApplyPathSnapshotFrom(oldSnapshot, baseline)
		require.ErrorIs(t, err, dkvs.ErrConcurrentUpdate)
		actual, err := target.client.GetRecord(k)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(latest), dkvs.RecordHash(actual))
		actual, err = target.client.GetRecord(extra.Key)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(extra), dkvs.RecordHash(actual))
	})

	t.Run("ReplicatedPaidStateAfterLocalDeleteHasUsableCASBasis", func(t *testing.T) {
		source, target := newReleaseReviewStore(t), newReleaseReviewStore(t)
		k := key(t, "release-local-delete/a")
		free(t, target, k, "local-copy")
		deleted, err := target.client.DeleteCurrentRecord(owner.Wallet, k, 100)
		require.NoError(t, err)
		require.Equal(t, uint64(2), deleted.Seq)
		absent, err := target.client.GetKeyState(k)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateNeverSeen, absent.Status)
		durable := paid(t, source, k, "durable-v1")
		p := prefix(t, k)
		snapshot, err := source.backend.GetPathSnapshot(p)
		require.NoError(t, err)
		baseline, err := target.backend.NetworkSyncBaseline(p)
		require.NoError(t, err)
		_, err = target.backend.ApplyPathSnapshotFrom(snapshot, baseline)
		require.NoError(t, err)
		visible, err := target.client.GetKeyState(k)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateActive, visible.Status)
		require.Equal(t, durable.Seq, visible.Seq)
		updated := paid(t, target, k, "durable-v2")
		require.Equal(t, visible.Seq+1, updated.Seq, "read state must be the actual CAS basis; no hidden floor")
	})

	t.Run("CanonicalTakeoverAndDeleteCannotExposeSupersededLocalValue", func(t *testing.T) {
		source, target := newReleaseReviewStore(t), newReleaseReviewStore(t)
		k := key(t, "release-canonical-delete/a")
		free(t, target, k, "old-local-value")
		durable := paid(t, source, k, "canonical-value")
		updated, err := target.backend.AcceptCurrentRecord(durable)
		require.NoError(t, err)
		require.True(t, updated)
		visible, err := target.client.GetRecord(k)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(durable), dkvs.RecordHash(visible))
		// Omission removes the accepted canonical state, not an unrelated local
		// key which has never participated in this source's network view.
		p := prefix(t, k)
		baseline, err := target.backend.NetworkSyncBaseline(p)
		require.NoError(t, err)
		_, err = source.client.DeleteCurrentRecord(owner.Wallet, k, 100)
		require.NoError(t, err)
		snapshot, err := source.backend.GetPathSnapshot(p)
		require.NoError(t, err)
		require.Empty(t, snapshot.Records)
		_, err = target.backend.ApplyPathSnapshotFrom(snapshot, baseline)
		require.NoError(t, err)
		actual, err := target.client.GetRecord(k)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		require.Nil(t, actual)
		state, err := target.client.GetKeyState(k)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateNeverSeen, state.Status)
		require.Zero(t, state.Seq)
	})

	t.Run("DeleteChurnCannotStrandPrefixSynchronization", func(t *testing.T) {
		s := newReleaseReviewStore(t)
		p := prefix(t, key(t, "release-delete-churn/a"))
		replica := newReplica(t)
		scope := dkvs.ActiveScope{Prefix: p}
		_, err := s.client.SyncActiveScope(ctx, replica, "release-churn", scope, true)
		require.NoError(t, err)
		count := dkvs.MaxPrefixReadRecords + 1
		require.LessOrEqual(t, count, 20000)
		proof, err := dkvs.EncodeFeeProof(&dkvs.FeeProof{Mode: dkvs.FeeModeFreeLocal})
		require.NoError(t, err)
		for base := 0; base < count; {
			n := 16
			if count-base < n {
				n = count - base
			}
			creates := make([]dkvs.CASMutation, 0, n)
			deletes := make([]dkvs.CASMutation, 0, n)
			for j := 0; j < n; j++ {
				k := fmt.Sprintf("%s/k%05d", p, base+j)
				r, err := wallet.NewDKVSSignedRecord(owner.Wallet, k, []byte("x"), dkvs.RecordOptions{Seq: 1, IssueHeight: 100, TTL: 10, FeeProof: proof})
				require.NoError(t, err)
				d, err := wallet.NewDKVSDeleteCommand(owner.Wallet, r, 100)
				require.NoError(t, err)
				creates = append(creates, sdkDKVSReviewAbsent(r))
				deletes = append(deletes, sdkDKVSReviewExpected(d, r))
			}
			_, err := s.client.WithWriteSigner(owner.Wallet).PutRecordBatchCAS(creates)
			require.NoError(t, err)
			_, err = s.client.WithWriteSigner(owner.Wallet).PutRecordBatchCAS(deletes)
			require.NoError(t, err)
			base += n
		}
		live := free(t, s, p+"/live", "still-active")
		usage, err := s.client.GetUsage(p)
		require.NoError(t, err)
		require.Equal(t, uint64(1), usage.ActiveRecords)
		snapshot, err := sdkDKVSReviewActivePage(s.client, p)
		require.NoError(t, err)
		require.Len(t, snapshot.Records, 1)
		_, err = s.client.SyncActiveScope(ctx, replica, "release-churn", scope, false)
		require.NoError(t, err)
		_, err = s.client.SyncActiveScope(ctx, replica, "release-churn", scope, true)
		require.NoError(t, err)
		got, err := replica.LoadSubscriptionRecord("release-churn", live.Key)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(live), dkvs.RecordHash(got))
		t.Logf("release-review: %d create/delete operations leave one synchronizable active key", count)
	})

	t.Run("DIDTransferRequiresCompleteReceivingWalletResign", func(t *testing.T) {
		source, target := newReleaseReviewStore(t), newReleaseReviewStore(t)
		successor := newDKVSKeyPathActor(t, keyFromMnemonic(t, bootstrapMnemonic, 2))
		const service = "release-multi.btc"
		setOwner := func(actor *dkvsKeyPathActor) {
			for _, s := range []*releaseReviewStore{source, target} {
				s.backend.SetResolver(dkvs.StaticDIDResolver{Services: map[string]dkvs.DIDIdentity{service: {
					CanonicalName: service, NameID: dkvs.NormalizeNameID(service),
					SigningKeys: [][]byte{actor.Wallet.GetPubKey().SerializeCompressed()}, Active: true,
				}}})
			}
		}
		setOwner(owner)
		x, err := dkvs.ServiceKey(service, "config/x")
		require.NoError(t, err)
		y, err := dkvs.ServiceKey(service, "config/y")
		require.NoError(t, err)
		paid(t, source, x, "x1")
		paid(t, source, y, "historical-y1")
		p := prefix(t, x)
		initial, err := source.backend.GetPathSnapshot(p)
		require.NoError(t, err)
		baseline, err := target.backend.NetworkSyncBaseline(p)
		require.NoError(t, err)
		_, err = target.backend.ApplyPathSnapshotFrom(initial, baseline)
		require.NoError(t, err)
		setOwner(successor)
		_, err = source.client.PutSignedRecordWithAutopay(successor.Wallet, x, []byte("x2-new-owner"), dkvs.RecordOptions{IssueHeight: 100}, releaseReviewAutopay())
		require.NoError(t, err)
		mixed, err := source.backend.GetPathSnapshot(p)
		require.NoError(t, err)
		baseline, err = target.backend.NetworkSyncBaseline(p)
		require.NoError(t, err)
		_, err = target.backend.ApplyPathSnapshotFrom(mixed, baseline)
		require.ErrorIs(t, err, dkvs.ErrPermissionDenied, "historical signer must not regain write permission through a snapshot")
		resigned, err := source.client.ResignDIDRecords(successor.Wallet, service)
		require.NoError(t, err)
		require.Len(t, resigned, 2)
		for _, r := range resigned {
			require.Equal(t, successor.Wallet.GetPubKey().SerializeCompressed(), r.PubKey)
			require.NoError(t, dkvs.VerifySignature(r))
		}
		allNew, err := source.backend.GetPathSnapshot(p)
		require.NoError(t, err)
		_, err = target.backend.ApplyPathSnapshotFrom(allNew, baseline)
		require.NoError(t, err)
		for _, want := range resigned {
			got, err := target.client.GetRecord(want.Key)
			require.NoError(t, err)
			require.Equal(t, dkvs.RecordHash(want), dkvs.RecordHash(got))
		}
	})
}

type releaseReviewStore struct {
	backend *dkvs.Indexer
	client  *wallet.SatsNetDKVSClient
	height  atomic.Uint64
}

type releaseReviewFeeVerifier struct {
	dkvs.JSONFeeVerifier
	height *atomic.Uint64
}

func (v releaseReviewFeeVerifier) PaidRecordRetention(*wire.DKVSRecord, dkvs.ParsedKey) (dkvs.PaidRecordRetention, error) {
	h := v.height.Load()
	return dkvs.PaidRecordRetention{CurrentBlock: h, LastPayHeight: h}, nil
}
func releaseReviewAutopay() wallet.DKVSAutopayOptions {
	return wallet.DKVSAutopayOptions{PoolContract: "release-review-pool", AddressParams: &chaincfg.TestNetParams}
}
func newReleaseReviewStore(t *testing.T) *releaseReviewStore {
	t.Helper()
	db := indexerdb.NewKVDB(t.TempDir())
	require.NotNil(t, db)
	t.Cleanup(func() { db.Close() })
	s := &releaseReviewStore{}
	s.height.Store(100)
	s.backend = dkvs.New(db, dkvs.Config{
		EndpointID: "release-review-endpoint", AllowFreeLocal: true,
		FreeLocalCache: dkvs.DefaultFreeLocalCachePolicy(), CurrentHeight: s.height.Load,
		FeeVerifier: releaseReviewFeeVerifier{JSONFeeVerifier: dkvs.JSONFeeVerifier{AllowFreeLocal: true}, height: &s.height},
	})
	s.client = wallet.NewSatsNetDKVSClient("http", "release-review.invalid", "testnet", s)
	return s
}
func releaseReviewResponse(data any, err error) ([]byte, error) {
	response := map[string]any{"code": 0, "msg": "ok", "data": data}
	if err != nil {
		response["code"], response["msg"], response["error_code"] = -1, err.Error(), string(dkvs.ErrorCodeOf(err))
	} else if record, ok := data.(*wire.DKVSRecord); ok && record != nil {
		response["etag"] = dkvs.RecordHash(record).String()
	}
	return json.Marshal(response)
}
func (s *releaseReviewStore) DKVSClientConfig() (*dkvs.ClientConfig, error) {
	cfg := s.backend.ClientConfig()
	return &cfg, nil
}
func (s *releaseReviewStore) SendGetRequest(u *wallet.URL) ([]byte, error) {
	if strings.HasSuffix(u.Path, "/btc/block/bestblockheight") {
		return releaseReviewResponse(int64(s.height.Load()), nil)
	}
	return nil, fmt.Errorf("unexpected generic GET %s", u.Path)
}
func (s *releaseReviewStore) SendPostRequest(u *wallet.URL, _ []byte) ([]byte, error) {
	return nil, fmt.Errorf("unexpected generic POST %s", u.Path)
}
func (s *releaseReviewStore) SendDKVSGet(path string, query map[string]string) ([]byte, error) {
	switch path {
	case "/v3/dkvs/config":
		return releaseReviewResponse(s.backend.ClientConfig(), nil)
	case "/v3/dkvs/record":
		r, err := s.backend.Get(query["key"])
		return releaseReviewResponse(r, err)
	case "/v3/dkvs/key-state":
		r, err := s.backend.GetKeyState(query["key"])
		return releaseReviewResponse(r, err)
	default:
		return nil, fmt.Errorf("unexpected DKVS GET %s", path)
	}
}
func (s *releaseReviewStore) SendDKVSPost(path string, body []byte) ([]byte, error) {
	if path == "/v3/dkvs/active/sync" {
		var req dkvs.ActiveSyncRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		result, err := s.backend.ActiveSyncPage(context.Background(), req)
		return releaseReviewResponse(result, err)
	}
	if path == "/v3/dkvs/records/batch-cas" {
		var req wallet.DKVSBatchCASRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		mutations := make([]dkvs.CASMutation, 0, len(req.Mutations))
		for _, m := range req.Mutations {
			p := dkvs.WritePrecondition{ExpectAbsent: m.ExpectAbsent}
			if m.ExpectedETag != "" {
				hash, err := chainhash.NewHashFromStr(m.ExpectedETag)
				if err != nil {
					return nil, err
				}
				p.ExpectedHash = hash
			}
			mutations = append(mutations, dkvs.CASMutation{Record: m.Record, Precondition: p})
		}
		if err := s.backend.ValidateBatchEndpointID(mutations, req.EndpointID); err != nil {
			return releaseReviewResponse(nil, err)
		}
		result, err := s.backend.PutLocalBatchCASResultWithOptions(mutations, dkvs.BatchCASOptions{EndpointID: req.EndpointID, RequestID: req.RequestID})
		return releaseReviewResponse(result, err)
	}
	var req struct {
		Prefix          string                  `json:"prefix"`
		EndpointID      string                  `json:"endpoint_id"`
		AfterGeneration uint64                  `json:"after_generation"`
		Prefixes        []dkvs.PrefixGeneration `json:"prefixes"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	switch path {
	case "/v3/dkvs/prefixes/read":
		r, err := s.backend.ReadPrefix(req.Prefix)
		return releaseReviewResponse(r, err)
	default:
		return nil, fmt.Errorf("unexpected DKVS POST %s", path)
	}
}
