package wallet

import (
	"github.com/btcsuite/btcd/wire"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type FundingDataInDB struct {
	ReservationBase
	ChannelId           string
	NeedSendFundingTx   bool
	SkipOpeningAnchorTx bool

	FundingTxId string // 服务端只有txid
	FundingTx   *wire.MsgTx
	AnchorTx    *swire.MsgTx
}

func (p *FundingDataInDB) GetType() string {
	return RESV_TYPE_OPEN
}

func (p *FundingDataInDB) GetStructInDB() interface{} {
	return p
}

type ClosingDataInDB struct {
	ReservationBase
	ChannelId   string
	CloseHeight int

	FeeRate         int64
	SplicingOutTxId string
	SplicingResvId  int64
	PreTxs          []*wire.MsgTx
	CloseTx         *wire.MsgTx
	DeAnchorTx      *swire.MsgTx
}

func (p *ClosingDataInDB) GetType() string {
	return RESV_TYPE_CLOSE
}

func (p *ClosingDataInDB) GetStructInDB() interface{} {
	return p
}

type PaymentDataInDB struct {
	ReservationBase
	ChannelId string
	AssetName *AssetName
	Amt       *Decimal            // for lock
	DestAddr  []string            // for unlock
	DestAmt   []*Decimal          // for unlock
	Utxos     []*TxOutput_SatsNet // available when lock these utxos
	Fees      []*TxOutput_SatsNet
	Memo      []byte

	IsUnlock       bool
	NeedSendLockTx bool

	Reason   string
	MoreData []byte

	PaymentTx *swire.MsgTx
}

func (p *PaymentDataInDB) GetType() string {
	return RESV_TYPE_PAYMENT
}

func (p *PaymentDataInDB) GetStructInDB() interface{} {
	return p
}

type SplicingDataInDB struct {
	ReservationBase
	NeedSendSplicingTx bool
	ChannelId          string
	OldChanPoint       string

	AssetName     *AssetName
	Amt           *Decimal // 请求的资产数量
	SplicingAmt   *Decimal // 实际的资产数量， 最终的utxo的assets
	SplicingValue int64    // 聪数量， 最终的utxo的value
	ServiceFee    int64
	RequiredFee   int64
	Memo          []byte

	SplicingChange int64 // 聪数量
	FeeChange      int64 // 聪数量
	StubIsSet      bool  // 是否设置了stub utxo
	DestAddr       string

	SplicingTx *wire.MsgTx
	AnchorTx   *swire.MsgTx // or deanchorTx
	PreTxs     []*wire.MsgTx

	RecoverAscended         bool
	RecoveredAnchorTxId     string
	RecoveredAnchorOutpoint string
}

func (p *SplicingDataInDB) GetType() string {
	return RESV_TYPE_SPLICING
}

func (p *SplicingDataInDB) GetStructInDB() interface{} {
	return p
}
