package wallet

import (
	
	"github.com/btcsuite/btcd/txscript"
	
	swire "github.com/sat20-labs/satoshinet/wire"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

const (
	TX_STATUS_INIT        int = 0
	TX_STATUS_BROADCASTED int = 1
	TX_STATUS_CONFIRMED   int = 2

	RESV_TYPE_DEPLOYCONTRACT     = "deploycontract"
)

type ContractDeployDataInDB struct {
	ReservationBase
	Contract   ContractRuntime
	ChannelId  string
	DeployTime int64
	Deployer   string 

	HasSentDeployTx int
	HasRunContract  int

	FeeRate          int64
	Invoice          []byte
	InvoiceRemoteSig []byte
	InvoiceLocalSig  []byte
	ServiceFee       int64
	RequiredFee      int64

	DeployContractTx   *swire.MsgTx
	DeployContractTxId string
}

func (p *ContractDeployDataInDB) GetType() string {
	return RESV_TYPE_DEPLOYCONTRACT
}

func (p *ContractDeployDataInDB) GetStructInDB() interface{} {
	return p
}

func (p *ContractDeployDataInDB) GetResult() []byte {
	return []byte(p.DeployContractTxId)
}

type ContractDeployReservation struct {
	ContractDeployDataInDB

	remotePubKey []byte
	feeUtxos  []string
	feeInputs []*TxOutput_SatsNet
	req       *wwire.DeployContractRequest
	reqSig    []byte
	resyncingL1 bool
	resyncingL2 bool
}

func (p *ContractDeployReservation) UnconfirmedTxId() string {
	return p.Contract.UnconfirmedTxId()
}

func (p *ContractDeployReservation) UnconfirmedTxId_SatsNet() string {
	if p.Contract.DeploySelf() {
		return p.Contract.UnconfirmedTxId_SatsNet()
	} else {
		if p.HasSentDeployTx == TX_STATUS_BROADCASTED {
			return p.DeployContractTxId
		}
		if p.HasSentDeployTx == TX_STATUS_CONFIRMED {
			return p.Contract.UnconfirmedTxId_SatsNet()
		}
	}

	return ""
}

// 调用 sindexer.NullDataScript 组装成最终的 op_return 数据
func SignedDeployContractInvoice(resv *ContractDeployReservation) ([]byte, error) {
	buf, err := resv.Contract.Encode()
	if err != nil {
		return nil, err
	}
	return txscript.NewScriptBuilder().
		AddData([]byte(resv.Contract.RelativePath())).
		AddData(buf).
		AddInt64(int64(resv.DeployTime)).
		AddData(resv.InvoiceLocalSig).
		AddData(resv.InvoiceRemoteSig).Script()
}

func UnsignedDeployContractInvoice(resv *ContractDeployReservation) ([]byte, error) {
	buf, err := resv.Contract.Encode()
	if err != nil {
		return nil, err
	}
	return txscript.NewScriptBuilder().
		AddData([]byte(resv.Contract.RelativePath())).
		AddData(buf).
		AddInt64(int64(resv.DeployTime)).Script()
}
