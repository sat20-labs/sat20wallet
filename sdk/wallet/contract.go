package wallet

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/runes/runestone"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const (
	URL_SEPARATOR = "_"

	// 已经完成的
	TEMPLATE_CONTRACT_LAUNCHPOOL string = "launchpool.tc"
	TEMPLATE_CONTRACT_SWAP       string = "swap.tc"
	TEMPLATE_CONTRACT_AMM        string = "amm.tc"
	TEMPLATE_CONTRACT_TRANSCEND  string = "transcend.tc" // 支持任意资产进出通道，优先级比 TEMPLATE_CONTRACT_AMM 低
	// 开发中的
	TEMPLATE_CONTRACT_RECYCLE     string = "recycle.tc"
	TEMPLATE_CONTRACT_VAULT       string = "vault.tc"
	TEMPLATE_CONTRACT_STAKE       string = "stake.tc"
	TEMPLATE_CONTRACT_MINER_STAKE string = "minerstake.tc"

	CONTRACT_STATUS_EXPIRED int = -2
	CONTRACT_STATUS_CLOSED  int = -1
	CONTRACT_STATUS_INIT    int = 0
	// ...   deploying status
	CONTRACT_STATUS_READY int = 100 // 正常工作阶段
	// ...   running status，101-199，合约正常运行时的自定义状态
	CONTRACT_STATUS_ADJUSTING int = 101 // 调整完成后，重新进入ready

	CONTRACT_STATUS_CLOSING int = 200 // 进入最后的关闭阶段
	// ...   closing status, at last change to CONTRACT_STATUS_CLOSED or CONTRACT_STATUS_EXPIRED

	INVOKE_API_ENABLE string = "enable" // 每个合约的第一个调用，用来激活合约

	INVOKE_RESULT_OK              string = "ok"
	INVOKE_RESULT_REFUND          string = INVOKE_API_REFUND
	INVOKE_RESULT_DEAL            string = "deal"
	INVOKE_RESULT_DEPOSIT         string = INVOKE_API_DEPOSIT
	INVOKE_RESULT_WITHDRAW        string = INVOKE_API_WITHDRAW
	INVOKE_RESULT_DEANCHOR        string = "deanchor"
	INVOKE_RESULT_ANCHOR          string = "anchor"
	INVOKE_RESULT_STAKE           string = INVOKE_API_STAKE
	INVOKE_RESULT_UNSTAKE         string = INVOKE_API_UNSTAKE
	INVOKE_RESULT_ADDLIQUIDITY    string = INVOKE_API_ADDLIQUIDITY
	INVOKE_RESULT_REMOVELIQUIDITY string = INVOKE_API_REMOVELIQUIDITY
	INVOKE_RESULT_PROFIT          string = INVOKE_API_PROFIT
	INVOKE_RESULT_REWARD          string = "reward"
)

const (
	INVOKE_API_SWAP            string = "swap"
	INVOKE_API_REFUND          string = "refund"
	INVOKE_API_FUND            string = "fund"     //
	INVOKE_API_DEPOSIT         string = "deposit"  // L1->L2  免费
	INVOKE_API_WITHDRAW        string = "withdraw" // L2->L1  收
	INVOKE_API_STAKE           string = "stake"    // 如果是L1的stake，必须要有op_return携带invokeParam，否则会被认为是deposit
	INVOKE_API_UNSTAKE         string = "unstake"  // 可以unstake到一层
	INVOKE_API_ADDLIQUIDITY    string = "addliq"   // 如果是L1，必须要有op_return携带invokeParam，否则会被认为是deposit
	INVOKE_API_REMOVELIQUIDITY string = "removeliq"
	INVOKE_API_PROFIT          string = "profit"
	INVOKE_API_RECYCLE         string = "recycle"
	INVOKE_API_REWARD          string = "reward"

	ORDERTYPE_NOSPEC          = 0
	ORDERTYPE_SELL            = 1
	ORDERTYPE_BUY             = 2
	ORDERTYPE_REFUND          = 3
	ORDERTYPE_FUND            = 4
	ORDERTYPE_PROFIT          = 5
	ORDERTYPE_DEPOSIT         = 6
	ORDERTYPE_WITHDRAW        = 7
	ORDERTYPE_MINT            = 8
	ORDERTYPE_ADDLIQUIDITY    = 9
	ORDERTYPE_REMOVELIQUIDITY = 10
	ORDERTYPE_STAKE           = 11
	ORDERTYPE_UNSTAKE         = 12
	ORDERTYPE_RECYCLE         = 13
	ORDERTYPE_REWARD          = 14
	ORDERTYPE_UNUSED          = 15

	INVOKE_FEE          int64 = 10
	SWAP_INVOKE_FEE     int64 = 10
	DEPOSIT_INVOKE_FEE  int64 = 0
	WITHDRAW_INVOKE_FEE int64 = 2000

	MAX_PRICE_DIVISIBILITY = 10
	MAX_ASSET_DIVISIBILITY = 10

	BUCK_SIZE = 100

	SWAP_SERVICE_FEE_RATIO = 8 // 千分之
	DEPTH_SLOT             = 10
)

const (
	DONE_NOTYET          = 0
	DONE_DEALT           = 1
	DONE_REFUNDED        = 2
	DONE_CLOSED_DIRECTLY = 3
	DONE_CANCELLED       = 4
)

type ResvStatus int

const (
	RS_DEPLOY_CONTRACT_STARTED         ResvStatus = 0x2000
	RS_DEPLOY_CONTRACT_TX_BROADCASTED  ResvStatus = 0x2001
	RS_DEPLOY_CONTRACT_TX_CONFIRMED    ResvStatus = 0x2002
	RS_DEPLOY_CONTRACT_INSTALL_STARTED ResvStatus = 0x2003
	//   合约内置的初始化状态机，最终转到 RS_DEPLOY_CONTRACT_RUNNING 退出
	RS_DEPLOY_CONTRACT_RUNNING   ResvStatus = RS_DEPLOY_CONTRACT_INSTALL_STARTED + ResvStatus(CONTRACT_STATUS_READY) // 0x2067
	RS_DEPLOY_CONTRACT_SUPPENDED ResvStatus = RS_DEPLOY_CONTRACT_RUNNING + ResvStatus(CONTRACT_STATUS_CLOSING-CONTRACT_STATUS_READY)
	RS_DEPLOY_CONTRACT_COMPLETED ResvStatus = RS_CONFIRMED
)

const (
	INVOKE_REASON_NORMAL               string = ""
	INVOKE_REASON_REFUND               string = "refund"      // 退款
	INVOKE_REASON_CANCEL               string = "cancel"      // 不需要退款，指令取消
	INVOKE_REASON_INVALID              string = "invalid"     // 参数错误
	INVOKE_REASON_INNER_ERROR          string = "inner error" // 内部错误
	INVOKE_REASON_NO_ENOUGH_ASSET      string = "no enough asset"
	INVOKE_REASON_SLIPPAGE_PROTECT     string = "slippage protection"
	INVOKE_REASON_UTXO_NOT_FOUND       string = "input utxo not found"
	INVOKE_REASON_UTXO_NOT_FOUND_REORG string = "input utxo not found after reorg"
	INVOKE_REASON_NO_PROFIT            string = "no profit"
	INVOKE_REASON_UTXO_FORMAT          string = "input utxo incorrect format"
)

const (
	ERR_MERKLE_ROOT_INCONSISTENT = "contract runtime merkle root is inconsistent"
)

// 用在开发过程修改数据库，设置为true，然后数据库自动升级，然后马上要设置为false，并且将所有oldversion的数据结果，等同于最新结构
const ContractRuntimeBaseUpgrade = false

type ContractDeployResvIF interface {
	GetId() int64
	GetType() string
	GetStatus() ResvStatus
	SetStatus(ResvStatus)
	GetResult() []byte

	GetContract() ContractRuntime
	GetMutex() *sync.RWMutex
	LocalIsInitiator() bool

	GetChannelAddr() string
	GetDeployer() string
	GetRemotePubKey() []byte

	GetFeeRate() int64
	GetFeeUtxos() []string
	SetFeeUtxos([]string)
	SetFeeInputs([]*TxOutput_SatsNet)
	SetRequiredFee(int64)
	SetServiceFee(int64)

	GetDeployContractTx() *swire.MsgTx
	SetDeployContractTx(*swire.MsgTx)
	GetDeployContractTxId() string
	SetDeployContractTxId(string)
	SetHasSentDeployTx(int)

	ResyncBlock(start, end int)
	ResyncBlock_SatsNet(start, end int)

	SignedDeployContractInvoice() ([]byte, error)
}

type ContractManager interface {
	/////////////////////////////
	// 只观察的合约管理器需要实现这些接口
	GetTickerInfo(name *swire.AssetName) *indexer.TickerInfo
	GetWallet() common.Wallet
	GetWalletMgr() *Manager
	GetIndexerClient() IndexerRPCClient
	GetIndexerClient_SatsNet() IndexerRPCClient
	GetFeeRate() int64
	GetMode() string
	/////////////////////////////

	GetContract(url string) ContractRuntime
	GetServerNodePubKey() *secp256k1.PublicKey
	GetSpecialContractResv(assetName, templateName string) ContractDeployResvIF
	GetDeployReservation(id int64) ContractDeployResvIF
	SaveReservation(ContractDeployResvIF) error
	SaveReservationWithLock(ContractDeployResvIF) error
	GetDB() indexer.KVDB
	NeedRebuildTraderHistory() bool

	CoGenerateStubUtxos(n int, feeRate int64, contractURL string, invokeCount int64,
		excludeRecentBlock bool) (string, int64, error)
	CoBatchSendV3(dest []*SendAssetInfo, assetNameStr string, feeRate int64,
		reason, contractURL string, invokeCount int64, memo, static, runtime []byte,
		sendDeAnchorTx, excludeRecentBlock bool) (string, int64, error)
	CoSendOrdxWithStub(dest string, assetNameStr string, amt int64, feeRate int64, stub string,
		reason, contractURL string, invokeCount int64, memo, static, runtime []byte,
		sendDeAnchorTx, excludeRecentBlock bool) (string, int64, error)
	CoBatchSendV2_SatsNet(dest []*SendAssetInfo, assetName string,
		reason, contractURL string, invokeCount int64, memo, static, runtime []byte) (string, error)
	CoBatchSend_SatsNet(destAddr []string, assetName string, amtVect []string,
		reason, contractURL string, invokeCount int64, memo, static, runtime []byte) (string, error)
	SendSigReq(req *wwire.SignRequest, sig []byte) ([][][]byte, error)

	SendContractEnabledTx(url string, h1, h2 int) (string, error)
	CreateContractDepositAnchorTx(contract ContractRuntime, destAddr string,
		splicingOutput *indexer.TxOutput, assetName *AssetName, memo []byte) (*swire.MsgTx, error)

	SendMessageToUpper(eventName string, data interface{})
	BroadcastTx(tx *wire.MsgTx) (string, error)
	BroadcastTx_SatsNet(tx *swire.MsgTx) (string, error)

	AscendAssetInCoreChannel(assetNameStr string, utxo string, memo []byte) (string, error)
	DeployContract(templateName, contractContent string,
		fees []string, feeRate int64, deployer string) (string, int64, error)
}

type ActionFunc func(ContractManager, ContractDeployResvIF, any) (any, error)

type ContractDeployAction struct {
	Action ActionFunc
	Name   string
}

