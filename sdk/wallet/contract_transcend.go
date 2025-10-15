package wallet

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"

	"sort"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
)

/*
Transcend合约
1. 支持指定资产进入和退出聪网，优先级比amm低，但支持白聪
2. 每个区块处理完成后，统一回款
3. 项目方提取池子利润 （只能提取白聪，默认的分配规则：项目方：节点A：节点B：基金会=55:20:20:5）

对该地址上该资产的约定：
1. 利润是白聪总量
2. 在L1上，没有被Ascend过的utxo，可以用来支持withdraw。如果没有足够的utxo可用，就必须先Descend一些utxo（DeAnchorTx）。但必须保留TranscendContract指定数量资产。
3. 在L2上，超出TranscendContract的资产，可以直接send出去给用户，用来支持deposit操作。如果不够，就需要先Ascend用户转进来的utxo
4. 为了简单一点，现在withdraw直接deAnchor，但deposit不执行anchor
*/


func init() {
	// 让 gob 知道旧的类型对应新的实现
	gob.RegisterName("*stp.TranscendContractRuntime", new(TranscendContractRuntime))
}


type TranscendContract struct {
	SwapContract
}

func NewTranscendContract() *TranscendContract {
	c := &TranscendContract{
		SwapContract: *NewSwapContract(),
	}
	c.TemplateName = TEMPLATE_CONTRACT_TRANSCEND
	return c
}

func (p *TranscendContract) CheckContent() error {
	if indexer.IsPlainAsset(&p.AssetName) {
		return nil
	}

	err := p.SwapContract.CheckContent()
	if err != nil {
		return err
	}

	return nil
}

