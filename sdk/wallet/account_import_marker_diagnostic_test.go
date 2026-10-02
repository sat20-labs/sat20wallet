package wallet

import (
	"context"
	"errors"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

func TestAccountManagedImportMarkerRecordsRemoteApplyFailure(t *testing.T) {
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
	importFailure := errors.New("diagnostic provider import failure")
	targetProvider.importErr = importFailure
	if err := target.SyncAccountManagementState(context.Background()); !errors.Is(err, importFailure) {
		t.Fatalf("expected provider import failure: %v", err)
	}

	marker, err := target.readAccountManagedDataImportMarker()
	if err != nil {
		t.Fatal(err)
	}
	if marker == nil {
		t.Fatal("failed remote apply did not leave an import marker")
	}
	if marker.Origin != accountManagedImportOriginRemoteApply {
		t.Fatalf("origin=%q want %q", marker.Origin, accountManagedImportOriginRemoteApply)
	}
	if marker.Stage != accountManagedImportStageProviderImport {
		t.Fatalf("stage=%q want %q", marker.Stage, accountManagedImportStageProviderImport)
	}
	if marker.TargetStateRevision == 0 || marker.TargetDataRevision == 0 {
		t.Fatalf("marker lacks target revisions: %+v", marker)
	}
	if marker.TargetStateHash == "" || marker.TargetDataHash == "" {
		t.Fatalf("marker lacks target hashes: %+v", marker)
	}
	if marker.CreatedAtUnix == 0 || marker.UpdatedAtUnix < marker.CreatedAtUnix {
		t.Fatalf("marker lacks usable timestamps: %+v", marker)
	}
}

func TestLegacyAccountManagedImportMarkerRemainsFailClosed(t *testing.T) {
	manager, _ := managedImportTestManager(t, newRGB11MemoryDKVSHTTP())
	if err := manager.db.Write(accountManagedDataImportKey(), []byte{1}); err != nil {
		t.Fatal(err)
	}
	marker, err := manager.readAccountManagedDataImportMarker()
	if err != nil {
		t.Fatal(err)
	}
	if marker == nil || marker.Origin != accountManagedImportOriginUnknown ||
		marker.Stage != accountManagedImportStageUnknown {
		t.Fatalf("legacy marker must decode as unknown, got %+v", marker)
	}
	if err := manager.checkAccountManagedDataImport(); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
		t.Fatalf("legacy marker no longer fails closed: %v", err)
	}
}

func TestAccountManagedImportMarkerRecordsExplicitRestoreFailure(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	remote := newRGB11MemoryDKVSHTTP()
	source, _ := managedImportTestSource(t, remote)
	target, provider := managedImportTestManager(t, remote)
	importFailure := errors.New("restore provider import failure")
	provider.importErr = importFailure

	_, err := target.RestoreAccountManagementState(
		managedImportRecoveryValue(t, source),
		source.accountSecret,
		"password",
		account.Locator{AccountID: source.accountProfile.AccountID},
		AccountManagementRestoreOptions{
			StorageMode: AccountStorageTemporary,
			RecordTTL: testRGB11FreeLocalTTL,
		},
	)
	if !errors.Is(err, importFailure) {
		t.Fatalf("expected provider import failure: %v", err)
	}
	marker, err := target.readAccountManagedDataImportMarker()
	if err != nil {
		t.Fatal(err)
	}
	if marker == nil {
		t.Fatal("failed restore did not leave an import marker")
	}
	if marker.Origin != accountManagedImportOriginRestore {
		t.Fatalf("origin=%q want %q", marker.Origin, accountManagedImportOriginRestore)
	}
	if marker.Stage != accountManagedImportStageProviderImport {
		t.Fatalf("stage=%q want %q", marker.Stage, accountManagedImportStageProviderImport)
	}
}
