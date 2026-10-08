package wallet

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
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
	defer func() { _chain = oldChain }()
	for _, restart := range []bool{false, true} {
		name := "same-manager"
		if restart {
			name = "cold-manager"
		}
		t.Run(name, func(t *testing.T) {
			remote := newRGB11MemoryDKVSHTTP()
			source, sourceProvider := managedImportTestSource(t, remote)
			target, provider := managedImportTestManager(t, remote)
			failure := errors.New("transient provider failure")
			provider.importErr = failure
			original := managedImportRecoveryValue(t, source)
			if err := restoreManagedImportTestWallet(t, target, source); !errors.Is(err, failure) {
				t.Fatalf("restore did not reach provider failure: %v", err)
			}
			before := target.GetWalletCatalog()
			if restart {
				reopened, reopenedProvider := managedImportTestManager(t, remote)
				reopened.db = target.db
				reopened.status = loadStatusFromDB(target.db)
				var err error
				reopened.walletInfoMap, err = loadAllWalletFromDB(target.db)
				if err != nil {
					t.Fatal(err)
				}
				if err := reopened.loadAccountManagementProfileLocked(); err != nil {
					t.Fatal(err)
				}
				target, provider = reopened, reopenedProvider
			}
			provider.importErr = nil
			// A newer valid snapshot must not authorize overwriting the partial
			// local commit. Only the exact authenticated target can resume it.
			sourceProvider.payloads[0].Payload = []byte("newer remote provider state")
			if err := source.SyncAccountManagementState(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := restoreManagedImportTestWallet(t, target, source); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
				t.Fatalf("different snapshot bypassed the import boundary: %v", err)
			}
			locator := account.Locator{AccountID: source.accountProfile.AccountID}
			options := AccountManagementRestoreOptions{StorageMode: AccountStorageTemporary, RecordTTL: testRGB11FreeLocalTTL}
			if _, err := target.RestoreAccountManagementState(original, source.accountSecret, "wrong password", locator, options); err == nil {
				t.Fatal("retry accepted an invalid local password")
			}
			if err := target.SyncAccountManagementState(nil); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
				t.Fatalf("rejected retry removed upload protection: %v", err)
			}
			results, err := target.RestoreAccountManagementState(original, source.accountSecret, "password", locator, options)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != len(before) || !reflect.DeepEqual(before, target.GetWalletCatalog()) {
				t.Fatal("retry replaced wallet handles or catalog identities")
			}
			if err := target.checkAccountManagedDataImport(); err != nil {
				t.Fatalf("completed retry retained import protection: %v", err)
			}
			if len(provider.payloads) != 1 || string(provider.payloads[0].Payload) != "A" {
				t.Fatal("retry did not import the authenticated original payload")
			}
		})
	}
}
