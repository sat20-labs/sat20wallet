package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/indexer/share/btclucky"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	satschaincfg "github.com/sat20-labs/satoshinet/chaincfg"
	sindexercommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"github.com/tyler-smith/go-bip39"
)

type luckyMgrFixture struct {
	core       *spRealCoreRPC
	coreWallet *spRealCoreRPC
	network    *realSatoshiNet
	seeds      map[string]string
	corePub    string
	backupPub  string
	primary    *luckyIndexerProc
	backup     *luckyIndexerProc
	l1Probe    *luckyMgrProbe
	l2Probe    *luckyMgrProbe
}

type luckyMgrGate struct {
	mu          sync.Mutex
	want        int
	count       int
	ready       chan struct{}
	release     chan struct{}
	readyOnce   sync.Once
	releaseOnce sync.Once
}

type luckyMgrProbe struct {
	server *httptest.Server
	proxy  *httputil.ReverseProxy
	target *url.URL

	mu          sync.Mutex
	counts      map[string]int
	failHeight  bool
	gate        *luckyMgrGate
	hardBits    bool
	blockHashFn func(int) string
}

const luckyPOSV2Height int32 = 1

func TestBTCLuckyManagerRegtestE2E(t *testing.T) {
	configureLuckyPOSV2(t)
	f := newLuckyMgrFixture(t)
	t.Run("provider_identity_and_signer", func(t *testing.T) {
		testLuckyProvider(t, f)
	})
	t.Run("concurrent_lifecycle_and_switch", func(t *testing.T) {
		testLuckyLifecycle(t, f)
	})
	t.Run("idle_monitor_and_mature_discovery", func(t *testing.T) {
		testLuckyMonitor(t, f)
	})
	t.Run("source_block_hash_matches_active_chain", func(t *testing.T) {
		testLuckyBlockHash(t, f)
	})
}

func configureLuckyPOSV2(t *testing.T) {
	t.Helper()
	oldRegtest := satschaincfg.RegressionNetParams.POSV2Height
	oldTestnet := satschaincfg.TestNetParams.POSV2Height
	satschaincfg.RegressionNetParams.POSV2Height = luckyPOSV2Height
	satschaincfg.TestNetParams.POSV2Height = luckyPOSV2Height
	t.Setenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT", strconv.FormatInt(int64(luckyPOSV2Height), 10))
	t.Cleanup(func() {
		satschaincfg.RegressionNetParams.POSV2Height = oldRegtest
		satschaincfg.TestNetParams.POSV2Height = oldTestnet
	})
}

func newLuckyMgrFixture(t *testing.T) *luckyMgrFixture {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	if os.Getenv("SAT20_REAL_TEST_WALLETS") == "" {
		t.Setenv("SAT20_REAL_TEST_WALLETS", filepath.Join(filepath.Dir(source), "testdata", "regtest", "wallets.json"))
	}
	t.Setenv("SAT20_REAL_REGTEST_RUNTIME", testCaseDirectory(t))
	t.Setenv("SAT20_REAL_SATOSHINET_ATTACH", "")

	core, coreWallet, mine := spRealBitcoind(t)
	coreWallet.client.Timeout = 2 * time.Minute
	mine()
	var address string
	require.NoError(t, coreWallet.call("getnewaddress", nil, &address))
	var blocks []string
	require.NoError(t, coreWallet.call("generatetoaddress", []any{101, address}, &blocks))
	require.Len(t, blocks, 101)

	seeds := readSDKPWARegtestMnemonics(t)
	coreKey := keyFromMnemonic(t, seeds["core"], 0)
	backupKey := keyFromMnemonic(t, seeds["core"], 1)
	corePub := hex.EncodeToString(coreKey.PubKey().SerializeCompressed())
	backupPub := hex.EncodeToString(backupKey.PubKey().SerializeCompressed())
	primary := newLuckyIndexer(t, core, corePub)
	setLuckyMaturity(t, primary, 10)
	primary.start(t)
	backup := newLuckyIndexer(t, core, backupPub)
	setLuckyMaturity(t, backup, 10)
	backup.start(t)
	t.Setenv("SAT20_REAL_L1_INDEXER", primary.url)
	waitLuckyIndexer(t, primary, core)
	waitLuckyIndexer(t, backup, core)

	t.Setenv("SAT20_REAL_SATOSHINET_PORT_BASE", strconv.Itoa(luckyNodePortBase(t)))
	network := prepareSDKPWARegtestNodes(t)
	require.Equal(t, luckyPOSV2Height, wallet.GetChainParam_SatsNet().POSV2Height,
		"SDK and rpctest nodes must use the same private POS v2 activation height")
	_, l2Height, err := network.Bootstrap.Client.GetBestBlock()
	require.NoError(t, err)
	require.Zero(t, l2Height, "the first valid output-bound lucky anchor must be included in the first scheduled L2 block")
	l1Probe := newLuckyMgrProbe(t, primary.url)
	l2URL, err := network.Core.IndexerURL("regtest")
	require.NoError(t, err)
	l2Probe := newLuckyMgrProbe(t, l2URL)
	return &luckyMgrFixture{
		core: core, coreWallet: coreWallet, network: network, seeds: seeds,
		corePub: corePub, backupPub: backupPub, primary: primary, backup: backup,
		l1Probe: l1Probe, l2Probe: l2Probe,
	}
}

