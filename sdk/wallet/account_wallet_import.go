package wallet

import (
	"fmt"
	"slices"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
)

// A shared SilentPay address remains enabled when an older wallet snapshot
// omits its opt-in. Keep the subaccount rows needed to scan that address.
func (p *Manager) keepAccountSilentPay(accountID string, payload []byte) ([]byte, []byte, error) {
	state, err := account.DecodeManagedState(payload)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		for _, wallet := range state.Wallets {
			zeroBytes(wallet.Secret)
		}
	}()
	changed := false
	p.mutex.RLock()
	if p.accountProfile == nil || p.accountProfile.AccountID != accountID || len(p.accountSecret) != 32 {
		p.mutex.RUnlock()
		return nil, nil, errAccountSnapshotChanged
	}
	for i := range state.Wallets {
		wallet := &state.Wallets[i]
		info := p.walletInfoByFingerprintLocked(wallet.Fingerprint)
		if mergeWalletSilentPay(wallet, info) {
			changed = true
		}
	}
	var secret []byte
	if changed {
		secret = slices.Clone(p.accountSecret)
	}
	p.mutex.RUnlock()
	if !changed {
		return nil, nil, nil
	}
	defer zeroBytes(secret)
	repaired, err := account.EncodeManagedState(state)
	if err != nil {
		return nil, nil, err
	}
	encoded, err := account.SealManagedState(secret, accountID, state, nil)
	if err != nil {
		zeroBytes(repaired)
		return nil, nil, err
	}
	return encoded, repaired, nil
}

func mergeWalletSilentPay(wallet *account.ManagedWallet, info *WalletInfo) bool {
	if wallet == nil || info == nil || wallet.Deleted || wallet.Type != account.WalletMnemonic {
		return false
	}
	changed := false
	for index, enabled := range info.SPEnabled {
		if !enabled || index >= uint32(info.Accounts) || slices.Contains(wallet.SPEnabled, index) {
			continue
		}
		wallet.SPEnabled = append(wallet.SPEnabled, index)
		for wallet.AccountCount <= index {
			sub := wallet.AccountCount
			wallet.SubAccounts = append(wallet.SubAccounts, account.SubAccount{
				Index: sub, Name: info.AccountNames[sub], DID: info.AccountDIDs[sub],
			})
			wallet.AccountCount++
		}
		changed = true
	}
	return changed
}

// Only failed database writes stop account sync. Removal also reads records.
type accountRemovalBatch struct{ indexer.WriteBatch }

func (b accountRemovalBatch) Put(key, value []byte) error {
	return accountDBWriteError(b.WriteBatch.Put(key, value))
}

func (b accountRemovalBatch) Delete(key []byte) error {
	return accountDBWriteError(b.WriteBatch.Delete(key))
}

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
	var deletes []*WalletInfo
	for _, remote := range state.Wallets {
		info := walletInfoByFingerprintInMap(wallets, remote.Fingerprint)
		if remote.Deleted {
			if info != nil {
				if remote.Fingerprint == state.RootFingerprint {
					return fmt.Errorf("the account root cannot be deleted")
				}
				delete(wallets, info.Id)
				deletes = append(deletes, info)
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
		info.SPEnabled = make(map[uint32]bool, len(remote.SPEnabled))
		for _, index := range remote.SPEnabled {
			info.SPEnabled[index] = true
		}
		encoded, err := EncodeToBytes(&info.WalletInDB)
		if err != nil {
			return err
		}
		puts[info.Id] = encoded
	}
	err := p.withAccountLocalState(false, func() error {
		p.rgbReservationMu.Lock()
		defer p.rgbReservationMu.Unlock()
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
		for _, info := range deletes {
			if err := p.stageAccountWalletDelete(batch, walletFingerprint(info.Wallet)); err != nil {
				return err
			}
			if err := batch.Delete([]byte(getWalletDBKey(info.Id))); err != nil {
				return accountDBWriteError(err)
			}
		}
		if err := batch.Put([]byte(DB_KEY_STATUS), encodedStatus); err != nil {
			return accountDBWriteError(err)
		}
		finish, err := p.stageWalletRemoval(accountRemovalBatch{batch}, deletes)
		if err != nil {
			return err
		}
		defer func() {
			if finish != nil {
				finish(false)
			}
		}()
		if err := batch.Flush(); err != nil {
			return accountDBWriteError(err)
		}
		finish(true)
		finish = nil
		if status.CurrentWallet != p.status.CurrentWallet || status.CurrentAccount != p.status.CurrentAccount {
			p.clearRGBDirectPreviews()
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
