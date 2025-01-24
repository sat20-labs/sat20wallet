package sindexer

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sat20-labs/sat20wallet/sdk/wallet/indexer"
	"github.com/sat20-labs/satsnet_btcd/wire"
)

type AssetInfo = wire.AssetInfo

var ASSET_PLAIN_SAT wire.AssetName = wire.AssetName{}


type TxOutput struct {
	OutPointStr string
	OutValue    wire.TxOut
	// 注意BindingSat属性，TxOutput.OutValue.Value必须大于等于
	// Assets数组中任何一个AssetInfo.BindingSat
}

func NewTxOutput(value int64) *TxOutput {
	return  &TxOutput{
		OutPointStr: "",
		OutValue:    wire.TxOut{Value:value},
	}
}

func CloneTxOut(a *wire.TxOut) *wire.TxOut {
	n := &wire.TxOut{
		Value: a.Value,
		Assets: a.Assets.Clone(),
	}
	n.PkScript = make([]byte, len(a.PkScript))
	copy(n.PkScript, a.PkScript)

	return n
}

func (p *TxOutput) Clone() *TxOutput {
	return &TxOutput{
		OutPointStr: p.OutPointStr,
		OutValue: *CloneTxOut(&p.OutValue),
	}
}

func (p *TxOutput) Value() int64 {
	return p.OutValue.Value
}

func (p *TxOutput) Zero() bool {
	return p.OutValue.Value == 0 && len(p.OutValue.Assets) == 0
}

func (p *TxOutput) HasPlainSat() bool {
	if len(p.OutValue.Assets) == 0 {
		return p.OutValue.Value > 0
	}
	assetAmt := p.OutValue.Assets.GetBindingSatAmout()
	return p.OutValue.Value > assetAmt
}

func (p *TxOutput) GetPlainSat() int64 {
	if len(p.OutValue.Assets) == 0 {
		return p.OutValue.Value
	}
	assetAmt := p.OutValue.Assets.GetBindingSatAmout()
	return p.OutValue.Value - assetAmt
}

func (p *TxOutput) OutPoint() *wire.OutPoint {
	outpoint, _ := wire.NewOutPointFromString(p.OutPointStr)
	return outpoint
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

func (p *TxOutput) SizeOfBindingSats() int64 {
	bindingSats := int64(0)
	for _, asset := range p.OutValue.Assets {
		amount := int64(0)
		if asset.BindingSat != 0 {
			amount = (asset.Amount)
		}

		if amount > (bindingSats) {
			bindingSats = amount
		}
	}
	return bindingSats
}


func (p *TxOutput) Merge(another *TxOutput) error {
	if another == nil {
		return nil
	}

	if p.OutValue.Value + another.OutValue.Value < 0 {
		return fmt.Errorf("out of bounds")
	}
	p.OutValue.Value += another.OutValue.Value
	return p.OutValue.Assets.Merge(&another.OutValue.Assets)
}

func (p *TxOutput) Subtract(another *TxOutput) error {
	if another == nil {
		return nil
	}

	if p.OutValue.Value < another.OutValue.Value {
		return fmt.Errorf("can't split")
	}

	tmpAssets := p.OutValue.Assets.Clone()
	err := tmpAssets.Split(&another.OutValue.Assets)
	if err != nil {
		return err
	}
	bindingSat := tmpAssets.GetBindingSatAmout()
	if p.OutValue.Value - another.OutValue.Value < bindingSat {
		return fmt.Errorf("no enough sats")
	}
	
	p.OutValue.Value -= another.OutValue.Value
	p.OutValue.Assets = tmpAssets

	return nil
}

func (p *TxOutput) Split(name *wire.AssetName, value, amt int64) (*TxOutput, *TxOutput, error) {

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

	asset, err := p.OutValue.Assets.Find(name)
	if err != nil {
		return nil, nil, err
	}

	if asset.Amount < amt {
		return nil, nil, fmt.Errorf("amount too large")
	}
	asset1 := asset.Clone()
	asset1.Amount = amt
	asset2 := asset.Clone()
	asset2.Amount = asset.Amount - amt

	part1.OutValue.Assets = wire.TxAssets{*asset1}
	part2.OutValue.Assets = wire.TxAssets{*asset2}

	if indexer.IsBindingSat(name) == 0 {
		// runes：no offsets
		return part1, part2, nil
	}

	if asset.Amount == amt {
		return part1, nil, nil
	}

	return part1, part2, nil
}

func (p *TxOutput) GetAsset(assetName *wire.AssetName) int64 {
	if assetName == nil || *assetName == ASSET_PLAIN_SAT {
		return p.GetPlainSat()
	}
	asset, err := p.OutValue.Assets.Find(assetName)
	if err != nil {
		return 0
	}
	return asset.Amount
}

func (p *TxOutput) AddAsset(asset *AssetInfo) error {
	if asset == nil {
		return nil
	}
	
	if asset.Name == ASSET_PLAIN_SAT {
		if p.OutValue.Value + asset.Amount < 0 {
			return fmt.Errorf("out of bounds")
		}
		p.OutValue.Value += asset.Amount
		return nil
	}

	if asset.BindingSat > 0 {
		if p.OutValue.Value + asset.Amount < 0 {
			return fmt.Errorf("out of bounds")
		}
	}

	err := p.OutValue.Assets.Add(asset)
	if err != nil {
		return err
	}

	if asset.BindingSat > 0 {
		p.OutValue.Value += asset.Amount
	}

	return nil
}

func (p *TxOutput) SubAsset(asset *AssetInfo) error {
	if asset == nil {
		return nil
	}
	if asset.Name == ASSET_PLAIN_SAT {
		if p.OutValue.Value < asset.Amount {
			return fmt.Errorf("no enough sats")
		}
		bindingSat := p.OutValue.Assets.GetBindingSatAmout()
		if p.OutValue.Value - asset.Amount < bindingSat {
			return fmt.Errorf("no enough sats")
		}
		p.OutValue.Value -= asset.Amount
		return nil
	}

	if asset.BindingSat > 0 {
		tmpAssets := p.OutValue.Assets.Clone()
		err := tmpAssets.Subtract(asset)
		if err != nil {
			return err
		}
		bindingSat := tmpAssets.GetBindingSatAmout()
		if p.OutValue.Value - asset.Amount < bindingSat {
			return fmt.Errorf("no enough sats")
		}

		p.OutValue.Value -= asset.Amount
		p.OutValue.Assets = tmpAssets
		return nil
	}

	err := p.OutValue.Assets.Subtract(asset)
	if err != nil {
		return err
	}

	return nil
}

func GenerateTxOutput(tx *wire.MsgTx, index int) *TxOutput {
	return &TxOutput{
		OutPointStr: tx.TxHash().String() + ":" + strconv.Itoa(index),
		OutValue:    *tx.TxOut[index],
	}
}


// UtxoL1 进入聪网，TxIdL2是进入交易
type AscendData struct {
	FundingUtxo string        `json:"fundingUtxo"`
	AnchorTxId  string        `json:"anchorTxId"`
	Value       int64         `json:"value"`
	Assets      wire.TxAssets `json:"assets"`

	Address string `json:"address"`
	PubA    []byte `json:"pubkey1"`
	PubB    []byte `json:"pubkey2"`
}

// UtxoL2 离开聪网，TxIdL1是回到主网
type DescendData struct {
	DescendTxId  string        `json:"descendTxId"`
	NullDataUtxo string        `json:"opreturn"`
	Value        int64         `json:"value"`
	Assets       wire.TxAssets `json:"assets"`

	Address string `json:"address"`
}

