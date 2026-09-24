//go:build rgb11discard

package dkvs

import (
	"errors"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func TestDiscardExactReplica(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB returned nil")
	}
	defer database.Close()
	store := NewReplicaStore(database)
	namespace := "testnet:account"
	key := "/mail/receiver/msg/sender/id"
	record := &swire.DKVSRecord{Version: dkvsindexer.Version, Key: key, Value: []byte("ciphertext"), Seq: 1}
	encoded, err := dkvsindexer.MarshalRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	hash := dkvsindexer.RecordHash(record).String()
	batch := database.NewWriteBatch()
	if err := batch.Put(dkvsSubscriptionRecordKey(namespace, key), encoded); err != nil {
		t.Fatal(err)
	}
	if err := putLocalKeyStateBatch(batch, namespace, LocalKeyState{
		Key: key, Seq: 1, ETag: hash, Deleted: false,
	}); err != nil {
		t.Fatal(err)
	}
	if err := batch.Flush(); err != nil {
		t.Fatal(err)
	}
	batch.Close()

	if err := store.DiscardExactReplica(namespace, key, "wrong"); err == nil {
		t.Fatal("mismatched record hash was accepted")
	}
	if _, err := store.LoadSubscriptionRecord(namespace, key); err != nil {
		t.Fatalf("mismatched cleanup removed record: %v", err)
	}
	if err := store.DiscardExactReplica(namespace, key, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadSubscriptionRecord(namespace, key); !errors.Is(err, indexercommon.ErrKeyNotFound) {
		t.Fatalf("record survived cleanup: %v", err)
	}
	if _, err := store.LoadLocalKeyState(namespace, key); !errors.Is(err, indexercommon.ErrKeyNotFound) {
		t.Fatalf("key state survived cleanup: %v", err)
	}
	if err := store.DiscardExactReplica(namespace, key, hash); err != nil {
		t.Fatalf("idempotent cleanup failed: %v", err)
	}
}
