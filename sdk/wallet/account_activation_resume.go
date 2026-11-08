package wallet

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func (p *Manager) registerActivationPaths(keys ...string) error {
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		prefix, _, err := dkvsManagedPathForKey(key)
		if err != nil {
			return err
		}
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		if err := p.ensureOwnedDKVSPrefix(prefix); err != nil {
			return err
		}
	}
	return nil
}

func activationTargetMatches(profile *accountManagementProfile,
	auth AccountStorageAuthorization, locator account.Locator, publicLocator string) bool {
	if profile == nil || profile.Activation == nil {
		return false
	}
	target := profile.Activation
	autopay := ""
	if auth.Autopay != nil {
		autopay = auth.Autopay.PoolContract
	}
	return target.AccountID == locator.AccountID && target.PackageID == locator.PackageID &&
		target.RecoveryMode == locator.RecoveryMode && target.StorageMode == auth.Mode &&
		target.Location == auth.Location && target.RecordTTL == auth.RecordOptions.TTL &&
		target.PublicLocator == publicLocator && target.AutopayContract == autopay
}

func applyActivationTarget(profile *accountManagementProfile, target *accountActivationIntent) {
	profile.PackageID, profile.RecoveryMode = target.PackageID, target.RecoveryMode
	profile.StorageMode, profile.Location = target.StorageMode, target.Location
	profile.RecordTTL, profile.AutopayContract = target.RecordTTL, target.AutopayContract
	profile.PublicLocator = target.PublicLocator
}

// Only immutable account identity and the chosen service configuration own an
// activation request. A normal catalog or RGB edit cannot invalidate it.
func (p *Manager) activationIdentityLocked(target *accountActivationIntent,
	secret []byte, password string) error {
	if target == nil || p.accountProfile == nil || p.accountProfile.Activation == nil ||
		p.accountProfile.AccountID != target.AccountID ||
		p.accountProfile.RootFingerprint != target.RootFingerprint || _chain != target.Network || _env != target.Environment ||
		!bytes.Equal(p.accountSecret, secret) || p.accountPassword != password ||
		!bytes.Equal(p.accountProfile.Activation.Wrapper, target.Wrapper) {
		return errAccountSnapshotChanged
	}
	location, err := p.AccountIndexerLocation()
	if err != nil || location != target.Location {
		return errAccountSnapshotChanged
	}
	root, err := p.accountManagementRootWalletLocked()
	if err != nil || walletFingerprint(root.Wallet) != target.RootFingerprint {
		return ErrAccountManagementWalletUnavailable
	}
	return p.checkAccountManagedDataImport()
}

