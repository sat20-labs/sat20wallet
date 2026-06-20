package wallet

import (
	"bytes"
	"fmt"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/runes/runestone"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func CreateSplicingInAnchorTx(resv *SplicingReservation, toDAOPkScript []byte,
	tickerInfo *indexer.TickerInfo, memo []byte) *swire.MsgTx {
	channel := resv.Channel
	splicingOutput := resv.SplicingOutput
	assetName := resv.AssetName
	localValue := resv.SplicingOutput.OutValue.Value

	var localAsset *swire.AssetInfo
	if len(splicingOutput.Assets) > 0 && !indexer.IsPlainAsset(&assetName.AssetName) {
		if !resv.SplicingAmt.IsZero() {
			localAsset = &swire.AssetInfo{
				Name:       assetName.AssetName,
				Amount:     *resv.SplicingAmt.Clone(),
				BindingSat: uint32(tickerInfo.N),
			}
		} else {
			Log.Errorf("Invalid splicingAmt %s", resv.SplicingAmt.String())
			return nil
		}
	}

	redeemScript, pkScript, err := GetP2WSHscriptFromChannel(channel)
	if err != nil {
		Log.Errorf("GetP2WSHScriptFromChannel failed, %v", err)
		return nil
	}

	value := int64(0)
	if IsPlainAsset(assetName) {
		value = splicingOutput.Value()
	} else if IsBindingSat(assetName) {
		value = indexer.GetBindingSatNum(&localAsset.Amount, uint32(assetName.N))
		localValue = value
	} else {
		localValue = 0
	}
	anchorScript, err := sindexer.StandardAnchorScriptWithSig(splicingOutput.OutPointStr,
		redeemScript, value, splicingOutput.Assets, resv.InvoiceSig)
	if err != nil {
		Log.Errorf("StandardAnchorScript failed, %v", err)
		return nil
	}
	if _, _, err = CheckAnchorPkScript(anchorScript); err != nil {
		Log.Errorf("CheckAnchorPkScript failed, %v", err)
		return nil
	}

	tx := swire.NewMsgTx(swire.TxVersion)
	tx.AddTxIn(&swire.TxIn{
		PreviousOutPoint: *swire.NewOutPoint(&chainhash.Hash{}, swire.AnchorTxOutIndex),
		Sequence:         swire.AnchorTxOutIndex,
		SignatureScript:  anchorScript,
	})

	txOut0 := &swire.TxOut{PkScript: pkScript, Value: localValue}
	if localAsset != nil {
		txOut0.Assets = swire.TxAssets{*localAsset}
	}
	tx.AddTxOut(txOut0)

	assetInfo := fmt.Sprintf("%s-%s-%d-%d", tickerInfo.AssetName.String(),
		tickerInfo.MaxSupply, tickerInfo.Divisibility, tickerInfo.N)
	nullDataScript1, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_ASCENDING, []byte(assetInfo))
	if err != nil {
		Log.Errorf("sindexer.NullDataScript failed. %v", err)
		return nil
	}
	tx.AddTxOut(swire.NewTxOut(0, nil, nullDataScript1))

	nullDataScript2, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_CHANNELID,
		[]byte(fmt.Sprintf("%s-%d", channel.ChannelId, channel.CommitHeight)))
	if err != nil {
		Log.Errorf("sindexer.NullDataScript failed. %v", err)
		return nil
	}
	tx.AddTxOut(swire.NewTxOut(0, nil, nullDataScript2))

	if len(memo) > 0 {
		tx.AddTxOut(swire.NewTxOut(0, nil, memo))
	}

	PrintJsonTx_SatsNet(tx, "splicing-in anchor")
	return tx
}

