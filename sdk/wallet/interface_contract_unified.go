package wallet

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const (
	ContractTypeTemplate = "template"
	ContractTypeAgent    = "agent"
	ContractTypeEVM      = "evm"

	ContractQueryList    = "list"
	ContractQueryInfo    = "info"
	ContractQueryState   = "state"
	ContractQueryHistory = "history"
)

type ContractQueryRequest struct {
	ContractType string
	Query        string
	Contract     string
	Start        int
	Limit        int
}

type ContractDeployRequest struct {
	ContractType    string
	SubType         string
	Version         uint32
	DeployNonce     uint64
	GasLimit        int64
	ContractContent string
	ContentEncoding string
	GasAssetAmount  int64
	FundingValue    int64
	Assets          []ContractFundingAsset
}

type ContractInvokeRequest struct {
	ContractType    string
	SubType         string
	ContractAddress string
	GasLimit        int64
	CallNonce       uint64
	Action          string
	Param           string
	ParamEncoding   string
	GasAssetAmount  int64
	Value           int64
	Assets          []ContractFundingAsset
	DefaultInvoke   bool
	SkipResultFee   bool
}

type ContractFundingAsset struct {
	AssetName string
	Amount    string
}

type EVMCalldataInvokeParam struct {
	Calldata       string `json:"calldata,omitempty"`
	CalldataHex    string `json:"calldataHex,omitempty"`
	CalldataBase64 string `json:"calldataBase64,omitempty"`
}

type AgentPredictionOutcome struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type AgentPredictionContract struct {
	Subtype      string                   `json:"subtype"`
	Title        string                   `json:"title"`
	Description  string                   `json:"description"`
	TimeBase     string                   `json:"time_base"`
	EventTime    int64                    `json:"event_time"`
	BetDeadline  int64                    `json:"bet_deadline"`
	ConfirmAfter int64                    `json:"confirm_after"`
	SourceURL    string                   `json:"source_url"`
	BetAsset     string                   `json:"bet_asset"`
	MinBetUnit   string                   `json:"min_bet_unit"`
	Outcomes     []AgentPredictionOutcome `json:"outcomes"`
}

type AgentPredictionBetParam struct {
	OutcomeID string `json:"outcome_id"`
}

type ContractTxResult struct {
	ContractType    string `json:"contractType"`
	TxID            string `json:"txid"`
	ContractAddress string `json:"contractAddress,omitempty"`
	Caller          string `json:"caller,omitempty"`
	GasAssetAmount  int64  `json:"gasAssetAmount,omitempty"`
	GasFeeAmount    int64  `json:"gasFeeAmount,omitempty"`
	GasFundAmount   int64  `json:"gasFundAmount,omitempty"`
	GasLimit        int64  `json:"gasLimit,omitempty"`
	Nonce           uint64 `json:"nonce,omitempty"`
}

func (p *Manager) QueryContract(req *ContractQueryRequest) (string, error) {
	if req == nil {
		return "", fmt.Errorf("missing contract query request")
	}
	switch req.Query {
	case "", ContractQueryInfo:
		if req.Contract == "" {
			return "", fmt.Errorf("missing contract")
		}
		return p.l2IndexerClient.GetContractJSON(req.Contract)
	case ContractQueryList:
		return p.l2IndexerClient.GetContractsJSON(req.Start, req.Limit)
	case ContractQueryState:
		if req.Contract == "" {
			return "", fmt.Errorf("missing contract")
		}
		return p.l2IndexerClient.GetContractStateJSON(req.Contract)
	case ContractQueryHistory:
		if req.Contract == "" {
			return "", fmt.Errorf("missing contract")
		}
		return p.l2IndexerClient.GetContractHistoryJSON(req.Contract, req.Start, req.Limit)
	default:
		return "", fmt.Errorf("unsupported contract query %s", req.Query)
	}
}

func (p *Manager) DeployUnifiedContract(req *ContractDeployRequest) (*ContractTxResult, error) {
	if req == nil {
		return nil, fmt.Errorf("missing contract deploy request")
	}
	switch normalizeContractType(req.ContractType) {
	case ContractTypeEVM:
		return p.deployEVMContract(req)
	case ContractTypeTemplate:
		return p.deployTemplateContract(req)
	case ContractTypeAgent:
		return p.deployAgentContract(req)
	default:
		return nil, fmt.Errorf("unsupported contract type %s", req.ContractType)
	}
}

func (p *Manager) EstimateDeployUnifiedContract(req *ContractDeployRequest) (*ContractTxResult, error) {
	if req == nil {
		return nil, fmt.Errorf("missing contract deploy request")
	}
	switch normalizeContractType(req.ContractType) {
	case ContractTypeEVM:
		return p.EstimateEVMDeployContract(req)
	case ContractTypeTemplate:
		return p.estimateTemplateDeployContract(req)
	case ContractTypeAgent:
		return p.estimateAgentDeployContract(req)
	default:
		return nil, fmt.Errorf("unsupported contract type %s", req.ContractType)
	}
}

func (p *Manager) InvokeUnifiedContract(req *ContractInvokeRequest) (*ContractTxResult, error) {
	if req == nil {
		return nil, fmt.Errorf("missing contract invoke request")
	}
	switch normalizeContractType(req.ContractType) {
	case ContractTypeEVM:
		return p.invokeEVMContract(req)
	case ContractTypeTemplate:
		return p.invokeTemplateContract(req)
	case ContractTypeAgent:
		return p.invokeAgentContract(req)
	default:
		return nil, fmt.Errorf("unsupported contract type %s", req.ContractType)
	}
}

func (p *Manager) QueryParamForInvokeUnifiedContract(contractType, subtype, action string) (string, error) {
	action = strings.TrimSpace(action)
	if action == "" {
		action = contractcommon.ContractInvokeAPIDefault
	}
	if action == contractcommon.ContractInvokeAPIDefault {
		return "{}", nil
	}
	rawContractType := strings.TrimSpace(contractType)
	contractType = normalizeContractType(rawContractType)
	if contractType == ContractTypeTemplate && normalizeTemplateName(subtype) == "" {
		if templateName := normalizeTemplateName(rawContractType); templateName != "" {
			subtype = templateName
		}
	}
	switch contractType {
	case ContractTypeTemplate:
		templateName := normalizeTemplateName(subtype)
		if templateName == "" {
			return "", fmt.Errorf("missing template contract subtype")
		}
		return templateInvokeParamTemplate(templateName, action)
	case ContractTypeAgent:
		return agentInvokeParamTemplate(subtype, action)
	case ContractTypeEVM:
		return evmInvokeParamTemplate(action)
	default:
		return "", fmt.Errorf("unsupported contract type %s", contractType)
	}
}

func (p *Manager) QueryFeeForInvokeUnifiedContract(req *ContractInvokeRequest) (int64, error) {
	if req == nil {
		return 0, fmt.Errorf("missing contract invoke fee request")
	}
	switch normalizeContractType(req.ContractType) {
	case ContractTypeTemplate:
		return p.queryTemplateInvokeFee(req)
	case ContractTypeAgent:
		return p.queryAgentInvokeFee(req)
	case ContractTypeEVM:
		return p.queryEVMInvokeFee(req)
	default:
		return 0, fmt.Errorf("unsupported contract type %s", req.ContractType)
	}
}

func templateInvokeParamTemplate(templateName, action string) (string, error) {
	templateName = contractcommon.NormalizeTemplateName(templateName)
	action = strings.ToLower(strings.TrimSpace(action))
	if !contractcommon.IsKnownTemplateName(templateName) {
		return "", fmt.Errorf("template contract %s not found", templateName)
	}
	if !contractcommon.IsTemplateInvokeActionSupported(templateName, action) {
		return "", fmt.Errorf("template contract %s does not support %s", templateName, action)
	}
	var innerParam interface{}
	switch action {
	case contractcommon.TemplateInvokeAPISwap:
		innerParam = map[string]interface{}{
			"orderType": contractcommon.OrderTypeBuy,
			"assetName": "",
			"amt":       "",
			"unitPrice": "",
		}
	case contractcommon.TemplateInvokeAPIRefund:
		innerParam = map[string]interface{}{
			"itemIds": []int64{},
		}
	case contractcommon.TemplateInvokeAPIAddLiquidity:
		innerParam = map[string]interface{}{
			"orderType": contractcommon.OrderTypeAddLiquidity,
			"assetName": "",
			"amt":       "",
			"value":     0,
		}
	case contractcommon.TemplateInvokeAPIRemoveLiquidity:
		innerParam = map[string]interface{}{
			"orderType": contractcommon.OrderTypeRemoveLiquidity,
			"assetName": "",
			"lptAmt":    "",
		}
	case contractcommon.TemplateInvokeAPIExchange:
		innerParam = map[string]interface{}{
			"minOutA": "",
		}
	case contractcommon.TemplateInvokeAPIClose:
		innerParam = nil
	default:
		return "", fmt.Errorf("template contract %s does not support %s", templateName, action)
	}
	return unifiedInvokeParamTemplate(action, innerParam)
}

