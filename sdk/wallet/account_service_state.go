package wallet

import (
	"bytes"
	"context"
	"errors"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
)

// Discovery authorization is runtime-only: a reopened device must establish
// a new online session before cached absence or a cached backup permits writes.
func (p *Manager) accountDiscoveryRootLocked() (common.Wallet, error) {
	if p.accountProfile != nil {
		info, err := p.accountManagementRootWalletLocked()
		if err != nil {
			return nil, err
		}
		return cloneWalletAtAccountZero(info.Wallet), nil
	}
	info := p.firstWalletLocked()
	if info == nil || info.Wallet == nil {
		return nil, ErrAccountManagementWalletUnavailable
	}
	return cloneWalletAtAccountZero(info.Wallet), nil
}

// Caller owns the profile gate. Only the verified dependency-group replica is
// read here. Network work belongs to the existing background account task.
func (p *Manager) accountServiceStateLocked() (string, error) {
	if p.dkvs == nil {
		return "unknown", ErrDKVSPathNotSynced
	}
	root, err := p.accountDiscoveryRootLocked()
	if err != nil {
		return "unknown", err
	}
	client, err := p.dkvs.primaryClient()
	if err != nil {
		return "unknown", err
	}
	wrapperKey, err := accountRootWrapperKey(root)
	if err != nil {
		return "unknown", err
	}
	stateKey, err := p.accountManagedStateKey(root)
	if err != nil {
		return "unknown", err
	}
	accountID, err := dkvsAccountID(root)
	if err != nil {
		return "unknown", err
	}
	dataKey, err := account.ManagedIndexKey(accountID)
	if err != nil {
		return "unknown", err
	}
	keys := []string{wrapperKey, stateKey, dataKey}
	if err := p.dkvs.requireOnlineGroup(client, keys); err != nil {
		return "unknown", err
	}
	values, err := newDKVSReplicaStore(p.db).ReadSnapshot(client.replicaNamespace, keys)
	if err != nil {
		return "unknown", err
	}
	wrapper := values[wrapperKey]
	if wrapper == nil {
		if values[stateKey] != nil || values[dataKey] != nil ||
			p.accountProfile != nil && (p.accountProfile.RecoveryConfigured || p.accountProfile.Activation != nil) {
			return "unknown", ErrDKVSPathNotSynced
		}
		return "inactive", p.checkAccountManagedDataImport()
	}
	payload, err := openAccountRootWrapper(root, _chain, accountID, wrapper.Value)
	if err != nil {
		return "unknown", err
	}
	defer zeroBytes(payload.Secret)
	if payload.PackageID == "" {
		return "unknown", ErrRootAccountWrapperInvalid
	}
	if p.accountProfile == nil || !p.accountProfile.RecoveryConfigured {
		return "active-waiting-backup", ErrRootAccountDiscoveryPending
	}
	if !bytes.Equal(payload.Secret, p.accountSecret) ||
		!accountRootWrapperMetadataMatchesProfile(payload, *p.accountProfile) {
		return "active-waiting-backup", ErrDKVSPathNotSynced
	}
	return "active", nil
}

// Discovery, activation resumption and private backup share the existing
// replaceable account task. An inactive account never uploads a backup.
func (p *Manager) refreshAccountService() (resultErr error) {
	finish := p.accountSyncResult("service")
	defer func() { finish(resultErr) }()
	p.mutex.RLock()
	if p.accountProfile == nil || len(p.accountSecret) != 32 || p.accountPassword == "" {
		p.mutex.RUnlock()
		return nil
	}
	pending := p.accountProfile.Activation != nil
	configured := p.accountProfile.RecoveryConfigured
	password := p.accountPassword
	secret := bytes.Clone(p.accountSecret)
	p.mutex.RUnlock()
	defer zeroBytes(secret)
	if pending {
		return p.runAccountOperation(nil, func() error {
			_, err := p.resumePendingActivation(secret, password)
			return err
		})
	}
	if !configured {
		_, err := p.RecoverAccountManagementFromCurrentWallet(context.Background(), password)
		if errors.Is(err, ErrRootAccountNotFound) {
			return nil
		}
		return err
	}
	if err := p.ensureAccountCoreBindingForSync(); err != nil {
		return err
	}
	return p.SyncAccountManagementState(nil)
}
