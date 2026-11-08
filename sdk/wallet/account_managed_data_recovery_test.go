package wallet

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
)

type managedImportTestProvider struct {
	accountManagedDataProviderStub
	beforeImport func()
	owner        *Manager
}

func importProviderDataKey(id string) []byte { return []byte("test-provider-data/" + id) }

func (p *managedImportTestProvider) Import(catalog AccountManagedDataCatalog, payloads []AccountManagedDataPayload) error {
	if p.beforeImport != nil {
		p.beforeImport()
	}
	if err := p.accountManagedDataProviderStub.Import(catalog, payloads); err != nil {
		return err
	}
	owned := make([]AccountManagedDataPayload, len(payloads))
	for i, payload := range payloads {
		owned[i] = AccountManagedDataPayload{Scope: payload.Scope, Payload: bytes.Clone(payload.Payload)}
	}
	if p.owner != nil {
		encoded, err := EncodeToBytes(owned)
		if err != nil {
			return err
		}
		if err := p.owner.db.Write(importProviderDataKey(p.id), encoded); err != nil {
			return err
		}
	}
	p.payloads = owned
	return nil
}

func managedImportTestManager(t *testing.T, remote *rgb11MemoryDKVSHTTP) (*Manager, *managedImportTestProvider) {
	t.Helper()
	manager := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(manager, remote)
	provider := managedImportProvider(t, manager)
	manager.managedDataProviders = map[string]AccountManagedDataProvider{provider.id: provider}
	return manager, provider
}

func managedImportProvider(t *testing.T, manager *Manager) *managedImportTestProvider {
	t.Helper()
	return importProviderWithID(t, manager, "test")
}

func importProviderWithID(t *testing.T, manager *Manager, id string) *managedImportTestProvider {
	t.Helper()
	provider := &managedImportTestProvider{accountManagedDataProviderStub: accountManagedDataProviderStub{id: id}, owner: manager}
	if encoded, err := manager.db.Read(importProviderDataKey(id)); err == nil {
		if err := DecodeFromBytes(encoded, &provider.payloads); err != nil {
			t.Fatal(err)
		}
	} else if !errors.Is(err, indexer.ErrKeyNotFound) {
		t.Fatal(err)
	}
	provider.beforeImport = func() {
		if err := manager.CheckAccountRestore(provider.id, AccountManagedDataGlobalScope); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
			t.Fatalf("provider started without durable import marker: %v", err)
		}
		if !manager.accountSyncMu.TryLock() {
			t.Fatal("provider import holds the account coordinator mutex")
		}
		manager.accountSyncMu.Unlock()
		if !manager.channelIdentityMu.TryLock() {
			t.Fatal("provider import holds the application identity mutex across validation")
		}
		manager.channelIdentityMu.Unlock()
		if !manager.rgbOperationMu.TryLock() {
			t.Fatal("provider import holds the application RGB mutex across validation")
		}
		manager.rgbOperationMu.Unlock()
		if err := manager.CheckAccountWrite(provider.id, AccountManagedDataGlobalScope); !errors.Is(err, ErrDKVSPathNotSynced) &&
			!errors.Is(err, ErrAccountManagedDataImportIncomplete) {
			t.Fatalf("running import should wait, not report terminal failure: %v", err)
		}
	}
	return provider
}

