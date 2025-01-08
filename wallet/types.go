package wallet

import swire "github.com/sat20-labs/satsnet_btcd/wire"

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
