package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/sat20wallet/sdk/wire"
	satschaincfg "github.com/sat20-labs/satoshinet/chaincfg"
	sindexercommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/stretchr/testify/require"
)

const sdkPWARegtestPOSV2ActivationHeight int32 = 32
const sdkPWARegtestPOSV2ReviewedStartHeight int32 = 20

const sdkPWARegtestL2PlainFundingAmount int64 = 10_000
const sdkPWARegtestGuardianRecoveryCase = "usage: real 2of3 settings and independent Guardian pages complete setup and rehearsal"
const sdkPWARegtestPaidAccountUsageCase = "usage: fixed regtest ordinary-1 renews paid storage through current AUTOPAY"

// Funds a fresh B0 address through the SDK's real L2 send path, then checks
// the same confirmed output in both the parent transaction and receiver indexer.
func prepareSDKPWARegtestL2PlainFunding(t *testing.T, sender, receiver *wallet.Manager,
	network *realSatoshiNet, requireSenderReady func(*testing.T, *wallet.Manager)) {
	t.Helper()
	attachRGB11RegtestWallet(t, sender, network)
	requireSenderReady(t, sender)
	plainAsset := wallet.ParseAssetString(wallet.PLAIN_ASSET.String())
	require.NotNil(t, plainAsset)
	senderBefore := sender.GetAssetBalance_SatsNet("", plainAsset)
	require.NotNil(t, senderBefore, "A0 must have indexed L2 plain funds before funding B0")
	require.GreaterOrEqual(t, senderBefore.Int64(),
		sdkPWARegtestL2PlainFundingAmount+wallet.DEFAULT_FEE_SATSNET,
		"A0 must cover the exact B0 amount and the L2 transaction fee")

	tx, err := sender.SendAssets_SatsNet(receiver.GetWallet().GetAddress(),
		wallet.PLAIN_ASSET.String(), strconv.FormatInt(sdkPWARegtestL2PlainFundingAmount, 10), nil)
	require.NoError(t, err, "A0 must fund B0 through a real SDK-signed L2 transaction")
	require.NotNil(t, tx)
	txid := tx.TxID()
	waitRGB11RegtestL2Transaction(t, sender, network, txid)

	raw, err := sender.GetIndexerRPCClient_SatsNet().GetRawTx(txid)
	require.NoError(t, err)
	parent, err := wallet.DecodeMsgTx_SatsNet(raw)
	require.NoError(t, err)
	require.Equal(t, txid, parent.TxID())
	receiverScript, err := wallet.GetPkScriptFromAddress_SatsNet(receiver.GetWallet().GetAddress())
	require.NoError(t, err)
	outputIndex := -1
	for i, output := range parent.TxOut {
		if !bytes.Equal(output.PkScript, receiverScript) {
			continue
		}
		require.Equal(t, sdkPWARegtestL2PlainFundingAmount, output.Value)
		require.Empty(t, output.Assets, "plain sats are carried by TxOut.Value, not token assets")
		require.Equal(t, -1, outputIndex, "the transfer must create one exact B0 output")
		outputIndex = i
	}
	require.NotEqual(t, -1, outputIndex, "confirmed parent transaction must pay B0 exactly once")

	outpoint := fmt.Sprintf("%s:%d", txid, outputIndex)
	indexed, err := receiver.GetIndexerRPCClient_SatsNet().GetTxOutput(outpoint)
	require.NoError(t, err)
	require.NotNil(t, indexed, "B0 indexer must expose the actual confirmed transfer output")
	require.Equal(t, outpoint, indexed.OutPointStr)
	require.Equal(t, sdkPWARegtestL2PlainFundingAmount, indexed.OutValue.Value)
	require.Equal(t, receiverScript, indexed.OutValue.PkScript)
	require.Equal(t, parent.TxOut[outputIndex].Assets, indexed.Assets)

	receiverAfter := receiver.GetAssetBalance_SatsNet("", plainAsset)
	require.NotNil(t, receiverAfter, "B0 plain balance must be indexed after the real transfer")
	require.GreaterOrEqual(t, receiverAfter.Int64(), sdkPWARegtestL2PlainFundingAmount,
		"B0 indexed balance must include the confirmed SDK transfer")
}

