package wallet

import (
	"strings"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
)

func TestManagedRootWalletCannotBeDeleted(t *testing.T) {
	database := newMemoryKVDB()
	rootWallet := NewInternalWalletWithMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"", &chaincfg.TestNet4Params,
	)
	childWallet := NewInternalWalletWithMnemonic(
		"legal winner thank year wave sausage worth useful legal winner thank yellow",
		"", &chaincfg.TestNet4Params,
	)
	if rootWallet == nil || childWallet == nil {
		t.Fatal("create test wallets")
	}
	rootWallet.id = 1
	childWallet.id = 2
	rootInfo := &WalletInfo{WalletInDB: WalletInDB{
		Id: 1, Accounts: 1, Type: WALLET_TYPE_MNEMONIC, Name: "Root",
		AccountNames: map[uint32]string{0: "Account 1"}, AccountDIDs: map[uint32]string{},
	}, Wallet: rootWallet}
	childInfo := &WalletInfo{WalletInDB: WalletInDB{
		Id: 2, Accounts: 1, Type: WALLET_TYPE_MNEMONIC, Name: "Child",
		AccountNames: map[uint32]string{0: "Account 1"}, AccountDIDs: map[uint32]string{},
	}, Wallet: childWallet}
	if err := saveWallet(database, &rootInfo.WalletInDB); err != nil {
		t.Fatal(err)
	}
	if err := saveWallet(database, &childInfo.WalletInDB); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		db: database, wallet: rootWallet, status: &Status{CurrentWallet: 1},
		walletInfoMap: map[int64]*WalletInfo{1: rootInfo, 2: childInfo},
		accountProfile: &accountManagementProfile{
			Version: accountManagementProfileVersion, RootFingerprint: walletFingerprint(rootWallet),
			AccountID: strings.Repeat("a", 64), DeviceID: make([]byte, accountManagementDeviceIDSize),
		},
	}

	if err := manager.DeleteWallet(1); err == nil {
		t.Fatal("account management root wallet was deleted")
	}
	if len(manager.walletInfoMap) != 2 {
		t.Fatal("root deletion changed the wallet catalog")
	}
	if err := manager.DeleteWallet(2); err != nil {
		t.Fatal(err)
	}
	if len(manager.walletInfoMap) != 1 || manager.walletInfoMap[1] == nil {
		t.Fatal("non-root wallet deletion damaged the root wallet")
	}
	if len(manager.accountProfile.Pending) != 1 ||
		manager.accountProfile.Pending[0].Type != accountMutationDeleteWallet ||
		manager.accountProfile.Pending[0].Fingerprint != walletFingerprint(childWallet) {
		t.Fatal("non-root wallet deletion was not queued for DKVS")
	}
}

func TestEnsureAccountOnlyExtendsAccountIndexes(t *testing.T) {
	database := newMemoryKVDB()
	value := NewInternalWalletWithMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"", &chaincfg.TestNet4Params,
	)
	if value == nil {
		t.Fatal("create test wallet")
	}
	value.id = 1
	info := &WalletInfo{WalletInDB: WalletInDB{
		Id: 1, Accounts: 1, Type: WALLET_TYPE_MNEMONIC, Name: "Root",
		AccountNames: map[uint32]string{0: "Account 1"}, AccountDIDs: map[uint32]string{},
	}, Wallet: value}
	if err := saveWallet(database, &info.WalletInDB); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{db: database, walletInfoMap: map[int64]*WalletInfo{1: info}}
	if err := manager.EnsureAccount(1, 2, "Third", ""); err != nil {
		t.Fatal(err)
	}
	if info.Accounts != 3 || info.AccountNames[2] != "Third" {
		t.Fatal("account index was not extended")
	}
	if err := manager.EnsureAccount(1, 0, "Renamed", ""); err != nil {
		t.Fatal(err)
	}
	if info.Accounts != 3 || info.AccountNames[0] != "Renamed" {
		t.Fatal("updating an existing index changed account count")
	}
}

func TestAccountManagementIgnoresDuplicateWalletFingerprints(t *testing.T) {
	const mnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	const password = "test-password"

	database := newMemoryKVDB()
	first := NewInternalWalletWithMnemonic(mnemonic, "", &chaincfg.TestNet4Params)
	duplicate := NewInternalWalletWithMnemonic(mnemonic, "", &chaincfg.TestNet4Params)
	if first == nil || duplicate == nil {
		t.Fatal("create duplicate test wallets")
	}
	first.id = 1
	duplicate.id = 2
	manager := &Manager{db: database, walletInfoMap: make(map[int64]*WalletInfo)}
	if err := manager.saveMnemonic(mnemonic, password, duplicate); err != nil {
		t.Fatal(err)
	}
	if err := manager.saveMnemonic(mnemonic, password, first); err != nil {
		t.Fatal(err)
	}

	backup, err := manager.ExportAccountBackup(password, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(backup.Wallets) != 1 {
		t.Fatalf("duplicate wallet was included in backup: %d wallets", len(backup.Wallets))
	}

	fingerprint := walletFingerprint(first)
	state, err := manager.buildInitialManagedStateLocked(password, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Wallets) != 1 || state.Wallets[0].Fingerprint != fingerprint {
		t.Fatalf("duplicate wallet was included in managed state: %#v", state.Wallets)
	}
	manager.accountProfile = &accountManagementProfile{
		Version: accountManagementProfileVersion, RootFingerprint: fingerprint,
		AccountID: strings.Repeat("a", 64), DeviceID: make([]byte, accountManagementDeviceIDSize),
	}
	root, err := manager.accountManagementRootWalletLocked()
	if err != nil {
		t.Fatal(err)
	}
	if root.Id != 1 {
		t.Fatalf("root wallet is not deterministic: got %d, want 1", root.Id)
	}
}

func TestLocalRGB11AccountsIgnoreDuplicateWalletFingerprints(t *testing.T) {
	const mnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	first := NewInternalWalletWithMnemonic(mnemonic, "", &chaincfg.TestNet4Params)
	duplicate := NewInternalWalletWithMnemonic(mnemonic, "", &chaincfg.TestNet4Params)
	if first == nil || duplicate == nil {
		t.Fatal("create duplicate test wallets")
	}
	first.id = 1
	duplicate.id = 2
	manager := &Manager{walletInfoMap: map[int64]*WalletInfo{
		2: {WalletInDB: WalletInDB{Id: 2, Accounts: 2}, Wallet: duplicate},
		1: {WalletInDB: WalletInDB{Id: 1, Accounts: 2}, Wallet: first},
	}}

	accounts := manager.localRGB11Accounts()
	if len(accounts) != 2 {
		t.Fatalf("duplicate wallet accounts were not deduplicated: got %d, want 2", len(accounts))
	}
	for index, account := range accounts {
		if account.WalletID != 1 || account.AccountIndex != uint32(index) {
			t.Fatalf("non-canonical RGB11 account selected: %+v", account)
		}
	}
}
