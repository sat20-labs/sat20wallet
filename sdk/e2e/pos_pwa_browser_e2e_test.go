package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcjson"
	"github.com/sat20-labs/satoshinet/btcutil"
	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	scommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

const posPWAActivationHeight int32 = 32

// Each subtest invokes the existing complete fixture constructor. Go's normal
// cleanup stops that group's nodes before another group can reuse a slot.
// Only existing immutable build artifacts are shared across these serial runs.
func failUnexpectedPWASkip(t *testing.T) {
	t.Helper()
	if t.Skipped() {
		t.Error("PWA group unexpectedly skipped")
	}
}

// A public deposit is routed by the real STP Transcend runtime. Bootstrap/Core
// must first negotiate and confirm its deployment; seeding an Anchor alone
// does not create that runtime. This uses Core's existing loopback management
// API in its temporary test directory, then checks both peers' public state.
func posPWADeployPublicContracts(t *testing.T, parent context.Context, f *posPWAControl, assets []string) map[string]string {
	t.Helper()
	contracts := make(map[string]string)
	for _, asset := range assets {
		contract := wallet.NewTranscendContract()
		contract.AssetName = *indexercommon.NewAssetNameFromString(asset)
		contracts[asset] = posPWADeployContract(t, parent, f, contract, 0)
	}
	return contracts
}

