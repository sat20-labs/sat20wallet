package wallet

import (
	"encoding/json"
	"fmt"

	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func (p *Manager) InitUnlockProcess(resv *PaymentReservation, feeUtxos []string) (string, error) {
	err := p.AllowUnlock(resv, feeUtxos)
	if err != nil {
		return "", fmt.Errorf("not allow unlock, %v", err)
	}
	if err := p.SaveBackupChannelToDB(&resv.OldChannel.ChannelInDB); err != nil {
		return "", err
	}

	channelId := resv.ChannelId
	channel := resv.Channel

	destAmt := make([]string, 0, len(resv.DestAmt))
	for _, amt := range resv.DestAmt {
		destAmt = append(destAmt, amt.String())
	}

	resv.UnlockReq = &wwire.UnlockRequest{
		MsgHeader:    wwire.NewMsgHeader(),
		ChannelId:    channelId,
		CommitHeight: channel.CommitHeight,
		AssetName:    resv.AssetName.String(),
		Amt:          destAmt,
		FeeRate:      resv.FeeRate,
		FeeUtxos:     feeUtxos,
		DestAddr:     resv.DestAddr,
		Memo:         resv.Memo,
		Reason:       resv.Reason,
		MoreData:     resv.MoreData,
	}
	msg, err := json.Marshal(resv.UnlockReq)
	if err != nil {
		return "", err
	}
	resv.ReqSig, err = resv.LocalWallet().SignMessage(msg)
	if err != nil {
		return "", err
	}

	for {
		if channel.PeerRPC == nil {
			err = fmt.Errorf("peer rpc is not initialized")
			break
		}
		err = channel.PeerRPC.SendUnlockReq(resv)
		if err != nil {
			Log.Errorf("SendUnlockReq %s failed. %v", channelId, err)
			break
		}
		channel.ResvId = resv.Id

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
		err = p.SignNextCommitment(&resv.RevocationInfo)
		if err != nil {
			Log.Errorf("SignNextCommitment %s failed. %v", channelId, err)
			break
		}
		resv.LocalRevKey, err = p.GetCurrRevocationKey(channel)
		if err != nil {
			Log.Errorf("GetCurrRevocationKey %s failed. %v", channelId, err)
			break
		}
		resv.LocalNextRevKey, err = p.GetNextRevocationKey(channel)
		if err != nil {
			Log.Errorf("GetNextRevocationKey %s failed. %v", channelId, err)
			break
		}

		err = channel.PeerRPC.SendUnlockCommitSigReq(resv)
		if err != nil {
			Log.Errorf("SendCommitSigReq %s failed. %v", channelId, err)
			break
		}

		err = p.ReceiveRevocation(&resv.RevocationInfo, resv.RemoteRev)
		if err != nil {
			Log.Errorf("ReceiveRevocation %s failed. %v", channelId, err)
			break
		}

		err = p.ReceiveNewCommitment(&resv.RevocationInfo, &resv.LocalCommitInfo.CommitSigInfo, nil)
		if err != nil {
			Log.Errorf("ReceiveNewCommitment %s failed. %v", channelId, err)
			break
		}

		resv.LocalRev, err = p.RevokeCurrentCommitment(&resv.RevocationInfo)
		if err != nil {
			Log.Errorf("RevokeCurrentCommitment %s failed. %v", channelId, err)
			break
		}

		resv.LocalPaymentSig, err = PartialSignTxWithChannel_SatsNet(resv.Channel, resv.PaymentTx, resv.PaymentPreFetcher)
		if err != nil {
			Log.Errorf("PartialSignTx_SatsNet failed. %v", err)
			break
		}

		err = channel.PeerRPC.SendUnlockRevokeAndAckReq(resv)
		if err != nil {
			if !isBroadcastResultUnknown(err) {
				Log.Errorf("SendRevokeAndAckReq %s failed. %v", channelId, err)
			} else {
				Log.Warnf("SendRevokeAndAckReq %s result is unknown. Abort local unlock state advance and wait for peer sync. %v", channelId, err)
			}
			break
		}

		err = VerifyPaymentTx(resv)
		if err != nil {
			Log.Errorf("VerifyPaymentTx failed. %v", err)
			break
		}
		err = p.TestAcceptance_SatsNet([]*swire.MsgTx{resv.PaymentTx, resv.Channel.LocalCommitment.DeAnchorTx})
		if err != nil {
			Log.Errorf("TestAcceptance_SatsNet %s failed. %v", channelId, err)
			break
		}

		resv.Status = RS_PAYMENT_STARTED
		p.addResv(resv)
		err = SaveReservation(p.db, resv)
		if err != nil {
			Log.Errorf("saveReservation failed. %v", err)
			break
		}
		channel.CommitHeight += 1
		channel.UpdateTime = resv.Id
		p.saveChannelToDB(channel)

		go p.HandlePaymentStarted(resv)
		break
	}

	channel.ResvId = 0
	if err != nil {
		if resv.SignedPunishTx != nil && resv.OldChannel.RemoteCommitment != nil &&
			resv.OldChannel.RemoteCommitment.CommitTx != nil {
			p.GetWatchTower().RemoveCommitTx(resv.OldChannel, resv.OldChannel.RemoteCommitment.CommitTx.TxID())
		}
		p.saveChannelToDB(resv.OldChannel)
		p.enableChannel(resv.OldChannel)
		p.DelResvWithId(resv.Id)
		if resv.Id != 0 && channel.PeerRPC != nil {
			_ = channel.PeerRPC.SendActionResultNfty(resv.Id, RESV_TYPE_PAYMENT, -1, err.Error())
		}
		return "", err
	}

	return resv.PaymentTx.TxID(), nil
}

func (p *Manager) InitLockProcess(resv *PaymentReservation, utxos, fees []string) (string, error) {
	err := p.AllowLock(resv, utxos, fees, DEFAULT_FEE_SATSNET)
	if err != nil {
		return "", fmt.Errorf("not allow lock, %v", err)
	}
	if err := p.SaveBackupChannelToDB(&resv.OldChannel.ChannelInDB); err != nil {
		return "", err
	}

	channelId := resv.ChannelId
	channel := resv.Channel

	for {
		if channel.PeerRPC == nil {
			err = fmt.Errorf("peer rpc is not initialized")
			break
		}
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
			ChannelId:      channelId,
			CommitHeight:   channel.CommitHeight,
			AssetName:      resv.AssetName.String(),
			Amt:            resv.Amt.String(),
			FeeRate:        resv.FeeRate,
			LockUtxos:      utxos,
			FeeUtxos:       fees,
			RevealKey:      resv.RevealPrivKey,
			RevKey:         resv.LocalRevKey,
			NextRevKey:     resv.LocalNextRevKey,
			NeedSendLockTx: resv.NeedSendLockTx,
			Memo:           resv.Memo,
			Reason:         resv.Reason,
			MoreData:       resv.MoreData,
		}
		var msg []byte
		msg, err = json.Marshal(resv.LockReq)
		if err != nil {
			break
		}
		resv.ReqSig, err = resv.LocalWallet().SignMessage(msg)
		if err != nil {
			break
		}

		err = channel.PeerRPC.SendLockReq(resv)
		if err != nil {
			Log.Errorf("SendLockReq %s failed. %v", channelId, err)
			break
		}
		channel.ResvId = resv.Id

		err = p.ReceiveNewCommitment(&resv.RevocationInfo, &resv.LocalCommitInfo.CommitSigInfo, nil)
		if err != nil {
			Log.Errorf("ReceiveNewCommitment %s failed. %v", channelId, err)
			break
		}

		resv.LocalRev, err = p.RevokeCurrentCommitment(&resv.RevocationInfo)
		if err != nil {
			Log.Errorf("RevokeCurrentCommitment %s failed. %v", channelId, err)
			break
		}

		err = p.SignNextCommitment(&resv.RevocationInfo)
		if err != nil {
			Log.Errorf("SignNextCommitment %s failed. %v", channelId, err)
			break
		}

		err = channel.PeerRPC.SendLockCommitSigAndRevokeReq(resv)
		if err != nil {
			Log.Errorf("SendLockCommitSigAndRevokeReq %s failed. %v", channelId, err)
			break
		}

		err = p.ReceiveRevocation(&resv.RevocationInfo, resv.RemoteRev)
		if err != nil {
			Log.Errorf("ReceiveRevocation %s failed. %v", channelId, err)
			break
		}

		err = SignAndVerifyPaymentTx(resv)
		if err != nil {
			Log.Errorf("SignAndVerifyPaymentTx failed. %v", err)
			break
		}
		err = p.TestAcceptance_SatsNet([]*swire.MsgTx{resv.PaymentTx, resv.Channel.LocalCommitment.DeAnchorTx})
		if err != nil {
			Log.Errorf("TestAcceptance_SatsNet failed. %v", err)
			break
		}

		err = channel.PeerRPC.SendLockAckReq(resv)
		if err != nil {
			if !isBroadcastResultUnknown(err) {
				Log.Errorf("SendRevokeAndAckReq %s failed. %v", channelId, err)
			} else {
				Log.Warnf("SendRevokeAndAckReq %s result is unknown. Abort local lock state advance and wait for peer sync. %v", channelId, err)
			}
			break
		}

		resv.Status = RS_PAYMENT_STARTED
		p.addResv(resv)
		err = SaveReservation(p.db, resv)
		if err != nil {
			Log.Errorf("saveReservation failed. %v", err)
			break
		}
		channel.CommitHeight += 1
		channel.UpdateTime = resv.Id
		p.saveChannelToDB(channel)

		go p.HandlePaymentStarted(resv)
		break
	}

	channel.ResvId = 0
	if err != nil {
		if resv.SignedPunishTx != nil && resv.OldChannel.RemoteCommitment != nil &&
			resv.OldChannel.RemoteCommitment.CommitTx != nil {
			p.GetWatchTower().RemoveCommitTx(resv.OldChannel, resv.OldChannel.RemoteCommitment.CommitTx.TxID())
		}
		p.saveChannelToDB(resv.OldChannel)
		p.enableChannel(resv.OldChannel)
		p.DelResvWithId(resv.Id)
		if resv.Id != 0 && channel.PeerRPC != nil {
			_ = channel.PeerRPC.SendActionResultNfty(resv.Id, RESV_TYPE_PAYMENT, -1, err.Error())
		}
		return "", err
	}

	return resv.PaymentTx.TxID(), nil
}
