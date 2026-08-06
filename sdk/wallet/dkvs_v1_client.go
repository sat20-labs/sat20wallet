package wallet

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// DKVSError preserves the node's stable machine-readable error code. Wallet
// code must use errors.Is/As rather than parsing an English message.
type DKVSError struct {
	Code    dkvsindexer.ErrorCode
	Message string
}

func (e *DKVSError) Error() string {
	if e == nil {
		return "DKVS request failed"
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return string(e.Code)
}

func (e *DKVSError) Unwrap() error {
	if e == nil {
		return nil
	}
	switch e.Code {
	case dkvsindexer.ErrorCodeWriteConflict:
		return dkvsindexer.ErrWriteConflict
	case dkvsindexer.ErrorCodeStaleGeneration:
		return dkvsindexer.ErrStaleGeneration
	case dkvsindexer.ErrorCodeStaleEndpoint:
		return dkvsindexer.ErrStaleEndpoint
	case dkvsindexer.ErrorCodePermissionDenied:
		return dkvsindexer.ErrPermissionDenied
	case dkvsindexer.ErrorCodeInvalidSequence:
		return dkvsindexer.ErrInvalidSequence
	case dkvsindexer.ErrorCodePathDiverged:
		return dkvsindexer.ErrPathDiverged
	case dkvsindexer.ErrorCodeLocalOnlyEndpointMismatch:
		return dkvsindexer.ErrLocalOnlyEndpointMismatch
	case dkvsindexer.ErrorCodeQuotaExceeded:
		return dkvsindexer.ErrFreeLocalQuotaExceeded
	case dkvsindexer.ErrorCodeRecordNotFound:
		return dkvsindexer.ErrRecordNotFound
	default:
		return dkvsindexer.ErrInvalidRecord
	}
}

type dkvsV1BaseResp struct {
	Code      int    `json:"code"`
	Msg       string `json:"msg"`
	ErrorCode string `json:"error_code,omitempty"`
}

func decodeDKVSV1Response(raw []byte, out interface{}) error {
	var base dkvsV1BaseResp
	if err := json.Unmarshal(raw, &base); err != nil {
		return err
	}
	if base.Code != 0 {
		return &DKVSError{Code: dkvsindexer.ErrorCode(base.ErrorCode), Message: base.Msg}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (p *SatsNetDKVSClient) getDKVSV1(path string, query map[string]string, out interface{}) error {
	if p == nil || p.RESTClient == nil || p.Http == nil {
		return fmt.Errorf("DKVS client is unavailable")
	}
	var raw []byte
	var err error
	if transport, ok := p.Http.(dkvsV1HTTPTransport); ok {
		raw, err = transport.SendDKVSV1Get(path, query)
	} else {
		url := p.GetUrl(path)
		url.Query = query
		raw, err = p.Http.SendGetRequest(url)
	}
	if err != nil {
		return err
	}
	return decodeDKVSV1Response(raw, out)
}

func (p *SatsNetDKVSClient) postDKVSV1(path string, req interface{}, out interface{}) error {
	if p == nil || p.RESTClient == nil || p.Http == nil {
		return fmt.Errorf("DKVS client is unavailable")
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		return err
	}
	var raw []byte
	if transport, ok := p.Http.(dkvsV1HTTPTransport); ok {
		raw, err = transport.SendDKVSV1Post(path, encoded)
	} else {
		raw, err = p.Http.SendPostRequest(p.GetUrl(path), encoded)
	}
	if err != nil {
		return err
	}
	return decodeDKVSV1Response(raw, out)
}

type DKVSPathMetaResult struct {
	ServerTimeMS uint64                `json:"server_time_ms"`
	PathMeta     *dkvsindexer.PathMeta `json:"pathmeta"`
}

type dkvsPathMetaV1ClientResp struct {
	dkvsV1BaseResp
	Data *DKVSPathMetaResult `json:"data,omitempty"`
}

func (p *SatsNetDKVSClient) GetPathMetaV1(path string) (*DKVSPathMetaResult, error) {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	if _, err := dkvsindexer.ParsePrefix(path); err != nil {
		return nil, err
	}
	var resp dkvsPathMetaV1ClientResp
	if err := p.getDKVSV1("/v3/dkvs/pathmeta", map[string]string{"path": path}, &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil || resp.Data.PathMeta == nil || resp.Data.PathMeta.Path != path {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return resp.Data, nil
}

type DKVSPathSyncRequest struct {
	Path string `json:"path"`
}

type dkvsPathSyncClientResp struct {
	dkvsV1BaseResp
	Data *dkvsindexer.PathSnapshot `json:"data,omitempty"`
}

func (p *SatsNetDKVSClient) SyncPath(path string,
	opts dkvsindexer.RecordVerificationOptions) (*dkvsindexer.PathSnapshot, error) {

	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	if _, err := dkvsindexer.ParsePrefix(path); err != nil {
		return nil, err
	}
	var resp dkvsPathSyncClientResp
	if err := p.postDKVSV1("/v3/dkvs/sync/path", DKVSPathSyncRequest{Path: path}, &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil || resp.Data.Path != path {
		return nil, dkvsindexer.ErrInvalidSnapshot
	}
	if err := dkvsindexer.ValidatePathSnapshotForClient(resp.Data, opts); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

type DKVSPathWatchRequest struct {
	Path           string `json:"path"`
	Generation     uint64 `json:"generation"`
	StateRoot      string `json:"state_root"`
	ViewHeight     uint64 `json:"view_height,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type DKVSPathWatchResult struct {
	Changed      bool                  `json:"changed"`
	ServerTimeMS uint64                `json:"server_time_ms"`
	PathMeta     *dkvsindexer.PathMeta `json:"pathmeta"`
}

type dkvsPathWatchClientResp struct {
	dkvsV1BaseResp
	Data *DKVSPathWatchResult `json:"data,omitempty"`
}

func (p *SatsNetDKVSClient) WatchPath(req DKVSPathWatchRequest) (*DKVSPathWatchResult, error) {
	req.Path = strings.TrimSuffix(strings.TrimSpace(req.Path), "/")
	if _, err := dkvsindexer.ParsePrefix(req.Path); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.StateRoot) == "" {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	var resp dkvsPathWatchClientResp
	if err := p.postDKVSV1("/v3/dkvs/watch/path", req, &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil || resp.Data.PathMeta == nil || resp.Data.PathMeta.Path != req.Path {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return resp.Data, nil
}

type dkvsWriteResultClientResp struct {
	dkvsV1BaseResp
	Data *dkvsindexer.WriteResult `json:"data,omitempty"`
}

func buildDKVSBatchCASRequest(mutations []dkvsindexer.CASMutation,
	pathConditions []dkvsindexer.PathWritePrecondition,
	endpointID string) (DKVSBatchCASRequest, error) {

	if len(mutations) == 0 {
		return DKVSBatchCASRequest{}, dkvsindexer.ErrInvalidRecord
	}
	req := DKVSBatchCASRequest{
		Mutations:  make([]DKVSCASMutationRequest, 0, len(mutations)),
		EndpointID: endpointID,
	}
	for _, mutation := range mutations {
		encoded, err := casMutationRequest(mutation)
		if err != nil {
			return DKVSBatchCASRequest{}, err
		}
		req.Mutations = append(req.Mutations, encoded)
	}
	for _, condition := range pathConditions {
		req.PathPreconditions = append(req.PathPreconditions, DKVSPathPreconditionRequest{
			Path: condition.Path, ExpectedRoot: condition.ExpectedRoot.String(),
			ExpectedGeneration: condition.ExpectedGeneration,
		})
	}
	return req, nil
}

func verifyDKVSWriteResult(mutations []dkvsindexer.CASMutation,
	result *dkvsindexer.WriteResult) error {
	if result == nil {
		return fmt.Errorf("DKVS batch response is nil: %w", dkvsindexer.ErrInvalidRecord)
	}
	if result.Applied != 0 && result.Applied != len(mutations) {
		return fmt.Errorf("DKVS batch applied=%d mutations=%d: %w", result.Applied, len(mutations), dkvsindexer.ErrInvalidRecord)
	}
	if len(result.Records) != len(mutations) || len(result.Hashes) != len(mutations) {
		return fmt.Errorf("DKVS batch echo records=%d hashes=%d mutations=%d: %w",
			len(result.Records), len(result.Hashes), len(mutations), dkvsindexer.ErrInvalidRecord)
	}
	for index, mutation := range mutations {
		if _, err := verifyDKVSWriteEcho(mutation.Record, result.Records[index], result.Hashes[index]); err != nil {
			return fmt.Errorf("verify DKVS batch echo index=%d key=%s: %w", index, mutation.Record.Key, err)
		}
	}
	if result.PathMeta == nil && !result.LocalOnly {
		return fmt.Errorf("DKVS relayable batch response has no path metadata: %w", dkvsindexer.ErrInvalidRecord)
	}
	return nil
}

// putRecordBatchCASV1Raw submits an already-persisted exact batch. It never
// creates, mutates or acknowledges an outbox entry and is therefore safe for
// the SyncWorker replay path.
func (p *SatsNetDKVSClient) putRecordBatchCASV1Raw(mutations []dkvsindexer.CASMutation,
	pathConditions []dkvsindexer.PathWritePrecondition,
	endpointID string) (*dkvsindexer.WriteResult, error) {

	req, err := buildDKVSBatchCASRequest(mutations, pathConditions, endpointID)
	if err != nil {
		return nil, err
	}
	var resp dkvsWriteResultClientResp
	if err := p.postDKVSV1("/v3/dkvs/records/batch-cas", req, &resp); err != nil {
		return nil, fmt.Errorf("submit DKVS batch: %w", err)
	}
	if err := verifyDKVSWriteResult(mutations, resp.Data); err != nil {
		return nil, fmt.Errorf("verify DKVS batch response: %w", err)
	}
	return resp.Data, nil
}

func batchRequiresLocalEndpoint(mutations []dkvsindexer.CASMutation) bool {
	for _, mutation := range mutations {
		if mutation.Record != nil && !dkvsindexer.RecordRequiresPathPrecondition(mutation.Record) {
			return true
		}
	}
	return false
}

// PutRecordBatchCASV1 atomically persists the exact signed batch before
// network submission and atomically updates the confirmed replica and removes
// the outbox entry only after a verified response.
func (p *SatsNetDKVSClient) PutRecordBatchCASV1(mutations []dkvsindexer.CASMutation,
	pathConditions []dkvsindexer.PathWritePrecondition) (*dkvsindexer.WriteResult, error) {

	endpointID := ""
	if batchRequiresLocalEndpoint(mutations) {
		config, err := p.GetClientConfigV1()
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(config.EndpointID) == "" {
			return nil, dkvsindexer.ErrStaleEndpoint
		}
		endpointID = config.EndpointID
	}
	if p == nil || p.manager == nil || p.manager.owner == nil ||
		p.manager.owner.db == nil || strings.TrimSpace(p.replicaNamespace) == "" {
		return p.putRecordBatchCASV1Raw(mutations, pathConditions, endpointID)
	}
	store := newDKVSReplicaStore(p.manager.owner.db)
	entry, err := newDKVSBatchOutboxEntry(p.replicaNamespace, mutations,
		pathConditions, endpointID)
	if err != nil {
		return nil, err
	}
	if err := store.queueBatchOutbox(entry); err != nil {
		return nil, fmt.Errorf("persist DKVS batch outbox: %w", err)
	}
	if err := store.updateBatchOutboxState(entry, dkvsSessionInflight, nil); err != nil {
		return nil, fmt.Errorf("mark DKVS batch inflight: %w", err)
	}
	result, err := p.putRecordBatchCASV1Raw(mutations, pathConditions, endpointID)
	if err != nil {
		_ = store.markOutboxFailure(entry, err)
		return nil, err
	}
	if err := store.applyWriteResultAndAck(entry, result); err != nil {
		_ = store.markOutboxFailure(entry, err)
		return nil, fmt.Errorf("commit DKVS batch replica: %w", err)
	}
	return result, nil
}

func IsDKVSErrorCode(err error, code dkvsindexer.ErrorCode) bool {
	var remote *DKVSError
	return errors.As(err, &remote) && remote.Code == code
}
