package wallet

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const rgb11MemoryDKVSEndpointID = "rgb11-memory-dkvs"

var rgb11PathLeafDomain = []byte("dkvs-path-leaf-v1")

func (h *rgb11MemoryDKVSHTTP) DKVSClientConfigV1() (*dkvsindexer.ClientConfig, error) {
	if h == nil {
		return nil, ErrDKVSPathNotSynced
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return &dkvsindexer.ClientConfig{
		FreeLocal:         h.freeLocal,
		Blob:              dkvsindexer.DefaultBlobPolicy(),
		MaxBatchMutations: dkvsindexer.MaxBatchCASMutations,
		MaxBatchBytes:     dkvsindexer.MaxBatchCASTotalSize,
		EndpointID:        rgb11MemoryDKVSEndpointID,
	}, nil
}

func rgb11DKVSV1Response(data interface{}) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"code": 0,
		"msg":  "ok",
		"data": data,
	})
}

func rgb11DKVSV1Error(err error) ([]byte, error) {
	if err == nil {
		err = dkvsindexer.ErrInvalidRecord
	}
	return json.Marshal(map[string]interface{}{
		"code":       -1,
		"msg":        err.Error(),
		"error_code": string(dkvsindexer.ErrorCodeOf(err)),
	})
}

func rgb11PathLeaf(record *swire.DKVSRecord) chainhash.Hash {
	if record == nil {
		return chainhash.Hash{}
	}
	effectiveHash := dkvsindexer.RecordHash(record)
	hash := sha256.New()
	_, _ = hash.Write(rgb11PathLeafDomain)
	var scratch [8]byte
	binary.BigEndian.PutUint64(scratch[:], uint64(len(record.Key)))
	_, _ = hash.Write(scratch[:])
	_, _ = hash.Write([]byte(record.Key))
	_, _ = hash.Write(effectiveHash[:])
	var result chainhash.Hash
	copy(result[:], hash.Sum(nil))
	return result
}

func xorRGB11PathRoot(root *chainhash.Hash, record *swire.DKVSRecord) {
	leaf := rgb11PathLeaf(record)
	for index := range root {
		root[index] ^= leaf[index]
	}
}

func (h *rgb11MemoryDKVSHTTP) pathMetaV1Locked(path string, now uint64) *dkvsindexer.PathMeta {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	meta := &dkvsindexer.PathMeta{
		Version:    3,
		Path:       path,
		Generation: h.generations[path],
		ViewHeight: 1,
	}
	for _, record := range h.records {
		if record == nil || !dkvsindexer.RecordRequiresPathPrecondition(record) {
			continue
		}
		recordPath, err := dkvsindexer.CollectionPathForKey(record.Key)
		if err != nil || recordPath != path || dkvsindexer.IsTombstone(record.Flags) ||
			dkvsindexer.IsExpired(record, meta.ViewHeight) {
			continue
		}
		meta.ActiveRecords++
		meta.ActiveTotalSize += uint64(dkvsindexer.RecordSize(record))
		xorRGB11PathRoot(&meta.StateRoot, record)
		if expiry := dkvsindexer.RecordExpiryHeight(record); expiry != 0 &&
			(meta.MinExpiryHeight == 0 || expiry < meta.MinExpiryHeight) {
			meta.MinExpiryHeight = expiry
		}
	}
	meta.ActiveRoot = meta.StateRoot
	return meta
}

func (h *rgb11MemoryDKVSHTTP) pathSnapshotV1Locked(path string, now uint64) *dkvsindexer.PathSnapshot {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	records := make([]*swire.DKVSRecord, 0)
	for _, record := range h.records {
		if record == nil || !dkvsindexer.RecordRequiresPathPrecondition(record) {
			continue
		}
		recordPath, err := dkvsindexer.CollectionPathForKey(record.Key)
		if err == nil && recordPath == path {
			records = append(records, cloneRGB11DKVSRecord(record))
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Key < records[j].Key })
	return &dkvsindexer.PathSnapshot{
		Path:         path,
		PathMeta:     h.pathMetaV1Locked(path, now),
		Records:      records,
		ServerTimeMS: now,
	}
}

