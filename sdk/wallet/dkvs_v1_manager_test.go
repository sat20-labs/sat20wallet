package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func TestDKVSManagerPaidLifecycleRewritesAfterTombstoneAndSyncsPeer(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	writer := newRGB11MultiDeviceManager(t, priv, 1201)
	reader := newRGB11MultiDeviceManager(t, priv, 1202)
	remote := newRGB11MemoryDKVSHTTP()
	configureRGB11DKVSTestManager(writer, remote)
	configureRGB11DKVSTestManager(reader, remote)

	writerStore, err := writer.ensureDKVSManager().primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	readerStore, err := reader.ensureDKVSManager().primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	key, err := dkvsindexer.PersonalKey(priv.PubKey().SerializeCompressed(),
		"account/recovery/dkvs-lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	policy := dkvsStoragePolicy{
		Autopay: &DKVSAutopayOptions{PoolContract: "tc1ptestautopay"},
	}
	put := func(value string, tombstone bool) *dkvsValueMutation {
		return &dkvsValueMutation{
			Key: key, Value: []byte(value), Owner: writer.wallet,
			Policy: policy, Signature: dkvsSignatureAccount, Tombstone: tombstone,
		}
	}
	update := func(value string, tombstone bool) (*dkvsValue, error) {
		values, updateErr := writerStore.Update([]string{key},
			func(map[string]*dkvsValue, map[string]uint64) ([]dkvsValueMutation, error) {
				mutation := put(value, tombstone)
				return []dkvsValueMutation{*mutation}, nil
			})
		if updateErr != nil {
			return nil, updateErr
		}
		if len(values) != 1 {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		return values[0], nil
	}

	first, err := writerStore.Put(*put("first", false))
	if err != nil {
		t.Fatal(err)
	}
	if first.Seq != 1 {
		t.Fatalf("initial paid record sequence=%d", first.Seq)
	}
	peer, err := readerStore.Get(key)
	if err != nil || peer.Seq != 1 || string(peer.Value) != "first" {
		t.Fatalf("peer did not receive initial AUTOPAY record value=%+v err=%v", peer, err)
	}

	updated, err := update("updated", false)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Seq != 2 {
		t.Fatalf("updated paid record sequence=%d", updated.Seq)
	}
	if err := readerStore.Refresh(key); err != nil {
		t.Fatal(err)
	}
	peer, err = readerStore.Get(key)
	if err != nil || peer.Seq != 2 || string(peer.Value) != "updated" {
		t.Fatalf("peer did not receive AUTOPAY update value=%+v err=%v", peer, err)
	}

	deleted, err := update("", true)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Seq != 3 || !dkvsindexer.IsTombstone(deleted.Flags) {
		t.Fatalf("tombstone=%+v", deleted)
	}
	if _, err := writerStore.Get(key); !errors.Is(err, ErrDKVSRecordNotFound) {
		t.Fatalf("writer still exposed tombstoned record: %v", err)
	}
	if err := readerStore.Refresh(key); err != nil {
		t.Fatal(err)
	}
	if _, err := readerStore.Get(key); !errors.Is(err, ErrDKVSRecordNotFound) {
		t.Fatalf("peer still exposed tombstoned record: %v", err)
	}

	rewritten, err := writerStore.Put(*put("rewritten", false))
	if err != nil {
		t.Fatal(err)
	}
	if rewritten.Seq != 4 {
		t.Fatalf("rewrite did not use delete floor: sequence=%d", rewritten.Seq)
	}
	if err := readerStore.Refresh(key); err != nil {
		t.Fatal(err)
	}
	peer, err = readerStore.Get(key)
	if err != nil || peer.Seq != 4 || string(peer.Value) != "rewritten" {
		t.Fatalf("peer did not receive post-delete AUTOPAY rewrite value=%+v err=%v", peer, err)
	}

	remote.mu.Lock()
	defer remote.mu.Unlock()
	if _, ok := remote.deleteFloors[key]; ok {
		t.Fatalf("remote delete floor was not cleared after rewrite")
	}
	if record := remote.records[key]; record == nil || record.Seq != 4 {
		t.Fatalf("remote final record=%+v", record)
	}
}

type invalidFirstDKVSBatchHTTP struct {
	remote *rgb11MemoryDKVSHTTP
	mu     sync.Mutex
	failed bool
}

func (h *invalidFirstDKVSBatchHTTP) SendGetRequest(url *URL) ([]byte, error) {
	return h.remote.SendGetRequest(url)
}

func (h *invalidFirstDKVSBatchHTTP) SendPostRequest(url *URL, body []byte) ([]byte, error) {
	return h.remote.SendPostRequest(url, body)
}

func (h *invalidFirstDKVSBatchHTTP) SendDKVSV1Get(path string,
	query map[string]string) ([]byte, error) {
	return h.remote.SendDKVSV1Get(path, query)
}

func (h *invalidFirstDKVSBatchHTTP) SendDKVSV1Post(path string, body []byte) ([]byte, error) {
	h.mu.Lock()
	if path == "/v3/dkvs/records/batch-cas" && !h.failed {
		h.failed = true
		h.mu.Unlock()
		return json.Marshal(map[string]interface{}{
			"code": -1, "msg": "invalid test record",
			"error_code": string(dkvsindexer.ErrorCodeInvalidRecord),
		})
	}
	h.mu.Unlock()
	return h.remote.SendDKVSV1Post(path, body)
}

func TestDKVSErrorSupportsTypedMatching(t *testing.T) {
	err := &DKVSError{
		Code:    dkvsindexer.ErrorCodeStaleGeneration,
		Message: "remote message text must not control the branch",
	}
	if !errors.Is(err, dkvsindexer.ErrStaleGeneration) {
		t.Fatalf("typed error did not unwrap: %v", err)
	}
	if !IsDKVSErrorCode(err, dkvsindexer.ErrorCodeStaleGeneration) {
		t.Fatalf("typed error code was not preserved: %v", err)
	}
	if errors.Is(err, dkvsindexer.ErrWriteConflict) {
		t.Fatalf("typed error matched the wrong sentinel: %v", err)
	}
}

func TestDKVSPerPathLocksSerializeOnlyTheSamePath(t *testing.T) {
	manager := newDKVSManager(&Manager{})
	defer releaseDKVSManagerRuntime(manager)
	accountA := "0000000000000000000000000000000000000000000000000000000000000000"
	accountB := "1111111111111111111111111111111111111111111111111111111111111111"
	keyA := "/personal/" + accountA + "/account/state"
	keyASamePath := "/personal/" + accountA + "/account/head"
	keyB := "/personal/" + accountB + "/account/state"

	unlockA, err := manager.lockPathsForKeys([]string{keyA})
	if err != nil {
		t.Fatal(err)
	}

	samePath := make(chan func(), 1)
	go func() {
		unlock, lockErr := manager.lockPathsForKeys([]string{keyASamePath})
		if lockErr == nil {
			samePath <- unlock
		}
	}()
	select {
	case unlock := <-samePath:
		unlock()
		unlockA()
		t.Fatal("same logical path was not serialized")
	case <-time.After(30 * time.Millisecond):
	}

	differentPath := make(chan func(), 1)
	go func() {
		unlock, lockErr := manager.lockPathsForKeys([]string{keyB})
		if lockErr == nil {
			differentPath <- unlock
		}
	}()
	select {
	case unlock := <-differentPath:
		unlock()
	case <-time.After(time.Second):
		unlockA()
		t.Fatal("different owner paths were serialized by a global lock")
	}

	unlockA()
	select {
	case unlock := <-samePath:
		unlock()
	case <-time.After(time.Second):
		t.Fatal("same path lock did not resume after release")
	}
}

func TestDKVSBatchOutboxPreservesExactBytesAndPreconditions(t *testing.T) {
	store := newDKVSReplicaStore(newMemoryKVDB())
	record := testDKVSReplicaRecord(t, 1, "exact")
	path, err := dkvsindexer.CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	root := chainhash.DoubleHashH([]byte("baseline"))
	mutations := []dkvsindexer.CASMutation{{
		Record:       record,
		Precondition: dkvsindexer.WritePrecondition{ExpectAbsent: true},
	}}
	conditions := []dkvsindexer.PathWritePrecondition{{
		Path: path, ExpectedRoot: root, ExpectedGeneration: 7,
	}}
	entry, err := newDKVSBatchOutboxEntry("endpoint-a", mutations, conditions, "node-a",
		dkvsOutboxOrigin{Key: record.Key})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.queueBatchOutbox(entry); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.loadBatchOutbox("endpoint-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Key != entry.Key || loaded[0].EndpointID != "node-a" {
		t.Fatalf("unexpected outbox entries: %#v", loaded)
	}
	decodedMutations, decodedConditions, err := loaded[0].decode()
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, err := dkvsindexer.MarshalRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	gotBytes, err := dkvsindexer.MarshalRecord(decodedMutations[0].Record)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatal("outbox regenerated or changed signed record bytes")
	}
	if !decodedMutations[0].Precondition.ExpectAbsent ||
		len(decodedConditions) != 1 ||
		decodedConditions[0].Path != path ||
		decodedConditions[0].ExpectedRoot != root ||
		decodedConditions[0].ExpectedGeneration != 7 {
		t.Fatalf("outbox preconditions changed: %#v %#v", decodedMutations, decodedConditions)
	}
}

