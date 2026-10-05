package e2e

import (
	"context"
	"fmt"
	"testing"

	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestSDKDKVSDIDResignBoundaries(t *testing.T) {
	oldOwner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	newOwner := newDKVSKeyPathActor(t, keyFromMnemonic(t, bootstrapMnemonic, 2))
	const did = "limits.btc"
	setOwner := func(f *boundRPCFixture, actor *dkvsKeyPathActor) {
		identity := dkvs.DIDIdentity{CanonicalName: did, NameID: dkvs.NormalizeNameID(did), Active: true,
			SigningKeys: [][]byte{actor.Wallet.GetPubKey().SerializeCompressed()}}
		f.store.SetResolver(dkvs.StaticDIDResolver{Names: map[string]dkvs.DIDIdentity{did: identity}, Services: map[string]dkvs.DIDIdentity{did: identity}})
	}
	setup := func(t *testing.T) *boundRPCFixture {
		t.Helper()
		f := newBoundRPCFixture(t)
		f.bind(t, oldOwner, f.coreID)
		f.bind(t, newOwner, f.coreID)
		setOwner(f, oldOwner)
		return f
	}

	t.Run("FreeLocalTakeoverDoesNotExtendLease", func(t *testing.T) {
		f := setup(t)
		key, err := dkvs.ServiceKey(did, "config/temporary")
		require.NoError(t, err)
		old, err := f.client.PutSignedRecordFreeLocal(oldOwner.Wallet, key, []byte("temporary service"), dkvs.RecordOptions{IssueHeight: 100, TTL: 50})
		require.NoError(t, err)
		f.height.Store(110)
		setOwner(f, newOwner)
		updated, err := f.client.ResignDIDRecords(newOwner.Wallet, did)
		require.NoError(t, err)
		require.Len(t, updated, 1)
		require.Equal(t, old.Value, updated[0].Value)
		require.Equal(t, old.Seq+1, updated[0].Seq)
		require.Equal(t, uint64(110), updated[0].IssueHeight)
		require.Equal(t, dkvs.RecordExpiryHeight(old), dkvs.RecordExpiryHeight(updated[0]))
		proof, err := dkvs.ParseFeeProof(updated[0].FeeProof)
		require.NoError(t, err)
		require.Equal(t, dkvs.FeeModeFreeLocal, proof.Mode)
	})

	t.Run("OversizedTakeoverFailsBeforeAnyWrite", func(t *testing.T) {
		f := setup(t)
		count := dkvs.MaxBatchCASMutations + 1
		require.LessOrEqual(t, count, 1024, "keep the regression resource-bounded if batch limits change")
		old := make([]*wire.DKVSRecord, 0, count)
		for n := 0; n < count; n++ {
			key, err := dkvs.ServiceKey(did, fmt.Sprintf("config/k%04d", n))
			require.NoError(t, err)
			record, err := f.client.PutSignedRecordWithAutopay(oldOwner.Wallet, key, []byte("unchanged"), dkvs.RecordOptions{IssueHeight: 100}, releaseReviewAutopay())
			require.NoError(t, err)
			old = append(old, record)
		}
		scope := dkvs.ActiveScope{Prefix: "/svc/" + did}
		before, err := f.store.ActiveMetadata(context.Background(), scope)
		require.NoError(t, err)
		setOwner(f, newOwner)
		_, err = f.client.ResignDIDRecords(newOwner.Wallet, did)
		require.ErrorIs(t, err, dkvs.ErrBatchTooLarge)
		after, err := f.store.ActiveMetadata(context.Background(), scope)
		require.NoError(t, err)
		require.Equal(t, before, after, "an oversized request must not publish a partial takeover")
		for _, record := range old {
			actual, err := f.client.GetRecord(record.Key)
			require.NoError(t, err)
			require.Equal(t, dkvs.RecordHash(record), dkvs.RecordHash(actual))
		}
	})
}
