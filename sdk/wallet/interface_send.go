package wallet

import (
	"fmt"
	"math/big"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"

	sbtcutil "github.com/sat20-labs/satoshinet/btcutil"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"

	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"

	"github.com/sat20-labs/indexer/indexer/runes/runestone"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

const (
	ERR_NO_ASSETS string = "no assets"
	ERR_NO_ENOUGH_ASSETS string = "no enough assets"

	ERR_NO_SATS string = "no plain sats"
	ERR_NO_ENOUGH_SATS string = "no enough plain sats"
)

// 发送给多个地址不同数量资产
func (p *Manager) BatchSendAssetsV2_SatsNet(destAddr []string,
	assetName string, amtVect []string, memo []byte) (string, error) {

	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	name := ParseAssetString(assetName)
	if name == nil {
		return "", fmt.Errorf("invalid asset name %s", assetName)
	}
	tickerInfo := p.getTickerInfo(name)
	if tickerInfo == nil {
		return "", fmt.Errorf("can't get ticker %s info", assetName)
	}

	var totalAmt *Decimal
	var destAmt []*Decimal
	for _, amt := range amtVect {
		dAmt, err := indexer.NewDecimalFromString(amt, tickerInfo.Divisibility)
		if err != nil {
			return "", err
		}
		if dAmt.Sign() <= 0 {
			return "", fmt.Errorf("invalid amount %s", amt)
		}
		destAmt = append(destAmt, dAmt)
		if totalAmt == nil {
			totalAmt = dAmt
		} else {
			totalAmt = totalAmt.Add(dAmt)
		}
	}

	utxos, fees, err := p.GetUtxosWithAssetV2_SatsNet("", DEFAULT_FEE_SATSNET, totalAmt, name, nil)
	if err != nil {
		return "", err
	}

	tx, prevFetcher, err := p.BuildBatchSendTx_SatsNet(destAddr, name, destAmt, utxos, fees, memo)
	if err != nil {
		return "", err
	}

	// sign
	tx, err = p.SignTx_SatsNet(tx, prevFetcher)
	if err != nil {
		Log.Errorf("SignTx_SatsNet failed. %v", err)
		return "", err
	}

	PrintJsonTx_SatsNet(tx, "batchSendAssetsV2_SatsNet")

	txid, err := p.BroadcastTx_SatsNet(tx)
	if err != nil {
		Log.Errorf("batchSendAssetsV2_SatsNet failed. %v", err)
		return "", err
	}

	return txid, nil
}

// 构建tx，发送给多个地址不同数量的资产，只发送资产（可以是白聪）
func (p *Manager) BuildBatchSendTx_SatsNet(destAddr []string,
	assetName *swire.AssetName, destAmt []*Decimal, utxos, fees []string, memo []byte) (*swire.MsgTx, *stxscript.MultiPrevOutFetcher, error) {

	if p.wallet == nil {
		return nil, nil, fmt.Errorf("wallet is not created/unlocked")
	}

	if len(destAddr) != len(destAmt) {
		return nil, nil, fmt.Errorf("the length of address and amount should be equal")
	}
	if len(destAddr) == 0 {
		return nil, nil, fmt.Errorf("the lenght of address is 0")
	}
	if len(utxos) == 0 {
		return nil, nil, fmt.Errorf("the lenght of utxos is 0")
	}

	tickerInfo := p.getTickerInfo(assetName)
	if tickerInfo == nil {
		return nil, nil, fmt.Errorf("can't get ticker %s info", assetName)
	}

	var destPkScript [][]byte
	for _, str := range destAddr {
		pkScript, err := GetPkScriptFromAddress(str)
		if err != nil {
			return nil, nil, fmt.Errorf("GetPkScriptFromAddress %s failed. %v", str, err)
		}

		destPkScript = append(destPkScript, pkScript)
	}

	tx := swire.NewMsgTx(swire.TxVersion)

	fee := indexer.NewDefaultDecimal(DEFAULT_FEE_SATSNET)

	p.utxoLockerL2.Reload(p.wallet.GetAddress())
	prevFetcher := stxscript.NewMultiPrevOutFetcher(nil)
	var input TxOutput_SatsNet
	value := int64(0)
	var assetAmt *Decimal
	var changePkScript []byte
	allUtxos := append(utxos, fees...)
	for i, utxo := range allUtxos {
		if p.utxoLockerL2.IsLocked(utxo) {
			return nil, nil, fmt.Errorf("utxo %s is locked", utxo)
		}
		txOut, err := p.l2IndexerClient.GetTxOutput(utxo)
		if err != nil {
			return nil, nil, fmt.Errorf("GetTxOutput %s failed, %v", utxo, err)
		}
		output := OutputToSatsNet(txOut)
		outpoint := output.OutPoint()

		value += output.Value()
		assetAmt = assetAmt.Add(output.GetAsset(assetName))
		txIn := swire.NewTxIn(outpoint, nil, nil)
		tx.AddTxIn(txIn)
		prevFetcher.AddPrevOut(*outpoint, &output.OutValue)
		input.Merge(output)
		if i+1 == len(allUtxos) {
			changePkScript = txOut.OutValue.PkScript
		}
	}

	for i, pkScript := range destPkScript {
		var sendAsset *swire.AssetInfo
		if !destAmt[i].IsZero() {
			sendAsset = &swire.AssetInfo{
				Name:       *assetName,
				Amount:     *destAmt[i],
				BindingSat: uint32(tickerInfo.N),
			}
		}

		outValue := GetBindingSatNum(destAmt[i], tickerInfo.N)
		txOut := swire.NewTxOut(outValue, GenTxAssetsFromAssetInfo(sendAsset), pkScript)
		tx.AddTxOut(txOut)
		err := input.SubAsset(sendAsset)
		if err != nil {
			return nil, nil, err
		}
	}

	feeAsset := swire.AssetInfo{
		Name:       ASSET_PLAIN_SAT,
		Amount:     *fee,
		BindingSat: 1,
	}
	err := input.SubAsset(&feeAsset)
	if err != nil {
		return nil, nil, err
	}

	SplitChangeAsset(&input, changePkScript, tx)
	if len(memo) > 0 {
		txOut2 := swire.NewTxOut(0, nil, memo)
		tx.AddTxOut(txOut2)
	}

	return tx, prevFetcher, nil
}

// 发送给不同地址不同数量的资产和白聪，资产只能同一种
func (p *Manager) BatchSendAssetsV3_SatsNet(dest []*SendAssetInfo,
	assetName string, memo []byte) (string, error) {

	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	name := ParseAssetString(assetName)
	if name == nil {
		return "", fmt.Errorf("invalid asset name %s", assetName)
	}
	tickerInfo := p.getTickerInfo(name)
	if tickerInfo == nil {
		return "", fmt.Errorf("can't get ticker %s info", assetName)
	}

	var totalValue int64
	var totalAmt *Decimal
	for _, item := range dest {
		totalAmt = totalAmt.Add(item.AssetAmt)
		totalValue += item.Value
	}

	var err error
	var utxos, fees []string
	utxos, fees, err = p.GetUtxosWithAssetV2_SatsNet("", totalValue, totalAmt, name, nil)
	if err != nil {
		return "", err
	}

	tx, prevFetcher, err := p.BuildBatchSendTxV2_SatsNet(dest, name, utxos, fees, memo)
	if err != nil {
		Log.Errorf("buildBatchSendTxV2_SatsNet failed. %v", err)
		return "", err
	}

	// sign
	tx, err = p.SignTx_SatsNet(tx, prevFetcher)
	if err != nil {
		Log.Errorf("SignTx_SatsNet failed. %v", err)
		return "", err
	}
	PrintJsonTx_SatsNet(tx, "batchSendAssetsV2_SatsNet")

	txid, err := p.BroadcastTx_SatsNet(tx)
	if err != nil {
		Log.Errorf("batchSendAssetsV2_SatsNet failed. %v", err)
		return "", err
	}

	return txid, nil
}

// 构建tx，从本地地址或者通道地址，发送资产到指定的地址列表，包括白聪和资产。资产只能一种。
func (p *Manager) BuildBatchSendTxV2_SatsNet(dest []*SendAssetInfo,
	assetName *swire.AssetName, utxos, fees []string, memo []byte) (*swire.MsgTx, *stxscript.MultiPrevOutFetcher, error) {

	if p.wallet == nil {
		return nil, nil, fmt.Errorf("wallet is not created/unlocked")
	}
	address := p.wallet.GetAddress()

	if len(dest) == 0 {
		return nil, nil, fmt.Errorf("the lenght of address is 0")
	}
	var destPkScript [][]byte
	for _, d := range dest {
		if d.AssetName != nil {
			if d.AssetName.String() != assetName.String() {
				return nil, nil, fmt.Errorf("the asset name is not equal")
			}
		}
		pkScript, err := GetPkScriptFromAddress(d.Address)
		if err != nil {
			return nil, nil, fmt.Errorf("GetPkScriptFromAddress %s failed. %v", d.Address, err)
		}

		destPkScript = append(destPkScript, pkScript)
	}

	tickerInfo := p.getTickerInfo(assetName)
	if tickerInfo == nil {
		return nil, nil, fmt.Errorf("can't get ticker %s info", assetName)
	}

	tx := swire.NewMsgTx(swire.TxVersion)

	fee := indexer.NewDefaultDecimal(DEFAULT_FEE_SATSNET)

	p.utxoLockerL2.Reload(address)
	prevFetcher := stxscript.NewMultiPrevOutFetcher(nil)
	var input TxOutput_SatsNet

	var assetAmt *Decimal
	var changePkScript []byte
	allUtxos := append(utxos, fees...)
	for i, utxo := range allUtxos {
		if p.utxoLockerL2.IsLocked(utxo) {
			return nil, nil, fmt.Errorf("utxo %s is locked", utxo)
		}
		txOut, err := p.l2IndexerClient.GetTxOutput(utxo)
		if err != nil {
			return nil, nil, fmt.Errorf("GetTxOutput %s failed, %v", utxo, err)
		}
		output := OutputToSatsNet(txOut)
		outpoint := output.OutPoint()

		assetAmt = assetAmt.Add(output.GetAsset(assetName))
		txIn := swire.NewTxIn(outpoint, nil, nil)
		tx.AddTxIn(txIn)
		prevFetcher.AddPrevOut(*outpoint, &output.OutValue)
		input.Merge(output)
		if i+1 == len(allUtxos) {
			changePkScript = txOut.OutValue.PkScript
		}
	}

	for i, pkScript := range destPkScript {
		var sendAsset *swire.AssetInfo
		if !dest[i].AssetAmt.IsZero() {
			sendAsset = &swire.AssetInfo{
				Name:       *assetName,
				Amount:     *dest[i].AssetAmt,
				BindingSat: uint32(tickerInfo.N),
			}
		}

		outValue := dest[i].Value + indexer.GetBindingSatNum(dest[i].AssetAmt, uint32(tickerInfo.N))
		txOut := swire.NewTxOut(outValue, GenTxAssetsFromAssetInfo(sendAsset), pkScript)
		tx.AddTxOut(txOut)
		output := sindexer.GenerateTxOutput(tx, len(tx.TxOut)-1)
		err := input.Subtract(output)
		if err != nil {
			return nil, nil, err
		}
	}

	feeAsset := swire.AssetInfo{
		Name:       ASSET_PLAIN_SAT,
		Amount:     *fee,
		BindingSat: 1,
	}
	err := input.SubAsset(&feeAsset)
	if err != nil {
		return nil, nil, err
	}

	SplitChangeAsset(&input, changePkScript, tx)
	if len(memo) > 0 {
		txOut2 := swire.NewTxOut(0, nil, memo)
		tx.AddTxOut(txOut2)
	}

	return tx, prevFetcher, nil
	//return nil, nil, fmt.Errorf("not implemented")
}

func SplitChangeAsset(input *TxOutput_SatsNet, changePkScript []byte, tx *swire.MsgTx) {
	if !input.Zero() {
		// 分离所有资产
		bindingSat := int64(0)
		txouts := make([]*swire.TxOut, 0)
		for _, asset := range input.OutValue.Assets {
			if asset.Amount.Sign() > 0 {
				value := indexer.GetBindingSatNum(&asset.Amount, asset.BindingSat)
				bindingSat += value
				txOut := swire.NewTxOut(value, swire.TxAssets{asset}, changePkScript)
				txouts = append(txouts, txOut)
			}
		}
		if bindingSat > input.OutValue.Value {
			// 可能因为历史的原因，有一些utxo聪不足，需要为这些utxo补充聪，所以这里不做任何事
			txOut1 := swire.NewTxOut(input.Value(), input.OutValue.Assets, changePkScript)
			tx.AddTxOut(txOut1)
		} else {
			for _, txOut := range txouts {
				tx.AddTxOut(txOut)
			}
			if input.OutValue.Value-bindingSat > 0 {
				txOut := swire.NewTxOut(input.OutValue.Value-bindingSat, nil, changePkScript)
				tx.AddTxOut(txOut)
			}
		}
	}
}

// 发送资产到一个地址上，拆分n个输出
func (p *Manager) BatchSendAssets_SatsNet(destAddr string,
	assetName string, amt string, n int) (string, error) {

	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}
	name := ParseAssetString(assetName)
	if name == nil {
		return "", fmt.Errorf("invalid asset name %s", assetName)
	}
	tickerInfo := p.getTickerInfo(name)
	if tickerInfo == nil {
		return "", fmt.Errorf("can't get ticker %s info", assetName)
	}
	dAmt, err := indexer.NewDecimalFromString(amt, tickerInfo.Divisibility)
	if err != nil {
		return "", err
	}
	if dAmt.Sign() <= 0 {
		return "", fmt.Errorf("invalid amt")
	}
	totalAmt := dAmt.Clone()
	totalAmt = totalAmt.MulBigInt(big.NewInt(int64(n)))

	address := p.wallet.GetAddress()
	outputs := p.l2IndexerClient.GetUtxoListWithTicker(address, name)
	if len(outputs) == 0 {
		Log.Errorf("no asset %s", assetName)
		return "", fmt.Errorf("no asset %s", assetName)
	}

	Log.Infof("SendAssets_SatsNet %s %s", assetName, amt)
	tx := swire.NewMsgTx(swire.TxVersion)

	addr, err := sbtcutil.DecodeAddress(destAddr, GetChainParam_SatsNet())
	if err != nil {
		return "", err
	}
	pkScript, err := stxscript.PayToAddrScript(addr)
	if err != nil {
		return "", err
	}

	fee := indexer.NewDefaultDecimal(DEFAULT_FEE_SATSNET)
	expectedAmt := totalAmt.Clone()
	if indexer.IsPlainAsset(name) {
		expectedAmt = expectedAmt.Add(fee)
	}

	usedUtxos := make(map[string]bool)
	p.utxoLockerL2.Reload(address)
	prevFetcher := stxscript.NewMultiPrevOutFetcher(nil)
	var input TxOutput_SatsNet
	value := int64(0)
	var assetAmt *Decimal
	for _, out := range outputs {
		if p.utxoLockerL2.IsLocked(out.OutPoint) {
			continue
		}
		output := OutputInfoToOutput_SatsNet(out)
		outpoint := output.OutPoint()
		txOut := output.OutValue

		value += out.Value
		assetAmt = assetAmt.Add(output.GetAsset(name))
		txIn := swire.NewTxIn(outpoint, nil, nil)
		tx.AddTxIn(txIn)
		prevFetcher.AddPrevOut(*outpoint, &txOut)
		input.Merge(output)
		usedUtxos[out.OutPoint] = true

		if assetAmt.Cmp(expectedAmt) >= 0 {
			break
		}
	}
	if assetAmt.Cmp(expectedAmt) < 0 {
		return "", fmt.Errorf("not enough asset %s", assetName)
	}

	var feeOutputs []*indexerwire.TxOutputInfo
	if !indexer.IsPlainAsset(name) {
		feeValue := input.GetPlainSat()
		if feeValue < DEFAULT_FEE_SATSNET {
			feeOutputs = p.l2IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
			if len(feeOutputs) == 0 {
				Log.Errorf("no plain sats")
				return "", fmt.Errorf("no plain sats")
			}

			for _, out := range feeOutputs {
				if p.utxoLockerL2.IsLocked(out.OutPoint) {
					continue
				}
				_, ok := usedUtxos[out.OutPoint]
				if ok {
					continue
				}
				output := OutputInfoToOutput_SatsNet(out)
				outpoint := output.OutPoint()
				txOut := output.OutValue

				feeValue += out.Value
				txIn := swire.NewTxIn(outpoint, nil, nil)
				tx.AddTxIn(txIn)
				prevFetcher.AddPrevOut(*outpoint, &txOut)
				input.Merge(output)

				if feeValue >= DEFAULT_FEE_SATSNET {
					break
				}
			}

			if feeValue < DEFAULT_FEE_SATSNET {
				return "", fmt.Errorf("no enough fee")
			}
		}
	}

	sendAsset := swire.AssetInfo{
		Name:       *name,
		Amount:     *dAmt,
		BindingSat: uint32(p.getBindingSat(name)),
	}
	outValue := GetBindingSatNum(dAmt, tickerInfo.N)
	txOut := swire.NewTxOut(outValue, GenTxAssetsFromAssetInfo(&sendAsset), pkScript)
	for i := 0; i < n; i++ {
		tx.AddTxOut(txOut)
		err = input.SubAsset(&sendAsset)
		if err != nil {
			return "", err
		}
	}

	feeAsset := swire.AssetInfo{
		Name:       ASSET_PLAIN_SAT,
		Amount:     *fee,
		BindingSat: 1,
	}
	err = input.SubAsset(&feeAsset)
	if err != nil {
		return "", err
	}

	changePkScript, err := GetP2TRpkScript(p.wallet.GetPaymentPubKey())
	if err != nil {
		return "", err
	}
	SplitChangeAsset(&input, changePkScript, tx)

	// sign
	tx, err = p.SignTx_SatsNet(tx, prevFetcher)
	if err != nil {
		Log.Errorf("SignTx_SatsNet failed. %v", err)
		return "", err
	}

	PrintJsonTx_SatsNet(tx, "BatchSendAssets_SatsNet")

	txid, err := p.BroadcastTx_SatsNet(tx)
	if err != nil {
		Log.Errorf("BroadCastTx_SatsNet failed. %v", err)
		return "", err
	}

	return txid, nil
}

