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
	Prefix       string
	Root         string
	Generation   uint64
	ViewHeight   uint64
	ServerTimeMS uint64
	Scope        string
	Filters      []dkvsindexer.Subscription
}

type dkvsPathWriteContext struct {
	Conditions   []dkvsindexer.PathWritePrecondition
	ServerTimeMS map[string]uint64
	PathMeta     map[string]*dkvsindexer.PathMeta
}

var ErrDKVSPathNotSynced = errors.New("DKVS path has not completed initial synchronization")

func (p *Manager) dkvsReplicaNamespace() string {
	if p == nil || p.cfg == nil || p.cfg.IndexerL2 == nil {
		return ""
	}
	indexerConfig := p.cfg.IndexerL2
	return p.dkvsReplicaNamespaceFor(indexerConfig.Scheme, indexerConfig.Host, indexerConfig.Proxy)
}

func (p *Manager) dkvsReplicaNamespaceFor(scheme, host, proxy string) string {
	if p == nil || p.cfg == nil {
		return ""
	}
	return strings.Join([]string{p.cfg.Env, p.cfg.Chain, scheme, host, proxy}, ":")
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
	// Replay v1 atomic batches first. Their successful response already updates
	// the confirmed replica and removes the exact batch outbox atomically, so
	// they deliberately do not request a write-after-full-refresh.
	if _, err := p.flushDKVSBatchOutbox(client, store); err != nil {
		return false, err
	}

	// Legacy per-record entries are retained only for migration of pre-v1 local
	// state. New v1 writes replace these entries with one exact batch outbox.
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
		if record != nil {
			confirmedByKey[record.Key] = record
		}
	}
	submitted := false
	for _, record := range pending {
		if record == nil {
			return submitted, dkvsindexer.ErrInvalidRecord
		}
		active := confirmedByKey[record.Key]
		if active != nil {
			if dkvsindexer.RecordHash(active) == dkvsindexer.RecordHash(record) {
				if err := store.acknowledgeOutbox(scope, record); err != nil {
					return submitted, err
				}
				continue
			}
			return submitted, dkvsindexer.ErrWriteConflict
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

func pathReplicaScope(client *SatsNetDKVSClient, path string) string {
	return dkvsReplicaScope(client.replicaNamespace, []dkvsindexer.Subscription{{
		Type: dkvsindexer.SubscriptionPrefix,
		Target: path,
	}})
}

func (p *Manager) syncDKVSDirectory(client *SatsNetDKVSClient, store *dkvsReplicaStore,
	path string) (dkvsDirectoryState, error) {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	if _, err := dkvsindexer.ParsePrefix(path); err != nil {
		return dkvsDirectoryState{}, err
	}
	filters := []dkvsindexer.Subscription{{Type: dkvsindexer.SubscriptionPrefix, Target: path}}
	scope := dkvsReplicaScope(client.replicaNamespace, filters)
	remote, err := client.GetPathMetaV1(path)
	if err != nil {
		return dkvsDirectoryState{}, err
	}
	baseline, baselineErr := store.loadBaseline(scope)
	if baselineErr == nil {
		if remote.PathMeta.Generation < baseline.Generation {
			return dkvsDirectoryState{}, dkvsindexer.ErrStaleEndpoint
		}
		if remote.PathMeta.Generation == baseline.Generation &&
			remote.PathMeta.StateRoot == baseline.ActiveRoot {
			state := dkvsDirectoryState{
				Prefix: path, Root: remote.PathMeta.StateRoot.String(),
				Generation: remote.PathMeta.Generation, ViewHeight: remote.PathMeta.ViewHeight,
				ServerTimeMS: remote.ServerTimeMS, Scope: scope, Filters: filters,
			}
			p.ensureDKVSManager().markReady(scope)
			return state, nil
		}
	} else if !errors.Is(baselineErr, indexer.ErrKeyNotFound) &&
		!errors.Is(baselineErr, dkvsindexer.ErrInvalidRecord) {
		return dkvsDirectoryState{}, baselineErr
	}

	snapshot, err := client.SyncPath(path, dkvsindexer.RecordVerificationOptions{
		Height: remote.PathMeta.ViewHeight,
		Now:    remote.ServerTimeMS,
	})
	if err != nil {
		return dkvsDirectoryState{}, err
	}
	if baselineErr == nil && snapshot.PathMeta.Generation < baseline.Generation {
		return dkvsDirectoryState{}, dkvsindexer.ErrStaleEndpoint
	}
	if err := store.applyPathSnapshot(scope, snapshot); err != nil {
		return dkvsDirectoryState{}, err
	}
	state := dkvsDirectoryState{
		Prefix: path, Root: snapshot.PathMeta.StateRoot.String(),
		Generation: snapshot.PathMeta.Generation, ViewHeight: snapshot.PathMeta.ViewHeight,
		ServerTimeMS: snapshot.ServerTimeMS, Scope: scope, Filters: filters,
	}
	p.ensureDKVSManager().markReady(scope)
	return state, nil
}

// syncedPathWriteContext reads only the confirmed local replica. The caller
// holds the path lock, so the signed generation and CAS precondition are based
// on one stable local snapshot without an additional live GET/TOCTOU window.
func (p *Manager) syncedPathWriteContext(client *SatsNetDKVSClient,
	keys []string) (*dkvsPathWriteContext, error) {
	if p == nil || p.db == nil || client == nil {
		return nil, ErrDKVSPathNotSynced
	}
	paths := make(map[string]struct{})
	for _, key := range keys {
		path, err := dkvsindexer.CollectionPathForKey(key)
		if err != nil {
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
	writeContext := &dkvsPathWriteContext{
		Conditions:   make([]dkvsindexer.PathWritePrecondition, 0, len(ordered)),
		ServerTimeMS: make(map[string]uint64, len(ordered)),
		PathMeta:     make(map[string]*dkvsindexer.PathMeta, len(ordered)),
	}
	for _, path := range ordered {
		scope := pathReplicaScope(client, path)
		state, err := store.loadPathState(scope)
		if err != nil {
			if errors.Is(err, indexer.ErrKeyNotFound) ||
				errors.Is(err, dkvsindexer.ErrInvalidRecord) {
				return nil, ErrDKVSPathNotSynced
			}
			return nil, err
		}
		if state.Path != path || state.PathMeta == nil || state.PathMeta.Path != path {
			return nil, ErrDKVSPathNotSynced
		}
		meta := cloneWalletPathMeta(state.PathMeta)
		writeContext.Conditions = append(writeContext.Conditions, dkvsindexer.PathWritePrecondition{
			Path: path, ExpectedRoot: meta.StateRoot,
			ExpectedGeneration: meta.Generation,
		})
		writeContext.ServerTimeMS[path] = state.ServerTimeMS
		writeContext.PathMeta[path] = meta
	}
	return writeContext, nil
}

func (p *Manager) syncedPathWritePreconditions(client *SatsNetDKVSClient,
	keys []string) ([]dkvsindexer.PathWritePrecondition, error) {
	context, err := p.syncedPathWriteContext(client, keys)
	if err != nil {
		return nil, err
	}
	return context.Conditions, nil
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
	states := make([]dkvsDirectoryState, 0, len(directories)+len(exactKeys))
	for _, path := range directories {
		state, err := p.syncDKVSDirectory(client, store, path)
		if err != nil {
			return states, err
		}
		submitted, err := p.flushDKVSOutbox(client, store, state.Scope)
		if err != nil {
			return states, err
		}
		if submitted {
			// Only legacy per-record migration writes request this refresh. Native
			// v1 batch writes already applied their write echo atomically.
			state, err = p.syncDKVSDirectory(client, store, path)
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
