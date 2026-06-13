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
	ContractType string

	TemplateName    string
	ContractContent string
	FeeRate         int64
	SendTxInL1      bool

	Template *TemplateContractDeployRequest
	EVM      *EVMContractDeployRequest
	Agent    *AgentContractDeployRequest
}

type ContractInvokeRequest struct {
	ContractType string

	ContractURL     string
	JSONInvokeParam string
	AssetName       string
	Amount          string
	FeeRate         int64
	DefaultInvoke   bool

	Template *TemplateContractInvokeRequest
	EVM      *EVMContractInvokeRequest
	Agent    *AgentContractInvokeRequest
}

type TemplateContractDeployRequest struct {
	TemplateName    string
	ContractContent string
	GasLimit        uint64
	RandomHex       string
	GasAssetAmount  uint64
	FundingValue    int64
}

type TemplateContractInvokeRequest struct {
	ContractAddress string
	JSONInvokeParam string
	GasLimit        uint64
	CallNonce       uint64
	GasAssetAmount  uint64
	Value           int64
	AssetName       string
	Amount          string
	DefaultInvoke   bool
	SkipResultFee   bool
}

type EVMContractDeployRequest struct {
	InitCodeHex            string
	BytecodeHex            string
	ConstructorCalldataHex string
	GasLimit               uint64
	DeployNonce            uint64
	GasAssetAmount         uint64
}

type EVMContractInvokeRequest struct {
	ContractAddress string
	JSONInvokeParam string
	GasLimit        uint64
	CallNonce       uint64
	GasAssetAmount  uint64
	Value           int64
	AssetName       string
	Amount          string
	DefaultInvoke   bool
	SkipResultFee   bool
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

type AgentContractDeployRequest struct {
	Subtype         string
	Prediction      *AgentPredictionContract
	ContractContent string
	GasLimit        uint64
	RandomHex       string
	GasAssetAmount  uint64
}

type AgentContractInvokeRequest struct {
	ContractAddress string
	JSONInvokeParam string
	GasLimit        uint64
	CallNonce       uint64
	GasAssetAmount  uint64
	BetAssetName    string
	BetAmount       string
	Value           int64
	AssetName       string
	Amount          string
	DefaultInvoke   bool
}

type ContractTxResult struct {
	ContractType    string `json:"contractType"`
	TxID            string `json:"txid"`
	ContractAddress string `json:"contractAddress,omitempty"`
	Caller          string `json:"caller,omitempty"`
	GasAssetAmount  uint64 `json:"gasAssetAmount,omitempty"`
	GasFeeAmount    uint64 `json:"gasFeeAmount,omitempty"`
	GasFundAmount   uint64 `json:"gasFundAmount,omitempty"`
	GasLimit        uint64 `json:"gasLimit,omitempty"`
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
		return p.deployEVMContract(req.EVM)
	case ContractTypeTemplate:
		return p.deployTemplateContract(req)
	case ContractTypeAgent:
		return p.deployAgentContract(req.Agent)
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

func (p *Manager) QueryFeeForInvokeUnifiedContract(req *ContractInvokeRequest) (uint64, error) {
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
	default:
		return "", fmt.Errorf("agent contract %s does not support %s", subtype, action)
	}
	return unifiedInvokeParamTemplate(action, innerParam)
}

func evmInvokeParamTemplate(action string) (string, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "call":
		return unifiedInvokeParamTemplate(action, map[string]interface{}{"calldataHex": ""})
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
	if wrapperParam.Action != "call" {
		return nil, fmt.Errorf("evm contract does not support %s", wrapperParam.Action)
	}
	var param struct {
		CalldataHex string `json:"calldataHex"`
	}
	if err := json.Unmarshal([]byte(wrapperParam.Param), &param); err != nil {
		return nil, err
	}
	calldata, err := decodeHexField("calldata", param.CalldataHex)
	if err != nil {
		return nil, err
	}
	wrapperParam.Param = base64.StdEncoding.EncodeToString(calldata)
	return wrapperParam, nil
}

func (p *Manager) queryTemplateInvokeFee(req *ContractInvokeRequest) (uint64, error) {
	treq := &TemplateContractInvokeRequest{}
	if req.Template != nil {
		*treq = *req.Template
	}
	if treq.ContractAddress == "" {
		treq.ContractAddress = req.ContractURL
	}
	if treq.AssetName == "" {
		treq.AssetName = req.AssetName
	}
	if treq.Amount == "" {
		treq.Amount = req.Amount
	}
	if treq.JSONInvokeParam == "" {
		treq.JSONInvokeParam = req.JSONInvokeParam
	}
	defaultInvoke := treq.DefaultInvoke || req.DefaultInvoke
	gasLimit := treq.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.InvokeBaseGas
	}
	if defaultInvoke {
		return p.queryDefaultInvokeFee(ContractTypeTemplate, gasLimit, treq.GasAssetAmount)
	}
	converted, err := ConvertUnifiedInvokeParam(ContractTypeTemplate, "", treq.JSONInvokeParam)
	if err != nil {
		return 0, err
	}
	if treq.ContractAddress != "" {
		if err := p.checkTemplateInvokeSupported(treq.ContractAddress, converted.Action); err != nil {
			return 0, err
		}
	}
	gasAmount, _, err := p.templateGasAssetAmount(gasLimit, !treq.SkipResultFee, treq.GasAssetAmount)
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

func (p *Manager) queryAgentInvokeFee(req *ContractInvokeRequest) (uint64, error) {
	if req == nil {
		return 0, fmt.Errorf("missing agent invoke fee request")
	}
	areq := &AgentContractInvokeRequest{}
	if req.Agent != nil {
		*areq = *req.Agent
	}
	if areq.ContractAddress == "" {
		areq.ContractAddress = req.ContractURL
	}
	if areq.JSONInvokeParam == "" {
		areq.JSONInvokeParam = req.JSONInvokeParam
	}
	defaultInvoke := areq.DefaultInvoke || req.DefaultInvoke
	gasLimit := areq.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.InvokeBaseGas
	}
	if defaultInvoke {
		return p.queryDefaultInvokeFee(ContractTypeAgent, gasLimit, areq.GasAssetAmount)
	}
	converted, err := ConvertUnifiedInvokeParam(ContractTypeAgent, contractcommon.SubtypePrediction, areq.JSONInvokeParam)
	if err != nil {
		return 0, err
	}
	gasAmount, _, err := p.agentGasAssetAmount(contractcommon.InvokeBaseGas, areq.GasAssetAmount)
	if err != nil {
		return 0, err
	}
	fundingAmount := uint64(0)
	if converted.Action == contractcommon.AgentInvokeAPIBet {
		fundingAmount, err = assetAmountStringToUint64("agent bet amount", areq.BetAmount)
		if err != nil {
			return 0, err
		}
		if math.MaxUint64-gasAmount < fundingAmount {
			return 0, fmt.Errorf("agent invoke gas amount overflows uint64")
		}
		gasAmount += fundingAmount
	}
	return gasAmount, nil
}

