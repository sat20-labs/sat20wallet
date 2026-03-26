package wallet

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func init() {
	// 让 gob 知道旧的类型对应新的实现
	// gob.RegisterName("DaoContractRunTime", new(DaoContractRunTime))
	gob.Register(new(DaoContractRunTime))
}

/*
社区基金管理合约：按照社区制定的规则，管理社区基金 （独立的地址）
功能：
1. 社区成员UID注册（UID由社区线下约定）
2. 社区资金池管理
	a 捐献记录
	b 空投记录
3. 空投条件设置
4. 空投申请
5. 空投审核

*/

const (
	MIN_REGISTER_FEE int64 = 20
	MIN_AIRDROP_FEE  int64 = 20

	DEF_VALIDATOR_NUM int = 5
	RANKING_MAX_SIZE  int = 100

	RESULT_UNHANDLED int = 1
	RESULT_OK 		 int = 0
	RESULT_REJECTED  int = -1
	RESULT_INVALID 	 int = -2
)

// 1. 定义合约内容
type DaoContract struct {
	ContractBase
	// 池子最少的激活资产 （暂时没有使用到，直接设置为0）
	AssetAmt string
	SatValue int64

	ValidatorNum     int   // 默认5
	RegisterFee      int64 // 最少20
	RegisterTimeOut  int   // 聪网区块数，注册审核时间，超时自动确认，一般设置为 7200 （1天）
	OnlyRegisterSelf bool  // true：只能自己注册。默认是false，支持推荐人注册被推荐人，但优先级低于被推荐人自己注册

	// airdrop conditions
	HoldingAssetName      indexer.AssetName // 持有该类资产，
	HoldingAssetThreshold string            // 并且数量大于这个门限，才能获得空投
	AirDropRatio          string            // 乘数，任意浮点数，比如1，小数位数最好不要太长
	AirDropLimit          string            // 空投量限额
	AirDropTimeOut        int               // 聪网区块数，超时自动确认，一般设置为 7200 （1天）
	ReferralRatio         int               // 百分比，默认为0，在空投中，一部分给被推荐人 （暂时没有用到，直接设置为0）

	// 更多的配置数据
}

func NewDaoContract() *DaoContract {
	c := &DaoContract{
		ContractBase: ContractBase{
			TemplateName: TEMPLATE_CONTRACT_DAO,
		},
		RegisterFee:  MIN_REGISTER_FEE,
		ValidatorNum: DEF_VALIDATOR_NUM,
	}
	c.contract = c
	return c
}

func (p *DaoContract) IsExclusive() bool {
	return true
}

func (p *DaoContract) CheckContent() error {

	if p.AssetAmt != "" && p.AssetAmt != "0" {
		_, err := indexer.NewDecimalFromString(p.AssetAmt, MAX_ASSET_DIVISIBILITY)
		if err != nil {
			return fmt.Errorf("invalid AssetAmt %s", p.AssetAmt)
		}
	}
	if p.SatValue < 0 {
		return fmt.Errorf("invalid SatValue")
	}

	//
	if p.RegisterFee < MIN_REGISTER_FEE {
		return fmt.Errorf("invalid RegisterFee, should >= %d", MIN_REGISTER_FEE)
	}
	if p.ValidatorNum <= 0 {
		return fmt.Errorf("validator number should >= 0")
	}

	_, err := indexer.NewDecimalFromString(p.HoldingAssetThreshold, MAX_ASSET_DIVISIBILITY)
	if err != nil {
		return fmt.Errorf("invalid HoldingAssetAmt %s", p.HoldingAssetThreshold)
	}
	_, err = indexer.NewDecimalFromString(p.AirDropRatio, MAX_ASSET_DIVISIBILITY)
	if err != nil {
		return fmt.Errorf("invalid AirDropRatio %s", p.HoldingAssetThreshold)
	}

	if p.AirDropLimit != "" && p.AirDropLimit != "0" {
		_, err = indexer.NewDecimalFromString(p.AirDropLimit, MAX_ASSET_DIVISIBILITY)
		if err != nil {
			return fmt.Errorf("invalid AirDropLimit %s", p.AirDropLimit)
		}
	}
	if p.ReferralRatio < 0 {
		return fmt.Errorf("invalid ReferralRatio")
	}

	err = p.ContractBase.CheckContent()
	if err != nil {
		return err
	}

	return nil
}

func (p *DaoContract) Content() string {
	b, err := json.Marshal(p)
	if err != nil {
		Log.Errorf("Marshal Contract failed, %v", err)
		return ""
	}
	return string(b)
}

func (p *DaoContract) Encode() ([]byte, error) {
	base, err := p.ContractBase.Encode()
	if err != nil {
		return nil, err
	}
	var OnlyRegisterSelf int64
	if p.OnlyRegisterSelf {
		OnlyRegisterSelf = 1
	}

	return txscript.NewScriptBuilder().
		AddData(base).
		AddData([]byte(p.AssetAmt)).
		AddInt64(p.SatValue).
		AddInt64(int64(p.ValidatorNum)).
		AddInt64(p.RegisterFee).
		AddInt64(int64(p.RegisterTimeOut)).
		AddInt64(int64(OnlyRegisterSelf)).
		AddData([]byte(p.HoldingAssetName.String())).
		AddData([]byte(p.HoldingAssetThreshold)).
		AddData([]byte(p.AirDropRatio)).
		AddData([]byte(p.AirDropLimit)).
		AddInt64(int64(p.AirDropTimeOut)).
		AddInt64(int64(p.ReferralRatio)).
		Script()
}

func (p *DaoContract) Decode(data []byte) error {
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
		return fmt.Errorf("missing AssetAmt")
	}
	p.AssetAmt = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing SatValue")
	}
	p.SatValue = tokenizer.ExtractInt64()

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing ValidatorNum")
	}
	p.ValidatorNum = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing RegisterFee")
	}
	p.RegisterFee = tokenizer.ExtractInt64()

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing RegisterTimeOut")
	}
	p.RegisterTimeOut = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing OnlyRegisterSelf")
	}
	OnlyRegisterSelf := (tokenizer.ExtractInt64())
	if OnlyRegisterSelf > 0 {
		p.OnlyRegisterSelf = true
	} else {
		p.OnlyRegisterSelf = false
	}

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing HoldingAssetName")
	}
	p.HoldingAssetName = *indexer.NewAssetNameFromString(string(tokenizer.Data()))

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing HoldingAssetAmt")
	}
	p.HoldingAssetThreshold = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing AirDropRatio")
	}
	p.AirDropRatio = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing AirDropLimit")
	}
	p.AirDropLimit = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing AirDropTimeOut")
	}
	p.AirDropTimeOut = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing ReferralRatio")
	}
	p.ReferralRatio = int(tokenizer.ExtractInt64())

	return nil
}

func (p *DaoContract) InvokeParam(action string) string {
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

// 2. 定义合约调用的数据结构

// InvokeParam
type RegisterInvokeParam struct {
	UID         string `json:"uid"`
	ReferrerUID string `json:"referrerUid"`
}

func (p *RegisterInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(p.UID)).
		AddData([]byte(p.ReferrerUID)).Script()
}

func (p *RegisterInvokeParam) EncodeV2() ([]byte, error) {
	return p.Encode()
}

func (p *RegisterInvokeParam) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing uid")
	}
	p.UID = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing ReferrerUID")
	}
	p.ReferrerUID = string(tokenizer.Data())

	return nil
}

func (p *RegisterInvokeParam) Check() error {
	if p.UID == "" {
		return fmt.Errorf("invalid uid")
	}
	return nil
}

// 推荐人绑定被推荐人
type ReferralParam struct {
	UID     string `json:"uid"`
	Address string `json:"address"`
}
type BindInvokeParam struct {
	Items   []*ReferralParam `json:"items"`
}

func (p *BindInvokeParam) Encode() ([]byte, error) {
	builder := txscript.NewScriptBuilder()
	for _, item := range p.Items {
		builder = builder.AddData([]byte(item.UID)).AddData([]byte(item.Address))
	}
	return builder.Script()
}

func (p *BindInvokeParam) EncodeV2() ([]byte, error) {
	return p.Encode()
}

func (p *BindInvokeParam) Decode(data []byte) error {

	tokenizer := txscript.MakeScriptTokenizer(0, data)
	for tokenizer.Next() && tokenizer.Err() == nil {
		var item ReferralParam
		item.UID = string(tokenizer.Data())
		if !tokenizer.Next() || tokenizer.Err() != nil {
			return fmt.Errorf("missing address")
		}
		item.Address = string(tokenizer.Data())
		p.Items = append(p.Items, &item)
	}

	return nil
}

func (p *BindInvokeParam) Check() error {
	if len(p.Items) == 0 {
		return fmt.Errorf("empty items")
	}
	for _, item := range p.Items {
		if len(item.UID) == 0 {
			return fmt.Errorf("invalid uid %v", item.UID)
		}
		if len(item.Address) == 0 || !IsBtcAddress(item.Address) {
			return fmt.Errorf("invalid address")
		}
	}

	return nil
}

type AddressResult struct {
	Address string
	Result int
}

func (p *BindInvokeParam) ToPaded() ([]byte, error) {
	result := make(map[string]*AddressResult)
	for _, item := range p.Items {
		result[item.UID] = &AddressResult{
			Address: item.Address,
			Result: RESULT_UNHANDLED, // unhandle
		}
	}
	return EncodeToBytes(result)
}

// //
type DonateInvokeParam struct {
	AssetName string `json:"assetName"` // 资产名字
	Amt       string `json:"amt"`       // 资产数量
	Value     int64  `json:"value"`     // 聪数量
}

func (p *DonateInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(p.AssetName)).
		AddData([]byte(p.Amt)).
		AddInt64(p.Value).
		Script()
}

func (p *DonateInvokeParam) EncodeV2() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte("")).
		AddData([]byte(p.Amt)).
		AddInt64(p.Value).
		Script()
}

func (p *DonateInvokeParam) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing asset name")
	}
	p.AssetName = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing amt")
	}
	p.Amt = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing value")
	}
	p.Value = (tokenizer.ExtractInt64())

	return nil
}

