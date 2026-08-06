package wallet

import (
	"context"
	"errors"
	"fmt"
	"time"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

const rgb11ChainReconciliationRetryDelay = 10 * time.Minute

func (r *rgb11ScopeStateRegistry) reconciliation(scope string) *rgb11ScopeReconciliation {
	if r == nil || scope == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.reconciliations[scope]
	if item == nil {
		item = &rgb11ScopeReconciliation{}
		r.reconciliations[scope] = item
	}
	return item
}

func (r *rgb11ScopeStateRegistry) startReconciliation(scope string) (*rgb11ScopeReconciliation, bool) {
	item := r.reconciliation(scope)
	if item == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping {
		return item, false
	}
	if item.running {
		select {
		case item.wake <- struct{}{}:
		default:
		}
		return item, false
	}
	item.running = true
	item.stopped = false
	item.stop = make(chan struct{})
	item.wake = make(chan struct{}, 1)
	r.reconciliationWG.Add(1)
	return item, true
}

func (r *rgb11ScopeStateRegistry) finishReconciliation(scope string, item *rgb11ScopeReconciliation) {
	if r == nil || item == nil {
		return
	}
	r.mu.Lock()
	if r.reconciliations[scope] == item {
		item.running = false
		item.stopped = false
		item.stop = nil
		item.wake = nil
	}
	r.mu.Unlock()
	r.reconciliationWG.Done()
}

func (r *rgb11ScopeStateRegistry) wakeReconciliation(scope string) {
	if r == nil || scope == "" {
		return
	}
	r.mu.RLock()
	item := r.reconciliations[scope]
	if item != nil && item.running {
		select {
		case item.wake <- struct{}{}:
		default:
		}
	}
	r.mu.RUnlock()
}

func (r *rgb11ScopeStateRegistry) stopReconciliations() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.stopping = true
	for _, item := range r.reconciliations {
		if item.running && !item.stopped && item.stop != nil {
			close(item.stop)
			item.stopped = true
		}
	}
	r.mu.Unlock()
	r.reconciliationWG.Wait()
}

func (r *rgb11ScopeStateRegistry) reconciliationDelay() time.Duration {
	if r == nil {
		return rgb11ChainReconciliationRetryDelay
	}
	r.mu.RLock()
	delay := r.retryDelay
	r.mu.RUnlock()
	if delay <= 0 {
		return rgb11ChainReconciliationRetryDelay
	}
	return delay
}

func (p *rgb11Manager) setRGB11ReconciliationState(status string) {
	p.updateRGB11ScopeState(func(state *rgb11ScopeBackupState) {
		state.ReconciliationState = status
	})
}

func (p *rgb11Manager) hasPendingRGB11ChainReconciliation() (bool, error) {
	if p == nil || p.projectionStore == nil {
		return false, ErrRGB11Inconsistent
	}
	transfers, err := p.projectionStore.ListTransfers()
	if err != nil {
		return false, err
	}
	for _, state := range transfers {
		if state == nil {
			continue
		}
		if state.Direction == "send" && (state.Status == "broadcast" || state.Status == "pending") {
			return true, nil
		}
		if state.Direction == "receive" &&
			(state.Status == "awaiting_broadcast" || state.Status == "pending") {
			return true, nil
		}
	}
	return false, nil
}

func (p *rgb11Manager) fixedRGB11ScopeAccount() (localRGB11Account, error) {
	if p == nil || p.wallet == nil || p.status == nil {
		return localRGB11Account{}, ErrRGB11WalletLocked
	}
	fixedWallet := p.wallet.Clone()
	if fixedWallet != nil {
		fixedWallet.SetSubAccount(p.status.CurrentAccount)
	}
	if fixedWallet == nil || fixedWallet.GetPubKey() == nil || fixedWallet.GetAddress() == "" {
		if p.wallet.GetSubAccount() != p.status.CurrentAccount ||
			p.wallet.GetPubKey() == nil || p.wallet.GetAddress() == "" {
			return localRGB11Account{}, ErrRGB11WalletLocked
		}
		fixedWallet = p.wallet
	}
	return localRGB11Account{
		WalletID: p.status.CurrentWallet, AccountIndex: p.status.CurrentAccount,
		Address: fixedWallet.GetAddress(), Wallet: fixedWallet,
	}, nil
}

