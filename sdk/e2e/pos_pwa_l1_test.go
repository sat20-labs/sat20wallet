package e2e

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	btcbtcec "github.com/btcsuite/btcd/btcec/v2"
	btcutil "github.com/btcsuite/btcd/btcutil"
	btcchaincfg "github.com/btcsuite/btcd/chaincfg"
	btcchainhash "github.com/btcsuite/btcd/chaincfg/chainhash"
	btctxscript "github.com/btcsuite/btcd/txscript"
	btcwire "github.com/btcsuite/btcd/wire"
	indexercommon "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	sdkwallet "github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

// Only the L1 Indexer is simulated. Browser/WASM/STP requests use these normal
// HTTP endpoints; every non-seed transaction is decoded and signature-checked.
// Bound assets follow the actual input/output sat order through the Indexer's
// production Append/Cut operations. No wallet result or balance is injected.
type posPWAL1Indexer struct {
	mu               sync.Mutex
	nodeFixture      *fakeL1Indexer
	bootstrapPub     string
	parentByPub      map[string]string
	height           int64
	seedSequence     int
	outputs          map[string]*indexercommon.TxOutput
	transactions     map[string]*posPWAL1Transaction
	spent            map[string]posPWAL1Spend
	pending          []string
	tickers          map[string]*indexercommon.TickerInfo
	unknownRequests  []string
	checkpointHeight int64
	blocks           map[int64]*btcwire.MsgBlock
	blocksByHash     map[string]*btcwire.MsgBlock
	lucky            *posPWALuckyL1
}

type posPWAL1Transaction struct {
	tx         *btcwire.MsgTx
	raw        string
	height     int64 // -1 means admitted to the fake L1 mempool, not confirmed.
	broadcasts int
}

type posPWAL1Spend struct {
	txid string
	vin  uint32
}

type posPWAL1Snapshot struct {
	Height          int64          `json:"height"`
	PendingTxIDs    []string       `json:"pending_txids"`
	ConfirmedTxIDs  []string       `json:"confirmed_txids"`
	BroadcastCount  map[string]int `json:"broadcast_count"`
	UnknownRequests []string       `json:"unknown_requests,omitempty"`
}

func newPOSPWAL1Indexer(t *testing.T, bootstrapPub string, parentByPub map[string]string) *posPWAL1Indexer {
	t.Helper()
	f := &posPWAL1Indexer{
		bootstrapPub: bootstrapPub, parentByPub: make(map[string]string), height: 100_000,
		outputs:      make(map[string]*indexercommon.TxOutput),
		transactions: make(map[string]*posPWAL1Transaction), spent: make(map[string]posPWAL1Spend),
		tickers:          make(map[string]*indexercommon.TickerInfo),
		checkpointHeight: 99_000, blocks: make(map[int64]*btcwire.MsgBlock), blocksByHash: make(map[string]*btcwire.MsgBlock),
		lucky: newPOSPWALuckyL1(),
	}
	for pub, parent := range parentByPub {
		f.parentByPub[pub] = parent
	}
	// Read-only issuance eligibility used by the partial/remainder mint UI
	// case. The fixture does not claim to index a successful inscription.
	f.tickers["ordx:f:PWAMINT"] = fakeTickerInfo("ordx:f:PWAMINT", "10000", 0, 1)
	f.tickers["ordx:f:PWAMINT"].Limit = "1000"
	server := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	f.nodeFixture = &fakeL1Indexer{server: server, names: make(map[string]string)}
	t.Cleanup(server.Close)
	return f
}

func (f *posPWAL1Indexer) NodeFixture() *fakeL1Indexer { return f.nodeFixture }

