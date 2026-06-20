package wallet

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type ChannelInDB struct {
	Version        int
	ChannelId      string // 通道地址，作为通道ID使用，永不改变
	ShortChannelID uint64 // 通道点的短ID
	Address        string // multi-signed address
	Status         ChannelStatus

	ChannelType ChannelType
	Contract    []byte

	RedeemScript  []byte // 可以不用保存
	IsInitiator   bool
	FundingTime   int64
	PeerNodeId    []byte //*btcec.PublicKey
	Capacity      int64  // 以聪计算的最大容量
	CsvDelay      uint16
	FeeCfg        *ChannelFeeConfig // 打开通道时的费用配置
	LocalWalletId int64
	LocalChanCfg  ChannelConfigV2 // 打开通道时的配置
	RemoteChanCfg ChannelConfigV2 // 打开通道时的配置
	Memo          string

	// 如果一个output没有utxoId，说明还没有确认，不能使用该output，一层和二层都一样
	ChanPoint    *TxOutput                 // 白聪，跟随splicing更新，至少包含 MIN_SERVER_RESERVE_SATS
	FundingUtxos map[AssetName][]*TxOutput // 不包括ChanPoint
	PendingUtxos map[string]*TxOutput      // key: txId, 还没有广播 (暂时无用)
	StubUtxos    map[AssetName][]*TxOutput // 某些资产需要预先提供一个stub的utxo，方便做资产的分配。必须是330聪
	// 如果自身通道没有stub utxo，可以由服务节点提供，但需要支付一定的费用
	// channel 地址上的资产
	// 一层，已经被通道管理的utxo，按照通道最终状态分配；还不在通道管理范围的，属于通道发起方
	// 二层，所有地址上的资产，属于通道发起方；特殊通道，按照通道协议分配
	UtxosL2        map[string]*TxOutput_SatsNet // key: utxo
	PendingUtxosL2 map[string]*TxOutput_SatsNet // key: txId, 还没有广播

	LastPaymentTxId  string
	TotalSatSent     int64
	TotalSatReceived int64
	CommitHeight     int // 通道变更次数，0,1,2...
	LocalCommitment  *ChannelCommitment
	RemoteCommitment *ChannelCommitment // 属于S的部分，其中包含了miner的fee，在unlock时S代替miner收取了这部分fee

	// closed channel info
	UpdateTime int64 // resvId, time.Now().UnixMicro()
	DeAnchorTx *swire.MsgTx
	ClosingTx  *wire.MsgTx

	StaticMerkleRoot       []byte
	LocalAssetsMerkleRoot  []byte
	RemoteAssetsMerkleRoot []byte
	ChannelHash            []byte
}

func NewChannelInDB() *ChannelInDB {
	return &ChannelInDB{
		Version:        CV_INIT,
		FeeCfg:         NewFeeConfig(),
		FundingUtxos:   make(map[AssetName][]*TxOutput),
		PendingUtxos:   make(map[string]*TxOutput),
		StubUtxos:      make(map[AssetName][]*TxOutput),
		UtxosL2:        make(map[string]*TxOutput_SatsNet),
		PendingUtxosL2: make(map[string]*TxOutput_SatsNet),
	}
}

// 没有完全clone，仅用来做预估计算
func (c *ChannelInDB) Clone() *ChannelInDB {
	n := &ChannelInDB{
		Version:        c.Version,
		ChannelId:      c.Address,
		ShortChannelID: c.ShortChannelID,
		Address:        c.Address,
		Status:         c.Status,

		ChannelType: CT_NORMAL,
		Contract:    nil,

		RedeemScript:  c.RedeemScript,
		IsInitiator:   c.IsInitiator,
		FundingTime:   c.FundingTime,
		PeerNodeId:    c.PeerNodeId,
		Capacity:      c.Capacity,
		CsvDelay:      c.CsvDelay,
		FeeCfg:        c.FeeCfg,
		LocalWalletId: c.LocalWalletId,
		LocalChanCfg:  c.LocalChanCfg,
		RemoteChanCfg: c.RemoteChanCfg,
		Memo:          c.Memo,

		ChanPoint:      c.ChanPoint,
		FundingUtxos:   make(map[AssetName][]*TxOutput),
		PendingUtxos:   make(map[string]*TxOutput),
		UtxosL2:        make(map[string]*TxOutput_SatsNet),
		PendingUtxosL2: make(map[string]*TxOutput_SatsNet),

		LastPaymentTxId:  c.LastPaymentTxId,
		TotalSatSent:     c.TotalSatSent,
		TotalSatReceived: c.TotalSatReceived,
		CommitHeight:     c.CommitHeight,
		LocalCommitment: &ChannelCommitment{
			ChannelCommitmentV1: ChannelCommitmentV1{
				LocalBalance:  make(map[AssetName]*indexer.Decimal),
				RemoteBalance: make(map[AssetName]*indexer.Decimal),
			},
		},
		RemoteCommitment: &ChannelCommitment{
			ChannelCommitmentV1: ChannelCommitmentV1{
				LocalBalance:  make(map[AssetName]*indexer.Decimal),
				RemoteBalance: make(map[AssetName]*indexer.Decimal),
			},
		},

		UpdateTime: c.UpdateTime,
		DeAnchorTx: c.DeAnchorTx,
		ClosingTx:  c.ClosingTx,
	}

	for k, v := range c.FundingUtxos {
		outputs := make([]*TxOutput, 0, len(v))
		for _, u := range v {
			outputs = append(outputs, u.Clone())
		}
		n.FundingUtxos[k] = outputs
	}
	for k, v := range c.PendingUtxos {
		n.PendingUtxos[k] = v.Clone()
	}
	for k, v := range c.UtxosL2 {
		n.UtxosL2[k] = v.Clone()
	}
	for k, v := range c.PendingUtxosL2 {
		n.PendingUtxosL2[k] = v.Clone()
	}
	for k, v := range c.LocalCommitment.LocalBalance {
		n.LocalCommitment.LocalBalance[k] = v.Clone()
	}
	for k, v := range c.LocalCommitment.RemoteBalance {
		n.LocalCommitment.RemoteBalance[k] = v.Clone()
	}
	for k, v := range c.RemoteCommitment.LocalBalance {
		n.RemoteCommitment.LocalBalance[k] = v.Clone()
	}
	for k, v := range c.RemoteCommitment.RemoteBalance {
		n.RemoteCommitment.RemoteBalance[k] = v.Clone()
	}

	return n
}