func sdkPWARegtestL2Indexer(t *testing.T, node *testHarness) *wallet.IndexerClient {
	t.Helper()
	endpoint, err := node.IndexerURL("regtest")
	require.NoError(t, err)
	parsed, err := url.Parse(endpoint)
	require.NoError(t, err)
	return wallet.NewIndexerClient(parsed.Scheme, parsed.Host, "regtest", wallet.NewHTTPClient())
}

func sdkPWARegtestL2PlainBalance(t *testing.T, client *wallet.IndexerClient, address string) int64 {
	t.Helper()
	summary := client.GetAssetSummaryWithAddress(address)
	if summary == nil {
		return 0
	}
	for _, asset := range summary.Data {
		if asset != nil && asset.Name == indexercommon.ASSET_PLAIN_SAT {
			return asset.Amount.Int64()
		}
	}
	return 0
}

// Prepare only the real L2 plain-sat fees needed by the RGB public route.
// Both transfers use the ordinary SDK send path and are confirmed/indexed by
// both test nodes before the browser starts.
func prepareSDKPWARegtestRGBCrossLayerPlainFunding(t *testing.T, source *wallet.Manager,
	deployerAddress string, network *realSatoshiNet) map[string]string {
	t.Helper()
	require.NotNil(t, source)
	require.NotNil(t, network)
	require.NotEmpty(t, deployerAddress)
	coreKey := keyFromMnemonic(t, coreMnemonic, 0)
	coreAddress := wallet.PublicKeyToP2TRAddress(coreKey.PubKey())
	corePubkey := hex.EncodeToString(coreKey.PubKey().SerializeCompressed())
	require.Equal(t, network.Core.nodePubKey, corePubkey,
		"fund the actual Core code-node address used by DeployContract")
	require.NotEqual(t, source.GetWallet().GetAddress(), coreAddress)
	require.NotEqual(t, source.GetWallet().GetAddress(), deployerAddress)
	require.NotEqual(t, coreAddress, deployerAddress)

	feeRate := source.GetFeeRate()
	require.Positive(t, feeRate, "use the live Bitcoin fee rate for the RGB withdrawal quote")
	feeAsset := wallet.ParseAssetString("rgb11:f:fee-quote@0000")
	require.NotNil(t, feeAsset)
	feeAssetName := &wallet.AssetName{AssetName: *feeAsset, N: 1}
	withdrawAssetAmount := indexercommon.NewDecimal(100, 0)
	withdrawQuote := wallet.WITHDRAW_INVOKE_FEE + wallet.CalcFee_SendTx(
		2, 3, 1, feeAssetName, withdrawAssetAmount, feeRate, true)
	deployQuote := wallet.DEFAULT_SERVICE_FEE_DEPLOY_CONTRACT + wallet.DEFAULT_FEE_SATSNET +
		wallet.INVOKE_FEE + wallet.DEFAULT_FEE_SATSNET
	// Reserve both full public operations: deployment and RGB withdrawal each
	// submit an L2 transaction with the SDK's 10-sat network fee.
	minimumDeployerBalance := deployQuote + wallet.DEFAULT_FEE_SATSNET +
		withdrawQuote + wallet.DEFAULT_FEE_SATSNET
	channelFeeBudget := 2 * wallet.DEFAULT_FEE_SATSNET // Unlock and Lock each spend one L2 fee.
	minimumDeployerBalance += channelFeeBudget
	minimumCoreBalance := int64(30) // Core's AllowDeploy reserve, separate from the deployer's fee.

	nodes := make(map[*testHarness]*wallet.IndexerClient, len(network.Nodes))
	for _, node := range network.Nodes {
		nodes[node] = sdkPWARegtestL2Indexer(t, node)
	}
	coreBefore := sdkPWARegtestL2PlainBalance(t, nodes[network.Core], coreAddress)
	deployerBefore := sdkPWARegtestL2PlainBalance(t, nodes[network.Core], deployerAddress)
	for _, node := range network.Nodes {
		require.Equal(t, coreBefore, sdkPWARegtestL2PlainBalance(t, nodes[node], coreAddress),
			"all live node indexes must agree on Core's starting plain balance")
		require.Equal(t, deployerBefore, sdkPWARegtestL2PlainBalance(t, nodes[node], deployerAddress),
			"all live node indexes must agree on ordinary-2's starting plain balance")
	}
	coreGap := max(minimumCoreBalance-coreBefore, int64(0))
	deployerGap := max(minimumDeployerBalance-deployerBefore, int64(0))
	topupCount := 0
	if coreGap > 0 {
		topupCount++
	}
	if deployerGap > 0 {
		topupCount++
	}
	plainAsset := wallet.ParseAssetString(wallet.PLAIN_ASSET.String())
	require.NotNil(t, plainAsset)
	sourceAddress := source.GetWallet().GetAddress()
	sourceBefore := source.GetAssetBalance_SatsNet(sourceAddress, plainAsset)
	require.NotNil(t, sourceBefore, "ordinary-1 must read its existing live L2 plain balance")
	spendable := int64(0)
	for _, output := range source.GetIndexerRPCClient_SatsNet().GetUtxoListWithTicker(sourceAddress, plainAsset) {
		if output == nil || source.IsLocked_SatsNet(sourceAddress, output.OutPoint) {
			continue
		}
		spendable += output.Value
	}
	totalFunding := coreGap + deployerGap
	fees := int64(topupCount) * wallet.DEFAULT_FEE_SATSNET
	require.GreaterOrEqual(t, spendable, totalFunding+fees,
		"ordinary-1's confirmed, unlocked L2 plain outputs must cover both exact gaps and SDK network fees")

	fund := func(label, address string, amount, startingBalance int64) {
		if amount == 0 {
			return
		}
		senderBefore := sdkPWARegtestL2PlainBalance(t, nodes[network.Core], sourceAddress)
		receiverBefore := sdkPWARegtestL2PlainBalance(t, nodes[network.Core], address)
		require.Equal(t, startingBalance, receiverBefore)
		tx, err := source.SendAssets_SatsNet(address, wallet.PLAIN_ASSET.String(), strconv.FormatInt(amount, 10), nil)
		require.NoError(t, err, "ordinary-1 must fund %s through the real SDK L2 transfer path", label)
		require.NotNil(t, tx)
		txid := tx.TxID()
		waitRGB11RegtestL2Transaction(t, source, network, txid)
		receiverScript, err := wallet.GetPkScriptFromAddress_SatsNet(address)
		require.NoError(t, err)
		outputIndex := -1
		var outputTotal int64
		for i, output := range tx.TxOut {
			outputTotal += output.Value
			require.Empty(t, output.Assets, "%s funding must transfer plain sats only", label)
			if !bytes.Equal(output.PkScript, receiverScript) {
				continue
			}
			require.Equal(t, amount, output.Value, "%s funding output must be exact", label)
			require.Equal(t, -1, outputIndex, "%s transfer must have one exact recipient output", label)
			outputIndex = i
		}
		require.NotEqual(t, -1, outputIndex, "%s transfer must pay its exact recipient", label)
		inputTotal := int64(0)
		for _, input := range tx.TxIn {
			parentID := input.PreviousOutPoint.Hash.String()
			parentRaw, err := nodes[network.Core].GetRawTx(parentID)
			require.NoError(t, err, "Core indexer must retain the L2 funding parent %s", parentID)
			parent, err := wallet.DecodeMsgTx_SatsNet(parentRaw)
			require.NoError(t, err)
			require.Less(t, int(input.PreviousOutPoint.Index), len(parent.TxOut))
			inputTotal += parent.TxOut[input.PreviousOutPoint.Index].Value
		}
		actualFee := inputTotal - outputTotal
		require.Equal(t, wallet.DEFAULT_FEE_SATSNET, actualFee,
			"the SDK L2 transfer must pay only its configured network fee")

		outpoint := fmt.Sprintf("%s:%d", txid, outputIndex)
		var confirmedHeight int64
		var confirmedBlock string
		for _, node := range network.Nodes {
			client := nodes[node]
			info, err := client.GetTxInfo(txid)
			require.NoError(t, err, "%s indexer must confirm the exact L2 funding transaction", node.role)
			require.NotNil(t, info)
			require.Equal(t, txid, info.TxID)
			require.GreaterOrEqual(t, info.Confirmations, uint64(1))
			if confirmedHeight == 0 {
				confirmedHeight = info.BlockHeight
			} else {
				require.Equal(t, confirmedHeight, info.BlockHeight,
					"Core and Bootstrap must index the funding tx at the same confirmed height")
			}
			blockHash, err := client.GetBlockHash(int(info.BlockHeight))
			require.NoError(t, err)
			if confirmedBlock == "" {
				confirmedBlock = blockHash
			} else {
				require.Equal(t, confirmedBlock, blockHash,
					"Core and Bootstrap must confirm the funding tx in the same block")
			}
			indexed, err := client.GetTxOutput(outpoint)
			require.NoError(t, err, "%s indexer must expose the exact confirmed recipient output", node.role)
			require.NotNil(t, indexed)
			require.Equal(t, outpoint, indexed.OutPointStr)
			require.Equal(t, amount, indexed.OutValue.Value)
			require.Equal(t, receiverScript, indexed.OutValue.PkScript)
			require.Empty(t, indexed.Assets)
			require.Equal(t, amount, sdkPWARegtestL2PlainBalance(t, client, address)-receiverBefore,
				"%s balance delta must match its exact confirmed transfer", label)
			raw, err := client.GetRawTx(txid)
			require.NoError(t, err)
			confirmedRaw, err := wallet.DecodeMsgTx_SatsNet(raw)
			require.NoError(t, err)
			require.Equal(t, tx.TxID(), confirmedRaw.TxID(),
				"each node's indexer must return the same signed transaction")
		}
		require.Equal(t, senderBefore-amount-actualFee,
			sdkPWARegtestL2PlainBalance(t, nodes[network.Core], sourceAddress),
			"ordinary-1 balance decrease must equal the exact payment plus actual fee")
		t.Logf("RGB cross-layer L2 funding %s: txid=%s height=%d block=%s recipient=%s amount=%d fee=%d",
			label, txid, confirmedHeight, confirmedBlock, address, amount, actualFee)
	}
	fund("Core deployment reserve", coreAddress, coreGap, coreBefore)
	fund("ordinary-2 public and private route budget", deployerAddress, deployerGap, deployerBefore)
	coreAfter := sdkPWARegtestL2PlainBalance(t, nodes[network.Core], coreAddress)
	deployerAfter := sdkPWARegtestL2PlainBalance(t, nodes[network.Core], deployerAddress)
	require.GreaterOrEqual(t, coreAfter, minimumCoreBalance)
	require.GreaterOrEqual(t, deployerAfter, minimumDeployerBalance)
	for _, node := range network.Nodes {
		require.Equal(t, coreAfter, sdkPWARegtestL2PlainBalance(t, nodes[node], coreAddress))
		require.Equal(t, deployerAfter, sdkPWARegtestL2PlainBalance(t, nodes[node], deployerAddress))
	}
	require.Equal(t, sourceBefore.Int64()-totalFunding-fees,
		sdkPWARegtestL2PlainBalance(t, nodes[network.Core], sourceAddress),
		"ordinary-1 must pay exactly the Core and ordinary-2 gaps plus both L2 fees")
	t.Logf("RGB cross-layer L2 fee budget: fee_rate=%d deploy_quote=%d withdraw_quote=%d core=%d/%d ordinary2=%d/%d sender=%d->%d",
		feeRate, deployQuote, withdrawQuote, coreAfter, minimumCoreBalance,
		deployerAfter, minimumDeployerBalance, sourceBefore.Int64(),
		sdkPWARegtestL2PlainBalance(t, nodes[network.Core], sourceAddress))
	return map[string]string{
		"core_address": coreAddress, "core_before": strconv.FormatInt(coreBefore, 10),
		"core_after": strconv.FormatInt(coreAfter, 10), "core_minimum": strconv.FormatInt(minimumCoreBalance, 10),
		"deployer_address": deployerAddress, "deployer_before": strconv.FormatInt(deployerBefore, 10),
		"deployer_after":     strconv.FormatInt(deployerAfter, 10),
		"deployer_minimum":   strconv.FormatInt(minimumDeployerBalance, 10),
		"channel_fee_budget": strconv.FormatInt(channelFeeBudget, 10),
		"withdraw_fee_rate":  strconv.FormatInt(feeRate, 10),
		"withdraw_fee_quote": strconv.FormatInt(withdrawQuote, 10),
		"withdraw_amount":    withdrawAssetAmount.String(),
		"ordinary1_before":   strconv.FormatInt(sourceBefore.Int64(), 10),
		"ordinary1_after":    strconv.FormatInt(sdkPWARegtestL2PlainBalance(t, nodes[network.Core], sourceAddress), 10),
		"funding_fee_each":   strconv.FormatInt(wallet.DEFAULT_FEE_SATSNET, 10),
	}
}

