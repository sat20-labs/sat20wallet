package e2e

import (
	"encoding/base64"
	"encoding/hex"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/satoshinet/btcutil"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestSDKSmartContractTemplateReleaseEscrow(t *testing.T) {
	f := newSDKContractReviewFixture(t)
	encoded, err := wallet.BuildUnifiedContractContent(wallet.ContractTypeTemplate, contract.TemplateExchange,
		sdkReviewJSON(t, contract.TemplateExchangeContract{AssetAName: reviewAssetA, AssetBName: contract.SatoshiAssetName,
			PriceMode: contract.ExchangePriceModeHeight, Steps: []contract.TemplateExchangePriceStep{{Threshold: "0", BPerA: "10"}}}))
	require.NoError(t, err)
	height, err := f.network.Core.Client.GetBlockCount()
	require.NoError(t, err)
	baseFee, err := contract.GasFeeAtHeight(contract.DeployBaseGas, uint64(height))
	require.NoError(t, err)
	req := &wallet.ContractDeployRequest{ContractType: wallet.ContractTypeTemplate, SubType: contract.TemplateExchange,
		ContractContent: encoded, ContentEncoding: "base64", DeployNonce: 8090002,
		GasLimit: contract.DeployBaseGas, GasAssetAmount: baseFee, FundingValue: 1000,
		Assets: []wallet.ContractFundingAsset{{AssetName: reviewAssetA, Amount: "8"}}}
	t.Run("quote_rejects_missing_Result_escrow", func(t *testing.T) {
		_, err := f.owner.EstimateDeployUnifiedContract(req)
		require.Error(t, err, "release-review: template quote omitted Result escrow")
	})
	t.Run("SDK_rejects_or_refunds_underfunded_template_deploy", func(t *testing.T) {
		result, err := f.owner.DeployUnifiedContract(req)
		if err != nil {
			require.Nil(t, result)
			require.Regexp(t, "(?i)(gas|fund|escrow)", err.Error())
		} else {
			work, block := f.mined(t, result)
			require.NotNil(t, work)
			require.EqualValues(t, 8, sdkReviewReturnedAsset(block, f.owner.GetWallet().GetAddress(), reviewAssetA))
			requireTxOutputValueAmount(t, contractResultTxs(block)[0], f.owner.GetWallet().GetAddress(), 990)
		}
		// Reusing the nonce with valid funding must work after a failed attempt.
		req.GasAssetAmount = 0
		result, err = f.owner.DeployUnifiedContract(req)
		require.NoError(t, err)
		work, block := f.mined(t, result)
		require.Equal(t, contract.ResultStatusSuccess, sdkReviewResultStatus(t, work, block))
	})
}