func (p *Manager) queryEVMInvokeFee(req *ContractInvokeRequest) (uint64, error) {
	if req == nil {
		return 0, fmt.Errorf("missing evm invoke fee request")
	}
	ereq := &EVMContractInvokeRequest{}
	if req.EVM != nil {
		*ereq = *req.EVM
	}
	if ereq.ContractAddress == "" {
		ereq.ContractAddress = req.ContractURL
	}
	if ereq.JSONInvokeParam == "" {
		ereq.JSONInvokeParam = req.JSONInvokeParam
	}
	gasLimit := ereq.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.InvokeBaseGas
	}
	if ereq.DefaultInvoke || req.DefaultInvoke {
		return 0, nil
	}
	if _, err := ConvertUnifiedInvokeParam(ContractTypeEVM, "", ereq.JSONInvokeParam); err != nil {
		return 0, err
	}
	gasAmount, _, err := p.evmGasAssetAmount(gasLimit, true, ereq.GasAssetAmount)
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

func (p *Manager) queryDefaultInvokeFee(contractType string, gasLimit uint64, gasOverride uint64) (uint64, error) {
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
	if math.MaxUint64-fundingGasAmount < gasBaseFee {
		return 0, fmt.Errorf("gas asset amount overflows uint64")
	}
	return fundingGasAmount + gasBaseFee, nil
}

