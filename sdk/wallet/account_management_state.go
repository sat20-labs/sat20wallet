package wallet

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	accountMutationWalletName    = "wallet-name"
	accountMutationMetadata      = "metadata"
)

type AccountManagementStatus struct {
	Active              bool   `json:"active"`
	RecoveryConfigured  bool   `json:"recovery_configured"`
	ManagedDataRevision uint64 `json:"managed_data_revision,omitempty"`
	ManagedDataDirty    bool   `json:"managed_data_dirty,omitempty"`
	AccountID           string `json:"account_id,omitempty"`
	PackageID           string `json:"package_id,omitempty"`
	RecoveryMode        string `json:"recovery_mode,omitempty"`
	StorageMode         string `json:"storage_mode,omitempty"`
	PublicLocator       string `json:"public_locator,omitempty"`
	RootFingerprint     string `json:"root_fingerprint,omitempty"`
	RootWalletID        int64  `json:"root_wallet_id,omitempty"`
	StateSeq            uint64 `json:"state_seq,omitempty"`
	PendingChanges      int    `json:"pending_changes,omitempty"`
	LastRehearsalAt     int64  `json:"last_rehearsal_at,omitempty"`
}

type AccountManagementRestoreOptions struct {
	Location        AccountIndexerLocation
	StorageMode     string
	RecordTTL       uint64
	AutopayContract string
	PublicLocator   string
}

type RecoveredAccountManagementState struct {
	State               account.ManagedState
	Seq                 uint64
	Hash                string
	Envelope            []byte
	ManagedData         account.ManagedDataBundle
	ManagedDataHash     string
	ManagedDataEnvelope []byte
}

