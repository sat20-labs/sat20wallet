package wallet

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// A changed verified wallet-catalog baseline needs a fresh SDK merge before
// another publication. It is not a conflict between the business fields yet.
var errWalletBaselineChanged = fmt.Errorf("wallet publication baseline changed: %w", dkvsindexer.ErrWriteConflict)

// captureAccountValue freezes exactly the count exported by the provider. A
// newer business commit keeps its marker and forces a new export instead.
func (p *Manager) captureAccountValue(accountID, provider, scope, key, codec string,
	encoded []byte, generation uint64) (*accountLocalKV, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.accountProfile == nil || p.accountProfile.AccountID != accountID || len(p.accountSecret) != 32 {
		return nil, errAccountSnapshotChanged
	}
	value, err := p.readAccountLocal(accountID, provider, scope)
	if err != nil {
		return nil, err
	}
	if value == nil || value.Generation != generation {
		return nil, errAccountSnapshotChanged
	}
	if value.Key == key && value.Codec == codec && value.Captured == generation && bytes.Equal(value.Value, encoded) {
		return value, nil
	}
	value.Key, value.Codec = key, codec
	value.Value = bytes.Clone(encoded)
	value.Captured = generation
	if err := p.writeAccountLocal(nil, value); err != nil {
		return nil, err
	}
	return value, nil
}

func (p *Manager) publishAccountValue(store *dkvsStore, value *accountLocalKV,
	root common.Wallet, profile accountManagementProfile) error {
	return p.sendAccountValue(store, value, root, profile, false)
}

func (p *Manager) sendAccountValue(store *dkvsStore, value *accountLocalKV,
	root common.Wallet, profile accountManagementProfile, renew bool) error {
	if value == nil || value.Captured == 0 || value.Key == "" || !value.Deleted && len(value.Value) == 0 {
		return fmt.Errorf("account snapshot is not captured")
	}
	if err := p.pendingAccountSend(store, value.Key); err != nil {
		return err
	}
	ttl := profile.RecordTTL
	if profile.StorageMode == AccountStorageTemporary && !value.Deleted {
		ctx := store.readCtx
		if ctx == nil {
			ctx = store.client.requestContext()
		}
		config, err := store.storageConfig(ctx, store.client)
		if err != nil {
			return err
		}
		if err := store.manager.validateVerificationEndpoint(store.client, config.EndpointID); err != nil {
			return err
		}
		if !config.FreeLocal.Enabled || config.FreeLocal.MaxTTL == 0 {
			return dkvsindexer.ErrFreeLocalDisabled
		}
		ttl = min(ttl, config.FreeLocal.MaxTTL)
	}
	var issueHeight uint64
	if renew {
		issueHeight, _ = store.manager.verificationHeight()
		// A smaller policy or a reorg must never shorten a valid lease.
		if estimatedDKVSExpiryHeight(issueHeight, ttl) <= dkvsindexer.RecordExpiryHeight(value.Baseline) {
			return nil
		}
	}
	source := store.manager.sessionKey(store.client, "write")
	store.manager.mu.Lock()
	run := store.manager.done
	store.manager.mu.Unlock()
	_, err := store.updateWithOutboxOrigin([]string{value.Key}, func(observed map[string]*dkvsValue,
		_ map[string]uint64) ([]dkvsValueMutation, error) {
		p.mutex.RLock()
		current := p.accountProfile != nil && p.accountProfile.AccountID == profile.AccountID &&
			p.accountProfile.RootFingerprint == profile.RootFingerprint && p.dkvs == store.manager &&
			p.accountProfile.Location == profile.Location && p.cfg.Chain == value.Network &&
			p.accountProfile.PackageID == profile.PackageID && p.accountProfile.StorageMode == profile.StorageMode &&
			p.accountProfile.RecordTTL == profile.RecordTTL && p.accountProfile.AutopayContract == profile.AutopayContract
		var readErr error
		if current {
			var live *accountLocalKV
			live, readErr = p.readAccountLocal(value.AccountID, value.Provider, value.Scope)
			current = readErr == nil && live != nil && live.Deleted == value.Deleted &&
				live.Generation == value.Generation && live.Captured == value.Captured
		}
		p.mutex.RUnlock()
		if readErr != nil {
			return nil, readErr
		}
		client, clientErr := store.manager.primaryClient()
		store.manager.mu.Lock()
		sameRun := store.manager.done == run
		store.manager.mu.Unlock()
		current = current && clientErr == nil && client == store.client && sameRun &&
			store.manager.sessionKey(store.client, "write") == source
		if !current {
			return nil, errAccountSnapshotChanged
		}
		actual := observed[value.Key]
		if value.Baseline == nil && actual != nil || value.Baseline != nil &&
			(actual == nil || actual.Hash != dkvsindexer.RecordHash(value.Baseline).String()) {
			expectedSeq, actualSeq := uint64(0), uint64(0)
			expectedTag, actualTag := "none", "none"
			if value.Baseline != nil {
				expectedSeq = value.Baseline.Seq
				expectedTag = accountTraceScope(dkvsindexer.RecordHash(value.Baseline).String())
			}
			if actual != nil {
				actualSeq, actualTag = actual.Seq, accountTraceScope(actual.Hash)
			}
			Log.Warningf("account baseline diagnostic: at_ns=%d manager=%d branch=publish-baseline key_tag=%s scope_tag=%s generation=%d captured=%d confirmed=%d expected_seq=%d expected_record_tag=%s actual_seq=%d actual_record_tag=%s",
				time.Now().UnixNano(), p.debugID, accountTraceScope(value.Key), accountTraceScope(value.Scope),
				value.Generation, value.Captured, value.Confirmed, expectedSeq, expectedTag, actualSeq, actualTag)
			if value.Provider == "wallets" && value.Scope == AccountManagedDataGlobalScope && !value.Deleted {
				return nil, errWalletBaselineChanged
			}
			// Two devices can discover the same new RGB scope before either
			// publishes it. Re-read and compare their verified module payloads.
			if value.Provider == rgb11AccountManagedProviderID && !value.Deleted && value.Baseline == nil && actual != nil {
				return nil, errWalletBaselineChanged
			}
			return nil, dkvsindexer.ErrWriteConflict
		}
		content := value.Value
		if value.Deleted {
			content = []byte{1}
		}
		mutation, err := accountStateMutation(&profile, root, value.Key, content)
		if err != nil {
			return nil, err
		}
		mutation.Tombstone = value.Deleted
		mutation.Policy.TTL = ttl
		mutation.IssueHeight = issueHeight
		return []dkvsValueMutation{mutation}, nil
	}, dkvsOutboxOrigin{Key: value.Key, Domain: accountKVOutboxDomain, Generation: value.Captured})
	return err
}

