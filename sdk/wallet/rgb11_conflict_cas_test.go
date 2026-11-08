package wallet

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type rgb11ConflictArmCASDB struct {
	indexer.KVDB
	fixture *rgb11ConflictFixture
	arm     func()
	armed   bool
}

func (db *rgb11ConflictArmCASDB) Write(key, value []byte) error {
	if !db.armed && bytes.Equal(key, accountManagementProfileKey()) {
		p, err := db.fixture.sender.rgbManager.projectionStore.LoadPendingTransfer(db.fixture.pending.State.TransferID)
		if err == nil && p.State.Status == "conflicted" {
			db.armed = true
			db.arm()
		}
	}
	return db.KVDB.Write(key, value)
}
func TestRGB11ConflictCandidateCASRejectionRequiresReconciliation(t *testing.T) {
	f := newRGB11ConflictFixture(t, false)
	remote, ok := f.sender.http.(*rgb11MemoryDKVSHTTP)
	if !ok {
		t.Fatalf("unexpected existing fixture transport %T", f.sender.http)
	}
	// Save the genuine complete remote account state before changing only the
	// test transport's quota at the first post-commit profile marker.
	prior := snapshotRGB11MemoryRecords(remote)
	f.publish(t, true)
	db := &rgb11ConflictArmCASDB{KVDB: f.sender.db, fixture: f, arm: func() {
		prior = snapshotRGB11MemoryRecords(remote)
		remote.mu.Lock()
		remote.maxRecords = 1
		remote.mu.Unlock()
	}}
	f.sender.db = db
	defer func() { f.sender.db = db.KVDB; remote.mu.Lock(); remote.maxRecords = 0; remote.mu.Unlock() }()
	result, err := f.sender.RefreshRGB11State(context.Background())
	if !db.armed || !errors.Is(err, ErrRGB11Inconsistent) || result == nil || len(result.Inconsistent) != 1 {
		t.Fatalf("lost local committed result: result=%+v err=%v", result, err)
	}
	if f.journal(t).State.Status != "conflicted" {
		t.Fatal("remote CAS error rolled back local safe state")
	}
	if verifyErr := f.sender.verifyCurrentRGB11RecoveryClosure(); verifyErr == nil {
		t.Fatal("unconfirmed remote closure reported ready")
	}
	if err := f.sender.SyncAccountManagementState(context.Background()); err == nil {
		t.Fatal("explicit backup did not exercise the injected CAS rejection")
	}
	store, err := f.sender.accountDKVSStore()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := newDKVSReplicaStore(f.sender.db).LoadOutbox(store.client.replicaNamespace)
	if err != nil || len(entries) != 1 || entries[0].State != DKVSOutboxConflict {
		t.Fatalf("rejected backup intent was not retained: %v %v", len(entries), err)
	}
	after := snapshotRGB11MemoryRecords(remote)
	remote.mu.Lock()
	remote.maxRecords = 0
	remote.mu.Unlock()
	if len(prior) == 0 {
		t.Fatal("quota rejection has no authoritative baseline to protect")
	}
	if !reflect.DeepEqual(prior, after) {
		t.Fatal("rejected CAS changed remote account data")
	}
	if err := f.sender.SyncAccountManagementState(context.Background()); !errors.Is(err, dkvsindexer.ErrFreeLocalQuotaExceeded) {
		t.Fatalf("unresolved rejected request was silently reauthorized: %v", err)
	}
	retained, err := newDKVSReplicaStore(f.sender.db).LoadOutbox(store.client.replicaNamespace)
	if err != nil || len(retained) != 1 || retained[0].RequestID != entries[0].RequestID ||
		!reflect.DeepEqual(retained[0].Mutations, entries[0].Mutations) ||
		!reflect.DeepEqual(retained[0].Authorization, entries[0].Authorization) {
		t.Fatalf("retry replaced or discarded the original conflicting intent: %v", err)
	}
	if err := f.sender.verifyCurrentRGB11RecoveryClosure(); err == nil {
		t.Fatal("unresolved rejected backup was reported synchronized")
	}
	if f.journal(t).State.Status != "conflicted" || f.sender.GetRGB11ConsistencyStatus() != "broken" {
		t.Fatal("retry lost safe quarantine")
	}
}

type rgb11ConflictArmCASBatch struct {
	indexer.WriteBatch
	owner *rgb11ConflictArmCASDB
}

func (db *rgb11ConflictArmCASDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return &rgb11ConflictArmCASBatch{WriteBatch: db.KVDB.(indexer.AtomicWriteBatchDB).NewAtomicWriteBatch(), owner: db}
}
func (batch *rgb11ConflictArmCASBatch) Flush() error {
	if err := batch.WriteBatch.Flush(); err != nil {
		return err
	}
	db := batch.owner
	if !db.armed {
		pending, err := db.fixture.sender.rgbManager.projectionStore.LoadPendingTransfer(db.fixture.pending.State.TransferID)
		if err == nil && pending.State.Status == "conflicted" {
			db.armed = true
			db.arm()
		}
	}
	return nil
}
