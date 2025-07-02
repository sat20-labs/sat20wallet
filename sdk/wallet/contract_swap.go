package wallet

import (
	"bytes"
	
	"encoding/gob"
	"encoding/json"
	"fmt"

	"sync"
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
*/

func init() {
	gob.Register(&SwapContractRuntime{})
}

const (
	INVOKE_API_SWAP     string = "swap"
	INVOKE_API_REFUND   string = "refund"
	INVOKE_API_FUND     string = "fund"     //
	INVOKE_API_DEPOSIT  string = "deposit"  // L1->L2  免费
	INVOKE_API_WITHDRAW string = "withdraw" // L2->L1  收

	ORDERTYPE_SELL     = 1
	ORDERTYPE_BUY      = 2
	ORDERTYPE_REFUND   = 3
	ORDERTYPE_FUND     = 4
	ORDERTYPE_PROFIT   = 5
	ORDERTYPE_DEPOSIT  = 6
	ORDERTYPE_WITHDRAW = 7
	ORDERTYPE_UNUSED   = 8

	SWAP_INVOKE_FEE     int64 = 10
	DEPOSIT_INVOKE_FEE  int64 = DEFAULT_SERVICE_FEE_DEPOSIT
	WITHDRAW_INVOKE_FEE int64 = DEFAULT_SERVICE_FEE_WITHDRAW

	MAX_PRICE_DIVISIBILITY = 10

	MAX_BLOCK_BUFFER = 100
	BUCK_SIZE        = 100

	SWAP_SERVICE_FEE_RATIO = 8 // 千分之
	DEPTH_SLOT             = 10
)

const (
	DONE_NOTYET          = 0
	DONE_DEALT           = 1
	DONE_REFUNDED        = 2
	DONE_CLOSED_DIRECTLY = 3
)


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
	return DEFAULT_SERVICE_FEE_DEPLOY_CONTRACT + SWAP_INVOKE_FEE // deployTx 的utxo含聪量
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

type SwapHistoryItem struct {
	InvokeHistoryItemBase

	OrderType      int    //
	UtxoId         uint64 // 其实是utxoId
	OrderTime      int64
	AssetName      string
	UnitPrice      *Decimal // X per Y
	ExpectedAmt    *Decimal // 期望的数量
	Address        string   // 所有人
	FromL1         bool     // 是否主网的调用，默认是false
	InUtxo         string   // sell or buy 的utxo
	InValue        int64    // 白聪，不包括资产聪
	InAmt          *Decimal
	RemainingAmt   *Decimal // 要买或者卖的资产的剩余数量
	RemainingValue int64    // 用来买资产的聪的剩余数量
	ToL1           bool     // 是否主网的调用，默认是false
	OutTxId        string   // 回款的TxId，可能是成交后汇款，也可能是撤销后的回款
	OutAmt         *Decimal // 买到的资产
	OutValue       int64    // 卖出得到的聪，扣除服务费
}

func (p *SwapHistoryItem) ToNewVersion() InvokeHistoryItem {
	return p
}

// 买单用来购买的聪数量
func (p *SwapHistoryItem) GetTradingValue() int64 {
	return indexer.DecimalMul(p.UnitPrice, p.ExpectedAmt).Ceil()
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

	TotalInputAssets *Decimal // 所有进入池子交易的资产数量，包括历史记录 （不包括路过的）
	TotalInputSats   int64    // 所有进入池子交易的聪数量，包括历史记录 （不包括路过的）

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
	TotalDepositTx     int
	TotalDepositTxFee  int64

	TotalWithdrawAssets *Decimal
	TotalWithdrawTx     int
	TotalWithdrawTxFee  int64
}

func (p *SwapContractRunningData) ToNewVersion() *SwapContractRunningData {
	return p
}

type SwapContractRuntimeInDB struct {
	Contract
	ContractRuntimeBase

	SwapContractRunningData
}

type TraderStatistic struct {
	OnSaleAmt    *Decimal
	OnBuyValue   int64
	InvokeCount  int
	DealAmt      *Decimal // 只累加卖单中成交的资产数量
	DealValue    int64    // 只累加买单中成交的聪数量
	RefundAmt    *Decimal
	RefundValue  int64
	FundingAmt   *Decimal
	FundingValue int64
	DepositAmt   *Decimal
	WithdrawAmt  *Decimal
	ProfitAmt    *Decimal
	ProfitValue  int64
}

type TraderStatus struct {
	Address         string
	Statistic       TraderStatistic
	Refund          bool            // 是否准备退款
	Profit          bool            // 是否准备提取利润
	SwapUtxoMap     map[string]bool // utxo map 交易中的记录
	RefundUtxoMap   map[string]bool // utxo map 要退款的记录，包括指令utxo和要退款的utxo
	ProfitUtxoMap   map[string]bool // utxo map
	DepositUtxoMap  map[string]bool // utxo map
	WithdrawUtxoMap map[string]bool // utxo map
	FundingUtxoMap  map[string]bool // utxo map
	InvalidUtxoMap  map[string]bool // (废弃不用，合并到RefundUtxoMap)
	History         map[int][]int64 // 用户的invoke历史记录，每100个为一桶，用InvokeCount计算 TODO 目前统一一块存储，数据量大了后要分桶保存，用到才加载
	UpdateTime      int64
}

func NewTraderStatus(address string, divisibility int) *TraderStatus {
	return &TraderStatus{
		Address: address,
		Statistic: TraderStatistic{
			OnSaleAmt:   indexer.NewDecimal(0, divisibility),
			DealAmt:     indexer.NewDecimal(0, divisibility),
			RefundAmt:   indexer.NewDecimal(0, divisibility),
			DepositAmt:  indexer.NewDecimal(0, divisibility),
			WithdrawAmt: indexer.NewDecimal(0, divisibility),
			ProfitAmt:   indexer.NewDecimal(0, divisibility),
		},
		SwapUtxoMap:     make(map[string]bool),
		RefundUtxoMap:   make(map[string]bool),
		ProfitUtxoMap:   make(map[string]bool),
		DepositUtxoMap:  make(map[string]bool),
		WithdrawUtxoMap: make(map[string]bool),
		FundingUtxoMap:  make(map[string]bool),
		InvalidUtxoMap:  make(map[string]bool),
		History:         make(map[int][]int64),
	}
}

type SwapContractRuntime struct {
	SwapContractRuntimeInDB

	swapHistory   map[string]*SwapHistoryItem           // key:utxo 单独记录数据库，区块缓存, 6个区块以后，并且已经成交的可以删除
	buyPool       []*SwapHistoryItem                    // 还没有成交的记录，按价格从大到小排序
	sellPool      []*SwapHistoryItem                    // 还没有成交的记录，按价格从小到大排序
	traderInfoMap map[string]*TraderStatus              // user address -> utxo list 还没有成交的记录
	refundMap     map[string]map[int64]*SwapHistoryItem // 准备退款的账户, address -> refund invoke item list, 无效的item也放进来，一起退款
	depositMap    map[string]map[int64]*SwapHistoryItem // 准备deposit的账户, address -> deposit invoke item list
	withdrawMap   map[string]map[int64]*SwapHistoryItem // 准备withdraw的账户, address -> withdraw invoke item list
	isSending     bool

	refreshTime_swap   int64
	responseCache_swap []*responseItem_swap
	responseStatus     *responseStatus_swap
	responseHistory    map[int][]*SwapHistoryItem // 按照100个为一桶，根据区块顺序记录，跟swapHistory保持一致
	responseAnalytics  *AnalytcisData
	dealPrice          *Decimal

	mutex sync.RWMutex
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
	p.swapHistory = make(map[string]*SwapHistoryItem)
	p.buyPool = make([]*SwapHistoryItem, 0)
	p.sellPool = make([]*SwapHistoryItem, 0)
	p.traderInfoMap = make(map[string]*TraderStatus)
	p.refundMap = make(map[string]map[int64]*SwapHistoryItem)
	p.depositMap = make(map[string]map[int64]*SwapHistoryItem)
	p.withdrawMap = make(map[string]map[int64]*SwapHistoryItem)
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
	Amount *Decimal `json:"amount" swaggertype:"string"`
	Volume int64    `json:"volume"` // 指定时间段内的交易额
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

		// 检查二层网络上是否有足够的资产：对应一个anchor交易，就可以解决这个问题
		// totalAmt := p.stp.GetAssetBalance_SatsNet(p.ChannelId, p.GetAssetName())
		// if totalAmt.Cmp(indexer.DecimalAdd(amt, p.AssetAmtInPool)) < 0 {
		// 	return 0, fmt.Errorf("no enough asset in amm pool L2, required %s but only %s (%s-%s)",
		// 		amt.String(), indexer.DecimalSub(totalAmt, p.AssetAmtInPool).String(), totalAmt.String(), p.AssetAmtInPool.String())
		// }

		return DEPOSIT_INVOKE_FEE, nil

	case INVOKE_API_WITHDRAW:
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
		// 检查一层网络上是否有足够的资产
		totalAmt := p.stp.GetAssetBalance(p.ChannelId, p.GetAssetName())
		if totalAmt.Cmp(indexer.DecimalAdd(amt, p.AssetAmtInPool)) < 0 {
			return 0, fmt.Errorf("no enough asset in amm pool L1, required %s but only %s-%s",
				amt.String(), totalAmt.String(), p.AssetAmtInPool.String())
		}

		if assetName.Protocol == indexer.PROTOCOL_NAME_ORDX {
			// if indexer.GetBindingSatNum(amt, uint32(p.N)) < 330 {
			// 	return fmt.Errorf("ordx assset should withdraw at least %d, but only %d", 330 * p.N, amt.Int64())
			// }
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
	return (value*SWAP_SERVICE_FEE_RATIO) / 1000 // 交易服务费
}
