package wallet

import (
	"fmt"
	"sort"
)

// AccountModuleStatus exposes backup health without leaking data or transport
// internals. Healthy dirty modules remain writable while their backup runs.
type AccountModuleStatus struct {
	Provider      string `json:"provider"`
	Scope         string `json:"scope"`
	WriteAllowed  bool   `json:"write_allowed"`
	BackupState   string `json:"backup_state"`
	LastError     string `json:"last_error,omitempty"`
	LastErrorCode string `json:"last_error_code,omitempty"`
}

func (p *Manager) listAccountLocal(accountID string) ([]*accountLocalKV, error) {
	var values []*accountLocalKV
	err := p.db.BatchRead(accountLocalPrefix(accountID), false, func(key, encoded []byte) error {
		var value accountLocalKV
		if DecodeFromBytes(encoded, &value) != nil || !validAccountLocal(&value, accountID, p.cfg.Chain) ||
			string(accountLocalKey(accountID, value.Provider, value.Scope)) != string(key) {
			return fmt.Errorf("invalid local account KV")
		}
		values = append(values, &value)
		return nil
	})
	sort.Slice(values, func(i, j int) bool {
		if values[i].Provider == values[j].Provider {
			return values[i].Scope < values[j].Scope
		}
		return values[i].Provider < values[j].Provider
	})
	return values, err
}

// Caller owns the profile read gate. This function performs local reads only.
func (p *Manager) fillAccountStatus(status *AccountManagementStatus) {
	if err := p.accountDBFailure(); err != nil {
		status.AccountState, status.BackupState = "error", "pending"
		status.WriteAllowed, status.DKVSWriteAllowed = false, false
		status.ManagedDataDirty = true
		status.LastDKVSSyncErrorCode, status.LastDKVSSyncError = accountDBFailureCode, err.Error()
		return
	}
	status.AccountState, status.BackupState = "syncing", "none"
	if p.wallet == nil || p.accountProfile != nil && (len(p.accountSecret) != 32 || p.accountPassword == "") {
		status.AccountState = "locked"
		return
	}
	if status.LocalOnly && !status.RecoveryConfigured && !status.ActivationPending {
		status.AccountState, status.WriteAllowed = "local", true
		return
	}
	status.WriteAllowed = status.DKVSWriteAllowed
	if status.WriteAllowed {
		status.AccountState = "ready"
	} else if status.DKVSStatus == DKVSSubscriptionOfflineReady || status.DKVSStatus == DKVSSubscriptionError ||
		status.DKVSStatus == DKVSSubscriptionResetRequired {
		status.AccountState = "error"
	}
	if !status.RecoveryConfigured || p.accountProfile == nil {
		return
	}
	// A failed local read cannot establish that every change was backed up.
	// Keep the existing warning until all required backup facts are readable.
	status.BackupState, status.ManagedDataDirty = "pending", true
	values, err := p.listAccountLocal(p.accountProfile.AccountID)
	if err != nil {
		status.AccountState, status.WriteAllowed = "error", false
		return
	}
	outbox, err := newDKVSReplicaStore(p.db).LoadOutbox(p.dkvsReplicaNamespace())
	if err != nil {
		status.AccountState, status.WriteAllowed = "error", false
		return
	}
	entries, err := p.confirmedAccountEntries()
	if err != nil {
		status.AccountState, status.WriteAllowed = "error", false
		return
	}
	controls, err := p.accountControlKeysLocked()
	if err != nil {
		status.AccountState, status.WriteAllowed = "error", false
		return
	}
	status.BackupState = "backed_up"
	status.ManagedDataDirty, status.PendingChanges = false, 0
	// Confirmed module rows alone do not complete the first backup: its
	// directory may still be awaiting publication. Business readiness is separate.
	if p.accountProfile.InitialBackupPending {
		status.BackupState, status.ManagedDataDirty = "pending", true
	}
	status.ManagedDataRevision = 0
	states := make(map[string]string)
	failures := make(map[string]*DKVSBatchOutboxEntry)
	for _, entry := range outbox {
		if entry.OriginDomain != accountKVOutboxDomain {
			continue
		}
		if entry.State == DKVSOutboxAcknowledged {
			states[entry.OriginKey] = "cleanup"
		} else if entry.State == DKVSOutboxConflict || entry.State == DKVSOutboxTerminal {
			failures[entry.OriginKey] = entry
		} else if entry.LastError == "" {
			states[entry.OriginKey] = "uploading"
		}
	}
	for _, value := range values {
		if value.Provider == "account" && !controls[value.Key] {
			continue
		}
		deleted := accountDeleteConfirmed(value, entries)
		backup := "backed_up"
		if value.Generation > value.Confirmed {
			if deleted {
				backup = "cleanup"
				if status.BackupState == "backed_up" {
					status.BackupState = "cleanup"
				}
			} else {
				backup = "pending"
				status.BackupState = "pending"
				status.ManagedDataDirty = true
				status.PendingChanges++
			}
		}
		if state := states[value.Key]; (!deleted && state == "uploading") || (state == "cleanup" && value.Generation <= value.Confirmed) {
			backup = state
			if status.BackupState == "backed_up" || state == "uploading" {
				status.BackupState = state
			}
		}
		if value.Provider == "account" {
			continue
		}
		if value.Provider == "wallets" && value.Baseline != nil {
			status.StateSeq = value.Baseline.Seq
		}
		module := AccountModuleStatus{Provider: value.Provider, Scope: value.Scope,
			WriteAllowed: status.WriteAllowed, BackupState: backup, LastError: value.LastError}
		if failure := failures[value.Key]; failure != nil {
			module.LastErrorCode = failure.LastErrorCode
			if module.LastError == "" {
				module.LastError = failure.LastError
			}
		}
		switch {
		case deleted:
			// Keep cleanup diagnostics on the removed scope, without marking
			// its confirmed deletion or unrelated modules as failed.
			module.WriteAllowed = false
			if module.LastError != "" || module.LastErrorCode != "" {
				module.BackupState = "cleanup"
				if status.BackupState == "backed_up" {
					status.BackupState = "cleanup"
				}
			}
		case value.Restoring:
			module.BackupState, module.WriteAllowed = "restoring", false
		case value.Conflict:
			module.BackupState, module.WriteAllowed = "conflict", false
		case module.LastError != "" || module.LastErrorCode != "":
			module.BackupState, module.WriteAllowed = "error", false
		}
		status.Modules = append(status.Modules, module)
	}
}
