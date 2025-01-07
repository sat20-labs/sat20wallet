package wallet

import (
	"fmt"

	swire "github.com/sat20-labs/satsnet_btcd/wire"
	"github.com/sat20-labs/sat20wallet/wallet/indexer"
	"github.com/sat20-labs/sat20wallet/wallet/sindexer"
)

type AssetName = swire.AssetName

// 白聪
var ASSET_PLAIN_SAT = indexer.ASSET_PLAIN_SAT

type TxOutput = indexer.TxOutput
type TxOutput_SatsNet = sindexer.TxOutput

// 只支持一种资产，不处理offset
func GetTxOutputAssets(assetName *AssetName, outputs []*TxOutput) *swire.AssetInfo {

	result := swire.AssetInfo{
		Name:       *assetName,
		BindingSat: indexer.IsBindingSat(assetName),
	}
	for _, o := range outputs {
		if indexer.IsPlainAsset(assetName) {
			result.Amount += o.Value()
		} else {
			for _, u := range o.Assets {
				if u.Name == *assetName {
					result.Add(&u)
				}
			}
		}
	}

	return &result
}

func GetSizeOfTxOutputs(outputs map[string]*TxOutput_SatsNet) int64 {
	value := int64(0)
	for _, o := range outputs {
		value += o.Value()
	}
	return value
}

func OutputToSatsNet(output *TxOutput) *TxOutput_SatsNet {

	n := TxOutput_SatsNet{
		OutPointStr: output.OutPointStr,
		OutValue: swire.TxOut{
			Value:    output.Value(),
			PkScript: output.OutValue.PkScript,
			Assets:   output.Assets.Clone(),
		},
	}
	return &n
}

func ParseAssetString(assetName string) *AssetName {
	if assetName == "" {
		return &ASSET_PLAIN_SAT
	}

	return swire.NewAssetNameFromString(assetName)
}

func NewAssetInfo(name *AssetName, amt int64) *swire.AssetInfo {
	return &swire.AssetInfo{
		Name:       *name,
		Amount:     amt,
		BindingSat: indexer.IsBindingSat(name),
	}
}

func GenTxAssetsFromAssetInfo(asset *swire.AssetInfo) swire.TxAssets {
	if indexer.IsPlainAsset(&asset.Name) {
		return nil
	}
	return swire.TxAssets{*asset}
}

func GenTxAssetsFromAssets(assets swire.TxAssets) swire.TxAssets {
	// just remove plain asset
	var result swire.TxAssets
	for _, asset := range assets {
		if indexer.IsPlainAsset(&asset.Name) {
			continue
		}
		result = append(result, asset)
	}
	return result
}

// 只保留指定资产
func AlignAsset(output *TxOutput, name *AssetName) (error) {
	// 为了效率，要先确保output中没有其他无关资产
	if indexer.IsPlainAsset(name) {
		output.Assets = nil
		output.Offsets = nil
	} else {
		asset, err := output.Assets.Find(name)
		if err != nil {
			return err
		}
		output.Assets = swire.TxAssets{*asset}

		offsets, ok := output.Offsets[*name]
		if !ok {
			return fmt.Errorf("can't find offsets for %s", name.String())
		}
		output.Offsets = make(map[swire.AssetName]indexer.AssetOffsets)
		output.Offsets[*name] = offsets
	}

	return nil
}

// 获取output中指定资产的前后空白聪偏移值
func GetPlainOffset(output *TxOutput, name *AssetName) (int64, int64, error) {

	prefixOffset := int64(0)
	suffixOffset := int64(0)
	if output.OutValue.Value != output.GetAsset(name) {
		// has plain sats
		offsets := output.Offsets[*name]
		if len(offsets) == 0 {
			return 0, 0, fmt.Errorf("no asset in %s", output.OutPointStr)
		}
		if offsets[0].Start != 0 {
			prefixOffset = offsets[0].Start
		}
		last := offsets[len(offsets)-1]
		if last.End != output.OutValue.Value {
			suffixOffset = output.OutValue.Value - last.End
		}
	}

	return prefixOffset, suffixOffset, nil
}

func GenTxOutput(inputs []*indexer.TxOutput, assetName *AssetName, amt int64) (*TxOutput, *TxOutput, error) {
	combined := indexer.NewTxOutput(0)
	for _, u := range inputs {
		combined.Append(u)
	}

	return combined.Split(assetName, amt)
}

func ToTxAssets(assets []*indexer.AssetInfo) swire.TxAssets {
	var result swire.TxAssets
	for _, asset := range assets {
		result = append(result, *asset.Asset.Clone())
	}
	return result
}

func OutputInfoToOutput(output *indexer.TxOutputInfo) *TxOutput {
	result := &TxOutput{
		OutPointStr: output.OutPoint,
		OutValue:    output.OutValue,
		Offsets:    make(map[swire.AssetName]indexer.AssetOffsets),
	}

	for _, asset := range output.AssetInfo {
		result.Assets = append(result.Assets, *asset.Asset.Clone())
		result.Offsets[asset.Asset.Name] = asset.Offsets
	}

	return result
}


func OutputInfoToOutput_SatsNet(output *indexer.TxOutputInfo) *TxOutput_SatsNet {
	result := &TxOutput_SatsNet{
		OutPointStr: output.OutPoint,
		OutValue:  swire.TxOut{
			Value:   output.OutValue.Value,
			PkScript: output.OutValue.PkScript,
			Assets:  ToTxAssets(output.AssetInfo),
		},
	}

	return result
}
