package wallet

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"

	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
)

/*
AMM交易合约
1. 池子中满足一定的资产份额（常数K）后，合约激活
2. 两种任意的资产，一般一种是聪
3. 每笔交易，按照区块顺序自动处理
4. 每个区块处理完成后，统一回款


对该地址上该资产的约定：
1. 合约参数规定的资产和对应数量，由该合约管理。合约需要确保不论L1和L2上，都有对应数量的资产
	a. AmmContract 的参数规定了池子基本资产, 只有这部分资产必须严格要求L1和L2都要有，并且不允许动用
	b. SwapContractRunningData 运行参数，包含了合约运行的盈利，也是合约管理的资产，但可能在L1，也可能在L2
2. 在L1和L2上持有的更多的资产，可以支持withdraw和deposit操作
3. 在L1上，没有被Ascend过的utxo，可以用来支持withdraw。如果没有足够的utxo可用，就必须先Descend一些utxo（DeAnchorTx）。但必须保留AmmContract指定数量资产。
4. 在L2上，超出AmmContract的资产，可以直接send出去给用户，用来支持deposit操作。如果不够，就需要先Ascend用户转进来的utxo
5. 为了简单一点，现在withdraw直接deAnchor，但deposit不执行anchor
*/

func init() {
	gob.Register(&AmmContractRuntime{})
}

type AmmContract struct {
	SwapContract
	AssetAmt string `json:"assetAmt"`
	SatValue int64  `json:"satValue"`
	K        string `json:"k"`
}

func NewAmmContract() *AmmContract {
	c := &AmmContract{
		SwapContract: *NewSwapContract(),
	}
	c.TemplateName = TEMPLATE_CONTRACT_AMM
	return c
}

func (p *AmmContract) CheckContent() error {
	err := p.ContractBase.CheckContent()
	if err != nil {
		return err
	}

	if p.SatValue <= 0 {
		return fmt.Errorf("invalid sats value %d", p.SatValue)
	}

	if p.AssetAmt == "" || p.AssetAmt == "0" {
		return fmt.Errorf("invalid asset amt %s", p.AssetAmt)
	}
	amt, err := indexer.NewDecimalFromString(p.AssetAmt, MAX_PRICE_DIVISIBILITY)
	if err != nil {
		return fmt.Errorf("invalid amt %s", p.AssetAmt)
	}
	if p.K == "" || p.K == "0" {
		return fmt.Errorf("invalid K %s", p.K)
	}
	k, err := indexer.NewDecimalFromString(p.K, MAX_PRICE_DIVISIBILITY)
	if err != nil {
		return fmt.Errorf("invalid amt %s", p.K)
	}

	if k.Cmp(indexer.DecimalMul(amt, indexer.NewDefaultDecimal(p.SatValue))) != 0 {
		return fmt.Errorf("k is not the result of assetAmt*satValue")
	}

	return nil
}

func (p *AmmContract) InvokeParam(action string) string {

	var param InvokeParam
	param.Action = action
	switch action {
	case INVOKE_API_SWAP:
		var innerParam SwapInvokeParam
		buf, err := json.Marshal(&innerParam)
		if err != nil {
			return ""
		}
		param.Param = string(buf)

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

func (p *AmmContract) Content() string {
	b, err := json.Marshal(p)
	if err != nil {
		Log.Errorf("Marshal AmmContract failed, %v", err)
		return ""
	}
	return string(b)
}

func (p *AmmContract) Encode() ([]byte, error) {
	base, err := p.ContractBase.Encode()
	if err != nil {
		return nil, err
	}

	return txscript.NewScriptBuilder().
		AddData(base).
		AddData([]byte(p.AssetAmt)).
		AddInt64(p.SatValue).
		AddData([]byte(p.K)).Script()
}

func (p *AmmContract) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing base content")
	}
	base := tokenizer.Data()
	err := p.ContractBase.Decode(base)
	if err != nil {
		return err
	}

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing asset amt")
	}
	p.AssetAmt = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing sat value")
	}
	p.SatValue = tokenizer.ExtractInt64()

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing K parameter")
	}
	p.K = string(tokenizer.Data())

	return nil
}

// InvokeParam
type DepositInvokeParam struct {
	OrderType int    `json:"orderType"`
	AssetName string `json:"assetName"` // 资产名字
	Amt       string `json:"amt"`       // 资产数量
}

func (p *DepositInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
		AddData([]byte(p.AssetName)).
		AddData([]byte(p.Amt)).Script()
}

func (p *DepositInvokeParam) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing order type")
	}
	p.OrderType = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing asset name")
	}
	p.AssetName = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing asset amt")
	}
	p.Amt = string(tokenizer.Data())

	return nil
}

type WithdrawInvokeParam = DepositInvokeParam

type AmmContractRuntime struct {
	SwapContractRuntime

	originalValue int64
	originalAmt   *Decimal
	k             *Decimal
}

func NewAmmContractRuntime(stp *Manager) *AmmContractRuntime {
	p := &AmmContractRuntime{
		SwapContractRuntime: SwapContractRuntime{
			SwapContractRuntimeInDB: SwapContractRuntimeInDB{
				Contract: NewAmmContract(),
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

func (p *AmmContractRuntime) InitFromContent(content []byte, stp *Manager) error {
	err := p.SwapContractRuntime.InitFromContent(content, stp)
	if err != nil {
		return err
	}

	contractBase, ok := p.Contract.(*AmmContract)
	if !ok {
		return fmt.Errorf("not AmmContract")
	}

	p.originalAmt, err = indexer.NewDecimalFromString(contractBase.AssetAmt, p.Divisibility)
	if err != nil {
		return err
	}
	p.originalValue = contractBase.SatValue

	p.k, err = indexer.NewDecimalFromString(contractBase.K, p.Divisibility+2)
	if err != nil {
		return err
	}

	return nil
}

func (p *AmmContractRuntime) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)

	if err := enc.Encode(p.Contract); err != nil {
		return nil, err
	}

	if err := enc.Encode(p.ContractRuntimeBase); err != nil {
		return nil, err
	}

	if err := enc.Encode(p.SwapContractRunningData); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (p *AmmContractRuntime) GobDecode(data []byte) error {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)

	var amm AmmContract
	if err := dec.Decode(&amm); err != nil {
		return err
	}
	p.Contract = &amm

	
	if err := dec.Decode(&p.ContractRuntimeBase); err != nil {
		return err
	}

	if err := dec.Decode(&p.SwapContractRunningData); err != nil {
		return err
	}
	

	return nil
}
