package fakeindexer

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

// The existing in-memory L2 fixture serves endpoint-local account data. It
// tracks only each current key's last generation, never a mutation history.
func (p *Client) fakeActivePage(request dkvs.ActiveSyncRequest) (*dkvs.ActivePage, error) {
	scope, err := dkvs.NormalizeActiveScope(request.Scope)
	if err != nil {
		return nil, err
	}
	if scope.Network {
		return nil, dkvs.ErrFreeLocalNotRelayable
	}
	if request.EndpointID != DKVSEndpointID {
		return nil, dkvs.ErrEndpointMismatch
	}
	p.Network.Mutex.RLock()
	defer p.Network.Mutex.RUnlock()
	height := p.Network.fakeDKVSViewHeightLocked()
	if p.Network.Height > 1 && uint64(p.Network.Height-1) > height {
		height = uint64(p.Network.Height - 1)
	}
	if cursor := request.Cursor; cursor != nil {
		if cursor.Meta.EndpointID != request.EndpointID || !dkvs.SameActiveScope(cursor.Meta.Scope, scope) ||
			cursor.After != request.After || cursor.Full != request.Full || cursor.LastKey == "" ||
			!dkvs.ActiveScopeMatches(scope, cursor.LastKey) || cursor.Meta.ViewHeight > height {
			return nil, dkvs.ErrInvalidSnapshot
		}
		height = cursor.Meta.ViewHeight
	}
	var records []*wire.DKVSRecord
	for key, record := range p.Network.DkvsRecords {
		if dkvs.ActiveScopeMatches(scope, key) && !dkvs.IsExpired(record, height) {
			records = append(records, cloneFakeDKVSRecord(record))
		}
	}
	root, err := dkvs.ActiveRecordsRoot(records)
	if err != nil {
		return nil, err
	}
	meta := dkvs.ActiveMeta{EndpointID: DKVSEndpointID, Scope: scope,
		Generation: p.Network.DkvsGenerations[scope.Prefix], ViewHeight: height, Root: root}
	if cursor := request.Cursor; cursor != nil && (cursor.Meta.Generation != meta.Generation || cursor.Meta.Root != meta.Root) {
		return nil, dkvs.ErrStaleGeneration
	}
	if !request.Full && request.After > meta.Generation {
		return nil, dkvs.ErrStaleGeneration
	}
	sort.Slice(records, func(a, b int) bool {
		if !request.Full && p.Network.DkvsChangedAt[records[a].Key] != p.Network.DkvsChangedAt[records[b].Key] {
			return p.Network.DkvsChangedAt[records[a].Key] < p.Network.DkvsChangedAt[records[b].Key]
		}
		return records[a].Key < records[b].Key
	})
	limit := request.PageSize
	if limit <= 0 || limit > dkvs.ActiveSyncPageRecords {
		limit = dkvs.ActiveSyncPageRecords
	}
	page := &dkvs.ActivePage{Meta: meta, Records: make([]*wire.DKVSRecord, 0), Complete: true}
	total := 0
	for _, record := range records {
		generation := p.Network.DkvsChangedAt[record.Key]
		if !request.Full && generation <= request.After {
			continue
		}
		if cursor := request.Cursor; cursor != nil {
			if request.Full && record.Key <= cursor.LastKey || !request.Full &&
				(generation < cursor.LastGeneration || generation == cursor.LastGeneration && record.Key <= cursor.LastKey) {
				continue
			}
		}
		size := dkvs.RecordSize(record)
		if size > dkvs.ActiveSyncPageBytes {
			return nil, dkvs.ErrRecordTooLarge
		}
		if len(page.Records) >= limit || size > dkvs.ActiveSyncPageBytes-total {
			last := page.Records[len(page.Records)-1]
			page.Complete = false
			page.Next = &dkvs.ActivePageCursor{Meta: meta, After: request.After, Full: request.Full, LastKey: last.Key}
			if !request.Full {
				page.Next.LastGeneration = p.Network.DkvsChangedAt[last.Key]
			}
			break
		}
		total += size
		page.Records = append(page.Records, record)
	}
	return page, nil
}

func (p *Client) SendGetRequestContext(ctx context.Context, url *wallet.URL) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p.SendGetRequest(url)
}

func (p *Client) SendPostRequestContext(ctx context.Context, url *wallet.URL, body []byte) ([]byte, error) {
	return p.SendDKVSPostContext(ctx, url.Path, body)
}

func (p *Client) SendDKVSPostContext(ctx context.Context, path string, body []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if path == "/v3/dkvs/active/sync" && p.Network.Name == "satoshinet" {
		var request dkvs.ActiveSyncRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		page, err := p.fakeActivePage(request)
		if err != nil {
			return fakeDKVSErrorResponse(err)
		}
		return fakeDKVSResponse(0, "ok", page)
	}
	if path != "/v3/dkvs/active/watch" || p.Network.Name != "satoshinet" {
		return p.SendDKVSPost(path, body)
	}
	var request dkvs.ActiveWatchRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, err
	}
	if len(request.Scopes) == 0 || len(request.Scopes) > dkvs.MaxPrefixesPerTerminal {
		return fakeDKVSErrorResponse(dkvs.ErrTooManySubscriptions)
	}
	ctx, cancel := context.WithTimeout(ctx, dkvs.ActiveWatchTimeout)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, item := range request.Scopes {
			page, err := p.fakeActivePage(dkvs.ActiveSyncRequest{Scope: item.Scope, EndpointID: request.EndpointID, Full: true})
			if err != nil {
				return fakeDKVSErrorResponse(err)
			}
			if page.Meta.Root != item.Root || page.Meta.Generation < item.Generation {
				page, err = p.fakeActivePage(dkvs.ActiveSyncRequest{Scope: item.Scope, EndpointID: request.EndpointID,
					After: item.Generation, Full: item.Generation > page.Meta.Generation})
				if err != nil {
					return fakeDKVSErrorResponse(err)
				}
				return fakeDKVSResponse(0, "ok", &dkvs.ActiveWatchResult{Page: page})
			}
		}
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				return fakeDKVSResponse(0, "ok", &dkvs.ActiveWatchResult{})
			}
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
