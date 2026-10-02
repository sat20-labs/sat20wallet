package wallet

import (
	"bytes"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	indexer "github.com/sat20-labs/indexer/common"
)

// The hook runs only after the backing DB has completed its read transaction.
// It never manufactures a half-written record or holds the DB's own read lock.
type reviewReservationReadGateDB struct {
	indexer.KVDB
	armed atomic.Bool
	scanned chan struct{}
	release chan struct{}
	batchPrepared chan struct{}
	preparedOnce sync.Once
}

func (db *reviewReservationReadGateDB) BatchRead(prefix []byte, reverse bool, visit func([]byte, []byte) error) error {
	err := db.KVDB.BatchRead(prefix, reverse, visit)
	wanted := []byte(GetDBKeyPrefix() + DB_KEY_RESV + RESV_TYPE_RGB11 + "-")
	if err == nil && bytes.Equal(prefix, wanted) && db.armed.CompareAndSwap(true, false) {
		close(db.scanned)
		<-db.release
	}
	return err
}

func (db *reviewReservationReadGateDB) NewWriteBatch() indexer.WriteBatch {
	return &reviewReservationPreparedBatch{WriteBatch: db.KVDB.NewWriteBatch(), owner: db}
}

type reviewReservationPreparedBatch struct {
	indexer.WriteBatch
	owner *reviewReservationReadGateDB
}

func (batch *reviewReservationPreparedBatch) Put(key, value []byte) error {
	err := batch.WriteBatch.Put(key, value)
	if err == nil && bytes.Contains(key, []byte("receive/")) {
		batch.owner.preparedOnce.Do(func() { close(batch.owner.batchPrepared) })
	}
	return err
}

func TestReviewRGB11ReadDoesNotMixAtomicInvoiceGenerations(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	wallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params)
	manager := newRGB11FlowManager(t, wallet, &rgb11FlowIndexer{}, &rgb11FlowEvidence{}, 991)
	gate := &reviewReservationReadGateDB{
		KVDB: manager.db, scanned: make(chan struct{}), release: make(chan struct{}),
		batchPrepared: make(chan struct{}),
	}
	manager.db = gate
	replacement, err := newRGB11Manager(manager, gate, manager.utxoLockerL1, &rgb11FlowEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	manager.rgbManager = replacement
	if err := replacement.selectRGB11Scope(); err != nil {
		t.Fatal(err)
	}
	gate.armed.Store(true)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate.release) }) }
	defer release()

	readDone := make(chan error, 1)
	go func() {
		_, err := manager.GetRGB11State()
		readDone <- err
	}()
	select {
	case <-gate.scanned:
	case err := <-readDone:
		t.Fatalf("reader did not reach reservation snapshot boundary: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("reader did not reach reservation snapshot boundary")
	}

	writeDone := make(chan error, 1)
	go func() {
		_, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
			Mode: "witness", TransportMode: "out-of-band", AmountRaw: "2",
			Expiry: time.Now().Add(time.Hour).Unix(),
		})
		writeDone <- err
	}()
	select {
	case <-gate.batchPrepared:
	case err := <-writeDone:
		t.Fatalf("invoice did not stage its atomic request/reservation batch: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("invoice did not stage its atomic request/reservation batch")
	}
	// The original code commits now; a coherent-read implementation may defer
	// only the short local commit until this reader releases its snapshot.
	var writeErr error
	writerFinished := false
	select {
	case writeErr = <-writeDone:
		writerFinished = true
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case err := <-readDone:
		if err != nil {
			t.Errorf("valid atomic invoice commit was reported as inconsistent: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reader did not finish after snapshot release")
	}
	if !writerFinished {
		select {
		case writeErr = <-writeDone:
		case <-time.After(5 * time.Second):
			t.Fatal("local invoice commit remained blocked after read completed")
		}
	}
	if writeErr != nil {
		t.Fatalf("invoice creation failed: %v", writeErr)
	}
	state, err := manager.GetRGB11State()
	if err != nil || state == nil || len(state.Reservations) != 1 {
		t.Fatalf("final persisted invoice/reservation pair is invalid: state=%+v err=%v", state, err)
	}
}
