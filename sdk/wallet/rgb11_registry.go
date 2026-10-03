package wallet

import (
	"encoding/hex"
	"strings"

	indexercommon "github.com/sat20-labs/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// rgb11NetworkRegistryVerifier uses the same locally configured, network-stable
// trust roots as SatoshiNet's CoreNode registry verifier. An HTTP endpoint may
// relay registry records, but cannot choose the keys that authenticate them.
type rgb11NetworkRegistryVerifier struct{}

func (rgb11NetworkRegistryVerifier) CanWriteSystem(key string, pubKey []byte) error {
	if !strings.HasPrefix(key, "/rgb11/") {
		return dkvsindexer.ErrPermissionDenied
	}
	signer := hex.EncodeToString(pubKey)
	if signer != indexercommon.GetBootstrapPubKey() && signer != indexercommon.GetCoreNodePubKey() {
		return dkvsindexer.ErrPermissionDenied
	}
	return nil
}

// GetRGB11Registration reads an authenticated SatoshiNet RGB11 registry entry.
// Wallet code never creates /rgb11 records. The trusted SatoshiNet/STP
// registration path persists the canonical mapping before RGB11 ingress.
func (p *SatsNetDKVSClient) GetRGB11Registration(providerDID, ticker,
	contractID string) (*dkvsindexer.RGB11Registration, error) {

	return p.GetRGB11RegistrationWithVerifier(providerDID, ticker, contractID, rgb11NetworkRegistryVerifier{})
}

// GetRGB11RegistrationWithVerifier supports explicitly configured registry
// authorities. The verifier must come from trusted local network configuration,
// never from the endpoint response. A nil verifier fails closed.
func (p *SatsNetDKVSClient) GetRGB11RegistrationWithVerifier(providerDID, ticker,
	contractID string, verifier dkvsindexer.SystemVerifier) (*dkvsindexer.RGB11Registration, error) {

	if verifier == nil {
		return nil, dkvsindexer.ErrPermissionDenied
	}
	if p == nil || p.RESTClient == nil || p.Http == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	prefix, err := dkvsindexer.RGB11RegistryPrefix(providerDID, ticker)
	if err != nil {
		return nil, err
	}
	// Reuse the registry's exact ContractID validation; do not accept shortened,
	// zero, uppercase or otherwise noncanonical identities as lookup keys.
	if _, err := dkvsindexer.EncodeRGB11RegistryValue(indexercommon.ASSET_TYPE_FT, contractID); err != nil {
		return nil, err
	}
	records, _, err := p.ListRecords(prefix, 0, 0)
	if err != nil {
		return nil, err
	}
	if err := dkvsindexer.VerifyRecordsForClient(records, prefix, dkvsindexer.RecordVerificationOptions{}); err != nil {
		return nil, err
	}
	seenKeys := make(map[string]struct{}, len(records))
	seenContracts := make(map[string]struct{}, len(records))
	var found *dkvsindexer.RGB11Registration
	for _, record := range records {
		// Registry facts are permanent, immutable records, not arbitrary signed
		// DKVS metadata. A valid signature alone cannot establish these rules.
		if record == nil || record.Seq != 1 || record.TTL != 0 ||
			dkvsindexer.IsTombstone(record.Flags) || len(record.FeeProof) != 0 || len(record.PubKey) == 0 {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		if err := verifier.CanWriteSystem(record.Key, record.PubKey); err != nil {
			return nil, err
		}
		registration, err := dkvsindexer.RGB11RegistrationFromRecord(record)
		if err != nil {
			return nil, err
		}
		gotPrefix, err := dkvsindexer.RGB11RegistryPrefix(registration.ProviderDID, registration.BaseTicker)
		if err != nil || gotPrefix != prefix {
			return nil, dkvsindexer.ErrInvalidKey
		}
		if _, duplicate := seenKeys[record.Key]; duplicate {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		if _, duplicate := seenContracts[registration.ContractID]; duplicate {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		seenKeys[record.Key] = struct{}{}
		seenContracts[registration.ContractID] = struct{}{}
		if registration.ContractID == contractID {
			found = registration
		}
	}
	// Validate the entire response before returning a match. An early valid
	// entry must not hide a forged or conflicting entry later in the list.
	if found == nil {
		return nil, dkvsindexer.ErrRecordNotFound
	}
	return found, nil
}
