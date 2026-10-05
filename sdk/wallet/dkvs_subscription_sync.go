package wallet

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
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

func (m *dkvsManager) syncManagedPrefixes(client *SatsNetDKVSClient, store *dkvsReplicaStore, prefixes []string, force bool) ([]string, error) {
	if m == nil || client == nil || store == nil || len(prefixes) == 0 {
		return nil, ErrDKVSPathNotSynced
	}
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
	forcePaths := make(map[string]bool, len(prefixes))
	for _, prefix := range prefixes {
		forcePaths[prefix] = force
	}
	var changed []string
	for _, prefix := range all {
		scope := dkvsindexer.ActiveScope{Prefix: prefix}
		keys, err := client.SyncActiveScope(m.requestContext(), store, client.replicaNamespace, scope, forcePaths[prefix])
		if err != nil {
			if complete && isDKVSNetworkFailure(err) {
				err = errors.Join(err, store.MarkOfflineReady(client.replicaNamespace))
			}
			return changed, err
		}
		changed = append(changed, keys...)
		meta, err := store.LoadActiveMeta(client.replicaNamespace, scope)
		if err != nil {
			return changed, err
		}
		m.observeEndpointVerificationHeight(meta.ViewHeight)
	}
	if err := store.CompletePrefixSync(client.replicaNamespace, config.EndpointID, all); err != nil {
		return changed, err
	}
	return changed, nil
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
	submitted, err := p.flushDKVSBatchOutbox(client, store)
	if err != nil && p != nil && p.dkvs != nil {
		p.dkvs.setLastSyncError(err)
	}
	return submitted, err
}

func (p *Manager) syncDKVSOnce() (bool, error) {
	didWork, sendErr, receiveErr := p.syncDKVSOnceResult()
	return didWork, errors.Join(receiveErr, sendErr)
}

// Submission failures remain visible without deciding whether the confirmed
// replica can receive. Only receiving validates its own endpoint and baseline.
func (p *Manager) syncDKVSOnceResult() (bool, error, error) {
	if p == nil || p.dkvs == nil || p.db == nil {
		return false, nil, ErrDKVSPathNotSynced
	}
	var client *SatsNetDKVSClient
	var changed []string
	var submitted bool
	var sendErr error
	err := p.dkvs.runTransport(func() error {
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
		ready := len(prefixes) == 0
		if len(prefixes) != 0 {
			state, err := store.LoadSubscriptionState(client.replicaNamespace)
			if err != nil && !errors.Is(err, indexercommon.ErrKeyNotFound) {
				return err
			}
			ready = state != nil && prefixStateReady(state, prefixes)
		}
		if ready {
			submitted, sendErr = p.flushDKVSOutbox(client, store)
		}
		if len(prefixes) != 0 {
			changed, err = p.dkvs.syncManagedPrefixes(client, store, prefixes, false)
			if err != nil {
				return err
			}
		}
		if !ready {
			submitted, sendErr = p.flushDKVSOutbox(client, store)
		}
		return nil
	})
	if len(changed) != 0 {
		p.dkvs.enqueueNotifications(changed)
	}
	if err != nil {
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
	mailbox, err := p.pollRootAccountMailboxIfDue(client)
	return submitted || hasJobs || mailbox, errors.Join(sendErr, err), nil
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
	return state, err
}

func (p *Manager) SetDKVSUpdateCallback(callback func()) {
	if manager := p.ensureDKVSManager(); manager != nil {
		manager.setCallback(callback)
	}
}

// The long poll never holds the transport coordinator: a foreground write
// must remain able to commit while the wallet waits for remote changes.
func (m *dkvsManager) watchManagedActive(stop, wake <-chan struct{}, retryAt time.Time) error {
	var client *SatsNetDKVSClient
	var request dkvsindexer.ActiveWatchRequest
	var prefixes []string
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
		changed, err = client.syncActiveScope(m.requestContext(), store, client.replicaNamespace, response.Page.Meta.Scope, false, response.Page)
		if err == nil {
			// syncActiveScope may discard an old Watch page and install a fresh
			// response. Use the committed metadata as the verification height.
			meta, metaErr := store.LoadActiveMeta(client.replicaNamespace, response.Page.Meta.Scope)
			if metaErr != nil {
				return metaErr
			}
			m.observeEndpointVerificationHeight(meta.ViewHeight)
		}
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
		_, sendErr, err := m.owner.syncDKVSOnceResult()
		if err == nil {
			// A blocked outbox must not stop receiving. Rejected intents keep
			// their original authorization until explicitly reconciled.
			m.setLastSyncError(sendErr)
			var retryAt time.Time
			if sendErr != nil && isDKVSNetworkFailure(sendErr) {
				retryAt = time.Now().Add(dkvsSyncRetryDelay)
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
		m.setLastSyncError(err)
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
	return errors.Is(err, ErrAccountAutopayFundingRequired) || errors.Is(err, dkvsindexer.ErrRecordNotFound) ||
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
