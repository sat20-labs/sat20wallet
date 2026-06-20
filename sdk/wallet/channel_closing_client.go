package wallet

import (
	"encoding/json"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func (p *Manager) IsCoreChannel(channel *Channel) bool {
	if p.IsBootstrapNode() {
		return true
	}
	if p.ServerIsBootstrapNode() {
		return channel != nil && channel.IsInitiator
	}
	return false
}

func (p *Manager) AllowClose(resv *ClosingReservation) error {
	if p.IsCoreChannel(resv.Channel) {
		return fmt.Errorf("can't close core channel rightnow")
	}
	return nil
}

func (p *Manager) CreateCoopCloseTx(channel *Channel, toDAOPkScript, revealKey []byte,
	feeRate int64) (*wire.MsgTx, []*InscribeResv, error) {

	remotePubKey := channel.RemoteChanCfg.PaymentKey
	localPubKey := channel.LocalChanCfg.PaymentKey

	toLocalScript, err := GetP2TRpkScript(localPubKey)
	if err != nil {
		return nil, nil, err
	}

	toRemoteScript, err := GetP2TRpkScript(remotePubKey)
	if err != nil {
		return nil, nil, err
	}

	var localBalance, remoteBalance map[AssetName]*Decimal
	if channel.IsInitiator {
		localBalance = channel.GetCommitLocalBalance()
		remoteBalance = channel.GetCommitRemoteBalance()
	} else {
		localBalance = channel.GetCommitRemoteBalance()
		remoteBalance = channel.GetCommitLocalBalance()
		toLocalScript = toRemoteScript
	}
	toRemoteScript = toDAOPkScript

	var weightEstimate utils.TxWeightEstimator
	closingTx := wire.NewMsgTx(2)

	brc20Outputs := channel.GetFundingOutputWithProtocol(indexer.PROTOCOL_NAME_BRC20)
	inscribes, brc20Stubs, _, _, err := p.HandleOutputsBrc20(closingTx, brc20Outputs, localBalance, remoteBalance,
		channel.GetStubUtxos(indexer.PROTOCOL_NAME_BRC20),
		toLocalScript, toRemoteScript, revealKey, &weightEstimate, feeRate, channel.ChannelId)
	if err != nil {
		Log.Errorf("HandleOutputsBrc20 failed, %v", err)
		return nil, nil, err
	}

	ordxOutputs := channel.GetFundingOutputWithProtocol(indexer.PROTOCOL_NAME_ORDX)
	if err := p.HandleOutputsOrdx(closingTx, ordxOutputs, localBalance, remoteBalance,
		channel.GetStubUtxos(indexer.PROTOCOL_NAME_ORDX), toLocalScript, toRemoteScript, &weightEstimate, channel.ChannelId); err != nil {
		Log.Errorf("HandleOutputsOrdx failed, %v", err)
		return nil, nil, err
	}

	runesOutputs := channel.GetFundingOutputWithProtocol(indexer.PROTOCOL_NAME_RUNES)
	plainSats, err := p.HandleOutputsRunes(closingTx, runesOutputs, localBalance, remoteBalance,
		channel.GetStubUtxos(indexer.PROTOCOL_NAME_RUNES), toLocalScript, toRemoteScript, &weightEstimate, channel.ChannelId)
	if err != nil {
		Log.Errorf("HandleOutputsRunes failed, %v", err)
		return nil, nil, err
	}

	plainSats += AddCommitmentPlainAndStubInputs(closingTx, channel, brc20Stubs, &weightEstimate)

	localPlainValue := localBalance[PLAIN_ASSET].Int64() + plainSats
	remotePlainValue := remoteBalance[PLAIN_ASSET].Int64()

	if remotePlainValue >= 330 {
		txOutRemote := &wire.TxOut{PkScript: toRemoteScript, Value: remotePlainValue}
		closingTx.AddTxOut(txOutRemote)
		weightEstimate.AddTxOutput(txOutRemote)
	}

	txOutLocal := &wire.TxOut{PkScript: toLocalScript, Value: localPlainValue}
	weightEstimate.AddTxOutput(txOutLocal)
	requiredFee := weightEstimate.Fee(feeRate)
	localPlainValue -= requiredFee
	if localPlainValue >= 330 {
		txOutLocal.Value = localPlainValue
		closingTx.AddTxOut(txOutLocal)
	}

	PrintJsonTx(closingTx, "co-closing")
	Log.Infof("closeTx(%d->%d): feeRate=%d, requiredFee=%d", len(closingTx.TxIn), len(closingTx.TxOut), feeRate, requiredFee)
	return closingTx, inscribes, nil
}

func (p *Manager) FunderProcessClosingSigned(resv *ClosingReservation) (*wwire.ClosingSigned, error) {
	remoteSig := resv.RemoteSigned

	closingTx, inscribes, err := p.CreateCoopCloseTx(resv.Channel, p.GetDAOPkScript(resv.Channel), resv.RevealPrivKey, resv.Req.FeeRate)
	if err != nil {
		return nil, err
	}
	resv.Inscribes = inscribes

	sig, preTxs, preLocalTxSigs, err := p.SignAndVerifyClosingTx(resv.Channel, closingTx, remoteSig.ClosgingSig,
		inscribes, remoteSig.PrevTxSig, true)
	if err != nil {
		Log.Errorf("VerifyClosingTx failed. %v", err)
		return nil, err
	}
	resv.CloseTx = closingTx
	resv.PreTxs = preTxs

	deAnchorTx, prefetcher, err := CreateClosingDeAnchorTx(resv.Channel, closingTx.TxID(), p.GetDAOPkScript(resv.Channel))
	if err != nil {
		return nil, err
	}
	deAnchorSig, err := SignAndVerifyTxWithChannel_SatsNet(resv.Channel, deAnchorTx, prefetcher, remoteSig.DeAnchorSig)
	if err != nil {
		return nil, err
	}
	resv.DeAnchorTx = deAnchorTx
	resv.DeAnchorPrefetcher = prefetcher

	if err := p.TestAcceptance_SatsNet([]*swire.MsgTx{deAnchorTx}); err != nil {
		Log.Errorf("TestAcceptance_SatsNet failed. %v", err)
		return nil, err
	}

	resv.LocalSigned = &wwire.ClosingSigned{
		Id:          resv.Id,
		DeAnchorSig: deAnchorSig,
		ClosgingSig: sig,
		PrevTxSig:   preLocalTxSigs,
	}
	return resv.LocalSigned, nil
}

func (p *Manager) CloserInitCoopCloseProcess(channelID string, feeRate int64) (string, string, error) {
	channel := p.getChannel(channelID)
	if channel == nil {
		return "", "", fmt.Errorf("can't find channel %s", channelID)
	}
	if !p.IsPeerOnline(channel.PeerNodeId) {
		return "", "", fmt.Errorf("peer is offline")
	}
	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()

	if err := p.SaveBackupChannelToDB(&channel.ChannelInDB); err != nil {
		return "", "", err
	}
	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		return "", "", err
	}

	resv := ClosingReservation{Channel: channel}
	resv.InitRuntime()
	resv.Status = RS_INIT
	resv.IsInitiator = true
	resv.SetLocalWallet(p.wallet.Clone())
	resv.WalletId = resv.LocalWallet().GetWalletId()
	resv.FeeRate = feeRate
	resv.RevealPrivKey = priv.Serialize()

	if err := p.AllowClose(&resv); err != nil {
		return "", "", err
	}

	resv.Req = &wwire.CloseChannelRequest{
		MsgHeader:    wwire.NewMsgHeader(),
		ChannelId:    resv.Channel.ChannelId,
		CommitHeight: resv.Channel.CommitHeight,
		FeeRate:      resv.FeeRate,
		RevealKey:    resv.RevealPrivKey,
	}
	msg, err := json.Marshal(resv.Req)
	if err != nil {
		return "", "", err
	}
	resv.ReqSig, err = resv.LocalWallet().SignMessage(msg)
	if err != nil {
		return "", "", err
	}

	for {
		if channel.PeerRPC == nil {
			err = fmt.Errorf("peer rpc is not initialized")
			break
		}
		err = channel.PeerRPC.SendCloseChannelReq(&resv)
		if err != nil {
			Log.Errorf("SendCloseChannelReq failed. %v", err)
			break
		}
		channel.ResvId = resv.Id

		if _, err = p.FunderProcessClosingSigned(&resv); err != nil {
			Log.Errorf("funderProcessClosingSigned failed. %v", err)
			break
		}

		err = channel.PeerRPC.SendClosingSignedReq(&resv)
		if err != nil {
			Log.Errorf("SendClosingSignedReq failed. %v", err)
			break
		}
		Log.Infof("splicing-out tx: %s", resv.SplicingOutTxId)

		resv.ClosingBroadcasted = &wwire.ClosingBroadcasted{
			Id:           resv.Id,
			DeAnchorTxId: resv.DeAnchorTx.TxID(),
		}
		resv.Channel.Status = CS_CLOSING_STARTED
		resv.Channel.UpdateTime = resv.Id
		resv.Channel.DeAnchorTx = resv.DeAnchorTx
		resv.Channel.ClosingTx = resv.CloseTx
		if err = SaveReservation(p.db, &resv.ClosingDataInDB); err != nil {
			break
		}
		if err = p.saveChannelToDB(resv.Channel); err != nil {
			break
		}

		p.addResv(&resv)
		p.disableChannel(resv.Channel)

		_, err = p.BroadcastTx_SatsNet(resv.DeAnchorTx)
		if err != nil {
			Log.Errorf("BroadCastTx fundingTX failed. %v", err)
			err = nil
		}

		p.SendClosingBroadcastedReq(&resv)
		resv.Channel.Status = CS_CLOSING_DEANCHOR_BROADCASTED
		_ = p.saveChannelToDB(resv.Channel)
		break
	}

	channel.ResvId = 0
	if err != nil {
		p.DelResvWithId(resv.Id)
		if resv.Id != 0 && channel.PeerRPC != nil {
			_ = channel.PeerRPC.SendActionResultNfty(resv.Id, RESV_TYPE_CLOSE, -1, err.Error())
		}
		return "", "", err
	}

	closeTxID := ""
	if resv.CloseTx != nil {
		closeTxID = resv.CloseTx.TxID()
	}
	Log.Infof("closechannel: closeTx %s, deAnchorTx %s", closeTxID, resv.Channel.DeAnchorTx.TxID())
	return closeTxID, resv.Channel.DeAnchorTx.TxID(), nil
}
