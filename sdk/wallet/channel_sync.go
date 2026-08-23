package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

func (p *Manager) SyncChannel(reason string, client NodeRPCClient) error {
	p.channelIdentityMu.RLock()
	p.mutex.RLock()
	if p.wallet == nil {
		p.mutex.RUnlock()
		p.channelIdentityMu.RUnlock()
		return fmt.Errorf("wallet is not created/unlocked")
	}
	localWallet := p.wallet.Clone()
	identityGeneration := p.channelIdentityGeneration
	p.mutex.RUnlock()
	p.channelIdentityMu.RUnlock()
	if err := p.rejectPendingChannelLifecycleSync(localWallet); err != nil {
		return err
	}
	return p.syncChannelForIdentity(context.Background(), reason, client, localWallet, identityGeneration)
}

func (p *Manager) syncChannelForIdentity(ctx context.Context, reason string, client NodeRPCClient, localWallet common.Wallet,
	identityGeneration uint64) error {
	if err := p.rejectPendingChannelLifecycleSync(localWallet); err != nil {
		return err
	}
	channelData, err := p.requestChannelSync(ctx, reason, client, localWallet)
	if err != nil {
		return err
	}

	p.channelIdentityMu.RLock()
	defer p.channelIdentityMu.RUnlock()
	p.mutex.RLock()
	identityUnchanged := p.channelIdentityGeneration == identityGeneration && p.wallet != nil &&
		bytes.Equal(localWallet.GetPaymentPubKey().SerializeCompressed(),
			p.wallet.GetPaymentPubKey().SerializeCompressed())
	p.mutex.RUnlock()
	if !identityUnchanged {
		return fmt.Errorf("wallet identity changed during channel sync")
	}
	if err := p.rejectPendingChannelLifecycleSync(localWallet); err != nil {
		return err
	}

	if err := p.rebuildChannelFromPeerChanInfoForWallet(channelData, localWallet); err != nil {
		Log.Errorf("RebuildChannelFromPeerChanInfo failed. %v", err)
		return err
	}
	return nil
}

func (p *Manager) rejectPendingChannelLifecycleSync(localWallet common.Wallet) error {
	if localWallet == nil || localWallet.GetPaymentPubKey() == nil {
		return fmt.Errorf("wallet is not created/unlocked")
	}
	p.mutex.RLock()
	var serverPubKey []byte
	if p.serverNode != nil && p.serverNode.Pubkey != nil {
		serverPubKey = append([]byte(nil), p.serverNode.Pubkey.SerializeCompressed()...)
	}
	p.mutex.RUnlock()
	if len(serverPubKey) == 0 {
		return fmt.Errorf("server node is not initialized")
	}
	channelID, err := GetP2WSHaddress(serverPubKey, localWallet.GetPaymentPubKey().SerializeCompressed())
	if err != nil {
		return err
	}
	if p.hasPendingFundingReservation(channelID, localWallet.GetWalletId()) {
		return fmt.Errorf("channel funding is pending; peer sync is disabled")
	}
	if p.hasPendingClosingReservation(channelID, localWallet.GetWalletId()) {
		return fmt.Errorf("channel closing is pending; peer sync is disabled")
	}
	return nil
}

func (p *Manager) requestChannelSync(ctx context.Context, reason string, client NodeRPCClient, localWallet common.Wallet) ([]byte, error) {
	if client == nil {
		return nil, fmt.Errorf("node client is nil")
	}
	if localWallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}

	req := &wwire.ActionSyncReq{
		ActionSyncRequest: wwire.ActionSyncRequest{
			MsgHeader: wwire.NewMsgHeader(),
			PubKey:    localWallet.GetPaymentPubKey().SerializeCompressed(),
			Reason:    reason,
			NodeId:    localWallet.GetNodePubKey().SerializeCompressed(),
		},
	}
	msg, err := json.Marshal(req.ActionSyncRequest)
	if err != nil {
		return nil, err
	}
	req.Sig, err = localWallet.SignMessageWithIndex(msg, 0)
	if err != nil {
		return nil, err
	}

	var resp *wwire.ActionSyncResp
	if contextClient, ok := client.(interface {
		SendActionSyncReqContext(context.Context, *wwire.ActionSyncReq) (*wwire.ActionSyncResp, error)
	}); ok {
		resp, err = contextClient.SendActionSyncReqContext(ctx, req)
	} else {
		resp, err = client.SendActionSyncReq(req)
	}
	if err != nil {
		Log.Errorf("SendActionSyncReq failed. %v", err)
		return nil, err
	}
	return resp.ChannelData, nil
}

