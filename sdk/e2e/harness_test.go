package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/indexer/share/btclucky"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/btcutil/hdkeychain"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	sindexercommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/rpcclient"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"github.com/tyler-smith/go-bip39"
)

const (
	bootstrapMnemonic = "acquire pet news congress unveil erode paddle crumble blue fish match eye"
	coreMnemonic      = "uniform bulb body vital later special era tourist build chief devote annual"
	minerMnemonic     = "letter advice cage absurd amount doctor acoustic avoid letter advice cage above"
)

var (
	satoshinetBuildMu       sync.Mutex
	satoshinetTestArtifacts satoshinetArtifacts
	nextHarnessPort         uint32 = initialHarnessPort()
	satoshinetTestConf             = `env: test
chain: testnet
mode: local
log: info
db: ""
indexer_layer1:
  scheme: http
  host: %q
  proxy: testnet
indexer_layer2:
  scheme: http
  host: %q
  proxy: testnet
rpc:
  scheme: http
  host: %q
  proxy: testnet
management:
  listen: %q
wallet:
  mode: local
  password: rpctest
  psfile: wallet.pass
  mnemonic: %q
`
)

type satoshinetArtifacts struct {
	coreExecutable  string
	corePlugin      string
	minerExecutable string
	minerPlugin     string
}

func initialHarnessPort() uint32 {
	return uint32(20000 + (os.Getpid()%5000)*4)
}

type fakeL1Asset struct {
	Utxo     string
	Value    int64
	Assets   map[string]string
	Metadata map[string]fakeL1AssetMeta
}

type fakeL1AssetMeta struct {
	Precision  int
	BindingSat int
}

type fakeL1Indexer struct {
	server    *httptest.Server
	btcLucky  *btclucky.TemplateService
	btcHeight func() int64
	namesMu   sync.RWMutex
	names     map[string]string
}

