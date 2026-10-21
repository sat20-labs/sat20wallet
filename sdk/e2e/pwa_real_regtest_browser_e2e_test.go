package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/sat20wallet/sdk/wire"
	satschaincfg "github.com/sat20-labs/satoshinet/chaincfg"
	sindexercommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/stretchr/testify/require"
)

const sdkPWARegtestPOSV2ActivationHeight int32 = 32
const sdkPWARegtestPOSV2ReviewedStartHeight int32 = 20

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
func TestSDKPWARealRegtestWalletBasicAndBiometricBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "basic")
}

func TestSDKPWARealRegtestFixedAccountManagementBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "account-usage")
}

func TestSDKPWARealRegtestRGBBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "rgb")
}

func TestSDKPWARealRegtestRGBContinuationBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "rgb-resume")
}

func TestSDKPWARealRegtestRGBRemainingBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "rgb-remaining")
}

func TestSDKPWARealRegtestRGBCancellationDiagnosticBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "rgb-cancel-diagnostic")
}

func TestSDKPWARealRegtestRGBReadbackBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "rgb-readback")
}

func TestSDKPWARealRegtestRGBIssueDiagnosticBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "rgb-issue")
}

func TestSDKPWARealRegtestDappBroadcastContinuationBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "dapp-resume")
}

func TestSDKPWARealRegtestBRC20ExistingTransferSendBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "l1-brc-transfer")
}

func TestSDKPWARealRegtestRunesDecimalSendBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "l1-runes")
}

func TestSDKPWARealRegtestRunesPremineReturnBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "l1-runes-return")
}

func TestSDKPWARealRegtestRunesOpenMintSendBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "l1-runes-mint")
}

func TestSDKPWARealRegtestRunesOpenMintReturnBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "l1-runes-mint-return")
}

func TestSDKPWARealRegtestBitcoinFundsSendBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "l1-funds")
}

func TestSDKPWARealRegtestBitcoinFundsContinuationBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "l1-funds-resume")
}

func TestSDKPWARealRegtestSatoshiNetPlainSendBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "l2-plain-send")
}

func TestSDKPWARealRegtestChannelOpeningBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "l2-channel-open")
}

// The reusable gate opens only when absent; an existing READY channel is
// verified through the UI without closing it merely to repeat first opening.
func TestSDKPWARealRegtestChannelOpeningOrReadyBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "l2-channel-opening-or-ready")
}

func TestSDKPWARealRegtestChannelSplicingBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "l2-channel-splice")
}

func TestSDKPWARealRegtestChannelSafetyBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "l2-channel-safety")
}

func TestSDKPWARealRegtestPrivateBTCRoundTripBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "l2-channel-private-btc")
}

func TestSDKPWARealRegtestPrivateBTCSplicingOutBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "l2-channel-splice-out-btc")
}

func TestSDKPWARealRegtestChannelActivationBrowser(t *testing.T) {
	previousRegressionHeight := satschaincfg.RegressionNetParams.POSV2Height
	previousTestHeight := satschaincfg.TestNetParams.POSV2Height
	t.Setenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT", strconv.Itoa(int(sdkPWARegtestPOSV2ActivationHeight)))
	satschaincfg.RegressionNetParams.POSV2Height = sdkPWARegtestPOSV2ActivationHeight
	satschaincfg.TestNetParams.POSV2Height = sdkPWARegtestPOSV2ActivationHeight
	t.Cleanup(func() {
		satschaincfg.RegressionNetParams.POSV2Height = previousRegressionHeight
		satschaincfg.TestNetParams.POSV2Height = previousTestHeight
	})
	runSDKPWARealRegtestBrowser(t, "l2-channel-activation")
}

func TestSDKPWARealRegtestChannelV2AssetSplicingBrowser(t *testing.T) {
	previousRegressionHeight := satschaincfg.RegressionNetParams.POSV2Height
	previousTestHeight := satschaincfg.TestNetParams.POSV2Height
	t.Setenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT", strconv.Itoa(int(sdkPWARegtestPOSV2ActivationHeight)))
	satschaincfg.RegressionNetParams.POSV2Height = sdkPWARegtestPOSV2ActivationHeight
	satschaincfg.TestNetParams.POSV2Height = sdkPWARegtestPOSV2ActivationHeight
	t.Cleanup(func() {
		satschaincfg.RegressionNetParams.POSV2Height = previousRegressionHeight
		satschaincfg.TestNetParams.POSV2Height = previousTestHeight
	})
	runSDKPWARealRegtestBrowser(t, "l2-channel-v2-asset-splice")
}

func TestSDKPWARealRegtestChannelV2AssetSplicingDiagnosticBrowser(t *testing.T) {
	previousRegressionHeight := satschaincfg.RegressionNetParams.POSV2Height
	previousTestHeight := satschaincfg.TestNetParams.POSV2Height
	t.Setenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT", strconv.Itoa(int(sdkPWARegtestPOSV2ActivationHeight)))
	satschaincfg.RegressionNetParams.POSV2Height = sdkPWARegtestPOSV2ActivationHeight
	satschaincfg.TestNetParams.POSV2Height = sdkPWARegtestPOSV2ActivationHeight
	t.Cleanup(func() {
		satschaincfg.RegressionNetParams.POSV2Height = previousRegressionHeight
		satschaincfg.TestNetParams.POSV2Height = previousTestHeight
	})
	runSDKPWARealRegtestBrowser(t, "l2-channel-v2-asset-splice-diagnostic")
}

func TestSDKPWARealRegtestBRC20BalanceReadbackBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "l1-brc-readback")
}

func TestSDKPWARealRegtestBRC20BalanceSendBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "l1-brc-balance")
}

func TestSDKPWARealRegtestMintContinuationBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "mint-resume")
}

func TestSDKPWARealRegtestMintDIDContinuationBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "mint-did-resume")
}

func TestSDKPWARealRegtestMintPartialReviewBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "mint-partial")
}

func TestSDKPWARealRegtestMintBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "mint")
}

func TestSDKPWARealRegtestMiningBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "mining")
}

func TestSDKPWARealRegtestDappPermissionsBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "dapp")
}

func TestSDKPWARealRegtestPOSRotationContinuationBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "pos-resume")
}

