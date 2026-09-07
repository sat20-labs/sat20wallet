package wallet

import (
	"sync"
	"testing"
	"time"
)

type blockingAccountRootWrapperStore struct {
	*memoryAccountRootWrapperStore
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (s *blockingAccountRootWrapperStore) WaitReady(keys ...string) error {
	s.once.Do(func() {
		close(s.started)
		<-s.release
	})
	return s.memoryAccountRootWrapperStore.WaitReady(keys...)
}

func TestAccountRootWrapperSyncRevalidatesPaidActivationAfterBlockedRefresh(t *testing.T) {
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

	done := make(chan error, 1)
	go func() {
		done <- manager.syncAccountRootWrapper(store)
	}()
	<-store.started

	// The network refresh is blocked, but the coordinator mutex must remain
	// available to unrelated local state changes.
	if !manager.accountSyncMu.TryLock() {
		close(store.release)
		t.Fatal("account coordinator mutex is held across remote refresh")
	}
	manager.accountSyncMu.Unlock()

	manager.mutex.Lock()
	manager.accountProfile.StorageMode = AccountStoragePaid
	manager.accountProfile.RecordTTL = 0
	manager.accountProfile.AutopayContract = "autopay-contract"
	manager.accountProfile.RecoveryConfigured = true
	manager.bumpAccountGenerationLocked()
	manager.mutex.Unlock()
	close(store.release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("root-wrapper sync did not converge after the blocked refresh resumed")
	}
	root, err := manager.accountManagementRootWallet()
	if err != nil {
		t.Fatal(err)
	}
	key, err := accountRootWrapperKey(root)
	if err != nil {
		t.Fatal(err)
	}
	value, err := memoryStore.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	manager.mutex.RLock()
	profile := *manager.accountProfile
	manager.mutex.RUnlock()
	if !accountRecordMatchesStorage(value, &profile) {
		t.Fatal("wrapper sync published the stale temporary policy after paid activation")
	}
	if value.record == nil {
		t.Fatal("paid wrapper fixture has no signed record")
	}
}
