package wallet

import (
	"errors"
	"testing"
)

func TestPendingAccountStorageAuthorizationIsRootBoundNotSelectionBound(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

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

	// The subject here is UI selection, not replacing the configured indexer.
	stored, err := manager.ConfirmAccountStorage(AccountStorageTemporary, 0)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID == "" || stored.ID == AccountStorageTemporary {
		t.Fatal("SDK storage authorization has no distinct runtime handle")
	}

	if err := manager.EnsureAccount(manager.GetCurrentWalletId(), 1, "Savings", ""); err != nil {
		t.Fatal(err)
	}
	if err := manager.SwitchAccount(1); err != nil {
		t.Fatal(err)
	}
	if manager.status.CurrentAccount != 1 {
		t.Fatal("selection did not change")
	}
	if current, err := manager.PendingAccountStorageAuthorization(); err != nil || current.ID != stored.ID || current.Mode != AccountStorageTemporary {
		t.Fatalf("account selection invalidated root authorization: %+v %v", current, err)
	}
	if err := manager.SwitchWallet(childID, "password"); err != nil {
		t.Fatal(err)
	}
	if current, err := manager.PendingAccountStorageAuthorization(); err != nil || current.ID != stored.ID {
		t.Fatalf("wallet selection invalidated root authorization: %+v %v", current, err)
	}

	manager.CancelPendingAccountStorageAuthorization()
	if _, err := manager.PendingAccountStorageAuthorization(); !errors.Is(err, ErrAccountStorageAuthorizationMissing) {
		t.Fatalf("cancelled authorization remained available: %v", err)
	}
}

func TestReviewStorageAuthorizationRejectsForeignEndpoint(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, _, _ := newInactiveAccountGroup(t, accountRootWrapperTestMnemonic, "password")
	location, err := manager.AccountIndexerLocation()
	if err != nil {
		t.Fatal(err)
	}
	location.Host = "another-indexer.test"
	grant, err := manager.rememberAccountStorageAuthorization(&AccountStorageAuthorization{
		Mode: AccountStorageTemporary, Location: location,
		Summary: AccountStorageOption{ID: AccountStorageTemporary, Mode: AccountStorageTemporary},
	})
	if grant != nil || !errors.Is(err, ErrAccountStorageAuthorizationMissing) {
		t.Fatalf("foreign endpoint grant was accepted: grant=%+v err=%v", grant, err)
	}
	if pending, err := manager.PendingAccountStorageAuthorization(); pending != nil || !errors.Is(err, ErrAccountStorageAuthorizationMissing) {
		t.Fatalf("failed endpoint validation left a usable authorization: pending=%+v err=%v", pending, err)
	}
}
