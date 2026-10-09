package wallet

import (
	"github.com/btcsuite/btcd/chaincfg"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRGB11RegistryReviewE2EFullSnapshotAndReopen(t *testing.T) {
	source, makeRecord := newRGB11RegistryE2ESource(t)
	records := make([]*swire.DKVSRecord, 0, 12)
	for ordinal := uint64(1); ordinal <= 12; ordinal++ {
		record := makeRecord("alice", "USD", "f", ordinal, int(700+ordinal))
		_, err := source.indexer.PutInternalContract(record)
		require.NoError(t, err)
		records = append(records, record)
	}
	snapshot, err := source.indexer.Snapshot()
	require.NoError(t, err)
	target, _ := newRGB11RegistryE2ESource(t)
	db := newMemoryKVDB()
	core := NewInternalWalletWithMnemonic(rgb11RegistryTestMnemonic, "", &chaincfg.TestNet4Params)
	cfg := dkvsindexer.Config{CurrentHeight: func() uint64 { return 100 }, SystemVerifier: dkvsindexer.StaticSystemVerifier{Keys: [][]byte{core.GetPubKey().SerializeCompressed()}}}
	target.indexer = dkvsindexer.New(db, cfg)
	_, err = target.indexer.ApplySnapshot(snapshot)
	require.NoError(t, err)
	assertRead := func(t *testing.T) {
		client, reads := newRGB11RegistryE2EHTTPClient(t, target, nil)
		for _, i := range []int{0, 1, 9, 11} {
			got, err := client.GetRGB11Registration("alice", "USD", rgb11RegistryRecordID(records[i]))
			require.NoError(t, err)
			require.EqualValues(t, i+1, got.Ordinal)
		}
		require.EqualValues(t, 4, reads.Load())
	}
	t.Run("after_full_snapshot", assertRead)
	target.indexer = dkvsindexer.New(db, cfg)
	t.Run("after_indexer_reconstruction", assertRead)
}

func TestRGB11RegistryReviewE2ERejectMutableRegistryShape(t *testing.T) {
	source, makeRecord := newRGB11RegistryE2ESource(t)
	record := makeRecord("alice", "USD", "f", 1, 801)
	_, err := source.indexer.PutInternalContract(record)
	require.NoError(t, err)
	// Existing HTTP injection tests re-sign malformed TTL/Seq with the trusted
	// authority. Here verify recovery cannot omit an immutable contract.
	target, _ := newRGB11RegistryE2ESource(t)
	snapshot, err := source.indexer.GetPathSnapshot(rgb11wallet.RGB11RegistryPath)
	require.NoError(t, err)
	_, err = target.indexer.ApplyPathSnapshot(snapshot)
	require.NoError(t, err)
	empty, _ := newRGB11RegistryE2ESource(t)
	snapshot, err = empty.indexer.GetPathSnapshot(rgb11wallet.RGB11RegistryPath)
	require.NoError(t, err)
	baseline, err := target.indexer.NetworkSyncBaseline(rgb11wallet.RGB11RegistryPath)
	require.NoError(t, err)
	_, err = target.indexer.ApplyPathSnapshotFrom(snapshot, baseline)
	require.ErrorIs(t, err, dkvsindexer.ErrWriteConflict)
	got, err := target.indexer.Get(record.Key)
	require.NoError(t, err)
	require.Equal(t, dkvsindexer.RecordHash(record), dkvsindexer.RecordHash(got))
}
