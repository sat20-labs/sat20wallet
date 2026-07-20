package wallet

import (
	"bytes"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func NewDKVSSignedRecord(wallet common.Wallet, key string, value []byte, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	pubKey, err := dkvsWalletPubKey(wallet)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	record, err := dkvsindexer.NewRecord(key, value, pubKey, opts)
	if err != nil {
		return nil, err
	}
	if err := SignDKVSRecord(wallet, record); err != nil {
		return nil, err
	}
	return record, nil
}

func NewDKVSSignedTombstone(wallet common.Wallet, key string, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	opts.Flags |= dkvsindexer.FlagTombstone
	return NewDKVSSignedRecord(wallet, key, nil, opts)
}

func NewDKVSSignedRenewalRecord(wallet common.Wallet, existing *swire.DKVSRecord, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	pubKey, err := dkvsWalletPubKey(wallet)
	if err != nil || existing == nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	if dkvsindexer.IsTombstone(existing.Flags) {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if _, err := dkvsindexer.ParseKey(existing.Key); err != nil {
		return nil, err
	}
	if !bytes.Equal(existing.PubKey, pubKey) {
		return nil, dkvsindexer.ErrPermissionDenied
	}
	if opts.ExpiryHeight <= existing.ExpiryHeight {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	record := *existing
	record.PubKey = append([]byte{}, existing.PubKey...)
	record.Value = append([]byte{}, existing.Value...)
	record.Signature = nil
	record.IssueTime = opts.IssueTime
	if record.IssueTime == 0 {
		record.IssueTime = uint64(time.Now().UnixMilli())
	}
	if opts.TTL != 0 {
		record.TTL = opts.TTL
	}
	record.ExpiryHeight = opts.ExpiryHeight
	if opts.FeeProof != nil {
		record.FeeProof = append([]byte{}, opts.FeeProof...)
	} else {
		record.FeeProof = append([]byte{}, existing.FeeProof...)
	}
	if dkvsindexer.RecordSize(&record) > swire.MaxDKVSRecordSize ||
		len(record.Value) > dkvsindexer.MaxRecordValueSize {
		return nil, dkvsindexer.ErrRecordTooLarge
	}
	if err := SignDKVSRecord(wallet, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func SignDKVSRecord(wallet common.Wallet, record *swire.DKVSRecord) error {
	pubKey, err := dkvsWalletPubKey(wallet)
	if err != nil || record == nil {
		return dkvsindexer.ErrInvalidSignature
	}
	record.PubKey = pubKey
	sig, err := wallet.SignMessage(dkvsindexer.SigningMessage(record))
	if err != nil {
		return err
	}
	record.Signature = sig
	return nil
}

func AttachDKVSFeeProof(record *swire.DKVSRecord, proof *dkvsindexer.FeeProof) error {
	if record == nil || proof == nil {
		return dkvsindexer.ErrInvalidFeeProof
	}
	encoded, err := dkvsindexer.EncodeFeeProof(proof)
	if err != nil {
		return err
	}
	record.FeeProof = encoded
	return nil
}

func BuildDKVSSignedBlobRecords(wallet common.Wallet, objectID string, chunks [][]byte, metadata []byte, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, []*swire.DKVSRecord, error) {
	pubKey, err := dkvsWalletPubKey(wallet)
	if err != nil {
		return nil, nil, dkvsindexer.ErrInvalidSignature
	}
	// A blob is one logical wallet-signed object. Its manifest and every chunk
	// must share the same issue time or client-side assembly will reject it.
	if opts.IssueTime == 0 {
		opts.IssueTime = uint64(time.Now().UnixMilli())
	}
	accountID := dkvsindexer.AccountID(pubKey)
	manifest, manifestValue, err := dkvsindexer.BuildBlobManifest(chunks, metadata, opts.TTL, opts.ExpiryHeight)
	if err != nil {
		return nil, nil, err
	}
	manifestKey, err := dkvsindexer.BlobManifestKey(accountID, objectID)
	if err != nil {
		return nil, nil, err
	}
	manifestRecord, err := NewDKVSSignedRecord(wallet, manifestKey, manifestValue, opts)
	if err != nil {
		return nil, nil, err
	}
	chunkRecords := make([]*swire.DKVSRecord, 0, manifest.ChunkCount)
	for n, chunk := range chunks {
		chunkKey, err := dkvsindexer.BlobChunkKey(accountID, objectID, uint32(n))
		if err != nil {
			return nil, nil, err
		}
		chunkRecord, err := NewDKVSSignedRecord(wallet, chunkKey, chunk, opts)
		if err != nil {
			return nil, nil, err
		}
		chunkRecords = append(chunkRecords, chunkRecord)
	}
	return manifestRecord, chunkRecords, nil
}

func dkvsWalletPubKey(wallet common.Wallet) ([]byte, error) {
	if wallet == nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	pubKey := wallet.GetPubKey()
	if pubKey == nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	return pubKey.SerializeCompressed(), nil
}
