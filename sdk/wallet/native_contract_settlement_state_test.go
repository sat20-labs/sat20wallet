package wallet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

var errNativeSettlementWrite = errors.New("injected native settlement write failure")

// The fault wrapper retains the real backend's transaction semantics. Failed
// staging and failed Flush must be discarded when the underlying batch closes.
type nativeSettlementFaultDB struct {
	indexer.KVDB
	failOperation int
	failFlush     bool
	operations    int
	commits       int
	beforeFlush   func()
}

func (d *nativeSettlementFaultDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return &nativeSettlementFaultBatch{
		WriteBatch: d.KVDB.(indexer.AtomicWriteBatchDB).NewAtomicWriteBatch(),
		db:         d,
	}
}

type nativeSettlementFaultBatch struct {
	indexer.WriteBatch
	db *nativeSettlementFaultDB
}

func (b *nativeSettlementFaultBatch) next() error {
	b.db.operations++
	if b.db.operations == b.db.failOperation {
		return errNativeSettlementWrite
	}
	return nil
}

func (b *nativeSettlementFaultBatch) Put(key, value []byte) error {
	if err := b.next(); err != nil {
		return err
	}
	return b.WriteBatch.Put(key, value)
}

func (b *nativeSettlementFaultBatch) Delete(key []byte) error {
	if err := b.next(); err != nil {
		return err
	}
	return b.WriteBatch.Delete(key)
}

func (b *nativeSettlementFaultBatch) Flush() error {
	if b.db.beforeFlush != nil {
		b.db.beforeFlush()
	}
	if b.db.failFlush {
		return errNativeSettlementWrite
	}
	if err := b.WriteBatch.Flush(); err != nil {
		return err
	}
	b.db.commits++
	return nil
}

type nativeSettlementManager struct {
	*rollbackContractManager
	appendError error
	stateKeys   [][]byte
}

func (m *nativeSettlementManager) AppendNativeContractState(batch indexer.WriteBatch, reservation ContractDeployResvIF, candidate ContractRuntime) error {
	if m.appendError != nil {
		return m.appendError
	}
	if reservation.GetContract().URL() != candidate.URL() {
		return fmt.Errorf("candidate does not match reservation")
	}
	raw, err := EncodeToBytes(candidate)
	if err != nil {
		return err
	}
	for _, key := range m.stateKeys {
		if err := batch.Put(key, raw); err != nil {
			return err
		}
	}
	return nil
}

type nativeSettlementFixture struct {
	directory string
	db        *nativeSettlementFaultDB
	manager   *nativeSettlementManager
	runtime   ContractRuntime
	swap      *SwapContractRuntime
	items     []*SwapHistoryItem
	deal      *DealInfo
	pending   []byte
	settled   []byte
}

