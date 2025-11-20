package wallet

import (
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/txscript"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)
const (
	MAX_PAYLOAD_LEN_INBTC = txscript.MaxDataCarrierSize - 2  // bitcoin network
	MAX_PAYLOAD_LEN_INSATOSHINET = stxscript.MaxDataCarrierSize - 2  // bitcoin network
)

func IsValidNullData(nullData []byte) bool {
	return len(nullData) <= txscript.MaxDataCarrierSize
}

func IsValidNullData_SatsNet(nullData []byte) bool {
	return len(nullData) <= stxscript.MaxDataCarrierSize
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

func TxOutToOutput(txOut *swire.TxOut) *TxOutput_SatsNet {
	return &TxOutput_SatsNet{
		UtxoId:      indexer.INVALID_ID,
		OutPointStr: "",
		OutValue:    *txOut,
	}
}

func ParseAssetString(assetName string) *swire.AssetName {
	if assetName == "" {
		return &ASSET_PLAIN_SAT
	}

	return swire.NewAssetNameFromString(assetName)
}

func GenTxAssetsFromAssetInfo(asset *swire.AssetInfo) swire.TxAssets {
	if asset == nil {
		return nil
	}
	if indexer.IsPlainAsset(&asset.Name) {
		return nil
	}
	if asset.Amount.Sign() == 0 {
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
func AlignAsset(output *TxOutput, name *swire.AssetName) error {
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
		if ok {
			output.Offsets = make(map[swire.AssetName]indexer.AssetOffsets)
			output.Offsets[*name] = offsets
		}
	}

	return nil
}

// remove unknown nft
func RemoveNFTAsset(output *TxOutput) {
	
	var filtered indexer.TxAssets
	for _, asset := range output.Assets {
		if asset.Name.Protocol == indexer.PROTOCOL_NAME_ORDX &&
			asset.Name.Type == indexer.ASSET_TYPE_NFT {
			delete(output.Offsets, asset.Name)
			continue
		}
		filtered = append(filtered, asset)
	}
	output.Assets = filtered
}

func HasMultiAsset(output *TxOutput) bool {
	var hasOrdx, hasRunes, hasBrc20 int
	for _, asset := range output.Assets {
		if asset.Name.Protocol == indexer.PROTOCOL_NAME_BRC20 {
			hasBrc20++
		} else if asset.Name.Protocol == indexer.PROTOCOL_NAME_RUNES {
			hasRunes++
		} else if asset.Name.Protocol == indexer.PROTOCOL_NAME_ORDX {
			if asset.Name.Type == indexer.ASSET_TYPE_NFT {
				// 忽略nft
				continue
			}
			hasOrdx++
		}
	}
	return (hasOrdx+hasRunes+hasBrc20) >= 2
}

// 获取output中指定资产的前后空白聪偏移值
func GetPlainOffset(output *TxOutput, name *AssetName) (int64, int64, error) {

	prefixOffset := int64(0)
	suffixOffset := int64(0)
	satNum := indexer.GetBindingSatNum(output.GetAsset(&name.AssetName), uint32(name.N))
	if output.OutValue.Value != satNum {
		// has plain sats
		offsets := output.Offsets[name.AssetName]
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

func CloneOutput(outputs []*indexer.TxOutput) []*indexer.TxOutput {
	result := make([]*indexer.TxOutput, len(outputs))
	for i, output := range outputs {
		result[i] = output.Clone()
	}
	return result
}

func GenTxOutput(inputs []*indexer.TxOutput, assetName *AssetName, value int64, amt *indexer.Decimal) (*TxOutput, *TxOutput, error) {
	combined := indexer.NewTxOutput(0)
	for _, u := range inputs {
		combined.Append(u)
	}

	return combined.Split(&assetName.AssetName, value, amt)
}

func ToTxAssets(assets []*indexer.DisplayAsset) swire.TxAssets {
	var result swire.TxAssets
	for _, asset := range assets {
		result = append(result, *asset.ToAssetInfo())
	}
	return result
}

func OutputInfoToOutput(output *indexerwire.TxOutputInfo) *TxOutput {
	return output.ToTxOutput()
}

func OutputInfoToOutput_SatsNet(output *indexerwire.TxOutputInfo) *TxOutput_SatsNet {
	result := &TxOutput_SatsNet{
		UtxoId:      output.UtxoId,
		OutPointStr: output.OutPoint,
		OutValue: swire.TxOut{
			Value:    output.Value,
			PkScript: output.PkScript,
			Assets:   ToTxAssets(output.Assets),
		},
	}

	return result
}

// 分割为两个部分，一个空白聪，一个含有其他资产
func SplitOutput(output *TxOutput_SatsNet) (*TxOutput_SatsNet, *TxOutput_SatsNet) {
	if output == nil {
		return nil, nil
	}

	var value1, value2 int64
	value1 = output.GetPlainSat()
	value2 = output.Value() - value1

	var part1, part2 *TxOutput_SatsNet
	if value1 != 0 {
		part1 = sindexer.NewTxOutput(value1)
	}

	if len(output.OutValue.Assets) != 0 {
		part2 = sindexer.NewTxOutput(value2)
		part2.OutValue.Assets = output.OutValue.Assets
	}

	return part1, part2
}

func IsRunes(protocol string) bool {
	return protocol == indexer.PROTOCOL_NAME_RUNES
}

func IsBindingSat(assetName *AssetName) bool {
	if assetName == nil {
		return true // plain sats
	}
	return assetName.N > 0
}

func IsPlainAsset(name *AssetName) bool {
	return indexer.IsPlainAsset(&name.AssetName)
}

func GetAssetName(ticker *indexer.TickerInfo) *AssetName {
	return &AssetName{
		AssetName: ticker.AssetName,
		N:         ticker.N,
	}
}

func GetAssetName2(info *swire.AssetInfo) *AssetName {
	return &AssetName{
		AssetName: info.Name,
		N:         int(info.BindingSat),
	}
}

func GetBindingSatNum(amt *indexer.Decimal, n int) int64 {
	return indexer.GetBindingSatNum(amt, uint32(n))
}

func IsLPT(assetName *AssetName) bool {
	return assetName.Protocol == indexer.PROTOCOL_NAME_ORDX &&
		assetName.Type == indexer.ASSET_TYPE_FT &&
		assetName.N >= 1 &&
		strings.Contains(assetName.Ticker, ".lpt")
}

func IsCoreAsset(assetName *indexer.AssetName) bool {
	return assetName.String() == GetCoreAssetName().String()
}

func GetCoreAssetAmount() int64 {
	if IsTestNet() {
		return indexer.TESTNET_CORENODE_STAKING_ASSET_AMOUNT
	}
	return indexer.CORENODE_STAKING_ASSET_AMOUNT
}

func GetCoreAssetName() *indexer.AssetName {
	if IsTestNet() {
		return indexer.NewAssetNameFromString(indexer.TESTNET_CORENODE_STAKING_ASSET_NAME)
	}
	return indexer.NewAssetNameFromString(indexer.CORENODE_STAKING_ASSET_NAME)
}

// ordx 需要2个
// runes需要1个
// brc20需要n个
// 输出时目标地址上需要的stub
func NeedStubUtxoForChannel(name *indexer.AssetName) (int, int64) {
	switch name.Protocol {
	case indexer.PROTOCOL_NAME_RUNES:
		return 1, 330
	case indexer.PROTOCOL_NAME_ORDX:
		if name.Type == indexer.ASSET_TYPE_FT || name.Type == indexer.ASSET_TYPE_EXOTIC {
			return 2, 330
		}
		return 0, 0
	case indexer.PROTOCOL_NAME_BRC20:
		return 2, STUB_VALUE_BRC20
	default:
		return 0, 0
	}
}

// 输入时需要一个330的stub
func NeedStubUtxoForInputAsset(name *AssetName, amt *Decimal) bool {
	if c, _ := NeedStubUtxoForChannel(&name.AssetName); c > 0 {
		satsNum := GetBindingSatNum(amt, name.N)
		return satsNum != 0 && satsNum < 330
	}
	return false
}
