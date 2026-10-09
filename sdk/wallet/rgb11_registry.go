package wallet

import (
	"encoding/hex"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"strings"

	indexercommon "github.com/sat20-labs/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

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

// GetRGB11Registration reads an authenticated SatoshiNet RGB11 registry entry.
// Ordinary wallets only read. A trusted registration service using the SDK
// persists the canonical mapping through the generic contract store.
func (p *SatsNetDKVSClient) GetRGB11Registration(providerDID, ticker,
	contractID string) (*rgb11wallet.RGB11Registration, error) {

	return p.GetRGB11RegistrationWithVerifier(providerDID, ticker, contractID, rgb11NetworkRegistryVerifier{})
}

// GetRGB11RegistrationWithVerifier supports explicitly configured registry
// authorities. The verifier must come from trusted local network configuration,
// never from the endpoint response. A nil verifier fails closed.
func (p *SatsNetDKVSClient) GetRGB11RegistrationWithVerifier(providerDID, ticker,
	contractID string, verifier dkvsindexer.SystemVerifier) (*rgb11wallet.RGB11Registration, error) {

	if verifier == nil {
		return nil, dkvsindexer.ErrPermissionDenied
	}
	if p == nil || p.RESTClient == nil || p.Http == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	key, err := rgb11wallet.RGB11RegistryKey(contractID)
	if err != nil {
		return nil, err
	}
	base, err := rgb11wallet.NormalizeRGB11Ticker(ticker)
	if err != nil || dkvsindexer.ValidatePrimaryDIDName(providerDID) != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	record, err := p.GetRecord(key)
	if err != nil {
		return nil, err
	}
	registration, err := rgb11wallet.VerifyRGB11RegistrationForClient(record, contractID, verifier)
	if err != nil {
		return nil, err
	}
	if registration.ProviderDID != providerDID || registration.BaseTicker != base {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return registration, nil
}
