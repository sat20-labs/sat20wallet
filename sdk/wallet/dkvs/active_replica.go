package dkvs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const MaxActiveReplicaBytes = 64 * 1024 * 1024

var activeMetaPrefix = []byte("dkvs:active-meta:")
var activeAckPrefix = []byte("dkvs:active-ack:")

// Fixed stripes avoid an unbounded global map of account/database locks.
// Manager also serializes transport commits; the stripe protects the public
// store API against an ACK racing a complete current-state installation.
var activeReplicaLocks [64]sync.Mutex

func lockActiveReplica(namespace string) func() {
	h := sha256.Sum256([]byte(namespace))
	lock := &activeReplicaLocks[int(h[0])%len(activeReplicaLocks)]
	lock.Lock()
	return lock.Unlock
}

func activeMetaKey(namespace string, scope dkvsindexer.ActiveScope) []byte {
	encoded, _ := json.Marshal(scope)
	hash := sha256.Sum256(encoded)
	return append(dkvsNamespacedPrefix(activeMetaPrefix, namespace), hex.EncodeToString(hash[:])...)
}

func activeAckKey(namespace, prefix string) []byte {
	return append(dkvsNamespacedPrefix(activeAckPrefix, namespace), prefix...)
}

type activeAckMark struct {
	EndpointID string `json:"endpoint_id"`
	Generation uint64 `json:"generation"`
}

