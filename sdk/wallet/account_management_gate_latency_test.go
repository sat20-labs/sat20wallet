package wallet

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type blockedAccountConfigHTTP struct {
	*rgb11MemoryDKVSHTTP
	once                 sync.Once
	started, release     chan struct{}
	continueAfterRelease bool
}

func assertAccountNetworkReleasedScopeLocks(t *testing.T, manager *Manager) {
	t.Helper()
	if manager.channelIdentityMu.TryLock() {
		manager.channelIdentityMu.Unlock()
	} else {
		t.Error("network wait holds channel identity lock")
	}
	if manager.rgbOperationMu.TryLock() {
		manager.rgbOperationMu.Unlock()
	} else {
		t.Error("network wait holds RGB scope lock")
	}
	if manager.mutex.TryLock() {
		manager.mutex.Unlock()
	} else {
		t.Error("network wait holds manager data lock")
	}
}

func TestAccountRemoteCommitDoesNotDependOnCurrentSelection(t *testing.T) {
	for _, change := range []string{"account", "wallet"} {
		t.Run(change, func(t *testing.T) {
			manager, _, secret := buildRootWrapperSource(t)
			defer zeroBytes(secret)
			rootID := manager.status.CurrentWallet
			if err := manager.EnsureAccount(rootID, 1, "Savings", ""); err != nil {
				t.Fatal(err)
			}
			childID, _, err := manager.CreateWallet("password")
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.SwitchWallet(rootID, "password"); err != nil {
				t.Fatal(err)
			}
			snapshot, err := manager.captureAccountManagementSyncSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			defer zeroBytes(snapshot.secret)
			state, err := account.OpenManagedState(snapshot.secret, snapshot.profile.AccountID, snapshot.profile.StateEnvelope)
			if err != nil {
				t.Fatal(err)
			}
			target, _, err := buildAccountManagedStateTarget(state, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			envelope, err := account.SealManagedState(snapshot.secret, snapshot.profile.AccountID, target, nil)
			if err != nil {
				t.Fatal(err)
			}
			commit, err := manager.prepareAccountManagedCommitForSync(target, snapshot, envelope, nil)
			if err != nil {
				t.Fatal(err)
			}
			if change == "account" {
				manager.SwitchAccount(1)
			} else if err := manager.SwitchWallet(childID, "password"); err != nil {
				t.Fatal(err)
			}
			walletID, accountIndex := manager.status.CurrentWallet, manager.status.CurrentAccount
			err = manager.withAccountLocalState(false, func() error {
				_, _, err := manager.applyAccountManagedCommitForSync(commit, snapshot)
				return err
			})
			if err != nil {
				t.Fatalf("selection-only change invalidated root-account commit: %v", err)
			}
			if manager.status.CurrentWallet != walletID || manager.status.CurrentAccount != accountIndex {
				t.Fatal("root-account commit replaced current wallet/account selection")
			}
		})
	}
}

func TestAccountRemoteCommitRejectsChangedManagedDataGeneration(t *testing.T) {
	manager, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	snapshot, err := manager.captureAccountManagementSyncSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(snapshot.secret)
	state, err := account.OpenManagedState(snapshot.secret, snapshot.profile.AccountID, snapshot.profile.StateEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := manager.prepareAccountManagedCommitForSync(state, snapshot, snapshot.profile.StateEnvelope, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager.markAccountManagedDataDirtyDeferred(rgb11AccountManagedProviderID)
	generation := manager.accountProfile.ManagedDataGeneration
	err = manager.withAccountLocalState(false, func() error {
		_, _, err := manager.applyAccountManagedCommitForSync(commit, snapshot)
		return err
	})
	if !errors.Is(err, errAccountSnapshotChanged) {
		t.Fatalf("changed managed-data generation accepted: %v", err)
	}
	if manager.accountProfile.ManagedDataGeneration != generation {
		t.Fatal("stale remote commit replaced managed-data generation")
	}
	if err := manager.checkAccountManagedDataImport(); err != nil {
		t.Fatalf("stale result created import marker: %v", err)
	}
}

func (h *blockedAccountConfigHTTP) DKVSClientConfig() (*dkvsindexer.ClientConfig, error) {
	h.once.Do(func() { close(h.started) })
	<-h.release
	if h.continueAfterRelease {
		return h.rgb11MemoryDKVSHTTP.DKVSClientConfig()
	}
	return nil, context.DeadlineExceeded
}

func TestAccountActiveMailboxReadRejectsChangedCatalog(t *testing.T) {
	manager, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	remote := &blockedAccountConfigHTTP{rgb11MemoryDKVSHTTP: newRGB11MemoryDKVSHTTP(),
		started: make(chan struct{}), release: make(chan struct{}), continueAfterRelease: true}
	manager.http = remote
	manager.dkvs.mu.Lock()
	manager.dkvs.clients = make(map[string]*SatsNetDKVSClient)
	manager.dkvs.mu.Unlock()
	manager.accountProfile.RecoveryConfigured = true
	done := make(chan error, 1)
	go func() { done <- manager.importAccountManagedActiveDataForSync(false) }()
	select {
	case <-remote.started:
	case <-time.After(2 * time.Second):
		t.Fatal("mailbox read did not reach network")
	}
	editDone := make(chan error, 1)
	go func() { editDone <- manager.EnsureAccount(manager.status.CurrentWallet, 1, "Savings", "") }()
	select {
	case err := <-editDone:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(2 * time.Second):
		t.Error("catalog edit blocked behind mailbox network read")
	}
	close(remote.release)
	select {
	case err := <-done:
		if !errors.Is(err, errAccountSnapshotChanged) {
			t.Fatalf("changed mailbox catalog: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("mailbox import did not finish")
	}
}

func TestTemporaryAccountConfigWaitDoesNotBlockLocalWalletAccess(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	remote := &blockedAccountConfigHTTP{rgb11MemoryDKVSHTTP: newRGB11MemoryDKVSHTTP(), started: make(chan struct{}), release: make(chan struct{})}
	manager.http = remote
	manager.dkvs.mu.Lock()
	manager.dkvs.clients = make(map[string]*SatsNetDKVSClient)
	manager.dkvs.mu.Unlock()
	manager.accountProfile.StorageMode = AccountStorageTemporary
	manager.accountProfile.RecoveryConfigured = false
	manager.accountProfile.ManagedDataDirty = true
	syncDone := make(chan error, 1)
	go func() { syncDone <- manager.SyncAccountManagementState(context.Background()) }()
	select {
	case <-remote.started:
	case <-time.After(2 * time.Second):
		t.Fatal("temporary account sync did not reach config request")
	}
	assertAccountNetworkReleasedScopeLocks(t, manager)
	unlockDone, stateDone := make(chan error, 1), make(chan error, 1)
	go func() { _, err := manager.UnlockWallet("password"); unlockDone <- err }()
	go func() { _, err := manager.GetRGB11State(); stateDone <- err }()
	status := manager.GetAccountManagementStatus()
	if !status.Active || !status.ManagedDataDirty || status.RecoveryConfigured {
		t.Error("fixture does not match temporary dirty account status")
	}
	for name, done := range map[string]chan error{"reauthenticate": unlockDone, "RGB state": stateDone} {
		select {
		case err := <-done:
			done <- err
		case <-time.After(5 * time.Second):
			t.Errorf("%s blocked behind DKVS config request", name)
		}
	}
	close(remote.release)
	if err := <-syncDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("config request error = %v", err)
	}
	for name, done := range map[string]chan error{"reauthenticate": unlockDone, "RGB state": stateDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%s after release: %v", name, err)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("%s did not resume after config request failed", name)
		}
	}
}

// Diagnostic regression: an already-unlocked wallet should remain locally
// readable and re-authenticatable while a background remote refresh is stalled.
func TestAccountRootWrapperRemoteWaitDoesNotBlockLocalWalletAccess(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, memoryStore, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	store := &blockingAccountRootWrapperStore{
		memoryAccountRootWrapperStore: memoryStore,
		started:                       make(chan struct{}),
		release:                       make(chan struct{}),
	}
	syncDone := make(chan error, 1)
	go func() { syncDone <- manager.syncAccountRootWrapper(store) }()
	select {
	case <-store.started:
	case <-time.After(2 * time.Second):
		t.Fatal("background wrapper did not reach the remote wait")
	}
	assertAccountNetworkReleasedScopeLocks(t, manager)
	unlockDone := make(chan error, 1)
	stateDone := make(chan error, 1)
	go func() { _, err := manager.UnlockWallet("password"); unlockDone <- err }()
	go func() { _, err := manager.GetRGB11State(); stateDone <- err }()
	status := manager.GetAccountManagementStatus()
	if !status.Active {
		t.Error("local account status unexpectedly unavailable")
	}
	for name, done := range map[string]chan error{"reauthenticate": unlockDone, "RGB state": stateDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%s failed: %v", name, err)
			}
			done <- err // Leave completion available for bounded cleanup below.
		case <-time.After(5 * time.Second):
			t.Errorf("%s blocked behind background remote refresh", name)
		}
	}
	stack := make([]byte, 1<<20)
	n := runtime.Stack(stack, true)
	for _, line := range strings.Split(string(stack[:n]), "\n") {
		if strings.Contains(line, "UnlockWallet(") || strings.Contains(line, "beginRGB11Operation(") ||
			strings.Contains(line, "runAccountApplicationSync(") || strings.Contains(line, "blockingAccountRootWrapperStore).WaitReady(") {
			t.Log(line)
		}
	}
	close(store.release)
	for name, done := range map[string]chan error{"background": syncDone, "reauthenticate": unlockDone, "RGB state": stateDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%s after release: %v", name, err)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("%s did not resume after remote wait released", name)
		}
	}
}
