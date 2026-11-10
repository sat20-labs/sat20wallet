package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	btcblockchain "github.com/btcsuite/btcd/blockchain"
	btcbtcjson "github.com/btcsuite/btcd/btcjson"
	btcbtcutil "github.com/btcsuite/btcd/btcutil"
	btcchaincfg "github.com/btcsuite/btcd/chaincfg"
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

type luckyRelayReply struct {
	path    string
	status  int
	body    []byte
	elapsed time.Duration
}

type luckyRPCRelay struct {
	target        string
	client        *http.Client
	server        *httptest.Server
	mu            sync.Mutex
	mode          string
	bodies        [][]byte
	contentTypes  []string
	responses     [][]byte
	statuses      []int
	readbacks     []luckyRelayReply
	templates     []luckyRelayReply
	submitStart   chan struct{}
	templateStart chan struct{}
	cancelEvents  chan string
}

func newLuckyRPCRelay(t *testing.T, target string) *luckyRPCRelay {
	t.Helper()
	relay := &luckyRPCRelay{
		target: strings.TrimRight(target, "/"), client: &http.Client{Timeout: 2 * time.Minute}, mode: "forward",
		submitStart: make(chan struct{}, 16), templateStart: make(chan struct{}, 16),
		cancelEvents: make(chan string, 16),
	}
	relay.server = httptest.NewServer(http.HandlerFunc(relay.serveHTTP))
	t.Cleanup(relay.server.Close)
	return relay
}

func (p *luckyRPCRelay) url() string { return p.server.URL }

func (p *luckyRPCRelay) setMode(mode string) {
	p.mu.Lock()
	p.mode = mode
	p.mu.Unlock()
}

func (p *luckyRPCRelay) requestBodies() [][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	bodies := make([][]byte, len(p.bodies))
	for i := range p.bodies {
		bodies[i] = append([]byte(nil), p.bodies[i]...)
	}
	return bodies
}

func (p *luckyRPCRelay) submitResponses() ([][]byte, []int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	responses := make([][]byte, len(p.responses))
	for i := range p.responses {
		responses[i] = append([]byte(nil), p.responses[i]...)
	}
	return responses, append([]int(nil), p.statuses...)
}

func (p *luckyRPCRelay) submitContentTypes() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.contentTypes...)
}

func (p *luckyRPCRelay) readbackReplies() []luckyRelayReply {
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneLuckyRelayReplies(p.readbacks)
}

func (p *luckyRPCRelay) templateReplies() []luckyRelayReply {
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneLuckyRelayReplies(p.templates)
}

func cloneLuckyRelayReplies(replies []luckyRelayReply) []luckyRelayReply {
	clones := make([]luckyRelayReply, len(replies))
	for i := range replies {
		clones[i] = replies[i]
		clones[i].body = append([]byte(nil), replies[i].body...)
	}
	return clones
}

func (p *luckyRPCRelay) waitSubmitStart(t *testing.T) {
	t.Helper()
	select {
	case <-p.submitStart:
	case <-time.After(15 * time.Second):
		t.Fatal("relay did not receive a submitblock request")
	}
}

func (p *luckyRPCRelay) waitTemplateStart(t *testing.T) {
	t.Helper()
	select {
	case <-p.templateStart:
	case <-time.After(15 * time.Second):
		t.Fatal("relay did not receive a template request")
	}
}

func (p *luckyRPCRelay) waitCancel(t *testing.T, phase string) {
	t.Helper()
	select {
	case got := <-p.cancelEvents:
		require.Equal(t, phase, got)
	case <-time.After(5 * time.Second):
		t.Fatalf("relay did not observe %s cancellation", phase)
	}
}

