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
	// The account-managed bundle is authoritative for every catalog scope.
	// Missing payload means that scope has no non-reconstructible RGB state;
	// clear stale local projections/history and rebuild caches from an empty
	// minimum snapshot.
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
		snapshot := &rgb11wallet.RGB11WalletSnapshot{
			Version: rgb11wallet.WalletSnapshotVersion, WalletID: walletID,
			AccountIndex: scope.AccountIndex, EngineBuildID: rgb11wallet.NativeEngineBuildID,
		}
		if encoded := byScope[scope.ID()]; len(encoded) != 0 {
			packageValue, err := rgb11wallet.DecodeRecoveryPackage(encoded)
			if err != nil {
				return err
			}
			snapshot, err = packageValue.WalletSnapshot()
			if err != nil {
				return err
			}
		}
		if err := manager.importRGB11WalletSnapshot(snapshot); err != nil {
			return err
		}
		if err := manager.rebuildRGB11Locks(); err != nil {
			return err
		}
		// importRGB11WalletSnapshot restores the canonical ticker metadata from
		// the minimum recovery objects before rebuilding derived caches.
		manager.scheduleRGB11ChainReconciliation()
	}
	return nil
}