func (p *ChannelInDB) GetChannelPkScript() []byte {
	r, _ := utils.WitnessScriptHash(p.RedeemScript)
	return r
}

func (p *ChannelInDB) GetLocalPkScript() []byte {
	pkScript, _ := GetP2TRpkScript(p.LocalChanCfg.PaymentKey)
	return pkScript
}

func (p *ChannelInDB) GetRemotePkScript() []byte {
	pkScript, _ := GetP2TRpkScript(p.RemoteChanCfg.PaymentKey)
	return pkScript
}

func (p *ChannelInDB) GetLocalAddress() string {
	return PublicKeyToP2TRAddress(p.LocalChanCfg.PaymentKey)
}

func (p *ChannelInDB) GetRemoteAddress() string {
	return PublicKeyToP2TRAddress(p.RemoteChanCfg.PaymentKey)
}

func (p *ChannelInDB) GetLocalPubKey() *btcec.PublicKey {
	return p.LocalChanCfg.PaymentKey
}

func (p *ChannelInDB) GetRemotePubKey() *btcec.PublicKey {
	return p.RemoteChanCfg.PaymentKey
}

func (p *ChannelInDB) GetHeight() int {
	h, _, _ := indexer.FromUtxoId(p.ShortChannelID)
	return h
}

func (p *ChannelInDB) IsReady() bool {
	return p.Status == CS_READY
}

// 当前通道点，跟随splicing更新
func (p *ChannelInDB) GetChanPoint() *TxOutput {
	return p.ChanPoint
}

func (p *ChannelInDB) SetChanPoint(chanPoint *TxOutput) {
	p.ChanPoint = chanPoint
}

func (p *ChannelInDB) IsLiquidPoolExisting(assetName *AssetName) bool {
	if !IsLPT(assetName) {
		return false
	}
	outputs := p.GetFundingOutputs(assetName)
	return len(outputs) != 0
}

func (p *ChannelInDB) GetFundingOutputWithProtocol(protocol string) []*AssetToOutput {
	// 需要确保输出是有序的
	result := make([]*AssetToOutput, 0)

	for k, v := range p.FundingUtxos {
		if k.Protocol != protocol {
			continue
		}
		result = append(result, &AssetToOutput{
			AssetName: &k,
			Outputs:   v,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].AssetName.String() < result[j].AssetName.String()
	})

	return result
}

func (p *ChannelInDB) GetAllFundingOutput() []*AssetToOutput {
	// 需要确保输出是有序的
	result := make([]*AssetToOutput, 0)

	for k, v := range p.FundingUtxos {
		outputs := make([]*TxOutput, 0)
		for _, o := range v {
			outputs = append(outputs, o.Clone())
		}
		sort.Slice(outputs, func(i, j int) bool {
			if outputs[i].OutValue.Value == outputs[j].OutValue.Value {
				return outputs[i].UtxoId < outputs[j].UtxoId
			}
			return outputs[i].OutValue.Value < outputs[j].OutValue.Value
		})
		result = append(result, &AssetToOutput{
			AssetName: &k,
			Outputs:   outputs,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].AssetName.String() < result[j].AssetName.String()
	})

	return result
}

func (p *ChannelInDB) GetFundingOutputs(name *AssetName) []*TxOutput {
	result := p.FundingUtxos[*name]
	if name.AssetName == ASSET_PLAIN_SAT {
		result = append(result, p.ChanPoint)
	}
	return result
}