func setLuckyMaturity(t *testing.T, proc *luckyIndexerProc, maturity int) {
	t.Helper()
	data, err := os.ReadFile(proc.config)
	require.NoError(t, err)
	updated := strings.Replace(string(data), "coinbase_maturity: 100", fmt.Sprintf("coinbase_maturity: %d", maturity), 1)
	require.NotEqual(t, string(data), updated, "private indexer fixture must expose the requested maturity")
	require.NoError(t, os.WriteFile(proc.config, []byte(updated), 0600))
}

func newLuckyMgrProbe(t *testing.T, endpoint string) *luckyMgrProbe {
	t.Helper()
	upstream, err := url.Parse(endpoint)
	require.NoError(t, err)
	upstream.Path = ""
	upstream.RawPath = ""
	upstream.RawQuery = ""
	probe := &luckyMgrProbe{counts: make(map[string]int), target: upstream,
		proxy: httputil.NewSingleHostReverseProxy(upstream)}
	probe.server = httptest.NewServer(http.HandlerFunc(probe.serveHTTP))
	t.Cleanup(probe.server.Close)
	return probe
}

func (p *luckyMgrProbe) serveHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	p.mu.Lock()
	p.counts[path]++
	failHeight := p.failHeight && strings.HasSuffix(path, "/bestheight")
	gate := p.gate
	if gate != nil && strings.HasSuffix(path, "/v3/indexer/pubkey") {
		gate.mu.Lock()
		gate.count++
		count := gate.count
		gate.mu.Unlock()
		if count >= gate.want {
			gate.readyOnce.Do(func() { close(gate.ready) })
		}
		p.mu.Unlock()
		select {
		case <-gate.release:
		case <-r.Context().Done():
			return
		}
		p.mu.Lock()
	}
	hardBits := p.hardBits && strings.HasSuffix(path, "/btc/lucky/template")
	blockHashFn := p.blockHashFn
	p.mu.Unlock()
	if failHeight {
		http.Error(w, "connection refused", http.StatusServiceUnavailable)
		return
	}
	if blockHashFn != nil && strings.Contains(path, "/btc/block/blockhash/") {
		height, err := strconv.Atoi(path[strings.LastIndex(path, "/")+1:])
		if err == nil {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(indexerwire.BlockHashResp{
				BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}, Data: blockHashFn(height),
			})
			return
		}
	}
	if !hardBits {
		p.proxy.ServeHTTP(w, r)
		return
	}
	remoteURL := *p.target
	remoteURL.Path = r.URL.Path
	remoteURL.RawPath = r.URL.RawPath
	remoteURL.RawQuery = r.URL.RawQuery
	request, err := http.NewRequestWithContext(r.Context(), r.Method, remoteURL.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	request.Header = r.Header.Clone()
	request.Header.Set("Accept-Encoding", "identity")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if response.StatusCode != http.StatusOK {
		copyLuckyHeaders(w.Header(), response.Header)
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(body)
		return
	}
	var payload btclucky.APIResponse[*btclucky.BlockTemplate]
	if err := json.Unmarshal(body, &payload); err != nil || payload.Data == nil {
		http.Error(w, "invalid template response", http.StatusBadGateway)
		return
	}
	payload.Data.Bits = "1d00ffff"
	payload.Data.Target = "00000000ffff0000000000000000000000000000000000000000000000000000"
	body, err = json.Marshal(payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	copyLuckyHeaders(w.Header(), response.Header)
	hash := sha256.Sum256(body)
	w.Header().Set("ETag", fmt.Sprintf("\"%x\"", hash))
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func copyLuckyHeaders(dst, src http.Header) {
	for name, values := range src {
		for _, value := range values {
			dst.Add(name, value)
		}
	}
}

func (p *luckyMgrProbe) countSuffix(suffix string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	count := 0
	for path, value := range p.counts {
		if strings.HasSuffix(path, suffix) {
			count += value
		}
	}
	return count
}

func (p *luckyMgrProbe) countContains(fragment string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	count := 0
	for path, value := range p.counts {
		if strings.Contains(path, fragment) {
			count += value
		}
	}
	return count
}

func (p *luckyMgrProbe) setHardBits(value bool) {
	p.mu.Lock()
	p.hardBits = value
	p.mu.Unlock()
}

func (p *luckyMgrProbe) setFailHeight(value bool) {
	p.mu.Lock()
	p.failHeight = value
	p.mu.Unlock()
}

func (p *luckyMgrProbe) setGate(want int) *luckyMgrGate {
	gate := &luckyMgrGate{want: want, ready: make(chan struct{}), release: make(chan struct{})}
	p.mu.Lock()
	p.gate = gate
	p.mu.Unlock()
	return gate
}

func (p *luckyMgrProbe) clearGate(gate *luckyMgrGate) {
	gate.releaseAll()
	p.mu.Lock()
	if p.gate == gate {
		p.gate = nil
	}
	p.mu.Unlock()
}

func (g *luckyMgrGate) releaseAll() {
	g.releaseOnce.Do(func() { close(g.release) })
}

func (g *luckyMgrGate) countRequests() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.count
}

func newLuckyManager(t *testing.T, f *luckyMgrFixture, includeCore bool, userMnemonic ...string) (*wallet.Manager, func()) {
	t.Helper()
	primary, err := url.Parse(f.l1Probe.server.URL)
	require.NoError(t, err)
	backup, err := url.Parse(f.backup.url)
	require.NoError(t, err)
	l2, err := url.Parse(f.l2Probe.server.URL)
	require.NoError(t, err)
	bootstrapKey := keyFromMnemonic(t, f.seeds["bootstrap"], 0)
	portBase := sdkPWARegtestSatoshiNetPortBase(t)
	peers := []string{fmt.Sprintf("b@%s@http://127.0.0.1:%d/regtest",
		hex.EncodeToString(bootstrapKey.PubKey().SerializeCompressed()), portBase+3)}
	if includeCore {
		peers = append(peers, fmt.Sprintf("s@%s@http://127.0.0.1:%d/regtest", f.network.Core.nodePubKey, portBase+13))
	}
	config := &sdkcommon.Config{
		Env: "dev", Chain: "regtest", Mode: wallet.LIGHT_NODE, Peers: peers,
		IndexerL1:      &sdkcommon.Indexer{Scheme: primary.Scheme, Host: primary.Host, Proxy: "btc/regtest"},
		SlaveIndexerL1: &sdkcommon.Indexer{Scheme: backup.Scheme, Host: backup.Host, Proxy: "btc/regtest"},
		IndexerL2:      &sdkcommon.Indexer{Scheme: l2.Scheme, Host: l2.Host, Proxy: "regtest"},
	}
	database := indexerdb.NewKVDB(t.TempDir())
	require.NotNil(t, database)
	manager := wallet.NewManager(config, database)
	require.NotNil(t, manager)
	mnemonic := f.seeds["ordinary-1"]
	if len(userMnemonic) > 0 {
		mnemonic = userMnemonic[0]
	}
	_, err = manager.ImportWallet(mnemonic, "regtest-acceptance-only")
	require.NoError(t, err)
	return manager, func() {
		manager.Close()
		require.NoError(t, database.Close())
	}
}

func testLuckyProvider(t *testing.T, f *luckyMgrFixture) {
	t.Helper()
	f.l1Probe.setHardBits(true)
	manager, closeManager := newLuckyManager(t, f, true)
	defer closeManager()
	f.l1Probe.setFailHeight(true)
	require.NoError(t, manager.GetIndexerRPCClient().Ping())
	f.l1Probe.setFailHeight(false)
	backupURL, err := url.Parse(f.backup.url)
	require.NoError(t, err)
	require.Equal(t, backupURL.Host, manager.GetIndexerRPCClient().Host(),
		"the ordinary indexer client must be using the selected backup")

	status, err := manager.StartBTCLuckyMining(wallet.BTCLuckyMiningConfig{Jobs: "1", LowPriority: true, LowPrioritySleep: "1h"})
	require.NoError(t, err)
	userPub := manager.GetWallet().GetPubKey().SerializeCompressed()
	coreKey := keyFromMnemonic(t, f.seeds["core"], 0)
	expected, err := wallet.GetP2WSHaddress(userPub, coreKey.PubKey().SerializeCompressed())
	require.NoError(t, err)
	require.Equal(t, expected, status.RewardAddress, "the main provider must remain the reward signer during backup selection")
	require.Eventually(t, func() bool { return f.l1Probe.countSuffix("/btc/lucky/template") > 0 }, 10*time.Second, 50*time.Millisecond)
	backupAPI := btclucky.NewHTTPTemplateBackend(f.backup.url, time.Second)
	info, err := backupAPI.Info()
	require.NoError(t, err)
	require.Zero(t, info.Service.TemplateCacheSize, "templates must stay pinned to the main provider")
	manager.StopBTCLuckyMining()

	bad, closeBad := newLuckyManager(t, f, false)
	defer closeBad()
	_, err = bad.StartBTCLuckyMining(wallet.BTCLuckyMiningConfig{Jobs: "1"})
	require.Error(t, err, "a provider without a configured STP signing peer must be rejected")
}

func testLuckyLifecycle(t *testing.T, f *luckyMgrFixture) {
	t.Helper()
	f.l1Probe.setHardBits(true)
	manager, closeManager := newLuckyManager(t, f, true)
	defer closeManager()
	firstID := prepareLuckySubaccount(t, manager, f.network, f.seeds["ordinary-1"])
	rootPub := manager.GetWallet().GetPubKey().SerializeCompressed()
	coreKey := keyFromMnemonic(t, f.seeds["core"], 0)
	firstExpected, err := wallet.GetP2WSHaddress(rootPub, coreKey.PubKey().SerializeCompressed())
	require.NoError(t, err)

	templateBeforeConcurrentStart := f.l1Probe.countSuffix("/btc/lucky/template")
	gate := f.l1Probe.setGate(1)
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := manager.StartBTCLuckyMining(wallet.BTCLuckyMiningConfig{Jobs: "1", LowPriority: true, LowPrioritySleep: "1h"})
			results <- err
		}()
	}
	select {
	case <-gate.ready:
	case <-time.After(2 * time.Second):
		f.l1Probe.clearGate(gate)
		t.Fatal("the first concurrent start never reached the provider identity barrier")
	}
	arrivals := gate.countRequests()
	gate.releaseAll()
	f.l1Probe.clearGate(gate)
	startErrors := []error{<-results, <-results}
	successes := 0
	for _, err := range startErrors {
		if err == nil {
			successes++
		} else {
			require.ErrorContains(t, err, "already running")
		}
	}
	t.Logf("parallel starts: successes=%d identity_requests_before_release=%d", successes, arrivals)
	require.Equal(t, 1, successes, "exactly one concurrent start must create a miner; identity barrier arrivals=%d", arrivals)
	require.Equal(t, firstExpected, manager.GetBTCLuckyMiningStatus().RewardAddress,
		"the first wallet account must own the miner reward address")
	require.Eventually(t, func() bool {
		return f.l1Probe.countSuffix("/btc/lucky/template") > templateBeforeConcurrentStart
	}, 10*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool {
		return manager.GetBTCLuckyMiningStatus().TemplateID != ""
	}, 10*time.Second, 50*time.Millisecond, "the concurrent start must receive a valid template")
	manager.StopBTCLuckyMining()
	settled := f.l1Probe.countSuffix("/btc/lucky/template")
	require.Never(t, func() bool { return f.l1Probe.countSuffix("/btc/lucky/template") > settled }, 11*time.Second, 100*time.Millisecond,
		"Stop must leave no untracked miner polling the main template provider")
	t.Logf("template requests after concurrent Stop: %d", f.l1Probe.countSuffix("/btc/lucky/template")-settled)

	require.NoError(t, manager.SwitchAccount(1))
	require.EqualValues(t, 1, manager.GetCurrentAccountId())
	accountStatus, err := manager.StartBTCLuckyMining(wallet.BTCLuckyMiningConfig{Jobs: "1", LowPriority: true, LowPrioritySleep: "1h"})
	require.NoError(t, err)
	require.Equal(t, firstExpected, accountStatus.RewardAddress, "starting from account 1 must still use root account 0")
	require.Eventually(t, func() bool {
		return manager.GetBTCLuckyMiningStatus().TemplateID != ""
	}, 10*time.Second, 50*time.Millisecond, "the miner must receive a valid identity-encoded template")
	require.True(t, manager.GetBTCLuckyMiningStatus().Running)
	require.Equal(t, firstExpected, manager.GetBTCLuckyMiningStatus().RewardAddress)
	require.NoError(t, manager.SwitchAccount(0))
	require.EqualValues(t, 0, manager.GetCurrentAccountId())
	require.True(t, manager.GetBTCLuckyMiningStatus().Running, "switching accounts must leave the root miner running")
	require.Equal(t, firstExpected, manager.GetBTCLuckyMiningStatus().RewardAddress)
	require.NoError(t, manager.SwitchAccount(1))
	require.True(t, manager.GetBTCLuckyMiningStatus().Running, "switching back to account 1 must leave the root miner running")
	require.Equal(t, firstExpected, manager.GetBTCLuckyMiningStatus().RewardAddress)

	secondMnemonic := luckyTestMnemonic(t)
	secondID, err := manager.ImportWallet(secondMnemonic, "regtest-acceptance-only")
	require.NoError(t, err)
	require.NotEqual(t, firstID, secondID)
	require.NoError(t, manager.SwitchWallet(firstID, ""))
	require.True(t, manager.GetBTCLuckyMiningStatus().Running, "switching to the root wallet must leave the miner running")
	require.Equal(t, firstExpected, manager.GetBTCLuckyMiningStatus().RewardAddress)
	require.NoError(t, manager.SwitchWallet(secondID, ""))
	require.EqualValues(t, secondID, manager.GetCurrentWalletId())
	require.True(t, manager.GetBTCLuckyMiningStatus().Running, "switching wallets must leave the root miner running")
	require.Equal(t, firstExpected, manager.GetBTCLuckyMiningStatus().RewardAddress)
	manager.StopBTCLuckyMining()
	settled = f.l1Probe.countSuffix("/btc/lucky/template")
	require.Never(t, func() bool { return f.l1Probe.countSuffix("/btc/lucky/template") > settled }, 11*time.Second, 100*time.Millisecond,
		"explicit Stop must leave no untracked miner polling the provider")
	secondStatus, err := manager.StartBTCLuckyMining(wallet.BTCLuckyMiningConfig{Jobs: "1", LowPriority: true, LowPrioritySleep: "1h"})
	require.NoError(t, err)
	require.Equal(t, firstExpected, secondStatus.RewardAddress, "starting from another wallet must still use the first root account")
	manager.StopBTCLuckyMining()
	settled = f.l1Probe.countSuffix("/btc/lucky/template")
	require.Never(t, func() bool { return f.l1Probe.countSuffix("/btc/lucky/template") > settled }, 11*time.Second, 100*time.Millisecond,
		"explicit Stop after a secondary-wallet start must stop the root miner")
}

