package wallet

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sync"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

/*
资产锁定合约
1. 锁定某种资产
2. 锁定人满足某个条件，才能释放
3. 条件（可扩展，通过不同的版本不断增加可配置条件）
	a. 时间条件（在指定的区块高度，可以提取对应的资产数量）
	b. 积分条件：从索引器获得积分数据，根据积分释放
	c. 价格条件：价格来源是市场（暂时不支持）
*/


func init() {
	gob.Register(&VaultContractRuntime{})
}

const (
	VAULT_UNLOCKTYPE_TIME 	int = 1
	VAULT_UNLOCKTYPE_POINT 	int = 2
	VAULT_UNLOCKTYPE_PRICE 	int = 3


	HALVING_TYPE_ASSET_AMOUNT	int = 1
	HALVING_TYPE_ASSET_RATIO	int = 2 // 千分之
	HALVING_TYPE_HEIGHT			int = 3
)

// 在这个高度释放对应数量的资产
type TimeToUnlockSchedule struct {
	Height 	int
	UnlockAmt string
}

func (p *TimeToUnlockSchedule) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.Height)).
		AddData([]byte(p.UnlockAmt)).
		Script()
}

func (p *TimeToUnlockSchedule) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing height")
	}
	p.Height = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing unlock amt")
	}
	p.UnlockAmt = string(tokenizer.Data())

	return nil
}

// 由某些特殊的交易消耗的聪解锁对应的资产
type BurnToUnlockSchedule struct {
	HalvingType		int		// 减半类型
	HalvingCycle	int64   // 减半周期：资产数量，比例（千分之），或者高度，由HalvingType确定
	InitialValue	int64	// 初始兑换比例，消耗多少聪才能换的一份资产
}


func (p *BurnToUnlockSchedule) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.HalvingType)).
		AddInt64(int64(p.HalvingCycle)).
		AddInt64(int64(p.InitialValue)).
		Script()
}

func (p *BurnToUnlockSchedule) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing HalvingType")
	}
	p.HalvingType = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing HalvingCycle")
	}
	p.HalvingCycle = tokenizer.ExtractInt64()

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing InitialValue")
	}
	p.InitialValue = tokenizer.ExtractInt64()

	return nil
}


// 提取时的分配比例，单位：千分之
type WithdrawAllocationRatio struct {
	Trader			int		// 交易者
	Referrer		int		// 交易者绑定的推荐人
	Referree		int		// 被推荐人，也就是交易者
	Server			int		// 服务提供者
	Foundation		int		// 基金会
}

func (p *WithdrawAllocationRatio) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddInt64(int64(p.Trader)).
		AddInt64(int64(p.Referrer)).
		AddInt64(int64(p.Referree)).
		AddInt64(int64(p.Server)).
		AddInt64(int64(p.Foundation)).
		Script()
}

func (p *WithdrawAllocationRatio) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing Trader")
	}
	p.Trader = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing Referrer")
	}
	p.Referrer = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing Referree")
	}
	p.Referree = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing Server")
	}
	p.Server = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing Foundation")
	}
	p.Foundation = int(tokenizer.ExtractInt64())

	return nil
}


type VaultContract struct {
	ContractBase
	AssetAmt 		string `json:"assetAmt"`  // 最低数量，可能会超过，根据区块而定
	IsL1 			bool `json:"isL1"` // 锁定在一层还是二层

	// 释放计划
	UnlockType 		int  `json:"unlockType"`
	UnlockSchedule  string `json:"unlockSchedule"`
	
	// TimeToUnlock  	[]*TimeToUnlockSchedule
	// BurnToUnlock    *BurnToUnlockSchedule
	
	// 提取时的分配
	WithdrawRatio 	WithdrawAllocationRatio `json:"allocationRatio"`
}

func NewVaultContract() *VaultContract {
	return &VaultContract{
		ContractBase: ContractBase{
			TemplateName: TEMPLATE_CONTRACT_VAULT,
		},
	}
}


func (p *VaultContract) GetContractName() string {
	return p.AssetName.String() + URL_SEPARATOR + p.TemplateName
}

