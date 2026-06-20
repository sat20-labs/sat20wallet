package wallet

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func convertOutputsToUtxos(outputs []*TxOutput) []string {
	result := make([]string, len(outputs))
	for i, output := range outputs {
		result[i] = output.OutPointStr
	}
	return result
}

func (p *Manager) getTxOutputFromIndexerOrInscribe(utxo string, inscribe *InscribeResv) (*TxOutput, error) {
	info, err := p.GetIndexerClient().GetTxOutput(utxo)
	if err == nil {
		return info, nil
	}
	change := inscribe.GetChangeOutput()
	if change != nil && change.OutPointStr == utxo {
		return change, nil
	}
	return nil, err
}

func (p *Manager) FunderInitSplicingInProcess(channel *Channel, assetName *swire.AssetName,
	amt *Decimal, feeRate int64, utxos, fees []*TxOutput, inscribe *InscribeResv,
	revealPrivKey []byte, reason string) (string, int64, error) {

	oldChannel, err := p.loadChannel(channel.ChannelId)
	if err != nil {
		return "", 0, err
	}

	tickerInfo := p.getTickerInfo(assetName)
	if tickerInfo == nil {
		return "", 0, fmt.Errorf("can't get ticker %s info", assetName.String())
	}
	if revealPrivKey == nil {
		if inscribe != nil {
			revealPrivKey = inscribe.RevealPrivateKey
		} else {
			priv, err := btcec.NewPrivateKey()
			if err != nil {
				return "", 0, err
			}
			revealPrivKey = priv.Serialize()
		}
	}

	resv := SplicingReservation{}
	resv.InitRuntime()
	resv.Status = RS_INIT
	resv.IsInitiator = reason != SPLICING_REASON_REMOTE
	resv.WalletId = channel.LocalWallet().GetWalletId()
	resv.SetLocalWallet(channel.LocalWallet().Clone())
	resv.ChannelId = channel.ChannelId
	resv.OldChanPoint = channel.ChanPoint.OutPointStr
	resv.OldChannel = oldChannel
	resv.Channel = channel
	resv.AssetName = GetAssetName(tickerInfo)
	resv.TickerInfo = tickerInfo
	resv.Amt = amt
	resv.NeedSendSplicingTx = true
	resv.FeeRate = feeRate
	resv.RevealPrivKey = revealPrivKey
	resv.Inscribe = inscribe

	if inscribe != nil {
		var preSignedTx []*wire.MsgTx
		if resv.IsInitiator {
			fundingPubKey := channel.GetLocalPubKey().SerializeCompressed()
			sig, err := GetSignFromSignedTx(resv.Inscribe.CommitTx, resv.Inscribe.GetCommitPrevOutputFetcher(), fundingPubKey)
			if err != nil {
				Log.Errorf("GetSignFromSignedTx failed, %v", err)
				return "", 0, err
			}
			resv.LocalSplicingSigInfo.SplicingPrevTxSig = append(resv.LocalSplicingSigInfo.SplicingPrevTxSig, sig)
		} else {
			resv.LocalSplicingSigInfo.SplicingPrevTxSig = append(resv.LocalSplicingSigInfo.SplicingPrevTxSig, nil)
		}
		preSignedTx = append(preSignedTx, resv.Inscribe.CommitTx, resv.Inscribe.RevealTx)
		resv.LocalSplicingSigInfo.SplicingPrevTxSig = append(resv.LocalSplicingSigInfo.SplicingPrevTxSig, nil)
		resv.PreTxs = preSignedTx
	}

	n, v := resv.Channel.NeedStubUtxo(resv.AssetName)
	if err := p.AllowSplicingInV2(&resv, utxos, fees, n, v); err != nil {
		return "", 0, err
	}
	if err := p.SaveBackupChannelToDB(&resv.OldChannel.ChannelInDB); err != nil {
		Log.Errorf("SaveBackupChannelToDB failed. %v", err)
		return "", 0, err
	}

	resv.InReq = &wwire.SplicingInRequest{
		MsgHeader:          wwire.NewMsgHeader(),
		ChannelId:          channel.ChannelId,
		CommitHeight:       channel.CommitHeight,
		AssetName:          resv.AssetName.String(),
		Amt:                resv.Amt.String(),
		Stub:               resv.InputStubOutpoint(),
		Utxos:              convertOutputsToUtxos(utxos),
		Fees:               convertOutputsToUtxos(fees),
		PreTxInputs:        inscribe.GetInputs(),
		RevealKey:          resv.RevealPrivKey,
		FeeRate:            resv.FeeRate,
		NeedSendSplicingTx: resv.NeedSendSplicingTx,
		Reason:             reason,
		Memo:               nil,
	}
	msg, err := json.Marshal(resv.InReq)
	if err != nil {
		return "", 0, err
	}
	resv.ReqSig, err = resv.LocalWallet().SignMessage(msg)
	if err != nil {
		return "", 0, err
	}

	for {
		channelID := resv.Channel.ChannelId
		if channel.PeerRPC == nil {
			err = fmt.Errorf("peer rpc is not initialized")
			break
		}
		err = channel.PeerRPC.SendSplicingInReq(&resv)
		if err != nil {
			Log.Errorf("SendSplicingInReq failed. %v", err)
			break
		}
		channel.ResvId = resv.Id

		err = p.FunderProcessAcceptSplicingIn(&resv)
		if err != nil {
			Log.Errorf("funderProcessAcceptSplicingIn failed. %v", err)
			break
		}

		err = channel.PeerRPC.SendSplicingInCommitSigReq(&resv)
		if err != nil {
			Log.Errorf("SendSplicingInCommitSigReq failed. %v", err)
			break
		}
		if !resv.IsInitiator && len(resv.PreTxs) != 0 {
			if len(resv.RemoteSplicingSigInfo.SplicingPrevTxSig) != 1 {
				err = fmt.Errorf("funderInitSplicingInProcess should provide sig of previous tx")
				break
			}
			err = SignAndVerifyTx(resv.Inscribe.CommitTx, resv.Inscribe.GetCommitPrevOutputFetcher(),
				resv.Channel.GetRemotePubKey().SerializeCompressed(), resv.RemoteSplicingSigInfo.SplicingPrevTxSig[0])
			if err != nil {
				Log.Errorf("funderInitSplicingInProcess SignAndVerifyTx failed, %v", err)
				break
			}
		}
		_, err = SignAndVerifyTxWithChannel(resv.Channel, resv.SplicingTx, resv.PreFetcherForSplicing, resv.RemoteSplicingSigInfo.SplicingSig)
		if err != nil {
			Log.Errorf("SignAndVerifyTx failed. %v", err)
			break
		}

		err = p.ReceiveRevocation(&resv.RevocationInfo, resv.RemoteRev)
		if err != nil {
			Log.Errorf("ReceiveRevocation %s failed. %v", channelID, err)
			break
		}

		err = p.ReceiveNewCommitment(&resv.RevocationInfo, &resv.LocalCommitInfo.CommitSigInfo, append(resv.PreTxs, resv.SplicingTx))
		if err != nil {
			Log.Errorf("ReceiveNewCommitment %s failed. %v", channelID, err)
			break
		}

		resv.LocalRev, err = p.RevokeCurrentCommitment(&resv.RevocationInfo)
		if err != nil {
			Log.Errorf("RevokeCurrentCommitment %s failed. %v", channelID, err)
			break
		}

		err = channel.PeerRPC.SendSplicingInRevokeAndAckReq(&resv)
		if err != nil {
			if !isBroadcastResultUnknown(err) {
				Log.Errorf("SendSplicingRevokeAndAckReq %s failed. %v", channelID, err)
			} else {
				Log.Warnf("SendSplicingRevokeAndAckReq %s result is unknown. Abort local splicing-in state advance and wait for peer sync. %v", channelID, err)
			}
			break
		}

		resv.Status = RS_SPLICINGIN_STARTED
		resv.Channel.CommitHeight += 1
		resv.Channel.UpdateTime = resv.Id
		err = SaveReservation(p.db, &resv.SplicingDataInDB)
		if err != nil {
			Log.Errorf("SaveReservation failed. %v", err)
			break
		}
		err = p.saveChannelToDB(resv.Channel)
		if err != nil {
			Log.Errorf("saveChannelToDB failed. %v", err)
			break
		}

		p.addResv(&resv)
		err = p.HandleSplicingInStarted(&resv)
		break
	}

	channel.ResvId = 0
	splicingTxID := ""
	if err == nil {
		if inscribe != nil {
			inscribe.Status = RS_CLOSED
		}
		splicingTxID = resv.SplicingTx.TxID()
		Log.Infof("SplicingIn TxId: %s", splicingTxID)
	} else {
		_ = p.saveChannelToDB(resv.OldChannel)
		p.enableChannel(resv.OldChannel)
		p.DelResvWithId(resv.Id)
		if resv.Id != 0 && channel.PeerRPC != nil {
			_ = channel.PeerRPC.SendActionResultNfty(resv.Id, RESV_TYPE_SPLICING, -1, err.Error())
		}
		p.GetUtxoLocker().UnlockUtxosWithTx(resv.SplicingTx)
	}

	return splicingTxID, resv.Id, err
}

