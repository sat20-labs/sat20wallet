package indexer

import (

	swire "github.com/sat20-labs/satsnet_btcd/wire"
)

type Range struct {
	Start int64 `protobuf:"varint,1,opt,name=start,proto3" json:"start,omitempty"`
	Size  int64 `protobuf:"varint,2,opt,name=size,proto3" json:"size,omitempty"`
}

type TickerInfo struct {
	swire.AssetName        `json:"name"`
	DisplayName     string `json:"displayname"`
	Id 				int64  `json:"id"`
	Divisibility 	int	   `json:"divisibility,omitempty"`
	StartBlock      int    `json:"startBlock,omitempty"`
	EndBlock        int    `json:"endBlock,omitempty"`
	SelfMint        int    `json:"selfmint,omitempty"`
	DeployHeight    int    `json:"deployHeight"`
	DeployBlocktime int64  `json:"deployBlockTime"`
	DeployTx        string `json:"deployTx"`
	Limit           string `json:"limit"`
	TotalMinted     string `json:"totalMinted"`
	MintTimes       int64  `json:"mintTimes"`
	MaxSupply       string `json:"maxSupply,omitempty"`
	HoldersCount    int    `json:"holdersCount"`
	InscriptionId   string `json:"inscriptionId,omitempty"`
	InscriptionNum  int64  `json:"inscriptionNum,omitempty"`
	Description     string `json:"description,omitempty"`
	Rarity          string `json:"rarity,omitempty"`
	DeployAddress   string `json:"deployAddress,omitempty"`
	Content         []byte `json:"content,omitempty"`
	ContentType     string `json:"contenttype,omitempty"`
	Delegate        string `json:"delegate,omitempty"`
}

type MintInfo struct {
	Id             int64  `json:"id"`  // ticker内的铸造序号，非全局
	Address        string `json:"mintaddress"`
	Amount         string `json:"amount"`
	Ordinals       []*Range `json:"ordinals,omitempty"`
	Height         int    `json:"height"`
	InscriptionId  string `json:"inscriptionId,omitempty"`  // 铭文id，或者符文的铸造输出utxo
	InscriptionNum int64  `json:"inscriptionNumber,omitempty"`
}

type MintHistory struct {
	swire.AssetName        `json:"name"`
	Total    int           `json:"total,omitempty"`
	Start    int           `json:"start,omitempty"`
	Limit    int           `json:"limit,omitempty"`
	Items    []*MintInfo   `json:"items,omitempty"`
}

type DisplayAsset struct {
	swire.AssetName        `json:"name"`
	Amount  string         `json:"amount"`
	BindingSat bool        `json:"bindingsat"`
	Offsets []*OffsetRange `json:"offsets"`
}

type AssetsInUtxo struct {
	OutPoint    string     `json:"outpoint"`
	Value       int64      `json:"value"`
	Assets  	[]*DisplayAsset `json:"assets"`
}
