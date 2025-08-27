package wallet

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"time"

	"github.com/btcsuite/btcd/txscript"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func init() {
	gob.Register(&LaunchPoolContractRunTime{})
}

const (
	LAUNCH_POOL_MIN_RATION int = 60 // %
	LAUNCH_POOL_MAX_RATION int = 90 // %

	INVOKE_API_MINT string = "mint"
)


var (
	LAUNCHPOOL_MIN_SPENT int64 = 20 // 最小开销：发射一个tx，10聪，退款一个tx，10聪 （部署amm的开销，算在部署launchpool的开销中）
	LAUNCHPOOL_MIN_SATS int64 = 100000 // 
)

type LaunchPoolContract struct {
	ContractBase
	AssetSymbol   int32 `json:"assetSymbol,omitempty"`
	BindingSat    int   `json:"bindingSat"`    // 每一聪绑定的资产数量，每一聪携带的该资产数量，用于在一层铸造ordx资产时使用
	MintAmtPerSat int   `json:"mintAmtPerSat"` // 在二层分发时，每一聪换多少资产数量，一般MintAmtPerSat比BindingSat小10-1000倍
	Limit         int64 `json:"limit"`         // 每个地址最大铸造量，0 不限制
	MaxSupply     int64 `json:"maxSupply"`     // 最大资产供应量，
	LaunchRatio   int   `json:"launchRation"`  // 铸造量达到总量的多少比例后，自动发射，向之前所有铸造者自动转token；
	// 剩下的比例，留存在池子中当作流动性池子
	// 比例必须在LAUNCH_POOL_MIN_RATION 和 LAUNCH_POOL_MAX_RATION 之间
}

func (p *LaunchPoolContract) ToNewVersion() *LaunchPoolContract {
	return p
}

func NewLaunchPoolContract() *LaunchPoolContract {
	return &LaunchPoolContract{
		ContractBase: ContractBase{
			TemplateName: TEMPLATE_CONTRACT_LAUNCHPOOL,
		},
	}
}

func (p *LaunchPoolContract) GetContractName() string {
	return p.AssetName.String() + URL_SEPARATOR + p.TemplateName
}

func (p *LaunchPoolContract) CalcDeployFee() int64 {

	return 0
}

func (p *LaunchPoolContract) GetAssetName() *swire.AssetName {
	return &p.AssetName
}

// 发射水位：asset num
func (p *LaunchPoolContract) TotalAssetToMint() *Decimal {
	return indexer.NewDefaultDecimal(p.MaxSupply * int64(p.LaunchRatio) / 100)
}

// 铸造的总额度: asset num
func (p *LaunchPoolContract) MaxAssetToMint() *Decimal {
	return indexer.NewDefaultDecimal(p.MaxSupply * int64(LAUNCH_POOL_MAX_RATION) / 100)
}

// sat num
func (p *LaunchPoolContract) TotalSatsToMint() int64 {
	return p.TotalAssetToMint().Int64() / int64(p.MintAmtPerSat)
}

func (p *LaunchPoolContract) CheckContent() error {
	err := p.ContractBase.CheckContent()
	if err != nil {
		return err
	}

	if p.AssetName.Protocol == indexer.PROTOCOL_NAME_ORDX {
		if p.BindingSat == 0 {
			return fmt.Errorf("invalid binding sat %d", p.BindingSat)
		}
		if p.BindingSat >= 65535 {
			return fmt.Errorf("binding sat should < 65535")
		}
		if p.MaxSupply%int64(p.BindingSat) != 0 {
			return fmt.Errorf("max supply should be times of bindingSat")
		}
		if p.Limit%int64(p.BindingSat) != 0 {
			return fmt.Errorf("limit should be times of bindingSat")
		}
	}

	if p.MintAmtPerSat == 0 {
		return fmt.Errorf("invalid mint amt per sat %d", p.MintAmtPerSat)
	}

	if p.MaxSupply <= 0 {
		return fmt.Errorf("invalid max supply %d", p.MaxSupply)
	}
	if p.Limit < 0 {
		return fmt.Errorf("invalid limit %d", p.Limit)
	}
	if p.Limit > p.MaxSupply {
		return fmt.Errorf("limit %d should not larger than max supply %d", p.Limit, p.MaxSupply)
	}
	// if p.MaxSupply%p.Limit != 0 {
	// 	return fmt.Errorf("max supply should be times of limitation")
	// }
	if p.MaxSupply%int64(p.MintAmtPerSat) != 0 {
		return fmt.Errorf("max supply should be times of mintAmtPerSat")
	}
	if p.Limit%int64(p.MintAmtPerSat) != 0 {
		return fmt.Errorf("limit should be times of mintAmtPerSat")
	}

	if p.LaunchRatio < LAUNCH_POOL_MIN_RATION || p.LaunchRatio > LAUNCH_POOL_MAX_RATION {
		return fmt.Errorf("invalid launch ratio %d", p.LaunchRatio)
	}

	minSatsInPool := (p.MaxSupply * int64(p.LaunchRatio) / 100 ) / int64(p.MintAmtPerSat)
	if minSatsInPool < LAUNCHPOOL_MIN_SATS {
		return fmt.Errorf("too small sats in pool after launched, %d", minSatsInPool)
	}

	return nil
}

