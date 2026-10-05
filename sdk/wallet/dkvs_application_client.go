package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type DKVSError = dkvscore.DKVSError

func (p *SatsNetDKVSClient) dkvsWritePrecondition(key string) (dkvsindexer.WritePrecondition, *swire.DKVSRecord, error) {
	state, err := p.GetKeyState(key)
	if err != nil {
		return dkvsindexer.WritePrecondition{}, nil, err
	}
	switch state.Status {
	case dkvsindexer.KeyStateNeverSeen:
		return dkvsindexer.WritePrecondition{ExpectAbsent: true}, nil, nil
	case dkvsindexer.KeyStateActive:
		etag, err := chainhash.NewHashFromStr(strings.TrimSpace(state.ETag))
		if err != nil {
			return dkvsindexer.WritePrecondition{}, nil, dkvsindexer.ErrInvalidRecord
		}
		record, err := p.GetRecordDirect(key)
		if err != nil {
			return dkvsindexer.WritePrecondition{}, nil, err
		}
		return dkvsindexer.WritePrecondition{ExpectedHash: etag}, record, nil
	default:
		return dkvsindexer.WritePrecondition{}, nil, dkvsindexer.ErrInvalidRecord
	}
}

func (p *SatsNetDKVSClient) PutRecordCAS(record *swire.DKVSRecord, precondition dkvsindexer.WritePrecondition) (*swire.DKVSRecord, error) {
	if record == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	result, err := p.PutRecordBatchCAS([]dkvsindexer.CASMutation{{Record: record, Precondition: precondition}})
	if err != nil {
		return nil, err
	}
	if result == nil || len(result.Records) != 1 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return result.Records[0], nil
}

func (p *SatsNetDKVSClient) PutRecordBatchCAS(mutations []dkvsindexer.CASMutation) (*DKVSBatchCASResult, error) {
	if p == nil || len(mutations) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if p.manager != nil {
		return p.manager.putBatchCAS(p, mutations)
	}
	config, err := p.GetDKVSClientConfig()
	if err != nil {
		return nil, err
	}
	endpointID := strings.TrimSpace(config.EndpointID)
	if endpointID == "" {
		return nil, dkvsindexer.ErrStaleEndpoint
	}
	requestID, err := newDKVSRequestID()
	if err != nil {
		return nil, err
	}
	result, err := p.putRecordBatchCASRaw(mutations, endpointID, requestID)
	if err != nil {
		return nil, err
	}
	return writeResultToBatchResult(result), nil
}

type dkvsApplicationBaseResp = dkvscore.ApplicationResponse

type dkvsHTTPTransport interface {
	SendDKVSGet(path string, query map[string]string) ([]byte, error)
	SendDKVSPost(path string, body []byte) ([]byte, error)
}
type dkvsHTTPGetContextTransport interface {
	SendDKVSGetContext(ctx context.Context, path string, query map[string]string) ([]byte, error)
}
type dkvsHTTPPostContextTransport interface {
	SendDKVSPostContext(ctx context.Context, path string, body []byte) ([]byte, error)
}
type dkvsConfigProvider interface {
	DKVSClientConfig() (*dkvsindexer.ClientConfig, error)
}

