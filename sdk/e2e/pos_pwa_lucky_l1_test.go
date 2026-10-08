package e2e

import (
	"encoding/binary"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	btcblockchain "github.com/btcsuite/btcd/blockchain"
	btcutil "github.com/btcsuite/btcd/btcutil"
	btcchaincfg "github.com/btcsuite/btcd/chaincfg"
	btcchainhash "github.com/btcsuite/btcd/chaincfg/chainhash"
	btctxscript "github.com/btcsuite/btcd/txscript"
	btcwire "github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/indexer/share/btclucky"
)

// The fake L1 Indexer supplies low-difficulty test jobs. The production WASM
// miner must do the hashing and submit a genuine solution. Validate every
// submitted header independently; no success response or wallet status is
// injected into the browser. This does not assert Bitcoin network rewards.
type posPWALuckyL1 struct {
	mu sync.Mutex
	sequence uint64
	jobs map[string]*posPWALuckyJob
	found []btclucky.FoundBlockRecord
}

type posPWALuckyJob struct {
	job btclucky.CompactMiningJob
	coinbase *btcwire.MsgTx
	submitted bool
}

func newPOSPWALuckyL1() *posPWALuckyL1 {
	return &posPWALuckyL1{jobs: make(map[string]*posPWALuckyJob), found: []btclucky.FoundBlockRecord{}}
}

func (f *posPWALuckyL1) serveHTTP(w http.ResponseWriter, r *http.Request, path string, height int64, tipHash string) bool {
	if path != "/btc/lucky/job" && path != "/btc/lucky/submit" && path != "/btc/lucky/info" { return false }
	if path == "/btc/lucky/info" {
		if r.Method != http.MethodGet { w.WriteHeader(http.StatusMethodNotAllowed); return true }
		f.mu.Lock()
		defer f.mu.Unlock()
		posPWAL1JSON(w, btclucky.APIResponse[map[string]any]{Code: 0, Msg: "ok", Data: map[string]any{
			"foundBlocks": append([]btclucky.FoundBlockRecord{}, f.found...),
			"jobCount": f.sequence, "evidence": "independently verified proof of work on fake L1",
		}})
		return true
	}
	if r.Method != http.MethodPost { w.WriteHeader(http.StatusMethodNotAllowed); return true }
	if path == "/btc/lucky/job" {
		var req btclucky.JobRequest
		if err := posPWAL1Decode(r, &req); err != nil { posPWAL1Error(w, err); return true }
		job, err := f.newJob(req, height, tipHash)
		if err != nil { posPWAL1Error(w, err); return true }
		posPWAL1JSON(w, btclucky.APIResponse[*btclucky.CompactMiningJob]{Code: 0, Msg: "ok", Data: job})
		return true
	}
	var solution btclucky.MiningSolution
	if err := posPWAL1Decode(r, &solution); err != nil { posPWAL1Error(w, err); return true }
	record, err := f.submit(&solution)
	if err != nil { posPWAL1Error(w, err); return true }
	posPWAL1JSON(w, btclucky.APIResponse[*btclucky.FoundBlockRecord]{Code: 0, Msg: "ok", Data: record})
	return true
}

