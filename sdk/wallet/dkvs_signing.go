package wallet

import (
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type dkvsAccountSchnorrSigner interface {
	SignSchnorrMessage(hash []byte) ([]byte, error)
}

func NewDKVSSignedRecord(wallet common.Wallet, key string, value []byte,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	return dkvscore.NewSignedRecord(wallet, key, value, opts)
}

func NewDKVSSignedTombstone(wallet common.Wallet, key string,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	return dkvscore.NewSignedTombstone(wallet, key, opts)
}

func NewDKVSSignedRenewalRecord(wallet common.Wallet, existing *swire.DKVSRecord,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	return dkvscore.NewSignedRenewalRecord(wallet, existing, opts)
}

func SignDKVSRecord(wallet common.Wallet, record *swire.DKVSRecord) error {
	return dkvscore.SignRecord(wallet, record)
}

func SignDKVSAccountRecord(wallet common.Wallet, record *swire.DKVSRecord) error {
	return dkvscore.SignAccountRecord(wallet, record)
}

func AttachDKVSFeeProof(record *swire.DKVSRecord, proof *dkvsindexer.FeeProof) error {
	return dkvscore.AttachFeeProof(record, proof)
}

func dkvsWalletPubKey(wallet common.Wallet) ([]byte, error) {
	return dkvscore.WalletPubKey(wallet)
}

func dkvsAccountID(wallet common.Wallet) (string, error) {
	return dkvscore.AccountID(wallet)
}
