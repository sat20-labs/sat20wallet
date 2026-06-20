package wallet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	indexer "github.com/sat20-labs/indexer/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func (p *Manager) RecoverPaymentChannel(channelId, paymentTxId, reason string) (string, int64, int, error) {
	if !p.IsReady() {
		return "", 0, 0, fmt.Errorf("not ready")
	}
	channel := p.GetChannel(channelId)
	if channel == nil {
		return "", 0, 0, fmt.Errorf("can't find channel %s", channelId)
	}
	if channel.Status != CS_READY {
		return "", 0, 0, fmt.Errorf("channel %s is not ready", channelId)
	}
	if !channel.IsInitiator {
		return "", 0, 0, fmt.Errorf("can't perform this action from remote peer")
	}
	if !p.IsPeerOnline(channel.PeerNodeId) {
		return "", 0, 0, fmt.Errorf("peer is offline")
	}

	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()
	if channel.Status != CS_READY {
		return "", 0, 0, fmt.Errorf("channel %s is not ready", channelId)
	}
	if channel.IsBusy() {
		return "", 0, 0, fmt.Errorf("%s %s", CHANNEL_IS_BUSY, channelId)
	}
	if channel.PeerRPC == nil {
		return "", 0, 0, fmt.Errorf("peer rpc client is not initialized")
	}

	req := &wwire.RecoverPaymentRequest{
		MsgHeader:    wwire.NewMsgHeader(),
		ChannelId:    channelId,
		CommitHeight: channel.CommitHeight,
		PaymentTxId:  paymentTxId,
		Reason:       reason,
	}
	msg, err := json.Marshal(req)
	if err != nil {
		return "", 0, 0, err
	}
	sig, err := channel.LocalWallet().SignMessage(msg)
	if err != nil {
		return "", 0, 0, err
	}

	resv := &PaymentReservation{
		PaymentDataInDB: PaymentDataInDB{
			ReservationBase: NewReservationBase(0, true, RS_INIT, channel.LocalWallet()),
			ChannelId:       channelId,
			IsUnlock:        true,
			NeedSendLockTx:  true,
			AssetName:       &PLAIN_ASSET,
			Reason:          reason,
		},
		RevocationInfo: RevocationInfo{
			Channel: channel,
			FeeRate: p.GetFeeRate(),
		},
		RecoverPaymentReq: req,
		ReqSig:            sig,
	}
	resv.InitRuntime()

	err = channel.PeerRPC.SendRecoverPaymentReq(resv)
	if err != nil {
		return "", 0, 0, err
	}

	if channel.CommitHeight != req.CommitHeight {
		return "", 0, 0, fmt.Errorf("commit height changed. old %d current %d", req.CommitHeight, channel.CommitHeight)
	}

	err = p.PrepareRecoverPaymentReservationLocked(resv, paymentTxId)
	if err != nil {
		return "", 0, 0, err
	}
	if err := p.SaveBackupChannelToDB(&resv.OldChannel.ChannelInDB); err != nil {
		return "", 0, 0, err
	}
	channel.ResvId = resv.Id
	p.AddResv(resv)

	err = UpdateCommitWithPaymentResv(resv)
	if err != nil {
		return "", 0, 0, err
	}
	err = p.SignNextCommitment(&resv.RevocationInfo)
	if err != nil {
		return "", 0, 0, err
	}
	resv.LocalRevKey, err = p.GetCurrRevocationKey(channel)
	if err != nil {
		return "", 0, 0, err
	}
	resv.LocalNextRevKey, err = p.GetNextRevocationKey(channel)
	if err != nil {
		return "", 0, 0, err
	}
	err = channel.PeerRPC.SendRecoverPaymentCommitSigReq(resv)
	if err != nil {
		return "", 0, 0, err
	}
	err = p.ReceiveRevocation(&resv.RevocationInfo, resv.RemoteRev)
	if err != nil {
		return "", 0, 0, err
	}
	err = p.ReceiveNewCommitment(&resv.RevocationInfo, &resv.LocalCommitInfo.CommitSigInfo, nil)
	if err != nil {
		return "", 0, 0, err
	}
	resv.LocalRev, err = p.RevokeCurrentCommitment(&resv.RevocationInfo)
	if err != nil {
		return "", 0, 0, err
	}
	resv.LocalPaymentSig, err = PartialSignTxWithChannel_SatsNet(resv.Channel, resv.PaymentTx, resv.PaymentPreFetcher)
	if err != nil {
		return "", 0, 0, err
	}
	err = channel.PeerRPC.SendRecoverPaymentRevokeAndAckReq(resv)
	if err != nil {
		return "", 0, 0, err
	}
	err = VerifyPaymentTx(resv)
	if err != nil {
		return "", 0, 0, err
	}
	err = p.TestAcceptance_SatsNet([]*swire.MsgTx{resv.Channel.LocalCommitment.DeAnchorTx})
	if err != nil {
		return "", 0, 0, err
	}
	err = p.FinalizeRecoveredPaymentLocked(resv)
	if err != nil {
		return "", 0, 0, err
	}

	return paymentTxId, resv.Id, channel.CommitHeight, nil
}