// 发送资产到一个地址上
func (p *Manager) SendAssets_SatsNet(destAddr string,
	assetName string, amt string, memo []byte) (*swire.MsgTx, error) {

	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	name := ParseAssetString(assetName)
	if name == nil {
		return nil, fmt.Errorf("invalid asset name %s", assetName)
	}
	tickerInfo := p.getTickerInfo(name)
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
	if !IsValidNullData_SatsNet(memo) {
		return nil, fmt.Errorf("invalid length of null data %d", len(memo))
	}

	address := p.wallet.GetAddress()
	outputs := p.l2IndexerClient.GetUtxoListWithTicker(address, name)
	if len(outputs) == 0 {
		Log.Errorf("no asset %s", assetName)
		return nil, fmt.Errorf("no asset %s", assetName)
	}

	Log.Infof("SendAssets_SatsNet %s %s", assetName, amt)
	tx := swire.NewMsgTx(swire.TxVersion)

	addr, err := sbtcutil.DecodeAddress(destAddr, GetChainParam_SatsNet())
	if err != nil {
		return nil, err
	}
	pkScript, err := stxscript.PayToAddrScript(addr)
	if err != nil {
		return nil, err
	}

	fee := indexer.NewDefaultDecimal(DEFAULT_FEE_SATSNET)
	expectedAmt := dAmt.Clone()
	if indexer.IsPlainAsset(name) {
		expectedAmt = expectedAmt.Add(fee)
	}

	p.utxoLockerL2.Reload(address)
	prevFetcher := stxscript.NewMultiPrevOutFetcher(nil)
	var input TxOutput_SatsNet
	var assetAmt *Decimal
	usedUtxos := make(map[string]bool)
	for _, out := range outputs {
		if p.utxoLockerL2.IsLocked(out.OutPoint) {
			continue
		}
		output := OutputInfoToOutput_SatsNet(out)
		outpoint := output.OutPoint()
		txOut := output.OutValue

		assetAmt = assetAmt.Add(output.GetAsset(name))
		txIn := swire.NewTxIn(outpoint, nil, nil)
		tx.AddTxIn(txIn)
		prevFetcher.AddPrevOut(*outpoint, &txOut)
		input.Merge(output)
		usedUtxos[out.OutPoint] = true

		if assetAmt.Cmp(expectedAmt) >= 0 {
			break
		}
	}
	if assetAmt.Cmp(expectedAmt) < 0 {
		if indexer.IsPlainAsset(name) && assetAmt.Cmp(dAmt) == 0 {
			// 转移全部，微调下dAmt
			dAmt = dAmt.Sub(fee)
		} else {
			return nil, fmt.Errorf("not enough asset %s", assetName)
		}
	}

	var feeOutputs []*indexerwire.TxOutputInfo
	if !indexer.IsPlainAsset(name) {
		feeValue := input.GetPlainSat()
		if feeValue < DEFAULT_FEE_SATSNET {
			feeOutputs = p.l2IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
			if len(feeOutputs) == 0 {
				Log.Errorf("no plain sats")
				return nil, fmt.Errorf("no plain sats")
			}

			for _, out := range feeOutputs {
				if p.utxoLockerL2.IsLocked(out.OutPoint) {
					continue
				}
				_, ok := usedUtxos[out.OutPoint]
				if ok {
					continue
				}
				output := OutputInfoToOutput_SatsNet(out)
				outpoint := output.OutPoint()
				txOut := output.OutValue

				feeValue += out.Value
				txIn := swire.NewTxIn(outpoint, nil, nil)
				tx.AddTxIn(txIn)
				prevFetcher.AddPrevOut(*outpoint, &txOut)
				input.Merge(output)

				if feeValue >= DEFAULT_FEE_SATSNET {
					break
				}
			}

			if feeValue < DEFAULT_FEE_SATSNET {
				return nil, fmt.Errorf("no enough fee")
			}
		}
	}

	sendAsset := swire.AssetInfo{
		Name:       *name,
		Amount:     *dAmt,
		BindingSat: uint32(p.getBindingSat(name)),
	}
	outValue := GetBindingSatNum(dAmt, tickerInfo.N)
	txOut := swire.NewTxOut(outValue, GenTxAssetsFromAssetInfo(&sendAsset), pkScript)
	tx.AddTxOut(txOut)

	err = input.SubAsset(&sendAsset)
	if err != nil {
		return nil, err
	}
	feeAsset := swire.AssetInfo{
		Name:       ASSET_PLAIN_SAT,
		Amount:     *fee,
		BindingSat: 1,
	}
	err = input.SubAsset(&feeAsset)
	if err != nil {
		return nil, err
	}

	changePkScript, err := GetP2TRpkScript(p.wallet.GetPaymentPubKey())
	if err != nil {
		return nil, err
	}
	SplitChangeAsset(&input, changePkScript, tx)

	// attached data
	if memo != nil {
		if len(memo) > stxscript.MaxDataCarrierSize {
			return nil, fmt.Errorf("attached data too large")
		}
		txOut3 := swire.NewTxOut(0, nil, memo)
		tx.AddTxOut(txOut3)
	}

	// sign
	tx, err = p.SignTx_SatsNet(tx, prevFetcher)
	if err != nil {
		Log.Errorf("SignTx_SatsNet failed. %v", err)
		return nil, err
	}

	PrintJsonTx_SatsNet(tx, "SendAssets_SatsNet")

	_, err = p.BroadcastTx_SatsNet(tx)
	if err != nil {
		Log.Errorf("BroadCastTx_SatsNet failed. %v", err)
		return nil, err
	}

	return tx, nil
}

// 发送一个op_return，只支付网络费
func (p *Manager) SendNullData_SatsNet(memo []byte) (string, error) {

	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	if len(memo) == 0 {
		return "", fmt.Errorf("empty data")
	}

	if !IsValidNullData_SatsNet(memo) {
		return "", fmt.Errorf("invalid length of null data %d", len(memo))
	}

	address := p.wallet.GetAddress()
	tx := swire.NewMsgTx(swire.TxVersion)
	fee := indexer.NewDefaultDecimal(DEFAULT_FEE_SATSNET)
	p.utxoLockerL2.Reload(address)
	prevFetcher := stxscript.NewMultiPrevOutFetcher(nil)
	var input TxOutput_SatsNet
	feeValue := int64(0)
	feeOutputs := p.l2IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
	for _, out := range feeOutputs {
		if p.utxoLockerL2.IsLocked(out.OutPoint) {
			continue
		}
		output := OutputInfoToOutput_SatsNet(out)
		outpoint := output.OutPoint()
		txOut := output.OutValue

		feeValue += output.GetPlainSat()
		txIn := swire.NewTxIn(outpoint, nil, nil)
		tx.AddTxIn(txIn)
		prevFetcher.AddPrevOut(*outpoint, &txOut)
		input.Merge(output)

		if feeValue >= DEFAULT_FEE_SATSNET {
			break
		}
	}

	if feeValue < DEFAULT_FEE_SATSNET {
		return "", fmt.Errorf("no enough fee")
	}

	feeAsset := swire.AssetInfo{
		Name:       ASSET_PLAIN_SAT,
		Amount:     *fee,
		BindingSat: 1,
	}
	err := input.SubAsset(&feeAsset)
	if err != nil {
		return "", err
	}

	if !input.Zero() {
		changePkScript, err := GetP2TRpkScript(p.wallet.GetPaymentPubKey())
		if err != nil {
			return "", err
		}
		txOut0 := swire.NewTxOut(input.Value(), input.OutValue.Assets, changePkScript)
		tx.AddTxOut(txOut0)
	}

	txOut1 := swire.NewTxOut(0, nil, memo)
	tx.AddTxOut(txOut1)

	// sign
	tx, err = p.SignTx_SatsNet(tx, prevFetcher)
	if err != nil {
		Log.Errorf("SignTx_SatsNet failed. %v", err)
		return "", err
	}

	PrintJsonTx_SatsNet(tx, "SendNulData_SatsNet")

	txid, err := p.BroadcastTx_SatsNet(tx)
	if err != nil {
		Log.Errorf("BroadCastTx_SatsNet failed. %v", err)
		return "", err
	}

	return txid, nil
}

// 发送utxo
func (p *Manager) SendUtxos_SatsNet(destAddr string,
	utxos []string, memo []byte) (string, error) {

	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}
	if !IsValidNullData_SatsNet(memo) {
		return "", fmt.Errorf("invalid length of null data %d", len(memo))
	}

	var pkScript []byte
	addr, err := sbtcutil.DecodeAddress(destAddr, GetChainParam_SatsNet())
	if err != nil {
		return "", err
	}
	pkScript, err = stxscript.PayToAddrScript(addr)
	if err != nil {
		return "", err
	}

	tx := swire.NewMsgTx(swire.TxVersion)
	prevFetcher := stxscript.NewMultiPrevOutFetcher(nil)
	var input TxOutput_SatsNet
	plainSats := int64(0)
	for _, utxo := range utxos {
		txOut, err := p.l2IndexerClient.GetTxOutput(utxo)
		if err != nil {
			return "", fmt.Errorf("GetTxOutput %s failed, %v", utxo, err)
		}
		txOut_SatsNet := OutputToSatsNet(txOut)
		outpoint := txOut_SatsNet.OutPoint()

		plainSats += txOut_SatsNet.GetPlainSat()

		txIn := swire.NewTxIn(outpoint, nil, nil)
		tx.AddTxIn(txIn)
		prevFetcher.AddPrevOut(*outpoint, &txOut_SatsNet.OutValue)
		input.Merge(txOut_SatsNet)
	}
	if plainSats < DEFAULT_FEE_SATSNET {
		return "", fmt.Errorf("not enough plain sats for fee %d", plainSats)
	}

	_, feeChange, err := input.Split(&ASSET_PLAIN_SAT, DEFAULT_FEE_SATSNET, nil)
	if err != nil {
		return "", err
	}

	txOut0 := swire.NewTxOut(feeChange.Value(), feeChange.OutValue.Assets, pkScript)
	tx.AddTxOut(txOut0)

	// attached data
	if memo != nil {
		if len(memo) > stxscript.MaxDataCarrierSize {
			return "", fmt.Errorf("attached data too large")
		}
		txOut1 := swire.NewTxOut(0, nil, memo)
		tx.AddTxOut(txOut1)
	}

	// sign
	tx, err = p.SignTx_SatsNet(tx, prevFetcher)
	if err != nil {
		Log.Errorf("SignTx_SatsNet failed. %v", err)
		return "", err
	}

	PrintJsonTx_SatsNet(tx, "SendUtxos_SatsNet")

	txid, err := p.BroadcastTx_SatsNet(tx)
	if err != nil {
		Log.Errorf("BroadCastTx_SatsNet failed. %v", err)
		return "", err
	}

	return txid, nil
}

