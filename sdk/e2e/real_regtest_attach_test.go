package e2e

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/rpcclient"
	"github.com/stretchr/testify/require"
)

// Attach uses the original SDK addresses through SSH forwards. These handles
// never own, start, stop, rebuild, or rewrite either running node.
type sdkPWARegtestAttachNode struct {
	RPC, Username, Password, Indexer, STP, Management, Pubkey string
}

type sdkPWARegtestAttachConfig struct {
	Bootstrap, Core sdkPWARegtestAttachNode
	POSV2Height     int32
}

func validateSDKPWARegtestAttach(config sdkPWARegtestAttachConfig) error {
	// The live frozen regtest binaries use this reviewed activation height.
	if config.POSV2Height != sdkPWARegtestPOSV2ActivationHeight {
		return fmt.Errorf("attach POS v2 height differs from the reviewed regtest binaries")
	}
	for _, item := range []struct {
		role string
		port int
		node sdkPWARegtestAttachNode
	}{{"bootstrap", 30250, config.Bootstrap}, {"core", 30260, config.Core}} {
		node := item.node
		if node.RPC != fmt.Sprintf("127.0.0.1:%d", item.port+1) ||
			node.Indexer != fmt.Sprintf("http://127.0.0.1:%d/regtest", item.port+2) ||
			node.STP != fmt.Sprintf("http://127.0.0.1:%d/regtest", item.port+3) ||
			node.Management != fmt.Sprintf("127.0.0.1:%d", item.port+4) {
			return fmt.Errorf("%s attach must use the existing SDK loopback forwards", item.role)
		}
		if node.Username == "" || node.Password == "" {
			return fmt.Errorf("%s attach requires the existing RPC credentials", item.role)
		}
		key, err := hex.DecodeString(node.Pubkey)
		if err != nil || len(key) != 33 || (key[0] != 2 && key[0] != 3) {
			return fmt.Errorf("%s attach requires its fixed compressed public key", item.role)
		}
	}
	return nil
}

func attachSDKPWARegtestNodes(t *testing.T, path string) *realSatoshiNet {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "attach configuration contains private RPC credentials")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var config sdkPWARegtestAttachConfig
	require.NoError(t, json.Unmarshal(data, &config))
	require.NoError(t, validateSDKPWARegtestAttach(config))
	previousRegression, previousTest := chaincfg.RegressionNetParams.POSV2Height, chaincfg.TestNetParams.POSV2Height
	chaincfg.RegressionNetParams.POSV2Height, chaincfg.TestNetParams.POSV2Height = config.POSV2Height, config.POSV2Height
	t.Setenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT", fmt.Sprint(config.POSV2Height))
	t.Cleanup(func() {
		chaincfg.RegressionNetParams.POSV2Height, chaincfg.TestNetParams.POSV2Height = previousRegression, previousTest
	})
	seeds := readSDKPWARegtestMnemonics(t)
	connect := func(role string, node sdkPWARegtestAttachNode) *testHarness {
		require.Equal(t, hex.EncodeToString(keyFromMnemonic(t, seeds[role], 0).PubKey().SerializeCompressed()), node.Pubkey,
			"attach role must match the original fixed wallet identity")
		client, err := rpcclient.New(&rpcclient.ConnConfig{Host: node.RPC,
			User: node.Username, Pass: node.Password, DisableTLS: true, HTTPPostMode: true,
			DisableAutoReconnect: true}, nil)
		require.NoError(t, err)
		t.Cleanup(client.Shutdown)
		genesis, err := client.GetBlockHash(0)
		require.NoError(t, err)
		require.Equal(t, *chaincfg.RegressionNetParams.GenesisHash, *genesis)
		mining, err := client.GetGenerate()
		require.NoError(t, err)
		require.True(t, mining, "attach must preserve the existing transaction-gated miner")
		h := &testHarness{Client: client, rpcAddr: node.RPC, role: role, nodePubKey: node.Pubkey,
			stpAddr: node.STP[len("http://") : len(node.STP)-len("/regtest")], managementAddr: node.Management}
		waitSDKPWARegtestManagerHealth(t, h)
		return h
	}
	bootstrap, core := connect("bootstrap", config.Bootstrap), connect("core", config.Core)
	bstHash, bstHeight, err := bootstrap.Client.GetBestBlock()
	require.NoError(t, err)
	coreHash, coreHeight, err := core.Client.GetBestBlock()
	require.NoError(t, err)
	require.Equal(t, bstHeight, coreHeight, "attach nodes must already share the existing tip")
	require.Equal(t, *bstHash, *coreHash)
	network := &realSatoshiNet{Bootstrap: bootstrap, Core: core, Nodes: []*testHarness{bootstrap, core}}
	waitSDKPWARegtestIndexes(t, network)
	t.Logf("attached existing regtest nodes at height=%d hash=%s; no node process is owned", bstHeight, bstHash)
	return network
}

func TestSDKPWARegtestAttachConfigRequiresExistingIsolatedEndpoints(t *testing.T) {
	config := sdkPWARegtestAttachConfig{POSV2Height: 32,
		Bootstrap: sdkPWARegtestAttachNode{RPC: "127.0.0.1:30251", Indexer: "http://127.0.0.1:30252/regtest",
			STP: "http://127.0.0.1:30253/regtest", Management: "127.0.0.1:30254", Username: "test", Password: "test",
			Pubkey: "02" + fmt.Sprintf("%064x", 1)},
		Core: sdkPWARegtestAttachNode{RPC: "127.0.0.1:30261", Indexer: "http://127.0.0.1:30262/regtest",
			STP: "http://127.0.0.1:30263/regtest", Management: "127.0.0.1:30264", Username: "test", Password: "test",
			Pubkey: "03" + fmt.Sprintf("%064x", 2)}}
	require.NoError(t, validateSDKPWARegtestAttach(config))
	for name, mutate := range map[string]func(*sdkPWARegtestAttachConfig){
		"public rpc":       func(c *sdkPWARegtestAttachConfig) { c.Core.RPC = "8.8.8.8:30261" },
		"testnet indexer":  func(c *sdkPWARegtestAttachConfig) { c.Core.Indexer = "http://127.0.0.1:30262/testnet" },
		"wrong stp":        func(c *sdkPWARegtestAttachConfig) { c.Bootstrap.STP = c.Core.STP },
		"wrong management": func(c *sdkPWARegtestAttachConfig) { c.Core.Management = c.Bootstrap.Management },
		"missing auth":     func(c *sdkPWARegtestAttachConfig) { c.Core.Password = "" },
		"missing identity": func(c *sdkPWARegtestAttachConfig) { c.Bootstrap.Pubkey = "" },
		"wrong activation": func(c *sdkPWARegtestAttachConfig) { c.POSV2Height = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := config
			mutate(&candidate)
			require.Error(t, validateSDKPWARegtestAttach(candidate))
		})
	}
}

func TestSDKPWARealRegtestAttachedNodeReadback(t *testing.T) {
	if os.Getenv("SAT20_REAL_SATOSHINET_ATTACH") == "" {
		t.Skip("requires the existing isolated regtest attach configuration")
	}
	network := prepareSDKPWARegtestNodes(t)
	for _, node := range network.Nodes {
		require.Nil(t, node.cmd, "an attached node cannot be owned by this test")
	}
}
