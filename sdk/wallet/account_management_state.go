package wallet

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const accountManagedStatePath = "account/state"

const (
	accountMutationAddWallet     = "add-wallet"
	accountMutationDeleteWallet  = "delete-wallet"
	accountMutationEnsureAccount = "ensure-account"
	accountMutationMetadata      = "metadata"
)

type AccountManagementStatus struct {
	Active          bool   `json:"active"`
	AccountID       string `json:"account_id,omitempty"`
	PackageID       string `json:"package_id,omitempty"`
	RecoveryMode    string `json:"recovery_mode,omitempty"`
	StorageMode     string `json:"storage_mode,omitempty"`
	PublicLocator   string `json:"public_locator,omitempty"`
	RootFingerprint string `json:"root_fingerprint,omitempty"`
	RootWalletID    int64  `json:"root_wallet_id,omitempty"`
	StateSeq        uint64 `json:"state_seq,omitempty"`
	PendingChanges  int    `json:"pending_changes,omitempty"`
	LastRehearsalAt int64  `json:"last_rehearsal_at,omitempty"`
}

type AccountManagementRestoreOptions struct {
	Location        AccountIndexerLocation
	StorageMode     string
	RecordTTL       uint64
	AutopayContract string
	PublicLocator   string
}

type RecoveredAccountManagementState struct {
	State    account.ManagedState
	Seq      uint64
	Hash     string
	Envelope []byte
}

func (p *Manager) GetAccountManagementStatus() AccountManagementStatus {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if p.accountProfile == nil {
		return AccountManagementStatus{}
	}
	result := AccountManagementStatus{
		Active: true, AccountID: p.accountProfile.AccountID,
		PackageID: p.accountProfile.PackageID, RecoveryMode: string(p.accountProfile.RecoveryMode),
		StorageMode: p.accountProfile.StorageMode, PublicLocator: p.accountProfile.PublicLocator,
		RootFingerprint: p.accountProfile.RootFingerprint, StateSeq: p.accountProfile.StateSeq,
		PendingChanges: len(p.accountProfile.Pending), LastRehearsalAt: p.accountProfile.LastRehearsalAt,
	}
	if root, err := p.accountManagementRootWalletLocked(); err == nil {
		result.RootWalletID = root.Id
	}
	return result
}

func (p *Manager) accountManagementCandidateRootLocked() (*WalletInfo, error) {
	root := p.firstWalletLocked()
	if root == nil || root.Wallet == nil || root.Type != WALLET_TYPE_MNEMONIC {
		return nil, fmt.Errorf("the first mnemonic wallet must be unlocked")
	}
	clone := root.Wallet.Clone()
	if clone == nil {
		return nil, fmt.Errorf("account management wallet is unavailable")
	}
	clone.SetSubAccount(0)
	return root, nil
}

func cloneWalletAtAccountZero(value common.Wallet) common.Wallet {
	if value == nil {
		return nil
	}
	clone := value.Clone()
	if clone != nil {
		clone.SetSubAccount(0)
	}
	return clone
}

func (p *Manager) accountManagementRootWallet() (common.Wallet, error) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	var info *WalletInfo
	var err error
	if p.accountProfile == nil {
		info, err = p.accountManagementCandidateRootLocked()
	} else {
		info, err = p.accountManagementRootWalletLocked()
	}
	if err != nil {
		return nil, err
	}
	root := cloneWalletAtAccountZero(info.Wallet)
	if root == nil {
		return nil, fmt.Errorf("account management wallet is unavailable")
	}
	return root, nil
}

func (p *Manager) accountManagedStateKey(root common.Wallet) (string, error) {
	if root == nil || root.GetPubKey() == nil {
		return "", fmt.Errorf("account management wallet is unavailable")
	}
	return dkvsindexer.PersonalKey(root.GetPubKey().SerializeCompressed(), accountManagedStatePath)
}

func accountStateDigest(value []byte) string {
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:])
}

