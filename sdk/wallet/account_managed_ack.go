package wallet

import (
	"bytes"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

// Caller owns the account profile gate. This reads one confirmed local view;
// it never tries to refresh it or wait for transport.
func (p *Manager) requireAccountManagedBaselineLocked(client *SatsNetDKVSClient) error {
	if p.accountProfile == nil || len(p.accountSecret) != 32 {
		return ErrDKVSPathNotSynced
	}
	rootInfo, err := p.accountManagementRootWalletLocked()
	if err != nil {
		return err
	}
	root := cloneWalletAtAccountZero(rootInfo.Wallet)
	wrapperKey, err := accountRootWrapperKey(root)
	if err != nil {
		return err
	}
	indexKey, err := account.ManagedIndexKey(p.accountProfile.AccountID)
	if err != nil {
		return err
	}
	values, err := newDKVSReplicaStore(p.db).ReadSnapshot(client.replicaNamespace, []string{wrapperKey, indexKey})
	if err != nil {
		return err
	}
	wrapper := values[wrapperKey]
	if wrapper == nil {
		return ErrRootAccountDiscoveryPending
	}
	payload, err := openAccountRootWrapper(root, _chain, p.accountProfile.AccountID, wrapper.Value)
	if err != nil {
		return err
	}
	defer zeroBytes(payload.Secret)
	if !bytes.Equal(payload.Secret, p.accountSecret) || payload.PackageID != p.accountProfile.PackageID {
		return ErrRootAccountWrapperInvalid
	}
	index := values[indexKey]
	if index == nil {
		local, err := p.readAccountLocal(p.accountProfile.AccountID, "account", indexKey)
		if err != nil {
			return err
		}
		if !p.accountProfile.InitialBackupPending && (local == nil || len(local.Value) == 0) {
			return ErrRootAccountDiscoveryPending
		}
		return nil
	}
	_, err = account.OpenManagedIndex(p.accountSecret, p.accountProfile.AccountID, _chain, index.Value)
	return err
}

// Finish only per-key receipts. This path does not receive remote state or
// grant business eligibility; its cleanup may safely be retried locally.
func (p *Manager) finishAccountManagedACK(client *SatsNetDKVSClient, entry *DKVSBatchOutboxEntry) error {
	return p.finishAccountKVACK(client, entry)
}

func (p *Manager) finishAccountManagedACKLocked(client *SatsNetDKVSClient, entry *DKVSBatchOutboxEntry) error {
	return p.finishAccountKVACKLocked(client, entry)
}

func (p *Manager) finishReadyAccountACKsLocked() error {
	if p.accountProfile == nil || !p.accountProfile.RecoveryConfigured || p.dkvs == nil {
		return nil
	}
	client, err := p.dkvs.primaryClient()
	if err != nil {
		return err
	}
	store := newDKVSReplicaStore(p.db)
	entries, err := store.LoadOutbox(client.replicaNamespace)
	if err != nil {
		return err
	}
	var cleanup error
	for _, entry := range entries {
		if entry.OriginDomain != accountKVOutboxDomain || entry.State != DKVSOutboxAcknowledged {
			continue
		}
		if err := p.finishAccountKVACKLocked(client, entry); err != nil {
			return accountACKCleanup(entry, err)
		}
		if err := store.DiscardOutbox(entry); err != nil {
			cleanup = accountACKCleanup(entry, err)
		}
	}
	return cleanup
}
