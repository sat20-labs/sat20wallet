package wallet

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountGuardianDerivationContextIsolation(t *testing.T) {
	root := NewInternalWalletWithMnemonic(accountRootWrapperTestMnemonic, "", GetChainParam())
	require.NotNil(t, root)
	accountID, err := dkvsAccountID(root)
	require.NoError(t, err)
	key, err := deriveAccountGuardianPrivateKey(root, "testnet", accountID)
	require.NoError(t, err)
	defer zeroWalletBytes(key)
	require.Len(t, key, 32)
	otherDevice := NewInternalWalletWithMnemonic(accountRootWrapperTestMnemonic, "", GetChainParam())
	otherDevice.SetSubAccount(7)
	same, err := deriveAccountGuardianPrivateKey(otherDevice, "testnet", accountID)
	require.NoError(t, err)
	defer zeroWalletBytes(same)
	require.Equal(t, key, same, "selection and device changes cannot alter root identity")
	differentNetwork, err := deriveAccountGuardianPrivateKey(root, "mainnet", accountID)
	require.NoError(t, err)
	defer zeroWalletBytes(differentNetwork)
	require.False(t, bytes.Equal(key, differentNetwork))
	differentAccount, err := deriveAccountGuardianPrivateKey(root, "testnet", "another-account")
	require.NoError(t, err)
	defer zeroWalletBytes(differentAccount)
	require.False(t, bytes.Equal(key, differentAccount))
	wrapper, err := deriveAccountRootWrapperKey(root, "testnet", accountID)
	require.NoError(t, err)
	defer zeroWalletBytes(wrapper)
	require.False(t, bytes.Equal(key, wrapper), "different encryption purposes cannot share a key")
	// The derivation must not erase or modify the root signing key.
	require.Equal(t, root.GetPubKeyByIndex(0), otherDevice.GetPubKeyByIndex(0))
	for _, context := range [][2]string{{"", accountID}, {"testnet", ""}} {
		_, err := deriveAccountGuardianPrivateKey(root, context[0], context[1])
		require.Error(t, err)
	}
	_, err = deriveAccountGuardianPrivateKey(nil, "testnet", accountID)
	require.Error(t, err)
}

func TestAccountGuardianIdentityDoesNotDependOnCurrentWallet(t *testing.T) {
	manager := newAccountManagementAutoTestManager(t)
	_, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password")
	require.NoError(t, err)
	require.NoError(t, manager.InitializeAccountManagement("password"))
	first, err := manager.GetOrCreateAccountGuardianIdentity("password")
	require.NoError(t, err)
	_, _, err = manager.CreateWallet("password")
	require.NoError(t, err)
	id := manager.GetWalletCatalog()[len(manager.GetWalletCatalog())-1].ID
	require.NoError(t, manager.EnsureAccount(id, 1, "second", ""))
	require.NoError(t, manager.SwitchWallet(id, "password"))
	require.NoError(t, manager.SwitchAccount(1))
	profile, err := manager.db.Read(accountManagementProfileKey())
	require.NoError(t, err)
	next, err := manager.GetOrCreateAccountGuardianIdentity("password")
	require.NoError(t, err)
	require.Equal(t, first, next)
	after, err := manager.db.Read(accountManagementProfileKey())
	require.NoError(t, err)
	require.Equal(t, profile, after, "deriving an identity must not dirty or persist backup data")
	_, err = manager.LoadAccountGuardianPrivateKey("incorrect-password")
	require.Error(t, err)
}