func (p *Manager) PrepareRecoverPaymentReservationLocked(resv *PaymentReservation, paymentTxId string) error {
	info, err := p.GetIndexerRPCClient_SatsNet().GetTxInfo(paymentTxId)
	if err != nil {
		return fmt.Errorf("GetTxInfo %s failed: %v", paymentTxId, err)
	}
	if info == nil || info.Confirmations == 0 {
		return fmt.Errorf("payment tx %s is not confirmed", paymentTxId)
	}
	rawTx, err := p.GetIndexerRPCClient_SatsNet().GetRawTx(paymentTxId)
	if err != nil {
		return fmt.Errorf("GetRawTx %s failed: %v", paymentTxId, err)
	}
	paymentTx, err := DecodeMsgTx_SatsNet(rawTx)
	if err != nil {
		return err
	}
	if paymentTx.TxID() != paymentTxId {
		return fmt.Errorf("payment tx id mismatch. expected %s got %s", paymentTxId, paymentTx.TxID())
	}

	channel := resv.Channel
	if channel == nil {
		return fmt.Errorf("nil channel")
	}
	if channel.Status != CS_READY {
		return fmt.Errorf("channel %s is not ready", channel.ChannelId)
	}
	oldChannel, err := p.LoadChannel(channel.ChannelId)
	if err != nil {
		return err
	}

	prefetcher := stxscript.NewMultiPrevOutFetcher(nil)
	channelInputs := make([]*TxOutput_SatsNet, 0)
	feeInputs := make([]*TxOutput_SatsNet, 0)
	spentChannelInput := false
	for _, txIn := range paymentTx.TxIn {
		outpoint := txIn.PreviousOutPoint.String()
		if output, ok := channel.UtxosL2[outpoint]; ok {
			channelInputs = append(channelInputs, output.Clone())
			prefetcher.AddPrevOut(*output.OutPoint(), &output.OutValue)
			spentChannelInput = true
			continue
		}
		output, err := p.recoverSatsNetPrevOutput(outpoint)
		if err != nil {
			return err
		}
		if bytes.Equal(output.OutValue.PkScript, channel.GetChannelPkScript()) {
			return fmt.Errorf("payment input %s belongs to channel but is not managed by current channel state", outpoint)
		}
		feeInputs = append(feeInputs, output)
		prefetcher.AddPrevOut(*output.OutPoint(), &output.OutValue)
	}
	if !spentChannelInput {
		return fmt.Errorf("payment tx %s does not spend current channel L2 utxo", paymentTxId)
	}
	if len(paymentTx.TxOut) == 0 || !bytes.Equal(paymentTx.TxOut[0].PkScript, channel.GetChannelPkScript()) {
		return fmt.Errorf("payment tx %s does not create channel output at vout 0", paymentTxId)
	}

	destAmt, destAddr, err := extractPlainUnlockDest(paymentTx, channel)
	if err != nil {
		return err
	}
	if destAmt.Sign() <= 0 {
		return fmt.Errorf("invalid recovered unlock amount %s", destAmt.String())
	}

	resv.OldChannel = oldChannel
	resv.PaymentTx = paymentTx
	resv.PaymentPreFetcher = prefetcher
	resv.Utxos = channelInputs
	resv.Fees = feeInputs
	resv.AssetName = &PLAIN_ASSET
	resv.DestAmt = []*Decimal{destAmt}
	resv.DestAddr = []string{destAddr}
	resv.Amt = nil
	resv.IsUnlock = true
	resv.NeedSendLockTx = true
	channel.LastPaymentTxId = paymentTxId
	channel.UpdateUtxosByPendingTx_SatsNet(paymentTx)

	return nil
}