type Contract interface {
	GetTemplateName() string          // 合约模版名称
	GetAssetName() *indexer.AssetName // 资产名称
	GetContractName() string          // 资产名称_模版名称
	CheckContent() error              // 合约内容检查，部署前调用
	Content() string                  // 合约内容， json格式
	InvokeParam(string) string        // 调用合约的参数， json格式
	GetContractBase() *ContractBase

	Encode() ([]byte, error) // 合约内容， script格式
	Decode([]byte) error     // 合约内容， script格式

	GetStartBlock() int
	GetEndBlock() int

	DeployFee(feeRate int64) int64 // in satsnet，部署这个合约的人需要支付的费用, 最少 DEFAULT_SERVICE_FEE_DEPLOY_CONTRACT

	CalcStaticMerkleRoot() []byte
}

type ContractRuntime interface {
	Contract

	InitFromDB(ContractManager, ContractDeployResvIF) error              // 初始化哪些不以大写开头的内部变量
	InitFromContent([]byte, ContractManager, ContractDeployResvIF) error // 根据合约模版参数初始化合约，非json
	InitFromJson([]byte, ContractManager) error                          // 一个从json数据构建的非运行对象
	GetRuntimeBase() *ContractRuntimeBase

	GetStatus() int
	GetInvokerStatus(string) InvokerStatus
	GetRedeemScript() []byte
	GetPkScript() []byte
	GetLocalPkScript() []byte
	GetRemotePkScript() []byte
	GetLocalAddress() string
	GetRemoteAddress() string
	GetSvrAddress() string // 主导合约的节点地址
	Address() string       // 合约钱包地址
	GetLocalPubKey() *secp256k1.PublicKey
	GetRemotePubKey() *secp256k1.PublicKey
	IsInitiator() bool
	URL() string          // 绝对路径
	RelativePath() string // 相对路径，不包括channelId或者其他
	GetAssetName() *indexer.AssetName
	GetAssetNameV2() *AssetName
	RuntimeContent() []byte                 // 运行时数据，用于备份数据
	InstallStatus() string                  // 运行时状态，json格式
	RuntimeStatus() string                  // 运行时状态，json格式
	RuntimeAnalytics() string               // 运行时状态，json格式
	InvokeHistory(any, int, int) string     // 调用历史记录，可以增加过滤条件
	AllAddressInfo(int, int) string         // 所有地址信息，json格式
	StatusByAddress(string) (string, error) // 运行时状态，json格式
	GetDeployTime() int64
	SetDeployTime(int64)
	SetResvId(int64)
	IsExpired() bool
	GetEnableBlock() int
	GetEnableBlockL1() int
	SetEnableBlock(int, int)
	GetAssetAmount() (*Decimal, int64) // 该合约目前管理的资产（和白聪）的数量，还在池子中的资产

	// 安装过程的支持接口
	DeploySelf() bool                // 在 GetAction 中自动部署合约
	UnconfirmedTxId() string         // 当前等待确认的Tx
	UnconfirmedTxId_SatsNet() string // 当前等待确认的Tx
	AllowDeploy() error
	GetDeployAction() *ContractDeployAction // 部署合约，直到合约进入ready状态，需要经过的各种动作，安装过程需要等待 UnconfirmedTxId 被确认，然后进入下一个状态。
	SetDeployActionResult([]byte)           // 一个动作如何只能由一端发起，那执行完的结果要通知对方
	IsReadyToRun(*swire.MsgTx) error
	SetReady()      // 仅调用一次
	IsActive() bool // >=ready && enabled
	IsReady() bool  // ready && enabled

	// 合约调用的支持接口
	CheckInvokeParam(string) (int64, error) // 调用合约的参数检查(json)，调用合约前调用
	AllowInvoke() error
	VerifyAndAcceptInvokeItem_SatsNet(*InvokeTx_SatsNet, int) (InvokeHistoryItem, error) // return：被接受，处理结果
	VerifyAndAcceptInvokeItem(*InvokeTx, int) (InvokeHistoryItem, error)                 // return：被接受，处理结果
	PreprocessInvokeData(data *InvokeDataInBlock) error
	PreprocessInvokeData_SatsNet(data *InvokeDataInBlock_SatsNet) error
	InvokeWithBlock_SatsNet(*InvokeDataInBlock_SatsNet) error
	InvokeCompleted_SatsNet(*InvokeDataInBlock_SatsNet)
	InvokeWithBlock(*InvokeDataInBlock) error
	InvokeCompleted(*InvokeDataInBlock)
	HandleReorg_SatsNet(int, int) error
	HandleReorg(int, int) error
	DisableItem(InvokeHistoryItem) // 因为reorg导致某个item无效

	// 作为通道合约，本地节点不能发起的动作，需要由peer发起，在这里检查和设置结果，并推动合约内部状态变迁
	AllowPeerAction(string, any) (any, error)
	SetPeerActionResult(string, any)
	HandleInvokeResult(*swire.MsgTx, int, string, string) // 处理感兴趣的调用结果，只处理聪网的Tx

	checkSelf() error
	CalcRuntimeMerkleRoot() []byte

	// 维护接口
	AddLostInvokeItem(string, bool) (string, error)
}

// 合约调用历史记录
type InvokeHistoryItem interface {
	GetVersion() int
	GetId() int64
	GetKey() string
	HasDone() bool
	GetHeight() int
	FromSatsNet() bool
	ToSatsNet() bool
	ToNewVersion() InvokeHistoryItem
}

type InvokeHistoryItemBase struct {
	Version int
	Id      int64
	Reason  string // "" 表示一切正常，按照期望完成
	Done    int    // 0 进行中；1，交易完成; 2，退款； 3，直接关闭
}

func (p *InvokeHistoryItemBase) GetVersion() int {
	return p.Version
}

func (p *InvokeHistoryItemBase) GetId() int64 {
	return p.Id
}

func (p *InvokeHistoryItemBase) GetKey() string {
	return GetKeyFromId(p.Id)
}

func (p *InvokeHistoryItemBase) HasDone() bool {
	return p.Done != DONE_NOTYET
}

func (p *InvokeHistoryItemBase) GetHeight() int {
	return -1
}

func (p *InvokeHistoryItemBase) FromSatsNet() bool {
	return true
}

func (p *InvokeHistoryItemBase) ToSatsNet() bool {
	return true
}

func (p *InvokeHistoryItemBase) ToNewVersion() InvokeHistoryItem {
	return p
}

func GetKeyFromId(id int64) string {
	return fmt.Sprintf("%012d", id)
}

func NewInvokeHistoryItem(cn string) InvokeHistoryItem {
	switch cn {
	case TEMPLATE_CONTRACT_SWAP:
		return &SwapHistoryItem{}
	case TEMPLATE_CONTRACT_LAUNCHPOOL:
		return &MintHistoryItem{}
	case TEMPLATE_CONTRACT_AMM:
		return &SwapHistoryItem{}
	}
	return &InvokeItem{}
}

func NewInvokeHistoryItem_old(cn string) InvokeHistoryItem {
	// switch cn {
	// case TEMPLATE_CONTRACT_SWAP:
	// 	return &SwapHistoryItem_old{}
	// case TEMPLATE_CONTRACT_LAUNCHPOOL:
	// 	return &MintHistoryItem_old{}
	// }
	return &InvokeItem_old{}
}

type InvokeItem_old = InvokeItem

// type InvokeItem_old struct {
// 	Version        int
// 	Id             int64
// 	OrderType      int    //
// 	UtxoId         uint64 // 其实是utxoId
// 	OrderTime      int64
// 	AssetName      string
// 	UnitPrice      *Decimal // X per Y
// 	ExpectedAmt    *Decimal // 期望的数量
// 	Address        string   // 所有人
// 	FromL1         bool     // 是否主网的调用，默认是false
// 	InUtxo         string   // sell or buy 的utxo
// 	InValue        int64    // 白聪，不包括资产聪, 去掉手续费
// 	InAmt          *Decimal
// 	RemainingAmt   *Decimal // 要买或者卖的资产的剩余数量
// 	RemainingValue int64    // 用来买资产的聪的剩余数量
// 	OutTxId        string   // 回款的TxId，可能是成交后汇款，也可能是撤销后的回款
// 	OutAmt         *Decimal // 买到的资产，或者
// 	OutValue       int64    // 卖出得到的聪
// 	Valid          bool
// 	Done           int // 0 交易中；1，交易完成，2，退款
// }

// func (p *InvokeItem_old) ToNewVersion() InvokeHistoryItem {
// 	return &InvokeItem{}
// }

type InvokeItem struct {
	InvokeHistoryItemBase

	OrderType      int //
	UtxoId         uint64
	OrderTime      int64
	AssetName      string
	ServiceFee     int64
	UnitPrice      *Decimal // X per Y
	ExpectedAmt    *Decimal // 期望的数量
	Address        string   // 所有人
	FromL1         bool     // InUtxo是否主网的调用，默认是false
	InUtxo         string   // 调用合约的Utxo
	InValue        int64    // InUtxo的白聪，不包括资产聪，包括手续费
	InAmt          *Decimal // InUtxo的资产，每个utxo只能使用一种资产来和合约交互
	RemainingAmt   *Decimal // 输入资产扣除费用后，能参与合约的资产，动态数据，比如要买或者卖的资产的剩余数量
	RemainingValue int64    // 输入资产扣除费用后，能参与合约的聪，动态数据，比如用来买资产的聪的剩余数量
	ToL1           bool     // OutTxId是否主网的调用，默认是false
	OutTxId        string   // 输出的TxId，可能是成交后汇款，也可能是撤销后的回款
	OutAmt         *Decimal // 合约交互结果，比如买到的资产
	OutValue       int64    // 合约交互结果，卖出得到的聪，扣除服务费

	// 增加
	Padded []byte // 扩展使用
}

func (p *InvokeItem) ToNewVersion() InvokeHistoryItem {
	return p
}

func (p *InvokeItem) GetHeight() int {
	h, _, _ := indexer.FromUtxoId(p.UtxoId)
	return h
}

func (p *InvokeItem) FromSatsNet() bool {
	return !p.FromL1
}

func (p *InvokeItem) ToSatsNet() bool {
	return !p.ToL1
}

func (p *InvokeItem) Clone() *InvokeItem {
	n := *p
	n.UnitPrice = p.UnitPrice.Clone()
	n.ExpectedAmt = p.ExpectedAmt.Clone()
	n.InAmt = p.InAmt.Clone()
	n.RemainingAmt = p.RemainingAmt.Clone()
	n.OutAmt = p.OutAmt.Clone()
	return &n
}

// InvokeParam.Param
type InvokeInnerParamIF interface {
	Encode() ([]byte, error)
	Decode(data []byte) error

	EncodeV2() ([]byte, error) // 尽可能节省字节数的编码方式，比如不需要资产名称
}

type InvokeParam struct {
	Action string `json:"action"`
	Param  string `json:"param,omitempty"` // 外部使用时是json，内部使用时是编码过的string
}

func (p *InvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(p.Action)).
		AddData([]byte(p.Param)).Script()
}

func (p *InvokeParam) EncodeV2() ([]byte, error) {
	return p.Encode()
}

func (p *InvokeParam) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing action")
	}
	p.Action = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing parameter")
	}
	p.Param = string(tokenizer.Data())
	return nil
}

type EnableInvokeParam struct {
	HeightL1 int `json:"heightL1"`
	HeightL2 int `json:"heightL2"`
}

