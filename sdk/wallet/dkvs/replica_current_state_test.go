package dkvs

import (
	"errors"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func TestReplicaStateIsDerivedFromConfirmedRecordAndActiveMeta(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB returned nil")
	}
	t.Cleanup(func() { database.Close() })
	store := NewReplicaStore(database)
	const namespace = "testnet:derived"
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := dkvsindexer.PersonalKey(priv.PubKey().SerializeCompressed(), "derived/value")
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := dkvsindexer.CollectionPathForKey(key)
	if err != nil {
		t.Fatal(err)
	}
	scope := dkvsindexer.ActiveScope{Prefix: prefix}
	proof, err := dkvsindexer.EncodeFeeProof(&dkvsindexer.FeeProof{Mode: dkvsindexer.FeeModeFreeLocal})
	if err != nil {
		t.Fatal(err)
	}
	record, err := dkvsindexer.NewAccountRecord(key, []byte("confirmed"), dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 10, TTL: 100, FeeProof: proof})
	if err != nil {
		t.Fatal(err)
	}
	hash := dkvsindexer.SigningHash(record)
	signature, err := schnorr.Sign(priv, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	record.Signature = signature.Serialize()
	if err := store.AddRegisteredPrefix(namespace, prefix); err != nil {
		t.Fatal(err)
	}
	if err := store.PreparePrefixSync(namespace, "core", []string{prefix}, true); err != nil {
		t.Fatal(err)
	}
	baseline, err := store.ActiveBaseline(namespace, scope)
	if err != nil {
		t.Fatal(err)
	}
	root, err := dkvsindexer.ActiveRecordsRoot([]*dkvsindexer.Record{record})
	if err != nil {
		t.Fatal(err)
	}
	meta := dkvsindexer.ActiveMeta{EndpointID: "core", Scope: scope, Generation: 7, Root: root, ViewHeight: 10}
	if _, err := store.InstallActiveState(namespace, baseline, meta, []*dkvsindexer.Record{record}, true); err != nil {
		t.Fatal(err)
	}
	if err := store.CompletePrefixSync(namespace, "core", []string{prefix}); err != nil {
		t.Fatal(err)
	}
	local, err := store.LoadLocalKeyState(namespace, key)
	if err != nil {
		t.Fatal(err)
	}
	if local.Seq != 1 || local.ETag != dkvsindexer.RecordHash(record).String() || local.ExpiryHeight != 110 || local.StorageMode != dkvsindexer.StorageModeFreeLocal {
		t.Fatalf("derived key state=%+v", local)
	}
	state, err := store.LoadSubscriptionState(namespace)
	if err != nil || state.Generations[prefix] != 7 {
		t.Fatalf("derived generation=%+v err=%v", state, err)
	}
	rows := 0
	if err := database.BatchRead([]byte("dkvs:"), false, func(_, _ []byte) error { rows++; return nil }); err != nil {
		t.Fatal(err)
	}
	if rows != 4 {
		t.Fatalf("expected registry, readiness, record and ActiveMeta only; rows=%d", rows)
	}
	// Reopen through a fresh store to prove this is derived from disk, not a
	// second mutable key-state cache or a duplicated generation field.
	store = NewReplicaStore(database)
	baseline, err = store.ActiveBaseline(namespace, scope)
	if err != nil {
		t.Fatal(err)
	}
	meta.Generation++
	meta.Root = [32]byte{}
	if _, err := store.InstallActiveState(namespace, baseline, meta, nil, true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadLocalKeyState(namespace, key); !errors.Is(err, indexercommon.ErrKeyNotFound) {
		t.Fatalf("removed key state retained: %v", err)
	}
	state, err = store.LoadSubscriptionState(namespace)
	if err != nil || state.Generations[prefix] != 8 {
		t.Fatalf("generation did not follow ActiveMeta: %+v err=%v", state, err)
	}
}