func (p *DonateInvokeParam) Check() error {
	name := indexer.NewAssetNameFromString(p.AssetName) 
	if p.AssetName != name.String() {
		return fmt.Errorf("invalid asset name %s", p.AssetName)
	}

	_, err := indexer.NewDecimalFromString(p.Amt, MAX_ASSET_DIVISIBILITY)
	if err != nil {
		return fmt.Errorf("invalid amt %s", p.Amt)
	}

	if p.Value < 0 {
		return fmt.Errorf("invalid value %d", p.Value)
	}
	return nil
}

// //
type AirDropInvokeParam struct {
	UIDs []string `json:"uids"` // uid
}

func (p *AirDropInvokeParam) Encode() ([]byte, error) {
	builder := txscript.NewScriptBuilder()
	for _, uid := range p.UIDs {
		builder = builder.AddData([]byte(uid))
	}
	return builder.Script()
}

func (p *AirDropInvokeParam) EncodeV2() ([]byte, error) {
	return p.Encode()
}

func (p *AirDropInvokeParam) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)
	for tokenizer.Next() && tokenizer.Err() == nil {
		p.UIDs = append(p.UIDs, string(tokenizer.Data()))
	}
	return nil
}

func (p *AirDropInvokeParam) Check() error {
	if len(p.UIDs) == 0 {
		return fmt.Errorf("invalid UIDs")
	}
	return nil
}


func (p *AirDropInvokeParam) ToPaded() ([]byte, error) {
	result := make(map[string]string)
	for _, uid := range p.UIDs {
		result[uid] = ""
	}
	return EncodeToBytes(result)
}


func (p *AirDropInvokeParam) GetUIDs() []string {
	// uids := make([]string, 0, len(p.UIDs))
	// for _, uid := range p.UIDs {
	// 	u, _ := retrieveFromInvokeUID(uid)
	// 	uids = append(uids, u)
	// }
	// return uids
	return p.UIDs
}

// func retrieveFromInvokeUID(uid string) (string, string) {
// 	parts := strings.Split(uid, ":")
// 	switch len(parts) {
// 	case 1:
// 		return parts[0], ""
// 	case 2:
// 		return parts[0], parts[1]
// 	}
// 	return uid, ""
// }

type ValidateInvokeParam struct {
	OrderType int    `json:"orderType"` // ORDERTYPE_REGISTER or ORDERTYPE_AIRDROP
	Result    int    `json:"result"`    // 0 成功；其他，失败
	Reason    string `json:"reason"`
	Param     []byte `json:"para"` // 以空格隔开的id列表，id是uid
}

func (p *ValidateInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
		AddInt64(int64(p.Result)).
		AddData([]byte(p.Reason)).
		AddData([]byte(p.Param)).
		Script()
}

func (p *ValidateInvokeParam) EncodeV2() ([]byte, error) {
	return p.Encode()
}

func (p *ValidateInvokeParam) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing order type")
	}
	p.OrderType = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing result")
	}
	p.Result = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing amt")
	}
	p.Reason = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing amt")
	}
	p.Param = (tokenizer.Data())

	return nil
}

func (p *ValidateInvokeParam) Check() error {
	if len(p.Param) == 0 {
		return fmt.Errorf("invalid parameter")
	}
	// uids := strings.Split(string(p.Param), " ")
	switch p.OrderType {
	case ORDERTYPE_REGISTER:
		// uid list

	case ORDERTYPE_AIRDROP:
		// uid list

	default:
		return fmt.Errorf("invalid order type %d", p.OrderType)
	}
	return nil
}


// 3. 定义合约交互者的数据结构

type DaoInvokerStatus struct {
	InvokerStatusBaseV2

	ReferrerUID     string   // 推荐人UID
	UID             string   // 自身UID
	TotalAirdropAmt *Decimal // 作为推荐人得到的空投
	Airdropped      bool     // 作为被推荐人，已经让推荐人得到了空投
	AirdroppedAmt   *Decimal // 作为被推荐人，已经让推荐人得到了空投的数量
	ReferralUIDs    []string // 所有有效推荐的UID
}

func NewDaoInvokerStatus(address string, divisibility int) *DaoInvokerStatus {
	return &DaoInvokerStatus{
		InvokerStatusBaseV2: *NewInvokerStatusBaseV2(address, divisibility),
	}
}

func (p *DaoInvokerStatus) GetVersion() int {
	return p.Version
}

func (p *DaoInvokerStatus) GetKey() string {
	return p.Address
}

func (p *DaoInvokerStatus) GetInvokeCount() int {
	return p.InvokeCount
}

func (p *DaoInvokerStatus) GetHistory() map[int][]int64 {
	return p.History
}

// 非数据记录
type DaoInvokerStatistic struct {
	ReferrerUID   string
	UID           string
	InvokeCount   int
	DonateAmt     string
	DonateValue   int64
	AirdropAmt    string
	ReferralCount int
}

type DaoContractRunningData_old = DaoContractRunningData

// 4. 定义合约运行时需要维护的数据
type DaoContractRunningData struct {
	AssetAmtInPool  *Decimal
	SatsValueInPool int64 // 池子中聪的数量

	TotalDonateCount  int
	TotalDonateAmt    *Decimal // 所有进入池子的资产数量，指主网上每个地址参与回收的每个TX的输入资产总量
	TotalInputValue   int64
	TotalAirdropCount int                 // 空投交易计数
	TotalAirdropAmt   *Decimal            // 所有空投出去的资产数量
	TotalFeeValue     int64               // 所有由合约支付的相关交易的网络费用
	Validators        map[string]*Decimal // address
}

func (p *DaoContractRunningData) ToNewVersion() *DaoContractRunningData {
	return p
}

// 5. 定义合约保存到数据库中的数据
type DaoContractRunTimeInDB struct {
	DaoContract
	ContractRuntimeBase

	// 运行过程的状态
	DaoContractRunningData
}

type UnhandledBindInfo struct {
	ItemId		int64
	UID 		string
	Address     string
	ReferrerUID  string
}

// 6. 合约运行时状态
type DaoContractRunTime struct {
	DaoContractRunTimeInDB

	invokerMap  map[string]*DaoInvokerStatus // key: address
	registerMap map[string]map[int64]*InvokeItem
	bindMap     map[string]map[int64]*InvokeItem
	donateMap   map[string]map[int64]*InvokeItem // 还在处理中的调用, address -> invoke item list,
	airdropMap  map[string]map[int64]*InvokeItem
	validateMap map[string]map[int64]*InvokeItem

	uidMap          map[string]string   // uid->address 所有有效的
	unhandledUidMap map[string]*UnhandledBindInfo  // uid 还在处理中的
	donateRanking   map[string]*Decimal // 前100名
	airdropRanking  map[string]*Decimal // 前100名

	airdropThreshold *Decimal
	airdropRatio     *Decimal
	airdropLimit     *Decimal

	responseCache      []*responseItem_dao // 所有注册成功的invoker的简单状态
	responseStatus     Response_DaoContract // 合约状态
	responseInvokerMap map[string]*Response_DaoInvokerStatus // 
	responseAnalytics  *analytcisData_dao // 两个排行榜
}

func NewDaoContractRunTime(stp ContractManager) *DaoContractRunTime {
	p := &DaoContractRunTime{
		DaoContractRunTimeInDB: DaoContractRunTimeInDB{
			DaoContract:         *NewDaoContract(),
			ContractRuntimeBase: *NewContractRuntimeBase(stp),
			DaoContractRunningData: DaoContractRunningData{
				Validators: make(map[string]*Decimal),
			},
		},
	}
	p.init()

	return p
}

func (p *DaoContractRunTime) init() {
	p.contract = p
	p.runtime = p
	p.invokerMap = make(map[string]*DaoInvokerStatus)
	p.registerMap = make(map[string]map[int64]*InvokeItem)
	p.bindMap = make(map[string]map[int64]*InvokeItem)
	p.airdropMap = make(map[string]map[int64]*InvokeItem)
	p.donateMap = make(map[string]map[int64]*InvokeItem)
	p.validateMap = make(map[string]map[int64]*InvokeItem)

	p.uidMap = make(map[string]string)
	p.unhandledUidMap = make(map[string]*UnhandledBindInfo)
	p.donateRanking = make(map[string]*Decimal)
	p.airdropRanking = make(map[string]*Decimal)
	p.responseInvokerMap = make(map[string]*Response_DaoInvokerStatus)

	p.airdropRatio, _ = indexer.NewDecimalFromString(p.AirDropRatio, p.Divisibility)
	p.airdropLimit, _ = indexer.NewDecimalFromString(p.AirDropLimit, p.Divisibility)
	p.airdropThreshold, _ = indexer.NewDecimalFromString(p.HoldingAssetThreshold, p.Divisibility)
}

func (p *DaoContractRunTime) InitFromJson(content []byte, stp ContractManager) error {
	err := json.Unmarshal(content, p)
	if err != nil {
		return err
	}
	p.init()

	return nil
}

func (p *DaoContractRunTime) InitFromContent(content []byte, stp ContractManager, resv ContractDeployResvIF) error {

	err := p.ContractRuntimeBase.InitFromContent(content, stp, resv)
	if err != nil {
		Log.Errorf("ContractRuntimeBase.InitFromContent failed, %v", err)
		return err
	}
	p.init()
	return nil
}

