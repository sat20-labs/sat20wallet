package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func (p *Manager) loadAccountCopies(store *dkvsStore, root common.Wallet,
	locator account.Locator, secret []byte) (*RecoveredAccountManagementState, error) {
	accountID, err := dkvsAccountID(root)
	if err != nil || locator.AccountID != accountID {
		return nil, ErrRootAccountWrapperInvalid
	}
	wrapperKey, err := accountRootWrapperKey(root)
	if err != nil {
		return nil, err
	}
	indexKey, err := account.ManagedIndexKey(accountID)
	if err != nil {
		return nil, err
	}
	if store != nil && store.manager != nil {
		store.manager.rememberPaths([]string{wrapperKey, indexKey})
	}
	if err := store.WaitReady(wrapperKey, indexKey); err != nil {
		return nil, err
	}
	if err := store.SyncCurrent(wrapperKey, indexKey); err != nil {
		return nil, err
	}
	wrapper, err := store.Get(wrapperKey)
	if err != nil || wrapper == nil {
		return nil, ErrRootAccountDiscoveryPending
	}
	config, err := openAccountRootWrapper(root, _chain, accountID, wrapper.Value)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(config.Secret)
	if !bytes.Equal(config.Secret, secret) || config.PackageID != locator.PackageID {
		return nil, ErrRootAccountWrapperInvalid
	}
	indexValue, err := store.Get(indexKey)
	if err != nil || indexValue == nil {
		return nil, ErrRootAccountDiscoveryPending
	}
	index, err := account.OpenManagedIndex(secret, accountID, _chain, indexValue.Value)
	if err != nil {
		return nil, err
	}
	packageKey, err := dkvsindexer.AccountPersonalKey(accountID, accountPackagePath(locator.PackageID))
	if err != nil {
		return nil, err
	}
	if err := store.WaitReady(packageKey); err != nil {
		return nil, err
	}
	if err := store.SyncCurrent(packageKey); err != nil {
		return nil, err
	}
	pkgValue, err := store.Get(packageKey)
	if err != nil || pkgValue == nil {
		return nil, ErrDKVSRecordNotFound
	}
	pkg, err := account.DecodeRecoveryPackageStorage(pkgValue.Value)
	if err != nil || pkg.Envelope.Locator != locator {
		return nil, ErrRootAccountWrapperInvalid
	}
	result := &RecoveredAccountManagementState{Index: index}
	for _, control := range []struct {
		key   string
		value *dkvsValue
	}{{wrapperKey, wrapper}, {indexKey, indexValue}, {packageKey, pkgValue}} {
		result.Copies = append(result.Copies, AccountKVCopy{Entry: account.ManagedEntry{Provider: "account", Scope: control.key, Key: control.key, Codec: "v1"},
			Value: bytes.Clone(control.value.Value), Record: cloneDKVSRecord(control.value.record)})
	}
	for _, entry := range index.Entries {
		if entry.Deleted {
			continue
		}
		if err := store.rememberAccountModule(accountID, entry); err != nil {
			return nil, err
		}
		if err := store.WaitReady(entry.Key); err != nil {
			return nil, err
		}
		if err := store.SyncCurrent(entry.Key); err != nil {
			return nil, err
		}
		value, err := store.Get(entry.Key)
		if err != nil || value == nil {
			if isDKVSNetworkFailure(err) {
				return nil, err
			}
			result.Copies = append(result.Copies, AccountKVCopy{Entry: entry})
			continue
		}
		result.Copies = append(result.Copies, AccountKVCopy{Entry: entry, Value: bytes.Clone(value.Value), Record: cloneDKVSRecord(value.record)})
		if entry.Provider == "wallets" && entry.Scope == AccountManagedDataGlobalScope {
			state, err := account.OpenManagedState(secret, accountID, value.Value)
			if err != nil || state.RootFingerprint != walletFingerprint(root) {
				return nil, ErrRootAccountWrapperInvalid
			}
			result.State, result.Seq = state, value.Seq
			result.Envelope, result.Hash = bytes.Clone(value.Value), accountStateDigest(value.Value)
		}
	}
	if len(result.Envelope) == 0 {
		return nil, ErrRootAccountDiscoveryPending
	}
	return result, nil
}