func unifiedInvokeParamTemplate(action string, innerParam interface{}) (string, error) {
	param := InvokeParam{Action: strings.ToLower(strings.TrimSpace(action))}
	if innerParam != nil {
		buf, err := json.Marshal(innerParam)
		if err != nil {
			return "", err
		}
		param.Param = string(buf)
	}
	result, err := json.Marshal(&param)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

func ConvertUnifiedInvokeParam(contractType, subtype, jsonInvokeParam string) (*InvokeParam, error) {
	switch normalizeContractType(contractType) {
	case ContractTypeTemplate:
		return convertTemplateInvokeParam(subtype, jsonInvokeParam)
	case ContractTypeAgent:
		return convertAgentInvokeParam(subtype, jsonInvokeParam)
	case ContractTypeEVM:
		return convertEVMInvokeParam(jsonInvokeParam)
	default:
		return nil, fmt.Errorf("unsupported contract type %s", contractType)
	}
}

func parseUnifiedInvokeParam(jsonInvokeParam string) (*InvokeParam, error) {
	var wrapperParam InvokeParam
	if err := json.Unmarshal([]byte(jsonInvokeParam), &wrapperParam); err != nil {
		return nil, err
	}
	wrapperParam.Action = strings.ToLower(strings.TrimSpace(wrapperParam.Action))
	if wrapperParam.Action == "" {
		return nil, fmt.Errorf("missing invoke action")
	}
	return &wrapperParam, nil
}

func convertTemplateInvokeParam(templateName, jsonInvokeParam string) (*InvokeParam, error) {
	templateName = contractcommon.NormalizeTemplateName(templateName)
	wrapperParam, err := parseUnifiedInvokeParam(jsonInvokeParam)
	if err != nil {
		return nil, err
	}
	if templateName != "" && !contractcommon.IsTemplateInvokeActionSupported(templateName, wrapperParam.Action) {
		return nil, fmt.Errorf("template contract %s does not support %s", templateName, wrapperParam.Action)
	}
	var (
		innerParam []byte
	)
	switch wrapperParam.Action {
	case contractcommon.TemplateInvokeAPISwap:
		var param contractcommon.TemplateLimitOrderInvokeParam
		if err = json.Unmarshal([]byte(wrapperParam.Param), &param); err != nil {
			return nil, err
		}
		innerParam, err = param.Encode()
	case contractcommon.TemplateInvokeAPIRefund:
		var param contractcommon.TemplateRefundInvokeParam
		if err = json.Unmarshal([]byte(wrapperParam.Param), &param); err != nil {
			return nil, err
		}
		innerParam, err = param.Encode()
	case contractcommon.TemplateInvokeAPIAddLiquidity:
		var param contractcommon.TemplateAddLiquidityInvokeParam
		if err = json.Unmarshal([]byte(wrapperParam.Param), &param); err != nil {
			return nil, err
		}
		innerParam, err = param.Encode()
	case contractcommon.TemplateInvokeAPIRemoveLiquidity:
		var param contractcommon.TemplateRemoveLiquidityInvokeParam
		if err = json.Unmarshal([]byte(wrapperParam.Param), &param); err != nil {
			return nil, err
		}
		innerParam, err = param.Encode()
	case contractcommon.TemplateInvokeAPIExchange:
		var param contractcommon.TemplateExchangeInvokeParam
		if err = json.Unmarshal([]byte(wrapperParam.Param), &param); err != nil {
			return nil, err
		}
		innerParam, err = param.Encode()
	case contractcommon.TemplateInvokeAPIClose:
		param := contractcommon.TemplateCloseInvokeParam{}
		if strings.TrimSpace(wrapperParam.Param) != "" {
			if err = json.Unmarshal([]byte(wrapperParam.Param), &param); err != nil {
				return nil, err
			}
		}
		innerParam, err = param.Encode()
	default:
		return nil, fmt.Errorf("unsupported template invoke action %s", wrapperParam.Action)
	}
	if err != nil {
		return nil, err
	}
	wrapperParam.Param = base64.StdEncoding.EncodeToString(innerParam)
	return wrapperParam, nil
}

func agentInvokeParamTemplate(subtype, action string) (string, error) {
	if strings.TrimSpace(subtype) == "" {
		subtype = contractcommon.SubtypePrediction
	}
	action = strings.ToLower(strings.TrimSpace(action))
	if subtype != contractcommon.SubtypePrediction {
		return "", fmt.Errorf("agent contract %s not found", subtype)
	}
	var innerParam interface{}
	switch action {
	case contractcommon.AgentInvokeAPIReady:
		innerParam = nil
	case contractcommon.AgentInvokeAPIBet:
		innerParam = contractcommon.AgentPredictionBetParam{}
	case contractcommon.AgentInvokeAPIConfirm:
		innerParam = contractcommon.AgentPredictionConfirmParam{}
	case contractcommon.AgentInvokeAPIReject:
		innerParam = contractcommon.AgentPredictionRejectParam{}
	case contractcommon.AgentInvokeAPIClose:
		innerParam = nil
	default:
		return "", fmt.Errorf("agent contract %s does not support %s", subtype, action)
	}
	return unifiedInvokeParamTemplate(action, innerParam)
}

func evmInvokeParamTemplate(action string) (string, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case contractcommon.ContractInvokeAPICall:
		return unifiedInvokeParamTemplate(action, EVMCalldataInvokeParam{})
	case contractcommon.ContractInvokeAPIClose:
		return unifiedInvokeParamTemplate(action, nil)
	default:
		return "", fmt.Errorf("evm contract does not support %s", action)
	}
}

func convertAgentInvokeParam(subtype, jsonInvokeParam string) (*InvokeParam, error) {
	if strings.TrimSpace(subtype) == "" {
		subtype = contractcommon.SubtypePrediction
	}
	if subtype != contractcommon.SubtypePrediction {
		return nil, fmt.Errorf("agent contract %s not found", subtype)
	}
	wrapperParam, err := parseUnifiedInvokeParam(jsonInvokeParam)
	if err != nil {
		return nil, err
	}
	var innerParam []byte
	switch wrapperParam.Action {
	case contractcommon.AgentInvokeAPIReady:
		innerParam = nil
	case contractcommon.AgentInvokeAPIBet:
		var param contractcommon.AgentPredictionBetParam
		if err = json.Unmarshal([]byte(wrapperParam.Param), &param); err != nil {
			return nil, err
		}
		innerParam, err = param.Encode()
	case contractcommon.AgentInvokeAPIConfirm:
		var param contractcommon.AgentPredictionConfirmParam
		if err = json.Unmarshal([]byte(wrapperParam.Param), &param); err != nil {
			return nil, err
		}
		innerParam, err = param.Encode()
	case contractcommon.AgentInvokeAPIReject:
		var param contractcommon.AgentPredictionRejectParam
		if err = json.Unmarshal([]byte(wrapperParam.Param), &param); err != nil {
			return nil, err
		}
		innerParam, err = param.Encode()
	case contractcommon.AgentInvokeAPIClose:
		innerParam = nil
	default:
		return nil, fmt.Errorf("agent contract %s does not support %s", subtype, wrapperParam.Action)
	}
	if err != nil {
		return nil, err
	}
	wrapperParam.Param = base64.StdEncoding.EncodeToString(innerParam)
	return wrapperParam, nil
}

func convertEVMInvokeParam(jsonInvokeParam string) (*InvokeParam, error) {
	wrapperParam, err := parseUnifiedInvokeParam(jsonInvokeParam)
	if err != nil {
		return nil, err
	}
	if wrapperParam.Action == contractcommon.ContractInvokeAPIClose {
		wrapperParam.Param = ""
		return wrapperParam, nil
	}
	if wrapperParam.Action != contractcommon.ContractInvokeAPICall {
		return nil, fmt.Errorf("evm contract does not support %s", wrapperParam.Action)
	}
	innerParam, err := decodeEVMCalldataParam(wrapperParam.Param)
	if err != nil {
		return nil, err
	}
	wrapperParam.Param = base64.StdEncoding.EncodeToString(innerParam)
	return wrapperParam, nil
}

func decodeEVMCalldataParam(param string) ([]byte, error) {
	if strings.TrimSpace(param) == "" {
		return nil, nil
	}
	var call EVMCalldataInvokeParam
	if err := json.Unmarshal([]byte(param), &call); err != nil {
		return nil, err
	}
	var provided int
	var decoded []byte
	if strings.TrimSpace(call.CalldataBase64) != "" {
		provided++
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(call.CalldataBase64))
		if err != nil {
			return nil, fmt.Errorf("decode evm calldata base64: %w", err)
		}
		decoded = data
	}
	hexValue := strings.TrimSpace(call.CalldataHex)
	if hexValue == "" {
		hexValue = strings.TrimSpace(call.Calldata)
	}
	if hexValue != "" {
		provided++
		data, err := decodeHexField("evm calldata", hexValue)
		if err != nil {
			return nil, err
		}
		decoded = data
	}
	if provided > 1 {
		return nil, fmt.Errorf("evm calldata must specify only one encoding")
	}
	return decoded, nil
}

func emptyJSONParam(param string) string {
	if strings.TrimSpace(param) == "" {
		return "{}"
	}
	return param
}