func (h *rgb11MemoryDKVSHTTP) SendDKVSV1Get(path string,
	query map[string]string) ([]byte, error) {

	if h == nil {
		return rgb11DKVSV1Error(ErrDKVSPathNotSynced)
	}
	now := uint64(time.Now().UnixMilli())
	switch path {
	case "/v3/dkvs/pathmeta":
		h.mu.Lock()
		meta := h.pathMetaV1Locked(query["path"], now)
		h.mu.Unlock()
		return rgb11DKVSV1Response(&DKVSPathMetaResult{
			ServerTimeMS: now,
			PathMeta:     meta,
		})
	default:
		return rgb11DKVSV1Error(dkvsindexer.ErrInvalidRecord)
	}
}

func (h *rgb11MemoryDKVSHTTP) SendDKVSV1Post(path string, body []byte) ([]byte, error) {
	if h == nil {
		return rgb11DKVSV1Error(ErrDKVSPathNotSynced)
	}
	switch path {
	case "/v3/dkvs/sync/path":
		var request DKVSPathSyncRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		now := uint64(time.Now().UnixMilli())
		h.mu.Lock()
		snapshot := h.pathSnapshotV1Locked(request.Path, now)
		h.mu.Unlock()
		return rgb11DKVSV1Response(snapshot)
	case "/v3/dkvs/watch/path":
		var request DKVSPathWatchRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		now := uint64(time.Now().UnixMilli())
		h.mu.Lock()
		meta := h.pathMetaV1Locked(request.Path, now)
		h.mu.Unlock()
		root, err := chainhash.NewHashFromStr(strings.TrimSpace(request.StateRoot))
		if err != nil {
			return rgb11DKVSV1Error(dkvsindexer.ErrInvalidRecord)
		}
		changed := meta.Generation != request.Generation || meta.StateRoot != *root ||
			(request.ViewHeight != 0 && meta.ViewHeight != request.ViewHeight)
		return rgb11DKVSV1Response(&DKVSPathWatchResult{
			Changed: changed, ServerTimeMS: now, PathMeta: meta,
		})
	case "/v3/dkvs/records/batch-cas":
		var request DKVSBatchCASRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		return h.applyBatchCASV1(request)
	default:
		return rgb11DKVSV1Error(dkvsindexer.ErrInvalidRecord)
	}
}