func (p *Manager) prepareAccountActivation(secret []byte, password string,
	authorization AccountStorageAuthorization, locator account.Locator, publicLocator string) error {
	p.mutex.RLock()
	if p.accountProfile == nil {
		p.mutex.RUnlock()
		return ErrDKVSPathNotSynced
	}
	previous := *p.accountProfile
	if previous.AccountID != locator.AccountID || !bytes.Equal(p.accountSecret, secret) || p.accountPassword != password {
		p.mutex.RUnlock()
		return ErrAccountManagementSecretConflict
	}
	if err := p.checkAccountManagedDataImport(); err != nil {
		p.mutex.RUnlock()
		return err
	}
	service, discoveryErr := p.accountServiceStateLocked()
	if discoveryErr != nil {
		p.mutex.RUnlock()
		return discoveryErr
	}
	if !previous.RecoveryConfigured && service != "inactive" {
		p.mutex.RUnlock()
		return ErrRootAccountDiscoveryPending
	}
	rootInfo, err := p.accountManagementRootWalletLocked()
	if err != nil {
		p.mutex.RUnlock()
		return err
	}
	root := cloneWalletAtAccountZero(rootInfo.Wallet)
	network, environment := _chain, _env
	p.mutex.RUnlock()
	location, err := p.AccountIndexerLocation()
	if err != nil || location != authorization.Location {
		return errAccountSnapshotChanged
	}
	if previous.StorageMode == AccountStoragePaid && authorization.Mode == AccountStorageTemporary {
		return ErrAccountStorageModeDowngrade
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return err
	}
	wrapperKey, err := accountRootWrapperKey(root)
	if err != nil {
		return err
	}
	stateKey, err := p.accountManagedStateKey(root)
	if err != nil {
		return err
	}
	dataKey, err := account.ManagedIndexKey(previous.AccountID)
	if err != nil {
		return err
	}
	keys := []string{wrapperKey, stateKey, dataKey}
	if err := p.registerActivationPaths(keys...); err != nil {
		return err
	}
	values, err := store.readSnapshot(keys...)
	if err != nil {
		return err
	}
	current := values[wrapperKey]
	if !previous.RecoveryConfigured && current != nil {
		// Another device already opened this service. Missing private state is
		// a wait-for-backup condition, never permission to initialize it again.
		return ErrRootAccountDiscoveryPending
	}
	if !previous.RecoveryConfigured && (values[stateKey] != nil || values[dataKey] != nil) {
		return dkvsindexer.ErrWriteConflict
	}
	baseHash := ""
	if current != nil {
		payload, err := openAccountRootWrapper(root, network, previous.AccountID, current.Value)
		if err != nil {
			return err
		}
		matches := bytes.Equal(payload.Secret, secret) && accountRootWrapperMetadataMatchesProfile(payload, previous)
		zeroBytes(payload.Secret)
		if !matches {
			return dkvsindexer.ErrWriteConflict
		}
		baseHash = current.Hash
	} else if previous.RecoveryConfigured {
		return ErrDKVSPathNotSynced
	}
	target := &accountActivationIntent{AccountID: previous.AccountID,
		RootFingerprint: previous.RootFingerprint, Network: network, Environment: environment,
		PackageID: locator.PackageID, RecoveryMode: locator.RecoveryMode,
		StorageMode: authorization.Mode, Location: authorization.Location,
		RecordTTL: authorization.RecordOptions.TTL, PublicLocator: publicLocator,
		BaseWrapperHash: baseHash, Initial: !previous.RecoveryConfigured}
	if authorization.Autopay != nil {
		target.AutopayContract = authorization.Autopay.PoolContract
	}
	configured := previous
	applyActivationTarget(&configured, target)
	configured.RecoveryConfigured = true
	payload := rootWrapperPayload(configured, secret)
	defer zeroBytes(payload.Secret)
	if locator.RecoveryMode == account.RecoveryMode2Of3 && publicLocator != "" {
		public, err := DecodeAccountPublicLocator(publicLocator, network)
		if err != nil {
			return err
		}
		if public.Locator != locator {
			return ErrRootAccountWrapperInvalid
		}
		payload.GuardianLocation = public.GuardianLocation
	}
	target.Wrapper, err = sealAccountRootWrapper(root, network, target.AccountID, payload, nil)
	if err != nil {
		return err
	}
	return p.withAccountLocalState(false, func() error {
		p.mutex.Lock()
		defer p.mutex.Unlock()
		live := p.accountProfile
		if live == nil || live.AccountID != previous.AccountID || live.RootFingerprint != previous.RootFingerprint ||
			live.Activation != nil || _chain != network || _env != environment || !bytes.Equal(p.accountSecret, secret) || p.accountPassword != password ||
			live.PackageID != previous.PackageID || live.RecoveryMode != previous.RecoveryMode ||
			live.RecordTTL != previous.RecordTTL || live.StorageMode != previous.StorageMode ||
			live.AutopayContract != previous.AutopayContract || live.Location != previous.Location ||
			live.PublicLocator != previous.PublicLocator || live.RecoveryConfigured != previous.RecoveryConfigured {
			return errAccountSnapshotChanged
		}
		// Recheck in the local publication gate before persisting a new intent.
		if err := p.checkAccountManagedDataImport(); err != nil {
			return err
		}
		staged := *live
		staged.ManagedEntered, staged.Activation = true, target
		encoded, err := EncodeToBytes(&staged)
		if err != nil {
			return err
		}
		batch, err := NewAtomicWriteBatch(p.db)
		if err != nil {
			return err
		}
		defer batch.Close()
		if err := p.saveAccountKVLocked(batch, target.AccountID, wrapperKey, target.Wrapper); err != nil {
			return err
		}
		if err := batch.Put(accountManagementProfileKey(), encoded); err != nil {
			return err
		}
		if err := batch.Flush(); err != nil {
			return err
		}
		p.accountProfile = &staged
		p.bumpAccountGenerationLocked()
		return nil
	})
}

