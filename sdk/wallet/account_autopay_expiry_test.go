package wallet

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func autopayExpiryFields(t *testing.T, status any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(status)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(encoded, &fields))
	return fields
}

func requireAutopayExpiryNumber(t *testing.T, fields map[string]any, key string, want int64) {
	t.Helper()
	value, ok := fields[key]
	require.True(t, ok, "%s is missing from AUTOPAY status: %s", key, fields)
	got, ok := value.(float64)
	require.True(t, ok, "%s is not a JSON number: %#v", key, value)
	require.Equal(t, float64(want), got, "%s", key)
}

func TestReadyAccountAutopayCanBeFundedForRenewal(t *testing.T) {
	defaults, state := accountAutopayReadyFixture()
	status := accountAutopayStateFundingStatus(state, defaults, "payer", "5")
	require.True(t, status.Ready)
	require.False(t, status.NeedsFunding, "a healthy delegate does not need emergency funding")
	require.True(t, status.CanFund, "the existing payment flow must remain available for an explicit renewal")
}

func TestFundReadyAccountAutopayRejectsChangedPrincipalBeforeCreatingOperation(t *testing.T) {
	manager, _, _ := accountAutopayExpiryManager(t, "active", "25", "10", 100)
	status, err := manager.GetAccountAutopayFundingStatus()
	require.NoError(t, err)
	require.True(t, status.Ready)

	logs := manager.operationLogManager()
	before, err := logs.List()
	require.NoError(t, err)
	require.Empty(t, before)

	confirmed := *status
	confirmed.RecommendedFundingAmount = "tampered-principal"
	_, err = manager.FundAccountAutopay(confirmed)
	require.ErrorIs(t, err, ErrAccountAutopayFundingQuoteChanged)

	after, err := logs.List()
	require.NoError(t, err)
	require.Equal(t, before, after, "a changed renewal quote must be rejected before creating an operation log")
}

func TestAccountAutopayCurrentExpiryUsesExactDecimalBlocks(t *testing.T) {
	for _, tc := range []struct {
		name, balance, rate     string
		current, blocks, expiry int64
	}{
		{name: "whole blocks", balance: "25", rate: "10", current: 100, blocks: 2, expiry: 102},
		{name: "fractional decimal rate", balance: "0.3", rate: "0.1", current: 100, blocks: 3, expiry: 103},
		{name: "fractional remainder is discarded", balance: "29", rate: "10", current: 100, blocks: 2, expiry: 102},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defaults, state := accountAutopayReadyFixture()
			state.CurrentBlock = tc.current
			delegate := state.Delegates["payer"]
			delegate.Balance = tc.balance
			delegate.AmountPerBlock = tc.rate
			delegate.LastPayHeight = tc.current
			state.Delegates["payer"] = delegate

			status := accountAutopayStateFundingStatus(state, defaults, "payer", tc.rate)
			require.True(t, status.Ready)
			require.Equal(t, tc.current, status.LastPayHeight, "actual paid-through height must remain authoritative")
			fields := autopayExpiryFields(t, status)
			requireAutopayExpiryNumber(t, fields, "remaining_payment_blocks", tc.blocks)
			requireAutopayExpiryNumber(t, fields, "estimated_expiry_height", tc.expiry)
			require.Equal(t, float64(tc.current), fields["last_pay_height"], "expiry is a forecast, not proof of payment")
		})
	}
}

func TestAccountAutopayCurrentExpiryIsOmittedWhenStatusIsUntrusted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*dkvsindexer.AutopayContractState)
	}{
		{name: "zero rate", mutate: func(state *dkvsindexer.AutopayContractState) {
			delegate := state.Delegates["payer"]
			delegate.AmountPerBlock = "0"
			state.Delegates["payer"] = delegate
		}},
		{name: "negative rate", mutate: func(state *dkvsindexer.AutopayContractState) {
			delegate := state.Delegates["payer"]
			delegate.AmountPerBlock = "-1"
			state.Delegates["payer"] = delegate
		}},
		{name: "invalid balance", mutate: func(state *dkvsindexer.AutopayContractState) {
			delegate := state.Delegates["payer"]
			delegate.Balance = "not-a-decimal"
			state.Delegates["payer"] = delegate
		}},
		{name: "negative balance", mutate: func(state *dkvsindexer.AutopayContractState) {
			delegate := state.Delegates["payer"]
			delegate.Balance = "-1"
			state.Delegates["payer"] = delegate
		}},
		{name: "paid-through height is stale", mutate: func(state *dkvsindexer.AutopayContractState) {
			delegate := state.Delegates["payer"]
			delegate.LastPayHeight--
			state.Delegates["payer"] = delegate
		}},
		{name: "contract is paused for funding", mutate: func(state *dkvsindexer.AutopayContractState) {
			state.Status = "funding"
		}},
		{name: "expiry height overflows int64", mutate: func(state *dkvsindexer.AutopayContractState) {
			delegate := state.Delegates["payer"]
			delegate.AmountPerBlock = "1"
			delegate.Balance = "9223372036854775807"
			state.Delegates["payer"] = delegate
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defaults, state := accountAutopayReadyFixture()
			tc.mutate(state)
			status := accountAutopayStateFundingStatus(state, defaults, "payer", "5")
			fields := autopayExpiryFields(t, status)
			require.NotContains(t, fields, "estimated_expiry_height", "untrusted state must not publish a current expiry: %s", fields)
		})
	}
}

