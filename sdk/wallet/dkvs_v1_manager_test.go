package wallet

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

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
		Record: record,
		Precondition: dkvsindexer.WritePrecondition{ExpectAbsent: true},
	}}
	conditions := []dkvsindexer.PathWritePrecondition{{
		Path: path, ExpectedRoot: root, ExpectedGeneration: 7,
	}}
	entry, err := newDKVSBatchOutboxEntry("endpoint-a", mutations, conditions, "node-a")
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
	if len(loaded) != 1 || loaded[0].ID != entry.ID || loaded[0].EndpointID != "node-a" {
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
		Record: record,
		Precondition: dkvsindexer.WritePrecondition{ExpectAbsent: true},
	}}, []dkvsindexer.PathWritePrecondition{condition}, "")
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
	if err := manager.ensureEndpointAffinity(second, []*swire.DKVSRecord{record});
		!errors.Is(err, dkvsindexer.ErrStaleEndpoint) {
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
	if err := manager.ensureEndpointAffinity(first, []*swire.DKVSRecord{record});
		!errors.Is(err, dkvsindexer.ErrLocalOnlyEndpointMismatch) {
		t.Fatalf("FREE_LOCAL endpoint switch err=%v", err)
	}
}
