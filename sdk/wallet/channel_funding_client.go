package wallet

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
)

func (p *Manager) funderProcessAcceptChannel(resv *FundingReservation) error {
	if resv == nil || resv.Accept == nil || resv.Channel == nil {
		return fmt.Errorf("invalid funding reservation")
	}
	if p.serverNode == nil || p.serverNode.Pubkey == nil {
		return fmt.Errorf("server node is not configured")
	}

	msg := resv.Accept
	if !bytes.Equal(msg.FundingKey, p.serverNode.Pubkey.SerializeCompressed()) {
		return fmt.Errorf("server node use a different wallet public key %s %s",
			hex.EncodeToString(msg.FundingKey), hex.EncodeToString(p.serverNode.Pubkey.SerializeCompressed()))
	}

	remoteFundingKey, err := utils.BytesToPublicKey(msg.FundingKey)
	if err != nil {
		return err
	}
	remoteRevocationBasePoint, err := utils.BytesToPublicKey(msg.RevocationBasePoint)
	if err != nil {
		return err
	}

	resv.Channel.CommitHeight = msg.CommitHeight
	resv.Channel.FundingTime = msg.Id
	resv.Channel.RemoteChanCfg.PaymentKey = remoteFundingKey
	resv.Channel.RemoteChanCfg.RevocationBasePoint = remoteRevocationBasePoint
	resv.Channel.CsvDelay = msg.CsvDelay
	resv.Channel.Address, err = GetP2WSHaddress(
		resv.Channel.LocalChanCfg.PaymentKey.SerializeCompressed(),
		resv.Channel.RemoteChanCfg.PaymentKey.SerializeCompressed(),
	)
	if err != nil {
		return err
	}
	resv.ChannelId = resv.Channel.Address
	resv.Channel.ChannelId = resv.Channel.Address

	var redeemScript []byte
	var chanPoint *TxOutput
	daoPkScript := p.GetDAOPkScript(resv.Channel)
	if resv.NeedSendFundingTx {
		changePkScript, err := GetP2TRpkScript(resv.Channel.LocalChanCfg.PaymentKey)
		if err != nil {
			return err
		}
		fundingTx, script, preFetcher, err := CreateFundingTx2(
			resv.FundingUtxos,
			resv.Req.FundingKey,
			resv.Accept.FundingKey,
			resv.Req.LocalFundingAmount,
			resv.Req.FeeRate,
			changePkScript,
			daoPkScript,
			resv.Channel.FeeCfg,
		)
		if err != nil {
			return err
		}
		redeemScript = script
		fundingTx, err = SignAndVerifyFundingTx(resv.LocalWallet(), fundingTx, preFetcher)
		if err != nil {
			return err
		}
		resv.FundingTx = fundingTx
		chanPoint = indexer.GenerateTxOutput(fundingTx, 0)
	} else {
		chanPoint = resv.FundingUtxos[0]
		redeemScript, _, err = GetP2WSHscript(msg.FundingKey, resv.LocalWallet().GetPaymentPubKey().SerializeCompressed())
		if err != nil {
			return err
		}
	}

	resv.Channel.ChanPoint = chanPoint
	resv.Channel.RedeemScript = redeemScript
	resv.Channel.LocalChanCfg.InitialBalance = resv.Channel.Capacity - msg.OpenFee.MinReserveSats
	resv.Channel.RemoteChanCfg.InitialBalance = msg.OpenFee.MinReserveSats

	invoice, err := sindexer.StandardAnchorScript(
		chanPoint.OutPointStr,
		redeemScript,
		chanPoint.Value(),
		chanPoint.Assets,
	)
	if err != nil {
		return err
	}
	if !VerifyMessage(resv.Channel.RemoteChanCfg.PaymentKey, invoice, msg.InvoiceSig) {
		return fmt.Errorf("VerifyMessage failed")
	}

	if resv.SkipOpeningAnchorTx {
		if err := p.AddExistingOpeningAnchorOutput(resv); err != nil {
			return err
		}
	} else {
		anchorTx := CreateOpeningAnchorTx(
			resv.Channel,
			resv.Channel.ChanPoint,
			resv.Channel.LocalChanCfg.InitialBalance,
			resv.Channel.RemoteChanCfg.InitialBalance,
			msg.InvoiceSig,
			daoPkScript,
		)
		if anchorTx == nil {
			return fmt.Errorf("can't generate anchor tx")
		}
		resv.AnchorTx = anchorTx
		resv.Channel.AddUtxo_SatsNet(sindexer.GenerateTxOutput(anchorTx, 0))
	}

	bootstrapKey := p.GetBootstrapNodePaymentPubKey()
	commitPoint, err := utils.BytesToPublicKey(msg.CommitmentPoint)
	if err != nil {
		return err
	}
	keyRing := DeriveCommitmentKeys(commitPoint, 1, bootstrapKey, nil, resv.Channel)

	resv.Channel.RemoteCommitment = NewChannelCommitment()
	resv.Channel.LocalCommitment = NewChannelCommitment()
	resv.Channel.SetCommitLocalValue(&PLAIN_ASSET, indexer.NewDefaultDecimal(resv.Channel.LocalChanCfg.InitialBalance))
	resv.Channel.SetCommitRemoteValue(&PLAIN_ASSET, indexer.NewDefaultDecimal(resv.Channel.RemoteChanCfg.InitialBalance))

	remoteCommitTx, _, _, err := p.CreateCommitTx(1, resv.Channel, keyRing, bootstrapKey, resv.Req.FeeRate)
	if err != nil {
		return err
	}
	remoteCommitSig, _, _, err := PartialSignCommitTx(resv.Channel, remoteCommitTx, nil, nil)
	if err != nil {
		return err
	}
	resv.Channel.RemoteCommitment.CommitTx = remoteCommitTx
	resv.Channel.RemoteCommitment.Revocation = nil
	resv.Channel.RemoteCommitment.CommitSig = remoteCommitSig

	remoteDeAnchorTx, prefetcher, err := CreateClosingDeAnchorTx(resv.Channel, remoteCommitTx.TxID(), daoPkScript)
	if err != nil {
		return err
	}
	remoteDeAnchorSig, err := PartialSignTxWithChannel_SatsNet(resv.Channel, remoteDeAnchorTx, prefetcher)
	if err != nil {
		return err
	}
	resv.Channel.RemoteCommitment.DeAnchorTx = remoteDeAnchorTx
	resv.Channel.RemoteCommitment.DeAnchorSig = remoteDeAnchorSig

	localCommitPoint := resv.LocalWallet().GetCommitSecret(resv.Channel.PeerNodeId, uint32(resv.Channel.CommitHeight))
	resv.FundingCreated = &wwire.FundingCreated{
		Id:                  resv.Id,
		FundingPoint:        resv.Channel.ChanPoint.OutPointStr,
		RevocationBasePoint: resv.LocalWallet().GetRevocationBaseKey().SerializeCompressed(),
		CommitmentPoint:     localCommitPoint.PubKey().SerializeCompressed(),
		CommitSig:           remoteCommitSig,
		DeAnchorSig:         remoteDeAnchorSig,
	}
	return nil
}

