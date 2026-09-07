package dkvs

import (
	"errors"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func TestApplyWriteResultAndAckAdvancesPrefixTokenAtomically(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB returned nil")
	}
	defer database.Close()
	store := NewReplicaStore(database)
	namespace := "testnet:account"
	endpointID := "core-1"

	privateKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	publicKey := privateKey.PubKey().SerializeCompressed()
	key, err := dkvsindexer.PersonalKey(publicKey, "account/state")
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := dkvsindexer.CollectionPathForKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PersistRegisteredPrefixes(namespace, []string{prefix}); err != nil {
		t.Fatal(err)
	}

	batch := database.NewWriteBatch()
	state := &SubscriptionState{
		EndpointID: endpointID, Prefixes: []string{prefix},
		Generations: map[string]uint64{prefix: 7}, Status: DKVSSubscriptionReady,
	}
	if err := putSubscriptionStateBatch(batch, namespace, state); err != nil {
		batch.Close()
		t.Fatal(err)
	}
	if err := batch.Flush(); err != nil {
		batch.Close()
		t.Fatal(err)
	}
	batch.Close()

	record, err := dkvsindexer.NewRecord(key, []byte("state-v1"), publicKey,
		dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := NewBatchOutboxEntry(namespace, []dkvsindexer.CASMutation{{
		Record: record, Precondition: dkvsindexer.WritePrecondition{ExpectAbsent: true},
	}}, endpointID, OutboxOrigin{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.QueueOutbox(entry); err != nil {
		t.Fatal(err)
	}

	hash := dkvsindexer.RecordHash(record).String()
	invalid := &dkvsindexer.WriteResult{
		Applied: 1, Records: []*dkvsindexer.Record{record}, Hashes: []string{hash},
		EndpointID: endpointID, RequestID: entry.RequestID, ViewHeight: 101,
	}
	if err := store.ApplyWriteResultAndAck(entry, invalid); !errors.Is(err, dkvsindexer.ErrInvalidRecord) {
		t.Fatalf("missing prefix state err=%v", err)
	}
	unchanged, err := store.LoadSubscriptionState(namespace)
	if err != nil || unchanged.Generations[prefix] != 7 {
		t.Fatalf("state changed after rejected ACK state=%#v err=%v", unchanged, err)
	}
	if _, err := store.LoadSubscriptionRecord(namespace, key); !errors.Is(err, indexercommon.ErrKeyNotFound) {
		t.Fatalf("record was applied before a valid ACK err=%v", err)
	}

	valid := *invalid
	valid.PrefixStates = []dkvsindexer.PrefixGeneration{{Prefix: prefix, Generation: 8}}
	if err := store.ApplyWriteResultAndAck(entry, &valid); err != nil {
		t.Fatal(err)
	}
	updated, err := store.LoadSubscriptionState(namespace)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Generations[prefix] != 8 || updated.ViewHeight != 101 ||
		updated.Status != DKVSSubscriptionReady {
		t.Fatalf("updated subscription state=%#v", updated)
	}
	stored, err := store.LoadSubscriptionRecord(namespace, key)
	if err != nil || dkvsindexer.RecordHash(stored) != dkvsindexer.RecordHash(record) {
		t.Fatalf("stored record=%#v err=%v", stored, err)
	}
	pending, err := store.HasPendingOutbox(namespace)
	if err != nil || pending {
		t.Fatalf("outbox pending=%v err=%v", pending, err)
	}

	rebasedRecord, err := dkvsindexer.NewRecord(key, []byte("state-v2"), publicKey,
		dkvsindexer.RecordOptions{Seq: 2, IssueHeight: 101})
	if err != nil {
		t.Fatal(err)
	}
	previousHash := dkvsindexer.RecordHash(record)
	rebasedEntry, err := NewBatchOutboxEntry(namespace, []dkvsindexer.CASMutation{{
		Record: rebasedRecord, Precondition: dkvsindexer.WritePrecondition{ExpectedHash: &previousHash},
	}}, endpointID, OutboxOrigin{PreservePrefixGenerations: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.QueueOutbox(rebasedEntry); err != nil {
		t.Fatal(err)
	}
	rebasedHash := dkvsindexer.RecordHash(rebasedRecord).String()
	rebasedResult := &dkvsindexer.WriteResult{
		Applied: 1, Records: []*dkvsindexer.Record{rebasedRecord}, Hashes: []string{rebasedHash},
		EndpointID: endpointID, RequestID: rebasedEntry.RequestID, ViewHeight: 102,
		PrefixStates: []dkvsindexer.PrefixGeneration{{Prefix: prefix, Generation: 9}},
	}
	if err := store.ApplyWriteResultAndAck(rebasedEntry, rebasedResult); err != nil {
		t.Fatal(err)
	}
	rebasedState, err := store.LoadSubscriptionState(namespace)
	if err != nil {
		t.Fatal(err)
	}
	if rebasedState.Generations[prefix] != 8 {
		t.Fatalf("conflict rebase hid unrelated prefix changes: generation=%d",
			rebasedState.Generations[prefix])
	}
	rebasedStored, err := store.LoadSubscriptionRecord(namespace, key)
	if err != nil || dkvsindexer.RecordHash(rebasedStored) != dkvsindexer.RecordHash(rebasedRecord) {
		t.Fatalf("rebased record=%#v err=%v", rebasedStored, err)
	}
}

func TestApplyWriteResultAndAckUnmanagedWriteDoesNotRequireReplica(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB returned nil")
	}
	defer database.Close()
	store := NewReplicaStore(database)
	namespace := "testnet:unmanaged"
	privateKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	publicKey := privateKey.PubKey().SerializeCompressed()
	key, err := dkvsindexer.PersonalKey(publicKey, "account/recovery/package")
	if err != nil {
		t.Fatal(err)
	}
	record, err := dkvsindexer.NewRecord(key, []byte("recovery"), publicKey,
		dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := NewBatchOutboxEntry(namespace, []dkvsindexer.CASMutation{{
		Record: record, Precondition: dkvsindexer.WritePrecondition{ExpectAbsent: true},
	}}, "core-a", OutboxOrigin{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.QueueOutbox(entry); err != nil {
		t.Fatal(err)
	}
	hash := dkvsindexer.RecordHash(record).String()
	result := &dkvsindexer.WriteResult{
		Applied: 1, Records: []*dkvsindexer.Record{record}, Hashes: []string{hash},
		EndpointID: "core-b", RequestID: entry.RequestID,
	}
	if err := store.ApplyWriteResultAndAck(entry, result); !errors.Is(err, dkvsindexer.ErrEndpointMismatch) {
		t.Fatalf("unpinned endpoint err=%v", err)
	}
	result.EndpointID = "core-a"
	if err := store.ApplyWriteResultAndAck(entry, result); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadSubscriptionState(namespace); !errors.Is(err, indexercommon.ErrKeyNotFound) {
		t.Fatalf("unmanaged write created subscription state err=%v", err)
	}
	if _, err := store.LoadSubscriptionRecord(namespace, key); !errors.Is(err, indexercommon.ErrKeyNotFound) {
		t.Fatalf("unmanaged write persisted replica record err=%v", err)
	}
	pending, err := store.HasPendingOutbox(namespace)
	if err != nil || pending {
		t.Fatalf("unmanaged outbox pending=%v err=%v", pending, err)
	}
}
