package wallet

// import (
// 	"encoding/json"
// 	"fmt"
// 	"strings"
// 	"sync"
// 	"time"

// 	"github.com/sat20-labs/satoshinet/txscript"

// 	indexer "github.com/sat20-labs/indexer/common"
// 	"github.com/sat20-labs/indexer/indexer/runes/runestone"
// 	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
// 	stxscript "github.com/sat20-labs/satoshinet/txscript"
// )

// // 仅作为客户端参考定义

// const (
// 	URL_SEPARATOR = "_"

// 	TEMPLATE_CONTRACT_SWAP        string = "swap.tc"
// 	TEMPLATE_CONTRACT_AMM         string = "amm.tc"
// 	TEMPLATE_CONTRACT_VAULT       string = "vault.tc"
// 	TEMPLATE_CONTRACT_LAUNCHPOOL  string = "launchpool.tc"
// 	TEMPLATE_CONTRACT_STAKE       string = "stake.tc"
// 	TEMPLATE_CONTRACT_TRANSCEND   string = "transcend.tc" // 支持任意资产进出通道，优先级比 TEMPLATE_CONTRACT_AMM 低
// 	TEMPLATE_CONTRACT_MINER_STAKE string = "minerstake.tc"

// 	CONTRACT_STATUS_EXPIRED int = -2
// 	CONTRACT_STATUS_CLOSED  int = -1
// 	CONTRACT_STATUS_INIT    int = 0
// 	// ...   deploying status
// 	CONTRACT_STATUS_READY 	int = 100 // 正常工作阶段
// 	// ...   running status，101-199，合约正常运行时的自定义状态
// 	CONTRACT_STATUS_STAKING int = 101

// 	CONTRACT_STATUS_CLOSING int = 200 // 进入最后的关闭阶段
// 	// ...   closing status, at last change to CONTRACT_STATUS_CLOSED or CONTRACT_STATUS_EXPIRED

// 	INVOKE_API_ENABLE string = "enable" // 每个合约的第一个调用，用来激活合约

// 	INVOKE_RESULT_OK       string = "ok"
// 	INVOKE_RESULT_REFUND   string = "refund"
// 	INVOKE_RESULT_DEAL     string = "deal"
// 	INVOKE_RESULT_DEPOSIT  string = "deposit"
// 	INVOKE_RESULT_WITHDRAW string = "withdraw"
// 	INVOKE_RESULT_DEANCHOR string = "deanchor"
// 	INVOKE_RESULT_ANCHOR   string = "anchor"
// 	INVOKE_RESULT_STAKE    string = "stake"
// 	INVOKE_RESULT_UNSTAKE  string = "unstake"
// )


// const (
// 	INVOKE_API_SWAP     string = "swap"
// 	INVOKE_API_REFUND   string = "refund"
// 	INVOKE_API_FUND     string = "fund"     //
// 	INVOKE_API_DEPOSIT  string = "deposit"  // L1->L2  免费
// 	INVOKE_API_WITHDRAW string = "withdraw" // L2->L1  收
// 	INVOKE_API_STAKE    string = "stake"    // 如果是L1的stake，必须要有op_return携带invokeParam，否则会被认为是deposit
// 	INVOKE_API_UNSTAKE  string = "unstake"  // 可以unstake到一层

// 	ORDERTYPE_SELL     = 1
// 	ORDERTYPE_BUY      = 2
// 	ORDERTYPE_REFUND   = 3
// 	ORDERTYPE_FUND     = 4
// 	ORDERTYPE_PROFIT   = 5
// 	ORDERTYPE_DEPOSIT  = 6
// 	ORDERTYPE_WITHDRAW = 7
// 	ORDERTYPE_MINT     = 8
// 	ORDERTYPE_STAKE    = 9
// 	ORDERTYPE_UNSTAKE  = 10
// 	ORDERTYPE_UNUSED   = 11

// 	INVOKE_FEE          int64 = 10
// 	SWAP_INVOKE_FEE     int64 = 10
// 	DEPOSIT_INVOKE_FEE  int64 = DEFAULT_SERVICE_FEE_DEPOSIT
// 	WITHDRAW_INVOKE_FEE int64 = DEFAULT_SERVICE_FEE_WITHDRAW

// 	MAX_PRICE_DIVISIBILITY = 10
// 	MAX_ASSET_DIVISIBILITY = 10

// 	BUCK_SIZE        = 100

// 	SWAP_SERVICE_FEE_RATIO = 8 // 千分之
// 	DEPTH_SLOT             = 10
// )

// const (
// 	DONE_NOTYET          = 0
// 	DONE_DEALT           = 1
// 	DONE_REFUNDED        = 2
// 	DONE_CLOSED_DIRECTLY = 3
// 	DONE_CANCELLED       = 4
// )


