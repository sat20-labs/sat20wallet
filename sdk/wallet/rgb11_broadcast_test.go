package wallet

import (
	"context"
	"errors"
	"sync"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type controlledRGB11BroadcastEvidence struct {
	*expiredCancelEvidence
	mu        sync.Mutex
	txID      string
	err       error
	broadcast int
}

func (e *controlledRGB11BroadcastEvidence) Broadcast([]byte) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.broadcast++
	return e.txID, e.err
}

func (e *controlledRGB11BroadcastEvidence) calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.broadcast
}

type failNthFlushDB struct {
	indexer.KVDB
	mu     sync.Mutex
	count  int
	failAt int
}

type failNthFlushBatch struct {
	indexer.WriteBatch
	owner *failNthFlushDB
}

func (db *failNthFlushDB) NewWriteBatch() indexer.WriteBatch {
	return &failNthFlushBatch{WriteBatch: db.KVDB.NewWriteBatch(), owner: db}
}

func (batch *failNthFlushBatch) Flush() error {
	batch.owner.mu.Lock()
	batch.owner.count++
	fail := batch.owner.count == batch.owner.failAt
	batch.owner.mu.Unlock()
	if fail {
		return errors.New("injected RGB11 post-broadcast persistence failure")
	}
	return batch.WriteBatch.Flush()
}

func setRGB11BroadcastTestStore(t testing.TB, manager *Manager, db indexer.KVDB) *rgb11wallet.ProjectionStore {
	t.Helper()
	store := rgb11wallet.NewProjectionStore(db, manager.utxoLockerL1)
	if err := store.SetScope(rgb11StorageScope(manager.status.CurrentWallet, manager.status.CurrentAccount)); err != nil {
		t.Fatal(err)
	}
	manager.rgbManager.projectionStore = store
	return store
}

func TestRGB11BroadcastUnknownPersistsIrreversibleIntent(t *testing.T) {
	fixture := newExpiredCancelFixture(t, 1)
	evidence := &controlledRGB11BroadcastEvidence{
		expiredCancelEvidence: fixture.evidence,
		err:                   context.DeadlineExceeded,
	}
	runtime := fixture.manager.rgbManager
	runtime.evidence = evidence
	runtime.scopeStates = nil // keep the test deterministic; reconciliation is inspected explicitly below.
	pending, err := runtime.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
	if err != nil {
		t.Fatal(err)
	}

	txID, err := runtime.broadcastRGB11PendingBatch([]*rgb11wallet.PendingTransfer{pending}, nil)
	if !errors.Is(err, ErrRGB11BroadcastResultUnknown) || txID != fixture.witnessTxID {
		t.Fatalf("unexpected ambiguous broadcast result: txid=%s err=%v", txID, err)
	}
	stored, err := runtime.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if stored.State.Status != rgb11StatusBroadcastAttempted ||
		!rgb11TransferKeepsInputsLocked(&stored.State) {
		t.Fatalf("ambiguous broadcast was not retained fail-closed: %+v", stored.State)
	}
	needsReconciliation, err := runtime.hasPendingRGB11ChainReconciliation()
	if err != nil || !needsReconciliation {
		t.Fatalf("broadcast intent was invisible to reconciliation: pending=%v err=%v", needsReconciliation, err)
	}
	if cancelErr := fixture.manager.CancelExpiredRGB11Transfer(fixture.transferIDs[0]); cancelErr == nil {
		t.Fatal("ambiguous broadcast intent remained cancellable")
	}
}

func TestRGB11BroadcastPersistenceFailureRecoversFromBitcoinEvidence(t *testing.T) {
	fixture := newExpiredCancelFixture(t, 1)
	evidence := &controlledRGB11BroadcastEvidence{
		expiredCancelEvidence: fixture.evidence,
		txID:                  fixture.witnessTxID,
	}
	runtime := fixture.manager.rgbManager
	runtime.evidence = evidence
	runtime.scopeStates = nil
	failingDB := &failNthFlushDB{KVDB: fixture.manager.db, failAt: 2}
	store := setRGB11BroadcastTestStore(t, fixture.manager, failingDB)
	pending, err := store.LoadPendingTransfer(fixture.transferIDs[0])
	if err != nil {
		t.Fatal(err)
	}

	txID, err := runtime.broadcastRGB11PendingBatch([]*rgb11wallet.PendingTransfer{pending}, nil)
	if !errors.Is(err, ErrRGB11BroadcastPersistence) || txID != fixture.witnessTxID {
		t.Fatalf("unexpected persistence result: txid=%s err=%v", txID, err)
	}
	if evidence.calls() != 1 {
		t.Fatalf("broadcast calls=%d, want 1", evidence.calls())
	}

	// Re-open the durable projection view as a restarted process would. The
	// pre-broadcast intent must have survived even though the final state write
	// failed after the backend accepted the transaction.
	stableStore := setRGB11BroadcastTestStore(t, fixture.manager, fixture.manager.db)
	stored, err := stableStore.LoadPendingTransfer(fixture.transferIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if stored.State.Status != rgb11StatusBroadcastAttempted {
		t.Fatalf("durable intent status=%s, want %s", stored.State.Status, rgb11StatusBroadcastAttempted)
	}
	fixture.evidence.mu.Lock()
	fixture.evidence.status = &rgb11wallet.BitcoinTxStatus{
		TxID: fixture.witnessTxID, InMempool: true,
	}
	fixture.evidence.mu.Unlock()
	runtime.scopeStates = newRGB11ScopeStateRegistry()
	result, refreshErr := runtime.RefreshRGB11State(context.Background())
	if refreshErr != nil {
		t.Fatalf("reconcile durable broadcast intent: result=%+v err=%v", result, refreshErr)
	}
	recovered, err := stableStore.LoadPendingTransfer(fixture.transferIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State.Status != "pending" {
		t.Fatalf("recovered status=%s, want pending", recovered.State.Status)
	}
}

func TestRGB11AddressBroadcastUsesDurableIntent(t *testing.T) {
	fixture := newExpiredCancelFixture(t, 1)
	evidence := &controlledRGB11BroadcastEvidence{
		expiredCancelEvidence: fixture.evidence,
		err:                   context.DeadlineExceeded,
	}
	runtime := fixture.manager.rgbManager
	runtime.evidence = evidence
	runtime.scopeStates = nil
	pending, err := runtime.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	pending.State.Status = "delivered"
	pending.State.DeliveryRecordHash = "durable-record-hash"
	pending.State.DeliveryRecordKey = "/mail/test/msg"
	if err := runtime.projectionStore.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}
	txID, err := runtime.BroadcastRGB11AddressTransfer(fixture.transferIDs[0])
	if !errors.Is(err, ErrRGB11BroadcastResultUnknown) || txID != fixture.witnessTxID {
		t.Fatalf("address broadcast did not preserve ambiguous txid: txid=%s err=%v", txID, err)
	}
	stored, err := runtime.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if stored.State.Status != rgb11StatusBroadcastAttempted {
		t.Fatalf("address broadcast status=%s", stored.State.Status)
	}
}
