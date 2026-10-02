package e2e

import (
    "encoding/hex"
    "testing"

    "github.com/sat20-labs/sat20wallet/sdk/wallet"
    contract "github.com/sat20-labs/satoshinet/contract"
    "github.com/stretchr/testify/require"
)

func TestSDKSmartContractsEVM(t *testing.T) {
    artifact, err := CompileSolidityFile("testdata/contracts/SDKReviewProbe.sol", "SDKReviewProbe", SolidityCompileOptions{})
    require.NoError(t, err)
    f := newSDKContractReviewFixture(t)
    code := appendSolidityConstructorArgs(artifact.Bytecode, evmABIString(reviewAssetA), evmABIString(reviewAssetB))
    address := f.deployEVM(t, code)
    t.Run("basic_SDK_deploy_invoke_and_independent_query", func(t *testing.T) {
        sdkReviewRequireCounter(t, f.owner, address, 0)
        f.invoke(t, f.owner, sdkReviewEVMCall(address, solidityCall("inc()")), contract.ResultStatusSuccess)
        sdkReviewRequireCounter(t, f.owner, address, 1)
        sdkReviewRequireCounter(t, f.reader, address, 1)
        for _, query := range []string{wallet.ContractQueryInfo, wallet.ContractQueryList, wallet.ContractQueryHistory} {
            value, err := f.owner.QueryContract(&wallet.ContractQueryRequest{Query: query, Contract: address, Limit: 100})
            require.NoError(t, err); require.NotEmpty(t, value)
        }
    })
    t.Run("revert_preserves_state_and_refunds_only_call_funding", func(t *testing.T) {
        before := sdkReviewProbeState(t, f.owner, address)["counter"]
        req := sdkReviewEVMCall(address, solidityCall("setThenRevert()")); req.Value = 100
        req.Assets = []wallet.ContractFundingAsset{{AssetName: reviewAssetA, Amount: "9"}}
        _, block := f.invoke(t, f.owner, req, contract.ResultStatusRevert)
        require.Equal(t, before, sdkReviewProbeState(t, f.owner, address)["counter"])
        require.EqualValues(t, 9, sdkReviewReturnedAsset(block, f.owner.GetWallet().GetAddress(), reviewAssetA))
    })
    for _, method := range []string{"nestedRevertCaught()", "staticWriteRejected()"} {
        t.Run(method, func(t *testing.T) {
            before := sdkReviewProbeState(t, f.owner, address)["counter"].(float64)
            f.invoke(t, f.owner, sdkReviewEVMCall(address, solidityCall(method)), contract.ResultStatusSuccess)
            require.Equal(t, before+1, sdkReviewProbeState(t, f.owner, address)["counter"])
        })
    }
    t.Run("multi_asset_explicit_deposit_claims_exact_funding", func(t *testing.T) {
        req := sdkReviewEVMCall(address, solidityCall("deposit()")); req.Value = 100
        req.Assets = []wallet.ContractFundingAsset{{AssetName: reviewAssetA, Amount: "30"}, {AssetName: reviewAssetB, Amount: "20"}}
        work, _ := f.invoke(t, f.owner, req, contract.ResultStatusSuccess)
        require.EqualValues(t, 30, sdkReviewAssetAmount(work, address, reviewAssetA))
        require.EqualValues(t, 20, sdkReviewAssetAmount(work, address, reviewAssetB))
        state := sdkReviewProbeState(t, f.owner, address)
        require.Equal(t, float64(30), state["depositedA"]); require.Equal(t, float64(20), state["depositedB"])
    })
    t.Run("reverted_asset_intent_creates_no_external_payment", func(t *testing.T) {
        before := sdkReviewProbeState(t, f.owner, address)["counter"]
        req := sdkReviewEVMCall(address, solidityCall("pay(string,string,bool)", evmABIString(f.other.GetWallet().GetAddress()), evmABIString("7"), evmABIUint(1)))
        _, block := f.invoke(t, f.owner, req, contract.ResultStatusRevert)
        require.Equal(t, before, sdkReviewProbeState(t, f.owner, address)["counter"])
        require.Zero(t, sdkReviewReturnedAsset(block, f.other.GetWallet().GetAddress(), reviewAssetA))
    })
    t.Run("asset_transfer_produces_exact_recipient_UTXO", func(t *testing.T) {
        req := sdkReviewEVMCall(address, solidityCall("pay(string,string,bool)", evmABIString(f.other.GetWallet().GetAddress()), evmABIString("7"), evmABIUint(0)))
        _, block := f.invoke(t, f.owner, req, contract.ResultStatusSuccess)
        require.EqualValues(t, 7, sdkReviewReturnedAsset(block, f.other.GetWallet().GetAddress(), reviewAssetA))
    })
    t.Run("non_owner_cannot_transfer_contract_assets", func(t *testing.T) {
        before := sdkReviewProbeState(t, f.owner, address)
        req := sdkReviewEVMCall(address, solidityCall("pay(string,string,bool)", evmABIString(f.other.GetWallet().GetAddress()), evmABIString("1"), evmABIUint(0)))
        _, block := f.invoke(t, f.other, req, contract.ResultStatusRevert)
        require.Equal(t, before, sdkReviewProbeState(t, f.reader, address))
        require.Zero(t, sdkReviewReturnedAsset(block, f.other.GetWallet().GetAddress(), reviewAssetA))
    })
    t.Run("non_owner_cannot_close_contract", func(t *testing.T) {
        before := sdkReviewProbeState(t, f.owner, address)
        f.invoke(t, f.other, &wallet.ContractInvokeRequest{ContractType: wallet.ContractTypeEVM, ContractAddress: address, Action: "close", GasLimit: 1000000}, contract.ResultStatusInvalid)
        require.Equal(t, before, sdkReviewProbeState(t, f.owner, address))
    })
    t.Run("single_asset_default_invoke_preserves_value_and_asset", func(t *testing.T) {
        before := sdkReviewProbeState(t, f.owner, address)["depositedA"].(float64)
        f.invoke(t, f.owner, &wallet.ContractInvokeRequest{ContractType: wallet.ContractTypeEVM, ContractAddress: address,
            DefaultInvoke: true, Value: 100, Assets: []wallet.ContractFundingAsset{{AssetName: reviewAssetA, Amount: "3"}}}, contract.ResultStatusSuccess)
        require.Equal(t, before+3, sdkReviewProbeState(t, f.owner, address)["depositedA"])
    })
    t.Run("default_multi_asset_must_not_silently_drop_second_asset", func(t *testing.T) {
        req := &wallet.ContractInvokeRequest{ContractType: wallet.ContractTypeEVM, ContractAddress: address,
            DefaultInvoke: true, Value: 100, Assets: []wallet.ContractFundingAsset{{AssetName: reviewAssetA, Amount: "11"}, {AssetName: reviewAssetB, Amount: "13"}}}
        result, err := f.owner.InvokeUnifiedContract(req)
        if err != nil {
            require.Nil(t, result)
            require.Regexp(t, "(?i)(multiple|multi.asset|one asset|single asset|unsupported|not support)", err.Error())
            return
        }
        work, _ := f.mined(t, result)
        require.EqualValues(t, 11, sdkReviewAssetAmount(work, address, reviewAssetA))
        require.EqualValues(t, 13, sdkReviewAssetAmount(work, address, reviewAssetB), "contract-review: default invoke silently discarded the second funding asset")
    })
    for _, amount := range []struct{name, value string}{
        {"uint64_overflow", "18446744073709551616"},
        {"positive_fraction", "0.5"},
        {"negative_fraction", "-0.5"},
    } {
        t.Run("invalid_plain_funding_"+amount.name+"_rejected_before_broadcast", func(t *testing.T) {
            req := sdkReviewEVMCall(address, solidityCall("inc()"))
            req.Assets = []wallet.ContractFundingAsset{{AssetName: contract.SatoshiAssetName, Amount: amount.value}}
            result, err := f.owner.InvokeUnifiedContract(req)
            // Keep later scenarios independent when defective SDK validation
            // has already broadcast a perfectly mineable, wrong-amount tx.
            if err == nil && result != nil { _, _ = f.mined(t, result) }
            require.Error(t, err, "contract-review: invalid plain-sat amount %s was silently truncated", amount.value)
            require.Nil(t, result)
        })
    }
    t.Run("constructor_revert_has_no_runtime_and_refunds_funding", func(t *testing.T) {
        failed, err := CompileSolidityFile("testdata/contracts/SDKReviewProbe.sol", "SDKReviewRevertingConstructor", SolidityCompileOptions{})
        require.NoError(t, err)
        result, err := f.owner.DeployUnifiedContract(&wallet.ContractDeployRequest{ContractType: wallet.ContractTypeEVM,
            ContractContent: hex.EncodeToString(failed.Bytecode), ContentEncoding: "hex", GasLimit: contract.DeployBaseGas, FundingValue: 100,
            Assets: []wallet.ContractFundingAsset{{AssetName: reviewAssetB, Amount: "8"}}})
        require.NoError(t, err)
        work, block := f.mined(t, result)
        require.Equal(t, contract.ResultStatusRevert, sdkReviewResultStatus(t, work, block))
        require.EqualValues(t, 8, sdkReviewReturnedAsset(block, f.owner.GetWallet().GetAddress(), reviewAssetB))
        // An info/history record of a failed attempt is legitimate. Check the
        // actual runtime and the default active list, not HTTP success alone.
        state := sdkReviewQueryState(t, f.owner, result.ContractAddress)
        exists, found := sdkReviewFindJSON(state, "exists")
        require.True(t, found, "contract-review: failed deployment lacks explicit runtime absence")
        require.Equal(t, false, exists, "contract-review: reverted constructor left a live runtime")
        listed, err := f.owner.QueryContract(&wallet.ContractQueryRequest{Query: wallet.ContractQueryList, Limit: 100})
        require.NoError(t, err)
        require.NotContains(t, listed, result.ContractAddress, "contract-review: failed deployment appeared in the active list")
    })
    t.Run("malformed_calldata_encoding_does_not_broadcast", func(t *testing.T) {
        before := sdkReviewProbeState(t, f.owner, address)
        req := sdkReviewEVMCall(address, nil); req.Param = "this-is-not-hex"
        result, err := f.owner.InvokeUnifiedContract(req)
        require.Error(t, err); require.Nil(t, result)
        require.Equal(t, before, sdkReviewProbeState(t, f.reader, address))
    })
    t.Run("deployer_close_then_repeated_close_cannot_pay_twice", func(t *testing.T) {
        req := &wallet.ContractInvokeRequest{ContractType: wallet.ContractTypeEVM, ContractAddress: address, Action: "close", GasLimit: 1000000}
        f.invoke(t, f.owner, req, contract.ResultStatusSuccess)
        result, err := f.owner.InvokeUnifiedContract(req)
        if err == nil {
            work, block := f.mined(t, result)
            require.NotEqual(t, contract.ResultStatusSuccess, sdkReviewResultStatus(t, work, block), "contract-review: repeated close succeeded")
            require.Zero(t, sdkReviewReturnedAsset(block, f.owner.GetWallet().GetAddress(), reviewAssetA))
        }
    })
}

