package wallet

import (
	
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sat20-labs/satoshinet/txscript"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/runes/runestone"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
)

// 仅作为客户端参考定义

const (
	URL_SEPARATOR = "_"

	TEMPLATE_CONTRACT_SWAP        string = "swap.tc"
	TEMPLATE_CONTRACT_AMM         string = "amm.tc"
	TEMPLATE_CONTRACT_ASSETLOCKER string = "assetlocker.tc"
	TEMPLATE_CONTRACT_LAUNCHPOOL  string = "launchpool.tc"

	CONTRACT_STATUS_EXPIRED int = -2
	CONTRACT_STATUS_CLOSED  int = -1
	CONTRACT_STATUS_INIT    int = 0
	// ...   deploying status
	CONTRACT_STATUS_READY int = 100 // 正常工作阶段
	// ...   running status
	CONTRACT_STATUS_CLOSING int = 200 // 进入最后的关闭阶段
	// ...   closing status, at last change to CONTRACT_STATUS_CLOSED or CONTRACT_STATUS_EXPIRED

	INVOKE_API_ENABLE		string = "enable"	// 每个合约的第一个调用，用来激活合约

	INVOKE_RESULT_OK     	string = "ok"
	INVOKE_RESULT_REFUND 	string = "refund"
	INVOKE_RESULT_DEAL   	string = "deal"
	INVOKE_RESULT_DEPOSIT   string = "deposit"
	INVOKE_RESULT_WITHDRAW  string = "withdraw"
	INVOKE_RESULT_DEANCHOR  string = "deanchor"
	INVOKE_RESULT_ANCHOR    string = "anchor"
)

const (
	INVOKE_REASON_NORMAL 			string = ""
	INVOKE_REASON_REFUND            string = "refund"
	INVOKE_REASON_INVALID 			string = "invalid"  // 参数错误
	INVOKE_REASON_INNER_ERROR 	 	string = "inner error"  // 内部错误
	INVOKE_REASON_NO_ENOUGH_ASSET 	string = "no enough asset"
	INVOKE_REASON_SLIPPAGE_PROTECT 	string = "slippage protection"
)


// 用在开发过程修改数据库，设置为true，然后数据库自动升级，然后马上要设置为false，并且将所有oldversion的数据结果，等同于最新结构
const ContractRuntimeBaseUpgrade = false


type Contract interface {
	GetTemplateName() string          // 合约模版名称
	GetAssetName() *indexer.AssetName // 资产名称
	GetContractName() string          // 完整名字，包括更多信息
	CheckContent() error              // 合约内容检查，部署前调用
	Content() string                  // 合约内容， json格式
	InvokeParam(string) string        // 调用合约的参数， json格式
	GetContractBase() *ContractBase

	Encode() ([]byte, error) // 合约内容， script格式
	Decode([]byte) error     // 合约内容， script格式

	GetStartBlock() int
	GetEndBlock() int

	DeployFee(feeRate int64) int64 // in satsnet，部署这个合约的人需要支付的费用
}

type ContractRuntime interface {
	Contract

	InitFromContent([]byte, *Manager) error // 根据合约模版参数初始化合约，非json
	GetRuntimeBase() *ContractRuntimeBase

	GetStatus() int
	Address() string      // 合约地址
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
	IsExpired() bool
	GetEnableBlock() (int)
	GetEnableBlockL1() (int)

	// 合约调用的支持接口
	CheckInvokeParam(string) (int64, error)    // 调用合约的参数检查(json)，调用合约前调用
	AllowInvoke(*Manager) error
	
}

// 合约调用历史记录
type InvokeHistoryItem interface {
	GetVersion() int
	GetId() int64
	GetKey() string
	HasDone() bool
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

func (p *InvokeHistoryItemBase) ToNewVersion() InvokeHistoryItem {
	return p
}

func GetKeyFromId(id int64) string {
	return fmt.Sprintf("%012d", id)
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
	HeightL1  int 	 `json:"heightL1"`  
	HeightL2  int 	 `json:"heightL2"` 
}

func (p *EnableInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.HeightL1)).
		AddInt64(int64(p.HeightL2)).Script()
}

