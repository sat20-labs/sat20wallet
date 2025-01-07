package indexer

type PlainUtxo struct {
	Txid  string `json:"txid"`
	Vout  int    `json:"vout"`
	Value int64  `json:"value"`
}

type SatInfo struct {
	Sat        int64    `json:"sat"`
	Height     int64    `json:"height"`
	Cycle      int64    `json:"cycle"`
	Epoch      int64    `json:"epoch"`
	Period     int64    `json:"period"`
	Satributes []string `json:"satributes"`
}

type SpecificSatInUtxo struct {
	Utxo        string     `json:"utxo"`
	Value       int64      `json:"value"`
	SpecificSat int64      `json:"specificsat"`
	Sats        []SatRange `json:"sats"`
}

type SpecificSat struct {
	Address     string     `json:"address"`
	Utxo        string     `json:"utxo"`
	Value       int64      `json:"value"`
	SpecificSat int64      `json:"specificsat"`
	Sats        []SatRange `json:"sats"`
}

type SatRange struct {
	Start  int64 `json:"start"`
	Size   int64 `json:"size"`
	Offset int64 `json:"offset"`
}

type SatributeRange struct {
	SatRange
	Satributes []string `json:"satributes"`
}

type SatDetailInfo struct {
	SatributeRange
	Block int `json:"block"`
	// Time  int64 `json:"time"`
}

type ExoticSatRangeUtxo struct {
	Utxo  string          `json:"utxo"`
	Value int64           `json:"value"`
	Sats  []SatDetailInfo `json:"sats"`
}

type SpecificExoticUtxo struct {
	Utxo   string     `json:"utxo"`
	Value  int64      `json:"value"`
	Type   string     `json:"type"`
	Amount int64      `json:"amount"`
	Sats   []SatRange `json:"sats"`
}


type HealthStatusResp struct {
	Status    string `json:"status" example:"ok"`
	Version   string `json:"version" example:"0.2.1"`
	BaseDBVer string `json:"basedbver" example:"1.0."`
	OrdxDBVer string `json:"ordxdbver" example:"1.0.0"`
}

type OrdStatusResp struct {
	IndexVersion                  string `json:"indexVersion"`
	DbVersion                     string `json:"dbVersion"`
	SyncInscriptionHeight         uint64 `json:"syncInscriptionHeight"`
	SyncTransferInscriptionHeight uint64 `json:"syncTransferInscriptionHeight"`
	BlessedInscriptions           uint64 `json:"blessedInscriptions"`
	CursedInscriptions            uint64 `json:"cursedInscriptions"`
	AddressCount                  uint64 `json:"addressCount"`
	GenesesAddressCount           uint64 `json:"genesesAddressCount"`
}

type SatRangeResp struct {
	BaseResp
	Data *ExoticSatRangeUtxo `json:"data"`
}

type SatInfoResp struct {
	BaseResp
	Data *SatInfo `json:"data"`
}

type SpecificSatReq struct {
	Address string  `json:"address"`
	Sats    []int64 `json:"sats"`
}

type SpecificSatResp struct {
	BaseResp
	Data []*SpecificSat `json:"data"`
}

type SatributesResp struct {
	BaseResp
	Data []string `json:"data"`
}

type SatRangeUtxoResp struct {
	BaseResp
	Data []*ExoticSatRangeUtxo `json:"data"`
}

type PlainUtxosResp struct {
	BaseResp
	Total int                 `json:"total"`
	Data  []*PlainUtxo `json:"data"`
}

type AllUtxosResp struct {
	BaseResp
	Total      int                 `json:"total"`
	PlainUtxos []*PlainUtxo `json:"plainutxos"`
	OtherUtxos []*PlainUtxo `json:"otherutxos"`
}

type SpecificExoticUtxoResp struct {
	BaseResp
	Data []*SpecificExoticUtxo `json:"data"`
}
