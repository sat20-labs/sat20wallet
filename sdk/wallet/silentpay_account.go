package wallet

import (
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)

// SilentPayEnabled belongs to the existing account catalog, not to a second
// wallet system. The account-management encrypted managed-data bundle already
// supports provider-scoped replication without changing its published codec.
// One byte per enabled scope is sufficient; all disabled accounts are absent.
const silentPayManagedProviderID = "silentpay"

type silentPayAccountManagedDataProvider struct {
	owner *Manager
}

func (*silentPayAccountManagedDataProvider) ID() string { return silentPayManagedProviderID }

func (*silentPayAccountManagedDataProvider) Validate(catalog AccountManagedDataCatalog, payloads []AccountManagedDataPayload) error {
	allowed := accountManagedScopeSet(catalog)
	seen := make(map[string]bool, len(payloads))
	for _, item := range payloads {
		if _, valid := allowed[item.Scope]; !valid || item.Scope == AccountManagedDataGlobalScope ||
			len(item.Payload) != 1 || item.Payload[0] != 1 || seen[item.Scope] {
			return fmt.Errorf("silentpay: invalid managed account scope or opt-in")
		}
		seen[item.Scope] = true
	}
	return nil
}

func (p *silentPayAccountManagedDataProvider) Export(catalog AccountManagedDataCatalog) ([]AccountManagedDataPayload, error) {
	if p == nil || p.owner == nil {
		return nil, fmt.Errorf("silentpay: account provider unavailable")
	}
	p.owner.mutex.RLock()
	defer p.owner.mutex.RUnlock()
	var result []AccountManagedDataPayload
	for _, scope := range catalog.Scopes {
		info := walletInfoByFingerprintInMap(p.owner.walletInfoMap, scope.WalletFingerprint)
		if info == nil || info.Type != WALLET_TYPE_MNEMONIC ||
			int(scope.AccountIndex) >= info.Accounts {
			return nil, fmt.Errorf("silentpay: managed account %s is unavailable", scope.ID())
		}
		if info.SPEnabled[scope.AccountIndex] {
			result = append(result, AccountManagedDataPayload{Scope: scope.ID(), Payload: []byte{1}})
		}
	}
	return result, nil
}

func (p *silentPayAccountManagedDataProvider) Import(catalog AccountManagedDataCatalog, payloads []AccountManagedDataPayload) error {
	if p == nil || p.owner == nil {
		return fmt.Errorf("silentpay: account provider unavailable")
	}
	if err := p.Validate(catalog, payloads); err != nil {
		return err
	}
	p.owner.mutex.Lock()
	defer p.owner.mutex.Unlock()
	type changedWallet struct {
		info *WalletInfo
		candidate WalletInDB
	}
	changed := make(map[int64]changedWallet)
	for _, item := range payloads {
		var target *WalletInfo
		var index uint32
		for _, scope := range catalog.Scopes {
			if scope.ID() == item.Scope {
				target = walletInfoByFingerprintInMap(p.owner.walletInfoMap, scope.WalletFingerprint)
				index = scope.AccountIndex
				break
			}
		}
		if target == nil || target.Type != WALLET_TYPE_MNEMONIC ||
			int(index) >= target.Accounts {
			return fmt.Errorf("silentpay: managed account %s is unavailable", item.Scope)
		}
		value, exists := changed[target.Id]
		if !exists {
			value.info = target
			value.candidate = target.WalletInDB
			value.candidate.SPEnabled = make(map[uint32]bool, len(target.SPEnabled))
			for key, enabled := range target.SPEnabled {
				if enabled { value.candidate.SPEnabled[key] = true }
			}
		}
		// Opt-in is monotonic: an older remote snapshot or absence never
		// disables scanning an SP address that may already have been shared.
		value.candidate.SPEnabled[index] = true
		changed[target.Id] = value
	}
	if len(changed) == 0 { return nil }
	batch := p.owner.db.NewWriteBatch()
	if batch == nil { return fmt.Errorf("silentpay: wallet batch unavailable") }
	defer batch.Close()
	for id, value := range changed {
		encoded, err := EncodeToBytes(&value.candidate)
		if err != nil { return err }
		if err := batch.Put([]byte(getWalletDBKey(id)), encoded); err != nil { return err }
	}
	if err := batch.Flush(); err != nil { return err }
	for _, value := range changed {
		value.info.WalletInDB = value.candidate
	}
	return nil
}