func convertUnifiedInvokeRequestParam(contractType string, req *ContractInvokeRequest) (*InvokeParam, error) {
	if req == nil {
		return nil, fmt.Errorf("missing contract invoke request")
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		return nil, fmt.Errorf("missing invoke action")
	}
	encoding := strings.ToLower(strings.TrimSpace(req.ParamEncoding))
	if encoding == "" {
		encoding = "json"
	}
	switch encoding {
	case "json":
		wrapper := InvokeParam{Action: action, Param: req.Param}
		data, err := json.Marshal(&wrapper)
		if err != nil {
			return nil, err
		}
		return ConvertUnifiedInvokeParam(contractType, req.SubType, string(data))
	case "base64":
		if strings.TrimSpace(req.Param) != "" {
			if _, err := base64.StdEncoding.DecodeString(req.Param); err != nil {
				return nil, fmt.Errorf("decode invoke param base64: %w", err)
			}
		}
		return &InvokeParam{Action: action, Param: req.Param}, nil
	case "hex":
		param, err := decodeHexField("invoke param", req.Param)
		if err != nil {
			return nil, err
		}
		return &InvokeParam{Action: action, Param: base64.StdEncoding.EncodeToString(param)}, nil
	default:
		return nil, fmt.Errorf("unsupported invoke param encoding %s", req.ParamEncoding)
	}
}

func gasOverrideAmount(name string, amount int64) (int64, error) {
	if amount < 0 {
		return 0, fmt.Errorf("%s must be non-negative", name)
	}
	return amount, nil
}

func (p *Manager) queryTemplateInvokeFee(req *ContractInvokeRequest) (int64, error) {
	if req == nil {
		return 0, fmt.Errorf("missing template invoke fee request")
	}
	gasLimit := req.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.InvokeBaseGas
	}
	gasOverride, err := gasOverrideAmount("gas asset amount", req.GasAssetAmount)
	if err != nil {
		return 0, err
	}
	if req.DefaultInvoke {
		return p.queryDefaultInvokeFee(ContractTypeTemplate, gasLimit, gasOverride)
	}
	converted, err := convertUnifiedInvokeRequestParam(ContractTypeTemplate, req)
	if err != nil {
		return 0, err
	}
	if req.ContractAddress != "" {
		if err := p.checkTemplateInvokeSupported(req.ContractAddress, converted.Action); err != nil {
			return 0, err
		}
	}
	gasAmount, _, err := p.templateGasAssetAmount(gasLimit, !req.SkipResultFee, gasOverride)
	if err != nil {
		return 0, err
	}
	gasBaseFee, err := p.evmBaseGasFee(contractcommon.InvokeBaseGas)
	if err != nil {
		return 0, err
	}
	if gasAmount < gasBaseFee {
		return 0, fmt.Errorf("gas asset amount %d is less than required base gas fee %d", gasAmount, gasBaseFee)
	}
	return gasAmount, nil
}

func (p *Manager) queryAgentInvokeFee(req *ContractInvokeRequest) (int64, error) {
	if req == nil {
		return 0, fmt.Errorf("missing agent invoke fee request")
	}
	gasLimit := req.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.InvokeBaseGas
	}
	gasOverride, err := gasOverrideAmount("gas asset amount", req.GasAssetAmount)
	if err != nil {
		return 0, err
	}
	if req.DefaultInvoke {
		return p.queryDefaultInvokeFee(ContractTypeAgent, gasLimit, gasOverride)
	}
	converted, err := convertUnifiedInvokeRequestParam(ContractTypeAgent, req)
	if err != nil {
		return 0, err
	}
	needsResultFunding := converted.Action == contractcommon.AgentInvokeAPIBet && !req.SkipResultFee
	gasAmount, _, err := p.agentInvokeGasAssetAmount(needsResultFunding, gasOverride)
	if err != nil {
		return 0, err
	}
	if converted.Action == contractcommon.AgentInvokeAPIBet {
		if len(req.Assets) == 0 || strings.TrimSpace(req.Assets[0].AssetName) == "" {
			return 0, fmt.Errorf("agent bet asset is required")
		}
		if _, err := assetAmountStringToInt64("agent bet amount", req.Assets[0].Amount); err != nil {
			return 0, err
		}
	}
	return gasAmount, nil
}

func (p *Manager) queryEVMInvokeFee(req *ContractInvokeRequest) (int64, error) {
	if req == nil {
		return 0, fmt.Errorf("missing evm invoke fee request")
	}
	gasLimit := req.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.InvokeBaseGas
	}
	if req.DefaultInvoke {
		return 0, nil
	}
	if _, err := convertUnifiedInvokeRequestParam(ContractTypeEVM, req); err != nil {
		return 0, err
	}
	gasOverride, err := gasOverrideAmount("gas asset amount", req.GasAssetAmount)
	if err != nil {
		return 0, err
	}
	gasAmount, _, err := p.evmGasAssetAmount(gasLimit, true, gasOverride)
	if err != nil {
		return 0, err
	}
	gasBaseFee, err := p.evmBaseGasFee(contractcommon.InvokeBaseGas)
	if err != nil {
		return 0, err
	}
	if gasAmount < gasBaseFee {
		return 0, fmt.Errorf("gas asset amount %d is less than required base gas fee %d", gasAmount, gasBaseFee)
	}
	return gasAmount, nil
}

func (p *Manager) queryDefaultInvokeFee(contractType string, gasLimit int64, gasOverride int64) (int64, error) {
	if contractType == ContractTypeEVM {
		return 0, nil
	}
	fundingGasAmount, _, err := p.evmGasAssetAmount(gasLimit, false, gasOverride)
	if err != nil {
		return 0, err
	}
	gasBaseFee, err := p.evmBaseGasFee(contractcommon.InvokeBaseGas)
	if err != nil {
		return 0, err
	}
	if fundingGasAmount > math.MaxInt64-gasBaseFee {
		return 0, fmt.Errorf("gas asset amount overflows int64")
	}
	return fundingGasAmount + gasBaseFee, nil
}

func (p *Manager) estimateAgentDeployContract(req *ContractDeployRequest) (*ContractTxResult, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	if req == nil {
		return nil, fmt.Errorf("missing agent deploy request")
	}
	subtype := strings.TrimSpace(req.SubType)
	if subtype == "" {
		subtype = contractcommon.SubtypePrediction
	}
	content, err := decodeContractContent(req.ContractContent, req.ContentEncoding)
	if err != nil {
		return nil, err
	}
	if len(content) == 0 {
		return nil, fmt.Errorf("missing agent contract content")
	}
	if subtype == contractcommon.SubtypePrediction {
		contract, err := contractcommon.DecodeAgentPredictionContract(content)
		if err != nil {
			return nil, fmt.Errorf("decode prediction contract: %w", err)
		}
		if err := contract.Check(); err != nil {
			return nil, err
		}
	}
	gasLimit := req.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.DeployBaseGas
	}
	deployBaseFee, _, err := p.agentGasAssetAmount(contractcommon.DeployBaseGas, 0)
	if err != nil {
		return nil, err
	}
	invokeBaseFee, _, err := p.agentGasAssetAmount(contractcommon.InvokeBaseGas, 0)
	if err != nil {
		return nil, err
	}
	gasAmount, err := gasOverrideAmount("agent deploy gas asset amount", req.GasAssetAmount)
	if err != nil {
		return nil, err
	}
	if gasAmount == 0 {
		if invokeBaseFee > math.MaxInt64/2 || deployBaseFee > math.MaxInt64-invokeBaseFee*2 {
			return nil, fmt.Errorf("agent deploy default gas amount overflows int64")
		}
		gasAmount = deployBaseFee + invokeBaseFee*2
	}
	if gasAmount < deployBaseFee {
		return nil, fmt.Errorf("agent deploy gas asset amount %d is less than required base gas fee %d", gasAmount, deployBaseFee)
	}
	deployNonce := uint64(time.Now().UnixNano())
	contractAddr, _, err := contractcommon.DeriveAgentContractAddress(
		p.contractAddressPrefix(),
		subtype,
		content,
		p.wallet.GetAddress(),
		deployNonce,
	)
	if err != nil {
		return nil, err
	}
	return &ContractTxResult{
		ContractType:    ContractTypeAgent,
		ContractAddress: contractAddr.EncodeAddress(),
		Caller:          p.wallet.GetAddress(),
		GasAssetAmount:  gasAmount,
		GasFeeAmount:    deployBaseFee,
		GasFundAmount:   gasAmount - deployBaseFee,
		GasLimit:        gasLimit,
		Nonce:           deployNonce,
	}, nil
}

