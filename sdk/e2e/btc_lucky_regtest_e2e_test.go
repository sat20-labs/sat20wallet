package e2e

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	btcblockchain "github.com/btcsuite/btcd/blockchain"
	btcbtcutil "github.com/btcsuite/btcd/btcutil"
	btcchaincfg "github.com/btcsuite/btcd/chaincfg"
	btcrpcclient "github.com/btcsuite/btcd/rpcclient"
	btcwire "github.com/btcsuite/btcd/wire"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/indexer/share/btclucky"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

type luckyIndexerProc struct {
	dir      string
	binary   string
	config   string
	url      string
	cmd      *exec.Cmd
	log      *os.File
	startNum int
	mu       sync.Mutex
}

type luckySubmitResult struct {
	record *btclucky.FoundBlockRecord
	err    error
	raw    []byte
}

// luckyMinerGate lets the real shared miner finish a block while the test
// controls only when that complete block is sent to the selected backend.
type luckyMinerGate struct {
	backend        btclucky.MiningBackend
	seenTemplate   chan *btclucky.BlockTemplate
	pendingBlock   chan []byte
	result         chan luckySubmitResult
	releaseTpl     chan struct{}
	releaseSubmit  chan struct{}
	finish         chan struct{}
	blockTpl       bool
	blockSubmit    bool
	tplOnce        sync.Once
	submitOnce     sync.Once
	stopAfterBlock atomic.Bool
}

func (g *luckyMinerGate) CurrentTemplate() (*btclucky.BlockTemplate, error) {
	if g.stopAfterBlock.Load() {
		<-g.finish
		return nil, fmt.Errorf("acceptance miner stopped")
	}
	template, err := g.backend.CurrentTemplate()
	if err != nil || template == nil {
		return template, err
	}
	g.tplOnce.Do(func() {
		g.seenTemplate <- template
		if g.blockTpl {
			<-g.releaseTpl
		}
	})
	return template, nil
}

func (g *luckyMinerGate) SubmitBlock(raw []byte) (*btclucky.FoundBlockRecord, error) {
	g.submitOnce.Do(func() {
		g.stopAfterBlock.Store(true)
		g.pendingBlock <- append([]byte(nil), raw...)
		if g.blockSubmit {
			<-g.releaseSubmit
		}
	})
	record, err := g.backend.SubmitBlock(raw)
	select {
	case g.result <- luckySubmitResult{record: record, err: err, raw: append([]byte(nil), raw...)}:
	case <-g.finish:
	}
	return record, err
}

