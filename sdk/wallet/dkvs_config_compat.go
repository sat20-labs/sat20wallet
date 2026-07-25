package wallet

import dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"

// AccountFreeLocalPolicy is the wallet-facing projection of the connected
// node's FREE_LOCAL section from GET /v3/dkvs/config.
type AccountFreeLocalPolicy struct {
	Enabled             bool   `json:"enabled"`
	MaxTTL              uint64 `json:"max_ttl_ms"`
	MaxRecordsPerSigner uint64 `json:"max_records_per_signer"`
	MaxBytesPerSigner   uint64 `json:"max_bytes_per_signer"`
	MaxTotalRecords     uint64 `json:"max_total_records"`
	MaxTotalBytes       uint64 `json:"max_total_bytes"`
}

type accountDKVSConfigResp struct {
	dkvsBaseResp
	Data *dkvsindexer.ClientConfig `json:"data,omitempty"`
}

// GetConfig reads the FREE_LOCAL cache policy of the connected node.
func (p *SatsNetDKVSClient) GetConfig() (*AccountFreeLocalPolicy, error) {
	var resp accountDKVSConfigResp
	if err := p.getPathJSON("/v3/dkvs/config", &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil {
		return nil, ErrDKVSRecordNotFound
	}
	policy := resp.Data.FreeLocal
	return &AccountFreeLocalPolicy{
		Enabled:             policy.Enabled,
		MaxTTL:              policy.MaxTTL,
		MaxRecordsPerSigner: policy.MaxRecordsPerSigner,
		MaxBytesPerSigner:   policy.MaxBytesPerSigner,
		MaxTotalRecords:     policy.MaxTotalRecords,
		MaxTotalBytes:       policy.MaxTotalBytes,
	}, nil
}
