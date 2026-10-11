package wallet

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

const rgb11ManagedOperationTestMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

func configuredRGB11ManagedOperationTestManager(t *testing.T) *Manager {
	t.Helper()
	manager := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(manager, newRGB11MemoryDKVSHTTP())
	if _, err := manager.ImportWallet(rgb11ManagedOperationTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	if err := manager.InitializeAccountManagement("password"); err != nil {
		t.Fatal(err)
	}
	// ImportWallet creates the local account profile, but remote recovery is not
	// active until the user completes account-storage activation. These tests
	// exercise the post-activation RGB transaction boundary, so make that
	// precondition explicit without constructing an unrelated recovery package.
	manager.mutex.Lock()
	manager.accountProfile.RecoveryConfigured = true
	if err := manager.saveAccountManagementProfileLocked(); err != nil {
		manager.mutex.Unlock()
		t.Fatal(err)
	}
	manager.mutex.Unlock()
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestRGB11ManagedOperationAggregatesMutationMarker(t *testing.T) {
	manager := &Manager{}
	manager.beginRGB11ManagedOperationState()
	if inOperation, first := manager.noteRGB11ManagedOperationMutation(); !inOperation || !first {
		t.Fatalf("first mutation state=(%v,%v)", inOperation, first)
	}
	if inOperation, first := manager.noteRGB11ManagedOperationMutation(); !inOperation || first {
		t.Fatalf("second mutation state=(%v,%v)", inOperation, first)
	}
	if !manager.endRGB11ManagedOperationState() {
		t.Fatal("operation did not retain its aggregated dirty marker")
	}
	if inOperation, first := manager.noteRGB11ManagedOperationMutation(); inOperation || first {
		t.Fatalf("mutation leaked beyond operation=(%v,%v)", inOperation, first)
	}
}

func TestRGB11ManagedOperationPublishesOneStableGeneration(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager := configuredRGB11ManagedOperationTestManager(t)
	initialGeneration := manager.accountProfile.ManagedDataGeneration
	_, err := runRGB11ManagedOperation(manager, context.Background(), rgb11ManagedOperationNew,
		func(rgb *rgb11Manager) (struct{}, error) {
			seedRGB11DurableAccountRecoveryState(t, rgb)
			// Multiple low-level mutation notifications inside one business
			// operation must still produce one managed generation and one final
			// account snapshot.
			rgb.autoBackupRGB11AfterMutation()
			rgb.autoBackupRGB11AfterMutation()
			return struct{}{}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if manager.accountProfile.ManagedDataDirty {
		t.Fatalf("stable ACK left account-managed data dirty: %+v", manager.accountProfile)
	}
	if got := manager.accountProfile.ManagedDataGeneration; got != initialGeneration+1 {
		t.Fatalf("managed generation=%d want=%d", got, initialGeneration+1)
	}
	payloads, err := manager.currentAccountManagedProviderPayloads(rgb11AccountManagedProviderID)
	if err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 1 {
		t.Fatalf("ACK-confirmed RGB11 recovery payloads=%d want=1", len(payloads))
	}
}

func TestAccountManagedSyncCannotClearDirtyActiveRGB11Transition(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager := configuredRGB11ManagedOperationTestManager(t)
	saveRGB11StatusTransfer(t, manager, "active-managed-sync", "prepared")
	manager.markAccountManagedDataDirtyDeferred(rgb11AccountManagedProviderID)
	generation := manager.accountProfile.ManagedDataGeneration

	err := manager.SyncAccountManagementState(context.Background())
	if !errors.Is(err, ErrRGB11ManagedOperationActive) {
		t.Fatalf("active account sync error=%v", err)
	}
	if !manager.accountProfile.ManagedDataDirty ||
		manager.accountProfile.ManagedDataGeneration != generation {
		t.Fatalf("active sync cleared or changed local operation marker: %+v", manager.accountProfile)
	}
}

type rgb11ManagedOperationLockWaitContext struct {
	context.Context
	mu       sync.Mutex
	tracking bool
	waiting  chan struct{}
	once     sync.Once
}

func (c *rgb11ManagedOperationLockWaitContext) trackLockWait() {
	c.mu.Lock()
	c.tracking = true
	c.mu.Unlock()
}

func (c *rgb11ManagedOperationLockWaitContext) Done() <-chan struct{} {
	c.mu.Lock()
	tracking := c.tracking
	c.mu.Unlock()
	if tracking {
		c.once.Do(func() { close(c.waiting) })
	}
	return c.Context.Done()
}

type rgb11ManagedRefreshBarrierEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (e *rgb11ManagedRefreshBarrierEvidence) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	status, err := e.BitcoinEvidenceProvider.GetTxStatus(txid)
	e.once.Do(func() {
		close(e.entered)
		<-e.release
	})
	return status, err
}

func TestRGB11ManagedNewRechecksActiveAfterRefreshWait(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	t.Run("already-pending-control-is-rejected", func(t *testing.T) {
		manager := configuredRGB11ManagedOperationTestManager(t)
		manager.rgbManager.scopeStates.stopReconciliations()
		if err := manager.rgbManager.projectionStore.SaveTransferState(&rgb11wallet.TransferState{
			TransferID: "managed-new-pending-control", Direction: "receive", Status: "pending",
			WitnessTxID:     "managed-new-pending-control-witness",
			OutputOutPoints: []string{"managed-new-pending-control-witness:0"}, MinConfirmations: 1,
		}); err != nil {
			t.Fatal("save pending receive fixture")
		}
		active, err := manager.hasAccountManagedRGB11Transition()
		if err != nil || !active {
			t.Fatalf("pending control fixture active=%t error=%t", active, err != nil)
		}
		called := false
		_, err = runRGB11ManagedOperation(manager, context.Background(), rgb11ManagedOperationNew,
			func(*rgb11Manager) (struct{}, error) {
				called = true
				return struct{}{}, nil
			})
		if !errors.Is(err, ErrRGB11ManagedOperationActive) || called {
			t.Fatalf("already-pending New operation was not rejected: active_error=%t callback_called=%t",
				errors.Is(err, ErrRGB11ManagedOperationActive), called)
		}
	})

	t.Run("settled-becomes-pending-while-new-operation-waits", func(t *testing.T) {
		manager := configuredRGB11ManagedOperationTestManager(t)
		manager.rgbManager.scopeStates.stopReconciliations()
		baseEvidence := &rgb11StatusEvidence{
			statuses:  make(map[string]*rgb11wallet.BitcoinTxStatus),
			outspends: make(map[string]*rgb11wallet.BitcoinOutspend),
		}
		barrier := &rgb11ManagedRefreshBarrierEvidence{
			BitcoinEvidenceProvider: baseEvidence,
			entered:                 make(chan struct{}),
			release:                 make(chan struct{}),
		}
		manager.rgbManager.evidence = barrier
		if err := manager.rgbManager.projectionStore.SaveTransferState(&rgb11wallet.TransferState{
			TransferID: "managed-new-settled-reorg", Direction: "receive", Status: "settled",
			WitnessTxID:     "managed-new-settled-reorg-witness",
			OutputOutPoints: []string{"managed-new-settled-reorg-witness:0"}, MinConfirmations: 1,
		}); err != nil {
			t.Fatal("save settled receive fixture")
		}
		activeBefore, err := manager.hasAccountManagedRGB11Transition()
		if err != nil || activeBefore {
			t.Fatalf("settled fixture active before refresh=%t error=%t", activeBefore, err != nil)
		}

		type refreshOutcome struct {
			result *RGB11RefreshResult
			err    error
		}
		refreshDone := make(chan refreshOutcome, 1)
		go func() {
			result, refreshErr := manager.rgbManager.RefreshRGB11State(context.Background())
			refreshDone <- refreshOutcome{result: result, err: refreshErr}
		}()
		var releaseOnce sync.Once
		releaseRefresh := func() { releaseOnce.Do(func() { close(barrier.release) }) }
		refreshFinished := false
		operationStarted := false
		operationFinished := false
		operationDone := make(chan error, 1)
		defer func() {
			releaseRefresh()
			if !refreshFinished {
				select {
				case <-refreshDone:
				case <-time.After(5 * time.Second):
				}
			}
			if operationStarted && !operationFinished {
				select {
				case <-operationDone:
				case <-time.After(5 * time.Second):
				}
			}
		}()
		select {
		case <-barrier.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("refresh did not reach the Bitcoin status barrier")
		}

		waitContext := &rgb11ManagedOperationLockWaitContext{
			Context: context.Background(), waiting: make(chan struct{}),
		}
		var selectMu sync.Mutex
		selectCalls := 0
		selectManager := func() (*rgb11Manager, error) {
			selectMu.Lock()
			selectCalls++
			calls := selectCalls
			selectMu.Unlock()
			if calls == 2 {
				// The second selection occurs after baseline synchronization. The next
				// Done call is the refresh-lock wait, not a baseline/network check.
				waitContext.trackLockWait()
			}
			return manager.synchronizedRGB11Manager()
		}
		operationCalled := make(chan struct{})
		operationStarted = true
		go func() {
			_, operationErr := runRGB11ManagedOperationWithManager(manager, waitContext,
				rgb11ManagedOperationNew, selectManager,
				func(*rgb11Manager) (struct{}, error) {
					close(operationCalled)
					return struct{}{}, nil
				})
			operationDone <- operationErr
		}()
		select {
		case <-waitContext.waiting:
		case <-time.After(5 * time.Second):
			t.Fatal("managed operation did not reach the chain-refresh lock wait")
		}
		select {
		case <-operationCalled:
			releaseRefresh()
			t.Fatal("New operation callback ran while refresh still held chain lock")
		default:
		}

		releaseRefresh()
		var refreshed refreshOutcome
		select {
		case refreshed = <-refreshDone:
			refreshFinished = true
		case <-time.After(5 * time.Second):
			t.Fatal("refresh did not finish after barrier release")
		}
		stateAfterRefresh, stateLoadErr := manager.rgbManager.projectionStore.LoadTransferState("managed-new-settled-reorg")
		if refreshed.err != nil || refreshed.result == nil || refreshed.result.Reorged != 1 || refreshed.result.Pending != 1 ||
			stateLoadErr != nil || stateAfterRefresh == nil || stateAfterRefresh.Status != "pending" {
			statePending := stateLoadErr == nil && stateAfterRefresh != nil && stateAfterRefresh.Status == "pending"
			reorged, pending := -1, -1
			if refreshed.result != nil {
				reorged, pending = refreshed.result.Reorged, refreshed.result.Pending
			}
			t.Fatalf("refresh did not move the settled fixture to pending: reorged=%d pending=%d state_pending=%t error=%t",
				reorged, pending, statePending, refreshed.err != nil)
		}

		var operationErr error
		select {
		case operationErr = <-operationDone:
			operationFinished = true
		case <-time.After(5 * time.Second):
			t.Fatal("managed operation did not return after refresh lock release")
		}
		if !errors.Is(operationErr, ErrRGB11ManagedOperationActive) {
			t.Errorf("mode=New was not rejected after refresh made the transition active: error_is_active=%t callback_called=%t",
				errors.Is(operationErr, ErrRGB11ManagedOperationActive), func() bool {
					select {
					case <-operationCalled:
						return true
					default:
						return false
					}
				}())
		}
		select {
		case <-operationCalled:
			t.Error("mode=New callback ran despite active transition after waiting for refresh")
		default:
		}
	})
}
