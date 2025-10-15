package wallet

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
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

	TEMPLATE_CONTRACT_SWAP        string = "swap.tc"
	TEMPLATE_CONTRACT_AMM         string = "amm.tc"
	TEMPLATE_CONTRACT_VAULT       string = "vault.tc"
	TEMPLATE_CONTRACT_LAUNCHPOOL  string = "launchpool.tc"
	TEMPLATE_CONTRACT_STAKE       string = "stake.tc"
	TEMPLATE_CONTRACT_TRANSCEND   string = "transcend.tc" // 支持任意资产进出通道，优先级比 TEMPLATE_CONTRACT_AMM 低
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
	INVOKE_RESULT_REFUND          string = "refund"
	INVOKE_RESULT_DEAL            string = "deal"
	INVOKE_RESULT_DEPOSIT         string = "deposit"
	INVOKE_RESULT_WITHDRAW        string = "withdraw"
	INVOKE_RESULT_DEANCHOR        string = "deanchor"
	INVOKE_RESULT_ANCHOR          string = "anchor"
	INVOKE_RESULT_STAKE           string = "stake"
	INVOKE_RESULT_UNSTAKE         string = "unstake"
	INVOKE_RESULT_ADDLIQUIDITY    string = "addliq"
	INVOKE_RESULT_REMOVELIQUIDITY string = "removeliq"
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
	ORDERTYPE_UNUSED          = 13

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

	CoGenerateStubUtxos(n int, contractURL string, invokeCount int64, excludeRecentBlock bool) (string, int64, error)
	CoBatchSendV3(dest []*SendAssetInfo, assetNameStr string,
		reason, contractURL string, invokeCount int64, memo, static, runtime []byte,
		sendDeAnchorTx, excludeRecentBlock bool) ([]string, int64, error)
	CoSendOrdxWithStub(dest string, assetNameStr string, amt int64, stub string,
		reason, contractURL string, invokeCount int64, memo, static, runtime []byte,
		sendDeAnchorTx, excludeRecentBlock bool) ([]string, int64, error)
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
	GetRedeemScript() []byte
	GetPkScript() []byte
	GetLocalPkScript() []byte
	GetRemotePkScript() []byte
	GetLocalAddress() string
	GetRemoteAddress() string
	Address() string // 合约钱包地址
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
	Invoke_SatsNet(*InvokeTx_SatsNet, int) (InvokeHistoryItem, error) // return：被接受，处理结果
	Invoke(*InvokeTx, int) (InvokeHistoryItem, error)                 // return：被接受，处理结果
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

	OrderType      int    //
	UtxoId         uint64 // 其实是utxoId
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

type InvokeParam struct {
	Action string `json:"action"`
	Param  string `json:"param,omitempty"` // 外部使用时是json，内部使用时是编码过的string
}

func (p *InvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(p.Action)).
		AddData([]byte(p.Param)).Script()
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
}

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
	if p.AssetName.Protocol != indexer.PROTOCOL_NAME_ORDX &&
		p.AssetName.Protocol != indexer.PROTOCOL_NAME_RUNES {
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
	}

	if indexer.IsPlainAsset(&p.AssetName) {
		return fmt.Errorf("should be one asset")
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

// 合约运行时基础结构，合约区块以聪网为主，主网区块辅助使用
type ContractRuntimeBase struct {
	DeployTime    int64  `json:"deployTime"` // s
	Status        int    `json:"status"`
	EnableBlock   int    `json:"enableBlock"`    // 合约在哪个区块进入ready状态
	CurrBlock     int    `json:"currentBlock"`   // 合约区块不能跳，必须一块一块执行，即使EnableBlock还没到，也要同步
	EnableBlockL1 int    `json:"enableBlockL1"`  // 合约在哪个区块进入ready状态
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

	mutex sync.RWMutex
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
		var address string
		if p.isInitiator {
			address = p.GetLocalAddress()
		} else {
			address = p.GetRemoteAddress()
		}

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

	if p.EnableBlock == 0 {
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

func (p *ContractRuntimeBase) InvokeHistory(any, int, int) string {
	return ""
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
				return nil, fmt.Errorf("invoke count is inconsistent, %d %d", p.InvokeCount, req.InvokeCount)
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
		return invoke.InvokeParam.ContractPath != p.RelativePath() ||
			invoke.InvokeParam.ContractPath != p.URL()
	} else if invoke.TxOutput != nil {
		// 只检查是否有合约对应的资产
		assetName := p.contract.GetAssetName()
		if indexer.IsPlainAsset(assetName) {
			// TODO 目前只有transcend支持白聪
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

func (p *ContractRuntimeBase) InvokeWithBlock(data *InvokeDataInBlock) error {
	p.mutex.Lock()
	if p.EnableBlockL1 == 0 || data.Height < p.EnableBlockL1 {
		if p.CurrBlockL1 == 0 {
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
			// TODO 区块回滚，不在这里处理，防止同一个交易处理多次
			// 也可能是索引器重建数据
			p.mutex.Unlock()
			return fmt.Errorf("contract current block %d large than %d", p.CurrBlockL1, data.Height)
		} else { // p.CurrBlockL1+1 < data.Height
			// 不可能出现，启动时已经同步了区块
			// Log.Panicf("%s missing some L2 block, current %d, but new block %d", p.URL(), p.CurrBlockL1, data.Height)
			// 丢失中间的区块
			if p.CurrBlockL1 < p.EnableBlockL1 {
				p.CurrBlockL1 = p.EnableBlockL1
			}

			// 同步缺少的区块，确保合约运行正常
			p.mutex.Unlock()
			Log.Errorf("%s missing some L1 block, current %d, but new block %d", p.URL(), p.CurrBlockL1, data.Height)
			p.resyncBlock(p.CurrBlockL1+1, data.Height-1)
			Log.Infof("%s has resync L1 from %d to %d", p.URL(), p.CurrBlockL1+1, data.Height-1)
			p.mutex.Lock()
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
	if p.ChannelAddr != invoke.Address {
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
	if p.EnableBlock == 0 || data.Height < p.EnableBlock {
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
			if currBlock == data.Height {
				// 最高区块，重新进来，看看合约是不是有tx没发送出去
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

			// 同步缺少的区块，确保合约运行正常
			p.mutex.Unlock()
			Log.Errorf("%s missing some L2 block, current %d, but new block %d", p.URL(), p.CurrBlock, data.Height)
			p.resyncBlock_SatsNet(p.CurrBlock+1, data.Height-1)
			Log.Infof("%s has resync L2 from %d to %d", p.URL(), p.CurrBlock+1, data.Height-1)
			p.mutex.Lock()
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

	//case TEMPLATE_CONTRACT_VAULT:
	//return NewVaultContract()

	case TEMPLATE_CONTRACT_LAUNCHPOOL:
		return NewLaunchPoolContract()

	case TEMPLATE_CONTRACT_TRANSCEND:
		return NewTranscendContract()
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

	//case TEMPLATE_CONTRACT_VAULT:
	//return NewVaultContractRuntime(stp)

	case TEMPLATE_CONTRACT_LAUNCHPOOL:
		r = NewLaunchPoolContractRuntime(stp)

	case TEMPLATE_CONTRACT_TRANSCEND:
		r = NewTranscendContractRuntime(stp)
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
	InvokeTxVect []*InvokeTx
}
