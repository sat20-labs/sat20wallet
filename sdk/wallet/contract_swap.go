package wallet

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
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
	// 让 gob 知道旧的类型对应新的实现
	gob.RegisterName("*stp.SwapContractRuntime", new(SwapContractRuntime))

	if IsTestNet() {
		_valueLimit = 50
		_addressLimit = 1
	}
}

const ADDR_OPRETURN string = "op_return"

var _asset_fee_ratio, _ = indexer.NewDecimalFromString("0.985", MAX_PRICE_DIVISIBILITY)

var _valueLimit = int64(1000)
var _addressLimit = 10

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
	if action != INVOKE_API_SWAP {
		return ""
	}
	
	var param InvokeParam
	param.Action = action
	innerParam := GetInvokeInnerParam(action)
	if innerParam != nil {
		buf, err := json.Marshal(&innerParam)
		if err != nil {
			return ""
		}
		param.Param = string(buf)
	}

	result, err := json.Marshal(&param)
	if err != nil {
		return ""
	}
	return string(result)
}

func (p *SwapContract) CalcStaticMerkleRoot() []byte {
	return CalcContractStaticMerkleRoot(p)
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
	UnitPrice string `json:"unitPrice"` // 资产价格，默认是聪的对价；
	// 如果是AMM合约：（Amt参数是期望买/卖的最小值）
	// 	1. 如果是买单，声明utxo带的聪数量，以这些聪购买至少Amt数量的资产
	//  2. 如果是卖单，声明utxo带的资产数量
}

func (p *SwapInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
		AddData([]byte(p.AssetName)).
		AddData([]byte(p.Amt)).
		AddData([]byte(p.UnitPrice)).Script()
}

func (p *SwapInvokeParam) EncodeV2() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
		AddData([]byte("")).
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

type SwapContractRunningData_old = SwapContractRunningData

// type SwapContractRunningData_old struct {
// 	AssetAmtInPool   *Decimal // 池子中资产的数量
// 	SatsValueInPool  int64    // 池子中聪的数量
// 	LowestSellPrice  *Decimal
// 	HighestBuyPrice  *Decimal
// 	LastDealPrice    *Decimal
// 	HighestDealPrice *Decimal
// 	LowestDealPrice  *Decimal

// 	TotalInputAssets *Decimal // 所有进入池子交易的资产数量，包括历史记录 （不包括路过的）
// 	TotalInputSats   int64    // 所有进入池子交易的聪数量，包括历史记录 （不包括路过的）

// 	TotalDealAssets *Decimal // 已经卖出的资产数量
// 	TotalDealSats   int64    // 已经买入资产的聪的数量
// 	TotalDealCount  int      // 总成交次数
// 	TotalDealTx     int      //
// 	TotalDealTxFee  int64

// 	TotalRefundAssets *Decimal // 对同一个挂单来说，不包括已经成交部分
// 	TotalRefundSats   int64    // 对同一个挂单来说，不包括已经成交部分
// 	TotalRefundTx     int
// 	TotalRefundTxFee  int64

// 	// 项目方提取利润的交易在这里记录
// 	TotalProfitAssets *Decimal //
// 	TotalProfitSats   int64    //
// 	TotalProfitTx     int
// 	TotalProfitTxFee  int64

// 	TotalDepositAssets *Decimal
// 	TotalDepositSats   int64
// 	TotalDepositTx     int
// 	TotalDepositTxFee  int64

// 	TotalWithdrawAssets *Decimal
// 	TotalWithdrawSats   int64
// 	TotalWithdrawTx     int
// 	TotalWithdrawTxFee  int64
// }

// func (s *SwapContractRunningData_old) ToNewVersion() *SwapContractRunningData {
//     return &SwapContractRunningData{
//         AssetAmtInPool:     s.AssetAmtInPool,
//         SatsValueInPool:    s.SatsValueInPool,
//         LowestSellPrice:    s.LowestSellPrice,
//         HighestBuyPrice:    s.HighestBuyPrice,
//         LastDealPrice:      s.LastDealPrice,
//         HighestDealPrice:   s.HighestDealPrice,
//         LowestDealPrice:    s.LowestDealPrice,

//         TotalInputAssets:   s.TotalInputAssets,
//         TotalInputSats:     s.TotalInputSats,

//         TotalDealAssets:    s.TotalDealAssets,
//         TotalDealSats:      s.TotalDealSats,
//         TotalDealCount:     s.TotalDealCount,
//         TotalDealTx:        s.TotalDealTx,
//         TotalDealTxFee:     s.TotalDealTxFee,

//         TotalRefundAssets:  s.TotalRefundAssets,
//         TotalRefundSats:    s.TotalRefundSats,
//         TotalRefundTx:      s.TotalRefundTx,
//         TotalRefundTxFee:   s.TotalRefundTxFee,

// 		TotalProfitAssets:  s.TotalProfitAssets,
// 		TotalProfitSats:     s.TotalProfitSats,
// 		TotalProfitTx:       s.TotalProfitTx,
// 		TotalProfitTxFee:    s.TotalProfitTxFee,

//         TotalDepositAssets:  s.TotalDepositAssets,
// 		TotalDepositSats:    s.TotalDepositSats,
//         TotalDepositTx:      s.TotalDepositTx,
//         TotalDepositTxFee:   s.TotalDepositTxFee,

//         TotalWithdrawAssets: s.TotalWithdrawAssets,
// 		TotalWithdrawSats:   s.TotalWithdrawSats,
//         TotalWithdrawTx:     s.TotalWithdrawTx,
//         TotalWithdrawTxFee:  s.TotalWithdrawTxFee,

// 		TotalStakeAssets:    nil,
// 		TotalStakeSats:      0,
// 		TotalUnstakeAssets:  nil,
// 		TotalUnstakeSats:    0,
// 		TotalUnstakeTx:      0,
// 		TotalUnstakeTxFee:   0,
//     }
// }

type SwapContractRunningData struct {
	AssetAmtInPool  *Decimal // 池子中资产的数量
	SatsValueInPool int64    // 池子中聪的数量

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

	TotalStakeAssets *Decimal //
	TotalStakeSats   int64    //

	TotalUnstakeAssets *Decimal
	TotalUnstakeSats   int64
	TotalUnstakeTx     int
	TotalUnstakeTxFee  int64

	TotalOutputAssets *Decimal
	TotalOutputSats   int64

	// 新增加
	TotalRetrieveAssets *Decimal // 移除流动性相关统计
	TotalRetrieveSats   int64
	TotalRetrieveTx     int
	TotalRetrieveTxFee  int64

	BaseLptAmt         *Decimal // 由launchpool建立的池子，有一个底池份额
	TotalLptAmt        *Decimal // 现有的LPToken数量
	TotalAddedLptAmt   *Decimal // 增加流动性时铸造的LPToken数量
	TotalRemovedLptAmt *Decimal // 移除流动性时销毁的LPToken数量
	TotalFeeLptAmt     *Decimal // 在用户removeLiq时，归属市场和基金会的利润转换为lpt，累积在这里
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
	LptAmt        *Decimal
	RetrieveAmt   *Decimal // 加池子剩余资产
	RetrieveValue int64    // 加池子剩余资产
	LiqSatsValue  int64
}

const (
	SETTLE_STATE_NORMAL             int = 0
	SETTLE_STATE_REMOVING_LIQ_READY int = 1
)

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

	SwapUtxoMap   map[string]bool // 废弃
	ProfitUtxoMap map[string]bool // 废弃
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
	}
}

// 新版本，version=1
type TraderStatus struct {
	TraderStatusV0

	// 新增加
	StakeAmt      *Decimal //
	StakeValue    int64    // 质押资产的比例
	UnStakeAmt    *Decimal
	UnStakeValue  int64
	LptAmt        *Decimal // 持有的LptToken数量
	SettleState   int      // 0: 正常运行； 1: 准备清退，可以发起退款交易；退款交易完成后，回到0
	RetrieveAmt   *Decimal // 加池子剩余资产
	RetrieveValue int64    // 加池子剩余资产
	LiqSatsValue  int64    // LptAmt 对应的sats的数量，也就是用户的成本，按照addLiq时的对价转换为satsValue
	// 在removeLiq时，按照remove的LptAmt的比例，同时减去对应的LiqSatsValue
}

func NewTraderStatus(address string, divisibility int) *TraderStatus {
	s := &TraderStatus{
		TraderStatusV0: *NewTraderStatusV0(address, divisibility),
	}
	s.Version = 1
	return s
}

func (p *TraderStatus) Statistic() *TraderStatistic {
	return &TraderStatistic{
		InvokeCount:   p.InvokeCount,
		OnSaleAmt:     p.OnSaleAmt.Clone(),
		OnBuyValue:    p.OnBuyValue,
		DealAmt:       p.DealAmt.Clone(),
		DealValue:     p.DealValue,
		RefundAmt:     p.RefundAmt.Clone(),
		RefundValue:   p.RefundValue,
		DepositAmt:    p.DepositAmt.Clone(),
		DepositValue:  p.DepositValue,
		WithdrawAmt:   p.WithdrawAmt.Clone(),
		WithdrawValue: p.WithdrawValue,
		ProfitAmt:     p.ProfitAmt.Clone(),
		ProfitValue:   p.ProfitValue,
		StakeAmt:      p.StakeAmt.Clone(),
		StakeValue:    p.StakeValue,
		UnstakeAmt:    p.UnStakeAmt.Clone(),
		UnstakeValue:  p.UnStakeValue,
		LptAmt:        p.LptAmt.Clone(),
		RetrieveAmt:   p.RetrieveAmt.Clone(),
		RetrieveValue: p.RetrieveValue,
		LiqSatsValue:  p.LiqSatsValue,
	}
}

type SwapContractRuntime struct {
	SwapContractRuntimeInDB

	buyPool            []*SwapHistoryItem                    // 还没有成交的记录，按价格从大到小排序
	sellPool           []*SwapHistoryItem                    // 还没有成交的记录，按价格从小到大排序
	traderInfoMap      map[string]*TraderStatus              // user address -> utxo list 还没有成交的记录
	swapMap            map[string]map[int64]*SwapHistoryItem // 还在交易中的账户, address -> refund invoke item list,
	refundMap          map[string]map[int64]*SwapHistoryItem // 准备退款的账户, address -> refund invoke item list, 无效的item也放进来，一起退款
	depositMap         map[string]map[int64]*SwapHistoryItem // 准备deposit的账户, address -> deposit invoke item list
	withdrawMap        map[string]map[int64]*SwapHistoryItem // 准备withdraw的账户, address -> withdraw invoke item list
	addLiquidityMap    map[string]map[int64]*SwapHistoryItem // 准备添加流动性的账户, address -> addliq invoke item list
	removeLiquidityMap map[string]map[int64]*SwapHistoryItem // 准备移除流动性的账户, address -> removeliq invoke item list
	stakeMap           map[string]map[int64]*SwapHistoryItem // 准备质押的账户, address -> stake invoke item list
	unstakeMap         map[string]map[int64]*SwapHistoryItem // 准备取消质押的账户, address -> unstake invoke item list
	profitMap          map[string]map[int64]*SwapHistoryItem // 准备提取利润的账户, address -> profit invoke item list
	stubFeeMap         map[int64]int64                       // invokeCount->fee
	isSending          bool

	refreshTime_swap   int64
	responseCache_swap []*responseItem_swap
	responseStatus     *responseStatus_swap
	responseHistory    map[int][]*SwapHistoryItem // 按照100个为一桶，根据区块顺序记录，跟swapHistory保持一致
	responseAnalytics  *AnalytcisData
	dealPrice          *Decimal
}

