package wallet

import (
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

func TestImportFirstMnemonicWalletAutomaticallyEnablesAccountManagement(t *testing.T) {
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
