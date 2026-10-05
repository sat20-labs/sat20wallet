package dkvs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const (
	DKVSSubscriptionSyncing       = "SYNCING"
	DKVSSubscriptionReady         = "READY"
	DKVSSubscriptionOfflineReady  = "OFFLINE_READY"
	DKVSSubscriptionResetRequired = "RESET_REQUIRED"
	DKVSSubscriptionError         = "ERROR"
	DKVSOutboxPending             = "pending"
	DKVSOutboxInflight            = "inflight"
	DKVSOutboxConflict            = "conflict"
	DKVSOutboxTerminal            = "terminal"
)

var (
	dkvsSubscriptionStatePrefix  = []byte("dkvs:subscription-state:")
	dkvsSubscriptionPrefixPrefix = []byte("dkvs:subscription-prefix:")
	dkvsSubscriptionRecordPrefix = []byte("dkvs:subscription-record:")
	dkvsOutboxPrefix             = []byte("dkvs:outbox:")
)

func OutboxPrefix() []byte { return append([]byte(nil), dkvsOutboxPrefix...) }

type SubscriptionState struct {
	EndpointID string   `json:"endpoint_id"`
	Prefixes   []string `json:"prefixes"`
	// Generations is derived from ActiveMeta when loaded; it is not persisted here.
	Generations   map[string]uint64 `json:"generations,omitempty"`
	ViewHeight    uint64            `json:"view_height"`
	LastSyncAtMS  uint64            `json:"last_sync_at_ms"`
	Status        string            `json:"status"`
	LastErrorCode string            `json:"last_error_code,omitempty"`
}

type LocalKeyState struct {
	Key          string                  `json:"key"`
	Seq          uint64                  `json:"seq"`
	ETag         string                  `json:"etag"`
	Deleted      bool                    `json:"deleted"`
	ExpiryHeight uint64                  `json:"expiry_height,omitempty"`
	StorageMode  dkvsindexer.StorageMode `json:"storage_mode,omitempty"`
}

type PersistedMutation struct {
	Record       []byte `json:"record"`
	ExpectedETag string `json:"expected_etag,omitempty"`
	ExpectAbsent bool   `json:"expect_absent,omitempty"`
}

type BatchOutboxEntry struct {
	RequestID        string              `json:"request_id"`
	Namespace        string              `json:"namespace"`
	EndpointID       string              `json:"endpoint_id,omitempty"`
	Mutations        []PersistedMutation `json:"mutations"`
	State            string              `json:"state"`
	Attempts         uint32              `json:"attempts"`
	LastErrorCode    string              `json:"last_error_code,omitempty"`
	LastError        string              `json:"last_error,omitempty"`
	CreatedAtMS      uint64              `json:"created_at_ms"`
	UpdatedAtMS      uint64              `json:"updated_at_ms"`
	OriginKey        string              `json:"origin_key,omitempty"`
	OriginDomain     string              `json:"origin_domain,omitempty"`
	OriginGeneration uint64              `json:"origin_generation,omitempty"`
	// Persist the exact signed operation context BEFORE the first send. A
	// retry must not silently obtain a new generation and authorize an old
	// operation again after another device has changed or deleted the data.
	Authorization *dkvsindexer.WalletWriteAuthorization `json:"authorization,omitempty"`
}

func dkvsNamespacedKey(prefix []byte, namespace string) []byte {
	return append(append([]byte(nil), prefix...), strings.TrimSpace(namespace)...)
}
func dkvsNamespacedPrefix(prefix []byte, namespace string) []byte {
	return append(dkvsNamespacedKey(prefix, namespace), ':')
}
func dkvsSubscriptionStateKey(namespace string) []byte {
	return dkvsNamespacedKey(dkvsSubscriptionStatePrefix, namespace)
}
func dkvsSubscriptionPrefixKey(namespace, prefix string) []byte {
	return append(dkvsNamespacedPrefix(dkvsSubscriptionPrefixPrefix, namespace), prefix...)
}
func dkvsSubscriptionRecordKey(namespace, key string) []byte {
	return append(dkvsNamespacedPrefix(dkvsSubscriptionRecordPrefix, namespace), key...)
}
func dkvsOutboxNamespacePrefix(namespace string) []byte {
	return dkvsNamespacedPrefix(dkvsOutboxPrefix, namespace)
}
func dkvsOutboxKey(namespace, requestID string) []byte {
	return append(dkvsOutboxNamespacePrefix(namespace), requestID...)
}
func OutboxKey(namespace, requestID string) []byte { return dkvsOutboxKey(namespace, requestID) }

