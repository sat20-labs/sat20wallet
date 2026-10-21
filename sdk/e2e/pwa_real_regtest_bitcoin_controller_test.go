package e2e

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcutil"
	satschaincfg "github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	sindexercommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Only observes the existing Core chain and mines it on explicit confirmation.
// Wallet, transaction, UTXO, balance and indexer responses are never supplied
// by this controller. HTTP broadcast counts are observed in the browser runner.
func startSDKPWARegtestBitcoinController(t *testing.T, address string, mine func(string, int), nodes ...*realSatoshiNet) string {
	return startSDKPWARegtestBitcoinControllerInternal(t, address, mine, nil, nil, nodes...)
}

func startSDKPWARegtestBitcoinControllerWithActivation(t *testing.T, address string, mine func(string, int),
	activate func(context.Context) (map[string]any, error), nodes ...*realSatoshiNet) string {
	t.Helper()
	require.NotNil(t, activate)
	require.Len(t, nodes, 1, "POS activation requires the existing two-node real SatoshiNet")
	return startSDKPWARegtestBitcoinControllerInternal(t, address, mine, activate, nil, nodes...)
}

func startSDKPWARegtestPOSController(t *testing.T, address string, mine func(string, int),
	pos *sdkPWARegtestPOSController, network *realSatoshiNet) string {
	t.Helper()
	require.NotNil(t, pos)
	require.NotNil(t, network)
	return startSDKPWARegtestBitcoinControllerInternal(t, address, mine, nil, pos, network)
}