func (p *Manager) deployAgentContract(req *ContractDeployRequest) (*ContractTxResult, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	if req == nil {
		return nil, fmt.Errorf("missing agent deploy request")
	}
	subtype := strings.TrimSpace(req.SubType)
	if subtype == "" {
		subtype = contractcommon.SubtypePrediction
	}
	content, err := decodeContractContent(req.ContractContent, req.ContentEncoding)
	if err != nil {
		return nil, err
	}
	if len(content) == 0 {
		return nil, fmt.Errorf("missing agent contract content")
	}
	if subtype == contractcommon.SubtypePrediction {
		contract, err := contractcommon.DecodeAgentPredictionContract(content)
		if err != nil {
			return nil, fmt.Errorf("decode prediction contract: %w", err)
		}
		if err := contract.Check(); err != nil {
			return nil, err
		}
	}
	gasLimit := req.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.DeployBaseGas
	}
	deployBaseFee, gasAsset, err := p.agentGasAssetAmount(contractcommon.DeployBaseGas, 0)
	if err != nil {
		return nil, err
	}
	invokeBaseFee, _, err := p.agentGasAssetAmount(contractcommon.InvokeBaseGas, 0)
	if err != nil {
		return nil, err
	}
	gasAmount, err := gasOverrideAmount("agent deploy gas asset amount", req.GasAssetAmount)
	if err != nil {
		return nil, err
	}
	if gasAmount == 0 {
		if invokeBaseFee > math.MaxInt64/2 || deployBaseFee > math.MaxInt64-invokeBaseFee*2 {
			return nil, fmt.Errorf("agent deploy default gas amount overflows int64")
		}
		gasAmount = deployBaseFee + invokeBaseFee*2
	}
	if gasAmount < deployBaseFee {
		return nil, fmt.Errorf("agent deploy gas asset amount %d is less than required base gas fee %d", gasAmount, deployBaseFee)
	}
	fundingAmount := gasAmount - deployBaseFee
	fundingAssets := contractFundingAssets(req.Assets)
	funding, inputs, changeOutputs, prevFetcher, _, err := p.selectUnifiedContractFunding(gasAsset, gasAmount, fundingAmount, req.FundingValue, fundingAssets)
	if err != nil {
		return nil, err
	}
	version := req.Version
	if version == 0 {
		version = contractcommon.CurrentAgentVersion
	}
	deployNonce := uint64(time.Now().UnixNano())
	tx, contractAddr, err := contractcommon.BuildDeployTx(contractcommon.DeployTxBuildRequest{
		ContractPrefix:  p.contractAddressPrefix(),
		Type:            contractcommon.ContractTypeAgent,
		SubType:         subtype,
		Version:         version,
		Deployer:        p.wallet.GetAddress(),
		DeployNonce:     deployNonce,
		ContractContent: content,
		GasLimit:        gasLimit,
		Funding:         funding,
		Inputs:          inputs,
		ExtraOutputs:    changeOutputs,
	})
	if err != nil {
		return nil, err
	}
	signedTx, err := p.SignContractTx_SatsNet(tx, prevFetcher, gasAsset, deployBaseFee)
	if err != nil {
		return nil, err
	}
	PrintJsonTx_SatsNet(signedTx, "DeployAgentContract")
	txid, err := p.BroadcastTx_SatsNet(signedTx)
	if err != nil {
		return nil, err
	}
	return &ContractTxResult{
		ContractType:    ContractTypeAgent,
		TxID:            txid,
		ContractAddress: contractAddr.EncodeAddress(),
		GasAssetAmount:  gasAmount,
		GasFeeAmount:    deployBaseFee,
		GasFundAmount:   fundingAmount,
		GasLimit:        gasLimit,
		Nonce:           deployNonce,
	}, nil
}

func (p *Manager) invokeAgentContract(req *ContractInvokeRequest) (*ContractTxResult, error) {
	if req == nil {
		return nil, fmt.Errorf("missing agent invoke request")
	}
	gasOverride, err := gasOverrideAmount("gas asset amount", req.GasAssetAmount)
	if err != nil {
		return nil, err
	}
	if req.DefaultInvoke {
		assetName, amount := firstFundingAsset(req.Assets)
		return p.invokeDefaultContract(ContractTypeAgent, req.ContractAddress, req.Value, req.GasLimit, gasOverride, assetName, amount)
	}
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	contract, err := contractcommon.DecodeContractAddress(req.ContractAddress)
	if err != nil {
		return nil, err
	}
	converted, err := convertUnifiedInvokeRequestParam(ContractTypeAgent, req)
	if err != nil {
		return nil, err
	}
	param, err := base64.StdEncoding.DecodeString(converted.Param)
	if err != nil {
		return nil, fmt.Errorf("decode agent invoke param: %w", err)
	}
	gasLimit := req.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.InvokeBaseGas
	}
	needsResultFunding := converted.Action == contractcommon.AgentInvokeAPIBet && !req.SkipResultFee
	gasAmount, gasAsset, err := p.agentInvokeGasAssetAmount(needsResultFunding, gasOverride)
	if err != nil {
		return nil, err
	}
	gasBaseFee, _, err := p.agentGasAssetAmount(contractcommon.InvokeBaseGas, 0)
	if err != nil {
		return nil, err
	}
	fundingGasAmount, err := agentInvokeFundingGasAmount(converted.Action, req.SkipResultFee, gasAmount, gasBaseFee)
	if err != nil {
		return nil, err
	}
	fundingAssets := contractFundingAssets(req.Assets)
	if converted.Action == contractcommon.AgentInvokeAPIBet {
		if len(fundingAssets) == 0 || strings.TrimSpace(fundingAssets[0].Name) == "" {
			return nil, fmt.Errorf("agent bet asset is required")
		}
		if _, err := assetAmountStringToInt64("agent bet amount", fundingAssets[0].Amount); err != nil {
			return nil, err
		}
	}
	callNonce := req.CallNonce
	if callNonce == 0 {
		callNonce = uint64(time.Now().UnixNano())
	}
	funding, inputs, changeOutputs, prevFetcher, _, err := p.selectUnifiedContractFunding(gasAsset, gasAmount, fundingGasAmount, req.Value, fundingAssets)
	if err != nil {
		return nil, err
	}
	tx, err := contractcommon.BuildInvokeTx(contractcommon.InvokeTxBuildRequest{
		Contract:     contract,
		GasLimit:     gasLimit,
		CallNonce:    callNonce,
		Action:       converted.Action,
		Param:        param,
		Funding:      funding,
		Inputs:       inputs,
		ExtraOutputs: changeOutputs,
	})
	if err != nil {
		return nil, err
	}
	gasFeeAmount := gasAmount
	if needsResultFunding {
		gasFeeAmount = gasBaseFee
	}
	signedTx, err := p.SignContractTx_SatsNet(tx, prevFetcher, gasAsset, gasFeeAmount)
	if err != nil {
		return nil, err
	}
	PrintJsonTx_SatsNet(signedTx, "InvokeAgentContract")
	txid, err := p.BroadcastTx_SatsNet(signedTx)
	if err != nil {
		return nil, err
	}
	return &ContractTxResult{
		ContractType:    ContractTypeAgent,
		TxID:            txid,
		ContractAddress: req.ContractAddress,
		GasAssetAmount:  gasAmount,
		GasFeeAmount:    gasFeeAmount,
		GasFundAmount:   fundingGasAmount,
		GasLimit:        gasLimit,
		Nonce:           callNonce,
	}, nil
}

func (p *Manager) estimateTemplateDeployContract(req *ContractDeployRequest) (*ContractTxResult, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	if req == nil {
		return nil, fmt.Errorf("missing template deploy request")
	}
	templateName := normalizeTemplateName(req.SubType)
	if templateName == "" {
		return nil, fmt.Errorf("missing template contract subtype")
	}
	content, err := decodeContractContent(req.ContractContent, req.ContentEncoding)
	if err != nil {
		return nil, err
	}
	if len(content) == 0 {
		return nil, fmt.Errorf("missing template contract content")
	}
	gasLimit := req.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.DeployBaseGas
	}
	if gasLimit < contractcommon.DeployBaseGas {
		return nil, fmt.Errorf("template deploy gas limit %d is less than required base gas %d",
			gasLimit, contractcommon.DeployBaseGas)
	}
	gasOverride, err := gasOverrideAmount("gas asset amount", req.GasAssetAmount)
	if err != nil {
		return nil, err
	}
	gasAmount, _, err := p.templateGasAssetAmount(gasLimit, true, gasOverride)
	if err != nil {
		return nil, err
	}
	gasBaseFee, err := p.evmBaseGasFee(contractcommon.DeployBaseGas)
	if err != nil {
		return nil, err
	}
	if gasAmount < gasBaseFee {
		return nil, fmt.Errorf("gas asset amount %d is less than required base gas fee %d", gasAmount, gasBaseFee)
	}
	deployNonce := uint64(time.Now().UnixNano())
	contractAddr, _, err := contractcommon.DeriveTemplateContractAddress(
		p.contractAddressPrefix(),
		content,
		p.wallet.GetAddress(),
		deployNonce,
	)
	if err != nil {
		return nil, err
	}
	return &ContractTxResult{
		ContractType:    ContractTypeTemplate,
		ContractAddress: contractAddr.EncodeAddress(),
		Caller:          p.wallet.GetAddress(),
		GasAssetAmount:  gasAmount,
		GasFeeAmount:    gasBaseFee,
		GasFundAmount:   gasAmount - gasBaseFee,
		GasLimit:        gasLimit,
		Nonce:           deployNonce,
	}, nil
}