func TestBTCLuckyRegtestE2E(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	testWallets := filepath.Join(filepath.Dir(source), "testdata", "regtest", "wallets.json")
	if os.Getenv("SAT20_REAL_TEST_WALLETS") == "" {
		t.Setenv("SAT20_REAL_TEST_WALLETS", testWallets)
	}
	runtimeDir := testCaseDirectory(t)
	t.Setenv("SAT20_REAL_REGTEST_RUNTIME", runtimeDir)
	t.Setenv("SAT20_REAL_SATOSHINET_ATTACH", "")

	core, coreWallet, mine := spRealBitcoind(t)
	coreWallet.client.Timeout = 2 * time.Minute
	var networkInfo struct {
		Version int `json:"version"`
	}
	require.NoError(t, core.call("getnetworkinfo", nil, &networkInfo))
	t.Logf("Bitcoin Core regtest version: %d", networkInfo.Version)
	var chainInfo struct {
		Chain string `json:"chain"`
	}
	require.NoError(t, core.call("getblockchaininfo", nil, &chainInfo))
	require.Equal(t, "regtest", chainInfo.Chain)
	mine()
	var coreHeight int
	require.NoError(t, core.call("getblockcount", nil, &coreHeight))
	require.Equal(t, 1, coreHeight, "private Bitcoin Core starts at its own regtest genesis")
	var miningAddress string
	require.NoError(t, coreWallet.call("getnewaddress", nil, &miningAddress))
	var maturedBlocks []string
	require.NoError(t, coreWallet.call("generatetoaddress", []any{101, miningAddress}, &maturedBlocks))
	require.Len(t, maturedBlocks, 101, "mine enough private blocks to fund a real witness mempool transaction")
	require.NoError(t, core.call("getblockcount", nil, &coreHeight))
	require.Equal(t, 102, coreHeight)
	seeds := readSDKPWARegtestMnemonics(t)
	require.NotEmpty(t, seeds["bootstrap"])
	require.NotEmpty(t, seeds["core"])
	require.NotEmpty(t, seeds["ordinary-1"])
	coreKey := keyFromMnemonic(t, seeds["core"], 0)
	corePub := hex.EncodeToString(coreKey.PubKey().SerializeCompressed())

	indexer := newLuckyIndexer(t, core, corePub)
	indexer.start(t)
	t.Setenv("SAT20_REAL_L1_INDEXER", indexer.url)
	waitLuckyIndexer(t, indexer, core)
	portBase := luckyNodePortBase(t)
	t.Setenv("SAT20_REAL_SATOSHINET_PORT_BASE", strconv.Itoa(portBase))

	// The wallet reads both layers through their normal HTTP clients. The
	// existing node runner starts private regtest nodes; no remote service is
	// attached and no SatoshiNet transactions are created by this case.
	network := prepareSDKPWARegtestNodes(t)
	manager, closeManager := newLuckyWallet(t, indexer.url, network, seeds)
	t.Cleanup(closeManager)

	minerWallet := manager.GetWallet()
	require.NotNil(t, minerWallet)
	servicePub := coreKey.PubKey().SerializeCompressed()
	rewardAddress, err := wallet.GetP2WSHaddress(minerWallet.GetPubKey().SerializeCompressed(), servicePub)
	require.NoError(t, err)
	_, rewardScript, err := wallet.GetP2WSHscript(minerWallet.GetPubKey().SerializeCompressed(), servicePub)
	require.NoError(t, err)
	var mempoolTxID string
	require.NoError(t, coreWallet.call("sendtoaddress", []any{rewardAddress, 0.05}, &mempoolTxID))
	var mempoolTxHex string
	require.NoError(t, core.call("getrawtransaction", []any{mempoolTxID}, &mempoolTxHex))
	mempoolTx := spRealDecodeTx(t, mempoolTxHex)
	require.NotEmpty(t, mempoolTx.TxIn)
	require.NotEmpty(t, mempoolTx.TxIn[0].Witness, "Core wallet payment must be a real witness mempool transaction")
	api := btclucky.NewHTTPTemplateBackend(indexer.url, 15*time.Second)
	minerCfg := btclucky.BTCLuckyMinerConfig{
		Enabled: true, Backend: btclucky.BTCLuckyBackendHTTPTemplate,
		RewardAddr: rewardAddress,
		MinerID:    hex.EncodeToString(minerWallet.GetPubKey().SerializeCompressed()),
		Jobs:       "1", Network: "regtest", TipCheckInterval: 100 * time.Millisecond,
	}

	t.Run("A_old_template_survives_indexer_restart", func(t *testing.T) {
		gate := newLuckyMinerGate(api, false, true)
		miner, err := btclucky.NewMiner(minerCfg, gate)
		require.NoError(t, err)
		t.Cleanup(func() {
			gate.releaseAll()
			miner.Stop()
		})
		require.NoError(t, miner.Start())
		template := waitLuckyTemplate(t, gate.seenTemplate)
		assertLuckyETag(t, indexer.url, template)
		require.Len(t, template.Transactions, 1)
		require.Equal(t, mempoolTx.TxHash().String(), template.Transactions[0].TxID)
		require.Equal(t, mempoolTx.WitnessHash().String(), template.Transactions[0].Hash)
		require.Greater(t, template.Transactions[0].Fee, int64(0))
		require.NotNil(t, template.CoinbaseValue)
		subsidy := btcblockchain.CalcBlockSubsidy(int32(template.Height), &btcchaincfg.RegressionNetParams)
		require.Equal(t, subsidy+template.Transactions[0].Fee, *template.CoinbaseValue,
			"template coinbase value must include the real witness transaction fee")
		info, err := api.Info()
		require.NoError(t, err)
		require.Equal(t, 1, info.Service.TemplateCacheSize, "first template request must populate the live indexer cache")
		raw := waitLuckyBlock(t, gate.pendingBlock)
		block, coinbase := assertLuckyBlock(t, raw, rewardScript, template)

		indexer.stop(t)
		gate.allowSubmit()
		failed := waitLuckySubmit(t, gate.result)
		require.ErrorIs(t, failed.err, btclucky.ErrCoreUnavailable, "miner HTTP submit during the outage must stay retryable")
		require.Equal(t, raw, failed.raw)
		indexer.start(t)
		waitLuckyIndexer(t, indexer, core)
		info, err = api.Info()
		require.NoError(t, err)
		require.Zero(t, info.Service.TemplateCacheSize, "a new indexer process must start with an empty template cache")

		result := waitLuckyAccepted(t, gate.result, raw)
		require.NotNil(t, result.record)
		require.True(t, result.record.Submitted)
		require.Equal(t, block.BlockHash().String(), result.record.BlockHash)
		require.Equal(t, coinbase.TxHash().String(), result.record.CoinbaseTxID)
		require.Equal(t, coinbase.TxOut[0].Value, result.record.Amount)
		assertLuckyMainChain(t, core, block, coinbase)
		waitLuckyIndexer(t, indexer, core)
		t.Logf("Lucky A accepted: height=%d block=%s coinbase=%s amount=%d", template.Height,
			block.BlockHash(), coinbase.TxHash(), coinbase.TxOut[0].Value)
		gate.finishAll()
		miner.Stop()
	})

	t.Run("B_core_submit_survives_indexer_outage_and_wallet_poll", func(t *testing.T) {
		direct := newLuckyCoreBackend(t, api, core)
		gate := newLuckyMinerGate(direct, true, false)
		miner, err := btclucky.NewMiner(minerCfg, gate)
		require.NoError(t, err)
		t.Cleanup(func() {
			gate.releaseAll()
			miner.Stop()
		})
		require.NoError(t, miner.Start())
		template := waitLuckyTemplate(t, gate.seenTemplate)
		indexer.stop(t)
		gate.allowTemplate()
		raw := waitLuckyBlock(t, gate.pendingBlock)
		block, coinbase := assertLuckyBlock(t, raw, rewardScript, template)
		result := waitLuckySubmit(t, gate.result)
		require.NoError(t, result.err)
		require.NotNil(t, result.record)
		require.True(t, result.record.Submitted)
		require.Equal(t, block.BlockHash().String(), result.record.BlockHash)
		assertLuckyMainChain(t, core, block, coinbase)

		indexer.start(t)
		waitLuckyIndexer(t, indexer, core)
		info, err := api.Info()
		require.NoError(t, err)
		for _, found := range info.FoundBlocks {
			require.NotEqual(t, block.BlockHash().String(), found.BlockHash,
				"direct Core submission must not create an indexer found-block record")
		}
		gate.finishAll()
		miner.Stop()

		var rewards []*wallet.BTCLuckyReward
		require.Eventually(t, func() bool {
			var rewardErr error
			rewards, rewardErr = manager.GetBTCLuckyRewards()
			return rewardErr == nil && hasLuckyReward(rewards, coinbase.TxHash().String()+":0")
		}, 45*time.Second, 500*time.Millisecond, "the actual SDK wallet must discover Core's coinbase by polling after indexer recovery")
		var reward *wallet.BTCLuckyReward
		for _, item := range rewards {
			if item.FundingUtxo == coinbase.TxHash().String()+":0" {
				reward = item
				break
			}
		}
		require.NotNil(t, reward)
		require.Equal(t, coinbase.TxOut[0].Value, reward.Amount)
		require.EqualValues(t, 1, reward.Confirmations)
		require.EqualValues(t, 100, reward.Maturity)
		require.Equal(t, "immature", reward.Status)
		require.Equal(t, block.BlockHash().String(), reward.BTCBlockHash)
		t.Logf("Lucky B wallet discovered: height=%d block=%s coinbase=%s amount=%d confirmations=%d status=%s indexer_records=%d",
			template.Height, reward.BTCBlockHash, coinbase.TxHash(), reward.Amount, reward.Confirmations, reward.Status, len(info.FoundBlocks))
	})
}

