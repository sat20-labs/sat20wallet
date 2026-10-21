package wallet

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	db "github.com/sat20-labs/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

var (
	errInjectedRGB11StageLockRead  = errors.New("injected RGB11 lock read failure")
	errInjectedRGB11StageLockFlush = errors.New("injected RGB11 lock flush failure")
)

type rgb11StageFaultDB struct {
	db.KVDB
	targetKey       string
	lockPrefix      string
	failLockReads   bool
	failNextFlush   bool
	targetReadCount int
	prefixReadCount int
}

func (p *rgb11StageFaultDB) Read(key []byte) ([]byte, error) {
	if string(key) == p.targetKey {
		p.targetReadCount++
		if p.failLockReads {
			return nil, errInjectedRGB11StageLockRead
		}
	}
	return p.KVDB.Read(key)
}

func (p *rgb11StageFaultDB) BatchRead(prefix []byte, reverse bool, r func(k, v []byte) error) error {
	if string(prefix) == p.lockPrefix {
		p.prefixReadCount++
		if p.failLockReads {
			return errInjectedRGB11StageLockRead
		}
	}
	return p.KVDB.BatchRead(prefix, reverse, r)
}

func (p *rgb11StageFaultDB) NewWriteBatch() db.WriteBatch {
	return &rgb11StageFaultBatch{WriteBatch: p.KVDB.NewWriteBatch(), db: p}
}

type rgb11StageFaultBatch struct {
	db.WriteBatch
	db *rgb11StageFaultDB
}

func (p *rgb11StageFaultBatch) Flush() error {
	if p.db.failNextFlush {
		p.db.failNextFlush = false
		return errInjectedRGB11StageLockFlush
	}
	return p.WriteBatch.Flush()
}

func TestStageRGB11LockReasonsPropagatesReadFailureWithoutReplacingCompleteLock(t *testing.T) {
	const (
		network  = "testnet4"
		outpoint = "existingcarrier:2"
	)
	base, faultDB, locker, want := newRGB11StageLockFixture(t, network, outpoint)
	if err := saveLastLockTime(base, network, 2026101102); err != nil {
		t.Fatal(err)
	}
	faultDB.targetReadCount = 0
	faultDB.prefixReadCount = 0
	faultDB.failLockReads = true

	batch := faultDB.NewWriteBatch()
	finish, stageErr := locker.stageRGB11LockReasons(batch, map[string]string{outpoint: "rgb"})
	if stageErr == nil {
		// Reproduce the caller's normal commit path so a swallowed read error
		// demonstrates the actual persisted and cached replacement.
		flushErr := batch.Flush()
		if flushErr != nil {
			if finish != nil {
				finish(false)
			}
			batch.Close()
			t.Fatalf("flush staged lock after swallowed read failure: %v", flushErr)
		}
		if finish != nil {
			finish(true)
		}
	} else if finish != nil {
		finish(false)
	}
	batch.Close()

	if !errors.Is(stageErr, errInjectedRGB11StageLockRead) {
		t.Errorf("stage error = %v, want injected lock read failure", stageErr)
	}
	if faultDB.targetReadCount+faultDB.prefixReadCount == 0 {
		t.Error("stage did not attempt either the target lock read or lock-prefix read")
	}
	assertRGB11StageLockUnchanged(t, base, locker, network, outpoint, want)
}