func (p *EnableInvokeParam) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)

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
	return txscript.NewScriptBuilder().
		AddData([]byte(p.TemplateName)).
		AddData([]byte(p.AssetName.String())).
		AddInt64(int64(p.StartBlock)).
		AddInt64(int64(p.EndBlock)).Script()
}

func (p *ContractBase) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)

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

// 合约运行时基础结构，合约区块以聪网为主，主网区块辅助使用
type ContractRuntimeBase struct {
	DeployTime   int64  `json:"deployTime"` // s
	Status       int    `json:"status"`
	EnableBlock  int    `json:"enableBlock"`  // 合约在哪个区块进入ready状态
	CurrBlock    int    `json:"currentBlock"` // 合约区块不能跳，必须从EnableBlock开始，一块一块执行
	EnableBlockL1  int  `json:"enableBlockL1"`  // 合约在哪个区块进入ready状态
	CurrBlockL1    int  `json:"currentBlockL1"` // 合约区块不能跳，必须从EnableBlock开始，一块一块执行
	EnableTxId	 string `json:"enableTxId"`		// 只设置，暂时没有用起来
	Deployer     string `json:"deployer"`
	ResvId       int64  `json:"resvId"`
	ChannelId    string `json:"channelId"`
	InvokeCount  int64  `json:"invokeCount"`
	Divisibility int    `json:"divisibility"`
	N            int    `json:"n"`
	
	CheckPoint	int  // 上个检查高度
	CheckPointL1 int	 // 
	StaticMerkleRoot []byte	// 合约静态数据
	AssetMerkleRoot  []byte	// 上个检查的资产状态数据
	CurrAssetMerkleRoot  []byte	// 当前高度下的资产状态数据

	stp      *Manager
	contract Contract
}

func (p *ContractRuntimeBase) ToNewVersion() *ContractRuntimeBase {
	return p
}

func (p *ContractRuntimeBase) GetAssetNameV2() *AssetName {
	return &AssetName{
		AssetName: *p.contract.GetAssetName(),
		N: p.N,
	}
}

func (p *ContractRuntimeBase) InitFromContent(content []byte, stp *Manager) error {

	
	// p.ChannelId = resv.ChannelId
	// p.Deployer = resv.Deployer
	p.stp = stp

	err := p.contract.Decode(content)
	if err != nil {
		return err
	}
	err = p.contract.CheckContent()
	if err != nil {
		return err
	}

	tickInfo := stp.getTickerInfo(p.contract.GetAssetName())
	if tickInfo != nil {
		p.Divisibility = tickInfo.Divisibility
		p.N = tickInfo.N
	} else {
		if p.contract.GetTemplateName() != TEMPLATE_CONTRACT_LAUNCHPOOL {
			return fmt.Errorf("%s can't find ticker %s", p.URL(), p.contract.GetAssetName())
		}
		// 发射池合约肯定找不到，但本身就有足够的数据
		// 由合约自己设置
	}

	return nil
}


func (p *ContractRuntimeBase) GetRuntimeBase() *ContractRuntimeBase {
	return p
}

func (p *ContractRuntimeBase) GetDeployTime() int64 {
	return p.DeployTime
}


func (p *ContractRuntimeBase) DeploySelf() bool {
	return false
}

func (p *ContractRuntimeBase) Address() string {
	return p.ChannelId
}

func (p *ContractRuntimeBase) URL() string {
	return p.ChannelId + URL_SEPARATOR + p.contract.GetContractName()
}

func (p *ContractRuntimeBase) RelativePath() string {
	return p.contract.GetContractName()
}

func (p *ContractRuntimeBase) GetStatus() int {
	return p.Status
}


func (p *ContractRuntimeBase) GetEnableBlock() int {
	return p.EnableBlock
}

