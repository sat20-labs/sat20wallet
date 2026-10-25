package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

const channelHeartbeatInterval = 30 * time.Second

const channelHeartbeatRebuildReadyEvent = "channelrebuildready"

type closedReopenFundingPolicy uint8

const (
	closedReopenExplicitFunding closedReopenFundingPolicy = iota
	closedReopenExistingFundingOnly
)

var errClosedChannelFundingApprovalRequired = errors.New("closed-channel recovery requires funding approval")

func closedChannelFundingApprovalRequired(channelID string) error {
	return fmt.Errorf("%w: channel %s cannot proceed using only existing channel BTC; adding account BTC or fees requires your approval",
		errClosedChannelFundingApprovalRequired, channelID)
}

// Existing indexers predate typed missing-ascend errors. Recognize only the
// concrete missing responses, never transport, storage or malformed replies.
func closedReopenAscendMissing(outpoint string, err error) bool {
	return errors.Is(err, indexer.ErrKeyNotFound) || err != nil &&
		(err.Error() == "not found" || err.Error() == fmt.Sprintf("GetAscendData %s not found", outpoint))
}

func (p *Manager) validateClosedReopenExistingFunding(channelID string, selected *TxOutput, amount int64) (*TxOutput, error) {
	if selected == nil || selected.OutPointStr == "" {
		return nil, closedChannelFundingApprovalRequired(channelID)
	}
	existing, err := p.l1IndexerClient.GetExistingUtxos([]string{selected.OutPointStr})
	if err != nil {
		return nil, fmt.Errorf("cannot revalidate channel funding availability: %w", err)
	}
	if len(existing) != 1 || existing[0] != selected.OutPointStr {
		return nil, fmt.Errorf("selected channel funding %s is no longer unspent", selected.OutPointStr)
	}
	actual, err := p.getL1TxOutput(selected.OutPointStr)
	if err != nil {
		return nil, err
	}
	script, err := GetPkScriptFromAddress(channelID)
	if err != nil {
		return nil, err
	}
	if actual == nil || actual.OutPointStr != selected.OutPointStr || actual.Value() != amount ||
		actual.HasAsset() || !bytes.Equal(actual.OutValue.PkScript, script) {
		return nil, fmt.Errorf("selected channel funding no longer matches its exact plain output, value and payment account")
	}
	if _, err := p.l2IndexerClient.GetAscendData(selected.OutPointStr); err == nil {
		return nil, fmt.Errorf("selected channel funding %s is already ascended", selected.OutPointStr)
	} else if !closedReopenAscendMissing(selected.OutPointStr, err) {
		return nil, fmt.Errorf("cannot revalidate channel funding ascend state: %w", err)
	}
	return actual, nil
}

func (p *Manager) notifyClosedChannelFundingApproval(w common.Wallet, channelID string, err error) {
	if w == nil || !errors.Is(err, errClosedChannelFundingApprovalRequired) {
		return
	}
	identity := w.GetWalletId()
	notice := fmt.Sprintf("wallet %d account %d channel %s: automatic recovery paused; %v",
		identity.Id, identity.SubAccountId, channelID, err)
	p.channelHeartbeatMu.Lock()
	if p.channelRebuildNotices == nil {
		p.channelRebuildNotices = make(map[string]string)
	}
	if p.channelRebuildNotices[channelID] == notice {
		p.channelHeartbeatMu.Unlock()
		return
	}
	p.channelRebuildNotices[channelID] = notice
	p.channelHeartbeatMu.Unlock()
	// Runtime-only deduplication, not a funding approval, quote or new owner.
	p.SendMessageToUpper(MSG_OPERATION_FAILED, notice)
	payload, marshalErr := json.Marshal(struct {
		WalletID     int64  `json:"walletId"`
		AccountIndex uint32 `json:"accountIndex"`
		ChannelID    string `json:"channelId"`
		Message      string `json:"message"`
	}{identity.Id, identity.SubAccountId, channelID, notice})
	if marshalErr == nil {
		p.SendMessageToUpper("channelrebuildfundingrequired", string(payload))
	}
}

func (p *Manager) clearClosedChannelFundingNotice(channelID string) {
	p.channelHeartbeatMu.Lock()
	delete(p.channelRebuildNotices, channelID)
	p.channelHeartbeatMu.Unlock()
}

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
	if result.NextAction != "rebuild" {
		owner, intent, ownerErr := p.closedChannelAssetRestoreOwnerForWallet(localWallet, channelID)
		if ownerErr != nil {
			Log.Warningf("channel asset owner unavailable for %s: %v", channelID, ownerErr)
			return
		}
		if owner != nil && !intent.AssetsComplete {
			p.runClosedChannelAssetRestoreTick(ctx, localWallet, client, channelID)
			return
		}
	}
	if result.NextAction == "rebuild" {
		if result.ActionParam != channelID {
			Log.Errorf("channel heartbeat refused rebuild for a different payment account")
			return
		}
		if ctx.Err() != nil {
			return
		}
		// Rebuild is a fresh handshake, never a closed peer snapshot. Keep the
		// signed Ping's captured account and require existing channel BTC only.
		if _, err := p.reopenClosedChannelForWalletWithFundingPolicy(ctx, localWallet, client, false, closedReopenExistingFundingOnly, channelID); err != nil {
			Log.Warningf("channel heartbeat rebuild paused for %s: %v", channelID, err)
			p.notifyClosedChannelFundingApproval(localWallet, channelID, err)
			return
		}
		p.clearClosedChannelFundingNotice(channelID)
		identity := localWallet.GetWalletId()
		p.SendMessageToUpper(channelHeartbeatRebuildReadyEvent, fmt.Sprintf("wallet %d account %d channel %s: channel READY; asset restoration pending",
			identity.Id, identity.SubAccountId, channelID))
		p.runClosedChannelAssetRestoreTick(ctx, localWallet, client, channelID)
		return
	}
	p.clearClosedChannelFundingNotice(channelID)
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

func (p *Manager) runClosedChannelAssetRestoreTick(ctx context.Context, w common.Wallet, client NodeRPCClient, channelID string) {
	done, err := p.driveClosedChannelAssetRestoreForWallet(ctx, w, client, channelID)
	if err != nil {
		Log.Warningf("closed-channel asset restoration paused for %s: %v", channelID, err)
		p.notifyClosedChannelFundingApproval(w, channelID, err)
		return
	}
	if done {
		p.clearClosedChannelFundingNotice(channelID)
		p.SendMessageToUpper(MSG_CHANNEL_RESTORED, channelID)
	}
}
