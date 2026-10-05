package dkvs

import (
	"errors"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func TestApplyWriteResultAndAckDoesNotMaterializeOrAdvancePrefixToken(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB returned nil")
	}
	defer database.Close()
	store := NewReplicaStore(database)
	namespace, endpointID := "testnet:account", "core-1"
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
	if err := store.PreparePrefixSync(namespace, endpointID, []string{prefix}, true); err != nil {
		t.Fatal(err)
	}
	scope := dkvsindexer.ActiveScope{Prefix: prefix}
	baseline, err := store.ActiveBaseline(namespace, scope)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.InstallActiveState(namespace, baseline, dkvsindexer.ActiveMeta{EndpointID: endpointID, Scope: scope, Generation: 7}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompletePrefixSync(namespace, endpointID, []string{prefix}); err != nil {
		t.Fatal(err)
	}
	record, err := dkvsindexer.NewRecord(key, []byte("state-v1"), publicKey, dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := NewBatchOutboxEntry(namespace, []dkvsindexer.CASMutation{{Record: record, Precondition: dkvsindexer.WritePrecondition{ExpectAbsent: true}}}, endpointID, OutboxOrigin{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.QueueOutbox(entry); err != nil {
		t.Fatal(err)
	}
	hash := dkvsindexer.RecordHash(record).String()
	invalid := &dkvsindexer.WriteResult{Applied: 1, Records: []*dkvsindexer.Record{record}, Hashes: []string{hash}, EndpointID: endpointID, RequestID: entry.RequestID, ViewHeight: 101}
	if err := store.ApplyWriteResultAndAck(entry, invalid); !errors.Is(err, dkvsindexer.ErrInvalidRecord) {
		t.Fatalf("missing prefix state err=%v", err)
	}
	unchanged, err := store.LoadSubscriptionState(namespace)
	if err != nil || unchanged.Generations[prefix] != 7 {
		t.Fatalf("rejected ACK changed state=%#v err=%v", unchanged, err)
	}
	if _, err := store.LoadSubscriptionRecord(namespace, key); !errors.Is(err, indexercommon.ErrKeyNotFound) {
		t.Fatalf("invalid ACK installed record: %v", err)
	}
	pending, err := store.HasPendingOutbox(namespace)
	if err != nil || !pending {
		t.Fatalf("invalid ACK removed outbox: pending=%v err=%v", pending, err)
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
	if updated.Generations[prefix] != 7 || updated.ViewHeight != 0 || updated.Status != DKVSSubscriptionReady {
		t.Fatalf("ACK advanced subscription=%#v", updated)
	}
	if _, err := store.LoadSubscriptionRecord(namespace, key); !errors.Is(err, indexercommon.ErrKeyNotFound) {
		t.Fatalf("ACK materialized confirmed record: %v", err)
	}
	if _, err := store.LoadLocalKeyState(namespace, key); !errors.Is(err, indexercommon.ErrKeyNotFound) {
		t.Fatalf("ACK materialized local key state: %v", err)
	}
	pending, err = store.HasPendingOutbox(namespace)
	if err != nil || pending {
		t.Fatalf("outbox pending=%v err=%v", pending, err)
	}

	// A subsequent server-accepted update also completes only its request.
	// Until the sync installer runs, neither ACK supplies confirmed KV data.
	rebasedRecord, err := dkvsindexer.NewRecord(key, []byte("state-v2"), publicKey, dkvsindexer.RecordOptions{Seq: 2, IssueHeight: 101})
	if err != nil {
		t.Fatal(err)
	}
	previousHash := dkvsindexer.RecordHash(record)
	rebasedEntry, err := NewBatchOutboxEntry(namespace, []dkvsindexer.CASMutation{{Record: rebasedRecord, Precondition: dkvsindexer.WritePrecondition{ExpectedHash: &previousHash}}}, endpointID, OutboxOrigin{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.QueueOutbox(rebasedEntry); err != nil {
		t.Fatal(err)
	}
	rebasedResult := &dkvsindexer.WriteResult{Applied: 1, Records: []*dkvsindexer.Record{rebasedRecord}, Hashes: []string{dkvsindexer.RecordHash(rebasedRecord).String()},
		EndpointID: endpointID, RequestID: rebasedEntry.RequestID, ViewHeight: 102, PrefixStates: []dkvsindexer.PrefixGeneration{{Prefix: prefix, Generation: 9}}}
	if err := store.ApplyWriteResultAndAck(rebasedEntry, rebasedResult); err != nil {
		t.Fatal(err)
	}
	rebasedState, err := store.LoadSubscriptionState(namespace)
	if err != nil {
		t.Fatal(err)
	}
	if rebasedState.Generations[prefix] != 7 || rebasedState.ViewHeight != 0 {
		t.Fatalf("update ACK advanced cursor/view: %#v", rebasedState)
	}
	if _, err := store.LoadSubscriptionRecord(namespace, key); !errors.Is(err, indexercommon.ErrKeyNotFound) {
		t.Fatalf("update ACK materialized record: %v", err)
	}
	if _, err := store.LoadLocalKeyState(namespace, key); !errors.Is(err, indexercommon.ErrKeyNotFound) {
		t.Fatalf("update ACK materialized state: %v", err)
	}
	pending, err = store.HasPendingOutbox(namespace)
	if err != nil || pending {
		t.Fatalf("update ACK failed to complete outbox: pending=%v err=%v", pending, err)
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
	record, err := dkvsindexer.NewRecord(key, []byte("recovery"), publicKey, dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := NewBatchOutboxEntry(namespace, []dkvsindexer.CASMutation{{Record: record, Precondition: dkvsindexer.WritePrecondition{ExpectAbsent: true}}}, "core-a", OutboxOrigin{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.QueueOutbox(entry); err != nil {
		t.Fatal(err)
	}
	result := &dkvsindexer.WriteResult{Applied: 1, Records: []*dkvsindexer.Record{record}, Hashes: []string{dkvsindexer.RecordHash(record).String()}, EndpointID: "core-b", RequestID: entry.RequestID}
	if err := store.ApplyWriteResultAndAck(entry, result); !errors.Is(err, dkvsindexer.ErrEndpointMismatch) {
		t.Fatalf("unpinned endpoint err=%v", err)
	}
	result.EndpointID = "core-a"
	if err := store.ApplyWriteResultAndAck(entry, result); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadSubscriptionState(namespace); !errors.Is(err, indexercommon.ErrKeyNotFound) {
		t.Fatalf("unmanaged ACK created subscription: %v", err)
	}
	if _, err := store.LoadSubscriptionRecord(namespace, key); !errors.Is(err, indexercommon.ErrKeyNotFound) {
		t.Fatalf("unmanaged ACK created record: %v", err)
	}
	pending, err := store.HasPendingOutbox(namespace)
	if err != nil || pending {
		t.Fatalf("unmanaged outbox pending=%v err=%v", pending, err)
	}
}
