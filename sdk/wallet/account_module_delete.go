package wallet

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
)

// The same business batch retains an explicit deletion intent. A missing Export
// item is never interpreted as a deletion.
func (p *Manager) stageAccountWalletDelete(batch indexer.WriteBatch, fingerprint string) error {
	if p.accountProfile == nil {
		return nil
	}
	values, err := p.listAccountLocal(p.accountProfile.AccountID)
	if err != nil {
		return err
	}
	for _, value := range values {
		parts := strings.Split(value.Scope, "/")
		if value.Provider == "account" || len(parts) != 3 || parts[1] != fingerprint || value.Deleted {
			continue
		}
		entry, err := accountEntry(p.accountSecret, value.AccountID, value.Provider, value.Scope)
		if err != nil {
			return err
		}
		value.Key, value.Codec, value.Deleted = entry.Key, entry.Codec, true
		if value.Generation == ^uint64(0) {
			return fmt.Errorf("account change counter overflow")
		}
		value.Generation++
		value.Captured = value.Generation
		value.LastError, value.Conflict, value.Restoring = "", false, false
		value.Value, value.ConflictValue = nil, nil
		if err := p.writeAccountLocal(batch, value); err != nil {
			return err
		}
	}
	return nil
}

func accountWalletHasScope(info *WalletInfo, network, scope string) bool {
	if info == nil || info.Wallet == nil {
		return false
	}
	parts := strings.Split(scope, "/")
	if len(parts) != 3 {
		return false
	}
	index, err := strconv.ParseUint(parts[2], 10, 32)
	if err != nil || index >= uint64(max(info.Accounts, 1)) {
		return false
	}
	candidate := AccountManagedDataScope{Network: network, WalletFingerprint: walletFingerprint(info.Wallet), AccountIndex: uint32(index)}
	return candidate.ID() == scope
}

// A successful local add owns a new instance. Its pending catalog mutation
// prevents an older remote tombstone from deleting the new empty module.
// Caller holds p.mutex.
func (p *Manager) pendingAccountScopeAdd(scope string) bool {
	if p.accountProfile == nil {
		return false
	}
	parts := strings.Split(scope, "/")
	if len(parts) != 3 {
		return false
	}
	info := p.walletInfoByFingerprintLocked(parts[1])
	if !accountWalletHasScope(info, p.cfg.Chain, scope) {
		return false
	}
	index, _ := strconv.ParseUint(parts[2], 10, 32)
	for i := len(p.accountProfile.Pending) - 1; i >= 0; i-- {
		mutation := p.accountProfile.Pending[i]
		if mutation.Fingerprint != parts[1] || mutation.WalletID != info.Id {
			continue
		}
		switch mutation.Type {
		case accountMutationDeleteWallet:
			return false
		case accountMutationAddWallet:
			return true
		case accountMutationEnsureAccount:
			if uint64(mutation.Account) == index {
				return true
			}
		}
	}
	return false
}

// Initialize only the newly created local scopes. The old data is gone; the
// existing receipt counters and CAS baseline remain solely for reconciliation.
func (p *Manager) stageNewWalletScopes(batch indexer.WriteBatch, candidate *WalletInfo, first int) error {
	if p.accountProfile == nil || first >= max(candidate.Accounts, 1) {
		return nil
	}
	for index := first; index < max(candidate.Accounts, 1); index++ {
		scope := AccountManagedDataScope{Network: p.cfg.Chain,
			WalletFingerprint: walletFingerprint(candidate.Wallet), AccountIndex: uint32(index)}.ID()
		entry, err := accountEntry(p.accountSecret, p.accountProfile.AccountID, rgb11AccountManagedProviderID, scope)
		if err != nil {
			return err
		}
		value, err := p.readAccountLocal(p.accountProfile.AccountID, entry.Provider, scope)
		if err != nil {
			return err
		}
		if value == nil {
			value = &accountLocalKV{Version: 1, AccountID: p.accountProfile.AccountID,
				Network: p.cfg.Chain, Provider: entry.Provider, Scope: scope}
		}
		if value.Generation == ^uint64(0) {
			return fmt.Errorf("account change counter overflow")
		}
		value.Generation++
		value.Key, value.Codec = entry.Key, entry.Codec
		value.Value, value.ConflictValue = nil, nil
		value.Deleted, value.Restoring, value.Conflict = false, false, false
		value.LastError = ""
		if err := p.writeAccountLocal(batch, value); err != nil {
			return err
		}
	}
	key, err := account.ManagedIndexKey(p.accountProfile.AccountID)
	if err != nil {
		return err
	}
	value, err := p.readAccountLocal(p.accountProfile.AccountID, "account", key)
	if err != nil {
		return err
	}
	if value == nil {
		value = &accountLocalKV{Version: 1, AccountID: p.accountProfile.AccountID, Network: p.cfg.Chain,
			Provider: "account", Scope: key, Key: key, Codec: "v1"}
	}
	if value.Generation == ^uint64(0) {
		return fmt.Errorf("account change counter overflow")
	}
	value.Generation++
	if len(value.Value) != 0 {
		value.Captured = value.Generation
	}
	return p.writeAccountLocal(batch, value)
}

