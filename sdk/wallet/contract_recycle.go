package wallet

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/sat20-labs/satoshinet/txscript"
)


func init() {
	// 让 gob 知道旧的类型对应新的实现
	gob.RegisterName("RecycleContractRunTime", new(RecycleContractRunTime))

	if IsTestNet() {
		_valueLimit = 50
		_addressLimit = 1
	}
}


/*
垃圾utxo回收合约
1. 在主网运行
2. 转入合约地址的小于1000聪的utxo，自动当作合约调用
3. 合约作用：
  a. 积分积累：每个utxo，根据分别给予1-2-3分
  b. 自动抽奖：根据打包的block的hash，和交易的hash，两者最后6个数字，异或成兑奖号码
*/

// 1. 定义合约内容
type RecycleContract struct {
	ContractBase

	NumberOfLastDigits  int

	FirstPrize 	*Decimal
	SecondPrize *Decimal
	ThirdPrize 	*Decimal
}

func NewRecycleContract() *RecycleContract {
	return &RecycleContract{
		ContractBase: ContractBase{
			TemplateName: TEMPLATE_CONTRACT_RECYCLE,
		},
	}
}


func (p *RecycleContract) CheckContent() error {
	if indexer.IsPlainAsset(&p.AssetName) {
		return nil
	}

	err := p.ContractBase.CheckContent()
	if err != nil {
		return err
	}

	return nil
}

func (p *RecycleContract) GetContractName() string {
	return p.AssetName.String() + URL_SEPARATOR + p.TemplateName
}

func (p *RecycleContract) GetAssetName() *swire.AssetName {
	return &p.AssetName
}

func (p *RecycleContract) Content() string {
	b, err := json.Marshal(p)
	if err != nil {
		Log.Errorf("Marshal Contract failed, %v", err)
		return ""
	}
	return string(b)
}

func (p *RecycleContract) DeployFee(feeRate int64) int64 {
	return DEFAULT_SERVICE_FEE_DEPLOY_CONTRACT + // 服务费，如果不需要，可以在外面扣除
	DEFAULT_FEE_SATSNET + SWAP_INVOKE_FEE + // 部署该合约需要的网络费用和调用合约费用
	DEFAULT_FEE_SATSNET // 激活合约的网络费用
}

func (p *RecycleContract) InvokeParam(action string) string {
	if action != INVOKE_API_RECYCLE {
		return ""
	}
	
	var param InvokeParam
	param.Action = action
	param.Param = ""

	result, err := json.Marshal(&param)
	if err != nil {
		return ""
	}
	return string(result)
}

func (p *RecycleContract) CalcStaticMerkleRoot() []byte {
	return CalcContractStaticMerkleRoot(p)
}


// 2. 定义合约调用的数据结构 

// InvokeParam
type RecycleInvokeParam struct {
	AssetName string `json:"assetName"` // 资产名字
	Amt       string `json:"amt"`       // 资产数量
}

func (p *RecycleInvokeParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(p.AssetName)).
		AddData([]byte(p.Amt)).Script()
}

func (p *RecycleInvokeParam) EncodeV2() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte("")).
		AddData([]byte(p.Amt)).Script()
}

func (p *RecycleInvokeParam) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing asset name")
	}
	p.AssetName = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing amt")
	}
	p.Amt = string(tokenizer.Data())

	return nil
}

// 3. 定义合约交互者的数据结构

type RecycleInvokerStatus struct {
	InvokerStatusBaseV2
	
	TotalRewardAmt        *Decimal
	TotalRewardValue      int64
}

func NewRecycleInvokerStatus(address string, divisibility int) *RecycleInvokerStatus {
	return &RecycleInvokerStatus{
		InvokerStatusBaseV2: *NewInvokerStatusBaseV2(address, divisibility),
	}
}

func (p *RecycleInvokerStatus) GetVersion() int {
	return p.Version
}

func (p *RecycleInvokerStatus) GetKey() string {
	return p.Address
}


func (p *RecycleInvokerStatus) GetInvokeCount() int {
	return p.InvokeCount
}

func (p *RecycleInvokerStatus) GetHistory() map[int][]int64 {
	return p.History
}