func NewSwapContractRuntime(stp ContractManager) *SwapContractRuntime {
	p := &SwapContractRuntime{
		SwapContractRuntimeInDB: SwapContractRuntimeInDB{
			Contract: NewSwapContract(),
			ContractRuntimeBase: *NewContractRuntimeBase(stp),
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
	p.swapMap = make(map[string]map[int64]*SwapHistoryItem)
	p.refundMap = make(map[string]map[int64]*SwapHistoryItem)
	p.depositMap = make(map[string]map[int64]*SwapHistoryItem)
	p.withdrawMap = make(map[string]map[int64]*SwapHistoryItem)
	p.addLiquidityMap = make(map[string]map[int64]*SwapHistoryItem)
	p.removeLiquidityMap = make(map[string]map[int64]*SwapHistoryItem)
	p.stakeMap = make(map[string]map[int64]*SwapHistoryItem)
	p.unstakeMap = make(map[string]map[int64]*SwapHistoryItem)
	p.profitMap = make(map[string]map[int64]*SwapHistoryItem)
	p.stubFeeMap = make(map[int64]int64)
	p.responseHistory = make(map[int][]*SwapHistoryItem)

}

func (p *SwapContractRuntime) InitFromJson(content []byte, stp ContractManager) error {
	err := json.Unmarshal(content, p)
	if err != nil {
		return err
	}
	p.init()

	return nil
}

func (p *SwapContractRuntime) InitFromDB(stp ContractManager, resv ContractDeployResvIF) error {

	err := p.ContractRuntimeBase.InitFromDB(stp, resv)
	if err != nil {
		Log.Errorf("SwapContractRuntime.InitFromDB failed, %v", err)
		return err
	}
	p.init()

	p.rebuildTraderHistory()

	history := LoadContractInvokeHistory(stp.GetDB(), p.URL(), true, false)
	for _, v := range history {
		item, ok := v.(*SwapHistoryItem)
		if !ok {
			continue
		}

		//p.loadTraderInfo(item.Address)
		// // 将无效的item设置为退款
		// if item.OrderType == 7 && item.AssetName == "::" && item.RemainingValue < 330 {
		// 	item.RemainingValue += item.ServiceFee - INVOKE_FEE
		// 	item.ServiceFee = 0
		// 	item.Reason = INVOKE_REASON_INVALID
		// 	p.addRefundItem(item, false)
		// 	delete(trader.WithdrawUtxoMap, item.InUtxo)
		// 	saveContractInvokerStatus(stp.db, url, trader)
		// 	saveContractInvokeHistoryItem(stp.db, url, item)
		// 	Log.Infof("item fixed: %v", item)
		// }

		p.loadTraderInfo(item.Address)
		p.addItem(item)
		p.history[item.InUtxo] = item
	}

	// p.calcAssetMerkleRoot()
	// saveReservation(p.stp.db, resv)

	// if p.GetTemplateName() == TEMPLATE_CONTRACT_SWAP {
	// 	err = p.checkSelf()
	// 	if err != nil {
	// 		Log.Errorf("%s checkSelf failed, %v", p.URL(), err)
	// 	}
	// }

	return nil
}

func (p *SwapContractRuntime) rebuildTraderHistory() {

	if p.stp.NeedRebuildTraderHistory() {
		url := p.URL()
		history := LoadContractInvokeHistory(p.stp.GetDB(), url, false, false)
		if len(history) == 0 {
			return
		}
		mid1 := make([]*SwapHistoryItem, 0)
		for _, v := range history {
			item, ok := v.(*SwapHistoryItem)
			if !ok {
				continue
			}
			mid1 = append(mid1, item)
		}

		sort.Slice(mid1, func(i, j int) bool {
			return mid1[i].Id < mid1[j].Id
		})
		traderMap := make(map[string]*TraderStatus)
		for _, item := range mid1 {
			trader, ok := traderMap[item.Address]
			if !ok {
				trader = NewTraderStatus(item.Address, p.Divisibility)
				traderMap[item.Address] = trader
			}
			insertItemToTraderHistroy(&trader.InvokerStatusBase, item)
		}
		for k, v := range traderMap {
			trader := p.loadTraderInfo(k)
			if trader.InvokeCount != v.InvokeCount {
				trader.InvokeCount = v.InvokeCount
				trader.History = v.History
				trader.UpdateTime = time.Now().Unix()
				saveContractInvokerStatus(p.stp.GetDB(), url, trader)
			}
		}
	}

}

// 调用前自己加锁
func (p *SwapContractRuntime) CalcRuntimeMerkleRoot() []byte {
	//Log.Debugf("Invoke: %d", p.InvokeCount)
	base := CalcContractRuntimeBaseMerkleRoot(&p.ContractRuntimeBase)
	running := CalcSwapContractRunningDataMerkleRoot(&p.SwapContractRunningData)

	buf := append(base, running...)
	hash := chainhash.DoubleHashH(buf)
	Log.Debugf("%s CalcRuntimeMerkleRoot: %d %s", p.stp.GetMode(), p.InvokeCount, hex.EncodeToString(hash.CloneBytes()))
	return hash.CloneBytes()
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

func (p *SwapContractRuntime) GetAssetAmount() (*Decimal, int64) {
	return p.AssetAmtInPool, p.SatsValueInPool
}

func (p *SwapContractRuntime) RuntimeContent() []byte {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

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

func getPriceSpan(pool []*SwapHistoryItem, buy bool) (*Decimal, *Decimal, *Decimal, int) {
	var min, max, minSpan *Decimal
	if len(pool) == 0 {
		return nil, nil, nil, 0
	}
	min = pool[0].UnitPrice
	max = min
	minSpan = indexer.NewDecimal(1, max.Precision)
	priceMap := make(map[string]int) // Decimal 不能作为map的key
	var priceArea *Decimal
	var totalAmt *Decimal
	if buy {
		amt := indexer.NewDefaultDecimal(pool[0].RemainingValue)
		totalAmt = totalAmt.Add(amt)
		priceArea = priceArea.Add(indexer.DecimalMul(pool[0].UnitPrice, amt))
	} else {
		priceArea = priceArea.Add(indexer.DecimalMul(pool[0].UnitPrice, pool[0].RemainingAmt))
		totalAmt = totalAmt.Add(pool[0].RemainingAmt)
	}
	priceMap[min.String()] = 1
	for i := 1; i < len(pool); i++ {
		pre := pool[i-1]
		item := pool[i]
		var span *Decimal
		if item.UnitPrice.Cmp(pre.UnitPrice) != 0 {
			if buy {
				amt := indexer.NewDefaultDecimal(item.RemainingValue)
				totalAmt = totalAmt.Add(amt)
				priceArea = priceArea.Add(indexer.DecimalMul(item.UnitPrice, amt))
				span = indexer.DecimalSub(pre.UnitPrice, item.UnitPrice).Abs()
			} else {
				priceArea = priceArea.Add(indexer.DecimalMul(item.UnitPrice, item.RemainingAmt))
				totalAmt = totalAmt.Add(item.RemainingAmt)
				span = indexer.DecimalSub(item.UnitPrice, pre.UnitPrice).Abs()
			}
			if span.Cmp(minSpan) < 0 {
				minSpan = span
			}
		}
		priceMap[item.UnitPrice.String()] = priceMap[item.UnitPrice.String()] + 1

		if min.Cmp(item.UnitPrice) > 0 {
			min = item.UnitPrice
		}
		if max.Cmp(item.UnitPrice) < 0 {
			max = item.UnitPrice
		}
	}

	middlePrice := priceArea.Div(totalAmt)
	Log.Infof("min %s, max %s, middle %s", min.String(), max.String(), middlePrice.String())

	return min, max, minSpan, len(priceMap)
}

func (p *SwapContractRuntime) calcDepth(pool []*SwapHistoryItem, dealPrice *Decimal, buy bool, span *Decimal) []*DepthInfo {

	type depth struct {
		price *Decimal
		amt   *Decimal
		value int64
	}

	if dealPrice == nil {
		return nil
	}

	m := make([]*depth, DEPTH_SLOT)
	for i := range m {
		m[i] = &depth{}
		if buy {
			m[i].price = indexer.DecimalSub(dealPrice, indexer.DecimalMul(span, indexer.NewDefaultDecimal(int64(i))))
			if m[i].price.Sign() < 0 {
				m[i].price.SetValue(0)
			}
		} else {
			m[i].price = indexer.DecimalAdd(dealPrice, indexer.DecimalMul(span, indexer.NewDefaultDecimal(int64(i))))
		}
	}

	for _, item := range pool {
		if buy {
			distance := indexer.DecimalSub(dealPrice, item.UnitPrice).Abs()
			slot := indexer.DecimalDiv(distance, span).Int64()
			if slot >= DEPTH_SLOT {
				continue
			}
			d := m[slot]
			d.value += item.RemainingValue
			//amt := indexer.DecimalDiv(indexer.NewDecimal(item.RemainingValue, p.Divisibility), item.UnitPrice)
			amt := indexer.DecimalSub(item.ExpectedAmt, item.OutAmt)
			d.amt = d.amt.Add(amt)
		} else {
			distance := indexer.DecimalSub(item.UnitPrice, dealPrice).Abs()
			slot := indexer.DecimalDiv(distance, span).Int64()
			if slot >= DEPTH_SLOT {
				continue
			}
			d := m[slot]
			d.amt = indexer.DecimalAdd(d.amt, item.RemainingAmt)
			d.value += indexer.DecimalMul(item.RemainingAmt, item.UnitPrice).Int64()
		}
	}

	result := make([]*DepthInfo, DEPTH_SLOT)
	for i, d := range m {
		result[i] = &DepthInfo{
			Price: d.price.String(),
			Amt:   d.amt.String(),
			Value: d.value,
		}
	}

	return result
}

// 小量数据，直接按价格放
func (p *SwapContractRuntime) calcDepthV2(pool []*SwapHistoryItem, buy bool) []*DepthInfo {

	type depth struct {
		price *Decimal
		amt   *Decimal
		value int64
	}

	m := make(map[string]*depth)
	for _, item := range pool {
		d, ok := m[item.UnitPrice.String()]
		if !ok {
			d = &depth{
				price: item.UnitPrice,
			}
			m[item.UnitPrice.String()] = d
		}
		if buy {
			d.value += item.RemainingValue
			amt := indexer.DecimalSub(item.ExpectedAmt, item.OutAmt)
			d.amt = d.amt.Add(amt)

		} else {
			d.amt = indexer.DecimalAdd(d.amt, item.RemainingAmt)
			d.value += indexer.DecimalMul(item.RemainingAmt, item.UnitPrice).Int64()
		}
	}

	m2 := make([]*depth, 0)
	for _, v := range m {
		m2 = append(m2, v)
	}

	if buy {
		sort.Slice(m2, func(i, j int) bool {
			return m2[i].price.Cmp(m2[j].price) > 0
		})
	} else {
		sort.Slice(m2, func(i, j int) bool {
			return m2[i].price.Cmp(m2[j].price) < 0
		})
	}

	result := make([]*DepthInfo, len(m2))
	for i, d := range m2 {
		result[i] = &DepthInfo{
			Price: d.price.String(),
			Amt:   d.amt.String(),
			Value: d.value,
		}
	}

	return result
}

func (p *SwapContractRuntime) updateResponseData() {
	if p.refreshTime_swap == 0 {

		p.mutex.Lock()
		defer p.mutex.Unlock()

		//////////////////////////////
		// analytics
		p.responseAnalytics = p.genAnalyticsData()

		//////////////////////////////
		// 整体状态
		// 默认十档，超出部分不显示
		// 默认1聪为一个档位，从成交价上下统计
		// 如果挂单太少，直接按挂单显示
		var sellDepth, buyDepth []*DepthInfo

		minSellPrice, _, sellSpan, sellCount := getPriceSpan(p.sellPool, false)
		if sellCount > 0 && sellCount <= DEPTH_SLOT {
			sellDepth = p.calcDepthV2(p.sellPool, false)
		} else {
			sellDepth = p.calcDepth(p.sellPool, minSellPrice.Clone(), false, sellSpan)
		}

		_, maxBuyPrice, buySpan, buyCount := getPriceSpan(p.buyPool, true)
		if buyCount > 0 && buyCount <= DEPTH_SLOT {
			buyDepth = p.calcDepthV2(p.buyPool, true)
		} else {
			buyDepth = p.calcDepth(p.buyPool, maxBuyPrice.Clone(), true, buySpan)
		}

		p.responseStatus = &responseStatus_swap{
			SwapContractRuntimeInDB: &p.SwapContractRuntimeInDB,
			DealPrice:               minSellPrice.String(),
			BuyDepth:                buyDepth,
			SellDepth:               sellDepth,
		}

		data24H := &ItemData{}
		// 24H 统计
		for _, h := range p.responseAnalytics.Items24Hours {
			if h != nil {
				data24H.AssetAmt = data24H.AssetAmt.Add(h.AssetAmt)
				data24H.OrderCount += h.OrderCount
				data24H.SatsValue += h.SatsValue
			}
		}
		if data24H.AssetAmt.Sign() == 0 {
			data24H.AvgPrice = nil
		} else {
			data24H.AvgPrice = indexer.NewDecimal(data24H.SatsValue, MAX_PRICE_DIVISIBILITY).Div(data24H.AssetAmt)
		}
		p.responseStatus.TradeInfo24H = data24H

		data30D := &ItemData{}
		// 30day 统计
		for _, d := range p.responseAnalytics.Items30Days {
			if d != nil {
				data30D.AssetAmt = data30D.AssetAmt.Add(d.AssetAmt)
				data30D.OrderCount += d.OrderCount
				data30D.SatsValue += d.SatsValue
			}
		}
		if data30D.AssetAmt.Sign() == 0 {
			data30D.AvgPrice = nil
		} else {
			data30D.AvgPrice = indexer.NewDecimal(data30D.SatsValue, MAX_PRICE_DIVISIBILITY).Div(data30D.AssetAmt)
		}
		p.responseStatus.TradeInfo30D = data30D

		/////////////////////////
		// responseCache_swap
		addressmap := make(map[string]*responseItem_swap)
		for _, v := range p.traderInfoMap {
			addressmap[v.Address] = &responseItem_swap{
				Address: v.Address,
				Buy:     len(p.swapMap[v.Address]),
			}
		}

		p.responseCache_swap = make([]*responseItem_swap, 0, len(addressmap))
		for _, v := range addressmap {
			p.responseCache_swap = append(p.responseCache_swap, v)
		}

		sort.Slice(p.responseCache_swap, func(i, j int) bool {
			if p.responseCache_swap[i].Buy == p.responseCache_swap[j].Buy {
				if p.responseCache_swap[i].Sell == p.responseCache_swap[j].Sell {
					return p.responseCache_swap[i].Address < p.responseCache_swap[j].Address
				} else {
					return p.responseCache_swap[i].Sell > p.responseCache_swap[j].Sell
				}
			}
			return p.responseCache_swap[i].Buy > p.responseCache_swap[j].Buy
		})

		/////////////////////////////////
		// history

		p.refreshTime_swap = time.Now().Unix()
	}
}

func (p *SwapContractRuntime) RuntimeStatus() string {

	p.updateResponseData()

	p.mutex.RLock()
	defer p.mutex.RUnlock()

	buf, err := json.Marshal(p.responseStatus)
	if err != nil {
		Log.Errorf("RuntimeStatus Marshal %s failed, %v", p.URL(), err)
		return ""
	}
	return string(buf)
}

func (p *SwapContractRuntime) RuntimeAnalytics() string {
	p.updateResponseData()

	p.mutex.RLock()
	defer p.mutex.RUnlock()

	buf, err := json.Marshal(p.responseAnalytics)
	if err != nil {
		Log.Errorf("RuntimeAnalytics Marshal %s failed, %v", p.URL(), err)
		return ""
	}
	return string(buf)
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
	Items15Min   []*AnalytcisItem   `json:"items_15hours"`
	Items24Hours []*AnalytcisItem   `json:"items_24hours"`
	Items30Days  []*AnalytcisItem   `json:"items_30days"`
}

const (
	MIN15_SECOND = 15 * 60
	HOUR_SECOND  = 60 * 60
	DAY_SECOND   = 24 * HOUR_SECOND
)

func (p *SwapContractRuntime) genAnalyticsData() *AnalytcisData {
	Log.Debugf("genAnalyticsData...")

	nowTimeStamp := time.Now().Unix()
	todayTimeStamp := nowTimeStamp - DAY_SECOND

	result := &AnalytcisData{
		AssetsName: p.GetAssetName(),
	}
	result.Items15Min = make([]*AnalytcisItem, 24)
	result.Items24Hours = make([]*AnalytcisItem, 24)
	result.Items30Days = make([]*AnalytcisItem, 30)

	if p.InvokeCount == 0 {
		return result
	}

	// Initial 24hours analytics data
	for index := range result.Items15Min {
		minAnalytcisItem := NewAnalytcisItem()
		timestampHour := nowTimeStamp - int64(index)*MIN15_SECOND
		hourTime := time.Unix(timestampHour, 0)
		minAnalytcisItem.Date = hourTime.Format(time.DateOnly)
		minAnalytcisItem.Time = hourTime.Format("15:04")
		result.Items15Min[index] = minAnalytcisItem
	}

	// Initial 24hours analytics data
	for index := range result.Items24Hours {
		hourAnalytcisItem := NewAnalytcisItem()
		timestampHour := nowTimeStamp - int64(index)*HOUR_SECOND
		hourTime := time.Unix(timestampHour, 0)
		hourAnalytcisItem.Date = hourTime.Format(time.DateOnly)
		hourAnalytcisItem.Time = hourTime.Format("15:04")
		result.Items24Hours[index] = hourAnalytcisItem
	}

	// Initial 30 days analytics data
	for index := range result.Items30Days {
		dayAnalytcisItem := NewAnalytcisItem()
		timestampDay := nowTimeStamp - int64(index)*DAY_SECOND
		dayTime := time.Unix(timestampDay, 0)
		dayAnalytcisItem.Date = dayTime.Format(time.DateOnly)
		dayAnalytcisItem.Time = "00:00"
		result.Items30Days[index] = dayAnalytcisItem
	}

	minsIndex := int(0)
	hoursIndex := int(0)
	daysIndex := int(0)
	var minAnalytcisItem *AnalytcisItem
	var hourAnalytcisItem *AnalytcisItem
	var dayAnalytcisItem *AnalytcisItem

	for i := p.InvokeCount; i >= 0; i-- {
		item := p.getItemFromBuck(i)
		if item == nil || item.Done != DONE_DEALT {
			continue
		}

		minsIndex = int((nowTimeStamp - item.OrderTime) / MIN15_SECOND)
		if minsIndex >= 0 && minsIndex < len(result.Items15Min) {
			// in 24 15min
			minAnalytcisItem = result.Items15Min[minsIndex]
			if minAnalytcisItem == nil {
				minAnalytcisItem = NewAnalytcisItem()
				result.Items15Min[minsIndex] = minAnalytcisItem
			}
		} else {
			// out of 24 hours, not analytic hours data
			minAnalytcisItem = nil
		}

		hoursIndex = int((nowTimeStamp - item.OrderTime) / HOUR_SECOND)
		if hoursIndex >= 0 && hoursIndex < len(result.Items24Hours) {
			// in 24 hours
			hourAnalytcisItem = result.Items24Hours[hoursIndex]
			if hourAnalytcisItem == nil {
				hourAnalytcisItem = NewAnalytcisItem()
				result.Items24Hours[hoursIndex] = hourAnalytcisItem
			}
		} else {
			// out of 24 hours, not analytic hours data
			hourAnalytcisItem = nil
		}

		if item.OrderTime >= todayTimeStamp {
			daysIndex = 0
		} else {
			daysIndex = int((todayTimeStamp-item.OrderTime)/DAY_SECOND + 1)
		}

		if daysIndex >= 0 && daysIndex < len(result.Items30Days) {
			// in 30 days
			dayAnalytcisItem = result.Items30Days[daysIndex]
			if dayAnalytcisItem == nil {
				dayAnalytcisItem = NewAnalytcisItem()
				result.Items30Days[daysIndex] = dayAnalytcisItem
			}
		} else {
			// out of 24 hours, not analytic hours data
			dayAnalytcisItem = nil
		}

		if dayAnalytcisItem == nil && hourAnalytcisItem == nil && minAnalytcisItem == nil {
			break
		}

		if minAnalytcisItem != nil {
			if item.OrderType == ORDERTYPE_BUY {
				minAnalytcisItem.SatsValue += item.InValue - item.ServiceFee
				minAnalytcisItem.AssetAmt = minAnalytcisItem.AssetAmt.Add(item.OutAmt)
			} else if item.OrderType == ORDERTYPE_SELL {
				minAnalytcisItem.AssetAmt = minAnalytcisItem.AssetAmt.Add(item.InAmt)
				minAnalytcisItem.SatsValue += item.OutValue + item.ServiceFee
			}
			minAnalytcisItem.OrderCount++
		}

		if hourAnalytcisItem != nil {
			if item.OrderType == ORDERTYPE_BUY {
				hourAnalytcisItem.SatsValue += item.InValue - item.ServiceFee
				hourAnalytcisItem.AssetAmt = hourAnalytcisItem.AssetAmt.Add(item.OutAmt)
			} else if item.OrderType == ORDERTYPE_SELL {
				hourAnalytcisItem.AssetAmt = hourAnalytcisItem.AssetAmt.Add(item.InAmt)
				hourAnalytcisItem.SatsValue += item.OutValue + item.ServiceFee
			}
			hourAnalytcisItem.OrderCount++
		}

		if dayAnalytcisItem != nil {
			if item.OrderType == ORDERTYPE_BUY {
				dayAnalytcisItem.SatsValue += item.InValue - item.ServiceFee
				dayAnalytcisItem.AssetAmt = dayAnalytcisItem.AssetAmt.Add(item.OutAmt)
			} else if item.OrderType == ORDERTYPE_SELL {
				dayAnalytcisItem.AssetAmt = dayAnalytcisItem.AssetAmt.Add(item.InAmt)
				dayAnalytcisItem.SatsValue += item.OutValue
			}
			dayAnalytcisItem.OrderCount++
		}
	}

	// 资产在统计时累加了两次，一个买单得到的资产，肯定是来自卖单的资产
	for _, item := range result.Items15Min {
		item.AssetAmt = item.AssetAmt.DivBigInt(big.NewInt(2))
		item.SatsValue /= 2
		if item.AssetAmt.Sign() != 0 {
			item.AvgPrice = indexer.DecimalDiv(indexer.NewDecimal(item.SatsValue, MAX_PRICE_DIVISIBILITY), item.AssetAmt)
		}
	}

	for _, item := range result.Items24Hours {
		item.AssetAmt = item.AssetAmt.DivBigInt(big.NewInt(2))
		item.SatsValue /= 2
		if item.AssetAmt.Sign() != 0 {
			item.AvgPrice = indexer.DecimalDiv(indexer.NewDecimal(item.SatsValue, MAX_PRICE_DIVISIBILITY), item.AssetAmt)
		}
	}

	for _, item := range result.Items30Days {
		item.AssetAmt = item.AssetAmt.DivBigInt(big.NewInt(2))
		item.SatsValue /= 2
		if item.AssetAmt.Sign() != 0 {
			item.AvgPrice = indexer.DecimalDiv(indexer.NewDecimal(item.SatsValue, MAX_PRICE_DIVISIBILITY), item.AssetAmt)
		}
	}

	return result
}

func getBuckIndex(id int64) int {
	return int(id / BUCK_SIZE)
}

func getBuckSubIndex(id int64) int {
	return int(id % BUCK_SIZE)
}

func (p *SwapContractRuntime) getItemFromBuck(id int64) *SwapHistoryItem {
	index := getBuckIndex(id)
	subIndex := getBuckSubIndex(id)
	buck, ok := p.responseHistory[index]
	if !ok {
		buck = p.loadBuckFromDB(index)
	}
	if buck != nil {
		return buck[subIndex]
	}
	return nil
}

func (p *SwapContractRuntime) insertBuck(item *SwapHistoryItem) {
	index := getBuckIndex(item.Id)
	buck, ok := p.responseHistory[index]
	if !ok {
		buck = p.loadBuckFromDB(index)
	}
	buck[getBuckSubIndex(item.Id)] = item
}

func (p *SwapContractRuntime) loadBuckFromDB(id int) []*SwapHistoryItem {
	items := loadContractInvokeHistoryWithRange(p.stp.GetDB(), p.URL(), id*BUCK_SIZE, BUCK_SIZE)
	item2 := make([]*SwapHistoryItem, BUCK_SIZE)
	for _, item := range items {
		swapItem, ok := item.(*SwapHistoryItem)
		if ok {
			item2[getBuckSubIndex(swapItem.Id)] = swapItem
		}
	}
	p.responseHistory[id] = item2
	return item2
}

func (p *SwapContractRuntime) InvokeHistory(f any, start, limit int) string {
	p.updateResponseData()

	// TODO getItemFromBuck 需要写，以后要优化下，不然可能会影响效率，很卡
	p.mutex.Lock()
	defer p.mutex.Unlock()

	type response struct {
		Total int                `json:"total"`
		Start int                `json:"start"`
		Data  []*SwapHistoryItem `json:"data"`
	}
	defaultRsp := `{"total":0,"start":0,"data":[]}`

	// 默认倒序，也就是start是最后一个，往前读取日志
	if f == nil {
		result := &response{
			Total: int(p.InvokeCount),
			Start: start,
		}
		if p.InvokeCount != 0 && start >= 0 && start < int(p.InvokeCount) {
			if limit <= 0 {
				limit = 100
			}

			// 换算成真实坐标
			start = int(p.InvokeCount) - 1 - start
			if start < 0 {
				start = 0
			}
			end := start - limit
			if end < 0 {
				end = -1 // 不包括end
			}

			for i := start; i > end; i-- {
				item := p.getItemFromBuck(int64(i))
				if item == nil {
					p.loadBuckFromDB(getBuckIndex(int64(i)))
					item = p.getItemFromBuck(int64(i))
					if item == nil {
						continue
					}
				}

				result.Data = append(result.Data, item)
			}
		}

		// TODO 如果数据太多，只保留前20，后10，共30桶
		buf, err := json.Marshal(result)
		if err != nil {
			Log.Errorf("Marshal responseHistory failed, %v", err)
			return defaultRsp
		}
		return string(buf)
	}

	// 根据地址过滤
	filter := f.(string)
	parts := strings.Split(filter, "=")
	if len(parts) != 2 {
		return defaultRsp
	}
	if parts[0] != "address" {
		return defaultRsp
	}

	trader := p.loadTraderInfo(parts[1])
	result := &response{
		Total: int(trader.InvokeCount),
		Start: start,
	}
	if trader.InvokeCount != 0 && start >= 0 && start < int(trader.InvokeCount) {
		if limit <= 0 {
			limit = 100
		}

		// 换算成真实坐标
		start = int(trader.InvokeCount) - 1 - start
		if start < 0 {
			start = 0
		}
		end := start - limit + 1
		if end < 0 {
			end = 0 // 包括end
		}

		url := p.URL()
		buck1 := getBuckIndex(int64(start))
		buck2 := getBuckIndex(int64(end))
		for i := buck1; i >= buck2; i-- {
			buck, ok := trader.History[i]
			if !ok {
				continue
			}

			var idvect []int64
			if i == buck1 {
				if i == buck2 {
					idvect = buck[end%BUCK_SIZE : (start+1)%BUCK_SIZE]
				} else {
					idvect = buck[0 : (start+1)%BUCK_SIZE]
				}
			} else if i == buck2 {
				idvect = buck[end%BUCK_SIZE : BUCK_SIZE]
			} else {
				idvect = buck[0:BUCK_SIZE]
			}

			for j := len(idvect) - 1; j >= 0; j-- {
				id := idvect[j]
				item := p.getItemFromBuck(id)
				if item == nil {
					itemBase, err := loadContractInvokeHistoryItem(p.stp.GetDB(), url, GetKeyFromId(id))
					if err != nil {
						Log.Errorf("loadContractInvokeHistoryItem %s %d failed", url, id)
						continue
					}
					item = itemBase.(*SwapHistoryItem)
					if item != nil {
						p.insertBuck(item)
						result.Data = append(result.Data, item)
					}
				} else {
					result.Data = append(result.Data, item)
				}
			}
		}
	}

	// 如果数据太多，只保留前20，后10，共30桶
	buf, err := json.Marshal(result)
	if err != nil {
		Log.Errorf("Marshal responseHistory failed, %v", err)
		return defaultRsp
	}
	return string(buf)

}

type responseItem_swap struct {
	Address string `json:"address"`
	Sell    int    `json:"sell"`
	Buy     int    `json:"buy"`
}

func (p *SwapContractRuntime) AllAddressInfo(start, limit int) string {

	p.updateResponseData()

	p.mutex.RLock()
	defer p.mutex.RUnlock()

	type response struct {
		Total int                  `json:"total"`
		Start int                  `json:"start"`
		Data  []*responseItem_swap `json:"data"`
	}

	result := &response{
		Total: len(p.responseCache_swap),
		Start: start,
	}
	if start < 0 || start >= len(p.responseCache_swap) {
		return ""
	}
	if limit <= 0 {
		limit = 100
	}
	end := start + limit
	if end > len(p.responseCache_swap) {
		end = len(p.responseCache_swap)
	}
	result.Data = p.responseCache_swap[start:end]

	buf, err := json.Marshal(result)
	if err != nil {
		Log.Errorf("Marshal SwapContractRuntime failed, %v", err)
		return ""
	}
	return string(buf)
}

func (p *SwapContractRuntime) StatusByAddress(address string) (string, error) {

	//p.updateResponseData()

	p.mutex.RLock()
	defer p.mutex.RUnlock()

	type response struct {
		Statistic   *TraderStatistic `json:"status"`
		OnList      []string         `json:"onList"`
		OnRefund    []string         `json:"refund"`
		Deposit     []string         `json:"deposit"`
		Withdraw    []string         `json:"withdraw"`
		AddLiq      []string         `json:"addLiq"`
		RemoveLiq   []string         `json:"removeLiq"`
		StakeList   []string         `json:"stake"`
		UnstakeList []string         `json:"unstake"`
		ProfitList  []string         `json:"profit"`
	}

	result := &response{}
	trader := p.loadTraderInfo(address)
	if trader != nil {
		result.Statistic = trader.Statistic()
		swapmap := p.swapMap[address]
		for _, v := range swapmap {
			result.OnList = append(result.OnList, v.InUtxo)
		}
		refundmap := p.refundMap[address]
		for _, v := range refundmap {
			result.OnRefund = append(result.OnRefund, v.InUtxo)
		}
		for _, v := range p.depositMap[address] {
			result.Deposit = append(result.Deposit, v.InUtxo)
		}
		for _, v := range p.withdrawMap[address] {
			result.Withdraw = append(result.Withdraw, v.InUtxo)
		}
		for _, v := range p.addLiquidityMap[address] {
			result.AddLiq = append(result.AddLiq, v.InUtxo)
		}
		for _, v := range p.removeLiquidityMap[address] {
			result.RemoveLiq = append(result.RemoveLiq, v.InUtxo)
		}
		for _, v := range p.stakeMap[address] {
			result.StakeList = append(result.StakeList, v.InUtxo)
		}
		for _, v := range p.profitMap[address] {
			result.ProfitList = append(result.ProfitList, v.InUtxo)
		}
	}

	buf, err := json.Marshal(result)
	if err != nil {
		Log.Errorf("Marshal trader status failed, %v", err)
		return "", err
	}

	return string(buf), nil
}

func (p *SwapContractRuntime) DeploySelf() bool {
	return false
}

func (p *SwapContractRuntime) AllowDeploy() error {

	// 检查合约的资产名称是否已经存在
	tickerInfo := p.stp.GetTickerInfo(p.resv.GetContract().GetAssetName())
	if tickerInfo == nil {
		return fmt.Errorf("getTickerInfo %s failed", p.resv.GetContract().GetAssetName().String())
	}

	err := p.ContractRuntimeBase.AllowDeploy()
	if err != nil {
		return err
	}

	return nil
}

func (p *SwapContractRuntime) UnconfirmedTxId() string {
	return ""
}

func (p *SwapContractRuntime) UnconfirmedTxId_SatsNet() string {
	return ""
}

// return fee: 调用费用+该invoke需要的聪数量
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
				fee := CalcSwapFee(satsValue)
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
			// 如果是买单，是utxo的聪数量
			// 如果是卖单，是utxo的资产数量
			if swapParam.OrderType == ORDERTYPE_BUY {
				satsValue := price.Int64()
				fee := CalcSwapFee(satsValue)
				return fee, nil
			} else {
				// 卖单在交易前扣除资产，进入池子
				return SWAP_INVOKE_FEE, nil
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
		isPlainSat := innerParam.AssetName == indexer.ASSET_PLAIN_SAT.String()
		if isPlainSat && amt.Int64() < 330 {
			return 0, fmt.Errorf("withdraw sats should bigger than 330")
		}

		// 检查一层网络上是否有足够的资产，现在合约不保留一层资产，只需要确保二层资产有足够资产就行
		totalAmt := p.stp.GetWalletMgr().GetAssetBalance(p.ChannelAddr, p.GetAssetName())
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

	case INVOKE_API_ADDLIQUIDITY:
		if templateName != TEMPLATE_CONTRACT_AMM {
			return 0, fmt.Errorf("unsupport")
		}
		var innerParam AddLiqInvokeParam
		err := json.Unmarshal([]byte(invoke.Param), &innerParam)
		if err != nil {
			return 0, err
		}
		if innerParam.OrderType != ORDERTYPE_ADDLIQUIDITY {
			return 0, fmt.Errorf("invalid order type %d", innerParam.OrderType)
		}
		if innerParam.AssetName != assetName.String() {
			return 0, fmt.Errorf("invalid asset name %s", innerParam.AssetName)
		}
		if innerParam.Amt == "" || innerParam.Amt == "0" {
			return 0, fmt.Errorf("invalid amt %s", innerParam.Amt)
		}
		_, err = indexer.NewDecimalFromString(innerParam.Amt, p.Divisibility)
		if err != nil {
			return 0, fmt.Errorf("invalid amt %s", innerParam.Amt)
		}
		if innerParam.Value <= 0 {
			return 0, fmt.Errorf("invalid value %d", innerParam.Value)
		}

		return INVOKE_FEE + innerParam.Value, nil

	case INVOKE_API_REMOVELIQUIDITY:
		if templateName != TEMPLATE_CONTRACT_AMM {
			return 0, fmt.Errorf("unsupport")
		}
		var innerParam RemoveLiqInvokeParam
		err := json.Unmarshal([]byte(invoke.Param), &innerParam)
		if err != nil {
			return 0, err
		}
		if innerParam.OrderType != ORDERTYPE_REMOVELIQUIDITY {
			return 0, fmt.Errorf("invalid order type %d", innerParam.OrderType)
		}
		if innerParam.AssetName != assetName.String() {
			return 0, fmt.Errorf("invalid asset name %s", innerParam.AssetName)
		}
		if innerParam.LptAmt == "" || innerParam.LptAmt == "0" {
			return 0, fmt.Errorf("invalid amt %s", innerParam.LptAmt)
		}
		amt, err := indexer.NewDecimalFromString(innerParam.LptAmt, MAX_ASSET_DIVISIBILITY)
		if err != nil {
			return 0, fmt.Errorf("invalid amt %s", innerParam.LptAmt)
		}

		// 检查invoker是否有足够的资产 (TODO 这个接口无法知道inoker，无法检查)
		if amt.Cmp(p.TotalLptAmt) > 0 {
			return 0, fmt.Errorf("no enough lpt asset")
		}
		return INVOKE_FEE, nil
	
	case INVOKE_API_PROFIT:
		if templateName != TEMPLATE_CONTRACT_AMM {
			return 0, fmt.Errorf("unsupport")
		}
		var innerParam ProfitInvokeParam
		err := json.Unmarshal([]byte(invoke.Param), &innerParam)
		if err != nil {
			return 0, err
		}
		if innerParam.OrderType != ORDERTYPE_PROFIT {
			return 0, fmt.Errorf("invalid order type %d", innerParam.OrderType)
		}
		ratio, err := indexer.NewDecimalFromString(innerParam.Ratio, MAX_ASSET_DIVISIBILITY)
		if err != nil {
			return 0, fmt.Errorf("invalid ratio %s", innerParam.Ratio)
		}
		f := ratio.Float64()
		if f <= 0 || f > 1 {
			return 0, fmt.Errorf("invalid ratio %s", innerParam.Ratio)
		}
		return INVOKE_FEE, nil

	default:
		return 0, fmt.Errorf("unsupport action %s", invoke.Action)
	}

	return SWAP_INVOKE_FEE, nil
}

func (p *SwapContractRuntime) processInvoke_SatsNet(data *InvokeDataInBlock_SatsNet) error {

	err := p.AllowInvoke()
	if err == nil {

		bUpdate := false
		for _, tx := range data.InvokeTxVect {
			if !p.IsMyInvoke_SatsNet(tx) {
				continue
			}
			err := p.CheckInvokeTx_SatsNet(tx)
			if err != nil {
				Log.Warningf("%s CheckInvokeTx_SatsNet failed, %v", p.RelativePath(), err)
				continue
			}

			_, err = p.Invoke_SatsNet(tx, data.Height)
			if err == nil {
				Log.Infof("%s Invoke_SatsNet %s succeed", p.RelativePath(), tx.Tx.TxID())
				bUpdate = true
			} else {
				Log.Errorf("%s Invoke_SatsNet %s failed, %v", p.RelativePath(), tx.Tx.TxID(), err)
			}
		}
		if bUpdate {
			p.refreshTime_swap = 0
			p.stp.SaveReservation(p.resv)
		}
	} else {
		//Log.Infof("%s not allowed yet, %v", p.RelativePath(), err)
	}
	return nil
}

func (p *SwapContractRuntime) InvokeWithBlock_SatsNet(data *InvokeDataInBlock_SatsNet) error {

	err := p.ContractRuntimeBase.InvokeWithBlock_SatsNet(data)
	if err != nil {
		return err
	}

	p.mutex.Lock()
	p.processInvoke_SatsNet(data)
	p.swap()
	p.ContractRuntimeBase.InvokeCompleted_SatsNet(data)
	p.mutex.Unlock()

	// 发送
	p.sendInvokeResultTx_SatsNet()
	return nil
}

func (p *SwapContractRuntime) processInvoke(data *InvokeDataInBlock) error {

	err := p.AllowInvoke()
	if err == nil {
		bUpdate := false
		for _, tx := range data.InvokeTxVect {
			if !p.IsMyInvoke(tx) {
				continue
			}
			err := p.CheckInvokeTx(tx)
			if err != nil {
				Log.Warningf("%s CheckInvokeTx failed, %v", p.RelativePath(), err)
				continue
			}

			_, err = p.Invoke(tx, data.Height)
			if err == nil {
				Log.Infof("%s invoke %s succeed", p.RelativePath(), tx.Tx.TxID())
				bUpdate = true
			} else {
				Log.Infof("%s invoke %s failed, %v", p.RelativePath(), tx.Tx.TxID(), err)
			}
		}
		if bUpdate {
			p.refreshTime_swap = 0
			p.stp.SaveReservation(p.resv)
		}
	} else {
		Log.Infof("%s allowInvoke failed, %v", p.URL(), err)
	}
	return nil
}

func (p *SwapContractRuntime) InvokeWithBlock(data *InvokeDataInBlock) error {

	err := p.ContractRuntimeBase.InvokeWithBlock(data)
	if err != nil {
		return err
	}

	if p.IsActive() {
		p.mutex.Lock()
		p.processInvoke(data)
		p.swap()
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

func (p *SwapContractRuntime) Invoke_SatsNet(invokeTx *InvokeTx_SatsNet, height int) (InvokeHistoryItem, error) {

	invokeData := invokeTx.InvokeParam
	output := sindexer.GenerateTxOutput(invokeTx.Tx, invokeTx.InvokeVout)
	address := invokeTx.Invoker

	var param InvokeParam
	err := param.Decode(invokeData.InvokeParam)
	if err != nil {
		return nil, err
	}
	utxoId := indexer.ToUtxoId(height, invokeTx.TxIndex, invokeTx.InvokeVout)
	output.UtxoId = utxoId
	utxo := fmt.Sprintf("%s:%d", invokeTx.Tx.TxID(), invokeTx.InvokeVout)
	org, ok := p.history[utxo]
	if ok {
		org.UtxoId = utxoId
		return nil, fmt.Errorf("contract utxo %s has been handled", utxo)
	}

	url := p.URL()
	switch param.Action {
	case INVOKE_API_SWAP:
		// 检查交换资产的数据
		paramBytes, err := base64.StdEncoding.DecodeString(param.Param)
		if err != nil {
			return nil, err
		}
		var swapParam SwapInvokeParam
		err = swapParam.Decode(paramBytes)
		if err != nil {
			return nil, err
		}
		if swapParam.AssetName != p.GetAssetName().String() {
			return nil, fmt.Errorf("invalid asset name %s", swapParam.AssetName)
		}

		// 到这里，客观条件都满足了，如果还不能符合铸造条件，那就需要退款
		bValid := true
		for {

			// 在不同的交易中有不同的含义：
			// amm中指最小要买入的资产数量或得到的聪数量, 可以不设置
			// swap中指资产购买量，买到这么多就截止 （unitPrice是最高买入价）
			amt, err := indexer.NewDecimalFromString(swapParam.Amt, p.Divisibility)
			if err != nil {
				// amm模式可以不设置
				if p.GetTemplateName() == TEMPLATE_CONTRACT_AMM {
					amt = indexer.NewDecimal(0, p.Divisibility)
				} else {
					Log.Errorf("NewDecimalFromString %s failed, %v", swapParam.Amt, err)
					bValid = false
					break
				}
			}

			price, err := indexer.NewDecimalFromString(swapParam.UnitPrice, MAX_PRICE_DIVISIBILITY)
			if err != nil {
				Log.Errorf("NewDecimalFromString %s failed, %v", swapParam.UnitPrice, err)
				bValid = false
				break
			}

			switch swapParam.OrderType {
			case ORDERTYPE_BUY:
				plainSats := output.GetPlainSat()
				tn := p.GetTemplateName()
				var requireValue int64
				if tn == TEMPLATE_CONTRACT_SWAP {
					requireValue = indexer.DecimalMul(price, amt).Ceil()
				} else if tn == TEMPLATE_CONTRACT_AMM {
					requireValue = price.Int64() // 聪数量
				} else {
					Log.Errorf("invalid template %s", tn)
					bValid = false
					break
				}
				expected := requireValue + CalcSwapFee(requireValue)
				min := expected * 95 / 100
				max := expected * 105 / 100

				// 检查太严格容易导致挂单失败，需要一些精度容许范围内的误差
				if plainSats < min || plainSats > max {
					Log.Errorf("utxo %s should provide %d sats", utxo, expected)
					bValid = false
				}

			case ORDERTYPE_SELL:
				plainSats := output.GetPlainSat_Ceil() - SWAP_INVOKE_FEE
				if plainSats < 0 {
					Log.Errorf("utxo %s no enough sats to pay invoke fee", utxo)
					bValid = false
					break
				}

				assetAmt := output.GetAsset(p.GetAssetName())
				if assetAmt.IsZero() {
					Log.Errorf("utxo %s no asset %s", utxo, p.GetAssetName().String())
					bValid = false
					break
				}

				if p.GetTemplateName() == TEMPLATE_CONTRACT_SWAP {
					// swap
					if assetAmt.Cmp(amt) != 0 {
						Log.Errorf("utxo %s should provide %s %s", utxo, amt.String(), p.GetAssetName().String())
						bValid = false
						break
					}
				} else {
					// amm合约, price是资产数量
				}

			default:
				Log.Errorf("invalid order type %d", swapParam.OrderType)
				bValid = false
			}

			break
		}

		// 更新合约状态
		return p.updateContract_swap(address, output, &swapParam, bValid), nil

	case INVOKE_API_WITHDRAW:
		// 检查资产的数据
		paramBytes, err := base64.StdEncoding.DecodeString(param.Param)
		if err != nil {
			return nil, err
		}
		var withdrawParam WithdrawInvokeParam
		err = withdrawParam.Decode(paramBytes)
		if err != nil {
			return nil, err
		}
		if withdrawParam.AssetName != p.GetAssetName().String() {
			return nil, fmt.Errorf("invalid asset name %s", withdrawParam.AssetName)
		}

		// 到这里，客观条件都满足了，如果还不能符合铸造条件，那就需要退款

		assetAmt := indexer.NewDecimal(0, p.Divisibility)
		bValid := true
		for {
			amt, err := indexer.NewDecimalFromString(withdrawParam.Amt, p.Divisibility)
			if err != nil {
				Log.Errorf("NewDecimalFromString %s failed, %v", withdrawParam.Amt, err)
				bValid = false
				break
			}

			switch withdrawParam.OrderType {
			case ORDERTYPE_WITHDRAW:
				plainSats := output.GetPlainSat()
				isPlainSat := withdrawParam.AssetName == indexer.ASSET_PLAIN_SAT.String()
				if isPlainSat {
					plainSats -= amt.Int64()
				}
				assetName := &AssetName{
					AssetName: *p.GetAssetName(),
					N:         p.N,
				}
				fee := CalcFee_SendTx(2, 3, 1, assetName, amt, p.stp.GetFeeRate(), true)
				expected := WITHDRAW_INVOKE_FEE + fee
				min := expected * 95 / 100
				if plainSats < min {
					Log.Errorf("utxo %s no enough sats to pay withdraw fee %d", utxo, expected)
					bValid = false
					break
				}

				if isPlainSat {
					assetAmt = amt
					if amt.Int64() < 330 {
						Log.Errorf("utxo %s withdraw dust %d", utxo, amt.Int64())
						bValid = false
						break
					}
				} else {
					assetAmt = output.GetAsset(p.GetAssetName())
					if assetAmt.IsZero() {
						Log.Errorf("utxo %s no asset %s", utxo, p.GetAssetName().String())
						bValid = false
						break
					}

					if assetAmt.Cmp(amt) < 0 {
						Log.Errorf("asset amt %s is less than required amt %s", assetAmt.String(), amt.String())
						bValid = false
						break
					}
				}

			default:
				Log.Errorf("invalid order type %d", withdrawParam.OrderType)
				bValid = false
			}
			break
		}
		// 更新合约状态
		return p.updateContract_deposit(address, output, &withdrawParam, bValid, false), nil

	case INVOKE_API_REFUND:
		// 取回所有资产，包括已经部分成交但还没有发送的和未成交的
		return p.updateContract_refund(address, output.GetPlainSat(), utxo, false, utxoId), nil

	case INVOKE_API_ADDLIQUIDITY:
		paramBytes, err := base64.StdEncoding.DecodeString(param.Param)
		if err != nil {
			return nil, err
		}
		var invokeParam AddLiqInvokeParam
		err = invokeParam.Decode(paramBytes)
		if err != nil {
			return nil, err
		}
		if invokeParam.AssetName != p.GetAssetName().String() &&
			invokeParam.AssetName != indexer.ASSET_PLAIN_SAT.String() {
			return nil, fmt.Errorf("invalid asset name %s", invokeParam.AssetName)
		}

		// 到这里，客观条件都满足了，如果还不能符合铸造条件，那就需要退款

		satsValue := int64(0)
		assetAmt := indexer.NewDecimal(0, p.Divisibility)
		bValid := true
		for {
			amt, err := indexer.NewDecimalFromString(invokeParam.Amt, p.Divisibility)
			if err != nil {
				Log.Errorf("NewDecimalFromString %s failed, %v", invokeParam.Amt, err)
				bValid = false
				break
			}
			satsValue = invokeParam.Value

			switch invokeParam.OrderType {
			case ORDERTYPE_ADDLIQUIDITY:
				plainSats := output.GetPlainSat_Ceil()
				plainSats -= satsValue
				if plainSats < INVOKE_FEE {
					Log.Errorf("utxo %s no enough sats to pay stake fee %d", utxo, INVOKE_FEE)
					bValid = false
					break
				}

				assetAmt = output.GetAsset(p.GetAssetName())
				if assetAmt.IsZero() {
					Log.Errorf("utxo %s no asset %s", utxo, p.GetAssetName().String())
					bValid = false
					break
				}

				if assetAmt.Cmp(amt) < 0 {
					Log.Errorf("asset amt %s is less than required amt %s", assetAmt.String(), amt.String())
					bValid = false
					break
				}

			default:
				Log.Errorf("invalid order type %d", invokeParam.OrderType)
				bValid = false
			}
			break
		}
		// 更新合约状态
		return p.updateContract_liquidity(address, output, invokeParam.OrderType,
			assetAmt, satsValue, bValid, false, false), nil

	case INVOKE_API_REMOVELIQUIDITY:
		paramBytes, err := base64.StdEncoding.DecodeString(param.Param)
		if err != nil {
			return nil, err
		}
		var invokeParam RemoveLiqInvokeParam
		err = invokeParam.Decode(paramBytes)
		if err != nil {
			return nil, err
		}
		if invokeParam.AssetName != p.GetAssetName().String() &&
			invokeParam.AssetName != indexer.ASSET_PLAIN_SAT.String() {
			return nil, fmt.Errorf("invalid asset name %s", invokeParam.AssetName)
		}

		// 到这里，客观条件都满足了，如果还不能符合铸造条件，那就需要退款

		assetAmt := indexer.NewDecimal(0, p.Divisibility)
		bValid := true
		for {
			amt, err := indexer.NewDecimalFromString(invokeParam.LptAmt, MAX_ASSET_DIVISIBILITY)
			if err != nil {
				Log.Errorf("NewDecimalFromString %s failed, %v", invokeParam.LptAmt, err)
				bValid = false
				break
			}

			switch invokeParam.OrderType {
			case ORDERTYPE_REMOVELIQUIDITY:
				plainSats := output.GetPlainSat()
				if plainSats < INVOKE_FEE {
					Log.Errorf("utxo %s no enough sats to pay stake fee %d", utxo, INVOKE_FEE)
					bValid = false
					break
				}

				assetAmt = amt
				if assetAmt.Sign() <= 0 {
					Log.Errorf("invalid expected amt %s", assetAmt.String())
					bValid = false
					break
				}

				// 检查个人是否有足够的流动性资产
				trader := p.loadTraderInfo(invokeTx.Invoker)
				if amt.Cmp(trader.LptAmt) > 0 {
					Log.Errorf("no enough liquidity asset, require %s but only %s",
						amt.String(), trader.LptAmt.String())
					bValid = false
					break
				}

			default:
				Log.Errorf("invalid order type %d", invokeParam.OrderType)
				bValid = false
			}
			break
		}
		// 更新合约状态
		return p.updateContract_liquidity(address, output, invokeParam.OrderType,
			assetAmt, 0, bValid, false, false), nil

	case INVOKE_API_PROFIT:
		paramBytes, err := base64.StdEncoding.DecodeString(param.Param)
		if err != nil {
			return nil, err
		}
		var invokeParam ProfitInvokeParam
		err = invokeParam.Decode(paramBytes)
		if err != nil {
			return nil, err
		}

		// 到这里，客观条件都满足了，如果还不能符合铸造条件，直接设置为无效调用
		var ratio *Decimal
		bValid := true
		for {
			ratio, err = indexer.NewDecimalFromString(invokeParam.Ratio, MAX_ASSET_DIVISIBILITY)
			if err != nil {
				Log.Errorf("invalid ratio %s", invokeParam.Ratio)
				bValid = false
				break
			}
			if ratio.Sign() <= 0 || ratio.Float64() > 1 {
				Log.Errorf("invalid ratio %s", invokeParam.Ratio)
				bValid = false
				break
			}

			switch invokeParam.OrderType {
			case ORDERTYPE_PROFIT:
				plainSats := output.GetPlainSat()
				if plainSats < INVOKE_FEE {
					Log.Errorf("utxo %s no enough sats to pay stake fee %d", utxo, INVOKE_FEE)
					bValid = false
					break
				}

				// 检查是否有利润可以提取
				// profit := p.getBaseProfit()
				// if profit.Sign() <= 0 {
				// 	Log.Errorf("no profit to retrieve")
				// 	bValid = false
				// 	break
				// }

			default:
				Log.Errorf("invalid order type %d", invokeParam.OrderType)
				bValid = false
			}
			break

		}
		// 更新合约状态
		return p.updateContract(address, output, invokeParam.OrderType,
			ratio, bValid, false, false), nil

	default:
		Log.Errorf("contract %s is not support action %s", url, param.Action)
		return nil, fmt.Errorf("not support action %s", param.Action)
	}
}

func (p *SwapContractRuntime) Invoke(invokeTx *InvokeTx, height int) (InvokeHistoryItem, error) {

	invokeData := invokeTx.InvokeParam
	output := indexer.GenerateTxOutput(invokeTx.Tx, invokeTx.InvokeVout)
	address := invokeTx.Invoker

	// 如果invokeData.InvokeParam == nil 默认就是deposit
	var param InvokeParam
	if invokeData != nil && invokeData.InvokeParam != nil {
		err := param.Decode(invokeData.InvokeParam)
		if err != nil {
			return nil, err
		}
	} else {
		switch p.GetTemplateName() {
		case TEMPLATE_CONTRACT_SWAP:
			return nil, fmt.Errorf("swap contract not support deposit")
		default:
		}
		param.Action = INVOKE_API_DEPOSIT
	}

	utxoId := indexer.ToUtxoId(height, invokeTx.TxIndex, invokeTx.InvokeVout)
	output.UtxoId = utxoId
	utxo := fmt.Sprintf("%s:%d", invokeTx.Tx.TxID(), invokeTx.InvokeVout)
	Log.Infof("utxo %x %s\n", utxoId, utxo)
	org, ok := p.history[utxo]
	if ok {
		org.UtxoId = utxoId
		return nil, fmt.Errorf("contract utxo %s has been handled", utxo)
	}

	switch param.Action {
	case INVOKE_API_SWAP: // 必须有 invokeTx.InvokeParam
		return nil, fmt.Errorf("not support action %s", param.Action)

	case INVOKE_API_DEPOSIT: // 主网没有调用参数时的默认动作
		// 检查交换资产的数据
		output := invokeTx.TxOutput
		assetAmt, _ := output.GetAssetV2(p.GetAssetName())

		// 到这里，客观条件都满足了，如果还不能符合铸造条件，那就需要退款
		bValid := true
		if assetAmt.IsZero() {
			Log.Errorf("utxo %s no asset %s", utxo, p.GetAssetName().String())
			bValid = false
		}

		// 如果invokeParam不为nil，要检查数据是否一致
		if param.Param != "" {
			paramBytes, err := base64.StdEncoding.DecodeString(param.Param)
			if err != nil {
				return nil, err
			}
			var depositParam DepositInvokeParam
			err = depositParam.Decode(paramBytes)
			if err != nil {
				return nil, err
			}
			if depositParam.AssetName != "" {
				if depositParam.AssetName != p.GetAssetName().String() {
					return nil, fmt.Errorf("invalid asset name %s", depositParam.AssetName)
				}
			}
			if depositParam.Amt != "0" && depositParam.Amt != "" {
				if depositParam.Amt != assetAmt.String() {
					return nil, fmt.Errorf("invalid asset amt %s", depositParam.Amt)
				}
			}
		}

		// 临时锁定该UTXO TODO 需要一个更好的方案，这里不一定能锁住
		if bValid {
			p.stp.GetWalletMgr().utxoLockerL1.LockUtxo(utxo, "contract deposit")
		}

		// 更新合约状态
		return p.updateContract_deposit(address, OutputToSatsNet(output), nil, bValid, true), nil

	case INVOKE_API_ADDLIQUIDITY: // 必须有 invokeTx.InvokeParam
		paramBytes, err := base64.StdEncoding.DecodeString(param.Param)
		if err != nil {
			return nil, err
		}
		var invokeParam AddLiqInvokeParam
		err = invokeParam.Decode(paramBytes)
		if err != nil {
			return nil, err
		}
		if invokeParam.AssetName != p.GetAssetName().String() &&
			invokeParam.AssetName != indexer.ASSET_PLAIN_SAT.String() {
			return nil, fmt.Errorf("invalid asset name %s", invokeParam.AssetName)
		}

		// 到这里，客观条件都满足了，如果还不能符合铸造条件，那就需要退款

		satsValue := int64(0)
		assetAmt := indexer.NewDecimal(0, p.Divisibility)
		bValid := true
		for {
			amt, err := indexer.NewDecimalFromString(invokeParam.Amt, p.Divisibility)
			if err != nil {
				Log.Errorf("NewDecimalFromString %s failed, %v", invokeParam.Amt, err)
				bValid = false
				break
			}
			satsValue = invokeParam.Value

			switch invokeParam.OrderType {
			case ORDERTYPE_ADDLIQUIDITY:
				plainSats := output.GetPlainSat()
				plainSats -= satsValue
				if plainSats < INVOKE_FEE {
					Log.Errorf("utxo %s no enough sats to pay stake fee %d", utxo, INVOKE_FEE)
					bValid = false
					break
				}

				assetAmt = output.GetAsset(p.GetAssetName())
				if assetAmt.IsZero() {
					Log.Errorf("utxo %s no asset %s", utxo, p.GetAssetName().String())
					bValid = false
					break
				}

				if assetAmt.Cmp(amt) < 0 {
					Log.Errorf("asset amt %s is less than required amt %s", assetAmt.String(), amt.String())
					bValid = false
					break
				}

			default:
				Log.Errorf("invalid order type %d", invokeParam.OrderType)
				bValid = false
			}
			break
		}
		// 更新合约状态
		return p.updateContract_liquidity(address, OutputToSatsNet(output),
			invokeParam.OrderType, assetAmt, satsValue,
			bValid, false, false), nil

	case INVOKE_API_REMOVELIQUIDITY: // 必须有 invokeTx.InvokeParam
		paramBytes, err := base64.StdEncoding.DecodeString(param.Param)
		if err != nil {
			return nil, err
		}
		var invokeParam RemoveLiqInvokeParam
		err = invokeParam.Decode(paramBytes)
		if err != nil {
			return nil, err
		}
		if invokeParam.AssetName != p.GetAssetName().String() &&
			invokeParam.AssetName != indexer.ASSET_PLAIN_SAT.String() {
			return nil, fmt.Errorf("invalid asset name %s", invokeParam.AssetName)
		}

		// 到这里，客观条件都满足了，如果还不能符合铸造条件，那就需要退款

		assetAmt := indexer.NewDecimal(0, p.Divisibility)
		bValid := true
		for {
			amt, err := indexer.NewDecimalFromString(invokeParam.LptAmt, MAX_ASSET_DIVISIBILITY)
			if err != nil {
				Log.Errorf("NewDecimalFromString %s failed, %v", invokeParam.LptAmt, err)
				bValid = false
				break
			}

			switch invokeParam.OrderType {
			case ORDERTYPE_REMOVELIQUIDITY:
				plainSats := output.GetPlainSat()
				if plainSats < INVOKE_FEE {
					Log.Errorf("utxo %s no enough sats to pay stake fee %d", utxo, INVOKE_FEE)
					bValid = false
					break
				}

				assetAmt = amt
				if assetAmt.Sign() <= 0 {
					Log.Errorf("invalid expected amt %s", assetAmt.String())
					bValid = false
					break
				}

				// 检查个人是否已经质押了足够的资产
				trader := p.loadTraderInfo(invokeTx.Invoker)
				if amt.Cmp(trader.LptAmt) > 0 {
					Log.Errorf("no enough liquidity asset, require %s but only %s",
						amt.String(), trader.LptAmt.String())
					bValid = false
					break
				}

			default:
				Log.Errorf("invalid order type %d", invokeParam.OrderType)
				bValid = false
			}
			break
		}
		// 更新合约状态
		return p.updateContract_liquidity(address, OutputToSatsNet(output),
			invokeParam.OrderType, assetAmt, 0,
			bValid, false, false), nil

	default:
		Log.Errorf("contract %s is not support action %s", p.URL(), param.Action)
		return nil, fmt.Errorf("not support action %s", param.Action)
	}
}

func (p *SwapContractRuntime) loadTraderInfo(address string) *TraderStatus {
	status, ok := p.traderInfoMap[address]
	if ok {
		return status
	}

	r, err := loadContractInvokerStatus(p.stp.GetDB(), p.URL(), address)
	if err != nil {
		status = NewTraderStatus(address, p.Divisibility)
		p.traderInfoMap[address] = status
		return status
	}

	status, ok = r.(*TraderStatus)
	if !ok {
		status = NewTraderStatus(address, p.Divisibility)
		p.traderInfoMap[address] = status
		return status
	}
	p.traderInfoMap[address] = status
	return status
} 

func (p *SwapContractRuntime) loadSvrTraderInfo() *TraderStatus {
	return p.loadTraderInfo(p.GetSvrAddress())
}

func (p *SwapContractRuntime) loadFoundationTraderInfo() *TraderStatus {
	return p.loadTraderInfo(p.GetFoundationAddress())
}

func CalcSwapFee(value int64) int64 {
	return SWAP_INVOKE_FEE + CalcSwapServiceFee(value)
}

// 不包括调用费用 （向下取整）
func CalcSwapServiceFee(value int64) int64 {
	return (value * SWAP_SERVICE_FEE_RATIO) / 1000 // 交易服务费
}

// 是上面的逆运算
// 根据amm卖出资产得到的聪（扣除calcSwapServiceFee），计算参与交易的聪数量，用于统计（向下取整）
func CalcDealValue(value int64) int64 {
	return (value * 1000) / (1000 - SWAP_SERVICE_FEE_RATIO)
}

func (p *SwapContractRuntime) updateContract_swap(
	address string, output *sindexer.TxOutput, param *SwapInvokeParam,
	bValid bool) *SwapHistoryItem {

	inValue := output.GetPlainSat_Ceil()
	inAmt := output.GetAsset(p.GetAssetName())
	tn := p.GetTemplateName()

	expectedAmt, _ := indexer.NewDecimalFromString(param.Amt, p.Divisibility)
	price, err := indexer.NewDecimalFromString(param.UnitPrice, MAX_PRICE_DIVISIBILITY)
	if err != nil {
		price = indexer.NewDecimal(0, MAX_PRICE_DIVISIBILITY)
	}

	reason := INVOKE_REASON_NORMAL
	if !bValid {
		reason = INVOKE_REASON_INVALID
	}
	item := &SwapHistoryItem{
		InvokeHistoryItemBase: InvokeHistoryItemBase{
			Id:     p.InvokeCount,
			Reason: reason,
			Done:   DONE_NOTYET,
		},

		OrderType:      param.OrderType,
		UtxoId:         output.UtxoId,
		OrderTime:      time.Now().Unix(),
		AssetName:      p.GetAssetName().String(),
		ServiceFee:     SWAP_INVOKE_FEE,
		UnitPrice:      price,
		ExpectedAmt:    expectedAmt,
		Address:        address,
		FromL1:         false,
		InUtxo:         output.OutPointStr,
		InValue:        inValue,
		InAmt:          inAmt,
		RemainingAmt:   inAmt.Clone(),
		RemainingValue: 0,
		ToL1:           false,
		OutAmt:         indexer.NewDecimal(0, p.Divisibility),
		OutValue:       0,
	}

	switch param.OrderType {
	case ORDERTYPE_SELL:
		// 卖家在完成交易后扣除服务费用（swap暂时免费，amm收费）
		item.RemainingValue = 0
		if tn == TEMPLATE_CONTRACT_SWAP {
		} else if tn == TEMPLATE_CONTRACT_AMM {
			item.UnitPrice = indexer.NewDecimal(0, MAX_PRICE_DIVISIBILITY)
		}
	case ORDERTYPE_BUY:
		// 扣除交易服务费用0.8%
		if tn == TEMPLATE_CONTRACT_SWAP {
			item.ServiceFee = CalcSwapFee(item.GetTradingValue())
		} else if tn == TEMPLATE_CONTRACT_AMM {
			item.UnitPrice = indexer.NewDecimal(0, MAX_PRICE_DIVISIBILITY)
			// item.ServiceFee = calcSwapFee(price.Int64()) AMM升级后，服务费只记录一个调用费用，参与交易的资产的收费，直接留存在AMM池子中
		}
		item.RemainingValue = item.InValue - item.ServiceFee

		if item.RemainingValue < 0 {
			item.RemainingValue = 0
			bValid = false
			item.Reason = INVOKE_REASON_INVALID
		}
	}

	p.updateContractStatus(item)
	p.addItem(item)
	SaveContractInvokeHistoryItem(p.stp.GetDB(), p.URL(), item)
	return item
}

// 包括withdraw
func (p *SwapContractRuntime) updateContract_deposit(
	address string, output *sindexer.TxOutput,
	param *DepositInvokeParam,
	bValid, fromL1 bool) *SwapHistoryItem {

	var remainingValue int64
	var inAmt, expectedAmt, remainingAmt *Decimal
	orderType := ORDERTYPE_DEPOSIT
	assetName := p.GetAssetName()
	if param != nil {
		orderType = param.OrderType
		assetName = indexer.NewAssetNameFromString(param.AssetName)
		expectedAmt, _ = indexer.NewDecimalFromString(param.Amt, p.Divisibility)
	}

	bPlainAsset := indexer.IsPlainAsset(assetName)
	inValue := output.GetPlainSat()
	serviceFee := inValue
	if orderType == ORDERTYPE_DEPOSIT {
		if bPlainAsset {
			inAmt = nil
			expectedAmt = indexer.NewDefaultDecimal(inValue)

			remainingValue = inValue
			remainingAmt = nil
			serviceFee -= remainingValue
		} else {
			inAmt = output.GetAsset(assetName)
			expectedAmt = inAmt.Clone()
			remainingAmt = inAmt.Clone()
		}
	} else {
		if bPlainAsset {
			inAmt = nil

			remainingValue = expectedAmt.Int64()
			remainingAmt = nil
			serviceFee -= remainingValue
		} else {
			inAmt = output.GetAsset(assetName)
			expectedAmt = inAmt.Clone()
			remainingAmt = inAmt.Clone()
		}
	}

	reason := INVOKE_REASON_NORMAL
	if !bValid {
		reason = INVOKE_REASON_INVALID
		if orderType == ORDERTYPE_WITHDRAW {
			serviceFee -= INVOKE_FEE
			if serviceFee < 0 {
				serviceFee = 0
			}
			remainingValue += serviceFee - INVOKE_FEE
		}
	}
	item := &SwapHistoryItem{
		InvokeHistoryItemBase: InvokeHistoryItemBase{
			Id:     p.InvokeCount,
			Reason: reason,
			Done:   DONE_NOTYET,
		},

		OrderType:      orderType,
		UtxoId:         output.UtxoId,
		OrderTime:      time.Now().Unix(),
		AssetName:      assetName.String(),
		ServiceFee:     serviceFee,
		UnitPrice:      nil,
		ExpectedAmt:    expectedAmt,
		Address:        address,
		FromL1:         fromL1,
		InUtxo:         output.OutPointStr,
		InValue:        inValue,
		InAmt:          inAmt,
		RemainingAmt:   remainingAmt.Clone(),
		RemainingValue: remainingValue,
		ToL1:           !fromL1,
		OutAmt:         indexer.NewDecimal(0, p.Divisibility),
		OutValue:       0,
	}
	p.updateContractStatus(item)
	p.addItem(item)
	SaveContractInvokeHistoryItem(p.stp.GetDB(), p.URL(), item)
	return item
}

func (p *SwapContractRuntime) updateContract_liquidity(
	address string, output *sindexer.TxOutput,
	orderType int, assetAmt *Decimal, satsValue int64,
	bValid, fromL1, toL1 bool) *SwapHistoryItem {

	expectedAmt := assetAmt.Clone()
	assetName := p.GetAssetName()
	inValue := output.GetPlainSat_Ceil()
	inAmt := output.GetAsset(assetName)

	serviceFee := inValue
	remainingValue := satsValue
	remainingAmt := assetAmt.Clone()
	// addLiq: in 是输入资产数量，remaining是还剩下的资产（没有加入池子），out是加池子成功的资产
	// removeLiq: in 为空，remaining是要unstake的资产，out是unstake成功的输出
	if orderType == ORDERTYPE_ADDLIQUIDITY {
		serviceFee -= remainingValue
	} else if orderType == ORDERTYPE_REMOVELIQUIDITY {
		remainingAmt = nil
	}

	reason := INVOKE_REASON_NORMAL
	if !bValid {
		reason = INVOKE_REASON_INVALID
	}
	item := &SwapHistoryItem{
		InvokeHistoryItemBase: InvokeHistoryItemBase{
			Id:     p.InvokeCount,
			Reason: reason,
			Done:   DONE_NOTYET,
		},

		OrderType:      orderType,
		UtxoId:         output.UtxoId,
		OrderTime:      time.Now().Unix(),
		AssetName:      assetName.String(),
		ServiceFee:     serviceFee,
		UnitPrice:      nil,
		ExpectedAmt:    expectedAmt, // removeLiq 时，这里是 lptToken的数量
		Address:        address,
		FromL1:         fromL1,
		InUtxo:         output.OutPointStr,
		InValue:        inValue,
		InAmt:          inAmt,
		RemainingAmt:   remainingAmt,
		RemainingValue: remainingValue,
		ToL1:           toL1,
		OutAmt:         indexer.NewDecimal(0, p.Divisibility),
		OutValue:       0,
	}
	p.updateContractStatus(item)
	if reason == INVOKE_REASON_INVALID && orderType == ORDERTYPE_REMOVELIQUIDITY {
		// 无效的指令，直接关闭
		item.Done = DONE_CLOSED_DIRECTLY
	} else {
		p.addItem(item)
	}
	SaveContractInvokeHistoryItem(p.stp.GetDB(), p.URL(), item)
	return item
}

// 通用的调用参数入口
func (p *SwapContractRuntime) updateContract(
	invoker string, output *sindexer.TxOutput,
	orderType int, expectedAmt *Decimal, 
	bValid, fromL1, toL1 bool) *SwapHistoryItem {

	assetName := p.GetAssetName()
	inValue := output.GetPlainSat_Ceil()
	inAmt := output.GetAsset(assetName)

	serviceFee := INVOKE_FEE
	remainingValue := inValue - serviceFee
	remainingAmt := inAmt.Clone()

	reason := INVOKE_REASON_NORMAL
	if !bValid {
		reason = INVOKE_REASON_INVALID
	}
	item := &SwapHistoryItem{
		InvokeHistoryItemBase: InvokeHistoryItemBase{
			Id:     p.InvokeCount,
			Reason: reason,
			Done:   DONE_NOTYET,
		},

		OrderType:      orderType,
		UtxoId:         output.UtxoId,
		OrderTime:      time.Now().Unix(),
		AssetName:      assetName.String(),
		ServiceFee:     serviceFee,
		UnitPrice:      nil,
		ExpectedAmt:    expectedAmt,
		Address:        invoker,
		FromL1:         fromL1,
		InUtxo:         output.OutPointStr,
		InValue:        inValue,
		InAmt:          inAmt,
		RemainingAmt:   remainingAmt,
		RemainingValue: remainingValue,
		ToL1:           toL1,
		OutAmt:         indexer.NewDecimal(0, p.Divisibility),
		OutValue:       0,
	}
	p.updateContractStatus(item)
	if reason == INVOKE_REASON_INVALID && remainingAmt.Sign() == 0 && remainingValue < 10 {
		// 无效的指令，直接关闭
		item.Done = DONE_CLOSED_DIRECTLY
	} else {
		p.addItem(item)
	}
	SaveContractInvokeHistoryItem(p.stp.GetDB(), p.URL(), item)
	return item
}


func (p *SwapContractRuntime) updateContract_refund(
	address string, plainSat int64, utxo string, fromL1 bool, utxoId uint64) *SwapHistoryItem {

	item := &SwapHistoryItem{
		InvokeHistoryItemBase: InvokeHistoryItemBase{
			Id:     p.InvokeCount,
			Reason: INVOKE_REASON_NORMAL,
			Done:   DONE_NOTYET,
		},

		OrderType:      ORDERTYPE_REFUND,
		UtxoId:         utxoId,
		OrderTime:      time.Now().Unix(),
		AssetName:      "",
		ServiceFee:     SWAP_INVOKE_FEE,
		UnitPrice:      nil,
		ExpectedAmt:    nil,
		Address:        address,
		FromL1:         fromL1,
		InUtxo:         utxo,
		InValue:        0,
		InAmt:          nil,
		RemainingAmt:   nil,
		RemainingValue: 0,
		ToL1:           false,
		OutAmt:         nil,
		OutValue:       0,
	}
	p.updateContractStatus(item)
	p.addItem(item)
	SaveContractInvokeHistoryItem(p.stp.GetDB(), p.URL(), item)
	return item
}

func insertItemToTraderHistroy(trader *InvokerStatusBase, item *SwapHistoryItem) {
	index := getBuckIndex(int64(trader.InvokeCount))
	if trader.History == nil {
		trader.History = make(map[int][]int64)
	}
	trader.History[index] = append(trader.History[index], item.Id)
	trader.InvokeCount++
	trader.UpdateTime = time.Now().Unix()
}

// 更新需要写入数据库的数据
func (p *SwapContractRuntime) updateContractStatus(item *SwapHistoryItem) {
	p.history[item.InUtxo] = item

	trader := p.loadTraderInfo(item.Address)
	insertItemToTraderHistroy(&trader.InvokerStatusBase, item)

	p.InvokeCount++
	p.TotalInputAssets = p.TotalInputAssets.Add(item.InAmt)
	p.TotalInputSats += item.InValue

	if item.Reason == INVOKE_REASON_NORMAL {
		switch item.OrderType {
		case ORDERTYPE_BUY:
			trader.OnBuyValue += item.RemainingValue
			p.SatsValueInPool += item.RemainingValue
			if p.HighestBuyPrice.Cmp(item.UnitPrice) < 0 {
				p.HighestBuyPrice = item.UnitPrice.Clone()
			}
			Log.Infof("SatsValueInPool add %d to %d with item %d\n", item.RemainingValue, p.SatsValueInPool, item.Id)

		case ORDERTYPE_SELL:
			trader.OnSaleAmt = trader.OnSaleAmt.Add(item.RemainingAmt)
			p.AssetAmtInPool = p.AssetAmtInPool.Add(item.RemainingAmt)
			if p.LowestSellPrice == nil || p.LowestSellPrice.Cmp(item.UnitPrice) > 0 {
				p.LowestSellPrice = item.UnitPrice.Clone()
			}
			Log.Infof("AssetAmtInPool add %s to %s with item %d\n", item.RemainingAmt.String(), p.AssetAmtInPool.String(), item.Id)

		case ORDERTYPE_REFUND:
			p.addRefundItem(item, true)

		case ORDERTYPE_DEPOSIT:
		case ORDERTYPE_WITHDRAW:
		case ORDERTYPE_ADDLIQUIDITY:
		case ORDERTYPE_REMOVELIQUIDITY:
		case ORDERTYPE_PROFIT:

		default:
			Log.Errorf("%s updateContractStatus unsupport order type %d", p.URL(), item.OrderType)
		}
	} // else 只可能是 INVOKE_REASON_INVALID 不用更新任何数据

	saveContractInvokerStatus(p.stp.GetDB(), p.URL(), trader)
	// 整体状态在外部保存
}

func addItemToMap(item *SwapHistoryItem, addrMap map[string]map[int64]*SwapHistoryItem) {
	itemMap, ok := addrMap[item.Address]
	if !ok {
		itemMap = make(map[int64]*SwapHistoryItem)
		addrMap[item.Address] = itemMap
	}
	itemMap[item.Id] = item
}

func removeItemFromMap(item *SwapHistoryItem, addrMap map[string]map[int64]*SwapHistoryItem) {
	itemMap, ok := addrMap[item.Address]
	if ok {
		delete(itemMap, item.Id)
	}
	if len(itemMap) == 0 {
		delete(addrMap, item.Address)
	}
}

// 会修改sellPool和buyPool，不可在其循环中使用
func (p *SwapContractRuntime) addRefundItem(item *SwapHistoryItem, updatePool bool) {
	if item.OrderType == ORDERTYPE_REFUND {
		// 指令
		swapmap := p.swapMap[item.Address]
		if len(swapmap) == 0 {
			// 没有交易数据，该条记录无效
			item.Reason = INVOKE_REASON_INVALID
			item.Done = DONE_DEALT
		} else {
			if updatePool {
				// 将相关交易从pool中撤下
				i := 0
				for i < len(p.buyPool) {
					buy := p.buyPool[i]
					if buy.Address == item.Address {
						p.SatsValueInPool -= buy.RemainingValue
						p.buyPool = utils.RemoveIndex(p.buyPool, i)
					} else {
						i++
					}
				}

				i = 0
				for i < len(p.sellPool) {
					sell := p.sellPool[i]
					if sell.Address == item.Address {
						p.AssetAmtInPool = p.AssetAmtInPool.Sub(sell.RemainingAmt)
						p.sellPool = utils.RemoveIndex(p.sellPool, i)
					} else {
						i++
					}
				}
			}

			addItemToMap(item, p.refundMap)

			// 将所有该用户的item都设置为refund
			for _, swapItem := range swapmap {
				if swapItem.Reason == INVOKE_REASON_NORMAL && swapItem.Done == DONE_NOTYET {
					swapItem.Reason = INVOKE_REASON_REFUND
					addItemToMap(swapItem, p.refundMap)
					// buy&sell pool在updateWithDealInfo_refund时统一处理无效的item
				}
			}
			p.swapMap[item.Address] = make(map[int64]*SwapHistoryItem)

		}
	} else {
		// 数据
		if updatePool {
			// 将相关交易从pool中撤下
			if item.OrderType == ORDERTYPE_SELL {
				i := 0
				for ; i < len(p.sellPool); i++ {
					if p.sellPool[i].Id == item.Id {
						p.sellPool = utils.RemoveIndex(p.sellPool, i)
						p.AssetAmtInPool = p.AssetAmtInPool.Sub(item.RemainingAmt)
						break
					}
				}
			} else if item.OrderType == ORDERTYPE_BUY {
				i := 0
				for ; i < len(p.buyPool); i++ {
					if p.buyPool[i].Id == item.Id {
						p.buyPool = utils.RemoveIndex(p.buyPool, i)
						p.SatsValueInPool -= item.RemainingValue
						//p.TotalInputSats -= item.InValue
						break
					}
				}
			}
		}

		if item.Reason == INVOKE_REASON_NORMAL {
			item.Reason = INVOKE_REASON_REFUND
		}
		if item.Done == DONE_NOTYET {
			addItemToMap(item, p.refundMap)
		}

		removeItemFromMap(item, p.swapMap)
	}
}

// 不需要写入数据库的缓存数据，不能修改任何需要保存数据库的变量
func (p *SwapContractRuntime) addItem(item *SwapHistoryItem) {
	if item.Reason == INVOKE_REASON_NORMAL {
		switch item.OrderType {
		case ORDERTYPE_BUY:
			// 插入buyPool，按价格从大到小排序
			// 同样价格，按照区块顺序
			idx := sort.Search(len(p.buyPool), func(i int) bool {
				if p.buyPool[i].UnitPrice.Cmp(item.UnitPrice) == 0 {
					return p.buyPool[i].UtxoId > item.UtxoId
				}
				return p.buyPool[i].UnitPrice.Cmp(item.UnitPrice) < 0
			})
			// 插入到idx位置
			p.buyPool = append(p.buyPool, nil)
			copy(p.buyPool[idx+1:], p.buyPool[idx:])
			p.buyPool[idx] = item
			addItemToMap(item, p.swapMap)

		case ORDERTYPE_SELL:
			// 插入sellPool，按价格从小到大排序
			idx := sort.Search(len(p.sellPool), func(i int) bool {
				if p.sellPool[i].UnitPrice.Cmp(item.UnitPrice) == 0 {
					return p.sellPool[i].UtxoId > item.UtxoId
				}
				return p.sellPool[i].UnitPrice.Cmp(item.UnitPrice) > 0
			})
			p.sellPool = append(p.sellPool, nil)
			copy(p.sellPool[idx+1:], p.sellPool[idx:])
			p.sellPool[idx] = item
			addItemToMap(item, p.swapMap)

		case ORDERTYPE_REFUND:
			addItemToMap(item, p.refundMap)

		case ORDERTYPE_DEPOSIT:
			addItemToMap(item, p.depositMap)

		case ORDERTYPE_WITHDRAW:
			addItemToMap(item, p.withdrawMap)

		case ORDERTYPE_ADDLIQUIDITY:
			addItemToMap(item, p.addLiquidityMap)

		case ORDERTYPE_REMOVELIQUIDITY:
			addItemToMap(item, p.removeLiquidityMap)

		case ORDERTYPE_PROFIT:
			addItemToMap(item, p.profitMap)
		}
	} else {
		addItemToMap(item, p.refundMap)
	}

	p.insertBuck(item)
}

// TODO
func (p *SwapContractRuntime) DisableItem(input InvokeHistoryItem) {
	item, ok := input.(*SwapHistoryItem)
	if !ok {
		return
	}

	switch item.OrderType {

	}

}

// func (p *SwapContractRuntime) AddLostInvokeItem(txId string, fromL1 bool) (string, error) {

// 	url := p.URL()
// 	item := findContractInvokeItem(p.stp.GetDB(), url, txId)
// 	if item != nil {
// 		return item.GetKey(), fmt.Errorf("same item exists")
// 	}

// 	if fromL1 {
// 		txHex, err := p.stp.GetIndexerClient().GetRawTx(txId)
// 		if err != nil {
// 			return "", err
// 		}
// 		tx, err := DecodeMsgTx(txHex)
// 		if err != nil {
// 			return "", err
// 		}
// 		channelUtxosMap := make(map[string]map[string]bool)
// 		contractAddrMap := map[string]bool{
// 			p.Address(): true,
// 		}
// 		invokeTx, err := p.stp.generateInvokeInfoFromTx(tx, contractAddrMap, channelUtxosMap)
// 		if err != nil {
// 			return "", err
// 		}
// 		err = p.stp.fillInvokeInfo(invokeTx)
// 		if err != nil {
// 			return "", err
// 		}
// 		var height int
// 		height, invokeTx.TxIndex, invokeTx.InvokeVout = indexer.FromUtxoId(invokeTx.TxOutput.UtxoId)
// 		item, err := p.Invoke(invokeTx, height)
// 		if err != nil {
// 			return "", err
// 		}
// 		return item.GetKey(), nil

// 	} else {
// 		txHex, err := p.stp.l2IndexerClient.GetRawTx(txId)
// 		if err != nil {
// 			return "", err
// 		}
// 		tx, err := DecodeMsgTx_SatsNet(txHex)
// 		if err != nil {
// 			return "", err
// 		}

// 		invokeTx, err := p.stp.generateInvokeInfoFromTx_SatsNet(tx)
// 		if err != nil {
// 			return "", err
// 		}
// 		err = p.stp.fillInvokeInfo_SatsNet(invokeTx)
// 		if err != nil {
// 			return "", err
// 		}
// 		var height int
// 		height, invokeTx.TxIndex, invokeTx.InvokeVout = indexer.FromUtxoId(invokeTx.TxOutput.UtxoId)
// 		item, err := p.Invoke_SatsNet(invokeTx, height)
// 		if err != nil {
// 			return "", err
// 		}
// 		return item.GetKey(), nil
// 	}
// }

type DealInfo struct {
	SendInfo          map[string]*SendAssetInfo // deposit时，key是item的InUtxo，其他情况是address
	SendTxIdMap       map[string]string         // depoist时使用，key是item的InUtxo
	PreOutputs        []*TxOutput              // 主交易的输入
	AssetName         *swire.AssetName
	TotalAmt          *Decimal // 输出总和
	TotalValue        int64    // 输出总和
	Reason            string
	Height            int
	InvokeCount       int64
	StaticMerkleRoot  []byte
	RuntimeMerkleRoot []byte
	TxId              string
	Fee               int64
}

func (p *SwapContractRuntime) genDealInfo(height int) *DealInfo {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	sendInfoMap := make(map[string]*SendAssetInfo)

	var sendTotalAmt *Decimal // 资产数量
	var sendTotalValue int64  // 交易所需的聪数量

	// 检查remainning为0的记录，发起转账，并记录成交记录
	maxHeight := 0
	for _, buy := range p.buyPool {
		if buy.Done != DONE_NOTYET {
			continue
		}
		h, _, _ := indexer.FromUtxoId(buy.UtxoId)
		if h > height {
			continue
		}
		if buy.RemainingValue == 0 {
			maxHeight = max(maxHeight, h)

			info, ok := sendInfoMap[buy.Address]
			if !ok {
				info = &SendAssetInfo{
					Address:   buy.Address,
					Value:     0,
					AssetName: p.GetAssetName(),
					AssetAmt:  nil,
				}
				sendInfoMap[buy.Address] = info
			}
			info.AssetAmt = info.AssetAmt.Add(buy.OutAmt)
			info.Value += buy.OutValue

			sendTotalAmt = sendTotalAmt.Add(buy.OutAmt)
			sendTotalValue += buy.OutValue

		} else {
			break
		}
	}

	for _, sell := range p.sellPool {
		if sell.Done != DONE_NOTYET {
			continue
		}
		h, _, _ := indexer.FromUtxoId(sell.UtxoId)
		if h > height {
			continue
		}
		if sell.RemainingAmt.Sign() == 0 {
			maxHeight = max(maxHeight, h)

			info, ok := sendInfoMap[sell.Address]
			if !ok {
				info = &SendAssetInfo{
					Address:   sell.Address,
					Value:     0,
					AssetName: p.GetAssetName(),
					AssetAmt:  nil,
				}
				sendInfoMap[sell.Address] = info
			}
			info.AssetAmt = info.AssetAmt.Add(sell.OutAmt)
			info.Value += sell.OutValue

			sendTotalAmt = indexer.DecimalAdd(sendTotalAmt, sell.OutAmt)
			sendTotalValue += sell.OutValue

		} else {
			break
		}
	}

	return &DealInfo{
		SendInfo:          sendInfoMap,
		AssetName:         p.GetAssetName(),
		TotalAmt:          sendTotalAmt,   // 实际买到的
		TotalValue:        sendTotalValue, // 退款
		Reason:            INVOKE_RESULT_DEAL,
		Height:            maxHeight,
		InvokeCount:       p.InvokeCount,
		StaticMerkleRoot:  p.StaticMerkleRoot,
		RuntimeMerkleRoot: p.CurrAssetMerkleRoot,
	}
}

func (p *SwapContractRuntime) deal() error {

	// 发送
	if p.resv.LocalIsInitiator() {
		// 处理买单
		p.mutex.RLock()
		height := p.CurrBlock
		p.mutex.RUnlock()
		dealInfo := p.genDealInfo(height)

		url := p.URL()

		if len(dealInfo.SendInfo) != 0 {
			// 发送费用已经从所有参与者扣除，但如果该交易的聪资产太少，就暂时不发送，等下次
			if dealInfo.TotalValue+indexer.DecimalMul(dealInfo.TotalAmt, p.dealPrice).Int64() >= _valueLimit ||
				len(dealInfo.SendInfo) >= _addressLimit {
				txId, err := p.sendTx_SatsNet(dealInfo, INVOKE_RESULT_DEAL)
				if err != nil {
					p.isSending = false
					Log.Errorf("contract %s sendTx_SatsNet %s failed %v", url, INVOKE_RESULT_DEAL, err)
					// 下个区块再试
					return err
				}
				dealInfo.TxId = txId
				dealInfo.Fee = DEFAULT_FEE_SATSNET

				// record
				//p.updateWithDealInfo(buyInfo, sellInfo, txId, stp.db)
				p.updateWithDealInfo_swap(dealInfo)
				// 成功一步记录一步
				p.stp.SaveReservationWithLock(p.resv)
				Log.Infof("contract %s swap completed, %s", url, txId)
			}
		}

		Log.Debugf("contract %s deal completed", url)
	} else {
		Log.Debugf("server: waiting the deal Tx of contract %s ", p.URL())
	}

	return nil
}

func (p *SwapContractRuntime) updateWithDealInfo_swap(dealInfo *DealInfo) {

	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.TotalDealTx++
	p.TotalDealTxFee += dealInfo.Fee
	p.TotalOutputAssets = p.TotalOutputAssets.Add(dealInfo.TotalAmt)
	p.TotalOutputSats += dealInfo.TotalValue + dealInfo.Fee

	height := dealInfo.Height
	txId := dealInfo.TxId

	updatedAddressMap := make(map[string]bool)
	i := 0
	for i < len(p.buyPool) {
		buy := p.buyPool[i]
		h, _, _ := indexer.FromUtxoId(buy.UtxoId)
		if h > height {
			i++
			continue
		}
		if buy.Reason != INVOKE_REASON_NORMAL {
			continue
		}
		// 顺序处理
		if buy.RemainingValue != 0 {
			break
		}
		sendInfo, ok := dealInfo.SendInfo[buy.Address]
		if !ok {
			break
		}
		// 将sendInfo的相关资产数量消耗光为止
		// buy 对应输出资产数量
		if sendInfo.AssetAmt.Cmp(buy.OutAmt) < 0 {
			// 不够消耗，已经超出Tx对应的范围
			break
		}

		if buy.Done == DONE_NOTYET {
			// 这条记录已经完全处理完成
			sendInfo.AssetAmt = sendInfo.AssetAmt.Sub(buy.OutAmt)
			sendInfo.Value -= buy.OutValue

			buy.OutTxId = txId
			buy.Done = DONE_DEALT
			delete(p.history, buy.InUtxo)
			SaveContractInvokeHistoryItem(p.stp.GetDB(), p.URL(), buy)
			trader, ok := p.traderInfoMap[buy.Address]
			if ok {
				onBuyValue := buy.InValue - CalcSwapFee(buy.InValue)
				trader.DealValue += onBuyValue - buy.OutValue
				trader.OnBuyValue -= onBuyValue
				trader.UpdateTime = time.Now().Unix()
				removeItemFromMap(buy, p.swapMap)
				updatedAddressMap[buy.Address] = true
			}
		}

		p.buyPool = utils.RemoveIndex(p.buyPool, i)
	}
	if len(p.buyPool) != 0 {
		p.HighestBuyPrice = p.buyPool[0].UnitPrice
	} else {
		p.HighestBuyPrice = nil
	}

	i = 0
	for i < len(p.sellPool) {
		sell := p.sellPool[i]
		h, _, _ := indexer.FromUtxoId(sell.UtxoId)
		if h > height {
			i++
			continue
		}
		if sell.Reason != INVOKE_REASON_NORMAL {
			continue
		}
		if sell.RemainingAmt.Sign() != 0 {
			break
		}
		// 顺序处理
		sendInfo, ok := dealInfo.SendInfo[sell.Address]
		if !ok {
			break
		}
		// 将sendInfo的相关资产数量消耗光为止
		// sell 对应输出聪数量
		if sendInfo.Value < sell.OutValue {
			// 不够消耗，已经超出Tx对应的范围
			break
		}

		if sell.Done == DONE_NOTYET {
			// 这条记录已经完全处理完成
			sendInfo.AssetAmt = sendInfo.AssetAmt.Sub(sell.OutAmt)
			sendInfo.Value -= sell.OutValue

			sell.OutTxId = txId
			sell.Done = DONE_DEALT
			delete(p.history, sell.InUtxo)
			SaveContractInvokeHistoryItem(p.stp.GetDB(), p.URL(), sell)
			trader, ok := p.traderInfoMap[sell.Address]
			if ok {
				trader.DealAmt = indexer.DecimalAdd(trader.DealAmt, sell.InAmt).Sub(sell.OutAmt)
				trader.OnSaleAmt = trader.OnSaleAmt.Sub(sell.InAmt)
				trader.UpdateTime = time.Now().Unix()
				removeItemFromMap(sell, p.swapMap)
				updatedAddressMap[sell.Address] = true
			}
		}

		p.sellPool = utils.RemoveIndex(p.sellPool, i)
	}
	if len(p.sellPool) != 0 {
		p.LowestSellPrice = p.sellPool[0].UnitPrice
	} else {
		p.LowestSellPrice = nil
	}

	for k := range updatedAddressMap {
		trader, ok := p.traderInfoMap[k]
		if ok {
			saveContractInvokerStatus(p.stp.GetDB(), p.URL(), trader)
		}
	}

	p.CheckPoint = dealInfo.InvokeCount
	p.AssetMerkleRoot = dealInfo.RuntimeMerkleRoot
	p.CheckPointBlock = p.CurrBlock
	p.CheckPointBlockL1 = p.CurrBlockL1

	p.refreshTime_swap = 0

	//p.checkSelf()
}

// 执行交换，每个区块统一执行一次
func (p *SwapContractRuntime) swap() error {

	if p.GetTemplateName() != TEMPLATE_CONTRACT_SWAP {
		return nil
	}
	if len(p.buyPool) == 0 && len(p.sellPool) == 0 {
		return nil
	}

	Log.Debugf("%s start contract %s with action swap, buy %d, sell %d", p.stp.GetMode(), p.URL(), len(p.buyPool), len(p.sellPool))

	url := p.URL()
	buyPool := p.buyPool
	sellPool := p.sellPool
	updated := false
	// 撮合
	i := 0
	j := 0
	for i < len(buyPool) && j < len(sellPool) {
		buy := buyPool[i]
		sell := sellPool[j]

		if buy.Reason != INVOKE_REASON_NORMAL {
			i++
			continue
		}
		if buy.RemainingValue == 0 {
			i++
			continue
		}
		if sell.Reason != INVOKE_REASON_NORMAL {
			j++
			continue
		}
		if sell.RemainingAmt.Sign() == 0 {
			j++
			continue
		}

		// 买价 >= 卖价才可成交，以卖价为准
		if buy.UnitPrice.Cmp(sell.UnitPrice) < 0 {
			break // 后面卖单价格更高，直接break
		}
		p.dealPrice = sell.UnitPrice.Clone()
		// unitPrice := sell.UnitPrice
		// 成交数量 = min(买单剩余, 卖单剩余)

		// 因为聪的精度问题，这里需要计算花费多少聪买多少份资产，但剩余的资产或者聪已经无法交易，只能退款
		var matchAmt *Decimal
		var matchValue int64
		var buyExhausted, sellExhausted bool
		sellValue := indexer.DecimalMul(sell.RemainingAmt, sell.UnitPrice)
		if buy.RemainingValue <= sellValue.Int64() {
			toBuy := indexer.NewDecimal(buy.RemainingValue, sell.RemainingAmt.Precision) // 保持资产精度
			matchAmt = indexer.DecimalDiv(toBuy, sell.UnitPrice)                         // 保持资产精度
			matchValue = indexer.DecimalMul(sell.UnitPrice, matchAmt).Ceil()             // 回到聪的精度
			buyExhausted = true
		} else {
			toSell := indexer.DecimalMul(sell.RemainingAmt, sell.UnitPrice)
			matchValue = toSell.Ceil()                            // 保持资产精度
			matchAmt = indexer.DecimalDiv(toSell, sell.UnitPrice) // 保持资产精度
			sellExhausted = true
		}
		if buy.ExpectedAmt.Sign() != 0 && buy.ExpectedAmt.Cmp(indexer.DecimalAdd(buy.OutAmt, matchAmt)) < 0 {
			matchAmt = indexer.DecimalSub(buy.ExpectedAmt, buy.OutAmt)
			matchValue = indexer.DecimalMul(sell.UnitPrice, matchAmt).Ceil()
			if matchValue == 0 {
				// 不足一聪，这个订单异常，如果直接取消，对双方都不利，只能帮买家用最小一聪的成本，买入对应的资产
				matchValue = 1
				toBuy := indexer.NewDecimal(matchValue, p.Divisibility)
				matchAmt = indexer.DecimalDiv(toBuy, sell.UnitPrice)
			}
			buyExhausted = true
			sellExhausted = false
		}

		// 更新剩余数量
		buy.RemainingValue -= matchValue
		buy.OutAmt = buy.OutAmt.Add(matchAmt)
		if !buyExhausted {
			// 进一步判断
			toBuy := indexer.NewDecimal(buy.RemainingValue, sell.RemainingAmt.Precision)
			newMatch := indexer.DecimalDiv(toBuy, sell.UnitPrice)
			if newMatch.Sign() == 0 {
				buyExhausted = true
			}
			if !buyExhausted {
				if buy.ExpectedAmt.Cmp(buy.OutAmt) == 0 {
					buyExhausted = true
				}
			}
		}
		if buyExhausted {
			// 买不到一单位资产，或者买够数量了
			buy.OutValue = buy.RemainingValue
			buy.RemainingValue = 0
			i++
		}

		sell.RemainingAmt = sell.RemainingAmt.Sub(matchAmt)
		sell.OutValue += matchValue
		if !sellExhausted {
			// 进一步判断
			toSell := indexer.DecimalMul(sell.RemainingAmt, sell.UnitPrice)
			newValue := toSell.Int64()
			if newValue == 0 {
				sellExhausted = true
			}
		}
		if sellExhausted {
			// 卖价不足一聪
			sell.OutAmt = sell.RemainingAmt
			sell.RemainingAmt = nil
			j++
		}

		// 更新统计数据
		p.AssetAmtInPool = p.AssetAmtInPool.Sub(matchAmt)
		if sellExhausted {
			p.AssetAmtInPool = p.AssetAmtInPool.Sub(sell.OutAmt)
		}
		p.SatsValueInPool -= matchValue
		Log.Infof("SatsValueInPool sub %d to %d with item %d\n", matchValue, p.SatsValueInPool, buy.Id)

		if buyExhausted {
			p.SatsValueInPool -= buy.OutValue
			Log.Infof("SatsValueInPool sub %d to %d with item %d\n", buy.OutValue, p.SatsValueInPool, buy.Id)
		}
		p.TotalDealCount++
		p.TotalDealAssets = p.TotalDealAssets.Add(matchAmt)
		p.TotalDealSats += matchValue
		p.LastDealPrice = sell.UnitPrice
		if p.HighestDealPrice.Cmp(p.LastDealPrice) < 0 {
			p.HighestDealPrice = p.LastDealPrice.Clone()
		}
		if p.LowestDealPrice == nil || p.LowestDealPrice.Cmp(p.LastDealPrice) > 0 {
			p.LowestDealPrice = p.LastDealPrice.Clone()
		}

		SaveContractInvokeHistoryItem(p.stp.GetDB(), url, buy)
		SaveContractInvokeHistoryItem(p.stp.GetDB(), url, sell)

		updated = true

		Log.Infof("Order matched: amt=%s, value=%d, price=%s, BUY %s <-> SELL %s, ",
			matchAmt.String(), matchValue, sell.UnitPrice.String(), buy.InUtxo, sell.InUtxo)
	}

	// 交易的结果先保存
	if updated {
		p.stp.SaveReservation(p.resv)
		// if p.InvokeCount%100 == 0 {
		// 	p.checkSelf()
		// }
	}

	// 执行发送和退款。可能会失败，失败后，等下个区块再尝试

	return nil
}

// 涉及发送各种tx，运行在线程中
func (p *SwapContractRuntime) sendInvokeResultTx_SatsNet() error {
	if p.resv.LocalIsInitiator() {
		if p.isSending {
			return nil
		}
		p.isSending = true
		url := p.URL()

		err := p.deal()
		if err != nil {
			Log.Errorf("contract %s deal failed, %v", url, err)
		}

		// 执行退款
		err = p.refund()
		if err != nil {
			Log.Errorf("contract %s refund failed, %v", url, err)
		}

		// 先存，再提
		err = p.deposit()
		if err != nil {
			Log.Errorf("contract %s deposit failed, %v", url, err)
		}

		err = p.withdraw()
		if err != nil {
			Log.Errorf("contract %s withdraw failed, %v", url, err)
		}

		err = p.retrieve()
		if err != nil {
			Log.Errorf("contract %s retrieve failed, %v", url, err)
		}

		err = p.profit()
		if err != nil {
			Log.Errorf("contract %s profit failed, %v", url, err)
		}

		p.isSending = false
		Log.Debugf("contract %s sendInvokeResultTx_SatsNet completed", url)
	} else {
		//Log.Infof("server: waiting the deal Tx of contract %s ", p.URL())
	}
	return nil
}

func (p *SwapContractRuntime) sendInvokeResultTx() error {
	return p.sendInvokeResultTx_SatsNet()
}

func (p *ContractRuntimeBase) buildDepositAnchorTx(output *indexer.TxOutput, destAddr string,
	height int, reason string) (*swire.MsgTx, error) {
	//
	var assetName *AssetName
	if len(output.Assets) == 0 {
		assetName = &AssetName{
			AssetName: indexer.ASSET_PLAIN_SAT,
			N:         1,
		}
	} else {
		assetName = &AssetName{
			AssetName: output.Assets[0].Name,
			N:         int(output.Assets[0].BindingSat),
		}
	}

	invoice, _ := UnsignedContractResultInvoice(p.URL(), reason, fmt.Sprintf("%d", height))
	nullDataScript, _ := sindexer.NullDataScript(sindexer.CONTENT_TYPE_INVOKERESULT, invoice)

	anchorTx, err := p.stp.CreateContractDepositAnchorTx(p.runtime, destAddr, output, assetName, nullDataScript)
	if err != nil {
		Log.Errorf("CreateContractDepositAnchorTx %s %s failed, %v", assetName.String(), output.OutPointStr, err)
		return nil, err
	}

	return anchorTx, nil
}

func (p *ContractRuntimeBase) notifyAndSendDepositAnchorTxs(anchorTxs []*swire.MsgTx) error {
	// 通知peer, 仅仅为了同步更新merkle root
	localKey := p.stp.GetWallet().GetPaymentPubKey().SerializeCompressed()
	peerPubKey := p.stp.GetServerNodePubKey().SerializeCompressed()
	witness, _, err := GetP2WSHscript(localKey, peerPubKey)
	if err != nil {
		Log.Errorf("GetP2WSHscript failed, %v", err)
		return err
	}

	var txs []*wwire.TxSignInfo
	for _, tx := range anchorTxs {
		txHex, err := EncodeMsgTx_SatsNet(tx)
		if err != nil {
			Log.Errorf("EncodeMsgTx_SatsNet failed, %v", err)
			return err
		}
		txs = append(txs, &wwire.TxSignInfo{
			Tx:        txHex,
			L1Tx:      false,
			LocalSigs: nil,
			Reason:    "ascend",
		})
	}

	moredata := wwire.RemoteSignMoreData_Contract{
		Tx:                txs,
		LocalPubKey:       localKey,
		Witness:           witness,
		ContractURL:       p.URL(),
		InvokeCount:       p.InvokeCount,
		StaticMerkleRoot:  p.StaticMerkleRoot,
		RuntimeMerkleRoot: p.CurrAssetMerkleRoot,
		Action:            INVOKE_API_DEPOSIT,
	}
	md, err := json.Marshal(moredata)
	if err != nil {
		Log.Errorf("Marshal failed, %v", err)
		return err
	}

	req := wwire.SignRequest{
		ChannelId:    p.ChannelAddr,
		CommitHeight: -1,
		Reason:       "contract",
		MoreData:     md,
		PubKey:       localKey,
	}
	msg, err := json.Marshal(req)
	if err != nil {
		return err
	}
	sig, err := p.stp.GetWalletMgr().SignMessage(msg)
	if err != nil {
		return err
	}

	_, err = p.stp.SendSigReq(&req, sig)
	if err != nil {
		Log.Errorf("SendBootstrapSigReq failed. %v", err)
		return err
	}

	// peer已经广播过，这个再次广播
	for _, tx := range anchorTxs {
		_, err := p.stp.BroadcastTx_SatsNet(tx)
		if err != nil && strings.Contains(err.Error(), "can't find utxo") {
			// -26: TX rejected: The anchor tx is invalid 3e4946319fc0facddbbda06b0153c8fe2be9dffe5db469ef5566e5325f661197:http://127.0.0.1:8023/btc/testnet/v3/utxo/info/77e2027afa5cf8fb90dfc41c22ba0184c1450b6640c26b96148fb77f49d226ff:1 response failed: can't find utxo 77e2027afa5cf8fb90dfc41c22ba0184c1450b6640c26b96148fb77f49d226ff:1
			parts := strings.Split(err.Error(), "can't find utxo")
			if len(parts) == 2 {
				utxo := strings.TrimSpace(parts[1])
				item, ok := p.history[utxo]
				if ok {
					h, _, _ := indexer.FromUtxoId(item.UtxoId)
					if h+6 < p.CurrBlockL1 {
						// 这个输入无效，这条记录设置为异常，不再重试
						Log.Errorf("deposit detect an invalid item %d, set to invalid. ", item.Id)
						item.Reason = INVOKE_REASON_UTXO_NOT_FOUND
						item.Done = DONE_CLOSED_DIRECTLY
					}
				}
			}
		}
	}

	return nil
}

func (p *ContractRuntimeBase) sendTx_SatsNet(dealInfo *DealInfo, reason string) (string, error) {
	sendInfoMap := dealInfo.SendInfo
	height := dealInfo.Height

	sendInfoVect := make([]*SendAssetInfo, 0, len(sendInfoMap))
	for _, v := range sendInfoMap {
		if v.AssetAmt.IsZero() && v.Value == 0 {
			continue
		}
		sendInfoVect = append(sendInfoVect, v)
	}

	sort.Slice(sendInfoVect, func(i, j int) bool {
		return sendInfoVect[i].Address < sendInfoVect[j].Address
	})

	invoice, _ := UnsignedContractResultInvoice(p.URL(), reason, fmt.Sprintf("%d", height))
	nullDataScript, _ := sindexer.NullDataScript(sindexer.CONTENT_TYPE_INVOKERESULT, invoice)

	url := p.URL()
	var txId string
	var err error
	for i := 0; i < 3; i++ {
		txId, err = p.stp.CoBatchSendV2_SatsNet(sendInfoVect, dealInfo.AssetName.String(),
			"contract", url, dealInfo.InvokeCount, nullDataScript,
			dealInfo.StaticMerkleRoot, dealInfo.RuntimeMerkleRoot)
		if err != nil {
			if strings.Contains(err.Error(), ERR_MERKLE_ROOT_INCONSISTENT) {
				// recalc
				Log.Infof("contract %s CoBatchSendV2_SatsNet failed %v, recalculate merkle root and try again", url, err)
				p.calcAssetMerkleRoot()
			} else {
				Log.Errorf("contract %s CoBatchSendV2_SatsNet failed %v, wait a second and try again", url, err)
				// 服务端可能还没有同步到数据，需要多尝试几次，但不要卡太久
				//time.Sleep(2 * time.Second)
				continue
			}
		}
		Log.Infof("contract %s CoBatchSendV2_SatsNet txId %s", url, txId)
		break
	}
	if err != nil {
		Log.Errorf("contract %s CoBatchSendV2_SatsNet failed %v", url, err)
		// 下个区块再试
		return "", err
	}
	saveContractInvokeResult(p.stp.GetDB(), url, txId, reason)

	return txId, nil
}

func (p *ContractRuntimeBase) sendTx(dealInfo *DealInfo,
	reason string, sendDeAnchorTx bool, excludeRecentBlock bool) (string, int64, int64, error) {
	//
	sendInfoMap := dealInfo.SendInfo
	height := dealInfo.Height
	stubNum := 0
	stubs := make([]string, 0)
	// 分开发送
	sendInfoVect := make([]*SendAssetInfo, 0, len(sendInfoMap))
	sendInfoVectWithStub := make([]*SendAssetInfo, 0, len(sendInfoMap))
	for _, v := range sendInfoMap {
		if v.AssetAmt.IsZero() && v.Value == 0 {
			continue
		}

		if v.AssetName.Protocol == indexer.PROTOCOL_NAME_ORDX {
			if indexer.GetBindingSatNum(v.AssetAmt, uint32(p.N)) < 330 {
				stubNum++
				sendInfoVectWithStub = append(sendInfoVectWithStub, v)
				continue
			}
		}
		sendInfoVect = append(sendInfoVect, v)
	}

	if len(sendInfoVect) == 0 && len(sendInfoVectWithStub) == 0 {
		return "", 0, 0, fmt.Errorf("no send info")
	}

	sort.Slice(sendInfoVect, func(i, j int) bool {
		return sendInfoVect[i].Address < sendInfoVect[j].Address
	})

	url := p.URL()
	var err error
	var stubFee int64
	if stubNum != 0 { // 前面处理过，最多一个
		stubs, err = p.stp.GetWalletMgr().GetUtxosForStubs(p.Address(), stubNum, nil)
		if err != nil {
			// 重新生成一堆
			stubTx, fee, err := p.stp.CoGenerateStubUtxos(stubNum+10, p.URL(), dealInfo.InvokeCount, excludeRecentBlock)
			if err != nil {
				Log.Errorf("CoGenerateStubUtxos %d failed, %v", stubNum+10, err)
				return "", 0, 0, err
			}
			Log.Infof("sendTx CoGenerateStubUtxos %s %d", stubTx, fee)
			for i := range stubNum {
				stubs = append(stubs, fmt.Sprintf("%s:%d", stubTx, i))
			}
			stubFee = fee
			saveContractInvokeResult(p.stp.GetDB(), url, stubTx, "stub")
		}
	}

	var fee int64
	var txId string
	invoice, _ := UnsignedContractResultInvoice(p.RelativePath(), reason, fmt.Sprintf("%d", height))
	nullDataScript, _ := sindexer.NullDataScript(sindexer.CONTENT_TYPE_INVOKERESULT, invoice)
	if len(sendInfoVect) > 0 {
		for i := 0; i < 3; i++ {
			txId, fee, err = p.stp.CoBatchSendV3(sendInfoVect, dealInfo.AssetName.String(),
				"contract", url, dealInfo.InvokeCount, nullDataScript,
				dealInfo.StaticMerkleRoot, dealInfo.RuntimeMerkleRoot, sendDeAnchorTx, excludeRecentBlock)
			if err != nil {
				if strings.Contains(err.Error(), ERR_MERKLE_ROOT_INCONSISTENT) {
					// recalc
					Log.Infof("contract %s CoBatchSendV3 failed %v, recalculate merkle root and try again", url, err)
					p.calcAssetMerkleRoot()
				} else {
					Log.Errorf("contract %s CoBatchSendV3 failed %v, wait a second and try again", url, err)
					// 服务端可能还没有同步到数据，需要多尝试几次，但不要卡太久
					//time.Sleep(2 * time.Second)
					continue
				}
			}
			Log.Infof("contract %s CoBatchSendV3 %s %d", url, txId, fee)
			break
		}
		if err != nil {
			return "", 0, stubFee, err
		}
	} else {
		if stubNum != 0 {
			p.stp.GetWalletMgr().utxoLockerL1.LockUtxo(stubs[0], "stub for "+reason)
			// 这里只有一个交易
			if len(sendInfoVectWithStub) > 1 {
				return "", 0, stubFee, fmt.Errorf("only one output in stub is supported")
			}
			sendInfo := sendInfoVectWithStub[0]
			for i := 0; i < 3; i++ {
				txId, fee, err = p.stp.CoSendOrdxWithStub(sendInfo.Address, sendInfo.AssetName.String(), sendInfo.AssetAmt.Int64(),
					stubs[0], "contract", url, dealInfo.InvokeCount, nullDataScript,
					dealInfo.StaticMerkleRoot, dealInfo.RuntimeMerkleRoot, sendDeAnchorTx, excludeRecentBlock)
				if err != nil {
					if strings.Contains(err.Error(), ERR_MERKLE_ROOT_INCONSISTENT) {
						// recalc
						Log.Infof("contract %s CoSendOrdxWithStub failed %v, recalculate merkle root and try again", url, err)
						p.calcAssetMerkleRoot()
					} else {
						Log.Errorf("contract %s CoSendOrdxWithStub failed %v, wait a second and try again", url, err)
						// 服务端可能还没有同步到数据，需要多尝试几次，但不要卡太久
						//time.Sleep(2 * time.Second)
						continue
					}
				}
				Log.Infof("contract %s CoSendOrdxWithStub txId %s %d", url, txId, fee)
				break

			}
			if err != nil {
				p.stp.GetWalletMgr().utxoLockerL1.UnlockUtxo(stubs[0])
				return "", 0, stubFee, err
			}
		}
	}

	saveContractInvokeResult(p.stp.GetDB(), url, txId, reason)
	return txId, fee, stubFee, nil
}

func (p *ContractRuntimeBase) genSendInfoFromTx_SatsNet(tx *swire.MsgTx, includedAll bool) (*DealInfo, error) {

	dealInfo := &DealInfo{
		SendInfo: make(map[string]*SendAssetInfo),
		TxId:     tx.TxID(),
		Fee:      DEFAULT_FEE_SATSNET,
	}

	assetName := p.contract.GetAssetName()
	isPlainAsset := indexer.IsPlainAsset(assetName)
	for i, txOut := range tx.TxOut {
		if sindexer.IsOpReturn(txOut.PkScript) {
			ctype, data, err := sindexer.ReadDataFromNullDataScript(txOut.PkScript)
			if err == nil {
				switch ctype {
				case sindexer.CONTENT_TYPE_INVOKERESULT:
					url, r, h, err := ParseContractResultInvoice(data)
					if err != nil {
						Log.Errorf("ParseContractResultInvoice failed, %v", err)
						return nil, err
					}
					if url != p.URL() {
						if url != p.RelativePath() {
							return nil, fmt.Errorf("%s not expected contract invoke result tx %s", url, tx.TxID())
						}
					}
					height, err := strconv.ParseInt(h, 10, 32)
					if err != nil {
						return nil, err
					}

					dealInfo.Height = int(height)
					dealInfo.Reason = r
				}
			}
		}

		addr, err := AddrFromPkScript(txOut.PkScript)
		if err != nil {
			addr = ADDR_OPRETURN
		}

		if !includedAll {
			if addr == ADDR_OPRETURN || addr == p.ChannelAddr {
				continue
			}
		}

		output := sindexer.GenerateTxOutput(tx, i)
		// 在计算utxo中的空白聪时，需要和发送方一致：总聪-资产占有的空白聪。不能直接使用GetPlainSat，因为会为资产多预留了一聪
		value := output.Value() - output.SizeOfBindingSats()
		amt := output.GetAsset(assetName)
		if isPlainAsset {
			amt = nil
		}
		info := SendAssetInfo{
			Address:   addr,
			Value:     value,
			AssetName: assetName,
			AssetAmt:  amt,
		}
		old, ok := dealInfo.SendInfo[addr]
		if ok {
			old.AssetAmt = old.AssetAmt.Add(info.AssetAmt)
			old.Value += info.Value
		} else {
			dealInfo.SendInfo[addr] = &info
		}
		dealInfo.TotalAmt = dealInfo.TotalAmt.Add(amt)
		dealInfo.TotalValue += info.Value
	}
	dealInfo.AssetName = assetName

	return dealInfo, nil
}

// 因为不清楚具体是哪一种资产，这里按照默认的优先级选择资产： ft>names>nft, ordx>runes
func (p *ContractRuntimeBase) GetAssetInfoFromOutput(output *TxOutput) (*indexer.AssetName, *Decimal, int, error) {
	var assetAmt *Decimal
	var assetName *indexer.AssetName
	divisibility := 0
	if len(output.Assets) == 0 {
		assetAmt = indexer.NewDefaultDecimal(output.Value())
		assetName = &indexer.ASSET_PLAIN_SAT
	} else if len(output.Assets) == 1 {
		// 不管什么资产，都先上
		assetName = &output.Assets[0].Name
		assetAmt = output.Assets[0].Amount.Clone()
		tickInfo := p.stp.GetTickerInfo(assetName)
		if tickInfo == nil {
			return nil, nil, 0, fmt.Errorf("can't find tick %s", assetName.String())
		}
		divisibility = tickInfo.Divisibility
	} else {
		var asset *indexer.AssetInfo
		for _, a := range output.Assets {
			switch a.Name.Type {
			case indexer.ASSET_TYPE_FT:
				if asset == nil {
					asset = &a
				} else {
					if asset.Name.Protocol == a.Name.Protocol {
						if asset.Amount.Cmp(&a.Amount) < 0 {
							asset = &a
						}
					} else {
						if a.Name.Protocol == indexer.PROTOCOL_NAME_ORDX {
							asset = &a
						}
					}
				}

			case indexer.ASSET_TYPE_EXOTIC:
				if asset == nil {
					asset = &a
				}
			}
		}
		tickInfo := p.stp.GetTickerInfo(&asset.Name)
		if tickInfo == nil {
			return nil, nil, 0, fmt.Errorf("can't find tick %s", asset.Name.String())
		}
		divisibility = tickInfo.Divisibility

		assetName = &asset.Name
		assetAmt = asset.Amount.Clone()
	}
	return assetName, assetAmt, divisibility, nil
}

func (p *ContractRuntimeBase) genSendInfoFromTx(tx *wire.MsgTx, preFectcher map[string]*TxOutput, 
	moreData []byte) (*DealInfo, error) {

	dealInfo := &DealInfo{
		SendInfo: make(map[string]*SendAssetInfo),
		TxId:     tx.TxID(),
	}

	inputs, outputs, err := p.stp.GetWalletMgr().RebuildTxOutput(tx, preFectcher)
	if err != nil {
		Log.Errorf("rebuildTxOutput %s failed, %v", tx.TxID(), err)
		return nil, err
	}
	dealInfo.PreOutputs = inputs

	var in, out int64
	for _, input := range inputs {
		in += input.Value()
	}

	assetName := p.contract.GetAssetName()
	isPlainAsset := indexer.IsPlainAsset(assetName)
	for i, txOut := range tx.TxOut {
		out += txOut.Value
		if sindexer.IsOpReturn(txOut.PkScript) {
			ctype, data, err := sindexer.ReadDataFromNullDataScript(txOut.PkScript)
			if err != nil {
				// 存在符文的情况下，只能传递moreData
				if len(moreData) != 0 {
					ctype, data, err = sindexer.ReadDataFromNullDataScript(moreData)
					if err != nil {
						return nil, fmt.Errorf("%s invalid more data", p.URL())
					}
				}
			}
			switch ctype {
			case sindexer.CONTENT_TYPE_INVOKERESULT:
				url, r, h, err := ParseContractResultInvoice(data)
				if err != nil {
					Log.Errorf("ParseContractResultInvoice failed, %v", err)
					return nil, err
				}
				if url != p.URL() {
					if url != p.RelativePath() {
						return nil, fmt.Errorf("%s not expected contract invoke result tx %s", url, tx.TxID())
					}
				}
				height, err := strconv.ParseInt(h, 10, 32)
				if err != nil {
					return nil, err
				}

				dealInfo.Height = int(height)
				dealInfo.Reason = r
			}

		} else {
			addr, err := AddrFromPkScript(txOut.PkScript)
			if err != nil {
				Log.Errorf("AddrFromPkScript failed, %v", err)
				return nil, err
			}
			if addr == p.ChannelAddr {
				continue
			}

			// 主网有资产的utxo，其value就当作0
			output := outputs[i]
			plainSat := int64(0)
			amt := output.GetAsset(assetName)
			if isPlainAsset {
				plainSat = output.GetPlainSat()
				amt = nil
			}
			info := SendAssetInfo{
				Address:   addr,
				Value:     plainSat,
				AssetName: assetName,
				AssetAmt:  amt,
			}
			old, ok := dealInfo.SendInfo[addr]
			if ok {
				old.AssetAmt = old.AssetAmt.Add(info.AssetAmt)
				old.Value += info.Value
			} else {
				dealInfo.SendInfo[addr] = &info
			}
			dealInfo.TotalAmt = dealInfo.TotalAmt.Add(amt)
			dealInfo.TotalValue += info.Value
		}
	}
	dealInfo.Fee = in - out
	dealInfo.AssetName = assetName

	return dealInfo, nil
}

func (p *SwapContractRuntime) genRefundInfo(height int) *DealInfo {

	p.mutex.RLock()
	defer p.mutex.RUnlock()

	var totalRefundAmt *Decimal // 资产数量
	var totalRefundValue int64  // 交易所需的聪数量

	sendInfoMap := make(map[string]*SendAssetInfo) // key: address
	maxHeight := 0
	for _, refundMap := range p.refundMap {

		for _, item := range refundMap {
			h, _, _ := indexer.FromUtxoId(item.UtxoId)
			if h > height {
				continue
			}
			if item.Done != DONE_NOTYET {
				continue
			}
			maxHeight = max(maxHeight, h)
			if item.RemainingAmt.IsZero() && item.RemainingValue == 0 {
				// 不需要更新什么数据
				continue
			}
			if item.OrderType == ORDERTYPE_REFUND {
				// 不需要更新什么数据
				continue
			}

			info, ok := sendInfoMap[item.Address]
			if !ok {
				info = &SendAssetInfo{
					Address:   item.Address,
					Value:     0,
					AssetName: p.GetAssetName(),
					AssetAmt:  nil,
				}
				sendInfoMap[item.Address] = info
			}

			amt := indexer.DecimalAdd(item.RemainingAmt, item.OutAmt)
			value := item.RemainingValue + item.OutValue

			info.AssetAmt = info.AssetAmt.Add(amt)
			info.Value += value

			// 只有remain部分才是refund的，但是如果从最后的TX来看，无法分清，所以就当作全部都是refund的
			// totalRefundAmt = totalRefundAmt.Add(item.RemainingAmt)
			// totalRefundValue += item.RemainingValue
			totalRefundAmt = totalRefundAmt.Add(amt)
			totalRefundValue += value
		}

	}

	return &DealInfo{
		SendInfo:          sendInfoMap,
		AssetName:         p.GetAssetName(),
		TotalAmt:          totalRefundAmt,
		TotalValue:        totalRefundValue,
		Reason:            INVOKE_RESULT_REFUND,
		Height:            maxHeight,
		InvokeCount:       p.InvokeCount,
		StaticMerkleRoot:  p.StaticMerkleRoot,
		RuntimeMerkleRoot: p.CurrAssetMerkleRoot,
	}
}

func (p *SwapContractRuntime) updateWithDealInfo_refund(dealInfo *DealInfo) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.TotalRefundAssets = p.TotalRefundAssets.Add(dealInfo.TotalAmt)
	p.TotalRefundSats += dealInfo.TotalValue
	p.TotalRefundTx++
	p.TotalRefundTxFee += dealInfo.Fee
	p.TotalOutputAssets = p.TotalOutputAssets.Add(dealInfo.TotalAmt)
	p.TotalOutputSats += dealInfo.TotalValue + dealInfo.Fee

	url := p.URL()
	height := dealInfo.Height
	txId := dealInfo.TxId

	for k, info := range dealInfo.SendInfo {
		refundMap, ok := p.refundMap[k]
		if ok {
			deleted := make([]int64, 0)
			for _, item := range refundMap {
				h, _, _ := indexer.FromUtxoId(item.UtxoId)
				if h > height {
					continue
				}
				if item.Done == DONE_NOTYET {
					if item.OrderType == ORDERTYPE_REFUND {
						// 指令
						item.Done = DONE_DEALT
					} else {
						// 数据
						item.Done = DONE_REFUNDED
						if item.RemainingValue != 0 {
							item.OutValue += item.RemainingValue
							item.RemainingValue = 0
						}
						if item.RemainingAmt.Sign() != 0 {
							item.OutAmt = item.OutAmt.Add(item.RemainingAmt)
							item.RemainingAmt = nil
						}
					}
					item.OutTxId = txId
				} // 设置为取消，或者直接关闭的item，也需要保存
				SaveContractInvokeHistoryItem(p.stp.GetDB(), url, item)
				deleted = append(deleted, item.Id)
				delete(p.history, item.InUtxo)
			}

			for _, id := range deleted {
				delete(refundMap, id)
			}

			trader := p.loadTraderInfo(k)
			if trader != nil {
				trader.RefundAmt = trader.RefundAmt.Add(info.AssetAmt)
				trader.RefundValue += info.Value
				saveContractInvokerStatus(p.stp.GetDB(), url, trader)
			}
		}
	}

	// 更新refundMap
	deletedAddr := make([]string, 0)
	for k, v := range p.refundMap {
		deleted := make([]int64, 0)
		for id, item := range v {
			h, _, _ := indexer.FromUtxoId(item.UtxoId)
			if h > height {
				continue
			}
			deleted = append(deleted, id)
			// item在上面已经更新
		}
		for _, id := range deleted {
			delete(v, id)
		}
		if len(v) == 0 {
			deletedAddr = append(deletedAddr, k)
		}
	}
	for _, addr := range deletedAddr {
		delete(p.refundMap, addr)
	}

	p.CheckPoint = dealInfo.InvokeCount
	p.AssetMerkleRoot = dealInfo.RuntimeMerkleRoot
	p.CheckPointBlock = p.CurrBlock
	p.CheckPointBlockL1 = p.CurrBlockL1

	p.refreshTime_swap = 0
}

// 退款。（在撮合交易之后进行）
func (p *SwapContractRuntime) refund() error {

	// 发送
	if p.resv.LocalIsInitiator() {
		if len(p.refundMap) == 0 {
			return nil
		}

		Log.Debugf("%s start contract %s with action refund", p.stp.GetMode(), p.URL())

		p.mutex.RLock()
		height := p.CurrBlock
		p.mutex.RUnlock()
		refundInfo := p.genRefundInfo(height)
		// 发送
		if len(refundInfo.SendInfo) != 0 {
			// 发送费用已经从所有参与者扣除，但如果该交易的聪资产太少，就暂时不发送，等下次
			if refundInfo.TotalValue >= _valueLimit || len(refundInfo.SendInfo) >= _addressLimit {
				txId, err := p.sendTx_SatsNet(refundInfo, INVOKE_RESULT_REFUND)
				if err != nil {
					Log.Errorf("contract %s sendTx_SatsNet %s failed %v", p.URL(), INVOKE_RESULT_REFUND, err)
					// 下个区块再试
					return err
				}
				refundInfo.TxId = txId
				refundInfo.Fee = DEFAULT_FEE_SATSNET

				// record
				p.updateWithDealInfo_refund(refundInfo)
				// 成功一步记录一步
				p.stp.SaveReservationWithLock(p.resv)

				Log.Infof("contract %s refund completed, %s", p.URL(), txId)
			}
		}

		//Log.Debugf("contract %s refund completed", p.URL())

	} else {
		Log.Debugf("server: waiting the refund Tx of contract %s ", p.URL())
	}

	return nil
}

// 注意这是L1的数据
func (p *SwapContractRuntime) genDepositInfo(height int) *DealInfo {

	p.mutex.RLock()
	defer p.mutex.RUnlock()

	assetName := p.GetAssetName()
	maxHeight := 0
	var totalValue int64
	var totalAmt *Decimal                          // 资产数量
	sendInfoMap := make(map[string]*SendAssetInfo) // key: address
	for _, depositMap := range p.depositMap {
		for _, item := range depositMap {
			// 处理deposit异常： 配合发送交易的代码，用来单独处理某个utxo没有正确asend的导致deposit失败的问题
			// Log.Infof("genDepositInfo utxo: %s", utxo)
			// if utxo == "649c3db46f8da14e46a9f1dd89a46d0554f2f8db1b70a1870ccec48a5d652119:0" {
			// 	Log.Infof("genDepositInfo skip this utxo")
			// 	continue
			// }

			h, _, _ := indexer.FromUtxoId(item.UtxoId)
			if h > height {
				continue
			}
			if item.Done != DONE_NOTYET || item.Reason != INVOKE_REASON_NORMAL {
				continue
			}
			maxHeight = max(maxHeight, h)

			sendInfoMap[item.InUtxo] = &SendAssetInfo{
				Address:   item.Address,
				Value:     item.RemainingValue,
				AssetName: assetName,
				AssetAmt:  item.RemainingAmt.Clone(),
			}

			totalAmt = totalAmt.Add(item.RemainingAmt)
			totalValue += item.RemainingValue
		}
	}

	// 处理deposit异常
	// if len(sendInfoMap) == 0 && p.URL() == "bc1qmgactfmdfympq5tqld7rc53y4dphvdyqnmtuuv9jwgpn7hqwr2kss26dls_::_transcend.tc" {
	// 	addr := "bc1ptcv33dapwpw93nnmn24tjce4fm79f7edvz9a82hxvy4yqcd3n65s0efvk5"
	// 	utxo := "649c3db46f8da14e46a9f1dd89a46d0554f2f8db1b70a1870ccec48a5d652119:0"
	// 	trader := p.traderInfoMap[addr]
	// 	if trader != nil {
	// 		_, ok := trader.DepositUtxoMap[utxo]
	// 		if ok {
	// 			item, ok := p.history[utxo]
	// 			if ok {
	// 				if item.Done == DONE_NOTYET {
	// 					Log.Infof("genDepositInfo add utxo: %s", utxo)
	// 					sendInfoMap[item.InUtxo] = &SendAssetInfo{
	// 						Address:   item.Address,
	// 						Value:     item.RemainingValue,
	// 						AssetName: assetName,
	// 						AssetAmt:  item.RemainingAmt.Clone(),
	// 					}

	// 					totalAmt = totalAmt.Add(item.RemainingAmt)
	// 					totalValue += item.RemainingValue
	// 				}
	// 			}
	// 		}
	// 	}
	// }

	return &DealInfo{
		SendInfo:          sendInfoMap,
		AssetName:         assetName,
		TotalAmt:          totalAmt,
		TotalValue:        totalValue,
		Reason:            INVOKE_RESULT_DEPOSIT,
		Height:            maxHeight,
		InvokeCount:       p.InvokeCount,
		StaticMerkleRoot:  p.StaticMerkleRoot,
		RuntimeMerkleRoot: p.CurrAssetMerkleRoot,
	}
}

func (p *SwapContractRuntime) updateWithDealInfo_deposit(dealInfo *DealInfo) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.TotalDepositAssets = p.TotalDepositAssets.Add(dealInfo.TotalAmt)
	p.TotalDepositSats += dealInfo.TotalValue
	p.TotalDepositTx++
	p.TotalDepositTxFee += dealInfo.Fee
	p.TotalOutputAssets = p.TotalOutputAssets.Add(dealInfo.TotalAmt)
	p.TotalOutputSats += dealInfo.TotalValue + dealInfo.Fee

	for inUtxo := range dealInfo.SendInfo {

		item, ok := p.history[inUtxo]
		if ok {
			p.updateWithDepositItem(item, dealInfo.SendTxIdMap[inUtxo])
		} else {
			// 不是InUtxo，而是address, 那就只有一条记录
			p.updateWithDepositItem(item, dealInfo.TxId)
		}

	}

	p.CheckPoint = dealInfo.InvokeCount
	p.AssetMerkleRoot = dealInfo.RuntimeMerkleRoot
	p.CheckPointBlock = p.CurrBlock
	p.CheckPointBlockL1 = p.CurrBlockL1

	p.refreshTime_swap = 0
}

func (p *SwapContractRuntime) updateWithDepositItem(item *SwapHistoryItem, txId string) {
	if txId == "" {
		// 异常处理
		// item.Reason = INVOKE_REASON_UTXO_NOT_FOUND
		// item.Done = DONE_CLOSED_DIRECTLY
		return
	}

	// deposit utxo 解锁
	p.stp.GetWalletMgr().utxoLockerL1.UnlockUtxo(item.InUtxo)
	Log.Infof("deposit utxo %s unlocked", item.InUtxo)

	// 更新对应数据
	item.OutTxId = txId
	item.OutAmt = item.RemainingAmt
	item.OutValue = item.RemainingValue
	item.RemainingAmt = nil
	item.RemainingValue = 0
	item.Done = DONE_DEALT

	url := p.URL()
	SaveContractInvokeHistoryItem(p.stp.GetDB(), url, item)
	delete(p.history, item.InUtxo)

	trader := p.traderInfoMap[item.Address]
	if trader != nil {
		trader.DepositAmt = trader.DepositAmt.Add(item.OutAmt)
		trader.DepositValue += item.OutValue
		saveContractInvokerStatus(p.stp.GetDB(), url, trader)
	}

	items, ok := p.depositMap[item.Address]
	if ok {
		delete(items, item.Id)
	}

	if len(items) == 0 {
		delete(p.depositMap, item.Address)
	}
}

// 根据deposit的item信息，恢复TxOutput
func (p *ContractRuntimeBase) GetInvokeOutput(item *SwapHistoryItem) *indexer.TxOutput {
	var value int64
	isPlainSat := item.AssetName == indexer.ASSET_PLAIN_SAT.String()
	if isPlainSat {
		value = item.RemainingValue
	} else {
		value = indexer.GetBindingSatNum(item.RemainingAmt, uint32(p.N))
	}
	output := &indexer.TxOutput{
		UtxoId:      item.UtxoId,
		OutPointStr: item.InUtxo,
		OutValue: wire.TxOut{
			Value:    value,
			PkScript: p.GetPkScript(),
		},
	}
	if !isPlainSat {
		output.Assets = indexer.TxAssets{indexer.AssetInfo{
			Name:       *indexer.NewAssetNameFromString(item.AssetName),
			Amount:     *item.RemainingAmt,
			BindingSat: uint32(p.N),
		}}
	}

	return output
}

// 收到deposit的交易，执行二层分发
func (p *SwapContractRuntime) deposit() error {

	// 发送
	if p.resv.LocalIsInitiator() {
		if len(p.depositMap) == 0 {
			return nil
		}

		Log.Debugf("%s start contract %s with action deposit", p.stp.GetMode(), p.URL())

		url := p.URL()
		p.mutex.RLock()
		height := p.CurrBlockL1
		p.mutex.RUnlock()
		dealInfo := p.genDepositInfo(height)

		if len(dealInfo.SendInfo) != 0 {
			dealInfo.SendTxIdMap = make(map[string]string)
			var anchorTxs []*swire.MsgTx
			for inUtxo, v := range dealInfo.SendInfo {
				item, ok := p.history[inUtxo]
				if !ok {
					continue
				}
				h, _, _ := indexer.FromUtxoId(item.UtxoId)
				output := p.GetInvokeOutput(item)
				anchorTx, err := p.buildDepositAnchorTx(output, v.Address, h, INVOKE_RESULT_DEPOSIT)
				if err != nil {
					Log.Errorf("contract %s buildAndBroadcastAnchorTx %s failed, %v", url, output.OutPointStr, err)
					return err
				}
				anchorTxs = append(anchorTxs, anchorTx)
				dealInfo.SendTxIdMap[inUtxo] = anchorTx.TxID()
			}

			if len(anchorTxs) > 0 {
				var err error
				for i := 0; i < 3; i++ {
					err = p.notifyAndSendDepositAnchorTxs(anchorTxs)
					if err != nil {
						if strings.Contains(err.Error(), ERR_MERKLE_ROOT_INCONSISTENT) {
							// recalc
							Log.Infof("contract %s notifyAndSendDepositAnchorTxs failed %v, recalculate merkle root and try again", url, err)
							p.calcAssetMerkleRoot()
						} else {
							Log.Errorf("contract %s notifyAndSendDepositAnchorTxs failed %v, wait a second and try again", url, err)
							// 服务端可能还没有同步到数据，需要多尝试几次，但不要卡太久
							// time.Sleep(2 * time.Second)
							continue
						}
					}
					Log.Infof("contract %s notifyPeerDepositAnchorTxs completed", url)
					break
				}
				if err != nil {
					// 处理deposit异常
					// 处理某些情况下，该utxo已经被花费了，只能在二层直接转
					// targetUtxo := "649c3db46f8da14e46a9f1dd89a46d0554f2f8db1b70a1870ccec48a5d652119:0"
					// info := dealInfo.SendInfo[targetUtxo]
					// if len(dealInfo.SendInfo) == 1 && info != nil {
					// 	Log.Infof("deposit try sendTx_SatsNet...")
					// 	txId, err := p.sendTx_SatsNet(dealInfo, INVOKE_RESULT_DEPOSIT)
					// 	if err != nil {
					// 		Log.Errorf("contract %s sendTx_SatsNet %s failed %v", p.URL(), INVOKE_RESULT_DEPOSIT, err)
					// 		// 下个区块再试
					// 		return err
					// 	}
					// 	Log.Infof("deposit sendTx_SatsNet succeed: %s", txId)
					// 	dealInfo.SendTxIdMap[targetUtxo] = txId
					// 	dealInfo.TxId = txId
					// 	dealInfo.Fee = DEFAULT_FEE_SATSNET
					// } else {
					Log.Errorf("contract %s deposit failed %v", url, err)
					// 下个区块再试
					return err
					//}
				}

				p.updateWithDealInfo_deposit(dealInfo)
				p.stp.SaveReservationWithLock(p.resv)
			}
		}

		Log.Debugf("contract %s deposit completed", url)
	} else {
		Log.Debugf("server: waiting the deposit Tx of contract %s ", p.URL())
	}

	return nil
}

func (p *SwapContractRuntime) genWithdrawInfo(height int) *DealInfo {

	p.mutex.RLock()
	defer p.mutex.RUnlock()

	isRune := false
	assetName := p.GetAssetName()
	isRune = assetName.Protocol == indexer.PROTOCOL_NAME_RUNES

	maxHeight := 0
	var totalValue int64
	var totalAmt *Decimal                          // 资产数量
	sendInfoMap := make(map[string]*SendAssetInfo) // key: address
	for _, withdrawMap := range p.withdrawMap {
		for _, item := range withdrawMap {
			h, _, _ := indexer.FromUtxoId(item.UtxoId)
			if h > height {
				continue
			}
			if item.Done != DONE_NOTYET || item.Reason != INVOKE_REASON_NORMAL {
				continue
			}
			maxHeight = max(maxHeight, h)

			info, ok := sendInfoMap[item.Address]
			if !ok {
				info = &SendAssetInfo{
					Address:   item.Address,
					Value:     0,
					AssetName: assetName,
					AssetAmt:  nil,
				}
				sendInfoMap[item.Address] = info
			}

			info.AssetAmt = info.AssetAmt.Add(item.RemainingAmt)
			info.Value += item.RemainingValue
			totalAmt = totalAmt.Add(item.RemainingAmt)
			totalValue += item.RemainingValue

			if isRune && len(sendInfoMap) == 8 {
				break // 其他后面再处理
			}
		}
	}

	if !isRune && len(sendInfoMap) > 0 {
		n := p.N
		isPlainAsset := indexer.IsPlainAsset(assetName)
		// 看看某个地址是否需要stub，
		var not int
		var need []string
		for k, v := range sendInfoMap {
			if isPlainAsset {
				not++
			} else {
				if n != 0 && indexer.GetBindingSatNum(v.AssetAmt, uint32(n)) < 330 {
					need = append(need, k)
				} else {
					not++
				}
			}
		}
		if not != 0 {
			if len(need) > 0 {
				// 先处理不需要stub的withdraw，所以把所有需要stub的删除
				for _, addr := range need {
					info, ok := sendInfoMap[addr]
					if ok {
						totalAmt = totalAmt.Sub(info.AssetAmt)
						delete(sendInfoMap, addr) // 下次再处理
					}
				}
			}
		} else {
			// 现在都是需要stub的
			// 为了记录对应的输出，每次只处理一条
			if len(need) > 0 {
				for i := 1; i < len(need); i++ {
					addr := need[i]
					info, ok := sendInfoMap[addr]
					if ok {
						totalAmt = totalAmt.Sub(info.AssetAmt)
						delete(sendInfoMap, addr) // 下次再处理
					}
				}
			}
		}
	}

	return &DealInfo{
		SendInfo:          sendInfoMap,
		AssetName:         assetName,
		TotalAmt:          totalAmt,
		TotalValue:        totalValue,
		Reason:            INVOKE_RESULT_WITHDRAW,
		Height:            maxHeight,
		InvokeCount:       p.InvokeCount,
		StaticMerkleRoot:  p.StaticMerkleRoot,
		RuntimeMerkleRoot: p.CurrAssetMerkleRoot,
	}
}

func (p *SwapContractRuntime) updateWithDealInfo_withdraw(dealInfo *DealInfo) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.TotalWithdrawAssets = p.TotalWithdrawAssets.Add(dealInfo.TotalAmt)
	p.TotalWithdrawSats += dealInfo.TotalValue
	p.TotalWithdrawTx++
	p.TotalWithdrawTxFee += dealInfo.Fee
	p.TotalOutputAssets = p.TotalOutputAssets.Add(dealInfo.TotalAmt)
	p.TotalOutputSats += dealInfo.TotalValue + dealInfo.Fee

	height := dealInfo.Height
	txId := dealInfo.TxId
	url := p.URL()
	// 通过height确定哪些item是需要处理的，然后更新对应的数据
	deletedAddr := make([]string, 0)
	for addr := range dealInfo.SendInfo {
		if addr == ADDR_OPRETURN || addr == p.ChannelAddr {
			continue
		}
		items, ok := p.withdrawMap[addr]
		if !ok {
			// 数据丢失？不应该出现这种情况
			Log.Errorf("updateWithDealInfo_withdraw can't find %s for txId %s", addr, dealInfo.TxId)
			continue
		}
		trader := p.traderInfoMap[addr]

		deletedItems := make([]int64, 0)
		for id, item := range items {
			h, _, _ := indexer.FromUtxoId(item.UtxoId)
			if h > height {
				continue
			}
			if item.AssetName != dealInfo.AssetName.String() {
				continue
			}
			deletedItems = append(deletedItems, id)

			// 更新对应数据
			item.OutTxId = txId
			item.OutAmt = item.RemainingAmt
			item.OutValue = item.RemainingValue
			item.RemainingAmt = nil
			item.RemainingValue = 0
			item.Done = DONE_DEALT
			item.Padded = []byte(fmt.Sprintf("%d", dealInfo.Fee)) // 记录该OutTx的费用，方便统计
			SaveContractInvokeHistoryItem(p.stp.GetDB(), url, item)
			delete(p.history, item.InUtxo)

			if trader != nil {
				trader.WithdrawAmt = trader.WithdrawAmt.Add(item.OutAmt)
				trader.WithdrawValue += item.OutValue
			}
		}
		if trader != nil {
			saveContractInvokerStatus(p.stp.GetDB(), url, trader)
		}

		for _, id := range deletedItems {
			delete(items, id)
		}
		if len(items) == 0 {
			deletedAddr = append(deletedAddr, addr)
		}
	}
	for _, addr := range deletedAddr {
		delete(p.withdrawMap, addr)
	}

	p.CheckPoint = dealInfo.InvokeCount
	p.AssetMerkleRoot = dealInfo.RuntimeMerkleRoot
	p.CheckPointBlock = p.CurrBlock
	p.CheckPointBlockL1 = p.CurrBlockL1

	p.refreshTime_swap = 0
}

// 收到withdraw的交易，执行一层分发
func (p *SwapContractRuntime) withdraw() error {

	// 发送
	if p.resv.LocalIsInitiator() {
		if len(p.withdrawMap) == 0 {
			return nil
		}
		url := p.URL()

		Log.Debugf("%s start contract %s with action withdraw", p.stp.GetMode(), url)

		p.mutex.RLock()
		height := p.CurrBlock
		p.mutex.RUnlock()
		for {
			dealInfo := p.genWithdrawInfo(height)
			// 发送
			if len(dealInfo.SendInfo) != 0 {
				// 发送费用已经从所有参与者扣除，但如果该交易的聪资产太少，就暂时不发送，等下次
				//if dealInfo.TotalValue >= _valueLimit || len(dealInfo.SendInfo) >= _addressLimit {
				txId, fee, stubFee, err := p.sendTx(dealInfo, INVOKE_RESULT_WITHDRAW, true, true)
				if err != nil {
					if stubFee != 0 {
						p.mutex.Lock()
						p.SwapContractRunningData.TotalWithdrawTxFee += stubFee
						p.mutex.Unlock()
						p.stp.SaveReservationWithLock(p.resv)
					}
					Log.Errorf("contract %s sendTx %s failed %v", url, INVOKE_RESULT_WITHDRAW, err)
					// 下个区块再试
					return err
				}
				// 调整fee
				dealInfo.Fee = fee + stubFee
				dealInfo.TxId = txId
				// record
				p.updateWithDealInfo_withdraw(dealInfo)
				// 成功一步记录一步
				p.stp.SaveReservationWithLock(p.resv)
				Log.Infof("contract %s withdraw completed, %s", url, txId)
				//}
			} else {
				break
			}
		}
		Log.Debugf("contract %s withdraw completed", url)
	} else {
		Log.Debugf("server: waiting the withdraw Tx of contract %s ", p.URL())
	}

	return nil
}

func (p *SwapContractRuntime) genRemoveLiquidityInfo(height int) *DealInfo {

	p.mutex.RLock()
	defer p.mutex.RUnlock()

	assetName := p.GetAssetName()
	var totalValue int64
	var totalAmt *Decimal                          // 资产数量
	sendInfoMap := make(map[string]*SendAssetInfo) // key: address

	addressmap := make(map[string]bool)
	for address := range p.removeLiquidityMap {
		addressmap[address] = true
	}
	if !PROFIT_REINVESTING && len(addressmap) > 0 {
		addressmap[p.GetSvrAddress()] = true
		addressmap[p.GetFoundationAddress()] = true
	}

	for address := range addressmap {
		trader := p.loadTraderInfo(address)
		if trader == nil {
			continue
		}
		if trader.SettleState != SETTLE_STATE_REMOVING_LIQ_READY {
			continue
		}
		if trader.RetrieveAmt.Sign() == 0 && trader.RetrieveValue == 0 {
			continue
		}
		sendInfoMap[address] = &SendAssetInfo{
			Address:   address,
			Value:     trader.RetrieveValue,
			AssetName: assetName,
			AssetAmt:  trader.RetrieveAmt.Clone(),
		}

		totalAmt = totalAmt.Add(trader.RetrieveAmt)
		totalValue += trader.RetrieveValue
	}

	return &DealInfo{
		SendInfo:          sendInfoMap,
		AssetName:         assetName,
		TotalAmt:          totalAmt,
		TotalValue:        totalValue,
		Reason:            INVOKE_RESULT_REMOVELIQUIDITY,
		Height:            height,
		InvokeCount:       p.InvokeCount,
		StaticMerkleRoot:  p.StaticMerkleRoot,
		RuntimeMerkleRoot: p.CurrAssetMerkleRoot,
	}
}

// 取回流动性质押资产
func (p *SwapContractRuntime) retrieve() error {

	// 发送
	if p.resv.LocalIsInitiator() {
		if len(p.removeLiquidityMap) == 0 {
			return nil
		}

		Log.Debugf("%s start contract %s with action removeliq", p.stp.GetMode(), p.URL())
		p.mutex.RLock()
		height := p.CurrBlock
		p.mutex.RUnlock()
		removeLiqInfo := p.genRemoveLiquidityInfo(height)
		// 发送
		if len(removeLiqInfo.SendInfo) != 0 {
			txId, err := p.sendTx_SatsNet(removeLiqInfo, INVOKE_RESULT_REMOVELIQUIDITY)
			if err != nil {
				Log.Errorf("contract %s sendTx_SatsNet %s failed %v", p.URL(), INVOKE_RESULT_REMOVELIQUIDITY, err)
				// 下个区块再试
				return err
			}
			removeLiqInfo.TxId = txId
			removeLiqInfo.Fee = DEFAULT_FEE_SATSNET

			// record
			p.updateWithDealInfo_removeLiquidity(removeLiqInfo)
			// 成功一步记录一步
			p.stp.SaveReservationWithLock(p.resv)

			Log.Debugf("contract %s unstake completed, %s", p.URL(), txId)
		}

	} else {
		//Log.Infof("server: waiting the deal Tx of contract %s ", p.URL())
	}

	return nil
}

func (p *SwapContractRuntime) updateWithDealInfo_removeLiquidity(dealInfo *DealInfo) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.TotalRetrieveAssets = p.TotalRetrieveAssets.Add(dealInfo.TotalAmt)
	p.TotalRetrieveSats += dealInfo.TotalValue
	p.TotalRetrieveTx++
	p.TotalRetrieveTxFee += dealInfo.Fee
	p.TotalOutputAssets = p.TotalOutputAssets.Add(dealInfo.TotalAmt)
	p.TotalOutputSats += dealInfo.TotalValue + dealInfo.Fee

	height := dealInfo.Height
	txId := dealInfo.TxId
	url := p.URL()
	// 通过height确定哪些item是需要处理的，然后更新对应的数据
	deletedAddr := make([]string, 0)
	for addr, info := range dealInfo.SendInfo {
		if addr == ADDR_OPRETURN || addr == p.ChannelAddr {
			continue
		}
		items, ok := p.removeLiquidityMap[addr]
		if !ok {
			if addr != p.GetSvrAddress() && addr != p.GetFoundationAddress() {
				Log.Panicf("updateWithDealInfo_removeLiquidity can't find %s for txId %s", addr, dealInfo.TxId)
			}
			// 服务节点收取利润
		}

		trader := p.loadTraderInfo(addr)
		if trader == nil {
			Log.Panicf("%s can't find trader %s", url, addr)
		}
		if trader.SettleState != SETTLE_STATE_REMOVING_LIQ_READY {
			continue
		}
		trader.RetrieveAmt = trader.RetrieveAmt.Sub(info.AssetAmt)
		trader.RetrieveValue -= info.Value
		trader.SettleState = SETTLE_STATE_NORMAL
		saveContractInvokerStatus(p.stp.GetDB(), url, trader)

		deletedItems := make([]int64, 0)
		for id, item := range items {
			h, _, _ := indexer.FromUtxoId(item.UtxoId)
			if h > height {
				continue
			}

			deletedItems = append(deletedItems, id)

			// 更新对应数据
			item.OutTxId = txId
			item.OutAmt = info.AssetAmt.Clone()
			item.OutValue = info.Value
			item.RemainingAmt = nil
			item.RemainingValue = 0
			item.Done = DONE_DEALT
			SaveContractInvokeHistoryItem(p.stp.GetDB(), url, item)
			delete(p.history, item.InUtxo)
		}

		for _, id := range deletedItems {
			delete(items, id)
		}
		if len(deletedItems) != 0 && len(items) == 0 {
			deletedAddr = append(deletedAddr, addr)
		}
	}
	for _, addr := range deletedAddr {
		delete(p.removeLiquidityMap, addr)
	}

	p.CheckPoint = dealInfo.InvokeCount
	p.AssetMerkleRoot = dealInfo.RuntimeMerkleRoot
	p.CheckPointBlock = p.CurrBlock
	p.CheckPointBlockL1 = p.CurrBlockL1

	p.refreshTime_swap = 0
}

func (p *SwapContractRuntime) genProfitInfo(height int) *DealInfo {

	p.mutex.RLock()
	defer p.mutex.RUnlock()

	assetName := p.GetAssetName()
	var totalValue int64
	var totalAmt *Decimal                          // 资产数量
	sendInfoMap := make(map[string]*SendAssetInfo) // key: address

	for address := range p.profitMap {
		trader := p.loadTraderInfo(address)
		if trader == nil {
			continue
		}

		if trader.ProfitAmt.Sign() == 0 && trader.ProfitValue == 0 {
			continue
		}
		sendInfoMap[address] = &SendAssetInfo{
			Address:   address,
			Value:     trader.ProfitValue,
			AssetName: assetName,
			AssetAmt:  trader.ProfitAmt.Clone(),
		}

		totalAmt = totalAmt.Add(trader.ProfitAmt)
		totalValue += trader.ProfitValue
	}

	return &DealInfo{
		SendInfo:          sendInfoMap,
		AssetName:         assetName,
		TotalAmt:          totalAmt,
		TotalValue:        totalValue,
		Reason:            INVOKE_RESULT_PROFIT,
		Height:            height,
		InvokeCount:       p.InvokeCount,
		StaticMerkleRoot:  p.StaticMerkleRoot,
		RuntimeMerkleRoot: p.CurrAssetMerkleRoot,
	}
}


func (p *SwapContractRuntime) updateWithDealInfo_profit(dealInfo *DealInfo) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.TotalProfitAssets = p.TotalProfitAssets.Add(dealInfo.TotalAmt)
	p.TotalProfitSats += dealInfo.TotalValue
	p.TotalProfitTx++
	p.TotalProfitTxFee += dealInfo.Fee
	p.TotalOutputAssets = p.TotalOutputAssets.Add(dealInfo.TotalAmt)
	p.TotalOutputSats += dealInfo.TotalValue + dealInfo.Fee

	height := dealInfo.Height
	txId := dealInfo.TxId
	url := p.URL()
	// 通过height确定哪些item是需要处理的，然后更新对应的数据
	deletedAddr := make([]string, 0)
	for addr, info := range dealInfo.SendInfo {
		if addr == ADDR_OPRETURN || addr == p.ChannelAddr {
			continue
		}
		items, ok := p.profitMap[addr]
		if !ok {
			Log.Panicf("updateWithDealInfo_profit can't find %s for txId %s", addr, dealInfo.TxId)
		}

		trader := p.loadTraderInfo(addr)
		if trader == nil {
			Log.Panicf("%s can't find trader %s", url, addr)
		}
		
		trader.ProfitAmt = trader.ProfitAmt.Sub(info.AssetAmt)
		trader.ProfitValue -= info.Value
		saveContractInvokerStatus(p.stp.GetDB(), url, trader)

		deletedItems := make([]int64, 0)
		for id, item := range items {
			h, _, _ := indexer.FromUtxoId(item.UtxoId)
			if h > height {
				continue
			}

			deletedItems = append(deletedItems, id)

			// 更新对应数据
			item.OutTxId = txId
			item.OutAmt = info.AssetAmt.Clone()
			item.OutValue = info.Value
			item.RemainingAmt = nil
			item.RemainingValue = 0
			item.Done = DONE_DEALT
			SaveContractInvokeHistoryItem(p.stp.GetDB(), url, item)
			delete(p.history, item.InUtxo)
		}

		for _, id := range deletedItems {
			delete(items, id)
		}
		if len(deletedItems) != 0 && len(items) == 0 {
			deletedAddr = append(deletedAddr, addr)
		}
	}
	for _, addr := range deletedAddr {
		delete(p.profitMap, addr)
	}

	p.CheckPoint = dealInfo.InvokeCount
	p.AssetMerkleRoot = dealInfo.RuntimeMerkleRoot
	p.CheckPointBlock = p.CurrBlock
	p.CheckPointBlockL1 = p.CurrBlockL1

	p.refreshTime_swap = 0
}


