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

	utxos, fees, err := p.getUtxosWithAssetV2_SatsNet("", DEFAULT_FEE_SATSNET, totalAmt, name)
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
	utxos, fees, err = p.getUtxosWithAssetV2_SatsNet("", totalValue, totalAmt, name)
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
			if input.OutValue.Value - bindingSat > 0 {
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
	value := int64(0)
	var assetAmt *Decimal
	usedUtxos := make(map[string]bool)
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
		return nil, fmt.Errorf("not enough asset %s", assetName)
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

		feeValue += out.GetPlainSat()
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

// 同时发送资产时，同时给服务地址发送服务费用
func (p *Manager) SendAssetsV2_SatsNet(destAddr string,
	assetName string, amt string, serviceAddr string, serviceFee int64, memo []byte) (string, error) {

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

	Log.Infof("SendAssets_SatsNet %s %s", assetName, amt)
	tx := swire.NewMsgTx(swire.TxVersion)

	addr, err := sbtcutil.DecodeAddress(destAddr, GetChainParam_SatsNet())
	if err != nil {
		return "", err
	}
	assetPkScript, err := stxscript.PayToAddrScript(addr)
	if err != nil {
		return "", err
	}

	addr2, err := sbtcutil.DecodeAddress(serviceAddr, GetChainParam_SatsNet())
	if err != nil {
		return "", err
	}
	servicePkScript, err := stxscript.PayToAddrScript(addr2)
	if err != nil {
		return "", err
	}

	satsNum := serviceFee + DEFAULT_FEE_SATSNET
	fee := indexer.NewDefaultDecimal(satsNum)
	expectedAmt := dAmt.Clone()
	if indexer.IsPlainAsset(name) {
		expectedAmt = expectedAmt.Add(fee)
	}

	p.utxoLockerL2.Reload(address)
	prevFetcher := stxscript.NewMultiPrevOutFetcher(nil)
	var input TxOutput_SatsNet
	value := int64(0)
	var assetAmt *Decimal
	usedUtxos := make(map[string]bool)
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
	outValue := GetBindingSatNum(dAmt, tickerInfo.N)
	txOut0 := swire.NewTxOut(outValue, GenTxAssetsFromAssetInfo(&sendAsset), assetPkScript)
	tx.AddTxOut(txOut0)
	err = input.SubAsset(&sendAsset)
	if err != nil {
		return "", err
	}

	if serviceFee != 0 {
		svrfee := swire.AssetInfo{
			Name:       ASSET_PLAIN_SAT,
			Amount:     *indexer.NewDefaultDecimal(serviceFee),
			BindingSat: 1,
		}
		txOut1 := swire.NewTxOut(serviceFee, nil, servicePkScript)
		tx.AddTxOut(txOut1)
		err = input.SubAsset(&svrfee)
		if err != nil {
			return "", err
		}
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

	Log.Infof("SendAssets_SatsNet %s %s", assetName, amt)
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

func (p *Manager) BatchSendPlainSats(destAddr string, value int64, n int,
	feeRate int64, memo []byte) (string, int64, error) {
	//
	tx, fee, err :=  p.BatchSendAssets(destAddr, indexer.ASSET_PLAIN_SAT.String(),
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

	if indexer.IsPlainAsset(name) {
		tx, prevFetcher, fee, err = p.BuildBatchSendTx_PlainSats(destAddr, dAmt.Int64(), n, feeRate, memo)
	} else if name.Protocol == indexer.PROTOCOL_NAME_ORDX {
		newName := GetAssetName(tickerInfo)
		tx, prevFetcher, fee, err = p.BuildBatchSendTx_Ordx(destAddr, newName, dAmt, n, feeRate, memo)
	} else {
		newName := GetAssetName(tickerInfo)
		tx, prevFetcher, fee, err = p.BuildBatchSendTx_Runes(destAddr, newName, dAmt, n, feeRate, memo)
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

	_, err = p.BroadcastTx(tx)
	if err != nil {
		Log.Errorf("BroadCastTx failed. %v", err)
		return nil, 0, err
	}

	return tx, fee, nil
}

// 白聪
func (p *Manager) BuildBatchSendTx_PlainSats(destAddr string, amt int64, n int,
	feeRate int64, memo []byte) (*wire.MsgTx, *txscript.MultiPrevOutFetcher, int64, error) {

	address := p.wallet.GetAddress()
	outputs := p.l1IndexerClient.GetUtxoListWithTicker(address, &ASSET_PLAIN_SAT)
	if len(outputs) == 0 {
		Log.Errorf("no plain sats")
		return nil, nil, 0, fmt.Errorf("no plain sats")
	}
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
	prevFetcher, changePkScript, changeOutput, fee0, err := p.selectUtxosForPlainSats(
		required, feeRate, false, tx, &weightEstimate)
	if err != nil {
		return nil, nil, 0, err
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

func adjustInputsForSplicingIn(inputs []*TxOutput, name *AssetName) ([]*TxOutput, int64, int64) {
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
func (p *Manager) BuildBatchSendTx_Ordx(destAddr string,
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
	selected, totalAsset, total, err := p.selectUtxosForAsset(
		name, requiredAmt, &weightEstimate, false)
	if err != nil {
		return nil, nil, 0, err
	}
	var prefix, suffix int64
	selected, prefix, suffix = adjustInputsForSplicingIn(selected, name)
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
		selected, feeValue, err = p.selectUtxosForFee(feeValue,
			feeRate, &weightEstimate, false)
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
func (p *Manager) BuildBatchSendTx_Runes(destAddr string,
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
	selected, totalAsset, total, err := p.selectUtxosForAsset(
		name, requiredAmt, &weightEstimate, false)
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
		selected, feeValue, err = p.selectUtxosForFee(feeValue,
			feeRate, &weightEstimate, false)
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

// 该Tx还没有广播或者广播了还没有确认，才有可能重建
func (p *Manager) RebuildTxOutput(tx *wire.MsgTx) ([]*TxOutput, []*TxOutput, error) {
	// 尝试为tx的输出分配资产
	// 按ordx协议的规则
	// 按runes协议的规则
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

	return inputs, outputs, nil
}


// 只用于白聪输出，包括fee
func (p *Manager) selectUtxosForPlainSats(
	requiredValue int64, feeRate int64, excludeRecentBlock bool, 
	tx *wire.MsgTx, weightEstimate *utils.TxWeightEstimator,
	) (*txscript.MultiPrevOutFetcher, []byte, int64, int64, error) {
	/* 规则：
	1. 先根据目标输出的value，先选1个，或者最多5个utxo，其聪数量不大于value
	2. 再从其余的聪数量大于330聪的utxo中，凑齐足够的network fee，注意每增加一个输入，其交易的fee就会增加一些
	3. 如果上面的选择方式找不到足够的utxo，就按照老的流程，从最大的开始找。
	*/

	address := p.wallet.GetAddress()
	utxos := p.l1IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
	if len(utxos) == 0 {
		return nil, nil, 0, 0, fmt.Errorf("no plain sats")
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
		utxo := u.OutPoint
		if p.utxoLockerL1.IsLocked(utxo) {
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
		localWeightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		total += u.Value
		if total >= requiredValue {
			break
		}
	}

	// 再选小的utxo作为fee
	for i := len(utxos) - 1; i >= 0; i-- {
		u := utxos[i]
		utxo := u.OutPoint
		if p.utxoLockerL1.IsLocked(utxo) {
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
		localWeightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
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
		return prevFetcher, changePkScript, changeOutput, fee0, nil
	}

	// 上面所选的utxo不够，换成老的方案：
	prevFetcher = txscript.NewMultiPrevOutFetcher(nil)
	total = 0
	for _, u := range utxos {
		utxo := u.OutPoint
		if p.utxoLockerL1.IsLocked(utxo) {
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
		weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		total += out.Value
		if requiredValue+weightEstimate.Fee(feeRate) <= total {
			break
		}
	}

	fee0 = weightEstimate.Fee(feeRate)
	changeOutput = total - requiredValue - fee0
	if changeOutput < 0 {
		return nil, nil, 0, 0, fmt.Errorf("no enough plain sats, required %d but only %d",
			requiredValue+fee0, total)
	}
	
	return prevFetcher, changePkScript, changeOutput, fee0, nil
}

func (p *Manager) selectUtxosForAsset(assetName *AssetName, requiredAmt *Decimal,
	weightEstimate *utils.TxWeightEstimator, excludeRecentBlock bool) ([]*TxOutput, *Decimal, int64, error) {

	address := p.wallet.GetAddress()
	utxos := p.l1IndexerClient.GetUtxoListWithTicker(address, &assetName.AssetName)
	if len(utxos) == 0 {
		return nil, nil, 0, fmt.Errorf("no enough assets")
	}
	p.utxoLockerL1.Reload(address)

	localWeightEstimate := *weightEstimate
	// 先选满足条件的utxo
	total := int64(0)
	var totalAsset *Decimal
	selected := make([]*TxOutput, 0)
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
		assetAmt := txOut.GetAsset(&assetName.AssetName)
		if assetAmt.Cmp(requiredAmt) > 0 {
			continue
		}
		RemoveNFTAsset(txOut)

		selected = append(selected, txOut)
		localWeightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
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
		weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		total += txOut.OutValue.Value
		assetAmt := txOut.GetAsset(&assetName.AssetName)
		totalAsset = totalAsset.Add(assetAmt)
		if totalAsset.Cmp(requiredAmt) >= 0 {
			break
		}
	}

	if totalAsset.Cmp(requiredAmt) < 0 {
		return nil, nil, 0, fmt.Errorf("no enough assets")
	}
	return selected, totalAsset, total, nil
}

// 选择合适大小的utxo，而不是从最大的utxo选择
func (p *Manager) selectUtxosForFee(
	feeValue int64, feeRate int64,
	weightEstimate *utils.TxWeightEstimator,
	excludeRecentBlock bool) ([]*TxOutput, int64, error) {
	
	address := p.wallet.GetAddress()
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
		localWeightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
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
		weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		if feeValue >= weightEstimate.Fee(feeRate) {
			break
		}
	}

	if feeValue < weightEstimate.Fee(feeRate) {
		return nil, 0, fmt.Errorf("no enough fee")
	}

	return selected, feeValue, nil
}