func (p *luckyRPCRelay) serveHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	body, err := io.ReadAll(io.LimitReader(r.Body, 9<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	isSubmit := r.Method == http.MethodPost && r.URL.Path == "/v3/bitcoin/block/submit"
	isTemplate := r.Method == http.MethodGet && r.URL.Path == "/btc/lucky/template"
	isReadback := r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/btc/block/blockhash/")
	p.mu.Lock()
	mode := p.mode
	if isSubmit {
		p.bodies = append(p.bodies, append([]byte(nil), body...))
		p.contentTypes = append(p.contentTypes, r.Header.Get("Content-Type"))
	}
	p.mu.Unlock()
	if isSubmit {
		select {
		case p.submitStart <- struct{}{}:
		default:
		}
		switch mode {
		case "unavailable":
			p.writeGateway503(w)
			return
		case "rpc-reject":
			p.writeJSONRPC(w, http.StatusInternalServerError, testJSONRPCID(body), nil,
				&testJSONRPCError{Code: -22, Message: "rejected"})
			return
		case "hold-submit":
			<-r.Context().Done()
			p.cancelEvents <- "submit"
			return
		}
	}
	if isTemplate {
		select {
		case p.templateStart <- struct{}{}:
		default:
		}
	}
	if isTemplate && mode == "hold-template" {
		<-r.Context().Done()
		p.cancelEvents <- "template"
		return
	}
	if isTemplate && mode == "slow-template" && !p.waitSlow(r.Context(), "template") {
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), r.Method, p.target+r.URL.RequestURI(), bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	for key, values := range r.Header {
		if strings.EqualFold(key, "Accept-Encoding") {
			request.Header.Set(key, "identity")
			continue
		}
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	if request.Header.Get("Accept-Encoding") == "" {
		request.Header.Set("Accept-Encoding", "identity")
	}
	response, err := p.client.Do(request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	for key, values := range response.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	status, outputBody := response.StatusCode, responseBody
	if isSubmit && mode == "truncate-forward" && len(outputBody) > 1 {
		outputBody = append([]byte(nil), outputBody[:len(outputBody)/2]...)
	}
	if isSubmit && mode == "slow-submit" && !p.waitSlow(r.Context(), "submit") {
		return
	}
	if isSubmit {
		p.recordResponse(status, outputBody)
	}
	if isTemplate {
		p.recordTemplate(r.URL.Path, status, outputBody, time.Since(started))
	}
	if isReadback {
		p.recordReadback(r.URL.Path, status, outputBody, time.Since(started))
	}
	w.WriteHeader(status)
	_, _ = w.Write(outputBody)
}

func (p *luckyRPCRelay) writeJSONRPC(w http.ResponseWriter, status int, id json.RawMessage, result any, rpcErr *testJSONRPCError) {
	body := testJSONRPCBody(id, result, rpcErr)
	p.recordResponse(status, body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (p *luckyRPCRelay) writeGateway503(w http.ResponseWriter) {
	body := []byte(`{"message":"Service Unavailable"}`)
	p.recordResponse(http.StatusServiceUnavailable, body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write(body)
}

func (p *luckyRPCRelay) waitSlow(ctx context.Context, phase string) bool {
	timer := time.NewTimer(11 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		p.cancelEvents <- phase
		return false
	}
}

func (p *luckyRPCRelay) recordResponse(status int, body []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.responses = append(p.responses, append([]byte(nil), body...))
	p.statuses = append(p.statuses, status)
}

func (p *luckyRPCRelay) recordTemplate(path string, status int, body []byte, elapsed time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.templates = append(p.templates, luckyRelayReply{path: path, status: status, body: append([]byte(nil), body...), elapsed: elapsed})
}

func (p *luckyRPCRelay) recordReadback(path string, status int, body []byte, elapsed time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.readbacks = append(p.readbacks, luckyRelayReply{path: path, status: status, body: append([]byte(nil), body...), elapsed: elapsed})
}

func testJSONRPCID(body []byte) json.RawMessage {
	var request testJSONRPCRequest
	if err := json.Unmarshal(body, &request); err != nil || len(request.ID) == 0 {
		return json.RawMessage("null")
	}
	return request.ID
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

func (g *luckyMinerGate) CurrentTemplate(ctx context.Context) (*btclucky.BlockTemplate, error) {
	if g.stopAfterBlock.Load() {
		select {
		case <-g.finish:
			return nil, fmt.Errorf("acceptance miner stopped")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	template, err := g.backend.CurrentTemplate(ctx)
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

func (g *luckyMinerGate) SubmitBlock(ctx context.Context, raw []byte) (*btclucky.FoundBlockRecord, error) {
	var waitErr error
	g.submitOnce.Do(func() {
		g.stopAfterBlock.Store(true)
		g.pendingBlock <- append([]byte(nil), raw...)
		if g.blockSubmit {
			select {
			case <-g.releaseSubmit:
			case <-ctx.Done():
				waitErr = ctx.Err()
			}
		}
	})
	if waitErr != nil {
		return nil, waitErr
	}
	record, err := g.backend.SubmitBlock(ctx, raw)
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

	t.Run("B_backup_indexer_accepts_after_primary_outage", func(t *testing.T) {
		backup := newLuckyIndexer(t, core, corePub)
		backup.start(t)
		waitLuckyIndexer(t, backup, core)
		primaryRelay := newLuckyRPCRelay(t, indexer.url)
		backupRelay := newLuckyRPCRelay(t, backup.url)
		fallbackAPI := btclucky.NewHTTPTemplateBackend(primaryRelay.url(), 15*time.Second)
		fallbackAPI.SetSubmitURLs([]string{backupRelay.url() + "/v3/bitcoin/block/submit"})
		backupAPI := btclucky.NewHTTPTemplateBackend(backup.url, 15*time.Second)
		gate := newLuckyMinerGate(fallbackAPI, true, true)
		miner, err := btclucky.NewMiner(minerCfg, gate)
		require.NoError(t, err)
		t.Cleanup(func() {
			gate.releaseAll()
			miner.Stop()
		})
		require.NoError(t, miner.Start())
		template := waitLuckyTemplate(t, gate.seenTemplate)
		primaryInfo, err := fallbackAPI.Info()
		require.NoError(t, err)
		require.Equal(t, 1, primaryInfo.Service.TemplateCacheSize, "only the primary indexer should serve the template")
		backupInfo, err := backupAPI.Info()
		require.NoError(t, err)
		require.Zero(t, backupInfo.Service.TemplateCacheSize, "backup must have no template cache before failover")
		gate.allowTemplate()
		raw := waitLuckyBlock(t, gate.pendingBlock)
		block, coinbase := assertLuckyBlock(t, raw, rewardScript, template)
		primaryRelay.setMode("truncate-forward")
		gate.allowSubmit()
		result := waitLuckySubmit(t, gate.result)
		require.NoError(t, result.err)
		require.NotNil(t, result.record)
		require.True(t, result.record.Submitted)
		require.Equal(t, "duplicate", result.record.SubmitResult,
			"the primary accepted the block but its truncated HTTP 200 forced an idempotent backup submission")
		require.Equal(t, block.BlockHash().String(), result.record.BlockHash)
		primaryBodies := primaryRelay.requestBodies()
		backupBodies := backupRelay.requestBodies()
		require.Len(t, primaryBodies, 1, "primary endpoint must receive one submitblock request")
		require.Len(t, backupBodies, 1, "backup indexer must receive the fallback request")
		require.Equal(t, primaryBodies[0], backupBodies[0], "both indexers must receive the same complete JSON-RPC bytes")
		assertLuckySubmitBody(t, primaryBodies[0], raw, block.BlockHash().String())
		require.Equal(t, []string{"application/json"}, primaryRelay.submitContentTypes())
		require.Equal(t, []string{"application/json"}, backupRelay.submitContentTypes())
		primaryResponses, primaryStatuses := primaryRelay.submitResponses()
		backupResponses, backupStatuses := backupRelay.submitResponses()
		require.Equal(t, []int{http.StatusOK}, primaryStatuses)
		require.Error(t, json.Unmarshal(primaryResponses[0], new(any)), "primary HTTP 200 reply must be truncated JSON")
		require.Equal(t, []int{http.StatusOK}, backupStatuses)
		assertLuckySubmitResult(t, backupResponses[0], block.BlockHash().String(), "duplicate")
		require.Empty(t, primaryRelay.readbackReplies(), "no receipt from the primary means no primary-chain readback")
		backupReads := backupRelay.readbackReplies()
		require.Len(t, backupReads, 1, "duplicate receipt must be checked against the submitting backup's active chain")
		assertLuckyReadback(t, backupReads[0], template.Height, block.BlockHash().String())

		backupInfo, err = backupAPI.Info()
		require.NoError(t, err)
		require.Zero(t, backupInfo.Service.TemplateCacheSize, "backup accepted without fetching a template")
		for _, found := range backupInfo.FoundBlocks {
			require.NotEqual(t, block.BlockHash().String(), found.BlockHash,
				"a duplicate receipt is verified client-side without writing a second server record")
		}
		assertLuckyMainChain(t, core, block, coinbase)

		// A duplicate already on the active chain is successful only after the
		// same primary endpoint confirms its blockhash at the reported height.
		primaryRelay.setMode("forward")
		duplicate, err := fallbackAPI.SubmitBlock(context.Background(), raw)
		require.NoError(t, err)
		require.NotNil(t, duplicate)
		require.True(t, duplicate.Submitted)
		require.Equal(t, "duplicate", duplicate.SubmitResult)
		primaryReads := primaryRelay.readbackReplies()
		require.Len(t, primaryReads, 1)
		assertLuckyReadback(t, primaryReads[0], template.Height, block.BlockHash().String())

		// A plain gateway 503 without JSON-RPC fields is retryable and sends the
		// identical block to the backup; the backup returns duplicate.
		indexer.stop(t)
		primaryRelay.setMode("unavailable")
		fallbackDuplicate, err := fallbackAPI.SubmitBlock(context.Background(), raw)
		require.NoError(t, err)
		require.NotNil(t, fallbackDuplicate)
		require.True(t, fallbackDuplicate.Submitted)
		require.Equal(t, "duplicate", fallbackDuplicate.SubmitResult)
		primaryBodies = primaryRelay.requestBodies()
		backupBodies = backupRelay.requestBodies()
		require.Len(t, primaryBodies, 3)
		require.Len(t, backupBodies, 2)
		require.Equal(t, primaryBodies[2], backupBodies[1])
		primaryResponses, primaryStatuses = primaryRelay.submitResponses()
		backupResponses, backupStatuses = backupRelay.submitResponses()
		require.Equal(t, []int{http.StatusOK, http.StatusOK, http.StatusServiceUnavailable}, primaryStatuses)
		require.JSONEq(t, `{"message":"Service Unavailable"}`, string(primaryResponses[2]))
		var gatewayResponse map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(primaryResponses[2], &gatewayResponse))
		require.NotContains(t, gatewayResponse, "jsonrpc")
		require.NotContains(t, gatewayResponse, "id")
		require.Equal(t, []int{http.StatusOK, http.StatusOK}, backupStatuses)
		assertLuckySubmitBody(t, primaryBodies[2], raw, block.BlockHash().String())
		assertLuckySubmitResult(t, backupResponses[1], block.BlockHash().String(), "duplicate")
		backupReads = backupRelay.readbackReplies()
		require.Len(t, backupReads, 2)
		assertLuckyReadback(t, backupReads[1], template.Height, block.BlockHash().String())

		indexer.start(t)
		waitLuckyIndexer(t, indexer, core)
		primaryInfo, err = fallbackAPI.Info()
		require.NoError(t, err)
		require.Contains(t, luckyFoundHashes(primaryInfo), block.BlockHash().String(),
			"primary accepted before its truncated response and must retain its accepted record")
		gate.finishAll()
		miner.Stop()

		// A deterministic RPC rejection over HTTP 500 must not fall through to
		// the configured live backup indexer.
		primaryRelay.setMode("rpc-reject")
		_, rejectErr := fallbackAPI.SubmitBlock(context.Background(), raw)
		require.Error(t, rejectErr)
		require.False(t, errors.Is(rejectErr, btclucky.ErrCoreUnavailable), "a deterministic RPC rejection is not retryable")
		var rpcErr *btcbtcjson.RPCError
		require.ErrorAs(t, rejectErr, &rpcErr)
		require.EqualValues(t, -22, rpcErr.Code)
		require.Equal(t, "rejected", rpcErr.Message)
		primaryBodies = primaryRelay.requestBodies()
		backupBodies = backupRelay.requestBodies()
		require.Len(t, primaryBodies, 4)
		require.Len(t, backupBodies, 2, "rejected submissions must not be sent to backup")
		assertLuckySubmitBody(t, primaryBodies[3], raw, block.BlockHash().String())
		rejectResponses, rejectStatuses := primaryRelay.submitResponses()
		require.Len(t, rejectResponses, 4)
		require.Equal(t, []int{http.StatusOK, http.StatusOK, http.StatusServiceUnavailable, http.StatusInternalServerError}, rejectStatuses)
		var rejectRequest testJSONRPCRequest
		require.NoError(t, json.Unmarshal(primaryBodies[3], &rejectRequest))
		var rejectResponse struct {
			ID    json.RawMessage   `json:"id"`
			Error *testJSONRPCError `json:"error"`
		}
		require.NoError(t, json.Unmarshal(rejectResponses[3], &rejectResponse))
		require.Equal(t, string(rejectRequest.ID), string(rejectResponse.ID), "HTTP 500 JSON-RPC rejection must echo the request ID")
		require.NotNil(t, rejectResponse.Error)
		require.Equal(t, -22, rejectResponse.Error.Code)
		require.Equal(t, "rejected", rejectResponse.Error.Message)
		_, invalidErr := fallbackAPI.SubmitBlock(context.Background(), []byte("invalid block"))
		require.Error(t, invalidErr, "an invalid local block must fail before endpoint fallback")
		require.Len(t, primaryRelay.requestBodies(), 4)
		require.Len(t, backupRelay.requestBodies(), 2)

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
			template.Height, reward.BTCBlockHash, coinbase.TxHash(), reward.Amount, reward.Confirmations, reward.Status, len(primaryInfo.FoundBlocks))
	})

	t.Run("C_near_limit_block_slow_transfers_and_cancel", func(t *testing.T) {
		heavyTxIDs := makeLuckyHeavyTxs(t, core, coreWallet)
		waitLuckyIndexer(t, indexer, core)
		cancelTemplateRelay := newLuckyRPCRelay(t, indexer.url)
		cancelTemplateRelay.setMode("hold-template")
		cancelTemplateAPI := btclucky.NewHTTPTemplateBackend(cancelTemplateRelay.url(), 5*time.Second)
		cancelTemplateMiner, err := btclucky.NewMiner(minerCfg, cancelTemplateAPI)
		require.NoError(t, err)
		t.Cleanup(cancelTemplateMiner.Stop)
		require.NoError(t, cancelTemplateMiner.Start())
		cancelTemplateRelay.waitTemplateStart(t)
		cancelStart := time.Now()
		cancelTemplateMiner.Stop()
		require.Less(t, time.Since(cancelStart), 2*time.Second, "Miner.Stop must cancel an in-flight template transfer")
		cancelTemplateRelay.waitCancel(t, "template")

		slowRelay := newLuckyRPCRelay(t, indexer.url)
		slowRelay.setMode("slow-template")
		slowAPI := btclucky.NewHTTPTemplateBackend(slowRelay.url(), 5*time.Second)
		transferCtx, cancelTransfer := context.WithTimeout(context.Background(), 2*time.Minute)
		templateStart := time.Now()
		template, err := slowAPI.CurrentTemplate(transferCtx)
		templateElapsed := time.Since(templateStart)
		cancelTransfer()
		require.NoError(t, err)
		require.GreaterOrEqual(t, templateElapsed, 10*time.Second,
			"large template transport must succeed beyond the ordinary HTTP client's 10-second timeout")
		require.Len(t, template.Transactions, len(heavyTxIDs))
		require.Equal(t, int64(4_000_000), template.WeightLimit)
		var txWeight int64
		for _, item := range template.Transactions {
			txWeight += item.Weight
			require.Contains(t, heavyTxIDs, item.TxID, "all intentionally large mempool transactions must reach getblocktemplate")
			require.NotEmpty(t, item.Data)
		}
		require.Greater(t, txWeight, int64(3_500_000), "template must approach the mainnet block weight limit")
		templateReplies := slowRelay.templateReplies()
		require.Len(t, templateReplies, 1)
		require.Greater(t, len(templateReplies[0].body), 1_500_000, "template transfer must carry the near-limit transaction set")
		require.GreaterOrEqual(t, templateReplies[0].elapsed, 10*time.Second)

		slowRelay.setMode("slow-submit")
		gate := newLuckyMinerGate(slowAPI, false, true)
		miner, err := btclucky.NewMiner(minerCfg, gate)
		require.NoError(t, err)
		t.Cleanup(func() {
			gate.releaseAll()
			miner.Stop()
		})
		require.NoError(t, miner.Start())
		minedTemplate := waitLuckyTemplate(t, gate.seenTemplate)
		require.Equal(t, template.ID, minedTemplate.ID)
		raw := waitLuckyBlock(t, gate.pendingBlock)
		block, coinbase := assertLuckyBlock(t, raw, rewardScript, template)
		weight := btcblockchain.GetBlockWeight(btcbtcutil.NewBlock(block))
		require.Greater(t, int64(len(raw)), int64(900_000), "near-limit block serialization must cross 900 KB")
		require.GreaterOrEqual(t, template.SizeLimit, int64(len(raw)), "serialized block must fit the Core template size limit")
		require.Greater(t, weight, int64(3_500_000))
		require.LessOrEqual(t, weight, template.WeightLimit)
		submitStart := time.Now()
		gate.allowSubmit()
		result := waitLuckySubmit(t, gate.result)
		submitElapsed := time.Since(submitStart)
		require.NoError(t, result.err)
		require.NotNil(t, result.record)
		require.True(t, result.record.Submitted)
		require.Equal(t, "accepted", result.record.SubmitResult)
		require.GreaterOrEqual(t, submitElapsed, 10*time.Second,
			"large block submission must remain in flight beyond the ordinary HTTP timeout")
		require.Equal(t, raw, result.raw)
		assertLuckyMainChain(t, core, block, coinbase)
		submitResponses, submitStatuses := slowRelay.submitResponses()
		require.Len(t, submitResponses, 1)
		require.Equal(t, []int{http.StatusOK}, submitStatuses)
		assertLuckyNullReceipt(t, submitResponses[0], block.BlockHash().String())
		readbacks := slowRelay.readbackReplies()
		require.Len(t, readbacks, 1)
		assertLuckyReadback(t, readbacks[0], template.Height, block.BlockHash().String())
		t.Logf("near-limit transfer: template_ms=%d template_bytes=%d tx_weight=%d block_bytes=%d block_weight=%d submit_ms=%d",
			templateElapsed.Milliseconds(), len(templateReplies[0].body), txWeight, len(raw), weight, submitElapsed.Milliseconds())
		gate.finishAll()
		miner.Stop()

		cancelSubmitRelay := newLuckyRPCRelay(t, indexer.url)
		cancelSubmitAPI := btclucky.NewHTTPTemplateBackend(cancelSubmitRelay.url(), 5*time.Second)
		cancelSubmitGate := newLuckyMinerGate(cancelSubmitAPI, true, true)
		cancelSubmitMiner, err := btclucky.NewMiner(minerCfg, cancelSubmitGate)
		require.NoError(t, err)
		t.Cleanup(func() {
			cancelSubmitGate.releaseAll()
			cancelSubmitMiner.Stop()
		})
		require.NoError(t, cancelSubmitMiner.Start())
		waitLuckyTemplate(t, cancelSubmitGate.seenTemplate)
		cancelSubmitGate.allowTemplate()
		cancelRaw := waitLuckyBlock(t, cancelSubmitGate.pendingBlock)
		cancelSubmitRelay.setMode("hold-submit")
		cancelSubmitGate.allowSubmit()
		cancelSubmitRelay.waitSubmitStart(t)
		cancelStart = time.Now()
		cancelSubmitMiner.Stop()
		require.Less(t, time.Since(cancelStart), 2*time.Second,
			"Miner.Stop must cancel an in-flight submit transfer")
		cancelSubmitRelay.waitCancel(t, "submit")
		cancelResult := waitLuckySubmit(t, cancelSubmitGate.result)
		require.Equal(t, cancelRaw, cancelResult.raw)
		require.ErrorIs(t, cancelResult.err, btclucky.ErrCoreUnavailable)
		require.Len(t, cancelSubmitRelay.requestBodies(), 1)
	})

	t.Run("D_missing_parent_retry_and_chain_readback", func(t *testing.T) {
		backup := newLuckyIndexer(t, core, corePub)
		backup.start(t)
		waitLuckyIndexer(t, backup, core)
		primaryRelay := newLuckyRPCRelay(t, indexer.url)
		backupRelay := newLuckyRPCRelay(t, backup.url)
		api := btclucky.NewHTTPTemplateBackend(primaryRelay.url(), 15*time.Second)
		api.SetSubmitURLs([]string{backupRelay.url() + "/v3/bitcoin/block/submit"})
		template, err := api.CurrentTemplate(context.Background())
		require.NoError(t, err)
		parentRaw := mineLuckyCandidate(t, minerCfg, template)
		sideRaw := mineLuckyCandidate(t, minerCfg, template)
		parentBlock, parentCoinbase := assertLuckyBlock(t, parentRaw, rewardScript, template)
		sideBlock, _ := assertLuckyBlock(t, sideRaw, rewardScript, template)
		require.NotEqual(t, parentBlock.BlockHash(), sideBlock.BlockHash(), "independent coinbases must create competing blocks")
		require.Equal(t, parentBlock.Header.PrevBlock, sideBlock.Header.PrevBlock)

		childTemplate := *template
		childTemplate.ID += "-child"
		childTemplate.Height = template.Height + 1
		childTemplate.PreviousHash = parentBlock.BlockHash().String()
		childTemplate.CurTime = max(time.Now().Unix(), template.CurTime+2)
		childTemplate.MinTime = childTemplate.CurTime
		childTemplate.MaxTime = childTemplate.CurTime + int64(time.Hour/time.Second)
		childTemplate.Transactions = nil
		childTemplate.MerkleBranch = nil
		childTemplate.DefaultWitnessCommitment = ""
		subsidy := btcblockchain.CalcBlockSubsidy(int32(childTemplate.Height), &btcchaincfg.RegressionNetParams)
		childTemplate.CoinbaseValue = &subsidy
		childRaw := mineLuckyCandidate(t, minerCfg, &childTemplate)
		childBlock, childCoinbase := assertLuckyBlock(t, childRaw, rewardScript, &childTemplate)
		require.Equal(t, parentBlock.BlockHash(), childBlock.Header.PrevBlock,
			"child must depend on the not-yet-submitted parent block")

		_, missingParentErr := api.SubmitBlock(context.Background(), childRaw)
		require.ErrorIs(t, missingParentErr, btclucky.ErrCoreUnavailable,
			"a missing-parent result is retryable so the exact block can be retried after its parent")
		primaryBodies := primaryRelay.requestBodies()
		backupBodies := backupRelay.requestBodies()
		require.Len(t, primaryBodies, 1)
		require.Len(t, backupBodies, 1, "retryable missing-parent result must try the configured independent indexer")
		require.Equal(t, primaryBodies[0], backupBodies[0], "missing-parent fallback must preserve the complete submitblock bytes")
		assertLuckySubmitBody(t, primaryBodies[0], childRaw, childBlock.BlockHash().String())
		require.Empty(t, primaryRelay.readbackReplies(), "missing parent must not produce an accepted receipt readback")
		require.Empty(t, backupRelay.readbackReplies())
		primaryInfo, err := api.Info()
		require.NoError(t, err)
		backupAPI := btclucky.NewHTTPTemplateBackend(backup.url, 15*time.Second)
		backupInfo, err := backupAPI.Info()
		require.NoError(t, err)
		require.NotContains(t, luckyFoundHashes(primaryInfo), childBlock.BlockHash().String())
		require.NotContains(t, luckyFoundHashes(backupInfo), childBlock.BlockHash().String())

		parentResult, err := api.SubmitBlock(context.Background(), parentRaw)
		require.NoError(t, err)
		require.NotNil(t, parentResult)
		require.True(t, parentResult.Submitted)
		require.Equal(t, "accepted", parentResult.SubmitResult)
		assertLuckyMainChain(t, core, parentBlock, parentCoinbase)
		reads := primaryRelay.readbackReplies()
		require.Len(t, reads, 1)
		assertLuckyReadback(t, reads[0], template.Height, parentBlock.BlockHash().String())

		mainDuplicate, err := api.SubmitBlock(context.Background(), parentRaw)
		require.NoError(t, err)
		require.NotNil(t, mainDuplicate)
		require.True(t, mainDuplicate.Submitted)
		require.Equal(t, "duplicate", mainDuplicate.SubmitResult)
		reads = primaryRelay.readbackReplies()
		require.Len(t, reads, 2)
		assertLuckyReadback(t, reads[1], template.Height, parentBlock.BlockHash().String())

		childResult, err := api.SubmitBlock(context.Background(), childRaw)
		require.NoError(t, err, "the same missing-parent block must be accepted after its parent is active")
		require.NotNil(t, childResult)
		require.True(t, childResult.Submitted)
		require.Equal(t, "accepted", childResult.SubmitResult)
		assertLuckyMainChain(t, core, childBlock, childCoinbase)
		reads = primaryRelay.readbackReplies()
		require.Len(t, reads, 3)
		assertLuckyReadback(t, reads[2], childTemplate.Height, childBlock.BlockHash().String())

		var sideSeed string
		require.NoError(t, core.call("submitblock", []any{hex.EncodeToString(sideRaw)}, &sideSeed),
			"seed the competing sibling directly into the isolated Core block index")
		var sideHeader struct {
			Hash          string `json:"hash"`
			Height        int64  `json:"height"`
			Confirmations int64  `json:"confirmations"`
		}
		require.NoError(t, core.call("getblockheader", []any{sideBlock.BlockHash().String()}, &sideHeader))
		require.Equal(t, sideBlock.BlockHash().String(), sideHeader.Hash)
		require.Equal(t, template.Height, sideHeader.Height)
		require.LessOrEqual(t, sideHeader.Confirmations, int64(0), "sibling must be known only on Core's side chain")
		var activeAtParentHeight string
		require.NoError(t, core.call("getblockhash", []any{template.Height}, &activeAtParentHeight))
		require.Equal(t, parentBlock.BlockHash().String(), activeAtParentHeight)

		_, sideErr := api.SubmitBlock(context.Background(), sideRaw)
		require.Error(t, sideErr)
		require.ErrorContains(t, sideErr, "side chain")
		require.False(t, errors.Is(sideErr, btclucky.ErrCoreUnavailable), "a known side-chain duplicate is terminal")
		require.Len(t, backupRelay.requestBodies(), 1, "side-chain duplicate must not be retried on backup")
		require.Len(t, primaryRelay.requestBodies(), 5)
		require.Equal(t, primaryBodies[0], backupBodies[0])
		primaryInfo, err = api.Info()
		require.NoError(t, err)
		require.NotContains(t, luckyFoundHashes(primaryInfo), sideBlock.BlockHash().String(),
			"side-chain duplicate must not be remembered as a found active block")
		reads = primaryRelay.readbackReplies()
		require.Len(t, reads, 4, "duplicate side-chain receipt must be checked against the active blockhash")
		assertLuckyReadback(t, reads[3], template.Height, parentBlock.BlockHash().String())
		submitResponses, _ := primaryRelay.submitResponses()
		require.Len(t, submitResponses, 5)
		assertLuckySubmitResult(t, submitResponses[4], sideBlock.BlockHash().String(), "duplicate")
		t.Logf("Core chain checks: missing_parent_calls=%d/%d parent=%s child=%s side=%s seed_result=%s",
			len(primaryBodies), len(backupBodies), parentBlock.BlockHash(), childBlock.BlockHash(), sideBlock.BlockHash(), sideSeed)
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

func assertLuckySubmitBody(t *testing.T, body, raw []byte, blockHash string) {
	t.Helper()
	var request struct {
		JSONRPC string   `json:"jsonrpc"`
		ID      string   `json:"id"`
		Method  string   `json:"method"`
		Params  []string `json:"params"`
	}
	require.NoError(t, json.Unmarshal(body, &request))
	require.Equal(t, "2.0", request.JSONRPC)
	require.Equal(t, blockHash, request.ID)
	require.Equal(t, "submitblock", request.Method)
	require.Equal(t, []string{hex.EncodeToString(raw)}, request.Params,
		"submitblock must carry the original complete block hex")
}

func assertLuckySubmitResult(t *testing.T, body []byte, blockHash, want string) {
	t.Helper()
	var response struct {
		JSONRPC string            `json:"jsonrpc"`
		ID      string            `json:"id"`
		Result  json.RawMessage   `json:"result"`
		Error   *testJSONRPCError `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &response))
	require.Equal(t, "2.0", response.JSONRPC)
	require.Equal(t, blockHash, response.ID)
	require.Nil(t, response.Error)
	var result string
	require.NoError(t, json.Unmarshal(response.Result, &result))
	require.Equal(t, want, result)
}

func assertLuckyNullReceipt(t *testing.T, body []byte, blockHash string) {
	t.Helper()
	var response struct {
		JSONRPC string            `json:"jsonrpc"`
		ID      string            `json:"id"`
		Result  json.RawMessage   `json:"result"`
		Error   *testJSONRPCError `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &response))
	require.Equal(t, "2.0", response.JSONRPC)
	require.Equal(t, blockHash, response.ID)
	require.Nil(t, response.Error)
	require.JSONEq(t, "null", string(response.Result))
}

func assertLuckyReadback(t *testing.T, reply luckyRelayReply, height int64, blockHash string) {
	t.Helper()
	require.Equal(t, fmt.Sprintf("/btc/block/blockhash/%d", height), reply.path)
	require.Equal(t, http.StatusOK, reply.status)
	var response struct {
		Code int    `json:"code"`
		Data string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(reply.body, &response))
	require.Zero(t, response.Code)
	require.Equal(t, blockHash, response.Data)
}

func luckyFoundHashes(info *btclucky.InfoResponse) []string {
	if info == nil {
		return nil
	}
	hashes := make([]string, len(info.FoundBlocks))
	for i := range info.FoundBlocks {
		hashes[i] = info.FoundBlocks[i].BlockHash
	}
	return hashes
}

func makeLuckyHeavyTxs(t *testing.T, core, coreWallet *spRealCoreRPC) []string {
	t.Helper()
	const txCount, outputCount = 14, 2200
	wallets := make([]*spRealCoreRPC, txCount)
	funding := make(map[string]float64, txCount)
	for i := range wallets {
		name := fmt.Sprintf("lucky-heavy-%d", i)
		var created any
		require.NoError(t, core.call("createwallet", []any{name}, &created))
		wallet := *coreWallet
		wallet.wallet = name
		wallets[i] = &wallet
		var address string
		require.NoError(t, wallet.call("getnewaddress", nil, &address))
		funding[address] = 0.1
	}
	var fundingTx string
	require.NoError(t, coreWallet.call("sendmany", []any{"", funding, 1, "fund independent lucky wallets"}, &fundingTx))
	var miningAddress string
	require.NoError(t, coreWallet.call("getnewaddress", nil, &miningAddress))
	var confirmed []string
	require.NoError(t, core.call("generatetoaddress", []any{1, miningAddress}, &confirmed))
	require.Len(t, confirmed, 1, "each heavy transaction wallet needs an independent confirmed UTXO")

	txIDs := make([]string, 0, txCount)
	for txIndex := 0; txIndex < txCount; txIndex++ {
		amounts := make(map[string]float64, outputCount)
		for outputIndex := 0; outputIndex < outputCount; outputIndex++ {
			seed := []byte(fmt.Sprintf("lucky-heavy-%d-%d", txIndex, outputIndex))
			hash := sha256.Sum256(seed)
			address, err := btcbtcutil.NewAddressWitnessPubKeyHash(hash[:20], &btcchaincfg.RegressionNetParams)
			require.NoError(t, err)
			amounts[address.EncodeAddress()] = 0.00001
		}
		var txID string
		require.NoError(t, wallets[txIndex].call("sendmany", []any{"", amounts, 1, "near-limit lucky block"}, &txID))
		require.Len(t, txID, 64)
		txIDs = append(txIDs, txID)
	}
	return txIDs
}

type luckyCaptureBackend struct {
	template *btclucky.BlockTemplate
	blocks   chan []byte
}

func (b *luckyCaptureBackend) CurrentTemplate(ctx context.Context) (*btclucky.BlockTemplate, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return b.template, nil
	}
}

func (b *luckyCaptureBackend) SubmitBlock(ctx context.Context, raw []byte) (*btclucky.FoundBlockRecord, error) {
	block := btcwire.NewMsgBlock(&btcwire.BlockHeader{})
	if err := block.Deserialize(bytes.NewReader(raw)); err != nil {
		return nil, err
	}
	select {
	case b.blocks <- append([]byte(nil), raw...):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &btclucky.FoundBlockRecord{
		BlockHash: block.BlockHash().String(), Submitted: true, SubmitResult: "captured",
	}, nil
}

func mineLuckyCandidate(t *testing.T, cfg btclucky.BTCLuckyMinerConfig, template *btclucky.BlockTemplate) []byte {
	t.Helper()
	backend := &luckyCaptureBackend{template: template, blocks: make(chan []byte, 1)}
	miner, err := btclucky.NewMiner(cfg, backend)
	require.NoError(t, err)
	require.NoError(t, miner.Start())
	defer miner.Stop()
	select {
	case raw := <-backend.blocks:
		return raw
	case <-time.After(45 * time.Second):
		t.Fatal("shared Bitcoin miner did not produce the candidate block")
		return nil
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
