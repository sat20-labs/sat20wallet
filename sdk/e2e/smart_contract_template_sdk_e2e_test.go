package e2e

import (
    "encoding/json"
    "fmt"
    "strconv"
    "testing"
    "time"

    indexer "github.com/sat20-labs/indexer/common"
    "github.com/sat20-labs/sat20wallet/sdk/wallet"
    contract "github.com/sat20-labs/satoshinet/contract"
    "github.com/stretchr/testify/require"
)

func sdkReviewJSON(t *testing.T, value any) string {
    t.Helper(); data, err := json.Marshal(value); require.NoError(t, err); return string(data)
}

func (f *sdkContractReviewFixture) deployTemplate(t *testing.T, name string, content any, value int64, assets []wallet.ContractFundingAsset) string {
    t.Helper()
    var text string
    if raw, ok := content.(string); ok { text = raw } else { text = sdkReviewJSON(t, content) }
    encoded, err := wallet.BuildUnifiedContractContent(wallet.ContractTypeTemplate, name, text)
    require.NoError(t, err, "contract-review: SDK template content encoder")
    req := &wallet.ContractDeployRequest{ContractType: wallet.ContractTypeTemplate, SubType: name,
        ContractContent: encoded, ContentEncoding: "base64", GasLimit: contract.DeployBaseGas, FundingValue: value, Assets: assets}
    result, err := f.owner.DeployUnifiedContract(req)
    require.NoError(t, err, "contract-review: SDK template deployment")
    work, block := f.mined(t, result)
    require.Equal(t, contract.ResultStatusSuccess, sdkReviewResultStatus(t, work, block))
    text, err = f.owner.QueryContract(&wallet.ContractQueryRequest{Query: wallet.ContractQueryInfo, Contract: result.ContractAddress})
    require.NoError(t, err)
    require.Contains(t, text, name, "contract-review: deployment produced no live template")
    return result.ContractAddress
}

func sdkReviewTemplateCall(t *testing.T, name, address, action string, param any) *wallet.ContractInvokeRequest {
    t.Helper()
    inner := ""
    if param != nil { inner = sdkReviewJSON(t, param) }
    encoded, err := wallet.ConvertUnifiedInvokeParam(wallet.ContractTypeTemplate, name,
        sdkReviewJSON(t, wallet.InvokeParam{Action: action, Param: inner}))
    require.NoError(t, err, "contract-review: SDK invoke parameter encoder")
    return &wallet.ContractInvokeRequest{ContractType: wallet.ContractTypeTemplate, SubType: name,
        ContractAddress: address, Action: encoded.Action, Param: encoded.Param, ParamEncoding: "base64", GasLimit: contract.InvokeBaseGas}
}

func sdkReviewStateField(t *testing.T, manager *wallet.Manager, address, name string) any {
    t.Helper()
    state := sdkReviewQueryState(t, manager, address)
    value, ok := sdkReviewFindJSON(state, name)
    require.True(t, ok, "contract-review: missing state field %s: %.1300s", name, fmt.Sprint(state))
    return value
}

// These specific query fields use omitempty. Absence is a documented zero
// representation, not evidence that the whole state query may be omitted.
func sdkReviewOptionalAmount(t *testing.T, state map[string]any, field string) int64 {
    t.Helper()
    value, ok := sdkReviewFindJSON(state, field)
    if !ok || value == "" { return 0 }
    text, ok := value.(string); require.True(t, ok)
    amount, err := strconv.ParseInt(text, 10, 64); require.NoError(t, err)
    return amount
}

