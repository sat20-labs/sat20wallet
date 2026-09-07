package wallet

import (
	"context"
	"errors"
	"fmt"
)

var ErrRGB11ManagedOperationActive = errors.New("an RGB11 recovery operation is already active")

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
// The RGB scope write lock is an application-state gate. The manager data lock
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
		p.channelIdentityMu.Lock()
		p.rgbOperationMu.Lock()
		defer func() {
			p.rgbOperationMu.Unlock()
			p.channelIdentityMu.Unlock()
		}()

		manager, err := selectManager()
		if err != nil {
			return err
		}
		if !p.accountManagedRecoveryConfigured() {
			result, err = operation(manager)
			return err
		}
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
