package wallet

import (

	indexer "github.com/sat20-labs/indexer/common"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const (
	BOOTSTRAP_NODE string = "bootstrap"
	SERVER_NODE    string = "server"
	CLIENT_NODE    string = "client"    // 
	LIGHT_NODE     string = "light"		// browser plugin

	L1_NETWORK_BITCOIN 		string = "bitcoinnet"
	L2_NETWORK_SATOSHI   	string = "satoshinet"
)

// print the TX
type TxWitness []string

type OutPoint struct {
	Hash  string `json:"Hash"`
	Index uint32 `json:"Index"`
}

type TxIn struct {
	PreviousOutPoint OutPoint  `json:"PreviousOutPoint"`
	SignatureScript  string    `json:"SignatureScript"`
	Witness          TxWitness `json:"Witness"`
	Sequence         uint32    `json:"Sequence"`
}

type TxOut struct {
	Value    int64  `json:"Value"`
	PkScript string `json:"PkScript"`
	Assets   swire.TxAssets `json:"Assets,omitempty"`  
}

type MsgTx struct {
	Version  int32    `json:"Version"`
	TxIn     []*TxIn  `json:"TxIn"`
	TxOut    []*TxOut `json:"TxOut"`
	LockTime uint32   `json:"LockTime"`
}

type TxAssetInfo struct {
	TxId         string
	TxHex        string
	InputAssets  []*indexer.AssetsInUtxo // 与输入索引一一对应
	OutputAssets []*indexer.AssetsInUtxo // 与输出索引一一对应
}


type SendAssetInfo struct {
	Address string
	Value   int64  // 额外的聪，不包含资产中所必须带的聪。
	AssetName *indexer.AssetName
	AssetAmt *Decimal
}


type AssetName struct {
	swire.AssetName
	N int
}

func (p *AssetName) String() string {
	return p.AssetName.String()
}

var PLAIN_ASSET = AssetName{
	AssetName: indexer.ASSET_PLAIN_SAT,
	N:         1,
}

// 白聪
var ASSET_PLAIN_SAT = indexer.ASSET_PLAIN_SAT


type TxOutput = indexer.TxOutput
type TxOutput_SatsNet = sindexer.TxOutput


type Decimal = indexer.Decimal


