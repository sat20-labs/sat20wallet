package wallet

import (
	"context"
	"encoding/json"
	"time"

	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

const channelHeartbeatInterval = 30 * time.Second

func (p *Manager) startChannelHeartbeat() {
	p.channelHeartbeatMu.Lock()
	defer p.channelHeartbeatMu.Unlock()
	if p.channelHeartbeatRunning {
		return
	}

	stop := make(chan struct{})
	wake := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	p.channelHeartbeatStop = stop
	p.channelHeartbeatWake = wake
	p.channelHeartbeatCancel = cancel
	p.channelHeartbeatRunning = true
	p.channelHeartbeatWG.Add(1)
	go p.channelHeartbeatThread(ctx, stop, wake)
}

func (p *Manager) stopChannelHeartbeat() {
	p.channelHeartbeatMu.Lock()
	if !p.channelHeartbeatRunning {
		p.channelHeartbeatMu.Unlock()
		return
	}
	stop := p.channelHeartbeatStop
	cancel := p.channelHeartbeatCancel
	p.channelHeartbeatRunning = false
	p.channelHeartbeatStop = nil
	p.channelHeartbeatWake = nil
	p.channelHeartbeatCancel = nil
	cancel()
	close(stop)
	p.channelHeartbeatMu.Unlock()

	p.channelHeartbeatWG.Wait()
}

func (p *Manager) wakeChannelHeartbeat() {
	p.channelHeartbeatMu.Lock()
	defer p.channelHeartbeatMu.Unlock()
	if !p.channelHeartbeatRunning {
		return
	}
	select {
	case p.channelHeartbeatWake <- struct{}{}:
	default:
	}
}

func (p *Manager) channelHeartbeatThread(ctx context.Context, stop <-chan struct{}, wake <-chan struct{}) {
	defer p.channelHeartbeatWG.Done()
	ticker := time.NewTicker(channelHeartbeatInterval)
	defer ticker.Stop()

	p.runChannelHeartbeatTick(ctx)
	for {
		select {
		case <-stop:
			return
		case <-wake:
			p.runChannelHeartbeatTick(ctx)
		case <-ticker.C:
			p.runChannelHeartbeatTick(ctx)
		}
	}
}

func (p *Manager) runChannelHeartbeatTick(contexts ...context.Context) {
	ctx := context.Background()
	if len(contexts) != 0 && contexts[0] != nil {
		ctx = contexts[0]
	}
	p.channelIdentityMu.RLock()
	p.mutex.RLock()
	if p.wallet == nil || p.serverNode == nil || p.serverNode.RPCClient() == nil {
		p.mutex.RUnlock()
		p.channelIdentityMu.RUnlock()
		return
	}
	if _, monitor := p.wallet.(*MonitorWallet); monitor {
		p.mutex.RUnlock()
		p.channelIdentityMu.RUnlock()
		return
	}
	localWallet := p.wallet.Clone()
	serverPubKey := p.serverNode.Pubkey
	client := p.serverNode.RPCClient()
	mode := ""
	if p.cfg != nil {
		mode = p.cfg.Mode
	}
	p.mutex.RUnlock()
	p.channelIdentityMu.RUnlock()
	if localWallet == nil || serverPubKey == nil || localWallet.GetPaymentPubKey() == nil ||
		localWallet.GetNodePubKey() == nil {
		return
	}

	pubKey := localWallet.GetPaymentPubKey().SerializeCompressed()
	channelID, err := GetP2WSHaddress(serverPubKey.SerializeCompressed(), pubKey)
	if err != nil {
		Log.Warningf("channel heartbeat derive channel id failed: %v", err)
		return
	}
	channel := p.GetActiveChannelWithId(channelID)
	if channel != nil {
		channel.Mutex.Lock()
		if channel.IsBusy() {
			channel.Mutex.Unlock()
			return
		}
		channel.Mutex.Unlock()
	}

	req := &wwire.PingReq{PingRequest: wwire.PingRequest{
		MsgHeader: wwire.NewMsgHeader(),
		PubKey:    pubKey,
		Mode:      mode,
		NodeId:    localWallet.GetNodePubKey().SerializeCompressed(),
	}}
	if channel != nil {
		channel.Mutex.Lock()
		channel.CalcAssetsMerkleRoot()
		req.Channel = &wwire.AbbrChannelInfo{
			Version:               channel.Version,
			ChannelId:             channel.ChannelId,
			CommitHeight:          channel.CommitHeight,
			LocalAssetMerkleRoot:  append([]byte(nil), channel.LocalAssetsMerkleRoot...),
			RemoteAssetMerkleRoot: append([]byte(nil), channel.RemoteAssetsMerkleRoot...),
			StaticMerkleRoot:      append([]byte(nil), channel.StaticMerkleRoot...),
		}
		channel.Mutex.Unlock()
	}

	msg, err := json.Marshal(req.PingRequest)
	if err != nil {
		Log.Errorf("channel heartbeat marshal failed: %v", err)
		return
	}
	req.Sig, err = localWallet.SignMessageWithIndex(msg, 0)
	if err != nil {
		Log.Errorf("channel heartbeat sign failed: %v", err)
		return
	}

	var result *wwire.PingResp
	if contextClient, ok := client.(interface {
		SendPingReqContext(context.Context, *wwire.PingReq) (*wwire.PingResp, error)
	}); ok {
		result, err = contextClient.SendPingReqContext(ctx, req)
	} else {
		result, err = client.SendPingReq(req)
	}
	if err != nil {
		Log.Warningf("channel heartbeat ping failed: %v", err)
		return
	}
	if result == nil || result.Code < 0 || result.PingResponse == nil {
		if result != nil && result.Code < 0 {
			Log.Warningf("channel heartbeat rejected: %s", result.Msg)
		}
		return
	}
	if result.NextAction != wwire.STP_ACTION_SYNC {
		return
	}
	if channel != nil {
		channel.Mutex.RLock()
		localHeight := channel.CommitHeight
		channel.Mutex.RUnlock()
		if localHeight > result.CommitHeight {
			Log.Errorf("channel heartbeat refused rollback: local commit height %d exceeds remote %d",
				localHeight, result.CommitHeight)
			return
		}
	}
	if p.hasPendingClosingReservation(channelID, localWallet.GetWalletId()) {
		Log.Warningf("channel heartbeat skipped sync while the current channel is closing")
		return
	}
	if p.hasPendingFundingReservation(channelID, localWallet.GetWalletId()) {
		Log.Warningf("channel heartbeat skipped sync while channel funding is pending")
		return
	}

	if ctx.Err() != nil {
		return
	}

	if err := p.syncChannelForWallet(ctx, result.ActionParam, client, localWallet); err != nil {
		Log.Errorf("channel heartbeat sync failed: %v", err)
		return
	}
	p.SendMessageToUpper(MSG_CHANNEL_RESTORED, channelID)
}
