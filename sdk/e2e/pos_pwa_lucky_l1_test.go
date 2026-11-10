package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	btcblockchain "github.com/btcsuite/btcd/blockchain"
	btcbtcjson "github.com/btcsuite/btcd/btcjson"
	btcutil "github.com/btcsuite/btcd/btcutil"
	btcchaincfg "github.com/btcsuite/btcd/chaincfg"
	btcchainhash "github.com/btcsuite/btcd/chaincfg/chainhash"
	btctxscript "github.com/btcsuite/btcd/txscript"
	btcwire "github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/indexer/share/btclucky"
)

// The fake L1 Indexer supplies a valid Testnet4-difficulty Bitcoin template.
// The production WASM miner must do the hashing. Validate any submitted block
// independently; this fixture does not expect a Testnet4 block hit.
const posPWALuckyTestnet4Bits = uint32(0x1d00ffff)

type posPWALuckyL1 struct {
	mu            sync.Mutex
	sequence      uint64
	template      *btclucky.BlockTemplate
	templateBytes []byte
	etag          string
	found         []btclucky.FoundBlockRecord
	submitted     map[string]struct{}
}

func newPOSPWALuckyL1() *posPWALuckyL1 {
	return &posPWALuckyL1{found: []btclucky.FoundBlockRecord{}, submitted: make(map[string]struct{})}
}

func (f *posPWALuckyL1) serveHTTP(w http.ResponseWriter, r *http.Request, path string, height int64, tipHash string) bool {
	if path != "/btc/lucky/template" && path != "/v3/bitcoin/block/submit" && path != "/btc/lucky/info" {
		return false
	}
	if path == "/btc/lucky/info" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		posPWAL1JSON(w, btclucky.APIResponse[map[string]any]{Code: 0, Msg: "ok", Data: map[string]any{
			"foundBlocks":   append([]btclucky.FoundBlockRecord{}, f.found...),
			"templateCount": f.sequence, "evidence": "independently verified proof of work on fake L1",
		}})
		return true
	}
	if path == "/btc/lucky/template" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return true
		}
		f.mu.Lock()
		f.refreshTemplateLocked(height, tipHash)
		body, etag := append([]byte(nil), f.templateBytes...), f.etag
		f.mu.Unlock()
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
		return true
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return true
	}
	id, raw, rpcErr := decodeTestSubmitBlock(r)
	if rpcErr != nil {
		writeTestJSONRPC(w, id, nil, rpcErr)
		return true
	}
	if contentType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); contentType != "application/json" {
		writeTestJSONRPC(w, id, nil, &testJSONRPCError{Code: -32600, Message: "submitblock requires application/json"})
		return true
	}
	result, rpcErr := f.submitBlock(raw)
	if rpcErr != nil {
		writeTestJSONRPC(w, id, nil, rpcErr)
		return true
	}
	var value any
	if result != "" {
		value = result
	}
	writeTestJSONRPC(w, id, value, nil)
	return true
}

func (f *posPWALuckyL1) refreshTemplateLocked(height int64, tipHash string) {
	if f.template != nil && f.template.Height == height+1 && f.template.PreviousHash == tipHash {
		return
	}
	f.sequence++
	now := time.Now().Unix()
	subsidy := btcblockchain.CalcBlockSubsidy(int32(height+1), &btcchaincfg.TestNet4Params)
	template := &btclucky.BlockTemplate{
		GetBlockTemplateResult: btcbtcjson.GetBlockTemplateResult{
			Bits: fmt.Sprintf("%08x", posPWALuckyTestnet4Bits), CurTime: now, Height: height + 1,
			PreviousHash: tipHash, SigOpLimit: 80000, SizeLimit: 1000000,
			WeightLimit: 4000000, Transactions: []btcbtcjson.GetBlockTemplateResultTx{},
			Version: 0x20000000, CoinbaseAux: &btcbtcjson.GetBlockTemplateResultAux{Flags: fmt.Sprintf("%x", []byte("pwa-l1-test"))},
			CoinbaseValue: &subsidy, WorkID: fmt.Sprintf("pwa-l1-%d", f.sequence),
			Target:  fmt.Sprintf("%064x", btcblockchain.CompactToBig(posPWALuckyTestnet4Bits)),
			MaxTime: now + 2*60*60, MinTime: now, Mutable: []string{"time"},
			NonceRange: "00000000ffffffff", Capabilities: []string{"proposal"},
		},
		Network: "testnet4", MerkleBranch: []string{},
	}
	body, _ := json.Marshal(btclucky.APIResponse[*btclucky.BlockTemplate]{Code: 0, Msg: "ok", Data: template})
	hash := sha256.Sum256(body)
	f.template = template
	f.templateBytes = body
	f.etag = fmt.Sprintf("\"%x\"", hash)
	template.ID = fmt.Sprintf("%x", hash)
}

