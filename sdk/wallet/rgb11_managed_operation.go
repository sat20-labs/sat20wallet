package wallet

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrRGB11ManagedOperationActive = errors.New("an RGB11 recovery operation is already active")
	ErrRGB11OperationScopeChanged  = errors.New("RGB11 operation source changed during account synchronization")
)

type rgb11ManagedOperationMode uint8

const (
	rgb11ManagedOperationNew rgb11ManagedOperationMode = iota
	rgb11ManagedOperationContinue
)

func (p *Manager) beginRGB11ManagedOperationState() {
	p.rgbManagedOperationMu.Lock()
	p.rgbManagedOperationActive = true
	p.rgbManagedOperationDirty = false
	p.rgbManagedOperationMu.Unlock()
}

func (p *Manager) endRGB11ManagedOperationState() bool {
	p.rgbManagedOperationMu.Lock()
	dirty := p.rgbManagedOperationDirty
	p.rgbManagedOperationActive = false
	p.rgbManagedOperationDirty = false
	p.rgbManagedOperationMu.Unlock()
	return dirty
}

// noteRGB11ManagedOperationMutation lets low-level RGB code preserve its
// existing mutation hooks without triggering a PUT for every sub-step. The
// first mutation persists one local dirty-generation marker; the operation
// boundary performs the only stable PUT.
func (p *Manager) noteRGB11ManagedOperationMutation() (bool, bool) {
	if p == nil {
		return false, false
	}
	p.rgbManagedOperationMu.Lock()
	defer p.rgbManagedOperationMu.Unlock()
	if !p.rgbManagedOperationActive {
		return false, false
	}
	first := !p.rgbManagedOperationDirty
	p.rgbManagedOperationDirty = true
	return true, first
}

func (p *Manager) hasAccountManagedRGB11Transition() (bool, error) {
	if p == nil {
		return false, ErrDKVSPathNotSynced
	}
	provider, ok := p.accountManagedActiveDataProvider(rgb11AccountManagedProviderID).(*rgb11AccountManagedDataProvider)
	if !ok || provider == nil {
		return false, nil
	}
	catalog, err := p.accountManagedDataCatalog()
	if err != nil {
		return false, err
	}
	return provider.HasActiveTransition(catalog)
}

func (p *Manager) accountManagedRecoveryConfigured() bool {
	if p == nil {
		return false
	}
	p.mutex.RLock()
	configured := p.accountProfile != nil && p.accountProfile.RecoveryConfigured
	p.mutex.RUnlock()
	return configured
}

// runRGB11ManagedOperation is the application transaction boundary for a
// public RGB operation:
//  1. confirm the stable account baseline before a new operation;
//  2. aggregate all local mutations without intermediate managed PUTs;
//  3. let broadcast/ACK paths synchronously persist active recovery data;
//  4. publish one stable snapshot after the transition becomes stable.
//
// Managed RGB operations are serialized separately from the scope lock. A read
// scope lease freezes wallet/account identity while allowing read-only RGB UI
// calls to inspect durable progress during network waits. The manager data lock
// is never held across the network calls made by account synchronization.
func runRGB11ManagedOperation[T any](p *Manager, ctx context.Context,
	mode rgb11ManagedOperationMode,
	operation func(*rgb11Manager) (T, error)) (T, error) {
	return runRGB11ManagedOperationWithManager(p, ctx, mode, p.synchronizedRGB11Manager, operation)
}

func runRootRGB11ManagedOperation[T any](p *Manager, ctx context.Context,
	mode rgb11ManagedOperationMode,
	operation func(*rgb11Manager) (T, error)) (T, error) {
	return runRGB11ManagedOperationWithManager(p, ctx, mode, p.rootRGB11Manager, operation)
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
	var result T
	var stableConfirmed bool
	err := p.runAccountOperation(ctx, func() error {
		// Serialize managed RGB state machines without taking the exclusive RGB
		// scope lock. The read side still freezes wallet/account scope changes,
		// while read-only RGB UI calls can observe prepared/active progress during
		// network waits instead of stalling behind an unrelated write lock.
		p.rgbManagedExecutionMu.Lock()
		defer p.rgbManagedExecutionMu.Unlock()
		p.channelIdentityMu.RLock()
		p.rgbOperationMu.RLock()
		defer func() {
			p.rgbOperationMu.RUnlock()
			p.channelIdentityMu.RUnlock()
		}()

		manager, err := selectManager()
		if err != nil {
			return err
		}
		if !p.accountManagedRecoveryConfigured() {
			result, err = operation(manager)
			return err
		}
		if manager == nil || manager.wallet == nil {
			return ErrRGB11Inconsistent
		}
		requestedScope := manager.rgb11ScopeKey()
		requestedFingerprint := walletFingerprint(manager.wallet)
		requestedNetwork := _chain
		activeBefore, err := p.hasAccountManagedRGB11Transition()
		if err != nil {
			return err
		}
		if activeBefore && mode == rgb11ManagedOperationNew {
			return ErrRGB11ManagedOperationActive
		}
		if !activeBefore {
			if err := p.syncAccountManagementState(ctx, 0, false); err != nil {
				return fmt.Errorf("confirm RGB11 account-managed baseline: %w", err)
			}
			// Baseline synchronization can apply a remote catalog deletion or an
			// active recovery journal. Neither the captured manager nor the old
			// transition check remains a valid authorization to start business
			// work. Re-select fixed-root callers too, without making account
			// management depend on the UI's selected wallet.
			manager, err = selectManager()
			if err != nil {
				return err
			}
			if manager == nil || manager.wallet == nil || _chain != requestedNetwork ||
				manager.rgb11ScopeKey() != requestedScope ||
				walletFingerprint(manager.wallet) != requestedFingerprint {
				return ErrRGB11OperationScopeChanged
			}
			// A successful own-PUT returns before the ordinary sync path imports
			// active mail. Confirm that recovery layer here as well: a stable PUT
			// must not authorize new work over a remotely pending transition.
			if err := p.importAccountManagedActiveDataForSync(true); err != nil {
				return fmt.Errorf("confirm RGB11 active account recovery: %w", err)
			}
			active, err := p.hasAccountManagedRGB11Transition()
			if err != nil {
				return err
			}
			if active && mode == rgb11ManagedOperationNew {
				return ErrRGB11ManagedOperationActive
			}
		}

		p.beginRGB11ManagedOperationState()
		var operationErr error
		result, operationErr = operation(manager)
		dirty := p.endRGB11ManagedOperationState()
		if !dirty {
			return operationErr
		}
		activeAfter, inspectErr := p.hasAccountManagedRGB11Transition()
		if inspectErr != nil {
			return errors.Join(operationErr, inspectErr)
		}
		if activeAfter {
			// The irreversible transport paths own the synchronous active-data
			// barrier. Keep the local dirty marker until reconciliation reaches
			// a stable state; ordinary account sync must not export this scope.
			return operationErr
		}
		if syncErr := p.syncAccountManagementState(ctx, 0, false); syncErr != nil {
			return errors.Join(operationErr,
				fmt.Errorf("publish stable RGB11 account-managed data: %w", syncErr))
		}
		stableConfirmed = true
		return operationErr
	})
	if stableConfirmed {
		// Pruning is deliberately after the account operation releases its
		// gate. WaitAccountManagedDataReady can now observe the ACK-confirmed
		// generation and cleanup never races the final export.
		p.cleanupAccountManagedActiveDataAfterDurable(rgb11AccountManagedProviderID)
	}
	return result, err
}
