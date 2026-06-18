package wallet

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
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
	assertCloseInvokeParamRoundTrip(t)
	assertUnifiedTemplateInvokeParamQuery(t, manager)
}

func TestUnifiedNativeTemplateTxCoverage(t *testing.T) {
	limitContent, err := contractcommon.EncodeTemplateLimitOrderContent(unifiedTemplateTestAsset)
	if err != nil {
		t.Fatalf("EncodeTemplateLimitOrderContent: %v", err)
	}
	limitAddr := assertNativeTemplateDeployTx(t, contractcommon.TemplateLimitOrder, limitContent, "limit-order")
	assertNativeTemplateInvokeTx(t, limitAddr, contractcommon.TemplateInvokeAPISwap, mustEncodeTemplateParam((&contractcommon.TemplateLimitOrderInvokeParam{
		OrderType: contractcommon.OrderTypeSell,
		AssetName: unifiedTemplateTestAsset,
		Amt:       "1000",
		UnitPrice: "2",
	}).Encode()))
	assertNativeTemplateInvokeTx(t, limitAddr, contractcommon.TemplateInvokeAPIRefund, mustEncodeTemplateParam((&contractcommon.TemplateRefundInvokeParam{ItemIDs: []int64{1, 2}}).Encode()))

	ammContent, err := contractcommon.EncodeTemplateAMMContent(unifiedTemplateTestAsset, "100000", 1000, "100000000")
	if err != nil {
		t.Fatalf("EncodeTemplateAMMContent: %v", err)
	}
	ammAddr := assertNativeTemplateDeployTx(t, contractcommon.TemplateAMM, ammContent, "amm")
	assertNativeTemplateInvokeTx(t, ammAddr, contractcommon.TemplateInvokeAPISwap, mustEncodeTemplateParam((&contractcommon.TemplateLimitOrderInvokeParam{
		OrderType: contractcommon.OrderTypeBuy,
		AssetName: unifiedTemplateTestAsset,
		Amt:       "100",
		UnitPrice: "3",
	}).Encode()))
	assertNativeTemplateInvokeTx(t, ammAddr, contractcommon.TemplateInvokeAPIAddLiquidity, mustEncodeTemplateParam((&contractcommon.TemplateAddLiquidityInvokeParam{
		OrderType: contractcommon.OrderTypeAddLiquidity,
		AssetName: unifiedTemplateTestAsset,
		Amt:       "5000",
		Value:     50,
	}).Encode()))
	assertNativeTemplateInvokeTx(t, ammAddr, contractcommon.TemplateInvokeAPIRemoveLiquidity, mustEncodeTemplateParam((&contractcommon.TemplateRemoveLiquidityInvokeParam{
		OrderType: contractcommon.OrderTypeRemoveLiquidity,
		AssetName: unifiedTemplateTestAsset,
		LptAmt:    "25",
	}).Encode()))
	if contractcommon.IsTemplateInvokeActionSupported(contractcommon.TemplateAMM, contractcommon.TemplateInvokeAPIRefund) {
		t.Fatalf("AMM should not support refund invoke")
	}

	exchangeContent, err := contractcommon.EncodeTemplateExchangeContent(contractcommon.TemplateExchangeContract{
		AssetAName: GetGasAssetName(),
		AssetBName: indexer.ASSET_PLAIN_SAT.String(),
		PriceMode:  contractcommon.ExchangePriceModeHeight,
		Steps: []contractcommon.TemplateExchangePriceStep{{
			Threshold: "0",
			BPerA:     "0.0001",
		}},
	})
	if err != nil {
		t.Fatalf("EncodeTemplateExchangeContent: %v", err)
	}
	exchangeAddr := assertNativeTemplateDeployTx(t, contractcommon.TemplateExchange, exchangeContent, "exchange")
	assertNativeTemplateInvokeTx(t, exchangeAddr, contractcommon.TemplateInvokeAPIExchange, mustEncodeTemplateParam((&contractcommon.TemplateExchangeInvokeParam{MinOutA: "1"}).Encode()))
	assertNativeTemplateInvokeTx(t, exchangeAddr, contractcommon.TemplateInvokeAPIClose, nil)
}