// 同时发送资产和聪，资产可以是聪
func (p *Manager) SendAssetsV3_SatsNet(destAddr string,
	assetName string, amt string, value int64, memo []byte) (string, error) {

	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}
	name := ParseAssetString(assetName)
	if name == nil {
		return "", fmt.Errorf("invalid asset name %s", assetName)
	}
	tickerInfo := p.getTickerInfo(name)
	if tickerInfo == nil {
		return "", fmt.Errorf("can't get ticker %s info", assetName)
	}
	dAmt, err := indexer.NewDecimalFromString(amt, tickerInfo.Divisibility)
	if err != nil {
		return "", err
	}
	if dAmt.Sign() <= 0 {
		return "", fmt.Errorf("invalid amt")
	}
	if !IsValidNullData_SatsNet(memo) {
		return "", fmt.Errorf("invalid length of null data %d", len(memo))
	}

	address := p.wallet.GetAddress()
	outputs := p.l2IndexerClient.GetUtxoListWithTicker(address, name)
	if len(outputs) == 0 {
		Log.Errorf("no asset %s", assetName)
		return "", fmt.Errorf("no asset %s", assetName)
	}

	Log.Infof("SendAssetsV3_SatsNet %s %s", assetName, amt)
	tx := swire.NewMsgTx(swire.TxVersion)

	addr, err := sbtcutil.DecodeAddress(destAddr, GetChainParam_SatsNet())
	if err != nil {
		return "", err
	}
	assetPkScript, err := stxscript.PayToAddrScript(addr)
	if err != nil {
		return "", err
	}

	satsNum := value + DEFAULT_FEE_SATSNET
	fee := indexer.NewDefaultDecimal(satsNum)
	expectedAmt := dAmt.Clone()
	if indexer.IsPlainAsset(name) {
		expectedAmt = expectedAmt.Add(fee)
	}

	p.utxoLockerL2.Reload(address)
	prevFetcher := stxscript.NewMultiPrevOutFetcher(nil)
	var input TxOutput_SatsNet
	var assetAmt *Decimal
	usedUtxos := make(map[string]bool)
	for _, out := range outputs {
		if p.utxoLockerL2.IsLocked(out.OutPoint) {
			continue
		}
		output := OutputInfoToOutput_SatsNet(out)
		outpoint := output.OutPoint()
		txOut := output.OutValue

		// 如果该utxo夹杂其他资产，并且其n>1，可能会导致两个原来没有携带聪的资产，这里合并后要求至少一个聪
		// 合并方需要为这些资产提供足够的聪，这反过来会导致白聪数量降低
		assetAmt = assetAmt.Add(output.GetAsset(name))
		txIn := swire.NewTxIn(outpoint, nil, nil)
		tx.AddTxIn(txIn)
		prevFetcher.AddPrevOut(*outpoint, &txOut)
		input.Merge(output)
		usedUtxos[out.OutPoint] = true

		if assetAmt.Cmp(expectedAmt) >= 0 {
			break
		}
	}
	if assetAmt.Cmp(expectedAmt) < 0 {
		return "", fmt.Errorf("not enough asset %s", assetName)
	}

	var feeOutputs []*indexerwire.TxOutputInfo
	if !indexer.IsPlainAsset(name) {
		feeValue := input.GetPlainSat()
		if feeValue < satsNum {
			feeOutputs = p.l2IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
			if len(feeOutputs) == 0 {
				Log.Errorf("no plain sats")
				return "", fmt.Errorf("no plain sats")
			}

			for _, out := range feeOutputs {
				if p.utxoLockerL2.IsLocked(out.OutPoint) {
					continue
				}
				_, ok := usedUtxos[out.OutPoint]
				if ok {
					continue
				}
				output := OutputInfoToOutput_SatsNet(out)
				outpoint := output.OutPoint()
				txOut := output.OutValue

				feeValue += out.Value
				txIn := swire.NewTxIn(outpoint, nil, nil)
				tx.AddTxIn(txIn)
				prevFetcher.AddPrevOut(*outpoint, &txOut)
				input.Merge(output)

				if feeValue >= satsNum {
					break
				}
			}

			if feeValue < satsNum {
				return "", fmt.Errorf("no enough fee")
			}
		}
	}

	sendAsset := swire.AssetInfo{
		Name:       *name,
		Amount:     *dAmt,
		BindingSat: uint32(p.getBindingSat(name)),
	}
	outValue := value + GetBindingSatNum(dAmt, tickerInfo.N)
	txOut0 := swire.NewTxOut(outValue, GenTxAssetsFromAssetInfo(&sendAsset), assetPkScript)
	tx.AddTxOut(txOut0)
	err = input.Subtract(TxOutToOutput(txOut0))
	if err != nil {
		return "", err
	}

	feeAsset := swire.AssetInfo{
		Name:       ASSET_PLAIN_SAT,
		Amount:     *indexer.NewDefaultDecimal(DEFAULT_FEE_SATSNET),
		BindingSat: 1,
	}
	err = input.SubAsset(&feeAsset)
	if err != nil {
		return "", err
	}

	changePkScript, err := GetP2TRpkScript(p.wallet.GetPaymentPubKey())
	if err != nil {
		return "", err
	}
	SplitChangeAsset(&input, changePkScript, tx)

	// attached data
	if memo != nil {
		if len(memo) > stxscript.MaxDataCarrierSize {
			return "", fmt.Errorf("attached data too large")
		}
		txOut3 := swire.NewTxOut(0, nil, memo)
		tx.AddTxOut(txOut3)
	}

	// sign
	tx, err = p.SignTx_SatsNet(tx, prevFetcher)
	if err != nil {
		Log.Errorf("SignTx_SatsNet failed. %v", err)
		return "", err
	}

	PrintJsonTx_SatsNet(tx, "SendAssetsV2_SatsNet")

	txid, err := p.BroadcastTx_SatsNet(tx)
	if err != nil {
		Log.Errorf("BroadCastTx_SatsNet failed. %v", err)
		return "", err
	}

	return txid, nil
}

func (p *Manager) GenerateStubUtxos(n int, feeRate int64) (string, int64, error) {
	//
	tx, fee, err := p.BatchSendAssets(p.wallet.GetAddress(), indexer.ASSET_PLAIN_SAT.String(),
		"330", n, feeRate, nil)
	if err != nil {
		return "", fee, err
	}
	return tx.TxID(), fee, nil
}

func (p *Manager) GenerateStubUtxosV2(n int, excludedUtxoMap map[string]bool,
	feeRate int64) (*wire.MsgTx, int64, error) {

	if p.wallet == nil {
		return nil, 0, fmt.Errorf("wallet is not created/unlocked")
	}
	destAddr := p.wallet.GetAddress()

	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	tx, prevFetcher, fee, err := p.BuildBatchSendTx_btc(destAddr, 330, n, excludedUtxoMap, feeRate, nil)
	if err != nil {
		Log.Errorf("buildBatchSendTx failed. %v", err)
		return nil, 0, err
	}

	// sign
	tx, err = p.SignTx(tx, prevFetcher)
	if err != nil {
		Log.Errorf("SignTx failed. %v", err)
		return nil, 0, err
	}

	_, err = p.BroadcastTx(tx)
	if err != nil {
		Log.Errorf("BroadCastTx failed. %v", err)
		return nil, 0, err
	}

	return tx, fee, nil
}

func (p *Manager) BatchSendPlainSats(destAddr string, value int64, n int,
	feeRate int64, memo []byte) (string, int64, error) {
	//
	tx, fee, err := p.BatchSendAssets(destAddr, indexer.ASSET_PLAIN_SAT.String(),
		fmt.Sprintf("%d", value), n, feeRate, memo)
	if err != nil {
		return "", fee, err
	}
	return tx.TxID(), fee, nil
}

// 发送资产到一个地址上，拆分n个输出
func (p *Manager) BatchSendAssets(destAddr string, assetName string, 
	amt string, n int, feeRate int64, memo []byte) (*wire.MsgTx, int64, error) {

	if p.wallet == nil {
		return nil, 0, fmt.Errorf("wallet is not created/unlocked")
	}
	name := ParseAssetString(assetName)
	if name == nil {
		return nil, 0, fmt.Errorf("invalid asset name %s", assetName)
	}
	tickerInfo := p.getTickerInfo(name)
	if tickerInfo == nil {
		return nil, 0, fmt.Errorf("can't get ticker %s info", assetName)
	}
	dAmt, err := indexer.NewDecimalFromString(amt, tickerInfo.Divisibility)
	if err != nil {
		return nil, 0, err
	}
	if dAmt.Sign() <= 0 {
		return nil, 0, fmt.Errorf("invalid amt")
	}
	if !IsValidNullData(memo) {
		return nil, 0, fmt.Errorf("invalid length of null data %d", len(memo))
	}
	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	var tx *wire.MsgTx
	var prevFetcher *txscript.MultiPrevOutFetcher
	var fee int64

	var inscribe *InscribeResv
	newName := GetAssetName(tickerInfo)
	switch name.Protocol {
	case "": // btc
		tx, prevFetcher, fee, err = p.BuildBatchSendTx_btc(destAddr, dAmt.Int64(), n, nil, feeRate, memo)
	case indexer.PROTOCOL_NAME_ORDX:
		tx, prevFetcher, fee, err = p.BuildBatchSendTx_ordx(destAddr, newName, dAmt, n, feeRate, memo)
	case indexer.PROTOCOL_NAME_RUNES:
		tx, prevFetcher, fee, err = p.BuildBatchSendTx_runes(destAddr, newName, dAmt, n, feeRate, memo)
	case indexer.PROTOCOL_NAME_BRC20:
		tx, prevFetcher, fee, inscribe, err = p.BuildBatchSendTx_brc20(destAddr, newName, dAmt, n, feeRate, memo)
	default:
		return nil, 0, fmt.Errorf("buildBatchSendTx unsupport protocol %s", name.Protocol)
	}
	
	if err != nil {
		Log.Errorf("buildBatchSendTx failed. %v", err)
		return nil, 0, err
	}

	// sign
	tx, err = p.SignTx(tx, prevFetcher)
	if err != nil {
		Log.Errorf("SignTx failed. %v", err)
		return nil, 0, err
	}

	var txs []*wire.MsgTx
	if inscribe != nil {
		txs = []*wire.MsgTx{inscribe.CommitTx, inscribe.RevealTx, tx}
		err = p.TestAcceptance(txs)
		if err != nil {
			Log.Errorf("TestAcceptance failed. %v", err)
			p.utxoLockerL1.UnlockUtxosWithTx(inscribe.CommitTx)
			return nil, 0, err
		}
	} else {
		txs = []*wire.MsgTx{tx}
	}

	err = p.BroadcastTxs(txs)
	if err != nil {
		Log.Errorf("BroadcastTxs failed. %v", err)
		if inscribe != nil {
			p.utxoLockerL1.UnlockUtxosWithTx(inscribe.CommitTx)	
		}
		return nil, 0, err
	}

	return tx, fee, nil
}

// 从p2tr地址发出
func (p *Manager) BuildBatchSendTx_btc(destAddr string, amt int64, n int,
	excludedUtxoMap map[string]bool,
	feeRate int64, memo []byte) (*wire.MsgTx, *txscript.MultiPrevOutFetcher, int64, error) {

	if amt < 330 {
		return nil, nil, 0, fmt.Errorf("amount too small")
	}

	tx := wire.NewMsgTx(wire.TxVersion)

	addr, err := btcutil.DecodeAddress(destAddr, GetChainParam())
	if err != nil {
		return nil, nil, 0, err
	}
	destPkScript, err := txscript.PayToAddrScript(addr)
	if err != nil {
		return nil, nil, 0, err
	}

	var weightEstimate utils.TxWeightEstimator
	for range n {
		weightEstimate.AddP2TROutput() // output
		txOut := &wire.TxOut{
			PkScript: destPkScript,
			Value:    int64(amt),
		}
		tx.AddTxOut(txOut)
	}
	if len(memo) > 0 {
		weightEstimate.AddOutput(memo[:]) // op_return
	}

	required := amt * int64(n)
	prevFetcher, changePkScript, outputValue, changeOutput, fee0, err := p.SelectUtxosForPlainSats(
		p.wallet.GetAddress(), excludedUtxoMap,
		required, feeRate, tx, &weightEstimate, false, false)
	if err != nil {
		return nil, nil, 0, err
	}
	if outputValue != required {
		// 调整输出  TODO　由调用方决定要不要调整
		amt = outputValue / int64(n)
		for _, txOut := range tx.TxOut {
			txOut.Value = amt
		}
	}

	weightEstimate.AddP2TROutput() // fee change
	fee1 := weightEstimate.Fee(feeRate)
	changeOutput += fee0 - fee1
	fee := fee0
	if changeOutput >= 330 {
		fee = fee1
		txOut2 := &wire.TxOut{
			PkScript: changePkScript,
			Value:    int64(changeOutput),
		}
		tx.AddTxOut(txOut2)
	}

	if len(memo) > 0 {
		txOut3 := &wire.TxOut{
			PkScript: memo,
			Value:    0,
		}
		tx.AddTxOut(txOut3)
	}

	return tx, prevFetcher, fee, nil
}

