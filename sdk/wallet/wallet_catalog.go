package wallet

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)

type WalletCatalogAccount struct {
	Index   uint32 `json:"index"`
	Name    string `json:"name"`
	DID     string `json:"did,omitempty"`
	Address string `json:"address,omitempty"`
	PubKey  string `json:"pub_key,omitempty"`
}

type WalletCatalogEntry struct {
	ID          int64                  `json:"id"`
	Name        string                 `json:"name"`
	Fingerprint string                 `json:"fingerprint,omitempty"`
	Accounts    []WalletCatalogAccount `json:"accounts"`
}

func defaultWalletName(position int) string {
	return fmt.Sprintf("Wallet %d", position+1)
}

func defaultAccountName(index uint32) string {
	return fmt.Sprintf("Account %d", index+1)
}

func normalizeWalletInfoMetadata(info *WalletInfo, position int) bool {
	if info == nil {
		return false
	}
	changed := false
	if info.Accounts < 1 {
		info.Accounts = 1
		changed = true
	}
	if strings.TrimSpace(info.Name) == "" {
		info.Name = defaultWalletName(position)
		changed = true
	}
	if info.AccountNames == nil {
		info.AccountNames = make(map[uint32]string)
		changed = true
	}
	if info.AccountDIDs == nil {
		info.AccountDIDs = make(map[uint32]string)
		changed = true
	}
	for index := uint32(0); index < uint32(info.Accounts); index++ {
		if strings.TrimSpace(info.AccountNames[index]) == "" {
			info.AccountNames[index] = defaultAccountName(index)
			changed = true
		}
	}
	return changed
}

func walletFingerprint(value common.Wallet) string {
	if value == nil || value.GetNodePubKey() == nil {
		return ""
	}
	hash := sha256.Sum256(value.GetNodePubKey().SerializeCompressed())
	return fmt.Sprintf("%x", hash[:])
}