func (p *Manager) deployAgentContract(req *AgentContractDeployRequest) (*ContractTxResult, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	if req == nil {
		return nil, fmt.Errorf("missing agent deploy request")
	}
	subtype := strings.TrimSpace(req.Subtype)
	if subtype == "" {
		subtype = contractcommon.SubtypePrediction
	}
	content, err := agentPredictionContent(req)
	if err != nil {
		return nil, err
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
	gasAmount := req.GasAssetAmount
	if gasAmount == 0 {
		if math.MaxUint64-invokeBaseFee < invokeBaseFee || math.MaxUint64-deployBaseFee < invokeBaseFee*2 {
			return nil, fmt.Errorf("agent deploy default gas amount overflows uint64")
		}
		gasAmount = deployBaseFee + invokeBaseFee*2
	}
	if gasAmount < deployBaseFee {
		return nil, fmt.Errorf("agent deploy gas asset amount %d is less than required base gas fee %d", gasAmount, deployBaseFee)
	}
	fundingAmount := gasAmount - deployBaseFee
	funding, inputs, changeOutputs, prevFetcher, _, err := p.selectEVMContractFunding(gasAsset, gasAmount, fundingAmount, 0)
	if err != nil {
		return nil, err
	}
	random, err := decodeTemplateRandom(req.RandomHex)
	if err != nil {
		return nil, err
	}
	if len(random) == 0 {
		random = []byte(fmt.Sprintf("%s:%d", p.wallet.GetAddress(), time.Now().UnixNano()))
	}
	tx, contractAddr, err := contractcommon.BuildAgentDeployTx(contractcommon.AgentDeployTxBuildRequest{
		ContractPrefix:  p.contractAddressPrefix(),
		Subtype:         subtype,
		AgentVersion:    contractcommon.CurrentAgentVersion,
		Deployer:        p.wallet.GetAddress(),
		Random:          random,
		ContractContent: content,
		GasLimit:        gasLimit,
		Funding:         funding,
		Inputs:          inputs,
		ChangeOutputs:   changeOutputs,
	})
	if err != nil {
		return nil, err
	}
	signedTx, err := p.SignTx_SatsNet(tx, prevFetcher)
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
	}, nil
}

func (p *Manager) invokeAgentContract(req *ContractInvokeRequest) (*ContractTxResult, error) {
	if req == nil {
		return nil, fmt.Errorf("missing agent invoke request")
	}
	areq := &AgentContractInvokeRequest{}
	if req.Agent != nil {
		*areq = *req.Agent
	}
	if areq.ContractAddress == "" {
		areq.ContractAddress = req.ContractURL
	}
	if areq.JSONInvokeParam == "" {
		areq.JSONInvokeParam = req.JSONInvokeParam
	}
	if areq.DefaultInvoke || req.DefaultInvoke {
		return p.invokeDefaultContract(ContractTypeAgent, areq.ContractAddress, areq.Value, areq.GasLimit, areq.GasAssetAmount, areq.AssetName, areq.Amount)
	}
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	contract, err := contractcommon.DecodeContractAddress(areq.ContractAddress)
	if err != nil {
		return nil, err
	}
	converted, err := ConvertUnifiedInvokeParam(ContractTypeAgent, contractcommon.SubtypePrediction, areq.JSONInvokeParam)
	if err != nil {
		return nil, err
	}
	param, err := base64.StdEncoding.DecodeString(converted.Param)
	if err != nil {
		return nil, fmt.Errorf("decode agent invoke param: %w", err)
	}
	gasLimit := areq.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.InvokeBaseGas
	}
	gasAmount, gasAsset, err := p.agentGasAssetAmount(contractcommon.InvokeBaseGas, areq.GasAssetAmount)
	if err != nil {
		return nil, err
	}
	fundingAmount := uint64(0)
	if converted.Action == contractcommon.AgentInvokeAPIBet {
		fundingAmount, err = assetAmountStringToUint64("agent bet amount", areq.BetAmount)
		if err != nil {
			return nil, err
		}
		if math.MaxUint64-gasAmount < fundingAmount {
			return nil, fmt.Errorf("agent invoke gas amount overflows uint64")
		}
		gasAmount += fundingAmount
	}
	callNonce := areq.CallNonce
	if callNonce == 0 {
		callNonce = uint64(time.Now().UnixNano())
	}
	funding, inputs, changeOutputs, prevFetcher, _, err := p.selectEVMContractFunding(gasAsset, gasAmount, fundingAmount, areq.Value)
	if err != nil {
		return nil, err
	}
	tx, err := contractcommon.BuildAgentInvokeTx(contractcommon.AgentInvokeTxBuildRequest{
		Contract:      contract,
		GasLimit:      gasLimit,
		CallNonce:     callNonce,
		Action:        converted.Action,
		Param:         param,
		Funding:       funding,
		Inputs:        inputs,
		ChangeOutputs: changeOutputs,
	})
	if err != nil {
		return nil, err
	}
	signedTx, err := p.SignTx_SatsNet(tx, prevFetcher)
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
		ContractAddress: areq.ContractAddress,
		GasAssetAmount:  gasAmount,
		GasFeeAmount:    gasAmount - fundingAmount,
		GasFundAmount:   fundingAmount,
		GasLimit:        gasLimit,
		Nonce:           callNonce,
	}, nil
}

