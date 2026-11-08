package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/btcsuite/btcwallet/snacl"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
)

// privateAccountDevice establishes one real account through production APIs.
// Only the existing HTTP/message service boundaries are injected; no profile,
// secret, readiness, status, wallet cache, or second-device snapshot is copied.
func privateAccountDevice(t *testing.T) *Manager {
	t.Helper()
	oldEnv, oldChain, oldMode, oldIndexerChain := _env, _chain, _mode, indexer.CHAIN
	t.Cleanup(func() { _env, _chain, _mode, indexer.CHAIN = oldEnv, oldChain, oldMode, oldIndexerChain })
	_chain, indexer.CHAIN = "testnet", "testnet"
	remote := newRGB11MemoryDKVSHTTP()
	service := newRGB11MessageNodeClient(remote)
	cfg := &common.Config{Env: "test", Chain: "testnet", Mode: CLIENT_NODE,
		IndexerL1: &common.Indexer{Scheme: "http", Host: "127.0.0.1:1", Proxy: "testnet"},
		IndexerL2: &common.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"},
		Peers: []string{
			"b@" + indexer.GetBootstrapPubKey() + "@http://127.0.0.1:1/stp/testnet",
			"s@" + hex.EncodeToString(service.CoreNodePubKey().SerializeCompressed()) + "@http://127.0.0.1:1/stp/testnet",
		}}
	database := NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("open private account database failed")
	}
	manager := NewManager(cfg, database)
	if manager == nil {
		_ = database.Close()
		t.Fatal("initialize private account manager failed")
	}
	t.Cleanup(func() {
		manager.Close()
		if err := database.Close(); err != nil {
			t.Fatal("close private account database failed")
		}
	})
	manager.SetDKVSHttpClient(remote)
	manager.SetServerNodeHttpClient(service)
	if _, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatalf("import single-device root: %v", err)
	}
	if err := manager.InitializeAccountManagement("password"); err != nil {
		t.Fatalf("initialize single-device account: %v", err)
	}
	// Existing verified helper performs inactive discovery and builds real
	// storage authorization/recovery material; it does not inject ready state.
	pkg, auth, secret := rereviewRecoveryPackage(t, manager)
	defer zeroBytes(secret)
	if err := manager.ActivateAccountManagement(secret, "password", *auth,
		pkg.Envelope.Locator, "account://"+pkg.Envelope.Locator.PackageID); err != nil {
		t.Fatalf("activate single-device account: %v", err)
	}
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatalf("synchronize single-device account: %v", err)
	}
	status := manager.GetAccountManagementStatus()
	if !status.RecoveryConfigured || status.ActivationPending || !status.DKVSWriteAllowed ||
		status.ManagedDataDirty || status.PendingChanges != 0 {
		t.Fatal("single-device account did not reach its real synchronized baseline")
	}
	return manager
}

