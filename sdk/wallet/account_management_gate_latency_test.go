package wallet

import (
	"context"
	"errors"
	"reflect"
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

func accountOperationWithin(t *testing.T, name string, operation func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- operation() }()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not return during the held network wait", name)
		return nil
	}
}

func accountStatusWithin(t *testing.T, manager *Manager) AccountManagementStatus {
	t.Helper()
	done := make(chan AccountManagementStatus, 1)
	go func() { done <- manager.GetAccountManagementStatus() }()
	select {
	case status := <-done:
		return status
	case <-time.After(2 * time.Second):
		t.Fatal("account status read did not return during the held network wait")
		return AccountManagementStatus{}
	}
}

func TestAccountRemoteCommitDoesNotDependOnCurrentSelection(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	for _, change := range []string{"account", "wallet"} {
		t.Run(change, func(t *testing.T) {
			manager, _, _, _ := accountTTLNoopFixture(t)
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
			catalog, err := manager.accountManagedDataCatalog()
			if err != nil {
				t.Fatal(err)
			}
			entry, err := accountEntry(manager.accountProfile.AccountID, "wallets", AccountManagedDataGlobalScope)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := manager.exportAccountPayload(entry, catalog)
			if err != nil {
				t.Fatal(err)
			}
			defer zeroBytes(payload)
			state, err := account.DecodeManagedState(payload)
			if err != nil {
				t.Fatal(err)
			}
			if change == "account" {
				if err := manager.SwitchAccount(1); err != nil {
					t.Fatal(err)
				}
			} else if err := manager.SwitchWallet(childID, "password"); err != nil {
				t.Fatal(err)
			}
			walletID, accountIndex := manager.status.CurrentWallet, manager.status.CurrentAccount
			if err := manager.importAccountWallets(state); err != nil {
				t.Fatalf("selection-only change invalidated root-account import: %v", err)
			}
			if manager.status.CurrentWallet != walletID || manager.status.CurrentAccount != accountIndex {
				t.Fatal("root-account import replaced current wallet/account selection")
			}
		})
	}
}

