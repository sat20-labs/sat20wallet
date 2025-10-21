package wallet

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

/*
AMM交易合约
1. 池子中满足一定的资产份额（常数K）后，合约激活
2. 两种资产，一种是聪 (TODO 支持两种任意资产)
3. 每笔交易，按照区块顺序自动处理
4. 每个区块处理完成后，统一回款
5. 项目方提取池子利润

对该地址上该资产的约定：
1. 合约参数规定的资产和对应数量，由该合约管理。合约需要确保L2上有对应数量的资产
	a. AmmContract 的参数规定了池子基本资产, 只有这部分资产必须严格要求L1和L2都要有，并且不允许动用
	b. SwapContractRunningData 运行参数，包含了合约运行的盈利，也是合约管理的资产，但可能在L1，也可能在L2
2. 在L1和L2上持有的更多的资产，可以支持withdraw和deposit操作

AMM V2 交易合约：组池子和动态调整池子
在第一版本的基础上，增加：
1. 池子建立的过程: 初始化参数，池子在满足初始化参数后进入AMM模式
2. AddLiq和RemoveLiq：在池子的正常运作过程，随时可以addliq和removeliq
3. 默认利润分配比例： LP:市场:基金会=60:35:5，如果是发射池，50:50

发射池的部署人，可以提走AMM池子底池的利润，利润需要50:50分成。
利润的计算：
1. 假设发射成功时，资产乘积，也就是常数为K1，对应的LPT为lpt1
2. 运行一段时间后，lpt1对应的资产的乘积为K2，那么利润 dK = K2 - K1，只有dK大于零才能提取
3. 将dK按比例折算成资产A和B，提取时提走这些资产，同时LPT下降为lpt2，继续记在Base中


*/


func init() {
	// 让 gob 知道旧的类型对应新的实现
	gob.RegisterName("*stp.AmmContractRuntime", new(AmmContractRuntime))
}


var (
	DEFAULT_SETTLEMENT_PERIOD int = 100000 // 大约2周 10 * 60 * 24 * 7 // 一周

	// 池子利润分配比例
	PROFIT_SHARE_LP         int = 60 // 包括项目方，资金方
	PROFIT_SHARE_MARKET     int = 35 // 包括节点，每个节点10
	PROFIT_SHARE_FOUNDATION int = 5  //

	// 提取底池的利润分成
	PROFIT_SHARE_BASE_LP     int = 50
	PROFIT_SHARE_BASE_MARKET int = 45

	PROFIT_REINVESTING bool = true //
)

type AmmContract struct {
	SwapContract
	AssetAmt string `json:"assetAmt"`
	SatValue int64  `json:"satValue"`
	K        string `json:"k"`

	SettlePeriod int `json:"settlePeriod"` // 区块数，从EnableBlock开始算. 已废弃
}

func calcLPProfit(profit int64) int64 {
	return profit * int64(PROFIT_SHARE_LP) / 100
}
func calcServerProfit(profit int64) int64 {
	return profit * int64(PROFIT_SHARE_MARKET) / 100
}
func calcFoundationProfit(profit int64) int64 {
	return profit * int64(PROFIT_SHARE_FOUNDATION) / 100
}