// Legal execution exhaustion and invalid admission are separate assertions.
// Invalid gas runs last on isolated nodes so it cannot contaminate later tests.
func TestSDKSmartContractsOutOfGas(t *testing.T) {
    artifact, err := CompileSolidityFile("testdata/contracts/SDKReviewProbe.sol", "SDKReviewProbe", SolidityCompileOptions{})
    require.NoError(t, err)
    f := newSDKContractReviewFixture(t)
    code := appendSolidityConstructorArgs(artifact.Bytecode, evmABIString(reviewAssetA), evmABIString(reviewAssetB))
    address := f.deployEVM(t, code)
    t.Run("legal_budget_exhaustion_rolls_back_and_refunds", func(t *testing.T) {
        req := sdkReviewEVMCall(address, solidityCall("burnGas()")); req.GasLimit = contract.InvokeBaseGas + 100000
        req.Assets = []wallet.ContractFundingAsset{{AssetName: reviewAssetB, Amount: "5"}}
        _, block := f.invoke(t, f.owner, req, contract.ResultStatusOutOfGas)
        sdkReviewRequireCounter(t, f.owner, address, 0)
        require.EqualValues(t, 5, sdkReviewReturnedAsset(block, f.owner.GetWallet().GetAddress(), reviewAssetB))
        // Ordinary gas exhaustion must not impede a later valid operation.
        f.invoke(t, f.owner, sdkReviewEVMCall(address, solidityCall("inc()")), contract.ResultStatusSuccess)
        sdkReviewRequireCounter(t, f.reader, address, 1)
    })
    t.Run("fee_quote_rejects_below_admission_floor", func(t *testing.T) {
        req := sdkReviewEVMCall(address, solidityCall("inc()")); req.GasLimit = contract.InvokeBaseGas - 1
        _, err := f.owner.QueryFeeForInvokeUnifiedContract(req)
        require.Error(t, err, "contract-review: fee quote accepted a gas limit that consensus rejects")
    })
    t.Run("gas_below_admission_floor_must_not_poison_mining", func(t *testing.T) {
        req := sdkReviewEVMCall(address, solidityCall("inc()")); req.GasLimit = contract.InvokeBaseGas - 1
        result, err := f.owner.InvokeUnifiedContract(req)
        if err != nil {
            require.Nil(t, result)
            require.Regexp(t, "(?i)(gas|limit)", err.Error())
            return
        }
        t.Errorf("contract-review: below-base gas request was accepted by SDK and node mempool")
        _, _ = f.mined(t, result)
    })
}
