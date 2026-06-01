package wallet

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const unifiedTemplateTestAsset = "brc20:f:ooxx"

func TestUnifiedTemplateContractsLocalCoverage(t *testing.T) {
	manager := &Manager{}

	limitOrder := NewContract(TEMPLATE_CONTRACT_LIMITORDER)
	if limitOrder == nil {
		t.Fatalf("NewContract(%s) returned nil", TEMPLATE_CONTRACT_LIMITORDER)
	}
	limitOrder.GetContractBase().AssetName = *indexer.NewAssetNameFromString(unifiedTemplateTestAsset)
	assertTemplateContractContent(t, manager, TEMPLATE_CONTRACT_LIMITORDER, limitOrder)
	assertSwapInvokeParamRoundTrip(t, manager, TEMPLATE_CONTRACT_LIMITORDER, SwapInvokeParam{
		OrderType: ORDERTYPE_SELL,
		AssetName: unifiedTemplateTestAsset,
		Amt:       "1000",
		UnitPrice: "2",
	})

	legacyLimitOrder := NewContract(TEMPLATE_CONTRACT_SWAP)
	if legacyLimitOrder == nil {
		t.Fatalf("NewContract(%s) returned nil", TEMPLATE_CONTRACT_SWAP)
	}
	legacyLimitOrder.GetContractBase().AssetName = *indexer.NewAssetNameFromString(unifiedTemplateTestAsset)
	assertTemplateContractContent(t, manager, TEMPLATE_CONTRACT_SWAP, legacyLimitOrder)

	amm := NewAmmContract()
	amm.AssetName = *indexer.NewAssetNameFromString(unifiedTemplateTestAsset)
	amm.AssetAmt = "100000"
	amm.SatValue = 1000
	amm.K = "100000000"
	assertTemplateContractContent(t, manager, TEMPLATE_CONTRACT_AMM, amm)
	assertSwapInvokeParamRoundTrip(t, manager, TEMPLATE_CONTRACT_AMM, SwapInvokeParam{
		OrderType: ORDERTYPE_BUY,
		AssetName: unifiedTemplateTestAsset,
		Amt:       "100",
		UnitPrice: "3",
	})
	assertAddLiquidityInvokeParamRoundTrip(t, manager, AddLiqInvokeParam{
		OrderType: ORDERTYPE_ADDLIQUIDITY,
		AssetName: unifiedTemplateTestAsset,
		Amt:       "5000",
		Value:     50,
	})
	assertRemoveLiquidityInvokeParamRoundTrip(t, manager, RemoveLiqInvokeParam{
		OrderType: ORDERTYPE_REMOVELIQUIDITY,
		AssetName: unifiedTemplateTestAsset,
		LptAmt:    "25",
	})
}

func TestUnifiedNativeTemplateTxCoverage(t *testing.T) {
	limitOrder := tmplcontract.NewLimitOrderContract(unifiedTemplateTestAsset)
	limitAddr, limitRuntime := assertNativeTemplateDeployTx(t, limitOrder, "limit-order")
	assertNativeTemplateInvokeTx(t, limitAddr, limitRuntime, tmplcontract.InvokeAPISwap, mustEncodeTemplateParam((&tmplcontract.LimitOrderInvokeParam{
		OrderType: tmplcontract.OrderTypeSell,
		AssetName: unifiedTemplateTestAsset,
		Amt:       "1000",
		UnitPrice: "2",
	}).Encode()))
	assertNativeTemplateInvokeTx(t, limitAddr, limitRuntime, tmplcontract.InvokeAPIRefund, mustEncodeTemplateParam((&tmplcontract.RefundInvokeParam{ItemIDs: []int64{1, 2}}).Encode()))

	amm := tmplcontract.NewAMMContract(unifiedTemplateTestAsset, "100000", 1000, "100000000")
	ammAddr, ammRuntime := assertNativeTemplateDeployTx(t, amm, "amm")
	assertNativeTemplateInvokeTx(t, ammAddr, ammRuntime, tmplcontract.InvokeAPISwap, mustEncodeTemplateParam((&tmplcontract.LimitOrderInvokeParam{
		OrderType: tmplcontract.OrderTypeBuy,
		AssetName: unifiedTemplateTestAsset,
		Amt:       "100",
		UnitPrice: "3",
	}).Encode()))
	assertNativeTemplateInvokeTx(t, ammAddr, ammRuntime, tmplcontract.InvokeAPIAddLiquidity, mustEncodeTemplateParam((&tmplcontract.AddLiquidityInvokeParam{
		OrderType: tmplcontract.OrderTypeAddLiquidity,
		AssetName: unifiedTemplateTestAsset,
		Amt:       "5000",
		Value:     50,
	}).Encode()))
	assertNativeTemplateInvokeTx(t, ammAddr, ammRuntime, tmplcontract.InvokeAPIRemoveLiquidity, mustEncodeTemplateParam((&tmplcontract.RemoveLiquidityInvokeParam{
		OrderType: tmplcontract.OrderTypeRemoveLiquidity,
		AssetName: unifiedTemplateTestAsset,
		LptAmt:    "25",
	}).Encode()))
	assertNativeTemplateInvokeRejected(t, ammRuntime, tmplcontract.InvokeAPIRefund, mustEncodeTemplateParam((&tmplcontract.RefundInvokeParam{ItemIDs: []int64{3}}).Encode()))

	exchange := tmplcontract.NewExchangeContract(contractcommon.GasAssetName, indexer.ASSET_PLAIN_SAT.String(), tmplcontract.ExchangePriceModeHeight, []tmplcontract.ExchangePriceStep{{
		Threshold: "0",
		BPerA:     "0.0001",
	}})
	exchangeAddr, exchangeRuntime := assertNativeTemplateDeployTx(t, exchange, "exchange")
	assertNativeTemplateInvokeTx(t, exchangeAddr, exchangeRuntime, tmplcontract.InvokeAPIExchange, mustEncodeTemplateParam((&tmplcontract.ExchangeInvokeParam{MinOutA: "1"}).Encode()))
	assertNativeTemplateInvokeTx(t, exchangeAddr, exchangeRuntime, tmplcontract.InvokeAPIClose, nil)
}

