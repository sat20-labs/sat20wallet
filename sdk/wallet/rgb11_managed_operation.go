package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

var (
	ErrRGB11ManagedOperationActive = errors.New("an RGB11 recovery operation is already active")
	ErrRGB11OperationScopeChanged  = errors.New("RGB11 operation source changed during account synchronization")
)

type rgb11ManagedOperationMode uint8

const (
	rgb11ManagedOperationNew rgb11ManagedOperationMode = iota
	rgb11ManagedOperationContinue
	// Refresh is the one public mutation mode allowed without an account
	// recovery configuration: it reconciles local projections from current
	// Bitcoin facts so the account can be brought back to a safe state.
	rgb11ManagedOperationRefresh
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
	var provider *rgb11AccountManagedDataProvider
	for _, registered := range p.accountManagedDataProviders() {
		if registered != nil && registered.ID() == rgb11AccountManagedProviderID {
			provider, _ = registered.(*rgb11AccountManagedDataProvider)
			break
		}
	}
	if provider == nil {
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

// syncRGB11RecoveryBarrier confirms the complete current account bundle before
// a caller returns a newly shareable invoice/preparation or performs an
// external ACK, delivery, broadcast, or cancellation. The active mailbox is
// optional cache; only the account state/blob CAS is authoritative.
func (p *Manager) syncRGB11RecoveryBarrier(ctx context.Context) error {
	if p == nil || !p.accountManagedRecoveryConfigured() {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	err := p.syncAccountManagementStateMode(ctx, 0, false, false)
	if err == nil {
		return nil
	}
	// Account sync performs optional registration/mailbox maintenance after a
	// successful CAS. If that trailing cache work failed, verify both that this
	// exact local recovery closure is clean and that the current bound CoreNode
	// still stores the matching state/blob before treating the barrier as met.
	if verifyErr := p.verifyCurrentRGB11RecoveryClosure(); verifyErr == nil {
		return nil
	} else {
		return errors.Join(fmt.Errorf("sync complete RGB11 recovery before external action: %w", err),
			fmt.Errorf("verify account-managed RGB11 recovery CAS: %w", verifyErr))
	}
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
	if err := owner.checkAccountManagedDataImport(); err != nil {
		return err
	}
	return nil
}

func (p *rgb11Manager) syncRGB11OperationRecovery(ctx context.Context) error {
	if err := p.checkRGB11OperationSource(); err != nil {
		return err
	}
	if err := p.accountManagementOwner().syncRGB11RecoveryBarrier(ctx); err != nil {
		return err
	}
	return p.checkRGB11OperationSource()
}

func (p *Manager) verifyCurrentRGB11RecoveryClosure() error {
	if p == nil {
		return ErrDKVSPathNotSynced
	}
	p.mutex.Lock()
	snapshot, _, err := p.captureAccountManagementSyncSnapshotBaseLocked()
	if err == nil && snapshot != nil {
		snapshot.profile.StateEnvelope = append([]byte(nil), snapshot.profile.StateEnvelope...)
		snapshot.profile.ManagedDataEnvelope = append([]byte(nil), snapshot.profile.ManagedDataEnvelope...)
	}
	p.mutex.Unlock()
	if err != nil {
		return err
	}
	if snapshot == nil {
		return ErrDKVSPathNotSynced
	}
	defer zeroBytes(snapshot.secret)
	profile := snapshot.profile
	if profile.ManagedDataDirty || profile.ManagedDataRevision == 0 ||
		len(profile.StateEnvelope) == 0 || len(profile.ManagedDataEnvelope) == 0 || len(snapshot.secret) == 0 {
		return ErrDKVSPathNotSynced
	}
	state, err := account.OpenManagedState(snapshot.secret, profile.AccountID, profile.StateEnvelope)
	if err != nil || state.RootFingerprint != profile.RootFingerprint ||
		state.DataRevision != profile.ManagedDataRevision || state.DataHash != profile.ManagedDataHash {
		return fmt.Errorf("local account state does not confirm RGB11 recovery data")
	}
	catalog, err := p.accountManagedDataCatalog()
	if err != nil {
		return err
	}
	bundle, hash, err := p.exportAccountManagedData(catalog, profile.ManagedDataRevision)
	if err != nil {
		return err
	}
	if bundle.Revision != profile.ManagedDataRevision || hash != profile.ManagedDataHash {
		return fmt.Errorf("local RGB11 recovery closure differs from confirmed account data")
	}
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return err
	}
	if walletFingerprint(root) != profile.RootFingerprint {
		return ErrRGB11OperationScopeChanged
	}
	stateKey, err := p.accountManagedStateKey(root)
	if err != nil {
		return err
	}
	dataKey, err := p.accountManagedDataBlobKey(root)
	if err != nil {
		return err
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return err
	}
	coreNodeID, err := p.currentCoreNodeID()
	if err != nil {
		return err
	}
	config, err := store.client.GetDKVSClientConfig()
	if err != nil {
		return err
	}
	if config == nil || strings.TrimSpace(config.EndpointID) != coreNodeID {
		return dkvsindexer.ErrEndpointMismatch
	}
	if err := p.verifyAccountManagedStorage(store, &profile, stateKey, dataKey,
		profile.StateEnvelope, profile.ManagedDataEnvelope); err != nil {
		return err
	}
	// The storage readback is a network operation. Do not accept it as a
	// confirmation for a profile that was dirtied, rebound, or changed while
	// that read was in flight.
	p.mutex.RLock()
	current := p.accountSyncSnapshotCurrentLocked(snapshot) && p.accountProfile != nil &&
		!p.accountProfile.ManagedDataDirty &&
		p.accountProfile.ManagedDataRevision == profile.ManagedDataRevision &&
		p.accountProfile.ManagedDataHash == profile.ManagedDataHash &&
		bytes.Equal(p.accountProfile.StateEnvelope, profile.StateEnvelope) &&
		bytes.Equal(p.accountProfile.ManagedDataEnvelope, profile.ManagedDataEnvelope)
	p.mutex.RUnlock()
	if !current {
		return errAccountSnapshotChanged
	}
	currentCoreNodeID, err := p.currentCoreNodeID()
	if err != nil {
		return err
	}
	currentStore, err := p.accountDKVSStore()
	if err != nil {
		return err
	}
	currentConfig, err := currentStore.client.GetDKVSClientConfig()
	if err != nil {
		return err
	}
	if currentConfig == nil || currentCoreNodeID != coreNodeID ||
		strings.TrimSpace(currentConfig.EndpointID) != coreNodeID {
		return dkvsindexer.ErrEndpointMismatch
	}
	return nil
}

// runRGB11ManagedOperation is the application transaction boundary for a
// public RGB operation:
//  1. confirm the stable account baseline before a new operation;
//  2. aggregate all local mutations without intermediate managed PUTs;
//  3. let broadcast/ACK paths synchronously persist active recovery data;
//  4. publish one stable snapshot after the transition becomes stable.
//
// The existing account transaction coordinates CAS operations. Each callback
// uses a captured wallet/account view, so UI selection can change during remote
// work. Local publication checks its baseline under the short commit gate;
// account synchronization never inherits an identity or scope mutex.
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
	runOperation := func(manager *rgb11Manager) (result T, operationErr error) {
		defer manager.publishRGB11ConsistencyStatus()
		// This Manager is a call-scoped view, so ownership/dirty bookkeeping cannot
		// be borrowed by another scope while the network request is outstanding.
		manager.Manager.beginRGB11ManagedOperationState()
		defer func() {
			if manager.Manager.endRGB11ManagedOperationState() {
				p.scheduleAccountManagedStateSync()
			}
		}()
		return operation(manager)
	}
	var result T
	var stableConfirmed bool
	err := p.runAccountOperation(ctx, func() error {
		// The existing account operation coordinates account CAS transactions.
		// No identity, scope, execution, or chain-refresh mutex spans its remote
		// phases. Selection is captured briefly by selectManager instead.

		manager, err := selectManager()
		if err != nil {
			return err
		}
		if !p.accountManagedRecoveryConfigured() {
			if mode != rgb11ManagedOperationRefresh {
				return ErrDKVSPathNotSynced
			}
			result, err = runOperation(manager)
			return err
		}
		if manager == nil || manager.wallet == nil {
			return ErrRGB11Inconsistent
		}
		requestedScope := manager.rgb11ScopeKey()
		requestedFingerprint := walletFingerprint(manager.wallet)
		requestedNetwork := _chain
		// Every operation starts from the account's current stable CAS baseline.
		// The provider exports all recovery obligations, including pending ones;
		// a mailbox active marker is only a cache and cannot make the whole
		// account busy or override a newer account bundle.
		if err := p.syncRGB11RecoveryBarrier(ctx); err != nil {
			return fmt.Errorf("confirm RGB11 account-managed baseline: %w", err)
		}
		// Baseline synchronization can apply a remote catalog or stable recovery
		// change. Re-select fixed-root callers too, without making account
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

		var operationErr error
		var dirty bool
		func() {
			defer manager.publishRGB11ConsistencyStatus()
			manager.Manager.beginRGB11ManagedOperationState()
			defer func() { dirty = manager.Manager.endRGB11ManagedOperationState() }()
			result, operationErr = operation(manager)
		}()

		if !dirty {
			return operationErr
		}
		if syncErr := p.syncRGB11RecoveryBarrier(ctx); syncErr != nil {
			return errors.Join(operationErr,
				fmt.Errorf("publish complete RGB11 account-managed recovery: %w", syncErr))
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
	// A new invoice or prepared transfer can be shared by the caller as soon
	// as this function returns. If the operation changed local RGB state but
	// its stable account CAS was not confirmed, keep that local recovery work
	// resumable while withholding the outward-facing result. Continuations
	// intentionally retain their result (notably a broadcast txid) on error.
	if mode == rgb11ManagedOperationNew && err != nil && !stableConfirmed {
		result = zero
	}
	return result, err
}