func (p *LaunchPoolContract) Content() string {
	b, err := json.Marshal(p)
	if err != nil {
		Log.Errorf("Marshal LaunchPoolContract failed, %v", err)
		return ""
	}
	return string(b)
}

func (p *LaunchPoolContract) Encode() ([]byte, error) {
	base, err := p.ContractBase.Encode()
	if err != nil {
		return nil, err
	}

	return stxscript.NewScriptBuilder().
		AddData(base).
		AddData([]byte(p.AssetName.String())).
		AddInt64(int64(p.AssetSymbol)).
		AddInt64(int64(p.BindingSat)).
		AddInt64(int64(p.MintAmtPerSat)).
		AddInt64(int64(p.Limit)).
		AddInt64(int64(p.MaxSupply)).
		AddInt64(int64(p.LaunchRatio)).Script()
}

func (p *LaunchPoolContract) Decode(data []byte) error {
	tokenizer := stxscript.MakeScriptTokenizer(0, data)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing base content")
	}
	base := tokenizer.Data()
	err := p.ContractBase.Decode(base)
	if err != nil {
		return err
	}

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing asset name")
	}
	name := string(tokenizer.Data())
	p.AssetName = *indexer.NewAssetNameFromString(name)
	// 检查下protocol
	if p.AssetName.Protocol != indexer.PROTOCOL_NAME_BRC20 &&
		p.AssetName.Protocol != indexer.PROTOCOL_NAME_ORDX &&
		p.AssetName.Protocol != indexer.PROTOCOL_NAME_RUNES {
		return fmt.Errorf("invalid protocol %s", p.AssetName.Protocol)
	}

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("invalid asset symbol: %v", err)
	}
	p.AssetSymbol = int32(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("invalid bindingSat: %v", err)
	}
	p.BindingSat = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("invalid mintAmtPerSat: %v", err)
	}
	p.MintAmtPerSat = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("invalid limit: %v", err)
	}
	p.Limit = (tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("invalid maxSupply: %v", err)
	}
	p.MaxSupply = (tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("invalid ratio: %v", err)
	}
	p.LaunchRatio = int(tokenizer.ExtractInt64())

	return nil
}

// 仅仅是估算，并且尽可能多预估了输入和输出
func (p *LaunchPoolContract) DeployFee(feeRate int64) int64 {
	assetName := AssetName{
		AssetName: *p.GetAssetName(),
		N:         p.BindingSat,
	}
	feeLen := 4
	var total int64
	switch assetName.Protocol {
	case indexer.PROTOCOL_NAME_ORDX:
		// deploy
		estimatedDeployFee := []int64{325, 385, 445, 505}
		total = estimatedDeployFee[feeLen-1]*feeRate + 330

		// mint
		estimatedMintFee := []int64{310, 370, 430, 490}
		total += estimatedMintFee[feeLen-1]*feeRate +
			GetBindingSatNum(indexer.NewDefaultDecimal(p.MaxSupply), assetName.N)

		// splicing-in
		total += CalcFee_SplicingIn(1, feeLen, feeRate)

		// two stub utxos
		total += 660

		// 激活tx
		total += DEFAULT_FEE_SATSNET
		// 最后还需要部署amm合约和激活tx
		total += DEFAULT_FEE_SATSNET + SWAP_INVOKE_FEE

		// deploy service fee
		total += DEFAULT_SERVICE_FEE_DEPLOY_CONTRACT

	case indexer.PROTOCOL_NAME_RUNES:
		// deploy+mint
		estimatedDeployFee := []int64{326, 384, 441, 449}
		total = estimatedDeployFee[feeLen-1]*feeRate + 660

		// splicing-in
		total += CalcFee_SplicingIn(1, feeLen, feeRate)

		// 激活tx
		total += DEFAULT_FEE_SATSNET
		// 最后还需要部署amm合约和激活tx
		total += DEFAULT_FEE_SATSNET + SWAP_INVOKE_FEE

		// deploy service fee
		total += DEFAULT_SERVICE_FEE_DEPLOY_CONTRACT
	}

	return total
}


