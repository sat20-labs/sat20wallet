package wallet

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const (
	dkvsReplicaStateVersion = uint32(1)
	dkvsOutboxBatchVersion  = uint32(1)
)

const (
	dkvsSessionIdle      = "idle"
	dkvsSessionPrepared  = "prepared"
	dkvsSessionInflight  = "inflight"
	dkvsSessionConfirmed = "confirmed"
	dkvsSessionConflict  = "conflict"
	dkvsSessionError     = "error"
)

var (
	dkvsPathStatePrefix   = []byte("dkvs-path-state-v1:")
	dkvsBatchOutboxPrefix = []byte("dkvs-batch-outbox-v1:")
)

type dkvsPathReplicaState struct {
	Version       uint32                 `json:"version"`
	Path          string                 `json:"path"`
	PathMeta      *dkvsindexer.PathMeta `json:"pathmeta,omitempty"`
	ServerTimeMS  uint64                 `json:"server_time_ms"`
	EndpointID    string                 `json:"endpoint_id,omitempty"`
	HasLocalOnly  bool                   `json:"has_local_only,omitempty"`
	SessionState  string                 `json:"session_state"`
	LastErrorCode string                 `json:"last_error_code,omitempty"`
	UpdatedAtMS   uint64                 `json:"updated_at_ms"`
}

type dkvsPersistedMutation struct {
	Record         []byte `json:"record"`
	ExpectedHash   string `json:"expected_hash,omitempty"`
	ExpectAbsent   bool   `json:"expect_absent,omitempty"`
}

type dkvsPersistedPathPrecondition struct {
	Path               string `json:"path"`
	ExpectedRoot       string `json:"expected_root"`
	ExpectedGeneration uint64 `json:"expected_generation"`
}

type dkvsBatchOutboxEntry struct {
	Version           uint32                          `json:"version"`
	ID                string                          `json:"id"`
	Namespace         string                          `json:"namespace"`
	Mutations         []dkvsPersistedMutation         `json:"mutations"`
	PathPreconditions []dkvsPersistedPathPrecondition `json:"path_preconditions,omitempty"`
	EndpointID        string                          `json:"endpoint_id,omitempty"`
	State             string                          `json:"state"`
	Attempts          uint32                          `json:"attempts"`
	LastErrorCode     string                          `json:"last_error_code,omitempty"`
	CreatedAtMS       uint64                          `json:"created_at_ms"`
	UpdatedAtMS       uint64                          `json:"updated_at_ms"`
}

type dkvsBatchWriter interface {
	Put(key, value []byte) error
	Delete(key []byte) error
}

func cloneWalletPathMeta(meta *dkvsindexer.PathMeta) *dkvsindexer.PathMeta {
	if meta == nil {
		return nil
	}
	copyMeta := *meta
	return &copyMeta
}

func activeRecordsFromPathSnapshot(snapshot *dkvsindexer.PathSnapshot) []*swire.DKVSRecord {
	if snapshot == nil || snapshot.PathMeta == nil {
		return nil
	}
	active := make([]*swire.DKVSRecord, 0, len(snapshot.Records))
	for _, record := range snapshot.Records {
		if record == nil || dkvsindexer.IsTombstone(record.Flags) ||
			dkvsindexer.IsExpired(record, snapshot.PathMeta.ViewHeight, snapshot.ServerTimeMS) {
			continue
		}
		active = append(active, record)
	}
	sort.Slice(active, func(a, b int) bool { return active[a].Key < active[b].Key })
	return active
}

func dkvsPathStateKey(scope string) []byte {
	key := make([]byte, 0, len(dkvsPathStatePrefix)+len(scope))
	key = append(key, dkvsPathStatePrefix...)
	return append(key, scope...)
}

func dkvsOutboxNamespaceScope(namespace string) string {
	hash := sha256.Sum256([]byte(strings.TrimSpace(namespace)))
	return hex.EncodeToString(hash[:])
}

func dkvsBatchOutboxNamespacePrefix(namespace string) []byte {
	scope := dkvsOutboxNamespaceScope(namespace)
	key := make([]byte, 0, len(dkvsBatchOutboxPrefix)+len(scope)+1)
	key = append(key, dkvsBatchOutboxPrefix...)
	key = append(key, scope...)
	return append(key, ':')
}