func (p *ChannelInDB) GetFundingOutput(utxo string) *TxOutput {
	if utxo == p.ChanPoint.OutPointStr {
		return p.ChanPoint.Clone()
	}
	for _, v := range p.FundingUtxos {
		for _, o := range v {
			if utxo == o.OutPointStr {
				return o.Clone()
			}
		}
	}
	return nil
}

func (p *ChannelInDB) GetManagedAssetAmount(name *AssetName) *Decimal {
	var result *Decimal
	amt1, ok := p.LocalCommitment.LocalBalance[*name]
	if ok {
		result = result.Add(amt1)
	}

	amt2, ok := p.LocalCommitment.LocalBalance[*name]
	if ok {
		result = result.Add(amt2)
	}
	return result
}

func (p *ChannelInDB) AddFundingOutput(output *TxOutput, name *AssetName) error {
	// 为了效率，要先确保output中没有其他无关资产
	n := output.Clone()
	if name.Protocol == indexer.PROTOCOL_NAME_BRC20 {
		n.Invalids[name.AssetName] = true
	}
	uv := p.FundingUtxos[*name]
	uv = append(uv, n)

	sort.Slice(uv, func(i, j int) bool {
		return uv[i].UtxoId < uv[j].UtxoId
	})

	p.FundingUtxos[*name] = uv
	return nil
}

func (p *ChannelInDB) RemoveFundingOutput(outputs []*TxOutput, name *AssetName) {

	uv := p.FundingUtxos[*name]

	// brc20先更新保存的output资产；如果该资产已经被完全移出，也要清理旧funding记录。
	if name.Protocol == indexer.PROTOCOL_NAME_BRC20 {
		for _, removed := range outputs {
			amt := removed.GetAsset(&name.AssetName)
			for _, existing := range uv {
				amt, _ = existing.RemoveAssetWithAmt(&name.AssetName, amt)
				if amt.Sign() == 0 {
					break
				}
			}
		}
		p.cleanupFundingOutputsWithoutAsset(*name)

		return
	}

	utxoMap := make(map[string]*TxOutput)
	for _, u := range uv {
		utxoMap[u.OutPointStr] = u
	}
	bPlainAsset := indexer.IsPlainAsset(&name.AssetName)
	if bPlainAsset {
		utxoMap[p.ChanPoint.OutPointStr] = p.ChanPoint
	}

	for _, u := range outputs {
		delete(utxoMap, u.OutPointStr)
	}

	if bPlainAsset {
		_, ok := utxoMap[p.ChanPoint.OutPointStr]
		if ok {
			// chanpoint没有被花费
			delete(utxoMap, p.ChanPoint.OutPointStr)
		} else {
			// spent, set back in AddFundingOutput
			p.ChanPoint = nil
		}
	}

	uv = make([]*TxOutput, len(utxoMap))
	i := 0
	for _, v := range utxoMap {
		uv[i] = v
		i++
	}
	sort.Slice(uv, func(i, j int) bool {
		return uv[i].OutPointStr < uv[j].OutPointStr
	})

	p.FundingUtxos[*name] = uv
}

func (p *ChannelInDB) cleanupFundingOutputsWithoutAsset(name AssetName) bool {
	uv, ok := p.FundingUtxos[name]
	if !ok {
		return false
	}

	filtered := make([]*TxOutput, 0, len(uv))
	updated := false
	for _, u := range uv {
		asset, err := u.Assets.Find(&name.AssetName)
		if err != nil || asset.Amount.Sign() == 0 {
			Log.Warnf("remove stale funding utxo %s without asset %s", u.OutPointStr, name.String())
			updated = true
			continue
		}
		filtered = append(filtered, u)
	}

	if !updated {
		return false
	}
	if len(filtered) == 0 {
		delete(p.FundingUtxos, name)
	} else {
		p.FundingUtxos[name] = filtered
	}
	return true
}

func (p *ChannelInDB) CleanupFundingOutputsWithoutAsset(name AssetName) bool {
	return p.cleanupFundingOutputsWithoutAsset(name)
}

func (p *ChannelInDB) GetCommitLocalBalance() map[AssetName]*indexer.Decimal {
	result := make(map[AssetName]*indexer.Decimal)
	for k, v := range p.LocalCommitment.LocalBalance {
		result[k] = v.Clone()
	}
	return result
}

func (p *ChannelInDB) GetCommitRemoteBalance() map[AssetName]*indexer.Decimal {
	result := make(map[AssetName]*indexer.Decimal)
	for k, v := range p.LocalCommitment.RemoteBalance {
		result[k] = v.Clone()
	}
	return result
}

func (p *ChannelInDB) GetCommitLocalValue(name *AssetName) *indexer.Decimal {
	return p.LocalCommitment.LocalBalance[*name].Clone()
}

func (p *ChannelInDB) GetCommitRemoteValue(name *AssetName) *indexer.Decimal {
	return p.LocalCommitment.RemoteBalance[*name].Clone()
}

func (p *ChannelInDB) SetCapacity(newCap int64) {
	p.Capacity = newCap
}

