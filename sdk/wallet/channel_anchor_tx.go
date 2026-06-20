package wallet

import (
	"bytes"
	"fmt"

	bwire "github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

func CreateOpeningAnchorTx(channel *Channel, fundingUtxo *TxOutput,
	localValue, remoteValue int64, invoiceSig []byte, daoPkScript []byte) *wire.MsgTx {

	redeemScript, pkScript, err := GetP2WSHscriptFromChannel(channel)
	if err != nil {
		Log.Errorf("GetP2WSHScriptFromChannel failed, %v", err)
		return nil
	}
	anchorScript, err := sindexer.StandardAnchorScriptWithSig(fundingUtxo.OutPointStr, redeemScript,
		fundingUtxo.Value(), fundingUtxo.Assets, invoiceSig)
	if err != nil {
		Log.Errorf("StandardAnchorScript failed, %v", err)
		return nil
	}
	if _, _, err = CheckAnchorPkScript(anchorScript); err != nil {
		Log.Errorf("CheckAnchorPkScript failed, %v", err)
		return nil
	}

	if !channel.IsInitiator {
		localValue, remoteValue = remoteValue, localValue
	}

	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(&wire.TxIn{
		PreviousOutPoint: *wire.NewOutPoint(&chainhash.Hash{}, wire.AnchorTxOutIndex),
		Sequence:         wire.AnchorTxOutIndex,
		SignatureScript:  anchorScript,
	})

	if localValue > 0 {
		tx.AddTxOut(&wire.TxOut{
			PkScript: pkScript,
			Value:    localValue,
		})
	}
	if remoteValue > 0 {
		tx.AddTxOut(&wire.TxOut{
			PkScript: daoPkScript,
			Value:    remoteValue,
		})
	}

	assetInfo := PLAIN_ASSET.String() + "-21000000000000000-0-1"
	nullDataScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_ASCENDING, []byte(assetInfo))
	if err != nil {
		Log.Errorf("sindexer.NullDataScript failed. %v", err)
		return nil
	}
	tx.AddTxOut(wire.NewTxOut(0, nil, nullDataScript))

	PrintJsonTx_SatsNet(tx, "anchor")
	return tx
}

func CreateClosingDeAnchorTx(channel *Channel, closeTxId string, daoPkScript []byte) (*wire.MsgTx, txscript.PrevOutputFetcher, error) {
	value1 := channel.GetAllValue_SatsNet()
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)

	var txIn1 []*wire.TxIn
	for _, u := range channel.GetAllOutput_SatsNet() {
		txIn1 = append(txIn1, u.TxIn())
		prevFetcher.AddPrevOut(*u.OutPoint(), &u.OutValue)
	}

	payload, err := sindexer.EncodeDescendPayloadV2(closeTxId, sindexer.DESCEND_OP_CLOSE, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("sindexer.EncodeDescendPayloadV2 failed. %v", err)
	}
	nullDataScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_DESCENDING, payload)
	if err != nil {
		return nil, nil, fmt.Errorf("sindexer.NullDataScript failed. %v", err)
	}
	txOut1 := wire.NewTxOut(value1.Value(), GenTxAssetsFromAssets(value1.OutValue.Assets), nullDataScript)

	nullDataScript2, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_CHANNELID,
		[]byte(fmt.Sprintf("%s-%d", channel.ChannelId, channel.CommitHeight)))
	if err != nil {
		return nil, nil, fmt.Errorf("sindexer.NullDataScript failed. %v", err)
	}
	txOut2 := wire.NewTxOut(0, nil, nullDataScript2)

	tx := wire.NewMsgTx(wire.TxVersion)
	for _, txIn := range txIn1 {
		tx.AddTxIn(txIn)
	}
	tx.AddTxOut(txOut1)
	tx.AddTxOut(txOut2)

	PrintJsonTx_SatsNet(tx, "closing deAnchor")
	return tx, prevFetcher, nil
}

