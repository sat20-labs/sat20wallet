package sindexer

import (
	indexerwire "github.com/sat20-labs/sat20wallet/sdk/wallet/indexer"
)


type AscendResp struct {
	indexerwire.BaseResp
	Data *AscendData `json:"data"`
}


type DescendResp struct {
	indexerwire.BaseResp
	Data *DescendData `json:"data"`
}


