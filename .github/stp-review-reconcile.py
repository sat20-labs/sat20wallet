from pathlib import Path


def replace(path, old, new):
    p = Path(path)
    text = p.read_text()
    if text.count(old) != 1:
        raise SystemExit(f"Expected one reviewed preimage in {path}, got {text.count(old)}")
    p.write_text(text.replace(old, new))


path = "sdk/wallet/native_contract_pending.go"
replace(path, '\t"sync"\n', '\t"time"\n')
replace(path, 'var nativeContractSettlementMu sync.Mutex\n\n', '')
replace(path, '\tnativeChannelInventoryMu.Lock()\n\tdefer nativeChannelInventoryMu.Unlock()\n', '''	// Selection and ledger commit share only this contract's local mutex.
	// No chain query, peer request or broadcast is allowed in this section.
	p.mutex.Lock()
	defer p.mutex.Unlock()
''')
start = 'func (p *SwapContractRuntime) reconcileNativeContractWithdrawals() (bool, error) {'
p = Path(path)
text = p.read_text()
a, b = text.index(start), text.index('\nfunc (p *ContractRuntimeBase) reconcileNativeContractBlock()', text.index(start))
text = text[:a] + '''func (p *SwapContractRuntime) reconcileNativeContractWithdrawals() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return p.reconcileNativeContractWithdrawal(ctx, func(ctx context.Context, txid string) (*ConfirmedNativeContractWithdrawal, error) {
		manager := p.stp.GetWalletMgr()
		if err := manager.resumeNativeContractBroadcast(ctx, p.localWallet, p.URL(), txid); err != nil {
			return nil, err
		}
		return manager.LoadConfirmedNativeContractWithdrawal(ctx, p.localWallet, txid)
	})
}

// Chain verification runs on an immutable pending snapshot without a mutex.
// Concurrent monitors may observe the same transaction: exact-byte replay is
// idempotent, and applyNativeContractWithdrawalAtomic rechecks the selected
// obligation under the contract mutex before committing its ledger once.
func (p *SwapContractRuntime) reconcileNativeContractWithdrawal(ctx context.Context,
	verify func(context.Context, string) (*ConfirmedNativeContractWithdrawal, error)) (bool, error) {
	if !p.nativePublicContract() {
		return false, nil
	}
	pending, err := p.loadNativeContractPending()
	if err != nil || pending == nil {
		return pending != nil, err
	}
	if pending.Deal.TxId == "" {
		return true, nil
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	confirmed, err := verify(ctx, pending.Deal.TxId)
	if err != nil {
		return true, err
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	if confirmed == nil || confirmed.ContractURL != p.URL() || confirmed.Amount.Cmp(pending.Deal.TotalAmt) != 0 {
		return true, fmt.Errorf("native confirmed pair differs from frozen withdrawal")
	}
	pending.Deal.Fee = confirmed.Fee
	pending.Settled = true
	raw, err := json.Marshal(pending)
	if err != nil {
		return true, err
	}
	if err := p.applyNativeContractWithdrawalAtomic(pending.Deal, p.nativeWithdrawalPendingKey(), raw); err != nil {
		return true, err
	}
	return false, nil
}
''' + text[b:]
p.write_text(text)

path = "sdk/wallet/native_contract_settlement_state.go"
replace(path, '''// confirmed settlement. The caller serializes reconciliation of the two chain
// monitors and supplies an immutable pending record and its canonical final
// encoding. No live ledger state changes until the single transaction commits.''', '''// confirmed settlement. Network verification is performed without locks;
// this local commit serializes against selection changes and other monitors.
// No live ledger state changes until the single transaction commits.''')
replace(path, '''	if existing, err := database.Read(pendingKey); err != nil {
		return fmt.Errorf("native withdrawal pending record unavailable: %w", err)
	} else if len(existing) == 0 {
		return fmt.Errorf("empty native withdrawal pending record")
	}
''', '''	// A slow chain response must not settle or delete a different pending
	// selection installed while the network request was in flight.
	pending, err := p.loadNativeContractPending()
	if err != nil {
		return err
	}
	if pending == nil || !bytes.Equal(pendingKey, p.nativeWithdrawalPendingKey()) ||
		pending.Deal.TxId != deal.TxId {
		return fmt.Errorf("native withdrawal pending identity changed during verification")
	}
	expected, err := nativeWithdrawalSelection(pending.Deal)
	if err != nil {
		return err
	}
	actual, err := nativeWithdrawalSelection(deal)
	if err != nil {
		return err
	}
	if !bytes.Equal(expected, actual) {
		return fmt.Errorf("native withdrawal pending selection changed during verification")
	}
''')

test = Path("sdk/wallet/native_contract_reconcile_test.go")
if test.exists():
    raise SystemExit("Refusing to overwrite existing regression test")
