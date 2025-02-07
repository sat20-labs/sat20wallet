package indexer

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/btcsuite/btcd/wire"

	swire "github.com/sat20-labs/satsnet_btcd/wire"
)

// 白聪
var ASSET_PLAIN_SAT swire.AssetName = swire.AssetName{}

// offset range in a UTXO, not satoshi ordinals
type OffsetRange struct {
	Start int64
	End   int64 // 不包括End
}

func (p *OffsetRange) Clone() *OffsetRange {
	n := *p
	return &n
}

type AssetOffsets []*OffsetRange

func (p *AssetOffsets) Clone() AssetOffsets {
	result := make([]*OffsetRange, len(*p))
	for i, u := range *p {
		result[i] = &OffsetRange{Start: u.Start, End: u.End}
	}
	return result
}

func (p *AssetOffsets) Split(offset int64) (AssetOffsets, AssetOffsets) {
	var left, right []*OffsetRange

	for _, r := range *p {
		if r.End <= offset {
			// 完全在左边
			left = append(left, r)
		} else if r.Start >= offset {
			// 完全在右边
			n := r.Clone()
			n.Start -= offset
			n.End -= offset
			right = append(right, n)
		} else {
			// 跨越 offset，需要拆分
			left = append(left, &OffsetRange{Start: r.Start, End: offset})
			right = append(right, &OffsetRange{Start: 0, End: r.End-offset})
		}
	}

	return left, right
}

// another 已经调整过偏移值
func (p *AssetOffsets) Append(another AssetOffsets) {
	var r1, r2 *OffsetRange
	len1 := len(*p)
	len2 := len(another)
	if len1 > 0 {
		if len2 == 0 {
			return
		}
		r1 = (*p)[len1-1]
		r2 = another[0]
		if r1.End == r2.Start {
			r1.End = r2.End
			*p = append(*p, another[1:]...)
		} else {
			*p = append(*p, another...)
		}
	} else {
		*p = append(*p, another...)
	}
}

type TxAssets = swire.TxAssets

type TxOutput struct {
	UtxoId      uint64
	OutPointStr string
	OutValue    wire.TxOut
	//Sats        TxRanges  废弃。需要时重新获取
	Assets  TxAssets
	Offsets map[swire.AssetName]AssetOffsets
	// 注意BindingSat属性，TxOutput.OutValue.Value必须大于等于
	// Assets数组中任何一个AssetInfo.BindingSat
}

func NewTxOutput(value int64) *TxOutput {
	return &TxOutput{
		UtxoId:      INVALID_ID,
		OutPointStr: "",
		OutValue:    wire.TxOut{Value: value},
		Assets:      nil,
		Offsets:     make(map[swire.AssetName]AssetOffsets),
	}
}

func (p *TxOutput) Clone() *TxOutput {
	n := &TxOutput{
		UtxoId:      p.UtxoId,
		OutPointStr: p.OutPointStr,
		OutValue:    p.OutValue,
		Assets:      p.Assets.Clone(),
	}

	n.Offsets = make(map[swire.AssetName]AssetOffsets)
	for i, u := range p.Offsets {
		n.Offsets[i] = u.Clone()
	}
	return n
}

func (p *TxOutput) Height() int {
	if p.UtxoId == INVALID_ID {
		return -1
	}
	h, _, _ := FromUtxoId(p.UtxoId)
	return h
}

func (p *TxOutput) Value() int64 {
	return p.OutValue.Value
}

func (p *TxOutput) Zero() bool {
	return p.OutValue.Value == 0 && len(p.Assets) == 0
}

func (p *TxOutput) HasPlainSat() bool {
	if len(p.Assets) == 0 {
		return true
	}
	assetAmt := p.Assets.GetBindingSatAmout()
	return p.OutValue.Value > assetAmt
}

func (p *TxOutput) GetPlainSat() int64 {
	if len(p.Assets) == 0 {
		return p.OutValue.Value
	}
	assetAmt := p.Assets.GetBindingSatAmout()
	return p.OutValue.Value - assetAmt
}