func TestAccountRemoteCommitRejectsChangedManagedDataGeneration(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	manager, _, _, _ := accountTTLNoopFixture(t)
	manager.mutex.RLock()
	secret := append([]byte(nil), manager.accountSecret...)
	manager.mutex.RUnlock()
	defer zeroBytes(secret)
	accountID := manager.accountProfile.AccountID
	entry, err := accountEntry(accountID, "wallets", AccountManagedDataGlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := manager.accountManagedDataCatalog()
	if err != nil {
		t.Fatal(err)
	}
	before, err := manager.readAccountLocal(accountID, entry.Provider, entry.Scope)
	if err != nil || before == nil {
		t.Fatalf("wallet module baseline: %v", err)
	}
	payload, err := openAccountPayload(secret, accountID, _chain, entry, before.Value)
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(payload)
	if err := manager.EnsureAccount(manager.status.CurrentWallet, 1, "New local account", ""); err != nil {
		t.Fatal(err)
	}
	changed, err := manager.readAccountLocal(accountID, entry.Provider, entry.Scope)
	if err != nil || changed == nil || changed.Generation <= before.Generation {
		t.Fatalf("catalog edit did not stage a module generation: %v", err)
	}
	err = manager.importAccountValue(entry, catalog, before, nil, before.Value, payload, false)
	if !errors.Is(err, errAccountSnapshotChanged) {
		t.Fatalf("changed module generation accepted stale import: %v", err)
	}
	after, err := manager.readAccountLocal(accountID, entry.Provider, entry.Scope)
	if err != nil || !reflect.DeepEqual(changed, after) {
		t.Fatalf("stale import replaced newer module state: %v", err)
	}
}

func TestAccountRemoteCommitSurvivesPWAReauthentication(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	manager, _, _, _ := accountTTLNoopFixture(t)
	manager.mutex.RLock()
	secret := append([]byte(nil), manager.accountSecret...)
	manager.mutex.RUnlock()
	defer zeroBytes(secret)
	catalog, err := manager.accountManagedDataCatalog()
	if err != nil {
		t.Fatal(err)
	}
	entry, err := accountEntry(manager.accountProfile.AccountID, "wallets", AccountManagedDataGlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := manager.exportAccountPayload(entry, catalog)
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(payload)
	state, err := account.DecodeManagedState(payload)
	if err != nil {
		t.Fatal(err)
	}
	wallet := manager.wallet
	generation := manager.accountGeneration
	if _, err := manager.UnlockWallet("incorrect-password"); err == nil {
		t.Fatal("incorrect password authenticated")
	}
	if manager.accountGeneration != generation || manager.wallet != wallet {
		t.Fatal("failed authentication invalidated the running wallet session")
	}
	if _, err := manager.UnlockWallet("password"); err != nil {
		t.Fatal(err)
	}
	if manager.wallet != wallet || manager.accountGeneration != generation {
		t.Fatal("screen reauthentication replaced the wallet runtime or session")
	}
	if err := manager.importAccountWallets(state); err != nil {
		t.Fatalf("PWA screen reauthentication rejected a valid wallet module import: %v", err)
	}
	if manager.wallet != wallet {
		t.Fatal("wallet module import replaced the running wallet runtime")
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
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	db := &finalBoundaryReadDB{}
	manager, _, _, _ := accountTTLNoopFixture(t, db.attach)
	manager.accountBackgroundWG.Wait()
	if err := manager.RegisterAccountManagedDataProvider(&accountManagedActiveDataProviderStub{
		accountManagedDataProviderStub: accountManagedDataProviderStub{id: "latency-active"},
	}); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var gateOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	// Active import reads the synchronized local mailbox cache. Gate that real
	// read after catalog capture, rather than an unrelated config HTTP request.
	db.arm(func([]byte) error {
		stack := make([]byte, 16<<10)
		if strings.Contains(string(stack[:runtime.Stack(stack, false)]), ".prepareAccountManagedActiveDataImport(") {
			gateOnce.Do(func() { close(started); <-release })
		}
		return nil
	})
	done := make(chan error, 1)
	go func() { done <- manager.importAccountManagedActiveDataForSync(false) }()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("mailbox import ended before cache gate: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("mailbox read did not reach cache")
	}
	editDone := make(chan error, 1)
	go func() { editDone <- manager.EnsureAccount(manager.status.CurrentWallet, 1, "Savings", "") }()
	select {
	case err := <-editDone:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(2 * time.Second):
		t.Error("catalog edit blocked behind mailbox cache read")
	}
	unblock()
	select {
	case err := <-done:
		if !errors.Is(err, errAccountSnapshotChanged) {
			t.Fatalf("changed mailbox catalog: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mailbox import did not finish")
	}
}

func TestAccountDiscoveryReadOnly(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	manager := newAccountManagementAutoTestManager(t)
	base := newRGB11MemoryDKVSHTTP()
	remote := &blockedAccountConfigHTTP{rgb11MemoryDKVSHTTP: base,
		started: make(chan struct{}), release: make(chan struct{})}
	configureRGB11DKVSTestManager(manager, remote)
	if _, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	before := manager.GetWalletCatalog()
	if err := manager.StartDKVSSync(); err != nil {
		t.Fatal(err)
	}
	defer manager.StopDKVSSync()
	var once sync.Once
	release := func() { once.Do(func() { close(remote.release) }) }
	defer release()
	select {
	case <-remote.started:
	case <-time.After(5 * time.Second):
		t.Fatal("discovery did not reach online configuration")
	}
	if err := accountOperationWithin(t, "wallet reauthentication", func() error {
		_, err := manager.UnlockWallet("password")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := accountOperationWithin(t, "RGB state read", func() error {
		_, err := manager.GetRGB11State()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := accountOperationWithin(t, "rejected local edit", func() error {
		return manager.UpdateWalletName(manager.GetCurrentWalletId(), "unknown must not write")
	}); err == nil {
		t.Fatal("unknown discovery permitted a local edit")
	} else if !errors.Is(err, ErrDKVSPathNotSynced) {
		t.Fatalf("unknown discovery edit error=%v", err)
	}
	if !reflect.DeepEqual(before, manager.GetWalletCatalog()) {
		t.Fatal("rejected discovery edit changed local data")
	}
	if status := accountStatusWithin(t, manager); status.LocalOnly || status.DKVSWriteAllowed {
		t.Fatalf("unknown discovery was called inactive: %+v", status)
	}
	release()
	manager.StopDKVSSync()
	manager.SetDKVSHttpClient(base)
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	if !manager.GetAccountManagementStatus().LocalOnly {
		t.Fatal("verified absence did not establish local-only mode")
	}
	if err := manager.UpdateWalletName(manager.GetCurrentWalletId(), "verified inactive"); err != nil {
		t.Fatal(err)
	}
	if len(snapshotRGB11MemoryRecords(base)) != 0 {
		t.Fatal("inactive account uploaded a private backup")
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
	unlockDone := make(chan error, 1)
	stateDone := make(chan error, 1)
	go func() { _, err := manager.UnlockWallet("password"); unlockDone <- err }()
	go func() { _, err := manager.GetRGB11State(); stateDone <- err }()
	status := accountStatusWithin(t, manager)
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