func (p *Manager) FunderInitSplicingOutProcess(channel *Channel, destAddr string,
	assetName *AssetName, feeRate int64, amt *Decimal, utxos, fees []*TxOutput,
	inscribe *InscribeResv, revealPrivKey []byte, reason string, moreData []byte) (string, int64, error) {

	oldChannel, err := p.loadChannel(channel.ChannelId)
	if err != nil {
		return "", 0, err
	}
	tickerInfo := p.getTickerInfo(&assetName.AssetName)
	if tickerInfo == nil {
		return "", 0, fmt.Errorf("can't get ticker %s info", assetName.String())
	}
	if destAddr == "" {
		destAddr = channel.LocalWallet().GetAddress()
	}
	if revealPrivKey == nil {
		if inscribe != nil {
			revealPrivKey = inscribe.RevealPrivateKey
		} else {
			priv, err := btcec.NewPrivateKey()
			if err != nil {
				return "", 0, err
			}
			revealPrivKey = priv.Serialize()
		}
	}

	resv := SplicingReservation{}
	resv.InitRuntime()
	resv.Status = RS_INIT
	resv.IsInitiator = reason != SPLICING_REASON_REMOTE
	resv.WalletId = channel.LocalWallet().GetWalletId()
	resv.SetLocalWallet(channel.LocalWallet().Clone())
	resv.ChannelId = channel.ChannelId
	resv.OldChanPoint = channel.ChanPoint.OutPointStr
	resv.OldChannel = oldChannel
	resv.Channel = channel
	resv.AssetName = assetName
	resv.TickerInfo = tickerInfo
	resv.Amt = amt
	resv.NeedSendSplicingTx = true
	resv.FeeRate = feeRate
	resv.DestAddr = destAddr
	resv.SplicingInputs = utxos
	resv.RevealPrivKey = revealPrivKey
	resv.Inscribe = inscribe

	if inscribe != nil {
		var preSignedTx []*wire.MsgTx
		if resv.IsInitiator {
			fundingPubKey := channel.GetLocalPubKey().SerializeCompressed()
			sig, err := GetSignFromSignedTx(resv.Inscribe.CommitTx, resv.Inscribe.GetCommitPrevOutputFetcher(), fundingPubKey)
			if err != nil {
				Log.Errorf("GetSignFromSignedTx failed, %v", err)
				return "", 0, err
			}
			resv.LocalSplicingSigInfo.SplicingPrevTxSig = append(resv.LocalSplicingSigInfo.SplicingPrevTxSig, sig)
		} else {
			resv.LocalSplicingSigInfo.SplicingPrevTxSig = append(resv.LocalSplicingSigInfo.SplicingPrevTxSig, nil)
		}
		preSignedTx = append(preSignedTx, resv.Inscribe.CommitTx, resv.Inscribe.RevealTx)
		resv.LocalSplicingSigInfo.SplicingPrevTxSig = append(resv.LocalSplicingSigInfo.SplicingPrevTxSig, nil)
		resv.PreTxs = preSignedTx
	}

	err = p.AllowSplicingOutV2(&resv, fees)
	if err != nil {
		return "", 0, err
	}
	if err := p.SaveBackupChannelToDB(&resv.OldChannel.ChannelInDB); err != nil {
		Log.Errorf("SaveBackupChannelToDB failed. %v", err)
		return "", 0, err
	}

	resv.OutReq = &wwire.SplicingOutRequest{
		MsgHeader:    wwire.NewMsgHeader(),
		ChannelId:    channel.ChannelId,
		CommitHeight: channel.CommitHeight,
		AssetName:    resv.AssetName.String(),
		Amt:          resv.Amt.String(),
		Stub:         resv.InputStubOutpoint(),
		Utxos:        convertOutputsToUtxos(utxos),
		Fees:         convertOutputsToUtxos(fees),
		PreTxInputs:  inscribe.GetInputs(),
		RevealKey:    resv.RevealPrivKey,
		FeeRate:      resv.FeeRate,
		DestAddr:     resv.DestAddr,
		Reason:       reason,
		Memo:         moreData,
	}
	msg, err := json.Marshal(resv.OutReq)
	if err != nil {
		return "", 0, err
	}
	resv.ReqSig, err = resv.LocalWallet().SignMessage(msg)
	if err != nil {
		return "", 0, err
	}

	for {
		channelID := resv.Channel.ChannelId
		if channel.PeerRPC == nil {
			err = fmt.Errorf("peer rpc is not initialized")
			break
		}
		err = channel.PeerRPC.SendSplicingOutReq(&resv)
		if err != nil {
			Log.Errorf("SendSplicingOutReq failed. %v", err)
			break
		}
		channel.ResvId = resv.Id

		err = p.FunderProcessSplicingOutAccpted(&resv)
		if err != nil {
			Log.Errorf("funderProcessAcceptSplicingOut failed. %v", err)
			break
		}

		err = channel.PeerRPC.SendSplicingOutCommitSigReq(&resv)
		if err != nil {
			Log.Errorf("SendSplicingOutCommitSigReq failed. %v", err)
			break
		}

		err = p.FunderProcessSplicingOutSigned(&resv)
		if err != nil {
			Log.Errorf("funderProcessSplicingOutSigned failed. %v", err)
			break
		}

		err = channel.PeerRPC.SendSplicingOutRevokeAndAckReq(&resv)
		if err != nil {
			if !isBroadcastResultUnknown(err) {
				Log.Errorf("SendSplicingOutRevokeAndAckReq %s failed. %v", channelID, err)
			} else {
				Log.Warnf("SendSplicingOutRevokeAndAckReq %s result is unknown. Abort local splicing-out state advance and wait for peer sync. %v", channelID, err)
			}
			break
		}

		resv.Status = RS_SPLICINGOUT_STARTED
		resv.Channel.CommitHeight += 1
		resv.Channel.UpdateTime = resv.Id
		err = SaveReservation(p.db, &resv.SplicingDataInDB)
		if err != nil {
			Log.Errorf("SaveReservation failed. %v", err)
			break
		}
		err = p.saveChannelToDB(resv.Channel)
		if err != nil {
			Log.Errorf("saveChannelToDB failed. %v", err)
			break
		}

		p.addResv(&resv)
		go p.HandleSplicingOutStarted(&resv)
		break
	}

	channel.ResvId = 0
	splicingTxID := ""
	if err == nil {
		if inscribe != nil {
			inscribe.Status = RS_CLOSED
		}
		splicingTxID = resv.SplicingTx.TxID()
		Log.Infof("SplicingOut TxId: %s", splicingTxID)
	} else {
		_ = p.saveChannelToDB(resv.OldChannel)
		p.enableChannel(resv.OldChannel)
		p.DelResvWithId(resv.Id)
		if resv.Id != 0 && channel.PeerRPC != nil {
			_ = channel.PeerRPC.SendActionResultNfty(resv.Id, RESV_TYPE_SPLICING, -1, err.Error())
		}
		p.GetUtxoLocker().UnlockUtxosWithTx(resv.SplicingTx)
	}

	return splicingTxID, resv.Id, err
}

