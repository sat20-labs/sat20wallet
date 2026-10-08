package wallet

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestAccountCatalogCommitFailureIsAtomic(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	for _, operation := range []struct {
		name   string
		mutate func(*Manager, int64) error
	}{
		{"rename", func(m *Manager, id int64) error { return m.UpdateWalletName(id, "Atomic name") }},
		{"ensure-account", func(m *Manager, id int64) error { return m.EnsureAccount(id, 2, "Atomic account", "did:atomic") }},
		{"metadata", func(m *Manager, id int64) error {
			return m.UpdateAccountMetadata(id, 0, "Atomic metadata", "did:atomic")
		}},
		{"create", func(m *Manager, _ int64) error { _, _, err := m.CreateWallet("password"); return err }},
		{"import", func(m *Manager, _ int64) error {
			_, mn, err := NewInteralWallet(GetChainParam())
			if err != nil {
				return err
			}
			_, err = m.ImportWallet(mn, "password")
			return err
		}},
	} {
		for _, faultPoint := range []string{"profile-put", "status-put", "flush"} {
			if faultPoint == "status-put" && operation.name != "create" && operation.name != "import" {
				continue
			}
			t.Run(operation.name+"/"+faultPoint, func(t *testing.T) {
				manager, _, _ := reviewAccountDevices(t)
				manager.accountBackgroundWG.Wait()
				id := manager.GetAccountManagementStatus().RootWalletID
				db := manager.db
				before := manager.GetWalletCatalog()
				durable := accountPersistenceReviewCatalog(t, db)
				status := loadStatusFromDB(db)
				profile := *manager.accountProfile
				generation := manager.accountGeneration
				var putFault *accountPersistenceReviewDB
				if faultPoint == "flush" {
					manager.db = &failFlushDB{KVDB: db}
				} else {
					key := accountManagementProfileKey()
					if faultPoint == "status-put" {
						key = []byte(DB_KEY_STATUS)
					}
					putFault = &accountPersistenceReviewDB{KVDB: db, profileKey: key}
					putFault.armed.Store(true)
					manager.db = putFault
				}
				err := operation.mutate(manager, id)
				manager.db = db
				if err == nil {
					t.Fatal("transaction failure was hidden")
				}
				if putFault != nil && (!errors.Is(err, errAccountPersistenceReview) || putFault.hits.Load() != 1) {
					t.Fatalf("wrong fault: %v", err)
				}
				if !reflect.DeepEqual(before, manager.GetWalletCatalog()) || !reflect.DeepEqual(durable, accountPersistenceReviewCatalog(t, db)) ||
					!reflect.DeepEqual(status, loadStatusFromDB(db)) || !reflect.DeepEqual(profile, *manager.accountProfile) || generation != manager.accountGeneration {
					t.Fatal("failed transaction changed catalog, status, pending overlay or generation")
				}
				if err := operation.mutate(manager, id); err != nil {
					t.Fatalf("healthy retry: %v", err)
				}
				if manager.GetAccountManagementStatus().PendingChanges != 1 {
					t.Fatal("healthy retry did not queue exactly one change")
				}
				if err := manager.SyncAccountManagementState(context.Background()); err != nil {
					t.Fatal(err)
				}
				if manager.GetAccountManagementStatus().PendingChanges != 0 {
					t.Fatal("healthy retry did not publish")
				}
			})
		}
	}
}

func TestAccountRemoteApplyFlushFailureLeavesNoMarker(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	first, second, _ := reviewAccountDevices(t)
	second.accountBackgroundWG.Wait()
	if err := first.UpdateWalletName(first.GetAccountManagementStatus().RootWalletID, "Atomic remote"); err != nil {
		t.Fatal(err)
	}
	if err := first.SyncAccountManagementState(nil); err != nil {
		t.Fatal(err)
	}
	db := second.db
	before := second.GetWalletCatalog()
	second.db = &failFlushDB{KVDB: db}
	err := second.SyncAccountManagementState(nil)
	second.db = db
	if err == nil {
		t.Fatal("flush failure hidden")
	}
	marker, err := second.readAccountManagedDataImportMarker()
	if err != nil || marker != nil {
		t.Fatalf("failed transaction left an import marker: %+v %v", marker, err)
	}
	if !reflect.DeepEqual(before, second.GetWalletCatalog()) {
		t.Fatal("failed transaction changed live catalog")
	}
	if err := second.SyncAccountManagementState(nil); err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(before, second.GetWalletCatalog()) {
		t.Fatal("retry did not apply remote catalog")
	}
}

func TestAccountSwitchCreatingSubAccountFailureIsAtomic(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, _, _ := reviewAccountDevices(t)
	manager.accountBackgroundWG.Wait()
	db := manager.db
	before := manager.GetWalletCatalog()
	status := loadStatusFromDB(db)
	manager.db = &failFlushDB{KVDB: db}
	manager.SwitchAccount(2)
	manager.db = db
	if !reflect.DeepEqual(before, manager.GetWalletCatalog()) ||
		!reflect.DeepEqual(status, loadStatusFromDB(db)) ||
		manager.GetCurrentAccountId() != status.CurrentAccount ||
		manager.GetAccountManagementStatus().PendingChanges != 0 {
		t.Fatal("failed switch committed a partial sub-account or changed selection")
	}
	manager.SwitchAccount(2)
	if manager.GetCurrentAccountId() != 2 || manager.GetAccountManagementStatus().PendingChanges != 1 {
		t.Fatal("healthy switch did not select and queue the new sub-account")
	}
	if err := manager.SyncAccountManagementState(nil); err != nil {
		t.Fatal(err)
	}
}
