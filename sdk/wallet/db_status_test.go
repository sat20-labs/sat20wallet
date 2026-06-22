package wallet

import (
	"bytes"
	"sort"
	"sync"
	"testing"

	db "github.com/sat20-labs/indexer/common"
)

type memoryKVDB struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newMemoryKVDB() *memoryKVDB {
	return &memoryKVDB{data: make(map[string][]byte)}
}

func (m *memoryKVDB) DropAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = make(map[string][]byte)
	return nil
}

func (m *memoryKVDB) DropPrefix(prefix []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key := range m.data {
		if bytes.HasPrefix([]byte(key), prefix) {
			delete(m.data, key)
		}
	}
	return nil
}

func (m *memoryKVDB) Read(key []byte) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.data[string(key)]
	if !ok {
		return nil, db.ErrKeyNotFound
	}
	return append([]byte(nil), value...), nil
}

func (m *memoryKVDB) Write(key, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[string(key)] = append([]byte(nil), value...)
	return nil
}

func (m *memoryKVDB) Delete(key []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, string(key))
	return nil
}

func (m *memoryKVDB) Close() error { return nil }

func (m *memoryKVDB) NewWriteBatch() db.WriteBatch {
	return &memoryWriteBatch{db: m}
}

func (m *memoryKVDB) BatchRead(prefix []byte, reverse bool, r func(k, v []byte) error) error {
	m.mu.RLock()
	keys := make([]string, 0, len(m.data))
	for key := range m.data {
		if bytes.HasPrefix([]byte(key), prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if reverse {
		for i, j := 0, len(keys)-1; i < j; i, j = i+1, j-1 {
			keys[i], keys[j] = keys[j], keys[i]
		}
	}
	values := make(map[string][]byte, len(keys))
	for _, key := range keys {
		values[key] = append([]byte(nil), m.data[key]...)
	}
	m.mu.RUnlock()

	for _, key := range keys {
		if err := r([]byte(key), values[key]); err != nil {
			return err
		}
	}
	return nil
}

func (m *memoryKVDB) BatchReadV2(prefix, seekKey []byte, reverse bool, r func(k, v []byte) error) error {
	return m.BatchRead(prefix, reverse, r)
}

func (m *memoryKVDB) View(fn func(db.ReadBatch) error) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return fn(memoryReadBatch{data: m.data})
}

type memoryReadBatch struct {
	data map[string][]byte
}

func (b memoryReadBatch) Get(key []byte) ([]byte, error) {
	value, ok := b.data[string(key)]
	if !ok {
		return nil, db.ErrKeyNotFound
	}
	return append([]byte(nil), value...), nil
}

func (b memoryReadBatch) GetRef(key []byte) ([]byte, error) {
	value, ok := b.data[string(key)]
	if !ok {
		return nil, db.ErrKeyNotFound
	}
	return value, nil
}

type memoryWriteBatch struct {
	db      *memoryKVDB
	puts    map[string][]byte
	deletes []string
}

func (b *memoryWriteBatch) Put(key, value []byte) error {
	if b.puts == nil {
		b.puts = make(map[string][]byte)
	}
	b.puts[string(key)] = append([]byte(nil), value...)
	return nil
}

func (b *memoryWriteBatch) Delete(key []byte) error {
	b.deletes = append(b.deletes, string(key))
	return nil
}

func (b *memoryWriteBatch) Flush() error {
	b.db.mu.Lock()
	defer b.db.mu.Unlock()
	for _, key := range b.deletes {
		delete(b.db.data, key)
	}
	for key, value := range b.puts {
		b.db.data[key] = append([]byte(nil), value...)
	}
	return nil
}

func (b *memoryWriteBatch) Close() {}

func TestLoadStatusMigratesLegacySTPStatus(t *testing.T) {
	kv := newMemoryKVDB()
	legacy := &Status{
		SyncHeight:              11,
		SyncHeightL1:            900001,
		SyncHeightL2:            3404,
		BlockHashMapL1:          map[int]string{900001: "l1"},
		BlockHashMapL2:          map[int]string{3404: "l2"},
		MaxFeeRateL1:            12,
		HasStaked:               true,
		ContractSubAccountIndex: 7,
	}
	buf, err := encodeStatusToBytes(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := kv.Write([]byte(legacySTPStatusDBKey()), buf); err != nil {
		t.Fatal(err)
	}

	status := loadStatusWithLegacyMigration(kv)
	assertMigratedSTPStatus(t, status, legacy)
	if _, err := kv.Read([]byte(DB_KEY_STATUS)); err != nil {
		t.Fatalf("new status key not written: %v", err)
	}
	if _, err := kv.Read([]byte(legacySTPStatusDBKey())); err == nil {
		t.Fatal("legacy status key was not deleted")
	}
}

func TestLoadStatusMergesLegacySTPStatusIntoWalletStatus(t *testing.T) {
	kv := newMemoryKVDB()
	current := newDefaultStatus()
	current.CurrentWallet = 42
	current.CurrentAccount = 3
	current.SyncHeightL1 = 0
	current.SyncHeightL2 = 0
	current.BlockHashMapL1 = nil
	current.BlockHashMapL2 = nil
	currentBuf, err := encodeStatusToBytes(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := kv.Write([]byte(DB_KEY_STATUS), currentBuf); err != nil {
		t.Fatal(err)
	}

	legacy := &Status{
		SyncHeight:              15,
		SyncHeightL1:            900123,
		SyncHeightL2:            3500,
		BlockHashMapL1:          map[int]string{900123: "legacy-l1"},
		BlockHashMapL2:          map[int]string{3500: "legacy-l2"},
		MaxFeeRateL1:            21,
		HasStaked:               true,
		ContractSubAccountIndex: 9,
	}
	legacyBuf, err := encodeStatusToBytes(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := kv.Write([]byte(legacySTPStatusDBKey()), legacyBuf); err != nil {
		t.Fatal(err)
	}

	status := loadStatusWithLegacyMigration(kv)
	if status.CurrentWallet != current.CurrentWallet || status.CurrentAccount != current.CurrentAccount {
		t.Fatalf("wallet status not preserved: got wallet=%d account=%d",
			status.CurrentWallet, status.CurrentAccount)
	}
	assertMigratedSTPStatus(t, status, legacy)
	if _, err := kv.Read([]byte(legacySTPStatusDBKey())); err == nil {
		t.Fatal("legacy status key was not deleted")
	}
}

func TestLoadStatusMigratesLegacySyncHeightAsL1Height(t *testing.T) {
	kv := newMemoryKVDB()
	legacy := &Status{
		SyncHeight:     900321,
		SyncHeightL2:   3600,
		BlockHashMapL1: map[int]string{900321: "legacy-l1"},
		BlockHashMapL2: map[int]string{3600: "legacy-l2"},
	}
	buf, err := encodeStatusToBytes(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := kv.Write([]byte(legacySTPStatusDBKey()), buf); err != nil {
		t.Fatal(err)
	}

	status := loadStatusWithLegacyMigration(kv)
	if status.SyncHeightL1 != legacy.SyncHeight {
		t.Fatalf("legacy SyncHeight not migrated as L1 height: got %d want %d",
			status.SyncHeightL1, legacy.SyncHeight)
	}
	if _, err := kv.Read([]byte(legacySTPStatusDBKey())); err == nil {
		t.Fatal("legacy status key was not deleted")
	}
}

func TestLoadStatusNormalizesEmptyWalletStatusHeights(t *testing.T) {
	kv := newMemoryKVDB()
	current := &Status{}
	buf, err := encodeStatusToBytes(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := kv.Write([]byte(DB_KEY_STATUS), buf); err != nil {
		t.Fatal(err)
	}

	status := loadStatusWithLegacyMigration(kv)
	if status.SyncHeight != -1 || status.SyncHeightL1 != -1 || status.SyncHeightL2 != -1 {
		t.Fatalf("empty status heights not normalized: got sync=%d l1=%d l2=%d",
			status.SyncHeight, status.SyncHeightL1, status.SyncHeightL2)
	}
}

func TestLoadStatusReportsMissingPersistentStatus(t *testing.T) {
	status, loaded := loadStatusWithLegacyMigrationResult(newMemoryKVDB())
	if loaded {
		t.Fatal("empty database reported persistent status")
	}
	if status.SyncHeight != -1 || status.SyncHeightL1 != -1 || status.SyncHeightL2 != -1 {
		t.Fatalf("default status heights not initialized as missing: got sync=%d l1=%d l2=%d",
			status.SyncHeight, status.SyncHeightL1, status.SyncHeightL2)
	}
}

func assertMigratedSTPStatus(t *testing.T, got, want *Status) {
	t.Helper()
	if got.SyncHeight != want.SyncHeight ||
		got.SyncHeightL1 != want.SyncHeightL1 ||
		got.SyncHeightL2 != want.SyncHeightL2 ||
		got.MaxFeeRateL1 != want.MaxFeeRateL1 ||
		got.HasStaked != want.HasStaked ||
		got.ContractSubAccountIndex != want.ContractSubAccountIndex {
		t.Fatalf("unexpected migrated status: got %+v want %+v", got, want)
	}
	if got.BlockHashMapL1[want.SyncHeightL1] != want.BlockHashMapL1[want.SyncHeightL1] {
		t.Fatalf("L1 block hash map not migrated: got %+v want %+v",
			got.BlockHashMapL1, want.BlockHashMapL1)
	}
	if got.BlockHashMapL2[want.SyncHeightL2] != want.BlockHashMapL2[want.SyncHeightL2] {
		t.Fatalf("L2 block hash map not migrated: got %+v want %+v",
			got.BlockHashMapL2, want.BlockHashMapL2)
	}
}
