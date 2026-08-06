package wallet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

var ErrDKVSRecordNotFound = errors.New("DKVS record not found")

type SatsNetDKVSClient struct {
	*RESTClient
	manager          *dkvsManager
	replicaNamespace string
}

type DKVSNameResolution struct {
	CanonicalName string            `json:"canonical_name"`
	NameID        string            `json:"name_id"`
	Record        *swire.DKVSRecord `json:"record,omitempty"`
}

type httpDeleteClient interface {
	SendDeleteRequest(url *URL, marshalledJSON []byte) ([]byte, error)
}

type DKVSAutopayOptions struct {
	AddressParams *chaincfg.Params
	PoolContract  string
}

type dkvsBaseResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

type dkvsRecordResp struct {
	dkvsBaseResp
	Data *swire.DKVSRecord `json:"data,omitempty"`
	Hash string            `json:"hash,omitempty"`
}

type dkvsRecordsResp struct {
	dkvsBaseResp
	Start int                 `json:"start"`
	Total int                 `json:"total"`
	Data  []*swire.DKVSRecord `json:"data,omitempty"`
}

type dkvsCheckpointResp struct {
	dkvsBaseResp
	Data interface{} `json:"data,omitempty"`
}

type dkvsUsageResp struct {
	dkvsBaseResp
	Data *dkvsindexer.Usage `json:"data,omitempty"`
}

type dkvsPathMetaResp struct {
	dkvsBaseResp
	Data *dkvsindexer.PathMeta `json:"data,omitempty"`
}

type dkvsConfigResp struct {
	dkvsBaseResp
	Data *dkvsindexer.ClientConfig `json:"data,omitempty"`
}

type dkvsSubscriptionResp struct {
	dkvsBaseResp
	Total         int                        `json:"total"`
	Subscriptions []dkvsindexer.Subscription `json:"subscriptions,omitempty"`
	Data          []*swire.DKVSRecord        `json:"data,omitempty"`
}

type dkvsSnapshotImportResp struct {
	dkvsBaseResp
	Applied int `json:"applied"`
}

type dkvsPruneResp struct {
	dkvsBaseResp
	Pruned int `json:"pruned"`
}

type DKVSSyncPage struct {
	Records    []*swire.DKVSRecord `json:"records,omitempty"`
	NextCursor []byte              `json:"next_cursor,omitempty"`
	Done       bool                `json:"done"`
	Root       string              `json:"root"`
}

type dkvsSyncResp struct {
	dkvsBaseResp
	Data *DKVSSyncPage `json:"data,omitempty"`
}

type DKVSSyncRequest struct {
	Cursor  []byte                     `json:"cursor,omitempty"`
	Limit   uint32                     `json:"limit,omitempty"`
	Filters []dkvsindexer.Subscription `json:"filters"`
}

type DKVSWatchResult struct {
	Changed bool   `json:"changed"`
	Root    string `json:"root"`
}

type dkvsWatchResp struct {
	dkvsBaseResp
	Data *DKVSWatchResult `json:"data,omitempty"`
}

type DKVSWatchRequest struct {
	Filters        []dkvsindexer.Subscription `json:"filters"`
	Root           string                     `json:"root"`
	TimeoutSeconds int                        `json:"timeout_seconds,omitempty"`
}

func NewSatsNetDKVSClient(scheme, host, proxy string, http HttpClient) *SatsNetDKVSClient {
	if http == nil {
		http = &NetClient{Client: nethttp.DefaultClient}
	}
	return &SatsNetDKVSClient{RESTClient: NewRESTClient(scheme, host, proxy, http)}
}

func (p *SatsNetDKVSClient) SyncFiltered(req DKVSSyncRequest) (*DKVSSyncPage, error) {
	if len(req.Filters) == 0 {
		return nil, fmt.Errorf("at least one DKVS sync filter is required")
	}
	var resp dkvsSyncResp
	if err := p.postJSON("/v3/dkvs/sync", req, &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("DKVS sync response is missing data")
	}
	return resp.Data, nil
}

func (p *SatsNetDKVSClient) WatchFiltered(req DKVSWatchRequest) (*DKVSWatchResult, error) {
	if len(req.Filters) == 0 || strings.TrimSpace(req.Root) == "" {
		return nil, fmt.Errorf("DKVS watch filters and root are required")
	}
	var resp dkvsWatchResp
	if err := p.postJSON("/v3/dkvs/watch", req, &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("DKVS watch response is missing data")
	}
	return resp.Data, nil
}