func runSDKPWARealRegtestBrowser(t *testing.T, feature string) {
	logSDKPWARegtestOverlay(t)
	openingOrReady := feature == "l2-channel-opening-or-ready"
	if openingOrReady {
		feature = "l2-channel-open"
	}
	var channelPreparation sdkPWARegtestChannelPrepState
	posResume := feature == "pos-resume"
	v2AssetSplice := feature == "l2-channel-v2-asset-splice" || feature == "l2-channel-v2-asset-splice-diagnostic"

	network := prepareSDKPWARegtestNodes(t)
	_, currentHeight, err := network.Bootstrap.Client.GetBestBlock()
	require.NoError(t, err)
	require.GreaterOrEqual(t, currentHeight, int32(3), "reuse only the existing H1/H2/H3 regtest chain")
	if feature == "l2-channel-activation" {
		require.Equal(t, sdkPWARegtestPOSV2ReviewedStartHeight, currentHeight,
			"activation must reuse the reviewed nodes-anchor chain without advancing it first")
		require.Less(t, currentHeight, sdkPWARegtestPOSV2ActivationHeight,
			"the existing chain must still be below the reviewed POS v2 activation height")
		require.Equal(t, sdkPWARegtestPOSV2ActivationHeight, satschaincfg.RegressionNetParams.POSV2Height)
		require.Equal(t, sdkPWARegtestPOSV2ActivationHeight, satschaincfg.TestNetParams.POSV2Height)
		require.Equal(t, "32", os.Getenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT"))
	} else if v2AssetSplice {
		require.GreaterOrEqual(t, currentHeight, sdkPWARegtestPOSV2ActivationHeight,
			"v2 asset splicing must reuse the existing chain after POS v2 activation")
		require.Equal(t, sdkPWARegtestPOSV2ActivationHeight, satschaincfg.RegressionNetParams.POSV2Height)
		require.Equal(t, sdkPWARegtestPOSV2ActivationHeight, satschaincfg.TestNetParams.POSV2Height)
		require.Equal(t, "32", os.Getenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT"))
	} else if posResume {
		require.GreaterOrEqual(t, currentHeight, sdkPWARegtestPOSV2ActivationHeight,
			"selected POS continuation must reuse the existing chain after POS v2 activation")
		require.Equal(t, sdkPWARegtestPOSV2ActivationHeight, satschaincfg.RegressionNetParams.POSV2Height)
		require.Equal(t, sdkPWARegtestPOSV2ActivationHeight, satschaincfg.TestNetParams.POSV2Height)
		require.Equal(t, "32", os.Getenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT"))
	}
	for _, node := range network.Nodes {
		_, height, err := node.Client.GetBestBlock()
		require.NoError(t, err)
		require.Equal(t, currentHeight, height, "both existing regtest nodes must share the same height")
	}

	var ordinary1, ordinary2 *wallet.Manager
	var mine func(address string, blocks int)
	var closeWalletManager func(*wallet.Manager)
	if posResume {
		ordinary1, ordinary2, mine, closeWalletManager = prepareSDKPWARegtestAssetsWithClose(t, true)
		mine = func(address string, blocks int) { mineSDKPWARegtestL1WithoutWalletManager(t, address, blocks) }
	} else {
		ordinary1, ordinary2, mine = prepareSDKPWARegtestAssets(t, true)
	}
	if feature == "account-usage" {
		prepareSDKPWARegtestAccountUsageFunding(t, ordinary1, network, mine)
	}
	baselineHash, baselineHeight, err := network.Bootstrap.Client.GetBestBlock()
	require.NoError(t, err)
	for _, node := range network.Nodes {
		hash, height, err := node.Client.GetBestBlock()
		require.NoError(t, err)
		require.Equal(t, baselineHeight, height)
		require.Equal(t, *baselineHash, *hash)
		if os.Getenv("SAT20_REAL_SATOSHINET_ATTACH") == "" {
			require.NoError(t, node.Client.SetGenerate(true, 1), "restore the configured transaction-gated scheduled miner after startup")
		}
		mining, err := node.Client.GetGenerate()
		require.NoError(t, err)
		require.True(t, mining)
	}
	t.Cleanup(func() {
		for _, node := range network.Nodes {
			if node.Client == nil {
				continue
			}
			_, height, err := node.Client.GetBestBlock()
			if err != nil {
				t.Errorf("read final %s regtest tip: %v", node.role, err)
				continue
			}
			if height < baselineHeight {
				t.Errorf("final %s regtest height %d is below baseline %d", node.role, height, baselineHeight)
				continue
			}
			finalBaselineHash, err := node.Client.GetBlockHash(int64(baselineHeight))
			if err != nil {
				t.Errorf("read final %s regtest baseline hash: %v", node.role, err)
			} else if finalBaselineHash == nil || *finalBaselineHash != *baselineHash {
				t.Errorf("final %s regtest hash changed at baseline height %d", node.role, baselineHeight)
			}
			for blockHeight := baselineHeight + 1; blockHeight <= height; blockHeight++ {
				blockHash, err := node.Client.GetBlockHash(int64(blockHeight))
				if err != nil || blockHash == nil {
					t.Errorf("read %s regtest block hash at %d: %v", node.role, blockHeight, err)
					break
				}
				block, err := node.Client.GetBlock(blockHash)
				if err != nil {
					t.Errorf("read %s regtest block at %d: %v", node.role, blockHeight, err)
					break
				}
				if len(block.Transactions) <= 1 {
					t.Errorf("scheduled miner created an empty regtest block at height %d on %s", blockHeight, node.role)
				}
			}
		}
	})
	seeds := readSDKPWARegtestMnemonics(t)
	bootstrapKey := keyFromMnemonic(t, seeds["bootstrap"], 0)
	coreKey := keyFromMnemonic(t, seeds["core"], 0)
	witnessScript, anchorPkScript, err := wallet.GetP2WSHscript(
		bootstrapKey.PubKey().SerializeCompressed(), coreKey.PubKey().SerializeCompressed())
	require.NoError(t, err)

	anchorTx := findAnchorTx(t, blockAtHeight(t, network.Bootstrap, 1))
	require.Len(t, anchorTx.TxOut, 3, "existing H1 anchor has channel funding, ascending metadata, and channel ID outputs")
	plainTicker := ordinary1.GetTickerInfoV2(wallet.PLAIN_ASSET.String())
	require.NotNil(t, plainTicker, "the SDK plain ticker defines the H1 ascending metadata")
	wantAscending := fmt.Sprintf("%s-%s-%d-%d", plainTicker.AssetName.String(),
		plainTicker.MaxSupply, plainTicker.Divisibility, plainTicker.N)
	for outputIndex := 1; outputIndex <= 2; outputIndex++ {
		require.Equal(t, int64(0), anchorTx.TxOut[outputIndex].Value,
			"H1 metadata output must not carry channel funds")
		require.Empty(t, anchorTx.TxOut[outputIndex].Assets,
			"H1 metadata output must not carry assets")
	}
	ascendingType, ascendingData, err := sindexercommon.ReadDataFromNullDataScript(anchorTx.TxOut[1].PkScript)
	require.NoError(t, err)
	require.Equal(t, uint8(sindexercommon.CONTENT_TYPE_ASCENDING), ascendingType)
	require.Equal(t, wantAscending, string(ascendingData))
	channelIDType, channelIDData, err := sindexercommon.ReadDataFromNullDataScript(anchorTx.TxOut[2].PkScript)
	require.NoError(t, err)
	require.Equal(t, uint8(sindexercommon.CONTENT_TYPE_CHANNELID), channelIDType)
	anchorData, err := wallet.ParseStandardAnchorScript(anchorTx.TxIn[0].SignatureScript)
	require.NoError(t, err)
	require.Equal(t, witnessScript, anchorData.WitnessScript)
	require.Equal(t, anchorPkScript, anchorTx.TxOut[0].PkScript)
	fundingPoint := anchorData.Utxo
	parts := strings.Split(fundingPoint, ":")
	require.Len(t, parts, 2)
	vout64, err := strconv.ParseUint(parts[1], 10, 32)
	require.NoError(t, err)
	// This is historical provenance. A confirmed withdrawal may legitimately
	// spend the original L1 carrier without invalidating the existing H1/H2.
	coreFundingHex := strings.TrimSpace(string(realRegtestBitcoinCLI(t, "getrawtransaction", parts[0], "false")))
	fundingTx, err := wallet.DecodeMsgTx(coreFundingHex)
	require.NoError(t, err)
	require.Equal(t, parts[0], fundingTx.TxHash().String())
	require.Less(t, vout64, uint64(len(fundingTx.TxOut)))
	indexedFundingHex, err := ordinary1.GetIndexerClient().GetRawTx(parts[0])
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(coreFundingHex), strings.ToLower(indexedFundingHex),
		"indexer and Core must retain the same witness-bearing original funding bytes")
	fundingValue := fundingTx.TxOut[vout64].Value
	fundingScript := fundingTx.TxOut[vout64].PkScript
	require.Equal(t, anchorData.Value, fundingValue)
	require.Equal(t, anchorData.Value, anchorTx.TxOut[0].Value)
	require.Equal(t, anchorPkScript, fundingScript)
	fundingOutput := indexercommon.NewTxOutput(fundingValue)
	fundingOutput.OutPointStr = fundingPoint
	fundingOutput.OutValue.PkScript = fundingScript
	bitcoinTxInfo, err := ordinary1.GetIndexerClient().GetTxInfo(parts[0])
	require.NoError(t, err)
	require.NotNil(t, bitcoinTxInfo)
	require.Equal(t, parts[0], bitcoinTxInfo.TxID)
	require.Greater(t, bitcoinTxInfo.Confirmations, uint64(0))
	require.Greater(t, bitcoinTxInfo.BlockHeight, int64(0))
	coreBlockHash := strings.TrimSpace(string(realRegtestBitcoinCLI(t, "getblockhash", strconv.FormatInt(bitcoinTxInfo.BlockHeight, 10))))
	require.Len(t, coreBlockHash, 64)
	indexedBlockHash, err := ordinary1.GetIndexerClient().GetBlockHash(int(bitcoinTxInfo.BlockHeight))
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(coreBlockHash), strings.ToLower(indexedBlockHash),
		"L1 indexer confirmation height must identify the same Core block")

	// The full SatoshiNet node is the historical source for H1/H2. Export the
	// actual witness-bearing H2 bytes so the browser independently checks the
	// SatoshiNet indexer's raw transaction and confirmation metadata.
	confirmedAnchor, h2, h1BlockHash, h2BlockHash := verifyAnchorHistory(t, network, ordinary1,
		witnessScript, fundingPoint, fundingOutput)
	require.Equal(t, anchorTx.TxHash(), confirmedAnchor.TxHash())
	require.Equal(t, anchorTx.TxHash(), h2.TxIn[0].PreviousOutPoint.Hash)
	require.Equal(t, uint32(0), h2.TxIn[0].PreviousOutPoint.Index)
	require.Len(t, h2.TxOut, 1)
	require.Equal(t, anchorData.Value-1000, h2.TxOut[0].Value,
		"H2 must preserve the existing 1000-sat channel-spend fee")
	require.Equal(t, int64(1000), anchorData.Value-h2.TxOut[0].Value)
	prevOutput := confirmedAnchor.TxOut[0]
	fetcher := txscript.NewCannedPrevOutputFetcher(prevOutput.PkScript, prevOutput.Value, prevOutput.Assets)
	hashes := txscript.NewTxSigHashes(h2, fetcher)
	engine, err := txscript.NewEngine(prevOutput.PkScript, h2, 0, txscript.StandardVerifyFlags, nil,
		hashes, prevOutput.Value, prevOutput.Assets, fetcher)
	require.NoError(t, err)
	require.NoError(t, engine.Execute(), "the persisted H2 witness must satisfy the real two-key script")
	h2RawHex, err := wallet.EncodeMsgTx_SatsNet(h2)
	require.NoError(t, err)
	require.NotEmpty(t, h2RawHex)

	ascend, err := ordinary1.GetIndexerRPCClient_SatsNet().GetAscendData(fundingPoint)
	require.NoError(t, err)
	require.NotNil(t, ascend)
	require.Equal(t, fundingPoint, ascend.FundingUtxo)
	require.Equal(t, confirmedAnchor.TxID(), ascend.AnchorTxId)
	require.Equal(t, 1, ascend.Height)
	require.Equal(t, anchorData.Value, ascend.Value)
	_, channelAddresses, _, err := txscript.ExtractPkScriptAddrs(anchorPkScript, wallet.GetChainParam_SatsNet())
	require.NoError(t, err)
	require.Len(t, channelAddresses, 1)
	channelAddress := channelAddresses[0].EncodeAddress()
	require.Equal(t, channelAddress, string(channelIDData),
		"H1 channel ID metadata must match the channel address from its funding script")
	require.Equal(t, channelAddress, ascend.Address,
		"ascending history must retain the actual H1 channel address separately from its H2 recipient")
	ledger, err := ordinary1.GetIndexerRPCClient_SatsNet().GetChannelLedger(ascend.Address)
	require.NoError(t, err)
	var anchorLedgerEntry *sindexercommon.ChannelLedgerEntry
	for _, entry := range ledger {
		if entry != nil && entry.Direction == "ascending" && entry.Operation == "ascending" &&
			entry.L2TxId == confirmedAnchor.TxID() && entry.L1TxId == parts[0] {
			for _, outpoint := range entry.L1Outpoints {
				if outpoint == fundingPoint {
					anchorLedgerEntry = entry
					break
				}
			}
		}
	}
	require.NotNil(t, anchorLedgerEntry, "the existing L2 ledger must link H1 to the exact L1 outpoint")
	require.Equal(t, anchorData.Value, anchorLedgerEntry.Value)

	address := ordinary1.GetWallet().GetAddress()
	summary := ordinary1.GetIndexerRPCClient_SatsNet().GetAssetSummaryWithAddress(address)
	require.NotNil(t, summary, "read the actual current L2 address summary")
	var plainBalance string
	for _, asset := range summary.Data {
		if asset != nil && asset.Name == indexercommon.ASSET_PLAIN_SAT {
			plainBalance = asset.Amount.String()
			break
		}
	}
	require.NotEmpty(t, plainBalance)
	currentBalance := ordinary1.GetAssetBalance_SatsNet("", wallet.ParseAssetString(wallet.PLAIN_ASSET.String()))
	require.NotNil(t, currentBalance)
	require.Equal(t, plainBalance, currentBalance.String(), "SDK balance must match the live L2 summary")
	require.NotEqual(t, strconv.FormatInt(anchorData.Value, 10), plainBalance,
		"current wallet balance is a separate live summary, not the original H1 anchor principal")

	config := sdkPWARegtestConfig{
		Env: "dev", Chain: "regtest", Mode: "light", Log: "debug",
		Peers: []string{
			"b@" + network.Bootstrap.nodePubKey + "@http://" + network.Bootstrap.stpAddr + "/regtest",
			"s@" + network.Core.nodePubKey + "@http://" + network.Core.stpAddr + "/regtest",
		},
		IndexerL1: sdkPWARegtestEndpoint{Scheme: "http", Host: "127.0.0.1:30143", Proxy: "btc/regtest"},
		IndexerL2: sdkPWARegtestEndpoint{Scheme: "http", Host: "127.0.0.1:30262", Proxy: "regtest"},
	}
	l1URL, err := url.Parse(os.Getenv("SAT20_REAL_L1_INDEXER"))
	require.NoError(t, err)
	l1IP := net.ParseIP(l1URL.Hostname())
	require.True(t, l1URL.Scheme == "http" && l1IP != nil && l1IP.IsLoopback() && l1URL.Port() != "" && l1URL.User == nil)
	require.Equal(t, "/btc/regtest", strings.TrimRight(l1URL.Path, "/"))
	config.IndexerL1 = sdkPWARegtestEndpoint{Scheme: l1URL.Scheme, Host: l1URL.Host, Proxy: "btc/regtest"}
	bootstrapURL, err := network.Bootstrap.IndexerURL("regtest")
	require.NoError(t, err)
	coreURL, err := network.Core.IndexerURL("regtest")
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:30252/regtest", bootstrapURL)
	require.Equal(t, "http://127.0.0.1:30262/regtest", coreURL)
	requireBrowserSDKPWARegtestOrigin(t, network)

	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	sdk := filepath.Dir(filepath.Dir(source))
	pwa := filepath.Join(sdk, "..", "pwa")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if deadline, ok := t.Deadline(); ok {
		ctx, cancel = context.WithDeadline(context.Background(), deadline.Add(-20*time.Second))
		defer cancel()
	}
	var wasmActivation []int32
	if feature == "l2-channel-activation" || v2AssetSplice || os.Getenv("SAT20_REAL_SATOSHINET_ATTACH") != "" {
		wasmActivation = []int32{sdkPWARegtestPOSV2ActivationHeight}
	}
	wasmPath, wasmRuntimePath := buildPWAWalletRuntime(t, ctx, sdk, wasmActivation...)
	wasmBytes, err := os.ReadFile(wasmPath)
	require.NoError(t, err)
	runtimeBytes, err := os.ReadFile(wasmRuntimePath)
	require.NoError(t, err)
	wasmHash, runtimeHash := sha256.Sum256(wasmBytes), sha256.Sum256(runtimeBytes)
	t.Logf("private regtest WASM artifacts sha256 wallet=%x wasm_exec=%x", wasmHash, runtimeHash)

	// Stop SDK wallet writers before the browser opens its own ordinary-1
	// database. The browser only reads/reuses the existing on-chain state.
	actor := func(role string, manager *wallet.Manager) map[string]string {
		pkScript, err := wallet.GetPkScriptFromAddress(manager.GetWallet().GetAddress())
		require.NoError(t, err)
		return map[string]string{"mnemonic": seeds[role], "password": "regtest-acceptance-only",
			"address":   manager.GetWallet().GetAddress(),
			"pk_script": hex.EncodeToString(pkScript)}
	}
	var originalConsignment string
	if feature == "rgb-resume" || feature == "rgb-remaining" || feature == "rgb-cancel-diagnostic" {
		_, err := ordinary1.RecoverAccountManagementFromRootMnemonic(ctx, seeds["ordinary-1"], "regtest-acceptance-only")
		require.NoError(t, err)
		restored, err := ordinary1.GetRGB11State()
		require.NoError(t, err)
		var original *rgb11wallet.TransferState
		for _, transfer := range restored.Transfers {
			if transfer.Direction == "send" && transfer.WitnessTxID == "41a4379b9c9aad9b8ed2742e666a8321ee7739879fa65e1e5f66935b29bc2a0a" {
				require.Nil(t, original)
				original = transfer
			}
		}
		require.NotNil(t, original)
		store := rgb11wallet.NewProjectionStore(ordinary1.GetDB(), nil)
		require.NoError(t, store.SetScope(fmt.Sprintf("wallet-%d-account-%d-rgb11v2", ordinary1.GetCurrentWalletId(), ordinary1.GetCurrentAccountId())))
		material, err := store.LoadObject(original.ConsignmentHash)
		require.NoError(t, err)
		digest := sha256.Sum256(material)
		require.Equal(t, original.ConsignmentHash, hex.EncodeToString(digest[:]))
		originalConsignment = string(material)
	}
	actorA, actorB := actor("ordinary-1", ordinary1), actor("ordinary-2", ordinary2)
	confirmationAddress := ordinary1.GetWallet().GetAddressByIndex(1)
	if posResume {
		confirmationAddress = ordinary2.GetWallet().GetAddressByIndex(1)
	}
	var rgbSender, rgbReceiver, udaSender, udaReceiver map[string]string
	if feature == "rgb" {
		// Keep old account-0 intents intact. Independent RGB journeys use
		// fixed authorized scopes; an unresolved scope fails instead of rotating.
		for i, manager := range []*wallet.Manager{ordinary1, ordinary2} {
			_, err := manager.RecoverAccountManagementFromRootMnemonic(ctx, seeds[[]string{"ordinary-1", "ordinary-2"}[i]], "regtest-acceptance-only")
			require.NoError(t, err, "synchronize existing RGB account scopes before selecting acceptance actors")
		}
		original := make([]*rgb11wallet.RGB11State, 2)
		for i, manager := range []*wallet.Manager{ordinary1, ordinary2} {
			original[i], err = manager.GetRGB11State()
			require.NoError(t, err)
		}
		for _, index := range []uint32{2, 3} {
			for i, manager := range []*wallet.Manager{ordinary1, ordinary2} {
				require.NoError(t, manager.SwitchAccount(index))
				_, err := manager.RefreshRGB11State(ctx)
				require.NoError(t, err)
				state, err := manager.GetRGB11State()
				require.NoError(t, err)
				require.Equal(t, "ok", state.ConsistencyStatus, "RGB acceptance scope must be consistent; retain unresolved history on failure")
				for _, transfer := range state.Transfers {
					require.Contains(t, []string{"settled", "rejected", "failed", "conflicted"}, transfer.Status,
						"RGB acceptance scope has an unfinished transfer; do not cancel or replace it")
				}
				for _, reservation := range state.Reservations {
					require.Contains(t, []string{"settled", "rejected", "failed", "conflicted", "expired", "cancelled"}, reservation.Status,
						"RGB acceptance scope has an active reservation; preserve it")
				}
				t.Logf("isolated RGB browser actor ordinary-%d account=%d address=%s transfers=%d reservations=%d consistency=%s",
					i+1, index, manager.GetWallet().GetAddress(), len(state.Transfers), len(state.Reservations), state.ConsistencyStatus)
			}
			prepareSDKPWARegtestRGBBrowserCoins(t, ordinary1, ordinary2, mine)
			a, b := actor("ordinary-1", ordinary1), actor("ordinary-2", ordinary2)
			a["account_index"], b["account_index"] = strconv.Itoa(int(index)), strconv.Itoa(int(index))
			if index == 2 {
				rgbSender, rgbReceiver = a, b
			} else {
				udaSender, udaReceiver = a, b
			}
		}
		for i, manager := range []*wallet.Manager{ordinary1, ordinary2} {
			require.NoError(t, manager.SwitchAccount(0))
			preserved, err := manager.GetRGB11State()
			require.NoError(t, err)
			require.Equal(t, original[i].Transfers, preserved.Transfers, "account-0 transaction history must remain intact")
			require.Equal(t, original[i].Reservations, preserved.Reservations, "account-0 reservations must remain intact")
		}
	}
	if feature == "mint-partial" {
		_, err := ordinary1.RecoverAccountManagementFromRootMnemonic(ctx, seeds["ordinary-1"], "regtest-acceptance-only")
		require.NoError(t, err, "synchronize existing RGB ownership before native inscription coin selection")
		_, err = ordinary1.RefreshRGB11State(ctx)
		require.NoError(t, err)
		prepareSDKPWARegtestMintReviewAssets(t, ordinary1, mine)
	}
	var brc20TransferPoint string
	brc20Sender, brc20Recipient := actorA, actorB
	var runeAsset string
	var runeBalances [2]string
	if feature == "l1-brc-transfer" || strings.HasPrefix(feature, "l1-runes") || feature == "l1-funds" || feature == "l1-funds-resume" || strings.HasPrefix(feature, "l2-channel-") {
		_, err := ordinary1.RecoverAccountManagementFromRootMnemonic(ctx, seeds["ordinary-1"], "regtest-acceptance-only")
		require.NoError(t, err, "synchronize RGB carrier ownership before SDK inscription coin selection")
		_, err = ordinary1.RefreshRGB11State(ctx)
		require.NoError(t, err)
		if feature == "l1-brc-transfer" {
			source := ordinary1
			asset := &indexercommon.AssetName{Protocol: "brc20", Type: "f", Ticker: "pwbt"}
			if balance := ordinary1.GetAssetBalance("", asset); balance == nil || balance.Sign() == 0 {
				if peerBalance := ordinary2.GetAssetBalance("", asset); peerBalance != nil && peerBalance.String() == "250" {
					// A prior real send moves this fixed supply to the peer. Reuse
					// that holder rather than minting an already exhausted ticker.
					_, err := ordinary2.RecoverAccountManagementFromRootMnemonic(ctx, seeds["ordinary-2"], "regtest-acceptance-only")
					require.NoError(t, err, "synchronize the actual BRC20 holder's RGB carrier ownership")
					_, err = ordinary2.RefreshRGB11State(ctx)
					require.NoError(t, err)
					source, brc20Sender, brc20Recipient = ordinary2, actorB, actorA
				}
			}
			brc20TransferPoint = prepareSDKPWARegtestBRC20TransferAsset(t, source, mine)
		} else if strings.HasPrefix(feature, "l1-runes") {
			openMint := strings.Contains(feature, "-mint")
			runeAsset = prepareSDKPWARegtestRuneAsset(t, ordinary1, mine, openMint)
			name := indexercommon.NewAssetNameFromString(runeAsset)
			total := indexercommon.NewDecimal(0, 2)
			for i, actor := range []*wallet.Manager{ordinary1, ordinary2} {
				balance := actor.GetAssetBalance("", name)
				if balance == nil {
					balance = indexercommon.NewDecimal(0, 2)
				}
				runeBalances[i] = balance.String()
				total = total.AddAlignPrecision(balance)
			}
			require.Equal(t, "100", total.String(), "both fixed wallets must conserve the actual Rune supply")
		} else if strings.HasPrefix(feature, "l2-channel-") {
			active, err := ordinary1.AnchorOutputsActive()
			require.NoError(t, err)
			if feature == "l2-channel-activation" {
				require.Equal(t, "32", os.Getenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT"))
				require.Equal(t, sdkPWARegtestPOSV2ActivationHeight, wallet.GetChainParam_SatsNet().POSV2Height,
					"native SDK invoice selection must use the same activation height as the nodes")
				require.False(t, active, "the existing chain is below POS v2 activation before the controller advances it")
				balance := ordinary1.GetAssetBalance_SatsNet("", wallet.ParseAssetString(wallet.PLAIN_ASSET.String()))
				require.NotNil(t, balance, "ordinary-1 must read its real existing L2 plain balance before activation")
				require.Greater(t, balance.Int64(), int64(0), "activation blocks must use existing ordinary-1 L2 funds")
			} else if v2AssetSplice {
				require.Equal(t, "32", os.Getenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT"))
				require.Equal(t, sdkPWARegtestPOSV2ActivationHeight, wallet.GetChainParam_SatsNet().POSV2Height,
					"native SDK invoice selection must use the activated POS v2 height")
				require.True(t, active, "the existing chain must already be past POS v2 activation")
			} else if os.Getenv("SAT20_REAL_SATOSHINET_ATTACH") != "" {
				require.Equal(t, sdkPWARegtestPOSV2ActivationHeight, wallet.GetChainParam_SatsNet().POSV2Height)
				require.Equal(t, currentHeight >= sdkPWARegtestPOSV2ActivationHeight, active,
					"SDK activation must match the existing attached chain")
			} else {
				require.Empty(t, os.Getenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT"), "preserve the existing unscheduled regtest upgrade configuration")
				require.False(t, active, "this case reuses the existing unactivated chain")
			}
			// Ordinary-1 already owns the persistent preparation channel. Keep
			// it intact and use the other fixed ordinary wallet for first opening.
			_, err = ordinary2.RecoverAccountManagementFromRootMnemonic(ctx, seeds["ordinary-2"], "regtest-acceptance-only")
			require.NoError(t, err)
			_, err = ordinary2.RefreshRGB11State(ctx)
			require.NoError(t, err)
			_, channelPreparation = prepareSDKPWARegtestChannelPrerequisites(t, feature, ordinary1, ordinary2, mine)
			if v2AssetSplice {
				assetBalance := ordinary2.GetAssetBalance("", wallet.ParseAssetString("ordx:f:pwapos"))
				require.NotNil(t, assetBalance, "B0 must read its existing real PWAPOS balance from Bitcoin")
				require.GreaterOrEqual(t, assetBalance.Int64(), int64(1200),
					"reuse at least 1200 existing PWAPOS units; do not inject or recreate the asset")
				if feature == "l2-channel-v2-asset-splice-diagnostic" {
					asset := indexercommon.NewAssetNameFromString("ordx:f:pwapos")
					carriers := ordinary2.GetIndexerClient().GetUtxoListWithTicker(ordinary2.GetWallet().GetAddress(), asset)
					require.NotEmpty(t, carriers, "diagnostic preflight requires the existing indexed PWAPOS carrier")
					unspentCarrier := false
					for _, carrier := range carriers {
						require.NotNil(t, carrier)
						parts := strings.Split(carrier.OutPoint, ":")
						require.Len(t, parts, 2, "indexed PWAPOS carrier must identify an exact Bitcoin outpoint")
						vout, parseErr := strconv.ParseUint(parts[1], 10, 32)
						require.NoError(t, parseErr)
						_, _, unspent := bitcoinUnspentAt(t, parts[0], uint32(vout))
						if unspent {
							unspentCarrier = true
						}
					}
					require.True(t, unspentCarrier, "diagnostic preflight requires the original PWAPOS carrier to remain unspent")
				}
			}
		} else {
			prepareSDKPWARegtestORDXSendAsset(t, ordinary1, mine)
		}
		if strings.HasPrefix(feature, "l1-runes") {
			original := prepareSDKPWARegtestMixedRuneCarrier(t, ordinary1, mine)
			require.NotNil(t, original)
			require.Equal(t, int64(660), original.OutValue.Value)
			require.Len(t, original.Assets, 2)
			for _, asset := range original.Assets {
				switch asset.Name.Protocol {
				case "runes":
					require.Equal(t, "100", asset.Amount.String())
				case "ordx":
					require.Equal(t, "o", asset.Name.Type)
					require.Equal(t, "1", asset.Amount.String())
				default:
					t.Fatalf("unexpected mixed protection asset protocol %s", asset.Name.Protocol)
				}
			}
			point := strings.Split(original.OutPointStr, ":")
			require.Len(t, point, 2)
			checkProtected := func() {
				var unspent map[string]any
				require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "gettxout", point[0], point[1], "true"), &unspent))
				require.NotNil(t, unspent, "the exact mixed Rune/NFT carrier must remain unspent")
			}
			checkProtected()
			t.Cleanup(checkProtected)
		}
	}
	var privateChannelID string
	if strings.HasPrefix(feature, "l2-channel-") || posResume {
		privateChannelID, err = ordinary2.GetChannelAddress()
		require.NoError(t, err)
		require.NotEmpty(t, privateChannelID)
	}
	var resumeDeposit map[string]string
	var posController *sdkPWARegtestPOSController
	ordinary1Closed := false
	if posResume {
		// Close ordinary-1's account-0 owner before A1 opens the exact Miner
		// plugin DB. The PWA actor and native transaction owner are B0 only.
		closeWalletManager(ordinary1)
		ordinary1Closed = true
		runtimeDir := os.Getenv("SAT20_REAL_REGTEST_RUNTIME")
		minerDir := filepath.Join(runtimeDir, "nodes-anchor", "miner")
		minerManager, minerPubkey, closeMinerManager := preseedSDKPWARegtestMinerWallet(t,
			network, minerDir, seeds["ordinary-1"])
		registerSDKPWARegtestMiner(t, minerManager, network)
		closeMinerManager()
		startSDKPWARegtestMinerNode(t, network, seeds["ordinary-1"], minerPubkey)
		b0Script, err := wallet.GetPkScriptFromAddress(actorB["address"])
		require.NoError(t, err)
		resumeDeposit = verifySDKPWARegtestResumeDeposit(t, network, ordinary2, b0Script)
		posController = newSDKPWARegtestPOSController(t, network, ordinary2, minerPubkey)
	}
	if feature != "l2-channel-activation" && !ordinary1Closed {
		ordinary1.Stop()
	}
	ordinary2.Stop()

	artifactDir := t.TempDir()
	configPath := writeSDKPWARegtestPrivateJSON(t, artifactDir, "config.json", config)
	fixture := map[string]any{
		"remoteAttach": os.Getenv("SAT20_REAL_SATOSHINET_ATTACH") != "",
		"anchorEvidence": map[string]any{
			"source": "bitcoin-l1", "txid": parts[0], "vout": uint32(vout64),
			"confirmationHeight": bitcoinTxInfo.BlockHeight, "blockHash": strings.ToLower(coreBlockHash),
			"valueSats": fundingValue, "scriptPubKey": hex.EncodeToString(fundingScript),
			"l2Credit": map[string]any{
				"fundingOutpoint": fundingPoint, "anchorTxid": confirmedAnchor.TxID(),
				"anchorHeight": ascend.Height, "anchorValueSats": ascend.Value,
				"channelAddress": channelAddress, "recipientAddress": address,
				"h2Txid": h2.TxID(), "h2Height": 2, "h2RawHex": h2RawHex,
				"h2InputOutpoint": fmt.Sprintf("%s:%d", h2.TxIn[0].PreviousOutPoint.Hash.String(), h2.TxIn[0].PreviousOutPoint.Index),
				"h2OutputSats":    h2.TxOut[0].Value, "h2FeeSats": anchorData.Value - h2.TxOut[0].Value,
				"h2BlockHash": h2BlockHash, "anchorBlockHash": h1BlockHash,
				"assetKey": wallet.PLAIN_ASSET.String(), "currentBalance": plainBalance,
			},
		},
		"walletFixture": map[string]any{
			"browserPort": sdkPWARegtestBrowserPort(t),
			"basicWallet": map[string]string{
				"mnemonic": seeds["ordinary-1"], "password": "regtest-acceptance-only", "address": address,
			},
			"wasmPath": wasmPath, "wasmRuntimePath": wasmRuntimePath,
		},
	}
	if existing := os.Getenv("SAT20_REAL_PWA_EXISTING_URL"); existing != "" {
		require.NotEmpty(t, os.Getenv("SAT20_REAL_SATOSHINET_ATTACH"))
		require.Equal(t, sdkPWARegtestBrowserOrigin(t)+"/", existing)
		fixture["walletFixture"].(map[string]any)["existingPwaURL"] = existing
	}
	if feature == "account-usage" {
		prepared := fixture["walletFixture"].(map[string]any)
		prepared["fixedAccountManagement"] = map[string]any{
			"owner":    map[string]string{"role": "ordinary-1", "mnemonic": actorA["mnemonic"], "password": "123456", "address": actorA["address"]},
			"guardian": map[string]string{"role": "ordinary-2", "mnemonic": actorB["mnemonic"], "password": "123456", "address": actorB["address"]},
		}
	}
	if posResume {
		prepared := fixture["walletFixture"].(map[string]any)
		prepared["mnemonic"], prepared["password"], prepared["address"], prepared["pk_script"] =
			actorB["mnemonic"], actorB["password"], actorB["address"], actorB["pk_script"]
		prepared["recipient"] = actorA
		prepared["asset"] = map[string]string{"key": "ordx:f:pwapos", "type": "ORDX", "ticker": "pwapos"}
		prepared["amounts"] = map[string]string{"opening": "1000000", "splicing_btc": "100000",
			"splicing_asset": "700", "deposit_btc": "10000", "deposit_asset": "600"}
		prepared["realBitcoinControl"], prepared["realChannel"], prepared["resumePrivateChannel"] = true, true, true
		prepared["realSatoshiNetFunds"], prepared["pos_v2_active"], prepared["pos_activation_scheduled"] = true, true, false
		prepared["posProducerRotation"], prepared["node_count"] = true, 3
		prepared["activation_height"], prepared["privateChannelID"] = sdkPWARegtestPOSV2ActivationHeight, privateChannelID
		prepared["resumeDeposit"] = resumeDeposit
		prepared["control_url"] = startSDKPWARegtestPOSController(t, confirmationAddress, mine, posController, network)
	}
	if strings.HasPrefix(feature, "rgb") {
		controller := startSDKPWARegtestBitcoinController(t, confirmationAddress, mine)
		fixture["walletFixture"].(map[string]any)["recipient"] = actorB
		fixture["walletFixture"].(map[string]any)["udaWallets"] = map[string]any{"sender": actorA, "recipient": actorB}
		if feature == "rgb" {
			fixture["walletFixture"].(map[string]any)["rgbWallets"] = map[string]any{"sender": rgbSender, "recipient": rgbReceiver}
			fixture["walletFixture"].(map[string]any)["udaWallets"] = map[string]any{"sender": udaSender, "recipient": udaReceiver}
		}
		fixture["walletFixture"].(map[string]any)["control_url"] = controller
		fixture["walletFixture"].(map[string]any)["realBitcoinControl"] = true
		if feature == "rgb-readback" {
			fixture["walletFixture"].(map[string]any)["rgbReadbackWitness"] = "8fa96dbe7cb39373f99c19dfe00326cbb04b7997a02d731adc5c94989d9ea14f"
		}
		if feature == "rgb-resume" || feature == "rgb-remaining" || feature == "rgb-cancel-diagnostic" {
			fixture["walletFixture"].(map[string]any)["resumeRGBWitnessTxID"] = "41a4379b9c9aad9b8ed2742e666a8321ee7739879fa65e1e5f66935b29bc2a0a"
			fixture["walletFixture"].(map[string]any)["resumeRGBConsignment"] = originalConsignment
			if feature == "rgb-cancel-diagnostic" {
				fixture["walletFixture"].(map[string]any)["rgbCancellationDiagnostic"] = true
			}

		}
	}
	if feature == "mint" || feature == "mint-partial" || feature == "mint-resume" || feature == "mint-did-resume" {
		fixture["walletFixture"].(map[string]any)["control_url"] = startSDKPWARegtestBitcoinController(t, confirmationAddress, mine)
		fixture["walletFixture"].(map[string]any)["realBitcoinControl"] = true
		if feature == "mint-resume" {
			fixture["walletFixture"].(map[string]any)["resumeMintOrdxDeploy"] = map[string]string{
				"commitTxId": "e72598ae118cec22067a6827d188b732c0de4b2f24b895c4931adc2219d4d590",
				"revealTxId": "980d3f018d70ab5a55e190e1221792cc8a51c488f581c4a569264585a97ec70a",
				"txId":       "980d3f018d70ab5a55e190e1221792cc8a51c488f581c4a569264585a97ec70a",
			}
		}

		if feature == "mint-did-resume" {
			fixture["walletFixture"].(map[string]any)["resumeMintDID"] = map[string]string{
				"commitTxId": "f0e9ab6601558e6820ef0a97e2e48e08128557a70eb0cfbea92d55af6c25555d",
				"revealTxId": "4a87a984dd90acc4689e519c124f944cc7f4e6a37ae37b83d6ea6171020e7006",
				"txId":       "4a87a984dd90acc4689e519c124f944cc7f4e6a37ae37b83d6ea6171020e7006",
			}
		}

	}
	if feature == "dapp" || feature == "dapp-resume" {
		fixture["walletFixture"].(map[string]any)["control_url"] = startSDKPWARegtestBitcoinController(t, confirmationAddress, mine)
		fixture["walletFixture"].(map[string]any)["realBitcoinControl"] = true
		fixture["walletFixture"].(map[string]any)["dappWallet"] = actorA
		fixture["walletFixture"].(map[string]any)["dappRecipient"] = actorB
		if feature == "dapp-resume" {
			fixture["walletFixture"].(map[string]any)["dappResumeTxID"] = "3c8bec789e7c36c82db7cc4b227dfcbdc089c4fc781893c5ffbf366afdaf1532"
		}
	}
	if feature == "l1-brc-balance" || feature == "l1-brc-readback" {
		balances := [2]string{"20", "0"}
		if feature == "l1-brc-balance" {
			asset := &indexercommon.AssetName{Protocol: "brc20", Type: "f", Ticker: "pwbx"}
			for i, manager := range []*wallet.Manager{ordinary1, ordinary2} {
				summary := manager.GetIndexerClient().GetAssetSummaryWithAddress(manager.GetWallet().GetAddress())
				require.NotNil(t, summary, "read the fixed BRC20 actor's actual ledger baseline")
				balance := indexercommon.NewDecimal(0, 0)
				for _, entry := range summary.Data {
					if entry != nil && entry.Name == *asset {
						balance = balance.Add(&entry.Amount)
					}
				}
				balances[i] = balance.String()
				if i == 0 {
					require.GreaterOrEqual(t, balance.Cmp(indexercommon.NewDecimal(5, 0)), 0)
				}
			}
		}
		fixture["walletFixture"].(map[string]any)["control_url"] = startSDKPWARegtestBitcoinController(t, confirmationAddress, mine)
		fixture["walletFixture"].(map[string]any)["realBitcoinControl"] = true
		fixture["walletFixture"].(map[string]any)["protocolWallets"] = map[string]any{
			"brc_balance": map[string]any{
				"sender": actorA, "recipient": actorB, "asset": "brc20:f:pwbx",
				"precision": 0, "label": "BRC20", "balance": balances[0], "recipient_balance": balances[1], "amount": "5",
			},
		}
		if feature == "l1-brc-readback" {
			fixture["walletFixture"].(map[string]any)["protocolWallets"].(map[string]any)["brc_balance"].(map[string]any)["resume"] = map[string]any{
				"txids":              []string{"ac448c4212bec73b28187046f32a23529555e6afd5b4547c5459358c0462c70a", "5e7229c1f739180cb8cff304f30d6548f5444c5c5e49301acc492f741e84a0e9", "ca144a45f414776932c92f85ecd2e4072fde7cb2169ff0fe7f010bd1afffbf36"},
				"payment_txid":       "ca144a45f414776932c92f85ecd2e4072fde7cb2169ff0fe7f010bd1afffbf36",
				"coinbase_txid":      "241c49db1097cf85eeb550cfd7378545f1f35dffb75ff1dfb32ab6f69116cc67",
				"sender_before_sats": "107055399", "receiver_before_sats": "19531740", "fee_rate": 1,
			}
		}

	}

	if feature == "l1-brc-transfer" {
		fixture["walletFixture"].(map[string]any)["control_url"] = startSDKPWARegtestBitcoinController(t, confirmationAddress, mine)
		fixture["walletFixture"].(map[string]any)["realBitcoinControl"] = true
		fixture["walletFixture"].(map[string]any)["protocolWallets"] = map[string]any{
			"brc_transfer": map[string]any{"sender": brc20Sender, "recipient": brc20Recipient, "asset": "brc20:f:pwbt", "precision": 0, "label": "BRC20", "balance": "250", "amount": "250", "seed_outpoint": brc20TransferPoint},
		}
	}
	if strings.HasPrefix(feature, "l1-runes") {
		fixture["walletFixture"].(map[string]any)["control_url"] = startSDKPWARegtestBitcoinController(t, confirmationAddress, mine)
		fixture["walletFixture"].(map[string]any)["realBitcoinControl"] = true
		fixture["walletFixture"].(map[string]any)["protocolWallets"] = map[string]any{
			"runes": map[string]any{"sender": actorA, "recipient": actorB, "asset": runeAsset, "precision": 2, "label": "Runes", "balance": runeBalances[0], "recipient_balance": runeBalances[1], "amount": "12.34"},
		}
		if strings.HasSuffix(feature, "-return") {
			fixture["walletFixture"].(map[string]any)["protocolWallets"].(map[string]any)["runes"] = map[string]any{
				"sender": actorB, "recipient": actorA, "asset": runeAsset, "precision": 2, "label": "Runes", "balance": runeBalances[1], "recipient_balance": runeBalances[0], "amount": "2.34",
			}
		}
	}
	if feature == "l1-funds" || feature == "l1-funds-resume" || feature == "l2-plain-send" || strings.HasPrefix(feature, "l2-channel-") {
		prepared := fixture["walletFixture"].(map[string]any)
		prepared["control_url"] = startSDKPWARegtestBitcoinController(t, confirmationAddress, mine)
		prepared["realBitcoinControl"] = true
		prepared["realBitcoinFunds"] = true
		if feature == "l1-funds-resume" {
			prepared["bitcoinFundsResume"] = map[string]string{"txid": "01722aae1107170828a463e2d9f4ab7a0af8fa33a8afa5aa2679e8c4653ee76a"}
		}
		prepared["mnemonic"], prepared["password"], prepared["address"], prepared["pk_script"] = actorA["mnemonic"], actorA["password"], actorA["address"], actorA["pk_script"]
		prepared["recipient"] = actorB
		prepared["asset"] = map[string]string{"key": "ordx:f:pwapos", "type": "ORDX", "ticker": "pwapos"}
		prepared["amounts"] = map[string]string{"opening": "1000000", "splicing_btc": "100000", "splicing_asset": "700", "deposit_btc": "10000", "deposit_asset": "600"}
		if feature == "l2-plain-send" {
			prepared["realSatoshiNetFunds"] = true
			prepared["control_url"] = startSDKPWARegtestBitcoinController(t, confirmationAddress, mine, network)
		}
		if strings.HasPrefix(feature, "l2-channel-") {
			prepared["mnemonic"], prepared["password"], prepared["address"], prepared["pk_script"] = actorB["mnemonic"], actorB["password"], actorB["address"], actorB["pk_script"]
			prepared["recipient"] = actorA
			prepared["realChannel"] = true
			prepared["realSatoshiNetFunds"] = true
			prepared["privateChannelID"] = privateChannelID
			prepared["resumePrivateChannel"] = feature != "l2-channel-open" ||
				openingOrReady && channelPreparation == sdkPWARegtestChannelReadyReused
			prepared["node_count"] = len(network.Nodes)
			if feature == "l2-channel-activation" {
				prepared["activation_height"], prepared["pos_activation_scheduled"] = sdkPWARegtestPOSV2ActivationHeight, true
				activate := func(activationCtx context.Context) (map[string]any, error) {
					return activateSDKPWARegtestPOSV2(t, activationCtx, network, ordinary1, sdkPWARegtestPOSV2ActivationHeight)
				}
				prepared["control_url"] = startSDKPWARegtestBitcoinControllerWithActivation(t, confirmationAddress, mine, activate, network)
			} else if v2AssetSplice {
				prepared["activation_height"], prepared["pos_activation_scheduled"], prepared["pos_v2_active"] =
					sdkPWARegtestPOSV2ActivationHeight, false, true
				if feature == "l2-channel-v2-asset-splice-diagnostic" {
					prepared["splicingInDiagnostic"] = true
				}
				prepared["control_url"] = startSDKPWARegtestBitcoinController(t, confirmationAddress, mine, network)
			} else if os.Getenv("SAT20_REAL_SATOSHINET_ATTACH") != "" {
				prepared["activation_height"], prepared["pos_activation_scheduled"], prepared["pos_v2_active"] =
					sdkPWARegtestPOSV2ActivationHeight, false, currentHeight >= sdkPWARegtestPOSV2ActivationHeight
				prepared["control_url"] = startSDKPWARegtestBitcoinController(t, confirmationAddress, mine, network)
			} else {
				prepared["activation_height"], prepared["pos_activation_scheduled"] = 0, false
				prepared["control_url"] = startSDKPWARegtestBitcoinController(t, confirmationAddress, mine, network)
			}
			if feature == "l2-channel-private-btc" {
				prepared["privateBTCRoundTripResume"] = map[string]any{
					"unlock_txid":  "6c5bff0f44bcba2c64d6fe8b53df8f1d4f4b9d8de0c84287117e96e038359878",
					"lock_txid":    "a414b6e31b059b7e86e5e502799efb711c8157e4a9699212a7d2b1fb49c89b0b",
					"source_txid":  "a75833fe6e1af04ac14cf02407b466b448d26c10de759aed0af1adac3b05e8fa",
					"anchor_txids": []string{"1aac5d128433435c6bae3c13fae1d12af4462f3e08ad4a62178ca06ac33dfd9f", "e5eef045e89306a32c0985927740792ee6a7acb8f9a5a56c4b74bba3567b5aee"},
				}
			}
		}
	}

	fixturePath := writeSDKPWARegtestPrivateJSON(t, artifactDir, "fixture.json", fixture)
	evidenceDir := filepath.Join(os.Getenv("SAT20_REAL_REGTEST_RUNTIME"), "pwa-browser-evidence")
	require.NoError(t, os.MkdirAll(evidenceDir, 0o700))
	require.NoError(t, os.Chmod(evidenceDir, 0o700))
	if feature == "account-usage" {
		cases := []string{
			"usage: fixed regtest ordinary-1 adds selects renames subaccount and preserves root identity",
			"usage: fixed regtest ordinary-1 renews paid storage through current AUTOPAY",
		}
		if selected := os.Getenv("SAT20_REAL_PWA_USAGE_CASE"); selected != "" {
			require.Contains(t, cases, selected, "incremental usage selection must name an exact existing UI case")
			cases = []string{selected}
			t.Logf("incremental fixed-account UI case: %s; other usage cases not executed", selected)
		}
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "fixed-account-management", cases, "--usage-cases")
		return
	}

	basicCases := []string{
		"Wallet PWA: create through the page and unlock the same identity after reload",
		"Wallet PWA: import a funded test wallet through the page",
		"Wallet PWA: receive address and clipboard match the active wallet",
		"Wallet PWA: public key and peer channel address match the SDK",
		"Wallet PWA: recovery phrase requires authentication and clears on navigation",
		"Wallet PWA: balance visibility and auto-lock preference survive reload",
		"Wallet PWA: language changes persist without changing wallet identity",
		"Wallet PWA: manually locked Bitcoin UTXO survives reload and can be unlocked",
		"Wallet PWA: signature review signs the displayed payload with the active key",
		"Wallet PWA: cancelling a node stake leaves funds and local stake state unchanged",
		"Wallet PWA: operation logs retain real results and can be cleared without deleting the wallet",
		"Wallet PWA: configured inactivity locks signing until password authentication",
		"Wallet PWA: referrer registration without an owned name cannot broadcast",
		"Wallet PWA: referrer cancellation and unknown-name rejection preserve funds",
	}
	biometricCases := []string{
		"WebAuthn PWA: real PRF credential creation persists an encrypted wallet binding",
		"WebAuthn PWA: an actual authenticator assertion unlocks the same SDK wallet after reload",
		"WebAuthn PWA: password change clears the old binding and permits enrollment with the new password",
		"WebAuthn PWA: deleting the biometric binding persists and restores password-only unlock",
	}
	if strings.HasPrefix(feature, "rgb") {
		cases := []string{
			"RGB PWA: issue a real NIA contract and protect its confirmed carrier",
			"RGB PWA: export and import a standard contract without crediting the recipient",
			"RGB PWA: an out-of-band witness invoice survives authenticated reload",
			"RGB PWA: prepare and sign a transfer without broadcasting before receiver validation",
			"RGB PWA: receiver validation persists the same package and grants no spendable balance",
			"RGB PWA: restored sender matches the receiver summary before broadcasting the actual transaction",
			"RGB PWA: confirmed receive conserves RGB supply and persists the actual carrier proofs",
			"RGB PWA: cancelling an unbroadcast transfer releases its reservations and preserves carrier protection",
			"RGB PWA: issue a real IFA with separately committed inflation rights",
			"RGB PWA: issue a real indivisible UDA",
			"RGB PWA: transfer the unique UDA to another wallet and persist its carrier proof",
		}
		if feature == "rgb-issue" {
			cases = cases[:1]
		}
		if feature == "rgb-resume" {
			cases = cases[5:10]
		}
		if feature == "rgb-readback" {
			cases = cases[10:]
		}
		if feature == "rgb-remaining" {
			cases = cases[8:10]
		}
		if feature == "rgb-cancel-diagnostic" {
			cases = cases[7:8]
		}
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "wallet-rgb", cases)
		return
	}
	if feature == "mint" || feature == "mint-partial" || feature == "mint-resume" || feature == "mint-did-resume" {
		cases := []string{
			"Mint PWA: ORDX deployment parameters reach review and cancellation preserves funds",
			"Mint PWA: BRC20 ticker length and self-mint parameters reach review without signing",
			"Mint PWA: Runes normalization and deployment terms survive review cancellation",
			"Mint PWA: absent ORDX BRC20 and Runes tickers cannot enter transaction review",
			"Mint PWA: DID validation and available-name review cancel without reserving or spending",
			"Mint PWA: ORDX deploy and mint are signed indexed confirmed and survive reload",
			"Mint PWA: BRC20 deploy and mint are signed indexed confirmed and survive reload",
			"Mint PWA: DID inscription confirms ownership and duplicate registration is rejected",
		}
		if feature == "mint-resume" {
			cases = cases[5:]
		}
		if feature == "mint-did-resume" {
			cases = cases[7:]
		}
		if feature == "mint-partial" {
			cases = []string{"Mint PWA: partial and remainder amounts survive rechecks and reach review"}
		}
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "wallet-mint", cases)
		return
	}

	if feature == "l1-brc-transfer" {
		cases := []string{"Assets PWA: BRC20 sends an existing transfer inscription and persists exact balances"}
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "wallet-l1-protocols", cases)
		return
	}
	if strings.HasPrefix(feature, "l1-runes") {
		cases := []string{"Assets PWA: Runes decimal partial send preserves the reviewed amount and change"}
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "wallet-l1-protocols", cases)
		return
	}
	if feature == "l1-funds" || feature == "l1-funds-resume" {
		cases := []string{
			"Funds PWA: Bitcoin Send confirms the requested BTC at the recipient",
			"Funds PWA: Bitcoin Send confirms the requested ORDX at the recipient",
			"Funds PWA: Bitcoin advanced Send creates two reviewed BTC outputs",
			"Funds PWA: Bitcoin advanced ORDX Send creates two reviewed asset outputs",
		}
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "bitcoin-funds", cases)
		return
	}
	if feature == "l2-plain-send" {
		cases := []string{"Funds PWA: SatoshiNet Send confirms the requested BTC at the recipient"}
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "satoshinet-funds", cases)
		return
	}
	if feature == "l2-channel-open" {
		cases := []string{"POS PWA: opening confirms the signed Anchor after pending reload"}
		if openingOrReady && channelPreparation == sdkPWARegtestChannelReadyReused {
			cases = []string{"POS PWA: existing READY channel survives page reload without a new funding"}
			t.Log("existing READY channel: UI readback and reload only; first opening is not executed")
		}
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "channel-open", cases)
		return
	}
	if feature == "l2-channel-splice" {
		cases := []string{"POS PWA: BTC splicing-in survives closing and reopening the page"}
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "channel-splice", cases)
		return
	}
	if feature == "l2-channel-activation" {
		cases := []string{"POS PWA: a drained activation preserves the existing private channel"}
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "channel-activation", cases)
		return
	}
	if feature == "l2-channel-v2-asset-splice" {
		cases := []string{"POS PWA: v2 asset splicing-in preserves quantities and signed outpoints"}
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "channel-v2-asset-splice", cases)
		return
	}
	if feature == "l2-channel-v2-asset-splice-diagnostic" {
		cases := []string{"POS PWA: v2 asset splicing-in preserves quantities and signed outpoints"}
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "channel-v2-asset-splice-diagnostic", cases)
		return
	}
	if feature == "l2-channel-safety" {
		cases := []string{
			"Escape PWA: cancelling cooperative close preserves the channel and funds",
			"Escape PWA: force-close safety confirmation displays evidence and cancels safely",
		}
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "channel-safety", cases)
		return
	}
	if feature == "l2-channel-private-btc" {
		cases := []string{"Funds PWA: private BTC unlock and lock restore the channel balance"}
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "channel-private-btc", cases)
		return
	}
	if feature == "l2-channel-splice-out-btc" {
		cases := []string{"Funds PWA: private BTC splicing-out returns confirmed Bitcoin funds"}
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "channel-splice-out-btc", cases)
		return
	}
	if feature == "pos-resume" {
		cases := []string{
			"POS PWA: scheduled producer rotation preserves wallet state",
			"POS PWA: offline Miner substitution confirms a PWA deposit",
			"POS PWA: a restored Miner and normal Core restart preserve wallet state",
		}
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "pos-resume", cases)
		return
	}

	if feature == "l1-brc-balance" || feature == "l1-brc-readback" {
		cases := []string{"Assets PWA: BRC20 creates a transfer inscription before sending an untransferable balance"}
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "wallet-l1-protocols", cases)
		return
	}

	if feature == "mining" {
		cases := []string{
			"Mining PWA: real WASM worker submits independently verified L1 proof of work",
			"Mining PWA: stopping halts the worker and reload preserves its configuration",
		}
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "wallet-mining", cases)
		return
	}

	if feature == "dapp" || feature == "dapp-resume" {
		cases := []string{
			"DApp PWA: isolated client cannot read accounts before approval or after cancellation",
			"DApp PWA: explicit account permissions expose the actual wallet identity",
			"DApp PWA: signing requires its separately approved capability",
			"DApp PWA: cancelling message approval returns no signature",
			"DApp PWA: approved domain message verifies against the displayed wallet key",
			"DApp PWA: disconnect revokes the grant and notifies the client",
			"DApp PWA: authorized Bitcoin PSBT signing preserves exact outputs without broadcasting",
			"DApp PWA: separately authorized broadcast confirms the same signed transaction exactly once",
		}
		if feature == "dapp-resume" {
			cases = cases[7:]
		}
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "dapp-permissions", cases)
		return
	}
	if !t.Run("wallet-basic", func(t *testing.T) {
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "wallet-basic", basicCases)
	}) {
		return
	}
	t.Run("biometric", func(t *testing.T) {
		runSDKPWARegtestBrowserGroup(t, pwa, configPath, fixturePath, evidenceDir, "biometric", biometricCases)
	})
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

