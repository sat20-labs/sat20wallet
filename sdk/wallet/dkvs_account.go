package wallet

import (
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type dkvsAccountSchnorrSigner interface {
	SignSchnorrMessage(hash []byte) ([]byte, error)
}

// SignSchnorrMessage signs a 32-byte digest with the current SAT20 subaccount
// payment key using BIP340. The corresponding x-only public key is the DKVS v1
// account ID.
func (p *InternalWallet) SignSchnorrMessage(hash []byte) ([]byte, error) {
	if p == nil || len(hash) != 32 {
		return nil, fmt.Errorf("invalid Schnorr message hash")
	}
	p.mutex.Lock()
	priv := p.getPaymentPrivKey()
	p.mutex.Unlock()
	if priv == nil {
		return nil, fmt.Errorf("no payment private key available")
	}
	btcecPriv, _ := btcec.PrivKeyFromBytes(priv.Serialize())
	sig, err := schnorr.Sign(btcecPriv, hash)
	if err != nil {
		return nil, err
	}
	return sig.Serialize(), nil
}

func dkvsAccountID(wallet common.Wallet) (string, error) {
	if wallet == nil || wallet.GetPubKey() == nil {
		return "", dkvsindexer.ErrInvalidSignature
	}
	return dkvsindexer.CanonicalAccountID(wallet.GetPubKey().SerializeCompressed())
}

func SignDKVSAccountRecord(wallet common.Wallet, record *swire.DKVSRecord) error {
	if wallet == nil || record == nil || record.Version != dkvsindexer.Version {
		return dkvsindexer.ErrInvalidSignature
	}
	signer, ok := wallet.(dkvsAccountSchnorrSigner)
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

func NewDKVSAccountSignedRecord(wallet common.Wallet, key string, value []byte,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	record, err := dkvsindexer.NewAccountRecord(key, value, opts)
	if err != nil {
		return nil, err
	}
	if err := SignDKVSAccountRecord(wallet, record); err != nil {
		return nil, err
	}
	return record, nil
}

func newDKVSAccountSignedRecordWithAutopay(wallet common.Wallet, key string, value []byte,
	opts dkvsindexer.RecordOptions, autopay DKVSAutopayOptions) (*swire.DKVSRecord, error) {
	opts.TTL = 0
	opts.ExpiryHeight = 0
	record, err := dkvsindexer.NewAccountRecord(key, value, opts)
	if err != nil {
		return nil, err
	}
	params := autopay.AddressParams
	if params == nil {
		params = GetChainParam_SatsNet()
	}
	poolContract := autopay.PoolContract
	if poolContract == "" {
		defaults := dkvsindexer.NetworkDefaultsForParams(params)
		poolContract = defaults.AutopayContract
	}
	if poolContract == "" {
		return nil, dkvsindexer.ErrInvalidFeeProof
	}
	parsed, err := dkvsindexer.ParseKey(record.Key)
	if err != nil {
		return nil, err
	}
	proof, err := dkvsindexer.NewAutopayFeeProof(
		record.Key, parsed.Namespace, swire.MaxDKVSRecordSize, record.ExpiryHeight, poolContract, "",
	)
	if err != nil {
		return nil, err
	}
	if err := AttachDKVSFeeProof(record, proof); err != nil {
		return nil, err
	}
	if err := SignDKVSAccountRecord(wallet, record); err != nil {
		return nil, err
	}
	return record, nil
}

func (p *SatsNetDKVSClient) PutAccountSignedRecord(wallet common.Wallet, key string, value []byte,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	record, err := NewDKVSAccountSignedRecord(wallet, key, value, opts)
	if err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) PutAccountSignedRecordWithAutopay(wallet common.Wallet, key string,
	value []byte, opts dkvsindexer.RecordOptions, autopay DKVSAutopayOptions) (*swire.DKVSRecord, error) {
	record, err := newDKVSAccountSignedRecordWithAutopay(wallet, key, value, opts, autopay)
	if err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) PutAccountPersonalRecord(wallet common.Wallet, path string, value []byte,
	opts dkvsindexer.RecordOptions, autopay *DKVSAutopayOptions) (*swire.DKVSRecord, error) {
	accountID, err := dkvsAccountID(wallet)
	if err != nil {
		return nil, err
	}
	key, err := dkvsindexer.AccountPersonalKey(accountID, path)
	if err != nil {
		return nil, err
	}
	if autopay != nil {
		return p.PutAccountSignedRecordWithAutopay(wallet, key, value, opts, *autopay)
	}
	return p.PutAccountSignedRecord(wallet, key, value, opts)
}

func (p *SatsNetDKVSClient) PublishAccountAddress(wallet common.Wallet, network, address string,
	opts dkvsindexer.RecordOptions, autopay *DKVSAutopayOptions) (*swire.DKVSRecord, error) {
	accountID, err := dkvsAccountID(wallet)
	if err != nil {
		return nil, err
	}
	key, err := dkvsindexer.AccountMappingKey(network, address)
	if err != nil {
		return nil, err
	}
	value, err := dkvsindexer.EncodeAccountMappingValue(accountID)
	if err != nil {
		return nil, err
	}
	if autopay != nil {
		return p.PutAccountSignedRecordWithAutopay(wallet, key, value, opts, *autopay)
	}
	return p.PutAccountSignedRecord(wallet, key, value, opts)
}

func (p *SatsNetDKVSClient) ResolveAccountAddress(network, address string,
	opts dkvsindexer.RecordVerificationOptions) (string, *swire.DKVSRecord, error) {
	key, err := dkvsindexer.AccountMappingKey(network, address)
	if err != nil {
		return "", nil, err
	}
	record, err := p.GetRecord(key)
	if err != nil {
		return "", nil, err
	}
	opts.ExpectedKey = key
	if err := dkvsindexer.VerifyAccountRecordForClient(record, opts); err != nil {
		return "", nil, err
	}
	accountID, err := dkvsindexer.DecodeAccountMappingValue(record.Value)
	if err != nil {
		return "", nil, err
	}
	return accountID, record, nil
}

func (p *SatsNetDKVSClient) SendAccountMailboxMessage(wallet common.Wallet, mailboxID, msgID string,
	value []byte, opts dkvsindexer.RecordOptions, autopay *DKVSAutopayOptions) (*swire.DKVSRecord, error) {
	senderID, err := dkvsAccountID(wallet)
	if err != nil {
		return nil, err
	}
	key, err := dkvsindexer.MailMsgKey(mailboxID, senderID, msgID)
	if err != nil {
		return nil, err
	}
	if autopay != nil {
		return p.PutAccountSignedRecordWithAutopay(wallet, key, value, opts, *autopay)
	}
	return p.PutAccountSignedRecord(wallet, key, value, opts)
}
