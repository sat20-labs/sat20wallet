package wallet

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type WalletCatalogAccount struct {
	Index     uint32 `json:"index"`
	Name      string `json:"name"`
	DID       string `json:"did,omitempty"`
	Address   string `json:"address,omitempty"`
	PubKey    string `json:"pub_key,omitempty"`
	AccountID string `json:"account_id,omitempty"`
}

type WalletCatalogEntry struct {
	ID          int64                  `json:"id"`
	Name        string                 `json:"name"`
	Fingerprint string                 `json:"fingerprint,omitempty"`
	Accounts    []WalletCatalogAccount `json:"accounts"`
}

// WalletCatalogSnapshot keeps the catalog and its selected identity in one
// read of the Manager. Clients need no separately persisted wallet view.
type WalletCatalogSnapshot struct {
	Wallets             []WalletCatalogEntry
	CurrentWalletID     int64
	CurrentAccountIndex uint32
	RootAccountID       string
}

func (p *Manager) GetWalletCatalogSnapshot() (WalletCatalogSnapshot, error) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	result := WalletCatalogSnapshot{Wallets: p.walletCatalogLocked(),
		CurrentWalletID: p.status.CurrentWallet, CurrentAccountIndex: p.status.CurrentAccount}
	var root *WalletInfo
	var err error
	if p.accountProfile == nil {
		if first := p.firstWalletLocked(); first != nil && first.Type == WALLET_TYPE_PRIVKEY {
			return result, nil
		}
		root, err = p.accountManagementCandidateRootLocked()
	} else {
		root, err = p.accountManagementRootWalletLocked()
	}
	if err != nil {
		if p.wallet != nil {
			return result, err
		}
		return result, nil
	}
	if root != nil && root.Wallet != nil {
		result.RootAccountID, err = dkvsAccountID(cloneWalletAtAccountZero(root.Wallet))
	}
	return result, err
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
	return p.walletCatalogLocked()
}

func (p *Manager) walletCatalogLocked() []WalletCatalogEntry {
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
					compressed := pubKey.SerializeCompressed()
					account.PubKey = fmt.Sprintf("%x", compressed)
					if accountID, err := dkvsindexer.CanonicalAccountID(compressed); err == nil {
						account.AccountID = accountID
					}
				}
			}
			entry.Accounts = append(entry.Accounts, account)
		}
		result = append(result, entry)
	}
	return result
}

// Validate the candidate without mutating the live catalog or its database.
func (p *Manager) validateWalletCatalogChangeLocked(candidate *WalletInfo) error {
	if err := p.checkAccountManagedDataImport(); err != nil {
		return err
	}
	infos := p.canonicalWalletInfosLocked()
	found := false
	for index, info := range infos {
		if info.Id == candidate.Id {
			infos[index], found = candidate, true
		}
	}
	if !found {
		infos = append(infos, candidate)
	}
	if len(infos) > account.MaxManagedStateItems {
		return fmt.Errorf("wallet catalog exceeds recovery limit")
	}
	names := make(map[string]struct{}, len(infos))
	for _, info := range infos {
		name := strings.Join(strings.Fields(info.Name), " ")
		if name == "" || len(name) > account.MaxManagedStateString ||
			info.Accounts < 1 || info.Accounts > account.MaxManagedStateItems {
			return fmt.Errorf("wallet metadata exceeds recovery limit")
		}
		if _, duplicate := names[name]; duplicate {
			return fmt.Errorf("duplicate wallet name")
		}
		names[name] = struct{}{}
		for index := uint32(0); index < uint32(info.Accounts); index++ {
			if len(info.AccountNames[index]) > account.MaxManagedStateString ||
				len(info.AccountDIDs[index]) > account.MaxManagedStateString {
				return fmt.Errorf("account metadata exceeds recovery limit")
			}
		}
	}
	if p.accountProfile != nil && p.accountPassword != "" {
		state, err := p.buildInitialManagedStateFromInfosLocked(p.accountPassword, p.accountProfile.RootFingerprint, infos)
		if err != nil {
			return err
		}
		defer func() {
			for index := range state.Wallets {
				state.Wallets[index].Mnemonic = ""
			}
		}()
		// Admission must include the published data reference and retained
		// tombstones, rather than the smaller first-wallet bootstrap encoding.
		base, err := account.OpenManagedState(p.accountSecret, p.accountProfile.AccountID, p.accountProfile.StateEnvelope)
		if err != nil {
			return err
		}
		state.Revision = base.Revision + 1
		state.DataRevision = p.accountProfile.ManagedDataRevision + 1
		if state.Revision == 0 || state.DataRevision == 0 {
			return fmt.Errorf("account management revision overflow")
		}
		state.DataHash = strings.Repeat("0", 64)
		for index := range state.Wallets {
			state.Wallets[index].Revision = state.Revision
		}
		for _, previous := range base.Wallets {
			if findManagedWallet(&state, previous.Fingerprint) == nil {
				state.Wallets = append(state.Wallets, account.ManagedWallet{
					Fingerprint: previous.Fingerprint, Revision: state.Revision, Deleted: true,
				})
			}
		}
		return account.ValidateManagedState(state)
	}
	return nil
}