func CreateSplicingOutDeAnchorTx(resv *SplicingReservation) (*swire.MsgTx, stxscript.PrevOutputFetcher, error) {
	prevFetcher := stxscript.NewMultiPrevOutFetcher(nil)
	channel := resv.Channel

	var err error
	var inputs []*TxOutput_SatsNet
	requireAmt := resv.SplicingAmt.Clone()
	if len(resv.Fees) == 0 {
		plainSats := indexer.NewDefaultDecimal(resv.RequiredFee)
		if IsPlainAsset(resv.AssetName) {
			requireAmt = requireAmt.Add(plainSats)
			inputs, err = channel.GetChannelUtxosWithAsset_SatsNet(requireAmt, resv.AssetName)
			if err != nil {
				return nil, nil, err
			}
		} else {
			inputs, err = channel.GetChannelUtxosWithAsset_SatsNet(resv.SplicingAmt, resv.AssetName)
			if err != nil {
				return nil, nil, err
			}
			inputs2, err := channel.GetChannelUtxosWithAsset_SatsNet(plainSats, &PLAIN_ASSET)
			if err != nil {
				return nil, nil, err
			}
			inputs = append(inputs, inputs2...)
		}
	} else {
		inputs, err = channel.GetChannelUtxosWithAsset_SatsNet(resv.SplicingAmt, resv.AssetName)
		if err != nil {
			return nil, nil, err
		}
	}

	var localValue *TxOutput_SatsNet
	var txIn1 []*swire.TxIn
	for _, u := range inputs {
		txIn1 = append(txIn1, u.TxIn())
		prevFetcher.AddPrevOut(*u.OutPoint(), &u.OutValue)
		if localValue == nil {
			localValue = u.Clone()
		} else {
			localValue.Merge(u)
		}
	}

	descentOutput := &TxOutput_SatsNet{
		OutValue: swire.TxOut{
			Value: indexer.GetBindingSatNum(resv.SplicingAmt, uint32(resv.AssetName.N)),
		},
	}
	if !IsPlainAsset(resv.AssetName) {
		descentOutput.OutValue.Assets = swire.TxAssets{{
			Name:       resv.AssetName.AssetName,
			Amount:     *resv.SplicingAmt.Clone(),
			BindingSat: uint32(resv.AssetName.N),
		}}
	}

	err = localValue.Subtract(descentOutput)
	if err != nil {
		return nil, nil, err
	}

	if len(resv.Fees) == 0 {
		localValue.OutValue.Value -= resv.RequiredFee
		if localValue.OutValue.Value < 0 {
			return nil, nil, fmt.Errorf("invalid local value")
		}
		descentOutput.OutValue.Value += resv.RequiredFee
	}

	txOut1 := swire.NewTxOut(localValue.OutValue.Value, GenTxAssetsFromAssets(localValue.OutValue.Assets), localValue.OutValue.PkScript)

	returnedVouts, err := getReturnedChannelVouts(resv.SplicingTx, channel.ChannelId)
	if err != nil {
		return nil, nil, err
	}
	payload, err := sindexer.EncodeDescendPayloadV2(resv.SplicingTx.TxID(), sindexer.DESCEND_OP_SPLICING_OUT, returnedVouts)
	if err != nil {
		return nil, nil, fmt.Errorf("sindexer.EncodeDescendPayloadV2 failed. %v", err)
	}
	nullDataScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_DESCENDING, payload)
	if err != nil {
		return nil, nil, fmt.Errorf("sindexer.NullDataScript failed. %v", err)
	}
	txOut2 := swire.NewTxOut(descentOutput.Value(), GenTxAssetsFromAssets(descentOutput.OutValue.Assets), nullDataScript)

	nullDataScript2, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_CHANNELID,
		[]byte(fmt.Sprintf("%s-%d", channel.ChannelId, channel.CommitHeight)))
	if err != nil {
		return nil, nil, fmt.Errorf("sindexer.NullDataScript failed. %v", err)
	}
	txOut3 := swire.NewTxOut(0, nil, nullDataScript2)

	tx := swire.NewMsgTx(swire.TxVersion)
	for _, txIn := range txIn1 {
		tx.AddTxIn(txIn)
	}
	tx.AddTxOut(txOut1)
	tx.AddTxOut(txOut2)
	tx.AddTxOut(txOut3)

	PrintJsonTx_SatsNet(tx, "splicing-out deAnchor")
	return tx, prevFetcher, nil
}