func AdjustInputsForSplicingIn(inputs []*TxOutput, name *AssetName) ([]*TxOutput, int64, int64) {
	hasPrefix := -1
	prefixOffset := int64(0)
	hasSuffix := -1
	suffixOffset := int64(0)
	value := int64(0)
	for i, u := range inputs {
		value += u.OutValue.Value

		prefix, suffix, _ := GetPlainOffset(u, name)

		if prefix != 0 {
			if prefix > prefixOffset {
				hasPrefix = i
				prefixOffset = prefix
			}
		}

		if suffix != 0 {
			if suffix > suffixOffset {
				hasSuffix = i
				suffixOffset = suffix
			}
		}
	}

	if hasPrefix == -1 && hasSuffix == -1 {
		return inputs, 0, 0
	}

	// 调整成前后有最大的白聪的列表

	// adjust
	if len(inputs) == 1 {
		return inputs, prefixOffset, suffixOffset
	}

	var pre, suf *TxOutput
	var others []*TxOutput
	for i, u := range inputs {
		if i == hasPrefix {
			pre = u
		} else if i == hasSuffix {
			suf = u
		} else {
			others = append(others, u)
		}
	}
	var result []*TxOutput
	if pre != nil {
		result = append(result, pre)
	}
	result = append(result, others...)
	if suf != nil {
		result = append(result, suf)
	}

	return result, prefixOffset, suffixOffset
}

// 给同一个地址发送n等分资产
func (p *Manager) BuildBatchSendTx_ordx(destAddr string,
	name *AssetName, amt *Decimal, n int, feeRate int64,
	memo []byte) (*wire.MsgTx, *txscript.MultiPrevOutFetcher, int64, error) {

	addr, err := btcutil.DecodeAddress(destAddr, GetChainParam())
	if err != nil {
		return nil, nil, 0, err
	}
	destPkScript, err := txscript.PayToAddrScript(addr)
	if err != nil {
		return nil, nil, 0, err
	}

	// TODO 选择合适的utxo
	requiredAmt := amt.Clone().MulBigInt(big.NewInt(int64(n)))
	var weightEstimate utils.TxWeightEstimator
	selected, totalAsset, total, err := p.SelectUtxosForAsset(
		p.wallet.GetAddress(), nil,
		&name.AssetName, requiredAmt, &weightEstimate, false, false)
	if err != nil {
		return nil, nil, 0, err
	}
	var prefix, suffix int64
	selected, prefix, suffix = AdjustInputsForSplicingIn(selected, name)
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	allInput := indexer.NewTxOutput(0)
	tx := wire.NewMsgTx(wire.TxVersion)
	for _, output := range selected {
		tx.AddTxIn(output.TxIn())
		prevFetcher.AddPrevOut(*output.OutPoint(), &output.OutValue)
		allInput.Append(output)
	}
	changePkScript := selected[0].OutValue.PkScript

	totalOutputSats := int64(0)
	remainingOutput := allInput
	if prefix >= 330 {
		var output *TxOutput
		output, remainingOutput, err = remainingOutput.Split(&ASSET_PLAIN_SAT, prefix, nil)
		if err != nil {
			return nil, nil, 0, err
		}

		txOut0 := &wire.TxOut{
			PkScript: changePkScript,
			Value:    output.Value(),
		}
		tx.AddTxOut(txOut0)
		weightEstimate.AddTxOutput(txOut0)
		totalOutputSats += output.Value()
	}

	for range n {
		var output *TxOutput
		output, remainingOutput, err = remainingOutput.Split(&name.AssetName, 0, amt)
		if err != nil {
			return nil, nil, 0, err
		}
		if output.Value() >= 330 {
			txOut1 := &wire.TxOut{
				PkScript: destPkScript,
				Value:    output.Value(),
			}
			tx.AddTxOut(txOut1)
			weightEstimate.AddTxOutput(txOut1)
			totalOutputSats += output.Value()
		} else {
			return nil, nil, 0, fmt.Errorf("output is less than 330 sats")
		}
	}
	if len(memo) > 0 {
		weightEstimate.AddOutput(memo[:]) // op_return
	}

	if totalAsset.Cmp(requiredAmt) > 0 {
		assetChange := indexer.DecimalSub(totalAsset, requiredAmt)

		var output *TxOutput
		output, _, err = remainingOutput.Split(&name.AssetName, 0, assetChange)
		if err != nil {
			return nil, nil, 0, err
		}
		if output.Value() >= 330 {
			txOut2 := &wire.TxOut{
				PkScript: changePkScript,
				Value:    output.Value(),
			}
			tx.AddTxOut(txOut2)
			weightEstimate.AddTxOutput(txOut2)
			totalOutputSats += output.Value()
		} else {
			return nil, nil, 0, fmt.Errorf("output is less than 330 sats")
		}
	}

	// 剩下的都是白聪
	feeValue := total - totalOutputSats
	if feeValue != suffix {
		return nil, nil, 0, fmt.Errorf("something wrong, %d != %d", feeValue, suffix)
	}

	// TODO 选择合适的白聪utxo
	fee0 := weightEstimate.Fee(feeRate)
	if feeValue < fee0 {
		// 增加fee
		var selected []*TxOutput
		selected, feeValue, err = p.SelectUtxosForFee(p.wallet.GetAddress(), nil,
			feeValue, feeRate, &weightEstimate, false, false)
		if err != nil {
			return nil, nil, 0, err
		}
		for _, output := range selected {
			tx.AddTxIn(output.TxIn())
			prevFetcher.AddPrevOut(*output.OutPoint(), &output.OutValue)
		}
	}

	fee0 = weightEstimate.Fee(feeRate)
	weightEstimate.AddP2TROutput() // fee change
	fee1 := weightEstimate.Fee(feeRate)

	if feeValue < fee0 {
		return nil, nil, 0, fmt.Errorf("no enough fee")
	}

	feeChange := feeValue - fee1
	if feeChange >= 330 {
		txOut3 := &wire.TxOut{
			PkScript: changePkScript,
			Value:    int64(feeChange),
		}
		tx.AddTxOut(txOut3)
	} else {
		feeChange = 0
	}
	if len(memo) > 0 {
		txOut4 := &wire.TxOut{
			PkScript: memo,
			Value:    0,
		}
		tx.AddTxOut(txOut4)
	}

	return tx, prevFetcher, feeValue - feeChange, nil
}

// 给同一个地址发送n等分资产
func (p *Manager) BuildBatchSendTx_runes(destAddr string,
	name *AssetName, amt *Decimal, n int, feeRate int64,
	memo []byte) (*wire.MsgTx, *txscript.MultiPrevOutFetcher, int64, error) {

	addr, err := btcutil.DecodeAddress(destAddr, GetChainParam())
	if err != nil {
		return nil, nil, 0, err
	}
	destPkScript, err := txscript.PayToAddrScript(addr)
	if err != nil {
		return nil, nil, 0, err
	}

	// TODO 选择合适的utxo
	requiredAmt := amt.Clone().MulBigInt(big.NewInt(int64(n)))
	var weightEstimate utils.TxWeightEstimator
	selected, totalAsset, total, err := p.SelectUtxosForAsset(
		p.wallet.GetAddress(), nil,
		&name.AssetName, requiredAmt, &weightEstimate, false, false)
	if err != nil {
		return nil, nil, 0, err
	}
	changePkScript := selected[0].OutValue.PkScript

	tx := wire.NewMsgTx(wire.TxVersion)
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	for _, output := range selected {
		tx.AddTxIn(output.TxIn())
		prevFetcher.AddPrevOut(*output.OutPoint(), &output.OutValue)
	}

	// 余额输出到第一个utxo中，其他目标地址从第二个utxo开始
	// 远端的资产使用edict明确表示输出数量
	transferEdicts := make([]runestone.Edict, 0)
	runeId, err := p.getRuneIdFromName(&name.AssetName)
	if err != nil {
		return nil, nil, 0, err
	}
	totalOutputSats := int64(0)
	if totalAsset.Cmp(requiredAmt) > 0 {
		// 先输出余额
		txOut0 := &wire.TxOut{
			PkScript: changePkScript,
			Value:    330,
		}
		tx.AddTxOut(txOut0)
		weightEstimate.AddTxOutput(txOut0)
		totalOutputSats += 330
	}

	for range n {
		txOut1 := &wire.TxOut{
			PkScript: destPkScript,
			Value:    330,
		}
		tx.AddTxOut(txOut1)
		weightEstimate.AddTxOutput(txOut1)
		totalOutputSats += 330
		transferEdicts = append(transferEdicts, runestone.Edict{
			ID:     *runeId,
			Output: uint32(len(tx.TxOut) - 1),
			Amount: amt.ToUint128(),
		})
	}

	if len(transferEdicts) > 0 {
		nullDataScript, err := EncipherRunePayload(transferEdicts)
		if err != nil {
			Log.Errorf("too many edicts, %d, %v", len(transferEdicts), err)
			return nil, nil, 0, err
		}
		weightEstimate.AddOutput(nullDataScript)

		txOut2 := &wire.TxOut{
			PkScript: nullDataScript,
			Value:    int64(0),
		}
		tx.AddTxOut(txOut2)
	}

	feeValue := total - totalOutputSats
	fee0 := weightEstimate.Fee(feeRate)
	if feeValue < fee0 {
		// 增加fee
		var selected []*TxOutput
		selected, feeValue, err = p.SelectUtxosForFee(
			p.wallet.GetAddress(), nil, feeValue,
			feeRate, &weightEstimate, false, false)
		if err != nil {
			return nil, nil, 0, err
		}
		for _, output := range selected {
			tx.AddTxIn(output.TxIn())
			prevFetcher.AddPrevOut(*output.OutPoint(), &output.OutValue)
		}
	}

	fee0 = weightEstimate.Fee(feeRate)
	weightEstimate.AddP2TROutput() // fee change
	fee1 := weightEstimate.Fee(feeRate)

	if feeValue < fee0 {
		return nil, nil, 0, fmt.Errorf("no enough fee")
	}

	feeChange := feeValue - fee1
	if feeChange >= 330 {
		txOut3 := &wire.TxOut{
			PkScript: changePkScript,
			Value:    int64(feeChange),
		}
		tx.AddTxOut(txOut3)
	} else {
		feeChange = 0
	}
	if len(memo) > 0 {
		txOut4 := &wire.TxOut{
			PkScript: memo,
			Value:    0,
		}
		tx.AddTxOut(txOut4)
	}

	return tx, prevFetcher, feeValue - feeChange, nil
}

// 给同一个地址发送n等分资产. brc20只支持n==1的情况
func (p *Manager) BuildBatchSendTx_brc20(destAddr string,
	name *AssetName, amt *Decimal, n int, feeRate int64,
	memo []byte) (*wire.MsgTx, *txscript.MultiPrevOutFetcher, int64, *InscribeResv, error) {

	if n != 1 {
		return nil, nil, 0, nil, fmt.Errorf("not support")
	}

	// 如果刚好有指定的transfer nft（可以多个），那么直接转移这些nft
	// 如果没有，就铸造，并且转移

	addr, err := btcutil.DecodeAddress(destAddr, GetChainParam())
	if err != nil {
		return nil, nil, 0, nil, err
	}
	destPkScript, err := txscript.PayToAddrScript(addr)
	if err != nil {
		return nil, nil, 0, nil, err
	}

	// TODO 选择合适的utxo
	localAddr := p.wallet.GetAddress()
	requiredAmt := amt.Clone().MulBigInt(big.NewInt(int64(n)))
	var weightEstimate utils.TxWeightEstimator
	selected, totalAsset, total, err := p.SelectUtxosForAsset(
		localAddr, nil,
		&name.AssetName, requiredAmt, &weightEstimate, false, false)
	if err == nil {
		if totalAsset.Cmp(requiredAmt) != 0 {
			// 有多的，去掉最后一个，重新铸造一个
			last := selected[len(selected)-1]
			totalAsset = totalAsset.Sub(last.GetAsset(&name.AssetName))
			selected = selected[0:len(selected)-1]
		}
	} else {
		// 没有，或者不足
		// if err.Error() == ERR_NO_ASSETS {
		// 	// 完全没有
		// } else {
		// 	// 有部分
		// }
	}
	var inscribe *InscribeResv
	wantToMint :=  indexer.DecimalSub(requiredAmt, totalAsset)
	if wantToMint.Sign() > 0 {
		// 再铸造一个，加入selected中，还没有广播
		inscribe, err = p.MintTransfer_brc20(localAddr, &name.AssetName, wantToMint, nil, feeRate, false)
		if err != nil {
			Log.Errorf("MintTransfer_brc20 failed, %v", err)
			return nil, nil, 0, nil, err
		}
		output := indexer.GenerateTxOutput(inscribe.RevealTx, 0)
		output.Assets = indexer.TxAssets{indexer.AssetInfo{
			Name: name.AssetName,
			Amount: *wantToMint,
			BindingSat: 0,
		}}
		selected = append(selected, output)
	}
	changePkScript := selected[0].OutValue.PkScript

	tx := wire.NewMsgTx(wire.TxVersion)
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	totalOutputSats := int64(0)
	for _, output := range selected {
		tx.AddTxIn(output.TxIn())
		prevFetcher.AddPrevOut(*output.OutPoint(), &output.OutValue)

		value := output.Value()
		txOut := &wire.TxOut{
			PkScript: destPkScript,
			Value:    value,
		}
		tx.AddTxOut(txOut)
		weightEstimate.AddTxOutput(txOut)
		totalOutputSats += value
	}


	feeValue := total - totalOutputSats
	fee0 := weightEstimate.Fee(feeRate)
	if feeValue < fee0 {
		// 增加fee
		var selected []*TxOutput
		selected, feeValue, err = p.SelectUtxosForFee(
			localAddr, nil, feeValue,
			feeRate, &weightEstimate, false, false)
		if err != nil {
			if inscribe != nil {
				p.utxoLockerL1.UnlockUtxosWithTx(inscribe.CommitTx)
			}
			return nil, nil, 0, nil, err
		}
		for _, output := range selected {
			tx.AddTxIn(output.TxIn())
			prevFetcher.AddPrevOut(*output.OutPoint(), &output.OutValue)
		}
	}

	fee0 = weightEstimate.Fee(feeRate)
	weightEstimate.AddP2TROutput() // fee change
	fee1 := weightEstimate.Fee(feeRate)

	if feeValue < fee0 {
		if inscribe != nil {
			p.utxoLockerL1.UnlockUtxosWithTx(inscribe.CommitTx)
		}
		return nil, nil, 0, nil, fmt.Errorf("no enough fee")
	}

	feeChange := feeValue - fee1
	if feeChange >= 330 {
		txOut3 := &wire.TxOut{
			PkScript: changePkScript,
			Value:    int64(feeChange),
		}
		tx.AddTxOut(txOut3)
	} else {
		feeChange = 0
	}
	if len(memo) > 0 {
		txOut4 := &wire.TxOut{
			PkScript: memo,
			Value:    0,
		}
		tx.AddTxOut(txOut4)
	}

	return tx, prevFetcher, feeValue - feeChange, inscribe, nil
}

