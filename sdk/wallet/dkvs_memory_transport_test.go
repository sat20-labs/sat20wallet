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

// rgb11MemoryDKVSHTTP is a deterministic final-protocol endpoint used by DKVS,
// account-management and RGB11 tests. It deliberately exposes no path-meta,
// directory-sync, overlay, promote or epoch API.
type rgb11MemoryDKVSHTTP struct {
	mu            sync.Mutex
	records       map[string]*swire.DKVSRecord
	deleted       map[string]dkvsindexer.DKVSKeyState
	generations   map[string]uint64
	endpointID    string
	freeLocal     dkvsindexer.FreeLocalCachePolicy
	maxRecords    int
	postGate      <-chan struct{}
	postCommitErr error
	autopayState  *dkvsindexer.AutopayContractState
	autopayError  error
	bestHeight    int64
	bestHeightErr error

	statusCalls   int
	snapshotCalls int
	readCalls     int
}

func newRGB11MemoryDKVSHTTP() *rgb11MemoryDKVSHTTP {
	return &rgb11MemoryDKVSHTTP{
		records:     make(map[string]*swire.DKVSRecord),
		deleted:     make(map[string]dkvsindexer.DKVSKeyState),
		generations: make(map[string]uint64),
		endpointID:  "test-endpoint",
		bestHeight:  1,
		freeLocal: dkvsindexer.FreeLocalCachePolicy{
			Enabled: true, MaxTTL: testRGB11FreeLocalTTL,
			MaxRecordsPerSigner: 100, MaxBytesPerSigner: 1 << 20,
			MaxTotalRecords: 100_000, MaxTotalBytes: 1 << 30,
		},
	}
}

func (h *rgb11MemoryDKVSHTTP) DKVSClientConfig() (*dkvsindexer.ClientConfig, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return &dkvsindexer.ClientConfig{
		FreeLocal: h.freeLocal, Blob: dkvsindexer.BlobPolicy{MaxValueSize: swire.MaxDKVSBlobValueSize, MaxFreeLocalKeysPerSigner: 1},
		MaxBatchMutations:      dkvsindexer.MaxBatchCASMutations,
		MaxBatchBytes:          dkvsindexer.MaxBatchCASTotalSize,
		MaxPrefixesPerTerminal: dkvsindexer.MaxPrefixesPerTerminal,
		EndpointID:             h.endpointID,
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
			return rgb11DKVSResponse(-1, "contract state not found", nil,
				string(dkvsindexer.ErrorCodeRecordNotFound), 0)
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
			MaxBatchMutations:      dkvsindexer.MaxBatchCASMutations,
			MaxBatchBytes:          dkvsindexer.MaxBatchCASTotalSize,
			MaxPrefixesPerTerminal: dkvsindexer.MaxPrefixesPerTerminal,
			EndpointID:             h.endpointID,
		}
		return rgb11DKVSResponse(0, "ok", config, "", 0)
	case "/v3/dkvs/record":
		key := query["key"]
		record := h.records[key]
		if record == nil {
			return rgb11DKVSResponse(-1, "DKVS record not found", nil,
				string(dkvsindexer.ErrorCodeRecordNotFound), 0)
		}
		return json.Marshal(map[string]interface{}{
			"code": 0, "msg": "ok", "data": cloneRGB11DKVSRecord(record),
			"etag": dkvsindexer.RecordHash(record).String(),
		})
	case "/v3/dkvs/key-state":
		key := query["key"]
		if record := h.records[key]; record != nil {
			state := dkvsindexer.DKVSKeyState{
				Key: key, Status: dkvsindexer.KeyStateActive, Seq: record.Seq,
				ETag:         dkvsindexer.RecordHash(record).String(),
				ExpiryHeight: dkvsindexer.RecordExpiryHeight(record), Record: cloneRGB11DKVSRecord(record),
			}
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
		if state, ok := h.deleted[key]; ok {
			copyState := state
			return rgb11DKVSResponse(0, "ok", &copyState, "", 0)
		}
		return rgb11DKVSResponse(0, "ok", &dkvsindexer.DKVSKeyState{
			Key: key, Status: dkvsindexer.KeyStateNeverSeen,
		}, "", 0)
	default:
		return nil, fmt.Errorf("unexpected DKVS GET path %s", path)
	}
}

func (h *rgb11MemoryDKVSHTTP) recordFloorLocked(key string) uint64 {
	if record := h.records[key]; record != nil {
		return record.Seq
	}
	if deleted, ok := h.deleted[key]; ok {
		return deleted.Seq
	}
	return 0
}

