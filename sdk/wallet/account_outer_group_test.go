package wallet

import (
	"encoding/hex"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
)

// openAccountGroup opens the same production database and lifecycle as the
// PWA. The returned close function owns the DB and is safe to call before cleanup.
func openAccountGroup(t *testing.T, cfg *common.Config, remote *rgb11MemoryDKVSHTTP,
	service *rgb11MessageNodeClient) (*Manager, func()) {
	t.Helper()
	database := NewKVDB(cfg.DB)
	if database == nil {
		t.Fatal("open account group database failed")
	}
	manager := NewManager(cfg, database)
	if manager == nil {
		_ = database.Close()
		t.Fatal("initialize account group manager failed")
	}
	closed := false
	close := func() {
		if closed {
			return
		}
		manager.Close()
		if err := database.Close(); err != nil {
			t.Fatal("close account group database failed")
		}
		closed = true
	}
	t.Cleanup(close)
	manager.SetDKVSHttpClient(remote)
	manager.SetServerNodeHttpClient(service)
	return manager, close
}

func newAccountGroup(t *testing.T, endpoints ...*rgb11MemoryDKVSHTTP) (*Manager, *rgb11MemoryDKVSHTTP, *rgb11MessageNodeClient, func()) {
	t.Helper()
	oldEnv, oldChain, oldMode, oldIndexerChain := _env, _chain, _mode, indexer.CHAIN
	t.Cleanup(func() { _env, _chain, _mode, indexer.CHAIN = oldEnv, oldChain, oldMode, oldIndexerChain })
	_chain, indexer.CHAIN = "testnet", "testnet"
	remote := newRGB11MemoryDKVSHTTP()
	if len(endpoints) != 0 {
		remote = endpoints[0]
	}
	service := newRGB11MessageNodeClient(remote)
	cfg := &common.Config{Env: "test", Chain: "testnet", Mode: CLIENT_NODE,
		IndexerL1: &common.Indexer{Scheme: "http", Host: "127.0.0.1:1", Proxy: "testnet"},
		IndexerL2: &common.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"},
		Peers: []string{
			"b@" + indexer.GetBootstrapPubKey() + "@http://127.0.0.1:1/stp/testnet",
			"s@" + hex.EncodeToString(service.CoreNodePubKey().SerializeCompressed()) + "@http://127.0.0.1:1/stp/testnet",
		}}
	cfg.DB = t.TempDir()
	manager, close := openAccountGroup(t, cfg, remote, service)
	return manager, remote, service, close
}

// newInactiveAccountGroup creates a real single-device catalog and discovers
// authenticated absence. Later activation remains part of the calling case.
func newInactiveAccountGroup(t *testing.T, mnemonic, password string, prepare ...func(*Manager, *rgb11MemoryDKVSHTTP)) (*Manager, *rgb11MemoryDKVSHTTP, *rgb11MessageNodeClient) {
	t.Helper()
	manager, remote, service, _ := newAccountGroup(t)
	// Install case transport boundaries before import starts any discovery.
	for _, setup := range prepare {
		setup(manager, remote)
	}
	if mnemonic == "" {
		if _, _, err := manager.CreateWallet(password); err != nil {
			t.Fatalf("create account group root: %v", err)
		}
	} else {
		if _, err := manager.ImportWallet(mnemonic, password); err != nil {
			t.Fatalf("import account group root: %v", err)
		}
	}
	if err := manager.InitializeAccountManagement(password); err != nil {
		t.Fatalf("initialize account group: %v", err)
	}
	discoverInactiveAccount(t, manager)
	return manager, remote, service
}