// 提取利润，目前只支持底池的提取
func (p *SwapContractRuntime) profit() error {

	// 发送
	if p.resv.LocalIsInitiator() {
		if len(p.profitMap) == 0 {
			return nil
		}

		Log.Debugf("%s start contract %s with action profit", p.stp.GetMode(), p.URL())
		p.mutex.RLock()
		height := p.CurrBlock
		p.mutex.RUnlock()
		profitInfo := p.genProfitInfo(height)
		// 发送
		if len(profitInfo.SendInfo) != 0 {
			txId, err := p.sendTx_SatsNet(profitInfo, INVOKE_RESULT_PROFIT)
			if err != nil {
				Log.Errorf("contract %s sendTx_SatsNet %s failed %v", p.URL(), INVOKE_RESULT_PROFIT, err)
				// 下个区块再试
				return err
			}
			profitInfo.TxId = txId
			profitInfo.Fee = DEFAULT_FEE_SATSNET

			// record
			p.updateWithDealInfo_profit(profitInfo)
			// 成功一步记录一步
			p.stp.SaveReservationWithLock(p.resv)

			Log.Debugf("contract %s unstake completed, %s", p.URL(), txId)
		}

	} else {
		//Log.Infof("server: waiting the deal Tx of contract %s ", p.URL())
	}

	return nil
}