func (p *DaoContractRunTime) InitFromDB(stp ContractManager, resv ContractDeployResvIF) error {

	err := p.ContractRuntimeBase.InitFromDB(stp, resv)
	if err != nil {
		Log.Errorf("ContractRuntimeBase.InitFromDB failed, %v", err)
		return err
	}
	p.init()

	url := p.URL()

	invokers := loadAllContractInvokerStatus(p.db, url)
	invokerVector := make([]*DaoInvokerStatus, 0, len(invokers))
	for _, v := range invokers {
		invoker, ok := v.(*DaoInvokerStatus)
		if !ok {
			continue
		}
		if invoker.UID != "" {
			p.uidMap[invoker.UID] = invoker.Address
		}
		invokerVector = append(invokerVector, invoker)
	}
	sort.Slice(invokerVector, func(i, j int) bool {
		r := invokerVector[i].InvokeAmt.Cmp(invokerVector[j].InvokeAmt)
		if r == 0 {
			return invokerVector[i].GetOldestItemId() < invokerVector[j].GetOldestItemId()
		}
		return r > 0
	})
	for i := 0; i < RANKING_MAX_SIZE && i < len(invokerVector); i++ {
		invoker := invokerVector[i]
		if invoker.InvokeAmt.Sign() == 0 {
			continue
		}
		p.donateRanking[invoker.Address] = invoker.InvokeAmt.Clone()
	}
	sort.Slice(invokerVector, func(i, j int) bool {
		r := invokerVector[i].TotalAirdropAmt.Cmp(invokerVector[j].TotalAirdropAmt)
		if r == 0 {
			return invokerVector[i].GetOldestItemId() < invokerVector[j].GetOldestItemId()
		}
		return r > 0
	})
	for i := 0; i < RANKING_MAX_SIZE && i < len(invokerVector); i++ {
		invoker := invokerVector[i]
		if invoker.TotalAirdropAmt.Sign() == 0 {
			continue
		}
		p.airdropRanking[invoker.Address] = invoker.TotalAirdropAmt.Clone()
	}

	// 在 uidMap 后
	history := LoadContractInvokeHistory(p.db, url, true, false)
	for _, v := range history {
		item, ok := v.(*InvokeItem)
		if !ok {
			continue
		}

		p.loadInvokerInfo(item.Address)
		p.addItem(item)
		p.history[item.InUtxo] = item
	}

	// fix
	// if url == "tb1qfwx8fyajtk9yrefdru5tkz7k2q0xxs0mwv3gu4teya75awcsfj8qfyaupt_brc20:f:ordi_dao.tc" {
	// 	p.RegisterTimeOut = 10
	// 	p.AirDropTimeOut = 10
	// 	p.stp.SaveReservation(p.resv)
	// }

	return nil
}

func (p *DaoContractRunTime) IsIdle() bool {
	return len(p.donateMap) == 0 && len(p.airdropMap) == 0
}

func (p *DaoContractRunTime) IsActive() bool {
	return p.ContractRuntimeBase.IsActive() &&
		p.CurrBlockL1 >= p.EnableBlockL1
}

// 只计算在 calcAssetMerkleRoot 之前已经确定的数据，其他在广播TX之后才修改的数据暂时不要管，不然容易导致数据不一致
func CalcDaoContractRunningDataMerkleRoot(r *DaoContractRunningData) []byte {
	var buf []byte

	buf2 := fmt.Sprintf("%s %d ", r.AssetAmtInPool.String(), r.SatsValueInPool)
	buf = append(buf, buf2...)

	buf2 = fmt.Sprintf("%d %s %d", r.TotalDonateCount, r.TotalDonateAmt.String(), r.TotalInputValue)
	buf = append(buf, buf2...)

	buf2 = fmt.Sprintf("%d %s %d %d", r.TotalAirdropCount, r.TotalAirdropAmt.String(), r.TotalInputValue, r.TotalFeeValue)
	buf = append(buf, buf2...)

	Log.Debugf("DaoContractRunningData: %s", string(buf))

	hash := chainhash.DoubleHashH(buf)
	result := hash.CloneBytes()
	Log.Debugf("hash: %s", hex.EncodeToString(result))
	return result
}

// 调用前自己加锁
func (p *DaoContractRunTime) CalcRuntimeMerkleRoot() []byte {
	//Log.Debugf("Invoke: %d", p.InvokeCount)
	base := CalcContractRuntimeBaseMerkleRoot(&p.ContractRuntimeBase)
	running := CalcDaoContractRunningDataMerkleRoot(&p.DaoContractRunningData)

	buf := append(base, running...)
	hash := chainhash.DoubleHashH(buf)
	Log.Debugf("%s CalcRuntimeMerkleRoot: %d %s", p.stp.GetMode(), p.InvokeCount, hex.EncodeToString(hash.CloneBytes()))
	return hash.CloneBytes()
}

func (p *DaoContractRunTime) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)

	if err := enc.Encode(p.DaoContract); err != nil {
		return nil, err
	}

	if err := enc.Encode(p.ContractRuntimeBase); err != nil {
		return nil, err
	}

	if err := enc.Encode(p.DaoContractRunningData); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (p *DaoContractRunTime) GobDecode(data []byte) error {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)

	var recycle DaoContract
	if err := dec.Decode(&recycle); err != nil {
		return err
	}
	p.DaoContract = recycle

	if ContractRuntimeBaseUpgrade {
		var old ContractRuntimeBase_old
		if err := dec.Decode(&old); err != nil {
			return err
		}
		p.ContractRuntimeBase = *old.ToNewVersion()

		var old2 DaoContractRunningData_old
		if err := dec.Decode(&old2); err != nil {
			return err
		}
		p.DaoContractRunningData = *old2.ToNewVersion()
	} else {
		if err := dec.Decode(&p.ContractRuntimeBase); err != nil {
			return err
		}

		if err := dec.Decode(&p.DaoContractRunningData); err != nil {
			return err
		}
	}

	return nil
}

func (p *DaoContractRunTime) GetAssetAmount() (*Decimal, int64) {
	return p.AssetAmtInPool, p.SatsValueInPool
}

// 7. rpc接口和相关数据结构定义

type invokeItem_register struct {
	Id          int64
	InUtxo      string
	Address     string
	UID         string
	ReferrerUID string
}

type invokeItem_airdrop struct {
	Id           int64
	InUtxo       string
	Address      string
	UID          string
	ReferralUIDs []string
}

func (p *DaoContractRunTime) updateResponseData() {
	if p.refreshTime == 0 {
		p.mutex.Lock()
		defer p.mutex.Unlock()

		/////////////////////////
		// responseCache
		p.responseCache = make([]*responseItem_dao, 0, len(p.uidMap))
		for _, address := range p.uidMap {
			v := p.loadInvokerInfo(address)
			item := &responseItem_dao{
				Address:       v.Address,
				UID:           v.UID,
				ReferrerUID:   v.ReferrerUID,
				ReferralCount: len(v.ReferralUIDs),
				DonateAmt:     v.InvokeAmt.String(),
				AirdropAmt:    v.TotalAirdropAmt.String(),
			}
			p.responseCache = append(p.responseCache, item)
		}
		sort.Slice(p.responseCache, func(i, j int) bool {
			return p.responseCache[i].UID < p.responseCache[j].UID
		})

		/////////////////////////
		// responseStatus
		p.responseStatus.DaoContractRunTimeInDB = &p.DaoContractRunTimeInDB
		p.responseStatus.UIDCount = len(p.uidMap)

		p.responseStatus.RegisterList = make([]string, 0)
		for _, registers := range p.registerMap {
			for _, v := range registers {
				if v.Finished() {
					continue
				}
				item := invokeItem_register{
					Id:      v.Id,
					InUtxo:  v.InUtxo,
					Address: v.Address,
				}
				paramBytes, err := base64.StdEncoding.DecodeString(string(v.Padded))
				if err != nil {
					continue
				}
				var innerParam RegisterInvokeParam
				err = innerParam.Decode(paramBytes)
				if err != nil {
					continue
				}
				item.UID = innerParam.UID
				item.ReferrerUID = innerParam.ReferrerUID

				buf, err := json.Marshal(item)
				if err != nil {
					continue
				}
				p.responseStatus.RegisterList = append(p.responseStatus.RegisterList, string(buf))
			}
		}
		for _, binds := range p.bindMap {
			for _, v := range binds {
				if v.Finished() {
					continue
				}
				var pad map[string]*AddressResult
				err := DecodeFromBytes(v.Padded, &pad)
				if err != nil {
					Log.Errorf("DecodeFromBytes Padded failed, %v", err)
					continue
				}

				referrer := p.loadInvokerInfo(v.Address)
				for uid, r := range pad {
					if r.Result != RESULT_UNHANDLED {
						continue
					}
					item := invokeItem_register{
						Id:      v.Id,
						InUtxo:  v.InUtxo,
						Address: r.Address,
						UID:     uid,
						ReferrerUID: referrer.UID,
					}
					buf, err := json.Marshal(item)
					if err != nil {
						continue
					}
					p.responseStatus.RegisterList = append(p.responseStatus.RegisterList, string(buf))
				}
			}
		}

		p.responseStatus.AirdropList = make([]string, 0)
		for addr, airdrops := range p.airdropMap {
			invoker := p.loadInvokerInfo(addr)
			for _, v := range airdrops {
				if v.Finished() {
					continue
				}
				var pad map[string]string
				err := DecodeFromBytes(v.Padded, &pad)
				if err != nil {
					Log.Errorf("DecodeFromBytes Padded failed, %v", err)
					continue
				}

				item := invokeItem_airdrop{
					Id:      v.Id,
					InUtxo:  v.InUtxo,
					Address: v.Address,
					UID:     invoker.UID,
				}
				for uid, result := range pad {
					if result == "" {
						item.ReferralUIDs = append(item.ReferralUIDs, uid)
					}
				}

				buf, err := json.Marshal(item)
				if err != nil {
					continue
				}
				p.responseStatus.AirdropList = append(p.responseStatus.AirdropList, string(buf))
			}
		}

		/////////////////////////
		// responseInvokerMap
		p.responseInvokerMap = make(map[string]*Response_DaoInvokerStatus)

		/////////////////////////
		// responseAnalytics
		p.responseAnalytics = &analytcisData_dao{
			AssetsName: &p.AssetName,
		}
		donate := make([]*analytcisItem_dao, 0, len(p.donateRanking))
		for addr, amt := range p.donateRanking {
			invoker := p.loadInvokerInfo(addr)
			donate = append(donate, &analytcisItem_dao{
				UID:           invoker.UID,
				Address:       addr,
				Amount:        amt,
				ReferralCount: len(invoker.ReferralUIDs),
			})
		}
		sort.Slice(donate, func(i, j int) bool {
			r := donate[i].Amount.Cmp(donate[j].Amount)
			if r == 0 {
				return donate[i].ReferralCount > donate[j].ReferralCount
			}
			return r > 0
		})
		p.responseAnalytics.ItemsDonate = donate

		airdrop := make([]*analytcisItem_dao, 0, len(p.airdropRanking))
		for addr, amt := range p.airdropRanking {
			invoker := p.loadInvokerInfo(addr)
			airdrop = append(airdrop, &analytcisItem_dao{
				UID:           invoker.UID,
				Address:       addr,
				Amount:        amt,
				ReferralCount: len(invoker.ReferralUIDs),
			})
		}
		sort.Slice(airdrop, func(i, j int) bool {
			r := airdrop[i].Amount.Cmp(airdrop[j].Amount)
			if r == 0 {
				return airdrop[i].ReferralCount > airdrop[j].ReferralCount
			}
			return r > 0
		})
		p.responseAnalytics.ItemsAirdrop = airdrop

		p.refreshTime = time.Now().Unix()
	}
}