func (p *Manager) CreateSplicingInTx(resv *SplicingReservation) (*wire.MsgTx, txscript.PrevOutputFetcher, error) {
	splicingTx := wire.NewMsgTx(wire.TxVersion)
	utxos := resv.SplicingInputs
	fees := resv.Fees

	var err error
	var changePkScript []byte
	if len(utxos) > 0 {
		changePkScript = utxos[0].OutValue.PkScript
	} else if len(fees) > 0 {
		changePkScript = fees[0].OutValue.PkScript
	}
	if changePkScript == nil {
		return nil, nil, fmt.Errorf("invalid change pkScript")
	}
	_, pkScript, err := GetP2WSHscriptFromChannel(resv.Channel)
	if err != nil {
		Log.Errorf("GetP2WSHScript failed. %v", err)
		return nil, nil, err
	}

	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	if resv.StubIsSet {
		if resv.StubUtxo == nil {
			return nil, nil, fmt.Errorf("splicing-in input stub is not set")
		}
		splicingTx.AddTxIn(resv.StubUtxo.TxIn())
		prevFetcher.AddPrevOut(*resv.StubUtxo.OutPoint(), &resv.StubUtxo.OutValue)
	}
	for _, info := range utxos {
		splicingTx.AddTxIn(info.TxIn())
		prevFetcher.AddPrevOut(*info.OutPoint(), &info.OutValue)
	}
	chanpoint := resv.Channel.GetChanPoint()
	splicingTx.AddTxIn(chanpoint.TxIn())
	prevFetcher.AddPrevOut(*chanpoint.OutPoint(), &chanpoint.OutValue)
	for _, info := range fees {
		splicingTx.AddTxIn(info.TxIn())
		prevFetcher.AddPrevOut(*info.OutPoint(), &info.OutValue)
	}
	newInputs := make([]*TxOutput, 0, len(resv.SplicingInputs)+len(resv.Fees)+1)
	if resv.StubIsSet {
		newInputs = append(newInputs, resv.StubUtxo.Clone())
	}
	newInputs = append(newInputs, CloneOutput(resv.SplicingInputs)...)
	newInputs = append(newInputs, resv.Fees...)
	resv.SplicingOutput, resv.SplicingChangeOutput, err = GenTxOutput(newInputs, resv.AssetName, resv.SplicingValue, resv.SplicingAmt)
	if err != nil {
		return nil, nil, err
	}

	txOut0 := wire.NewTxOut(resv.SplicingValue, pkScript)
	splicingTx.AddTxOut(txOut0)
	if resv.SplicingChange >= 330 {
		splicingTx.AddTxOut(wire.NewTxOut(resv.SplicingChange, changePkScript))
	}
	txOut2 := wire.NewTxOut(chanpoint.Value(), pkScript)
	splicingTx.AddTxOut(txOut2)
	chanPointIndex := len(splicingTx.TxOut) - 1
	if c, v := resv.Channel.NeedStubUtxo(resv.AssetName); c > 0 {
		for range c {
			splicingTx.AddTxOut(wire.NewTxOut(v, pkScript))
		}
	}
	if resv.FeeChange >= 330 {
		splicingTx.AddTxOut(wire.NewTxOut(resv.FeeChange, changePkScript))
	}

	hasSplicingChangeAsset := false
	if resv.SplicingChangeOutput != nil {
		change := resv.SplicingChangeOutput.GetAsset(&resv.AssetName.AssetName)
		hasSplicingChangeAsset = change.Sign() != 0
		switch resv.AssetName.Protocol {
		case indexer.PROTOCOL_NAME_BRC20:
			if change.Sign() != 0 {
				return nil, nil, fmt.Errorf("brc20 asset change is unexpected")
			}
		case indexer.PROTOCOL_NAME_RUNES:
			if change.Sign() > 0 {
				id, err := p.getRuneIdFromName(&resv.AssetName.AssetName)
				if err != nil {
					return nil, nil, err
				}
				transferEdicts := []runestone.Edict{{
					ID:     *id,
					Output: uint32(1),
					Amount: change.ToUint128(),
				}}
				nullDataScript, err := EncipherRunePayload(transferEdicts)
				if err != nil {
					return nil, nil, err
				}
				splicingTx.AddTxOut(&wire.TxOut{PkScript: nullDataScript, Value: 0})
			}
		}
	}

	resv.SplicingOutput.OutPointStr = fmt.Sprintf("%s:%d", splicingTx.TxID(), 0)
	resv.SplicingOutput.OutValue.PkScript = splicingTx.TxOut[0].PkScript
	if hasSplicingChangeAsset {
		resv.SplicingChangeOutput.OutPointStr = fmt.Sprintf("%s:%d", splicingTx.TxID(), 1)
		resv.SplicingChangeOutput.OutValue.PkScript = splicingTx.TxOut[1].PkScript
	}
	resv.NewChanPoint = indexer.GenerateTxOutput(splicingTx, chanPointIndex)
	if c, _ := resv.Channel.NeedStubUtxo(resv.AssetName); c > 0 {
		stubs := make([]*TxOutput, 0)
		for i := range c {
			stub := indexer.GenerateTxOutput(splicingTx, chanPointIndex+1+i)
			stubs = append(stubs, stub)
		}
		resv.Channel.SetStubUtxoForAsset(stubs, resv.AssetName)
	}

	PrintJsonTx(splicingTx, "splicing-in")
	return splicingTx, prevFetcher, nil
}

