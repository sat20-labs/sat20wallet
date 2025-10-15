package wallet

import (
	"bytes"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/btcsuite/btcd/txscript"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
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
	LAUNCHPOOL_MIN_SPENT int64 = 20     // 最小开销：发射一个tx，10聪，退款一个tx，10聪 （部署amm的开销，算在部署launchpool的开销中）
	LAUNCHPOOL_MIN_SATS  int64 = 100000 //
)

type LaunchPoolContract_old = LaunchPoolContract

// type LaunchPoolContract_old struct {
// 	ContractBase
// 	AssetSymbol   int32 `json:"assetSymbol,omitempty"`
// 	BindingSat    int   `json:"bindingSat"`      // 每一聪绑定的资产数量，每一聪携带的该资产数量，用于在一层铸造ordx资产时使用
// 	Limit         int64 `json:"limit"`        // 每个地址最大铸造量，0 不限制
// 	MaxSupply     int64 `json:"maxSupply"`    // 最大资产供应量，
// 	LaunchRatio   int   `json:"launchRation"` // 铸造量达到总量的多少比例后，自动发射，向之前所有铸造者自动转token；
// 	// 剩下的比例，留存在池子中当作流动性池子
// 	//
// }

// func (p *LaunchPoolContract_old) ToNewVersion() *LaunchPoolContract {
// 	return &LaunchPoolContract{
// 		ContractBase: p.ContractBase,
// 		AssetSymbol: p.AssetSymbol,
// 		BindingSat: p.BindingSat,
// 		MintAmtPerSat: p.BindingSat,
// 		Limit: p.Limit,
// 		MaxSupply: p.MaxSupply,
// 		LaunchRatio: p.LaunchRatio,
// 	}
// }

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

	minSatsInPool := (p.MaxSupply * int64(p.LaunchRatio) / 100) / int64(p.MintAmtPerSat)
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
		total += CalcFee_SplicingIn(1, feeLen, p.GetAssetName(), feeRate)

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
		total += CalcFee_SplicingIn(1, feeLen, p.GetAssetName(), feeRate)

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
func CalcFee_SplicingIn(utxoLen, feeLen int, assetName *indexer.AssetName, feeRate int64) int64 {

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
	var payload [txscript.MaxDataCarrierSize]byte
	weightEstimate.AddOutput(payload[:])

	weightEstimate.AddP2WSHOutput() // splicing-in
	weightEstimate.AddP2TROutput()  // asset change
	weightEstimate.AddP2TROutput()  // fees change

	moreSats := int64(0)
	n := NeedStubUtxoForChannel(assetName)
	if n > 0 {
		for range n {
			weightEstimate.AddP2WSHOutput() // stub
			moreSats += 330
		}
		if assetName.Protocol == indexer.PROTOCOL_NAME_RUNES {
			// 符文的输入聪数量，在需要余额时可能不够
			moreSats += 330
		}
	}

	return weightEstimate.Fee(feeRate) + moreSats
}

func (p *LaunchPoolContract) InvokeParam(string) string {
	var param LaunchPoolInvokeParam
	buf, err := json.Marshal(&param)
	if err != nil {
		return ""
	}
	return string(buf)
}

func (p *LaunchPoolContract) CalcStaticMerkleRoot() []byte {
	return CalcContractStaticMerkleRoot(p)
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
	TotalInvalid      int64    // sats num
	AssetAmtInPool    *Decimal // 池子中资产的数量
	SatsValueInPool   int64    // 池子中聪的数量
	TotalInputAssets  *Decimal // 所有进入池子的资产数量，指资产铸造总量
	TotalInputSats    int64    // 所有进入池子的聪数量，指聪网上每个地址参与铸造的每个TX的输入聪总量
	TotalOutputAssets *Decimal // 所有退出池子的资产数量，发射时发射出去的资产总量
	TotalOutputSats   int64    // 所有退出池子的聪数量，铸造失败退回的聪
	IsLaunching       bool
	LaunchTxIDs       []string // 发射
	RefundTxIDs       []string // 退款

	AmmContractURL string
	AmmResvId      int64
}

