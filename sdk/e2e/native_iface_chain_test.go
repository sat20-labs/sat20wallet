package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
	"github.com/tyler-smith/go-bip39"
)

const nativeIfacePassword = "native-interface-test-only"

// One group owns real Bitcoin Core, the full L1 indexer and the existing
// two-node SatoshiNet fixture. No browser, fixed ports or persistent DB is used.
type nativeIfaceChain struct {
	core        *spRealCoreRPC
	l1          *luckyIndexerProc
	network     *realSatoshiNet
	cfg         *sdkcommon.Config
	seeds       map[string]string
	faucet      *wallet.Manager
	mineAddress string
}

func newNativeIfaceChain(t *testing.T) *nativeIfaceChain {
	t.Helper()
	// Absence of the runtime is a failure, not a silently skipped business gate.
	t.Setenv("SAT20_E2E_REQUIRE_BITCOIND", "1")
	t.Setenv("SAT20_REAL_SATOSHINET_ATTACH", "")
	t.Setenv("SAT20_REAL_SDK_WALLET_DIR", "")
	t.Setenv("SAT20_REAL_SATOSHINET_PORT_BASE", strconv.Itoa(luckyNodePortBase(t)))
	t.Setenv("SAT20_REAL_REGTEST_RUNTIME", testCaseDirectory(t))
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	bundle, err := readE2EArtifactBundle()
	require.NoError(t, err)
	if bundle == nil {
		t.Setenv("SAT20_REAL_TEST_WALLETS", filepath.Join(filepath.Dir(source), "testdata", "regtest", "wallets.json"))
	} else {
		t.Setenv("SAT20_REAL_TEST_WALLETS", prebuiltSPWalletFixture(t))
	}
	seeds := readSDKPWARegtestMnemonics(t)
	l1Ready := e2ePhase(t, "native_iface_l1_ready")
	core, _, _ := spRealBitcoind(t)
	// H0..H1000 rewards are entirely Vintage on the real non-mainnet
	// indexer. Keep those rewards outside the faucet and mature exactly one
	// post-Vintage coinbase before splitting it with the SDK below.
	entropy := sha256.Sum256([]byte("sat20wallet-native-funding-pool"))
	faucetMnemonic, err := bip39.NewMnemonic(entropy[:16])
	require.NoError(t, err)
	faucetKey := keyFromMnemonic(t, faucetMnemonic, 0)
	faucetAddress := nativeIfaceAddress(t, faucetKey.PubKey().SerializeCompressed(), &chaincfg.RegressionNetParams)
	sinkKey := keyFromMnemonic(t, seeds["bootstrap"], 99)
	sink := nativeIfaceAddress(t, sinkKey.PubKey().SerializeCompressed(), &chaincfg.RegressionNetParams)
	for left := 1001; left > 0; {
		blocks := 10
		if left < blocks {
			blocks = left
		}
		require.NoError(t, core.call("generatetoaddress", []any{blocks, sink}, nil))
		left -= blocks
	}
	var fundedBlocks []string
	require.NoError(t, core.call("generatetoaddress", []any{1, faucetAddress}, &fundedBlocks))
	require.Len(t, fundedBlocks, 1)
	for i := 0; i < 10; i++ {
		require.NoError(t, core.call("generatetoaddress", []any{10, sink}, nil))
	}
	var fundingBlock struct {
		Height int      `json:"height"`
		Tx     []string `json:"tx"`
	}
	require.NoError(t, core.call("getblock", []any{fundedBlocks[0], 1}, &fundingBlock))
	require.Equal(t, 1002, fundingBlock.Height)
	require.Len(t, fundingBlock.Tx, 1)
	key := keyFromMnemonic(t, seeds["core"], 0)
	const flushPeriod = 12
	l1 := newLuckyIndexerWithFlush(t, core, hex.EncodeToString(key.PubKey().SerializeCompressed()), flushPeriod)
	l1.startWithin(t, 15*time.Minute)
	waitLuckyIndexer(t, l1, core)
	l1Ready()
	t.Setenv("SAT20_REAL_L1_INDEXER", l1.url)
	f := &nativeIfaceChain{core: core, l1: l1, seeds: seeds, mineAddress: sink}
	f.network = prepareSDKPWARegtestNodes(t)
	l2raw, err := f.network.Core.IndexerURL("regtest")
	require.NoError(t, err)
	l2, err := url.Parse(l2raw)
	require.NoError(t, err)
	l1url, err := url.Parse(l1.url)
	require.NoError(t, err)
	f.cfg = &sdkcommon.Config{Env: "dev", Chain: "regtest", Mode: "client",
		IndexerL1: &sdkcommon.Indexer{Scheme: l1url.Scheme, Host: l1url.Host, Proxy: "btc/regtest"},
		IndexerL2: &sdkcommon.Indexer{Scheme: l2.Scheme, Host: l2.Host, Proxy: "regtest"},
		Peers: []string{"s@" + f.network.Core.nodePubKey + "@http://" + f.network.Core.stpAddr + "/regtest",
			"b@" + f.network.Bootstrap.nodePubKey + "@http://" + f.network.Bootstrap.stpAddr + "/regtest"}}
	f.faucet = f.device(t, faucetMnemonic).mgr
	require.Equal(t, faucetAddress, f.faucet.GetWallet().GetAddress())
	nativeIfaceFundPool(t, f, fundingBlock.Tx[0])
	return f
}

func (f *nativeIfaceChain) mine(t *testing.T, blocks int) {
	t.Helper()
	require.NoError(t, f.core.call("generatetoaddress", []any{blocks, f.mineAddress}, nil))
	waitLuckyIndexer(t, f.l1, f.core)
}

