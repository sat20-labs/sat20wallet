package wallet

import (
	"errors"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func TestDKVSDeletedPayloadValidationIsAtomic(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	t.Cleanup(func() { _ = database.Close() })
	server := dkvsindexer.New(database, dkvsindexer.Config{
		EndpointID: "deleted-payload-validation", AllowFreeLocal: true,
		FeeVerifier: dkvsindexer.JSONFeeVerifier{AllowFreeLocal: true},
		CurrentHeight: func() uint64 { return 100 },
	})
	priv, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	signer := dkvsTestWalletFromPriv(t, priv)
	prefix, err := dkvsindexer.PersonalKey(signer.GetPubKey().SerializeCompressed(), "deletion-validation")
	if err != nil { t.Fatal(err) }
	key := prefix + "/key"
	record, err := NewDKVSSignedRecord(signer, key, []byte("unchanged-on-error"), dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 100, TTL: 100})
	if err != nil { t.Fatal(err) }
	if _, err := server.PutLocal(record); err != nil { t.Fatal(err) }
	before, err := server.PrefixSnapshot(prefix)
	if err != nil { t.Fatal(err) }
	tombstone, err := NewDKVSSignedTombstone(signer, key, dkvsindexer.RecordOptions{Seq: 2, IssueHeight: 100})
	if err != nil { t.Fatal(err) }
	if _, err := server.PutLocal(tombstone); err != nil { t.Fatal(err) }
	valid, err := server.PrefixDelta(prefix, before.EndpointID, before.Generation)
	if err != nil { t.Fatal(err) }
	if len(valid.KeyStates) != 1 || valid.KeyStates[0].Status != dkvsindexer.KeyStateDeleted {
		t.Fatal("fixture did not reach a real deletion")
	}
	cases := []struct {
		name string
		mutate func(*dkvsindexer.PrefixDeltaResult)
	}{
		{"duplicate_state", func(d *dkvsindexer.PrefixDeltaResult) { d.KeyStates = append(d.KeyStates, d.KeyStates[0]) }},
		{"foreign_prefix", func(d *dkvsindexer.PrefixDeltaResult) { d.KeyStates[0].Key = prefix + "-other/key" }},
		{"active_without_value", func(d *dkvsindexer.PrefixDeltaResult) { d.KeyStates[0].Status = dkvsindexer.KeyStateActive }},
		{"deleted_with_live_value", func(d *dkvsindexer.PrefixDeltaResult) { d.Records = []*swire.DKVSRecord{record} }},
		{"deleted_with_embedded_record", func(d *dkvsindexer.PrefixDeltaResult) { d.KeyStates[0].Record = record }},
		{"invalid_hash", func(d *dkvsindexer.PrefixDeltaResult) { d.KeyStates[0].ETag = "not-a-hash" }},
		{"zero_sequence", func(d *dkvsindexer.PrefixDeltaResult) { d.KeyStates[0].Seq = 0 }},
		{"never_seen_is_not_a_deletion", func(d *dkvsindexer.PrefixDeltaResult) { d.KeyStates[0].Status = dkvsindexer.KeyStateNeverSeen }},
		{"same_sequence_conflicting_hash", func(d *dkvsindexer.PrefixDeltaResult) { d.KeyStates[0].Seq = 1 }},
		{"oversized_state_set", func(d *dkvsindexer.PrefixDeltaResult) {
			d.KeyStates = make([]dkvsindexer.DKVSKeyState, dkvsindexer.MaxPrefixReadRecords+1)
		}},
	}
	for _, mode := range []string{"delta", "snapshot"} {
		for _, test := range cases {
			t.Run(mode+"/"+test.name, func(t *testing.T) {
				db := indexerdb.NewKVDB(t.TempDir())
				t.Cleanup(func() { _ = db.Close() })
				replica := newDKVSReplicaStore(db)
				if _, err := replica.ReplacePrefixSnapshot("replica", before); err != nil { t.Fatal(err) }
				bad := *valid
				bad.KeyStates = append([]dkvsindexer.DKVSKeyState(nil), valid.KeyStates...)
				bad.Records = append([]*swire.DKVSRecord(nil), valid.Records...)
				test.mutate(&bad)
				var applyErr error
				if mode == "delta" {
					_, applyErr = replica.ApplyPrefixDelta("replica", before.Generation, &bad)
				} else {
					_, applyErr = replica.ReplacePrefixSnapshot("replica", &dkvsindexer.PrefixSnapshot{
						EndpointID: bad.EndpointID, Prefix: bad.Prefix, Generation: bad.Generation,
						ViewHeight: bad.ViewHeight, Records: bad.Records, KeyStates: bad.KeyStates,
					})
				}
				if applyErr == nil { t.Fatal("invalid deletion payload was accepted") }
				current, err := replica.LoadSubscriptionRecord("replica", key)
				if err != nil || current == nil || dkvsindexer.RecordHash(current) != dkvsindexer.RecordHash(record) {
					t.Fatal("rejected deletion altered the live record")
				}
				state, err := replica.LoadSubscriptionState("replica")
				if err != nil || state.Generations[prefix] != before.Generation {
					t.Fatal("rejected deletion advanced the directory cursor")
				}
			})
		}
	}
	t.Run("stale_snapshot_cannot_resurrect_deleted_value", func(t *testing.T) {
		db := indexerdb.NewKVDB(t.TempDir())
		t.Cleanup(func() { _ = db.Close() })
		replica := newDKVSReplicaStore(db)
		if _, err := replica.ReplacePrefixSnapshot("replica", before); err != nil { t.Fatal(err) }
		changed, err := replica.ApplyPrefixDelta("replica", before.Generation, valid)
		if err != nil { t.Fatal(err) }
		if len(changed) != 1 || changed[0] != key { t.Fatal("deletion did not notify the changed-key observer") }
		if _, err := replica.ReplacePrefixSnapshot("replica", before); err == nil { t.Fatal("stale baseline resurrected a deleted key") }
		if _, err := replica.LoadSubscriptionRecord("replica", key); !errors.Is(err, indexercommon.ErrKeyNotFound) { t.Fatal("deleted value remains readable") }
		state, err := replica.LoadSubscriptionState("replica")
		if err != nil || state.Generations[prefix] != valid.Generation { t.Fatal("stale baseline rolled back the cursor") }
	})
}
