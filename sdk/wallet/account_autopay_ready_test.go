package wallet

import (
	"testing"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func accountAutopayReadyFixture() (dkvsindexer.NetworkDefaults, *dkvsindexer.AutopayContractState) {
	defaults := dkvsindexer.NetworkDefaults{
		AutopayContract:     "autopay",
		AutopayServiceName:  "dkvs",
		AutopayRecipient:    "",
		AutopayFeeAssetName: "brc20:f:sgas",
	}
	state := &dkvsindexer.AutopayContractState{
		Contract:     defaults.AutopayContract,
		TemplateName: TEMPLATE_CONTRACT_AUTOPAY,
		CurrentBlock: 100,
		ServiceName:  defaults.AutopayServiceName,
		Recipient:    defaults.AutopayRecipient,
		FeeAssetName: defaults.AutopayFeeAssetName,
		Status:       "funding",
		Delegates: map[string]dkvsindexer.AutopayDelegateState{
			"payer": {AmountPerBlock: "5", Balance: "0", LastPayHeight: 100, Status: "funding"},
		},
	}
	return defaults, state
}

// A current-block payment is authoritative even if the next block is not funded yet.
func TestAccountAutopayStateReadyAfterCurrentBlockPayment(t *testing.T) {
	defaults, state := accountAutopayReadyFixture()
	require.True(t, accountAutopayStateReady(state, defaults, "payer", "5"))
}

func TestAccountAutopayStateRejectsStaleOrInsufficientDelegate(t *testing.T) {
	defaults, state := accountAutopayReadyFixture()
	delegate := state.Delegates["payer"]
	delegate.LastPayHeight = state.CurrentBlock - 1
	state.Delegates["payer"] = delegate
	require.False(t, accountAutopayStateReady(state, defaults, "payer", "5"))

	delegate.LastPayHeight = state.CurrentBlock
	delegate.AmountPerBlock = "4"
	state.Delegates["payer"] = delegate
	require.False(t, accountAutopayStateReady(state, defaults, "payer", "5"))
}

func TestAccountAutopayStateRejectsWrongContractConfiguration(t *testing.T) {
	defaults, state := accountAutopayReadyFixture()
	state.ServiceName = "other"
	require.False(t, accountAutopayStateReady(state, defaults, "payer", "5"))

	state.ServiceName = defaults.AutopayServiceName
	state.FeeAssetName = "other"
	require.False(t, accountAutopayStateReady(state, defaults, "payer", "5"))

	state.FeeAssetName = defaults.AutopayFeeAssetName
	state.Closed = true
	state.Status = "closed"
	require.False(t, accountAutopayStateReady(state, defaults, "payer", "5"))
}
