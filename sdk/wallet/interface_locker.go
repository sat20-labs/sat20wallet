package wallet

import (
	"bytes"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/btcutil/psbt"
	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
	indexer "github.com/sat20-labs/indexer/common"
)

// 以下接口，作为不依赖钱包的基础api向上层开放
// 这里的stp模块可能是一个独立的模块，但跟管理钱包的stp模块共享底层数据

func (p *Manager) GetTickerInfo(assetName string) *indexer.TickerInfo {
	asset := ParseAssetString(assetName)
	if asset == nil {
		Log.Errorf("invalid asset name %s", assetName)
		return nil
	}

	return p.getTickerInfo(asset)
}

func (p *Manager) LockUtxo(address, utxo, reason string) error {
	p.utxoLockerL1.Reload(address)
	return p.utxoLockerL1.LockUtxo(utxo, reason)
}

func (p *Manager) UnlockUtxo(address, utxo string) error {
	p.utxoLockerL1.Reload(address)
	return p.utxoLockerL1.UnlockUtxo(utxo)
}

func (p *Manager) IsLocked(address, utxo string) bool {
	p.utxoLockerL1.Reload(address)
	return p.utxoLockerL1.IsLocked(utxo)
}

func (p *Manager) GetLockedUtxoList(address string) (map[string]*LockedUtxo, error) {
	p.utxoLockerL1.Reload(address)
	return p.utxoLockerL1.GetLockedUtxoList(), nil
}

func (p *Manager) LockUtxo_SatsNet(address, utxo, reason string) error {
	p.utxoLockerL2.Reload(address)
	return p.utxoLockerL2.LockUtxo(utxo, reason)
}

func (p *Manager) UnlockUtxo_SatsNet(address, utxo string) error {
	p.utxoLockerL2.Reload(address)
	return p.utxoLockerL2.UnlockUtxo(utxo)
}

func (p *Manager) IsLocked_SatsNet(address, utxo string) bool {
	p.utxoLockerL2.Reload(address)
	return p.utxoLockerL2.IsLocked(utxo)
}

func (p *Manager) GetLockedUtxoList_SatsNet(address string) (map[string]*LockedUtxo, error) {
	p.utxoLockerL2.Reload(address)
	return p.utxoLockerL2.GetLockedUtxoList(), nil
}

func (p *Manager) GetUtxosWithAssetForJS(address, amt, assetName string) ([]string, error) {
	if address == "" {
		return nil, fmt.Errorf("address is null")
	}
	p.utxoLockerL1.Reload(address)
	asset := ParseAssetString(assetName)
	if asset == nil {
		return nil, fmt.Errorf("invalid asset name %s", assetName)
	}
	tickerInfo := p.getTickerInfo(asset)
	if tickerInfo == nil {
		return nil, fmt.Errorf("can't get ticker %s info", assetName)
	}
	dAmt, err := indexer.NewDecimalFromString(amt, tickerInfo.Divisibility)
	if err != nil {
		return nil, err
	}
	if dAmt.Sign() <= 0 {
		return nil, fmt.Errorf("invalid amt")
	}
	return p.GetUtxosWithAsset(address, dAmt, asset, nil)
}

func (p *Manager) GetUtxosWithAssetForJS_SatsNet(address, amt, assetName string) ([]string, error) {
	if address == "" {
		return nil, fmt.Errorf("address is null")
	}
	p.utxoLockerL2.Reload(address)
	asset := ParseAssetString(assetName)
	if asset == nil {
		return nil, fmt.Errorf("invalid asset name %s", assetName)
	}
	tickerInfo := p.getTickerInfo(asset)
	if tickerInfo == nil {
		return nil, fmt.Errorf("can't get ticker %s info", assetName)
	}
	dAmt, err := indexer.NewDecimalFromString(amt, tickerInfo.Divisibility)
	if err != nil {
		return nil, err
	}
	if dAmt.Sign() <= 0 {
		return nil, fmt.Errorf("invalid amt")
	}
	return p.GetUtxosWithAsset_SatsNet(address, dAmt, asset, nil)
}