func ParseInscribeInfo(txSignInfo []*wwire.TxSignInfo) ([]*InscribeInfo, map[string]*TxOutput, error) {
	var inscribes []*InscribeInfo
	preOutputs := make(map[string]*TxOutput)
	for _, txInfo := range txSignInfo {
		switch txInfo.Reason {
		case "commit":
			// moredata: transfer body, reveal private key 
			if len(txInfo.MoreData) == 0 {
				return nil, nil, fmt.Errorf("should provide more data")
			}
			address, assetName, amt, feeRate, privateKey, err := ParseInscribeMoreData(txInfo.MoreData)
			if err != nil {
				return nil, nil, err
			}

			if !txInfo.L1Tx {
				return nil, nil,fmt.Errorf("should be a L1 tx")
			}
			tx, err := DecodeMsgTx(txInfo.Tx)
			if err != nil {
				return nil, nil, err
			}

			if len(tx.TxOut) == 2 {
				output := indexer.GenerateTxOutput(tx, 1)
				preOutputs[output.OutPointStr] = output
			}

			inscribes = append(inscribes, &InscribeInfo{
				CommitTx: tx,
				RemoteSig: txInfo.LocalSigs,
				DestAddr: address,
				AssetName: assetName,
				Amt: amt,
				FeeRate: feeRate,
				RevealPrivateKey: privateKey,
			})
		case "reveal":
			// 一个commit后面必须跟着reveal
			if len(inscribes) == 0 {
				return nil, nil, fmt.Errorf("no commit tx")
			}
			inscribe := inscribes[len(inscribes)-1]
			if inscribe == nil || inscribe.CommitTx == nil {
				return nil, nil, fmt.Errorf("can't find commit tx")
			}
			if inscribe.RevealTx != nil {
				return nil, nil, fmt.Errorf("reveal tx is set")
			}
			if !txInfo.L1Tx {
				return nil, nil, fmt.Errorf("should be a L1 tx")
			}
			tx, err := DecodeMsgTx(txInfo.Tx)
			if err != nil {
				return nil, nil, err
			}
			inscribe.RevealTx = tx

			utxo := fmt.Sprintf("%s:0", tx.TxID())
			assetInfo := indexer.AssetInfo{
				Name: *inscribe.AssetName,
				Amount: *inscribe.Amt,
				BindingSat: 0,
			}
			preOutputs[utxo] = &indexer.TxOutput{
				UtxoId: INVALID_ID,
				OutPointStr: utxo,
				OutValue: *tx.TxOut[0],
				Assets: indexer.TxAssets{assetInfo},
				Offsets: map[indexer.AssetName]indexer.AssetOffsets{
					*inscribe.AssetName: {{Start:0, End:1}},
				},
				SatBindingMap: map[int64]*indexer.AssetInfo{
					0: &assetInfo,
				},
				Invalids: make(map[indexer.AssetName]bool),
			}
		}
	}
	return inscribes, preOutputs, nil
}

