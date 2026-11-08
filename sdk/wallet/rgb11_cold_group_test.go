package wallet

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Only selected cold groups coordinate a second real device before RGB seed.
type rgbColdPrepare func(*Manager, *rgb11MemoryDKVSHTTP, *rgb11AddressEvidence, *rgb11FlowIndexer)

func assertRGBColdEmpty(t *testing.T, manager *Manager) {
	t.Helper()
	walletID, err := manager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	if rgb11SnapshotHasState(snapshot) {
		t.Fatal("cold account preparation preloaded RGB business state")
	}
}

// The new DB and Manager own their generated identity and encrypted credential.
// Import and root-wrapper recovery, not copied Manager/profile fields, establish
// the target account on the source's actual Active endpoint.
func newRGBColdDevice(t *testing.T, source *Manager, remote *rgb11MemoryDKVSHTTP,
	evidence rgb11wallet.BitcoinEvidenceProvider, rpc *rgb11FlowIndexer, mnemonic, password string) *Manager {
	t.Helper()
	if source == nil || remote == nil || evidence == nil || rpc == nil {
		t.Fatal("cold group needs its real source, endpoint and Bitcoin transports")
	}
	assertRGBColdEmpty(t, source)
	expected := source.GetAccountManagementStatus()
	if !expected.RecoveryConfigured || expected.ActivationPending || !expected.DKVSWriteAllowed {
		t.Fatal("cold source has no verified Active account baseline")
	}
	accountIndex := source.GetCurrentAccountId()
	location, err := source.AccountIndexerLocation()
	if err != nil {
		t.Fatal(err)
	}
	node := source.getAccountServiceNode()
	if node == nil || node.Pubkey == nil || node.RPCClient() == nil {
		t.Fatal("cold source has no actual Core service")
	}
	oldEnv, oldChain, oldMode, oldIndexerChain := _env, _chain, _mode, indexer.CHAIN
	t.Cleanup(func() { _env, _chain, _mode, indexer.CHAIN = oldEnv, oldChain, oldMode, oldIndexerChain })
	_chain, indexer.CHAIN = "testnet", "testnet"
	cfg := &common.Config{Env: "test", Chain: "testnet", Mode: CLIENT_NODE,
		IndexerL1: &common.Indexer{Scheme: location.Scheme, Host: location.Host, Proxy: location.Proxy},
		IndexerL2: &common.Indexer{Scheme: location.Scheme, Host: location.Host, Proxy: location.Proxy},
		Peers: []string{
			"b@" + indexer.GetBootstrapPubKey() + "@http://127.0.0.1:1/stp/testnet",
			"s@" + hex.EncodeToString(node.Pubkey.SerializeCompressed()) + "@" + node.Host,
		}}
	database := NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("open cold device database failed")
	}
	manager := NewManager(cfg, database)
	if manager == nil {
		_ = database.Close()
		t.Fatal("initialize real cold Manager failed")
	}
	t.Cleanup(func() {
		manager.Close()
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
	})
	// Preserve the original RPC/evidence boundaries before real ImportWallet
	// can begin discovery. No worker uses a temporary substitute endpoint.
	manager.SetDKVSHttpClient(remote)
	manager.SetServerNodeHttpClient(node.RPCClient())
	manager.SetIndexerHttpClient(rpc)
	manager.rgbManager.evidence = evidence
	if err := manager.SetAccountServiceNode(node); err != nil {
		t.Fatal(err)
	}
	useRGB11TestCoreAuthority(t, manager)
	if _, err := manager.ImportWallet(mnemonic, password); err != nil {
		t.Fatal(err)
	}
	imported := manager.GetAccountManagementStatus()
	if imported.RootFingerprint != expected.RootFingerprint || manager.GetCurrentAccountId() != accountIndex {
		t.Fatal("real wallet import changed the cold account identity")
	}
	assertRGBColdEmpty(t, manager)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := source.WaitAccountManagedDataReady(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.refreshDKVSRegistrations(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RecoverAccountManagementFromCurrentWallet(ctx, password); err != nil {
		t.Fatal(err)
	}
	if err := manager.BindAccountToCurrentCoreNode(); err != nil {
		t.Fatal(err)
	}
	if err := manager.SyncAccountManagementState(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.WaitAccountManagedDataReady(ctx); err != nil {
		t.Fatal(err)
	}
	status := manager.GetAccountManagementStatus()
	if status.DiscoveryStatus != "active" || !status.RecoveryConfigured ||
		status.ActivationPending || !status.DKVSWriteAllowed ||
		status.RootFingerprint != expected.RootFingerprint || manager.GetCurrentAccountId() != accountIndex {
		t.Fatal("real account recovery did not preserve identity and synchronized eligibility")
	}
	assertRGBColdEmpty(t, manager)
	manager.rgbManager.scopeStates.stopReconciliations()
	return manager
}
