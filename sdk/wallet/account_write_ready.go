package wallet

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
)

// A durable, healthy baseline can coexist with normal pending publications.
// This sentinel is scheduling, not evidence of a failed synchronization.
var errAccountSyncPending = errors.Join(ErrDKVSPathNotSynced, errDKVSJobDeferred)

type dkvsACKCleanupError struct{ cause error }

func (e *dkvsACKCleanupError) Error() string {
	return "confirmed DKVS ACK cleanup pending: " + e.cause.Error()
}
func (e *dkvsACKCleanupError) Unwrap() error { return e.cause }
func accountACKCleanupOnly(err error) bool {
	for err != nil {
		if _, ok := err.(*dkvsACKCleanupError); ok {
			return true
		}
		if _, joined := err.(interface{ Unwrap() []error }); joined {
			return false
		}
		err = errors.Unwrap(err)
	}
	return false
}

// The existing session-grant map owns this permission. A cold open starts with
// no grant; a verified account sync must establish one. Starting a normal sync
// does not withdraw the preceding successful grant.
func (p *Manager) accountWriteScopeLocked() string {
	if p.accountProfile == nil || !p.accountProfile.RecoveryConfigured || p.dkvs == nil {
		return ""
	}
	client, err := p.dkvs.primaryClient()
	if err != nil {
		return ""
	}
	return p.dkvs.sessionKey(client, "account-write:"+p.accountProfile.AccountID)
}

// Scope includes namespace, URL and observed endpoint. Client identity pins a
// replacement transport even at the same URL; done pins the worker lifecycle.
// An initially unknown endpoint may be learned by this same verified sync.
func (p *Manager) accountSyncResult(labels ...string) func(error) {
	source := "unspecified"
	if len(labels) != 0 {
		source = labels[0]
	}
	p.mutex.RLock()
	scope := p.accountWriteScopeLocked()
	manager := p.dkvs
	var client *SatsNetDKVSClient
	var done chan struct{}
	endpointKnown := false
	if manager != nil {
		client, _ = manager.primaryClient()
		manager.mu.Lock()
		done = manager.done
		manager.mu.Unlock()
	}
	if client != nil {
		client.endpointMu.RLock()
		endpointKnown = client.endpointID != ""
		client.endpointMu.RUnlock()
	}
	p.mutex.RUnlock()
	return func(err error) {
		outcome := "skip-capture"
		sameScope, sameClient, sameRun := false, false, false
		grantBefore, grantAfter := false, false
		learnedEndpoint := false
		endpointID := ""
		currentScope := ""
		defer func() {
			Log.Debugf("account authorization completion: manager=%d scope_tag=%s current_scope_tag=%s source=%s outcome=%s endpoint_known_at_start=%t endpoint_known_at_end=%t same_scope=%t same_client=%t same_run=%t result_nil=%t result_unsynced=%t result_pending=%t result_deferred=%t result_snapshot=%t result_canceled=%t result_deadline=%t result_cleanup=%t grant_before=%t grant_after=%t result_type=%T",
				p.debugID, accountTraceScope(scope), accountTraceScope(currentScope), source, outcome, endpointKnown, learnedEndpoint, sameScope, sameClient, sameRun,
				err == nil, errors.Is(err, ErrDKVSPathNotSynced), errors.Is(err, errAccountSyncPending), errors.Is(err, errDKVSJobDeferred), errors.Is(err, errAccountSnapshotChanged), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), accountACKCleanupOnly(err), grantBefore, grantAfter, err)
		}()
		if manager == nil || scope == "" || client == nil {
			return
		}
		client.endpointMu.RLock()
		endpointID = client.endpointID
		learnedEndpoint = endpointID != ""
		client.endpointMu.RUnlock()
		p.mutex.RLock()
		defer p.mutex.RUnlock()
		outcome = "skip-scope-or-lock"
		if p.dkvs != manager {
			return
		}
		currentScope = p.accountWriteScopeLocked()
		sameScope = currentScope == scope
		current, currentErr := manager.primaryClient()
		manager.mu.Lock()
		sameRun = manager.done == done
		manager.mu.Unlock()
		sameClient = current == client
		// The captured scope ends in an empty endpoint segment on a cold
		// client. Learning that endpoint is initialization, not a source
		// replacement. Keep every preceding scope field and the exact client
		// and worker pinned; success must have verified its online session.
		if !sameScope && !endpointKnown && learnedEndpoint && sameClient && sameRun &&
			currentScope == scope+endpointID {
			sameScope = err != nil || (manager.scopeReady(manager.sessionKey(client, "read")) &&
				manager.scopeReady(manager.sessionKey(client, "write")))
		}
		if !sameScope || len(p.accountSecret) != 32 || p.accountPassword == "" {
			return
		}
		outcome = "skip-client-or-run"
		if currentErr != nil || !sameClient || !sameRun {
			return
		}
		grantBefore = manager.scopeReady(currentScope)
		if err == nil {
			outcome = "grant-success"
			manager.markReady(currentScope)
		} else if !errors.Is(err, errAccountSyncPending) && !errors.Is(err, errAccountSnapshotChanged) &&
			!accountACKCleanupOnly(err) && !accountModuleOnly(err) {
			outcome = "revoke-error"
			p.failAccountSyncLocked(err)
		} else {
			outcome = "keep-normal-pending"
		}
		grantAfter = manager.scopeReady(currentScope)
	}
}

