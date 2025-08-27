package wallet

import (
	"bytes"

	"encoding/gob"
	"encoding/json"
	"fmt"

	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

/*
限价单交易合约
1. 挂限价的卖单和买单
2. 每个区块进行交易撮合
3. 一个单完全被吃点后，在该区块处理完成后自动回款
4. 如何计算swap交易的利润：
*/

func init() {
	gob.Register(&SwapContractRuntime{})
}

const ADDR_OPRETURN string = "op_return"

type SwapContract struct {
	ContractBase
}

func NewSwapContract() *SwapContract {
	return &SwapContract{
		ContractBase: ContractBase{
			TemplateName: TEMPLATE_CONTRACT_SWAP,
		},
	}
}

func (p *SwapContract) GetContractName() string {
	return p.AssetName.String() + URL_SEPARATOR + p.TemplateName
}

func (p *SwapContract) GetAssetName() *swire.AssetName {
	return &p.AssetName
}

func (p *SwapContract) Content() string {
	b, err := json.Marshal(p)
	if err != nil {
		Log.Errorf("Marshal Contract failed, %v", err)
		return ""
	}
	return string(b)
}

// 仅仅是估算，并且尽可能多预估了输入和输出
func (p *SwapContract) DeployFee(feeRate int64) int64 {
	return DEFAULT_SERVICE_FEE_DEPLOY_CONTRACT + DEFAULT_FEE_SATSNET + SWAP_INVOKE_FEE // deployTx 的utxo含聪量
}

func (p *SwapContract) InvokeParam(action string) string {

	switch action {
	case INVOKE_API_SWAP:
		var innerParam SwapInvokeParam
		buf, err := json.Marshal(&innerParam)
		if err != nil {
			return ""
		}

		var param InvokeParam
		param.Action = INVOKE_API_SWAP
		param.Param = string(buf)

		result, err := json.Marshal(&param)
		if err != nil {
			return ""
		}

		return string(result)
	default:
		return ""
	}

}

type SwapHistoryItem = InvokeItem

// 买单用来购买的聪数量，只适合swap合约，amm合约需要通过InValue来计算参与交易的聪数量
func (p *SwapHistoryItem) GetTradingValue() int64 {
	return indexer.DecimalMul(p.UnitPrice, p.ExpectedAmt).Ceil()
}

// 买单用来购买的聪数量，只适合amm
func (p *SwapHistoryItem) GetTradingValueForAmm() int64 {
	return ((p.InValue-SWAP_INVOKE_FEE)*1000 + 1000 + SWAP_SERVICE_FEE_RATIO - 1) / (1000 + SWAP_SERVICE_FEE_RATIO)
}

// InvokeParam
type SwapInvokeParam struct {
	OrderType int    `json:"orderType"`
	AssetName string `json:"assetName"` // 要买或者卖的资产名称
	Amt       string `json:"amt"`       // 要买或者卖的资产数量，以utxo带的资产为准，需要加判断
	UnitPrice string `json:"unitPrice"` // 资产价格，默认是聪的对价
}

func (p *SwapInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
		AddData([]byte(p.AssetName)).
		AddData([]byte(p.Amt)).
		AddData([]byte(p.UnitPrice)).Script()
}

func (p *SwapInvokeParam) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)

	// if !tokenizer.Next() || tokenizer.Err() != nil {
	// 	return fmt.Errorf("missing amt")
	// }
	// p.Amt = string(tokenizer.Data())

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
		return fmt.Errorf("missing unit price")
	}
	p.UnitPrice = string(tokenizer.Data())

	return nil
}

