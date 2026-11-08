package wallet

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func accountEntryMap(value account.ManagedIndex) map[string]account.ManagedEntry {
	entries := make(map[string]account.ManagedEntry, len(value.Entries))
	for _, entry := range value.Entries {
		entries[entry.Provider+"\x00"+entry.Scope] = entry
	}
	return entries
}

func mergeAccountIndex(base, local, remote account.ManagedIndex) (account.ManagedIndex, error) {
	b, l, r := accountEntryMap(base), accountEntryMap(local), accountEntryMap(remote)
	keys := make(map[string]bool)
	for key := range b {
		keys[key] = true
	}
	for key := range l {
		keys[key] = true
	}
	for key := range r {
		keys[key] = true
	}
	result := account.ManagedIndex{Version: account.ManagedIndexVersion}
	for key := range keys {
		bv, bok := b[key]
		lv, lok := l[key]
		rv, rok := r[key]
		var value account.ManagedEntry
		var exists bool
		switch {
		case lok && lv.Deleted:
			value, exists = lv, true
		case rok && rv.Deleted:
			value, exists = rv, true
		case lok == bok && lv == bv:
			value, exists = rv, rok
		case rok == bok && rv == bv:
			value, exists = lv, lok
		case lok == rok && lv == rv:
			value, exists = lv, lok
		default:
			return account.ManagedIndex{}, fmt.Errorf("account index entry conflict")
		}
		if exists {
			result.Entries = append(result.Entries, value)
		}
	}
	return result, nil
}

func (p *Manager) accountModuleEntries(catalog AccountManagedDataCatalog,
	index account.ManagedIndex) ([]account.ManagedEntry, uint64, error) {
	p.mutex.RLock()
	current, err := p.accountManagedCatalogLocked()
	generation := p.accountGeneration
	var values []*accountLocalKV
	if err == nil && p.accountProfile != nil && reflect.DeepEqual(current, catalog) {
		values, err = p.listAccountLocal(catalog.AccountID)
	} else if err == nil {
		err = errAccountSnapshotChanged
	}
	p.mutex.RUnlock()
	if err != nil {
		return nil, 0, err
	}
	known := accountEntryMap(index)
	for _, value := range values {
		known[value.Provider+"\x00"+value.Scope] = account.ManagedEntry{
			Provider: value.Provider, Scope: value.Scope, Deleted: value.Deleted,
		}
	}
	entries := []account.ManagedEntry{{Provider: "wallets", Scope: AccountManagedDataGlobalScope}}
	for _, provider := range p.accountManagedDataProviders() {
		id := strings.TrimSpace(provider.ID())
		// An existing global module needs no wallet-scope discovery. Its
		// changes, like those of existing scoped modules, use StageChange.
		if _, exists := known[id+"\x00"+AccountManagedDataGlobalScope]; exists {
			continue
		}
		selected := catalog
		selected.Scopes = nil
		for _, scope := range catalog.Scopes {
			if _, exists := known[id+"\x00"+scope.ID()]; !exists {
				selected.Scopes = append(selected.Scopes, scope)
			}
		}
		if len(catalog.Scopes) != 0 && len(selected.Scopes) == 0 {
			continue
		}
		// Scope discovery uses the provider's actual local export, including
		// global payloads. Never call Export while holding the Manager gate.
		payloads, err := provider.Export(selected)
		if err != nil {
			return nil, 0, fmt.Errorf("export %s account-managed data: %w", id, err)
		}
		if err := provider.Validate(selected, payloads); err != nil {
			return nil, 0, fmt.Errorf("validate exported %s account-managed data: %w", id, err)
		}
		allowed, seen := accountManagedScopeSet(selected), make(map[string]bool, len(payloads))
		for _, payload := range payloads {
			if _, ok := allowed[payload.Scope]; !ok || len(payload.Payload) == 0 {
				return nil, 0, fmt.Errorf("provider %s exported invalid scope %q", id, payload.Scope)
			}
			if seen[payload.Scope] {
				return nil, 0, fmt.Errorf("provider %s exported duplicate scope %q", id, payload.Scope)
			}
			seen[payload.Scope] = true
			entries = append(entries, account.ManagedEntry{Provider: id, Scope: payload.Scope})
		}
	}
	return entries, generation, nil
}