// AckGeneration is a verified request-completion position, not a receive cursor
// or a write grant. A different source's mark supplies no authorization here;
// endpoint reset and baseline validation keep their existing responsibilities.
func (s *ReplicaStore) AckGeneration(namespace, prefix, endpointID string) (uint64, error) {
	if s == nil || s.db == nil || strings.TrimSpace(namespace) == "" || strings.TrimSpace(endpointID) == "" {
		return 0, ErrReplicaNotReady
	}
	scope, err := dkvsindexer.NormalizeActiveScope(dkvsindexer.ActiveScope{Prefix: prefix})
	if err != nil {
		return 0, err
	}
	encoded, err := s.db.Read(activeAckKey(namespace, scope.Prefix))
	if errors.Is(err, indexercommon.ErrKeyNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	mark, decodeErr := decodeActiveAckMark(encoded)
	if decodeErr != nil || mark.EndpointID == "" {
		return 0, dkvsindexer.ErrInvalidRecord
	}
	if mark.EndpointID != endpointID {
		return 0, nil
	}
	return mark.Generation, nil
}

func (s *ReplicaStore) LoadActiveMeta(namespace string, scope dkvsindexer.ActiveScope) (*dkvsindexer.ActiveMeta, error) {
	if s == nil || s.db == nil || strings.TrimSpace(namespace) == "" {
		return nil, ErrReplicaNotReady
	}
	scope, err := dkvsindexer.NormalizeActiveScope(scope)
	if err != nil {
		return nil, err
	}
	encoded, err := s.db.Read(activeMetaKey(namespace, scope))
	if err != nil {
		return nil, err
	}
	meta, decodeErr := decodeActiveMeta(encoded)
	if decodeErr != nil || meta.EndpointID == "" || !dkvsindexer.SameActiveScope(scope, meta.Scope) {
		return nil, dkvsindexer.ErrInvalidSnapshot
	}
	return meta, nil
}

func (s *ReplicaStore) activeScopeRecords(namespace string, scope dkvsindexer.ActiveScope) ([]*swire.DKVSRecord, error) {
	base := dkvsSubscriptionRecordKey(namespace, scope.Prefix)
	var records []*swire.DKVSRecord
	total := 0
	err := s.db.BatchRead(base, false, func(_, encoded []byte) error {
		record, err := dkvsindexer.UnmarshalRecord(encoded)
		if err != nil {
			return err
		}
		if !dkvsindexer.ActiveScopeMatches(scope, record.Key) {
			return nil
		}
		size := dkvsindexer.RecordSize(record)
		if size > MaxActiveReplicaBytes-total {
			return dkvsindexer.ErrBatchTooLarge
		}
		total += size
		records = append(records, record)
		return nil
	})
	sort.Slice(records, func(a, b int) bool { return records[a].Key < records[b].Key })
	return records, err
}

// ActiveBaseline fingerprints the CONFIRMED local scope, its source cursor,
// and the last source ACK. Outbox and business-local edits are not replica
// data and are deliberately neither included nor removed by reconciliation.
func (s *ReplicaStore) ActiveBaseline(namespace string, scope dkvsindexer.ActiveScope) (chainhash.Hash, error) {
	if s == nil || s.db == nil {
		return chainhash.Hash{}, ErrReplicaNotReady
	}
	scope, err := dkvsindexer.NormalizeActiveScope(scope)
	if err != nil {
		return chainhash.Hash{}, err
	}
	records, err := s.activeScopeRecords(namespace, scope)
	if err != nil {
		return chainhash.Hash{}, err
	}
	root, err := dkvsindexer.ActiveRecordsRoot(records)
	if err != nil {
		return root, err
	}
	payload := append([]byte(nil), root[:]...)
	for _, key := range [][]byte{activeMetaKey(namespace, scope), activeAckKey(namespace, scope.Prefix)} {
		encoded, err := s.db.Read(key)
		if err != nil && !errors.Is(err, indexercommon.ErrKeyNotFound) {
			return chainhash.Hash{}, err
		}
		hash := sha256.Sum256(encoded)
		payload = append(payload, hash[:]...)
	}
	return chainhash.DoubleHashH(payload), nil
}

func validateActiveRecords(meta dkvsindexer.ActiveMeta, records []*swire.DKVSRecord) error {
	scope, err := dkvsindexer.NormalizeActiveScope(meta.Scope)
	if err != nil || !dkvsindexer.SameActiveScope(scope, meta.Scope) || meta.EndpointID == "" {
		return dkvsindexer.ErrInvalidSnapshot
	}
	seen, total := make(map[string]struct{}, len(records)), 0
	for _, record := range records {
		if record == nil || dkvsindexer.IsTombstone(record.Flags) || !dkvsindexer.ActiveScopeMatches(scope, record.Key) {
			return dkvsindexer.ErrInvalidSnapshot
		}
		if _, exists := seen[record.Key]; exists {
			return dkvsindexer.ErrInvalidSnapshot
		}
		seen[record.Key] = struct{}{}
		size := dkvsindexer.RecordSize(record)
		if size > MaxActiveReplicaBytes-total {
			return dkvsindexer.ErrBatchTooLarge
		}
		total += size
		if dkvsindexer.IsExpired(record, meta.ViewHeight) {
			return dkvsindexer.ErrInvalidSnapshot
		}
		if err := dkvsindexer.VerifyRecordForClient(record, dkvsindexer.RecordVerificationOptions{ExpectedKey: record.Key, Height: meta.ViewHeight}); err != nil {
			return err
		}
		if scope.Network && RecordIsFreeLocal(record) {
			return dkvsindexer.ErrInvalidSnapshot
		}
	}
	return nil
}

func (s *ReplicaStore) checkActiveSource(namespace string, meta dkvsindexer.ActiveMeta) error {
	previous, err := s.LoadActiveMeta(namespace, meta.Scope)
	if err != nil && !errors.Is(err, indexercommon.ErrKeyNotFound) {
		return err
	}
	if previous != nil {
		if previous.EndpointID != meta.EndpointID {
			return dkvsindexer.ErrEndpointMismatch
		}
		if meta.Generation < previous.Generation || meta.ViewHeight < previous.ViewHeight {
			return dkvsindexer.ErrStaleEndpoint
		}
		// A committed source position describes exactly one current root at a
		// given height. Full refresh is not permission to overwrite it with a
		// contradictory snapshot. A later height can legitimately expire data.
		if meta.Generation == previous.Generation && meta.ViewHeight == previous.ViewHeight && meta.Root != previous.Root {
			return dkvsindexer.ErrPathDiverged
		}
	}
	encoded, err := s.db.Read(activeAckKey(namespace, meta.Scope.Prefix))
	if errors.Is(err, indexercommon.ErrKeyNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	ack, decodeErr := decodeActiveAckMark(encoded)
	if decodeErr != nil {
		return dkvsindexer.ErrInvalidRecord
	}
	if ack.EndpointID != meta.EndpointID {
		return dkvsindexer.ErrEndpointMismatch
	}
	if meta.Generation < ack.Generation {
		return dkvsindexer.ErrStaleEndpoint
	}
	return nil
}

// NoteActiveAckBatch stores one high watermark per prefix, not a request log.
// It never advances a completed sync cursor.
func (s *ReplicaStore) NoteActiveAckBatch(batch batchWriter, namespace string, result *dkvsindexer.WriteResult) error {
	if result == nil || result.EndpointID == "" {
		return dkvsindexer.ErrInvalidRecord
	}
	for _, prefix := range result.PrefixStates {
		mark := activeAckMark{EndpointID: result.EndpointID, Generation: prefix.Generation}
		encoded, err := s.db.Read(activeAckKey(namespace, prefix.Prefix))
		if err != nil && !errors.Is(err, indexercommon.ErrKeyNotFound) {
			return err
		}
		if err == nil {
			old, decodeErr := decodeActiveAckMark(encoded)
			if decodeErr != nil || old.EndpointID != mark.EndpointID {
				return dkvsindexer.ErrEndpointMismatch
			}
			if old.Generation >= mark.Generation {
				continue
			}
		}
		encoded, err = encodeActiveAckMark(mark)
		if err != nil {
			return err
		}
		if err := batch.Put(activeAckKey(namespace, prefix.Prefix), encoded); err != nil {
			return err
		}
	}
	return nil
}

// InstallActiveState validates a complete source view, or merges a complete
// generation candidate set and checks its resulting root. A delta missing a
// physical deletion returns ErrPathDiverged WITHOUT committing any data/cursor;
// the caller must obtain a full current view from this same source.
// ActiveInstall is an uncommitted, fully collected source candidate.
type ActiveInstall struct {
	Baseline chainhash.Hash
	Meta     dkvsindexer.ActiveMeta
	Records  []*swire.DKVSRecord
	Full     bool
}

func (s *ReplicaStore) InstallActiveState(namespace string, baseline chainhash.Hash,
	meta dkvsindexer.ActiveMeta, records []*swire.DKVSRecord, full bool) ([]string, error) {
	return s.InstallActiveStates(namespace, []ActiveInstall{{baseline, meta, records, full}})
}

// InstallActiveStates publishes every collection in one transaction. No Put or
// Delete may become visible before Flush, even at bulk-writer size thresholds.
func (s *ReplicaStore) InstallActiveStates(namespace string, installs []ActiveInstall) ([]string, error) {
	if s == nil || s.db == nil || strings.TrimSpace(namespace) == "" || len(installs) == 0 {
		return nil, ErrReplicaNotReady
	}
	atomic, ok := s.db.(indexercommon.AtomicWriteBatchDB)
	if !ok {
		return nil, errors.New("DKVS requires atomic write transactions")
	}
	unlock := lockActiveReplica(namespace)
	defer unlock()
	batch := atomic.NewAtomicWriteBatch()
	if batch == nil {
		return nil, ErrReplicaNotReady
	}
	defer batch.Close()
	changed := make(map[string]struct{})
	var scopes []dkvsindexer.ActiveScope
	var height uint64
	totalBytes := 0
	wrote := false
	var endpoint string
	for _, candidate := range installs {
		if endpoint != "" && candidate.Meta.EndpointID != endpoint {
			return nil, dkvsindexer.ErrEndpointMismatch
		}
		endpoint = candidate.Meta.EndpointID
		scope, err := dkvsindexer.NormalizeActiveScope(candidate.Meta.Scope)
		if err != nil {
			return nil, err
		}
		for _, previous := range scopes {
			if previous.Prefix != scope.Prefix {
				continue
			}
			if len(previous.Keys) == 0 || len(scope.Keys) == 0 {
				return nil, dkvsindexer.ErrInvalidSnapshot
			}
			for _, key := range scope.Keys {
				if dkvsindexer.ActiveScopeMatches(previous, key) {
					return nil, dkvsindexer.ErrInvalidSnapshot
				}
			}
		}
		scopes = append(scopes, scope)
		keys, err := s.stageActiveState(batch, namespace, candidate.Baseline, candidate.Meta, candidate.Records, candidate.Full, &totalBytes, &wrote)
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			changed[key] = struct{}{}
		}
		if candidate.Meta.ViewHeight > height {
			height = candidate.Meta.ViewHeight
		}
	}
	state, err := s.LoadSubscriptionState(namespace)
	if err == nil {
		if state.EndpointID != "" && state.EndpointID != endpoint {
			return nil, dkvsindexer.ErrEndpointMismatch
		}
		if wrote || state.EndpointID != endpoint || height > state.ViewHeight {
			state.EndpointID = endpoint
			if height > state.ViewHeight {
				state.ViewHeight = height
			}
			state.LastSyncAtMS = uint64(time.Now().UnixMilli())
			if err := putSubscriptionStateBatch(batch, namespace, state); err != nil {
				return nil, err
			}
			wrote = true
		}
	} else if !errors.Is(err, indexercommon.ErrKeyNotFound) {
		return nil, err
	}
	if wrote {
		if err := batch.Flush(); err != nil {
			return nil, err
		}
	}
	keys := make([]string, 0, len(changed))
	for key := range changed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func (s *ReplicaStore) stageActiveState(batch indexercommon.WriteBatch, namespace string, baseline chainhash.Hash,
	meta dkvsindexer.ActiveMeta, records []*swire.DKVSRecord, full bool, totalBytes *int, wrote *bool) ([]string, error) {
	if err := validateActiveRecords(meta, records); err != nil {
		return nil, err
	}
	currentBaseline, err := s.ActiveBaseline(namespace, meta.Scope)
	if err != nil {
		return nil, err
	}
	if currentBaseline != baseline {
		return nil, dkvsindexer.ErrConcurrentUpdate
	}
	if err := s.checkActiveSource(namespace, meta); err != nil {
		return nil, err
	}
	old, err := s.activeScopeRecords(namespace, meta.Scope)
	if err != nil {
		return nil, err
	}
	candidate := make(map[string]*swire.DKVSRecord, len(old)+len(records))
	if !full {
		for _, record := range old {
			if !dkvsindexer.IsExpired(record, meta.ViewHeight) {
				candidate[record.Key] = record
			}
		}
	}
	for _, record := range records {
		candidate[record.Key] = record
	}
	merged, total := make([]*swire.DKVSRecord, 0, len(candidate)), 0
	for _, record := range candidate {
		size := dkvsindexer.RecordSize(record)
		if size > MaxActiveReplicaBytes-total {
			return nil, dkvsindexer.ErrBatchTooLarge
		}
		total += size
		if size > MaxActiveReplicaBytes-*totalBytes {
			return nil, dkvsindexer.ErrBatchTooLarge
		}
		*totalBytes += size
		merged = append(merged, record)
	}
	root, err := dkvsindexer.ActiveRecordsRoot(merged)
	if err != nil {
		return nil, err
	}
	if root != meta.Root {
		return nil, dkvsindexer.ErrPathDiverged
	}
	changed := make(map[string]struct{})
	oldByKey := make(map[string]*swire.DKVSRecord, len(old))
	for _, record := range old {
		oldByKey[record.Key] = record
		if _, exists := candidate[record.Key]; !exists {
			if err := batch.Delete(dkvsSubscriptionRecordKey(namespace, record.Key)); err != nil {
				return nil, err
			}
			*wrote = true
			changed[record.Key] = struct{}{}
		}
	}
	for _, record := range merged {
		previous := oldByKey[record.Key]
		if previous != nil && dkvsindexer.RecordHash(previous) == dkvsindexer.RecordHash(record) {
			continue // A retained collection must not rewrite unchanged values.
		}
		changed[record.Key] = struct{}{}
		encoded, err := dkvsindexer.MarshalRecord(record)
		if err != nil {
			return nil, err
		}
		if err := batch.Put(dkvsSubscriptionRecordKey(namespace, record.Key), encoded); err != nil {
			return nil, err
		}
		*wrote = true
	}
	encoded, err := encodeActiveMeta(meta)
	if err != nil {
		return nil, err
	}
	key := activeMetaKey(namespace, meta.Scope)
	previous, err := s.db.Read(key)
	if err != nil && !errors.Is(err, indexercommon.ErrKeyNotFound) {
		return nil, err
	}
	if !bytes.Equal(previous, encoded) {
		if err := batch.Put(key, encoded); err != nil {
			return nil, err
		}
		*wrote = true
	}
	keys := make([]string, 0, len(changed))
	for key := range changed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

// ValidateActiveCache checks completeness and signatures before granting a new
// session offline reads. Callers retain that grant; ordinary Get never scans.
func (s *ReplicaStore) ValidateActiveCache(namespace string) error {
	unlock := lockActiveReplica(namespace)
	defer unlock()
	state, err := s.LoadSubscriptionState(namespace)
	if err != nil {
		return err
	}
	if state.Status != DKVSSubscriptionReady && state.Status != DKVSSubscriptionOfflineReady {
		return ErrReplicaNotReady
	}
	for _, prefix := range state.Prefixes {
		scope := dkvsindexer.ActiveScope{Prefix: prefix}
		meta, err := s.LoadActiveMeta(namespace, scope)
		if err != nil {
			return err
		}
		if meta.EndpointID != state.EndpointID {
			return dkvsindexer.ErrEndpointMismatch
		}
		records, err := s.activeScopeRecords(namespace, scope)
		if err != nil {
			return err
		}
		if err = validateActiveRecords(*meta, records); err != nil {
			return err
		}
		root, err := dkvsindexer.ActiveRecordsRoot(records)
		if err != nil {
			return err
		}
		if root != meta.Root {
			return dkvsindexer.ErrPathDiverged
		}
	}
	return nil
}