type SwapContractRunningData struct {
	AssetAmtInPool   *Decimal // 池子中资产的数量
	SatsValueInPool  int64    // 池子中聪的数量
	LowestSellPrice  *Decimal
	HighestBuyPrice  *Decimal
	LastDealPrice    *Decimal
	HighestDealPrice *Decimal
	LowestDealPrice  *Decimal

	TotalInputAssets *Decimal // 所有进入池子交易的资产数量，（包括异常的）
	TotalInputSats   int64    // 所有进入池子交易的聪数量，（包括异常的）

	TotalDealAssets *Decimal // 已经卖出的资产数量
	TotalDealSats   int64    // 已经买入资产的聪的数量
	TotalDealCount  int      // 总成交次数
	TotalDealTx     int      //
	TotalDealTxFee  int64

	TotalRefundAssets *Decimal // 对同一个挂单来说，不包括已经成交部分
	TotalRefundSats   int64    // 对同一个挂单来说，不包括已经成交部分
	TotalRefundTx     int
	TotalRefundTxFee  int64

	// 项目方提取利润的交易在这里记录
	TotalProfitAssets *Decimal //
	TotalProfitSats   int64    //
	TotalProfitTx     int
	TotalProfitTxFee  int64

	TotalDepositAssets *Decimal
	TotalDepositSats   int64
	TotalDepositTx     int   // 无用
	TotalDepositTxFee  int64 // 无用

	TotalWithdrawAssets *Decimal
	TotalWithdrawSats   int64
	TotalWithdrawTx     int
	TotalWithdrawTxFee  int64

	// 新增加
	TotalStakeAssets *Decimal
	TotalStakeSats   int64

	TotalUnstakeAssets *Decimal
	TotalUnstakeSats   int64
	TotalUnstakeTx     int
	TotalUnstakeTxFee  int64

	TotalOutputAssets  *Decimal
	TotalOutputSats    int64
}

func (p *SwapContractRunningData) ToNewVersion() *SwapContractRunningData {
	return p
}

type SwapContractRuntimeInDB struct {
	Contract
	ContractRuntimeBase

	SwapContractRunningData
}

// 非数据记录
type TraderStatistic struct {
	InvokeCount int

	OnSaleAmt     *Decimal
	OnBuyValue    int64
	DealAmt       *Decimal // 只累加卖单中成交的资产数量
	DealValue     int64    // 只累加买单中成交的聪数量
	RefundAmt     *Decimal
	RefundValue   int64
	DepositAmt    *Decimal
	DepositValue  int64
	WithdrawAmt   *Decimal
	WithdrawValue int64
	ProfitAmt     *Decimal
	ProfitValue   int64
	StakeAmt      *Decimal
	StakeValue    int64
	UnstakeAmt    *Decimal
	UnstakeValue  int64
}

// 数据库记录: 老版本 version=0
type TraderStatusV0 struct {
	InvokerStatusBase
	Address     string
	OnSaleAmt   *Decimal
	OnBuyValue  int64
	DealAmt     *Decimal // 只累加卖单中成交的资产数量
	DealValue   int64    // 只累加买单中成交的聪数量
	ProfitAmt   *Decimal
	ProfitValue int64

	SwapUtxoMap   map[string]bool // utxo map 交易中的记录
	ProfitUtxoMap map[string]bool // utxo map
}

func (p *TraderStatusV0) Statistic() *TraderStatistic {
	return &TraderStatistic{
		InvokeCount:   p.InvokeCount,
		OnSaleAmt:     p.OnSaleAmt,
		OnBuyValue:    p.OnBuyValue,
		DealAmt:       p.DealAmt,
		DealValue:     p.DealValue,
		RefundAmt:     p.RefundAmt,
		RefundValue:   p.RefundValue,
		DepositAmt:    p.DepositAmt,
		DepositValue:  p.DepositValue,
		WithdrawAmt:   p.WithdrawAmt,
		WithdrawValue: p.WithdrawValue,
		ProfitAmt:     p.ProfitAmt,
		ProfitValue:   p.ProfitValue,
	}
}

func NewTraderStatusV0(address string, divisibility int) *TraderStatusV0 {
	return &TraderStatusV0{
		InvokerStatusBase: *NewInvokerStatusBase(address, divisibility),
		OnSaleAmt:         indexer.NewDecimal(0, divisibility),
		DealAmt:           indexer.NewDecimal(0, divisibility),
		ProfitAmt:         indexer.NewDecimal(0, divisibility),
		SwapUtxoMap:       make(map[string]bool),
		ProfitUtxoMap:     make(map[string]bool),
	}
}

