package wallet

import (
	"fmt"
	"strings"

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
		recovery, err := rgb11wallet.RecoveryPackageFromSnapshot(full, 0)
		if err != nil {
			return nil, err
		}
		encoded, err := rgb11wallet.EncodeRecoveryPackage(recovery)
		if err != nil {
			return nil, err
		}
		payloads = append(payloads, AccountManagedDataPayload{Scope: scope.ID(), Payload: encoded})
	}
	return payloads, nil
}

// HasActiveTransition reports whether any local account scope currently has
// an active RGB transition. It does not make active mailbox data authoritative:
// the versioned stable recovery package contains the complete recovery closure.
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
			!rgb11wallet.CompatibleEngineBuildID(packageValue.EngineBuildID) {
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

	// Prepare every selected target and its rollback snapshot before changing any
	// local scope. An omitted provider scope denotes an empty selected closure;
	// selected import rejects that closure if it would silently drop local
	// recovery responsibilities. A supplied scope must be a complete package.
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

		target := &rgb11wallet.RGB11WalletSnapshot{
			Version: rgb11wallet.WalletSnapshotVersion, WalletID: walletID,
			AccountIndex: scope.AccountIndex, EngineBuildID: rgb11wallet.NativeEngineBuildID,
		}
		if encoded, supplied := byScope[scope.ID()]; supplied {
			packageValue, err := rgb11wallet.DecodeRecoveryPackage(encoded)
			if err != nil {
				return err
			}
			target, err = packageValue.WalletSnapshot()
			if err != nil {
				return err
			}
		} else {
			// The empty selected closure is already installed on a genuinely empty
			// local scope. Avoid constructing/importing a synthetic empty package;
			// any recovery root in the local snapshot still goes through selected
			// merge and is rejected unless incoming evidence safely completes it.
			localClosure, err := rgb11wallet.RecoveryPackageFromSnapshot(previous, 0)
			if err != nil {
				return err
			}
			if len(localClosure.ProjectionRecords) == 0 && len(localClosure.EngineRecords) == 0 {
				continue
			}
		}

		mgr := manager
		before := previous
		after := target
		var applied *RGB11WalletSnapshot
		steps = append(steps, rgb11AccountImportStep{
			apply: func() error {
				var err error
				applied, err = mgr.importRGB11AccountRecoverySnapshotResult(after, true)
				return err
			},
			rollback: func() error {
				if applied == nil {
					return nil
				}
				if err := mgr.restoreRGB11AccountSnapshotIfCurrent(before, applied); err != nil {
					return err
				}
				mgr.scheduleRGB11ChainReconciliation()
				return nil
			},
			commit: func() {
				// The selected snapshot and all derived locks/caches have already
				// committed atomically; schedule only a fresh chain-evidence pass.
				mgr.scheduleRGB11ChainReconciliation()
			},
		})
	}

	return runRGB11AccountImportSteps(steps)
}