func (p *Manager) canonicalWalletInfosLocked() []*WalletInfo {
	ids := make([]int64, 0, len(p.walletInfoMap))
	for id := range p.walletInfoMap {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	result := make([]*WalletInfo, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		info := p.walletInfoMap[id]
		if info == nil {
			continue
		}
		fingerprint := walletFingerprint(info.Wallet)
		if fingerprint != "" {
			if _, ok := seen[fingerprint]; ok {
				continue
			}
			seen[fingerprint] = struct{}{}
		}
		result = append(result, info)
	}
	return result
}

func (p *Manager) normalizeWalletCatalogLocked() error {
	ids := make([]int64, 0, len(p.walletInfoMap))
	for id := range p.walletInfoMap {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for position, id := range ids {
		info := p.walletInfoMap[id]
		if normalizeWalletInfoMetadata(info, position) {
			if err := saveWallet(p.db, &info.WalletInDB); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Manager) GetWalletCatalog() []WalletCatalogEntry {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	ids := make([]int64, 0, len(p.walletInfoMap))
	for id := range p.walletInfoMap {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	result := make([]WalletCatalogEntry, 0, len(ids))
	for position, id := range ids {
		info := p.walletInfoMap[id]
		if info == nil {
			continue
		}
		name := strings.TrimSpace(info.Name)
		if name == "" {
			name = defaultWalletName(position)
		}
		entry := WalletCatalogEntry{
			ID:          id,
			Name:        name,
			Fingerprint: walletFingerprint(info.Wallet),
			Accounts:    make([]WalletCatalogAccount, 0, info.Accounts),
		}
		for index := uint32(0); index < uint32(info.Accounts); index++ {
			account := WalletCatalogAccount{
				Index: index,
				Name:  strings.TrimSpace(info.AccountNames[index]),
				DID:   strings.TrimSpace(info.AccountDIDs[index]),
			}
			if account.Name == "" {
				account.Name = defaultAccountName(index)
			}
			if info.Wallet != nil {
				account.Address = info.Wallet.GetAddressByIndex(index)
				if pubKey := info.Wallet.GetPubKeyByIndex(index); pubKey != nil {
					account.PubKey = fmt.Sprintf("%x", pubKey.SerializeCompressed())
				}
			}
			entry.Accounts = append(entry.Accounts, account)
		}
		result = append(result, entry)
	}
	return result
}

func (p *Manager) UpdateWalletName(id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("wallet name is required")
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	info := p.walletInfoMap[id]
	if info == nil {
		return fmt.Errorf("can't find wallet %d", id)
	}
	if strings.TrimSpace(info.Name) == name {
		return nil
	}
	info.Name = name
	if err := saveWallet(p.db, &info.WalletInDB); err != nil {
		return err
	}
	return p.queueAccountMutationLocked(accountManagementMutation{
		Type: accountMutationWalletName, Fingerprint: walletFingerprint(info.Wallet),
		WalletID: id, Name: name,
	})
}

func (p *Manager) EnsureAccount(id int64, index uint32, name, did string) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	info := p.walletInfoMap[id]
	if info == nil {
		return fmt.Errorf("can't find wallet %d", id)
	}
	changed := normalizeWalletInfoMetadata(info, 0)
	if info.Accounts <= int(index) {
		info.Accounts = int(index) + 1
		changed = true
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultAccountName(index)
	}
	did = strings.TrimSpace(did)
	if info.AccountNames[index] != name || info.AccountDIDs[index] != did {
		info.AccountNames[index] = name
		info.AccountDIDs[index] = did
		changed = true
	}
	if !changed {
		return nil
	}
	if err := saveWallet(p.db, &info.WalletInDB); err != nil {
		return err
	}
	return p.queueAccountMutationLocked(accountManagementMutation{
		Type: accountMutationEnsureAccount, Fingerprint: walletFingerprint(info.Wallet),
		WalletID: id, Account: index, Name: name, DID: did,
	})
}

func (p *Manager) UpdateAccountMetadata(id int64, index uint32, name, did string) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	info := p.walletInfoMap[id]
	if info == nil {
		return fmt.Errorf("can't find wallet %d", id)
	}
	if int(index) >= info.Accounts {
		return fmt.Errorf("account index %d is not enabled", index)
	}
	changed := normalizeWalletInfoMetadata(info, 0)
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultAccountName(index)
	}
	did = strings.TrimSpace(did)
	if info.AccountNames[index] != name || info.AccountDIDs[index] != did {
		info.AccountNames[index] = name
		info.AccountDIDs[index] = did
		changed = true
	}
	if !changed {
		return nil
	}
	if err := saveWallet(p.db, &info.WalletInDB); err != nil {
		return err
	}
	return p.queueAccountMutationLocked(accountManagementMutation{
		Type: accountMutationMetadata, Fingerprint: walletFingerprint(info.Wallet),
		WalletID: id, Account: index, Name: name, DID: did,
	})
}

func (p *Manager) firstWalletLocked() *WalletInfo {
	var selected *WalletInfo
	var selectedID int64
	for id, info := range p.walletInfoMap {
		if info == nil || (selected != nil && id >= selectedID) {
			continue
		}
		selected = info
		selectedID = id
	}
	return selected
}

func (p *Manager) DeleteWallet(id int64) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if len(p.walletInfoMap) <= 1 {
		return fmt.Errorf("the last wallet cannot be deleted")
	}
	info := p.walletInfoMap[id]
	if info == nil {
		return fmt.Errorf("can't find wallet %d", id)
	}
	if p.isAccountManagementRootLocked(info) {
		return fmt.Errorf("the account management wallet cannot be deleted")
	}
	pendingBefore := 0
	if p.accountProfile != nil {
		pendingBefore = len(p.accountProfile.Pending)
		if err := p.queueAccountMutationLocked(accountManagementMutation{
			Type: accountMutationDeleteWallet, Fingerprint: walletFingerprint(info.Wallet),
			WalletID: id,
		}); err != nil {
			return err
		}
	}
	if err := p.db.Delete([]byte(getWalletDBKey(id))); err != nil {
		if p.accountProfile != nil && len(p.accountProfile.Pending) > pendingBefore {
			p.accountProfile.Pending = p.accountProfile.Pending[:pendingBefore]
			_ = p.saveAccountManagementProfileLocked()
		}
		return err
	}
	delete(p.walletInfoMap, id)
	if p.status.CurrentWallet != id {
		return nil
	}
	next := p.firstWalletLocked()
	if next == nil || next.Wallet == nil {
		return fmt.Errorf("no unlocked wallet is available after deletion")
	}
	p.wallet = next.Wallet
	p.wallet.SetSubAccount(0)
	p.status.CurrentWallet = next.Id
	p.status.CurrentAccount = 0
	_ = p.rgbManager.selectRGB11Scope()
	_ = p.rgbManager.rebuildRGB11Locks()
	if err := p.saveStatus(); err != nil {
		return err
	}
	p.markDKVSStateDirty()
	return nil
}