func TestBuildNativeTemplateExchangeContract(t *testing.T) {
	exchange := contractcommon.TemplateExchangeContract{
		AssetAName: GetGasAssetName(),
		AssetBName: indexer.ASSET_PLAIN_SAT.String(),
		PriceMode:  contractcommon.ExchangePriceModeHeight,
		Steps: []contractcommon.TemplateExchangePriceStep{{
			Threshold: "0",
			BPerA:     "0.0001",
		}, {
			Threshold: "100",
			BPerA:     "0.0001111111",
		}},
	}
	content, err := json.Marshal(exchange)
	if err != nil {
		t.Fatalf("marshal exchange content: %v", err)
	}
	templateName, encoded, err := encodeTemplateContractContent(TEMPLATE_CONTRACT_EXCHANGE, string(content))
	if err != nil {
		t.Fatalf("encodeTemplateContractContent(exchange): %v", err)
	}
	if templateName != contractcommon.TemplateExchange {
		t.Fatalf("unexpected template %s", templateName)
	}
	if len(encoded) == 0 {
		t.Fatalf("expected encoded exchange content")
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
	converted, err := ConvertUnifiedInvokeParam(ContractTypeTemplate, templateName, invokeJSON)
	if err != nil {
		t.Fatalf("ConvertUnifiedInvokeParam(%s swap): %v", templateName, err)
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
	converted, err := ConvertUnifiedInvokeParam(ContractTypeTemplate, TEMPLATE_CONTRACT_AMM, invokeJSON)
	if err != nil {
		t.Fatalf("ConvertUnifiedInvokeParam(addliq): %v", err)
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
	converted, err := ConvertUnifiedInvokeParam(ContractTypeTemplate, TEMPLATE_CONTRACT_AMM, invokeJSON)
	if err != nil {
		t.Fatalf("ConvertUnifiedInvokeParam(removeliq): %v", err)
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

func assertCloseInvokeParamRoundTrip(t *testing.T) {
	t.Helper()
	for _, invokeJSON := range []string{
		mustInvokeJSON(t, INVOKE_API_CLOSE, contractcommon.TemplateCloseInvokeParam{}),
		mustInvokeJSONWithRawParam(t, INVOKE_API_CLOSE, ""),
	} {
		converted, err := ConvertUnifiedInvokeParam(ContractTypeTemplate, TEMPLATE_CONTRACT_EXCHANGE, invokeJSON)
		if err != nil {
			t.Fatalf("ConvertUnifiedInvokeParam(close): %v", err)
		}
		if converted.Action != INVOKE_API_CLOSE {
			t.Fatalf("unexpected close action %s", converted.Action)
		}
		encoded, err := base64.StdEncoding.DecodeString(converted.Param)
		if err != nil {
			t.Fatalf("decode close param: %v", err)
		}
		var decoded contractcommon.TemplateCloseInvokeParam
		if err := decoded.Decode(encoded); err != nil {
			t.Fatalf("decode close script: %v", err)
		}
	}
}

func assertUnifiedTemplateInvokeParamQuery(t *testing.T, manager *Manager) {
	t.Helper()
	tests := []struct {
		subtype string
		action  string
		fields  []string
	}{
		{TEMPLATE_CONTRACT_LIMITORDER, INVOKE_API_SWAP, []string{"orderType", "assetName", "amt", "unitPrice"}},
		{TEMPLATE_CONTRACT_LIMITORDER, INVOKE_API_REFUND, []string{"itemIds"}},
		{TEMPLATE_CONTRACT_AMM, INVOKE_API_ADDLIQUIDITY, []string{"orderType", "assetName", "amt", "value"}},
		{TEMPLATE_CONTRACT_AMM, INVOKE_API_REMOVELIQUIDITY, []string{"orderType", "assetName", "lptAmt"}},
		{TEMPLATE_CONTRACT_EXCHANGE, contractcommon.TemplateInvokeAPIExchange, []string{"minOutA"}},
		{TEMPLATE_CONTRACT_EXCHANGE, INVOKE_API_CLOSE, nil},
	}
	for _, tt := range tests {
		paramJSON, err := manager.QueryParamForInvokeUnifiedContract(ContractTypeTemplate, tt.subtype, tt.action)
		if err != nil {
			t.Fatalf("QueryParamForInvokeUnifiedContract(%s, %s): %v", tt.subtype, tt.action, err)
		}
		var wrapper InvokeParam
		if err := json.Unmarshal([]byte(paramJSON), &wrapper); err != nil {
			t.Fatalf("unmarshal unified invoke wrapper: %v", err)
		}
		if wrapper.Action != tt.action {
			t.Fatalf("unexpected unified action %s", wrapper.Action)
		}
		if len(tt.fields) == 0 {
			if wrapper.Param != "" {
				t.Fatalf("expected empty close param, got %q", wrapper.Param)
			}
			continue
		}
		var fields map[string]interface{}
		if err := json.Unmarshal([]byte(wrapper.Param), &fields); err != nil {
			t.Fatalf("unmarshal unified inner param: %v", err)
		}
		for _, field := range tt.fields {
			if _, ok := fields[field]; !ok {
				t.Fatalf("missing field %s in %s", field, wrapper.Param)
			}
		}
	}
	assertUnifiedAgentInvokeParamQuery(t, manager)
	assertUnifiedEVMInvokeParamQuery(t, manager)
}

func assertUnifiedAgentInvokeParamQuery(t *testing.T, manager *Manager) {
	t.Helper()
	for _, action := range []string{contractcommon.AgentInvokeAPIReady, contractcommon.AgentInvokeAPIBet, contractcommon.AgentInvokeAPIConfirm, contractcommon.AgentInvokeAPIReject} {
		paramJSON, err := manager.QueryParamForInvokeUnifiedContract(ContractTypeAgent, contractcommon.SubtypePrediction, action)
		if err != nil {
			t.Fatalf("QueryParamForInvokeUnifiedContract(agent, %s): %v", action, err)
		}
		var wrapper InvokeParam
		if err := json.Unmarshal([]byte(paramJSON), &wrapper); err != nil {
			t.Fatalf("unmarshal agent invoke wrapper: %v", err)
		}
		if wrapper.Action != action {
			t.Fatalf("unexpected agent action %s", wrapper.Action)
		}
	}
	invokeJSON := mustInvokeJSON(t, contractcommon.AgentInvokeAPIBet, contractcommon.AgentPredictionBetParam{OutcomeID: "a"})
	converted, err := ConvertUnifiedInvokeParam(ContractTypeAgent, contractcommon.SubtypePrediction, invokeJSON)
	if err != nil {
		t.Fatalf("ConvertUnifiedInvokeParam(agent bet): %v", err)
	}
	encoded, err := base64.StdEncoding.DecodeString(converted.Param)
	if err != nil {
		t.Fatalf("decode agent bet param: %v", err)
	}
	decoded, err := contractcommon.DecodeAgentPredictionBetParam(encoded)
	if err != nil {
		t.Fatalf("decode agent bet payload: %v", err)
	}
	if decoded.OutcomeID != "a" {
		t.Fatalf("unexpected agent outcome %s", decoded.OutcomeID)
	}
}

func TestQueryAgentInvokeFeeDoesNotIncludeBetAmount(t *testing.T) {
	manager := &Manager{}
	invokeJSON := mustInvokeJSON(t, contractcommon.AgentInvokeAPIBet, contractcommon.AgentPredictionBetParam{OutcomeID: "a"})
	fee, err := manager.QueryFeeForInvokeUnifiedContract(&ContractInvokeRequest{
		ContractType: ContractTypeAgent,
		Action:       contractcommon.AgentInvokeAPIBet,
		Param:        mustInvokeInnerParam(t, invokeJSON),
		Assets:       []ContractFundingAsset{{AssetName: contractcommon.SatoshiAssetName, Amount: "1000"}},
	})
	if err != nil {
		t.Fatalf("QueryFeeForInvokeUnifiedContract(agent bet): %v", err)
	}
	want, _, err := manager.agentGasAssetAmount(contractcommon.InvokeBaseGas, 0)
	if err != nil {
		t.Fatalf("agentGasAssetAmount: %v", err)
	}
	if fee != want {
		t.Fatalf("agent bet fee includes bet amount: got %d want %d", fee, want)
	}
}

func assertUnifiedEVMInvokeParamQuery(t *testing.T, manager *Manager) {
	t.Helper()
	paramJSON, err := manager.QueryParamForInvokeUnifiedContract(ContractTypeEVM, "", "call")
	if err != nil {
		t.Fatalf("QueryParamForInvokeUnifiedContract(evm, call): %v", err)
	}
	var wrapper InvokeParam
	if err := json.Unmarshal([]byte(paramJSON), &wrapper); err != nil {
		t.Fatalf("unmarshal evm invoke wrapper: %v", err)
	}
	if wrapper.Action != "call" {
		t.Fatalf("unexpected evm action %s", wrapper.Action)
	}
	invokeJSON := mustInvokeJSON(t, "call", map[string]string{"calldataHex": "0xdeadbeef"})
	converted, err := ConvertUnifiedInvokeParam(ContractTypeEVM, "", invokeJSON)
	if err != nil {
		t.Fatalf("ConvertUnifiedInvokeParam(evm call): %v", err)
	}
	encoded, err := base64.StdEncoding.DecodeString(converted.Param)
	if err != nil {
		t.Fatalf("decode evm call param: %v", err)
	}
	if string(encoded) != string([]byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Fatalf("unexpected evm calldata %x", encoded)
	}
}

func assertNativeTemplateDeployTx(t *testing.T, templateName string, content []byte, randomSuffix string) contractcommon.ContractAddress {
	t.Helper()
	tx, addr, err := contractcommon.BuildDeployTx(contractcommon.DeployTxBuildRequest{
		ContractPrefix:  contractcommon.TestnetContractPrefix,
		Type:            contractcommon.ContractTypeTemplate,
		SubType:         templateName,
		Version:         contractcommon.CurrentTemplateVersion,
		ContractContent: content,
		Deployer:        "tb1ptestdeployer000000000000000000000000000000",
		DeployNonce:     uint64(len(randomSuffix) + 1),
		GasLimit:        contractcommon.DeployBaseGas,
		Funding:         testTemplateFundingTxOut(100000),
		Inputs:          []swire.OutPoint{testTemplateOutPoint(randomSuffix)},
	})
	if err != nil {
		t.Fatalf("BuildDeployTx(%s): %v", templateName, err)
	}
	txType, rawPayload, err := contractcommon.ReadNullDataScript(tx.TxOut[0].PkScript)
	if err != nil {
		t.Fatalf("ReadNullDataScript deploy(%s): %v", templateName, err)
	}
	if txType != contractcommon.TxTypeDeploy {
		t.Fatalf("unexpected deploy tx type %v", txType)
	}
	payload, err := contractcommon.DecodeDeployPayload(rawPayload)
	if err != nil {
		t.Fatalf("DecodeDeployPayload(%s): %v", templateName, err)
	}
	if payload.Type != contractcommon.ContractTypeTemplate {
		t.Fatalf("unexpected deploy contract type %d", payload.Type)
	}
	if payload.SubType != templateName {
		t.Fatalf("template mismatch %s != %s", payload.SubType, templateName)
	}
	derived, _, err := contractcommon.DeriveTemplateContractAddress(
		contractcommon.TestnetContractPrefix, content,
		"tb1ptestdeployer000000000000000000000000000000", payload.DeployNonce)
	if err != nil {
		t.Fatalf("DeriveTemplateContractAddress(%s): %v", templateName, err)
	}
	if !derived.Equal(addr) {
		t.Fatalf("deploy address mismatch %s != %s", derived.EncodeAddress(), addr.EncodeAddress())
	}
	return addr
}

func assertNativeTemplateInvokeTx(t *testing.T, contract contractcommon.ContractAddress, action string, param []byte) {
	t.Helper()
	tx, err := contractcommon.BuildInvokeTx(contractcommon.InvokeTxBuildRequest{
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
	txType, rawPayload, err := contractcommon.ReadNullDataScript(tx.TxOut[0].PkScript)
	if err != nil {
		t.Fatalf("ReadNullDataScript invoke(%s): %v", action, err)
	}
	if txType != contractcommon.TxTypeInvoke {
		t.Fatalf("unexpected invoke tx type %v", txType)
	}
	payload, err := contractcommon.DecodeInvokePayload(rawPayload)
	if err != nil {
		t.Fatalf("DecodeInvokePayload(%s): %v", action, err)
	}
	if payload.Action != action {
		t.Fatalf("invoke action mismatch %s != %s", payload.Action, action)
	}
	contractOut := tx.TxOut[1]
	addr, ok, err := contractcommon.ParseContractPkScript(contractOut.PkScript, contractcommon.TestnetContractPrefix)
	if err != nil {
		t.Fatalf("ParseContractPkScript(%s): %v", action, err)
	}
	if !ok || !addr.Equal(contract) {
		t.Fatalf("invoke contract output mismatch")
	}
	if len(contractOut.Assets) == 0 {
		t.Fatalf("expected funding output for %s", action)
	}
}

func testTemplateFundingTxOut(amount int64) swire.TxOut {
	gasName := swire.NewAssetNameFromString(GetGasAssetName())
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

func mustJSONParam(t *testing.T, param any) string {
	t.Helper()
	data, err := json.Marshal(param)
	if err != nil {
		t.Fatalf("marshal invoke inner param: %v", err)
	}
	return string(data)
}

func mustUnifiedContractContent(t *testing.T, contractType, subtype, jsonContent string) string {
	t.Helper()
	content, err := BuildUnifiedContractContent(contractType, subtype, jsonContent)
	if err != nil {
		t.Fatalf("BuildUnifiedContractContent(%s, %s): %v", contractType, subtype, err)
	}
	return content
}

func mustInvokeInnerParam(t *testing.T, invokeJSON string) string {
	t.Helper()
	var outer InvokeParam
	if err := json.Unmarshal([]byte(invokeJSON), &outer); err != nil {
		t.Fatalf("unmarshal invoke param: %v", err)
	}
	return outer.Param
}

func mustInvokeJSONWithRawParam(t *testing.T, action string, param string) string {
	t.Helper()
	outer, err := json.Marshal(InvokeParam{Action: action, Param: param})
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
