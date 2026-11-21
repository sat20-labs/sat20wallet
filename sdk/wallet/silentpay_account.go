package wallet

import (
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)

// EnableSilentPay deliberately has no 'disable' counterpart: already shared
// addresses must remain scanned after account switches, updates or recovery.
func (p *Manager) EnableSilentPay(walletID int64, index uint32) (string, error) {
	if p == nil || p.db == nil {
		return "", ErrSilentPayUnsupported
	}
	p.channelIdentityMu.Lock()
	defer p.channelIdentityMu.Unlock()
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if err := p.checkAccountRestore("wallets", AccountManagedDataGlobalScope); err != nil {
		return "", err
	}
	info := p.walletInfoMap[walletID]
	if info == nil || info.Type != WALLET_TYPE_MNEMONIC || info.Wallet == nil ||
		int(index) >= info.Accounts {
		return "", ErrSilentPayUnsupported
	}
	internal, ok := info.Wallet.(*InternalWallet)
	if !ok {
		return "", ErrSilentPayUnsupported
	}
	address, err := internal.GetSilentPayAddress(index)
	if err != nil {
		return "", err
	}
	if info.SPEnabled[index] {
		return address, nil
	}
	candidate := cloneWalletInfoForAccountSync(info)
	if candidate.SPEnabled == nil {
		candidate.SPEnabled = make(map[uint32]bool)
	}
	candidate.SPEnabled[index] = true
	if err := p.validateWalletCatalogChangeLocked(candidate); err != nil {
		return "", err
	}
	err = p.commitWalletCatalogMutationLocked(candidate, nil, accountManagementMutation{
		Type: accountMutationMetadata, Fingerprint: walletFingerprint(info.Wallet),
		WalletID: walletID, Account: index,
		Name: info.AccountNames[index], DID: info.AccountDIDs[index],
	})
	if err != nil {
		return "", err
	}
	return address, nil
}

func (p *Manager) SilentPayAccount(walletID int64, index uint32) (bool, string, error) {
	if p == nil {
		return false, "", ErrSilentPayUnsupported
	}
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	info := p.walletInfoMap[walletID]
	if info == nil || int(index) >= info.Accounts {
		return false, "", fmt.Errorf("silentpay: unknown account")
	}
	if !info.SPEnabled[index] {
		return false, "", nil
	}
	if info.Type != WALLET_TYPE_MNEMONIC || info.Wallet == nil {
		return true, "", ErrSilentPayUnsupported
	}
	wallet, ok := info.Wallet.(*InternalWallet)
	if !ok {
		return true, "", ErrSilentPayUnsupported
	}
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
	if !ok {
		return SilentPayKeys{}, ErrSilentPayUnsupported
	}
	return wallet.DeriveSilentPayKeys(index)
}

var _ common.Wallet = (*InternalWallet)(nil)