func (p *ChannelInDB) SetCommitLocalValue(name *AssetName, value *indexer.Decimal) {
	if value == nil {
		delete(p.LocalCommitment.LocalBalance, *name)
		delete(p.RemoteCommitment.RemoteBalance, *name)
	} else {
		p.LocalCommitment.LocalBalance[*name] = value.Clone()
		p.RemoteCommitment.RemoteBalance[*name] = value.Clone()
	}
}

func (p *ChannelInDB) SetCommitRemoteValue(name *AssetName, value *indexer.Decimal) {
	if value == nil {
		delete(p.LocalCommitment.RemoteBalance, *name)
		delete(p.RemoteCommitment.LocalBalance, *name)
	} else {
		p.LocalCommitment.RemoteBalance[*name] = value.Clone()
		p.RemoteCommitment.LocalBalance[*name] = value.Clone()
	}
}

func (p *ChannelInDB) GetValidValue_SatsNet() *TxOutput_SatsNet {
	result := TxOutput_SatsNet{}
	for _, u := range p.UtxosL2 {
		if result.OutValue.Assets == nil {
			result.OutValue.Assets = u.OutValue.Assets.Clone()
		} else {
			result.OutValue.Assets.Merge(u.OutValue.Assets)
		}

		result.OutValue.Value += u.OutValue.Value
		if result.OutValue.PkScript == nil {
			result.OutValue.PkScript = u.OutValue.PkScript
		}
	}
	return &result
}

func (p *ChannelInDB) GetAllValue_SatsNet() *TxOutput_SatsNet {
	result := TxOutput_SatsNet{}
	for _, u := range p.UtxosL2 {
		if result.OutValue.Assets == nil {
			result.OutValue.Assets = u.OutValue.Assets.Clone()
		} else {
			result.OutValue.Assets.Merge(u.OutValue.Assets)
		}

		result.OutValue.Value += u.OutValue.Value
		if result.OutValue.PkScript == nil {
			result.OutValue.PkScript = u.OutValue.PkScript
		}
	}
	for _, u := range p.PendingUtxosL2 {
		if result.OutValue.Assets == nil {
			result.OutValue.Assets = u.OutValue.Assets.Clone()
		} else {
			result.OutValue.Assets.Merge(u.OutValue.Assets)
		}

		result.OutValue.Value += u.OutValue.Value
		if result.OutValue.PkScript == nil {
			result.OutValue.PkScript = u.OutValue.PkScript
		}
	}
	return &result
}

func (p *ChannelInDB) GetValidOutput_SatsNet() []*TxOutput_SatsNet {
	// 需要确保输出是有序的
	result := make([]*TxOutput_SatsNet, 0)
	for _, u := range p.UtxosL2 {
		result = append(result, u.Clone())
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].OutPointStr < result[j].OutPointStr
	})

	return result
}

func (p *ChannelInDB) GetPendingOutput_SatsNet() []*TxOutput_SatsNet {
	// 需要确保输出是有序的
	result := make([]*TxOutput_SatsNet, 0)
	for _, u := range p.PendingUtxosL2 {
		result = append(result, u.Clone())
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].OutPointStr < result[j].OutPointStr
	})

	return result
}

func (p *ChannelInDB) GetAllOutput_SatsNet() []*TxOutput_SatsNet {
	// 需要确保输出是有序的
	result := make([]*TxOutput_SatsNet, 0)
	for _, u := range p.UtxosL2 {
		result = append(result, u.Clone())
	}
	for _, u := range p.PendingUtxosL2 {
		result = append(result, u.Clone())
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].OutPointStr < result[j].OutPointStr
	})

	return result
}

func (p *ChannelInDB) AddUtxo_SatsNet(output *TxOutput_SatsNet) {
	if output == nil {
		return
	}
	p.UtxosL2[output.OutPointStr] = output
}

func (p *ChannelInDB) UpdateUtxosByPendingTx_SatsNet(tx *swire.MsgTx) {
	for _, txIn := range tx.TxIn {
		delete(p.UtxosL2, txIn.PreviousOutPoint.String())
	}
	output := sindexer.GenerateTxOutput(tx, 0)
	p.PendingUtxosL2[tx.TxID()] = output
	Log.Infof("L2 utxo %s is pending", output.OutPointStr)
}

// 调用 UpdateUtxosByPendingTxL2 后需要调用 EnableUtxo_SatsNet
func (p *ChannelInDB) EnableUtxo_SatsNet(txId string) {
	u, ok := p.PendingUtxosL2[txId]
	if ok {
		delete(p.PendingUtxosL2, txId)
		p.UtxosL2[u.OutPointStr] = u
		Log.Infof("L2 utxo %s is enabled", u.OutPointStr)
	}
}

func (p *ChannelInDB) GetCommitmentPrefetchor() *txscript.MultiPrevOutFetcher {
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	for _, uv := range p.FundingUtxos {
		for _, u := range uv {
			prevFetcher.AddPrevOut(*u.OutPoint(), &u.OutValue)
		}
	}
	prevFetcher.AddPrevOut(*p.ChanPoint.OutPoint(), &p.ChanPoint.OutValue)
	for _, uv := range p.StubUtxos {
		for _, u := range uv {
			prevFetcher.AddPrevOut(*u.OutPoint(), &u.OutValue)
		}
	}
	return prevFetcher
}

