package wallet

import (
	"context"
	"errors"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func (p *Manager) accountBackupDone(targets map[string]uint64) (bool, error) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if p.accountProfile == nil || !p.accountProfile.RecoveryConfigured || len(p.accountSecret) != 32 {
		return false, ErrDKVSPathNotSynced
	}
	if err := p.requireAccountManagedWriteLocked(); err != nil {
		if errors.Is(err, ErrDKVSPathNotSynced) || errors.Is(err, ErrRootAccountDiscoveryPending) {
			return false, nil
		}
		return false, err
	}
	indexKey, err := account.ManagedIndexKey(p.accountProfile.AccountID)
	if err != nil {
		return false, err
	}
	indexValue, err := p.readAccountLocal(p.accountProfile.AccountID, "account", indexKey)
	if err != nil {
		return false, err
	}
	if indexValue == nil || len(indexValue.Value) == 0 || indexValue.Baseline == nil {
		return false, nil
	}
	index, err := account.OpenManagedIndex(p.accountSecret, p.accountProfile.AccountID, _chain, indexValue.Baseline.Value)
	if err != nil {
		return false, err
	}
	entries := accountEntryMap(index)
	values, err := p.listAccountLocal(p.accountProfile.AccountID)
	if err != nil {
		return false, err
	}
	seen := make(map[string]bool)
	for _, value := range values {
		id := value.Provider + "\x00" + value.Scope
		target, wanted := targets[id]
		if !wanted {
			continue
		}
		seen[id] = true
		if accountDeleteConfirmed(value, entries) {
			// The directory makes this scope non-restorable. Reclaiming its
			// old blob is housekeeping, not another backup requirement.
			continue
		}
		if value.Restoring {
			return false, ErrAccountManagedDataImportIncomplete
		}
		if value.Conflict {
			return false, dkvsindexer.ErrWriteConflict
		}
		if value.LastError != "" {
			return false, p.accountModuleWriteError(value)
		}
		if value.Confirmed < target {
			return false, nil
		}
		if value.Provider != "account" {
			entry, registered := entries[id]
			if !registered || entry.Deleted != value.Deleted {
				return false, nil
			}
		}
	}
	return len(seen) == len(targets), nil
}

// WaitAccountBackup captures local counters once; later edits do not extend the
// wait. Empty provider/scope means all current important account data.
func (p *Manager) WaitAccountBackup(ctx context.Context, provider, scope string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, bounded := ctx.Deadline(); !bounded {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, accountManagedDataReadyTimeout)
		defer cancel()
	}
	catalog, err := p.accountManagedDataCatalog()
	if err != nil {
		return err
	}
	if err := p.ensureAccountModules(catalog, account.ManagedIndex{Version: account.ManagedIndexVersion}); err != nil {
		return err
	}
	targets, err := p.captureAccountBackup(provider, scope)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	p.requestAccountSync()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		done, err := p.accountBackupDone(targets)
		// A stale authorization is a definite rejection which the existing
		// sender refreshes and re-signs. Let that rebase finish within this
		// wait's original deadline; it is not a content-conflict decision.
		if err != nil && !errors.Is(err, dkvsindexer.ErrStaleGeneration) &&
			!IsDKVSErrorCode(err, dkvsindexer.ErrorCodeStaleGeneration) {
			return err
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (p *Manager) captureAccountBackup(provider, scope string) (map[string]uint64, error) {
	p.mutex.RLock()
	if p.accountProfile == nil || !p.accountProfile.RecoveryConfigured {
		p.mutex.RUnlock()
		return nil, ErrDKVSPathNotSynced
	}
	values, err := p.listAccountLocal(p.accountProfile.AccountID)
	controls, controlErr := p.accountControlKeysLocked()
	p.mutex.RUnlock()
	if err != nil {
		return nil, err
	}
	if controlErr != nil {
		return nil, controlErr
	}
	targets := make(map[string]uint64)
	for _, value := range values {
		if provider != "" && (value.Provider != provider || value.Scope != scope) {
			continue
		}
		if value.Provider == "account" && !controls[value.Key] {
			continue
		}
		targets[value.Provider+"\x00"+value.Scope] = value.Generation
	}
	return targets, nil
}

// Caller owns the Manager read/write gate. Unselected recovery drafts remain
// complete local copies, but are not part of the current recovery chain.
func (p *Manager) accountControlKeysLocked() (map[string]bool, error) {
	if p.accountProfile == nil {
		return nil, ErrDKVSPathNotSynced
	}
	root, err := p.accountManagementRootWalletLocked()
	if err != nil {
		return nil, err
	}
	wrapperKey, err := accountRootWrapperKey(cloneWalletAtAccountZero(root.Wallet))
	if err != nil {
		return nil, err
	}
	indexKey, err := account.ManagedIndexKey(p.accountProfile.AccountID)
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{wrapperKey: true, indexKey: true}
	if p.accountProfile.PackageID != "" {
		packageKey, err := dkvsindexer.AccountPersonalKey(p.accountProfile.AccountID, accountPackagePath(p.accountProfile.PackageID))
		if err != nil {
			return nil, err
		}
		keys[packageKey] = true
	}
	return keys, nil
}

// Explicit backup readiness checks the current module counters and directory,
// rather than a state/blob hash pair or the delivery of an in-memory callback.
func (p *Manager) requireCurrentAccountManagedData() error {
	targets, err := p.captureAccountBackup("", "")
	if err != nil {
		return err
	}
	done, err := p.accountBackupDone(targets)
	if err != nil {
		return err
	}
	if !done {
		return ErrDKVSPathNotSynced
	}
	return nil
}
