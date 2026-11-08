package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	indexer "github.com/sat20-labs/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Values are the complete, already encrypted account values. Transport records
// belong to the last verified source; their expired signatures are never replayed.
// Generation counters belong to this database, not to the remote value codec.
type accountLocalKV struct {
	Version       uint32
	AccountID     string
	Network       string
	Provider      string
	Scope         string
	Key           string
	Codec         string
	Value         []byte
	Baseline      *swire.DKVSRecord
	Generation    uint64
	Captured      uint64
	Confirmed     uint64
	Deleted       bool
	Restoring     bool
	Conflict      bool
	ConflictValue []byte
	LastError     string
}

func accountLocalPrefix(accountID string) []byte {
	return []byte(GetDBKeyPrefix() + "account/" + accountID + "/")
}

func accountLocalKey(accountID, provider, scope string) []byte {
	var id bytes.Buffer
	for _, part := range []string{provider, scope} {
		_ = binary.Write(&id, binary.BigEndian, uint32(len(part)))
		id.WriteString(part)
	}
	digest := sha256.Sum256(id.Bytes())
	return append(accountLocalPrefix(accountID), hex.EncodeToString(digest[:])...)
}

func validAccountLocal(value *accountLocalKV, accountID, network string) bool {
	return value != nil && value.Version == 1 && value.AccountID == accountID &&
		value.Network == network && validAccountManagedProviderID(value.Provider) &&
		strings.TrimSpace(value.Scope) == value.Scope && value.Scope != "" &&
		value.Confirmed <= value.Generation && value.Captured <= value.Generation
}

func (p *Manager) readAccountLocal(accountID, provider, scope string) (*accountLocalKV, error) {
	encoded, err := p.db.Read(accountLocalKey(accountID, provider, scope))
	if errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var value accountLocalKV
	if DecodeFromBytes(encoded, &value) != nil ||
		!validAccountLocal(&value, accountID, p.cfg.Chain) || value.Provider != provider || value.Scope != scope {
		return nil, fmt.Errorf("invalid local account KV")
	}
	return &value, nil
}

func (p *Manager) writeAccountLocal(batch indexer.WriteBatch, value *accountLocalKV) error {
	if err := p.accountDBFailure(); err != nil {
		return err
	}
	if !validAccountLocal(value, value.AccountID, p.cfg.Chain) {
		return fmt.Errorf("invalid local account KV")
	}
	encoded, err := EncodeToBytes(value)
	if err != nil {
		return err
	}
	key := accountLocalKey(value.AccountID, value.Provider, value.Scope)
	if batch != nil {
		return accountDBWriteError(batch.Put(key, encoded))
	}
	return accountDBWriteError(p.db.Write(key, encoded))
}

func (p *Manager) stageAccountChange(batch indexer.WriteBatch, provider, scope string) error {
	if batch == nil || !validAccountManagedProviderID(provider) || scope == "" || strings.TrimSpace(scope) != scope {
		return fmt.Errorf("invalid account change")
	}
	if p.accountProfile == nil {
		return nil
	}
	accountID := p.accountProfile.AccountID
	value, err := p.readAccountLocal(accountID, provider, scope)
	if err != nil {
		return err
	}
	if value == nil {
		value = &accountLocalKV{Version: 1, AccountID: accountID, Network: p.cfg.Chain, Provider: provider, Scope: scope}
	}
	if value.Restoring {
		return ErrAccountManagedDataImportIncomplete
	}
	if value.Generation == ^uint64(0) {
		return fmt.Errorf("account change counter overflow")
	}
	value.Generation++
	return p.writeAccountLocal(batch, value)
}

// StageAccountChange participates in the caller's existing local batch. Call
// the returned completion exactly after commit/abort (a deferred abort is safe).
// CheckWrite is a separate business boundary; reconciliation also stages data.
// The caller must not already own the Manager mutex.
func (p *Manager) StageAccountChange(batch indexer.WriteBatch, provider, scope string) (func(bool), error) {
	if p == nil || p.db == nil {
		return nil, fmt.Errorf("account manager is unavailable")
	}
	p.mutex.Lock()
	if err := p.stageAccountChange(batch, provider, scope); err != nil {
		p.mutex.Unlock()
		return nil, err
	}
	var once sync.Once
	return func(committed bool) {
		once.Do(func() {
			p.mutex.Unlock()
			if committed {
				p.NotifyAccountChanged()
			}
		})
	}, nil
}