func accountStateMutation(profile *accountManagementProfile, root common.Wallet,
	key string, value []byte) (dkvsValueMutation, error) {

	if profile == nil || root == nil || key == "" || len(value) == 0 {
		return dkvsValueMutation{}, fmt.Errorf("invalid account management state")
	}
	mutation := dkvsValueMutation{
		Key: key, Value: value, Owner: root, Signature: dkvsSignatureAccount,
		Policy: dkvsStoragePolicy{TTL: profile.RecordTTL},
	}
	switch profile.StorageMode {
	case AccountStorageTemporary:
		mutation.Policy.FreeLocal = true
	case AccountStoragePaid:
		mutation.Policy.Autopay = &DKVSAutopayOptions{
			AddressParams: GetChainParam_SatsNet(), PoolContract: profile.AutopayContract,
		}
	default:
		return dkvsValueMutation{}, fmt.Errorf("unsupported account storage mode %q", profile.StorageMode)
	}
	return mutation, nil
}

func (p *Manager) managedWalletFromInfoLocked(info *WalletInfo, password string,
	revision uint64) (account.ManagedWallet, error) {

	if info == nil || info.Wallet == nil || info.Type != WALLET_TYPE_MNEMONIC {
		return account.ManagedWallet{}, fmt.Errorf("only unlocked mnemonic wallets can be managed")
	}
	mnemonic, err := p.loadWalletSecret(info, password)
	if err != nil {
		return account.ManagedWallet{}, err
	}
	subAccounts := make([]account.SubAccount, 0, info.Accounts)
	for index := uint32(0); index < uint32(info.Accounts); index++ {
		subAccounts = append(subAccounts, account.SubAccount{
			Index: index, Name: info.AccountNames[index], DID: info.AccountDIDs[index],
		})
	}
	return account.ManagedWallet{
		Fingerprint: walletFingerprint(info.Wallet), Revision: revision,
		Name: info.Name, Mnemonic: mnemonic, AccountCount: uint32(info.Accounts),
		SubAccounts: subAccounts,
	}, nil
}

func (p *Manager) buildInitialManagedStateLocked(password, rootFingerprint string) (account.ManagedState, error) {
	state := account.ManagedState{
		Version: account.ManagedStateVersion, RootFingerprint: rootFingerprint, Revision: 1,
		Wallets: make([]account.ManagedWallet, 0, len(p.walletInfoMap)),
	}
	for _, info := range p.canonicalWalletInfosLocked() {
		item, err := p.managedWalletFromInfoLocked(info, password, state.Revision)
		if err != nil {
			return account.ManagedState{}, err
		}
		state.Wallets = append(state.Wallets, item)
	}
	return state, nil
}

