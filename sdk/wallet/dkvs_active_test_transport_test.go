package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

// Deterministic transport support for unit tests that directly inspect the
// in-memory endpoint. Real HTTP/indexer behavior is exercised in sdk/e2e.
func (h *rgb11MemoryDKVSHTTP) activePage(request dkvs.ActiveSyncRequest) (*dkvs.ActivePage, error) {
	scope, err := dkvs.NormalizeActiveScope(request.Scope)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if request.EndpointID != h.endpointID {
		return nil, dkvs.ErrEndpointMismatch
	}
	height := uint64(1)
	if h.bestHeight > 0 {
		height = uint64(h.bestHeight)
	}
	if request.Cursor != nil {
		height = request.Cursor.Meta.ViewHeight
	}
	var current []*wire.DKVSRecord
	for key, record := range h.records {
		if dkvs.ActiveScopeMatches(scope, key) && !dkvs.IsExpired(record, height) {
			current = append(current, cloneRGB11DKVSRecord(record))
		}
	}
	root, err := dkvs.ActiveRecordsRoot(current)
	if err != nil {
		return nil, err
	}
	meta := dkvs.ActiveMeta{EndpointID: h.endpointID, Scope: scope, Generation: h.generations[scope.Prefix], ViewHeight: height, Root: root}
	if request.Cursor != nil {
		cursor := request.Cursor
		if cursor.Meta.EndpointID != meta.EndpointID || !dkvs.SameActiveScope(cursor.Meta.Scope, scope) || cursor.Full != request.Full || cursor.After != request.After {
			return nil, dkvs.ErrInvalidSnapshot
		}
		if cursor.Meta.Generation != meta.Generation || cursor.Meta.Root != meta.Root {
			return nil, dkvs.ErrStaleGeneration
		}
	}
	if !request.Full && request.After > meta.Generation {
		return nil, dkvs.ErrStaleGeneration
	}
	if request.Full {
		h.snapshotCalls++
	} else {
		h.deltaCalls++
	}
	sort.Slice(current, func(a, b int) bool {
		if !request.Full && h.changedAt[current[a].Key] != h.changedAt[current[b].Key] {
			return h.changedAt[current[a].Key] < h.changedAt[current[b].Key]
		}
		return current[a].Key < current[b].Key
	})
	limit := request.PageSize
	if limit <= 0 || limit > dkvs.ActiveSyncPageRecords {
		limit = dkvs.ActiveSyncPageRecords
	}
	page := &dkvs.ActivePage{Meta: meta, Records: make([]*wire.DKVSRecord, 0), Complete: true}
	total := 0
	for _, record := range current {
		generation := h.changedAt[record.Key]
		if !request.Full && generation <= request.After {
			continue
		}
		if cursor := request.Cursor; cursor != nil {
			if request.Full && record.Key <= cursor.LastKey {
				continue
			}
			if !request.Full && (generation < cursor.LastGeneration || (generation == cursor.LastGeneration && record.Key <= cursor.LastKey)) {
				continue
			}
		}
		size := dkvs.RecordSize(record)
		if len(page.Records) >= limit || size > dkvs.ActiveSyncPageBytes-total {
			if len(page.Records) == 0 {
				return nil, dkvs.ErrRecordTooLarge
			}
			last := page.Records[len(page.Records)-1]
			page.Complete = false
			page.Next = &dkvs.ActivePageCursor{Meta: meta, After: request.After, Full: request.Full, LastKey: last.Key}
			if !request.Full {
				page.Next.LastGeneration = h.changedAt[last.Key]
			}
			break
		}
		total += size
		page.Records = append(page.Records, record)
	}
	return page, nil
}

func testActiveResponse(value any, err error) ([]byte, error) {
	if err != nil {
		return rgb11DKVSResponse(-1, err.Error(), nil, string(dkvs.ErrorCodeOf(err)), 0)
	}
	return rgb11DKVSResponse(0, "ok", value, "", 0)
}

func (h *rgb11MemoryDKVSHTTP) sendActiveRequest(ctx context.Context, path string, body []byte) ([]byte, error) {
	if path == "/v3/dkvs/active/sync" {
		var request dkvs.ActiveSyncRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		page, err := h.activePage(request)
		return testActiveResponse(page, err)
	}
	var request dkvs.ActiveWatchRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, err
	}
	if len(request.Scopes) == 0 {
		return testActiveResponse(nil, dkvs.ErrInvalidRecord)
	}
	ctx, cancel := context.WithTimeout(ctx, dkvs.ActiveWatchTimeout)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, item := range request.Scopes {
			// Unit tests sometimes mutate fixture maps directly. Sampling here
			// observes their state without inventing a mutation queue or log.
			h.mu.Lock()
			endpoint, generation := h.endpointID, h.generations[item.Scope.Prefix]
			height := uint64(1)
			if h.bestHeight > 0 {
				height = uint64(h.bestHeight)
			}
			var records []*wire.DKVSRecord
			for key, r := range h.records {
				if dkvs.ActiveScopeMatches(item.Scope, key) && !dkvs.IsExpired(r, height) {
					records = append(records, cloneRGB11DKVSRecord(r))
				}
			}
			h.mu.Unlock()
			if endpoint != request.EndpointID {
				return testActiveResponse(nil, dkvs.ErrEndpointMismatch)
			}
			root, err := dkvs.ActiveRecordsRoot(records)
			if err != nil {
				return nil, err
			}
			if root == item.Root && generation >= item.Generation {
				continue
			}
			page, err := h.activePage(dkvs.ActiveSyncRequest{Scope: item.Scope, EndpointID: endpoint, After: item.Generation, Full: item.Generation > generation})
			return testActiveResponse(&dkvs.ActiveWatchResult{Page: page}, err)
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return testActiveResponse(&dkvs.ActiveWatchResult{}, nil)
			}
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