func (p *Manager) deployTemplateContract(req *ContractDeployRequest) (*ContractTxResult, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	treq := &TemplateContractDeployRequest{}
	if req.Template != nil {
		*treq = *req.Template
	}
	if treq.TemplateName == "" {
		treq.TemplateName = req.TemplateName
	}
	if treq.ContractContent == "" {
		treq.ContractContent = req.ContractContent
	}
	templateName, content, fundingValue, fundingAssetAmount, err := p.buildNativeTemplateContract(treq)
	if err != nil {
		return nil, err
	}
	if treq.FundingValue != 0 {
		fundingValue = treq.FundingValue
	}
	gasLimit := treq.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.DeployBaseGas
	}
	gasAmount, gasAsset, err := p.templateGasAssetAmount(gasLimit, true, treq.GasAssetAmount)
	if err != nil {
		return nil, err
	}
	gasBaseFee, err := p.evmBaseGasFee(contractcommon.DeployBaseGas)
	if err != nil {
		return nil, err
	}
	if math.MaxUint64-gasAmount < fundingAssetAmount {
		return nil, fmt.Errorf("template funding asset amount overflows uint64")
	}
	gasAmount += fundingAssetAmount
	if gasAmount < gasBaseFee {
		return nil, fmt.Errorf("gas asset amount %d is less than required base gas fee %d", gasAmount, gasBaseFee)
	}
	funding, inputs, changeOutputs, prevFetcher, _, err := p.selectEVMContractFunding(gasAsset, gasAmount, gasAmount-gasBaseFee, fundingValue)
	if err != nil {
		return nil, err
	}
	random, err := decodeTemplateRandom(treq.RandomHex)
	if err != nil {
		return nil, err
	}
	if len(random) == 0 {
		random = []byte(fmt.Sprintf("%s:%d", p.wallet.GetAddress(), time.Now().UnixNano()))
	}
	tx, contractAddr, err := contractcommon.BuildTemplateDeployTx(contractcommon.TemplateDeployTxBuildRequest{
		ContractPrefix:  p.contractAddressPrefix(),
		TemplateName:    templateName,
		TemplateVersion: contractcommon.CurrentTemplateVersion,
		ContractContent: content,
		Deployer:        p.wallet.GetAddress(),
		Random:          random,
		GasLimit:        gasLimit,
		Funding:         funding,
		Inputs:          inputs,
		ChangeOutputs:   changeOutputs,
	})
	if err != nil {
		return nil, err
	}
	signedTx, err := p.SignTx_SatsNet(tx, prevFetcher)
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
	}, nil
}

func (p *Manager) invokeTemplateContract(req *ContractInvokeRequest) (*ContractTxResult, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	treq := &TemplateContractInvokeRequest{}
	if req.Template != nil {
		*treq = *req.Template
	}
	if treq.ContractAddress == "" {
		treq.ContractAddress = req.ContractURL
	}
	if treq.JSONInvokeParam == "" {
		treq.JSONInvokeParam = req.JSONInvokeParam
	}
	if treq.AssetName == "" {
		treq.AssetName = req.AssetName
	}
	if treq.Amount == "" {
		treq.Amount = req.Amount
	}
	if treq.DefaultInvoke || req.DefaultInvoke {
		return p.invokeDefaultContract(ContractTypeTemplate, treq.ContractAddress, treq.Value, treq.GasLimit, treq.GasAssetAmount, treq.AssetName, treq.Amount)
	}
	contract, err := contractcommon.DecodeContractAddress(treq.ContractAddress)
	if err != nil {
		return nil, err
	}
	converted, err := ConvertUnifiedInvokeParam(ContractTypeTemplate, "", treq.JSONInvokeParam)
	if err != nil {
		return nil, err
	}
	if err := p.checkTemplateInvokeSupported(treq.ContractAddress, converted.Action); err != nil {
		return nil, err
	}
	param, err := base64.StdEncoding.DecodeString(converted.Param)
	if err != nil {
		return nil, fmt.Errorf("decode template invoke param: %w", err)
	}
	gasLimit := treq.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.InvokeBaseGas
	}
	gasAmount, gasAsset, err := p.templateGasAssetAmount(gasLimit, !treq.SkipResultFee, treq.GasAssetAmount)
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
	callNonce := treq.CallNonce
	if callNonce == 0 {
		callNonce = uint64(time.Now().UnixNano())
	}
	funding, inputs, changeOutputs, prevFetcher, _, err := p.selectDefaultContractFunding(gasAsset, gasAmount, gasAmount-gasBaseFee, treq.Value, treq.AssetName, treq.Amount)
	if err != nil {
		return nil, err
	}
	tx, err := contractcommon.BuildTemplateInvokeTx(contractcommon.TemplateInvokeTxBuildRequest{
		Contract:      contract,
		GasLimit:      gasLimit,
		CallNonce:     callNonce,
		Action:        converted.Action,
		Param:         param,
		Funding:       funding,
		Inputs:        inputs,
		ChangeOutputs: changeOutputs,
	})
	if err != nil {
		return nil, err
	}
	signedTx, err := p.SignTx_SatsNet(tx, prevFetcher)
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
		ContractAddress: treq.ContractAddress,
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

