package wallet

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
)

/*
Faucet合约
1. 按照指定的费率兑换资产，目前主要用于兑换聪网gas
2. 支持默认调用，无论是L1/L2，直接往合约地址发送聪，自动兑换，结果转移到L2上调用者地址
*/

func init() {
	gob.Register(new(FaucetContractRuntime))
}

const DEFAULT_PRICE = "0.001" // 0.001 sat = 1 asset

type FaucetContract struct {
	SwapContract
	Price string `json:"price"`
}

func NewFaucetContract() *FaucetContract {
	c := &FaucetContract{
		SwapContract: *NewSwapContract(),
		Price:        DEFAULT_PRICE,
	}
	c.TemplateName = TEMPLATE_CONTRACT_FAUCET
	return c
}

func (p *FaucetContract) IsExclusive() bool {
	return true
}

func (p *FaucetContract) CheckContent() error {
	if indexer.IsPlainAsset(&p.AssetName) {
		return nil
	}

	err := p.SwapContract.CheckContent()
	if err != nil {
		return err
	}

	return nil
}

func (p *FaucetContract) InvokeParam(action string) string {
	return ""
}

func (p *FaucetContract) Encode() ([]byte, error) {
	base, err := p.SwapContract.Encode()
	if err != nil {
		return nil, err
	}

	return txscript.NewScriptBuilder().
		AddData(base).
		AddData([]byte(p.Price)).
		Script()
}

func (p *FaucetContract) Decode(data []byte) error {
	tokenizer := txscript.MakeScriptTokenizer(0, data)

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing base content")
	}
	base := tokenizer.Data()
	err := p.SwapContract.Decode(base)
	if err != nil {
		return err
	}

	if !tokenizer.Next() || tokenizer.Err() != nil {
		return fmt.Errorf("missing asset amt")
	}
	p.Price = string(tokenizer.Data())

	return nil
}

func (p *FaucetContract) Content() string {
	b, err := json.Marshal(p)
	if err != nil {
		Log.Errorf("Marshal FaucetContract failed, %v", err)
		return ""
	}
	return string(b)
}

func (p *FaucetContract) CalcStaticMerkleRoot() []byte {
	return CalcContractStaticMerkleRoot(p)
}

type FaucetContractRuntime struct {
	SwapContractRuntime

	price *Decimal
}

func NewFaucetContractRuntime(stp ContractManager) *FaucetContractRuntime {
	p := &FaucetContractRuntime{
		SwapContractRuntime: SwapContractRuntime{
			SwapContractRuntimeInDB: SwapContractRuntimeInDB{
				Contract:                NewFaucetContract(),
				ContractRuntimeBase:     *NewContractRuntimeBase(stp),
				SwapContractRunningData: SwapContractRunningData{},
			},
		},
	}
	p.init()
	p.runtime = p

	return p
}

func (p *FaucetContractRuntime) InitFromContent(content []byte, stp ContractManager, resv ContractDeployResvIF) error {
	err := p.SwapContractRuntime.InitFromContent(content, stp, resv)
	if err != nil {
		return err
	}
	p.runtime = p

	err = p.setOriginalValue()
	if err != nil {
		return err
	}

	return nil
}

func (p *FaucetContractRuntime) InitFromJson(content []byte, stp ContractManager) error {
	err := json.Unmarshal(content, p)
	if err != nil {
		return err
	}
	p.init()
	p.runtime = p

	err = p.setOriginalValue()
	if err != nil {
		return err
	}

	return nil
}

// 从gob中加载的对象，并没有经过 NewSwapContractRuntime 赋值，需要重新初始化一些对象
func (p *FaucetContractRuntime) InitFromDB(stp ContractManager, resv ContractDeployResvIF) error {
	err := p.SwapContractRuntime.InitFromDB(stp, resv)
	if err != nil {
		return err
	}
	p.runtime = p	// 关键是设置这个

	err = p.setOriginalValue()
	if err != nil {
		return err
	}

	return nil
}


func (p *FaucetContractRuntime) setOriginalValue() error {
	contractBase, ok := p.Contract.(*FaucetContract)
	if !ok {
		return fmt.Errorf("not FaucetContract")
	}

	var err error
	p.price, err = indexer.NewDecimalFromString(contractBase.Price, MAX_PRICE_DIVISIBILITY)
	if err != nil {
		return err
	}
	return nil
}


func (p *FaucetContractRuntime) AllowDeploy() error {

	// 如果amm合约存在，不能部署
	if p.stp.GetSpecialContractResv(p.GetAssetName().String(), TEMPLATE_CONTRACT_AMM) != nil {
		return fmt.Errorf("no need to deploy %s contract", p.RelativePath())
	}

	err := p.SwapContractRuntime.AllowDeploy()
	if err != nil {
		return err
	}

	return nil
}

