package wallet

import (
	"fmt"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	indexer "github.com/sat20-labs/indexer/common"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

func CreatePaymentTx(payment *PaymentReservation) error {
	channel := payment.Channel
	if payment.NeedSendLockTx {
		var err error
		var satsNum int64
		var tx *wire.MsgTx
		var prevFetcher txscript.PrevOutputFetcher
		if payment.IsUnlock {
			tx, prevFetcher, err = CreateUnlockTx(payment.OldChannel, payment.DestAddr,
				payment.AssetName, payment.DestAmt, payment.Utxos, payment.Fees, payment.Memo)
			if err != nil {
				return fmt.Errorf("CreateUnlockTx failed. %v", err)
			}
			for _, amt := range payment.DestAmt {
				satsNum += GetBindingSatNum(amt, payment.AssetName.N)
			}
		} else {
			tx, prevFetcher, err = CreateLockTx(payment.OldChannel, payment.AssetName,
				payment.Amt, payment.Utxos, payment.Fees, payment.Memo)
			if err != nil {
				Log.Errorf("CreateLockTx failed. %v", err)
				return err
			}
			satsNum = GetBindingSatNum(payment.Amt, payment.AssetName.N)
		}

		payment.PaymentPreFetcher = prevFetcher
		payment.PaymentTx = tx
		channel.LastPaymentTxId = tx.TxID()
		channel.UpdateUtxosByPendingTx_SatsNet(payment.PaymentTx)
		if channel.IsInitiator {
			if payment.IsUnlock {
				channel.TotalSatSent += satsNum
			} else {
				channel.TotalSatReceived += satsNum
			}
		} else {
			if payment.IsUnlock {
				channel.TotalSatReceived += satsNum
			} else {
				channel.TotalSatSent += satsNum
			}
		}
	} else {
		for _, utxo := range payment.Utxos {
			channel.AddUtxo_SatsNet(utxo)
		}
	}

	return nil
}

func UpdateCommitWithUnlock(channel *Channel, isInitiator bool,
	assetName *AssetName, amt *indexer.Decimal, hasFeeUtxo bool, requiredFee int64) error {
	localValue := channel.GetCommitLocalValue(assetName)
	remoteValue := channel.GetCommitRemoteValue(assetName)
	fee := indexer.NewDefaultDecimal(requiredFee)

	var localCommitBalance, remoteCommitBalance *indexer.Decimal
	if hasFeeUtxo {
		if channel.IsInitiator {
			localCommitBalance = indexer.DecimalSub(localValue, amt)
			remoteCommitBalance = indexer.DecimalAdd(remoteValue, amt)
		} else {
			localCommitBalance = indexer.DecimalAdd(localValue, amt)
			remoteCommitBalance = indexer.DecimalSub(remoteValue, amt)
		}
	} else {
		if IsPlainAsset(assetName) {
			if channel.IsInitiator {
				localCommitBalance = indexer.DecimalSub(localValue, amt).Sub(fee)
				remoteCommitBalance = indexer.DecimalAdd(remoteValue, amt).Add(fee)
			} else {
				localCommitBalance = indexer.DecimalAdd(localValue, amt).Add(fee)
				remoteCommitBalance = indexer.DecimalSub(remoteValue, amt).Sub(fee)
			}
		} else {
			localPlainValue := channel.GetCommitLocalValue(&PLAIN_ASSET)
			remotePlainValue := channel.GetCommitRemoteValue(&PLAIN_ASSET)

			if channel.IsInitiator {
				localPlainValue = localPlainValue.Sub(fee)
				remotePlainValue = remotePlainValue.Add(fee)
				localCommitBalance = indexer.DecimalSub(localValue, amt)
				remoteCommitBalance = indexer.DecimalAdd(remoteValue, amt)
			} else {
				localPlainValue = localPlainValue.Add(fee)
				remotePlainValue = remotePlainValue.Sub(fee)
				localCommitBalance = indexer.DecimalAdd(localValue, amt)
				remoteCommitBalance = indexer.DecimalSub(remoteValue, amt)
			}

			channel.SetCommitLocalValue(&PLAIN_ASSET, localPlainValue)
			channel.SetCommitRemoteValue(&PLAIN_ASSET, remotePlainValue)
		}
	}

	channel.SetCommitLocalValue(assetName, localCommitBalance)
	channel.SetCommitRemoteValue(assetName, remoteCommitBalance)
	return nil
}

func UpdateCommitWithLock(channel *Channel, isInitiator bool,
	assetName *AssetName, amt *indexer.Decimal,
	hasFeeUtxo, needSendLockTx bool, requiredFee int64) error {
	localValue := channel.GetCommitLocalValue(assetName)
	remoteValue := channel.GetCommitRemoteValue(assetName)
	fee := indexer.NewDefaultDecimal(requiredFee)

	var localCommitBalance, remoteCommitBalance *indexer.Decimal
	if channel.IsInitiator {
		localCommitBalance = indexer.DecimalAdd(localValue, amt)
		remoteCommitBalance = indexer.DecimalSub(remoteValue, amt)
	} else {
		localCommitBalance = indexer.DecimalSub(localValue, amt)
		remoteCommitBalance = indexer.DecimalAdd(remoteValue, amt)
	}

	if !IsPlainAsset(assetName) && !hasFeeUtxo && needSendLockTx {
		localPlainValue := channel.GetCommitLocalValue(&PLAIN_ASSET)
		remotePlainValue := channel.GetCommitRemoteValue(&PLAIN_ASSET)

		if channel.IsInitiator {
			localPlainValue = localPlainValue.Sub(fee)
			remotePlainValue = remotePlainValue.Add(fee)
		} else {
			localPlainValue = localPlainValue.Add(fee)
			remotePlainValue = remotePlainValue.Sub(fee)
		}

		channel.SetCommitLocalValue(&PLAIN_ASSET, localPlainValue)
		channel.SetCommitRemoteValue(&PLAIN_ASSET, remotePlainValue)
	}

	channel.SetCommitLocalValue(assetName, localCommitBalance)
	channel.SetCommitRemoteValue(assetName, remoteCommitBalance)
	return nil
}

func UpdateCommitWithPaymentResv(payment *PaymentReservation) error {
	channel := payment.Channel
	localValue := channel.GetCommitLocalValue(payment.AssetName)
	remoteValue := channel.GetCommitRemoteValue(payment.AssetName)
	fee := indexer.NewDefaultDecimal(DEFAULT_FEE_SATSNET)
	if payment.IsUnlock {
		var alteredAmt *Decimal
		for _, amt := range payment.DestAmt {
			if alteredAmt == nil {
				alteredAmt = amt.Clone()
			} else {
				alteredAmt = alteredAmt.Add(amt)
			}
		}
		if len(payment.Fees) > 0 {
			if channel.IsInitiator {
				payment.LocalCommitBalance = indexer.DecimalSub(localValue, alteredAmt)
				payment.RemoteCommitBalance = indexer.DecimalAdd(remoteValue, alteredAmt)
			} else {
				payment.LocalCommitBalance = indexer.DecimalAdd(localValue, alteredAmt)
				payment.RemoteCommitBalance = indexer.DecimalSub(remoteValue, alteredAmt)
			}
		} else {
			if IsPlainAsset(payment.AssetName) {
				if channel.IsInitiator {
					payment.LocalCommitBalance = indexer.DecimalSub(localValue, alteredAmt).Sub(fee)
					payment.RemoteCommitBalance = indexer.DecimalAdd(remoteValue, alteredAmt).Add(fee)
				} else {
					payment.LocalCommitBalance = indexer.DecimalAdd(localValue, alteredAmt).Add(fee)
					payment.RemoteCommitBalance = indexer.DecimalSub(remoteValue, alteredAmt).Sub(fee)
				}
			} else {
				localPlainValue := channel.GetCommitLocalValue(&PLAIN_ASSET)
				remotePlainValue := channel.GetCommitRemoteValue(&PLAIN_ASSET)

				if channel.IsInitiator {
					localPlainValue = localPlainValue.Sub(fee)
					remotePlainValue = remotePlainValue.Add(fee)
					payment.LocalCommitBalance = indexer.DecimalSub(localValue, alteredAmt)
					payment.RemoteCommitBalance = indexer.DecimalAdd(remoteValue, alteredAmt)
				} else {
					localPlainValue = localPlainValue.Add(fee)
					remotePlainValue = remotePlainValue.Sub(fee)
					payment.LocalCommitBalance = indexer.DecimalAdd(localValue, alteredAmt)
					payment.RemoteCommitBalance = indexer.DecimalSub(remoteValue, alteredAmt)
				}

				payment.Channel.SetCommitLocalValue(&PLAIN_ASSET, localPlainValue)
				payment.Channel.SetCommitRemoteValue(&PLAIN_ASSET, remotePlainValue)
			}
		}
	} else {
		if channel.IsInitiator {
			payment.LocalCommitBalance = indexer.DecimalAdd(localValue, payment.Amt)
			payment.RemoteCommitBalance = indexer.DecimalSub(remoteValue, payment.Amt)
		} else {
			payment.LocalCommitBalance = indexer.DecimalSub(localValue, payment.Amt)
			payment.RemoteCommitBalance = indexer.DecimalAdd(remoteValue, payment.Amt)
		}

		if !IsPlainAsset(payment.AssetName) && len(payment.Fees) == 0 && payment.NeedSendLockTx {
			localPlainValue := channel.GetCommitLocalValue(&PLAIN_ASSET)
			remotePlainValue := channel.GetCommitRemoteValue(&PLAIN_ASSET)

			if channel.IsInitiator {
				localPlainValue = localPlainValue.Sub(fee)
				remotePlainValue = remotePlainValue.Add(fee)
			} else {
				localPlainValue = localPlainValue.Add(fee)
				remotePlainValue = remotePlainValue.Sub(fee)
			}

			payment.Channel.SetCommitLocalValue(&PLAIN_ASSET, localPlainValue)
			payment.Channel.SetCommitRemoteValue(&PLAIN_ASSET, remotePlainValue)
		}
	}
	payment.Channel.SetCommitLocalValue(payment.AssetName, payment.LocalCommitBalance)
	payment.Channel.SetCommitRemoteValue(payment.AssetName, payment.RemoteCommitBalance)

	return nil
}

func CreateUnlockTx(channel *Channel, destAddr []string, assetName *AssetName, amtVect []*Decimal, utxos, fees []*TxOutput_SatsNet,
	memo []byte) (*wire.MsgTx, txscript.PrevOutputFetcher, error) {
	fee := CalcFee_SatsNet()

	var pubkey *secp256k1.PublicKey
	if channel.IsInitiator {
		pubkey = channel.LocalChanCfg.PaymentKey
	} else {
		pubkey = channel.RemoteChanCfg.PaymentKey
	}

	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	var txIn1 []*wire.TxIn
	var inputValue TxOutput_SatsNet
	for _, u := range utxos {
		txIn1 = append(txIn1, u.TxIn())
		prevFetcher.AddPrevOut(*u.OutPoint(), &u.OutValue)
		inputValue.Merge(u)
	}

	var unlockAssets []*wire.AssetInfo
	for _, amt := range amtVect {
		unlockAsset := wire.AssetInfo{
			Name:       assetName.AssetName,
			Amount:     *amt.Clone(),
			BindingSat: uint32(assetName.N),
		}
		if err := inputValue.SubAsset(&unlockAsset); err != nil {
			return nil, nil, err
		}
		unlockAssets = append(unlockAssets, &unlockAsset)
	}

	feeAsset := wire.AssetInfo{
		Name:       ASSET_PLAIN_SAT,
		Amount:     *indexer.NewDefaultDecimal(fee),
		BindingSat: 1,
	}
	if len(fees) == 0 {
		if err := inputValue.SubAsset(&feeAsset); err != nil {
			return nil, nil, err
		}
	}

	var destPkScript [][]byte
	if len(destAddr) == 0 {
		pkScript, err := GetP2TRpkScript(pubkey)
		if err != nil {
			return nil, nil, fmt.Errorf("GetP2TRpkScript failed. %v", err)
		}
		destPkScript = append(destPkScript, pkScript)
	} else {
		for _, str := range destAddr {
			var pkScript []byte
			var err error
			if str == "" {
				pkScript, err = GetP2TRpkScript(pubkey)
				if err != nil {
					return nil, nil, fmt.Errorf("GetP2TRpkScript failed. %v", err)
				}
			} else {
				pkScript, err = GetPkScriptFromAddress(str)
				if err != nil {
					return nil, nil, fmt.Errorf("GetPkScriptFromAddress %s failed. %v", str, err)
				}
			}
			destPkScript = append(destPkScript, pkScript)
		}
	}

	_, mulpkScript, err := GetP2WSHscriptFromChannel(channel)
	if err != nil {
		Log.Errorf("GetP2WSHScriptFromChannel failed. %v", err)
		return nil, nil, err
	}
	var changePkScript []byte
	if channel.IsInitiator {
		changePkScript = channel.GetLocalPkScript()
	} else {
		changePkScript = channel.GetRemotePkScript()
	}

	var txIn2 []*wire.TxIn
	var txOut3 *wire.TxOut
	var feeOutput TxOutput_SatsNet
	if len(fees) != 0 {
		txIn2 = make([]*wire.TxIn, 0)
		for _, info := range fees {
			txIn2 = append(txIn2, info.TxIn())
			prevFetcher.AddPrevOut(*info.OutPoint(), &info.OutValue)
			feeOutput.Merge(info)
		}

		if err := feeOutput.SubAsset(&feeAsset); err != nil {
			return nil, nil, err
		}

		if !feeOutput.Zero() {
			txOut3 = wire.NewTxOut(feeOutput.Value(), GenTxAssetsFromAssets(feeOutput.OutValue.Assets), changePkScript)
		}
	}

	txOut1 := wire.NewTxOut(inputValue.Value(), GenTxAssetsFromAssets(inputValue.OutValue.Assets), mulpkScript)

	var txOut2 []*wire.TxOut
	for i, pkScript := range destPkScript {
		value2 := indexer.GetBindingSatNum(&unlockAssets[i].Amount, unlockAssets[i].BindingSat)
		txOut := wire.NewTxOut(value2, GenTxAssetsFromAssetInfo(unlockAssets[i]), pkScript)
		txOut2 = append(txOut2, txOut)
	}

	nullDataScript2, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_CHANNELID,
		[]byte(fmt.Sprintf("%s-%d", channel.ChannelId, channel.CommitHeight)))
	if err != nil {
		return nil, nil, fmt.Errorf("sindexer.NullDataScript failed. %v", err)
	}
	txOut4 := wire.NewTxOut(0, nil, nullDataScript2)

	var txOut5 *wire.TxOut
	if len(memo) > 0 {
		txOut5 = wire.NewTxOut(0, nil, memo)
	}

	tx := wire.NewMsgTx(wire.TxVersion)
	for _, txIn := range txIn1 {
		tx.AddTxIn(txIn)
	}
	for _, in := range txIn2 {
		tx.AddTxIn(in)
	}

	tx.AddTxOut(txOut1)
	for _, out := range txOut2 {
		tx.AddTxOut(out)
	}
	if txOut3 != nil {
		tx.AddTxOut(txOut3)
	}
	tx.AddTxOut(txOut4)
	if txOut5 != nil {
		tx.AddTxOut(txOut5)
	}

	PrintJsonTx_SatsNet(tx, "unlock")
	return tx, prevFetcher, nil
}