func (p *Manager) buildNativeTemplateContract(req *TemplateContractDeployRequest) (string, []byte, int64, uint64, error) {
	if req == nil {
		return "", nil, 0, 0, fmt.Errorf("missing template deploy request")
	}
	name := normalizeTemplateName(req.TemplateName)
	if name == "" {
		return "", nil, 0, 0, fmt.Errorf("missing template name")
	}
	if name == TEMPLATE_CONTRACT_EXCHANGE {
		var exchange contractcommon.TemplateExchangeContract
		if err := json.Unmarshal([]byte(req.ContractContent), &exchange); err != nil {
			return "", nil, 0, 0, err
		}
		content, err := contractcommon.EncodeTemplateExchangeContent(exchange)
		if err != nil {
			return "", nil, 0, 0, err
		}
		return contractcommon.TemplateExchange, content, 0, 0, nil
	}
	contract, err := ContractContentUnMarsh(name, req.ContractContent)
	if err != nil {
		return "", nil, 0, 0, err
	}
	assetName := contract.GetAssetName().String()
	switch name {
	case TEMPLATE_CONTRACT_LIMITORDER, TEMPLATE_CONTRACT_SWAP:
		content, err := contractcommon.EncodeTemplateLimitOrderContent(assetName)
		if err != nil {
			return "", nil, 0, 0, err
		}
		return contractcommon.TemplateLimitOrder, content, 0, 0, nil
	case TEMPLATE_CONTRACT_AMM:
		amm, ok := contract.(*AmmContract)
		if !ok {
			return "", nil, 0, 0, fmt.Errorf("template content is not AMM")
		}
		assetFunding := uint64(0)
		gasAsset := GetGasAssetName()
		if assetName == gasAsset {
			amt, err := indexer.NewDecimalFromString(amm.AssetAmt, MAX_ASSET_DIVISIBILITY)
			if err != nil {
				return "", nil, 0, 0, err
			}
			if amt.Int64() < 0 {
				return "", nil, 0, 0, fmt.Errorf("invalid AMM asset amount %s", amm.AssetAmt)
			}
			assetFunding = uint64(amt.Int64())
		}
		content, err := contractcommon.EncodeTemplateAMMContent(assetName, amm.AssetAmt, amm.SatValue, amm.K)
		if err != nil {
			return "", nil, 0, 0, err
		}
		return contractcommon.TemplateAMM, content, amm.SatValue, assetFunding, nil
	default:
		return "", nil, 0, 0, fmt.Errorf("unsupported template contract %s", req.TemplateName)
	}
}

func agentPredictionContent(req *AgentContractDeployRequest) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("missing agent deploy request")
	}
	if req.Prediction != nil {
		prediction := contractcommon.AgentPredictionContract{
			Subtype:      req.Prediction.Subtype,
			Title:        req.Prediction.Title,
			Description:  req.Prediction.Description,
			TimeBase:     req.Prediction.TimeBase,
			EventTime:    req.Prediction.EventTime,
			BetDeadline:  req.Prediction.BetDeadline,
			ConfirmAfter: req.Prediction.ConfirmAfter,
			SourceURL:    req.Prediction.SourceURL,
			BetAsset:     req.Prediction.BetAsset,
			MinBetUnit:   req.Prediction.MinBetUnit,
		}
		if prediction.Subtype == "" {
			prediction.Subtype = contractcommon.SubtypePrediction
		}
		prediction.Outcomes = make([]contractcommon.AgentPredictionOutcome, 0, len(req.Prediction.Outcomes))
		for _, outcome := range req.Prediction.Outcomes {
			prediction.Outcomes = append(prediction.Outcomes, contractcommon.AgentPredictionOutcome{ID: outcome.ID, Text: outcome.Text})
		}
		return prediction.Encode()
	}
	if strings.TrimSpace(req.ContractContent) == "" {
		return nil, fmt.Errorf("missing agent contract content")
	}
	return []byte(req.ContractContent), nil
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