func (p *Manager) AllowSplicingIn(resv *SplicingReservation, utxos, fees []string, needStub int, stubValue int64) error {
	var assetInputs, feeInputs []*TxOutput
	if resv.Inscribe != nil {
		info := GenerateBRC20TransferOutput(resv.Inscribe.RevealTx, &resv.AssetName.AssetName, resv.Amt)
		assetInputs = []*TxOutput{info}
	} else {
		for _, utxo := range utxos {
			info, err := p.GetIndexerClient().GetTxOutput(utxo)
			if err != nil {
				Log.Errorf("AllowSplicingIn: GetTxOutput %s failed, %v", utxo, err)
				return err
			}
			assetInputs = append(assetInputs, info)
		}
	}

	for _, utxo := range fees {
		info, err := p.getTxOutputFromIndexerOrInscribe(utxo, resv.Inscribe)
		if err != nil {
			Log.Errorf("AllowSplicingIn: GetTxOutput %s failed, %v", utxo, err)
			return err
		}
		feeInputs = append(feeInputs, info)
	}

	return p.AllowSplicingInV2(resv, assetInputs, feeInputs, needStub, stubValue)
}

func (p *Manager) AllowSplicingInV2(resv *SplicingReservation, utxos, fees []*TxOutput, needStub int, stubValue int64) error {
	assetName := resv.AssetName
	amt := resv.Amt.Clone()
	if amt.Sign() <= 0 {
		return fmt.Errorf("amt should be larger than 0")
	}
	if IsBindingSat(resv.AssetName) {
		if indexer.IsPlainAsset(&resv.AssetName.AssetName) && amt.Int64() < 330 {
			return fmt.Errorf("plain sats should larger than 330")
		}
		if amt.Int64()%int64(resv.AssetName.N) != 0 {
			return fmt.Errorf("amt should be times of %d", resv.AssetName.N)
		}
	}

	feeRate := resv.FeeRate

	var initiatorPkScript, peerPkScript, channelPkScript []byte
	if resv.Channel != nil {
		channelPkScript = resv.Channel.GetChannelPkScript()
		if resv.IsInitiator {
			initiatorPkScript = resv.Channel.GetLocalPkScript()
			peerPkScript = resv.Channel.GetRemotePkScript()
		} else {
			initiatorPkScript = resv.Channel.GetRemotePkScript()
			peerPkScript = resv.Channel.GetLocalPkScript()
		}
	}

	splicingUtxosInfo := make([]*TxOutput, 0)
	var weightEstimate utils.TxWeightEstimator
	weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
	weightEstimate.AddP2WSHOutput()
	weightEstimate.AddP2WSHOutput()

	var totalAssetAmount *Decimal
	totalValue := int64(0)
	for i := 0; i < len(utxos); i++ {
		info := utxos[i]
		utxo := info.OutPointStr

		n, _ := info.GetAssetV2(&assetName.AssetName)
		if n.Sign() == 0 {
			Log.Warningf("utxo %s has no asset %s", utxo, assetName.String())
			continue
		}

		if peerPkScript != nil && channelPkScript != nil {
			if bytes.Equal(info.OutValue.PkScript, peerPkScript) || bytes.Equal(info.OutValue.PkScript, channelPkScript) {
				return fmt.Errorf("can't use this utxo %s as fee", utxo)
			}
		}

		if err := AlignAsset(info, &assetName.AssetName); err != nil {
			return err
		}

		totalAssetAmount = totalAssetAmount.Add(n)
		totalValue += info.OutValue.Value
		splicingUtxosInfo = append(splicingUtxosInfo, info)
		weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
	}
	if totalAssetAmount.Cmp(amt) < 0 {
		return fmt.Errorf("no enough asset amount. require %d, but only %d", amt, totalAssetAmount)
	}
	if totalAssetAmount.Cmp(amt) > 0 {
		weightEstimate.AddP2TROutput()
		if IsRunes(assetName.Protocol) {
			payload, err := GetRunesEstimatePayload(assetName, 1)
			if err != nil {
				return err
			}
			weightEstimate.AddOutput(payload)
		}
	}

	if !IsPlainAsset(assetName) && len(fees) == 0 {
		return fmt.Errorf("need some utxo as fee")
	}
	feeValue := int64(0)
	var feeUtxosInfo []*TxOutput
	needInputStub := NeedStubUtxoForInputAsset(assetName, amt)
	if !needInputStub && resv.StubUtxo != nil {
		return fmt.Errorf("unexpected splicing-in input stub for asset %s amt %s", assetName.String(), amt.String())
	}
	if needInputStub {
		if resv.Channel == nil {
			return fmt.Errorf("splicing-in input stub requires a channel")
		}
		if resv.StubUtxo == nil {
			var address string
			if resv.IsInitiator == resv.Channel.IsInitiator {
				address = resv.Channel.GetLocalAddress()
			} else {
				address = resv.Channel.GetRemoteAddress()
			}
			excludedUtxos := make(map[string]bool)
			for _, v := range splicingUtxosInfo {
				excludedUtxos[v.OutPointStr] = true
			}
			for _, v := range fees {
				excludedUtxos[v.OutPointStr] = true
			}
			if resv.Inscribe != nil {
				for _, txIn := range resv.Inscribe.CommitTx.TxIn {
					excludedUtxos[txIn.PreviousOutPoint.String()] = true
				}
			}
			stubs, err := p.GetOrGenerateStubs(address, 1, excludedUtxos, feeRate)
			if err != nil {
				return fmt.Errorf("splicing-in can't get stub utxo, %v", err)
			}
			resv.StubUtxo = stubs[0]
		}
		if resv.StubUtxo.Value() != 330 {
			return fmt.Errorf("stub utxo should be 330 sats, %d", resv.StubUtxo.Value())
		}
		if resv.StubUtxo.HasAsset() {
			return fmt.Errorf("stub utxo %s has assets", resv.StubUtxo.OutPointStr)
		}
		AlignAsset(resv.StubUtxo, &indexer.ASSET_PLAIN_SAT)
		if initiatorPkScript != nil && !bytes.Equal(resv.StubUtxo.OutValue.PkScript, initiatorPkScript) {
			return fmt.Errorf("stub utxo %s does not belong to action initiator", resv.StubUtxo.OutPointStr)
		}
		resv.StubIsSet = true
		weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
	}
	if needStub > 0 {
		for range needStub {
			weightEstimate.AddP2WSHOutput()
		}
		feeValue -= int64(needStub) * stubValue
	}

	switch assetName.Protocol {
	case indexer.PROTOCOL_NAME_RUNES:
		if totalAssetAmount.Cmp(amt) != 0 && totalValue < 660 {
			feeValue -= 330
		}
	case indexer.PROTOCOL_NAME_ORDX:
		satsNum1 := indexer.GetBindingSatNum(amt, uint32(assetName.N))
		if satsNum1 < 330 {
			feeValue -= 330
		}
	}
	if len(fees) != 0 {
		for i := 0; i < len(fees); i++ {
			info := fees[i]
			if info.HasAsset() {
				continue
			}
			AlignAsset(info, &indexer.ASSET_PLAIN_SAT)

			if bytes.Equal(info.OutValue.PkScript, peerPkScript) || bytes.Equal(info.OutValue.PkScript, channelPkScript) {
				return fmt.Errorf("can't use this utxo %s as fee", info.OutPointStr)
			}
			if initiatorPkScript != nil && !bytes.Equal(info.OutValue.PkScript, initiatorPkScript) {
				return fmt.Errorf("fee utxo %s does not belong to action initiator", info.OutPointStr)
			}

			Log.Infof("fee %d", info.OutValue.Value)
			feeValue += info.OutValue.Value
			feeUtxosInfo = append(feeUtxosInfo, info)
			weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
			if feeValue >= weightEstimate.Fee(feeRate) {
				break
			}
		}
	}
	switch assetName.Protocol {
	case indexer.PROTOCOL_NAME_RUNES:
		if totalAssetAmount.Cmp(amt) != 0 && totalValue < 660 {
			feeValue += 330
		}
	case indexer.PROTOCOL_NAME_ORDX:
		satsNum1 := indexer.GetBindingSatNum(amt, uint32(assetName.N))
		if satsNum1 < 330 {
			feeValue += 330
		}
	}

	requiredFee1 := weightEstimate.Fee(feeRate)
	weightEstimate.AddP2TROutput()
	requiredFee2 := weightEstimate.Fee(feeRate)

	Log.Infof("require amt %s, total input asset %s, total input sats %d feeValue %d",
		amt.String(), totalAssetAmount.String(), totalValue, feeValue)

	requiredFee := requiredFee1
	splicingValue := int64(0)
	splicingChange := int64(0)
	feechange := int64(0)

	if feeValue == 0 {
		if IsPlainAsset(assetName) {
			satsNum := amt.Int64()
			splicingValue = satsNum
			if splicingValue < 330 {
				return fmt.Errorf("splicing-in value too small. at least 330, but only %d", splicingValue)
			}
			if totalValue < satsNum+requiredFee1 {
				return fmt.Errorf("no enough sats to pay fee. require %d, but only %d", requiredFee1, totalValue-satsNum)
			}

			feechange = totalValue - (satsNum + requiredFee2)
			if feechange >= 330 {
				requiredFee = requiredFee2
			} else {
				feechange = 0
			}
		} else {
			if IsBindingSat(assetName) {
				return fmt.Errorf("need some plain utxo to pay fee")
			}
			if totalAssetAmount.Cmp(amt) > 0 && !IsRunes(assetName.Protocol) {
				return fmt.Errorf("all assets should be transferred together. require %d, but %d", amt, totalAssetAmount)
			}

			splicingValue = 330
			if totalAssetAmount.Cmp(amt) > 0 {
				splicingChange = 330
			}
			if totalValue < splicingValue+splicingChange+requiredFee1 {
				return fmt.Errorf("no enough sats to pay fee. require %d, but only %d", requiredFee1, totalValue-splicingValue-splicingChange)
			}
			feechange = totalValue - (splicingValue + splicingChange + requiredFee2)
			if feechange >= 330 {
				requiredFee = requiredFee2
			} else {
				feechange = 0
			}
		}
	} else {
		if IsPlainAsset(assetName) {
			satsNum := amt.Int64()
			splicingValue = satsNum
			feeValue += totalValue - satsNum
			if feeValue < 0 {
				return fmt.Errorf("splicing-in no enough sats to pay fee, %d", feeValue)
			}
			if splicingValue < 330 {
				return fmt.Errorf("splicing-in value too small. at least 330, but only %d", splicingValue)
			}
			if feeValue < requiredFee1 {
				return fmt.Errorf("no enough sats to pay fee. require %d, but only %d", requiredFee1, feeValue)
			}
			feechange = feeValue - requiredFee2
			if feechange >= 330 {
				requiredFee = requiredFee2
			} else {
				feechange = 0
			}
		} else if IsBindingSat(assetName) {
			if resv.StubIsSet {
				splitInputs := make([]*TxOutput, 0, len(splicingUtxosInfo)+1)
				splitInputs = append(splitInputs, resv.StubUtxo.Clone())
				splitInputs = append(splitInputs, CloneOutput(splicingUtxosInfo)...)
				splicingOutput, changeOutput, err := GenTxOutput(splitInputs, assetName, 0, amt)
				if err != nil {
					return err
				}
				splicingValue = splicingOutput.Value()
				if splicingValue < 330 {
					return fmt.Errorf("splicing-in value too small. at least 330, but only %d", splicingValue)
				}
				inputValue := totalValue + resv.StubUtxo.Value()
				if amt.Cmp(totalAssetAmount) < 0 {
					if changeOutput == nil {
						return fmt.Errorf("can't split the asset change")
					}
					splicingChange = changeOutput.Value()
					if splicingChange > 0 && splicingChange < 330 {
						feeValue -= 330 - splicingChange
						splicingChange = 330
					}
				} else {
					feeValue += inputValue - splicingValue
				}
			} else {
				_, offset, err := calcAssetOffsetForSplicing(splicingUtxosInfo, assetName, amt)
				if err != nil {
					Log.Errorf("calcAssetOffsetForSplicing failed, %v", err)
					return err
				}

				splicingValue = offset
				if splicingValue < 330 {
					return fmt.Errorf("splicing-in value too small. at least 330, but only %d", splicingValue)
				}

				splicingChange = totalValue - offset
				if amt.Cmp(totalAssetAmount) < 0 {
					if splicingChange > 0 && splicingChange < 330 {
						splicingChange = 330
						feeValue -= 330 - (totalValue - offset)
					}
				} else {
					splicingChange = 0
					feeValue += totalValue - offset
				}
			}

			if feeValue < requiredFee1 {
				return fmt.Errorf("no enough sats to pay fee. require %d, but only %d", requiredFee1, feeValue)
			}
			feechange = feeValue - requiredFee2
			if feechange >= 330 {
				requiredFee = requiredFee2
			} else {
				feechange = 0
			}
		} else {
			if totalAssetAmount.Cmp(amt) > 0 {
				if !IsRunes(assetName.Protocol) {
					return fmt.Errorf("all assets should be transferred together. require %d, but %d", amt, totalAssetAmount)
				}
				splicingChange = 330
			}

			splicingValue = 330
			feeValue += totalValue - splicingValue - splicingChange
			if feeValue < requiredFee1 {
				return fmt.Errorf("no enough sats to pay fee. require %d, but only %d", requiredFee1, feeValue)
			}
			feechange = feeValue - requiredFee2
			if feechange >= 330 {
				requiredFee = requiredFee2
			} else {
				feechange = 0
			}
		}
	}

	Log.Infof("splicingValue %d, splicingChange %d, feechange %d, requiredFee %d", splicingValue, splicingChange, feechange, requiredFee)

	resv.SplicingAmt = amt
	resv.SplicingValue = splicingValue
	resv.SplicingInputs = splicingUtxosInfo
	resv.SplicingChange = splicingChange
	resv.Fees = feeUtxosInfo
	resv.FeeChange = feechange
	resv.RequiredFee = requiredFee
	return nil
}