func (p *VaultContract) CalcDeployFee() int64 {
	return 2000
}

func (p *VaultContract) GetAssetName() *swire.AssetName {
	return &p.AssetName
}


func (p *VaultContract) CheckContent() error {
	err := p.ContractBase.CheckContent()
	if err != nil {
		return err
	}

	switch p.UnlockType {
	case VAULT_UNLOCKTYPE_TIME:
		var schedule []*TimeToUnlockSchedule
		err := json.Unmarshal([]byte(p.UnlockSchedule), &schedule)
		if err != nil {
			return err
		}

		amtInVault, err := indexer.NewDecimalFromString(p.AssetAmt, MAX_ASSET_DIVISIBILITY)
		if err != nil {
			return err
		}

		var total *Decimal
		for _, s := range schedule {
			amt, err := indexer.NewDecimalFromString(s.UnlockAmt, MAX_ASSET_DIVISIBILITY)
			if err != nil {
				return err
			}
			total = total.Add(amt)
		}
		if amtInVault.Cmp(total) != 0 {
			return fmt.Errorf("asset amt different")
		}

	case VAULT_UNLOCKTYPE_POINT:
		var schedule BurnToUnlockSchedule
		err := json.Unmarshal([]byte(p.UnlockSchedule), &schedule)
		if err != nil {
			return err
		}
		switch schedule.HalvingType {
		case HALVING_TYPE_ASSET_AMOUNT:
		case HALVING_TYPE_ASSET_RATIO:
		case HALVING_TYPE_HEIGHT:
		default:
			return fmt.Errorf("invalid halving type %d", schedule.HalvingType)
		}

		if schedule.HalvingCycle < 0 {
			return fmt.Errorf("invalid halving cycle %d", schedule.HalvingCycle)
		}
		if schedule.InitialValue < 0 {
			return fmt.Errorf("invalid intial value %d", schedule.InitialValue)
		}

	case VAULT_UNLOCKTYPE_PRICE:
	default:
		return fmt.Errorf("invalid unlock type %d", p.UnlockType)
	}
	
	if p.WithdrawRatio.Foundation + p.WithdrawRatio.Server + p.WithdrawRatio.Referree + 
	p.WithdrawRatio.Referrer + p.WithdrawRatio.Trader != 1000 {
		return fmt.Errorf("withdraw ratio should be 1000 totally")
	}

	return nil
}

func (p *VaultContract) InvokeParam(action string) string {

	var param InvokeParam
	param.Action = action
	switch action {
	case INVOKE_API_DEPOSIT:
		var innerParam DepositInvokeParam
		innerParam.OrderType = ORDERTYPE_DEPOSIT
		buf, err := json.Marshal(&innerParam)
		if err != nil {
			return ""
		}
		param.Param = string(buf)

	case INVOKE_API_WITHDRAW:
		var innerParam DepositInvokeParam
		innerParam.OrderType = ORDERTYPE_WITHDRAW
		buf, err := json.Marshal(&innerParam)
		if err != nil {
			return ""
		}
		param.Param = string(buf)

	default:
		return ""
	}

	result, err := json.Marshal(&param)
	if err != nil {
		return ""
	}
	return string(result)

}

func (p *VaultContract) Content() string {
	b, err := json.Marshal(p)
	if err != nil {
		Log.Errorf("Marshal VaultContract failed, %v", err)
		return ""
	}
	return string(b)
}

func (p *VaultContract) Encode() ([]byte, error) {
	base, err := p.ContractBase.Encode()
	if err != nil {
		return nil, err
	}

	isL1 := 0
	if p.IsL1 {
		isL1 = 1
	}

	ratio, err := p.WithdrawRatio.Encode()
	if err != nil {
		return nil, err
	}

	return txscript.NewScriptBuilder().
		AddData(base).
		AddData([]byte(p.AssetAmt)).
		AddInt64(int64(isL1)).
		AddInt64(int64(p.UnlockType)).
		AddData([]byte(p.UnlockSchedule)).
		AddData(ratio).
		Script()
}