func TestManagedPrivateWalletRestore(t *testing.T) {
	sender := privateAccountDevice(t)
	scalar := make([]byte, 32)
	scalar[31] = 1
	defer zeroBytes(scalar)
	id, err := sender.ImportWalletWithPrivateKey(hex.EncodeToString(scalar), "password")
	if err != nil {
		t.Fatal("managed private import failed")
	}
	sender.mutex.RLock()
	imported := sender.walletInfoMap[id]
	valid := imported != nil && imported.Type == WALLET_TYPE_PRIVKEY && imported.Accounts == 1
	fingerprint := ""
	if valid {
		fingerprint = walletFingerprint(imported.Wallet)
	}
	sender.mutex.RUnlock()
	if !valid {
		t.Fatal("private import lost type or account-zero capability")
	}
	if err := sender.SwitchAccount(1); err == nil {
		t.Fatal("private wallet acquired an HD subaccount")
	}
	if err := sender.EnsureAccount(id, 1, "extra", ""); err == nil {
		t.Fatal("private wallet admitted an HD subaccount")
	}
	if err := sender.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal("mixed account backup failed")
	}
	backup, err := sender.ExportAccountBackup("password", nil)
	if err != nil {
		t.Fatal("full backup rejected private wallet")
	}
	defer clearAccountBackup(&backup)
	count := 0
	for _, w := range backup.Wallets {
		if w.Type == account.WalletPrivateKey {
			count++
		}
	}
	if count != 1 {
		t.Fatal("full backup omitted or duplicated private type")
	}
	// Follow the production open/initialize/restore/close/reopen/unlock path.
	// Config mirrors the existing production SP lifecycle fixture; all peers
	// and indexers are loopback, and this test does not start network workers.
	path := t.TempDir()
	endpoint := &common.Indexer{Scheme: "http", Host: "127.0.0.1:1", Proxy: "testnet"}
	cfg := &common.Config{Env: "test", Chain: "testnet", Mode: CLIENT_NODE,
		IndexerL1: endpoint, IndexerL2: endpoint,
		Peers: []string{"b@" + indexer.GetBootstrapPubKey() + "@http://127.0.0.1:1/testnet"}}
	open := func() (*Manager, func()) {
		database := NewKVDB(path)
		if database == nil {
			t.Fatal("open restore database failed")
		}
		manager := NewManager(cfg, database)
		if manager == nil {
			_ = database.Close()
			t.Fatal("production manager initialization failed")
		}
		closed := false
		close := func() {
			if !closed {
				manager.Close()
				if err := database.Close(); err != nil {
					t.Fatal("close restore database failed")
				}
				closed = true
			}
		}
		t.Cleanup(close)
		return manager, close
	}
	target, closeTarget := open()
	results, err := target.RestoreAccountBackupWithResult(backup, t.Name())
	if err != nil || len(results) != len(backup.Wallets) {
		t.Fatal("full restore omitted wallets")
	}
	found := false
	target.mutex.RLock()
	for _, info := range target.walletInfoMap {
		if info.Type == WALLET_TYPE_PRIVKEY {
			found = info.Accounts == 1 && walletFingerprint(info.Wallet) == fingerprint
		}
	}
	target.mutex.RUnlock()
	if !found {
		t.Fatal("restored private identity differs")
	}
	beforeRestart := target.GetWalletCatalog()
	selectedID, selectedIndex := target.GetCurrentWalletId(), target.GetCurrentAccountId()
	closeTarget()
	cold, _ := open()
	if cold.GetWallet() != nil {
		t.Fatal("production restart exposed an unlocked wallet")
	}
	if _, err := cold.UnlockWallet(t.Name()); err != nil {
		t.Fatal("mixed cold unlock failed")
	}
	if !reflect.DeepEqual(beforeRestart, cold.GetWalletCatalog()) ||
		cold.GetCurrentWalletId() != selectedID || cold.GetCurrentAccountId() != selectedIndex {
		t.Fatal("cold restart changed mixed identities or selection")
	}
	cold.mutex.RLock()
	privateCount, mnemonicCount := 0, 0
	for _, info := range cold.walletInfoMap {
		switch info.Type {
		case WALLET_TYPE_PRIVKEY:
			privateCount++
			if info.Accounts != 1 || walletFingerprint(info.Wallet) != fingerprint {
				t.Error("cold private identity differs")
			}
		case WALLET_TYPE_MNEMONIC:
			mnemonicCount++
		}
	}
	cold.mutex.RUnlock()
	if privateCount != 1 || mnemonicCount != 1 {
		t.Fatal("cold restart omitted a wallet type")
	}
}

