package wallet

import (
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// GetRGB11Registration reads an existing SatoshiNet RGB11 registry entry.
// Wallet code never creates /rgb11 records; the CoreNode processing the
// corresponding transcend.tc deployment owns that write path.
func (p *SatsNetDKVSClient) GetRGB11Registration(providerDID, ticker,
	contractID string) (*dkvsindexer.RGB11Registration, error) {

	key, err := dkvsindexer.RGB11RegistryKey(providerDID, ticker)
	if err != nil {
		return nil, err
	}
	record, err := p.GetRecord(key)
	if err != nil {
		return nil, err
	}
	return dkvsindexer.RGB11RegistrationFromRecord(record, contractID)
}