func (p *TranscendContract) InvokeParam(action string) string {

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

func (p *TranscendContract) Encode() ([]byte, error) {
	return p.SwapContract.Encode()
}

func (p *TranscendContract) Decode(data []byte) error {
	return p.SwapContract.Decode(data)
}

func (p *TranscendContract) Content() string {
	b, err := json.Marshal(p)
	if err != nil {
		Log.Errorf("Marshal TranscendContract failed, %v", err)
		return ""
	}
	return string(b)
}

func (p *TranscendContract) CalcStaticMerkleRoot() []byte {
	return CalcContractStaticMerkleRoot(p)
}

type TranscendContractRuntime struct {
	SwapContractRuntime
}

func NewTranscendContractRuntime(stp ContractManager) *TranscendContractRuntime {
	p := &TranscendContractRuntime{
		SwapContractRuntime: SwapContractRuntime{
			SwapContractRuntimeInDB: SwapContractRuntimeInDB{
				Contract: NewTranscendContract(),
				ContractRuntimeBase: ContractRuntimeBase{
					DeployTime: time.Now().Unix(),
					stp:        stp,
				},
				SwapContractRunningData: SwapContractRunningData{},
			},
		},
	}
	p.init()

	return p
}

func (p *TranscendContractRuntime) AllowDeploy() error {

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

func (p *TranscendContractRuntime) GobEncode() ([]byte, error) {
	return p.SwapContractRuntime.GobEncode()
}

func (p *TranscendContractRuntime) GobDecode(data []byte) error {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)

	var swap TranscendContract
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

// 仅用于amm合约
func (p *TranscendContractRuntime) checkSelf() error {
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

		// fix
		// if item.OrderType == ORDERTYPE_DEPOSIT || item.OrderType == ORDERTYPE_WITHDRAW {
		// 	item.ToL1 = !item.FromL1
		// 	saveContractInvokeHistoryItem(p.stp.db, url, item)
		// }
	}

	sort.Slice(mid1, func(i, j int) bool {
		return mid1[i].Id < mid1[j].Id
	})

	// 导出历史记录用于测试
	// Log.Infof("url: %s\n", url)
	// buf, _ := json.Marshal(mid1)
	// Log.Infof("items: %s\n", string(buf))
	// buf, _ = json.Marshal(p.SwapContractRunningData)
	// Log.Infof("running data: %s\n", string(buf))

	InvokeCount := int64(0)
	traderInfoMap := make(map[string]*TraderStatus)
	var runningData SwapContractRunningData
	runningData.AssetAmtInPool = nil
	runningData.SatsValueInPool = 0

	// 重新生成统计数据
	var onSendingVaue int64
	var onSendngAmt *Decimal
	for i, item := range mid1 {
		if int64(i) != item.Id {
			return fmt.Errorf("missing history. previous %d, current %d", i-1, item.Id)
		}
		// if item.Id == 61 {
		// 	Log.Infof("")
		// }

		trader, ok := traderInfoMap[item.Address]
		if !ok {
			trader = NewTraderStatus(item.Address, p.Divisibility)
			traderInfoMap[item.Address] = trader
		}
		insertItemToTraderHistroy(&trader.InvokerStatusBase, item)
		trader.DealAmt = trader.DealAmt.Add(item.OutAmt)
		trader.DealValue += CalcDealValue(item.OutValue)

		InvokeCount++
		runningData.TotalInputSats += item.InValue
		runningData.TotalInputAssets = runningData.TotalInputAssets.Add(item.InAmt)
		if item.Done != DONE_NOTYET {
			runningData.TotalOutputAssets = runningData.TotalOutputAssets.Add(item.OutAmt)
			runningData.TotalOutputSats += item.OutValue
		}

		if item.OrderType == ORDERTYPE_BUY || item.OrderType == ORDERTYPE_SELL {
			if item.Done == DONE_NOTYET {
				Log.Errorf("amm should handle item already. %v", item)
				if item.Reason == INVOKE_REASON_NORMAL {
					// 有效的，还在交易中，或者交易完成，准备发送
					if item.RemainingAmt.Sign() == 0 && item.RemainingValue == 0 {
						onSendingVaue += item.OutValue
						onSendngAmt = onSendngAmt.Add(item.OutAmt)
					}

					// 跟DONE_DEALT同样处理
					if item.OrderType == ORDERTYPE_BUY {
						runningData.TotalDealAssets = runningData.TotalDealAssets.Add(item.OutAmt)
						//runningData.TotalDealSats += item.InValue - calcSwapFee(item.InValue)
						runningData.SatsValueInPool += item.GetTradingValueForAmm()
						runningData.AssetAmtInPool = runningData.AssetAmtInPool.Sub(item.OutAmt)
					} else if item.OrderType == ORDERTYPE_SELL {
						//runningData.TotalDealAssets = runningData.TotalDealAssets.Add(item.InAmt)
						runningData.TotalDealSats += CalcDealValue(item.OutValue)
						runningData.AssetAmtInPool = runningData.AssetAmtInPool.Add(item.InAmt)
						runningData.SatsValueInPool -= CalcDealValue(item.OutValue)
					}

					Log.Infof("OnSending %d: Amt: %s-%s-%s Value: %d-%d-%d Price: %s in: %s", item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
						item.InValue, item.RemainingValue, item.OutValue, item.UnitPrice.String(), item.InUtxo)
				} else {
					// 无效的，即将退款
					Log.Infof("Refunding %d: Amt: %s-%s-%s Value: %d-%d-%d Price: %s in: %s reason: %s", item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
						item.InValue, item.RemainingValue, item.OutValue, item.UnitPrice.String(), item.InUtxo, item.Reason)
					runningData.TotalRefundAssets = runningData.TotalRefundAssets.Add(item.RemainingAmt).Add(item.OutAmt)
					runningData.TotalRefundSats += item.RemainingValue + item.OutValue
				}

			} else if item.Done == DONE_DEALT {
				if item.OrderType == ORDERTYPE_BUY {
					runningData.TotalDealAssets = runningData.TotalDealAssets.Add(item.OutAmt)
					//runningData.TotalDealSats += item.InValue - calcSwapFee(item.InValue)
					runningData.SatsValueInPool += item.GetTradingValueForAmm()
					runningData.AssetAmtInPool = runningData.AssetAmtInPool.Sub(item.OutAmt)
				} else if item.OrderType == ORDERTYPE_SELL {
					//runningData.TotalDealAssets = runningData.TotalDealAssets.Add(item.InAmt)
					runningData.AssetAmtInPool = runningData.AssetAmtInPool.Add(item.InAmt)
					runningData.SatsValueInPool -= CalcDealValue(item.OutValue)
					runningData.TotalDealSats += CalcDealValue(item.OutValue)
				}

				// 已经发送
				Log.Infof("Done %d: Amt: %s-%s-%s Value: %d-%d-%d Price: %s in: %s out: %s", item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
					item.InValue, item.RemainingValue, item.OutValue, item.UnitPrice.String(), item.InUtxo, item.OutTxId)
				runningData.TotalDealCount++
			} else if item.Done == DONE_REFUNDED {
				Log.Infof("Refund %d: Amt: %s-%s-%s Value: %d-%d-%d in: %s out: %s", item.Id, item.InAmt.String(), item.RemainingAmt.String(), item.OutAmt.String(),
					item.InValue, item.RemainingValue, item.OutValue, item.InUtxo, item.OutTxId)
				// 退款
				runningData.TotalRefundTx++
				runningData.TotalRefundAssets = runningData.TotalRefundAssets.Add(item.OutAmt)
				runningData.TotalRefundSats += item.OutValue
			}
		}

	}

	// 对比数据
	Log.Infof("OnSending: value: %d, amt: %s", onSendingVaue, onSendngAmt.String())
	Log.Infof("invokeCount %d %d", InvokeCount, p.InvokeCount)
	Log.Infof("runningData: \n%v\n%v", runningData, p.SwapContractRunningData)

	// Log.Infof("assetName: %s", p.GetAssetName())
	// amt := p.stp.GetAssetBalance_SatsNet(p.ChannelId, p.GetAssetName())
	// Log.Infof("amt: %s", amt.String())
	// value := p.stp.GetAssetBalance_SatsNet(p.ChannelId, &indexer.ASSET_PLAIN_SAT)
	// Log.Infof("value: %d", value)

	err := "different: "
	if runningData.SatsValueInPool != p.SwapContractRunningData.SatsValueInPool {
		Log.Errorf("SatsValueInPool: %d %d", runningData.SatsValueInPool, p.SwapContractRunningData.SatsValueInPool)
		err = fmt.Sprintf("%s SatsValueInPool", err)
	}
	if runningData.AssetAmtInPool.Cmp(p.SwapContractRunningData.AssetAmtInPool) != 0 {
		Log.Errorf("AssetAmtInPool: %s %s", runningData.AssetAmtInPool.String(), p.SwapContractRunningData.AssetAmtInPool.String())
		err = fmt.Sprintf("%s AssetAmtInPool", err)
	}
	if runningData.TotalDealSats != p.SwapContractRunningData.TotalDealSats {
		Log.Errorf("TotalDealSats: %d %d", runningData.TotalDealSats, p.SwapContractRunningData.TotalDealSats)
		err = fmt.Sprintf("%s TotalDealSats", err)
	}
	if runningData.TotalDealAssets.Cmp(p.SwapContractRunningData.TotalDealAssets) != 0 {
		Log.Errorf("TotalDealAssets: %s %s", runningData.TotalDealAssets.String(), p.SwapContractRunningData.TotalDealAssets.String())
		err = fmt.Sprintf("%s TotalDealAssets", err)
	}
	if runningData.TotalInputSats != p.SwapContractRunningData.TotalInputSats {
		Log.Errorf("TotalInputSats: %d %d", runningData.TotalInputSats, p.SwapContractRunningData.TotalInputSats)
		err = fmt.Sprintf("%s TotalInputSats", err)
	}
	if runningData.TotalInputAssets.Cmp(p.SwapContractRunningData.TotalInputAssets) != 0 {
		Log.Errorf("TotalInputAssets: %s %s", runningData.TotalInputAssets.String(), p.SwapContractRunningData.TotalInputAssets.String())
		err = fmt.Sprintf("%s TotalInputAssets", err)
	}

	if err == "different: " {
		return nil
	}

	// 更新统计
	// p.SwapContractRunningData = runningData
	// saveReservation(p.stp.db, &p.resv.ContractDeployDataInDB)

	Log.Errorf(err)
	return fmt.Errorf("%s", err)
}
