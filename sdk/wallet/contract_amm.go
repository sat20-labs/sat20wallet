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
5. 项目方提取池子利润 （只能提取白聪，默认的分配规则：项目方：节点A：节点B：基金会=55:20:20:5）

对该地址上该资产的约定：
1. 合约参数规定的资产和对应数量，由该合约管理。合约需要确保不论L1和L2上，都有对应数量的资产
	a. AmmContract 的参数规定了池子基本资产, 只有这部分资产必须严格要求L1和L2都要有，并且不允许动用
	b. SwapContractRunningData 运行参数，包含了合约运行的盈利，也是合约管理的资产，但可能在L1，也可能在L2
2. 在L1和L2上持有的更多的资产，可以支持withdraw和deposit操作
3. 在L1上，没有被Ascend过的utxo，可以用来支持withdraw。如果没有足够的utxo可用，就必须先Descend一些utxo（DeAnchorTx）。但必须保留AmmContract指定数量资产。
4. 在L2上，超出AmmContract的资产，可以直接send出去给用户，用来支持deposit操作。如果不够，就需要先Ascend用户转进来的utxo
5. 为了简单一点，现在withdraw直接deAnchor，但deposit不执行anchor

AMM V2 交易合约：组池子和动态调整池子
在第一版本的基础上，增加：
1. 池子建立的过程: 初始化参数，池子在满足初始化参数后进入AMM模式，在一个周期后自动调整
2. STAKE和UNSTAKE：在池子的正常运作过程，允许任何stake和unstake，stake和unstake可以swap
3. SETTLEMENT：运行周期结束，计算池子的运行利润，自动分配利润，但不自动构造交易，默认滚动投资
4. FROZEN：池子低于初始水位，自动冻结，不能交易，但支持stake和unstake
5. 默认利润分配比例： LP：节点A：节点B：基金会=55:20:20:5
*/

func init() {
	gob.Register(&AmmContractRuntime{})
}

const (
	DEFAULT_SETTLEMENT_PERIOD int = 5 * 60 * 24 * 7 // 一周
)

type AmmContract struct {
	SwapContract
	AssetAmt string `json:"assetAmt"`
	SatValue int64  `json:"satValue"`
	K        string `json:"k"`

	SettlePeriod int `json:"settlePeriod"` // 区块数，从EnableBlock开始算
}

func NewAmmContract() *AmmContract {
	c := &AmmContract{
		SwapContract: *NewSwapContract(),
	}
	c.TemplateName = TEMPLATE_CONTRACT_AMM
	return c
}

func (p *AmmContract) CheckContent() error {
	err := p.SwapContract.CheckContent()
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

	if p.SettlePeriod != 0 && p.SettlePeriod < DEFAULT_SETTLEMENT_PERIOD {
		return fmt.Errorf("settle period should bigger than %d", DEFAULT_SETTLEMENT_PERIOD)
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
		var innerParam WithdrawInvokeParam
		innerParam.OrderType = ORDERTYPE_WITHDRAW
		buf, err := json.Marshal(&innerParam)
		if err != nil {
			return ""
		}
		param.Param = string(buf)

	case INVOKE_API_STAKE:
		var innerParam StakeInvokeParam
		innerParam.OrderType = ORDERTYPE_STAKE
		buf, err := json.Marshal(&innerParam)
		if err != nil {
			return ""
		}
		param.Param = string(buf)

	case INVOKE_API_UNSTAKE:
		var innerParam UnstakeInvokeParam
		innerParam.OrderType = ORDERTYPE_UNSTAKE
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
	base, err := p.SwapContract.Encode()
	if err != nil {
		return nil, err
	}

	return txscript.NewScriptBuilder().
		AddData(base).
		AddData([]byte(p.AssetAmt)).
		AddInt64(p.SatValue).
		AddData([]byte(p.K)).
		AddInt64(int64(p.SettlePeriod)).
		Script()
}

func (p *AmmContract) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing base content")
	}
	base := tokenizer.Data()
	err := p.SwapContract.Decode(base)
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

	if !tokenizer.Next() || tokenizer.Err() != nil {
		// 老版本没有该字段
		p.SettlePeriod = DEFAULT_SETTLEMENT_PERIOD
	} else {
		p.SettlePeriod = int(tokenizer.ExtractInt64())
	}

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