func (p *VaultContract) Decode(data []byte) error {
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
		return fmt.Errorf("missing asset amt")
	}
	p.AssetAmt = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing isL1")
	}
	isL1 := tokenizer.ExtractInt64()
	p.IsL1 = isL1 != 0

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing unlock type")
	}
	p.UnlockType = int(tokenizer.ExtractInt64())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing unlock schedule")
	}
	p.UnlockSchedule = string(tokenizer.Data())

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing ratio")
	}
	ratio := tokenizer.Data()
	err = p.WithdrawRatio.Decode(ratio)
	if err != nil {
		return err
	}

	return nil
}


type responseItem_vault struct {
	Address string  `json:"address"`
	InvokeCount  	int
	DepositAmt   	string	// 存款总额
	WithdrawAmt  	string	// 取款总额
	WithdrawableAmt string	// 可提取金额，还没有提取
	RefundAmt    	string	// 无效退回，不计入存款总额
}

type responseStatus_vault struct {
	*VaultContractRunTimeInDB

	// 增加更多参数
}

type VaultRunningData_old = VaultRunningData

type VaultRunningData struct {
	AllowDeposit      bool
	AllowWithdraw     bool

	// vault中的初始资产总量=LockedAssetAmt + UnlockedAssetAmt
	LockedAssetAmt    *Decimal // 池子中资产的数量
	LockedSatsValue   int64    // 池子中聪的数量，用于各种费用

	UnlockedAssetAmt  *Decimal // 已经解锁的资产总量
	UnlockedSatsValue int64

	WithdrawableAmt   *Decimal // 现在可提取的资产数量
	WithdrawableValue int64	   

	TotalInputAssets  *Decimal // 所有进入池子的资产数量，不包括无效资产，等于总的锁定资产数量
	TotalInputSats    int64 
	TotalOutputAssets *Decimal // 所有退出池子的资产数量，
	TotalOutputSats   int64 

	TotalRefundAssets *Decimal // 无效输入退款
	TotalRefundSats   int64    // 无效输入退款
	TotalRefundTx     int
	TotalRefundTxFee  int64

	TotalWithdrawAssets *Decimal
	TotalWithdrawTx     int
	TotalWithdrawTxFee  int64

	LastHeight 		  int
	LastIncoming	  int64
}

func (p *VaultRunningData) ToNewVersion() *VaultRunningData {
	return p
}

type VaultContractRunTimeInDB struct {
	VaultContract
	ContractRuntimeBase

	// 运行过程的状态
	VaultRunningData
}

type VaultInvokerStatus struct {
	InvokerStatusBase
	
	WithdrawableAmt *Decimal	// 可提取金额，还没有提取
}

func NewVaultInvokerStatus(address string, divisibility int) *VaultInvokerStatus {
	return &VaultInvokerStatus{
		InvokerStatusBase: *NewInvokerStatusBase(address, divisibility),
		WithdrawableAmt: indexer.NewDecimal(0, divisibility),
	}
}

type UnlockStatus struct {
	Height 				int
	UnlockAmt 			*Decimal
	ContractMerkleRoot 	[]byte
}

type VaultContractRuntime struct {
	VaultContractRunTimeInDB

	
	invokeHistory   map[string]*InvokeItem           // key:utxo 单独记录数据库，区块缓存, 
	unlockHistory   map[int]*UnlockStatus			 // height
	invokerInfoMap  map[string]*VaultInvokerStatus        // user address，缓存
	refundMap       map[string]map[int64]*SwapHistoryItem // 准备退款的账户, address -> refund invoke item list, 无效的item也放进来，一起退款
	depositMap      map[string]map[int64]*InvokeItem // deposit的账户, address -> deposit invoke item list
	withdrawMap     map[string]map[int64]*InvokeItem // 准备withdraw的账户, address -> withdraw invoke item list
	isSending		bool

	burnToUnlock 	*BurnToUnlockSchedule

	originalAmt   	*Decimal
	refreshTime   	int64
	responseCache 	[]*responseItem_vault
	responseStatus  *responseStatus_vault
	responseHistory map[int][]*InvokeItem // 按照100个为一桶，根据区块顺序记录，跟History保持一致
	
	mutex sync.RWMutex
}