func TestStageRGB11LockReasonsFlushFailurePreservesCompleteLock(t *testing.T) {
	const (
		network  = "testnet4"
		outpoint = "existingcarrier:3"
	)
	base, faultDB, locker, want := newRGB11StageLockFixture(t, network, outpoint)
	// This control needs the stage path to write the complete lock record;
	// active reservation records are intentionally left unchanged by the helper.
	want.ReservationID = ""
	if err := saveLockedUtxo(base, network, outpoint, want); err != nil {
		t.Fatalf("seed unreserved complete lock: %v", err)
	}
	locker.mutex.Lock()
	locker.lockmap[outpoint] = cloneLockedUtxo(want)
	locker.mutex.Unlock()
	if err := saveLastLockTime(base, network, 2026101103); err != nil {
		t.Fatal(err)
	}

	// Keep a successful targeted Read control for implementations that resolve
	// one lock record directly instead of scanning the lock prefix.
	encoded, err := faultDB.Read([]byte(GetLockedUtxoKey(network, outpoint)))
	if err != nil {
		t.Fatalf("normal target lock read: %v", err)
	}
	var readBack LockedUtxo
	if err := DecodeFromBytes(encoded, &readBack); err != nil {
		t.Fatalf("decode normal target lock read: %v", err)
	}
	if !reflect.DeepEqual(&readBack, want) {
		t.Fatalf("normal target read changed lock fields: got %#v want %#v", &readBack, want)
	}

	batch := faultDB.NewWriteBatch()
	finish, err := locker.stageRGB11LockReasons(batch, map[string]string{outpoint: "rgb"})
	if err != nil {
		batch.Close()
		t.Fatalf("stage lock before injected flush failure: %v", err)
	}
	if finish == nil {
		batch.Close()
		t.Fatal("stage returned no completion callback for the lock update")
	}
	faultDB.failNextFlush = true
	flushErr := batch.Flush()
	finish(flushErr == nil)
	batch.Close()
	if !errors.Is(flushErr, errInjectedRGB11StageLockFlush) {
		t.Fatalf("flush error = %v, want injected batch flush failure", flushErr)
	}
	assertRGB11StageLockUnchanged(t, base, locker, network, outpoint, want)
}

func TestStageRGB11LockReasonsFlushFailureDoesNotMutateStaleCache(t *testing.T) {
	const (
		network  = "testnet4"
		outpoint = "newerrecord:4"
		marker   = int64(2026101104)
	)
	base := newMemoryKVDB()
	stale := &LockedUtxo{
		LockedTime:                1728633600,
		Reason:                    "cached-reason",
		ReservationPreviousReason: "cached-previous",
		Value:                     123456,
		Assets:                    swire.TxAssets{{BindingSat: 7}},
		Owner: &UtxoLockOwner{
			Origin:            "dapp",
			Network:           network,
			WalletFingerprint: "cached-fingerprint",
			AccountIndex:      4,
		},
	}
	if err := saveLockedUtxo(base, network, outpoint, stale); err != nil {
		t.Fatalf("seed cached lock: %v", err)
	}
	if err := saveLastLockTime(base, network, marker); err != nil {
		t.Fatalf("seed refresh marker: %v", err)
	}
	faultDB := &rgb11StageFaultDB{
		KVDB:       base,
		targetKey:  GetLockedUtxoKey(network, outpoint),
		lockPrefix: GetDBKeyPrefix() + DB_KEY_LOCKEDUTXO + network,
	}
	locker := NewUtxoLocker(faultDB, nil, network)
	locker.Init()

	newer := cloneLockedUtxo(stale)
	newer.LockedTime = 1728720000
	newer.Reason = "database-newer-reason"
	newer.ReservationPreviousReason = "database-newer-previous"
	newer.Value = 654321
	newer.Assets = swire.TxAssets{{BindingSat: 11}}
	newer.Owner.WalletFingerprint = "database-newer-fingerprint"
	if err := saveLockedUtxo(base, network, outpoint, newer); err != nil {
		t.Fatalf("write newer persisted lock without changing marker: %v", err)
	}
	markerBefore, err := base.Read([]byte(GeLastLockTimeKey(network)))
	if err != nil {
		t.Fatalf("read unchanged marker before stage: %v", err)
	}
	if locker.refreshTime != marker {
		t.Fatalf("cached refresh marker = %d, want unchanged marker %d", locker.refreshTime, marker)
	}

	batch := faultDB.NewWriteBatch()
	finish, err := locker.stageRGB11LockReasons(batch, map[string]string{outpoint: "rgb"})
	if err != nil {
		batch.Close()
		t.Fatalf("stage lock from newer target record: %v", err)
	}
	if finish == nil {
		batch.Close()
		t.Fatal("stage returned no completion callback for the lock update")
	}
	faultDB.failNextFlush = true
	flushErr := batch.Flush()
	finish(flushErr == nil)
	batch.Close()
	if !errors.Is(flushErr, errInjectedRGB11StageLockFlush) {
		t.Fatalf("flush error = %v, want injected batch flush failure", flushErr)
	}

	if !locker.mutex.TryLock() {
		t.Fatal("UtxoLocker mutex remains held after failed flush")
	}
	cached := cloneLockedUtxo(locker.lockmap[outpoint])
	cachedMarker := locker.refreshTime
	locker.mutex.Unlock()
	if !reflect.DeepEqual(cached, stale) {
		t.Errorf("failed stage mutated stale cached lock: got %#v want %#v", cached, stale)
	}
	if cachedMarker != marker {
		t.Errorf("failed stage changed cached marker: got %d want %d", cachedMarker, marker)
	}

	encoded, err := base.Read([]byte(GetLockedUtxoKey(network, outpoint)))
	if err != nil {
		t.Fatalf("read persisted target after failed flush: %v", err)
	}
	var persisted LockedUtxo
	if err := DecodeFromBytes(encoded, &persisted); err != nil {
		t.Fatalf("decode persisted target after failed flush: %v", err)
	}
	if !reflect.DeepEqual(&persisted, newer) {
		t.Errorf("failed stage changed newer persisted lock: got %#v want %#v", &persisted, newer)
	}
	markerAfter, err := base.Read([]byte(GeLastLockTimeKey(network)))
	if err != nil {
		t.Fatalf("read refresh marker after failed flush: %v", err)
	}
	if !bytes.Equal(markerAfter, markerBefore) {
		t.Errorf("failed stage changed persisted refresh marker: before %x after %x", markerBefore, markerAfter)
	}
}

