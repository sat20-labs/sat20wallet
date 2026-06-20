package wallet

import (
	"encoding/json"
	"fmt"

	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

func (p *NodeClient) postChannelProtocolReq(path string, req any, resp any, op string) error {
	buff, err := json.Marshal(req)
	if err != nil {
		return err
	}

	url := p.GetUrl(path)
	rsp, err := p.Http.SendPostRequest(url, buff)
	if err != nil {
		Log.Errorf("SendPostRequest %v failed. %v", url, err)
		return err
	}

	if err := json.Unmarshal(rsp, resp); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return err
	}

	var base wwire.BaseResp
	if err := json.Unmarshal(rsp, &base); err != nil {
		return err
	}
	if base.Code != 0 {
		Log.Errorf("%s failed, %s", op, base.Msg)
		return fmt.Errorf("%s", base.Msg)
	}

	return nil
}

func (p *NodeClient) SendChannelOpenReq(req *wwire.ChannelOpenReq) (*wwire.ChannelOpenResp, error) {
	var resp wwire.ChannelOpenResp
	err := p.postChannelProtocolReq(wwire.STP_FUNDING_REQ, req, &resp, "SendChannelOpenReq")
	return &resp, err
}

func (p *NodeClient) SendChannelFundingCreatedReq(req *wwire.FundingCreatedReq) (*wwire.FundingCreatedResp, error) {
	var resp wwire.FundingCreatedResp
	err := p.postChannelProtocolReq(wwire.STP_FUNDING_CREATED, req, &resp, "SendChannelFundingCreatedReq")
	return &resp, err
}

func (p *NodeClient) SendChannelFundingBroadcastedReq(req *wwire.FundingBroadcastedReq) (*wwire.FundingBroadcastedResp, error) {
	var resp wwire.FundingBroadcastedResp
	err := p.postChannelProtocolReq(wwire.STP_FUNDING_BROADCASTED, req, &resp, "SendChannelFundingBroadcastedReq")
	return &resp, err
}

func (p *NodeClient) SendChannelCloseReq(req *wwire.ChannelCloseReq) (*wwire.ChannelCloseResp, error) {
	var resp wwire.ChannelCloseResp
	err := p.postChannelProtocolReq(wwire.STP_CLOSE_REQ, req, &resp, "SendChannelCloseReq")
	return &resp, err
}

func (p *NodeClient) SendChannelClosingSignedReq(req *wwire.ClosingSignedReq) (*wwire.ClosingSignedResp, error) {
	var resp wwire.ClosingSignedResp
	err := p.postChannelProtocolReq(wwire.STP_CLOSE_SIGNED, req, &resp, "SendChannelClosingSignedReq")
	return &resp, err
}

func (p *NodeClient) SendChannelClosingBroadcastedReq(req *wwire.ClosingBroadcastedReq) (*wwire.ClosingBroadcastedResp, error) {
	var resp wwire.ClosingBroadcastedResp
	err := p.postChannelProtocolReq(wwire.STP_CLOSE_BROADCASTED, req, &resp, "SendChannelClosingBroadcastedReq")
	return &resp, err
}

func (p *NodeClient) SendChannelLockReq(req *wwire.LockReq) (*wwire.LockResp, error) {
	var resp wwire.LockResp
	err := p.postChannelProtocolReq(wwire.STP_LOCK_REQ, req, &resp, "SendChannelLockReq")
	return &resp, err
}

func (p *NodeClient) SendChannelLockCommitSigAndRevokeReq(req *wwire.LockCommitSigAndRevokeReq) (*wwire.LockCommitSigAndRevokeResp, error) {
	var resp wwire.LockCommitSigAndRevokeResp
	err := p.postChannelProtocolReq(wwire.STP_LOCK_SIGANDREVOKE, req, &resp, "SendChannelLockCommitSigAndRevokeReq")
	return &resp, err
}

func (p *NodeClient) SendChannelLockAckReq(req *wwire.LockAckReq) (*wwire.LockAckResp, error) {
	var resp wwire.LockAckResp
	err := p.postChannelProtocolReq(wwire.STP_LOCK_ACK, req, &resp, "SendChannelLockAckReq")
	return &resp, err
}

func (p *NodeClient) SendChannelUnlockReq(req *wwire.UnlockReq) (*wwire.UnlockResp, error) {
	var resp wwire.UnlockResp
	err := p.postChannelProtocolReq(wwire.STP_UNLOCK_REQ, req, &resp, "SendChannelUnlockReq")
	return &resp, err
}