func (f *posPWAL1Indexer) SeedUTXO(t *testing.T, output *indexercommon.AssetsInUtxo) {
	t.Helper()
	require.NotNil(t, output)
	point, err := btcwire.NewOutPointFromString(output.OutPoint)
	require.NoError(t, err)
	require.GreaterOrEqual(t, output.Value, int64(0))
	require.NotEmpty(t, output.PkScript)
	copy := output.ToTxOutput().Clone()
	copy.OutValue.PkScript = append([]byte(nil), output.PkScript...)
	// Initial fixture issuance places its bound asset at the start of this
	// known output. Later transfers always derive offsets from real tx order.
	for _, asset := range copy.Assets {
		if asset.BindingSat == 0 || len(copy.Offsets[asset.Name]) != 0 {
			continue
		}
		boundSats := indexercommon.GetBindingSatNum(&asset.Amount, asset.BindingSat)
		require.Greater(t, boundSats, int64(0))
		require.LessOrEqual(t, boundSats, copy.Value())
		copy.Offsets[asset.Name] = indexercommon.AssetOffsets{{Start: 0, End: boundSats}}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotContains(t, f.outputs, output.OutPoint)
	f.seedSequence++
	copy.UtxoId = indexercommon.ToUtxoId(int(f.height)-6, f.seedSequence, int(point.Index))
	f.outputs[output.OutPoint] = copy
	f.addTickerMetadata(copy)
}

// FundAddress creates an initial, confirmed test coinbase with real serialized
// bytes and a matching txid. SeedUTXO is for predeclared POS stake outpoints.
func (f *posPWAL1Indexer) FundAddress(t *testing.T, address string, value int64, assets []*indexercommon.DisplayAsset) string {
	t.Helper()
	addr, err := btcutil.DecodeAddress(address, &btcchaincfg.TestNet4Params)
	require.NoError(t, err)
	script, err := btctxscript.PayToAddrScript(addr)
	require.NoError(t, err)
	f.mu.Lock()
	sequence := f.seedSequence + 1
	seedHeight := f.checkpointHeight + int64(sequence)
	require.Empty(t, f.blocks, "FundAddress must finish before the initial chain is observed")
	require.LessOrEqual(t, seedHeight, f.height-100, "test funding must be mature")
	f.mu.Unlock()
	tx := btcwire.NewMsgTx(2)
	coinbaseScript, err := btctxscript.NewScriptBuilder().AddInt64(seedHeight).AddData([]byte(fmt.Sprintf("pwa-pos-funding-%d", sequence))).Script()
	require.NoError(t, err)
	tx.AddTxIn(btcwire.NewTxIn(&btcwire.OutPoint{Index: 0xffffffff}, coinbaseScript, nil))
	tx.AddTxOut(btcwire.NewTxOut(value, script))
	var raw bytes.Buffer
	require.NoError(t, tx.Serialize(&raw))
	point := fmt.Sprintf("%s:0", tx.TxID())
	f.SeedUTXO(t, &indexercommon.AssetsInUtxo{OutPoint: point, Value: value, PkScript: script, Assets: assets})
	f.mu.Lock()
	f.transactions[tx.TxID()] = &posPWAL1Transaction{tx: tx.Copy(), raw: hex.EncodeToString(raw.Bytes()), height: seedHeight}
	f.outputs[point].UtxoId = indexercommon.ToUtxoId(int(seedHeight), 0, 0)
	f.mu.Unlock()
	return point
}

func (f *posPWAL1Indexer) addTickerMetadata(output *indexercommon.TxOutput) {
	for _, asset := range output.Assets {
		name := asset.Name.String()
		info := f.tickers[name]
		if info == nil {
			f.tickers[name] = fakeTickerInfo(name, asset.Amount.String(), asset.Amount.Precision, int(asset.BindingSat))
			continue
		}
		amount, err := indexercommon.NewDecimalFromString(info.TotalMinted, info.Divisibility)
		if err == nil {
			info.TotalMinted = amount.Add(&asset.Amount).String()
			info.MaxSupply, info.Limit = info.TotalMinted, info.TotalMinted
		}
	}
}

func (f *posPWAL1Indexer) Snapshot() posPWAL1Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := posPWAL1Snapshot{Height: f.height, PendingTxIDs: append([]string{}, f.pending...),
		ConfirmedTxIDs: []string{}, BroadcastCount: make(map[string]int), UnknownRequests: append([]string{}, f.unknownRequests...)}
	for id, tx := range f.transactions {
		if tx.height >= 0 {
			s.ConfirmedTxIDs = append(s.ConfirmedTxIDs, id)
		}
		if tx.broadcasts > 0 {
			s.BroadcastCount[id] = tx.broadcasts
		}
	}
	sort.Strings(s.ConfirmedTxIDs)
	return s
}

func (f *posPWAL1Indexer) RawTx(txid string) (*btcwire.MsgTx, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if tx := f.transactions[txid]; tx != nil {
		return tx.tx.Copy(), true
	}
	return nil, false
}

func (f *posPWAL1Indexer) Output(point string) (*indexercommon.AssetsInUtxo, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.outputs[point]
	if out == nil {
		return nil, false
	}
	return out.Clone().ToAssetsInUtxo(), true
}

// Confirmation is explicit so the browser can close/reopen while its real
// reservation is pending. No timer, retry loop or extra HTTP control API exists.
func (f *posPWAL1Indexer) ConfirmPending() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := append([]string{}, f.pending...)
	if len(ids) == 0 {
		return ids
	}
	previous := f.blockLocked(f.height).BlockHash()
	f.height++
	transactions := make([]*btcwire.MsgTx, 0, len(ids))
	for txIndex, id := range ids {
		record := f.transactions[id]
		record.height = f.height
		transactions = append(transactions, record.tx.Copy())
		for vout := range record.tx.TxOut {
			point := fmt.Sprintf("%s:%d", id, vout)
			if output := f.outputs[point]; output != nil {
				output.UtxoId = indexercommon.ToUtxoId(int(f.height), txIndex+1, vout)
			}
		}
	}
	block := posPWAL1Block(f.height, previous, transactions)
	f.blocks[f.height], f.blocksByHash[block.BlockHash().String()] = block, block
	f.pending = nil
	return ids
}

func decodePOSPWAL1Tx(raw string) (*btcwire.MsgTx, error) {
	data, err := hex.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	reader := bytes.NewReader(data)
	tx := btcwire.NewMsgTx(2)
	if err := tx.Deserialize(reader); err != nil {
		return nil, err
	}
	if reader.Len() != 0 {
		return nil, fmt.Errorf("trailing transaction data")
	}
	if len(tx.TxIn) == 0 || len(tx.TxOut) == 0 {
		return nil, fmt.Errorf("transaction has no inputs or outputs")
	}
	return tx, nil
}

func posPWAL1Outputs(tx *btcwire.MsgTx, outputs map[string]*indexercommon.TxOutput, spent map[string]posPWAL1Spend) (map[string]*indexercommon.TxOutput, error) {
	inputs := indexercommon.NewTxOutput(0)
	prev := make(map[btcwire.OutPoint]*btcwire.TxOut)
	for _, in := range tx.TxIn {
		point := in.PreviousOutPoint.String()
		output := outputs[point]
		if output == nil || spent[point].txid != "" {
			return nil, fmt.Errorf("input %s missing or spent", point)
		}
		if _, exists := prev[in.PreviousOutPoint]; exists {
			return nil, fmt.Errorf("duplicate input %s", point)
		}
		for _, asset := range output.Assets {
			// This fixture transfers ordinary BTC and binding-sat assets. RGB11
			// ownership stays in the real provider and needs no Indexer asset.
			if asset.BindingSat == 0 || len(output.Offsets[asset.Name]) == 0 {
				return nil, fmt.Errorf("L1 fixture cannot infer non-binding transfer %s", asset.Name.String())
			}
		}
		value := output.OutValue
		prev[in.PreviousOutPoint] = &value
		if err := inputs.Append(output); err != nil {
			return nil, err
		}
	}
	var totalOut int64
	for _, out := range tx.TxOut {
		if out.Value < 0 || out.Value > btcutil.MaxSatoshi || totalOut > btcutil.MaxSatoshi-out.Value {
			return nil, fmt.Errorf("invalid output value")
		}
		totalOut += out.Value
	}
	if totalOut > inputs.Value() {
		return nil, fmt.Errorf("transaction creates Bitcoin value")
	}
	if err := sdkwallet.VerifySignedTx(tx, btctxscript.NewMultiPrevOutFetcher(prev)); err != nil {
		return nil, fmt.Errorf("verify actual L1 signatures: %w", err)
	}
	result := make(map[string]*indexercommon.TxOutput, len(tx.TxOut))
	rest := inputs
	for i, out := range tx.TxOut {
		part, next, err := rest.Cut(out.Value)
		if err != nil {
			return nil, fmt.Errorf("allocate output %d: %w", i, err)
		}
		part.OutPointStr = fmt.Sprintf("%s:%d", tx.TxID(), i)
		part.OutValue.PkScript = append([]byte(nil), out.PkScript...)
		part.UtxoId = indexercommon.INVALID_ID
		result[part.OutPointStr] = part
		rest = next
		if rest == nil {
			rest = indexercommon.NewTxOutput(0)
		}
	}
	if rest.HasAsset() {
		return nil, fmt.Errorf("transaction places an indexed asset in the miner fee")
	}
	return result, nil
}