func (p *EnableInvokeParam) Encode() ([]byte, error) {
	return stxscript.NewScriptBuilder().
		AddInt64(int64(p.HeightL1)).
		AddInt64(int64(p.HeightL2)).Script()
}

func (p *EnableInvokeParam) Decode(data []byte) error {
	tokenizer := stxscript.MakeScriptTokenizer(0, data)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing heightL1")
	}
	p.HeightL1 = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing heightL2")
	}
	p.HeightL2 = int(tokenizer.ExtractInt64())

	return nil
}

type InvokerStatus interface {
	GetVersion() int
	GetKey() string

	GetInvokeCount() int
	GetInvokeAmt() *Decimal
	GetInvokeValue() int64
	GetHistory() map[int][]int64
}

type InvokerStatusBaseV2 struct {
	Version     int
	Address     string
	InvokeCount int
	InvokeAmt   *Decimal // 交互资产总额
	InvokeValue int64
	History     map[int][]int64 // 用户的invoke历史记录，每100个为一桶，用InvokeCount计算 TODO 目前统一一块存储，数据量大了后要分桶保存，用到才加载
	UpdateTime  int64
}

func NewInvokerStatusBaseV2(address string, divisibility int) *InvokerStatusBaseV2 {
	return &InvokerStatusBaseV2{
		Address:    address,
		History:    make(map[int][]int64),
		UpdateTime: time.Now().Unix(),
	}
}

func (p *InvokerStatusBaseV2) GetVersion() int {
	return p.Version
}

func (p *InvokerStatusBaseV2) GetKey() string {
	return p.Address
}

func (p *InvokerStatusBaseV2) GetInvokeCount() int {
	return p.InvokeCount
}

func (p *InvokerStatusBaseV2) GetInvokeAmt() *Decimal {
	return p.InvokeAmt
}

func (p *InvokerStatusBaseV2) GetInvokeValue() int64 {
	return p.InvokeValue
}

func (p *InvokerStatusBaseV2) GetHistory() map[int][]int64 {
	return p.History
}

// 老的调用者数据结构，新合约不要用，用 InvokerStatusBaseV2
type InvokerStatusBase struct {
	Version       int
	Address       string
	InvokeCount   int
	DepositAmt    *Decimal // 存款总额
	DepositValue  int64
	WithdrawAmt   *Decimal // 取款总额
	WithdrawValue int64
	RefundAmt     *Decimal // 无效退回，不计入存款总额
	RefundValue   int64

	DepositUtxoMap  map[string]bool // 废弃
	WithdrawUtxoMap map[string]bool // 废弃
	RefundUtxoMap   map[string]bool // 废弃
	History         map[int][]int64 // 用户的invoke历史记录，每100个为一桶，用InvokeCount计算 TODO 目前统一一块存储，数据量大了后要分桶保存，用到才加载
	UpdateTime      int64
}

func NewInvokerStatusBase(address string, divisibility int) *InvokerStatusBase {
	return &InvokerStatusBase{
		Address:    address,
		History:    make(map[int][]int64),
		UpdateTime: time.Now().Unix(),
	}
}

func (p *InvokerStatusBase) GetVersion() int {
	return p.Version
}

func (p *InvokerStatusBase) GetKey() string {
	return p.Address
}

func (p *InvokerStatusBase) GetInvokeCount() int {
	return p.InvokeCount
}

func (p *InvokerStatusBase) GetInvokeAmt() *Decimal {
	return p.DepositAmt.Add(p.WithdrawAmt)
}

func (p *InvokerStatusBase) GetInvokeValue() int64 {
	return p.DepositValue + p.WithdrawValue
}

func (p *InvokerStatusBase) GetHistory() map[int][]int64 {
	return p.History
}

func NewInvokerStatus(cn string) InvokerStatus {
	switch cn {
	case TEMPLATE_CONTRACT_SWAP, TEMPLATE_CONTRACT_AMM:
		return &TraderStatus{}
	case TEMPLATE_CONTRACT_LAUNCHPOOL:
		return nil
		//case TEMPLATE_CONTRACT_VAULT:
		//	return &VaultInvokerStatus{}
	}
	return &TraderStatus{}
}

// 合约内容基础结构
type ContractBase struct {
	TemplateName string            `json:"contractType"`
	AssetName    indexer.AssetName `json:"assetName"`
	StartBlock   int               `json:"startBlock"` // 0，部署即可以使用
	EndBlock     int               `json:"endBlock"`   // 0，部署后永久可用，除非合约其他规则
	//Sendback     bool   `json:"sendBack"` // send back when invoke fail(only send in satsnet)
}

func (p *ContractBase) CheckContent() error {
	if indexer.IsPlainAsset(&p.AssetName) {
		return nil
	}
	if p.AssetName.Protocol != indexer.PROTOCOL_NAME_ORDX &&
		p.AssetName.Protocol != indexer.PROTOCOL_NAME_RUNES &&
		p.AssetName.Protocol != indexer.PROTOCOL_NAME_BRC20 {
		return fmt.Errorf("invalid protocol %s", p.AssetName.Protocol)
	}
	if p.AssetName.Ticker == "" {
		return fmt.Errorf("invalid asset name %s", p.AssetName.Ticker)
	}
	if p.AssetName.Type != indexer.ASSET_TYPE_FT {
		return fmt.Errorf("invalid asset type %s", p.AssetName.Type)
	}

	if p.AssetName.Protocol == indexer.PROTOCOL_NAME_ORDX {
		if !indexer.IsValidSat20Name(p.AssetName.Ticker) {
			return fmt.Errorf("invalid asset name %s", p.AssetName.Ticker)
		}
		p.AssetName.Ticker = strings.ToLower(p.AssetName.Ticker)
	} else if p.AssetName.Protocol == indexer.PROTOCOL_NAME_RUNES {
		// 为了简化处理，我们简单拒绝“.”
		if strings.Contains(p.AssetName.Ticker, ".") {
			return fmt.Errorf("\".\" not allowed, use • instead")
		}
		_, err := runestone.SpacedRuneFromString(p.AssetName.Ticker)
		if err != nil {
			return fmt.Errorf("invalid asset name %s", p.AssetName.Ticker)
		}
	} else if p.AssetName.Protocol == indexer.PROTOCOL_NAME_BRC20 {
		if len(p.AssetName.Ticker) != 4 && len(p.AssetName.Ticker) != 5 {
			return fmt.Errorf("invalid asset name %s", p.AssetName.Ticker)
		}
	}

	return nil
}

func (p *ContractBase) GetContractBase() *ContractBase {
	return p
}

func (p *ContractBase) Content() string {
	buf, err := json.Marshal(p)
	if err != nil {
		Log.Panicf("Marshal ContractBase failed, %v", err)
	}
	return string(buf)
}

func (p *ContractBase) Encode() ([]byte, error) {
	return stxscript.NewScriptBuilder().
		AddData([]byte(p.TemplateName)).
		AddData([]byte(p.AssetName.String())).
		AddInt64(int64(p.StartBlock)).
		AddInt64(int64(p.EndBlock)).Script()
}

func (p *ContractBase) Decode(data []byte) error {
	tokenizer := stxscript.MakeScriptTokenizer(0, data)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing template name")
	}
	p.TemplateName = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing asset name")
	}
	assetName := string(tokenizer.Data())
	p.AssetName = *indexer.NewAssetNameFromString(assetName)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing contract start block")
	}
	p.StartBlock = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing contract end block")
	}
	p.EndBlock = int(tokenizer.ExtractInt64())
	return nil
}

func (p *ContractBase) GetTemplateName() string {
	return p.TemplateName
}

func (p *ContractBase) GetAssetName() *indexer.AssetName {
	return &p.AssetName
}

func (p *ContractBase) GetStartBlock() int {
	return p.StartBlock
}

func (p *ContractBase) GetEndBlock() int {
	return p.EndBlock
}

func (p *ContractBase) DeployFee(feeRate int64) int64 {
	return 0
}

func (p *ContractBase) InvokeParam(string) string {
	return ""
}

type ContractRuntimeBase_old = ContractRuntimeBase

// type ContractRuntimeBase_old struct {
// 	DeployTime    int64  `json:"deployTime"` // s
// 	Status        int    `json:"status"`
// 	EnableBlock   int    `json:"enableBlock"`    // 合约在哪个区块进入ready状态
// 	CurrBlock     int    `json:"currentBlock"`   // 合约区块不能跳，必须从EnableBlock开始，一块一块执行
// 	EnableBlockL1 int    `json:"enableBlockL1"`  // 合约在哪个区块进入ready状态
// 	CurrBlockL1   int    `json:"currentBlockL1"` // 合约区块不能跳，必须从EnableBlock开始，一块一块执行
// 	EnableTxId    string `json:"enableTxId"`     // 只设置，暂时没有用起来
// 	Deployer      string `json:"deployer"`
// 	ResvId        int64  `json:"resvId"`
// 	ChannelId     string `json:"channelId"`
// 	InvokeCount   int64  `json:"invokeCount"`
// 	Divisibility  int    `json:"divisibility"`
// 	N             int    `json:"n"`

// 	CheckPoint          int64  // 上个与peer端校验过merkleRoot的invokeCount
// 	StaticMerkleRoot    []byte // 合约静态数据
// 	AssetMerkleRoot     []byte //
// 	CurrAssetMerkleRoot []byte // 上个检查的资产状态数据。每个InvokeCount会有两次计算的机会 invokeCompleted 计算时，合约发起的交易还没被确认，在下一个invokeCompleted时才真正是当前调用次数的结果

// }

// func (p *ContractRuntimeBase_old) ToNewVersion() *ContractRuntimeBase {
// 	if p == nil {
// 		return nil
// 	}
// 	return &ContractRuntimeBase{
// 		DeployTime:    p.DeployTime,
// 		Status:        p.Status,
// 		EnableBlock:   p.EnableBlock,
// 		CurrBlock:     p.CurrBlock,
// 		EnableBlockL1: p.EnableBlockL1,
// 		CurrBlockL1:   p.CurrBlockL1,
// 		EnableTxId:    p.EnableTxId,
// 		Deployer:      p.Deployer,
// 		ResvId:        p.ResvId,
// 		ChannelAddr:   p.ChannelId,
// 		InvokeCount:   p.InvokeCount,
// 		Divisibility:  p.Divisibility,
// 		N:             p.N,

// 		CheckPoint:          p.CheckPoint,
// 		StaticMerkleRoot:    p.StaticMerkleRoot,
// 		AssetMerkleRoot:     p.AssetMerkleRoot,
// 		CurrAssetMerkleRoot: p.CurrAssetMerkleRoot,

// 		CheckPointBlock:   0,
// 		CheckPointBlockL1: 0,
// 	}
// }

const INIT_ENABLE_BLOCK int = math.MaxInt