func (p *Manager) FunderProcessAcceptChannel(resv *FundingReservation) error {
	return p.funderProcessAcceptChannel(resv)
}

func (p *Manager) funderProcessFundingSigned(resv *FundingReservation) error {
	if resv == nil || resv.FundingSigned == nil || resv.Channel == nil {
		return fmt.Errorf("invalid funding reservation")
	}

	bootstrapKey := p.GetBootstrapNodePaymentPubKey()
	commitPoint := resv.LocalWallet().GetCommitSecret(resv.Channel.PeerNodeId, uint32(resv.Channel.CommitHeight))
	keyRing := DeriveCommitmentKeys(commitPoint.PubKey(), 0, bootstrapKey, nil, resv.Channel)

	localCommitTx, inscribes, next, err := p.CreateCommitTx(0, resv.Channel, keyRing, bootstrapKey, resv.Req.FeeRate)
	if err != nil {
		return err
	}
	var prevSignedTx []*wire.MsgTx
	if resv.FundingTx != nil {
		prevSignedTx = []*wire.MsgTx{resv.FundingTx}
	}
	if err := p.SignAndVerifyCommitTx(resv.Channel, prevSignedTx, localCommitTx, resv.FundingSigned.CommitSig,
		inscribes, nil, next, nil, true); err != nil {
		return err
	}
	resv.Channel.LocalCommitment.CommitTx = localCommitTx
	resv.Channel.LocalCommitment.Revocation = nil
	resv.Channel.LocalCommitment.CommitSig = resv.FundingSigned.CommitSig

	deAnchorTx, prefetcher, err := CreateClosingDeAnchorTx(resv.Channel, localCommitTx.TxID(), p.GetDAOPkScript(resv.Channel))
	if err != nil {
		return err
	}
	if _, err := SignAndVerifyTxWithChannel_SatsNet(resv.Channel, deAnchorTx, prefetcher, resv.FundingSigned.DeAnchorSig); err != nil {
		return err
	}
	resv.Channel.LocalCommitment.DeAnchorTx = deAnchorTx
	resv.Channel.LocalCommitment.DeAnchorSig = resv.FundingSigned.DeAnchorSig
	resv.LocalDeAnchorPreFetcher = prefetcher

	return nil
}