func posPWADeployContract(t *testing.T, parent context.Context, f *posPWAControl, contract wallet.Contract,
	accountIndex uint32) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	// Faucet owns its channel exclusively. Use a different Core subaccount
	// from the public Transcend contracts.
	coreKey := keyFromMnemonic(t, coreMnemonic, accountIndex)
	witness, _, err := getP2WSHScript(f.keys[0].PubKey().SerializeCompressed(), coreKey.PubKey().SerializeCompressed())
	require.NoError(t, err)
	witnessHash := sha256.Sum256(witness)
	channel, err := btcutil.NewAddressWitnessScriptHash(witnessHash[:], &chaincfg.TestNetParams)
	require.NoError(t, err)
	asset := contract.GetAssetName().String()
	content, err := json.Marshal(map[string]any{"index": accountIndex, "template": contract.GetTemplateName(),
		"content": contract.Content(), "feeRate": 1, "local": true})
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+f.network.Core.managementAddr+"/local/contract/deploy", bytes.NewReader(content))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	require.NoError(t, err)
	var deployed struct {
		wwire.BaseResp
		TxID          string `json:"txId"`
		ReservationID int64  `json:"resvId"`
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&deployed)
	response.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Zero(t, deployed.Code, "deploy public %s: %s", asset, deployed.Msg)
	require.Positive(t, deployed.ReservationID)
	txid, err := chainhash.NewHashFromStr(deployed.TxID)
	require.NoError(t, err, "public deployment must broadcast its actual signed fee transaction")
	contractURL := wallet.GenerateContractURl(channel.EncodeAddress(), asset, contract.GetTemplateName())
	for {
		ready := true
		targetHeight := int32(0)
		for _, node := range []*testHarness{f.network.Bootstrap, f.network.Core} {
			var response wwire.ContractStatusResp
			readErr := posPWAReadJSON("http://"+node.stpAddr+"/testnet"+wwire.QUERY_INFO_CONTRACT+"/"+url.PathEscape(contractURL), &response)
			var state struct {
				Status int `json:"status"`
				Height int `json:"currentBlock"`
				Enable int `json:"enableBlock"`
			}
			if readErr != nil || response.Code != 0 || json.Unmarshal([]byte(response.Status), &state) != nil {
				ready = false
				continue
			}
			if state.Status < wallet.CONTRACT_STATUS_READY || state.Status >= wallet.CONTRACT_STATUS_CLOSING || state.Height < state.Enable {
				ready = false
			}
			if state.Enable != wallet.INIT_ENABLE_BLOCK && state.Enable > int(targetHeight) {
				targetHeight = int32(state.Enable)
			}
		}
		if ready {
			mined, err := f.network.Bootstrap.Client.GetRawTransactionVerbose(txid)
			require.NoError(t, err)
			require.Positive(t, mined.Confirmations, "public contract must be backed by a canonical deployment transaction")
			break
		}
		_, height, err := f.network.Bootstrap.Client.GetBestBlock()
		require.NoError(t, err)
		require.Less(t, height, posPWAActivationHeight-6, "public setup must leave room for the PWA pre-H workflows")
		if targetHeight > height {
			require.NoError(t, f.produce(ctx))
			continue
		}
		select {
		case <-ctx.Done():
			t.Fatalf("public %s never became active on both STP peers: %v", asset, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
	require.NoError(t, f.waitSynced(ctx))
	return contractURL
}

type posPWANodeState struct {
	Role      string `json:"role"`
	Height    int32  `json:"height"`
	Hash      string `json:"hash"`
	APIHeight int32  `json:"api_height"`
}

type posPWAAnchorOutput struct {
	N        int           `json:"n"`
	Value    int64         `json:"value"`
	Assets   wire.TxAssets `json:"assets"`
	PkScript string        `json:"pk_script"`
}

type posPWAAnchorEvidence struct {
	NativeProof     bool                 `json:"native_proof"`
	TxID            string               `json:"txid"`
	Height          int32                `json:"height"`
	FundingOutpoint string               `json:"funding_outpoint"`
	Version         int32                `json:"version"`
	LockTime        uint32               `json:"lock_time"`
	Sequence        uint32               `json:"sequence"`
	Outputs         []posPWAAnchorOutput `json:"outputs"`
}

type posPWASnapshot struct {
	Height       int32                  `json:"height"`
	Hash         string                 `json:"hash"`
	Nodes        []posPWANodeState      `json:"nodes"`
	L1           posPWAL1Snapshot       `json:"l1"`
	Anchors      []posPWAAnchorEvidence `json:"anchors"`
	MempoolTxIDs []string               `json:"mempool_txids"`
	POSBlocks    []posPWABlockEvidence  `json:"pos_blocks"`
}

type posPWABlockEvidence struct {
	Height       int32  `json:"height"`
	Hash         string `json:"hash"`
	Slot         string `json:"slot"`
	Producer     string `json:"producer"`
	RewardScript string `json:"reward_script"`
}

// This mutex only serializes this test's fixed actions. It is not a
// production control endpoint or a general network orchestration protocol.
type posPWAControl struct {
	mu                                                                      sync.Mutex
	network                                                                 *realSatoshiNet
	l1                                                                      *posPWAL1Indexer
	heartbeat                                                               *wire.MsgTx
	actor                                                                   *dkvsKeyPathActor
	keys                                                                    []*btcec.PrivateKey
	rewards                                                                 [][]byte
	proofs                                                                  []posPWABlockEvidence
	failures                                                                []string
	offlineHeight                                                           int32
	activationChecked, rotationChecked, substitutionChecked, restartChecked bool
}

func (f *posPWAControl) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Minute)
	defer cancel()
	var err error
	switch r.URL.Path {
	case "/inspect-psbt":
		// Independent Go decoding and script execution validate the exact bytes
		// signed by WASM. This endpoint neither signs nor broadcasts a tx.
		var request struct {
			PSBT   string `json:"psbt"`
			Verify bool   `json:"verify"`
		}
		if decodeErr := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&request); decodeErr != nil {
			http.Error(w, decodeErr.Error(), http.StatusBadRequest)
			return
		}
		result, inspectErr := posPWAInspectPSBT(request.PSBT, request.Verify)
		if inspectErr != nil {
			http.Error(w, inspectErr.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
		return
	case "/transaction":
		var request struct {
			TxID string `json:"txid"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&request); err != nil {
			http.Error(w, "invalid transaction request", http.StatusBadRequest)
			return
		}
		hash, parseHashErr := chainhash.NewHashFromStr(request.TxID)
		if parseHashErr != nil || len(request.TxID) != 64 {
			http.Error(w, "invalid txid", http.StatusBadRequest)
			return
		}
		transaction, lookupErr := f.network.Bootstrap.Client.GetRawTransactionVerbose(hash)
		if lookupErr != nil {
			// PWA has just submitted to Core; gossip to Bootstrap can still be
			// in flight. The caller must keep polling until actual confirmation.
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": lookupErr.Error()})
			return
		}
		raw, decodeErr := hex.DecodeString(transaction.Hex)
		if decodeErr != nil {
			err = decodeErr
			break
		}
		var decoded wire.MsgTx
		if decodeErr = decoded.Deserialize(bytes.NewReader(raw)); decodeErr != nil {
			err = decodeErr
			break
		}
		if decoded.TxHash() != *hash {
			err = fmt.Errorf("node raw transaction does not match requested txid")
			break
		}
		outputSats := make([]int64, len(decoded.TxOut))
		for i, output := range decoded.TxOut {
			outputSats[i] = output.Value
		}
		result := struct {
			*btcjson.TxRawResult
			OutputSats []int64                `json:"output_sats"`
			Results    []*btcjson.TxRawResult `json:"results"`
		}{TxRawResult: transaction, OutputSats: outputSats, Results: []*btcjson.TxRawResult{}}
		if transaction.BlockHash != "" {
			blockHash, parseErr := chainhash.NewHashFromStr(transaction.BlockHash)
			if parseErr != nil {
				err = parseErr
				break
			}
			block, blockErr := f.network.Bootstrap.Client.GetBlock(blockHash)
			if blockErr != nil {
				err = blockErr
				break
			}
			for _, candidate := range contractResultTxs(block) {
				spendsWork := false
				for _, input := range candidate.TxIn {
					if input.PreviousOutPoint.Hash == *hash {
						spendsWork = true
						break
					}
				}
				if !spendsWork {
					continue
				}
				resultHash := candidate.TxHash()
				actual, readErr := f.network.Bootstrap.Client.GetRawTransactionVerbose(&resultHash)
				if readErr != nil {
					err = readErr
					break
				}
				result.Results = append(result.Results, actual)
			}
			if err != nil {
				break
			}
		}
		_ = json.NewEncoder(w).Encode(result)
		return
	case "/snapshot":
	case "/confirm-l1":
		options := struct {
			WaitAnchors *bool `json:"wait_anchors"`
			EmptyBlocks int   `json:"empty_blocks"`
		}{}
		if r.Body != nil {
			decodeErr := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&options)
			if decodeErr != nil && decodeErr != io.EOF {
				err = decodeErr
				break
			}
		}
		if options.EmptyBlocks < 0 || options.EmptyBlocks > 257 {
			err = fmt.Errorf("empty_blocks must be between 0 and 257")
			break
		}
		txids := f.l1.ConfirmPending()
		if options.WaitAnchors == nil || *options.WaitAnchors {
			if len(txids) == 0 {
				err = fmt.Errorf("no pending L1 funding transaction to confirm")
				break
			}
			err = f.waitAnchors(ctx, txids)
		}
		if err == nil && options.EmptyBlocks > 0 {
			f.l1.mineEmptyBlocks(options.EmptyBlocks)
		}
	case "/activate":
		err = f.activate(ctx)
	case "/rotation":
		if !f.activationChecked {
			err = fmt.Errorf("activation must complete first")
			break
		}
		start := len(f.proofs)
		seen := make(map[string]bool)
		for len(seen) < 3 && err == nil {
			err = f.produce(ctx)
			for _, proof := range f.proofs[start:] {
				if proof.Slot == proof.Producer {
					seen[proof.Producer] = true
				}
			}
		}
		f.rotationChecked = err == nil
	case "/miner-offline":
		err = f.stopMinerAtItsSlot(ctx)
	case "/restore-miner":
		err = f.restoreMiner(ctx)
	case "/restart-core":
		if !f.activationChecked {
			err = fmt.Errorf("activation must complete first")
			break
		}
		err = f.restartCore(ctx)
		f.restartChecked = err == nil
	case "/mine":
		err = f.produce(ctx)
	default:
		http.Error(w, "unknown test action", http.StatusNotFound)
		return
	}
	var snapshot *posPWASnapshot
	if err == nil {
		snapshot, err = f.snapshot()
	}
	if err != nil {
		f.failures = append(f.failures, r.URL.Path+": "+err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(snapshot)
}

func posPWAInspectPSBT(encoded string, verify bool) (map[string]any, error) {
	raw, err := hex.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	packet, err := spsbt.NewFromRawBytes(bytes.NewReader(raw), false)
	if err != nil {
		return nil, err
	}
	unsigned, err := wallet.EncodeMsgTx_SatsNet(packet.UnsignedTx)
	if err != nil {
		return nil, err
	}
	inputs := make([]map[string]any, 0, len(packet.Inputs))
	outputs := make([]map[string]any, 0, len(packet.UnsignedTx.TxOut))
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	for i, input := range packet.Inputs {
		if i >= len(packet.UnsignedTx.TxIn) || input.WitnessUtxo == nil {
			return nil, fmt.Errorf("PSBT input %d has no witness prevout", i)
		}
		point := packet.UnsignedTx.TxIn[i].PreviousOutPoint
		prev := input.WitnessUtxo
		fetcher.AddPrevOut(point, prev)
		inputs = append(inputs, map[string]any{"outpoint": point.String(), "value": prev.Value,
			"assets": prev.Assets, "pk_script": hex.EncodeToString(prev.PkScript)})
	}
	for _, output := range packet.UnsignedTx.TxOut {
		outputs = append(outputs, map[string]any{"value": output.Value, "assets": output.Assets,
			"pk_script": hex.EncodeToString(output.PkScript)})
	}
	result := map[string]any{"unsigned_tx": unsigned, "inputs": inputs, "outputs": outputs, "signatures_verified": false}
	if verify {
		if err := spsbt.MaybeFinalizeAll(packet); err != nil {
			return nil, err
		}
		tx, err := spsbt.Extract(packet)
		if err != nil {
			return nil, err
		}
		hashes := txscript.NewTxSigHashes(tx, fetcher)
		for i, input := range tx.TxIn {
			prev := fetcher.FetchPrevOutput(input.PreviousOutPoint)
			vm, err := txscript.NewEngine(prev.PkScript, tx, i, txscript.StandardVerifyFlags,
				nil, hashes, prev.Value, prev.Assets, fetcher)
			if err != nil {
				return nil, err
			}
			if err := vm.Execute(); err != nil {
				return nil, fmt.Errorf("PSBT signature %d: %w", i, err)
			}
		}
		encodedTx, err := wallet.EncodeMsgTx_SatsNet(tx)
		if err != nil {
			return nil, err
		}
		result["tx"], result["txid"], result["signatures_verified"] = encodedTx, tx.TxID(), true
	}
	return result, nil
}

func posPWASDKPSBTFixture(t *testing.T, seed *wire.MsgTx, owner, recipient map[string]string, asset string, realIndexers ...wallet.IndexerRPCClient) map[string]any {
	t.Helper()
	ownerScript, err := hex.DecodeString(owner["pk_script"])
	require.NoError(t, err)
	recipientScript, err := hex.DecodeString(recipient["pk_script"])
	require.NoError(t, err)
	require.LessOrEqual(t, len(realIndexers), 1)
	indexedAssetOutput := func(point string, previous *wire.TxOut, amount string) *indexercommon.AssetsInUtxo {
		if len(realIndexers) == 1 {
			indexed, lookupErr := realIndexers[0].GetTxOutput(point)
			require.NoError(t, lookupErr)
			require.NotNil(t, indexed)
			require.Equal(t, previous.Value, indexed.OutValue.Value)
			require.Equal(t, previous.PkScript, indexed.OutValue.PkScript)
			require.Equal(t, previous.Assets, indexed.Assets)
			require.Len(t, indexed.Assets, 1)
			require.Equal(t, asset, indexed.Assets[0].Name.String())
			require.Equal(t, amount, indexed.Assets[0].Amount.String())
			return indexed.ToAssetsInUtxo()
		}
		return &indexercommon.AssetsInUtxo{OutPoint: point, Value: previous.Value, PkScript: previous.PkScript,
			Assets: []*indexercommon.DisplayAsset{displayAssetWithMeta(asset, amount, 0, 0)}}
	}
	var indexes []uint32
	for i, output := range seed.TxOut {
		if bytes.Equal(output.PkScript, ownerScript) {
			indexes = append(indexes, uint32(i))
		}
	}
	require.Len(t, indexes, 2)
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: seed.TxHash(), Index: indexes[0]}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(2000, txAsset(asset, 700), recipientScript))
	tx.AddTxOut(wire.NewTxOut(seed.TxOut[indexes[0]].Value-2500, txAsset(asset, 12_500_000-700), ownerScript))
	packet, err := spsbt.NewFromUnsignedTx(tx)
	require.NoError(t, err)
	packet.Inputs[0].NonWitnessUtxo = seed.Copy()
	packet.Inputs[0].WitnessUtxo = seed.Copy().TxOut[indexes[0]]
	encode := func(p *spsbt.Packet) string {
		var buf bytes.Buffer
		require.NoError(t, p.Serialize(&buf))
		return hex.EncodeToString(buf.Bytes())
	}
	encoded := encode(packet)
	observed, err := posPWAInspectPSBT(encoded, false)
	require.NoError(t, err)
	var orders []string
	var points []string
	var infos []*indexercommon.AssetsInUtxo
	for i, index := range indexes {
		prev := seed.TxOut[index]
		point := fmt.Sprintf("%s:%d", seed.TxID(), index)
		info := indexedAssetOutput(point, prev, "12500000")
		order := &wallet.UtxoInfo{AssetsInUtxo: *info, Price: int64((i + 1) * 1000)}
		orders = append(orders, sdkReviewJSON(t, order))
		points = append(points, point)
		infos = append(infos, info)
	}
	var buyerOrder *wallet.UtxoInfo
	for i, prev := range seed.TxOut {
		if bytes.Equal(prev.PkScript, recipientScript) {
			point := fmt.Sprintf("%s:%d", seed.TxID(), i)
			buyerOrder = &wallet.UtxoInfo{AssetsInUtxo: *indexedAssetOutput(point, prev, "25000000")}
		}
	}
	require.NotNil(t, buyerOrder)
	// NonWitnessUtxo anchors the witness metadata. Altered value/asset copies
	// must be rejected before WASM signing, even though the outpoint is valid.
	packet.Inputs[0].WitnessUtxo.Value++
	badValue := encode(packet)
	packet.Inputs[0].WitnessUtxo = seed.Copy().TxOut[indexes[0]]
	packet.Inputs[0].WitnessUtxo.Assets[0].Amount = *indexercommon.NewDecimal(12_500_001, 0)
	badAsset := encode(packet)
	exchangeContent, err := contractcommon.EncodeTemplateExchangeContent(contractcommon.TemplateExchangeContract{
		AssetAName: asset, AssetBName: "::", PriceMode: contractcommon.ExchangePriceModeHeight,
		Steps: []contractcommon.TemplateExchangePriceStep{{Threshold: "0", BPerA: "0.001"}},
	})
	require.NoError(t, err)
	return map[string]any{"psbt": encoded, "unsignedTx": observed["unsigned_tx"], "inputs": observed["inputs"],
		"outputs": observed["outputs"], "orders": orders, "orderOutpoints": points,
		"additionalInput": sdkReviewJSON(t, infos[1]), "additionalOutput": sdkReviewJSON(t, infos[1]),
		"additionalValue": infos[1].Value, "tampered": []string{badValue, badAsset},
		"buyerOrder": sdkReviewJSON(t, buyerOrder), "buyerValue": buyerOrder.Value,
		"exchangeContent": base64.StdEncoding.EncodeToString(exchangeContent), "l2BatchFee": wallet.DEFAULT_FEE_SATSNET}
}

func (f *posPWAControl) onlineNodes() []*testHarness {
	nodes := make([]*testHarness, 0, 3)
	for _, node := range f.network.Nodes {
		if node.Client != nil {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func posPWAReadJSON(target string, output any) error {
	response, err := (&http.Client{Timeout: 5 * time.Second}).Get(target)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", target, response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(output)
}

func (f *posPWAControl) nodeStates() ([]posPWANodeState, error) {
	states := make([]posPWANodeState, 0, 3)
	for _, node := range f.onlineNodes() {
		hash, height, err := node.Client.GetBestBlock()
		if err != nil {
			return nil, err
		}
		base, err := node.IndexerURL("testnet")
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
			return nil, fmt.Errorf("%s indexer: %s", node.role, indexed.Msg)
		}
		states = append(states, posPWANodeState{Role: node.role, Height: height, Hash: hash.String(), APIHeight: indexed.Data.Height})
	}
	return states, nil
}

func (f *posPWAControl) waitSynced(ctx context.Context) error {
	var last string
	for {
		states, err := f.nodeStates()
		if err == nil && len(states) >= 2 {
			matched := true
			for _, state := range states {
				if state.Height != states[0].Height || state.Hash != states[0].Hash || state.APIHeight != state.Height {
					matched = false
				}
			}
			if matched {
				return nil
			}
			last = fmt.Sprint(states)
		} else {
			last = fmt.Sprint(err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("canonical/indexer convergence: %s: %w", last, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (f *posPWAControl) anchors(height int32) ([]posPWAAnchorEvidence, error) {
	anchors := make([]posPWAAnchorEvidence, 0)
	for h := int32(1); h <= height; h++ {
		hash, err := f.network.Bootstrap.Client.GetBlockHash(int64(h))
		if err != nil {
			return nil, err
		}
		block, err := f.network.Bootstrap.Client.GetBlock(hash)
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
			item := posPWAAnchorEvidence{TxID: tx.TxHash().String(), Height: h, FundingOutpoint: info.Utxo,
				Version: tx.Version, LockTime: tx.LockTime, Sequence: tx.TxIn[0].Sequence, Outputs: make([]posPWAAnchorOutput, 0, len(tx.TxOut))}
			for n, output := range tx.TxOut {
				item.Outputs = append(item.Outputs, posPWAAnchorOutput{N: n, Value: output.Value,
					Assets: output.Assets.Clone(), PkScript: hex.EncodeToString(output.PkScript)})
			}
			anchors = append(anchors, item)
		}
	}
	return anchors, nil
}

func (f *posPWAControl) snapshot() (*posPWASnapshot, error) {
	states, err := f.nodeStates()
	if err != nil {
		return nil, err
	}
	if len(states) == 0 {
		return nil, fmt.Errorf("no online nodes")
	}
	anchors, err := f.anchors(states[0].Height)
	if err != nil {
		return nil, err
	}
	mempool, err := f.network.Bootstrap.Client.GetRawMempool()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(mempool))
	for _, id := range mempool {
		ids = append(ids, id.String())
	}
	return &posPWASnapshot{Height: states[0].Height, Hash: states[0].Hash, Nodes: states,
		L1: f.l1.Snapshot(), Anchors: anchors, MempoolTxIDs: ids, POSBlocks: append([]posPWABlockEvidence{}, f.proofs...)}, nil
}

func (f *posPWAControl) waitAnchors(ctx context.Context, fundingTxIDs []string) error {
	var last string
	for {
		snapshot, err := f.snapshot()
		if err != nil {
			return err
		}
		found := make(map[string]posPWAAnchorEvidence)
		for _, anchor := range snapshot.Anchors {
			for _, txid := range fundingTxIDs {
				if strings.HasPrefix(anchor.FundingOutpoint, txid+":") {
					found[txid] = anchor
				}
			}
		}
		if len(found) == len(fundingTxIDs) {
			if err := f.waitSynced(ctx); err != nil {
				return err
			}
			indexedAll := true
			// Check actual AIDX records on every online node. The public indexer
			// API exposes height plus Anchor identity, not an independent head hash.
			for _, node := range f.onlineNodes() {
				base, err := node.IndexerURL("testnet")
				if err != nil {
					return err
				}
				for _, anchor := range found {
					var indexed struct {
						Code int                 `json:"code"`
						Msg  string              `json:"msg"`
						Data *scommon.AscendData `json:"data"`
					}
					if err := posPWAReadJSON(base+"/v3/ascend/"+anchor.FundingOutpoint, &indexed); err != nil {
						indexedAll = false
						last = fmt.Sprintf("%s Anchor API: %v", node.role, err)
						continue
					}
					if indexed.Code != 0 || indexed.Data == nil || indexed.Data.AnchorTxId != anchor.TxID || indexed.Data.Height != int(anchor.Height) {
						indexedAll = false
						last = fmt.Sprintf("%s AIDX has not indexed Anchor %s at height %d: %+v", node.role, anchor.TxID, anchor.Height, indexed)
					}
				}
			}
			if indexedAll {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("L1 funding %v did not become indexed Anchors (%s): %w", fundingTxIDs, last, ctx.Err())
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func (f *posPWAControl) checkBlock(height int32, block *wire.MsgBlock, producer int) error {
	if len(block.Transactions) == 0 || len(block.Transactions[0].TxIn) != 1 {
		return fmt.Errorf("H%d missing coinbase", height)
	}
	coinbase := block.Transactions[0]
	if err := scommon.VerifyStandardCoinbaseScript(coinbase.TxIn[0].SignatureScript, f.keys[producer].PubKey().SerializeCompressed()); err != nil {
		return fmt.Errorf("H%d producer=%d: %w", height, producer, err)
	}
	if height < posPWAActivationHeight {
		if len(coinbase.TxIn[0].Witness) != 1 {
			return fmt.Errorf("H%d must retain legacy witness", height)
		}
		return nil
	}
	slot := int((height - posPWAActivationHeight) % 3)
	if len(coinbase.TxOut) == 0 || !bytes.Equal(coinbase.TxOut[0].PkScript, f.rewards[slot]) {
		return fmt.Errorf("H%d slot=%d paid wrong reward channel", height, slot)
	}
	if len(coinbase.TxIn[0].Witness) != 2 {
		return fmt.Errorf("H%d missing POS approval", height)
	}
	signature, err := ecdsa.ParseDERSignature(coinbase.TxIn[0].Witness[1])
	if err != nil {
		return err
	}
	if !anchortx.VerifyMessage(f.keys[0].PubKey(), scommon.POSApprovalMessage(chaincfg.TestNetParams.Net, height, block.BlockHash()), signature) {
		return fmt.Errorf("H%d approval does not bind network/height/hash", height)
	}
	if err := blockchain.ValidatePOSWitnessCommitment(btcutil.NewBlock(block), false); err != nil {
		return err
	}
	roles := []string{"bootstrap", "core", "miner"}
	f.proofs = append(f.proofs, posPWABlockEvidence{Height: height, Hash: block.BlockHash().String(),
		Slot: roles[slot], Producer: roles[producer], RewardScript: hex.EncodeToString(coinbase.TxOut[0].PkScript)})
	return nil
}

func (f *posPWAControl) produce(ctx context.Context) error {
	previousHash, previousHeight, err := f.network.Bootstrap.Client.GetBestBlock()
	if err != nil {
		return err
	}
	previous := f.heartbeat.TxOut[0]
	if previous.Value < 2000 {
		return fmt.Errorf("heartbeat funding exhausted")
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: f.heartbeat.TxHash(), Index: 0}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(previous.Value-1000, previous.Assets.Clone(), f.actor.PkScript))
	// Sign through the production SDK; this key-path spend needs its real
	// private key and cannot pass through the older OP_TRUE fixture script.
	fetcher := txscript.NewCannedPrevOutputFetcher(previous.PkScript, previous.Value, previous.Assets)
	if err := wallet.SignTxIn_P2TR(tx, 0, f.actor.Key, fetcher); err != nil {
		return err
	}
	hash, err := f.network.Bootstrap.Client.SendRawTransaction(tx, true)
	if err != nil {
		return err
	}
	if *hash != tx.TxHash() {
		return fmt.Errorf("heartbeat txid changed")
	}
	for {
		verbose, err := f.network.Bootstrap.Client.GetRawTransactionVerbose(hash)
		if err == nil && verbose.BlockHash != "" {
			blockHash, err := chainhash.NewHashFromStr(verbose.BlockHash)
			if err != nil {
				return err
			}
			currentHash, currentHeight, err := f.network.Bootstrap.Client.GetBestBlock()
			if err != nil {
				return err
			}
			if currentHeight <= previousHeight {
				return fmt.Errorf("confirmed heartbeat did not extend H%d", previousHeight)
			}
			parent := *previousHash
			found := false
			// Normal wallet activity may submit another transaction while this
			// heartbeat waits. Check every resulting canonical slot instead of
			// requiring one block for one test request.
			for height := previousHeight + 1; height <= currentHeight; height++ {
				canonical, err := f.network.Bootstrap.Client.GetBlockHash(int64(height))
				if err != nil {
					return err
				}
				block, err := f.network.Bootstrap.Client.GetBlock(canonical)
				if err != nil {
					return err
				}
				if block.Header.PrevBlock != parent {
					return fmt.Errorf("H%d does not extend the previous canonical block", height)
				}
				producer := 0
				if height >= posPWAActivationHeight {
					producer = int((height - posPWAActivationHeight) % 3)
					if producer == 2 && f.network.Miner.Client == nil {
						producer = 1
					}
				}
				if err := f.checkBlock(height, block, producer); err != nil {
					return err
				}
				if *canonical == *blockHash {
					found = true
				}
				parent = *canonical
			}
			if !found || parent != *currentHash {
				return fmt.Errorf("heartbeat block is outside the canonical extension")
			}
			f.heartbeat = tx
			return f.waitSynced(ctx)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("heartbeat %s not confirmed: %w", hash, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (f *posPWAControl) activate(ctx context.Context) error {
	if f.activationChecked {
		return fmt.Errorf("activation scenario already ran")
	}
	if len(f.l1.Snapshot().PendingTxIDs) != 0 {
		return fmt.Errorf("drain all L1 funding before H")
	}
	pool, err := f.network.Bootstrap.Client.GetRawMempool()
	if err != nil {
		return err
	}
	if len(pool) != 0 {
		return fmt.Errorf("drain L2 mempool before H")
	}
	_, height, err := f.network.Bootstrap.Client.GetBestBlock()
	if err != nil {
		return err
	}
	if height >= posPWAActivationHeight {
		return fmt.Errorf("preactivation scenarios already crossed H")
	}
	for height < posPWAActivationHeight-1 {
		if err := f.produce(ctx); err != nil {
			return err
		}
		_, height, err = f.network.Bootstrap.Client.GetBestBlock()
		if err != nil {
			return err
		}
	}
	if err := f.restartCore(ctx); err != nil {
		return err
	}
	for i := 0; i < 2; i++ {
		if err := f.produce(ctx); err != nil {
			return err
		}
	}
	f.activationChecked = true
	return nil
}

func (f *posPWAControl) stopMinerAtItsSlot(ctx context.Context) error {
	if !f.rotationChecked || f.network.Miner.Client == nil {
		return fmt.Errorf("complete online rotation before stopping Miner")
	}
	for {
		_, height, err := f.network.Bootstrap.Client.GetBestBlock()
		if err != nil {
			return err
		}
		if (height+1-posPWAActivationHeight)%3 == 2 {
			f.offlineHeight = height + 1
			break
		}
		if err := f.produce(ctx); err != nil {
			return err
		}
	}
	return posPWAStopNode(ctx, f.network.Miner)
}

func (f *posPWAControl) restoreMiner(ctx context.Context) error {
	if f.offlineHeight == 0 || f.network.Miner.Client != nil {
		return fmt.Errorf("Miner was not stopped for its slot")
	}
	_, height, err := f.network.Bootstrap.Client.GetBestBlock()
	if err != nil {
		return err
	}
	if height < f.offlineHeight {
		return fmt.Errorf("PWA deposit has not confirmed during Miner outage")
	}
	hash, err := f.network.Bootstrap.Client.GetBlockHash(int64(f.offlineHeight))
	if err != nil {
		return err
	}
	block, err := f.network.Bootstrap.Client.GetBlock(hash)
	if err != nil {
		return err
	}
	if err := f.checkBlock(f.offlineHeight, block, 1); err != nil {
		return err
	}
	if err := posPWARestartNode(ctx, f.network.Miner); err != nil {
		return err
	}
	if err := connectNode(f.network.Miner, f.network.Core); err != nil {
		return err
	}
	if err := connectNode(f.network.Miner, f.network.Bootstrap); err != nil {
		return err
	}
	if err := f.waitSynced(ctx); err != nil {
		return err
	}
	start := len(f.proofs)
	for {
		if err := f.produce(ctx); err != nil {
			return err
		}
		restored := false
		for _, proof := range f.proofs[start:] {
			if proof.Slot == "miner" && proof.Producer == "miner" {
				restored = true
			}
		}
		if restored {
			break
		}
	}
	f.substitutionChecked = true
	return nil
}

func (f *posPWAControl) restartCore(ctx context.Context) error {
	hash, height, err := f.network.Bootstrap.Client.GetBestBlock()
	if err != nil {
		return err
	}
	if err := posPWAStopNode(ctx, f.network.Core); err != nil {
		return err
	}
	if err := posPWARestartNode(ctx, f.network.Core); err != nil {
		return err
	}
	if err := connectNode(f.network.Core, f.network.Bootstrap); err != nil {
		return err
	}
	if err := f.waitSynced(ctx); err != nil {
		return err
	}
	got, gotHeight, err := f.network.Core.Client.GetBestBlock()
	if err != nil {
		return err
	}
	if gotHeight != height || *got != *hash {
		return fmt.Errorf("normal restart changed canonical tip")
	}
	return nil
}

// Unlike TearDown/restartTestHarness, this acceptance path waits for normal
// RPC stop and a zero process exit. A timeout fails the case before cleanup.
func posPWAStopNode(ctx context.Context, node *testHarness) error {
	if node.Client == nil || node.cmd == nil {
		return fmt.Errorf("%s is not running", node.role)
	}
	if _, err := node.Client.RawRequest("stop", nil); err != nil {
		return err
	}
	node.Client.Shutdown()
	node.Client.WaitForShutdown()
	node.Client = nil
	select {
	case <-node.exited:
		if node.exitErr != nil {
			return fmt.Errorf("%s normal stop: %w", node.role, node.exitErr)
		}
		return nil
	case <-ctx.Done():
		_ = node.cmd.Process.Kill()
		<-node.exited
		node.cmd = nil
		return ctx.Err()
	case <-time.After(45 * time.Second):
		_ = node.cmd.Process.Kill()
		<-node.exited
		node.cmd = nil
		return fmt.Errorf("%s did not stop normally in 45 seconds", node.role)
	}
}

func posPWARestartNode(ctx context.Context, node *testHarness) error {
	if node.Client != nil || node.cmd == nil {
		return fmt.Errorf("%s is not stopped", node.role)
	}
	previous := node.cmd
	logFile, err := os.OpenFile(node.logFile, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	command := exec.Command(previous.Path, previous.Args[1:]...)
	command.Dir, command.Env = previous.Dir, append([]string(nil), previous.Env...)
	command.Stdout, command.Stderr = logFile, logFile
	command.ExtraFiles = satoshinetRuntimeLocks
	if err := node.startProcess(command); err != nil {
		_ = logFile.Close()
		return err
	}
	node.extraLogFiles = append(node.extraLogFiles, logFile)
	return node.waitForRPC(ctx)
}

// Keep Playwright's graceful signal handling; the platform helper owns only
// browser groups explicitly registered by this command.
func configurePWACancellation(t *testing.T, command *exec.Cmd) func() error {
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	command.WaitDelay = 5 * time.Second
	return configureOwnedPWACancellation(t, command)
}
