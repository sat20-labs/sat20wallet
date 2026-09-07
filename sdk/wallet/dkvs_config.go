package wallet

import (
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// AccountFreeLocalPolicy is the wallet-facing alias of the service-node policy.
type AccountFreeLocalPolicy = dkvscore.FreeLocalPolicy

// GetConfig reads the FREE_LOCAL cache policy of the connected endpoint.
func (p *SatsNetDKVSClient) GetConfig() (*AccountFreeLocalPolicy, error) {
	config, err := p.GetDKVSClientConfig()
	if err != nil {
		return nil, err
	}
	policy := config.FreeLocal
	return &AccountFreeLocalPolicy{
		Enabled:             policy.Enabled,
		MaxTTL:              policy.MaxTTL,
		MaxRecordsPerSigner: policy.MaxRecordsPerSigner,
		MaxBytesPerSigner:   policy.MaxBytesPerSigner,
		MaxTotalRecords:     policy.MaxTotalRecords,
		MaxTotalBytes:       policy.MaxTotalBytes,
	}, nil
}

func (p *SatsNetDKVSClient) configureFreeLocalRetention(options *dkvsindexer.RecordOptions) (*AccountFreeLocalPolicy, error) {
	policy, err := p.GetConfig()
	if err != nil {
		return nil, err
	}
	if err := dkvscore.ApplyFreeLocalRetention(policy, options); err != nil {
		return nil, err
	}
	return policy, nil
}
