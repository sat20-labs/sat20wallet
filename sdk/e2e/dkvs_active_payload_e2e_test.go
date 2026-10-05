package e2e

import (
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
	"testing"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	sdkdkvs "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// SDK/API boundary fault injection complements real-node positive cases.
// Claimed deletion history must not become a hidden sequence floor, submit a
// write, or erase a previously confirmed local value.
func TestSDKDKVSCurrentPayloadRejectsDeletionHistory(t *testing.T) {
	source := newReleaseReviewStore(t)
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "active-payload/value")
	require.NoError(t, err)
	record, err := source.client.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("confirmed"), dkvs.RecordOptions{IssueHeight: 100, TTL: 10})
	require.NoError(t, err)
	etag := dkvs.RecordHash(record).String()
	for _, scenario := range []struct {
		name  string
		state dkvs.DKVSKeyState
	}{
		{"DeletedHistory", dkvs.DKVSKeyState{Key: key, Status: "deleted", Seq: 2, ETag: etag}},
		{"AbsentWithFloor", dkvs.DKVSKeyState{Key: key, Status: dkvs.KeyStateNeverSeen, Seq: 1, ETag: etag}},
		{"ActiveWithMismatchedEmbeddedRecord", dkvs.DKVSKeyState{Key: key, Status: dkvs.KeyStateActive, Seq: 2, ETag: etag, Record: record}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			transport := &activePayloadFaultTransport{releaseReviewStore: source, state: scenario.state}
			client := wallet.NewSatsNetDKVSClient("http", "current-payload.invalid", "testnet", transport)
			_, err := client.GetKeyState(key)
			require.ErrorIs(t, err, dkvs.ErrInvalidRecord)
			_, err = client.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("must-not-submit"), dkvs.RecordOptions{IssueHeight: 100, TTL: 10})
			require.Error(t, err)
			require.Zero(t, transport.writes)
			actual, err := source.client.GetRecord(key)
			require.NoError(t, err)
			require.Equal(t, dkvs.RecordHash(record), dkvs.RecordHash(actual))
		})
	}

	t.Run("DirectoryPayloadCannotInstallDeletedKeyState", func(t *testing.T) {
		prefix, err := dkvs.CollectionPathForKey(key)
		require.NoError(t, err)
		db := indexerdb.NewKVDB(t.TempDir())
		require.NotNil(t, db)
		t.Cleanup(func() { db.Close() })
		store := sdkdkvs.NewReplicaStore(db)
		const namespace = "current-payload"
		initial := sdkDKVSReviewInstallActive(t, source.client, store, namespace, prefix)
		baseline, err := store.ActiveBaseline(namespace, initial.Scope)
		require.NoError(t, err)
		malformed := initial
		malformed.Generation++
		command, err := wallet.NewDKVSDeleteCommand(owner.Wallet, record, initial.ViewHeight)
		require.NoError(t, err)
		_, err = store.InstallActiveState(namespace, baseline, malformed, []*wire.DKVSRecord{command}, false)
		require.ErrorIs(t, err, dkvs.ErrInvalidSnapshot)
		_, err = store.InstallActiveState(namespace, baseline, malformed, []*wire.DKVSRecord{command}, true)
		require.ErrorIs(t, err, dkvs.ErrInvalidSnapshot)
		// A claimed empty current root with no delete command cannot clear a
		// confirmed value through incremental installation.
		malformed.Root = chainhash.Hash{}
		_, err = store.InstallActiveState(namespace, baseline, malformed, nil, false)
		require.ErrorIs(t, err, dkvs.ErrPathDiverged)
		actual, err := store.LoadSubscriptionRecord(namespace, key)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(record), dkvs.RecordHash(actual))
		state, err := store.LoadSubscriptionState(namespace)
		require.NoError(t, err)
		require.Equal(t, initial.Generation, state.Generations[prefix])
	})

	t.Run("MatchingActiveAndCleanAbsenceRemainValid", func(t *testing.T) {
		actual, err := source.client.GetKeyState(key)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateActive, actual.Status)
		_, err = source.client.DeleteCurrentRecord(owner.Wallet, key, 100)
		require.NoError(t, err)
		actual, err = source.client.GetKeyState(key)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateNeverSeen, actual.Status)
		require.Zero(t, actual.Seq)
		require.Empty(t, actual.ETag)
	})
}

type activePayloadFaultTransport struct {
	*releaseReviewStore
	state  dkvs.DKVSKeyState
	writes int
}

func (p *activePayloadFaultTransport) SendDKVSGet(path string, query map[string]string) ([]byte, error) {
	if path == "/v3/dkvs/key-state" {
		return releaseReviewResponse(&p.state, nil)
	}
	return p.releaseReviewStore.SendDKVSGet(path, query)
}
func (p *activePayloadFaultTransport) SendDKVSPost(path string, body []byte) ([]byte, error) {
	if path == "/v3/dkvs/records/batch-cas" {
		p.writes++
	}
	return p.releaseReviewStore.SendDKVSPost(path, body)
}