func dkvsBatchOutboxKey(namespace, id string) []byte {
	key := dkvsBatchOutboxNamespacePrefix(namespace)
	return append(key, id...)
}

func (s *dkvsReplicaStore) loadPathState(scope string) (*dkvsPathReplicaState, error) {
	if s == nil || s.db == nil || scope == "" {
		return nil, ErrDKVSPathNotSynced
	}
	encoded, err := s.db.Read(dkvsPathStateKey(scope))
	if err != nil {
		return nil, err
	}
	var state dkvsPathReplicaState
	if err := json.Unmarshal(encoded, &state); err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if state.Version != dkvsReplicaStateVersion || state.Path == "" || state.SessionState == "" {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	state.PathMeta = cloneWalletPathMeta(state.PathMeta)
	return &state, nil
}

func putPathStateBatch(batch dkvsBatchWriter, scope string, state *dkvsPathReplicaState) error {
	if batch == nil || scope == "" || state == nil || state.Path == "" {
		return dkvsindexer.ErrInvalidRecord
	}
	copyState := *state
	copyState.Version = dkvsReplicaStateVersion
	copyState.PathMeta = cloneWalletPathMeta(state.PathMeta)
	if copyState.SessionState == "" {
		copyState.SessionState = dkvsSessionIdle
	}
	copyState.UpdatedAtMS = uint64(time.Now().UnixMilli())
	encoded, err := json.Marshal(&copyState)
	if err != nil {
		return err
	}
	return batch.Put(dkvsPathStateKey(scope), encoded)
}

func encodeReplicaBaseline(root chainhash.Hash, generation uint64) []byte {
	value := make([]byte, 1+2*chainhash.HashSize+8)
	value[0] = dkvsReplicaVersion
	copy(value[1:1+chainhash.HashSize], root[:])
	copy(value[1+chainhash.HashSize:1+2*chainhash.HashSize], root[:])
	binary.LittleEndian.PutUint64(value[1+2*chainhash.HashSize:], generation)
	return value
}

func putReplicaBaselineBatch(batch dkvsBatchWriter, scope string,
	meta *dkvsindexer.PathMeta) error {
	if batch == nil || scope == "" || meta == nil {
		return dkvsindexer.ErrInvalidRecord
	}
	key := append(append([]byte(nil), dkvsReplicaRootPrefix...), scope...)
	return batch.Put(key, encodeReplicaBaseline(meta.StateRoot, meta.Generation))
}

func (s *dkvsReplicaStore) stageConfirmedReplacement(batch dkvsBatchWriter, scope, path string,
	records []*swire.DKVSRecord, verify dkvsindexer.RecordVerificationOptions) error {
	if s == nil || s.db == nil || batch == nil || scope == "" || path == "" {
		return dkvsindexer.ErrInvalidRecord
	}
	existing, err := s.loadConfirmed(scope)
	if err != nil {
		return err
	}
	for _, record := range existing {
		if record != nil {
			if err := batch.Delete(dkvsReplicaRecordKey(dkvsReplicaConfirmedPrefix, scope, record.Key)); err != nil {
				return err
			}
		}
	}
	seen := make(map[string]chainhash.Hash, len(records))
	for _, record := range records {
		if record == nil || dkvsindexer.IsTombstone(record.Flags) {
			return dkvsindexer.ErrInvalidRecord
		}
		recordPath, err := dkvsindexer.CollectionPathForKey(record.Key)
		if err != nil || recordPath != path {
			return dkvsindexer.ErrInvalidKey
		}
		if err := dkvsindexer.VerifyRecordForClient(record, verify); err != nil {
			return err
		}
		hash := dkvsindexer.RecordHash(record)
		if previous, ok := seen[record.Key]; ok && previous != hash {
			return dkvsindexer.ErrPathDiverged
		}
		seen[record.Key] = hash
		encoded, err := dkvsindexer.MarshalRecord(record)
		if err != nil {
			return err
		}
		if err := batch.Put(dkvsReplicaRecordKey(dkvsReplicaConfirmedPrefix, scope, record.Key), encoded); err != nil {
			return err
		}
	}
	return nil
}

// applyPathSnapshot atomically replaces one path's confirmed replica and its
// network-comparable PathMeta. FREE_LOCAL records are intentionally absent.
func (s *dkvsReplicaStore) applyPathSnapshot(scope string,
	snapshot *dkvsindexer.PathSnapshot) error {
	if snapshot == nil || snapshot.PathMeta == nil {
		return dkvsindexer.ErrInvalidSnapshot
	}
	verify := dkvsindexer.RecordVerificationOptions{
		Height: snapshot.PathMeta.ViewHeight,
		Now:    snapshot.ServerTimeMS,
	}
	if err := dkvsindexer.ValidatePathSnapshotForClient(snapshot, verify); err != nil {
		return err
	}
	batch := s.db.NewWriteBatch()
	defer batch.Close()
	if err := s.stageConfirmedReplacement(batch, scope, snapshot.Path,
		activeRecordsFromPathSnapshot(snapshot), verify); err != nil {
		return err
	}
	if err := putReplicaBaselineBatch(batch, scope, snapshot.PathMeta); err != nil {
		return err
	}
	if err := putPathStateBatch(batch, scope, &dkvsPathReplicaState{
		Path: snapshot.Path, PathMeta: snapshot.PathMeta,
		ServerTimeMS: snapshot.ServerTimeMS, SessionState: dkvsSessionIdle,
	}); err != nil {
		return err
	}
	return batch.Flush()
}

func (s *dkvsReplicaStore) loadConfirmedByKey(scope string) (map[string]*swire.DKVSRecord, error) {
	records, err := s.loadConfirmed(scope)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]*swire.DKVSRecord, len(records))
	for _, record := range records {
		if record != nil {
			byKey[record.Key] = record
		}
	}
	return byKey, nil
}

