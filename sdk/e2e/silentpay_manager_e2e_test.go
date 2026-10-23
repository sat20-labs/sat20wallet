package e2e

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/gin-gonic/gin"
	indexercommon "github.com/sat20-labs/indexer/common"
	baseindex "github.com/sat20-labs/indexer/indexer/base"
	indexdb "github.com/sat20-labs/indexer/indexer/db"
	rpcbase "github.com/sat20-labs/indexer/rpcserver/base"
	rpcbitcoin "github.com/sat20-labs/indexer/rpcserver/bitcoind"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/indexer/share/bitcoin_rpc"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

// Reuse the SP Core/Base/HTTP fixture. The small ordinary-BTC read adapter
// derives its responses from the same production Base DB and real Core bytes;
// it never invents funding, signatures, SP records or broadcast results.
type spManagerRegtest struct {
	core, miner *spRealCoreRPC
	base        *baseindex.BaseIndexer
	db          indexercommon.KVDB
	server      *httptest.Server
	cfg         *sdkcommon.Config
	mu          sync.Mutex
	broadcasts  atomic.Int64
}

func newSPManagerRegtest(t *testing.T) *spManagerRegtest {
	t.Helper()
	core, miner, _ := spRealBitcoind(t)
	var address string
	require.NoError(t, miner.call("getnewaddress", nil, &address))
	// Bulk maturity mining is setup work, unlike the helper's ordinary 5s RPC
	// reads. Give this one request time to produce 110 actual consensus blocks.
	bulkMining := *core
	bulkMining.client = &http.Client{Timeout: 30 * time.Second}
	for batch := 0; batch < 11; batch++ {
		require.NoError(t, bulkMining.call("generatetoaddress", []any{10, address}, nil), "maturity mining batch %d", batch)
	}
	previousRPC, previousChain := bitcoin_rpc.ShareBitconRpc, indexercommon.CHAIN
	t.Cleanup(func() { bitcoin_rpc.ShareBitconRpc, indexercommon.CHAIN = previousRPC, previousChain })
	indexercommon.CHAIN = "regtest"
	port, err := strconv.Atoi(strings.TrimPrefix(core.base, "http://127.0.0.1:"))
	require.NoError(t, err)
	require.NoError(t, bitcoin_rpc.InitBitconRpc("127.0.0.1", port, core.user, core.pass, false))
	database := indexdb.NewKVDBWithCache(filepath.Join(testCaseDirectory(t), "base"), 1)
	require.NotNil(t, database)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	base := baseindex.NewBaseIndexer(database, &chaincfg.RegressionNetParams, 0, 1000)
	base.Init()
	base.SetBlockCallback(func(*indexercommon.Block, []*indexercommon.Range) {})
	t.Cleanup(base.CloseSilentPayViews)
	f := &spManagerRegtest{core: core, miner: miner, base: base, db: database}
	require.NoError(t, f.sync())
	router := gin.New()
	rpcbase.NewService(&spRealHTTPReader{SilentPayReader: base}).InitRouter(router, "/test")
	(&rpcbitcoin.Service{}).InitRouter(router, "/test")
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/test/control/mine" {
			var a string
			err := miner.call("getnewaddress", nil, &a)
			if err == nil {
				err = core.call("generatetoaddress", []any{1, a}, nil)
			}
			if err == nil {
				err = f.sync()
			}
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"broadcasts": f.broadcasts.Load()})
			return
		}
		if r.URL.Path == "/test/control/stats" {
			_ = json.NewEncoder(w).Encode(map[string]any{"broadcasts": f.broadcasts.Load()})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/test/v1/sp/") || strings.HasPrefix(r.URL.Path, "/test/btc/") || strings.HasPrefix(r.URL.Path, "/test/v3/bitcoin/") {
			if r.Method == http.MethodPost && (r.URL.Path == "/test/btc/tx" || r.URL.Path == "/test/btc/txs" || r.URL.Path == "/test/v3/bitcoin/tx/broadcast") {
				f.broadcasts.Add(1)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			router.ServeHTTP(w, r)
			return
		}
		f.ordinaryRead(w, r)
	}))
	t.Cleanup(f.server.Close)
	endpoint := &sdkcommon.Indexer{Scheme: "http", Host: strings.TrimPrefix(f.server.URL, "http://"), Proxy: "test"}
	f.cfg = &sdkcommon.Config{Env: "dev", Chain: "regtest", Mode: "client", IndexerL1: endpoint, IndexerL2: endpoint,
		Peers: []string{"b@" + indexercommon.GetBootstrapPubKey() + "@" + f.server.URL + "/test"}}
	return f
}

func (f *spManagerRegtest) sync() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if result := f.base.SyncToChainTip(make(chan struct{})); result != 0 {
		return fmt.Errorf("Base sync: %d", result)
	}
	f.base.UpdateDB()
	return nil
}

