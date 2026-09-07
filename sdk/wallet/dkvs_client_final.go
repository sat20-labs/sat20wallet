package wallet

import (
	"context"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"sort"
	"strings"
	"sync"

	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

var (
	ErrDKVSRecordNotFound         = dkvsindexer.ErrRecordNotFound
	ErrDKVSMessageManagerRequired = fmt.Errorf("mailbox message operation requires Wallet MessageManager")
)

type SatsNetDKVSClient struct {
	*RESTClient
	manager          *dkvsManager
	replicaNamespace string
	endpointMu       sync.RWMutex
	endpointID       string
}

func (p *SatsNetDKVSClient) unmanagedReadCacheNamespace() string {
	if p == nil || p.RESTClient == nil {
		return ""
	}
	p.endpointMu.RLock()
	endpointID := p.endpointID
	p.endpointMu.RUnlock()
	return p.replicaNamespace + "\x00" + dkvsEndpointKey(p.Scheme, p.Host, p.Proxy) + "\x00" + endpointID
}

func (p *SatsNetDKVSClient) rememberEndpointID(endpointID string) {
	if p == nil || strings.TrimSpace(endpointID) == "" {
		return
	}
	p.endpointMu.Lock()
	p.endpointID = strings.TrimSpace(endpointID)
	p.endpointMu.Unlock()
}

type DKVSNameResolution struct {
	CanonicalName string            `json:"canonical_name"`
	NameID        string            `json:"name_id"`
	Record        *swire.DKVSRecord `json:"record,omitempty"`
}

type DKVSAutopayOptions struct {
	AddressParams *chaincfg.Params
	PoolContract  string
}

type dkvsBaseResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

func NewSatsNetDKVSClient(scheme, host, proxy string, http HttpClient) *SatsNetDKVSClient {
	if http == nil {
		http = &NetClient{Client: nethttp.DefaultClient}
	}
	return &SatsNetDKVSClient{RESTClient: NewRESTClient(scheme, host, proxy, http)}
}

func (p *SatsNetDKVSClient) requestContext() context.Context {
	if p != nil && p.manager != nil {
		return p.manager.requestContext()
	}
	return context.Background()
}

func (p *SatsNetDKVSClient) sendGetRequest(url *URL) ([]byte, error) {
	if client, ok := p.Http.(ContextHttpClient); ok {
		return client.SendGetRequestContext(p.requestContext(), url)
	}
	return p.Http.SendGetRequest(url)
}

func (p *SatsNetDKVSClient) sendPostRequest(url *URL, body []byte) ([]byte, error) {
	if client, ok := p.Http.(ContextHttpClient); ok {
		return client.SendPostRequestContext(p.requestContext(), url, body)
	}
	return p.Http.SendPostRequest(url, body)
}

func (p *SatsNetDKVSClient) getJSON(url *URL, out interface{}) error {
	rsp, err := p.sendGetRequest(url)
	if err != nil {
		return err
	}
	return decodeDKVSResp(url, rsp, out)
}

func (p *SatsNetDKVSClient) getPathJSON(path string, out interface{}) error {
	return p.getJSON(p.GetUrl(path), out)
}

func (p *SatsNetDKVSClient) postJSON(path string, req interface{}, out interface{}) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	url := p.GetUrl(path)
	rsp, err := p.sendPostRequest(url, body)
	if err != nil {
		return err
	}
	return decodeDKVSResp(url, rsp, out)
}

func decodeDKVSResp(url *URL, rsp []byte, out interface{}) error {
	if err := json.Unmarshal(rsp, out); err != nil {
		return err
	}
	var base struct {
		Code      int    `json:"code"`
		Msg       string `json:"msg"`
		ErrorCode string `json:"error_code,omitempty"`
	}
	if err := json.Unmarshal(rsp, &base); err != nil {
		return err
	}
	if base.Code != 0 {
		if base.ErrorCode != "" {
			return &DKVSError{Code: dkvsindexer.ErrorCode(base.ErrorCode), Message: base.Msg}
		}
		if strings.Contains(strings.ToLower(base.Msg), "not found") {
			return fmt.Errorf("%w: %s", ErrDKVSRecordNotFound, base.Msg)
		}
		return fmt.Errorf("%s", base.Msg)
	}
	_ = url
	return nil
}

