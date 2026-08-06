package wallet

import (
	"errors"
	"strings"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func TestDKVSWatchChangeRevokesReadyUntilResync(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manager := newRGB11MultiDeviceManager(t, priv, 993)
	remote := newRGB11MemoryDKVSHTTP()
	configureRGB11DKVSTestManager(manager, remote)
	dkvs := manager.ensureDKVSManager()

	key, err := dkvsindexer.PersonalKey(manager.wallet.GetPubKey().SerializeCompressed(), "freshness/value")
	if err != nil {
		t.Fatal(err)
	}
	path, err := dkvsindexer.CollectionPathForKey(key)
	if err != nil {
		t.Fatal(err)
	}
	writeRemote := func(seq uint64, value string) {
		record, buildErr := NewDKVSSignedRecord(manager.wallet, key, []byte(value),
			dkvsindexer.RecordOptions{Seq: seq, IssueHeight: 1})
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		remote.mu.Lock()
		remote.records[key] = cloneRGB11DKVSRecord(record)
		remote.generations[path] = seq
		remote.mu.Unlock()
	}

	writeRemote(1, "first")
	dkvs.rememberPaths([]string{key})
	states, err := manager.syncDKVSOnce()
	if err != nil || len(states) != 1 {
		t.Fatalf("initial sync states=%+v err=%v", states, err)
	}
	if !dkvs.scopeReady(states[0].Scope) {
		t.Fatal("initial sync did not mark scope ready")
	}

	writeRemote(2, "second")
	if !dkvs.watch(states, make(chan struct{})) {
		t.Fatal("watch stopped unexpectedly")
	}
	if dkvs.scopeReady(states[0].Scope) {
		t.Fatal("changed remote path remained ready before resynchronization")
	}
	store, err := dkvs.primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if value.Seq != 2 || string(value.Value) != "second" {
		t.Fatalf("resynchronized value=%+v", value)
	}
	if !dkvs.scopeReady(states[0].Scope) {
		t.Fatal("successful resynchronization did not restore readiness")
	}
}

func TestSyncedPathWriteContextRejectsNonConfirmedSession(t *testing.T) {
	owner := &Manager{db: newMemoryKVDB()}
	manager := newDKVSManager(owner)
	owner.dkvs = manager
	client := &SatsNetDKVSClient{replicaNamespace: "freshness:test"}
	accountID := strings.Repeat("1", 64)
	key := "/personal/" + accountID + "/account/state"
	path, err := dkvsindexer.CollectionPathForKey(key)
	if err != nil {
		t.Fatal(err)
	}
	scope := pathReplicaScope(client, path)
	putState := func(session, code string) {
		batch := owner.db.NewWriteBatch()
		defer batch.Close()
		if err := putPathStateBatch(batch, scope, &dkvsPathReplicaState{
			Path: path,
			PathMeta: &dkvsindexer.PathMeta{
				Version: 3, Path: path, Generation: 1, ViewHeight: 1,
			},
			SessionState: session, LastErrorCode: code,
		}); err != nil {
			t.Fatal(err)
		}
		if err := batch.Flush(); err != nil {
			t.Fatal(err)
		}
	}

	putState(dkvsSessionConflict, string(dkvsindexer.ErrorCodeWriteConflict))
	manager.markReady(scope)
	if _, err := owner.syncedPathWriteContext(client, []string{key}); !errors.Is(err, ErrDKVSPathNotSynced) {
		t.Fatalf("conflict session write context err=%v", err)
	}

	putState(dkvsSessionIdle, "")
	manager.markReady(scope)
	context, err := owner.syncedPathWriteContext(client, []string{key})
	if err != nil || len(context.Conditions) != 1 {
		t.Fatalf("confirmed context=%+v err=%v", context, err)
	}
}
