package e2e

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	failSP      atomic.Bool
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
		// The real SatoshiNet harness uses the normal L1 regtest prefix.
		// Keep the SP browser fixture's existing /test paths unchanged.
		if strings.HasPrefix(r.URL.Path, "/btc/regtest/") {
			r = r.Clone(r.Context())
			r.URL.Path = "/test/" + strings.TrimPrefix(r.URL.Path, "/btc/regtest/")
		}
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
		if f.failSP.Load() && strings.HasPrefix(r.URL.Path, "/test/v1/sp/") {
			http.Error(w, "injected interrupted SP refresh", http.StatusServiceUnavailable)
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
	// Keep background account discovery on the fixture's real isolated Core.
	// Never attach to or reuse the persistent PWA acceptance node ports.
	t.Setenv("SAT20_REAL_SATOSHINET_ATTACH", "")
	t.Setenv("SAT20_REAL_SATOSHINET_PORT_BASE", strconv.Itoa(luckyNodePortBase(t)))
	t.Setenv("SAT20_REAL_L1_INDEXER", f.server.URL+"/btc/regtest")
	t.Setenv("SAT20_REAL_REGTEST_RUNTIME", testCaseDirectory(t))
	prebuiltBundle, bundleErr := readE2EArtifactBundle()
	require.NoError(t, bundleErr)
	if prebuiltBundle == nil {
		_, source, _, ok := runtime.Caller(0)
		require.True(t, ok)
		t.Setenv("SAT20_REAL_TEST_WALLETS", filepath.Join(filepath.Dir(source), "testdata", "regtest", "wallets.json"))
	} else {
		t.Setenv("SAT20_REAL_TEST_WALLETS", prebuiltSPWalletFixture(t))
	}
	network := prepareSDKPWARegtestNodes(t)
	coreURL, err := network.Core.IndexerURL("regtest")
	require.NoError(t, err)
	endpoint := &sdkcommon.Indexer{Scheme: "http", Host: strings.TrimPrefix(f.server.URL, "http://"), Proxy: "test"}
	f.cfg = &sdkcommon.Config{Env: "dev", Chain: "regtest", Mode: "client", IndexerL1: endpoint,
		IndexerL2: &sdkcommon.Indexer{Scheme: "http", Host: strings.TrimSuffix(strings.TrimPrefix(coreURL, "http://"), "/regtest"), Proxy: "regtest"},
		Peers: []string{"s@" + network.Core.nodePubKey + "@http://" + network.Core.stpAddr + "/regtest",
			"b@" + network.Bootstrap.nodePubKey + "@http://" + network.Bootstrap.stpAddr + "/regtest"}}
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
	if path == "/health" {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		return
	}
	if path == "/v3/indexer/pubkey" && r.Method == http.MethodGet {
		// Declare this isolated L1 fixture's configured indexer identity. DKVS
		// records and authenticated absence still come from the real L2 nodes.
		_ = json.NewEncoder(w).Encode(indexerwire.IndexerPubKeyResp{
			BaseResp: indexerwire.BaseResp{Code: 0}, PubKey: indexercommon.GetBootstrapPubKey(),
		})
		return
	}
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
	require.NoError(t, manager.RetryDKVSSync())
	require.Eventually(t, func() bool {
		return manager.GetAccountManagementStatus().LocalOnly
	}, 20*time.Second, 20*time.Millisecond, "real DKVS must confirm account absence before SP fixture edits")
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

	t.Run("invalid_checksum_never_broadcasts", func(t *testing.T) {
		last := "q"
		if strings.HasSuffix(address, last) {
			last = "p"
		}
		_, err := sender.SendSilentPayment(address[:len(address)-1]+last, 2000, 2)
		require.Error(t, err)
		require.Equal(t, int64(1), f.broadcasts.Load())
	})
	t.Run("interrupted_refresh_preserves_real_receipt", func(t *testing.T) {
		id := receiver.GetWallet().GetId()
		before, err := receiver.LastSyncedSilentPay(id, 1)
		require.NoError(t, err)
		f.failSP.Store(true)
		defer f.failSP.Store(false)
		_, err = receiver.SyncSilentPayLive(context.Background())
		require.Error(t, err)
		after, err := receiver.LastSyncedSilentPay(id, 1)
		require.NoError(t, err)
		require.Equal(t, before, after)
		f.failSP.Store(false)
		retried, err := receiver.SyncSilentPayLive(context.Background())
		require.NoError(t, err)
		require.Equal(t, []wallet.SPAccountBalance{before}, retried)
	})
	t.Run("cancelled_refresh_preserves_real_receipt", func(t *testing.T) {
		id := receiver.GetWallet().GetId()
		before, err := receiver.LastSyncedSilentPay(id, 1)
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = receiver.SyncSilentPayLive(ctx)
		require.ErrorIs(t, err, context.Canceled)
		after, err := receiver.LastSyncedSilentPay(id, 1)
		require.NoError(t, err)
		require.Equal(t, before, after)
		require.Equal(t, int64(1), f.broadcasts.Load())
	})
	t.Run("subaccount_selection_does_not_move_funds", func(t *testing.T) {
		id := receiver.GetWallet().GetId()
		other, err := receiver.EnableSilentPay(id, 0)
		require.NoError(t, err)
		require.NotEqual(t, address, other)
		require.NoError(t, receiver.SwitchAccount(0))
		balances, err := receiver.SyncSilentPayLive(context.Background())
		require.NoError(t, err)
		require.Len(t, balances, 2)
		for _, balance := range balances {
			require.Equal(t, id, balance.WalletID)
			require.True(t, balance.Complete)
			if balance.AccountIndex == 0 {
				require.Zero(t, balance.Balance)
				require.Empty(t, balance.Outputs)
			} else {
				require.Equal(t, uint32(1), balance.AccountIndex)
				require.Equal(t, int64(100000), balance.Balance)
				require.Len(t, balance.Outputs, 1)
				require.Equal(t, txid, balance.Outputs[0].TxID)
			}
		}
		require.NoError(t, receiver.SwitchAccount(1))
		_, same, err := receiver.SilentPayAccount(id, 1)
		require.NoError(t, err)
		require.Equal(t, address, same)
	})
}
