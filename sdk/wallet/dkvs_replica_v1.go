package wallet

import (
	"bytes"
	"encoding/binary"
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
)

const (
	dkvsSessionIdle      = "idle"
	dkvsSessionPrepared  = "prepared"
	dkvsSessionInflight  = "inflight"
	dkvsSessionConfirmed = "confirmed"
	dkvsSessionConflict  = "conflict"
	dkvsSessionError     = "error"
	dkvsSessionTerminal  = "terminal"
)

var (
	dkvsPathStatePrefix       = []byte("dkvs-path-state:")
	dkvsLegacyPathStatePrefix = []byte("dkvs-path-state-v1:")
	dkvsBatchOutboxPrefix     = []byte("dkvs-batch-outbox:")
)

type dkvsPathReplicaState struct {
	Version      uint32                    `json:"version"`
	Path         string                    `json:"path"`
	PathMeta     *dkvsindexer.PathMeta     `json:"pathmeta,omitempty"`
	DeleteFloors []dkvsindexer.DeleteFloor `json:"delete_floors,omitempty"`
	// LocalDeleteFloors are endpoint-local tombstone sequence floors. They are
	// intentionally kept separate from DeleteFloors because FREE_LOCAL state is
	// excluded from the network path root and must not invalidate it.
	LocalDeleteFloors []dkvsindexer.DeleteFloor `json:"local_delete_floors,omitempty"`
	ServerTimeMS      uint64                    `json:"server_time_ms"`
	EndpointID        string                    `json:"endpoint_id,omitempty"`
	HasLocalOnly      bool                      `json:"has_local_only,omitempty"`
	SessionState      string                    `json:"session_state"`
	LastErrorCode     string                    `json:"last_error_code,omitempty"`
	UpdatedAtMS       uint64                    `json:"updated_at_ms"`
}

func cloneDKVSDeleteFloors(floors []dkvsindexer.DeleteFloor) []dkvsindexer.DeleteFloor {
	cloned := make([]dkvsindexer.DeleteFloor, len(floors))
	for index := range floors {
		cloned[index] = floors[index]
		cloned[index].PubKey = append([]byte(nil), floors[index].PubKey...)
	}
	return cloned
}

type dkvsPersistedMutation struct {
	Record       []byte
	ExpectedHash []byte
	ExpectAbsent bool
}

type dkvsPersistedPathPrecondition struct {
	Path               string
	ExpectedRoot       []byte
	ExpectedGeneration uint64
}

type dkvsBatchOutboxEntry struct {
	Key               string
	Namespace         string
	Mutations         []dkvsPersistedMutation
	PathPreconditions []dkvsPersistedPathPrecondition
	EndpointID        string
	State             string
	Attempts          uint32
	LastErrorCode     string
	LastError         string
	CreatedAtMS       uint64
	UpdatedAtMS       uint64
	OriginDomain      string
	OriginGeneration  uint64
}