func (p *Manager) ActivateAccountManagement(secret []byte, password string,
	authorization AccountStorageAuthorization, locator account.Locator, publicLocator string) error {

	if len(secret) != 32 {
		return fmt.Errorf("invalid account management secret")
	}
	p.mutex.Lock()
	if p.accountProfile != nil {
		p.mutex.Unlock()
		return fmt.Errorf("account management is already active")
	}
	rootInfo, err := p.accountManagementCandidateRootLocked()
	if err != nil {
		p.mutex.Unlock()
		return err
	}
	root := cloneWalletAtAccountZero(rootInfo.Wallet)
	rootFingerprint := walletFingerprint(root)
	accountID, err := dkvsAccountID(root)
	if err != nil || accountID != locator.AccountID {
		p.mutex.Unlock()
		return fmt.Errorf("recovery package does not belong to the first wallet")
	}
	state, err := p.buildInitialManagedStateLocked(password, rootFingerprint)
	if err != nil {
		p.mutex.Unlock()
		return err
	}
	envelope, err := account.SealManagedState(secret, accountID, state, nil)
	if err != nil {
		p.mutex.Unlock()
		return err
	}
	secretCipher, secretSalt, err := p.encryptAccountManagementSecret(password, secret)
	if err != nil {
		p.mutex.Unlock()
		return err
	}
	deviceID, err := p.newAccountManagementDeviceID()
	if err != nil {
		p.mutex.Unlock()
		return err
	}
	profile := &accountManagementProfile{
		Version: accountManagementProfileVersion, RootFingerprint: rootFingerprint,
		AccountID: accountID, PackageID: locator.PackageID, RecoveryMode: locator.RecoveryMode,
		StorageMode: authorization.Mode, Location: authorization.Location,
		RecordTTL: authorization.RecordOptions.TTL, PublicLocator: publicLocator,
		LastRehearsalAt: time.Now().UnixMilli(),
		SecretCipher:    secretCipher, SecretSalt: secretSalt, DeviceID: deviceID,
		StateSeq: 1, StateEnvelope: envelope,
	}
	if authorization.Autopay != nil {
		profile.AutopayContract = authorization.Autopay.PoolContract
	}
	p.mutex.Unlock()

	store, err := p.accountDKVSStore()
	if err != nil {
		return err
	}
	key, err := p.accountManagedStateKey(root)
	if err != nil {
		return err
	}
	mutation, err := accountStateMutation(profile, root, key, envelope)
	if err != nil {
		return err
	}
	if _, err := store.Put(mutation); err != nil {
		return err
	}
	profile.StateHash = accountStateDigest(envelope)

	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.accountProfile = profile
	zeroBytes(p.accountSecret)
	p.accountSecret = append([]byte(nil), secret...)
	p.accountPassword = password
	if err := p.saveAccountManagementProfileLocked(); err != nil {
		p.accountProfile = nil
		zeroBytes(p.accountSecret)
		p.accountSecret = nil
		p.accountPassword = ""
		return err
	}
	return nil
}

func (p *Manager) LoadAccountManagementStateForRecovery(location AccountIndexerLocation, locator account.Locator,
	secret []byte, rootMnemonic string) (*RecoveredAccountManagementState, error) {

	root := NewInternalWalletWithMnemonic(rootMnemonic, "", GetChainParam())
	if root == nil {
		return nil, fmt.Errorf("invalid account management root wallet")
	}
	root.SetSubAccount(0)
	accountID, err := dkvsAccountID(root)
	if err != nil || accountID != locator.AccountID {
		return nil, fmt.Errorf("recovery package does not belong to its root wallet")
	}
	key, err := dkvsindexer.PersonalKey(root.GetPubKey().SerializeCompressed(), accountManagedStatePath)
	if err != nil {
		return nil, err
	}
	store, err := p.accountDKVSStoreForLocation(location)
	if err != nil {
		return nil, err
	}
	record, err := store.Get(key)
	if err != nil {
		return nil, err
	}
	state, err := account.OpenManagedState(secret, locator.AccountID, record.Value)
	if err != nil {
		return nil, err
	}
	if state.RootFingerprint != walletFingerprint(root) {
		return nil, fmt.Errorf("managed account state root does not match recovery package")
	}
	return &RecoveredAccountManagementState{
		State: state, Seq: state.Revision, Hash: accountStateDigest(record.Value),
		Envelope: append([]byte(nil), record.Value...),
	}, nil
}

