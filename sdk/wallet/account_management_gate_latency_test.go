package wallet

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

type bindingSyncBarrier struct {
	*rgb11MemoryDKVSHTTP
	armed   atomic.Bool
	entered chan context.Context
	release chan struct{}
}

func (h *bindingSyncBarrier) SendDKVSPostContext(ctx context.Context, path string, body []byte) ([]byte, error) {
	if path == "/v3/dkvs/active/sync" && h.armed.CompareAndSwap(true, false) {
		h.entered <- ctx
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-h.release:
		}
	}
	return h.rgb11MemoryDKVSHTTP.SendDKVSPostContext(ctx, path, body)
}

func TestCoreBindDuringDiscovery(t *testing.T) {
	for _, scenario := range []string{"discovery completes", "wallet closes"} {
		t.Run(scenario, func(t *testing.T) {
			manager, remote, service := newInactiveAccountGroup(t, accountRootWrapperTestMnemonic, "password")
			node := NewNode(service, "message.test", SERVER_NODE, service.CoreNodePubKey(), service.CoreNodePubKey())
			require.NoError(t, manager.SetAccountServiceNode(node))
			barrier := &bindingSyncBarrier{rgb11MemoryDKVSHTTP: remote,
				entered: make(chan context.Context, 1), release: make(chan struct{})}
			manager.SetDKVSHttpClient(barrier)

			atCompletion, finish := make(chan struct{}), make(chan struct{})
			var allowFinish, allowSync sync.Once
			defer allowSync.Do(func() { close(barrier.release) })
			defer allowFinish.Do(func() { close(finish) })
			var reported atomic.Bool
			manager.dkvs.setStatusCallback(func() {
				if reported.CompareAndSwap(false, true) {
					close(atCompletion)
					<-finish
				}
			})
			require.NoError(t, manager.startAccountDiscovery())
			select {
			case <-atCompletion:
			case <-time.After(5 * time.Second):
				t.Fatal("discovery did not reach completion")
			}
			manager.dkvs.mu.Lock()
			discoveryDone := manager.dkvs.done
			manager.dkvs.mu.Unlock()
			require.NotNil(t, discoveryDone)

			barrier.armed.Store(true)
			result := make(chan error, 1)
			go func() { result <- manager.BindAccountToCurrentCoreNode() }()
			var request context.Context
			select {
			case request = <-barrier.entered:
			case err := <-result:
				t.Fatalf("binding ended before its initial sync: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("binding did not reach initial sync")
			}
			allowFinish.Do(func() { close(finish) })
			select {
			case <-discoveryDone:
			case <-time.After(5 * time.Second):
				t.Fatal("binding blocked discovery completion")
			}
			survived := request.Err()
			if scenario == "wallet closes" {
				manager.Close()
			} else {
				allowSync.Do(func() { close(barrier.release) })
			}
			select {
			case err := <-result:
				if scenario == "wallet closes" {
					require.ErrorIs(t, err, context.Canceled)
					require.ErrorIs(t, request.Err(), context.Canceled)
				} else {
					require.NoError(t, err)
					store, err := manager.accountDKVSStore()
					require.NoError(t, err)
					root, err := manager.accountManagementRootWallet()
					require.NoError(t, err)
					key, err := dkvsindexer.AccountMappingKey(GetChainParam().Name, root.GetAddress())
					require.NoError(t, err)
					record, err := store.Get(key)
					require.NoError(t, err)
					_, _, descriptor, err := dkvsindexer.ValidateAccountMappingBindingRecord(record.record)
					require.NoError(t, err)
					require.Equal(t, remote.endpointID, descriptor.CoreNodeID)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("binding did not return")
			}
			require.NoError(t, survived, "ending discovery must leave the binding request live")
		})
	}
}

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
			entry, err := accountEntry(nil, manager.accountProfile.AccountID, "wallets", AccountManagedDataGlobalScope)
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
	entry, err := accountEntry(nil, accountID, "wallets", AccountManagedDataGlobalScope)
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
	entry, err := accountEntry(nil, manager.accountProfile.AccountID, "wallets", AccountManagedDataGlobalScope)
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
	durable := accountPersistenceReviewCatalog(t, manager.db)
	walletID := manager.GetCurrentWalletId()
	defer manager.StopDKVSSync()
	var once sync.Once
	release := func() { once.Do(func() { close(remote.release) }) }
	defer release()
	// Unactivated wallets discover only at an explicit entry/retry. Discovery
	// itself remains read-only; it does not grant private-backup write access.
	if _, err := manager.RefreshAccount(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-remote.started:
	case <-time.After(5 * time.Second):
		t.Fatal("discovery did not reach online configuration")
	}
	manager.dkvs.mu.Lock()
	done := manager.dkvs.done
	manager.dkvs.mu.Unlock()
	if done == nil {
		t.Fatal("finite discovery has no active lifetime")
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
	if !reflect.DeepEqual(before, manager.GetWalletCatalog()) {
		t.Fatal("pending discovery changed the readable local catalog")
	}
	if err := accountOperationWithin(t, "local edit during discovery", func() error {
		return manager.UpdateWalletName(walletID, "pending discovery")
	}); !errors.Is(err, ErrRootAccountDiscoveryPending) {
		t.Fatalf("edit before discovery completed: %v", err)
	}
	if !reflect.DeepEqual(before, manager.GetWalletCatalog()) ||
		!reflect.DeepEqual(durable, accountPersistenceReviewCatalog(t, manager.db)) {
		t.Fatal("rejected rename changed the catalog")
	}
	if status := accountStatusWithin(t, manager); status.LocalOnly || status.RecoveryConfigured ||
		status.WriteAllowed || status.DKVSWriteAllowed || status.DiscoveryStatus != "unknown" {
		t.Fatal("unconfirmed discovery must keep the account read-only")
	}
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("finite failed discovery did not finish")
	}
	if status := accountStatusWithin(t, manager); status.LocalOnly || status.RecoveryConfigured ||
		status.WriteAllowed || status.DKVSWriteAllowed || status.DiscoveryStatus != "unknown" {
		t.Fatal("failed discovery changed local access or claimed a trusted remote result")
	}
	if !reflect.DeepEqual(before, manager.GetWalletCatalog()) ||
		!reflect.DeepEqual(durable, accountPersistenceReviewCatalog(t, manager.db)) {
		t.Fatal("failed discovery changed the persisted local catalog")
	}
	manager.StopDKVSSync()
	manager.SetDKVSHttpClient(base)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := manager.RecoverAccountManagementFromCurrentWallet(ctx, "password"); !errors.Is(err, ErrRootAccountNotFound) {
		t.Fatalf("online discovery must establish trusted absence: %v", err)
	}
	if status := manager.GetAccountManagementStatus(); !status.LocalOnly || status.RecoveryConfigured ||
		status.DKVSWriteAllowed || status.DiscoveryStatus != "inactive" {
		t.Fatal("verified absence must remain local-only without enabling recovery backup")
	}
	if err := manager.UpdateWalletName(manager.GetCurrentWalletId(), "verified inactive"); err != nil {
		t.Fatal(err)
	}
	for i := range before {
		if before[i].ID == walletID {
			before[i].Name = "verified inactive"
		}
	}
	stored := durable[walletID]
	stored.Name = "verified inactive"
	if stored.SPEnabled == nil {
		stored.SPEnabled = make(map[uint32]bool)
	}
	durable[walletID] = stored
	if !reflect.DeepEqual(before, manager.GetWalletCatalog()) ||
		!reflect.DeepEqual(durable, accountPersistenceReviewCatalog(t, manager.db)) {
		t.Fatal("verified local rename did not persist exactly the requested catalog change")
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
