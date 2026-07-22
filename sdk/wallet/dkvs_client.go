package wallet

import (
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
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

type dkvsConfigResp struct {
	dkvsBaseResp
	Data *dkvsindexer.FreeLocalCachePolicy `json:"data,omitempty"`
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

func NewSatsNetDKVSClient(scheme, host, proxy string, http HttpClient) *SatsNetDKVSClient {
	if http == nil {
		http = &NetClient{Client: nethttp.DefaultClient}
	}
	return &SatsNetDKVSClient{RESTClient: NewRESTClient(scheme, host, proxy, http)}
}

func (p *SatsNetDKVSClient) PutRecord(record *swire.DKVSRecord) (*swire.DKVSRecord, error) {
	var resp dkvsRecordResp
	if err := p.postJSON("/v3/dkvs/records", record, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (p *SatsNetDKVSClient) Tombstone(record *swire.DKVSRecord) (*swire.DKVSRecord, error) {
	var resp dkvsRecordResp
	if err := p.postJSON("/v3/dkvs/tombstone", record, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (p *SatsNetDKVSClient) GetRecord(key string) (*swire.DKVSRecord, error) {
	var resp dkvsRecordResp
	url := p.GetUrl("/v3/dkvs/records")
	url.Query = map[string]string{"key": key}
	if err := p.getJSON(url, &resp); err != nil {
		return nil, err
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

// GetFreeLocalCachePolicy returns the policy of the node this wallet is
// connected to. It is node-local capacity information, not a network rule.
func (p *SatsNetDKVSClient) GetFreeLocalCachePolicy() (*dkvsindexer.FreeLocalCachePolicy, error) {
	var resp dkvsConfigResp
	if err := p.getPathJSON("/v3/dkvs/config", &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
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
		record.Key, parsed.Namespace, swire.MaxDKVSRecordSize, record.ExpiryHeight, poolContract, "",
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
		swire.MaxDKVSRecordSize, record.ExpiryHeight)
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

func (p *SatsNetDKVSClient) PutBlobRecords(manifestRecord *swire.DKVSRecord, chunkRecords []*swire.DKVSRecord) error {
	if _, err := p.PutRecord(manifestRecord); err != nil {
		return err
	}
	for _, record := range chunkRecords {
		if _, err := p.PutRecord(record); err != nil {
			return err
		}
	}
	return nil
}

func (p *SatsNetDKVSClient) PutBlob(wallet common.Wallet, objectID string, data []byte, metadata []byte, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, []*swire.DKVSRecord, error) {
	if len(data) == 0 {
		return nil, nil, dkvsindexer.ErrBlobManifestInvalid
	}
	chunks := chunkBlobData(data, 0)
	manifestRecord, chunkRecords, err := p.PutChunkedBlob(wallet, objectID, chunks, metadata, opts)
	if err != nil {
		return nil, nil, err
	}
	return manifestRecord, chunkRecords, nil
}

// PutBlobWithAutopay attaches a fee proof to the manifest and every chunk,
// then re-signs each record with the owning wallet before publication.
func (p *SatsNetDKVSClient) PutBlobWithAutopay(wallet common.Wallet, objectID string, data []byte,
	metadata []byte, opts dkvsindexer.RecordOptions, autopay DKVSAutopayOptions) (*swire.DKVSRecord, []*swire.DKVSRecord, error) {
	if len(data) == 0 {
		return nil, nil, dkvsindexer.ErrBlobManifestInvalid
	}
	chunks := chunkBlobData(data, 0)
	manifestRecord, chunkRecords, err := BuildDKVSSignedBlobRecords(wallet, objectID, chunks, metadata, opts)
	if err != nil {
		return nil, nil, err
	}
	all := append([]*swire.DKVSRecord{manifestRecord}, chunkRecords...)
	for _, record := range all {
		if err := attachDKVSAutopayFeeProof(wallet, record, autopay); err != nil {
			return nil, nil, err
		}
	}
	if err := p.PutBlobRecords(manifestRecord, chunkRecords); err != nil {
		return nil, nil, err
	}
	return manifestRecord, chunkRecords, nil
}

func (p *SatsNetDKVSClient) PutChunkedBlob(wallet common.Wallet, objectID string, chunks [][]byte, metadata []byte, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, []*swire.DKVSRecord, error) {
	manifestRecord, chunkRecords, err := BuildDKVSSignedBlobRecords(wallet, objectID, chunks, metadata, opts)
	if err != nil {
		return nil, nil, err
	}
	if err := p.PutBlobRecords(manifestRecord, chunkRecords); err != nil {
		return nil, nil, err
	}
	return manifestRecord, chunkRecords, nil
}

func (p *SatsNetDKVSClient) GetBlob(accountID, objectID string, policy dkvsindexer.BlobPolicy) (*dkvsindexer.BlobManifest, []byte, error) {
	if policy.MaxTotalSize == 0 {
		policy.MaxTotalSize = dkvsindexer.DefaultBlobMaxTotalSize
	}
	if policy.MaxChunkSize == 0 {
		policy.MaxChunkSize = dkvsindexer.DefaultBlobMaxChunkSize
	}
	if policy.MaxChunks == 0 {
		policy.MaxChunks = dkvsindexer.DefaultBlobMaxChunks
	}
	manifestKey, err := dkvsindexer.BlobManifestKey(accountID, objectID)
	if err != nil {
		return nil, nil, err
	}
	manifestRecord, err := p.GetRecord(manifestKey)
	if err != nil {
		return nil, nil, err
	}
	manifest, err := dkvsindexer.ParseBlobManifestValue(manifestRecord.Value, policy)
	if err != nil {
		return nil, nil, err
	}
	chunkPrefix := "/blob/" + accountID + "/" + objectID + "/chunk/"
	chunkRecords, total, err := p.ListRecords(chunkPrefix, 0, int(manifest.ChunkCount))
	if err != nil {
		return nil, nil, err
	}
	if total != int(manifest.ChunkCount) || len(chunkRecords) != int(manifest.ChunkCount) {
		return nil, nil, dkvsindexer.ErrBlobChunkInvalid
	}
	return dkvsindexer.AssembleBlobFromRecords(manifestRecord, chunkRecords, policy)
}

func (p *SatsNetDKVSClient) GetChunkedBlob(accountID, objectID string, policy dkvsindexer.BlobPolicy) (*dkvsindexer.BlobManifest, []byte, error) {
	return p.GetBlob(accountID, objectID, policy)
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

func chunkBlobData(data []byte, chunkSize int) [][]byte {
	if chunkSize <= 0 {
		// MaxDKVSRecordSize limits the complete wire record, not just Value.
		// Reserve the maximum key/pubkey/signature/fee-proof sizes plus fixed
		// and varint fields so signed chunks remain serializable at all allowed
		// DKVS field sizes.
		chunkSize = swire.MaxDKVSRecordSize - swire.MaxDKVSKeySize - swire.MaxDKVSPubKeySize -
			swire.MaxDKVSSignatureSize - swire.MaxDKVSFeeProofSize - 64
	}
	chunks := make([][]byte, 0, (len(data)+chunkSize-1)/chunkSize)
	for len(data) > 0 {
		n := chunkSize
		if len(data) < n {
			n = len(data)
		}
		chunks = append(chunks, append([]byte{}, data[:n]...))
		data = data[n:]
	}
	return chunks
}