func (p *SwapContractRuntime) AllowPeerAction(action string, param any) (any, error) {

	// 内部自己锁
	// p.mutex.RLock()
	// defer p.mutex.RUnlock()

	Log.Infof("AllowPeerAction %s ", action)
	_, err := p.ContractRuntimeBase.AllowPeerAction(action, param)
	if err != nil {
		return nil, err
	}

	switch action {

	case wwire.STP_ACTION_SIGN: // 通道外资产
		req, ok := param.(*wwire.RemoteSignMoreData_Contract)
		if !ok {
			return nil, fmt.Errorf("not RemoteSignMoreData_Contract")
		}

		// 1. 读取交易信息
		if req.Action == INVOKE_API_DEPOSIT {
			// deposit 特殊处理
			dealInfo, err := p.genDepositInfoFromAnchorTxs(req)
			if err == nil {
				return dealInfo, nil
			}
			// 如果失败，可能走普通转账的deposit，尝试走下面的流程
		}

		var dealInfo *DealInfo
		var transcendTx *swire.MsgTx
		inscribes, preOutputs, err := ParseInscribeInfo(req.Tx)
		if err != nil {
			return nil, err
		}
		for _, txInfo := range req.Tx {
			switch txInfo.Reason {
			case "ascend", "descend":
				if txInfo.L1Tx {
					return nil, fmt.Errorf("only a anchor/deanchor tx followed can be accepted")
				}
				transcendTx, err = DecodeMsgTx_SatsNet(txInfo.Tx)
				if err != nil {
					return nil, err
				}
			
			case "commit", "reveal":
				// ParseInscribeInfo
	
			case "": // main tx
				if txInfo.L1Tx {
					tx, err := DecodeMsgTx(txInfo.Tx)
					if err != nil {
						return nil, err
					}
					dealInfo, err = p.genSendInfoFromTx(tx, preOutputs, req.MoreData)
					if err != nil {
						return nil, err
					}
				} else {
					tx, err := DecodeMsgTx_SatsNet(txInfo.Tx)
					if err != nil {
						return nil, err
					}
					dealInfo, err = p.genSendInfoFromTx_SatsNet(tx, false)
					if err != nil {
						return nil, err
					}
				}
				
			default:
				return nil, fmt.Errorf("not support %s", txInfo.Reason)
			}
		}

		// 2. 检查主交易的有效性
		dealInfo.InvokeCount = req.InvokeCount
		dealInfo.StaticMerkleRoot = req.StaticMerkleRoot
		dealInfo.RuntimeMerkleRoot = req.RuntimeMerkleRoot

		// 以发起invoke请求的区块高度做判断，发起请求时在哪一层，就用哪一层的高度
		var expectedSendInfo map[string]*SendAssetInfo
		switch dealInfo.Reason {
		case INVOKE_RESULT_DEAL:
			info := p.genDealInfo(dealInfo.Height)
			expectedSendInfo = info.SendInfo

		case INVOKE_RESULT_REFUND:
			refundInfo := p.genRefundInfo(dealInfo.Height)
			if refundInfo != nil {
				expectedSendInfo = refundInfo.SendInfo
			}

		case INVOKE_RESULT_DEPOSIT:
			dealInfo.Fee = 0 // anchorTx
			info := p.genDepositInfo(dealInfo.Height)
			if info != nil {
				// deposit的key是InUtxo，需要修改为addr
				expectedSendInfo = make(map[string]*SendAssetInfo)
				for _, v := range dealInfo.SendInfo {
					sendInfo, ok := expectedSendInfo[v.Address]
					if !ok {
						sendInfo = &SendAssetInfo{
							Address:   v.Address,
							AssetName: v.AssetName,
						}
						expectedSendInfo[v.Address] = sendInfo
					}
					sendInfo.Value += v.Value
					sendInfo.AssetAmt = sendInfo.AssetAmt.Add(v.AssetAmt)
				}
			}

		case INVOKE_RESULT_WITHDRAW:
			info := p.genWithdrawInfo(dealInfo.Height)
			if info != nil {
				expectedSendInfo = info.SendInfo
			}
			// 调整fee
			n := p.N
			if n == 0 {
				dealInfo.Fee += int64(len(dealInfo.SendInfo) * 330)
			} else {
				if len(dealInfo.SendInfo) == 1 {
					if !indexer.IsPlainAsset(p.GetAssetName()) {
						satsNum := indexer.GetBindingSatNum(dealInfo.TotalAmt, uint32(n))
						if satsNum < 330 {
							dealInfo.Fee += 330
						}
					}
				}
			}

		case INVOKE_RESULT_REMOVELIQUIDITY:
			info := p.genRemoveLiquidityInfo(dealInfo.Height)
			if info != nil {
				expectedSendInfo = info.SendInfo
			}

		case INVOKE_RESULT_PROFIT:
			info := p.genProfitInfo(dealInfo.Height)
			if info != nil {
				expectedSendInfo = info.SendInfo
			}

		default:
			return nil, fmt.Errorf("not expected contract invoke reason %s", dealInfo.Reason)
		}

		// if len(sendInfo) != len(expectedSendInfo) {
		// 	return fmt.Errorf("count of address is different %d %d", len(sendInfo), len(expectedSendInfo))
		// }

		for addr, infoInTx := range dealInfo.SendInfo {
			if addr == ADDR_OPRETURN {
				continue
			}
			if addr == p.ChannelAddr {
				continue
			}
			infoExpected, ok := expectedSendInfo[addr]
			if !ok {
				return nil, fmt.Errorf("%s not allow send %v (expected %v) to %s", p.URL(), infoInTx, infoExpected, addr)
			}

			if infoInTx.AssetName.String() != infoExpected.AssetName.String() {
				return nil, fmt.Errorf("%s not allow send %s (expected %s) %s to %s", p.URL(),
					infoInTx.AssetAmt.String(), infoExpected.AssetAmt.String(), infoInTx.AssetName.String(), addr)
			}

			if infoInTx.Value != infoExpected.Value {
				return nil, fmt.Errorf("%s not allow send sats value %d (expected %d) to %s",
					p.URL(), infoInTx.Value, infoExpected.Value, addr)
			}

			if infoInTx.AssetAmt.Cmp(infoExpected.AssetAmt) != 0 {
				return nil, fmt.Errorf("%s not allow send asset amt %s (expected %s) to %s",
					p.URL(), infoInTx.AssetAmt.String(), infoExpected.AssetAmt.String(), addr)
			}
		}

		// 3. 检查其他相关交易的有效性
		if transcendTx != nil {
			dealInfo2, err := p.genSendInfoFromTx_SatsNet(transcendTx, true)
			if err != nil {
				return nil, err
			}
			for addr, info := range dealInfo2.SendInfo {
				if addr == ADDR_OPRETURN {
					if dealInfo.TotalValue != info.Value {
						return nil, fmt.Errorf("%s not allow deanchor value %d (expected %d)",
							p.URL(), info.Value, dealInfo.TotalValue)
					}

					if dealInfo.TotalAmt.Cmp(info.AssetAmt) != 0 {
						return nil, fmt.Errorf("%s not allow deanchor asset amt %s (expected %s)",
							p.URL(), info.AssetAmt.String(), dealInfo.TotalAmt.String())
					}
					continue
				}
				if addr == p.ChannelAddr {
					continue
				}
				return nil, fmt.Errorf("%s deanchorTx not allow send asset to %s", p.URL(), addr)
			}

			//dealInfo.TxId = dealInfo.TxId + " " + dealInfo2.TxId
		}

		if len(inscribes) != 0 {
			// 验证每一个transfer铭文
			preOutMap := make(map[string]*TxOutput)
			for _, output := range dealInfo.PreOutputs {
				preOutMap[output.OutPointStr] = output
			}

			for _, insc := range inscribes {
				dest := dealInfo.SendInfo[insc.DestAddr]
				if dest.AssetName.String() != insc.AssetName.String() {
					return nil, fmt.Errorf("inscribe: different asset name, expected %s but %s",
				 		dest.AssetName.String(), insc.AssetName.String())
				}
				if dest.AssetAmt.Cmp(insc.Amt) != 0 {
					return nil, fmt.Errorf("inscribe: different asset amt, expected %s but %s",
				 		dest.AssetAmt.String(), insc.Amt )
				}

				inputs := make([]*TxOutput, 0)
				for _, txIn := range insc.CommitTx.TxIn {
					utxo := txIn.PreviousOutPoint.String()
					output, ok := preOutputs[utxo]
					if !ok {
						output, err = p.stp.GetIndexerClient().GetTxOutput(utxo)
						if err != nil {
							Log.Errorf("inscribe: GetTxOutFromRawTx %s failed, %v", utxo, err)
							return nil, err
						}
					}
					inputs = append(inputs, output)
				}
				insc2, err := p.stp.GetWalletMgr().MintTransferV2_brc20(p.ChannelAddr,
					p.ChannelAddr, insc.AssetName, insc.Amt, insc.FeeRate, inputs, true, insc.RevealPrivateKey, true, false, false)
				if err != nil {
					return nil, fmt.Errorf("can't regenerate inscribe info from request: %v", insc)
				}
				if !CompareMsgTx(insc2.CommitTx, insc.CommitTx) {
					return nil, fmt.Errorf("commit tx different")
				}
				if !CompareMsgTx(insc2.RevealTx, insc.RevealTx) {
					return nil, fmt.Errorf("reveal tx different")
				}
				// reveal的输出是组成主交易的输入之一，其对应的输出前面已经检查
				utxo := fmt.Sprintf("%s:0", insc.RevealTx.TxID())
				_, ok := preOutMap[utxo]
				if !ok {
					return nil, fmt.Errorf("reveal output %s is not in main tx", utxo)
				}
			}
		}

		Log.Infof("%s is allowed by contract %s (reason: %s)", wwire.STP_ACTION_SIGN, p.URL(), dealInfo.Reason)
		return dealInfo, nil

	case "stub":
		req, ok := param.(*wwire.RemoteSignMoreData_Contract)
		if !ok {
			return nil, fmt.Errorf("not RemoteSignMoreData_Contract")
		}
		if len(req.Tx) != 1 {
			return nil, fmt.Errorf("only one TX can be accepted in stub action")
		}
		tx1 := req.Tx[0]

		if !tx1.L1Tx {
			return nil, fmt.Errorf("only L1 TX can be accepted in stub action")
		}

		tx, err := DecodeMsgTx(tx1.Tx)
		if err != nil {
			return nil, err
		}

		for _, txOut := range tx.TxOut {
			if sindexer.IsOpReturn(txOut.PkScript) {
				continue
			}

			addr, err := AddrFromPkScript(txOut.PkScript)
			if err != nil {
				Log.Errorf("AddrFromPkScript failed, %v", err)
				return nil, err
			}
			if addr == p.ChannelAddr {
				continue
			}
			return nil, fmt.Errorf("stub not allow send asset to %s", addr)
		}
		return nil, nil

	case "testing":
		if !_enable_testing {
			return nil, fmt.Errorf("not support")
		}

		req, ok := param.(*wwire.RemoteSignMoreData_Contract)
		if !ok {
			return nil, fmt.Errorf("not RemoteSignMoreData_Contract")
		}
		if len(req.Tx) != 1 {
			return nil, fmt.Errorf("only one TX can be accepted in testing")
		}
		tx1 := req.Tx[0]

		if tx1.L1Tx {
			tx, err := DecodeMsgTx(tx1.Tx)
			if err != nil {
				return nil, err
			}

			for _, txOut := range tx.TxOut {
				if sindexer.IsOpReturn(txOut.PkScript) {
					continue
				}

				addr, err := AddrFromPkScript(txOut.PkScript)
				if err != nil {
					Log.Errorf("AddrFromPkScript failed, %v", err)
					return nil, err
				}
				if addr == p.ChannelAddr {
					continue
				}
				return nil, fmt.Errorf("stub not allow send asset to %s", addr)
			}
		} else {
			tx, err := DecodeMsgTx_SatsNet(tx1.Tx)
			if err != nil {
				return nil, err
			}

			for _, txOut := range tx.TxOut {
				if sindexer.IsOpReturn(txOut.PkScript) {
					continue
				}

				addr, err := AddrFromPkScript(txOut.PkScript)
				if err != nil {
					Log.Errorf("AddrFromPkScript failed, %v", err)
					return nil, err
				}
				if addr == p.ChannelAddr {
					continue
				}
				return nil, fmt.Errorf("stub not allow send asset to %s", addr)
			}
		}
		return nil, nil

	default:
		return nil, fmt.Errorf("AllowPeerAction not support action %s", action)
	}

}

