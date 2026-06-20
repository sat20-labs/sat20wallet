package wallet

import (
	"encoding/json"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

func signRPCMessage(localWallet common.Wallet, msg interface{}) ([]byte, error) {
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	return localWallet.SignMessage(data)
}

func (p *NodeClient) SendUnlockReservationReq(info *PaymentReservation) error {
	resp, err := p.SendChannelUnlockReq(&wwire.UnlockReq{
		UnlockRequest: *info.UnlockReq,
		Sig:           info.ReqSig,
	})
	if err != nil {
		return err
	}
	info.Id = resp.Id
	info.RevealPrivKey = resp.RevealKey
	info.RemoteNextRevKey = resp.NextRevKey
	info.RemoteRevKey = resp.RevKey
	info.FeeRate = resp.FeeRate
	return nil
}

func (p *NodeClient) SendUnlockReq(info *PaymentReservation) error {
	return p.SendUnlockReservationReq(info)
}

func (p *NodeClient) SendUnlockCommitSigReservationReq(info *PaymentReservation) error {
	req := &wwire.UnlockCommitSigReq{
		ChannelId:     info.Channel.ChannelId,
		Id:            info.Id,
		CommitSigInfo: info.RemoteCommitInfo.CommitSigInfo,
		RevKey:        info.LocalRevKey,
		NextRevKey:    info.LocalNextRevKey,
	}
	sig, err := signRPCMessage(info.LocalWallet(), req)
	if err != nil {
		return err
	}
	req.Sig = sig

	resp, err := p.SendChannelUnlockCommitSigReq(req)
	if err != nil {
		return err
	}
	info.RemoteRev = resp.Rev
	info.LocalCommitInfo.CommitSigInfo = resp.CommitSigInfo
	info.Channel.LocalCommitment.CommitSig = resp.CommitSig
	info.Channel.LocalCommitment.DeAnchorSig = resp.CommitDeAnchorSig
	return nil
}

func (p *NodeClient) SendUnlockCommitSigReq(info *PaymentReservation) error {
	return p.SendUnlockCommitSigReservationReq(info)
}

func (p *NodeClient) SendUnlockRevokeAndAckReservationReq(info *PaymentReservation) error {
	req := &wwire.UnlockRevokeAndAckReq{
		ChannelId: info.Channel.ChannelId,
		Id:        info.Id,
		Rev:       info.LocalRev,
		UnlockSig: info.LocalPaymentSig,
	}
	sig, err := signRPCMessage(info.LocalWallet(), req)
	if err != nil {
		return err
	}
	req.Sig = sig

	resp, err := p.SendChannelUnlockRevokeAndAckReq(req)
	if err != nil {
		return err
	}
	info.RemotePaymentSig = resp.UnlockSig
	return nil
}

func (p *NodeClient) SendUnlockRevokeAndAckReq(info *PaymentReservation) error {
	return p.SendUnlockRevokeAndAckReservationReq(info)
}

func (p *NodeClient) SendRecoverPaymentReq(info *PaymentReservation) error {
	resp, err := p.SendChannelRecoverPaymentReq(&wwire.RecoverPaymentRequireReq{
		RecoverPaymentRequest: *info.RecoverPaymentReq,
		Sig:                   info.ReqSig,
	})
	if err != nil {
		return err
	}
	info.Id = resp.Id
	info.RevealPrivKey = resp.RevealKey
	info.RemoteNextRevKey = resp.NextRevKey
	info.RemoteRevKey = resp.RevKey
	info.FeeRate = resp.FeeRate
	return nil
}

func (p *NodeClient) SendRecoverPaymentCommitSigReq(info *PaymentReservation) error {
	req := &wwire.RecoverPaymentCommitSigReq{
		ChannelId:     info.Channel.ChannelId,
		Id:            info.Id,
		CommitSigInfo: info.RemoteCommitInfo.CommitSigInfo,
		RevKey:        info.LocalRevKey,
		NextRevKey:    info.LocalNextRevKey,
	}
	sig, err := signRPCMessage(info.LocalWallet(), req)
	if err != nil {
		return err
	}
	req.Sig = sig

	resp, err := p.SendChannelRecoverPaymentCommitSigReq(req)
	if err != nil {
		return err
	}
	info.RemoteRev = resp.Rev
	info.LocalCommitInfo.CommitSigInfo = resp.CommitSigInfo
	info.Channel.LocalCommitment.CommitSig = resp.CommitSig
	info.Channel.LocalCommitment.DeAnchorSig = resp.CommitDeAnchorSig
	return nil
}

func (p *NodeClient) SendRecoverPaymentRevokeAndAckReq(info *PaymentReservation) error {
	req := &wwire.RecoverPaymentRevokeAndAckReq{
		ChannelId:  info.Channel.ChannelId,
		Id:         info.Id,
		Rev:        info.LocalRev,
		PaymentSig: info.LocalPaymentSig,
	}
	sig, err := signRPCMessage(info.LocalWallet(), req)
	if err != nil {
		return err
	}
	req.Sig = sig

	resp, err := p.SendChannelRecoverPaymentRevokeAndAckReq(req)
	if err != nil {
		return err
	}
	info.RemotePaymentSig = resp.PaymentSig
	return nil
}

func (p *NodeClient) SendLockReservationReq(info *PaymentReservation) error {
	resp, err := p.SendChannelLockReq(&wwire.LockReq{
		LockRequest: *info.LockReq,
		Sig:         info.ReqSig,
	})
	if err != nil {
		return err
	}
	info.Id = resp.Id
	info.FeeRate = resp.FeeRate
	info.RemoteNextRevKey = resp.NextRevKey
	info.RemoteRevKey = resp.RevKey
	info.LocalCommitInfo.CommitSigInfo = resp.CommitSigInfo
	info.Channel.LocalCommitment.CommitSig = resp.CommitSig
	info.Channel.LocalCommitment.DeAnchorSig = resp.CommitDeAnchorSig
	return nil
}

func (p *NodeClient) SendLockReq(info *PaymentReservation) error {
	return p.SendLockReservationReq(info)
}

func (p *NodeClient) SendLockCommitSigAndRevokeReservationReq(info *PaymentReservation) error {
	req := &wwire.LockCommitSigAndRevokeReq{
		ChannelId:     info.Channel.ChannelId,
		Id:            info.Id,
		CommitSigInfo: info.RemoteCommitInfo.CommitSigInfo,
		Rev:           info.LocalRev,
	}
	sig, err := signRPCMessage(info.LocalWallet(), req)
	if err != nil {
		return err
	}
	req.Sig = sig

	resp, err := p.SendChannelLockCommitSigAndRevokeReq(req)
	if err != nil {
		return err
	}
	info.RemoteRev = resp.Rev
	info.RemotePaymentSig = resp.LockSig
	return nil
}

func (p *NodeClient) SendLockCommitSigAndRevokeReq(info *PaymentReservation) error {
	return p.SendLockCommitSigAndRevokeReservationReq(info)
}

func (p *NodeClient) SendLockAckReservationReq(info *PaymentReservation) error {
	req := &wwire.LockAckReq{
		ChannelId: info.Channel.ChannelId,
		Id:        info.Id,
		LockSig:   info.LocalPaymentSig,
	}
	sig, err := signRPCMessage(info.LocalWallet(), req)
	if err != nil {
		return err
	}
	req.Sig = sig

	_, err = p.SendChannelLockAckReq(req)
	return err
}

func (p *NodeClient) SendLockAckReq(info *PaymentReservation) error {
	return p.SendLockAckReservationReq(info)
}

func (p *NodeClient) SendOpenChannelReservationReq(info *FundingReservation) error {
	resp, err := p.SendChannelOpenReq(&wwire.ChannelOpenReq{
		OpenChannelRequest: *info.Req,
		Sig:                info.ReqSig,
	})
	if err != nil {
		return err
	}
	info.Id = resp.Id
	info.Accept = &resp.AcceptChannel
	return nil
}

func (p *NodeClient) SendOpenChannelReq(info *FundingReservation) error {
	return p.SendOpenChannelReservationReq(info)
}

func (p *NodeClient) SendFundingCreatedReservationReq(info *FundingReservation) error {
	req := &wwire.FundingCreatedReq{
		FundingCreated: *info.FundingCreated,
	}
	sig, err := signRPCMessage(info.LocalWallet(), req)
	if err != nil {
		return err
	}
	req.Sig = sig

	resp, err := p.SendChannelFundingCreatedReq(req)
	if err != nil {
		return err
	}
	info.FundingSigned = &resp.FundingSigned
	return nil
}

func (p *NodeClient) SendFundingCreatedReq(info *FundingReservation) error {
	return p.SendFundingCreatedReservationReq(info)
}

func (p *NodeClient) SendFundingBroadcastedReservationReq(info *FundingReservation) error {
	req := &wwire.FundingBroadcastedReq{
		FundingBroadcasted: *info.FundingBroadcasted,
	}
	sig, err := signRPCMessage(info.LocalWallet(), req)
	if err != nil {
		return err
	}
	req.Sig = sig

	_, err = p.SendChannelFundingBroadcastedReq(req)
	return err
}

func (p *NodeClient) SendFundingBroadcastedReq(info *FundingReservation) error {
	return p.SendFundingBroadcastedReservationReq(info)
}

func (p *NodeClient) SendCloseChannelReservationReq(info *ClosingReservation) error {
	resp, err := p.SendChannelCloseReq(&wwire.ChannelCloseReq{
		CloseChannelRequest: *info.Req,
		Sig:                 info.ReqSig,
	})
	if err != nil {
		return err
	}
	info.Id = resp.Id
	info.RemoteSigned = &resp.ClosingSigned
	return nil
}

func (p *NodeClient) SendCloseChannelReq(info *ClosingReservation) error {
	return p.SendCloseChannelReservationReq(info)
}

func (p *NodeClient) SendClosingSignedReservationReq(info *ClosingReservation) error {
	req := &wwire.ClosingSignedReq{
		ChannelId:     info.ChannelId,
		ClosingSigned: *info.LocalSigned,
	}
	sig, err := signRPCMessage(info.LocalWallet(), req)
	if err != nil {
		return err
	}
	req.Sig = sig

	resp, err := p.SendChannelClosingSignedReq(req)
	if err != nil {
		return err
	}
	info.SplicingOutTxId = resp.SplicingTxId
	return nil
}

func (p *NodeClient) SendClosingSignedReq(info *ClosingReservation) error {
	return p.SendClosingSignedReservationReq(info)
}

func (p *NodeClient) SendClosingBroadcastedReservationReq(info *ClosingReservation) error {
	req := &wwire.ClosingBroadcastedReq{
		ChannelId:          info.ChannelId,
		ClosingBroadcasted: *info.ClosingBroadcasted,
	}
	sig, err := signRPCMessage(info.LocalWallet(), req)
	if err != nil {
		return err
	}
	req.Sig = sig

	_, err = p.SendChannelClosingBroadcastedReq(req)
	return err
}

func (p *NodeClient) SendClosingBroadcastedReq(info *ClosingReservation) error {
	return p.SendClosingBroadcastedReservationReq(info)
}

func (p *NodeClient) SendSplicingInReq(info *SplicingReservation) error {
	resp, err := p.SendChannelSplicingInReq(&wwire.SplicingInReq{
		SplicingInRequest: *info.InReq,
		Sig:               info.ReqSig,
	})
	if err != nil {
		return err
	}

	localBalance, err := indexer.NewDecimalFromString(resp.NewRemoteBalance, info.TickerInfo.Divisibility)
	if err != nil {
		return err
	}
	remoteBalance, err := indexer.NewDecimalFromString(resp.NewLocalBalance, info.TickerInfo.Divisibility)
	if err != nil {
		return err
	}

	info.Id = resp.Id
	info.NewCapacity = resp.NewCapacity
	info.NewLocalBalance = localBalance
	info.NewRemoteBalance = remoteBalance
	info.ServiceFee = resp.ServiceFee
	info.RemoteRevKey = resp.RevKey
	info.RemoteNextRevKey = resp.NextRevKey
	info.InvoiceSig = resp.InvoiceSig
	return nil
}

func (p *NodeClient) SendSplicingInCommitSigReq(info *SplicingReservation) error {
	req := &wwire.SplicingInCommitSigReq{
		ChannelId:       info.Channel.ChannelId,
		Id:              info.Id,
		SplicingSigInfo: info.LocalSplicingSigInfo,
		CommitSigInfo:   info.RemoteCommitInfo.CommitSigInfo,
		RevKey:          info.LocalRevKey,
		NextRevKey:      info.LocalNextRevKey,
	}
	sig, err := signRPCMessage(info.LocalWallet(), req)
	if err != nil {
		return err
	}
	req.Sig = sig

	resp, err := p.SendChannelSplicingInCommitSigReq(req)
	if err != nil {
		return err
	}
	info.RemoteRev = resp.Rev
	info.RemoteSplicingSigInfo = resp.SplicingSigInfo
	info.LocalCommitInfo.CommitSigInfo = resp.CommitSigInfo
	info.Channel.LocalCommitment.CommitSig = resp.CommitSig
	info.Channel.LocalCommitment.DeAnchorSig = resp.CommitDeAnchorSig
	return nil
}

func (p *NodeClient) SendSplicingInRevokeAndAckReq(info *SplicingReservation) error {
	txId := ""
	if info.NeedSendSplicingTx && info.SplicingTx != nil {
		txId = info.SplicingTx.TxID()
	}

	req := &wwire.SplicingInRevokeAndAckReq{
		ChannelId:    info.Channel.ChannelId,
		Id:           info.Id,
		SplicingTxId: txId,
		Rev:          info.LocalRev,
	}
	sig, err := signRPCMessage(info.LocalWallet(), req)
	if err != nil {
		return err
	}
	req.Sig = sig

	_, err = p.SendChannelSplicingInRevokeAndAckReq(req)
	return err
}

func (p *NodeClient) SendSplicingOutReq(info *SplicingReservation) error {
	resp, err := p.SendChannelSplicingOutReq(&wwire.SplicingOutReq{
		SplicingOutRequest: *info.OutReq,
		Sig:                info.ReqSig,
	})
	if err != nil {
		return err
	}

	localBalance, err := indexer.NewDecimalFromString(resp.NewRemoteBalance, info.TickerInfo.Divisibility)
	if err != nil {
		return err
	}
	remoteBalance, err := indexer.NewDecimalFromString(resp.NewLocalBalance, info.TickerInfo.Divisibility)
	if err != nil {
		return err
	}

	info.Id = resp.Id
	info.NewCapacity = resp.NewCapacity
	info.NewLocalBalance = localBalance
	info.NewRemoteBalance = remoteBalance
	info.ServiceFee = resp.ServiceFee
	info.RemoteRevKey = resp.RevKey
	info.RemoteNextRevKey = resp.NextRevKey
	return nil
}

func (p *NodeClient) SendSplicingOutCommitSigReq(info *SplicingReservation) error {
	req := &wwire.SplicingOutCommitSigReq{
		ChannelId:       info.Channel.ChannelId,
		Id:              info.Id,
		SplicingSigInfo: info.LocalSplicingSigInfo,
		CommitSigInfo:   info.RemoteCommitInfo.CommitSigInfo,
		RevKey:          info.LocalRevKey,
		NextRevKey:      info.LocalNextRevKey,
	}
	sig, err := signRPCMessage(info.LocalWallet(), req)
	if err != nil {
		return err
	}
	req.Sig = sig

	resp, err := p.SendChannelSplicingOutCommitSigReq(req)
	if err != nil {
		return err
	}
	info.RemoteRev = resp.Rev
	info.RemoteSplicingSigInfo = resp.SplicingSigInfo
	info.LocalCommitInfo.CommitSigInfo = resp.CommitSigInfo
	info.Channel.LocalCommitment.CommitSig = resp.CommitSig
	info.Channel.LocalCommitment.DeAnchorSig = resp.CommitDeAnchorSig
	return nil
}

func (p *NodeClient) SendSplicingOutRevokeAndAckReq(info *SplicingReservation) error {
	req := &wwire.SplicingOutRevokeAndAckReq{
		ChannelId:    info.Channel.ChannelId,
		Id:           info.Id,
		DeAnchorTxId: info.AnchorTx.TxID(),
		Rev:          info.LocalRev,
	}
	sig, err := signRPCMessage(info.LocalWallet(), req)
	if err != nil {
		return err
	}
	req.Sig = sig

	_, err = p.SendChannelSplicingOutRevokeAndAckReq(req)
	return err
}