func (p *SatsNetDKVSClient) SyncFilteredAll(filters []dkvsindexer.Subscription,
	opts dkvsindexer.RecordVerificationOptions) ([]*swire.DKVSRecord, string, error) {

	if len(filters) == 0 {
		return nil, "", fmt.Errorf("at least one DKVS sync filter is required")
	}
	var cursor []byte
	root := ""
	recordsByKey := make(map[string]*swire.DKVSRecord)
	for pageNumber := 0; pageNumber < 10000; pageNumber++ {
		page, err := p.SyncFiltered(DKVSSyncRequest{
			Cursor: cursor, Limit: swire.MaxDKVSRecordsPerMsg, Filters: filters,
		})
		if err != nil {
			return nil, "", err
		}
		if root == "" {
			root = page.Root
		} else if root != page.Root {
			cursor = nil
			root = ""
			recordsByKey = make(map[string]*swire.DKVSRecord)
			continue
		}
		for _, record := range page.Records {
			matched := false
			for _, filter := range filters {
				if dkvsindexer.SubscriptionMatchesKey(filter, record.Key) {
					matched = true
					break
				}
			}
			if !matched {
				return nil, "", dkvsindexer.ErrInvalidKey
			}
			if err := dkvsindexer.VerifyRecordForClient(record, opts); err != nil {
				return nil, "", err
			}
			if existing := recordsByKey[record.Key]; existing != nil &&
				dkvsindexer.RecordHash(existing) != dkvsindexer.RecordHash(record) {
				return nil, "", fmt.Errorf("conflicting DKVS sync record %s", record.Key)
			}
			recordsByKey[record.Key] = record
		}
		if page.Done {
			keys := make([]string, 0, len(recordsByKey))
			for key := range recordsByKey {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			records := make([]*swire.DKVSRecord, 0, len(keys))
			for _, key := range keys {
				records = append(records, recordsByKey[key])
			}
			return records, root, nil
		}
		if len(page.NextCursor) == 0 || bytes.Equal(page.NextCursor, cursor) {
			return nil, "", fmt.Errorf("invalid DKVS sync cursor")
		}
		cursor = append(cursor[:0], page.NextCursor...)
	}
	return nil, "", fmt.Errorf("DKVS sync exceeded page limit")
}

func verifyDKVSWriteEcho(request, echoed *swire.DKVSRecord, hashText string) (*swire.DKVSRecord, error) {
	if request == nil || echoed == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	want := dkvsindexer.RecordHash(request)
	if dkvsindexer.RecordHash(echoed) != want {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if strings.TrimSpace(hashText) != "" {
		got, err := chainhash.NewHashFromStr(strings.TrimSpace(hashText))
		if err != nil || *got != want {
			return nil, dkvsindexer.ErrInvalidRecord
		}
	}
	return echoed, nil
}

func (p *SatsNetDKVSClient) PutRecord(record *swire.DKVSRecord) (*swire.DKVSRecord, error) {
	if record == nil || record.Seq == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if p.manager != nil && p.manager.managesKey(record.Key) {
		return p.manager.putRecord(p, record)
	}
	if record.Seq == 1 {
		return p.PutRecordCAS(record, dkvsindexer.WritePrecondition{ExpectAbsent: true})
	}
	precondition, existing, err := p.dkvsWritePrecondition(record.Key)
	if err != nil {
		return nil, err
	}
	if existing != nil && dkvsindexer.RecordHash(existing) == dkvsindexer.RecordHash(record) {
		return existing, nil
	}
	return p.PutRecordCAS(record, precondition)
}

func (p *SatsNetDKVSClient) Tombstone(record *swire.DKVSRecord) (*swire.DKVSRecord, error) {
	if record == nil || record.Seq == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if p.manager != nil && p.manager.managesKey(record.Key) {
		return p.manager.putRecord(p, record)
	}
	if record.Seq == 1 {
		return p.PutRecordCAS(record, dkvsindexer.WritePrecondition{ExpectAbsent: true})
	}
	precondition, existing, err := p.dkvsWritePrecondition(record.Key)
	if err != nil {
		return nil, err
	}
	if existing != nil && dkvsindexer.RecordHash(existing) == dkvsindexer.RecordHash(record) {
		return existing, nil
	}
	return p.PutRecordCAS(record, precondition)
}

func (p *SatsNetDKVSClient) GetRecord(key string) (*swire.DKVSRecord, error) {
	if p.manager != nil && p.manager.managesKey(key) {
		if err := p.manager.waitPathsReady(p, []string{key}); err != nil {
			return nil, err
		}
	}
	var resp dkvsRecordResp
	url := p.GetUrl("/v3/dkvs/records")
	url.Query = map[string]string{"key": key}
	if err := p.getJSON(url, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (p *SatsNetDKVSClient) GetPathMeta(path string) (*dkvsindexer.PathMeta, error) {
	var resp dkvsPathMetaResp
	url := p.GetUrl("/v3/dkvs/path-meta")
	url.Query = map[string]string{"path": path}
	if err := p.getJSON(url, &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil || resp.Data.Path != path {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return resp.Data, nil
}

func (p *SatsNetDKVSClient) GetVerifiedRecord(key string, opts dkvsindexer.RecordVerificationOptions) (*swire.DKVSRecord, error) {
	record, err := p.GetRecord(key)
	if err != nil {
		return nil, err
	}
	if opts.ExpectedKey == "" {
		opts.ExpectedKey = key
	}
	if err := dkvsindexer.VerifyRecordForClient(record, opts); err != nil {
		return nil, err
	}
	return record, nil
}

func (p *SatsNetDKVSClient) GetRecordByHash(hash chainhash.Hash) (*swire.DKVSRecord, error) {
	var resp dkvsRecordResp
	url := p.GetUrl("/v3/dkvs/records")
	url.Query = map[string]string{"hash": hash.String()}
	if err := p.getJSON(url, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (p *SatsNetDKVSClient) GetVerifiedRecordByHash(hash chainhash.Hash, opts dkvsindexer.RecordVerificationOptions) (*swire.DKVSRecord, error) {
	record, err := p.GetRecordByHash(hash)
	if err != nil {
		return nil, err
	}
	opts.ExpectedHash = hash
	opts.CheckHash = true
	if err := dkvsindexer.VerifyRecordForClient(record, opts); err != nil {
		return nil, err
	}
	return record, nil
}

func (p *SatsNetDKVSClient) ListRecords(prefix string, start, limit int) ([]*swire.DKVSRecord, int, error) {
	var resp dkvsRecordsResp
	url := p.GetUrl("/v3/dkvs/records/prefix")
	url.Query = map[string]string{
		"prefix": prefix,
		"start":  strconv.Itoa(start),
		"limit":  strconv.Itoa(limit),
	}
	if err := p.getJSON(url, &resp); err != nil {
		return nil, 0, err
	}
	return resp.Data, resp.Total, nil
}

func (p *SatsNetDKVSClient) ListVerifiedRecords(prefix string, start, limit int, opts dkvsindexer.RecordVerificationOptions) ([]*swire.DKVSRecord, int, error) {
	records, total, err := p.ListRecords(prefix, start, limit)
	if err != nil {
		return nil, 0, err
	}
	if err := dkvsindexer.VerifyRecordsForClient(records, prefix, opts); err != nil {
		return nil, 0, err
	}
	return records, total, nil
}

func (p *SatsNetDKVSClient) GetUsage(prefix string) (*dkvsindexer.Usage, error) {
	var resp dkvsUsageResp
	url := p.GetUrl("/v3/dkvs/usage")
	url.Query = map[string]string{"prefix": prefix}
	if err := p.getJSON(url, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// GetDKVSClientConfig returns the write and cache policy of the connected
// node. Applications must treat it as node-local policy, not network consensus.
func (p *SatsNetDKVSClient) GetDKVSClientConfig() (*dkvsindexer.ClientConfig, error) {
	var resp dkvsConfigResp
	if err := p.getPathJSON("/v3/dkvs/config", &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return resp.Data, nil
}

// GetFreeLocalCachePolicy is retained as a convenience wrapper for callers
// that only need the FREE_LOCAL portion of the node policy.
func (p *SatsNetDKVSClient) GetFreeLocalCachePolicy() (*dkvsindexer.FreeLocalCachePolicy, error) {
	config, err := p.GetDKVSClientConfig()
	if err != nil {
		return nil, err
	}
	policy := config.FreeLocal
	return &policy, nil
}

func (p *SatsNetDKVSClient) GetCheckpoint() (*dkvsindexer.Checkpoint, error) {
	var resp struct {
		dkvsBaseResp
		Data *dkvsindexer.Checkpoint `json:"data,omitempty"`
	}
	if err := p.getPathJSON("/v3/dkvs/checkpoint", &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (p *SatsNetDKVSClient) GetSnapshot() (*dkvsindexer.Snapshot, error) {
	var resp struct {
		dkvsBaseResp
		Data *dkvsindexer.Snapshot `json:"data,omitempty"`
	}
	if err := p.getPathJSON("/v3/dkvs/snapshot", &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (p *SatsNetDKVSClient) GetVerifiedSnapshot() (*dkvsindexer.Snapshot, error) {
	snapshot, err := p.GetSnapshot()
	if err != nil {
		return nil, err
	}
	if err := dkvsindexer.ValidateSnapshot(snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (p *SatsNetDKVSClient) ApplySnapshot(snapshot *dkvsindexer.Snapshot) (int, error) {
	var resp dkvsSnapshotImportResp
	if err := p.postJSON("/v3/dkvs/snapshot", snapshot, &resp); err != nil {
		return 0, err
	}
	return resp.Applied, nil
}

func (p *SatsNetDKVSClient) PruneExpired() (int, error) {
	var resp dkvsPruneResp
	if err := p.postJSON("/v3/dkvs/prune", struct{}{}, &resp); err != nil {
		return 0, err
	}
	return resp.Pruned, nil
}

func (p *SatsNetDKVSClient) Subscribe(sub dkvsindexer.Subscription) ([]*swire.DKVSRecord, int, error) {
	var resp dkvsSubscriptionResp
	if err := p.postJSON("/v3/dkvs/subscriptions", sub, &resp); err != nil {
		return nil, 0, err
	}
	return resp.Data, resp.Total, nil
}

func (p *SatsNetDKVSClient) SubscribeVerified(sub dkvsindexer.Subscription, opts dkvsindexer.RecordVerificationOptions) ([]*swire.DKVSRecord, int, error) {
	records, total, err := p.Subscribe(sub)
	if err != nil {
		return nil, 0, err
	}
	if err := dkvsindexer.VerifySubscriptionRecordsForClient(records, sub, opts); err != nil {
		return nil, 0, err
	}
	return records, total, nil
}

func (p *SatsNetDKVSClient) Unsubscribe(sub dkvsindexer.Subscription) ([]dkvsindexer.Subscription, error) {
	var resp dkvsSubscriptionResp
	if err := p.deleteJSON("/v3/dkvs/subscriptions", sub, &resp); err != nil {
		return nil, err
	}
	return resp.Subscriptions, nil
}

func (p *SatsNetDKVSClient) ListSubscriptions() ([]dkvsindexer.Subscription, error) {
	var resp dkvsSubscriptionResp
	if err := p.getPathJSON("/v3/dkvs/subscriptions", &resp); err != nil {
		return nil, err
	}
	return resp.Subscriptions, nil
}

func newSignedRecordWithAutopay(wallet common.Wallet, key string, value []byte,
	opts dkvsindexer.RecordOptions, autopay DKVSAutopayOptions) (*swire.DKVSRecord, error) {
	opts.TTL = 0
	record, err := NewDKVSSignedRecord(wallet, key, value, opts)
	if err != nil {
		return nil, err
	}
	if err := attachDKVSAutopayFeeProof(wallet, record, autopay); err != nil {
		return nil, err
	}
	return record, nil
}

func attachDKVSAutopayFeeProof(wallet common.Wallet, record *swire.DKVSRecord,
	autopay DKVSAutopayOptions) error {
	if wallet == nil || record == nil {
		return dkvsindexer.ErrInvalidFeeProof
	}
	params := autopay.AddressParams
	if params == nil {
		params = &chaincfg.TestNetParams
	}
	poolContract := autopay.PoolContract
	if poolContract == "" {
		defaults := dkvsindexer.NetworkDefaultsForParams(params)
		poolContract = defaults.AutopayContract
	}
	if poolContract == "" {
		return dkvsindexer.ErrInvalidFeeProof
	}
	parsed, err := dkvsindexer.ParseKey(record.Key)
	if err != nil {
		return err
	}
	proof, err := dkvsindexer.NewAutopayFeeProof(
		record.Key, parsed.Namespace, swire.MaxDKVSRecordSize, dkvsindexer.RecordExpiryHeight(record), poolContract, "",
	)
	if err != nil {
		return err
	}
	if err := AttachDKVSFeeProof(record, proof); err != nil {
		return err
	}
	if err := SignDKVSRecord(wallet, record); err != nil {
		return err
	}
	return nil
}

func (p *SatsNetDKVSClient) PutSignedRecord(wallet common.Wallet, key string, value []byte, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	record, err := NewDKVSSignedRecord(wallet, key, value, opts)
	if err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

// PutSignedRecordFreeLocal writes a bounded, node-local cache record. The
// record is never relayed through SatoshiNet P2P and remains available only
// from this connected node until its TTL expires.
func (p *SatsNetDKVSClient) PutSignedRecordFreeLocal(wallet common.Wallet, key string, value []byte,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	policy, err := p.GetFreeLocalCachePolicy()
	if err != nil {
		return nil, err
	}
	if policy == nil || !policy.Enabled {
		return nil, dkvsindexer.ErrFreeLocalDisabled
	}
	if opts.TTL == 0 || opts.TTL > policy.MaxTTL {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	record, err := NewDKVSSignedRecord(wallet, key, value, opts)
	if err != nil {
		return nil, err
	}
	parsed, err := dkvsindexer.ParseKey(record.Key)
	if err != nil {
		return nil, err
	}
	proof, err := dkvsindexer.NewFreeLocalFeeProof(record.Key, parsed.Namespace,
		swire.MaxDKVSRecordSize, dkvsindexer.RecordExpiryHeight(record))
	if err != nil {
		return nil, err
	}
	if err := AttachDKVSFeeProof(record, proof); err != nil {
		return nil, err
	}
	if err := SignDKVSRecord(wallet, record); err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) PutSignedRecordWithAutopay(wallet common.Wallet, key string, value []byte,
	opts dkvsindexer.RecordOptions, autopay DKVSAutopayOptions) (*swire.DKVSRecord, error) {

	record, err := newSignedRecordWithAutopay(wallet, key, value, opts, autopay)
	if err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) TombstoneSigned(wallet common.Wallet, key string, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	record, err := NewDKVSSignedTombstone(wallet, key, opts)
	if err != nil {
		return nil, err
	}
	return p.Tombstone(record)
}

func (p *SatsNetDKVSClient) TombstoneSignedWithAutopay(wallet common.Wallet, key string,
	opts dkvsindexer.RecordOptions, autopay DKVSAutopayOptions) (*swire.DKVSRecord, error) {

	opts.Flags |= dkvsindexer.FlagTombstone
	record, err := newSignedRecordWithAutopay(wallet, key, nil, opts, autopay)
	if err != nil {
		return nil, err
	}
	return p.Tombstone(record)
}

func (p *SatsNetDKVSClient) RenewRecord(wallet common.Wallet, existing *swire.DKVSRecord, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	record, err := NewDKVSSignedRenewalRecord(wallet, existing, opts)
	if err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) PutPersonalRecord(wallet common.Wallet, path string, value []byte, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	pubKey, err := dkvsWalletPubKey(wallet)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	key, err := dkvsindexer.PersonalKey(pubKey, path)
	if err != nil {
		return nil, err
	}
	return p.PutSignedRecord(wallet, key, value, opts)
}

func (p *SatsNetDKVSClient) PutPersonalRecordFreeLocal(wallet common.Wallet, path string, value []byte,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {

	pubKey, err := dkvsWalletPubKey(wallet)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	key, err := dkvsindexer.PersonalKey(pubKey, path)
	if err != nil {
		return nil, err
	}
	return p.PutSignedRecordFreeLocal(wallet, key, value, opts)
}

func (p *SatsNetDKVSClient) PutPersonalRecordWithAutopay(wallet common.Wallet, path string, value []byte,
	opts dkvsindexer.RecordOptions, autopay DKVSAutopayOptions) (*swire.DKVSRecord, error) {

	pubKey, err := dkvsWalletPubKey(wallet)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	key, err := dkvsindexer.PersonalKey(pubKey, path)
	if err != nil {
		return nil, err
	}
	return p.PutSignedRecordWithAutopay(wallet, key, value, opts, autopay)
}

func (p *SatsNetDKVSClient) GetPersonalRecord(pubKey []byte, path string) (*swire.DKVSRecord, error) {
	key, err := dkvsindexer.PersonalKey(pubKey, path)
	if err != nil {
		return nil, err
	}
	return p.GetRecord(key)
}

func (p *SatsNetDKVSClient) TombstonePersonalRecord(wallet common.Wallet, path string, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	pubKey, err := dkvsWalletPubKey(wallet)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	key, err := dkvsindexer.PersonalKey(pubKey, path)
	if err != nil {
		return nil, err
	}
	return p.TombstoneSigned(wallet, key, opts)
}

func (p *SatsNetDKVSClient) TombstonePersonalRecordWithAutopay(wallet common.Wallet, path string,
	opts dkvsindexer.RecordOptions, autopay DKVSAutopayOptions) (*swire.DKVSRecord, error) {

	pubKey, err := dkvsWalletPubKey(wallet)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	key, err := dkvsindexer.PersonalKey(pubKey, path)
	if err != nil {
		return nil, err
	}
	return p.TombstoneSignedWithAutopay(wallet, key, opts, autopay)
}

func (p *SatsNetDKVSClient) RenewPersonalRecord(wallet common.Wallet, path string, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	pubKey, err := dkvsWalletPubKey(wallet)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	existing, err := p.GetPersonalRecord(pubKey, path)
	if err != nil {
		return nil, err
	}
	return p.RenewRecord(wallet, existing, opts)
}

func (p *SatsNetDKVSClient) SubscribeKey(key string) ([]*swire.DKVSRecord, int, error) {
	if _, err := dkvsindexer.ParseKey(key); err != nil {
		return nil, 0, err
	}
	return p.Subscribe(dkvsindexer.Subscription{Type: dkvsindexer.SubscriptionKey, Target: key})
}

func (p *SatsNetDKVSClient) UnsubscribeKey(key string) ([]dkvsindexer.Subscription, error) {
	if _, err := dkvsindexer.ParseKey(key); err != nil {
		return nil, err
	}
	return p.Unsubscribe(dkvsindexer.Subscription{Type: dkvsindexer.SubscriptionKey, Target: key})
}

func (p *SatsNetDKVSClient) SubscribePrefix(prefix string) ([]*swire.DKVSRecord, int, error) {
	if _, err := dkvsindexer.ParsePrefix(prefix); err != nil {
		return nil, 0, err
	}
	return p.Subscribe(dkvsindexer.Subscription{Type: dkvsindexer.SubscriptionPrefix, Target: prefix})
}

func (p *SatsNetDKVSClient) UnsubscribePrefix(prefix string) ([]dkvsindexer.Subscription, error) {
	if _, err := dkvsindexer.ParsePrefix(prefix); err != nil {
		return nil, err
	}
	return p.Unsubscribe(dkvsindexer.Subscription{Type: dkvsindexer.SubscriptionPrefix, Target: prefix})
}

func (p *SatsNetDKVSClient) CreateMailbox(pubKey []byte) (string, error) {
	mailboxID := dkvsindexer.AccountID(pubKey)
	if _, err := mailboxSubscriptionTarget(mailboxID); err != nil {
		return "", err
	}
	return mailboxID, nil
}

func (p *SatsNetDKVSClient) SendMailboxMessage(record *swire.DKVSRecord) (*swire.DKVSRecord, error) {
	if err := requireDKVSRecordKeyKind(record, "mail", "msg"); err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) SendSignedMailboxMessage(wallet common.Wallet, mailboxID, msgID string, encryptedMessage []byte, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	pubKey, err := dkvsWalletPubKey(wallet)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	key, err := dkvsindexer.MailMsgKey(mailboxID, dkvsindexer.AccountID(pubKey), msgID)
	if err != nil {
		return nil, err
	}
	record, err := NewDKVSSignedRecord(wallet, key, encryptedMessage, opts)
	if err != nil {
		return nil, err
	}
	return p.SendMailboxMessage(record)
}

func (p *SatsNetDKVSClient) SendSignedMailboxMessageWithAutopay(wallet common.Wallet, mailboxID, msgID string,
	encryptedMessage []byte, opts dkvsindexer.RecordOptions, autopay DKVSAutopayOptions) (*swire.DKVSRecord, error) {

	pubKey, err := dkvsWalletPubKey(wallet)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	key, err := dkvsindexer.MailMsgKey(mailboxID, dkvsindexer.AccountID(pubKey), msgID)
	if err != nil {
		return nil, err
	}
	record, err := newSignedRecordWithAutopay(wallet, key, encryptedMessage, opts, autopay)
	if err != nil {
		return nil, err
	}
	return p.SendMailboxMessage(record)
}

func (p *SatsNetDKVSClient) PutMailboxShare(record *swire.DKVSRecord) (*swire.DKVSRecord, error) {
	if err := requireDKVSRecordKeyKind(record, "mail", "share"); err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) ReadMailboxMessages(mailboxID string, start, limit int) ([]*swire.DKVSRecord, int, error) {
	prefix, err := mailboxPrefix(mailboxID, "msg")
	if err != nil {
		return nil, 0, err
	}
	return p.ListRecords(prefix, start, limit)
}

func (p *SatsNetDKVSClient) ReadMailboxShares(mailboxID string, start, limit int) ([]*swire.DKVSRecord, int, error) {
	prefix, err := mailboxPrefix(mailboxID, "share")
	if err != nil {
		return nil, 0, err
	}
	return p.ListRecords(prefix, start, limit)
}

func (p *SatsNetDKVSClient) DeleteMailboxRecord(tombstone *swire.DKVSRecord) (*swire.DKVSRecord, error) {
	if err := requireDKVSRecordNamespace(tombstone, "mail"); err != nil {
		return nil, err
	}
	if tombstone.Flags&dkvsindexer.FlagTombstone == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return p.Tombstone(tombstone)
}

func (p *SatsNetDKVSClient) DeleteMessage(wallet common.Wallet, mailboxID, senderID, msgID string, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	key, err := dkvsindexer.MailMsgKey(mailboxID, senderID, msgID)
	if err != nil {
		return nil, err
	}
	tombstone, err := NewDKVSSignedTombstone(wallet, key, opts)
	if err != nil {
		return nil, err
	}
	return p.DeleteMailboxRecord(tombstone)
}

func (p *SatsNetDKVSClient) SubscribeMailbox(mailboxID string) ([]*swire.DKVSRecord, int, error) {
	target, err := mailboxSubscriptionTarget(mailboxID)
	if err != nil {
		return nil, 0, err
	}
	return p.Subscribe(dkvsindexer.Subscription{Type: dkvsindexer.SubscriptionMailbox, Target: target})
}

func (p *SatsNetDKVSClient) UnsubscribeMailbox(mailboxID string) ([]dkvsindexer.Subscription, error) {
	target, err := mailboxSubscriptionTarget(mailboxID)
	if err != nil {
		return nil, err
	}
	return p.Unsubscribe(dkvsindexer.Subscription{Type: dkvsindexer.SubscriptionMailbox, Target: target})
}

func (p *SatsNetDKVSClient) PutNameRecord(record *swire.DKVSRecord) (*swire.DKVSRecord, error) {
	if err := requireDKVSRecordNamespace(record, "name"); err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) PutSignedNameRecord(wallet common.Wallet, name string, value []byte, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	key, err := dkvsindexer.NameKey(name)
	if err != nil {
		return nil, err
	}
	record, err := NewDKVSSignedRecord(wallet, key, value, opts)
	if err != nil {
		return nil, err
	}
	return p.PutNameRecord(record)
}

func (p *SatsNetDKVSClient) GetNameRecord(name string) (*swire.DKVSRecord, error) {
	key, err := dkvsindexer.NameKey(name)
	if err != nil {
		return nil, err
	}
	return p.GetRecord(key)
}

func (p *SatsNetDKVSClient) ResolveNameRecord(name string) (*DKVSNameResolution, error) {
	nameID := dkvsindexer.NormalizeNameID(name)
	record, err := p.GetNameRecord(name)
	if err != nil {
		return nil, err
	}
	return &DKVSNameResolution{
		CanonicalName: name,
		NameID:        nameID,
		Record:        record,
	}, nil
}

func (p *SatsNetDKVSClient) PutServiceRecord(record *swire.DKVSRecord) (*swire.DKVSRecord, error) {
	if err := requireDKVSRecordNamespace(record, "svc"); err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) PutSignedServiceRecord(wallet common.Wallet, serviceName, path string, value []byte, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	key, err := dkvsindexer.ServiceKey(serviceName, path)
	if err != nil {
		return nil, err
	}
	record, err := NewDKVSSignedRecord(wallet, key, value, opts)
	if err != nil {
		return nil, err
	}
	return p.PutServiceRecord(record)
}

func (p *SatsNetDKVSClient) GetServiceRecord(serviceName, path string) (*swire.DKVSRecord, error) {
	key, err := dkvsindexer.ServiceKey(serviceName, path)
	if err != nil {
		return nil, err
	}
	return p.GetRecord(key)
}

func (p *SatsNetDKVSClient) ListServiceRecords(serviceName string, start, limit int) ([]*swire.DKVSRecord, int, error) {
	target, err := serviceSubscriptionTarget(serviceName)
	if err != nil {
		return nil, 0, err
	}
	return p.ListRecords(target, start, limit)
}

func (p *SatsNetDKVSClient) SubscribeService(serviceName string) ([]*swire.DKVSRecord, int, error) {
	target, err := serviceSubscriptionTarget(serviceName)
	if err != nil {
		return nil, 0, err
	}
	return p.Subscribe(dkvsindexer.Subscription{Type: dkvsindexer.SubscriptionService, Target: target})
}

func (p *SatsNetDKVSClient) UnsubscribeService(serviceName string) ([]dkvsindexer.Subscription, error) {
	target, err := serviceSubscriptionTarget(serviceName)
	if err != nil {
		return nil, err
	}
	return p.Unsubscribe(dkvsindexer.Subscription{Type: dkvsindexer.SubscriptionService, Target: target})
}

func (p *SatsNetDKVSClient) getPathJSON(path string, out interface{}) error {
	return p.getJSON(p.GetUrl(path), out)
}

func (p *SatsNetDKVSClient) getJSON(url *URL, out interface{}) error {
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return err
	}
	return decodeDKVSResp(url, rsp, out)
}

func (p *SatsNetDKVSClient) postJSON(path string, req interface{}, out interface{}) error {
	buff, err := json.Marshal(req)
	if err != nil {
		return err
	}
	url := p.GetUrl(path)
	rsp, err := p.Http.SendPostRequest(url, buff)
	if err != nil {
		Log.Errorf("SendPostRequest %v failed. %v", url, err)
		return err
	}
	return decodeDKVSResp(url, rsp, out)
}

func (p *SatsNetDKVSClient) deleteJSON(path string, req interface{}, out interface{}) error {
	buff, err := json.Marshal(req)
	if err != nil {
		return err
	}
	client, ok := p.Http.(httpDeleteClient)
	if !ok {
		return fmt.Errorf("http client does not support DELETE")
	}
	url := p.GetUrl(path)
	rsp, err := client.SendDeleteRequest(url, buff)
	if err != nil {
		Log.Errorf("SendDeleteRequest %v failed. %v", url, err)
		return err
	}
	return decodeDKVSResp(url, rsp, out)
}

func decodeDKVSResp(url *URL, rsp []byte, out interface{}) error {
	if err := json.Unmarshal(rsp, out); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return err
	}
	var base dkvsBaseResp
	if err := json.Unmarshal(rsp, &base); err != nil {
		return err
	}
	if base.Code != 0 {
		Log.Errorf("%v response message %s", url, base.Msg)
		if strings.Contains(strings.ToLower(base.Msg), "not found") {
			return fmt.Errorf("%w: %s", ErrDKVSRecordNotFound, base.Msg)
		}
		return fmt.Errorf("%s", base.Msg)
	}
	return nil
}

func requireDKVSRecordNamespace(record *swire.DKVSRecord, namespace string) error {
	if record == nil {
		return dkvsindexer.ErrInvalidRecord
	}
	parsed, err := dkvsindexer.ParseKey(record.Key)
	if err != nil {
		return err
	}
	if parsed.Namespace != namespace {
		return dkvsindexer.ErrInvalidKey
	}
	return nil
}

func requireDKVSRecordKeyKind(record *swire.DKVSRecord, namespace, kind string) error {
	if record == nil {
		return dkvsindexer.ErrInvalidRecord
	}
	parsed, err := dkvsindexer.ParseKey(record.Key)
	if err != nil {
		return err
	}
	if parsed.Namespace != namespace || len(parsed.Segments) < 2 || parsed.Segments[1] != kind {
		return dkvsindexer.ErrInvalidKey
	}
	return nil
}

func mailboxPrefix(mailboxID, kind string) (string, error) {
	target, err := mailboxSubscriptionTarget(mailboxID)
	if err != nil {
		return "", err
	}
	prefix := target + "/" + kind
	if _, err := dkvsindexer.ParsePrefix(prefix); err != nil {
		return "", err
	}
	return prefix, nil
}

func mailboxSubscriptionTarget(mailboxID string) (string, error) {
	target := "/mail/" + mailboxID
	if _, err := dkvsindexer.ParsePrefix(target); err != nil {
		return "", err
	}
	return target, nil
}

func serviceSubscriptionTarget(serviceName string) (string, error) {
	target := "/svc/" + dkvsindexer.NormalizeNameID(serviceName)
	if _, err := dkvsindexer.ParsePrefix(target); err != nil {
		return "", err
	}
	return target, nil
}