func (f *posPWAL1Indexer) submit(raws []string, commit bool) ([]*indexerwire.TxTestResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	outputs := make(map[string]*indexercommon.TxOutput, len(f.outputs))
	for point, output := range f.outputs {
		outputs[point] = output
	}
	spent := make(map[string]posPWAL1Spend, len(f.spent))
	for point, spend := range f.spent {
		spent[point] = spend
	}
	results := make([]*indexerwire.TxTestResult, 0, len(raws))
	for _, raw := range raws {
		tx, err := decodePOSPWAL1Tx(raw)
		if err != nil {
			return nil, err
		}
		id := tx.TxID()
		result := &indexerwire.TxTestResult{TxId: id, Allowed: true}
		if known := f.transactions[id]; known != nil {
			if known.raw != strings.ToLower(raw) {
				return nil, fmt.Errorf("different serialized transaction already stored for %s", id)
			}
			if commit {
				known.broadcasts++
			}
			results = append(results, result)
			continue
		}
		created, err := posPWAL1Outputs(tx, outputs, spent)
		if err != nil {
			result.Allowed, result.RejectReason = false, err.Error()
			results = append(results, result)
			if commit {
				return results, err
			}
			continue
		}
		for i, in := range tx.TxIn {
			point := in.PreviousOutPoint.String()
			spent[point] = posPWAL1Spend{txid: id, vin: uint32(i)}
			if commit {
				f.spent[point] = spent[point]
			}
		}
		for point, output := range created {
			outputs[point] = output
			if commit {
				f.outputs[point] = output
			}
		}
		if commit {
			f.transactions[id] = &posPWAL1Transaction{tx: tx.Copy(), raw: strings.ToLower(raw), height: -1, broadcasts: 1}
			f.pending = append(f.pending, id)
		}
		results = append(results, result)
	}
	return results, nil
}

// This fake Indexer starts from one explicit checkpoint. It exposes a short,
// self-consistent Bitcoin history, not public-chain data. Every funding tx has
// its own mature coinbase block; every later block preserves broadcast order.
func (f *posPWAL1Indexer) blockLocked(height int64) *btcwire.MsgBlock {
	if height < f.checkpointHeight || height > f.height {
		return nil
	}
	if block := f.blocks[height]; block != nil {
		return block
	}
	for current := f.checkpointHeight; current <= height; current++ {
		if f.blocks[current] != nil {
			continue
		}
		var previous btcchainhash.Hash
		if current > f.checkpointHeight {
			previous = f.blocks[current-1].BlockHash()
		}
		var transactions []*btcwire.MsgTx
		for _, record := range f.transactions {
			if record.height == current {
				transactions = append(transactions, record.tx.Copy())
			}
		}
		// Initial funding blocks contain one coinbase each. ConfirmPending
		// inserts future blocks directly, preserving their topological order.
		block := posPWAL1Block(current, previous, transactions)
		f.blocks[current], f.blocksByHash[block.BlockHash().String()] = block, block
	}
	return f.blocks[height]
}

func posPWAL1Merkle(transactions []*btcwire.MsgTx, witness bool) btcchainhash.Hash {
	hashes := make([]btcchainhash.Hash, len(transactions))
	for i, tx := range transactions {
		if witness {
			if i != 0 {
				hashes[i] = tx.WitnessHash()
			}
		} else {
			hashes[i] = tx.TxHash()
		}
	}
	for len(hashes) > 1 {
		if len(hashes)%2 != 0 {
			hashes = append(hashes, hashes[len(hashes)-1])
		}
		next := make([]btcchainhash.Hash, 0, len(hashes)/2)
		for i := 0; i < len(hashes); i += 2 {
			var pair [64]byte
			copy(pair[:32], hashes[i][:])
			copy(pair[32:], hashes[i+1][:])
			next = append(next, btcchainhash.DoubleHashH(pair[:]))
		}
		hashes = next
	}
	if len(hashes) == 0 {
		return btcchainhash.Hash{}
	}
	return hashes[0]
}

