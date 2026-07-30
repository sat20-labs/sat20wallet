package wallet

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const (
	dkvsWatchTimeoutSeconds = 20
	dkvsSyncRetryDelay      = 5 * time.Second
)

type dkvsDirectoryState struct {
	Prefix     string
	Root       string
	Generation uint64
	Scope      string
	Filters    []dkvsindexer.Subscription
}

var ErrDKVSPathNotSynced = errors.New("DKVS path has not completed initial synchronization")

func (p *Manager) dkvsReplicaNamespace() string {
	if p == nil || p.cfg == nil || p.cfg.IndexerL2 == nil {
		return ""
	}
	indexer := p.cfg.IndexerL2
	return p.dkvsReplicaNamespaceFor(indexer.Scheme, indexer.Host, indexer.Proxy)
}

func (p *Manager) dkvsReplicaNamespaceFor(scheme, host, proxy string) string {
	if p == nil || p.cfg == nil {
		return ""
	}
	return strings.Join([]string{
		p.cfg.Env, p.cfg.Chain, scheme, host, proxy,
	}, ":")
}

func (p *Manager) dkvsManagedDirectories() ([]string, error) {
	if p == nil || p.dkvs == nil {
		return nil, nil
	}
	return p.dkvs.managedPaths(), nil
}

func (p *Manager) dkvsManagedExactKeys() ([]string, error) {
	if p == nil || p.dkvs == nil {
		return nil, nil
	}
	return p.dkvs.managedExactKeys(), nil
}

func (p *Manager) flushDKVSOutbox(client *SatsNetDKVSClient, store *dkvsReplicaStore,
	scope string) (bool, error) {

	pending, err := store.loadOutbox(scope)
	if err != nil {
		return false, err
	}
	confirmed, err := store.loadConfirmed(scope)
	if err != nil {
		return false, err
	}
	confirmedByKey := make(map[string]*swire.DKVSRecord, len(confirmed))
	for _, record := range confirmed {
		confirmedByKey[record.Key] = record
	}
	submitted := false
	for _, record := range pending {
		active := confirmedByKey[record.Key]
		if active != nil && dkvsindexer.CompareRecords(active, record) >= 0 {
			if err := store.acknowledgeOutbox(scope, record); err != nil {
				return submitted, err
			}
			continue
		}
		if dkvsindexer.IsTombstone(record.Flags) {
			_, err = client.Tombstone(record)
		} else {
			_, err = client.PutRecord(record)
		}
		if err != nil {
			return submitted, err
		}
		submitted = true
	}
	return submitted, nil
}

func (p *Manager) syncDKVSDirectory(client *SatsNetDKVSClient, store *dkvsReplicaStore,
	prefix string) (dkvsDirectoryState, error) {

	filters := []dkvsindexer.Subscription{{Type: dkvsindexer.SubscriptionPrefix, Target: prefix}}
	scope := dkvsReplicaScope(client.replicaNamespace, filters)
	for attempt := 0; attempt < 3; attempt++ {
		before, err := client.GetPathMeta(prefix)
		if err != nil {
			return dkvsDirectoryState{}, err
		}
		records, root, err := client.SyncDirectoryAll(prefix,
			dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())})
		if err != nil {
			return dkvsDirectoryState{}, err
		}
		after, err := client.GetPathMeta(prefix)
		if err != nil {
			return dkvsDirectoryState{}, err
		}
		if before.Generation != after.Generation || before.ActiveRoot != after.ActiveRoot {
			continue
		}
		if err := store.applyConfirmed(scope, filters, records, root,
			after.ActiveRoot, after.Generation); err != nil {
			return dkvsDirectoryState{}, err
		}
		state := dkvsDirectoryState{
			Prefix: prefix, Root: root, Generation: after.Generation, Scope: scope, Filters: filters,
		}
		p.ensureDKVSManager().markReady(state.Scope)
		return state, nil
	}
	return dkvsDirectoryState{}, dkvsindexer.ErrConcurrentUpdate
}

