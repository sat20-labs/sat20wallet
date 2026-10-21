package wallet

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
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
func TestRGB11ConflictCandidateCASFailureAndRetry(t *testing.T) {
	f := newRGB11ConflictFixture(t, false)
	remote, ok := f.sender.http.(*rgb11MemoryDKVSHTTP)
	if !ok {
		t.Fatalf("unexpected existing fixture transport %T", f.sender.http)
	}
	// Save the genuine complete remote account state before changing only the
	// test transport's quota at the first post-commit profile marker.
	prior := map[string]string{}
	f.publish(t, true)
	db := &rgb11ConflictArmCASDB{KVDB: f.sender.db, fixture: f, arm: func() {
		remote.mu.Lock()
		for key, r := range remote.records {
			if len(key) >= 9 && key[:9] == "/account/" {
				prior[key] = string(r.Value)
			}
		}
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
	remote.mu.Lock()
	after := map[string]string{}
	for key, r := range remote.records {
		if len(key) >= 9 && key[:9] == "/account/" {
			after[key] = string(r.Value)
		}
	}
	remote.maxRecords = 0
	remote.mu.Unlock()
	if !reflect.DeepEqual(prior, after) {
		t.Fatal("rejected CAS changed remote account data")
	}
	if err := f.sender.syncRGB11RecoveryBarrier(context.Background()); err != nil {
		t.Fatalf("existing barrier retry: %v", err)
	}
	if err := f.sender.verifyCurrentRGB11RecoveryClosure(); err != nil {
		t.Fatalf("retry did not confirm exact closure: %v", err)
	}
	if f.journal(t).State.Status != "conflicted" || f.sender.GetRGB11ConsistencyStatus() != "broken" {
		t.Fatal("retry lost safe quarantine")
	}
}
