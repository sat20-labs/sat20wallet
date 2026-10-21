package rgb11wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
)

type projectionReadAtomicGate struct {
	mu sync.RWMutex

	watchCommit     atomic.Bool
	commitAttempted chan struct{}
	attemptOnce     sync.Once
}

func newProjectionReadAtomicGate() *projectionReadAtomicGate {
	return &projectionReadAtomicGate{commitAttempted: make(chan struct{})}
}

func (g *projectionReadAtomicGate) LockReservationCommit() func() {
	if g.watchCommit.Load() {
		g.attemptOnce.Do(func() { close(g.commitAttempted) })
	}
	g.mu.Lock()
	return g.mu.Unlock
}

func (g *projectionReadAtomicGate) LockReservationRead() func() {
	g.mu.RLock()
	return g.mu.RUnlock
}

func (*projectionReadAtomicGate) StageReservation(indexer.WriteBatch, string, any) (func(), error) {
	return nil, nil
}

// projectionReadBarrierDB pauses Balance only after the backing DB has
// completed its output scan. The writer runs in its own goroutine, so the
// test never attempts to upgrade a reader lock from inside a DB callback.
type projectionReadBarrierDB struct {
	indexer.KVDB
	outputPrefix []byte

	armed        atomic.Bool
	watchWriter  atomic.Bool
	outputRead   chan struct{}
	resumeRead   chan struct{}
	batchReady   chan struct{}
	flushEntered chan struct{}
	readyOnce    sync.Once
	flushOnce    sync.Once
	resumeOnce   sync.Once
}

func (db *projectionReadBarrierDB) BatchRead(prefix []byte, reverse bool, visit func([]byte, []byte) error) error {
	if err := db.KVDB.BatchRead(prefix, reverse, visit); err != nil {
		return err
	}
	if bytes.Equal(prefix, db.outputPrefix) && db.armed.CompareAndSwap(true, false) {
		close(db.outputRead)
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-db.resumeRead:
			return nil
		case <-timer.C:
			return errors.New("timed out waiting to resume RGB11 output scan")
		}
	}
	return nil
}

func (db *projectionReadBarrierDB) NewWriteBatch() indexer.WriteBatch {
	batch := db.KVDB.NewWriteBatch()
	if batch == nil {
		return nil
	}
	return &projectionReadObservedBatch{WriteBatch: batch, owner: db}
}

type projectionReadObservedBatch struct {
	indexer.WriteBatch
	owner *projectionReadBarrierDB
}

func (b *projectionReadObservedBatch) Put(key, value []byte) error {
	err := b.WriteBatch.Put(key, value)
	if err == nil {
		b.owner.markBatchReady()
	}
	return err
}

func (b *projectionReadObservedBatch) Delete(key []byte) error {
	err := b.WriteBatch.Delete(key)
	if err == nil {
		b.owner.markBatchReady()
	}
	return err
}

func (b *projectionReadObservedBatch) Flush() error {
	if b.owner.watchWriter.Load() {
		b.owner.flushOnce.Do(func() { close(b.owner.flushEntered) })
	}
	return b.WriteBatch.Flush()
}

func (db *projectionReadBarrierDB) markBatchReady() {
	if db.watchWriter.Load() {
		db.readyOnce.Do(func() { close(db.batchReady) })
	}
}

func (db *projectionReadBarrierDB) resume() {
	db.resumeOnce.Do(func() { close(db.resumeRead) })
}

