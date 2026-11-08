package wallet

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const (
	dkvsIdleSyncInterval = time.Minute
	dkvsSyncRetryDelay   = 5 * time.Second
)

var ErrDKVSPathNotSynced = errors.New("DKVS managed path is not synchronized")

func (p *Manager) dkvsReplicaNamespace() string {
	if p == nil || p.cfg == nil {
		return ""
	}
	return strings.Join([]string{p.cfg.Env, p.cfg.Chain}, ":")
}
func (p *Manager) dkvsReplicaNamespaceFor(_, _, _ string) string { return p.dkvsReplicaNamespace() }

func (m *dkvsManager) desiredPrefixes(store *dkvsReplicaStore, namespace string) ([]string, error) {
	persisted, err := store.LoadRegisteredPrefixes(namespace)
	if err != nil && !errors.Is(err, indexercommon.ErrKeyNotFound) {
		return nil, err
	}
	m.mu.Lock()
	memory := make([]string, 0, len(m.paths))
	for prefix := range m.paths {
		memory = append(memory, prefix)
	}
	m.mu.Unlock()
	prefixes, err := mergeSubscriptionPrefixes(persisted, memory)
	if err != nil {
		return nil, err
	}
	if !sameStringList(persisted, prefixes) {
		if err := store.PersistRegisteredPrefixes(namespace, prefixes); err != nil {
			return nil, err
		}
	}
	return prefixes, nil
}

func (m *dkvsManager) subscriptionEndpointConfig(client *SatsNetDKVSClient) (*dkvsindexer.ClientConfig, error) {
	if client == nil {
		return nil, ErrDKVSPathNotSynced
	}
	config, err := client.GetDKVSClientConfig()
	if err != nil {
		return nil, err
	}
	if config == nil || strings.TrimSpace(config.EndpointID) == "" {
		return nil, dkvsindexer.ErrStaleEndpoint
	}
	return config, nil
}

func (m *dkvsManager) validateSubscriptionEndpointSwitch(store *dkvsReplicaStore, namespace string,
	state *DKVSSubscriptionState, config *dkvsindexer.ClientConfig) error {
	if state == nil || state.EndpointID == "" || config == nil || state.EndpointID == config.EndpointID {
		return nil
	}
	active, err := store.HasActiveFreeLocal(namespace)
	if err != nil {
		return err
	}
	pending, err := store.PendingFreeLocalOutbox(namespace)
	if err != nil {
		return err
	}
	if active || pending {
		return dkvsindexer.ErrLocalOnlyEndpointMismatch
	}
	// A new source requires an explicit source/baseline decision. A larger
	// counter on another endpoint never authorizes destructive replacement.
	return dkvsindexer.ErrEndpointMismatch
}

func prefixStateReady(state *DKVSSubscriptionState, prefixes []string) bool {
	if state == nil || (state.Status != DKVSSubscriptionReady && state.Status != DKVSSubscriptionOfflineReady) || state.EndpointID == "" || !sameStringList(state.Prefixes, prefixes) {
		return false
	}
	for _, prefix := range prefixes {
		if _, found := state.Generations[prefix]; !found {
			return false
		}
	}
	return true
}

func (m *dkvsManager) syncManagedPrefixes(client *SatsNetDKVSClient, store *dkvsReplicaStore, prefixes []string, force bool) (changed []string, err error) {
	return m.syncPrefixSet(client, store, prefixes, force, true, nil, 0)
}