func (p *Manager) deployTemplateContract(req *ContractDeployRequest) (*ContractTxResult, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	if req == nil {
		return nil, fmt.Errorf("missing template deploy request")
	}
	templateName := normalizeTemplateName(req.SubType)
	if templateName == "" {
		return nil, fmt.Errorf("missing template contract subtype")
	}
	content, err := decodeContractContent(req.ContractContent, req.ContentEncoding)
	if err != nil {
		return nil, err
	}
	if len(content) == 0 {
		return nil, fmt.Errorf("missing template contract content")
	}
	gasLimit := req.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.DeployBaseGas
	}
	gasOverride, err := gasOverrideAmount("gas asset amount", req.GasAssetAmount)
	if err != nil {
		return nil, err
	}
	gasAmount, gasAsset, err := p.templateGasAssetAmount(gasLimit, true, gasOverride)
	if err != nil {
		return nil, err
	}
	gasBaseFee, err := p.evmBaseGasFee(contractcommon.DeployBaseGas)
	if err != nil {
		return nil, err
	}
	if gasAmount < gasBaseFee {
		return nil, fmt.Errorf("gas asset amount %d is less than required base gas fee %d", gasAmount, gasBaseFee)
	}
	fundingAssets := contractFundingAssets(req.Assets)
	funding, inputs, changeOutputs, prevFetcher, _, err := p.selectUnifiedContractFunding(gasAsset, gasAmount, gasAmount-gasBaseFee, req.FundingValue, fundingAssets)
	if err != nil {
		return nil, err
	}
	version := req.Version
	if version == 0 {
		version = contractcommon.CurrentTemplateVersion
	}
	deployNonce := uint64(time.Now().UnixNano())
	tx, contractAddr, err := contractcommon.BuildDeployTx(contractcommon.DeployTxBuildRequest{
		ContractPrefix:  p.contractAddressPrefix(),
		Type:            contractcommon.ContractTypeTemplate,
		SubType:         templateName,
		Version:         version,
		ContractContent: content,
		Deployer:        p.wallet.GetAddress(),
		DeployNonce:     deployNonce,
		GasLimit:        gasLimit,
		Funding:         funding,
		Inputs:          inputs,
		ExtraOutputs:    changeOutputs,
	})
	if err != nil {
		return nil, err
	}
	signedTx, err := p.SignContractTx_SatsNet(tx, prevFetcher, gasAsset, gasBaseFee)
	if err != nil {
		return nil, err
	}
	PrintJsonTx_SatsNet(signedTx, "DeployTemplateContract")
	txid, err := p.BroadcastTx_SatsNet(signedTx)
	if err != nil {
		return nil, err
	}
	return &ContractTxResult{
		ContractType:    ContractTypeTemplate,
		TxID:            txid,
		ContractAddress: contractAddr.EncodeAddress(),
		GasAssetAmount:  gasAmount,
		GasFeeAmount:    gasBaseFee,
		GasFundAmount:   gasAmount - gasBaseFee,
		GasLimit:        gasLimit,
		Nonce:           deployNonce,
	}, nil
}

func (p *Manager) invokeTemplateContract(req *ContractInvokeRequest) (*ContractTxResult, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	if req == nil {
		return nil, fmt.Errorf("missing template invoke request")
	}
	gasOverride, err := gasOverrideAmount("gas asset amount", req.GasAssetAmount)
	if err != nil {
		return nil, err
	}
	if req.DefaultInvoke {
		assetName, amount := firstFundingAsset(req.Assets)
		return p.invokeDefaultContract(ContractTypeTemplate, req.ContractAddress, req.Value, req.GasLimit, gasOverride, assetName, amount)
	}
	contract, err := contractcommon.DecodeContractAddress(req.ContractAddress)
	if err != nil {
		return nil, err
	}
	converted, err := convertUnifiedInvokeRequestParam(ContractTypeTemplate, req)
	if err != nil {
		return nil, err
	}
	if err := p.checkTemplateInvokeSupported(req.ContractAddress, converted.Action); err != nil {
		return nil, err
	}
	param, err := base64.StdEncoding.DecodeString(converted.Param)
	if err != nil {
		return nil, fmt.Errorf("decode template invoke param: %w", err)
	}
	gasLimit := req.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.InvokeBaseGas
	}
	gasAmount, gasAsset, err := p.templateGasAssetAmount(gasLimit, !req.SkipResultFee, gasOverride)
	if err != nil {
		return nil, err
	}
	gasBaseFee, err := p.evmBaseGasFee(contractcommon.InvokeBaseGas)
	if err != nil {
		return nil, err
	}
	if gasAmount < gasBaseFee {
		return nil, fmt.Errorf("gas asset amount %d is less than required base gas fee %d", gasAmount, gasBaseFee)
	}
	callNonce := req.CallNonce
	if callNonce == 0 {
		callNonce = uint64(time.Now().UnixNano())
	}
	funding, inputs, changeOutputs, prevFetcher, _, err := p.selectUnifiedContractFunding(gasAsset, gasAmount, gasAmount-gasBaseFee, req.Value, contractFundingAssets(req.Assets))
	if err != nil {
		return nil, err
	}
	tx, err := contractcommon.BuildInvokeTx(contractcommon.InvokeTxBuildRequest{
		Contract:     contract,
		GasLimit:     gasLimit,
		CallNonce:    callNonce,
		Action:       converted.Action,
		Param:        param,
		Funding:      funding,
		Inputs:       inputs,
		ExtraOutputs: changeOutputs,
	})
	if err != nil {
		return nil, err
	}
	signedTx, err := p.SignContractTx_SatsNet(tx, prevFetcher, gasAsset, gasBaseFee)
	if err != nil {
		return nil, err
	}
	PrintJsonTx_SatsNet(signedTx, "InvokeTemplateContract")
	txid, err := p.BroadcastTx_SatsNet(signedTx)
	if err != nil {
		return nil, err
	}
	return &ContractTxResult{
		ContractType:    ContractTypeTemplate,
		TxID:            txid,
		ContractAddress: req.ContractAddress,
		GasAssetAmount:  gasAmount,
		GasFeeAmount:    gasBaseFee,
		GasFundAmount:   gasAmount - gasBaseFee,
		GasLimit:        gasLimit,
		Nonce:           callNonce,
	}, nil
}

func (p *Manager) checkTemplateInvokeSupported(contractAddress, action string) error {
	info, err := p.l2IndexerClient.GetContractJSON(contractAddress)
	if err != nil {
		return fmt.Errorf("query template contract before %s: %w", action, err)
	}
	type templateInfo struct {
		Name    string `json:"name"`
		Subtype string `json:"subtype"`
		Details struct {
			Template struct {
				TemplateName string `json:"templateName"`
			} `json:"template"`
		} `json:"details"`
	}
	var direct templateInfo
	if err := json.Unmarshal([]byte(info), &direct); err != nil {
		return fmt.Errorf("decode template contract before %s: %w", action, err)
	}
	var wrapped struct {
		Data templateInfo `json:"data"`
	}
	_ = json.Unmarshal([]byte(info), &wrapped)

	templateName := direct.Details.Template.TemplateName
	if templateName == "" {
		templateName = direct.Subtype
	}
	if templateName == "" {
		templateName = direct.Name
	}
	if templateName == "" {
		templateName = wrapped.Data.Details.Template.TemplateName
	}
	if templateName == "" {
		templateName = wrapped.Data.Subtype
	}
	if templateName == "" {
		templateName = wrapped.Data.Name
	}
	normalized := contractcommon.NormalizeTemplateName(templateName)
	if !contractcommon.IsKnownTemplateName(normalized) {
		return nil
	}
	if !contractcommon.IsTemplateInvokeActionSupported(normalized, action) {
		return fmt.Errorf("template contract %s does not support %s", normalized, action)
	}
	return nil
}

func BuildUnifiedContractContent(contractType, subtype, jsonContent string) (string, error) {
	content, err := encodeUnifiedContractContent(contractType, subtype, jsonContent)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(content), nil
}

func encodeUnifiedContractContent(contractType, subtype, jsonContent string) ([]byte, error) {
	switch normalizeContractType(contractType) {
	case ContractTypeTemplate:
		_, content, err := encodeTemplateContractContent(subtype, jsonContent)
		return content, err
	case ContractTypeAgent:
		return encodeAgentContractContent(subtype, jsonContent)
	case ContractTypeEVM:
		return encodeEVMContractContent(jsonContent)
	default:
		return nil, fmt.Errorf("unsupported contract type %s", contractType)
	}
}

func encodeTemplateContractContent(subtype, jsonContent string) (string, []byte, error) {
	name := normalizeTemplateName(subtype)
	if name == "" {
		return "", nil, fmt.Errorf("missing template name")
	}
	if name == TEMPLATE_CONTRACT_EXCHANGE {
		var exchange contractcommon.TemplateExchangeContract
		if err := json.Unmarshal([]byte(jsonContent), &exchange); err != nil {
			return "", nil, err
		}
		content, err := contractcommon.EncodeTemplateExchangeContent(exchange)
		if err != nil {
			return "", nil, err
		}
		return contractcommon.TemplateExchange, content, nil
	}
	contract, err := ContractContentUnMarsh(name, jsonContent)
	if err != nil {
		return "", nil, err
	}
	assetName := contract.GetAssetName().String()
	switch name {
	case TEMPLATE_CONTRACT_LIMITORDER, TEMPLATE_CONTRACT_SWAP:
		content, err := contractcommon.EncodeTemplateLimitOrderContent(assetName)
		if err != nil {
			return "", nil, err
		}
		return contractcommon.TemplateLimitOrder, content, nil
	case TEMPLATE_CONTRACT_AMM:
		amm, ok := contract.(*AmmContract)
		if !ok {
			return "", nil, fmt.Errorf("template content is not AMM")
		}
		content, err := contractcommon.EncodeTemplateAMMContent(assetName, amm.AssetAmt, amm.SatValue, amm.K)
		if err != nil {
			return "", nil, err
		}
		return contractcommon.TemplateAMM, content, nil
	default:
		return "", nil, fmt.Errorf("unsupported template contract %s", subtype)
	}
}

