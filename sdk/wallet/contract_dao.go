package wallet

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
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
	MIN_REGISTER_FEE  int64 = 20
	MIN_VALIDATOR_NUM int   = 3
)

// 1. 定义合约内容
type DaoContract struct {
	ContractBase
	// 池子最少的激活资产
	AssetAmt string
	SatValue int64

	ValidatorNum    int   // 至少3
	RegisterFee     int64 // 最少20
	RegisterTimeOut int   // 聪网区块数，注册审核时间，超时自动确认

	// airdrop
	HoldingAssetName indexer.AssetName
	HoldingAssetAmt  string
	AirDropRatio     int
	AirDropLimit     string
	AirDropTimeOut   int // 聪网区块数，超时自动确认
	ReferralRatio    int // 默认为0，在空投中，一部分给被推荐人

	// 更多的配置数据
}

func NewDaoContract() *DaoContract {
	c := &DaoContract{
		ContractBase: ContractBase{
			TemplateName: TEMPLATE_CONTRACT_FUNDATION,
		},
		RegisterFee:  MIN_REGISTER_FEE,
		ValidatorNum: MIN_VALIDATOR_NUM,
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

	_, err := indexer.NewDecimalFromString(p.HoldingAssetAmt, MAX_ASSET_DIVISIBILITY)
	if err != nil {
		return fmt.Errorf("invalid HoldingAssetAmt %s", p.HoldingAssetAmt)
	}
	if p.AirDropRatio <= 0 {
		return fmt.Errorf("AirDropRatio should >= 0")
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

	return txscript.NewScriptBuilder().
		AddData(base).
		AddData([]byte(p.AssetAmt)).
		AddInt64(p.SatValue).
		AddInt64(p.RegisterFee).
		AddInt64(int64(p.ValidatorNum)).
		AddData([]byte(p.HoldingAssetName.String())).
		AddData([]byte(p.HoldingAssetAmt)).
		AddInt64(int64(p.AirDropRatio)).
		AddData([]byte(p.AirDropLimit)).
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
		return fmt.Errorf("missing RegisterFee")
	}
	p.RegisterFee = tokenizer.ExtractInt64()

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing ValidatorNum")
	}
	p.ValidatorNum = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing HoldingAssetName")
	}
	p.HoldingAssetName = *indexer.NewAssetNameFromString(string(tokenizer.Data()))

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing HoldingAssetAmt")
	}
	p.HoldingAssetAmt = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing AirDropRatio")
	}
	p.AirDropRatio = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing AirDropLimit")
	}
	p.AirDropLimit = string(tokenizer.Data())

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

// //
type AirDropInvokeParam struct {
	UIDs []string `json:"uids"`
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

type ValidateInvokeParam struct {
	OrderType int    `json:"orderType"`
	Param     []byte `json:"para"`
}

func (p *ValidateInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.OrderType)).
		AddData([]byte(p.Param)).
		Script()
}

func (p *ValidateInvokeParam) EncodeV2() ([]byte, error) {
	return p.Encode()
}

func (p *ValidateInvokeParam) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing type")
	}
	p.OrderType = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing amt")
	}
	p.Param = (tokenizer.Data())

	return nil
}

// 3. 定义合约交互者的数据结构

