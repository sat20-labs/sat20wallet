package wallet

import (
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// GetRGB11Registration reads an existing SatoshiNet RGB11 registry entry.
// Wallet code never creates /rgb11 records; the CoreNode processing the
// corresponding transcend.tc deployment owns that write path.
func (p *SatsNetDKVSClient) GetRGB11Registration(providerDID, ticker,
	contractID string) (*dkvsindexer.RGB11Registration, error) {

	prefix, err := dkvsindexer.RGB11RegistryPrefix(providerDID, ticker)
	if err != nil {
		return nil, err
	}
	records, _, err := p.ListRecords(prefix, 0, 0)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		registration, err := dkvsindexer.RGB11RegistrationFromRecord(record)
		if err != nil {
			return nil, err
		}
		if registration.ContractID == contractID {
			return registration, nil
		}
	}
	return nil, dkvsindexer.ErrRecordNotFound
}