func (p *Manager) RestoreAccountManagementState(value RecoveredAccountManagementState,
	secret []byte, password string, locator account.Locator,
	options AccountManagementRestoreOptions) ([]RestoredWalletResult, error) {

	if len(secret) != 32 || value.State.RootFingerprint == "" || value.Seq == 0 ||
		len(value.Envelope) == 0 {
		return nil, fmt.Errorf("invalid managed account recovery state")
	}
	backup, err := account.BackupFromManagedState(value.State)
	if err != nil {
		return nil, err
	}
	defer clearAccountBackup(&backup)
	secretCipher, secretSalt, err := p.encryptAccountManagementSecret(password, secret)
	if err != nil {
		return nil, err
	}
	deviceID, err := p.newAccountManagementDeviceID()
	if err != nil {
		return nil, err
	}
	wallets, err := p.RestoreAccountBackupWithResult(backup, password)
	if err != nil {
		return nil, err
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()
	root := p.firstWalletLocked()
	if root == nil || walletFingerprint(root.Wallet) != value.State.RootFingerprint {
		return nil, fmt.Errorf("restored account management root wallet is invalid")
	}
	profile := &accountManagementProfile{
		Version: accountManagementProfileVersion, RootFingerprint: value.State.RootFingerprint,
		AccountID: locator.AccountID, PackageID: locator.PackageID, RecoveryMode: locator.RecoveryMode,
		StorageMode: options.StorageMode, Location: options.Location, RecordTTL: options.RecordTTL,
		AutopayContract: options.AutopayContract, PublicLocator: options.PublicLocator,
		SecretCipher: secretCipher, SecretSalt: secretSalt, DeviceID: deviceID,
		StateSeq: value.Seq, StateHash: value.Hash,
		StateEnvelope: append([]byte(nil), value.Envelope...),
	}
	p.accountProfile = profile
	zeroBytes(p.accountSecret)
	p.accountSecret = append([]byte(nil), secret...)
	p.accountPassword = password
	if err := p.saveAccountManagementProfileLocked(); err != nil {
		p.accountProfile = nil
		zeroBytes(p.accountSecret)
		p.accountSecret = nil
		p.accountPassword = ""
		return nil, err
	}
	return wallets, nil
}

func randomAccountMutationID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func (p *Manager) queueAccountMutationLocked(mutation accountManagementMutation) error {
	if p.accountProfile == nil {
		return nil
	}
	if mutation.ID == "" {
		id, err := randomAccountMutationID()
		if err != nil {
			return err
		}
		mutation.ID = id
	}
	p.accountProfile.Pending = append(p.accountProfile.Pending, mutation)
	if err := p.saveAccountManagementProfileLocked(); err != nil {
		return err
	}
	p.markDKVSStateDirty()
	return nil
}

func accountPendingFingerprints(values []accountManagementMutation) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value.Fingerprint != "" {
			result[value.Fingerprint] = struct{}{}
		}
	}
	return result
}

func findManagedWallet(state *account.ManagedState, fingerprint string) *account.ManagedWallet {
	if state == nil {
		return nil
	}
	for index := range state.Wallets {
		if state.Wallets[index].Fingerprint == fingerprint {
			return &state.Wallets[index]
		}
	}
	return nil
}

func (p *Manager) walletInfoByFingerprintLocked(fingerprint string) *WalletInfo {
	for _, info := range p.canonicalWalletInfosLocked() {
		if info != nil && info.Wallet != nil && walletFingerprint(info.Wallet) == fingerprint {
			return info
		}
	}
	return nil
}

func (p *Manager) applyRemoteManagedStateLocked(state account.ManagedState,
	pending map[string]struct{}) error {

	password := p.accountPassword
	for _, remote := range state.Wallets {
		if _, ok := pending[remote.Fingerprint]; ok {
			continue
		}
		local := p.walletInfoByFingerprintLocked(remote.Fingerprint)
		if remote.Deleted {
			if local == nil || remote.Fingerprint == p.accountProfile.RootFingerprint {
				continue
			}
			if err := p.db.Delete([]byte(getWalletDBKey(local.Id))); err != nil {
				return err
			}
			delete(p.walletInfoMap, local.Id)
			if p.status.CurrentWallet == local.Id {
				root, err := p.accountManagementRootWalletLocked()
				if err != nil {
					return err
				}
				p.wallet = root.Wallet
				p.wallet.SetSubAccount(0)
				p.status.CurrentWallet = root.Id
				p.status.CurrentAccount = 0
				if err := p.saveStatus(); err != nil {
					return err
				}
			}
			continue
		}
		if local == nil {
			walletValue := NewInternalWalletWithMnemonic(remote.Mnemonic, "", GetChainParam())
			if walletValue == nil || walletFingerprint(walletValue) != remote.Fingerprint {
				return fmt.Errorf("invalid managed wallet %s", remote.Fingerprint)
			}
			if err := p.saveMnemonic(remote.Mnemonic, password, walletValue); err != nil {
				return err
			}
			local = p.walletInfoMap[walletValue.GetId()]
		}
		local.Name = remote.Name
		local.Accounts = int(remote.AccountCount)
		local.AccountNames = make(map[uint32]string, len(remote.SubAccounts))
		local.AccountDIDs = make(map[uint32]string, len(remote.SubAccounts))
		for _, sub := range remote.SubAccounts {
			local.AccountNames[sub.Index] = sub.Name
			local.AccountDIDs[sub.Index] = sub.DID
		}
		if err := saveWallet(p.db, &local.WalletInDB); err != nil {
			return err
		}
	}
	return nil
}

