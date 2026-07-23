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
)

func accountAutopayStateReady(state *dkvsindexer.AutopayContractState, defaults dkvsindexer.NetworkDefaults,
	payer, requiredAmount string) bool {

	if state == nil || state.TemplateName != TEMPLATE_CONTRACT_AUTOPAY || state.Closed ||
		strings.EqualFold(strings.TrimSpace(state.Status), "closed") ||
		strings.EqualFold(strings.TrimSpace(state.Status), "expired") ||
		!strings.EqualFold(strings.TrimSpace(state.ServiceName), defaults.AutopayServiceName) ||
		!strings.EqualFold(strings.TrimSpace(state.Recipient), defaults.AutopayRecipient) ||
		strings.TrimSpace(state.FeeAssetName) != defaults.AutopayFeeAssetName ||
		state.CurrentBlock <= 0 {
		return false
	}
	delegate, ok := state.Delegates[strings.TrimSpace(payer)]
	if !ok || delegate.LastPayHeight < state.CurrentBlock {
		return false
	}
	amount, err := decimalRat(delegate.AmountPerBlock)
	if err != nil || amount.Sign() <= 0 {
		return false
	}
	required, err := decimalRat(requiredAmount)
	return err == nil && required.Sign() > 0 && amount.Cmp(required) >= 0
}

func (p *Manager) accountAutopayReady(defaults dkvsindexer.NetworkDefaults, payer, requiredAmount string) (bool, error) {
	if p == nil || p.l2IndexerClient == nil {
		return false, fmt.Errorf("SatoshiNet contract indexer is not configured")
	}
	raw, err := p.l2IndexerClient.GetContractStateJSON(defaults.AutopayContract)
	if err != nil {
		return false, err
	}
	state, err := dkvsindexer.DecodeAutopayContractState([]byte(raw), defaults.AutopayContract)
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
