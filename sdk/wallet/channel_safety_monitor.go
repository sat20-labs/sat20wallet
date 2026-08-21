package wallet

import (
	"context"
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

// HandleChannelSafetyStatus is the SDK-owned client watchtower tick. It must
// work without a PWA/Agent call and without an upper transcend monitor.
func (p *Manager) HandleChannelSafetyStatus() {
	if p == nil || p.GetIndexerRPCClient() == nil {
		return
	}

	funding := make(map[string]*Channel)
	for _, channel := range p.GetAllChannels() {
		if channel == nil {
			continue
		}
		channel.Mutex.RLock()
		if channel.Status == CS_READY {
			if channel.ChanPoint != nil && channel.ChanPoint.OutPointStr != "" {
				funding[channel.ChanPoint.OutPointStr] = channel
			}
			for name, outputs := range channel.FundingUtxos {
				// BRC20 funding outputs may be consumed by transfer inscription
				// construction and are not reliable channel-close sentinels.
				if name.Protocol == indexer.PROTOCOL_NAME_BRC20 {
					continue
				}
				for _, output := range outputs {
					if output != nil && output.OutPointStr != "" {
						funding[output.OutPointStr] = channel
					}
				}
			}
		}
		channel.Mutex.RUnlock()
	}
	if len(funding) == 0 {
		return
	}

	outpoints := make([]string, 0, len(funding))
	for outpoint := range funding {
		outpoints = append(outpoints, outpoint)
	}
	existing, err := p.GetIndexerRPCClient().GetExistingUtxos(outpoints)
	if err != nil {
		Log.Warnf("channel safety monitor GetExistingUtxos failed. %v", err)
		return
	}
	for _, outpoint := range existing {
		delete(funding, outpoint)
	}
	for outpoint, channel := range funding {
		if p.shouldDeferMissingChannelFunding(outpoint) {
			continue
		}
		if err := p.HandleUnexpectedChannelClose(channel, "", outpoint); err != nil {
			Log.Errorf("channel %s unexpected close handling failed for %s. %v",
				channel.ChannelId, outpoint, err)
		}
	}
}

func (p *Manager) shouldDeferMissingChannelFunding(outpoint string) bool {
	txid, _, ok := strings.Cut(outpoint, ":")
	if !ok || txid == "" {
		return false
	}
	info, err := p.GetIndexerRPCClient().GetTxInfo(txid)
	if err == nil {
		return info != nil && info.Confirmations == 0
	}
	if !isChannelFundingTxNotIndexedYetError(err) {
		return false
	}
	rawTx, rawErr := p.GetIndexerRPCClient().GetRawTx(txid)
	return rawErr == nil && rawTx != ""
}

func isChannelFundingTxNotIndexedYetError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not be indexed yet") ||
		strings.Contains(msg, "not indexed yet")
}

type channelSafetyGeneration struct {
	channelID  string
	status     ChannelStatus
	height     int
	localTxID  string
	remoteTxID string
}

func captureChannelSafetyGeneration(channel *Channel) (channelSafetyGeneration, error) {
	channel.Mutex.RLock()
	defer channel.Mutex.RUnlock()
	if channel.LocalCommitment == nil || channel.LocalCommitment.CommitTx == nil ||
		channel.RemoteCommitment == nil || channel.RemoteCommitment.CommitTx == nil {
		return channelSafetyGeneration{}, fmt.Errorf("channel %s commitment state is incomplete", channel.ChannelId)
	}
	return channelSafetyGeneration{
		channelID: channel.ChannelId, status: channel.Status, height: channel.CommitHeight,
		localTxID:  channel.LocalCommitment.CommitTx.TxID(),
		remoteTxID: channel.RemoteCommitment.CommitTx.TxID(),
	}, nil
}

func channelSafetyGenerationMatches(channel *Channel, expected channelSafetyGeneration) bool {
	return channel != nil && channel.ChannelId == expected.channelID &&
		channel.CommitHeight == expected.height &&
		channel.LocalCommitment != nil && channel.LocalCommitment.CommitTx != nil &&
		channel.RemoteCommitment != nil && channel.RemoteCommitment.CommitTx != nil &&
		channel.LocalCommitment.CommitTx.TxID() == expected.localTxID &&
		channel.RemoteCommitment.CommitTx.TxID() == expected.remoteTxID
}