func (p *Manager) FunderProcessFundingSigned(resv *FundingReservation) error {
	return p.funderProcessFundingSigned(resv)
}

func (p *Manager) FunderInitFundingProcess(feeRate, amt int64, utxos []string, memo string, l2DrainTxId string) (string, error) {
	channelID, err := p.GetChannelAddress()
	if err != nil {
		return "", err
	}
	if err := p.rejectUnfinishedChannelLifecycle(channelID); err != nil {
		return "", err
	}
	peerWallet := p.GetServerWalletAddress()
	if c := p.GetChannelByPeerWallet(peerWallet); c != nil {
		return "", fmt.Errorf("channel exists")
	}
	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}
	if len(utxos) == 0 {
		feeInfo, err := p.GetChannelOpenFeeInServer()
		if err != nil {
			return "", err
		}
		if feeInfo == nil || feeInfo.OpenFee == nil {
			return "", fmt.Errorf("server returned empty channel fee config")
		}
		utxos, err = p.SelectWalletFundingOutpoints(feeRate, amt, NewFromOpenChannelFee(feeInfo.OpenFee))
		if err != nil {
			return "", err
		}
	}

	logID := p.beginOperationLogBestEffort(OperationLogCreate{
		Category: "channel",
		Action:   "open_channel",
		Title:    "Open channel",
		Summary:  "Preparing channel opening",
		Parameters: map[string]string{
			"amount":   strconv.FormatInt(amt, 10),
			"fee_rate": strconv.FormatInt(feeRate, 10),
			"memo":     memo,
		},
	})

	resv, err := p.InitInitiatorFundingReservation(FundingInitOptions{
		FeeRate:           feeRate,
		Amount:            amt,
		Outpoints:         utxos,
		Memo:              memo,
		NeedSendFundingTx: true,
		L2DrainTxId:       l2DrainTxId,
	})
	if err != nil {
		p.updateOperationLogBestEffort(logID, OperationLogUpdate{Status: OperationLogFailed, Message: err.Error(), Details: map[string]string{"error": err.Error()}})
		return "", err
	}

	for {
		if err = p.serverNode.client.SendOpenChannelReq(resv); err != nil {
			break
		}
		p.bindOperationLogReservationBestEffort(logID, RESV_TYPE_OPEN, resv.Id)
		p.updateOperationLogBestEffort(logID, OperationLogUpdate{
			Status:  OperationLogRunning,
			Message: "Peer accepted channel request",
			Details: map[string]string{"reservation_id": strconv.FormatInt(resv.Id, 10)},
		})
		resv.Channel.FeeCfg = NewFromOpenChannelFee(resv.Accept.OpenFee)
		resv.Channel.Capacity = amt - resv.Channel.FeeCfg.FeeToDAO()
		resv.FundingUtxos, err = p.AllowOpen(feeRate, amt, utxos, resv.Channel.FeeCfg)
		if err != nil {
			break
		}
		if err = p.funderProcessAcceptChannel(resv); err != nil {
			break
		}
		if err = p.serverNode.client.SendFundingCreatedReq(resv); err != nil {
			break
		}
		if err = p.funderProcessFundingSigned(resv); err != nil {
			break
		}
		if _, err = p.BroadcastTxsIrreversibleL1([]*wire.MsgTx{resv.FundingTx}, "open funding"); err != nil {
			break
		}

		fundingTxID := resv.FundingTx.TxID()
		p.updateOperationLogBestEffort(logID, OperationLogUpdate{
			Status:  OperationLogRunning,
			Message: "Funding transaction broadcast; waiting for confirmation",
			TxID:    fundingTxID,
			Details: map[string]string{"txid": fundingTxID},
		})

		resv.Channel.Status = CS_FUNDING_BROADCASTED
		resv.Status = ResvStatus(resv.Channel.Status)
		resv.Channel.ResvId = 0
		resv.FundingBroadcasted = &wwire.FundingBroadcasted{
			Id:          resv.Id,
			FundingTxId: fundingTxID,
		}
		resv.Channel.StaticMerkleRoot = resv.Channel.CalcStaticMerkleRoot()
		resv.Channel.UpdateTime = resv.Id

		p.AddResv(resv)
		if err = p.SaveWalletReservation(resv); err != nil {
			break
		}
		if err = p.SaveChannelToDB(resv.Channel); err != nil {
			break
		}
		p.AddChannelToNode(resv.Channel)
		p.SendFundingBroadcastedReq(resv)
		break
	}

	if err != nil {
		p.updateOperationLogBestEffort(logID, OperationLogUpdate{Status: OperationLogFailed, Message: err.Error(), Details: map[string]string{"error": err.Error()}})
		p.DelResvWithId(resv.Id)
		if resv.Id != 0 && p.serverNode != nil && p.serverNode.client != nil {
			_ = p.serverNode.client.SendActionResultNfty(resv.Id, RESV_TYPE_OPEN, -1, err.Error())
		}
		return "", err
	}
	return resv.Channel.ChannelId, nil
}

