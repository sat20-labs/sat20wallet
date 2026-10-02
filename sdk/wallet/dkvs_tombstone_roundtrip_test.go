package wallet

import (
	"errors"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// Real server index + SDK replica. Structural fee policy isolates deletion;
// TestSDKCoreModulesE2E separately verifies real AUTOPAY settlement.
func TestDKVSTombstoneRoundTrip(t *testing.T) {
	serverDB := indexerdb.NewKVDB(t.TempDir())
	t.Cleanup(func() { _ = serverDB.Close() })
	server := dkvsindexer.New(serverDB, dkvsindexer.Config{
		EndpointID: "tombstone-regression", AllowFreeLocal: true,
		FeeVerifier: dkvsindexer.JSONFeeVerifier{AllowFreeLocal: true},
		CurrentHeight: func() uint64 { return 100 },
	})
	key, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	signer := dkvsTestWalletFromPriv(t, key)
	prefix, err := dkvsindexer.PersonalKey(signer.GetPubKey().SerializeCompressed(), "delete-regression")
	if err != nil { t.Fatal(err) }
	removed, keep := prefix+"/removed", prefix+"/keep"
	put := func(path string, seq uint64) {
		t.Helper()
		record, err := NewDKVSSignedRecord(signer, path, []byte("live"), dkvsindexer.RecordOptions{Seq: seq, IssueHeight: 100, TTL: 100})
		if err != nil { t.Fatal(err) }
		if _, err = server.PutLocal(record); err != nil { t.Fatal(err) }
	}
	put(removed, 1)
	put(keep, 1)
	before, err := server.PrefixSnapshot(prefix)
	if err != nil { t.Fatal(err) }
	tombstone, err := NewDKVSSignedTombstone(signer, removed, dkvsindexer.RecordOptions{Seq: 2, IssueHeight: 100})
	if err != nil { t.Fatal(err) }
	if _, err = server.PutLocal(tombstone); err != nil { t.Fatal(err) }
	direct, err := server.GetKeyState(removed)
	if err != nil { t.Fatal(err) }
	if direct.Status != dkvsindexer.KeyStateDeleted || direct.Seq != 2 || direct.ETag != dkvsindexer.RecordHash(tombstone).String() || direct.Record != nil {
		t.Fatal("direct key state lost the deletion, sequence floor or tombstone hash")
	}
	newReplica := func(t *testing.T) *dkvsReplicaStore {
		t.Helper()
		db := indexerdb.NewKVDB(t.TempDir())
		t.Cleanup(func() { _ = db.Close() })
		replica := newDKVSReplicaStore(db)
		if _, err := replica.ReplacePrefixSnapshot("replica", before); err != nil { t.Fatal(err) }
		return replica
	}
	assertDeleted := func(t *testing.T, replica *dkvsReplicaStore) {
		t.Helper()
		record, err := replica.LoadSubscriptionRecord("replica", removed)
		if record != nil || !errors.Is(err, indexercommon.ErrKeyNotFound) {
			t.Errorf("deleted record remains live in local replica: present=%t err=%v", record != nil, err)
		}
		state, err := replica.LoadLocalKeyState("replica", removed)
		if err != nil || state == nil || !state.Deleted || state.Seq != 2 || state.ETag != direct.ETag {
			t.Errorf("local replica lost deleted key state/sequence floor: state=%+v err=%v", state, err)
		}
		if record, err := replica.LoadSubscriptionRecord("replica", keep); err != nil || record == nil { t.Fatal("deletion removed unrelated record") }
	}
	t.Run("delta_notifies_deletion", func(t *testing.T) {
		replica := newReplica(t)
		delta, err := server.PrefixDelta(prefix, before.EndpointID, before.Generation)
		if err != nil { t.Fatal(err) }
		found := false
		for _, state := range delta.KeyStates { found = found || state.Key == removed && state.Status == dkvsindexer.KeyStateDeleted && state.Seq == 2 }
		if !found { t.Error("server advanced generation without reporting deleted key") }
		if _, err := replica.ApplyPrefixDelta("replica", before.Generation, delta); err != nil { t.Fatal(err) }
		assertDeleted(t, replica)
	})
	t.Run("snapshot_carries_deletion_floor", func(t *testing.T) {
		replica := newReplica(t)
		after, err := server.PrefixSnapshot(prefix)
		if err != nil { t.Fatal(err) }
		if _, err := replica.ReplacePrefixSnapshot("replica", after); err != nil { t.Fatal(err) }
		assertDeleted(t, replica)
	})
	t.Run("recreation_supersedes_deletion", func(t *testing.T) {
		put(removed, 3)
		after, err := server.PrefixSnapshot(prefix)
		if err != nil { t.Fatal(err) }
		replica := newReplica(t)
		if _, err := replica.ReplacePrefixSnapshot("replica", after); err != nil { t.Fatal(err) }
		record, err := replica.LoadSubscriptionRecord("replica", removed)
		if err != nil || record == nil || record.Seq != 3 { t.Fatal("recreated record is not live at sequence 3") }
		state, err := replica.LoadLocalKeyState("replica", removed)
		if err != nil || state == nil || state.Deleted || state.Seq != 3 { t.Fatal("recreated key retained stale deletion") }
	})
}
