package wallet

import (
	"context"
	"errors"
	"testing"
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
