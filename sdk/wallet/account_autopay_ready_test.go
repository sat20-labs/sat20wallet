package wallet

import (
	"encoding/json"
	"fmt"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"strings"
	"testing"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestPendingAccountAutopayLogCannotBeCleared(t *testing.T) {
	logs := NewOperationLogManager(newMemoryKVDB())
	record, err := logs.Create(OperationLogCreate{WalletID: 7, Category: "account",
		Action: "account_autopay_fund", Title: "Fund account AUTOPAY"})
	require.NoError(t, err)
	_, err = logs.Update(record.ID, OperationLogUpdate{Status: OperationLogPending,
		TxID: strings.Repeat("a", 64), Message: "Submitted; waiting for contract confirmation"})
	require.NoError(t, err)
	require.Error(t, logs.DeleteIdentity(7, 0))
	require.Error(t, logs.DeleteAll())
	stored, err := logs.Get(record.ID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.Equal(t, record.ID, stored.ID)
	_, err = logs.Update(record.ID, OperationLogUpdate{Status: OperationLogSucceeded,
		Message: "Contract confirms AUTOPAY is ready"})
	require.NoError(t, err)
	require.NoError(t, logs.DeleteIdentity(7, 0))
}

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
		Status:       "active",
		Delegates: map[string]dkvsindexer.AutopayDelegateState{
			"payer": {AmountPerBlock: "5", Balance: "5", LastPayHeight: 100, Status: "active"},
		},
	}
	return defaults, state
}

func TestAccountAutopayStateReadyMatchesPaidRetentionVerifier(t *testing.T) {
	defaults, state := accountAutopayReadyFixture()
	require.True(t, accountAutopayStateReady(state, defaults, "payer", "5"))
	status := accountAutopayStateFundingStatus(state, defaults, "payer", "5")
	require.True(t, status.Ready)
	require.False(t, status.NeedsFunding)
	require.Equal(t, AccountAutopayReasonReady, status.Reason)
}

func TestAccountAutopayFundingStatusExplainsRechargeReasons(t *testing.T) {
	defaults, state := accountAutopayReadyFixture()
	delegate := state.Delegates["payer"]

	delegate.LastPayHeight = state.CurrentBlock - 1
	state.Delegates["payer"] = delegate
	status := accountAutopayStateFundingStatus(state, defaults, "payer", "5")
	require.False(t, status.Ready)
	require.True(t, status.NeedsFunding)
	require.True(t, status.CanFund)
	require.Equal(t, AccountAutopayReasonPaymentExpired, status.Reason)

	delegate.LastPayHeight = state.CurrentBlock
	delegate.Balance = "4"
	state.Delegates["payer"] = delegate
	status = accountAutopayStateFundingStatus(state, defaults, "payer", "5")
	require.Equal(t, AccountAutopayReasonBalanceInsufficient, status.Reason)
	require.True(t, status.CanFund)

	delete(state.Delegates, "payer")
	status = accountAutopayStateFundingStatus(state, defaults, "payer", "5")
	require.Equal(t, AccountAutopayReasonDelegateMissing, status.Reason)
	require.True(t, status.CanFund)
}

