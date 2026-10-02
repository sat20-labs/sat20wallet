package e2e

import (
	"encoding/hex"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// These release regressions run in ordinary go test ./... too. Each poisoning
// candidate is last on its own temporary network; no existing wallets are used.
func TestSDKSmartContractReleaseEscrow(t *testing.T) {
	f := newSDKContractReviewFixture(t)
	artifact, err := CompileSolidityFile("testdata/contracts/SDKReviewProbe.sol", "SDKReviewProbe", SolidityCompileOptions{})
	require.NoError(t, err)
	code := appendSolidityConstructorArgs(artifact.Bytecode, evmABIString(reviewAssetA), evmABIString(reviewAssetB))
	address := f.deployEVM(t, code)
	t.Run("funded_deploy_and_call_control", func(t *testing.T) {
		f.invoke(t, f.owner, sdkReviewEVMCall(address, solidityCall("inc()")), contract.ResultStatusSuccess)
		sdkReviewRequireCounter(t, f.reader, address, 1)
	})
	t.Run("underfunded_invoke_is_rejected_or_refunded_not_block_fatal", func(t *testing.T) {
		height, err := f.network.Core.Client.GetBlockCount()
		require.NoError(t, err)
		fee, err := contract.GasFeeAtHeight(contract.InvokeBaseGas, uint64(height))
		require.NoError(t, err)
		req := sdkReviewEVMCall(address, solidityCall("inc()"))
		req.GasAssetAmount, req.Value = fee, 1000
		req.Assets = []wallet.ContractFundingAsset{{AssetName: reviewAssetA, Amount: "9"}}
		result, err := f.owner.InvokeUnifiedContract(req)
		if err != nil {
			require.Nil(t, result)
			require.Regexp(t, "(?i)(gas|fund|escrow)", err.Error())
		} else {
			work, block := f.mined(t, result)
			require.NotEqual(t, contract.ResultStatusSuccess, sdkReviewResultStatus(t, work, block))
			require.EqualValues(t, 9, sdkReviewReturnedAsset(block, f.owner.GetWallet().GetAddress(), reviewAssetA))
		}
		sdkReviewRequireCounter(t, f.reader, address, 1)
		f.invoke(t, f.owner, sdkReviewEVMCall(address, solidityCall("inc()")), contract.ResultStatusSuccess)
		sdkReviewRequireCounter(t, f.reader, address, 2)
	})
	height, err := f.network.Core.Client.GetBlockCount()
	require.NoError(t, err)
	baseFee, err := contract.GasFeeAtHeight(contract.DeployBaseGas, uint64(height))
	require.NoError(t, err)
	request := &wallet.ContractDeployRequest{ContractType: wallet.ContractTypeEVM,
		ContractContent: hex.EncodeToString(code), ContentEncoding: "hex", DeployNonce: 8090001,
		GasLimit: 5000000, GasAssetAmount: baseFee, FundingValue: 1000,
		Assets: []wallet.ContractFundingAsset{{AssetName: reviewAssetB, Amount: "8"}}}
	t.Run("quote_must_not_offer_a_budget_without_execution_or_result_escrow", func(t *testing.T) {
		quote, err := f.owner.EstimateDeployUnifiedContract(request)
		if err == nil {
			t.Errorf("release-review: SDK quoted legal gasLimit=%d with networkFee=%d and execution/Result funding=%d", quote.GasLimit, quote.GasFeeAmount, quote.GasFundAmount)
		}
	})
	t.Run("underfunded_deploy_is_rejected_or_refunded_not_block_fatal", func(t *testing.T) {
		result, err := f.owner.DeployUnifiedContract(request)
		if err != nil {
			require.Nil(t, result)
			require.Regexp(t, "(?i)(gas|fund|escrow)", err.Error())
		} else {
			t.Logf("release-review: SDK and mempool accepted deployment with legal gasLimit=%d, gas funding=%d", result.GasLimit, result.GasFundAmount)
			work, block := f.mined(t, result)
			require.NotEqual(t, contract.ResultStatusSuccess, sdkReviewResultStatus(t, work, block))
			require.EqualValues(t, 8, sdkReviewReturnedAsset(block, f.owner.GetWallet().GetAddress(), reviewAssetB))
		}
		f.invoke(t, f.owner, sdkReviewEVMCall(address, solidityCall("inc()")), contract.ResultStatusSuccess)
		sdkReviewRequireCounter(t, f.reader, address, 3)
	})
}

// The ordinary suite checks the same public transaction-validation entry used
// by block connection as well as mempool submission. This is not a forged-PoS
// block integration test. Asset invariants apply without an activation height.
func TestSDKSmartContractReleaseAssetValidation(t *testing.T) {
	const bound = "ordx:f:releasebound"
	profile := templateAssetProfile{Name: "bound", Asset: bound, BindingSat: 1, Supply: "10000"}
	base := newTemplateFixtureWithProfiles(t, []templateAssetProfile{profile})
	key := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	splitToDKVSKeyPathActors(t, base, base.gasAnchor, wallet.GetGasAssetName(),
		[]int64{50000000, 50000000}, []int64{1000000, 1000000}, []*dkvsKeyPathActor{key, key})
	owner, _ := newWalletManagerForNode(t, base.Network.Core, dkvsClientMnemonic)
	script, err := wallet.GetP2TRpkScript(owner.GetWallet().GetPaymentPubKey())
	require.NoError(t, err)
	base.splitProfile(t, base.A, base.assetAnchors[bound], profile,
		[]string{"10000"}, []int64{100000}, []*templateActor{{PkScript: script}})
	f := &sdkContractReviewFixture{network: base.Network, owner: owner}
	for _, mode := range []string{"unchanged_control", "changed_binding", "missing_carrier", "plain_asset_alias"} {
		t.Run(mode, func(t *testing.T) {
			tx, prev := sdkAdmissionOrdinaryTx(t, f, bound)
			name := wire.NewAssetNameFromString(bound)
			asset, err := tx.TxOut[0].Assets.Find(name)
			require.NoError(t, err)
			switch mode {
			case "changed_binding":
				asset.BindingSat = 0
			case "missing_carrier":
				value := tx.TxOut[0].Value
				tx.TxOut[0].Value = 0
				tx.AddTxOut(wire.NewTxOut(value, nil, script))
			case "plain_asset_alias":
				require.NoError(t, tx.TxOut[0].Assets.Add(&wire.AssetInfo{Name: wire.AssetName{}, Amount: *indexer.NewDefaultDecimal(0)}))
			}
			signed, err := owner.SignContractTx_SatsNet(tx, prev, "", 0)
			require.NoError(t, err)
			require.NoError(t, wallet.VerifySignedTx_SatsNet(signed, prev))
			height, err := base.Network.Core.Client.GetBlockCount()
			require.NoError(t, err)
			view := blockchain.NewUtxoViewpoint()
			for _, input := range signed.TxIn {
				original, err := base.Network.Core.Client.GetRawTransaction(&input.PreviousOutPoint.Hash)
				require.NoError(t, err)
				view.AddTxOuts(original, int32(height))
			}
			validated := btcutil.NewTx(signed)
			validationErr := blockchain.CheckTransactionSanity(validated)
			if validationErr == nil {
				_, _, validationErr = blockchain.CheckTransactionInputs(validated, false, int32(height+1), view, wallet.GetChainParam_SatsNet())
			}
			if mode == "unchanged_control" {
				require.NoError(t, validationErr)
				return
			}
			_, admissionErr := owner.BroadcastTx_SatsNet(signed)
			require.Error(t, admissionErr, "release-review: malformed asset must be rejected by mempool")
			require.Regexp(t, "(?i)(asset|BindingSat|carrier|satoshi|TxAssets)", admissionErr.Error())
			if validationErr == nil {
				t.Errorf("release-review: %s rejected by mempool but accepted by signed-transaction block-validation entry", mode)
			}
		})
	}
}
