package wallet

import (
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type DKVSBlob = dkvscore.Blob

func EncodeDKVSBlobValue(data, metadata []byte) ([]byte, error) {
	return dkvscore.EncodeBlobValue(data, metadata)
}

func DecodeDKVSBlobValue(value []byte) (*DKVSBlob, error) {
	return dkvscore.DecodeBlobValue(value)
}

func buildDKVSBlobRecord(wallet common.Wallet, blobKey string, data, metadata []byte,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {

	accountID, err := dkvsAccountID(wallet)
	if err != nil {
		return nil, err
	}
	key, err := dkvsindexer.BlobKey(accountID, blobKey)
	if err != nil {
		return nil, err
	}
	value, err := EncodeDKVSBlobValue(data, metadata)
	if err != nil {
		return nil, err
	}
	return NewDKVSAccountSignedRecord(wallet, key, value, opts)
}

func BuildDKVSSignedBlobRecord(wallet common.Wallet, blobKey string, data, metadata []byte,
	opts dkvsindexer.RecordOptions, autopay DKVSAutopayOptions) (*swire.DKVSRecord, error) {

	accountID, err := dkvsAccountID(wallet)
	if err != nil {
		return nil, err
	}
	key, err := dkvsindexer.BlobKey(accountID, blobKey)
	if err != nil {
		return nil, err
	}
	value, err := EncodeDKVSBlobValue(data, metadata)
	if err != nil {
		return nil, err
	}
	return newDKVSAccountSignedRecordWithAutopay(wallet, key, value, opts, autopay)
}

func BuildDKVSSignedBlobRecordFreeLocal(wallet common.Wallet, blobKey string, data, metadata []byte,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {

	if opts.TTL == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	record, err := buildDKVSBlobRecord(wallet, blobKey, data, metadata, opts)
	if err != nil {
		return nil, err
	}
	proof, err := dkvsindexer.NewFreeLocalFeeProof(
		record.Key, "blob", uint32(dkvsindexer.RecordSize(record)), dkvsindexer.RecordExpiryHeight(record),
	)
	if err != nil {
		return nil, err
	}
	if err := AttachDKVSFeeProof(record, proof); err != nil {
		return nil, err
	}
	if err := SignDKVSRecord(wallet, record); err != nil {
		return nil, err
	}
	return record, nil
}

func (p *SatsNetDKVSClient) PutBlobWithAutopay(wallet common.Wallet, blobKey string, data,
	metadata []byte, opts dkvsindexer.RecordOptions, autopay DKVSAutopayOptions) (*swire.DKVSRecord, error) {

	record, err := BuildDKVSSignedBlobRecord(wallet, blobKey, data, metadata, opts, autopay)
	if err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) PutBlobFreeLocal(wallet common.Wallet, blobKey string, data,
	metadata []byte, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {

	config, err := p.GetDKVSClientConfig()
	if err != nil {
		return nil, err
	}
	if !config.FreeLocal.Enabled {
		return nil, dkvsindexer.ErrFreeLocalDisabled
	}
	if opts.TTL == 0 || opts.TTL > config.FreeLocal.MaxTTL {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	value, err := EncodeDKVSBlobValue(data, metadata)
	if err != nil {
		return nil, err
	}
	if config.Blob.MaxValueSize <= 0 || len(value) > config.Blob.MaxValueSize {
		return nil, dkvsindexer.ErrRecordTooLarge
	}
	record, err := BuildDKVSSignedBlobRecordFreeLocal(wallet, blobKey, data, metadata, opts)
	if err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) GetBlob(accountID, blobKey string,
	opts dkvsindexer.RecordVerificationOptions) (*swire.DKVSRecord, *DKVSBlob, error) {

	key, err := dkvsindexer.BlobKey(accountID, blobKey)
	if err != nil {
		return nil, nil, err
	}
	record, err := p.GetRecord(key)
	if err != nil {
		return nil, nil, err
	}
	if opts.ExpectedKey == "" {
		opts.ExpectedKey = key
	}
	if err := dkvsindexer.VerifyBlobRecordForClient(record, opts); err != nil {
		return nil, nil, err
	}
	blob, err := DecodeDKVSBlobValue(record.Value)
	if err != nil {
		return nil, nil, fmt.Errorf("decode DKVS blob: %w", err)
	}
	return record, blob, nil
}
