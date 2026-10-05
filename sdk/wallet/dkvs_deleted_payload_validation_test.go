package wallet

import (
	"context"
	"errors"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// A physical deletion is represented by a complete current view, not a
// synthetic deleted key-state. Invalid data or provenance must leave both the
// previous confirmed values and the completed source cursor unchanged.
func TestDKVSCurrentPayloadValidationIsAtomic(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("create server database")
	}
	t.Cleanup(func() { _ = database.Close() })
	server := dkvsindexer.New(database, dkvsindexer.Config{
		EndpointID: "current-payload-validation", AllowFreeLocal: true,
		FeeVerifier:   dkvsindexer.JSONFeeVerifier{AllowFreeLocal: true},
		CurrentHeight: func() uint64 { return 100 },
	})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer := dkvsTestWalletFromPriv(t, priv)
	prefix, err := dkvsindexer.PersonalKey(signer.GetPubKey().SerializeCompressed(), "deletion-validation")
	if err != nil {
		t.Fatal(err)
	}
	key := prefix + "/key"
	record, err := NewDKVSSignedRecord(signer, key, []byte("unchanged-on-error"), dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 100, TTL: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	scope := dkvsindexer.ActiveScope{Prefix: prefix}
	before, err := server.ActiveMetadata(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	command, err := NewDKVSDeleteCommand(signer, record, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.PutLocal(command); err != nil {
		t.Fatal(err)
	}
	after, err := server.ActiveMetadata(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	newReplica := func(t *testing.T) *dkvsReplicaStore {
		t.Helper()
		db := indexerdb.NewKVDB(t.TempDir())
		if db == nil {
			t.Fatal("create replica database")
		}
		t.Cleanup(func() { _ = db.Close() })
		replica := newDKVSReplicaStore(db)
		baseline, err := replica.ActiveBaseline("replica", scope)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := replica.InstallActiveState("replica", baseline, before, []*swire.DKVSRecord{record}, true); err != nil {
			t.Fatal(err)
		}
		return replica
	}
	type payload struct {
		meta    dkvsindexer.ActiveMeta
		records []*swire.DKVSRecord
	}
	cases := []struct {
		name   string
		mutate func(*payload)
	}{
		{"duplicate_key", func(p *payload) { p.records = []*swire.DKVSRecord{record, record} }},
		{"foreign_prefix", func(p *payload) {
			copyRecord := *record
			copyRecord.Key = prefix + "-other/key"
			p.records = []*swire.DKVSRecord{&copyRecord}
		}},
		{"delete_command_is_not_snapshot_data", func(p *payload) { p.records = []*swire.DKVSRecord{command} }},
		{"nil_record", func(p *payload) { p.records = []*swire.DKVSRecord{nil} }},
		{"invalid_signature", func(p *payload) {
			copyRecord := *record
			copyRecord.Signature = []byte{1, 2, 3}
			p.records = []*swire.DKVSRecord{&copyRecord}
		}},
		{"unknown_flags", func(p *payload) {
			copyRecord := *record
			copyRecord.Flags = 1 << 20
			p.records = []*swire.DKVSRecord{&copyRecord}
		}},
		{"mismatched_complete_root", func(p *payload) { p.meta.Root = before.Root }},
		{"wrong_endpoint", func(p *payload) { p.meta.EndpointID = "not-the-confirmed-source" }},
		{"generation_rollback", func(p *payload) { p.meta.Generation = before.Generation - 1 }},
		{"height_rollback", func(p *payload) { p.meta.ViewHeight = before.ViewHeight - 1 }},
	}
	for _, full := range []bool{false, true} {
		mode := "delta"
		if full {
			mode = "snapshot"
		}
		for _, test := range cases {
			t.Run(mode+"/"+test.name, func(t *testing.T) {
				replica := newReplica(t)
				baseline, err := replica.ActiveBaseline("replica", scope)
				if err != nil {
					t.Fatal(err)
				}
				bad := payload{meta: after}
				test.mutate(&bad)
				// For delta mode, returning the unchanged old root is a valid
				// no-op only at the original generation. Force an inconsistent
				// root here so this case tests malformed data, not omission.
				if !full && test.name == "mismatched_complete_root" {
					bad.meta.Root[0] ^= 1
				}
				if _, err := replica.InstallActiveState("replica", baseline, bad.meta, bad.records, full); err == nil {
					t.Fatal("invalid current-state payload accepted")
				}
				unchanged, err := replica.ActiveBaseline("replica", scope)
				if err != nil || unchanged != baseline {
					t.Fatal("rejected payload modified confirmed data/cursor")
				}
				current, err := replica.LoadSubscriptionRecord("replica", key)
				if err != nil || current == nil || dkvsindexer.RecordHash(current) != dkvsindexer.RecordHash(record) {
					t.Fatal("rejected payload changed the live record")
				}
			})
		}
	}
	t.Run("stale_snapshot_cannot_resurrect_physically_deleted_value", func(t *testing.T) {
		replica := newReplica(t)
		baseline, err := replica.ActiveBaseline("replica", scope)
		if err != nil {
			t.Fatal(err)
		}
		changed, err := replica.InstallActiveState("replica", baseline, after, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(changed) != 1 || changed[0] != key {
			t.Fatal("deletion did not notify changed-key observers")
		}
		baseline, err = replica.ActiveBaseline("replica", scope)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := replica.InstallActiveState("replica", baseline, before, []*swire.DKVSRecord{record}, true); !errors.Is(err, dkvsindexer.ErrStaleEndpoint) {
			t.Fatalf("stale baseline resurrected key: %v", err)
		}
		if _, err := replica.LoadSubscriptionRecord("replica", key); !errors.Is(err, indexercommon.ErrKeyNotFound) {
			t.Fatal("deleted value remains readable")
		}
		if _, err := replica.LoadLocalKeyState("replica", key); !errors.Is(err, indexercommon.ErrKeyNotFound) {
			t.Fatal("deleted key retained local history")
		}
	})
	t.Run("same_generation_and_height_cannot_change_confirmed_root", func(t *testing.T) {
		replica := newReplica(t)
		baseline, err := replica.ActiveBaseline("replica", scope)
		if err != nil {
			t.Fatal(err)
		}
		invalid := after
		invalid.Generation = before.Generation
		if _, err := replica.InstallActiveState("replica", baseline, invalid, nil, true); err == nil {
			t.Fatal("same source position replaced a confirmed root")
		}
		unchanged, err := replica.ActiveBaseline("replica", scope)
		if err != nil || unchanged != baseline {
			t.Fatal("conflicting source position changed data/cursor")
		}
	})
}