func TestBuildNativeTemplateExchangeContract(t *testing.T) {
	exchange := tmplcontract.NewExchangeContract(contractcommon.GasAssetName, indexer.ASSET_PLAIN_SAT.String(), tmplcontract.ExchangePriceModeHeight, []tmplcontract.ExchangePriceStep{{
		Threshold: "0",
		BPerA:     "0.0001",
	}, {
		Threshold: "100",
		BPerA:     "0.0001111111",
	}})
	content, err := json.Marshal(exchange)
	if err != nil {
		t.Fatalf("marshal exchange content: %v", err)
	}
	contract, fundingValue, fundingAssetAmount, err := (&Manager{}).buildNativeTemplateContract(&TemplateContractDeployRequest{
		TemplateName:    TEMPLATE_CONTRACT_EXCHANGE,
		ContractContent: string(content),
	})
	if err != nil {
		t.Fatalf("buildNativeTemplateContract(exchange): %v", err)
	}
	if contract.TemplateName() != tmplcontract.TemplateExchange {
		t.Fatalf("unexpected template %s", contract.TemplateName())
	}
	if fundingValue != 0 || fundingAssetAmount != 0 {
		t.Fatalf("unexpected exchange deploy funding value=%d asset=%d", fundingValue, fundingAssetAmount)
	}
}

func assertTemplateContractContent(t *testing.T, manager *Manager, templateName string, contract Contract) {
	t.Helper()
	content := string(contract.Content())
	var contentJSON struct {
		ContractType string `json:"contractType"`
		AssetName    struct {
			Protocol string `json:"Protocol"`
			Type     string `json:"Type"`
			Ticker   string `json:"Ticker"`
		} `json:"assetName"`
	}
	if err := json.Unmarshal([]byte(content), &contentJSON); err != nil {
		t.Fatalf("unmarshal %s content: %v", templateName, err)
	}
	if contentJSON.ContractType != templateName {
		t.Fatalf("unexpected %s contract type %s", templateName, contentJSON.ContractType)
	}
	if got := contentJSON.AssetName.Protocol + ":" + contentJSON.AssetName.Type + ":" + contentJSON.AssetName.Ticker; got != unifiedTemplateTestAsset {
		t.Fatalf("unexpected %s asset %s", templateName, got)
	}
	parsed, err := ContractContentUnMarsh(templateName, content)
	if err != nil {
		t.Fatalf("ContractContentUnMarsh(%s): %v", templateName, err)
	}
	if parsed.GetTemplateName() != templateName {
		t.Fatalf("unexpected parsed template %s", parsed.GetTemplateName())
	}
	fee, err := manager.QueryFeeForDeployContract(templateName, content, 1)
	if err != nil {
		t.Fatalf("QueryFeeForDeployContract(%s): %v", templateName, err)
	}
	if fee <= 0 {
		t.Fatalf("expected positive deploy fee for %s, got %d", templateName, fee)
	}
}

