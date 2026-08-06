package wallet

import (
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// AccountFreeLocalPolicy is retained as the wallet-facing alias of the
// low-level DKVS service-node policy.
type AccountFreeLocalPolicy = dkvscore.FreeLocalPolicy

type accountDKVSConfigResp struct {
	dkvsBaseResp
	Data *dkvsindexer.ClientConfig `json:"data,omitempty"`
}

func (p *SatsNetDKVSClient) GetClientConfigV1() (*dkvsindexer.ClientConfig, error) {
	if p == nil || p.Http == nil {
		return nil, ErrDKVSPathNotSynced
	}
	if provider, ok := p.Http.(dkvsV1ConfigProvider); ok {
		config, err := provider.DKVSClientConfigV1()
		if err != nil {
			return nil, err
		}
		if config == nil {
			return nil, ErrDKVSRecordNotFound
		}
		copyConfig := *config
		return &copyConfig, nil
	}
	var resp accountDKVSConfigResp
	if err := p.getPathJSON("/v3/dkvs/config", &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil {
		return nil, ErrDKVSRecordNotFound
	}
	copyConfig := *resp.Data
	return &copyConfig, nil
}

// GetConfig reads the FREE_LOCAL cache policy of the connected node.
func (p *SatsNetDKVSClient) GetConfig() (*AccountFreeLocalPolicy, error) {
	config, err := p.GetClientConfigV1()
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

func (p *SatsNetDKVSClient) configureFreeLocalRetention(
	options *dkvsindexer.RecordOptions) (*AccountFreeLocalPolicy, error) {

	policy, err := p.GetConfig()
	if err != nil {
		return nil, err
	}
	if err := dkvscore.ApplyFreeLocalRetention(policy, options); err != nil {
		return nil, err
	}
	return policy, nil
}
