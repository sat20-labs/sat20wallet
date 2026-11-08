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
	// Refresh reconciles local projections from current Bitcoin facts.
	rgb11ManagedOperationRefresh
	// Reconcile is used only for a locally recorded, observed settlement.
	// It cannot prepare, sign or broadcast a new external action.
	rgb11ManagedOperationReconcile
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

// Refresh and explicitly recorded settlements reconcile existing operations.
// Continue also includes delivery/signing/broadcast paths and retains the same
// business qualification as New.
func (p *rgb11Manager) checkRGB11Account(mode rgb11ManagedOperationMode) error {
	scope, err := p.rgb11AccountManagedScopeID()
	if err != nil {
		return err
	}
	owner := p.accountManagementOwner()
	if mode == rgb11ManagedOperationRefresh || mode == rgb11ManagedOperationReconcile {
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
	if err := owner.checkAccountManagedDataImport(); err != nil {
		return err
	}
	return nil
}

func (p *rgb11Manager) syncRGB11OperationRecovery(ctx context.Context) error {
	if err := p.checkRGB11OperationSource(); err != nil {
		return err
	}
	if err := p.checkRGB11Account(rgb11ManagedOperationNew); err != nil {
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
	p.mutex.Unlock()
	if err != nil {
		return err
	}
	if snapshot == nil {
		return ErrDKVSPathNotSynced
	}
	defer zeroBytes(snapshot.secret)
	catalog, err := p.accountManagedDataCatalog()
	if err != nil {
		return err
	}
	if catalog.AccountID != snapshot.profile.AccountID {
		return errAccountSnapshotChanged
	}
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return err
	}
	if walletFingerprint(root) != snapshot.profile.RootFingerprint {
		return ErrRGB11OperationScopeChanged
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
	indexKey, err := account.ManagedIndexKey(snapshot.profile.AccountID)
	if err != nil {
		return err
	}
	indexValue, err := store.GetAuthoritative(indexKey)
	if err != nil {
		return err
	}
	index, err := account.OpenManagedIndex(snapshot.secret, snapshot.profile.AccountID, snapshot.network, indexValue.Value)
	if err != nil {
		return err
	}
	registered := accountEntryMap(index)
	values := make([]*accountLocalKV, 0, len(catalog.Scopes))
	for _, scope := range catalog.Scopes {
		entry, err := accountEntry(catalog.AccountID, rgb11AccountManagedProviderID, scope.ID())
		if err != nil {
			return err
		}
		if selected, ok := registered[entry.Provider+"\x00"+entry.Scope]; !ok || selected != entry {
			return ErrDKVSPathNotSynced
		}
		p.mutex.RLock()
		value, err := p.readAccountLocal(catalog.AccountID, entry.Provider, entry.Scope)
		p.mutex.RUnlock()
		if err != nil {
			return err
		}
		if value == nil || value.Deleted || value.Restoring || value.Conflict || value.LastError != "" ||
			value.Generation == 0 || value.Generation != value.Confirmed || value.Captured != value.Generation ||
			value.Baseline == nil || len(value.Value) == 0 {
			return ErrDKVSPathNotSynced
		}
		payload, err := openAccountPayload(snapshot.secret, catalog.AccountID, snapshot.network, entry, value.Value)
		if err != nil {
			return err
		}
		current, err := p.exportAccountPayload(entry, catalog)
		equal := err == nil && bytes.Equal(payload, current)
		zeroBytes(payload)
		zeroBytes(current)
		if err != nil {
			return err
		}
		if !equal {
			return fmt.Errorf("local RGB11 recovery closure differs from confirmed module data")
		}
		remote, err := store.GetAuthoritative(entry.Key)
		if err != nil {
			return err
		}
		if remote.record == nil || remote.record.Seq < value.Baseline.Seq || !bytes.Equal(remote.Value, value.Value) {
			return ErrDKVSPathNotSynced
		}
		values = append(values, value)
	}
	latestIndex, err := store.GetAuthoritative(indexKey)
	if err != nil {
		return err
	}
	if latestIndex.Hash != indexValue.Hash {
		return errAccountSnapshotChanged
	}
	// Network readback must not confirm a module dirtied or rebound meanwhile.
	p.mutex.RLock()
	current := p.accountSyncSnapshotCurrentLocked(snapshot)
	for _, value := range values {
		live, readErr := p.readAccountLocal(value.AccountID, value.Provider, value.Scope)
		if readErr != nil || live == nil || live.Generation != value.Generation ||
			live.Captured != value.Captured || live.Confirmed != value.Confirmed ||
			live.Restoring || live.Deleted || live.Conflict || live.LastError != "" ||
			!bytes.Equal(live.Value, value.Value) {
			current = false
			break
		}
	}
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
		currentStore.client != store.client || strings.TrimSpace(currentConfig.EndpointID) != coreNodeID {
		return dkvsindexer.ErrEndpointMismatch
	}
	return nil
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
	manager.Manager.beginRGB11ManagedOperationState()
	// The durable StageAccountChange completion already wakes account sync.
	// Keep legacy mutation hooks coalesced without a second boundary wakeup.
	defer manager.Manager.endRGB11ManagedOperationState()
	return operation(manager)
}
