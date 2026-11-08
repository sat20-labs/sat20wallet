package wallet

import (
	"bytes"
	"encoding/hex"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"strings"

	indexercommon "github.com/sat20-labs/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// RegisterRGB11SignedContract submits an authority-signed public technical
// record to the Core business route. The route independently validates its
// contract; this client additionally requires an authenticated readback.
// Ordinary wallet clients have no Core signing key and cannot use this to
// register arbitrary names or private recovery data.
func (p *SatsNetDKVSClient) RegisterRGB11SignedContract(record *swire.DKVSRecord) (*rgb11wallet.RGB11Registration, error) {
	if record == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	id := strings.TrimPrefix(record.Key, rgb11wallet.RGB11RegistryPath+"/")
	key, err := rgb11wallet.RGB11RegistryKey(id)
	if err != nil || key != record.Key {
		return nil, dkvsindexer.ErrInvalidKey
	}
	if _, err := rgb11wallet.VerifyRGB11RegistrationForClient(record, id, rgb11NetworkRegistryVerifier{}); err != nil {
		return nil, err
	}
	var response struct {
		ContractID string `json:"contract_id"`
	}
	if err := p.postDKVSApplication("/v3/rgb11/contracts/register", map[string]any{"record": record}, &response); err != nil {
		return nil, err
	}
	if response.ContractID != id {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	registration, value, err := p.getRGB11ContractWithVerifier(id, rgb11NetworkRegistryVerifier{})
	if err != nil {
		return nil, err
	}
	local, err := rgb11wallet.DecodeRGB11RegistryValue(record.Value)
	if err != nil || !bytes.Equal(value.ContractContent, local.ContractContent) {
		return nil, dkvsindexer.ErrWriteConflict
	}
	return registration, nil
}

// rgb11NetworkRegistryVerifier uses the same locally configured, network-stable
// trust roots as SatoshiNet's CoreNode registry verifier. An HTTP endpoint may
// relay registry records, but cannot choose the keys that authenticate them.
type rgb11NetworkRegistryVerifier struct{}

func (rgb11NetworkRegistryVerifier) CanWriteSystem(key string, pubKey []byte) error {
	if !strings.HasPrefix(key, rgb11wallet.RGB11RegistryPath+"/") {
		return dkvsindexer.ErrPermissionDenied
	}
	signer := hex.EncodeToString(pubKey)
	if signer != indexercommon.GetCoreNodePubKey() {
		return dkvsindexer.ErrPermissionDenied
	}
	return nil
}

// GetRGB11Registration reads an authenticated public technical record.
// It does not establish a DID alias or give the submitting wallet issuer rights.
func (p *SatsNetDKVSClient) GetRGB11Registration(contractID string) (*rgb11wallet.RGB11Registration, error) {
	return p.GetRGB11RegistrationWithVerifier(contractID, rgb11NetworkRegistryVerifier{})
}

// GetRGB11RegistrationWithVerifier supports explicitly configured registry
// authorities. The verifier must come from trusted local network configuration,
// never from the endpoint response. A nil verifier fails closed.
func (p *SatsNetDKVSClient) GetRGB11RegistrationWithVerifier(contractID string,
	verifier dkvsindexer.SystemVerifier) (*rgb11wallet.RGB11Registration, error) {
	registration, _, err := p.getRGB11ContractWithVerifier(contractID, verifier)
	return registration, err
}

func (p *SatsNetDKVSClient) getRGB11ContractWithVerifier(contractID string,
	verifier dkvsindexer.SystemVerifier) (*rgb11wallet.RGB11Registration, *rgb11wallet.RGB11ContractValue, error) {

	if verifier == nil {
		return nil, nil, dkvsindexer.ErrPermissionDenied
	}
	if p == nil || p.RESTClient == nil || p.Http == nil {
		return nil, nil, dkvsindexer.ErrInvalidRecord
	}
	key, err := rgb11wallet.RGB11RegistryKey(contractID)
	if err != nil {
		return nil, nil, err
	}
	// Public technical records are independently readable. They are not part
	// of an account-managed replica subscription, so a local cache miss must
	// not be reported as an unsynchronized private DKVS path.
	record, err := p.GetRecordDirect(key)
	if err != nil {
		return nil, nil, err
	}
	registration, err := rgb11wallet.VerifyRGB11RegistrationForClient(record, contractID, verifier)
	if err != nil {
		return nil, nil, err
	}
	value, err := rgb11wallet.DecodeRGB11RegistryValue(record.Value)
	if err != nil {
		return nil, nil, err
	}
	return registration, value, nil
}