func assertSwapInvokeParamRoundTrip(t *testing.T, manager *Manager, templateName string, param SwapInvokeParam) {
	t.Helper()
	invokeJSON := mustInvokeJSON(t, INVOKE_API_SWAP, param)
	converted, err := ConvertInvokeParam(invokeJSON, false)
	if err != nil {
		t.Fatalf("ConvertInvokeParam(%s swap): %v", templateName, err)
	}
	if converted.Action != INVOKE_API_SWAP {
		t.Fatalf("unexpected action %s", converted.Action)
	}
	encoded, err := base64.StdEncoding.DecodeString(converted.Param)
	if err != nil {
		t.Fatalf("decode swap param: %v", err)
	}
	var decoded SwapInvokeParam
	if err := decoded.Decode(encoded); err != nil {
		t.Fatalf("decode swap script: %v", err)
	}
	if decoded.OrderType != param.OrderType || decoded.AssetName != param.AssetName || decoded.Amt != param.Amt || decoded.UnitPrice != param.UnitPrice {
		t.Fatalf("unexpected decoded swap param %+v", decoded)
	}
	if _, err := manager.QueryParamForInvokeContract(templateName, INVOKE_API_SWAP); err != nil {
		t.Fatalf("QueryParamForInvokeContract(%s, swap): %v", templateName, err)
	}
}

func assertAddLiquidityInvokeParamRoundTrip(t *testing.T, manager *Manager, param AddLiqInvokeParam) {
	t.Helper()
	invokeJSON := mustInvokeJSON(t, INVOKE_API_ADDLIQUIDITY, param)
	converted, err := ConvertInvokeParam(invokeJSON, false)
	if err != nil {
		t.Fatalf("ConvertInvokeParam(addliq): %v", err)
	}
	encoded, err := base64.StdEncoding.DecodeString(converted.Param)
	if err != nil {
		t.Fatalf("decode addliq param: %v", err)
	}
	var decoded AddLiqInvokeParam
	if err := decoded.Decode(encoded); err != nil {
		t.Fatalf("decode addliq script: %v", err)
	}
	if decoded.OrderType != param.OrderType || decoded.AssetName != param.AssetName || decoded.Amt != param.Amt || decoded.Value != param.Value {
		t.Fatalf("unexpected decoded addliq param %+v", decoded)
	}
	if _, err := manager.QueryParamForInvokeContract(TEMPLATE_CONTRACT_AMM, INVOKE_API_ADDLIQUIDITY); err != nil {
		t.Fatalf("QueryParamForInvokeContract(amm, addliq): %v", err)
	}
}

func assertRemoveLiquidityInvokeParamRoundTrip(t *testing.T, manager *Manager, param RemoveLiqInvokeParam) {
	t.Helper()
	invokeJSON := mustInvokeJSON(t, INVOKE_API_REMOVELIQUIDITY, param)
	converted, err := ConvertInvokeParam(invokeJSON, false)
	if err != nil {
		t.Fatalf("ConvertInvokeParam(removeliq): %v", err)
	}
	encoded, err := base64.StdEncoding.DecodeString(converted.Param)
	if err != nil {
		t.Fatalf("decode removeliq param: %v", err)
	}
	var decoded RemoveLiqInvokeParam
	if err := decoded.Decode(encoded); err != nil {
		t.Fatalf("decode removeliq script: %v", err)
	}
	if decoded.OrderType != param.OrderType || decoded.AssetName != param.AssetName || decoded.LptAmt != param.LptAmt {
		t.Fatalf("unexpected decoded removeliq param %+v", decoded)
	}
	if _, err := manager.QueryParamForInvokeContract(TEMPLATE_CONTRACT_AMM, INVOKE_API_REMOVELIQUIDITY); err != nil {
		t.Fatalf("QueryParamForInvokeContract(amm, removeliq): %v", err)
	}
}

