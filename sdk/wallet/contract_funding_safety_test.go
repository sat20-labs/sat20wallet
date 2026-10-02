package wallet

import (
	"math"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcutil"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func safetyAsset(t *testing.T, name, amount string, binding uint32) wire.AssetInfo {
	t.Helper()
	n := wire.NewAssetNameFromString(name)
	require.NotNil(t, n)
	d, err := indexer.NewDecimalFromString(amount, MAX_ASSET_DIVISIBILITY)
	require.NoError(t, err)
	return wire.AssetInfo{Name: *n, Amount: *d, BindingSat: binding}
}

func TestContractFundingSafety(t *testing.T) {
	const gas = "brc20:f:safetygas"
	const bound = "ordx:f:safetybound"
	const other = "runes:f:safetyother"
	gasName := wire.NewAssetNameFromString(gas)
	t.Run("sats_always_use_value_including_zero_and_integer_aliases", func(t *testing.T) {
		for _, amount := range []string{"0", "100", "18446744073709551616", "0.5", "-0.5"} {
			for _, mode := range []string{ContractTypeEVM, ContractTypeTemplate} {
				mgr := &Manager{}
				assets := []ContractFundingAsset{{AssetName: "::", Amount: amount}}
				_, err := mgr.InvokeUnifiedContract(&ContractInvokeRequest{ContractType: mode, Assets: assets})
				require.ErrorContains(t, err, "must use value")
				_, err = mgr.QueryFeeForInvokeUnifiedContract(&ContractInvokeRequest{ContractType: mode, Assets: assets})
				require.ErrorContains(t, err, "must use value")
				_, err = mgr.DeployUnifiedContract(&ContractDeployRequest{ContractType: mode, Assets: assets})
				require.ErrorContains(t, err, "must use value")
				_, err = mgr.EstimateDeployUnifiedContract(&ContractDeployRequest{ContractType: mode, Assets: assets})
				require.ErrorContains(t, err, "must use value")
			}
		}
		require.NoError(t, validateContractFundingRequest(100, nil))
	})
	t.Run("default_multiple_assets_rejected_without_hiding_explicit_support", func(t *testing.T) {
		assets := []ContractFundingAsset{{AssetName: bound, Amount: "1"}, {AssetName: other, Amount: "1.25"}}
		req := &ContractInvokeRequest{DefaultInvoke: true, Assets: assets}
		require.ErrorContains(t, validateContractInvokeRequest(req), "only one")
		req.DefaultInvoke = false
		require.NoError(t, validateContractInvokeRequest(req))
		req.DefaultInvoke = true
		req.Assets[1] = ContractFundingAsset{AssetName: other}
		require.ErrorContains(t, validateContractInvokeRequest(req), "provided together")
	})
	t.Run("gas_floor_and_ceiling_apply_even_with_large_fee_override", func(t *testing.T) {
		for _, invalid := range []int64{-1, contract.InvokeBaseGas-1, contract.MaxGasPerInvoke+1} {
			require.Error(t, validateContractInvokeRequest(&ContractInvokeRequest{GasLimit: invalid, GasAssetAmount: 100000000}))
		}
		for _, valid := range []int64{0, contract.InvokeBaseGas, contract.MaxGasPerInvoke} {
			require.NoError(t, validateContractInvokeRequest(&ContractInvokeRequest{GasLimit: valid}))
		}
		for _, invalid := range []int64{-1, contract.DeployBaseGas-1, contract.MaxGasPerInvoke+1} {
			require.Error(t, validateContractDeployRequest(&ContractDeployRequest{GasLimit: invalid, GasAssetAmount: 100000000}))
		}
	})
	t.Run("metadata_carrier_business_value_change_and_gas_are_conserved", func(t *testing.T) {
		input := *wire.NewTxOut(1000, nil, nil)
		require.NoError(t, input.Assets.Add(ptrSafetyAsset(t, gas, "10000", 0)))
		require.NoError(t, input.Assets.Add(ptrSafetyAsset(t, bound, "100", 10)))
		require.NoError(t, input.Assets.Add(ptrSafetyAsset(t, other, "500", 0)))
		before := input.Assets.Clone()
		funding, change, err := contractFundingOutputs(input, gasName, 200, 150, 100, 0,
			[]contractFundingAsset{{Name: bound, Amount: "15"}})
		require.NoError(t, err)
		require.EqualValues(t, 101, funding.Value)
		require.EqualValues(t, 899, change.OutValue.Value)
		name := wire.NewAssetNameFromString(bound)
		asset, err := funding.Assets.Find(name)
		require.NoError(t, err)
		require.EqualValues(t, 10, asset.BindingSat)
		require.Equal(t, "15", asset.Amount.String())
		remaining, err := change.OutValue.Assets.Find(name)
		require.NoError(t, err)
		require.Equal(t, "85", remaining.Amount.String())
		require.EqualValues(t, 10, remaining.BindingSat)
		reconstructed := funding.Assets.Clone()
		require.NoError(t, reconstructed.Merge(change.OutValue.Assets))
		require.NoError(t, reconstructed.Add(ptrSafetyAsset(t, gas, "50", 0)))
		require.True(t, reconstructed.Equal(input.Assets))
		require.True(t, before.Equal(input.Assets), "assembly mutated caller-owned input assets")
	})
	t.Run("group_rounding_occurs_after_aggregation", func(t *testing.T) {
		input := *wire.NewTxOut(100, wire.TxAssets{safetyAsset(t, bound, "20", 10)}, nil)
		funding, change, err := contractFundingOutputs(input, nil, 0, 0, 0, 1,
			[]contractFundingAsset{{Name: bound, Amount: "6"}, {Name: bound, Amount: "6"}})
		require.NoError(t, err)
		require.EqualValues(t, 1, funding.Value)
		require.EqualValues(t, 98, change.OutValue.Value)
		require.Equal(t, "12", funding.Assets[0].Amount.String())
		require.Equal(t, "8", change.OutValue.Assets[0].Amount.String())
	})
	t.Run("large_bound_quantity_is_divided_before_integer_narrowing", func(t *testing.T) {
		asset := safetyAsset(t, bound, "18446744073709551616", math.MaxUint32)
		carrier, err := contract.RequiredBindingSats(wire.TxAssets{asset})
		require.NoError(t, err)
		require.EqualValues(t, 4294967297, carrier)
	})
	t.Run("insufficient_carrier_and_value_overflow_fail_without_mutation", func(t *testing.T) {
		for _, tc := range []struct{input, value int64}{
			{0, 0}, {btcutil.MaxSatoshi, btcutil.MaxSatoshi},
		} {
			input := *wire.NewTxOut(tc.input, wire.TxAssets{safetyAsset(t, bound, "10", 1)}, nil)
			before := input.Assets.Clone()
			_, change, err := contractFundingOutputs(input, nil, 0, 0, tc.value, 0,
				[]contractFundingAsset{{Name: bound, Amount: "10"}})
			require.Error(t, err)
			require.Nil(t, change)
			require.True(t, before.Equal(input.Assets))
		}
	})
	t.Run("conflicting_input_metadata_is_not_merged", func(t *testing.T) {
		total := &TxOutput_SatsNet{OutValue: *wire.NewTxOut(10, wire.TxAssets{safetyAsset(t, bound, "10", 1)}, nil)}
		next := &TxOutput_SatsNet{OutValue: *wire.NewTxOut(10, wire.TxAssets{safetyAsset(t, bound, "10", 2)}, nil)}
		err := mergeContractFundingInput(total, next)
		require.ErrorContains(t, err, "BindingSat")
		require.EqualValues(t, 10, total.OutValue.Value)
		require.Equal(t, "10", total.OutValue.Assets[0].Amount.String())
	})
}

func ptrSafetyAsset(t *testing.T, name, amount string, binding uint32) *wire.AssetInfo {
	t.Helper()
	a := safetyAsset(t, name, amount, binding)
	return &a
}
