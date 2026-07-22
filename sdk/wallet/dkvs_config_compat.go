package wallet

import dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"

// GetConfig is retained as the account-management-facing alias for the
// connected node's DKVS local-cache policy endpoint.
func (p *SatsNetDKVSClient) GetConfig() (*dkvsindexer.FreeLocalCachePolicy, error) {
	return p.GetFreeLocalCachePolicy()
}
