package wallet

import (
	"bytes"
	"fmt"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const accountKVOutboxDomain = "account"

func (p *Manager) accountLocalForKey(accountID, key string) (*accountLocalKV, error) {
	values, err := p.listAccountLocal(accountID)
	if err != nil {
		return nil, err
	}
	for _, value := range values {
		if value.Key == key {
			return value, nil
		}
	}
	return nil, nil
}

// A request ACK confirms only its own frozen local count. It never imports
// business data, installs a receive cursor, or creates a write grant.
func (p *Manager) finishAccountKVACK(client *SatsNetDKVSClient, entry *DKVSBatchOutboxEntry) error {
	p.mutex.Lock()
	err := p.finishAccountKVACKLocked(client, entry)
	p.mutex.Unlock()
	if err == nil && p.dkvs != nil {
		p.dkvs.notifyStatusCallback()
	}
	return err
}

func (p *Manager) finishAccountKVACKLocked(client *SatsNetDKVSClient, entry *DKVSBatchOutboxEntry) error {
	if entry == nil || entry.State != DKVSOutboxAcknowledged || entry.OriginDomain != accountKVOutboxDomain ||
		entry.Namespace != p.dkvsReplicaNamespace() || p.accountProfile == nil || len(p.accountSecret) != 32 {
		return ErrDKVSPathNotSynced
	}
	current, err := p.dkvs.primaryClient()
	if err != nil || current != client {
		return ErrDKVSPathNotSynced
	}
	client.endpointMu.RLock()
	endpoint := client.endpointID
	client.endpointMu.RUnlock()
	if endpoint == "" || endpoint != entry.EndpointID {
		return dkvsindexer.ErrEndpointMismatch
	}
	mutations, err := entry.DecodeMutations()
	if err != nil || len(mutations) != 1 || mutations[0].Record == nil ||
		mutations[0].Record.Key != entry.OriginKey || entry.OriginGeneration == 0 {
		return dkvsindexer.ErrInvalidRecord
	}
	record := mutations[0].Record
	value, err := p.accountLocalForKey(p.accountProfile.AccountID, record.Key)
	if err != nil {
		return err
	}
	if value == nil || value.Generation < entry.OriginGeneration || value.Captured < entry.OriginGeneration {
		return fmt.Errorf("account ACK has no captured local version")
	}
	deleted := dkvsindexer.IsTombstone(record.Flags)
	if deleted != value.Deleted || !deleted && value.Captured == entry.OriginGeneration && !bytes.Equal(value.Value, record.Value) {
		Log.Warningf("account ACK diagnostic: manager=%d branch=captured-value key_tag=%s request_tag=%s origin_count=%d generation=%d captured=%d confirmed=%d frozen_seq=%d local_deleted=%t record_deleted=%t captured_value_equal=%t local_bytes=%d frozen_bytes=%d",
			p.debugID, accountTraceScope(entry.OriginKey), accountTraceScope(entry.RequestID),
			entry.OriginGeneration, value.Generation, value.Captured, value.Confirmed, record.Seq,
			value.Deleted, deleted, bytes.Equal(value.Value, record.Value), len(value.Value), len(record.Value))
		return dkvsindexer.ErrInvalidRecord
	}
	// A newer verified baseline may already include another device's update.
	// A late receipt must not move that baseline backwards.
	if value.Baseline == nil || value.Baseline.Seq < record.Seq {
		value.Baseline = cloneDKVSRecord(record)
	} else if value.Baseline.Seq == record.Seq && dkvsindexer.RecordHash(value.Baseline) != dkvsindexer.RecordHash(record) {
		return dkvsindexer.ErrWriteConflict
	}
	if value.Confirmed < entry.OriginGeneration {
		value.Confirmed = entry.OriginGeneration
	}
	if value.Generation == entry.OriginGeneration {
		value.LastError = ""
		// An older receipt cannot resolve a conflict against a newer remote
		// baseline that has already been verified locally.
		if value.Baseline != nil && dkvsindexer.RecordHash(value.Baseline) == dkvsindexer.RecordHash(record) {
			value.Conflict, value.ConflictValue = false, nil
		}
		if deleted {
			value.Value = nil
		}
	}
	return p.saveAccountReceipt(value)
}

func (p *Manager) accountKVACKCovered(entry *DKVSBatchOutboxEntry) (bool, error) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if entry == nil || entry.State != DKVSOutboxAcknowledged || entry.OriginDomain != accountKVOutboxDomain ||
		p.accountProfile == nil || entry.Namespace != p.dkvsReplicaNamespace() || entry.OriginGeneration == 0 {
		return false, nil
	}
	value, err := p.accountLocalForKey(p.accountProfile.AccountID, entry.OriginKey)
	if err != nil || value == nil {
		return false, err
	}
	return value.Confirmed >= entry.OriginGeneration, nil
}
