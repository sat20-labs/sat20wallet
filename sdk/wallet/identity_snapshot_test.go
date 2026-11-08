package wallet

import "testing"

func TestWalletIdentitySnapshotSurvivesAccountSwitch(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, _, _ := newInactiveAccountGroup(t, "", "password")
	if err := manager.EnsureAccount(manager.GetCurrentWalletId(), 1, "Second", ""); err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.captureWalletIdentity()
	if err != nil {
		t.Fatal(err)
	}
	address := snapshot.GetAddress()
	if err := manager.SwitchAccount(1); err != nil {
		t.Fatal(err)
	}
	if snapshot.GetSubAccount() != 0 || snapshot.GetAddress() != address {
		t.Fatal("captured signing identity changed with the live account")
	}
	if manager.wallet.GetSubAccount() != 1 || manager.wallet.GetAddress() == address {
		t.Fatal("test did not switch the live account")
	}
}

func TestAccountManagementSyncSnapshotCarriesRootSigner(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager := newAccountManagementAutoTestManager(t)
	rootID, _, err := manager.CreateWallet("password")
	if err != nil {
		t.Fatal(err)
	}
	activateSyncFixture(t, manager, manager.http.(*rgb11MemoryDKVSHTTP), "password")
	if err := manager.SyncAccountManagementState(nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.CreateWallet("password"); err != nil {
		t.Fatal(err)
	}
	manager.mutex.Lock()
	snapshot, err := manager.captureAccountManagementSyncSnapshotLocked()
	manager.mutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(snapshot.secret)
	if snapshot.root == nil || snapshot.root.GetId() != rootID || snapshot.root.GetSubAccount() != 0 {
		t.Fatalf("sync root signer = %+v, want root wallet %d account 0", snapshot.root, rootID)
	}
}
