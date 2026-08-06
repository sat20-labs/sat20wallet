package rgb11wallet

import (
	"errors"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	corestorage "github.com/sat20-labs/rgb11/storage"
)

func TestEngineTransactionFreezesScopeAtBegin(t *testing.T) {
	db := indexerdb.NewKVDB(t.TempDir())
	defer db.Close()
	store := NewEngineStore(db)
	if err := store.SetScope("wallet-a-account-0"); err != nil {
		t.Fatal(err)
	}
	tx, err := store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetScope("wallet-b-account-0"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("state"), []byte("scope-a")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get([]byte("state")); !errors.Is(err, corestorage.ErrNotFound) {
		t.Fatalf("transaction leaked into the new scope: %v", err)
	}
	key, err := engineStoreKey("wallet-a-account-0", []byte("state"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := db.Read(key)
	if err != nil && !errors.Is(err, indexer.ErrKeyNotFound) {
		t.Fatal(err)
	}
	if string(value) != "scope-a" {
		t.Fatalf("transaction did not commit to its original scope: %q", value)
	}
}