func (p *ChannelInDB) HasProtocolAsset(protocol string) bool {
	for k := range p.FundingUtxos {
		if k.Protocol == protocol {
			return true
		}
	}

	return false
}

// unlock: 通道内，从A->S
func (p *ChannelInDB) HasEnoughAssetForUnlock(assetName *AssetName, amt *indexer.Decimal,
	hasFee, bFromInitiator bool) error {
	nAmt := amt.Clone()
	gas := indexer.NewDefaultDecimal(DEFAULT_FEE_SATSNET)
	if indexer.IsPlainAsset(&assetName.AssetName) {
		if !hasFee {
			nAmt = nAmt.Add(gas)
		}
	} else {
		if !hasFee {
			return fmt.Errorf("should provide fee")
			// err := p.HasEnoughAsset(&PLAIN_ASSET, gas, hasFee, bFromInitiator)
			// if err != nil {
			// 	return err
			// }
			// err = p.HasEnoughCapacity(&PLAIN_ASSET, gas, hasFee, !bFromInitiator)
			// if err != nil {
			// 	return err
			// }
		}
	}

	// 本地需要有足够的资产
	err := p.HasEnoughAsset(assetName, nAmt, hasFee, bFromInitiator)
	if err != nil {
		return err
	}
	// peer需要有足够的空间
	return p.HasEnoughCapacity(assetName, nAmt, hasFee, !bFromInitiator)
}

// lock: 通道内，从S->A
func (p *ChannelInDB) HasEnoughCapacityForLock(assetName *AssetName, amt *indexer.Decimal,
	hasFee, bFromInitiator bool) error {
	nAmt := amt.Clone()
	gas := indexer.NewDefaultDecimal(DEFAULT_FEE_SATSNET)
	if indexer.IsPlainAsset(&assetName.AssetName) {
		if !hasFee {
			if nAmt.Int64() < DEFAULT_FEE_SATSNET {
				return fmt.Errorf("not enough sats")
			}
			nAmt = nAmt.Sub(gas)
		}
	} else {
		if !hasFee {
			return fmt.Errorf("should provide fee")
			// A->S
			// err := p.HasEnoughAsset(&PLAIN_ASSET, gas, hasFee, bFromInitiator)
			// if err != nil {
			// 	return err
			// }
			// err = p.HasEnoughCapacity(&PLAIN_ASSET, gas, hasFee, !bFromInitiator)
			// if err != nil {
			// 	return err
			// }
		}
	}

	// peer需要有足够的资产
	err := p.HasEnoughAsset(assetName, nAmt, hasFee, !bFromInitiator)
	if err != nil {
		return err
	}
	// 本地需要有足够的空间
	return p.HasEnoughCapacity(assetName, nAmt, hasFee, bFromInitiator)
}

// 分别对两个端点进行判断，方便在各种情况下进行组合.
// 比如unlock, 需要同时判断本地有足够的asset，而远端有足够的capacity。如果是splicing-out，只需要判断本地有足够的asset。
// 因为utxo的限制，utxo的值，只能是0或者大于等于330，不允许取值中间
// 这是为了确保在广播承诺交易，或者关闭交易时，回到一层的聪可以正确被冻结，不多也不少
// 根据是否存在feeutxo，自行调整amt的数据，但只有资产是白聪才
func (p *ChannelInDB) HasEnoughAsset(assetName *AssetName, amt *indexer.Decimal,
	hasFee, bFromInitiator bool) error {
	var fromValue *indexer.Decimal
	var fromResvSats int64
	bIsPlainAsset := IsPlainAsset(assetName)
	bIsBindingSat := IsBindingSat(assetName)
	if p.IsInitiator == bFromInitiator {
		fromValue = p.GetCommitLocalValue(assetName)
	} else {
		fromValue = p.GetCommitRemoteValue(assetName)
	}

	if fromValue.IsZero() {
		return fmt.Errorf("no assets")
	}

	expected := amt.Clone()
	if bIsPlainAsset {
		if bFromInitiator {
			fromResvSats = p.FeeCfg.CommitmentFee
		} else {
			fromResvSats = p.FeeCfg.MinReserveSats
		}
	}

	// 只保证localvalue的值大于 localResvSats
	if bIsBindingSat {
		// valid balance: 0,1,2... (+localResvSats)

		// 聪网中每一聪可以携带少于N的资产
		// if amt%int64(assetName.N) != 0 {
		// 	return fmt.Errorf("amt should be times of %d", assetName.N)
		// }

		// 先转为sats数量
		fromSatsNum := GetBindingSatNum(fromValue, (assetName.N))
		expectedSatsNum := GetBindingSatNum(expected, (assetName.N))
		fromResvSats = GetBindingSatNum(indexer.NewDefaultDecimal(fromResvSats), (assetName.N))

		Log.Infof("fromSatsNum %d", fromSatsNum)
		Log.Infof("expectedSatsNum %d", expectedSatsNum)
		Log.Infof("fromResvSats %d", fromResvSats)

		if fromSatsNum-expectedSatsNum >= fromResvSats {
			return nil
		}
		return fmt.Errorf("only %d assets can be used", (fromSatsNum-fromResvSats)*int64(assetName.N))
	} else {
		if fromValue.Cmp(expected) < 0 {
			return fmt.Errorf("only %s assets can be used", fromValue.String())
		}
	}

	return nil
}

