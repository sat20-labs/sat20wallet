package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
	"github.com/tyler-smith/go-bip39"
)

const sdkPWARegtestWASMAsset = "brc20:f:swze"
const sdkPWARegtestWASMAmount int64 = 50_000_000

func TestSDKPWARealRegtestSDKWASMInterfacesBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "sdk-wasm")
}

func sdkPWARegtestSyntheticIdentity(t *testing.T, label string) map[string]string {
	t.Helper()
	entropy := sha256.Sum256([]byte("sat20wallet-pwa-e2e:" + label))
	mnemonic, err := bip39.NewMnemonic(entropy[:16])
	require.NoError(t, err)
	derived := wallet.NewInternalWalletWithMnemonic(mnemonic, "", wallet.GetChainParam())
	require.NotNil(t, derived)
	pkScript, err := wallet.PubKeyToPkScript(derived.GetPubKey())
	require.NoError(t, err)
	address, err := wallet.AddrFromPkScript_SatsNet(pkScript)
	require.NoError(t, err)
	require.Equal(t, derived.GetAddress(), address)
	return map[string]string{"mnemonic": mnemonic, "address": address,
		"pk_script": hex.EncodeToString(pkScript), "password": "regtest-sdk-wasm-" + label}
}

func confirmSDKPWARegtestL1SDKTransaction(t *testing.T, manager *wallet.Manager, txid string,
	mine func(string, int)) {
	t.Helper()
	var pending []string
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getrawmempool"), &pending))
	require.Contains(t, pending, txid, "SDK transaction must reach the real Core mempool before confirmation")
	mine(manager.GetWallet().GetAddressByIndex(1), 1)
	require.Eventually(t, func() bool {
		info, err := manager.GetIndexerClient().GetTxInfo(txid)
		return err == nil && info != nil && info.Confirmations > 0
	}, rpcReadyTimeout, 200*time.Millisecond, "real L1 indexer did not confirm SDK transaction %s", txid)
}

func waitSDKPWARegtestSplicingAssetReady(t *testing.T, manager *wallet.Manager, network *realSatoshiNet,
	channelID string, reservationID int64, expectedAnchorOutpoint, assetText string, expectedAmount int64) {
	t.Helper()
	asset := indexercommon.NewAssetNameFromString(assetText)
	require.NotNil(t, asset)
	client := &http.Client{Timeout: 10 * time.Second}
	endpoint := "http://" + network.Core.stpAddr + "/regtest/info/channel/" + channelID
	var spendableOutpoint string
	require.Eventually(t, func() bool {
		local := manager.GetChannel(channelID)
		if local == nil {
			return false
		}
		local.Mutex.RLock()
		if local.Status != wallet.CS_READY || local.ChanPoint == nil {
			local.Mutex.RUnlock()
			return false
		}
		localPoint := local.ChanPoint.OutPointStr
		localValid := local.GetValidOutput_SatsNet()
		localPending := local.GetPendingOutput_SatsNet()
		local.Mutex.RUnlock()

		if _, pending := manager.GetSplicingReservation(reservationID); pending {
			return false
		}
		response, err := client.Get(endpoint)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		var peer struct {
			Code    int                 `json:"code"`
			Msg     string              `json:"msg"`
			Channel *wallet.ChannelInfo `json:"channel"`
		}
		if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&peer) != nil ||
			peer.Code != 0 || peer.Channel == nil || peer.Channel.Status != int(wallet.CS_READY) ||
			peer.Channel.ChanPoint != localPoint {
			return false
		}
		localOutpoint, localSpendable := sdkPWARegtestExactAssetOutpoint(localValid, asset, expectedAmount)
		remoteOutpoint, remoteSpendable := sdkPWARegtestExactAssetOutpoint(peer.Channel.UtxoL2, asset, expectedAmount)
		if !localSpendable || !remoteSpendable || localOutpoint != remoteOutpoint ||
			localOutpoint != expectedAnchorOutpoint || remoteOutpoint != expectedAnchorOutpoint ||
			sdkPWARegtestHasAsset(localPending, asset) ||
			sdkPWARegtestHasAsset(peer.Channel.PendingUtxoL2, asset) {
			return false
		}
		spendableOutpoint = localOutpoint
		return true
	}, rpcReadyTimeout, 200*time.Millisecond,
		"both peers must finish splice reservation and expose the exact confirmed %s=%d L2 output as spendable",
		assetText, expectedAmount)
	t.Logf("real splice asset spendable on both peers: %s %s=%d", spendableOutpoint, assetText, expectedAmount)
}