// Group receiving retains the existing subscription-wide publication check.
// A targeted Refresh/SyncCurrent only installs the explicitly requested set.
func (m *dkvsManager) syncPrefixSet(client *SatsNetDKVSClient, store *dkvsReplicaStore, prefixes []string,
	force, expand bool, first *dkvsindexer.ActivePage, firstAfter uint64) (changed []string, err error) {
	if m == nil || client == nil || store == nil || len(prefixes) == 0 {
		return nil, ErrDKVSPathNotSynced
	}
	prefixes, err = normalizeWalletSubscriptionPrefixes(prefixes)
	if err != nil {
		return nil, err
	}
	wasWritable := m.scopeReady(m.sessionKey(client, "write"))
	defer func() {
		if err != nil {
			m.markNotReady(m.sessionKey(client, "write"))
		}
		if err != nil && isDKVSNetworkFailure(err) {
			replica := newDKVSReplicaStore(m.owner.db)
			if !m.scopeReady(m.sessionKey(client, "read")) {
				if cacheErr := replica.ValidateActiveCache(client.replicaNamespace); cacheErr != nil {
					err = errors.Join(err, cacheErr)
					return
				}
			}
			if replica.MarkOfflineReady(client.replicaNamespace) == nil {
				m.markReady(m.sessionKey(client, "read"))
			}
		}
	}()
	config, err := m.subscriptionEndpointConfig(client)
	if err != nil {
		return nil, err
	}
	state, err := store.LoadSubscriptionState(client.replicaNamespace)
	if err != nil && !errors.Is(err, indexercommon.ErrKeyNotFound) {
		return nil, err
	}
	if err := m.validateSubscriptionEndpointSwitch(store, client.replicaNamespace, state, config); err != nil {
		return nil, err
	}
	registered, err := store.LoadRegisteredPrefixes(client.replicaNamespace)
	if err != nil {
		return nil, err
	}
	all, err := mergeSubscriptionPrefixes(registered, prefixes)
	if err != nil {
		return nil, err
	}
	// Refreshing a complete replica does not invalidate its confirmed data.
	// A new prefix set or source still requires the normal initial-sync gate.
	complete := prefixStateReady(state, all) && state.EndpointID == config.EndpointID
	if !complete {
		if err := store.PreparePrefixSync(client.replicaNamespace, config.EndpointID, all, state == nil || state.EndpointID == ""); err != nil {
			return nil, err
		}
	}
	requested := make(map[string]bool, len(prefixes))
	for _, prefix := range prefixes {
		requested[prefix] = true
	}
	targets := prefixes
	if expand {
		targets = all
	}
	installed := false
	for attempt := 0; attempt < 3; attempt++ {
		watchPage := first
		first = nil // A retry must use a fresh source view, not a consumed Page.
		usedWatchPage := false
		candidates := make([]dkvscore.ActiveInstall, 0, len(targets))
		for _, prefix := range targets {
			scope := dkvsindexer.ActiveScope{Prefix: prefix}
			baseline, e := store.ActiveBaseline(client.replicaNamespace, scope)
			if e != nil {
				return nil, e
			}
			previous, e := store.LoadActiveMeta(client.replicaNamespace, scope)
			if e != nil && !errors.Is(e, indexercommon.ErrKeyNotFound) {
				return nil, e
			}
			var initial *dkvsindexer.ActivePage
			// Unaffected collections need only a metadata read when unchanged.
			// Still include their baseline/root in the atomic publication check.
			if complete && !force && !requested[prefix] && previous != nil {
				page, e := client.GetActivePage(m.requestContext(), dkvsindexer.ActiveSyncRequest{Scope: scope, EndpointID: config.EndpointID, After: previous.Generation, PageSize: 1})
				if e != nil {
					return nil, e
				}
				if page.Meta.Generation == previous.Generation && page.Meta.Root == previous.Root && page.Meta.ViewHeight == previous.ViewHeight {
					candidates = append(candidates, dkvscore.ActiveInstall{Baseline: baseline, Meta: page.Meta})
					continue
				}
				initial = page // Reuse a changed sibling's probe instead of downloading it again.
			}
			request := dkvsindexer.ActiveSyncRequest{Scope: scope, EndpointID: config.EndpointID, Full: force || previous == nil}
			if previous != nil {
				request.After = previous.Generation
			}
			if !request.Full && watchPage != nil && dkvsindexer.SameActiveScope(watchPage.Meta.Scope, scope) {
				if previous.Generation >= watchPage.Meta.Generation && previous.Root == watchPage.Meta.Root && previous.ViewHeight >= watchPage.Meta.ViewHeight {
					// A foreground receive already installed this content. The group
					// validation below still checks the current source before commit.
					candidates = append(candidates, dkvscore.ActiveInstall{Baseline: baseline, Meta: *previous})
					continue
				}
				if previous.Generation == firstAfter {
					initial, usedWatchPage = watchPage, true
				}
			}
			meta, records, e := client.collectActivePages(m.requestContext(), request, initial)
			if e != nil {
				if complete && isDKVSNetworkFailure(e) {
					e = errors.Join(e, store.MarkOfflineReady(client.replicaNamespace))
				}
				return nil, e
			}
			candidates = append(candidates, dkvscore.ActiveInstall{Baseline: baseline, Meta: meta, Records: records, Full: request.Full})
		}
		// Collect first, then validate the whole vector. With monotonic source
		// generations this rejects a transaction straddling the collection reads.
		consistent := true
		if len(candidates) > 1 {
			for _, candidate := range candidates {
				page, e := client.GetActivePage(m.requestContext(), dkvsindexer.ActiveSyncRequest{Scope: candidate.Meta.Scope, EndpointID: config.EndpointID, After: candidate.Meta.Generation, PageSize: 1})
				if e != nil {
					return nil, e
				}
				if page.Meta.Generation != candidate.Meta.Generation || page.Meta.Root != candidate.Meta.Root || page.Meta.ViewHeight != candidate.Meta.ViewHeight {
					consistent = false
					break
				}
			}
		}
		if !consistent {
			continue
		}
		// Only local replica publication shares the account commit gate. Fetch
		// and verification above never hold it across network requests.
		m.owner.mutex.Lock()
		changed, err = store.InstallActiveStates(client.replicaNamespace, candidates)
		m.owner.mutex.Unlock()
		if usedWatchPage && errors.Is(err, dkvsindexer.ErrStaleEndpoint) {
			// A foreground ACK may have completed after Watch captured its Page.
			// Discard that response once; fresh reads keep all source/ACK checks.
			continue
		}
		if errors.Is(err, dkvsindexer.ErrPathDiverged) && !force {
			force = true
			continue
		}
		if errors.Is(err, dkvsindexer.ErrConcurrentUpdate) || errors.Is(err, dkvsindexer.ErrStaleGeneration) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, candidate := range candidates {
			m.observeEndpointVerificationHeight(candidate.Meta.ViewHeight)
		}
		installed = true
		break
	}
	if err != nil {
		return nil, err
	}
	if !installed {
		return nil, dkvsindexer.ErrConcurrentUpdate
	}

	if !sameStringList(targets, all) {
		// A partial refresh is not a cold-start/reconnect grant for unreceived
		// subscriptions. The existing background initial sync owns that gate.
		if !wasWritable {
			return changed, nil
		}
		current, loadErr := store.LoadSubscriptionState(client.replicaNamespace)
		if loadErr != nil {
			return changed, loadErr
		}
		for _, prefix := range all {
			if _, found := current.Generations[prefix]; !found {
				return changed, nil
			}
		}
	}
	if err := store.CompletePrefixSync(client.replicaNamespace, config.EndpointID, all); err != nil {
		return changed, err
	}
	m.markReady(m.sessionKey(client, "read"))
	// A continuing online session permits newer local edits while its current
	// request is in flight. Startup/recovery must first reconcile old requests.
	if wasWritable {
		m.markReady(m.sessionKey(client, "write"))
	} else if err := m.grantReconciledWriteSession(client, store); err != nil {
		return changed, err
	}
	return changed, nil
}