// 合约运行时基础结构，合约区块以聪网为主，主网区块辅助使用
type ContractRuntimeBase struct {
	DeployTime    int64  `json:"deployTime"` // s
	Status        int    `json:"status"`
	EnableBlock   int    `json:"enableBlock"`    // 合约在哪个区块进入ready状态，初始值 INIT_ENABLE_BLOCK
	CurrBlock     int    `json:"currentBlock"`   // 合约区块不能跳，必须一块一块执行，即使EnableBlock还没到，也要同步
	EnableBlockL1 int    `json:"enableBlockL1"`  // 合约在哪个区块进入ready状态，初始值 INIT_ENABLE_BLOCK
	CurrBlockL1   int    `json:"currentBlockL1"` // 合约区块不能跳，必须从EnableBlock开始，一块一块执行
	EnableTxId    string `json:"enableTxId"`     // 只设置，暂时没有用起来
	Deployer      string `json:"deployer"`
	ResvId        int64  `json:"resvId"`
	ChannelAddr   string `json:"channelAddr"`
	InvokeCount   int64  `json:"invokeCount"`
	Divisibility  int    `json:"divisibility"`
	N             int    `json:"n"`

	CheckPoint          int64  // 上个与peer端校验过merkleRoot的invokeCount
	StaticMerkleRoot    []byte // 合约静态数据
	AssetMerkleRoot     []byte //
	CurrAssetMerkleRoot []byte // 上个检查的资产状态数据。每个InvokeCount会有两次计算的机会 invokeCompleted 计算时，合约发起的交易还没被确认，在下一个invokeCompleted时才真正是当前调用次数的结果

	CheckPointBlock   int
	CheckPointBlockL1 int
	LocalPubKey       []byte
	RemotePubKey      []byte

	history            map[string]*InvokeItem // key:utxo 单独记录数据库，区块缓存, 6个区块以后，并且已经成交的可以删除
	resv               ContractDeployResvIF
	stp                ContractManager
	contract           Contract
	runtime            ContractRuntime
	assetMerkleRootMap map[int64][]byte // invokeCount -> AssetMerkleRoot 临时缓存
	lastInvokeCount    int64            // 上个区块的InvokeCount

	isInitiator  bool
	localPubKey  *secp256k1.PublicKey
	remotePubKey *secp256k1.PublicKey
	redeemScript []byte
	pkScript     []byte

	// rpc 缓存
	refreshTime     int64
	responseHistory map[int][]*InvokeItem // 按照100个为一桶，根据区块顺序记录，跟swapHistory保持一致

	mutex sync.RWMutex
}

func NewContractRuntimeBase(stp ContractManager) *ContractRuntimeBase {
	return &ContractRuntimeBase{
		EnableBlock:   INIT_ENABLE_BLOCK,
		EnableBlockL1: INIT_ENABLE_BLOCK,
		DeployTime:    time.Now().Unix(),
		stp:           stp,
	}
}

func (p *ContractRuntimeBase) ToNewVersion() *ContractRuntimeBase {
	return p
}

func (p *ContractRuntimeBase) GetAssetNameV2() *AssetName {
	return &AssetName{
		AssetName: *p.contract.GetAssetName(),
		N:         p.N,
	}
}

func (p *ContractRuntimeBase) InitFromContent(content []byte, stp ContractManager, resv ContractDeployResvIF) error {
	// 这里resv还没有获得正确的ResvId，需要后面补上
	p.resv = resv
	p.ChannelAddr = resv.GetChannelAddr()
	p.Deployer = resv.GetDeployer()
	p.stp = stp
	p.assetMerkleRootMap = make(map[int64][]byte)
	p.history = make(map[string]*InvokeItem)
	p.isInitiator = resv.LocalIsInitiator()
	p.localPubKey = stp.GetWallet().GetPubKey()
	remotePK, err := utils.BytesToPublicKey(resv.GetRemotePubKey())
	if err != nil {
		return err
	}
	p.remotePubKey = remotePK
	p.LocalPubKey = p.localPubKey.SerializeCompressed()
	p.RemotePubKey = p.remotePubKey.SerializeCompressed()
	p.redeemScript, err = utils.GenMultiSigScript(p.LocalPubKey, p.RemotePubKey)
	if err != nil {
		return err
	}
	p.pkScript, err = utils.WitnessScriptHash(p.redeemScript)
	if err != nil {
		return err
	}

	err = p.contract.Decode(content)
	if err != nil {
		return err
	}
	err = p.contract.CheckContent()
	if err != nil {
		return err
	}

	tickInfo := stp.GetTickerInfo(p.contract.GetAssetName())
	if tickInfo != nil {
		p.Divisibility = tickInfo.Divisibility
		p.N = tickInfo.N
	} else {
		tc := p.contract.GetTemplateName()
		switch tc {
		case TEMPLATE_CONTRACT_TRANSCEND:
			p.Divisibility = MAX_ASSET_DIVISIBILITY
			p.N = 0
		case TEMPLATE_CONTRACT_LAUNCHPOOL:
		default:
			return fmt.Errorf("%s can't find ticker %s", p.URL(), tc)
		}
		// 发射池合约肯定找不到，但本身就有足够的数据
		// 由合约自己设置
	}

	p.StaticMerkleRoot = p.contract.CalcStaticMerkleRoot()

	return nil
}

func (p *ContractRuntimeBase) InitFromDB(stp ContractManager, resv ContractDeployResvIF) error {
	p.resv = resv
	p.stp = stp
	p.assetMerkleRootMap = make(map[int64][]byte)
	p.history = make(map[string]*InvokeItem)

	p.isInitiator = resv.LocalIsInitiator()
	var err error
	p.localPubKey, err = utils.BytesToPublicKey(p.LocalPubKey)
	if err != nil {
		return err
	}
	p.remotePubKey, err = utils.BytesToPublicKey(p.RemotePubKey)
	if err != nil {
		return err
	}
	p.redeemScript, err = utils.GenMultiSigScript(p.LocalPubKey, p.RemotePubKey)
	if err != nil {
		return err
	}
	p.pkScript, err = utils.WitnessScriptHash(p.redeemScript)
	if err != nil {
		return err
	}

	return nil
}

func (p *ContractRuntimeBase) GetRuntimeBase() *ContractRuntimeBase {
	return p
}

func (p *ContractRuntimeBase) GetDeployTime() int64 {
	return p.DeployTime
}

func (p *ContractRuntimeBase) SetDeployTime(t int64) {
	p.DeployTime = t
}

func (p *ContractRuntimeBase) SetResvId(id int64) {
	p.ResvId = id
}

func (p *ContractRuntimeBase) DeploySelf() bool {
	return false
}

func (p *ContractRuntimeBase) Address() string {
	return p.ChannelAddr
}

func (p *ContractRuntimeBase) GetRedeemScript() []byte {
	return p.redeemScript
}

func (p *ContractRuntimeBase) GetPkScript() []byte {
	return p.pkScript
}

func (p *ContractRuntimeBase) GetLocalPkScript() []byte {
	pkScript, _ := GetP2TRpkScript(p.localPubKey)
	return pkScript
}

func (p *ContractRuntimeBase) GetRemotePkScript() []byte {
	pkScript, _ := GetP2TRpkScript(p.remotePubKey)
	return pkScript
}

func (p *ContractRuntimeBase) GetLocalAddress() string {
	return PublicKeyToP2TRAddress(p.localPubKey)
}

func (p *ContractRuntimeBase) GetRemoteAddress() string {
	return PublicKeyToP2TRAddress(p.remotePubKey)
}

func (p *ContractRuntimeBase) GetSvrAddress() string {
	if p.isInitiator {
		return p.GetLocalAddress()
	} else {
		return p.GetRemoteAddress()
	}
}

func (p *ContractRuntimeBase) GetFoundationAddress() string {
	addr, _ := indexer.GetBootstrapAddress(GetChainParam())
	return addr
}

func (p *ContractRuntimeBase) GetLocalPubKey() *secp256k1.PublicKey {
	return p.localPubKey
}

func (p *ContractRuntimeBase) GetRemotePubKey() *secp256k1.PublicKey {
	return p.remotePubKey
}

func (p *ContractRuntimeBase) IsInitiator() bool {
	return p.isInitiator
}

func (p *ContractRuntimeBase) URL() string {
	if p.contract == nil {
		return ""
	}
	return p.ChannelAddr + URL_SEPARATOR + p.contract.GetContractName()
}

func (p *ContractRuntimeBase) RelativePath() string {
	return p.contract.GetContractName()
}

func (p *ContractRuntimeBase) GetStatus() int {
	return p.Status
}

func (p *ContractRuntimeBase) SetStatus(s int) {
	p.Status = s
}

func (p *ContractRuntimeBase) GetEnableBlock() int {
	return p.EnableBlock
}

func (p *ContractRuntimeBase) GetEnableBlockL1() int {
	return p.EnableBlockL1
}

func (p *ContractRuntimeBase) SetEnableBlock(height, heightL1 int) {
	p.EnableBlock = height
	p.EnableBlockL1 = heightL1
	if p.CurrBlock == 0 {
		p.CurrBlock = height - 2
		if p.CurrBlock < 0 {
			p.CurrBlock = 0
		}
	}
	if p.CurrBlockL1 == 0 {
		p.CurrBlockL1 = heightL1
		if p.CurrBlockL1 < 0 {
			p.CurrBlockL1 = 0
		}
	}

	p.CheckPointBlock = p.EnableBlock
	p.CheckPointBlockL1 = p.EnableBlockL1

	if p.resv.LocalIsInitiator() {
		txId, err := p.stp.SendContractEnabledTx(p.URL(), heightL1, height)
		if err != nil {
			// TODO send again later
			Log.Errorf("sendContractEnabledTx %s failed, %v", p.URL(), err)
		} else {
			p.EnableTxId = txId
		}
	}

}

func (p *ContractRuntimeBase) IsExpired() bool {
	if p.contract.GetEndBlock() <= 0 {
		return false
	}
	return p.CurrBlock > p.contract.GetEndBlock()
}

func (p *ContractRuntimeBase) GetDeployer() string {
	return p.Deployer
}

func (p *ContractRuntimeBase) UnconfirmedTxId() string {
	return ""
}

func (p *ContractRuntimeBase) UnconfirmedTxId_SatsNet() string {
	return ""
}

func (p *ContractRuntimeBase) AllowDeploy() error {
	contract := p.runtime
	resv := p.resv

	// 同一个资产只能有一个同名字合约，目前不允许覆盖部署
	runtime := p.stp.GetContract(contract.URL())
	if runtime != nil {
		return fmt.Errorf("the same contract exists")
	}

	// 检查费用是否足够
	estimatedFee := contract.DeployFee(resv.GetFeeRate()) // 发起人需要给服务节点的费用
	if estimatedFee <= DEFAULT_SERVICE_FEE_DEPLOY_CONTRACT {
		return fmt.Errorf("DeployFee failed")
	}
	requiredFee := estimatedFee - DEFAULT_SERVICE_FEE_DEPLOY_CONTRACT // 部署过程需要的费用
	feeUtxos := resv.GetFeeUtxos()
	if len(feeUtxos) == 0 {
		address := p.GetSvrAddress()
		var err error
		feeUtxos, err = p.stp.GetWalletMgr().GetUtxosWithAsset_SatsNet(address,
			indexer.NewDefaultDecimal(requiredFee), &ASSET_PLAIN_SAT, nil)
		if err != nil {
			return err
		}
		resv.SetFeeUtxos(feeUtxos)
	}

	var feeUtxosInfo []*TxOutput_SatsNet
	plainAmt := int64(0)
	for _, utxo := range feeUtxos {
		txOut, err := p.stp.GetIndexerClient_SatsNet().GetTxOutput(utxo)
		if err != nil {
			return fmt.Errorf("GetTxOutput %s failed, %v", utxo, err)
		}
		txOut_SatsNet := OutputToSatsNet(txOut)

		value := txOut.GetPlainSat()
		if value > 0 {
			feeUtxosInfo = append(feeUtxosInfo, txOut_SatsNet)
			plainAmt += value
			if plainAmt >= requiredFee {
				break
			}
		}
	}
	if plainAmt < requiredFee {
		return fmt.Errorf("no enough fee, required %d but only %d", requiredFee, plainAmt)
	}
	resv.SetFeeInputs(feeUtxosInfo)
	resv.SetRequiredFee(requiredFee)
	resv.SetServiceFee(DEFAULT_SERVICE_FEE_DEPLOY_CONTRACT)

	return nil
}