func (p *Manager) FunderInitReOpenProcess(amt int64, fundingUtxo *TxOutput, memo string, needSendFundingTx bool,
	skipOpeningAnchorTx bool, fundingFeeCfg *ChannelFeeConfig, l2DrainTxId string) (string, error) {
	channelID, err := p.GetChannelAddress()
	if err != nil {
		return "", err
	}
	if err := p.rejectUnfinishedChannelLifecycle(channelID); err != nil {
		return "", err
	}
	peerWallet := p.GetServerWalletAddress()
	if c := p.GetChannelByPeerWallet(peerWallet); c != nil {
		return "", fmt.Errorf("channel exists")
	}

	feeRate := p.GetFeeRate()
	outpoints := make([]string, 0)
	if needSendFundingTx {
		var err error
		outpoints, err = p.SelectWalletFundingOutpoints(feeRate, amt, fundingFeeCfg)
		if err != nil {
			return "", err
		}
	}

	action := "reopen_channel"
	title := "Reopen channel"
	if memo == "rebuild" {
		action = "rebuild_channel"
		title = "Rebuild channel"
	}
	logID := p.beginOperationLogBestEffort(OperationLogCreate{
		Category: "channel",
		Action:   action,
		Title:    title,
		Summary:  "Preparing channel recovery",
		Parameters: map[string]string{
			"amount":               strconv.FormatInt(amt, 10),
			"fee_rate":             strconv.FormatInt(feeRate, 10),
			"new_funding_tx":       strconv.FormatBool(needSendFundingTx),
			"reuse_opening_anchor": strconv.FormatBool(skipOpeningAnchorTx),
		},
	})

	resv, err := p.InitInitiatorFundingReservation(FundingInitOptions{
		FeeRate:             feeRate,
		Amount:              amt,
		Outpoints:           outpoints,
		FundingUtxo:         fundingUtxo,
		Memo:                memo,
		NeedSendFundingTx:   needSendFundingTx,
		SkipOpeningAnchorTx: skipOpeningAnchorTx,
		L2DrainTxId:         l2DrainTxId,
		InitialCapacity:     amt,
	})
	if err != nil {
		p.updateOperationLogBestEffort(logID, OperationLogUpdate{Status: OperationLogFailed, Message: err.Error(), Details: map[string]string{"error": err.Error()}})
		return "", err
	}

	for {
		if err = p.serverNode.client.SendOpenChannelReq(resv); err != nil {
			break
		}
		p.bindOperationLogReservationBestEffort(logID, RESV_TYPE_OPEN, resv.Id)
		p.updateOperationLogBestEffort(logID, OperationLogUpdate{
			Status:  OperationLogRunning,
			Message: "Peer accepted channel recovery request",
			Details: map[string]string{"reservation_id": strconv.FormatInt(resv.Id, 10)},
		})
		resv.Channel.FeeCfg = NewFromOpenChannelFee(resv.Accept.OpenFee)
		if resv.NeedSendFundingTx {
			resv.Channel.Capacity = amt - resv.Channel.FeeCfg.FeeToDAO()
			resv.FundingUtxos, err = p.AllowOpen(feeRate, amt, outpoints, resv.Channel.FeeCfg)
			if err != nil {
				break
			}
		}
		if err = p.funderProcessAcceptChannel(resv); err != nil {
			break
		}
		if err = p.serverNode.client.SendFundingCreatedReq(resv); err != nil {
			break
		}
		if err = p.funderProcessFundingSigned(resv); err != nil {
			break
		}
		if resv.NeedSendFundingTx {
			if _, err = p.BroadcastTxsIrreversibleL1([]*wire.MsgTx{resv.FundingTx}, "reopen funding"); err != nil {
				break
			}
			resv.Channel.Status = CS_FUNDING_BROADCASTED
		} else {
			resv.Channel.Status = CS_FUNDING_CONFIRMED
		}
		resv.Status = ResvStatus(resv.Channel.Status)
		resv.Channel.ResvId = 0

		fundingTxId := ""
		if resv.NeedSendFundingTx {
			fundingTxId = resv.FundingTx.TxID()
		} else if fundingUtxo != nil {
			fundingTxId = fundingUtxo.TxID()
		}
		message := "Existing funding output accepted; preparing channel"
		if resv.NeedSendFundingTx {
			message = "Funding transaction broadcast; waiting for confirmation"
		}
		p.updateOperationLogBestEffort(logID, OperationLogUpdate{
			Status:  OperationLogRunning,
			Message: message,
			TxID:    fundingTxId,
			Details: map[string]string{"txid": fundingTxId},
		})

		resv.FundingBroadcasted = &wwire.FundingBroadcasted{
			Id:          resv.Id,
			FundingTxId: fundingTxId,
		}
		resv.Channel.StaticMerkleRoot = resv.Channel.CalcStaticMerkleRoot()
		resv.Channel.UpdateTime = resv.Id

		p.AddResv(resv)
		if err = p.SaveWalletReservation(resv); err != nil {
			break
		}
		if err = p.SaveChannelToDB(resv.Channel); err != nil {
			break
		}
		p.AddChannelToNode(resv.Channel)
		p.SendFundingBroadcastedReq(resv)
		break
	}

	if err != nil {
		p.updateOperationLogBestEffort(logID, OperationLogUpdate{Status: OperationLogFailed, Message: err.Error(), Details: map[string]string{"error": err.Error()}})
		p.DelResvWithId(resv.Id)
		if resv.Id != 0 && p.serverNode != nil && p.serverNode.client != nil {
			_ = p.serverNode.client.SendActionResultNfty(resv.Id, RESV_TYPE_OPEN, -1, err.Error())
		}
		return "", err
	}
	return resv.Channel.ChannelId, nil
}
