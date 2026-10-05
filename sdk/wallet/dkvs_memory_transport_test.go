package wallet

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Deterministic current-state endpoint used by DKVS, account and RGB11 unit
// tests. Current KV and one latest-generation entry per key are the only state.
type rgb11MemoryDKVSHTTP struct {
	mu      sync.Mutex
	records map[string]*swire.DKVSRecord
	// Retained only for negative fixtures that inject obsolete payloads. The
	// current-state endpoint neither populates nor consumes this map.
	deleted          map[string]dkvsindexer.DKVSKeyState
	generations      map[string]uint64
	changedAt        map[string]uint64
	endpointID       string
	freeLocal        dkvsindexer.FreeLocalCachePolicy
	maxRecords       int
	postGate         <-chan struct{}
	postCommitErr    error
	autopayState     *dkvsindexer.AutopayContractState
	autopayError     error
	bestHeight       int64
	bestHeightErr    error
	statusCalls      int
	snapshotCalls    int
	deltaCalls       int
	deltaUnavailable bool
	readCalls        int
}

func newRGB11MemoryDKVSHTTP() *rgb11MemoryDKVSHTTP {
	return &rgb11MemoryDKVSHTTP{
		records: make(map[string]*swire.DKVSRecord), deleted: make(map[string]dkvsindexer.DKVSKeyState),
		generations: make(map[string]uint64), changedAt: make(map[string]uint64),
		endpointID: "test-endpoint", bestHeight: 1,
		freeLocal: dkvsindexer.FreeLocalCachePolicy{Enabled: true, MaxTTL: testRGB11FreeLocalTTL,
			MaxRecordsPerSigner: 100, MaxBytesPerSigner: 1 << 20, MaxTotalRecords: 100_000, MaxTotalBytes: 1 << 30},
	}
}

func (h *rgb11MemoryDKVSHTTP) DKVSClientConfig() (*dkvsindexer.ClientConfig, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return &dkvsindexer.ClientConfig{
		FreeLocal: h.freeLocal, Blob: dkvsindexer.BlobPolicy{MaxValueSize: swire.MaxDKVSBlobValueSize, MaxFreeLocalKeysPerSigner: 1},
		MaxBatchMutations: dkvsindexer.MaxBatchCASMutations, MaxBatchBytes: dkvsindexer.MaxBatchCASTotalSize,
		MaxPrefixesPerTerminal: dkvsindexer.MaxPrefixesPerTerminal, EndpointID: h.endpointID,
	}, nil
}

func (h *rgb11MemoryDKVSHTTP) SendGetRequest(url *URL) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if strings.HasSuffix(url.Path, "/btc/block/bestblockheight") {
		if h.bestHeightErr != nil {
			return nil, h.bestHeightErr
		}
		height := h.bestHeight
		if height == 0 {
			height = 1
		}
		return rgb11DKVSResponse(0, "ok", height, "", 0)
	}
	if strings.Contains(url.Path, "/v3/contracts/") && strings.HasSuffix(url.Path, "/state") {
		if h.autopayError != nil {
			return nil, h.autopayError
		}
		if h.autopayState == nil {
			return rgb11DKVSResponse(-1, "contract state not found", nil, string(dkvsindexer.ErrorCodeRecordNotFound), 0)
		}
		return rgb11DKVSResponse(0, "ok", h.autopayState, "", 0)
	}
	return nil, fmt.Errorf("unexpected GET path %s", url.Path)
}
func (h *rgb11MemoryDKVSHTTP) SendPostRequest(url *URL, body []byte) ([]byte, error) {
	return nil, fmt.Errorf("unexpected generic POST path %s", url.Path)
}