func CreateLockTx(channel *Channel, assetName *AssetName, amt *Decimal, lockUtxos, fees []*TxOutput_SatsNet,
	memo []byte) (*wire.MsgTx, txscript.PrevOutputFetcher, error) {
	fee := CalcFee_SatsNet()
	var pkScriptForP2TR []byte
	if channel.IsInitiator {
		pkScriptForP2TR = channel.GetLocalPkScript()
	} else {
		pkScriptForP2TR = channel.GetRemotePkScript()
	}

	lockAsset := wire.AssetInfo{
		Name:       assetName.AssetName,
		Amount:     *amt.Clone(),
		BindingSat: uint32(assetName.N),
	}
	var inputValue TxOutput_SatsNet
	if err := inputValue.AddAsset(&lockAsset); err != nil {
		return nil, nil, err
	}

	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	var txIn2 []*wire.TxIn
	var changeOutput TxOutput_SatsNet
	for _, info := range lockUtxos {
		txIn2 = append(txIn2, info.TxIn())
		prevFetcher.AddPrevOut(*info.OutPoint(), &info.OutValue)
		changeOutput.Merge(info)
	}
	txIn3 := make([]*wire.TxIn, 0)
	for _, info := range fees {
		txIn3 = append(txIn3, info.TxIn())
		prevFetcher.AddPrevOut(*info.OutPoint(), &info.OutValue)
		changeOutput.Merge(info)
	}

	if err := changeOutput.SubAsset(&lockAsset); err != nil {
		return nil, nil, err
	}
	feeAsset := wire.AssetInfo{
		Name:       ASSET_PLAIN_SAT,
		Amount:     *indexer.NewDefaultDecimal(fee),
		BindingSat: 1,
	}
	if err := changeOutput.SubAsset(&feeAsset); err != nil {
		return nil, nil, err
	}

	_, mulpkScript, err := GetP2WSHscriptFromChannel(channel)
	if err != nil {
		Log.Errorf("GetP2WSHScriptFromChannel faile. %v", err)
		return nil, nil, err
	}
	txOut1 := wire.NewTxOut(inputValue.Value(), GenTxAssetsFromAssets(inputValue.OutValue.Assets), mulpkScript)

	var txOut2 *wire.TxOut
	if !changeOutput.Zero() {
		txOut2 = wire.NewTxOut(changeOutput.Value(), GenTxAssetsFromAssets(changeOutput.OutValue.Assets), pkScriptForP2TR)
	}

	nullDataScript2, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_CHANNELID,
		[]byte(fmt.Sprintf("%s-%d", channel.ChannelId, channel.CommitHeight)))
	if err != nil {
		return nil, nil, fmt.Errorf("sindexer.NullDataScript failed. %v", err)
	}
	txOut4 := wire.NewTxOut(0, nil, nullDataScript2)

	var txOut5 *wire.TxOut
	if len(memo) > 0 {
		txOut5 = wire.NewTxOut(0, nil, memo)
	}

	tx := wire.NewMsgTx(wire.TxVersion)
	for _, in := range txIn2 {
		tx.AddTxIn(in)
	}
	for _, in := range txIn3 {
		tx.AddTxIn(in)
	}

	tx.AddTxOut(txOut1)
	if txOut2 != nil {
		tx.AddTxOut(txOut2)
	}
	tx.AddTxOut(txOut4)
	if txOut5 != nil {
		tx.AddTxOut(txOut5)
	}

	PrintJsonTx_SatsNet(tx, "lock")
	return tx, prevFetcher, nil
}
