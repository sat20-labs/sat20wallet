package wallet

import (
	"errors"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
)

func newAccountManagementAutoTestManager(t *testing.T) *Manager {
	t.Helper()
	database := newMemoryKVDB()
	manager := &Manager{
		db: database, status: newDefaultStatus(),
		walletInfoMap:        make(map[int64]*WalletInfo),
		tickerInfoMap:        make(map[string]*indexer.TickerInfo),
		utxoLockerL1:         NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN),
		utxoLockerL2:         NewUtxoLocker(database, nil, L2_NETWORK_SATOSHI),
		managedDataProviders: make(map[string]AccountManagedDataProvider),
	}
	rgbManager, err := newRGB11Manager(manager, database, manager.utxoLockerL1, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager.rgbManager = rgbManager
	if err := manager.RegisterAccountManagedDataProvider(&rgb11AccountManagedDataProvider{owner: manager}); err != nil {
		t.Fatal(err)
	}
	manager.ensureDKVSManager()
	t.Cleanup(func() {
		manager.clearAccountManagementSession()
		if manager.rgbManager != nil && manager.rgbManager.scopeStates != nil {
			manager.rgbManager.scopeStates.stopReconciliations()
		}
	})
	return manager
}

func assertInitialAccountManagementStatus(t *testing.T, manager *Manager, walletID int64) {
	t.Helper()
	status := manager.GetAccountManagementStatus()
	if !status.Active || status.RecoveryConfigured || status.StorageMode != AccountStorageTemporary ||
		status.AccountID == "" || status.RootFingerprint == "" || status.RootWalletID != walletID ||
		status.StateSeq == 0 || !status.ManagedDataDirty {
		t.Fatalf("initial account management status=%+v", status)
	}
	providers := manager.accountManagedDataProviders()
	if len(providers) != 1 || providers[0].ID() != rgb11AccountManagedProviderID {
		t.Fatalf("built-in account-managed providers=%v", providers)
	}
}

func TestImportFirstMnemonicWalletDoesNotInitializeAccountManagement(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager := newAccountManagementAutoTestManager(t)
	walletID, err := manager.ImportWallet(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"password",
	)
	if err != nil {
		t.Fatal(err)
	}
	if status := manager.GetAccountManagementStatus(); status.Active {
		t.Fatalf("ordinary wallet import initialized account management: %+v", status)
	}
	if err := manager.InitializeAccountManagement("password"); err != nil {
		t.Fatal(err)
	}
	assertInitialAccountManagementStatus(t, manager, walletID)
}

func TestCreateFirstMnemonicWalletAutomaticallyEnablesAccountManagement(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager := newAccountManagementAutoTestManager(t)
	walletID, mnemonic, err := manager.CreateWallet("password")
	if err != nil {
		t.Fatal(err)
	}
	if mnemonic == "" {
		t.Fatal("created wallet has no mnemonic")
	}
	assertInitialAccountManagementStatus(t, manager, walletID)
}

func TestImportWalletRejectsDuplicateFingerprintFromLockedCatalog(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	const mnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	manager := newAccountManagementAutoTestManager(t)
	if _, err := manager.ImportWallet(mnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	manager.wallet = nil
	for _, info := range manager.walletInfoMap {
		info.Wallet = nil
	}
	before := len(manager.walletInfoMap)
	if _, err := manager.ImportWallet(mnemonic, "password"); !errors.Is(err, ErrWalletAlreadyExists) {
		t.Fatalf("duplicate import error=%v", err)
	}
	if len(manager.walletInfoMap) != before || manager.wallet != nil {
		t.Fatal("duplicate import changed the locked wallet catalog")
	}
	if _, err := manager.ImportWallet(
		"legal winner thank year wave sausage worth useful legal winner thank yellow",
		"wrong-password",
	); !errors.Is(err, ErrWalletCatalogUnverifiable) {
		t.Fatalf("unverifiable catalog error=%v", err)
	}
}

func TestImportPrivateKeyRejectsDuplicateFingerprint(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager := newAccountManagementAutoTestManager(t)
	const privateKey = "1d5da8898fa894a056473e19e18bb2fa907172d25424cea6a0894312b2801bcc"
	if _, err := manager.ImportWalletWithPrivateKey(privateKey, "password"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ImportWalletWithPrivateKey(privateKey, "password"); !errors.Is(err, ErrWalletAlreadyExists) {
		t.Fatalf("duplicate private-key import error=%v", err)
	}
}