func (p *Manager) UpdateWalletName(id int64, name string) error {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return fmt.Errorf("wallet name is required")
	}
	p.channelIdentityMu.Lock()
	defer p.channelIdentityMu.Unlock()
	p.mutex.Lock()
	defer p.mutex.Unlock()
	info := p.walletInfoMap[id]
	if info == nil {
		return fmt.Errorf("can't find wallet %d", id)
	}
	if strings.TrimSpace(info.Name) == name {
		return nil
	}
	candidate := cloneWalletInfoForAccountSync(info)
	candidate.Name = name
	if err := p.validateWalletCatalogChangeLocked(candidate); err != nil {
		return err
	}
	return p.commitWalletCatalogMutationLocked(candidate, nil, accountManagementMutation{
		Type: accountMutationWalletName, Fingerprint: walletFingerprint(info.Wallet),
		WalletID: id, Name: name,
	})
}

func (p *Manager) EnsureAccount(id int64, index uint32, name, did string) error {
	if index >= account.MaxManagedStateItems {
		return fmt.Errorf("account index exceeds recovery limit")
	}
	p.channelIdentityMu.Lock()
	defer p.channelIdentityMu.Unlock()
	p.mutex.Lock()
	defer p.mutex.Unlock()
	info := p.walletInfoMap[id]
	if info == nil {
		return fmt.Errorf("can't find wallet %d", id)
	}
	info = cloneWalletInfoForAccountSync(info)
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
	if err := p.validateWalletCatalogChangeLocked(info); err != nil {
		return err
	}
	return p.commitWalletCatalogMutationLocked(info, nil, accountManagementMutation{
		Type: accountMutationEnsureAccount, Fingerprint: walletFingerprint(info.Wallet),
		WalletID: id, Account: index, Name: name, DID: did,
	})
}

func (p *Manager) UpdateAccountMetadata(id int64, index uint32, name, did string) error {
	p.channelIdentityMu.Lock()
	defer p.channelIdentityMu.Unlock()
	p.mutex.Lock()
	defer p.mutex.Unlock()
	info := p.walletInfoMap[id]
	if info == nil {
		return fmt.Errorf("can't find wallet %d", id)
	}
	if uint64(index) >= uint64(info.Accounts) {
		return fmt.Errorf("account index %d is not enabled", index)
	}
	info = cloneWalletInfoForAccountSync(info)
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
	if err := p.validateWalletCatalogChangeLocked(info); err != nil {
		return err
	}
	return p.commitWalletCatalogMutationLocked(info, nil, accountManagementMutation{
		Type: accountMutationMetadata, Fingerprint: walletFingerprint(info.Wallet),
		WalletID: id, Account: index, Name: name, DID: did,
	})
}