func posPWAL1Block(height int64, previous btcchainhash.Hash, transactions []*btcwire.MsgTx) *btcwire.MsgBlock {
	coinbase := len(transactions) == 1 && len(transactions[0].TxIn) == 1 &&
		transactions[0].TxIn[0].PreviousOutPoint.Hash == (btcchainhash.Hash{}) &&
		transactions[0].TxIn[0].PreviousOutPoint.Index == 0xffffffff
	if !coinbase {
		script, _ := btctxscript.NewScriptBuilder().AddInt64(height).AddData([]byte("pwa-l1-indexer")).Script()
		mint := btcwire.NewMsgTx(2)
		mint.AddTxIn(btcwire.NewTxIn(&btcwire.OutPoint{Index: 0xffffffff}, script, nil))
		mint.AddTxOut(btcwire.NewTxOut(0, []byte{btctxscript.OP_RETURN}))
		transactions = append([]*btcwire.MsgTx{mint}, transactions...)
	}
	// Preserve witness evidence as well as the txid Merkle tree. No proof of
	// work or spendable mining reward is manufactured by this Indexer fixture.
	for _, tx := range transactions {
		if !tx.HasWitness() {
			continue
		}
		witnessRoot := posPWAL1Merkle(transactions, true)
		var commitment [64]byte
		copy(commitment[:32], witnessRoot[:])
		digest := btcchainhash.DoubleHashB(commitment[:])
		transactions[0].TxIn[0].Witness = btcwire.TxWitness{make([]byte, 32)}
		transactions[0].AddTxOut(btcwire.NewTxOut(0, append([]byte{btctxscript.OP_RETURN, 0x24, 0xaa, 0x21, 0xa9, 0xed}, digest...)))
		break
	}
	header := btcwire.BlockHeader{Version: 4, PrevBlock: previous, MerkleRoot: posPWAL1Merkle(transactions, false),
		Timestamp: time.Unix(1_700_000_000+height, 0), Bits: 0x207fffff}
	block := btcwire.NewMsgBlock(&header)
	for _, tx := range transactions {
		_ = block.AddTransaction(tx)
	}
	return block
}

func (f *posPWAL1Indexer) txStatusLocked(id string) *indexerwire.BitcoinTxStatus {
	status := &indexerwire.BitcoinTxStatus{TxID: id}
	if tx := f.transactions[id]; tx != nil {
		status.Exists, status.InMempool = true, tx.height < 0
		status.Confirmed = tx.height >= 0
		if status.Confirmed {
			status.BlockHeight, status.BlockHash = tx.height, f.blockLocked(tx.height).BlockHash().String()
			status.Confirmations, status.BlockTime = f.height-tx.height+1, 1_700_000_000+tx.height
		}
	}
	return status
}

func (f *posPWAL1Indexer) addressOutputsLocked(address, asset string) []*indexercommon.AssetsInUtxo {
	result := make([]*indexercommon.AssetsInUtxo, 0)
	addr, err := btcutil.DecodeAddress(address, &btcchaincfg.TestNet4Params)
	if err != nil {
		return result
	}
	script, err := btctxscript.PayToAddrScript(addr)
	if err != nil {
		return result
	}
	for point, output := range f.outputs {
		if f.spent[point].txid != "" || output.UtxoId == indexercommon.INVALID_ID || !bytes.Equal(script, output.OutValue.PkScript) {
			continue
		}
		include := asset == ""
		if asset == "::" {
			include = !output.HasAsset()
		}
		for _, item := range output.Assets {
			if item.Name.String() == asset {
				include = true
			}
		}
		if include {
			result = append(result, output.Clone().ToAssetsInUtxo())
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].OutPoint < result[j].OutPoint })
	return result
}

func (f *posPWAL1Indexer) summaryLocked(address string) []*indexercommon.DisplayAsset {
	assets := make(map[indexercommon.AssetName]*indexercommon.AssetInfo)
	plain := int64(0)
	total := int64(0)
	for _, output := range f.addressOutputsLocked(address, "") {
		parsed := output.ToTxOutput()
		total += parsed.Value()
		plain += parsed.GetPlainSat()
		for _, asset := range parsed.Assets {
			if existing := assets[asset.Name]; existing != nil {
				existing.Amount = *existing.Amount.Add(&asset.Amount)
			} else {
				assets[asset.Name] = asset.Clone()
			}
		}
	}
	result := []*indexercommon.DisplayAsset{
		{AssetName: indexercommon.AssetName{Type: "*"}, Amount: strconv.FormatInt(total, 10)},
		{AssetName: indexercommon.ASSET_PLAIN_SAT, Amount: strconv.FormatInt(plain, 10)},
	}
	for _, asset := range assets {
		result = append(result, &indexercommon.DisplayAsset{AssetName: asset.Name, Amount: asset.Amount.String(), Precision: asset.Amount.Precision, BindingSat: int(asset.BindingSat)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].AssetName.String() < result[j].AssetName.String() })
	return result
}

