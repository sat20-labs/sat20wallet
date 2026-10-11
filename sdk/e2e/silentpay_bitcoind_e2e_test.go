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
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
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
	baseapi "github.com/sat20-labs/indexer/share/base_indexer"
	"github.com/sat20-labs/indexer/share/bitcoin_rpc"
	walletsdk "github.com/sat20-labs/sat20wallet/sdk/wallet"
)

// No special build tags, mock Bitcoin RPC or fake Indexer. When bitcoind is
// installed the default E2E suite runs this integration case automatically.
// CI sets SAT20_E2E_REQUIRE_BITCOIND=1 so missing Bitcoin Core is a failure.
type spRealCoreRPC struct {
	base   string
	wallet string
	user   string
	pass   string
	client *http.Client
}

type spRealRPCReply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *spRealCoreRPC) call(method string, params []any, out any) error {
	request := map[string]any{"jsonrpc": "1.0", "id": "sp-e2e", "method": method, "params": params}
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	endpoint := c.base
	if c.wallet != "" {
		endpoint += "/wallet/" + c.wallet
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.user, c.pass)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var reply spRealRPCReply
	if err := json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&reply); err != nil {
		return fmt.Errorf("bitcoind %s status %d: %w", method, res.StatusCode, err)
	}
	if reply.Error != nil {
		return fmt.Errorf("bitcoind %s (%d): %s", method, reply.Error.Code, reply.Error.Message)
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("bitcoind %s HTTP status %d", method, res.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(reply.Result, out)
}

func spRealFreePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
}

