package wallet

import (
	"context"
	"errors"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

// Create the marker through a real background recovery import which fails at
// the provider boundary, rather than writing a fabricated marker into the DB.
// Activation must retain its fail-closed check; setup should detect the same
// incomplete import before returning recovery material that cannot be activated.
func TestAccountRecoverySetupRejectsIncompleteImportBeforeCreatingPackage(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	remote := newRGB11MemoryDKVSHTTP()
	source, sourceProvider := managedImportTestSource(t, remote)
	target, targetProvider := managedImportTestManager(t, remote)
	if err := restoreManagedImportTestWallet(t, target, source); err != nil {
		t.Fatal(err)
	}
	sourceProvider.payloads[0].Payload = []byte("new remote provider state")
	if err := source.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	importFailure := errors.New("test provider interrupted during import")
	targetProvider.importErr = importFailure
	if err := target.SyncAccountManagementState(context.Background()); !errors.Is(err, importFailure) {
		t.Fatalf("expected provider import failure: %v", err)
	}
	if err := target.checkAccountManagedDataImport(); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
		t.Fatalf("interrupted import did not leave protection: %v", err)
	}
	// A new manager proves the marker survives restart. The transient provider
	// fault is gone; neither a retry nor package creation may erase protection.
	restarted, _ := managedImportTestManager(t, remote)
	restarted.db = target.db
	if err := restarted.loadAccountManagementProfileLocked(); err != nil {
		t.Fatal(err)
	}
	if err := restarted.unlockAccountManagementLocked("password"); err != nil {
		t.Fatal(err)
	}
	if err := restarted.SyncAccountManagementState(context.Background()); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
		t.Fatalf("restart lost incomplete import protection: %v", err)
	}
	// Match the createRecovery preamble on the live, partially imported wallet.
	backup, err := target.ExportAccountBackupForPWA("password", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clearAccountBackup(&backup)
	if err := target.InitializeAccountManagement("password"); err != nil {
		t.Fatal(err)
	}
	bootstrap, err := account.RootBootstrapBackup(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer clearAccountBackup(&bootstrap)
	questions := []account.QuestionAnswer{
		{Question: account.KnowledgeQuestion{ID: "one", Prompt: "one"}, Answer: "answer one", Confirmation: "answer one"},
		{Question: account.KnowledgeQuestion{ID: "two", Prompt: "two"}, Answer: "answer two", Confirmation: "answer two"},
		{Question: account.KnowledgeQuestion{ID: "three", Prompt: "three"}, Answer: "answer three", Confirmation: "answer three"},
	}
	pkg, err := target.CreateAccountRecoveryPackage(account.CreateOptions{
		AccountID: target.accountProfile.AccountID, Backup: bootstrap,
		RecoveryMode: account.RecoveryMode2Of2, Questions: questions,
	})
	if !errors.Is(err, ErrAccountManagedDataImportIncomplete) || pkg != nil {
		t.Errorf("setup did not reject incomplete import: package_created=%t error=%v", pkg != nil, err)
	}
	if err := target.ActivateAccountManagement(source.accountSecret, "password",
		AccountStorageAuthorization{Mode: AccountStorageTemporary}, account.Locator{}, ""); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
		t.Fatalf("activation must retain import protection: %v", err)
	}
	if err := target.checkAccountManagedDataImport(); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
		t.Fatal("recovery setup cleared the import marker")
	}
}
