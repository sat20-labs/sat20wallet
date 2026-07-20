package wallet

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const (
	RGB11ReceiveCapabilityVersion = uint8(1)
	RGB11ReceiveCapabilityPath    = "rgb11/receive"

	RGB11ReceiveCapabilityAddress = uint8(1 << 0)
	RGB11ReceiveCapabilityAny     = uint8(1 << 1)
)

var ErrRGB11TraditionalReceiveRequired = errors.New("receiver has no RGB11 DKVS address capability; use a traditional RGB invoice")

type RGB11ReceiveCapability struct {
	Version uint8 `json:"version"`
	Flags   uint8 `json:"flags"`
}

type RGB11ReceiveCapabilityOptions struct {
	RecordOptions dkvsindexer.RecordOptions `json:"record_options"`
	Autopay       *DKVSAutopayOptions       `json:"autopay,omitempty"`
	Flags         uint8                     `json:"flags"`
}

type RGB11AddressEndpoint struct {
	AccountID string `json:"account_id"`
	Address   string `json:"address"`
	MailboxID string `json:"mailbox_id"`

	CompressedPubKey []byte `json:"compressed_pubkey"`
	PkScript         []byte `json:"pk_script"`

	CapabilityFlags      uint8  `json:"capability_flags"`
	CapabilityRecordKey  string `json:"capability_record_key"`
	CapabilityRecordHash string `json:"capability_record_hash"`
	Temporary             bool   `json:"temporary"`
	ExpiryHeight          uint64 `json:"expiry_height"`
	TTL                   uint64 `json:"ttl"`
}

func encodeRGB11ReceiveCapability(capability RGB11ReceiveCapability) ([]byte, error) {
	if capability.Version != RGB11ReceiveCapabilityVersion ||
		capability.Flags&RGB11ReceiveCapabilityAddress == 0 {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	return []byte{capability.Version, capability.Flags}, nil
}

func decodeRGB11ReceiveCapability(value []byte) (RGB11ReceiveCapability, error) {
	if len(value) != 2 || value[0] != RGB11ReceiveCapabilityVersion ||
		value[1]&RGB11ReceiveCapabilityAddress == 0 {
		return RGB11ReceiveCapability{}, ErrRGB11TraditionalReceiveRequired
	}
	return RGB11ReceiveCapability{Version: value[0], Flags: value[1]}, nil
}

func nextRGB11CapabilityRecordOptions(client *SatsNetDKVSClient, key string, value []byte,
	opts dkvsindexer.RecordOptions) (dkvsindexer.RecordOptions, *swire.DKVSRecord) {
	if opts.Seq != 0 || client == nil {
		return opts, nil
	}
	existing, err := client.GetRecord(key)
	if err != nil || existing == nil || existing.Version != dkvsindexer.VersionV2 {
		opts.Seq = 1
		return opts, nil
	}
	if bytes.Equal(existing.Value, value) {
		opts.Seq = existing.Seq
	} else {
		opts.Seq = existing.Seq + 1
	}
	return opts, existing
}

func putRGB11CapabilityRecord(client *SatsNetDKVSClient, wallet *InternalWallet, key string, value []byte,
	opts dkvsindexer.RecordOptions, autopay *DKVSAutopayOptions) (*swire.DKVSRecord, error) {
	opts, existing := nextRGB11CapabilityRecordOptions(client, key, value, opts)
	if existing != nil && bytes.Equal(existing.Value, value) &&
		opts.ExpiryHeight <= existing.ExpiryHeight && opts.TTL <= existing.TTL {
		return existing, nil
	}
	if autopay != nil {
		return client.PutAccountSignedRecordWithAutopay(wallet, key, value, opts, *autopay)
	}
	return client.PutAccountSignedRecord(wallet, key, value, opts)
}

// EnableRGB11AddressReceive publishes the public address mapping and the
// minimal account capability. Callers should invoke this when DKVS wallet
// communication/storage is enabled for the current SAT20 subaccount.
func (p *Manager) EnableRGB11AddressReceive(client *SatsNetDKVSClient,
	options RGB11ReceiveCapabilityOptions) (*RGB11AddressEndpoint, error) {
	if p == nil || client == nil || p.wallet == nil {
		return nil, ErrRGB11Inconsistent
	}
	wallet, ok := p.wallet.(*InternalWallet)
	if !ok {
		return nil, fmt.Errorf("RGB11 address receive requires an internal wallet")
	}
	flags := options.Flags
	if flags == 0 {
		flags = RGB11ReceiveCapabilityAddress | RGB11ReceiveCapabilityAny
	}
	value, err := encodeRGB11ReceiveCapability(RGB11ReceiveCapability{
		Version: RGB11ReceiveCapabilityVersion,
		Flags:   flags,
	})
	if err != nil {
		return nil, err
	}
	accountID, err := dkvsAccountID(wallet)
	if err != nil {
		return nil, err
	}
	address := wallet.GetAddress()
	network := GetChainParam().Name
	mappingKey, err := dkvsindexer.AccountMappingKey(network, address)
	if err != nil {
		return nil, err
	}
	mappingValue, err := dkvsindexer.EncodeAccountMappingValue(accountID)
	if err != nil {
		return nil, err
	}
	if _, err := putRGB11CapabilityRecord(
		client, wallet, mappingKey, mappingValue, options.RecordOptions, options.Autopay,
	); err != nil {
		return nil, err
	}
	capabilityKey, err := dkvsindexer.PersonalKeyV2(accountID, RGB11ReceiveCapabilityPath)
	if err != nil {
		return nil, err
	}
	if _, err := putRGB11CapabilityRecord(
		client, wallet, capabilityKey, value, options.RecordOptions, options.Autopay,
	); err != nil {
		return nil, err
	}
	verify := dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())}
	return p.ResolveRGB11AddressEndpoint(client, address, verify)
}

func (p *Manager) ResolveRGB11AddressEndpoint(client *SatsNetDKVSClient, address string,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressEndpoint, error) {
	if client == nil || address == "" {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	if verify.Now == 0 {
		verify.Now = uint64(time.Now().UnixMilli())
	}
	accountID, _, err := client.ResolveAccountAddress(GetChainParam().Name, address, verify)
	if err != nil {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	key, err := dkvsindexer.PersonalKeyV2(accountID, RGB11ReceiveCapabilityPath)
	if err != nil {
		return nil, err
	}
	record, err := client.GetRecord(key)
	if err != nil {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	verify.ExpectedKey = key
	if err := dkvsindexer.VerifyAccountRecordForClient(record, verify); err != nil {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	capability, err := decodeRGB11ReceiveCapability(record.Value)
	if err != nil {
		return nil, err
	}
	pubKey, err := dkvsindexer.AccountPubKeyV2(accountID)
	if err != nil {
		return nil, err
	}
	pkScript, err := AddrToPkScript(address, GetChainParam())
	if err != nil {
		return nil, err
	}
	recordHash := dkvsindexer.RecordHash(record)
	return &RGB11AddressEndpoint{
		AccountID:              accountID,
		Address:                address,
		MailboxID:              accountID,
		CompressedPubKey:       pubKey,
		PkScript:               pkScript,
		CapabilityFlags:        capability.Flags,
		CapabilityRecordKey:    key,
		CapabilityRecordHash:   hex.EncodeToString(recordHash[:]),
		Temporary:              len(record.FeeProof) == 0,
		ExpiryHeight:           record.ExpiryHeight,
		TTL:                    record.TTL,
	}, nil
}
