package wallet

import (
	"sort"
	"strings"
	"sync"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type dkvsManagerRuntime struct {
	mu       sync.Mutex
	keyLocks map[string]*sync.Mutex
}

var dkvsManagerRuntimes sync.Map

func runtimeForDKVSManager(manager *dkvsManager) *dkvsManagerRuntime {
	if manager == nil {
		return nil
	}
	if value, ok := dkvsManagerRuntimes.Load(manager); ok {
		return value.(*dkvsManagerRuntime)
	}
	runtime := &dkvsManagerRuntime{keyLocks: make(map[string]*sync.Mutex)}
	actual, _ := dkvsManagerRuntimes.LoadOrStore(manager, runtime)
	return actual.(*dkvsManagerRuntime)
}

// lockPathsForKeys keeps its historical name because domain callers already
// use it, but the final DKVS concurrency boundary is strictly per key.
func (m *dkvsManager) lockPathsForKeys(keys []string) (func(), error) {
	runtime := runtimeForDKVSManager(m)
	if runtime == nil {
		return nil, ErrDKVSPathNotSynced
	}
	unique := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if _, err := dkvsindexer.ParseKey(key); err != nil {
			return nil, err
		}
		unique[key] = struct{}{}
	}
	targets := make([]string, 0, len(unique))
	for key := range unique {
		targets = append(targets, key)
	}
	sort.Strings(targets)
	locks := make([]*sync.Mutex, 0, len(targets))
	runtime.mu.Lock()
	for _, key := range targets {
		lock := runtime.keyLocks[key]
		if lock == nil {
			lock = &sync.Mutex{}
			runtime.keyLocks[key] = lock
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

func dkvsWalletRecordIsFreeLocal(record *swire.DKVSRecord) bool {
	if record == nil || len(record.FeeProof) == 0 {
		return false
	}
	proof, err := dkvsindexer.ParseFeeProof(record.FeeProof)
	return err == nil && proof.Mode == dkvsindexer.FeeModeFreeLocal
}

func releaseDKVSManagerRuntime(manager *dkvsManager) {
	if manager == nil {
		return
	}
	dkvsManagerRuntimes.Delete(manager)
}