func validateAccountCopies(value RecoveredAccountManagementState, secret []byte,
	locator account.Locator) (RecoveredAccountManagementState, error) {
	if len(secret) != 32 || value.Seq == 0 || len(value.Envelope) == 0 {
		return value, account.ErrInvalidBackup
	}
	state, err := account.OpenManagedState(secret, locator.AccountID, value.Envelope)
	if err != nil || !reflect.DeepEqual(state, value.State) || value.Hash != accountStateDigest(value.Envelope) {
		return value, account.ErrInvalidBackup
	}
	if err := validateAccountWalletState(state); err != nil {
		return value, err
	}
	index, err := account.NormalizeManagedIndex(locator.AccountID, value.Index)
	if err != nil {
		return value, err
	}
	indexKey, _ := account.ManagedIndexKey(locator.AccountID)
	rootItem := findManagedWallet(&state, state.RootFingerprint)
	if rootItem == nil || rootItem.Deleted || rootItem.Type != account.WalletMnemonic {
		return value, account.ErrInvalidBackup
	}
	root, _, material, err := accountWalletMaterial(rootItem.Type, rootItem.Secret)
	zeroBytes(material)
	if err != nil || root == nil || walletFingerprint(root) != state.RootFingerprint {
		return value, account.ErrInvalidBackup
	}
	rootID, err := dkvsAccountID(root)
	if err != nil || rootID != locator.AccountID {
		return value, account.ErrInvalidBackup
	}
	wrapperKey, err := accountRootWrapperKey(root)
	if err != nil {
		return value, err
	}
	packageKey, err := dkvsindexer.AccountPersonalKey(locator.AccountID, accountPackagePath(locator.PackageID))
	if err != nil {
		return value, err
	}
	stateKey, err := dkvsindexer.AccountPersonalKey(locator.AccountID, accountManagedStatePath)
	if err != nil {
		return value, err
	}
	allowed := map[string]account.ManagedEntry{}
	for _, key := range []string{indexKey, wrapperKey, packageKey} {
		allowed[key] = account.ManagedEntry{Provider: "account", Scope: key, Key: key, Codec: "v1"}
	}
	for _, entry := range index.Entries {
		if !entry.Deleted {
			allowed[entry.Key] = entry
		}
	}
	seen := make(map[string]bool)
	for _, copy := range value.Copies {
		if expected, ok := allowed[copy.Entry.Key]; !ok || expected != copy.Entry || seen[copy.Entry.Key] {
			return value, account.ErrInvalidBackup
		}
		seen[copy.Entry.Key] = true
		if copy.Record != nil && (copy.Record.Key != copy.Entry.Key || !bytes.Equal(copy.Record.Value, copy.Value)) {
			return value, account.ErrInvalidBackup
		}
		if copy.Entry.Key == indexKey {
			decoded, err := account.OpenManagedIndex(secret, locator.AccountID, _chain, copy.Value)
			if err != nil || !reflect.DeepEqual(decoded, index) {
				return value, account.ErrInvalidBackup
			}
		}
		if copy.Entry.Provider == "account" && (copy.Record == nil || copy.Record.Seq == 0 || len(copy.Value) == 0) {
			return value, account.ErrInvalidBackup
		}
		if copy.Entry.Key == wrapperKey {
			config, err := openAccountRootWrapper(root, _chain, locator.AccountID, copy.Value)
			valid := err == nil && bytes.Equal(config.Secret, secret) && config.PackageID == locator.PackageID
			zeroBytes(config.Secret)
			if !valid {
				return value, account.ErrInvalidBackup
			}
		}
		if copy.Entry.Key == packageKey {
			pkg, err := account.DecodeRecoveryPackageStorage(copy.Value)
			if err != nil || pkg.Envelope.Locator != locator {
				return value, account.ErrInvalidBackup
			}
		}
		if copy.Entry.Key == stateKey && (copy.Record == nil || copy.Record.Seq != value.Seq || !bytes.Equal(copy.Value, value.Envelope)) {
			return value, account.ErrInvalidBackup
		}
	}
	if !seen[indexKey] || !seen[wrapperKey] || !seen[packageKey] || !seen[stateKey] {
		return value, account.ErrInvalidBackup
	}
	for _, entry := range index.Entries {
		if !entry.Deleted && !seen[entry.Key] {
			return value, account.ErrInvalidBackup
		}
	}
	value.State, value.Index = state, index
	return value, nil
}

// Recovery pages authenticate an existing cold database with the supplied
// local password. An absent RAM session is not an account-secret mismatch.
// No data gate is held while the normal unlock lifecycle installs its runtime.
func (p *Manager) authenticateAccountRestore(accountID, fingerprint string,
	secret []byte, password string) (bool, error) {
	p.mutex.RLock()
	existing := p.accountProfile != nil && p.accountProfile.RecoveryConfigured
	matches := !existing || p.accountProfile.AccountID == accountID &&
		p.accountProfile.RootFingerprint == fingerprint
	p.mutex.RUnlock()
	if !matches || len(secret) != 32 {
		return existing, ErrAccountManagementSecretConflict
	}
	if !existing {
		return false, nil
	}
	_, err := p.UnlockWallet(password)
	if err != nil && !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
		return true, err
	}
	p.mutex.RLock()
	matches = p.accountProfile != nil && p.accountProfile.RecoveryConfigured &&
		p.accountProfile.AccountID == accountID && p.accountProfile.RootFingerprint == fingerprint &&
		p.wallet != nil && p.accountPassword == password && len(p.accountSecret) == 32 &&
		bytes.Equal(p.accountSecret, secret)
	p.mutex.RUnlock()
	if !matches {
		if err != nil {
			return true, err
		}
		return true, ErrAccountManagementSecretConflict
	}
	return true, nil
}