func persistedMutationFromCAS(mutation dkvsindexer.CASMutation) (dkvsPersistedMutation, error) {
	if mutation.Record == nil || !mutation.Precondition.Valid() {
		return dkvsPersistedMutation{}, dkvsindexer.ErrInvalidRecord
	}
	encoded, err := dkvsindexer.MarshalRecord(mutation.Record)
	if err != nil {
		return dkvsPersistedMutation{}, err
	}
	stored := dkvsPersistedMutation{Record: encoded, ExpectAbsent: mutation.Precondition.ExpectAbsent}
	if mutation.Precondition.ExpectedHash != nil {
		stored.ExpectedHash = mutation.Precondition.ExpectedHash.String()
	}
	return stored, nil
}

func persistedPathPrecondition(condition dkvsindexer.PathWritePrecondition) dkvsPersistedPathPrecondition {
	return dkvsPersistedPathPrecondition{
		Path: condition.Path, ExpectedRoot: condition.ExpectedRoot.String(),
		ExpectedGeneration: condition.ExpectedGeneration,
	}
}

func outboxEntryID(mutations []dkvsPersistedMutation,
	conditions []dkvsPersistedPathPrecondition, endpointID string) string {
	h := sha256.New()
	for _, mutation := range mutations {
		_, _ = h.Write(mutation.Record)
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(mutation.ExpectedHash))
		if mutation.ExpectAbsent {
			_, _ = h.Write([]byte{1})
		} else {
			_, _ = h.Write([]byte{0})
		}
	}
	for _, condition := range conditions {
		_, _ = h.Write([]byte(condition.Path))
		_, _ = h.Write([]byte(condition.ExpectedRoot))
		var scratch [8]byte
		binary.BigEndian.PutUint64(scratch[:], condition.ExpectedGeneration)
		_, _ = h.Write(scratch[:])
	}
	_, _ = h.Write([]byte(endpointID))
	return hex.EncodeToString(h.Sum(nil))
}

func newDKVSBatchOutboxEntry(namespace string, mutations []dkvsindexer.CASMutation,
	conditions []dkvsindexer.PathWritePrecondition, endpointID string) (*dkvsBatchOutboxEntry, error) {
	if strings.TrimSpace(namespace) == "" || len(mutations) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	storedMutations := make([]dkvsPersistedMutation, 0, len(mutations))
	for _, mutation := range mutations {
		stored, err := persistedMutationFromCAS(mutation)
		if err != nil {
			return nil, err
		}
		storedMutations = append(storedMutations, stored)
	}
	storedConditions := make([]dkvsPersistedPathPrecondition, 0, len(conditions))
	for _, condition := range conditions {
		storedConditions = append(storedConditions, persistedPathPrecondition(condition))
	}
	now := uint64(time.Now().UnixMilli())
	entry := &dkvsBatchOutboxEntry{
		Version: dkvsOutboxBatchVersion, Namespace: namespace,
		Mutations: storedMutations, PathPreconditions: storedConditions,
		EndpointID: endpointID, State: dkvsSessionPrepared,
		CreatedAtMS: now, UpdatedAtMS: now,
	}
	entry.ID = outboxEntryID(storedMutations, storedConditions, endpointID)
	return entry, nil
}