func (p *FaucetContractRuntime) AllowInvokeWithNoParam() bool {
	return true
}

func (p *FaucetContractRuntime) AllowInvokeWithNoParam_SatsNet() bool {
	return true
}

func (p *FaucetContractRuntime) GobEncode() ([]byte, error) {
	return p.SwapContractRuntime.GobEncode()
}

func (p *FaucetContractRuntime) GobDecode(data []byte) error {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)

	var swap FaucetContract
	if err := dec.Decode(&swap); err != nil {
		return err
	}
	p.Contract = &swap

	if ContractRuntimeBaseUpgrade {
		var old ContractRuntimeBase_old
		if err := dec.Decode(&old); err != nil {
			return err
		}
		p.ContractRuntimeBase = *old.ToNewVersion()

		var old2 SwapContractRunningData_old
		if err := dec.Decode(&old2); err != nil {
			return err
		}
		p.SwapContractRunningData = *old2.ToNewVersion()
	} else {
		if err := dec.Decode(&p.ContractRuntimeBase); err != nil {
			return err
		}

		if err := dec.Decode(&p.SwapContractRunningData); err != nil {
			return err
		}
	}

	return nil
}

func (p *FaucetContractRuntime) checkSelf() error {
	return nil
}

func CalcExpectedFaucetAmt(amt int64, height int, price *Decimal) (*Decimal) {
	// 目前先简单按照固定值，后续可以根据实际情况调整
	return indexer.NewDecimal(amt, 0).Div(price)
}

func (p *FaucetContractRuntime) VerifyAndAcceptInvokeItem_SatsNet(invokeTx *InvokeTx_SatsNet, height int) (InvokeHistoryItem, error) {

	invokeData := invokeTx.InvokeParam
	output := invokeTx.TxOutput
	address := invokeTx.Invoker

	var param InvokeParam
	if invokeData != nil && invokeData.InvokeParam != nil {
		err := param.Decode(invokeData.InvokeParam)
		if err != nil {
			return nil, err
		}
	} else { 
		if output.GetAsset(p.GetAssetName()).Sign() == 0 {
			if output.GetPlainSat() == 0 {
				return nil, fmt.Errorf("invalid invoke without asset and value")
			}
			param.Action = INVOKE_API_SWAP
		} else {
			param.Action = INVOKE_API_ADDLIQUIDITY
		}
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
	case INVOKE_API_ADDLIQUIDITY:
		// 更新合约状态
		invokeTx.Handled = true
		p.updateContract_liquidity(address, output, ORDERTYPE_ADDLIQUIDITY, output.GetAsset(p.GetAssetName()), output.GetPlainSat(), true, false, false)
		return nil, nil

	case INVOKE_API_SWAP:
		// 更新合约状态
		invokeTx.Handled = true
		if output.Value() < INVOKE_FEE {
			return nil, fmt.Errorf("no sats")
		}
		param := SwapInvokeParam{
			OrderType: ORDERTYPE_BUY,
			AssetName: p.GetAssetName().String(),
			Amt:       "0",
			UnitPrice: p.price.String(),
		}
		p.updateContract_swap(address, output, &param, false, true)
		
		return nil, nil
	default:
		Log.Errorf("contract %s does not support action %s", url, param.Action)
		return nil, fmt.Errorf("not support action %s", param.Action)
	}
}

func (p *FaucetContractRuntime) VerifyAndAcceptInvokeItem(invokeTx *InvokeTx, height int) (InvokeHistoryItem, error) {

	invokeData := invokeTx.InvokeParam
	output := invokeTx.TxOutput
	address := invokeTx.Invoker

	var param InvokeParam
	if invokeData != nil && invokeData.InvokeParam != nil {
		err := param.Decode(invokeData.InvokeParam)
		if err != nil {
			return nil, err
		}
	} else {
		param.Action = INVOKE_API_SWAP
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

	case INVOKE_API_SWAP: // 主网没有调用参数时的默认动作
		// 更新合约状态
		invokeTx.Handled = true
		if output.Value() < INVOKE_FEE {
			return nil, fmt.Errorf("no sats")
		}
		param := SwapInvokeParam{
			OrderType: ORDERTYPE_BUY,
			AssetName: p.GetAssetName().String(),
			Amt:       "0",
			UnitPrice: p.price.String(),
		}
		p.updateContract_swap(address, OutputToSatsNet(output), &param, true, true)
		return nil, nil

	default:
		Log.Errorf("contract %s does not support action %s", p.URL(), param.Action)
		return nil, fmt.Errorf("not support action %s", param.Action)
	}
}


