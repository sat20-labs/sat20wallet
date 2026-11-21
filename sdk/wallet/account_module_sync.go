package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func (p *Manager) accountModuleFailure(entry account.ManagedEntry, cause error, remote *dkvsValue) error {
	if errors.Is(cause, errAccountDBWrite) {
		// The import marker already survives. Do not write another failure
		// record to a database that has just refused its local commit.
		return p.stopAccountDB(cause)
	}
	if errors.Is(cause, errAccountSnapshotChanged) || errors.Is(cause, errWalletBaselineChanged) {
		return cause
	}
	if isDKVSNetworkFailure(cause) {
		return cause
	}
	err := func() error {
		p.mutex.Lock()
		defer p.mutex.Unlock()
		if p.accountProfile == nil {
			return errAccountSnapshotChanged
		}
		expected, err := accountEntry(p.accountSecret, p.accountProfile.AccountID, entry.Provider, entry.Scope)
		if err != nil || expected.Key != entry.Key {
			return errAccountSnapshotChanged
		}
		value, err := p.readAccountLocal(p.accountProfile.AccountID, entry.Provider, entry.Scope)
		if err != nil {
			return err
		}
		if value == nil {
			value = &accountLocalKV{Version: 1, AccountID: p.accountProfile.AccountID, Network: p.cfg.Chain,
				Provider: entry.Provider, Scope: entry.Scope, Key: entry.Key, Codec: entry.Codec}
		}
		value.LastError = cause.Error()
		value.Conflict = errors.Is(cause, dkvsindexer.ErrWriteConflict)
		if value.Conflict && remote != nil {
			value.ConflictValue = bytes.Clone(remote.Value)
		}
		return p.writeAccountLocal(nil, value)
	}()
	if err != nil {
		return err
	}
	return &accountModuleError{provider: entry.Provider, scope: entry.Scope, cause: cause}
}