func newNativeSettlementFixture(t *testing.T, template string) *nativeSettlementFixture {
	t.Helper()
	f := &nativeSettlementFixture{directory: t.TempDir()}
	database := NewKVDB(f.directory)
	if database == nil {
		t.Fatal("NewKVDB failed")
	}
	f.db = &nativeSettlementFaultDB{KVDB: database}
	t.Cleanup(func() { _ = f.db.Close() })
	f.manager = &nativeSettlementManager{
		rollbackContractManager: &rollbackContractManager{Manager: &Manager{
			db: f.db, cfg: &common.Config{Mode: "test"},
		}},
		stateKeys: [][]byte{[]byte("test/contract-state"), []byte("test/active-reservation")},
	}
	f.runtime = NewContractRuntime(f.manager, template)
	var err error
	f.swap, err = nativeSettlementSwap(f.runtime)
	if err != nil {
		t.Fatal(err)
	}
	p := f.swap
	p.Contract.GetContractBase().AssetName = indexer.AssetName{Protocol: rgb11wallet.Protocol, Type: indexer.ASSET_TYPE_FT, Ticker: strings.Repeat("a", 64)}
	p.ChannelAddr, p.Status = "native-settlement-channel", CONTRACT_STATUS_READY
	p.InvokeCount, p.CheckPoint, p.CheckPointBlock = 4, 3, 49
	p.CurrBlock, p.CurrBlockL1 = 50, 50
	p.AssetMerkleRoot, p.refreshTime = []byte("previous-root"), 99
	p.TotalWithdrawAssets, p.TotalOutputAssets = indexer.NewDecimal(17, 0), indexer.NewDecimal(21, 0)
	p.TotalWithdrawSats, p.TotalOutputSats = 17, 31
	p.TotalWithdrawTx, p.TotalWithdrawTxFee = 2, 3
	p.AssetAmtInPool = indexer.NewDecimal(5000, 0)
	p.resv = &rollbackContractReservation{contract: f.runtime, channel: p.ChannelAddr, status: RS_DEPLOY_CONTRACT_RUNNING}
	for _, owner := range []string{"alice", "bob"} {
		trader := NewTraderStatus(owner, 0)
		trader.WithdrawAmt, trader.WithdrawValue = indexer.NewDecimal(7, 0), 7
		trader.ProfitAmt = indexer.NewDecimal(25, 0)
		p.traderInfoMap[owner] = trader
		if err := saveContractInvokerStatus(f.db, p.URL(), trader); err != nil {
			t.Fatal(err)
		}
	}
	for i, owner := range []string{"alice", "bob", "alice"} {
		id := int64(i + 1)
		amount, value := id*100, id*11
		item := &SwapHistoryItem{
			InvokeHistoryItemBase: InvokeHistoryItemBase{Id: id, Done: ITEM_STATUS_INIT, Reason: INVOKE_REASON_NORMAL},
			OrderType:             ORDERTYPE_WITHDRAW, Address: owner, InUtxo: fmt.Sprintf("withdrawal-%d:0", id),
			UtxoId: indexer.ToUtxoId(50, i, 0), AssetName: p.GetAssetName().String(), ToL1: true,
			InAmt: indexer.NewDecimal(amount, 0), RemainingAmt: indexer.NewDecimal(amount, 0),
			InValue: value, RemainingValue: value,
		}
		if owner == "bob" {
			item.Padded = []byte("alice")
		}
		f.items = append(f.items, item)
		p.history[item.InUtxo] = item
		addItemToMap(item, p.withdrawMap)
		if err := SaveContractInvokeHistoryItem(f.db, p.URL(), item); err != nil {
			t.Fatal(err)
		}
	}
	f.deal = &DealInfo{
		AssetName: p.GetAssetName(), TotalAmt: indexer.NewDecimal(300, 0), TotalValue: 33,
		ItemIDs: []int64{2, 1}, Reason: INVOKE_RESULT_WITHDRAW, Height: 50, InvokeCount: 4,
		TxId: strings.Repeat("b", 64), Fee: 5, RuntimeMerkleRoot: []byte("signed-root"),
		SendInfo: map[string]*SendAssetInfo{"alice": {
			Address: "alice", AssetName: p.GetAssetName(), AssetAmt: indexer.NewDecimal(300, 0), Value: 33,
		}},
	}
	f.pending = p.nativeWithdrawalPendingKey()
	if err := p.stageNativeContractWithdrawal(f.deal); err != nil {
		t.Fatal(err)
	}
	f.settled, err = json.Marshal(nativeContractPendingWithdrawal{Version: 1, ContractURL: p.URL(), Deal: f.deal, Settled: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := EncodeToBytes(f.runtime)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range append([][]byte{[]byte(GetContractRuntimeKey(p.URL()))}, f.manager.stateKeys...) {
		if err := f.db.Write(key, raw); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func nativeSettlementDatabaseSnapshot(t *testing.T, database indexer.KVDB) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	if err := database.Scan(indexer.ScanOptions{}, func(k, v []byte) error {
		result[string(k)] = append([]byte(nil), v...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func nativeSettlementLedgerSnapshot(t *testing.T, p *SwapContractRuntime) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	encode := func(key string, value any) {
		raw, err := EncodeToBytes(value)
		if err != nil {
			t.Fatal(err)
		}
		result[key] = raw
	}
	encode("runtime", p.runtime)
	encode("refresh", p.refreshTime)
	for key, item := range p.history {
		encode("history/"+key, item)
	}
	for owner, items := range p.withdrawMap {
		for id, item := range items {
			encode(fmt.Sprintf("pending/%s/%d", owner, id), item)
		}
	}
	for owner, trader := range p.traderInfoMap {
		encode("trader/"+owner, trader)
	}
	return result
}

func assertNativeSettlementUnchanged(t *testing.T, f *nativeSettlementFixture, database, ledger map[string][]byte) {
	t.Helper()
	if !reflect.DeepEqual(database, nativeSettlementDatabaseSnapshot(t, f.db)) {
		t.Fatal("uncommitted settlement changed durable state")
	}
	if !reflect.DeepEqual(ledger, nativeSettlementLedgerSnapshot(t, f.swap)) {
		t.Fatal("uncommitted settlement changed live state")
	}
	if f.manager.saves != 0 {
		t.Fatal("settlement called the non-atomic reservation saver")
	}
}

func assertNativeSettlementCommitted(t *testing.T, f *nativeSettlementFixture) {
	t.Helper()
	p := f.swap
	if p.TotalWithdrawAssets.String() != "317" || p.TotalOutputAssets.String() != "321" ||
		p.TotalWithdrawSats != 50 || p.TotalOutputSats != 69 || p.TotalWithdrawTx != 3 || p.TotalWithdrawTxFee != 8 ||
		p.AssetAmtInPool.String() != "5000" || p.refreshTime != 0 {
		t.Fatalf("unexpected settled totals: %+v", p.SwapContractRunningData)
	}
	if len(p.history) != 1 || p.history[f.items[2].InUtxo] != f.items[2] ||
		len(p.withdrawMap) != 1 || len(p.withdrawMap["alice"]) != 1 || p.withdrawMap["alice"][3] != f.items[2] || f.items[2].Finished() {
		t.Fatal("settlement consumed an unsigned withdrawal from the same height and destination")
	}
	for i, item := range f.items {
		byID, err := loadContractInvokeHistoryItem(f.db, p.URL(), item.GetKey())
		if err != nil {
			t.Fatal(err)
		}
		byOutpoint, err := loadContractInvokeHistoryItemByInUtxo(f.db, p.URL(), item.InUtxo)
		if err != nil {
			t.Fatal(err)
		}
		for _, loaded := range []InvokeHistoryItem{byID, byOutpoint} {
			history := loaded.(*InvokeItem)
			if i < 2 {
				if !history.Finished() || history.Done != ITEM_STATUS_DEALT || history.OutTxId != f.deal.TxId ||
					history.OutAmt.Cmp(indexer.NewDecimal(int64(i+1)*100, 0)) != 0 || history.OutValue != int64(i+1)*11 ||
					history.RemainingAmt != nil || history.RemainingValue != 0 {
					t.Fatalf("selected history was not settled: %+v", history)
				}
			} else if history.Finished() || history.RemainingAmt.String() != "300" || history.RemainingValue != 33 {
				t.Fatalf("unsigned history changed: %+v", history)
			}
		}
	}
	for i, owner := range []string{"alice", "bob"} {
		loaded, err := loadContractInvokerStatus(f.db, p.URL(), owner)
		if err != nil {
			t.Fatal(err)
		}
		for _, trader := range []*TraderStatus{loaded.(*TraderStatus), p.traderInfoMap[owner]} {
			if trader.WithdrawAmt.Cmp(indexer.NewDecimal(int64(i+1)*100+7, 0)) != 0 ||
				trader.WithdrawValue != int64(i+1)*11+7 || trader.ProfitAmt.String() != "25" {
				t.Fatalf("wrong owner ledger for %s: %+v", owner, trader)
			}
		}
	}
	raw, err := f.db.Read([]byte(GetContractRuntimeKey(p.URL())))
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := ContractRuntimeUnMarsh(f.manager, f.runtime.GetTemplateName(), raw)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.GetRuntimeBase().runtime = reloaded
	want, err := EncodeToBytes(f.runtime)
	if err != nil || !bytes.Equal(raw, want) || reflect.TypeOf(reloaded) != reflect.TypeOf(f.runtime) {
		t.Fatalf("runtime reload differs from live concrete runtime: %T: %v", reloaded, err)
	}
	for _, key := range f.manager.stateKeys {
		got, err := f.db.Read(key)
		if err != nil || !bytes.Equal(got, raw) {
			t.Fatalf("reservation state did not commit with ledger: %s: %v", key, err)
		}
	}
	if got, err := f.db.Read([]byte(GetContractInvokeResultKey(p.URL(), f.deal.TxId))); err != nil || string(got) != INVOKE_RESULT_WITHDRAW {
		t.Fatalf("withdrawal result marker missing: %q: %v", got, err)
	}
	if got, err := f.db.Read(nativeContractWithdrawKey("settled", f.deal.TxId)); err != nil || !bytes.Equal(got, f.settled) {
		t.Fatalf("final canonical settlement missing: %v", err)
	}
	if _, err := f.db.Read(f.pending); !errors.Is(err, indexer.ErrKeyNotFound) {
		t.Fatalf("pending record survived committed settlement: %v", err)
	}
}

func TestNativeContractSettlementAtomicSelectionAndReload(t *testing.T) {
	for _, template := range []string{TEMPLATE_CONTRACT_TRANSCEND, TEMPLATE_CONTRACT_SWAP, TEMPLATE_CONTRACT_AMM, TEMPLATE_CONTRACT_FAUCET} {
		t.Run(template, func(t *testing.T) {
			f := newNativeSettlementFixture(t, template)
			trader := f.swap.traderInfoMap["alice"]
			delete(f.swap.traderInfoMap, "bob") // Exercise restoration of an uncached owner.
			database, ledger := nativeSettlementDatabaseSnapshot(t, f.db), nativeSettlementLedgerSnapshot(t, f.swap)
			f.db.beforeFlush = func() { assertNativeSettlementUnchanged(t, f, database, ledger) }
			if err := f.swap.applyNativeContractWithdrawalAtomic(f.deal, f.pending, f.settled); err != nil {
				t.Fatal(err)
			}
			f.db.beforeFlush = nil
			if f.db.commits != 1 || f.swap.traderInfoMap["alice"] != trader ||
				f.swap.CheckPoint != 4 || f.swap.CheckPointBlock != 50 || string(f.swap.AssetMerkleRoot) != "signed-root" {
				t.Fatal("settlement did not publish exactly one candidate")
			}
			assertNativeSettlementCommitted(t, f)
			if err := f.db.KVDB.Close(); err != nil {
				t.Fatal(err)
			}
			f.db.KVDB = NewKVDB(f.directory)
			if f.db.KVDB == nil {
				t.Fatal("reopen database failed")
			}
			assertNativeSettlementCommitted(t, f)
			database, ledger = nativeSettlementDatabaseSnapshot(t, f.db), nativeSettlementLedgerSnapshot(t, f.swap)
			if err := f.swap.applyNativeContractWithdrawalAtomic(f.deal, f.pending, f.settled); err != nil {
				t.Fatalf("replayed confirmed settlement failed: %v", err)
			}
			if err := f.swap.applyNativeContractWithdrawalAtomic(f.deal, f.pending, append(f.settled, ' ')); err == nil {
				t.Fatal("conflicting settlement record was accepted")
			}
			assertNativeSettlementUnchanged(t, f, database, ledger)
			if f.db.commits != 1 {
				t.Fatal("replay committed the ledger twice")
			}
		})
	}
}

func TestNativeContractSettlementWriteFailuresAreAtomic(t *testing.T) {
	f := newNativeSettlementFixture(t, TEMPLATE_CONTRACT_TRANSCEND)
	database, ledger := nativeSettlementDatabaseSnapshot(t, f.db), nativeSettlementLedgerSnapshot(t, f.swap)
	f.db.failFlush = true
	if err := f.swap.applyNativeContractWithdrawalAtomic(f.deal, f.pending, f.settled); !errors.Is(err, errNativeSettlementWrite) {
		t.Fatalf("flush failure was not returned: %v", err)
	}
	assertNativeSettlementUnchanged(t, f, database, ledger)
	operations := f.db.operations
	if operations == 0 {
		t.Fatal("flush fault did not exercise any writes")
	}
	f.db.failFlush = false
	for operation := 1; operation <= operations; operation++ {
		t.Run(fmt.Sprintf("stage-%d", operation), func(t *testing.T) {
			f.db.operations, f.db.failOperation = 0, operation
			if err := f.swap.applyNativeContractWithdrawalAtomic(f.deal, f.pending, f.settled); !errors.Is(err, errNativeSettlementWrite) {
				t.Fatalf("stage failure was not returned: %v", err)
			}
			assertNativeSettlementUnchanged(t, f, database, ledger)
		})
	}
	f.db.failOperation, f.db.operations = 0, 0
	f.manager.appendError = errNativeSettlementWrite
	if err := f.swap.applyNativeContractWithdrawalAtomic(f.deal, f.pending, f.settled); !errors.Is(err, errNativeSettlementWrite) {
		t.Fatalf("reservation failure was not returned: %v", err)
	}
	assertNativeSettlementUnchanged(t, f, database, ledger)
	f.manager.appendError = nil
	if f.db.commits != 0 {
		t.Fatal("a failed attempt committed the batch")
	}
	if err := f.swap.applyNativeContractWithdrawalAtomic(f.deal, f.pending, f.settled); err != nil {
		t.Fatalf("retry after discarded transaction failed: %v", err)
	}
	assertNativeSettlementCommitted(t, f)
	if f.db.commits != 1 {
		t.Fatal("retry did not commit exactly once")
	}
}

func TestNativeContractSettlementRejectsChangedSelection(t *testing.T) {
	f := newNativeSettlementFixture(t, TEMPLATE_CONTRACT_TRANSCEND)
	database, ledger := nativeSettlementDatabaseSnapshot(t, f.db), nativeSettlementLedgerSnapshot(t, f.swap)
	for _, test := range []struct {
		name   string
		change func(*DealInfo)
	}{
		{"duplicate item", func(d *DealInfo) { d.ItemIDs = []int64{1, 1} }},
		{"missing item", func(d *DealInfo) { d.ItemIDs = []int64{1, 40} }},
		{"changed total", func(d *DealInfo) { d.TotalAmt = indexer.NewDecimal(301, 0) }},
		{"changed destination", func(d *DealInfo) { d.SendInfo["alice"].Address = "mallory" }},
		{"item outside signed snapshot", func(d *DealInfo) { d.InvokeCount = 2 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(f.deal)
			if err != nil {
				t.Fatal(err)
			}
			var deal DealInfo
			if err := json.Unmarshal(raw, &deal); err != nil {
				t.Fatal(err)
			}
			test.change(&deal)
			if err := f.swap.applyNativeContractWithdrawalAtomic(&deal, f.pending, f.settled); err == nil {
				t.Fatal("changed withdrawal selection was accepted")
			}
			assertNativeSettlementUnchanged(t, f, database, ledger)
		})
	}
}

func TestNativeContractSettlementPreservesNewerCheckpoint(t *testing.T) {
	f := newNativeSettlementFixture(t, TEMPLATE_CONTRACT_TRANSCEND)
	f.swap.InvokeCount, f.swap.CheckPoint, f.swap.CheckPointBlock = 10, 9, 80
	f.swap.AssetMerkleRoot = []byte("newer-root")
	if err := f.swap.applyNativeContractWithdrawalAtomic(f.deal, f.pending, f.settled); err != nil {
		t.Fatal(err)
	}
	if f.swap.CheckPoint != 9 || f.swap.CheckPointBlock != 80 || string(f.swap.AssetMerkleRoot) != "newer-root" {
		t.Fatal("an older pending transaction rolled the contract checkpoint back")
	}
	assertNativeSettlementCommitted(t, f)
}

func TestNativeContractSettlementRequiresAtomicReservationPersistence(t *testing.T) {
	f := newNativeSettlementFixture(t, TEMPLATE_CONTRACT_TRANSCEND)
	database, ledger := nativeSettlementDatabaseSnapshot(t, f.db), nativeSettlementLedgerSnapshot(t, f.swap)
	// An ordinary bulk-batch capability must not silently substitute for one
	// transaction, even when it happens to use an atomic backend underneath.
	f.manager.Manager.db = struct{ indexer.KVDB }{f.db}
	if err := f.swap.applyNativeContractWithdrawalAtomic(f.deal, f.pending, f.settled); !errors.Is(err, ErrAtomicPersistenceUnavailable) {
		t.Fatalf("non-atomic database was accepted: %v", err)
	}
	assertNativeSettlementUnchanged(t, f, database, ledger)
	f.manager.Manager.db = f.db
	f.swap.stp = f.manager.rollbackContractManager
	if err := f.swap.applyNativeContractWithdrawalAtomic(f.deal, f.pending, f.settled); err == nil {
		t.Fatal("reservation without an atomic appender was accepted")
	}
	assertNativeSettlementUnchanged(t, f, database, ledger)
}