test.write_text('''package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
)

func nativeReconcileResult(f *nativeSettlementFixture) *ConfirmedNativeContractWithdrawal {
	return &ConfirmedNativeContractWithdrawal{ContractURL: f.swap.URL(), Amount: f.deal.TotalAmt.Clone(), Fee: f.deal.Fee}
}

func prepareNativeReconcileExpected(t *testing.T, f *nativeSettlementFixture) {
	t.Helper()
	pending, err := f.swap.loadNativeContractPending()
	if err != nil {
		t.Fatal(err)
	}
	pending.Settled = true
	f.settled, err = json.Marshal(pending)
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeContractReconcileNetworkRunsWithoutContractLock(t *testing.T) {
	f := newNativeSettlementFixture(t, TEMPLATE_CONTRACT_TRANSCEND)
	prepareNativeReconcileExpected(t, f)
	_, err := f.swap.reconcileNativeContractWithdrawal(context.Background(), func(ctx context.Context, txid string) (*ConfirmedNativeContractWithdrawal, error) {
		if !f.swap.mutex.TryLock() {
			return nil, fmt.Errorf("network verification holds contract mutex")
		}
		f.swap.mutex.Unlock()
		if txid != f.deal.TxId {
			return nil, fmt.Errorf("network verification changed transaction identity")
		}
		return nativeReconcileResult(f), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertNativeSettlementCommitted(t, f)
}

func TestNativeContractReconcileSlowContractDoesNotBlockAnother(t *testing.T) {
	a := newNativeSettlementFixture(t, TEMPLATE_CONTRACT_TRANSCEND)
	b := newNativeSettlementFixture(t, TEMPLATE_CONTRACT_AMM)
	prepareNativeReconcileExpected(t, a)
	prepareNativeReconcileExpected(t, b)
	entered, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	aDone := make(chan error, 1)
	go func() {
		_, err := a.swap.reconcileNativeContractWithdrawal(ctx, func(ctx context.Context, _ string) (*ConfirmedNativeContractWithdrawal, error) {
			close(entered)
			select {
			case <-release:
				return nativeReconcileResult(a), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
		aDone <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first contract did not enter network verification")
	}
	bDone := make(chan error, 1)
	go func() {
		_, err := b.swap.reconcileNativeContractWithdrawal(ctx, func(context.Context, string) (*ConfirmedNativeContractWithdrawal, error) {
			return nativeReconcileResult(b), nil
		})
		bDone <- err
	}()
	select {
	case err := <-bDone:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("unrelated contract was blocked by a network call")
	}
	close(release)
	if err := <-aDone; err != nil {
		t.Fatal(err)
	}
	assertNativeSettlementCommitted(t, a)
	assertNativeSettlementCommitted(t, b)
}

func TestNativeContractReconcileConcurrentMonitorsCommitOnce(t *testing.T) {
	f := newNativeSettlementFixture(t, TEMPLATE_CONTRACT_TRANSCEND)
	prepareNativeReconcileExpected(t, f)
	entered, release := make(chan struct{}, 2), make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := f.swap.reconcileNativeContractWithdrawal(ctx, func(ctx context.Context, _ string) (*ConfirmedNativeContractWithdrawal, error) {
				entered <- struct{}{}
				select {
				case <-release:
					return nativeReconcileResult(f), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
			done <- err
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("a monitor held a lock while verifying the network")
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if f.db.commits != 1 {
		t.Fatalf("settlement committed %d times", f.db.commits)
	}
	assertNativeSettlementCommitted(t, f)
}

func TestNativeContractReconcileRejectsStaleNetworkResult(t *testing.T) {
	f := newNativeSettlementFixture(t, TEMPLATE_CONTRACT_TRANSCEND)
	_, err := f.swap.reconcileNativeContractWithdrawal(context.Background(), func(context.Context, string) (*ConfirmedNativeContractWithdrawal, error) {
		pending, err := f.swap.loadNativeContractPending()
		if err != nil {
			return nil, err
		}
		pending.Deal.TotalAmt = indexer.NewDefaultDecimal(301)
		raw, err := json.Marshal(pending)
		if err != nil {
			return nil, err
		}
		if err := f.db.Write(f.pending, raw); err != nil {
			return nil, err
		}
		return nativeReconcileResult(f), nil
	})
	if err == nil || f.db.commits != 0 || f.swap.TotalWithdrawTx != 2 {
		t.Fatalf("stale network response changed the ledger: %v", err)
	}
	if _, err := f.db.Read(f.pending); err != nil {
		t.Fatal("stale result deleted the current pending selection")
	}
}

func TestNativeContractReconcileCancellationDoesNotCommit(t *testing.T) {
	f := newNativeSettlementFixture(t, TEMPLATE_CONTRACT_TRANSCEND)
	ctx, cancel := context.WithCancel(context.Background())
	_, err := f.swap.reconcileNativeContractWithdrawal(ctx, func(context.Context, string) (*ConfirmedNativeContractWithdrawal, error) {
		cancel()
		return nativeReconcileResult(f), nil
	})
	if !errors.Is(err, context.Canceled) || f.db.commits != 0 {
		t.Fatalf("cancelled chain verification committed: %v", err)
	}
}
''')
