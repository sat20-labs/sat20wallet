package wallet

import (
	"fmt"
	"strings"
	"time"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const (
	accountAutopayReadyTimeout      = 2 * time.Minute
	accountAutopayReadyPollInterval = time.Second

	AccountAutopayReasonNotRequired         = "not_required"
	AccountAutopayReasonReady               = "ready"
	AccountAutopayReasonContractInactive    = "contract_inactive"
	AccountAutopayReasonDelegateMissing     = "delegate_missing"
	AccountAutopayReasonDelegateInactive    = "delegate_inactive"
	AccountAutopayReasonPaymentExpired      = "payment_expired"
	AccountAutopayReasonRateInsufficient    = "rate_insufficient"
	AccountAutopayReasonBalanceInsufficient = "balance_insufficient"
	AccountAutopayReasonInvalidState        = "invalid_state"
)

// AccountAutopayFundingStatus is a live, read-only view of the paid account's
// AUTOPAY delegate. It is intentionally separate from account storage mode:
// an expired paid delegate remains paid and must be funded again.
type AccountAutopayFundingStatus struct {
	Required                 bool   `json:"required"`
	Ready                    bool   `json:"ready"`
	NeedsFunding             bool   `json:"needs_funding"`
	CanFund                  bool   `json:"can_fund"`
	Reason                   string `json:"reason"`
	Message                  string `json:"message,omitempty"`
	ContractAddress          string `json:"contract_address,omitempty"`
	FeeAsset                 string `json:"fee_asset,omitempty"`
	Payer                    string `json:"payer,omitempty"`
	CurrentBlock             int64  `json:"current_block,omitempty"`
	LastPayHeight            int64  `json:"last_pay_height,omitempty"`
	AmountPerBlock           string `json:"amount_per_block,omitempty"`
	Balance                  string `json:"balance,omitempty"`
	RequiredAmountPerBlock   string `json:"required_amount_per_block,omitempty"`
	RecommendedFundingAmount string `json:"recommended_funding_amount,omitempty"`
	RecommendedFundingBlocks uint64 `json:"recommended_funding_blocks,omitempty"`
}

func accountAutopayStateFundingStatus(state *dkvsindexer.AutopayContractState,
	defaults dkvsindexer.NetworkDefaults, payer, requiredAmount string) AccountAutopayFundingStatus {

	result := AccountAutopayFundingStatus{
		Required: true, ContractAddress: defaults.AutopayContract,
		FeeAsset: defaults.AutopayFeeAssetName, Payer: strings.TrimSpace(payer),
		RequiredAmountPerBlock:   requiredAmount,
		RecommendedFundingBlocks: accountPaidDefaultFundingBlocks,
	}
	if state == nil {
		result.Reason = AccountAutopayReasonInvalidState
		result.Message = "AUTOPAY 合约状态无效，无法充值。"
		return result
	}
	result.CurrentBlock = state.CurrentBlock
	if state.TemplateName != TEMPLATE_CONTRACT_AUTOPAY || state.Closed ||
		!strings.EqualFold(strings.TrimSpace(state.Status), "active") ||
		!strings.EqualFold(strings.TrimSpace(state.ServiceName), defaults.AutopayServiceName) ||
		!strings.EqualFold(strings.TrimSpace(state.Recipient), defaults.AutopayRecipient) ||
		strings.TrimSpace(state.FeeAssetName) != defaults.AutopayFeeAssetName ||
		state.CurrentBlock <= 0 {
		result.Reason = AccountAutopayReasonContractInactive
		result.Message = "AUTOPAY 合约当前不可用，请检查网络或联系节点维护者。"
		return result
	}

	delegate, ok := state.Delegates[result.Payer]
	if !ok {
		result.NeedsFunding = true
		result.CanFund = true
		result.Reason = AccountAutopayReasonDelegateMissing
		result.Message = "账户尚未配置 AUTOPAY，请充值以恢复付费同步。"
		return result
	}
	result.LastPayHeight = delegate.LastPayHeight
	result.AmountPerBlock = strings.TrimSpace(delegate.AmountPerBlock)
	result.Balance = strings.TrimSpace(delegate.Balance)
	if !strings.EqualFold(strings.TrimSpace(delegate.Status), "active") {
		result.NeedsFunding = true
		result.CanFund = true
		result.Reason = AccountAutopayReasonDelegateInactive
		result.Message = "账户 AUTOPAY 已停止，请充值以恢复付费同步。"
		return result
	}
	if delegate.LastPayHeight < state.CurrentBlock {
		result.NeedsFunding = true
		result.CanFund = true
		result.Reason = AccountAutopayReasonPaymentExpired
		result.Message = "账户 AUTOPAY 已过期，请充值以恢复付费同步。"
		return result
	}
	amount, amountErr := decimalRat(delegate.AmountPerBlock)
	required, requiredErr := decimalRat(requiredAmount)
	if amountErr != nil || requiredErr != nil || amount.Sign() <= 0 || required.Sign() <= 0 {
		result.Reason = AccountAutopayReasonInvalidState
		result.Message = "AUTOPAY 支付参数无效，无法充值。"
		return result
	}
	if amount.Cmp(required) < 0 {
		result.NeedsFunding = true
		result.CanFund = true
		result.Reason = AccountAutopayReasonRateInsufficient
		result.Message = "账户 AUTOPAY 每区块费率不足，请充值以恢复付费同步。"
		return result
	}
	balance, balanceErr := decimalRat(delegate.Balance)
	if balanceErr != nil {
		result.Reason = AccountAutopayReasonInvalidState
		result.Message = "AUTOPAY 余额状态无效，无法充值。"
		return result
	}
	if balance.Cmp(amount) < 0 {
		result.NeedsFunding = true
		result.CanFund = true
		result.Reason = AccountAutopayReasonBalanceInsufficient
		result.Message = "账户 AUTOPAY 余额不足，请充值以继续付费同步。"
		return result
	}
	result.Ready = true
	result.Reason = AccountAutopayReasonReady
	result.Message = "AUTOPAY 支付正常。"
	return result
}

func accountAutopayStateReady(state *dkvsindexer.AutopayContractState, defaults dkvsindexer.NetworkDefaults,
	payer, requiredAmount string) bool {
	return accountAutopayStateFundingStatus(state, defaults, payer, requiredAmount).Ready
}

func (p *Manager) accountAutopayState(defaults dkvsindexer.NetworkDefaults) (*dkvsindexer.AutopayContractState, error) {
	if p == nil || p.l2IndexerClient == nil {
		return nil, fmt.Errorf("SatoshiNet contract indexer is not configured")
	}
	raw, err := p.l2IndexerClient.GetContractStateJSON(defaults.AutopayContract)
	if err != nil {
		return nil, err
	}
	return dkvsindexer.DecodeAutopayContractState([]byte(raw), defaults.AutopayContract)
}

func (p *Manager) accountAutopayReady(defaults dkvsindexer.NetworkDefaults, payer, requiredAmount string) (bool, error) {
	state, err := p.accountAutopayState(defaults)
	if err != nil {
		return false, err
	}
	return accountAutopayStateReady(state, defaults, payer, requiredAmount), nil
}

func (p *Manager) waitForAccountAutopayReady(defaults dkvsindexer.NetworkDefaults, requiredAmount string) error {
	if p == nil || p.wallet == nil || p.wallet.GetPubKey() == nil {
		return fmt.Errorf("wallet is not created/unlocked")
	}
	payer := PublicKeyToP2TRAddress_SatsNet(p.wallet.GetPubKey())
	if strings.TrimSpace(payer) == "" {
		return fmt.Errorf("unable to derive AUTOPAY payer")
	}

	deadline := time.NewTimer(accountAutopayReadyTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(accountAutopayReadyPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		ready, err := p.accountAutopayReady(defaults, payer, requiredAmount)
		if ready {
			return nil
		}
		if err != nil {
			lastErr = err
		}
		select {
		case <-deadline.C:
			if lastErr != nil {
				return fmt.Errorf("AUTOPAY first block payment was not confirmed: %w", lastErr)
			}
			return fmt.Errorf("AUTOPAY first block payment was not confirmed within %s", accountAutopayReadyTimeout)
		case <-ticker.C:
		}
	}
}