// Preserve the first rare sat and last fee sats outside the funding pool.
// This is the same real, post-H1000 SDK coinbase split as the regtest asset
// fixture, not fabricated indexer metadata or Core wallet coin selection.
func nativeIfaceFundPool(t *testing.T, f *nativeIfaceChain, coinbaseID string) {
	t.Helper()
	var unspent *struct {
		Coinbase      bool `json:"coinbase"`
		Confirmations int  `json:"confirmations"`
	}
	require.NoError(t, f.core.call("gettxout", []any{coinbaseID, 0, true}, &unspent))
	require.NotNil(t, unspent)
	require.True(t, unspent.Coinbase)
	require.Greater(t, unspent.Confirmations, 100)
	point := coinbaseID + ":0"
	input, err := f.faucet.GetIndexerClient().GetTxOutput(point)
	require.NoError(t, err)
	require.NotNil(t, input)
	parent := nativeIfaceRawL1(t, f, coinbaseID)
	require.Equal(t, parent.TxOut[0].Value, input.OutValue.Value)
	require.Equal(t, parent.TxOut[0].PkScript, input.OutValue.PkScript)
	require.False(t, f.faucet.IsLocked(f.faucet.GetWallet().GetAddress(), point))
	sinkScript, err := wallet.AddrToPkScript(f.mineAddress, wallet.GetChainParam())
	require.NoError(t, err)
	split := wire.NewMsgTx(2)
	split.AddTxIn(wire.NewTxIn(input.OutPoint(), nil, nil))
	split.AddTxOut(wire.NewTxOut(330, sinkScript))
	split.AddTxOut(wire.NewTxOut(input.OutValue.Value-330-1000, input.OutValue.PkScript))
	previous := txscript.NewCannedPrevOutputFetcher(input.OutValue.PkScript, input.OutValue.Value)
	signed, err := f.faucet.SignTx(split, previous)
	require.NoError(t, err)
	id, err := f.faucet.BroadcastTx(signed)
	require.NoError(t, err)
	require.Equal(t, signed.TxID(), id)
	require.Equal(t, int64(1000), nativeIfaceL1Fee(t, f, signed))
	f.mine(t, 1)
	plain := f.faucet.GetIndexerClient().GetUtxoListWithTicker(f.faucet.GetWallet().GetAddress(), &indexercommon.ASSET_PLAIN_SAT)
	require.Len(t, plain, 1)
	require.Equal(t, fmt.Sprintf("%s:1", id), plain[0].OutPoint)
	require.Equal(t, signed.TxOut[1].Value, plain[0].Value)
	require.Empty(t, plain[0].Assets)
}

func (f *nativeIfaceChain) fund(t *testing.T, address string, sats int64) {
	t.Helper()
	f.fundOutputs(t, address, sats, 1)
}

func (f *nativeIfaceChain) fundOutputs(t *testing.T, address string, sats int64, count int) {
	t.Helper()
	require.Positive(t, sats)
	require.Positive(t, count)
	txid, _, err := f.faucet.BatchSendPlainSats(address, sats, count, 1, nil)
	require.NoError(t, err)
	require.Len(t, txid, 64)
	f.mine(t, 1)
	want := make(map[string]struct{}, count)
	for vout := range count {
		want[fmt.Sprintf("%s:%d", txid, vout)] = struct{}{}
	}
	outputs := f.faucet.GetIndexerClient().GetUtxoListWithTicker(address, &indexercommon.ASSET_PLAIN_SAT)
	for _, out := range outputs {
		if _, ok := want[out.OutPoint]; ok {
			require.Equal(t, sats, out.Value)
			require.Empty(t, out.Assets)
			delete(want, out.OutPoint)
		}
	}
	require.Empty(t, want, "real SDK funding must create every indexed plain recipient output")
}

type nativeIfaceDevice struct {
	mgr *wallet.Manager
	db  indexercommon.KVDB
	dir string
	cfg *sdkcommon.Config
}

func (f *nativeIfaceChain) device(t *testing.T, mnemonic string) *nativeIfaceDevice {
	t.Helper()
	return newNativeIfaceDevice(t, f.cfg, mnemonic)
}

func newNativeIfaceDevice(t *testing.T, cfg *sdkcommon.Config, mnemonic string) *nativeIfaceDevice {
	t.Helper()
	d := &nativeIfaceDevice{dir: t.TempDir(), cfg: cfg}
	d.open(t)
	t.Cleanup(func() { d.close(t) })
	if mnemonic != "" {
		_, err := d.mgr.ImportWallet(mnemonic, nativeIfacePassword)
		require.NoError(t, err)
		requireE2EInactive(t, d.mgr, nativeIfacePassword)
	}
	return d
}

func (d *nativeIfaceDevice) open(t *testing.T) {
	t.Helper()
	d.db = indexerdb.NewKVDB(d.dir)
	require.NotNil(t, d.db)
	d.mgr = wallet.NewManager(d.cfg, d.db)
	require.NotNil(t, d.mgr)
}

func (d *nativeIfaceDevice) close(t *testing.T) {
	t.Helper()
	if d.mgr != nil {
		d.mgr.Close()
		d.mgr = nil
	}
	if d.db != nil {
		require.NoError(t, d.db.Close())
		d.db = nil
	}
}

// Close both the manager and its real KVDB before constructing fresh handles.
// Unlock is deliberately the caller's responsibility, including failure cases.
func (d *nativeIfaceDevice) reopen(t *testing.T) {
	t.Helper()
	d.close(t)
	d.open(t)
}