func newLuckyMinerGate(backend btclucky.MiningBackend, blockTpl, blockSubmit bool) *luckyMinerGate {
	return &luckyMinerGate{
		backend: backend, seenTemplate: make(chan *btclucky.BlockTemplate, 1),
		pendingBlock: make(chan []byte, 1), result: make(chan luckySubmitResult, 4),
		releaseTpl: make(chan struct{}), releaseSubmit: make(chan struct{}), finish: make(chan struct{}),
		blockTpl: blockTpl, blockSubmit: blockSubmit,
	}
}

func (g *luckyMinerGate) allowTemplate() { close(g.releaseTpl) }
func (g *luckyMinerGate) allowSubmit()   { close(g.releaseSubmit) }
func (g *luckyMinerGate) finishAll()     { close(g.finish) }
func (g *luckyMinerGate) releaseAll() {
	select {
	case <-g.releaseTpl:
	default:
		g.allowTemplate()
	}
	select {
	case <-g.releaseSubmit:
	default:
		g.allowSubmit()
	}
	select {
	case <-g.finish:
	default:
		g.finishAll()
	}
}

func newLuckyIndexer(t *testing.T, core *spRealCoreRPC, pubkey string) *luckyIndexerProc {
	t.Helper()
	dir := testCaseDirectory(t)
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	workspace := filepath.Clean(filepath.Join(filepath.Dir(source), "../../.."))
	indexerDir := filepath.Join(workspace, "indexer")
	binary := filepath.Join(dir, "indexer")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	build.Dir = indexerDir
	output, err := build.CombinedOutput()
	require.NoError(t, err, "build isolated real indexer: %s", strings.TrimSpace(string(output)))

	rpcPort, err := strconv.Atoi(strings.TrimPrefix(core.base, "http://127.0.0.1:"))
	require.NoError(t, err)
	httpPort, err := strconv.Atoi(spRealFreePort(t))
	require.NoError(t, err)
	dbPath := filepath.Join(dir, "db")
	config := filepath.Join(dir, ".env")
	configBody := fmt.Sprintf(`chain: regtest
db:
  path: %q
  badger_block_cache_total_mb: 64
  badger_index_cache_total_mb: 16
  badger_num_compactors: 1
  badger_flatten_workers: 1
share_rpc:
  bitcoin:
    host: 127.0.0.1
    port: %d
    user: %q
    password: %q
    coinbase_maturity: 100
log:
  level: warn
  path: %q
basic_index:
  max_index_height: 0
  period_flush_to_db: 1
rpc_service:
  addr: 127.0.0.1:%d
  proxy: btc/regtest
  log_path: %q
pubkey: %q
`, dbPath, rpcPort, core.user, core.pass, filepath.Join(dir, "log"), httpPort, filepath.Join(dir, "rpc-log"), pubkey)
	require.NoError(t, os.WriteFile(config, []byte(configBody), 0o600))
	proc := &luckyIndexerProc{
		dir: dir, binary: binary, config: config,
		url: fmt.Sprintf("http://127.0.0.1:%d/btc/regtest", httpPort),
	}
	t.Cleanup(func() { proc.stop(t) })
	return proc
}