func accountAutopayExpiryManager(t *testing.T, stateStatus, balance, rate string, lastPaid int64) (*Manager, dkvsindexer.NetworkDefaults, string) {
	manager, defaults, payer, _ := accountAutopayExpiryManagerAt(t, stateStatus, balance, rate,
		100, lastPaid, 0, nil)
	return manager, defaults, payer
}

func accountAutopayExpiryManagerAt(t *testing.T, stateStatus, balance, rate string,
	currentBlock, lastPaid int64, bestHeight int, bestHeightErr error) (*Manager, dkvsindexer.NetworkDefaults, string, *autopayReceiptTestHTTP) {
	t.Helper()
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })

	manager := newAccountManagementAutoTestManager(t)
	_, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password")
	require.NoError(t, err)
	require.NoError(t, manager.InitializeAccountManagement("password"))
	manager.accountProfile.StorageMode = AccountStoragePaid
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	payer := PublicKeyToP2TRAddress_SatsNet(manager.GetWallet().GetPubKey())
	state := &dkvsindexer.AutopayContractState{
		Contract: defaults.AutopayContract, TemplateName: TEMPLATE_CONTRACT_AUTOPAY,
		CurrentBlock: currentBlock, ServiceName: defaults.AutopayServiceName, Recipient: defaults.AutopayRecipient,
		FeeAssetName: defaults.AutopayFeeAssetName, Status: stateStatus,
		Delegates: map[string]dkvsindexer.AutopayDelegateState{
			payer: {AmountPerBlock: rate, Balance: balance, LastPayHeight: lastPaid, Status: "active"},
		},
	}
	manager.l2IndexerClient = NewIndexerRPCClientMgr()
	http := &autopayReceiptTestHTTP{state: state, bestHeight: bestHeight, bestHeightErr: bestHeightErr}
	manager.l2IndexerClient.SetMaster(NewIndexerClient("http", "receipt-test", "testnet", http))
	return manager, defaults, payer, http
}

func TestGetAccountAutopayFundingStatusForecastsCurrentAndRecommendedExpiry(t *testing.T) {
	manager, _, _ := accountAutopayExpiryManager(t, "active", "25", "10", 100)
	status, err := manager.GetAccountAutopayFundingStatus()
	require.NoError(t, err)
	require.True(t, status.Ready)
	fields := autopayExpiryFields(t, status)
	requireAutopayExpiryNumber(t, fields, "last_pay_height", 100)
	requireAutopayExpiryNumber(t, fields, "remaining_payment_blocks", 2)
	requireAutopayExpiryNumber(t, fields, "estimated_expiry_height", 102)
	requireAutopayExpiryNumber(t, fields, "estimated_funded_expiry_height", 1102)
}

