package e2e

import (
	"context"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestRealSatoshiNetAccountManagementLifecycleAndConcurrentDevices(t *testing.T) {
	fixture := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, fixture.Network)

	primary, location := newWalletManagerForNode(t, fixture.Network.Bootstrap, dkvsClientMnemonic)
	_, err := primary.ImportWallet(bootstrapMnemonic, "123456")
	require.NoError(t, err)
	catalog := primary.GetWalletCatalog()
	require.Len(t, catalog, 2)
	rootID := catalog[0].ID
	secondaryID := catalog[1].ID
	// ImportWallet selects the newly imported wallet. Account management is
	// rooted in the first canonical wallet, so explicitly restore that context
	// before generating the locator and repository.
	require.NoError(t, primary.SwitchWallet(rootID, ""))

	authorization, err := primary.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
	require.NoError(t, err)
	root := primary.GetWallet()
	accountID := dkvsindexer.AccountID(root.GetPubKey().SerializeCompressed())
	repository, err := primary.NewAccountRepositoryForStorage(*authorization)
	require.NoError(t, err)
	accountManager := account.NewManager(repository)
	pkg, secret, err := accountManager.CreateRecoveryPackageWithSecret(account.CreateOptions{
		AccountID: accountID,
		Backup: account.Backup{Version: account.Version, Wallets: []account.WalletBackup{{
			Name: "Root", Mnemonic: dkvsClientMnemonic, AccountCount: 1,
			SubAccounts: []account.SubAccount{{Index: 0, Name: "Primary", DID: "did:root:0"}},
		}}},
		RecoveryMode: account.RecoveryMode2Of2,
		Questions:    e2eKnowledgeQuestions(),
	})
	require.NoError(t, err)
	defer clearBytes(secret)
	require.NoError(t, accountManager.Publish(context.Background(), *pkg))

	require.NoError(t, primary.ActivateAccountManagement(secret, "123456", *authorization,
		pkg.Envelope.Locator, "account://"+pkg.Envelope.Locator.PackageID))
	status := primary.GetAccountManagementStatus()
	require.True(t, status.Active)
	require.Equal(t, uint64(2), status.StateSeq)
	require.Zero(t, status.PendingChanges)
	require.Equal(t, rootID, status.RootWalletID)

	// Root protection and catalog boundaries.
	require.ErrorContains(t, primary.DeleteWallet(rootID), "cannot be deleted")
	require.Error(t, primary.UpdateAccountMetadata(rootID, 8, "invalid", ""))

	require.NoError(t, primary.UpdateWalletName(rootID, "Primary Vault"))
	require.NoError(t, primary.EnsureAccount(rootID, 2, "Cold Savings", "did:root:2"))
	require.NoError(t, primary.UpdateWalletName(secondaryID, "Disposable"))
	require.NoError(t, primary.DeleteWallet(secondaryID))
	require.GreaterOrEqual(t, primary.GetAccountManagementStatus().PendingChanges, 3)
	require.NoError(t, primary.SyncAccountManagementState(context.Background()))
	status = primary.GetAccountManagementStatus()
	require.Equal(t, uint64(3), status.StateSeq)
	require.Zero(t, status.PendingChanges)

	// A new device rejects wrong recovery material and restores the latest
	// encrypted managed state with the deleted wallet omitted.
	fresh, _ := newWalletManagerForNode(t, fixture.Network.Bootstrap, "")
	wrongSecret := append([]byte(nil), secret...)
	wrongSecret[0] ^= 0xff
	_, err = fresh.LoadAccountManagementStateForRecovery(location, pkg.Envelope.Locator,
		wrongSecret, dkvsClientMnemonic)
	require.Error(t, err)
	_, err = fresh.LoadAccountManagementStateForRecovery(location, pkg.Envelope.Locator,
		secret, coreMnemonic)
	require.ErrorContains(t, err, "root wallet")

	recovered, err := fresh.LoadAccountManagementStateForRecovery(location,
		pkg.Envelope.Locator, secret, dkvsClientMnemonic)
	require.NoError(t, err)
	require.Equal(t, uint64(3), recovered.Seq)
	_, err = fresh.RestoreAccountManagementState(*recovered, secret, "123456",
		pkg.Envelope.Locator, wallet.AccountManagementRestoreOptions{
			Location: location, StorageMode: wallet.AccountStorageTemporary,
			RecordTTL:     authorization.RecordOptions.TTL,
			PublicLocator: "account://" + pkg.Envelope.Locator.PackageID,
		})
	require.NoError(t, err)
	freshCatalog := fresh.GetWalletCatalog()
	require.Len(t, freshCatalog, 1)
	require.Equal(t, "Primary Vault", freshCatalog[0].Name)
	require.Len(t, freshCatalog[0].Accounts, 3)
	require.Equal(t, "Cold Savings", freshCatalog[0].Accounts[2].Name)
	require.Equal(t, "did:root:2", freshCatalog[0].Accounts[2].DID)

	// Device B writes first. Device A is one revision stale but must merge its
	// own pending rename on top of B's remote account addition.
	freshRootID := freshCatalog[0].ID
	require.NoError(t, primary.UpdateWalletName(rootID, "Primary Vault Renamed"))
	require.NoError(t, fresh.EnsureAccount(freshRootID, 3, "Travel", "did:root:3"))
	require.NoError(t, fresh.SyncAccountManagementState(context.Background()))
	require.Equal(t, uint64(4), fresh.GetAccountManagementStatus().StateSeq)
	require.NoError(t, primary.SyncAccountManagementState(context.Background()))
	require.Equal(t, uint64(5), primary.GetAccountManagementStatus().StateSeq)
	require.NoError(t, fresh.SyncAccountManagementState(context.Background()))
	freshStatus := fresh.GetAccountManagementStatus()
	require.Equal(t, uint64(5), freshStatus.StateSeq)
	require.Zero(t, freshStatus.PendingChanges)

	finalCatalog := fresh.GetWalletCatalog()
	require.Len(t, finalCatalog, 1)
	require.Equal(t, "Primary Vault Renamed", finalCatalog[0].Name)
	require.Len(t, finalCatalog[0].Accounts, 4)
	require.Equal(t, "Travel", finalCatalog[0].Accounts[3].Name)
	require.Equal(t, "did:root:3", finalCatalog[0].Accounts[3].DID)

	// Recovery from a different endpoint must not expose endpoint-local state.
	other, otherLocation := newWalletManagerForNode(t, fixture.Network.Core, "")
	_, err = other.LoadAccountManagementStateForRecovery(otherLocation,
		pkg.Envelope.Locator, secret, dkvsClientMnemonic)
	require.Error(t, err)
}