func (p *luckyIndexerProc) start(t *testing.T) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	require.Nil(t, p.cmd, "indexer process already running")
	p.startNum++
	logPath := filepath.Join(p.dir, fmt.Sprintf("indexer-%d.log", p.startNum))
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	cmd := exec.Command(p.binary, "-env", p.config)
	cmd.Dir = p.dir
	cmd.Stdout, cmd.Stderr = log, log
	require.NoError(t, cmd.Start())
	p.cmd, p.log = cmd, log
	require.Eventually(t, func() bool {
		client := &http.Client{Timeout: time.Second}
		resp, err := client.Get(p.url + "/health")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		var health struct {
			Status string `json:"status"`
		}
		return resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&health) == nil && health.Status == "ok"
	}, 90*time.Second, 250*time.Millisecond, "isolated L1 indexer did not become healthy; log=%s", logPath)
}

func (p *luckyIndexerProc) stop(t *testing.T) {
	t.Helper()
	p.mu.Lock()
	cmd, log := p.cmd, p.log
	if cmd == nil {
		p.mu.Unlock()
		return
	}
	p.cmd, p.log = nil, nil
	p.mu.Unlock()
	_ = cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		require.NoError(t, err, "stop isolated indexer process")
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatal("isolated indexer did not stop on interrupt")
	}
	if log != nil {
		require.NoError(t, log.Close())
	}
}

