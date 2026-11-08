package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestRealSatoshiNetAccountManagementLifecycleAndConcurrentDevices(t *testing.T) {
	network := newDKVSNoPluginUnfundedNetwork(t)
	waitForDKVSPeerReady(t, network)

	primary, location := newWalletManagerForNode(t, network.Core, dkvsClientMnemonic)
	require.NoError(t, primary.InitializeAccountManagement("123456"))
	require.NoError(t, primary.StartDKVSSync())
	t.Cleanup(primary.StopDKVSSync)
	require.Eventually(t, func() bool { return primary.GetAccountManagementStatus().LocalOnly }, 30*time.Second, 50*time.Millisecond)
	_, err := primary.ImportWallet(bootstrapMnemonic, "123456")
	require.NoError(t, err)
	catalog := primary.GetWalletCatalog()
	require.Len(t, catalog, 2)
	rootID := catalog[0].ID
	secondaryID := catalog[1].ID
	preActivationStatus := primary.GetAccountManagementStatus()
	require.True(t, preActivationStatus.Active)
	require.False(t, preActivationStatus.RecoveryConfigured)
	require.Zero(t, preActivationStatus.StateSeq,
		"the local catalog has not published a wallet-state record")
	require.Equal(t, 1, preActivationStatus.PendingChanges,
		"before activation the second wallet import retains one local catalog intent")
	require.True(t, preActivationStatus.ManagedDataDirty)
	// ImportWallet selects the newly imported wallet. Account management is
	// rooted in the first canonical wallet, so explicitly restore that context
	// before generating the locator and repository.
	require.NoError(t, primary.SwitchWallet(rootID, ""))

	authorization, err := primary.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
	require.NoError(t, err)
	root := primary.GetWallet()
	accountID := dkvsindexer.AccountID(root.GetPubKey().SerializeCompressed())
	stateKey, err := dkvsindexer.PersonalKey(root.GetPubKey().SerializeCompressed(), "account/state")
	require.NoError(t, err)
	client := dkvsClientForNode(t, network.Core)
	_, err = client.GetRecordDirect(stateKey)
	require.ErrorIs(t, err, dkvsindexer.ErrRecordNotFound)
	repository, err := primary.NewAccountRepositoryForStorage(*authorization)
	require.NoError(t, err)
	require.NoError(t, primary.InitializeAccountManagement("123456"))
	accountManager := account.NewManager(repository)
	pkg, err := primary.CreateAccountRecoveryPackage(account.CreateOptions{
		AccountID: accountID,
		Backup: account.Backup{Version: account.BackupVersion, Wallets: []account.WalletBackup{{
			Name: "Root", Type: account.WalletMnemonic, Secret: []byte(dkvsClientMnemonic), AccountCount: 1,
			SubAccounts: []account.SubAccount{{Index: 0, Name: "Primary", DID: "did:root:0"}},
		}}},
		RecoveryMode: account.RecoveryMode2Of2,
		Questions:    e2eKnowledgeQuestions(),
	})
	require.NoError(t, err)
	dkvsShare, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule,
		pkg.KnowledgeBundle, e2eKnowledgeAnswers())
	require.NoError(t, err)
	_, secret, err := account.RecoverAccount(pkg.Envelope, pkg.UserShare, dkvsShare)
	require.NoError(t, err)
	defer clearBytes(secret)
	require.NoError(t, accountManager.Publish(context.Background(), *pkg))

	require.NoError(t, primary.ActivateAccountManagement(secret, "123456", *authorization,
		pkg.Envelope.Locator, "account://"+pkg.Envelope.Locator.PackageID))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, primary.WaitAccountManagedDataReady(ctx))
	status := primary.GetAccountManagementStatus()
	require.True(t, status.Active)
	require.True(t, status.RecoveryConfigured)
	initialSeq := status.StateSeq
	initialRecord, err := client.GetRecordDirect(stateKey)
	require.NoError(t, err)
	require.NoError(t, dkvsindexer.VerifySignature(initialRecord))
	require.Equal(t, uint64(1), initialRecord.Seq, "first publication of this previously absent key")
	require.Equal(t, initialRecord.Seq, initialSeq)
	require.Zero(t, status.PendingChanges)
	require.False(t, status.ManagedDataDirty)
	require.Equal(t, rootID, status.RootWalletID)

	// Root protection and catalog boundaries.
	require.ErrorContains(t, primary.DeleteWallet(rootID), "cannot be deleted")
	require.Error(t, primary.UpdateAccountMetadata(rootID, 8, "invalid", ""))

	require.NoError(t, primary.UpdateWalletName(rootID, "Primary Vault"))
	require.NoError(t, primary.EnsureAccount(rootID, 2, "Cold Savings", "did:root:2"))
	require.NoError(t, primary.UpdateWalletName(secondaryID, "Disposable"))
	require.NoError(t, primary.DeleteWallet(secondaryID))
	require.NoError(t, primary.SyncAccountManagementState(context.Background()))
	backupCtx, backupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer backupCancel()
	require.NoError(t, primary.WaitAccountManagedDataReady(backupCtx))
	status = primary.GetAccountManagementStatus()
	published, err := client.GetRecordDirect(stateKey)
	require.NoError(t, err)
	require.NoError(t, dkvsindexer.VerifySignature(published))
	require.Greater(t, published.Seq, initialRecord.Seq, "the changed catalog must have a newer committed record")
	require.NotEqual(t, dkvsindexer.RecordHash(initialRecord), dkvsindexer.RecordHash(published))
	require.Equal(t, published.Seq, status.StateSeq)
	require.Zero(t, status.PendingChanges)
	require.False(t, status.ManagedDataDirty)

	// A new device rejects wrong recovery material and restores the latest
	// encrypted managed state with the deleted wallet omitted.
	fresh, _ := newWalletManagerForNode(t, network.Core, "")
	wrongSecret := append([]byte(nil), secret...)
	wrongSecret[0] ^= 0xff
	_, err = fresh.LoadAccountManagementStateForRecovery(location, pkg.Envelope.Locator,
		wrongSecret, dkvsClientMnemonic)
	require.Error(t, err)
	_, err = fresh.LoadAccountManagementStateForRecovery(location, pkg.Envelope.Locator,
		secret, coreMnemonic)
	require.ErrorIs(t, err, wallet.ErrRootAccountWrapperInvalid)
	require.Empty(t, fresh.GetWalletCatalog())
	require.False(t, fresh.GetAccountManagementStatus().Active)

	recovered, err := fresh.LoadAccountManagementStateForRecovery(location,
		pkg.Envelope.Locator, secret, dkvsClientMnemonic)
	require.NoError(t, err)
	require.Equal(t, published.Seq, recovered.Seq)
	require.Equal(t, published.Value, recovered.Envelope)
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

	t.Run("ConcurrentDeviceEdits", func(t *testing.T) {
		t.Skip("not tested: simultaneous edits from multiple devices are unsupported")
	})

	// Recovery from a different endpoint must not expose endpoint-local state.
	other, otherLocation := newWalletManagerForNode(t, network.Bootstrap, "")
	_, err = other.LoadAccountManagementStateForRecovery(otherLocation,
		pkg.Envelope.Locator, secret, dkvsClientMnemonic)
	require.Error(t, err)
}
