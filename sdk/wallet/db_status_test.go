package wallet

import (
	"bytes"
	"sort"
	"sync"
	"testing"

	db "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
)

type memoryKVDB struct {
	mu   sync.RWMutex
	data map[string][]byte
}

type statusTipClient struct {
	IndexerRPCClient
	syncHeight     int
	syncCalls      int
	blockHashCalls int
}

func (c *statusTipClient) GetSyncHeight() int {
	c.syncCalls++
	return c.syncHeight
}

func (c *statusTipClient) GetBlockHash(_ int) (string, error) {
	c.blockHashCalls++
	return "hash", nil
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
		DBver:                   "0.1.2",
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
	if status.DBver != legacy.DBver {
		t.Fatalf("legacy STP DB version lost: got %q want %q", status.DBver, legacy.DBver)
	}
	if _, err := kv.Read([]byte(DB_KEY_STATUS)); err != nil {
		t.Fatalf("new status key not written: %v", err)
	}
	if _, err := kv.Read([]byte(legacySTPStatusDBKey())); err == nil {
		t.Fatal("legacy status key was not deleted")
	}
}

func TestLoadStatusLegacySTPStatusOverridesWalletStatus(t *testing.T) {
	kv := newMemoryKVDB()
	current := newDefaultStatus()
	current.DBver = DB_VERSION
	current.CurrentWallet = 42
	current.CurrentAccount = 3
	current.SyncHeightL1 = 123
	current.SyncHeightL2 = 456
	currentBuf, err := encodeStatusToBytes(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := kv.Write([]byte(DB_KEY_STATUS), currentBuf); err != nil {
		t.Fatal(err)
	}

	legacy := &Status{
		SoftwareVer:             "0.2.1",
		DBver:                   "0.1.3",
		CurrentWallet:           7,
		CurrentAccount:          2,
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
	if status.DBver != legacy.DBver {
		t.Fatalf("legacy STP DB version lost: got %q want %q", status.DBver, legacy.DBver)
	}
	if status.CurrentWallet != legacy.CurrentWallet || status.CurrentAccount != legacy.CurrentAccount {
		t.Fatalf("legacy STP status was not authoritative: got wallet=%d account=%d want wallet=%d account=%d",
			status.CurrentWallet, status.CurrentAccount, legacy.CurrentWallet, legacy.CurrentAccount)
	}
	assertMigratedSTPStatus(t, status, legacy)

	persisted, ok := readStatusFromDB(kv, DB_KEY_STATUS)
	if !ok || persisted.DBver != legacy.DBver || persisted.SyncHeightL1 != legacy.SyncHeightL1 {
		t.Fatalf("wallet-status did not persist authoritative legacy STP status: %+v", persisted)
	}
	if _, err := kv.Read([]byte(legacySTPStatusDBKey())); err == nil {
		t.Fatal("legacy status key was not deleted after successful migration")
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

func TestLoadStatusNeverQueriesIndexerTips(t *testing.T) {
	oldTesting := ENABLE_TESTING
	ENABLE_TESTING = true
	defer func() { ENABLE_TESTING = oldTesting }()

	l1 := &statusTipClient{syncHeight: 200}
	l2 := &statusTipClient{syncHeight: 100}
	l1Manager := NewIndexerRPCClientMgr()
	l1Manager.Set(l1)
	l2Manager := NewIndexerRPCClientMgr()
	l2Manager.Set(l2)

	kv := newMemoryKVDB()
	manager := &Manager{
		db:              kv,
		l1IndexerClient: l1Manager,
		l2IndexerClient: l2Manager,
	}
	status := manager.loadStatus()

	if l1.syncCalls != 0 || l1.blockHashCalls != 0 ||
		l2.syncCalls != 0 || l2.blockHashCalls != 0 {
		t.Fatalf("local status load queried indexer tips: l1 sync=%d hash=%d, l2 sync=%d hash=%d",
			l1.syncCalls, l1.blockHashCalls, l2.syncCalls, l2.blockHashCalls)
	}
	if status.SyncHeight != -1 || status.SyncHeightL1 != -1 || status.SyncHeightL2 != -1 {
		t.Fatalf("testing mode initialized status heights: sync=%d l1=%d l2=%d",
			status.SyncHeight, status.SyncHeightL1, status.SyncHeightL2)
	}
	if _, loaded := loadStatusWithLegacyMigrationResult(kv); !loaded {
		t.Fatal("default status was not persisted")
	}
}

func TestLoadStatusResetsChainStateWithoutNetwork(t *testing.T) {
	oldTesting := ENABLE_TESTING
	oldChain := _chain
	ENABLE_TESTING = false
	_chain = "testnet"
	defer func() {
		ENABLE_TESTING = oldTesting
		_chain = oldChain
	}()

	kv := newMemoryKVDB()
	mainnetStatus := &Status{
		CurrentChain:   "mainnet",
		SyncHeight:     700000,
		SyncHeightL1:   700000,
		SyncHeightL2:   39794,
		BlockHashMapL1: map[int]string{700000: "mainnet-l1"},
		BlockHashMapL2: map[int]string{39794: "mainnet-l2"},
	}
	normalizeStatus(mainnetStatus)
	if err := saveStatusToDB(kv, mainnetStatus); err != nil {
		t.Fatal(err)
	}

	l1 := &statusTipClient{syncHeight: 300}
	l2 := &statusTipClient{syncHeight: 3445}
	l1Manager := NewIndexerRPCClientMgr()
	l1Manager.Set(l1)
	l2Manager := NewIndexerRPCClientMgr()
	l2Manager.Set(l2)
	manager := &Manager{
		cfg:             &common.Config{Chain: "testnet"},
		db:              kv,
		l1IndexerClient: l1Manager,
		l2IndexerClient: l2Manager,
	}

	status := manager.loadStatus()
	if status.CurrentChain != "testnet" || status.SyncHeightL1 != -1 ||
		status.SyncHeightL2 != -1 || status.SyncHeight != -1 {
		t.Fatalf("chain status was not reset for background initialization: %+v", status)
	}
	if _, ok := status.BlockHashMapL1[700000]; ok {
		t.Fatalf("mainnet L1 hash survived chain reset: %+v", status.BlockHashMapL1)
	}
	if _, ok := status.BlockHashMapL2[39794]; ok {
		t.Fatalf("mainnet L2 hash survived chain reset: %+v", status.BlockHashMapL2)
	}
	if l1.syncCalls != 0 || l1.blockHashCalls != 0 ||
		l2.syncCalls != 0 || l2.blockHashCalls != 0 {
		t.Fatalf("chain reset queried indexers synchronously: l1 sync=%d hash=%d, l2 sync=%d hash=%d",
			l1.syncCalls, l1.blockHashCalls, l2.syncCalls, l2.blockHashCalls)
	}
}

func TestInitialStatusBlockHashWindowByChain(t *testing.T) {
	tests := []struct {
		name  string
		chain string
		l1    bool
		want  int
	}{
		{name: "bitcoin testnet4", chain: "testnet", l1: true, want: 144},
		{name: "bitcoin mainnet", chain: "mainnet", l1: true, want: 7},
		{name: "satoshinet testnet", chain: "testnet", l1: false, want: 7},
		{name: "satoshinet mainnet", chain: "mainnet", l1: false, want: 7},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := initialStatusBlockHashWindow(test.chain, test.l1); got != test.want {
				t.Fatalf("unexpected block hash window: got %d want %d", got, test.want)
			}
		})
	}
}

func TestBootstrapStatusTipsUsesChainSpecificWindows(t *testing.T) {
	kv := newMemoryKVDB()
	manager := &Manager{
		db:     kv,
		status: newDefaultStatus(),
	}
	manager.status.CurrentChain = "testnet"
	manager.statusBootstrapGeneration = 1

	l1 := &statusTipClient{syncHeight: 300}
	manager.bootstrapStatusTip(1, "testnet", true, l1)
	if l1.syncCalls != 1 || l1.blockHashCalls != 144 {
		t.Fatalf("bitcoin testnet4 bootstrap calls: sync=%d hashes=%d", l1.syncCalls, l1.blockHashCalls)
	}

	l2 := &statusTipClient{syncHeight: 3445}
	manager.bootstrapStatusTip(1, "testnet", false, l2)
	if l2.syncCalls != 1 || l2.blockHashCalls != 7 {
		t.Fatalf("satoshinet bootstrap calls: sync=%d hashes=%d", l2.syncCalls, l2.blockHashCalls)
	}
	if manager.status.SyncHeightL1 != 300 || manager.status.SyncHeightL2 != 3445 {
		t.Fatalf("background tips were not committed: %+v", manager.status)
	}
}

func TestBootstrapStatusTipRejectsStaleGeneration(t *testing.T) {
	manager := &Manager{
		db:     newMemoryKVDB(),
		status: newDefaultStatus(),
	}
	manager.status.CurrentChain = "testnet"
	manager.statusBootstrapGeneration = 2

	client := &statusTipClient{syncHeight: 300}
	manager.bootstrapStatusTip(1, "testnet", true, client)
	if manager.status.SyncHeightL1 != -1 || manager.status.SyncHeight != -1 {
		t.Fatalf("stale bootstrap changed status: %+v", manager.status)
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