func newRGB11StageLockFixture(t *testing.T, network, outpoint string) (*memoryKVDB, *rgb11StageFaultDB, *UtxoLocker, *LockedUtxo) {
	t.Helper()
	base := newMemoryKVDB()
	want := &LockedUtxo{
		LockedTime:                1728633600,
		Reason:                    "rgb11-carrier",
		ReservationID:             "reservation-keep",
		ReservationPreviousReason: "previous-keep",
		Value:                     123456,
		Assets:                    swire.TxAssets{{BindingSat: 7}},
		Owner: &UtxoLockOwner{
			Origin:            "dapp",
			Network:           network,
			WalletFingerprint: "wallet-fingerprint",
			AccountIndex:      4,
		},
	}
	if err := saveLockedUtxo(base, network, outpoint, want); err != nil {
		t.Fatalf("seed complete lock: %v", err)
	}
	if err := saveLastLockTime(base, network, 2026101101); err != nil {
		t.Fatalf("seed refresh marker: %v", err)
	}
	faultDB := &rgb11StageFaultDB{
		KVDB:       base,
		targetKey:  GetLockedUtxoKey(network, outpoint),
		lockPrefix: GetDBKeyPrefix() + DB_KEY_LOCKEDUTXO + network,
	}
	locker := NewUtxoLocker(faultDB, nil, network)
	locker.Init()
	assertRGB11StageLockUnchanged(t, base, locker, network, outpoint, want)
	return base, faultDB, locker, cloneLockedUtxo(want)
}

func assertRGB11StageLockUnchanged(t *testing.T, base *memoryKVDB, locker *UtxoLocker, network, outpoint string, want *LockedUtxo) {
	t.Helper()
	if !locker.mutex.TryLock() {
		t.Errorf("UtxoLocker mutex remains held after stage completion")
	} else {
		got := cloneLockedUtxo(locker.lockmap[outpoint])
		locker.mutex.Unlock()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("cached lock changed: got %#v want %#v", got, want)
		}
	}

	encoded, err := base.Read([]byte(GetLockedUtxoKey(network, outpoint)))
	if err != nil {
		t.Errorf("read persisted lock: %v", err)
		return
	}
	var got LockedUtxo
	if err := DecodeFromBytes(encoded, &got); err != nil {
		t.Errorf("decode persisted lock: %v", err)
		return
	}
	if !reflect.DeepEqual(&got, want) {
		t.Errorf("persisted lock changed: got %#v want %#v", &got, want)
	}
}