// const (
// 	INVOKE_REASON_NORMAL           string = ""
// 	INVOKE_REASON_REFUND           string = "refund"  // 退款
// 	INVOKE_REASON_CANCEL           string = "cancel"  // 不需要退款，指令取消
// 	INVOKE_REASON_INVALID          string = "invalid"     // 参数错误
// 	INVOKE_REASON_INNER_ERROR      string = "inner error" // 内部错误
// 	INVOKE_REASON_NO_ENOUGH_ASSET  string = "no enough asset"
// 	INVOKE_REASON_SLIPPAGE_PROTECT string = "slippage protection"
// 	INVOKE_REASON_UTXO_NOT_FOUND   string = "input utxo not found"
// )

// const (
// 	ERR_MERKLE_ROOT_INCONSISTENT = "contract runtime merkle root is inconsistent"
// )

// // 用在开发过程修改数据库，设置为true，然后数据库自动升级，然后马上要设置为false，并且将所有oldversion的数据结果，等同于最新结构
// const ContractRuntimeBaseUpgrade = false

// type ActionFunc func(*any, any, any) (any, error)

// type ContractDeployAction struct {
// 	Action ActionFunc
// 	Name   string
// }

// type Contract interface {
// 	GetTemplateName() string          // 合约模版名称
// 	GetAssetName() *indexer.AssetName // 资产名称
// 	GetContractName() string          // 资产名称_模版名称
// 	CheckContent() error              // 合约内容检查，部署前调用
// 	Content() string                  // 合约内容， json格式
// 	InvokeParam(string) string        // 调用合约的参数， json格式
// 	GetContractBase() *ContractBase

// 	Encode() ([]byte, error) // 合约内容， script格式
// 	Decode([]byte) error     // 合约内容， script格式

// 	GetStartBlock() int
// 	GetEndBlock() int

// 	DeployFee(feeRate int64) int64 // in satsnet，部署这个合约的人需要支付的费用
// }

// type ContractRuntime interface {
// 	Contract

// 	InitFromContent([]byte, *Manager) error // 根据合约模版参数初始化合约，非json
// 	GetRuntimeBase() *ContractRuntimeBase

// 	GetStatus() int
// 	Address() string      // 合约钱包地址
// 	URL() string          // 绝对路径
// 	RelativePath() string // 相对路径，不包括channelId或者其他
// 	GetAssetName() *indexer.AssetName
// 	GetAssetNameV2() *AssetName
// 	RuntimeContent() []byte                 // 运行时数据，用于备份数据
// 	InstallStatus() string                  // 运行时状态，json格式
// 	RuntimeStatus() string                  // 运行时状态，json格式
// 	RuntimeAnalytics() string               // 运行时状态，json格式
// 	InvokeHistory(any, int, int) string     // 调用历史记录，可以增加过滤条件
// 	AllAddressInfo(int, int) string         // 所有地址信息，json格式
// 	StatusByAddress(string) (string, error) // 运行时状态，json格式
// 	GetDeployTime() int64
// 	IsExpired() bool
// 	GetEnableBlock() int
// 	GetEnableBlockL1() int

// 	IsActive() bool // >=ready && enabled
// 	IsReady() bool  // ready && enabled


// 	// 合约调用的支持接口
// 	CheckInvokeParam(string) (int64, error) // 调用合约的参数检查(json)，调用合约前调用
// 	AllowInvoke(*Manager) error
// }

// // 合约调用历史记录
// type InvokeHistoryItem interface {
// 	GetVersion() int
// 	GetId() int64
// 	GetKey() string
// 	HasDone() bool
// 	GetHeight() int
// 	FromSatsNet() bool
// 	ToSatsNet() bool
// 	ToNewVersion() InvokeHistoryItem
// }

// type InvokeHistoryItemBase struct {
// 	Version int
// 	Id      int64
// 	Reason  string // "" 表示一切正常，按照期望完成
// 	Done    int    // 0 进行中；1，交易完成; 2，退款； 3，直接关闭
// }

// func (p *InvokeHistoryItemBase) GetVersion() int {
// 	return p.Version
// }

// func (p *InvokeHistoryItemBase) GetId() int64 {
// 	return p.Id
// }

// func (p *InvokeHistoryItemBase) GetKey() string {
// 	return GetKeyFromId(p.Id)
// }

// func (p *InvokeHistoryItemBase) HasDone() bool {
// 	return p.Done != DONE_NOTYET
// }

// func (p *InvokeHistoryItemBase) GetHeight() int {
// 	return -1
// }

// func (p *InvokeHistoryItemBase) FromSatsNet() bool {
// 	return true
// }

// func (p *InvokeHistoryItemBase) ToSatsNet() bool {
// 	return true
// }

// func (p *InvokeHistoryItemBase) ToNewVersion() InvokeHistoryItem {
// 	return p
// }

// func GetKeyFromId(id int64) string {
// 	return fmt.Sprintf("%012d", id)
// }

// func NewInvokeHistoryItem(cn string) InvokeHistoryItem {
// 	switch cn {
// 	case TEMPLATE_CONTRACT_SWAP:
// 		return &SwapHistoryItem{}
// 	case TEMPLATE_CONTRACT_LAUNCHPOOL:
// 		return &MintHistoryItem{}
// 	case TEMPLATE_CONTRACT_AMM:
// 		return &SwapHistoryItem{}
// 	}
// 	return &InvokeItem{}
// }