// 仅仅是估算，并且尽可能多预估了输入和输出
func CalcFee_SplicingIn(utxoLen, feeLen int, feeRate int64) int64 {

	var weightEstimate utils.TxWeightEstimator

	// asset utxo
	for i := 0; i < utxoLen; i++ {
		weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
	}

	// fee utxo
	for i := 0; i < feeLen; i++ {
		weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
	}

	// chanpoint in and out
	weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
	weightEstimate.AddP2WSHOutput()

	// invoice
	var payload [stxscript.MaxDataCarrierSize]byte
	weightEstimate.AddOutput(payload[:])

	weightEstimate.AddP2WSHOutput() // splicing-in

	weightEstimate.AddP2TROutput()  // asset change
	weightEstimate.AddP2WSHOutput() // stub
	weightEstimate.AddP2WSHOutput() // stub
	weightEstimate.AddP2TROutput()  // fees change

	return weightEstimate.Fee(feeRate)
}


func (p *LaunchPoolContract) InvokeParam(string) string {
	var param LaunchPoolInvokeParam
	buf, err := json.Marshal(&param)
	if err != nil {
		return ""
	}
	return string(buf)
}

type LaunchPoolInstallData struct {
	DeployTickerResvId int64
	MintTickerResvId   int64

	DeployTickerTxId string
	MintTxId         string
	AnchorTxId       string // 也是deployTx for contract

	HasDeployed int
	HasMinted   int
	HasExpanded int
	HasRun      int
}

type LaunchPoolRunningData struct {
	TotalMinted       *Decimal // asset num
	TotalInvalid      int64 // sats num
	AssetAmtInPool    *Decimal // 池子中资产的数量
	SatsValueInPool   int64 // 池子中聪的数量
	TotalInputAssets  *Decimal // 所有进入池子的资产数量，指资产铸造总量
	TotalInputSats    int64 // 所有进入池子的聪数量，指聪网上每个地址参与铸造的每个TX的输入聪总量
	TotalOutputAssets *Decimal // 所有退出池子的资产数量，发射时发射出去的资产总量
	TotalOutputSats   int64 // 所有退出池子的聪数量，铸造失败退回的聪
	IsLaunching       bool
	LaunchTxIDs       []string // 发射
	RefundTxIDs       []string // 退款

	AmmContractURL string
	AmmResvId      int64
}

type MinterStatus struct {
	TotalMint   *Decimal // 有效铸造的资产数量，或者退款的聪数量
	HasLaunched bool  // 已经发射，或者已经退款
	MintHistory []*MintHistoryItem
}

type LaunchPoolInvokeParam = InvokeParam

type MintHistoryItem = InvokeItem

type LaunchPoolContractRunTimeInDB struct {
	LaunchPoolContract
	ContractRuntimeBase

	// 安装过程的状态
	LaunchPoolInstallData

	// 运行过程的状态
	LaunchPoolRunningData
}

func (p *LaunchPoolContractRunTime) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)

	if err := enc.Encode(p.LaunchPoolContract); err != nil {
		return nil, err
	}

	if err := enc.Encode(p.ContractRuntimeBase); err != nil {
		return nil, err
	}

	if err := enc.Encode(p.LaunchPoolInstallData); err != nil {
		return nil, err
	}

	if err := enc.Encode(p.LaunchPoolRunningData); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (p *LaunchPoolContractRunTime) GobDecode(data []byte) error {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)

	if err := dec.Decode(&p.LaunchPoolContract); err != nil {
		return err
	}
	if err := dec.Decode(&p.ContractRuntimeBase); err != nil {
		return err
	}

	if err := dec.Decode(&p.LaunchPoolInstallData); err != nil {
		return err
	}

	if err := dec.Decode(&p.LaunchPoolRunningData); err != nil {
		return err
	}

	return nil
}

