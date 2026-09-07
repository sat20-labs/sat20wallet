package wallet

import (
	"bytes"
	"context"
	"errors"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type managedImportTestProvider struct {
	accountManagedDataProviderStub
	beforeImport func()
}

func (p *managedImportTestProvider) Import(catalog AccountManagedDataCatalog, payloads []AccountManagedDataPayload) error {
	if p.beforeImport != nil {
		p.beforeImport()
	}
	if err := p.accountManagedDataProviderStub.Import(catalog, payloads); err != nil {
		return err
	}
	p.payloads = payloads
	return nil
}

func managedImportTestManager(t *testing.T, remote *rgb11MemoryDKVSHTTP) (*Manager, *managedImportTestProvider) {
	t.Helper()
	manager := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(manager, remote)
	provider := &managedImportTestProvider{accountManagedDataProviderStub: accountManagedDataProviderStub{id: "test"}}
	manager.managedDataProviders = map[string]AccountManagedDataProvider{provider.id: provider}
	provider.beforeImport = func() {
		if err := manager.checkAccountManagedDataImport(); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
			t.Fatalf("provider started without durable import marker: %v", err)
		}
		if !manager.accountSyncMu.TryLock() {
			t.Fatal("provider import holds the account coordinator mutex")
		}
		manager.accountSyncMu.Unlock()
		if manager.channelIdentityMu.TryLock() {
			manager.channelIdentityMu.Unlock()
			t.Fatal("provider import is not protected by the application identity gate")
		}
		if manager.rgbOperationMu.TryLock() {
			manager.rgbOperationMu.Unlock()
			t.Fatal("provider import is not protected by the application RGB gate")
		}
		if err := manager.requireCurrentAccountManagedData(); !errors.Is(err, ErrDKVSPathNotSynced) {
			t.Fatalf("running import should wait, not report terminal failure: %v", err)
		}
	}
	return manager, provider
}

func managedImportRecoveryValue(t *testing.T, source *Manager) RecoveredAccountManagementState {
	t.Helper()
	profile := source.accountProfile
	state, err := account.OpenManagedState(source.accountSecret, profile.AccountID, profile.StateEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := openProfileManagedDataBundle(*profile, source.accountSecret)
	if err != nil {
		t.Fatal(err)
	}
	return RecoveredAccountManagementState{
		State: state, Seq: profile.StateSeq, Hash: profile.StateHash, Envelope: profile.StateEnvelope,
		ManagedData: bundle, ManagedDataHash: profile.ManagedDataHash, ManagedDataEnvelope: profile.ManagedDataEnvelope,
	}
}

func restoreManagedImportTestWallet(t *testing.T, target, source *Manager) error {
	t.Helper()
	_, err := target.RestoreAccountManagementState(managedImportRecoveryValue(t, source),
		source.accountSecret, "password", account.Locator{AccountID: source.accountProfile.AccountID},
		AccountManagementRestoreOptions{StorageMode: AccountStorageTemporary, RecordTTL: testRGB11FreeLocalTTL})
	return err
}

func managedImportTestSource(t *testing.T, remote *rgb11MemoryDKVSHTTP) (*Manager, *managedImportTestProvider) {
	t.Helper()
	source, provider := managedImportTestManager(t, remote)
	if _, err := source.ImportWallet(accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	if err := source.InitializeAccountManagement("password"); err != nil {
		t.Fatal(err)
	}
	provider.payloads = []AccountManagedDataPayload{{Scope: AccountManagedDataGlobalScope, Payload: []byte("A")}}
	if err := source.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	return source, provider
}

func TestManagedDataImportFailureCannotOverwriteServer(t *testing.T) {
	for _, mode := range []string{"recovery", "background-sync"} {
		t.Run(mode, func(t *testing.T) {
			remote := newRGB11MemoryDKVSHTTP()
			source, sourceProvider := managedImportTestSource(t, remote)
			target, provider := managedImportTestManager(t, remote)
			if mode == "background-sync" {
				if err := restoreManagedImportTestWallet(t, target, source); err != nil {
					t.Fatal(err)
				}
			}
			sourceProvider.payloads[0].Payload = []byte("B")
			if err := source.SyncAccountManagementState(context.Background()); err != nil {
				t.Fatal(err)
			}
			before := make(map[string]string)
			for key, record := range remote.records {
				before[key] = dkvsindexer.RecordHash(record).String()
			}
			failure := errors.New("provider import failed")
			provider.importErr = failure
			var err error
			if mode == "recovery" {
				err = restoreManagedImportTestWallet(t, target, source)
			} else {
				err = target.SyncAccountManagementState(context.Background())
			}
			if !errors.Is(err, failure) {
				t.Fatalf("import error was not returned: %v", err)
			}
			if target.accountProfile.ManagedDataHash != source.accountProfile.ManagedDataHash {
				t.Fatal("test did not reach core commit before provider failure")
			}
			if mode == "background-sync" && string(provider.payloads[0].Payload) != "A" {
				t.Fatal("test did not retain old local provider data")
			}
			// Even after transient provider errors disappear, retrying sync must
			// not reinterpret the old/empty local data as a deliberate deletion.
			provider.importErr = nil
			for attempt := 0; attempt < 2; attempt++ {
				if err := target.SyncAccountManagementState(context.Background()); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
					t.Fatalf("unsafe sync was not blocked: %v", err)
				}
			}
			if err := target.ActivateAccountManagement(source.accountSecret, "password",
				AccountStorageAuthorization{Mode: AccountStorageTemporary}, account.Locator{}, ""); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
				t.Fatalf("activation bypassed import protection: %v", err)
			}
			if err := target.WaitAccountManagedDataReady(context.Background()); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
				t.Fatalf("incomplete import reported ready: %v", err)
			}
			// New Manager, same persisted database: no in-memory failure flag.
			restarted, _ := managedImportTestManager(t, remote)
			restarted.db = target.db
			if err := restarted.loadAccountManagementProfileLocked(); err != nil {
				t.Fatal(err)
			}
			if err := restarted.unlockAccountManagementLocked("password"); err != nil {
				t.Fatal(err)
			}
			if err := restarted.SyncAccountManagementState(context.Background()); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
				t.Fatalf("restart lost import protection: %v", err)
			}
			if len(remote.records) != len(before) {
				t.Fatal("failed import added remote records")
			}
			for key, hash := range before {
				if dkvsindexer.RecordHash(remote.records[key]).String() != hash {
					t.Fatal("failed import overwrote a server record")
				}
			}
			// Discarding local state and importing into a fresh wallet succeeds.
			fresh, freshProvider := managedImportTestManager(t, remote)
			if err := restoreManagedImportTestWallet(t, fresh, source); err != nil {
				t.Fatal(err)
			}
			if err := fresh.checkAccountManagedDataImport(); err != nil {
				t.Fatalf("successful import kept marker: %v", err)
			}
			if len(freshProvider.payloads) != 1 || string(freshProvider.payloads[0].Payload) != "B" {
				t.Fatal("server no longer contains the recoverable data")
			}
			if err := fresh.SyncAccountManagementState(context.Background()); err != nil {
				t.Fatalf("successful import cannot sync: %v", err)
			}
		})
	}
}