func newFakeL1Indexer(t *testing.T, indexerPubKey string, lockedPkScript []byte, assets []fakeL1Asset) *fakeL1Indexer {
	t.Helper()

	utxos := make(map[string]*indexercommon.AssetsInUtxo, len(assets))
	tickers := make(map[string]*indexercommon.TickerInfo)
	for _, asset := range assets {
		displayAssets := make([]*indexercommon.DisplayAsset, 0, len(asset.Assets))
		names := make([]string, 0, len(asset.Assets))
		for name := range asset.Assets {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			meta := asset.Metadata[name]
			displayAssets = append(displayAssets, displayAssetWithMeta(name, asset.Assets[name], meta.Precision, meta.BindingSat))
			if _, ok := tickers[name]; !ok {
				tickers[name] = fakeTickerInfo(name, asset.Assets[name], meta.Precision, meta.BindingSat)
			}
		}
		utxos[asset.Utxo] = &indexercommon.AssetsInUtxo{
			OutPoint: asset.Utxo,
			Value:    asset.Value,
			PkScript: lockedPkScript,
			Assets:   displayAssets,
		}
	}

	fake := &fakeL1Indexer{names: make(map[string]string)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/testnet/kv/register" {
			require.NoError(t, json.NewEncoder(w).Encode(indexerwire.RegisterPubKeyResp{
				BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"},
				PubKey:   indexerPubKey,
			}))
			return
		}

		if r.URL.Path == "/testnet/bestheight" {
			height := int64(0)
			if fake.btcHeight != nil {
				height = fake.btcHeight()
			}
			require.NoError(t, json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"msg":  "ok",
				"data": map[string]int64{"height": height},
			}))
			return
		}

		if strings.HasPrefix(r.URL.Path, "/testnet/btc/block/blockhash/") {
			require.NoError(t, json.NewEncoder(w).Encode(indexerwire.BlockHashResp{
				BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"},
				Data:     strings.Repeat("0", 64),
			}))
			return
		}

		if r.URL.Path == "/testnet/btc/tx/test" {
			require.NoError(t, json.NewEncoder(w).Encode(indexerwire.TestRawTxResp{
				BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"},
			}))
			return
		}

		if strings.HasPrefix(r.URL.Path, "/testnet/ns/name/") {
			name, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/testnet/ns/name/"))
			require.NoError(t, err)
			fake.namesMu.RLock()
			address, ok := fake.names[name]
			fake.namesMu.RUnlock()
			if !ok || address == "" {
				w.WriteHeader(http.StatusNotFound)
				require.NoError(t, json.NewEncoder(w).Encode(map[string]interface{}{
					"code": -1,
					"msg":  "name not found",
				}))
				return
			}
			require.NoError(t, json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"msg":  "ok",
				"data": map[string]string{
					"name":    name,
					"address": address,
				},
			}))
			return
		}

		if r.URL.Path == "/testnet/btc/lucky/job" {
			resp := btclucky.APIResponse[*btclucky.CompactMiningJob]{Code: -1, Msg: "btc lucky template service is not enabled"}
			if fake.btcLucky == nil || !fake.btcLucky.IsReady() {
				require.NoError(t, json.NewEncoder(w).Encode(resp))
				return
			}
			var req btclucky.JobRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			job, err := fake.btcLucky.CurrentJob(req)
			if err != nil {
				resp.Msg = err.Error()
			} else {
				resp.Code = 0
				resp.Msg = "ok"
				resp.Data = job
			}
			require.NoError(t, json.NewEncoder(w).Encode(resp))
			return
		}

		if r.URL.Path == "/testnet/btc/lucky/submit" {
			resp := btclucky.APIResponse[*btclucky.FoundBlockRecord]{Code: -1, Msg: "btc lucky template service is not enabled"}
			if fake.btcLucky == nil || !fake.btcLucky.IsReady() {
				require.NoError(t, json.NewEncoder(w).Encode(resp))
				return
			}
			var req btclucky.MiningSolution
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			found, err := fake.btcLucky.SubmitSolution(&req)
			if found != nil {
				resp.Data = found
			}
			if err != nil {
				resp.Msg = err.Error()
			} else {
				resp.Code = 0
				resp.Msg = "ok"
				require.NoError(t, fake.btcLucky.RefreshTemplate())
			}
			require.NoError(t, json.NewEncoder(w).Encode(resp))
			return
		}

		if r.URL.Path == "/testnet/btc/lucky/info" {
			resp := btclucky.APIResponse[btclucky.InfoResponse]{Code: -1, Msg: "btc lucky template service is not enabled"}
			if fake.btcLucky != nil {
				resp.Code = 0
				resp.Msg = "ok"
				resp.Data = btclucky.InfoResponse{
					Service:     fake.btcLucky.Status(),
					FoundBlocks: fake.btcLucky.FoundBlocks(),
				}
			}
			require.NoError(t, json.NewEncoder(w).Encode(resp))
			return
		}

		const tickPrefix = "/testnet/v3/tick/info/"
		if strings.HasPrefix(r.URL.Path, tickPrefix) {
			name, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, tickPrefix))
			require.NoError(t, err)
			info := tickers[name]
			if info == nil {
				require.NoError(t, json.NewEncoder(w).Encode(indexerwire.TickerInfoResp{
					BaseResp: indexerwire.BaseResp{Code: 404, Msg: "missing ticker"},
				}))
				return
			}
			require.NoError(t, json.NewEncoder(w).Encode(indexerwire.TickerInfoResp{
				BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"},
				Data:     info,
			}))
			return
		}

		const prefix = "/testnet/v3/utxo/info/"
		require.True(t, strings.HasPrefix(r.URL.Path, prefix), r.URL.Path)
		utxo := strings.TrimPrefix(r.URL.Path, prefix)
		data, ok := utxos[utxo]
		if !ok {
			require.NoError(t, json.NewEncoder(w).Encode(indexerwire.TxOutputRespV3{
				BaseResp: indexerwire.BaseResp{Code: 404, Msg: "missing utxo"},
			}))
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(indexerwire.TxOutputRespV3{
			BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"},
			Data:     data,
		}))
	}))
	t.Cleanup(server.Close)
	fake.server = server
	return fake
}