func runSDKPWARegtestBrowserGroup(t *testing.T, pwa, configPath, fixturePath, evidenceDir, group string, cases []string, selector ...string) {
	t.Helper()
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	if deadline, ok := t.Deadline(); ok {
		ctx, cancel = context.WithDeadline(context.Background(), deadline.Add(-15*time.Second))
		defer cancel()
	}
	logFile, err := os.CreateTemp(evidenceDir, "pwa-"+group+"-*.jsonl")
	require.NoError(t, err)
	logPath := logFile.Name()
	require.NoError(t, logFile.Chmod(0o600))
	caseSelector := "--wallet-cases"
	if len(selector) > 0 {
		require.Len(t, selector, 1)
		require.Contains(t, []string{"--wallet-cases", "--usage-cases"}, selector[0])
		caseSelector = selector[0]
	}
	args := []string{"scripts/verify/account-management-e2e.mjs", "--regtest",
		"--regtest-config", configPath, "--regtest-fixture", fixturePath, caseSelector}
	args = append(args, cases...)
	command := exec.CommandContext(ctx, node, args...)
	command.Dir = pwa
	command.Stdout = logFile
	command.Stderr = logFile
	err = command.Run()
	require.NoError(t, logFile.Close())
	require.NoError(t, err, "regtest %s PWA runner failed; private JSON log: %s", group, logPath)
	t.Logf("regtest %s PWA runner exit=0; private JSON log: %s", group, logPath)
}