func managedImportRecoveryValue(t *testing.T, source *Manager) RecoveredAccountManagementState {
	t.Helper()
	locator, location := importGroupMaterial(t, source)
	value, err := source.LoadAccountManagementStateForRecovery(location, locator, source.accountSecret, accountRootWrapperTestMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	return *value
}

func restoreManagedImportTestWallet(t *testing.T, target, source *Manager) error {
	t.Helper()
	locator, location := importGroupMaterial(t, source)
	target.serverNode = source.serverNode
	_, err := target.RestoreAccountManagementState(managedImportRecoveryValue(t, source),
		source.accountSecret, "password", locator,
		AccountManagementRestoreOptions{Location: location, StorageMode: source.accountProfile.StorageMode,
			RecordTTL: source.accountProfile.RecordTTL, PublicLocator: source.accountProfile.PublicLocator,
			AutopayContract: source.accountProfile.AutopayContract})
	return err
}

func managedImportTestSource(t *testing.T, remote *rgb11MemoryDKVSHTTP) (*Manager, *managedImportTestProvider) {
	t.Helper()
	if _chain == "" {
		oldChain := _chain
		_chain = "testnet"
		t.Cleanup(func() { _chain = oldChain })
	}
	source, provider := managedImportTestManager(t, remote)
	client := newRGB11MessageNodeClient(remote)
	source.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
	if _, err := source.ImportWallet(accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	if err := source.InitializeAccountManagement("password"); err != nil {
		t.Fatal(err)
	}
	provider.payloads = []AccountManagedDataPayload{{Scope: AccountManagedDataGlobalScope, Payload: []byte("A")}}
	pkg, auth, secret := rereviewRecoveryPackage(t, source)
	if err := source.ActivateAccountManagement(secret, "password", *auth,
		pkg.Envelope.Locator, "account://"+pkg.Envelope.Locator.PackageID); err != nil {
		t.Fatal(err)
	}
	if err := source.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	return source, provider
}

func TestManagedDataImportFailureCannotOverwriteServer(t *testing.T) {
	for _, mode := range []string{"recovery", "background-sync"} {
		t.Run(mode, func(t *testing.T) {
			remote := newRGB11MemoryDKVSHTTP()
			source, sourceProvider, _ := newImportSource(t, remote)
			target, provider, closeTarget := newImportGroup(t, remote)
			if mode == "background-sync" {
				if err := restoreImportGroup(t, target, source); err != nil {
					t.Fatal(err)
				}
			}
			stageManagedImportTestPayload(t, source, sourceProvider, []byte("B"))
			if err := source.SyncAccountManagementState(context.Background()); err != nil {
				t.Fatal(err)
			}
			before := snapshotRGB11MemoryRecords(remote)
			failure := errors.New("provider import failed")
			provider.importErr = failure
			if mode == "recovery" {
				if err := restoreImportGroup(t, target, source); err != nil {
					t.Fatalf("module failure aborted wallet restoration: %v", err)
				}
			} else if err := target.SyncAccountManagementState(context.Background()); !errors.Is(err, failure) || !accountModuleOnly(err) {
				t.Fatalf("module import error was not scoped: %v", err)
			}
			assertManagedImportModuleProtected(t, target, provider.id)
			if !bytes.Equal(target.accountSecret, source.accountSecret) || len(target.GetWalletCatalog()) == 0 {
				t.Fatal("module failure lost the restored account secret or wallet catalog")
			}
			if mode == "background-sync" && string(provider.payloads[0].Payload) != "A" {
				t.Fatal("failed import replaced the previous provider data")
			}
			if !reflect.DeepEqual(before, snapshotRGB11MemoryRecords(remote)) {
				t.Fatal("failed import overwrote server records")
			}
			// Reopen the actual database; the new device must retain its per-module
			// protection until normal synchronization imports the committed target.
			cfg := target.cfg
			closeTarget()
			restarted, restartedProvider, _ := openImportGroup(t, cfg, remote)
			if _, err := restarted.UnlockWallet("password"); err != nil {
				t.Fatal(err)
			}
			assertManagedImportModuleProtected(t, restarted, restartedProvider.id)
			if err := restarted.SyncAccountManagementState(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := restarted.CheckAccountRestore(restartedProvider.id, AccountManagedDataGlobalScope); err != nil {
				t.Fatalf("completed module import remains blocked: %v", err)
			}
			if len(restartedProvider.payloads) != 1 || string(restartedProvider.payloads[0].Payload) != "B" {
				t.Fatal("normal retry exported stale data instead of importing the committed target")
			}
			if !reflect.DeepEqual(before, snapshotRGB11MemoryRecords(remote)) {
				t.Fatal("module retry rewrote server records")
			}
			fresh, freshProvider, _ := newImportGroup(t, remote)
			if err := restoreImportGroup(t, fresh, source); err != nil {
				t.Fatal(err)
			}
			if len(freshProvider.payloads) != 1 || string(freshProvider.payloads[0].Payload) != "B" {
				t.Fatal("server no longer contains recoverable provider data")
			}
		})
	}
}

func stageManagedImportTestPayload(t *testing.T, manager *Manager, provider *managedImportTestProvider, payload []byte) {
	t.Helper()
	batch, err := NewAtomicWriteBatch(manager.db)
	if err != nil {
		t.Fatal(err)
	}
	defer batch.Close()
	done, err := manager.StageAccountChange(batch, provider.id, AccountManagedDataGlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	defer done(false)
	owned := []AccountManagedDataPayload{{Scope: AccountManagedDataGlobalScope, Payload: bytes.Clone(payload)}}
	encoded, err := EncodeToBytes(owned)
	if err != nil {
		t.Fatal(err)
	}
	if err := batch.Put(importProviderDataKey(provider.id), encoded); err != nil {
		t.Fatal(err)
	}
	if err := batch.Flush(); err != nil {
		t.Fatal(err)
	}
	provider.payloads = owned
	done(true)
}

func assertManagedImportModuleProtected(t *testing.T, manager *Manager, provider string) {
	t.Helper()
	value, err := manager.readAccountLocal(manager.accountProfile.AccountID, provider, AccountManagedDataGlobalScope)
	if err != nil || value == nil || !value.Restoring || len(value.Value) == 0 {
		t.Fatalf("interrupted module lost its durable encrypted target: %v", err)
	}
	if err := manager.CheckAccountRestore(provider, AccountManagedDataGlobalScope); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
		t.Fatalf("interrupted module import is not protected: %v", err)
	}
	if err := manager.CheckAccountWrite(provider, AccountManagedDataGlobalScope); err == nil {
		t.Fatal("interrupted module allowed new business")
	}
	if err := manager.CheckAccountRestore("wallets", AccountManagedDataGlobalScope); err != nil {
		t.Fatalf("provider failure blocked the healthy wallet module: %v", err)
	}
}

type managedImportFaultDB struct {
	indexer.KVDB
	key               []byte
	readErr, writeErr error
	hits              atomic.Int64
}

func (d *managedImportFaultDB) Read(key []byte) ([]byte, error) {
	if bytes.Equal(key, d.key) && d.readErr != nil {
		return nil, d.readErr
	}
	return d.KVDB.Read(key)
}

func (d *managedImportFaultDB) Write(key, value []byte) error {
	if bytes.Equal(key, d.key) && d.writeErr != nil {
		d.hits.Add(1)
		return d.writeErr
	}
	return d.KVDB.Write(key, value)
}

func TestManagedDataRecoveryReturnsErrorsAndKeepsProtection(t *testing.T) {
	remote := newRGB11MemoryDKVSHTTP()
	source, _ := managedImportTestSource(t, remote)
	for _, point := range []string{"local-copy-write", "core-flush", "validation", "provider", "completion-write"} {
		t.Run(point, func(t *testing.T) {
			target, provider := managedImportTestManager(t, remote)
			database := target.db
			key := accountLocalKey(source.accountProfile.AccountID, provider.id, AccountManagedDataGlobalScope)
			failure := errors.New("injected " + point + " failure")
			var completionFault *managedImportFaultDB
			switch point {
			case "local-copy-write":
				fault := &accountPersistenceReviewDB{KVDB: database, profileKey: key}
				fault.armed.Store(true)
				target.db = fault
				failure = errAccountPersistenceReview
			case "core-flush":
				target.db = &passwordChangeFailFlushDB{KVDB: database}
			case "validation":
				provider.validateErr = failure
			case "provider":
				provider.importErr = failure
			case "completion-write":
				fault := &managedImportFaultDB{KVDB: database, key: key}
				completionFault = fault
				target.db = fault
				beforeImport := provider.beforeImport
				provider.beforeImport = func() { beforeImport(); fault.writeErr = failure }
			}
			err := restoreManagedImportTestWallet(t, target, source)
			target.db = database
			if point == "local-copy-write" || point == "core-flush" {
				if err == nil {
					t.Fatal("atomic restore write failure was ignored")
				}
				if target.accountProfile != nil || target.wallet != nil || provider.imports != 0 {
					t.Fatal("failed atomic restore mutated account or provider state")
				}
				if _, err := database.Read(accountManagementProfileKey()); !errors.Is(err, indexer.ErrKeyNotFound) {
					t.Fatalf("failed atomic restore persisted profile: %v", err)
				}
				if err := restoreManagedImportTestWallet(t, target, source); !errors.Is(err, ErrAccountDBStopped) {
					t.Fatalf("failed Manager resumed after storage recovered: %v", err)
				}
				service := target.serverNode
				target.Close()
				target = newAccountManagementAutoTestManager(t)
				target.db, target.serverNode = database, service
				configureRGB11DKVSTestManager(target, remote)
				if err := target.initDB(); err != nil {
					t.Fatal(err)
				}
				provider = managedImportProvider(t, target)
				target.managedDataProviders = map[string]AccountManagedDataProvider{provider.id: provider}
				if target.accountProfile != nil || target.wallet != nil {
					t.Fatal("restart loaded a partial account from the failed atomic restore")
				}
				if err := restoreManagedImportTestWallet(t, target, source); err != nil {
					t.Fatal(err)
				}
				assertImportComplete(t, target, provider, []byte("A"))
				return
			}
			if point == "completion-write" {
				if completionFault.hits.Load() == 0 {
					t.Fatal("completion persistence fault was not reached")
				}
				if !errors.Is(err, failure) {
					t.Fatalf("completion persistence failure not propagated: %v", err)
				}
			} else if err != nil {
				t.Fatalf("module failure aborted core restore: %v", err)
			}
			assertManagedImportModuleProtected(t, target, provider.id)
			if !bytes.Equal(target.accountSecret, source.accountSecret) {
				t.Fatal("failed provider import discarded the account secret")
			}
		})
	}
	t.Run("local-copy-read", func(t *testing.T) {
		failure := errors.New("module copy read failed")
		manager, _ := managedImportTestSource(t, newRGB11MemoryDKVSHTTP())
		before := snapshotRGB11MemoryRecords(manager.http.(*rgb11MemoryDKVSHTTP))
		database := manager.db
		manager.db = &managedImportFaultDB{KVDB: database,
			key: accountLocalKey(manager.accountProfile.AccountID, "test", AccountManagedDataGlobalScope), readErr: failure}
		if err := manager.CheckAccountWrite("test", AccountManagedDataGlobalScope); !errors.Is(err, failure) {
			t.Fatalf("unreadable local copy treated as ready: %v", err)
		}
		manager.db = database
		if !reflect.DeepEqual(before, snapshotRGB11MemoryRecords(manager.http.(*rgb11MemoryDKVSHTTP))) {
			t.Fatal("module read failure changed remote records")
		}
	})
}