func TestDKVSQueuePreservesTerminalEntryWithSameKey(t *testing.T) {
	store := newDKVSReplicaStore(newMemoryKVDB())
	record := testDKVSReplicaRecord(t, 1, "terminal-evidence")
	entry, err := newDKVSBatchOutboxEntry("terminal-namespace",
		[]dkvsindexer.CASMutation{{
			Record: record, Precondition: dkvsindexer.WritePrecondition{ExpectAbsent: true},
		}}, nil, "endpoint-a", dkvsOutboxOrigin{
			Key: record.Key, Domain: accountManagedOutboxDomain, Generation: 3,
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.queueBatchOutbox(entry); err != nil {
		t.Fatal(err)
	}
	remoteErr := &DKVSError{
		Code: dkvsindexer.ErrorCodeInvalidRecord, Message: "future height fixture",
	}
	if err := store.updateBatchOutboxState(entry, dkvsSessionInflight, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.markOutboxTerminal(entry, remoteErr); err != nil {
		t.Fatal(err)
	}
	originalAttempts := entry.Attempts

	retry, err := newDKVSBatchOutboxEntry("terminal-namespace",
		[]dkvsindexer.CASMutation{{
			Record: record, Precondition: dkvsindexer.WritePrecondition{ExpectAbsent: true},
		}}, nil, "endpoint-a", dkvsOutboxOrigin{
			Key: record.Key, Domain: accountManagedOutboxDomain, Generation: 3,
		})
	if err != nil {
		t.Fatal(err)
	}
	var terminal *dkvsTerminalOutboxError
	if err := store.queueBatchOutbox(retry); !errors.As(err, &terminal) {
		t.Fatalf("same terminal key was overwritten: %v", err)
	}
	loaded, err := store.loadBatchOutbox("terminal-namespace")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].State != dkvsSessionTerminal ||
		loaded[0].Attempts != originalAttempts || loaded[0].LastError != remoteErr.Error() ||
		loaded[0].OriginGeneration != 3 {
		t.Fatalf("terminal evidence changed: %+v", loaded)
	}
}

func TestDKVSWriteResultAtomicallyUpdatesReplicaAndAcknowledgesOutbox(t *testing.T) {
	store := newDKVSReplicaStore(newMemoryKVDB())
	record := testDKVSReplicaRecord(t, 1, "confirmed")
	path, err := dkvsindexer.CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	namespace := "endpoint-a"
	root := chainhash.DoubleHashH([]byte("path-root"))
	condition := dkvsindexer.PathWritePrecondition{
		Path: path, ExpectedRoot: chainhash.Hash{}, ExpectedGeneration: 0,
	}
	entry, err := newDKVSBatchOutboxEntry(namespace, []dkvsindexer.CASMutation{{
		Record:       record,
		Precondition: dkvsindexer.WritePrecondition{ExpectAbsent: true},
	}}, []dkvsindexer.PathWritePrecondition{condition}, "", dkvsOutboxOrigin{Key: record.Key})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.queueBatchOutbox(entry); err != nil {
		t.Fatal(err)
	}
	result := &dkvsindexer.WriteResult{
		Applied: 1,
		Records: []*swire.DKVSRecord{record},
		Hashes:  []string{dkvsindexer.RecordHash(record).String()},
		PathMeta: map[string]*dkvsindexer.PathMeta{path: {
			Version: 1, Path: path, Generation: 1, StateRoot: root,
			ActiveRecords: 1, ActiveTotalSize: uint64(dkvsindexer.RecordSize(record)),
		}},
		ServerTimeMS: uint64(time.Now().UnixMilli()),
	}
	if err := store.applyWriteResultAndAck(entry, result); err != nil {
		t.Fatal(err)
	}
	entries, err := store.loadBatchOutbox(namespace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("confirmed outbox was not acknowledged: %#v", entries)
	}
	scope := dkvsReplicaScope(namespace, []dkvsindexer.Subscription{{
		Type: dkvsindexer.SubscriptionPrefix, Target: path,
	}})
	confirmed, err := store.loadConfirmed(scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(confirmed) != 1 || dkvsindexer.RecordHash(confirmed[0]) != dkvsindexer.RecordHash(record) {
		t.Fatalf("confirmed replica mismatch: %#v", confirmed)
	}
	baseline, err := store.loadBaseline(scope)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Generation != 1 || baseline.ActiveRoot != root {
		t.Fatalf("baseline mismatch: %#v", baseline)
	}
	state, err := store.loadPathState(scope)
	if err != nil {
		t.Fatal(err)
	}
	if state.SessionState != dkvsSessionConfirmed || state.PathMeta == nil ||
		state.PathMeta.Generation != 1 {
		t.Fatalf("path state mismatch: %#v", state)
	}
}

func TestDKVSInvalidOutboxFailsBeforeLaterEntry(t *testing.T) {
	database := newMemoryKVDB()
	owner := &Manager{db: database}
	manager := newDKVSManager(owner)
	owner.dkvs = manager
	remote := newRGB11MemoryDKVSHTTP()
	http := &invalidFirstDKVSBatchHTTP{remote: remote}
	client := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", http)
	client.manager = manager
	client.replicaNamespace = "outbox-test"
	store := newDKVSReplicaStore(database)

	makeEntry := func(seq uint64, value string, created uint64) *dkvsBatchOutboxEntry {
		record := testDKVSReplicaRecord(t, seq, value)
		path, err := dkvsindexer.CollectionPathForKey(record.Key)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := newDKVSBatchOutboxEntry(client.replicaNamespace,
			[]dkvsindexer.CASMutation{{
				Record:       record,
				Precondition: dkvsindexer.WritePrecondition{ExpectAbsent: true},
			}}, []dkvsindexer.PathWritePrecondition{{
				Path: path, ExpectedRoot: chainhash.Hash{}, ExpectedGeneration: 0,
			}}, "", dkvsOutboxOrigin{Key: record.Key})
		if err != nil {
			t.Fatal(err)
		}
		entry.CreatedAtMS = created
		entry.UpdatedAtMS = created
		if err := store.queueBatchOutbox(entry); err != nil {
			t.Fatal(err)
		}
		return entry
	}
	first := makeEntry(1, "invalid", 1)
	second := makeEntry(1, "valid", 2)

	submitted, err := owner.flushDKVSBatchOutbox(client, store)
	if !errors.Is(err, dkvsindexer.ErrInvalidRecord) {
		t.Fatalf("permanent invalid outbox error=%v", err)
	}
	if submitted {
		t.Fatal("sync advanced past a permanently invalid outbox entry")
	}
	entries, err := store.loadBatchOutbox(client.replicaNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Key != first.Key ||
		entries[0].State != dkvsSessionTerminal || entries[1].Key != second.Key ||
		entries[1].State != dkvsSessionPrepared ||
		entries[0].LastErrorCode != string(dkvsindexer.ErrorCodeInvalidRecord) ||
		entries[0].LastError == "" {
		t.Fatalf("terminal evidence=%+v", entries)
	}
	pending, err := store.hasPendingBatchOutbox(client.replicaNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if !pending {
		t.Fatal("later unsubmitted entry was not retained as pending work")
	}
}

func TestDKVSOutboxErrorClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want dkvsOutboxErrorClass
	}{
		{name: "network", err: errors.New("temporary network failure"), want: dkvsOutboxTransient},
		{name: "cancelled", err: context.Canceled, want: dkvsOutboxTransient},
		{name: "conflict", err: dkvsindexer.ErrWriteConflict, want: dkvsOutboxConflict},
		{name: "sequence", err: dkvsindexer.ErrInvalidSequence, want: dkvsOutboxConflict},
		{name: "invalid", err: &DKVSError{Code: dkvsindexer.ErrorCodeInvalidRecord, Message: "bad signature or schema"}, want: dkvsOutboxPermanent},
		{name: "permission", err: dkvsindexer.ErrPermissionDenied, want: dkvsOutboxPermanent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyDKVSOutboxError(test.err); got != test.want {
				t.Fatalf("class=%d want=%d error=%v", got, test.want, test.err)
			}
		})
	}
}

func TestDKVSEndpointAffinityRequiresSyncedTakeover(t *testing.T) {
	owner := &Manager{db: newMemoryKVDB()}
	manager := newDKVSManager(owner)
	defer releaseDKVSManagerRuntime(manager)
	first := &SatsNetDKVSClient{replicaNamespace: "endpoint-a"}
	second := &SatsNetDKVSClient{replicaNamespace: "endpoint-b"}
	record := testDKVSReplicaRecord(t, 1, "value")

	if err := manager.ensureEndpointAffinity(first, []*swire.DKVSRecord{record}); err != nil {
		t.Fatalf("initial endpoint pin failed: %v", err)
	}
	if err := manager.ensureEndpointAffinity(second, []*swire.DKVSRecord{record}); !errors.Is(err, dkvsindexer.ErrStaleEndpoint) {
		t.Fatalf("unsynced endpoint takeover err=%v", err)
	}
	path, err := dkvsindexer.CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	manager.markReady(pathReplicaScope(second, path))
	if err := manager.ensureEndpointAffinity(second, []*swire.DKVSRecord{record}); err != nil {
		t.Fatalf("synced endpoint takeover failed: %v", err)
	}

	proof, err := dkvsindexer.NewFreeLocalFeeProof(record.Key, "personal",
		uint32(dkvsindexer.RecordSize(record)), 0)
	if err != nil {
		t.Fatal(err)
	}
	record.FeeProof, err = dkvsindexer.EncodeFeeProof(proof)
	if err != nil {
		t.Fatal(err)
	}
	manager.markReady(pathReplicaScope(first, path))
	if err := manager.ensureEndpointAffinity(first, []*swire.DKVSRecord{record}); !errors.Is(err, dkvsindexer.ErrLocalOnlyEndpointMismatch) {
		t.Fatalf("FREE_LOCAL endpoint switch err=%v", err)
	}
}