func channelSafetyTransitionMatches(channel *Channel, expected channelSafetyGeneration) bool {
	return channelSafetyGenerationMatches(channel, expected) && channel.Status == expected.status
}

// HandleUnexpectedChannelClose identifies the commitment that spent channel
// funding and lets the SDK complete the corresponding recovery automatically.
func (p *Manager) HandleUnexpectedChannelClose(channel *Channel, txid string, spentOutpoints ...string) error {
	if channel == nil {
		return fmt.Errorf("channel is nil")
	}
	generation, err := captureChannelSafetyGeneration(channel)
	if err != nil {
		return err
	}
	if generation.status <= CS_CLOSED {
		return nil
	}
	if txid == "" {
		switch {
		case p.GetIndexerRPCClient().IsTxConfirmed(generation.remoteTxID):
			txid = generation.remoteTxID
		case p.GetIndexerRPCClient().IsTxConfirmed(generation.localTxID):
			return nil // The persisted force-close reservation handles our tx.
		default:
			tower := p.GetWatchTower()
			if tower == nil {
				return fmt.Errorf("watchtower is not initialized")
			}
			txid = tower.FindConfirmedCommitTxBySpentUtxos(generation.channelID, spentOutpoints)
			if txid == "" {
				return fmt.Errorf("funding outpoint spent but no confirmed commitment found: %v", spentOutpoints)
			}
		}
	}

	switch txid {
	case generation.localTxID:
		return nil
	case generation.remoteTxID:
		return p.finalizePeerForceClose(channel, generation, txid)
	default:
		return p.handleRevokedCommitment(channel, generation, txid)
	}
}

func (p *Manager) finalizePeerForceClose(channel *Channel, generation channelSafetyGeneration, txid string) error {
	if err := p.persistTerminalTransition(channel, generation, CS_CLOSED_FORCELY); err != nil {
		return err
	}
	p.DisableChannel(channel)
	p.SendMessageToUpper(MSG_CHANNEL_FORCELY_CLOSED, txid)
	if tower := p.GetWatchTower(); tower != nil {
		return tower.CleanAllCommitTx(channel.ChannelId)
	}
	return nil
}

func (p *Manager) handleRevokedCommitment(channel *Channel, generation channelSafetyGeneration, commitTxID string) error {
	tower := p.GetWatchTower()
	if tower == nil {
		return fmt.Errorf("watchtower is not initialized")
	}
	storedChannelID, txs, err := tower.GetPunishTx(commitTxID)
	if err != nil {
		return err
	}
	if storedChannelID != generation.channelID {
		return fmt.Errorf("commit tx %s does not belong to channel %s", commitTxID, generation.channelID)
	}
	// Persist retry intent before changing terminal channel state. A restart in
	// any later failure window can therefore resume the idempotent broadcast.
	if err := tower.markPunishPending(commitTxID); err != nil {
		return err
	}
	if err := p.persistTerminalTransition(channel, generation, CS_CLOSED_UNEXPECTED); err != nil {
		_ = tower.SetBroadcastedFlag(commitTxID)
		return err
	}
	p.DisableChannel(channel)
	p.SendMessageToUpper(MSG_CHANNEL_UNEXPECTEDLY_CLOSED, commitTxID)

	broadcasted, err := p.broadcastPunishPackage(txs, "punish")
	if err != nil || !broadcasted {
		if flagErr := tower.SetBroadcastedFlag(commitTxID); flagErr != nil {
			return flagErr
		}
		return err
	}
	return p.completePunishBroadcast(generation.channelID, commitTxID, lastSafetyTxIdFromTxs(txs))
}

func (p *Manager) completePunishBroadcast(channelID, commitTxID, punishTxID string) error {
	if err := p.completePunishBroadcastState(channelID, commitTxID); err != nil {
		return err
	}
	p.SendMessageToUpper(MSG_CHANNEL_PUNISHED, punishTxID)
	return nil
}

