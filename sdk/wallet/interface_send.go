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

	tx, prevFetcher, err := p.buildBatchSendTx_SatsNet(destAddr, name, destAmt, utxos, fees, memo)
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
func (p *Manager) buildBatchSendTx_SatsNet(destAddr []string,
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

	if !input.Zero() {
		// 最后一个提供输入的utxo获得change
		// changePkScript, err := GetP2TRpkScript(p.wallet.GetPaymentPubKey())
		// if err != nil {
		// 	return nil, nil, err
		// }
		txOut1 := swire.NewTxOut(input.Value(), input.OutValue.Assets, changePkScript)
		tx.AddTxOut(txOut1)
	}
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

	tx, prevFetcher, err := p.buildBatchSendTxV2_SatsNet(dest, name, utxos, fees, memo)
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
func (p *Manager) buildBatchSendTxV2_SatsNet(dest []*SendAssetInfo,
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

	if !input.Zero() {
		// 最后一个提供输入的utxo获得change
		// changePkScript, err := GetP2TRpkScript(p.wallet.GetPaymentPubKey())
		// if err != nil {
		// 	return nil, nil, err
		// }
		txOut1 := swire.NewTxOut(input.Value(), input.OutValue.Assets, changePkScript)
		tx.AddTxOut(txOut1)
	}
	if len(memo) > 0 {
		txOut2 := swire.NewTxOut(0, nil, memo)
		tx.AddTxOut(txOut2)
	}

	return tx, prevFetcher, nil
	//return nil, nil, fmt.Errorf("not implemented")
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
				return "", fmt.Errorf("not enough fee")
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

	if !input.Zero() {
		changePkScript, err := GetP2TRpkScript(p.wallet.GetPaymentPubKey())
		if err != nil {
			return "", err
		}
		txOut2 := swire.NewTxOut(input.Value(), input.OutValue.Assets, changePkScript)
		tx.AddTxOut(txOut2)
	}

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
	assetName string, amt string, memo []byte) (string, error) {

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
	pkScript, err := stxscript.PayToAddrScript(addr)
	if err != nil {
		return "", err
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
				return "", fmt.Errorf("not enough fee")
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
		return "", err
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

	if !input.Zero() {
		changePkScript, err := GetP2TRpkScript(p.wallet.GetPaymentPubKey())
		if err != nil {
			return "", err
		}
		txOut2 := swire.NewTxOut(input.Value(), input.OutValue.Assets, changePkScript)
		tx.AddTxOut(txOut2)
	}

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

	PrintJsonTx_SatsNet(tx, "SendAssets_SatsNet")

	txid, err := p.BroadcastTx_SatsNet(tx)
	if err != nil {
		Log.Errorf("BroadCastTx_SatsNet failed. %v", err)
		return "", err
	}

	return txid, nil
}

// 发送资产到一个地址上
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
		return "", fmt.Errorf("not enough fee")
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
				return "", fmt.Errorf("not enough fee")
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

	if !input.Zero() {
		changePkScript, err := GetP2TRpkScript(p.wallet.GetPaymentPubKey())
		if err != nil {
			return "", err
		}
		txOut2 := swire.NewTxOut(input.Value(), input.OutValue.Assets, changePkScript)
		tx.AddTxOut(txOut2)
	}

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

// 同时发送资产和聪
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
				return "", fmt.Errorf("not enough fee")
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

	if !input.Zero() {
		changePkScript, err := GetP2TRpkScript(p.wallet.GetPaymentPubKey())
		if err != nil {
			return "", err
		}
		txOut2 := swire.NewTxOut(input.Value(), input.OutValue.Assets, changePkScript)
		tx.AddTxOut(txOut2)
	}

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

func (p *Manager) batchSendPlainSats(destAddr string, value int64, n int,
	feeRate int64, memo []byte) (string, int64, error) {
	//
	return p.BatchSendAssets(destAddr, indexer.ASSET_PLAIN_SAT.String(),
		fmt.Sprintf("%d", value), n, feeRate, memo)
}

// 发送资产到一个地址上，拆分n个输出
func (p *Manager) BatchSendAssets(destAddr string, assetName string,
	amt string, n int, feeRate int64, memo []byte) (string, int64, error) {

	if p.wallet == nil {
		return "", 0, fmt.Errorf("wallet is not created/unlocked")
	}
	name := ParseAssetString(assetName)
	if name == nil {
		return "", 0, fmt.Errorf("invalid asset name %s", assetName)
	}
	tickerInfo := p.getTickerInfo(name)
	if tickerInfo == nil {
		return "", 0, fmt.Errorf("can't get ticker %s info", assetName)
	}
	dAmt, err := indexer.NewDecimalFromString(amt, tickerInfo.Divisibility)
	if err != nil {
		return "", 0, err
	}
	if dAmt.Sign() <= 0 {
		return "", 0, fmt.Errorf("invalid amt")
	}
	if !IsValidNullData(memo) {
		return "", 0, fmt.Errorf("invalid length of null data %d", len(memo))
	}
	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	var tx *wire.MsgTx
	var prevFetcher *txscript.MultiPrevOutFetcher
	var fee int64

	if indexer.IsPlainAsset(name) {
		tx, prevFetcher, fee, err = p.buildBatchSendTx_PlainSats(destAddr, dAmt.Int64(), n, feeRate, memo)
	} else if name.Protocol == indexer.PROTOCOL_NAME_ORDX {
		newName := GetAssetName(tickerInfo)
		tx, prevFetcher, fee, err = p.buildBatchSendTx_Ordx(destAddr, newName, dAmt, n, feeRate, memo)
	} else {
		return "", 0, fmt.Errorf("unsupport batch send for asset name %s", assetName)
	}
	if err != nil {
		Log.Errorf("buildBatchSendTx failed. %v", err)
		return "", 0, err
	}

	// sign
	tx, err = p.SignTx(tx, prevFetcher)
	if err != nil {
		Log.Errorf("SignTx failed. %v", err)
		return "", 0, err
	}

	txid, err := p.BroadcastTx(tx)
	if err != nil {
		Log.Errorf("BroadCastTx failed. %v", err)
		return "", 0, err
	}

	return txid, fee, nil
}

// 白聪
func (p *Manager) buildBatchSendTx_PlainSats(destAddr string, amt int64, n int,
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
	changePkScript, err := GetP2TRpkScript(p.wallet.GetPaymentPubKey())
	if err != nil {
		return nil, nil, 0, err
	}

	var weightEstimate utils.TxWeightEstimator
	for range n {
		weightEstimate.AddP2TROutput() // output
	}
	if len(memo) > 0 {
		weightEstimate.AddOutput(memo[:]) // op_return
	}

	p.utxoLockerL1.Reload(address)
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	total := int64(0)
	required := amt * int64(n)
	remaining := required
	for _, out := range outputs {
		if p.utxoLockerL1.IsLocked(out.OutPoint) {
			continue
		}
		output := OutputInfoToOutput(out)
		outpoint := output.OutPoint()
		txOut := output.OutValue

		txIn := wire.NewTxIn(outpoint, nil, nil)
		tx.AddTxIn(txIn)
		prevFetcher.AddPrevOut(*outpoint, &txOut)
		weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)

		total += out.Value
		remaining -= out.Value
		if required+weightEstimate.Fee(feeRate) <= total {
			break
		}
	}
	if remaining > 0 {
		return nil, nil, 0, fmt.Errorf("not enough plain sats")
	}

	//fee0 := weightEstimate.Fee(feeRate)
	weightEstimate.AddP2TROutput() // fee
	fee1 := weightEstimate.Fee(feeRate)

	feeValue := total - required // >= fee0
	change := feeValue - fee1
	if change < 330 {
		change = 0
	}

	for range n {
		txOut1 := &wire.TxOut{
			PkScript: destPkScript,
			Value:    int64(amt),
		}
		tx.AddTxOut(txOut1)
	}

	if change >= 330 {
		txOut2 := &wire.TxOut{
			PkScript: changePkScript,
			Value:    int64(change),
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

	return tx, prevFetcher, feeValue - change, nil
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
func (p *Manager) buildBatchSendTx_Ordx(destAddr string,
	name *AssetName, amt *Decimal, n int, feeRate int64,
	memo []byte) (*wire.MsgTx, *txscript.MultiPrevOutFetcher, int64, error) {

	address := p.wallet.GetAddress()
	outputs := p.l1IndexerClient.GetUtxoListWithTicker(address, &name.AssetName)
	if len(outputs) == 0 {
		Log.Errorf("no asset %s", name.String())
		return nil, nil, 0, fmt.Errorf("no asset %s", name.String())
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
	changePkScript, err := GetP2TRpkScript(p.wallet.GetPaymentPubKey())
	if err != nil {
		return nil, nil, 0, err
	}

	var weightEstimate utils.TxWeightEstimator
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	total := int64(0)
	var totalAsset *Decimal
	requiredAmt := amt.Clone().MulBigInt(big.NewInt(int64(n)))
	inputVect := make([]*TxOutput, 0)
	p.utxoLockerL1.Reload(address)
	for _, out := range outputs {
		if p.utxoLockerL1.IsLocked(out.OutPoint) {
			continue
		}
		output := OutputInfoToOutput(out)
		if HasMultiAsset(output) {
			continue
		}
		RemoveNFTAsset(output)

		inputVect = append(inputVect, output)
		outpoint := output.OutPoint()
		txOut := output.OutValue

		prevFetcher.AddPrevOut(*outpoint, &txOut)
		weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)

		total += out.Value
		assetAmt := output.GetAsset(&name.AssetName)
		if totalAsset == nil {
			totalAsset = assetAmt.Clone()
		} else {
			totalAsset = totalAsset.Add(assetAmt)
		}

		if totalAsset.Cmp(requiredAmt) >= 0 {
			break
		}
	}
	if totalAsset.Cmp(requiredAmt) < 0 {
		return nil, nil, 0, fmt.Errorf("not enough asset %s", name.String())
	}

	// 重新排序下，将有空白聪的utxo尽可能放在第一个或者最后一个
	var prefix, suffix int64
	inputVect, prefix, suffix = adjustInputsForSplicingIn(inputVect, name)
	allInput := indexer.NewTxOutput(0)
	for _, output := range inputVect {
		tx.AddTxIn(output.TxIn())
		allInput.Append(output)
	}

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

	fee0 := weightEstimate.Fee(feeRate)
	if feeValue < fee0 {
		// 增加fee
		feeOutputs := p.l1IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
		if len(feeOutputs) == 0 {
			Log.Errorf("no plain sats")
			return nil, nil, 0, fmt.Errorf("no plain sats")
		}

		for _, out := range feeOutputs {
			if p.utxoLockerL1.IsLocked(out.OutPoint) {
				continue
			}
			output := OutputInfoToOutput(out)
			outpoint := output.OutPoint()
			txOut := output.OutValue

			feeValue += out.Value
			txIn := wire.NewTxIn(outpoint, nil, nil)
			tx.AddTxIn(txIn)
			prevFetcher.AddPrevOut(*outpoint, &txOut)
			weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
			if feeValue >= weightEstimate.Fee(feeRate) {
				break
			}
		}
	}

	fee0 = weightEstimate.Fee(feeRate)
	weightEstimate.AddP2TROutput() // fee change
	fee1 := weightEstimate.Fee(feeRate)

	if feeValue < fee0 {
		return nil, nil, 0, fmt.Errorf("not enough fee")
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
	amt string, feeRate int64, memo []byte) (string, error) {

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
	if !IsValidNullData(memo) {
		return "", fmt.Errorf("invalid length of null data %d", len(memo))
	}
	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	if indexer.IsPlainAsset(name) {
		return p.sendPlainSats(destAddr, dAmt.Int64(), feeRate, memo)
	} else if name.Protocol == indexer.PROTOCOL_NAME_ORDX {
		newName := GetAssetName(tickerInfo)
		return p.sendOrdxs(destAddr, newName, dAmt, feeRate, memo)
	} else if name.Protocol == indexer.PROTOCOL_NAME_RUNES {
		return p.sendRunes(destAddr, name, dAmt, feeRate, memo)
	}

	return "", fmt.Errorf("invalid asset name %s", assetName)
}

// 白聪
func (p *Manager) sendPlainSats(destAddr string, amt int64, feeRate int64, memo []byte) (string, error) {
	tx, prevFetcher, _, err := p.buildBatchSendTx_PlainSats(destAddr, amt, 1, feeRate, memo)
	if err != nil {
		return "", err
	}

	// sign
	tx, err = p.SignTx(tx, prevFetcher)
	if err != nil {
		Log.Errorf("SignTx failed. %v", err)
		return "", err
	}

	txid, err := p.BroadcastTx(tx)
	if err != nil {
		Log.Errorf("BroadCastTx failed. %v", err)
		return "", err
	}
	return txid, nil
}

func (p *Manager) sendOrdxs(destAddr string,
	name *AssetName, amt *Decimal, feeRate int64, memo []byte) (string, error) {
	txid, _, err := p.BatchSendAssets(destAddr, name.String(), amt.String(), 1, feeRate, memo)
	if err != nil {
		return "", err
	}

	return txid, nil
}

func (p *Manager) sendRunes(destAddr string,
	name *indexer.AssetName, amt *Decimal, feeRate int64, memo []byte) (string, error) {

	address := p.wallet.GetAddress()
	outputs := p.l1IndexerClient.GetUtxoListWithTicker(address, name)
	if len(outputs) == 0 {
		Log.Errorf("no asset %s", name.String())
		return "", fmt.Errorf("no asset %s", name.String())
	}

	tx := wire.NewMsgTx(wire.TxVersion)

	addr, err := btcutil.DecodeAddress(destAddr, GetChainParam())
	if err != nil {
		return "", err
	}
	destPkScript, err := txscript.PayToAddrScript(addr)
	if err != nil {
		return "", err
	}
	changePkScript, err := GetP2TRpkScript(p.wallet.GetPaymentPubKey())
	if err != nil {
		return "", err
	}
	expectedAmt := amt.Clone()

	p.utxoLockerL1.Reload(address)
	var weightEstimate utils.TxWeightEstimator
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	total := int64(0)
	var totalAmt *Decimal
	remaining := expectedAmt.Clone()
	for _, out := range outputs {
		if p.utxoLockerL1.IsLocked(out.OutPoint) {
			continue
		}
		output := OutputInfoToOutput(out)
		outpoint := output.OutPoint()
		txOut := output.OutValue

		txIn := wire.NewTxIn(outpoint, nil, nil)
		tx.AddTxIn(txIn)
		prevFetcher.AddPrevOut(*outpoint, &txOut)
		weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)

		total += out.Value
		assetAmt := output.GetAsset(name)
		if totalAmt == nil {
			totalAmt = assetAmt.Clone()
		} else {
			totalAmt = totalAmt.Add(assetAmt.Clone())
		}
		if remaining.Cmp(assetAmt) > 0 {
			remaining = remaining.Sub(assetAmt)
		} else {
			remaining.SetValue(0)
			break
		}
	}
	if remaining.Sign() > 0 {
		return "", fmt.Errorf("not enough asset %s", name.String())
	}

	// 默认输出到第一个非零utxo中
	// 远端的资产使用edict明确表示输出数量
	transferEdicts := make([]runestone.Edict, 0)
	if totalAmt.Cmp(amt) > 0 {
		// runes: 增加transfer edict
		runeId, err := p.getRuneIdFromName(name)
		if err != nil {
			return "", err
		}
		transferEdicts = append(transferEdicts, runestone.Edict{
			ID:     *runeId,
			Output: uint32(1),
			Amount: indexer.DecimalSub(totalAmt, amt).ToUint128(),
		})

		if len(memo) > 0 {
			return "", fmt.Errorf("mainnet can't support multi op_return")
		}
	}
	if len(memo) > 0 {
		weightEstimate.AddOutput(memo[:]) // op_return
	}

	changeOutput := total - 330

	txOut0 := &wire.TxOut{
		PkScript: destPkScript,
		Value:    int64(330),
	}
	tx.AddTxOut(txOut0)
	weightEstimate.AddTxOutput(txOut0)

	if len(transferEdicts) > 0 {
		txOut1 := &wire.TxOut{
			PkScript: changePkScript,
			Value:    int64(330),
		}
		tx.AddTxOut(txOut1)
		weightEstimate.AddTxOutput(txOut1)
		changeOutput -= 330

		nullDataScript, err := EncipherRunePayload(transferEdicts)
		if err != nil {
			Log.Errorf("too many edicts, %d, %v", len(transferEdicts), err)
			return "", err
		}
		// 增加费率
		weightEstimate.AddOutput(nullDataScript)

		txOut2 := &wire.TxOut{
			PkScript: nullDataScript,
			Value:    int64(0),
		}
		tx.AddTxOut(txOut2)
	}

	fee0 := weightEstimate.Fee(feeRate)
	feeValue := changeOutput
	if feeValue < fee0 {
		feeOutputs := p.l1IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
		if len(feeOutputs) == 0 {
			Log.Errorf("no plain sats")
			return "", fmt.Errorf("no plain sats")
		}

		for _, out := range feeOutputs {
			if p.utxoLockerL1.IsLocked(out.OutPoint) {
				continue
			}
			output := OutputInfoToOutput(out)
			outpoint := output.OutPoint()
			txOut := output.OutValue

			feeValue += out.Value
			txIn := wire.NewTxIn(outpoint, nil, nil)
			tx.AddTxIn(txIn)
			prevFetcher.AddPrevOut(*outpoint, &txOut)
			weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
			if feeValue >= weightEstimate.Fee(feeRate) {
				break
			}
		}
	}
	fee0 = weightEstimate.Fee(feeRate)
	weightEstimate.AddP2TROutput() // fee change
	fee1 := weightEstimate.Fee(feeRate)

	if feeValue < fee0 {
		return "", fmt.Errorf("not enough fee")
	}

	feeChange := feeValue - fee1
	if feeChange >= 330 {
		txOut3 := &wire.TxOut{
			PkScript: changePkScript,
			Value:    int64(feeChange),
		}
		tx.AddTxOut(txOut3)
	}
	if len(memo) > 0 {
		txOut4 := &wire.TxOut{
			PkScript: memo,
			Value:    0,
		}
		tx.AddTxOut(txOut4)
	}

	// sign
	tx, err = p.SignTx(tx, prevFetcher)
	if err != nil {
		Log.Errorf("SignTx failed. %v", err)
		return "", err
	}

	txid, err := p.BroadcastTx(tx)
	if err != nil {
		Log.Errorf("BroadCastTx failed. %v", err)
		return "", err
	}

	return txid, nil
}

// 给多个地址发送不同数量的白聪
func (p *Manager) buildBatchSendTxV2_PlainSats(dest []*SendAssetInfo, utxos, fees []string,
	feeRate int64, memo []byte, bInChannel bool) (*wire.MsgTx, *txscript.MultiPrevOutFetcher, int64, error) {
	if p.wallet == nil {
		return nil, nil, 0, fmt.Errorf("wallet is not created/unlocked")
	}
	address := p.wallet.GetAddress()

	tx := wire.NewMsgTx(wire.TxVersion)

	var requiredValue int64
	var destPkScript [][]byte
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

		destPkScript = append(destPkScript, pkScript)
		requiredValue += d.Value
	}

	var weightEstimate utils.TxWeightEstimator
	for range len(dest) {
		weightEstimate.AddP2TROutput() // output
	}
	if len(memo) > 0 {
		weightEstimate.AddOutput(memo[:]) // op_return
	}

	p.utxoLockerL1.Reload(address)
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	total := int64(0)
	var changePkScript []byte
	utxos = append(utxos, fees...)
	for i, utxo := range utxos {
		if p.utxoLockerL1.IsLocked(utxo) {
			continue
		}
		txOut, err := p.l1IndexerClient.GetTxOutput(utxo)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("GetTxOutput %s failed, %v", utxo, err)
		}

		outpoint := txOut.OutPoint()
		out := txOut.OutValue

		txIn := wire.NewTxIn(outpoint, nil, nil)
		tx.AddTxIn(txIn)
		prevFetcher.AddPrevOut(*outpoint, &out)
		if bInChannel {
			weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
		} else {
			weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		}

		total += out.Value
		if requiredValue+weightEstimate.Fee(feeRate) <= total {
			changePkScript = txOut.OutValue.PkScript
			break
		}

		if i+1 == len(utxos) {
			changePkScript = txOut.OutValue.PkScript
		}
	}

	totalOutSats := int64(0)
	for i, user := range dest {
		txOut1 := &wire.TxOut{
			PkScript: destPkScript[i],
			Value:    user.Value,
		}
		tx.AddTxOut(txOut1)
		totalOutSats += user.Value
	}

	fee0 := weightEstimate.Fee(feeRate)
	changeOutput := total - totalOutSats - fee0
	if changeOutput < 0 {
		return nil, nil, 0, fmt.Errorf("no enough plain sats, required %d but only %d", totalOutSats+fee0, total)
	}

	weightEstimate.AddP2TROutput() // fee
	fee1 := weightEstimate.Fee(feeRate)

	fee := fee0
	changeOutput = total - totalOutSats - fee1
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

// 给不同地址发送不同数量的资产，只支持ordx协议
func (p *Manager) buildBatchSendTxV2_ordx(dest []*SendAssetInfo,
	assetName *AssetName, utxos, fees []string,
	feeRate int64, memo []byte, bInChannel bool) (
	*wire.MsgTx, *txscript.MultiPrevOutFetcher, int64, error) {

	address := p.wallet.GetAddress()

	tx := wire.NewMsgTx(wire.TxVersion)

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
		if d.AssetAmt.Int64()%int64(assetName.N) != 0 {
			return nil, nil, 0, fmt.Errorf("the asset amt should be times of %d", assetName.N)
		}
		if indexer.GetBindingSatNum(d.AssetAmt, uint32(assetName.N)) < 330 {
			return nil, nil, 0, fmt.Errorf("the asset amt should at lease 330 times of %d", assetName.N)
		}
		requiredValue += d.Value
	}

	var weightEstimate utils.TxWeightEstimator
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	total := int64(0)
	var totalAsset *Decimal

	inputVect := make([]*TxOutput, 0)
	p.utxoLockerL1.Reload(address)
	var changePkScript []byte
	utxos = append(utxos, fees...)
	for i, utxo := range utxos {
		if p.utxoLockerL1.IsLocked(utxo) {
			continue
		}
		txOut, err := p.l1IndexerClient.GetTxOutput(utxo)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("GetTxOutput %s failed, %v", utxo, err)
		}
		if HasMultiAsset(txOut) {
			continue
		}
		RemoveNFTAsset(txOut)

		inputVect = append(inputVect, txOut)
		outpoint := txOut.OutPoint()
		out := txOut.OutValue

		prevFetcher.AddPrevOut(*outpoint, &out)
		if bInChannel {
			weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
		} else {
			weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		}

		total += out.Value
		assetAmt := txOut.GetAsset(&assetName.AssetName)
		totalAsset = totalAsset.Add(assetAmt)

		if i+1 == len(utxos) {
			changePkScript = txOut.OutValue.PkScript
		}
	}

	// 重新排序下，将有空白聪的utxo尽可能放在第一个或者最后一个
	var prefix int64
	inputVect, prefix, _ = adjustInputsForSplicingIn(inputVect, assetName)
	allInput := indexer.NewTxOutput(0)
	for _, output := range inputVect {
		tx.AddTxIn(output.TxIn())
		allInput.Append(output)
	}

	var err error
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
			return nil, nil, 0, fmt.Errorf("output is less than 330 sats")
		}
	}

	if len(memo) > 0 {
		weightEstimate.AddOutput(memo[:]) // op_return
	}

	// 资产余额
	if totalAsset.Cmp(requiredAmt) > 0 {
		assetChange := indexer.DecimalSub(totalAsset, requiredAmt)

		var output *TxOutput
		output, remainingOutput, err = remainingOutput.Split(&assetName.AssetName, 0, assetChange)
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

	// 输出白聪
	if requiredValue != 0 {
		for i, user := range dest {
			if user.Value >= 330 {
				var output *TxOutput
				output, remainingOutput, err = remainingOutput.Split(&indexer.ASSET_PLAIN_SAT,
					0, indexer.NewDefaultDecimal(user.Value))
				if err != nil {
					return nil, nil, 0, err
				}
				txOut3 := &wire.TxOut{
					PkScript: destPkScript[i],
					Value:    output.Value(),
				}
				tx.AddTxOut(txOut3)
				weightEstimate.AddTxOutput(txOut3)
				totalOutputSats += output.Value()
			} else {
				return nil, nil, 0, fmt.Errorf("output is less than 330 sats")
			}
		}
	}

	// 剩下余额
	feeValue := total - totalOutputSats
	fee0 := weightEstimate.Fee(feeRate)
	if feeValue < fee0 {
		return nil, nil, 0, fmt.Errorf("not enough fee")
	}

	weightEstimate.AddP2TROutput() // fee change
	fee1 := weightEstimate.Fee(feeRate)
	fee := fee0

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
func (p *Manager) buildBatchSendTxV2_runes(dest []*SendAssetInfo,
	assetName *AssetName, utxos, fees []string,
	feeRate int64, bInChannel bool) (
	*wire.MsgTx, *txscript.MultiPrevOutFetcher, int64, error) {

	address := p.wallet.GetAddress()

	tx := wire.NewMsgTx(wire.TxVersion)

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
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	total := int64(0)
	var totalAsset *Decimal

	inputVect := make([]*TxOutput, 0)
	p.utxoLockerL1.Reload(address)
	var changePkScript []byte
	utxos = append(utxos, fees...)
	for i, utxo := range utxos {
		if p.utxoLockerL1.IsLocked(utxo) {
			continue
		}
		txOut, err := p.l1IndexerClient.GetTxOutput(utxo)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("GetTxOutput %s failed, %v", utxo, err)
		}
		if HasMultiAsset(txOut) {
			continue
		}
		RemoveNFTAsset(txOut)

		inputVect = append(inputVect, txOut)
		outpoint := txOut.OutPoint()
		out := txOut.OutValue

		prevFetcher.AddPrevOut(*outpoint, &out)
		if bInChannel {
			weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
		} else {
			weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		}

		total += out.Value
		assetAmt := txOut.GetAsset(&assetName.AssetName)
		totalAsset = totalAsset.Add(assetAmt)

		if i+1 == len(utxos) {
			changePkScript = txOut.OutValue.PkScript
		}
	}

	allInput := indexer.NewTxOutput(0)
	for _, output := range inputVect {
		tx.AddTxIn(output.TxIn())
		allInput.Append(output)
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
		return nil, nil, 0, fmt.Errorf("not enough fee")
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

// 构建tx，发送给多个地址不同数量的资产，对于ordx资产，不允许插桩
func (p *Manager) BatchSendAssetsV2(dest []*SendAssetInfo,
	assetNameStr string, feeRate int64, memo []byte, bInChannel bool) (string, error) {

	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	name := ParseAssetString(assetNameStr)
	if name == nil {
		return "", fmt.Errorf("invalid asset name %s", assetNameStr)
	}
	tickerInfo := p.getTickerInfo(name)
	if tickerInfo == nil {
		return "", fmt.Errorf("can't get ticker %s info", assetNameStr)
	}
	assetName := GetAssetName(tickerInfo)
	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}
	if len(memo) > txscript.MaxDataCarrierSize {
		return "", fmt.Errorf("too large data %d in op_return", len(memo))
	}

	var totalValue int64
	var totalAmt *Decimal
	for _, item := range dest {
		totalAmt = totalAmt.Add(item.AssetAmt)
		totalValue += item.Value
	}
	totalValue += CalcFee_SendTx(len(dest), len(dest), len(dest), assetName,
		totalAmt, feeRate, bInChannel)

	var err error
	var utxos, fees []string
	if IsPlainAsset(assetName) {
		expected := totalValue + totalAmt.Int64()
		utxos, err = p.GetUtxosForFee("", expected)
		if err != nil {
			return "", err
		}
	} else {
		utxos, fees, err = p.getUtxosWithAssetV2("", totalValue, totalAmt, name)
		if err != nil {
			return "", err
		}
	}

	var tx *wire.MsgTx
	var prevFetcher *txscript.MultiPrevOutFetcher
	var fee int64
	if indexer.IsPlainAsset(name) {
		tx, prevFetcher, fee, err = p.buildBatchSendTxV2_PlainSats(dest,
			utxos, fees, feeRate, memo, bInChannel)
	} else if name.Protocol == indexer.PROTOCOL_NAME_ORDX {
		tx, prevFetcher, fee, err = p.buildBatchSendTxV2_ordx(dest, assetName,
			utxos, fees, feeRate, memo, bInChannel)
	} else if name.Protocol == indexer.PROTOCOL_NAME_RUNES {
		if len(memo) != 0 {
			return "", fmt.Errorf("do not attach memo when send runes asset")
		}
		tx, prevFetcher, fee, err = p.buildBatchSendTxV2_runes(dest, assetName,
			utxos, fees, feeRate, bInChannel)
	}
	if err != nil {
		return "", err
	}

	// sign
	tx, err = p.SignTx(tx, prevFetcher)
	if err != nil {
		Log.Errorf("SignTx_SatsNet failed. %v", err)
		return "", err
	}
	PrintJsonTx(tx, "BatchSendAssetsV3")

	txid, err := p.BroadcastTx(tx)
	if err != nil {
		Log.Errorf("BatchSendAssetsV3 failed. %v", err)
		return "", err
	}
	Log.Infof("BatchSendAssetsV3 succeed. %s %d", txid, fee)

	return txid, nil
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

	if NeedStubUtxoForAssetV3(assetName, amt) {
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
func (p *Manager) rebuildTxOutput(tx *wire.MsgTx) ([]*TxOutput, []*TxOutput, error) {
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

		tickerInfo := p.getTickerInfoFromRuneId(edict.ID.String())
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