// func NewInvokeHistoryItem_old(cn string) InvokeHistoryItem {
// 	// switch cn {
// 	// case TEMPLATE_CONTRACT_SWAP:
// 	// 	return &SwapHistoryItem_old{}
// 	// case TEMPLATE_CONTRACT_LAUNCHPOOL:
// 	// 	return &MintHistoryItem_old{}
// 	// }
// 	return &InvokeItem_old{}
// }

// type InvokeItem_old = InvokeItem

// // type InvokeItem_old struct {
// // 	Version        int
// // 	Id             int64
// // 	OrderType      int    //
// // 	UtxoId         uint64 // 其实是utxoId
// // 	OrderTime      int64
// // 	AssetName      string
// // 	UnitPrice      *Decimal // X per Y
// // 	ExpectedAmt    *Decimal // 期望的数量
// // 	Address        string   // 所有人
// // 	FromL1         bool     // 是否主网的调用，默认是false
// // 	InUtxo         string   // sell or buy 的utxo
// // 	InValue        int64    // 白聪，不包括资产聪, 去掉手续费
// // 	InAmt          *Decimal
// // 	RemainingAmt   *Decimal // 要买或者卖的资产的剩余数量
// // 	RemainingValue int64    // 用来买资产的聪的剩余数量
// // 	OutTxId        string   // 回款的TxId，可能是成交后汇款，也可能是撤销后的回款
// // 	OutAmt         *Decimal // 买到的资产，或者
// // 	OutValue       int64    // 卖出得到的聪
// // 	Valid          bool
// // 	Done           int // 0 交易中；1，交易完成，2，退款
// // }

// // func (p *InvokeItem_old) ToNewVersion() InvokeHistoryItem {
// // 	return &InvokeItem{}
// // }

// type InvokeItem struct {
// 	InvokeHistoryItemBase

// 	OrderType      int    //
// 	UtxoId         uint64 // 其实是utxoId
// 	OrderTime      int64
// 	AssetName      string
// 	ServiceFee     int64
// 	UnitPrice      *Decimal // X per Y
// 	ExpectedAmt    *Decimal // 期望的数量
// 	Address        string   // 所有人
// 	FromL1         bool     // InUtxo是否主网的调用，默认是false
// 	InUtxo         string   // 调用合约的Utxo
// 	InValue        int64    // InUtxo的白聪，不包括资产聪，包括手续费
// 	InAmt          *Decimal // InUtxo的资产，每个utxo只能使用一种资产来和合约交互
// 	RemainingAmt   *Decimal // 输入资产扣除费用后，能参与合约的资产，动态数据，比如要买或者卖的资产的剩余数量
// 	RemainingValue int64    // 输入资产扣除费用后，能参与合约的聪，动态数据，比如用来买资产的聪的剩余数量
// 	ToL1           bool     // OutTxId是否主网的调用，默认是false
// 	OutTxId        string   // 输出的TxId，可能是成交后汇款，也可能是撤销后的回款
// 	OutAmt         *Decimal // 合约交互结果，比如买到的资产
// 	OutValue       int64    // 合约交互结果，卖出得到的聪，扣除服务费

// 	// 增加
// 	Padded		   []byte   // 扩展使用
// }

// func (p *InvokeItem) ToNewVersion() InvokeHistoryItem {
// 	return p
// }

// func (p *InvokeItem) GetHeight() int {
// 	h, _, _ := indexer.FromUtxoId(p.UtxoId)
// 	return h
// }

// func (p *InvokeItem) FromSatsNet() bool {
// 	return !p.FromL1
// }

// func (p *InvokeItem) ToSatsNet() bool {
// 	return !p.ToL1
// }

// func (p *InvokeItem) Clone() *InvokeItem {
// 	n := *p
// 	n.UnitPrice = p.UnitPrice.Clone()
// 	n.ExpectedAmt = p.ExpectedAmt.Clone()
// 	n.InAmt = p.InAmt.Clone()
// 	n.RemainingAmt = p.RemainingAmt.Clone()
// 	n.OutAmt = p.OutAmt.Clone()
// 	return &n
// }

// type InvokeParam struct {
// 	Action string `json:"action"`
// 	Param  string `json:"param,omitempty"` // 外部使用时是json，内部使用时是编码过的string
// }

// func (p *InvokeParam) Encode() ([]byte, error) {
// 	return txscript.NewScriptBuilder().
// 		AddData([]byte(p.Action)).
// 		AddData([]byte(p.Param)).Script()
// }

// func (p *InvokeParam) Decode(data []byte) error {
// 	tokenizer := txscript.MakeScriptTokenizer(0, data)
// 	if !tokenizer.Next() || tokenizer.Err() != nil {
// 		return fmt.Errorf("missing action")
// 	}
// 	p.Action = string(tokenizer.Data())