func sdkPWARegtestExactAssetOutpoint(outputs []*wallet.TxOutput_SatsNet, asset *indexercommon.AssetName,
	expectedAmount int64) (string, bool) {
	var total int64
	var outpoint string
	count := 0
	for _, output := range outputs {
		if output == nil {
			continue
		}
		if indexercommon.IsPlainAsset(asset) {
			if len(output.OutValue.Assets) == 0 && output.OutValue.Value == expectedAmount {
				total += output.OutValue.Value
				count++
				outpoint = output.OutPointStr
			}
			continue
		}
		for _, info := range output.OutValue.Assets {
			if info.Name != *asset {
				continue
			}
			expected := indexercommon.NewDecimal(expectedAmount, 0)
			if info.Amount.Cmp(expected) != 0 {
				return "", false
			}
			total += info.Amount.Int64()
			count++
			outpoint = output.OutPointStr
		}
	}
	return outpoint, count == 1 && total == expectedAmount && outpoint != ""
}

func sdkPWARegtestHasAsset(outputs []*wallet.TxOutput_SatsNet, asset *indexercommon.AssetName) bool {
	for _, output := range outputs {
		if output == nil {
			continue
		}
		if indexercommon.IsPlainAsset(asset) && len(output.OutValue.Assets) == 0 && output.OutValue.Value > 0 {
			return true
		}
		for _, info := range output.OutValue.Assets {
			if info.Name == *asset && info.Amount.Sign() > 0 {
				return true
			}
		}
	}
	return false
}