// EnableSilentPay deliberately has no 'disable' counterpart: already shared
// addresses must remain scanned after account switches, updates or recovery.
func (p *Manager) EnableSilentPay(walletID int64, index uint32) (string, error) {
	if p == nil || p.db == nil { return "", ErrSilentPayUnsupported }
	p.channelIdentityMu.Lock()
	defer p.channelIdentityMu.Unlock()
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if err := p.checkAccountManagedDataImport(); err != nil { return "", err }
	info := p.walletInfoMap[walletID]
	if info == nil || info.Type != WALLET_TYPE_MNEMONIC || info.Wallet == nil ||
		int(index) >= info.Accounts {
		return "", ErrSilentPayUnsupported
	}
	internal, ok := info.Wallet.(*InternalWallet)
	if !ok { return "", ErrSilentPayUnsupported }
	address, err := internal.GetSilentPayAddress(index)
	if err != nil { return "", err }
	if info.SPEnabled[index] { return address, nil }
	candidate := cloneWalletInfoForAccountSync(info)
	if candidate.SPEnabled == nil { candidate.SPEnabled = make(map[uint32]bool) }
	candidate.SPEnabled[index] = true
	if err := p.validateWalletCatalogChangeLocked(candidate); err != nil { return "", err }
	err = p.commitWalletCatalogMutationLocked(candidate, nil, accountManagementMutation{
		Type: accountMutationMetadata, Fingerprint: walletFingerprint(info.Wallet),
		WalletID: walletID, Account: index,
		Name: info.AccountNames[index], DID: info.AccountDIDs[index],
	})
	if err != nil { return "", err }
	// commitWalletCatalogMutationLocked durably marks managed-data dirty
	// together with the wallet row when the account profile is configured.
	return address, nil
}

func (p *Manager) SilentPayAccount(walletID int64, index uint32) (bool, string, error) {
	if p == nil { return false, "", ErrSilentPayUnsupported }
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	info := p.walletInfoMap[walletID]
	if info == nil || int(index) >= info.Accounts {
		return false, "", fmt.Errorf("silentpay: unknown account")
	}
	if !info.SPEnabled[index] { return false, "", nil }
	if info.Type != WALLET_TYPE_MNEMONIC || info.Wallet == nil {
		return true, "", ErrSilentPayUnsupported
	}
	wallet, ok := info.Wallet.(*InternalWallet)
	if !ok { return true, "", ErrSilentPayUnsupported }
	addr, err := wallet.GetSilentPayAddress(index)
	return true, addr, err
}

// User-visible SP balances are account-local; the Manager never treats a
// single Root Scan Key as a shortcut across different mnemonic wallets.
func (p *Manager) silentPayKeysForAccount(walletID int64, index uint32) (SilentPayKeys, error) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	info := p.walletInfoMap[walletID]
	if info == nil || !info.SPEnabled[index] || int(index) >= info.Accounts ||
		info.Type != WALLET_TYPE_MNEMONIC || info.Wallet == nil {
		return SilentPayKeys{}, ErrSilentPayUnsupported
	}
	wallet, ok := info.Wallet.(*InternalWallet)
	if !ok { return SilentPayKeys{}, ErrSilentPayUnsupported }
	return wallet.DeriveSilentPayKeys(index)
}

// Compile-time assertion: imports and exports use the existing account
// managed-data mechanism, rather than adding a new backup RPC.
var _ AccountManagedDataProvider = (*silentPayAccountManagedDataProvider)(nil)
var _ common.Wallet = (*InternalWallet)(nil)