func startSDKPWARegtestBitcoinControllerInternal(t *testing.T, address string, mine func(string, int),
	activate func(context.Context) (map[string]any, error), pos *sdkPWARegtestPOSController, nodes ...*realSatoshiNet) string {
	t.Helper()
	var cursor int
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getblockcount"), &cursor))
	confirmed := make(map[string]bool)
	var mu sync.Mutex
	var anchorStart int32
	activationUsed := false
	if len(nodes) == 1 && pos == nil {
		_, height, err := nodes[0].Bootstrap.Client.GetBestBlock()
		require.NoError(t, err)
		anchorStart = height
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/transaction" && len(nodes) == 1 {
			var request struct {
				TxID string `json:"txid"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&request); err != nil {
				http.Error(w, "invalid transaction request", http.StatusBadRequest)
				return
			}
			hash, err := chainhash.NewHashFromStr(request.TxID)
			if err != nil || len(request.TxID) != 64 {
				http.Error(w, "invalid txid", http.StatusBadRequest)
				return
			}
			transaction, err := nodes[0].Bootstrap.Client.GetRawTransactionVerbose(hash)
			if err != nil {
				http.Error(w, "transaction not observed yet", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(transaction); err != nil {
				t.Error(err)
			}
			return
		}
		activateRequest := r.Method == http.MethodPost && r.URL.Path == "/activate" && activate != nil && len(nodes) == 1
		posRequest := r.Method == http.MethodPost && pos != nil && (r.URL.Path == "/rotation" ||
			r.URL.Path == "/miner-offline" || r.URL.Path == "/restore-miner" || r.URL.Path == "/restart-core")
		if r.Method != http.MethodPost || (r.URL.Path != "/snapshot" && r.URL.Path != "/confirm-l1" && !activateRequest && !posRequest) {
			http.Error(w, "unsupported real regtest control", http.StatusNotFound)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		var activationEvidence map[string]any
		if activateRequest {
			if activationUsed {
				http.Error(w, "POS activation action may run only once", http.StatusConflict)
				return
			}
			activationUsed = true
			var err error
			activationEvidence, err = activate(r.Context())
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
		}
		var posEvidence map[string]any
		if posRequest {
			var err error
			posEvidence, err = pos.handle(r.Context(), r.URL.Path)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
		}
		if pos != nil {
			if err := pos.sync(); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadGateway)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
		}
		var anchorTargets []string
		waitAnchors := false
		if r.URL.Path == "/confirm-l1" {
			if len(nodes) == 1 {
				var request struct {
					WaitAnchors *bool `json:"wait_anchors"`
				}
				if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&request); err != nil {
					http.Error(w, "invalid confirmation request", http.StatusBadRequest)
					return
				}
				waitAnchors = request.WaitAnchors == nil || *request.WaitAnchors
				require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getrawmempool"), &anchorTargets))
			}
			mine(address, 1)
		}
		var tip int
		require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getblockcount"), &tip))
		require.GreaterOrEqual(t, tip, cursor, "preserve the original regtest chain")
		for height := cursor + 1; height <= tip; height++ {
			hash := strings.TrimSpace(string(realRegtestBitcoinCLI(t, "getblockhash", strconv.Itoa(height))))
			var block struct {
				Tx []string `json:"tx"`
			}
			require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getblock", hash, "1"), &block))
			require.NotEmpty(t, block.Tx)
			for _, txid := range block.Tx[1:] {
				confirmed[txid] = true
			}
		}
		cursor = tip
		pending := []string{}
		require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getrawmempool"), &pending))
		settled := []string{}
		for txid := range confirmed {
			settled = append(settled, txid)
		}
		result := map[string]any{"l1": map[string]any{
			"height": tip, "pending_txids": pending, "confirmed_txids": settled,
		}}
		if activationEvidence != nil {
			result["pos_activation"] = activationEvidence
		}
		if posEvidence != nil {
			result["pos_action"] = posEvidence
		}
		if len(nodes) == 1 {
			ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
			defer cancel()
			for {
				if pos != nil {
					if err := pos.sync(); err != nil {
						http.Error(w, err.Error(), http.StatusBadGateway)
						return
					}
				}
				snapshot, err := readSDKPWARegtestSatoshiNetSnapshot(nodes[0], anchorStart)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadGateway)
					return
				}
				ready := true
				for _, state := range snapshot.Nodes {
					ready = ready && state.Height == snapshot.Height && state.Hash == snapshot.Hash && state.APIHeight == snapshot.Height
				}
				if waitAnchors {
					for _, target := range anchorTargets {
						found := false
						for _, anchor := range snapshot.Anchors {
							if !strings.HasPrefix(anchor.FundingOutpoint, target+":") {
								continue
							}
							found = true
							for _, node := range nodes[0].Nodes {
								if node.Client == nil {
									continue
								}
								base, err := node.IndexerURL("regtest")
								if err != nil {
									http.Error(w, err.Error(), http.StatusBadGateway)
									return
								}
								var indexed struct {
									Code int                        `json:"code"`
									Data *sindexercommon.AscendData `json:"data"`
								}
								readErr := posPWAReadJSON(base+"/v3/ascend/"+anchor.FundingOutpoint, &indexed)
								ready = ready && readErr == nil && indexed.Code == 0 && indexed.Data != nil && indexed.Data.AnchorTxId == anchor.TxID && indexed.Data.Height == int(anchor.Height)
							}
						}
						ready = ready && found
					}
				}
				if ready {
					result["height"], result["hash"], result["nodes"] = snapshot.Height, snapshot.Hash, snapshot.Nodes
					result["anchors"], result["mempool_txids"] = snapshot.Anchors, snapshot.MempoolTxIDs
					if pos != nil {
						result["pos_blocks"] = pos.proofs
					}
					break
				}
				select {
				case <-ctx.Done():
					http.Error(w, "actual nodes/confirmed Anchors did not become consistent", http.StatusGatewayTimeout)
					return
				case <-time.After(200 * time.Millisecond):
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Error(fmt.Errorf("real regtest control response: %w", err))
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func activateSDKPWARegtestPOSV2(t *testing.T, ctx context.Context, network *realSatoshiNet,
	ordinaryA *wallet.Manager, activationHeight int32) (map[string]any, error) {
	t.Helper()
	require.NotNil(t, network)
	require.NotNil(t, ordinaryA)
	require.Equal(t, activationHeight, satschaincfg.RegressionNetParams.POSV2Height)
	require.Equal(t, activationHeight, satschaincfg.TestNetParams.POSV2Height)
	startHash, startHeight, err := network.Bootstrap.Client.GetBestBlock()
	require.NoError(t, err)
	require.Less(t, startHeight, activationHeight-1, "activation driver must begin below H-1")
	for _, node := range network.Nodes {
		hash, height, err := node.Client.GetBestBlock()
		require.NoError(t, err)
		require.Equal(t, startHeight, height)
		require.Equal(t, *startHash, *hash)
		mining, err := node.Client.GetGenerate()
		require.NoError(t, err)
		require.True(t, mining, "%s transaction-gated miner must already be scheduled", node.role)
	}
	assertDrained := func() (int, error) {
		var l1Pending []string
		if err := json.Unmarshal(realRegtestBitcoinCLI(t, "getrawmempool"), &l1Pending); err != nil {
			return 0, err
		}
		var snetPendingCount int
		for _, node := range network.Nodes {
			snetPending, err := node.Client.GetRawMempool()
			if err != nil {
				return 0, err
			}
			if len(snetPending) != 0 {
				return 0, fmt.Errorf("activation requires drained mempools: L1=%d %s SatoshiNet=%d", len(l1Pending), node.role, len(snetPending))
			}
			snetPendingCount += len(snetPending)
		}
		if len(l1Pending) != 0 {
			return 0, fmt.Errorf("activation requires drained L1 mempool: %d pending", len(l1Pending))
		}
		return snetPendingCount, nil
	}
	if _, err := assertDrained(); err != nil {
		return nil, err
	}
	balance := ordinaryA.GetAssetBalance_SatsNet("", wallet.ParseAssetString(wallet.PLAIN_ASSET.String()))
	if balance == nil || balance.Sign() <= 0 {
		return nil, fmt.Errorf("ordinary-1 has no existing spendable SatoshiNet plain balance for real activation transactions")
	}
	steps := make([]map[string]any, 0, int(activationHeight+1-startHeight))
	var hMinus1, atH, hPlus1 map[string]any
	var restartEvidence map[string]any
	for expectedHeight := startHeight + 1; expectedHeight <= activationHeight+1; expectedHeight++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		baselineHash, baselineHeight, err := network.Bootstrap.Client.GetBestBlock()
		require.NoError(t, err)
		require.Equal(t, expectedHeight-1, baselineHeight,
			"only one real SDK transaction may advance each scheduled activation height")
		baselineBlock := blockAtHeight(t, network.Bootstrap, baselineHeight)
		submittedAt := time.Now()
		tx, err := ordinaryA.SendAssets_SatsNet(ordinaryA.GetWallet().GetAddress(), wallet.PLAIN_ASSET.String(), "10000", nil)
		require.NoError(t, err, "ordinary-1 real SDK self-send for POS activation height %d", expectedHeight)
		require.NotNil(t, tx)
		txID := tx.TxID()
		requireMempoolTx(t, network.Bootstrap, txID)
		blockHash, confirmedHeight, submitToConfirm, blockInterval := waitForScheduledSatsNetTx(t,
			network, txID, submittedAt, baselineHeight, baselineHash, baselineBlock.Header.Timestamp)
		require.Equal(t, expectedHeight, confirmedHeight, "one transaction must confirm at its exact scheduled height")
		if _, err := assertDrained(); err != nil {
			return nil, err
		}
		activeForBlock := satschaincfg.RegressionNetParams.POSV2Active(confirmedHeight)
		sdkAnchorOutputsActive, err := ordinaryA.AnchorOutputsActive()
		require.NoError(t, err, "native SDK must read the same indexed activation schedule")
		step := map[string]any{
			"txid": txID, "height": confirmedHeight, "hash": blockHash,
			"pos_v2_active": activeForBlock, "sdk_anchor_outputs_active": sdkAnchorOutputsActive,
			"l1_pending": 0, "snet_mempool": 0,
			"submit_to_confirm": submitToConfirm.String(), "block_timestamp_interval": blockInterval.String(),
		}
		steps = append(steps, step)
		switch confirmedHeight {
		case activationHeight - 1:
			require.False(t, activeForBlock, "H-1 must retain the legacy block format")
			require.True(t, sdkAnchorOutputsActive, "at H-1 the SDK must select the H format for its next invoice")
			hMinus1 = step
			beforeHash, beforeHeight, err := network.Core.Client.GetBestBlock()
			require.NoError(t, err)
			require.Equal(t, activationHeight-1, beforeHeight)
			require.Equal(t, blockHash, beforeHash.String())
			restartCtx, cancel := context.WithTimeout(ctx, rpcReadyTimeout)
			defer cancel()
			if err := posPWAStopNode(restartCtx, network.Core); err != nil {
				return nil, err
			}
			if err := posPWARestartNode(restartCtx, network.Core); err != nil {
				return nil, err
			}
			afterHash, afterHeight, err := network.Core.Client.GetBestBlock()
			require.NoError(t, err, "read immediate same-DB tip before peer reconnection")
			require.Equal(t, beforeHeight, afterHeight)
			require.Equal(t, *beforeHash, *afterHash)
			require.NoError(t, connectNode(network.Core, network.Bootstrap))
			require.NoError(t, joinBlocks(network.Nodes))
			waitSDKPWARegtestIndexes(t, network)
			for _, node := range network.Nodes {
				waitSDKPWARegtestManagerHealth(t, node)
				require.Eventually(t, func() bool {
					mining, readErr := node.Client.GetGenerate()
					return readErr == nil && mining
				}, rpcReadyTimeout, 200*time.Millisecond, "%s scheduled miner did not resume after normal restart", node.role)
			}
			restartEvidence = map[string]any{
				"before_height": beforeHeight, "before_hash": beforeHash.String(),
				"after_height": afterHeight, "after_hash": afterHash.String(), "generate": true,
			}
		case activationHeight:
			require.True(t, activeForBlock, "H must activate the POS v2 block format")
			require.True(t, sdkAnchorOutputsActive)
			atH = step
		case activationHeight + 1:
			require.True(t, activeForBlock, "H+1 must remain on the POS v2 block format")
			require.True(t, sdkAnchorOutputsActive)
			hPlus1 = step
		}
	}
	require.NotNil(t, hMinus1)
	require.NotNil(t, atH)
	require.NotNil(t, hPlus1)
	require.NotNil(t, restartEvidence)
	return map[string]any{
		"start_height": startHeight, "start_hash": startHash.String(), "activation_height": activationHeight,
		"steps": steps, "h_minus_1": hMinus1, "restart": restartEvidence, "h": atH, "h_plus_1": hPlus1,
	}, nil
}

func readSDKPWARegtestSatoshiNetSnapshot(network *realSatoshiNet, after int32) (*posPWASnapshot, error) {
	hash, height, err := network.Bootstrap.Client.GetBestBlock()
	if err != nil {
		return nil, err
	}
	result := &posPWASnapshot{Height: height, Hash: hash.String(), Nodes: []posPWANodeState{}, Anchors: []posPWAAnchorEvidence{}, MempoolTxIDs: []string{}}
	for _, node := range network.Nodes {
		if node.Client == nil {
			continue
		}
		best, tip, err := node.Client.GetBestBlock()
		if err != nil {
			return nil, err
		}
		base, err := node.IndexerURL("regtest")
		if err != nil {
			return nil, err
		}
		var indexed struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				Height int32 `json:"height"`
			} `json:"data"`
		}
		if err := posPWAReadJSON(base+"/bestheight", &indexed); err != nil {
			return nil, err
		}
		if indexed.Code != 0 {
			return nil, fmt.Errorf("%s actual indexer: %s", node.role, indexed.Msg)
		}
		result.Nodes = append(result.Nodes, posPWANodeState{Role: node.role, Height: tip, Hash: best.String(), APIHeight: indexed.Data.Height})
	}
	for h := after + 1; h <= height; h++ {
		blockHash, err := network.Bootstrap.Client.GetBlockHash(int64(h))
		if err != nil {
			return nil, err
		}
		block, err := network.Bootstrap.Client.GetBlock(blockHash)
		if err != nil {
			return nil, err
		}
		for _, tx := range block.Transactions {
			if !blockchain.IsAnchorTx(tx) {
				continue
			}
			info, err := anchortx.ParseAnchorScript(tx.TxIn[0].SignatureScript)
			if err != nil {
				return nil, err
			}
			anchor := posPWAAnchorEvidence{TxID: tx.TxID(), Height: h, FundingOutpoint: info.Utxo, Version: tx.Version, LockTime: tx.LockTime, Sequence: tx.TxIn[0].Sequence, Outputs: []posPWAAnchorOutput{}}
			for n, output := range tx.TxOut {
				anchor.Outputs = append(anchor.Outputs, posPWAAnchorOutput{N: n, Value: output.Value, Assets: output.Assets.Clone(), PkScript: hex.EncodeToString(output.PkScript)})
			}
			result.Anchors = append(result.Anchors, anchor)
		}
	}
	mempool, err := network.Bootstrap.Client.GetRawMempool()
	if err != nil {
		return nil, err
	}
	for _, id := range mempool {
		result.MempoolTxIDs = append(result.MempoolTxIDs, id.String())
	}
	return result, nil
}

// This controller observes the real registered hierarchy and replays the
// canonical chain through the existing MiningSequenceMgr. Its cursor is only
// in memory for the lifetime of this test process.
type sdkPWARegtestPOSController struct {
	t                           *testing.T
	network                     *realSatoshiNet
	b0                          *wallet.Manager
	sequence                    *sindexercommon.MiningSequenceMgr
	bootstrapPubKey, corePubKey string
	minerPubKey, b0PubKey       string
	roles                       map[string]string
	lastHeight                  int32
	lastHash                    chainhash.Hash
	proofs                      []posPWABlockEvidence
	rotationChecked             bool
	offlineHeight               int32
}

func newSDKPWARegtestPOSController(t *testing.T, network *realSatoshiNet, b0 *wallet.Manager,
	minerPubKey string) *sdkPWARegtestPOSController {
	t.Helper()
	require.NotNil(t, network)
	require.NotNil(t, network.Miner)
	require.NotNil(t, b0)
	require.Equal(t, int32(32), satschaincfg.RegressionNetParams.POSV2Height)
	bootstrapPubKey, corePubKey := network.Bootstrap.nodePubKey, network.Core.nodePubKey
	minerBytes, err := hex.DecodeString(minerPubKey)
	require.NoError(t, err)
	coreBytes, err := hex.DecodeString(corePubKey)
	require.NoError(t, err)
	minerInfo, err := b0.GetIndexerRPCClient_SatsNet().GetMinerInfo(minerBytes)
	require.NoError(t, err)
	require.NotNil(t, minerInfo)
	require.Equal(t, corePubKey, minerInfo.ServerNode)
	require.Greater(t, minerInfo.AscendHeight, 0)
	require.NotEmpty(t, minerInfo.AscendUtxo)
	coreInfo, err := b0.GetIndexerRPCClient_SatsNet().GetCoreNodeInfo(coreBytes)
	require.NoError(t, err)
	require.NotNil(t, coreInfo)
	child := coreInfo.ChildMiners[minerPubKey]
	require.NotNil(t, child, "A1 must be present in the actual Core child hierarchy")
	require.Equal(t, minerInfo.AscendHeight, child.AscendHeight)
	require.Equal(t, minerInfo.AscendUtxo, child.AscendUtxo)

	base, err := network.Core.IndexerURL("regtest")
	require.NoError(t, err)
	var all struct {
		Code int      `json:"code"`
		Msg  string   `json:"msg"`
		Data []string `json:"data"`
	}
	require.NoError(t, posPWAReadJSON(base+"/v3/corenode/all", &all))
	require.Zero(t, all.Code, all.Msg)
	nodes := make(map[string]*sindexercommon.CoreNodeInfo, len(all.Data))
	for _, pubKey := range all.Data {
		var response struct {
			Code int                          `json:"code"`
			Msg  string                       `json:"msg"`
			Data *sindexercommon.CoreNodeInfo `json:"data"`
		}
		endpoint := base + "/v3/corenode/info/" + url.PathEscape(pubKey)
		require.NoError(t, posPWAReadJSON(endpoint, &response))
		require.Zero(t, response.Code, response.Msg)
		require.NotNil(t, response.Data)
		nodes[pubKey] = response.Data
	}
	require.Contains(t, nodes, bootstrapPubKey)
	require.Contains(t, nodes, corePubKey)
	require.Contains(t, coreInfo.ChildMiners, minerPubKey)
	sequence := sindexercommon.NewMiningSequenceMgr(&satschaincfg.RegressionNetParams)
	require.NoError(t, sequence.Init(nodes, 0, ""), "initialize the real registered hierarchy from genesis")
	genesis, err := network.Bootstrap.Client.GetBlockHash(0)
	require.NoError(t, err)
	activeWallet := b0.GetWallet()
	require.NotNil(t, activeWallet)
	require.Equal(t, uint32(0), b0.GetCurrentAccountId(), "native controller must use B0 account 0")
	return &sdkPWARegtestPOSController{
		t: t, network: network, b0: b0, sequence: sequence, bootstrapPubKey: bootstrapPubKey,
		corePubKey: corePubKey, minerPubKey: minerPubKey,
		b0PubKey:   hex.EncodeToString(activeWallet.GetPubKey().SerializeCompressed()),
		roles:      map[string]string{bootstrapPubKey: "bootstrap", corePubKey: "core", minerPubKey: "miner"},
		lastHeight: 0, lastHash: *genesis, proofs: []posPWABlockEvidence{},
	}
}

func (p *sdkPWARegtestPOSController) sync() error {
	canonical, err := p.network.Bootstrap.Client.GetBlockHash(int64(p.lastHeight))
	if err != nil {
		return err
	}
	if *canonical != p.lastHash {
		return fmt.Errorf("canonical chain changed at verified height %d", p.lastHeight)
	}
	_, tip, err := p.network.Bootstrap.Client.GetBestBlock()
	if err != nil {
		return err
	}
	if tip < p.lastHeight {
		return fmt.Errorf("canonical tip %d is below verified cursor %d", tip, p.lastHeight)
	}
	for height := p.lastHeight + 1; height <= tip; height++ {
		hash, err := p.network.Bootstrap.Client.GetBlockHash(int64(height))
		if err != nil {
			return err
		}
		block, err := p.network.Bootstrap.Client.GetBlock(hash)
		if err != nil {
			return err
		}
		if block.Header.PrevBlock != p.lastHash {
			return fmt.Errorf("H%d does not extend verified canonical parent", height)
		}
		if len(block.Transactions) < 2 || len(block.Transactions[0].TxIn) != 1 {
			return fmt.Errorf("H%d is empty or lacks one real coinbase input", height)
		}
		coinbase := block.Transactions[0]
		rewardAddress := sindexercommon.GetMiningAddress(block, &satschaincfg.RegressionNetParams)
		if rewardAddress == "" {
			return fmt.Errorf("H%d has no actual mining reward address", height)
		}
		if satschaincfg.RegressionNetParams.POSV2Active(height) {
			if err := p.verifyPOSBlock(height, block, coinbase, rewardAddress); err != nil {
				return fmt.Errorf("verify real POS block H%d: %w", height, err)
			}
		} else if err := p.sequence.CheckMiningAddr(coinbase, int(height), rewardAddress); err != nil {
			return fmt.Errorf("verify scheduled legacy producer H%d: %w", height, err)
		}
		if err := p.sequence.MoveMiningAddr(int(height), rewardAddress); err != nil {
			return fmt.Errorf("advance real mining sequence at H%d: %w", height, err)
		}
		p.lastHeight, p.lastHash = height, *hash
	}
	return nil
}

func (p *sdkPWARegtestPOSController) verifyPOSBlock(height int32, block *swire.MsgBlock,
	coinbase *swire.MsgTx, rewardAddress string) error {
	slot := p.sequence.GetCurrentMiningInfo()
	if slot == nil {
		return fmt.Errorf("no scheduled slot at H%d", height)
	}
	producer, approver, reward, err := p.sequence.POSMiningInfo(coinbase, int(height))
	if err != nil {
		return fmt.Errorf("identify signed producer: %w", err)
	}
	rewardByProducer, err := p.sequence.POSReward(int(height), producer)
	if err != nil {
		return err
	}
	if reward != rewardAddress || rewardByProducer != rewardAddress {
		return fmt.Errorf("H%d reward address %s differs from sequence reward %s/%s", height, rewardAddress, reward, rewardByProducer)
	}
	address, err := btcutil.DecodeAddress(reward, &satschaincfg.RegressionNetParams)
	if err != nil {
		return err
	}
	expectedScript, err := txscript.PayToAddrScript(address)
	if err != nil {
		return err
	}
	if len(coinbase.TxOut) == 0 || coinbase.TxOut[0].Value <= 0 || !bytes.Equal(coinbase.TxOut[0].PkScript, expectedScript) {
		return fmt.Errorf("H%d coinbase does not pay the dynamically selected slot reward", height)
	}
	if len(coinbase.TxIn[0].Witness) != 2 {
		return fmt.Errorf("H%d must carry the exact POS witness and approval", height)
	}
	bootstrapBytes, err := hex.DecodeString(approver)
	if err != nil {
		return err
	}
	bootstrapKey, err := secp256k1.ParsePubKey(bootstrapBytes)
	if err != nil {
		return err
	}
	approval, err := ecdsa.ParseDERSignature(coinbase.TxIn[0].Witness[1])
	if err != nil {
		return err
	}
	if !anchortx.VerifyMessage(bootstrapKey,
		sindexercommon.POSApprovalMessage(satschaincfg.RegressionNetParams.Net, height, block.BlockHash()), approval) {
		return fmt.Errorf("H%d approval does not bind the real network, height and block hash", height)
	}
	if err := blockchain.ValidatePOSWitnessCommitment(btcutil.NewBlock(block), false); err != nil {
		return err
	}
	slotRole, producerRole, approverRole := p.roles[slot.PubKey], p.roles[producer], p.roles[approver]
	if slotRole == "" || producerRole == "" || approverRole == "" {
		return fmt.Errorf("H%d references an unregistered role slot=%s producer=%s approval=%s", height, slot.PubKey, producer, approver)
	}
	p.proofs = append(p.proofs, posPWABlockEvidence{Height: height, Hash: block.BlockHash().String(),
		Slot: slotRole, Producer: producerRole, RewardScript: hex.EncodeToString(coinbase.TxOut[0].PkScript)})
	return nil
}

func (p *sdkPWARegtestPOSController) produce(ctx context.Context) error {
	if p.network.Miner == nil || (p.network.Miner.Client == nil && p.offlineHeight == 0) {
		return fmt.Errorf("cannot advance a real Miner slot without the Miner node unless it is explicitly offline")
	}
	manager := p.b0
	manager.Start()
	ready := false
	for !ready {
		if err := ctx.Err(); err != nil {
			manager.Stop()
			return err
		}
		ready = manager.IsReady()
		if !ready {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if manager.GetCurrentAccountId() != 0 || hex.EncodeToString(manager.GetWallet().GetPubKey().SerializeCompressed()) != p.b0PubKey {
		manager.Stop()
		return fmt.Errorf("native heartbeat writer is not the selected B0 account 0")
	}
	tx, err := manager.SendAssets_SatsNet(manager.GetWallet().GetAddress(), wallet.PLAIN_ASSET.String(), "1000", nil)
	manager.Stop()
	if err != nil {
		return fmt.Errorf("real B0 SDK self-send heartbeat: %w", err)
	}
	if tx == nil {
		return fmt.Errorf("real B0 SDK heartbeat returned no transaction")
	}
	txID := tx.TxID()
	hash, err := chainhash.NewHashFromStr(txID)
	if err != nil {
		return err
	}
	deadline := time.NewTicker(200 * time.Millisecond)
	defer deadline.Stop()
	for {
		verbose, lookupErr := p.network.Bootstrap.Client.GetRawTransactionVerbose(hash)
		if lookupErr == nil && verbose.BlockHash != "" {
			blockHash, parseErr := chainhash.NewHashFromStr(verbose.BlockHash)
			if parseErr != nil {
				return parseErr
			}
			block, blockErr := p.network.Bootstrap.Client.GetBlock(blockHash)
			if blockErr != nil {
				return blockErr
			}
			found := false
			for _, candidate := range block.Transactions {
				if candidate.TxID() == txID {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("reported confirmed B0 heartbeat %s is absent from its block", txID)
			}
			return p.sync()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("B0 heartbeat %s did not confirm: %w", txID, ctx.Err())
		case <-deadline.C:
		}
	}
}

func (p *sdkPWARegtestPOSController) handle(ctx context.Context, action string) (map[string]any, error) {
	switch action {
	case "/rotation":
		if p.rotationChecked {
			return nil, fmt.Errorf("real producer rotation already verified")
		}
		if err := p.sync(); err != nil {
			return nil, err
		}
		start, firstProof := p.lastHeight+1, len(p.proofs)
		seenSlots, seenProducers := map[string]bool{}, map[string]bool{}
		for i := 0; i < 18 && (len(seenSlots) < 3 || len(seenProducers) < 3); i++ {
			if err := p.produce(ctx); err != nil {
				return nil, err
			}
			proof := p.proofs[len(p.proofs)-1]
			seenSlots[proof.Slot], seenProducers[proof.Producer] = true, true
		}
		if len(seenSlots) != 3 || len(seenProducers) != 3 {
			return nil, fmt.Errorf("actual dynamic rotation did not prove all three slots and producers: slots=%v producers=%v", seenSlots, seenProducers)
		}
		p.rotationChecked = true
		return map[string]any{"start_height": start, "end_height": p.lastHeight,
			"slots": seenSlots, "producers": seenProducers, "proofs": append([]posPWABlockEvidence(nil), p.proofs[firstProof:]...)}, nil
	case "/miner-offline":
		if !p.rotationChecked {
			return nil, fmt.Errorf("prove the complete three-producer rotation before taking Miner offline")
		}
		for i := 0; i < 18; i++ {
			slot := p.sequence.GetCurrentMiningInfo()
			if slot == nil {
				return nil, fmt.Errorf("dynamic mining sequence has no next slot")
			}
			if slot.PubKey == p.minerPubKey {
				break
			}
			if err := p.produce(ctx); err != nil {
				return nil, err
			}
		}
		slot := p.sequence.GetCurrentMiningInfo()
		if slot == nil || slot.PubKey != p.minerPubKey {
			return nil, fmt.Errorf("could not reach A1's actual scheduled slot")
		}
		p.offlineHeight = p.lastHeight + 1
		if err := posPWAStopNode(ctx, p.network.Miner); err != nil {
			return nil, err
		}
		firstProof := len(p.proofs)
		if err := p.produce(ctx); err != nil {
			return nil, err
		}
		if len(p.proofs) != firstProof+1 || p.proofs[firstProof].Height != p.offlineHeight ||
			p.proofs[firstProof].Slot != "miner" || p.proofs[firstProof].Producer != "core" {
			return nil, fmt.Errorf("offline A1 slot did not produce a Core-signed real substitution at H%d", p.offlineHeight)
		}
		return map[string]any{"offline_height": p.offlineHeight, "slot": "miner", "producer": "core",
			"proof": p.proofs[firstProof]}, nil
	case "/restore-miner":
		if p.offlineHeight == 0 || p.network.Miner == nil || p.network.Miner.Client != nil {
			return nil, fmt.Errorf("A1 Miner is not in the verified offline state")
		}
		if err := posPWARestartNode(ctx, p.network.Miner); err != nil {
			return nil, err
		}
		if err := connectNode(p.network.Miner, p.network.Core); err != nil {
			return nil, err
		}
		if err := connectNode(p.network.Miner, p.network.Bootstrap); err != nil {
			return nil, err
		}
		if err := joinBlocks(sdkPWARegtestOnlineNodes(p.network)); err != nil {
			return nil, err
		}
		waitSDKPWARegtestIndexes(p.t, p.network)
		if err := p.sync(); err != nil {
			return nil, err
		}
		start, firstProof := p.lastHeight+1, len(p.proofs)
		for i := 0; i < 18; i++ {
			if err := p.produce(ctx); err != nil {
				return nil, err
			}
			proof := p.proofs[len(p.proofs)-1]
			if proof.Slot == "miner" && proof.Producer == "miner" {
				return map[string]any{"start_height": start, "end_height": p.lastHeight,
					"restored_miner_proof": proof, "proofs": append([]posPWABlockEvidence(nil), p.proofs[firstProof:]...)}, nil
			}
		}
		return nil, fmt.Errorf("restored A1 account did not sign its real scheduled Miner slot")
	case "/restart-core":
		if p.offlineHeight == 0 || p.network.Miner == nil || p.network.Miner.Client == nil {
			return nil, fmt.Errorf("restore and verify the registered Miner before Core restart")
		}
		beforeHash, beforeHeight, err := p.network.Bootstrap.Client.GetBestBlock()
		if err != nil {
			return nil, err
		}
		if err := posPWAStopNode(ctx, p.network.Core); err != nil {
			return nil, err
		}
		if err := posPWARestartNode(ctx, p.network.Core); err != nil {
			return nil, err
		}
		if err := connectNode(p.network.Core, p.network.Bootstrap); err != nil {
			return nil, err
		}
		if err := joinBlocks(sdkPWARegtestOnlineNodes(p.network)); err != nil {
			return nil, err
		}
		waitSDKPWARegtestIndexes(p.t, p.network)
		afterHash, afterHeight, err := p.network.Core.Client.GetBestBlock()
		if err != nil {
			return nil, err
		}
		if afterHeight != beforeHeight || *afterHash != *beforeHash {
			return nil, fmt.Errorf("normal Core restart changed the canonical tip")
		}
		if err := p.sync(); err != nil {
			return nil, err
		}
		return map[string]any{"height": afterHeight, "hash": afterHash.String(), "normal_restart": true}, nil
	default:
		return nil, fmt.Errorf("unsupported real POS action %s", action)
	}
}

func sdkPWARegtestOnlineNodes(network *realSatoshiNet) []*testHarness {
	online := make([]*testHarness, 0, len(network.Nodes))
	for _, node := range network.Nodes {
		if node.Client != nil {
			online = append(online, node)
		}
	}
	return online
}
