package wallet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func isIndexerNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "can't find") || strings.Contains(msg, "cannot find")
}

func (p *Manager) markRecoverAscended(resv *SplicingReservation, ascendData *sindexer.AscendData, utxo string) error {
	if ascendData == nil {
		return nil
	}
	if ascendData.FundingUtxo != "" && ascendData.FundingUtxo != utxo {
		return fmt.Errorf("ascend funding utxo mismatch %s != %s", ascendData.FundingUtxo, utxo)
	}
	if ascendData.Address != "" && ascendData.Address != resv.Channel.ChannelId {
		return fmt.Errorf("ascend channel mismatch %s != %s", ascendData.Address, resv.Channel.ChannelId)
	}
	if ascendData.AnchorTxId == "" {
		return fmt.Errorf("ascend anchor tx is empty for %s", utxo)
	}

	resv.RecoverAscended = true
	resv.RecoveredAnchorTxId = ascendData.AnchorTxId
	resv.RecoveredAnchorOutpoint = fmt.Sprintf("%s:0", ascendData.AnchorTxId)
	if _, err := p.getRecoveredAscendedOutput(resv); err != nil {
		resv.RecoverAscended = false
		resv.RecoveredAnchorTxId = ""
		resv.RecoveredAnchorOutpoint = ""
		return err
	}
	Log.Infof("recover ascended funding utxo %s with anchor %s", utxo, resv.RecoveredAnchorOutpoint)
	return nil
}

func (p *Manager) AllowExpand(resv *SplicingReservation, utxo string) error {
	channel := resv.Channel
	assetName := resv.AssetName
	if len(channel.InControl([]string{utxo})) > 0 {
		return fmt.Errorf("utxo has been in channel")
	}

	ascendData, err := p.GetIndexerRPCClient_SatsNet().GetAscendData(utxo)
	if err != nil && !isIndexerNotFoundErr(err) {
		return err
	}

	info, err := p.GetIndexerClient().GetTxOutput(utxo)
	if err != nil {
		return err
	}
	amt, _ := info.GetAssetV2(&assetName.AssetName)
	if amt.Sign() == 0 {
		return fmt.Errorf("utxo %s has not specific asset", utxo)
	}
	if !bytes.Equal(channel.ChanPoint.OutValue.PkScript, info.OutValue.PkScript) {
		return fmt.Errorf("utxo %s is not in channel address", utxo)
	}
	if err := AlignAsset(info, &assetName.AssetName); err != nil {
		return err
	}

	resv.SplicingValue = info.Value()
	resv.SplicingInputs = []*TxOutput{info}
	resv.Amt = amt.Clone()
	resv.SplicingAmt = amt.Clone()
	if ascendData != nil {
		if err := p.markRecoverAscended(resv, ascendData, utxo); err != nil {
			return err
		}
	}
	return nil
}

func (p *Manager) AllowExpandSatsNet(payment *PaymentReservation, utxos []string) error {
	channel := payment.Channel
	utxos = channel.NotInControl_SatsNet(utxos)
	if len(utxos) == 0 {
		return fmt.Errorf("no new utxo")
	}

	var amt *indexer.Decimal
	result := make([]*TxOutput_SatsNet, 0, len(utxos))
	for _, utxo := range utxos {
		info, err := p.GetIndexerRPCClient_SatsNet().GetTxOutput(utxo)
		if err != nil {
			return err
		}
		v := info.GetAsset(&payment.AssetName.AssetName)
		if v.Sign() == 0 {
			return fmt.Errorf("utxo %s has not specific asset", utxo)
		}
		if !bytes.Equal(channel.ChanPoint.OutValue.PkScript, info.OutValue.PkScript) {
			return fmt.Errorf("utxo %s is not in channel address", utxo)
		}
		result = append(result, OutputToSatsNet(info))
		amt = indexer.DecimalAdd(amt, v)
	}

	if err := channel.HasEnoughCapacityForLock(payment.AssetName, amt, true, payment.Channel.IsInitiator == payment.IsInitiator); err != nil {
		return err
	}
	payment.Amt = amt
	payment.Utxos = result
	return nil
}