func calcAssetOffsetForSplicing(inputs []*TxOutput, name *AssetName, amt *Decimal) (int64, int64, error) {
	outputs := indexer.NewTxOutput(0)
	var total int64
	for _, u := range inputs {
		total += u.Value()
		if err := outputs.Append(u.Clone()); err != nil {
			return 0, 0, err
		}
	}
	offset, err := outputs.GetAssetOffset(&name.AssetName, amt)
	if err != nil {
		return 0, 0, err
	}
	if offset < 330 {
		if indexer.IsBindingSat(&name.AssetName) {
			return total, 330, nil
		}
		offsets, ok := outputs.Offsets[name.AssetName]
		if ok {
			_, offset2 := offsets.Cut(offset)
			if len(offset2) == 0 {
				return total, 330, nil
			}
			if offset2[0].Start >= 330-offset {
				return total, 330, nil
			}
		}
	}
	return total, offset, nil
}

func (p *Manager) AllowSplicingOut(resv *SplicingReservation, fees []string) error {
	var feeInputs []*TxOutput
	for _, utxo := range fees {
		info, err := p.getTxOutputFromIndexerOrInscribe(utxo, resv.Inscribe)
		if err != nil {
			Log.Errorf("AllowSplicingOut: GetTxOutput %s failed, %v", utxo, err)
			return err
		}
		feeInputs = append(feeInputs, info)
	}
	return p.AllowSplicingOutV2(resv, feeInputs)
}

