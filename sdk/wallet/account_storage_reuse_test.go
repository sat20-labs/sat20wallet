package wallet

import (
	"errors"
	"testing"
)

func TestReusePaidStorageAuthorizationNeverFundsNotReadyDelegate(t *testing.T) {
	defaults, state := accountAutopayReadyFixture()
	defaults.Enabled = true
	defaults.AutopayMinAmountPerBlock = "1"
	defaults.FullRecordFeePerBlock = "0.1"
	location := AccountIndexerLocation{Scheme: "https", Host: "indexer.test", Proxy: "satsnet/testnet"}
	delegate := state.Delegates["payer"]
	delegate.AmountPerBlock = "10"
	delegate.Balance = "10"
	state.Delegates["payer"] = delegate

	authorization, err := accountPaidStorageAuthorizationFromState(
		location, defaults, accountMinimumRecordCount, "payer", state)
	if err != nil {
		t.Fatalf("ready delegate was not reusable: %v", err)
	}
	if authorization.TransactionID != "" {
		t.Fatalf("reuse unexpectedly produced a funding transaction: %q", authorization.TransactionID)
	}

	delegate = state.Delegates["payer"]
	delegate.Balance = "0"
	state.Delegates["payer"] = delegate
	authorization, err = accountPaidStorageAuthorizationFromState(
		location, defaults, accountMinimumRecordCount, "payer", state)
	if !errors.Is(err, ErrAccountPaidStorageNotReusable) {
		t.Fatalf("not-ready delegate should require explicit funding confirmation: %v", err)
	}
	if authorization != nil {
		t.Fatal("not-ready delegate returned a reusable authorization")
	}
}

func TestAccountStorageAuthorizationOwnedByRootSDKSession(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	// Obtain the authorization through the real SDK endpoint/policy path.
	// A fabricated grant with no location no longer satisfies root binding.
	authorization, err := manager.ConfirmAccountStorage(AccountStorageTemporary, 0)
	if err != nil {
		t.Fatal(err)
	}
	if authorization.ID == "" || authorization.ID == AccountStorageTemporary {
		t.Fatalf("SDK did not assign an opaque authorization id: %q", authorization.ID)
	}
	manager.SwitchAccount(1)
	pending, err := manager.PendingAccountStorageAuthorization()
	if err != nil {
		t.Fatalf("current account selection invalidated root authorization: %v", err)
	}
	if pending.ID != authorization.ID || pending.Mode != AccountStorageTemporary {
		t.Fatalf("pending authorization=%+v want=%+v", pending, authorization)
	}
	manager.CancelPendingAccountStorageAuthorization()
	if _, err := manager.PendingAccountStorageAuthorization(); !errors.Is(err, ErrAccountStorageAuthorizationMissing) {
		t.Fatalf("cancelled authorization remained available: %v", err)
	}
}

func TestPendingAccountStorageAuthorizationIsRootBoundNotSelectionBound(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

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

	// The subject here is UI selection, not replacing the configured indexer.
	stored, err := manager.ConfirmAccountStorage(AccountStorageTemporary, 0)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID == "" {
		t.Fatal("SDK storage authorization has no runtime handle")
	}

	manager.SwitchAccount(1)
	if current, err := manager.PendingAccountStorageAuthorization(); err != nil || current.ID != stored.ID {
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
	manager, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
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

func TestAccountStorageRuntimeStopRejectsAuthorizationWork(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager, _, secret := buildRootWrapperSource(t)
	defer zeroBytes(secret)
	if _, err := manager.ConfirmAccountStorage(AccountStorageTemporary, 0); err != nil {
		t.Fatal(err)
	}

	manager.Stop()

	if pending, err := manager.PendingAccountStorageAuthorization(); pending != nil ||
		!errors.Is(err, ErrAccountStorageRuntimeStopped) {
		t.Fatalf("stopped runtime exposed authorization: pending=%+v err=%v", pending, err)
	}
	called := false
	err := manager.UseAccountStorageAuthorization(AccountStoragePurposeRecovery,
		func(*AccountStorageAuthorization) error {
			called = true
			return nil
		})
	if !errors.Is(err, ErrAccountStorageRuntimeStopped) || called {
		t.Fatalf("stopped runtime used authorization: called=%v err=%v", called, err)
	}
	if grant, err := manager.ConfirmAccountStorage(AccountStorageTemporary, 0); grant != nil ||
		!errors.Is(err, ErrAccountStorageRuntimeStopped) {
		t.Fatalf("stopped runtime minted authorization: grant=%+v err=%v", grant, err)
	}
}