// 发送指定资产
func (p *Manager) SendAssets(destAddr string, assetName string,
	amt string, feeRate int64, memo []byte) (*wire.MsgTx, error) {

	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}

	if !IsValidNullData(memo) {
		return nil, fmt.Errorf("invalid length of null data %d", len(memo))
	}
	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	tx, _, err := p.BatchSendAssets(destAddr, assetName, amt, 1, feeRate, memo)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

// 仅仅是估算，并且尽可能多预估了输入和输出
func CalcFee_SendTx(inputLen, outputLen, feeLen int, assetName *AssetName,
	amt *Decimal, feeRate int64, bInChannel bool) int64 {

	var weightEstimate utils.TxWeightEstimator

	for range outputLen {
		weightEstimate.AddP2TROutput() // splicing out
	}

	// fee utxo
	for i := 0; i < feeLen; i++ {
		if bInChannel {
			weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
		} else {
			weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		}
	}

	if NeedStubUtxoForAsset(assetName, amt) {
		// 需要一个用户的stub utxo作为输入，放在资产输入的上面
		if bInChannel {
			weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
		} else {
			weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		}
	}

	// input utxos
	for i := 0; i < inputLen; i++ {
		if bInChannel {
			weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
		} else {
			weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		}
	}

	if bInChannel {
		weightEstimate.AddP2WSHOutput() // asset change
		weightEstimate.AddP2WSHOutput() // fees change
	} else {
		weightEstimate.AddP2TROutput() // asset change
		weightEstimate.AddP2TROutput() // fees change
	}

	requiredFee := weightEstimate.Fee(feeRate)
	switch assetName.Protocol {
	case indexer.PROTOCOL_NAME_BRC20:

	case indexer.PROTOCOL_NAME_RUNES:
		var payload [txscript.MaxDataCarrierSize]byte
		weightEstimate.AddOutput(payload[:]) // op_return
		requiredFee = weightEstimate.Fee(feeRate)
		requiredFee += 330 // splicing-out utxo
	default:
	}

	return requiredFee
}

// 该Tx还没有广播或者广播了还没有确认，才有可能重建，索引器的限制，utxo被花费后就删除了
// 只支持一种资产
func (p *Manager) RebuildTxOutput(tx *wire.MsgTx) ([]*TxOutput, []*TxOutput, error) {
	// 尝试为tx的输出分配资产
	// 按ordx协议的规则
	// 按runes协议的规则
	// 增加brc20的规则：在transfer时，可以认为是直接绑定在一个聪上，容纳所有brc20的资产，由indexer.TxOutput执行相关规则
	var inputs []*TxOutput
	var input *TxOutput
	for _, txIn := range tx.TxIn {
		if txIn.PreviousOutPoint.Index == swire.MaxPrevOutIndex {
			continue
		}
		utxo := txIn.PreviousOutPoint.String()

		info, err := p.l1IndexerClient.GetTxOutput(utxo)
		if err != nil {
			return nil, nil, err
		}

		if input == nil {
			input = info
		} else {
			input.Append(info)
		}
		inputs = append(inputs, info.Clone())
	}

	txId := tx.TxID()
	outputs := make([]*TxOutput, 0)
	defaultRuneOutput := -1
	var err error
	var edicts []runestone.Edict
	for i, txOut := range tx.TxOut {
		var curr *indexer.TxOutput
		if indexer.IsOpReturn(txOut.PkScript) {
			stone := runestone.Runestone{}
			result, err := stone.DecipherFromPkScript(txOut.PkScript)
			if err == nil {
				if result.Runestone != nil {
					edicts = result.Runestone.Edicts
				}
			}
			if txOut.Value != 0 {
				curr, input, err = input.Cut(txOut.Value)
				if err != nil {
					Log.Errorf("Cut failed, %v", err)
					return nil, nil, err
				}
				curr.OutValue.PkScript = txOut.PkScript
			} else {
				curr = indexer.GenerateTxOutput(tx, i)
			}
		} else {
			curr, input, err = input.Cut(txOut.Value)
			if err != nil {
				Log.Errorf("Cut failed, %v", err)
				return nil, nil, err
			}
			curr.OutValue.PkScript = txOut.PkScript
			if defaultRuneOutput == -1 {
				defaultRuneOutput = i
			}
		}
		curr.OutPointStr = fmt.Sprintf("%s:%d", txId, i)
		outputs = append(outputs, curr)
	}

	// 执行runes的转移规则
	for _, edict := range edicts {
		if int(edict.Output) >= len(tx.TxOut) {
			return nil, nil, fmt.Errorf("invalid edict %v", edict)
		}

		tickerInfo := p.GetTickerInfoFromRuneId(edict.ID.String())
		if tickerInfo == nil {
			return nil, nil, fmt.Errorf("can't find tick %s", edict.ID.String())
		}
		assetName := tickerInfo.AssetName
		amount := indexer.NewDecimalFromUint128(edict.Amount, tickerInfo.Divisibility)

		asset := indexer.AssetInfo{
			Name:       assetName,
			Amount:     *amount,
			BindingSat: 0,
		}

		output := outputs[edict.Output]
		if output.Assets != nil {
			output.Assets.Add(&asset)
		} else {
			output.Assets = indexer.TxAssets{asset}
		}

		output = outputs[defaultRuneOutput]
		err := output.Assets.Subtract(&asset)
		if err != nil {
			return nil, nil, err
		}
	}

	// 看看brc20的transfer铭文输出到哪个txOut，重建资产数据
	// brc20 在transfer时，可以认为是直接绑定在一个聪上，其bindingSat为maxInt64，容纳所有brc20的资产
	// brc20AssetsMap := make(map[string]*SendAssetInfo)
	// for _, output := range outputs {
	// 	i := 0
	// 	for i < len(output.Assets) {
	// 		asset := output.Assets[i]
	// 		if asset.Name.Protocol == indexer.PROTOCOL_NAME_BRC20 {
	// 			addr, err := AddrFromPkScript(output.OutValue.PkScript)
	// 			if err != nil {
	// 				return nil, nil, nil, err
	// 			}
				
	// 			info, ok := brc20AssetsMap[addr]
	// 			if ok {
	// 				if info.AssetName.String() != asset.Name.String() {
	// 					return nil, nil, nil, fmt.Errorf("has more than one asset %s, %s", asset.Name.String(), info.AssetName.String())
	// 				}
	// 				info.AssetAmt = info.AssetAmt.Add(&asset.Amount)
	// 			} else {
	// 				brc20AssetsMap[addr] = &SendAssetInfo{
	// 					Address: addr,
	// 					Value: 0,
	// 					AssetName: &asset.Name,
	// 					AssetAmt: &asset.Amount,
	// 				}
	// 			}
	// 			// 将brc20资产从output中去掉
	// 			output.Assets = utils.RemoveIndex(output.Assets, i)
	// 		} else {
	// 			i++
	// 		}
	// 	}
	// 	if len(output.Assets) == 0 {
	// 		output.Assets = nil
	// 		output.SatBindingMap = make(map[int64]*indexer.AssetInfo)
	// 	}
	// }

	return inputs, outputs, nil
}

// 只用于白聪输出，包括fee
func (p *Manager) SelectUtxosForPlainSats(
	address string, excludedUtxoMap map[string]bool,
	requiredValue int64, feeRate int64,
	tx *wire.MsgTx, weightEstimate *utils.TxWeightEstimator,
	excludeRecentBlock, inChannel bool,
) (*txscript.MultiPrevOutFetcher, []byte, int64, int64, int64, error) {
	/* 规则：
	1. 先根据目标输出的value，先选1个，或者最多5个utxo，其聪数量不大于value
	2. 再从其余的聪数量大于330聪的utxo中，凑齐足够的network fee，注意每增加一个输入，其交易的fee就会增加一些
	3. 如果上面的选择方式找不到足够的utxo，就按照老的流程，从最大的开始找。
	*/

	utxos := p.l1IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
	if len(utxos) == 0 {
		return nil, nil, 0, 0, 0, fmt.Errorf("no plain sats")
	}
	changePkScript := utxos[0].PkScript
	p.utxoLockerL1.Reload(address)

	selected := make(map[string]*indexerwire.TxOutputInfo)
	localWeightEstimate := *weightEstimate
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	txIns := make([]*wire.TxIn, 0)

	// 先选满足条件的主utxo
	total := int64(0)
	for _, u := range utxos {
		if _, ok := excludedUtxoMap[u.OutPoint]; ok {
			continue
		}
		if p.utxoLockerL1.IsLocked(u.OutPoint) {
			continue
		}
		if excludeRecentBlock {
			if p.IsRecentBlockUtxo(u.UtxoId) {
				continue
			}
		}
		if u.Value == 330 {
			continue
		}
		if u.Value >= requiredValue {
			continue
		}
		selected[u.OutPoint] = u

		txOut := OutputInfoToOutput(u)
		outpoint := txOut.OutPoint()
		out := txOut.OutValue
		txIn := wire.NewTxIn(outpoint, nil, nil)
		txIns = append(txIns, txIn)
		//tx.AddTxIn(txIn)
		prevFetcher.AddPrevOut(*outpoint, &out)
		if inChannel {
			localWeightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
		} else {
			localWeightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		}
		total += u.Value
		if total >= requiredValue {
			break
		}
	}

	// 再选小的utxo作为fee
	for i := len(utxos) - 1; i >= 0; i-- {
		u := utxos[i]
		if _, ok := excludedUtxoMap[u.OutPoint]; ok {
			continue
		}
		if p.utxoLockerL1.IsLocked(u.OutPoint) {
			continue
		}
		if excludeRecentBlock {
			if p.IsRecentBlockUtxo(u.UtxoId) {
				continue
			}
		}
		if u.Value == 330 {
			continue
		}
		if _, ok := selected[u.OutPoint]; ok {
			continue
		}

		txOut := OutputInfoToOutput(utxos[i])
		outpoint := txOut.OutPoint()
		out := txOut.OutValue

		txIn := wire.NewTxIn(outpoint, nil, nil)
		//tx.AddTxIn(txIn)
		txIns = append(txIns, txIn)
		prevFetcher.AddPrevOut(*outpoint, &out)
		if inChannel {
			localWeightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
		} else {
			localWeightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		}
		total += out.Value
		if requiredValue+localWeightEstimate.Fee(feeRate) <= total {
			break
		}
	}
	fee0 := localWeightEstimate.Fee(feeRate)
	changeOutput := total - requiredValue - fee0
	if changeOutput >= 0 {
		*weightEstimate = localWeightEstimate
		for _, txIn := range txIns {
			tx.AddTxIn(txIn)
		}
		return prevFetcher, changePkScript, requiredValue, changeOutput, fee0, nil
	}

	// 上面所选的utxo不够，换成老的方案：
	prevFetcher = txscript.NewMultiPrevOutFetcher(nil)
	total = 0
	for _, u := range utxos {
		if _, ok := excludedUtxoMap[u.OutPoint]; ok {
			continue
		}
		if p.utxoLockerL1.IsLocked(u.OutPoint) {
			continue
		}
		if excludeRecentBlock {
			if p.IsRecentBlockUtxo(u.UtxoId) {
				continue
			}
		}
		txOut := OutputInfoToOutput(u)

		outpoint := txOut.OutPoint()
		out := txOut.OutValue

		txIn := wire.NewTxIn(outpoint, nil, nil)
		tx.AddTxIn(txIn)
		prevFetcher.AddPrevOut(*outpoint, &out)
		if inChannel {
			weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
		} else {
			weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		}
		total += out.Value
		if requiredValue+weightEstimate.Fee(feeRate) <= total {
			break
		}
	}

	fee0 = weightEstimate.Fee(feeRate)
	changeOutput = total - requiredValue - fee0
	if changeOutput < 0 {
		if requiredValue == total {
			// 很可能是用户选择了MAX，所以需要修改输出
			requiredValue -= fee0
			changeOutput = 0
		} else {
			return nil, nil, 0, 0, 0, fmt.Errorf("no enough plain sats, required %d but only %d",
				requiredValue+fee0, total)
		}
	}

	return prevFetcher, changePkScript, requiredValue, changeOutput, fee0, nil
}

// TODO GetUtxoListWithTicker。对于brc20，返回transfer铭文
func (p *Manager) SelectUtxosForAsset(address string, excludedUtxoMap map[string]bool,
	assetName *indexer.AssetName, requiredAmt *Decimal,
	weightEstimate *utils.TxWeightEstimator, excludeRecentBlock, inChannel bool) (
	[]*TxOutput, *Decimal, int64, error) {

	utxos := p.l1IndexerClient.GetUtxoListWithTicker(address, assetName)
	if len(utxos) == 0 {
		return nil, nil, 0, fmt.Errorf(ERR_NO_ASSETS)
	}
	p.utxoLockerL1.Reload(address)

	localWeightEstimate := *weightEstimate
	// 先选满足条件的utxo
	total := int64(0)
	var totalAsset *Decimal
	selected := make([]*TxOutput, 0)
	for _, u := range utxos {
		if _, ok := excludedUtxoMap[u.OutPoint]; ok {
			continue
		}
		if p.utxoLockerL1.IsLocked(u.OutPoint) {
			continue
		}
		if excludeRecentBlock {
			if p.IsRecentBlockUtxo(u.UtxoId) {
				continue
			}
		}
		txOut := OutputInfoToOutput(u)
		if HasMultiAsset(txOut) {
			continue
		}
		assetAmt := txOut.GetAsset(assetName)
		if assetAmt.Cmp(requiredAmt) > 0 {
			continue
		}
		RemoveNFTAsset(txOut)

		selected = append(selected, txOut)
		if inChannel {
			localWeightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
		} else {
			localWeightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		}
		total += txOut.OutValue.Value
		totalAsset = totalAsset.Add(assetAmt)
		if totalAsset.Cmp(requiredAmt) >= 0 {
			break
		}
	}
	if totalAsset.Cmp(requiredAmt) >= 0 {
		*weightEstimate = localWeightEstimate
		return selected, totalAsset, total, nil
	}

	// 上面所选的utxo不够，换成老的方案：
	total = int64(0)
	totalAsset = nil
	selected = make([]*TxOutput, 0)
	for _, u := range utxos {
		if _, ok := excludedUtxoMap[u.OutPoint]; ok {
			continue
		}
		if p.utxoLockerL1.IsLocked(u.OutPoint) {
			continue
		}
		if excludeRecentBlock {
			if p.IsRecentBlockUtxo(u.UtxoId) {
				continue
			}
		}
		txOut := OutputInfoToOutput(u)
		if HasMultiAsset(txOut) {
			continue
		}
		RemoveNFTAsset(txOut)

		selected = append(selected, txOut)
		if inChannel {
			weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
		} else {
			weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		}
		total += txOut.OutValue.Value
		assetAmt := txOut.GetAsset(assetName)
		totalAsset = totalAsset.Add(assetAmt)
		if totalAsset.Cmp(requiredAmt) >= 0 {
			break
		}
	}

	if totalAsset.Cmp(requiredAmt) < 0 {
		return selected, totalAsset, total, fmt.Errorf(ERR_NO_ENOUGH_ASSETS)
	}
	return selected, totalAsset, total, nil
}