func TestAccountAutopayFundingEffectiveRate(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager := newAccountManagementAutoTestManager(t)
	_, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password")
	require.NoError(t, err)
	require.NoError(t, manager.InitializeAccountManagement("password"))
	manager.accountProfile.StorageMode = AccountStoragePaid
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	payer := PublicKeyToP2TRAddress_SatsNet(manager.GetWallet().GetPubKey())
	state := &dkvsindexer.AutopayContractState{Contract: defaults.AutopayContract, TemplateName: TEMPLATE_CONTRACT_AUTOPAY,
		Status: "active", ServiceName: defaults.AutopayServiceName, Recipient: defaults.AutopayRecipient,
		FeeAssetName: defaults.AutopayFeeAssetName, CurrentBlock: 100,
		Delegates: map[string]dkvsindexer.AutopayDelegateState{}}
	manager.l2IndexerClient = NewIndexerRPCClientMgr()
	manager.l2IndexerClient.SetMaster(NewIndexerClient("http", "receipt-test", "testnet", &autopayReceiptTestHTTP{state: state}))
	for _, tc := range []struct{ rate, effective, amount, reason string }{
		{"5", "10", "10000", AccountAutopayReasonRateInsufficient},
		{"10", "10", "10000", AccountAutopayReasonBalanceInsufficient},
		{"20", "20", "20000", AccountAutopayReasonBalanceInsufficient},
	} {
		t.Run(tc.rate, func(t *testing.T) {
			state.Delegates[payer] = dkvsindexer.AutopayDelegateState{AmountPerBlock: tc.rate, Balance: "0", LastPayHeight: 100, Status: "active"}
			status, err := manager.GetAccountAutopayFundingStatus()
			require.NoError(t, err)
			require.True(t, status.CanFund)
			require.Equal(t, tc.reason, status.Reason)
			encoded, err := json.Marshal(status)
			require.NoError(t, err)
			var fields map[string]any
			require.NoError(t, json.Unmarshal(encoded, &fields))
			require.Equal(t, tc.effective, fields["effective_amount_per_block"])
			require.Equal(t, tc.amount, status.RecommendedFundingAmount)
			for _, change := range []struct {
				name  string
				apply func(*AccountAutopayFundingStatus)
			}{
				{"rate", func(s *AccountAutopayFundingStatus) { s.EffectiveAmountPerBlock = "999" }},
				{"principal", func(s *AccountAutopayFundingStatus) { s.RecommendedFundingAmount = "999" }},
				{"blocks", func(s *AccountAutopayFundingStatus) { s.RecommendedFundingBlocks++ }},
				{"asset", func(s *AccountAutopayFundingStatus) { s.FeeAsset = "other" }},
				{"contract", func(s *AccountAutopayFundingStatus) { s.ContractAddress = "other" }},
				{"payer", func(s *AccountAutopayFundingStatus) { s.Payer = "other" }},
			} {
				t.Run(change.name, func(t *testing.T) {
					confirmed := *status
					change.apply(&confirmed)
					_, err := manager.FundAccountAutopay(confirmed)
					require.ErrorIs(t, err, ErrAccountAutopayFundingQuoteChanged)
					record, err := manager.accountAutopayFundingRecord(defaults, payer)
					require.NoError(t, err)
					require.Nil(t, record, "changed quote entered submission before rejection")
				})
			}
		})
	}
}

func TestAccountAutopayFundingStatusDoesNotOfferFundingForInvalidContract(t *testing.T) {
	defaults, state := accountAutopayReadyFixture()
	state.Closed = true
	state.Status = "closed"
	status := accountAutopayStateFundingStatus(state, defaults, "payer", "5")
	require.False(t, status.Ready)
	require.False(t, status.CanFund)
	require.Equal(t, AccountAutopayReasonContractInactive, status.Reason)
}

