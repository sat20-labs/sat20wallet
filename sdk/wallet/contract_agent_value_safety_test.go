package wallet

import (
	"testing"

	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/stretchr/testify/require"
)

// Native sats are value, never an asset entry. This exercises the public quote
// path shared with transaction construction, rather than a source-text assertion.
func TestContractAgentValueFundingSafety(t *testing.T) {
	manager := &Manager{}
	base := ContractInvokeRequest{
		ContractType: ContractTypeAgent,
		Action:       contract.AgentInvokeAPIBet,
		Param:        `{"outcome_id":"a"}`,
		ParamEncoding: "json",
	}
	want, _, err := manager.agentInvokeGasAssetAmount(true, 0)
	require.NoError(t, err)
	for _, value := range []int64{1, 1000, 1000000} {
		t.Run("native_value_"+safetyValueName(value), func(t *testing.T) {
			req := base
			req.Value = value
			fee, err := manager.QueryFeeForInvokeUnifiedContract(&req)
			require.NoError(t, err, "native bet must use Value without a fictitious :: asset")
			require.Equal(t, want, fee, "business value must not be charged as a gas fee")
			require.Empty(t, req.Assets, "quote must not rewrite sats into Assets")
			require.Equal(t, value, req.Value)
		})
	}
	t.Run("fractional_token_is_not_narrowed_to_sats", func(t *testing.T) {
		req := base
		req.Assets = []ContractFundingAsset{{AssetName: "runes:f:testfraction", Amount: "1.25"}}
		fee, err := manager.QueryFeeForInvokeUnifiedContract(&req)
		require.NoError(t, err)
		require.Equal(t, want, fee)
		require.Equal(t, "1.25", req.Assets[0].Amount)
	})
	t.Run("missing_negative_and_reserved_alias_funding_rejected", func(t *testing.T) {
		req := base
		_, err := manager.QueryFeeForInvokeUnifiedContract(&req)
		require.Error(t, err)
		req.Value = -1
		_, err = manager.QueryFeeForInvokeUnifiedContract(&req)
		require.Error(t, err)
		req.Value = 0
		req.Assets = []ContractFundingAsset{{AssetName: "::", Amount: "1000"}}
		_, err = manager.QueryFeeForInvokeUnifiedContract(&req)
		require.ErrorContains(t, err, "must use value")
	})
}

func safetyValueName(value int64) string {
	switch value {
	case 1:
		return "one"
	case 1000:
		return "thousand"
	default:
		return "million"
	}
}