// 	if !tokenizer.Next() || tokenizer.Err() != nil {
// 		return fmt.Errorf("missing parameter")
// 	}
// 	p.Param = string(tokenizer.Data())
// 	return nil
// }

// type EnableInvokeParam struct {
// 	HeightL1 int `json:"heightL1"`
// 	HeightL2 int `json:"heightL2"`
// }

// func (p *EnableInvokeParam) Encode() ([]byte, error) {
// 	return stxscript.NewScriptBuilder().
// 		AddInt64(int64(p.HeightL1)).
// 		AddInt64(int64(p.HeightL2)).Script()
// }

// func (p *EnableInvokeParam) Decode(data []byte) error {
// 	tokenizer := stxscript.MakeScriptTokenizer(0, data)

// 	if !tokenizer.Next() || tokenizer.Err() != nil {
// 		return fmt.Errorf("missing heightL1")
// 	}
// 	p.HeightL1 = int(tokenizer.ExtractInt64())

// 	if !tokenizer.Next() || tokenizer.Err() != nil {
// 		return fmt.Errorf("missing heightL2")
// 	}
// 	p.HeightL2 = int(tokenizer.ExtractInt64())

// 	return nil
// }

// type InvokerStatus interface {
// 	GetVersion() int
// 	GetKey() string
// }

// type InvokerStatusBase struct {
// 	Version       int
// 	Address       string
// 	InvokeCount   int
// 	DepositAmt    *Decimal // 存款总额
// 	DepositValue  int64
// 	WithdrawAmt   *Decimal // 取款总额
// 	WithdrawValue int64
// 	RefundAmt     *Decimal // 无效退回，不计入存款总额
// 	RefundValue   int64

// 	DepositUtxoMap  map[string]bool //
// 	WithdrawUtxoMap map[string]bool // utxo map
// 	RefundUtxoMap   map[string]bool // utxo map 要退款的记录，包括指令utxo和要退款的utxo
// 	History         map[int][]int64 // 用户的invoke历史记录，每100个为一桶，用InvokeCount计算 TODO 目前统一一块存储，数据量大了后要分桶保存，用到才加载
// 	UpdateTime      int64
// }

// func NewInvokerStatusBase(address string, divisibility int) *InvokerStatusBase {
// 	return &InvokerStatusBase{
// 		Address:     address,
// 		RefundAmt:   indexer.NewDecimal(0, divisibility),
// 		DepositAmt:  indexer.NewDecimal(0, divisibility),
// 		WithdrawAmt: indexer.NewDecimal(0, divisibility),

// 		RefundUtxoMap:   make(map[string]bool),
// 		DepositUtxoMap:  make(map[string]bool),
// 		WithdrawUtxoMap: make(map[string]bool),
// 		History:         make(map[int][]int64),
// 		UpdateTime:      time.Now().Unix(),
// 	}
// }

// func (p *InvokerStatusBase) GetVersion() int {
// 	return p.Version
// }

// func (p *InvokerStatusBase) GetKey() string {
// 	return p.Address
// }

// func NewInvokerStatus(cn string) InvokerStatus {
// 	switch cn {
// 	case TEMPLATE_CONTRACT_SWAP, TEMPLATE_CONTRACT_AMM:
// 		return &TraderStatus{}
// 	case TEMPLATE_CONTRACT_LAUNCHPOOL:
// 		return nil
// 	case TEMPLATE_CONTRACT_VAULT:
// 		return &VaultInvokerStatus{}
// 	}
// 	return &TraderStatus{}
// }

// // 合约内容基础结构
// type ContractBase struct {
// 	TemplateName string            `json:"contractType"`
// 	AssetName    indexer.AssetName `json:"assetName"`
// 	StartBlock   int               `json:"startBlock"` // 0，部署即可以使用
// 	EndBlock     int               `json:"endBlock"`   // 0，部署后永久可用，除非合约其他规则
// 	//Sendback     bool   `json:"sendBack"` // send back when invoke fail(only send in satsnet)
// }

// func (p *ContractBase) CheckContent() error {
// 	if p.AssetName.Protocol != indexer.PROTOCOL_NAME_ORDX &&
// 		p.AssetName.Protocol != indexer.PROTOCOL_NAME_RUNES {
// 		return fmt.Errorf("invalid protocol %s", p.AssetName.Protocol)
// 	}
// 	if p.AssetName.Ticker == "" {
// 		return fmt.Errorf("invalid asset name %s", p.AssetName.Ticker)
// 	}
// 	if p.AssetName.Type != indexer.ASSET_TYPE_FT {
// 		return fmt.Errorf("invalid asset type %s", p.AssetName.Type)
// 	}

