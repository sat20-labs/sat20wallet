package wallet

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Use the production constructor, encrypted wallet catalog and real on-disk
// KVDB. The existing SP public-data fixture controls only the L1 boundary.
func TestSilentPayProductionManagerLifecycleE2E(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wallet")
	network := "testnet"
	remote := newRGB11MemoryDKVSHTTP()
	assertSPLocalNoUploads(t, remote)
	open := func() (*Manager, func()) {
		return openSPLocalDevice(t, path, network, remote)
	}
	manager, close := open()
	const password = "sp-lifecycle-test-only"
	id, err := manager.ImportWallet(silentPayE2EMnemonic, password)
	require.NoError(t, err)
	discoverInactiveAccount(t, manager)
	require.NoError(t, manager.SwitchAccount(1))
	address, err := manager.EnableSilentPay(id, 1)
	require.NoError(t, err)
	// Automatic provider registration must survive normal initialization.
	found := false
	for _, provider := range manager.accountManagedDataProviders() {
		if provider.ID() == silentPayManagedProviderID {
			found = true
		}
	}
	require.True(t, found, "production NewManager must register the SP provider")
	payment := spPWAMakePayment(t, manager.GetWallet().(*InternalWallet), 1, 83, 1, 15, 54321)
	manager.SetIndexerHttpClient(spPWANewIndexer(payment))
	_, err = manager.SyncSilentPayLive(context.Background())
	require.NoError(t, err)
	_, err = manager.UnlockWallet("incorrect-password")
	require.Error(t, err)
	_, err = manager.UnlockWallet(password)
	require.NoError(t, err)
	close()

	reopened, closeAgain := open()
	_, _, err = reopened.SilentPayAccount(id, 1)
	require.Error(t, err, "cold encrypted wallet must require authentication")
	_, err = reopened.UnlockWallet("incorrect-password")
	require.Error(t, err)
	restoredID, err := reopened.UnlockWallet(password)
	require.NoError(t, err)
	require.Equal(t, id, restoredID)
	discoverInactiveAccount(t, reopened)
	enabled, restoredAddress, err := reopened.SilentPayAccount(id, 1)
	require.NoError(t, err)
	require.True(t, enabled)
	require.Equal(t, address, restoredAddress)
	balance, err := reopened.LastSyncedSilentPay(id, 1)
	require.NoError(t, err)
	require.True(t, balance.Complete)
	require.Equal(t, int64(54321), balance.Balance)
	require.Len(t, balance.Outputs, 1)
	_, err = reopened.LastSyncedSilentPay(id, 0)
	require.Error(t, err, "disabled account must not inherit the other account's funds")
	accountZeroAddress, err := reopened.EnableSilentPay(id, 0)
	require.NoError(t, err)
	require.NotEqual(t, address, accountZeroAddress)
	_, err = reopened.LastSyncedSilentPay(id, 0)
	require.ErrorContains(t, err, "no complete balance scan", "enabled account must not inherit another account's checkpoint")
	require.NoError(t, reopened.SwitchAccount(0))
	require.NoError(t, reopened.SwitchAccount(1))
	_, sameAddress, err := reopened.SilentPayAccount(id, 1)
	require.NoError(t, err)
	require.Equal(t, address, sameAddress)
	closeAgain()

	network = "mainnet"
	otherNetwork, closeOtherNetwork := open()
	mainnetID, err := otherNetwork.UnlockWallet(password)
	require.NoError(t, err)
	require.Equal(t, id, mainnetID)
	mainnetEnabled, mainnetAddress, err := otherNetwork.SilentPayAccount(id, 1)
	require.NoError(t, err)
	require.True(t, mainnetEnabled)
	require.NotEqual(t, address, mainnetAddress)
	_, err = otherNetwork.LastSyncedSilentPay(id, 1)
	require.ErrorContains(t, err, "no complete balance scan", "authenticated mainnet wallet must not read testnet funds")
	closeOtherNetwork()

	network = "testnet"
	backOnTestnet, _ := open()
	_, err = backOnTestnet.UnlockWallet(password)
	require.NoError(t, err)
	_, restoredAddress, err = backOnTestnet.SilentPayAccount(id, 1)
	require.NoError(t, err)
	require.Equal(t, address, restoredAddress)
	balance, err = backOnTestnet.LastSyncedSilentPay(id, 1)
	require.NoError(t, err)
	require.Equal(t, int64(54321), balance.Balance, "network round trip must preserve the original checkpoint")
}