func (p *ContractRuntimeBase) GetDeployAction() *ContractDeployAction {
	return &ContractDeployAction{
		Action: runContract,
		Name:   "runContract",
	}
}

// 启动合约
func runContract(stp ContractManager, resv ContractDeployResvIF, param any) (any, error) {
	contract := resv.GetContract()

	txId, ok := param.(string)
	if !ok {
		return nil, fmt.Errorf("param not string")
	}

	mutex := resv.GetMutex()
	mutex.Lock()
	defer mutex.Unlock()

	if txId != resv.GetDeployContractTxId() {
		return nil, fmt.Errorf("not deploy txId")
	}

	// 同时检查并且启动contract
	if contract.GetStatus() < CONTRACT_STATUS_READY {
		// 检查合约运行的条件是否完备，如果完备，设置进入运行状态
		if resv.GetDeployContractTx() == nil {
			txHex, err := stp.GetIndexerClient_SatsNet().GetRawTx(resv.GetDeployContractTxId())
			if err != nil {
				Log.Errorf("runContract GetRawTx %s failed, %v", resv.GetDeployContractTxId(), err)
				return nil, err
			}
			tx, err := DecodeMsgTx_SatsNet(txHex)
			if err != nil {
				return nil, err
			}
			resv.SetDeployContractTx(tx)
		}

		err := contract.IsReadyToRun(resv.GetDeployContractTx())
		if err != nil {
			return nil, err
		}

		// 服务端也准备好了，可以启动contract

		Log.Infof("%s contract %s is ready now.", stp.GetMode(), contract.URL())
		contract.SetReady()
	}

	resv.SetHasSentDeployTx(2)
	if contract.GetStatus() >= CONTRACT_STATUS_READY {
		resv.SetStatus(RS_DEPLOY_CONTRACT_RUNNING)
		stp.SaveReservation(resv)
	}

	return "ok", nil
}

func (p *ContractRuntimeBase) SetDeployActionResult([]byte) {
}

func (p *ContractRuntimeBase) IsReadyToRun(deployTx *swire.MsgTx) error {

	if deployTx == nil {
		return fmt.Errorf("no deploy TX")
	}

	_, _, err := p.CheckDeployTx(deployTx)
	if err != nil {
		return err
	}

	return nil
}

// 这个时候很可能 EnableBlock 还没有设置，CurrBlock 也没有设置
func (p *ContractRuntimeBase) SetReady() {
	p.Status = (CONTRACT_STATUS_READY)
}

func (p *ContractRuntimeBase) IsReady() bool {
	return p.Status == CONTRACT_STATUS_READY &&
		p.CurrBlock >= p.EnableBlock
}

func (p *ContractRuntimeBase) IsActive() bool {
	return p.Status >= CONTRACT_STATUS_READY &&
		p.Status < CONTRACT_STATUS_CLOSING &&
		p.CurrBlock >= p.EnableBlock
}

func (p *ContractRuntimeBase) CheckInvokeParam(string) (int64, error) {
	return 0, nil
}

func (p *ContractRuntimeBase) AllowInvoke() error {

	// resv, ok := r.(ContractDeployResvBase)
	// if !ok {
	// 	return fmt.Errorf("not ContractDeployReservation")
	// }
	if p.Status < CONTRACT_STATUS_READY {
		return fmt.Errorf("contract not ready")
	}

	if p.EnableBlock == INIT_ENABLE_BLOCK {
		return fmt.Errorf("contract enable block not set yet")
	}

	if p.CurrBlock < p.EnableBlock {
		return fmt.Errorf("contract not enabled")
	}

	if p.contract.GetStartBlock() != 0 {
		if p.CurrBlock < p.contract.GetStartBlock() {
			return fmt.Errorf("not reach start block")
		}
	}
	if p.contract.GetEndBlock() != 0 {
		if p.CurrBlock > p.contract.GetEndBlock() {
			return fmt.Errorf("exceed the end block")
		}
	}

	return nil
}

func (p *ContractRuntimeBase) RuntimeContent() []byte {
	return nil
}

func (p *ContractRuntimeBase) InstallStatus() string {
	return ""
}

func (p *ContractRuntimeBase) RuntimeStatus() string {
	return ""
}

func (p *ContractRuntimeBase) RuntimeAnalytics() string {
	return ""
}

func getBuckIndex(id int64) int {
	return int(id / BUCK_SIZE)
}

func getBuckSubIndex(id int64) int {
	return int(id % BUCK_SIZE)
}

func InsertItemToTraderHistroy(trader *InvokerStatusBaseV2, item *InvokeItem) {
	index := getBuckIndex(int64(trader.InvokeCount))
	if trader.History == nil {
		trader.History = make(map[int][]int64)
	}
	trader.History[index] = append(trader.History[index], item.Id)
	trader.InvokeCount++
	trader.UpdateTime = time.Now().Unix()
}