// Bypass the high-level encoder, but retain public SDK signing/submission and
// real node execution. Each module has its own network so a red deployment
// cannot hide the other module's result by poisoning a shared mempool.
func TestSDKSmartContractDeployFailureSettlement(t *testing.T) {
	for _, module := range []string{wallet.ContractTypeEVM, wallet.ContractTypeTemplate} {
		t.Run(module, func(t *testing.T) {
			f := newSDKContractReviewFixture(t)
			owner := f.owner.GetWallet().GetAddress()
			ownerScript, err := wallet.GetP2TRpkScript(f.owner.GetWallet().GetPaymentPubKey())
			require.NoError(t, err)
			kind, subtype, actor, limit := byte(contract.ContractTypeTemplate), contract.TemplateExchange, owner, int64(contract.DeployBaseGas)
			encoded, err := wallet.BuildUnifiedContractContent(wallet.ContractTypeTemplate, contract.TemplateExchange,
				sdkReviewJSON(t, contract.TemplateExchangeContract{AssetAName: reviewAssetA, AssetBName: contract.SatoshiAssetName,
					PriceMode: contract.ExchangePriceModeHeight, Steps: []contract.TemplateExchangePriceStep{{Threshold: "0", BPerA: "10"}}}))
			require.NoError(t, err)
			code, err := base64.StdEncoding.DecodeString(encoded)
			require.NoError(t, err)
			if module == wallet.ContractTypeEVM {
				artifact, err := CompileSolidityFile("testdata/contracts/SDKReviewProbe.sol", "SDKReviewProbe", SolidityCompileOptions{})
				require.NoError(t, err)
				code = appendSolidityConstructorArgs(artifact.Bytecode, evmABIString(reviewAssetA), evmABIString(reviewAssetB))
				kind, subtype, actor, limit = contract.ContractTypeEVM, "", "0x"+hex.EncodeToString(btcutil.Hash160([]byte(owner))), 5000000
			}
			height, err := f.network.Core.Client.GetBlockCount()
			require.NoError(t, err)
			baseFee, err := contract.GasFeeAtHeight(contract.DeployBaseGas, uint64(height))
			require.NoError(t, err)
			gasInputs, _, gasPrev := sdkAdmissionInputs(t, f, wallet.GetGasAssetName(), baseFee)
			assetInputs, _, assetPrev := sdkAdmissionInputs(t, f, reviewAssetA, 8)
			inputs := []wire.OutPoint{}
			prev := txscript.NewMultiPrevOutFetcher(nil)
			var total wire.TxOut
			seen := map[wire.OutPoint]bool{}
			for _, group := range []struct {
				inputs []wire.OutPoint
				prev   *txscript.MultiPrevOutFetcher
			}{{gasInputs, gasPrev}, {assetInputs, assetPrev}} {
				for _, input := range group.inputs {
					if seen[input] {
						continue
					}
					seen[input] = true
					output := group.prev.FetchPrevOutput(input)
					require.NotNil(t, output)
					inputs = append(inputs, input)
					prev.AddPrevOut(input, output)
					total.Value += output.Value
					require.NoError(t, total.Assets.Merge(output.Assets))
				}
			}
			business := wire.TxAssets{{Name: *wire.NewAssetNameFromString(reviewAssetA), Amount: *indexer.NewDefaultDecimal(8)}}
			baseFeeAssets := wire.TxAssets{{Name: *wire.NewAssetNameFromString(wallet.GetGasAssetName()), Amount: *indexer.NewDefaultDecimal(baseFee)}}
			changeAssets := total.Assets.Clone()
			require.NoError(t, changeAssets.Split(business))
			require.NoError(t, changeAssets.Split(baseFeeAssets))
			tx, address, err := contract.BuildDeployTx(contract.DeployTxBuildRequest{ContractPrefix: contract.TestnetContractPrefix,
				Type: kind, SubType: subtype, Version: 1, Deployer: actor, DeployNonce: 8090011, ContractContent: code,
				GasLimit: limit, Funding: *wire.NewTxOut(1000, business, nil), Inputs: inputs,
				ExtraOutputs: []*wire.TxOut{wire.NewTxOut(total.Value-1000, changeAssets, ownerScript)}})
			if err != nil {
				t.Fatalf("release-review: raw %s deployment fixture build: %v", module, err)
			}
			signed, err := f.owner.SignContractTx_SatsNet(tx, prev, wallet.GetGasAssetName(), baseFee)
			if err != nil {
				t.Fatalf("release-review: raw %s deployment fixture signing: %v", module, err)
			}
			// The base network fee is intentionally paid in the gas asset.
			// Verify its exact allowed burn, not the ordinary zero-burn policy.
			require.NoError(t, wallet.VerifySignedTxSatsNetAllowBurn(signed, prev, baseFeeAssets))
			id, admissionErr := f.owner.BroadcastTx_SatsNet(signed)
			if admissionErr != nil {
				require.Regexp(t, "(?i)(gas|fund|escrow)", admissionErr.Error())
				t.Logf("release-review: raw %s deployment rejected at admission", module)
			} else {
				work, block := f.mined(t, &wallet.ContractTxResult{TxID: id})
				require.Equal(t, signed.TxHash(), work.TxHash())
				results := contractResultTxs(block)
				require.Len(t, results, 1)
				require.Len(t, results[0].TxIn, 1, "refund spent unrelated contract funds")
				require.Equal(t, signed.TxHash(), results[0].TxIn[0].PreviousOutPoint.Hash)
				require.EqualValues(t, 8, sdkReviewReturnedAsset(block, owner, reviewAssetA))
				requireTxOutputValueAmount(t, results[0], owner, 990)
				t.Logf("release-review: raw %s deployment settled with isolated asset refund and 10-sat refund fee", module)
			}
			// A failed attempt must not register the derived address. A normal
			// funded deployment with the same nonce must mine on the same node.
			req := &wallet.ContractDeployRequest{ContractType: module, SubType: subtype, ContractContent: hex.EncodeToString(code),
				ContentEncoding: "hex", DeployNonce: 8090011, GasLimit: limit, FundingValue: 1000,
				Assets: []wallet.ContractFundingAsset{{AssetName: reviewAssetA, Amount: "8"}}}
			result, err := f.owner.DeployUnifiedContract(req)
			require.NoError(t, err)
			require.Equal(t, address.MustEncode(), result.ContractAddress)
			work, block := f.mined(t, result)
			require.Equal(t, contract.ResultStatusSuccess, sdkReviewResultStatus(t, work, block))
			if module == wallet.ContractTypeEVM {
				f.invoke(t, f.owner, sdkReviewEVMCall(result.ContractAddress, solidityCall("inc()")), contract.ResultStatusSuccess)
				sdkReviewRequireCounter(t, f.reader, result.ContractAddress, 1)
			}
		})
	}
}