func spRealBitcoind(t *testing.T) (*spRealCoreRPC, *spRealCoreRPC, func()) {
	t.Helper()
	binary, err := exec.LookPath("bitcoind")
	if err != nil {
		if os.Getenv("SAT20_E2E_REQUIRE_BITCOIND") == "1" {
			t.Fatal("Bitcoin Core bitcoind is required for the real L1 E2E")
		}
		t.Skip("bitcoind unavailable; install Bitcoin Core to run real L1 E2E")
	}
	dir := t.TempDir()
	rpcPort, p2pPort := spRealFreePort(t), spRealFreePort(t)
	logFile, err := os.Create(filepath.Join(dir, "bitcoind.log"))
	if err != nil {
		t.Fatal(err)
	}
	args := []string{
		"-regtest", "-server=1", "-listen=0", "-dnsseed=0",
		"-txindex=1", "-fallbackfee=0.0002",
		"-datadir=" + dir, "-rpcbind=127.0.0.1",
		"-rpcallowip=127.0.0.1", "-rpcport=" + rpcPort,
		"-port=" + p2pPort,
		"-rpcuser=sp-e2e", "-rpcpassword=sp-e2e-only",
		"-printtoconsole=1",
	}
	cmd := exec.Command(binary, args...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		logFile.Close()
	})
	root := &spRealCoreRPC{
		base: "http://127.0.0.1:" + rpcPort,
		user: "sp-e2e", pass: "sp-e2e-only",
		client: &http.Client{Timeout: 5 * time.Second},
	}
	t.Cleanup(func() {
		_ = root.call("stop", nil, nil)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		var height int
		if err := root.call("getblockcount", nil, &height); err == nil {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	var height int
	if err := root.call("getblockcount", nil, &height); err != nil {
		t.Fatalf("Bitcoin Core regtest failed to start (log %s): %v", logFile.Name(), err)
	}
	var created any
	if err := root.call("createwallet", []any{"sp-e2e"}, &created); err != nil {
		t.Fatal(err)
	}
	wallet := *root
	wallet.wallet = "sp-e2e"
	return root, &wallet, func() {
		var hash string
		if err := wallet.call("getnewaddress", nil, &hash); err != nil {
			t.Fatal(err)
		}
		if err := root.call("generatetoaddress", []any{1, hash}, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func spRealDecodeTx(t *testing.T, txHex string) *wire.MsgTx {
	t.Helper()
	raw, err := hex.DecodeString(txHex)
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	return tx
}

func spRealTxHex(t *testing.T, tx *wire.MsgTx) string {
	t.Helper()
	var raw bytes.Buffer
	if err := tx.Serialize(&raw); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(raw.Bytes())
}

func spRealEvenYKey(t *testing.T) *btcec.PrivateKey {
	t.Helper()
	for n := byte(1); n < 127; n++ {
		var scalar [32]byte
		scalar[31] = n
		key, _ := btcec.PrivKeyFromBytes(scalar[:])
		if key.PubKey().SerializeCompressed()[0] == 2 {
			return key
		}
	}
	t.Fatal("could not find an eligible P2WPKH fixture key")
	return nil
}

type spRealHTTPReader struct {
	baseapi.Indexer
	rpcbase.SilentPayReader
}

func TestSPBitcoinCoreRegtestIndexerE2E(t *testing.T) {
	// Serial: Bitcoin RPC is a repository-wide singleton. Every resource in
	// this test has a private temp path/port; it cannot touch production data.
	core, miner, mine := spRealBitcoind(t)
	var miningAddress string
	if err := miner.call("getnewaddress", nil, &miningAddress); err != nil {
		t.Fatal(err)
	}
	var blocks []string
	if err := core.call("generatetoaddress", []any{110, miningAddress}, &blocks); err != nil {
		t.Fatal(err)
	}

	payer := spRealEvenYKey(t)
	payerAddress, err := btcutil.NewAddressWitnessPubKeyHash(
		btcutil.Hash160(payer.PubKey().SerializeCompressed()), &chaincfg.RegressionNetParams)
	if err != nil {
		t.Fatal(err)
	}
	payerScript, err := txscript.PayToAddrScript(payerAddress)
	if err != nil {
		t.Fatal(err)
	}

	const originalValue = int64(5_000_000)
	var fundingTxid string
	if err := miner.call("sendtoaddress", []any{payerAddress.String(), 0.05}, &fundingTxid); err != nil {
		t.Fatal(err)
	}
	mine()
	var fundingRaw string
	if err := core.call("getrawtransaction", []any{fundingTxid, false}, &fundingRaw); err != nil {
		t.Fatal(err)
	}
	funding := spRealDecodeTx(t, fundingRaw)
	var fundingOut wire.OutPoint
	foundFunding := false
	for vout, output := range funding.TxOut {
		if output.Value == originalValue && bytes.Equal(output.PkScript, payerScript) {
			fundingOut = wire.OutPoint{Hash: funding.TxHash(), Index: uint32(vout)}
			foundFunding = true
			break
		}
	}
	if !foundFunding {
		t.Fatal("real Bitcoin Core funding output not found")
	}

	recipient := walletsdk.NewInternalWalletWithMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"", &chaincfg.RegressionNetParams)
	if recipient == nil {
		t.Fatal("receiver mnemonic unavailable")
	}
	addr, err := recipient.GetSilentPayAddress(1)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := walletsdk.DecodeSilentPayAddress(addr, &chaincfg.RegressionNetParams)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := recipient.DeriveSilentPayKeys(1)
	if err != nil {
		t.Fatal(err)
	}
	script, err := walletsdk.SilentPayDestinationScript(decoded, []walletsdk.SilentPaySenderInput{
		{Outpoint: fundingOut, Key: payer},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}

	payment := wire.NewMsgTx(2)
	payment.AddTxIn(wire.NewTxIn(&fundingOut, nil, nil))
	payment.AddTxOut(wire.NewTxOut(100_000, script))
	payment.AddTxOut(wire.NewTxOut(originalValue-101_000, payerScript))
	fetcher := txscript.NewCannedPrevOutputFetcher(payerScript, originalValue)
	hashes := txscript.NewTxSigHashes(payment, fetcher)
	payment.TxIn[0].Witness, err = txscript.WitnessSignature(payment, hashes, 0,
		originalValue, payerScript, txscript.SigHashAll, payer, true)
	if err != nil {
		t.Fatal(err)
	}
	var paymentTxid string
	if err := core.call("sendrawtransaction", []any{spRealTxHex(t, payment)}, &paymentTxid); err != nil {
		t.Fatalf("Bitcoin Core refused SDK BIP352 payment: %v", err)
	}
	if paymentTxid != payment.TxHash().String() {
		t.Fatal("broadcast transaction identity mismatch")
	}
	mine()

	previousRPC, previousChain := bitcoin_rpc.ShareBitconRpc, indexercommon.CHAIN
	t.Cleanup(func() { bitcoin_rpc.ShareBitconRpc, indexercommon.CHAIN = previousRPC, previousChain })
	indexercommon.CHAIN = "testnet"
	rpcPort, _ := strconv.Atoi(strings.TrimPrefix(core.base, "http://127.0.0.1:"))
	if err := bitcoin_rpc.InitBitconRpc("127.0.0.1", rpcPort, core.user, core.pass, false); err != nil {
		t.Fatalf("production Bitcoin RPC could not connect to real Core: %v", err)
	}
	db := indexdb.NewKVDBWithCache(filepath.Join(t.TempDir(), "base"), 1)
	if db == nil {
		t.Fatal("open real BaseIndexer database")
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	base := baseindex.NewBaseIndexer(db, &chaincfg.RegressionNetParams, 0, 1000)
	base.Init()
	base.SetBlockCallback(func(*indexercommon.Block, []*indexercommon.Range) {})
	t.Cleanup(base.CloseSilentPayViews)
	if h := base.SyncToChainTip(make(chan struct{})); h != 0 {
		t.Fatalf("BaseIndexer sync failed: %d", h)
	}
	base.UpdateDB()

	router := gin.New()
	rpcbase.NewService(&spRealHTTPReader{SilentPayReader: base}).InitRouter(router, "/test")
	(&rpcbitcoin.Service{}).InitRouter(router, "/test")
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	indexerClient := walletsdk.NewIndexerClient("http",
		strings.TrimPrefix(server.URL, "http://"), "/test",
		&walletsdk.NetClient{Client: &http.Client{Timeout: 15 * time.Second}})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	status, err := indexerClient.SPStatus(ctx)
	if err != nil || status.Network != "regtest" || !status.DataReady {
		t.Fatalf("real Indexer SP status wrong: %+v %v", status, err)
	}
	snapshot, err := indexerClient.SPSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var matched walletsdk.SilentPayMatch
	matchedOK := false
	var height int
	var blockHash string
	var txIndex uint32
	cursor := ""
	for pages := 0; pages < 200; pages++ {
		page, err := indexerClient.SPScan(ctx, walletsdk.SPScanRequest{
			Mode: "live", SnapshotID: snapshot.SnapshotID,
			FromHeight: status.CoverageStartHeight, ToHeight: snapshot.Anchor.Height,
			Limit: 100, Cursor: cursor,
		})
		if err != nil {
			t.Fatal(err)
		}
		if page.Anchor != snapshot.Anchor || page.SnapshotID != snapshot.SnapshotID {
			t.Fatal("real Indexer snapshot moved during wallet scan")
		}
		for _, block := range page.Blocks {
			for _, row := range block.Transactions {
				outputs := make([]walletsdk.SilentPayCandidate, 0, len(row.P2TROutputs))
				for _, output := range row.P2TROutputs {
					outputs = append(outputs, walletsdk.SilentPayCandidate{Vout: output.Vout, OutputKey: output.OutputKey})
				}
				recognized, err := walletsdk.MatchSilentPayOutputs(keys, row.Tweak, outputs)
				if err != nil {
					t.Fatal(err)
				}
				for _, m := range recognized {
					if matchedOK {
						t.Fatal("ambiguous SP match")
					}
					height, blockHash, txIndex = block.Height, block.BlockHash, row.TxIndex
					matched = m
					matchedOK = true
				}
			}
		}
		if page.Done {
			break
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			t.Fatal("stuck real Indexer cursor")
		}
		cursor = page.NextCursor
	}
	if !matchedOK || matched.Vout != 0 || matched.K != 0 {
		t.Fatalf("SDK did not discover real Bitcoin Core SP payment: %+v", matched)
	}
	positions := []walletsdk.SPIndexerPosition{{Height: height, BlockHash: blockHash, TxIndex: txIndex, Vout: matched.Vout}}
	located, err := indexerClient.SPLookup(ctx, positions)
	if err != nil || len(located) != 1 || located[0].TxID != paymentTxid {
		t.Fatalf("real Indexer source lookup failed: %+v %v", located, err)
	}
	bitcoinTx, err := indexerClient.GetBitcoinRawTx(paymentTxid)
	if err != nil || bitcoinTx == nil {
		t.Fatalf("real Core rawtx/batch failed: %v", err)
	}
	if bitcoinTx.RawTx == "" {
		t.Fatal("rawtx/batch returned empty transaction")
	}
	if tx := spRealDecodeTx(t, bitcoinTx.RawTx); tx.TxHash() != payment.TxHash() {
		t.Fatal("Indexer rawtx/batch returned wrong txid")
	}

	private, err := walletsdk.SilentPayOutputSpendKey(keys, matched)
	if err != nil {
		t.Fatal(err)
	}
	spending := wire.NewMsgTx(2)
	spOutpoint := wire.OutPoint{Hash: payment.TxHash(), Index: matched.Vout}
	spending.AddTxIn(wire.NewTxIn(&spOutpoint, nil, nil))
	spending.AddTxOut(wire.NewTxOut(99_000, payerScript))
	spFetcher := txscript.NewCannedPrevOutputFetcher(script, 100_000)
	spHashes := txscript.NewTxSigHashes(spending, spFetcher)
	digest, err := txscript.CalcTaprootSignatureHash(spHashes, txscript.SigHashDefault, spending, 0, spFetcher)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := schnorr.Sign(private, digest)
	if err != nil {
		t.Fatal(err)
	}
	spending.TxIn[0].Witness = wire.TxWitness{sig.Serialize()}
	if err := core.call("sendrawtransaction", []any{spRealTxHex(t, spending)}, nil); err != nil {
		t.Fatalf("Bitcoin Core refused recovered SP spending key: %v", err)
	}
	mine()
	if h := base.SyncToChainTip(make(chan struct{})); h != 0 {
		t.Fatalf("BaseIndexer SP spend sync failed: %d", h)
	}
	base.UpdateDB()
	fresh, err := indexerClient.SPSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	page, err := indexerClient.SPScan(ctx, walletsdk.SPScanRequest{
		Mode: "live", SnapshotID: fresh.SnapshotID,
		FromHeight: status.CoverageStartHeight, ToHeight: fresh.Anchor.Height, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range page.Blocks {
		for _, tx := range block.Transactions {
			if block.Height == height && tx.TxIndex == txIndex {
				t.Fatal("fully spent SP output remains Live after real Bitcoin Core confirmation")
			}
		}
	}
	if !base.CheckSelf() {
		t.Fatal("real L1 BaseIndexer CheckSelf rejected Bitcoin Core chain")
	}
	t.Logf("real Core + BaseIndexer SP E2E passed: payment=%s spend=%s height=%d", paymentTxid, spending.TxHash(), fresh.Anchor.Height)
}