func waitLuckyIndexer(t *testing.T, proc *luckyIndexerProc, core *spRealCoreRPC) {
	t.Helper()
	var want int
	require.NoError(t, core.call("getblockcount", nil, &want))
	client := &http.Client{Timeout: 2 * time.Second}
	require.Eventually(t, func() bool {
		resp, err := client.Get(proc.url + "/bestheight")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		var height struct {
			Code int `json:"code"`
			Data struct {
				Height int `json:"height"`
			} `json:"data"`
		}
		return resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&height) == nil && height.Code == 0 && height.Data.Height >= want
	}, 60*time.Second, 250*time.Millisecond, "indexer did not index Core height %d", want)
}

func newLuckyWallet(t *testing.T, endpoint string, network *realSatoshiNet, seeds map[string]string) (*wallet.Manager, func()) {
	t.Helper()
	endpointURL, err := url.Parse(endpoint)
	require.NoError(t, err)
	bootstrapKey := keyFromMnemonic(t, seeds["bootstrap"], 0)
	peers := []string{
		fmt.Sprintf("b@%s@http://127.0.0.1:%d/regtest", hex.EncodeToString(bootstrapKey.PubKey().SerializeCompressed()), sdkPWARegtestSatoshiNetPortBase(t)+3),
		fmt.Sprintf("s@%s@http://127.0.0.1:%d/regtest", network.Core.nodePubKey, sdkPWARegtestSatoshiNetPortBase(t)+13),
	}
	coreURL, err := network.Core.IndexerURL("regtest")
	require.NoError(t, err)
	coreURLParsed, err := url.Parse(coreURL)
	require.NoError(t, err)
	cfg := &sdkcommon.Config{
		Env: "dev", Chain: "regtest", Mode: "client", Peers: peers,
		IndexerL1: &sdkcommon.Indexer{Scheme: endpointURL.Scheme, Host: endpointURL.Host, Proxy: "btc/regtest"},
		IndexerL2: &sdkcommon.Indexer{Scheme: coreURLParsed.Scheme, Host: coreURLParsed.Host, Proxy: "regtest"},
	}
	dbPath := t.TempDir()
	database := indexerdb.NewKVDB(dbPath)
	require.NotNil(t, database)
	manager := wallet.NewManager(cfg, database)
	require.NotNil(t, manager)
	_, err = manager.ImportWallet(seeds["ordinary-1"], "regtest-acceptance-only")
	require.NoError(t, err, "import the existing ordinary-1 regtest wallet into a private SDK DB")
	return manager, func() {
		manager.Close()
		require.NoError(t, database.Close())
	}
}

func luckyNodePortBase(t *testing.T) int {
	t.Helper()
	const minPort, maxPort = 20000, 28000
	span := maxPort - minPort - 14
	start := int(time.Now().UnixNano() % int64(span))
	for attempt := 0; attempt < 128; attempt++ {
		base := minPort + (start+attempt*37)%span
		listeners := make([]net.Listener, 0, 15)
		available := true
		for port := base; port < base+15; port++ {
			listener, listenErr := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", port))
			if listenErr != nil {
				available = false
				break
			}
			listeners = append(listeners, listener)
		}
		for _, listener := range listeners {
			_ = listener.Close()
		}
		if available {
			return base
		}
	}
	t.Fatal("could not reserve an unused private SatoshiNet regtest port block")
	return 0
}