func (p *Manager) AllowSplicingOutV2(resv *SplicingReservation, fees []*TxOutput) error {
	channel := resv.Channel
	amt := resv.Amt.Clone()
	fromInitiator := resv.IsInitiator == channel.IsInitiator
	serviceFee := CalcSplicingOutServiceFee(amt, fromInitiator, channel.FeeCfg)
	feeRate := resv.FeeRate

	if IsBindingSat(resv.AssetName) {
		if indexer.IsPlainAsset(&resv.AssetName.AssetName) && amt.Int64() < 330 {
			return fmt.Errorf("plain sats should larger than 330")
		}
		if amt.Int64()%int64(resv.AssetName.N) != 0 {
			return fmt.Errorf("amt should be times of %d", resv.AssetName.N)
		}
	}

	expectedAmt := amt
	if len(fees) == 0 {
		if IsPlainAsset(resv.AssetName) {
			expectedAmt = expectedAmt.Add(indexer.NewDefaultDecimal(serviceFee))
		} else {
			return fmt.Errorf("should provide plain utxos as fees")
		}
	}
	if err := channel.HasEnoughAsset(resv.AssetName, expectedAmt, len(fees) != 0, fromInitiator); err != nil {
		return err
	}

	splicingUtxosInfo := make([]*TxOutput, 0)
	var weightEstimate utils.TxWeightEstimator
	weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
	weightEstimate.AddP2WSHOutput()
	weightEstimate.AddP2TROutput()
	weightEstimate.AddP2TROutput()

	switch resv.AssetName.Protocol {
	case indexer.PROTOCOL_NAME_RUNES:
		payload, err := GetRunesEstimatePayload(resv.AssetName, 1)
		if err != nil {
			return err
		}
		weightEstimate.AddOutput(payload)
	}

	channelPkScript := channel.GetChannelPkScript()
	var initiatorPkScript, peerPkScript []byte
	if resv.IsInitiator {
		initiatorPkScript = channel.GetLocalPkScript()
		peerPkScript = channel.GetRemotePkScript()
	} else {
		initiatorPkScript = channel.GetRemotePkScript()
		peerPkScript = channel.GetLocalPkScript()
	}

	feeValue := int64(0)
	var feeUtxosInfo []*TxOutput
	estimatedFee := CalcFee_SplicingOut(3, len(fees), resv.AssetName, amt, feeRate, resv.IsInitiator == channel.IsInitiator, channel.FeeCfg)
	if len(fees) != 0 {
		for i := 0; i < len(fees); i++ {
			info := fees[i]
			if info.HasAsset() {
				return fmt.Errorf("has assets in utxo %s", info.OutPointStr)
			}
			AlignAsset(info, &indexer.ASSET_PLAIN_SAT)
			if bytes.Equal(info.OutValue.PkScript, peerPkScript) || bytes.Equal(info.OutValue.PkScript, channelPkScript) {
				return fmt.Errorf("can't use this utxo %s as fee", info.OutPointStr)
			}
			if !bytes.Equal(info.OutValue.PkScript, initiatorPkScript) {
				return fmt.Errorf("fee utxo %s does not belong to action initiator", info.OutPointStr)
			}

			feeValue += info.OutValue.Value
			feeUtxosInfo = append(feeUtxosInfo, info)
			weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
			if feeValue >= estimatedFee {
				break
			}
		}
	} else if !IsPlainAsset(resv.AssetName) {
		return fmt.Errorf("need some plain utxo to pay fee")
	}

	splicingValue := int64(0)
	splicingChange := int64(0)
	bonus := int64(0)
	if resv.AssetName.Protocol == indexer.PROTOCOL_NAME_BRC20 {
		if len(resv.SplicingInputs) != 1 {
			return fmt.Errorf("should only one input for splicing out brc20 asset")
		}
		splicingUtxosInfo = resv.SplicingInputs
		weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
		splicingValue = splicingUtxosInfo[0].Value()
	} else {
		inputUtxos := channel.GetFundingOutputs(resv.AssetName)
		var inputAmt *Decimal
		inputValue := int64(0)

		for i := 0; i < len(inputUtxos); i++ {
			utxo := inputUtxos[i]
			assetAmt, _ := utxo.GetAssetV2(&resv.AssetName.AssetName)
			inputAmt = inputAmt.Add(assetAmt)
			inputValue += utxo.OutValue.Value
			splicingUtxosInfo = append(splicingUtxosInfo, utxo)
			weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
			if feeValue != 0 {
				if inputAmt.Cmp(amt) == 0 {
					if IsBindingSat(resv.AssetName) {
						splicingValue = inputValue
						offset, err := utxo.GetAssetOffset(&resv.AssetName.AssetName, assetAmt)
						if err == nil && offset < utxo.OutValue.Value && splicingValue-(utxo.OutValue.Value-offset) >= 330 {
							bonus = utxo.OutValue.Value - offset
							splicingValue -= bonus
						}
					} else {
						splicingValue = 330
						bonus = inputValue - splicingValue
					}
					break
				} else if inputAmt.Cmp(amt) > 0 {
					if IsBindingSat(resv.AssetName) {
						balance := indexer.DecimalSub(amt, indexer.DecimalSub(inputAmt, assetAmt))
						offset, err := utxo.GetAssetOffset(&resv.AssetName.AssetName, balance)
						if err != nil {
							return err
						}
						splicingChange = utxo.Value() - offset
						splicingValue = inputValue - splicingChange
						if splicingValue < 330 {
							if resv.StubUtxo == nil {
								var address string
								if resv.IsInitiator == resv.Channel.IsInitiator {
									address = resv.Channel.GetLocalAddress()
								} else {
									address = resv.Channel.GetRemoteAddress()
								}
								excludedUtxos := make(map[string]bool)
								for _, v := range fees {
									excludedUtxos[v.OutPointStr] = true
								}
								if resv.Inscribe != nil {
									for _, txIn := range resv.Inscribe.CommitTx.TxIn {
										excludedUtxos[txIn.PreviousOutPoint.String()] = true
									}
								}
								stubs, err := p.GetOrGenerateStubs(address, 2, excludedUtxos, resv.FeeRate)
								if err != nil {
									return fmt.Errorf("splicing-out can't get stub utxo, %v", err)
								}
								resv.StubUtxo = stubs[0]
							}
							if resv.StubUtxo.OutValue.Value > 330 {
								return fmt.Errorf("stub utxo too large, %d", resv.StubUtxo.OutValue.Value)
							}
							splicingValue += resv.StubUtxo.OutValue.Value
							resv.StubIsSet = true
							weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
						}
						if splicingChange > 0 {
							if splicingChange < 330 {
								feeValue -= 330 - splicingChange
								splicingChange = 330
							}
							weightEstimate.AddP2WSHOutput()
						}
						break
					}

					splicingValue = 330
					feeValue -= splicingValue
					if feeValue < 0 {
						return fmt.Errorf("no enough sats to pay the fee")
					}
					splicingChange = inputValue
					weightEstimate.AddP2WSHOutput()
					break
				}
			} else {
				tmp := weightEstimate
				fee0 := tmp.Fee(feeRate) + serviceFee
				satsNum := amt.Int64()
				if inputValue == satsNum+fee0 {
					weightEstimate.AddP2WSHOutput()
					splicingValue = satsNum
					break
				} else if inputValue > satsNum+fee0 {
					tmp.AddP2WSHOutput()
					fee1 := tmp.Fee(feeRate) + serviceFee
					if inputValue >= satsNum+fee1+330 {
						weightEstimate.AddP2WSHOutput()
						splicingValue = satsNum
						splicingChange = inputValue - satsNum - fee1
						break
					}
				}
			}
		}
	}

	requiredFee := weightEstimate.Fee(feeRate) + serviceFee
	if len(fees) != 0 && feeValue < requiredFee {
		return fmt.Errorf("no enough sats to pay the fee, requird %d but only %d", requiredFee, feeValue)
	}
	feeChange := feeValue - requiredFee
	if feeChange < 0 {
		return fmt.Errorf("no enough sats to pay the fee, requird %d but only %d", requiredFee, feeValue)
	} else if feeChange < 330 {
		feeChange = 0
	} else {
		weightEstimate.AddP2TROutput()
		requiredFee2 := weightEstimate.Fee(feeRate) + serviceFee
		feeChange2 := feeValue - requiredFee2
		if feeChange >= 330 {
			requiredFee = requiredFee2
			feeChange = feeChange2
		}
	}

	if splicingValue < 330 {
		return fmt.Errorf("splicing-out utxo too small, %d", splicingValue)
	}

	resv.SplicingAmt = amt
	resv.SplicingValue = splicingValue
	resv.SplicingInputs = splicingUtxosInfo
	resv.SplicingChange = splicingChange
	resv.Fees = feeUtxosInfo
	resv.FeeChange = feeChange
	resv.RequiredFee = requiredFee
	resv.Bonus = bonus
	return nil
}