func prepareLuckySubaccount(t *testing.T, manager *wallet.Manager, network *realSatoshiNet, mnemonic string) int64 {
	t.Helper()
	attachRGB11RegtestWallet(t, manager, network)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, discoveryErr := manager.RecoverAccountManagementFromRootMnemonic(ctx, mnemonic, "regtest-acceptance-only")
	require.ErrorIs(t, discoveryErr, wallet.ErrRootAccountNotFound,
		"use an isolated root whose real DKVS discovery confirms no existing wrapper")
	ensureRGB11RegtestAccountManagement(t, manager)
	authorization, err := manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 100)
	require.NoError(t, err)
	ensureRGB11RegtestRecoveryPackage(t, manager, authorization)
	require.NoError(t, manager.SyncAccountManagementState(ctx))
	require.NoError(t, manager.SyncAccountRootWrapper(ctx))
	require.NoError(t, manager.WaitAccountManagedDataReady(ctx))
	rootID := manager.GetCurrentWalletId()
	require.NoError(t, manager.EnsureAccount(rootID, 1, "Lucky Mining", ""))
	require.NoError(t, manager.SyncAccountManagementState(ctx))
	require.NoError(t, manager.WaitAccountManagedDataReady(ctx))
	return rootID
}

func luckyTestMnemonic(t *testing.T) string {
	return luckyMnemonic(t, "switch-wallet")
}