// 	if p.AssetName.Protocol == indexer.PROTOCOL_NAME_ORDX {
// 		if !indexer.IsValidSat20Name(p.AssetName.Ticker) {
// 			return fmt.Errorf("invalid asset name %s", p.AssetName.Ticker)
// 		}
// 		p.AssetName.Ticker = strings.ToLower(p.AssetName.Ticker)
// 	} else if p.AssetName.Protocol == indexer.PROTOCOL_NAME_RUNES {
// 		// 为了简化处理，我们简单拒绝“.”
// 		if strings.Contains(p.AssetName.Ticker, ".") {
// 			return fmt.Errorf("\".\" not allowed, use • instead")
// 		}
// 		_, err := runestone.SpacedRuneFromString(p.AssetName.Ticker)
// 		if err != nil {
// 			return fmt.Errorf("invalid asset name %s", p.AssetName.Ticker)
// 		}
// 	}

// 	if indexer.IsPlainAsset(&p.AssetName) {
// 		return fmt.Errorf("should be one asset")
// 	}
// 	return nil
// }

// func (p *ContractBase) GetContractBase() *ContractBase {
// 	return p
// }

// func (p *ContractBase) Content() string {
// 	buf, err := json.Marshal(p)
// 	if err != nil {
// 		Log.Panicf("Marshal ContractBase failed, %v", err)
// 	}
// 	return string(buf)
// }

// func (p *ContractBase) Encode() ([]byte, error) {
// 	return stxscript.NewScriptBuilder().
// 		AddData([]byte(p.TemplateName)).
// 		AddData([]byte(p.AssetName.String())).
// 		AddInt64(int64(p.StartBlock)).
// 		AddInt64(int64(p.EndBlock)).Script()
// }

// func (p *ContractBase) Decode(data []byte) error {
// 	tokenizer := stxscript.MakeScriptTokenizer(0, data)

// 	if !tokenizer.Next() || tokenizer.Err() != nil {
// 		return fmt.Errorf("missing template name")
// 	}
// 	p.TemplateName = string(tokenizer.Data())

// 	if !tokenizer.Next() || tokenizer.Err() != nil {
// 		return fmt.Errorf("missing asset name")
// 	}
// 	assetName := string(tokenizer.Data())
// 	p.AssetName = *indexer.NewAssetNameFromString(assetName)

// 	if !tokenizer.Next() || tokenizer.Err() != nil {
// 		return fmt.Errorf("missing contract start block")
// 	}
// 	p.StartBlock = int(tokenizer.ExtractInt64())

// 	if !tokenizer.Next() || tokenizer.Err() != nil {
// 		return fmt.Errorf("missing contract end block")
// 	}
// 	p.EndBlock = int(tokenizer.ExtractInt64())
// 	return nil
// }

// func (p *ContractBase) GetTemplateName() string {
// 	return p.TemplateName
// }

// func (p *ContractBase) GetAssetName() *indexer.AssetName {
// 	return &p.AssetName
// }

// func (p *ContractBase) GetStartBlock() int {
// 	return p.StartBlock
// }

// func (p *ContractBase) GetEndBlock() int {
// 	return p.EndBlock
// }

// func (p *ContractBase) DeployFee(feeRate int64) int64 {
// 	return 0
// }

// func (p *ContractBase) InvokeParam(string) string {
// 	return ""
// }

// // 合约运行时基础结构，合约区块以聪网为主，主网区块辅助使用
// type ContractRuntimeBase struct {
// 	DeployTime    int64  `json:"deployTime"` // s
// 	Status        int    `json:"status"`
// 	EnableBlock   int    `json:"enableBlock"`    // 合约在哪个区块进入ready状态
// 	CurrBlock     int    `json:"currentBlock"`   // 合约区块不能跳，必须一块一块执行，即使EnableBlock还没到，也要同步
// 	EnableBlockL1 int    `json:"enableBlockL1"`  // 合约在哪个区块进入ready状态
// 	CurrBlockL1   int    `json:"currentBlockL1"` // 合约区块不能跳，必须从EnableBlock开始，一块一块执行
// 	EnableTxId    string `json:"enableTxId"`     // 只设置，暂时没有用起来
// 	Deployer      string `json:"deployer"`
// 	ResvId        int64  `json:"resvId"`
// 	ChannelAddr   string `json:"channelAddr"`
// 	InvokeCount   int64  `json:"invokeCount"`
// 	Divisibility  int    `json:"divisibility"`
// 	N             int    `json:"n"`

// 	CheckPoint          int64  // 上个与peer端校验过merkleRoot的invokeCount
// 	StaticMerkleRoot    []byte // 合约静态数据
// 	AssetMerkleRoot     []byte //
// 	CurrAssetMerkleRoot []byte // 上个检查的资产状态数据。每个InvokeCount会有两次计算的机会 invokeCompleted 计算时，合约发起的交易还没被确认，在下一个invokeCompleted时才真正是当前调用次数的结果

// 	CheckPointBlock   int
// 	CheckPointBlockL1 int
// 	LocalPubKey       []byte
// 	RemotePubKey      []byte

// 	stp      *Manager
// 	contract Contract
// 	runtime  ContractRuntime
// 	history  map[string]*InvokeItem

// 	mutex sync.RWMutex
// }

// func (p *ContractRuntimeBase) ToNewVersion() *ContractRuntimeBase {
// 	return p
// }