func posPWAL1JSON(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func posPWAL1Error(w http.ResponseWriter, err error) {
	posPWAL1JSON(w, indexerwire.BaseResp{Code: -1, Msg: err.Error()})
}

func posPWAL1Decode(r *http.Request, dst any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(dst)
}

func (f *posPWAL1Indexer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, pubkey, signature, nonce, Authorization")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/testnet/") {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/testnet")
	if strings.HasPrefix(path, "/btc/lucky/") {
		f.mu.Lock()
		height, tipHash := f.height, f.blockLocked(f.height).BlockHash().String()
		f.mu.Unlock()
		if f.lucky.serveHTTP(w, r, path, height, tipHash) {
			return
		}
	}
	ok := indexerwire.BaseResp{Code: 0, Msg: "ok"}
	if path == "/btc/tx/test" || path == "/btc/tx" || path == "/btc/txs" || path == "/v3/bitcoin/tx/broadcast" {
		var raws []string
		var err error
		switch path {
		case "/btc/tx/test":
			var req indexerwire.TestRawTxReq
			err = posPWAL1Decode(r, &req)
			raws = req.SignedTxs
		case "/btc/tx":
			var req indexerwire.SendRawTxReq
			err = posPWAL1Decode(r, &req)
			raws = []string{req.SignedTxHex}
		case "/btc/txs":
			var req indexerwire.SendRawTxsReq
			err = posPWAL1Decode(r, &req)
			raws = req.SignedTxHex
		default:
			var req indexerwire.BitcoinBroadcastReq
			err = posPWAL1Decode(r, &req)
			raws = []string{req.RawTx}
		}
		if err != nil {
			posPWAL1Error(w, err)
			return
		}
		if len(raws) == 0 {
			posPWAL1Error(w, fmt.Errorf("empty transaction package"))
			return
		}
		results, err := f.submit(raws, path != "/btc/tx/test")
		if err != nil {
			posPWAL1Error(w, err)
			return
		}
		ids := make([]string, len(results))
		for i, result := range results {
			ids[i] = result.TxId
		}
		switch path {
		case "/btc/tx/test":
			posPWAL1JSON(w, indexerwire.TestRawTxResp{BaseResp: ok, Data: results})
		case "/btc/tx":
			posPWAL1JSON(w, indexerwire.SendRawTxResp{BaseResp: ok, Data: ids[0]})
		case "/btc/txs":
			posPWAL1JSON(w, indexerwire.SendRawTxsResp{BaseResp: ok, Data: ids})
		default:
			posPWAL1JSON(w, indexerwire.BitcoinBroadcastResp{BaseResp: ok, Data: &indexerwire.BitcoinBroadcastResult{Accepted: true, TxID: ids[0]}})
		}
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.HasPrefix(path, "/mint/permission/PWAMINT/"):
		posPWAL1JSON(w, map[string]any{"code": 0, "data": map[string]string{"amount": "200"}})
	case path == "/kv/register":
		var req indexerwire.RegisterPubKeyReq
		if err := posPWAL1Decode(r, &req); err != nil {
			posPWAL1Error(w, err)
			return
		}
		pub := f.bootstrapPub
		if parent := f.parentByPub[req.PubKey]; parent != "" {
			pub = parent
		}
		posPWAL1JSON(w, indexerwire.RegisterPubKeyResp{BaseResp: ok, PubKey: pub})
	case path == "/v3/indexer/pubkey":
		pub := f.bootstrapPub
		if parent := f.parentByPub[r.Header.Get("pubkey")]; parent != "" {
			pub = parent
		}
		posPWAL1JSON(w, indexerwire.IndexerPubKeyResp{BaseResp: ok, PubKey: pub})
	case path == "/bestheight":
		posPWAL1JSON(w, indexerwire.BestHeightResp{BaseResp: ok, Data: map[string]int{"height": int(f.height)}})
	case path == "/btc/block/bestblockheight":
		posPWAL1JSON(w, indexerwire.BestBlockHeightResp{BaseResp: ok, Data: f.height})
	case path == "/btc/block/bestblockhash":
		posPWAL1JSON(w, indexerwire.BestBlockhashResp{BaseResp: ok, Data: f.blockLocked(f.height).BlockHash().String()})
	case strings.HasPrefix(path, "/btc/block/blockhash/"):
		height, err := strconv.ParseInt(strings.TrimPrefix(path, "/btc/block/blockhash/"), 10, 64)
		if err != nil || height < f.checkpointHeight || height > f.height {
			posPWAL1Error(w, fmt.Errorf("unknown block height"))
			return
		}
		posPWAL1JSON(w, indexerwire.BlockHashResp{BaseResp: ok, Data: f.blockLocked(height).BlockHash().String()})
	case strings.HasPrefix(path, "/btc/block/"):
		_ = f.blockLocked(f.height)
		block := f.blocksByHash[strings.TrimPrefix(path, "/btc/block/")]
		if block == nil {
			posPWAL1Error(w, fmt.Errorf("unknown raw block"))
			return
		}
		var raw bytes.Buffer
		if err := block.Serialize(&raw); err != nil {
			posPWAL1Error(w, err)
			return
		}
		posPWAL1JSON(w, indexerwire.RawBlockResp{BaseResp: ok, Data: hex.EncodeToString(raw.Bytes())})
	case path == "/btc/fee/summary":
		posPWAL1JSON(w, indexerwire.FeeSummaryResp{BaseResp: ok, Data: &indexerwire.FeeSummaryList{List: []*indexerwire.FeeSummary{{Title: "Local L1 fixture", Desc: "Fixed test fee in sat/vB", FeeRate: "1"}}}})
	case strings.HasPrefix(path, "/btc/rawtx/"):
		id := strings.TrimPrefix(path, "/btc/rawtx/")
		if tx := f.transactions[id]; tx != nil {
			posPWAL1JSON(w, indexerwire.TxResp{BaseResp: ok, Data: tx.raw})
		} else {
			posPWAL1Error(w, fmt.Errorf("tx not found: %s", id))
		}
	case strings.HasPrefix(path, "/btc/tx/simpleinfo/"):
		id := strings.TrimPrefix(path, "/btc/tx/simpleinfo/")
		tx := f.transactions[id]
		if tx == nil {
			posPWAL1Error(w, fmt.Errorf("tx not found: %s", id))
			return
		}
		status := f.txStatusLocked(id)
		posPWAL1JSON(w, indexerwire.TxSimpleInfoResp{BaseResp: ok, Data: &indexerwire.TxSimpleInfo{TxID: id, Version: uint32(tx.tx.Version), Confirmations: uint64(status.Confirmations), BlockHeight: tx.height, BlockTime: status.BlockTime}})
	case strings.HasPrefix(path, "/v3/utxo/info/"):
		point := strings.TrimPrefix(path, "/v3/utxo/info/")
		output := f.outputs[point]
		if output == nil || f.spent[point].txid != "" {
			posPWAL1Error(w, fmt.Errorf("utxo not found: %s", point))
			return
		}
		posPWAL1JSON(w, indexerwire.TxOutputRespV3{BaseResp: ok, Data: output.Clone().ToAssetsInUtxo()})
	case path == "/v3/utxos/existing":
		var req indexerwire.UtxosReq
		if err := posPWAL1Decode(r, &req); err != nil {
			posPWAL1Error(w, err)
			return
		}
		points := make([]string, 0, len(req.Utxos))
		for _, point := range req.Utxos {
			if f.outputs[point] != nil && f.spent[point].txid == "" {
				points = append(points, point)
			}
		}
		posPWAL1JSON(w, indexerwire.ExistingUtxoResp{BaseResp: ok, ExistingUtxos: points})
	case strings.HasPrefix(path, "/v3/address/summary/"):
		posPWAL1JSON(w, indexerwire.AssetSummaryRespV3{BaseResp: ok, Data: f.summaryLocked(strings.TrimPrefix(path, "/v3/address/summary/"))})
	case strings.HasPrefix(path, "/v3/address/asset/"):
		parts := strings.SplitN(strings.TrimPrefix(path, "/v3/address/asset/"), "/", 2)
		if len(parts) != 2 {
			posPWAL1Error(w, fmt.Errorf("asset path must contain address and asset"))
			return
		}
		outputs := f.addressOutputsLocked(parts[0], parts[1])
		posPWAL1JSON(w, indexerwire.UtxosWithAssetRespV3{BaseResp: ok, ListResp: indexerwire.ListResp{Total: uint64(len(outputs))}, Data: outputs})
	case strings.HasPrefix(path, "/v3/address/utxos/"):
		outputs := f.addressOutputsLocked(strings.TrimPrefix(path, "/v3/address/utxos/"), "")
		posPWAL1JSON(w, indexerwire.UtxosWithAssetRespV3{BaseResp: ok, ListResp: indexerwire.ListResp{Total: uint64(len(outputs))}, Data: outputs})
	case strings.HasPrefix(path, "/v3/utxos/locked/"):
		// This L1 fixture creates no inscription locks; wallet reservation locks
		// remain owned and enforced by the real SDK.
		posPWAL1JSON(w, indexerwire.TxOutputListRespV3{BaseResp: ok, Data: []*indexercommon.AssetsInUtxo{}})
	case strings.HasPrefix(path, "/allutxos/address/") || strings.HasPrefix(path, "/utxo/address/"):
		address := strings.TrimPrefix(path, "/allutxos/address/")
		if strings.HasPrefix(path, "/utxo/address/") {
			address = strings.Split(strings.TrimPrefix(path, "/utxo/address/"), "/")[0]
		}
		plain, other := []*indexerwire.PlainUtxo{}, []*indexerwire.PlainUtxo{}
		for _, output := range f.addressOutputsLocked(address, "") {
			point, err := btcwire.NewOutPointFromString(output.OutPoint)
			if err != nil {
				posPWAL1Error(w, err)
				return
			}
			height, index, vout := indexercommon.FromUtxoId(output.UtxoId)
			item := &indexerwire.PlainUtxo{Height: height, Index: index, Txid: point.Hash.String(), Vout: vout, Value: output.Value}
			if len(output.Assets) == 0 {
				plain = append(plain, item)
			} else {
				other = append(other, item)
			}
		}
		if strings.HasPrefix(path, "/utxo/address/") {
			posPWAL1JSON(w, indexerwire.PlainUtxosResp{BaseResp: ok, Total: len(plain), Data: plain})
		} else {
			posPWAL1JSON(w, indexerwire.AllUtxosResp{BaseResp: ok, Total: len(plain) + len(other), PlainUtxos: plain, OtherUtxos: other})
		}
	case strings.HasPrefix(path, "/v3/tick/info/"):
		name := strings.TrimPrefix(path, "/v3/tick/info/")
		info := f.tickers[name]
		if info == nil {
			posPWAL1Error(w, fmt.Errorf("ticker not found: %s", name))
			return
		}
		copy := *info
		posPWAL1JSON(w, indexerwire.TickerInfoResp{BaseResp: ok, Data: &copy})
	case strings.HasPrefix(path, "/ns/address/"):
		// No DID is issued in this fixture; account/DKVS application state is
		// supplied only by the real L2 services, never fabricated here.
		posPWAL1JSON(w, indexerwire.NamesWithAddressResp{BaseResp: ok, Data: &indexerwire.NamesWithAddressData{Address: strings.TrimPrefix(path, "/ns/address/"), Names: []*indexerwire.OrdinalsName{}}})
	case strings.HasPrefix(path, "/ns/name/"):
		name := strings.TrimPrefix(path, "/ns/name/")
		f.nodeFixture.namesMu.RLock()
		address, exists := f.nodeFixture.names[name]
		f.nodeFixture.namesMu.RUnlock()
		if !exists {
			posPWAL1JSON(w, indexerwire.NamePropertiesResp{BaseResp: indexerwire.BaseResp{Code: -1, Msg: fmt.Sprintf("can't find name %s", name)}})
			return
		}
		posPWAL1JSON(w, map[string]any{"code": 0, "msg": "ok", "data": map[string]string{"name": name, "address": address}})
	case strings.HasPrefix(path, "/v3/bitcoin/"):
		f.bitcoinEvidenceLocked(w, r, path)
	default:
		f.unknownRequests = append(f.unknownRequests, r.Method+" "+path)
		w.WriteHeader(http.StatusNotFound)
		posPWAL1Error(w, fmt.Errorf("unsupported local L1 Indexer request: %s %s", r.Method, path))
	}
}

func (f *posPWAL1Indexer) bitcoinEvidenceLocked(w http.ResponseWriter, r *http.Request, path string) {
	ok := indexerwire.BaseResp{Code: 0, Msg: "ok"}
	switch path {
	case "/v3/bitcoin/tip":
		posPWAL1JSON(w, indexerwire.BitcoinTipResp{BaseResp: ok, Data: &indexerwire.BitcoinTip{Height: f.height, BlockHash: f.blockLocked(f.height).BlockHash().String()}})
	case "/v3/bitcoin/rawtx/batch", "/v3/bitcoin/tx/status/batch":
		var req indexerwire.BitcoinTxIDsReq
		if err := posPWAL1Decode(r, &req); err != nil {
			posPWAL1Error(w, err)
			return
		}
		if path == "/v3/bitcoin/tx/status/batch" {
			data := make([]*indexerwire.BitcoinTxStatus, 0, len(req.TxIDs))
			for _, id := range req.TxIDs {
				data = append(data, f.txStatusLocked(id))
			}
			posPWAL1JSON(w, indexerwire.BitcoinTxStatusResp{BaseResp: ok, Data: data})
			return
		}
		data := make([]*indexerwire.BitcoinRawTx, 0, len(req.TxIDs))
		for _, id := range req.TxIDs {
			item := &indexerwire.BitcoinRawTx{TxID: id}
			if tx := f.transactions[id]; tx != nil {
				item.RawTx = tx.raw
			} else {
				item.Error = "transaction not found"
			}
			data = append(data, item)
		}
		posPWAL1JSON(w, indexerwire.BitcoinRawTxResp{BaseResp: ok, Data: data})
	case "/v3/bitcoin/outspends/batch", "/v3/bitcoin/utxos/status":
		var req indexerwire.BitcoinOutpointsReq
		if err := posPWAL1Decode(r, &req); err != nil {
			posPWAL1Error(w, err)
			return
		}
		outspends := make([]*indexerwire.BitcoinOutspend, 0, len(req.Outpoints))
		utxos := make([]*indexerwire.BitcoinUTXOStatus, 0, len(req.Outpoints))
		for _, point := range req.Outpoints {
			output, spend := f.outputs[point], f.spent[point]
			outspends = append(outspends, &indexerwire.BitcoinOutspend{Outpoint: point, Exists: output != nil, Spent: spend.txid != "", SpendingTx: spend.txid, Vin: spend.vin})
			item := &indexerwire.BitcoinUTXOStatus{Outpoint: point, Exists: output != nil, Unspent: output != nil && spend.txid == ""}
			if output != nil {
				item.Value, item.PkScript = output.Value(), hex.EncodeToString(output.OutValue.PkScript)
				if output.UtxoId != indexercommon.INVALID_ID {
					h, _, _ := indexercommon.FromUtxoId(output.UtxoId)
					item.Confirmations = f.height - int64(h) + 1
					item.BlockHash = f.blockLocked(int64(h)).BlockHash().String()
				}
			}
			utxos = append(utxos, item)
		}
		if path == "/v3/bitcoin/outspends/batch" {
			posPWAL1JSON(w, indexerwire.BitcoinOutspendsResp{BaseResp: ok, Data: outspends})
		} else {
			posPWAL1JSON(w, indexerwire.BitcoinUTXOStatusResp{BaseResp: ok, Data: utxos})
		}
	case "/v3/bitcoin/utxos/by-scripts":
		var req indexerwire.BitcoinScriptsReq
		if err := posPWAL1Decode(r, &req); err != nil {
			posPWAL1Error(w, err)
			return
		}
		data := make([]*indexerwire.BitcoinScriptUTXOs, 0, len(req.Scripts))
		for _, script := range req.Scripts {
			item := &indexerwire.BitcoinScriptUTXOs{Script: script, UTXOs: []*indexerwire.BitcoinUTXO{}}
			for point, output := range f.outputs {
				if f.spent[point].txid != "" || hex.EncodeToString(output.OutValue.PkScript) != script {
					continue
				}
				confirmations := int64(0)
				if output.UtxoId != indexercommon.INVALID_ID {
					h, _, _ := indexercommon.FromUtxoId(output.UtxoId)
					confirmations = f.height - int64(h) + 1
				}
				item.UTXOs = append(item.UTXOs, &indexerwire.BitcoinUTXO{Outpoint: point, Value: output.Value(), PkScript: script, Confirmations: confirmations})
			}
			sort.Slice(item.UTXOs, func(i, j int) bool { return item.UTXOs[i].Outpoint < item.UTXOs[j].Outpoint })
			data = append(data, item)
		}
		posPWAL1JSON(w, indexerwire.BitcoinUTXOsByScriptsResp{BaseResp: ok, Data: data})
	default:
		f.unknownRequests = append(f.unknownRequests, r.Method+" "+path)
		w.WriteHeader(http.StatusNotFound)
		posPWAL1Error(w, fmt.Errorf("unsupported local Bitcoin evidence request: %s", path))
	}
}

func TestPOSPWAL1IndexerPreservesRealSignedTransactionAndAssetFlow(t *testing.T) {
	f := newPOSPWAL1Indexer(t, "", nil)
	key, _ := btcbtcec.PrivKeyFromBytes(bytes.Repeat([]byte{1}, 32))
	recipientKey, _ := btcbtcec.PrivKeyFromBytes(bytes.Repeat([]byte{2}, 32))
	address, err := btcutil.NewAddressWitnessPubKeyHash(btcutil.Hash160(key.PubKey().SerializeCompressed()), &btcchaincfg.TestNet4Params)
	require.NoError(t, err)
	recipient, err := btcutil.NewAddressWitnessPubKeyHash(btcutil.Hash160(recipientKey.PubKey().SerializeCompressed()), &btcchaincfg.TestNet4Params)
	require.NoError(t, err)
	assetName := indexercommon.NewAssetNameFromString("ordx:f:PWAPOS")
	require.NotNil(t, assetName)
	point := f.FundAddress(t, address.EncodeAddress(), 10_000, []*indexercommon.DisplayAsset{{
		AssetName: *assetName, Amount: "2000", BindingSat: 1,
		Offsets: []*indexercommon.OffsetRange{{Start: 0, End: 2000}},
	}})
	source, exists := f.Output(point)
	require.True(t, exists)
	outpoint, err := btcwire.NewOutPointFromString(point)
	require.NoError(t, err)
	recipientScript, err := btctxscript.PayToAddrScript(recipient)
	require.NoError(t, err)
	tx := btcwire.NewMsgTx(2)
	tx.AddTxIn(btcwire.NewTxIn(outpoint, nil, nil))
	tx.AddTxOut(btcwire.NewTxOut(1500, recipientScript))
	tx.AddTxOut(btcwire.NewTxOut(8300, source.PkScript))
	prev := btctxscript.NewCannedPrevOutputFetcher(source.PkScript, source.Value)
	hashes := btctxscript.NewTxSigHashes(tx, prev)
	tx.TxIn[0].Witness, err = btctxscript.WitnessSignature(tx, hashes, 0, source.Value, source.PkScript, btctxscript.SigHashAll, key, true)
	require.NoError(t, err)
	encode := func(transaction *btcwire.MsgTx) string {
		var raw bytes.Buffer
		require.NoError(t, transaction.Serialize(&raw))
		return hex.EncodeToString(raw.Bytes())
	}
	post := func(path string, request, response any) {
		raw, err := json.Marshal(request)
		require.NoError(t, err)
		resp, err := http.Post(f.NodeFixture().server.URL+"/testnet"+path, "application/json", bytes.NewReader(raw))
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, json.NewDecoder(resp.Body).Decode(response))
	}
	get := func(path string, response any) {
		resp, err := http.Get(f.NodeFixture().server.URL + "/testnet" + path)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, json.NewDecoder(resp.Body).Decode(response))
	}

	// A rejected signature must not consume funding or manufacture a balance.
	invalid := tx.Copy()
	invalid.TxIn[0].Witness[0][10] ^= 1
	var rejected indexerwire.TestRawTxResp
	post("/btc/tx/test", indexerwire.TestRawTxReq{SignedTxs: []string{encode(invalid)}}, &rejected)
	require.Zero(t, rejected.Code)
	require.Len(t, rejected.Data, 1)
	require.False(t, rejected.Data[0].Allowed)
	require.Empty(t, f.Snapshot().PendingTxIDs)

	var checked indexerwire.TestRawTxResp
	post("/btc/tx/test", indexerwire.TestRawTxReq{SignedTxs: []string{encode(tx)}}, &checked)
	require.Zero(t, checked.Code)
	require.Len(t, checked.Data, 1)
	require.True(t, checked.Data[0].Allowed, checked.Data[0].RejectReason)
	require.Equal(t, tx.TxID(), checked.Data[0].TxId)
	require.Empty(t, f.Snapshot().PendingTxIDs, "preflight must not broadcast")
	var sent indexerwire.SendRawTxsResp
	post("/btc/txs", indexerwire.SendRawTxsReq{SignedTxHex: []string{encode(tx)}}, &sent)
	require.Zero(t, sent.Code, sent.Msg)
	require.Equal(t, []string{tx.TxID()}, sent.Data)
	require.Equal(t, []string{tx.TxID()}, f.Snapshot().PendingTxIDs)
	stored, exists := f.RawTx(tx.TxID())
	require.True(t, exists)
	require.Equal(t, encode(tx), encode(stored))

	first, exists := f.Output(tx.TxID() + ":0")
	require.True(t, exists)
	require.Equal(t, uint64(indexercommon.INVALID_ID), first.UtxoId)
	require.Len(t, first.Assets, 1)
	require.Equal(t, "1500", first.Assets[0].Amount)
	change, exists := f.Output(tx.TxID() + ":1")
	require.True(t, exists)
	require.Len(t, change.Assets, 1)
	require.Equal(t, "500", change.Assets[0].Amount)
	require.Equal(t, []*indexercommon.OffsetRange{{Start: 0, End: 500}}, change.Assets[0].Offsets)
	var previous indexerwire.BestBlockhashResp
	get("/btc/block/bestblockhash", &previous)
	require.Zero(t, previous.Code)
	require.Equal(t, []string{tx.TxID()}, f.ConfirmPending())
	first, exists = f.Output(tx.TxID() + ":0")
	require.True(t, exists)
	require.NotEqual(t, uint64(indexercommon.INVALID_ID), first.UtxoId)
	var tip indexerwire.BestBlockhashResp
	get("/btc/block/bestblockhash", &tip)
	require.Zero(t, tip.Code)
	var rawBlock indexerwire.RawBlockResp
	get("/btc/block/"+tip.Data, &rawBlock)
	require.Zero(t, rawBlock.Code, rawBlock.Msg)
	blockBytes, err := hex.DecodeString(rawBlock.Data)
	require.NoError(t, err)
	var block btcwire.MsgBlock
	require.NoError(t, block.Deserialize(bytes.NewReader(blockBytes)))
	require.Equal(t, tip.Data, block.BlockHash().String())
	require.Equal(t, previous.Data, block.Header.PrevBlock.String())
	require.Len(t, block.Transactions, 2)
	require.Equal(t, encode(tx), encode(block.Transactions[1]), "block monitor must receive the original signed bytes")
	coinbaseID, transactionID := block.Transactions[0].TxHash(), tx.TxHash()
	root := btcchainhash.DoubleHashH(append(coinbaseID[:], transactionID[:]...))
	require.Equal(t, root, block.Header.MerkleRoot)
	height, index, vout := indexercommon.FromUtxoId(first.UtxoId)
	require.Equal(t, int(f.Snapshot().Height), height)
	require.Equal(t, 1, index)
	require.Equal(t, 0, vout)
	var statuses indexerwire.BitcoinTxStatusResp
	post("/v3/bitcoin/tx/status/batch", indexerwire.BitcoinTxIDsReq{TxIDs: []string{tx.TxID()}}, &statuses)
	require.Zero(t, statuses.Code)
	require.Len(t, statuses.Data, 1)
	require.Equal(t, tip.Data, statuses.Data[0].BlockHash)

	// Peers may repeat the same package. Keep one tx and one credit, while
	// retaining attempt counts so browser tests can diagnose duplicate sends.
	post("/btc/txs", indexerwire.SendRawTxsReq{SignedTxHex: []string{encode(tx)}}, &sent)
	require.Zero(t, sent.Code, sent.Msg)
	require.Empty(t, f.Snapshot().PendingTxIDs)
	require.Equal(t, 2, f.Snapshot().BroadcastCount[tx.TxID()])
	f.mu.Lock()
	credited := f.addressOutputsLocked(recipient.EncodeAddress(), assetName.String())
	f.mu.Unlock()
	require.Len(t, credited, 1)
	require.Equal(t, "1500", credited[0].Assets[0].Amount)
}