func (p *Manager) GetUtxosWithAssetV2ForJS(address string, value int64, amt, assetName string) ([]string, []string, error) {
	if address == "" {
		return nil, nil, fmt.Errorf("address is null")
	}
	p.utxoLockerL1.Reload(address)
	asset := ParseAssetString(assetName)
	if asset == nil {
		return nil, nil, fmt.Errorf("invalid asset name %s", assetName)
	}
	tickerInfo := p.getTickerInfo(asset)
	if tickerInfo == nil {
		return nil, nil, fmt.Errorf("can't get ticker %s info", assetName)
	}
	dAmt, err := indexer.NewDecimalFromString(amt, tickerInfo.Divisibility)
	if err != nil {
		return nil, nil, err
	}
	if dAmt.Sign() <= 0 {
		return nil, nil, fmt.Errorf("invalid amt")
	}
	return p.GetUtxosWithAssetV2(address, value, dAmt, asset, nil, false)
}

func (p *Manager) GetUtxosWithAssetV2ForJS_SatsNet(address string, value int64, amt, assetName string) ([]string, []string, error) {
	if address == "" {
		return nil, nil, fmt.Errorf("address is null")
	}
	p.utxoLockerL2.Reload(address)
	asset := ParseAssetString(assetName)
	if asset == nil {
		return nil, nil, fmt.Errorf("invalid asset name %s", assetName)
	}
	tickerInfo := p.getTickerInfo(asset)
	if tickerInfo == nil {
		return nil, nil, fmt.Errorf("can't get ticker %s info", assetName)
	}
	dAmt, err := indexer.NewDecimalFromString(amt, tickerInfo.Divisibility)
	if err != nil {
		return nil, nil, err
	}
	if dAmt.Sign() <= 0 {
		return nil, nil, fmt.Errorf("invalid amt")
	}
	return p.GetUtxosWithAssetV2_SatsNet(address, value, dAmt, asset, nil)
}

// available, locked
func (p *Manager) GetAssetAmountForJS(address, assetName string) (*Decimal, *Decimal, error) {
	if address == "" {
		return nil, nil, fmt.Errorf("address is null")
	}
	p.utxoLockerL1.Reload(address)
	asset := ParseAssetString(assetName)
	if asset == nil {
		return nil, nil, fmt.Errorf("invalid asset name %s", assetName)
	}
	tickerInfo := p.getTickerInfo(asset)
	if tickerInfo == nil {
		return nil, nil, fmt.Errorf("can't get ticker %s info", assetName)
	}

	available, locked := p.GetAssetAmount(address, asset, nil)
	return available, locked, nil
}

func (p *Manager) GetAssetAmountForJS_SatsNet(address, assetName string) (*Decimal, *Decimal, error) {
	if address == "" {
		return nil, nil, fmt.Errorf("address is null")
	}
	p.utxoLockerL2.Reload(address)
	asset := ParseAssetString(assetName)
	if asset == nil {
		return nil, nil, fmt.Errorf("invalid asset name %s", assetName)
	}
	tickerInfo := p.getTickerInfo(asset)
	if tickerInfo == nil {
		return nil, nil, fmt.Errorf("can't get ticker %s info", assetName)
	}

	available, locked := p.GetAssetAmount_SatsNet(address, asset, nil)
	return available, locked, nil
}