// 新版本，version=1
type TraderStatus struct {
	TraderStatusV0

	StakeAmt     *Decimal
	StakeValue   int64
	UnStakeAmt   *Decimal
	UnStakeValue int64

	StakeUtxoMap   map[string]bool // utxo map
	UnstakeUtxoMap map[string]bool // utxo map
}

func (p *TraderStatus) Statistic() *TraderStatistic {
	return &TraderStatistic{
		InvokeCount:   p.InvokeCount,
		OnSaleAmt:     p.OnSaleAmt,
		OnBuyValue:    p.OnBuyValue,
		DealAmt:       p.DealAmt,
		DealValue:     p.DealValue,
		RefundAmt:     p.RefundAmt,
		RefundValue:   p.RefundValue,
		DepositAmt:    p.DepositAmt,
		DepositValue:  p.DepositValue,
		WithdrawAmt:   p.WithdrawAmt,
		WithdrawValue: p.WithdrawValue,
		ProfitAmt:     p.ProfitAmt,
		ProfitValue:   p.ProfitValue,
		StakeAmt:      p.StakeAmt,
		StakeValue:    p.StakeValue,
		UnstakeAmt:    p.UnStakeAmt,
		UnstakeValue:  p.UnStakeValue,
	}
}

func NewTraderStatus(address string, divisibility int) *TraderStatus {
	s := &TraderStatus{
		TraderStatusV0: *NewTraderStatusV0(address, divisibility),
		StakeAmt:       indexer.NewDecimal(0, divisibility),
		UnStakeAmt:     indexer.NewDecimal(0, divisibility),
		StakeUtxoMap:   make(map[string]bool),
		UnstakeUtxoMap: make(map[string]bool),
	}
	s.Version = 1
	return s
}

type SwapContractRuntime struct {
	SwapContractRuntimeInDB

	buyPool       []*SwapHistoryItem                    // 还没有成交的记录，按价格从大到小排序
	sellPool      []*SwapHistoryItem                    // 还没有成交的记录，按价格从小到大排序
	traderInfoMap map[string]*TraderStatus              // user address -> utxo list 还没有成交的记录
	refundMap     map[string]map[int64]*SwapHistoryItem // 准备退款的账户, address -> refund invoke item list, 无效的item也放进来，一起退款
	depositMap    map[string]map[int64]*SwapHistoryItem // 准备deposit的账户, address -> deposit invoke item list
	withdrawMap   map[string]map[int64]*SwapHistoryItem // 准备withdraw的账户, address -> withdraw invoke item list
	stakeMap      map[string]map[int64]*SwapHistoryItem // 准备stake的账户, address -> stake invoke item list
	unstakeMap    map[string]map[int64]*SwapHistoryItem // 准备unstake的账户, address -> unstake invoke item list
	stubFeeMap    map[int64]int64                       // invokeCount->fee
	isSending     bool

	refreshTime_swap   int64
	responseCache_swap []*responseItem_swap
	responseStatus     *responseStatus_swap
	responseHistory    map[int][]*SwapHistoryItem // 按照100个为一桶，根据区块顺序记录，跟swapHistory保持一致
	responseAnalytics  *AnalytcisData
	dealPrice          *Decimal
}

func NewSwapContractRuntime(stp *Manager) *SwapContractRuntime {
	p := &SwapContractRuntime{
		SwapContractRuntimeInDB: SwapContractRuntimeInDB{
			Contract: NewSwapContract(),
			ContractRuntimeBase: ContractRuntimeBase{
				DeployTime: time.Now().Unix(),
				stp:        stp,
			},
			SwapContractRunningData: SwapContractRunningData{},
		},
	}
	p.init()

	return p
}