func TestSDKSmartContractsTemplates(t *testing.T) {
    f := newSDKContractReviewFixture(t)
    t.Run("limitorder", func(t *testing.T) {
        c := wallet.NewContract(wallet.TEMPLATE_CONTRACT_LIMITORDER); require.NotNil(t, c)
        c.GetContractBase().AssetName = *indexer.NewAssetNameFromString(reviewAssetA)
        address := f.deployTemplate(t, contract.TemplateLimitOrder, string(c.Content()), 0, nil)
        t.Run("basic_sell_partial_buy_and_exact_recipient", func(t *testing.T) {
            sell := sdkReviewTemplateCall(t, contract.TemplateLimitOrder, address, contract.TemplateInvokeAPISwap,
                contract.TemplateLimitOrderInvokeParam{OrderType: contract.OrderTypeSell, AssetName: reviewAssetA, Amt: "10", UnitPrice: "10"})
            sell.Assets = []wallet.ContractFundingAsset{{AssetName: reviewAssetA, Amount: "10"}}
            f.invoke(t, f.owner, sell, contract.ResultStatusSuccess)
            require.Equal(t, float64(1), sdkReviewStateField(t, f.owner, address, "activeSellCount"))
            buy := sdkReviewTemplateCall(t, contract.TemplateLimitOrder, address, contract.TemplateInvokeAPISwap,
                contract.TemplateLimitOrderInvokeParam{OrderType: contract.OrderTypeBuy, AssetName: reviewAssetA, Amt: "4", UnitPrice: "10"})
            buy.Value = 40
            _, block := f.invoke(t, f.other, buy, contract.ResultStatusSuccess)
            require.EqualValues(t, 4, sdkReviewReturnedAsset(block, f.other.GetWallet().GetAddress(), reviewAssetA))
            require.Equal(t, float64(1), sdkReviewStateField(t, f.reader, address, "activeSellCount"))
        })
        t.Run("invalid_order_funding_cannot_create_phantom_order", func(t *testing.T) {
            before := sdkReviewStateField(t, f.owner, address, "activeSellCount")
            req := sdkReviewTemplateCall(t, contract.TemplateLimitOrder, address, contract.TemplateInvokeAPISwap,
                contract.TemplateLimitOrderInvokeParam{OrderType: contract.OrderTypeSell, AssetName: reviewAssetA, Amt: "50000", UnitPrice: "10"})
            req.Assets = []wallet.ContractFundingAsset{{AssetName: reviewAssetA, Amount: "2"}}
            _, block := f.invoke(t, f.owner, req, contract.ResultStatusSuccess)
            require.Equal(t, before, sdkReviewStateField(t, f.owner, address, "activeSellCount"))
            require.EqualValues(t, 2, sdkReviewReturnedAsset(block, f.owner.GetWallet().GetAddress(), reviewAssetA))
        })
        t.Run("non_deployer_close_preserves_open_orders", func(t *testing.T) {
            before := sdkReviewStateField(t, f.owner, address, "activeSellCount")
            req := sdkReviewTemplateCall(t, contract.TemplateLimitOrder, address, contract.TemplateInvokeAPIClose, nil)
            f.invoke(t, f.other, req, contract.ResultStatusSuccess)
            require.Equal(t, before, sdkReviewStateField(t, f.owner, address, "activeSellCount"))
        })
        t.Run("deployer_close_refunds_unfilled_principal", func(t *testing.T) {
            req := sdkReviewTemplateCall(t, contract.TemplateLimitOrder, address, contract.TemplateInvokeAPIClose, nil)
            _, block := f.invoke(t, f.owner, req, contract.ResultStatusSuccess)
            require.EqualValues(t, 6, sdkReviewReturnedAsset(block, f.owner.GetWallet().GetAddress(), reviewAssetA), "contract-review: close did not return the unfilled 6 units")
        })
    })
    t.Run("amm", func(t *testing.T) {
        c := wallet.NewAmmContract(); c.AssetName = *indexer.NewAssetNameFromString(reviewAssetB)
        c.AssetAmt, c.SatValue, c.K = "100", 1000, "100000"
        address := f.deployTemplate(t, contract.TemplateAMM, string(c.Content()), 1000,
            []wallet.ContractFundingAsset{{AssetName: reviewAssetB, Amount: "100"}})
        t.Run("basic_initial_pool_and_add_liquidity", func(t *testing.T) {
            require.Equal(t, "100", sdkReviewStateField(t, f.owner, address, "assetAInPool"))
            req := sdkReviewTemplateCall(t, contract.TemplateAMM, address, contract.TemplateInvokeAPIAddLiquidity,
                contract.TemplateAddLiquidityInvokeParam{OrderType: contract.OrderTypeAddLiquidity, AssetName: reviewAssetB, Amt: "50", Value: 500})
            req.Assets = []wallet.ContractFundingAsset{{AssetName: reviewAssetB, Amount: "50"}}; req.Value = 500
            f.invoke(t, f.owner, req, contract.ResultStatusSuccess)
            require.Equal(t, "150", sdkReviewStateField(t, f.owner, address, "assetAInPool"))
            require.Equal(t, "1500", sdkReviewStateField(t, f.reader, address, "assetBInPool"))
        })
        t.Run("non_LP_cannot_withdraw_liquidity", func(t *testing.T) {
            before := sdkReviewStateField(t, f.owner, address, "assetAInPool")
            req := sdkReviewTemplateCall(t, contract.TemplateAMM, address, contract.TemplateInvokeAPIRemoveLiquidity,
                contract.TemplateRemoveLiquidityInvokeParam{OrderType: contract.OrderTypeRemoveLiquidity, AssetName: reviewAssetB, LptAmt: "1"})
            _, block := f.invoke(t, f.other, req, contract.ResultStatusSuccess)
            require.Equal(t, before, sdkReviewStateField(t, f.owner, address, "assetAInPool"))
            require.Zero(t, sdkReviewReturnedAsset(block, f.other.GetWallet().GetAddress(), reviewAssetB))
        })
        t.Run("unsupported_refund_rejected_by_SDK", func(t *testing.T) {
            _, err := f.owner.InvokeUnifiedContract(&wallet.ContractInvokeRequest{ContractType: wallet.ContractTypeTemplate, SubType: contract.TemplateAMM,
                ContractAddress: address, Action: contract.TemplateInvokeAPIRefund, Param: "{}", ParamEncoding: "json", GasLimit: contract.InvokeBaseGas})
            require.Error(t, err)
        })
        t.Run("owner_withdraws_all_LP_without_profit_haircut", func(t *testing.T) {
            balances := sdkReviewStateField(t, f.owner, address, "lpBalances").(map[string]any)
            amount, ok := balances[f.owner.GetWallet().GetAddress()].(string); require.True(t, ok); require.NotEmpty(t, amount)
            req := sdkReviewTemplateCall(t, contract.TemplateAMM, address, contract.TemplateInvokeAPIRemoveLiquidity,
                contract.TemplateRemoveLiquidityInvokeParam{OrderType: contract.OrderTypeRemoveLiquidity, AssetName: reviewAssetB, LptAmt: amount})
            _, block := f.invoke(t, f.owner, req, contract.ResultStatusSuccess)
            require.EqualValues(t, 150, sdkReviewReturnedAsset(block, f.owner.GetWallet().GetAddress(), reviewAssetB))
            requireTxOutputValueAmount(t, contractResultTxs(block)[0], f.owner.GetWallet().GetAddress(), 1500)
            require.Zero(t, sdkReviewOptionalAmount(t, sdkReviewQueryState(t, f.owner, address), "assetAInPool"))
        })
    })
    t.Run("exchange", func(t *testing.T) {
        content := contract.TemplateExchangeContract{AssetAName: reviewAssetA, AssetBName: contract.SatoshiAssetName,
            PriceMode: contract.ExchangePriceModeHeight, Steps: []contract.TemplateExchangePriceStep{{Threshold: "0", BPerA: "10"}}}
        address := f.deployTemplate(t, contract.TemplateExchange, content, 0, []wallet.ContractFundingAsset{{AssetName: reviewAssetA, Amount: "100"}})
        t.Run("basic_exchange_exact_asset_output", func(t *testing.T) {
            req := sdkReviewTemplateCall(t, contract.TemplateExchange, address, contract.TemplateInvokeAPIExchange, contract.TemplateExchangeInvokeParam{MinOutA: "4"}); req.Value = 40
            _, block := f.invoke(t, f.other, req, contract.ResultStatusSuccess)
            require.EqualValues(t, 4, sdkReviewReturnedAsset(block, f.other.GetWallet().GetAddress(), reviewAssetA))
        })
        t.Run("minimum_output_guard_refunds_without_asset_payout", func(t *testing.T) {
            req := sdkReviewTemplateCall(t, contract.TemplateExchange, address, contract.TemplateInvokeAPIExchange, contract.TemplateExchangeInvokeParam{MinOutA: "1000"}); req.Value = 40
            _, block := f.invoke(t, f.other, req, contract.ResultStatusSuccess)
            require.Zero(t, sdkReviewReturnedAsset(block, f.other.GetWallet().GetAddress(), reviewAssetA))
            requireTxOutputValueAmount(t, contractResultTxs(block)[0], f.other.GetWallet().GetAddress(), 40)
        })
    })
    t.Run("autopay", func(t *testing.T) {
        content := contract.TemplateAutopayContract{ServiceName: "sdk-review", Recipient: f.other.GetWallet().GetAddress(), FeeAssetName: reviewAssetB, MinAmountPerBlock: "1"}
        address := f.deployTemplate(t, contract.TemplateAutopay, content, 0, []wallet.ContractFundingAsset{{AssetName: reviewAssetB, Amount: "100"}})
        t.Run("basic_deployer_principal_registered", func(t *testing.T) {
            delegates := sdkReviewStateField(t, f.owner, address, "delegates").(map[string]any)
            row, ok := delegates[f.owner.GetWallet().GetAddress()].(map[string]any); require.True(t, ok)
            require.Equal(t, "100", row["balance"])
            require.Zero(t, sdkReviewOptionalAmount(t, sdkReviewQueryState(t, f.owner, address), "gasBalance"))
        })
        t.Run("unauthorized_operating_reserve_configuration_is_rejected", func(t *testing.T) {
            before := sdkReviewOptionalAmount(t, sdkReviewQueryState(t, f.owner, address), "gasBalance")
            req := sdkReviewTemplateCall(t, contract.TemplateAutopay, address, contract.TemplateInvokeAPIConfig, contract.TemplateAutopayConfigInvokeParam{GasFundingAmount: "100"})
            req.GasAssetAmount = 1000
            f.invoke(t, f.other, req, contract.ResultStatusSuccess)
            require.Equal(t, before, sdkReviewOptionalAmount(t, sdkReviewQueryState(t, f.owner, address), "gasBalance"))
        })
        t.Run("operator_funding_enables_real_payments_without_spending_principal_as_gas", func(t *testing.T) {
            req := sdkReviewTemplateCall(t, contract.TemplateAutopay, address, contract.TemplateInvokeAPIConfig, contract.TemplateAutopayConfigInvokeParam{GasFundingAmount: "4000"})
            req.GasAssetAmount = 6000
            f.invoke(t, f.owner, req, contract.ResultStatusSuccess)
            require.Eventually(t, func() bool {
                state := sdkReviewQueryState(t, f.owner, address)
                delegates, _ := sdkReviewFindJSON(state, "delegates")
                row := delegates.(map[string]any)[f.owner.GetWallet().GetAddress()].(map[string]any)
                paid := sdkReviewOptionalAmount(t, row, "totalPaid")
                balance := sdkReviewOptionalAmount(t, row, "balance")
                require.EqualValues(t, 100, paid+balance, "contract-review: delegate principal used as operating gas")
                return paid > 0
            }, 10*time.Second, 150*time.Millisecond)
        })
        t.Run("cancel_refunds_remaining_principal_and_cannot_pay_twice", func(t *testing.T) {
            req := sdkReviewTemplateCall(t, contract.TemplateAutopay, address, contract.TemplateInvokeAPICancel, nil)
            _, block := f.invoke(t, f.owner, req, contract.ResultStatusSuccess)
            delegates := sdkReviewStateField(t, f.owner, address, "delegates").(map[string]any)
            row := delegates[f.owner.GetWallet().GetAddress()].(map[string]any)
            refund := sdkReviewReturnedAsset(block, f.owner.GetWallet().GetAddress(), reviewAssetB)
            require.Positive(t, refund)
            require.EqualValues(t, 100, refund+sdkReviewOptionalAmount(t, row, "totalPaid"))
            require.Zero(t, sdkReviewOptionalAmount(t, row, "balance"))
            _, repeated := f.invoke(t, f.owner, req, contract.ResultStatusSuccess)
            require.Zero(t, sdkReviewReturnedAsset(repeated, f.owner.GetWallet().GetAddress(), reviewAssetB))
        })
    })
}