func (p *Manager) syncedPathWritePreconditions(client *SatsNetDKVSClient,
	keys []string) ([]dkvsindexer.PathWritePrecondition, error) {

	if p == nil || p.db == nil || client == nil {
		return nil, ErrDKVSPathNotSynced
	}
	paths := make(map[string]struct{})
	for _, key := range keys {
		path, err := dkvsindexer.CollectionPathForKey(key)
		if err != nil {
			if errors.Is(err, dkvsindexer.ErrInvalidKey) {
				continue
			}
			return nil, err
		}
		paths[path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	store := newDKVSReplicaStore(p.db)
	conditions := make([]dkvsindexer.PathWritePrecondition, 0, len(ordered))
	for _, path := range ordered {
		filters := []dkvsindexer.Subscription{{Type: dkvsindexer.SubscriptionPrefix, Target: path}}
		scope := dkvsReplicaScope(client.replicaNamespace, filters)
		baseline, err := store.loadBaseline(scope)
		if err != nil {
			if errors.Is(err, indexer.ErrKeyNotFound) || errors.Is(err, dkvsindexer.ErrInvalidRecord) {
				return nil, ErrDKVSPathNotSynced
			}
			return nil, err
		}
		meta, err := client.GetPathMeta(path)
		if err != nil {
			return nil, err
		}
		if baseline.Generation != meta.Generation || baseline.ActiveRoot != meta.ActiveRoot {
			return nil, dkvsindexer.ErrWriteConflict
		}
		conditions = append(conditions, dkvsindexer.PathWritePrecondition{
			Path: path, ExpectedRoot: baseline.ActiveRoot, ExpectedGeneration: baseline.Generation,
		})
	}
	return conditions, nil
}

func (p *Manager) refreshDKVSPathsAfterWrite(client *SatsNetDKVSClient, keys []string) error {
	if p == nil || p.db == nil || client == nil {
		return ErrDKVSPathNotSynced
	}
	paths := make(map[string]struct{})
	exact := make(map[string]struct{})
	for _, key := range keys {
		path, err := dkvsindexer.CollectionPathForKey(key)
		if err != nil {
			if errors.Is(err, dkvsindexer.ErrInvalidKey) {
				if _, parseErr := dkvsindexer.ParseKey(key); parseErr != nil {
					return parseErr
				}
				exact[key] = struct{}{}
				continue
			}
			return err
		}
		paths[path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	store := newDKVSReplicaStore(p.db)
	for _, path := range ordered {
		if _, err := p.syncDKVSDirectory(client, store, path); err != nil {
			return err
		}
	}
	orderedKeys := make([]string, 0, len(exact))
	for key := range exact {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Strings(orderedKeys)
	for _, key := range orderedKeys {
		if err := p.ensureDKVSManager().syncExactKey(client, store, key); err != nil {
			return err
		}
	}
	return nil
}

func (p *Manager) syncRGB11WalletPaths(client *SatsNetDKVSClient, walletID string) error {
	if p == nil || p.wallet == nil || p.wallet.GetPubKey() == nil || walletID == "" {
		return ErrDKVSPathNotSynced
	}
	pubkey := p.wallet.GetPubKey().SerializeCompressed()
	headKey, err := dkvsindexer.PersonalKey(pubkey, RGB11WalletHeadPath(walletID))
	if err != nil {
		return err
	}
	snapshotKey, err := dkvsindexer.BlobKey(
		dkvsindexer.AccountID(pubkey), RGB11WalletSnapshotBlobKey(walletID))
	if err != nil {
		return err
	}
	paths := make(map[string]struct{})
	for _, key := range []string{headKey, snapshotKey} {
		path, err := dkvsindexer.CollectionPathForKey(key)
		if err != nil {
			return err
		}
		paths[path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	store := newDKVSReplicaStore(p.db)
	for _, path := range ordered {
		if _, err := p.syncDKVSDirectory(client, store, path); err != nil {
			return err
		}
	}
	return nil
}

func (p *Manager) syncDKVSOnce() ([]dkvsDirectoryState, error) {
	if p == nil || p.dkvs == nil {
		return nil, fmt.Errorf("wallet manager is unavailable")
	}
	p.dkvs.runMu.Lock()
	defer p.dkvs.runMu.Unlock()
	directories, err := p.dkvsManagedDirectories()
	if err != nil {
		return nil, err
	}
	exactKeys, err := p.dkvsManagedExactKeys()
	if err != nil {
		return nil, err
	}
	if len(directories) == 0 && len(exactKeys) == 0 {
		return nil, nil
	}
	client, err := p.dkvs.primaryClient()
	if err != nil {
		return nil, err
	}
	store := newDKVSReplicaStore(p.db)
	states := make([]dkvsDirectoryState, 0, len(directories))
	for _, prefix := range directories {
		state, err := p.syncDKVSDirectory(client, store, prefix)
		if err != nil {
			return states, err
		}
		submitted, err := p.flushDKVSOutbox(client, store, state.Scope)
		if err != nil {
			return states, err
		}
		if submitted {
			state, err = p.syncDKVSDirectory(client, store, prefix)
			if err != nil {
				return states, err
			}
		}
		states = append(states, state)
	}
	for _, key := range exactKeys {
		state, err := p.dkvs.syncExactKeyState(client, store, key)
		if err != nil {
			return states, err
		}
		states = append(states, state)
	}
	managedStore := &dkvsStore{manager: p.dkvs, client: client}
	if err := p.dkvs.runPendingJobs(managedStore); err != nil {
		return states, err
	}
	paths := make([]string, 0, len(states))
	for _, state := range states {
		paths = append(paths, state.Prefix)
	}
	p.dkvs.notifyObservers(paths)
	p.dkvs.notifyCallback()
	return states, nil
}

func (p *Manager) SetDKVSUpdateCallback(callback func()) {
	if manager := p.ensureDKVSManager(); manager != nil {
		manager.setCallback(callback)
	}
}