func (p *ContractRuntimeBase) getItemFromBuck(id int64) *InvokeItem {
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

func (p *ContractRuntimeBase) insertBuck(item *InvokeItem) {
	index := getBuckIndex(item.Id)
	buck, ok := p.responseHistory[index]
	if !ok {
		buck = p.loadBuckFromDB(index)
	}
	buck[getBuckSubIndex(item.Id)] = item
}

func (p *ContractRuntimeBase) loadBuckFromDB(id int) []*InvokeItem {
	items := loadContractInvokeHistoryWithRange(p.stp.GetDB(), p.URL(), id*BUCK_SIZE, BUCK_SIZE)
	item2 := make([]*InvokeItem, BUCK_SIZE)
	for _, item := range items {
		swapItem, ok := item.(*InvokeItem)
		if ok {
			item2[getBuckSubIndex(swapItem.Id)] = swapItem
		}
	}
	p.responseHistory[id] = item2
	return item2
}

func (p *ContractRuntimeBase) InvokeHistory(f any, start, limit int) string {

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

	trader := p.runtime.GetInvokerStatus(parts[1])
	invokeCount := trader.GetInvokeCount()
	history := trader.GetHistory()
	result := &response{
		Total: int(invokeCount),
		Start: start,
	}
	if invokeCount != 0 && start >= 0 && start < int(invokeCount) {
		if limit <= 0 {
			limit = 100
		}

		// 换算成真实坐标
		start = int(invokeCount) - 1 - start
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
			buck, ok := history[i]
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

func (p *ContractRuntimeBase) AllAddressInfo(int, int) string {
	return ""
}

func (p *ContractRuntimeBase) StatusByAddress(address string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (p *ContractRuntimeBase) AllowPeerAction(action string, param any) (any, error) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	switch action {

	case wwire.STP_ACTION_SIGN: // 通道外资产
		req, ok := param.(*wwire.RemoteSignMoreData_Contract)
		if !ok {
			return nil, fmt.Errorf("not RemoteSignMoreData_Contract")
		}

		Log.Debugf("AllowPeerAction %s", req.ContractURL)
		Log.Debugf("req:   %d %s", req.InvokeCount, hex.EncodeToString(req.RuntimeMerkleRoot))
		Log.Debugf("local: %d %s", p.InvokeCount, hex.EncodeToString(p.CurrAssetMerkleRoot))

		if !bytes.Equal(req.StaticMerkleRoot, p.StaticMerkleRoot) {
			return nil, fmt.Errorf("contract static merkle root is inconsistent")
		}

		// 检查merkleRoot
		if req.InvokeCount > p.InvokeCount {
			return nil, fmt.Errorf("invoke count is inconsistent, %d %d", req.InvokeCount, p.InvokeCount)
		}

		merkleRoot, ok := p.assetMerkleRootMap[req.InvokeCount]
		if !ok {
			if req.InvokeCount == p.InvokeCount {
				merkleRoot = p.CurrAssetMerkleRoot
			} else {
				return nil, fmt.Errorf("can't find %d merkle root", req.InvokeCount)
			}
		}

		if !bytes.Equal(req.RuntimeMerkleRoot, merkleRoot) {
			if req.InvokeCount == p.InvokeCount {
				return nil, fmt.Errorf("%s at %d", ERR_MERKLE_ROOT_INCONSISTENT, req.InvokeCount)
			} else {
				return nil, fmt.Errorf("invoke count is inconsistent, %d %d", req.InvokeCount, p.InvokeCount)
			}
		}

		return nil, nil
	}
	return nil, nil
}

func (p *ContractRuntimeBase) SetPeerActionResult(string, any) {

}

func (p *ContractRuntimeBase) HandleInvokeResult(*swire.MsgTx, int, string, string) {

}

func (p *ContractRuntimeBase) CheckDeployTx(
	deployTx *swire.MsgTx) (*swire.TxOut, *sindexer.ContractDeployData, error) {

	if deployTx == nil {
		return nil, nil, fmt.Errorf("no deploy TX")
	}

	contract := p.runtime
	resv := p.resv

	pkScript, err := AddrToPkScript(resv.GetChannelAddr(), GetChainParam())
	if err != nil {
		return nil, nil, err
	}

	// 检查是否满足条件
	var output *swire.TxOut // 池子初始的白聪
	var deployParam *sindexer.ContractDeployData
	for i, txOut := range deployTx.TxOut {
		if sindexer.IsOpReturn(txOut.PkScript) {
			ctype, data, err := sindexer.ReadDataFromNullDataScript(txOut.PkScript)
			if err == nil {
				switch ctype {
				case sindexer.CONTENT_TYPE_DEPLOYCONTRACT:
					deployParam, err = sindexer.ParseSignedDeployContractInvoice(data)
					if err != nil {
						Log.Errorf("ParseSignedDeployContractInvoice failed, %v", err)
						return nil, nil, err
					}
				}
			} else {
				Log.Errorf("ReadDataFromNullDataScript %s:%d failed, %v", deployTx.TxID(), i, err)
			}
		} else {
			if bytes.Equal(txOut.PkScript, pkScript) {
				output = txOut
			}
		}
	}

	if deployParam == nil {
		return nil, nil, fmt.Errorf("invalid contract deploy TX, parameter is nil")
	}
	Log.Debugf("%s %s", deployParam.ContractPath, hex.EncodeToString(deployParam.ContractContent))
	if output == nil {
		return nil, nil, fmt.Errorf("invalid contract deploy TX, funding Output is nil")
	}
	Log.Debugf("%v", output)

	// 是否有正确的invoice
	if deployParam.ContractPath != contract.RelativePath() {
		if deployParam.ContractPath != contract.URL() {
			return nil, nil, fmt.Errorf("invalid contract path %s", deployParam.ContractPath)
		}
	}
	buf, err := contract.Encode()
	if err != nil {
		return nil, nil, err
	}
	if !bytes.Equal(deployParam.ContractContent, buf) {
		if !bytes.Equal(deployParam.ContractContent, []byte(contract.Content())) {
			// 尝试兼容老版本
			return nil, nil, fmt.Errorf("invalid contract content %s", hex.EncodeToString(deployParam.ContractContent))
		}
	}
	invoice, err := UnsignedDeployContractInvoiceV2(deployParam)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid contract deploy invoice, %v", err)
	}
	if p.resv.LocalIsInitiator() {
		if !VerifyMessage(p.GetLocalPubKey(), invoice, deployParam.LocalSign) {
			return nil, nil, fmt.Errorf("invalid contract local sig")
		}
		if !VerifyMessage(p.GetRemotePubKey(), invoice, deployParam.RemoteSign) {
			return nil, nil, fmt.Errorf("invalid contract remote sig")
		}
	} else {
		if !VerifyMessage(p.GetLocalPubKey(), invoice, deployParam.RemoteSign) {
			return nil, nil, fmt.Errorf("invalid contract remote sig")
		}
		if !VerifyMessage(p.GetRemotePubKey(), invoice, deployParam.LocalSign) {
			return nil, nil, fmt.Errorf("invalid contract local sig")
		}
	}

	// utxo 不能为空，必须转入一些资产，作为通道运行合约的费用
	if output.Value == 0 && len(output.Assets) == 0 {
		return nil, nil, fmt.Errorf("invalid contract output value %d", output.Value)
	}

	// 交易是否已经完成
	// 被接受就很快打包，可以不做这个判断

	return output, deployParam, nil
}

func (p *ContractRuntimeBase) CheckInvokeTx_SatsNet(invokeTx *InvokeTx_SatsNet) error {
	// resv, ok := r.(ContractDeployResvBase)
	// if !ok {
	// 	return fmt.Errorf("not ContractDeployReservation")
	// }

	if invokeTx.InvokeParam == nil {
		return fmt.Errorf("invalid contract invoke TX, parameter is nil")
	}

	invokeParam := invokeTx.InvokeParam

	if invokeTx.TxOutput == nil { // 可能是该合约的enable调用
		return fmt.Errorf("invalid contract invoke TX, fundingOutput is nil, maybe a enable invoke tx: %s", invokeTx.Tx.TxID())
	}

	if len(invokeParam.PubKey) != 0 && len(invokeParam.Sig) != 0 {
		invoice, err := UnsignedInvokeContractInvoice(invokeParam)
		if err != nil {
			return fmt.Errorf("invalid contract invoice, %v", err)
		}
		pubkey, err := utils.BytesToPublicKey(invokeParam.PubKey)
		if err != nil {
			return fmt.Errorf("invalid contract initor key, %v", err)
		}
		if !VerifyMessage(pubkey, invoice, invokeParam.Sig) {
			return fmt.Errorf("invalid contract sig")
		}
	}

	var invokerAddr string
	if len(invokeParam.PubKey) == 0 {
		// 取最后一个输入作为调用者
		txIn := invokeTx.Tx.TxIn[len(invokeTx.Tx.TxIn)-1]
		preTxId := txIn.PreviousOutPoint.Hash.String()
		txHex, err := p.stp.GetIndexerClient_SatsNet().GetRawTx(preTxId)
		if err != nil {
			Log.Errorf("GetRawTx %s failed, %v", preTxId, err)
			return err
		}
		preTx, err := DecodeMsgTx_SatsNet(txHex)
		if err != nil {
			Log.Errorf("DecodeMsgTx_SatsNet from %s failed, %v", preTxId, err)
			return err
		}
		if int(txIn.PreviousOutPoint.Index) >= len(preTx.TxOut) {
			return fmt.Errorf("index out of bound. %d %s", txIn.PreviousOutPoint.Index, preTxId)
		}
		txOut := preTx.TxOut[txIn.PreviousOutPoint.Index]
		invokerAddr, err = AddrFromPkScript(txOut.PkScript)
		if err != nil {
			Log.Errorf("AddrFromPkScript %s failed, %v", hex.EncodeToString(txOut.PkScript), err)
			return err
		}
	} else {
		invokerAddr = HexPubKeyToP2TRAddress(invokeParam.PubKey)
	}
	invokeTx.Invoker = invokerAddr

	return nil
}

func (p *ContractRuntimeBase) IsMyInvoke(invoke *InvokeTx) bool {

	// 输出地址是合约地址
	if p.ChannelAddr != invoke.Address {
		return false
	}

	if invoke.InvokeParam != nil {
		return invoke.InvokeParam.ContractPath == p.RelativePath() ||
			invoke.InvokeParam.ContractPath == p.URL()
	} else if invoke.TxOutput != nil {
		assetName := p.contract.GetAssetName()
		// 除非是符文，否则都是有 InvokeParam
		if assetName.Protocol == indexer.PROTOCOL_NAME_RUNES {
			// 只检查是否有合约对应的资产
			amt := invoke.TxOutput.GetAsset(assetName)
			return amt.Sign() != 0
		}
		// TODO 以后特殊的合约不要单独的地址，所以只要输出地址是合约地址，就不要丢弃
		if indexer.IsPlainAsset(assetName) {
			// 只有transcend支持白聪
			return len(invoke.TxOutput.Assets) == 0
		}
		amt := invoke.TxOutput.GetAsset(assetName)
		return amt.Sign() != 0
	}
	return false
}

func (p *ContractRuntimeBase) CheckInvokeTx(invokeTx *InvokeTx) error {

	if invokeTx.InvokeParam != nil {
		invokeParam := invokeTx.InvokeParam

		if invokeTx.TxOutput == nil {
			return fmt.Errorf("invalid contract invoke TX, fundingOutput is nil")
		}

		if len(invokeParam.PubKey) != 0 && len(invokeParam.Sig) != 0 {
			invoice, err := UnsignedInvokeContractInvoice(invokeParam)
			if err != nil {
				return fmt.Errorf("invalid contract invoice, %v", err)
			}
			pubkey, err := utils.BytesToPublicKey(invokeParam.PubKey)
			if err != nil {
				return fmt.Errorf("invalid contract initor key, %v", err)
			}
			if !VerifyMessage(pubkey, invoice, invokeParam.Sig) {
				return fmt.Errorf("invalid contract sig")
			}
		}

		if len(invokeParam.PubKey) != 0 {
			invokeTx.Invoker = HexPubKeyToP2TRAddress(invokeParam.PubKey)
		}
	} else if invokeTx.TxOutput != nil {
		// 已经检查过
		// 只检查是否有合约对应的资产
		// amt := invokeTx.TxOutput.GetAsset(p.contract.wallet.GetAssetName())
		// if amt.Sign() == 0 {
		// 	// TODO 以后考虑白聪也可以
		// 	return fmt.Errorf("no asset %s", p.contract.wallet.GetAssetName().String())
		// }
	}

	if invokeTx.Invoker == "" {
		// 取最后一个输入作为调用者
		txIn := invokeTx.Tx.TxIn[len(invokeTx.Tx.TxIn)-1]
		preTxId := txIn.PreviousOutPoint.Hash.String()
		txHex, err := p.stp.GetIndexerClient().GetRawTx(preTxId)
		if err != nil {
			Log.Errorf("GetRawTx %s failed, %v", preTxId, err)
			return err
		}
		preTx, err := DecodeMsgTx(txHex)
		if err != nil {
			Log.Errorf("DecodeMsgTx from %s failed, %v", preTxId, err)
			return err
		}
		if int(txIn.PreviousOutPoint.Index) >= len(preTx.TxOut) {
			return fmt.Errorf("index out of bound. %d %s", txIn.PreviousOutPoint.Index, preTxId)
		}
		txOut := preTx.TxOut[txIn.PreviousOutPoint.Index]
		invokeTx.Invoker, err = AddrFromPkScript(txOut.PkScript)
		if err != nil {
			Log.Errorf("AddrFromPkScript %s failed, %v", hex.EncodeToString(txOut.PkScript), err)
			return err
		}

		// 如果输入和输出是同一个地址，很可能是合约地址的withdraw操作，直接忽略
		if invokeTx.TxOutput != nil {
			if bytes.Equal(txOut.PkScript, invokeTx.TxOutput.OutValue.PkScript) {
				return fmt.Errorf("contract withdraw transaction %s, not need to do anything", invokeTx.Tx.TxID())
			}
		}
	}

	return nil
}

func (p *ContractRuntimeBase) PreprocessInvokeData(data *InvokeDataInBlock) error {

	err := p.runtime.AllowInvoke()
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

			_, err = p.runtime.VerifyAndAcceptInvokeItem(tx, data.Height)
			if err == nil {
				Log.Infof("%s invoke %s succeed", p.RelativePath(), tx.Tx.TxID())
				bUpdate = true
			} else {
				Log.Infof("%s invoke %s failed, %v", p.RelativePath(), tx.Tx.TxID(), err)
			}
		}
		if bUpdate {
			p.refreshTime = 0
			p.stp.SaveReservation(p.resv)
		}
	} else {
		Log.Infof("%s allowInvoke failed, %v", p.URL(), err)
	}
	return nil
}

func (p *ContractRuntimeBase) InvokeWithBlock(data *InvokeDataInBlock) error {
	p.mutex.Lock()
	if p.EnableBlockL1 == INIT_ENABLE_BLOCK || data.Height < p.EnableBlockL1 {
		if p.CurrBlockL1 < data.Height {
			// 需要考虑索引器重建数据的可能性，这种情况下，不要更新 p.CurrBlock
			p.CurrBlockL1 = data.Height
		}
		p.lastInvokeCount = p.InvokeCount
		p.mutex.Unlock()
		return fmt.Errorf("not reach enabled block")
	}

	// 这里不处理分叉，分叉由manager去处理
	if p.CurrBlockL1+1 != data.Height {
		// 异常处理流程，一直等到符合目标的区块，不然就不处理
		if p.CurrBlockL1+1 > data.Height {
			// 区块回滚，不在这里处理，防止同一个交易处理多次
			// 也可能是索引器重建数据
			p.lastInvokeCount = p.InvokeCount
			p.mutex.Unlock()
			return fmt.Errorf("contract current block %d >= data block %d, not need to process it", p.CurrBlockL1, data.Height)
		} else { // p.CurrBlockL1+1 < data.Height
			// 不可能出现，启动时已经同步了区块
			// Log.Panicf("%s missing some L2 block, current %d, but new block %d", p.URL(), p.CurrBlockL1, data.Height)
			// 丢失中间的区块
			if p.CurrBlockL1 < p.EnableBlockL1 {
				p.CurrBlockL1 = p.EnableBlockL1
			}

			// 同步缺少的区块，确保合约运行正常
			if p.CurrBlockL1+1 < data.Height {
				p.mutex.Unlock()
				Log.Errorf("%s missing some L1 block, current %d, but new block %d", p.URL(), p.CurrBlockL1, data.Height)
				p.resyncBlock(p.CurrBlockL1+1, data.Height-1)
				Log.Infof("%s has resync L1 from %d to %d", p.URL(), p.CurrBlockL1+1, data.Height-1)
				p.mutex.Lock()
			}
		}
	}

	// 到这里才是正常调用
	p.CurrBlockL1 = data.Height
	p.lastInvokeCount = p.InvokeCount
	p.mutex.Unlock()
	return nil
}

// 外面加锁
func (p *ContractRuntimeBase) InvokeCompleted(data *InvokeDataInBlock) {
	p.invokeCompleted()
}

func (p *ContractRuntimeBase) IsMyInvoke_SatsNet(invoke *InvokeTx_SatsNet) bool {
	// 输出地址是合约地址
	if p.ChannelAddr != invoke.Address { // 有些特殊指令只有op_return，没有对应的output，比如合约激活指令
		channelAddr := ExtractChannelId(invoke.InvokeParam.ContractPath)
		if channelAddr != p.ChannelAddr {
			return false
		}
	}

	return invoke.InvokeParam.ContractPath == p.RelativePath() ||
		invoke.InvokeParam.ContractPath == p.URL()
}