func prepareSDKPWARegtestBRC20BalanceAsset(t *testing.T, manager *wallet.Manager, mine func(string, int)) {
	t.Helper()
	asset := indexercommon.NewAssetNameFromString("brc20:f:pwbx")
	require.NotNil(t, asset)
	if manager.GetIndexerClient().GetTickInfo(asset) == nil {
		deployed, err := manager.DeployTicker_brc20(asset.Ticker, 20, 20, 0, 2)
		require.NoError(t, err, "SDK deploy the real BRC20 balance-send test ticker")
		require.NotNil(t, deployed)
		require.NotNil(t, deployed.CommitTx)
		require.NotNil(t, deployed.RevealTx)
		confirmSDKPWARegtestL1SDKTransaction(t, manager, deployed.CommitTx.TxID(), mine)
	}
	require.Eventually(t, func() bool {
		ticker := manager.GetIndexerClient().GetTickInfo(asset)
		return ticker != nil && ticker.MaxSupply == "20" && ticker.Limit == "20" &&
			ticker.Divisibility == 0
	}, rpcReadyTimeout, 200*time.Millisecond, "real L1 indexer must expose the 20-token pwbx deployment")
	balance := manager.GetAssetBalance("", asset)
	if balance == nil || balance.Sign() == 0 {
		ticker := manager.GetIndexerClient().GetTickInfo(asset)
		require.NotNil(t, ticker)
		require.Equal(t, "0", ticker.TotalMinted, "fresh balance-send ticker must not be pre-minted")
		minted, err := manager.MintAsset_brc20(manager.GetWallet().GetAddress(), asset,
			indexercommon.NewDecimal(20, 0), nil, 2)
		require.NoError(t, err, "SDK mint the real 20-token balance-send fixture")
		require.NotNil(t, minted)
		require.NotNil(t, minted.CommitTx)
		require.NotNil(t, minted.RevealTx)
		confirmSDKPWARegtestL1SDKTransaction(t, manager, minted.CommitTx.TxID(), mine)
	} else {
		require.Equal(t, "20", balance.String(), "reuse only the exact fresh balance-send supply")
	}
	require.Eventually(t, func() bool {
		ticker := manager.GetIndexerClient().GetTickInfo(asset)
		balance := manager.GetAssetBalance("", asset)
		return ticker != nil && ticker.TotalMinted == "20" && balance != nil && balance.String() == "20"
	}, rpcReadyTimeout, 200*time.Millisecond, "real L1 indexer must expose exactly 20 minted pwbx units")
	transfers := manager.GetIndexerClient().GetUtxoListWithTicker(manager.GetWallet().GetAddress(), asset)
	require.Empty(t, transfers, "balance-send preparation must leave transfer inscriptions for the original UI case")
}

