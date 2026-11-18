package wallet

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
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
				manager, peer, _ := reviewAccountDevices(t)
				manager.accountBackgroundWG.Wait()
				id := manager.GetAccountManagementStatus().RootWalletID
				db := manager.db
				before := manager.GetWalletCatalog()
				facts := catalogLocalFacts(t, manager)
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
					!reflect.DeepEqual(status, loadStatusFromDB(db)) || !reflect.DeepEqual(profile, *manager.accountProfile) || generation != manager.accountGeneration || !reflect.DeepEqual(facts, catalogLocalFacts(t, manager)) {
					t.Fatal("failed transaction changed catalog, status, pending overlay or generation")
				}
				if err := operation.mutate(manager, id); err != nil {
					t.Fatalf("healthy retry: %v", err)
				}
				checkCatalogDirty(t, manager, before)
				if err := manager.SyncAccountManagementState(context.Background()); err != nil {
					t.Fatal(err)
				}
				if manager.GetAccountManagementStatus().PendingChanges != 0 {
					t.Fatal("healthy retry did not publish")
				}
				checkCatalogPeer(t, manager, peer)
			})
		}
	}
}

func TestAccountRemoteApplyFlush(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	first, second, remote := reviewAccountDevices(t)
	second.accountBackgroundWG.Wait()
	if err := first.UpdateWalletName(first.GetAccountManagementStatus().RootWalletID, "Atomic remote"); err != nil {
		t.Fatal(err)
	}
	if err := first.SyncAccountManagementState(nil); err != nil {
		t.Fatal(err)
	}
	db := second.db
	rootID := second.GetAccountManagementStatus().RootWalletID
	before := second.GetWalletCatalog()
	durable := accountPersistenceReviewCatalog(t, db)
	status := loadStatusFromDB(db)
	generation := second.accountGeneration
	fault := &accountPersistenceReviewDB{KVDB: db,
		profileKey: []byte(getWalletDBKey(rootID)), flushOnly: true}
	fault.armed.Store(true)
	second.db = fault
	err := second.SyncAccountManagementState(nil)
	if !errors.Is(err, ErrAccountDBStopped) || !errors.Is(err, errAccountPersistenceReview) || fault.hits.Load() != 1 {
		t.Fatalf("actual catalog Flush was not the single stopping fault: hits=%d err=%v", fault.hits.Load(), err)
	}
	if !reflect.DeepEqual(before, second.GetWalletCatalog()) ||
		!reflect.DeepEqual(durable, accountPersistenceReviewCatalog(t, db)) ||
		!reflect.DeepEqual(status, loadStatusFromDB(db)) || generation != second.accountGeneration {
		t.Fatal("failed import changed catalog, status or generation")
	}
	local, err := second.readAccountLocal(second.accountProfile.AccountID, "wallets", AccountManagedDataGlobalScope)
	if err != nil || local == nil || !local.Restoring {
		t.Fatal("failed import lost its durable module restore marker")
	}
	if err := second.UpdateWalletName(rootID, "Must not write"); !errors.Is(err, ErrAccountDBStopped) {
		t.Fatalf("stopped Manager permitted new business: %v", err)
	}
	// Recovery uses a new production Manager over the original durable DB,
	// never clearing the old runtime's latched storage failure.
	cfg := second.cfg
	second.Close()
	restarted := NewManager(cfg, db)
	if restarted == nil {
		t.Fatal("cold Manager initialization failed")
	}
	t.Cleanup(restarted.Close)
	restarted.SetDKVSHttpClient(remote)
	restarted.SetServerNodeHttpClient(newRGB11MessageNodeClient(remote))
	if _, err := restarted.UnlockWallet("password"); err != nil {
		t.Fatal(err)
	}
	if err := restarted.SyncAccountManagementState(nil); err != nil {
		t.Fatal(err)
	}
	if err := restarted.CheckAccountRestore("wallets", AccountManagedDataGlobalScope); err != nil {
		t.Fatalf("cold recovery left restore marker: %v", err)
	}
	local, err = restarted.readAccountLocal(restarted.accountProfile.AccountID, "wallets", AccountManagedDataGlobalScope)
	if err != nil || local == nil || local.Restoring {
		t.Fatal("cold recovery did not clear durable restore marker")
	}
	checkCatalogPeer(t, first, restarted)
}