// Commit the catalog record and its sync overlay together. Callers own p.mutex;
// no live catalog, selection or generation changes until Flush succeeds.
func (p *Manager) commitWalletCatalogMutationLocked(candidate *WalletInfo, status *Status,
	mutation accountManagementMutation) error {
	profile, err := p.prepareAccountMutationLocked(mutation)
	if err != nil {
		return err
	}
	encoded, err := EncodeToBytes(&candidate.WalletInDB)
	if err != nil {
		return err
	}
	batch := p.db.NewWriteBatch()
	if batch == nil {
		return fmt.Errorf("create wallet catalog batch")
	}
	defer batch.Close()
	if err := batch.Put([]byte(getWalletDBKey(candidate.Id)), encoded); err != nil {
		return err
	}
	if profile != nil {
		encoded, err := EncodeToBytes(profile)
		if err != nil {
			return err
		}
		if err := batch.Put(accountManagementProfileKey(), encoded); err != nil {
			return err
		}
	}
	if status != nil {
		encoded, err := encodeStatusToBytes(status)
		if err != nil {
			return err
		}
		if err := batch.Put([]byte(DB_KEY_STATUS), encoded); err != nil {
			return err
		}
	}
	if err := batch.Flush(); err != nil {
		return err
	}
	if live := p.walletInfoMap[candidate.Id]; live != nil {
		live.WalletInDB = candidate.WalletInDB
	} else {
		p.walletInfoMap[candidate.Id] = candidate
	}
	p.accountProfile = profile
	if status != nil {
		applyStatusSnapshot(p.status, status)
	}
	p.bumpAccountGenerationLocked()
	if profile != nil {
		p.scheduleAccountManagedStateSync()
	}
	return nil
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
	p.channelIdentityMu.Lock()
	defer p.channelIdentityMu.Unlock()
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if err := p.checkAccountManagedDataImport(); err != nil {
		return err
	}
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
	profile, err := p.prepareAccountMutationLocked(accountManagementMutation{
		Type: accountMutationDeleteWallet, Fingerprint: walletFingerprint(info.Wallet), WalletID: id,
	})
	if err != nil {
		return err
	}
	status := cloneStatusForAccountRestore(p.status)
	var next *WalletInfo
	if status.CurrentWallet == id {
		for candidateID, candidate := range p.walletInfoMap {
			if candidateID != id && candidate != nil && (next == nil || candidateID < next.Id) {
				next = candidate
			}
		}
		if next == nil || next.Wallet == nil {
			return fmt.Errorf("no unlocked wallet is available after deletion")
		}
		status.CurrentWallet, status.CurrentAccount = next.Id, 0
	}
	status.TotalWallet = len(p.walletInfoMap) - 1
	batch := p.db.NewWriteBatch()
	if batch == nil {
		return fmt.Errorf("create wallet deletion batch")
	}
	defer batch.Close()
	if profile != nil {
		encoded, err := EncodeToBytes(profile)
		if err != nil {
			return err
		}
		if err := batch.Put(accountManagementProfileKey(), encoded); err != nil {
			return err
		}
	}
	if err := batch.Delete([]byte(getWalletDBKey(id))); err != nil {
		return err
	}
	encodedStatus, err := encodeStatusToBytes(status)
	if err != nil {
		return err
	}
	if err := batch.Put([]byte(DB_KEY_STATUS), encodedStatus); err != nil {
		return err
	}
	if err := batch.Flush(); err != nil {
		return err
	}
	delete(p.walletInfoMap, id)
	p.accountProfile = profile
	applyStatusSnapshot(p.status, status)
	p.bumpAccountGenerationLocked()
	if profile != nil {
		p.scheduleAccountManagedStateSync()
	}
	if next != nil {
		p.wallet = next.Wallet
		p.wallet.SetSubAccount(0)
		_ = p.rgbManager.selectRGB11Scope()
		_ = p.rgbManager.rebuildRGB11Locks()
	}
	p.markDKVSStateDirty()
	p.wakeChannelHeartbeat()
	return nil
}