func (f *spManagerRegtest) outputs(address string) ([]*indexercommon.AssetsInUtxo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, utxos := f.base.GetUTXOsWithAddress(address)
	result := make([]*indexercommon.AssetsInUtxo, 0, len(utxos))
	for id, amount := range utxos {
		height, txIndex, vout := indexercommon.FromUtxoId(id)
		var hash string
		if err := f.core.call("getblockhash", []any{height}, &hash); err != nil {
			return nil, err
		}
		var block struct {
			Tx []string `json:"tx"`
		}
		if err := f.core.call("getblock", []any{hash, 1}, &block); err != nil {
			return nil, err
		}
		if int(txIndex) >= len(block.Tx) {
			return nil, fmt.Errorf("invalid Base position")
		}
		var raw string
		if err := f.core.call("getrawtransaction", []any{block.Tx[txIndex]}, &raw); err != nil {
			return nil, err
		}
		decoded, err := hex.DecodeString(raw)
		if err != nil {
			return nil, err
		}
		tx := wire.NewMsgTx(2)
		err = tx.Deserialize(bytes.NewReader(decoded))
		if err != nil {
			return nil, err
		}
		if int(vout) >= len(tx.TxOut) || tx.TxOut[vout].Value != amount {
			return nil, fmt.Errorf("Base/Core output mismatch")
		}
		result = append(result, &indexercommon.AssetsInUtxo{OutPoint: fmt.Sprintf("%s:%d", block.Tx[txIndex], vout), UtxoId: id,
			Value: amount, PkScript: tx.TxOut[vout].PkScript})
	}
	return result, nil
}

func (f *spManagerRegtest) ordinaryRead(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/test")
	if path == "/bestheight" {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]int{"height": f.base.GetHeight()}})
		return
	}
	const assets = "/v3/address/asset/"
	const summary = "/v3/address/summary/"
	if strings.HasPrefix(path, assets) || strings.HasPrefix(path, summary) {
		address := strings.TrimPrefix(path, summary)
		if strings.HasPrefix(path, assets) {
			address = strings.Split(strings.TrimPrefix(path, assets), "/")[0]
		}
		outputs, err := f.outputs(address)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if strings.HasPrefix(path, assets) {
			_ = json.NewEncoder(w).Encode(indexerwire.UtxosWithAssetRespV3{BaseResp: indexerwire.BaseResp{Code: 0}, Data: outputs})
			return
		}
		var total int64
		for _, out := range outputs {
			total += out.Value
		}
		_ = json.NewEncoder(w).Encode(indexerwire.AssetSummaryRespV3{BaseResp: indexerwire.BaseResp{Code: 0}, Data: []*indexercommon.DisplayAsset{{AssetName: indexercommon.AssetName{Type: "*"}, Amount: strconv.FormatInt(total, 10)}}})
		return
	}
	// Unused L2/name/DKVS discovery is explicitly unavailable, never fabricated.
	_ = json.NewEncoder(w).Encode(map[string]any{"code": -1, "msg": "not provided by the isolated SP L1 fixture"})
}

func (f *spManagerRegtest) manager(t *testing.T, mnemonic string) *wallet.Manager {
	t.Helper()
	db := wallet.NewKVDB(testCaseDirectory(t))
	require.NotNil(t, db)
	manager := wallet.NewManager(f.cfg, db)
	require.NotNil(t, manager)
	t.Cleanup(func() { manager.Close(); require.NoError(t, db.Close()) })
	_, err := manager.ImportWallet(mnemonic, "sp-regtest-only")
	require.NoError(t, err)
	return manager
}

func (f *spManagerRegtest) fund(t *testing.T, address string, sats int64) {
	t.Helper()
	var txid string
	require.NoError(t, f.miner.call("sendtoaddress", []any{address, float64(sats) / 1e8}, &txid))
	var mining string
	require.NoError(t, f.miner.call("getnewaddress", nil, &mining))
	require.NoError(t, f.core.call("generatetoaddress", []any{1, mining}, nil))
	require.NoError(t, f.sync())
}

