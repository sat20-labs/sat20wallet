package wallet

import (
	"context"
	"errors"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// Real server index and SDK replica, with current-state deletion semantics.
// Network AUTOPAY settlement is covered separately by the SDK E2E package.
func TestDKVSPhysicalDeleteRoundTrip(t *testing.T) {
	serverDB := indexerdb.NewKVDB(t.TempDir())
	if serverDB == nil {
		t.Fatal("create server database")
	}
	t.Cleanup(func() { _ = serverDB.Close() })
	server := dkvsindexer.New(serverDB, dkvsindexer.Config{
		EndpointID: "physical-delete-regression", AllowFreeLocal: true,
		FeeVerifier:   dkvsindexer.JSONFeeVerifier{AllowFreeLocal: true},
		CurrentHeight: func() uint64 { return 100 },
	})
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer := dkvsTestWalletFromPriv(t, key)
	prefix, err := dkvsindexer.PersonalKey(signer.GetPubKey().SerializeCompressed(), "delete-regression")
	if err != nil {
		t.Fatal(err)
	}
	removed, keep := prefix+"/removed", prefix+"/keep"
	put := func(path, value string, seq uint64) {
		t.Helper()
		record, err := NewDKVSSignedRecord(signer, path, []byte(value), dkvsindexer.RecordOptions{Seq: seq, IssueHeight: 100, TTL: 100})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = server.PutLocal(record); err != nil {
			t.Fatal(err)
		}
	}
	put(removed, "old-lifetime", 1)
	put(keep, "keep", 1)
	before, err := server.ActiveSyncPage(context.Background(), dkvsindexer.ActiveSyncRequest{Scope: dkvsindexer.ActiveScope{Prefix: prefix}, EndpointID: server.EndpointID(), Full: true})
	if err != nil {
		t.Fatal(err)
	}
	scope := dkvsindexer.ActiveScope{Prefix: prefix}
	beforeMeta, err := server.ActiveMetadata(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	original, err := server.Get(removed)
	if err != nil {
		t.Fatal(err)
	}
	command, err := NewDKVSDeleteCommand(signer, original, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = server.PutLocal(command); err != nil {
		t.Fatal(err)
	}
	direct, err := server.GetKeyState(removed)
	if err != nil {
		t.Fatal(err)
	}
	if direct.Status != dkvsindexer.KeyStateNeverSeen || direct.Seq != 0 || direct.ETag != "" || direct.Record != nil {
		t.Fatalf("deleted key retained history: %+v", direct)
	}
	afterMeta, err := server.ActiveMetadata(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	after, err := server.ActiveSyncPage(context.Background(), dkvsindexer.ActiveSyncRequest{Scope: dkvsindexer.ActiveScope{Prefix: prefix}, EndpointID: server.EndpointID(), Full: true})
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
		if _, err := replica.InstallActiveState("replica", baseline, beforeMeta, before.Records, true); err != nil {
			t.Fatal(err)
		}
		return replica
	}
	assertDeleted := func(t *testing.T, replica *dkvsReplicaStore) {
		t.Helper()
		if _, err := replica.LoadSubscriptionRecord("replica", removed); !errors.Is(err, indexercommon.ErrKeyNotFound) {
			t.Fatalf("deleted record remains in replica: %v", err)
		}
		if _, err := replica.LoadLocalKeyState("replica", removed); !errors.Is(err, indexercommon.ErrKeyNotFound) {
			t.Fatalf("replica retained deleted-key state: %v", err)
		}
		if record, err := replica.LoadSubscriptionRecord("replica", keep); err != nil || record == nil {
			t.Fatal("deletion removed sibling")
		}
		meta, err := replica.LoadActiveMeta("replica", scope)
		if err != nil || meta.Generation != afterMeta.Generation || meta.Root != afterMeta.Root {
			t.Fatalf("confirmed cursor=%+v err=%v", meta, err)
		}
	}
	t.Run("delta_root_mismatch_requires_full_current_set", func(t *testing.T) {
		replica := newReplica(t)
		baseline, err := replica.ActiveBaseline("replica", scope)
		if err != nil {
			t.Fatal(err)
		}
		// Current-key deltas cannot carry a removed key. A root mismatch must
		// not advance the cursor or partially modify the old confirmed replica.
		if _, err := replica.InstallActiveState("replica", baseline, afterMeta, nil, false); !errors.Is(err, dkvsindexer.ErrPathDiverged) {
			t.Fatalf("incomplete delta err=%v", err)
		}
		unchanged, err := replica.ActiveBaseline("replica", scope)
		if err != nil || unchanged != baseline {
			t.Fatal("incomplete delta changed confirmed data/cursor")
		}
		if _, err := replica.InstallActiveState("replica", baseline, afterMeta, after.Records, true); err != nil {
			t.Fatal(err)
		}
		assertDeleted(t, replica)
	})
	t.Run("complete_snapshot_removes_omitted_record", func(t *testing.T) {
		replica := newReplica(t)
		baseline, err := replica.ActiveBaseline("replica", scope)
		if err != nil {
			t.Fatal(err)
		}
		changed, err := replica.InstallActiveState("replica", baseline, afterMeta, after.Records, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(changed) != 1 || changed[0] != removed {
			t.Fatalf("changed keys=%v", changed)
		}
		assertDeleted(t, replica)
	})
	t.Run("recreation_starts_new_lifetime_without_floor", func(t *testing.T) {
		replica := newReplica(t)
		baseline, err := replica.ActiveBaseline("replica", scope)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := replica.InstallActiveState("replica", baseline, afterMeta, after.Records, true); err != nil {
			t.Fatal(err)
		}
		put(removed, "new-lifetime", 1)
		latest, err := server.ActiveSyncPage(context.Background(), dkvsindexer.ActiveSyncRequest{Scope: dkvsindexer.ActiveScope{Prefix: prefix}, EndpointID: server.EndpointID(), Full: true})
		if err != nil {
			t.Fatal(err)
		}
		meta, err := server.ActiveMetadata(context.Background(), scope)
		if err != nil {
			t.Fatal(err)
		}
		baseline, err = replica.ActiveBaseline("replica", scope)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := replica.InstallActiveState("replica", baseline, meta, latest.Records, true); err != nil {
			t.Fatal(err)
		}
		record, err := replica.LoadSubscriptionRecord("replica", removed)
		if err != nil || record == nil || record.Seq != 1 || string(record.Value) != "new-lifetime" {
			t.Fatal("new incarnation not installed")
		}
		if _, err := server.PutLocal(command); !errors.Is(err, dkvsindexer.ErrWriteConflict) {
			t.Fatalf("delayed delete affected new lifetime: %v", err)
		}
	})
}
