package wallet

import (
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type dkvsDirectRecordBuilder func(dkvsindexer.RecordOptions) (*swire.DKVSRecord, error)

func directPathKeyState(snapshot *dkvsindexer.PathSnapshot, key string) (*swire.DKVSRecord, uint64, error) {
	if snapshot == nil || snapshot.PathMeta == nil || key == "" {
		return nil, 0, dkvsindexer.ErrInvalidRecord
	}
	var existing *swire.DKVSRecord
	for _, record := range snapshot.Records {
		if record == nil || record.Key != key {
			continue
		}
		if existing == nil || dkvsindexer.CompareRecords(existing, record) < 0 {
			existing = record
		}
	}
	var floorSeq uint64
	for _, floor := range snapshot.DeleteFloors {
		if floor.Key == key && floor.FloorSeq > floorSeq {
			floorSeq = floor.FloorSeq
		}
	}
	if existing != nil && existing.Seq > floorSeq {
		floorSeq = existing.Seq
	}
	return existing, floorSeq, nil
}

// putSignedPathRecordV1 is the explicit low-level V1 write path for callers
// that intentionally use SatsNetDKVSClient without dkvsManager. It derives the
// CAS state, generation and trusted block height from one verified path
// snapshot before signing, avoiding a GET/pathmeta TOCTOU window and HTTP
// not-found ambiguity.
func (p *SatsNetDKVSClient) putSignedPathRecordV1(key string,
	opts dkvsindexer.RecordOptions, build dkvsDirectRecordBuilder) (*swire.DKVSRecord, error) {

	if p == nil || build == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	path, err := dkvsindexer.CollectionPathForKey(key)
	if err != nil {
		return nil, err
	}
	snapshot, err := p.SyncPath(path, dkvsindexer.RecordVerificationOptions{})
	if err != nil {
		return nil, err
	}
	if snapshot == nil || snapshot.PathMeta == nil || snapshot.PathMeta.Path != path ||
		snapshot.PathMeta.Generation == ^uint64(0) {
		return nil, dkvsindexer.ErrStaleGeneration
	}

	existing, floorSeq, err := directPathKeyState(snapshot, key)
	if err != nil {
		return nil, err
	}
	nextSeq := floorSeq + 1
	if nextSeq == 0 {
		return nil, dkvsindexer.ErrInvalidSequence
	}
	precondition := dkvsindexer.WritePrecondition{ExpectAbsent: true}
	if existing != nil {
		hash := dkvsindexer.RecordHash(existing)
		precondition = dkvsindexer.WritePrecondition{ExpectedHash: &hash}
	}
	if opts.Seq == 0 {
		opts.Seq = nextSeq
	} else if opts.Seq != nextSeq {
		return nil, dkvsindexer.ErrInvalidSequence
	}
	opts.IssueHeight = snapshot.PathMeta.ViewHeight

	record, err := build(opts)
	if err != nil {
		return nil, err
	}
	if record == nil || record.Key != key || record.Seq != opts.Seq ||
		record.IssueHeight != opts.IssueHeight {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	mutations := []dkvsindexer.CASMutation{{Record: record, Precondition: precondition}}
	conditions := []dkvsindexer.PathWritePrecondition{{
		Path: path, ExpectedRoot: snapshot.PathMeta.StateRoot,
		ExpectedGeneration: snapshot.PathMeta.Generation,
	}}
	result, err := p.PutRecordBatchCASV1(mutations, conditions)
	if err != nil {
		return nil, err
	}
	if result == nil || len(result.Records) != 1 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return result.Records[0], nil
}

// PutSignedRecordWithAutopayV1 performs a direct AUTOPAY write using the
// normative path-oriented V1 protocol. Applications managed by Manager should
// continue to use dkvsStore; this API exists for integration tools and tests.
func (p *SatsNetDKVSClient) PutSignedRecordWithAutopayV1(owner common.Wallet,
	key string, value []byte, opts dkvsindexer.RecordOptions,
	autopay DKVSAutopayOptions) (*swire.DKVSRecord, error) {

	return p.putSignedPathRecordV1(key, opts, func(prepared dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
		return newSignedRecordWithAutopay(owner, key, value, prepared, autopay)
	})
}

func (p *SatsNetDKVSClient) SendSignedMailboxMessageWithAutopayV1(owner common.Wallet,
	mailboxID, msgID string, encryptedMessage []byte, opts dkvsindexer.RecordOptions,
	autopay DKVSAutopayOptions) (*swire.DKVSRecord, error) {

	pubKey, err := dkvsWalletPubKey(owner)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	key, err := dkvsindexer.MailMsgKey(mailboxID, dkvsindexer.AccountID(pubKey), msgID)
	if err != nil {
		return nil, err
	}
	return p.PutSignedRecordWithAutopayV1(owner, key, encryptedMessage, opts, autopay)
}

func (p *SatsNetDKVSClient) TombstoneSignedV1(owner common.Wallet, key string,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {

	return p.putSignedPathRecordV1(key, opts, func(prepared dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
		return NewDKVSSignedTombstone(owner, key, prepared)
	})
}

func (p *SatsNetDKVSClient) DeleteMessageV1(owner common.Wallet,
	mailboxID, senderID, msgID string,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {

	key, err := dkvsindexer.MailMsgKey(mailboxID, senderID, msgID)
	if err != nil {
		return nil, err
	}
	return p.TombstoneSignedV1(owner, key, opts)
}