// func (p *ContractRuntimeBase) GetAssetNameV2() *AssetName {
// 	return &AssetName{
// 		AssetName: *p.contract.GetAssetName(),
// 		N:         p.N,
// 	}
// }

// func (p *ContractRuntimeBase) InitFromContent(content []byte, stp *Manager) error {

// 	// p.ChannelId = resv.ChannelId
// 	// p.Deployer = resv.Deployer
// 	p.stp = stp
// 	p.history = make(map[string]*InvokeItem)
	
// 	err := p.contract.Decode(content)
// 	if err != nil {
// 		return err
// 	}
// 	err = p.contract.CheckContent()
// 	if err != nil {
// 		return err
// 	}

// 	tickInfo := stp.getTickerInfo(p.contract.GetAssetName())
// 	if tickInfo != nil {
// 		p.Divisibility = tickInfo.Divisibility
// 		p.N = tickInfo.N
// 	} else {
// 		tc := p.contract.GetTemplateName()
// 		switch tc {
// 		case TEMPLATE_CONTRACT_TRANSCEND:
// 			p.Divisibility = MAX_ASSET_DIVISIBILITY
// 			p.N = 0
// 		case TEMPLATE_CONTRACT_LAUNCHPOOL:
// 		default:
// 			return fmt.Errorf("%s can't find ticker %s", p.URL(), tc)
// 		}
// 		// 发射池合约肯定找不到，但本身就有足够的数据
// 		// 由合约自己设置
// 	}

// 	return nil
// }

// func (p *ContractRuntimeBase) GetRuntimeBase() *ContractRuntimeBase {
// 	return p
// }

// func (p *ContractRuntimeBase) GetDeployTime() int64 {
// 	return p.DeployTime
// }

// func (p *ContractRuntimeBase) DeploySelf() bool {
// 	return false
// }

// func (p *ContractRuntimeBase) Address() string {
// 	return p.ChannelAddr
// }

// func (p *ContractRuntimeBase) URL() string {
// 	return p.ChannelAddr + URL_SEPARATOR + p.contract.GetContractName()
// }

// func (p *ContractRuntimeBase) RelativePath() string {
// 	return p.contract.GetContractName()
// }

// func (p *ContractRuntimeBase) GetStatus() int {
// 	return p.Status
// }

// func (p *ContractRuntimeBase) GetEnableBlock() int {
// 	return p.EnableBlock
// }

// func (p *ContractRuntimeBase) GetEnableBlockL1() int {
// 	return p.EnableBlockL1
// }

// func (p *ContractRuntimeBase) IsExpired() bool {
// 	if p.contract.GetEndBlock() <= 0 {
// 		return false
// 	}
// 	return p.CurrBlock > p.contract.GetEndBlock()
// }

// func (p *ContractRuntimeBase) GetDeployer() string {
// 	return p.Deployer
// }

// func (p *ContractRuntimeBase) UnconfirmedTxId() string {
// 	return ""
// }

// func (p *ContractRuntimeBase) UnconfirmedTxId_SatsNet() string {
// 	return ""
// }

// func (p *ContractRuntimeBase) IsReady() bool {
// 	return p.Status == CONTRACT_STATUS_READY &&
// 	p.CurrBlock >= p.EnableBlock
// }

// func (p *ContractRuntimeBase) IsActive() bool {
// 	return p.Status >= CONTRACT_STATUS_READY && 
// 	p.Status < CONTRACT_STATUS_CLOSING &&
// 	p.CurrBlock >= p.EnableBlock
// }

// func (p *ContractRuntimeBase) CheckInvokeParam(string) (int64, error) {
// 	return 0, nil
// }

// func (p *ContractRuntimeBase) AllowInvoke(stp *Manager) error {

// 	// resv, ok := r.(*ContractDeployReservation)
// 	// if !ok {
// 	// 	return fmt.Errorf("not ContractDeployReservation")
// 	// }
// 	if p.Status < CONTRACT_STATUS_READY {
// 		return fmt.Errorf("contract not ready")
// 	}

// 	if p.EnableBlock == 0 {
// 		return fmt.Errorf("contract enable block not set yet")
// 	}

// 	if p.CurrBlock < p.EnableBlock {
// 		return fmt.Errorf("contract not enabled")
// 	}

// 	if p.contract.GetStartBlock() != 0 {
// 		if p.CurrBlock < p.contract.GetStartBlock() {
// 			return fmt.Errorf("not reach start block")
// 		}
// 	}
// 	if p.contract.GetEndBlock() != 0 {
// 		if p.CurrBlock > p.contract.GetEndBlock() {
// 			return fmt.Errorf("exceed the end block")
// 		}
// 	}

// 	return nil
// }

// func (p *ContractRuntimeBase) RuntimeContent() []byte {
// 	return nil
// }

// func (p *ContractRuntimeBase) InstallStatus() string {
// 	return ""
// }

// func (p *ContractRuntimeBase) RuntimeStatus() string {
// 	return ""
// }

