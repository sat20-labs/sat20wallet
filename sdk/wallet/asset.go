package wallet

import (
	indexer "github.com/sat20-labs/indexer/common"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type AssetName = swire.AssetName

// 白聪
var ASSET_PLAIN_SAT = indexer.ASSET_PLAIN_SAT

type TxOutput = indexer.TxOutput
type TxOutput_SatsNet = sindexer.TxOutput