func (p *Manager) agentGasAssetAmount(baseGas uint64, override uint64) (uint64, string, error) {
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

func assetAmountStringToUint64(name, value string) (uint64, error) {
	amount, err := indexer.NewDecimalFromString(value, MAX_ASSET_DIVISIBILITY)
	if err != nil {
		return 0, fmt.Errorf("decode %s: %w", name, err)
	}
	if amount.Int64() < 0 {
		return 0, fmt.Errorf("%s must be non-negative", name)
	}
	return uint64(amount.Int64()), nil
}

func decodeTemplateRandom(randomHex string) ([]byte, error) {
	if randomHex == "" {
		return nil, nil
	}
	return decodeHexField("template random", randomHex)
}

func (p *Manager) templateGasAssetAmount(gasLimit uint64, needsResult bool, override uint64) (uint64, string, error) {
	return p.evmGasAssetAmount(gasLimit, needsResult, override)
}

func normalizeTemplateName(name string) string {
	return contractcommon.NormalizeTemplateName(name)
}

func (p *Manager) EstimateEVMDeployContract(req *EVMContractDeployRequest) (*ContractTxResult, error) {
	if req == nil {
		return nil, fmt.Errorf("missing evm deploy request")
	}
	caller, err := p.walletEVMAddress()
	if err != nil {
		return nil, err
	}
	gasLimit := req.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.DeployBaseGas
	}
	gasAmount, _, err := p.evmGasAssetAmount(gasLimit, true, req.GasAssetAmount)
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
	contract, err := contractcommon.DeriveEVMCreateContractAddress(p.contractAddressPrefix(), caller, req.DeployNonce)
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
		Nonce:           req.DeployNonce,
	}, nil
}

func (p *Manager) deployEVMContract(req *EVMContractDeployRequest) (*ContractTxResult, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	estimate, err := p.EstimateEVMDeployContract(req)
	if err != nil {
		return nil, err
	}
	initCode, err := decodeEVMInitCode(req)
	if err != nil {
		return nil, err
	}
	gasAsset := GetGasAssetName()
	funding, inputs, changeOutputs, prevFetcher, caller, err := p.selectEVMContractFunding(gasAsset, estimate.GasAssetAmount, estimate.GasFundAmount, 0)
	if err != nil {
		return nil, err
	}
	tx, contract, err := contractcommon.BuildEVMDeployTx(contractcommon.EVMDeployTxBuildRequest{
		ContractPrefix: p.contractAddressPrefix(),
		Caller:         caller,
		GasLimit:       estimate.GasLimit,
		DeployNonce:    estimate.Nonce,
		InitCode:       initCode,
		Funding:        funding,
		Inputs:         inputs,
		ChangeOutputs:  changeOutputs,
	})
	if err != nil {
		return nil, err
	}
	signedTx, err := p.SignTx_SatsNet(tx, prevFetcher)
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
	ereq := &EVMContractInvokeRequest{}
	if req.EVM != nil {
		*ereq = *req.EVM
	}
	if ereq.ContractAddress == "" {
		ereq.ContractAddress = req.ContractURL
	}
	if ereq.JSONInvokeParam == "" {
		ereq.JSONInvokeParam = req.JSONInvokeParam
	}
	if ereq.DefaultInvoke || req.DefaultInvoke {
		return p.invokeDefaultContract(ContractTypeEVM, ereq.ContractAddress, ereq.Value, ereq.GasLimit, ereq.GasAssetAmount, ereq.AssetName, ereq.Amount)
	}
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	contract, err := contractcommon.DecodeContractAddress(ereq.ContractAddress)
	if err != nil {
		return nil, err
	}
	gasLimit := ereq.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.InvokeBaseGas
	}
	gasAmount, gasAsset, err := p.evmGasAssetAmount(gasLimit, true, ereq.GasAssetAmount)
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
	converted, err := ConvertUnifiedInvokeParam(ContractTypeEVM, "", ereq.JSONInvokeParam)
	if err != nil {
		return nil, err
	}
	calldata, err := base64.StdEncoding.DecodeString(converted.Param)
	if err != nil {
		return nil, fmt.Errorf("decode evm calldata: %w", err)
	}
	funding, inputs, changeOutputs, prevFetcher, caller, err := p.selectEVMContractFunding(gasAsset, gasAmount, gasAmount-gasBaseFee, ereq.Value)
	if err != nil {
		return nil, err
	}
	tx, err := contractcommon.BuildEVMInvokeTx(contractcommon.EVMInvokeTxBuildRequest{
		Contract:      contract,
		GasLimit:      gasLimit,
		CallNonce:     ereq.CallNonce,
		Calldata:      calldata,
		Funding:       funding,
		Inputs:        inputs,
		ChangeOutputs: changeOutputs,
	})
	if err != nil {
		return nil, err
	}
	signedTx, err := p.SignTx_SatsNet(tx, prevFetcher)
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
		ContractAddress: ereq.ContractAddress,
		Caller:          caller.String(),
		GasAssetAmount:  gasAmount,
		GasFeeAmount:    gasBaseFee,
		GasFundAmount:   gasAmount - gasBaseFee,
		GasLimit:        gasLimit,
		Nonce:           ereq.CallNonce,
	}, nil
}