func (p *Manager) importAccountValue(entry account.ManagedEntry, catalog AccountManagedDataCatalog,
	value *accountLocalKV, remote *dkvsValue, encoded, payload []byte, pending bool) error {
	// Wait for existing RGB actions before applying account data. A waiting
	// import also prevents new actions from entering the module.
	applied := false
	if entry.Provider == rgb11AccountManagedProviderID || entry.Provider == "wallets" {
		p.rgbManagedExecutionMu.Lock()
		defer func() {
			p.rgbManagedExecutionMu.Unlock()
			if applied && p.rgbManager != nil {
				for _, scope := range catalog.Scopes {
					if entry.Provider == "wallets" || scope.ID() == entry.Scope {
						p.rgbManager.scopeStates.wakeReconciliation(rgb11StorageScope(scope.WalletID, scope.AccountIndex))
					}
				}
			}
		}()
	}
	if entry.Provider == "wallets" {
		repaired, merged, err := p.keepAccountSilentPay(value.AccountID, payload)
		if err != nil {
			return err
		}
		if merged != nil {
			defer zeroBytes(merged)
			encoded, payload, pending = repaired, merged, true
		}
	}
	if err := p.validateAccountPayload(entry, catalog, payload); err != nil {
		return err
	}
	p.mutex.Lock()
	if p.accountProfile == nil || p.accountProfile.AccountID != value.AccountID ||
		p.cfg.Chain != value.Network || len(p.accountSecret) != 32 || p.accountPassword == "" {
		p.mutex.Unlock()
		return errAccountSnapshotChanged
	}
	live, err := p.readAccountLocal(value.AccountID, entry.Provider, entry.Scope)
	if err == nil && live != nil && live.Generation != value.Generation {
		err = errAccountSnapshotChanged
	}
	if err != nil {
		p.mutex.Unlock()
		return err
	}
	if live == nil {
		live = value
	}
	if pending && live.Generation == live.Confirmed {
		if live.Generation == ^uint64(0) {
			p.mutex.Unlock()
			return fmt.Errorf("account change counter overflow")
		}
		live.Generation++
	}
	live.Key, live.Codec = entry.Key, entry.Codec
	live.Value, live.Restoring = bytes.Clone(encoded), true
	live.Captured = live.Generation
	if remote != nil {
		live.Baseline = cloneDKVSRecord(remote.record)
	} else {
		live.Baseline = nil
	}
	err = p.writeAccountLocal(nil, live)
	p.mutex.Unlock()
	if err != nil {
		return err
	}
	if err := p.importAccountPayload(entry, catalog, payload); err != nil {
		return err
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	current, err := p.readAccountLocal(value.AccountID, entry.Provider, entry.Scope)
	if err != nil || current == nil {
		return err
	}
	if current.Generation != live.Generation {
		return errAccountSnapshotChanged
	}
	current.Restoring, current.Conflict, current.LastError = false, false, ""
	current.ConflictValue = nil
	if !pending {
		current.Confirmed = current.Generation
	}
	err = p.saveAccountReceipt(current)
	applied = err == nil
	return err
}

func (p *Manager) syncAccountImport(store *dkvsStore, snapshot *accountManagementSyncSnapshot,
	entry account.ManagedEntry, catalog AccountManagedDataCatalog, value *accountLocalKV,
	remote *dkvsValue, encoded, payload []byte, pending bool) error {
	if err := p.importAccountValue(entry, catalog, value, remote, encoded, payload, pending); err != nil {
		return p.accountModuleFailure(entry, err, remote)
	}
	if entry.Provider != "wallets" {
		return nil
	}
	p.mutex.RLock()
	live, err := p.readAccountLocal(value.AccountID, entry.Provider, entry.Scope)
	p.mutex.RUnlock()
	if err != nil {
		return err
	}
	// A repaired or merged wallet import is already captured and can be sent
	// now. An intervening edit is left to the normal backup worker.
	if live == nil || live.Generation == live.Confirmed || live.Generation != live.Captured {
		return nil
	}
	err = p.publishAccountValue(store, live, snapshot.root, snapshot.profile)
	if err != nil && !isDKVSNetworkFailure(err) && !accountACKCleanupOnly(err) {
		return p.accountModuleFailure(entry, err, remote)
	}
	return err
}

func (p *Manager) syncAccountModule(store *dkvsStore, snapshot *accountManagementSyncSnapshot,
	catalog AccountManagedDataCatalog, entry account.ManagedEntry) error {
	for attempt := 0; attempt < 3; attempt++ {
		err := p.syncAccountModuleOnce(store, snapshot, catalog, entry)
		if !errors.Is(err, errWalletBaselineChanged) {
			return err
		}
		if attempt < 2 {
			// Re-read the current local catalog as well as the verified remote
			// value; the prior attempt may already have merged wallet fields.
			catalog, err = p.accountManagedDataCatalog()
			if err != nil {
				return err
			}
			Log.Infof("account rebase diagnostic: manager=%d phase=wallet-rebuild key_tag=%s attempt=%d",
				p.debugID, accountTraceScope(entry.Key), attempt+2)
		}
	}
	return p.accountModuleFailure(entry, dkvsindexer.ErrWriteConflict, nil)
}

func (p *Manager) syncAccountModuleOnce(store *dkvsStore, snapshot *accountManagementSyncSnapshot,
	catalog AccountManagedDataCatalog, entry account.ManagedEntry) error {
	if entry.Deleted {
		return nil
	}
	if err := store.rememberAccountModule(snapshot.secret, snapshot.profile.AccountID, entry); err != nil {
		return err
	}
	if err := store.WaitReady(entry.Key); err != nil {
		return err
	}
	// Resolve this key's frozen request before exporting or replacing its
	// captured bytes. A pending request must retain its exact ciphertext;
	// after a durable ACK, receive again before comparing the next local edit.
	if err := p.pendingAccountSend(store, entry.Key); err != nil {
		if !isDKVSNetworkFailure(err) && !accountACKCleanupOnly(err) {
			return p.accountModuleFailure(entry, err, nil)
		}
		return err
	}
	if err := store.SyncCurrent(entry.Key); err != nil {
		return err
	}
	remote, err := store.Get(entry.Key)
	if errors.Is(err, ErrDKVSRecordNotFound) {
		remote, err = nil, nil
	}
	if err != nil {
		return err
	}
	// A tombstone has no module payload, but its verified record still owns
	// the CAS baseline when an explicitly reimported wallet republishes data.
	observed := remote
	if remote != nil && dkvsindexer.IsTombstone(remote.Flags) {
		remote = nil
	}
	p.mutex.RLock()
	value, err := p.readAccountLocal(snapshot.profile.AccountID, entry.Provider, entry.Scope)
	fresh := entry.Provider == rgb11AccountManagedProviderID && p.pendingAccountScopeAdd(entry.Scope)
	newWallets := make(map[string]bool)
	if entry.Provider == "wallets" && p.accountProfile != nil {
		for _, mutation := range p.accountProfile.Pending {
			if mutation.Type == accountMutationAddWallet {
				info := p.walletInfoByFingerprintLocked(mutation.Fingerprint)
				if info != nil && info.Id == mutation.WalletID {
					newWallets[mutation.Fingerprint] = true
				}
			}
		}
	}
	p.mutex.RUnlock()
	if err != nil {
		return err
	}
	if value != nil && value.Deleted {
		return nil
	}
	var remotePayload []byte
	if remote != nil && !fresh {
		remotePayload, err = openAccountPayload(snapshot.secret, snapshot.profile.AccountID, snapshot.network, entry, remote.Value)
		if err != nil {
			return p.accountModuleFailure(entry, err, remote)
		}
		defer zeroBytes(remotePayload)
		if err := p.validateAccountPayload(entry, catalog, remotePayload); err != nil {
			return p.accountModuleFailure(entry, err, remote)
		}
	}
	if value == nil || value.Restoring && len(value.Value) == 0 || value.Generation == 0 {
		if value == nil || value.Generation == 0 {
			value = &accountLocalKV{Version: 1, AccountID: snapshot.profile.AccountID, Network: p.cfg.Chain,
				Provider: entry.Provider, Scope: entry.Scope, Key: entry.Key, Codec: entry.Codec, Generation: 1}
		}
		if remote == nil {
			return p.accountModuleFailure(entry, ErrDKVSRecordNotFound, nil)
		}
		return p.syncAccountImport(store, snapshot, entry, catalog, value, remote, remote.Value, remotePayload, false)
	}
	if value.Restoring {
		encoded := value.Value
		payload, err := openAccountPayload(snapshot.secret, value.AccountID, snapshot.network, entry, encoded)
		if err == nil {
			err = p.validateAccountPayload(entry, catalog, payload)
		}
		if err != nil {
			zeroBytes(payload)
			// A failed remote restore has no newer local edits. Retry from the
			// independently verified remote copy instead of reopening damaged
			// persisted bytes forever. Never discard an unconfirmed local edit.
			if remote == nil || value.Generation != value.Confirmed {
				return p.accountModuleFailure(entry, err, remote)
			}
			encoded = remote.Value
			payload = bytes.Clone(remotePayload)
		}
		defer zeroBytes(payload)
		if err := p.importAccountValue(entry, catalog, value, observed, encoded, payload, value.Confirmed < value.Generation); err != nil {
			return p.accountModuleFailure(entry, err, remote)
		}
		p.mutex.RLock()
		value, err = p.readAccountLocal(snapshot.profile.AccountID, entry.Provider, entry.Scope)
		p.mutex.RUnlock()
		if err != nil || value == nil {
			return err
		}
	}
	// A clean local module needs no Export. Only that changed remote module is
	// imported; other scopes are not included in its provider catalog.
	if !fresh && value.Generation == value.Confirmed && len(value.Value) != 0 {
		localPayload, err := openAccountPayload(snapshot.secret, value.AccountID, snapshot.network, entry, value.Value)
		if err != nil {
			return p.accountModuleFailure(entry, err, remote)
		}
		defer zeroBytes(localPayload)
		if remote == nil {
			if err := p.validateAccountPayload(entry, catalog, localPayload); err != nil {
				return p.accountModuleFailure(entry, err, nil)
			}
			value, err = p.setAccountBaseline(value, observed)
			if err != nil {
				return err
			}
			return p.publishAccountValue(store, value, snapshot.root, snapshot.profile)
		}
		if bytes.Equal(localPayload, remotePayload) {
			value, err = p.captureAccountValue(value.AccountID, entry.Provider, entry.Scope, entry.Key, entry.Codec, remote.Value, value.Generation)
			if err != nil {
				return err
			}
			return p.confirmAccountValue(store, snapshot, value, remote)
		}
		return p.syncAccountImport(store, snapshot, entry, catalog, value, remote, remote.Value, remotePayload, false)
	}
	localPayload, err := p.exportAccountPayload(entry, catalog)
	if err != nil {
		return p.accountModuleFailure(entry, err, remote)
	}
	defer zeroBytes(localPayload)
	var basePayload []byte
	if !fresh && value.Baseline != nil && !dkvsindexer.IsTombstone(value.Baseline.Flags) {
		basePayload, err = openAccountPayload(snapshot.secret, value.AccountID, snapshot.network, entry, value.Baseline.Value)
		if err != nil {
			return p.accountModuleFailure(entry, err, remote)
		}
		defer zeroBytes(basePayload)
	}
	if remote != nil && bytes.Equal(localPayload, remotePayload) {
		captured, err := p.captureAccountValue(value.AccountID, entry.Provider, entry.Scope, entry.Key, entry.Codec, remote.Value, value.Generation)
		if err != nil {
			return err
		}
		return p.confirmAccountValue(store, snapshot, captured, remote)
	}
	if !fresh && remote != nil && !bytes.Equal(remotePayload, basePayload) {
		if len(newWallets) == 0 && bytes.Equal(localPayload, basePayload) {
			return p.syncAccountImport(store, snapshot, entry, catalog, value, remote, remote.Value, remotePayload, false)
		}
		var merged []byte
		if entry.Provider == "wallets" {
			merged, err = mergeAccountWalletPayload(basePayload, localPayload, remotePayload, newWallets)
		} else if merger, ok := p.accountProvider(entry.Provider).(AccountDataMerger); ok {
			selected, catalogErr := accountScopeCatalog(catalog, entry.Scope)
			if catalogErr != nil {
				return catalogErr
			}
			merged, err = merger.Merge(selected, entry.Scope, basePayload, localPayload, remotePayload)
		} else {
			if entry.Provider == rgb11AccountManagedProviderID {
				baseSeq, baseTag := uint64(0), "none"
				if value.Baseline != nil {
					baseSeq = value.Baseline.Seq
					baseTag = accountTraceScope(dkvsindexer.RecordHash(value.Baseline).String())
				}
				Log.Warningf("account RGB conflict diagnostic: at_ns=%d manager=%d branch=three-way key_tag=%s scope_tag=%s generation=%d captured=%d confirmed=%d base_seq=%d base_record_tag=%s remote_seq=%d remote_record_tag=%s base_payload_tag=%s local_payload_tag=%s remote_payload_tag=%s local_base_equal=%t local_remote_equal=%t base_remote_equal=%t base_bytes=%d local_bytes=%d remote_bytes=%d",
					time.Now().UnixNano(), p.debugID, accountTraceScope(entry.Key), accountTraceScope(entry.Scope),
					value.Generation, value.Captured, value.Confirmed, baseSeq, baseTag, remote.Seq, accountTraceScope(remote.Hash),
					accountTraceScope(string(basePayload)), accountTraceScope(string(localPayload)), accountTraceScope(string(remotePayload)),
					bytes.Equal(localPayload, basePayload), bytes.Equal(localPayload, remotePayload), bytes.Equal(basePayload, remotePayload),
					len(basePayload), len(localPayload), len(remotePayload))
			}
			err = dkvsindexer.ErrWriteConflict
		}
		if err != nil {
			return p.accountModuleFailure(entry, err, remote)
		}
		defer zeroBytes(merged)
		encoded, err := sealAccountPayload(snapshot.secret, value.AccountID, snapshot.network, entry, merged)
		if err != nil {
			return p.accountModuleFailure(entry, err, remote)
		}
		if entry.Provider == "wallets" {
			return p.syncAccountImport(store, snapshot, entry, catalog, value, remote, encoded, merged, true)
		}
		if err := p.importAccountValue(entry, catalog, value, remote, encoded, merged, true); err != nil {
			return p.accountModuleFailure(entry, err, remote)
		}
		localPayload = merged
	}
	encoded, err := sealAccountPayload(snapshot.secret, value.AccountID, snapshot.network, entry, localPayload)
	if err != nil {
		return p.accountModuleFailure(entry, err, remote)
	}
	// Public RGB identities keep their existing independent registration flow.
	// This network work is outside provider Export and all local business locks.
	if entry.Provider == rgb11AccountManagedProviderID {
		if err := p.prepareRGB11PublicBackup(account.ManagedDataBundle{Items: []account.ManagedDataItem{{Provider: entry.Provider, Scope: entry.Scope, Payload: localPayload}}}); err != nil {
			return p.accountModuleFailure(entry, err, remote)
		}
	}
	value, err = p.captureAccountValue(value.AccountID, entry.Provider, entry.Scope, entry.Key, entry.Codec, encoded, value.Generation)
	if err != nil {
		return err
	}
	value, err = p.setAccountBaseline(value, observed)
	if err != nil {
		return err
	}
	err = p.publishAccountValue(store, value, snapshot.root, snapshot.profile)
	if err != nil && !isDKVSNetworkFailure(err) && !accountACKCleanupOnly(err) {
		return p.accountModuleFailure(entry, err, remote)
	}
	return err
}

func (p *Manager) syncAccountModules(ctx context.Context) (result error) {
	if err := p.accountDBFailure(); err != nil {
		return err
	}
	defer func() { result = p.stopAccountDB(result) }()
	base, err := p.prepareAccountBaseline(ctx)
	if err != nil {
		return err
	}
	snapshot, store := base.snapshot, base.store
	index, remoteIndex, catalog := base.index, base.remoteIndex, base.catalog
	needsRestore, finish := base.needsRestore, base.finish
	defer zeroBytes(snapshot.secret)
	defer func() { finish(result) }()
	var failures []error
	failedKeys := make(map[string]bool)
	walletsReady := false
	ordered := make([]account.ManagedEntry, 0, len(index.Entries))
	for _, entry := range index.Entries {
		if entry.Provider == "wallets" {
			ordered = append(ordered, entry)
		}
	}
	for _, entry := range index.Entries {
		if entry.Provider != "wallets" {
			ordered = append(ordered, entry)
		}
	}
	for _, entry := range ordered {
		if entry.Deleted {
			continue
		}
		if err := p.syncAccountModule(store, snapshot, catalog, entry); err != nil {
			if err = p.stopAccountDB(err); accountSyncInterrupted(err) {
				return err
			}
			if entry.Provider == "wallets" && needsRestore {
				return err
			}
			failedKeys[entry.Key] = true
			failures = append(failures, err)
		} else if entry.Provider == "wallets" {
			walletsReady = true
		}
		catalog, err = p.accountManagedDataCatalog()
		if err != nil {
			return err
		}
	}
	if err := p.ensureAccountModules(catalog, index); err != nil {
		return err
	}
	known := accountEntryMap(index)
	p.mutex.RLock()
	values, err := p.listAccountLocal(snapshot.profile.AccountID)
	p.mutex.RUnlock()
	if err != nil {
		return err
	}
	for _, value := range values {
		if value.Provider == "account" {
			continue
		}
		if _, ok := known[value.Provider+"\x00"+value.Scope]; ok {
			continue
		}
		entry, err := accountEntry(snapshot.secret, value.AccountID, value.Provider, value.Scope)
		if err != nil {
			return err
		}
		if err := p.syncAccountModule(store, snapshot, catalog, entry); err != nil {
			if err = p.stopAccountDB(err); accountSyncInterrupted(err) {
				return err
			}
			failedKeys[entry.Key] = true
			failures = append(failures, err)
		} else if entry.Provider == "wallets" && !value.Deleted {
			walletsReady = true
		}
	}
	if snapshot.profile.StorageMode != AccountStorageTemporary || walletsReady || remoteIndex != nil && base.wrapper == nil {
		if err := p.publishAccountIndex(store, snapshot, remoteIndex, index, failedKeys); err != nil {
			return err
		}
		if base.wrapper != nil {
			if err := p.publishAccountValue(store, base.wrapper, snapshot.root, snapshot.profile); err != nil {
				return err
			}
		}
	}
	if err := p.renewAccountCache(store, snapshot, failedKeys); err != nil {
		if accountSyncInterrupted(err) || err == errAccountSnapshotChanged {
			return err
		}
		failures = append(failures, err)
	}
	if needsRestore || base.readyLater {
		finish(nil)
	}
	cleanup := false
	for _, entry := range index.Entries {
		cleanup = cleanup || entry.Deleted
	}
	for _, value := range values {
		cleanup = cleanup || value.Deleted
	}
	if cleanup {
		p.scheduleAccountCleanup()
	}
	return errors.Join(failures...)
}

func accountSyncInterrupted(err error) bool {
	return errors.Is(err, ErrAccountDBStopped) ||
		errors.Is(err, dkvsindexer.ErrEndpointMismatch) || errors.Is(err, ErrDKVSVerificationHeightRequired) ||
		isDKVSNetworkFailure(err)
}