// who want to add amt asset
func (p *ChannelInDB) HasEnoughCapacity(assetName *AssetName, amt *indexer.Decimal,
	hasFee, bFromInitiator bool) error {
	var toValue *indexer.Decimal
	total := indexer.DecimalAdd(p.GetCommitLocalValue(assetName), p.GetCommitRemoteValue(assetName))
	if p.IsInitiator == bFromInitiator {
		toValue = p.GetCommitLocalValue(assetName)
	} else {
		toValue = p.GetCommitRemoteValue(assetName)
	}
	expected := indexer.DecimalAdd(amt, toValue)

	if total.Cmp(expected) < 0 {
		return fmt.Errorf("no enough capacity for %s asset %s", assetName.String(), amt.String())
	}

	return nil
}

// 本地可用的白聪，用于解锁，参考HasEnoughAsset
func (p *ChannelInDB) GetAvalaiblePlainSats() int64 {
	var fromValue *indexer.Decimal
	var fromResvSats int64

	if p.IsInitiator {
		fromValue = p.GetCommitLocalValue(&PLAIN_ASSET)
		fromResvSats = p.FeeCfg.CommitmentFee
	} else {
		fromValue = p.GetCommitRemoteValue(&PLAIN_ASSET)
		fromResvSats = p.FeeCfg.MinReserveSats
	}

	if fromValue.IsZero() {
		return 0
	}

	return fromValue.Int64() - fromResvSats
}

func (p *ChannelInDB) NeedStubUtxo(name *AssetName) (int, int64) {
	if c, v := NeedStubUtxoForChannel(&name.AssetName); c > 0 {
		stubs := p.GetStubUtxoForAsset(name)
		if len(stubs) < c {
			return c - len(stubs), v
		}
	}
	return 0, 0
}

func (p *ChannelInDB) GetStubUtxoForAsset(name *AssetName) []*TxOutput {
	if name.Protocol == indexer.PROTOCOL_NAME_RUNES {
		// 符文的stub不区分资产名称
		n := *name
		n.Ticker = ""
		return p.StubUtxos[n]
	}
	return p.StubUtxos[*name]
}

func (p *ChannelInDB) GetStubUtxos(protocol string) map[AssetName][]*TxOutput {
	result := make(map[AssetName][]*TxOutput)
	for k, v := range p.StubUtxos {
		if protocol == "" {
			result[k] = v
		} else if k.Protocol == protocol {
			result[k] = v
		}
	}
	return result
}

// 排除brc20的stub
func (p *ChannelInDB) GetStubUtxoList() []*TxOutput {
	result := make([]*TxOutput, 0)
	for k, v := range p.StubUtxos {
		if k.Protocol == indexer.PROTOCOL_NAME_BRC20 {
			continue
		}
		result = append(result, v...)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].OutPointStr < result[j].OutPointStr
	})
	return result
}

func (p *ChannelInDB) SetStubUtxoForAsset(output []*TxOutput, name *AssetName) {
	if name.Protocol == indexer.PROTOCOL_NAME_RUNES {
		n := *name
		n.Ticker = ""
		p.StubUtxos[n] = output
	} else {
		p.StubUtxos[*name] = output
	}
}

func (p *ChannelInDB) UtxosInControl() map[string]bool {
	utxosInControl := make(map[string]bool)
	utxosInControl[p.ChanPoint.OutPointStr] = true

	for _, uv := range p.FundingUtxos {
		for _, u := range uv {
			utxosInControl[u.OutPointStr] = true
		}
	}
	for _, v := range p.PendingUtxos {
		utxosInControl[v.OutPointStr] = true
	}
	for _, uv := range p.StubUtxos {
		for _, u := range uv {
			utxosInControl[u.OutPointStr] = true
		}
	}

	return utxosInControl
}

func (p *ChannelInDB) UtxosInControlV2() map[string]*TxOutput {
	utxosInControl := make(map[string]*TxOutput)
	utxosInControl[p.ChanPoint.OutPointStr] = p.ChanPoint

	for _, uv := range p.FundingUtxos {
		for _, u := range uv {
			utxosInControl[u.OutPointStr] = u
		}
	}
	for _, v := range p.PendingUtxos {
		utxosInControl[v.OutPointStr] = v
	}
	for _, uv := range p.StubUtxos {
		for _, u := range uv {
			utxosInControl[u.OutPointStr] = u
		}
	}

	return utxosInControl
}

