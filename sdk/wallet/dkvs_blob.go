package wallet

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

var dkvsBlobEnvelopeMagic = []byte{'D', 'K', 'B', '1'}

type DKVSBlob struct {
	Data     []byte
	Metadata []byte
}

// EncodeDKVSBlobValue keeps metadata optional. Raw data is stored without an
// envelope when metadata is empty, allowing the complete 1 MiB value budget to
// remain available to ordinary blob users.
func EncodeDKVSBlobValue(data, metadata []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if len(metadata) == 0 {
		if len(data) > swire.MaxDKVSBlobValueSize {
			return nil, dkvsindexer.ErrRecordTooLarge
		}
		return append([]byte(nil), data...), nil
	}
	if len(metadata) > int(^uint32(0)) {
		return nil, dkvsindexer.ErrRecordTooLarge
	}
	value := make([]byte, 8+len(metadata)+len(data))
	copy(value, dkvsBlobEnvelopeMagic)
	binary.BigEndian.PutUint32(value[4:8], uint32(len(metadata)))
	copy(value[8:], metadata)
	copy(value[8+len(metadata):], data)
	if len(value) > swire.MaxDKVSBlobValueSize {
		return nil, dkvsindexer.ErrRecordTooLarge
	}
	return value, nil
}

func DecodeDKVSBlobValue(value []byte) (*DKVSBlob, error) {
	if len(value) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	blob := &DKVSBlob{}
	if len(value) < 8 || !bytes.Equal(value[:4], dkvsBlobEnvelopeMagic) {
		blob.Data = append([]byte(nil), value...)
		return blob, nil
	}
	metadataSize := int(binary.BigEndian.Uint32(value[4:8]))
	if metadataSize <= 0 || metadataSize > len(value)-8 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	blob.Metadata = append([]byte(nil), value[8:8+metadataSize]...)
	blob.Data = append([]byte(nil), value[8+metadataSize:]...)
	if len(blob.Data) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return blob, nil
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
		record.Key, "blob", uint32(dkvsindexer.RecordSize(record)), record.ExpiryHeight,
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