type managedImportFaultDB struct {
	indexer.KVDB
	readErr, writeErr, deleteErr error
}

func (d *managedImportFaultDB) Read(key []byte) ([]byte, error) {
	if bytes.Equal(key, accountManagedDataImportKey()) && d.readErr != nil {
		return nil, d.readErr
	}
	return d.KVDB.Read(key)
}

func (d *managedImportFaultDB) Write(key, value []byte) error {
	if bytes.Equal(key, accountManagedDataImportKey()) && d.writeErr != nil {
		return d.writeErr
	}
	return d.KVDB.Write(key, value)
}

func (d *managedImportFaultDB) Delete(key []byte) error {
	if bytes.Equal(key, accountManagedDataImportKey()) && d.deleteErr != nil {
		return d.deleteErr
	}
	return d.KVDB.Delete(key)
}

func TestManagedDataRecoveryReturnsErrorsAndKeepsProtection(t *testing.T) {
	remote := newRGB11MemoryDKVSHTTP()
	source, _ := managedImportTestSource(t, remote)
	for _, point := range []string{"marker-write", "core-flush", "validation", "provider", "later-provider", "registrations", "marker-delete"} {
		t.Run(point, func(t *testing.T) {
			target, provider := managedImportTestManager(t, remote)
			database := target.db
			failure := errors.New("injected " + point + " failure")
			switch point {
			case "marker-write":
				target.db = &managedImportFaultDB{KVDB: database, writeErr: failure}
			case "core-flush":
				target.db = &passwordChangeFailFlushDB{KVDB: database}
			case "validation":
				provider.validateErr = failure
			case "provider":
				provider.importErr = failure
			case "later-provider":
				target.managedDataProviders["z"] = &accountManagedDataProviderStub{id: "z", importErr: failure}
			case "registrations":
				provider.beforeImport = func() { target.accountProfile.RootFingerprint = "unavailable" }
				failure = ErrAccountManagementWalletUnavailable
			case "marker-delete":
				target.db = &managedImportFaultDB{KVDB: database, deleteErr: failure}
			}
			err := restoreManagedImportTestWallet(t, target, source)
			if err == nil || (point != "core-flush" && !errors.Is(err, failure)) {
				t.Fatalf("%s error not propagated: %v", point, err)
			}
			if point == "later-provider" && provider.imports != 1 {
				t.Fatal("test did not reach a partially imported provider set")
			}
			target.db = database
			if point == "marker-write" {
				if target.accountProfile != nil || target.wallet != nil || provider.imports != 0 {
					t.Fatal("marker write failure did not stop recovery before mutation")
				}
				if _, err := database.Read(accountManagementProfileKey()); !errors.Is(err, indexer.ErrKeyNotFound) {
					t.Fatalf("marker write failure persisted profile: %v", err)
				}
				return
			}
			if point == "registrations" {
				if err := target.checkAccountManagedDataImport(); err != nil {
					t.Fatalf("retryable registration failure retained recovery marker: %v", err)
				}
				return
			}
			if err := target.SyncAccountManagementState(nil); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
				t.Fatalf("%s failure allowed upload: %v", point, err)
			}
		})
	}
	t.Run("marker-read", func(t *testing.T) {
		failure := errors.New("marker read failed")
		manager := &Manager{db: &managedImportFaultDB{KVDB: newMemoryKVDB(), readErr: failure}}
		if err := manager.SyncAccountManagementState(nil); !errors.Is(err, failure) {
			t.Fatalf("unreadable marker treated as absent: %v", err)
		}
	})
}
