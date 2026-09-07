package wallet

import (
	"fmt"
	"strings"
	"time"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

const rgb11AccountManagedProviderID = "rgb11"

type rgb11AccountManagedDataProvider struct {
	owner *Manager
}

type rgb11AccountImportStep struct {
	apply    func() error
	rollback func() error
	commit   func()
}

// runRGB11AccountImportSteps provides the transaction boundary that the RGB11
// provider needs across wallet/account scopes. A failing step may already have
// replaced its local database before a derived-cache/lock rebuild fails, so the
// failing step is rolled back as well as every previously applied step.
func runRGB11AccountImportSteps(steps []rgb11AccountImportStep) error {
	for i := range steps {
		if err := steps[i].apply(); err != nil {
			rollbackFailures := make([]string, 0)
			for j := i; j >= 0; j-- {
				if rollbackErr := steps[j].rollback(); rollbackErr != nil {
					rollbackFailures = append(rollbackFailures,
						fmt.Sprintf("scope %d: %v", j, rollbackErr))
				}
			}
			if len(rollbackFailures) != 0 {
				return fmt.Errorf("RGB11 account-managed import failed: %w; rollback failed: %s",
					err, strings.Join(rollbackFailures, "; "))
			}
			return err
		}
	}
	for i := range steps {
		if steps[i].commit != nil {
			steps[i].commit()
		}
	}
	return nil
}

func (p *rgb11AccountManagedDataProvider) ID() string {
	return rgb11AccountManagedProviderID
}

func (p *rgb11AccountManagedDataProvider) accountsByScope() (map[string]localRGB11Account, error) {
	if p == nil || p.owner == nil {
		return nil, fmt.Errorf("RGB11 account-managed provider is unavailable")
	}
	accounts := p.owner.localRGB11Accounts()
	result := make(map[string]localRGB11Account, len(accounts))
	for _, item := range accounts {
		if item.Wallet == nil {
			continue
		}
		scope := AccountManagedDataScope{
			WalletID: item.WalletID, WalletFingerprint: walletFingerprint(item.Wallet),
			AccountIndex: item.AccountIndex, Network: _chain,
		}.ID()
		result[scope] = item
	}
	return result, nil
}

func (p *rgb11AccountManagedDataProvider) Export(catalog AccountManagedDataCatalog) (
	[]AccountManagedDataPayload, error) {

	accounts, err := p.accountsByScope()
	if err != nil {
		return nil, err
	}
	previous, err := p.owner.currentAccountManagedProviderPayloads(rgb11AccountManagedProviderID)
	if err != nil {
		return nil, err
	}
	payloads := make([]AccountManagedDataPayload, 0)
	for _, scope := range catalog.Scopes {
		accountValue, ok := accounts[scope.ID()]
		if !ok {
			return nil, fmt.Errorf("RGB11 scope %s is unavailable", scope.ID())
		}
		manager, err := p.owner.newScopedRGB11Manager(accountValue)
		if err != nil {
			return nil, err
		}
		walletID, err := manager.RGB11WalletID()
		if err != nil {
			return nil, err
		}
		full, _, err := manager.exportRGB11WalletSnapshot(walletID)
		if err != nil {
			return nil, err
		}
		active, err := rgb11wallet.ActiveRecoveryPackageFromSnapshot(full)
		if err != nil {
			return nil, err
		}
		if rgb11wallet.ActiveRecoveryPackageHasTransition(active) {
			if stable, ok := previous[scope.ID()]; ok {
				payloads = append(payloads, stable)
			}
			continue
		}
		recovery, err := rgb11wallet.RecoveryPackageFromSnapshot(full, time.Now().Unix())
		if err != nil {
			return nil, err
		}
		if len(recovery.ProjectionRecords) == 0 && len(recovery.EngineRecords) == 0 {
			continue
		}
		encoded, err := rgb11wallet.EncodeRecoveryPackage(recovery)
		if err != nil {
			return nil, err
		}
		payloads = append(payloads, AccountManagedDataPayload{Scope: scope.ID(), Payload: encoded})
	}
	return payloads, nil
}

// HasActiveTransition reports whether any account scope is between its stable
// recovery snapshot and an irreversible RGB transition. Account-managed PUTs
// are prohibited while this is true: the active package, not a partial stable
// export, is the recovery authority for that interval.
func (p *rgb11AccountManagedDataProvider) HasActiveTransition(
	catalog AccountManagedDataCatalog) (bool, error) {

	accounts, err := p.accountsByScope()
	if err != nil {
		return false, err
	}
	for _, scope := range catalog.Scopes {
		accountValue, ok := accounts[scope.ID()]
		if !ok {
			return false, fmt.Errorf("RGB11 scope %s is unavailable", scope.ID())
		}
		manager, err := p.owner.newScopedRGB11Manager(accountValue)
		if err != nil {
			return false, err
		}
		active, err := manager.hasActiveRGB11Transition()
		if err != nil {
			return false, err
		}
		if active {
			return true, nil
		}
	}
	return false, nil
}

func (p *rgb11AccountManagedDataProvider) ExportActive(catalog AccountManagedDataCatalog) (
	[]AccountManagedDataPayload, error) {

	accounts, err := p.accountsByScope()
	if err != nil {
		return nil, err
	}
	payloads := make([]AccountManagedDataPayload, 0)
	for _, scope := range catalog.Scopes {
		accountValue, ok := accounts[scope.ID()]
		if !ok {
			return nil, fmt.Errorf("RGB11 scope %s is unavailable", scope.ID())
		}
		manager, err := p.owner.newScopedRGB11Manager(accountValue)
		if err != nil {
			return nil, err
		}
		walletID, err := manager.RGB11WalletID()
		if err != nil {
			return nil, err
		}
		full, _, err := manager.exportRGB11WalletSnapshot(walletID)
		if err != nil {
			return nil, err
		}
		active, err := rgb11wallet.ActiveRecoveryPackageFromSnapshot(full)
		if err != nil {
			return nil, err
		}
		if !rgb11wallet.ActiveRecoveryPackageHasTransition(active) {
			continue
		}
		encoded, err := rgb11wallet.EncodeActiveRecoveryPackage(active)
		if err != nil {
			return nil, err
		}
		payloads = append(payloads, AccountManagedDataPayload{Scope: scope.ID(), Payload: encoded})
	}
	return payloads, nil
}

func (p *rgb11AccountManagedDataProvider) Validate(catalog AccountManagedDataCatalog,
	payloads []AccountManagedDataPayload) error {

	accounts, err := p.accountsByScope()
	if err != nil {
		return err
	}
	allowed := accountManagedScopeSet(catalog)
	seen := make(map[string]struct{}, len(payloads))
	for _, payload := range payloads {
		scope := strings.TrimSpace(payload.Scope)
		if scope == AccountManagedDataGlobalScope {
			return fmt.Errorf("RGB11 recovery data requires a wallet/account scope")
		}
		if _, ok := allowed[scope]; !ok {
			return fmt.Errorf("unknown RGB11 recovery scope %q", scope)
		}
		if _, duplicate := seen[scope]; duplicate {
			return fmt.Errorf("duplicate RGB11 recovery scope %q", scope)
		}
		seen[scope] = struct{}{}
		accountValue, ok := accounts[scope]
		if !ok {
			return fmt.Errorf("RGB11 recovery scope %q is unavailable", scope)
		}
		manager, err := p.owner.newScopedRGB11Manager(accountValue)
		if err != nil {
			return err
		}
		packageValue, err := rgb11wallet.DecodeRecoveryPackage(payload.Payload)
		if err != nil {
			return err
		}
		walletID, err := manager.RGB11WalletID()
		if err != nil || packageValue.WalletID != walletID ||
			packageValue.AccountIndex != accountValue.AccountIndex ||
			packageValue.EngineBuildID != rgb11wallet.NativeEngineBuildID {
			return ErrRGB11Inconsistent
		}
	}
	return nil
}

func (p *rgb11AccountManagedDataProvider) ValidateActive(catalog AccountManagedDataCatalog,
	payloads []AccountManagedDataPayload) error {

	accounts, err := p.accountsByScope()
	if err != nil {
		return err
	}
	allowed := accountManagedScopeSet(catalog)
	seen := make(map[string]struct{}, len(payloads))
	for _, payload := range payloads {
		scope := strings.TrimSpace(payload.Scope)
		if scope == AccountManagedDataGlobalScope {
			return fmt.Errorf("RGB11 active recovery requires a wallet/account scope")
		}
		if _, ok := allowed[scope]; !ok {
			return fmt.Errorf("unknown RGB11 active recovery scope %q", scope)
		}
		if _, duplicate := seen[scope]; duplicate {
			return fmt.Errorf("duplicate RGB11 active recovery scope %q", scope)
		}
		seen[scope] = struct{}{}
		accountValue, ok := accounts[scope]
		if !ok {
			return fmt.Errorf("RGB11 active recovery scope %q is unavailable", scope)
		}
		manager, err := p.owner.newScopedRGB11Manager(accountValue)
		if err != nil {
			return err
		}
		packageValue, err := rgb11wallet.DecodeActiveRecoveryPackage(payload.Payload)
		if err != nil {
			return err
		}
		walletID, err := manager.RGB11WalletID()
		if err != nil || packageValue.WalletID != walletID ||
			packageValue.AccountIndex != accountValue.AccountIndex ||
			packageValue.EngineBuildID != rgb11wallet.NativeEngineBuildID {
			return ErrRGB11Inconsistent
		}
	}
	return nil
}

func (p *rgb11AccountManagedDataProvider) Import(catalog AccountManagedDataCatalog,
	payloads []AccountManagedDataPayload) error {

	if err := p.Validate(catalog, payloads); err != nil {
		return err
	}
	accounts, err := p.accountsByScope()
	if err != nil {
		return err
	}
	byScope := make(map[string][]byte, len(payloads))
	for _, payload := range payloads {
		byScope[payload.Scope] = append([]byte(nil), payload.Payload...)
	}

	// Prepare every target and its rollback snapshot before changing any local
	// scope. This keeps both manual recovery and background managed-data sync
	// all-or-nothing without widening the account-management transaction model.
	steps := make([]rgb11AccountImportStep, 0, len(catalog.Scopes))
	for _, scope := range catalog.Scopes {
		accountValue, ok := accounts[scope.ID()]
		if !ok {
			return fmt.Errorf("RGB11 recovery scope %q is unavailable", scope.ID())
		}
		manager, err := p.owner.newScopedRGB11Manager(accountValue)
		if err != nil {
			return err
		}
		walletID, err := manager.RGB11WalletID()
		if err != nil {
			return err
		}
		previous, _, err := manager.exportRGB11WalletSnapshot(walletID)
		if err != nil {
			return err
		}

		// Account management carries only durable ownership. It is authoritative
		// for those records, but it is never a complete replacement for the
		// wallet-local transaction database. A missing payload therefore leaves
		// local lifecycle state intact and lets chain reconciliation decide which
		// durable projections are stale.
		target := &rgb11wallet.RGB11WalletSnapshot{
			Version: rgb11wallet.WalletSnapshotVersion, WalletID: walletID,
			AccountIndex: scope.AccountIndex, EngineBuildID: rgb11wallet.NativeEngineBuildID,
		}
		if encoded := byScope[scope.ID()]; len(encoded) != 0 {
			packageValue, err := rgb11wallet.DecodeRecoveryPackage(encoded)
			if err != nil {
				return err
			}
			target, err = packageValue.WalletSnapshot()
			if err != nil {
				return err
			}
		}

		mgr := manager
		before := previous
		after := target
		steps = append(steps, rgb11AccountImportStep{
			apply: func() error {
				if err := mgr.importRGB11AccountRecoverySnapshot(after); err != nil {
					return err
				}
				return mgr.rebuildRGB11Locks()
			},
			rollback: func() error {
				if err := mgr.importRGB11WalletSnapshot(before); err != nil {
					return err
				}
				if err := mgr.rebuildRGB11Locks(); err != nil {
					return err
				}
				mgr.scheduleRGB11ChainReconciliation()
				return nil
			},
			commit: func() {
				// importRGB11WalletSnapshot restores canonical ticker metadata from
				// the minimum recovery objects before rebuilding derived caches.
				mgr.scheduleRGB11ChainReconciliation()
			},
		})
	}

	return runRGB11AccountImportSteps(steps)
}

func (p *rgb11AccountManagedDataProvider) ImportActive(catalog AccountManagedDataCatalog,
	payloads []AccountManagedDataPayload) error {

	if err := p.ValidateActive(catalog, payloads); err != nil {
		return err
	}
	accounts, err := p.accountsByScope()
	if err != nil {
		return err
	}
	steps := make([]rgb11AccountImportStep, 0, len(payloads))
	for _, payload := range payloads {
		accountValue := accounts[payload.Scope]
		manager, err := p.owner.newScopedRGB11Manager(accountValue)
		if err != nil {
			return err
		}
		walletID, err := manager.RGB11WalletID()
		if err != nil {
			return err
		}
		previous, _, err := manager.exportRGB11WalletSnapshot(walletID)
		if err != nil {
			return err
		}
		packageValue, err := rgb11wallet.DecodeActiveRecoveryPackage(payload.Payload)
		if err != nil {
			return err
		}
		target, err := packageValue.WalletSnapshot()
		if err != nil {
			return err
		}
		mgr := manager
		before := previous
		after := target
		steps = append(steps, rgb11AccountImportStep{
			apply: func() error {
				if err := mgr.importRGB11ActiveRecoverySnapshot(after); err != nil {
					return err
				}
				return mgr.rebuildRGB11Locks()
			},
			rollback: func() error {
				if err := mgr.importRGB11WalletSnapshot(before); err != nil {
					return err
				}
				return mgr.rebuildRGB11Locks()
			},
			commit: func() { mgr.scheduleRGB11ChainReconciliation() },
		})
	}
	return runRGB11AccountImportSteps(steps)
}