func (p *Manager) GetAccountManagementStatus() AccountManagementStatus {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if p.accountProfile == nil {
		return AccountManagementStatus{}
	}
	result := AccountManagementStatus{
		Active: true, RecoveryConfigured: p.accountProfile.RecoveryConfigured,
		ManagedDataRevision: p.accountProfile.ManagedDataRevision,
		ManagedDataDirty:    p.accountProfile.ManagedDataDirty,
		AccountID:           p.accountProfile.AccountID,
		PackageID:           p.accountProfile.PackageID, RecoveryMode: string(p.accountProfile.RecoveryMode),
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
		if profile.RecordTTL == 0 {
			return dkvsValueMutation{}, dkvsindexer.ErrInvalidRecord
		}
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

func (p *Manager) initializeAccountManagementLocked(password string) error {
	if p.accountProfile != nil {
		return nil
	}
	rootInfo, err := p.accountManagementCandidateRootLocked()
	if err != nil {
		return err
	}
	root := cloneWalletAtAccountZero(rootInfo.Wallet)
	rootFingerprint := walletFingerprint(root)
	accountID, err := dkvsAccountID(root)
	if err != nil {
		return err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	defer zeroBytes(secret)
	state, err := p.buildInitialManagedStateLocked(password, rootFingerprint)
	if err != nil {
		return err
	}
	envelope, err := account.SealManagedState(secret, accountID, state, nil)
	if err != nil {
		return err
	}
	secretCipher, secretSalt, err := p.encryptAccountManagementSecret(password, secret)
	if err != nil {
		return err
	}
	deviceID, err := p.newAccountManagementDeviceID()
	if err != nil {
		return err
	}
	location := AccountIndexerLocation{}
	if p.cfg != nil && p.cfg.IndexerL2 != nil {
		location = AccountIndexerLocation{
			Scheme: p.cfg.IndexerL2.Scheme, Host: p.cfg.IndexerL2.Host, Proxy: p.cfg.IndexerL2.Proxy,
		}
	}
	profile := &accountManagementProfile{
		Version: accountManagementProfileVersion, RootFingerprint: rootFingerprint,
		AccountID: accountID, StorageMode: AccountStorageTemporary, Location: location,
		// FREE_LOCAL retention is resolved from the connected service node on
		// every synchronization. A zero value here means "not resolved yet".
		RecordTTL:    0,
		SecretCipher: secretCipher, SecretSalt: secretSalt, DeviceID: deviceID,
		StateSeq: state.Revision, StateHash: accountStateDigest(envelope),
		StateEnvelope: envelope, ManagedDataDirty: true, ManagedDataGeneration: 1,
		RecoveryConfigured: false,
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
		return err
	}
	p.markDKVSStateDirty()
	return nil
}

func (p *Manager) ActivateAccountManagement(secret []byte, password string,
	authorization AccountStorageAuthorization, locator account.Locator, publicLocator string) error {

	if len(secret) != 32 {
		return fmt.Errorf("invalid account management secret")
	}
	if authorization.Mode != AccountStorageTemporary && authorization.Mode != AccountStoragePaid {
		return fmt.Errorf("unsupported account storage mode %q", authorization.Mode)
	}
	if authorization.Mode == AccountStoragePaid &&
		(authorization.Autopay == nil || strings.TrimSpace(authorization.Autopay.PoolContract) == "") {
		return fmt.Errorf("paid account management requires AUTOPAY")
	}

	releaseRGB11Scope := p.beginRGB11ScopeChange()
	defer releaseRGB11Scope()

	p.mutex.Lock()
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
	if p.accountProfile != nil && (p.accountProfile.RootFingerprint != rootFingerprint ||
		p.accountProfile.AccountID != accountID) {
		p.mutex.Unlock()
		return fmt.Errorf("existing account management root is inconsistent")
	}
	state, err := p.buildInitialManagedStateLocked(password, rootFingerprint)
	if err != nil {
		p.mutex.Unlock()
		return err
	}
	stateRevision := uint64(1)
	dataRevision := uint64(1)
	var deviceID []byte
	if p.accountProfile != nil {
		stateRevision = p.accountProfile.StateSeq + 1
		if stateRevision == 0 {
			p.mutex.Unlock()
			return fmt.Errorf("account management state revision overflow")
		}
		if p.accountProfile.ManagedDataRevision != 0 {
			dataRevision = p.accountProfile.ManagedDataRevision + 1
			if dataRevision == 0 {
				p.mutex.Unlock()
				return fmt.Errorf("account-managed data revision overflow")
			}
		}
		deviceID = append([]byte(nil), p.accountProfile.DeviceID...)
	}
	state.Revision = stateRevision
	for index := range state.Wallets {
		state.Wallets[index].Revision = stateRevision
	}
	p.mutex.Unlock()

	managedData, err := p.buildAccountManagedDataSnapshot(secret, accountID, dataRevision)
	if err != nil {
		return err
	}
	state.DataRevision = managedData.Bundle.Revision
	state.DataHash = managedData.Hash
	stateEnvelope, err := account.SealManagedState(secret, accountID, state, nil)
	if err != nil {
		return err
	}
	secretCipher, secretSalt, err := p.encryptAccountManagementSecret(password, secret)
	if err != nil {
		return err
	}
	if len(deviceID) == 0 {
		deviceID, err = p.newAccountManagementDeviceID()
		if err != nil {
			return err
		}
	}
	profile := &accountManagementProfile{
		Version: accountManagementProfileVersion, RootFingerprint: rootFingerprint,
		AccountID: accountID, PackageID: locator.PackageID, RecoveryMode: locator.RecoveryMode,
		StorageMode: authorization.Mode, Location: authorization.Location,
		RecordTTL: authorization.RecordOptions.TTL, PublicLocator: publicLocator,
		LastRehearsalAt: time.Now().UnixMilli(), RecoveryConfigured: true,
		SecretCipher: secretCipher, SecretSalt: secretSalt, DeviceID: deviceID,
		StateSeq: state.Revision, StateHash: accountStateDigest(stateEnvelope),
		StateEnvelope: stateEnvelope, ManagedDataRevision: managedData.Bundle.Revision,
		ManagedDataHash: managedData.Hash, ManagedDataEnvelope: managedData.Envelope,
		ManagedDataDirty: false, ManagedDataGeneration: 1,
	}
	if authorization.Autopay != nil {
		profile.AutopayContract = authorization.Autopay.PoolContract
	}

	store, err := p.accountDKVSStore()
	if err != nil {
		return err
	}
	stateKey, err := p.accountManagedStateKey(root)
	if err != nil {
		return err
	}
	dataKey, err := p.accountManagedDataBlobKey(root)
	if err != nil {
		return err
	}
	_, err = store.Update([]string{stateKey, dataKey}, func(_ map[string]*dkvsValue,
		_ map[string]uint64) ([]dkvsValueMutation, error) {
		return accountManagementMutations(profile, root, stateKey, stateEnvelope,
			dataKey, managedData.Envelope, true)
	})
	if err != nil {
		return err
	}
	if err := p.verifyAccountManagedStorage(store, profile, stateKey, dataKey,
		stateEnvelope, managedData.Envelope); err != nil {
		return err
	}

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
	p.markDKVSStateDirty()
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
	stateKey, err := dkvsindexer.PersonalKey(root.GetPubKey().SerializeCompressed(), accountManagedStatePath)
	if err != nil {
		return nil, err
	}
	dataKey, err := p.accountManagedDataBlobKey(root)
	if err != nil {
		return nil, err
	}
	store, err := p.accountDKVSStoreForLocation(location)
	if err != nil {
		return nil, err
	}
	if err := store.Refresh(stateKey, dataKey); err != nil {
		return nil, err
	}
	stateRecord, err := store.Get(stateKey)
	if err != nil {
		return nil, err
	}
	state, err := account.OpenManagedState(secret, locator.AccountID, stateRecord.Value)
	if err != nil {
		return nil, err
	}
	if state.RootFingerprint != walletFingerprint(root) {
		return nil, fmt.Errorf("managed account state root does not match recovery package")
	}
	var dataValue *dkvsValue
	if state.DataRevision != 0 {
		dataValue, err = store.Get(dataKey)
		if err != nil {
			return nil, err
		}
	}
	managedData, err := openAccountManagedDataValue(secret, locator.AccountID, dataValue, state)
	if err != nil {
		return nil, err
	}
	return &RecoveredAccountManagementState{
		State: state, Seq: state.Revision, Hash: accountStateDigest(stateRecord.Value),
		Envelope:    append([]byte(nil), stateRecord.Value...),
		ManagedData: managedData.Bundle, ManagedDataHash: managedData.Hash,
		ManagedDataEnvelope: append([]byte(nil), managedData.Envelope...),
	}, nil
}

func (p *Manager) RestoreAccountManagementState(value RecoveredAccountManagementState,
	secret []byte, password string, locator account.Locator,
	options AccountManagementRestoreOptions) ([]RestoredWalletResult, error) {

	if len(secret) != 32 || value.State.RootFingerprint == "" || value.Seq == 0 || len(value.Envelope) == 0 {
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

	releaseRGB11Scope := p.beginRGB11ScopeChange()
	defer releaseRGB11Scope()
	p.mutex.Lock()
	prepared, err := p.prepareAccountRestoreLocked(backup, password)
	if err != nil {
		p.mutex.Unlock()
		return nil, err
	}
	root := prepared.wallets[prepared.status.CurrentWallet]
	if root == nil || root.Wallet == nil || walletFingerprint(root.Wallet) != value.State.RootFingerprint {
		p.mutex.Unlock()
		return nil, fmt.Errorf("restored account management root wallet is invalid")
	}
	profile := &accountManagementProfile{
		Version: accountManagementProfileVersion, RootFingerprint: value.State.RootFingerprint,
		AccountID: locator.AccountID, PackageID: locator.PackageID, RecoveryMode: locator.RecoveryMode,
		StorageMode: options.StorageMode, Location: options.Location, RecordTTL: options.RecordTTL,
		AutopayContract: options.AutopayContract, PublicLocator: options.PublicLocator,
		SecretCipher: secretCipher, SecretSalt: secretSalt, DeviceID: deviceID,
		StateSeq: value.Seq, StateHash: value.Hash,
		StateEnvelope:       append([]byte(nil), value.Envelope...),
		ManagedDataRevision: value.State.DataRevision, ManagedDataHash: value.ManagedDataHash,
		ManagedDataEnvelope: append([]byte(nil), value.ManagedDataEnvelope...),
		ManagedDataDirty:    false, RecoveryConfigured: true,
	}
	if err := p.persistPreparedAccountRestoreLocked(prepared, profile); err != nil {
		p.mutex.Unlock()
		return nil, err
	}
	zeroBytes(p.accountSecret)
	p.accountSecret = append([]byte(nil), secret...)
	p.accountPassword = password
	results := append([]RestoredWalletResult(nil), prepared.results...)
	p.mutex.Unlock()
	if err := p.importAccountManagedDataSnapshot(&accountManagedDataSnapshot{
		Bundle: value.ManagedData, Hash: value.ManagedDataHash,
		Envelope: append([]byte(nil), value.ManagedDataEnvelope...),
	}); err != nil {
		return nil, err
	}
	if err := p.refreshDKVSRegistrations(); err != nil {
		Log.Warningf("refresh DKVS registrations after managed account restore failed: %v", err)
	}
	return results, nil
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
	for index := len(p.accountProfile.Pending) - 1; index >= 0; index-- {
		current := &p.accountProfile.Pending[index]
		if current.Fingerprint != mutation.Fingerprint {
			continue
		}
		if current.Type == accountMutationAddWallet &&
			mutation.Type != accountMutationDeleteWallet {
			// The add mutation serializes the latest local wallet metadata.
			return nil
		}
		walletMetadata := mutation.Type == accountMutationWalletName &&
			current.Type == accountMutationWalletName
		accountMetadata := mutation.Account == current.Account &&
			(mutation.Type == accountMutationEnsureAccount || mutation.Type == accountMutationMetadata) &&
			(current.Type == accountMutationEnsureAccount || current.Type == accountMutationMetadata)
		if !walletMetadata && !accountMetadata {
			continue
		}
		mutation.ID = current.ID
		if current.Type == accountMutationEnsureAccount {
			mutation.Type = accountMutationEnsureAccount
		}
		*current = mutation
		p.accountProfile.ManagedDataDirty = true
		p.accountProfile.ManagedDataGeneration++
		if p.accountProfile.ManagedDataGeneration == 0 {
			p.accountProfile.ManagedDataGeneration = 1
		}
		if err := p.saveAccountManagementProfileLocked(); err != nil {
			return err
		}
		p.markDKVSStateDirty()
		return nil
	}
	p.accountProfile.Pending = append(p.accountProfile.Pending, mutation)
	p.accountProfile.ManagedDataDirty = true
	p.accountProfile.ManagedDataGeneration++
	if p.accountProfile.ManagedDataGeneration == 0 {
		p.accountProfile.ManagedDataGeneration = 1
	}
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

func accountPendingInventoryFingerprints(values []accountManagementMutation) map[string]struct{} {
	result := make(map[string]struct{})
	for _, value := range values {
		if value.Fingerprint != "" &&
			(value.Type == accountMutationAddWallet || value.Type == accountMutationDeleteWallet) {
			result[value.Fingerprint] = struct{}{}
		}
	}
	return result
}

func (p *Manager) replayPendingManagedMutationsLocked() error {
	if p.accountProfile == nil || len(p.accountProfile.Pending) == 0 {
		return nil
	}
	changed := make(map[int64]*WalletInfo)
	deleted := make(map[string]struct{})
	for _, mutation := range p.accountProfile.Pending {
		if mutation.Type == accountMutationDeleteWallet {
			deleted[mutation.Fingerprint] = struct{}{}
		}
	}
	for _, mutation := range p.accountProfile.Pending {
		if mutation.Type == accountMutationAddWallet || mutation.Type == accountMutationDeleteWallet {
			continue
		}
		if _, deletedLater := deleted[mutation.Fingerprint]; deletedLater {
			continue
		}
		info := p.walletInfoByFingerprintLocked(mutation.Fingerprint)
		if info == nil {
			return fmt.Errorf("managed wallet %s is unavailable", mutation.Fingerprint)
		}
		switch mutation.Type {
		case accountMutationWalletName:
			name := strings.TrimSpace(mutation.Name)
			if name == "" {
				return fmt.Errorf("wallet name is required")
			}
			info.Name = name
		case accountMutationEnsureAccount:
			normalizeWalletInfoMetadata(info, 0)
			if info.Accounts <= int(mutation.Account) {
				info.Accounts = int(mutation.Account) + 1
			}
			name := strings.TrimSpace(mutation.Name)
			if name == "" {
				name = defaultAccountName(mutation.Account)
			}
			info.AccountNames[mutation.Account] = name
			info.AccountDIDs[mutation.Account] = strings.TrimSpace(mutation.DID)
		case accountMutationMetadata:
			if int(mutation.Account) >= info.Accounts {
				return fmt.Errorf("account index %d is not enabled", mutation.Account)
			}
			normalizeWalletInfoMetadata(info, 0)
			name := strings.TrimSpace(mutation.Name)
			if name == "" {
				name = defaultAccountName(mutation.Account)
			}
			info.AccountNames[mutation.Account] = name
			info.AccountDIDs[mutation.Account] = strings.TrimSpace(mutation.DID)
		default:
			return fmt.Errorf("unsupported account management mutation %q", mutation.Type)
		}
		changed[info.Id] = info
	}
	for _, info := range changed {
		if err := saveWallet(p.db, &info.WalletInDB); err != nil {
			return err
		}
	}
	return nil
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

type accountManagementSyncSnapshot struct {
	profile  accountManagementProfile
	secret   []byte
	password string
	wallets  map[string]account.ManagedWallet
	pending  []accountManagementMutation
}

func cloneManagedWallet(value account.ManagedWallet) account.ManagedWallet {
	value.SubAccounts = append([]account.SubAccount(nil), value.SubAccounts...)
	return value
}

func cloneManagedState(value account.ManagedState) account.ManagedState {
	result := value
	result.Wallets = make([]account.ManagedWallet, len(value.Wallets))
	for index := range value.Wallets {
		result.Wallets[index] = cloneManagedWallet(value.Wallets[index])
	}
	return result
}

func managedWalletContentMatches(left account.ManagedWallet, right *account.ManagedWallet) bool {
	if right == nil || left.Deleted != right.Deleted || left.Fingerprint != right.Fingerprint {
		return false
	}
	if left.Deleted {
		return true
	}
	if strings.TrimSpace(left.Name) != strings.TrimSpace(right.Name) ||
		left.Mnemonic != right.Mnemonic || left.AccountCount != right.AccountCount ||
		len(left.SubAccounts) != len(right.SubAccounts) {
		return false
	}
	for index := range left.SubAccounts {
		l, r := left.SubAccounts[index], right.SubAccounts[index]
		if l.Index != r.Index || strings.TrimSpace(l.Name) != strings.TrimSpace(r.Name) ||
			strings.TrimSpace(l.DID) != strings.TrimSpace(r.DID) {
			return false
		}
	}
	return true
}

func (p *Manager) captureAccountManagementSyncSnapshotLocked() (*accountManagementSyncSnapshot, error) {
	if p.accountProfile == nil || len(p.accountSecret) != 32 || p.accountPassword == "" {
		return nil, nil
	}
	snapshot := &accountManagementSyncSnapshot{
		profile: *p.accountProfile, secret: append([]byte(nil), p.accountSecret...),
		password: p.accountPassword, wallets: make(map[string]account.ManagedWallet),
		pending: append([]accountManagementMutation(nil), p.accountProfile.Pending...),
	}
	snapshot.profile.Pending = append([]accountManagementMutation(nil), snapshot.pending...)
	for _, info := range p.canonicalWalletInfosLocked() {
		wallet, err := p.managedWalletFromInfoLocked(info, p.accountPassword, 1)
		if err != nil {
			zeroBytes(snapshot.secret)
			return nil, err
		}
		snapshot.wallets[wallet.Fingerprint] = wallet
	}
	return snapshot, nil
}

// buildAccountManagedStateTarget is deliberately pure. It models the old
// remote-apply -> pending-replay -> local-reconcile ordering without touching
// the live wallet catalog or database inside a DKVS CAS builder.
func buildAccountManagedStateTarget(remote account.ManagedState,
	snapshot *accountManagementSyncSnapshot) (account.ManagedState, bool, error) {
	if snapshot == nil {
		return account.ManagedState{}, false, fmt.Errorf("account management snapshot is unavailable")
	}
	target := cloneManagedState(remote)
	effective := make(map[string]account.ManagedWallet, len(snapshot.wallets)+len(remote.Wallets))
	for fingerprint, wallet := range snapshot.wallets {
		effective[fingerprint] = cloneManagedWallet(wallet)
	}
	inventoryPending := accountPendingInventoryFingerprints(snapshot.pending)
	for _, remoteWallet := range remote.Wallets {
		if _, protected := inventoryPending[remoteWallet.Fingerprint]; protected {
			continue
		}
		if remoteWallet.Deleted {
			if remoteWallet.Fingerprint != snapshot.profile.RootFingerprint {
				delete(effective, remoteWallet.Fingerprint)
			}
			continue
		}
		effective[remoteWallet.Fingerprint] = cloneManagedWallet(remoteWallet)
	}
	deleteRequested := make(map[string]bool)
	changed := make(map[string]struct{})
	for _, mutation := range snapshot.pending {
		if mutation.Fingerprint != "" && mutation.Type == accountMutationDeleteWallet {
			deleteRequested[mutation.Fingerprint] = true
		}
	}
	for _, mutation := range snapshot.pending {
		if mutation.Fingerprint == "" {
			continue
		}
		changed[mutation.Fingerprint] = struct{}{}
		if mutation.Type == accountMutationDeleteWallet || deleteRequested[mutation.Fingerprint] {
			continue
		}
		local, ok := snapshot.wallets[mutation.Fingerprint]
		if !ok {
			return account.ManagedState{}, false, fmt.Errorf("managed wallet %s is unavailable", mutation.Fingerprint)
		}
		switch mutation.Type {
		case accountMutationAddWallet:
			effective[mutation.Fingerprint] = cloneManagedWallet(local)
		case accountMutationWalletName:
			merged, exists := effective[mutation.Fingerprint]
			if !exists {
				return account.ManagedState{}, false, fmt.Errorf("managed wallet %s is unavailable", mutation.Fingerprint)
			}
			merged.Name = local.Name
			effective[mutation.Fingerprint] = merged
		case accountMutationEnsureAccount, accountMutationMetadata:
			merged, exists := effective[mutation.Fingerprint]
			if !exists {
				return account.ManagedState{}, false, fmt.Errorf("managed wallet %s is unavailable", mutation.Fingerprint)
			}
			if mutation.Account >= local.AccountCount {
				return account.ManagedState{}, false, fmt.Errorf("managed account %d is unavailable", mutation.Account)
			}
			if merged.AccountCount <= mutation.Account {
				merged.AccountCount = mutation.Account + 1
			}
			byIndex := make(map[uint32]account.SubAccount, len(merged.SubAccounts)+1)
			for _, item := range merged.SubAccounts {
				byIndex[item.Index] = item
			}
			for _, item := range local.SubAccounts {
				if item.Index == mutation.Account {
					byIndex[item.Index] = item
					break
				}
			}
			merged.SubAccounts = merged.SubAccounts[:0]
			for index := uint32(0); index < merged.AccountCount; index++ {
				item, exists := byIndex[index]
				if !exists {
					item = account.SubAccount{Index: index, Name: defaultAccountName(index)}
				}
				merged.SubAccounts = append(merged.SubAccounts, item)
			}
			effective[mutation.Fingerprint] = merged
		default:
			return account.ManagedState{}, false, fmt.Errorf("unsupported account management mutation %q", mutation.Type)
		}
	}
	for fingerprint, wallet := range effective {
		remoteWallet := findManagedWallet(&target, fingerprint)
		if remoteWallet == nil || !managedWalletContentMatches(wallet, remoteWallet) {
			changed[fingerprint] = struct{}{}
		}
	}
	if len(changed) == 0 {
		return target, false, nil
	}
	target.Revision++
	for fingerprint := range changed {
		item := findManagedWallet(&target, fingerprint)
		if deleteRequested[fingerprint] {
			if fingerprint == snapshot.profile.RootFingerprint {
				return account.ManagedState{}, false, fmt.Errorf("the account management wallet cannot be deleted")
			}
			deleted := account.ManagedWallet{Fingerprint: fingerprint, Revision: target.Revision, Deleted: true}
			if item == nil {
				target.Wallets = append(target.Wallets, deleted)
			} else {
				*item = deleted
			}
			continue
		}
		wallet, ok := effective[fingerprint]
		if !ok {
			return account.ManagedState{}, false, fmt.Errorf("managed wallet %s is unavailable", fingerprint)
		}
		wallet.Revision = target.Revision
		wallet.Deleted = false
		if item == nil {
			target.Wallets = append(target.Wallets, cloneManagedWallet(wallet))
		} else {
			*item = cloneManagedWallet(wallet)
		}
	}
	return target, true, nil
}

func cloneWalletInfoForAccountSync(info *WalletInfo) *WalletInfo {
	if info == nil {
		return nil
	}
	clone := *info
	clone.Mnemonic = append([]byte(nil), info.Mnemonic...)
	clone.Salt = append([]byte(nil), info.Salt...)
	clone.AccountNames = make(map[uint32]string, len(info.AccountNames))
	for key, value := range info.AccountNames {
		clone.AccountNames[key] = value
	}
	clone.AccountDIDs = make(map[uint32]string, len(info.AccountDIDs))
	for key, value := range info.AccountDIDs {
		clone.AccountDIDs[key] = value
	}
	return &clone
}

func walletInfoByFingerprintInMap(values map[int64]*WalletInfo, fingerprint string) *WalletInfo {
	for _, info := range values {
		if info != nil && info.Wallet != nil && walletFingerprint(info.Wallet) == fingerprint {
			return info
		}
	}
	return nil
}

func (p *Manager) newWalletInfoFromManagedWalletLocked(remote account.ManagedWallet,
	password string) (*WalletInfo, error) {
	walletValue := NewInternalWalletWithMnemonic(remote.Mnemonic, "", GetChainParam())
	if walletValue == nil || walletFingerprint(walletValue) != remote.Fingerprint {
		return nil, fmt.Errorf("invalid managed wallet %s", remote.Fingerprint)
	}
	info := &WalletInfo{WalletInDB: WalletInDB{
		Id: walletValue.GetId(), Accounts: int(remote.AccountCount), Type: WALLET_TYPE_MNEMONIC,
		Name: remote.Name, AccountNames: make(map[uint32]string, len(remote.SubAccounts)),
		AccountDIDs: make(map[uint32]string, len(remote.SubAccounts)),
	}, Wallet: walletValue}
	for _, sub := range remote.SubAccounts {
		info.AccountNames[sub.Index] = sub.Name
		info.AccountDIDs[sub.Index] = sub.DID
	}
	key, err := p.newSnaclKey(password)
	if err != nil {
		return nil, err
	}
	info.Mnemonic, err = key.Encrypt([]byte(remote.Mnemonic))
	if err != nil {
		return nil, err
	}
	info.Salt = key.Marshal()
	return info, nil
}

func pendingAfterCommittedSnapshot(current, committed []accountManagementMutation) []accountManagementMutation {
	byID := make(map[string]accountManagementMutation, len(committed))
	for _, mutation := range committed {
		byID[mutation.ID] = mutation
	}
	remaining := make([]accountManagementMutation, 0, len(current))
	for _, mutation := range current {
		if previous, ok := byID[mutation.ID]; ok && previous == mutation {
			continue
		}
		remaining = append(remaining, mutation)
	}
	return remaining
}

// commitAccountManagedStateLocked applies the already committed remote state
// in one local batch. Snapshot pending entries are cleared only when their
// current value is byte-for-byte unchanged; concurrent edits remain queued.
func (p *Manager) commitAccountManagedStateLocked(state account.ManagedState,
	snapshot *accountManagementSyncSnapshot, envelope []byte,
	managedData *accountManagedDataSnapshot) (bool, bool, error) {
	if p.accountProfile == nil || snapshot == nil || p.accountProfile.AccountID != snapshot.profile.AccountID {
		return false, false, nil
	}
	remaining := pendingAfterCommittedSnapshot(p.accountProfile.Pending, snapshot.pending)
	protected := accountPendingFingerprints(remaining)
	wallets := make(map[int64]*WalletInfo, len(p.walletInfoMap))
	for id, info := range p.walletInfoMap {
		wallets[id] = cloneWalletInfoForAccountSync(info)
	}
	status := cloneStatusForAccountRestore(p.status)
	puts := make(map[int64]*WalletInfo)
	deletes := make(map[int64]struct{})
	for _, remote := range state.Wallets {
		if _, skip := protected[remote.Fingerprint]; skip {
			continue
		}
		local := walletInfoByFingerprintInMap(wallets, remote.Fingerprint)
		if remote.Deleted {
			if local == nil || remote.Fingerprint == snapshot.profile.RootFingerprint {
				continue
			}
			delete(wallets, local.Id)
			deletes[local.Id] = struct{}{}
			continue
		}
		if local == nil {
			var err error
			local, err = p.newWalletInfoFromManagedWalletLocked(remote, snapshot.password)
			if err != nil {
				return false, false, err
			}
			if collision := wallets[local.Id]; collision != nil && walletFingerprint(collision.Wallet) != remote.Fingerprint {
				return false, false, fmt.Errorf("managed wallet id collision")
			}
			wallets[local.Id] = local
		}
		local.Name = remote.Name
		local.Accounts = int(remote.AccountCount)
		local.AccountNames = make(map[uint32]string, len(remote.SubAccounts))
		local.AccountDIDs = make(map[uint32]string, len(remote.SubAccounts))
		for _, sub := range remote.SubAccounts {
			local.AccountNames[sub.Index] = sub.Name
			local.AccountDIDs[sub.Index] = sub.DID
		}
		puts[local.Id] = local
	}
	status.TotalWallet = len(wallets)
	current := wallets[status.CurrentWallet]
	if current == nil {
		root := walletInfoByFingerprintInMap(wallets, snapshot.profile.RootFingerprint)
		if root == nil {
			return false, false, fmt.Errorf("account management root wallet is unavailable")
		}
		status.CurrentWallet, status.CurrentAccount = root.Id, 0
		current = root
	} else if status.CurrentAccount >= uint32(current.Accounts) {
		status.CurrentAccount = 0
	}
	profile := *p.accountProfile
	profile.RecordTTL = snapshot.profile.RecordTTL
	profile.Pending = remaining
	profile.StateSeq = state.Revision
	profile.StateHash = accountStateDigest(envelope)
	profile.StateEnvelope = append([]byte(nil), envelope...)
	if managedData != nil {
		profile.ManagedDataRevision = managedData.Bundle.Revision
		profile.ManagedDataHash = managedData.Hash
		profile.ManagedDataEnvelope = append([]byte(nil), managedData.Envelope...)
	}
	sameManagedGeneration := p.accountProfile.ManagedDataGeneration ==
		snapshot.profile.ManagedDataGeneration
	profile.ManagedDataDirty = len(remaining) != 0 || !sameManagedGeneration

	batch := p.db.NewWriteBatch()
	if batch == nil {
		return false, false, fmt.Errorf("create managed state batch")
	}
	defer batch.Close()
	for id := range deletes {
		if err := batch.Delete([]byte(getWalletDBKey(id))); err != nil {
			return false, false, err
		}
	}
	for id, info := range puts {
		encoded, err := EncodeToBytes(&info.WalletInDB)
		if err != nil {
			return false, false, err
		}
		if err := batch.Put([]byte(getWalletDBKey(id)), encoded); err != nil {
			return false, false, err
		}
	}
	statusBytes, err := encodeStatusToBytes(status)
	if err != nil {
		return false, false, err
	}
	if err := batch.Put([]byte(DB_KEY_STATUS), statusBytes); err != nil {
		return false, false, err
	}
	profileBytes, err := EncodeToBytes(&profile)
	if err != nil {
		return false, false, err
	}
	if err := batch.Put(accountManagementProfileKey(), profileBytes); err != nil {
		return false, false, err
	}
	if err := batch.Flush(); err != nil {
		return false, false, err
	}

	p.walletInfoMap = wallets
	if p.status == nil {
		p.status = status
	} else {
		applyStatusSnapshot(p.status, status)
	}
	p.accountProfile = &profile
	p.wallet = current.Wallet
	p.wallet.SetSubAccount(p.status.CurrentAccount)
	return len(remaining) != 0, sameManagedGeneration, nil
}

func (p *Manager) SyncAccountManagementState(ctx context.Context) error {
	return p.syncAccountManagementState(ctx, 0)
}

func (p *Manager) syncAccountManagementState(ctx context.Context, attempt int) error {
	_ = ctx
	p.mutex.Lock()
	snapshot, err := p.captureAccountManagementSyncSnapshotLocked()
	p.mutex.Unlock()
	if err != nil || snapshot == nil {
		return err
	}
	defer zeroBytes(snapshot.secret)

	root, err := p.accountManagementRootWallet()
	if err != nil {
		return err
	}
	stateKey, err := p.accountManagedStateKey(root)
	if err != nil {
		return err
	}
	dataKey, err := p.accountManagedDataBlobKey(root)
	if err != nil {
		return err
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return err
	}
	if err := configureAccountTemporaryRetention(store, &snapshot.profile); err != nil {
		return err
	}
	if err := store.Refresh(stateKey, dataKey); err != nil {
		return err
	}

	stateValue, stateErr := store.Get(stateKey)
	if stateErr != nil && !errors.Is(stateErr, ErrDKVSRecordNotFound) {
		return stateErr
	}
	if errors.Is(stateErr, ErrDKVSRecordNotFound) {
		stateValue = nil
	}
	var remoteState account.ManagedState
	var remoteStateEnvelope []byte
	if stateValue == nil {
		if len(snapshot.profile.StateEnvelope) == 0 {
			return fmt.Errorf("account management state is unavailable")
		}
		remoteState, err = account.OpenManagedState(snapshot.secret,
			snapshot.profile.AccountID, snapshot.profile.StateEnvelope)
		remoteStateEnvelope = append([]byte(nil), snapshot.profile.StateEnvelope...)
	} else {
		remoteState, err = account.OpenManagedState(snapshot.secret,
			snapshot.profile.AccountID, stateValue.Value)
		remoteStateEnvelope = append([]byte(nil), stateValue.Value...)
	}
	if err != nil {
		return err
	}
	usingLocalState := false
	if snapshot.profile.StateSeq > remoteState.Revision && len(snapshot.profile.StateEnvelope) != 0 {
		remoteState, err = account.OpenManagedState(snapshot.secret,
			snapshot.profile.AccountID, snapshot.profile.StateEnvelope)
		if err != nil {
			return err
		}
		remoteStateEnvelope = append([]byte(nil), snapshot.profile.StateEnvelope...)
		usingLocalState = true
	}

	var dataValue *dkvsValue
	if remoteState.DataRevision != 0 && !usingLocalState {
		dataValue, err = store.Get(dataKey)
		if err != nil {
			return err
		}
	}
	baseBundle, err := openProfileManagedDataBundle(snapshot.profile, snapshot.secret)
	if err != nil {
		return err
	}
	remoteManaged := &accountManagedDataSnapshot{Bundle: emptyAccountManagedDataBundle(1)}
	if usingLocalState {
		remoteManaged.Bundle = baseBundle
		remoteManaged.Hash = snapshot.profile.ManagedDataHash
		remoteManaged.Envelope = append([]byte(nil), snapshot.profile.ManagedDataEnvelope...)
	} else {
		remoteManaged, err = openAccountManagedDataValue(snapshot.secret,
			snapshot.profile.AccountID, dataValue, remoteState)
		if err != nil {
			return err
		}
	}

	target, walletChanged, err := buildAccountManagedStateTarget(remoteState, snapshot)
	if err != nil {
		return err
	}
	localCatalog, err := p.accountManagedDataCatalog()
	if err != nil {
		return err
	}
	localBundle, _, err := p.exportAccountManagedData(localCatalog, 1)
	if err != nil {
		return err
	}
	targetCatalog := accountManagedDataCatalogFromState(snapshot.profile.AccountID, _chain, target)
	maxDataRevision := snapshot.profile.ManagedDataRevision
	if remoteState.DataRevision > maxDataRevision {
		maxDataRevision = remoteState.DataRevision
	}
	if remoteManaged.Bundle.Revision > maxDataRevision {
		maxDataRevision = remoteManaged.Bundle.Revision
	}
	candidateRevision := maxDataRevision + 1
	if candidateRevision == 0 {
		return fmt.Errorf("account-managed data revision overflow")
	}
	mergedBundle, err := mergeAccountManagedDataBundles(baseBundle,
		remoteManaged.Bundle, localBundle, targetCatalog, candidateRevision)
	if err != nil {
		return err
	}
	mergedHash, err := accountManagedDataContentHash(mergedBundle.Items)
	if err != nil {
		return err
	}
	finalManaged := &accountManagedDataSnapshot{Catalog: targetCatalog,
		Bundle: mergedBundle, Hash: mergedHash}
	compressionBeneficial, err := account.ManagedDataBundleCompressionBeneficial(mergedBundle)
	if err != nil {
		return err
	}
	dataChanged := true
	if shouldReuseRemoteManagedDataEnvelope(remoteState, mergedHash,
		remoteManaged, compressionBeneficial) {
		mergedBundle.Revision = remoteState.DataRevision
		mergedBundle, err = account.NormalizeManagedDataBundle(mergedBundle)
		if err != nil {
			return err
		}
		finalManaged.Bundle = mergedBundle
		finalManaged.Envelope = append([]byte(nil), remoteManaged.Envelope...)
		dataChanged = false
	} else {
		var envelopeInfo account.ManagedDataEnvelopeInfo
		finalManaged.Envelope, envelopeInfo, err = account.SealManagedDataBundleWithInfo(
			snapshot.secret, snapshot.profile.AccountID, mergedBundle, nil)
		if err != nil {
			return err
		}
		finalManaged.Compressed = envelopeInfo.Compressed
	}
	target.DataRevision = finalManaged.Bundle.Revision
	target.DataHash = finalManaged.Hash
	dataReferenceChanged := target.DataRevision != remoteState.DataRevision ||
		target.DataHash != remoteState.DataHash
	if dataReferenceChanged && !walletChanged {
		target.Revision++
		if target.Revision == 0 {
			return fmt.Errorf("account management state revision overflow")
		}
	}
	logicalStateChanged := walletChanged || dataReferenceChanged
	finalStateEnvelope := remoteStateEnvelope
	if logicalStateChanged {
		finalStateEnvelope, err = account.SealManagedState(snapshot.secret,
			snapshot.profile.AccountID, target, nil)
		if err != nil {
			return err
		}
	}

	statePolicyMismatch := !accountRecordMatchesStorage(stateValue, &snapshot.profile)
	dataPolicyMismatch := !accountRecordMatchesStorage(dataValue, &snapshot.profile)
	needsPublish := logicalStateChanged || usingLocalState || stateValue == nil ||
		statePolicyMismatch || dataValue == nil || dataPolicyMismatch
	writeData := dataChanged || dataValue == nil || dataPolicyMismatch
	capturedStateHash := ""
	capturedDataHash := ""
	if stateValue != nil {
		capturedStateHash = stateValue.Hash
	}
	if dataValue != nil {
		capturedDataHash = dataValue.Hash
	}
	if needsPublish {
		_, err = store.Update([]string{stateKey, dataKey}, func(current map[string]*dkvsValue,
			_ map[string]uint64) ([]dkvsValueMutation, error) {
			currentStateHash, currentDataHash := "", ""
			if current[stateKey] != nil {
				currentStateHash = current[stateKey].Hash
			}
			if current[dataKey] != nil {
				currentDataHash = current[dataKey].Hash
			}
			if currentStateHash != capturedStateHash || currentDataHash != capturedDataHash {
				return nil, dkvsindexer.ErrWriteConflict
			}
			return accountManagementMutations(&snapshot.profile, root, stateKey,
				finalStateEnvelope, dataKey, finalManaged.Envelope, writeData)
		})
		if err != nil {
			if attempt < 2 && (errors.Is(err, dkvsindexer.ErrWriteConflict) ||
				errors.Is(err, dkvsindexer.ErrStaleGeneration) ||
				errors.Is(err, dkvsindexer.ErrPathDiverged) ||
				errors.Is(err, dkvsindexer.ErrInvalidSequence)) {
				return p.syncAccountManagementState(ctx, attempt+1)
			}
			return err
		}
	}

	releaseRGB11Scope := p.beginRGB11ScopeChange()
	p.mutex.Lock()
	pendingRemains, importManagedData, commitErr := p.commitAccountManagedStateLocked(
		target, snapshot, finalStateEnvelope, finalManaged)
	p.mutex.Unlock()
	releaseRGB11Scope()
	if commitErr != nil {
		return commitErr
	}
	if importManagedData {
		if err := p.importAccountManagedDataSnapshot(finalManaged); err != nil {
			return err
		}
	}
	if pendingRemains || !importManagedData {
		p.markDKVSStateDirty()
	}
	if err := p.refreshDKVSRegistrations(); err != nil {
		Log.Warningf("refresh DKVS registrations after account state sync failed: %v", err)
	}
	return nil
}
