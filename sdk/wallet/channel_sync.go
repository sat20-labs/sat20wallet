package wallet

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"

	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

func (p *Manager) SyncChannel(reason string, client NodeRPCClient) error {
	if client == nil {
		return fmt.Errorf("node client is nil")
	}
	if p.wallet == nil {
		return fmt.Errorf("wallet is not created/unlocked")
	}

	req := &wwire.ActionSyncReq{
		ActionSyncRequest: wwire.ActionSyncRequest{
			MsgHeader: wwire.NewMsgHeader(),
			PubKey:    p.wallet.GetPaymentPubKey().SerializeCompressed(),
			Reason:    reason,
			NodeId:    p.wallet.GetNodePubKey().SerializeCompressed(),
		},
	}
	msg, err := json.Marshal(req.ActionSyncRequest)
	if err != nil {
		return err
	}
	req.Sig, err = p.wallet.SignMessageWithIndex(msg, 0)
	if err != nil {
		return err
	}

	resp, err := client.SendActionSyncReq(req)
	if err != nil {
		Log.Errorf("SendActionSyncReq failed. %v", err)
		return err
	}

	err = p.RebuildChannelFromPeerChanInfo(resp.ChannelData)
	if err != nil {
		Log.Errorf("RebuildChannelFromPeerChanInfo failed. %v", err)
		return err
	}
	return nil
}

func (p *Manager) RebuildChannelFromPeerChanInfo(peerChannelInDB []byte) error {
	Log.Infof("channel data length %d", len(peerChannelInDB))

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

	if !bytes.Equal(channel.PeerNodeId, p.wallet.GetPaymentPubKey().SerializeCompressed()) {
		return fmt.Errorf("invalid peer %s", hex.EncodeToString(channel.PeerNodeId))
	}
	channel.PeerNodeId = channel.LocalChanCfg.PaymentKey.SerializeCompressed()
	channel.IsInitiator = !channel.IsInitiator
	channel.LocalChanCfg, channel.RemoteChanCfg = channel.RemoteChanCfg, channel.LocalChanCfg
	channel.TotalSatSent, channel.TotalSatReceived = channel.TotalSatReceived, channel.TotalSatSent
	channel.LocalCommitment, channel.RemoteCommitment = channel.RemoteCommitment, channel.LocalCommitment

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