func waitLuckyBlock(t *testing.T, blocks <-chan []byte) []byte {
	t.Helper()
	select {
	case raw := <-blocks:
		require.NotEmpty(t, raw)
		return raw
	case <-time.After(45 * time.Second):
		t.Fatal("shared miner did not produce a regtest block")
		return nil
	}
}

func waitLuckyTemplate(t *testing.T, templates <-chan *btclucky.BlockTemplate) *btclucky.BlockTemplate {
	t.Helper()
	select {
	case template := <-templates:
		require.NotNil(t, template)
		return template
	case <-time.After(45 * time.Second):
		t.Fatal("shared miner did not receive a Bitcoin template")
		return nil
	}
}

func assertLuckyETag(t *testing.T, base string, template *btclucky.BlockTemplate) {
	t.Helper()
	endpoint := strings.TrimRight(base, "/") + "/btc/lucky/template"
	client := &http.Client{Timeout: 5 * time.Second}
	get := func(tag string) (*http.Response, []byte) {
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		require.NoError(t, err)
		if tag != "" {
			req.Header.Set("If-None-Match", tag)
		}
		resp, err := client.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp, body
	}
	first, firstBody := get("")
	require.Equal(t, http.StatusOK, first.StatusCode)
	firstTag := first.Header.Get("ETag")
	require.NotEmpty(t, firstTag)
	second, secondBody := get("")
	require.Equal(t, http.StatusOK, second.StatusCode)
	require.Equal(t, firstTag, second.Header.Get("ETag"))
	require.Equal(t, firstBody, secondBody, "independent template GETs must return the same response bytes")
	var response btclucky.APIResponse[*btclucky.BlockTemplate]
	require.NoError(t, json.Unmarshal(firstBody, &response))
	require.Zero(t, response.Code)
	require.NotNil(t, response.Data)
	require.Equal(t, template.Height, response.Data.Height)
	require.Equal(t, template.PreviousHash, response.Data.PreviousHash)
	unchanged, unchangedBody := get(firstTag)
	require.Equal(t, http.StatusNotModified, unchanged.StatusCode)
	require.Empty(t, unchangedBody, "304 template response must have an empty body")
}

func waitLuckySubmit(t *testing.T, results <-chan luckySubmitResult) luckySubmitResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(45 * time.Second):
		t.Fatal("shared miner did not finish Core block submission")
		return luckySubmitResult{}
	}
}

func waitLuckyAccepted(t *testing.T, results <-chan luckySubmitResult, raw []byte) luckySubmitResult {
	t.Helper()
	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	for {
		select {
		case result := <-results:
			require.Equal(t, raw, result.raw, "miner retry must submit the same complete block bytes")
			if result.err != nil {
				require.ErrorIs(t, result.err, btclucky.ErrCoreUnavailable,
					"only retryable Core or Indexer outages are expected before acceptance")
				continue
			}
			return result
		case <-timer.C:
			t.Fatal("miner did not successfully retry the cached block after indexer recovery")
			return luckySubmitResult{}
		}
	}
}

