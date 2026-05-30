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
	agentcontract "github.com/sat20-labs/satoshinet/contract/agent"
	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
	"golang.org/x/crypto/sha3"
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
	GasAssetName    string
	FundingValue    int64
}

type TemplateContractInvokeRequest struct {
	ContractAddress string
	JSONInvokeParam string
	GasLimit        uint64
	CallNonce       uint64
	GasAssetAmount  uint64
	GasAssetName    string
	Value           int64
	SkipResultFee   bool
}

type EVMContractDeployRequest struct {
	InitCodeHex            string
	BytecodeHex            string
	ConstructorCalldataHex string
	GasLimit               uint64
	DeployNonce            uint64
	GasAssetAmount         uint64
	GasAssetName           string
}

type EVMContractInvokeRequest struct {
	ContractAddress string
	CalldataHex     string
	GasLimit        uint64
	CallNonce       uint64
	GasAssetAmount  uint64
	GasAssetName    string
	Value           int64
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
	GasAssetName    string
}

type AgentContractInvokeRequest struct {
	ContractAddress string
	Action          string
	ParamJSON       string
	Bet             *AgentPredictionBetParam
	GasLimit        uint64
	CallNonce       uint64
	GasAssetAmount  uint64
	GasAssetName    string
	BetAssetName    string
	BetAmount       string
	Value           int64
}

type ContractTxResult struct {
	ContractType    string `json:"contractType"`
	TxID            string `json:"txid"`
	ContractAddress string `json:"contractAddress,omitempty"`
	Caller          string `json:"caller,omitempty"`
	GasAssetName    string `json:"gasAssetName,omitempty"`
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
		return p.invokeEVMContract(req.EVM)
	case ContractTypeTemplate:
		return p.invokeTemplateContract(req)
	case ContractTypeAgent:
		return p.invokeAgentContract(req.Agent)
	default:
		return nil, fmt.Errorf("unsupported contract type %s", req.ContractType)
	}
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
		subtype = agentcontract.SubtypePrediction
	}
	content, err := agentPredictionContent(req)
	if err != nil {
		return nil, err
	}
	if subtype == agentcontract.SubtypePrediction {
		contract, err := agentcontract.DecodePredictionContract(content)
		if err != nil {
			return nil, fmt.Errorf("decode prediction contract: %w", err)
		}
		if err := contract.Check(); err != nil {
			return nil, err
		}
	}
	gasLimit := req.GasLimit
	if gasLimit == 0 {
		gasLimit = agentcontract.DefaultGasConfig().DeployBaseGas
	}
	deployBaseFee, gasAsset, err := p.agentGasAssetAmount(agentcontract.DefaultGasConfig().DeployBaseGas, 0, req.GasAssetName)
	if err != nil {
		return nil, err
	}
	invokeBaseFee, _, err := p.agentGasAssetAmount(agentcontract.DefaultGasConfig().InvokeBaseGas, 0, gasAsset)
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
	tx, contractAddr, err := agentcontract.BuildDeployTx(agentcontract.DeployTxBuildRequest{
		ContractPrefix:  p.contractAddressPrefix(),
		Subtype:         subtype,
		AgentVersion:    agentcontract.CurrentAgentVersion,
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
		GasAssetName:    gasAsset,
		GasAssetAmount:  gasAmount,
		GasFeeAmount:    deployBaseFee,
		GasFundAmount:   fundingAmount,
		GasLimit:        gasLimit,
	}, nil
}