func (p *Manager) invokeDefaultContract(contractType, contractAddress string, value int64, gasLimit uint64, gasOverride uint64, assetName, amount string) (*ContractTxResult, error) {
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
	var fundingGasAmount uint64
	var gasAmount uint64
	var gasBaseFee uint64
	if contractType == ContractTypeEVM {
		if gasOverride != 0 {
			return nil, fmt.Errorf("gas override is not supported for EVM default invoke")
		}
		funding, inputs, changeOutputs, prevFetcher, caller, err = p.selectEVMDefaultContractFunding(value, assetName, amount)
		if err != nil {
			return nil, err
		}
	} else {
		var gasAsset string
		fundingGasAmount, gasAsset, err = p.evmGasAssetAmount(gasLimit, false, gasOverride)
		if err != nil {
			return nil, err
		}
		gasBaseFee, err = p.evmBaseGasFee(contractcommon.InvokeBaseGas)
		if err != nil {
			return nil, err
		}
		if math.MaxUint64-fundingGasAmount < gasBaseFee {
			return nil, fmt.Errorf("gas asset amount overflows uint64")
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
	signedTx, err := p.SignTx_SatsNet(tx, prevFetcher)
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

func (p *Manager) selectDefaultContractFunding(gasAssetName string, gasAmount uint64, fundingGasAmount uint64, value int64, assetName string, amount string) (swire.TxOut, []swire.OutPoint, []*swire.TxOut, stxscript.PrevOutputFetcher, contractcommon.EVMAddress, error) {
	if gasAmount > uint64(math.MaxInt64) {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("gas asset amount overflows int64")
	}
	if fundingGasAmount > gasAmount {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("contract funding gas amount %d exceeds selected gas amount %d", fundingGasAmount, gasAmount)
	}
	if fundingGasAmount > uint64(math.MaxInt64) {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("contract funding gas amount overflows int64")
	}
	if value < 0 {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("contract value must be non-negative")
	}
	gasName := swire.NewAssetNameFromString(gasAssetName)
	if gasName == nil {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("invalid gas asset %s", gasAssetName)
	}
	selected := make([]string, 0)
	gasAmt := indexer.NewDefaultDecimal(int64(gasAmount))
	gasAssetUtxos, gasPlainUtxos, err := p.GetUtxosWithAssetV2_SatsNet("", value, gasAmt, gasName, nil)
	if err != nil {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
	}
	selected = append(selected, gasAssetUtxos...)
	selected = append(selected, gasPlainUtxos...)
	var businessName *swire.AssetName
	var businessAmt *indexer.Decimal
	if assetName != "" && amount != "" {
		businessName = swire.NewAssetNameFromString(assetName)
		if businessName == nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("invalid default invoke asset %s", assetName)
		}
		businessAmt, err = indexer.NewDecimalFromString(amount, MAX_ASSET_DIVISIBILITY)
		if err != nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
		}
		assetUtxos, _, err := p.GetUtxosWithAssetV2_SatsNet("", 0, businessAmt, businessName, nil)
		if err != nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
		}
		selected = append(selected, assetUtxos...)
	}
	outputMap := make(map[string]*TxOutput_SatsNet)
	address := p.wallet.GetAddress()
	for _, name := range []*swire.AssetName{gasName, businessName, &ASSET_PLAIN_SAT} {
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
	if err := input.SubAsset(&swire.AssetInfo{Name: *gasName, Amount: *gasAmt}); err != nil {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
	}
	var fundingAssets swire.TxAssets
	if fundingGasAmount > 0 {
		fundingGasAmt := indexer.NewDefaultDecimal(int64(fundingGasAmount))
		fundingAssets = swire.TxAssets{{Name: *gasName, Amount: *fundingGasAmt}}
	}
	if businessName != nil && businessAmt != nil && businessAmt.Sign() > 0 {
		business := swire.AssetInfo{Name: *businessName, Amount: *businessAmt}
		if err := input.SubAsset(&business); err != nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
		}
		if err := fundingAssets.Merge(swire.TxAssets{business}); err != nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
		}
	}
	if value > 0 {
		if err := input.SubAsset(&swire.AssetInfo{Name: ASSET_PLAIN_SAT, Amount: *indexer.NewDefaultDecimal(value), BindingSat: 1}); err != nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
		}
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