func calcFeeProfit(profit int64) int64 {
	return profit * int64(100-PROFIT_SHARE_LP) / 100
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

	// if p.SettlePeriod != 0 && p.SettlePeriod < DEFAULT_SETTLEMENT_PERIOD {
	// 	return fmt.Errorf("settle period should bigger than %d", DEFAULT_SETTLEMENT_PERIOD)
	// }

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

	case INVOKE_API_ADDLIQUIDITY:
		var innerParam AddLiqInvokeParam
		innerParam.OrderType = ORDERTYPE_ADDLIQUIDITY
		buf, err := json.Marshal(&innerParam)
		if err != nil {
			return ""
		}
		param.Param = string(buf)

	case INVOKE_API_REMOVELIQUIDITY:
		var innerParam RemoveLiqInvokeParam
		innerParam.OrderType = ORDERTYPE_REMOVELIQUIDITY
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

func (p *AmmContract) CalcStaticMerkleRoot() []byte {
	return CalcContractStaticMerkleRoot(p)
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

func (p *DepositInvokeParam) EncodeV2() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
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

type AddLiqInvokeParam struct {
	OrderType int    `json:"orderType"`
	AssetName string `json:"assetName"` // 资产名字 （合约的资产名称，用于识别是哪一个合约）
	Amt       string `json:"amt"`       // 资产数量
	Value     int64  `json:"value"`     // 成比例的聪数量
}

func (p *AddLiqInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
		AddData([]byte(p.AssetName)).
		AddData([]byte(p.Amt)).
		AddInt64(int64(p.Value)).
		Script()
}

func (p *AddLiqInvokeParam) EncodeV2() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
		AddData([]byte(p.Amt)).
		AddInt64(int64(p.Value)).
		Script()
}

func (p *AddLiqInvokeParam) Decode(data []byte) error {
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

type RemoveLiqInvokeParam struct {
	OrderType int    `json:"orderType"`
	AssetName string `json:"assetName"` // 资产名字
	LptAmt    string `json:"lptAmt"`    // 流动性资产数量
}

func (p *RemoveLiqInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
		AddData([]byte(p.AssetName)).
		AddData([]byte(p.LptAmt)).
		Script()
}

func (p *RemoveLiqInvokeParam) EncodeV2() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
		AddData([]byte(p.LptAmt)).
		Script()
}

func (p *RemoveLiqInvokeParam) Decode(data []byte) error {
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
	p.LptAmt = string(tokenizer.Data())

	return nil
}

type LiquidityData struct {
	Height       int
	TotalAssets  *Decimal            // 本轮开始时池子中资产的数量
	TotalSats    int64               // 本轮开始时池子中聪的数量
	K            *Decimal            // 本轮K参数
	TotalLPToken *Decimal            // 本轮
	LPMap        map[string]*Decimal // address->LPToken  //
}

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

type LiqProviderInfo struct {
	Address string
	LptAmt  *Decimal
}

type AmmContractRuntime struct {
	SwapContractRuntime

	originalValue int64
	originalAmt   *Decimal
	originalK     *Decimal
	k             *Decimal
	liquidityData *LiquidityData

	// rpc
	liqProviders []*LiqProviderInfo
}

func NewAmmContractRuntime(stp ContractManager) *AmmContractRuntime {
	p := &AmmContractRuntime{
		SwapContractRuntime: SwapContractRuntime{
			SwapContractRuntimeInDB: SwapContractRuntimeInDB{
				Contract: NewAmmContract(),
				ContractRuntimeBase: *NewContractRuntimeBase(stp),
				SwapContractRunningData: SwapContractRunningData{},
			},
		},
	}
	p.init()

	return p
}

func (p *AmmContractRuntime) InitFromContent(content []byte, stp ContractManager, resv ContractDeployResvIF) error {
	err := p.SwapContractRuntime.InitFromContent(content, stp, resv)
	if err != nil {
		return err
	}

	err = p.setOriginalValue()
	if err != nil {
		return err
	}

	if !p.isInitiator {
		// 如果是launchpool直接部署的，需要设置下launchpool相关属性
		launchPoolURL := GenerateContractURl(p.ChannelAddr, p.GetAssetName().String(), TEMPLATE_CONTRACT_LAUNCHPOOL)
		contract := p.stp.GetContract(launchPoolURL)
		if contract != nil &&
			(contract.GetStatus() == CONTRACT_STATUS_CLOSED ||
				contract.GetStatus() == CONTRACT_STATUS_CLOSING) {
			launchpool, ok := contract.(*LaunchPoolContractRunTime)
			if ok {
				if launchpool.AssetAmtInPool.Cmp(p.originalAmt) == 0 &&
					launchpool.SatsValueInPool == p.originalValue {
					launchpool.mutex.Lock()
					launchpool.AmmResvId = p.ResvId // 这个时候ResvId还是0 TODO
					launchpool.AmmContractURL = p.URL()
					launchpool.mutex.Unlock()
					stp.SaveReservationWithLock(launchpool.resv)
				}
			}
		}
	}

	return nil
}

func (p *AmmContractRuntime) InitFromJson(content []byte, stp ContractManager) error {
	err := json.Unmarshal(content, p)
	if err != nil {
		return err
	}

	err = p.setOriginalValue()
	if err != nil {
		return err
	}
	return nil
}

func (p *AmmContractRuntime) InitFromDB(stp ContractManager, resv ContractDeployResvIF) error {
	err := p.SwapContractRuntime.InitFromDB(stp, resv)
	if err != nil {
		return err
	}
	err = p.setOriginalValue()
	if err != nil {
		return err
	}

	// if p.GetTemplateName() == TEMPLATE_CONTRACT_AMM {
	// 	err = p.checkSelf()
	// 	if err != nil {
	// 		Log.Errorf("%s checkSelf failed, %v", p.URL(), err)
	// 	}
	// }

	return nil
}

func (p *AmmContractRuntime) setOriginalValue() error {

	contractBase, ok := p.Contract.(*AmmContract)
	if !ok {
		return fmt.Errorf("not AmmContract")
	}

	var err error
	p.originalAmt, err = indexer.NewDecimalFromString(contractBase.AssetAmt, p.Divisibility)
	if err != nil {
		return err
	}
	p.originalValue = contractBase.SatValue
	p.originalK, err = indexer.NewDecimalFromString(contractBase.K, p.Divisibility+2)
	if err != nil {
		return err
	}

	p.k = indexer.DecimalMul(indexer.NewDecimal(p.SatsValueInPool, p.Divisibility+2), p.AssetAmtInPool)

	p.liquidityData = p.loadLatestLiquidityData()

	// if p.IsReady() {
	// 	if p.k.Cmp(p.originalK) < 0 {
	// 		Log.Infof("%s k %s less than original k %s", p.URL(), p.k.String(), p.originalK.String())
	// 		// 通过增加p.SatsValueInPool来满足需求
	// 		d2 := indexer.DecimalDiv(p.originalK, p.AssetAmtInPool)
	// 		p.SatsValueInPool = d2.Floor()+1
	// 		p.k = indexer.DecimalMul(indexer.NewDecimal(p.SatsValueInPool, p.Divisibility+2), p.AssetAmtInPool)
	// 		if p.k.Cmp(p.originalK) < 0 {
	// 			Log.Panicf("%s k %s less than original k %s", p.URL(), p.k.String(), p.originalK.String())
	// 		}
	// 	}
	// 	Log.Infof("%s k = %s, original k = %s", p.URL(), p.k.String(), p.originalK.String())

	// 	if p.TotalLptAmt.Sign() == 0 {
	// 		// 从老版本升级上来，需要设置基础值
	// 		p.TotalLptAmt = indexer.DecimalMul(indexer.NewDecimal(p.SatsValueInPool, MAX_ASSET_DIVISIBILITY), p.AssetAmtInPool).Sqrt()
	// 		p.BaseLptAmt = p.TotalLptAmt.Clone()
	// 	}
	// 	saveReservationWithLock(p.stp.GetDB(), p.resv)
	// }

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
	return d2.Floor()
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

func (p *AmmContractRuntime) IsReadyToRun(deployTx *swire.MsgTx) error {

	if deployTx == nil {
		return fmt.Errorf("no deploy TX")
	}

	_, _, err := p.CheckDeployTx(deployTx)
	if err != nil {
		return err
	}

	return nil
}

func (p *AmmContractRuntime) SetReady() {
	p.ContractRuntimeBase.SetReady()

	// 不要通过判断池子地址上的资金方式，而是检查对应的stake数据
	// 地址上的资金来源很杂，不一定是池子的stake资金
	// 如果是继承了launchpool的资金，只检查该launchpool是否存在，并且当前是处于第一个周期。

	p.Status = CONTRACT_STATUS_ADJUSTING
	if p.CurrBlock <= p.EnableBlock {
		launchPoolURL := GenerateContractURl(p.ChannelAddr, p.GetAssetName().String(), TEMPLATE_CONTRACT_LAUNCHPOOL)
		contract := p.stp.GetContract(launchPoolURL)
		if contract != nil && (contract.GetStatus() == CONTRACT_STATUS_CLOSED ||
			contract.GetStatus() == CONTRACT_STATUS_CLOSING) {
			launchpool, ok := contract.(*LaunchPoolContractRunTime)
			if ok {
				if launchpool.AssetAmtInPool.Cmp(p.originalAmt) == 0 &&
					launchpool.SatsValueInPool == p.originalValue {
					//
					p.Status = CONTRACT_STATUS_READY
					p.AssetAmtInPool = p.originalAmt.Clone()
					p.SatsValueInPool = p.originalValue
					p.k = indexer.DecimalMul(indexer.NewDecimal(p.SatsValueInPool, p.Divisibility+2), p.AssetAmtInPool)
					p.TotalLptAmt = indexer.DecimalMul(indexer.NewDecimal(p.SatsValueInPool, MAX_ASSET_DIVISIBILITY), p.AssetAmtInPool).Sqrt()
					p.BaseLptAmt = p.TotalLptAmt.Clone()
				}
			}
		}
	}

	resv := p.stp.GetSpecialContractResv(p.GetAssetName().String(), TEMPLATE_CONTRACT_TRANSCEND)
	if resv != nil {
		// disable transcend contract
		resv.SetStatus(RS_DEPLOY_CONTRACT_SUPPENDED)
		p.stp.SaveReservation(resv)
	}
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

	if ContractRuntimeBaseUpgrade {
		var old ContractRuntimeBase_old
		if err := dec.Decode(&old); err != nil {
			return err
		}
		p.ContractRuntimeBase = *old.ToNewVersion()

		var old2 SwapContractRunningData_old
		if err := dec.Decode(&old2); err != nil {
			return err
		}
		p.SwapContractRunningData = *old2.ToNewVersion()

	} else {
		if err := dec.Decode(&p.ContractRuntimeBase); err != nil {
			return err
		}

		if err := dec.Decode(&p.SwapContractRunningData); err != nil {
			return err
		}
	}

	return nil
}

func (p *AmmContractRuntime) InvokeWithBlock_SatsNet(data *InvokeDataInBlock_SatsNet) error {

	// 需要让合约进入激活状态，才能处理stake和unstake的调用
	//
	err := p.ContractRuntimeBase.InvokeWithBlock_SatsNet(data)
	if err != nil {
		return err
	}

	if p.IsActive() {
		p.mutex.Lock()
		// 先保存池子中资产数量，因为processInvoke_SatsNet会更新池子数据，但这个更新不是我们所期望的

		beforeAmt := p.AssetAmtInPool.Clone()
		beforeValue := p.SatsValueInPool
		//Log.Infof("%s InvokeWithBlock_SatsNet %d %s %d", stp.GetMode(), data.Height, beforeAmt.String(), beforeValue)

		p.processInvoke_SatsNet(data)
		p.swap(beforeAmt, beforeValue)
		p.settle(data.Height)
		p.ContractRuntimeBase.InvokeCompleted_SatsNet(data)
		p.mutex.Unlock()

		p.sendInvokeResultTx_SatsNet()
	} else {
		p.mutex.Lock()
		p.ContractRuntimeBase.InvokeCompleted_SatsNet(data)
		p.mutex.Unlock()
	}

	return nil
}

func (p *AmmContractRuntime) InvokeWithBlock(data *InvokeDataInBlock) error {

	err := p.ContractRuntimeBase.InvokeWithBlock(data)
	if err != nil {
		return err
	}

	if p.IsActive() {
		p.mutex.Lock()
		// 先保存池子中资产数量，因为processInvoke会更新池子数据，但这个更新不是我们所期望的
		beforeAmt := p.AssetAmtInPool.Clone()
		beforeValue := p.SatsValueInPool
		Log.Infof("%s InvokeWithBlock %d %s %d", p.stp.GetMode(), data.Height, beforeAmt.String(), beforeValue)

		p.processInvoke(data)
		// 确保在区块后马上执行swap，发送可以等等
		p.swap(beforeAmt, beforeValue)
		p.settle(data.Height)
		p.ContractRuntimeBase.InvokeCompleted(data)
		p.mutex.Unlock()

		p.sendInvokeResultTx()
	} else {
		p.mutex.Lock()
		p.ContractRuntimeBase.InvokeCompleted(data)
		p.mutex.Unlock()
	}

	return nil
}

// 结果提高了精度
func RealSwapValue(value int64) *Decimal {
	return indexer.NewDecimal(value*(1000-SWAP_SERVICE_FEE_RATIO), 3).Div(
		indexer.NewDecimal(1000, 3))
}

// 结果提高精度
func RealSwapAmt(amt *Decimal) *Decimal {
	if amt == nil {
		return nil
	}
	return indexer.DecimalMulV2(amt, indexer.NewDecimal(1000-SWAP_SERVICE_FEE_RATIO, amt.Precision+3)).
		Div(indexer.NewDecimal(1000, amt.Precision+3))
}

// 执行交易，每个区块统一执行一次
func (p *AmmContractRuntime) swap(assetAmtInPool *Decimal, satsValueInPool int64) bool {

	if p.Status != CONTRACT_STATUS_READY {
		return false
	}

	if len(p.buyPool)+len(p.sellPool) == 0 {
		return false
	}
	if len(p.buyPool) == 0 && len(p.sellPool) == 0 {
		return false
	}

	Log.Debugf("%s start contract %s with action swap, buy %d, sell %d", p.stp.GetMode(), p.URL(), len(p.buyPool), len(p.sellPool))

	url := p.URL()
	ammPool := make([]*SwapHistoryItem, len(p.buyPool)+len(p.sellPool))
	i := 0
	for _, item := range p.buyPool {
		ammPool[i] = item
		i++
	}
	for _, item := range p.sellPool {
		ammPool[i] = item
		i++
	}
	// 按在区块中的顺序交易
	sort.Slice(ammPool, func(i, j int) bool {
		return ammPool[i].UtxoId < ammPool[j].UtxoId
	})

	oldK := p.k.Clone()
	updated := false
	refundItems := make([]*SwapHistoryItem, 0)
	for _, item := range ammPool {
		if item.Reason != INVOKE_REASON_NORMAL || item.Done != DONE_NOTYET {
			refundItems = append(refundItems, item)
			continue
		}

		if item.RemainingValue == 0 && item.RemainingAmt.Sign() == 0 {
			continue
		}
		// 设置item的版本
		item.Version = 1 // 0 是初始版本，服务费全部是聪
		// 1 是uniswap v2版本，输入资产扣除服务费，再参与交易，这样累积两种资产的数量

		// AMM核心公式：Δy = y - k/(x+Δx)
		// Δx = item.InAmt or item.InValue，扣除服务费后参与交易
		// out = outPool - k/(newInPool)
		// TODO 对于输入值太大的，拆成小分，逐个交易，平滑池子的交易曲线

		// 先提高精度做计算，至少提高3个数量级，因为手续费0.008

		if item.OrderType == ORDERTYPE_BUY {
			realswapValue := RealSwapValue(item.RemainingValue)
			if realswapValue.Sign() == 0 {
				Log.Errorf("AMM buy %s: in_amt=%s, min_value=%s, real_value=%s, utxo: %s", INVOKE_REASON_NO_ENOUGH_ASSET,
					item.InAmt.String(), item.ExpectedAmt.String(), realswapValue, item.InUtxo)
				item.Reason = INVOKE_REASON_NO_ENOUGH_ASSET
				item.Done = DONE_CLOSED_DIRECTLY // 不退款，直接关闭
				refundItems = append(refundItems, item)
				continue
			}

			kDivNewIn := indexer.DecimalDiv(p.k, indexer.DecimalAdd(indexer.NewDecimal(satsValueInPool, 3), realswapValue))
			outAmt := indexer.DecimalSub(assetAmtInPool, kDivNewIn)

			if outAmt.Sign() <= 0 { // 不大可能会走这里
				// 兑换失败，池子余额不足或参数异常，直接退款
				Log.Errorf("AMM buy %s: in_value=%d, min_amt=%s, real_amt=%s, utxo: %s", INVOKE_REASON_INNER_ERROR,
					item.InValue, item.ExpectedAmt.String(), outAmt.String(), item.InUtxo)
				item.Reason = INVOKE_REASON_INNER_ERROR
				refundItems = append(refundItems, item)
				continue
			}

			// 滑点保护判断：最小可接受资产数量
			if item.ExpectedAmt.Sign() != 0 && outAmt.Cmp(item.ExpectedAmt) < 0 {
				Log.Errorf("AMM buy %s: in_value=%d, min_amt=%s, real_amt=%s, utxo: %s", INVOKE_REASON_SLIPPAGE_PROTECT,
					item.InValue, item.ExpectedAmt.String(), outAmt.String(), item.InUtxo)
				// 实际成交量小于用户期望，拒绝成交
				item.Reason = INVOKE_REASON_SLIPPAGE_PROTECT
				refundItems = append(refundItems, item)
				continue
			}

			p.LastDealPrice = indexer.DecimalDiv(realswapValue.SetPrecision(MAX_PRICE_DIVISIBILITY), outAmt)

			// 更新池子
			satsValueInPool += item.RemainingValue // 利润留存在池子中
			assetAmtInPool = assetAmtInPool.Sub(outAmt)
			p.TotalDealAssets = p.TotalDealAssets.Add(outAmt)

			// 更新item
			item.RemainingValue = 0
			item.OutAmt = outAmt

			Log.Infof("AMM buy dealt: in_value=%d, out_amt=%s, price=%s, utxo: %s",
				item.InValue, item.OutAmt.String(), p.LastDealPrice.String(), item.InUtxo)

		} else if item.OrderType == ORDERTYPE_SELL {
			realSwapAmt := RealSwapAmt(item.RemainingAmt)
			if realSwapAmt.Sign() == 0 {
				Log.Errorf("AMM sell %s: in_amt=%s, min_value=%s, real_amt=%s, utxo: %s", INVOKE_REASON_NO_ENOUGH_ASSET,
					item.InAmt.String(), item.ExpectedAmt.String(), realSwapAmt.String(), item.InUtxo)
				item.Reason = INVOKE_REASON_NO_ENOUGH_ASSET
				item.Done = DONE_CLOSED_DIRECTLY // 不退款，直接关闭
				refundItems = append(refundItems, item)
				continue
			}

			kDivNewIn := indexer.DecimalDiv(p.k, indexer.DecimalAdd(assetAmtInPool, realSwapAmt))
			outValue := satsValueInPool - kDivNewIn.Ceil()

			if outValue <= 0 { // 不大可能走这里
				Log.Errorf("AMM sell %s: in_amt=%s, min_value=%s, real_value=%d, utxo: %s", INVOKE_REASON_INNER_ERROR,
					item.InAmt.String(), item.ExpectedAmt.String(), outValue, item.InUtxo)
				// 兑换失败，池子余额不足或参数异常
				item.Reason = INVOKE_REASON_INNER_ERROR
				refundItems = append(refundItems, item)
				continue
			}

			// 滑点保护判断：最小可接受聪数量
			if item.ExpectedAmt.Sign() != 0 && outValue < item.ExpectedAmt.Int64() {
				Log.Errorf("AMM sell %s: in_amt=%s, min_value=%s, real_value=%d, utxo: %s", INVOKE_REASON_SLIPPAGE_PROTECT,
					item.InAmt.String(), item.ExpectedAmt.String(), outValue, item.InUtxo)
				// 实际获得聪数量小于用户期望，拒绝成交
				item.Reason = INVOKE_REASON_SLIPPAGE_PROTECT
				refundItems = append(refundItems, item)
				continue
			}

			item.OutValue = outValue
			if item.OutValue <= 0 {
				Log.Errorf("AMM sell %s: in_amt=%s, min_value=%s, real_value=%d, utxo: %s", INVOKE_REASON_NO_ENOUGH_ASSET,
					item.InAmt.String(), item.ExpectedAmt.String(), outValue, item.InUtxo)
				item.OutValue = 0
				item.OutAmt = nil
				item.RemainingAmt = nil
				item.Reason = INVOKE_REASON_NO_ENOUGH_ASSET
				item.Done = DONE_CLOSED_DIRECTLY // 不退款，直接关闭
				refundItems = append(refundItems, item)
				continue
			}

			p.LastDealPrice = indexer.DecimalDiv(
				indexer.NewDecimal(outValue, MAX_PRICE_DIVISIBILITY), realSwapAmt)

			// 更新池子
			assetAmtInPool = assetAmtInPool.Add(item.RemainingAmt) // 利润留在池子中
			satsValueInPool -= outValue
			p.TotalDealSats += outValue

			// 更新item
			item.RemainingAmt = nil

			Log.Infof("AMM sell dealt: in_amt=%s, out_value=%d, price=%s, utxo: %s",
				item.InAmt.String(), item.OutValue, p.LastDealPrice.String(), item.InUtxo)
		} else {
			Log.Errorf("AMM unsupport %d: %v", item.OrderType, item)
			// 暂时不支持的交易
			refundItems = append(refundItems, item)
			continue
		}

		item.UnitPrice = p.LastDealPrice.Clone()
		if p.HighestDealPrice.Cmp(p.LastDealPrice) < 0 {
			p.HighestDealPrice = p.LastDealPrice.Clone()
		}
		if p.LowestDealPrice == nil || p.LowestDealPrice.Cmp(p.LastDealPrice) > 0 {
			p.LowestDealPrice = p.LastDealPrice.Clone()
		}

		// 更新k
		p.k = indexer.DecimalMul(indexer.NewDecimal(satsValueInPool, p.Divisibility+2), assetAmtInPool)

		p.TotalDealCount++
		SaveContractInvokeHistoryItem(p.stp.GetDB(), url, item)
		updated = true
	}

	for _, item := range refundItems {
		p.addRefundItem(item, true)
		SaveContractInvokeHistoryItem(p.stp.GetDB(), url, item)
	}

	// 最终更新池子
	p.SatsValueInPool = satsValueInPool
	p.AssetAmtInPool = assetAmtInPool.Clone()
	Log.Infof("Pool(%d): value=%d, amt=%s, k=%s (+%s), contract %s",
		p.CurrBlock, satsValueInPool, assetAmtInPool.String(), p.k.String(), indexer.DecimalSub(p.k, oldK).String(), p.GetContractName())

	// 交易的结果先保存
	if updated {
		p.stp.SaveReservationWithLock(p.resv)
		// if p.InvokeCount%100 == 0 {
		// 	p.checkSelf()
		// }
	}

	return updated
}

func (p *AmmContractRuntime) loadLatestLiquidityData() *LiquidityData {
	url := p.URL()
	liquidityData, err := loadLiquidityData(p.stp.GetDB(), url)
	if err != nil {
		liquidityData = &LiquidityData{
			LPMap: make(map[string]*Decimal),
		}
	}
	return liquidityData
}

// 保存当前池子的快照
func (p *AmmContractRuntime) saveLatestLiquidityData(height int) {
	p.liquidityData.K = p.k.Clone()
	p.liquidityData.Height = height
	p.liquidityData.TotalAssets = p.AssetAmtInPool.Clone()
	p.liquidityData.TotalSats = p.SatsValueInPool
	p.liquidityData.TotalLPToken = p.TotalLptAmt.Clone()
	saveLiquidityData(p.stp.GetDB(), p.URL(), p.liquidityData)
	p.refreshTime_swap = 0
}

func (p *AmmContractRuntime) GetLiquidityData(start, limit int) string {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	if p.refreshTime_swap == 0 || len(p.liqProviders) == 0 {
		p.liqProviders = nil
		if p.BaseLptAmt.Sign() != 0 {
			p.liqProviders = append(p.liqProviders, &LiqProviderInfo{
				Address: p.Deployer,
				LptAmt:  p.BaseLptAmt.Clone(),
			})
		}
		if p.TotalFeeLptAmt.Sign() != 0 {
			var serverAddress string
			if p.isInitiator {
				serverAddress = p.GetLocalAddress()
			} else {
				serverAddress = p.GetRemoteAddress()
			}
			p.liqProviders = append(p.liqProviders, &LiqProviderInfo{
				Address: serverAddress,
				LptAmt:  p.TotalFeeLptAmt.Clone(),
			})
		}
		for k, v := range p.liquidityData.LPMap {
			p.liqProviders = append(p.liqProviders, &LiqProviderInfo{
				Address: k,
				LptAmt:  v.Clone(),
			})
		}
		sort.Slice(p.liqProviders, func(i, j int) bool {
			return p.liqProviders[i].LptAmt.Cmp(p.liqProviders[j].LptAmt) < 0
		})
	}

	type response struct {
		TotalLptAmt *Decimal           `json:"totalLptAmt"`
		Total       int                `json:"total"`
		Start       int                `json:"start"`
		Data        []*LiqProviderInfo `json:"data"`
	}
	defaultRsp := `{"total":0,"start":0,"data":[]}`

	total := len(p.liqProviders)
	result := &response{
		TotalLptAmt: p.TotalLptAmt.Clone(),
		Total:       total,
		Start:       start,
	}

	if limit <= 0 {
		limit = 100
	}

	end := start + limit
	if end > total {
		end = total
	}
	result.Data = p.liqProviders[start:end]

	buf, err := json.Marshal(result)
	if err != nil {
		Log.Errorf("Marshal GetLatestLiquidityData failed, %v", err)
		return defaultRsp
	}
	return string(buf)
}

type LPInfo struct {
	ReserveAmt   *Decimal
	ReserveValue int64
	LeftAmt      *Decimal
	LeftValue    int64
}

func (p *AmmContractRuntime) generateLPmap(price *Decimal) (map[string]*LPInfo, *Decimal, int64) {
	liqProviderMap := make(map[string]*LPInfo)
	for k, v := range p.addLiquidityMap {
		var amt *Decimal
		var value int64
		for _, item := range v {
			amt = amt.Add(item.RemainingAmt)
			value += item.RemainingValue
		}

		info, ok := liqProviderMap[k]
		if !ok {
			info = &LPInfo{}
			liqProviderMap[k] = info
		}
		info.ReserveAmt = amt
		info.ReserveValue = value
	}

	if len(liqProviderMap) == 0 {
		return nil, nil, 0
	}

	// 计算新增加的LPToken
	var totalAddedValue int64
	var totalAddedAmt *Decimal
	for _, v := range liqProviderMap {
		reserveValue := v.ReserveValue
		reserveAmt := indexer.DecimalDiv(indexer.NewDecimal(v.ReserveValue, p.Divisibility), price)
		if reserveAmt.Cmp(v.ReserveAmt) > 0 {
			reserveValue = indexer.DecimalMul(v.ReserveAmt, price).Ceil()
			reserveAmt = v.ReserveAmt.Clone()
		}
		if reserveValue > v.ReserveValue {
			reserveValue = v.ReserveValue
		}

		// 剩余的
		v.LeftAmt = indexer.DecimalSub(v.ReserveAmt, reserveAmt)
		v.LeftValue = v.ReserveValue - reserveValue
		// 更新实际进入池子的
		v.ReserveAmt = reserveAmt
		v.ReserveValue = reserveValue

		totalAddedValue += reserveValue
		totalAddedAmt = totalAddedAmt.Add(reserveAmt)
	}
	return liqProviderMap, totalAddedAmt, totalAddedValue
}

func (p *AmmContractRuntime) updateLiquidity(oldAmtInPool *Decimal, oldValueInPool int64,
	oldTotalLptAmt *Decimal, price *Decimal,
	liqProviderMap map[string]*LPInfo, totalAddedAmt *Decimal, totalAddedValue int64) {
	// 已经准备好了，更新数据
	url := p.URL()
	var totalAddedLptAmt *Decimal
	for k, v := range liqProviderMap {
		// 计算获得的LPToken数量
		lpToken1 := indexer.NewDecimal(v.ReserveValue, MAX_ASSET_DIVISIBILITY).
			Mul(oldTotalLptAmt).
			Div(indexer.NewDecimal(oldValueInPool, p.Divisibility))
		lpToken2 := v.ReserveAmt.Mul(oldTotalLptAmt).Div(oldAmtInPool)
		var lpToken *Decimal
		if lpToken1.Cmp(lpToken2) <= 0 {
			lpToken = lpToken1
		} else {
			lpToken = lpToken2
		}
		totalAddedLptAmt = totalAddedLptAmt.Add(lpToken)

		trader := p.loadTraderInfo(k)
		trader.LptAmt = trader.LptAmt.Add(lpToken)
		trader.RetrieveAmt = trader.RetrieveAmt.Add(v.LeftAmt)
		trader.RetrieveValue += v.LeftValue
		trader.LiqSatsValue += v.ReserveValue + indexer.DecimalMul(price, v.ReserveAmt).Floor()

		p.liquidityData.LPMap[k] = trader.LptAmt.Clone()

		// 更新用户的item
		items := p.addLiquidityMap[k]
		for _, item := range items {
			if item.Done == DONE_NOTYET && item.Reason == INVOKE_REASON_NORMAL {
				item.Done = DONE_DEALT
				delete(p.history, item.InUtxo)
				if item.RemainingValue <= v.ReserveValue {
					v.ReserveValue -= item.RemainingValue
					item.OutValue = item.RemainingValue
					item.RemainingValue = 0
				} else {
					item.OutValue = v.ReserveValue
					item.RemainingValue -= v.ReserveValue // 余额部分
					v.ReserveValue = 0
				}
				if item.RemainingAmt.Cmp(v.ReserveAmt) <= 0 {
					v.ReserveAmt = v.ReserveAmt.Sub(item.RemainingAmt)
					item.OutAmt = item.RemainingAmt.Clone()
					item.RemainingAmt = nil
				} else {
					item.OutAmt = v.ReserveAmt.Clone()
					item.RemainingAmt = item.RemainingAmt.Sub(v.ReserveAmt) // 余额部分
					v.ReserveAmt = nil
				}
				SaveContractInvokeHistoryItem(p.stp.GetDB(), url, item)
			}
		}
		saveContractInvokerStatus(p.stp.GetDB(), url, trader)
	}
	p.addLiquidityMap = make(map[string]map[int64]*SwapHistoryItem)

	Log.Infof("total added lpt = %s, added asset %s %d", totalAddedLptAmt.String(), totalAddedAmt.String(), totalAddedValue)

	p.AssetAmtInPool = p.AssetAmtInPool.Add(totalAddedAmt)
	p.SatsValueInPool += totalAddedValue
	p.TotalLptAmt = p.TotalLptAmt.Add(totalAddedLptAmt)
	p.TotalAddedLptAmt = p.TotalAddedLptAmt.Add(totalAddedLptAmt)
	p.k = indexer.DecimalMul(indexer.NewDecimal(p.SatsValueInPool, p.Divisibility+2), p.AssetAmtInPool)

}

// 在enable后调用。一次完成，进入ready状态。外面加锁
func (p *AmmContractRuntime) initLiquidity(height int) error {
	if p.Status == CONTRACT_STATUS_ADJUSTING {
		// 仅在初始化时，会有这个状态，后续不再出现这个状态
		url := p.URL()
		if len(p.addLiquidityMap) == 0 {
			return nil
		}
		price := indexer.DecimalDiv(indexer.NewDecimal(p.originalValue, MAX_ASSET_DIVISIBILITY), p.originalAmt)

		liqProviderMap, totalAddedAmt, totalAddedValue := p.generateLPmap(price)
		if len(liqProviderMap) == 0 {
			return nil
		}

		k := indexer.DecimalMul(totalAddedAmt, indexer.NewDecimal(totalAddedValue, p.Divisibility))
		if k.Cmp(p.originalK) < 0 {
			Log.Infof("%s not ready, k = %s", url, k.String())
			return nil
		}

		oldTotalLptAmt := indexer.DecimalMul(indexer.NewDecimal(totalAddedValue, MAX_ASSET_DIVISIBILITY), totalAddedAmt).Sqrt()
		p.updateLiquidity(totalAddedAmt, totalAddedValue, oldTotalLptAmt, price, liqProviderMap, totalAddedAmt, totalAddedValue)
		p.Status = CONTRACT_STATUS_READY
		p.stp.SaveReservationWithLock(p.resv)
		p.saveLatestLiquidityData(height) // 更新流动性数据

		Log.Infof("%s initiated liquidity, k = %s, lpt = %s", url, p.k.String(), p.TotalLptAmt.String())
	}
	return nil
}

// 在settle中调用
func (p *AmmContractRuntime) addLiquidity(oldAmtInPool *Decimal, oldValueInPool int64, oldTotalLptAmt *Decimal) error {

	if len(p.addLiquidityMap) == 0 {
		return nil
	}
	if oldAmtInPool.Sign() == 0 {
		Log.Errorf("%s no asset in pool", p.URL())
		p.Status = CONTRACT_STATUS_ADJUSTING
		return nil
	}

	price := indexer.DecimalDiv(indexer.NewDecimal(oldValueInPool, MAX_ASSET_DIVISIBILITY), oldAmtInPool)

	// 新增加资产必须保持同样的比例，投入池子
	// 每个人都需要按比例出资
	liqProviderMap, totalAddedAmt, totalAddedValue := p.generateLPmap(price)
	if len(liqProviderMap) == 0 {
		return nil
	}
	p.updateLiquidity(oldAmtInPool, oldValueInPool, oldTotalLptAmt, price, liqProviderMap, totalAddedAmt, totalAddedValue)

	Log.Infof("%s added liquidity, k = %s, lpt = %s", p.URL(), p.k.String(), p.TotalLptAmt.String())

	return nil
}

// 仅在settle中调用
func (p *AmmContractRuntime) removeLiquidity(oldAmtInPool *Decimal, oldValueInPool int64, oldTotalLptAmt *Decimal) error {

	if len(p.removeLiquidityMap) == 0 {
		return nil
	}

	type lpInfo struct {
		LptAmt *Decimal
	}

	removeLiqMap := make(map[string]*lpInfo)
	for k, v := range p.removeLiquidityMap {
		var lptAmt *Decimal
		for _, item := range v {
			if item.Done == DONE_NOTYET &&
				item.Reason == INVOKE_REASON_NORMAL &&
				len(item.Padded) == 0 { // 设置了该条记录处理的periodId
				lptAmt = lptAmt.Add(item.ExpectedAmt)
			}
		}
		if lptAmt.Sign() == 0 {
			continue
		}

		info, ok := removeLiqMap[k]
		if !ok {
			info = &lpInfo{}
			removeLiqMap[k] = info
		}
		info.LptAmt = lptAmt
	}
	if len(removeLiqMap) == 0 {
		return nil
	}

	price := indexer.DecimalDiv(indexer.NewDecimal(oldValueInPool, MAX_ASSET_DIVISIBILITY), oldAmtInPool)
	oldTotalPoolValue := 2 * oldValueInPool
	lptPerSat := indexer.DecimalDiv(oldTotalLptAmt.NewPrecision(MAX_ASSET_DIVISIBILITY), indexer.NewDecimal(oldTotalPoolValue, MAX_ASSET_DIVISIBILITY))

	// var market, fundation *TraderStatus
	// if p.isInitiator {
	// 	market = p.loadTraderInfo(p.GetLocalAddress())
	// 	fundation = p.loadTraderInfo(p.GetRemoteAddress())
	// } else {
	// 	fundation = p.loadTraderInfo(p.GetLocalAddress())
	// 	market = p.loadTraderInfo(p.GetRemoteAddress())
	// }

	url := p.URL()
	// 将要取回的LPToken，转换为对应的资产，并调整池子容量
	var totalRemovedLptAmt *Decimal
	var totalAddedFeeLptAmt *Decimal
	var totalRemovedAmt *Decimal
	var totalRemovedValue int64
	for k, v := range removeLiqMap {
		// 计算获得的资产数量
		trader := p.loadTraderInfo(k)
		if trader.LptAmt.Sign() <= 0 {
			Log.Warningf("%s has not enough LPToken, require %s but only %s", k, v.LptAmt.String(), trader.LptAmt.String())
			continue
		}
		if trader.LptAmt.Cmp(v.LptAmt) < 0 {
			Log.Warningf("%s has not enough LPToken, require %s but only %s", k, v.LptAmt.String(), trader.LptAmt.String())
			// 修改为全部取出
			v.LptAmt = trader.LptAmt.Clone()
		}

		lptRatio := indexer.DecimalDiv(v.LptAmt, oldTotalLptAmt)

		retrivevAmt := indexer.DecimalMul(oldAmtInPool, lptRatio)
		retrivevValue := indexer.DecimalMul(indexer.NewDecimal(oldValueInPool, p.Divisibility), lptRatio)

		// 转换为sats
		totalRetrieveSats := retrivevValue.Floor() + indexer.DecimalMul(price, retrivevAmt).Floor()
		// 成本
		depositValue := indexer.NewDecimal(trader.LiqSatsValue, MAX_ASSET_DIVISIBILITY).Mul(v.LptAmt).Div(trader.LptAmt).Floor()
		// 减少成本
		trader.LiqSatsValue -= depositValue
		// 利润(用聪来表示)
		profitValue := totalRetrieveSats - depositValue
		if profitValue > 0 {
			// 扣去归属服务的利润
			lpProfitValue := calcLPProfit(profitValue)
			svrProfitValue := profitValue - lpProfitValue
			discountRatio := indexer.NewDecimal(totalRetrieveSats-svrProfitValue, MAX_ASSET_DIVISIBILITY).Div(indexer.NewDecimal(totalRetrieveSats, MAX_ASSET_DIVISIBILITY))
			// 用户的输出扣除对应比例
			retrivevAmt = retrivevAmt.Mul(discountRatio)
			retrivevValue = retrivevValue.Mul(discountRatio)

			if PROFIT_REINVESTING {
				// 服务费用折算为对应的lpt
				feeLptAmt := indexer.DecimalMul(lptPerSat, indexer.NewDecimal(svrProfitValue, MAX_ASSET_DIVISIBILITY))
				totalAddedFeeLptAmt = totalAddedFeeLptAmt.Add(feeLptAmt)
			} else {
				// 直接提走

			}
		}

		trader.LptAmt = trader.LptAmt.Sub(v.LptAmt)
		trader.RetrieveAmt = trader.RetrieveAmt.Add(retrivevAmt) // 在retrieve中发送出去
		trader.RetrieveValue += retrivevValue.Floor()
		trader.SettleState = SETTLE_STATE_REMOVING_LIQ_READY
		saveContractInvokerStatus(p.stp.GetDB(), url, trader)

		totalRemovedLptAmt = totalRemovedLptAmt.Add(v.LptAmt)
		totalRemovedAmt = totalRemovedAmt.Add(retrivevAmt)
		totalRemovedValue += retrivevValue.Floor()

		if trader.LptAmt.Sign() > 0 {
			p.liquidityData.LPMap[k] = trader.LptAmt.Clone()
		} else {
			delete(p.liquidityData.LPMap, k)
		}

		// 更新用户的item
		items := p.removeLiquidityMap[k]
		for _, item := range items {
			if item.Done == DONE_NOTYET && item.Reason == INVOKE_REASON_NORMAL {
				item.Padded = []byte(fmt.Sprintf("%d", 1)) // 设置下标志，防止重入
				// 发送出去后再更新该字段
				// item.Done = DONE_DEALT
				//delete(p.history, item.InUtxo)
				// item.OutValue = item.RemainingValue
				// item.RemainingValue = 0
				// item.OutAmt = item.RemainingAmt.Clone()
				// item.RemainingAmt = nil
				SaveContractInvokeHistoryItem(p.stp.GetDB(), url, item)
			}
		}
	}
	Log.Infof("total removed lpt = %s, AddedFeeLpt = %s, retrieved asset %s %d", totalRemovedLptAmt.String(), totalAddedFeeLptAmt.String(), totalRemovedAmt.String(), totalRemovedValue)

	realRemovedLpt := totalRemovedLptAmt.Sub(totalAddedFeeLptAmt)
	// 更新池子数据
	p.AssetAmtInPool = p.AssetAmtInPool.Sub(totalRemovedAmt)
	p.SatsValueInPool -= totalRemovedValue
	p.TotalLptAmt = p.TotalLptAmt.Sub(realRemovedLpt)
	p.TotalRemovedLptAmt = p.TotalRemovedLptAmt.Add(realRemovedLpt)
	p.TotalFeeLptAmt = p.TotalFeeLptAmt.Add(totalAddedFeeLptAmt)
	p.k = indexer.DecimalMul(indexer.NewDecimal(p.SatsValueInPool, p.Divisibility+2), p.AssetAmtInPool)
	Log.Infof("%s removed liquidity, k = %s, lpt = %s", url, p.k.String(), p.TotalLptAmt.String())

	if p.AssetAmtInPool.Sign() <= 0 || p.SatsValueInPool <= 0 {
		Log.Errorf("%s no asset in pool", p.URL())
		p.Status = CONTRACT_STATUS_ADJUSTING
	}

	return nil
}

// AddSingleSidedLiquidity 仅增加聪
func (p *AmmContractRuntime) addSingleSidedLiquidity(value int64) (lpMinted *Decimal, err error) {
	
	if value <= 0 {
		return nil, fmt.Errorf("innvalid value")
	}
/*
设池子当前状态（快照）：
A = 池中资产 A（数量，单位：asset）
B = 池中聪（sats）
K=A⋅B

用户只注入 ΔB（sats）。要实现 等效按比例注入（用户最终获得的 ΔA′,ΔB′ 满足 ΔA′/A=ΔB′/B），
但用户没有直接提供 A，只提供 B。系统可以用用户提供的部分 B 去做「内部 B→A 的 swap」，产生 ΔA′。

变量：
令 x = 用于内部 swap 的那部分 B（输入给 swap 的 B）
则剩下直接进入池子的 B 数为 ΔB−x
经过 B→A 的 swap（无手续费、恒定乘积模型），池中 A 会被减少到 
AafterSwap=K/(B+x)。用户从池里拿走的 A（即 swap 给用户的 A）为
amountAFromSwap=𝐴−𝐾/(𝐵+𝑥).
这正是用户“通过 swap 得到”的 A，记作 ΔA′.

最终加入到池子实际被当作流动性的量为：
ΔA′=amountAFromSwap （来自 swap）
ΔB′=ΔB−x （未用于 swap，直接存入）
我们要求：ΔA′/A = ΔB′/B
代入并化简（注意 K=AB）：
等式变为 𝑥/(𝐵+𝑥)=(Δ𝐵−𝑥)/𝐵
整理成关于 x 的二次方程（把ΔB写为D）：
移项得到 x^2+(2B−D)x−DB=0
判别式 Δ=(2B−D)^2+4DB=D^2+4B^2
正根（取能满足0≤x≤D 的）：
x=[−(2B−D)+sqrt(D^2+4B^2)]/2

用该 x：
ΔA′=A−K/(B+x)
ΔB′=D−x
并且满足 
ΔA′/A=ΔB′/B

然后按照常规比例铸造 LP：
LPmint=LPtotal⋅ΔA′/A
（等价地也可用 ΔB′/B）
*/

	// 原始状态
	A := p.AssetAmtInPool.Clone()
	B := indexer.NewDecimal(p.SatsValueInPool, 3)
	// 不扣除手续费
	D := indexer.NewDecimal(value, 3)

	// 计算 D^2 + 4B^2，D是输入聪，B是池子中聪数量
	D2 := D.Mul(D)
	B2 := B.Mul(B)
	four := indexer.NewDecimal(4, A.Precision)
	disc := D2.Add(four.Mul(B2))

	// sqrt(D^2 + 4B^2)
	sqrtDisc := disc.Sqrt()

	// -(2B - D) + sqrtDisc
	two := indexer.NewDecimal(2, A.Precision)
	twoB := B.Mul(two)
	num := sqrtDisc.Sub(twoB.Sub(D))

	// x: 内部兑换成A的那一部分聪
	// x = (- (2B - D) + sqrt(D^2 + 4B^2)) / 2
	x := num.Div(two)

	// A' = A - K/(B + x)
	//BplusX := B.Add(x)
	//newA := K.Div(BplusX)
	//deltaAprime := A.Sub(newA)

	// ΔB' = D - x
	deltaBprime := D.Sub(x)

	// LP minted = LP_total * (ΔA'/A)
	//        or = LP_total * (ΔB'/B)
	lpMinted = p.TotalLptAmt.Mul(deltaBprime.Div(B))

	// 更新池子状态
	// p.AssetAmtInPool 不会改变
	p.SatsValueInPool += value
	p.k = indexer.DecimalMul(indexer.NewDecimal(p.SatsValueInPool, p.Divisibility+2), p.AssetAmtInPool)
	p.TotalLptAmt = p.TotalLptAmt.Add(lpMinted)

	return lpMinted, nil
}

// 每个区块高度调用，需要合约处于激活状态。调用前不能加锁
func (p *AmmContractRuntime) settle(height int) error {
	// 不能加锁
	if p.Status == CONTRACT_STATUS_ADJUSTING {
		p.initLiquidity(height)
	}

	if p.Status == CONTRACT_STATUS_READY {

		// 如果有单边加池子，先处理单边加池子，而且必须按照顺序

		// 确保基数相同（本轮交易后的池子参数）
		oldAmtInPool := p.AssetAmtInPool.Clone()
		oldValueInPool := p.SatsValueInPool
		oldTotalLptAmt := p.TotalLptAmt

		p.addLiquidity(oldAmtInPool, oldValueInPool, oldTotalLptAmt)
		p.removeLiquidity(oldAmtInPool, oldValueInPool, oldTotalLptAmt)

		p.stp.SaveReservationWithLock(p.resv)
		p.saveLatestLiquidityData(height)
	}

	return nil
}

// 仅用于amm合约
func VerifyAmmHistory(history []*SwapHistoryItem, poolAmt *Decimal, poolValue int64,
	divisibility int, org *SwapContractRunningData) (*SwapContractRunningData, error) {

	InvokeCount := int64(0)
	traderInfoMap := make(map[string]*TraderStatus)
	var runningData SwapContractRunningData
	runningData.AssetAmtInPool = poolAmt.Clone()
	runningData.SatsValueInPool = poolValue

	// 重新生成统计数据
	var onSendingVaue int64
	var onSendngAmt *Decimal
	refundTxMap := make(map[string]bool)
	dealTxMap := make(map[string]bool)
	withdrawTxMap := make(map[string]bool)
	depositTxMap := make(map[string]bool)
	unstakeTxMap := make(map[string]bool)
	for i, item := range history {
		if int64(i) != item.Id {
			return nil, fmt.Errorf("missing history. previous %d, current %d", i-1, item.Id)
		}
		// if item.Id == 61 {
		// 	Log.Infof("")
		// }

		trader, ok := traderInfoMap[item.Address]
		if !ok {
			trader = NewTraderStatus(item.Address, divisibility)
			traderInfoMap[item.Address] = trader
		}
		insertItemToTraderHistroy(&trader.InvokerStatusBase, item)
		trader.DealAmt = trader.DealAmt.Add(item.OutAmt)
		trader.DealValue += CalcDealValue(item.OutValue)

		InvokeCount++
		runningData.TotalInputSats += item.InValue
		runningData.TotalInputAssets = runningData.TotalInputAssets.Add(item.InAmt)
		if item.Done != DONE_NOTYET {
			runningData.TotalOutputAssets = runningData.TotalOutputAssets.Add(item.OutAmt)
			runningData.TotalOutputSats += item.OutValue
		}

		switch item.OrderType {
		case ORDERTYPE_BUY, ORDERTYPE_SELL:

			switch item.Done {
			case DONE_NOTYET:
				Log.Errorf("amm should handle item already. %v", item)
				if item.Reason == INVOKE_REASON_NORMAL {
					// 有效的，还在交易中，或者交易完成，准备发送
					if item.RemainingAmt.Sign() == 0 && item.RemainingValue == 0 {
						onSendingVaue += item.OutValue
						onSendngAmt = onSendngAmt.Add(item.OutAmt)
					}

					// 跟DONE_DEALT同样处理
					if item.OrderType == ORDERTYPE_BUY {
						runningData.TotalDealAssets = runningData.TotalDealAssets.Add(item.OutAmt)
						//runningData.TotalDealSats += item.InValue - calcSwapFee(item.InValue)
						runningData.SatsValueInPool += item.GetTradingValueForAmm()
						runningData.AssetAmtInPool = runningData.AssetAmtInPool.Sub(item.OutAmt)
					} else if item.OrderType == ORDERTYPE_SELL {
						//runningData.TotalDealAssets = runningData.TotalDealAssets.Add(item.InAmt)
						runningData.TotalDealSats += CalcDealValue(item.OutValue)
						runningData.AssetAmtInPool = runningData.AssetAmtInPool.Add(item.InAmt)
						runningData.SatsValueInPool -= CalcDealValue(item.OutValue)
					}

					Log.Infof("OnSending %d: Amt: %s-%s-%s Value: %d-%d-%d Price: %s in: %s", item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
						item.InValue, item.RemainingValue, item.OutValue, item.UnitPrice.String(), item.InUtxo)
				} else {
					// 无效的，即将退款
					Log.Infof("Refunding %d: Amt: %s-%s-%s Value: %d-%d-%d Price: %s in: %s reason: %s", item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
						item.InValue, item.RemainingValue, item.OutValue, item.UnitPrice.String(), item.InUtxo, item.Reason)
					runningData.TotalRefundAssets = runningData.TotalRefundAssets.Add(item.RemainingAmt).Add(item.OutAmt)
					runningData.TotalRefundSats += item.RemainingValue + item.OutValue
				}

			case DONE_DEALT:
				dealTxMap[item.OutTxId] = true
				runningData.TotalDealCount++
				if len(dealTxMap) != runningData.TotalDealCount {
					Log.Infof("")
				}
				runningData.TotalDealTx = len(dealTxMap)
				runningData.TotalDealTxFee = int64(runningData.TotalDealTx) * DEFAULT_FEE_SATSNET
				if item.OrderType == ORDERTYPE_BUY {
					runningData.TotalDealAssets = runningData.TotalDealAssets.Add(item.OutAmt)
					//runningData.TotalDealSats += item.InValue - calcSwapFee(item.InValue)
					runningData.SatsValueInPool += item.GetTradingValueForAmm()
					runningData.AssetAmtInPool = runningData.AssetAmtInPool.Sub(item.OutAmt)
				} else if item.OrderType == ORDERTYPE_SELL {
					//runningData.TotalDealAssets = runningData.TotalDealAssets.Add(item.InAmt)
					runningData.AssetAmtInPool = runningData.AssetAmtInPool.Add(item.InAmt)
					runningData.SatsValueInPool -= CalcDealValue(item.OutValue)
					runningData.TotalDealSats += CalcDealValue(item.OutValue)
				}

				// 已经发送
				Log.Infof("swap %d: Amt: %s-%s-%s Value: %d-%d-%d Price: %s in: %s out: %s", item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
					item.InValue, item.RemainingValue, item.OutValue, item.UnitPrice.String(), item.InUtxo, item.OutTxId)

			case DONE_REFUNDED:
				Log.Infof("Refund %d: Amt: %s-%s-%s Value: %d-%d-%d in: %s out: %s", item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
					item.InValue, item.RemainingValue, item.OutValue, item.InUtxo, item.OutTxId)
				// 退款
				refundTxMap[item.OutTxId] = true
				runningData.TotalRefundTx = len(refundTxMap)
				runningData.TotalRefundTxFee = int64(runningData.TotalRefundTx) * DEFAULT_FEE_SATSNET
				runningData.TotalRefundAssets = runningData.TotalRefundAssets.Add(item.OutAmt)
				runningData.TotalRefundSats += item.OutValue

				if len(refundTxMap) != runningData.TotalRefundTx {
					Log.Infof("")
				}
			}

			Log.Infof("%d: Pool=(%s %d), K=%s", i, runningData.AssetAmtInPool.String(),
				runningData.SatsValueInPool, indexer.DecimalMul(runningData.AssetAmtInPool, indexer.NewDefaultDecimal(runningData.SatsValueInPool)).String())

		case ORDERTYPE_DEPOSIT:
			switch item.Done {
			case DONE_NOTYET:
			case DONE_DEALT:
				depositTxMap[item.OutTxId] = true
				runningData.TotalDepositTx = len(depositTxMap)
				runningData.TotalDealTxFee = 0

				runningData.TotalDepositAssets = runningData.TotalDepositAssets.Add(item.OutAmt)
				runningData.TotalDepositSats += item.OutValue

				// 已经发送
				Log.Infof("deposit %d: Amt: %s-%s-%s Value: %d-%d-%d Price: %s in: %s out: %s", item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
					item.InValue, item.RemainingValue, item.OutValue, item.UnitPrice.String(), item.InUtxo, item.OutTxId)
			}

		case ORDERTYPE_WITHDRAW:
			switch item.Done {
			case DONE_NOTYET:
			case DONE_DEALT:
				_, ok := withdrawTxMap[item.OutTxId]
				if !ok {
					// 新的withdraw txid
					withdrawTxMap[item.OutTxId] = true
					runningData.TotalWithdrawTx = len(withdrawTxMap)
					fee, err := strconv.ParseInt(string(item.Padded), 10, 64)
					if err == nil {
						runningData.TotalWithdrawTxFee += fee
						runningData.TotalOutputSats += fee
					}
				}

				runningData.TotalWithdrawAssets = runningData.TotalWithdrawAssets.Add(item.OutAmt)
				runningData.TotalWithdrawSats += item.OutValue

				// 已经发送
				Log.Infof("withdraw %d: Amt: %s-%s-%s Value: %d-%d-%d Price: %s in: %s out: %s", item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
					item.InValue, item.RemainingValue, item.OutValue, item.UnitPrice.String(), item.InUtxo, item.OutTxId)
			}

		case ORDERTYPE_ADDLIQUIDITY:
			switch item.Done {
			case DONE_NOTYET:
			case DONE_DEALT:
				runningData.TotalStakeAssets = runningData.TotalStakeAssets.Add(item.OutAmt)
				runningData.TotalStakeSats += item.OutValue

				Log.Infof("stake %d: Amt: %s-%s-%s Value: %d-%d-%d Price: %s in: %s out: %s", item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
					item.InValue, item.RemainingValue, item.OutValue, item.UnitPrice.String(), item.InUtxo, item.OutTxId)
			}

		case ORDERTYPE_REMOVELIQUIDITY:
			switch item.Done {
			case DONE_NOTYET:
			case DONE_DEALT:
				unstakeTxMap[item.OutTxId] = true
				runningData.TotalUnstakeTx = len(unstakeTxMap)
				runningData.TotalUnstakeTxFee = int64(runningData.TotalUnstakeTx) * DEFAULT_FEE_SATSNET

				runningData.TotalUnstakeAssets = runningData.TotalUnstakeAssets.Add(item.OutAmt)
				runningData.TotalUnstakeSats += item.OutValue

				// 已经发送
				Log.Infof("unstake %d: Amt: %s-%s-%s Value: %d-%d-%d Price: %s in: %s out: %s", item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
					item.InValue, item.RemainingValue, item.OutValue, item.UnitPrice.String(), item.InUtxo, item.OutTxId)

			}

		default:
			Log.Infof("unsupport(%d) %d: Amt: %s-%s-%s Value: %d-%d-%d in: %s out: %s", item.OrderType, item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
				item.InValue, item.RemainingValue, item.OutValue, item.InUtxo, item.OutTxId)
		}
	}
	runningData.TotalOutputSats += int64(len(refundTxMap)+len(dealTxMap)) * DEFAULT_FEE_SATSNET

	// 对比数据
	Log.Infof("OnSending: value: %d, amt: %s", onSendingVaue, onSendngAmt.String())
	Log.Infof("runningData: \nsimu: %v\nreal: %v", runningData, *org)

	// Log.Infof("assetName: %s", p.GetAssetName())
	// amt := p.stp.GetAssetBalance_SatsNet(p.ChannelId, p.GetAssetName())
	// Log.Infof("amt: %s", amt.String())
	// value := p.stp.GetAssetBalance_SatsNet(p.ChannelId, &indexer.ASSET_PLAIN_SAT)
	// Log.Infof("value: %d", value)

	err := "different: "
	if runningData.SatsValueInPool != org.SatsValueInPool {
		Log.Errorf("SatsValueInPool: %d %d", runningData.SatsValueInPool, org.SatsValueInPool)
		err = fmt.Sprintf("%s SatsValueInPool", err)
	}
	if runningData.AssetAmtInPool.Cmp(org.AssetAmtInPool) != 0 {
		Log.Errorf("AssetAmtInPool: %s %s", runningData.AssetAmtInPool.String(), org.AssetAmtInPool.String())
		err = fmt.Sprintf("%s AssetAmtInPool", err)
	}
	if runningData.TotalDealSats != org.TotalDealSats {
		Log.Errorf("TotalDealSats: %d %d", runningData.TotalDealSats, org.TotalDealSats)
		err = fmt.Sprintf("%s TotalDealSats", err)
	}
	if runningData.TotalDealAssets.Cmp(org.TotalDealAssets) != 0 {
		Log.Errorf("TotalDealAssets: %s %s", runningData.TotalDealAssets.String(), org.TotalDealAssets.String())
		err = fmt.Sprintf("%s TotalDealAssets", err)
	}
	if runningData.TotalWithdrawSats != org.TotalWithdrawSats {
		Log.Errorf("TotalWithdrawSats: %d %d", runningData.TotalWithdrawSats, org.TotalWithdrawSats)
		err = fmt.Sprintf("%s TotalWithdrawSats", err)
	}
	if runningData.TotalRefundAssets.Cmp(org.TotalRefundAssets) != 0 {
		Log.Errorf("TotalRefundAssets: %s %s", runningData.TotalRefundAssets.String(), org.TotalRefundAssets.String())
		err = fmt.Sprintf("%s TotalRefundAssets", err)
	}
	if runningData.TotalWithdrawSats != org.TotalWithdrawSats {
		Log.Errorf("TotalWithdrawSats: %d %d", runningData.TotalWithdrawSats, org.TotalWithdrawSats)
		err = fmt.Sprintf("%s TotalWithdrawSats", err)
	}
	if runningData.TotalWithdrawAssets.Cmp(org.TotalWithdrawAssets) != 0 {
		Log.Errorf("TotalWithdrawAssets: %s %s", runningData.TotalWithdrawAssets.String(), org.TotalWithdrawAssets.String())
		err = fmt.Sprintf("%s TotalWithdrawAssets", err)
	}
	if runningData.TotalStakeSats != org.TotalStakeSats {
		Log.Errorf("TotalStakeSats: %d %d", runningData.TotalStakeSats, org.TotalStakeSats)
		err = fmt.Sprintf("%s TotalStakeSats", err)
	}
	if runningData.TotalStakeAssets.Cmp(org.TotalStakeAssets) != 0 {
		Log.Errorf("TotalStakeAssets: %s %s", runningData.TotalStakeAssets.String(), org.TotalStakeAssets.String())
		err = fmt.Sprintf("%s TotalStakeAssets", err)
	}
	if runningData.TotalUnstakeSats != org.TotalUnstakeSats {
		Log.Errorf("TotalUnstakeSats: %d %d", runningData.TotalUnstakeSats, org.TotalUnstakeSats)
		err = fmt.Sprintf("%s TotalUnstakeSats", err)
	}
	if runningData.TotalUnstakeAssets.Cmp(org.TotalUnstakeAssets) != 0 {
		Log.Errorf("TotalUnstakeAssets: %s %s", runningData.TotalUnstakeAssets.String(), org.TotalUnstakeAssets.String())
		err = fmt.Sprintf("%s TotalUnstakeAssets", err)
	}
	if runningData.TotalInputSats != org.TotalInputSats {
		Log.Errorf("TotalInputSats: %d %d", runningData.TotalInputSats, org.TotalInputSats)
		err = fmt.Sprintf("%s TotalInputSats", err)
	}
	if runningData.TotalInputAssets.Cmp(org.TotalInputAssets) != 0 {
		Log.Errorf("TotalInputAssets: %s %s", runningData.TotalInputAssets.String(), org.TotalInputAssets.String())
		err = fmt.Sprintf("%s TotalInputAssets", err)
	}
	if runningData.TotalOutputSats != org.TotalOutputSats {
		Log.Errorf("TotalOutputSats: %d %d", runningData.TotalOutputSats, org.TotalOutputSats)
		err = fmt.Sprintf("%s TotalOutputSats", err)
	}
	if runningData.TotalOutputAssets.Cmp(org.TotalOutputAssets) != 0 {
		Log.Errorf("TotalOutputAssets: %s %s", runningData.TotalOutputAssets.String(), org.TotalOutputAssets.String())
		err = fmt.Sprintf("%s TotalOutputAssets", err)
	}

	if err == "different: " {
		return &runningData, nil
	}

	Log.Errorf(err)
	return &runningData, fmt.Errorf("%s", err)
}

// 仅用于amm合约
func (p *AmmContractRuntime) checkSelf() error {
	url := p.URL()
	history := LoadContractInvokeHistory(p.stp.GetDB(), url, false, false)
	Log.Infof("%s history count: %d\n", url, len(history))
	if len(history) == 0 {
		return nil
	}

	mid1 := make([]*SwapHistoryItem, 0)
	for _, v := range history {
		item, ok := v.(*SwapHistoryItem)
		if !ok {
			continue
		}
		mid1 = append(mid1, item)

		// fix
		// if item.OrderType == ORDERTYPE_DEPOSIT || item.OrderType == ORDERTYPE_WITHDRAW {
		// 	item.ToL1 = !item.FromL1
		// 	saveContractInvokeHistoryItem(p.stp.GetDB(), url, item)
		// }
	}

	sort.Slice(mid1, func(i, j int) bool {
		return mid1[i].Id < mid1[j].Id
	})

	// 导出历史记录用于测试
	// Log.Infof("url: %s\n", url)
	// buf, _ := json.Marshal(mid1)
	// Log.Infof("items: %s\n", string(buf))
	// buf, _ = json.Marshal(p.SwapContractRunningData)
	// Log.Infof("running data: %s\n", string(buf))

	runningData, err := VerifyAmmHistory(mid1, p.originalAmt, p.originalValue,
		p.Divisibility, &p.SwapContractRunningData)

	// 更新统计
	p.updateRunningData(runningData)

	if err != nil {
		Log.Errorf(err.Error())
		return err
	}
	return nil
}