func (p *ChannelInDB) InControl(utxos []string) []string {
	utxosInControl := p.UtxosInControl()

	result := make([]string, 0)
	for _, utxo := range utxos {
		_, ok := utxosInControl[utxo]
		if ok {
			result = append(result, utxo)
		}
	}
	return result
}

func (p *ChannelInDB) NotInControl(utxos []string) []string {
	utxosInControl := p.UtxosInControl()

	result := make([]string, 0)
	for _, utxo := range utxos {
		_, ok := utxosInControl[utxo]
		if !ok {
			result = append(result, utxo)
		}
	}
	return result
}

func (p *ChannelInDB) UtxosInControl_SatsNet() map[string]bool {
	utxosInControl := make(map[string]bool)
	for _, u := range p.UtxosL2 {
		utxosInControl[u.OutPointStr] = true
	}
	for _, u := range p.PendingUtxosL2 {
		utxosInControl[u.OutPointStr] = true
	}

	return utxosInControl
}

func (p *ChannelInDB) NotInControl_SatsNet(utxos []string) []string {
	utxosInControl := p.UtxosInControl_SatsNet()

	result := make([]string, 0)
	for _, utxo := range utxos {
		_, ok := utxosInControl[utxo]
		if !ok {
			result = append(result, utxo)
		}
	}
	return result
}

func (p *ChannelInDB) CalcStaticMerkleRoot() []byte {
	return CalcChannelStaticMerkleRoot(p)
}

func (p *ChannelInDB) CalcAssetsMerkleRoot() {
	p.CalcLocalAssetsMerkleRoot()
	p.CalcRemoteAssetsMerkleRoot()
}

func (p *ChannelInDB) CalcLocalAssetsMerkleRoot() []byte {
	return CalcChannelLocalAssetsMerkleRoot(p)
}

func (p *ChannelInDB) CalcRemoteAssetsMerkleRoot() []byte {
	return CalcChannelRemoteAssetsMerkleRoot(p)
}

func (p *ChannelInDB) CalcHash() []byte {

	return CalcChannelHash(p)
}

func (p *ChannelInDB) CheckConsistence() bool {
	return MapEqual(p.LocalCommitment.LocalBalance, p.RemoteCommitment.RemoteBalance) &&
		MapEqual(p.LocalCommitment.RemoteBalance, p.RemoteCommitment.LocalBalance)
}

func (p *ChannelInDB) PrepareForSave() error {

	// check consistence
	if !p.CheckConsistence() {
		Log.Errorf("channel %s is inconsistent", p.ChannelId)
		return fmt.Errorf("channel %s is inconsistent", p.ChannelId)
	}
	newStaticMerkleRoot := p.CalcStaticMerkleRoot()
	Log.Infof("channel %v %s static hash %s", p.IsInitiator, p.ChannelId, hex.EncodeToString(newStaticMerkleRoot))
	if !bytes.Equal(newStaticMerkleRoot, p.StaticMerkleRoot) {
		Log.Errorf("channel %s is modified", p.ChannelId)
		return fmt.Errorf("channel %s is modified", p.ChannelId)
	}

	//if p.StaticMerkleRoot == nil {
	// 创建后就不会变
	// p.StaticMerkleRoot = p.CalcStaticMerkleRoot()
	//}
	p.LocalAssetsMerkleRoot = p.CalcLocalAssetsMerkleRoot()
	p.RemoteAssetsMerkleRoot = p.CalcRemoteAssetsMerkleRoot()
	p.ChannelHash = p.CalcHash()
	Log.Infof("channel %s hash %s", p.ChannelId, hex.EncodeToString(p.ChannelHash))
	return nil
}

func (p *ChannelInDB) CheckMerkleRoot() error {

	// check consistence
	if !p.CheckConsistence() {
		return fmt.Errorf("channel %s is inconsistent", p.ChannelId)
	}
	newStaticMerkleRoot := p.CalcStaticMerkleRoot()
	Log.Infof("channel %v %s static hash %s", p.IsInitiator, p.ChannelId, hex.EncodeToString(newStaticMerkleRoot))
	if !bytes.Equal(newStaticMerkleRoot, p.StaticMerkleRoot) {
		return fmt.Errorf("channel %s is modified", p.ChannelId)
	}

	newAssetMerkleRoot := p.CalcLocalAssetsMerkleRoot()
	Log.Infof("channel %v %s local asset hash %s", p.IsInitiator, p.ChannelId, hex.EncodeToString(newAssetMerkleRoot))
	if !bytes.Equal(newAssetMerkleRoot, p.LocalAssetsMerkleRoot) {
		return fmt.Errorf("channel %s is modified", p.ChannelId)
	}

	newAssetMerkleRoot = p.CalcRemoteAssetsMerkleRoot()
	Log.Infof("channel %v %s remote asset hash %s", p.IsInitiator, p.ChannelId, hex.EncodeToString(newAssetMerkleRoot))
	if !bytes.Equal(newAssetMerkleRoot, p.RemoteAssetsMerkleRoot) {
		return fmt.Errorf("channel %s is modified", p.ChannelId)
	}

	return nil
}

