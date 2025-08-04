package wallet

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
)

/*
Transcend合约
1. 支持指定资产进入和退出聪网，优先级比amm低，但支持白聪
2. 每个区块处理完成后，统一回款
3. 项目方提取池子利润 （只能提取白聪，默认的分配规则：项目方：节点A：节点B：基金会=55:20:20:5）

对该地址上该资产的约定：
1. 利润是白聪总量
2. 在L1上，没有被Ascend过的utxo，可以用来支持withdraw。如果没有足够的utxo可用，就必须先Descend一些utxo（DeAnchorTx）。但必须保留TranscendContract指定数量资产。
3. 在L2上，超出TranscendContract的资产，可以直接send出去给用户，用来支持deposit操作。如果不够，就需要先Ascend用户转进来的utxo
4. 为了简单一点，现在withdraw直接deAnchor，但deposit不执行anchor
*/

func init() {
	gob.Register(&TranscendContractRuntime{})
}

type TranscendContract struct {
	SwapContract
}

func NewTranscendContract() *TranscendContract {
	c := &TranscendContract{
		SwapContract: *NewSwapContract(),
	}
	c.TemplateName = TEMPLATE_CONTRACT_TRANSCEND
	return c
}


func (p *TranscendContract) CheckContent() error {
	if indexer.IsPlainAsset(&p.AssetName) {
		return nil
	}
	
	err := p.SwapContract.CheckContent()
	if err != nil {
		return err
	}
	
	return nil
}

func (p *TranscendContract) InvokeParam(action string) string {

	var param InvokeParam
	param.Action = action
	switch action {

	case INVOKE_API_DEPOSIT:
		var innerParam DepositInvokeParam
		innerParam.OrderType = ORDERTYPE_DEPOSIT
		buf, err := json.Marshal(&innerParam)
		if err != nil {
			return ""
		}
		param.Param = string(buf)

	case INVOKE_API_WITHDRAW:
		var innerParam DepositInvokeParam
		innerParam.OrderType = ORDERTYPE_WITHDRAW
		buf, err := json.Marshal(&innerParam)
		if err != nil {
			return ""
		}
		param.Param = string(buf)

	default:
		return ""
	}

	result, err := json.Marshal(&param)
	if err != nil {
		return ""
	}
	return string(result)

}


func (p *TranscendContract) Encode() ([]byte, error) {
	return p.SwapContract.Encode()
}

func (p *TranscendContract) Decode(data []byte) error {
	return p.SwapContract.Decode(data)
}

func (p *TranscendContract) Content() string {
	b, err := json.Marshal(p)
	if err != nil {
		Log.Errorf("Marshal TranscendContract failed, %v", err)
		return ""
	}
	return string(b)
}


type TranscendContractRuntime struct {
	SwapContractRuntime
}

func NewTranscendContractRuntime(stp *Manager) *TranscendContractRuntime {
	p := &TranscendContractRuntime{
		SwapContractRuntime: SwapContractRuntime{
			SwapContractRuntimeInDB: SwapContractRuntimeInDB{
				Contract: NewTranscendContract(),
				ContractRuntimeBase: ContractRuntimeBase{
					DeployTime: time.Now().Unix(),
					stp:        stp,
				},
				SwapContractRunningData: SwapContractRunningData{},
			},
		},
	}
	p.init()

	return p
}

func (p *TranscendContractRuntime) GobEncode() ([]byte, error) {
	return p.SwapContractRuntime.GobEncode()
}

func (p *TranscendContractRuntime) GobDecode(data []byte) error {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)

	var swap TranscendContract
	if err := dec.Decode(&swap); err != nil {
		return err
	}
	p.Contract = &swap

	if err := dec.Decode(&p.ContractRuntimeBase); err != nil {
		return err
	}

	if err := dec.Decode(&p.SwapContractRunningData); err != nil {
		return err
	}
	

	return nil
}
