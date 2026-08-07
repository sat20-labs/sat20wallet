package wallet

import (
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/satoshinet/chaincfg"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestAccountAmountPerBlockCoversRecoveryRecords(t *testing.T) {
	defaults := dkvsindexer.NetworkDefaults{
		Enabled:                  true,
		AutopayMinAmountPerBlock: "1",
		FullRecordFeePerBlock:    "0.1",
	}
	amount, err := accountAmountPerBlock(defaults, 100)
	require.NoError(t, err)
	require.Equal(t, "10", amount)
}

func TestAccountAmountPerBlockRespectsNetworkMinimum(t *testing.T) {
	defaults := dkvsindexer.NetworkDefaults{
		Enabled:                  true,
		AutopayMinAmountPerBlock: "12",
		FullRecordFeePerBlock:    "0.1",
	}
	amount, err := accountAmountPerBlock(defaults, 100)
	require.NoError(t, err)
	require.Equal(t, "12", amount)
}

func TestAccountStorageTestnetDefaultsProduceContinuousQuote(t *testing.T) {
	defaults := dkvsindexer.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	require.Equal(t, "1", defaults.AutopayMinAmountPerBlock)
	require.Equal(t, "0.1", defaults.FullRecordFeePerBlock)
	amount, err := accountAmountPerBlock(defaults, 0)
	require.NoError(t, err)
	require.Equal(t, "10", amount)
	cost, err := multiplyDecimal(amount, accountPaidDefaultFundingBlocks)
	require.NoError(t, err)
	require.Equal(t, "10000", cost)
	annual, err := multiplyDecimal(amount, 2_628_000)
	require.NoError(t, err)
	require.Equal(t, "26280000", annual)
}

func TestAccountRecordCountRejectsInsufficientCapacity(t *testing.T) {
	_, err := normalizeAccountRecordCount(accountMinimumRecordCount - 1)
	require.Error(t, err)
	require.Less(t, accountRequiredRecords, accountMinimumRecordCount)
}

func TestAccountPaidStorageAuthorizationCanReuseActiveDelegate(t *testing.T) {
	defaults := dkvsindexer.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	authorization := accountPaidStorageAuthorization(AccountIndexerLocation{
		Scheme: "https", Host: "indexer.test", Proxy: "satsnet/testnet",
	}, defaults, 100, "10", "10000", "", true)
	require.Empty(t, authorization.TransactionID)
	require.Equal(t, defaults.AutopayContract, authorization.Summary.ContractAddress)
	require.Contains(t, authorization.Summary.Description, "复用")
	require.Equal(t, "10", authorization.Summary.AmountPerBlock)
}

func TestPrepareAccountRestoreRejectsDuplicateIdentityWithoutWriting(t *testing.T) {
	const mnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	database := newMemoryKVDB()
	manager := &Manager{
		db: database, status: &Status{SoftwareVer: SOFTWARE_VERSION, DBver: DB_VERSION, CurrentChain: "testnet"},
		walletInfoMap: make(map[int64]*WalletInfo),
	}
	backup := account.Backup{Version: account.Version, Wallets: []account.WalletBackup{
		{Name: "Root", Mnemonic: mnemonic, AccountCount: 1, SubAccounts: []account.SubAccount{{Index: 0, Name: "Account 1"}}},
		{Name: "Duplicate", Mnemonic: mnemonic, AccountCount: 1, SubAccounts: []account.SubAccount{{Index: 0, Name: "Account 1"}}},
	}}
	manager.mutex.Lock()
	_, err := manager.prepareAccountRestoreLocked(backup, "password123")
	manager.mutex.Unlock()
	if err == nil {
		t.Fatal("duplicate wallet identity was accepted")
	}
	wallets, loadErr := loadAllWalletFromDB(database)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(wallets) != 0 {
		t.Fatalf("prepare phase wrote partial wallets: %d", len(wallets))
	}
}

func TestPersistPreparedAccountRestoreCommitsCatalogAndStatusTogether(t *testing.T) {
	const mnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	database := newMemoryKVDB()
	manager := &Manager{
		db: database, status: &Status{SoftwareVer: SOFTWARE_VERSION, DBver: DB_VERSION, CurrentChain: "testnet"},
		walletInfoMap: make(map[int64]*WalletInfo),
	}
	originalStatus := manager.status
	backup := account.Backup{Version: account.Version, Wallets: []account.WalletBackup{{
		Name: "Root", Mnemonic: mnemonic, AccountCount: 2,
		SubAccounts: []account.SubAccount{{Index: 0, Name: "Primary", DID: "did:root"}, {Index: 1, Name: "Second", DID: "did:second"}},
	}}}
	manager.mutex.Lock()
	prepared, err := manager.prepareAccountRestoreLocked(backup, "password123")
	if err == nil {
		err = manager.persistPreparedAccountRestoreLocked(prepared, nil)
	}
	manager.mutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	wallets, err := loadAllWalletFromDB(database)
	if err != nil {
		t.Fatal(err)
	}
	if len(wallets) != 1 || manager.wallet == nil || manager.status.CurrentWallet == 0 {
		t.Fatalf("restored state is incomplete: wallets=%d status=%+v", len(wallets), manager.status)
	}
	if manager.status != originalStatus {
		t.Fatal("account restore replaced the live status pointer")
	}
	encodedStatus, err := database.Read([]byte(DB_KEY_STATUS))
	if err != nil || len(encodedStatus) == 0 {
		t.Fatalf("status was not committed with wallets: %v", err)
	}
}