func (p *Manager) selectEVMContractFunding(gasAssetName string, gasAmount uint64, fundingGasAmount uint64, value int64) (swire.TxOut, []swire.OutPoint, []*swire.TxOut, stxscript.PrevOutputFetcher, contractcommon.EVMAddress, error) {
	if gasAmount > uint64(math.MaxInt64) {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("gas asset amount overflows int64")
	}
	if fundingGasAmount > gasAmount {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("contract funding gas amount %d exceeds selected gas amount %d", fundingGasAmount, gasAmount)
	}
	if fundingGasAmount > uint64(math.MaxInt64) {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("contract funding gas amount overflows int64")
	}
	if value < 0 {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("contract value must be non-negative")
	}
	gasName := swire.NewAssetNameFromString(gasAssetName)
	if gasName == nil {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, fmt.Errorf("invalid gas asset %s", gasAssetName)
	}
	gasAmt := indexer.NewDefaultDecimal(int64(gasAmount))
	assetUtxos, feeUtxos, err := p.GetUtxosWithAssetV2_SatsNet("", value, gasAmt, gasName, nil)
	if err != nil {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
	}
	selected := append([]string{}, assetUtxos...)
	selected = append(selected, feeUtxos...)
	outputMap := make(map[string]*TxOutput_SatsNet)
	address := p.wallet.GetAddress()
	for _, info := range p.l2IndexerClient.GetUtxoListWithTicker(address, gasName) {
		outputMap[info.OutPoint] = OutputInfoToOutput_SatsNet(info)
	}
	for _, info := range p.l2IndexerClient.GetUtxoListWithTicker(address, &ASSET_PLAIN_SAT) {
		if _, ok := outputMap[info.OutPoint]; !ok {
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
	if value > 0 {
		if err := input.SubAsset(&swire.AssetInfo{Name: ASSET_PLAIN_SAT, Amount: *indexer.NewDefaultDecimal(value), BindingSat: 1}); err != nil {
			return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
		}
	}
	changePkScript, err := GetP2TRpkScript(p.wallet.GetPaymentPubKey())
	if err != nil {
		return swire.TxOut{}, nil, nil, nil, contractcommon.EVMAddress{}, err
	}
	changeTx := swire.NewMsgTx(swire.TxVersion)
	SplitChangeAsset(&input, changePkScript, changeTx)
	var fundingAssets swire.TxAssets
	if fundingGasAmount > 0 {
		fundingAssets = swire.TxAssets{{Name: *gasName, Amount: *indexer.NewDefaultDecimal(int64(fundingGasAmount))}}
	}
	funding := *swire.NewTxOut(value, fundingAssets, nil)
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

func (p *Manager) evmBaseGasFee(baseGas uint64) (uint64, error) {
	height := p.satsNetBestHeight()
	return contractcommon.GasFeeAtHeight(baseGas, uint64(height))
}

func (p *Manager) evmGasAssetAmount(gasLimit uint64, needsResult bool, override uint64) (uint64, string, error) {
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
		if math.MaxUint64-amount < resultFee {
			return 0, "", fmt.Errorf("gas asset amount overflows uint64")
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

func decodeEVMInitCode(req *EVMContractDeployRequest) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("missing evm deploy request")
	}
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