func (p *Manager) CreateSplicingOutTx(resv *SplicingReservation) (*wire.MsgTx, txscript.PrevOutputFetcher, error) {
	splicingTx := wire.NewMsgTx(wire.TxVersion)
	channel := resv.Channel
	pkScript := channel.GetChannelPkScript()

	var remotePkScript, changePkScript []byte
	if channel.IsInitiator {
		remotePkScript, _ = GetP2TRpkScript(channel.RemoteChanCfg.PaymentKey)
	} else {
		remotePkScript, _ = GetP2TRpkScript(channel.LocalChanCfg.PaymentKey)
	}
	if len(resv.Fees) > 0 {
		changePkScript = resv.Fees[0].OutValue.PkScript
	} else {
		return nil, nil, fmt.Errorf("invalid change pkScript")
	}

	destPkScript, err := GetPkScriptFromAddress(resv.DestAddr)
	if err != nil {
		return nil, nil, err
	}

	var allInputs []*indexer.TxOutput
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	if resv.StubIsSet {
		splicingTx.AddTxIn(resv.StubUtxo.TxIn())
		prevFetcher.AddPrevOut(*resv.StubUtxo.OutPoint(), &resv.StubUtxo.OutValue)
		allInputs = append(allInputs, resv.StubUtxo)
	}

	chanpointIsSet := false
	chanpoint := resv.Channel.GetChanPoint()
	for _, info := range resv.SplicingInputs {
		splicingTx.AddTxIn(info.TxIn())
		prevFetcher.AddPrevOut(*info.OutPoint(), &info.OutValue)
		allInputs = append(allInputs, info)
		if info.OutPointStr == chanpoint.OutPointStr {
			chanpointIsSet = true
		}
	}
	resv.SplicingOutput, resv.SplicingChangeOutput, err = GenTxOutput(allInputs, resv.AssetName, resv.SplicingValue, resv.SplicingAmt)
	if err != nil {
		return nil, nil, err
	}
	if !chanpointIsSet {
		splicingTx.AddTxIn(chanpoint.TxIn())
		prevFetcher.AddPrevOut(*chanpoint.OutPoint(), &chanpoint.OutValue)
	}

	for _, info := range resv.Fees {
		splicingTx.AddTxIn(info.TxIn())
		prevFetcher.AddPrevOut(*info.OutPoint(), &info.OutValue)
	}

	splicingTx.AddTxOut(wire.NewTxOut(resv.SplicingValue, destPkScript))

	var chanpointValue int64
	if chanpointIsSet {
		total := resv.SplicingChange + resv.Bonus
		resv.Bonus = 0
		chanpointValue = 330
		if total >= 660 {
			resv.SplicingChange = total - chanpointValue
			resv.SplicingChangeOutput.OutValue.Value = resv.SplicingChange
		} else {
			resv.SplicingChangeOutput = nil
			resv.SplicingChange = 0
			chanpointValue = total
		}
	} else {
		chanpointValue = chanpoint.Value()
	}
	if resv.SplicingChange >= 330 {
		resv.SplicingChangeOutput.OutValue.Value = resv.SplicingChange
		splicingTx.AddTxOut(wire.NewTxOut(resv.SplicingChange, pkScript))
	} else {
		resv.SplicingChangeOutput = nil
	}
	splicingTx.AddTxOut(wire.NewTxOut(chanpointValue, pkScript))
	chanPointIndex := len(splicingTx.TxOut) - 1

	if resv.ServiceFee+resv.Bonus >= 330 {
		splicingTx.AddTxOut(wire.NewTxOut(resv.ServiceFee+resv.Bonus, remotePkScript))
	}
	if resv.FeeChange >= 330 {
		splicingTx.AddTxOut(wire.NewTxOut(resv.FeeChange, changePkScript))
	}

	if resv.SplicingChangeOutput != nil {
		switch resv.AssetName.Protocol {
		case indexer.PROTOCOL_NAME_BRC20:
		case indexer.PROTOCOL_NAME_RUNES:
			id, err := p.getRuneIdFromName(&resv.AssetName.AssetName)
			if err != nil {
				return nil, nil, err
			}
			var transferEdicts []runestone.Edict
			if resv.SplicingChange >= 330 {
				change := resv.SplicingChangeOutput.GetAsset(&resv.AssetName.AssetName)
				if change.Sign() > 0 {
					transferEdicts = append(transferEdicts, runestone.Edict{
						ID:     *id,
						Output: uint32(1),
						Amount: change.ToUint128(),
					})
				}
			}
			nullDataScript, err := EncipherRunePayload(transferEdicts)
			if err != nil {
				return nil, nil, err
			}
			splicingTx.AddTxOut(&wire.TxOut{PkScript: nullDataScript, Value: 0})
		}
	}

	resv.SplicingOutput.OutPointStr = fmt.Sprintf("%s:%d", splicingTx.TxID(), 0)
	resv.SplicingOutput.OutValue.PkScript = splicingTx.TxOut[0].PkScript
	if resv.SplicingChangeOutput != nil {
		resv.SplicingChangeOutput.OutPointStr = fmt.Sprintf("%s:%d", splicingTx.TxID(), 1)
		resv.SplicingChangeOutput.OutValue.PkScript = splicingTx.TxOut[1].PkScript
	}
	resv.NewChanPoint = indexer.GenerateTxOutput(splicingTx, chanPointIndex)

	PrintJsonTx(splicingTx, "splicing-out")
	return splicingTx, prevFetcher, nil
}