func (f *posPWALuckyL1) newJob(req btclucky.JobRequest, height int64, tipHash string) (*btclucky.CompactMiningJob, error) {
	if req.Network != "testnet4" || req.Jobs != 1 || len(req.MinerID) != 66 {
		return nil, fmt.Errorf("PWA acceptance requires testnet4 and one real worker")
	}
	address, err := btcutil.DecodeAddress(req.RewardAddress, &btcchaincfg.TestNet4Params)
	if err != nil { return nil, err }
	if !address.IsForNet(&btcchaincfg.TestNet4Params) { return nil, fmt.Errorf("non-testnet reward address") }
	script, err := btctxscript.PayToAddrScript(address)
	if err != nil { return nil, err }
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sequence++
	extra := make([]byte, 8)
	binary.LittleEndian.PutUint64(extra, f.sequence)
	coinbaseScript, err := btctxscript.NewScriptBuilder().AddInt64(height+1).AddData([]byte("pwa-l1-test")).AddData(extra).Script()
	if err != nil { return nil, err }
	coinbase := btcwire.NewMsgTx(1)
	coinbase.AddTxIn(btcwire.NewTxIn(&btcwire.OutPoint{Index: ^uint32(0)}, coinbaseScript, nil))
	// Keep ordinary Bitcoin subsidy semantics even though accepted PoW is
	// evidence only and never inserted into the fixture's spendable UTXO set.
	subsidy := btcblockchain.CalcBlockSubsidy(int32(height+1), &btcchaincfg.TestNet4Params)
	coinbase.AddTxOut(btcwire.NewTxOut(subsidy, script))
	id := fmt.Sprintf("pwa-l1-%d", f.sequence)
	now := time.Now().Unix()
	job := btclucky.CompactMiningJob{
		JobID: id, TemplateID: id, Network: "testnet4", Height: height+1,
		PreviousBlockHash: tipHash, Version: 0x20000000,
		Bits: "207fffff", Target: fmt.Sprintf("%064x", btcblockchain.CompactToBig(0x207fffff)),
		CurTime: now, MinTime: now, RewardAddress: req.RewardAddress, MinerID: req.MinerID,
		WorkerRanges: []btclucky.WorkerRange{{WorkerID: 0, ExtraNonceStart: f.sequence, ExtraNonceEnd: f.sequence, MerkleRoot: coinbase.TxHash().String()}},
	}
	f.jobs[id] = &posPWALuckyJob{job: job, coinbase: coinbase}
	// One active browser worker only needs its current job; retain a bounded
	// history to reject duplicate/unknown submissions and diagnose failures.
	if f.sequence > 64 { delete(f.jobs, fmt.Sprintf("pwa-l1-%d", f.sequence-64)) }
	return &job, nil
}

func (f *posPWALuckyL1) submit(solution *btclucky.MiningSolution) (*btclucky.FoundBlockRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := f.jobs[solution.JobID]
	if entry == nil || entry.submitted { return nil, fmt.Errorf("unknown or already submitted mining job") }
	job := entry.job
	worker := job.WorkerRanges[0]
	if solution.TemplateID != job.TemplateID || solution.Network != job.Network || solution.RewardAddress != job.RewardAddress ||
		solution.WorkerID != worker.WorkerID || solution.ExtraNonce != worker.ExtraNonceStart || solution.NTime != job.CurTime {
		return nil, fmt.Errorf("mining solution changed its assigned job, reward, or worker")
	}
	previous, err := btcchainhash.NewHashFromStr(job.PreviousBlockHash)
	if err != nil { return nil, err }
	bits, err := strconv.ParseUint(job.Bits, 16, 32)
	if err != nil { return nil, err }
	header := btcwire.BlockHeader{Version: job.Version, PrevBlock: *previous, MerkleRoot: entry.coinbase.TxHash(),
		Timestamp: time.Unix(job.CurTime, 0), Bits: uint32(bits), Nonce: solution.Nonce}
	hash := header.BlockHash()
	if hash.String() != solution.HeaderHash || btcblockchain.HashToBig(&hash).Cmp(btcblockchain.CompactToBig(header.Bits)) > 0 {
		return nil, fmt.Errorf("submitted header does not prove the claimed work")
	}
	record := btclucky.FoundBlockRecord{
		BlockHash: hash.String(), BlockHeight: job.Height, CoinbaseTxID: entry.coinbase.TxHash().String(), Vout: 0,
		Amount: entry.coinbase.TxOut[0].Value, RewardAddress: job.RewardAddress, JobID: job.JobID, TemplateID: job.TemplateID,
		Submitted: true, SubmitResult: "pwa-fake-l1-pow-verified", CreatedAt: time.Now(),
	}
	entry.submitted = true
	f.found = append(f.found, record)
	if len(f.found) > 32 { f.found = f.found[len(f.found)-32:] }
	return &record, nil
}
