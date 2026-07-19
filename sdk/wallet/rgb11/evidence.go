package rgb11wallet

type BitcoinUTXO struct {
	OutPoint      string `json:"outpoint"`
	Value         int64  `json:"value"`
	PkScript      []byte `json:"pk_script"`
	Confirmations int64  `json:"confirmations"`
}

type BitcoinTxStatus struct {
	TxID          string `json:"txid"`
	InMempool     bool   `json:"in_mempool"`
	Confirmed     bool   `json:"confirmed"`
	BlockHeight   int64  `json:"block_height"`
	BlockHash     string `json:"block_hash"`
	Confirmations int64  `json:"confirmations"`
}

type BitcoinOutspend struct {
	Spent      bool   `json:"spent"`
	SpendingTx string `json:"spending_tx,omitempty"`
	Vin        uint32 `json:"vin,omitempty"`
}

type BitcoinTip struct {
	Height    int64  `json:"height"`
	BlockHash string `json:"block_hash"`
}

type BitcoinEvidenceProvider interface {
	GetUTXO(outpoint string) (*BitcoinUTXO, error)
	GetRawTx(txid string) ([]byte, error)
	GetTxStatus(txid string) (*BitcoinTxStatus, error)
	GetOutspend(outpoint string) (*BitcoinOutspend, error)
	GetTip() (*BitcoinTip, error)
	Broadcast(rawTx []byte) (string, error)
}