func (p *Manager) invokeAgentContract(req *AgentContractInvokeRequest) (*ContractTxResult, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	if req == nil {
		return nil, fmt.Errorf("missing agent invoke request")
	}
	contract, err := contractcommon.DecodeContractAddress(req.ContractAddress)
	if err != nil {
		return nil, err
	}
	action := strings.TrimSpace(req.Action)
	if action == "" && req.Bet != nil {
		action = agentcontract.InvokeAPIBet
	}
	if action == "" {
		return nil, fmt.Errorf("missing agent invoke action")
	}
	param, err := agentInvokeParam(req)
	if err != nil {
		return nil, err
	}
	gasLimit := req.GasLimit
	if gasLimit == 0 {
		gasLimit = agentcontract.DefaultGasConfig().InvokeBaseGas
	}
	gasAmount, gasAsset, err := p.agentGasAssetAmount(agentcontract.DefaultGasConfig().InvokeBaseGas, req.GasAssetAmount, req.GasAssetName)
	if err != nil {
		return nil, err
	}
	fundingAmount := uint64(0)
	if action == agentcontract.InvokeAPIBet {
		fundingAmount, err = assetAmountStringToUint64("agent bet amount", req.BetAmount)
		if err != nil {
			return nil, err
		}
		betAsset := req.BetAssetName
		if betAsset == "" {
			betAsset = gasAsset
		}
		if betAsset != gasAsset {
			return nil, fmt.Errorf("agent bet asset %s must match gas asset %s in this sdk path", betAsset, gasAsset)
		}
		if math.MaxUint64-gasAmount < fundingAmount {
			return nil, fmt.Errorf("agent invoke gas amount overflows uint64")
		}
		gasAmount += fundingAmount
	}
	callNonce := req.CallNonce
	if callNonce == 0 {
		callNonce = uint64(time.Now().UnixNano())
	}
	funding, inputs, changeOutputs, prevFetcher, _, err := p.selectEVMContractFunding(gasAsset, gasAmount, fundingAmount, req.Value)
	if err != nil {
		return nil, err
	}
	tx, err := agentcontract.BuildInvokeTx(agentcontract.InvokeTxBuildRequest{
		Contract:      contract,
		GasLimit:      gasLimit,
		CallNonce:     callNonce,
		Action:        action,
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
		ContractAddress: req.ContractAddress,
		GasAssetName:    gasAsset,
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
	contract, fundingValue, fundingAssetAmount, err := p.buildNativeTemplateContract(treq)
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
	gasAmount, gasAsset, err := p.templateGasAssetAmount(gasLimit, true, treq.GasAssetAmount, treq.GasAssetName)
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
	tx, contractAddr, err := tmplcontract.BuildDeployTx(tmplcontract.DeployTxBuildRequest{
		ContractPrefix: p.contractAddressPrefix(),
		Contract:       contract,
		Deployer:       p.wallet.GetAddress(),
		Random:         random,
		GasLimit:       gasLimit,
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
	PrintJsonTx_SatsNet(signedTx, "DeployTemplateContract")
	txid, err := p.BroadcastTx_SatsNet(signedTx)
	if err != nil {
		return nil, err
	}
	return &ContractTxResult{
		ContractType:    ContractTypeTemplate,
		TxID:            txid,
		ContractAddress: contractAddr.EncodeAddress(),
		GasAssetName:    gasAsset,
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
	contract, err := contractcommon.DecodeContractAddress(treq.ContractAddress)
	if err != nil {
		return nil, err
	}
	converted, err := ConvertInvokeParam(treq.JSONInvokeParam, false)
	if err != nil {
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
	gasAmount, gasAsset, err := p.templateGasAssetAmount(gasLimit, !treq.SkipResultFee, treq.GasAssetAmount, treq.GasAssetName)
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
	funding, inputs, changeOutputs, prevFetcher, _, err := p.selectEVMContractFunding(gasAsset, gasAmount, gasAmount-gasBaseFee, treq.Value)
	if err != nil {
		return nil, err
	}
	tx, err := tmplcontract.BuildInvokeTx(tmplcontract.InvokeTxBuildRequest{
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
		GasAssetName:    gasAsset,
		GasAssetAmount:  gasAmount,
		GasFeeAmount:    gasBaseFee,
		GasFundAmount:   gasAmount - gasBaseFee,
		GasLimit:        gasLimit,
		Nonce:           callNonce,
	}, nil
}

func (p *Manager) buildNativeTemplateContract(req *TemplateContractDeployRequest) (tmplcontract.Contract, int64, uint64, error) {
	if req == nil {
		return nil, 0, 0, fmt.Errorf("missing template deploy request")
	}
	name := normalizeTemplateName(req.TemplateName)
	if name == "" {
		return nil, 0, 0, fmt.Errorf("missing template name")
	}
	contract, err := ContractContentUnMarsh(name, req.ContractContent)
	if err != nil {
		return nil, 0, 0, err
	}
	assetName := contract.GetAssetName().String()
	switch name {
	case TEMPLATE_CONTRACT_LIMITORDER, TEMPLATE_CONTRACT_SWAP:
		return tmplcontract.NewLimitOrderContract(assetName), 0, 0, nil
	case TEMPLATE_CONTRACT_AMM:
		amm, ok := contract.(*AmmContract)
		if !ok {
			return nil, 0, 0, fmt.Errorf("template content is not AMM")
		}
		assetFunding := uint64(0)
		gasAsset := req.GasAssetName
		if gasAsset == "" {
			gasAsset = contractcommon.GasAssetName
		}
		if assetName == gasAsset {
			amt, err := indexer.NewDecimalFromString(amm.AssetAmt, MAX_ASSET_DIVISIBILITY)
			if err != nil {
				return nil, 0, 0, err
			}
			if amt.Int64() < 0 {
				return nil, 0, 0, fmt.Errorf("invalid AMM asset amount %s", amm.AssetAmt)
			}
			assetFunding = uint64(amt.Int64())
		}
		return tmplcontract.NewAMMContract(assetName, amm.AssetAmt, amm.SatValue, amm.K), amm.SatValue, assetFunding, nil
	default:
		return nil, 0, 0, fmt.Errorf("unsupported template contract %s", req.TemplateName)
	}
}

func agentPredictionContent(req *AgentContractDeployRequest) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("missing agent deploy request")
	}
	if req.Prediction != nil {
		prediction := agentcontract.PredictionContract{
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
			prediction.Subtype = agentcontract.SubtypePrediction
		}
		prediction.Outcomes = make([]agentcontract.PredictionOutcome, 0, len(req.Prediction.Outcomes))
		for _, outcome := range req.Prediction.Outcomes {
			prediction.Outcomes = append(prediction.Outcomes, agentcontract.PredictionOutcome{ID: outcome.ID, Text: outcome.Text})
		}
		return prediction.Encode()
	}
	if strings.TrimSpace(req.ContractContent) == "" {
		return nil, fmt.Errorf("missing agent contract content")
	}
	return []byte(req.ContractContent), nil
}

func agentInvokeParam(req *AgentContractInvokeRequest) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("missing agent invoke request")
	}
	if req.Bet != nil {
		return json.Marshal(agentcontract.PredictionBetParam{OutcomeID: req.Bet.OutcomeID})
	}
	if strings.TrimSpace(req.ParamJSON) == "" {
		return nil, nil
	}
	return []byte(req.ParamJSON), nil
}

func (p *Manager) agentGasAssetAmount(baseGas uint64, override uint64, gasAssetName string) (uint64, string, error) {
	if gasAssetName == "" {
		gasAssetName = agentcontract.DefaultGasConfig().GasAssetName
	}
	if override != 0 {
		return override, gasAssetName, nil
	}
	height := p.l2IndexerClient.GetBestHeight()
	if height < 0 {
		height = 0
	}
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

func (p *Manager) templateGasAssetAmount(gasLimit uint64, needsResult bool, override uint64, gasAssetName string) (uint64, string, error) {
	return p.evmGasAssetAmount(gasLimit, needsResult, override, gasAssetName)
}

func normalizeTemplateName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case TEMPLATE_CONTRACT_SWAP, TEMPLATE_CONTRACT_LIMITORDER:
		return TEMPLATE_CONTRACT_LIMITORDER
	case TEMPLATE_CONTRACT_AMM:
		return TEMPLATE_CONTRACT_AMM
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
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
	gasAmount, gasAsset, err := p.evmGasAssetAmount(gasLimit, true, req.GasAssetAmount, req.GasAssetName)
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
	contract, err := deriveCreateContractAddress(p.contractAddressPrefix(), caller, req.DeployNonce)
	if err != nil {
		return nil, err
	}
	return &ContractTxResult{
		ContractType:    ContractTypeEVM,
		ContractAddress: contract.EncodeAddress(),
		Caller:          caller.String(),
		GasAssetName:    gasAsset,
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
	funding, inputs, changeOutputs, prevFetcher, caller, err := p.selectEVMContractFunding(estimate.GasAssetName, estimate.GasAssetAmount, estimate.GasFundAmount, 0)
	if err != nil {
		return nil, err
	}
	tx, contract, err := buildEVMDeployTx(evmDeployTxBuildRequest{
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

func (p *Manager) invokeEVMContract(req *EVMContractInvokeRequest) (*ContractTxResult, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	if req == nil {
		return nil, fmt.Errorf("missing evm invoke request")
	}
	contract, err := contractcommon.DecodeContractAddress(req.ContractAddress)
	if err != nil {
		return nil, err
	}
	gasLimit := req.GasLimit
	if gasLimit == 0 {
		gasLimit = contractcommon.InvokeBaseGas
	}
	gasAmount, gasAsset, err := p.evmGasAssetAmount(gasLimit, !req.SkipResultFee, req.GasAssetAmount, req.GasAssetName)
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
	calldata, err := decodeHexField("calldata", req.CalldataHex)
	if err != nil {
		return nil, err
	}
	funding, inputs, changeOutputs, prevFetcher, caller, err := p.selectEVMContractFunding(gasAsset, gasAmount, gasAmount-gasBaseFee, req.Value)
	if err != nil {
		return nil, err
	}
	tx, err := buildEVMInvokeTx(evmInvokeTxBuildRequest{
		Contract:      contract,
		GasLimit:      gasLimit,
		CallNonce:     req.CallNonce,
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
		ContractAddress: req.ContractAddress,
		Caller:          caller.String(),
		GasAssetName:    gasAsset,
		GasAssetAmount:  gasAmount,
		GasFeeAmount:    gasBaseFee,
		GasFundAmount:   gasAmount - gasBaseFee,
		GasLimit:        gasLimit,
		Nonce:           req.CallNonce,
	}, nil
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
	height := p.l2IndexerClient.GetBestHeight()
	if height < 0 {
		height = 0
	}
	return contractcommon.GasFeeAtHeight(baseGas, uint64(height))
}

func (p *Manager) evmGasAssetAmount(gasLimit uint64, needsResult bool, override uint64, gasAssetName string) (uint64, string, error) {
	if gasAssetName == "" {
		gasAssetName = contractcommon.GasAssetName
	}
	if override != 0 {
		return override, gasAssetName, nil
	}
	height := p.l2IndexerClient.GetBestHeight()
	if height < 0 {
		height = 0
	}
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
	case "", ContractTypeTemplate, "channel", "template:channel", TEMPLATE_CONTRACT_LIMITORDER, TEMPLATE_CONTRACT_SWAP, TEMPLATE_CONTRACT_AMM:
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

type evmDeployTxBuildRequest struct {
	ContractPrefix string
	Caller         contractcommon.EVMAddress
	GasLimit       uint64
	DeployNonce    uint64
	InitCode       []byte
	Funding        swire.TxOut
	Inputs         []swire.OutPoint
	ChangeOutputs  []*swire.TxOut
}

type evmInvokeTxBuildRequest struct {
	Contract      contractcommon.ContractAddress
	GasLimit      uint64
	CallNonce     uint64
	Calldata      []byte
	Funding       swire.TxOut
	Inputs        []swire.OutPoint
	ChangeOutputs []*swire.TxOut
}

func buildEVMDeployTx(req evmDeployTxBuildRequest) (*swire.MsgTx, contractcommon.ContractAddress, error) {
	prefix := req.ContractPrefix
	if prefix == "" {
		prefix = contractcommon.TestnetContractPrefix
	}
	contract, err := deriveCreateContractAddress(prefix, req.Caller, req.DeployNonce)
	if err != nil {
		return nil, contractcommon.ContractAddress{}, err
	}
	if err := validateEVMFundingTxOut(req.Funding); err != nil {
		return nil, contractcommon.ContractAddress{}, err
	}
	scripts, err := contractcommon.DeployNullDataScripts(contractcommon.DeployPayload{GasLimit: req.GasLimit, DeployNonce: req.DeployNonce, InitCode: append([]byte(nil), req.InitCode...)})
	if err != nil {
		return nil, contractcommon.ContractAddress{}, err
	}
	contractOut, err := contractTxOutFromFunding(req.Funding, contract)
	if err != nil {
		return nil, contractcommon.ContractAddress{}, err
	}
	tx := newEVMUnsignedTx(req.Inputs)
	for _, script := range scripts {
		tx.AddTxOut(swire.NewTxOut(0, nil, script))
	}
	tx.AddTxOut(contractOut)
	addEVMChangeOutputs(tx, req.ChangeOutputs)
	return tx, contract, nil
}

func buildEVMInvokeTx(req evmInvokeTxBuildRequest) (*swire.MsgTx, error) {
	if err := validateEVMFundingTxOut(req.Funding); err != nil {
		return nil, err
	}
	scripts, err := contractcommon.InvokeNullDataScripts(contractcommon.InvokePayload{GasLimit: req.GasLimit, CallNonce: req.CallNonce, Calldata: append([]byte(nil), req.Calldata...)})
	if err != nil {
		return nil, err
	}
	contractOut, err := contractTxOutFromFunding(req.Funding, req.Contract)
	if err != nil {
		return nil, err
	}
	tx := newEVMUnsignedTx(req.Inputs)
	for _, script := range scripts {
		tx.AddTxOut(swire.NewTxOut(0, nil, script))
	}
	tx.AddTxOut(contractOut)
	addEVMChangeOutputs(tx, req.ChangeOutputs)
	return tx, nil
}

func validateEVMFundingTxOut(funding swire.TxOut) error {
	return contractcommon.ValidateFundingTxOut(funding, contractcommon.FundingValidation{RequireFunding: true})
}

func contractTxOutFromFunding(funding swire.TxOut, contract contractcommon.ContractAddress) (*swire.TxOut, error) {
	pkScript, err := contractcommon.ContractPkScript(contract)
	if err != nil {
		return nil, err
	}
	return swire.NewTxOut(funding.Value, funding.Assets.Clone(), pkScript), nil
}

func newEVMUnsignedTx(inputs []swire.OutPoint) *swire.MsgTx {
	tx := swire.NewMsgTx(2)
	for i := range inputs {
		tx.AddTxIn(swire.NewTxIn(&inputs[i], nil, nil))
	}
	return tx
}

func addEVMChangeOutputs(tx *swire.MsgTx, outputs []*swire.TxOut) {
	for _, output := range outputs {
		if output == nil {
			continue
		}
		cp := *output
		cp.PkScript = append([]byte(nil), output.PkScript...)
		cp.Assets = output.Assets.Clone()
		tx.AddTxOut(&cp)
	}
}

func evmAddressFromPublicKey(pubKey []byte) (contractcommon.EVMAddress, error) {
	var addr contractcommon.EVMAddress
	if len(pubKey) != 33 && len(pubKey) != 65 {
		return addr, fmt.Errorf("unsupported public key length %d", len(pubKey))
	}
	copy(addr[:], btcutil.Hash160(pubKey))
	return addr, nil
}

func deriveCreateContractAddress(prefix string, caller contractcommon.EVMAddress, nonce uint64) (contractcommon.ContractAddress, error) {
	payload := rlpEncodeCreateAddress(caller, nonce)
	hash := sha3.NewLegacyKeccak256()
	_, _ = hash.Write(payload)
	sum := hash.Sum(nil)
	var addr contractcommon.EVMAddress
	copy(addr[:], sum[len(sum)-20:])
	return contractcommon.NewContractAddress(prefix, contractcommon.AddressVersionV1, contractcommon.ContractTypeEVM, addr)
}

func rlpEncodeCreateAddress(caller contractcommon.EVMAddress, nonce uint64) []byte {
	addr := rlpEncodeBytes(caller[:])
	n := rlpEncodeUint(nonce)
	payload := append(addr, n...)
	return appendRLPListPrefix(payload)
}

func rlpEncodeUint(v uint64) []byte {
	if v == 0 {
		return []byte{0x80}
	}
	var buf [8]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte(v)
		v >>= 8
	}
	return rlpEncodeBytes(buf[i:])
}

func rlpEncodeBytes(b []byte) []byte {
	if len(b) == 1 && b[0] < 0x80 {
		return []byte{b[0]}
	}
	if len(b) <= 55 {
		out := []byte{byte(0x80 + len(b))}
		return append(out, b...)
	}
	lenBytes := encodeLength(len(b))
	out := []byte{byte(0xb7 + len(lenBytes))}
	out = append(out, lenBytes...)
	return append(out, b...)
}

func appendRLPListPrefix(payload []byte) []byte {
	if len(payload) <= 55 {
		out := []byte{byte(0xc0 + len(payload))}
		return append(out, payload...)
	}
	lenBytes := encodeLength(len(payload))
	out := []byte{byte(0xf7 + len(lenBytes))}
	out = append(out, lenBytes...)
	return append(out, payload...)
}

func encodeLength(n int) []byte {
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte(n)
		n >>= 8
	}
	return buf[i:]
}
