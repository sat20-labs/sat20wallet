package wallet

import (
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

// Prepare wallet keys and serialization outside the short publication gate.
// A module-local restoring marker, written by the caller first, owns resumption.
func (p *Manager) importAccountWallets(state account.ManagedState) error {
	p.mutex.RLock()
	if p.accountProfile == nil || p.accountProfile.RootFingerprint != state.RootFingerprint {
		p.mutex.RUnlock()
		return ErrAccountManagementSecretConflict
	}
	generation, password := p.accountGeneration, p.accountPassword
	wallets := make(map[int64]*WalletInfo, len(p.walletInfoMap))
	for id, info := range p.walletInfoMap {
		wallets[id] = cloneWalletInfoForAccountSync(info)
	}
	p.mutex.RUnlock()
	puts := make(map[int64][]byte)
	var deletes []int64
	for _, remote := range state.Wallets {
		info := walletInfoByFingerprintInMap(wallets, remote.Fingerprint)
		if remote.Deleted {
			if info != nil {
				if remote.Fingerprint == state.RootFingerprint {
					return fmt.Errorf("the account root cannot be deleted")
				}
				delete(wallets, info.Id)
				deletes = append(deletes, info.Id)
			}
			continue
		}
		if info == nil {
			var err error
			info, err = p.newWalletInfoFromManagedWalletLocked(remote, password)
			if err != nil {
				return err
			}
			if previous := wallets[info.Id]; previous != nil {
				return fmt.Errorf("managed wallet identifier collision")
			}
			wallets[info.Id] = info
		}
		info.Name, info.Accounts = remote.Name, int(remote.AccountCount)
		info.AccountNames, info.AccountDIDs = make(map[uint32]string), make(map[uint32]string)
		for _, sub := range remote.SubAccounts {
			info.AccountNames[sub.Index], info.AccountDIDs[sub.Index] = sub.Name, sub.DID
		}
		encoded, err := EncodeToBytes(&info.WalletInDB)
		if err != nil {
			return err
		}
		puts[info.Id] = encoded
	}
	err := p.withAccountLocalState(false, func() error {
		p.mutex.Lock()
		defer p.mutex.Unlock()
		if p.accountGeneration != generation || p.accountProfile == nil ||
			p.accountProfile.RootFingerprint != state.RootFingerprint {
			return errAccountSnapshotChanged
		}
		status := cloneStatusForAccountRestore(p.status)
		if status == nil {
			return ErrAccountManagementWalletUnavailable
		}
		status.TotalWallet = len(wallets)
		selected := wallets[status.CurrentWallet]
		if selected == nil {
			selected = walletInfoByFingerprintInMap(wallets, state.RootFingerprint)
			if selected == nil {
				return ErrAccountManagementWalletUnavailable
			}
			status.CurrentWallet, status.CurrentAccount = selected.Id, 0
		}
		if status.CurrentAccount >= uint32(selected.Accounts) {
			status.CurrentAccount = 0
		}
		encodedStatus, err := encodeStatusToBytes(status)
		if err != nil {
			return err
		}
		batch, err := NewAtomicWriteBatch(p.db)
		if err != nil {
			return err
		}
		defer batch.Close()
		for id, encoded := range puts {
			if err := batch.Put([]byte(getWalletDBKey(id)), encoded); err != nil {
				return accountDBWriteError(err)
			}
		}
		for _, id := range deletes {
			if err := batch.Delete([]byte(getWalletDBKey(id))); err != nil {
				return accountDBWriteError(err)
			}
		}
		if err := batch.Put([]byte(DB_KEY_STATUS), encodedStatus); err != nil {
			return accountDBWriteError(err)
		}
		if err := batch.Flush(); err != nil {
			return accountDBWriteError(err)
		}
		p.walletInfoMap = wallets
		applyStatusSnapshot(p.status, status)
		p.wallet = selected.Wallet
		p.wallet.SetSubAccount(status.CurrentAccount)
		p.bumpAccountGenerationLocked()
		return nil
	})
	if err != nil {
		return err
	}
	// Rebind the existing selected scope before dependent providers import.
	// No transport is performed and no Manager mutex spans these operations.
	if err := p.rgbManager.selectRGB11Scope(); err != nil {
		return err
	}
	return p.rgbManager.rebuildRGB11Locks()
}
