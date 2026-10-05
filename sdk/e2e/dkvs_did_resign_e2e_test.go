package e2e

import (
	"context"
	"testing"

	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func (f *boundRPCFixture) GetDKVSActivePage(ctx context.Context, request dkvs.ActiveSyncRequest) (*dkvs.ActivePage, error) {
	return f.store.ActiveSyncPage(ctx, request)
}
func (f *boundRPCFixture) WatchDKVSActive(ctx context.Context, request dkvs.ActiveWatchRequest) (*dkvs.ActiveWatchResult, error) {
	return f.store.WatchActive(ctx, request)
}

// SDK -> production HTTP routing, bound-wallet admission, real DID permission
// validation, and atomic Pebble CAS. Only DID ownership and payment are fixtures.
func TestSDKDKVSDIDResign(t *testing.T) {
	const did = "takeover.btc"
	oldOwner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	newOwner := newDKVSKeyPathActor(t, keyFromMnemonic(t, bootstrapMnemonic, 2))
	setOwner := func(f *boundRPCFixture, actor *dkvsKeyPathActor) {
		identity := dkvs.DIDIdentity{CanonicalName: did, NameID: dkvs.NormalizeNameID(did), Active: true,
			SigningKeys: [][]byte{actor.Wallet.GetPubKey().SerializeCompressed()}}
		f.store.SetResolver(dkvs.StaticDIDResolver{Names: map[string]dkvs.DIDIdentity{did: identity}, Services: map[string]dkvs.DIDIdentity{did: identity}})
	}
	setup := func(t *testing.T) (*boundRPCFixture, []*wire.DKVSRecord) {
		t.Helper()
		f := newBoundRPCFixture(t)
		f.bind(t, oldOwner, f.coreID)
		f.bind(t, newOwner, f.coreID)
		setOwner(f, oldOwner)
		name, err := dkvs.NameKey(did)
		require.NoError(t, err)
		x, err := dkvs.ServiceKey(did, "config/x")
		require.NoError(t, err)
		y, err := dkvs.ServiceKey(did, "config/y")
		require.NoError(t, err)
		var records []*wire.DKVSRecord
		for _, key := range []string{name, x, y} {
			record, err := f.client.PutSignedRecordWithAutopay(oldOwner.Wallet, key, []byte("original:"+key), dkvs.RecordOptions{IssueHeight: 100}, releaseReviewAutopay())
			require.NoError(t, err)
			records = append(records, record)
		}
		setOwner(f, newOwner)
		return f, records
	}

	t.Run("NameAndAllServiceValuesAreResignedTogether", func(t *testing.T) {
		f, original := setup(t)
		updated, err := f.client.ResignDIDRecords(newOwner.Wallet, did)
		require.NoError(t, err)
		require.Len(t, updated, len(original))
		for _, old := range original {
			record, err := f.client.GetRecord(old.Key)
			require.NoError(t, err)
			require.Equal(t, old.Value, record.Value)
			require.Equal(t, old.Seq+1, record.Seq)
			require.Equal(t, newOwner.Wallet.GetPubKey().SerializeCompressed(), record.PubKey)
			require.NoError(t, dkvs.VerifySignature(record))
		}
		snapshot, err := f.store.GetPathSnapshot("/svc/" + did)
		require.NoError(t, err)
		target := newReleaseReviewStore(t)
		identity := dkvs.DIDIdentity{CanonicalName: did, NameID: dkvs.NormalizeNameID(did), Active: true, SigningKeys: [][]byte{newOwner.Wallet.GetPubKey().SerializeCompressed()}}
		target.backend.SetResolver(dkvs.StaticDIDResolver{Services: map[string]dkvs.DIDIdentity{did: identity}})
		_, err = target.backend.ApplyPathSnapshot(snapshot)
		require.NoError(t, err, "complete re-signing must leave no historical sibling that blocks replication")
	})

	t.Run("PreviousOwnerCannotResignAfterTransfer", func(t *testing.T) {
		f, original := setup(t)
		_, err := f.client.ResignDIDRecords(oldOwner.Wallet, did)
		require.ErrorIs(t, err, dkvs.ErrPermissionDenied)
		for _, old := range original {
			record, err := f.client.GetRecord(old.Key)
			require.NoError(t, err)
			require.Equal(t, dkvs.RecordHash(old), dkvs.RecordHash(record))
		}
	})

	t.Run("ConcurrentSiblingUpdatePreventsPartialTakeover", func(t *testing.T) {
		f, original := setup(t)
		var concurrent *wire.DKVSRecord
		f.afterBindingRead = func() {
			var err error
			concurrent, err = f.client.PutSignedRecordWithAutopay(newOwner.Wallet, original[2].Key, []byte("other-device-change"), dkvs.RecordOptions{IssueHeight: 100}, releaseReviewAutopay())
			require.NoError(t, err)
		}
		_, err := f.client.ResignDIDRecords(newOwner.Wallet, did)
		// The signed endpoint generation is checked before the individual CAS
		// conditions, so a concurrent collection change fails at that fence.
		require.ErrorIs(t, err, dkvs.ErrStaleGeneration)
		for _, old := range original[:2] {
			record, err := f.client.GetRecord(old.Key)
			require.NoError(t, err)
			require.Equal(t, dkvs.RecordHash(old), dkvs.RecordHash(record), "no earlier key may be partially re-signed")
		}
		record, err := f.client.GetRecord(original[2].Key)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(concurrent), dkvs.RecordHash(record))
	})
}