func managedWalletMetadataMatches(info *WalletInfo, remote *account.ManagedWallet) bool {
	if info == nil || remote == nil || remote.Deleted ||
		strings.TrimSpace(info.Name) != strings.TrimSpace(remote.Name) ||
		uint32(info.Accounts) != remote.AccountCount ||
		len(remote.SubAccounts) != info.Accounts {
		return false
	}
	for _, sub := range remote.SubAccounts {
		if sub.Index >= uint32(info.Accounts) ||
			strings.TrimSpace(info.AccountNames[sub.Index]) != strings.TrimSpace(sub.Name) ||
			strings.TrimSpace(info.AccountDIDs[sub.Index]) != strings.TrimSpace(sub.DID) {
			return false
		}
	}
	return true
}

func (p *Manager) reconcileLocalManagedInventoryLocked(state *account.ManagedState) error {
	if p.accountProfile == nil || state == nil {
		return nil
	}
	pending := accountPendingFingerprints(p.accountProfile.Pending)
	added := false
	for _, info := range p.canonicalWalletInfosLocked() {
		if info == nil || info.Wallet == nil {
			continue
		}
		fingerprint := walletFingerprint(info.Wallet)
		if _, exists := pending[fingerprint]; exists {
			continue
		}
		remote := findManagedWallet(state, fingerprint)
		if remote != nil && !remote.Deleted && managedWalletMetadataMatches(info, remote) {
			continue
		}
		id, err := randomAccountMutationID()
		if err != nil {
			return err
		}
		mutationType := accountMutationMetadata
		if remote == nil || remote.Deleted {
			mutationType = accountMutationAddWallet
		}
		p.accountProfile.Pending = append(p.accountProfile.Pending, accountManagementMutation{
			ID: id, Type: mutationType, Fingerprint: fingerprint, WalletID: info.Id,
		})
		pending[fingerprint] = struct{}{}
		added = true
	}
	if added {
		return p.saveAccountManagementProfileLocked()
	}
	return nil
}

func (p *Manager) applyPendingManagedStateLocked(state *account.ManagedState) error {
	if p.accountProfile == nil || state == nil || len(p.accountProfile.Pending) == 0 {
		return nil
	}
	state.Revision++
	changed := make(map[string]struct{})
	for _, mutation := range p.accountProfile.Pending {
		changed[mutation.Fingerprint] = struct{}{}
	}
	for fingerprint := range changed {
		item := findManagedWallet(state, fingerprint)
		var deleteRequested bool
		for _, mutation := range p.accountProfile.Pending {
			if mutation.Fingerprint == fingerprint && mutation.Type == accountMutationDeleteWallet {
				deleteRequested = true
			}
		}
		if deleteRequested {
			if fingerprint == p.accountProfile.RootFingerprint {
				return fmt.Errorf("the account management wallet cannot be deleted")
			}
			if item == nil {
				state.Wallets = append(state.Wallets, account.ManagedWallet{
					Fingerprint: fingerprint, Revision: state.Revision, Deleted: true,
				})
			} else {
				*item = account.ManagedWallet{
					Fingerprint: fingerprint, Revision: state.Revision, Deleted: true,
				}
			}
			continue
		}
		info := p.walletInfoByFingerprintLocked(fingerprint)
		if info == nil {
			return fmt.Errorf("managed wallet %s is unavailable", fingerprint)
		}
		updated, err := p.managedWalletFromInfoLocked(info, p.accountPassword, state.Revision)
		if err != nil {
			return err
		}
		if item == nil {
			state.Wallets = append(state.Wallets, updated)
		} else {
			*item = updated
		}
	}
	return nil
}

