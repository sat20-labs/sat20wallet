package dkvs

import (
	"errors"
	"sort"
	"strings"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func (s *ReplicaStore) LoadRegisteredPrefixes(namespace string) ([]string, error) {
	if s == nil || s.db == nil || strings.TrimSpace(namespace) == "" {
		return nil, ErrReplicaNotReady
	}
	base := dkvsNamespacedPrefix(dkvsSubscriptionPrefixPrefix, namespace)
	baseText := string(base)
	prefixes := make([]string, 0)
	err := s.db.BatchRead(base, false, func(key, _ []byte) error {
		text := string(key)
		if !strings.HasPrefix(text, baseText) {
			return dkvsindexer.ErrInvalidRecord
		}
		prefix := strings.TrimPrefix(text, baseText)
		if prefix == "" {
			return dkvsindexer.ErrInvalidRecord
		}
		prefixes = append(prefixes, prefix)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return NormalizeSubscriptionPrefixes(prefixes)
}

func SameStringList(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (s *ReplicaStore) PersistRegisteredPrefixes(namespace string, prefixes []string) error {
	prefixes, err := NormalizeSubscriptionPrefixes(prefixes)
	if err != nil {
		return err
	}
	batch := s.db.NewWriteBatch()
	defer batch.Close()
	if err := s.replacePrefixRegistryBatch(batch, namespace, prefixes); err != nil {
		return err
	}
	state, stateErr := s.LoadSubscriptionState(namespace)
	if stateErr != nil {
		if !errors.Is(stateErr, indexercommon.ErrKeyNotFound) {
			return stateErr
		}
		state = &SubscriptionState{Status: DKVSSubscriptionSyncing}
	}
	if !SameStringList(state.Prefixes, prefixes) {
		previous := state.Generations
		state.Prefixes = prefixes
		state.Generations = make(map[string]uint64, len(prefixes))
		for _, prefix := range prefixes {
			if generation, ok := previous[prefix]; ok {
				state.Generations[prefix] = generation
			}
		}
		// Preserve the old EndpointID until the replacement snapshot succeeds.
		// If configuration switched endpoints, this lets the sync worker reject
		// the switch while active FREE_LOCAL state or outbox entries still exist.
		state.Status = DKVSSubscriptionSyncing
		state.LastErrorCode = ""
	}
	state.LastSyncAtMS = uint64(time.Now().UnixMilli())
	if err := putSubscriptionStateBatch(batch, namespace, state); err != nil {
		return err
	}
	return batch.Flush()
}

func (s *ReplicaStore) PreparePrefixSync(namespace, endpointID string,
	prefixes []string, resetGenerations bool) error {
	prefixes, err := NormalizeSubscriptionPrefixes(prefixes)
	if err != nil || strings.TrimSpace(endpointID) == "" {
		return dkvsindexer.ErrInvalidSnapshot
	}
	state, err := s.LoadSubscriptionState(namespace)
	if err != nil {
		if !errors.Is(err, indexercommon.ErrKeyNotFound) {
			return err
		}
		state = &SubscriptionState{}
	}
	previous := state.Generations
	state.EndpointID = endpointID
	state.Prefixes = prefixes
	state.Generations = make(map[string]uint64, len(prefixes))
	if !resetGenerations {
		for _, prefix := range prefixes {
			if generation, ok := previous[prefix]; ok {
				state.Generations[prefix] = generation
			}
		}
	}
	state.Status = DKVSSubscriptionSyncing
	state.LastErrorCode = ""
	state.LastSyncAtMS = uint64(time.Now().UnixMilli())
	batch := s.db.NewWriteBatch()
	defer batch.Close()
	if err := putSubscriptionStateBatch(batch, namespace, state); err != nil {
		return err
	}
	return batch.Flush()
}

func (s *ReplicaStore) CompletePrefixSync(namespace, endpointID string,
	prefixes []string) error {
	prefixes, err := NormalizeSubscriptionPrefixes(prefixes)
	if err != nil || strings.TrimSpace(endpointID) == "" {
		return dkvsindexer.ErrInvalidSnapshot
	}
	state, err := s.LoadSubscriptionState(namespace)
	if err != nil {
		return err
	}
	if state.EndpointID != endpointID {
		return dkvsindexer.ErrEndpointMismatch
	}
	for _, prefix := range prefixes {
		if _, ok := state.Generations[prefix]; !ok {
			return dkvsindexer.ErrInvalidSnapshot
		}
	}
	state.Prefixes = prefixes
	state.Status = DKVSSubscriptionReady
	state.LastErrorCode = ""
	state.LastSyncAtMS = uint64(time.Now().UnixMilli())
	batch := s.db.NewWriteBatch()
	defer batch.Close()
	if err := putSubscriptionStateBatch(batch, namespace, state); err != nil {
		return err
	}
	return batch.Flush()
}

func (s *ReplicaStore) AddRegisteredPrefix(namespace, prefix string) error {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	if _, err := NormalizeSubscriptionPrefixes([]string{prefix}); err != nil {
		return err
	}
	prefixes, err := s.LoadRegisteredPrefixes(namespace)
	if err != nil && !errors.Is(err, indexercommon.ErrKeyNotFound) {
		return err
	}
	for _, existing := range prefixes {
		if existing == prefix {
			return nil
		}
	}
	prefixes = append(prefixes, prefix)
	return s.PersistRegisteredPrefixes(namespace, prefixes)
}

func (s *ReplicaStore) RemoveRegisteredPrefix(namespace, prefix string) error {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	prefixes, err := s.LoadRegisteredPrefixes(namespace)
	if err != nil {
		return err
	}
	filtered := prefixes[:0]
	for _, existing := range prefixes {
		if existing != prefix {
			filtered = append(filtered, existing)
		}
	}
	return s.PersistRegisteredPrefixes(namespace, filtered)
}

func (s *ReplicaStore) KeyIsSubscribed(namespace, key string) (bool, error) {
	prefixes, err := s.LoadRegisteredPrefixes(namespace)
	if err != nil {
		return false, err
	}
	return KeyCoveredByPrefixes(key, prefixes), nil
}

func (s *ReplicaStore) HasActiveFreeLocal(namespace string) (bool, error) {
	if s == nil || s.db == nil {
		return false, ErrReplicaNotReady
	}
	base := dkvsNamespacedPrefix(dkvsSubscriptionKeyStatePrefix, namespace)
	found := false
	baseText := string(base)
	err := s.db.BatchRead(base, false, func(storageKey, value []byte) error {
		key := strings.TrimPrefix(string(storageKey), baseText)
		if key == "" || string(storageKey) == key {
			return dkvsindexer.ErrInvalidRecord
		}
		state, err := decodeDKVSLocalKeyState(value, key)
		if err != nil {
			return err
		}
		if !state.Deleted && state.StorageMode == dkvsindexer.StorageModeFreeLocal {
			found = true
		}
		return nil
	})
	return found, err
}

func (s *ReplicaStore) PendingFreeLocalOutbox(namespace string) (bool, error) {
	entries, err := s.LoadOutbox(namespace)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.State == DKVSOutboxTerminal || entry.State == DKVSOutboxConflict {
			continue
		}
		mutations, err := entry.DecodeMutations()
		if err != nil {
			return false, err
		}
		if BatchContainsFreeLocal(mutations) {
			return true, nil
		}
	}
	return false, nil
}

func (s *ReplicaStore) PruneToPrefixes(namespace string, prefixes []string) error {
	prefixes, err := NormalizeSubscriptionPrefixes(prefixes)
	if err != nil {
		return err
	}
	for _, base := range [][]byte{dkvsSubscriptionRecordPrefix, dkvsSubscriptionKeyStatePrefix} {
		storagePrefix := dkvsNamespacedPrefix(base, namespace)
		keys := make([][]byte, 0)
		if err := s.db.BatchRead(storagePrefix, false, func(key, _ []byte) error {
			keys = append(keys, append([]byte(nil), key...))
			return nil
		}); err != nil {
			return err
		}
		batch := s.db.NewWriteBatch()
		for _, storageKey := range keys {
			key := strings.TrimPrefix(string(storageKey), string(storagePrefix))
			if key == "" || KeyCoveredByPrefixes(key, prefixes) {
				continue
			}
			if err := batch.Delete(storageKey); err != nil {
				batch.Close()
				return err
			}
		}
		if err := batch.Flush(); err != nil {
			batch.Close()
			return err
		}
		batch.Close()
	}
	return nil
}

func MergeSubscriptionPrefixes(values ...[]string) ([]string, error) {
	set := make(map[string]struct{})
	for _, list := range values {
		for _, value := range list {
			set[value] = struct{}{}
		}
	}
	merged := make([]string, 0, len(set))
	for value := range set {
		merged = append(merged, value)
	}
	sort.Strings(merged)
	return NormalizeSubscriptionPrefixes(merged)
}