// 非数据记录
type RecycleInvokerStatistic struct {
	InvokeCount int
	InvokeAmt    string
	InvokeValue  int64
	RewardAmt    string
	RewardValue  int64
}

type RecycleContractRunningData_old = RecycleContractRunningData

// 4. 定义合约运行时需要维护的数据
type RecycleContractRunningData struct {
	AssetAmtInPool       *Decimal
	SatsValueInPool      int64    // 池子中聪的数量
	TotalInputCount      int      // 回收交易计数
	TotalInputSats       int64
	TotalInputAssets     *Decimal // 所有进入池子的资产数量，指主网上每个地址参与回收的每个TX的输入资产总量
	TotalRewardCount     int      // 获奖交易计数
	TotalRewardAmt       *Decimal // 所有奖励出去的资产数量
	TotalRewardValue     int64    // 所有奖励出去的聪数量
	TotalFeeValue        int64    // 所有由合约支付的相关交易的网络费用
	TotalAddress         int      // 所有参与回收的地址
	TotalRewardAddress   int      // 所有获奖的地址计数
}

func (p *RecycleContractRunningData) ToNewVersion() *RecycleContractRunningData {
	return p
}


// 5. 定义合约保存到数据库中的数据
type RecycleContractRunTimeInDB struct {
	RecycleContract
	ContractRuntimeBase

	// 运行过程的状态
	RecycleContractRunningData
}

// 6. 合约运行时状态
type RecycleContractRunTime struct {
	RecycleContractRunTimeInDB

	history          map[string]*InvokeItem
	invokerMap       map[string]*RecycleInvokerStatus // key: address
	recycleMap       map[string]map[int64]*InvokeItem // 还在处理中的调用, address -> invoke item list,
	rewardMap        map[string]map[int64]*InvokeItem // 获奖的item
	isSending        bool

	responseCache     []*responseItem_recycle
	responseStatus    Response_RecycleContract
}


func NewRecycleContractRunTime(stp ContractManager) *RecycleContractRunTime {
	p := &RecycleContractRunTime{
		RecycleContractRunTimeInDB: RecycleContractRunTimeInDB{
			RecycleContract: *NewRecycleContract(),
			ContractRuntimeBase: *NewContractRuntimeBase(stp),
			RecycleContractRunningData: RecycleContractRunningData{},
		},
	}
	p.init()

	return p
}

func (p *RecycleContractRunTime) init() {
	p.contract = p
	p.runtime = p
	p.history = make(map[string]*InvokeItem)
	p.invokerMap = make(map[string]*RecycleInvokerStatus)
	p.recycleMap = make(map[string]map[int64]*InvokeItem)
	p.rewardMap = make(map[string]map[int64]*InvokeItem)
	p.responseHistory = make(map[int][]*InvokeItem)
}

func (p *RecycleContractRunTime) InitFromJson(content []byte, stp ContractManager) error {
	err := json.Unmarshal(content, p)
	if err != nil {
		return err
	}
	p.init()

	return nil
}

func (p *RecycleContractRunTime) InitFromDB(stp ContractManager, resv ContractDeployResvIF) error {

	err := p.ContractRuntimeBase.InitFromDB(stp, resv)
	if err != nil {
		Log.Errorf("SwapContractRuntime.InitFromDB failed, %v", err)
		return err
	}
	p.init()

	history := LoadContractInvokeHistory(stp.GetDB(), p.URL(), true, false)
	for _, v := range history {
		item, ok := v.(*SwapHistoryItem)
		if !ok {
			continue
		}

		p.loadTraderInfo(item.Address)
		p.addItem(item)
		p.history[item.InUtxo] = item
	}

	return nil
}


// 只计算在 calcAssetMerkleRoot 之前已经确定的数据，其他在广播TX之后才修改的数据暂时不要管，不然容易导致数据不一致
func CalcRecycleContractRunningDataMerkleRoot(r *RecycleContractRunningData) []byte {
	var buf []byte

	buf2 := fmt.Sprintf("%s %d %s %d ", r.AssetAmtInPool.String(), r.SatsValueInPool,
		r.TotalInputAssets.String(), r.TotalInputSats)
	buf = append(buf, buf2...)

	buf2 = fmt.Sprintf("%d %s %d ", r.TotalRewardCount, r.TotalRewardAmt.String(), r.TotalRewardValue)
	buf = append(buf, buf2...)

	Log.Debugf("RecycleContractRunningData: %s", string(buf))

	hash := chainhash.DoubleHashH(buf)
	result := hash.CloneBytes()
	Log.Debugf("hash: %s", hex.EncodeToString(result))
	return result
}


