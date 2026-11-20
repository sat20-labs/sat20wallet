package wallet

import (
	"bytes"
	"errors"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
)

// A root observation belongs to the selected source and this unlocked session.
// It never grants permission to publish a record.
type accountRootCheck struct {
	client     *SatsNetDKVSClient
	key        string
	endpoint   string
	active     bool
	online     *byte
	generation uint64
}

func (r accountRootCheck) matches(client *SatsNetDKVSClient, key string) bool {
	if r.client != client || r.key != key || r.endpoint == "" {
		return false
	}
	if !r.active {
		client.manager.mu.Lock()
		online := r.online != nil && r.online == client.manager.rootOnline
		client.manager.mu.Unlock()
		if !online {
			return false
		}
	}
	client.endpointMu.RLock()
	defer client.endpointMu.RUnlock()
	return client.endpointID == r.endpoint
}

// Online grants authorize service activation. A verified cached wrapper still
// identifies an activated account whose local backup import is incomplete.
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
	values, err := newDKVSReplicaStore(p.db).ReadSnapshot(client.replicaNamespace, keys)
	if err != nil {
		return "unknown", err
	}
	wrapper := values[wrapperKey]
	if wrapper == nil {
		checked := p.rootCheck.matches(client, wrapperKey)
		if checked && p.rootCheck.active {
			return "active-waiting-backup", ErrRootAccountDiscoveryPending
		}
		if values[stateKey] != nil || values[dataKey] != nil ||
			p.accountProfile != nil && (p.accountProfile.RecoveryConfigured || p.accountProfile.Activation != nil) {
			return "unknown", ErrDKVSPathNotSynced
		}
		if !checked {
			if err := p.dkvs.requireOnlineGroup(client, keys); err != nil {
				return "unknown", err
			}
		}
		return "inactive", nil
	}
	payload, err := openAccountRootWrapper(root, _chain, accountID, wrapper.Value)
	if err != nil {
		return "unknown", err
	}
	defer zeroBytes(payload.Secret)
	if payload.PackageID == "" {
		return "unknown", ErrRootAccountWrapperInvalid
	}
	// A verified, bound wrapper is durable evidence of activation even when
	// this device has not restored the backup or reopened an online session.
	if p.accountProfile == nil || !p.accountProfile.RecoveryConfigured {
		return "active-waiting-backup", ErrRootAccountDiscoveryPending
	}
	if err := p.dkvs.requireOnlineGroup(client, keys); err != nil {
		return "unknown", err
	}
	if !bytes.Equal(payload.Secret, p.accountSecret) ||
		!accountRootWrapperMetadataMatchesProfile(payload, *p.accountProfile) {
		return "active-waiting-backup", ErrDKVSPathNotSynced
	}
	return "active", nil
}

// Only an explicit entry/retry discovers an inactive account. Ordinary
// replaceable backup jobs never rediscover or resume an unactivated account.
func (p *Manager) refreshAccountService(discover bool) (resultErr error) {
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
	if !configured && !discover {
		return nil
	}
	ctx := p.ensureDKVSManager().requestContext()
	if pending {
		return p.runAccountOperation(ctx, func() error {
			_, err := p.resumePendingActivation(secret, password)
			return err
		})
	}
	if !configured {
		_, err := p.RecoverAccountManagementFromCurrentWallet(ctx, password)
		if errors.Is(err, ErrRootAccountNotFound) {
			return nil
		}
		return err
	}
	if err := p.ensureAccountCoreBindingForSync(); err != nil {
		return err
	}
	return p.SyncAccountManagementState(ctx)
}