func TestAccountSwitchCreatingSubAccountFailureIsAtomic(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, peer, _ := reviewAccountDevices(t)
	manager.accountBackgroundWG.Wait()
	db := manager.db
	before := manager.GetWalletCatalog()
	facts := catalogLocalFacts(t, manager)
	status := loadStatusFromDB(db)
	manager.db = &failFlushDB{KVDB: db}
	switchErr := manager.SwitchAccount(2)
	manager.db = db
	if switchErr == nil {
		t.Fatal("failed switch hid storage failure")
	}
	if !reflect.DeepEqual(before, manager.GetWalletCatalog()) ||
		!reflect.DeepEqual(status, loadStatusFromDB(db)) ||
		manager.GetCurrentAccountId() != status.CurrentAccount ||
		manager.GetAccountManagementStatus().PendingChanges != 0 || !reflect.DeepEqual(facts, catalogLocalFacts(t, manager)) {
		t.Fatal("failed switch committed a partial sub-account or changed selection")
	}
	if err := manager.SwitchAccount(2); err != nil {
		t.Fatal(err)
	}
	if manager.GetCurrentAccountId() != 2 {
		t.Fatal("healthy switch did not select the new sub-account")
	}
	checkCatalogDirty(t, manager, before)
	if err := manager.SyncAccountManagementState(nil); err != nil {
		t.Fatal(err)
	}
	if manager.GetAccountManagementStatus().PendingChanges != 0 {
		t.Fatal("switch changes remain pending")
	}
	checkCatalogPeer(t, manager, peer)
}

// Compare complete durable module facts, not only the legacy profile overlay.
func catalogLocalFacts(t *testing.T, m *Manager) map[string]string {
	t.Helper()
	facts := make(map[string]string)
	err := m.db.BatchRead(accountLocalPrefix(m.accountProfile.AccountID), false, func(k, v []byte) error {
		facts[string(k)] = string(v)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func checkCatalogDirty(t *testing.T, m *Manager, before []WalletCatalogEntry) {
	t.Helper()
	want := map[string]bool{"wallets/" + AccountManagedDataGlobalScope: true}
	old := make(map[string]map[uint32]bool)
	for _, w := range before {
		old[w.Fingerprint] = make(map[uint32]bool)
		for _, sub := range w.Accounts {
			old[w.Fingerprint][sub.Index] = true
		}
	}
	added := false
	for _, w := range m.GetWalletCatalog() {
		for _, sub := range w.Accounts {
			if old[w.Fingerprint][sub.Index] {
				continue
			}
			added = true
			scope := AccountManagedDataScope{Network: m.cfg.Chain, WalletFingerprint: w.Fingerprint, AccountIndex: sub.Index}.ID()
			want[rgb11AccountManagedProviderID+"/"+scope] = true
		}
	}
	if added {
		key, err := account.ManagedIndexKey(m.accountProfile.AccountID)
		if err != nil {
			t.Fatal(err)
		}
		want["account/"+key] = true
	}
	values, err := m.listAccountLocal(m.accountProfile.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool)
	for _, value := range values {
		if value.Generation > value.Confirmed {
			got[value.Provider+"/"+value.Scope] = true
		}
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("dirty module set differs: expected %d actual %d", len(want), len(got))
	}
	if m.GetAccountManagementStatus().PendingChanges != len(want) {
		t.Fatal("pending module count differs from exact dirty set")
	}
}

func checkCatalogPeer(t *testing.T, source, peer *Manager) {
	t.Helper()
	if err := peer.SyncAccountManagementState(nil); err != nil {
		t.Fatal(err)
	}
	normalized := func(m *Manager) map[string]WalletCatalogEntry {
		result := make(map[string]WalletCatalogEntry)
		for _, w := range m.GetWalletCatalog() {
			w.ID = 0
			result[w.Fingerprint] = w
		}
		return result
	}
	if !reflect.DeepEqual(normalized(source), normalized(peer)) {
		t.Fatal("peer catalog differs after confirmed sync")
	}
}