type MinterStatus struct {
	TotalAmt *Decimal // 有效铸造的资产数量，或者退款的聪数量
	Settled  bool     // 已经发射，或者已经退款
	History  []*MintHistoryItem
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

	if ContractRuntimeBaseUpgrade {
		var oldC LaunchPoolContract_old
		if err := dec.Decode(&oldC); err != nil {
			return err
		}
		p.LaunchPoolContract = *oldC.ToNewVersion()

		var old ContractRuntimeBase_old
		if err := dec.Decode(&old); err != nil {
			return err
		}
		p.ContractRuntimeBase = *old.ToNewVersion()
	} else {
		if err := dec.Decode(&p.LaunchPoolContract); err != nil {
			return err
		}
		if err := dec.Decode(&p.ContractRuntimeBase); err != nil {
			return err
		}
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

	mintInfoMap      map[string]*MinterStatus // key: minter address， 缓存数据
	invalidMintMap   map[string]*MinterStatus // key: minter address，所有无效的调用记录，每个区块发起一次退款
	deployTickerResv *InscribeResv
	isSending        bool

	refreshTime     int64
	responseCache   []*responseItem_launchPool
	responseHistory []*MintHistoryItem // 按时间排序
}

func NewLaunchPoolContractRuntime(stp ContractManager) *LaunchPoolContractRunTime {
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

func (p *LaunchPoolContractRunTime) InitFromContent(content []byte, stp ContractManager,
	resv ContractDeployResvIF) error {
	err := p.ContractRuntimeBase.InitFromContent(content, stp, resv)
	if err != nil {
		Log.Errorf("LaunchPoolContractRunTime.InitFromContent failed, %v", err)
		return err
	}
	p.Divisibility = 0
	p.N = p.BindingSat
	return nil
}

func (p *LaunchPoolContractRunTime) InitFromJson(content []byte, stp ContractManager) error {
	err := json.Unmarshal(content, p)
	if err != nil {
		return err
	}
	p.init()

	return nil
}

func (p *LaunchPoolContractRunTime) InitFromDB(stp ContractManager, resv ContractDeployResvIF) error {
	err := p.ContractRuntimeBase.InitFromDB(stp, resv)
	if err != nil {
		Log.Errorf("LaunchPoolContractRunTime.InitFromDB failed, %v", err)
		return err
	}
	p.init()

	// 用于调试时修改本地一些错误的数据
	// url := p.URL()
	// if url == "bc1qmgactfmdfympq5tqld7rc53y4dphvdyqnmtuuv9jwgpn7hqwr2kss26dls_ordx:f:sat20_launchpool.tc" &&
	// p.InvokeCount == 172 {
	// 	itemHistory := ``
	// 	var inputs []*MintHistoryItem
	// 	err := json.Unmarshal([]byte(itemHistory), &inputs)
	// 	if err != nil {
	// 		Log.Panic(err)
	// 	}
	// 	for _, item := range inputs {
	// 		saveContractInvokeHistoryItem(stp.GetDB(), url, item)
	// 	}
	// 	// p.calcAssetMerkleRoot()
	// 	// saveReservation(p.stp.GetDB(), resv)
	// }

	history := LoadContractInvokeHistory(stp.GetDB(), p.URL(), false, false)
	for _, v := range history {
		item, ok := v.(*MintHistoryItem)
		if !ok {
			continue
		}

		p.history[item.InUtxo] = item
		p.responseHistory = append(p.responseHistory, item)
		p.addItem(item)
	}

	p.resv = resv

	p.ResvId = resv.GetId()
	p.ChannelAddr = resv.GetChannelAddr()

	if p.DeployTickerResvId != 0 {
		p.deployTickerResv = stp.GetWalletMgr().GetInscribeResv(p.DeployTickerResvId)
	}

	// p.calcAssetMerkleRoot()
	// saveReservation(p.stp.GetDB(), resv)

	// err = p.checkSelf()
	// if err != nil {
	// 	Log.Errorf("%s checkSelf failed, %v", p.URL(), err)
	// }

	return nil
}

func (p *LaunchPoolContractRunTime) GetAssetAmount() (*Decimal, int64) {
	return p.AssetAmtInPool, p.SatsValueInPool
}

// 调用前自己加锁
func (p *LaunchPoolContractRunTime) CalcRuntimeMerkleRoot() []byte {
	base := CalcContractRuntimeBaseMerkleRoot(&p.ContractRuntimeBase)

	running := CalcLaunchPoolInstallDataMerkleRoot(&p.LaunchPoolInstallData)

	buf := append(base, running...)
	hash := chainhash.DoubleHashH(buf)
	Log.Debugf("LaunchPoolContractRunTime: %d %s", p.InvokeCount, hex.EncodeToString(hash.CloneBytes()))
	return hash.CloneBytes()
}

func (p *LaunchPoolContractRunTime) DeploySelf() bool {
	return true
}

func (p *LaunchPoolContractRunTime) AllowDeploy() error {

	// 看看对应的资产是否存在
	info := p.stp.GetTickerInfo(p.GetAssetName())
	if info != nil {
		return fmt.Errorf("ticker %s exists", p.GetAssetName().String())
	}
	err := p.stp.GetIndexerClient().AllowDeployTick(p.GetAssetName())
	if err != nil {
		return fmt.Errorf("ticker %s not allowed to deploy, %v", p.GetAssetName().String(), err)
	}

	err = p.ContractRuntimeBase.AllowDeploy()
	if err != nil {
		return err
	}

	return nil
}

func (p *LaunchPoolContractRunTime) UnconfirmedTxId() string {
	if p.HasDeployed == 1 {
		return p.DeployTickerTxId
	}
	if p.HasMinted == 1 {
		return p.MintTxId
	}
	if p.HasDeployed == 2 && p.HasMinted == 0 {
		// 部署后没有进入铸造，回退下
		p.HasDeployed = 1
		return p.DeployTickerTxId
	}
	if p.HasMinted == 2 && p.HasExpanded == 0 {
		// 铸造后没有穿越，回退下
		p.HasMinted = 1
		return p.MintTxId
	}
	return ""
}

func (p *LaunchPoolContractRunTime) UnconfirmedTxId_SatsNet() string {
	if p.HasExpanded == 1 {
		return p.AnchorTxId
	}
	return ""
}

func (p *LaunchPoolContractRunTime) GetDeployAction() *ContractDeployAction {
	if p.HasDeployed == 0 {
		return &ContractDeployAction{
			Action: deployTicker,
			Name:   "deployTicker",
		}
	}
	if p.HasDeployed == 1 || p.HasMinted == 0 {
		return &ContractDeployAction{
			Action: mintTicker,
			Name:   "mintTicker",
		}
	}
	if p.HasMinted == 1 || p.HasExpanded == 0 {
		return &ContractDeployAction{
			Action: ascendAsset,
			Name:   "ascendAsset",
		}
	}
	if p.HasExpanded == 1 || p.HasRun == 0 {
		return &ContractDeployAction{
			Action: runLaunchPoolContract,
			Name:   "runLaunchPoolContract",
		}
	}
	return nil
}

func (p *LaunchPoolContractRunTime) InstallStatus() string {
	buf, err := json.Marshal(p.LaunchPoolInstallData)
	if err != nil {
		return ""
	}
	return string(buf)
}

func (p *LaunchPoolContractRunTime) SetDeployActionResult(status []byte) {
	Log.Infof("contract %s SetDeployActionResult %s", p.URL(), string(status))

	if len(status) == 0 || string(status) == "ok" {
		return
	}

	if p.HasRun == 1 {
		return
	}

	var install LaunchPoolInstallData
	err := json.Unmarshal([]byte(status), &install)
	if err != nil {
		Log.Errorf("SetDeployActionResult Unmarshal failed, %v", err)
		return
	}

	p.DeployTickerTxId = install.DeployTickerTxId
	p.MintTxId = install.MintTxId
	p.AnchorTxId = install.AnchorTxId

	if p.DeployTickerTxId != "" {
		if p.HasDeployed != 2 {
			if p.stp.GetIndexerClient().IsTxConfirmed(p.DeployTickerTxId) {
				p.HasDeployed = 2
			} else {
				p.HasDeployed = 1
			}
		}
		p.Status = CONTRACT_STATUS_INIT + 1
	}

	if p.MintTxId != "" {
		if p.HasMinted != 2 {
			if p.stp.GetIndexerClient().IsTxConfirmed(p.MintTxId) {
				p.HasMinted = 2
			} else {
				p.HasMinted = 1
			}
		}
		p.Status = CONTRACT_STATUS_INIT + 2
	}

	if p.AnchorTxId != "" {
		if p.HasExpanded != 2 {
			// 不要在这里设置HasExpanded = 2，统一由runLaunchPoolContract 设置
			// if p.stp.GetIndexerClient_SatsNet().IsTxConfirmed(p.AnchorTxId) {
			// 	p.HasExpanded = 2
			// 	p.resv.HasSentDeployTx = 2
			// } else {
			p.HasExpanded = 1
			p.resv.SetHasSentDeployTx(1)
			//}
		}
		p.Status = CONTRACT_STATUS_INIT + 3
		p.resv.SetDeployContractTxId(p.AnchorTxId)
	}

	// 不要在这里设置ready状态，统一由runLaunchPoolContract 设置
	// if p.HasExpanded == 2 {
	// 	p.HasRun = 1
	// 	p.Status = CONTRACT_STATUS_READY
	// }

	p.resv.SetStatus(RS_DEPLOY_CONTRACT_INSTALL_STARTED + ResvStatus(p.GetStatus()))
}

func doNothing(ContractManager, ContractDeployResvIF, any) (any, error) {
	return nil, nil
}

func deployTicker(stp ContractManager, resv ContractDeployResvIF, _ any) (any, error) {
	contract, ok := resv.GetContract().(*LaunchPoolContractRunTime)
	if !ok {
		return nil, fmt.Errorf("not LaunchPoolContractRunTime")
	}

	mutex := resv.GetMutex()

	mutex.Lock()
	defer mutex.Unlock()

	if resv.LocalIsInitiator() {
		if contract.HasDeployed == 0 {

			switch contract.AssetName.Protocol {
			case indexer.PROTOCOL_NAME_ORDX: // 一次性完成
				inscribeResv, err := stp.GetWalletMgr().DeployOrdxTicker(contract.AssetName.Ticker,
					contract.MaxSupply, contract.MaxSupply, int(contract.BindingSat))
				if err != nil {
					Log.Errorf("deployOrdxTicker %s faied, %v", contract.AssetName, err)
					return nil, err
				}
				Log.Infof("deployOrdxTicker %s return %s", contract.AssetName, inscribeResv.RevealTx.TxID())
				contract.DeployTickerResvId = inscribeResv.Id
				contract.deployTickerResv = inscribeResv
				contract.DeployTickerTxId = contract.deployTickerResv.RevealTx.TxID()

			case indexer.PROTOCOL_NAME_RUNES:
				// 符文部署比较特殊，在一个commitTx提交名字，这里就当作部署好了；然后真正部署时，包括了预挖，所以当作mint。
				inscribeResv, err := stp.GetWalletMgr().DeployRunesTicker(contract.Address(), contract.AssetName.Ticker, contract.AssetSymbol,
					contract.MaxSupply)
				if err != nil {
					Log.Errorf("DeployRunesTicker %s faied, %v", contract.AssetName, err)
					return nil, err
				}
				contract.DeployTickerResvId = inscribeResv.Id
				contract.deployTickerResv = inscribeResv
				Log.Infof("DeployRunesTicker %s return %s", contract.AssetName, inscribeResv.CommitTx.TxID())
				contract.DeployTickerTxId = contract.deployTickerResv.CommitTx.TxID()
			}

			contract.Status = (CONTRACT_STATUS_INIT + 1)
			contract.HasDeployed = 1
			stp.SendMessageToUpper(MSG_DEPLOY, contract.DeployTickerTxId)

		}
	} else {
		Log.Infof("Contract %s in server node enter %d", contract.URL(), contract.GetStatus())
	}

	if contract.DeployTickerTxId != "" {
		resv.SetStatus(RS_DEPLOY_CONTRACT_INSTALL_STARTED + ResvStatus(contract.GetStatus()))
		stp.SaveReservation(resv)
	}

	return contract.InstallStatus(), nil
}

func mintTicker(stp ContractManager, resv ContractDeployResvIF, param any) (any, error) {
	contract, ok := resv.GetContract().(*LaunchPoolContractRunTime)
	if !ok {
		return nil, fmt.Errorf("not LaunchPoolContractRunTime")
	}
	txId, ok := param.(string)
	if !ok {
		return nil, fmt.Errorf("param not string")
	}

	mutex := resv.GetMutex()
	mutex.Lock()
	defer mutex.Unlock()

	if txId != contract.DeployTickerTxId {
		return nil, fmt.Errorf("not deploy ticker txId")
	}

	if resv.LocalIsInitiator() {
		if contract.HasMinted == 0 {

			var mintTxId string
			switch contract.AssetName.Protocol {
			case indexer.PROTOCOL_NAME_ORDX:
				tickerInfo := stp.GetTickerInfo(contract.GetAssetName())
				if tickerInfo == nil {
					return nil, fmt.Errorf("can't get ticker %s info", contract.GetAssetName().String())
				}
				if contract.deployTickerResv != nil {
					// 已经关闭的话就不会加载
					contract.deployTickerResv.Status = (RS_CLOSED)
					SaveInscribeResv(stp.GetDB(), contract.deployTickerResv)
				}
				mintResv, err := stp.GetWalletMgr().MintOrdxAsset(contract.Address(), tickerInfo,
					contract.MaxSupply, fmt.Sprintf("%s:0", contract.DeployTickerTxId))
				if err != nil {
					Log.Errorf("mintOrdxAsset %s faied, %v", contract.AssetName, err)
					return nil, err
				}
				mintResv.Status = (RS_CLOSED)
				SaveInscribeResv(stp.GetDB(), mintResv) // 发送成功，就不再保持这个resv

				contract.HasDeployed = 2
				contract.MintTickerResvId = mintResv.Id
				mintTxId = mintResv.RevealTx.TxID()
				Log.Infof("mintOrdxAsset %s return txId %s", contract.AssetName, mintTxId)

			case indexer.PROTOCOL_NAME_RUNES:
				// 获取确认数
				txInfo, err := stp.GetIndexerClient().GetTxInfo(contract.DeployTickerTxId)
				if err != nil {
					Log.Errorf("GetTxInfo %s faied, %v", contract.DeployTickerTxId, err)
					return nil, err
				}
				if txInfo.Confirmations > 6 {
					mintTxId, err = stp.BroadcastTx(contract.deployTickerResv.RevealTx)
					if err != nil {
						Log.Errorf("BroadcastTx reveal tx %s failed, %v", contract.deployTickerResv.RevealTx.TxID(), err)
						return nil, err
					}
					Log.Infof("rune reveal txid: %s", mintTxId)

					contract.HasDeployed = 2
					contract.MintTickerResvId = contract.DeployTickerResvId
					contract.deployTickerResv.Status = (RS_CLOSED)
					SaveInscribeResv(stp.GetDB(), contract.deployTickerResv) // 发送成功，就不再保持这个resv
				} else {
					Log.Infof("tx %s has not confirmed 6 times", contract.DeployTickerTxId)
					return nil, fmt.Errorf("not reach 6 confirmations")
				}

			}

			contract.Status = (CONTRACT_STATUS_INIT + 2)
			contract.HasMinted = 1
			contract.MintTxId = mintTxId
			stp.SendMessageToUpper(MSG_MINT, mintTxId)
		}
	} else {
		Log.Infof("Contract %s in server node enter %d", contract.URL(), contract.GetStatus())
		contract.HasDeployed = 2
	}

	resv.SetStatus(RS_DEPLOY_CONTRACT_INSTALL_STARTED + ResvStatus(contract.GetStatus()))
	stp.SaveReservation(resv)

	return contract.InstallStatus(), nil
}

func ascendAsset(stp ContractManager, resv ContractDeployResvIF, param any) (any, error) {
	contract, ok := resv.GetContract().(*LaunchPoolContractRunTime)
	if !ok {
		return nil, fmt.Errorf("not LaunchPoolContractRunTime")
	}
	txId, ok := param.(string)
	if !ok {
		return nil, fmt.Errorf("param not string")
	}

	mutex := resv.GetMutex()
	mutex.Lock()
	defer mutex.Unlock()

	if txId != contract.MintTxId {
		return nil, fmt.Errorf("not mint ticker txId")
	}

	if resv.LocalIsInitiator() {
		if contract.HasExpanded == 0 {
			// 这个才将合约的内容加上
			signedContract, err := resv.SignedDeployContractInvoice()
			if err != nil {
				Log.Errorf("DeployContractScript failed. %v", err)
				return nil, err
			}
			nullDataScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_DEPLOYCONTRACT, signedContract)
			if err != nil {
				return nil, fmt.Errorf("NullDataScript failed. %v", err)
			}

			utxo := contract.MintTxId + ":0"
			anchorTxId, err := stp.AscendAssetInCoreChannel(contract.GetAssetName().String(), utxo, nullDataScript)
			if err != nil {
				Log.Errorf("AscendAsset %s failed, %v", utxo, err)
				return nil, err
			}

			// anchorTxId, _, _, err := stp.ExpandChannel(daoChannel.ChannelId,
			// 	contract.GetAssetName().String(), utxo, false, nullDataScript)
			// if err != nil {
			// 	Log.Errorf("ExpandChannel %s failed, %v", daoChannel.ChannelId, err)
			// 	return nil, err
			// }
			Log.Infof("ExpandChannel %s return txId %s", contract.GetAssetName().String(), anchorTxId)

			contract.Status = (CONTRACT_STATUS_INIT + 3)
			contract.HasExpanded = 1
			contract.AnchorTxId = anchorTxId
			resv.SetDeployContractTxId(anchorTxId)
			resv.SetHasSentDeployTx(1)
		}
		contract.HasMinted = 2
	} else {
		// 服务端只等待开始运行
		Log.Infof("Contract %s in server node enter %d", contract.URL(), contract.GetStatus())
		contract.HasMinted = 2
	}

	resv.SetStatus(RS_DEPLOY_CONTRACT_INSTALL_STARTED + ResvStatus(contract.GetStatus()))
	stp.SaveReservation(resv)

	return contract.InstallStatus(), nil
}

func runLaunchPoolContract(stp ContractManager, resv ContractDeployResvIF, param any) (any, error) {
	contract, ok := resv.GetContract().(*LaunchPoolContractRunTime)
	if !ok {
		return nil, fmt.Errorf("not LaunchPoolContractRunTime")
	}

	if contract.HasRun != 0 {
		Log.Warningf("contract %s is running", contract.URL())
		return nil, nil
	}

	txId, ok := param.(string)
	if !ok {
		return nil, fmt.Errorf("param not string")
	}

	mutex := resv.GetMutex()
	mutex.Lock()
	defer mutex.Unlock()

	if txId != contract.AnchorTxId {
		return nil, fmt.Errorf("not anchor txId")
	}

	// 同时检查并且启动contract
	if contract.HasRun == 0 {
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

		contract.HasExpanded = 2
		err := contract.IsReadyToRun(resv.GetDeployContractTx())
		if err != nil {
			contract.HasExpanded = 1
			return nil, err
		}

		// 服务端也准备好了，可以启动contract

		Log.Infof("%s contract %s is ready now.", stp.GetMode(), contract.URL())
		contract.Status = (CONTRACT_STATUS_READY)
		contract.HasRun = 1
		// 更新资产统计
		contract.TotalInputAssets = indexer.NewDefaultDecimal(contract.MaxSupply)
		contract.TotalInputSats = 0
		contract.AssetAmtInPool = contract.TotalInputAssets
		contract.SatsValueInPool = 0
	}

	contract.HasExpanded = 2
	resv.SetHasSentDeployTx(2)
	if contract.HasRun == 1 {
		resv.SetStatus(RS_DEPLOY_CONTRACT_RUNNING)
		stp.SaveReservation(resv)
	}

	return contract.InstallStatus(), nil
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

func (p *LaunchPoolContractRunTime) InvokeHistory(_ any, start, limit int) string {
	type response struct {
		Total int                `json:"total"`
		Start int                `json:"start"`
		Data  []*MintHistoryItem `json:"data"`
	}

	result := &response{
		Total: len(p.responseHistory),
		Start: start,
	}
	if start >= 0 && start < len(p.responseHistory) {
		if limit <= 0 {
			limit = 100
		}
		end := start + limit
		if end > len(p.responseHistory) {
			end = len(p.responseHistory)
		}
		result.Data = p.responseHistory[start:end]
	}

	buf, err := json.Marshal(result)
	if err != nil {
		Log.Errorf("Marshal InvokeHistory failed, %v", err)
		return ""
	}
	return string(buf)
}

type responseItem_launchPool struct {
	Address string `json:"address"`
	Valid   string `json:"valid"`
	Invalid string `json:"invalid"`
}

func (p *LaunchPoolContractRunTime) AllAddressInfo(start, limit int) string {

	if p.refreshTime == 0 {
		p.mutex.Lock()
		addressmap := make(map[string]*responseItem_launchPool)
		for k, v := range p.mintInfoMap {
			addressmap[k] = &responseItem_launchPool{
				Address: k,
				Valid:   v.TotalAmt.String(),
				Invalid: "0",
			}
		}
		for k, v := range p.invalidMintMap {
			addr, ok := addressmap[k]
			if ok {
				addr.Invalid = v.TotalAmt.String()
			} else {
				addressmap[k] = &responseItem_launchPool{
					Address: k,
					Valid:   "0",
					Invalid: v.TotalAmt.String(),
				}
			}
		}

		p.responseCache = make([]*responseItem_launchPool, 0, len(addressmap))
		for _, v := range addressmap {
			p.responseCache = append(p.responseCache, v)
		}

		sort.Slice(p.responseCache, func(i, j int) bool {
			if p.responseCache[i].Valid == p.responseCache[j].Valid {
				return p.responseCache[i].Address < p.responseCache[j].Address
			}
			return p.responseCache[i].Valid > p.responseCache[j].Valid
		})
		p.refreshTime = time.Now().Unix()
		p.mutex.Unlock()
	}

	p.mutex.RLock()
	defer p.mutex.RUnlock()

	type response struct {
		Total int                        `json:"total"`
		Start int                        `json:"start"`
		Data  []*responseItem_launchPool `json:"data"`
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
		Log.Errorf("Marshal LaunchPoolContractRunTime failed, %v", err)
		return ""
	}
	return string(buf)
}

func (p *LaunchPoolContractRunTime) StatusByAddress(address string) (string, error) {
	type response struct {
		Valid   *MinterStatus `json:"valid"`
		Invalid *MinterStatus `json:"invalid"`
	}

	result := &response{}
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	valid, ok := p.mintInfoMap[address]
	if ok {
		result.Valid = valid
	}
	invalid, ok := p.invalidMintMap[address]
	if ok {
		result.Invalid = invalid
	}

	buf, err := json.Marshal(result)
	if err != nil {
		Log.Errorf("Marshal LaunchPoolContractRunTime failed, %v", err)
		return "", err
	}

	return string(buf), nil
}

func (p *LaunchPoolContractRunTime) IsReadyToRun(deployTx *swire.MsgTx) error {

	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.HasDeployed != 2 {
		return fmt.Errorf("no deploy ticker")
	}
	if p.HasMinted != 2 {
		return fmt.Errorf("no mint ticker")
	}
	if p.HasExpanded != 2 {
		return fmt.Errorf("no transcend to satsnet")
	}
	if deployTx == nil {
		return fmt.Errorf("no deploy TX")
	}

	output, _, err := p.ContractRuntimeBase.CheckDeployTx(deployTx)
	if err != nil {
		return err
	}

	p.ChannelAddr = p.resv.GetChannelAddr()

	// utxo是否有指定资产
	if len(output.Assets) == 0 {
		return fmt.Errorf("invalid contract output asset")
	}
	asset, err := output.Assets.Find(p.GetAssetName())
	if err != nil {
		return fmt.Errorf("invalid contract output asset")
	}
	if asset.Amount.Int64() < p.MaxSupply {
		return fmt.Errorf("invalid contract output asset amount %s", asset.Amount.String())
	}

	// 交易是否已经完成
	// 被接受就很快打包，可以不做这个判断

	return nil
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

func (p *LaunchPoolContractRunTime) InvokeWithBlock_SatsNet(data *InvokeDataInBlock_SatsNet) error {

	err := p.ContractRuntimeBase.InvokeWithBlock_SatsNet(data)
	if err != nil {
		return err
	}

	p.mutex.Lock()

	err = p.AllowInvoke()
	if err == nil {

		bUpdate := false
		for _, tx := range data.InvokeTxVect {
			// 每个tx最多只对应一个合约调用
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
		}
		p.ContractRuntimeBase.InvokeCompleted_SatsNet(data)
	} else {
		Log.Infof("allowInvoke failed, %v", err)
		p.ContractRuntimeBase.InvokeCompleted_SatsNet(data)
	}

	// 是否准备发射？
	if p.ReadyToLaunch() &&
		(p.Status == CONTRACT_STATUS_READY || p.Status == CONTRACT_STATUS_CLOSING) {
		p.Status = CONTRACT_STATUS_CLOSING
		if p.CheckPointBlock == p.EnableBlock {
			// 截止到这里，后续其他invoke都无效
			p.CheckPointBlock = data.Height
			Log.Infof("%s checkpoint %d", p.URL(), p.CheckPoint)
		}
		p.stp.SaveReservationWithLock(p.resv)
		p.mutex.Unlock()

		delayLaunch := 10
		if IsTestNet() {
			delayLaunch = 1
		}
		if p.CheckPointBlock+delayLaunch <= data.Height {
			// 进入之前先解锁
			p.IsLaunching = true
			p.launch()
		}
	} else {
		p.mutex.Unlock()
	}

	return nil
}

func (p *LaunchPoolContractRunTime) InvokeWithBlock(data *InvokeDataInBlock) error {

	err := p.ContractRuntimeBase.InvokeWithBlock(data)
	if err != nil {
		return err
	}
	p.ContractRuntimeBase.InvokeCompleted(data)
	return nil
}

// 当前能铸造的额度: asset num
func (p *LaunchPoolContractRunTime) LimitToMint() *Decimal {
	return indexer.DecimalSub(p.MaxAssetToMint(), p.TotalMinted)
}

func (p *LaunchPoolContractRunTime) Invoke(invokeTx *InvokeTx, height int) (InvokeHistoryItem, error) {
	return nil, nil
}

func (p *LaunchPoolContractRunTime) Invoke_SatsNet(invokeTx *InvokeTx_SatsNet, height int) (InvokeHistoryItem, error) {

	invokeData := invokeTx.InvokeParam
	//output := invokeTx.Tx.TxOut[invokeTx.InvokeVout]
	output := sindexer.GenerateTxOutput(invokeTx.Tx, invokeTx.InvokeVout)

	var param LaunchPoolInvokeParam
	err := param.Decode(invokeData.InvokeParam)
	if err != nil {
		return nil, err
	}
	if param.Action != INVOKE_API_MINT {
		return nil, fmt.Errorf("invalid action %s", param.Action)
	}

	value := output.GetPlainSat()
	if value == 0 {
		return nil, fmt.Errorf("invalid plain sats 0")
	}

	utxoId := indexer.ToUtxoId(height, invokeTx.TxIndex, invokeTx.InvokeVout)
	output.UtxoId = utxoId
	var amt *Decimal
	amtParam := string(param.Param)
	if amtParam == "0" || amtParam == "" {
		amt = indexer.NewDefaultDecimal(value * int64(p.MintAmtPerSat))
	} else {
		// 聪网上必须设置amt
		amt, err = indexer.NewDecimalFromString(amtParam, 0)
		if err != nil {
			return nil, fmt.Errorf("invalid contract amt %s", amtParam)
		}
		if value < indexer.GetBindingSatNum(amt, uint32(p.MintAmtPerSat)) {
			return nil, fmt.Errorf("contract amt %s too large", amtParam)
		}
	}

	// 是否没有重复交易提交
	utxo := fmt.Sprintf("%s:%d", invokeTx.Tx.TxID(), invokeTx.InvokeVout)
	org, ok := p.history[utxo]
	if ok {
		org.UtxoId = utxoId
		return nil, fmt.Errorf("contract utxo %s has been handled", utxo)
	}

	// 交易是否已经完成
	// 被接受就很快打包，可以不做这个判断

	// 到这里，客观条件都满足了，如果还不能符合铸造条件，那就需要退款

	refundValue := int64(0)
	for {
		// 1. 合约状态是否正常 （在运行过程中，因为满足条件，从而开始关闭合约）
		status := p.GetStatus()
		if status != CONTRACT_STATUS_READY {
			if status >= CONTRACT_STATUS_CLOSING {
				Log.Errorf("contract is closing: %s", utxo)
				refundValue = value
				amt.SetValue(0)
				break
			}
		}

		// 2. 是否该地址还有额度，是否超过池子总额度
		var userLimit int64
		if p.Limit == 0 {
			userLimit = p.LimitToMint().Int64()
		} else {
			userLimit = min(p.Limit, p.LimitToMint().Int64())
			address := HexPubKeyToP2TRAddress(invokeData.PubKey)
			info, ok := p.mintInfoMap[address]
			if ok {
				userLimit = min(userLimit, p.Limit-info.TotalAmt.Int64())
			}
		}

		if amt.Int64() > userLimit {
			// 需要退款一部分
			amt.SetValue(userLimit)
			refundValue = value - indexer.GetBindingSatNumV2(userLimit, uint32(p.MintAmtPerSat))
		}

		break
	}

	// 更新合约状态
	return p.updateContract(invokeTx.Invoker, output, value, amt, refundValue), nil
}

func (p *LaunchPoolContractRunTime) addItem(item *MintHistoryItem) {
	address := item.Address
	amt := item.OutAmt
	if amt.Sign() != 0 {
		info, ok := p.mintInfoMap[address]
		if ok {
			info.TotalAmt = info.TotalAmt.Add(amt)
			info.History = append(info.History, item)
		} else {
			info = &MinterStatus{
				TotalAmt: amt,
				Settled:  item.Done != DONE_NOTYET,
				History:  []*MintHistoryItem{item},
			}
			p.mintInfoMap[address] = info
		}
	}

	if item.OutValue != 0 {
		info, ok := p.invalidMintMap[address]
		if ok {
			info.TotalAmt = info.TotalAmt.Add(indexer.NewDefaultDecimal(item.OutValue))
			info.History = append(info.History, item)
		} else {
			info = &MinterStatus{
				TotalAmt: indexer.NewDefaultDecimal(item.OutValue),
				Settled:  item.Done != DONE_NOTYET, // sendback
				History:  []*MintHistoryItem{item},
			}
			p.invalidMintMap[address] = info
		}
	}
}

func (p *LaunchPoolContractRunTime) DisableItem(input InvokeHistoryItem) {
	item, ok := input.(*MintHistoryItem)
	if !ok {
		return
	}
	p.TotalInputSats -= item.InValue
	p.SatsValueInPool -= item.InValue
	if item.OutAmt.Sign() > 0 {
		p.TotalMinted = p.TotalMinted.Sub(item.OutAmt)
	}
}

func (p *LaunchPoolContractRunTime) updateContract(
	invokerAddr string, output *sindexer.TxOutput,
	value int64, amt *Decimal, refundValue int64) *MintHistoryItem {

	item := &MintHistoryItem{
		InvokeHistoryItemBase: InvokeHistoryItemBase{
			Version: 1,
			Id:      p.InvokeCount,
		},
		OrderType:  ORDERTYPE_MINT,
		UtxoId:     output.UtxoId,
		OrderTime:  time.Now().Unix(),
		AssetName:  p.GetAssetName().String(),
		ServiceFee: 0,
		Address:    invokerAddr,
		FromL1:     false,
		InUtxo:     output.OutPointStr,
		InValue:    value,
		ToL1:       false,
		OutAmt:     amt,
		OutValue:   refundValue,
	}
	p.InvokeCount++
	p.TotalInputSats += value
	p.SatsValueInPool += value
	if amt.Sign() > 0 {
		p.TotalMinted = p.TotalMinted.Add(amt)
	}
	if refundValue != 0 {
		p.TotalInvalid += refundValue
	}
	p.history[item.InUtxo] = item
	p.responseHistory = append(p.responseHistory, item)
	p.addItem(item)
	SaveContractInvokeHistoryItem(p.stp.GetDB(), p.URL(), item)
	return item
}

func (p *LaunchPoolContractRunTime) ReadyToLaunch() bool {
	return p.LeftToMint().Sign() <= 0 || p.IsExpired()
}

// 设置所有投入为清退
func (p *LaunchPoolContractRunTime) setToRefundAll() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if len(p.mintInfoMap) == 0 {
		// 已经处理过
		return
	}

	p.mintInfoMap = make(map[string]*MinterStatus)
	p.invalidMintMap = make(map[string]*MinterStatus)

	totalAmt := p.LaunchPoolRunningData.TotalInputAssets.Clone()
	p.LaunchPoolRunningData = LaunchPoolRunningData{}
	p.LaunchPoolRunningData.TotalInputAssets = totalAmt.Clone()
	p.LaunchPoolRunningData.AssetAmtInPool = totalAmt
	for _, item := range p.history {
		p.TotalInputSats += item.InValue
		p.TotalInvalid += item.InValue
		p.SatsValueInPool += item.InValue
		item.OutAmt = nil
		item.OutValue = item.InValue
		p.addItem(item)
		SaveContractInvokeHistoryItem(p.stp.GetDB(), p.URL(), item)
	}

	// 扣除服务费和网络费
	for _, v := range p.invalidMintMap {
		v.TotalAmt = v.TotalAmt.Sub(indexer.NewDefaultDecimal(INVOKE_FEE))
		if v.TotalAmt.Sign() < 0 {
			v.TotalAmt = nil
		}
	}
}

// 执行发射，并关闭合约，支持重入
func (p *LaunchPoolContractRunTime) launch() error {
	// 有调用发送的接口，最好不要整个函数都锁，容易死锁
	// p.mutex.Lock()
	// defer p.mutex.Unlock()

	// 如果不满足发射条件，并且超时，全部退款
	if p.LeftToMint().Sign() > 0 && p.IsExpired() {
		p.setToRefundAll()
	}

	if len(p.invalidMintMap) != 0 {
		err := p.refund()
		if err != nil {
			Log.Errorf("refund contract %s failed, %v", p.URL(), err)
			return err
		}
		Log.Infof("contract %s refunded", p.URL())
	}

	if p.resv.LocalIsInitiator() {
		// 组装好所有的tx，然后
		// 分批发射，每次1000个输出，然后记录

		if p.isSending {
			return nil
		}
		p.isSending = true

		Log.Infof("%s start launch contract %s", p.stp.GetMode(), p.URL())

		type addrvalue struct {
			address string
			amt     string
		}

		p.mutex.RLock()
		invokeCount := p.InvokeCount
		height := p.CurrBlock
		launchAddresses := make([]*addrvalue, 0, len(p.mintInfoMap))
		for k, v := range p.mintInfoMap {
			if v.Settled {
				continue
			}

			launchAddresses = append(launchAddresses, &addrvalue{
				address: k,
				amt:     v.TotalAmt.String(),
			})
		}
		p.mutex.RUnlock()

		if len(launchAddresses) != 0 {
			sort.Slice(launchAddresses, func(i, j int) bool {
				return launchAddresses[i].address < launchAddresses[j].address
			})

			var destAddr []string
			var destAmt []string
			for i, out := range launchAddresses {
				destAddr = append(destAddr, string(out.address))
				destAmt = append(destAmt, out.amt)
				if (i+1)%1000 == 0 || (i+1) == len(launchAddresses) {
					// 每个tx最多1000个输出
					invoice, _ := UnsignedContractResultInvoice(p.URL(),
						INVOKE_RESULT_OK, fmt.Sprintf("%d", height))
					nullDataScript, _ := sindexer.NullDataScript(
						sindexer.CONTENT_TYPE_INVOKERESULT, invoice)

					var txId string
					var err error
					for i := 0; i < 3; i++ {
						txId, err = p.stp.CoBatchSend_SatsNet(destAddr, p.GetAssetName().String(),
							destAmt, "contract", p.URL(), invokeCount, nullDataScript, p.StaticMerkleRoot, p.CurrAssetMerkleRoot)
						if err != nil {
							Log.Infof("launch contract %s CoBatchSend_SatsNet failed %v, wait a second and try again", p.URL(), err)
							// 服务端可能还没有同步到数据，需要多尝试几次，但不要卡太久
							// time.Sleep(2 * time.Second)
							continue
						}
						Log.Infof("launch contract %s with txId %s", p.URL(), txId)
						break
					}
					if err != nil {
						Log.Errorf("launch contract %s CoBatchSend_SatsNet failed %v,  try again later", p.URL(), err)
						// 下个区块再试
						p.isSending = false
						return err
					}

					p.mutex.Lock()
					// record
					var totalOutputAssetAmt *Decimal
					for _, addr := range destAddr {
						info := p.mintInfoMap[addr]
						info.Settled = true
						totalOutputAssetAmt = totalOutputAssetAmt.Add(info.TotalAmt)
						for _, u := range info.History {
							u.Done = DONE_DEALT
							u.OutTxId = txId
							SaveContractInvokeHistoryItem(p.stp.GetDB(), p.URL(), u)
						}
					}
					p.TotalOutputAssets = p.TotalOutputAssets.Add(totalOutputAssetAmt)
					p.AssetAmtInPool = p.AssetAmtInPool.Sub(totalOutputAssetAmt)
					p.SatsValueInPool -= DEFAULT_FEE_SATSNET
					p.LaunchTxIDs = append(p.LaunchTxIDs, txId)
					p.mutex.Unlock()
					p.stp.SaveReservation(p.resv)

					destAddr = nil
					destAmt = nil
				}
			}
		}

		if p.AmmResvId == 0 && len(p.mintInfoMap) > 0 {
			// deploy an amm contract
			ammURL, id, err := p.deployAmmContract()
			if err != nil {
				// 下次再试
				p.isSending = false
				Log.Errorf("%s deployAmmContract failed, %v", p.URL(), err)
				return err
			}

			p.mutex.Lock()
			p.AmmResvId = id
			p.AmmContractURL = ammURL
			p.mutex.Unlock()
		}

		p.mutex.Lock()
		p.isSending = false
		p.IsLaunching = false
		p.Status = CONTRACT_STATUS_CLOSED
		p.resv.SetStatus(RS_DEPLOY_CONTRACT_COMPLETED)
		p.mutex.Unlock()
		p.stp.SaveReservation(p.resv)
		Log.Infof("contract %s closed", p.URL())

	} else {
		// 在 HandleUnlockReq 等待peer的操作
		Log.Infof("server: waiting the close of contract %s ", p.URL())
	}

	return nil
}

func (p *LaunchPoolContractRunTime) deployAmmContract() (string, int64, error) {
	assetName := p.GetAssetName()

	ammContract := NewAmmContractRuntime(p.stp)
	c := ammContract.Contract.(*AmmContract)
	c.AssetName = *assetName
	c.AssetAmt = p.AssetAmtInPool.String()
	c.SatValue = p.SatsValueInPool
	c.K = indexer.DecimalMul(p.AssetAmtInPool, indexer.NewDefaultDecimal(p.SatsValueInPool)).String()

	txId, id, err := p.stp.DeployContract(ammContract.GetTemplateName(),
		string(ammContract.Content()), nil, 0, p.Deployer)
	if err != nil {
		Log.Errorf("%s DeployContract %s failed, %v", p.URL(), ammContract.GetContractName(), err)
		return "", 0, err
	}
	Log.Infof("%s deploy an AMM contract %s, txId %s, resvId %d", p.URL(), ammContract.GetContractName(), txId, id)

	resv := p.stp.GetDeployReservation(id)
	if resv == nil {
		return "", id, nil
	}

	return resv.GetContract().URL(), id, nil
}

// 执行退款
func (p *LaunchPoolContractRunTime) refund() error {

	if p.resv.LocalIsInitiator() {
		// 组装好所有的tx，然后
		// 分批发射，每次1000个输出，然后记录
		if p.isSending {
			return nil
		}
		p.isSending = true

		Log.Infof("%s start refund contract %s", p.stp.GetMode(), p.URL())

		type addrvalue struct {
			address string
			amt     string
		}

		p.mutex.RLock()
		invokeCount := p.InvokeCount
		height := p.CurrBlock
		refundAddresses := make([]*addrvalue, 0, len(p.invalidMintMap))
		for k, v := range p.invalidMintMap {
			if v.Settled { // 已经退款
				continue
			}

			// TODO 如何支付网络费用
			refundAddresses = append(refundAddresses, &addrvalue{
				address: k,
				amt:     v.TotalAmt.String(),
			})
		}
		p.mutex.RUnlock()

		if len(refundAddresses) == 0 {
			p.isSending = false
			Log.Infof("no refund addresses for contract %s", p.URL())
			return nil
		}

		sort.Slice(refundAddresses, func(i, j int) bool {
			return refundAddresses[i].address < refundAddresses[j].address
		})

		var destAddr []string
		var destAmt []string
		for i, out := range refundAddresses {
			destAddr = append(destAddr, string(out.address))
			destAmt = append(destAmt, out.amt)
			if (i+1)%1000 == 0 || (i+1) == len(refundAddresses) {
				invoice, _ := UnsignedContractResultInvoice(p.URL(),
					INVOKE_RESULT_REFUND, fmt.Sprintf("%d", height))
				nullDataScript, _ := sindexer.NullDataScript(sindexer.CONTENT_TYPE_INVOKERESULT, invoice)

				var txId string
				var err error
				for i := 0; i < 3; i++ {
					txId, err = p.stp.CoBatchSend_SatsNet(destAddr, ASSET_PLAIN_SAT.String(),
						destAmt, "contract", p.URL(), invokeCount, nullDataScript, p.StaticMerkleRoot, p.CurrAssetMerkleRoot)
					if err != nil {
						Log.Infof("refund contract %s CoBatchSend_SatsNet failed %v, wait a second and try again", p.URL(), err)
						// 服务端可能还没有同步到数据，需要多尝试几次，但不要卡太久
						// time.Sleep(2 * time.Second)
						continue
					}
					Log.Infof("refund contract %s with co-signed txId %s", p.URL(), txId)
					break
				}
				if err != nil {
					Log.Errorf("refund contract %s CoBatchSend_SatsNet failed %v,  try again later", p.URL(), err)
					// 下个区块再试
					p.isSending = false
					return err
				}

				p.mutex.Lock()
				// record
				var totalOutputSatsValue int64
				for _, addr := range destAddr {
					info := p.invalidMintMap[addr]
					info.Settled = true
					totalOutputSatsValue += info.TotalAmt.Int64()
					for _, u := range info.History {
						u.Done = DONE_REFUNDED
						u.OutTxId = txId
						SaveContractInvokeHistoryItem(p.stp.GetDB(), p.URL(), u)
					}
				}
				p.TotalOutputSats += totalOutputSatsValue
				p.SatsValueInPool -= totalOutputSatsValue
				p.SatsValueInPool -= DEFAULT_FEE_SATSNET
				p.RefundTxIDs = append(p.RefundTxIDs, txId)
				p.stp.SaveReservation(p.resv)
				p.mutex.Unlock()

				destAddr = nil
				destAmt = nil
			}
		}
		p.isSending = false

		Log.Infof("refund contract %s completed", p.URL())

	} else {
		// 在 HandleUnlockReq 等待peer的操作
		Log.Infof("server: waiting the refund of contract %s ", p.URL())
	}

	return nil
}

func (p *LaunchPoolContractRunTime) AllowPeerAction(action string, param any) (any, error) {

	Log.Infof("AllowPeerAction %s ", action)
	_, err := p.ContractRuntimeBase.AllowPeerAction(action, param)
	if err != nil {
		return nil, err
	}

	p.mutex.RLock()
	defer p.mutex.RUnlock()

	switch action {
	case wwire.STP_ACTION_SIGN: // 通道外资产

		req, ok := param.(*wwire.RemoteSignMoreData_Contract)
		if !ok {
			return nil, fmt.Errorf("not RemoteSignMoreData_Contract")
		}
		if len(req.Tx) != 1 {
			return nil, fmt.Errorf("only one tx can be accepted")
		}

		var dealInfo *DealInfo
		if req.Tx[0].L1Tx {
			tx, err := DecodeMsgTx(req.Tx[0].Tx)
			if err != nil {
				return nil, err
			}
			dealInfo, err = p.genSendInfoFromTx(tx, req.MoreData)
			if err != nil {
				return nil, err
			}

			if dealInfo.Height > p.CurrBlockL1 {
				return nil, fmt.Errorf("L1 not sync to %d yet, only in %d", dealInfo.Height, p.CurrBlockL1)
			}

		} else {
			tx, err := DecodeMsgTx_SatsNet(req.Tx[0].Tx)
			if err != nil {
				return nil, err
			}
			dealInfo, err = p.genSendInfoFromTx_SatsNet(tx, false)
			if err != nil {
				return nil, err
			}

			if dealInfo.Height > p.CurrBlock {
				return nil, fmt.Errorf("L2 not sync to %d yet, only in %d", dealInfo.Height, p.CurrBlock)
			}
		}

		dealInfo.InvokeCount = req.InvokeCount
		dealInfo.StaticMerkleRoot = req.StaticMerkleRoot
		dealInfo.RuntimeMerkleRoot = req.RuntimeMerkleRoot

		if !p.IsLaunching {
			return nil, fmt.Errorf("contract not in lanuch status")
		}

		if dealInfo.Reason == INVOKE_RESULT_OK {
			mintInfo := p.mintInfoMap
			for addr, infoInTx := range dealInfo.SendInfo {
				if addr == ADDR_OPRETURN {
					continue
				}
				if addr == p.ChannelAddr {
					continue
				}
				info, ok := mintInfo[addr]
				if !ok {
					return nil, fmt.Errorf("address %s is not in contract", addr)
				}
				if info.Settled {
					return nil, fmt.Errorf("address %s has launched", addr)
				}
				if infoInTx.AssetAmt.Cmp(info.TotalAmt) > 0 {
					return nil, fmt.Errorf("address %s mint value incorrect, %d %s", addr, info.TotalAmt, infoInTx.AssetAmt.String())
				}
			}
			Log.Infof("%s is allowed by contract %s with launch action", wwire.STP_ACTION_SIGN, p.URL())
		} else {
			mintInfo := p.invalidMintMap
			for addr, infoInTx := range dealInfo.SendInfo {
				if addr == ADDR_OPRETURN {
					continue
				}
				if addr == p.ChannelAddr {
					continue
				}
				info, ok := mintInfo[addr]
				if !ok {
					return nil, fmt.Errorf("address %s is not in contract", addr)
				}
				if info.Settled {
					return nil, fmt.Errorf("address %s has refunded", addr)
				}
				if info.TotalAmt.Int64() != infoInTx.Value {
					return nil, fmt.Errorf("address %s mint value incorrect, %s %d", addr, info.TotalAmt.String(), infoInTx.Value)
				}
			}
			Log.Infof("%s is allowed by contract %s with refund action", wwire.STP_ACTION_SIGN, p.URL())
		}
		return dealInfo, nil

	default:
		return nil, fmt.Errorf("AllowPeerAction not support action %s", action)
	}

}

// 之前已经校验过
func (p *LaunchPoolContractRunTime) SetPeerActionResult(action string, param any) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	Log.Infof("%s SetPeerActionResult %s ", p.URL(), action)

	switch action {
	case wwire.STP_ACTION_SIGN: // 通道外资产
		dealInfo, ok := param.(*DealInfo)
		if !ok {
			Log.Errorf("not DealInfo")
			return
		}

		bUpdate := false
		if dealInfo.Reason == INVOKE_RESULT_OK {
			mintInfo := p.mintInfoMap
			var totalOutputAssetAmt *Decimal
			for addr := range dealInfo.SendInfo {
				info, ok := mintInfo[addr]
				if !ok {
					continue
				}
				if !info.Settled {
					info.Settled = true
					totalOutputAssetAmt = totalOutputAssetAmt.Add(info.TotalAmt)
					for _, u := range info.History {
						u.Done = DONE_DEALT
						u.OutTxId = dealInfo.TxId
						SaveContractInvokeHistoryItem(p.stp.GetDB(), p.URL(), u)
					}
					bUpdate = true
				}
			}

			if bUpdate {
				p.TotalOutputAssets = p.TotalOutputAssets.Add(totalOutputAssetAmt)
				p.AssetAmtInPool = p.AssetAmtInPool.Sub(totalOutputAssetAmt)
				p.SatsValueInPool -= DEFAULT_FEE_SATSNET
				p.LaunchTxIDs = append(p.LaunchTxIDs, dealInfo.TxId)

				p.stp.SaveReservation(p.resv)
				Log.Infof("server: contract %s updated by txId %s", p.URL(), dealInfo.TxId)

			} else {
				// 重复进来
			}

		} else if dealInfo.Reason == INVOKE_RESULT_REFUND {
			mintInfo := p.invalidMintMap
			var totalOutputSatsValue int64
			for addr, _ := range dealInfo.SendInfo {
				info, ok := mintInfo[addr]
				if !ok {
					continue
				}
				if !info.Settled {
					info.Settled = true
					totalOutputSatsValue += info.TotalAmt.Int64()
					for _, u := range info.History {
						u.Done = DONE_REFUNDED
						u.OutTxId = dealInfo.TxId
						SaveContractInvokeHistoryItem(p.stp.GetDB(), p.URL(), u)
					}
					bUpdate = true
				}
			}

			if bUpdate {
				p.TotalOutputSats += totalOutputSatsValue
				p.SatsValueInPool -= totalOutputSatsValue
				p.SatsValueInPool -= DEFAULT_FEE_SATSNET
				p.RefundTxIDs = append(p.RefundTxIDs, dealInfo.TxId)

				p.stp.SaveReservation(p.resv)
				Log.Infof("server: contract %s updated by txId %s", p.URL(), dealInfo.TxId)
			} else {
				// 重复进来
			}
		}

		if bUpdate {
			// 看看是否全部完成
			for _, v := range p.mintInfoMap {
				if !v.Settled {
					return
				}
			}
			for _, v := range p.invalidMintMap {
				if !v.Settled {
					return
				}
			}

			// update status
			p.IsLaunching = false
			p.Status = CONTRACT_STATUS_CLOSED
			p.resv.SetStatus(RS_DEPLOY_CONTRACT_COMPLETED)
			p.stp.SaveReservation(p.resv)
			Log.Infof("server: contract %s closed", p.URL())
		}

		return
	}
}

func (p *LaunchPoolContractRunTime) LeftToMint() *Decimal {
	return indexer.DecimalSub(p.TotalAssetToMint(), p.TotalMinted)
}

func VerifyLaunchPoolHistory(history []*SwapHistoryItem, invokeCount int64, status int, org *LaunchPoolRunningData) (*LaunchPoolRunningData, error) {
	InvokeCount := int64(0)
	mintInfoMap := make(map[string]*MinterStatus)
	refundMap := make(map[string]*MinterStatus)
	var runningData LaunchPoolRunningData
	runningData.AssetAmtInPool = org.AssetAmtInPool.Clone()
	runningData.TotalInputAssets = org.TotalInputAssets.Clone()

	// 重新生成统计数据
	var onSendingVaue int64
	var onSendngAmt *Decimal
	txmap := make(map[string]bool)
	for i, item := range history {
		if int64(i) != item.Id {
			return nil, fmt.Errorf("missing history. previous %d, current %d", i-1, item.Id)
		}

		txmap[item.OutTxId] = true
		InvokeCount++
		runningData.TotalInputSats += item.InValue
		runningData.TotalInputAssets = runningData.TotalInputAssets.Add(item.InAmt)
		if item.Done != DONE_NOTYET {
			runningData.TotalOutputAssets = runningData.TotalOutputAssets.Add(item.OutAmt)
			runningData.TotalOutputSats += item.OutValue
		}

		if item.OrderType == ORDERTYPE_MINT {
			if item.Done == DONE_NOTYET {
				runningData.SatsValueInPool += item.InValue
				minter, ok := mintInfoMap[item.Address]
				if !ok {
					minter = &MinterStatus{}
					mintInfoMap[item.Address] = minter
				}
				minter.TotalAmt = minter.TotalAmt.Add(item.OutAmt)

				Log.Infof("Minting %d: Amt: %s-%s-%s Value: %d-%d-%d Price: %s in: %s", item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
					item.InValue, item.RemainingValue, item.OutValue, item.UnitPrice.String(), item.InUtxo)

			} else if item.Done == DONE_DEALT {
				runningData.SatsValueInPool += item.InValue
				runningData.TotalMinted = runningData.TotalMinted.Add(item.OutAmt)

				// 已经发送
				Log.Infof("Done %d: Amt: %s-%s-%s Value: %d-%d-%d Price: %s in: %s out: %s", item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
					item.InValue, item.RemainingValue, item.OutValue, item.UnitPrice.String(), item.InUtxo, item.OutTxId)
			} else if item.Done == DONE_REFUNDED {
				Log.Infof("Refund %d: Amt: %s-%s-%s Value: %d-%d-%d in: %s out: %s", item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
					item.InValue, item.RemainingValue, item.OutValue, item.InUtxo, item.OutTxId)
				// 退款
				runningData.TotalInvalid += item.OutValue

				refunder, ok := refundMap[item.Address]
				if !ok {
					refunder = &MinterStatus{}
					refundMap[item.Address] = refunder
				}
				refunder.TotalAmt = refunder.TotalAmt.Add(item.RemainingAmt)
			}
		}
	}
	if org.IsLaunching || status == CONTRACT_STATUS_CLOSED {
		runningData.SatsValueInPool -= DEFAULT_FEE_SATSNET * int64(len(txmap))
	}

	// 对比数据
	Log.Infof("OnSending: value: %d, amt: %s", onSendingVaue, onSendngAmt.String())
	Log.Infof("invokeCount %d %d", InvokeCount, invokeCount)
	Log.Infof("runningData: \n%v\n%v", runningData, *org)

	err := "different: "
	if runningData.SatsValueInPool != org.SatsValueInPool {
		Log.Errorf("SatsValueInPool: %d %d", runningData.SatsValueInPool, org.SatsValueInPool)
		err = fmt.Sprintf("%s SatsValueInPool", err)
	}
	if runningData.AssetAmtInPool.Cmp(org.AssetAmtInPool) != 0 {
		Log.Errorf("AssetAmtInPool: %s %s", runningData.AssetAmtInPool.String(), org.AssetAmtInPool.String())
		err = fmt.Sprintf("%s AssetAmtInPool", err)
	}
	if runningData.TotalInputSats != org.TotalInputSats {
		Log.Errorf("TotalInputSats: %d %d", runningData.TotalInputSats, org.TotalInputSats)
		err = fmt.Sprintf("%s TotalInputSats", err)
	}
	if runningData.TotalInputAssets.Cmp(org.TotalInputAssets) != 0 {
		Log.Errorf("TotalInputAssets: %s %s", runningData.TotalInputAssets.String(), org.TotalInputAssets.String())
		err = fmt.Sprintf("%s TotalInputAssets", err)
	}

	return &runningData, nil
}

func (p *LaunchPoolContractRunTime) checkSelf() error {
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
	// 	buf, _ = json.Marshal(p.LaunchPoolRunningData)
	// 	Log.Infof("running data: %s\n", string(buf))
	// }

	runningData, err := VerifyLaunchPoolHistory(mid1,
		p.InvokeCount, p.Status, &p.LaunchPoolRunningData)
	// 更新统计
	p.updateRunningData(runningData)

	if err != nil {
		Log.Errorf(err.Error())
		return err
	}
	return nil
}

func (p *LaunchPoolContractRunTime) updateRunningData(runningData *LaunchPoolRunningData) {
	// p.LaunchPoolRunningData = runningData

	// p.LaunchPoolRunningData.TotalMinted = runningData.TotalMinted
	// p.LaunchPoolRunningData.TotalInvalid = runningData.TotalInvalid

	// p.LaunchPoolRunningData.AssetAmtInPool = runningData.AssetAmtInPool
	// p.LaunchPoolRunningData.SatsValueInPool = runningData.SatsValueInPool

	// p.LaunchPoolRunningData.TotalInputAssets = runningData.TotalInputAssets
	// p.LaunchPoolRunningData.TotalInputSats = runningData.TotalInputSats

	// p.LaunchPoolRunningData.TotalOutputAssets = runningData.TotalOutputAssets
	// p.LaunchPoolRunningData.TotalOutputSats = runningData.TotalOutputSats

	// p.calcAssetMerkleRoot()
	// saveReservation(p.stp.GetDB(), &p.resv.ContractDeployDataInDB)
}