func (h *rgb11MemoryDKVSHTTP) applyBatchCASV1(request DKVSBatchCASRequest) ([]byte, error) {
	if len(request.Mutations) == 0 || len(request.Mutations) > dkvsindexer.MaxBatchCASMutations {
		return rgb11DKVSV1Error(dkvsindexer.ErrInvalidRecord)
	}
	now := uint64(time.Now().UnixMilli())
	h.mu.Lock()
	defer h.mu.Unlock()

	mutationsByPath := make(map[string][]*swire.DKVSRecord)
	relayablePaths := make(map[string]struct{})
	hasLocalOnly := false
	seenKeys := make(map[string]struct{}, len(request.Mutations))
	for _, mutation := range request.Mutations {
		if mutation.Record == nil {
			return rgb11DKVSV1Error(dkvsindexer.ErrInvalidRecord)
		}
		if _, exists := seenKeys[mutation.Record.Key]; exists {
			return rgb11DKVSV1Error(dkvsindexer.ErrInvalidRecord)
		}
		seenKeys[mutation.Record.Key] = struct{}{}
		if err := dkvsindexer.VerifySignature(mutation.Record); err != nil {
			return rgb11DKVSV1Error(err)
		}
		path, err := dkvsindexer.CollectionPathForKey(mutation.Record.Key)
		if err != nil {
			return rgb11DKVSV1Error(err)
		}
		if dkvsindexer.RecordRequiresPathPrecondition(mutation.Record) {
			relayablePaths[path] = struct{}{}
			mutationsByPath[path] = append(mutationsByPath[path], mutation.Record)
		} else {
			hasLocalOnly = true
		}
	}
	if hasLocalOnly && strings.TrimSpace(request.EndpointID) != rgb11MemoryDKVSEndpointID {
		return rgb11DKVSV1Error(dkvsindexer.ErrStaleEndpoint)
	}

	conditions := make(map[string]DKVSPathPreconditionRequest, len(request.PathPreconditions))
	for _, condition := range request.PathPreconditions {
		condition.Path = strings.TrimSuffix(strings.TrimSpace(condition.Path), "/")
		if condition.Path == "" {
			return rgb11DKVSV1Error(dkvsindexer.ErrInvalidRecord)
		}
		if _, exists := conditions[condition.Path]; exists {
			return rgb11DKVSV1Error(dkvsindexer.ErrInvalidRecord)
		}
		conditions[condition.Path] = condition
	}
	if len(conditions) != len(relayablePaths) {
		return rgb11DKVSV1Error(dkvsindexer.ErrStaleGeneration)
	}
	for path := range relayablePaths {
		condition, ok := conditions[path]
		if !ok {
			return rgb11DKVSV1Error(dkvsindexer.ErrStaleGeneration)
		}
		root, err := chainhash.NewHashFromStr(strings.TrimSpace(condition.ExpectedRoot))
		if err != nil {
			return rgb11DKVSV1Error(dkvsindexer.ErrInvalidRecord)
		}
		meta := h.pathMetaV1Locked(path, now)
		if condition.ExpectedGeneration != meta.Generation || *root != meta.StateRoot {
			return rgb11DKVSV1Error(dkvsindexer.ErrStaleGeneration)
		}
		records := mutationsByPath[path]
		sort.Slice(records, func(i, j int) bool { return records[i].Key < records[j].Key })
	}

	exact := 0
	for _, mutation := range request.Mutations {
		current := h.records[mutation.Record.Key]
		if current != nil && dkvsindexer.RecordHash(current) == dkvsindexer.RecordHash(mutation.Record) {
			exact++
		}
	}
	if exact != 0 && exact != len(request.Mutations) {
		return rgb11DKVSV1Error(dkvsindexer.ErrWriteConflict)
	}
	if exact == 0 {
		for _, mutation := range request.Mutations {
			current := h.records[mutation.Record.Key]
			nextSeq := uint64(1)
			if current != nil {
				nextSeq = current.Seq + 1
			}
			if mutation.Record.Seq != nextSeq {
				return rgb11DKVSV1Error(dkvsindexer.ErrInvalidSequence)
			}
			if mutation.ExpectAbsent {
				if current != nil || strings.TrimSpace(mutation.ExpectedHash) != "" {
					return rgb11DKVSV1Error(dkvsindexer.ErrWriteConflict)
				}
			} else {
				want, err := chainhash.NewHashFromStr(strings.TrimSpace(mutation.ExpectedHash))
				if err != nil || current == nil || dkvsindexer.RecordHash(current) != *want {
					return rgb11DKVSV1Error(dkvsindexer.ErrWriteConflict)
				}
			}
		}
		projected := len(h.records)
		for _, mutation := range request.Mutations {
			_, exists := h.records[mutation.Record.Key]
			if dkvsindexer.IsTombstone(mutation.Record.Flags) {
				if exists {
					projected--
				}
			} else if !exists {
				projected++
			}
		}
		if h.maxRecords > 0 && projected > h.maxRecords {
			return rgb11DKVSV1Error(dkvsindexer.ErrFeeCapacityExceeded)
		}
		for _, mutation := range request.Mutations {
			if dkvsindexer.IsTombstone(mutation.Record.Flags) {
				delete(h.records, mutation.Record.Key)
			} else {
				h.records[mutation.Record.Key] = cloneRGB11DKVSRecord(mutation.Record)
			}
		}
		for path, records := range mutationsByPath {
			h.generations[path] += uint64(len(records))
		}
	}

	result := &dkvsindexer.WriteResult{
		Applied:      len(request.Mutations) - exact,
		Records:      make([]*swire.DKVSRecord, 0, len(request.Mutations)),
		Hashes:       make([]string, 0, len(request.Mutations)),
		PathMeta:     make(map[string]*dkvsindexer.PathMeta),
		ServerTimeMS: now,
		LocalOnly:    len(relayablePaths) == 0,
	}
	if hasLocalOnly {
		result.EndpointID = rgb11MemoryDKVSEndpointID
	}
	for _, mutation := range request.Mutations {
		record := cloneRGB11DKVSRecord(mutation.Record)
		result.Records = append(result.Records, record)
		result.Hashes = append(result.Hashes, dkvsindexer.RecordHash(record).String())
	}
	for path := range relayablePaths {
		result.PathMeta[path] = h.pathMetaV1Locked(path, now)
	}
	if len(result.PathMeta) == 0 && !result.LocalOnly {
		result.PathMeta = nil
	}
	return rgb11DKVSV1Response(result)
}
