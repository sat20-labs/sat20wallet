package wallet

import (
	"bytes"
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

const (
	accountManagedStatePath  = "account/state"
	accountManagedStateJobID = "account-managed-state"
)

var (
	ErrAccountManagementSecretConflict    = errors.New("account management secret conflicts with the active profile")
	ErrAccountManagementWalletUnavailable = errors.New("account management wallet is unavailable")
	ErrAccountStorageModeDowngrade        = errors.New("paid account storage cannot switch to temporary")
	errAccountSnapshotChanged             = errors.New("account management snapshot changed")
)

const (
	accountMutationAddWallet     = "add-wallet"
	accountMutationDeleteWallet  = "delete-wallet"
	accountMutationEnsureAccount = "ensure-account"
	accountMutationWalletName    = "wallet-name"
	accountMutationMetadata      = "metadata"
)

func (p *Manager) runAccountOperation(ctx context.Context, operation func() error) error {
	if p == nil || operation == nil {
		return ErrDKVSPathNotSynced
	}
	for {
		p.accountSyncMu.Lock()
		if !p.accountSyncActive {
			p.accountSyncActive = true
			p.accountSyncDone = make(chan struct{})
			done := p.accountSyncDone
			p.accountSyncMu.Unlock()

			return p.executeAccountOperation(done, operation)
		}
		done := p.accountSyncDone
		p.accountSyncMu.Unlock()
		if done == nil {
			continue
		}
		if ctx == nil {
			<-done
			continue
		}
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// runAccountApplicationSync serializes account synchronization/recovery with
// every operation which can change the selected wallet/account identity or an
// RGB11 scope. The outer locks are application-state gates: network I/O may
// run while they are held, but the short manager data lock is never held
// across that I/O.
func (p *Manager) runAccountApplicationSync(ctx context.Context, operation func() error) error {
	return p.runAccountOperation(ctx, func() error {
		p.channelIdentityMu.Lock()
		releaseRGB11Scope := p.beginRGB11ScopeChange()
		defer func() {
			releaseRGB11Scope()
			p.channelIdentityMu.Unlock()
		}()
		return operation()
	})
}

func (p *Manager) executeAccountOperation(done chan struct{}, operation func() error) (err error) {
	defer func() {
		p.accountSyncMu.Lock()
		if p.accountSyncDone == done {
			p.accountSyncActive = false
			p.accountSyncDone = nil
			close(done)
		}
		p.accountSyncMu.Unlock()
	}()
	return operation()
}

func (p *Manager) accountOperationActive() bool {
	if p == nil {
		return false
	}
	p.accountSyncMu.Lock()
	active := p.accountSyncActive
	p.accountSyncMu.Unlock()
	return active
}

func (p *Manager) bumpAccountGenerationLocked() {
	p.accountGeneration++
	if p.accountGeneration == 0 {
		p.accountGeneration = 1
	}
}

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
	// RootWalletID is only a process-local wallet handle. The stable root
	// identity persisted by account management is AccountID (the BIP340
	// x-only public key); callers must never persist or compare RootWalletID
	// across a restore, device or network manager.
	RootWalletID          int64  `json:"root_wallet_id,omitempty"`
	StateSeq              uint64 `json:"state_seq,omitempty"`
	PendingChanges        int    `json:"pending_changes,omitempty"`
	LastRehearsalAt       int64  `json:"last_rehearsal_at,omitempty"`
	LastDKVSSyncErrorCode string `json:"last_dkvs_sync_error_code,omitempty"`
	LastDKVSSyncError     string `json:"last_dkvs_sync_error,omitempty"`
	LastDKVSSyncErrorAt   int64  `json:"last_dkvs_sync_error_at,omitempty"`
}

// RootAccountID returns the stable BIP340 public-key identity for the account
// management root. Unlike InternalWallet.Id, this value survives restore and
// manager recreation.
func (p *Manager) RootAccountID() (string, error) {
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return "", err
	}
	return dkvsAccountID(root)
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
	if p.dkvs != nil {
		result.LastDKVSSyncErrorCode, result.LastDKVSSyncError, result.LastDKVSSyncErrorAt = p.dkvs.lastSyncErrorStatus()
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
		return nil, ErrAccountManagementWalletUnavailable
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
		return nil, ErrAccountManagementWalletUnavailable
	}
	return root, nil
}

func (p *Manager) accountManagedStateKey(root common.Wallet) (string, error) {
	if root == nil || root.GetPubKey() == nil {
		return "", ErrAccountManagementWalletUnavailable
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
	return p.buildInitialManagedStateFromInfosLocked(password, rootFingerprint,
		p.canonicalWalletInfosLocked())
}

func (p *Manager) buildInitialManagedStateFromInfosLocked(password, rootFingerprint string,
	infos []*WalletInfo) (account.ManagedState, error) {
	state := account.ManagedState{
		Version: account.ManagedStateVersion, RootFingerprint: rootFingerprint, Revision: 1,
		Wallets: make([]account.ManagedWallet, 0, len(infos)),
	}
	for _, info := range infos {
		item, err := p.managedWalletFromInfoLocked(info, password, state.Revision)
		if err != nil {
			return account.ManagedState{}, err
		}
		state.Wallets = append(state.Wallets, item)
	}
	return state, nil
}

func (p *Manager) prepareInitialAccountManagementLocked(password string, rootInfo *WalletInfo,
	infos []*WalletInfo) (*accountManagementProfile, []byte, error) {
	if rootInfo == nil || rootInfo.Wallet == nil {
		return nil, nil, ErrAccountManagementWalletUnavailable
	}
	root := cloneWalletAtAccountZero(rootInfo.Wallet)
	rootFingerprint := walletFingerprint(root)
	accountID, err := dkvsAccountID(root)
	if err != nil {
		return nil, nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, nil, err
	}
	state, err := p.buildInitialManagedStateFromInfosLocked(password, rootFingerprint, infos)
	if err != nil {
		zeroBytes(secret)
		return nil, nil, err
	}
	envelope, err := account.SealManagedState(secret, accountID, state, nil)
	if err != nil {
		zeroBytes(secret)
		return nil, nil, err
	}
	secretCipher, secretSalt, err := p.encryptAccountManagementSecret(password, secret)
	if err != nil {
		zeroBytes(secret)
		return nil, nil, err
	}
	deviceID, err := p.newAccountManagementDeviceID()
	if err != nil {
		zeroBytes(secret)
		return nil, nil, err
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
	return profile, secret, nil
}

// InitializeAccountManagement explicitly creates a managed account around an
// unlocked mnemonic wallet. First-wallet create/import already use the same
// atomic activation path; this entry point remains for pre-existing catalogs.
func (p *Manager) InitializeAccountManagement(password string) error {
	if p == nil {
		return fmt.Errorf("wallet manager is unavailable")
	}
	for attempt := 0; attempt < 3; attempt++ {
		p.mutex.Lock()
		if p.accountProfile != nil {
			p.mutex.Unlock()
			return nil
		}
		rootInfo, err := p.accountManagementCandidateRootLocked()
		if err != nil {
			p.mutex.Unlock()
			return err
		}
		rootInfo = cloneWalletInfoForAccountSync(rootInfo)
		infos := p.canonicalWalletInfosLocked()
		clonedInfos := make([]*WalletInfo, 0, len(infos))
		for _, info := range infos {
			clonedInfos = append(clonedInfos, cloneWalletInfoForAccountSync(info))
		}
		generation := p.accountGeneration
		p.mutex.Unlock()

		profile, secret, err := p.prepareInitialAccountManagementLocked(password, rootInfo, clonedInfos)
		if err != nil {
			return err
		}
		p.mutex.Lock()
		if p.accountGeneration != generation || p.accountProfile != nil {
			p.mutex.Unlock()
			zeroBytes(secret)
			continue
		}
		p.accountProfile = profile
		zeroBytes(p.accountSecret)
		p.accountSecret = append([]byte(nil), secret...)
		p.accountPassword = password
		p.bumpAccountGenerationLocked()
		err = p.saveAccountManagementProfileLocked()
		if err != nil {
			p.accountProfile = nil
			zeroBytes(p.accountSecret)
			p.accountSecret = nil
			p.accountPassword = ""
		}
		p.mutex.Unlock()
		zeroBytes(secret)
		if err != nil {
			return err
		}
		p.markDKVSStateDirty()
		if err := p.refreshDKVSRegistrations(); err != nil {
			Log.Warningf("refresh DKVS registrations after account initialization failed: %v", err)
		}
		return nil
	}
	return errAccountSnapshotChanged
}

func (p *Manager) ActivateAccountManagement(secret []byte, password string,
	authorization AccountStorageAuthorization, locator account.Locator, publicLocator string) error {
	return p.runAccountApplicationSync(nil, func() error {
		return p.activateAccountManagement(secret, password, authorization, locator, publicLocator, 0)
	})
}

func (p *Manager) activateAccountManagement(secret []byte, password string,
	authorization AccountStorageAuthorization, locator account.Locator, publicLocator string,
	attempt int) error {

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

	if err := p.checkAccountManagedDataImport(); err != nil {
		return err
	}
	p.mutex.RLock()
	downgrade := p.accountProfile != nil &&
		p.accountProfile.StorageMode == AccountStoragePaid &&
		authorization.Mode == AccountStorageTemporary
	p.mutex.RUnlock()
	if downgrade {
		return ErrAccountStorageModeDowngrade
	}

	p.mutex.Lock()
	if err := p.validateAccountActivationSecretLocked(secret); err != nil {
		p.mutex.Unlock()
		return err
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
	if p.accountProfile != nil && (p.accountProfile.RootFingerprint != rootFingerprint ||
		p.accountProfile.AccountID != accountID) {
		p.mutex.Unlock()
		return fmt.Errorf("existing account management root is inconsistent")
	}
	infos := p.canonicalWalletInfosLocked()
	clonedInfos := make([]*WalletInfo, 0, len(infos))
	for _, info := range infos {
		clonedInfos = append(clonedInfos, cloneWalletInfoForAccountSync(info))
	}
	generation := p.accountGeneration
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
	p.mutex.Unlock()
	state, err := p.buildInitialManagedStateFromInfosLocked(password, rootFingerprint, clonedInfos)
	if err != nil {
		return err
	}
	state.Revision = stateRevision
	for index := range state.Wallets {
		state.Wallets[index].Revision = stateRevision
	}

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
	wrapperKey, err := accountRootWrapperKey(root)
	if err != nil {
		return err
	}
	if err := store.WaitReady(wrapperKey); err != nil {
		return rootDiscoveryError(err)
	}
	currentWrapper, getErr := store.Get(wrapperKey)
	if getErr != nil && !errors.Is(getErr, ErrDKVSRecordNotFound) {
		return rootDiscoveryError(getErr)
	}
	if errors.Is(getErr, ErrDKVSRecordNotFound) {
		currentWrapper = nil
	}
	if err := validateAccountRootWrapperSecret(root, accountID, secret, currentWrapper); err != nil {
		return err
	}
	wrapperEnvelope, err := sealAccountRootWrapper(root, _chain, accountID,
		rootWrapperPayload(*profile, secret), nil)
	if err != nil {
		return err
	}
	_, err = store.Update([]string{wrapperKey, stateKey, dataKey}, func(values map[string]*dkvsValue,
		_ map[string]uint64) ([]dkvsValueMutation, error) {
		return accountActivationMutations(profile, root, secret, wrapperKey, wrapperEnvelope,
			stateKey, stateEnvelope, dataKey, managedData.Envelope, values)
	})
	if err != nil {
		return err
	}
	if err := p.verifyAccountManagedStorage(store, profile, stateKey, dataKey,
		stateEnvelope, managedData.Envelope); err != nil {
		return err
	}
	if err := verifyAccountRootWrapperStorage(store, profile, root, secret, wrapperKey); err != nil {
		return err
	}
	p.mutex.RLock()
	stale := p.accountGeneration != generation || p.accountProfile != nil &&
		(p.accountProfile.RootFingerprint != rootFingerprint || p.accountProfile.AccountID != accountID ||
			p.accountProfile.StorageMode == AccountStoragePaid && authorization.Mode == AccountStorageTemporary)
	p.mutex.RUnlock()
	if stale {
		if attempt < 2 {
			return p.activateAccountManagement(secret, password, authorization, locator, publicLocator, attempt+1)
		}
		return errAccountSnapshotChanged
	}
	if err := p.bindAccountToCurrentCoreNode(root); err != nil {
		return fmt.Errorf("bind account to current CoreNode: %w", err)
	}

	p.mutex.Lock()
	if p.accountGeneration != generation {
		p.mutex.Unlock()
		if attempt < 2 {
			return p.activateAccountManagement(secret, password, authorization, locator, publicLocator, attempt+1)
		}
		return errAccountSnapshotChanged
	}
	p.accountProfile = profile
	zeroBytes(p.accountSecret)
	p.accountSecret = append([]byte(nil), secret...)
	p.accountPassword = password
	p.bumpAccountGenerationLocked()
	if err := p.saveAccountManagementProfileLocked(); err != nil {
		p.accountProfile = nil
		zeroBytes(p.accountSecret)
		p.accountSecret = nil
		p.accountPassword = ""
		p.mutex.Unlock()
		return err
	}
	p.mutex.Unlock()
	p.markDKVSStateDirty()
	return nil
}

// validateAccountActivationSecretLocked prevents a recovery-package session
// from silently replacing the random AccountSecret of an already active local
// profile. An empty profile is the explicit recovery/bootstrap case and may be
// activated with the recovered secret.
func (p *Manager) validateAccountActivationSecretLocked(secret []byte) error {
	if len(secret) != 32 {
		return fmt.Errorf("invalid account management secret")
	}
	if p.accountProfile == nil {
		return nil
	}
	if len(p.accountSecret) != 32 || !bytes.Equal(p.accountSecret, secret) {
		return ErrAccountManagementSecretConflict
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
	return p.restoreAccountManagementState(value, secret, password, locator, options, "")
}

func (p *Manager) restoreAccountManagementState(value RecoveredAccountManagementState,
	secret []byte, password string, locator account.Locator,
	options AccountManagementRestoreOptions,
	allowedImportedRoot string) ([]RestoredWalletResult, error) {
	var results []RestoredWalletResult
	err := p.runAccountApplicationSync(nil, func() error {
		var operationErr error
		results, operationErr = p.restoreAccountManagementStateOperation(
			value, secret, password, locator, options, allowedImportedRoot)
		return operationErr
	})
	return results, err
}

func (p *Manager) restoreAccountManagementStateOperation(value RecoveredAccountManagementState,
	secret []byte, password string, locator account.Locator,
	options AccountManagementRestoreOptions,
	allowedImportedRoot string) ([]RestoredWalletResult, error) {

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

	var results []RestoredWalletResult
	for attempt := 0; attempt < 3; attempt++ {
		prepared, prepareErr := p.prepareAccountRestoreWithRootLocked(backup, password, allowedImportedRoot)
		if prepareErr != nil {
			return nil, prepareErr
		}
		root := prepared.wallets[prepared.status.CurrentWallet]
		if root == nil || root.Wallet == nil || walletFingerprint(root.Wallet) != value.State.RootFingerprint {
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
		// The application sync gate remains active for the whole recovery. Only
		// the short durable identity commit below takes the manager data lock.
		p.mutex.Lock()
		if p.accountGeneration != prepared.generation {
			p.mutex.Unlock()
			continue
		}
		if err := p.db.Write(accountManagedDataImportKey(), []byte{1}); err != nil {
			p.mutex.Unlock()
			return nil, err
		}
		if err := p.persistPreparedAccountRestoreLocked(prepared, profile); err != nil {
			p.mutex.Unlock()
			if errors.Is(err, errAccountSnapshotChanged) {
				continue
			}
			return nil, err
		}
		zeroBytes(p.accountSecret)
		p.accountSecret = append([]byte(nil), secret...)
		p.accountPassword = password
		results = append([]RestoredWalletResult(nil), prepared.results...)
		p.mutex.Unlock()
		break
	}
	if len(results) == 0 {
		return nil, errAccountSnapshotChanged
	}
	if err := p.importAccountManagedDataSnapshot(&accountManagedDataSnapshot{
		Bundle: value.ManagedData, Hash: value.ManagedDataHash,
		Envelope: append([]byte(nil), value.ManagedDataEnvelope...),
	}); err != nil {
		return nil, err
	}
	// The restored catalog and Status now contain the real process-local wallet
	// ID.  Bind the long-lived RGB11 manager before closing the crash boundary;
	// otherwise later operations could keep using a pre-recovery placeholder.
	if err := p.rgbManager.selectRGB11Scope(); err != nil {
		return nil, fmt.Errorf("select restored RGB11 wallet scope: %w", err)
	}
	if err := p.rgbManager.rebuildRGB11Locks(); err != nil {
		return nil, fmt.Errorf("rebuild restored RGB11 locks: %w", err)
	}
	// The durable recovery transaction is complete once the core account state
	// and every stable provider have been committed locally. Registration and
	// mailbox refreshes are retryable network work and must not extend the crash
	// boundary: a transient transport failure must never strand this marker.
	if err := p.db.Delete(accountManagedDataImportKey()); err != nil {
		return nil, err
	}
	if err := p.refreshDKVSRegistrations(); err != nil {
		return nil, err
	}
	if err := p.importAccountManagedActiveData(); err != nil {
		return nil, err
	}
	p.wakeChannelHeartbeat()
	return results, nil
}

func randomAccountMutationID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func (p *Manager) scheduleAccountManagedStateSync() {
	if p == nil {
		return
	}
	manager := p.ensureDKVSManager()
	manager.schedule(accountManagedStateJobID, func(_ *dkvsStore) error {
		return p.SyncAccountManagementState(nil)
	})
}

func (p *Manager) queueAccountMutationLocked(mutation accountManagementMutation) error {
	p.bumpAccountGenerationLocked()
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
		p.scheduleAccountManagedStateSync()
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
	p.scheduleAccountManagedStateSync()
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

// applyRemoteManagedStateLocked requires channelIdentityMu and mutex to be held.
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
	profile    accountManagementProfile
	generation uint64
	secret     []byte
	password   string
	root       common.Wallet
	wallets    map[string]account.ManagedWallet
	pending    []accountManagementMutation
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

func (p *Manager) captureAccountManagementSyncSnapshotBaseLocked() (
	*accountManagementSyncSnapshot, []*WalletInfo, error) {
	if p.accountProfile == nil || len(p.accountSecret) != 32 || p.accountPassword == "" {
		return nil, nil, nil
	}
	snapshot := &accountManagementSyncSnapshot{
		profile:    *p.accountProfile,
		secret:     append([]byte(nil), p.accountSecret...),
		generation: p.accountGeneration,
		password:   p.accountPassword,
		wallets:    make(map[string]account.ManagedWallet),
		pending:    append([]accountManagementMutation(nil), p.accountProfile.Pending...),
	}
	rootInfo, err := p.accountManagementRootWalletLocked()
	if err != nil {
		zeroBytes(snapshot.secret)
		return nil, nil, err
	}
	snapshot.root = cloneWalletAtAccountZero(rootInfo.Wallet)
	if snapshot.root == nil {
		zeroBytes(snapshot.secret)
		return nil, nil, ErrAccountManagementWalletUnavailable
	}
	snapshot.profile.Pending = append([]accountManagementMutation(nil), snapshot.pending...)
	infos := p.canonicalWalletInfosLocked()
	clonedInfos := make([]*WalletInfo, 0, len(infos))
	for _, info := range infos {
		clonedInfos = append(clonedInfos, cloneWalletInfoForAccountSync(info))
	}
	return snapshot, clonedInfos, nil
}

func (p *Manager) populateAccountManagementSyncSnapshot(snapshot *accountManagementSyncSnapshot,
	infos []*WalletInfo) (*accountManagementSyncSnapshot, error) {
	if snapshot == nil {
		return nil, nil
	}
	for _, info := range infos {
		wallet, err := p.managedWalletFromInfoLocked(info, snapshot.password, 1)
		if err != nil {
			zeroBytes(snapshot.secret)
			return nil, err
		}
		snapshot.wallets[wallet.Fingerprint] = wallet
	}
	return snapshot, nil
}

func (p *Manager) captureAccountManagementSyncSnapshotLocked() (*accountManagementSyncSnapshot, error) {
	snapshot, infos, err := p.captureAccountManagementSyncSnapshotBaseLocked()
	if err != nil {
		return nil, err
	}
	return p.populateAccountManagementSyncSnapshot(snapshot, infos)
}

func (p *Manager) captureAccountManagementSyncSnapshot() (*accountManagementSyncSnapshot, error) {
	p.mutex.Lock()
	snapshot, infos, err := p.captureAccountManagementSyncSnapshotBaseLocked()
	p.mutex.Unlock()
	if err != nil {
		return nil, err
	}
	return p.populateAccountManagementSyncSnapshot(snapshot, infos)
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

func accountManagedACKValue(values []*dkvsValue, key string) *dkvsValue {
	for _, value := range values {
		if value != nil && value.Key == key {
			return value
		}
	}
	return nil
}

// finalizePublishedAccountManagedState records only the server-confirmed
// baseline after this device successfully publishes a state.  The live wallet
// catalog and provider data are already the source of that publication and
// must never be replaced by the older snapshot captured before the request.
//
// Application-level synchronization keeps wallet/provider operations behind
// the in-flight PUT. Once the ACK baseline is committed, those operations may
// run and enqueue the next serialized PUT.
func (p *Manager) finalizePublishedAccountManagedState(state account.ManagedState,
	snapshot *accountManagementSyncSnapshot, envelope []byte,
	managedData *accountManagedDataSnapshot) (bool, error) {

	if p == nil || snapshot == nil || managedData == nil {
		return false, errAccountSnapshotChanged
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.accountProfile == nil ||
		p.accountProfile.AccountID != snapshot.profile.AccountID ||
		p.accountProfile.RootFingerprint != snapshot.profile.RootFingerprint {
		return false, errAccountSnapshotChanged
	}

	profile := *p.accountProfile
	profile.Pending = append([]accountManagementMutation(nil), p.accountProfile.Pending...)
	profile.RecordTTL = snapshot.profile.RecordTTL
	profile.StateSeq = state.Revision
	profile.StateHash = accountStateDigest(envelope)
	profile.StateEnvelope = append([]byte(nil), envelope...)
	profile.ManagedDataRevision = managedData.Bundle.Revision
	profile.ManagedDataHash = managedData.Hash
	profile.ManagedDataEnvelope = append([]byte(nil), managedData.Envelope...)
	// The server ACK confirms exactly the immutable request snapshot. Identity
	// and RGB11 application gates prevent wallet/provider mutations from
	// changing that snapshot while the PUT is in flight. Only work queued after
	// the snapshot remains pending; ACK handling never imports its own payload.
	profile.Pending = pendingAfterCommittedSnapshot(profile.Pending, snapshot.pending)
	profile.ManagedDataDirty = len(profile.Pending) != 0 ||
		profile.ManagedDataGeneration != snapshot.profile.ManagedDataGeneration
	encoded, err := EncodeToBytes(&profile)
	if err != nil {
		return false, err
	}
	if err := p.db.Write(accountManagementProfileKey(), encoded); err != nil {
		return false, err
	}
	p.accountProfile = &profile
	p.bumpAccountGenerationLocked()
	return len(profile.Pending) != 0 || profile.ManagedDataDirty, nil
}

type accountManagedCommitBase struct {
	profile accountManagementProfile
	wallets map[int64]*WalletInfo
	status  *Status
}

type accountManagedStateCommit struct {
	wallets           map[int64]*WalletInfo
	status            *Status
	profile           accountManagementProfile
	puts              map[int64][]byte
	deletes           map[int64]struct{}
	statusBytes       []byte
	profileBytes      []byte
	currentWallet     int64
	pendingRemains    bool
	importManagedData bool
}

func (p *Manager) captureAccountManagedCommitBaseLocked(
	snapshot *accountManagementSyncSnapshot) (*accountManagedCommitBase, error) {
	if p.accountProfile == nil || snapshot == nil ||
		p.accountProfile.AccountID != snapshot.profile.AccountID ||
		p.accountGeneration != snapshot.generation {
		return nil, errAccountSnapshotChanged
	}
	base := &accountManagedCommitBase{
		profile: *p.accountProfile,
		wallets: make(map[int64]*WalletInfo, len(p.walletInfoMap)),
		status:  cloneStatusForAccountRestore(p.status),
	}
	base.profile.Pending = append([]accountManagementMutation(nil), p.accountProfile.Pending...)
	for id, info := range p.walletInfoMap {
		base.wallets[id] = cloneWalletInfoForAccountSync(info)
	}
	return base, nil
}

// prepareAccountManagedStateCommit performs mnemonic derivation, encryption
// and serialization without holding a manager or account coordination lock.
func (p *Manager) prepareAccountManagedStateCommit(state account.ManagedState,
	snapshot *accountManagementSyncSnapshot, envelope []byte,
	managedData *accountManagedDataSnapshot, base *accountManagedCommitBase) (*accountManagedStateCommit, error) {
	if snapshot == nil || base == nil || base.status == nil {
		return nil, errAccountSnapshotChanged
	}
	remaining := pendingAfterCommittedSnapshot(base.profile.Pending, snapshot.pending)
	protected := accountPendingFingerprints(remaining)
	wallets := make(map[int64]*WalletInfo, len(base.wallets))
	for id, info := range base.wallets {
		wallets[id] = cloneWalletInfoForAccountSync(info)
	}
	status := cloneStatusForAccountRestore(base.status)
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
				return nil, err
			}
			if collision := wallets[local.Id]; collision != nil && walletFingerprint(collision.Wallet) != remote.Fingerprint {
				return nil, fmt.Errorf("managed wallet id collision")
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
			return nil, fmt.Errorf("account management root wallet is unavailable")
		}
		status.CurrentWallet, status.CurrentAccount = root.Id, 0
		current = root
	} else if status.CurrentAccount >= uint32(current.Accounts) {
		status.CurrentAccount = 0
	}
	profile := base.profile
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
	sameManagedGeneration := base.profile.ManagedDataGeneration ==
		snapshot.profile.ManagedDataGeneration
	profile.ManagedDataDirty = len(remaining) != 0 || !sameManagedGeneration
	encodedPuts := make(map[int64][]byte, len(puts))
	for id, info := range puts {
		encoded, err := EncodeToBytes(&info.WalletInDB)
		if err != nil {
			return nil, err
		}
		encodedPuts[id] = encoded
	}
	statusBytes, err := encodeStatusToBytes(status)
	if err != nil {
		return nil, err
	}
	profileBytes, err := EncodeToBytes(&profile)
	if err != nil {
		return nil, err
	}
	return &accountManagedStateCommit{
		wallets: wallets, status: status, profile: profile, puts: encodedPuts, deletes: deletes,
		statusBytes: statusBytes, profileBytes: profileBytes, currentWallet: current.Id,
		pendingRemains: len(remaining) != 0, importManagedData: sameManagedGeneration,
	}, nil
}

// commitPreparedAccountManagedStateLocked is the short local commit point. No
// network request, callback, retry, key derivation or serialization is allowed
// in this section.
func (p *Manager) commitPreparedAccountManagedStateLocked(commit *accountManagedStateCommit,
	snapshot *accountManagementSyncSnapshot, markImport bool) (bool, bool, error) {
	if commit == nil || p.accountProfile == nil || snapshot == nil ||
		p.accountProfile.AccountID != snapshot.profile.AccountID ||
		p.accountGeneration != snapshot.generation {
		return false, false, errAccountSnapshotChanged
	}
	// The application sync gate has already excluded wallet/RGB operations and
	// the final snapshot check above has passed. Only now may a persistent
	// crash marker be created. It is never a normal synchronization lock.
	if markImport {
		if err := p.db.Write(accountManagedDataImportKey(), []byte{1}); err != nil {
			return false, false, err
		}
	}
	batch := p.db.NewWriteBatch()
	if batch == nil {
		return false, false, fmt.Errorf("create managed state batch")
	}
	defer batch.Close()
	for id := range commit.deletes {
		if err := batch.Delete([]byte(getWalletDBKey(id))); err != nil {
			return false, false, err
		}
	}
	for id, encoded := range commit.puts {
		if err := batch.Put([]byte(getWalletDBKey(id)), encoded); err != nil {
			return false, false, err
		}
	}
	if err := batch.Put([]byte(DB_KEY_STATUS), commit.statusBytes); err != nil {
		return false, false, err
	}
	if err := batch.Put(accountManagementProfileKey(), commit.profileBytes); err != nil {
		return false, false, err
	}
	if err := batch.Flush(); err != nil {
		return false, false, err
	}

	p.walletInfoMap = commit.wallets
	if p.status == nil {
		p.status = commit.status
	} else {
		applyStatusSnapshot(p.status, commit.status)
	}
	p.accountProfile = &commit.profile
	p.bumpAccountGenerationLocked()
	p.wallet = commit.wallets[commit.currentWallet].Wallet
	p.wallet.SetSubAccount(p.status.CurrentAccount)
	return commit.pendingRemains, commit.importManagedData, nil
}

// commitAccountManagedStateLocked is retained for callers which already own
// p.mutex. Production synchronization uses commitAccountManagedStateForSync so
// preparation stays outside all manager locks.
func (p *Manager) commitAccountManagedStateLocked(state account.ManagedState,
	snapshot *accountManagementSyncSnapshot, envelope []byte,
	managedData *accountManagedDataSnapshot) (bool, bool, error) {
	base, err := p.captureAccountManagedCommitBaseLocked(snapshot)
	if err != nil {
		return false, false, err
	}
	commit, err := p.prepareAccountManagedStateCommit(state, snapshot, envelope, managedData, base)
	if err != nil {
		return false, false, err
	}
	return p.commitPreparedAccountManagedStateLocked(commit, snapshot, false)
}

func (p *Manager) commitAccountManagedStateForSync(state account.ManagedState,
	snapshot *accountManagementSyncSnapshot, envelope []byte,
	managedData *accountManagedDataSnapshot) (bool, bool, error) {
	p.mutex.Lock()
	base, err := p.captureAccountManagedCommitBaseLocked(snapshot)
	p.mutex.Unlock()
	if err != nil {
		return false, false, err
	}
	commit, err := p.prepareAccountManagedStateCommit(state, snapshot, envelope, managedData, base)
	if err != nil {
		return false, false, err
	}
	p.mutex.Lock()
	previousWalletID := p.status.CurrentWallet
	previousAccount := p.status.CurrentAccount
	pendingRemains, importManagedData, err := p.commitPreparedAccountManagedStateLocked(
		commit, snapshot, true)
	identityChanged := err == nil && (p.status.CurrentWallet != previousWalletID ||
		p.status.CurrentAccount != previousAccount)
	p.mutex.Unlock()
	if identityChanged {
		if scopeErr := p.rgbManager.selectRGB11Scope(); scopeErr != nil {
			return pendingRemains, importManagedData,
				fmt.Errorf("select synchronized RGB11 wallet scope: %w", scopeErr)
		}
		if scopeErr := p.rgbManager.rebuildRGB11Locks(); scopeErr != nil {
			return pendingRemains, importManagedData,
				fmt.Errorf("rebuild synchronized RGB11 locks: %w", scopeErr)
		}
	}
	if identityChanged {
		p.wakeChannelHeartbeat()
	}
	return pendingRemains, importManagedData, err
}

func (p *Manager) SyncAccountManagementState(ctx context.Context) error {
	if p == nil {
		return ErrDKVSPathNotSynced
	}
	err := p.runAccountApplicationSync(ctx, func() error {
		return p.syncAccountManagementState(ctx, 0, false)
	})
	if err == nil {
		p.cleanupAccountManagedActiveDataAfterDurable(rgb11AccountManagedProviderID)
	}
	return err
}

func (p *Manager) syncAccountManagementState(ctx context.Context, attempt int,
	authoritativeRebase bool) error {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	if err := p.checkAccountManagedDataImport(); err != nil {
		return err
	}
	if p.accountManagedRecoveryConfigured() {
		activeRGB11, err := p.hasAccountManagedRGB11Transition()
		if err != nil {
			return err
		}
		if activeRGB11 {
			// A partial stable export would either omit the scope or reuse its old
			// payload and could then clear the local dirty generation without ever
			// publishing the transition. Active recovery is authoritative until the
			// business operation settles; ordinary account PUTs are blocked here.
			return ErrRGB11ManagedOperationActive
		}
	}
	snapshot, err := p.captureAccountManagementSyncSnapshot()
	if err != nil || snapshot == nil {
		return err
	}
	defer zeroBytes(snapshot.secret)

	root := snapshot.root
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
	if err := store.WaitReady(stateKey, dataKey); err != nil {
		return err
	}
	outboxPlan, err := p.accountManagedOutboxPlanFor(store, snapshot.profile, stateKey, dataKey)
	if err != nil {
		return err
	}
	if outboxPlan.Pending {
		p.markDKVSStateDirty()
		return ErrDKVSPathNotSynced
	}
	// WaitReady above establishes the initial local baseline. Once any durable
	// outbox has been ruled out, check the server's prefix generations before
	// reading that baseline. This is non-forced: an unchanged token never
	// downloads a snapshot, while another device's committed write does.
	if err := store.SyncCurrent(stateKey, dataKey); err != nil {
		return err
	}

	readValue := store.Get
	if authoritativeRebase {
		readValue = store.GetAuthoritative
	}
	stateValue, stateErr := readValue(stateKey)
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
	remoteBaselineChanged := !usingLocalState &&
		!bytes.Equal(remoteStateEnvelope, snapshot.profile.StateEnvelope)

	var dataValue *dkvsValue
	if remoteState.DataRevision != 0 && !usingLocalState {
		dataValue, err = readValue(dataKey)
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
		origin := outboxPlan.origin(stateKey, snapshot.profile.ManagedDataGeneration)
		origin.PreservePrefixGenerations = authoritativeRebase
		update := store.updateWithOutboxOrigin
		if authoritativeRebase {
			update = store.updateAuthoritativeWithOutboxOrigin
		}
		ackValues, updateErr := update([]string{stateKey, dataKey}, func(current map[string]*dkvsValue,
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
			mutations, mutationErr := accountManagementMutations(&snapshot.profile, root, stateKey,
				finalStateEnvelope, dataKey, finalManaged.Envelope, writeData)
			if mutationErr != nil {
				return nil, mutationErr
			}
			return applyAccountManagedOutboxPlan(mutations,
				snapshot.profile.StorageMode, outboxPlan), nil
		}, origin)
		err = updateErr
		if err != nil {
			if attempt < 2 && (errors.Is(err, dkvsindexer.ErrWriteConflict) ||
				errors.Is(err, dkvsindexer.ErrInvalidSequence)) {
				return p.syncAccountManagementState(ctx, attempt+1, true)
			}
			postPlan, terminalErr := p.accountManagedOutboxPlanFor(store,
				snapshot.profile, stateKey, dataKey)
			if terminalErr != nil {
				return terminalErr
			}
			if postPlan.Pending {
				p.markDKVSStateDirty()
				return ErrDKVSPathNotSynced
			}
			return err
		}

		// Align the SDK's confirmed baseline from the signed server ACK. The DKVS
		// replica has already advanced its key state and removed this request from
		// the outbox. ACK handling must not import the request back into the live
		// wallet or enter the recovery-marker workflow.
		ackStateValue := accountManagedACKValue(ackValues, stateKey)
		if ackStateValue == nil || len(ackStateValue.Value) == 0 {
			return fmt.Errorf("account-managed state ACK is missing")
		}
		ackState, ackErr := account.OpenManagedState(snapshot.secret,
			snapshot.profile.AccountID, ackStateValue.Value)
		if ackErr != nil || ackState.RootFingerprint != snapshot.profile.RootFingerprint ||
			ackState.Revision != target.Revision {
			return fmt.Errorf("account-managed state ACK is invalid")
		}
		ackManaged := finalManaged
		if ackDataValue := accountManagedACKValue(ackValues, dataKey); ackDataValue != nil {
			ackManaged, ackErr = openAccountManagedDataValue(snapshot.secret,
				snapshot.profile.AccountID, ackDataValue, ackState)
			if ackErr != nil {
				return fmt.Errorf("account-managed data ACK is invalid: %w", ackErr)
			}
		}
		followUp, finalizeErr := p.finalizePublishedAccountManagedState(
			ackState, snapshot, ackStateValue.Value, ackManaged)
		if finalizeErr != nil {
			return finalizeErr
		}
		if followUp || remoteBaselineChanged {
			p.scheduleAccountManagedStateSync()
		}
		if err := p.refreshDKVSRegistrations(); err != nil {
			return err
		}
		p.scheduleAccountRootWrapperSync()
		return nil
	}

	if remoteBaselineChanged {
		pendingRemains, importManagedData, commitErr := p.commitAccountManagedStateForSync(
			target, snapshot, finalStateEnvelope, finalManaged)
		if commitErr != nil {
			if attempt < 2 && errors.Is(commitErr, errAccountSnapshotChanged) {
				return p.syncAccountManagementState(ctx, attempt+1, false)
			}
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
		// The crash marker protects only the non-atomic local import above. All
		// following work is retryable transport/cache maintenance and therefore
		// runs after the durable recovery boundary has closed.
		if err := p.db.Delete(accountManagedDataImportKey()); err != nil {
			return err
		}
	}
	if err := p.refreshDKVSRegistrations(); err != nil {
		return err
	}
	// The mailbox is a normal periodically refreshed account prefix. Import its
	// newest provider transition only after the stable account snapshot and
	// local registrations are ready.
	if err := p.importAccountManagedActiveData(); err != nil {
		return err
	}
	p.scheduleAccountRootWrapperSync()
	return nil
}