func (p *Manager) completePunishBroadcastState(channelID, commitTxID string) error {
	channel := p.FindChannel(channelID)
	if channel == nil {
		return fmt.Errorf("channel %s not found while completing punish", channelID)
	}
	generation, err := captureChannelSafetyGeneration(channel)
	if err != nil {
		return err
	}
	if err := p.persistTerminalTransition(channel, generation, CS_CLOSED_UNEXPECTED); err != nil {
		return err
	}
	p.DisableChannel(channel)
	tower := p.GetWatchTower()
	if tower == nil {
		return fmt.Errorf("watchtower is not initialized")
	}
	if err := tower.completePunishCleanup(channelID, commitTxID); err != nil {
		return err
	}
	return nil
}

func (p *Manager) persistTerminalTransition(channel *Channel, expected channelSafetyGeneration, status ChannelStatus) error {
	channel.Mutex.Lock()
	if !channelSafetyTransitionMatches(channel, expected) {
		channel.Mutex.Unlock()
		return fmt.Errorf("channel %s changed during safety handling", expected.channelID)
	}
	previousStatus := channel.Status
	channel.Status = status
	buf, encodeErr := EncodeToBytes(&channel.ChannelInDB)
	channel.Mutex.Unlock()
	if encodeErr != nil {
		p.revertTerminalTransition(channel, expected, status, previousStatus)
		return encodeErr
	}

	var snapshot ChannelInDB
	if err := DecodeFromBytes(buf, &snapshot); err != nil {
		p.revertTerminalTransition(channel, expected, status, previousStatus)
		return err
	}
	if err := p.SaveChannelInDB(&snapshot); err != nil {
		stored, loadErr := p.LoadChannelInDB(expected.channelID)
		if loadErr != nil || stored.Status != status {
			p.revertTerminalTransition(channel, expected, status, previousStatus)
			return fmt.Errorf("persist terminal channel %s failed: %w", expected.channelID, err)
		}
	}
	p.mutex.RLock()
	backupHandler := p.channelBackupHandler
	p.mutex.RUnlock()
	if backupHandler != nil {
		if err := backupHandler.BackupChannel(channel, buf); err != nil {
			Log.Warningf("backup terminal channel %s failed. %v", expected.channelID, err)
		}
	}
	return nil
}

func (p *Manager) revertTerminalTransition(channel *Channel, expected channelSafetyGeneration, status, previous ChannelStatus) {
	channel.Mutex.Lock()
	defer channel.Mutex.Unlock()
	if channelSafetyGenerationMatches(channel, expected) && channel.Status == status {
		channel.Status = previous
	}
}

func (p *Manager) broadcastPunishPackage(txs []*wire.MsgTx, action string) (bool, error) {
	return p.broadcastPunishPackageContext(context.Background(), txs, action)
}

func (p *Manager) broadcastPunishPackageContext(ctx context.Context, txs []*wire.MsgTx, action string) (bool, error) {
	for _, tx := range txs {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if tx == nil {
			continue
		}
		if p.isL1TxVisibleContext(ctx, tx.TxID()) {
			continue
		}
		broadcasted, err := p.BroadcastTxsIrreversibleL1Context(ctx, []*wire.MsgTx{tx}, action)
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			if p.isL1TxVisibleContext(ctx, tx.TxID()) {
				continue
			}
			return false, err
		}
		if !broadcasted && !p.isL1TxVisibleContext(ctx, tx.TxID()) {
			return false, nil
		}
	}
	return true, nil
}

func (p *Manager) isL1TxVisible(txID string) bool {
	return p.isL1TxVisibleContext(context.Background(), txID)
}

func (p *Manager) isL1TxVisibleContext(ctx context.Context, txID string) bool {
	if txID == "" || p.GetIndexerRPCClient() == nil {
		return false
	}
	_, err := getRawTxWithContext(ctx, p.GetIndexerRPCClient(), txID)
	return err == nil
}

func lastSafetyTxIdFromTxs(txs []*wire.MsgTx) string {
	for i := len(txs) - 1; i >= 0; i-- {
		if txs[i] != nil {
			return txs[i].TxID()
		}
	}
	return ""
}
