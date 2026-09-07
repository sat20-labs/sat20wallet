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
	activeLocal, err := store.HasActiveFreeLocal(namespace)
	if err != nil {
		return err
	}
	pendingLocal, err := store.PendingFreeLocalOutbox(namespace)
	if err != nil {
		return err
	}
	if activeLocal || pendingLocal {
		return dkvsindexer.ErrLocalOnlyEndpointMismatch
	}
	return nil
}

func prefixStateReady(state *DKVSSubscriptionState, prefixes []string) bool {
	if state == nil || state.Status != DKVSSubscriptionReady ||
		strings.TrimSpace(state.EndpointID) == "" || !sameStringList(state.Prefixes, prefixes) {
		return false
	}
	for _, prefix := range prefixes {
		if _, ok := state.Generations[prefix]; !ok {
			return false
		}
	}
	return true
}

func (m *dkvsManager) installPrefixSnapshot(client *SatsNetDKVSClient,
	store *dkvsReplicaStore, prefix, endpointID string) ([]string, error) {
	snapshot, err := client.GetPrefixSnapshot(prefix)
	if err != nil {
		return nil, err
	}
	if snapshot.EndpointID != endpointID || snapshot.Prefix != prefix {
		return nil, dkvsindexer.ErrEndpointMismatch
	}
	changed, err := store.ReplacePrefixSnapshot(client.replicaNamespace, snapshot)
	if err != nil {
		return nil, err
	}
	m.setEndpointVerificationHeight(snapshot.ViewHeight, true)
	return changed, nil
}

// syncManagedPrefixes uses the endpoint-local prefix generation returned by
// the server as its only remote freshness marker. It keeps no cursor and
// creates no server-side waiter/session.
func (m *dkvsManager) syncManagedPrefixes(client *SatsNetDKVSClient,
	store *dkvsReplicaStore, prefixes []string, force bool) ([]string, error) {
	if m == nil || client == nil || store == nil || len(prefixes) == 0 {
		return nil, ErrDKVSPathNotSynced
	}
	config, err := m.subscriptionEndpointConfig(client)
	if err != nil {
		return nil, err
	}
	state, stateErr := store.LoadSubscriptionState(client.replicaNamespace)
	if stateErr != nil && !errors.Is(stateErr, indexercommon.ErrKeyNotFound) {
		return nil, stateErr
	}
	if errors.Is(stateErr, indexercommon.ErrKeyNotFound) {
		state = &DKVSSubscriptionState{}
	}
	if stateErr == nil {
		if err := m.validateSubscriptionEndpointSwitch(store, client.replicaNamespace, state, config); err != nil {
			return nil, err
		}
	}
	resetEndpoint := stateErr != nil || state.EndpointID != config.EndpointID
	needSnapshot := make([]string, 0, len(prefixes))
	if force || resetEndpoint || !sameStringList(state.Prefixes, prefixes) {
		needSnapshot = append(needSnapshot, prefixes...)
	} else {
		known := make([]dkvsindexer.PrefixGeneration, 0, len(prefixes))
		for _, prefix := range prefixes {
			generation, ok := state.Generations[prefix]
			if !ok {
				needSnapshot = append(needSnapshot, prefix)
				continue
			}
			known = append(known, dkvsindexer.PrefixGeneration{Prefix: prefix, Generation: generation})
		}
		if len(known) != 0 {
			status, statusErr := client.GetPrefixStatus(config.EndpointID, known)
			if statusErr != nil {
				return nil, statusErr
			}
			m.setEndpointVerificationHeight(status.ViewHeight, true)
			for _, changed := range status.Changed {
				needSnapshot = append(needSnapshot, changed.Prefix)
			}
		}
	}
	needSnapshot, err = normalizeWalletSubscriptionPrefixes(needSnapshot)
	if err != nil {
		return nil, err
	}
	if len(needSnapshot) != 0 || state.Status != DKVSSubscriptionReady || resetEndpoint {
		if err := store.PreparePrefixSync(client.replicaNamespace, config.EndpointID,
			prefixes, resetEndpoint); err != nil {
			return nil, err
		}
	}
	changedKeys := make([]string, 0)
	for _, prefix := range needSnapshot {
		changed, snapshotErr := m.installPrefixSnapshot(client, store, prefix, config.EndpointID)
		if snapshotErr != nil {
			return changedKeys, snapshotErr
		}
		changedKeys = append(changedKeys, changed...)
	}
	if err := store.CompletePrefixSync(client.replicaNamespace, config.EndpointID, prefixes); err != nil {
		return changedKeys, err
	}
	return changedKeys, nil
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
	if p == nil || p.dkvs == nil || p.db == nil {
		return false, ErrDKVSPathNotSynced
	}
	var (
		client    *SatsNetDKVSClient
		changed   []string
		submitted bool
	)
	transportErr := p.dkvs.runTransport(func() error {
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
		readyBaseline := len(prefixes) == 0
		if len(prefixes) != 0 {
			state, stateErr := store.LoadSubscriptionState(client.replicaNamespace)
			if stateErr != nil && !errors.Is(stateErr, indexercommon.ErrKeyNotFound) {
				return stateErr
			}
			readyBaseline = stateErr == nil && prefixStateReady(state, prefixes)
		}
		if readyBaseline {
			submitted, err = p.flushDKVSOutbox(client, store)
			if err != nil {
				return err
			}
		}
		if len(prefixes) != 0 {
			changed, err = p.dkvs.syncManagedPrefixes(client, store, prefixes, false)
			if err != nil {
				changed = nil
				return err
			}
		}
		if !readyBaseline {
			submitted, err = p.flushDKVSOutbox(client, store)
		}
		return err
	})
	if len(changed) != 0 {
		p.dkvs.enqueueNotifications(changed)
	}
	if transportErr != nil {
		return false, transportErr
	}
	// Domain jobs and callbacks always run after the transport operation has
	// released its coordinator slot.
	p.dkvs.mu.Lock()
	hasJobs := len(p.dkvs.jobs) != 0
	p.dkvs.mu.Unlock()
	if hasJobs {
		managedStore := &dkvsStore{manager: p.dkvs, client: client}
		if err := p.dkvs.runPendingJobs(managedStore); err != nil {
			return submitted, err
		}
	}
	mailboxPolled, err := p.pollRootAccountMailboxIfDue(client)
	if err != nil {
		return submitted || hasJobs, err
	}
	return submitted || hasJobs || mailboxPolled, nil
}