func (f *fakeL1Indexer) setNameOwner(name, address string) {
	f.namesMu.Lock()
	defer f.namesMu.Unlock()
	if f.names == nil {
		f.names = make(map[string]string)
	}
	f.names[name] = address
}

func (f *fakeL1Indexer) host() string {
	return strings.TrimPrefix(f.server.URL, "http://")
}

type realSatoshiNet struct {
	Bootstrap *testHarness
	Core      *testHarness
	Miner     *testHarness
	Nodes     []*testHarness
	fakeL1    *fakeL1Indexer
}

func newRealSatoshiNet(t *testing.T, fakeL1 *fakeL1Indexer) *realSatoshiNet {
	return newRealSatoshiNetWithArgs(t, fakeL1, nil, nil, nil)
}

func newRealSatoshiNetWithArgs(t *testing.T, fakeL1 *fakeL1Indexer, bootstrapArgs, coreArgs, minerArgs []string) *realSatoshiNet {
	t.Helper()
	configureFastPOSTimers(t)

	bootstrap := startSatoshiNetNodeWithArgs(t, fakeL1, "bootstrap", bootstrapMnemonic, bootstrapArgs)
	core := startSatoshiNetNodeWithArgs(t, fakeL1, "core", coreMnemonic, coreArgs)
	miner := startSatoshiNetNodeWithArgs(t, fakeL1, "miner", minerMnemonic, minerArgs)

	require.NoError(t, connectNode(core, bootstrap))
	require.NoError(t, connectNode(miner, bootstrap))

	nodes := []*testHarness{bootstrap, core, miner}
	require.NoError(t, joinBlocks(nodes))
	return &realSatoshiNet{
		Bootstrap: bootstrap,
		Core:      core,
		Miner:     miner,
		Nodes:     nodes,
		fakeL1:    fakeL1,
	}
}

func startSatoshiNetNode(t *testing.T, fakeL1 *fakeL1Indexer, role, mnemonic string) *testHarness {
	return startSatoshiNetNodeWithArgs(t, fakeL1, role, mnemonic, nil)
}

func startSatoshiNetNodeWithArgs(t *testing.T, fakeL1 *fakeL1Indexer, role, mnemonic string, extraArgs []string) *testHarness {
	t.Helper()

	nodeKey := keyFromMnemonic(t, mnemonic, 0)
	nodePubKey := hex.EncodeToString(nodeKey.PubKey().SerializeCompressed())
	p2pAddr, rpcAddr, managementAddr := nextNodeAddresses(t)
	nodeDir := t.TempDir()
	dataDir := filepath.Join(nodeDir, "data")
	logDir := filepath.Join(nodeDir, "logs")
	args := []string{
		"--notls",
		"--nocheckpoints",
		"--nodnsseed",
		"--testnet",
		"--listen=" + p2pAddr,
		"--rpclisten=" + rpcAddr,
		"--rpcuser=user",
		"--rpcpass=pass",
		"--datadir=" + dataDir,
		"--logdir=" + logDir,
		"--indexerscheme=http",
		"--indexerhost=" + fakeL1.host(),
		"--indexerproxy=testnet",
	}
	if role != "miner" {
		args = append(args,
			"--generate",
			"--miningpubkey="+nodePubKey,
		)
	}
	args = append(args, extraArgs...)
	env := []string{
		"SATOSHINET_RPCTEST_NODE_ROLE=" + role,
		"SATOSHINET_RPCTEST_STP_MNEMONIC=" + mnemonic,
		"SATOSHINET_RPCTEST_STP_PASSWORD=rpctest",
	}
	for _, name := range []string{
		"SATOSHINET_POS_MINER_INTERVAL",
		"SATOSHINET_POS_PREWARNING_INTERVAL",
		"SATOSHINET_POS_CHECKING_INTERVAL",
	} {
		if value := os.Getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}

	harness := newTestHarness(t, stageSatoshiNetNodeRuntime(t, role, mnemonic, fakeL1.host(), l2IndexerHost(t, rpcAddr), rpcAddr, managementAddr, nodeDir), nodeDir, p2pAddr, rpcAddr, args, env)
	t.Logf("started %s node: pid=%d rpc=%s p2p=%s log=%s",
		role, harness.NodePID(), harness.RPCAddress(), harness.P2PAddress(), harness.LogFile())
	return harness
}