func isPeerChannelPendingError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "can't find channel") || strings.Contains(msg, "is not ready")
}

func (p *Manager) sendSplicingInReqWithRetry(channel *Channel, resv *SplicingReservation) error {
	var err error
	attempts := 12
	delay := time.Second
	if ENABLE_TESTING {
		attempts = 20
		delay = 100 * time.Millisecond
	}
	for i := 0; i < attempts; i++ {
		err = channel.PeerRPC.SendSplicingInReq(resv)
		if err == nil {
			return nil
		}
		if !isPeerChannelPendingError(err) {
			return err
		}
		time.Sleep(delay)
	}
	return err
}

func (p *Manager) getAllUnmanagedUtxos(channel *Channel, assetName *swire.AssetName) ([]string, error) {
	var utxomap []*indexerwire.TxOutputInfo
	if assetName.Protocol == indexer.PROTOCOL_NAME_BRC20 {
		utxomap = p.GetIndexerClient().GetUtxoListWithBRC20Ticker(channel.Address, assetName, true)
	} else {
		utxomap = p.GetIndexerClient().GetUtxoListWithTicker(channel.Address, assetName)
	}

	utxos := make([]string, 0)
	for _, v := range utxomap {
		if indexer.IsPlainAsset(assetName) && len(v.Assets) != 0 {
			continue
		}
		utxos = append(utxos, v.OutPoint)
	}

	utxos = channel.NotInControl(utxos)
	if len(utxos) == 0 {
		return nil, fmt.Errorf("no new utxo")
	}
	return utxos, nil
}

func (p *Manager) getAllUnmanagedUtxosSatsNet(channel *Channel) ([]string, error) {
	utxomap, err := p.GetIndexerRPCClient_SatsNet().GetUtxosWithAddress(channel.Address)
	if err != nil {
		Log.Errorf("GetUtxosWithAddress %s failed. %v", channel.Address, err)
		return nil, err
	}
	utxos := make([]string, 0, len(utxomap))
	for k := range utxomap {
		utxos = append(utxos, k)
	}
	utxos = channel.NotInControl_SatsNet(utxos)
	if len(utxos) == 0 {
		return nil, fmt.Errorf("no new utxo")
	}
	return utxos, nil
}

// channel mutex must already be held by caller.
func (p *Manager) getUtxosWithChannelAddress(channel *Channel, value int64, num int) ([]string, error) {
	address := channel.Address
	utxos := p.GetIndexerClient().GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)

	utxosInControl := channel.UtxosInControl()
	p.GetUtxoLocker().Reload(address)

	result := make([]string, 0, num)
	for _, u := range utxos {
		utxo := u.OutPoint
		if _, ok := utxosInControl[utxo]; ok {
			continue
		}
		if p.GetUtxoLocker().IsLocked(utxo) {
			continue
		}
		if u.Value == value {
			result = append(result, utxo)
			if len(result) == num {
				break
			}
		}
	}

	if len(result) != num {
		return nil, fmt.Errorf("can't find %d utxos with value %d in channel address", num, value)
	}
	return result, nil
}

func (p *Manager) ExpandAssetInChannel(channel *Channel, assetName *swire.AssetName) ([]string, *Decimal, error) {
	utxos, err := p.getAllUnmanagedUtxos(channel, assetName)
	if err != nil {
		Log.Errorf("AllowExpand failed. %v", err)
		return nil, nil, err
	}

	var value *Decimal
	var lastErr error
	successCount := 0
	anchorIDs := make([]string, 0)
	for _, output := range utxos {
		txid, amt, id, err := p.FunderInitExpandingProcess(channel, assetName, output, "", nil)
		if err != nil {
			Log.Errorf("FunderInitExpandingProcess %s failed. %v", output, err)
			lastErr = err
			continue
		}
		Log.Infof("%s ascending txid %s", output, txid)
		value = value.Add(amt)
		anchorIDs = append(anchorIDs, txid)
		successCount++

		finished := false
		for !finished {
			time.Sleep(100 * time.Millisecond)
			resv, ok := p.GetSplicingReservation(id)
			if ok {
				if resv.Status == RS_CLOSED || resv.Status == RS_SPLICINGIN_ANCHOR_CONFIRMED {
					Log.Infof("resv %d closed", id)
					finished = true
				}
			} else {
				Log.Infof("resv %d deleted", id)
				finished = true
			}
		}
	}
	if successCount == 0 && lastErr != nil {
		return anchorIDs, value, lastErr
	}
	return anchorIDs, value, nil
}