// NotifyAccountChanged only wakes the existing coalesced account task. The
// durable change marker, not delivery of this notification, owns retryability.
func (p *Manager) NotifyAccountChanged() {
	if p != nil {
		p.scheduleAccountManagedStateSync()
		if p.dkvs != nil {
			p.dkvs.notifyStatusCallback()
		}
	}
}

// CheckAccountWrite is a local qualification check, never a network operation
// or an ACK cleanup hook. A provider failure is scoped to that provider/scope.
func (p *Manager) CheckAccountWrite(provider, scope string) error {
	if p == nil {
		return ErrDKVSPathNotSynced
	}
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if err := p.checkAccountRestore(provider, scope); err != nil {
		return err
	}
	if p.accountProfile != nil {
		value, err := p.readAccountLocal(p.accountProfile.AccountID, provider, scope)
		if err != nil {
			return err
		}
		if err := p.accountModuleWriteError(value); err != nil {
			return err
		}
	}
	return p.requireAccountManagedWriteLocked()
}

// Caller owns the Manager read/write gate. Preserve a definite request refusal
// instead of interpreting every stopped outbox entry as a content conflict.
func (p *Manager) accountModuleWriteError(value *accountLocalKV) error {
	if value == nil {
		return nil
	}
	if value.Conflict {
		return dkvsindexer.ErrWriteConflict
	}
	if value.Key != "" {
		entries, err := newDKVSReplicaStore(p.db).LoadOutbox(p.dkvsReplicaNamespace())
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.OriginDomain != accountKVOutboxDomain || entry.OriginKey != value.Key || entry.State == DKVSOutboxAcknowledged {
				continue
			}
			if entry.State == DKVSOutboxConflict || entry.State == DKVSOutboxTerminal {
				return dkvsOutboxEntryError(entry)
			}
			if entry.LastError != "" || entry.LastErrorCode != "" {
				return ErrDKVSPathNotSynced
			}
		}
	}
	if value.LastError != "" {
		return fmt.Errorf("account module is unavailable: %s", value.LastError)
	}
	return nil
}

// CheckAccountRestore is for existing-operation reconciliation. It checks only
// incomplete local imports; it does not authorize a new external business action.
func (p *Manager) CheckAccountRestore(provider, scope string) error {
	if p == nil {
		return ErrAccountManagedDataImportIncomplete
	}
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.checkAccountRestore(provider, scope)
}

func (p *Manager) checkAccountRestore(provider, scope string) error {
	if err := p.checkAccountManagedDataImport(); err != nil {
		return err
	}
	if p.accountProfile == nil {
		return nil
	}
	value, err := p.readAccountLocal(p.accountProfile.AccountID, provider, scope)
	if err != nil {
		return err
	}
	if value != nil && value.Restoring {
		return ErrAccountManagedDataImportIncomplete
	}
	return nil
}

func (p *Manager) saveAccountKV(accountID, key string, encoded []byte) error {
	if p == nil || p.db == nil || len(encoded) == 0 {
		return fmt.Errorf("account value is unavailable")
	}
	if _, err := dkvsindexer.ParseKey(key); err != nil {
		return err
	}
	if !strings.HasPrefix(key, "/personal/"+accountID+"/account/") &&
		!strings.HasPrefix(key, "/blob/"+accountID+"/") {
		return fmt.Errorf("account KV belongs to another account")
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.saveAccountKVLocked(nil, accountID, key, encoded)
}

// Caller owns the Manager mutex; activation uses the same batch as its intent.
func (p *Manager) saveAccountKVLocked(batch indexer.WriteBatch, accountID, key string, encoded []byte) error {
	if p.accountProfile == nil || p.accountProfile.AccountID != accountID || len(p.accountSecret) != 32 {
		return ErrAccountManagementSecretConflict
	}
	value, err := p.readAccountLocal(accountID, "account", key)
	if err != nil {
		return err
	}
	if value == nil {
		value = &accountLocalKV{Version: 1, AccountID: accountID, Network: p.cfg.Chain, Provider: "account", Scope: key, Key: key, Codec: "v1"}
	}
	if !bytes.Equal(value.Value, encoded) {
		if value.Generation == ^uint64(0) {
			return fmt.Errorf("account change counter overflow")
		}
		value.Generation++
		value.Value = bytes.Clone(encoded)
		value.Captured = value.Generation
	}
	return p.writeAccountLocal(batch, value)
}

func (s *dkvsStore) saveAccountKV(accountID, key string, encoded []byte) error {
	if s == nil || s.manager == nil || s.manager.owner == nil {
		return ErrDKVSPathNotSynced
	}
	return s.manager.owner.saveAccountKV(accountID, key, encoded)
}