// GetBestHeight reads the best-chain height from the same endpoint used for DKVS.
func (p *SatsNetDKVSClient) GetBestHeight() (uint64, error) {
	if p == nil || p.RESTClient == nil || p.Http == nil {
		return 0, fmt.Errorf("DKVS endpoint is not configured")
	}
	url := p.GetUrl("/btc/block/bestblockheight")
	rsp, err := p.sendGetRequest(url)
	if err != nil {
		return 0, fmt.Errorf("query DKVS endpoint bestheight: %w", err)
	}
	var result indexerwire.BestBlockHeightResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		return 0, fmt.Errorf("decode DKVS endpoint bestheight: %w", err)
	}
	if result.Code != 0 || result.Data < 0 {
		return 0, fmt.Errorf("DKVS endpoint bestheight: %s", result.Msg)
	}
	return uint64(result.Data), nil
}

func (p *SatsNetDKVSClient) GetDKVSClientConfig() (*dkvsindexer.ClientConfig, error) {
	if p == nil || p.Http == nil {
		return nil, ErrDKVSPathNotSynced
	}
	if provider, ok := p.Http.(dkvsConfigProvider); ok {
		config, err := provider.DKVSClientConfig()
		if err != nil {
			return nil, err
		}
		if config == nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		copyConfig := *config
		p.rememberEndpointID(copyConfig.EndpointID)
		return &copyConfig, nil
	}
	var resp struct {
		dkvsApplicationBaseResp
		Data *dkvsindexer.ClientConfig `json:"data,omitempty"`
	}
	if err := p.getDKVSApplication("/v3/dkvs/config", nil, &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil || strings.TrimSpace(resp.Data.EndpointID) == "" {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	copyConfig := *resp.Data
	p.rememberEndpointID(copyConfig.EndpointID)
	return &copyConfig, nil
}

func (p *SatsNetDKVSClient) GetFreeLocalCachePolicy() (*dkvsindexer.FreeLocalCachePolicy, error) {
	config, err := p.GetDKVSClientConfig()
	if err != nil {
		return nil, err
	}
	policy := config.FreeLocal
	return &policy, nil
}

func (p *SatsNetDKVSClient) PutRecord(record *swire.DKVSRecord) (*swire.DKVSRecord, error) {
	if record == nil || record.Seq == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if p.manager != nil {
		return p.manager.putRecord(p, record)
	}
	precondition, existing, err := p.dkvsWritePrecondition(record.Key)
	if err != nil {
		return nil, err
	}
	if existing != nil && dkvsindexer.RecordHash(existing) == dkvsindexer.RecordHash(record) {
		return existing, nil
	}
	state, err := p.GetKeyState(record.Key)
	if err != nil {
		return nil, err
	}
	next := uint64(1)
	if state.Status != dkvsindexer.KeyStateNeverSeen {
		if state.Seq == ^uint64(0) {
			return nil, dkvsindexer.ErrInvalidSequence
		}
		next = state.Seq + 1
	}
	if record.Seq != next {
		return nil, dkvsindexer.ErrInvalidSequence
	}
	return p.PutRecordCAS(record, precondition)
}

func (p *SatsNetDKVSClient) Tombstone(record *swire.DKVSRecord) (*swire.DKVSRecord, error) {
	if record == nil || record.Seq == 0 || !dkvsindexer.IsTombstone(record.Flags) {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) GetRecord(key string) (*swire.DKVSRecord, error) {
	if p == nil {
		return nil, ErrDKVSPathNotSynced
	}
	if p.manager == nil {
		return p.GetRecordDirect(key)
	}
	value, err := (&dkvsStore{manager: p.manager, client: p}).Get(key)
	if err != nil {
		return nil, err
	}
	if value == nil || value.record == nil {
		return nil, ErrDKVSRecordNotFound
	}
	return value.record, nil
}

func (p *SatsNetDKVSClient) GetVerifiedRecord(key string,
	opts dkvsindexer.RecordVerificationOptions) (*swire.DKVSRecord, error) {
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

func (p *SatsNetDKVSClient) GetRecordByHash(chainhash.Hash) (*swire.DKVSRecord, error) {
	return nil, fmt.Errorf("record-by-hash is not part of the Wallet DKVS application protocol: %w", dkvsindexer.ErrRecordNotFound)
}

func (p *SatsNetDKVSClient) GetVerifiedRecordByHash(hash chainhash.Hash,
	opts dkvsindexer.RecordVerificationOptions) (*swire.DKVSRecord, error) {
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

func sliceRecords(records []*swire.DKVSRecord, start, limit int) ([]*swire.DKVSRecord, int) {
	total := len(records)
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	if limit <= 0 || start+limit > total {
		limit = total - start
	}
	return records[start : start+limit], total
}

func (p *SatsNetDKVSClient) ListRecords(prefix string, start, limit int) ([]*swire.DKVSRecord, int, error) {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	if _, err := dkvsindexer.ParsePrefix(prefix); err != nil {
		return nil, 0, err
	}
	var records []*swire.DKVSRecord
	if p.manager != nil && p.manager.desiredPrefixContainsKey(p, prefix) {
		if err := p.manager.waitDirectoriesReady(p, []string{prefix}); err != nil {
			return nil, 0, err
		}
		all, err := newDKVSReplicaStore(p.manager.owner.db).ListSubscriptionRecords(p.replicaNamespace)
		if err != nil {
			return nil, 0, err
		}
		for _, record := range all {
			if record != nil && walletSubscriptionMatches(prefix, record.Key) {
				records = append(records, record)
			}
		}
	} else {
		if p.manager != nil {
			if cached, ok := p.manager.cachedPrefixRead(p.unmanagedReadCacheNamespace(), prefix); ok {
				records = cached
			} else {
				ctx, cancel := context.WithTimeout(p.requestContext(), dkvsUnmanagedReadTimeout)
				result, err := p.ReadPrefixContext(ctx, prefix)
				cancel()
				if err != nil {
					return nil, 0, err
				}
				records = append(records, result.Records...)
				p.manager.cachePrefixRead(p.unmanagedReadCacheNamespace(), prefix, records)
			}
		} else {
			ctx, cancel := context.WithTimeout(p.requestContext(), dkvsUnmanagedReadTimeout)
			result, err := p.ReadPrefixContext(ctx, prefix)
			cancel()
			if err != nil {
				return nil, 0, err
			}
			records = append(records, result.Records...)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Key < records[j].Key })
	page, total := sliceRecords(records, start, limit)
	return page, total, nil
}

func (p *SatsNetDKVSClient) ListVerifiedRecords(prefix string, start, limit int,
	opts dkvsindexer.RecordVerificationOptions) ([]*swire.DKVSRecord, int, error) {
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
	records, _, err := p.ListRecords(prefix, 0, 0)
	if err != nil {
		return nil, err
	}
	usage := &dkvsindexer.Usage{Prefix: strings.TrimSuffix(strings.TrimSpace(prefix), "/")}
	for _, record := range records {
		usage.ActiveRecords++
		usage.ActiveTotalSize += uint64(dkvsindexer.RecordSize(record))
	}
	return usage, nil
}

func (p *SatsNetDKVSClient) Subscribe(sub dkvsindexer.Subscription) ([]*swire.DKVSRecord, int, error) {
	if p == nil || p.manager == nil || p.manager.owner == nil {
		return nil, 0, fmt.Errorf("persistent prefix subscription requires wallet manager")
	}
	prefix := strings.TrimSuffix(strings.TrimSpace(sub.Target), "/")
	if sub.Type == dkvsindexer.SubscriptionKey {
		if _, err := dkvsindexer.ParseKey(prefix); err != nil {
			return nil, 0, err
		}
	} else if _, err := dkvsindexer.ParsePrefix(prefix); err != nil {
		return nil, 0, err
	}
	if err := p.manager.owner.SubscribeDKVSPrefix(prefix); err != nil {
		return nil, 0, err
	}
	if err := p.manager.waitDirectoriesReady(p, []string{prefix}); err != nil {
		return nil, 0, err
	}
	return p.ListRecords(prefix, 0, 0)
}

func (p *SatsNetDKVSClient) SubscribeVerified(sub dkvsindexer.Subscription,
	opts dkvsindexer.RecordVerificationOptions) ([]*swire.DKVSRecord, int, error) {
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
	if p == nil || p.manager == nil || p.manager.owner == nil {
		return nil, fmt.Errorf("persistent prefix subscription requires wallet manager")
	}
	if err := p.manager.owner.UnsubscribeDKVSPrefix(sub.Target); err != nil {
		return nil, err
	}
	return p.ListSubscriptions()
}

func (p *SatsNetDKVSClient) ListSubscriptions() ([]dkvsindexer.Subscription, error) {
	if p == nil || p.manager == nil || p.manager.owner == nil {
		return nil, fmt.Errorf("persistent prefix subscription requires wallet manager")
	}
	prefixes, err := p.manager.owner.ListSubscribedDKVSPrefixes()
	if err != nil {
		return nil, err
	}
	result := make([]dkvsindexer.Subscription, 0, len(prefixes))
	for _, prefix := range prefixes {
		result = append(result, dkvsindexer.Subscription{Type: dkvsindexer.SubscriptionPrefix, Target: prefix})
	}
	return result, nil
}

func (p *SatsNetDKVSClient) GetCheckpoint() (*dkvsindexer.Checkpoint, error) {
	return nil, fmt.Errorf("checkpoint is node-local administration, not Wallet DKVS")
}

func (p *SatsNetDKVSClient) GetSnapshot() (*dkvsindexer.Snapshot, error) {
	return nil, fmt.Errorf("canonical snapshot is node-local administration, not Wallet DKVS")
}

func (p *SatsNetDKVSClient) GetVerifiedSnapshot() (*dkvsindexer.Snapshot, error) {
	return p.GetSnapshot()
}

func (p *SatsNetDKVSClient) ApplySnapshot(*dkvsindexer.Snapshot) (int, error) {
	return 0, fmt.Errorf("canonical snapshot import is node-local administration, not Wallet DKVS")
}

func (p *SatsNetDKVSClient) PruneExpired() (int, error) {
	return 0, fmt.Errorf("expiry pruning is node-local administration, not Wallet DKVS")
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
		poolContract = dkvsindexer.NetworkDefaultsForParams(params).AutopayContract
	}
	if poolContract == "" {
		return dkvsindexer.ErrInvalidFeeProof
	}
	parsed, err := dkvsindexer.ParseKey(record.Key)
	if err != nil {
		return err
	}
	proof, err := dkvsindexer.NewAutopayFeeProof(record.Key, parsed.Namespace,
		swire.MaxDKVSRecordSize, dkvsindexer.RecordExpiryHeight(record), poolContract, "")
	if err != nil {
		return err
	}
	if err := AttachDKVSFeeProof(record, proof); err != nil {
		return err
	}
	return SignDKVSRecord(wallet, record)
}

func (p *SatsNetDKVSClient) resolveRecordSequence(key string, seq uint64) (uint64, error) {
	if seq != 0 {
		return seq, nil
	}
	state, err := p.GetKeyState(key)
	if err != nil {
		return 0, err
	}
	if state.Status == dkvsindexer.KeyStateNeverSeen {
		return 1, nil
	}
	if state.Seq == ^uint64(0) {
		return 0, dkvsindexer.ErrInvalidSequence
	}
	return state.Seq + 1, nil
}

func (p *SatsNetDKVSClient) PutSignedRecord(wallet common.Wallet, key string, value []byte,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	seq, err := p.resolveRecordSequence(key, opts.Seq)
	if err != nil {
		return nil, err
	}
	opts.Seq = seq
	record, err := NewDKVSSignedRecord(wallet, key, value, opts)
	if err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) PutSignedRecordFreeLocal(wallet common.Wallet, key string, value []byte,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	seq, err := p.resolveRecordSequence(key, opts.Seq)
	if err != nil {
		return nil, err
	}
	opts.Seq = seq
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
	seq, err := p.resolveRecordSequence(key, opts.Seq)
	if err != nil {
		return nil, err
	}
	opts.Seq = seq
	record, err := newSignedRecordWithAutopay(wallet, key, value, opts, autopay)
	if err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) TombstoneSigned(wallet common.Wallet, key string,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	seq, err := p.resolveRecordSequence(key, opts.Seq)
	if err != nil {
		return nil, err
	}
	opts.Seq = seq
	record, err := NewDKVSSignedTombstone(wallet, key, opts)
	if err != nil {
		return nil, err
	}
	return p.Tombstone(record)
}

func (p *SatsNetDKVSClient) TombstoneSignedWithAutopay(wallet common.Wallet, key string,
	opts dkvsindexer.RecordOptions, autopay DKVSAutopayOptions) (*swire.DKVSRecord, error) {
	seq, err := p.resolveRecordSequence(key, opts.Seq)
	if err != nil {
		return nil, err
	}
	opts.Seq = seq
	opts.Flags |= dkvsindexer.FlagTombstone
	record, err := newSignedRecordWithAutopay(wallet, key, nil, opts, autopay)
	if err != nil {
		return nil, err
	}
	return p.Tombstone(record)
}

func (p *SatsNetDKVSClient) RenewRecord(wallet common.Wallet, existing *swire.DKVSRecord,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	record, err := NewDKVSSignedRenewalRecord(wallet, existing, opts)
	if err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) PutPersonalRecord(wallet common.Wallet, path string, value []byte,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
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

func (p *SatsNetDKVSClient) TombstonePersonalRecord(wallet common.Wallet, path string,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
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

func (p *SatsNetDKVSClient) RenewPersonalRecord(wallet common.Wallet, path string,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
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
	return p.Subscribe(dkvsindexer.Subscription{Type: dkvsindexer.SubscriptionKey, Target: key})
}

func (p *SatsNetDKVSClient) UnsubscribeKey(key string) ([]dkvsindexer.Subscription, error) {
	return p.Unsubscribe(dkvsindexer.Subscription{Type: dkvsindexer.SubscriptionKey, Target: key})
}

func (p *SatsNetDKVSClient) SubscribePrefix(prefix string) ([]*swire.DKVSRecord, int, error) {
	return p.Subscribe(dkvsindexer.Subscription{Type: dkvsindexer.SubscriptionPrefix, Target: prefix})
}

func (p *SatsNetDKVSClient) UnsubscribePrefix(prefix string) ([]dkvsindexer.Subscription, error) {
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
	return nil, ErrDKVSMessageManagerRequired
}

func (p *SatsNetDKVSClient) SendSignedMailboxMessage(wallet common.Wallet, mailboxID, msgID string,
	encryptedMessage []byte, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	return nil, ErrDKVSMessageManagerRequired
}

func (p *SatsNetDKVSClient) SendSignedMailboxMessageWithAutopay(wallet common.Wallet, mailboxID, msgID string,
	encryptedMessage []byte, opts dkvsindexer.RecordOptions, autopay DKVSAutopayOptions) (*swire.DKVSRecord, error) {
	return nil, ErrDKVSMessageManagerRequired
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
	return p.Tombstone(tombstone)
}

func (p *SatsNetDKVSClient) DeleteMessage(wallet common.Wallet, mailboxID, senderID, msgID string,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	return nil, ErrDKVSMessageManagerRequired
}

func (p *SatsNetDKVSClient) SubscribeMailbox(mailboxID string) ([]*swire.DKVSRecord, int, error) {
	return p.ReadMailboxMessages(mailboxID, 0, 0)
}

func (p *SatsNetDKVSClient) UnsubscribeMailbox(mailboxID string) ([]dkvsindexer.Subscription, error) {
	if _, err := mailboxSubscriptionTarget(mailboxID); err != nil {
		return nil, err
	}
	return p.ListSubscriptions()
}

func (p *SatsNetDKVSClient) PutNameRecord(record *swire.DKVSRecord) (*swire.DKVSRecord, error) {
	if err := requireDKVSRecordNamespace(record, "name"); err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) PutSignedNameRecord(wallet common.Wallet, name string, value []byte,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
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
	record, err := p.GetNameRecord(name)
	if err != nil {
		return nil, err
	}
	return &DKVSNameResolution{CanonicalName: name, NameID: dkvsindexer.NormalizeNameID(name), Record: record}, nil
}

func (p *SatsNetDKVSClient) PutServiceRecord(record *swire.DKVSRecord) (*swire.DKVSRecord, error) {
	if err := requireDKVSRecordNamespace(record, "svc"); err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) PutSignedServiceRecord(wallet common.Wallet, serviceName, path string,
	value []byte, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
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