// 选择合适大小的utxo，而不是从最大的utxo选择
func (p *Manager) SelectUtxosForFee(
	address string, excludedUtxoMap map[string]bool,
	feeValue int64, feeRate int64,
	weightEstimate *utils.TxWeightEstimator,
	excludeRecentBlock, inChannel bool) ([]*TxOutput, int64, error) {

	fee0 := weightEstimate.Fee(feeRate)
	localFeeValue := feeValue
	requiredFee := fee0 - localFeeValue
	if requiredFee <= 0 {
		// 不需要新增加fee
		return nil, feeValue, nil
	}

	feeOutputs := p.l1IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
	if len(feeOutputs) == 0 {
		Log.Errorf("no plain sats")
		return nil, 0, fmt.Errorf("no plain sats")
	}

	localWeightEstimate := *weightEstimate
	selected := make([]*TxOutput, 0)
	for _, out := range feeOutputs {
		if _, ok := excludedUtxoMap[out.OutPoint]; ok {
			continue
		}
		if p.utxoLockerL1.IsLocked(out.OutPoint) {
			continue
		}
		if excludeRecentBlock {
			if p.IsRecentBlockUtxo(out.UtxoId) {
				continue
			}
		}
		if out.Value == 330 {
			continue
		}
		if out.Value > requiredFee {
			continue
		}

		output := OutputInfoToOutput(out)
		localFeeValue += out.Value
		selected = append(selected, output)
		if inChannel {
			localWeightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
		} else {
			localWeightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		}
		if localFeeValue >= localWeightEstimate.Fee(feeRate) {
			break
		}
	}
	if localFeeValue >= localWeightEstimate.Fee(feeRate) {
		*weightEstimate = localWeightEstimate
		return selected, localFeeValue, nil
	}

	// 用老的方案，重新来一遍
	selected = make([]*TxOutput, 0)
	for _, out := range feeOutputs {
		if _, ok := excludedUtxoMap[out.OutPoint]; ok {
			continue
		}
		if p.utxoLockerL1.IsLocked(out.OutPoint) {
			continue
		}
		if excludeRecentBlock {
			if p.IsRecentBlockUtxo(out.UtxoId) {
				continue
			}
		}
		output := OutputInfoToOutput(out)
		feeValue += out.Value
		selected = append(selected, output)
		if inChannel {
			weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
		} else {
			weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		}
		if feeValue >= weightEstimate.Fee(feeRate) {
			break
		}
	}

	if feeValue < weightEstimate.Fee(feeRate) {
		return nil, 0, fmt.Errorf("no enough plain sats")
	}

	return selected, feeValue, nil
}

func (p *Manager) SelectUtxosForAsset_SatsNet(address string,
	excludedUtxoMap map[string]bool,
	assetName *indexer.AssetName, requiredAmt *Decimal) (
	[]*TxOutput_SatsNet, *Decimal, int64, error) {

	utxos := p.l2IndexerClient.GetUtxoListWithTicker(address, assetName)
	if len(utxos) == 0 {
		return nil, nil, 0, fmt.Errorf("no assets")
	}
	p.utxoLockerL2.Reload(address)

	// 先选满足条件的utxo
	totalPlainSats := int64(0)
	var totalAsset *Decimal
	selected := make([]*TxOutput_SatsNet, 0)
	bigger := make([]*TxOutput_SatsNet, 0)
	for _, u := range utxos {
		if _, ok := excludedUtxoMap[u.OutPoint]; ok {
			continue
		}
		if p.utxoLockerL2.IsLocked(u.OutPoint) {
			continue
		}
		txOut := OutputInfoToOutput_SatsNet(u)
		assetAmt := txOut.GetAsset(assetName)
		if assetAmt.Cmp(requiredAmt) > 0 {
			bigger = append(bigger, txOut)
			continue
		}

		selected = append(selected, txOut)
		totalPlainSats += txOut.GetPlainSat()
		totalAsset = totalAsset.Add(assetAmt)
		if totalAsset.Cmp(requiredAmt) >= 0 {
			break
		}
	}
	if totalAsset.Cmp(requiredAmt) >= 0 {
		return selected, totalAsset, totalPlainSats, nil
	}

	// 上面所选的utxo不够，接着从bigger的尾部往前添加
	for i := len(bigger) - 1; i >= 0; i-- {
		txOut := bigger[i]
		selected = append(selected, txOut)
		totalPlainSats += txOut.GetPlainSat()
		assetAmt := txOut.GetAsset(assetName)
		totalAsset = totalAsset.Add(assetAmt)
		if totalAsset.Cmp(requiredAmt) >= 0 {
			break
		}
	}

	if totalAsset.Cmp(requiredAmt) < 0 {
		return nil, nil, 0, fmt.Errorf("no enough assets")
	}
	return selected, totalAsset, totalPlainSats, nil
}

// 选择合适大小的utxo，而不是从最大的utxo选择
func (p *Manager) SelectUtxosForFee_SatsNet(address string, excludedUtxoMap map[string]bool,
	feeValue int64) ([]*TxOutput_SatsNet, int64, error) {
	requiredFee := DEFAULT_FEE_SATSNET - feeValue
	if requiredFee <= 0 {
		// 不需要新增加fee
		return nil, feeValue, nil
	}

	feeOutputs := p.l2IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
	if len(feeOutputs) == 0 {
		Log.Errorf("no plain sats")
		return nil, 0, fmt.Errorf("no plain sats")
	}

	var totalPlainSats int64
	bigger := make([]*TxOutput_SatsNet, 0)
	selected := make([]*TxOutput_SatsNet, 0)
	for _, out := range feeOutputs {
		if _, ok := excludedUtxoMap[out.OutPoint]; ok {
			continue
		}
		if p.utxoLockerL2.IsLocked(out.OutPoint) {
			continue
		}
		txOut := OutputInfoToOutput_SatsNet(out)
		plainSats := txOut.GetPlainSat()
		if plainSats > requiredFee {
			bigger = append(bigger, txOut)
			continue
		}

		totalPlainSats += plainSats
		selected = append(selected, txOut)
		if totalPlainSats >= requiredFee {
			break
		}
	}
	if totalPlainSats >= requiredFee {
		return selected, totalPlainSats, nil
	}

	// 上面所选的utxo不够，接着从bigger的尾部往前添加
	for i := len(bigger) - 1; i >= 0; i-- {
		txOut := bigger[i]
		selected = append(selected, txOut)
		totalPlainSats += txOut.GetPlainSat()
		if totalPlainSats >= requiredFee {
			break
		}
	}

	if totalPlainSats < requiredFee {
		return nil, 0, fmt.Errorf("no enough plain sats")
	}

	return selected, totalPlainSats, nil
}

func (p *Manager) SelectUtxosForAssetV2(address string, excludedUtxoMap map[string]bool,
	assetName *indexer.AssetName, requiredAmt *Decimal, excludeRecentBlock bool) ([]string, error) {

	var weightEstimate utils.TxWeightEstimator
	selected, _, _, err := p.SelectUtxosForAsset(address, excludedUtxoMap, assetName,
		requiredAmt, &weightEstimate, excludeRecentBlock, false)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0)
	for _, txOut := range selected {
		result = append(result, txOut.OutPointStr)
	}
	return result, nil
}

// 选择合适大小的utxo，而不是从最大的utxo选择
func (p *Manager) SelectUtxosForFeeV2(
	address string, excludedUtxoMap map[string]bool,
	requiredValue int64,
	excludeRecentBlock, inChannel bool) ([]string, error) {

	if address == "" {
		address = p.wallet.GetAddress()
	}

	utxos := p.l1IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
	p.utxoLockerL1.Reload(address)
	if requiredValue == 0 {
		requiredValue = MAX_FEE
	}

	bigger := make([]*indexerwire.TxOutputInfo, 0)
	result := make([]string, 0)
	total := int64(0)
	for _, u := range utxos {
		utxo := u.OutPoint
		if excludeRecentBlock {
			if p.IsRecentBlockUtxo(u.UtxoId) {
				continue
			}
		}
		if _, ok := excludedUtxoMap[utxo]; ok {
			continue
		}
		if p.utxoLockerL1.IsLocked(utxo) {
			continue
		}
		if u.Value > requiredValue {
			bigger = append(bigger, u)
			continue
		}

		total += u.Value
		result = append(result, utxo)
		if total >= requiredValue {
			break
		}
	}

	if total >= requiredValue {
		return result, nil
	}

	// 上面所选的utxo不够，接着从bigger的尾部往前添加
	for i := len(bigger) - 1; i >= 0; i-- {
		txOut := bigger[i]
		result = append(result, txOut.OutPoint)
		output := OutputInfoToOutput(txOut)
		total += output.GetPlainSat()
		if total >= requiredValue {
			break
		}
	}

	if total < requiredValue {
		return nil, fmt.Errorf("no enough utxo for fee, require %d but only %d", requiredValue, total)
	}

	return result, nil
}

func (p *Manager) SelectUtxosForAssetV2_SatsNet(address string,
	excludedUtxoMap map[string]bool,
	assetName *indexer.AssetName, requiredAmt *Decimal) ([]string, error) {

	selected, _, _, err := p.SelectUtxosForAsset_SatsNet(address, excludedUtxoMap, assetName,
		requiredAmt)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0)
	for _, txOut := range selected {
		result = append(result, txOut.OutPointStr)
	}
	return result, nil
}

// 选择合适大小的utxo，而不是从最大的utxo选择
func (p *Manager) SelectUtxosForFeeV2_SatsNet(address string, excludedUtxoMap map[string]bool,
	requiredValue int64) ([]string, error) {
	if address == "" {
		address = p.wallet.GetAddress()
	}

	utxos := p.l2IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
	p.utxoLockerL2.Reload(address)
	if requiredValue == 0 {
		requiredValue = DEFAULT_FEE_SATSNET
	}

	bigger := make([]*TxOutput_SatsNet, 0)
	result := make([]string, 0)
	total := int64(0)
	for _, u := range utxos {
		utxo := u.OutPoint
		if _, ok := excludedUtxoMap[utxo]; ok {
			continue
		}
		if p.utxoLockerL2.IsLocked(utxo) {
			continue
		}
		output := OutputInfoToOutput_SatsNet(u)
		plainSats := output.GetPlainSat()
		if plainSats > requiredValue {
			bigger = append(bigger, output)
			continue
		}

		total += plainSats
		result = append(result, utxo)
		if total >= requiredValue {
			break
		}
	}

	if total >= requiredValue {
		return result, nil
	}

	// 上面所选的utxo不够，接着从bigger的尾部往前添加
	for i := len(bigger) - 1; i >= 0; i-- {
		txOut := bigger[i]
		result = append(result, txOut.OutPointStr)
		total += txOut.GetPlainSat()
		if total >= requiredValue {
			break
		}
	}

	if total < requiredValue {
		return nil, fmt.Errorf("no enough utxo for fee, require %d but only %d", requiredValue, total)
	}

	return result, nil
}