func assertLuckyBlock(t *testing.T, raw, script []byte, template *btclucky.BlockTemplate) (*btcwire.MsgBlock, *btcwire.MsgTx) {
	t.Helper()
	reader := bytes.NewReader(raw)
	block := btcwire.NewMsgBlock(&btcwire.BlockHeader{})
	require.NoError(t, block.Deserialize(reader))
	require.Zero(t, reader.Len(), "submitted bytes must contain exactly one complete serialized Bitcoin block")
	require.NotEmpty(t, block.Transactions)
	coinbase := block.Transactions[0]
	require.True(t, btcblockchain.IsCoinBaseTx(coinbase))
	require.Contains(t, string(coinbase.TxIn[0].SignatureScript), "satoshinet")
	coinbaseHeight, err := btcblockchain.ExtractCoinbaseHeight(btcbtcutil.NewTx(coinbase))
	require.NoError(t, err)
	require.Equal(t, int32(template.Height), coinbaseHeight)
	require.Equal(t, template.PreviousHash, block.Header.PrevBlock.String())
	require.NotNil(t, template.CoinbaseValue)
	require.NotEmpty(t, coinbase.TxOut)
	require.Equal(t, *template.CoinbaseValue, coinbase.TxOut[0].Value, "coinbase must include the template subsidy and transaction fees")
	require.Equal(t, script, coinbase.TxOut[0].PkScript, "coinbase output must pay the SDK wallet's reward script")
	if template.DefaultWitnessCommitment != "" {
		commitment, err := hex.DecodeString(template.DefaultWitnessCommitment)
		require.NoError(t, err)
		found := false
		for _, output := range coinbase.TxOut {
			if bytes.Equal(output.PkScript, commitment) {
				found = true
				break
			}
		}
		require.True(t, found, "coinbase must include Core's witness commitment")
	}
	require.Len(t, block.Transactions, len(template.Transactions)+1, "the complete block must preserve every Core template transaction")
	for i, item := range template.Transactions {
		expectedRaw, err := hex.DecodeString(item.Data)
		require.NoError(t, err)
		expected := btcwire.NewMsgTx(btcwire.TxVersion)
		require.NoError(t, expected.Deserialize(bytes.NewReader(expectedRaw)))
		actual := block.Transactions[i+1]
		require.Equal(t, item.TxID, expected.TxHash().String())
		require.Equal(t, item.Hash, expected.WitnessHash().String())
		require.Equal(t, item.TxID, actual.TxHash().String())
		require.Equal(t, item.Hash, actual.WitnessHash().String())
		var actualRaw bytes.Buffer
		require.NoError(t, actual.Serialize(&actualRaw))
		require.Equal(t, expectedRaw, actualRaw.Bytes(), "block transaction must preserve Core's exact witness serialization")
	}
	target := btcblockchain.CompactToBig(block.Header.Bits)
	hash := block.Header.BlockHash()
	require.LessOrEqual(t, btcblockchain.HashToBig(&hash).Cmp(target), 0, "shared miner must supply valid regtest proof of work")
	return block, coinbase
}

func assertLuckyMainChain(t *testing.T, core *spRealCoreRPC, block *btcwire.MsgBlock, coinbase *btcwire.MsgTx) {
	t.Helper()
	var height int
	require.NoError(t, core.call("getblockcount", nil, &height))
	blockHash := block.BlockHash().String()
	var activeHash string
	require.NoError(t, core.call("getblockhash", []any{height}, &activeHash))
	require.Equal(t, blockHash, activeHash, "submitted block must be at the Core active-chain tip")
	var active struct {
		Height int      `json:"height"`
		Tx     []string `json:"tx"`
	}
	require.NoError(t, core.call("getblock", []any{activeHash, 1}, &active))
	require.Equal(t, height, active.Height)
	require.NotEmpty(t, active.Tx)
	require.Equal(t, coinbase.TxHash().String(), active.Tx[0])
}

func hasLuckyReward(rewards []*wallet.BTCLuckyReward, point string) bool {
	for _, reward := range rewards {
		if reward != nil && reward.FundingUtxo == point {
			return true
		}
	}
	return false
}

func newLuckyCoreBackend(t *testing.T, httpBackend *btclucky.HTTPTemplateBackend, core *spRealCoreRPC) *btclucky.CoreSubmitBackend {
	t.Helper()
	host := strings.TrimPrefix(core.base, "http://")
	client, err := btcrpcclient.New(&btcrpcclient.ConnConfig{
		Host: host, User: core.user, Pass: core.pass, HTTPPostMode: true, DisableTLS: true,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(client.Shutdown)
	backend, err := btclucky.NewCoreSubmitBackend(httpBackend, client, "regtest")
	require.NoError(t, err)
	return backend
}
