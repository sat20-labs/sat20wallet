package wallet

import (
	"errors"
	"reflect"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// Restore the same immutable recovery package after expiry. No new package ID,
// questions, shares, guardian ciphertext or activation operation is generated.
func (p *Manager) syncAccountRecovery(store *dkvsStore, snapshot *accountManagementSyncSnapshot,
	config accountRootWrapperPayload) error {
	var key string
	var remote *dkvsValue
	locator := account.Locator{AccountID: snapshot.profile.AccountID}
	if config.PackageID != "" {
		var err error
		key, err = dkvsindexer.AccountPersonalKey(snapshot.profile.AccountID, accountPackagePath(config.PackageID))
		if err != nil {
			return err
		}
		if err := store.WaitReady(key); err != nil {
			return err
		}
		if err := store.SyncCurrent(key); err != nil {
			return err
		}
		remote, err = store.Get(key)
		if err != nil && !errors.Is(err, ErrDKVSRecordNotFound) {
			return err
		}
		var encoded []byte
		if remote != nil {
			encoded = remote.Value
		} else {
			p.mutex.RLock()
			local, readErr := p.readAccountLocal(snapshot.profile.AccountID, "account", key)
			p.mutex.RUnlock()
			if readErr != nil {
				return readErr
			}
			if local == nil {
				return ErrDKVSRecordNotFound
			}
			encoded = local.Value
		}
		pkg, err := account.DecodeRecoveryPackageStorage(encoded)
		if err != nil {
			return err
		}
		locator = pkg.Envelope.Locator
		if locator.AccountID != snapshot.profile.AccountID || locator.PackageID != config.PackageID {
			return ErrRootAccountWrapperInvalid
		}
	} else if snapshot.profile.PackageID != "" {
		return ErrRootAccountWrapperInvalid
	}
	// Adopt the verified wrapper target before checking or migrating its package.
	err := func() error {
		locatorText, err := buildAccountPublicLocator(snapshot.root, snapshot.network,
			snapshot.profile.Location, config, locator)
		if err != nil {
			return err
		}
		client, err := store.manager.primaryClient()
		if err != nil || client != store.client {
			return errAccountSnapshotChanged
		}
		p.mutex.Lock()
		defer p.mutex.Unlock()
		if !p.accountSyncSnapshotCurrentLocked(snapshot) || p.accountProfile.Activation != nil ||
			p.accountProfile.Location != snapshot.profile.Location || p.dkvs != store.manager {
			return errAccountSnapshotChanged
		}
		profile := *p.accountProfile
		profile.PackageID = config.PackageID
		profile.RecoveryMode = locator.RecoveryMode
		profile.PublicLocator = locatorText
		profile.StorageMode, profile.RecordTTL, profile.AutopayContract = config.StorageMode, config.RecordTTL, config.AutopayContract
		if profile.StorageMode == AccountStorageTemporary && snapshot.profile.StorageMode == AccountStoragePaid {
			return ErrAccountStorageModeDowngrade
		}
		if reflect.DeepEqual(p.accountProfile, &profile) {
			snapshot.profile = profile
			return nil
		}
		profileBytes, err := EncodeToBytes(&profile)
		if err != nil {
			return err
		}
		if err := p.db.Write(accountManagementProfileKey(), profileBytes); err != nil {
			return accountDBWriteError(err)
		}
		p.accountProfile = &profile
		snapshot.profile = profile
		return nil
	}()
	if err != nil || key == "" {
		return err
	}
	if remote == nil {
		p.mutex.RLock()
		local, readErr := p.readAccountLocal(snapshot.profile.AccountID, "account", key)
		p.mutex.RUnlock()
		if readErr != nil {
			return readErr
		}
		if local == nil || len(local.Value) == 0 {
			return ErrDKVSRecordNotFound
		}
		local, err = p.setAccountBaseline(local, nil)
		if err != nil {
			return err
		}
		if err := p.publishAccountValue(store, local, snapshot.root, snapshot.profile); err != nil {
			return err
		}
	} else {
		if err := p.saveAccountKV(snapshot.profile.AccountID, key, remote.Value); err != nil {
			return err
		}
		p.mutex.RLock()
		local, readErr := p.readAccountLocal(snapshot.profile.AccountID, "account", key)
		p.mutex.RUnlock()
		if readErr != nil {
			return readErr
		}
		if err := p.confirmAccountValue(store, snapshot, local, remote); err != nil {
			return err
		}
	}
	return nil
}