func (p *Manager) GetTxAssetInfoFromPsbt(psbtStr string) (*TxAssetInfo, error) {
	hexBytes, err := hex.DecodeString(psbtStr)
	if err != nil {
		return nil, err
	}
	packet, err := psbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
	if err != nil {
		return nil, err
	}
	txHex, err := EncodeMsgTx(packet.UnsignedTx)
	if err != nil {
		return nil, err
	}

	tx := packet.UnsignedTx
	result := &TxAssetInfo{
		TxId: packet.UnsignedTx.TxID(),
		TxHex: txHex,
		InputAssets:  make([]*indexer.AssetsInUtxo, len(tx.TxIn)),
		OutputAssets: make([]*indexer.AssetsInUtxo, len(tx.TxOut)),
	}

	// 所有输入资产信息
	var input *TxOutput
	for i, txIn := range tx.TxIn {
		utxoInfo := indexer.AssetsInUtxo{
			OutPoint: txIn.PreviousOutPoint.String(),
		}
		info, err := p.l1IndexerClient.GetTxOutput(utxoInfo.OutPoint)
		if err != nil {
			Log.Errorf("can't find output info for utxo %s", utxoInfo.OutPoint)
			return nil, err
		}
		
		utxoInfo.UtxoId = info.UtxoId
		utxoInfo.PkScript = info.OutValue.PkScript
		utxoInfo.Value = info.OutValue.Value
		for _, asset := range info.Assets {
			precision := 0
			tickInfo := p.getTickerInfo(&asset.Name)
			if tickInfo != nil {
				precision = tickInfo.Divisibility
			}
			utxoInfo.Assets = append(utxoInfo.Assets, &indexer.DisplayAsset{
				AssetName:  asset.Name,
				Amount:     asset.Amount.String(),
				Precision:  precision,
				BindingSat: int(asset.BindingSat),
				Offsets:    info.Offsets[asset.Name],
			})
		}

		if input == nil {
			input = info
		} else {
			input.Append(info)
		}

		result.InputAssets[i] = &utxoInfo
	}

	// 如果是完整的psbt，按协议规则分配资产；否则直接

	if packet.IsComplete() {
		// 按协议规则分配资产
		for i, txOut := range tx.TxOut {
			utxo := fmt.Sprintf("%s:%d", tx.TxID(), i)
			utxoInfo := indexer.AssetsInUtxo{
				OutPoint: utxo,
				Value:    txOut.Value,
				PkScript: txOut.PkScript,
			}

			var err error
			var curr *indexer.TxOutput
			curr, input, err = input.Cut(txOut.Value)
			if err != nil {
				return nil, err
			}

			if curr != nil {
				for _, asset := range curr.Assets {
					precision := 0
					tickInfo := p.getTickerInfo(&asset.Name)
					if tickInfo != nil {
						precision = tickInfo.Divisibility
					}
					utxoInfo.Assets = append(utxoInfo.Assets, &indexer.DisplayAsset{
						AssetName:  asset.Name,
						Amount:     asset.Amount.String(),
						Precision:  precision,
						BindingSat: int(asset.BindingSat),
						Offsets:    curr.Offsets[asset.Name],
					})
				}
			} else {
				return nil, fmt.Errorf("inputs have no enough asset for output %d", i)
			}
			result.OutputAssets[i] = &utxoInfo
		}
	} else {
		for i, txOut := range tx.TxOut {
			utxo := fmt.Sprintf("%s:%d", tx.TxID(), i)
			utxoInfo := indexer.AssetsInUtxo{
				OutPoint: utxo,
				Value:    txOut.Value,
				PkScript: txOut.PkScript,
			}
				
			result.OutputAssets[i] = &utxoInfo
		}
	}

	

	return result, nil
}

func GetTxAssetInfoFromPsbt_SatsNet(psbtStr string) (*TxAssetInfo, error) {
	hexBytes, err := hex.DecodeString(psbtStr)
	if err != nil {
		return nil, err
	}
	packet, err := spsbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
	if err != nil {
		return nil, err
	}
	txHex, err := EncodeMsgTx_SatsNet(packet.UnsignedTx)
	if err != nil {
		return nil, err
	}

	result := TxAssetInfo{
		TxId: packet.UnsignedTx.TxID(),
		TxHex: txHex,
	}

	prevOutputFetcher := PsbtPrevOutputFetcher_SatsNet(packet)
	for _, txIn := range packet.UnsignedTx.TxIn {
		utxoInfo := indexer.AssetsInUtxo{
			OutPoint: txIn.PreviousOutPoint.String(),
		}
		output := prevOutputFetcher.FetchPrevOutput(txIn.PreviousOutPoint)
		if output != nil {
			utxoInfo.PkScript = output.PkScript
			utxoInfo.Value = output.Value
			for _, asset := range output.Assets {
				utxoInfo.Assets = append(utxoInfo.Assets, &indexer.DisplayAsset{
					AssetName:  asset.Name,
					Amount:     asset.Amount.String(),
					BindingSat: int(asset.BindingSat),
				})
			}
		} else {
			return nil, fmt.Errorf("can't find output info for utxo %s", utxoInfo.OutPoint)
		}
		result.InputAssets = append(result.InputAssets, &utxoInfo)
	}

	for i, txOut := range packet.UnsignedTx.TxOut {
		utxoInfo := indexer.AssetsInUtxo{
			OutPoint: fmt.Sprintf("%s:%d", packet.UnsignedTx.TxID(), i),
		}
		
		utxoInfo.PkScript = txOut.PkScript
		utxoInfo.Value = txOut.Value
		for _, asset := range txOut.Assets {
			utxoInfo.Assets = append(utxoInfo.Assets, &indexer.DisplayAsset{
				AssetName:  asset.Name,
				Amount:     asset.Amount.String(),
				BindingSat: int(asset.BindingSat),
			})
		}
		
		result.OutputAssets = append(result.OutputAssets, &utxoInfo)
	}

	return &result, nil
}