func (p *ContractRuntimeBase) resyncBlock(start, end int) {
	// 设置resv的属性，暂时不要从外面的区块同步调用进来
	p.resv.ResyncBlock(start, end)
}

func (p *ContractRuntimeBase) resyncBlock_SatsNet(start, end int) {
	// 设置resv的属性，暂时不要从外面的区块同步调用进来
	p.resv.ResyncBlock_SatsNet(start, end)
}

func (p *ContractRuntimeBase) PreprocessInvokeData_SatsNet(data *InvokeDataInBlock_SatsNet) error {

	err := p.runtime.AllowInvoke()
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

			_, err = p.runtime.VerifyAndAcceptInvokeItem_SatsNet(tx, data.Height)
			if err == nil {
				Log.Infof("%s Invoke_SatsNet %s succeed", p.RelativePath(), tx.Tx.TxID())
				bUpdate = true
			} else {
				Log.Errorf("%s Invoke_SatsNet %s failed, %v", p.RelativePath(), tx.Tx.TxID(), err)
			}
		}
		if bUpdate {
			p.refreshTime = 0
			p.stp.SaveReservation(p.resv)
		}
	} else {
		//Log.Infof("%s not allowed yet, %v", p.RelativePath(), err)
	}
	return nil
}

// 外面不能加锁
func (p *ContractRuntimeBase) InvokeWithBlock_SatsNet(data *InvokeDataInBlock_SatsNet) error {

	//
	p.mutex.Lock()
	if p.EnableTxId == "" {
		for _, invoke := range data.InvokeTxVect {
			invokeParam := invoke.InvokeParam
			if !p.IsMyInvoke_SatsNet(invoke) {
				continue
			}
			if p.resv.LocalIsInitiator() {
				continue
			}

			var param InvokeParam
			err := param.Decode(invokeParam.InvokeParam)
			if err != nil {
				continue
			}
			if param.Action == INVOKE_API_ENABLE {
				paramBytes, err := base64.StdEncoding.DecodeString(param.Param)
				if err != nil {
					continue
				}
				var innerParam EnableInvokeParam
				err = innerParam.Decode(paramBytes)
				if err != nil {
					continue
				}

				// 服务端更新
				p.SetEnableBlock(innerParam.HeightL2, innerParam.HeightL1)
				p.EnableTxId = invoke.Tx.TxID()
				break
			}
		}
	}

	// 如果还没有激活，直接返回
	if p.EnableBlock == INIT_ENABLE_BLOCK || data.Height < p.EnableBlock {
		if p.CurrBlock < data.Height {
			// 需要考虑索引器重建数据的可能性，这种情况下，不要更新 p.CurrBlock
			p.CurrBlock = data.Height
		}
		p.lastInvokeCount = p.InvokeCount
		p.mutex.Unlock()
		return fmt.Errorf("not reach enabled block")
	}

	// 这里不处理分叉，分叉由manager去处理
	if p.CurrBlock+1 != data.Height {
		// 异常处理流程，一直等到符合目标的区块，不然就不处理
		if p.CurrBlock+1 > data.Height {
			// TODO 区块回滚，不在这里处理，防止同一个交易处理多次
			// 也有可能是索引器在重建数据
			p.lastInvokeCount = p.InvokeCount
			currBlock := p.CurrBlock
			p.mutex.Unlock()
			if currBlock == data.Height && len(data.InvokeTxVect) == 0 {
				// 最高区块，重新进来，看看合约是不是有tx没发送出去，这种情况，不要有调用合约的交易
				return nil
			}
			return fmt.Errorf("contract current block %d large than %d", p.CurrBlock, data.Height)
		} else { // p.CurrBlock+1 < data.Height，需要补足缺少的区块
			// 不可能出现，启动时已经同步了区块
			// Log.Panicf("%s missing some L2 block, current %d, but new block %d", p.URL(), p.CurrBlock, data.Height)
			// 丢失中间的区块
			if p.CurrBlock < p.EnableBlock {
				p.CurrBlock = p.EnableBlock - 3 // 找回 EnableTxId
				if p.CurrBlock < 0 {
					p.CurrBlock = 0
				}
			}

			if p.CurrBlock+1 < data.Height {
				// 同步缺少的区块，确保合约运行正常
				p.mutex.Unlock()
				Log.Errorf("%s missing some L2 block, current %d, but new block %d", p.URL(), p.CurrBlock, data.Height)
				p.resyncBlock_SatsNet(p.CurrBlock+1, data.Height-1)
				Log.Infof("%s has resync L2 from %d to %d", p.URL(), p.CurrBlock+1, data.Height-1)
				p.mutex.Lock()
			}
		}
	}

	// 到这里才是正常调用
	p.CurrBlock = data.Height
	p.lastInvokeCount = p.InvokeCount
	p.mutex.Unlock()
	return nil
}

// 外面加锁
func (p *ContractRuntimeBase) InvokeCompleted_SatsNet(data *InvokeDataInBlock_SatsNet) {
	p.invokeCompleted()
}

func (p *ContractRuntimeBase) checkSelf() error {
	return nil
}

func (p *ContractRuntimeBase) calcAssetMerkleRoot() {
	p.assetMerkleRootMap[(p.lastInvokeCount)] = p.CurrAssetMerkleRoot
	p.CurrAssetMerkleRoot = p.runtime.CalcRuntimeMerkleRoot()
	p.stp.SaveReservationWithLock(p.resv)

	if len(p.assetMerkleRootMap) > 10 {
		// 删除最小的一个
		m := int64(math.MaxInt64)
		for k := range p.assetMerkleRootMap {
			m = min(k, m)
		}
		delete(p.assetMerkleRootMap, m)
	}
}

// 核心是检查输入InUtxo还在不在，不在的话，删除该记录
func (p *ContractRuntimeBase) HandleReorg_SatsNet(orgHeight, currHeight int) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	url := p.URL()
	history := loadContractInvokeHistoryByHeight(p.stp.GetDB(), url, false, orgHeight, true)
	for _, v := range history {
		item, ok := v.(*InvokeItem)
		if !ok {
			continue
		}
		item.UtxoId = 0
		parts := strings.Split(item.InUtxo, ":")
		if len(parts) != 2 {
			continue
		}
		_, err := p.stp.GetIndexerClient_SatsNet().GetTxInfo(parts[0])
		if err != nil {
			// tx 不存在了，需要将该条记录设置为无效
			Log.Warnf("HandleReorg_SatsNet delete invoke item %s", item.InUtxo)
			item.Reason = INVOKE_REASON_UTXO_NOT_FOUND_REORG
			if item.Done == DONE_NOTYET {
				p.runtime.DisableItem(item)
			}
			SaveContractInvokeHistoryItem(p.stp.GetDB(), url, item)
		}

		p.history[item.InUtxo] = item
	}
	p.CurrBlock = orgHeight - 1
	return nil
}

// 核心是检查输入InUtxo还在不在，不在的话，删除该记录
func (p *ContractRuntimeBase) HandleReorg(orgHeight, currHeight int) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	history := loadContractInvokeHistoryByHeight(p.stp.GetDB(), p.URL(), false, orgHeight, false)
	for _, v := range history {
		item, ok := v.(*InvokeItem)
		if !ok {
			continue
		}
		item.UtxoId = 0
		p.history[item.InUtxo] = item
	}
	p.CurrBlockL1 = orgHeight - 1
	return nil
}

func (p *ContractRuntimeBase) invokeCompleted() {
	if p.lastInvokeCount != p.InvokeCount {
		// 只在区块调用结束后更新合约的merkle root，不管合约后续的动作
		p.calcAssetMerkleRoot()
	}
}

