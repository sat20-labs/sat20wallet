package wallet

import dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"

type dkvsConfigResp struct {
	dkvsBaseResp
	Data *dkvsindexer.FreeLocalCachePolicy `json:"data,omitempty"`
}

func (p *SatsNetDKVSClient) GetConfig() (*dkvsindexer.FreeLocalCachePolicy, error) {
	var resp dkvsConfigResp
	if err := p.getPathJSON("/v3/dkvs/config", &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return resp.Data, nil
}
