package wallet

import (
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/stretchr/testify/require"
)

// Open the production encrypted catalog on a real database. Attach the
// deterministic account endpoint before import/unlock creates a primary client.
// Each handle closes once, including when a business assertion fails.
func openSPLocalDevice(t *testing.T, path, network string, remote *rgb11MemoryDKVSHTTP) (*Manager, func()) {
	t.Helper()
	oldEnv, oldChain, oldMode, oldIndexerChain := _env, _chain, _mode, indexer.CHAIN
	t.Cleanup(func() { _env, _chain, _mode, indexer.CHAIN = oldEnv, oldChain, oldMode, oldIndexerChain })
	indexer.CHAIN = network
	cfg := &common.Config{Env: "test", Chain: network, Mode: "client",
		Peers:     []string{"b@" + indexer.GetBootstrapPubKey() + "@http://127.0.0.1:1/" + network},
		IndexerL1: &common.Indexer{Scheme: "http", Host: "127.0.0.1:1", Proxy: network},
		IndexerL2: &common.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: network}}
	database := NewKVDB(path)
	require.NotNil(t, database)
	manager := NewManager(cfg, database)
	if manager == nil {
		require.NoError(t, database.Close())
		t.Fatal("open SilentPay production manager")
	}
	manager.SetDKVSHttpClient(remote)
	closed := false
	close := func() {
		if !closed {
			manager.Close()
			err := database.Close()
			closed = true
			require.NoError(t, err)
		}
	}
	t.Cleanup(close)
	return manager, close
}

// These groups use local persistence or a user-exported standalone backup,
// not account DKVS backup. Discovery must not publish any private records.
func assertSPLocalNoUploads(t *testing.T, remote *rgb11MemoryDKVSHTTP) {
	t.Helper()
	t.Cleanup(func() {
		remote.mu.Lock()
		count := len(remote.records)
		remote.mu.Unlock()
		if count != 0 {
			t.Errorf("inactive SilentPay group uploaded %d records", count)
		}
	})
}

// Build the original two mnemonic identities through public wallet/catalog
// APIs. Do not invent IDs or copy live Manager/profile/secret fields.
func newSPWalletPairGroup(t *testing.T, secondNames []string) (*Manager, *InternalWallet, *InternalWallet) {
	t.Helper()
	remote := newRGB11MemoryDKVSHTTP()
	assertSPLocalNoUploads(t, remote)
	manager, _ := openSPLocalDevice(t, t.TempDir(), "testnet", remote)
	firstID, err := manager.ImportWallet(silentPayE2EMnemonic, "password")
	require.NoError(t, err)
	discoverInactiveAccount(t, manager)
	require.NoError(t, manager.UpdateWalletName(firstID, "Personal"))
	for index, name := range []string{"Main", "Savings"} {
		require.NoError(t, manager.EnsureAccount(firstID, uint32(index), name, ""))
	}
	first, ok := manager.GetWallet().(*InternalWallet)
	require.True(t, ok)
	secondID, err := manager.ImportWallet(silentPayOtherMnemonic, "password")
	require.NoError(t, err)
	require.NoError(t, manager.UpdateWalletName(secondID, "Secondary"))
	for index, name := range secondNames {
		require.NoError(t, manager.EnsureAccount(secondID, uint32(index), name, ""))
	}
	second, ok := manager.GetWallet().(*InternalWallet)
	require.True(t, ok)
	require.NotEqual(t, firstID, secondID)
	require.NoError(t, manager.SwitchWallet(firstID, "password"))
	return manager, first, second
}