type testHarness struct {
	Client  *rpcclient.Client
	cmd     *exec.Cmd
	nodeDir string
	p2pAddr string
	rpcAddr string
	logFile string
}

func newTestHarness(t *testing.T, exe, nodeDir, p2pAddr, rpcAddr string, args, env []string) *testHarness {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(nodeDir, "logs"), 0o755))
	logPath := filepath.Join(nodeDir, "btcd.stdout.log")
	logFile, err := os.Create(logPath)
	require.NoError(t, err)

	cmd := exec.Command(exe, args...)
	cmd.Dir = nodeDir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	require.NoError(t, cmd.Start())

	h := &testHarness{
		cmd:     cmd,
		nodeDir: nodeDir,
		p2pAddr: p2pAddr,
		rpcAddr: rpcAddr,
		logFile: logPath,
	}
	t.Cleanup(func() {
		require.NoError(t, h.TearDown())
		require.NoError(t, logFile.Close())
		if t.Failed() {
			_ = preserveHarnessLog(t, h)
		}
	})
	h.Client = waitForRPCClient(t, rpcAddr)
	return h
}

func preserveHarnessLog(t *testing.T, h *testHarness) error {
	t.Helper()
	data, err := os.ReadFile(h.logFile)
	if err != nil {
		return err
	}
	dir := filepath.Join(os.TempDir(), "sat20wallet-satoshinet-rpctest-failures", sanitizePathPart(t.Name()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, sanitizePathPart(filepath.Base(h.nodeDir))+"-"+sanitizePathPart(h.p2pAddr)+".log")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	t.Logf("preserved node log: %s", path)
	return nil
}

func sanitizePathPart(s string) string {
	return strings.NewReplacer("/", "_", ":", "_", " ", "_").Replace(s)
}

func waitForRPCClient(t *testing.T, rpcAddr string) *rpcclient.Client {
	t.Helper()
	conf := rpcclient.ConnConfig{
		Host:                 rpcAddr,
		Endpoint:             "ws",
		User:                 "user",
		Pass:                 "pass",
		DisableTLS:           true,
		DisableAutoReconnect: true,
	}
	var lastErr error
	for i := 0; i < 200; i++ {
		client, err := rpcclient.New(&conf, &rpcclient.NotificationHandlers{})
		if err == nil {
			if _, _, err := client.GetBestBlock(); err == nil {
				return client
			}
			lastErr = err
			client.Shutdown()
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NoError(t, lastErr)
	return nil
}

func (h *testHarness) IndexerURL(proxy string) (string, error) {
	host, portText, err := net.SplitHostPort(h.rpcAddr)
	if err != nil {
		return "", err
	}
	port, err := net.LookupPort("tcp", portText)
	if err != nil {
		return "", err
	}
	if proxy == "" {
		proxy = "testnet"
	}
	return fmt.Sprintf("http://%s:%d/%s", host, port+1, proxy), nil
}

func (h *testHarness) NodePID() int {
	if h == nil || h.cmd == nil || h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

func (h *testHarness) RPCAddress() string {
	return h.rpcAddr
}

func (h *testHarness) P2PAddress() string {
	return h.p2pAddr
}

func (h *testHarness) LogFile() string {
	return h.logFile
}

func (h *testHarness) TearDown() error {
	if h.Client != nil {
		h.Client.Shutdown()
	}
	if h.cmd == nil || h.cmd.Process == nil {
		return nil
	}
	_ = h.cmd.Process.Kill()
	_, _ = h.cmd.Process.Wait()
	return nil
}

func nextNodeAddresses(t *testing.T) (string, string, string) {
	t.Helper()
	for {
		base := atomic.AddUint32(&nextHarnessPort, 4)
		p2p := fmt.Sprintf("127.0.0.1:%d", base)
		rpc := fmt.Sprintf("127.0.0.1:%d", base+1)
		indexer := fmt.Sprintf("127.0.0.1:%d", base+2)
		management := fmt.Sprintf("127.0.0.1:%d", base+3)
		if portsAvailable(p2p, rpc, indexer, management) {
			return p2p, rpc, management
		}
	}
}

func portsAvailable(addrs ...string) bool {
	listeners := make([]net.Listener, 0, len(addrs))
	for _, addr := range addrs {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			for _, prev := range listeners {
				_ = prev.Close()
			}
			return false
		}
		listeners = append(listeners, ln)
	}
	for _, ln := range listeners {
		_ = ln.Close()
	}
	return true
}

func connectNode(from, to *testHarness) error {
	peerInfo, err := from.Client.GetPeerInfo()
	if err != nil {
		return err
	}
	numPeers := len(peerInfo)
	if err := from.Client.AddNode(to.p2pAddr, rpcclient.ANAdd); err != nil {
		return err
	}
	for i := 0; i < 200; i++ {
		peerInfo, err = from.Client.GetPeerInfo()
		if err != nil {
			return err
		}
		if len(peerInfo) > numPeers {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("node %s did not connect to %s", from.p2pAddr, to.p2pAddr)
}

func joinBlocks(nodes []*testHarness) error {
	for i := 0; i < 600; i++ {
		var prevHash *chainhash.Hash
		var prevHeight int32
		matched := true
		for _, node := range nodes {
			blockHash, blockHeight, err := node.Client.GetBestBlock()
			if err != nil {
				return err
			}
			if prevHash != nil && (*blockHash != *prevHash || blockHeight != prevHeight) {
				matched = false
				break
			}
			prevHash, prevHeight = blockHash, blockHeight
		}
		if matched {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("nodes did not sync to the same best block")
}

func satoshinetBuildArtifacts(t *testing.T) satoshinetArtifacts {
	t.Helper()
	satoshinetBuildMu.Lock()
	defer satoshinetBuildMu.Unlock()
	if satoshinetTestArtifacts.coreExecutable != "" {
		return satoshinetTestArtifacts
	}

	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	workspace := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	satoshinetDir := filepath.Join(workspace, "satoshinet")
	require.FileExists(t, filepath.Join(satoshinetDir, "go.mod"))
	sdkPluginDir := filepath.Join(workspace, "sat20wallet", "sdk", "plugin")
	transcendPluginDir := filepath.Join(workspace, "transcend", "plugin")
	require.FileExists(t, filepath.Join(sdkPluginDir, "main.go"))
	require.FileExists(t, filepath.Join(transcendPluginDir, "main.go"))

	outputDir := filepath.Join(os.TempDir(), "sat20wallet-satoshinet-rpctest")
	require.NoError(t, os.MkdirAll(outputDir, 0o755))

	artifacts := satoshinetArtifacts{
		coreExecutable:  filepath.Join(outputDir, "satoshinet-core-rpctest"),
		corePlugin:      filepath.Join(outputDir, "stpd.so"),
		minerExecutable: filepath.Join(outputDir, "satoshinet-miner-rpctest"),
		minerPlugin:     filepath.Join(outputDir, "wallet.so"),
	}
	if runtime.GOOS == "windows" {
		artifacts.coreExecutable += ".exe"
		artifacts.minerExecutable += ".exe"
	}

	cmd := exec.Command("go", "build", "-buildmode=plugin", "-o", artifacts.minerPlugin, "main.go")
	cmd.Dir = sdkPluginDir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))

	cmd = exec.Command("go", "build", "-tags=rpctest,wallet_plugin", "-o", artifacts.minerExecutable, "github.com/sat20-labs/satoshinet")
	cmd.Dir = satoshinetDir
	output, err = cmd.CombinedOutput()
	require.NoError(t, err, string(output))

	cmd = exec.Command("go", "build", "-buildmode=plugin", "-o", artifacts.corePlugin, "main.go")
	cmd.Dir = transcendPluginDir
	output, err = cmd.CombinedOutput()
	require.NoError(t, err, string(output))

	cmd = exec.Command("go", "build", "-tags=rpctest,stp_plugin", "-o", artifacts.coreExecutable, "github.com/sat20-labs/satoshinet")
	cmd.Dir = satoshinetDir
	output, err = cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	satoshinetTestArtifacts = artifacts
	return satoshinetTestArtifacts
}

func stageSatoshiNetNodeRuntime(t *testing.T, role, mnemonic, l1IndexerHost, l2IndexerHost, rpcHost, managementHost, nodeDir string) string {
	t.Helper()
	artifacts := satoshinetBuildArtifacts(t)
	executable := artifacts.coreExecutable
	plugin := artifacts.corePlugin
	pluginName := "stpd.so"
	if role == "miner" {
		executable = artifacts.minerExecutable
		plugin = artifacts.minerPlugin
		pluginName = "wallet.so"
	}

	stagedExecutable := filepath.Join(nodeDir, filepath.Base(executable))
	copySatoshiNetRuntimeFile(t, executable, stagedExecutable)
	copySatoshiNetRuntimeFile(t, plugin, filepath.Join(nodeDir, pluginName))
	require.NoError(t, os.WriteFile(filepath.Join(nodeDir, "conf.yaml"), []byte(fmt.Sprintf(satoshinetTestConf, l1IndexerHost, l2IndexerHost, rpcHost, managementHost, mnemonic)), 0o600))
	return stagedExecutable
}

func l2IndexerHost(t *testing.T, rpcAddr string) string {
	t.Helper()
	host, portText, err := net.SplitHostPort(rpcAddr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	return net.JoinHostPort(host, strconv.Itoa(port+1))
}

func copySatoshiNetRuntimeFile(t *testing.T, source, destination string) {
	t.Helper()
	in, err := os.Open(source)
	require.NoError(t, err)
	defer in.Close()

	info, err := in.Stat()
	require.NoError(t, err)
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	require.NoError(t, err)
	_, err = io.Copy(out, in)
	require.NoError(t, err)
	require.NoError(t, out.Close())
}

func configureFastPOSTimers(t *testing.T) {
	t.Helper()
	if os.Getenv("SATOSHINET_POS_MINER_INTERVAL") == "" {
		t.Setenv("SATOSHINET_POS_MINER_INTERVAL", "1")
	}
	if os.Getenv("SATOSHINET_POS_PREWARNING_INTERVAL") == "" {
		t.Setenv("SATOSHINET_POS_PREWARNING_INTERVAL", "1")
	}
	if os.Getenv("SATOSHINET_POS_CHECKING_INTERVAL") == "" {
		t.Setenv("SATOSHINET_POS_CHECKING_INTERVAL", "1")
	}
}

func (n *realSatoshiNet) sendAndMine(t *testing.T, tx *wire.MsgTx, minHeight int32) {
	t.Helper()
	hash, err := n.Bootstrap.Client.SendRawTransaction(tx, true)
	require.NoError(t, err)
	require.Equal(t, tx.TxHash(), *hash)
	n.waitForTx(t, tx, minHeight)
}

func (n *realSatoshiNet) sendManyAndMine(t *testing.T, txs []*wire.MsgTx, minHeight int32) *wire.MsgBlock {
	t.Helper()
	require.NotEmpty(t, txs)
	for _, tx := range txs {
		hash, err := n.Bootstrap.Client.SendRawTransaction(tx, true)
		require.NoError(t, err)
		require.Equal(t, tx.TxHash(), *hash)
	}
	n.waitForTx(t, txs[len(txs)-1], minHeight)
	lastHash := txs[len(txs)-1].TxHash()
	verbose, err := n.Bootstrap.Client.GetRawTransactionVerbose(&lastHash)
	require.NoError(t, err)
	require.NotEmpty(t, verbose.BlockHash)
	blockHash, err := chainhash.NewHashFromStr(verbose.BlockHash)
	require.NoError(t, err)
	block, err := n.Bootstrap.Client.GetBlock(blockHash)
	require.NoError(t, err)
	return block
}

func (n *realSatoshiNet) waitForTx(t *testing.T, tx *wire.MsgTx, minHeight int32) {
	t.Helper()
	txHash := tx.TxHash()
	deadline := time.Now().Add(90 * time.Second)
	var lastStatus string
	for time.Now().Before(deadline) {
		verbose, err := n.Bootstrap.Client.GetRawTransactionVerbose(&txHash)
		if err == nil && verbose.Confirmations >= 1 {
			require.NoError(t, joinBlocks(n.Nodes))
			_, bestHeight, err := n.Bootstrap.Client.GetBestBlock()
			require.NoError(t, err)
			if bestHeight >= minHeight {
				return
			}
		}
		if err != nil {
			lastStatus = err.Error()
		} else {
			lastStatus = fmt.Sprintf("confirmations=%d block=%s", verbose.Confirmations, verbose.BlockHash)
		}
		_, _ = n.Bootstrap.Client.Generate(1)
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("transaction %s was not mined, last status: %s", txHash, lastStatus)
}

func keyFromMnemonic(t *testing.T, mnemonic string, index uint32) *btcec.PrivateKey {
	t.Helper()
	require.True(t, bip39.IsMnemonicValid(mnemonic))
	seed := bip39.NewSeed(mnemonic, "")
	key, err := hdkeychain.NewMaster(seed, &chaincfg.TestNetParams)
	require.NoError(t, err)
	for _, child := range []uint32{
		hdkeychain.HardenedKeyStart + 86,
		hdkeychain.HardenedKeyStart,
		hdkeychain.HardenedKeyStart,
		0,
		index,
	} {
		key, err = key.Derive(child)
		require.NoError(t, err)
	}
	privKey, err := key.ECPrivKey()
	require.NoError(t, err)
	return privKey
}

func callerTaprootScript(t *testing.T, internalKey *btcec.PrivateKey) ([]byte, string, []byte, []byte) {
	t.Helper()
	redeemScript := callerSpendScript(t)
	leaf := txscript.NewBaseTapLeaf(redeemScript)
	tree := txscript.AssembleTaprootScriptTree(leaf)
	rootHash := tree.RootNode.TapHash()
	outputKey := txscript.ComputeTaprootOutputKey(internalKey.PubKey(), rootHash[:])
	addr, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(outputKey), &chaincfg.TestNetParams)
	require.NoError(t, err)
	pkScript, err := txscript.PayToAddrScript(addr)
	require.NoError(t, err)
	control := tree.LeafMerkleProofs[0].ToControlBlock(internalKey.PubKey())
	controlBytes, err := control.ToBytes()
	require.NoError(t, err)
	return pkScript, addr.EncodeAddress(), redeemScript, controlBytes
}

func callerSpendScript(t *testing.T) []byte {
	t.Helper()
	script, err := txscript.NewScriptBuilder().
		AddOp(txscript.OP_DROP).
		AddOp(txscript.OP_TRUE).
		Script()
	require.NoError(t, err)
	return script
}

func signTaprootInputs(t *testing.T, tx *wire.MsgTx, signer *btcec.PrivateKey, redeemScript, controlBlock []byte) {
	t.Helper()
	for _, txIn := range tx.TxIn {
		txIn.SignatureScript = nil
		txIn.Witness = wire.TxWitness{
			signer.PubKey().SerializeCompressed(),
			redeemScript,
			controlBlock,
		}
	}
}

func signedAnchorScript(t *testing.T, utxo string, witnessScript []byte,
	value int64, assets wire.TxAssets, key *btcec.PrivateKey) []byte {

	t.Helper()
	invoice, err := standardAnchorScript(utxo, witnessScript, value, assets)
	require.NoError(t, err)
	sig := ecdsa.Sign(key, chainhash.HashB(invoice))
	assetsBuf, err := wire.SerializeTxAssets(&assets)
	require.NoError(t, err)
	script, err := txscript.NewScriptBuilder().
		AddData([]byte(utxo)).
		AddData(witnessScript).
		AddInt64(value).
		AddData(assetsBuf).
		AddData(sig.Serialize()).
		Script()
	require.NoError(t, err)
	return script
}

func getP2WSHScript(a, b []byte) ([]byte, []byte, error) {
	witnessScript, err := genMultiSigScript(a, b)
	if err != nil {
		return nil, nil, err
	}
	pkScript, err := witnessScriptHash(witnessScript)
	if err != nil {
		return nil, nil, err
	}
	return witnessScript, pkScript, nil
}

func genMultiSigScript(aPub, bPub []byte) ([]byte, error) {
	if len(aPub) != 33 || len(bPub) != 33 {
		return nil, fmt.Errorf("pubkey size error: compressed pubkeys only")
	}
	if bytes.Compare(aPub, bPub) == 1 {
		aPub, bPub = bPub, aPub
	}
	return txscript.NewScriptBuilder(txscript.WithScriptAllocSize(71)).
		AddOp(txscript.OP_2).
		AddData(aPub).
		AddData(bPub).
		AddOp(txscript.OP_2).
		AddOp(txscript.OP_CHECKMULTISIG).
		Script()
}

func witnessScriptHash(witnessScript []byte) ([]byte, error) {
	scriptHash := sha256.Sum256(witnessScript)
	return txscript.NewScriptBuilder(txscript.WithScriptAllocSize(34)).
		AddOp(txscript.OP_0).
		AddData(scriptHash[:]).
		Script()
}

func standardAnchorScript(fundingUtxo string, witnessScript []byte, value int64, assets wire.TxAssets) ([]byte, error) {
	assetsBuf, err := wire.SerializeTxAssets(&assets)
	if err != nil {
		return nil, err
	}
	return txscript.NewScriptBuilder().
		AddData([]byte(fundingUtxo)).
		AddData(witnessScript).
		AddInt64(value).
		AddData(assetsBuf).
		Script()
}

func buildAnchorTx(t *testing.T, lockedUtxo string, lockedValue int64, assets wire.TxAssets,
	ascendPayload string, witnessScript []byte, bootstrapKey *btcec.PrivateKey, spendScript []byte) *wire.MsgTx {

	t.Helper()
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{
		PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{}, Index: wire.AnchorTxOutIndex},
		SignatureScript:  signedAnchorScript(t, lockedUtxo, witnessScript, lockedValue, assets, bootstrapKey),
	})
	tx.AddTxOut(wire.NewTxOut(lockedValue, assets, spendScript))
	ascendingScript, err := sindexercommon.NullDataScript(
		sindexercommon.CONTENT_TYPE_ASCENDING,
		[]byte(ascendPayload),
	)
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, ascendingScript))
	return tx
}

func txAsset(name string, amount int64) wire.TxAssets {
	assetName := wire.NewAssetNameFromString(name)
	return wire.TxAssets{{
		Name:   *assetName,
		Amount: *indexercommon.NewDefaultDecimal(amount),
	}}
}

func displayAsset(name string, amount string) *indexercommon.DisplayAsset {
	return displayAssetWithMeta(name, amount, 0, 0)
}

func displayAssetWithMeta(name string, amount string, precision int, bindingSat int) *indexercommon.DisplayAsset {
	assetName := indexercommon.NewAssetNameFromString(name)
	return &indexercommon.DisplayAsset{
		AssetName:  *assetName,
		Amount:     amount,
		Precision:  precision,
		BindingSat: bindingSat,
	}
}

func fakeTickerInfo(name string, amount string, precision int, bindingSat int) *indexercommon.TickerInfo {
	assetName := indexercommon.NewAssetNameFromString(name)
	return &indexercommon.TickerInfo{
		AssetName:    *assetName,
		DisplayName:  name,
		Divisibility: precision,
		Limit:        amount,
		N:            bindingSat,
		TotalMinted:  amount,
		MaxSupply:    amount,
		Status:       1,
	}
}