func (p *rgb11Manager) scheduleRGB11ChainReconciliation() {
	if p == nil || p.scopeStates == nil {
		return
	}
	pending, err := p.hasPendingRGB11ChainReconciliation()
	if err != nil {
		p.setRGB11ReconciliationState("error")
		Log.Warningf("inspect RGB11 chain reconciliation failed: %v", err)
		return
	}
	if !pending {
		p.setRGB11ReconciliationState("idle")
		return
	}
	account, err := p.fixedRGB11ScopeAccount()
	if err != nil {
		p.setRGB11ReconciliationState("error")
		Log.Warningf("capture RGB11 reconciliation scope failed: %v", err)
		return
	}
	scope := rgb11StorageScope(account.WalletID, account.AccountIndex)
	worker, started := p.scopeStates.startReconciliation(scope)
	if !started {
		return
	}
	scoped, err := p.newScopedRGB11Manager(account)
	if err != nil {
		p.scopeStates.finishReconciliation(scope, worker)
		p.setRGB11ReconciliationState("error")
		Log.Warningf("create RGB11 reconciliation scope failed: %v", err)
		return
	}
	// Scoped managers must use the same Bitcoin evidence policy as their owner.
	// This keeps standard RGB and SAT20 transport paths on one chain view.
	scoped.evidence = p.evidence
	scoped.setRGB11ReconciliationState("syncing")
	go scoped.runRGB11ChainReconciliation(scope, worker)
}

func (p *rgb11Manager) runRGB11ChainReconciliation(scope string, worker *rgb11ScopeReconciliation) {
	defer p.scopeStates.finishReconciliation(scope, worker)
	for {
		p.scopeStates.mu.Lock()
		worker.attempts++
		p.scopeStates.mu.Unlock()
		_, refreshErr := p.RefreshRGB11State(context.Background())

		pending, inspectErr := p.hasPendingRGB11ChainReconciliation()
		if inspectErr != nil {
			refreshErr = errors.Join(refreshErr, inspectErr)
			pending = true
		}
		if refreshErr != nil {
			p.setRGB11ReconciliationState("error")
			Log.Warningf("RGB11 chain reconciliation wallet=%d account=%d failed: %v",
				p.status.CurrentWallet, p.status.CurrentAccount, refreshErr)
		} else if !pending {
			p.setRGB11ReconciliationState("idle")
			p.notifyRGB11ChainUpdate()
			return
		} else {
			p.setRGB11ReconciliationState("syncing")
		}
		p.notifyRGB11ChainUpdate()
		if errors.Is(refreshErr, rgb11wallet.ErrRGB11Inconsistent) {
			return
		}

		timer := time.NewTimer(p.scopeStates.reconciliationDelay())
		select {
		case <-worker.stop:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-worker.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
	}
}

func (p *rgb11Manager) notifyRGB11ChainUpdate() {
	if p != nil && p.dkvs != nil {
		p.dkvs.notifyCallback()
	}
}

func (p *rgb11Manager) wakeRGB11ChainReconciliation() {
	if p != nil && p.scopeStates != nil {
		p.scopeStates.wakeReconciliation(p.rgb11ScopeKey())
	}
}

func (p *rgb11Manager) lockRGB11ChainRefresh() (func(), error) {
	if p == nil || p.scopeStates == nil {
		return nil, fmt.Errorf("%w: RGB11 scope registry unavailable", ErrRGB11Inconsistent)
	}
	p.scopeStates.chainRefresh.Lock()
	return p.scopeStates.chainRefresh.Unlock, nil
}