func (entry *dkvsBatchOutboxEntry) decode() ([]dkvsindexer.CASMutation,
	[]dkvsindexer.PathWritePrecondition, error) {
	if entry == nil || entry.Version != dkvsOutboxBatchVersion || entry.ID == "" ||
		entry.Namespace == "" || len(entry.Mutations) == 0 {
		return nil, nil, dkvsindexer.ErrInvalidRecord
	}
	mutations := make([]dkvsindexer.CASMutation, 0, len(entry.Mutations))
	for _, stored := range entry.Mutations {
		record, err := dkvsindexer.UnmarshalRecord(stored.Record)
		if err != nil {
			return nil, nil, err
		}
		condition := dkvsindexer.WritePrecondition{ExpectAbsent: stored.ExpectAbsent}
		if stored.ExpectedHash != "" {
			hash, err := chainhash.NewHashFromStr(stored.ExpectedHash)
			if err != nil {
				return nil, nil, dkvsindexer.ErrInvalidRecord
			}
			condition.ExpectedHash = hash
		}
		if !condition.Valid() {
			return nil, nil, dkvsindexer.ErrInvalidRecord
		}
		mutations = append(mutations, dkvsindexer.CASMutation{Record: record, Precondition: condition})
	}
	conditions := make([]dkvsindexer.PathWritePrecondition, 0, len(entry.PathPreconditions))
	for _, stored := range entry.PathPreconditions {
		root, err := chainhash.NewHashFromStr(stored.ExpectedRoot)
		if err != nil {
			return nil, nil, dkvsindexer.ErrInvalidRecord
		}
		conditions = append(conditions, dkvsindexer.PathWritePrecondition{
			Path: stored.Path, ExpectedRoot: *root,
			ExpectedGeneration: stored.ExpectedGeneration,
		})
	}
	return mutations, conditions, nil
}

