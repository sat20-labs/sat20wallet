package wallet

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountDeleteFlushFailurePreservesCatalogAndPending(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, _, _ := reviewAccountDevices(t)
	childID, _, err := manager.CreateWallet("password")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SyncAccountManagementState(nil); err != nil {
		t.Fatal(err)
	}
	// The production DB is immutable. Wait for sync cleanup before the fixture
	// replaces it for fault injection, so no background reader observes a swap.
	manager.accountBackgroundWG.Wait()
	before := manager.GetWalletCatalog()
	walletID, accountIndex := manager.GetCurrentWalletId(), manager.GetCurrentAccountId()
	database := manager.db
	manager.db = &failFlushDB{KVDB: database}
	err = manager.DeleteWallet(childID)
	manager.db = database
	if err == nil {
		t.Fatal("delete ignored its transaction flush failure")
	}
	if !reflect.DeepEqual(before, manager.GetWalletCatalog()) ||
		manager.GetCurrentWalletId() != walletID || manager.GetCurrentAccountId() != accountIndex ||
		manager.GetAccountManagementStatus().PendingChanges != 0 {
		t.Fatal("failed transaction changed catalog, selected identity or pending deletion")
	}
	if _, err := loadWallet(database, childID); err != nil {
		t.Fatalf("failed transaction removed the durable wallet: %v", err)
	}
	status := loadStatusFromDB(database)
	if status.CurrentWallet != walletID || status.CurrentAccount != accountIndex {
		t.Fatal("failed transaction changed the durable selection")
	}
	if err := manager.SyncAccountManagementState(nil); err != nil {
		t.Fatal(err)
	}
	state := managedImportRecoveryValue(t, manager).State
	for _, item := range before {
		wallet := findManagedWallet(&state, item.Fingerprint)
		if wallet == nil || wallet.Deleted {
			t.Fatal("failed transaction published a remote deletion")
		}
	}
}

func TestAccountRestoreRetryRequiresMatchingSnapshot(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	for _, restart := range []bool{false, true} {
		name := "same-manager"
		if restart {
			name = "cold-manager"
		}
		t.Run(name, func(t *testing.T) {
			remote := newRGB11MemoryDKVSHTTP()
			source, sourceProvider, _ := newImportSource(t, remote)
			target, provider, closeTarget := newImportGroup(t, remote)
			failure := errors.New("transient provider failure")
			provider.importErr = failure
			original := managedImportRecoveryValue(t, source)
			require.NoError(t, restoreImportGroup(t, target, source), "healthy wallets survive a module import failure")
			require.Positive(t, provider.imports, "the actual provider failure must be reached")
			originalCopy := importModuleTarget(t, target, provider.id, []byte("A"))
			before := target.GetWalletCatalog()
			if restart {
				cfg := target.cfg
				closeTarget()
				target, provider, _ = openImportGroup(t, cfg, remote)
				provider.importErr = failure
				_, err := target.UnlockWallet("password")
				require.NoError(t, err)
			}
			// A newer remote module cannot replace the unfinished target A.
			// Its record may become the latest baseline while A still awaits Import.
			pendingTarget := func() {
				t.Helper()
				assertManagedImportModuleProtected(t, target, provider.id)
				value, err := target.readAccountLocal(target.accountProfile.AccountID, provider.id, AccountManagedDataGlobalScope)
				require.NoError(t, err)
				require.NotNil(t, value)
				require.Equal(t, originalCopy.Value, value.Value, "retry replaced the authenticated unfinished module target")
			}
			pendingTarget()
			newPayload := []byte("newer remote provider state")
			stageManagedImportTestPayload(t, source, sourceProvider, newPayload)
			require.NoError(t, source.SyncAccountManagementState(context.Background()))
			remoteBefore := snapshotRGB11MemoryRecords(remote)
			imports := provider.imports
			require.NoError(t, restoreImportGroup(t, target, source), "a newer wallet snapshot must not discard the failed module target")
			require.Greater(t, provider.imports, imports)
			pendingTarget()
			require.Equal(t, before, target.GetWalletCatalog())
			require.Equal(t, remoteBefore, snapshotRGB11MemoryRecords(remote))
			locator, location := importGroupMaterial(t, source)
			options := AccountManagementRestoreOptions{Location: location, StorageMode: AccountStorageTemporary,
				RecordTTL: testRGB11FreeLocalTTL, PublicLocator: source.GetAccountManagementStatus().PublicLocator}
			imports = provider.imports
			_, err := target.RestoreAccountManagementState(original, source.accountSecret, "wrong password", locator, options)
			require.Error(t, err, "retry accepted an invalid local password")
			require.Equal(t, imports, provider.imports, "invalid authentication reached the provider")
			pendingTarget()
			err = target.SyncAccountManagementState(nil)
			require.ErrorIs(t, err, failure)
			require.True(t, accountModuleOnly(err))
			pendingTarget()
			require.Equal(t, remoteBefore, snapshotRGB11MemoryRecords(remote))
			provider.importErr = nil
			attempts := len(provider.imported)
			results, err := target.RestoreAccountManagementState(original, source.accountSecret, "password", locator, options)
			require.NoError(t, err)
			require.Len(t, results, len(before))
			require.Equal(t, before, target.GetWalletCatalog(), "retry replaced wallet identities")
			replayed := provider.imported[attempts:]
			require.NotEmpty(t, replayed)
			require.Len(t, replayed[0], 1)
			require.Equal(t, []byte("A"), replayed[0][0].Payload, "the original authenticated target must finish before adopting the newer remote module")
			for _, attempt := range replayed {
				require.Len(t, attempt, 1)
				if string(attempt[0].Payload) != "A" {
					require.Equal(t, newPayload, attempt[0].Payload)
				}
			}
			assertImportComplete(t, target, provider, newPayload)
			require.Equal(t, remoteBefore, snapshotRGB11MemoryRecords(remote), "resume must not republish A over the newer remote module")
		})
	}
}
