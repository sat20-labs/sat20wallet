package wallet

import (
	"fmt"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// Only a definite rejection ends a frozen attempt. Unknown network outcomes
// keep the original signed request for reconciliation; no business is replayed.
func accountSendCanRebase(entry *DKVSBatchOutboxEntry) bool {
	if entry == nil || entry.OriginDomain != accountKVOutboxDomain || entry.State != DKVSOutboxConflict {
		return false
	}
	return isDKVSRebaseError(&DKVSError{Code: dkvsindexer.ErrorCode(entry.LastErrorCode), Message: entry.LastError})
}

func (p *Manager) accountSendFailure(entry *DKVSBatchOutboxEntry) error {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if p.accountProfile == nil {
		return ErrDKVSPathNotSynced
	}
	value, err := p.accountLocalForKey(p.accountProfile.AccountID, entry.OriginKey)
	if err != nil {
		return err
	}
	if value == nil || value.Provider == "account" {
		return fmt.Errorf("account control request refused: %w", dkvsOutboxEntryError(entry))
	}
	return &accountModuleError{provider: value.Provider, scope: value.Scope,
		cause: dkvsOutboxEntryError(entry)}
}

// An explicit retry may replace a definitely refused backup request. Its
// business data and dirty count stay intact; unknown outcomes and ACKs stay put.
func (p *Manager) retryAccountBackup(client *SatsNetDKVSClient) error {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if p.accountProfile == nil {
		return nil
	}
	store := newDKVSReplicaStore(p.db)
	entries, err := store.LoadOutbox(client.replicaNamespace)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.OriginDomain != accountKVOutboxDomain || entry.State != DKVSOutboxConflict {
			continue
		}
		switch dkvsindexer.ErrorCode(entry.LastErrorCode) {
		case dkvsindexer.ErrorCodeQuotaExceeded:
		case dkvsindexer.ErrorCodeInvalidRecord:
			// Expired AUTOPAY entitlement is reported as INVALID_RECORD.
			// Explicit retry rebuilds this refused backup from current data;
			// the node still validates its payment proof and record normally.
			mutations, err := entry.DecodeMutations()
			if err != nil {
				return err
			}
			if !batchContainsAutopay(mutations) {
				continue
			}
		default:
			continue
		}
		local, err := p.accountLocalForKey(p.accountProfile.AccountID, entry.OriginKey)
		if err != nil {
			return err
		}
		if local == nil {
			continue
		}
		if err := store.DiscardOutbox(entry); err != nil {
			return err
		}
	}
	return nil
}

func (p *Manager) pendingAccountSend(store *dkvsStore, key string) error {
	replica := newDKVSReplicaStore(p.db)
	entries, err := replica.LoadOutbox(store.client.replicaNamespace)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.OriginDomain != accountKVOutboxDomain || entry.OriginKey != key {
			continue
		}
		if entry.State == DKVSOutboxAcknowledged {
			if err := store.manager.finishAcknowledgedWrite(store.client, replica, entry); err != nil {
				return err
			}
			continue
		}
		if accountSendCanRebase(entry) {
			if err := replica.DiscardOutbox(entry); err != nil {
				return err
			}
			continue
		}
		if entry.State == DKVSOutboxConflict || entry.State == DKVSOutboxTerminal {
			return p.accountSendFailure(entry)
		}
		return &DKVSRequestPendingError{RequestID: entry.RequestID, Cause: ErrDKVSPathNotSynced}
	}
	return nil
}