// func (p *ContractRuntimeBase) RuntimeAnalytics() string {
// 	return ""
// }

// func (p *ContractRuntimeBase) InvokeHistory(any, int, int) string {
// 	return ""
// }

// func (p *ContractRuntimeBase) AllAddressInfo(int, int) string {
// 	return ""
// }

// func (p *ContractRuntimeBase) StatusByAddress(address string) (string, error) {
// 	return "", fmt.Errorf("not implemented")
// }

// func (p *ContractRuntimeBase) AllowPeerAction(*Manager, string, any) (any, error) {
// 	return nil, fmt.Errorf("not allow")
// }

// func (p *ContractRuntimeBase) SetPeerActionResult(*Manager, string, any) {

// }

// func NewContract(cname string) Contract {
// 	switch cname {
// 	case TEMPLATE_CONTRACT_SWAP:
// 		return NewSwapContract()

// 	case TEMPLATE_CONTRACT_AMM:
// 		return NewAmmContract()

// 	case TEMPLATE_CONTRACT_VAULT:
// 		return NewVaultContract()

// 	case TEMPLATE_CONTRACT_LAUNCHPOOL:
// 		return NewLaunchPoolContract()

// 	case TEMPLATE_CONTRACT_TRANSCEND:
// 		return NewTranscendContract()
// 	}
// 	return nil
// }

// func ContractContentUnMarsh(cname string, jsonStr string) (Contract, error) {
// 	c := NewContract(cname)
// 	if c == nil {
// 		return nil, fmt.Errorf("invalid contract name %s", cname)
// 	}
// 	err := json.Unmarshal([]byte(jsonStr), &c)
// 	if err != nil {
// 		return nil, err
// 	}

// 	err = c.CheckContent()
// 	if err != nil {
// 		return nil, err
// 	}

// 	return c, nil
// }

// func NewContractRuntime(stp *Manager, cname string) ContractRuntime {
// 	var r ContractRuntime
// 	switch cname {
// 	case TEMPLATE_CONTRACT_SWAP:
// 		return NewSwapContractRuntime(stp)

// 	case TEMPLATE_CONTRACT_AMM:
// 		return NewAmmContractRuntime(stp)

// 	case TEMPLATE_CONTRACT_VAULT:
// 		return NewVaultContractRuntime(stp)

// 	case TEMPLATE_CONTRACT_LAUNCHPOOL:
// 		r = NewLaunchPoolContractRuntime(stp)

// 	case TEMPLATE_CONTRACT_TRANSCEND:
// 		r = NewTranscendContractRuntime(stp)
// 	}

// 	return r
// }

// func ContractRuntimeUnMarsh(stp *Manager, cname string, data []byte) (ContractRuntime, error) {
// 	c := NewContractRuntime(stp, cname)
// 	if c == nil {
// 		return nil, fmt.Errorf("invalid contract name %s", cname)
// 	}
// 	err := DecodeFromBytes(data, c)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return c, nil
// }

// func GenerateContractURl(channelId, assetName, tc string) string {
// 	return channelId + URL_SEPARATOR + assetName + URL_SEPARATOR + tc
// }

// func GenerateContractRelativePath(assetName, tc string) string {
// 	return assetName + URL_SEPARATOR + tc
// }

// func ExtractChannelId(url string) string {
// 	parts := strings.Split(url, URL_SEPARATOR)
// 	if len(parts) < 3 {
// 		return ""
// 	}
// 	return parts[0]
// }

// func ExtractAssetName(url string) string {
// 	parts := strings.Split(url, URL_SEPARATOR)
// 	if len(parts) < 2 {
// 		return ""
// 	}
// 	if len(parts) < 3 {
// 		return parts[0]
// 	}
// 	return parts[1]
// }

// func ExtractContractType(url string) string {
// 	parts := strings.Split(url, URL_SEPARATOR)
// 	if len(parts) == 0 {
// 		return ""
// 	}
// 	return parts[len(parts)-1]
// }

// func ExtractRelativePath(url string) string {
// 	parts := strings.Split(url, URL_SEPARATOR)
// 	if len(parts) < 2 {
// 		return url
// 	}
// 	return parts[1] + URL_SEPARATOR + parts[2]
// }

// // channelId, assetName, contractType
// func ParseContractURL(url string) (string, string, string, error) {
// 	parts := strings.Split(url, URL_SEPARATOR)
// 	// channelid-assetname-type
// 	if len(parts) == 3 {
// 		return parts[0], parts[1], parts[2], nil
// 	}
// 	if len(parts) == 2 {
// 		// assetname, type
// 		return "", parts[0], parts[1], nil
// 	}
// 	if len(parts) == 1 {
// 		return "", "", parts[0], nil
// 	}

// 	return "", "", "", fmt.Errorf("invalid format of contract url. %s", url)
// }

