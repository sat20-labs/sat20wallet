package wire

// NativeChannelSettlementReceipt carries public proof and canonical chain
// references only. A receiver must reconstruct both the private mapped source
// and every paired exit before authorizing public-pool inventory. It contains
// no channel entropy or privately usable transfer package.
type NativeChannelSettlementReceipt struct {
	Version        int      `json:"version"`
	PrivateWitness []byte   `json:"privateWitness"`
	SourceOutpoint string   `json:"sourceOutpoint"`
	PublicProof    []byte   `json:"publicProof"`
	DescendTxIDs   []string `json:"descendTxids"`
}
