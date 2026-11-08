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

func (p *SatsNetDKVSClient) putRecordBatchCASRaw(mutations []dkvsindexer.CASMutation, endpointID, requestID string,
	authorizations ...*dkvsindexer.WalletWriteAuthorization) (*dkvsindexer.WriteResult, error) {
	if len(authorizations) > 1 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	var authorization *dkvsindexer.WalletWriteAuthorization
	if len(authorizations) == 1 {
		authorization = dkvsindexer.CloneWalletWriteAuthorization(authorizations[0])
	} else {
		var err error
		authorization, err = p.prepareWriteAuthorization(mutations, endpointID, requestID)
		if err != nil {
			return nil, err
		}
	}
	return p.submitDKVSBatch(mutations, endpointID, requestID, authorization, nil)
}

func (p *SatsNetDKVSClient) sendDKVSBatch(mutations []dkvsindexer.CASMutation, endpointID, requestID string,
	authorization *dkvsindexer.WalletWriteAuthorization) (*dkvsindexer.WriteResult, error) {
	req, err := dkvscore.BuildBatchCASRequest(mutations, endpointID, requestID)
	if err != nil {
		return nil, err
	}
	req.Authorization = dkvsindexer.CloneWalletWriteAuthorization(authorization)
	var resp dkvsWriteResultData
	if err := p.postDKVSApplication("/v3/dkvs/records/batch-cas", req, &resp); err != nil {
		return nil, fmt.Errorf("submit DKVS batch: %w", err)
	}
	if err := dkvscore.VerifyWriteResult(mutations, requestID, resp.Data); err != nil {
		return nil, &DKVSRequestPendingError{RequestID: requestID, Cause: fmt.Errorf("verify DKVS batch response: %w", err)}
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
	err = func() error {
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
		result, err = p.submitDKVSBatch(mutations, endpointID, entry.RequestID, entry.Authorization, entry)
		if err != nil {
			if p.manager.owner != nil {
				err = p.manager.owner.accountAutopaySubmissionFailure(mutations, err)
			}
			if errors.Is(err, ErrAccountAutopayFundingRequired) {
				state := DKVSOutboxPending
				if knownDKVSRefusal(err) {
					state = DKVSOutboxConflict
				}
				_ = store.UpdateOutboxState(entry, state, err)
				return err
			}
			if markErr := markDKVSOutboxSubmissionFailure(store, entry, err); markErr != nil {
				return errors.Join(err, markErr)
			}
			// This fresh, explicitly rejected business intent did not commit.
			// End it so a later user action for the same key can be authorized.
			// Replay of older pending requests and durable ACKs never comes here.
			if origin.Domain == "" && (IsDKVSErrorCode(err, dkvsindexer.ErrorCodePermissionDenied) || isDKVSRebaseError(err)) {
				if cleanupErr := store.DiscardOutbox(entry); cleanupErr != nil {
					return errors.Join(err, cleanupErr)
				}
			}
			if isDKVSNetworkFailure(err) {
				// The request may have committed before the response was lost.
				// Its signed outbox intent is durable; replay that same ID.
				return &DKVSRequestPendingError{RequestID: entry.RequestID, Cause: err}
			}
			return err
		}
		if ackErr := store.ApplyWriteResultAndAck(entry, result); ackErr != nil {
			var persistence *dkvscore.AckPersistenceError
			return &DKVSRequestPendingError{RequestID: entry.RequestID, Committed: errors.As(ackErr, &persistence), Cause: ackErr}
		}
		if err := p.manager.finishAcknowledgedWrite(p, store, entry); err != nil {
			return &DKVSRequestPendingError{RequestID: entry.RequestID, Committed: true, Durable: true, Cause: err}
		}
		return nil
	}()
	if err != nil {
		return nil, err
	}
	return result, nil
}

func IsDKVSErrorCode(err error, code dkvsindexer.ErrorCode) bool {
	return dkvscore.IsErrorCode(err, code)
}

// Called only while the existing manager transport coordinator is owned.
func (m *dkvsManager) finishAcknowledgedWrite(client *SatsNetDKVSClient, store *dkvsReplicaStore, entry *DKVSBatchOutboxEntry) error {
	if entry.State != DKVSOutboxAcknowledged {
		return dkvsindexer.ErrInvalidRecord
	}
	if entry.OriginDomain == accountKVOutboxDomain {
		if err := m.owner.finishAccountKVACK(client, entry); err != nil {
			return accountACKCleanup(entry, err)
		}
		if err := store.DiscardOutbox(entry); err != nil {
			return accountACKCleanup(entry, err)
		}
		return nil
	}
	mutations, err := entry.DecodeMutations()
	if err != nil {
		return err
	}
	var prefixes []string
	for _, mutation := range mutations {
		prefix, _, err := dkvsManagedPathForKey(mutation.Record.Key)
		if err != nil {
			return err
		}
		prefixes = append(prefixes, prefix)
	}
	if len(prefixes) > 0 {
		prefixes, err = normalizeWalletSubscriptionPrefixes(prefixes)
		if err != nil {
			return err
		}
		changed, err := m.syncManagedPrefixes(client, store, prefixes, false)
		if err != nil {
			return err
		}
		m.enqueueNotifications(changed)
	}
	{
		if err := store.DiscardOutbox(entry); err != nil {
			// The exact ACK is already durable. A one-off local deletion failure
			// must wake bounded reconciliation even when no remote watch changes.
			return &DKVSRequestPendingError{RequestID: entry.RequestID, Committed: true, Durable: true, Cause: err}
		}
	}
	if err := m.grantReconciledWriteSession(client, store); err != nil {
		return err
	}

	return nil
}

// DKVSRequestPendingError prevents a known remote commit being retried as a
// fresh business write when only local installation failed.
type DKVSRequestPendingError struct {
	RequestID string
	Committed bool
	Durable   bool // Acknowledgement persisted; recovery installs without resending.
	Cause     error
}

func (e *DKVSRequestPendingError) Error() string {
	return fmt.Sprintf("DKVS request %s committed=%t durable=%t; local reconciliation pending: %v", e.RequestID, e.Committed, e.Durable, e.Cause)
}
func (e *DKVSRequestPendingError) Unwrap() error { return e.Cause }
