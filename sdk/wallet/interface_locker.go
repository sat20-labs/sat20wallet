package wallet

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/btcsuite/btcd/btcutil/psbt"
	indexer "github.com/sat20-labs/indexer/common"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
)

// 以下接口，作为不依赖钱包的基础api向上层开放
// 这里的stp模块可能是一个独立的模块，但跟管理钱包的stp模块共享底层数据

func (p *Manager) LockUtxo(address, utxo, reason string) (opErr error) {
	opLogID := p.beginWalletLog("utxo", "lock_utxo_l1", "Lock L1 UTXO")
	defer func() { p.finishWalletLog(opLogID, opErr, nil) }()

	return p.lockWalletOutput(p.utxoLockerL1, address, utxo, reason)
}

func (p *Manager) lockWalletOutput(locker *UtxoLocker, address, point, reason string) error {
	if reason != sdkcommon.UtxoLockManual {
		locker.Reload(address)
		return locker.LockUtxo(point, reason)
	}
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if p.wallet == nil {
		return fmt.Errorf("manual lock requires an unlocked wallet")
	}
	if address != "" && address != p.wallet.GetAddress() {
		pub := p.wallet.GetPubKey()
		if pub == nil || address != PublicKeyToP2TRAddress_SatsNet(pub) {
			return fmt.Errorf("manual lock address does not match the selected account")
		}
	}
	locker.mutex.Lock()
	defer locker.mutex.Unlock()
	locker.reload()
	if locker.lockmap[point] != nil {
		return nil
	}
	lock := &LockedUtxo{LockedTime: time.Now().Unix(), Reason: reason, ManualWalletID: p.wallet.GetId()}
	return locker.persistReservationChangesLocked(map[string]*LockedUtxo{point: lock}, nil)
}

func (p *Manager) UnlockUtxo(address, utxo string) (opErr error) {
	opLogID := p.beginWalletLog("utxo", "unlock_utxo_l1", "Unlock L1 UTXO")
	defer func() { p.finishWalletLog(opLogID, opErr, nil) }()

	p.utxoLockerL1.Reload(address)
	return p.utxoLockerL1.UnlockUtxo(utxo)
}

func (p *Manager) LockUtxoForOwner(address, utxo, reason string, owner UtxoLockOwner) error {
	return p.utxoLockerL1.LockUtxoForOwner(utxo, reason, owner)
}

func (p *Manager) UnlockUtxoForOwner(address, utxo string, owner UtxoLockOwner) error {
	return p.utxoLockerL1.UnlockUtxoForOwner(utxo, owner)
}

func (p *Manager) IsLocked(address, utxo string) bool {
	p.utxoLockerL1.Reload(address)
	return p.utxoLockerL1.IsLocked(utxo)
}

func (p *Manager) GetLockedUtxoList(address string) (map[string]*LockedUtxo, error) {
	p.utxoLockerL1.Reload(address)
	return p.utxoLockerL1.GetLockedUtxoList(), nil
}

func (p *Manager) LockUtxo_SatsNet(address, utxo, reason string) (opErr error) {
	opLogID := p.beginWalletLog("utxo", "lock_utxo_l2", "Lock L2 UTXO")
	defer func() { p.finishWalletLog(opLogID, opErr, nil) }()

	return p.lockWalletOutput(p.utxoLockerL2, address, utxo, reason)
}

func (p *Manager) UnlockUtxo_SatsNet(address, utxo string) (opErr error) {
	opLogID := p.beginWalletLog("utxo", "unlock_utxo_l2", "Unlock L2 UTXO")
	defer func() { p.finishWalletLog(opLogID, opErr, nil) }()

	p.utxoLockerL2.Reload(address)
	return p.utxoLockerL2.UnlockUtxo(utxo)
}

func (p *Manager) LockUtxoForOwner_SatsNet(address, utxo, reason string, owner UtxoLockOwner) error {
	return p.utxoLockerL2.LockUtxoForOwner(utxo, reason, owner)
}

func (p *Manager) UnlockUtxoForOwner_SatsNet(address, utxo string, owner UtxoLockOwner) error {
	return p.utxoLockerL2.UnlockUtxoForOwner(utxo, owner)
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
	if asset.Protocol == rgb11wallet.Protocol {
		var err error
		asset, err = p.rgb11WalletAsset(asset)
		if err != nil {
			return nil, nil, err
		}
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
		TxId:         packet.UnsignedTx.TxID(),
		TxHex:        txHex,
		InputAssets:  make([]*indexer.AssetsInUtxo, len(tx.TxIn)),
		OutputAssets: make([]*indexer.AssetsInUtxo, len(tx.TxOut)),
	}

	// 所有输入资产信息
	var input *TxOutput
	for i, txIn := range tx.TxIn {
		utxo := txIn.PreviousOutPoint.String()
		info, err := p.getL1TxOutput(utxo)
		if err != nil {
			Log.Errorf("can't find output info for utxo %s", utxo)
			return nil, err
		}
		utxoInfo := info.ToAssetsInUtxo()

		if input == nil {
			input = info
		} else {
			input.Append(info)
		}

		result.InputAssets[i] = utxoInfo
	}

	// 如果是完整的psbt，按协议规则分配资产
	if packet.IsComplete() {
		// 按协议规则分配资产
		for i, txOut := range tx.TxOut {
			var err error
			var curr *indexer.TxOutput
			curr, input, err = input.Cut(txOut.Value)
			if err != nil {
				return nil, err
			}

			var utxoInfo *indexer.AssetsInUtxo
			if curr != nil {
				utxoInfo = curr.ToAssetsInUtxo()
			} else {
				return nil, fmt.Errorf("inputs have no enough asset for output %d", i)
			}
			// TODO 需要支持runes协议分配资产
			result.OutputAssets[i] = utxoInfo
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
		TxId:  packet.UnsignedTx.TxID(),
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