type StakeInvokeParam struct {
	OrderType int    `json:"orderType"`
	AssetName string `json:"assetName"` // 资产名字
	Amt       string `json:"amt"`       // 资产数量
	Value     int64  `json:"value"`     // 成比例的聪数量
}

func (p *StakeInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
		AddData([]byte(p.AssetName)).
		AddData([]byte(p.Amt)).
		AddInt64(int64(p.Value)).
		Script()
}

func (p *StakeInvokeParam) Decode(data []byte) error {
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

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing sats value")
	}
	p.Value = (tokenizer.ExtractInt64())

	return nil
}

type UnstakeInvokeParam struct {
	OrderType int    `json:"orderType"`
	AssetName string `json:"assetName"` // 资产名字
	Amt       string `json:"amt"`       // 资产数量
	Value     int64  `json:"value"`     // 成比例的聪数量
	ToL1      bool   `json:"toL1"`
}

func (p *UnstakeInvokeParam) Encode() ([]byte, error) {
	var toL1 int64
	if p.ToL1 {
		toL1 = 1
	}
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
		AddData([]byte(p.AssetName)).
		AddData([]byte(p.Amt)).
		AddInt64(int64(p.Value)).
		AddInt64(int64(toL1)).
		Script()
}

func (p *UnstakeInvokeParam) Decode(data []byte) error {
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

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing sats value")
	}
	p.Value = (tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing flag toL1")
	}
	toL1 := (tokenizer.ExtractInt64())
	p.ToL1 = toL1 > 0

	return nil
}

type AmmContractRuntime struct {
	SwapContractRuntime

	originalValue int64
	originalAmt   *Decimal
	originalK     *Decimal
	k             *Decimal
	settlePeriod  int
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
	p.originalK, err = indexer.NewDecimalFromString(contractBase.K, p.Divisibility+2)
	if err != nil {
		return err
	}

	if p.AmmK != nil {
		p.k = p.AmmK.Clone()
	} else {
		p.k = p.originalK.Clone()
		p.AmmK = p.k.Clone()
	}

	if contractBase.SettlePeriod == 0 {
		p.settlePeriod = DEFAULT_SETTLEMENT_PERIOD
	} else {
		p.settlePeriod = contractBase.SettlePeriod
	}
	return nil
}

func (p *AmmContractRuntime) CalcStakeValueByAssetAmt(amt *Decimal) int64 {
	var amtInPool *Decimal
	var valueInPool int64
	if p.SatsValueInPool == 0 {
		amtInPool = p.originalAmt.Clone()
		valueInPool = p.originalValue
	} else {
		amtInPool = p.AssetAmtInPool.Clone()
		valueInPool = p.SatsValueInPool
	}
	d1 := indexer.DecimalMul(amt, indexer.NewDecimal(valueInPool, p.Divisibility))
	d2 := indexer.DecimalDiv(d1, amtInPool)
	return d2.Round()
}

func (p *AmmContractRuntime) CalcStakeAssetAmtByValue(value int64) *Decimal {
	var amtInPool *Decimal
	var valueInPool int64
	if p.SatsValueInPool == 0 {
		amtInPool = p.originalAmt.Clone()
		valueInPool = p.originalValue
	} else {
		amtInPool = p.AssetAmtInPool.Clone()
		valueInPool = p.SatsValueInPool
	}
	d1 := indexer.DecimalMul(amtInPool, indexer.NewDecimal(value, p.Divisibility))
	return indexer.DecimalDiv(d1, indexer.NewDecimal(valueInPool, p.Divisibility))
}

func (p *AmmContractRuntime) GetPeriodCount() int {
	return (p.CurrBlock-p.EnableBlock)/p.settlePeriod
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
