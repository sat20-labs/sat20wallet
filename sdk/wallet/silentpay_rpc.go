package wallet

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

)

// These wire DTOs intentionally do not import indexer/base: that repository's
// SP extension is developed separately, and its unmerged branch must not be a
// compile-time dependency of this wallet PR.
type SPBaseAnchor struct {
	Height int `json:"height"`
	BlockHash string `json:"block_hash"`
}

type SPIndexerStatus struct {
	Network string `json:"network"`
	SchemaVersion int `json:"schema_version"`
	CoverageStartHeight int `json:"coverage_start_height"`
	BaseAnchor SPBaseAnchor `json:"base_anchor"`
	DataReady bool `json:"data_ready"`
}

type SPIndexerSnapshot struct {
	SnapshotID string `json:"snapshot_id"`
	Anchor SPBaseAnchor `json:"anchor"`
	CoverageStartHeight int `json:"coverage_start_height"`
	SchemaVersion int `json:"schema_version"`
}

type SPIndexerTransaction struct {
	TxIndex uint32 `json:"tx_index"`
	Tweak []byte `json:"tweak"`
	P2TROutputs []SPIndexerOutput `json:"p2tr_outputs"`
	UnspentBitmap []byte `json:"unspent_bitmap,omitempty"`
}
type SPIndexerOutput struct {
	Vout uint32 `json:"vout"`
	OutputKey []byte `json:"output_key"`
}
type SPIndexerBlock struct {
	Height int `json:"height"`
	BlockHash string `json:"block_hash"`
	Transactions []SPIndexerTransaction `json:"transactions"`
}
type SPIndexerPage struct {
	SnapshotID string `json:"snapshot_id,omitempty"`
	Anchor SPBaseAnchor `json:"anchor"`
	Blocks []SPIndexerBlock `json:"blocks"`
	NextCursor string `json:"next_cursor,omitempty"`
	Done bool `json:"done"`
}
type SPIndexerPosition struct {
	Height int `json:"height"`
	BlockHash string `json:"block_hash"`
	TxIndex uint32 `json:"tx_index"`
	Vout uint32 `json:"vout,omitempty"`
	ExpectedTxID string `json:"expected_txid,omitempty"`
}
type SPIndexerPositionResult struct {
	SPIndexerPosition
	TxID string `json:"txid,omitempty"`
	State string `json:"state,omitempty"`
	Error string `json:"error,omitempty"`
}

type SPScanRequest struct {
	Mode string
	SnapshotID string
	FromHeight int
	ToHeight int
	AnchorHash string
	Cursor string
	Limit int
}

// SPIndexerRPC is opt-in. Existing fake IndexerRPCClient implementations do
// not need to add unused methods; production IndexerClient implements it.
type SPIndexerRPC interface {
	SPStatus(context.Context) (SPIndexerStatus, error)
	SPSnapshot(context.Context) (SPIndexerSnapshot, error)
	SPScan(context.Context, SPScanRequest) (SPIndexerPage, error)
	SPLookup(context.Context, []SPIndexerPosition) ([]SPIndexerPositionResult, error)
}

func (p *IndexerClient) spGet(ctx context.Context, path string, query map[string]string, out any) error {
	if err := ctx.Err(); err != nil { return err }
	target := p.GetUrl(path)
	target.Query = query
	var data []byte
	var err error
	if client, ok := p.Http.(ContextHttpClient); ok {
		data, err = client.SendGetRequestContext(ctx, target)
	} else {
		data, err = p.Http.SendGetRequest(target)
	}
	if err != nil { return err }
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("silentpay: invalid Indexer response: %w", err)
	}
	return ctx.Err()
}

func (p *IndexerClient) spPost(ctx context.Context, path string, request, out any) error {
	if err := ctx.Err(); err != nil { return err }
	body, err := json.Marshal(request)
	if err != nil { return err }
	var data []byte
	if client, ok := p.Http.(ContextHttpClient); ok {
		data, err = client.SendPostRequestContext(ctx, p.GetUrl(path), body)
	} else {
		data, err = p.Http.SendPostRequest(p.GetUrl(path), body)
	}
	if err != nil { return err }
	if err := json.Unmarshal(data, out); err != nil { return err }
	return ctx.Err()
}

func (p *IndexerClient) SPStatus(ctx context.Context) (SPIndexerStatus, error) {
	var status SPIndexerStatus
	err := p.spGet(ctx, "/v1/sp/status", nil, &status)
	if err != nil { return status, err }
	if status.SchemaVersion != 1 || !status.DataReady || status.BaseAnchor.Height < 0 {
		return status, fmt.Errorf("silentpay: Indexer not ready")
	}
	return status, nil
}

func (p *IndexerClient) SPSnapshot(ctx context.Context) (SPIndexerSnapshot, error) {
	var snapshot SPIndexerSnapshot
	err := p.spGet(ctx, "/v1/sp/live-snapshot", nil, &snapshot)
	if err != nil { return snapshot, err }
	if snapshot.SnapshotID == "" || snapshot.SchemaVersion != 1 || snapshot.Anchor.Height < 0 {
		return snapshot, fmt.Errorf("silentpay: invalid snapshot")
	}
	return snapshot, nil
}

func (p *IndexerClient) SPScan(ctx context.Context, req SPScanRequest) (SPIndexerPage, error) {
	if req.Mode != "live" && req.Mode != "archive" { return SPIndexerPage{}, fmt.Errorf("silentpay: unsupported scan mode") }
	q := map[string]string{
		"mode":req.Mode, "from_height":strconv.Itoa(req.FromHeight), "limit":strconv.Itoa(req.Limit),
		"format":"json",
	}
	if req.SnapshotID != "" { q["snapshot_id"] = req.SnapshotID }
	if req.Cursor != "" { q["cursor"] = req.Cursor }
	if req.ToHeight >= 0 { q["to_height"] = strconv.Itoa(req.ToHeight) }
	if req.AnchorHash != "" { q["anchor_hash"] = req.AnchorHash }
	var page SPIndexerPage
	err := p.spGet(ctx, "/v1/sp/scan-data", q, &page)
	return page, err
}

func (p *IndexerClient) SPLookup(ctx context.Context, positions []SPIndexerPosition) ([]SPIndexerPositionResult, error) {
	var resp struct{
		Code int `json:"code"`
		Msg string `json:"msg"`
		Data []SPIndexerPositionResult `json:"data"`
	}
	if err := p.spPost(ctx, "/v1/sp/txids-by-position", positions, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 || len(resp.Data) != len(positions) {
		return nil, fmt.Errorf("silentpay: Indexer lookup failed: %s", resp.Msg)
	}
	for i := range resp.Data {
		got, expected := resp.Data[i], positions[i]
		if got.Error != "" || got.Height != expected.Height || got.BlockHash != expected.BlockHash ||
			got.TxIndex != expected.TxIndex || got.TxID == "" {
			return nil, fmt.Errorf("silentpay: invalid transaction lookup at %d: %s", i, got.Error)
		}
	}
	return resp.Data, nil
}

// Compile-time only, ensures the existing REST client exposes all SP
// endpoints but not the much larger IndexerRPCClient interface.
var _ SPIndexerRPC = (*IndexerClient)(nil)
