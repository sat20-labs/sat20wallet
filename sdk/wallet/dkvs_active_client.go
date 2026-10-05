package wallet

import (
	"context"
	"errors"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

type dkvsActivePageResponse struct {
	dkvsApplicationBaseResp
	Data *dkvs.ActivePage `json:"data"`
}

type dkvsActiveWatchResponse struct {
	dkvsApplicationBaseResp
	Data *dkvs.ActiveWatchResult `json:"data"`
}

func (p *SatsNetDKVSClient) GetActivePage(ctx context.Context, request dkvs.ActiveSyncRequest) (*dkvs.ActivePage, error) {
	scope, err := dkvs.NormalizeActiveScope(request.Scope)
	if err != nil {
		return nil, err
	}
	request.Scope = scope
	var response dkvsActivePageResponse
	if err := p.postDKVSApplicationContext(ctx, "/v3/dkvs/active/sync", request, &response); err != nil {
		return nil, err
	}
	if err := validateDKVSActivePage(response.Data, request.EndpointID, scope); err != nil {
		return nil, err
	}
	return response.Data, nil
}

func validateDKVSActivePage(page *dkvs.ActivePage, endpoint string, scope dkvs.ActiveScope) error {
	if page == nil || page.Meta.EndpointID != endpoint || !dkvs.SameActiveScope(page.Meta.Scope, scope) || page.Complete == (page.Next != nil) {
		return dkvs.ErrInvalidSnapshot
	}
	if len(page.Records) > dkvs.ActiveSyncPageRecords {
		return dkvs.ErrBatchTooLarge
	}
	total := 0
	for _, record := range page.Records {
		if record == nil || dkvs.IsTombstone(record.Flags) || !dkvs.ActiveScopeMatches(scope, record.Key) {
			return dkvs.ErrInvalidSnapshot
		}
		size := dkvs.RecordSize(record)
		if size > dkvs.ActiveSyncPageBytes-total {
			return dkvs.ErrBatchTooLarge
		}
		total += size
		if err := dkvs.VerifyRecordForClient(record, dkvs.RecordVerificationOptions{ExpectedKey: record.Key, Height: page.Meta.ViewHeight}); err != nil {
			return err
		}
	}
	if !page.Complete && (len(page.Records) == 0 || page.Next.Meta.EndpointID != endpoint ||
		!dkvs.SameActiveScope(page.Next.Meta.Scope, scope) || page.Next.Meta.Root != page.Meta.Root ||
		page.Next.Meta.Generation != page.Meta.Generation || page.Next.Meta.ViewHeight != page.Meta.ViewHeight) {
		return dkvs.ErrInvalidSnapshot
	}
	return nil
}

func (p *SatsNetDKVSClient) WatchActive(ctx context.Context, request dkvs.ActiveWatchRequest) (*dkvs.ActiveWatchResult, error) {
	if len(request.Scopes) == 0 || len(request.Scopes) > dkvs.MaxPrefixesPerTerminal {
		return nil, dkvs.ErrTooManySubscriptions
	}
	request.Scopes = append([]dkvs.ActiveWatchScope(nil), request.Scopes...)
	for n := range request.Scopes {
		scope, err := dkvs.NormalizeActiveScope(request.Scopes[n].Scope)
		if err != nil {
			return nil, err
		}
		request.Scopes[n].Scope = scope
	}
	var response dkvsActiveWatchResponse
	if err := p.postDKVSApplicationContext(ctx, "/v3/dkvs/active/watch", request, &response); err != nil {
		return nil, err
	}
	if response.Data == nil {
		return nil, dkvs.ErrInvalidSnapshot
	}
	if response.Data.Page == nil {
		return response.Data, nil
	}
	for _, item := range request.Scopes {
		if dkvs.SameActiveScope(item.Scope, response.Data.Page.Meta.Scope) {
			if err := validateDKVSActivePage(response.Data.Page, request.EndpointID, item.Scope); err != nil {
				return nil, err
			}
			return response.Data, nil
		}
	}
	return nil, dkvs.ErrInvalidSnapshot
}

// Page collection never changes confirmed data. A single complete boundary is
// validated before installation, including byte budget and progress checks.
func (p *SatsNetDKVSClient) collectActivePages(ctx context.Context, request dkvs.ActiveSyncRequest,
	first *dkvs.ActivePage) (dkvs.ActiveMeta, []*wire.DKVSRecord, error) {
	var meta dkvs.ActiveMeta
	var records []*wire.DKVSRecord
	seen := make(map[string]struct{})
	total := 0
	page := first
	for n := 0; ; n++ {
		if ctx != nil && ctx.Err() != nil {
			return meta, nil, ctx.Err()
		}
		if page == nil {
			var err error
			page, err = p.GetActivePage(ctx, request)
			if err != nil {
				return meta, nil, err
			}
		}
		if err := validateDKVSActivePage(page, request.EndpointID, request.Scope); err != nil {
			return meta, nil, err
		}
		if n == 0 {
			meta = page.Meta
		} else if page.Meta.Generation != meta.Generation || page.Meta.Root != meta.Root || page.Meta.ViewHeight != meta.ViewHeight {
			return meta, nil, dkvs.ErrStaleGeneration
		}
		for _, record := range page.Records {
			if _, exists := seen[record.Key]; exists {
				return meta, nil, dkvs.ErrInvalidSnapshot
			}
			seen[record.Key] = struct{}{}
			size := dkvs.RecordSize(record)
			if size > dkvscore.MaxActiveReplicaBytes-total {
				return meta, nil, dkvs.ErrBatchTooLarge
			}
			total += size
			records = append(records, record)
		}
		if page.Complete {
			return meta, records, nil
		}
		if page.Next.After != request.After || page.Next.Full != request.Full {
			return meta, nil, dkvs.ErrInvalidSnapshot
		}
		if request.Cursor != nil && page.Next.LastKey == request.Cursor.LastKey && page.Next.LastGeneration == request.Cursor.LastGeneration {
			return meta, nil, dkvs.ErrInvalidSnapshot
		}
		request.Cursor = page.Next
		page = nil
	}
}

func (p *SatsNetDKVSClient) SyncActiveScope(ctx context.Context, store *dkvscore.ReplicaStore,
	namespace string, scope dkvs.ActiveScope, force bool) ([]string, error) {
	return p.syncActiveScope(ctx, store, namespace, scope, force, nil)
}

func (p *SatsNetDKVSClient) syncActiveScope(ctx context.Context, store *dkvscore.ReplicaStore,
	namespace string, scope dkvs.ActiveScope, force bool, first *dkvs.ActivePage) ([]string, error) {
	if store == nil {
		return nil, dkvs.ErrInvalidRecord
	}
	scope, err := dkvs.NormalizeActiveScope(scope)
	if err != nil {
		return nil, err
	}
	config, err := p.GetDKVSClientConfig()
	if err != nil {
		return nil, err
	}
	if config == nil || config.EndpointID == "" {
		return nil, dkvs.ErrStaleEndpoint
	}
	for attempt := 0; attempt < 3; attempt++ {
		baseline, err := store.ActiveBaseline(namespace, scope)
		if err != nil {
			return nil, err
		}
		previous, err := store.LoadActiveMeta(namespace, scope)
		if err != nil && !errors.Is(err, indexercommon.ErrKeyNotFound) {
			return nil, err
		}
		if previous != nil && previous.EndpointID != config.EndpointID {
			return nil, dkvs.ErrEndpointMismatch
		}
		request := dkvs.ActiveSyncRequest{Scope: scope, EndpointID: config.EndpointID, Full: force || previous == nil}
		if previous != nil {
			request.After = previous.Generation
		}
		if request.Full {
			first = nil
		}
		usingWatchPage := first != nil
		meta, records, err := p.collectActivePages(ctx, request, first)
		first = nil
		if errors.Is(err, dkvs.ErrStaleGeneration) {
			continue
		}
		if err != nil {
			return nil, err
		}
		changed, err := store.InstallActiveState(namespace, baseline, meta, records, request.Full)
		if usingWatchPage && errors.Is(err, dkvs.ErrStaleEndpoint) {
			// A completed Watch can arrive after a foreground ACK. Reject that
			// page, then fetch current state through the existing bounded loop.
			// A fresh response still has to pass the source/ACK checks.
			continue
		}
		if errors.Is(err, dkvs.ErrPathDiverged) && !request.Full {
			request.Full, request.Cursor = true, nil
			meta, records, err = p.collectActivePages(ctx, request, nil)
			if err == nil {
				changed, err = store.InstallActiveState(namespace, baseline, meta, records, true)
			}
		}
		if errors.Is(err, dkvs.ErrConcurrentUpdate) || errors.Is(err, dkvs.ErrStaleGeneration) {
			continue
		}
		return changed, err
	}
	return nil, dkvs.ErrConcurrentUpdate
}

func NewDKVSDeleteCommand(signer common.Wallet, current *wire.DKVSRecord, issueHeight uint64) (*wire.DKVSRecord, error) {
	command, err := dkvs.DeleteCommand(current, issueHeight)
	if err != nil {
		return nil, err
	}
	if err := SignDKVSRecord(signer, command); err != nil {
		return nil, err
	}
	return command, nil
}

func (p *SatsNetDKVSClient) DeleteCurrentRecord(signer common.Wallet, key string, issueHeight uint64) (*wire.DKVSRecord, error) {
	if p != nil && p.manager != nil {
		writer := p.WithWriteSigner(signer)
		values, err := p.manager.updateValues(writer, []string{key}, func(current map[string]*dkvsValue, _ map[string]uint64) ([]dkvsValueMutation, error) {
			if current[key] == nil || current[key].record == nil {
				return nil, ErrDKVSRecordNotFound
			}
			return []dkvsValueMutation{{Key: key, Owner: writer.writeSigner, Tombstone: true, IssueHeight: issueHeight}}, nil
		})
		if err != nil {
			return nil, err
		}
		if len(values) != 1 || values[0] == nil || values[0].record == nil {
			return nil, dkvs.ErrInvalidRecord
		}
		return values[0].record, nil
	}
	current, err := p.GetRecordDirect(key)
	if err != nil {
		return nil, err
	}
	if issueHeight == 0 {
		issueHeight, err = p.GetBestHeight()
		if err != nil {
			return nil, err
		}
	}
	command, err := NewDKVSDeleteCommand(signer, current, issueHeight)
	if err != nil {
		return nil, err
	}
	target := dkvs.RecordHash(current)
	return p.WithWriteSigner(signer).PutRecordCAS(command, dkvs.WritePrecondition{ExpectedHash: &target})
}