func (p *SwapContractRuntime) init() {
	p.contract = p
	p.runtime = p
	p.history = make(map[string]*SwapHistoryItem)
	p.buyPool = make([]*SwapHistoryItem, 0)
	p.sellPool = make([]*SwapHistoryItem, 0)
	p.traderInfoMap = make(map[string]*TraderStatus)
	p.refundMap = make(map[string]map[int64]*SwapHistoryItem)
	p.depositMap = make(map[string]map[int64]*SwapHistoryItem)
	p.withdrawMap = make(map[string]map[int64]*SwapHistoryItem)
	p.stakeMap = make(map[string]map[int64]*SwapHistoryItem)
	p.unstakeMap = make(map[string]map[int64]*SwapHistoryItem)
	p.stubFeeMap = make(map[int64]int64)
	p.responseHistory = make(map[int][]*SwapHistoryItem)
}

func (p *SwapContractRuntime) GobEncode() ([]byte, error) {
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

func (p *SwapContractRuntime) GobDecode(data []byte) error {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)

	var swap SwapContract
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

func (p *SwapContractRuntime) GetAssetAmount() (*Decimal, int64) {
	return p.AssetAmtInPool, p.SatsValueInPool
}

func (p *SwapContractRuntime) RuntimeContent() []byte {
	b, err := EncodeToBytes(p)
	if err != nil {
		Log.Errorf("Marshal SwapContractRuntime failed, %v", err)
		return nil
	}
	return b
}

type DepthInfo struct {
	Price string // 资产价格
	Amt   string // 资产数量
	Value int64  // 聪数量
}

type responseStatus_swap struct {
	*SwapContractRuntimeInDB

	// 增加更多参数
	DealPrice    string       `json:"dealPrice"`
	BuyDepth     []*DepthInfo `json:"buyDepth"`
	SellDepth    []*DepthInfo `json:"sellDepth"`
	TradeInfo24H *ItemData    `json:"24hour"`
	TradeInfo30D *ItemData    `json:"30day"`
}

// ItemData 数据项
// swagger:model
type ItemData struct {
	OrderCount int64 `json:"order_count"` // 指定时间段内的订单数量
	// 指定时间段内的平均价格
	// example: 1200.0
	AvgPrice *Decimal `json:"avg_price" swaggertype:"string"`
	// 指定时间段内的资产交易量
	// example: 500.0
	AssetAmt  *Decimal `json:"amount" swaggertype:"string"`
	SatsValue int64    `json:"volume"` // 指定时间段内的交易额
}

func NewItemData() *ItemData {
	return &ItemData{}
}

// AnalytcisItem 分析数据项
// swagger:model
type AnalytcisItem struct {
	Date string `json:"date"` // 数据对应的日期
	Time string `json:"time"` // 数据对应的时间
	ItemData
}

func NewAnalytcisItem() *AnalytcisItem {
	return &AnalytcisItem{
		ItemData: *NewItemData(),
	}
}

// AnalytcisData 资产分析数据
// swagger:model
type AnalytcisData struct {
	AssetsName   *indexer.AssetName `json:"assets_name"`
	Items24Hours []*AnalytcisItem   `json:"items_24hours"`
	Items30Days  []*AnalytcisItem   `json:"items_30days"`
}

const (
	HOUR_SECOND = 60 * 60
	DAY_SECOND  = 24 * HOUR_SECOND
)

type responseItem_swap struct {
	Address string `json:"address"`
	Sell    int    `json:"sell"`
	Buy     int    `json:"buy"`
}

func (p *SwapContractRuntime) DeploySelf() bool {
	return false
}

func (p *SwapContractRuntime) CheckInvokeParam(param string) (int64, error) {
	var invoke InvokeParam
	err := json.Unmarshal([]byte(param), &invoke)
	if err != nil {
		return 0, err
	}
	assetName := p.GetAssetName()
	templateName := p.GetTemplateName()
	switch invoke.Action {
	case INVOKE_API_SWAP:
		var swapParam SwapInvokeParam
		err := json.Unmarshal([]byte(invoke.Param), &swapParam)
		if err != nil {
			return 0, err
		}
		if swapParam.OrderType != ORDERTYPE_BUY && swapParam.OrderType != ORDERTYPE_SELL {
			return 0, fmt.Errorf("invalid order type %d", swapParam.OrderType)
		}
		if swapParam.AssetName != p.GetAssetName().String() {
			return 0, fmt.Errorf("invalid asset name %s", swapParam.AssetName)
		}

		if swapParam.UnitPrice == "" || swapParam.UnitPrice == "0" {
			return 0, fmt.Errorf("unit price should be set")
		}
		price, err := indexer.NewDecimalFromString(swapParam.UnitPrice, MAX_PRICE_DIVISIBILITY)
		if err != nil {
			return 0, fmt.Errorf("invalid unit price %s", swapParam.UnitPrice)
		}

		if templateName == TEMPLATE_CONTRACT_SWAP {
			// 必须设置amt
			if swapParam.Amt == "" || swapParam.Amt == "0" {
				return 0, fmt.Errorf("expected amt should be set")
			}
			amt, err := indexer.NewDecimalFromString(swapParam.Amt, p.Divisibility)
			if err != nil {
				return 0, fmt.Errorf("invalid amt %s", swapParam.Amt)
			}
			// 要买或者卖所需的聪还不足1聪
			value := indexer.DecimalMul(price, amt) // price精度更高，放前面
			if value.Int64() == 0 {
				return 0, fmt.Errorf("too small amt")
			}
			if swapParam.OrderType == ORDERTYPE_BUY {
				satsValue := value.Ceil()
				fee := calcSwapFee(satsValue)
				return fee, nil
			} else {
				// sell，暂时只收 SWAP_INVOKE_FEE，在调用时已经支付
			}

			return SWAP_INVOKE_FEE, nil // 卖单暂时不收费

		} else if templateName == TEMPLATE_CONTRACT_AMM {
			// 可以不设置amt
			if swapParam.Amt != "" && swapParam.Amt != "0" {
				_, err = indexer.NewDecimalFromString(swapParam.Amt, p.Divisibility)
				if err != nil {
					return 0, fmt.Errorf("invalid amt %s", swapParam.Amt)
				}
			}
			// unitprice要设置，含义不同
			// 如果是买单，是聪数量
			// 如果是卖单，是资产数量
			if swapParam.OrderType == ORDERTYPE_BUY {
				satsValue := price.Int64()
				fee := calcSwapFee(satsValue)
				return fee, nil
			} else {
				// 卖单, 如果交易结果不足以扣除服务费，返回失败
				// assetAmt := price
				// unitPrice := p.LastDealPrice
				// if unitPrice.Sign() == 0 {
				// 	unitPrice = indexer.DecimalDiv(indexer.NewDecimal(p.SatsValueInPool, p.Divisibility), p.AssetAmtInPool)
				// }
				// satsValue := indexer.DecimalMul(assetAmt, unitPrice)
				// if satsValue <
				return SWAP_INVOKE_FEE, nil // 卖单在交易成功后再收交易服务费
			}

		} else {
			return 0, fmt.Errorf("invalid template %s", templateName)
		}

	case INVOKE_API_REFUND:
		Log.Infof("refund reason %s", string(invoke.Param))

	case INVOKE_API_DEPOSIT:
		if templateName != TEMPLATE_CONTRACT_AMM && templateName != TEMPLATE_CONTRACT_TRANSCEND {
			return 0, fmt.Errorf("unsupport")
		}
		var innerParam DepositInvokeParam
		err := json.Unmarshal([]byte(invoke.Param), &innerParam)
		if err != nil {
			return 0, err
		}
		if innerParam.OrderType != ORDERTYPE_DEPOSIT {
			return 0, fmt.Errorf("invalid order type %d", innerParam.OrderType)
		}
		if innerParam.AssetName != assetName.String() {
			return 0, fmt.Errorf("invalid asset name %s", innerParam.AssetName)
		}

		_, err = indexer.NewDecimalFromString(innerParam.Amt, p.Divisibility)
		if err != nil {
			return 0, fmt.Errorf("invalid amt %s", innerParam.Amt)
		}

		return DEPOSIT_INVOKE_FEE, nil

	case INVOKE_API_WITHDRAW:
		if templateName != TEMPLATE_CONTRACT_AMM && templateName != TEMPLATE_CONTRACT_TRANSCEND {
			return 0, fmt.Errorf("unsupport")
		}
		var innerParam DepositInvokeParam
		err := json.Unmarshal([]byte(invoke.Param), &innerParam)
		if err != nil {
			return 0, err
		}
		if innerParam.OrderType != ORDERTYPE_WITHDRAW {
			return 0, fmt.Errorf("invalid order type %d", innerParam.OrderType)
		}
		if innerParam.AssetName != assetName.String() {
			return 0, fmt.Errorf("invalid asset name %s", innerParam.AssetName)
		}
		if innerParam.Amt == "" || innerParam.Amt == "0" {
			return 0, fmt.Errorf("invalid amt %s", innerParam.Amt)
		}
		amt, err := indexer.NewDecimalFromString(innerParam.Amt, p.Divisibility)
		if err != nil {
			return 0, fmt.Errorf("invalid amt %s", innerParam.Amt)
		}
		// 检查一层网络上是否有足够的资产，现在合约不保留一层资产，只需要确保二层资产有足够资产就行
		totalAmt := p.stp.GetAssetBalance(p.ChannelAddr, p.GetAssetName())
		// := indexer.DecimalSub(totalAmt, p.AssetAmtInPool)
		if amt.Cmp(totalAmt) > 0 {
			return 0, fmt.Errorf("no enough asset in amm pool L1, required %s but only %s",
				amt.String(), totalAmt.String())
		}

		if assetName.Protocol == indexer.PROTOCOL_NAME_ORDX {
			if amt.Int64()%int64(p.N) != 0 {
				return 0, fmt.Errorf("ordx asset should withdraw be times of %d", p.N)
			}
		}
		assetName := AssetName{
			AssetName: *p.GetAssetName(),
			N:         p.N,
		}
		fee := CalcFee_SendTx(2, 3, 1, &assetName, amt, p.stp.GetFeeRate(), true)
		return WITHDRAW_INVOKE_FEE + fee, nil

	case INVOKE_API_STAKE:
		if templateName != TEMPLATE_CONTRACT_AMM {
			return 0, fmt.Errorf("unsupport")
		}
		var innerParam StakeInvokeParam
		err := json.Unmarshal([]byte(invoke.Param), &innerParam)
		if err != nil {
			return 0, err
		}
		if innerParam.OrderType != ORDERTYPE_STAKE {
			return 0, fmt.Errorf("invalid order type %d", innerParam.OrderType)
		}
		if innerParam.AssetName != assetName.String() {
			return 0, fmt.Errorf("invalid asset name %s", innerParam.AssetName)
		}
		if innerParam.Amt == "" || innerParam.Amt == "0" {
			return 0, fmt.Errorf("invalid amt %s", innerParam.Amt)
		}
		amt, err := indexer.NewDecimalFromString(innerParam.Amt, p.Divisibility)
		if err != nil {
			return 0, fmt.Errorf("invalid amt %s", innerParam.Amt)
		}
		if innerParam.Value <= 0 {
			return 0, fmt.Errorf("invalid value %d", innerParam.Value)
		}
		// 保持相同比例
		var amtInPool *Decimal
		var valueInPool int64
		if p.SatsValueInPool == 0 {
			amm, ok := p.Contract.(*AmmContract)
			if !ok {
				return 0, fmt.Errorf("not AMM contract")
			}
			amtInPool, err = indexer.NewDecimalFromString(amm.AssetAmt, MAX_ASSET_DIVISIBILITY)
			if err != nil {
				return 0, err
			}
			valueInPool = amm.SatValue
		} else {
			amtInPool = p.AssetAmtInPool.Clone()
			valueInPool = p.SatsValueInPool
		}
		d1 := indexer.DecimalMul(amt, indexer.NewDecimal(valueInPool, p.Divisibility))
		d2 := indexer.DecimalMul(amtInPool, indexer.NewDecimal(innerParam.Value, p.Divisibility))
		if d1.Cmp(d2) != 0 {
			threshold, _ := indexer.NewDecimalFromString("0.010", 3)
			if indexer.DecimalSub(d1, d2).Abs().Cmp(indexer.DecimalMul(d2, threshold)) >= 0 {
				return 0, fmt.Errorf("stake asset should keep the same ratio with current pool: %s %d", innerParam.Amt, innerParam.Value)
			}
		}

		return INVOKE_FEE, nil

	case INVOKE_API_UNSTAKE:
		if templateName != TEMPLATE_CONTRACT_AMM {
			return 0, fmt.Errorf("unsupport")
		}
		var innerParam UnstakeInvokeParam
		err := json.Unmarshal([]byte(invoke.Param), &innerParam)
		if err != nil {
			return 0, err
		}
		if innerParam.OrderType != ORDERTYPE_UNSTAKE {
			return 0, fmt.Errorf("invalid order type %d", innerParam.OrderType)
		}
		if innerParam.AssetName != assetName.String() {
			return 0, fmt.Errorf("invalid asset name %s", innerParam.AssetName)
		}
		if innerParam.Amt == "" || innerParam.Amt == "0" {
			return 0, fmt.Errorf("invalid amt %s", innerParam.Amt)
		}
		amt, err := indexer.NewDecimalFromString(innerParam.Amt, p.Divisibility)
		if err != nil {
			return 0, fmt.Errorf("invalid amt %s", innerParam.Amt)
		}
		if innerParam.Value <= 0 {
			return 0, fmt.Errorf("invalid value %d", innerParam.Value)
		}

		// 保持相同比例
		var amtInPool *Decimal
		var valueInPool int64
		if p.SatsValueInPool == 0 {
			amm, ok := p.Contract.(*AmmContract)
			if !ok {
				return 0, fmt.Errorf("not AMM contract")
			}
			amtInPool, err = indexer.NewDecimalFromString(amm.AssetAmt, MAX_ASSET_DIVISIBILITY)
			if err != nil {
				return 0, err
			}
			valueInPool = amm.SatValue
		} else {
			amtInPool = p.AssetAmtInPool.Clone()
			valueInPool = p.SatsValueInPool
		}
		d1 := indexer.DecimalMul(amt, indexer.NewDecimal(valueInPool, p.Divisibility))
		d2 := indexer.DecimalMul(amtInPool, indexer.NewDecimal(innerParam.Value, p.Divisibility))
		if d1.Cmp(d2) != 0 {
			threshold, _ := indexer.NewDecimalFromString("0.010", 3)
			if indexer.DecimalSub(d1, d2).Abs().Cmp(indexer.DecimalMul(d2, threshold)) >= 0 {
				return 0, fmt.Errorf("stake asset should keep the same ratio with current pool: %s %d", innerParam.Amt, innerParam.Value)
			}
		}

		// 检查invoker是否有足够的资产 (TODO 这个接口无法知道inoker，无法检查)
		if amt.Cmp(p.AssetAmtInPool) > 0 {
			return 0, fmt.Errorf("no enough asset in pool, required %s but only %s",
				amt.String(), p.AssetAmtInPool.String())
		}

		if assetName.Protocol == indexer.PROTOCOL_NAME_ORDX {
			if amt.Int64()%int64(p.N) != 0 {
				return 0, fmt.Errorf("ordx asset should withdraw be times of %d", p.N)
			}
		}

		if innerParam.ToL1 {
			assetName := AssetName{
				AssetName: *p.GetAssetName(),
				N:         p.N,
			}
			fee := CalcFee_SendTx(2, 3, 1, &assetName, amt, p.stp.GetFeeRate(), true)
			return WITHDRAW_INVOKE_FEE + fee, nil
		}
		return INVOKE_FEE, nil

	default:
		return 0, fmt.Errorf("unsupport action %s", invoke.Action)
	}

	return SWAP_INVOKE_FEE, nil
}

func calcSwapFee(value int64) int64 {
	return SWAP_INVOKE_FEE + calcSwapServiceFee(value)
}

// 不包括调用费用 （向下取整）
func calcSwapServiceFee(value int64) int64 {
	return (value * SWAP_SERVICE_FEE_RATIO) / 1000 // 交易服务费
}