// Service activation publishes only the immutable recovery/service wrapper.
// Private state/data are independently backed up by the normal account job.
// The persisted exact envelope and ordinary outbox survive an uncertain ACK.
func (p *Manager) resumePendingActivation(secret []byte, password string) (bool, error) {
	p.mutex.RLock()
	if p.accountProfile == nil || p.accountProfile.Activation == nil {
		p.mutex.RUnlock()
		return false, nil
	}
	target := *p.accountProfile.Activation
	target.Wrapper = bytes.Clone(target.Wrapper)
	err := p.activationIdentityLocked(&target, secret, password)
	rootInfo, rootErr := p.accountManagementRootWalletLocked()
	var root = p.wallet
	if rootErr == nil {
		root = cloneWalletAtAccountZero(rootInfo.Wallet)
	}
	p.mutex.RUnlock()
	if err != nil {
		return true, err
	}
	if rootErr != nil {
		return true, rootErr
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return true, err
	}
	wrapperKey, err := accountRootWrapperKey(root)
	if err != nil {
		return true, err
	}
	stateKey, err := p.accountManagedStateKey(root)
	if err != nil {
		return true, err
	}
	dataKey, err := account.ManagedIndexKey(target.AccountID)
	if err != nil {
		return true, err
	}
	keys := []string{wrapperKey, stateKey, dataKey}
	if err := p.registerActivationPaths(keys...); err != nil {
		return true, err
	}
	if err := store.WaitReady(keys...); err != nil {
		return true, err
	}
	if err := store.SyncCurrent(keys...); err != nil {
		return true, err
	}
	values, err := store.readSnapshot(keys...)
	if err != nil {
		return true, err
	}
	current := values[wrapperKey]
	published := current != nil && bytes.Equal(current.Value, target.Wrapper)
	if !published {
		actualHash := ""
		if current != nil {
			actualHash = current.Hash
		}
		if actualHash != target.BaseWrapperHash || target.Initial && (values[stateKey] != nil || values[dataKey] != nil) {
			return true, dkvsindexer.ErrWriteConflict
		}
		entries, err := newDKVSReplicaStore(p.db).LoadOutbox(store.client.replicaNamespace)
		if err != nil {
			return true, err
		}
		for _, entry := range entries {
			mutations, err := entry.DecodeMutations()
			if err != nil {
				return true, err
			}
			for _, mutation := range mutations {
				if mutation.Record != nil && mutation.Record.Key == wrapperKey {
					return true, ErrDKVSPathNotSynced
				}
			}
		}
		bind, err := p.prepareAccountCoreNodeBinding(root, store)
		if err != nil {
			return true, err
		}
		if err := bind(); err != nil {
			return true, err
		}
		profile := accountManagementProfile{AccountID: target.AccountID}
		applyActivationTarget(&profile, &target)
		_, err = store.Update(keys, func(observed map[string]*dkvsValue, _ map[string]uint64) ([]dkvsValueMutation, error) {
			hash := ""
			if observed[wrapperKey] != nil {
				hash = observed[wrapperKey].Hash
			}
			if hash != target.BaseWrapperHash || target.Initial && (observed[stateKey] != nil || observed[dataKey] != nil) {
				return nil, dkvsindexer.ErrWriteConflict
			}
			p.mutex.RLock()
			identityErr := p.activationIdentityLocked(&target, secret, password)
			p.mutex.RUnlock()
			if identityErr != nil {
				return nil, identityErr
			}
			mutation, err := accountRootWrapperMutation(&profile, root, wrapperKey, target.Wrapper)
			if err != nil {
				return nil, err
			}
			return []dkvsValueMutation{mutation}, nil
		})
		if err != nil {
			return true, fmt.Errorf("publish account service activation: %w", err)
		}
	}
	payload, err := openAccountRootWrapper(root, target.Network, target.AccountID, target.Wrapper)
	if err != nil {
		return true, err
	}
	defer zeroBytes(payload.Secret)
	if payload.PackageID != target.PackageID || !bytes.Equal(payload.Secret, secret) {
		return true, dkvsindexer.ErrWriteConflict
	}
	err = p.withAccountLocalState(false, func() error {
		p.mutex.Lock()
		defer p.mutex.Unlock()
		if err := p.activationIdentityLocked(&target, secret, password); err != nil {
			return err
		}
		updated := *p.accountProfile
		applyActivationTarget(&updated, &target)
		updated.RecoveryConfigured, updated.ManagedEntered = true, true
		updated.InitialBackupPending = updated.InitialBackupPending || target.Initial
		if updated.InitialBackupPending {
			updated.InitWrapperHash = accountStateDigest(target.Wrapper)
		}
		updated.Activation = nil
		updated.LastRehearsalAt = time.Now().UnixMilli()
		updated.ManagedDataGeneration++
		if updated.ManagedDataGeneration == 0 {
			return fmt.Errorf("account generation overflow")
		}
		updated.ManagedDataDirty = true
		encoded, err := EncodeToBytes(&updated)
		if err != nil {
			return err
		}
		if err := p.db.Write(accountManagementProfileKey(), encoded); err != nil {
			return err
		}
		p.accountProfile = &updated
		p.bumpAccountGenerationLocked()
		return nil
	})
	if err == nil {
		baseline, baselineErr := p.prepareAccountBaseline(context.Background())
		if baselineErr != nil {
			return true, baselineErr
		}
		zeroBytes(baseline.snapshot.secret)
		if p.dkvs != nil {
			p.dkvs.notifyStatusCallback()
		}
		p.markDKVSStateDirty()
		p.scheduleAccountManagedStateSync()
		if refreshErr := p.refreshDKVSRegistrations(); refreshErr != nil {
			return true, refreshErr
		}
	}
	return true, err
}

func (p *Manager) restoredCatalogResults() []RestoredWalletResult {
	var results []RestoredWalletResult
	for _, entry := range p.GetWalletCatalog() {
		result := RestoredWalletResult{ID: entry.ID, Name: entry.Name, Fingerprint: entry.Fingerprint}
		for _, sub := range entry.Accounts {
			result.Accounts = append(result.Accounts, RestoredSubAccountResult{
				Index: sub.Index, DID: sub.DID, Address: sub.Address, PubKey: sub.PubKey, AccountID: sub.AccountID,
			})
		}
		results = append(results, result)
	}
	return results
}
