package dkvs

import dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"

// FreeLocalPolicy is the wallet-facing projection of one service node's
// node-local FREE_LOCAL policy. It is not a network consensus parameter.
type FreeLocalPolicy struct {
	Enabled             bool   `json:"enabled"`
	MaxTTL              uint64 `json:"max_ttl_blocks"`
	MaxRecordsPerSigner uint64 `json:"max_records_per_signer"`
	MaxBytesPerSigner   uint64 `json:"max_bytes_per_signer"`
	MaxTotalRecords     uint64 `json:"max_total_records"`
	MaxTotalBytes       uint64 `json:"max_total_bytes"`
}

// ApplyFreeLocalRetention makes the connected service node authoritative for
// domain-level FREE_LOCAL retention. Wallet features use the longest retention
// advertised by that node instead of embedding a fallback duration.
func ApplyFreeLocalRetention(policy *FreeLocalPolicy, options *dkvsindexer.RecordOptions) error {
	if policy == nil || options == nil || !policy.Enabled {
		return dkvsindexer.ErrFreeLocalDisabled
	}
	if policy.MaxTTL == 0 {
		return dkvsindexer.ErrInvalidRecord
	}
	options.TTL = policy.MaxTTL
	return nil
}
