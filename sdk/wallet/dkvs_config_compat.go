package wallet

// AccountFreeLocalPolicy mirrors the public JSON returned by the connected
// indexer's GET /v3/dkvs/config endpoint. Keeping this transport type in the
// wallet SDK avoids coupling PWA account management to an indexer-internal Go
// type while preserving the endpoint contract.
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
	Data *AccountFreeLocalPolicy `json:"data,omitempty"`
}

// GetConfig reads the cache policy of the node to which this wallet is
// connected. It is node-local configuration, not a network-wide guarantee.
func (p *SatsNetDKVSClient) GetConfig() (*AccountFreeLocalPolicy, error) {
	var resp accountDKVSConfigResp
	if err := p.getPathJSON("/v3/dkvs/config", &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil {
		return nil, ErrDKVSRecordNotFound
	}
	return resp.Data, nil
}
