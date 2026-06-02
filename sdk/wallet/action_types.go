package wallet

import (
	"encoding/gob"
	"fmt"
	"sync"

	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	sindexerwire "github.com/sat20-labs/satoshinet/indexer/rpcserver/wire"
	"github.com/sat20-labs/satoshinet/txscript"
)

const (
	LOCAL_ACTION_CONFIRM_TX       string = "confirmtx"
	LOCAL_ACTION_CONFIRM_TX_L2    string = "confirmtxl2"
	LOCAL_ACTION_LOCK_WITH_EXPAND string = "lockwithexpand"
	LOCAL_ACTION_UNSTAKE_MINER    string = "unstakeminer"
)

type LocalActionParam_Expand struct {
	AssetName   *AssetName
	Amt         *Decimal
	ContractURL string
}

type LocalActionParam_UnstakeMiner struct {
	MinerInfo   *sindexerwire.MinerInfo
	AssetName   *AssetName
	Amt         *Decimal
	Value       int64
	ContractURL string
}

type SubActionInfo struct {
	ActionType string
	ResvId     int64

	TxId     string
	MoreData any
}

const (
	RS_PERFORM_ACTION_STARTED        ResvStatus = 0x2400
	RS_PERFORM_ACTION_TX_BROADCASTED ResvStatus = 0x2401
	RS_PERFORM_ACTION_TX_CONFIRMED   ResvStatus = 0x2402
	RS_PERFORM_ACTION_RUN_STARTED    ResvStatus = 0x2403
	RS_PERFORM_ACTION_COMPLETED      ResvStatus = RS_CONFIRMED
)

type LocalActionPerformData struct {
	ReservationBase
	Action      string
	ActionParam any
	FeeRate     int64
	ReqTime     int64
	ReqPubKey   []byte
	ReqSig      []byte
	TxId        string
	IsL1Tx      bool

	ActionResvs []*SubActionInfo
}

func (p *LocalActionPerformData) GetType() string {
	return RESV_TYPE_LOCALACTION
}

func (p *LocalActionPerformData) GetStructInDB() any {
	return p
}

func (p *LocalActionPerformData) GetCurrentResvId() int64 {
	if len(p.ActionResvs) == 0 {
		return 0
	}
	return p.ActionResvs[len(p.ActionResvs)-1].ResvId
}

const (
	REMOTE_ACTION_DEPLOY_CONTRACT string = "deploycontract"
	REMOTE_ACTION_ASCEND          string = "ascend"
	REMOTE_ACTION_DEPLOY_RUNES    string = "deployrunes"
)

const (
	REMOTE_ACTION_RESV_RUNES_DEPLOY string = "runesdeploy"
)

const REMOTE_DEPLOY_RUNES_SERVICE_FEE int64 = 2000

type RemoteDeployRunesParam struct {
	AssetName string `json:"assetName"`
	Symbol    int32  `json:"symbol"`
	MaxSupply int64  `json:"maxSupply"`
	Limit     int64  `json:"limit"`
	SelfMint  bool   `json:"selfmint"`
	DestAddr  string `json:"destAddr,omitempty"`
}

type RemoteDeployRunesResult struct {
	AssetName      string `json:"assetName"`
	CommitTxId     string `json:"commitTxId,omitempty"`
	RevealTxId     string `json:"revealTxId,omitempty"`
	InscribeResvId int64  `json:"inscribeResvId"`
	Status         int    `json:"status"`
}

type RemoteActionPerformReservation struct {
	ReservationBase
	Action              string
	ActionParam         []byte
	FeeRate             int64
	ReqTime             int64
	ReqPubKey           []byte
	ReqSig              []byte
	SendTxInL1          bool
	SendToBootstrapNode bool

	Invoice     []byte
	InvoiceSig  []byte
	ServiceFee  int64
	ServiceAddr string

	FeeTx   string
	FeeTxId string

	ActionResvType string
	ActionResvId   int64
	ActionStatus   int
	ActionResult   []byte

	MoreData []byte

	req *wwire.PerformActionRequest
}

func (p *RemoteActionPerformReservation) GetType() string {
	return RESV_TYPE_REMOTEACTION
}

func (p *RemoteActionPerformReservation) GetStructInDB() any {
	return p
}

type StartRemoteAction func(resv *RemoteActionPerformReservation) (string, string, error)

func (p *RemoteActionPerformReservation) Request() *wwire.PerformActionRequest {
	return p.req
}

var registerSTPActionGobTypesOnce sync.Once

func RegisterSTPActionGobTypes() {
	registerSTPActionGobTypesOnce.Do(func() {
		gob.RegisterName("*stp.LocalActionParam_Expand", new(LocalActionParam_Expand))
		gob.RegisterName("*stp.LocalActionParam_UnstakeMiner", new(LocalActionParam_UnstakeMiner))
		gob.Register(new(Decimal))
	})
}

func init() {
	RegisterSTPActionGobTypes()
}

func EncodeRemoteDeployRunesParam(param *RemoteDeployRunesParam) ([]byte, error) {
	selfMint := int64(0)
	if param.SelfMint {
		selfMint = 1
	}

	return txscript.NewScriptBuilder().
		AddData([]byte(param.AssetName)).
		AddInt64(int64(param.Symbol)).
		AddInt64(param.MaxSupply).
		AddInt64(param.Limit).
		AddInt64(selfMint).
		AddData([]byte(param.DestAddr)).
		Script()
}

func DecodeRemoteDeployRunesParam(script []byte) (*RemoteDeployRunesParam, error) {
	tokenizer := txscript.MakeScriptTokenizer(0, script)
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return nil, fmt.Errorf("parameter is missing asset name")
	}
	assetName := string(tokenizer.Data())
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return nil, fmt.Errorf("parameter is missing symbol")
	}
	symbol := int32(tokenizer.ExtractInt64())
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return nil, fmt.Errorf("parameter is missing max supply")
	}
	maxSupply := tokenizer.ExtractInt64()

	limit := int64(0)
	selfMint := true
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return nil, fmt.Errorf("parameter is missing dest address")
	}
	if !tokenizer.Done() {
		limit = tokenizer.ExtractInt64()
		if !tokenizer.Next() || tokenizer.Err() != nil {
			return nil, fmt.Errorf("parameter is missing selfmint")
		}
		selfMint = tokenizer.ExtractInt64() != 0
		if !tokenizer.Next() || tokenizer.Err() != nil {
			return nil, fmt.Errorf("parameter is missing dest address")
		}
	}
	destAddr := string(tokenizer.Data())
	if !tokenizer.Done() {
		return nil, fmt.Errorf("too many parameters")
	}

	return &RemoteDeployRunesParam{
		AssetName: assetName,
		Symbol:    symbol,
		MaxSupply: maxSupply,
		Limit:     limit,
		SelfMint:  selfMint,
		DestAddr:  destAddr,
	}, nil
}

func SignedPerformRemoteActionInvoice(action string, invoiceSig []byte) ([]byte, error) {
	hashCode := invoiceSig[:8]

	return txscript.NewScriptBuilder().
		AddData([]byte(action)).
		AddData(hashCode).
		Script()
}

func UnsignedPerformActionInvoice(action string) ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(action)).
		Script()
}

func ParsePerformActionInvoice(script []byte) (action string, invoice []byte, err error) {
	tokenizer := txscript.MakeScriptTokenizer(0, script)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		err = fmt.Errorf("script is missing action")
		return
	}
	action = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		err = fmt.Errorf("script is missing invoice")
		return
	}
	invoice = tokenizer.Data()

	return
}