type sdkPWARegtestEndpoint struct {
	Scheme string
	Host   string
	Proxy  string
}

type sdkPWARegtestConfig struct {
	Env       string
	Chain     string
	Mode      string
	Log       string
	Peers     []string
	IndexerL1 sdkPWARegtestEndpoint
	IndexerL2 sdkPWARegtestEndpoint
}

// This browser entry only reuses the existing nodes-anchor chain and existing
// ordinary-wallet data. It never calls the deposit/mining path or creates L1
// funds; every shown balance is read from the live SatoshiNet summary.

// Keep Guardian setup/rehearsal in its own local chain and browser-storage group.

// The reusable gate opens only when absent; an existing READY channel is
// verified through the UI without closing it merely to repeat first opening.

func configureSDKPWARegtestPOSV2(t *testing.T) {
	t.Helper()
	previousRegressionHeight := satschaincfg.RegressionNetParams.POSV2Height
	previousTestHeight := satschaincfg.TestNetParams.POSV2Height
	t.Setenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT", strconv.Itoa(int(sdkPWARegtestPOSV2ActivationHeight)))
	satschaincfg.RegressionNetParams.POSV2Height = sdkPWARegtestPOSV2ActivationHeight
	satschaincfg.TestNetParams.POSV2Height = sdkPWARegtestPOSV2ActivationHeight
	t.Cleanup(func() {
		satschaincfg.RegressionNetParams.POSV2Height = previousRegressionHeight
		satschaincfg.TestNetParams.POSV2Height = previousTestHeight
	})
}