func (p *Manager) failAccountSyncLocked(_ error) {
	if scope := p.accountWriteScopeLocked(); scope != "" {
		p.dkvs.markNotReady(scope)
	}
}

// A retained exact ACK whose baseline is already persisted is cleanup work,
// not an unresolved business submission. Never infer this from absence alone.
func (p *Manager) accountACKCoveredLocked(entry *DKVSBatchOutboxEntry) (bool, error) {
	if entry == nil || entry.State != DKVSOutboxAcknowledged || entry.OriginDomain != accountKVOutboxDomain ||
		entry.OriginGeneration == 0 || p.accountProfile == nil || entry.Namespace != p.dkvsReplicaNamespace() {
		return false, nil
	}
	value, err := p.accountLocalForKey(p.accountProfile.AccountID, entry.OriginKey)
	if err != nil || value == nil {
		return false, err
	}
	return value.Confirmed >= entry.OriginGeneration, nil
}
func (p *Manager) accountACKCovered(entry *DKVSBatchOutboxEntry) (bool, error) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.accountACKCoveredLocked(entry)
}
func (p *Manager) requireAccountSyncLocked(client *SatsNetDKVSClient, wrapperKey, indexKey string) error {
	entries, err := newDKVSReplicaStore(p.db).LoadOutbox(client.replicaNamespace)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.OriginDomain != accountKVOutboxDomain || (entry.OriginKey != wrapperKey && entry.OriginKey != indexKey) {
			continue
		}
		if entry.State == DKVSOutboxConflict {
			return dkvsOutboxEntryError(entry)
		}
		if entry.State != DKVSOutboxAcknowledged && (entry.LastError != "" || entry.LastErrorCode != "") {
			return ErrDKVSPathNotSynced
		}
	}
	if scope := p.accountWriteScopeLocked(); scope == "" || !p.dkvs.scopeReady(scope) {
		return ErrDKVSPathNotSynced
	}
	return nil
}

func (p *Manager) finishLocalAccountACKs(client *SatsNetDKVSClient) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.finishReadyAccountACKsLocked()
}
func accountACKCleanup(entry *DKVSBatchOutboxEntry, err error) error {
	return &DKVSRequestPendingError{RequestID: entry.RequestID, Committed: true, Durable: true,
		Cause: &dkvsACKCleanupError{cause: fmt.Errorf("remove confirmed request: %w", err)}}
}

// Actual mutation preparation may finish local ACK metadata. UI/status checks
// continue to call requireAccountManagedWriteLocked and remain read-only.
func (p *Manager) prepareAccountWriteLocked() error {
	if err := p.accountDBFailure(); err != nil {
		return err
	}
	if err := p.checkAccountRestore("wallets", AccountManagedDataGlobalScope); err != nil {
		return err
	}
	if p.accountProfile != nil {
		value, err := p.readAccountLocal(p.accountProfile.AccountID, "wallets", AccountManagedDataGlobalScope)
		if err != nil {
			return err
		}
		if err := p.accountModuleWriteError(value); err != nil {
			return err
		}
	}
	return p.requireAccountManagedWriteLocked()
}

func (p *Manager) revokeAccountOutboxWrite(entry *DKVSBatchOutboxEntry) {
	if entry == nil || entry.OriginDomain != accountManagedOutboxDomain {
		return
	}
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if p.accountProfile == nil || p.dkvs == nil {
		return
	}
	info, err := p.accountManagementRootWalletLocked()
	if err != nil {
		return
	}
	key, err := p.accountManagedStateKey(cloneWalletAtAccountZero(info.Wallet))
	if err != nil || key != entry.OriginKey {
		return
	}
	// An old stored retry error alone is not a newly verified ACK repair.
	p.failAccountSyncLocked(ErrDKVSPathNotSynced)
}

// Storage authorization holds only a profile read lock while reserving its
// slot. Complete ACK metadata first, then retain its original read-only recheck.
func (p *Manager) prepareAccountWrite() error {
	if p == nil {
		return ErrDKVSPathNotSynced
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.prepareAccountWriteLocked()
}

// Diagnostic label only. Never print the account ID, endpoint URL or raw scope.
func accountTraceScope(scope string) string {
	if scope == "" {
		return "none"
	}
	digest := sha256.Sum256([]byte(scope))
	return fmt.Sprintf("%x", digest[:6])
}