func (p *Manager) getRecoveredAscendedOutput(resv *SplicingReservation) (*TxOutput_SatsNet, error) {
	if !resv.RecoverAscended {
		return nil, fmt.Errorf("not recover ascended")
	}
	outpoint := resv.RecoveredAnchorOutpoint
	if outpoint == "" && resv.RecoveredAnchorTxId != "" {
		outpoint = fmt.Sprintf("%s:0", resv.RecoveredAnchorTxId)
		resv.RecoveredAnchorOutpoint = outpoint
	}
	if outpoint == "" {
		return nil, fmt.Errorf("recover ascended anchor output is empty")
	}
	if len(resv.Channel.NotInControl_SatsNet([]string{outpoint})) == 0 {
		return nil, fmt.Errorf("recovered anchor output %s has been in channel", outpoint)
	}

	output, err := p.GetIndexerRPCClient_SatsNet().GetTxOutput(outpoint)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(resv.Channel.GetChannelPkScript(), output.OutValue.PkScript) {
		return nil, fmt.Errorf("recovered anchor output %s is not in channel address", outpoint)
	}
	amt, invalid := output.GetAssetV2(&resv.AssetName.AssetName)
	if invalid {
		return nil, fmt.Errorf("recovered anchor output %s asset %s is invalid", outpoint, resv.AssetName.String())
	}
	if amt == nil || amt.Sign() == 0 {
		return nil, fmt.Errorf("recovered anchor output %s has not specific asset", outpoint)
	}
	if resv.SplicingAmt != nil && amt.Cmp(resv.SplicingAmt) != 0 {
		return nil, fmt.Errorf("recovered anchor amount mismatch %s != %s", amt.String(), resv.SplicingAmt.String())
	}
	return OutputToSatsNet(output), nil
}

func (p *Manager) AddRecoveredAscendedOutput(resv *SplicingReservation) error {
	output, err := p.getRecoveredAscendedOutput(resv)
	if err != nil {
		return err
	}
	resv.Channel.AddUtxo_SatsNet(output)
	return nil
}