func (h *rgb11MemoryDKVSHTTP) applyBatchCAS(request DKVSBatchCASRequest) ([]byte, error) {
	if h.postGate != nil {
		<-h.postGate
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(request.Mutations) == 0 {
		return rgb11DKVSResponse(-1, dkvsindexer.ErrInvalidRecord.Error(), nil,
			string(dkvsindexer.ErrorCodeInvalidRecord), 0)
	}
	localOnly := false
	already := 0
	for _, mutation := range request.Mutations {
		if mutation.Record == nil || dkvsindexer.VerifySignature(mutation.Record) != nil {
			return rgb11DKVSResponse(-1, dkvsindexer.ErrInvalidSignature.Error(), nil,
				string(dkvsindexer.ErrorCodeInvalidRecord), 0)
		}
		if dkvsWalletRecordIsFreeLocal(mutation.Record) {
			localOnly = true
		}
		if current := h.records[mutation.Record.Key]; current != nil &&
			dkvsindexer.RecordHash(current) == dkvsindexer.RecordHash(mutation.Record) {
			already++
		} else if deleted, ok := h.deleted[mutation.Record.Key]; ok &&
			dkvsindexer.IsTombstone(mutation.Record.Flags) && deleted.ETag == dkvsindexer.RecordHash(mutation.Record).String() {
			already++
		}
	}
	if localOnly && request.EndpointID != h.endpointID {
		return rgb11DKVSResponse(-1, dkvsindexer.ErrLocalOnlyEndpointMismatch.Error(), nil,
			string(dkvsindexer.ErrorCodeLocalOnlyEndpointMismatch), 0)
	}
	if already != 0 && already != len(request.Mutations) {
		return rgb11DKVSResponse(-1, dkvsindexer.ErrWriteConflict.Error(), nil,
			string(dkvsindexer.ErrorCodeWriteConflict), 0)
	}
	if already == 0 {
		for _, mutation := range request.Mutations {
			floor := h.recordFloorLocked(mutation.Record.Key)
			if mutation.Record.Seq != floor+1 {
				return rgb11DKVSResponse(-1, dkvsindexer.ErrInvalidSequence.Error(), nil,
					string(dkvsindexer.ErrorCodeInvalidSequence), 0)
			}
			if mutation.ExpectAbsent {
				if floor != 0 {
					return rgb11DKVSResponse(-1, dkvsindexer.ErrWriteConflict.Error(), nil,
						string(dkvsindexer.ErrorCodeWriteConflict), 0)
				}
			} else {
				var currentETag string
				if current := h.records[mutation.Record.Key]; current != nil {
					currentETag = dkvsindexer.RecordHash(current).String()
				} else if deleted, ok := h.deleted[mutation.Record.Key]; ok {
					currentETag = deleted.ETag
				}
				if mutation.ExpectedETag == "" || mutation.ExpectedETag != currentETag {
					return rgb11DKVSResponse(-1, dkvsindexer.ErrWriteConflict.Error(), nil,
						string(dkvsindexer.ErrorCodeWriteConflict), 0)
				}
			}
			if current := h.records[mutation.Record.Key]; current != nil &&
				!dkvsWalletRecordIsFreeLocal(current) && dkvsWalletRecordIsFreeLocal(mutation.Record) {
				return rgb11DKVSResponse(-1, dkvsindexer.ErrStorageModeDowngrade.Error(), nil,
					string(dkvsindexer.ErrorCodeStorageModeDowngrade), 0)
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
			return rgb11DKVSResponse(-1, dkvsindexer.ErrFeeCapacityExceeded.Error(), nil,
				string(dkvsindexer.ErrorCodeQuotaExceeded), 0)
		}
	}

	records := make([]*swire.DKVSRecord, 0, len(request.Mutations))
	hashes := make([]string, 0, len(request.Mutations))
	if already == 0 {
		for _, mutation := range request.Mutations {
			record := cloneRGB11DKVSRecord(mutation.Record)
			hash := dkvsindexer.RecordHash(record).String()
			if dkvsindexer.IsTombstone(record.Flags) {
				delete(h.records, record.Key)
				h.deleted[record.Key] = dkvsindexer.DKVSKeyState{
					Key: record.Key, Status: dkvsindexer.KeyStateDeleted, Seq: record.Seq, ETag: hash,
				}
			} else {
				h.records[record.Key] = record
				delete(h.deleted, record.Key)
			}
			if prefix, err := dkvsindexer.CollectionPathForKey(record.Key); err == nil {
				h.generations[prefix]++
			}
		}
	}
	for _, mutation := range request.Mutations {
		records = append(records, cloneRGB11DKVSRecord(mutation.Record))
		hashes = append(hashes, dkvsindexer.RecordHash(mutation.Record).String())
	}
	prefixSet := make(map[string]struct{})
	for _, mutation := range request.Mutations {
		prefix, err := dkvsindexer.CollectionPathForKey(mutation.Record.Key)
		if err == nil {
			prefixSet[prefix] = struct{}{}
		}
	}
	prefixStates := make([]dkvsindexer.PrefixGeneration, 0, len(prefixSet))
	for prefix := range prefixSet {
		prefixStates = append(prefixStates, dkvsindexer.PrefixGeneration{
			Prefix: prefix, Generation: h.generations[prefix],
		})
	}
	sort.Slice(prefixStates, func(a, b int) bool {
		return prefixStates[a].Prefix < prefixStates[b].Prefix
	})
	result := &dkvsindexer.WriteResult{
		Applied: len(request.Mutations) - already, Records: records, Hashes: hashes,
		ViewHeight: 1, ServerTimeMS: uint64(time.Now().UnixMilli()),
		LocalOnly: localOnly, EndpointID: h.endpointID, RequestID: request.RequestID,
		PrefixStates: prefixStates,
	}
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
		states = append(states, dkvsindexer.DKVSKeyState{
			Key: key, Status: dkvsindexer.KeyStateActive, Seq: record.Seq,
			ETag: dkvsindexer.RecordHash(record).String(), ExpiryHeight: dkvsindexer.RecordExpiryHeight(record),
			Record: copyRecord,
		})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Key < records[j].Key })
	sort.Slice(states, func(i, j int) bool { return states[i].Key < states[j].Key })
	return records, states
}