func luckyMnemonic(t *testing.T, role string) string {
	t.Helper()
	seed := sha256.Sum256([]byte("sat20-lucky-manager-" + role))
	phrase, err := bip39.NewMnemonic(seed[:16])
	require.NoError(t, err)
	return phrase
}

func testLuckyMonitor(t *testing.T, f *luckyMgrFixture) {
	t.Helper()
	f.l1Probe.setHardBits(false)
	rootMnemonic := luckyMnemonic(t, "monitor-root")
	manager, closeManager := newLuckyManager(t, f, true, rootMnemonic)
	defer closeManager()
	rootPub := manager.GetWallet().GetPubKey().SerializeCompressed()
	coreKey := keyFromMnemonic(t, f.seeds["core"], 0)
	wantAddress, err := wallet.GetP2WSHaddress(rootPub, coreKey.PubKey().SerializeCompressed())
	require.NoError(t, err)
	l1Ticks := make(chan struct{}, 8)
	manager.RegisterMonitorTickCallback(func(sendTxInL1 bool) {
		if sendTxInL1 {
			select {
			case l1Ticks <- struct{}{}:
			default:
			}
		}
	})
	utxoBeforeStart := f.l1Probe.countContains("/allutxos/address/")
	bestHeightBeforeStart := f.l1Probe.countSuffix("/bestheight")
	blockHashBeforeStart := f.l1Probe.countContains("/btc/block/blockhash/")
	waitL1Tick := func() {
		t.Helper()
		select {
		case <-l1Ticks:
		case <-time.After(25 * time.Second):
			t.Fatal("timed out waiting for an L1 monitor tick")
		}
	}
	_ = prepareLuckySubaccount(t, manager, f.network, rootMnemonic)
	require.NoError(t, manager.SwitchAccount(1))
	require.EqualValues(t, 1, manager.GetCurrentAccountId(), "the auto-ascend must run while account 1 stays selected")
	ledgerBeforeStart := f.l2Probe.countContains("/v3/channel/ledger/")
	active, err := manager.AnchorOutputsActive()
	require.NoError(t, err)
	require.True(t, active, "Manager must select output-bound invoices after the actual L2 activation height")
	baseHash, baseHeight, err := f.network.Bootstrap.Client.GetBestBlock()
	require.NoError(t, err)
	baseBlock := blockAtHeight(t, f.network.Bootstrap, baseHeight)
	manager.Start()
	require.Eventually(t, manager.IsReady, 30*time.Second, 100*time.Millisecond)
	var initialHeight int
	require.NoError(t, f.core.call("getblockcount", nil, &initialHeight))
	waitL1Tick()
	require.Eventually(t, func() bool { return f.l1Probe.countContains("/allutxos/address/") > utxoBeforeStart }, 5*time.Second, 50*time.Millisecond,
		"the first L1 monitor tick must scan current reward-address UTXOs")
	firstUTXOCount := f.l1Probe.countContains("/allutxos/address/")
	firstLedgerCount := f.l2Probe.countContains("/v3/channel/ledger/")
	firstBestHeightCount := f.l1Probe.countSuffix("/bestheight")
	firstBlockHashCount := f.l1Probe.countContains("/btc/block/blockhash/")
	require.Greater(t, firstBestHeightCount, bestHeightBeforeStart, "the monitor tick must read the indexed BTC tip")
	require.Greater(t, firstBlockHashCount, blockHashBeforeStart, "the monitor tick must identify the indexed BTC tip")
	require.Equal(t, ledgerBeforeStart, firstLedgerCount,
		"the initial background scan must not read historical SatoshiNet channel ledger entries")
	waitL1Tick()
	require.Equal(t, firstUTXOCount, f.l1Probe.countContains("/allutxos/address/"),
		"a repeated tick at the same tip must not rescan current reward-address UTXOs")
	require.Equal(t, firstLedgerCount, f.l2Probe.countContains("/v3/channel/ledger/"),
		"idle monitor ticks must not read historical SatoshiNet channel ledger entries")
	var unchangedHeight int
	require.NoError(t, f.core.call("getblockcount", nil, &unchangedHeight))
	require.Equal(t, initialHeight, unchangedHeight, "the idle-tip assertion requires an unchanged Bitcoin tip")

	miningStatus, err := manager.StartBTCLuckyMining(wallet.BTCLuckyMiningConfig{Jobs: "1"})
	require.NoError(t, err)
	require.Equal(t, wantAddress, miningStatus.RewardAddress, "a miner started from account 1 must reward root account 0")
	require.Eventually(t, func() bool {
		return manager.GetBTCLuckyMiningStatus().TemplateID != ""
	}, 10*time.Second, 50*time.Millisecond, "the root miner must receive a valid provider template")
	var found *btclucky.FoundBlockRecord
	require.Eventually(t, func() bool {
		status := manager.GetBTCLuckyMiningStatus()
		for _, item := range status.FoundBlocks {
			if item.Submitted && item.RewardAddress == manager.GetBTCLuckyMiningStatus().RewardAddress {
				copy := item
				found = &copy
				return true
			}
		}
		return false
	}, 90*time.Second, 100*time.Millisecond, "Manager miner must submit a block accepted by the isolated Bitcoin Core")
	manager.StopBTCLuckyMining()
	var address string
	require.NoError(t, f.coreWallet.call("getnewaddress", nil, &address))
	var blocks []string
	require.NoError(t, f.coreWallet.call("generatetoaddress", []any{9, address}, &blocks))
	require.Len(t, blocks, 9)
	waitLuckyIndexer(t, f.primary, f.core)

	outpoint := found.CoinbaseTxID + ":" + strconv.FormatUint(uint64(found.Vout), 10)
	var anchorTx *wire.MsgTx
	var submittedAt time.Time
	require.Eventually(t, func() bool {
		anchorTx = findAnchorTxInMempool(t, f.network.Core, outpoint)
		if anchorTx == nil {
			anchorTx = findAnchorTxInMempool(t, f.network.Bootstrap, outpoint)
		}
		if anchorTx != nil && submittedAt.IsZero() {
			submittedAt = time.Now()
		}
		return anchorTx != nil
	}, 90*time.Second, 25*time.Millisecond, "the mature Manager auto-ascend must reach a real SatoshiNet mempool")
	require.False(t, submittedAt.IsZero())
	anchorTxID := anchorTx.TxHash().String()
	invoice, err := wallet.ParseStandardAnchorScript(anchorTx.TxIn[0].SignatureScript)
	require.NoError(t, err)
	require.Equal(t, outpoint, invoice.Utxo, "the scheduled anchor must bind the exact mature Bitcoin outpoint")
	anchorBlockHash, anchorHeight, _, _ := waitForScheduledSatsNetTx(t, f.network, anchorTxID,
		submittedAt, baseHeight, baseHash, baseBlock.Header.Timestamp)
	t.Logf("confirmed mature lucky anchor: funding_utxo=%s txid=%s height=%d blockhash=%s", outpoint, anchorTxID, anchorHeight, anchorBlockHash)
	var anchor *sindexercommon.AscendData
	require.Eventually(t, func() bool {
		var err error
		anchor, err = manager.GetIndexerRPCClient_SatsNet().GetAscendData(outpoint)
		return err == nil && anchor != nil && anchor.AnchorTxId != "" && anchor.BTCLucky != nil
	}, 90*time.Second, 250*time.Millisecond, "the stopped manager must auto-ascend the mature reward without an explicit rewards query")
	require.Equal(t, outpoint, anchor.FundingUtxo)
	require.Equal(t, anchorTxID, anchor.AnchorTxId)
	require.Equal(t, int(anchorHeight), anchor.Height)
	require.Equal(t, found.BlockHeight, int64(anchor.BTCLucky.BlockHeight))
	require.Equal(t, found.BlockHash, anchor.BTCLucky.BlockHash.String())
	require.Equal(t, uint32(1), manager.GetCurrentAccountId(), "auto-ascend must preserve the selected child account")
	autoScanCount := f.l1Probe.countContains("/allutxos/address/")
	require.Greater(t, autoScanCount, firstUTXOCount, "a changed, mature Bitcoin tip must trigger a current-UTXO scan")
	autoLedgerCount := f.l2Probe.countContains("/v3/channel/ledger/")
	require.Equal(t, firstLedgerCount, autoLedgerCount,
		"background discovery and auto-ascend must not read the historical channel ledger")
	for len(l1Ticks) > 0 {
		<-l1Ticks
	}
	waitL1Tick()
	require.Equal(t, autoScanCount, f.l1Probe.countContains("/allutxos/address/"),
		"the same mature BTC tip must not cause another UTXO scan")
	require.Equal(t, autoLedgerCount, f.l2Probe.countContains("/v3/channel/ledger/"),
		"the same mature BTC tip must not trigger a historical ledger read")

	rewards, err := manager.GetBTCLuckyRewards()
	require.NoError(t, err, "the explicit rewards API must remain available separately from background scanning")
	var target *wallet.BTCLuckyReward
	for _, reward := range rewards {
		if reward.FundingUtxo == outpoint {
			target = reward
			break
		}
	}
	require.NotNil(t, target, "the explicit rewards query must include the target outpoint")
	require.Equal(t, "claimable", target.Status)
	require.Equal(t, anchor.AnchorTxId, target.AnchorTxID)
	require.Greater(t, f.l1Probe.countContains("/allutxos/address/"), autoScanCount,
		"the explicit rewards query must query current reward-address UTXOs")
	require.Greater(t, f.l2Probe.countContains("/v3/channel/ledger/"), autoLedgerCount,
		"the explicit rewards query must be the operation that reads historical channel ledger entries")
}