// // full path
// func IsValidContractURL(url string) bool {
// 	parts := strings.Split(url, URL_SEPARATOR)
// 	// channelid-assetname-type
// 	if len(parts) != 3 {
// 		return false
// 	}
// 	if len(parts[0]) == 0 || len(parts[1]) == 0 || len(parts[2]) == 0 {
// 		return false
// 	}
// 	// check template type ?
// 	return true
// }


// // 合约部署
// func UnsignedDeployContractInvoiceV2(data *sindexer.ContractDeployData) ([]byte, error) {
// 	return stxscript.NewScriptBuilder().
// 		AddData([]byte(data.ContractPath)).
// 		AddData([]byte(data.ContractContent)).
// 		AddInt64(int64(data.DeployTime)).Script()
// }

// func SignedDeployContractInvoiceV2(data *sindexer.ContractDeployData) ([]byte, error) {
// 	return stxscript.NewScriptBuilder().
// 		AddData([]byte(data.ContractPath)).
// 		AddData([]byte(data.ContractContent)).
// 		AddInt64(int64(data.DeployTime)).
// 		AddData(data.LocalSign).
// 		AddData(data.RemoteSign).Script()
// }

// func UnsignedContractEnabledInvoice(url string, heightL1, heightL2 int, pubKey []byte) ([]byte, error) {
// 	return stxscript.NewScriptBuilder().
// 		AddData([]byte(url)).
// 		AddInt64(int64(heightL1)).
// 		AddInt64(int64(heightL2)).
// 		AddData(pubKey).Script()
// }

// // 用在有特别需求的合约
// func SignedContractEnabledInvoice(url string, heightL1, heightL2 int, pubKey []byte, sig []byte) ([]byte, error) {
// 	return stxscript.NewScriptBuilder().
// 		AddData([]byte(url)).
// 		AddInt64(int64(heightL1)).
// 		AddInt64(int64(heightL2)).
// 		AddData(pubKey).
// 		AddData(sig).Script()
// }

// // 合约调用：大多数都只需要这个简化的调用参数
// func AbbrInvokeContractInvoice(data *sindexer.ContractInvokeData) ([]byte, error) {
// 	return stxscript.NewScriptBuilder().
// 		AddData([]byte(data.ContractPath)).
// 		AddData([]byte(data.InvokeParam)).Script()
// }

// func UnsignedInvokeContractInvoice(data *sindexer.ContractInvokeData) ([]byte, error) {
// 	return stxscript.NewScriptBuilder().
// 		AddData([]byte(data.ContractPath)).
// 		AddData([]byte(data.InvokeParam)).
// 		AddData(data.PubKey).Script()
// }

// // 用在有特别需求的合约
// func SignedInvokeContractInvoice(data *sindexer.ContractInvokeData, sig []byte) ([]byte, error) {
// 	return stxscript.NewScriptBuilder().
// 		AddData([]byte(data.ContractPath)).
// 		AddData([]byte(data.InvokeParam)).
// 		AddData(data.PubKey).
// 		AddData(sig).Script()
// }

// // 简化的调用invoice，仅用于主网
// func UnsignedAbbrInvokeContractInvoice(contractUrl string, param *InvokeParam) ([]byte, error) {

// 	_, asetName, templateName, err := ParseContractURL(contractUrl)
// 	if err != nil {
// 		return nil, err
// 	}

// 	return stxscript.NewScriptBuilder().
// 		AddData([]byte(templateName)).
// 		AddData([]byte(asetName)).
// 		AddData([]byte(param.Action)).Script()
// }

// func SignedAbbrInvokeContractInvoice(contractUrl string, param *InvokeParam, sig []byte) ([]byte, error) {

// 	_, asetName, templateName, err := ParseContractURL(contractUrl)
// 	if err != nil {
// 		return nil, err
// 	}

// 	hashCode := sig[:8]

// 	return stxscript.NewScriptBuilder().
// 		AddData([]byte(templateName)).
// 		AddData([]byte(asetName)).
// 		AddData([]byte(param.Action)).
// 		AddData([]byte(hashCode)).Script()
// }

// // 合约调用结果
// func UnsignedContractResultInvoice(contractPath string, result string, more string) ([]byte, error) {
// 	return stxscript.NewScriptBuilder().
// 		AddData([]byte(contractPath)).
// 		AddData([]byte(result)).
// 		AddData([]byte(more)).Script()
// }

// func ParseContractResultInvoice(script []byte) (string, string, string, error) {

// 	tokenizer := stxscript.MakeScriptTokenizer(0, script)

// 	if !tokenizer.Next() || tokenizer.Err() != nil {
// 		return "", "", "", fmt.Errorf("missing contract path")
// 	}
// 	contractURL := string(tokenizer.Data())

// 	if !tokenizer.Next() || tokenizer.Err() != nil {
// 		return "", "", "", fmt.Errorf("missing invoke result")
// 	}
// 	result := string(tokenizer.Data())

// 	if !tokenizer.Next() || tokenizer.Err() != nil {
// 		return contractURL, result, "", nil
// 	}
// 	more := string(tokenizer.Data())

// 	return contractURL, result, more, nil
// }