// Called while the transport coordinator is owned, after verified receiving.
// Read readiness alone is not permission to submit a new recovery-session intent.
func (m *dkvsManager) grantReconciledWriteSession(client *SatsNetDKVSClient, store *dkvsReplicaStore) error {
	entries, err := store.LoadOutbox(client.replicaNamespace)
	if err != nil {
		if errors.Is(err, dkvsindexer.ErrInvalidRecord) || errors.Is(err, dkvsindexer.ErrBatchTooLarge) {
			return err
		}
		return &dkvsLocalOutboxReadError{cause: err}
	}
	// Every unresolved request is checked at the exact dependency boundary by
	// requireWritePaths. A verified online receiver can grant unrelated keys.
	for _, entry := range entries {
		if _, err := dkvsOutboxEntryDependencies(entry); err != nil {
			return err
		}
		switch entry.State {
		case DKVSOutboxPending, DKVSOutboxInflight, DKVSOutboxAcknowledged,
			DKVSOutboxConflict, DKVSOutboxTerminal:
		default:
			return dkvsindexer.ErrInvalidRecord
		}
	}
	if m.scopeReady(m.sessionKey(client, "read")) {
		m.markReady(m.sessionKey(client, "write"))
	}
	return nil
}

func (m *dkvsManager) ensureCurrentSubscription(client *SatsNetDKVSClient) error {
	if m == nil || m.owner == nil || m.owner.db == nil || client == nil {
		return ErrDKVSPathNotSynced
	}
	var changed []string
	err := m.runTransport(func() error {
		store := newDKVSReplicaStore(m.owner.db)
		prefixes, err := m.desiredPrefixes(store, client.replicaNamespace)
		if err != nil {
			return err
		}
		if len(prefixes) == 0 {
			return ErrDKVSPathNotSynced
		}
		changed, err = m.syncManagedPrefixes(client, store, prefixes, false)
		return err
	})
	if len(changed) != 0 {
		m.enqueueNotifications(changed)
	}
	return err
}