func NewVaultContractRuntime(stp *Manager) *VaultContractRuntime {
	p := &VaultContractRuntime{
		VaultContractRunTimeInDB: VaultContractRunTimeInDB{
			VaultContract: *NewVaultContract(),
			ContractRuntimeBase: ContractRuntimeBase{
				DeployTime: time.Now().Unix(),
				stp:        stp,
			},
			VaultRunningData: VaultRunningData{
				AllowDeposit: true,
				AllowWithdraw: false,
			},
			
		},
	}
	p.init()

	return p
}


func (p *VaultContractRuntime) init() {
	p.contract = p
	p.invokeHistory = make(map[string]*InvokeItem)
	p.unlockHistory = make(map[int]*UnlockStatus)
	p.invokerInfoMap = make(map[string]*VaultInvokerStatus)
	p.refundMap = make(map[string]map[int64]*SwapHistoryItem)
	p.depositMap = make(map[string]map[int64]*InvokeItem)
	p.withdrawMap = make(map[string]map[int64]*InvokeItem)
	p.responseHistory = make(map[int][]*InvokeItem)
}


func (p *VaultContractRuntime) InitFromContent(content []byte, stp *Manager) error {
	err := p.ContractRuntimeBase.InitFromContent(content, stp)
	if err != nil {
		return err
	}

	contractBase := p.VaultContract
	p.originalAmt, err = indexer.NewDecimalFromString(contractBase.AssetAmt, p.Divisibility)
	if err != nil {
		return err
	}


	return nil
}


func (p *VaultContractRuntime) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)

	if err := enc.Encode(p.VaultContract); err != nil {
		return nil, err
	}

	if err := enc.Encode(p.ContractRuntimeBase); err != nil {
		return nil, err
	}

	if err := enc.Encode(p.VaultRunningData); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (p *VaultContractRuntime) GobDecode(data []byte) error {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)

	if err := dec.Decode(&p.VaultContract); err != nil {
		return err
	}


	if err := dec.Decode(&p.ContractRuntimeBase); err != nil {
		return err
	}

	if err := dec.Decode(&p.VaultRunningData); err != nil {
		return err
	}
	

	return nil
}


func (p *VaultContractRuntime) RuntimeContent() []byte {
	b, err := EncodeToBytes(p)
	if err != nil {
		Log.Errorf("Marshal VaultContractRuntime failed, %v", err)
		return nil
	}
	return b
}



func (p *VaultContractRuntime) getBurnToUnlockSchedule() *BurnToUnlockSchedule {
	if p.burnToUnlock != nil {
		return p.burnToUnlock
	}

	var schedule BurnToUnlockSchedule
	err := schedule.Decode([]byte(p.VaultContract.UnlockSchedule))
	if err != nil {
		return nil
	}
	p.burnToUnlock = &schedule

	return p.burnToUnlock
}

func (p *VaultContractRuntime) calcUnlockAmt(height int, incoming int64) *Decimal {
	if p.burnToUnlock != nil {
		total := p.TotalInputAssets.Clone()
		locked := p.LockedAssetAmt.Clone()
		unloked := indexer.DecimalSub(total, locked)
		p.LastHeight = height
		newIncoming := incoming - p.LastIncoming

		switch p.burnToUnlock.HalvingType {
		case HALVING_TYPE_ASSET_AMOUNT:
		case HALVING_TYPE_ASSET_RATIO:
			unlockedRatio := indexer.DecimalDiv(unloked, total).MulBigInt(big.NewInt(1000)).Int64()
			power := unlockedRatio/p.burnToUnlock.HalvingCycle
			swapRatio := int64(math.Pow(2, float64(power))) * p.burnToUnlock.InitialValue
			return indexer.NewDecimal(newIncoming / swapRatio, p.Divisibility)

		case HALVING_TYPE_HEIGHT:
		default:
			Log.Errorf("invalid halving type %d", p.burnToUnlock.HalvingType)
			return nil
		}

	} 

	return nil
}

func (p *VaultContractRuntime) calcMerkleRoot() []byte {
	return nil
}