// 调用前自己加锁
func (p *RecycleContractRunTime) CalcRuntimeMerkleRoot() []byte {
	//Log.Debugf("Invoke: %d", p.InvokeCount)
	base := CalcContractRuntimeBaseMerkleRoot(&p.ContractRuntimeBase)
	running := CalcRecycleContractRunningDataMerkleRoot(&p.RecycleContractRunningData)

	buf := append(base, running...)
	hash := chainhash.DoubleHashH(buf)
	Log.Debugf("%s CalcRuntimeMerkleRoot: %d %s", p.stp.GetMode(), p.InvokeCount, hex.EncodeToString(hash.CloneBytes()))
	return hash.CloneBytes()
}

func (p *RecycleContractRunTime) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)

	if err := enc.Encode(p.RecycleContract); err != nil {
		return nil, err
	}

	if err := enc.Encode(p.ContractRuntimeBase); err != nil {
		return nil, err
	}

	if err := enc.Encode(p.RecycleContractRunningData); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (p *RecycleContractRunTime) GobDecode(data []byte) error {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)

	var recycle RecycleContract
	if err := dec.Decode(&recycle); err != nil {
		return err
	}
	p.RecycleContract = recycle

	if ContractRuntimeBaseUpgrade {
		var old ContractRuntimeBase_old
		if err := dec.Decode(&old); err != nil {
			return err
		}
		p.ContractRuntimeBase = *old.ToNewVersion()

		var old2 RecycleContractRunningData_old
		if err := dec.Decode(&old2); err != nil {
			return err
		}
		p.RecycleContractRunningData = *old2.ToNewVersion()
	} else {
		if err := dec.Decode(&p.ContractRuntimeBase); err != nil {
			return err
		}

		if err := dec.Decode(&p.RecycleContractRunningData); err != nil {
			return err
		}
	}

	return nil
}


func (p *RecycleContractRunTime) GetAssetAmount() (*Decimal, int64) {
	return p.AssetAmtInPool, p.SatsValueInPool
}


// 7. rpc接口和相关数据结构定义

func (p *RecycleContractRunTime) RuntimeContent() []byte {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	b, err := EncodeToBytes(p)
	if err != nil {
		Log.Errorf("Marshal RecycleContractRunTime failed, %v", err)
		return nil
	}
	return b
}


func (p *RecycleContractRunTime) updateResponseData() {
	if p.refreshTime == 0 {
		p.mutex.Lock()
		defer p.mutex.Unlock()

		// responseCache

		// responseStatus
		p.responseStatus.RecycleContractRunTimeInDB = &p.RecycleContractRunTimeInDB

		p.refreshTime = time.Now().Unix()
	}
}