func (p *DaoContractRunTime) RuntimeStatus() string {

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

type analytcisItem_dao struct {
	UID           string   `json:"uid"`
	Address       string   `json:"address"`
	Amount        *Decimal `json:"amt"`                     // 空投或者捐赠的资产数量
	ReferralCount int      `json:"referralCount,omitempty"` // 被推荐人数量（作为空投参数时），其他设置为0
}

type analytcisData_dao struct {
	AssetsName   *indexer.AssetName   `json:"assets_name"`
	ItemsDonate  []*analytcisItem_dao `json:"items_donate"`
	ItemsAirdrop []*analytcisItem_dao `json:"items_airdrop"`
}

func (p *DaoContractRunTime) RuntimeAnalytics() string {
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


type ValidateItem struct {
	ID         int64  `json:"id"`         // history id
	Address    string `json:"address"`    // invoker
	Result     int    `json:"result"`
	Reason     string `json:"reason"`
}

type ValidateResult struct {
	Type int `json:"type"` // ORDERTYPE_REGISTER or ORDERTYPE_AIRDROP 
	UIDs string `json:"uids,omitempty"` 
	Reason string `json:"reason,omitempty"` 
}

type BindResult struct {
	UIDs string `json:"uids,omitempty"` 
	Reason string `json:"reason,omitempty"` 
}

type DaoHistoryItem struct {
	*InvokeItem

	// 将pad数据展开
	*AirdropResult  `json:"airdrop,omitempty"`
	*ValidateResult `json:"validate,omitempty"`
	BindResult *AirdropResult     `json:"bind,omitempty"`
}

type response_history_dao struct {
	Total int                `json:"total"`
	Start int                `json:"start"`
	Data  []*DaoHistoryItem  `json:"data"`
}

func (p *DaoContractRunTime) InvokeHistory(f any, start, limit int) string {
	//p.updateResponseData()

	//return p.GetRuntimeBase().InvokeHistory(f, start, limit)

	defaultRsp := `{"total":0,"start":0,"data":[]}`
	var buf []byte
	var err error
	orig := p.invokeHistory(f, start, limit)

	p.mutex.RLock()
	defer p.mutex.RUnlock()
	result := response_history_dao{
		Total: orig.Total,
		Start: orig.Start,
	}
	
	for _, item := range orig.Data {
		n := &DaoHistoryItem{
			InvokeItem: item.Clone(),
		}

		// 历史数据可能padded中的数据格式不对，直接忽略
		switch n.OrderType {
		case ORDERTYPE_BIND:
			bind := &AirdropResult{}
			var pad map[string]*AddressResult
			err := DecodeFromBytes(item.Padded, &pad)
			if err == nil {
				for k, v := range pad {
					var result string 
					switch v.Result {
					case RESULT_OK:
						result = "validated"
					case RESULT_INVALID:
						result = "invalid"
					case RESULT_REJECTED:
						result = "rejected"
					case RESULT_UNHANDLED:
						result = "validating"
					}
					bind.Items = append(bind.Items, &AirdropItem{
						UID: k,
						Address: v.Address,
						Result: result,
					})
				}
				n.BindResult = bind
			}

		case ORDERTYPE_AIRDROP:
			airdrop := &AirdropResult{}
			var pad map[string]string  // uid->result
			err := DecodeFromBytes(item.Padded, &pad)
			if err == nil {
				for k, v := range pad {
					airdrop.Items = append(airdrop.Items, &AirdropItem{
						UID: k,
						Address: p.uidMap[k],
						Result: v,
					})
				}
				n.AirdropResult = airdrop
			}

		case ORDERTYPE_VALIDATE:
			validate := &ValidateResult{}
			var innerParam ValidateInvokeParam
			paramBytes, err := base64.StdEncoding.DecodeString(string(n.Padded))
			if err == nil {
				err = innerParam.Decode(paramBytes)
				if err == nil {
					validate.Type = innerParam.OrderType
					validate.UIDs = string(innerParam.Param)
					if innerParam.Result == 0 {
						validate.Reason = "validated"
					} else {
						validate.Reason = innerParam.Reason
					}
					n.ValidateResult = validate
				}
			}
		}
		n.Padded = nil
		result.Data = append(result.Data, n)
	}

	buf, err = json.Marshal(result)
	if err != nil {
		Log.Errorf("Marshal responseHistory failed, %v", err)
		return defaultRsp
	}
	
	return string(buf)
}

type responseItem_dao struct {
	Address       string `json:"address"`
	UID           string `json:"uid"`
	ReferrerUID   string `json:"referer"`
	ReferralCount int    `json:"referralCount"`
	DonateAmt     string `json:"donate"`
	AirdropAmt    string `json:"airdrop"`
}

type Response_DaoContract struct {
	*DaoContractRunTimeInDB

	// 增加更多参数
	UIDCount     int      `json:"uidCount"`
	RegisterList []string `json:"registerList"` // include bind
	AirdropList  []string `json:"airdropList"`
}

func (p *DaoContractRunTime) AllAddressInfo(start, limit int) string {

	p.updateResponseData()

	p.mutex.RLock()
	defer p.mutex.RUnlock()

	type response struct {
		Total int                 `json:"total"`
		Start int                 `json:"start"`
		Data  []*responseItem_dao `json:"data"`
	}

	result := &response{
		Total: len(p.responseCache),
		Start: start,
	}
	if start < 0 || start >= len(p.responseCache) {
		return ""
	}
	if limit <= 0 {
		limit = 100
	}
	end := start + limit
	if end > len(p.responseCache) {
		end = len(p.responseCache)
	}
	result.Data = p.responseCache[start:end]

	buf, err := json.Marshal(result)
	if err != nil {
		Log.Errorf("Marshal SwapContractRuntime failed, %v", err)
		return ""
	}
	return string(buf)
}

type ReferralInfo struct {
	UID      string `json:"uid"`
	AssetAmt string `json:"amount"`
}

type Response_DaoInvokerStatus struct {
	Statistic    *DaoInvokerStatistic `json:"status"`
	AirdropList  []string             `json:"airdrops"`
	ReferralList []*ReferralInfo      `json:"referrals"`
}

func (p *DaoContractRunTime) StatusByAddress(address string) (string, error) {

	p.updateResponseData()

	p.mutex.Lock()
	defer p.mutex.Unlock()

	result, ok := p.responseInvokerMap[address]
	if !ok {
		result = &Response_DaoInvokerStatus{}
		invoker := p.loadInvokerInfo(address)
		if invoker != nil {
			result.Statistic = &DaoInvokerStatistic{
				ReferrerUID:   invoker.ReferrerUID,
				UID:           invoker.UID,
				InvokeCount:   invoker.GetInvokeCount(),
				DonateAmt:     invoker.GetInvokeAmt().String(),
				DonateValue:   invoker.GetInvokeValue(),
				AirdropAmt:    invoker.TotalAirdropAmt.String(),
				ReferralCount: len(invoker.ReferralUIDs),
			}

			airdrops := p.airdropMap[address]
			for _, v := range airdrops {
				result.AirdropList = append(result.AirdropList, v.InUtxo)
			}

			// 只返回还没有获取到空投的uid
			for _, v := range invoker.ReferralUIDs {
				// 过滤已经在等待审核的空投uid
				_, ok := p.unhandledUidMap[v]
				if ok {
					continue
				}

				_, amt, ok := p.checkAirdropFlag(invoker.UID, v)
				if !ok {
					continue
				}
				airdrop := p.calcAirdropAmt(amt)

				result.ReferralList = append(result.ReferralList, &ReferralInfo{
					UID:      v,
					AssetAmt: airdrop.String(),
				})
			}
		}
		p.responseInvokerMap[address] = result
	}

	buf, err := json.Marshal(result)
	if err != nil {
		Log.Errorf("Marshal trader status failed, %v", err)
		return "", err
	}

	return string(buf), nil
}

func (p *DaoContractRunTime) GetInvokerStatus(address string) InvokerStatus {
	return p.loadInvokerInfo(address)
}

func (p *DaoContractRunTime) loadInvokerInfo(address string) *DaoInvokerStatus {
	status, ok := p.invokerMap[address]
	if ok {
		return status
	}

	r, err := loadContractInvokerStatus(p.stp.GetDB(), p.URL(), address)
	if err != nil {
		status = NewDaoInvokerStatus(address, p.Divisibility)
	} else {
		status, ok = r.(*DaoInvokerStatus)
		if !ok {
			status = NewDaoInvokerStatus(address, p.Divisibility)
		}
	}

	p.invokerMap[address] = status
	return status
}

func (p *DaoContractRunTime) DeploySelf() bool {
	return false
}

func (p *DaoContractRunTime) AllowDeploy() error {

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

// 支持非实时对象调用
// return fee: 调用费用+该invoke需要的聪数量
func (p *DaoContractRunTime) CheckInvokeParam(param string) (int64, error) {
	var invoke InvokeParam
	err := json.Unmarshal([]byte(param), &invoke)
	if err != nil {
		return 0, err
	}
	assetName := p.GetAssetName()
	templateName := p.GetTemplateName()
	if templateName != TEMPLATE_CONTRACT_DAO {
		return 0, fmt.Errorf("unsupport")
	}

	switch invoke.Action {
	case INVOKE_API_REGISTER:
		var innerParam RegisterInvokeParam
		err := json.Unmarshal([]byte(invoke.Param), &innerParam)
		if err != nil {
			return 0, err
		}
		err = innerParam.Check()
		if err != nil {
			return 0, err
		}

		return p.RegisterFee, nil

	case INVOKE_API_DONATE:
		var innerParam DonateInvokeParam
		err := json.Unmarshal([]byte(invoke.Param), &innerParam)
		if err != nil {
			return 0, err
		}
		if innerParam.AssetName != assetName.String() {
			return 0, fmt.Errorf("invalid asset name %s", innerParam.AssetName)
		}

		err = innerParam.Check()
		if err != nil {
			return 0, err
		}

		return INVOKE_FEE, nil

	case INVOKE_API_AIRDROP:
		var innerParam AirDropInvokeParam
		err := json.Unmarshal([]byte(invoke.Param), &innerParam)
		if err != nil {
			return 0, err
		}

		err = innerParam.Check()
		if err != nil {
			return 0, err
		}

		return MIN_AIRDROP_FEE, nil

	case INVOKE_API_VALIDATE:
		var innerParam ValidateInvokeParam
		err := json.Unmarshal([]byte(invoke.Param), &innerParam)
		if err != nil {
			return 0, err
		}

		err = innerParam.Check()
		if err != nil {
			return 0, err
		}

		return INVOKE_FEE, nil

	case INVOKE_API_BIND:
		var innerParam BindInvokeParam
		err := json.Unmarshal([]byte(invoke.Param), &innerParam)
		if err != nil {
			return 0, err
		}

		err = innerParam.Check()
		if err != nil {
			return 0, err
		}

		return INVOKE_FEE, nil

	default:
		return 0, fmt.Errorf("unsupport action %s", invoke.Action)
	}
}

// 当作donate
func (p *DaoContractRunTime) AllowInvokeWithNoParam() bool {
	return true
}

// 当作donate
func (p *DaoContractRunTime) AllowInvokeWithNoParam_SatsNet() bool {
	return true
}

func (p *DaoContractRunTime) InvokeWithBlock_SatsNet(data *InvokeDataInBlock_SatsNet) error {

	err := p.ContractRuntimeBase.InvokeWithBlock_SatsNet(data)
	if err != nil {
		return err
	}

	if p.IsActive() {
		p.mutex.Lock()
		p.PreprocessInvokeData_SatsNet(data)
		p.process(data.Height, data.BlockHash)
		p.InvokeCompleted_SatsNet(data)
		p.mutex.Unlock()

		p.sendInvokeResultTx()
	} else {
		p.mutex.Lock()
		p.InvokeCompleted_SatsNet(data)
		p.mutex.Unlock()
	}

	return nil
}

func (p *DaoContractRunTime) InvokeWithBlock(data *InvokeDataInBlock) error {

	err := p.ContractRuntimeBase.InvokeWithBlock(data)
	if err != nil {
		return err
	}

	if p.IsActive() {
		p.mutex.Lock()
		p.PreprocessInvokeData(data)
		// p.process(data.Height, data.BlockHash) 只处理聪网
		p.InvokeCompleted(data)
		p.mutex.Unlock()

		p.sendInvokeResultTx()
	} else {
		p.mutex.Lock()
		p.InvokeCompleted(data)
		p.mutex.Unlock()
	}

	return nil
}

func (p *DaoContractRunTime) VerifyAndAcceptInvokeItem_SatsNet(invokeTx *InvokeTx_SatsNet, height int) (InvokeHistoryItem, error) {

	invokeData := invokeTx.InvokeParam
	output := invokeTx.TxOutput
	address := invokeTx.Invoker

	var param InvokeParam
	if invokeData != nil && invokeData.InvokeParam != nil {
		err := param.Decode(invokeData.InvokeParam)
		if err != nil {
			return nil, err
		}
	} else { // TODO 主网过来的调用都没有设置参数，跟AMM/transend的符文有冲突，一个地址不能同时部署两个recycle和amm/transcend合约
		param.Action = INVOKE_API_DONATE
	}

	utxoId := output.UtxoId
	utxo := output.OutPointStr
	org, ok := p.history[utxo]
	if ok {
		if org.UtxoId != utxoId { // reorg
			org.UtxoId = utxoId
			SaveContractInvokeHistoryItem(p.db, p.URL(), org)
		}
		invokeTx.Handled = true
		return nil, fmt.Errorf("contract utxo %s exists", utxo)
	}

	url := p.URL()
	switch param.Action {
	case INVOKE_API_REGISTER:
		if param.Param == "" {
			return nil, fmt.Errorf("invalid parameter")
		}
		paramBytes, err := base64.StdEncoding.DecodeString(param.Param)
		if err != nil {
			return nil, err
		}
		var innerParam RegisterInvokeParam
		err = innerParam.Decode(paramBytes)
		if err != nil {
			return nil, err
		}
		err = innerParam.Check()
		if err != nil {
			return nil, err
		}

		invokeTx.Handled = true
		return p.updateContract(ORDERTYPE_REGISTER, []byte(param.Param), address, output, true, false), nil

	case INVOKE_API_DONATE:
		assetAmt := output.GetAsset(p.GetAssetName())
		if assetAmt.IsZero() {
			return nil, fmt.Errorf("utxo %s no asset %s", utxo, p.GetAssetName().String())
		}

		if param.Param == "" {
			return nil, fmt.Errorf("invalid parameter")
		}
		paramBytes, err := base64.StdEncoding.DecodeString(param.Param)
		if err != nil {
			return nil, err
		}
		var innerParam DonateInvokeParam
		err = innerParam.Decode(paramBytes)
		if err != nil {
			return nil, err
		}
		if innerParam.AssetName != "" {
			if innerParam.AssetName != p.GetAssetName().String() {
				return nil, fmt.Errorf("invalid asset name %s", innerParam.AssetName)
			}
		}
		if innerParam.Amt != "0" && innerParam.Amt != "" {
			assetInParm, err := indexer.NewDecimalFromString(innerParam.Amt, MAX_ASSET_DIVISIBILITY)
			if err != nil {
				return nil, err
			}
			if assetInParm.Cmp(assetAmt) > 0 {
				return nil, fmt.Errorf("invalid asset amt %s", innerParam.Amt)
			}
		}
		if innerParam.Value != 0 {
			if innerParam.Value > output.GetPlainSat() {
				return nil, fmt.Errorf("invalid sats value %d", innerParam.Value)
			}
		}

		invokeTx.Handled = true
		return p.updateContract(ORDERTYPE_DONATE, nil, address, output, true, false), nil

	case INVOKE_API_AIRDROP:
		if param.Param == "" {
			return nil, fmt.Errorf("invalid parameter")
		}
		paramBytes, err := base64.StdEncoding.DecodeString(param.Param)
		if err != nil {
			return nil, err
		}
		var innerParam AirDropInvokeParam
		err = innerParam.Decode(paramBytes)
		if err != nil {
			return nil, err
		}
		if len(innerParam.UIDs) == 0 {
			return nil, fmt.Errorf("invalid UIDs")
		}
		paded, err := innerParam.ToPaded()
		if err != nil {
			return nil, err
		}


		invokeTx.Handled = true
		return p.updateContract(ORDERTYPE_AIRDROP, paded, address, output, true, false), nil

	case INVOKE_API_VALIDATE:
		if param.Param == "" {
			return nil, fmt.Errorf("invalid parameter")
		}
		paramBytes, err := base64.StdEncoding.DecodeString(param.Param)
		if err != nil {
			return nil, err
		}
		var innerParam ValidateInvokeParam
		err = innerParam.Decode(paramBytes)
		if err != nil {
			return nil, err
		}
		if len(innerParam.Param) == 0 {
			return nil, fmt.Errorf("invalid Parameter")
		}
		//items := strings.Split(string(innerParam.Param), " ")
		//
		
		switch innerParam.OrderType {
		case ORDERTYPE_REGISTER:

		case ORDERTYPE_AIRDROP:

		default:
			return nil, fmt.Errorf("invalid order type %d", innerParam.OrderType)
		}

		invokeTx.Handled = true
		return p.updateContract(ORDERTYPE_VALIDATE, []byte(param.Param), address, output, true, false), nil

	case INVOKE_API_BIND:
		if param.Param == "" {
			return nil, fmt.Errorf("invalid parameter")
		}
		paramBytes, err := base64.StdEncoding.DecodeString(param.Param)
		if err != nil {
			return nil, err
		}
		var innerParam BindInvokeParam
		err = innerParam.Decode(paramBytes)
		if err != nil {
			return nil, err
		}
		err = innerParam.Check()
		if err != nil {
			return nil, err
		}
		referrer := p.loadInvokerInfo(address)
		if referrer.UID == "" {
			return nil, fmt.Errorf("referrer hasn't uid")
		}

		paded, err := innerParam.ToPaded()
		if err != nil {
			return nil, err
		}

		invokeTx.Handled = true
		return p.updateContract(ORDERTYPE_BIND, paded, address, output, true, false), nil

	default:
		Log.Errorf("contract %s does not support action %s", url, param.Action)
		return nil, fmt.Errorf("not support action %s", param.Action)
	}
}

func (p *DaoContractRunTime) VerifyAndAcceptInvokeItem(invokeTx *InvokeTx, height int) (InvokeHistoryItem, error) {

	invokeData := invokeTx.InvokeParam
	output := invokeTx.TxOutput
	address := invokeTx.Invoker

	var param InvokeParam
	if invokeData != nil && invokeData.InvokeParam != nil {
		err := param.Decode(invokeData.InvokeParam)
		if err != nil {
			return nil, err
		}
	} else { // TODO 主网过来的调用都没有设置参数，跟AMM/transend的符文有冲突，一个地址不能同时部署两个recycle和amm/transcend合约
		param.Action = INVOKE_API_RECYCLE
	}

	utxoId := output.UtxoId
	utxo := output.OutPointStr
	Log.Infof("utxo %x %s\n", utxoId, utxo)
	org, ok := p.history[utxo]
	if ok {
		if org.UtxoId != utxoId { // reorg
			org.UtxoId = utxoId
			SaveContractInvokeHistoryItem(p.stp.GetDB(), p.URL(), org)
		}
		invokeTx.Handled = true
		return nil, fmt.Errorf("contract utxo %s exists", utxo)
	}

	switch param.Action {

	case INVOKE_API_DONATE:
		assetAmt := output.GetAsset(p.GetAssetName())
		if assetAmt.IsZero() {
			return nil, fmt.Errorf("utxo %s no asset %s", utxo, p.GetAssetName().String())
		}

		if param.Param == "" {
			return nil, fmt.Errorf("invalid parameter")
		}
		paramBytes, err := base64.StdEncoding.DecodeString(param.Param)
		if err != nil {
			return nil, err
		}
		var innerParam DonateInvokeParam
		err = innerParam.Decode(paramBytes)
		if err != nil {
			return nil, err
		}
		if innerParam.AssetName != "" {
			if innerParam.AssetName != p.GetAssetName().String() {
				return nil, fmt.Errorf("invalid asset name %s", innerParam.AssetName)
			}
		}
		if innerParam.Amt != "0" && innerParam.Amt != "" {
			if innerParam.Amt != assetAmt.String() {
				return nil, fmt.Errorf("invalid asset amt %s", innerParam.Amt)
			}
		}
		if innerParam.Value != 0 {
			if innerParam.Value > output.GetPlainSat() {
				return nil, fmt.Errorf("invalid sats value %d", innerParam.Value)
			}
		}

		invokeTx.Handled = true
		return p.updateContract(ORDERTYPE_DONATE, []byte(param.Param), address, OutputToSatsNet(output), true, true), nil

	default:
		Log.Errorf("contract %s does not support action %s", p.URL(), param.Action)
		return nil, fmt.Errorf("not support action %s", param.Action)
	}
}

// 通用的调用参数入口
func (p *DaoContractRunTime) updateContract(order int, param []byte,
	invoker string, output *TxOutput_SatsNet, bValid bool, fromL1 bool,
) *InvokeItem {

	assetName := p.GetAssetName()
	var inValue int64
	var inAmt *Decimal

	inValue = output.GetPlainSat()
	inAmt = output.GetAsset(assetName)

	serviceFee := INVOKE_FEE
	remainingValue := inValue - serviceFee
	remainingAmt := inAmt.Clone()

	reason := INVOKE_REASON_NORMAL
	if !bValid {
		reason = INVOKE_REASON_INVALID
	}
	item := &InvokeItem{
		InvokeHistoryItemBase: InvokeHistoryItemBase{
			Id:     p.InvokeCount,
			Reason: reason,
			Done:   ITEM_STATUS_INIT,
		},

		OrderType:      order,
		UtxoId:         output.UtxoId,
		OrderTime:      time.Now().Unix(),
		AssetName:      assetName.String(),
		ServiceFee:     serviceFee,
		UnitPrice:      nil,
		ExpectedAmt:    nil,
		Address:        invoker,
		FromL1:         fromL1,
		InUtxo:         output.OutPointStr,
		InValue:        inValue,
		InAmt:          inAmt,
		RemainingAmt:   remainingAmt,
		RemainingValue: remainingValue,
		ToL1:           false,
		OutAmt:         nil,
		OutValue:       0,
		Padded:         param,
	}
	p.updateContractStatus(item)
	if reason == INVOKE_REASON_INVALID {
		// 无效的指令，直接关闭
		item.Done = ITEM_STATUS_CLOSED_DIRECTLY
	} else {
		p.addItem(item)
	}
	SaveContractInvokeHistoryItem(p.stp.GetDB(), p.URL(), item)
	return item
}

// 更新需要写入数据库的数据
func (p *DaoContractRunTime) updateContractStatus(item *InvokeItem) {
	p.history[item.InUtxo] = item

	invoker := p.loadInvokerInfo(item.Address)
	InsertItemToInvokerHistroy(&invoker.InvokerStatusBaseV2, item)

	p.InvokeCount++
	p.TotalDonateAmt = p.TotalDonateAmt.Add(item.InAmt)
	p.TotalInputValue += item.InValue
	p.SatsValueInPool += item.InValue
	p.AssetAmtInPool = p.AssetAmtInPool.Add(item.InAmt)

	if item.Reason == INVOKE_REASON_NORMAL {
		invoker.InvokeAmt = invoker.InvokeAmt.Add(item.InAmt)
		invoker.InvokeValue += item.InValue
		switch item.OrderType {
		case ORDERTYPE_REGISTER:

		case ORDERTYPE_DONATE:

		case ORDERTYPE_AIRDROP:

		case ORDERTYPE_VALIDATE:

		}
	} // else 只可能是 INVOKE_REASON_INVALID 不用更新任何数据

	saveContractInvokerStatus(p.stp.GetDB(), p.URL(), invoker)
	// 整体状态在外部保存
}

func (p *DaoContractRunTime) addUnhandledUID_register(item *InvokeItem) {
	var innerParam RegisterInvokeParam
	paramBytes, err := base64.StdEncoding.DecodeString(string(item.Padded))
	if err != nil {
		return
	}
	err = innerParam.Decode(paramBytes)
	if err != nil {
		// 不可能会出现，前面检查过了
		return
	}
	p.unhandledUidMap[innerParam.UID] = &UnhandledBindInfo{
		ItemId: item.Id,
		UID: innerParam.UID,
		Address: item.Address,
		ReferrerUID: innerParam.ReferrerUID,
	}
}

func (p *DaoContractRunTime) addUnhandledUID_bind(item *InvokeItem) {
	var pad map[string]*AddressResult
	err := DecodeFromBytes(item.Padded, &pad)
	if err != nil {
		return
	}
	referrer := p.loadInvokerInfo(item.Address)
	for uid, value := range pad {
		if value.Result == RESULT_UNHANDLED {
			p.unhandledUidMap[uid] = &UnhandledBindInfo{
				ItemId: item.Id,
				UID: uid,
				Address: value.Address,
				ReferrerUID: referrer.UID,
			}
		}
	}
}

func (p *DaoContractRunTime) addUnhandledUID_airdrop(item *InvokeItem) {
	var pad map[string]string // uid->result
	err := DecodeFromBytes(item.Padded, &pad)
	if err != nil {
		return
	}
	referrer := p.loadInvokerInfo(item.Address)
	for uid, result := range pad {
		if result == "" {
			p.unhandledUidMap[uid] = &UnhandledBindInfo{
				ItemId: item.Id,
				UID: uid,
				Address: p.uidMap[uid],
				ReferrerUID: referrer.UID,
			}
		}
	}
}

// 不需要写入数据库的缓存数据，不能修改任何需要保存数据库的变量
func (p *DaoContractRunTime) addItem(item *InvokeItem) {
	if item.Reason == INVOKE_REASON_NORMAL {
		switch item.OrderType {
		case ORDERTYPE_REGISTER:
			addItemToMap(item, p.registerMap)
			p.addUnhandledUID_register(item)

		case ORDERTYPE_BIND:
			addItemToMap(item, p.bindMap)
			p.addUnhandledUID_bind(item)

		case ORDERTYPE_DONATE:
			addItemToMap(item, p.donateMap)

		case ORDERTYPE_AIRDROP:
			addItemToMap(item, p.airdropMap)
			p.addUnhandledUID_airdrop(item)

		case ORDERTYPE_VALIDATE:
			addItemToMap(item, p.validateMap)
		}
	}

	p.insertBuck(item)
}

func getMinItem(items map[string]*Decimal) (string, *Decimal) {
	var minDonate *Decimal
	var minAddr string

	for addr, donate := range items {
		if minDonate.Sign() == 0 {
			minDonate = donate.Clone()
			minAddr = addr
		} else {
			if donate.Cmp(minDonate) < 0 {
				minDonate = donate.Clone()
				minAddr = addr
			}
		}
	}
	return minAddr, minDonate
}

func updateItems(items map[string]*Decimal, maxSize int, addr string, amt *Decimal) bool {
	minAddr, minDonate := getMinItem(items)
	if len(items) < maxSize {
		items[addr] = amt
		return true
	} else {
		if amt.Cmp(minDonate) > 0 {
			items[addr] = amt
			// 删除最小的validator
			delete(items, minAddr)
			return true
		}
	}
	return false
}

func (p *DaoContractRunTime) binding(address, uid, referrerUID string, force bool,
	invokers map[string]*DaoInvokerStatus) int {
	referral := p.loadInvokerInfo(address)
	invokers[address] = referral
	result := RESULT_INVALID
	if referral.UID == "" || force {
		referral.UID = uid
		p.uidMap[uid] = address
		result = RESULT_OK
	}
	if (referral.ReferrerUID == "" || force) && referrerUID != "" {
		referral.ReferrerUID = referrerUID
		// 反向绑定
		referrerAddr, ok := p.uidMap[referrerUID]
		if ok {
			referrer := p.loadInvokerInfo(referrerAddr)
			referrer.ReferralUIDs = indexer.InsertVector_string(referrer.ReferralUIDs, uid)
			invokers[referrerAddr] = referrer
			result = RESULT_OK
		}
	}
	Log.Infof("%s bind uid %s, referrerUID %s", address, uid, referral.ReferrerUID)
	return result
}

func (p *DaoContractRunTime) handleRegisterItem(item *InvokeItem,
	result int, reason string, invokers map[string]*DaoInvokerStatus) {
	var innerParam RegisterInvokeParam
	paramBytes, err := base64.StdEncoding.DecodeString(string(item.Padded))
	if err != nil {
		return
	}
	err = innerParam.Decode(paramBytes)
	if err != nil {
		// 不可能会出现，前面检查过了
		return
	}
	if result == 0 {
		item.Done = ITEM_STATUS_DEALT
		p.binding(item.Address, innerParam.UID, innerParam.ReferrerUID, true, invokers)
	} else {
		item.Reason = reason
		item.Done = ITEM_STATUS_CLOSED_DIRECTLY
	}
	delete(p.unhandledUidMap, innerParam.UID)
	//delete(p.history, item.InUtxo) 暂时不删除，防止reorg
	SaveContractInvokeHistoryItem(p.db, p.URL(), item)
}

func (p *DaoContractRunTime) checkAirdropFlag(referrerUID, uid string) (*DaoInvokerStatus, *Decimal, bool) {
	//uid, addr := retrieveFromInvokeUID(uid)
	referralAddr, ok := p.uidMap[uid]
	if !ok {
		// if !p.OnlyRegisterSelf && addr != "" {
		// 	// 建设者注册被推荐人的uid，一个特殊的注册流程
		// 	p.binding(addr, uid, referrerUID, false, invokers)
		// } else {
			Log.Errorf("can't find address with uid %s", uid)
			return nil, nil, false
		//}
	}
	referral := p.loadInvokerInfo(referralAddr)
	if referral.ReferrerUID != referrerUID {
		Log.Errorf("invoker %s has different referrer %s, expected %s", referralAddr, referral.ReferrerUID, referrerUID)
		return nil, nil, false
	}
	if referral.Airdropped {
		return referral, nil, false
	}

	// 检查对应的资产数据
	amt1 := p.stp.GetWalletMgr().GetAssetBalance(referralAddr, &p.HoldingAssetName)
	amt2 := p.stp.GetWalletMgr().GetAssetBalance_SatsNet(referralAddr, &p.HoldingAssetName)
	amt := amt1.Add(amt2)
	// 如果没有资产，先直接返回
	if amt.Sign() == 0 {
		return referral, nil, false
	}
	if p.airdropThreshold.Sign() != 0 {
		if amt.Cmp(p.airdropThreshold) < 0 {
			return referral, nil, false
		}
	}

	return referral, amt, true
}

func (p *DaoContractRunTime) calcAirdropAmt(amt *Decimal) *Decimal {
	airdrop := amt.Mul(p.airdropRatio)
	if airdrop.Cmp(p.airdropLimit) > 0 {
		airdrop = p.airdropLimit.Clone()
	}
	return airdrop
}


type AirdropItem struct {
	UID        string `json:"uid"`        // 被推荐人
	Address    string `json:"address"`    // 被推荐人
	Result     string `json:"result"`     // 从该被推荐人获得的空投数量，或者被拒绝的原因
}

type AirdropResult struct {
	Items []*AirdropItem `json:"items,omitempty"` 
}

func (p *AirdropResult) Encode() ([]byte, error) {
	builder := txscript.NewScriptBuilder()
	for _, item := range p.Items {
		builder = builder.AddData([]byte(item.UID+":"+item.Result)) // 不保存address
	}
	return builder.Script()
}

func (p *AirdropResult) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)
	for tokenizer.Next() && tokenizer.Err() == nil {
		item := string(tokenizer.Data())
		parts := strings.Split(item, ":")
		if len(parts) != 2 {
			continue
		}
		p.Items = append(p.Items, &AirdropItem{
			UID: parts[0],
			Result: parts[1],
		})
	}
	return nil
}


func (p *DaoContractRunTime) handleAirdropItem(item *InvokeItem, 
	invokers map[string]*DaoInvokerStatus) bool {
	ret := false

	var pad map[string]string  // uid->result
	err := DecodeFromBytes(item.Padded, &pad)
	if err != nil {
		Log.Errorf("DecodeFromBytes %s failed %v", item.InUtxo, err)
		item.Reason = INVOKE_REASON_INVALID
		item.Done = ITEM_STATUS_CLOSED_DIRECTLY
		return false
	}

	invoker := p.loadInvokerInfo(item.Address)
	//invokers[item.Address] = invoker 没有更新，不需要加入

	var totalAirdropAmt *Decimal
	oldPad := utils.CloneStringMap(pad)
	updated := false
	for uid, value := range oldPad {
		var airdrop *Decimal
		if value != "" {
			// 被validate过，尝试看看是否能解码
			airdrop, err = indexer.NewDecimalFromString(value, p.Divisibility)
			if err != nil {
				continue
			}
			// 有效的结果
		} else {
			updated = true
			// 确保每一个uid的referrer都是 uid
			referral, amt, ok := p.checkAirdropFlag(invoker.UID, uid)
			if !ok {
				if referral == nil {
					pad[uid] = "invalid"
				} else {
					pad[uid] = "0"
				}
				continue
			}
			airdrop = p.calcAirdropAmt(amt)
			pad[uid] = airdrop.String()
			delete(p.unhandledUidMap, uid)

			referral.Airdropped = true
			referral.AirdroppedAmt = referral.AirdroppedAmt.Add(airdrop)
			invokers[referral.Address] = referral
		}
		totalAirdropAmt = totalAirdropAmt.Add(airdrop)
	}

	// 更新pad数据
	if updated {
		item.Padded, _ = EncodeToBytes(pad)
	}
	if totalAirdropAmt.Sign() != 0 {
		// 成功后再更新
		//invoker.TotalAirdropAmt = invoker.TotalAirdropAmt.Add(totalAirdropAmt)
		//invokers[invoker.Address] = invoker
		item.OutAmt = totalAirdropAmt
		item.Done = ITEM_STATUS_READY_TO_SEND
		ret = true
	} else {
		item.Reason = INVOKE_REASON_NO_AIRDROP_ASSET
		item.Done = ITEM_STATUS_CLOSED_DIRECTLY
	}
	
	Log.Infof("%s airdrop item %d with result %s", item.Address, item.Id, totalAirdropAmt.String())
	SaveContractInvokeHistoryItem(p.db, p.URL(), item)
	return ret
}

// 执行 height和blockhash必须来自聪网
func (p *DaoContractRunTime) process(height int, blockHash string) error {
	Log.Debugf("%s process contract %s with block %d %s",
		p.stp.GetMode(), p.URL(), height, blockHash)

	url := p.URL()
	updated := false

	invokers := make(map[string]*DaoInvokerStatus)

	// 1. 执行donate，目标是更新排行版
	if len(p.donateMap) > 0 {
		for addr, items := range p.donateMap {
			invoker := p.loadInvokerInfo(addr)
			invokers[addr] = invoker

			updateItems(p.Validators, p.ValidatorNum, addr, invoker.InvokeAmt.Clone())
			updateItems(p.donateRanking, RANKING_MAX_SIZE, addr, invoker.InvokeAmt.Clone())

			for _, item := range items {
				item.RemainingAmt = nil
				item.RemainingValue = 0
				item.Done = ITEM_STATUS_DEALT
				//delete(p.history, item.InUtxo) 暂时不删除，防止reorg
				SaveContractInvokeHistoryItem(p.db, url, item)
			}
		}
		updated = true
		p.donateMap = make(map[string]map[int64]*InvokeItem)
	}

	// 2. 执行validate
	if len(p.validateMap) > 0 {
		toSaveItems := make(map[int64]*InvokeItem)
		for addr, items := range p.validateMap {
			_, ok := p.Validators[addr]
			if !ok {
				// 全部设置为无效
				for _, item := range items {
					item.Reason = INVOKE_REASON_INVALID_VALIDATOR
					item.Done = ITEM_STATUS_CLOSED_DIRECTLY
					//delete(p.history, item.InUtxo) 暂时不删除，防止reorg
					SaveContractInvokeHistoryItem(p.db, url, item)
				}
				continue
			}

			for _, item := range items {
				var innerParam ValidateInvokeParam
				paramBytes, err := base64.StdEncoding.DecodeString(string(item.Padded))
				if err != nil {
					// 不可能会出现，前面检查过了
					continue
				}
				err = innerParam.Decode(paramBytes)
				if err != nil {
					// 不可能会出现，前面检查过了
					continue
				}
				if len(innerParam.Param) == 0 {
					// 不可能出现，前面检查过
					continue
				}
				uids := strings.Split(string(innerParam.Param), " ")
				for _, uid := range uids {
					info, ok := p.unhandledUidMap[uid]
					if !ok {
						Log.Errorf("uid %s is not in unhandled map", uid)
						continue
					}

					handingItem := p.getItemFromBuck(info.ItemId)
					if handingItem == nil {
						Log.Errorf("%s can't find item %d", url, info.ItemId)
						continue
					}

					switch innerParam.OrderType {
					case ORDERTYPE_REGISTER: // 包括bind
						
						switch handingItem.OrderType {
						case ORDERTYPE_REGISTER:
							p.handleRegisterItem(handingItem, innerParam.Result,
								innerParam.Reason, invokers)
							removeItemFromMap(handingItem, p.registerMap)

						case ORDERTYPE_BIND:
							var pad map[string]*AddressResult
							err := DecodeFromBytes(handingItem.Padded, &pad)
							if err != nil {
								continue
							}
							delete(p.unhandledUidMap, uid)
							referrer := p.loadInvokerInfo(handingItem.Address)
							value, ok := pad[uid]
							if ok {
								if innerParam.Result == 0 {
									value.Result = p.binding(value.Address, uid, referrer.UID, false, invokers)
								} else {
									value.Result = RESULT_REJECTED
								}

								finished := true
								for _, v := range pad {
									if v.Result == RESULT_UNHANDLED {
										finished = false
										break
									}
								}
								if finished {
									handingItem.Done = ITEM_STATUS_READY_TO_SEND
								}

								handingItem.Padded, _ = EncodeToBytes(pad)
								toSaveItems[handingItem.Id] = handingItem
								//SaveContractInvokeHistoryItem(p.db, p.URL(), handingItem)
							}
						}
						
					case ORDERTYPE_AIRDROP:
						var pad map[string]string  // uid->result
						err := DecodeFromBytes(handingItem.Padded, &pad)
						if err != nil {
							continue
						}
						referrer := p.loadInvokerInfo(handingItem.Address)
						value, ok := pad[uid]
						if ok && value == "" {
							if innerParam.Result == 0 {
								// 确保每一个uid的referrer都是 uid
								referral, amt, ok := p.checkAirdropFlag(referrer.UID, uid)
								if ok {
									airdrop := p.calcAirdropAmt(amt)
									// 设置空投标志
									referral.Airdropped = true
									referral.AirdroppedAmt = referral.AirdroppedAmt.Add(airdrop)
									invokers[referral.Address] = referral

									pad[uid] = airdrop.String()
								} else {
									if referral == nil {
										pad[uid] = "invalid"
									} else {
										pad[uid] = "0"
									}
								}
							} else {
								if innerParam.Reason == "" {
									pad[uid] = "rejected"
								} else {
									pad[uid] = innerParam.Reason
								}
							}

							finished := true
							for _, v := range pad {
								if v == "" {
									finished = false
									break
								}
							}
							if finished {
								handingItem.Done = ITEM_STATUS_READY_TO_SEND
							}

							delete(p.unhandledUidMap, uid)
							handingItem.Padded, _ = EncodeToBytes(pad)
							toSaveItems[handingItem.Id] = handingItem
							//SaveContractInvokeHistoryItem(p.db, p.URL(), handingItem)
						}
					}
				}

				item.Done = ITEM_STATUS_DEALT
				SaveContractInvokeHistoryItem(p.db, url, item)
			}
		}
		for _, v := range toSaveItems {
			SaveContractInvokeHistoryItem(p.db, url, v)
		}
		updated = true
		p.validateMap = make(map[string]map[int64]*InvokeItem)
	}

	// 3. 执行register
	if len(p.registerMap) > 0 {
		handled := make([]string, 0)
		for addr, items := range p.registerMap {
			deletedItems := make([]int64, 0)
			for id, item := range items {
				h, _, _ := indexer.FromUtxoId(item.UtxoId)
				if item.Finished() {
					continue
				}
				if height-h < p.RegisterTimeOut {
					continue
				}
				p.handleRegisterItem(item, 0, "", invokers)
				deletedItems = append(deletedItems, id)
				updated = true
			}

			for _, id := range deletedItems {
				delete(items, id)
			}
			if len(items) == 0 {
				handled = append(handled, addr)
			}
		}

		for _, addr := range handled {
			delete(p.registerMap, addr)
		}
	}

	// 4. 执行bind
	if len(p.bindMap) > 0 {
		handled := make([]string, 0)
		for addr, items := range p.bindMap {
			deletedItems := make([]int64, 0)
			for id, item := range items {
				if item.Finished() {
					continue
				}
				if item.Done != ITEM_STATUS_READY_TO_SEND { // 全部被审核完成
					h, _, _ := indexer.FromUtxoId(item.UtxoId)
					if height-h < p.RegisterTimeOut { // 超时
						continue
					}
				}

				var pad map[string]*AddressResult
				err := DecodeFromBytes(item.Padded, &pad)
				if err != nil {
					Log.Errorf("DecodeFromBytes Padded failed, %v", err)
					continue
				}
				referrer := p.loadInvokerInfo(item.Address)
				updated := false
				for uid, value := range pad {
					if value.Result == RESULT_UNHANDLED {
						value.Result = p.binding(value.Address, uid, referrer.UID, false, invokers)
						updated = true
					}
				}
				if updated {
					item.Padded, _ = EncodeToBytes(pad)
				}
				item.Done = ITEM_STATUS_DEALT
				SaveContractInvokeHistoryItem(p.db, p.URL(), item)

				deletedItems = append(deletedItems, id)
				updated = true
			}

			for _, id := range deletedItems {
				delete(items, id)
			}
			if len(items) == 0 {
				handled = append(handled, addr)
			}
		}

		for _, addr := range handled {
			delete(p.bindMap, addr)
		}
	}

	// 5. 执行airdrop
	if len(p.airdropMap) > 0 {
		handled := make([]string, 0)
		for addr, items := range p.airdropMap {
			deletedItems := make([]int64, 0)
			for id, item := range items {
				if item.Finished() {
					continue
				}
				if item.Done != ITEM_STATUS_READY_TO_SEND { // 全部被审核完成
					h, _, _ := indexer.FromUtxoId(item.UtxoId)
					if height-h < p.AirDropTimeOut { // 超时
						continue
					}
				}
				
				if p.handleAirdropItem(item, invokers) == false {
					deletedItems = append(deletedItems, id)
				}
				SaveContractInvokeHistoryItem(p.db, url, item)
				updated = true
			}

			for _, id := range deletedItems {
				delete(items, id)
			}
			if len(items) == 0 {
				handled = append(handled, addr)
			}
		}
		for _, addr := range handled {
			delete(p.airdropMap, addr)
		}
	}

	for _, invoker := range invokers {
		saveContractInvokerStatus(p.db, url, invoker)
	}

	// 结果先保存
	if updated {
		p.stp.SaveReservation(p.resv)
	}

	return nil
}

// 涉及发送各种tx，运行在线程中
func (p *DaoContractRunTime) sendInvokeResultTx() error {
	return p.sendInvokeResultTx_SatsNet()
}

// 涉及发送各种tx，运行在线程中
func (p *DaoContractRunTime) sendInvokeResultTx_SatsNet() error {
	if p.resv.LocalIsInitiator() {

		url := p.URL()

		err := p.airdrop()
		if err != nil {
			Log.Errorf("contract %s deal failed, %v", url, err)
		}

		//Log.Debugf("contract %s sendInvokeResultTx_SatsNet completed", url)
	} else {
		//Log.Debugf("server: waiting the deal Tx of contract %s ", p.URL())
	}
	return nil
}

// TODO 是否将涉及到的invokeItem的id列表，放入DealInfo中，发签名请求时同步到peer？这样确保不会有错误
// 目前是靠区块高度来对应处理所有相关invokeItem
func (p *DaoContractRunTime) updateWithDealInfo_airdrop(dealInfo *DealInfo) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.TotalAirdropAmt = p.TotalAirdropAmt.Add(dealInfo.TotalAmt)
	p.TotalAirdropCount++
	p.TotalFeeValue += dealInfo.Fee
	p.SatsValueInPool -= dealInfo.Fee
	p.AssetAmtInPool = p.AssetAmtInPool.Sub(dealInfo.TotalAmt)
	Log.Debugf("airdrop amt %s, fee %d, txId %s", p.TotalAirdropAmt, dealInfo.Fee, dealInfo.TxId)

	url := p.URL()
	height := dealInfo.Height
	txId := dealInfo.TxId

	for address, info := range dealInfo.SendInfo {
		airdropMap, ok := p.airdropMap[address]
		if ok {
			deleted := make([]int64, 0)
			for _, item := range airdropMap {
				h, _, _ := indexer.FromUtxoId(item.UtxoId)
				if h > height {
					continue
				}
				if item.Finished() {
					continue
				}
				item.Done = ITEM_STATUS_DEALT
				item.OutTxId = txId
				item.RemainingAmt = nil
				item.RemainingValue = 0
				item.ToL1 = false
				SaveContractInvokeHistoryItem(p.stp.GetDB(), url, item)
				deleted = append(deleted, item.Id)
				//delete(p.history, item.InUtxo) 暂时不删除，防止reorg
			}
			for _, id := range deleted {
				delete(airdropMap, id)
			}
			if len(airdropMap) == 0 {
				delete(p.airdropMap, address)
			}
		}
		invoker := p.loadInvokerInfo(address)
		if invoker != nil {
			invoker.TotalAirdropAmt = invoker.TotalAirdropAmt.Add(info.AssetAmt)
			saveContractInvokerStatus(p.stp.GetDB(), url, invoker)

			updateItems(p.airdropRanking, RANKING_MAX_SIZE, invoker.Address, invoker.TotalAirdropAmt.Clone())
		}
	}

	p.CheckPoint = dealInfo.InvokeCount
	p.AssetMerkleRoot = dealInfo.RuntimeMerkleRoot
	p.CheckPointBlock = dealInfo.Height

	p.refreshTime = 0
}