func GetP2WSHscriptFromChannel(channel *Channel) ([]byte, []byte, error) {
	return GetP2WSHscript(channel.LocalChanCfg.PaymentKey.SerializeCompressed(),
		channel.RemoteChanCfg.PaymentKey.SerializeCompressed())
}

func getReturnedChannelVouts(tx *bwire.MsgTx, channelAddr string) ([]uint32, error) {
	pkScript, err := GetPkScriptFromAddress(channelAddr)
	if err != nil {
		return nil, err
	}

	result := make([]uint32, 0)
	for i, txOut := range tx.TxOut {
		if bytes.Equal(txOut.PkScript, pkScript) {
			result = append(result, uint32(i))
		}
	}
	return result, nil
}

func CreateAnchorTx(splicingOutput *indexer.TxOutput, assetName *AssetName, tickerInfo *indexer.TickerInfo,
	redeemScript, invoiceSig []byte, memo []byte) (*wire.MsgTx, error) {
	var localAsset *wire.AssetInfo
	if len(splicingOutput.Assets) > 0 && !indexer.IsPlainAsset(&assetName.AssetName) {
		amt, _ := splicingOutput.GetAssetV2(&assetName.AssetName)
		if !amt.IsZero() {
			localAsset = &wire.AssetInfo{
				Name:       assetName.AssetName,
				Amount:     *amt,
				BindingSat: uint32(tickerInfo.N),
			}
		} else {
			Log.Errorf("can't find expected asset %s in utxo %s", assetName.String(), splicingOutput.OutPointStr)
			return nil, fmt.Errorf("can't find expected asset %s in utxo %s", assetName.String(), splicingOutput.OutPointStr)
		}
	}

	value := int64(0)
	if IsPlainAsset(assetName) {
		value = splicingOutput.Value()
	} else if IsBindingSat(assetName) {
		value = indexer.GetBindingSatNum(&localAsset.Amount, uint32(assetName.N))
	}

	pkScript, err := utils.WitnessScriptHash(redeemScript)
	if err != nil {
		return nil, err
	}
	address, err := GetP2WSHaddressFromScript(pkScript)
	if err != nil {
		return nil, err
	}

	anchorScript, err := sindexer.StandardAnchorScriptWithSig(splicingOutput.OutPointStr,
		redeemScript, value, splicingOutput.Assets, invoiceSig)
	if err != nil {
		Log.Errorf("StandardAnchorScript failed, %v", err)
		return nil, err
	}
	if _, _, err = CheckAnchorPkScript(anchorScript); err != nil {
		Log.Errorf("CheckAnchorPkScript failed, %v", err)
		return nil, err
	}

	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(&wire.TxIn{
		PreviousOutPoint: *wire.NewOutPoint(&chainhash.Hash{}, wire.AnchorTxOutIndex),
		Sequence:         wire.AnchorTxOutIndex,
		SignatureScript:  anchorScript,
	})

	tx.AddTxOut(&wire.TxOut{
		PkScript: pkScript,
		Value:    value,
	})
	if localAsset != nil {
		tx.TxOut[0].Assets = wire.TxAssets{*localAsset}
	}

	assetInfo := fmt.Sprintf("%s-%s-%d-%d", tickerInfo.AssetName.String(),
		tickerInfo.MaxSupply, tickerInfo.Divisibility, tickerInfo.N)
	nullDataScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_ASCENDING, []byte(assetInfo))
	if err != nil {
		return nil, err
	}
	tx.AddTxOut(wire.NewTxOut(0, nil, nullDataScript))

	nullDataScript2, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_CHANNELID, []byte(address))
	if err != nil {
		return nil, err
	}
	tx.AddTxOut(wire.NewTxOut(0, nil, nullDataScript2))

	if len(memo) > 0 {
		tx.AddTxOut(wire.NewTxOut(0, nil, memo))
	}

	PrintJsonTx_SatsNet(tx, "single anchor")
	return tx, nil
}