// The normal sync has verified the current receipts on the bound node.
// Renew confirmed live values with the same ciphertext and CAS baseline.
func (p *Manager) renewAccountCache(store *dkvsStore, snapshot *accountManagementSyncSnapshot, failedKeys map[string]bool) error {
	if snapshot.profile.StorageMode != AccountStorageTemporary {
		return nil
	}
	height, known := store.manager.verificationHeight()
	if !known {
		return ErrDKVSVerificationHeightRequired
	}
	p.mutex.RLock()
	// Sync may already have imported a new wallet catalog. Renewal uses
	// current confirmed rows and pins the recovery target, not the catalog.
	profile := p.accountProfile
	if profile == nil || !profile.RecoveryConfigured || profile.Activation != nil ||
		p.dkvs != store.manager || _chain != snapshot.network ||
		profile.AccountID != snapshot.profile.AccountID || profile.RootFingerprint != snapshot.profile.RootFingerprint ||
		profile.Location != snapshot.profile.Location || profile.PackageID != snapshot.profile.PackageID ||
		profile.StorageMode != snapshot.profile.StorageMode || profile.RecordTTL != snapshot.profile.RecordTTL ||
		profile.AutopayContract != snapshot.profile.AutopayContract ||
		p.accountPassword != snapshot.password || !bytes.Equal(p.accountSecret, snapshot.secret) {
		p.mutex.RUnlock()
		return errAccountSnapshotChanged
	}
	client, err := store.manager.primaryClient()
	if err != nil || client != store.client {
		p.mutex.RUnlock()
		return errAccountSnapshotChanged
	}
	controls, err := p.accountControlKeysLocked()
	if err != nil {
		p.mutex.RUnlock()
		return err
	}
	values, err := p.listAccountLocal(snapshot.profile.AccountID)
	p.mutex.RUnlock()
	if err != nil {
		return err
	}
	var failures []error
	for _, value := range values {
		if failedKeys[value.Key] || value.Deleted || value.Restoring || value.Conflict || value.Generation != value.Confirmed ||
			value.Provider == "account" && !controls[value.Key] || !accountStorageConfirmed(value, &snapshot.profile) {
			continue
		}
		record := value.Baseline
		expiry := dkvsindexer.RecordExpiryHeight(record)
		if !bytes.Equal(value.Value, record.Value) || expiry <= height ||
			record.IssueHeight >= height || expiry-height > max(uint64(1), record.TTL/4) {
			continue
		}
		if err := p.sendAccountValue(store, value, snapshot.root, snapshot.profile, true); err != nil {
			if err = p.stopAccountDB(err); accountSyncInterrupted(err) {
				return err
			}
			if value.Provider != "account" {
				err = &accountModuleError{provider: value.Provider, scope: value.Scope, cause: err}
			}
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// Equal content can still need a storage-policy migration. Reuse the exact
// observed ciphertext and baseline through the existing signed CAS sender.
func (p *Manager) confirmAccountValue(store *dkvsStore, snapshot *accountManagementSyncSnapshot,
	value *accountLocalKV, remote *dkvsValue) error {
	if accountRecordMatchesStorage(remote, &snapshot.profile) {
		return p.recordAccountEffect(value, remote)
	}
	value, err := p.setAccountBaseline(value, remote)
	if err != nil {
		return err
	}
	return p.publishAccountValue(store, value, snapshot.root, snapshot.profile)
}

// recordAccountEffect confirms equal content observed at the current service.
// It does not claim that an older request completed, and never clears N+1.
func (p *Manager) recordAccountEffect(value *accountLocalKV, remote *dkvsValue) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.accountProfile == nil || p.accountProfile.AccountID != value.AccountID ||
		remote == nil || remote.record == nil || !bytes.Equal(value.Value, remote.Value) {
		return errAccountSnapshotChanged
	}
	live, err := p.readAccountLocal(value.AccountID, value.Provider, value.Scope)
	if err != nil || live == nil {
		if err != nil {
			return err
		}
		return errAccountSnapshotChanged
	}
	if live.Generation < value.Captured {
		return errAccountSnapshotChanged
	}
	if live.Baseline != nil && (remote.record.Seq < live.Baseline.Seq ||
		remote.record.Seq == live.Baseline.Seq && dkvsindexer.RecordHash(remote.record) != dkvsindexer.RecordHash(live.Baseline)) {
		Log.Warningf("account baseline diagnostic: at_ns=%d manager=%d branch=record-effect key_tag=%s scope_tag=%s generation=%d captured=%d confirmed=%d before_seq=%d after_seq=%d before_record_tag=%s after_record_tag=%s",
			time.Now().UnixNano(), p.debugID, accountTraceScope(live.Key), accountTraceScope(live.Scope),
			live.Generation, live.Captured, live.Confirmed, live.Baseline.Seq, remote.record.Seq,
			accountTraceScope(dkvsindexer.RecordHash(live.Baseline).String()), accountTraceScope(dkvsindexer.RecordHash(remote.record).String()))
	}
	live.Baseline = cloneDKVSRecord(remote.record)
	if live.Confirmed < value.Captured {
		live.Confirmed = value.Captured
	}
	if live.Generation == value.Captured {
		live.LastError = ""
		live.Conflict, live.ConflictValue = false, nil
	}
	return p.saveAccountReceipt(live)
}

// Update only the baseline of the exact captured generation. Never write an
// earlier struct over a business StageChange that committed N+1 meanwhile.
func (p *Manager) setAccountBaseline(value *accountLocalKV, remote *dkvsValue) (*accountLocalKV, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	live, err := p.readAccountLocal(value.AccountID, value.Provider, value.Scope)
	if err != nil {
		return nil, err
	}
	if live == nil || live.Generation != value.Generation || live.Captured != value.Captured {
		return nil, errAccountSnapshotChanged
	}
	if live.Baseline != nil && remote != nil && remote.record != nil &&
		(remote.record.Seq < live.Baseline.Seq || remote.record.Seq == live.Baseline.Seq &&
			dkvsindexer.RecordHash(remote.record) != dkvsindexer.RecordHash(live.Baseline)) {
		Log.Warningf("account baseline diagnostic: at_ns=%d manager=%d branch=set-baseline key_tag=%s scope_tag=%s generation=%d captured=%d confirmed=%d before_seq=%d after_seq=%d before_record_tag=%s after_record_tag=%s",
			time.Now().UnixNano(), p.debugID, accountTraceScope(live.Key), accountTraceScope(live.Scope),
			live.Generation, live.Captured, live.Confirmed, live.Baseline.Seq, remote.record.Seq,
			accountTraceScope(dkvsindexer.RecordHash(live.Baseline).String()), accountTraceScope(dkvsindexer.RecordHash(remote.record).String()))
	}
	live.Baseline = nil
	if remote != nil {
		live.Baseline = cloneDKVSRecord(remote.record)
	}
	if err := p.writeAccountLocal(nil, live); err != nil {
		return nil, err
	}
	return live, nil
}
