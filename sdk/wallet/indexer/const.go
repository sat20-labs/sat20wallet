package indexer

import "math"


const (

	// 暂时将所有nft当做一类ticker来管理，以后扩展时，使用合集名称来区分
	ASSET_TYPE_NFT    = "o"
	ASSET_TYPE_FT     = "f"
	ASSET_TYPE_EXOTIC = "e"
	ASSET_TYPE_NS     = "n"
)


const (
	PROTOCOL_NAME_ORD = "ord"
	PROTOCOL_NAME_ORDX = "ordx"
	PROTOCOL_NAME_BRC20 = "brc20"
	PROTOCOL_NAME_RUNES = "runes"
)

const INVALID_ID = math.MaxUint64