// Caller holds the profile gate. Only an acknowledged or received directory,
// never its unconfirmed local overlay, establishes a remote deletion fact.
func (p *Manager) confirmedAccountEntries() (map[string]account.ManagedEntry, error) {
	key, err := account.ManagedIndexKey(p.accountProfile.AccountID)
	if err != nil {
		return nil, err
	}
	value, err := p.readAccountLocal(p.accountProfile.AccountID, "account", key)
	if err != nil || value == nil || value.Baseline == nil {
		return nil, err
	}
	index, err := account.OpenManagedIndex(p.accountSecret, p.accountProfile.AccountID, p.cfg.Chain, value.Baseline.Value)
	if err != nil {
		return nil, err
	}
	return accountEntryMap(index), nil
}

func accountDeleteConfirmed(value *accountLocalKV, entries map[string]account.ManagedEntry) bool {
	entry, found := entries[value.Provider+"\x00"+value.Scope]
	return value.Provider != "account" && value.Deleted && found && entry.Deleted &&
		entry.Key == value.Key && entry.Codec == value.Codec
}

// Physical reclamation is a separate task in the existing worker. Its durable
// intent is the directory tombstone; normal sync schedules it again on reopen.
func (p *Manager) scheduleAccountCleanup() {
	if p == nil || p.dkvs == nil {
		return
	}
	p.dkvs.schedule("account-module-cleanup", func(_ *dkvsStore) error {
		err := p.runAccountOperation(nil, func() error {
			p.mutex.Lock()
			snapshot, _, err := p.captureAccountManagementSyncSnapshotBaseLocked()
			p.mutex.Unlock()
			if err != nil || snapshot == nil {
				return err
			}
			defer zeroBytes(snapshot.secret)
			store, err := p.accountDKVSStore()
			if err != nil {
				return err
			}
			return p.cleanAccountDeleted(store, snapshot)
		})
		if p.dkvs != nil {
			p.dkvs.notifyStatusCallback()
		}
		if err != nil {
			Log.Debugf("account blob cleanup pending: manager=%d error_code=%s", p.debugID, dkvsSyncErrorCode(err))
			// Retain this job on the worker's existing retry delay, without
			// reporting a failed account backup or withdrawing its write grant.
			return fmt.Errorf("%w: account blob cleanup pending", errDKVSJobDeferred)
		}
		return nil
	})
}

// Only a confirmed directory tombstone authorizes data cleanup. The local
// directory and tombstone remain available for later TTL re-publication.
func (p *Manager) cleanAccountDeleted(store *dkvsStore, snapshot *accountManagementSyncSnapshot) error {
	key, err := account.ManagedIndexKey(snapshot.profile.AccountID)
	if err != nil {
		return err
	}
	p.mutex.RLock()
	local, err := p.readAccountLocal(snapshot.profile.AccountID, "account", key)
	p.mutex.RUnlock()
	if err != nil {
		return err
	}
	if local == nil || local.Baseline == nil {
		return nil
	}
	index, err := account.OpenManagedIndex(snapshot.secret, snapshot.profile.AccountID, snapshot.network, local.Baseline.Value)
	if err != nil {
		return err
	}
	for _, entry := range index.Entries {
		if !entry.Deleted {
			continue
		}
		p.mutex.RLock()
		before, readErr := p.readAccountLocal(snapshot.profile.AccountID, entry.Provider, entry.Scope)
		p.mutex.RUnlock()
		if readErr != nil {
			return readErr
		}
		// Cleanup only reclaims an already adopted local deletion. It must
		// never recreate that intent from an older directory snapshot.
		if before == nil || !before.Deleted {
			continue
		}
		if err := store.rememberAccountModule(snapshot.secret, snapshot.profile.AccountID, entry); err != nil {
			return err
		}
		if err := store.WaitReady(entry.Key); err != nil {
			return err
		}
		if err := store.SyncCurrent(entry.Key); err != nil {
			return err
		}
		remote, err := store.Get(entry.Key)
		if err != nil && !errors.Is(err, ErrDKVSRecordNotFound) {
			return err
		}
		p.mutex.Lock()
		value, readErr := p.readAccountLocal(snapshot.profile.AccountID, entry.Provider, entry.Scope)
		if readErr == nil && (value == nil || !value.Deleted || value.Generation != before.Generation) {
			p.mutex.Unlock()
			continue
		}
		if readErr == nil && remote == nil {
			value.Deleted, value.Value, value.Baseline = true, nil, nil
			value.Confirmed, value.LastError = value.Generation, ""
			readErr = p.writeAccountLocal(nil, value)
		}
		if readErr == nil && remote != nil {
			value.Deleted, value.Captured = true, value.Generation
			value.LastError, value.Restoring, value.Conflict = "", false, false
			readErr = p.writeAccountLocal(nil, value)
		}
		p.mutex.Unlock()
		if readErr != nil {
			return readErr
		}
		if remote == nil {
			continue
		}
		value.Deleted, value.Captured = true, value.Generation
		value, err = p.setAccountBaseline(value, remote)
		if err != nil {
			return err
		}
		if err := p.publishAccountValue(store, value, snapshot.root, snapshot.profile); err != nil {
			return err
		}
	}
	return nil
}