func (p *TxOutput) OutPoint() *wire.OutPoint {
	outpoint, _ := wire.NewOutPointFromString(p.OutPointStr)
	return outpoint
}

func (p *TxOutput) OutPoint_SatsNet() *swire.OutPoint {
	outpoint, _ := swire.NewOutPointFromString(p.OutPointStr)
	return outpoint
}

func (p *TxOutput) TxOut() *wire.TxOut {
	return &wire.TxOut{
		Value: p.Value(),
		PkScript: p.OutValue.PkScript,
	}
}

func (p *TxOutput) TxOut_SatsNet() *swire.TxOut {
	return &swire.TxOut{
		Value: p.Value(),
		Assets: p.Assets,
		PkScript: p.OutValue.PkScript,
	}
}

func (p *TxOutput) TxID() string {
	parts := strings.Split(p.OutPointStr, ":")
	if len(parts) != 2 {
		return ""
	}
	return parts[0]
}

func (p *TxOutput) TxIn() *wire.TxIn {
	outpoint, err := wire.NewOutPointFromString(p.OutPointStr)
	if err != nil {
		return nil
	}
	return wire.NewTxIn(outpoint, nil, nil)
}

func (p *TxOutput) TxIn_SatsNet() *swire.TxIn {
	outpoint, err := swire.NewOutPointFromString(p.OutPointStr)
	if err != nil {
		return nil
	}
	return swire.NewTxIn(outpoint, nil, nil)
}

func (p *TxOutput) SizeOfBindingSats() int64 {
	return p.Assets.GetBindingSatAmout()
}

func (p *TxOutput) Append(another *TxOutput) error {
	if another == nil {
		return nil
	}

	if p.OutValue.Value+another.OutValue.Value < 0 {
		return fmt.Errorf("out of bounds")
	}
	value := p.OutValue.Value
	for _, asset := range another.Assets {
		p.Assets.Add(&asset)

		offsets, ok := another.Offsets[asset.Name]
		if !ok {
			// 非绑定资产没有offset
			continue
		}
		newOffsets := offsets.Clone()
		for j := 0; j < len(newOffsets); j++ {
			newOffsets[j].Start += value
			newOffsets[j].End += value
		}
		existingOffsets, ok := p.Offsets[asset.Name]
		if ok {
			existingOffsets.Append(newOffsets)
		} else {
			existingOffsets = newOffsets
		}
		p.Offsets[asset.Name] = existingOffsets
	}
	p.OutValue.Value += another.OutValue.Value

	p.UtxoId = INVALID_ID
	p.OutPointStr = ""

	return nil
}

func (p *TxOutput) Split(name *swire.AssetName, value, amt int64) (*TxOutput, *TxOutput, error) {

	if p.Value() < value {
		return nil, nil, fmt.Errorf("output value too small")
	}
	
	var value1, value2 int64
	value1 = value
	value2 = p.Value() - value1
	part1 := NewTxOutput(value1)
	part2 := NewTxOutput(value2)

	if name == nil || *name == ASSET_PLAIN_SAT {
		if p.Value() < amt {
			return nil, nil, fmt.Errorf("amount too large")
		}
		return part1, part2, nil
	}

	asset, err := p.Assets.Find(name)
	if err != nil {
		return nil, nil, err
	}
	n := asset.BindingSat
	if n != 0 {
		if amt%int64(n) != 0 {
			return nil, nil, fmt.Errorf("amt must be times of %d", n)
		}
		requiredValue := GetBindingSatNum(amt, asset.BindingSat)
		if requiredValue > value {
			return nil, nil, fmt.Errorf("value too small")
		}
	}
	

	if asset.Amount < amt {
		return nil, nil, fmt.Errorf("amount too large")
	}
	asset1 := asset.Clone()
	asset1.Amount = amt
	asset2 := asset.Clone()
	asset2.Amount = asset.Amount - amt

	part1.Assets = swire.TxAssets{*asset1}
	if asset2.Amount != 0 {
		part2.Assets = swire.TxAssets{*asset2}
	}

	if !IsBindingSat(name) {
		// runes：no offsets
		return part1, part2, nil
	}

	offsets, ok := p.Offsets[*name]
	if !ok {
		return nil, nil, fmt.Errorf("can't find asset offset")
	}
	if asset.Amount == amt {
		part1.Offsets[*name] = offsets.Clone()
		if part2.Value() == 0 {
			part2 = nil
		}
		return part1, part2, nil
	}
	offset1, offset2 := offsets.Split(GetBindingSatNum(amt, n))
	part1.Offsets[*name] = offset1
	part2.Offsets[*name] = offset2

	return part1, part2, nil
}