func (p *Manager) ensureAccountModules(catalog AccountManagedDataCatalog, index account.ManagedIndex) error {
	entries, generation, err := p.accountModuleEntries(catalog, index)
	if err != nil {
		return err
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	current, err := p.accountManagedCatalogLocked()
	if err != nil {
		return err
	}
	if p.accountProfile == nil || p.accountGeneration != generation || !reflect.DeepEqual(current, catalog) {
		return errAccountSnapshotChanged
	}
	batch, err := NewAtomicWriteBatch(p.db)
	if err != nil {
		return err
	}
	defer batch.Close()
	registered := accountEntryMap(index)
	// WaitBackup may enter without a freshly fetched directory. Retain the
	// explicit deletion intents from the complete local directory as well.
	indexKey, err := account.ManagedIndexKey(catalog.AccountID)
	if err != nil {
		return err
	}
	stored, err := p.readAccountLocal(catalog.AccountID, "account", indexKey)
	if err != nil {
		return err
	}
	if stored != nil && len(stored.Value) != 0 {
		previous, err := account.OpenManagedIndex(p.accountSecret, catalog.AccountID, catalog.Network, stored.Value)
		if err != nil {
			return err
		}
		for id, entry := range accountEntryMap(previous) {
			if entry.Deleted {
				registered[id] = entry
			}
		}
	}
	ensure := func(provider, scope string) error {
		if entry, ok := registered[provider+"\x00"+scope]; ok && entry.Deleted {
			return nil
		}
		value, err := p.readAccountLocal(catalog.AccountID, provider, scope)
		if err != nil || value != nil {
			return err
		}
		return p.stageAccountChange(batch, provider, scope)
	}
	for _, entry := range entries {
		if err := ensure(entry.Provider, entry.Scope); err != nil {
			return err
		}
	}
	return accountDBWriteError(batch.Flush())
}

func (p *Manager) primeAccountImports(index account.ManagedIndex) (bool, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.accountProfile == nil {
		return false, ErrDKVSPathNotSynced
	}
	batch, err := NewAtomicWriteBatch(p.db)
	if err != nil {
		return false, err
	}
	defer batch.Close()
	needed := false
	for _, entry := range index.Entries {
		value, err := p.readAccountLocal(p.accountProfile.AccountID, entry.Provider, entry.Scope)
		if err != nil {
			return false, err
		}
		if entry.Deleted {
			// Adopt the verified deletion locally before any physical cleanup.
			// Old devices must not wait for blob I/O to learn this directory fact.
			if value != nil && value.Deleted {
				continue
			}
			if value == nil {
				value = &accountLocalKV{Version: 1, AccountID: p.accountProfile.AccountID, Network: p.cfg.Chain,
					Provider: entry.Provider, Scope: entry.Scope}
			}
			if value.Generation == ^uint64(0) {
				return false, fmt.Errorf("account change counter overflow")
			}
			value.Generation++
			value.Key, value.Codec, value.Deleted = entry.Key, entry.Codec, true
			value.Captured = value.Generation
			value.LastError, value.Conflict, value.Restoring = "", false, false
			if err := p.writeAccountLocal(batch, value); err != nil {
				return false, err
			}
			continue
		}
		if value != nil {
			needed = needed || value.Restoring
			continue
		}
		needed = true
		value = &accountLocalKV{Version: 1, AccountID: p.accountProfile.AccountID, Network: p.cfg.Chain,
			Provider: entry.Provider, Scope: entry.Scope, Key: entry.Key, Codec: entry.Codec, Generation: 1, Restoring: true}
		if err := p.writeAccountLocal(batch, value); err != nil {
			return false, err
		}
	}
	return needed, accountDBWriteError(batch.Flush())
}

func accountEntry(accountID, provider, scope string) (account.ManagedEntry, error) {
	entry := account.ManagedEntry{Provider: provider, Scope: scope, Codec: "v1"}
	var err error
	if provider == "wallets" && scope == AccountManagedDataGlobalScope {
		entry.Key, err = dkvsindexer.AccountPersonalKey(accountID, accountManagedStatePath)
		entry.Codec = "wallets-v1"
	} else {
		entry.Key, err = account.ManagedModuleKey(accountID, provider, scope)
	}
	if err == nil {
		_, err = dkvsindexer.ParseKey(entry.Key)
	}
	return entry, err
}

// The account has explicitly learned this key from its validated directory or
// local provider identity. Remember its canonical collection on the actual
// store; WaitReady then verifies and persists that source's subscription.
func (s *dkvsStore) rememberAccountModule(accountID string, entry account.ManagedEntry) error {
	if s == nil || s.manager == nil || s.client == nil {
		return ErrDKVSPathNotSynced
	}
	expected, err := accountEntry(accountID, entry.Provider, entry.Scope)
	if err != nil {
		return err
	}
	if entry.Key != expected.Key {
		return dkvsindexer.ErrInvalidKey
	}
	prefix, _, err := dkvsManagedPathForKey(entry.Key)
	if err != nil {
		return err
	}
	s.manager.rememberDirectories([]string{prefix})
	return nil
}

func (p *Manager) publishAccountIndex(store *dkvsStore, snapshot *accountManagementSyncSnapshot,
	remote *dkvsValue, index account.ManagedIndex) error {
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
	base := account.ManagedIndex{Version: account.ManagedIndexVersion}
	localIndex := index
	if local != nil && len(local.Value) != 0 {
		localIndex, err = account.OpenManagedIndex(snapshot.secret, snapshot.profile.AccountID, snapshot.network, local.Value)
		if err != nil {
			return err
		}
		if local.Baseline != nil {
			base, err = account.OpenManagedIndex(snapshot.secret, snapshot.profile.AccountID, snapshot.network, local.Baseline.Value)
			if err != nil {
				return err
			}
		}
	}
	index, err = mergeAccountIndex(base, localIndex, index)
	if err != nil {
		return err
	}
	entries := accountEntryMap(index)
	p.mutex.RLock()
	values, err := p.listAccountLocal(snapshot.profile.AccountID)
	p.mutex.RUnlock()
	if err != nil {
		return err
	}
	for _, value := range values {
		if value.Provider == "account" || value.Key == "" || value.Confirmed == 0 && !value.Deleted {
			continue
		}
		id := value.Provider + "\x00" + value.Scope
		if existing, ok := entries[id]; ok && existing.Deleted {
			continue
		}
		entry := account.ManagedEntry{Provider: value.Provider, Scope: value.Scope, Key: value.Key, Codec: value.Codec, Deleted: value.Deleted}
		entries[id] = entry
	}
	index.Entries = nil
	for _, entry := range entries {
		index.Entries = append(index.Entries, entry)
	}
	index, err = account.NormalizeManagedIndex(snapshot.profile.AccountID, index)
	if err != nil {
		return err
	}
	if remote != nil {
		current, err := account.OpenManagedIndex(snapshot.secret, snapshot.profile.AccountID, snapshot.network, remote.Value)
		if err != nil {
			return err
		}
		if reflect.DeepEqual(current, index) {
			if err := p.saveAccountKV(snapshot.profile.AccountID, key, remote.Value); err != nil {
				return err
			}
			p.mutex.RLock()
			value, err := p.readAccountLocal(snapshot.profile.AccountID, "account", key)
			p.mutex.RUnlock()
			if err != nil {
				return err
			}
			return p.recordAccountEffect(value, remote)
		}
	}
	var encoded []byte
	if local != nil && len(local.Value) != 0 && reflect.DeepEqual(localIndex, index) {
		encoded = append([]byte(nil), local.Value...)
	} else {
		encoded, err = account.SealManagedIndex(snapshot.secret, snapshot.profile.AccountID, snapshot.network, index, nil)
		if err != nil {
			return err
		}
	}
	if err := p.saveAccountKV(snapshot.profile.AccountID, key, encoded); err != nil {
		return err
	}
	p.mutex.Lock()
	value, err := p.readAccountLocal(snapshot.profile.AccountID, "account", key)
	if err == nil && value != nil {
		value.Baseline = nil
		if remote != nil {
			value.Baseline = cloneDKVSRecord(remote.record)
		}
		err = p.writeAccountLocal(nil, value)
	}
	p.mutex.Unlock()
	if err != nil {
		return err
	}
	return p.publishAccountValue(store, value, snapshot.root, snapshot.profile)
}