func (p *Manager) FunderProcessAcceptSplicingIn(resv *SplicingReservation) error {
	var err error

	if IsPlainAsset(resv.AssetName) {
		if resv.NewCapacity != resv.Channel.Capacity+resv.SplicingValue {
			return fmt.Errorf("new capacity incorrect %d", resv.NewCapacity)
		}
		resv.Channel.SetCapacity(resv.NewCapacity)
	} else if resv.NewCapacity != resv.Channel.Capacity {
		return fmt.Errorf("new capacity incorrect %d", resv.NewCapacity)
	}

	var newLocalBalance, newRemoteBalance *indexer.Decimal
	if resv.IsInitiator {
		newLocalBalance = resv.Channel.GetCommitLocalValue(resv.AssetName).Add(resv.SplicingAmt)
		newRemoteBalance = resv.Channel.GetCommitRemoteValue(resv.AssetName)
	} else {
		newLocalBalance = resv.Channel.GetCommitLocalValue(resv.AssetName)
		newRemoteBalance = resv.Channel.GetCommitRemoteValue(resv.AssetName).Add(resv.SplicingAmt)
	}

	if resv.NewLocalBalance.Cmp(newLocalBalance) != 0 {
		return fmt.Errorf("new local balance incorrect %d", resv.NewLocalBalance)
	}
	resv.Channel.SetCommitLocalValue(resv.AssetName, resv.NewLocalBalance)

	if resv.NewRemoteBalance.Cmp(newRemoteBalance) != 0 {
		return fmt.Errorf("new remote balance incorrect %d", resv.NewRemoteBalance)
	}
	resv.Channel.SetCommitRemoteValue(resv.AssetName, resv.NewRemoteBalance)

	if resv.NeedSendSplicingTx {
		splicingInTx, preFetcher, err := p.CreateSplicingInTx(resv)
		if err != nil {
			Log.Errorf("CreateSplicingInTx failed. %v", err)
			return err
		}
		splicingSigs, err := PartialSignTxWithWallet(resv.LocalWallet(), splicingInTx, preFetcher,
			resv.Channel.RedeemScript, false, resv.Channel.RemoteChanCfg.PaymentKey.SerializeCompressed())
		if err != nil {
			return err
		}
		resv.PreFetcherForSplicing = preFetcher
		resv.SplicingTx = splicingInTx
		resv.LocalSplicingSigInfo.SplicingSig = splicingSigs

		resv.Channel.SetChanPoint(resv.NewChanPoint)
		if err = resv.Channel.AddFundingOutput(resv.SplicingOutput, resv.AssetName); err != nil {
			return err
		}
	} else {
		if len(resv.SplicingInputs) != 1 {
			return fmt.Errorf("only accept one funding utxo each time")
		}
		output := resv.SplicingInputs[0]
		if err := AlignAsset(output, &resv.AssetName.AssetName); err != nil {
			return err
		}
		if c, _ := resv.Channel.NeedStubUtxo(resv.AssetName); c > 0 {
			if len(resv.Fees) != c {
				return fmt.Errorf("should provide %d stub for asset %s", c, resv.AssetName.String())
			}
			resv.Channel.SetStubUtxoForAsset(resv.Fees, resv.AssetName)
		}
		if err = resv.Channel.AddFundingOutput(output, resv.AssetName); err != nil {
			return err
		}
		resv.SplicingOutput = output
	}

	value := int64(0)
	if IsPlainAsset(resv.AssetName) {
		value = resv.SplicingOutput.Value()
	} else if IsBindingSat(resv.AssetName) {
		value = indexer.GetBindingSatNum(resv.Amt, uint32(resv.AssetName.N))
	}
	resv.Invoice, err = sindexer.StandardAnchorScript(resv.SplicingOutput.OutPointStr,
		resv.Channel.RedeemScript, value, resv.SplicingOutput.Assets)
	if err != nil {
		return err
	}
	if !VerifyMessage(resv.Channel.RemoteChanCfg.PaymentKey, resv.Invoice, resv.InvoiceSig) {
		return fmt.Errorf("VerifyMessage failed")
	}

	if resv.RecoverAscended {
		if err = p.AddRecoveredAscendedOutput(resv); err != nil {
			return err
		}
	} else {
		anchorTx := CreateSplicingInAnchorTx(resv, p.GetDAOPkScript(resv.Channel), resv.TickerInfo, resv.Memo)
		if anchorTx == nil {
			return fmt.Errorf("can't generate anchor TX")
		}
		resv.AnchorTx = anchorTx
		localOutput := sindexer.GenerateTxOutput(anchorTx, 0)
		if resv.NeedSendSplicingTx {
			resv.Channel.UpdateUtxosByPendingTx_SatsNet(anchorTx)
		} else {
			resv.Channel.AddUtxo_SatsNet(localOutput)
		}
	}

	if err = p.SignNextCommitment(&resv.RevocationInfo); err != nil {
		Log.Errorf("SignNextCommitment failed. %v", err)
		return err
	}
	resv.LocalRevKey, err = p.GetCurrRevocationKey(resv.Channel)
	if err != nil {
		Log.Errorf("GetCurrRevocationKey failed. %v", err)
		return err
	}
	resv.LocalNextRevKey, err = p.GetNextRevocationKey(resv.Channel)
	if err != nil {
		Log.Errorf("GetNextRevocationKey failed. %v", err)
		return err
	}
	return nil
}