func (p *Manager) FunderInitExpandingProcess(channel *Channel, assetName *swire.AssetName, utxo string,
	reason string, memo []byte) (string, *Decimal, int64, error) {
	oldChannel, err := p.loadChannel(channel.ChannelId)
	if err != nil {
		return "", nil, 0, err
	}
	tickerInfo := p.getTickerInfo(assetName)
	if tickerInfo == nil {
		return "", nil, 0, fmt.Errorf("can't get ticker %s info", assetName.String())
	}
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		return "", nil, 0, err
	}

	resv := SplicingReservation{}
	resv.InitRuntime()
	resv.Status = RS_INIT
	resv.IsInitiator = reason != SPLICING_REASON_REMOTE
	resv.SetLocalWallet(channel.LocalWallet().Clone())
	resv.WalletId = resv.LocalWallet().GetWalletId()
	resv.ChannelId = channel.ChannelId
	resv.OldChanPoint = channel.ChanPoint.OutPointStr
	resv.OldChannel = oldChannel
	resv.Channel = channel
	resv.AssetName = GetAssetName(tickerInfo)
	resv.TickerInfo = tickerInfo
	resv.NeedSendSplicingTx = false
	resv.FeeRate = p.GetFeeRate()
	resv.RevealPrivKey = priv.Serialize()
	resv.Memo = memo

	if err := p.AllowExpand(&resv, utxo); err != nil {
		Log.Errorf("AllowExpand failed. %v", err)
		return "", nil, 0, err
	}
	if err := p.SaveBackupChannelToDB(&resv.OldChannel.ChannelInDB); err != nil {
		Log.Errorf("SaveBackupChannelToDB failed. %v", err)
		return "", nil, 0, err
	}

	var stubUtxos []string
	if c, v := resv.Channel.NeedStubUtxo(resv.AssetName); c > 0 {
		stubUtxos, err = p.getUtxosWithChannelAddress(resv.Channel, v, c)
		if err != nil {
			txid, _, err := p.BatchSendPlainSats(resv.Channel.Address, v, c, resv.FeeRate, nil)
			if err != nil {
				return "", nil, 0, err
			}
			for i := range c {
				stubUtxos = append(stubUtxos, fmt.Sprintf("%s:%d", txid, i))
			}
		}
		stubs := make([]*TxOutput, 0, c)
		for _, outpoint := range stubUtxos {
			info, err := p.GetIndexerClient().GetTxOutput(outpoint)
			if err != nil {
				info = indexer.NewTxOutput(v)
				info.OutPointStr = outpoint
				info.OutValue.PkScript = resv.Channel.GetChannelPkScript()
			}
			stubs = append(stubs, info)
		}
		resv.Fees = stubs
	}

	resv.InReq = &wwire.SplicingInRequest{
		MsgHeader:          wwire.NewMsgHeader(),
		ChannelId:          channel.ChannelId,
		CommitHeight:       channel.CommitHeight,
		AssetName:          resv.AssetName.String(),
		Amt:                resv.Amt.String(),
		Utxos:              []string{utxo},
		Fees:               stubUtxos,
		FeeRate:            resv.FeeRate,
		RevealKey:          resv.RevealPrivKey,
		NeedSendSplicingTx: resv.NeedSendSplicingTx,
		Reason:             reason,
		Memo:               memo,
	}
	msg, err := json.Marshal(resv.InReq)
	if err != nil {
		return "", nil, 0, err
	}
	resv.ReqSig, err = resv.LocalWallet().SignMessage(msg)
	if err != nil {
		return "", nil, 0, err
	}

	for {
		channelID := resv.Channel.ChannelId
		if channel.PeerRPC == nil {
			err = fmt.Errorf("peer rpc is not initialized")
			break
		}
		err = p.sendSplicingInReqWithRetry(channel, &resv)
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

		err = p.ReceiveRevocation(&resv.RevocationInfo, resv.RemoteRev)
		if err != nil {
			Log.Errorf("ReceiveRevocation %s failed. %v", channelID, err)
			break
		}

		err = p.ReceiveNewCommitment(&resv.RevocationInfo, &resv.LocalCommitInfo.CommitSigInfo, nil)
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
				Log.Errorf("SendSplicingInRevokeAndAckReq %s failed. %v", channelID, err)
			} else {
				Log.Warnf("SendSplicingInRevokeAndAckReq %s result is unknown. Abort local expand state advance and wait for peer sync. %v", channelID, err)
			}
			break
		}

		resv.Status = RS_SPLICINGIN_STARTED
		resv.Channel.CommitHeight += 1
		resv.Channel.UpdateTime = resv.Id
		if err = SaveReservation(p.db, &resv.SplicingDataInDB); err != nil {
			Log.Errorf("SaveReservation failed. %v", err)
			break
		}
		if err = p.saveChannelToDB(resv.Channel); err != nil {
			Log.Errorf("saveChannelToDB failed. %v", err)
			break
		}
		p.addResv(&resv)
		_ = p.HandleSplicingInStarted(&resv)
		break
	}

	channel.ResvId = 0
	anchorTxID := ""
	if err == nil {
		anchorTxID = resv.AnchorTxId()
		Log.Infof("expand anchor TxId: %s", anchorTxID)
	} else {
		_ = p.saveChannelToDB(resv.OldChannel)
		p.enableChannel(resv.OldChannel)
		p.DelResvWithId(resv.Id)
		if resv.Id != 0 && channel.PeerRPC != nil {
			_ = channel.PeerRPC.SendActionResultNfty(resv.Id, RESV_TYPE_SPLICING, -1, err.Error())
		}
	}
	return anchorTxID, resv.Amt.Clone(), resv.Id, err
}

