package e2e

import (
	"net/url"
	"strings"
	"testing"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

// Close and reopen the real SDK database, rather than editing runtime private
// fields to simulate locking. No mnemonic or password is written to evidence.
func TestSDKAccountReviewPasswordRestart(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	indexerFor := func(node *testHarness) *sdkcommon.Indexer {
		raw, err := node.IndexerURL("testnet")
		require.NoError(t, err)
		u, err := url.Parse(raw)
		require.NoError(t, err)
		return &sdkcommon.Indexer{Scheme: u.Scheme, Host: u.Host, Proxy: strings.Trim(u.Path, "/")}
	}
	config := &sdkcommon.Config{
		Env: "test", Chain: "testnet",
		IndexerL1: indexerFor(network.Bootstrap), IndexerL2: indexerFor(network.Core),
		Peers: []string{
			"s@" + network.Core.nodePubKey + "@http://" + network.Core.stpAddr + "/testnet",
			"b@" + network.Bootstrap.nodePubKey + "@http://" + network.Bootstrap.stpAddr + "/testnet",
		},
	}
	dir := t.TempDir()
	database := indexerdb.NewKVDB(dir)
	require.NotNil(t, database)
	manager := wallet.NewManager(config, database)
	require.NotNil(t, manager)
	t.Cleanup(func() { manager.Close(); database.Close() })
	reopen := func() {
		manager.Close()
		database.Close()
		database = indexerdb.NewKVDB(dir)
		require.NotNil(t, database)
		manager = wallet.NewManager(config, database)
		require.NotNil(t, manager)
	}

	rootID, err := manager.ImportWallet(dkvsClientMnemonic, accountReviewPassword)
	require.NoError(t, err)
	require.NoError(t, manager.InitializeAccountManagement(accountReviewPassword))
	require.NoError(t, manager.UpdateWalletName(rootID, "Persistent Root"))
	require.NoError(t, manager.EnsureAccount(rootID, 1, "Root Savings", "did:restart:root"))
	secondaryID, err := manager.ImportWallet(coreMnemonic, accountReviewPassword)
	require.NoError(t, err)
	require.NoError(t, manager.EnsureAccount(secondaryID, 2, "Daily", "did:restart:daily"))
	manager.SwitchAccount(2)
	before := manager.GetWalletCatalog()
	identity := manager.GetAccountManagementStatus()
	const changedPassword = "account-review-new-local-password"

	require.Error(t, manager.ChangePassword("incorrect-old-password", changedPassword))
	require.Equal(t, before, manager.GetWalletCatalog())
	for _, entry := range before {
		require.NotEmpty(t, manager.GetMnemonic(entry.ID, accountReviewPassword))
		require.Empty(t, manager.GetMnemonic(entry.ID, changedPassword))
	}
	require.NoError(t, manager.ChangePassword(accountReviewPassword, changedPassword))
	require.Equal(t, before, manager.GetWalletCatalog())
	for _, entry := range before {
		require.Empty(t, manager.GetMnemonic(entry.ID, accountReviewPassword))
		require.NotEmpty(t, manager.GetMnemonic(entry.ID, changedPassword))
	}

	reopen()
	require.Nil(t, manager.GetWallet())
	coldCatalog := manager.GetWalletCatalog()
	_, err = manager.UnlockWallet(accountReviewPassword)
	require.Error(t, err)
	require.Nil(t, manager.GetWallet(), "wrong password must not install the selected wallet")
	require.Equal(t, coldCatalog, manager.GetWalletCatalog())
	for _, entry := range before {
		require.Nil(t, manager.FindWalletById(entry.ID), "wrong password must not partially unlock any wallet")
	}
	selected, err := manager.UnlockWallet(changedPassword)
	require.NoError(t, err)
	require.Equal(t, secondaryID, selected)
	require.Equal(t, uint32(2), manager.GetCurrentAccountId())
	require.Equal(t, before, manager.GetWalletCatalog())
	require.Equal(t, rootID, manager.GetAccountManagementStatus().RootWalletID)
	require.Equal(t, identity.AccountID, manager.GetAccountManagementStatus().AccountID)
	_, err = manager.AccountPreflight(changedPassword, nil)
	require.NoError(t, err, "the account-management secret must also decrypt under the new password")

	_, err = manager.ImportWallet(dkvsClientMnemonic, changedPassword)
	require.ErrorIs(t, err, wallet.ErrWalletAlreadyExists)
	require.Equal(t, before, manager.GetWalletCatalog())
	require.NoError(t, manager.DeleteWallet(secondaryID))
	require.Equal(t, rootID, manager.GetCurrentWalletId())
	require.Zero(t, manager.GetCurrentAccountId())
	require.Error(t, manager.DeleteWallet(rootID), "the final/root wallet must remain protected")
	reopen()
	selected, err = manager.UnlockWallet(changedPassword)
	require.NoError(t, err)
	require.Equal(t, rootID, selected)
	require.Len(t, manager.GetWalletCatalog(), 1)
	require.Equal(t, "Persistent Root", manager.GetWalletCatalog()[0].Name)
	require.Equal(t, identity.AccountID, manager.GetAccountManagementStatus().AccountID)
}