func (p *Manager) SyncAccountManagementState(_ context.Context) error {
	p.mutex.RLock()
	if p.accountProfile == nil || len(p.accountSecret) != 32 || p.accountPassword == "" {
		p.mutex.RUnlock()
		return nil
	}
	profile := *p.accountProfile
	profile.Pending = append([]accountManagementMutation(nil), p.accountProfile.Pending...)
	secret := append([]byte(nil), p.accountSecret...)
	p.mutex.RUnlock()
	defer zeroBytes(secret)

	root, err := p.accountManagementRootWallet()
	if err != nil {
		return err
	}
	key, err := p.accountManagedStateKey(root)
	if err != nil {
		return err
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return err
	}
	var (
		finalEnvelope []byte
		finalSeq      uint64
		pendingCount  int
	)
	_, err = store.Update([]string{key}, func(current map[string]*dkvsValue,
		_ map[string]uint64) ([]dkvsValueMutation, error) {

		remoteRecord := current[key]
		var (
			state     account.ManagedState
			remoteSeq uint64
		)
		if remoteRecord == nil {
			if len(profile.StateEnvelope) == 0 {
				return nil, fmt.Errorf("account management state is unavailable")
			}
			state, err = account.OpenManagedState(secret, profile.AccountID, profile.StateEnvelope)
		} else {
			state, err = account.OpenManagedState(secret, profile.AccountID, remoteRecord.Value)
			if err == nil {
				remoteSeq = state.Revision
			}
		}
		if err != nil {
			return nil, err
		}
		if profile.StateSeq > remoteSeq && len(profile.StateEnvelope) != 0 {
			state, err = account.OpenManagedState(secret, profile.AccountID, profile.StateEnvelope)
			if err != nil {
				return nil, err
			}
		}

		p.mutex.Lock()
		if p.accountProfile == nil || p.accountProfile.AccountID != profile.AccountID {
			p.mutex.Unlock()
			return nil, nil
		}
		pendingFingerprints := accountPendingFingerprints(p.accountProfile.Pending)
		if err := p.applyRemoteManagedStateLocked(state, pendingFingerprints); err != nil {
			p.mutex.Unlock()
			return nil, err
		}
		if err := p.reconcileLocalManagedInventoryLocked(&state); err != nil {
			p.mutex.Unlock()
			return nil, err
		}
		if err := p.applyPendingManagedStateLocked(&state); err != nil {
			p.mutex.Unlock()
			return nil, err
		}
		pendingCount = len(p.accountProfile.Pending)
		p.mutex.Unlock()

		needsPublish := pendingCount != 0 || profile.StateSeq > remoteSeq || remoteRecord == nil
		if !needsPublish {
			finalEnvelope = append([]byte(nil), remoteRecord.Value...)
			finalSeq = remoteSeq
			return nil, nil
		}
		finalEnvelope, err = account.SealManagedState(secret, profile.AccountID, state, nil)
		if err != nil {
			return nil, err
		}
		finalSeq = state.Revision
		mutation, err := accountStateMutation(&profile, root, key, finalEnvelope)
		if err != nil {
			return nil, err
		}
		return []dkvsValueMutation{mutation}, nil
	})
	if err != nil {
		return err
	}
	if len(finalEnvelope) == 0 || finalSeq == 0 {
		return nil
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.accountProfile == nil || p.accountProfile.AccountID != profile.AccountID {
		return nil
	}
	if pendingCount <= len(p.accountProfile.Pending) {
		p.accountProfile.Pending = append([]accountManagementMutation(nil), p.accountProfile.Pending[pendingCount:]...)
	}
	p.accountProfile.StateSeq = finalSeq
	p.accountProfile.StateHash = accountStateDigest(finalEnvelope)
	p.accountProfile.StateEnvelope = finalEnvelope
	return p.saveAccountManagementProfileLocked()
}
