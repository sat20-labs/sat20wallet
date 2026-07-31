package wallet

import (
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type dkvsDirectRecordBuilder func(dkvsindexer.RecordOptions) (*swire.DKVSRecord, error)

// putSignedPathRecordV1 is the explicit low-level V1 write path for callers
// that intentionally use SatsNetDKVSClient without dkvsManager. It derives the
// CAS state, generation and server time from one verified path snapshot before
// signing, avoiding a GET/pathmeta TOCTOU window and HTTP not-found ambiguity.
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

	var existing *swire.DKVSRecord
	var floorSeq uint64
	var previousIssue uint64
	for _, candidate := range snapshot.Records {
		if candidate == nil || candidate.Key != key {
			continue
		}
		if candidate.Seq > floorSeq {
			floorSeq = candidate.Seq
		}
		if candidate.IssueTime > previousIssue {
			previousIssue = candidate.IssueTime
		}
		if !dkvsindexer.IsTombstone(candidate.Flags) &&
			!dkvsindexer.IsExpired(candidate, snapshot.PathMeta.ViewHeight, snapshot.ServerTimeMS) {
			existing = candidate
		}
	}
	for _, floor := range snapshot.DeleteFloors {
		if floor.Key == key && floor.FloorSeq > floorSeq {
			floorSeq = floor.FloorSeq
		}
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
	opts.PathGeneration = snapshot.PathMeta.Generation + 1
	if opts.PathGeneration <= snapshot.PathMeta.Generation {
		return nil, dkvsindexer.ErrStaleGeneration
	}
	serverTime := snapshot.ServerTimeMS
	if serverTime == 0 {
		serverTime = uint64(time.Now().UnixMilli())
	}
	if serverTime <= previousIssue {
		serverTime = previousIssue + 1
	}
	opts.IssueTime = serverTime

	record, err := build(opts)
	if err != nil {
		return nil, err
	}
	if record == nil || record.Key != key || record.Seq != opts.Seq ||
		record.PathGeneration != opts.PathGeneration || record.IssueTime != opts.IssueTime {
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
