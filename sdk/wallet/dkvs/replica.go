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

	DKVSOutboxPending  = "pending"
	DKVSOutboxInflight = "inflight"
	DKVSOutboxConflict = "conflict"
	DKVSOutboxTerminal = "terminal"
)

var (
	dkvsSubscriptionStatePrefix    = []byte("dkvs:subscription-state:")
	dkvsSubscriptionPrefixPrefix   = []byte("dkvs:subscription-prefix:")
	dkvsSubscriptionRecordPrefix   = []byte("dkvs:subscription-record:")
	dkvsSubscriptionKeyStatePrefix = []byte("dkvs:subscription-key-state:")
	dkvsOutboxPrefix               = []byte("dkvs:outbox:")
)

func OutboxPrefix() []byte {
	return append([]byte(nil), dkvsOutboxPrefix...)
}

type SubscriptionState struct {
	EndpointID    string            `json:"endpoint_id"`
	Prefixes      []string          `json:"prefixes"`
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
	// PreservePrefixGenerations is set only after an authoritative conflict
	// rebase. The acknowledged records are materialized, but the old prefix
	// generation is retained so a later status check cannot miss unrelated
	// remote changes in the same collection.
	PreservePrefixGenerations bool `json:"preserve_prefix_generations,omitempty"`
}

func dkvsNamespacedKey(prefix []byte, namespace string) []byte {
	key := append([]byte(nil), prefix...)
	return append(key, strings.TrimSpace(namespace)...)
}

func dkvsNamespacedPrefix(prefix []byte, namespace string) []byte {
	key := dkvsNamespacedKey(prefix, namespace)
	return append(key, ':')
}

func dkvsSubscriptionStateKey(namespace string) []byte {
	return dkvsNamespacedKey(dkvsSubscriptionStatePrefix, namespace)
}

func dkvsSubscriptionPrefixKey(namespace, prefix string) []byte {
	key := dkvsNamespacedPrefix(dkvsSubscriptionPrefixPrefix, namespace)
	return append(key, prefix...)
}

func dkvsSubscriptionRecordKey(namespace, key string) []byte {
	storage := dkvsNamespacedPrefix(dkvsSubscriptionRecordPrefix, namespace)
	return append(storage, key...)
}

func dkvsSubscriptionKeyStateKey(namespace, key string) []byte {
	storage := dkvsNamespacedPrefix(dkvsSubscriptionKeyStatePrefix, namespace)
	return append(storage, key...)
}

func dkvsOutboxNamespacePrefix(namespace string) []byte {
	return dkvsNamespacedPrefix(dkvsOutboxPrefix, namespace)
}

func dkvsOutboxKey(namespace, requestID string) []byte {
	key := dkvsOutboxNamespacePrefix(namespace)
	return append(key, requestID...)
}

func OutboxKey(namespace, requestID string) []byte {
	return dkvsOutboxKey(namespace, requestID)
}

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

func SubscriptionMatches(prefix, key string) bool {
	return walletSubscriptionMatches(prefix, key)
}

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
	keys := make([][]byte, 0)
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
	return decodeDKVSSubscriptionState(encoded)
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
	copyState.Generations = make(map[string]uint64, len(state.Generations))
	for prefix, generation := range state.Generations {
		copyState.Generations[prefix] = generation
	}
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

func localStateFromServer(state dkvsindexer.DKVSKeyState) LocalKeyState {
	return LocalKeyState{
		Key: state.Key, Seq: state.Seq, ETag: state.ETag,
		Deleted:      state.Status != dkvsindexer.KeyStateActive,
		ExpiryHeight: state.ExpiryHeight, StorageMode: state.StorageMode,
	}
}