func (p *TxOutput) GetAssetOffset(name *swire.AssetName, amt int64) (int64, error) {

	if !IsBindingSat(name) {
		return 330, nil
	}

	if IsPlainAsset(name) {
		if p.Value() < amt {
			return 0, fmt.Errorf("amount too large")
		}
		return amt, nil
	}

	offsets, ok := p.Offsets[*name]
	if !ok {
		return 0, fmt.Errorf("no asset in %s", p.OutPointStr)
	}
	if len(offsets) == 0 {
		return 0, fmt.Errorf("no asset in %s", p.OutPointStr)
	}

	asset, err := p.Assets.Find(name)
	if err != nil {
		return 0, err
	}
	
	total := asset.Amount
	if amt > total {
		return 0, fmt.Errorf("amt too large")
	} else if amt == total {
		return offsets[len(offsets)-1].End, nil
	}

	amt = GetBindingSatNum(amt, asset.BindingSat)
	for _, off := range offsets {
		if amt >= off.End-off.Start {
			amt -= off.End - off.Start
		} else {
			return off.Start + amt, nil
		}
	}

	return 0, fmt.Errorf("offsets are wrong")
}

func (p *TxOutput) GetAsset(assetName *swire.AssetName) int64 {
	if assetName == nil || *assetName == ASSET_PLAIN_SAT {
		return p.GetPlainSat()
	}
	asset, err := p.Assets.Find(assetName)
	if err != nil {
		return 0
	}
	return asset.Amount
}

// should fill out Assets parameters.
func GenerateTxOutput(tx *wire.MsgTx, index int) *TxOutput {
	return &TxOutput{
		UtxoId:      INVALID_ID,
		OutPointStr: tx.TxHash().String() + ":" + strconv.Itoa(index),
		OutValue:    *tx.TxOut[index],
		Offsets:     make(map[swire.AssetName]AssetOffsets),
	}
}

func IsNft(assetType string) bool {
	return assetType == ASSET_TYPE_NFT || assetType == ASSET_TYPE_NS
}

func IsPlainAsset(assetName *swire.AssetName) bool {
	if assetName == nil {
		return true
	}
	return ASSET_PLAIN_SAT == *assetName
}

func IsBindingSat(name *swire.AssetName) bool {
	if name == nil {
		return true // ordx asset
	}
	if name.Protocol == PROTOCOL_NAME_ORD ||
		name.Protocol == PROTOCOL_NAME_ORDX ||
		name.Protocol == "" {
		return true
	}
	return false
}


func IsFungibleToken(name *swire.AssetName) bool {
	if name == nil {
		return true
	}
	
	return name.Type == ASSET_TYPE_FT
}

func IsOrdx(name *swire.AssetName) bool {
	if name == nil {
		return false
	}
	
	return name.Protocol == PROTOCOL_NAME_ORDX && name.Type == ASSET_TYPE_FT
}

// amt的资产需要多少聪
func GetBindingSatNum(amt int64, n uint16) int64 {
	if n == 0 {
		return 0
	}
	return (amt + int64(n) - 1)/int64(n)
}
