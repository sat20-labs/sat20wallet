package wallet

import (
	"context"
	"errors"
)

var (
	ErrRGB11ManagedOperationActive = errors.New("an RGB11 recovery operation is already active")
	ErrRGB11OperationScopeChanged  = errors.New("RGB11 operation source changed during account synchronization")
)

type rgb11ManagedOperationMode uint8

const (
	rgb11ManagedOperationNew rgb11ManagedOperationMode = iota
	rgb11ManagedOperationContinue
	// Refresh reconciles local projections from current Bitcoin facts.
	rgb11ManagedOperationRefresh
	// Reconcile is used only for a locally recorded, observed settlement.
	// It cannot prepare, sign or broadcast a new external action.
	rgb11ManagedOperationReconcile
)

func (p *Manager) accountManagedRecoveryConfigured() bool {
	if p == nil {
		return false
	}
	p.mutex.RLock()
	configured := p.accountProfile != nil && p.accountProfile.RecoveryConfigured
	p.mutex.RUnlock()
	return configured
}

// Refresh may receive proofs and change managed balances, so it has the same
// qualification as New and Continue. Only an already observed settlement uses
// the internal reconciliation check.
func (p *rgb11Manager) checkRGB11Account(mode rgb11ManagedOperationMode) error {
	scope, err := p.rgb11AccountManagedScopeID()
	if err != nil {
		return err
	}
	owner := p.accountManagementOwner()
	if mode == rgb11ManagedOperationReconcile {
		return owner.CheckAccountRestore(rgb11AccountManagedProviderID, scope)
	}
	return owner.CheckAccountWrite(rgb11AccountManagedProviderID, scope)
}

// checkRGB11OperationSource checks only local authorization/catalog state. A
// selected-account change does not retarget the captured wallet; deleting or
// replacing that wallet must prevent a new external action with its old keys.
func (p *rgb11Manager) checkRGB11OperationSource() error {
	if p == nil || p.wallet == nil || p.status == nil {
		return ErrRGB11OperationScopeChanged
	}
	owner := p.accountManagementOwner()
	owner.mutex.RLock()
	defer owner.mutex.RUnlock()
	info := owner.walletInfoMap[p.status.CurrentWallet]
	if info == nil || info.Wallet == nil || max(info.Accounts, 1) <= int(p.status.CurrentAccount) ||
		walletFingerprint(info.Wallet) != walletFingerprint(p.wallet) {
		return ErrRGB11OperationScopeChanged
	}
	if err := owner.checkAccountRestore("wallets", AccountManagedDataGlobalScope); err != nil {
		return err
	}
	return nil
}

func (p *rgb11Manager) checkRGB11Write() error {
	if err := p.checkRGB11OperationSource(); err != nil {
		return err
	}
	return p.checkRGB11Account(rgb11ManagedOperationNew)
}

// Public RGB operations use a captured scope and existing local candidate
// publication gates. The background account transport coordinator is separate:
// a slow backup cannot serialize or change the result of a local business commit.
func runRGB11ManagedOperation[T any](p *Manager, ctx context.Context,
	mode rgb11ManagedOperationMode,
	operation func(*rgb11Manager) (T, error)) (T, error) {
	return runRGB11ManagedOperationWithManager(p, ctx, mode,
		func() (*rgb11Manager, error) { return p.captureRGB11NetworkScope(false) }, operation)
}

func runRootRGB11ManagedOperation[T any](p *Manager, ctx context.Context,
	mode rgb11ManagedOperationMode,
	operation func(*rgb11Manager) (T, error)) (T, error) {
	return runRGB11ManagedOperationWithManager(p, ctx, mode,
		func() (*rgb11Manager, error) { return p.captureRGB11NetworkScope(true) }, operation)
}

func runRGB11ManagedOperationWithManager[T any](p *Manager, ctx context.Context,
	mode rgb11ManagedOperationMode, selectManager func() (*rgb11Manager, error),
	operation func(*rgb11Manager) (T, error)) (T, error) {

	var zero T
	if p == nil || selectManager == nil || operation == nil {
		return zero, ErrRGB11Inconsistent
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !p.rgbManagedExecutionMu.TryRLock() {
		return zero, ErrAccountManagedDataImportIncomplete
	}
	defer p.rgbManagedExecutionMu.RUnlock()
	manager, err := selectManager()
	if err != nil {
		return zero, err
	}
	if manager == nil {
		return zero, ErrRGB11Inconsistent
	}
	if err := manager.checkRGB11OperationSource(); err != nil {
		return zero, err
	}
	if err := manager.checkRGB11Account(mode); err != nil {
		return zero, err
	}
	defer manager.publishRGB11ConsistencyStatus()
	return operation(manager)
}