func (s *dkvsReplicaStore) putBatchOutboxEntry(entry *dkvsBatchOutboxEntry) error {
	if s == nil || s.db == nil || entry == nil {
		return dkvsindexer.ErrInvalidRecord
	}
	if _, _, err := entry.decode(); err != nil {
		return err
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return s.db.Write(dkvsBatchOutboxKey(entry.Namespace, entry.ID), encoded)
}

func (s *dkvsReplicaStore) updateBatchOutboxState(entry *dkvsBatchOutboxEntry,
	state string, err error) error {
	if entry == nil {
		return dkvsindexer.ErrInvalidRecord
	}
	copyEntry := *entry
	copyEntry.State = state
	copyEntry.UpdatedAtMS = uint64(time.Now().UnixMilli())
	if state == dkvsSessionInflight {
		copyEntry.Attempts++
	}
	copyEntry.LastErrorCode = ""
	if err != nil {
		copyEntry.LastErrorCode = string(dkvsindexer.ErrorCodeOf(err))
	}
	if writeErr := s.putBatchOutboxEntry(&copyEntry); writeErr != nil {
		return writeErr
	}
	*entry = copyEntry
	return nil
}

func (s *dkvsReplicaStore) loadBatchOutbox(namespace string) ([]*dkvsBatchOutboxEntry, error) {
	if s == nil || s.db == nil || strings.TrimSpace(namespace) == "" {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	prefix := dkvsBatchOutboxNamespacePrefix(namespace)
	entries := make([]*dkvsBatchOutboxEntry, 0)
	err := s.db.BatchRead(prefix, false, func(_, value []byte) error {
		var entry dkvsBatchOutboxEntry
		if err := json.Unmarshal(value, &entry); err != nil {
			return dkvsindexer.ErrInvalidRecord
		}
		if _, _, err := entry.decode(); err != nil {
			return err
		}
		entries = append(entries, &entry)
		return nil
	})
	sort.Slice(entries, func(a, b int) bool {
		if entries[a].CreatedAtMS == entries[b].CreatedAtMS {
			return entries[a].ID < entries[b].ID
		}
		return entries[a].CreatedAtMS < entries[b].CreatedAtMS
	})
	return entries, err
}

func mutationPaths(mutations []dkvsindexer.CASMutation) ([]string, error) {
	unique := make(map[string]struct{})
	for _, mutation := range mutations {
		if mutation.Record == nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		path, err := dkvsindexer.CollectionPathForKey(mutation.Record.Key)
		if err != nil {
			return nil, err
		}
		unique[path] = struct{}{}
	}
	paths := make([]string, 0, len(unique))
	for path := range unique {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func (s *dkvsReplicaStore) queueBatchOutbox(entry *dkvsBatchOutboxEntry) error {
	if s == nil || s.db == nil || entry == nil {
		return dkvsindexer.ErrInvalidRecord
	}
	mutations, _, err := entry.decode()
	if err != nil {
		return err
	}
	paths, err := mutationPaths(mutations)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	batch := s.db.NewWriteBatch()
	defer batch.Close()
	if err := batch.Put(dkvsBatchOutboxKey(entry.Namespace, entry.ID), encoded); err != nil {
		return err
	}
	for _, path := range paths {
		scope := dkvsReplicaScope(entry.Namespace, []dkvsindexer.Subscription{{
			Type: dkvsindexer.SubscriptionPrefix, Target: path,
		}})
		state, stateErr := s.loadPathState(scope)
		if stateErr != nil {
			if !errors.Is(stateErr, indexer.ErrKeyNotFound) {
				return stateErr
			}
			state = &dkvsPathReplicaState{Path: path}
		}
		state.SessionState = dkvsSessionPrepared
		state.LastErrorCode = ""
		if err := putPathStateBatch(batch, scope, state); err != nil {
			return err
		}
	}
	return batch.Flush()
}

func resultRecordsByPath(result *dkvsindexer.WriteResult) (map[string][]*swire.DKVSRecord, error) {
	if result == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	groups := make(map[string][]*swire.DKVSRecord)
	for _, record := range result.Records {
		if record == nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		path, err := dkvsindexer.CollectionPathForKey(record.Key)
		if err != nil {
			return nil, err
		}
		groups[path] = append(groups[path], record)
	}
	return groups, nil
}

func (s *dkvsReplicaStore) applyWriteResultAndAck(entry *dkvsBatchOutboxEntry,
	result *dkvsindexer.WriteResult) error {
	if s == nil || s.db == nil || entry == nil || result == nil {
		return dkvsindexer.ErrInvalidRecord
	}
	mutations, _, err := entry.decode()
	if err != nil {
		return err
	}
	if len(result.Records) != len(mutations) || len(result.Hashes) != len(mutations) ||
		(result.Applied != 0 && result.Applied != len(mutations)) {
		return dkvsindexer.ErrInvalidRecord
	}
	for index, mutation := range mutations {
		if result.Records[index] == nil ||
			dkvsindexer.RecordHash(result.Records[index]) != dkvsindexer.RecordHash(mutation.Record) ||
			result.Hashes[index] != dkvsindexer.RecordHash(mutation.Record).String() {
			return dkvsindexer.ErrInvalidRecord
		}
	}
	groups, err := resultRecordsByPath(result)
	if err != nil {
		return err
	}
	batch := s.db.NewWriteBatch()
	defer batch.Close()
	for path, changed := range groups {
		scope := dkvsReplicaScope(entry.Namespace, []dkvsindexer.Subscription{{
			Type: dkvsindexer.SubscriptionPrefix, Target: path,
		}})
		byKey, err := s.loadConfirmedByKey(scope)
		if err != nil {
			return err
		}
		hasLocalOnly := false
		for _, record := range changed {
			if dkvsWalletRecordIsFreeLocal(record) {
				hasLocalOnly = true
			}
			if dkvsindexer.IsTombstone(record.Flags) {
				delete(byKey, record.Key)
			} else {
				byKey[record.Key] = record
			}
		}
		records := make([]*swire.DKVSRecord, 0, len(byKey))
		for _, record := range byKey {
			records = append(records, record)
		}
		sort.Slice(records, func(a, b int) bool { return records[a].Key < records[b].Key })
		verify := dkvsindexer.RecordVerificationOptions{Now: result.ServerTimeMS}
		meta := result.PathMeta[path]
		if meta != nil {
			verify.Height = meta.ViewHeight
		}
		if err := s.stageConfirmedReplacement(batch, scope, path, records, verify); err != nil {
			return err
		}
		state, stateErr := s.loadPathState(scope)
		if stateErr != nil {
			if !errors.Is(stateErr, indexer.ErrKeyNotFound) {
				return stateErr
			}
			state = &dkvsPathReplicaState{Path: path}
		}
		if meta != nil {
			if meta.Path != path {
				return dkvsindexer.ErrInvalidRecord
			}
			if err := putReplicaBaselineBatch(batch, scope, meta); err != nil {
				return err
			}
			state.PathMeta = meta
		}
		state.ServerTimeMS = result.ServerTimeMS
		state.HasLocalOnly = state.HasLocalOnly || hasLocalOnly
		if hasLocalOnly {
			if result.EndpointID == "" {
				return dkvsindexer.ErrStaleEndpoint
			}
			state.EndpointID = result.EndpointID
		}
		state.SessionState = dkvsSessionConfirmed
		state.LastErrorCode = ""
		if err := putPathStateBatch(batch, scope, state); err != nil {
			return err
		}
	}
	if err := batch.Delete(dkvsBatchOutboxKey(entry.Namespace, entry.ID)); err != nil {
		return err
	}
	return batch.Flush()
}

// applyWriteResult is retained for callers that update one path without a v1
// batch outbox. New manager writes use applyWriteResultAndAck.
func (s *dkvsReplicaStore) applyWriteResult(scope, path string,
	result *dkvsindexer.WriteResult) error {
	if s == nil || s.db == nil || scope == "" || path == "" || result == nil {
		return dkvsindexer.ErrInvalidRecord
	}
	meta := result.PathMeta[path]
	if meta == nil || meta.Path != path {
		return dkvsindexer.ErrInvalidRecord
	}
	byKey, err := s.loadConfirmedByKey(scope)
	if err != nil {
		return err
	}
	for _, record := range result.Records {
		if record == nil {
			return dkvsindexer.ErrInvalidRecord
		}
		recordPath, pathErr := dkvsindexer.CollectionPathForKey(record.Key)
		if pathErr != nil || recordPath != path {
			continue
		}
		if dkvsindexer.IsTombstone(record.Flags) {
			delete(byKey, record.Key)
		} else {
			byKey[record.Key] = record
		}
	}
	records := make([]*swire.DKVSRecord, 0, len(byKey))
	for _, record := range byKey {
		records = append(records, record)
	}
	sort.Slice(records, func(a, b int) bool { return records[a].Key < records[b].Key })
	filters := []dkvsindexer.Subscription{{Type: dkvsindexer.SubscriptionPrefix, Target: path}}
	root := meta.StateRoot
	return s.applyConfirmed(scope, filters, records, root.String(), root, meta.Generation)
}

func (s *dkvsReplicaStore) hasPendingOutbox(scope string) (bool, error) {
	pending, err := s.loadOutbox(scope)
	if err != nil {
		return false, err
	}
	return len(pending) != 0, nil
}

func (s *dkvsReplicaStore) hasPendingBatchOutbox(namespace string) (bool, error) {
	entries, err := s.loadBatchOutbox(namespace)
	if err != nil {
		return false, err
	}
	return len(entries) != 0, nil
}

func (s *dkvsReplicaStore) markOutboxFailure(entry *dkvsBatchOutboxEntry, err error) error {
	state := dkvsSessionError
	if errors.Is(err, dkvsindexer.ErrWriteConflict) ||
		errors.Is(err, dkvsindexer.ErrStaleGeneration) ||
		errors.Is(err, dkvsindexer.ErrPathDiverged) {
		state = dkvsSessionConflict
	}
	return s.updateBatchOutboxState(entry, state, err)
}

func verifyOutboxEntryIdentity(entry *dkvsBatchOutboxEntry) error {
	if entry == nil {
		return dkvsindexer.ErrInvalidRecord
	}
	want := outboxEntryID(entry.Mutations, entry.PathPreconditions, entry.EndpointID)
	if entry.ID != want {
		return fmt.Errorf("DKVS outbox identity mismatch: %w", dkvsindexer.ErrInvalidRecord)
	}
	return nil
}