func (m *dkvsManager) forceCurrentPrefixes(client *SatsNetDKVSClient, prefixes []string) error {
	if m == nil || m.owner == nil || m.owner.db == nil || client == nil {
		return ErrDKVSPathNotSynced
	}
	var changed []string
	err := m.runTransport(func() error {
		var err error
		changed, err = m.syncManagedPrefixes(client, newDKVSReplicaStore(m.owner.db), prefixes, true)
		return err
	})
	if len(changed) != 0 {
		m.enqueueNotifications(changed)
	}
	return err
}

func (m *dkvsManager) refreshRequestedPrefixes(client *SatsNetDKVSClient, prefixes []string, force bool) error {
	if m == nil || m.owner == nil || m.owner.db == nil || client == nil {
		return ErrDKVSPathNotSynced
	}
	var changed []string
	err := m.runTransport(func() error {
		var err error
		changed, err = m.syncPrefixSet(client, newDKVSReplicaStore(m.owner.db), prefixes, force, false, nil, 0)
		return err
	})
	if len(changed) != 0 {
		m.enqueueNotifications(changed)
	}
	return err
}

func notificationTargets(changes []string) []string {
	set := make(map[string]struct{})
	for _, key := range changes {
		if path, err := dkvsindexer.CollectionPathForKey(key); err == nil && path != "" {
			set[path] = struct{}{}
		} else if key != "" {
			set[key] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for target := range set {
		result = append(result, target)
	}
	sort.Strings(result)
	return result
}

func (p *Manager) flushDKVSOutbox(client *SatsNetDKVSClient, store *dkvsReplicaStore) (bool, error) {
	return p.flushDKVSBatchOutbox(client, store)
}

func (p *Manager) syncDKVSOnce() (bool, error) {
	didWork, sendErr, receiveErr := p.syncDKVSOnceResult()
	err := errors.Join(receiveErr, sendErr)
	if p != nil && p.dkvs != nil && (p.dkvs.setLastSyncError(err) || didWork || err != nil) {
		p.dkvs.notifyStatusCallback()
	}
	return didWork, err
}

// Submission failures remain visible without deciding whether the confirmed
// replica can receive. Only receiving validates its own endpoint and baseline.
func (p *Manager) syncDKVSOnceResult() (bool, error, error) {
	return p.syncDKVSWork(true)
}

// Background job wakes do not imply a new remote version. Explicit sync keeps
// receiving; startup, registration changes and lost online grants also receive
// before the existing outbox/job work is allowed to resume.
func (p *Manager) syncDKVSWork(receive bool) (bool, error, error) {
	if p == nil || p.dkvs == nil || p.db == nil {
		return false, nil, ErrDKVSPathNotSynced
	}
	if err := p.accountDBFailure(); err != nil {
		return false, nil, err
	}
	var client *SatsNetDKVSClient
	var changed []string
	var submitted bool
	var sendErr error
	localClient, err := p.dkvs.primaryClient()
	if err != nil {
		return false, nil, err
	}
	localErr := p.stopAccountDB(p.finishLocalAccountACKs(localClient))
	if localErr != nil && !accountACKCleanupOnly(localErr) {
		return false, nil, localErr
	}
	err = p.dkvs.runTransport(func() error {
		var err error
		client, err = p.dkvs.primaryClient()
		if err != nil {
			return err
		}
		store := newDKVSReplicaStore(p.db)
		prefixes, err := p.dkvs.desiredPrefixes(store, client.replicaNamespace)
		if err != nil {
			return err
		}
		if len(prefixes) != 0 {
			state, stateErr := store.LoadSubscriptionState(client.replicaNamespace)
			if stateErr != nil && !errors.Is(stateErr, indexercommon.ErrKeyNotFound) {
				return stateErr
			}
			if receive || !p.dkvs.scopeReady(p.dkvs.sessionKey(client, "write")) ||
				!p.dkvs.scopeReady(p.dkvs.sessionKey(client, "read")) || !prefixStateReady(state, prefixes) {
				changed, err = p.dkvs.syncManagedPrefixes(client, store, prefixes, false)
				if err != nil {
					return err
				}
			}
		}
		submitted, sendErr = p.flushDKVSOutbox(client, store)
		if sendErr == nil {
			sendErr = localErr
		}
		if isDKVSOutboxGlobalTransportFailure(sendErr) {
			p.dkvs.invalidateOnlineSession()
		}
		return nil
	})
	if len(changed) != 0 {
		p.dkvs.enqueueNotifications(changed)
	}
	if err != nil {
		return submitted, sendErr, err
	}
	if err := p.stopAccountDB(sendErr); errors.Is(err, ErrAccountDBStopped) {
		return submitted, sendErr, err
	}
	p.dkvs.mu.Lock()
	hasJobs := len(p.dkvs.jobs) != 0
	p.dkvs.mu.Unlock()
	if hasJobs {
		if err := p.dkvs.runPendingJobs(&dkvsStore{manager: p.dkvs, client: client}); err != nil {
			// Domain tasks retain their own failure/retry state. Their result
			// must not stop the independently validated managed receiver.
			sendErr = errors.Join(sendErr, err)
		}
	}
	if err := p.accountDBFailure(); err != nil {
		return submitted || hasJobs, sendErr, err
	}
	mailbox, err := p.pollRootAccountMailboxIfDue(client)
	return submitted || hasJobs || mailbox || len(changed) != 0, errors.Join(sendErr, err), nil
}

func (p *Manager) SubscribeDKVSPrefix(prefix string) error {
	if p == nil || p.db == nil {
		return ErrDKVSPathNotSynced
	}
	manager := p.ensureDKVSManager()
	scope, err := dkvsindexer.NormalizeActiveScope(dkvsindexer.ActiveScope{Prefix: prefix})
	if err != nil {
		return err
	}
	err = manager.runTransport(func() error {
		client, err := manager.primaryClient()
		if err != nil {
			return err
		}
		if err := newDKVSReplicaStore(p.db).AddRegisteredPrefix(client.replicaNamespace, scope.Prefix); err != nil {
			return err
		}
		manager.mu.Lock()
		manager.paths[scope.Prefix] = struct{}{}
		manager.mu.Unlock()
		return nil
	})
	if err == nil {
		manager.wakeSync()
	}
	return err
}

func (p *Manager) UnsubscribeDKVSPrefix(prefix string) error {
	if p == nil || p.db == nil {
		return ErrDKVSPathNotSynced
	}
	manager := p.ensureDKVSManager()
	prefixes, err := normalizeWalletSubscriptionPrefixes([]string{prefix})
	if err != nil || len(prefixes) != 1 {
		return dkvsindexer.ErrInvalidKey
	}
	err = manager.runTransport(func() error {
		client, err := manager.primaryClient()
		if err != nil {
			return err
		}
		store := newDKVSReplicaStore(p.db)
		if err := store.RemoveRegisteredPrefix(client.replicaNamespace, prefixes[0]); err != nil {
			return err
		}
		remaining, err := store.LoadRegisteredPrefixes(client.replicaNamespace)
		if err != nil {
			return err
		}
		if err := store.PruneToPrefixes(client.replicaNamespace, remaining); err != nil {
			return err
		}
		manager.mu.Lock()
		delete(manager.paths, prefixes[0])
		manager.mu.Unlock()
		return nil
	})
	if err == nil {
		manager.wakeSync()
	}
	return err
}

func (p *Manager) ListSubscribedDKVSPrefixes() ([]string, error) {
	if p == nil || p.db == nil {
		return nil, ErrDKVSPathNotSynced
	}
	manager := p.ensureDKVSManager()
	client, err := manager.primaryClient()
	if err != nil {
		return nil, err
	}
	return manager.desiredPrefixes(newDKVSReplicaStore(p.db), client.replicaNamespace)
}

func (p *Manager) GetDKVSSubscriptionStatus() (*DKVSSubscriptionState, error) {
	if p == nil || p.db == nil {
		return nil, ErrDKVSPathNotSynced
	}
	manager := p.ensureDKVSManager()
	client, err := manager.primaryClient()
	if err != nil {
		return nil, err
	}
	state, err := newDKVSReplicaStore(p.db).LoadSubscriptionState(client.replicaNamespace)
	if errors.Is(err, indexercommon.ErrKeyNotFound) {
		return &DKVSSubscriptionState{Status: DKVSSubscriptionSyncing}, nil
	}
	if err == nil && !manager.scopeReady(manager.sessionKey(client, "write")) && state.Status == DKVSSubscriptionReady {
		state.Status = DKVSSubscriptionSyncing
	}
	return state, err
}

func (p *Manager) SetDKVSUpdateCallback(callback func()) {
	if manager := p.ensureDKVSManager(); manager != nil {
		manager.setCallback(callback)
	}
}

// SetDKVSStatusUpdateCallback notifies the UI after a background sync status change.
func (p *Manager) SetDKVSStatusUpdateCallback(callback func()) {
	if manager := p.ensureDKVSManager(); manager != nil {
		manager.setStatusCallback(callback)
	}
}

// The long poll never holds the transport coordinator: a foreground write
// must remain able to commit while the wallet waits for remote changes.
func (m *dkvsManager) watchManagedActive(stop, wake <-chan struct{}, retryAt time.Time) error {
	var client *SatsNetDKVSClient
	var request dkvsindexer.ActiveWatchRequest
	var prefixes []string
	var writeScope string
	var qualification *byte
	err := m.runTransport(func() error {
		var err error
		client, err = m.primaryClient()
		if err != nil {
			return err
		}
		config, err := m.subscriptionEndpointConfig(client)
		if err != nil {
			return err
		}
		store := newDKVSReplicaStore(m.owner.db)
		prefixes, err = m.desiredPrefixes(store, client.replicaNamespace)
		if err != nil {
			return err
		}
		if len(prefixes) == 0 {
			return ErrDKVSPathNotSynced
		}
		request.EndpointID = config.EndpointID
		for _, prefix := range prefixes {
			meta, err := store.LoadActiveMeta(client.replicaNamespace, dkvsindexer.ActiveScope{Prefix: prefix})
			if err != nil {
				return err
			}
			request.Scopes = append(request.Scopes, dkvsindexer.ActiveWatchScope{Scope: meta.Scope, Generation: meta.Generation, Root: meta.Root})
		}
		writeScope = m.sessionKey(client, "write")
		qualification = m.readyToken(writeScope)
		return nil
	})
	if err != nil {
		return err
	}
	var ctx context.Context
	var cancel context.CancelFunc
	if retryAt.IsZero() {
		ctx, cancel = context.WithCancel(m.requestContext())
	} else {
		ctx, cancel = context.WithDeadline(m.requestContext(), retryAt)
	}
	finished := make(chan struct{})
	go func() {
		select {
		case <-stop:
			cancel()
		case <-wake:
			cancel()
		case <-finished:
		}
	}()
	response, err := client.WatchActive(ctx, request)
	// Wake/stop and the existing retry deadline cancel our own long poll.
	// They do not provide evidence that the endpoint has gone offline.
	ownedCancel := errors.Is(err, context.Canceled) || (errors.Is(err, context.DeadlineExceeded) && !retryAt.IsZero())
	if err != nil && isDKVSNetworkFailure(err) && !ownedCancel {
		// The long poll belongs to the grant present when it started. A newer
		// completed sync (or another endpoint) must survive this late error.
		_ = m.runTransport(func() error {
			current, sourceErr := m.primaryClient()
			if sourceErr == nil && m.sessionKey(current, "write") == writeScope && m.readyToken(writeScope) == qualification {
				m.invalidateOnlineSession()
			}
			return nil
		})
	}
	close(finished)
	cancel()
	if err != nil {
		return err
	}
	if response.Page == nil {
		return nil
	}
	var changed []string
	err = m.runTransport(func() error {
		if err := m.owner.accountDBFailure(); err != nil {
			return err
		}
		current, err := m.primaryClient()
		if err != nil {
			return err
		}
		if current.Scheme != client.Scheme || current.Host != client.Host || current.Proxy != client.Proxy || current.replicaNamespace != client.replicaNamespace {
			return dkvsindexer.ErrEndpointMismatch
		}
		store := newDKVSReplicaStore(m.owner.db)
		now, err := m.desiredPrefixes(store, client.replicaNamespace)
		if err != nil {
			return err
		}
		if !sameStringList(now, prefixes) {
			return dkvsindexer.ErrResetRequired
		}
		var after uint64
		for _, item := range request.Scopes {
			if dkvsindexer.SameActiveScope(item.Scope, response.Page.Meta.Scope) {
				after = item.Generation
				break
			}
		}
		changed, err = m.syncPrefixSet(client, store, []string{response.Page.Meta.Scope.Prefix}, false, true, response.Page, after)
		return err
	})
	if len(changed) != 0 {
		m.enqueueNotifications(changed)
	}
	return err
}

func (m *dkvsManager) run(stop <-chan struct{}, done chan<- struct{}, wake <-chan struct{}) {
	defer close(done)
	for {
		select {
		case <-stop:
			return
		default:
		}
		didWork, sendErr, err := m.owner.syncDKVSWork(false)
		if err == nil {
			// A blocked outbox must not stop receiving. Rejected intents keep
			// their original authorization until explicitly reconciled.
			if m.setLastSyncError(sendErr) || didWork {
				m.notifyStatusCallback()
			}
			var retryAt time.Time
			var pendingRequest *DKVSRequestPendingError
			var localOutboxRead *dkvsLocalOutboxReadError
			var ackPersistence *dkvscore.AckPersistenceError
			// A verified ACK can be durable while its local outbox cleanup
			// fails once. An empty active watch will not wake the worker, so
			// retry reconciliation of the same request on a bounded timer.
			if sendErr != nil && (isDKVSNetworkFailure(sendErr) || errors.As(sendErr, &pendingRequest) ||
				errors.As(sendErr, &localOutboxRead) || errors.As(sendErr, &ackPersistence)) {
				retryAt = time.Now().Add(dkvsSyncRetryDelay)
			}
			if due := m.nextJobDue(); !due.IsZero() && (retryAt.IsZero() || due.Before(retryAt)) {
				retryAt = due
			}
			for {
				err = m.watchManagedActive(stop, wake, retryAt)
				if err != nil {
					if errors.Is(err, context.DeadlineExceeded) && !retryAt.IsZero() && !time.Now().Before(retryAt) {
						// The existing retry delay bounds this long-poll. Receiving
						// stays active until the next original-request attempt.
						err = dkvsindexer.ErrResetRequired
					}
					if errors.Is(err, ErrDKVSPathNotSynced) && sendErr != nil {
						err = sendErr
					}
					break
				}
				if !retryAt.IsZero() && !time.Now().Before(retryAt) {
					err = dkvsindexer.ErrResetRequired
					break
				}
				select {
				case <-stop:
					return
				default:
				}
			}
		}
		if errors.Is(err, context.Canceled) {
			if m.requestContext().Err() != nil {
				return
			}
			select {
			case <-stop:
				return
			default:
			}
			continue
		}
		if errors.Is(err, dkvsindexer.ErrResetRequired) || errors.Is(err, dkvsindexer.ErrConcurrentUpdate) {
			continue
		}
		if m.setLastSyncError(err) {
			m.notifyStatusCallback()
		}
		if isDKVSNonRetryableSyncError(err) {
			if !m.waitForWake(stop, wake) {
				return
			}
		} else if !m.wait(stop, wake, dkvsSyncRetryDelay) {
			return
		}
	}
}

func dkvsPollJitter() time.Duration {
	n := time.Now().UnixNano()
	if n < 0 {
		n = -n
	}
	return time.Duration(n % int64(5*time.Second))
}

func isDKVSNonRetryableSyncError(err error) bool {
	return errors.Is(err, ErrAccountDBStopped) || errors.Is(err, ErrAccountAutopayFundingRequired) || errors.Is(err, dkvsindexer.ErrRecordNotFound) ||
		isDKVSConflictError(err) || errors.Is(err, dkvsindexer.ErrInvalidSequence) ||
		errors.Is(err, dkvsindexer.ErrLocalOnlyEndpointMismatch) || errors.Is(err, dkvsindexer.ErrEndpointMismatch)
}
func (m *dkvsManager) wait(stop, wake <-chan struct{}, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-stop:
		return false
	case <-wake:
		return true
	case <-timer.C:
		return true
	}
}
func (m *dkvsManager) waitForWake(stop, wake <-chan struct{}) bool {
	select {
	case <-stop:
		return false
	case <-wake:
		return true
	}
}
func validateDKVSSubscriptionEndpoint(state *DKVSSubscriptionState, endpointID string) error {
	if state == nil || strings.TrimSpace(endpointID) == "" {
		return dkvsindexer.ErrStaleEndpoint
	}
	if state.EndpointID != "" && state.EndpointID != endpointID {
		return dkvsindexer.ErrEndpointMismatch
	}
	return nil
}
func subscriptionReadyForPrefixes(state *DKVSSubscriptionState, prefixes []string) bool {
	return prefixStateReady(state, prefixes)
}