func (h *rgb11MemoryDKVSHTTP) SendDKVSGet(path string, query map[string]string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch path {
	case "/v3/dkvs/config":
		config := &dkvsindexer.ClientConfig{
			FreeLocal: h.freeLocal, Blob: dkvsindexer.BlobPolicy{MaxValueSize: swire.MaxDKVSBlobValueSize, MaxFreeLocalKeysPerSigner: 1},
			MaxBatchMutations: dkvsindexer.MaxBatchCASMutations, MaxBatchBytes: dkvsindexer.MaxBatchCASTotalSize,
			MaxPrefixesPerTerminal: dkvsindexer.MaxPrefixesPerTerminal, EndpointID: h.endpointID,
		}
		return rgb11DKVSResponse(0, "ok", config, "", 0)
	case "/v3/dkvs/record":
		record := h.records[query["key"]]
		if record == nil {
			return rgb11DKVSResponse(-1, "DKVS record not found", nil, string(dkvsindexer.ErrorCodeRecordNotFound), 0)
		}
		return json.Marshal(map[string]interface{}{"code": 0, "msg": "ok", "data": cloneRGB11DKVSRecord(record), "etag": dkvsindexer.RecordHash(record).String()})
	case "/v3/dkvs/key-state":
		key := query["key"]
		if record := h.records[key]; record != nil {
			state := dkvsindexer.DKVSKeyState{Key: key, Status: dkvsindexer.KeyStateActive, Seq: record.Seq,
				ETag: dkvsindexer.RecordHash(record).String(), ExpiryHeight: dkvsindexer.RecordExpiryHeight(record), Record: cloneRGB11DKVSRecord(record)}
			if proof, err := dkvsindexer.ParseFeeProof(record.FeeProof); err == nil {
				switch proof.Mode {
				case dkvsindexer.FeeModeFreeLocal:
					state.StorageMode = dkvsindexer.StorageModeFreeLocal
				case dkvsindexer.FeeModeAutopay:
					state.StorageMode = dkvsindexer.StorageModeAutopay
				default:
					state.StorageMode = dkvsindexer.StorageModePaid
				}
			}
			return rgb11DKVSResponse(0, "ok", &state, "", 0)
		}
		return rgb11DKVSResponse(0, "ok", &dkvsindexer.DKVSKeyState{Key: key, Status: dkvsindexer.KeyStateNeverSeen}, "", 0)
	default:
		return nil, fmt.Errorf("unexpected DKVS GET path %s", path)
	}
}

func (h *rgb11MemoryDKVSHTTP) recordFloorLocked(key string) uint64 {
	if record := h.records[key]; record != nil {
		return record.Seq
	}
	return 0
}