func (p *DaoContractRunTime) genAirdropInfo(height int) *DealInfo {

	p.mutex.RLock()
	defer p.mutex.RUnlock()

	assetName := p.GetAssetName()

	maxHeight := 0
	var totalValue int64
	var totalAmt *Decimal                          // 资产数量
	sendInfoMap := make(map[string]*SendAssetInfo) // key: address
	for _, airdropMap := range p.airdropMap {
		for _, item := range airdropMap {
			h, _, _ := indexer.FromUtxoId(item.UtxoId)
			if h > height {
				continue
			}
			if item.Finished() {
				continue
			}
			maxHeight = max(maxHeight, h)

			if item.OutAmt.Sign() != 0 || item.OutValue != 0 {
				totalAmt = totalAmt.Add(item.OutAmt)
				totalValue += item.OutValue

				info := addSendInfo(sendInfoMap, item.Address, assetName)
				info.AssetAmt = info.AssetAmt.Add(item.OutAmt)
				info.Value += item.OutValue
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

func (p *DaoContractRunTime) airdrop() error {

	// 发送
	if p.resv.LocalIsInitiator() {
		if len(p.airdropMap) == 0 {
			return nil
		}
		url := p.URL()

		Log.Debugf("%s start contract %s with action airdrop", p.stp.GetMode(), url)

		p.mutex.RLock()
		height := p.CurrBlock
		p.mutex.RUnlock()

		dealInfo := p.genAirdropInfo(height)
		// 发送
		if len(dealInfo.SendInfo) != 0 {
			txId, err := p.sendTx_SatsNet(dealInfo, INVOKE_API_AIRDROP)
			if err != nil {
				Log.Errorf("contract %s sendTx %s failed %v", url, INVOKE_API_AIRDROP, err)
				// 下个区块再试
				return err
			}
			// 调整fee
			dealInfo.Fee = DEFAULT_FEE_SATSNET
			dealInfo.TxId = txId
			// record
			p.updateWithDealInfo_airdrop(dealInfo)
			// 成功一步记录一步
			p.stp.SaveReservationWithLock(p.resv)
			Log.Infof("contract %s airdrop completed, %s", url, txId)
		}

		Log.Debugf("contract %s airdrop completed", url)
	} else {
		Log.Debugf("server: waiting the airdrop Tx of contract %s ", p.URL())
	}

	return nil
}

func (p *DaoContractRunTime) AllowPeerAction(action string, param any) (any, error) {

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
		// dealInfo, err := p.genRewardInfoFromReq(req)
		// if err == nil {
		// 	return dealInfo, nil
		// }

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

		case INVOKE_API_AIRDROP:
			info := p.genAirdropInfo(dealInfo.Height)
			if info != nil {
				expectedSendInfo = info.SendInfo
			}

		default:
			return nil, fmt.Errorf("not expected contract invoke reason %s", dealInfo.Reason)
		}

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
						dest.AssetAmt.String(), insc.Amt)
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
					dealInfo.PreOutputs = append(dealInfo.PreOutputs, output)
				}
				insc2, err := p.stp.GetWalletMgr().MintTransferV2_brc20(p.ChannelAddr,
					p.ChannelAddr, map[string]bool{}, insc.AssetName, insc.Amt, insc.FeeRate,
					inputs, true, insc.RevealPrivateKey, true, false, false)
				if err != nil {
					return nil, fmt.Errorf("can't regenerate inscribe info from request: %v", insc)
				}
				PrintJsonTx(insc2.CommitTx, "prev transfer commit for contract")
				PrintJsonTx(insc2.RevealTx, "prev transfer reveal for contract")
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

	default:
		return nil, fmt.Errorf("AllowPeerAction not support action %s", action)
	}

}

// 之前已经校验过
func (p *DaoContractRunTime) SetPeerActionResult(action string, param any) {
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
		case INVOKE_API_AIRDROP:
			p.updateWithDealInfo_airdrop(dealInfo)

		default:
			return
		}

		p.stp.SaveReservationWithLock(p.resv)
		Log.Infof("%s SetPeerActionResult %s completed", p.URL(), action)
		return
	}
}

func (p *DaoContractRunTime) CheckUID(uid string) bool {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	_, ok := p.uidMap[uid]
	return ok
}