func prepareSDKPWARegtestSDKWASMFixture(t *testing.T, network *realSatoshiNet,
	ordinary1, ordinary2 *wallet.Manager, mine func(string, int), config sdkPWARegtestConfig) (map[string]string,
	map[string]string, map[string]any) {
	t.Helper()
	require.Equal(t, "regtest", config.Chain)
	owner := sdkPWARegtestSyntheticIdentity(t, "sdk-wasm-owner")
	buyer := sdkPWARegtestSyntheticIdentity(t, "sdk-wasm-buyer")

	// Fund both deterministic identities with SDK-signed, indexed Bitcoin
	// outputs. These are real plain UTXOs used by the existing L1 send cases.
	ownerFunding, _, err := ordinary1.BatchSendPlainSats(owner["address"], 5_000_000, 2, 2, nil)
	require.NoError(t, err)
	confirmSDKPWARegtestL1SDKTransaction(t, ordinary1, ownerFunding, mine)
	buyerFunding, _, err := ordinary1.BatchSendPlainSats(buyer["address"], 1_000_000, 1, 2, nil)
	require.NoError(t, err)
	confirmSDKPWARegtestL1SDKTransaction(t, ordinary1, buyerFunding, mine)
	for _, actor := range []map[string]string{owner, buyer} {
		outputs := ordinary1.GetIndexerClient().GetUtxoListWithTicker(actor["address"], &indexercommon.ASSET_PLAIN_SAT)
		require.NotEmpty(t, outputs, "synthetic actor must have actual indexed L1 plain outputs")
		for _, indexed := range outputs {
			require.NotEmpty(t, indexed.OutPoint)
			previous, outputErr := ordinary1.GetIndexerClient().GetTxOutput(indexed.OutPoint)
			require.NoError(t, outputErr)
			require.Equal(t, indexed.Value, previous.OutValue.Value)
			require.Equal(t, indexed.PkScript, previous.OutValue.PkScript)
		}
	}

	// Use the existing READY channel and real BRC20 issuer. No L1 indexer
	// records, channel balances, or transaction prevouts are seeded by tests.
	channelID, state := prepareSDKPWARegtestChannelPrerequisites(t, "l2-channel-open", ordinary1, ordinary2, mine)
	require.Equal(t, sdkPWARegtestChannelReadyReused, state,
		"SDK WASM assets require the channel opened by the preceding real browser case")
	channel := ordinary2.GetChannel(channelID)
	require.NotNil(t, channel)
	require.Equal(t, wallet.CS_READY, channel.Status)
	attachRGB11RegtestWallet(t, ordinary2, network)

	asset := indexercommon.NewAssetNameFromString(sdkPWARegtestWASMAsset)
	require.NotNil(t, asset)
	if ordinary2.GetTickerInfoV2(sdkPWARegtestWASMAsset) == nil {
		deployed, deployErr := ordinary2.DeployTicker_brc20(asset.Ticker, sdkPWARegtestWASMAmount,
			sdkPWARegtestWASMAmount, 0, 2)
		require.NoError(t, deployErr)
		require.NotNil(t, deployed)
		require.NotNil(t, deployed.CommitTx)
		require.NotNil(t, deployed.RevealTx)
		confirmSDKPWARegtestL1SDKTransaction(t, ordinary2, deployed.CommitTx.TxID(), mine)
	}
	ticker := ordinary2.GetTickerInfoV2(sdkPWARegtestWASMAsset)
	require.NotNil(t, ticker, "real L1 indexer must discover the SDK-deployed four-letter ticker")
	require.Equal(t, "50000000", ticker.MaxSupply)
	require.Equal(t, "50000000", ticker.Limit)
	require.Equal(t, 0, ticker.Divisibility)
	minted := ordinary2.GetAssetBalance("", asset)
	if minted == nil || minted.Int64() == 0 {
		result, mintErr := ordinary2.MintAsset_brc20(ordinary2.GetWallet().GetAddress(), asset,
			indexercommon.NewDecimal(sdkPWARegtestWASMAmount, 0), nil, 2)
		require.NoError(t, mintErr)
		require.NotNil(t, result)
		require.NotNil(t, result.CommitTx)
		confirmSDKPWARegtestL1SDKTransaction(t, ordinary2, result.CommitTx.TxID(), mine)
	}
	minted = ordinary2.GetAssetBalance("", asset)
	require.NotNil(t, minted)
	require.Equal(t, sdkPWARegtestWASMAmount, minted.Int64())
	transferOutputs := ordinary2.GetIndexerClient().GetUtxoListWithTicker(ordinary2.GetWallet().GetAddress(), asset)
	if len(transferOutputs) == 0 {
		transfer, transferErr := ordinary2.MintTransfer_brc20(ordinary2.GetWallet().GetAddress(),
			ordinary2.GetWallet().GetAddress(), nil, asset,
			indexercommon.NewDecimal(sdkPWARegtestWASMAmount, 0), 2, nil, false, nil, false, true, true)
		require.NoError(t, transferErr)
		require.NotNil(t, transfer)
		require.NotNil(t, transfer.CommitTx)
		confirmSDKPWARegtestL1SDKTransaction(t, ordinary2, transfer.CommitTx.TxID(), mine)
		transferOutputs = ordinary2.GetIndexerClient().GetUtxoListWithTicker(ordinary2.GetWallet().GetAddress(), asset)
	}
	require.Len(t, transferOutputs, 1, "one actual confirmed transfer inscription must fund the splice")
	transferPoint := transferOutputs[0].OutPoint
	indexedTransfer, err := ordinary2.GetIndexerClient().GetTxOutput(transferPoint)
	require.NoError(t, err)
	require.Equal(t, transferOutputs[0].Value, indexedTransfer.OutValue.Value)
	require.Equal(t, transferOutputs[0].PkScript, indexedTransfer.OutValue.PkScript)

	spliceTxID, spliceReservationID, err := ordinary2.SplicingIn(channelID, sdkPWARegtestWASMAsset,
		strconv.FormatInt(sdkPWARegtestWASMAmount, 10), nil, nil, nil, nil, 2, wallet.SPLICING_REASON_LOCAL)
	require.NoError(t, err)
	require.NotZero(t, spliceReservationID)
	spliceReservation, reservationFound := ordinary2.GetSplicingReservation(spliceReservationID)
	require.True(t, reservationFound, "the actual splice reservation must expose its anchor before L1 confirmation")
	require.NotNil(t, spliceReservation)
	anchorTxID := spliceReservation.AnchorTxId()
	require.NotEmpty(t, anchorTxID, "the SDK reservation must identify this splice's real anchor transaction")
	anchorOutpoint := anchorTxID + ":0"
	confirmSDKPWARegtestL1SDKTransaction(t, ordinary2, spliceTxID, mine)
	require.Eventually(t, func() bool {
		current := ordinary2.GetChannel(channelID)
		return current != nil && current.Status == wallet.CS_READY
	}, rpcReadyTimeout, 200*time.Millisecond, "confirmed BRC20 splice-in did not restore READY channel")
	waitRGB11RegtestPeerChannelReady(t, ordinary2, network, channelID)
	waitSDKPWARegtestSplicingAssetReady(t, ordinary2, network, channelID,
		spliceReservationID, anchorOutpoint, sdkPWARegtestWASMAsset, sdkPWARegtestWASMAmount)
	prepareSDKPWARegtestL2PlainFunding(t, ordinary1, ordinary2, network)
	waitRGB11RegtestPeerChannelReady(t, ordinary2, network, channelID)
	assetUnlock, _, err := ordinary2.UnlockFromChannel(channelID, ordinary2.GetWallet().GetAddress(), sdkPWARegtestWASMAsset,
		strconv.FormatInt(sdkPWARegtestWASMAmount, 10), nil, nil)
	require.NoError(t, err)
	waitRGB11RegtestL2Transaction(t, ordinary2, network, assetUnlock)
	waitRGB11RegtestPeerChannelReady(t, ordinary2, network, channelID)

	// Real BTC splice-in/unlock supplies the additional L2 plain fee/output
	// value needed for the exact 500k/500k/1m signed asset distribution.
	btcSpliceTxID, btcReservationID, err := ordinary2.SplicingIn(channelID, wallet.PLAIN_ASSET.String(),
		"3000000", nil, nil, nil, nil, 2, wallet.SPLICING_REASON_LOCAL)
	require.NoError(t, err)
	btcReservation, btcReservationFound := ordinary2.GetSplicingReservation(btcReservationID)
	require.True(t, btcReservationFound, "capture the actual BTC splice anchor before L1 confirmation")
	require.NotNil(t, btcReservation)
	btcAnchorTxID := btcReservation.AnchorTxId()
	require.NotEmpty(t, btcAnchorTxID)
	confirmSDKPWARegtestL1SDKTransaction(t, ordinary2, btcSpliceTxID, mine)
	require.Eventually(t, func() bool {
		current := ordinary2.GetChannel(channelID)
		return current != nil && current.Status == wallet.CS_READY
	}, rpcReadyTimeout, 200*time.Millisecond, "confirmed BTC splice-in did not restore READY channel")
	waitRGB11RegtestPeerChannelReady(t, ordinary2, network, channelID)
	waitSDKPWARegtestSplicingAssetReady(t, ordinary2, network, channelID,
		btcReservationID, btcAnchorTxID+":0", wallet.PLAIN_ASSET.String(), 3_000_000)
	plainUnlock, _, err := ordinary2.UnlockFromChannel(channelID, ordinary2.GetWallet().GetAddress(), wallet.PLAIN_ASSET.String(), "3000000", nil, nil)
	require.NoError(t, err)
	waitRGB11RegtestL2Transaction(t, ordinary2, network, plainUnlock)
	waitRGB11RegtestPeerChannelReady(t, ordinary2, network, channelID)

	// Let the existing SDK manager choose, index, sign, and broadcast the L2
	// distribution to the fixed synthetic identities used by the browser cases.
	assetName := indexercommon.NewAssetNameFromString(sdkPWARegtestWASMAsset)
	require.NotNil(t, assetName)
	destinations := []*wallet.SendAssetInfo{
		{Address: owner["address"], Value: 500_000, AssetName: assetName, AssetAmt: indexercommon.NewDecimal(12_500_000, 0)},
		{Address: owner["address"], Value: 500_000, AssetName: assetName, AssetAmt: indexercommon.NewDecimal(12_500_000, 0)},
		{Address: buyer["address"], Value: 1_000_000, AssetName: assetName, AssetAmt: indexercommon.NewDecimal(25_000_000, 0)},
	}
	baselineHash, baselineHeight, err := network.Bootstrap.Client.GetBestBlock()
	require.NoError(t, err)
	baselineBlock := blockAtHeight(t, network.Bootstrap, baselineHeight)
	submittedAt := time.Now()
	l2TxID, err := ordinary2.BatchSendAssetsV3_SatsNet(destinations, sdkPWARegtestWASMAsset, nil)
	require.NoError(t, err)
	require.NotEmpty(t, l2TxID)
	_, confirmedHeight, _, _ := waitForScheduledSatsNetTx(t, network, l2TxID, submittedAt,
		baselineHeight, baselineHash, baselineBlock.Header.Timestamp)
	require.Greater(t, confirmedHeight, baselineHeight)

	indexer := ordinary2.GetIndexerRPCClient_SatsNet()
	raw, err := indexer.GetRawTx(l2TxID)
	require.NoError(t, err)
	seed, err := wallet.DecodeMsgTx_SatsNet(raw)
	require.NoError(t, err)
	require.Equal(t, l2TxID, seed.TxID())
	ownerScript, err := hex.DecodeString(owner["pk_script"])
	require.NoError(t, err)
	buyerScript, err := hex.DecodeString(buyer["pk_script"])
	require.NoError(t, err)
	ownerValues, buyerValues := []int64{}, []int64{}
	for index, output := range seed.TxOut {
		point := fmt.Sprintf("%s:%d", l2TxID, index)
		isOwner := strings.EqualFold(hex.EncodeToString(output.PkScript), hex.EncodeToString(ownerScript))
		isBuyer := strings.EqualFold(hex.EncodeToString(output.PkScript), hex.EncodeToString(buyerScript))
		if !isOwner && !isBuyer {
			continue
		}
		indexed, outputErr := indexer.GetTxOutput(point)
		require.NoError(t, outputErr)
		require.NotNil(t, indexed)
		require.Equal(t, output.Value, indexed.OutValue.Value, "L2 indexer value must match parent transaction %s", point)
		require.Equal(t, output.PkScript, indexed.OutValue.PkScript, "L2 indexer script must match parent transaction %s", point)
		require.Equal(t, output.Assets, indexed.Assets, "L2 indexer asset metadata must match parent transaction %s", point)
		require.Len(t, indexed.Assets, 1)
		wantAmount := "12500000"
		if isBuyer {
			wantAmount = "25000000"
			buyerValues = append(buyerValues, output.Value)
		} else {
			ownerValues = append(ownerValues, output.Value)
		}
		require.Equal(t, wantAmount, indexed.Assets[0].Amount.String())
	}
	require.Equal(t, []int64{500_000, 500_000}, ownerValues)
	require.Equal(t, []int64{1_000_000}, buyerValues)
	return owner, buyer, posPWASDKPSBTFixture(t, seed, owner, buyer, sdkPWARegtestWASMAsset, indexer)
}
