package wallet

import (
	"bytes"
	"reflect"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

// The module row owns confirmation. First-backup completion and pending catalog
// edits are finalized in the same local batch.
// Caller holds the Manager mutex.
func (p *Manager) saveAccountReceipt(value *accountLocalKV) error {
	if err := p.accountDBFailure(); err != nil {
		return err
	}
	if p.accountProfile == nil || p.accountProfile.AccountID != value.AccountID {
		return errAccountSnapshotChanged
	}
	profile := *p.accountProfile
	values, err := p.listAccountLocal(value.AccountID)
	if err != nil {
		return err
	}
	controls, err := p.accountControlKeysLocked()
	if err != nil {
		return err
	}
	indexKey, err := account.ManagedIndexKey(value.AccountID)
	if err != nil {
		return err
	}
	var entries map[string]account.ManagedEntry
	var stored *accountLocalKV
	var walletReceipt, indexReceipt bool
	for i, item := range values {
		if item.Provider == value.Provider && item.Scope == value.Scope {
			stored = item
			values[i] = value
			item = value
		}
		if item.Provider == "account" && item.Key == indexKey && item.Baseline != nil {
			index, err := account.OpenManagedIndex(p.accountSecret, value.AccountID, p.cfg.Chain, item.Baseline.Value)
			if err != nil {
				return err
			}
			entries = accountEntryMap(index)
			indexReceipt = item.Generation != 0 && item.Generation == item.Confirmed && accountStorageConfirmed(item, &profile)
		}
		if item.Provider == "wallets" && item.Baseline != nil {
			walletReceipt = item.Generation != 0 && item.Generation == item.Confirmed && accountStorageConfirmed(item, &profile)
		}
	}
	// Receipt and first-backup completion use the same confirmed deletion fact
	// as Wait/status. A blob awaiting reclamation does not make its deletion dirty.
	complete := profile.RecoveryConfigured && profile.Activation == nil && len(controls) == 3
	seenControls, seenModules := make(map[string]bool), make(map[string]bool)
	for _, item := range values {
		if item.Provider == "account" && !controls[item.Key] {
			continue
		}
		if accountDeleteConfirmed(item, entries) {
			continue
		}
		confirmed := item.Generation != 0 && item.Generation == item.Confirmed &&
			!item.Restoring && !item.Conflict && item.LastError == "" && item.Baseline != nil &&
			item.Baseline.Key == item.Key && item.Baseline.Seq != 0 && bytes.Equal(item.Value, item.Baseline.Value) &&
			accountStorageConfirmed(item, &profile)
		complete = complete && confirmed
		if item.Provider == "account" {
			seenControls[item.Key] = confirmed
		} else {
			id := item.Provider + "\x00" + item.Scope
			entry, registered := entries[id]
			seenModules[id] = confirmed && registered && !entry.Deleted && !item.Deleted &&
				entry.Key == item.Key && entry.Codec == item.Codec
			complete = complete && seenModules[id]
		}
	}
	for key := range controls {
		complete = complete && seenControls[key]
	}
	for id, entry := range entries {
		if !entry.Deleted {
			complete = complete && seenModules[id]
		}
	}
	// A received/acknowledged directory must actually expose the wallet catalog;
	// partial first backup and a newly staged N+1 retain the initialization marker.
	complete = complete && seenModules["wallets\x00"+AccountManagedDataGlobalScope]
	if profile.InitialBackupPending && complete {
		profile.InitialBackupPending = false
		profile.InitWrapperHash = ""
	}
	if walletReceipt {
		var pending []accountManagementMutation
		for _, mutation := range profile.Pending {
			if mutation.Type != accountMutationAddWallet && mutation.Type != accountMutationEnsureAccount {
				continue
			}
			info := p.walletInfoByFingerprintLocked(mutation.Fingerprint)
			for _, item := range values {
				if item.Provider == "account" || item.Deleted || !accountWalletHasScope(info, p.cfg.Chain, item.Scope) {
					continue
				}
				entry, registered := entries[item.Provider+"\x00"+item.Scope]
				if !indexReceipt || !registered || entry.Deleted {
					pending = append(pending, mutation)
					break
				}
			}
		}
		profile.Pending = pending
	}
	valueChanged := !reflect.DeepEqual(stored, value)
	profileChanged := !reflect.DeepEqual(p.accountProfile, &profile)
	if !valueChanged && !profileChanged {
		return nil
	}
	batch, err := NewAtomicWriteBatch(p.db)
	if err != nil {
		return err
	}
	defer batch.Close()
	if valueChanged {
		if err := p.writeAccountLocal(batch, value); err != nil {
			return err
		}
	}
	if profileChanged {
		encoded, err := EncodeToBytes(&profile)
		if err != nil {
			return err
		}
		if err := batch.Put(accountManagementProfileKey(), encoded); err != nil {
			return accountDBWriteError(err)
		}
	}
	if err := batch.Flush(); err != nil {
		return accountDBWriteError(err)
	}
	p.accountProfile = &profile
	return nil
}