func encodeAgentContractContent(subtype, jsonContent string) ([]byte, error) {
	if strings.TrimSpace(subtype) == "" {
		subtype = contractcommon.SubtypePrediction
	}
	if subtype != contractcommon.SubtypePrediction {
		return nil, fmt.Errorf("unsupported agent contract subtype %s", subtype)
	}
	var prediction contractcommon.AgentPredictionContract
	if err := json.Unmarshal([]byte(jsonContent), &prediction); err != nil {
		return nil, err
	}
	if prediction.Subtype == "" {
		prediction.Subtype = contractcommon.SubtypePrediction
	}
	if err := prediction.Check(); err != nil {
		return nil, err
	}
	return prediction.Encode()
}

func encodeEVMContractContent(jsonContent string) ([]byte, error) {
	var req struct {
		InitCodeHex            string `json:"initCodeHex"`
		BytecodeHex            string `json:"bytecodeHex"`
		ConstructorCalldataHex string `json:"constructorCalldataHex"`
	}
	if err := json.Unmarshal([]byte(jsonContent), &req); err == nil &&
		(req.InitCodeHex != "" || req.BytecodeHex != "" || req.ConstructorCalldataHex != "") {
		if req.InitCodeHex != "" {
			return decodeHexField("init code", req.InitCodeHex)
		}
		bytecode, err := decodeHexField("bytecode", req.BytecodeHex)
		if err != nil {
			return nil, err
		}
		constructor, err := decodeHexField("constructor calldata", req.ConstructorCalldataHex)
		if err != nil {
			return nil, err
		}
		return append(bytecode, constructor...), nil
	}
	return decodeHexField("evm contract content", jsonContent)
}

func decodeContractContent(content string, encoding string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "base64":
		if strings.TrimSpace(content) == "" {
			return nil, nil
		}
		data, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return nil, fmt.Errorf("decode contract content base64: %w", err)
		}
		return data, nil
	case "hex":
		return decodeHexField("contract content", content)
	case "raw", "text", "json":
		return []byte(content), nil
	default:
		return nil, fmt.Errorf("unsupported contract content encoding %s", encoding)
	}
}

func (p *Manager) satsNetBestHeight() int64 {
	if p == nil || p.l2IndexerClient == nil {
		return 0
	}
	height := p.l2IndexerClient.GetBestHeight()
	if height < 0 {
		return 0
	}
	return int64(height)
}

func GetGasAssetName() string {
	return contractcommon.GasAssetNameForNet(GetChainParam_SatsNet().Net)
}

func (p *Manager) agentGasAssetAmount(baseGas int64, override int64) (int64, string, error) {
	gasAssetName := GetGasAssetName()
	if override != 0 {
		return override, gasAssetName, nil
	}
	height := p.satsNetBestHeight()
	amount, err := contractcommon.GasFeeAtHeight(baseGas, uint64(height))
	if err != nil {
		return 0, "", err
	}
	return amount, gasAssetName, nil
}

func (p *Manager) agentInvokeGasAssetAmount(needsResult bool, override int64) (int64, string, error) {
	return p.evmGasAssetAmount(contractcommon.InvokeBaseGas, needsResult, override)
}

func agentInvokeFundingGasAmount(action string, skipResultFee bool, gasAmount, gasBaseFee int64) (int64, error) {
	if action != contractcommon.AgentInvokeAPIBet || skipResultFee {
		return 0, nil
	}
	if gasAmount < gasBaseFee {
		return 0, fmt.Errorf("gas asset amount %d is less than required base gas fee %d", gasAmount, gasBaseFee)
	}
	return gasAmount - gasBaseFee, nil
}

func assetAmountStringToInt64(name, value string) (int64, error) {
	amount, err := indexer.NewDecimalFromString(value, MAX_ASSET_DIVISIBILITY)
	if err != nil {
		return 0, fmt.Errorf("decode %s: %w", name, err)
	}
	if amount.Int64() < 0 {
		return 0, fmt.Errorf("%s must be non-negative", name)
	}
	return amount.Int64(), nil
}

func (p *Manager) templateGasAssetAmount(gasLimit int64, needsResult bool, override int64) (int64, string, error) {
	return p.evmGasAssetAmount(gasLimit, needsResult, override)
}

func normalizeTemplateName(name string) string {
	return contractcommon.NormalizeTemplateName(name)
}

func (p *Manager) EstimateEVMDeployContract(req *ContractDeployRequest) (*ContractTxResult, error) {
	if req == nil {
		return nil, fmt.Errorf("missing evm deploy request")
	}
	gasLimit := req.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.DeployBaseGas
	}
	if gasLimit < contractcommon.DeployBaseGas {
		return nil, fmt.Errorf("evm deploy gas limit %d is less than required base gas %d",
			gasLimit, contractcommon.DeployBaseGas)
	}
	caller, err := p.walletEVMAddress()
	if err != nil {
		return nil, err
	}
	gasOverride, err := gasOverrideAmount("gas asset amount", req.GasAssetAmount)
	if err != nil {
		return nil, err
	}
	gasAmount, _, err := p.evmGasAssetAmount(gasLimit, true, gasOverride)
	if err != nil {
		return nil, err
	}
	gasBaseFee, err := p.evmBaseGasFee(contractcommon.DeployBaseGas)
	if err != nil {
		return nil, err
	}
	if gasAmount < gasBaseFee {
		return nil, fmt.Errorf("gas asset amount %d is less than required base gas fee %d", gasAmount, gasBaseFee)
	}
	deployNonce := uint64(time.Now().UnixNano())
	contract, err := contractcommon.DeriveEVMCreateContractAddress(p.contractAddressPrefix(), caller, deployNonce)
	if err != nil {
		return nil, err
	}
	return &ContractTxResult{
		ContractType:    ContractTypeEVM,
		ContractAddress: contract.EncodeAddress(),
		Caller:          caller.String(),
		GasAssetAmount:  gasAmount,
		GasFeeAmount:    gasBaseFee,
		GasFundAmount:   gasAmount - gasBaseFee,
		GasLimit:        gasLimit,
		Nonce:           deployNonce,
	}, nil
}

func (p *Manager) deployEVMContract(req *ContractDeployRequest) (*ContractTxResult, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	estimate, err := p.EstimateEVMDeployContract(req)
	if err != nil {
		return nil, err
	}
	initCode, err := decodeContractContent(req.ContractContent, req.ContentEncoding)
	if err != nil {
		return nil, err
	}
	if len(initCode) == 0 {
		return nil, fmt.Errorf("missing evm contract content")
	}
	gasAsset := GetGasAssetName()
	funding, inputs, changeOutputs, prevFetcher, caller, err := p.selectUnifiedContractFunding(gasAsset, estimate.GasAssetAmount, estimate.GasFundAmount, req.FundingValue, contractFundingAssets(req.Assets))
	if err != nil {
		return nil, err
	}
	tx, contract, err := contractcommon.BuildDeployTx(contractcommon.DeployTxBuildRequest{
		ContractPrefix:  p.contractAddressPrefix(),
		Type:            contractcommon.ContractTypeEVM,
		Deployer:        caller.String(),
		GasLimit:        estimate.GasLimit,
		DeployNonce:     estimate.Nonce,
		ContractContent: initCode,
		Funding:         funding,
		Inputs:          inputs,
		ExtraOutputs:    changeOutputs,
	})
	if err != nil {
		return nil, err
	}
	signedTx, err := p.SignContractTx_SatsNet(tx, prevFetcher, GetGasAssetName(), estimate.GasFeeAmount)
	if err != nil {
		return nil, err
	}
	PrintJsonTx_SatsNet(signedTx, "DeployEVMContract")
	txid, err := p.BroadcastTx_SatsNet(signedTx)
	if err != nil {
		return nil, err
	}
	estimate.TxID = txid
	estimate.ContractAddress = contract.EncodeAddress()
	estimate.Caller = caller.String()
	return estimate, nil
}

func (p *Manager) invokeEVMContract(req *ContractInvokeRequest) (*ContractTxResult, error) {
	if req == nil {
		return nil, fmt.Errorf("missing evm invoke request")
	}
	gasOverride, err := gasOverrideAmount("gas asset amount", req.GasAssetAmount)
	if err != nil {
		return nil, err
	}
	if req.DefaultInvoke {
		assetName, amount := firstFundingAsset(req.Assets)
		return p.invokeDefaultContract(ContractTypeEVM, req.ContractAddress, req.Value, req.GasLimit, gasOverride, assetName, amount)
	}
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	contract, err := contractcommon.DecodeContractAddress(req.ContractAddress)
	if err != nil {
		return nil, err
	}
	gasLimit := req.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.InvokeBaseGas
	}
	gasAmount, gasAsset, err := p.evmGasAssetAmount(gasLimit, true, gasOverride)
	if err != nil {
		return nil, err
	}
	gasBaseFee, err := p.evmBaseGasFee(contractcommon.InvokeBaseGas)
	if err != nil {
		return nil, err
	}
	if gasAmount < gasBaseFee {
		return nil, fmt.Errorf("gas asset amount %d is less than required base gas fee %d", gasAmount, gasBaseFee)
	}
	converted, err := convertUnifiedInvokeRequestParam(ContractTypeEVM, req)
	if err != nil {
		return nil, err
	}
	invokeParam, err := base64.StdEncoding.DecodeString(converted.Param)
	if err != nil {
		return nil, fmt.Errorf("decode evm invoke param: %w", err)
	}
	funding, inputs, changeOutputs, prevFetcher, caller, err := p.selectUnifiedContractFunding(gasAsset, gasAmount, gasAmount-gasBaseFee, req.Value, contractFundingAssets(req.Assets))
	if err != nil {
		return nil, err
	}
	tx, err := contractcommon.BuildInvokeTx(contractcommon.InvokeTxBuildRequest{
		Contract:     contract,
		GasLimit:     gasLimit,
		CallNonce:    req.CallNonce,
		Action:       converted.Action,
		Param:        invokeParam,
		Funding:      funding,
		Inputs:       inputs,
		ExtraOutputs: changeOutputs,
	})
	if err != nil {
		return nil, err
	}
	signedTx, err := p.SignContractTx_SatsNet(tx, prevFetcher, gasAsset, gasBaseFee)
	if err != nil {
		return nil, err
	}
	PrintJsonTx_SatsNet(signedTx, "InvokeEVMContract")
	txid, err := p.BroadcastTx_SatsNet(signedTx)
	if err != nil {
		return nil, err
	}
	return &ContractTxResult{
		ContractType:    ContractTypeEVM,
		TxID:            txid,
		ContractAddress: req.ContractAddress,
		Caller:          caller.String(),
		GasAssetAmount:  gasAmount,
		GasFeeAmount:    gasBaseFee,
		GasFundAmount:   gasAmount - gasBaseFee,
		GasLimit:        gasLimit,
		Nonce:           req.CallNonce,
	}, nil
}