func (p *Manager) ExpandAssetInChannelSatsNet(channel *Channel, assetName *swire.AssetName) (*Decimal, error) {
	utxos, err := p.getAllUnmanagedUtxosSatsNet(channel)
	if err != nil {
		Log.Errorf("getAllUnmanagedUtxosSatsNet failed. %v", err)
		return nil, err
	}
	return p.FunderInitExpandingProcessSatsNet(channel, assetName, utxos)
}

func (p *Manager) FunderInitExpandingProcessSatsNet(channel *Channel, assetName *swire.AssetName, utxos []string) (*Decimal, error) {
	oldChannel, err := p.loadChannel(channel.ChannelId)
	if err != nil {
		return nil, err
	}
	tickerInfo := p.getTickerInfo(assetName)
	if tickerInfo == nil {
		return nil, fmt.Errorf("can't get ticker %s info", assetName)
	}

	var revealPrivKey []byte
	if channel.HasProtocolAsset(indexer.PROTOCOL_NAME_BRC20) {
		priv, err := btcec.NewPrivateKey()
		if err != nil {
			return nil, err
		}
		revealPrivKey = priv.Serialize()
	}

	resv := &PaymentReservation{
		PaymentDataInDB: PaymentDataInDB{
			ReservationBase: NewReservationBase(0, true, RS_INIT, channel.LocalWallet()),
			ChannelId:       channel.ChannelId,
			IsUnlock:        false,
			NeedSendLockTx:  false,
			AssetName:       GetAssetName(tickerInfo),
		},
		RevocationInfo: RevocationInfo{
			OldChannel:    oldChannel,
			Channel:       channel,
			FeeRate:       p.GetFeeRate(),
			RevealPrivKey: revealPrivKey,
		},
	}

	if err := p.AllowExpandSatsNet(resv, utxos); err != nil {
		return nil, fmt.Errorf("not allow lock, %v", err)
	}
	_ = p.SaveBackupChannelToDB(&resv.OldChannel.ChannelInDB)
	channelID := resv.ChannelId

	for {
		err = CreatePaymentTx(resv)
		if err != nil {
			Log.Errorf("UpdateUtxoL2 failed. %v", err)
			break
		}
		err = UpdateCommitWithPaymentResv(resv)
		if err != nil {
			Log.Errorf("UpdateCommitValue failed. %v", err)
			break
		}
		resv.LocalRevKey, err = p.GetCurrRevocationKey(channel)
		if err != nil {
			break
		}
		resv.LocalNextRevKey, err = p.GetNextRevocationKey(channel)
		if err != nil {
			break
		}

		resv.LockReq = &wwire.LockRequest{
			MsgHeader:      wwire.NewMsgHeader(),
			ChannelId:      channelID,
			CommitHeight:   channel.CommitHeight,
			AssetName:      resv.AssetName.String(),
			Amt:            resv.Amt.String(),
			FeeRate:        resv.FeeRate,
			LockUtxos:      utxos,
			FeeUtxos:       nil,
			RevealKey:      resv.RevealPrivKey,
			RevKey:         resv.LocalRevKey,
			NextRevKey:     resv.LocalNextRevKey,
			NeedSendLockTx: resv.NeedSendLockTx,
			Memo:           resv.Memo,
		}
		msg, err := json.Marshal(resv.LockReq)
		if err != nil {
			break
		}
		resv.ReqSig, err = resv.LocalWallet().SignMessage(msg)
		if err != nil {
			break
		}

		if channel.PeerRPC == nil {
			err = fmt.Errorf("peer rpc is not initialized")
			break
		}
		err = channel.PeerRPC.SendLockReq(resv)
		if err != nil {
			Log.Errorf("SendLockReq %s failed. %v", channelID, err)
			break
		}
		channel.ResvId = resv.Id

		err = p.ReceiveNewCommitment(&resv.RevocationInfo, &resv.LocalCommitInfo.CommitSigInfo, nil)
		if err != nil {
			Log.Errorf("ReceiveNewCommitment %s failed. %v", channelID, err)
			break
		}
		resv.LocalRev, err = p.RevokeCurrentCommitment(&resv.RevocationInfo)
		if err != nil {
			Log.Errorf("RevokeCurrentCommitment %s failed. %v", channelID, err)
			break
		}
		err = p.SignNextCommitment(&resv.RevocationInfo)
		if err != nil {
			Log.Errorf("SignNextCommitment %s failed. %v", channelID, err)
			break
		}

		err = channel.PeerRPC.SendLockCommitSigAndRevokeReq(resv)
		if err != nil {
			Log.Errorf("SendLockCommitSigAndRevokeReq %s failed. %v", channelID, err)
			break
		}

		err = p.ReceiveRevocation(&resv.RevocationInfo, resv.RemoteRev)
		if err != nil {
			Log.Errorf("ReceiveRevocation %s failed. %v", channelID, err)
			break
		}

		err = p.TestAcceptance_SatsNet([]*swire.MsgTx{resv.Channel.LocalCommitment.DeAnchorTx})
		if err != nil {
			Log.Errorf("TestAcceptance_SatsNet failed. %v", err)
			break
		}

		err = channel.PeerRPC.SendLockAckReq(resv)
		if err != nil {
			if !isBroadcastResultUnknown(err) {
				Log.Errorf("SendRevokeAndAckReq %s failed. %v", channelID, err)
			} else {
				Log.Warnf("SendRevokeAndAckReq %s result is unknown. Abort local lock-with-expand state advance and wait for peer sync. %v", channelID, err)
			}
			break
		}

		channel.CommitHeight += 1
		channel.UpdateTime = resv.Id
		_ = p.saveChannelToDB(channel)
		_ = p.HandlePaymentFinished(resv)
		break
	}

	channel.ResvId = 0
	if err != nil {
		p.enableChannel(resv.OldChannel)
		if resv.Id != 0 && channel.PeerRPC != nil {
			_ = channel.PeerRPC.SendActionResultNfty(resv.Id, RESV_TYPE_PAYMENT, -1, err.Error())
		}
		return nil, err
	}
	return resv.Amt, nil
}