func (p *ContractRuntimeBase) GetEnableBlockL1() int {
	return p.EnableBlockL1
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


func (p *ContractRuntimeBase) IsActive() bool {
	return p.Status == CONTRACT_STATUS_READY && p.EnableBlock <= p.CurrBlock
}


func (p *ContractRuntimeBase) CheckInvokeParam(string) (int64, error) {
	return 0, nil
}

func (p *ContractRuntimeBase) AllowInvoke(stp *Manager) error {

	// resv, ok := r.(*ContractDeployReservation)
	// if !ok {
	// 	return fmt.Errorf("not ContractDeployReservation")
	// }
	if p.Status != CONTRACT_STATUS_READY {
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

func (p *ContractRuntimeBase) AllowPeerAction(*Manager, string, any) (any, error) {
	return nil, fmt.Errorf("not allow")
}

func (p *ContractRuntimeBase) SetPeerActionResult(*Manager, string, any) {

}

func NewContract(cname string) Contract {
	switch cname {
	case TEMPLATE_CONTRACT_SWAP:
		return NewSwapContract()

	case TEMPLATE_CONTRACT_AMM:
		return NewAmmContract()

	case TEMPLATE_CONTRACT_ASSETLOCKER:
		return NewAssetLockContract()

	case TEMPLATE_CONTRACT_LAUNCHPOOL:
		return NewLaunchPoolContract()
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

func NewContractRuntime(stp *Manager, cname string) ContractRuntime {
	var r ContractRuntime
	switch cname {
	case TEMPLATE_CONTRACT_SWAP:
		return NewSwapContractRuntime(stp)

	case TEMPLATE_CONTRACT_AMM:
		return NewAmmContractRuntime(stp)

	case TEMPLATE_CONTRACT_ASSETLOCKER:
		//return NewMintServerContract()

	case TEMPLATE_CONTRACT_LAUNCHPOOL:
		r = NewLaunchPoolContractRuntime(stp)
	}

	return r
}

func ContractRuntimeUnMarsh(stp *Manager, cname string, data []byte) (ContractRuntime, error) {
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

// 合约部署
func UnsignedDeployContractInvoiceV2(data *sindexer.ContractDeployData) ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(data.ContractPath)).
		AddData([]byte(data.ContractContent)).
		AddInt64(int64(data.DeployTime)).Script()
}

func SignedDeployContractInvoiceV2(data *sindexer.ContractDeployData) ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(data.ContractPath)).
		AddData([]byte(data.ContractContent)).
		AddInt64(int64(data.DeployTime)).
		AddData(data.LocalSign).
		AddData(data.RemoteSign).Script()
}


func UnsignedContractEnabledInvoice(url string, heightL1, heightL2 int, pubKey []byte) ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(url)).
		AddInt64(int64(heightL1)).
		AddInt64(int64(heightL2)).
		AddData(pubKey).Script()
}

// 用在有特别需求的合约
func SignedContractEnabledInvoice(url string, heightL1, heightL2 int, pubKey []byte, sig []byte) ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(url)).
		AddInt64(int64(heightL1)).
		AddInt64(int64(heightL2)).
		AddData(pubKey).
		AddData(sig).Script()
}

// 合约调用：大多数都只需要这个简化的调用参数
func AbbrInvokeContractInvoice(data *sindexer.ContractInvokeData) ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(data.ContractPath)).
		AddData([]byte(data.InvokeParam)).Script()
}

func UnsignedInvokeContractInvoice(data *sindexer.ContractInvokeData) ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(data.ContractPath)).
		AddData([]byte(data.InvokeParam)).
		AddData(data.PubKey).Script()
}

// 用在有特别需求的合约
func SignedInvokeContractInvoice(data *sindexer.ContractInvokeData, sig []byte) ([]byte, error) {
	return txscript.NewScriptBuilder().
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

	return txscript.NewScriptBuilder().
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

	return txscript.NewScriptBuilder().
		AddData([]byte(templateName)).
		AddData([]byte(asetName)).
		AddData([]byte(param.Action)).
		AddData([]byte(hashCode)).Script()
}

// 合约调用结果
func UnsignedContractResultInvoice(contractPath string, result string, more string) ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(contractPath)).
		AddData([]byte(result)).
		AddData([]byte(more)).Script()
}

func ParseContractResultInvoice(script []byte) (string, string, string, error) {

	tokenizer := txscript.MakeScriptTokenizer(0, script)

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