// v3
// 构造发送1聪资产到指定地址的Tx，需要提前准备stub,返回值包括了stub的聪
func (p *Manager) BuildSendOrdxTxWithStub(destAddr string, assetName *AssetName,
	amt int64, stub string, feeRate int64, memo []byte, excludeRecentBlock bool) (*wire.MsgTx, *txscript.MultiPrevOutFetcher, int64, error) {

	destPkScript, err := GetPkScriptFromAddress(destAddr)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("GetPkScriptFromAddress %s failed. %v", destAddr, err)
	}
	
	address := p.wallet.GetAddress()
	utxos := p.l1IndexerClient.GetUtxoListWithTicker(address, &assetName.AssetName)
	if len(utxos) == 0 {
		return nil, nil, 0, fmt.Errorf("no assets %s in %s", assetName.String(), address)
	}

	total := int64(0)
	var totalAsset *Decimal
	inputVect := make([]*TxOutput, 0)
	var weightEstimate utils.TxWeightEstimator
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)

	stubInfo, err := p.GetTxOutFromRawTx(stub)
	if err != nil {
		return nil, nil, 0, err
	}
	weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
	
	total += stubInfo.Value()
	inputVect = append(inputVect, stubInfo)
	outpoint := stubInfo.OutPoint()
	out := stubInfo.OutValue
	prevFetcher.AddPrevOut(*outpoint, &out)

	p.utxoLockerL1.Reload(address)
	var changePkScript []byte
	for _, u := range utxos {
		if p.utxoLockerL1.IsLocked(u.OutPoint) {
			continue
		}
		if excludeRecentBlock {
            if p.IsRecentBlockUtxo(u.UtxoId) {
                continue
            }
        }
		txOut := OutputInfoToOutput(u)

		if HasMultiAsset(txOut) {
			continue
		}
		RemoveNFTAsset(txOut) //  否则TxOutput.Split 会失败

		inputVect = append(inputVect, txOut)
		outpoint := txOut.OutPoint()
		out := txOut.OutValue

		prevFetcher.AddPrevOut(*outpoint, &out)
		weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)

		total += out.Value
		assetAmt := txOut.GetAsset(&assetName.AssetName)
		totalAsset = totalAsset.Add(assetAmt)

		if changePkScript == nil && assetAmt.Sign() != 0 {
			changePkScript = out.PkScript
		}

		if totalAsset.Int64() >= amt {
			break
		}
	}
	if totalAsset.Int64() < amt {
		return nil, nil, 0, fmt.Errorf("no enough asset %s, required %d but only %d", assetName.String(), amt, totalAsset.Int64())
	}

	tx := wire.NewMsgTx(wire.TxVersion)
	allInput := indexer.NewTxOutput(0)
	for _, output := range inputVect {
		tx.AddTxIn(output.TxIn())
		allInput.Append(output)
	}

	totalOutputSats := int64(0)
	remainingOutput := allInput

	// 插桩
	var output *TxOutput
	requiredAmt := indexer.NewDefaultDecimal(amt)
	output, remainingOutput, err = remainingOutput.Split(&assetName.AssetName, 0, requiredAmt)
	if err != nil {
		return nil, nil, 0, err
	}
	if output.Value() >= 330 { // 目标输出
		txOut0 := &wire.TxOut{
			PkScript: destPkScript,
			Value:    output.Value(),
		}
		tx.AddTxOut(txOut0)
		weightEstimate.AddTxOutput(txOut0)
		totalOutputSats += output.Value()
	} else {
		return nil, nil, 0, fmt.Errorf("output is less than 330 sats")
	}

	if len(memo) > 0 {
		weightEstimate.AddOutput(memo[:]) // op_return
	}

	var feeValue int64
	// 资产余额
	if totalAsset.Cmp(requiredAmt) > 0 {
		assetChange := indexer.DecimalSub(totalAsset, requiredAmt)

		// 这里有可能remainingOutput不足330，需要提前补足
		if remainingOutput.Value() < 330 {
			// 接入feeValue
			feeValue -= 330
			remainingOutput.OutValue.Value += 330
		}

		var output *TxOutput
		output, remainingOutput, err = remainingOutput.Split(&assetName.AssetName, 0, assetChange)
		if err != nil {
			return nil, nil, 0, err
		}
		if output.Value() >= 330 { // change
			txOut1 := &wire.TxOut{
				PkScript: changePkScript,
				Value:    output.Value(),
			}
			tx.AddTxOut(txOut1)
			weightEstimate.AddTxOutput(txOut1)
			totalOutputSats += output.Value()
		} else {
			return nil, nil, 0, fmt.Errorf("output is less than 330 sats")
		}
	}

	fee0 := weightEstimate.Fee(feeRate)
	if remainingOutput != nil {
		feeValue += remainingOutput.Value()
	}

	if feeValue < fee0 {
		// 增加fee
		var selected []*TxOutput
		selected, feeValue, err = p.SelectUtxosForFeeV3(feeValue,
			feeRate, &weightEstimate, excludeRecentBlock)
		if err != nil {
			return nil, nil, 0, err
		}
		for _, output := range selected {
			tx.AddTxIn(output.TxIn())
			prevFetcher.AddPrevOut(*output.OutPoint(), &output.OutValue)
		}
	}

	fee0 = weightEstimate.Fee(feeRate)
	weightEstimate.AddP2TROutput() // fee change
	fee1 := weightEstimate.Fee(feeRate)

	if feeValue < fee0 {
		return nil, nil, 0, fmt.Errorf("no enough fee")
	}

	feeChange := feeValue - fee1
	if feeChange >= 330 {
		txOut2 := &wire.TxOut{
			PkScript: changePkScript,
			Value:    int64(feeChange),
		}
		tx.AddTxOut(txOut2)
	} else {
		feeChange = 0
	}
	if len(memo) > 0 {
		txOut3 := &wire.TxOut{
			PkScript: memo,
			Value:    0,
		}
		tx.AddTxOut(txOut3)
	}

	return tx, prevFetcher, feeValue - feeChange + stubInfo.Value(), nil
}

// 构建tx，发送给多个地址不同数量的资产。对于ordx资产，允许插桩，但这种情况下只允许一个地址
// 不需要提前预估手续费，能更好利用utxo资源。如果是发送白聪，支持全部发送（内部自动调整发送数量）。
func (p *Manager) BatchSendAssetsV3(dest []*SendAssetInfo,
	assetNameStr string, feeRate int64, memo []byte,
	reason string) (string, int64, error) {

	if p.wallet == nil {
		return "", 0, fmt.Errorf("wallet is not created/unlocked")
	}
	if !IsValidNullData(memo) {
		return "", 0, fmt.Errorf("invalid length of null data %d", len(memo))
	}

	name := ParseAssetString(assetNameStr)
	if name == nil {
		return "", 0, fmt.Errorf("invalid asset name %s", assetNameStr)
	}
	tickerInfo := p.getTickerInfo(name)
	if tickerInfo == nil {
		return "", 0, fmt.Errorf("can't get ticker %s info", assetNameStr)
	}
	assetName := GetAssetName(tickerInfo)
	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	var tx *wire.MsgTx
	var prevFetcher *txscript.MultiPrevOutFetcher
	var fee int64
	var err error

	switch name.Protocol {
	case "": // btc
		tx, prevFetcher, fee, err = p.BuildBatchSendTxV3_btc(dest, feeRate, memo, false)
	case indexer.PROTOCOL_NAME_ORDX:
		tx, prevFetcher, fee, err = p.BuildBatchSendTxV3_ordx(dest, assetName, feeRate, memo, false)
	case indexer.PROTOCOL_NAME_RUNES:
		if len(memo) != 0 { // TODO 等主网支持多个op_return后再修改
			return "", 0, fmt.Errorf("do not attach memo when send runes asset")
		}
		tx, prevFetcher, fee, err = p.BuildBatchSendTxV3_runes(dest, assetName, feeRate, false)
	case indexer.PROTOCOL_NAME_BRC20:
		tx, prevFetcher, fee, err = p.BuildBatchSendTxV3_brc20(dest, assetName, feeRate, false)
	default:
		return "", 0, fmt.Errorf("BatchSendAssetsV3 unsupport protocol %s", name.Protocol)
	}
	if err != nil {
		return "", 0, err
	}

	
	// sign
	tx, err = p.SignTx(tx, prevFetcher)
	if err != nil {
		Log.Errorf("SignTx_SatsNet failed. %v", err)
		return "", 0, err
	}

	PrintJsonTx(tx, "BatchSendAssetsV3")
	txid, err := p.BroadcastTx(tx)
	if err != nil {
		Log.Errorf("BatchSendAssetsV3 failed. %v", err)
		return "", 0, err
	}
	Log.Infof("BatchSendAssetsV3 succeed. %s %d", txid, fee)

	return txid, fee, nil
}

// 给多个地址发送不同数量的白聪，支持全部发送
func (p *Manager) BuildBatchSendTxV3_btc(dest []*SendAssetInfo,
	feeRate int64, memo []byte, excludeRecentBlock bool) (*wire.MsgTx, *txscript.MultiPrevOutFetcher, int64, error) {
	if p.wallet == nil {
		return nil, nil, 0, fmt.Errorf("wallet is not created/unlocked")
	}
	
	tx := wire.NewMsgTx(wire.TxVersion)
	var requiredValue int64
	var weightEstimate utils.TxWeightEstimator
	for _, d := range dest {
		if d.AssetName != nil {
			if d.AssetName.String() != indexer.ASSET_PLAIN_SAT.String() {
				return nil, nil, 0, fmt.Errorf("the asset name is not equal")
			}
		}
		pkScript, err := GetPkScriptFromAddress(d.Address)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("GetPkScriptFromAddress %s failed. %v", d.Address, err)
		}

		requiredValue += d.Value
		weightEstimate.AddP2TROutput() // output
		txOut := &wire.TxOut{
			PkScript: pkScript,
			Value:    d.Value,
		}
		tx.AddTxOut(txOut)
	}
	if len(memo) > 0 {
		weightEstimate.AddOutput(memo[:]) // op_return
	}

	prevFetcher, changePkScript, outputValue, changeOutput, fee0, err := 
		p.SelectUtxosForPlainSatsV3(requiredValue, feeRate, excludeRecentBlock, tx, &weightEstimate)
	if err != nil {
		return nil, nil, 0, err
	}
	if outputValue != requiredValue {
		// 刚好输出等于所有可用的白聪，才会调整输出的聪数量
		// TODO　由调用方决定要不要调整
		if len(dest) != 1 {
			return nil, nil, 0, fmt.Errorf("no enough plain sats")
		}
		tx.TxOut[0].Value = outputValue
	}

	weightEstimate.AddP2TROutput() // fee
	fee1 := weightEstimate.Fee(feeRate)

	fee := fee0
	changeOutput += fee0 - fee1
	if changeOutput >= 330 {
		fee = fee1
		txOut2 := &wire.TxOut{
			PkScript: changePkScript,
			Value:    int64(changeOutput),
		}
		tx.AddTxOut(txOut2)
	}

	if len(memo) > 0 {
		txOut3 := &wire.TxOut{
			PkScript: memo,
			Value:    0,
		}
		tx.AddTxOut(txOut3)
	}

	return tx, prevFetcher, fee, nil
}

// 只用于白聪输出，包括fee
func (p *Manager) SelectUtxosForPlainSatsV3(
	requiredValue int64, feeRate int64, excludeRecentBlock bool, 
	tx *wire.MsgTx, weightEstimate *utils.TxWeightEstimator,
	) (*txscript.MultiPrevOutFetcher, []byte, int64, int64, int64, error) {
	address := p.wallet.GetAddress()
	return p.SelectUtxosForPlainSats(address, nil, requiredValue, 
		feeRate, tx, weightEstimate, excludeRecentBlock, false)
}

func (p *Manager) SelectUtxosForAssetV3(
	assetName *AssetName, requiredAmt *Decimal,
	weightEstimate *utils.TxWeightEstimator, excludeRecentBlock bool) ([]*TxOutput, *Decimal, int64, error) {

	address := p.wallet.GetAddress()	
	return p.SelectUtxosForAsset(address, nil, &assetName.AssetName, 
		requiredAmt, weightEstimate, excludeRecentBlock, false)
}

// 选择合适大小的utxo，而不是从最大的utxo选择
func (p *Manager) SelectUtxosForFeeV3(
	feeValue int64, feeRate int64,
	weightEstimate *utils.TxWeightEstimator,
	excludeRecentBlock bool) ([]*TxOutput, int64, error) {
	address := p.wallet.GetAddress()
	return p.SelectUtxosForFee(address, nil, 
		feeValue, feeRate, weightEstimate, excludeRecentBlock, false)
}

// 给不同地址发送不同数量的资产，只支持ordx协议，只支持一个桩
// 资产和聪分别放在两个utxo中
func (p *Manager) BuildBatchSendTxV3_ordx(dest []*SendAssetInfo,
	assetName *AssetName,
	feeRate int64, memo []byte, excludeRecentBlock bool) (
	*wire.MsgTx, *txscript.MultiPrevOutFetcher, int64, error) {

	tx := wire.NewMsgTx(wire.TxVersion)

	var requiredValue int64
	var requiredAmt *Decimal
	var destPkScript [][]byte
	var stubNum int
	for _, d := range dest {
		if d.AssetName != nil {
			if d.AssetName.String() != assetName.String() {
				return nil, nil, 0, fmt.Errorf("the asset name is not equal")
			}
		}
		pkScript, err := GetPkScriptFromAddress(d.Address)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("GetPkScriptFromAddress %s failed. %v", d.Address, err)
		}

		destPkScript = append(destPkScript, pkScript)
		requiredAmt = requiredAmt.Add(d.AssetAmt)
		if d.AssetAmt.Int64()%int64(assetName.N) != 0 {
			return nil, nil, 0, fmt.Errorf("the asset amt should be times of bindingsat")
		}
		if indexer.GetBindingSatNum(d.AssetAmt, uint32(assetName.N)) < 330 {
			stubNum++
		}
		requiredValue += d.Value
	}

	if stubNum > 1 {
		return nil, nil, 0, fmt.Errorf("only support to send a small asset to one address")
	} 
	if stubNum == 1 {
		if len(dest) > 1 {
			return nil, nil, 0, fmt.Errorf("only support to send a small asset to one address")
		}
		if requiredValue > 0 {
			return nil, nil, 0, fmt.Errorf("only support to send a small asset to one address")
		}

		var err error
		var stubs []string
		var stubFee int64
		
		stubs, err = p.GetUtxosForStubs("", stubNum, nil)
		if err != nil {
			// 重新生成
			stubTx, fee, err := p.GenerateStubUtxos(2, feeRate)
			if err != nil {
				Log.Errorf("GenerateStubUtxos %d failed, %v", stubNum+10, err)
				return nil, nil, 0, err
			}
			Log.Infof("buildBatchSendTxV3_ordx GenerateStubUtxos %s %d", stubTx, fee)
			for i := range stubNum {
				stubs = append(stubs, fmt.Sprintf("%s:%d", stubTx, i))
			}
			stubFee = fee
		}
		
		
		tx, prevFetch, fee, err := p.BuildSendOrdxTxWithStub(dest[0].Address, assetName, 
			dest[0].AssetAmt.Int64(), stubs[0], feeRate, memo, false)
		if err != nil {
			return nil, nil, stubFee, err
		}
		return tx, prevFetch, fee + stubFee, nil
	}

	var weightEstimate utils.TxWeightEstimator
	selected, totalAsset, total, err := p.SelectUtxosForAssetV3( 
		assetName, requiredAmt, &weightEstimate, excludeRecentBlock)
	if err != nil {
		return nil, nil, 0, err
	}
	var prefix int64
	selected, prefix, _ = AdjustInputsForSplicingIn(selected, assetName)
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	allInput := indexer.NewTxOutput(0)
	for _, output := range selected {
		tx.AddTxIn(output.TxIn())
		prevFetcher.AddPrevOut(*output.OutPoint(), &output.OutValue)
		allInput.Append(output)
	}
	changePkScript := selected[0].OutValue.PkScript


	totalOutputSats := int64(0)
	remainingOutput := allInput
	if prefix >= 330 {
		// 前面切割一小部分白聪出来
		var output *TxOutput
		output, remainingOutput, err = remainingOutput.Split(&ASSET_PLAIN_SAT, prefix, nil)
		if err != nil {
			return nil, nil, 0, err
		}

		txOut0 := &wire.TxOut{
			PkScript: changePkScript,
			Value:    output.Value(),
		}
		tx.AddTxOut(txOut0)
		weightEstimate.AddTxOutput(txOut0)
		totalOutputSats += output.Value()
	}

	for i, user := range dest {
		var output *TxOutput
		output, remainingOutput, err = remainingOutput.Split(&assetName.AssetName, 0, user.AssetAmt)
		if err != nil {
			return nil, nil, 0, err
		}
		if output.Value() >= 330 {
			txOut1 := &wire.TxOut{
				PkScript: destPkScript[i],
				Value:    output.Value(),
			}
			tx.AddTxOut(txOut1)
			weightEstimate.AddTxOutput(txOut1)
			totalOutputSats += output.Value()
		} else {
			// 前面已经处理过，这里不会出现这种情况
			return nil, nil, 0, fmt.Errorf("output is less than 330 sats")
		}
	}

	if len(memo) > 0 {
		weightEstimate.AddOutput(memo[:]) // op_return
	}

	// 资产余额：资产可能不足330，余额也可能不足330
	var feeValue int64
	if totalAsset.Cmp(requiredAmt) > 0 {
		if remainingOutput.OutValue.Value < 330 {
			// 先补充：从后面补充
			feeValue = remainingOutput.OutValue.Value - 330
			remainingOutput.OutValue.Value = 330
		}
		txOut2 := &wire.TxOut{
			PkScript: changePkScript,
			Value:    remainingOutput.Value(),
		}
		
		tx.AddTxOut(txOut2)
		weightEstimate.AddTxOutput(txOut2)
		totalOutputSats += txOut2.Value
	}

	// 输出白聪
	if requiredValue != 0 {
		for i, user := range dest {
			if user.Value >= 330 {
				txOut3 := &wire.TxOut{
					PkScript: destPkScript[i],
					Value:    user.Value,
				}
				tx.AddTxOut(txOut3)
				weightEstimate.AddTxOutput(txOut3)
				totalOutputSats += user.Value
			} else {
				return nil, nil, 0, fmt.Errorf("output is less than 330 sats")
			}
		}
	}

	// 剩下余额
	feeValue += total - totalOutputSats
	fee0 := weightEstimate.Fee(feeRate)
	if feeValue < fee0 {
		// 增加fee
		var selected []*TxOutput
		selected, feeValue, err = p.SelectUtxosForFeeV3(feeValue,
			feeRate, &weightEstimate, excludeRecentBlock)
		if err != nil {
			return nil, nil, 0, err
		}
		for _, output := range selected {
			tx.AddTxIn(output.TxIn())
			prevFetcher.AddPrevOut(*output.OutPoint(), &output.OutValue)
		}
	}

	fee0 = weightEstimate.Fee(feeRate)
	if feeValue < fee0 {
		return nil, nil, 0, fmt.Errorf("no enough fee")
	}

	weightEstimate.AddP2TROutput() // fee change
	fee1 := weightEstimate.Fee(feeRate)
	fee := fee0
	
	if feeValue < fee0 {
		return nil, nil, 0, fmt.Errorf("no enough fee")
	}

	feeChange := feeValue - fee1
	if feeChange >= 330 {
		fee = fee1
		txOut4 := &wire.TxOut{
			PkScript: changePkScript,
			Value:    int64(feeChange),
		}
		tx.AddTxOut(txOut4)
	} 
	if len(memo) > 0 {
		txOut5 := &wire.TxOut{
			PkScript: memo,
			Value:    0,
		}
		tx.AddTxOut(txOut5)
	}

	return tx, prevFetcher, fee, nil
}