func (p *Manager) invokeDefaultContract(contractType, contractAddress string, value int64, gasLimit int64, gasOverride int64, assetName, amount string) (*ContractTxResult, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	contract, err := contractcommon.DecodeContractAddress(contractAddress)
	if err != nil {
		return nil, err
	}
	if gasLimit == 0 {
		gasLimit = contractcommon.InvokeBaseGas
	}
	var funding swire.TxOut
	var inputs []swire.OutPoint
	var changeOutputs []*swire.TxOut
	var prevFetcher stxscript.PrevOutputFetcher
	var caller contractcommon.EVMAddress
	var fundingGasAmount int64
	var gasAmount int64
	var gasBaseFee int64
	var gasAsset string
	if contractType == ContractTypeEVM {
		if gasOverride != 0 {
			return nil, fmt.Errorf("gas override is not supported for EVM default invoke")
		}
		funding, inputs, changeOutputs, prevFetcher, caller, err = p.selectEVMDefaultContractFunding(value, assetName, amount)
		if err != nil {
			return nil, err
		}
	} else {
		fundingGasAmount, gasAsset, err = p.evmGasAssetAmount(gasLimit, false, gasOverride)
		if err != nil {
			return nil, err
		}
		gasBaseFee, err = p.evmBaseGasFee(contractcommon.InvokeBaseGas)
		if err != nil {
			return nil, err
		}
		if fundingGasAmount > math.MaxInt64-gasBaseFee {
			return nil, fmt.Errorf("gas asset amount overflows int64")
		}
		gasAmount = fundingGasAmount + gasBaseFee
		funding, inputs, changeOutputs, prevFetcher, caller, err = p.selectDefaultContractFunding(gasAsset, gasAmount, fundingGasAmount, value, assetName, amount)
		if err != nil {
			return nil, err
		}
	}
	pkScript, err := contractcommon.ContractPkScript(contract)
	if err != nil {
		return nil, err
	}
	funding.PkScript = pkScript
	tx := swire.NewMsgTx(swire.TxVersion)
	for _, input := range inputs {
		tx.AddTxIn(swire.NewTxIn(&input, nil, nil))
	}
	tx.AddTxOut(&funding)
	for _, change := range changeOutputs {
		tx.AddTxOut(change)
	}
	signedTx, err := p.SignContractTx_SatsNet(tx, prevFetcher, gasAsset, gasBaseFee)
	if err != nil {
		return nil, err
	}
	PrintJsonTx_SatsNet(signedTx, "DefaultInvokeContract")
	txid, err := p.BroadcastTx_SatsNet(signedTx)
	if err != nil {
		return nil, err
	}
	return &ContractTxResult{
		ContractType:    contractType,
		TxID:            txid,
		ContractAddress: contractAddress,
		Caller:          caller.String(),
		GasAssetAmount:  gasAmount,
		GasFeeAmount:    gasBaseFee,
		GasFundAmount:   fundingGasAmount,
		GasLimit:        gasLimit,
	}, nil
}

func (p *Manager) selectEVMDefaultContractFunding(value int64, assetName string, amount string) (swire.TxOut, []swire.OutPoint, []*swire.TxOut, stxscript.PrevOutputFetcher, contractcommon.EVMAddress, error) {
	if value < 0 {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("contract value must be non-negative")
	}
	plainRequired := value + DEFAULT_FEE_SATSNET
	if plainRequired < value {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("plain sats amount overflows int64")
	}
	selected := make([]string, 0)
	var businessName *swire.AssetName
	var businessAmt *indexer.Decimal
	var err error
	if assetName != "" && amount != "" {
		businessName = swire.NewAssetNameFromString(assetName)
		if businessName == nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("invalid default invoke asset %s", assetName)
		}
		businessAmt, err = indexer.NewDecimalFromString(amount, MAX_ASSET_DIVISIBILITY)
		if err != nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
		}
		assetUtxos, feeUtxos, err := p.GetUtxosWithAssetV2_SatsNet("", plainRequired, businessAmt, businessName, nil)
		if err != nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
		}
		selected = append(selected, assetUtxos...)
		selected = append(selected, feeUtxos...)
	} else {
		plainUtxos, _, err := p.GetUtxosWithAssetV2_SatsNet("", plainRequired, indexer.NewDefaultDecimal(0), &ASSET_PLAIN_SAT, nil)
		if err != nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
		}
		selected = append(selected, plainUtxos...)
	}

	outputMap := make(map[string]*TxOutput_SatsNet)
	address := p.wallet.GetAddress()
	for _, name := range []*swire.AssetName{businessName, &ASSET_PLAIN_SAT} {
		if name == nil {
			continue
		}
		for _, info := range p.l2IndexerClient.GetUtxoListWithTicker(address, name) {
			outputMap[info.OutPoint] = OutputInfoToOutput_SatsNet(info)
		}
	}
	prevFetcher := stxscript.NewMultiPrevOutFetcher(nil)
	inputs := make([]swire.OutPoint, 0, len(selected))
	var input TxOutput_SatsNet
	var callerPkScript []byte
	seen := make(map[string]bool)
	for _, utxo := range selected {
		if seen[utxo] {
			continue
		}
		seen[utxo] = true
		outpoint, err := swire.NewOutPointFromString(utxo)
		if err != nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
		}
		output := outputMap[utxo]
		if output == nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("selected utxo %s is missing from indexer asset lists", utxo)
		}
		input.Merge(output)
		inputs = append(inputs, *outpoint)
		prevFetcher.AddPrevOut(*outpoint, &output.OutValue)
		callerPkScript = output.OutValue.PkScript
	}
	caller, err := p.evmCallerFromPreviousPkScript(callerPkScript)
	if err != nil {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
	}

	var fundingAssets swire.TxAssets
	if businessName != nil && businessAmt != nil && businessAmt.Sign() > 0 {
		business := swire.AssetInfo{Name: *businessName, Amount: *businessAmt}
		if err := input.SubAsset(&business); err != nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
		}
		fundingAssets = swire.TxAssets{business}
	}
	if value > 0 {
		if err := input.SubAsset(&swire.AssetInfo{Name: ASSET_PLAIN_SAT, Amount: *indexer.NewDefaultDecimal(value), BindingSat: 1}); err != nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
		}
	}
	feeAsset := swire.AssetInfo{Name: ASSET_PLAIN_SAT, Amount: *indexer.NewDefaultDecimal(DEFAULT_FEE_SATSNET), BindingSat: 1}
	if err := input.SubAsset(&feeAsset); err != nil {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
	}
	changePkScript, err := GetP2TRpkScript(p.wallet.GetPaymentPubKey())
	if err != nil {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
	}
	changeTx := swire.NewMsgTx(swire.TxVersion)
	SplitChangeAsset(&input, changePkScript, changeTx)
	funding := *swire.NewTxOut(value, fundingAssets, nil)
	return funding, inputs, changeTx.TxOut, prevFetcher, caller, nil
}

type contractFundingAsset struct {
	Name   string
	Amount string
}

func contractFundingAssets(assets []ContractFundingAsset) []contractFundingAsset {
	out := make([]contractFundingAsset, 0, len(assets))
	for _, asset := range assets {
		name := strings.TrimSpace(asset.AssetName)
		amount := strings.TrimSpace(asset.Amount)
		if name == "" && amount == "" {
			continue
		}
		out = append(out, contractFundingAsset{Name: name, Amount: amount})
	}
	return out
}

func firstFundingAsset(assets []ContractFundingAsset) (string, string) {
	for _, asset := range assets {
		name := strings.TrimSpace(asset.AssetName)
		amount := strings.TrimSpace(asset.Amount)
		if name != "" || amount != "" {
			return name, amount
		}
	}
	return "", ""
}

func (p *Manager) selectDefaultContractFunding(gasAssetName string, gasAmount int64, fundingGasAmount int64, value int64, assetName string, amount string) (swire.TxOut, []swire.OutPoint, []*swire.TxOut, stxscript.PrevOutputFetcher, contractcommon.EVMAddress, error) {
	var fundingAssets []contractFundingAsset
	if strings.TrimSpace(assetName) != "" && strings.TrimSpace(amount) != "" {
		fundingAssets = []contractFundingAsset{{Name: assetName, Amount: amount}}
	}
	return p.selectUnifiedContractFunding(gasAssetName, gasAmount, fundingGasAmount, value, fundingAssets)
}