// 之前已经校验过
func (p *SwapContractRuntime) SetPeerActionResult(action string, param any) {
	Log.Infof("%s SetPeerActionResult %s ", p.URL(), action)

	// 内部自己锁
	// p.mutex.Lock()
	// defer p.mutex.Unlock()

	switch action {

	case wwire.STP_ACTION_SIGN: // 通道外资产
		dealInfo, ok := param.(*DealInfo)
		if !ok {
			Log.Errorf("not DealInfo")
			return
		}

		switch dealInfo.Reason {
		case INVOKE_RESULT_DEAL:
			p.updateWithDealInfo_swap(dealInfo)

		case INVOKE_RESULT_REFUND:
			p.updateWithDealInfo_refund(dealInfo)

		case INVOKE_RESULT_DEPOSIT:
			p.updateWithDealInfo_deposit(dealInfo)

		case INVOKE_RESULT_WITHDRAW:
			fee, ok := p.stubFeeMap[dealInfo.InvokeCount]
			if ok {
				dealInfo.Fee += fee
				delete(p.stubFeeMap, dealInfo.InvokeCount)
			}
			p.updateWithDealInfo_withdraw(dealInfo)

		case INVOKE_RESULT_REMOVELIQUIDITY:
			p.updateWithDealInfo_removeLiquidity(dealInfo)

		case INVOKE_RESULT_PROFIT:
			p.updateWithDealInfo_profit(dealInfo)

		default:
			return
		}

		p.stp.SaveReservationWithLock(p.resv)
		Log.Infof("%s SetPeerActionResult %s completed", p.URL(), action)
		return

	case "stub":
		req := param.(*wwire.RemoteSignMoreData_Contract)
		// 记录TxId和fee
		fee, _ := strconv.ParseInt(string(req.MoreData), 10, 64)
		tx1 := req.Tx[0]
		tx, _ := DecodeMsgTx(tx1.Tx)

		saveContractInvokeResult(p.stp.GetDB(), p.URL(), tx.TxID(), "stub")
		p.stubFeeMap[req.InvokeCount] = fee // 暂时不保存，会影响merkle root的计算，在下一个合约发送交易中保存起来
		// p.mutex.Lock()
		// p.SwapContractRunningData.TotalWithdrawTxFee += fee
		// saveReservation(p.stp.GetDB(), p.resv)
		// defer p.mutex.Unlock()
	}
}