// Capture both persisted state and live selection before a rejected import.
// Assertions emit booleans/stages only; no encrypted or plaintext material.
func privateImportUnchanged(t *testing.T, manager *Manager) func() {
	t.Helper()
	db := manager.db
	catalog := manager.GetWalletCatalog()
	durable := accountPersistenceReviewCatalog(t, db)
	status, err := db.Read([]byte(DB_KEY_STATUS))
	if err != nil {
		t.Fatal("status snapshot failed")
	}
	profile, err := db.Read(accountManagementProfileKey())
	if err != nil {
		t.Fatal("profile snapshot failed")
	}
	liveBytes, err := EncodeToBytes(manager.accountProfile)
	if err != nil {
		t.Fatal("live profile snapshot failed")
	}
	var liveProfile accountManagementProfile
	if err := DecodeFromBytes(liveBytes, &liveProfile); err != nil {
		t.Fatal("profile snapshot decode failed")
	}
	generation := manager.accountGeneration
	wallet, walletID, index := manager.wallet, manager.GetCurrentWalletId(), manager.GetCurrentAccountId()
	return func() {
		t.Helper()
		afterStatus, err := db.Read([]byte(DB_KEY_STATUS))
		if err != nil {
			t.Fatal("status reread failed")
		}
		afterProfile, err := db.Read(accountManagementProfileKey())
		if err != nil {
			t.Fatal("profile reread failed")
		}
		afterLiveBytes, err := EncodeToBytes(manager.accountProfile)
		if err != nil {
			t.Fatal("live_profile after snapshot encode failed")
		}
		defer zeroBytes(afterLiveBytes)
		var afterLiveProfile accountManagementProfile
		if err := DecodeFromBytes(afterLiveBytes, &afterLiveProfile); err != nil {
			t.Fatal("live_profile after snapshot decode failed")
		}
		// Both live-profile snapshots use the same complete codec round trip.
		// All profile fields, including full Pending contents, are compared.
		checks := []struct {
			name      string
			unchanged bool
		}{
			{"live_catalog", reflect.DeepEqual(catalog, manager.GetWalletCatalog())},
			{"durable_catalog", reflect.DeepEqual(durable, accountPersistenceReviewCatalog(t, db))},
			{"status_bytes", bytes.Equal(status, afterStatus)},
			{"profile_bytes", bytes.Equal(profile, afterProfile)},
			{"live_profile", reflect.DeepEqual(&liveProfile, &afterLiveProfile)},
			{"generation", generation == manager.accountGeneration},
			{"wallet_pointer", wallet == manager.wallet},
			{"selected_wallet", walletID == manager.GetCurrentWalletId()},
			{"selected_account", index == manager.GetCurrentAccountId()},
		}
		var changed []string
		for _, check := range checks {
			if !check.unchanged {
				changed = append(changed, check.name)
			}
		}
		if len(changed) != 0 {
			t.Fatalf("rejected private import changed: %s", strings.Join(changed, ", "))
		}
	}
}

func TestPrivateImportWrongPassword(t *testing.T) {
	manager := privateAccountDevice(t)
	manager.accountBackgroundWG.Wait()
	scalar := make([]byte, 32)
	scalar[31] = 1
	defer zeroBytes(scalar)
	unchanged := privateImportUnchanged(t, manager)
	_, err := manager.ImportWalletWithPrivateKey(hex.EncodeToString(scalar), "incorrect-fixture-password")
	if !errors.Is(err, snacl.ErrInvalidPassword) {
		t.Fatal("private import did not reach invalid-password rejection")
	}
	unchanged()
	if _, err := manager.ImportWalletWithPrivateKey(hex.EncodeToString(scalar), "password"); err != nil {
		t.Fatal("authenticated retry of private import failed")
	}
}

func TestPrivateImportDuplicate(t *testing.T) {
	manager := privateAccountDevice(t)
	scalar := make([]byte, 32)
	scalar[31] = 1
	defer zeroBytes(scalar)
	if _, err := manager.ImportWalletWithPrivateKey(hex.EncodeToString(scalar), "password"); err != nil {
		t.Fatal("first private import failed")
	}
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal("private baseline synchronization failed")
	}
	manager.accountBackgroundWG.Wait()
	unchanged := privateImportUnchanged(t, manager)
	_, err := manager.ImportWalletWithPrivateKey(hex.EncodeToString(scalar), "password")
	if !errors.Is(err, ErrWalletAlreadyExists) {
		t.Fatal("duplicate private identity was not rejected")
	}
	unchanged()
}

func TestPrivateImportFlushFailure(t *testing.T) {
	manager := privateAccountDevice(t)
	manager.accountBackgroundWG.Wait()
	scalar := make([]byte, 32)
	scalar[31] = 1
	defer zeroBytes(scalar)
	unchanged := privateImportUnchanged(t, manager)
	db := manager.db
	// Existing fixture preserves the backend's actual AtomicWriteBatch and
	// fails its Flush, ensuring the transaction boundary is really reached.
	manager.db = &dkvsAtomicFlushFaultDB{KVDB: db}
	_, err := manager.ImportWalletWithPrivateKey(hex.EncodeToString(scalar), "password")
	manager.db = db
	if err == nil || !strings.Contains(err.Error(), "injected atomic publication failure") {
		t.Fatal("private import did not reach the injected atomic flush failure")
	}
	unchanged()
	if _, err := manager.ImportWalletWithPrivateKey(hex.EncodeToString(scalar), "password"); err != nil {
		t.Fatal("healthy retry of private import failed")
	}
	if manager.GetAccountManagementStatus().PendingChanges != 1 {
		t.Fatal("healthy retry did not queue exactly one import")
	}
}