func TestSDKPWARealRegtestV2AssetPreparation(t *testing.T) {
	configureSDKPWARegtestPOSV2(t)
	network := prepareSDKPWARegtestNodes(t)
	a, b, mine := prepareSDKPWARegtestAssets(t, true)
	active, err := b.AnchorOutputsActive()
	require.NoError(t, err)
	require.True(t, active, "the activation case in this group must finish before V2 asset preparation")
	attachRGB11RegtestWallet(t, a, network)
	requireE2EAccountWrite(t, a)
	prepareSDKPWARegtestChannelV2Asset(t, a, b, mine)
}

func verifySDKPWARegtestResumeDeposit(t *testing.T, network *realSatoshiNet, manager *wallet.Manager,
	walletScript []byte) map[string]string {
	t.Helper()
	const fundingTxID = "cbda1b88f36cabd6e29c367827c29284f7dc667f18cc1c6e46b39899414c5342"
	const anchorTxID = "700727cc3291703df162100dfda257368b748b598f4bade2077ad56dbff8d01d"
	const expectedCredit = int64(10000)

	coreRaw := strings.TrimSpace(string(realRegtestBitcoinCLI(t, "getrawtransaction", fundingTxID, "false")))
	funding, err := wallet.DecodeMsgTx(coreRaw)
	require.NoError(t, err)
	require.Equal(t, fundingTxID, funding.TxHash().String())
	indexedRaw, err := manager.GetIndexerClient().GetRawTx(fundingTxID)
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(coreRaw), strings.ToLower(indexedRaw),
		"V84 funding must be the actual witness-bearing transaction retained by both Core and the L1 indexer")
	info, err := manager.GetIndexerClient().GetTxInfo(fundingTxID)
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Equal(t, fundingTxID, info.TxID)
	require.Greater(t, info.Confirmations, uint64(0))
	require.Greater(t, info.BlockHeight, int64(0))
	coreBlockHash := strings.TrimSpace(string(realRegtestBitcoinCLI(t, "getblockhash", strconv.FormatInt(info.BlockHeight, 10))))
	indexedBlockHash, err := manager.GetIndexerClient().GetBlockHash(int(info.BlockHeight))
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(coreBlockHash), strings.ToLower(indexedBlockHash),
		"V84 L1 confirmation must identify the same canonical Core block")

	snapshot, err := readSDKPWARegtestSatoshiNetSnapshot(network, 0)
	require.NoError(t, err)
	var receipt *posPWAAnchorEvidence
	for i := range snapshot.Anchors {
		anchor := &snapshot.Anchors[i]
		if anchor.TxID == anchorTxID {
			receipt = anchor
			break
		}
	}
	require.NotNil(t, receipt, "V84 SatoshiNet anchor must exist in the current canonical chain")
	require.Equal(t, int32(41), receipt.Height)
	require.True(t, strings.HasPrefix(receipt.FundingOutpoint, fundingTxID+":"),
		"V84 Anchor must reference the real confirmed funding transaction")
	var credit int64
	for _, output := range receipt.Outputs {
		pkScript, decodeErr := hex.DecodeString(output.PkScript)
		require.NoError(t, decodeErr)
		if !bytes.Equal(pkScript, walletScript) {
			continue
		}
		require.Empty(t, output.Assets, "V84 receipt is the BTC deposit, not an asset credit")
		credit += output.Value
	}
	require.Equal(t, expectedCredit, credit, "V84 anchor must retain the exact real B0 BTC credit")
	base, err := network.Core.IndexerURL("regtest")
	require.NoError(t, err)
	var ascend struct {
		Code int                        `json:"code"`
		Msg  string                     `json:"msg"`
		Data *sindexercommon.AscendData `json:"data"`
	}
	require.NoError(t, posPWAReadJSON(base+"/v3/ascend/"+url.PathEscape(receipt.FundingOutpoint), &ascend))
	require.Zero(t, ascend.Code, ascend.Msg)
	require.NotNil(t, ascend.Data)
	require.Equal(t, receipt.FundingOutpoint, ascend.Data.FundingUtxo)
	require.Equal(t, anchorTxID, ascend.Data.AnchorTxId)
	require.Equal(t, int(receipt.Height), ascend.Data.Height)
	require.Equal(t, expectedCredit, ascend.Data.Value)
	return map[string]string{"funding_txid": fundingTxID, "anchor_txid": anchorTxID,
		"funding_outpoint": receipt.FundingOutpoint, "height": strconv.Itoa(int(receipt.Height)),
		"amount": strconv.FormatInt(expectedCredit, 10)}
}