func NormalizeSubscriptionPrefixes(prefixes []string) ([]string, error) {
	if len(prefixes) > dkvsindexer.MaxPrefixesPerTerminal {
		return nil, dkvsindexer.ErrTooManySubscriptions
	}
	set := make(map[string]struct{}, len(prefixes))
	for _, prefix := range prefixes {
		prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
		if prefix == "" || len(prefix) > dkvsindexer.MaxPrefixLength {
			return nil, dkvsindexer.ErrInvalidKey
		}
		if _, err := dkvsindexer.ParsePrefix(prefix); err != nil {
			return nil, err
		}
		set[prefix] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for prefix := range set {
		result = append(result, prefix)
	}
	sort.Strings(result)
	return result, nil
}
func walletSubscriptionMatches(prefix, key string) bool {
	return key == prefix || strings.HasPrefix(key, prefix+"/")
}
func SubscriptionMatches(prefix, key string) bool { return walletSubscriptionMatches(prefix, key) }
func KeyCoveredByPrefixes(key string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if walletSubscriptionMatches(prefix, key) {
			return true
		}
	}
	return false
}
func NewRequestID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}

func (s *ReplicaStore) collectStorageKeys(prefix []byte) ([][]byte, error) {
	if s == nil || s.db == nil {
		return nil, ErrReplicaNotReady
	}
	var keys [][]byte
	err := s.db.BatchRead(prefix, false, func(key, _ []byte) error {
		keys = append(keys, append([]byte(nil), key...))
		return nil
	})
	return keys, err
}
func (s *ReplicaStore) LoadSubscriptionState(namespace string) (*SubscriptionState, error) {
	if s == nil || s.db == nil || strings.TrimSpace(namespace) == "" {
		return nil, ErrReplicaNotReady
	}
	encoded, err := s.db.Read(dkvsSubscriptionStateKey(namespace))
	if err != nil {
		return nil, err
	}
	state, err := decodeDKVSSubscriptionState(encoded)
	if err != nil {
		return nil, err
	}
	state.Generations = make(map[string]uint64, len(state.Prefixes))
	for _, prefix := range state.Prefixes {
		meta, err := s.LoadActiveMeta(namespace, dkvsindexer.ActiveScope{Prefix: prefix})
		if errors.Is(err, indexercommon.ErrKeyNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if meta.EndpointID == state.EndpointID {
			state.Generations[prefix] = meta.Generation
		}
	}
	return state, nil
}
func putSubscriptionStateBatch(batch batchWriter, namespace string, state *SubscriptionState) error {
	if batch == nil || strings.TrimSpace(namespace) == "" || state == nil {
		return dkvsindexer.ErrInvalidRecord
	}
	prefixes, err := NormalizeSubscriptionPrefixes(state.Prefixes)
	if err != nil {
		return err
	}
	copyState := *state
	copyState.Prefixes = prefixes
	encoded, err := encodeDKVSSubscriptionState(&copyState)
	if err != nil {
		return err
	}
	return batch.Put(dkvsSubscriptionStateKey(namespace), encoded)
}
func (s *ReplicaStore) replacePrefixRegistryBatch(batch batchWriter, namespace string, prefixes []string) error {
	if batch == nil {
		return dkvsindexer.ErrInvalidRecord
	}
	keys, err := s.collectStorageKeys(dkvsNamespacedPrefix(dkvsSubscriptionPrefixPrefix, namespace))
	if err != nil {
		return err
	}
	for _, key := range keys {
		if err := batch.Delete(key); err != nil {
			return err
		}
	}
	for _, prefix := range prefixes {
		if err := batch.Put(dkvsSubscriptionPrefixKey(namespace, prefix), []byte{1}); err != nil {
			return err
		}
	}
	return nil
}
func localStateFromRecord(record *swire.DKVSRecord) (LocalKeyState, error) {
	if record == nil {
		return LocalKeyState{}, dkvsindexer.ErrInvalidRecord
	}
	state := LocalKeyState{Key: record.Key, Seq: record.Seq, ETag: dkvsindexer.RecordHash(record).String(),
		Deleted: dkvsindexer.IsTombstone(record.Flags), ExpiryHeight: dkvsindexer.RecordExpiryHeight(record)}
	if len(record.FeeProof) != 0 {
		proof, err := dkvsindexer.ParseFeeProof(record.FeeProof)
		if err != nil && !state.Deleted {
			return LocalKeyState{}, err
		}
		if err == nil {
			switch proof.Mode {
			case dkvsindexer.FeeModeFreeLocal:
				state.StorageMode = dkvsindexer.StorageModeFreeLocal
			case dkvsindexer.FeeModeAutopay:
				state.StorageMode = dkvsindexer.StorageModeAutopay
			default:
				state.StorageMode = dkvsindexer.StorageModePaid
			}
		}
	}
	return state, nil
}
func (s *ReplicaStore) LoadSubscriptionRecord(namespace, key string) (*swire.DKVSRecord, error) {
	encoded, err := s.db.Read(dkvsSubscriptionRecordKey(namespace, key))
	if err != nil {
		return nil, err
	}
	return dkvsindexer.UnmarshalRecord(encoded)
}
func (s *ReplicaStore) LoadLocalKeyState(namespace, key string) (*LocalKeyState, error) {
	record, err := s.LoadSubscriptionRecord(namespace, key)
	if err != nil {
		return nil, err
	}
	state, err := localStateFromRecord(record)
	return &state, err
}
func (s *ReplicaStore) ListSubscriptionRecords(namespace string) ([]*swire.DKVSRecord, error) {
	prefix := dkvsNamespacedPrefix(dkvsSubscriptionRecordPrefix, namespace)
	var records []*swire.DKVSRecord
	err := s.db.BatchRead(prefix, false, func(_, value []byte) error {
		record, err := dkvsindexer.UnmarshalRecord(value)
		if err != nil {
			return err
		}
		records = append(records, record)
		return nil
	})
	sort.Slice(records, func(a, b int) bool { return records[a].Key < records[b].Key })
	return records, err
}
func persistedMutationFromCASFinal(mutation dkvsindexer.CASMutation) (PersistedMutation, error) {
	if mutation.Record == nil || !mutation.Precondition.Valid() {
		return PersistedMutation{}, dkvsindexer.ErrInvalidRecord
	}
	encoded, err := dkvsindexer.MarshalRecord(mutation.Record)
	if err != nil {
		return PersistedMutation{}, err
	}
	stored := PersistedMutation{Record: encoded, ExpectAbsent: mutation.Precondition.ExpectAbsent}
	if mutation.Precondition.ExpectedHash != nil {
		stored.ExpectedETag = mutation.Precondition.ExpectedHash.String()
	}
	return stored, nil
}
func (entry *BatchOutboxEntry) DecodeMutations() ([]dkvsindexer.CASMutation, error) {
	if entry == nil || entry.RequestID == "" || entry.Namespace == "" || len(entry.Mutations) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	mutations := make([]dkvsindexer.CASMutation, 0, len(entry.Mutations))
	for _, stored := range entry.Mutations {
		record, err := dkvsindexer.UnmarshalRecord(stored.Record)
		if err != nil {
			return nil, err
		}
		condition := dkvsindexer.WritePrecondition{ExpectAbsent: stored.ExpectAbsent}
		if stored.ExpectedETag != "" {
			hash, err := chainhash.NewHashFromStr(stored.ExpectedETag)
			if err != nil {
				return nil, dkvsindexer.ErrInvalidRecord
			}
			condition.ExpectedHash = hash
		}
		if !condition.Valid() {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		mutations = append(mutations, dkvsindexer.CASMutation{Record: record, Precondition: condition})
	}
	return mutations, nil
}
func NewBatchOutboxEntry(namespace string, mutations []dkvsindexer.CASMutation, endpointID string, origin OutboxOrigin) (*BatchOutboxEntry, error) {
	if strings.TrimSpace(namespace) == "" || len(mutations) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	requestID, err := NewRequestID()
	if err != nil {
		return nil, err
	}
	stored, seen := make([]PersistedMutation, 0, len(mutations)), make(map[string]struct{}, len(mutations))
	for _, mutation := range mutations {
		if mutation.Record == nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		if _, exists := seen[mutation.Record.Key]; exists {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		seen[mutation.Record.Key] = struct{}{}
		item, err := persistedMutationFromCASFinal(mutation)
		if err != nil {
			return nil, err
		}
		stored = append(stored, item)
	}
	now := uint64(time.Now().UnixMilli())
	return &BatchOutboxEntry{RequestID: requestID, Namespace: strings.TrimSpace(namespace), EndpointID: strings.TrimSpace(endpointID),
		Mutations: stored, State: DKVSOutboxPending, CreatedAtMS: now, UpdatedAtMS: now,
		OriginKey: strings.TrimSpace(origin.Key), OriginDomain: strings.TrimSpace(origin.Domain), OriginGeneration: origin.Generation}, nil
}
func (s *ReplicaStore) writeOutboxEntry(entry *BatchOutboxEntry) error {
	if s == nil || s.db == nil || entry == nil || entry.RequestID == "" || entry.Namespace == "" {
		return dkvsindexer.ErrInvalidRecord
	}
	if _, err := entry.DecodeMutations(); err != nil {
		return err
	}
	encoded, err := encodeDKVSOutboxEntry(entry)
	if err != nil {
		return err
	}
	return s.db.Write(dkvsOutboxKey(entry.Namespace, entry.RequestID), encoded)
}
func (s *ReplicaStore) QueueOutbox(entry *BatchOutboxEntry) error {
	if s == nil || s.db == nil || entry == nil {
		return dkvsindexer.ErrInvalidRecord
	}
	_, err := s.db.Read(dkvsOutboxKey(entry.Namespace, entry.RequestID))
	if err == nil {
		return dkvsindexer.ErrWriteConflict
	}
	if !errors.Is(err, indexercommon.ErrKeyNotFound) {
		return err
	}
	return s.writeOutboxEntry(entry)
}
func (s *ReplicaStore) LoadOutbox(namespace string) ([]*BatchOutboxEntry, error) {
	prefix := dkvsOutboxNamespacePrefix(namespace)
	var entries []*BatchOutboxEntry
	err := s.db.BatchRead(prefix, false, func(key, value []byte) error {
		requestID := strings.TrimPrefix(string(key), string(prefix))
		entry, err := decodeDKVSOutboxEntry(value, strings.TrimSpace(namespace), requestID)
		if err != nil || requestID == "" || string(key) != string(dkvsOutboxKey(namespace, requestID)) {
			return dkvsindexer.ErrInvalidRecord
		}
		if _, err := entry.DecodeMutations(); err != nil {
			return err
		}
		entries = append(entries, entry)
		return nil
	})
	sort.Slice(entries, func(a, b int) bool {
		if entries[a].CreatedAtMS == entries[b].CreatedAtMS {
			return entries[a].RequestID < entries[b].RequestID
		}
		return entries[a].CreatedAtMS < entries[b].CreatedAtMS
	})
	return entries, err
}
func (s *ReplicaStore) UpdateOutboxState(entry *BatchOutboxEntry, state string, cause error) error {
	if entry == nil {
		return dkvsindexer.ErrInvalidRecord
	}
	copyEntry := *entry
	copyEntry.State, copyEntry.UpdatedAtMS = state, uint64(time.Now().UnixMilli())
	if state == DKVSOutboxInflight {
		copyEntry.Attempts++
	}
	copyEntry.LastErrorCode, copyEntry.LastError = "", ""
	if cause != nil {
		copyEntry.LastErrorCode, copyEntry.LastError = string(dkvsindexer.ErrorCodeOf(cause)), cause.Error()
	}
	if err := s.writeOutboxEntry(&copyEntry); err != nil {
		return err
	}
	*entry = copyEntry
	return nil
}

// A rejected request can be discarded. Unknown delivery retains its signed
// authorization; reconnect never creates a fresh authorization for it.
func (s *ReplicaStore) DiscardOutbox(entry *BatchOutboxEntry) error {
	if s == nil || s.db == nil || entry == nil || entry.Namespace == "" || entry.RequestID == "" {
		return dkvsindexer.ErrInvalidRecord
	}
	return s.db.Delete(dkvsOutboxKey(entry.Namespace, entry.RequestID))
}

// ApplyWriteResultAndAck confirms REQUEST completion only. It never creates,
// overwrites or deletes a confirmed KV, even if the ACK arrived before the
// subscription update. InstallActiveState is the single replica commit path.
func (s *ReplicaStore) ApplyWriteResultAndAck(entry *BatchOutboxEntry, result *dkvsindexer.WriteResult) error {
	if s == nil || s.db == nil || entry == nil || result == nil {
		return dkvsindexer.ErrInvalidRecord
	}
	unlock := lockActiveReplica(entry.Namespace)
	defer unlock()
	mutations, err := entry.DecodeMutations()
	if err != nil {
		return err
	}
	if err := VerifyWriteResult(mutations, entry.RequestID, result); err != nil {
		return err
	}
	if result.EndpointID == "" {
		return dkvsindexer.ErrInvalidRecord
	}
	if entry.EndpointID != "" && entry.EndpointID != result.EndpointID {
		return dkvsindexer.ErrEndpointMismatch
	}
	expectedPrefixes := make(map[string]struct{})
	for _, mutation := range mutations {
		prefix, err := dkvsindexer.CollectionPathForKey(mutation.Record.Key)
		if err != nil || prefix == "" {
			return dkvsindexer.ErrInvalidRecord
		}
		expectedPrefixes[prefix] = struct{}{}
	}
	registered, err := s.LoadRegisteredPrefixes(entry.Namespace)
	if err != nil {
		return err
	}
	generations := make(map[string]uint64, len(result.PrefixStates))
	for _, item := range result.PrefixStates {
		prefix := strings.TrimSuffix(strings.TrimSpace(item.Prefix), "/")
		if _, exists := expectedPrefixes[prefix]; !exists {
			return dkvsindexer.ErrInvalidRecord
		}
		if _, duplicate := generations[prefix]; duplicate {
			return dkvsindexer.ErrInvalidRecord
		}
		if item.Generation == 0 && result.Applied != 0 {
			return dkvsindexer.ErrInvalidRecord
		}
		generations[prefix] = item.Generation
	}
	for prefix := range expectedPrefixes {
		if !KeyCoveredByPrefixes(prefix, registered) {
			continue
		}
		if _, exists := generations[prefix]; !exists {
			return dkvsindexer.ErrInvalidRecord
		}
		state, err := s.LoadSubscriptionState(entry.Namespace)
		if err != nil || state.EndpointID != result.EndpointID || !KeyCoveredByPrefixes(prefix, state.Prefixes) {
			return dkvsindexer.ErrEndpointMismatch
		}
	}
	batch := s.db.NewWriteBatch()
	defer batch.Close()
	// This request-completion bound only invalidates an older in-flight full
	// view. It is not a completed sync cursor and does not materialize values.
	if err := s.NoteActiveAckBatch(batch, entry.Namespace, result); err != nil {
		return err
	}
	if err := batch.Delete(dkvsOutboxKey(entry.Namespace, entry.RequestID)); err != nil {
		return err
	}
	return batch.Flush()
}

func (s *ReplicaStore) HasPendingOutbox(namespace string) (bool, error) {
	entries, err := s.LoadOutbox(namespace)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.State == DKVSOutboxPending || entry.State == DKVSOutboxInflight {
			return true, nil
		}
	}
	return false, nil
}
func (s *ReplicaStore) SetSubscriptionError(namespace, status, code string) error {
	state, err := s.LoadSubscriptionState(namespace)
	if err != nil {
		if errors.Is(err, indexercommon.ErrKeyNotFound) {
			state = &SubscriptionState{}
		} else {
			return err
		}
	}
	state.Status, state.LastErrorCode, state.LastSyncAtMS = status, code, uint64(time.Now().UnixMilli())
	batch := s.db.NewWriteBatch()
	defer batch.Close()
	if err := putSubscriptionStateBatch(batch, namespace, state); err != nil {
		return err
	}
	return batch.Flush()
}
func (s *ReplicaStore) MarkOfflineReady(namespace string) error {
	state, err := s.LoadSubscriptionState(namespace)
	if err != nil {
		return err
	}
	if state.Status != DKVSSubscriptionReady && state.Status != DKVSSubscriptionOfflineReady {
		return ErrReplicaNotReady
	}
	state.Status = DKVSSubscriptionOfflineReady
	batch := s.db.NewWriteBatch()
	defer batch.Close()
	if err := putSubscriptionStateBatch(batch, namespace, state); err != nil {
		return err
	}
	return batch.Flush()
}
func (s *ReplicaStore) SubscriptionKeyStateOrNotFound(namespace, key string) (*LocalKeyState, error) {
	state, err := s.LoadLocalKeyState(namespace, key)
	if errors.Is(err, indexercommon.ErrKeyNotFound) {
		return nil, dkvsindexer.ErrRecordNotFound
	}
	return state, err
}
func (s *ReplicaStore) ValidateStorage() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("DKVS replica store is unavailable")
	}
	return nil
}
