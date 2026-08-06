package wallet

import (
	"encoding/hex"
	"sort"
	"strings"
	"sync"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type dkvsManagerRuntime struct {
	mu        sync.Mutex
	pathLocks map[string]*sync.Mutex
	affinity  map[string]string
}

var dkvsManagerRuntimes sync.Map

func runtimeForDKVSManager(manager *dkvsManager) *dkvsManagerRuntime {
	if manager == nil {
		return nil
	}
	if value, ok := dkvsManagerRuntimes.Load(manager); ok {
		return value.(*dkvsManagerRuntime)
	}
	runtime := &dkvsManagerRuntime{
		pathLocks: make(map[string]*sync.Mutex),
		affinity:  make(map[string]string),
	}
	actual, _ := dkvsManagerRuntimes.LoadOrStore(manager, runtime)
	return actual.(*dkvsManagerRuntime)
}

func dkvsLockTargetForKey(key string) (string, error) {
	path, err := dkvsindexer.CollectionPathForKey(key)
	if err == nil {
		return path, nil
	}
	if _, parseErr := dkvsindexer.ParseKey(key); parseErr != nil {
		return "", parseErr
	}
	return key, nil
}

func (m *dkvsManager) lockPathsForKeys(keys []string) (func(), error) {
	runtime := runtimeForDKVSManager(m)
	if runtime == nil {
		return nil, ErrDKVSPathNotSynced
	}
	unique := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		target, err := dkvsLockTargetForKey(key)
		if err != nil {
			return nil, err
		}
		unique[target] = struct{}{}
	}
	targets := make([]string, 0, len(unique))
	for target := range unique {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	locks := make([]*sync.Mutex, 0, len(targets))
	runtime.mu.Lock()
	for _, target := range targets {
		lock := runtime.pathLocks[target]
		if lock == nil {
			lock = &sync.Mutex{}
			runtime.pathLocks[target] = lock
		}
		locks = append(locks, lock)
	}
	runtime.mu.Unlock()
	for _, lock := range locks {
		lock.Lock()
	}
	return func() {
		for index := len(locks) - 1; index >= 0; index-- {
			locks[index].Unlock()
		}
	}, nil
}

func dkvsEndpointIdentity(client *SatsNetDKVSClient) string {
	if client == nil {
		return ""
	}
	return strings.TrimSpace(client.replicaNamespace)
}

func dkvsRecordOwnerSession(record *swire.DKVSRecord) string {
	if record == nil {
		return ""
	}
	parsed, err := dkvsindexer.ParseKey(record.Key)
	if err != nil {
		return ""
	}
	switch parsed.Namespace {
	case "personal", "blob":
		if len(parsed.Segments) > 0 {
			return "account:" + parsed.Segments[0]
		}
	case "mail":
		if len(parsed.Segments) >= 3 && parsed.Segments[1] == "msg" &&
			!dkvsindexer.IsTombstone(record.Flags) {
			return "account:" + parsed.Segments[2]
		}
		if len(parsed.Segments) > 0 {
			return "account:" + parsed.Segments[0]
		}
	case "account":
		if len(parsed.Segments) == 2 {
			return "account-record:" + parsed.Segments[0] + ":" + parsed.Segments[1]
		}
	}
	if len(record.PubKey) != 0 {
		return "authority:" + hex.EncodeToString(record.PubKey)
	}
	return ""
}

func dkvsWalletRecordIsFreeLocal(record *swire.DKVSRecord) bool {
	if record == nil || len(record.FeeProof) == 0 {
		return false
	}
	proof, err := dkvsindexer.ParseFeeProof(record.FeeProof)
	return err == nil && proof.Mode == dkvsindexer.FeeModeFreeLocal
}

func (m *dkvsManager) ensureEndpointAffinity(client *SatsNetDKVSClient,
	records []*swire.DKVSRecord) error {

	runtime := runtimeForDKVSManager(m)
	if runtime == nil || client == nil || m.owner == nil || m.owner.db == nil {
		return ErrDKVSPathNotSynced
	}
	endpoint := dkvsEndpointIdentity(client)
	if endpoint == "" {
		return dkvsindexer.ErrStaleEndpoint
	}
	owners := make(map[string][]*swire.DKVSRecord)
	for _, record := range records {
		owner := dkvsRecordOwnerSession(record)
		if owner == "" {
			return dkvsindexer.ErrPermissionDenied
		}
		owners[owner] = append(owners[owner], record)
	}
	store := newDKVSReplicaStore(m.owner.db)
	for owner, ownerRecords := range owners {
		runtime.mu.Lock()
		pinned := runtime.affinity[owner]
		runtime.mu.Unlock()
		if pinned == "" || pinned == endpoint {
			runtime.mu.Lock()
			runtime.affinity[owner] = endpoint
			runtime.mu.Unlock()
			continue
		}
		pending, err := store.hasPendingBatchOutbox(pinned)
		if err != nil {
			return err
		}
		if pending {
			return dkvsindexer.ErrStaleEndpoint
		}
		for _, record := range ownerRecords {
			path, err := dkvsindexer.CollectionPathForKey(record.Key)
			if err != nil {
				return err
			}
			newScope := pathReplicaScope(client, path)
			if !m.scopeReady(newScope) {
				return dkvsindexer.ErrStaleEndpoint
			}
			if dkvsWalletRecordIsFreeLocal(record) {
				return dkvsindexer.ErrLocalOnlyEndpointMismatch
			}
		}
		runtime.mu.Lock()
		runtime.affinity[owner] = endpoint
		runtime.mu.Unlock()
	}
	return nil
}

func releaseDKVSManagerRuntime(manager *dkvsManager) {
	if manager != nil {
		dkvsManagerRuntimes.Delete(manager)
	}
}