func TestAccountAutopayFundingContractAllowsStoppedDelegateRecharge(t *testing.T) {
	defaults, state := accountAutopayReadyFixture()
	state.Status = "funding"
	delegate := state.Delegates["payer"]
	delegate.Status = "closed"
	delegate.Balance = "0"
	state.Delegates["payer"] = delegate
	status := accountAutopayStateFundingStatus(state, defaults, "payer", "5")
	require.True(t, status.CanFund)
	require.False(t, status.Ready)
	require.Equal(t, AccountAutopayReasonDelegateInactive, status.Reason)
	delete(state.Delegates, "payer")
	status = accountAutopayStateFundingStatus(state, defaults, "payer", "5")
	require.True(t, status.CanFund)
	require.False(t, status.Ready)
	// A funded delegate cannot imply that the whole contract has resumed.
	state.Delegates["payer"] = dkvsindexer.AutopayDelegateState{AmountPerBlock: "5", Balance: "5", LastPayHeight: 100, Status: "active"}
	status = accountAutopayStateFundingStatus(state, defaults, "payer", "5")
	require.False(t, status.Ready)
	require.False(t, status.CanFund)
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

	delegate.AmountPerBlock = "5"
	delegate.Balance = "4"
	state.Delegates["payer"] = delegate
	require.False(t, accountAutopayStateReady(state, defaults, "payer", "5"))

	delegate.Balance = "5"
	delegate.Status = "funding"
	state.Delegates["payer"] = delegate
	require.False(t, accountAutopayStateReady(state, defaults, "payer", "5"))

	delegate.Status = "active"
	state.Delegates["payer"] = delegate
	state.Status = "funding"
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

// Intercept only lookup transport: production REST decoding and receipt/log
// transitions run unchanged. Actual submission is covered by the node E2E.
type autopayReceiptTestHTTP struct {
	height    int64
	lookupErr error
	history   []contractcommon.ContractHistoryRecord
	state     *dkvsindexer.AutopayContractState
}

func (h *autopayReceiptTestHTTP) SendGetRequest(url *URL) ([]byte, error) {
	if strings.HasSuffix(url.Path, "/state") {
		return json.Marshal(map[string]any{"code": 0, "data": h.state})
	}
	if strings.Contains(url.Path, "/btc/tx/simpleinfo/") {
		if h.lookupErr != nil {
			return nil, h.lookupErr
		}
		return json.Marshal(map[string]any{"code": 0, "data": map[string]any{"block_height": h.height}})
	}
	return json.Marshal(map[string]any{"code": 0, "total": len(h.history), "data": h.history})
}
func (*autopayReceiptTestHTTP) SendPostRequest(*URL, []byte) ([]byte, error) {
	return nil, fmt.Errorf("lookup must not submit")
}

func TestAccountAutopayReceiptReconciliationRequiresOriginalTransaction(t *testing.T) {
	txid := strings.Repeat("a", 64)
	for _, test := range []struct {
		name      string
		ready     bool
		height    int64
		lookupErr error
		history   []contractcommon.ContractHistoryRecord
		want      OperationLogStatus
		failure   bool
	}{
		{name: "ready_and_confirmed", ready: true, height: 100, want: OperationLogSucceeded},
		{name: "ready_but_original_query_unavailable", ready: true, lookupErr: fmt.Errorf("lookup offline"), want: OperationLogPending},
		{name: "missing_original_is_unknown", ready: true, height: -1, want: OperationLogPending},
		{name: "confirmed_still_waiting_first_payment", height: 100, want: OperationLogPending},
		{name: "historical_success_current_balance_exhausted", height: 100, want: OperationLogSucceeded,
			history: []contractcommon.ContractHistoryRecord{{Contract: "autopay", Height: 101, TxID: strings.Repeat("b", 64), Kind: "result", Status: "success", Details: map[string]interface{}{"result_for_txid": txid}}}},
		{name: "invoke_inclusion_is_not_execution_success", height: 100, want: OperationLogPending,
			history: []contractcommon.ContractHistoryRecord{{Contract: "autopay", Height: 100, TxID: txid, Kind: "invoke", Status: "success"}}},
		{name: "explicit_contract_revert", height: 100, want: OperationLogFailed, failure: true,
			history: []contractcommon.ContractHistoryRecord{{Contract: "autopay", Height: 101, TxID: strings.Repeat("b", 64),
				Kind: "result", Status: "revert", Details: map[string]interface{}{"result_for_txid": txid}}}},
		{name: "another_transaction_failure_does_not_fail_our_payment", height: 100, want: OperationLogPending,
			history: []contractcommon.ContractHistoryRecord{{Contract: "autopay", Height: 101, TxID: strings.Repeat("b", 64), Status: "invalid"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := &Manager{db: newMemoryKVDB(), l2IndexerClient: NewIndexerRPCClientMgr()}
			manager.l2IndexerClient.SetMaster(NewIndexerClient("http", "receipt-test", "testnet", &autopayReceiptTestHTTP{
				height: test.height, lookupErr: test.lookupErr, history: test.history}))
			logs := manager.operationLogManager()
			record, err := logs.Create(OperationLogCreate{Action: "account_autopay_fund", Title: "Fund AUTOPAY",
				Parameters: map[string]string{"contract": "autopay"}})
			require.NoError(t, err)
			record, err = logs.Update(record.ID, OperationLogUpdate{Status: OperationLogPending, TxID: txid})
			require.NoError(t, err)
			waiting, err := manager.reconcileAccountAutopayReceipt(record, test.ready)
			require.Equal(t, test.failure, err != nil)
			require.Equal(t, test.want == OperationLogPending, waiting)
			stored, err := logs.Get(record.ID)
			require.NoError(t, err)
			require.Equal(t, test.want, stored.Status)
			require.Equal(t, txid, stored.TxID)
		})
	}
}

func TestHistoricalSuccessfulAutopayReceiptAllowsRechargeWhenBalanceExhausted(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager := newAccountManagementAutoTestManager(t)
	_, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password")
	require.NoError(t, err)
	require.NoError(t, manager.InitializeAccountManagement("password"))
	manager.accountProfile.StorageMode = AccountStoragePaid
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	payer := PublicKeyToP2TRAddress_SatsNet(manager.GetWallet().GetPubKey())
	rate, err := accountAmountPerBlock(defaults, accountDefaultRecordCount)
	require.NoError(t, err)
	txid := strings.Repeat("a", 64)
	state := &dkvsindexer.AutopayContractState{Contract: defaults.AutopayContract, TemplateName: TEMPLATE_CONTRACT_AUTOPAY,
		Status: "active", ServiceName: defaults.AutopayServiceName, Recipient: defaults.AutopayRecipient, FeeAssetName: defaults.AutopayFeeAssetName, CurrentBlock: 100,
		Delegates: map[string]dkvsindexer.AutopayDelegateState{payer: {AmountPerBlock: rate, Balance: "0", LastPayHeight: 100, Status: "active"}}}
	manager.l2IndexerClient = NewIndexerRPCClientMgr()
	manager.l2IndexerClient.SetMaster(NewIndexerClient("http", "receipt-test", "testnet", &autopayReceiptTestHTTP{height: 90, state: state,
		history: []contractcommon.ContractHistoryRecord{{Contract: defaults.AutopayContract, Height: 91, TxID: strings.Repeat("b", 64), Kind: "result", Status: "success", Details: map[string]interface{}{"result_for_txid": txid}}}}))
	record, err := manager.operationLogManager().Create(OperationLogCreate{Action: "account_autopay_fund", Title: "Fund AUTOPAY",
		Parameters: map[string]string{"network": _chain, "contract": defaults.AutopayContract, "payer": payer, "amount_per_block": rate}})
	require.NoError(t, err)
	_, err = manager.UpdateOperationLog(record.ID, OperationLogUpdate{Status: OperationLogPending, TxID: txid})
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		status, err := manager.GetAccountAutopayFundingStatus()
		require.NoError(t, err)
		require.False(t, status.Ready)
		require.False(t, status.FundingPending)
		require.True(t, status.CanFund)
		require.True(t, status.NeedsFunding)
		require.Equal(t, AccountAutopayReasonBalanceInsufficient, status.Reason)
		require.Equal(t, txid, status.FundingTransactionID)
	}
	stored, err := manager.operationLogManager().Get(record.ID)
	require.NoError(t, err)
	require.Equal(t, OperationLogSucceeded, stored.Status)
}
