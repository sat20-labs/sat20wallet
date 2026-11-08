package wallet

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

// Recovery setup concerns the healthy wallet identity. A failed business-module
// import stays protected independently, including through setup and a cold reopen.
func TestAccountRecoverySetupPreservesIncompleteModuleProtection(t *testing.T) {
	remote := newRGB11MemoryDKVSHTTP()
	source, sourceProvider, _ := newImportSource(t, remote)
	target, targetProvider, closeTarget := newImportGroup(t, remote)
	if err := restoreImportGroup(t, target, source); err != nil {
		t.Fatal(err)
	}
	stageManagedImportTestPayload(t, source, sourceProvider, []byte("new remote provider state"))
	if err := source.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	beforeRemote := snapshotRGB11MemoryRecords(remote)
	importFailure := errors.New("test provider interrupted during import")
	targetProvider.importErr = importFailure
	if err := target.SyncAccountManagementState(context.Background()); !errors.Is(err, importFailure) || !accountModuleOnly(err) {
		t.Fatalf("expected module-scoped provider import failure: %v", err)
	}
	assertManagedImportModuleProtected(t, target, targetProvider.id)
	localBefore, err := target.readAccountLocal(target.accountProfile.AccountID, targetProvider.id, AccountManagedDataGlobalScope)
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil || pkg == nil {
		t.Fatalf("unrelated module failure blocked wallet recovery setup: %v", err)
	}
	assertManagedImportModuleProtected(t, target, targetProvider.id)
	localAfter, err := target.readAccountLocal(target.accountProfile.AccountID, targetProvider.id, AccountManagedDataGlobalScope)
	if err != nil || !reflect.DeepEqual(localBefore, localAfter) {
		t.Fatalf("recovery setup altered an incomplete module target: %v", err)
	}
	if !bytes.Equal(target.accountSecret, source.accountSecret) {
		t.Fatal("recovery setup replaced the account secret")
	}

	cfg := target.cfg
	closeTarget()
	target, targetProvider, _ = openImportGroup(t, cfg, remote)
	if _, err := target.UnlockWallet("password"); err != nil {
		t.Fatal(err)
	}
	assertManagedImportModuleProtected(t, target, targetProvider.id)
	if err := target.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatalf("healthy reopened device could not resume module import: %v", err)
	}
	if err := target.CheckAccountRestore(targetProvider.id, AccountManagedDataGlobalScope); err != nil {
		t.Fatalf("completed module import remains blocked: %v", err)
	}
	if !reflect.DeepEqual(targetProvider.payloads, sourceProvider.payloads) {
		t.Fatal("resumed provider did not import its committed target")
	}
	if !bytes.Equal(target.accountSecret, source.accountSecret) ||
		target.GetAccountManagementStatus().AccountID != source.GetAccountManagementStatus().AccountID ||
		target.GetAccountManagementStatus().RootFingerprint != source.GetAccountManagementStatus().RootFingerprint {
		t.Fatal("module retry changed the restored account identity")
	}
	expectedBackup, err := source.ExportAccountBackupForPWA("password", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clearAccountBackup(&expectedBackup)
	actualBackup, err := target.ExportAccountBackupForPWA("password", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clearAccountBackup(&actualBackup)
	normalizedExpected, err := account.NormalizeBackup(expectedBackup)
	if err != nil {
		t.Fatal(err)
	}
	defer clearAccountBackup(&normalizedExpected)
	normalizedActual, err := account.NormalizeBackup(actualBackup)
	if err != nil {
		t.Fatal(err)
	}
	defer clearAccountBackup(&normalizedActual)
	if !reflect.DeepEqual(normalizedExpected, normalizedActual) {
		t.Fatal("resumed full wallet backup differs from the source")
	}
	if !reflect.DeepEqual(beforeRemote, snapshotRGB11MemoryRecords(remote)) {
		t.Fatal("setup or resumed import overwrote remote records")
	}
}