func (f *posPWALuckyL1) submitBlock(raw []byte) (string, *testJSONRPCError) {
	var block btcwire.MsgBlock
	reader := bytes.NewReader(raw)
	if err := block.Deserialize(reader); err != nil {
		return "", &testJSONRPCError{Code: -22, Message: err.Error()}
	}
	if reader.Len() != 0 {
		return "", &testJSONRPCError{Code: -22, Message: "submitted block has trailing bytes"}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.template == nil {
		return "no Bitcoin block template was requested", nil
	}
	template := f.template
	if block.Header.Version != template.Version || block.Header.PrevBlock.String() != template.PreviousHash || block.Header.Bits != posPWALuckyTestnet4Bits {
		return "submitted block does not match the active Bitcoin template", nil
	}
	if block.Header.Timestamp.Unix() < template.MinTime || block.Header.Timestamp.Unix() > template.MaxTime {
		return "submitted block time is outside the template bounds", nil
	}
	if len(block.Transactions) != 1 {
		return "submitted block does not contain the template transactions", nil
	}
	coinbase := block.Transactions[0]
	if len(coinbase.TxIn) != 1 || coinbase.TxIn[0].PreviousOutPoint.Index != ^uint32(0) ||
		coinbase.TxIn[0].PreviousOutPoint.Hash != (btcchainhash.Hash{}) || len(coinbase.TxOut) != 1 {
		return "submitted block does not contain one valid coinbase", nil
	}
	if coinbase.TxHash() != block.Header.MerkleRoot {
		return "submitted block merkle root does not match its coinbase", nil
	}
	height, err := btcblockchain.ExtractCoinbaseHeight(btcutil.NewTx(coinbase))
	if err != nil || int64(height) != template.Height {
		return "submitted coinbase height does not match the template", nil
	}
	subsidy := btcblockchain.CalcBlockSubsidy(int32(template.Height), &btcchaincfg.TestNet4Params)
	if coinbase.TxOut[0].Value != subsidy {
		return "submitted coinbase reward does not match the template subsidy", nil
	}
	_, addresses, _, err := btctxscript.ExtractPkScriptAddrs(coinbase.TxOut[0].PkScript, &btcchaincfg.TestNet4Params)
	if err != nil || len(addresses) != 1 {
		return "submitted coinbase has no single testnet reward address", nil
	}
	hash := block.Header.BlockHash()
	if btcblockchain.HashToBig(&hash).Cmp(btcblockchain.CompactToBig(block.Header.Bits)) > 0 {
		return "submitted block does not prove the claimed work", nil
	}
	if _, duplicate := f.submitted[hash.String()]; duplicate {
		return "duplicate", nil
	}
	record := btclucky.FoundBlockRecord{
		BlockHash: hash.String(), BlockHeight: template.Height, CoinbaseTxID: coinbase.TxHash().String(), Vout: 0,
		Amount: coinbase.TxOut[0].Value, RewardAddress: addresses[0].EncodeAddress(),
		Submitted: true, SubmitResult: "pwa-fake-l1-pow-verified", CreatedAt: time.Now(),
	}
	f.found = append(f.found, record)
	f.submitted[hash.String()] = struct{}{}
	if len(f.found) > 32 {
		f.found = f.found[len(f.found)-32:]
	}
	return "", nil
}
