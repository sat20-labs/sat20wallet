package wallet

import (
	"bytes"
	"context"
	"errors"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
)

func newAccountManagementAutoTestManager(t *testing.T) *Manager {
	t.Helper()
	database := newMemoryKVDB()
	manager := &Manager{
		db: database, status: newDefaultStatus(),
		cfg: &sdkcommon.Config{
			Env:   "test",
			Chain: _chain,
			IndexerL2: &sdkcommon.Indexer{
				Scheme: "http",
				Host:   "dkvs.test",
				Proxy:  _chain,
			},
		},
		walletInfoMap:        make(map[int64]*WalletInfo),
		tickerInfoMap:        make(map[string]*indexer.TickerInfo),
		utxoLockerL1:         NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN),
		utxoLockerL2:         NewUtxoLocker(database, nil, L2_NETWORK_SATOSHI),
		managedDataProviders: make(map[string]AccountManagedDataProvider),
	}
	manager.http = newRGB11MemoryDKVSHTTP()
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
		status.StateSeq != 1 || status.PendingChanges != 0 || !status.ManagedDataDirty {
		t.Fatalf("initial account management status=%+v", status)
	}
	providers := manager.accountManagedDataProviders()
	if len(providers) != 1 || providers[0].ID() != rgb11AccountManagedProviderID {
		t.Fatalf("built-in account-managed providers=%v", providers)
	}
}

func TestImportFirstMnemonicWalletAfterAuthoritativeRootNotFoundEnablesAccountManagement(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager := newAccountManagementAutoTestManager(t)
	store := &memoryAccountRootWrapperStore{records: make(map[string]*dkvsValue)}
	if _, err := manager.recoverAccountManagementFromRootMnemonic(context.Background(),
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"password", store, AccountIndexerLocation{}); !errors.Is(err, ErrRootAccountNotFound) {
		t.Fatalf("root discovery error=%v", err)
	}
	walletID, err := manager.ImportWallet(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"password",
	)
	if err != nil {
		t.Fatal(err)
	}
	assertInitialAccountManagementStatus(t, manager, walletID)
}

func TestImportFirstMnemonicWalletActivationIsAtomic(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager := newAccountManagementAutoTestManager(t)
	store := &memoryAccountRootWrapperStore{records: make(map[string]*dkvsValue)}
	if _, err := manager.recoverAccountManagementFromRootMnemonic(context.Background(),
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"password", store, AccountIndexerLocation{}); !errors.Is(err, ErrRootAccountNotFound) {
		t.Fatalf("root discovery error=%v", err)
	}
	originalDB := manager.db
	manager.db = &passwordChangeFailFlushDB{KVDB: originalDB}

	if _, err := manager.ImportWallet(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"password",
	); err == nil {
		t.Fatal("first mnemonic import unexpectedly survived an atomic flush failure")
	}
	if manager.wallet != nil || len(manager.walletInfoMap) != 0 || manager.accountProfile != nil ||
		len(manager.accountSecret) != 0 || manager.accountPassword != "" {
		t.Fatal("failed first mnemonic import changed live account state")
	}
	if _, err := originalDB.Read(accountManagementProfileKey()); !errors.Is(err, indexer.ErrKeyNotFound) {
		t.Fatalf("failed import persisted account profile: %v", err)
	}
	if wallets, err := loadAllWalletFromDB(originalDB); err != nil || len(wallets) != 0 {
		t.Fatalf("failed import persisted wallet catalog: wallets=%d err=%v", len(wallets), err)
	}
}

func TestImportFirstMnemonicWalletDoesNotReuseAnotherMnemonicAuthorization(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager := newAccountManagementAutoTestManager(t)
	store := &memoryAccountRootWrapperStore{records: make(map[string]*dkvsValue)}
	if _, err := manager.recoverAccountManagementFromRootMnemonic(context.Background(),
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"password", store, AccountIndexerLocation{}); !errors.Is(err, ErrRootAccountNotFound) {
		t.Fatalf("root discovery error=%v", err)
	}
	if _, err := manager.ImportWallet(
		"legal winner thank year wave sausage worth useful legal winner thank yellow",
		"password",
	); err != nil {
		t.Fatal(err)
	}
	if manager.GetAccountManagementStatus().Active || len(manager.accountSecret) != 0 {
		t.Fatal("another mnemonic reused the root-not-found authorization")
	}
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

func TestAccountRecoveryPackagesReuseActiveAccountSecret(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager := newAccountManagementAutoTestManager(t)
	if _, _, err := manager.CreateWallet("password"); err != nil {
		t.Fatal(err)
	}
	backup, err := manager.ExportAccountBackup("password", nil)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := account.RootBootstrapBackup(backup)
	if err != nil {
		t.Fatal(err)
	}
	questions := []account.QuestionAnswer{
		{Question: account.KnowledgeQuestion{ID: "one", Prompt: "one"}, Answer: "answer one", Confirmation: "answer one"},
		{Question: account.KnowledgeQuestion{ID: "two", Prompt: "two"}, Answer: "answer two", Confirmation: "answer two"},
		{Question: account.KnowledgeQuestion{ID: "three", Prompt: "three"}, Answer: "answer three", Confirmation: "answer three"},
	}
	options := account.CreateOptions{AccountID: manager.accountProfile.AccountID,
		Backup: bootstrap, RecoveryMode: account.RecoveryMode2Of2, Questions: questions}
	originalSecret := append([]byte(nil), manager.accountSecret...)
	defer zeroBytes(originalSecret)
	packageIDs := make(map[string]struct{}, 2)
	for range 2 {
		pkg, err := manager.CreateAccountRecoveryPackage(options)
		if err != nil {
			t.Fatal(err)
		}
		packageIDs[pkg.Envelope.Locator.PackageID] = struct{}{}
		dkvsShare, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule,
			pkg.KnowledgeBundle, []account.AnswerAttempt{{QuestionID: "one", Answer: "answer one"},
				{QuestionID: "two", Answer: "answer two"}})
		if err != nil {
			t.Fatal(err)
		}
		_, recovered, err := account.RecoverAccount(pkg.Envelope, pkg.UserShare, dkvsShare)
		if err != nil {
			t.Fatal(err)
		}
		matches := bytes.Equal(recovered, originalSecret)
		zeroBytes(recovered)
		if !matches {
			t.Fatal("recovery package replaced the active account secret")
		}
	}
	if len(packageIDs) != 2 {
		t.Fatal("consecutive recovery packages reused a package id")
	}
	options.AccountID = "wrong-account"
	if _, err := manager.CreateAccountRecoveryPackage(options); err == nil {
		t.Fatal("recovery package accepted a mismatched account id")
	}
	manager.clearAccountManagementSession()
	options.AccountID = manager.accountProfile.AccountID
	if _, err := manager.CreateAccountRecoveryPackage(options); err == nil {
		t.Fatal("recovery package accepted a locked account secret")
	}
}

