package wallet

import (
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestAccountGroupGate(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })

	t.Run("unrelated_mail", func(t *testing.T) {
		manager, _, store, _ := accountTTLNoopFixture(t)
		keys := accountGroupRequiredKeys(t, manager)
		id := manager.GetCurrentWalletId()
		replica := newDKVSReplicaStore(manager.db)
		mailKey, err := btcec.NewPrivateKey()
		require.NoError(t, err)
		mailID := dkvsindexer.AccountID(mailKey.PubKey().SerializeCompressed())
		mailPrefix, err := mailboxSubscriptionTarget(mailID)
		require.NoError(t, err)
		require.NoError(t, manager.dkvs.runTransport(func() error {
			require.NoError(t, manager.EnsureAccount(id, 1, "Savings", ""))
			require.NoError(t, replica.AddRegisteredPrefix(store.client.replicaNamespace, mailPrefix))
			state, err := replica.LoadSubscriptionState(store.client.replicaNamespace)
			require.NoError(t, err)
			require.Equal(t, DKVSSubscriptionSyncing, state.Status)
			require.ErrorIs(t, manager.dkvs.requirePathsReady(store.client, keys[:2]), ErrDKVSPathNotSynced)
			require.NoError(t, manager.dkvs.requireGroupReady(store.client, keys[:2]))
			require.NoError(t, manager.UpdateAccountMetadata(id, 1, "Travel", ""))
			require.NoError(t, manager.UpdateWalletName(id, "PWA Root"))
			return nil
		}))
		manager.mutex.RLock()
		accountName := manager.walletInfoMap[id].AccountNames[1]
		walletName := manager.walletInfoMap[id].Name
		accountID := manager.accountProfile.AccountID
		manager.mutex.RUnlock()
		require.Equal(t, "Travel", accountName)
		require.Equal(t, "PWA Root", walletName)
		modules, err := manager.listAccountLocal(accountID)
		require.NoError(t, err)
		walletModule, err := manager.readAccountLocal(accountID, "wallets", AccountManagedDataGlobalScope)
		require.NoError(t, err)
		require.NotNil(t, walletModule)
		require.NotEmpty(t, walletModule.Key)
		require.Greater(t, walletModule.Generation, walletModule.Confirmed,
			"the wallet catalog mutation must be pending in its actual module row")
		pendingModules := 0
		for _, module := range modules {
			if module.Generation > module.Confirmed {
				pendingModules++
			}
		}
		require.Positive(t, pendingModules, "per-module durable changes must remain visible while unrelated mail is syncing")
	})

	t.Run("required_missing", func(t *testing.T) {
		manager, _, store, _ := accountTTLNoopFixture(t)
		keys := accountGroupRequiredKeys(t, manager)
		id := manager.GetCurrentWalletId()
		replica := newDKVSReplicaStore(manager.db)
		prefix, _, err := dkvsManagedPathForKey(keys[1])
		require.NoError(t, err)
		require.NoError(t, manager.dkvs.runTransport(func() error {
			require.NoError(t, replica.RemoveRegisteredPrefix(store.client.replicaNamespace, prefix))
			require.ErrorIs(t, manager.dkvs.requireGroupReady(store.client, keys[:2]), ErrDKVSPathNotSynced)
			require.ErrorIs(t, manager.UpdateWalletName(id, "blocked"), ErrDKVSPathNotSynced)
			return nil
		}))
		manager.mutex.RLock()
		name := manager.walletInfoMap[id].Name
		manager.mutex.RUnlock()
		require.NotEqual(t, "blocked", name)
	})

	t.Run("offline", func(t *testing.T) {
		manager, _, store, _ := accountTTLNoopFixture(t)
		keys := accountGroupRequiredKeys(t, manager)
		id := manager.GetCurrentWalletId()
		require.NoError(t, manager.dkvs.runTransport(func() error {
			manager.dkvs.invalidateOnlineSession()
			require.ErrorIs(t, manager.dkvs.requireOnlineGroup(store.client, keys[:2]), ErrDKVSPathNotSynced)
			require.ErrorIs(t, manager.UpdateWalletName(id, "blocked"), ErrDKVSPathNotSynced)
			return nil
		}))
		manager.mutex.RLock()
		name := manager.walletInfoMap[id].Name
		manager.mutex.RUnlock()
		require.NotEqual(t, "blocked", name)
	})
}

func accountGroupRequiredKeys(t *testing.T, manager *Manager) []string {
	t.Helper()
	root, err := manager.accountManagementRootWallet()
	require.NoError(t, err)
	indexKey, err := account.ManagedIndexKey(manager.accountProfile.AccountID)
	require.NoError(t, err)
	wrapperKey, err := accountRootWrapperKey(root)
	require.NoError(t, err)
	return []string{wrapperKey, indexKey}
}