type DaoInvokerStatus struct {
	InvokerStatusBaseV2

	ReferrerUID     string   // 推荐人UID
	UID             string   // 自身UID
	TotalAirdropAmt *Decimal // 作为推荐人得到的空投
	Airdropped      bool     // 作为被推荐人，已经让推荐人得到了空投
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
	TotalAirdropCount int             // 空投交易计数
	TotalAirdropAmt   *Decimal        // 所有空投出去的资产数量
	TotalFeeValue     int64           // 所有由合约支付的相关交易的网络费用
	Validators        map[string]bool // address
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

// 6. 合约运行时状态
type DaoContractRunTime struct {
	DaoContractRunTimeInDB

	invokerMap  map[string]*DaoInvokerStatus // key: address
	registerMap map[string]map[int64]*InvokeItem
	donateMap   map[string]map[int64]*InvokeItem // 还在处理中的调用, address -> invoke item list,
	airdropMap  map[string]map[int64]*InvokeItem
	validateMap map[string]map[int64]*InvokeItem

	responseCache  []*responseItem_dao
	responseStatus Response_DaoContract
}

func NewDaoContractRunTime(stp ContractManager) *DaoContractRunTime {
	p := &DaoContractRunTime{
		DaoContractRunTimeInDB: DaoContractRunTimeInDB{
			DaoContract:         *NewDaoContract(),
			ContractRuntimeBase: *NewContractRuntimeBase(stp),
			DaoContractRunningData: DaoContractRunningData{
				Validators: make(map[string]bool),
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
	p.airdropMap = make(map[string]map[int64]*InvokeItem)
	p.donateMap = make(map[string]map[int64]*InvokeItem)
	p.validateMap = make(map[string]map[int64]*InvokeItem)
}

func (p *DaoContractRunTime) InitFromJson(content []byte, stp ContractManager) error {
	err := json.Unmarshal(content, p)
	if err != nil {
		return err
	}
	p.init()

	return nil
}

func (p *DaoContractRunTime) InitFromDB(stp ContractManager, resv ContractDeployResvIF) error {

	err := p.ContractRuntimeBase.InitFromDB(stp, resv)
	if err != nil {
		Log.Errorf("SwapContractRuntime.InitFromDB failed, %v", err)
		return err
	}
	p.init()

	history := LoadContractInvokeHistory(p.db, p.URL(), true, false)
	for _, v := range history {
		item, ok := v.(*SwapHistoryItem)
		if !ok {
			continue
		}

		p.loadInvokerInfo(item.Address)
		p.addItem(item)
		p.history[item.InUtxo] = item
	}

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

func (p *DaoContractRunTime) updateResponseData() {
	if p.refreshTime == 0 {
		p.mutex.Lock()
		defer p.mutex.Unlock()

		// responseCache

		// responseStatus
		tickerInfo := p.stp.GetTickerInfo(&p.AssetName)
		p.responseStatus.DaoContractRunTimeInDB = &p.DaoContractRunTimeInDB
		p.responseStatus.DisplayName = tickerInfo.DisplayName

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

func (p *DaoContractRunTime) RuntimeAnalytics() string {
	p.updateResponseData()

	p.mutex.RLock()
	defer p.mutex.RUnlock()

	// buf, err := json.Marshal(p.responseAnalytics)
	// if err != nil {
	// 	Log.Errorf("RuntimeAnalytics Marshal %s failed, %v", p.URL(), err)
	// 	return ""
	// }
	// return string(buf)
	return ""
}

func (p *DaoContractRunTime) InvokeHistory(f any, start, limit int) string {
	p.updateResponseData()

	return p.GetRuntimeBase().InvokeHistory(f, start, limit)
}

type responseItem_dao struct {
	Address    string `json:"address"`
	DonateAmt  string `json:"donate"`
	AirdropAmt string `json:"airdrop"`
}

type Response_DaoContract struct {
	*DaoContractRunTimeInDB

	// 增加更多参数
	DisplayName string `json:"displayName"`
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

type Response_DaoInvokerStatus struct {
	Statistic   *DaoInvokerStatistic `json:"status"`
	DonateList  []string             `json:"donnate"`
	AirdropList []string             `json:"airdrop"`
}

func (p *DaoContractRunTime) StatusByAddress(address string) (string, error) {

	//p.updateResponseData()

	p.mutex.Lock()
	defer p.mutex.Unlock()

	result := &Response_DaoInvokerStatus{}
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
		invokes := p.donateMap[address]
		for _, v := range invokes {
			result.DonateList = append(result.DonateList, v.InUtxo)
		}

		airdrops := p.airdropMap[address]
		for _, v := range airdrops {
			result.AirdropList = append(result.AirdropList, v.InUtxo)
		}
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
	if templateName != TEMPLATE_CONTRACT_FUNDATION {
		return 0, fmt.Errorf("unsupport")
	}

	switch invoke.Action {
	case INVOKE_API_REGISTER:
		var innerParam RegisterInvokeParam
		err := json.Unmarshal([]byte(invoke.Param), &innerParam)
		if err != nil {
			return 0, err
		}

		if innerParam.UID == "" {
			return 0, fmt.Errorf("invalid uid")
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

		_, err = indexer.NewDecimalFromString(innerParam.Amt, p.Divisibility)
		if err != nil {
			return 0, fmt.Errorf("invalid amt %s", innerParam.Amt)
		}

		if innerParam.Value < 0 {
			return 0, fmt.Errorf("invalid value %d", innerParam.Value)
		}

		return INVOKE_FEE, nil

	case INVOKE_API_AIRDROP:
		var innerParam AirDropInvokeParam
		err := json.Unmarshal([]byte(invoke.Param), &innerParam)
		if err != nil {
			return 0, err
		}

		if len(innerParam.UIDs) == 0 {
			return 0, fmt.Errorf("invalid UIDs")
		}
		return INVOKE_FEE, nil

	case INVOKE_API_VALIDATE:
		var innerParam ValidateInvokeParam
		err := json.Unmarshal([]byte(invoke.Param), &innerParam)
		if err != nil {
			return 0, err
		}

		if len(innerParam.Param) == 0 {
			return 0, fmt.Errorf("invalid parameter")
		}
		items := strings.Split(string(innerParam.Param), " ")
		for _, item := range items {
			_, err := strconv.ParseInt(item, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid parameter")
			}
		}
		switch innerParam.OrderType {
		case ORDERTYPE_REGISTER:
			// item id list

		case ORDERTYPE_DONATE:
			// item id list

		default:
			return 0, fmt.Errorf("invalid order type %d", innerParam.OrderType)
		}

		return 0, nil

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
		p.process(data.Height, data.BlockHash)
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
		if innerParam.UID == "" {
			return nil, fmt.Errorf("invalid UID %s", innerParam.UID)
		}

		return p.updateContract(ORDERTYPE_REGISTER, paramBytes, address, output, true, false), nil

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

		return p.updateContract(ORDERTYPE_DONATE, paramBytes, address, output, true, false), nil

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

		return p.updateContract(ORDERTYPE_AIRDROP, paramBytes, address, output, true, false), nil

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
		items := strings.Split(string(innerParam.Param), " ")
		for _, item := range items {
			_, err := strconv.ParseInt(item, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid parameter")
			}
		}
		switch innerParam.OrderType {
		case ORDERTYPE_REGISTER:

		case ORDERTYPE_AIRDROP:

		default:
			return nil, fmt.Errorf("invalid order type %d", innerParam.OrderType)
		}

		return p.updateContract(ORDERTYPE_VALIDATE, paramBytes, address, output, true, false), nil

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

		return p.updateContract(ORDERTYPE_DONATE, paramBytes, address, OutputToSatsNet(output), true, true), nil

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
	if indexer.IsPlainAsset(assetName) {
		inValue = output.OutValue.Value
	} else {
		inAmt = output.GetAsset(assetName)
	}

	serviceFee := int64(0)
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
			Done:   DONE_NOTYET,
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
		OutAmt:         indexer.NewDecimal(0, p.Divisibility),
		OutValue:       0,
		Padded:         param,
	}
	p.updateContractStatus(item)
	if reason == INVOKE_REASON_INVALID {
		// 无效的指令，直接关闭
		item.Done = DONE_CLOSED_DIRECTLY
	} else {
		p.addItem(item)
	}
	SaveContractInvokeHistoryItem(p.stp.GetDB(), p.URL(), item)
	return item
}

// 更新需要写入数据库的数据
func (p *DaoContractRunTime) updateContractStatus(item *SwapHistoryItem) {
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
	} // else 只可能是 INVOKE_REASON_INVALID 不用更新任何数据

	saveContractInvokerStatus(p.stp.GetDB(), p.URL(), invoker)
	// 整体状态在外部保存
}

// 不需要写入数据库的缓存数据，不能修改任何需要保存数据库的变量
func (p *DaoContractRunTime) addItem(item *SwapHistoryItem) {
	if item.Reason == INVOKE_REASON_NORMAL {
		switch item.OrderType {
		case ORDERTYPE_REGISTER:
			addItemToMap(item, p.registerMap)

		case ORDERTYPE_DONATE:
			addItemToMap(item, p.donateMap)

		case ORDERTYPE_AIRDROP:
			addItemToMap(item, p.airdropMap)

		case ORDERTYPE_VALIDATE:
			addItemToMap(item, p.validateMap)
		}
	}

	p.insertBuck(item)
}

// 执行
func (p *DaoContractRunTime) process(height int, blockHash string) error {
	if len(p.donateMap) == 0 {
		return nil
	}

	Log.Debugf("%s start contract %s with action recycle with block %d %s",
		p.stp.GetMode(), p.URL(), height, blockHash)

	url := p.URL()
	updated := false
	//isPlainAsset := indexer.IsPlainAsset(p.GetAssetName())

	processedItems := make([]*InvokeItem, 0)
	invokers := make(map[string]*DaoInvokerStatus)

	for _, invokes := range p.donateMap {
		for _, item := range invokes {

			updated = true
			Log.Infof("item %s processed: inValue=%d", item.InUtxo,
				item.InValue)
		}
	}

	for _, invoker := range invokers {
		saveContractInvokerStatus(p.db, url, invoker)
	}

	for _, item := range processedItems {
		removeItemFromMap(item, p.donateMap)
		SaveContractInvokeHistoryItem(p.db, url, item)
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

func (p *DaoContractRunTime) updateWithDealInfo_airdrop(dealInfo *DealInfo) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.TotalAirdropAmt = p.TotalAirdropAmt.Add(dealInfo.TotalAmt)
	p.TotalAirdropCount++
	p.TotalFeeValue += dealInfo.Fee
	p.SatsValueInPool -= dealInfo.Fee
	Log.Debugf("airdrop count %d, fee %d, txId %s", p.TotalAirdropAmt, dealInfo.Fee, dealInfo.TxId)

	url := p.URL()
	height := dealInfo.Height
	txId := dealInfo.TxId

	for address, info := range dealInfo.SendInfo {
		rewardMap, ok := p.airdropMap[address]
		if ok {
			deleted := make([]int64, 0)
			for _, item := range rewardMap {
				h, _, _ := indexer.FromUtxoId(item.UtxoId)
				if h > height {
					continue
				}
				if item.Done != DONE_NOTYET {
					continue
				}
				item.Done = DONE_DEALT
				item.OutTxId = txId
				item.RemainingAmt = nil
				item.RemainingValue = 0
				item.ToL1 = true
				SaveContractInvokeHistoryItem(p.stp.GetDB(), url, item)
				deleted = append(deleted, item.Id)
				delete(p.history, item.InUtxo)
			}
			for _, id := range deleted {
				delete(rewardMap, id)
			}
			if len(rewardMap) == 0 {
				delete(p.airdropMap, address)
			}
		}
		trader := p.loadInvokerInfo(address)
		if trader != nil {
			trader.TotalAirdropAmt = trader.TotalAirdropAmt.Add(info.AssetAmt)
			saveContractInvokerStatus(p.stp.GetDB(), url, trader)

			// TODO 需要同步设置哪些被推荐人的空投标志
		}
	}

	p.CheckPoint = dealInfo.InvokeCount
	p.AssetMerkleRoot = dealInfo.RuntimeMerkleRoot
	p.CheckPointBlockL1 = dealInfo.Height

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
	for _, rewardMap := range p.airdropMap {
		for _, item := range rewardMap {
			h, _, _ := indexer.FromUtxoId(item.UtxoId)
			if h > height {
				continue
			}
			if item.Done != DONE_NOTYET {
				continue
			}
			maxHeight = max(maxHeight, h)

			totalAmt = totalAmt.Add(item.OutAmt)
			totalValue += item.OutValue

			info := addSendInfo(sendInfoMap, item.Address, assetName)
			info.AssetAmt = info.AssetAmt.Add(item.OutAmt)
			info.Value += item.OutValue
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
		heightL1 := p.CurrBlockL1
		p.mutex.RUnlock()

		dealInfo := p.genAirdropInfo(heightL1)
		// 发送
		if len(dealInfo.SendInfo) != 0 {
			txId, fee, stubFee, err := p.sendTx(dealInfo, INVOKE_API_AIRDROP, false, false)
			if err != nil {
				if stubFee != 0 {
					p.mutex.Lock()
					p.TotalFeeValue += stubFee
					p.mutex.Unlock()
					p.stp.SaveReservationWithLock(p.resv)
				}
				Log.Errorf("contract %s sendTx %s failed %v", url, INVOKE_API_AIRDROP, err)
				// 下个区块再试
				return err
			}
			// 调整fee
			dealInfo.Fee = fee + stubFee
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

func (p *DaoContractRunTime) genRewardInfoFromReq(req *wwire.RemoteSignMoreData_Contract) (*DealInfo, error) {

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
		items, ok := p.airdropMap[destAddr]
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
