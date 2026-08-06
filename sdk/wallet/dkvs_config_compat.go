package wallet

import dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"

// AccountFreeLocalPolicy is the wallet-facing projection of the connected
// node's FREE_LOCAL section from GET /v3/dkvs/config.
type AccountFreeLocalPolicy struct {
	Enabled             bool   `json:"enabled"`
	MaxTTL              uint64 `json:"max_ttl_blocks"`
	MaxRecordsPerSigner uint64 `json:"max_records_per_signer"`
	MaxBytesPerSigner   uint64 `json:"max_bytes_per_signer"`
	MaxTotalRecords     uint64 `json:"max_total_records"`
	MaxTotalBytes       uint64 `json:"max_total_bytes"`
}

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