func TestChangePasswordReencryptsWalletsAndAccountSecret(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager := newAccountManagementAutoTestManager(t)
	if _, _, err := manager.CreateWallet("123456"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.CreateWallet("123456"); err != nil {
		t.Fatal(err)
	}
	originalSecret := append([]byte(nil), manager.accountSecret...)
	defer zeroBytes(originalSecret)
	if err := manager.ChangePassword("wrong-password", "new-password"); err == nil {
		t.Fatal("wrong old password was accepted")
	}
	if err := manager.ChangePassword("123456", "new-password"); err != nil {
		t.Fatal(err)
	}
	for _, info := range manager.walletInfoMap {
		if _, err := manager.loadWalletSecret(info, "123456"); err == nil {
			t.Fatalf("wallet %d still decrypts with old password", info.Id)
		}
		if _, err := manager.loadWalletSecret(info, "new-password"); err != nil {
			t.Fatalf("wallet %d does not decrypt with new password: %v", info.Id, err)
		}
	}
	manager.clearAccountManagementSession()
	manager.mutex.Lock()
	oldErr := manager.unlockAccountManagementLocked("123456")
	newErr := manager.unlockAccountManagementLocked("new-password")
	secretMatches := bytes.Equal(manager.accountSecret, originalSecret)
	manager.mutex.Unlock()
	if oldErr == nil {
		t.Fatal("account secret still decrypts with old password")
	}
	if newErr != nil {
		t.Fatalf("account secret does not decrypt with new password: %v", newErr)
	}
	if !secretMatches {
		t.Fatal("password change replaced the account secret")
	}
}

type passwordChangeFailFlushDB struct{ indexer.KVDB }
type passwordChangeFailFlushBatch struct{ indexer.WriteBatch }

func (db *passwordChangeFailFlushDB) NewWriteBatch() indexer.WriteBatch {
	return &passwordChangeFailFlushBatch{WriteBatch: db.KVDB.NewWriteBatch()}
}

func (*passwordChangeFailFlushBatch) Flush() error {
	return errors.New("injected password change flush failure")
}

func TestChangePasswordFailureKeepsOldCredentials(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager := newAccountManagementAutoTestManager(t)
	if _, _, err := manager.CreateWallet("123456"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.CreateWallet("123456"); err != nil {
		t.Fatal(err)
	}
	originalDB := manager.db
	manager.db = &passwordChangeFailFlushDB{KVDB: originalDB}

	if err := manager.ChangePassword("123456", "new-password"); err == nil {
		t.Fatal("password change unexpectedly survived an atomic flush failure")
	}
	for _, info := range manager.walletInfoMap {
		if _, err := manager.loadWalletSecret(info, "123456"); err != nil {
			t.Fatalf("wallet %d lost its old password after failed change: %v", info.Id, err)
		}
		if _, err := manager.loadWalletSecret(info, "new-password"); err == nil {
			t.Fatalf("wallet %d accepted the uncommitted new password", info.Id)
		}
	}
	manager.clearAccountManagementSession()
	manager.mutex.Lock()
	oldErr := manager.unlockAccountManagementLocked("123456")
	zeroBytes(manager.accountSecret)
	manager.accountSecret = nil
	manager.accountPassword = ""
	newErr := manager.unlockAccountManagementLocked("new-password")
	manager.mutex.Unlock()
	if oldErr != nil {
		t.Fatalf("account secret lost its old password after failed change: %v", oldErr)
	}
	if newErr == nil {
		t.Fatal("account secret accepted the uncommitted new password")
	}
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