func TestBalanceDoesNotMixProjectionGenerationsAcrossAtomicCommit(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	defer database.Close()

	const scope = "balance-atomic-generation"
	gateDB := &projectionReadBarrierDB{
		KVDB: database, outputPrefix: []byte("rgb11-" + scope + "-output-"),
		outputRead: make(chan struct{}), resumeRead: make(chan struct{}), batchReady: make(chan struct{}),
		flushEntered: make(chan struct{}),
	}
	store := NewProjectionStore(gateDB, &recordingLocker{})
	if err := store.SetScope(scope); err != nil {
		t.Fatal(err)
	}
	commitGate := newProjectionReadAtomicGate()
	store.SetReservationPersistence(commitGate)

	const officialContract = "rgb:Ar4ouaLv-b7f7Dc_-z5EMvtu-FA5KNh1-nlae~jk-8xMBo7E"
	name, err := NewContractAssetKey(officialContract, indexer.ASSET_TYPE_FT)
	if err != nil {
		t.Fatal(err)
	}
	p := makeAtomicReadReplacement(t, store, officialContract, name, strings.Repeat("11", 32)+":0", "old-p", "60", []byte("old-P-consignment"), 1)
	if err := store.CommitProjection(p.Output, p.Asset, p.Proof); err != nil {
		t.Fatal(err)
	}
	oldBalance, err := store.Balance(name)
	if err != nil || oldBalance == nil || oldBalance.Cmp(&p.Asset.Amount) != 0 {
		t.Fatalf("seed projection should have balance 60: balance=%v err=%v", oldBalance, err)
	}
	q := makeAtomicReadReplacement(t, store, officialContract, name, strings.Repeat("22", 32)+":0", "new-q", "35", []byte("new-Q-consignment"), 2)

	gateDB.armed.Store(true)
	type balanceResult struct {
		amount *indexer.Decimal
		err    error
	}
	readDone := make(chan balanceResult, 1)
	go func() {
		amount, err := store.Balance(name)
		readDone <- balanceResult{amount: amount, err: err}
	}()
	defer gateDB.resume()
	select {
	case <-gateDB.outputRead:
	case result := <-readDone:
		t.Fatalf("Balance returned before the output-scan barrier: balance=%v err=%v", result.amount, result.err)
	case <-time.After(5 * time.Second):
		t.Fatal("Balance did not reach the output-scan barrier")
	}

	writeDone := make(chan error, 1)
	writerStarted := make(chan struct{})
	gateDB.watchWriter.Store(true)
	commitGate.watchCommit.Store(true)
	go func() {
		close(writerStarted)
		writeDone <- store.ReplaceProjections([]string{p.Output.OutPointStr}, []ProjectionReplacement{q})
	}()
	<-writerStarted

	// On the unfixed implementation the DB batch reaches Flush while Balance is
	// paused. With the shared gate, the writer signals its gate attempt and waits
	// until Balance has completed both scans. Release only after one of those
	// states is observed, so the baseline cannot pass due to scheduler timing.
	writerFinished := false
	writerFlushedBeforeRead := false
	gateAttemptObserved := false
	writerSignalTimedOut := false
	var writerErr error
	select {
	case writerErr = <-writeDone:
		writerFinished = true
	case <-commitGate.commitAttempted:
		gateAttemptObserved = true
	case <-gateDB.flushEntered:
		writerFlushedBeforeRead = true
		select {
		case writerErr = <-writeDone:
			writerFinished = true
		case <-time.After(5 * time.Second):
		}
	case <-gateDB.batchReady:
		select {
		case writerErr = <-writeDone:
			writerFinished = true
		case <-commitGate.commitAttempted:
			gateAttemptObserved = true
		case <-gateDB.flushEntered:
			writerFlushedBeforeRead = true
			select {
			case writerErr = <-writeDone:
				writerFinished = true
			case <-time.After(5 * time.Second):
			}
		case <-time.After(5 * time.Second):
			writerSignalTimedOut = true
		}
	case <-time.After(5 * time.Second):
		writerSignalTimedOut = true
	}
	gateDB.resume()

	var result balanceResult
	select {
	case result = <-readDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Balance did not finish after the output-scan barrier was released")
	}
	if result.err != nil {
		t.Fatalf("Balance failed while reading a committed projection generation: %v", result.err)
	}
	if result.amount == nil {
		t.Fatal("Balance observed a mixed generation and returned no amount; expected exactly 60 or 35")
	}
	oldAmount, _ := indexer.NewDecimalFromString("60", 0)
	newAmount, _ := indexer.NewDecimalFromString("35", 0)
	if result.amount.Cmp(oldAmount) != 0 && result.amount.Cmp(newAmount) != 0 {
		t.Fatalf("Balance mixed projection generations: got %s, want exactly 60 or 35", result.amount.String())
	}

	if !writerFinished {
		select {
		case writerErr = <-writeDone:
		case <-time.After(5 * time.Second):
			t.Fatal("projection commit remained blocked after Balance completed")
		}
	}
	if writerSignalTimedOut {
		t.Fatal("writer neither reached the atomic commit gate nor the backing DB flush")
	}
	if writerFlushedBeforeRead && gateAttemptObserved {
		t.Fatal("writer both flushed before the reader resumed and reported a blocked commit gate")
	}
	if writerErr != nil {
		t.Fatalf("atomic replacement commit failed: %v", writerErr)
	}
	newBalance, err := store.Balance(name)
	if err != nil || newBalance == nil || newBalance.Cmp(newAmount) != 0 {
		t.Fatalf("committed replacement should have balance 35: balance=%v err=%v", newBalance, err)
	}
}

