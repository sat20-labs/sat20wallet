package indexer


type SendRawTxReq struct {
	SignedTxHex string  `json:"signedTxHex" binding:"required" example:"0100000001e34ac1e2baac09c366fce1c2245536bda8f7db0f6685862aecf53ebd69f9a89c000000006a47304402203e8a16522da80cef66bacfbc0c800c6d52c4a26d1d86a54e0a1b76d661f020c9022010397f00149f2a8fb2bc5bca52f2d7a7f87e3897a273ef54b277e4af52051a06012103c9700559f690c4a9182faa8bed88ad8a0c563777ac1d3f00fd44ea6c71dc5127ffffffff02a0252600000000001976a914d90d36e98f62968d2bc9bbd68107564a156a9bcf88ac50622500000000001976a91407bdb518fa2e6089fd810235cf1100c9c13d1fd288ac00000000"`
	Maxfeerate  float32 `json:"maxfeerate,omitempty"`
}

type SendRawTxResp struct {
	BaseResp
	Data string `json:"data"  example:"ae74538baa914f3799081ba78429d5d84f36a0127438e9f721dff584ac17b346"`
}

type RawBlockResp struct {
	BaseResp
	Data string `json:"data" example:""`
}

type TxResp struct {
	BaseResp
	Data any `json:"data"`
}

type Vin struct {
	Utxo     string `json:"utxo"`
	Address  string `json:"address"`
	Value    int64  `json:"value"`
	Sequence uint32 `json:"sequence"`
}

type Vout struct {
	Address string `json:"address"`
	Value   int64  `json:"value"`
}

type TxInfo struct {
	TxID          string `json:"txid"`
	Version       uint32 `json:"version"`
	Confirmations uint64 `json:"confirmations"`
	BlockHeight   int64  `json:"block_height"`
	BlockTime     int64  `json:"block_time"`
	Vins          []Vin  `json:"vin"`
	Vouts         []Vout `json:"vout"`
}


type TxSimpleInfoResp struct {
	BaseResp
	Data *TxSimpleInfo `json:"data"`
}

type TxSimpleInfo struct {
	TxID          string `json:"txid"`
	Version       uint32 `json:"version"`
	Confirmations uint64 `json:"confirmations"`
	BlockHeight   int64  `json:"block_height"`
	BlockTime     int64  `json:"block_time"`
}

type BlockHashResp struct {
	BaseResp
	Data string `json:"data" example:""`
}

type BestBlockhashResp struct {
	BaseResp
	Data string `json:"data"`
}

type BestBlockHeightResp struct {
	BaseResp
	Data int64 `json:"data"`
}