// 获取deposit数据，同时做检查
func (p *SwapContractRuntime) genDepositInfoFromAnchorTxs(req *wwire.RemoteSignMoreData_Contract) (*DealInfo, error) {
	// anchorTxs

	p.mutex.RLock()
	defer p.mutex.RUnlock()

	assetName := p.GetAssetName()
	maxHeight := 0
	var totalValue int64
	var totalAmt *Decimal                          // 资产数量
	sendInfoMap := make(map[string]*SendAssetInfo) // key: inUtxo
	sendTxIdMap := make(map[string]string)
	for _, tx := range req.Tx {
		if tx.L1Tx {
			return nil, fmt.Errorf("not anchor TX")
		}
		tx, err := DecodeMsgTx_SatsNet(tx.Tx)
		if err != nil {
			return nil, err
		}
		anchorData, _, err := CheckAnchorPkScript(tx.TxIn[0].SignatureScript)
		if err != nil {
			Log.Errorf("CheckAnchorPkScript %s failed", tx.TxID())
			return nil, err
		}

		// 进一步检查输出的invoice
		targetHeight := 0
		for _, txOut := range tx.TxOut {
			if sindexer.IsOpReturn(txOut.PkScript) {
				ctype, data, err := sindexer.ReadDataFromNullDataScript(txOut.PkScript)
				if err == nil {
					switch ctype {
					case sindexer.CONTENT_TYPE_INVOKERESULT:
						url, r, h, err := ParseContractResultInvoice(data)
						if err != nil {
							Log.Errorf("ParseContractResultInvoice failed, %v", err)
							return nil, err
						}
						if url != p.URL() {
							if url != p.RelativePath() {
								return nil, fmt.Errorf("%s not expected contract invoke result tx %s", url, tx.TxID())
							}
						}
						height, err := strconv.ParseInt(h, 10, 32)
						if err != nil {
							return nil, err
						}
						if r != INVOKE_RESULT_DEPOSIT {
							return nil, fmt.Errorf("%s is not a deposit anchor tx", tx.TxID())
						}

						targetHeight = int(height)
					}
				}
			}
		}

		// 到这里可以确定是一个anchorTx，第一个输出是目标地址
		destAddr, err := AddrFromPkScript(tx.TxOut[0].PkScript)
		if err != nil {
			Log.Errorf("AddressFromPkScript %s failed, %v", tx.TxID(), err)
			return nil, err
		}
		items, ok := p.depositMap[destAddr]
		if !ok {
			return nil, fmt.Errorf("invalid destination address %s", destAddr)
		}
		
		bFound := false
		for _, item := range items {
			if item.InUtxo == anchorData.Utxo {
				if item.Done != DONE_NOTYET || item.Reason != INVOKE_REASON_NORMAL {
					continue
				}
				h, _, _ := indexer.FromUtxoId(item.UtxoId)
				if h > targetHeight {
					continue
				}
				maxHeight = max(maxHeight, h)

				if item.AssetName == indexer.ASSET_PLAIN_SAT.String() {
					if len(anchorData.Assets) != 0 {
						return nil, fmt.Errorf("%s assets should be empty", tx.TxID())
					}
					if item.RemainingValue != anchorData.Value {
						return nil, fmt.Errorf("%s invalid value %d, expected %d", tx.TxID(), anchorData.Value, item.RemainingValue)
					}
				} else {
					if len(anchorData.Assets) != 1 {
						return nil, fmt.Errorf("%s should be only one asset", tx.TxID())
					}
					value := indexer.GetBindingSatNum(item.RemainingAmt, uint32(p.N))
					if value != anchorData.Value {
						return nil, fmt.Errorf("%s invalid value %d, expected %d", tx.TxID(), anchorData.Value, value)
					}
					assetInfo := anchorData.Assets[0]
					if assetInfo.Name.String() != item.AssetName {
						return nil, fmt.Errorf("%s invalid asset name %s, expected %s", tx.TxID(), assetInfo.Name.String(), item.AssetName)
					}
					if assetInfo.Amount.Cmp(item.RemainingAmt) != 0 {
						return nil, fmt.Errorf("%s invalid asset amt %s, expected %s", tx.TxID(), assetInfo.Amount.String(), item.RemainingAmt.String())
					}
				}

				sendInfoMap[item.InUtxo] = &SendAssetInfo{
					Address:   item.Address,
					Value:     item.RemainingValue,
					AssetName: assetName,
					AssetAmt:  item.RemainingAmt.Clone(),
				}
				sendTxIdMap[item.InUtxo] = tx.TxID()
				totalAmt = totalAmt.Add(item.RemainingAmt)
				totalValue += item.RemainingValue
				bFound = true
			}
		}
		if !bFound {
			return nil, fmt.Errorf("can't find deposit itme %s", anchorData.Utxo)
		}
	}

	return &DealInfo{
		SendInfo:          sendInfoMap,
		SendTxIdMap:       sendTxIdMap,
		AssetName:         assetName,
		TotalAmt:          totalAmt,
		TotalValue:        totalValue,
		Reason:            INVOKE_RESULT_DEPOSIT,
		Height:            maxHeight,
		InvokeCount:       req.InvokeCount,
		StaticMerkleRoot:  req.StaticMerkleRoot,
		RuntimeMerkleRoot: req.RuntimeMerkleRoot,
	}, nil
}