func (p *Manager) FinalizeRecoveredPaymentLocked(resv *PaymentReservation) error {
	resv.Channel.EnableUtxo_SatsNet(resv.PaymentTx.TxID())
	resv.Status = RS_CLOSED
	if err := p.SaveWalletReservation(resv); err != nil {
		return err
	}
	resv.Channel.CommitHeight += 1
	resv.Channel.UpdateTime = resv.Id
	resv.Channel.ResvId = 0
	if err := p.SaveChannelToDB(resv.Channel); err != nil {
		return err
	}
	p.SendMessageToUpper(MSG_UTXO_UNLOCKED_LOCKED, resv.ChannelId)
	p.DelResvWithId(resv.Id)
	return nil
}

func (p *Manager) recoverSatsNetPrevOutput(outpoint string) (*TxOutput_SatsNet, error) {
	txid, vout, err := parseOutPoint(outpoint)
	if err != nil {
		return nil, err
	}
	rawTx, err := p.GetIndexerRPCClient_SatsNet().GetRawTx(txid)
	if err != nil {
		return nil, fmt.Errorf("GetRawTx %s failed: %v", txid, err)
	}
	tx, err := DecodeMsgTx_SatsNet(rawTx)
	if err != nil {
		return nil, err
	}
	if vout < 0 || vout >= len(tx.TxOut) {
		return nil, fmt.Errorf("invalid outpoint %s", outpoint)
	}
	return sindexer.GenerateTxOutput(tx, vout), nil
}

func parseOutPoint(outpoint string) (string, int, error) {
	txid, voutRaw, ok := bytes.Cut([]byte(outpoint), []byte(":"))
	if !ok {
		return "", 0, fmt.Errorf("invalid outpoint %s", outpoint)
	}
	vout, err := strconv.Atoi(string(voutRaw))
	if err != nil {
		return "", 0, fmt.Errorf("invalid outpoint %s: %v", outpoint, err)
	}
	return string(txid), vout, nil
}

func extractPlainUnlockDest(tx *swire.MsgTx, channel *Channel) (*Decimal, string, error) {
	channelPkScript := channel.GetChannelPkScript()
	var destOut *swire.TxOut
	for i := 1; i < len(tx.TxOut); i++ {
		txOut := tx.TxOut[i]
		if bytes.Equal(txOut.PkScript, channelPkScript) || IsNullDataScript(txOut.PkScript) {
			continue
		}
		if len(txOut.Assets) != 0 {
			return nil, "", fmt.Errorf("only plain-sats confirmed payment recovery is supported")
		}
		if txOut.Value == 0 {
			continue
		}
		if destOut != nil {
			return nil, "", fmt.Errorf("ambiguous recovered unlock outputs")
		}
		destOut = txOut
	}
	if destOut == nil {
		return nil, "", fmt.Errorf("can't find recovered unlock destination output")
	}
	return indexer.NewDefaultDecimal(destOut.Value), "", nil
}