func (p *Manager) FunderProcessSplicingOutAccpted(resv *SplicingReservation) error {
	var err error

	newCapacity := resv.Channel.Capacity
	var newLocalBalance, newRemoteBalance *indexer.Decimal
	if resv.IsInitiator {
		newLocalBalance = resv.Channel.GetCommitLocalValue(resv.AssetName).Sub(resv.SplicingAmt)
		newRemoteBalance = resv.Channel.GetCommitRemoteValue(resv.AssetName)
	} else {
		newLocalBalance = resv.Channel.GetCommitLocalValue(resv.AssetName)
		newRemoteBalance = resv.Channel.GetCommitRemoteValue(resv.AssetName).Sub(resv.SplicingAmt)
	}

	resv.NewLocalPlainBalance = resv.Channel.GetCommitLocalValue(&PLAIN_ASSET).Int64()
	resv.NewRemotePlainBalance = resv.Channel.GetCommitRemoteValue(&PLAIN_ASSET).Int64()
	if len(resv.Fees) == 0 {
		if IsPlainAsset(resv.AssetName) {
			if resv.IsInitiator {
				newLocalBalance = newLocalBalance.Sub(indexer.NewDefaultDecimal(resv.RequiredFee))
				if newLocalBalance.Sign() < 0 {
					return fmt.Errorf("no enough balance, required %d but only %d", resv.RequiredFee, newLocalBalance)
				}
			} else {
				newRemoteBalance = newRemoteBalance.Sub(indexer.NewDefaultDecimal(resv.RequiredFee))
				if newRemoteBalance.Sign() < 0 {
					return fmt.Errorf("no enough balance, required %d but only %d", resv.RequiredFee, newRemoteBalance)
				}
			}
		} else {
			return fmt.Errorf("no fee utxos")
		}
		newCapacity -= resv.RequiredFee
	}

	if IsPlainAsset(resv.AssetName) {
		newCapacity -= resv.SplicingValue
	}
	if resv.NewCapacity != newCapacity {
		return fmt.Errorf("new capacity incorrect %d", resv.NewCapacity)
	}
	resv.Channel.SetCapacity(resv.NewCapacity)

	if newLocalBalance.Cmp(resv.NewLocalBalance) != 0 {
		return fmt.Errorf("new local value incorrect %d %d", newLocalBalance, resv.NewLocalBalance)
	}
	if newRemoteBalance.Cmp(resv.NewRemoteBalance) != 0 {
		return fmt.Errorf("new remote value incorrect %d %d", newRemoteBalance, resv.NewRemoteBalance)
	}

	resv.Channel.SetCommitLocalValue(resv.AssetName, resv.NewLocalBalance)
	resv.Channel.SetCommitRemoteValue(resv.AssetName, resv.NewRemoteBalance)
	if !IsPlainAsset(resv.AssetName) {
		resv.Channel.SetCommitLocalValue(&PLAIN_ASSET, indexer.NewDefaultDecimal(resv.NewLocalPlainBalance))
		resv.Channel.SetCommitRemoteValue(&PLAIN_ASSET, indexer.NewDefaultDecimal(resv.NewRemotePlainBalance))
	}

	splicingOutTx, preFetcher, err := p.CreateSplicingOutTx(resv)
	if err != nil {
		Log.Errorf("CreateSplicingOutTx failed. %v", err)
		return err
	}
	splicingSigs, err := PartialSignTxWithWallet(resv.LocalWallet(), splicingOutTx, preFetcher,
		resv.Channel.RedeemScript, false, resv.Channel.RemoteChanCfg.PaymentKey.SerializeCompressed())
	if err != nil {
		return err
	}
	resv.PreFetcherForSplicing = preFetcher
	resv.SplicingTx = splicingOutTx
	resv.LocalSplicingSigInfo.SplicingSig = splicingSigs

	resv.Channel.RemoveFundingOutput(resv.SplicingInputs, resv.AssetName)
	resv.Channel.SetChanPoint(resv.NewChanPoint)
	if resv.SplicingChangeOutput != nil {
		resv.Channel.AddFundingOutput(resv.SplicingChangeOutput, resv.AssetName)
	}

	deanchorTx, prevFetcher2, err := CreateSplicingOutDeAnchorTx(resv)
	if err != nil {
		Log.Errorf("CreateSplicingOutDeAnchorTx failed. %v", err)
		return err
	}
	deanchorSigs, err := PartialSignTxWithChannel_SatsNet(resv.Channel, deanchorTx, prevFetcher2)
	if err != nil {
		return err
	}
	resv.PreFetcherForAnchorTx = prevFetcher2
	resv.AnchorTx = deanchorTx
	resv.LocalSplicingSigInfo.DeAnchorSig = deanchorSigs
	resv.Channel.UpdateUtxosByPendingTx_SatsNet(resv.AnchorTx)

	if err = p.SignNextCommitment(&resv.RevocationInfo); err != nil {
		Log.Errorf("SignNextCommitment failed. %v", err)
		return err
	}
	resv.LocalRevKey, err = p.GetCurrRevocationKey(resv.Channel)
	if err != nil {
		Log.Errorf("GetCurrRevocationKey failed. %v", err)
		return err
	}
	resv.LocalNextRevKey, err = p.GetNextRevocationKey(resv.Channel)
	if err != nil {
		Log.Errorf("GetNextRevocationKey failed. %v", err)
		return err
	}

	return nil
}