func decodeDKVSApplicationResponse(raw []byte, out interface{}) error {
	return dkvscore.DecodeApplicationResponse(raw, out)
}
func (p *SatsNetDKVSClient) getDKVSApplication(path string, query map[string]string, out interface{}) error {
	return p.getDKVSApplicationContext(p.requestContext(), path, query, out)
}
func (p *SatsNetDKVSClient) getDKVSApplicationContext(ctx context.Context, path string, query map[string]string, out interface{}) error {
	if p == nil || p.RESTClient == nil || p.Http == nil {
		return fmt.Errorf("DKVS client is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var raw []byte
	var err error
	if transport, ok := p.Http.(dkvsHTTPGetContextTransport); ok {
		raw, err = transport.SendDKVSGetContext(ctx, path, query)
	} else if transport, ok := p.Http.(dkvsHTTPTransport); ok {
		raw, err = transport.SendDKVSGet(path, query)
	} else if client, ok := p.Http.(ContextHttpClient); ok {
		url := p.GetUrl(path)
		url.Query = query
		raw, err = client.SendGetRequestContext(ctx, url)
	} else {
		url := p.GetUrl(path)
		url.Query = query
		raw, err = p.sendGetRequest(url)
	}
	if err != nil {
		return err
	}
	return decodeDKVSApplicationResponse(raw, out)
}
func (p *SatsNetDKVSClient) postDKVSApplication(path string, req interface{}, out interface{}) error {
	return p.postDKVSApplicationContext(p.requestContext(), path, req, out)
}
func (p *SatsNetDKVSClient) postDKVSApplicationContext(ctx context.Context, path string, req interface{}, out interface{}) error {
	if p == nil || p.RESTClient == nil || p.Http == nil {
		return fmt.Errorf("DKVS client is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		return err
	}
	var raw []byte
	if transport, ok := p.Http.(dkvsHTTPPostContextTransport); ok {
		raw, err = transport.SendDKVSPostContext(ctx, path, encoded)
	} else if transport, ok := p.Http.(dkvsHTTPTransport); ok {
		raw, err = transport.SendDKVSPost(path, encoded)
	} else if client, ok := p.Http.(ContextHttpClient); ok {
		raw, err = client.SendPostRequestContext(ctx, p.GetUrl(path), encoded)
	} else {
		raw, err = p.Http.SendPostRequest(p.GetUrl(path), encoded)
	}
	if err != nil {
		return err
	}
	return decodeDKVSApplicationResponse(raw, out)
}

type dkvsApplicationRecordData struct {
	dkvsApplicationBaseResp
	Data *swire.DKVSRecord `json:"data,omitempty"`
	ETag string            `json:"etag,omitempty"`
}

func (p *SatsNetDKVSClient) GetRecordDirect(key string) (*swire.DKVSRecord, error) {
	return p.GetRecordDirectContext(p.requestContext(), key)
}
func (p *SatsNetDKVSClient) GetRecordDirectContext(ctx context.Context, key string) (*swire.DKVSRecord, error) {
	if _, err := dkvsindexer.ParseKey(key); err != nil {
		return nil, err
	}
	var resp dkvsApplicationRecordData
	if err := p.getDKVSApplicationContext(ctx, "/v3/dkvs/record", map[string]string{"key": key}, &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil || resp.Data.Key != key || resp.ETag != dkvsindexer.RecordHash(resp.Data).String() {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	// ETag is not a substitute for author-signature verification.
	if err := dkvsindexer.VerifyRecordForClient(resp.Data, dkvsindexer.RecordVerificationOptions{ExpectedKey: key}); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

type dkvsKeyStateData struct {
	dkvsApplicationBaseResp
	Data *dkvsindexer.DKVSKeyState `json:"data,omitempty"`
}

func (p *SatsNetDKVSClient) GetKeyState(key string) (*dkvsindexer.DKVSKeyState, error) {
	return p.getKeyStateContext(p.requestContext(), key)
}
func (p *SatsNetDKVSClient) getKeyStateContext(ctx context.Context, key string) (*dkvsindexer.DKVSKeyState, error) {
	if _, err := dkvsindexer.ParseKey(key); err != nil {
		return nil, err
	}
	var resp dkvsKeyStateData
	if err := p.getDKVSApplicationContext(ctx, "/v3/dkvs/key-state", map[string]string{"key": key}, &resp); err != nil {
		return nil, err
	}
	state := resp.Data
	if state == nil || state.Key != key {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	switch state.Status {
	case dkvsindexer.KeyStateNeverSeen:
		if state.Seq != 0 || state.ETag != "" || state.Record != nil || state.ExpiryHeight != 0 || state.StorageMode != "" {
			return nil, dkvsindexer.ErrInvalidRecord
		}
	case dkvsindexer.KeyStateActive:
		if state.Seq == 0 || len(state.ETag) != 64 {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		if _, err := chainhash.NewHashFromStr(state.ETag); err != nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		if state.Record != nil && (state.Record.Key != key || state.Record.Seq != state.Seq ||
			dkvsindexer.IsTombstone(state.Record.Flags) || dkvsindexer.RecordHash(state.Record).String() != state.ETag) {
			return nil, dkvsindexer.ErrInvalidRecord
		}
	default:
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if state.Record != nil {
		if err := dkvsindexer.VerifyRecordForClient(state.Record, dkvsindexer.RecordVerificationOptions{ExpectedKey: key}); err != nil {
			return nil, err
		}
		if state.ExpiryHeight != dkvsindexer.RecordExpiryHeight(state.Record) {
			return nil, dkvsindexer.ErrInvalidRecord
		}
	}
	return state, nil
}

type dkvsPrefixReadData struct {
	dkvsApplicationBaseResp
	Data *dkvsindexer.PrefixReadResult `json:"data,omitempty"`
}

func (p *SatsNetDKVSClient) ReadPrefixContext(ctx context.Context, prefix string) (*dkvsindexer.PrefixReadResult, error) {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	if _, err := dkvsindexer.ParsePrefix(prefix); err != nil {
		return nil, err
	}
	if prefix == "" {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	var resp dkvsPrefixReadData
	if err := p.postDKVSApplicationContext(ctx, "/v3/dkvs/prefixes/read", struct {
		Prefix string `json:"prefix"`
	}{Prefix: prefix}, &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil || resp.Data.Prefix != prefix || strings.TrimSpace(resp.Data.EndpointID) == "" {
		return nil, dkvsindexer.ErrInvalidSnapshot
	}
	if err := dkvscore.VerifyPrefixPayload(prefix, resp.Data.ViewHeight, resp.Data.Records, resp.Data.KeyStates); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

type dkvsWriteResultData struct {
	dkvsApplicationBaseResp
	Data *dkvsindexer.WriteResult `json:"data,omitempty"`
}

// Passing an explicit authorization (including nil for the binding exception)
// is a retry of a captured request. No fresh context is fetched in that case.
func (p *SatsNetDKVSClient) putRecordBatchCASRaw(mutations []dkvsindexer.CASMutation, endpointID, requestID string,
	authorizations ...*dkvsindexer.WalletWriteAuthorization) (*dkvsindexer.WriteResult, error) {
	req, err := dkvscore.BuildBatchCASRequest(mutations, endpointID, requestID)
	if err != nil {
		return nil, err
	}
	if len(authorizations) > 1 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if len(authorizations) == 1 {
		req.Authorization = dkvsindexer.CloneWalletWriteAuthorization(authorizations[0])
	} else {
		// Manager-attached clients resolve the pinned account-management root
		// inside prepareWriteAuthorization; request-scoped clones may instead
		// carry an explicit signer. The account-mapping bootstrap exception
		// intentionally returns nil authorization.
		req.Authorization, err = p.prepareWriteAuthorization(mutations, endpointID, requestID)
		if err != nil {
			return nil, err
		}
	}
	// A raw client has neither a manager-owned root nor an explicit signer, so
	// prepareWriteAuthorization fails before any unauthenticated business write.
	var resp dkvsWriteResultData
	if err := p.postDKVSApplication("/v3/dkvs/records/batch-cas", req, &resp); err != nil {
		return nil, fmt.Errorf("submit DKVS batch: %w", err)
	}
	if err := dkvscore.VerifyWriteResult(mutations, requestID, resp.Data); err != nil {
		return nil, fmt.Errorf("verify DKVS batch response: %w", err)
	}
	return resp.Data, nil
}

func (p *SatsNetDKVSClient) putRecordBatchCASWithOrigin(mutations []dkvsindexer.CASMutation, origin dkvsOutboxOrigin) (*dkvsindexer.WriteResult, error) {
	if p == nil || len(mutations) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	config, err := p.GetDKVSClientConfig()
	if err != nil {
		return nil, err
	}
	endpointID := strings.TrimSpace(config.EndpointID)
	if endpointID == "" {
		return nil, dkvsindexer.ErrStaleEndpoint
	}
	if p.manager == nil || p.manager.owner == nil || p.manager.owner.db == nil || strings.TrimSpace(p.replicaNamespace) == "" {
		requestID, err := newDKVSRequestID()
		if err != nil {
			return nil, err
		}
		return p.putRecordBatchCASRaw(mutations, endpointID, requestID)
	}
	store := newDKVSReplicaStore(p.manager.owner.db)
	entry, err := newDKVSBatchOutboxEntryFinal(p.replicaNamespace, mutations, endpointID, origin)
	if err != nil {
		return nil, err
	}
	var result *dkvsindexer.WriteResult
	err = p.manager.runTransport(func() error {
		entry.Authorization, err = p.prepareWriteAuthorization(mutations, endpointID, entry.RequestID)
		if err != nil {
			return err
		}
		if queueErr := store.QueueOutbox(entry); queueErr != nil {
			return fmt.Errorf("persist DKVS batch outbox: %w", queueErr)
		}
		if inflightErr := store.UpdateOutboxState(entry, DKVSOutboxInflight, nil); inflightErr != nil {
			return fmt.Errorf("mark DKVS batch inflight: %w", inflightErr)
		}
		result, err = p.putRecordBatchCASRaw(mutations, endpointID, entry.RequestID, entry.Authorization)
		if err != nil {
			if p.manager.owner != nil {
				err = p.manager.owner.accountAutopaySubmissionFailure(mutations, err)
			}
			if errors.Is(err, ErrAccountAutopayFundingRequired) {
				_ = store.UpdateOutboxState(entry, DKVSOutboxPending, err)
				return err
			}
			_ = markDKVSOutboxSubmissionFailure(store, entry, err)
			return err
		}
		if ackErr := store.ApplyWriteResultAndAck(entry, result); ackErr != nil {
			_ = markDKVSOutboxSubmissionFailure(store, entry, ackErr)
			return fmt.Errorf("complete DKVS request: %w", ackErr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func IsDKVSErrorCode(err error, code dkvsindexer.ErrorCode) bool {
	return dkvscore.IsErrorCode(err, code)
}