func TestSPManagerBitcoinCoreSendReceiveE2E(t *testing.T) {
	f := newSPManagerRegtest(t)
	sender, receiver := f.manager(t, minerMnemonic), f.manager(t, coreMnemonic)
	require.NoError(t, receiver.SwitchAccount(1))
	address, err := receiver.EnableSilentPay(receiver.GetWallet().GetId(), 1)
	require.NoError(t, err)
	f.fund(t, sender.GetWallet().GetAddress(), 60000)
	f.fund(t, sender.GetWallet().GetAddress(), 60000)
	txid, err := sender.SendSilentPayment(address, 100000, 2)
	require.NoError(t, err)
	var raw string
	require.NoError(t, f.core.call("getrawtransaction", []any{txid}, &raw))
	tx := spRealDecodeTx(t, raw)
	require.Len(t, tx.TxIn, 2, "exercise final multi-input BIP352 aggregation")
	require.Len(t, tx.TxOut, 2, "payment plus ordinary change")
	require.Equal(t, int64(100000), tx.TxOut[0].Value)
	changeAddress, err := btcutil.DecodeAddress(sender.GetWallet().GetAddress(), &chaincfg.RegressionNetParams)
	require.NoError(t, err)
	change, err := txscript.PayToAddrScript(changeAddress)
	require.NoError(t, err)
	require.Equal(t, change, tx.TxOut[1].PkScript)
	var mining string
	require.NoError(t, f.miner.call("getnewaddress", nil, &mining))
	require.NoError(t, f.core.call("generatetoaddress", []any{1, mining}, nil))
	require.NoError(t, f.sync())
	for i := 0; i < 2; i++ {
		balances, err := receiver.SyncSilentPayLive(context.Background())
		require.NoError(t, err)
		require.Len(t, balances, 1)
		require.True(t, balances[0].Complete)
		require.Equal(t, int64(100000), balances[0].Balance)
		require.Len(t, balances[0].Outputs, 1)
		encoded, err := json.Marshal(balances[0].Outputs)
		require.NoError(t, err)
		require.True(t, bytes.Contains(encoded, []byte(txid)), "receipt must identify the actual confirmed transaction")
	}
	require.Equal(t, int64(1), f.broadcasts.Load())
}

func TestSPSilentPayBrowserE2E(t *testing.T) {
	f := newSPManagerRegtest(t)
	sender, receiver := f.manager(t, minerMnemonic), f.manager(t, coreMnemonic)
	address, err := receiver.EnableSilentPay(receiver.GetWallet().GetId(), 0)
	require.NoError(t, err)
	f.fund(t, sender.GetWallet().GetAddress(), 1000000)
	_, err = sender.SendSilentPayment(address, 5000, 2)
	require.NoError(t, err, "real SP payment is a prerequisite for failure/preservation cases")
	var mining string
	require.NoError(t, f.miner.call("getnewaddress", nil, &mining))
	require.NoError(t, f.core.call("generatetoaddress", []any{1, mining}, nil))
	require.NoError(t, f.sync())
	actor := func(m *wallet.Manager, mnemonic string) map[string]string {
		return map[string]string{"mnemonic": mnemonic, "password": "sp-browser-test-only", "address": m.GetWallet().GetAddress()}
	}
	sp := map[string]any{"sender": actor(sender, minerMnemonic), "receiver": actor(receiver, coreMnemonic), "address": address, "amount": 2000}
	sender.Close()
	receiver.Close()
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	sdk := filepath.Dir(filepath.Dir(source))
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	wasm, runtimeJS := buildPWAWalletRuntime(t, ctx, sdk)
	fixture := map[string]any{"config": f.cfg, "silentPay": sp, "wasmPath": wasm, "wasmRuntimePath": runtimeJS}
	raw, err := json.Marshal(fixture)
	require.NoError(t, err)
	dir := testCaseDirectory(t)
	fixturePath := filepath.Join(dir, "sp-fixture.json")
	require.NoError(t, os.WriteFile(fixturePath, raw, 0600))
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	cases := []string{
		"SP PWA: home receive exposes the private receiving method",
		"SP PWA: enable QR clipboard and reload preserve the same subaccount",
		"SP PWA: reviewed BTC send confirms once and receiver refresh discovers exact funds",
		"SP PWA: late scan cannot overwrite another selected subaccount",
		"SP PWA: network boundary rejects a public switch during a delayed scan",
		"SP PWA: locking during a delayed scan hides private receive data",
		"SP PWA: interrupted refresh preserves verified funds and retries successfully",
		"SP PWA: invalid SP checksum is rejected without broadcasting",
		"SP PWA: cancelling SP confirmation never broadcasts",
	}
	args := append([]string{"scripts/verify/account-management-e2e.mjs", "--pos-fixture", fixturePath, "--wallet-cases"}, cases...)
	logDir := os.Getenv("SAT20_SP_EVIDENCE_DIR")
	if logDir == "" {
		logDir = testCaseDirectory(t)
	}
	require.NoError(t, os.MkdirAll(logDir, 0700))
	log, err := os.OpenFile(filepath.Join(logDir, "sp-pwa.jsonl"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	require.NoError(t, err)
	command := exec.CommandContext(ctx, node, args...)
	command.Dir = filepath.Join(sdk, "..", "pwa")
	command.Stdout, command.Stderr = log, log
	err = command.Run()
	require.NoError(t, log.Close())
	t.Logf("SP browser evidence: %s", log.Name())
	require.NoError(t, err, "SP browser production failures are retained in %s", log.Name())
}