func (p *Manager) FunderProcessSplicingOutSigned(resv *SplicingReservation) error {
	chanPoint := resv.ChannelId

	if !resv.IsInitiator && len(resv.PreTxs) != 0 {
		if len(resv.RemoteSplicingSigInfo.SplicingPrevTxSig) != 1 {
			return fmt.Errorf("funderProcessSplicingOutSigned should provide sig of previous tx")
		}
		err := SignAndVerifyTx(resv.Inscribe.CommitTx, resv.Inscribe.GetCommitPrevOutputFetcher(),
			resv.Channel.GetRemotePubKey().SerializeCompressed(), resv.RemoteSplicingSigInfo.SplicingPrevTxSig[0])
		if err != nil {
			return fmt.Errorf("funderProcessSplicingOutSigned SignAndVerifyTx failed, %v", err)
		}
	}

	_, err := SignAndVerifyTxWithChannel(resv.Channel, resv.SplicingTx, resv.PreFetcherForSplicing,
		resv.RemoteSplicingSigInfo.SplicingSig)
	if err != nil {
		return err
	}

	_, err = SignAndVerifyTxWithChannel_SatsNet(resv.Channel, resv.AnchorTx,
		resv.PreFetcherForAnchorTx, resv.RemoteSplicingSigInfo.DeAnchorSig)
	if err != nil {
		return err
	}
	err = p.TestAcceptance_SatsNet([]*swire.MsgTx{resv.AnchorTx})
	if err != nil {
		return err
	}

	err = p.ReceiveRevocation(&resv.RevocationInfo, resv.RemoteRev)
	if err != nil {
		Log.Errorf("ReceiveRevocation %s failed. %v", chanPoint, err)
		return err
	}

	err = p.ReceiveNewCommitment(&resv.RevocationInfo, &resv.LocalCommitInfo.CommitSigInfo, append(resv.PreTxs, resv.SplicingTx))
	if err != nil {
		Log.Errorf("ReceiveNewCommitment %s failed. %v", chanPoint, err)
		return err
	}

	resv.LocalRev, err = p.RevokeCurrentCommitment(&resv.RevocationInfo)
	if err != nil {
		Log.Errorf("RevokeCurrentCommitment %s failed. %v", chanPoint, err)
		return err
	}
	return nil
}
