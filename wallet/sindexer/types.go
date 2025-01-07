package sindexer

import (
	"github.com/sat20-labs/satsnet_btcd/wire"
)

const (
	DB_KEY_UTXO         = "u-"  // utxo -> UtxoValueInDB
	DB_KEY_ADDRESS      = "a-"  // address -> addressId
	DB_KEY_ADDRESSVALUE = "av-" // addressId-utxoId -> value
	DB_KEY_UTXOID       = "ui-" // utxoId -> utxo
	DB_KEY_ADDRESSID    = "ai-" // addressId -> address
	DB_KEY_BLOCK        = "b-"  // height -> block
)

// Address Type defined in txscript.ScriptClass

type UtxoValueInDB struct {
	UtxoId      uint64
	Value       int64
	AddressType uint16
	ReqSig      uint16
	AddressIds  []uint64
	Assets      wire.TxAssets
}

type BlockValueInDB struct {
	Height     int
	Timestamp  int64
	InputUtxo  int
	OutputUtxo int
	InputSats  int64
	OutputSats int64
	TxAmount   int
}

type BlockInfo struct {
	Height     int   `json:"height"`
	Timestamp  int64 `json:"timestamp"`
	InputUtxo  int   `json:"inpututxos"`
	OutputUtxo int   `json:"outpututxos"`
	InputSats  int64 `json:"inputsats"`
	OutputSats int64 `json:"outputsats"`
	TxAmount   int   `json:"txamount"`
}

type TickerName = wire.AssetName


type UtxoInfo struct {
	UtxoId   uint64
	Value    int64
	PkScript []byte
	Assets   wire.TxAssets
}