func logSDKPWARegtestOverlay(t *testing.T) {
	t.Helper()
	flags := strings.Fields(os.Getenv("GOFLAGS"))
	var overlayPath string
	for i, flag := range flags {
		switch {
		case strings.HasPrefix(flag, "-overlay="):
			overlayPath = strings.TrimPrefix(flag, "-overlay=")
		case flag == "-overlay" && i+1 < len(flags):
			overlayPath = flags[i+1]
		}
		if overlayPath != "" {
			break
		}
	}
	if overlayPath == "" {
		t.Log("private regtest source overlay: none; using the current workspace sources")
		return
	}
	require.True(t, filepath.IsAbs(overlayPath), "Go source overlay path must be absolute")
	data, err := os.ReadFile(overlayPath)
	require.NoError(t, err)
	hash := sha256.Sum256(data)
	t.Logf("private regtest source overlay sha256=%x", hash)
}

func requireBrowserSDKPWARegtestOrigin(t *testing.T, network *realSatoshiNet) {
	t.Helper()
	for _, node := range network.Nodes {
		request, err := http.NewRequest(http.MethodOptions,
			"http://"+node.stpAddr+"/regtest"+wire.STP_ACTION_NFTY, nil)
		require.NoError(t, err)
		request.Header.Set("Origin", sdkPWARegtestBrowserOrigin(t))
		request.Header.Set("Access-Control-Request-Method", http.MethodPost)
		request.Header.Set("Access-Control-Request-Headers", "content-type")
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
		require.NoError(t, err)
		response.Body.Close()
		require.Equal(t, http.StatusNoContent, response.StatusCode, "STP browser preflight failed for %s", node.role)
		require.Equal(t, sdkPWARegtestBrowserOrigin(t), response.Header.Get("Access-Control-Allow-Origin"))
	}
}

func writeSDKPWARegtestPrivateJSON(t *testing.T, dir, name string, value any) string {
	t.Helper()
	encoded, err := json.MarshalIndent(value, "", "  ")
	require.NoError(t, err)
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, encoded, 0o600))
	require.NoError(t, os.Chmod(path, 0o600))
	return path
}