type LaunchPoolContractRunTime struct {
	LaunchPoolContractRunTimeInDB

	mintInfoMap      map[string]*MinterStatus    // key: minter address， 缓存数据
	invalidMintMap   map[string]*MinterStatus    // key: minter address，所有无效的调用记录，每个区块发起一次退款
	deployTickerResv *InscribeResv
	isSending        bool

	refreshTime   	int64
	responseCache 	[]*responseItem_launchPool
	responseHistory []*MintHistoryItem // 按时间排序
}

func NewLaunchPoolContractRuntime(stp *Manager) *LaunchPoolContractRunTime {
	r := &LaunchPoolContractRunTime{
		LaunchPoolContractRunTimeInDB: LaunchPoolContractRunTimeInDB{
			LaunchPoolContract: *NewLaunchPoolContract(),
			ContractRuntimeBase: ContractRuntimeBase{
				DeployTime: time.Now().Unix(),
				stp:        stp,
			},
			LaunchPoolRunningData: LaunchPoolRunningData{},
		},
	}
	r.init()
	return r
}

func (p *LaunchPoolContractRunTime) init() {
	p.contract = p
	p.runtime = p
	p.mintInfoMap = make(map[string]*MinterStatus)
	p.invalidMintMap = make(map[string]*MinterStatus)
}

func (p *LaunchPoolContractRunTime) InitFromContent(content []byte, stp *Manager) error {
	err := p.ContractRuntimeBase.InitFromContent(content, stp)
	if err != nil {
		Log.Errorf("LaunchPoolContractRunTime.InitFromContent failed, %v", err)
		return err
	}
	p.Divisibility = 0
	p.N = p.BindingSat
	return nil
}


func (p *LaunchPoolContractRunTime) DeploySelf() bool {
	return true
}


func (p *LaunchPoolContractRunTime) InstallStatus() string {
	buf, err := json.Marshal(p.LaunchPoolInstallData)
	if err != nil {
		return ""
	}
	return string(buf)
}


func (p *LaunchPoolContractRunTime) RuntimeContent() []byte {
	b, err := EncodeToBytes(p)
	if err != nil {
		Log.Errorf("Marshal LaunchPoolContractRunTime failed, %v", err)
		return nil
	}
	return b
}

func (p *LaunchPoolContractRunTime) RuntimeStatus() string {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	buf, err := json.Marshal(p)
	if err != nil {
		Log.Errorf("GetContractStatus Marshal %s failed, %v", p.URL(), err)
		return ""
	}
	return string(buf)
}

type responseItem_launchPool struct {
	Address string  `json:"address"`
	Valid   string  `json:"valid"`
	Invalid string  `json:"invalid"`
}


func (p *LaunchPoolContractRunTime) CheckInvokeParam(param string) (int64, error) {
	var invoke LaunchPoolInvokeParam
	err := json.Unmarshal([]byte(param), &invoke)
	if err != nil {
		return 0, err
	}
	if invoke.Action != INVOKE_API_MINT {
		return 0, fmt.Errorf("invalid action %s", invoke.Action)
	}
	amt := string(invoke.Param)
	if amt == "" || amt == "0" {
		return 0, fmt.Errorf("should set a special amt")
	}
	
	dAmt, err := indexer.NewDecimalFromString(amt, 0)
	if err != nil {
		return 0, fmt.Errorf("invalid mint amount %s", amt)
	}
	if p.Limit > 0 && dAmt.Int64() > p.Limit {
		return 0, fmt.Errorf("mint amount %s exceed the limit %d", amt, p.Limit)
	}
	if dAmt.Int64() <= 0 {
		return 0, fmt.Errorf("invalid mint amount %s", amt)
	}
	
	// 非虚拟的运行时对象，增加实时检查
	// if p.channel != nil {
	// 	var invoke LaunchPoolInvokeParam
	// 	err = json.Unmarshal([]byte(param), &invoke)
	// 	if err != nil {
	// 		return err
	// 	}
	// 	if invoke.Param != "" && invoke.Param != "0" {
	// 		amt, err := indexer.NewDecimalFromString(invoke.Param, 0)

	// 	}
	// }

	return indexer.GetBindingSatNum(dAmt, uint32(p.MintAmtPerSat)), nil
}


// 当前能铸造的额度: asset num
func (p *LaunchPoolContractRunTime) LimitToMint() *Decimal {
	return indexer.DecimalSub(p.MaxAssetToMint(), p.TotalMinted)
}

func (p *LaunchPoolContractRunTime) LeftToMint() *Decimal {
	return indexer.DecimalSub(p.TotalAssetToMint(),  p.TotalMinted)
}