func (p *Manager) restoreAccountCopies(value RecoveredAccountManagementState, secret []byte,
	password string, locator account.Locator, options AccountManagementRestoreOptions,
	allowedRoot string) ([]RestoredWalletResult, error) {
	validated, err := validateAccountCopies(value, secret, locator)
	if err != nil {
		return nil, err
	}
	value = validated
	existing, err := p.authenticateAccountRestore(locator.AccountID, value.State.RootFingerprint, secret, password)
	if err != nil {
		return nil, err
	}
	if existing {
		if err := p.syncAccountModules(context.Background()); err != nil && !accountModuleOnly(err) {
			return nil, err
		}
		return p.restoredCatalogResults(), nil
	}
	backup, err := account.BackupFromManagedState(value.State)
	if err != nil {
		return nil, err
	}
	defer clearAccountBackup(&backup)
	prepared, err := p.prepareAccountRestoreWithRootLocked(backup, password, allowedRoot)
	if err != nil {
		return nil, err
	}
	rootInfo := walletInfoByFingerprintInMap(prepared.wallets, value.State.RootFingerprint)
	if rootInfo == nil || rootInfo.Wallet == nil {
		return nil, account.ErrInvalidBackup
	}
	root := cloneWalletAtAccountZero(rootInfo.Wallet)
	wrapperKey, err := accountRootWrapperKey(root)
	if err != nil {
		return nil, err
	}
	var config accountRootWrapperPayload
	for _, copy := range value.Copies {
		if copy.Entry.Key == wrapperKey {
			config, err = openAccountRootWrapper(root, _chain, locator.AccountID, copy.Value)
			if err != nil {
				return nil, err
			}
			break
		}
	}
	defer zeroBytes(config.Secret)
	if !bytes.Equal(config.Secret, secret) {
		return nil, ErrRootAccountWrapperInvalid
	}
	publicLocator, err := buildAccountPublicLocator(root, _chain, options.Location, config, locator)
	if err != nil {
		return nil, err
	}
	secretCipher, secretSalt, err := p.encryptAccountManagementSecret(password, secret)
	if err != nil {
		return nil, err
	}
	device, err := p.newAccountManagementDeviceID()
	if err != nil {
		return nil, err
	}
	profile := &accountManagementProfile{Version: accountManagementProfileVersion, RootFingerprint: value.State.RootFingerprint,
		AccountID: locator.AccountID, PackageID: locator.PackageID, RecoveryMode: locator.RecoveryMode,
		StorageMode: config.StorageMode, Location: options.Location, RecordTTL: config.RecordTTL, AutopayContract: config.AutopayContract,
		PublicLocator: publicLocator, SecretCipher: secretCipher, SecretSalt: secretSalt, DeviceID: device,
		StateSeq: value.Seq, StateHash: value.Hash, StateEnvelope: bytes.Clone(value.Envelope), RecoveryConfigured: true, ManagedEntered: true}
	for _, copy := range value.Copies {
		local := &accountLocalKV{Version: 1, AccountID: locator.AccountID, Network: p.cfg.Chain, Provider: copy.Entry.Provider,
			Scope: copy.Entry.Scope, Key: copy.Entry.Key, Codec: copy.Entry.Codec, Value: bytes.Clone(copy.Value),
			Baseline: cloneDKVSRecord(copy.Record), Generation: 1, Captured: 1, Confirmed: 1}
		if copy.Entry.Provider != "account" && copy.Entry.Provider != "wallets" {
			local.Restoring = true
		}
		if len(copy.Value) == 0 {
			local.Captured, local.Confirmed, local.LastError = 0, 0, "account module is missing remotely"
		}
		prepared.copies = append(prepared.copies, local)
	}
	err = p.withAccountLocalState(false, func() error {
		p.mutex.Lock()
		defer p.mutex.Unlock()
		if err := p.persistPreparedAccountRestoreLocked(prepared, profile); err != nil {
			return err
		}
		zeroBytes(p.accountSecret)
		p.accountSecret, p.accountPassword = bytes.Clone(secret), password
		return nil
	})
	if err != nil {
		return nil, err
	}
	catalog, err := p.accountManagedDataCatalog()
	if err != nil {
		return nil, err
	}
	for _, copy := range value.Copies {
		if copy.Entry.Provider == "account" || copy.Entry.Provider == "wallets" {
			continue
		}
		payload, err := openAccountPayload(secret, locator.AccountID, _chain, copy.Entry, copy.Value)
		if err == nil {
			p.mutex.RLock()
			local, readErr := p.readAccountLocal(locator.AccountID, copy.Entry.Provider, copy.Entry.Scope)
			p.mutex.RUnlock()
			if readErr != nil {
				return nil, readErr
			}
			remote := &dkvsValue{Value: copy.Value, record: copy.Record}
			err = p.importAccountValue(copy.Entry, catalog, local, remote, copy.Value, payload, false)
			zeroBytes(payload)
		}
		if err != nil {
			if recordErr := p.accountModuleFailure(copy.Entry, err, nil); !accountModuleOnly(recordErr) {
				return nil, recordErr
			}
		}
	}
	if err := p.rgbManager.selectRGB11Scope(); err != nil {
		return nil, fmt.Errorf("select restored account scope: %w", err)
	}
	if err := p.rgbManager.rebuildRGB11Locks(); err != nil {
		return nil, err
	}
	return prepared.results, nil
}