func (h *rgb11MemoryDKVSHTTP) prefixSnapshot(prefix string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.snapshotCalls++
	records, states := h.prefixRecordsLocked(prefix)
	return rgb11DKVSResponse(0, "ok", &dkvsindexer.PrefixSnapshot{
		EndpointID: h.endpointID, Prefix: prefix, Generation: h.generations[prefix], ViewHeight: 1,
		Records: records, KeyStates: states,
	}, "", 0)
}

func (h *rgb11MemoryDKVSHTTP) prefixStatus(endpointID string, known []dkvsindexer.PrefixGeneration) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.statusCalls++
	if endpointID != h.endpointID {
		return rgb11DKVSResponse(-1, dkvsindexer.ErrEndpointMismatch.Error(), nil,
			string(dkvsindexer.ErrorCodeEndpointMismatch), 0)
	}
	changed := make([]dkvsindexer.PrefixGeneration, 0)
	for _, item := range known {
		if generation := h.generations[item.Prefix]; generation != item.Generation {
			changed = append(changed, dkvsindexer.PrefixGeneration{Prefix: item.Prefix, Generation: generation})
		}
	}
	return rgb11DKVSResponse(0, "ok", &dkvsindexer.PrefixStatusResult{
		EndpointID: h.endpointID, ViewHeight: 1, Changed: changed,
	}, "", 0)
}

func (h *rgb11MemoryDKVSHTTP) prefixRead(prefix string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.readCalls++
	records, states := h.prefixRecordsLocked(prefix)
	return rgb11DKVSResponse(0, "ok", &dkvsindexer.PrefixReadResult{
		EndpointID: h.endpointID, Prefix: prefix, ViewHeight: 1, Records: records, KeyStates: states,
	}, "", 0)
}

func (h *rgb11MemoryDKVSHTTP) SendDKVSPost(path string, body []byte) ([]byte, error) {
	switch path {
	case "/v3/dkvs/records/batch-cas":
		var request DKVSBatchCASRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		return h.applyBatchCAS(request)
	case "/v3/dkvs/prefixes/snapshot":
		var request struct {
			Prefix string `json:"prefix"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		return h.prefixSnapshot(request.Prefix)
	case "/v3/dkvs/prefixes/status":
		var request struct {
			EndpointID string                         `json:"endpoint_id"`
			Prefixes   []dkvsindexer.PrefixGeneration `json:"prefixes"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		return h.prefixStatus(request.EndpointID, request.Prefixes)
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
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return h.SendGetRequest(url)
	}
}

func (h *rgb11MemoryDKVSHTTP) SendDKVSGetContext(ctx context.Context, path string,
	query map[string]string) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return h.SendDKVSGet(path, query)
	}
}

func (h *rgb11MemoryDKVSHTTP) SendDKVSPostContext(ctx context.Context, path string, body []byte) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return h.SendDKVSPost(path, body)
	}
}

func (h *rgb11MemoryDKVSHTTP) SendPostRequestContext(ctx context.Context, url *URL, body []byte) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return h.SendPostRequest(url, body)
	}
}

func rgb11DKVSResponse(code int, msg string, data interface{}, errorCode string, total int) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"code": code, "msg": msg, "data": data, "error_code": errorCode, "total": total,
	})
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
	delete(h.deleted, copyRecord.Key)
}