func (p *RecycleContractRunTime) RuntimeStatus() string {

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


func (p *RecycleContractRunTime) RuntimeAnalytics() string {
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


func (p *RecycleContractRunTime) InvokeHistory(f any, start, limit int) string {
	p.updateResponseData()

	return p.GetRuntimeBase().InvokeHistory(f, start, limit)
}


type responseItem_recycle struct {
	Address     string   `json:"address"`
	Invoke      string   `json:"invoke"`
	Reward      string   `json:"reward"`
}

type Response_RecycleContract struct {
	*RecycleContractRunTimeInDB
}

func (p *RecycleContractRunTime) AllAddressInfo(start, limit int) string {

	p.updateResponseData()

	p.mutex.RLock()
	defer p.mutex.RUnlock()

	type response struct {
		Total int                  `json:"total"`
		Start int                  `json:"start"`
		Data  []*responseItem_recycle `json:"data"`
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

type Response_InvokerStatus struct {
	Statistic   *RecycleInvokerStatistic `json:"status"`
	InvokeList []string                  `json:"invokes"`
	RewardList []string                  `json:"rewards"`
}

func (p *RecycleContractRunTime) StatusByAddress(address string) (string, error) {

	//p.updateResponseData()

	p.mutex.RLock()
	defer p.mutex.RUnlock()

	

	result := &Response_InvokerStatus{}
	trader := p.loadTraderInfo(address)
	if trader != nil {
		result.Statistic = &RecycleInvokerStatistic{
			InvokeCount: trader.GetInvokeCount(),
			InvokeAmt: trader.GetInvokeAmt().String(),
			InvokeValue: trader.GetInvokeValue(),
			RewardAmt: trader.TotalRewardAmt.String(),
			RewardValue: trader.TotalRewardValue,
		}
		invokes := p.recycleMap[address]
		for _, v := range invokes {
			result.InvokeList = append(result.InvokeList, v.InUtxo)
		}
		
		rewards := p.rewardMap[address]
		for _, v := range rewards {
			result.RewardList = append(result.RewardList, v.InUtxo)
		}
	}

	buf, err := json.Marshal(result)
	if err != nil {
		Log.Errorf("Marshal trader status failed, %v", err)
		return "", err
	}

	return string(buf), nil
}

func (p *RecycleContractRunTime) GetInvokerStatus(address string) InvokerStatus {
	return p.loadTraderInfo(address)
}


func (p *RecycleContractRunTime) loadTraderInfo(address string) *RecycleInvokerStatus {
	status, ok := p.invokerMap[address]
	if ok {
		return status
	}

	r, err := loadContractInvokerStatus(p.stp.GetDB(), p.URL(), address)
	if err != nil {
		status = NewRecycleInvokerStatus(address, p.Divisibility)
	} else {
		status, ok = r.(*RecycleInvokerStatus)
		if !ok {
			status = NewRecycleInvokerStatus(address, p.Divisibility)
		}
	}

	p.invokerMap[address] = status
	return status
} 

func (p *RecycleContractRunTime) DeploySelf() bool {
	return false
}


func (p *RecycleContractRunTime) AllowDeploy() error {

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


// return fee: 调用费用+该invoke需要的聪数量
func (p *RecycleContractRunTime) CheckInvokeParam(param string) (int64, error) {
	var invoke InvokeParam
	err := json.Unmarshal([]byte(param), &invoke)
	if err != nil {
		return 0, err
	}
	//assetName := p.GetAssetName()
	templateName := p.GetTemplateName()
	switch invoke.Action {
	

	case INVOKE_API_RECYCLE:
		if templateName != TEMPLATE_CONTRACT_RECYCLE {
			return 0, fmt.Errorf("unsupport")
		}


		return 0, nil

	default:
		return 0, fmt.Errorf("unsupport action %s", invoke.Action)
	}

}

func (p *RecycleContractRunTime) processInvoke_SatsNet(data *InvokeDataInBlock_SatsNet) error {

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
			p.refreshTime = 0
			p.stp.SaveReservation(p.resv)
		}
	} else {
		//Log.Infof("%s not allowed yet, %v", p.RelativePath(), err)
	}
	return nil
}

func (p *RecycleContractRunTime) InvokeWithBlock_SatsNet(data *InvokeDataInBlock_SatsNet) error {

	err := p.ContractRuntimeBase.InvokeWithBlock_SatsNet(data)
	if err != nil {
		return err
	}

	p.mutex.Lock()
	p.processInvoke_SatsNet(data)
	p.recycle()
	p.ContractRuntimeBase.InvokeCompleted_SatsNet(data)
	p.mutex.Unlock()

	// 发送
	p.sendInvokeResultTx_SatsNet()
	return nil
}

func (p *RecycleContractRunTime) processInvoke(data *InvokeDataInBlock) error {

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
			p.refreshTime = 0
			p.stp.SaveReservation(p.resv)
		}
	} else {
		Log.Infof("%s allowInvoke failed, %v", p.URL(), err)
	}
	return nil
}

func (p *RecycleContractRunTime) InvokeWithBlock(data *InvokeDataInBlock) error {

	err := p.ContractRuntimeBase.InvokeWithBlock(data)
	if err != nil {
		return err
	}

	if p.IsActive() {
		p.mutex.Lock()
		p.processInvoke(data)
		p.recycle()
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

func (p *RecycleContractRunTime) Invoke_SatsNet(invokeTx *InvokeTx_SatsNet, height int) (InvokeHistoryItem, error) {

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
	
	case INVOKE_API_RECYCLE:
		paramBytes, err := base64.StdEncoding.DecodeString(param.Param)
		if err != nil {
			return nil, err
		}
		var invokeParam InvokeParam
		err = invokeParam.Decode(paramBytes)
		if err != nil {
			return nil, err
		}

		// 到这里，客观条件都满足了，如果还不能符合铸造条件，直接设置为无效调用
		bValid := true
		for {
			plainSats := output.GetPlainSat()
			if plainSats < 500 {
				Log.Errorf("utxo %s no enough sats to pay stake fee %d", utxo, 500)
				bValid = false
				break
			}

		}
		// 更新合约状态
		return p.updateContract(address, output, bValid), nil

	default:
		Log.Errorf("contract %s is not support action %s", url, param.Action)
		return nil, fmt.Errorf("not support action %s", param.Action)
	}
}

func (p *RecycleContractRunTime) Invoke(invokeTx *InvokeTx, height int) (InvokeHistoryItem, error) {

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
		param.Action = INVOKE_API_RECYCLE
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

	case INVOKE_API_RECYCLE: // 主网没有调用参数时的默认动作
		// 检查交换资产的数据
		output := invokeTx.TxOutput
		//assetAmt, _ := output.GetAssetV2(p.GetAssetName())

		// 到这里，客观条件都满足了，如果还不能符合铸造条件，那就需要退款
		bValid := true

		// 如果invokeParam不为nil，要检查数据是否一致
		// if param.Param != "" {
		// 	paramBytes, err := base64.StdEncoding.DecodeString(param.Param)
		// 	if err != nil {
		// 		return nil, err
		// 	}
		// 	var depositParam DepositInvokeParam
		// 	err = depositParam.Decode(paramBytes)
		// 	if err != nil {
		// 		return nil, err
		// 	}
		// 	if depositParam.AssetName != "" {
		// 		if depositParam.AssetName != p.GetAssetName().String() {
		// 			return nil, fmt.Errorf("invalid asset name %s", depositParam.AssetName)
		// 		}
		// 	}
		// 	if depositParam.Amt != "0" && depositParam.Amt != "" {
		// 		if depositParam.Amt != assetAmt.String() {
		// 			return nil, fmt.Errorf("invalid asset amt %s", depositParam.Amt)
		// 		}
		// 	}
		// }

		// 更新合约状态
		return p.updateContract(address, OutputToSatsNet(output), bValid), nil

	default:
		Log.Errorf("contract %s is not support action %s", p.URL(), param.Action)
		return nil, fmt.Errorf("not support action %s", param.Action)
	}
}


// 通用的调用参数入口
func (p *RecycleContractRunTime) updateContract(
	invoker string, output *sindexer.TxOutput, bValid bool,
) *InvokeItem {

	assetName := p.GetAssetName()
	inValue := output.GetPlainSat_Ceil()
	var inAmt *Decimal
	if !indexer.IsPlainAsset(assetName) {
		inAmt = output.GetAsset(assetName)
	}

	serviceFee := int64(0)
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

		OrderType:      ORDERTYPE_RECYCLE,
		UtxoId:         output.UtxoId,
		OrderTime:      time.Now().Unix(),
		AssetName:      assetName.String(),
		ServiceFee:     serviceFee,
		UnitPrice:      nil,
		ExpectedAmt:    nil,
		Address:        invoker,
		FromL1:         true,
		InUtxo:         output.OutPointStr,
		InValue:        inValue,
		InAmt:          inAmt,
		RemainingAmt:   remainingAmt,
		RemainingValue: remainingValue,
		ToL1:           false,
		OutAmt:         indexer.NewDecimal(0, p.Divisibility),
		OutValue:       0,
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
func (p *RecycleContractRunTime) updateContractStatus(item *SwapHistoryItem) {
	p.history[item.InUtxo] = item

	trader := p.loadTraderInfo(item.Address)
	InsertItemToTraderHistroy(&trader.InvokerStatusBaseV2, item)

	p.InvokeCount++
	p.TotalInputAssets = p.TotalInputAssets.Add(item.InAmt)
	p.TotalInputSats += item.InValue
	
	if item.Reason == INVOKE_REASON_NORMAL {
		trader.InvokeAmt = trader.InvokeAmt.Add(item.InAmt)
		trader.InvokeValue += item.InValue
	} // else 只可能是 INVOKE_REASON_INVALID 不用更新任何数据

	saveContractInvokerStatus(p.stp.GetDB(), p.URL(), trader)
	// 整体状态在外部保存
}


// 不需要写入数据库的缓存数据，不能修改任何需要保存数据库的变量
func (p *RecycleContractRunTime) addItem(item *SwapHistoryItem) {
	if item.Reason == INVOKE_REASON_NORMAL {
		switch item.OrderType {
		case ORDERTYPE_RECYCLE:
			addItemToMap(item, p.recycleMap)
		}
	} 

	p.insertBuck(item)
}


// 执行回收，每个区块统一执行一次
func (p *RecycleContractRunTime) recycle() error {

	if len(p.recycleMap) == 0 {
		return nil
	}

	Log.Debugf("%s start contract %s with action recycle %d", p.stp.GetMode(), p.URL(), len(p.recycleMap))

	url := p.URL()
	updated := false

	for _, item := range p.history {
		// 根据区块和交易，生成兑奖号码
		// 如果中奖，放入 rewardMap
		

		SaveContractInvokeHistoryItem(p.stp.GetDB(), url, item)


		updated = true

		// Log.Infof("item processed: amt=%s, value=%d, price=%s, BUY %s <-> SELL %s, ",
		// 	matchAmt.String(), matchValue, sell.UnitPrice.String(), buy.InUtxo, sell.InUtxo)
	}

	// 交易的结果先保存
	if updated {
		p.stp.SaveReservation(p.resv)
	}


	return nil
}


// 涉及发送各种tx，运行在线程中
func (p *RecycleContractRunTime) sendInvokeResultTx() error {
	return p.sendInvokeResultTx_SatsNet()
}

// 涉及发送各种tx，运行在线程中
func (p *RecycleContractRunTime) sendInvokeResultTx_SatsNet() error {
	if p.resv.LocalIsInitiator() {
		if p.isSending {
			return nil
		}
		p.isSending = true
		url := p.URL()

		err := p.reward()
		if err != nil {
			Log.Errorf("contract %s deal failed, %v", url, err)
		}

		p.isSending = false
		Log.Debugf("contract %s sendInvokeResultTx_SatsNet completed", url)
	} else {
		//Log.Infof("server: waiting the deal Tx of contract %s ", p.URL())
	}
	return nil
}


func (p *RecycleContractRunTime) updateWithDealInfo_reward(dealInfo *DealInfo) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.TotalInputAssets = p.TotalInputAssets.Add(dealInfo.TotalAmt)
	p.TotalInputSats += dealInfo.TotalValue
	p.TotalInputCount++
	p.TotalFeeValue += dealInfo.Fee

	for inUtxo := range dealInfo.SendInfo {
		item, ok := p.history[inUtxo]
		if ok {
			p.updateWithRecycleItem(item, dealInfo.SendTxIdMap[inUtxo])
		} else {
			// 不是InUtxo，而是address, 那就只有一条记录
			p.updateWithRecycleItem(item, dealInfo.TxId)
		}

	}

	p.CheckPoint = dealInfo.InvokeCount
	p.AssetMerkleRoot = dealInfo.RuntimeMerkleRoot
	p.CheckPointBlock = p.CurrBlock
	p.CheckPointBlockL1 = p.CurrBlockL1

	p.refreshTime = 0
}

func (p *RecycleContractRunTime) updateWithRecycleItem(item *SwapHistoryItem, txId string) {
	if txId == "" {
		// 异常处理
		// item.Reason = INVOKE_REASON_UTXO_NOT_FOUND
		// item.Done = DONE_CLOSED_DIRECTLY
		return
	}

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

	trader := p.invokerMap[item.Address]
	if trader != nil {
		trader.InvokeAmt = trader.InvokeAmt.Add(item.OutAmt)
		trader.InvokeValue += item.OutValue
		saveContractInvokerStatus(p.stp.GetDB(), url, trader)
	}

	items, ok := p.recycleMap[item.Address]
	if ok {
		delete(items, item.Id)
	}

	if len(items) == 0 {
		delete(p.recycleMap, item.Address)
	}
}


func (p *RecycleContractRunTime) genRewardInfo(height int) *DealInfo {

	p.mutex.RLock()
	defer p.mutex.RUnlock()

	isRune := false
	assetName := p.GetAssetName()
	isRune = assetName.Protocol == indexer.PROTOCOL_NAME_RUNES

	// 如果费率不同，优先处理高费率的交易
	var highestFeeRate int64
	for _, rewardMap := range p.rewardMap {
		for _, item := range rewardMap {
			h, _, _ := indexer.FromUtxoId(item.UtxoId)
			if h > height {
				continue
			}
			if item.Done != DONE_NOTYET || item.Reason != INVOKE_REASON_NORMAL {
				continue
			}
			highestFeeRate = max(highestFeeRate, item.UnitPrice.Int64())
		}
	}

	maxHeight := 0
	var totalValue int64
	var totalAmt *Decimal                          // 资产数量
	sendInfoMap := make(map[string]*SendAssetInfo) // key: address
	for _, rewardMap := range p.rewardMap {
		for _, item := range rewardMap {
			h, _, _ := indexer.FromUtxoId(item.UtxoId)
			if h > height {
				continue
			}
			if item.Done != DONE_NOTYET || item.Reason != INVOKE_REASON_NORMAL {
				continue
			}
			if item.UnitPrice.Int64() < highestFeeRate {
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
		FeeRate:           highestFeeRate,
	}
}

// 收到withdraw的交易，执行一层分发
func (p *RecycleContractRunTime) reward() error {

	// 发送
	if p.resv.LocalIsInitiator() {
		if len(p.rewardMap) == 0 {
			return nil
		}
		url := p.URL()

		Log.Debugf("%s start contract %s with action withdraw", p.stp.GetMode(), url)

		p.mutex.RLock()
		height := p.CurrBlock
		p.mutex.RUnlock()
		for {
			dealInfo := p.genRewardInfo(height)
			// 发送
			if len(dealInfo.SendInfo) != 0 {
				// 发送费用已经从所有参与者扣除，但如果该交易的聪资产太少，就暂时不发送，等下次
				//if dealInfo.TotalValue >= _valueLimit || len(dealInfo.SendInfo) >= _addressLimit {
				txId, fee, stubFee, err := p.sendTx(dealInfo, INVOKE_RESULT_REWARD, true, true)
				if err != nil {
					if stubFee != 0 {
						p.mutex.Lock()
						p.TotalFeeValue += stubFee
						p.mutex.Unlock()
						p.stp.SaveReservationWithLock(p.resv)
					}
					Log.Errorf("contract %s sendTx %s failed %v", url, INVOKE_RESULT_REWARD, err)
					// 下个区块再试
					return err
				}
				// 调整fee
				dealInfo.Fee = fee + stubFee
				dealInfo.TxId = txId
				// record
				p.updateWithDealInfo_reward(dealInfo)
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


func (p *RecycleContractRunTime) AllowPeerAction(action string, param any) (any, error) {

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
		if req.Action == INVOKE_API_REWARD {
			dealInfo, err := p.genRewardInfoFromReq(req)
			if err == nil {
				return dealInfo, nil
			}
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

		case INVOKE_RESULT_REWARD:
			info := p.genRewardInfo(dealInfo.Height)
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
func (p *RecycleContractRunTime) SetPeerActionResult(action string, param any) {
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
		case INVOKE_RESULT_REWARD:
			p.updateWithDealInfo_reward(dealInfo)

		default:
			return
		}

		p.stp.SaveReservationWithLock(p.resv)
		Log.Infof("%s SetPeerActionResult %s completed", p.URL(), action)
		return
	}
}


// 获取reward数据，同时做检查
func (p *RecycleContractRunTime) genRewardInfoFromReq(req *wwire.RemoteSignMoreData_Contract) (*DealInfo, error) {

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
		items, ok := p.rewardMap[destAddr]
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