type dkvsOutboxOrigin struct {
	Key        string
	Domain     string
	Generation uint64
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
			dkvsindexer.IsExpired(record, snapshot.PathMeta.ViewHeight) {
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

func dkvsLegacyPathStateKey(scope string) []byte {
	key := make([]byte, 0, len(dkvsLegacyPathStatePrefix)+len(scope))
	key = append(key, dkvsLegacyPathStatePrefix...)
	return append(key, scope...)
}

func dkvsBatchOutboxNamespacePrefix(namespace string) []byte {
	namespace = strings.TrimSpace(namespace)
	key := make([]byte, 0, len(dkvsBatchOutboxPrefix)+len(namespace)+1)
	key = append(key, dkvsBatchOutboxPrefix...)
	key = append(key, namespace...)
	return append(key, ':')
}

func dkvsBatchOutboxKey(namespace, outboxKey string) []byte {
	key := dkvsBatchOutboxNamespacePrefix(namespace)
	return append(key, outboxKey...)
}

func (s *dkvsReplicaStore) loadPathState(scope string) (*dkvsPathReplicaState, error) {
	if s == nil || s.db == nil || scope == "" {
		return nil, ErrDKVSPathNotSynced
	}
	encoded, err := s.db.Read(dkvsPathStateKey(scope))
	legacy := false
	if err != nil && errors.Is(err, indexer.ErrKeyNotFound) {
		encoded, err = s.db.Read(dkvsLegacyPathStateKey(scope))
		legacy = err == nil
	}
	if err != nil {
		return nil, err
	}
	var state dkvsPathReplicaState
	if legacy {
		if err := json.Unmarshal(encoded, &state); err != nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
	} else if err := decodeDKVSPathReplicaState(encoded, &state); err != nil {
		return nil, err
	}
	if state.Version != dkvsReplicaStateVersion || state.Path == "" || state.SessionState == "" {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	state.PathMeta = cloneWalletPathMeta(state.PathMeta)
	state.DeleteFloors = cloneDKVSDeleteFloors(state.DeleteFloors)
	state.LocalDeleteFloors = cloneDKVSDeleteFloors(state.LocalDeleteFloors)
	return &state, nil
}

func putPathStateBatch(batch dkvsBatchWriter, scope string, state *dkvsPathReplicaState) error {
	if batch == nil || scope == "" || state == nil || state.Path == "" {
		return dkvsindexer.ErrInvalidRecord
	}
	copyState := *state
	copyState.Version = dkvsReplicaStateVersion
	copyState.PathMeta = cloneWalletPathMeta(state.PathMeta)
	copyState.DeleteFloors = cloneDKVSDeleteFloors(state.DeleteFloors)
	copyState.LocalDeleteFloors = cloneDKVSDeleteFloors(state.LocalDeleteFloors)
	if copyState.SessionState == "" {
		copyState.SessionState = dkvsSessionIdle
	}
	copyState.UpdatedAtMS = uint64(time.Now().UnixMilli())
	encoded, err := encodeDKVSPathReplicaState(&copyState)
	if err != nil {
		return err
	}
	if err := batch.Put(dkvsPathStateKey(scope), encoded); err != nil {
		return err
	}
	return batch.Delete(dkvsLegacyPathStateKey(scope))
}

// validateNetworkReplica compares the materialized network replica with the
// canonical remote PathMeta. Endpoint-local FREE_LOCAL records are excluded.
// The canonical root/count/size/expiry calculation remains owned by the
// SatoshiNet DKVS snapshot validator.
func (s *dkvsReplicaStore) validateNetworkReplica(scope, path string,
	meta *dkvsindexer.PathMeta) error {

	if s == nil || s.db == nil || scope == "" || path == "" || meta == nil {
		return dkvsindexer.ErrInvalidSnapshot
	}
	state, err := s.loadPathState(scope)
	if err != nil {
		return err
	}
	if state.Path != path {
		return dkvsindexer.ErrInvalidSnapshot
	}
	records, err := s.loadConfirmed(scope)
	if err != nil {
		return err
	}
	network := make([]*swire.DKVSRecord, 0, len(records))
	for _, record := range records {
		if record != nil && dkvsindexer.RecordRequiresPathPrecondition(record) {
			network = append(network, record)
		}
	}
	return dkvsindexer.ValidatePathSnapshotForClient(&dkvsindexer.PathSnapshot{
		Path: path, PathMeta: cloneWalletPathMeta(meta), Records: network,
		DeleteFloors: cloneDKVSDeleteFloors(state.DeleteFloors),
	}, dkvsindexer.RecordVerificationOptions{Height: meta.ViewHeight})
}

func deleteFloorForDKVSRecord(record *swire.DKVSRecord, pathGeneration uint64) dkvsindexer.DeleteFloor {
	if record == nil {
		return dkvsindexer.DeleteFloor{}
	}
	return dkvsindexer.DeleteFloor{
		Key: record.Key, FloorSeq: record.Seq, PathGeneration: pathGeneration,
		PubKey: append([]byte(nil), record.PubKey...), EffectiveHash: dkvsindexer.RecordHash(record),
	}
}

func upsertDKVSDeleteFloor(floors []dkvsindexer.DeleteFloor,
	floor dkvsindexer.DeleteFloor) []dkvsindexer.DeleteFloor {
	if floor.Key == "" || floor.FloorSeq == 0 {
		return floors
	}
	for index := range floors {
		if floors[index].Key != floor.Key {
			continue
		}
		if floors[index].FloorSeq <= floor.FloorSeq {
			floors[index] = floor
		}
		return floors
	}
	return append(floors, floor)
}

func removeDKVSDeleteFloor(floors []dkvsindexer.DeleteFloor, key string) []dkvsindexer.DeleteFloor {
	if key == "" {
		return floors
	}
	filtered := floors[:0]
	for _, floor := range floors {
		if floor.Key != key {
			filtered = append(filtered, floor)
		}
	}
	return filtered
}

func maxDKVSDeleteFloorSeq(floors []dkvsindexer.DeleteFloor, key string) uint64 {
	var max uint64
	for _, floor := range floors {
		if floor.Key == key && floor.FloorSeq > max {
			max = floor.FloorSeq
		}
	}
	return max
}

// writeResultDeleteFloors derives the authoritative network path generation
// for tombstones from the same key ordering used by batch-CAS. The API returns
// the projected PathMeta but not the individual delete-floor list.
func writeResultDeleteFloors(path string, changed []*swire.DKVSRecord,
	meta *dkvsindexer.PathMeta) (map[string]dkvsindexer.DeleteFloor, error) {
	if meta == nil {
		return nil, nil
	}
	relayable := make([]*swire.DKVSRecord, 0, len(changed))
	for _, record := range changed {
		if record == nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		recordPath, err := dkvsindexer.CollectionPathForKey(record.Key)
		if err != nil {
			return nil, err
		}
		if recordPath == path && dkvsindexer.RecordRequiresPathPrecondition(record) {
			relayable = append(relayable, record)
		}
	}
	sort.Slice(relayable, func(a, b int) bool { return relayable[a].Key < relayable[b].Key })
	if uint64(len(relayable)) > meta.Generation {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	floors := make(map[string]dkvsindexer.DeleteFloor)
	base := meta.Generation - uint64(len(relayable))
	for index, record := range relayable {
		if dkvsindexer.IsTombstone(record.Flags) {
			floors[record.Key] = deleteFloorForDKVSRecord(record, base+uint64(index)+1)
		}
	}
	return floors, nil
}

func applyWriteResultDeleteFloors(state *dkvsPathReplicaState, path string,
	changed []*swire.DKVSRecord, meta *dkvsindexer.PathMeta) error {
	if state == nil || path == "" {
		return dkvsindexer.ErrInvalidRecord
	}
	networkFloors, err := writeResultDeleteFloors(path, changed, meta)
	if err != nil {
		return err
	}
	for _, record := range changed {
		if record == nil {
			return dkvsindexer.ErrInvalidRecord
		}
		recordPath, pathErr := dkvsindexer.CollectionPathForKey(record.Key)
		if pathErr != nil || recordPath != path {
			continue
		}
		state.DeleteFloors = removeDKVSDeleteFloor(state.DeleteFloors, record.Key)
		state.LocalDeleteFloors = removeDKVSDeleteFloor(state.LocalDeleteFloors, record.Key)
		if !dkvsindexer.IsTombstone(record.Flags) {
			continue
		}
		if dkvsWalletRecordIsFreeLocal(record) {
			state.LocalDeleteFloors = upsertDKVSDeleteFloor(state.LocalDeleteFloors,
				deleteFloorForDKVSRecord(record, 0))
			continue
		}
		floor, ok := networkFloors[record.Key]
		if !ok {
			return dkvsindexer.ErrInvalidRecord
		}
		state.DeleteFloors = upsertDKVSDeleteFloor(state.DeleteFloors, floor)
	}
	return nil
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
	var localDeleteFloors []dkvsindexer.DeleteFloor
	var endpointID string
	var hasLocalOnly bool
	if previous, previousErr := s.loadPathState(scope); previousErr == nil {
		localDeleteFloors = cloneDKVSDeleteFloors(previous.LocalDeleteFloors)
		endpointID = previous.EndpointID
		hasLocalOnly = previous.HasLocalOnly
	}
	if err := putPathStateBatch(batch, scope, &dkvsPathReplicaState{
		Path: snapshot.Path, PathMeta: snapshot.PathMeta,
		DeleteFloors:      snapshot.DeleteFloors,
		LocalDeleteFloors: localDeleteFloors, ServerTimeMS: snapshot.ServerTimeMS,
		EndpointID: endpointID, HasLocalOnly: hasLocalOnly, SessionState: dkvsSessionIdle,
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
		stored.ExpectedHash = append([]byte(nil), mutation.Precondition.ExpectedHash[:]...)
	}
	return stored, nil
}

func persistedPathPrecondition(condition dkvsindexer.PathWritePrecondition) dkvsPersistedPathPrecondition {
	return dkvsPersistedPathPrecondition{
		Path: condition.Path, ExpectedRoot: append([]byte(nil), condition.ExpectedRoot[:]...),
		ExpectedGeneration: condition.ExpectedGeneration,
	}
}

func newDKVSBatchOutboxEntry(namespace string, mutations []dkvsindexer.CASMutation,
	conditions []dkvsindexer.PathWritePrecondition, endpointID string,
	origins ...dkvsOutboxOrigin) (*dkvsBatchOutboxEntry, error) {
	if strings.TrimSpace(namespace) == "" || len(mutations) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	origin := dkvsOutboxOrigin{}
	if len(origins) != 0 {
		origin = origins[0]
	}
	origin.Key = strings.TrimSpace(origin.Key)
	if origin.Key == "" {
		return nil, fmt.Errorf("DKVS outbox key is required: %w", dkvsindexer.ErrInvalidKey)
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
		Key: origin.Key, Namespace: strings.TrimSpace(namespace),
		Mutations: storedMutations, PathPreconditions: storedConditions,
		EndpointID: endpointID, State: dkvsSessionPrepared,
		CreatedAtMS: now, UpdatedAtMS: now,
		OriginDomain:     strings.TrimSpace(origin.Domain),
		OriginGeneration: origin.Generation,
	}
	if err := verifyOutboxEntryKey(entry); err != nil {
		return nil, err
	}
	return entry, nil
}

func (entry *dkvsBatchOutboxEntry) decode() ([]dkvsindexer.CASMutation,
	[]dkvsindexer.PathWritePrecondition, error) {
	if entry == nil || entry.Key == "" ||
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
		if len(stored.ExpectedHash) != 0 {
			if len(stored.ExpectedHash) != chainhash.HashSize {
				return nil, nil, dkvsindexer.ErrInvalidRecord
			}
			hash, err := chainhash.NewHash(stored.ExpectedHash)
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
		if len(stored.ExpectedRoot) != chainhash.HashSize {
			return nil, nil, dkvsindexer.ErrInvalidRecord
		}
		root, err := chainhash.NewHash(stored.ExpectedRoot)
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
	return s.writeBatchOutboxEntry(entry)
}

// writeBatchOutboxEntry persists lifecycle diagnostics without rebuilding or
// re-signing the original mutation.  Callers which create new entries must use
// putBatchOutboxEntry so malformed records cannot enter the outbox.
func (s *dkvsReplicaStore) writeBatchOutboxEntry(entry *dkvsBatchOutboxEntry) error {
	if s == nil || s.db == nil || entry == nil || entry.Namespace == "" || entry.Key == "" {
		return dkvsindexer.ErrInvalidRecord
	}
	encoded, err := encodeDKVSBatchOutboxEntry(entry)
	if err != nil {
		return err
	}
	return s.db.Write(dkvsBatchOutboxKey(entry.Namespace, entry.Key), encoded)
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
	copyEntry.LastError = ""
	if err != nil {
		copyEntry.LastErrorCode = string(dkvsindexer.ErrorCodeOf(err))
		copyEntry.LastError = err.Error()
	}
	if writeErr := s.writeBatchOutboxEntry(&copyEntry); writeErr != nil {
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
	err := s.db.BatchRead(prefix, false, func(key, value []byte) error {
		entry, err := decodeDKVSBatchOutboxEntry(value)
		if err != nil {
			return fmt.Errorf("decode DKVS outbox at %q: %w", string(key), err)
		}
		if entry.Namespace != strings.TrimSpace(namespace) ||
			!bytes.Equal(key, dkvsBatchOutboxKey(namespace, entry.Key)) {
			return fmt.Errorf("DKVS outbox storage key mismatch at %q: %w",
				string(key), dkvsindexer.ErrInvalidRecord)
		}
		entries = append(entries, entry)
		return nil
	})
	sort.Slice(entries, func(a, b int) bool {
		if entries[a].CreatedAtMS == entries[b].CreatedAtMS {
			return entries[a].Key < entries[b].Key
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
	if encodedExisting, readErr := s.db.Read(dkvsBatchOutboxKey(entry.Namespace, entry.Key)); readErr == nil {
		existing, decodeErr := decodeDKVSBatchOutboxEntry(encodedExisting)
		if decodeErr != nil {
			return fmt.Errorf("decode existing DKVS outbox %s: %w", entry.Key, decodeErr)
		}
		if existing.Namespace != entry.Namespace || existing.Key != entry.Key {
			return dkvsindexer.ErrInvalidRecord
		}
		if existing.State == dkvsSessionTerminal {
			return &dkvsTerminalOutboxError{
				Key: existing.Key, Code: existing.LastErrorCode, Message: existing.LastError,
			}
		}
		if existing.State == dkvsSessionConflict {
			return fmt.Errorf("DKVS outbox %s requires reconciliation: %w",
				existing.Key, dkvsindexer.ErrWriteConflict)
		}
		if !sameDKVSOutboxRequest(existing, entry) {
			return fmt.Errorf("DKVS outbox key %s is already occupied: %w",
				entry.Key, dkvsindexer.ErrWriteConflict)
		}
		*entry = *existing
	} else if !errors.Is(readErr, indexer.ErrKeyNotFound) {
		return readErr
	}
	encoded, err := encodeDKVSBatchOutboxEntry(entry)
	if err != nil {
		return err
	}
	batch := s.db.NewWriteBatch()
	defer batch.Close()
	if err := batch.Put(dkvsBatchOutboxKey(entry.Namespace, entry.Key), encoded); err != nil {
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
		return fmt.Errorf("decode DKVS batch outbox: %w", err)
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
		return fmt.Errorf("group DKVS batch result: %w", err)
	}
	batch := s.db.NewWriteBatch()
	defer batch.Close()
	for path, changed := range groups {
		scope := dkvsReplicaScope(entry.Namespace, []dkvsindexer.Subscription{{
			Type: dkvsindexer.SubscriptionPrefix, Target: path,
		}})
		byKey, err := s.loadConfirmedByKey(scope)
		if err != nil {
			if !errors.Is(err, indexer.ErrKeyNotFound) {
				return fmt.Errorf("load confirmed DKVS path %s: %w", path, err)
			}
			byKey = make(map[string]*swire.DKVSRecord)
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
		verify := dkvsindexer.RecordVerificationOptions{}
		meta := result.PathMeta[path]
		if meta != nil {
			verify.Height = meta.ViewHeight
		}
		if err := s.stageConfirmedReplacement(batch, scope, path, records, verify); err != nil {
			return fmt.Errorf("stage confirmed DKVS path %s: %w", path, err)
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
				return fmt.Errorf("DKVS path metadata mismatch got=%s want=%s: %w", meta.Path, path, dkvsindexer.ErrInvalidRecord)
			}
			if err := putReplicaBaselineBatch(batch, scope, meta); err != nil {
				return err
			}
			state.PathMeta = meta
		}
		if err := applyWriteResultDeleteFloors(state, path, changed, meta); err != nil {
			return err
		}
		state.ServerTimeMS = result.ServerTimeMS
		state.HasLocalOnly = state.HasLocalOnly || hasLocalOnly
		if hasLocalOnly {
			if result.EndpointID == "" {
				return fmt.Errorf("local-only DKVS batch has no endpoint identity: %w", dkvsindexer.ErrStaleEndpoint)
			}
			state.EndpointID = result.EndpointID
		}
		state.SessionState = dkvsSessionConfirmed
		state.LastErrorCode = ""
		if err := putPathStateBatch(batch, scope, state); err != nil {
			return err
		}
	}
	if err := batch.Delete(dkvsBatchOutboxKey(entry.Namespace, entry.Key)); err != nil {
		return err
	}
	return batch.Flush()
}

// applyWriteResult is retained for callers that update one path without the
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
	state, stateErr := s.loadPathState(scope)
	if stateErr != nil {
		if !errors.Is(stateErr, indexer.ErrKeyNotFound) {
			return stateErr
		}
		state = &dkvsPathReplicaState{Path: path}
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
	if err := applyWriteResultDeleteFloors(state, path, result.Records, meta); err != nil {
		return err
	}
	records := make([]*swire.DKVSRecord, 0, len(byKey))
	for _, record := range byKey {
		records = append(records, record)
	}
	sort.Slice(records, func(a, b int) bool { return records[a].Key < records[b].Key })
	filters := []dkvsindexer.Subscription{{Type: dkvsindexer.SubscriptionPrefix, Target: path}}
	root := meta.StateRoot
	if err := s.applyConfirmed(scope, filters, records, root.String(), root, meta.Generation); err != nil {
		return err
	}
	state.Path = path
	state.PathMeta = meta
	state.SessionState = dkvsSessionConfirmed
	batch := s.db.NewWriteBatch()
	defer batch.Close()
	if err := putPathStateBatch(batch, scope, state); err != nil {
		return err
	}
	return batch.Flush()
}

func (s *dkvsReplicaStore) hasPendingBatchOutbox(namespace string) (bool, error) {
	entries, err := s.loadBatchOutbox(namespace)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.State != dkvsSessionTerminal && entry.State != dkvsSessionConflict {
			return true, nil
		}
	}
	return false, nil
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

func (s *dkvsReplicaStore) markOutboxTerminal(entry *dkvsBatchOutboxEntry, err error) error {
	return s.updateBatchOutboxState(entry, dkvsSessionTerminal, err)
}

func verifyOutboxEntryKey(entry *dkvsBatchOutboxEntry) error {
	if entry == nil || strings.TrimSpace(entry.Key) == "" || entry.Key != strings.TrimSpace(entry.Key) {
		return dkvsindexer.ErrInvalidRecord
	}
	if _, err := dkvsindexer.ParseKey(entry.Key); err != nil {
		return fmt.Errorf("invalid DKVS outbox business key %s: %w", entry.Key, err)
	}
	mutations, _, err := entry.decode()
	if err != nil || len(mutations) == 0 {
		return dkvsindexer.ErrInvalidRecord
	}
	return verifyOutboxMutationKeys(mutations)
}

func verifyOutboxMutationKeys(mutations []dkvsindexer.CASMutation) error {
	if len(mutations) == 0 {
		return dkvsindexer.ErrInvalidRecord
	}
	seen := make(map[string]struct{}, len(mutations))
	for _, mutation := range mutations {
		if mutation.Record == nil {
			return dkvsindexer.ErrInvalidRecord
		}
		if _, duplicate := seen[mutation.Record.Key]; duplicate {
			return fmt.Errorf("duplicate DKVS outbox mutation key %s: %w",
				mutation.Record.Key, dkvsindexer.ErrInvalidRecord)
		}
		seen[mutation.Record.Key] = struct{}{}
	}
	return nil
}

func sameDKVSOutboxRequest(left, right *dkvsBatchOutboxEntry) bool {
	if left == nil || right == nil || left.Key != right.Key || left.Namespace != right.Namespace ||
		left.EndpointID != right.EndpointID || left.OriginDomain != right.OriginDomain ||
		left.OriginGeneration != right.OriginGeneration ||
		len(left.Mutations) != len(right.Mutations) ||
		len(left.PathPreconditions) != len(right.PathPreconditions) {
		return false
	}
	for index := range left.Mutations {
		if left.Mutations[index].ExpectAbsent != right.Mutations[index].ExpectAbsent ||
			!bytes.Equal(left.Mutations[index].Record, right.Mutations[index].Record) ||
			!bytes.Equal(left.Mutations[index].ExpectedHash, right.Mutations[index].ExpectedHash) {
			return false
		}
	}
	for index := range left.PathPreconditions {
		if left.PathPreconditions[index].Path != right.PathPreconditions[index].Path ||
			left.PathPreconditions[index].ExpectedGeneration != right.PathPreconditions[index].ExpectedGeneration ||
			!bytes.Equal(left.PathPreconditions[index].ExpectedRoot,
				right.PathPreconditions[index].ExpectedRoot) {
			return false
		}
	}
	return true
}
