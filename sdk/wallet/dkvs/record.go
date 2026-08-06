package dkvs

import (
	"bytes"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type accountSchnorrSigner interface {
	SignSchnorrMessage(hash []byte) ([]byte, error)
}

func AccountID(wallet common.Wallet) (string, error) {
	if wallet == nil || wallet.GetPubKey() == nil {
		return "", dkvsindexer.ErrInvalidSignature
	}
	return dkvsindexer.CanonicalAccountID(wallet.GetPubKey().SerializeCompressed())
}

func isAccountScopedNamespace(namespace string) bool {
	switch namespace {
	case "account", "personal", "mail", "blob":
		return true
	default:
		return false
	}
}

func WalletPubKey(wallet common.Wallet) ([]byte, error) {
	if wallet == nil || wallet.GetPubKey() == nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	return wallet.GetPubKey().SerializeCompressed(), nil
}

func SignAccountRecord(wallet common.Wallet, record *swire.DKVSRecord) error {
	if wallet == nil || record == nil || record.Version != dkvsindexer.Version {
		return dkvsindexer.ErrInvalidSignature
	}
	signer, ok := wallet.(accountSchnorrSigner)
	if !ok {
		return dkvsindexer.ErrInvalidSignature
	}
	hash := dkvsindexer.SigningHash(record)
	signature, err := signer.SignSchnorrMessage(hash[:])
	if err != nil {
		return err
	}
	record.PubKey = nil
	record.Signature = append([]byte(nil), signature...)
	return nil
}

func SignRecord(wallet common.Wallet, record *swire.DKVSRecord) error {
	if wallet == nil || record == nil {
		return dkvsindexer.ErrInvalidSignature
	}
	parsed, err := dkvsindexer.ParseKey(record.Key)
	if err != nil {
		return err
	}
	if isAccountScopedNamespace(parsed.Namespace) {
		return SignAccountRecord(wallet, record)
	}
	pubKey, err := WalletPubKey(wallet)
	if err != nil {
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

func NewSignedRecord(wallet common.Wallet, key string, value []byte,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	parsed, err := dkvsindexer.ParseKey(key)
	if err != nil {
		return nil, err
	}
	var record *swire.DKVSRecord
	if isAccountScopedNamespace(parsed.Namespace) {
		record, err = dkvsindexer.NewAccountRecord(key, value, opts)
	} else {
		pubKey, pubKeyErr := WalletPubKey(wallet)
		if pubKeyErr != nil {
			return nil, dkvsindexer.ErrInvalidSignature
		}
		record, err = dkvsindexer.NewRecord(key, value, pubKey, opts)
	}
	if err != nil {
		return nil, err
	}
	if err := SignRecord(wallet, record); err != nil {
		return nil, err
	}
	return record, nil
}

func NewSignedTombstone(wallet common.Wallet, key string,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	opts.Flags |= dkvsindexer.FlagTombstone
	return NewSignedRecord(wallet, key, nil, opts)
}

func NewSignedRenewalRecord(wallet common.Wallet, existing *swire.DKVSRecord,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	pubKey, err := WalletPubKey(wallet)
	if err != nil || existing == nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	if dkvsindexer.IsTombstone(existing.Flags) {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if proof, err := dkvsindexer.ParseFeeProof(existing.FeeProof); err == nil &&
		proof.Mode == dkvsindexer.FeeModeAutopay {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	parsed, err := dkvsindexer.ParseKey(existing.Key)
	if err != nil {
		return nil, err
	}
	if isAccountScopedNamespace(parsed.Namespace) {
		if len(existing.PubKey) != 0 {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		want, err := dkvsindexer.RecordSignerAccountID(existing, parsed)
		if err != nil {
			return nil, err
		}
		got, err := dkvsindexer.CanonicalAccountID(pubKey)
		if err != nil || got != want {
			return nil, dkvsindexer.ErrPermissionDenied
		}
	} else if !bytes.Equal(existing.PubKey, pubKey) {
		return nil, dkvsindexer.ErrPermissionDenied
	}
	if opts.TTL == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	record := *existing
	record.PubKey = append([]byte(nil), existing.PubKey...)
	record.Value = append([]byte(nil), existing.Value...)
	record.Signature = nil
	record.IssueHeight = opts.IssueHeight
	record.TTL = opts.TTL
	if dkvsindexer.RecordExpiryHeight(&record) == 0 ||
		dkvsindexer.RecordExpiryHeight(&record) <= dkvsindexer.RecordExpiryHeight(existing) {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if opts.FeeProof != nil {
		record.FeeProof = append([]byte(nil), opts.FeeProof...)
	} else {
		record.FeeProof = append([]byte(nil), existing.FeeProof...)
	}
	if dkvsindexer.RecordSize(&record) > swire.MaxDKVSRecordSize ||
		len(record.Value) > dkvsindexer.MaxRecordValueSize {
		return nil, dkvsindexer.ErrRecordTooLarge
	}
	if err := SignRecord(wallet, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func AttachFeeProof(record *swire.DKVSRecord, proof *dkvsindexer.FeeProof) error {
	if record == nil || proof == nil {
		return dkvsindexer.ErrInvalidFeeProof
	}
	if proof.Mode == dkvsindexer.FeeModeAutopay {
		record.TTL = 0
	}
	encoded, err := dkvsindexer.EncodeFeeProof(proof)
	if err != nil {
		return err
	}
	record.FeeProof = encoded
	return nil
}
