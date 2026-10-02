package wallet

import (
	"fmt"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcutil"
	contract "github.com/sat20-labs/satoshinet/contract"
	framework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
)

func validateContractFundingRequest(value int64, assets []ContractFundingAsset) error {
	if value < 0 || value > btcutil.MaxSatoshi {
		return fmt.Errorf("contract value is outside the allowed satoshi range")
	}
	for _, item := range assets {
		name, amount := strings.TrimSpace(item.AssetName), strings.TrimSpace(item.Amount)
		if name == "" && amount == "" { continue }
		if name == contract.SatoshiAssetName {
			return fmt.Errorf("satoshi (::) funding must use value, not Assets")
		}
		if name == "" || amount == "" || wire.NewAssetNameFromString(name) == nil {
			return fmt.Errorf("contract funding asset and amount must be valid and provided together")
		}
		parsed, err := indexer.NewDecimalFromString(amount, MAX_ASSET_DIVISIBILITY)
		if err != nil { return fmt.Errorf("invalid contract funding asset %s amount: %w", name, err) }
		if parsed.Sign() <= 0 { return fmt.Errorf("contract funding asset %s amount must be positive", name) }
	}
	return nil
}

func validateContractDeployRequest(req *ContractDeployRequest) error {
	if req == nil { return fmt.Errorf("missing contract deploy request") }
	if err := validateContractFundingRequest(req.FundingValue, req.Assets); err != nil { return err }
	if req.GasAssetAmount < 0 { return fmt.Errorf("gas asset amount must be non-negative") }
	gas := req.GasLimit
	if gas == 0 { gas = contract.DeployBaseGas }
	return framework.ValidateDeployGasLimit(gas, framework.GasConfig{})
}

func validateContractInvokeRequest(req *ContractInvokeRequest) error {
	if req == nil { return fmt.Errorf("missing contract invoke request") }
	if err := validateContractFundingRequest(req.Value, req.Assets); err != nil { return err }
	if req.GasAssetAmount < 0 { return fmt.Errorf("gas asset amount must be non-negative") }
	if req.DefaultInvoke {
		if len(contractFundingAssets(req.Assets)) > 1 {
			return fmt.Errorf("default invoke supports only one business asset; use an explicit call for multiple assets")
		}
		// Implicit execution has a protocol-selected gas budget. Do not
		// interpret this request field as a different serialized gas limit.
		if req.GasLimit < 0 { return fmt.Errorf("gas limit must be non-negative") }
		return nil
	}
	gas := req.GasLimit
	if gas == 0 { gas = contract.InvokeBaseGas }
	return framework.ValidateInvokeGasLimit(gas, framework.GasConfig{})
}

// Input metadata is authoritative. Merge must not silently combine one asset
// name with conflicting BindingSat values returned by the indexer.
func mergeContractFundingInput(total, next *TxOutput_SatsNet) error {
	if total == nil || next == nil { return fmt.Errorf("missing contract funding input") }
	if next.OutValue.Value < 0 || total.OutValue.Value < 0 ||
		next.OutValue.Value > btcutil.MaxSatoshi-total.OutValue.Value {
		return fmt.Errorf("contract input value overflows satoshi range")
	}
	for _, item := range next.OutValue.Assets {
		if item.Name == (wire.AssetName{}) { return fmt.Errorf("satoshi must not appear in input TxAssets") }
		if previous, err := total.OutValue.Assets.Find(&item.Name); err == nil && previous.BindingSat != item.BindingSat {
			return fmt.Errorf("conflicting input BindingSat for asset %s", item.Name.String())
		}
	}
	return total.Merge(next)
}

// contractFundingOutputs partitions quantities first, then computes carrier
// requirements for the final aggregated outputs. It never subtracts carrier
// sats once per asset fragment, and never represents plain sats in TxAssets.
// value is business sats; the funding output also carries its bound assets.
func contractFundingOutputs(input wire.TxOut, gasName *wire.AssetName,
	gasAmount, fundingGasAmount, value, plainFee int64, business []contractFundingAsset) (wire.TxOut, *TxOutput_SatsNet, error) {
	fail := func(err error) (wire.TxOut, *TxOutput_SatsNet, error) { return wire.TxOut{}, nil, err }
	if gasAmount < 0 || fundingGasAmount < 0 || fundingGasAmount > gasAmount || plainFee < 0 {
		return fail(fmt.Errorf("invalid contract funding gas or fee amount"))
	}
	requestAssets := make([]ContractFundingAsset, 0, len(business))
	for _, item := range business { requestAssets = append(requestAssets, ContractFundingAsset{AssetName: item.Name, Amount: item.Amount}) }
	if err := validateContractFundingRequest(value, requestAssets); err != nil { return fail(err) }
	if input.Value < 0 || input.Value > btcutil.MaxSatoshi { return fail(fmt.Errorf("invalid contract input value")) }
	var fundingAssets wire.TxAssets
	add := func(name *wire.AssetName, amount *indexer.Decimal) error {
		if name == nil || *name == (wire.AssetName{}) { return fmt.Errorf("invalid contract funding asset name") }
		if amount.Sign() == 0 { return nil }
		asset, err := input.Assets.PickUp(name, amount)
		if err != nil { return err }
		return fundingAssets.Add(asset)
	}
	if fundingGasAmount > 0 {
		if err := add(gasName, indexer.NewDefaultDecimal(fundingGasAmount)); err != nil { return fail(err) }
	}
	for _, item := range business {
		name, text := strings.TrimSpace(item.Name), strings.TrimSpace(item.Amount)
		if name == "" && text == "" { continue }
		amount, err := indexer.NewDecimalFromString(text, MAX_ASSET_DIVISIBILITY)
		if err != nil { return fail(err) }
		if err := add(wire.NewAssetNameFromString(name), amount); err != nil { return fail(err) }
	}
	spentAssets := fundingAssets.Clone()
	if feeAmount := gasAmount - fundingGasAmount; feeAmount > 0 {
		if gasName == nil || *gasName == (wire.AssetName{}) { return fail(fmt.Errorf("invalid gas asset name")) }
		feeAsset, err := input.Assets.PickUp(gasName, indexer.NewDefaultDecimal(feeAmount))
		if err != nil { return fail(err) }
		if err := spentAssets.Add(feeAsset); err != nil { return fail(err) }
	}
	changeAssets := input.Assets.Clone()
	if err := changeAssets.Split(spentAssets); err != nil { return fail(err) }
	carrier, err := contract.RequiredBindingSats(fundingAssets)
	if err != nil { return fail(err) }
	if carrier > btcutil.MaxSatoshi-value { return fail(fmt.Errorf("contract funding value and carrier sats overflow")) }
	fundingValue := value + carrier
	if fundingValue > input.Value || plainFee > input.Value-fundingValue { return fail(fmt.Errorf("insufficient contract funding sats")) }
	changeValue := input.Value - fundingValue - plainFee
	changeCarrier, err := contract.RequiredBindingSats(changeAssets)
	if err != nil { return fail(err) }
	if changeValue < changeCarrier { return fail(fmt.Errorf("insufficient carrier sats for contract change")) }
	change := &TxOutput_SatsNet{OutValue: *wire.NewTxOut(changeValue, changeAssets, nil)}
	return *wire.NewTxOut(fundingValue, fundingAssets, nil), change, nil
}