func (p *NodeClient) SendChannelUnlockCommitSigReq(req *wwire.UnlockCommitSigReq) (*wwire.UnlockCommitSigResp, error) {
	var resp wwire.UnlockCommitSigResp
	err := p.postChannelProtocolReq(wwire.STP_UNLOCK_COMMITSIG, req, &resp, "SendChannelUnlockCommitSigReq")
	return &resp, err
}

func (p *NodeClient) SendChannelUnlockRevokeAndAckReq(req *wwire.UnlockRevokeAndAckReq) (*wwire.UnlockRevokeAndAckResp, error) {
	var resp wwire.UnlockRevokeAndAckResp
	err := p.postChannelProtocolReq(wwire.STP_UNLOCK_REVOKEANDACK, req, &resp, "SendChannelUnlockRevokeAndAckReq")
	return &resp, err
}

func (p *NodeClient) SendChannelRecoverPaymentReq(req *wwire.RecoverPaymentRequireReq) (*wwire.RecoverPaymentRequireResp, error) {
	var resp wwire.RecoverPaymentRequireResp
	err := p.postChannelProtocolReq(wwire.STP_RECOVER_PAYMENT_REQ, req, &resp, "SendChannelRecoverPaymentReq")
	return &resp, err
}

func (p *NodeClient) SendChannelRecoverPaymentCommitSigReq(req *wwire.RecoverPaymentCommitSigReq) (*wwire.RecoverPaymentCommitSigResp, error) {
	var resp wwire.RecoverPaymentCommitSigResp
	err := p.postChannelProtocolReq(wwire.STP_RECOVER_PAYMENT_COMMITSIG, req, &resp, "SendChannelRecoverPaymentCommitSigReq")
	return &resp, err
}

func (p *NodeClient) SendChannelRecoverPaymentRevokeAndAckReq(req *wwire.RecoverPaymentRevokeAndAckReq) (*wwire.RecoverPaymentRevokeAndAckResp, error) {
	var resp wwire.RecoverPaymentRevokeAndAckResp
	err := p.postChannelProtocolReq(wwire.STP_RECOVER_PAYMENT_REVOKEANDACK, req, &resp, "SendChannelRecoverPaymentRevokeAndAckReq")
	return &resp, err
}

func (p *NodeClient) SendChannelSplicingInReq(req *wwire.SplicingInReq) (*wwire.SplicingInResp, error) {
	var resp wwire.SplicingInResp
	err := p.postChannelProtocolReq(wwire.STP_SPLICING_IN_REQ, req, &resp, "SendChannelSplicingInReq")
	return &resp, err
}

func (p *NodeClient) SendChannelSplicingInCommitSigReq(req *wwire.SplicingInCommitSigReq) (*wwire.SplicingInCommitSigResp, error) {
	var resp wwire.SplicingInCommitSigResp
	err := p.postChannelProtocolReq(wwire.STP_SPLICING_IN_COMMITSIG, req, &resp, "SendChannelSplicingInCommitSigReq")
	return &resp, err
}

func (p *NodeClient) SendChannelSplicingInRevokeAndAckReq(req *wwire.SplicingInRevokeAndAckReq) (*wwire.SplicingInRevokeAndAckResp, error) {
	var resp wwire.SplicingInRevokeAndAckResp
	err := p.postChannelProtocolReq(wwire.STP_SPLICING_IN_REVOKEANDACK, req, &resp, "SendChannelSplicingInRevokeAndAckReq")
	return &resp, err
}

func (p *NodeClient) SendChannelSplicingOutReq(req *wwire.SplicingOutReq) (*wwire.SplicingOutResp, error) {
	var resp wwire.SplicingOutResp
	err := p.postChannelProtocolReq(wwire.STP_SPLICING_OUT_REQ, req, &resp, "SendChannelSplicingOutReq")
	return &resp, err
}

func (p *NodeClient) SendChannelSplicingOutCommitSigReq(req *wwire.SplicingOutCommitSigReq) (*wwire.SplicingOutCommitSigResp, error) {
	var resp wwire.SplicingOutCommitSigResp
	err := p.postChannelProtocolReq(wwire.STP_SPLICING_OUT_COMMITSIG, req, &resp, "SendChannelSplicingOutCommitSigReq")
	return &resp, err
}

func (p *NodeClient) SendChannelSplicingOutRevokeAndAckReq(req *wwire.SplicingOutRevokeAndAckReq) (*wwire.SplicingOutRevokeAndAckResp, error) {
	var resp wwire.SplicingOutRevokeAndAckResp
	err := p.postChannelProtocolReq(wwire.STP_SPLICING_OUT_REVOKEANDACK, req, &resp, "SendChannelSplicingOutRevokeAndAckReq")
	return &resp, err
}