func testLuckyBlockHash(t *testing.T, f *luckyMgrFixture) {
	t.Helper()
	f.l1Probe.setHardBits(false)
	manager, closeManager := newLuckyManager(t, f, true)
	defer closeManager()
	walletPub := manager.GetWallet().GetPubKey().SerializeCompressed()
	serviceKey := keyFromMnemonic(t, f.seeds["core"], 0)
	witness, _, err := wallet.GetP2WSHscript(walletPub, serviceKey.PubKey().SerializeCompressed())
	require.NoError(t, err)
	_, err = manager.StartBTCLuckyMining(wallet.BTCLuckyMiningConfig{Jobs: "1"})
	require.NoError(t, err)
	var found *btclucky.FoundBlockRecord
	require.Eventually(t, func() bool {
		status := manager.GetBTCLuckyMiningStatus()
		for _, item := range status.FoundBlocks {
			if item.Submitted && item.RewardAddress == status.RewardAddress {
				copy := item
				found = &copy
				return true
			}
		}
		return false
	}, 90*time.Second, 100*time.Millisecond, "Manager miner must submit a source block for the active-chain check")
	manager.StopBTCLuckyMining()
	var address string
	require.NoError(t, f.coreWallet.call("getnewaddress", nil, &address))
	var blocks []string
	require.NoError(t, f.coreWallet.call("generatetoaddress", []any{9, address}, &blocks))
	require.Len(t, blocks, 9)
	waitLuckyIndexer(t, f.primary, f.core)
	outpoint := found.CoinbaseTxID + ":" + strconv.FormatUint(uint64(found.Vout), 10)
	reward, err := manager.InspectBTCLuckyReward(outpoint, witness)
	require.NoError(t, err)
	require.Equal(t, found.BlockHash, reward.BTCBlockHash)
	coinbaseTxID := strings.SplitN(outpoint, ":", 2)[0]
	info, err := manager.GetIndexerRPCClient().GetTxInfo(coinbaseTxID)
	require.NoError(t, err)
	require.Equal(t, coinbaseTxID, info.TxID)
	require.Equal(t, reward.BTCBlockHash, info.BlockHash, "simpleinfo must carry the exact active block hash for the source transaction")

	other, otherWallet, mineOther := spRealBitcoind(t)
	otherWallet.client.Timeout = 2 * time.Minute
	mineOther()
	var otherAddress string
	require.NoError(t, otherWallet.call("getnewaddress", nil, &otherAddress))
	var height int
	require.NoError(t, other.call("getblockcount", nil, &height))
	if height < int(reward.BTCHeight) {
		var blocks []string
		require.NoError(t, otherWallet.call("generatetoaddress", []any{int(reward.BTCHeight) - height, otherAddress}, &blocks))
	}
	var otherHash string
	require.NoError(t, other.call("getblockhash", []any{reward.BTCHeight}, &otherHash))
	require.NotEqual(t, reward.BTCBlockHash, otherHash, "two independent private regtest mains must expose different blocks at the same height")

	f.l1Probe.mu.Lock()
	f.l1Probe.blockHashFn = func(got int) string {
		if got == int(reward.BTCHeight) {
			return otherHash
		}
		return ""
	}
	f.l1Probe.mu.Unlock()
	_, err = manager.InspectBTCLuckyReward(outpoint, witness)
	require.Error(t, err, "a source tx from one main-chain view must be rejected against another same-height view")
	require.Contains(t, strings.ToLower(err.Error()), "block", "the rejection must identify the source block membership")
}