func (h *rgb11MemoryDKVSHTTP) applyBatchCAS(request DKVSBatchCASRequest) ([]byte, error) {
	if h.postGate != nil {
		<-h.postGate
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	fail := func(err error) ([]byte, error) {
		return rgb11DKVSResponse(-1, err.Error(), nil, string(dkvsindexer.ErrorCodeOf(err)), 0)
	}
	if len(request.Mutations) == 0 {
		return fail(dkvsindexer.ErrInvalidRecord)
	}
	localOnly, already := false, 0
	for _, mutation := range request.Mutations {
		record := mutation.Record
		if record == nil || dkvsindexer.VerifySignature(record) != nil {
			return fail(dkvsindexer.ErrInvalidSignature)
		}
		current := h.records[record.Key]
		if dkvsWalletRecordIsFreeLocal(record) || (dkvsindexer.IsTombstone(record.Flags) && current != nil && dkvsWalletRecordIsFreeLocal(current)) {
			localOnly = true
		}
		if dkvsindexer.IsTombstone(record.Flags) {
			target, err := dkvsindexer.DeleteTargetHash(record)
			if err != nil || target.String() != mutation.ExpectedETag {
				return fail(dkvsindexer.ErrInvalidRecord)
			}
			if current == nil {
				already++
			}
		} else if current != nil && dkvsindexer.RecordHash(current) == dkvsindexer.RecordHash(record) {
			already++
		}
	}
	if localOnly && request.EndpointID != h.endpointID {
		return fail(dkvsindexer.ErrLocalOnlyEndpointMismatch)
	}
	if already != 0 && already != len(request.Mutations) {
		return fail(dkvsindexer.ErrWriteConflict)
	}
	if already == 0 {
		for _, mutation := range request.Mutations {
			current := h.records[mutation.Record.Key]
			if mutation.ExpectAbsent {
				if current != nil {
					return fail(dkvsindexer.ErrWriteConflict)
				}
			} else if current == nil || mutation.ExpectedETag == "" || mutation.ExpectedETag != dkvsindexer.RecordHash(current).String() {
				return fail(dkvsindexer.ErrWriteConflict)
			}
			if mutation.Record.Seq != h.recordFloorLocked(mutation.Record.Key)+1 {
				return fail(dkvsindexer.ErrInvalidSequence)
			}
			if current != nil && !dkvsindexer.IsTombstone(mutation.Record.Flags) && !dkvsWalletRecordIsFreeLocal(current) && dkvsWalletRecordIsFreeLocal(mutation.Record) {
				return fail(dkvsindexer.ErrStorageModeDowngrade)
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
			return fail(dkvsindexer.ErrFeeCapacityExceeded)
		}
	}
	if already == 0 {
		paths := make(map[string]uint64)
		for _, mutation := range request.Mutations {
			prefix, err := dkvsindexer.CollectionPathForKey(mutation.Record.Key)
			if err == nil {
				if _, exists := paths[prefix]; !exists {
					h.generations[prefix]++
					paths[prefix] = h.generations[prefix]
				}
			}
		}
		for _, mutation := range request.Mutations {
			record := cloneRGB11DKVSRecord(mutation.Record)
			if dkvsindexer.IsTombstone(record.Flags) {
				delete(h.records, record.Key)
				delete(h.changedAt, record.Key)
			} else {
				h.records[record.Key] = record
				if prefix, err := dkvsindexer.CollectionPathForKey(record.Key); err == nil {
					h.changedAt[record.Key] = paths[prefix]
				}
			}
		}
	}
	records := make([]*swire.DKVSRecord, 0, len(request.Mutations))
	hashes := make([]string, 0, len(request.Mutations))
	prefixSet := make(map[string]struct{})
	for _, mutation := range request.Mutations {
		records = append(records, cloneRGB11DKVSRecord(mutation.Record))
		hashes = append(hashes, dkvsindexer.RecordHash(mutation.Record).String())
		if prefix, err := dkvsindexer.CollectionPathForKey(mutation.Record.Key); err == nil {
			prefixSet[prefix] = struct{}{}
		}
	}
	prefixStates := make([]dkvsindexer.PrefixGeneration, 0, len(prefixSet))
	for prefix := range prefixSet {
		prefixStates = append(prefixStates, dkvsindexer.PrefixGeneration{Prefix: prefix, Generation: h.generations[prefix]})
	}
	sort.Slice(prefixStates, func(a, b int) bool { return prefixStates[a].Prefix < prefixStates[b].Prefix })
	height := uint64(1)
	if h.bestHeight > 0 {
		height = uint64(h.bestHeight)
	}
	result := &dkvsindexer.WriteResult{Applied: len(request.Mutations) - already, Records: records, Hashes: hashes,
		ViewHeight: height, ServerTimeMS: uint64(time.Now().UnixMilli()), LocalOnly: localOnly, EndpointID: h.endpointID,
		RequestID: request.RequestID, PrefixStates: prefixStates}
	if h.postCommitErr != nil {
		err := h.postCommitErr
		h.postCommitErr = nil
		return nil, err
	}
	return rgb11DKVSResponse(0, "ok", result, "", 0)
}

func matchesAnyPrefix(key string, prefixes []string) bool {
	for _, prefix := range prefixes {
		prefix = strings.TrimSuffix(prefix, "/")
		if key == prefix || strings.HasPrefix(key, prefix+"/") {
			return true
		}
	}
	return false
}
func (h *rgb11MemoryDKVSHTTP) prefixRecordsLocked(prefix string) ([]*swire.DKVSRecord, []dkvsindexer.DKVSKeyState) {
	records := make([]*swire.DKVSRecord, 0)
	states := make([]dkvsindexer.DKVSKeyState, 0)
	for key, record := range h.records {
		if !matchesAnyPrefix(key, []string{prefix}) {
			continue
		}
		copyRecord := cloneRGB11DKVSRecord(record)
		records = append(records, copyRecord)
		states = append(states, dkvsindexer.DKVSKeyState{Key: key, Status: dkvsindexer.KeyStateActive, Seq: record.Seq,
			ETag: dkvsindexer.RecordHash(record).String(), ExpiryHeight: dkvsindexer.RecordExpiryHeight(record), Record: copyRecord})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Key < records[j].Key })
	sort.Slice(states, func(i, j int) bool { return states[i].Key < states[j].Key })
	return records, states
}
func (h *rgb11MemoryDKVSHTTP) prefixRead(prefix string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.readCalls++
	records, states := h.prefixRecordsLocked(prefix)
	return rgb11DKVSResponse(0, "ok", &dkvsindexer.PrefixReadResult{EndpointID: h.endpointID, Prefix: prefix, ViewHeight: 1, Records: records, KeyStates: states}, "", 0)
}
func (h *rgb11MemoryDKVSHTTP) SendDKVSPost(path string, body []byte) ([]byte, error) {
	switch path {
	case "/v3/dkvs/active/sync", "/v3/dkvs/active/watch":
		return h.sendActiveRequest(context.Background(), path, body)
	case "/v3/dkvs/records/batch-cas":
		var request DKVSBatchCASRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		return h.applyBatchCAS(request)
	case "/v3/dkvs/prefixes/read":
		var request struct {
			Prefix string `json:"prefix"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		return h.prefixRead(request.Prefix)
	default:
		return nil, fmt.Errorf("unexpected DKVS POST path %s", path)
	}
}
func (h *rgb11MemoryDKVSHTTP) SendGetRequestContext(ctx context.Context, url *URL) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return h.SendGetRequest(url)
}
func (h *rgb11MemoryDKVSHTTP) SendDKVSGetContext(ctx context.Context, path string, query map[string]string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return h.SendDKVSGet(path, query)
}
func (h *rgb11MemoryDKVSHTTP) SendDKVSPostContext(ctx context.Context, path string, body []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if path == "/v3/dkvs/active/sync" || path == "/v3/dkvs/active/watch" {
		return h.sendActiveRequest(ctx, path, body)
	}
	return h.SendDKVSPost(path, body)
}
func (h *rgb11MemoryDKVSHTTP) SendPostRequestContext(ctx context.Context, url *URL, body []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return h.SendPostRequest(url, body)
}
func rgb11DKVSResponse(code int, msg string, data interface{}, errorCode string, total int) ([]byte, error) {
	return json.Marshal(map[string]interface{}{"code": code, "msg": msg, "data": data, "error_code": errorCode, "total": total})
}
func cloneRGB11DKVSRecord(record *swire.DKVSRecord) *swire.DKVSRecord {
	if record == nil {
		return nil
	}
	copyValue := *record
	copyValue.Value = append([]byte(nil), record.Value...)
	copyValue.PubKey = append([]byte(nil), record.PubKey...)
	copyValue.Signature = append([]byte(nil), record.Signature...)
	copyValue.FeeProof = append([]byte(nil), record.FeeProof...)
	return &copyValue
}
func (h *rgb11MemoryDKVSHTTP) seedInternalMailboxRecord(record *swire.DKVSRecord) {
	if h == nil || record == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	copyRecord := cloneRGB11DKVSRecord(record)
	h.records[copyRecord.Key] = copyRecord
	if prefix, err := dkvsindexer.CollectionPathForKey(copyRecord.Key); err == nil {
		h.generations[prefix]++
		h.changedAt[copyRecord.Key] = h.generations[prefix]
	}
}
