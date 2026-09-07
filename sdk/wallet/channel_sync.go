package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
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
	p.mutex.RUnlock()
	p.channelIdentityMu.RUnlock()
	if err := p.rejectPendingChannelLifecycleSync(localWallet); err != nil {
		return err
	}
	return p.syncChannelForWallet(context.Background(), reason, client, localWallet)
}

// The request owns its wallet/account snapshot. Changing the UI selection does
// not invalidate it; only changes to this channel's lifecycle/state may do so.
func (p *Manager) syncChannelForWallet(ctx context.Context, reason string, client NodeRPCClient, localWallet common.Wallet) error {
	if err := p.rejectPendingChannelLifecycleSync(localWallet); err != nil {
		return err
	}
	channelData, err := p.requestChannelSync(ctx, reason, client, localWallet)
	if err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
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
	channelID, err := p.channelIDForWallet(localWallet)
	if err != nil {
		return err
	}
	if err := p.rejectUnfinishedChannelLifecycleForWallet(channelID, localWallet); err != nil {
		return err
	}
	if channel := p.GetChannel(channelID); channel != nil {
		if !channel.Mutex.TryRLock() {
			return fmt.Errorf("channel %s is busy; skip sync", channelID)
		}
		defer channel.Mutex.RUnlock()
		if channel.ResvId != 0 || channel.Status != CS_READY {
			return fmt.Errorf("channel %s is not idle; skip sync", channelID)
		}
	}
	return nil
}

func (p *Manager) channelIDForWallet(localWallet common.Wallet) (string, error) {
	if localWallet == nil || localWallet.GetPaymentPubKey() == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}
	p.mutex.RLock()
	var serverPubKey []byte
	if p.serverNode != nil && p.serverNode.Pubkey != nil {
		serverPubKey = append([]byte(nil), p.serverNode.Pubkey.SerializeCompressed()...)
	}
	p.mutex.RUnlock()
	if len(serverPubKey) == 0 {
		return "", fmt.Errorf("server node is not initialized")
	}
	channelID, err := GetP2WSHaddress(serverPubKey, localWallet.GetPaymentPubKey().SerializeCompressed())
	if err != nil {
		return "", err
	}
	return channelID, nil
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
	p.mutex.RLock()
	if p.wallet == nil {
		p.mutex.RUnlock()
		p.channelIdentityMu.RUnlock()
		return fmt.Errorf("wallet is not created/unlocked")
	}
	localWallet := p.wallet.Clone()
	p.mutex.RUnlock()
	p.channelIdentityMu.RUnlock()
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
	expectedChannelID, err := p.channelIDForWallet(localWallet)
	if err != nil {
		return err
	}
	if channel.ChannelId != expectedChannelID {
		return fmt.Errorf("peer snapshot channel %s does not match current channel %s",
			channel.ChannelId, expectedChannelID)
	}

	if channel.LocalChanCfg.PaymentKey == nil ||
		!channel.LocalChanCfg.PaymentKey.IsEqual(localWallet.GetPaymentPubKey()) {
		return fmt.Errorf("peer snapshot local payment key does not match requested wallet")
	}
	c := newPeerSnapshotChannel(&channel, p, localWallet)
	if err := p.SignAndVerifyCommitTxV2(c, true); err != nil {
		Log.Errorf("RebuildChannelFromPeerChanInfo VerifyCommitTx failed. %v", err)
		return err
	}

	if err := p.replaceChannelFromPeer(c, localWallet); err != nil {
		return err
	}
	Log.Infof("channel %s is restored", channel.ChannelId)
	return nil
}

func (p *Manager) replaceChannelFromPeer(channel *Channel, localWallet common.Wallet) error {
	if channel == nil {
		return fmt.Errorf("peer channel is nil")
	}
	current := p.GetChannel(channel.ChannelId)
	if current != nil {
		if !current.Mutex.TryLock() {
			return fmt.Errorf("channel %s is busy; skip sync", channel.ChannelId)
		}
		defer current.Mutex.Unlock()
	}

	// Use the existing channel/manager locks only for applying the response,
	// never across the peer request. Normal channel operations own these locks.
	err := func() error {
		p.mutex.Lock()
		defer p.mutex.Unlock()
		if p.channelMap[channel.ChannelId] != current {
			return fmt.Errorf("channel changed during sync")
		}
		if err := p.rejectUnfinishedChannelLifecycleLocked(channel.ChannelId, localWallet.GetWalletId(),
			localWallet.GetPaymentPubKey().SerializeCompressed()); err != nil {
			return fmt.Errorf("peer snapshot rejected: %w", err)
		}
		var local *ChannelInDB
		if current != nil {
			if current.ResvId != 0 || current.Status != CS_READY {
				return fmt.Errorf("channel %s is not idle; skip sync", channel.ChannelId)
			}
			local = &current.ChannelInDB
		} else {
			var err error
			local, err = p.LoadChannelInDB(channel.ChannelId)
			if err != nil && !errors.Is(err, indexer.ErrKeyNotFound) {
				return err
			}
		}
		localHeight := -1 // No local channel: the initial peer height 0 can be restored.
		if local != nil {
			localHeight = local.CommitHeight
		}
		if channel.CommitHeight <= localHeight {
			return fmt.Errorf("peer commit height %d must be greater than local %d; skip sync",
				channel.CommitHeight, localHeight)
		}
		if err := p.SaveChannelInDB(&channel.ChannelInDB); err != nil {
			return err
		}
		if channel.Status == CS_READY {
			if current != nil {
				// Preserve the runtime pointer for operations already waiting on
				// its mutex; they must see the new state when they acquire it.
				current.ChannelInDB = channel.ChannelInDB
				current.PeerRPC = channel.PeerRPC
				current.localWallet = channel.localWallet
				channel = current
			}
			p.installChannelLocked(channel)
		}
		return nil
	}()
	if err != nil {
		return err
	}

	p.backupChannel(channel)
	if channel.Status == CS_READY {
		if tower := p.GetWatchTower(); tower != nil {
			tower.CleanCurrentRemoteCommitTx(channel)
		}
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