func (p *Manager) selectUnifiedContractFunding(gasAssetName string, gasAmount int64, fundingGasAmount int64, value int64, businessAssets []contractFundingAsset) (swire.TxOut, []swire.OutPoint, []*swire.TxOut, stxscript.PrevOutputFetcher, contractcommon.EVMAddress, error) {
	if gasAmount < 0 {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("gas asset amount must be non-negative")
	}
	if fundingGasAmount > gasAmount {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("contract funding gas amount %d exceeds selected gas amount %d", fundingGasAmount, gasAmount)
	}
	if fundingGasAmount < 0 {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("contract funding gas amount must be non-negative")
	}
	if value < 0 {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("contract value must be non-negative")
	}
	gasName := swire.NewAssetNameFromString(gasAssetName)
	if gasName == nil {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("invalid gas asset %s", gasAssetName)
	}

	fundingValue := value
	type parsedBusinessAsset struct {
		Name   *swire.AssetName
		Amount *indexer.Decimal
	}
	parsedBusinessAssets := make([]parsedBusinessAsset, 0, len(businessAssets))
	for _, asset := range businessAssets {
		assetName := strings.TrimSpace(asset.Name)
		amount := strings.TrimSpace(asset.Amount)
		if assetName == "" && amount == "" {
			continue
		}
		if assetName == "" || amount == "" {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("contract funding asset and amount must be provided together")
		}
		if assetName == contractcommon.SatoshiAssetName {
			assetValue, err := assetAmountStringToInt64("contract funding value", amount)
			if err != nil {
				return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
			}
			if fundingValue > math.MaxInt64-assetValue {
				return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("contract funding value overflows int64")
			}
			fundingValue += assetValue
			continue
		}
		name := swire.NewAssetNameFromString(assetName)
		if name == nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("invalid contract funding asset %s", assetName)
		}
		amt, err := indexer.NewDecimalFromString(amount, MAX_ASSET_DIVISIBILITY)
		if err != nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
		}
		if amt.Sign() <= 0 {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("contract funding asset %s amount must be positive", assetName)
		}
		parsedBusinessAssets = append(parsedBusinessAssets, parsedBusinessAsset{Name: name, Amount: amt})
	}

	gasAmt := indexer.NewDefaultDecimal(gasAmount)
	assetUtxos, feeUtxos, err := p.GetUtxosWithAssetV2_SatsNet("", fundingValue, gasAmt, gasName, nil)
	if err != nil {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
	}
	selected := append([]string{}, assetUtxos...)
	selected = append(selected, feeUtxos...)
	for _, asset := range parsedBusinessAssets {
		assetUtxos, _, err := p.GetUtxosWithAssetV2_SatsNet("", 0, asset.Amount, asset.Name, nil)
		if err != nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
		}
		selected = append(selected, assetUtxos...)
	}

	outputMap := make(map[string]*TxOutput_SatsNet)
	address := p.wallet.GetAddress()
	outputNames := []*swire.AssetName{gasName, &ASSET_PLAIN_SAT}
	for _, asset := range parsedBusinessAssets {
		outputNames = append(outputNames, asset.Name)
	}
	seenNames := make(map[string]bool)
	for _, name := range outputNames {
		if name == nil {
			continue
		}
		nameKey := name.String()
		if seenNames[nameKey] {
			continue
		}
		seenNames[nameKey] = true
		for _, info := range p.l2IndexerClient.GetUtxoListWithTicker(address, name) {
			outputMap[info.OutPoint] = OutputInfoToOutput_SatsNet(info)
		}
	}

	prevFetcher := stxscript.NewMultiPrevOutFetcher(nil)
	inputs := make([]swire.OutPoint, 0, len(selected))
	var input TxOutput_SatsNet
	var callerPkScript []byte
	seen := make(map[string]bool)
	for _, utxo := range selected {
		if seen[utxo] {
			continue
		}
		seen[utxo] = true
		outpoint, err := swire.NewOutPointFromString(utxo)
		if err != nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
		}
		output := outputMap[utxo]
		if output == nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("selected utxo %s is missing from indexer asset lists", utxo)
		}
		input.Merge(output)
		inputs = append(inputs, *outpoint)
		prevFetcher.AddPrevOut(*outpoint, &output.OutValue)
		callerPkScript = output.OutValue.PkScript
	}

	caller, err := p.evmCallerFromPreviousPkScript(callerPkScript)
	if err != nil {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
	}

	gasInputAsset := swire.AssetInfo{Name: *gasName, Amount: *gasAmt}
	if err := input.SubAsset(&gasInputAsset); err != nil {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
	}
	var fundingAssets swire.TxAssets
	if fundingGasAmount > 0 {
		fundingAssets = swire.TxAssets{{Name: *gasName, Amount: *indexer.NewDefaultDecimal(fundingGasAmount)}}
	}
	for _, asset := range parsedBusinessAssets {
		business := swire.AssetInfo{Name: *asset.Name, Amount: *asset.Amount}
		if err := input.SubAsset(&business); err != nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
		}
		if err := fundingAssets.Merge(swire.TxAssets{business}); err != nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
		}
	}
	if fundingValue > 0 {
		if err := input.SubAsset(&swire.AssetInfo{Name: ASSET_PLAIN_SAT, Amount: *indexer.NewDefaultDecimal(fundingValue), BindingSat: 1}); err != nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
		}
	}
	changePkScript, err := GetP2TRpkScript(p.wallet.GetPaymentPubKey())
	if err != nil {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
	}
	changeTx := swire.NewMsgTx(swire.TxVersion)
	SplitChangeAsset(&input, changePkScript, changeTx)
	funding := *swire.NewTxOut(fundingValue, fundingAssets, nil)
	return funding, inputs, changeTx.TxOut, prevFetcher, caller, nil
}

func (p *Manager) evmCallerFromPreviousPkScript(pkScript []byte) (contractcommon.EVMAddress, error) {
	var caller contractcommon.EVMAddress
	if len(pkScript) == 0 {
		return caller, fmt.Errorf("missing EVM caller previous output script")
	}
	params := &chaincfg.MainNetParams
	if IsTestNet() {
		params = &chaincfg.TestNetParams
	}
	_, addresses, _, err := stxscript.ExtractPkScriptAddrs(pkScript, params)
	if err != nil {
		return caller, err
	}
	if len(addresses) == 0 {
		return caller, fmt.Errorf("missing EVM caller previous output address")
	}
	copy(caller[:], btcutil.Hash160([]byte(addresses[0].EncodeAddress())))
	return caller, nil
}

func (p *Manager) evmBaseGasFee(baseGas int64) (int64, error) {
	height := p.satsNetBestHeight()
	return contractcommon.GasFeeAtHeight(baseGas, uint64(height))
}

func (p *Manager) evmGasAssetAmount(gasLimit int64, needsResult bool, override int64) (int64, string, error) {
	gasAssetName := GetGasAssetName()
	if override != 0 {
		return override, gasAssetName, nil
	}
	height := p.satsNetBestHeight()
	amount, err := contractcommon.GasFeeAtHeight(gasLimit, uint64(height))
	if err != nil {
		return 0, "", err
	}
	if needsResult {
		resultFee, err := contractcommon.GasFeeAtHeight(contractcommon.ResultBaseGas, uint64(height))
		if err != nil {
			return 0, "", err
		}
		if amount > math.MaxInt64-resultFee {
			return 0, "", fmt.Errorf("gas asset amount overflows int64")
		}
		amount += resultFee
	}
	return amount, gasAssetName, nil
}

func (p *Manager) walletEVMAddress() (contractcommon.EVMAddress, error) {
	if p.wallet == nil {
		return contractcommon.EVMAddress{}, fmt.Errorf("wallet is not created/unlocked")
	}
	var caller contractcommon.EVMAddress
	copy(caller[:], btcutil.Hash160([]byte(p.wallet.GetAddress())))
	return caller, nil
}

func (p *Manager) contractAddressPrefix() string {
	if IsTestNet() {
		return contractcommon.TestnetContractPrefix
	}
	return contractcommon.MainnetContractPrefix
}

func normalizeContractType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "", ContractTypeTemplate, "channel", "template:channel", TEMPLATE_CONTRACT_LIMITORDER, TEMPLATE_CONTRACT_SWAP, TEMPLATE_CONTRACT_AMM, TEMPLATE_CONTRACT_EXCHANGE:
		return ContractTypeTemplate
	case ContractTypeAgent, "agent:prediction":
		return ContractTypeAgent
	case ContractTypeEVM, "smart", "smart-contract":
		return ContractTypeEVM
	default:
		return strings.ToLower(strings.TrimSpace(t))
	}
}

func decodeHexField(name, s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		s = s[2:]
	}
	if s == "" {
		return nil, nil
	}
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("%s hex length must be even", name)
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	return b, nil
}

func evmAddressFromPublicKey(pubKey []byte) (contractcommon.EVMAddress, error) {
	var addr contractcommon.EVMAddress
	if len(pubKey) != 33 && len(pubKey) != 65 {
		return addr, fmt.Errorf("unsupported public key length %d", len(pubKey))
	}
	copy(addr[:], btcutil.Hash160(pubKey))
	return addr, nil
}
