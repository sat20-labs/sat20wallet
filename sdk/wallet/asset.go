package wallet

import (
	indexer "github.com/sat20-labs/indexer/common"
	sindexer "github.com/sat20-labs/indexer_satsnet/common"
	swire "github.com/sat20-labs/satsnet_btcd/wire"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
)

type AssetName = swire.AssetName

// 白聪
var ASSET_PLAIN_SAT = indexer.ASSET_PLAIN_SAT

type TxOutput = indexer.TxOutput
type TxOutput_SatsNet = sindexer.TxOutput


func OutputToSatsNet(output *TxOutput) *TxOutput_SatsNet {

	n := TxOutput_SatsNet{
		UtxoId:      output.UtxoId,
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


func ToTxAssets(assets []*indexerwire.UtxoAssetInfo) swire.TxAssets {
	var result swire.TxAssets
	for _, asset := range assets {
		result = append(result, *asset.Asset.Clone())
	}
	return result
}

func OutputInfoToOutput(output *indexerwire.TxOutputInfo) *TxOutput {
	result := &TxOutput{
		UtxoId:      output.UtxoId,
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


func OutputInfoToOutput_SatsNet(output *indexerwire.TxOutputInfo) *TxOutput_SatsNet {
	result := &TxOutput_SatsNet{
		UtxoId:      output.UtxoId,
		OutPointStr: output.OutPoint,
		OutValue:  swire.TxOut{
			Value:   output.OutValue.Value,
			PkScript: output.OutValue.PkScript,
			Assets:  ToTxAssets(output.AssetInfo),
		},
	}

	return result
}

func IsRunes(protocol string) bool {
	return protocol == indexer.PROTOCOL_NAME_RUNES
}

func IsBindingSat(assetName *AssetName) bool {
	return indexer.IsBindingSat(assetName)
}

func IsPlainAsset(assetName *AssetName) bool {
	return indexer.IsPlainAsset(assetName)
}