func TestGetAccountAutopayFundingStatusOmitsExpiryForUntrustedStates(t *testing.T) {
	t.Run("pending payment", func(t *testing.T) {
		manager, defaults, payer := accountAutopayExpiryManager(t, "active", "25", "10", 100)
		record, err := manager.BeginOperationLog(OperationLogCreate{
			Category: "account", Action: "account_autopay_fund", Title: "充值 AUTOPAY",
			Parameters: map[string]string{"network": _chain, "payer": payer, "contract": defaults.AutopayContract,
				"asset": defaults.AutopayFeeAssetName, "amount_per_block": "10", "amount": "10000"},
		})
		require.NoError(t, err)
		_, err = manager.UpdateOperationLog(record.ID, OperationLogUpdate{Status: OperationLogPending, TxID: strings.Repeat("a", 64)})
		require.NoError(t, err)

		status, err := manager.GetAccountAutopayFundingStatus()
		require.NoError(t, err)
		require.True(t, status.FundingPending)
		fields := autopayExpiryFields(t, status)
		require.NotContains(t, fields, "estimated_expiry_height")
		require.NotContains(t, fields, "estimated_funded_expiry_height")
	})

	t.Run("contract paused", func(t *testing.T) {
		manager, _, _ := accountAutopayExpiryManager(t, "funding", "25", "10", 100)
		status, err := manager.GetAccountAutopayFundingStatus()
		require.NoError(t, err)
		require.False(t, status.Ready)
		fields := autopayExpiryFields(t, status)
		require.NotContains(t, fields, "estimated_expiry_height")
		require.NotContains(t, fields, "estimated_funded_expiry_height")
	})

	t.Run("last paid height has not caught up", func(t *testing.T) {
		manager, _, _ := accountAutopayExpiryManager(t, "active", "25", "10", 99)
		status, err := manager.GetAccountAutopayFundingStatus()
		require.NoError(t, err)
		require.False(t, status.Ready)
		fields := autopayExpiryFields(t, status)
		require.NotContains(t, fields, "estimated_expiry_height")
		require.NotContains(t, fields, "estimated_funded_expiry_height")
	})

	t.Run("payment is already expired", func(t *testing.T) {
		manager, _, _ := accountAutopayExpiryManager(t, "payment_expired", "25", "10", 100)
		status, err := manager.GetAccountAutopayFundingStatus()
		require.NoError(t, err)
		fields := autopayExpiryFields(t, status)
		require.NotContains(t, fields, "estimated_expiry_height")
		require.NotContains(t, fields, "estimated_funded_expiry_height")
	})
}

func TestGetAccountAutopayFundingStatusUsesIndexedTipForExpiry(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		rawCurrent, lastPaid, tip int64
		wantCurrent, wantExpiry   int64
		wantReady                 bool
	}{
		{name: "tip catches up after payment", rawCurrent: 42, lastPaid: 45, tip: 45, wantCurrent: 45, wantExpiry: 1009, wantReady: true},
		{name: "tip passes paid-through height", rawCurrent: 42, lastPaid: 45, tip: 46, wantCurrent: 46, wantReady: false},
		{name: "tip does not lower raw contract height", rawCurrent: 47, lastPaid: 46, tip: 46, wantCurrent: 47, wantReady: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager, _, _, _ := accountAutopayExpiryManagerAt(t, "active", "9640", "10",
				tc.rawCurrent, tc.lastPaid, int(tc.tip), nil)
			status, err := manager.GetAccountAutopayFundingStatus()
			require.NoError(t, err)
			require.Equal(t, tc.wantCurrent, status.CurrentBlock)
			require.Equal(t, tc.wantReady, status.Ready)
			require.Equal(t, tc.lastPaid, status.LastPayHeight, "tip height must not rewrite actual paid-through height")
			fields := autopayExpiryFields(t, status)
			if tc.wantExpiry > 0 {
				requireAutopayExpiryNumber(t, fields, "estimated_expiry_height", tc.wantExpiry)
				requireAutopayExpiryNumber(t, fields, "remaining_payment_blocks", 964)
			} else {
				require.NotContains(t, fields, "estimated_expiry_height", "stale or untrusted height must not publish an expiry: %s", fields)
			}
		})
	}
}

func TestAccountAutopayTipFailurePreventsReadinessAndFunding(t *testing.T) {
	manager, defaults, payer, http := accountAutopayExpiryManagerAt(t, "active", "9640", "10",
		42, 45, 0, errors.New("bestheight unavailable"))
	status, statusErr := manager.GetAccountAutopayFundingStatus()
	assert.ErrorContains(t, statusErr, "height")
	assert.Nil(t, status)
	if status != nil {
		assert.False(t, status.Ready, "a failed height query cannot make AUTOPAY ready")
	}

	_, fundErr := manager.FundAccountAutopay(AccountAutopayFundingStatus{
		Payer: payer, ContractAddress: defaults.AutopayContract, FeeAsset: defaults.AutopayFeeAssetName,
		EffectiveAmountPerBlock: "10", RecommendedFundingAmount: "10000",
		RecommendedFundingBlocks: accountPaidDefaultFundingBlocks,
	})
	assert.ErrorContains(t, fundErr, "height", "funding must stop on the unavailable tip")
	logs, err := manager.operationLogManager().List()
	require.NoError(t, err)
	assert.Empty(t, logs, "a failed tip query must not create a funding operation")
	assert.Zero(t, http.postCalls.Load(), "a failed tip query must not submit a payment")
}