var errInjectedProjectionFlush = errors.New("injected RGB11 projection flush failure")

type projectionFailFlushDB struct {
	indexer.KVDB
	failNext atomic.Bool
}

func (db *projectionFailFlushDB) NewWriteBatch() indexer.WriteBatch {
	batch := db.KVDB.NewWriteBatch()
	if batch == nil {
		return nil
	}
	return &projectionFailFlushBatch{WriteBatch: batch, owner: db}
}

type projectionFailFlushBatch struct {
	indexer.WriteBatch
	owner *projectionFailFlushDB
}

func (batch *projectionFailFlushBatch) Flush() error {
	if batch.owner.failNext.CompareAndSwap(true, false) {
		return errInjectedProjectionFlush
	}
	return batch.WriteBatch.Flush()
}

func TestReplaceProjectionsFlushFailureReopensAtOldGeneration(t *testing.T) {
	databasePath := t.TempDir()
	baseDB := indexerdb.NewKVDB(databasePath)
	database := &projectionFailFlushDB{KVDB: baseDB}
	databaseClosed := false
	defer func() {
		if !databaseClosed {
			_ = database.Close()
		}
	}()

	const scope = "balance-flush-failure"
	store := NewProjectionStore(database, &recordingLocker{})
	if err := store.SetScope(scope); err != nil {
		t.Fatal(err)
	}
	store.SetReservationPersistence(newProjectionReadAtomicGate())
	const officialContract = "rgb:Ar4ouaLv-b7f7Dc_-z5EMvtu-FA5KNh1-nlae~jk-8xMBo7E"
	name, err := NewContractAssetKey(officialContract, indexer.ASSET_TYPE_FT)
	if err != nil {
		t.Fatal(err)
	}
	p := makeAtomicReadReplacement(t, store, officialContract, name, strings.Repeat("33", 32)+":0", "old-p", "60", []byte("flush-failure-old-P"), 3)
	if err := store.CommitProjection(p.Output, p.Asset, p.Proof); err != nil {
		t.Fatal(err)
	}
	q := makeAtomicReadReplacement(t, store, officialContract, name, strings.Repeat("44", 32)+":0", "new-q", "35", []byte("flush-failure-new-Q"), 4)

	database.failNext.Store(true)
	if err := store.ReplaceProjections([]string{p.Output.OutPointStr}, []ProjectionReplacement{q}); !errors.Is(err, errInjectedProjectionFlush) {
		t.Fatalf("replacement should report the injected batch flush failure, got %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	databaseClosed = true

	reopenedDB := indexerdb.NewKVDB(databasePath)
	defer reopenedDB.Close()
	reopened := NewProjectionStore(reopenedDB, nil)
	if err := reopened.SetScope(scope); err != nil {
		t.Fatal(err)
	}
	oldOutput, err := reopened.LoadOutput(p.Output.OutPointStr)
	if err != nil {
		t.Fatalf("failed flush lost old output after reopen: %v", err)
	}
	if amount := oldOutput.GetAsset(&name); amount == nil || amount.Cmp(&p.Asset.Amount) != 0 {
		t.Fatalf("failed flush changed the old projection after reopen: amount=%v", amount)
	}
	oldProof, err := reopened.LoadProof(p.Output.OutPointStr, name)
	if err != nil || oldProof.Status != "valid" {
		t.Fatalf("failed flush changed the old proof after reopen: proof=%+v err=%v", oldProof, err)
	}
	if _, err := reopened.LoadOutput(q.Output.OutPointStr); !errors.Is(err, indexer.ErrKeyNotFound) {
		t.Fatalf("failed flush persisted part of the replacement output after reopen: %v", err)
	}
	if _, err := reopened.LoadProof(q.Output.OutPointStr, name); !errors.Is(err, indexer.ErrKeyNotFound) {
		t.Fatalf("failed flush persisted part of the replacement proof after reopen: %v", err)
	}
	balance, err := reopened.Balance(name)
	if err != nil || balance == nil || balance.Cmp(&p.Asset.Amount) != 0 {
		t.Fatalf("failed flush should reopen at complete old balance 60: balance=%v err=%v", balance, err)
	}
}

type projectionStageCallbackPersistence struct {
	*projectionReadAtomicGate
	database       indexer.KVDB
	reservationKey []byte
	projectionKey  []byte
	events         []string
}

func (p *projectionStageCallbackPersistence) StageReservation(batch indexer.WriteBatch, _ string, _ any) (func(), error) {
	if err := batch.Put(p.reservationKey, []byte("reservation")); err != nil {
		return nil, err
	}
	return func() {
		if _, err := p.database.Read(p.reservationKey); err != nil {
			p.events = append(p.events, "reservation-missing")
			return
		}
		p.events = append(p.events, "reservation-published")
	}, nil
}

func TestReservationFlushStagePublishesOnlyAfterSuccessfulFlush(t *testing.T) {
	baseDB := indexerdb.NewKVDB(t.TempDir())
	database := &projectionFailFlushDB{KVDB: baseDB}
	defer database.Close()

	const scope = "flush-stage-callbacks"
	reservationKey := []byte("test-reservation-stage")
	projectionKey := []byte("test-projection-stage")
	newPersistence := func() *projectionStageCallbackPersistence {
		return &projectionStageCallbackPersistence{
			projectionReadAtomicGate: newProjectionReadAtomicGate(), database: baseDB,
			reservationKey: reservationKey, projectionKey: projectionKey,
		}
	}
	newBatch := func(persistence *projectionStageCallbackPersistence, transferID string) indexer.WriteBatch {
		batch := NewReservationWriteBatch(database, scope, persistence)
		pending := &PendingTransfer{State: TransferState{TransferID: transferID, Direction: "send", Status: "pending"}}
		encoded, err := encode(pending)
		if err != nil {
			t.Fatal(err)
		}
		if err := batch.Put([]byte("rgb11-"+scope+"-pending-"+transferID), encoded); err != nil {
			t.Fatal(err)
		}
		return batch
	}
	stage := func(persistence *projectionStageCallbackPersistence) ReservationCommitStage {
		return func(batch indexer.WriteBatch) (func(bool), error) {
			if err := batch.Put(persistence.projectionKey, []byte("projection")); err != nil {
				return nil, err
			}
			return func(committed bool) {
				if !committed {
					persistence.events = append(persistence.events, "stage-aborted")
					return
				}
				if _, err := persistence.database.Read(persistence.reservationKey); err != nil {
					persistence.events = append(persistence.events, "stage-missing-reservation")
					return
				}
				if _, err := persistence.database.Read(persistence.projectionKey); err != nil {
					persistence.events = append(persistence.events, "stage-missing-projection")
					return
				}
				persistence.events = append(persistence.events, "stage-published")
			}, nil
		}
	}
	failedPersistence := newPersistence()
	failedBatch := newBatch(failedPersistence, "failed-stage")
	failedCommitBatch, ok := failedBatch.(ReservationCommitBatch)
	if !ok {
		t.Fatal("reservation batch does not implement ReservationCommitBatch")
	}
	database.failNext.Store(true)
	err := failedCommitBatch.FlushWith(stage(failedPersistence))
	failedBatch.Close()
	if !errors.Is(err, errInjectedProjectionFlush) {
		t.Fatalf("FlushWith should report the injected DB failure, got %v", err)
	}
	if len(failedPersistence.events) != 1 || failedPersistence.events[0] != "stage-aborted" {
		t.Fatalf("failed batch published staged state or reservation callbacks: %v", failedPersistence.events)
	}
	for _, key := range [][]byte{[]byte("rgb11-" + scope + "-pending-failed-stage"), reservationKey, projectionKey} {
		if _, err := baseDB.Read(key); !errors.Is(err, indexer.ErrKeyNotFound) {
			t.Fatalf("failed flush persisted staged key %q: %v", key, err)
		}
	}

	successPersistence := newPersistence()
	successBatch := newBatch(successPersistence, "successful-stage")
	successCommitBatch, ok := successBatch.(ReservationCommitBatch)
	if !ok {
		t.Fatal("reservation batch does not implement ReservationCommitBatch")
	}
	err = successCommitBatch.FlushWith(stage(successPersistence))
	successBatch.Close()
	if err != nil {
		t.Fatalf("FlushWith success failed: %v", err)
	}
	wantEvents := []string{"stage-published", "reservation-published"}
	if len(successPersistence.events) != len(wantEvents) {
		t.Fatalf("staged callbacks were not published once after the flush: %v", successPersistence.events)
	}
	for i, want := range wantEvents {
		if successPersistence.events[i] != want {
			t.Fatalf("callback order %v, want %v", successPersistence.events, wantEvents)
		}
	}
}

func makeAtomicReadReplacement(t *testing.T, store *ProjectionStore, contract string, name indexer.AssetName,
	outpoint, operationID, amountText string, raw []byte, state byte) ProjectionReplacement {
	t.Helper()
	amount, err := indexer.NewDecimalFromString(amountText, 0)
	if err != nil {
		t.Fatal(err)
	}
	output := indexer.NewTxOutput(1_000)
	output.OutPointStr = outpoint
	asset := &indexer.AssetInfo{Name: name, Amount: *amount.Clone(), BindingSat: 0}
	digest := sha256.Sum256(raw)
	receipt, err := store.ValidateAndStoreConsignment(context.Background(), testValidator{receipt: &ValidationReceipt{
		Version: 1, EngineBuildID: "test-engine", ConsignmentHash: hex.EncodeToString(digest[:]),
		ContractID: contract, SchemaID: "schema", StateHash: [32]byte{state}, Status: "valid",
		Allocations: []ValidatedAllocation{{
			OutPoint: outpoint, AssetName: name, Amount: *amount.Clone(), OperationID: operationID,
			AssignmentType: 4000, StateClass: "fungible", SealDisclosure: []byte{state},
		}},
	}}, testEvidence{}, raw)
	if err != nil {
		t.Fatal(err)
	}
	receiptHash, err := receipt.Hash()
	if err != nil {
		t.Fatal(err)
	}
	proof := &AllocationProof{
		OutPoint: outpoint, AssetName: name, OperationID: operationID,
		AssignmentType: 4000, StateClass: "fungible", SealDisclosure: []byte{state},
		Status: "valid", ConsignmentHash: receipt.ConsignmentHash, ValidationHash: receiptHash,
	}
	return ProjectionReplacement{Output: output, Asset: asset, Proof: proof}
}