func assertNativeTemplateDeployTx(t *testing.T, contract tmplcontract.Contract, randomSuffix string) (tmplcontract.ContractAddress, *tmplcontract.ContractRuntime) {
	t.Helper()
	tx, addr, err := tmplcontract.BuildDeployTx(tmplcontract.DeployTxBuildRequest{
		ContractPrefix: contractcommon.TestnetContractPrefix,
		Contract:       contract,
		Deployer:       "tb1ptestdeployer000000000000000000000000000000",
		Random:         []byte("sdk-" + randomSuffix),
		GasLimit:       contractcommon.DeployBaseGas,
		Funding:        testTemplateFundingTxOut(100000),
		Inputs:         []swire.OutPoint{testTemplateOutPoint(randomSuffix)},
	})
	if err != nil {
		t.Fatalf("BuildDeployTx(%s): %v", contract.TemplateName(), err)
	}
	parsed, err := tmplcontract.ParseTx(tx, tmplcontract.StandardContractScriptResolver(contractcommon.TestnetContractPrefix))
	if err != nil {
		t.Fatalf("ParseTx deploy(%s): %v", contract.TemplateName(), err)
	}
	if parsed.Type != tmplcontract.TxTypeDeploy || parsed.Deploy == nil {
		t.Fatalf("unexpected parsed deploy tx %+v", parsed)
	}
	validated, err := tmplcontract.ValidateDeployTxBasic(tx, contractcommon.TestnetContractPrefix, tmplcontract.NewDefaultRegistry(), tmplcontract.DefaultGasConfig())
	if err != nil {
		t.Fatalf("ValidateDeployTxBasic(%s): %v", contract.TemplateName(), err)
	}
	if !validated.Address.Equal(addr) {
		t.Fatalf("deploy address mismatch %s != %s", validated.Address.EncodeAddress(), addr.EncodeAddress())
	}
	if validated.Runtime.TemplateName() != contract.TemplateName() {
		t.Fatalf("runtime template mismatch %s != %s", validated.Runtime.TemplateName(), contract.TemplateName())
	}
	return addr, validated.Runtime
}

func assertNativeTemplateInvokeTx(t *testing.T, contract tmplcontract.ContractAddress, runtime *tmplcontract.ContractRuntime, action string, param []byte) {
	t.Helper()
	if err := runtime.CheckInvoke(action, param); err != nil {
		t.Fatalf("runtime CheckInvoke(%s): %v", action, err)
	}
	tx, err := tmplcontract.BuildInvokeTx(tmplcontract.InvokeTxBuildRequest{
		Contract:  contract,
		GasLimit:  contractcommon.InvokeBaseGas,
		CallNonce: 1,
		Action:    action,
		Param:     param,
		Funding:   testTemplateFundingTxOut(20000),
		Inputs:    []swire.OutPoint{testTemplateOutPoint(action)},
	})
	if err != nil {
		t.Fatalf("BuildInvokeTx(%s): %v", action, err)
	}
	validated, err := tmplcontract.ValidateInvokeTxBasic(
		tx,
		tmplcontract.StandardContractScriptResolver(contractcommon.TestnetContractPrefix),
		func(addr tmplcontract.ContractAddress) bool { return addr.Equal(contract) },
		tmplcontract.DefaultGasConfig(),
	)
	if err != nil {
		t.Fatalf("ValidateInvokeTxBasic(%s): %v", action, err)
	}
	if !validated.Contract.Equal(contract) {
		t.Fatalf("invoke contract mismatch %s != %s", validated.Contract.EncodeAddress(), contract.EncodeAddress())
	}
	if validated.Payload.Action != action {
		t.Fatalf("invoke action mismatch %s != %s", validated.Payload.Action, action)
	}
	if len(validated.FundingOutputs) == 0 {
		t.Fatalf("expected funding output for %s", action)
	}
}

func assertNativeTemplateInvokeRejected(t *testing.T, runtime *tmplcontract.ContractRuntime, action string, param []byte) {
	t.Helper()
	if err := runtime.CheckInvoke(action, param); err == nil {
		t.Fatalf("expected %s invoke to be rejected by %s", action, runtime.TemplateName())
	}
}

func testTemplateFundingTxOut(amount int64) swire.TxOut {
	gasName := swire.NewAssetNameFromString(contractcommon.GasAssetName)
	return *swire.NewTxOut(0, swire.TxAssets{{
		Name:   *gasName,
		Amount: *indexer.NewDefaultDecimal(amount),
	}}, nil)
}

func testTemplateOutPoint(seed string) swire.OutPoint {
	hash := chainhash.DoubleHashH([]byte(seed))
	return *swire.NewOutPoint(&hash, 0)
}

func mustInvokeJSON(t *testing.T, action string, param any) string {
	t.Helper()
	inner, err := json.Marshal(param)
	if err != nil {
		t.Fatalf("marshal inner invoke param: %v", err)
	}
	outer, err := json.Marshal(InvokeParam{Action: action, Param: string(inner)})
	if err != nil {
		t.Fatalf("marshal invoke param: %v", err)
	}
	return string(outer)
}

func mustEncodeTemplateParam(param []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return param
}
