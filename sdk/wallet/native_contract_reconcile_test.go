package wallet

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