func (p *Manager) RebuildChannelFromPeerChanInfo(peerChannelInDB []byte) error {
	p.channelIdentityMu.RLock()
	defer p.channelIdentityMu.RUnlock()
	p.mutex.RLock()
	if p.wallet == nil {
		p.mutex.RUnlock()
		return fmt.Errorf("wallet is not created/unlocked")
	}
	localWallet := p.wallet.Clone()
	p.mutex.RUnlock()
	return p.rebuildChannelFromPeerChanInfoForWallet(peerChannelInDB, localWallet)
}

func (p *Manager) rebuildChannelFromPeerChanInfoForWallet(peerChannelInDB []byte, localWallet common.Wallet) error {
	Log.Infof("channel data length %d", len(peerChannelInDB))
	if localWallet == nil {
		return fmt.Errorf("wallet is not created/unlocked")
	}
	if err := p.rejectPendingChannelLifecycleSync(localWallet); err != nil {
		return err
	}

	var channel ChannelInDB
	err := DecodeFromBytes(peerChannelInDB, &channel)
	if err != nil {
		Log.Errorf("DecodeFromBytes failed. %v", err)
		return err
	}
	err = channel.CheckMerkleRoot()
	if err != nil {
		Log.Errorf("channel %s CheckMerkleRoot failed, %v", channel.ChannelId, err)
		return fmt.Errorf("channel %s CheckMerkleRoot failed, %v", channel.ChannelId, err)
	}

	p.mutex.RLock()
	var serverNodeID []byte
	if p.serverNode != nil && p.serverNode.NodeId != nil {
		serverNodeID = append([]byte(nil), p.serverNode.NodeId.SerializeCompressed()...)
	}
	p.mutex.RUnlock()
	if err := restorePeerChannelPerspective(&channel, localWallet, serverNodeID); err != nil {
		return err
	}

	c := NewChannel(&channel, p)
	if err := p.SignAndVerifyCommitTxV2(c, true); err != nil {
		Log.Errorf("RebuildChannelFromPeerChanInfo VerifyCommitTx failed. %v", err)
		return err
	}

	if err := p.SaveChannelToDB(c); err != nil {
		return err
	}
	Log.Infof("channel %s is restored", channel.ChannelId)
	if channel.Status == CS_READY {
		p.EnableChannel(c)
	}
	return nil
}

func restorePeerChannelPerspective(channel *ChannelInDB, localWallet common.Wallet, serverNodeID []byte) error {
	if channel == nil || localWallet == nil || localWallet.GetNodePubKey() == nil ||
		!bytes.Equal(channel.PeerNodeId, localWallet.GetNodePubKey().SerializeCompressed()) {
		var peerNodeID []byte
		if channel != nil {
			peerNodeID = channel.PeerNodeId
		}
		return fmt.Errorf("invalid peer %s", hex.EncodeToString(peerNodeID))
	}
	if len(serverNodeID) == 0 {
		return fmt.Errorf("server node id is unavailable")
	}
	channel.PeerNodeId = append([]byte(nil), serverNodeID...)
	channel.IsInitiator = !channel.IsInitiator
	channel.LocalChanCfg, channel.RemoteChanCfg = channel.RemoteChanCfg, channel.LocalChanCfg
	channel.TotalSatSent, channel.TotalSatReceived = channel.TotalSatReceived, channel.TotalSatSent
	channel.LocalCommitment, channel.RemoteCommitment = channel.RemoteCommitment, channel.LocalCommitment
	return nil
}