// 检查自身数据的一致性
func (p *ChannelInDB) CheckData() bool {
	ok, _ := p.checkData()
	return ok
}

func (p *ChannelInDB) CheckDataWithCleanup() (bool, bool) {
	return p.checkData()
}

func (p *ChannelInDB) checkData() (bool, bool) {
	updated := false
	if p.Status <= CS_CLOSED {
		return true, updated
	}

	// fix nil map
	if p.StubUtxos == nil {
		p.StubUtxos = make(map[AssetName][]*TxOutput)
		updated = true
	}
	if p.PendingUtxos == nil {
		p.PendingUtxos = make(map[string]*TxOutput)
		updated = true
	}
	if p.PendingUtxosL2 == nil {
		p.PendingUtxosL2 = make(map[string]*TxOutput_SatsNet)
		updated = true
	}
	if p.FeeCfg == nil {
		p.FeeCfg = NewOldFeeConfig()
		updated = true
	}

	oldHash := p.ChannelHash
	newHash := p.CalcHash()
	p.ChannelHash = oldHash
	//Log.Infof("channel %s old hash %s, new hash %s", p.ChannelId, hex.EncodeToString(oldHash), hex.EncodeToString(newHash))
	if !bytes.Equal(oldHash, newHash) {
		Log.Errorf("channel hash different! %s old %s, new %s", p.ChannelId, hex.EncodeToString(oldHash), hex.EncodeToString(newHash))
		return false, updated
	}

	pkScript := p.GetChannelPkScript()
	localBalanceMap := p.GetCommitLocalBalance()
	remoteBalanceMap := p.GetCommitRemoteBalance()
	for k, uv := range p.FundingUtxos {
		local := localBalanceMap[k]
		remote := remoteBalanceMap[k]
		totalBalance := indexer.DecimalAdd(local, remote)
		isPlainAsset := IsPlainAsset(&k)

		var satsCap int64
		assetCap := indexer.NewDefaultDecimal(0)
		for _, u := range uv {
			if isPlainAsset {
				satsCap += u.Value()
			} else {
				asset, err := u.Assets.Find(&k.AssetName)
				if err != nil {
					if totalBalance == nil || totalBalance.Sign() == 0 {
						Log.Warnf("utxo %s has no asset %s but total balance is zero", u.OutPointStr, k.String())
						if p.cleanupFundingOutputsWithoutAsset(k) {
							updated = true
						}
						continue
					}
					Log.Panicf("utxo %s has no asset %s", u.OutPointStr, k.String())
				}
				assetCap = assetCap.Add(&asset.Amount)
			}
			if !bytes.Equal(u.OutValue.PkScript, pkScript) {
				Log.Panicf("utxo %s is not in channel %s", u.OutPointStr, p.ChannelId)
			}
		}

		if isPlainAsset {
			satsCap += p.ChanPoint.Value()
			d := satsCap - totalBalance.Int64()
			if d != 0 {
				Log.Errorf("channel %s plain sats cap check failed. %d %d", p.ChannelId, satsCap, totalBalance.Int64())
				return false, updated
			}
		} else {
			d := indexer.DecimalSub(assetCap, totalBalance)
			if d.Sign() != 0 {
				Log.Errorf("channel %s asset %s cap check failed. %s %s", p.ChannelId, k.String(), assetCap.String(), totalBalance.String())
				return false, updated
			}
		}
	}

	// 如果是私人通道（节点没有一方是引导节点），L2上的资产，都是属于initiator
	bootstrap := GetBootstrapPubKey()
	if !bootstrap.IsEqual(p.GetLocalPubKey()) && !bootstrap.IsEqual(p.GetRemotePubKey()) {
		var initiatorBalance map[AssetName]*indexer.Decimal
		if p.IsInitiator {
			initiatorBalance = localBalanceMap
		} else {
			initiatorBalance = remoteBalanceMap
		}

		var output *TxOutput_SatsNet
		for _, v := range p.UtxosL2 {
			if output == nil {
				output = v.Clone()
			} else {
				output.Merge(v)
			}
		}
		for _, v := range p.PendingUtxosL2 {
			if output == nil {
				output = v.Clone()
			} else {
				output.Merge(v)
			}
		}

		for name, amt := range initiatorBalance {
			amt2 := output.GetAsset(&name.AssetName)
			if amt.Cmp(amt2) > 0 { // 聪网地址上可能会有多余的资产，但不能少于通道记账结果
				Log.Errorf("channel %s L2 asset %s different: %s %s", p.ChannelId, name.String(), amt.String(), amt2.String())
				return false, updated
			}
		}
	}

	Log.Infof("channel %s checked! local commit tx %s, remote commit tx %s",
		p.ChannelId, p.LocalCommitment.CommitTx.TxID(), p.RemoteCommitment.CommitTx.TxID())

	return true, updated
}