func (p *ContractRuntimeBase) AddLostInvokeItem(string, bool) (string, error) {
	return "", fmt.Errorf("not accepted")
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
			NotSign:   true,
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
			stubTx, fee, err := p.stp.CoGenerateStubUtxos(stubNum+10, dealInfo.FeeRate,
				p.URL(), dealInfo.InvokeCount, excludeRecentBlock)
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
			txId, fee, err = p.stp.CoBatchSendV3(sendInfoVect, dealInfo.AssetName.String(), dealInfo.FeeRate,
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
				txId, fee, err = p.stp.CoSendOrdxWithStub(sendInfo.Address,
					sendInfo.AssetName.String(), sendInfo.AssetAmt.Int64(), dealInfo.FeeRate,
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

// TODO
func (p *ContractRuntimeBase) DisableItem(input InvokeHistoryItem) {
	item, ok := input.(*SwapHistoryItem)
	if !ok {
		return
	}

	switch item.OrderType {

	}
}

// TODO 不应该暴露，等resv和contract分离后取消
func (p *ContractRuntimeBase) GetMutex() *sync.RWMutex {
	return &p.mutex
}

func GetSupportedContracts() []string {
	result := make([]string, 0)
	c := NewContract(TEMPLATE_CONTRACT_LAUNCHPOOL)
	if c != nil {
		result = append(result, string(c.Content()))
	}

	c = NewContract(TEMPLATE_CONTRACT_SWAP)
	if c != nil {
		result = append(result, string(c.Content()))
	}

	c = NewContract(TEMPLATE_CONTRACT_AMM)
	if c != nil {
		result = append(result, string(c.Content()))
	}

	c = NewContract(TEMPLATE_CONTRACT_TRANSCEND)
	if c != nil {
		result = append(result, string(c.Content()))
	}

	c = NewContract(TEMPLATE_CONTRACT_VAULT)
	if c != nil {
		result = append(result, string(c.Content()))
	}

	c = NewContract(TEMPLATE_CONTRACT_RECYCLE)
	if c != nil {
		result = append(result, string(c.Content()))
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i] < result[j]
	})

	return result
}

func NewContract(cname string) Contract {
	switch cname {
	case TEMPLATE_CONTRACT_SWAP:
		return NewSwapContract()

	case TEMPLATE_CONTRACT_AMM:
		return NewAmmContract()

	case TEMPLATE_CONTRACT_LAUNCHPOOL:
		return NewLaunchPoolContract()

	case TEMPLATE_CONTRACT_TRANSCEND:
		return NewTranscendContract()

	case TEMPLATE_CONTRACT_RECYCLE:
		return NewRecycleContract()
	}
	return nil
}

func ContractContentUnMarsh(cname string, jsonStr string) (Contract, error) {
	c := NewContract(cname)
	if c == nil {
		return nil, fmt.Errorf("invalid contract name %s", cname)
	}
	err := json.Unmarshal([]byte(jsonStr), &c)
	if err != nil {
		return nil, err
	}

	err = c.CheckContent()
	if err != nil {
		return nil, err
	}

	return c, nil
}

func NewContractRuntime(stp ContractManager, cname string) ContractRuntime {
	var r ContractRuntime
	switch cname {
	case TEMPLATE_CONTRACT_SWAP:
		return NewSwapContractRuntime(stp)

	case TEMPLATE_CONTRACT_AMM:
		return NewAmmContractRuntime(stp)

	case TEMPLATE_CONTRACT_LAUNCHPOOL:
		r = NewLaunchPoolContractRuntime(stp)

	case TEMPLATE_CONTRACT_TRANSCEND:
		r = NewTranscendContractRuntime(stp)

	case TEMPLATE_CONTRACT_RECYCLE:
		return NewRecycleContractRunTime(stp)
	}

	return r
}

func ContractRuntimeUnMarsh(stp ContractManager, cname string, data []byte) (ContractRuntime, error) {
	c := NewContractRuntime(stp, cname)
	if c == nil {
		return nil, fmt.Errorf("invalid contract name %s", cname)
	}
	err := DecodeFromBytes(data, c)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func GenerateContractURl(channelId, assetName, tc string) string {
	return channelId + URL_SEPARATOR + assetName + URL_SEPARATOR + tc
}

func GenerateContractRelativePath(assetName, tc string) string {
	return assetName + URL_SEPARATOR + tc
}

func ExtractChannelId(url string) string {
	parts := strings.Split(url, URL_SEPARATOR)
	if len(parts) < 3 {
		return ""
	}
	return parts[0]
}

func ExtractAssetName(url string) string {
	parts := strings.Split(url, URL_SEPARATOR)
	if len(parts) < 2 {
		return ""
	}
	if len(parts) < 3 {
		return parts[0]
	}
	return parts[1]
}

func ExtractContractType(url string) string {
	parts := strings.Split(url, URL_SEPARATOR)
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func ExtractRelativePath(url string) string {
	parts := strings.Split(url, URL_SEPARATOR)
	if len(parts) < 2 {
		return url
	}
	return parts[1] + URL_SEPARATOR + parts[2]
}

// channelId, assetName, contractType
func ParseContractURL(url string) (string, string, string, error) {
	parts := strings.Split(url, URL_SEPARATOR)
	// channelid-assetname-type
	if len(parts) == 3 {
		return parts[0], parts[1], parts[2], nil
	}
	if len(parts) == 2 {
		// assetname, type
		return "", parts[0], parts[1], nil
	}
	if len(parts) == 1 {
		return "", "", parts[0], nil
	}

	return "", "", "", fmt.Errorf("invalid format of contract url. %s", url)
}

// full path
func IsValidContractURL(url string) bool {
	parts := strings.Split(url, URL_SEPARATOR)
	// channelid-assetname-type
	if len(parts) != 3 {
		return false
	}
	if len(parts[0]) == 0 || len(parts[1]) == 0 || len(parts[2]) == 0 {
		return false
	}
	// check template type ?
	return true
}

// 合约部署
func UnsignedDeployContractInvoiceV2(data *sindexer.ContractDeployData) ([]byte, error) {
	return stxscript.NewScriptBuilder().
		AddData([]byte(data.ContractPath)).
		AddData([]byte(data.ContractContent)).
		AddInt64(int64(data.DeployTime)).Script()
}

func SignedDeployContractInvoiceV2(data *sindexer.ContractDeployData) ([]byte, error) {
	return stxscript.NewScriptBuilder().
		AddData([]byte(data.ContractPath)).
		AddData([]byte(data.ContractContent)).
		AddInt64(int64(data.DeployTime)).
		AddData(data.LocalSign).
		AddData(data.RemoteSign).Script()
}

func UnsignedContractEnabledInvoice(url string, heightL1, heightL2 int, pubKey []byte) ([]byte, error) {
	return stxscript.NewScriptBuilder().
		AddData([]byte(url)).
		AddInt64(int64(heightL1)).
		AddInt64(int64(heightL2)).
		AddData(pubKey).Script()
}

// 用在有特别需求的合约
func SignedContractEnabledInvoice(url string, heightL1, heightL2 int, pubKey []byte, sig []byte) ([]byte, error) {
	return stxscript.NewScriptBuilder().
		AddData([]byte(url)).
		AddInt64(int64(heightL1)).
		AddInt64(int64(heightL2)).
		AddData(pubKey).
		AddData(sig).Script()
}

// 合约调用：大多数都只需要这个简化的调用参数
func AbbrInvokeContractInvoice(data *sindexer.ContractInvokeData) ([]byte, error) {
	return stxscript.NewScriptBuilder().
		AddData([]byte(data.ContractPath)).
		AddData([]byte(data.InvokeParam)).Script()
}

func UnsignedInvokeContractInvoice(data *sindexer.ContractInvokeData) ([]byte, error) {
	return stxscript.NewScriptBuilder().
		AddData([]byte(data.ContractPath)).
		AddData([]byte(data.InvokeParam)).
		AddData(data.PubKey).Script()
}

// 用在有特别需求的合约
func SignedInvokeContractInvoice(data *sindexer.ContractInvokeData, sig []byte) ([]byte, error) {
	return stxscript.NewScriptBuilder().
		AddData([]byte(data.ContractPath)).
		AddData([]byte(data.InvokeParam)).
		AddData(data.PubKey).
		AddData(sig).Script()
}

// 简化的调用invoice，仅用于主网
func UnsignedAbbrInvokeContractInvoice(contractUrl string, param *InvokeParam) ([]byte, error) {

	_, asetName, templateName, err := ParseContractURL(contractUrl)
	if err != nil {
		return nil, err
	}

	return stxscript.NewScriptBuilder().
		AddData([]byte(templateName)).
		AddData([]byte(asetName)).
		AddData([]byte(param.Action)).Script()
}

func SignedAbbrInvokeContractInvoice(contractUrl string, param *InvokeParam, sig []byte) ([]byte, error) {

	_, asetName, templateName, err := ParseContractURL(contractUrl)
	if err != nil {
		return nil, err
	}

	hashCode := sig[:8]

	return stxscript.NewScriptBuilder().
		AddData([]byte(templateName)).
		AddData([]byte(asetName)).
		AddData([]byte(param.Action)).
		AddData([]byte(hashCode)).Script()
}

// 合约调用结果
func UnsignedContractResultInvoice(contractPath string, result string, more string) ([]byte, error) {
	return stxscript.NewScriptBuilder().
		AddData([]byte(contractPath)).
		AddData([]byte(result)).
		AddData([]byte(more)).Script()
}

func ParseContractResultInvoice(script []byte) (string, string, string, error) {

	tokenizer := stxscript.MakeScriptTokenizer(0, script)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return "", "", "", fmt.Errorf("missing contract path")
	}
	contractURL := string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return "", "", "", fmt.Errorf("missing invoke result")
	}
	result := string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return contractURL, result, "", nil
	}
	more := string(tokenizer.Data())

	return contractURL, result, more, nil
}

type InvokeTx_SatsNet struct {
	Tx          *swire.MsgTx
	TxIndex     int
	InvokeVout  int
	TxOutput    *TxOutput_SatsNet
	Address     string
	InvokeParam *sindexer.ContractInvokeData
	Invoker     string
}

type InvokeDataInBlock_SatsNet struct {
	Height       int
	BlockHash    string
	InvokeTxVect []*InvokeTx_SatsNet
}

type InvokeTx struct {
	Tx          *wire.MsgTx
	TxIndex     int
	InvokeVout  int
	TxOutput    *TxOutput
	Address     string
	InvokeParam *sindexer.ContractInvokeData
	Invoker     string
}

type InvokeDataInBlock struct {
	Height       int
	BlockHash    string
	InvokeTxVect []*InvokeTx
}

func GetInvokeInnerParam(action string) InvokeInnerParamIF {
	orderType := GetOrderTypeWithAction(action)
	switch action {
	case INVOKE_API_SWAP:
		return &SwapInvokeParam{OrderType: orderType}

	case INVOKE_API_STAKE:
		return &StakeInvokeParam{OrderType: orderType}

	case INVOKE_API_UNSTAKE:
		return &UnstakeInvokeParam{OrderType: orderType}

	case INVOKE_API_DEPOSIT:
		return &DepositInvokeParam{OrderType: orderType}

	case INVOKE_API_WITHDRAW:
		return &WithdrawInvokeParam{OrderType: orderType}

	case INVOKE_API_ADDLIQUIDITY:
		return &AddLiqInvokeParam{OrderType: orderType}

	case INVOKE_API_REMOVELIQUIDITY:
		return &RemoveLiqInvokeParam{OrderType: orderType}

	case INVOKE_API_PROFIT:
		return &ProfitInvokeParam{OrderType: orderType}

	case INVOKE_API_RECYCLE:
		return &RecycleInvokeParam{}
	case INVOKE_API_REWARD:
		return &RecycleInvokeParam{}

	default:
		return nil
	}
}

func GetOrderTypeWithAction(action string) int {
	switch action {
	case INVOKE_API_DEPOSIT:
		return ORDERTYPE_DEPOSIT
	case INVOKE_API_REFUND:
		return ORDERTYPE_REFUND
	case INVOKE_API_STAKE:
		return ORDERTYPE_STAKE
	case INVOKE_API_UNSTAKE:
		return ORDERTYPE_UNSTAKE
	case INVOKE_API_WITHDRAW:
		return ORDERTYPE_WITHDRAW

	case INVOKE_API_ADDLIQUIDITY:
		return ORDERTYPE_ADDLIQUIDITY

	case INVOKE_API_REMOVELIQUIDITY:
		return ORDERTYPE_REMOVELIQUIDITY

	case INVOKE_API_PROFIT:
		return ORDERTYPE_PROFIT

	case INVOKE_API_RECYCLE:
		return ORDERTYPE_RECYCLE
	case INVOKE_API_REWARD:
		return ORDERTYPE_REWARD

	default:
		return ORDERTYPE_SELL
	}
}

// 将json格式的调用参数，转换为script格式的参数
func ConvertInvokeParam(jsonInvokeParam string, abbr bool) (*InvokeParam, error) {
	var wrapperParam InvokeParam
	err := json.Unmarshal([]byte(jsonInvokeParam), &wrapperParam)
	if err != nil {
		return nil, err
	}
	param := GetInvokeInnerParam(wrapperParam.Action)
	if param != nil {
		err = json.Unmarshal([]byte(wrapperParam.Param), param)
		if err != nil {
			return nil, err
		}
		var innerParam []byte
		if abbr {
			innerParam, err = param.EncodeV2()
		} else {
			innerParam, err = param.Encode()
		}
		if err != nil {
			return nil, err
		}
		wrapperParam.Param = base64.StdEncoding.EncodeToString(innerParam)
	}
	return &wrapperParam, nil

	// switch wrapperParam.Action {
	// case INVOKE_API_SWAP:
	// 	var swapParam SwapInvokeParam
	// 	err = json.Unmarshal([]byte(wrapperParam.Param), &swapParam)
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// 	innerParam, err := swapParam.Encode()
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// 	wrapperParam.Param = base64.StdEncoding.EncodeToString(innerParam)

	// case INVOKE_API_WITHDRAW:
	// 	var param WithdrawInvokeParam
	// 	err = json.Unmarshal([]byte(wrapperParam.Param), &param)
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// 	innerParam, err := param.Encode()
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// 	wrapperParam.Param = base64.StdEncoding.EncodeToString(innerParam)

	// case INVOKE_API_MINT:

	// case INVOKE_API_REFUND:

	// case INVOKE_API_ADDLIQUIDITY:
	// 	var stakeParam AddLiqInvokeParam
	// 	err = json.Unmarshal([]byte(wrapperParam.Param), &stakeParam)
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// 	fee += stakeParam.Value
	// 	innerParam, err := stakeParam.Encode()
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// 	wrapperParam.Param = base64.StdEncoding.EncodeToString(innerParam)

	// case INVOKE_API_REMOVELIQUIDITY:
	// 	var stakeParam RemoveLiqInvokeParam
	// 	err = json.Unmarshal([]byte(wrapperParam.Param), &stakeParam)
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// 	innerParam, err := stakeParam.Encode()
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// 	wrapperParam.Param = base64.StdEncoding.EncodeToString(innerParam)

	// default:
	// 	return nil, fmt.Errorf("unsupport action %s", wrapperParam.Action)
	// }
	//return &wrapperParam, nil
}
