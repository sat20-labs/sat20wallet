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
	"sync/atomic"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const (
	accountManagedStatePath     = "account/state"
	accountManagedStateJobID    = "account-managed-state"
	accountManagedBackupQuiet   = 10 * time.Second
	accountManagedBackupMaximum = time.Minute
)

var (
	ErrAccountManagementSecretConflict    = errors.New("account management secret conflicts with the active profile")
	ErrAccountManagementWalletUnavailable = errors.New("account management wallet is unavailable")
	ErrAccountStorageModeDowngrade        = errors.New("paid account storage cannot switch to temporary")
	errAccountSnapshotChanged             = errors.New("account management snapshot changed")
	managerDiagSeq                        atomic.Uint64
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
		if err := p.accountDBFailure(); err != nil {
			return err
		}
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

// runAccountApplicationSync coordinates the existing account transaction only.
// Identity/catalog commits use their local gates; the durable import marker
// prevents use of a partially restored account. Network validation must never
// inherit a lifecycle mutex from this outer operation.
func (p *Manager) runAccountApplicationSync(ctx context.Context, operation func() error) error {
	return p.runAccountOperation(ctx, operation)
}

// withAccountLocalState protects only local snapshot/apply work. Background
// transport and provider validation must run outside this gate.
func (p *Manager) withAccountLocalState(alreadyLocked bool, operation func() error) error {
	if alreadyLocked {
		return operation()
	}
	p.channelIdentityMu.Lock()
	release := p.beginRGB11ScopeChange()
	defer func() {
		release()
		p.channelIdentityMu.Unlock()
	}()
	return operation()
}

func (p *Manager) executeAccountOperation(done chan struct{}, operation func() error) (err error) {
	defer func() {
		err = p.stopAccountDB(err)
		p.accountSyncMu.Lock()
		if p.accountSyncDone == done {
			p.accountSyncActive = false
			p.accountSyncDone = nil
			close(done)
		}
		p.accountSyncMu.Unlock()
		if errors.Is(err, ErrAccountDBStopped) {
			p.dkvsInitMu.Lock()
			manager := p.dkvs
			p.dkvsInitMu.Unlock()
			if manager != nil {
				manager.wakeSync()
				manager.notifyStatusCallback()
			}
		}
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
	AccountState        string                `json:"account_state"`
	WriteAllowed        bool                  `json:"write_allowed"`
	BackupState         string                `json:"backup_state"`
	Modules             []AccountModuleStatus `json:"modules,omitempty"`
	Active              bool                  `json:"active"`
	RecoveryConfigured  bool                  `json:"recovery_configured"`
	LocalOnly           bool                  `json:"local_only"`
	DiscoveryStatus     string                `json:"discovery_status"`
	ActivationPending   bool                  `json:"activation_pending"`
	ManagedDataRevision uint64                `json:"managed_data_revision,omitempty"`
	ManagedDataDirty    bool                  `json:"managed_data_dirty,omitempty"`
	AccountID           string                `json:"account_id,omitempty"`
	PackageID           string                `json:"package_id,omitempty"`
	RecoveryMode        string                `json:"recovery_mode,omitempty"`
	StorageMode         string                `json:"storage_mode,omitempty"`
	PublicLocator       string                `json:"public_locator,omitempty"`
	RootFingerprint     string                `json:"root_fingerprint,omitempty"`
	// RootWalletID is only a process-local wallet handle. The stable root
	// identity persisted by account management is AccountID (the BIP340
	// x-only public key); callers must never persist or compare RootWalletID
	// across a restore, device or network manager.
	RootWalletID          int64  `json:"root_wallet_id,omitempty"`
	StateSeq              uint64 `json:"state_seq,omitempty"`
	PendingChanges        int    `json:"pending_changes,omitempty"`
	LastRehearsalAt       int64  `json:"last_rehearsal_at,omitempty"`
	DKVSStatus            string `json:"dkvs_status,omitempty"`
	DKVSCacheAtMS         uint64 `json:"dkvs_cache_at_ms,omitempty"`
	DKVSCacheHeight       uint64 `json:"dkvs_cache_height,omitempty"`
	DKVSWriteAllowed      bool   `json:"dkvs_write_allowed"`
	DKVSPendingRequests   int    `json:"dkvs_pending_requests,omitempty"`
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
	Index               account.ManagedIndex
	Copies              []AccountKVCopy
	State               account.ManagedState
	Seq                 uint64
	Hash                string
	Envelope            []byte
	ManagedData         account.ManagedDataBundle
	ManagedDataHash     string
	ManagedDataEnvelope []byte
}

type AccountKVCopy struct {
	Entry  account.ManagedEntry
	Value  []byte
	Record *swire.DKVSRecord
}

func validateRecoveredAccountManagementState(value RecoveredAccountManagementState,
	secret []byte, locator account.Locator) (RecoveredAccountManagementState, error) {
	return validateAccountCopies(value, secret, locator)
}

func (p *Manager) GetAccountManagementStatus() AccountManagementStatus {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if p.accountProfile == nil {
		service, _ := p.accountServiceStateLocked()
		result := AccountManagementStatus{LocalOnly: service == "inactive", DiscoveryStatus: service}
		p.fillAccountStatus(&result)
		return result
	}
	service, _ := p.accountServiceStateLocked()
	result := AccountManagementStatus{
		Active: true, RecoveryConfigured: p.accountProfile.RecoveryConfigured,
		LocalOnly:           service == "inactive",
		DiscoveryStatus:     service,
		ActivationPending:   p.accountProfile.Activation != nil,
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
		if state, err := p.GetDKVSSubscriptionStatus(); err == nil {
			result.DKVSStatus = state.Status
			result.DKVSCacheAtMS = state.LastSyncAtMS
			result.DKVSCacheHeight = state.ViewHeight
		}
		result.DKVSWriteAllowed = p.accountProfile.RecoveryConfigured &&
			p.requireAccountManagedWriteLocked() == nil
		if p.db == nil {
			result.DKVSWriteAllowed = false
		} else if entries, err := newDKVSReplicaStore(p.db).LoadOutbox(p.dkvsReplicaNamespace()); err == nil {
			result.DKVSPendingRequests = len(entries)
		} else {
			result.DKVSWriteAllowed = false
		}
		result.LastDKVSSyncErrorCode, result.LastDKVSSyncError, result.LastDKVSSyncErrorAt = p.dkvs.lastSyncErrorStatus()
	}
	p.fillAccountStatus(&result)
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

	if info == nil || info.Wallet == nil {
		return account.ManagedWallet{}, fmt.Errorf("unlocked wallet is required")
	}
	secret, err := p.decryptWalletSecretBytes(&info.WalletInDB, password)
	if err != nil {
		return account.ManagedWallet{}, err
	}
	defer zeroBytes(secret)
	typ, material, err := accountRecordMaterial(info.Type, secret)
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
		Fingerprint: walletFingerprint(info.Wallet),
		Name:        info.Name, Type: typ, Secret: material, AccountCount: uint32(info.Accounts),
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
		Version: account.ManagedStateVersion, RootFingerprint: rootFingerprint,
		Wallets: make([]account.ManagedWallet, 0, len(infos)),
	}
	for _, info := range infos {
		item, err := p.managedWalletFromInfoLocked(info, password, 0)
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
	secret, err := deriveAccountBackupSecret(root, GetChainParam_SatsNet().Name, accountID)
	if err != nil {
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
		StateSeq: 0, StateHash: accountStateDigest(envelope),
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
	immutableSecret := append([]byte(nil), secret...)
	defer zeroBytes(immutableSecret)
	return p.runAccountOperation(nil, func() error {
		return p.activateAccountManagement(immutableSecret, password, authorization, locator, publicLocator)
	})
}

func (p *Manager) activateAccountManagement(secret []byte, password string,
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
	if err := p.InitializeAccountManagement(password); err != nil {
		return err
	}
	p.mutex.RLock()
	pending := p.accountProfile != nil && p.accountProfile.Activation != nil
	matches := !pending || activationTargetMatches(p.accountProfile, authorization, locator, publicLocator)
	p.mutex.RUnlock()
	if !matches {
		return fmt.Errorf("pending account activation targets a different recovery configuration: %w", errAccountSnapshotChanged)
	}
	if !pending {
		if err := p.prepareAccountActivation(secret, password, authorization, locator, publicLocator); err != nil {
			return err
		}
	}
	_, err := p.resumePendingActivation(secret, password)
	return err
}

// validateAccountActivationSecretLocked prevents a recovery-package session
// from silently replacing the AccountSecret of an already active local
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
		return nil, fmt.Errorf("invalid account root wallet")
	}
	root.SetSubAccount(0)
	store, err := p.accountDKVSStoreForLocation(location)
	if err != nil {
		return nil, err
	}
	return p.loadAccountCopies(store, root, locator, secret)
}

func (p *Manager) RestoreAccountManagementState(value RecoveredAccountManagementState,
	secret []byte, password string, locator account.Locator,
	options AccountManagementRestoreOptions) ([]RestoredWalletResult, error) {
	results, err := p.restoreAccountManagementState(value, secret, password, locator, options, "")
	if err != nil {
		return results, err
	}
	p.finishAccountManagementRestorePostSync()
	return results, nil
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
	return p.restoreAccountCopies(value, secret, password, locator, options, allowedImportedRoot)
}

func (p *Manager) finishAccountManagementRestorePostSync() {
	defer p.scheduleAccountManagedStateSync()
	// Core wallet/profile/provider recovery is already durable and its crash
	// marker is closed. Registration and active-mailbox refresh are retryable
	// network maintenance: a transport failure must not turn a completed local
	// restore into an API failure which cannot safely be retried from an empty DB.
	if err := p.refreshDKVSRegistrations(); err != nil {
		Log.Warningf("post-restore DKVS registration refresh failed: %v", err)
		p.wakeChannelHeartbeat()
		return
	}
	if err := p.importAccountManagedActiveDataForSync(false); err != nil {
		Log.Warningf("post-restore active account data refresh failed: %v", err)
	}
	p.wakeChannelHeartbeat()
}

func randomAccountMutationID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

// Explicit retry and backup waits use the existing worker without the normal
// business-change quiet interval. The worker owns its own request context.
func (p *Manager) requestAccountSync() {
	if p == nil || p.accountDBFailure() != nil {
		return
	}
	p.ensureDKVSManager().schedule(accountManagedStateJobID, func(_ *dkvsStore) error {
		return p.refreshAccountService()
	})
}

func (p *Manager) scheduleAccountManagedStateSync() {
	if p == nil || p.accountDBFailure() != nil {
		return
	}
	manager := p.ensureDKVSManager()
	manager.scheduleCoalesced(accountManagedStateJobID, accountManagedBackupQuiet, accountManagedBackupMaximum, func(_ *dkvsStore) error {
		return p.refreshAccountService()
	})
}

func (p *Manager) queueAccountMutationLocked(mutation accountManagementMutation) error {
	profile, err := p.prepareAccountMutationLocked(mutation)
	if err != nil {
		return err
	}
	if profile != nil {
		encoded, err := EncodeToBytes(profile)
		if err != nil {
			return err
		}
		if err := p.db.Write(accountManagementProfileKey(), encoded); err != nil {
			return err
		}
		p.accountProfile = profile
		Log.Debugf("account generation advanced: source=catalog activation_pending=%t data_generation=%d",
			profile.ManagedEntered && !profile.RecoveryConfigured, profile.ManagedDataGeneration)
		p.scheduleAccountManagedStateSync()
	}
	p.bumpAccountGenerationLocked()
	return nil
}

func (p *Manager) prepareAccountMutationLocked(mutation accountManagementMutation) (*accountManagementProfile, error) {
	if err := p.prepareAccountWriteLocked(); err != nil {
		return nil, err
	}
	if p.accountProfile == nil {
		return nil, nil
	}
	// Persist an owned overlay before making it visible to background sync.
	profile := *p.accountProfile
	profile.Pending = append([]accountManagementMutation(nil), profile.Pending...)
	profile.ManagedDataDirty = true
	profile.ManagedDataGeneration++
	if profile.ManagedDataGeneration == 0 {
		profile.ManagedDataGeneration = 1
	}
	if mutation.ID == "" {
		id, err := randomAccountMutationID()
		if err != nil {
			return nil, err
		}
		mutation.ID = id
	}
	for index := len(profile.Pending) - 1; index >= 0; index-- {
		current := &profile.Pending[index]
		if current.Fingerprint != mutation.Fingerprint {
			continue
		}
		if current.Type == accountMutationAddWallet &&
			mutation.Type != accountMutationDeleteWallet {
			// An in-flight PUT may contain the previous add snapshot. Keep one
			// add, but give the updated wallet a new overlay identity so its old
			// ACK cannot clear metadata edited during transport.
			id := mutation.ID
			mutation = *current
			mutation.ID = id
		} else {
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
		}
		*current = mutation
		return &profile, nil
	}
	profile.Pending = append(profile.Pending, mutation)
	return &profile, nil
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

type accountManagementSyncSnapshot struct {
	profile    accountManagementProfile
	generation uint64
	secret     []byte
	password   string
	root       common.Wallet
	wallets    map[string]account.ManagedWallet
	pending    []accountManagementMutation
	network    string
}

// Account management is anchored to the root wallet/account, not the wallet or
// subaccount currently selected by the UI. Only root identity/configuration and
// network changes invalidate an in-flight account synchronization snapshot.
func (p *Manager) accountSyncSnapshotCurrentLocked(snapshot *accountManagementSyncSnapshot) bool {
	return snapshot != nil && p.accountProfile != nil &&
		p.accountProfile.AccountID == snapshot.profile.AccountID &&
		p.accountProfile.RootFingerprint == snapshot.profile.RootFingerprint &&
		p.accountGeneration == snapshot.generation && _chain == snapshot.network
}

func cloneManagedWallet(value account.ManagedWallet) account.ManagedWallet {
	value.SubAccounts = append([]account.SubAccount(nil), value.SubAccounts...)
	value.Secret = append([]byte(nil), value.Secret...)
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
		left.Type != right.Type || !bytes.Equal(left.Secret, right.Secret) || left.AccountCount != right.AccountCount ||
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
		network:    _chain,
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
	clone.SPEnabled = make(map[uint32]bool, len(info.SPEnabled))
	for key, enabled := range info.SPEnabled {
		if enabled {
			clone.SPEnabled[key] = true
		}
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
	walletValue, typ, localSecret, err := accountWalletMaterial(remote.Type, remote.Secret)
	defer zeroBytes(localSecret)
	if err != nil || walletValue == nil || walletFingerprint(walletValue) != remote.Fingerprint {
		return nil, fmt.Errorf("invalid managed wallet %s", remote.Fingerprint)
	}
	info := &WalletInfo{WalletInDB: WalletInDB{
		Id: walletValue.GetId(), Accounts: int(remote.AccountCount), Type: typ,
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
	info.Mnemonic, err = key.Encrypt(localSecret)
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

func (p *Manager) SyncAccountManagementState(ctx context.Context) error {
	if p == nil {
		return ErrDKVSPathNotSynced
	}
	p.mutex.RLock()
	configured := p.accountProfile != nil && p.accountProfile.RecoveryConfigured
	p.mutex.RUnlock()
	if !configured {
		return ErrDKVSPathNotSynced
	}
	err := p.runAccountOperation(ctx, func() error {
		return p.syncAccountModules(ctx)
	})
	if err == nil {
		p.cleanupAccountManagedActiveDataAfterDurable(rgb11AccountManagedProviderID)
	}
	return err
}

func (p *Manager) syncAccountManagementState(ctx context.Context, attempt int,
	authoritativeRebase bool) error {
	return p.syncAccountManagementStateMode(ctx, attempt, authoritativeRebase, false)
}

func (p *Manager) syncAccountManagementStateMode(ctx context.Context, attempt int,
	authoritativeRebase, applicationLocked bool) (resultErr error) {
	return p.syncAccountModules(ctx)
}

// Caller owns p.mutex. Account management alone interprets service state.
// This local check never refreshes, waits, finalizes an ACK, or requires clean
// backup state. An active account needs an online verified state/data baseline.
func (p *Manager) requireAccountManagedWriteLocked() error {
	if err := p.accountDBFailure(); err != nil {
		return err
	}
	if err := p.checkAccountManagedDataImport(); err != nil {
		return err
	}
	if p.accountProfile == nil || !p.accountProfile.RecoveryConfigured {
		service, discoveryErr := p.accountServiceStateLocked()
		if discoveryErr != nil {
			return discoveryErr
		}
		if service == "inactive" {
			return nil
		}
		return ErrDKVSPathNotSynced
	}
	if p.dkvs == nil {
		Log.Debugf("account managed write rejected: phase=no_manager")
		return ErrDKVSPathNotSynced
	}
	client, err := p.dkvs.primaryClient()
	if err != nil {
		Log.Debugf("account managed write rejected: phase=primary_client")
		return err
	}
	info, err := p.accountManagementRootWalletLocked()
	if err != nil {
		return err
	}
	root := cloneWalletAtAccountZero(info.Wallet)
	stateKey, err := accountRootWrapperKey(root)
	if err != nil {
		return err
	}
	dataKey, err := account.ManagedIndexKey(p.accountProfile.AccountID)
	if err != nil {
		return err
	}
	if err := p.dkvs.requireOnlineGroup(client, []string{stateKey, dataKey}); err != nil {
		Log.Debugf("account managed write rejected: phase=online_group manager=%d scope_tag=%s dirty=%t pending=%d generation=%d read_ready=%t write_ready=%t account_ready=%t",
			p.debugID, accountTraceScope(p.dkvs.sessionKey(client, "account-write:"+p.accountProfile.AccountID)),
			p.accountProfile.ManagedDataDirty, len(p.accountProfile.Pending), p.accountProfile.ManagedDataGeneration,
			p.dkvs.scopeReady(p.dkvs.sessionKey(client, "read")), p.dkvs.scopeReady(p.dkvs.sessionKey(client, "write")), p.dkvs.scopeReady(p.accountWriteScopeLocked()))
		return err
	}
	if err := p.requireAccountSyncLocked(client, stateKey, dataKey); err != nil {
		return err
	}
	if err := p.requireAccountManagedBaselineLocked(client); err != nil {
		Log.Debugf("account managed write rejected: phase=confirmed_baseline dirty=%t pending=%d generation=%d",
			p.accountProfile.ManagedDataDirty, len(p.accountProfile.Pending),
			p.accountProfile.ManagedDataGeneration)
		return err
	}
	return nil
}