// 给不同地址发送不同数量的资产，只支持runes协议, 目标地址不能太多，会超出op_return的限制
func (p *Manager) BuildBatchSendTxV3_runes(dest []*SendAssetInfo,
	assetName *AssetName,
	feeRate int64, excludeRecentBlock bool) (
	*wire.MsgTx, *txscript.MultiPrevOutFetcher, int64, error) {


	var requiredValue int64
	var requiredAmt *Decimal
	var destPkScript [][]byte
	for _, d := range dest {
		if d.AssetName != nil {
			if d.AssetName.String() != assetName.String() {
				return nil, nil, 0, fmt.Errorf("the asset name is not equal")
			}
		}
		pkScript, err := GetPkScriptFromAddress(d.Address)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("GetPkScriptFromAddress %s failed. %v", d.Address, err)
		}

		destPkScript = append(destPkScript, pkScript)
		requiredAmt = requiredAmt.Add(d.AssetAmt)
		requiredValue += d.Value
	}

	var weightEstimate utils.TxWeightEstimator
	selected, totalAsset, total, err := p.SelectUtxosForAssetV3( 
		assetName, requiredAmt, &weightEstimate, excludeRecentBlock)
	if err != nil {
		return nil, nil, 0, err
	}
	changePkScript := selected[0].OutValue.PkScript

	tx := wire.NewMsgTx(wire.TxVersion)
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	for _, output := range selected {
		tx.AddTxIn(output.TxIn())
		prevFetcher.AddPrevOut(*output.OutPoint(), &output.OutValue)
	}

	// 余额输出到第一个utxo中，其他目标地址从第二个utxo开始
	// 远端的资产使用edict明确表示输出数量
	transferEdicts := make([]runestone.Edict, 0)
	runeId, err := p.getRuneIdFromName(&assetName.AssetName)
	if err != nil {
		return nil, nil, 0, err
	}
	totalOutputSats := int64(0)
	if totalAsset.Cmp(requiredAmt) > 0 {
		// 先输出余额
		txOut0 := &wire.TxOut{
			PkScript: changePkScript,
			Value:    330,
		}
		tx.AddTxOut(txOut0)
		weightEstimate.AddTxOutput(txOut0)
		totalOutputSats += 330
	}

	for i, user := range dest {
		txOut1 := &wire.TxOut{
			PkScript: destPkScript[i],
			Value:    330,
		}
		tx.AddTxOut(txOut1)
		weightEstimate.AddTxOutput(txOut1)
		totalOutputSats += 330
		transferEdicts = append(transferEdicts, runestone.Edict{
			ID:     *runeId,
			Output: uint32(len(tx.TxOut) - 1),
			Amount: user.AssetAmt.ToUint128(),
		})
	}

	if len(transferEdicts) > 0 {
		nullDataScript, err := EncipherRunePayload(transferEdicts)
		if err != nil {
			Log.Errorf("too many edicts, %d, %v", len(transferEdicts), err)
			return nil, nil, 0, err
		}
		weightEstimate.AddOutput(nullDataScript)

		txOut2 := &wire.TxOut{
			PkScript: nullDataScript,
			Value:    int64(0),
		}
		tx.AddTxOut(txOut2)
	}

	// 输出白聪
	if requiredValue != 0 {
		for i, user := range dest {
			if user.Value >= 330 {
				txOut3 := &wire.TxOut{
					PkScript: destPkScript[i],
					Value:    user.Value,
				}
				tx.AddTxOut(txOut3)
				weightEstimate.AddTxOutput(txOut3)
				totalOutputSats += user.Value
			} else {
				return nil, nil, 0, fmt.Errorf("output is less than 330 sats")
			}
		}
	}

	// 剩下的都是白聪
	feeValue := total - totalOutputSats
	fee0 := weightEstimate.Fee(feeRate)
	if feeValue < fee0 {
		// 增加fee
		var selected []*TxOutput
		selected, feeValue, err = p.SelectUtxosForFeeV3(feeValue,
			feeRate, &weightEstimate, excludeRecentBlock)
		if err != nil {
			return nil, nil, 0, err
		}
		for _, output := range selected {
			tx.AddTxIn(output.TxIn())
			prevFetcher.AddPrevOut(*output.OutPoint(), &output.OutValue)
		}
	}

	fee0 = weightEstimate.Fee(feeRate)
	if feeValue < fee0 {
		return nil, nil, 0, fmt.Errorf("no enough fee")
	}

	weightEstimate.AddP2TROutput() // fee change
	fee1 := weightEstimate.Fee(feeRate)
	fee := fee0
	feeChange := feeValue - fee1
	if feeChange >= 330 {
		fee = fee1
		txOut3 := &wire.TxOut{
			PkScript: changePkScript,
			Value:    int64(feeChange),
		}
		tx.AddTxOut(txOut3)
	}

	return tx, prevFetcher, fee, nil
}


// 给不同地址发送不同数量的资产
func (p *Manager) BuildBatchSendTxV3_brc20(dest []*SendAssetInfo,
	assetName *AssetName,
	feeRate int64, excludeRecentBlock bool) (
	*wire.MsgTx, *txscript.MultiPrevOutFetcher, int64, error) {


	var requiredValue int64
	var requiredAmt *Decimal
	var destPkScript [][]byte
	for _, d := range dest {
		if d.AssetName != nil {
			if d.AssetName.String() != assetName.String() {
				return nil, nil, 0, fmt.Errorf("the asset name is not equal")
			}
		}
		pkScript, err := GetPkScriptFromAddress(d.Address)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("GetPkScriptFromAddress %s failed. %v", d.Address, err)
		}

		destPkScript = append(destPkScript, pkScript)
		requiredAmt = requiredAmt.Add(d.AssetAmt)
		requiredValue += d.Value
	}

	var weightEstimate utils.TxWeightEstimator
	selected, totalAsset, total, err := p.SelectUtxosForAssetV3( 
		assetName, requiredAmt, &weightEstimate, excludeRecentBlock)
	if err != nil {
		return nil, nil, 0, err
	}
	changePkScript := selected[0].OutValue.PkScript

	tx := wire.NewMsgTx(wire.TxVersion)
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	for _, output := range selected {
		tx.AddTxIn(output.TxIn())
		prevFetcher.AddPrevOut(*output.OutPoint(), &output.OutValue)
	}

	// 余额输出到第一个utxo中，其他目标地址从第二个utxo开始
	// 远端的资产使用edict明确表示输出数量
	transferEdicts := make([]runestone.Edict, 0)
	runeId, err := p.getRuneIdFromName(&assetName.AssetName)
	if err != nil {
		return nil, nil, 0, err
	}
	totalOutputSats := int64(0)
	if totalAsset.Cmp(requiredAmt) > 0 {
		// 先输出余额
		txOut0 := &wire.TxOut{
			PkScript: changePkScript,
			Value:    330,
		}
		tx.AddTxOut(txOut0)
		weightEstimate.AddTxOutput(txOut0)
		totalOutputSats += 330
	}

	for i, user := range dest {
		txOut1 := &wire.TxOut{
			PkScript: destPkScript[i],
			Value:    330,
		}
		tx.AddTxOut(txOut1)
		weightEstimate.AddTxOutput(txOut1)
		totalOutputSats += 330
		transferEdicts = append(transferEdicts, runestone.Edict{
			ID:     *runeId,
			Output: uint32(len(tx.TxOut) - 1),
			Amount: user.AssetAmt.ToUint128(),
		})
	}

	if len(transferEdicts) > 0 {
		nullDataScript, err := EncipherRunePayload(transferEdicts)
		if err != nil {
			Log.Errorf("too many edicts, %d, %v", len(transferEdicts), err)
			return nil, nil, 0, err
		}
		weightEstimate.AddOutput(nullDataScript)

		txOut2 := &wire.TxOut{
			PkScript: nullDataScript,
			Value:    int64(0),
		}
		tx.AddTxOut(txOut2)
	}

	// 输出白聪
	if requiredValue != 0 {
		for i, user := range dest {
			if user.Value >= 330 {
				txOut3 := &wire.TxOut{
					PkScript: destPkScript[i],
					Value:    user.Value,
				}
				tx.AddTxOut(txOut3)
				weightEstimate.AddTxOutput(txOut3)
				totalOutputSats += user.Value
			} else {
				return nil, nil, 0, fmt.Errorf("output is less than 330 sats")
			}
		}
	}

	// 剩下的都是白聪
	feeValue := total - totalOutputSats
	fee0 := weightEstimate.Fee(feeRate)
	if feeValue < fee0 {
		// 增加fee
		var selected []*TxOutput
		selected, feeValue, err = p.SelectUtxosForFeeV3(feeValue,
			feeRate, &weightEstimate, excludeRecentBlock)
		if err != nil {
			return nil, nil, 0, err
		}
		for _, output := range selected {
			tx.AddTxIn(output.TxIn())
			prevFetcher.AddPrevOut(*output.OutPoint(), &output.OutValue)
		}
	}

	fee0 = weightEstimate.Fee(feeRate)
	if feeValue < fee0 {
		return nil, nil, 0, fmt.Errorf("no enough fee")
	}

	weightEstimate.AddP2TROutput() // fee change
	fee1 := weightEstimate.Fee(feeRate)
	fee := fee0
	feeChange := feeValue - fee1
	if feeChange >= 330 {
		fee = fee1
		txOut3 := &wire.TxOut{
			PkScript: changePkScript,
			Value:    int64(feeChange),
		}
		tx.AddTxOut(txOut3)
	}

	return tx, prevFetcher, fee, nil
}

func (p *Manager) GetOrGenerateStubs(address string, c int,
	excludedUtxoMap map[string]bool, feeRate int64) ([]*TxOutput, error) {

	pkScript, err := AddrToPkScript(address, GetChainParam())
	if err != nil {
		return nil, err
	}
	stubUtxos, err := p.GetUtxosForStubs(address, c, excludedUtxoMap)
	if err != nil {
		// try to generate new stub utxos
		tx, _, err := p.GenerateStubUtxosV2(c, excludedUtxoMap, feeRate)
		if err != nil {
			return nil, err
		}
		txId := tx.TxID()
		for i := range c {
			stubUtxos = append(stubUtxos, fmt.Sprintf("%s:%d", txId, i))
		}
	}
	
	stubs := make([]*TxOutput, 0, c)
	for _, utxo := range stubUtxos {
		info, err := p.l1IndexerClient.GetTxOutput(utxo)
		if err != nil {
			// 可能刚广播，直接构造
			info = indexer.NewTxOutput(330)
			info.OutPointStr = utxo
			info.OutValue.PkScript = pkScript
		}
		stubs = append(stubs, info)
	}
	return stubs, nil
}