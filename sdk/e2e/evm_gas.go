package e2e

import (
	"fmt"
	"math"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
)

type EVMGasPlan struct {
	GasAssetName              string `json:"gasAssetName"`
	Height                    uint64 `json:"height"`
	GasLimit                  int64  `json:"gasLimit"`
	BaseGas                   int64  `json:"baseGas"`
	NeedsResult               bool   `json:"needsResult"`
	BaseFeeGasAsset           int64  `json:"baseFeeGasAsset"`
	ContractFundingGasAsset   int64  `json:"contractFundingGasAsset"`
	RequiredInputGasAsset     int64  `json:"requiredInputGasAsset"`
	InputGasAsset             int64  `json:"inputGasAsset,omitempty"`
	ChangeGasAsset            int64  `json:"changeGasAsset,omitempty"`
	ResultBaseFeeGasAsset     int64  `json:"resultBaseFeeGasAsset,omitempty"`
	ExecutionFundingGasAsset  int64  `json:"executionFundingGasAsset"`
	ExecutionGasUnitsPerAsset int64  `json:"executionGasUnitsPerAsset"`
}

// EVMDeployGasPlan calculates gas asset amounts for an explicit EVM deploy tx.
// DeployBaseGas/GasLimit are execution gas units. BaseFeeGasAsset is left as a
// tx-level fee, while ContractFundingGasAsset is paid to the contract funding
// output for later dynamic execution/result settlement.
func EVMDeployGasPlan(gasLimit int64, height uint64, inputGasAsset int64) (EVMGasPlan, error) {
	return evmGasPlan(contractcommon.DeployBaseGas, gasLimit, height, true, inputGasAsset)
}

// EVMInvokeGasPlan calculates gas asset amounts for an explicit EVM invoke tx.
// Explicit EVM invokes require result fee settlement; pass needsResult=true for
// normal signed calls.
func EVMInvokeGasPlan(gasLimit int64, height uint64, needsResult bool, inputGasAsset int64) (EVMGasPlan, error) {
	return evmGasPlan(contractcommon.InvokeBaseGas, gasLimit, height, needsResult, inputGasAsset)
}

func evmGasPlan(baseGas int64, gasLimit int64, height uint64, needsResult bool, inputGasAsset int64) (EVMGasPlan, error) {
	if gasLimit < baseGas {
		return EVMGasPlan{}, fmt.Errorf("gas limit %d is below base gas %d", gasLimit, baseGas)
	}
	baseFee, err := gasFeeInt64(baseGas, height)
	if err != nil {
		return EVMGasPlan{}, fmt.Errorf("base fee: %w", err)
	}
	executionFunding, err := gasFeeInt64(gasLimit-baseGas, height)
	if err != nil {
		return EVMGasPlan{}, fmt.Errorf("execution funding: %w", err)
	}
	resultBaseFee := int64(0)
	if needsResult {
		resultBaseFee, err = gasFeeInt64(contractcommon.ResultBaseGas, height)
		if err != nil {
			return EVMGasPlan{}, fmt.Errorf("result base fee: %w", err)
		}
	}
	contractFunding, err := addInt64(executionFunding, resultBaseFee)
	if err != nil {
		return EVMGasPlan{}, err
	}
	requiredInput, err := addInt64(baseFee, contractFunding)
	if err != nil {
		return EVMGasPlan{}, err
	}
	change := int64(0)
	if inputGasAsset > 0 {
		if inputGasAsset < requiredInput {
			return EVMGasPlan{}, fmt.Errorf("input gas %d below required %d", inputGasAsset, requiredInput)
		}
		change = inputGasAsset - requiredInput
	}
	return EVMGasPlan{
		GasAssetName:              wallet.GetGasAssetName(),
		Height:                    height,
		GasLimit:                  gasLimit,
		BaseGas:                   baseGas,
		NeedsResult:               needsResult,
		BaseFeeGasAsset:           baseFee,
		ContractFundingGasAsset:   contractFunding,
		RequiredInputGasAsset:     requiredInput,
		InputGasAsset:             inputGasAsset,
		ChangeGasAsset:            change,
		ResultBaseFeeGasAsset:     resultBaseFee,
		ExecutionFundingGasAsset:  executionFunding,
		ExecutionGasUnitsPerAsset: contractcommon.ExecutionGasUnitsPerGas,
	}, nil
}

func gasFeeInt64(gas int64, height uint64) (int64, error) {
	fee, err := contractcommon.GasFeeAtHeight(gas, height)
	if err != nil {
		return 0, err
	}
	return int64(fee), nil
}

func addInt64(left, right int64) (int64, error) {
	if right > 0 && left > math.MaxInt64-right {
		return 0, fmt.Errorf("gas amount overflows int64")
	}
	return left + right, nil
}