func (p *Manager) SubscribeDKVSPrefix(prefix string) error {
	if p == nil || p.db == nil {
		return ErrDKVSPathNotSynced
	}
	manager := p.ensureDKVSManager()
	client, err := manager.primaryClient()
	if err != nil {
		return err
	}
	prefixes, err := normalizeWalletSubscriptionPrefixes([]string{prefix})
	if err != nil || len(prefixes) != 1 {
		return dkvsindexer.ErrInvalidKey
	}
	store := newDKVSReplicaStore(p.db)
	if err := store.AddRegisteredPrefix(client.replicaNamespace, prefixes[0]); err != nil {
		return err
	}
	manager.mu.Lock()
	manager.paths[prefixes[0]] = struct{}{}
	manager.mu.Unlock()
	manager.wakeSync()
	return nil
}

func (p *Manager) UnsubscribeDKVSPrefix(prefix string) error {
	if p == nil || p.db == nil {
		return ErrDKVSPathNotSynced
	}
	manager := p.ensureDKVSManager()
	client, err := manager.primaryClient()
	if err != nil {
		return err
	}
	prefixes, err := normalizeWalletSubscriptionPrefixes([]string{prefix})
	if err != nil || len(prefixes) != 1 {
		return dkvsindexer.ErrInvalidKey
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
	manager.wakeSync()
	return nil
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

func (m *dkvsManager) run(stop <-chan struct{}, done chan<- struct{}, wake <-chan struct{}) {
	defer close(done)
	for {
		select {
		case <-stop:
			return
		default:
		}
		_, err := m.owner.syncDKVSOnce()
		if err != nil {
			m.setLastSyncError(err)
			if errors.Is(err, context.Canceled) && m.requestContext().Err() != nil {
				return
			}
			Log.Warningf("DKVS background sync failed: %v", err)
			if isDKVSNonRetryableSyncError(err) {
				if !m.waitForWake(stop, wake) {
					return
				}
				continue
			}
			if !m.wait(stop, wake, dkvsSyncRetryDelay) {
				return
			}
			continue
		}
		m.setLastSyncError(nil)
		if !m.wait(stop, wake, dkvsIdleSyncInterval+dkvsPollJitter()) {
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
	return errors.Is(err, ErrAccountAutopayFundingRequired) ||
		errors.Is(err, dkvsindexer.ErrRecordNotFound) ||
		isDKVSConflictError(err) || errors.Is(err, dkvsindexer.ErrInvalidSequence) ||
		errors.Is(err, dkvsindexer.ErrLocalOnlyEndpointMismatch)
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