func localStateFromRecord(record *swire.DKVSRecord) (LocalKeyState, error) {
	if record == nil {
		return LocalKeyState{}, dkvsindexer.ErrInvalidRecord
	}
	state := LocalKeyState{
		Key: record.Key, Seq: record.Seq, ETag: dkvsindexer.RecordHash(record).String(),
		Deleted:      dkvsindexer.IsTombstone(record.Flags),
		ExpiryHeight: dkvsindexer.RecordExpiryHeight(record),
	}
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

func putLocalKeyStateBatch(batch batchWriter, namespace string, state LocalKeyState) error {
	if batch == nil || state.Key == "" || state.Seq == 0 || state.ETag == "" {
		return dkvsindexer.ErrInvalidRecord
	}
	encoded, err := encodeDKVSLocalKeyState(state)
	if err != nil {
		return err
	}
	return batch.Put(dkvsSubscriptionKeyStateKey(namespace, state.Key), encoded)
}

// ReplacePrefixSnapshot atomically replaces only one managed canonical path.
// Other managed paths in the same wallet replica are left untouched.
func (s *ReplicaStore) ReplacePrefixSnapshot(namespace string,
	snapshot *dkvsindexer.PrefixSnapshot) ([]string, error) {
	if s == nil || s.db == nil || snapshot == nil || strings.TrimSpace(snapshot.EndpointID) == "" {
		return nil, dkvsindexer.ErrInvalidSnapshot
	}
	prefixes, err := NormalizeSubscriptionPrefixes([]string{snapshot.Prefix})
	if err != nil || len(prefixes) != 1 || prefixes[0] != snapshot.Prefix {
		return nil, dkvsindexer.ErrInvalidSnapshot
	}
	prefix := prefixes[0]
	statesByKey := make(map[string]dkvsindexer.DKVSKeyState, len(snapshot.KeyStates))
	for _, state := range snapshot.KeyStates {
		if state.Key == "" || !walletSubscriptionMatches(prefix, state.Key) {
			return nil, dkvsindexer.ErrInvalidSnapshot
		}
		statesByKey[state.Key] = state
	}
	for _, record := range snapshot.Records {
		if record == nil || !walletSubscriptionMatches(prefix, record.Key) {
			return nil, dkvsindexer.ErrInvalidSnapshot
		}
		if err := dkvsindexer.VerifyRecordForClient(record, dkvsindexer.RecordVerificationOptions{
			ExpectedKey: record.Key, Height: snapshot.ViewHeight,
		}); err != nil {
			return nil, err
		}
		state, ok := statesByKey[record.Key]
		if !ok || state.Status != dkvsindexer.KeyStateActive || state.Seq != record.Seq ||
			state.ETag != dkvsindexer.RecordHash(record).String() {
			return nil, dkvsindexer.ErrInvalidSnapshot
		}
	}

	oldRecords, err := s.ListSubscriptionRecords(namespace)
	if err != nil {
		return nil, err
	}
	oldHashes := make(map[string]string)
	for _, record := range oldRecords {
		if record != nil && walletSubscriptionMatches(prefix, record.Key) {
			oldHashes[record.Key] = dkvsindexer.RecordHash(record).String()
		}
	}
	changed := make(map[string]struct{})
	newHashes := make(map[string]string, len(snapshot.Records))
	for _, record := range snapshot.Records {
		hash := dkvsindexer.RecordHash(record).String()
		newHashes[record.Key] = hash
		if oldHashes[record.Key] != hash {
			changed[record.Key] = struct{}{}
		}
	}
	for key := range oldHashes {
		if _, exists := newHashes[key]; !exists {
			changed[key] = struct{}{}
		}
	}

	batch := s.db.NewWriteBatch()
	defer batch.Close()
	for _, base := range [][]byte{
		dkvsNamespacedPrefix(dkvsSubscriptionRecordPrefix, namespace),
		dkvsNamespacedPrefix(dkvsSubscriptionKeyStatePrefix, namespace),
	} {
		baseText := string(base)
		keys, collectErr := s.collectStorageKeys(base)
		if collectErr != nil {
			return nil, collectErr
		}
		for _, storageKey := range keys {
			key := strings.TrimPrefix(string(storageKey), baseText)
			if walletSubscriptionMatches(prefix, key) {
				if err := batch.Delete(storageKey); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, record := range snapshot.Records {
		encoded, err := dkvsindexer.MarshalRecord(record)
		if err != nil {
			return nil, err
		}
		if err := batch.Put(dkvsSubscriptionRecordKey(namespace, record.Key), encoded); err != nil {
			return nil, err
		}
	}
	for _, serverState := range snapshot.KeyStates {
		if serverState.Seq == 0 || serverState.ETag == "" {
			continue
		}
		if err := putLocalKeyStateBatch(batch, namespace, localStateFromServer(serverState)); err != nil {
			return nil, err
		}
	}
	state, stateErr := s.LoadSubscriptionState(namespace)
	if stateErr != nil {
		if !errors.Is(stateErr, indexercommon.ErrKeyNotFound) {
			return nil, stateErr
		}
		state = &SubscriptionState{Prefixes: []string{prefix}}
	}
	if state.Generations == nil {
		state.Generations = make(map[string]uint64)
	}
	state.EndpointID = snapshot.EndpointID
	state.Generations[prefix] = snapshot.Generation
	if snapshot.ViewHeight > state.ViewHeight {
		state.ViewHeight = snapshot.ViewHeight
	}
	state.LastSyncAtMS = uint64(time.Now().UnixMilli())
	if state.Status == "" {
		state.Status = DKVSSubscriptionSyncing
	}
	state.LastErrorCode = ""
	if err := putSubscriptionStateBatch(batch, namespace, state); err != nil {
		return nil, err
	}
	if err := batch.Flush(); err != nil {
		return nil, err
	}
	changedKeys := make([]string, 0, len(changed))
	for key := range changed {
		changedKeys = append(changedKeys, key)
	}
	sort.Strings(changedKeys)
	return changedKeys, nil
}

func (s *ReplicaStore) LoadSubscriptionRecord(namespace, key string) (*swire.DKVSRecord, error) {
	encoded, err := s.db.Read(dkvsSubscriptionRecordKey(namespace, key))
	if err != nil {
		return nil, err
	}
	return dkvsindexer.UnmarshalRecord(encoded)
}

func (s *ReplicaStore) LoadLocalKeyState(namespace, key string) (*LocalKeyState, error) {
	encoded, err := s.db.Read(dkvsSubscriptionKeyStateKey(namespace, key))
	if err != nil {
		return nil, err
	}
	return decodeDKVSLocalKeyState(encoded, key)
}

func (s *ReplicaStore) ListSubscriptionRecords(namespace string) ([]*swire.DKVSRecord, error) {
	prefix := dkvsNamespacedPrefix(dkvsSubscriptionRecordPrefix, namespace)
	records := make([]*swire.DKVSRecord, 0)
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

func NewBatchOutboxEntry(namespace string, mutations []dkvsindexer.CASMutation,
	endpointID string, origin OutboxOrigin) (*BatchOutboxEntry, error) {
	if strings.TrimSpace(namespace) == "" || len(mutations) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	requestID, err := NewRequestID()
	if err != nil {
		return nil, err
	}
	stored := make([]PersistedMutation, 0, len(mutations))
	seen := make(map[string]struct{}, len(mutations))
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
	return &BatchOutboxEntry{
		RequestID: requestID, Namespace: strings.TrimSpace(namespace), EndpointID: strings.TrimSpace(endpointID),
		Mutations: stored, State: DKVSOutboxPending, CreatedAtMS: now, UpdatedAtMS: now,
		OriginKey: strings.TrimSpace(origin.Key), OriginDomain: strings.TrimSpace(origin.Domain),
		OriginGeneration:          origin.Generation,
		PreservePrefixGenerations: origin.PreservePrefixGenerations,
	}, nil
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
	if entry == nil {
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
	entries := make([]*BatchOutboxEntry, 0)
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
	copyEntry.State = state
	copyEntry.UpdatedAtMS = uint64(time.Now().UnixMilli())
	if state == DKVSOutboxInflight {
		copyEntry.Attempts++
	}
	copyEntry.LastErrorCode, copyEntry.LastError = "", ""
	if cause != nil {
		copyEntry.LastErrorCode = string(dkvsindexer.ErrorCodeOf(cause))
		copyEntry.LastError = cause.Error()
	}
	if err := s.writeOutboxEntry(&copyEntry); err != nil {
		return err
	}
	*entry = copyEntry
	return nil
}

// DiscardOutbox removes exactly one request after the server has definitively
// rejected its CAS basis. Network failures must never use this path because
// they require replaying the identical signed request.
func (s *ReplicaStore) DiscardOutbox(entry *BatchOutboxEntry) error {
	if s == nil || s.db == nil || entry == nil || entry.Namespace == "" || entry.RequestID == "" {
		return dkvsindexer.ErrInvalidRecord
	}
	return s.db.Delete(dkvsOutboxKey(entry.Namespace, entry.RequestID))
}

func (s *ReplicaStore) ApplyWriteResultAndAck(entry *BatchOutboxEntry,
	result *dkvsindexer.WriteResult) error {
	if s == nil || s.db == nil || entry == nil || result == nil {
		return dkvsindexer.ErrInvalidRecord
	}
	mutations, err := entry.DecodeMutations()
	if err != nil {
		return err
	}
	if result.RequestID != "" && result.RequestID != entry.RequestID {
		return dkvsindexer.ErrInvalidRecord
	}
	if len(result.Records) != len(mutations) || len(result.Hashes) != len(mutations) ||
		(result.Applied != 0 && result.Applied != len(mutations)) {
		return dkvsindexer.ErrInvalidRecord
	}
	expectedPrefixes := make(map[string]struct{})
	for _, mutation := range mutations {
		if mutation.Record == nil {
			return dkvsindexer.ErrInvalidRecord
		}
		prefix, err := dkvsindexer.CollectionPathForKey(mutation.Record.Key)
		if err != nil || strings.TrimSpace(prefix) == "" {
			return dkvsindexer.ErrInvalidRecord
		}
		expectedPrefixes[prefix] = struct{}{}
	}
	if strings.TrimSpace(result.EndpointID) == "" {
		return dkvsindexer.ErrInvalidRecord
	}
	registered, err := s.LoadRegisteredPrefixes(entry.Namespace)
	if err != nil {
		return err
	}
	managedPrefixes := make(map[string]struct{})
	for prefix := range expectedPrefixes {
		if KeyCoveredByPrefixes(prefix, registered) {
			managedPrefixes[prefix] = struct{}{}
		}
	}
	generations := make(map[string]uint64, len(result.PrefixStates))
	for _, prefixState := range result.PrefixStates {
		prefix := strings.TrimSuffix(strings.TrimSpace(prefixState.Prefix), "/")
		if _, ok := expectedPrefixes[prefix]; !ok || prefixState.Generation == 0 {
			return dkvsindexer.ErrInvalidRecord
		}
		if _, duplicate := generations[prefix]; duplicate {
			return dkvsindexer.ErrInvalidRecord
		}
		generations[prefix] = prefixState.Generation
	}
	for prefix := range managedPrefixes {
		if _, ok := generations[prefix]; !ok {
			return dkvsindexer.ErrInvalidRecord
		}
	}
	if entry.EndpointID != "" && entry.EndpointID != result.EndpointID {
		return dkvsindexer.ErrEndpointMismatch
	}
	var state *SubscriptionState
	if len(managedPrefixes) != 0 {
		state, err = s.LoadSubscriptionState(entry.Namespace)
		if err != nil || state.EndpointID != result.EndpointID ||
			(state.Status != DKVSSubscriptionReady && state.Status != DKVSSubscriptionOfflineReady) {
			return dkvsindexer.ErrEndpointMismatch
		}
		if state.Generations == nil {
			state.Generations = make(map[string]uint64)
		}
	}
	for prefix, generation := range generations {
		if _, managed := managedPrefixes[prefix]; !managed {
			continue
		}
		if !KeyCoveredByPrefixes(prefix, state.Prefixes) {
			return dkvsindexer.ErrEndpointMismatch
		}
		if !entry.PreservePrefixGenerations {
			state.Generations[prefix] = generation
		}
	}
	if state != nil {
		if result.ViewHeight > state.ViewHeight {
			state.ViewHeight = result.ViewHeight
		}
		state.LastSyncAtMS = uint64(time.Now().UnixMilli())
		state.Status = DKVSSubscriptionReady
		state.LastErrorCode = ""
	}
	batch := s.db.NewWriteBatch()
	defer batch.Close()
	for index, mutation := range mutations {
		record := result.Records[index]
		if record == nil || dkvsindexer.RecordHash(record) != dkvsindexer.RecordHash(mutation.Record) ||
			result.Hashes[index] != dkvsindexer.RecordHash(mutation.Record).String() {
			return dkvsindexer.ErrInvalidRecord
		}
		if !KeyCoveredByPrefixes(record.Key, registered) {
			continue
		}
		local, err := localStateFromRecord(record)
		if err != nil {
			return err
		}
		if dkvsindexer.IsTombstone(record.Flags) {
			if err := batch.Delete(dkvsSubscriptionRecordKey(entry.Namespace, record.Key)); err != nil {
				return err
			}
			local.Deleted = true
		} else {
			encoded, err := dkvsindexer.MarshalRecord(record)
			if err != nil {
				return err
			}
			if err := batch.Put(dkvsSubscriptionRecordKey(entry.Namespace, record.Key), encoded); err != nil {
				return err
			}
		}
		if err := putLocalKeyStateBatch(batch, entry.Namespace, local); err != nil {
			return err
		}
	}
	if state != nil {
		if err := putSubscriptionStateBatch(batch, entry.Namespace, state); err != nil {
			return err
		}
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
	state.Status = status
	state.LastErrorCode = code
	state.LastSyncAtMS = uint64(time.Now().UnixMilli())
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
	if state.Status == DKVSSubscriptionReady || state.Status == DKVSSubscriptionSyncing {
		state.Status = DKVSSubscriptionOfflineReady
	}
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