func (p *FaucetContractRuntime) InvokeWithBlock_SatsNet(data *InvokeDataInBlock_SatsNet) error {
	err := p.ContractRuntimeBase.InvokeWithBlock_SatsNet(data)
	if err != nil {
		return err
	}

	if p.IsActive() {
		p.mutex.Lock()
		//Log.Infof("%s InvokeWithBlock_SatsNet %d", stp.GetMode(), data.Height)

		p.PreprocessInvokeData_SatsNet(data)
		p.addLiquidity()
		p.swap()
		p.ContractRuntimeBase.InvokeCompleted_SatsNet(data)
		p.mutex.Unlock()

		p.sendInvokeResultTx_SatsNet()
	} else {
		p.mutex.Lock()
		p.ContractRuntimeBase.InvokeCompleted_SatsNet(data)
		p.mutex.Unlock()
	}

	return nil
}

func (p *FaucetContractRuntime) InvokeWithBlock(data *InvokeDataInBlock) error {

	err := p.ContractRuntimeBase.InvokeWithBlock(data)
	if err != nil {
		return err
	}

	if p.IsActive() {
		p.mutex.Lock()
		// 先保存池子中资产数量，因为processInvoke会更新池子数据，但这个更新不是我们所期望的
		Log.Infof("%s InvokeWithBlock %d", p.stp.GetMode(), data.Height)

		p.PreprocessInvokeData(data)
		// 确保在区块后马上执行swap，发送可以等等
		p.swap()
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

func (p *FaucetContractRuntime) addLiquidity() error {
	if len(p.addLiquidityMap) == 0 {
		return nil
	}

	url := p.URL()
	var totalAddedAmt *Decimal
	var totalAddedValue int64
	for _, v := range p.addLiquidityMap {
		for _, item := range v {
			totalAddedAmt = totalAddedAmt.Add(item.RemainingAmt)
			totalAddedValue += item.RemainingValue
			item.Done = ITEM_STATUS_DEALT
			delete(p.history, item.InUtxo) 
			SaveContractInvokeHistoryItem(p.stp.GetDB(), url, item)
		}
	}

	p.addLiquidityMap = make(map[string]map[int64]*SwapHistoryItem)
	p.AssetAmtInPool = p.AssetAmtInPool.Add(totalAddedAmt)
	p.SatsValueInPool += totalAddedValue
	return nil
}

func (p *FaucetContractRuntime) swap() bool {
	if len(p.buyPool) == 0 {
		return false
	}

	url := p.URL()
	updated := false
	for _, item := range p.buyPool {
		if item.Finished() {
			continue
		}

		if item.RemainingValue == 0 {
			continue
		}
		// 设置item的版本
		item.Version = 1 // 0 是初始版本，服务费全部是聪

		if item.OrderType == ORDERTYPE_BUY {
			outAmt := CalcExpectedFaucetAmt(item.RemainingValue, p.CurrBlock, p.price)
			if p.AssetAmtInPool.Cmp(outAmt) < 0 {
				// 兑换失败，池子余额不足，直接返回
				Log.Errorf("Faucet buy %s: in_value=%d, min_amt=%s, real_amt=%s, utxo: %s", INVOKE_REASON_NO_ENOUGH_ASSET,
					item.InValue, outAmt.String(), "0", item.InUtxo)
				break
			}

			p.LastDealPrice = indexer.DecimalDiv(indexer.NewDecimal(int64(item.RemainingValue), p.dealDivisibility+2), outAmt)

			// 更新池子
			p.SatsValueInPool += item.RemainingValue // 利润留存在池子中
			p.AssetAmtInPool = p.AssetAmtInPool.Sub(outAmt)
			p.TotalDealAssets = p.TotalDealAssets.Add(outAmt)
			//p.TotalDealSats += item.RemainingValue 只记录卖出的

			// 更新item
			item.RemainingValue = 0
			item.OutAmt = outAmt

			Log.Infof("Faucet buy dealt: in_value=%d, out_amt=%s, price=%s, utxo: %s",
				item.InValue, item.OutAmt.String(), p.LastDealPrice.String(), item.InUtxo)

		} else {
			Log.Errorf("Faucet unsupport %d: %v", item.OrderType, item)
			// 不可能有，前面已经过滤
			continue
		}

		item.UnitPrice = p.LastDealPrice.Clone()
		if p.HighestDealPrice.Cmp(p.LastDealPrice) < 0 {
			p.HighestDealPrice = p.LastDealPrice.Clone()
		}
		if p.LowestDealPrice == nil || p.LowestDealPrice.Cmp(p.LastDealPrice) > 0 {
			p.LowestDealPrice = p.LastDealPrice.Clone()
		}

		p.TotalDealCount++
		SaveContractInvokeHistoryItem(p.stp.GetDB(), url, item)
		updated = true
	}

	// 最终更新池子
	Log.Infof("Pool(%d): value=%d, amt=%s, contract %s",
		p.CurrBlock, p.SatsValueInPool, p.AssetAmtInPool.String(), p.GetContractName())

	// 交易的结果先保存
	if updated {
		p.stp.SaveReservationWithLock(p.resv)
	}

	return updated
}