func (p *SwapContractRuntime) HandleInvokeResult(tx *swire.MsgTx, vout int, result string, more string) {
	if result == INVOKE_RESULT_DEPOSIT {
		// height, err := strconv.Atoi(more)
		// if err != nil {
		// 	Log.Errorf("HandleInvokeResult %s Atoi %s failed", tx.TxID(), more)
		// 	return
		// }

		data, _, err := CheckAnchorPkScript(tx.TxIn[0].SignatureScript)
		if err != nil {
			Log.Errorf("HandleInvokeResult %s CheckAnchorPkScript failed", tx.TxID())
			return
		}

		// 到这里可以确定是一个anchorTx，第一个输出是目标地址
		destAddr, err := AddrFromPkScript(tx.TxOut[0].PkScript)
		if err != nil {
			Log.Errorf("HandleInvokeResult %s AddressFromPkScript failed, %v", tx.TxID(), err)
			return
		}
		items, ok := p.depositMap[destAddr]
		if ok {
			for _, item := range items {
				if item.InUtxo == data.Utxo {
					// 不在这里更新，因为merkle root更新会异常
					// p.updateWithDepositItem(item, tx.TxID())
					// saveReservationWithLock(p.stp.GetDB(), &p.resv.ContractDeployDataInDB)
					// p.mutex.Lock()
					// p.invokeCompleted()
					// p.mutex.Unlock()
					// Log.Infof("item %d %s done.", item.Id, item.InUtxo)
					break
				}
			}
		}
	}
}

// 仅用于swap合约
func VerifySwapHistory(history []*SwapHistoryItem, divisibility int,
	org *SwapContractRunningData) (*SwapContractRunningData, error) {

	InvokeCount := int64(0)
	traderInfoMap := make(map[string]*TraderStatus)
	var runningData SwapContractRunningData

	// 重新生成统计数据
	var onSendingVaue int64
	var onSendngAmt *Decimal
	refundTxMap := make(map[string]bool)
	dealTxMap := make(map[string]bool)
	//depositTxMap := make(map[string]bool)
	for i, item := range history {
		if int64(i) != item.Id {
			return nil, fmt.Errorf("missing history. previous %d, current %d", i-1, item.Id)
		}

		trader, ok := traderInfoMap[item.Address]
		if !ok {
			trader = NewTraderStatus(item.Address, divisibility)
			traderInfoMap[item.Address] = trader
		}
		insertItemToTraderHistroy(&trader.InvokerStatusBase, item)
		trader.DealAmt = trader.DealAmt.Add(item.OutAmt) // 部分成交
		trader.DealValue += item.OutValue                // 部分成交

		InvokeCount++
		runningData.TotalInputSats += item.InValue
		runningData.TotalInputAssets = runningData.TotalInputAssets.Add(item.InAmt)
		if item.Done != DONE_NOTYET {
			runningData.TotalOutputAssets = runningData.TotalOutputAssets.Add(item.OutAmt)
			runningData.TotalOutputSats += item.OutValue
		}

		if item.OrderType == ORDERTYPE_BUY || item.OrderType == ORDERTYPE_SELL {
			if item.Done == DONE_NOTYET {
				if item.Reason == INVOKE_REASON_NORMAL {
					// 有效的，还在交易中，或者交易完成，准备发送
					if item.RemainingAmt.Sign() == 0 && item.RemainingValue == 0 {
						onSendingVaue += item.OutValue
						onSendngAmt = onSendngAmt.Add(item.OutAmt)
					}

					// 在swap时已经确认交易
					if item.OrderType == ORDERTYPE_BUY {
						runningData.TotalDealAssets = runningData.TotalDealAssets.Add(item.OutAmt)
						runningData.SatsValueInPool += item.RemainingValue
					} else if item.OrderType == ORDERTYPE_SELL {
						runningData.TotalDealSats += item.OutValue
						runningData.AssetAmtInPool = runningData.AssetAmtInPool.Add(item.RemainingAmt)
					}

					trader.OnBuyValue += item.RemainingValue
					trader.OnSaleAmt = trader.OnSaleAmt.Add(item.RemainingAmt)
					Log.Infof("Onsale %d: Amt: %s-%s-%s Value: %d-%d-%d Price: %s in: %s", item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
						item.InValue, item.RemainingValue, item.OutValue, item.UnitPrice.String(), item.InUtxo)
				} else {
					// 无效的，即将退款
					Log.Infof("Refund %d: Amt: %s-%s-%s Value: %d-%d-%d Price: %s in: %s", item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
						item.InValue, item.RemainingValue, item.OutValue, item.UnitPrice.String(), item.InUtxo)
					runningData.TotalRefundAssets = runningData.TotalRefundAssets.Add(item.RemainingAmt).Add(item.OutAmt)
					runningData.TotalRefundSats += item.RemainingValue + item.OutValue
				}

			} else if item.Done == DONE_DEALT {
				dealTxMap[item.OutTxId] = true
				runningData.TotalDealCount++
				runningData.TotalDealTx = len(dealTxMap)
				runningData.TotalDealTxFee = int64(runningData.TotalDealTx) * DEFAULT_FEE_SATSNET
				if item.OrderType == ORDERTYPE_BUY {
					runningData.TotalDealAssets = runningData.TotalDealAssets.Add(item.OutAmt)
				} else if item.OrderType == ORDERTYPE_SELL {
					runningData.TotalDealSats += item.OutValue
				}
				// 已经发送
				Log.Infof("Done %d: Amt: %s-%s-%s Value: %d-%d-%d Price: %s in: %s out: %s", item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
					item.InValue, item.RemainingValue, item.OutValue, item.UnitPrice.String(), item.InUtxo, item.OutTxId)
			} else if item.Done == DONE_REFUNDED {
				Log.Infof("Refund %d: Amt: %s-%s-%s Value: %d-%d-%d in: %s out: %s", item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
					item.InValue, item.RemainingValue, item.OutValue, item.InUtxo, item.OutTxId)
				// 退款
				refundTxMap[item.OutTxId] = true
				runningData.TotalRefundTx = len(refundTxMap)
				runningData.TotalRefundTxFee = int64(runningData.TotalRefundTx) * DEFAULT_FEE_SATSNET
				runningData.TotalRefundAssets = runningData.TotalRefundAssets.Add(item.OutAmt)
				runningData.TotalRefundSats += item.OutValue

				if item.OrderType == ORDERTYPE_BUY {
					runningData.TotalDealAssets = runningData.TotalDealAssets.Add(item.OutAmt)
				} else if item.OrderType == ORDERTYPE_SELL {
					runningData.TotalDealSats += item.OutValue
				}

				//runningData.SatsValueInPool -= item.OutValue
				//runningData.AssetAmtInPool = runningData.AssetAmtInPool.Sub(item.OutAmt)
			} else {
				Log.Infof("%s %d: Amt: %s-%s-%s Value: %d-%d-%d in: %s out: %s", item.Reason, item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
					item.InValue, item.RemainingValue, item.OutValue, item.InUtxo, item.OutTxId)
			}
		} else {
			Log.Infof("other(%d) %d: Amt: %s-%s-%s Value: %d-%d-%d in: %s out: %s", item.OrderType, item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
				item.InValue, item.RemainingValue, item.OutValue, item.InUtxo, item.OutTxId)
		}
	}
	runningData.TotalOutputSats += int64(len(refundTxMap)+len(dealTxMap)) * DEFAULT_FEE_SATSNET

	// 对比数据
	Log.Infof("OnSending: value: %d, amt: %s", onSendingVaue, onSendngAmt.String())
	Log.Infof("runningData: \nsimu: %v\nreal: %v", runningData, *org)

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
	if runningData.TotalRefundSats != org.TotalRefundSats {
		Log.Errorf("TotalRefundSats: %d %d", runningData.TotalRefundSats, org.TotalRefundSats)
		err = fmt.Sprintf("%s TotalRefundSats", err)
	}
	if runningData.TotalRefundAssets.Cmp(org.TotalRefundAssets) != 0 {
		Log.Errorf("TotalRefundAssets: %s %s", runningData.TotalRefundAssets.String(), org.TotalRefundAssets.String())
		err = fmt.Sprintf("%s TotalRefundAssets", err)
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

// 仅用于swap合约
func (p *SwapContractRuntime) checkSelf() error {
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
	}

	sort.Slice(mid1, func(i, j int) bool {
		return mid1[i].Id < mid1[j].Id
	})

	// 导出历史记录用于测试
	// if p.GetAssetName().String() == "ordx:f:testordx123" {
	// 	Log.Infof("url: %s\n", url)
	// 	buf, _ := json.Marshal(mid1)
	// 	Log.Infof("items: %s\n", string(buf))
	// 	buf, _ = json.Marshal(p.SwapContractRunningData)
	// 	Log.Infof("running data: %s\n", string(buf))
	// }

	runningData, err := VerifySwapHistory(mid1,
		p.Divisibility, &p.SwapContractRunningData)
	// 更新统计
	p.updateRunningData(runningData)

	if err != nil {
		Log.Errorf(err.Error())
		return err
	}
	return nil
}

func (p *SwapContractRuntime) updateRunningData(runningData *SwapContractRunningData) {
	// p.SwapContractRunningData = runningData

	// p.SwapContractRunningData.AssetAmtInPool = runningData.AssetAmtInPool
	// p.SwapContractRunningData.SatsValueInPool = runningData.SatsValueInPool
	// p.SwapContractRunningData.TotalDealAssets = runningData.TotalDealAssets
	// p.SwapContractRunningData.TotalDealCount = runningData.TotalDealCount
	// p.SwapContractRunningData.TotalDealSats = runningData.TotalDealSats
	// // p.SwapContractRunningData.TotalDealTx = runningData.TotalDealTx
	// // p.SwapContractRunningData.TotalDealTxFee = runningData.TotalDealTxFee
	// p.SwapContractRunningData.TotalDepositAssets = runningData.TotalDepositAssets
	// p.SwapContractRunningData.TotalDepositSats = runningData.TotalDepositSats
	// // p.SwapContractRunningData.TotalDepositTx = runningData.TotalDepositTx
	// // p.SwapContractRunningData.TotalDepositTxFee = runningData.TotalDepositTxFee
	// p.SwapContractRunningData.TotalInputAssets = runningData.TotalInputAssets
	// p.SwapContractRunningData.TotalInputSats = runningData.TotalInputSats
	// p.SwapContractRunningData.TotalRefundAssets = runningData.TotalRefundAssets
	// p.SwapContractRunningData.TotalRefundSats = runningData.TotalRefundSats
	// // p.SwapContractRunningData.TotalRefundTx = runningData.TotalRefundTx
	// // p.SwapContractRunningData.TotalRefundTxFee = runningData.TotalRefundTxFee
	// p.SwapContractRunningData.TotalWithdrawAssets = runningData.TotalWithdrawAssets
	// p.SwapContractRunningData.TotalWithdrawSats = runningData.TotalWithdrawSats
	// // p.SwapContractRunningData.TotalWithdrawTx = runningData.TotalWithdrawTx
	// // p.SwapContractRunningData.TotalWithdrawTxFee = runningData.TotalWithdrawTxFee

	// p.calcAssetMerkleRoot()
	// saveReservation(p.stp.GetDB(), &p.resv.ContractDeployDataInDB)
}
