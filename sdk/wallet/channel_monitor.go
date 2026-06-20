package wallet

import (
	"strings"
	"time"

	"github.com/btcsuite/btcd/wire"
)

func IsTimeout(t1, t2 int64) bool {
	if ENABLE_TESTING {
		return t1-t2 > 300*int64(time.Second.Microseconds())
	}
	return t1-t2 > 120*int64(time.Second.Microseconds())
}

func (p *Manager) FundingUtxoSpent(channel *Channel, tx *wire.MsgTx) {
	utxos := make([]string, 0, len(tx.TxIn))
	for _, txIn := range tx.TxIn {
		utxos = append(utxos, txIn.PreviousOutPoint.String())
	}
	p.GetWatchTower().UtxoSpent(channel, utxos)
}

func (p *Manager) ChanPointSpent(channel *Channel, chanPoint string) {
	p.GetWatchTower().UtxoSpent(channel, []string{chanPoint})
}

func (p *Manager) enableUtxosInChannel(channelId, txId string, needLock bool) {
	channel := p.GetChannel(channelId)
	if channel == nil {
		Log.Warnf("enableUtxosInChannel can't find channel %s", channelId)
		return
	}
	if needLock {
		channel.Mutex.Lock()
		defer channel.Mutex.Unlock()
	}
	channel.EnableUtxo_SatsNet(txId)
	_ = p.SaveChannelToDB(channel)
}

func (p *Manager) BroadcastTxsIrreversibleL1(txs []*wire.MsgTx, action string) (bool, error) {
	err := p.BroadcastTxs(txs)
	if err == nil {
		return true, nil
	}
	if !isBroadcastResultUnknown(err) {
		return false, err
	}
	if p.areL1TxsVisible(txs) {
		Log.Warnf("%s L1 broadcast returned an unknown network error, but txs are visible. %v", action, err)
		p.lockL1Txs(txs)
		return true, nil
	}
	Log.Warnf("%s L1 broadcast result is unknown and txs are not visible yet. Keep pending for retry. %v", action, err)
	p.lockL1Txs(txs)
	return false, nil
}

func (p *Manager) lockL1Txs(txs []*wire.MsgTx) {
	for _, tx := range txs {
		if tx == nil {
			continue
		}
		p.GetUtxoLocker().LockUtxosWithTx(tx)
	}
}

func (p *Manager) areL1TxsVisible(txs []*wire.MsgTx) bool {
	for _, tx := range txs {
		if tx == nil {
			continue
		}
		if _, err := p.GetIndexerRPCClient().GetRawTx(tx.TxID()); err != nil {
			Log.Warnf("L1 tx %s is not visible after unknown broadcast result. %v", tx.TxID(), err)
			return false
		}
	}
	return true
}

func isBroadcastResultUnknown(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	unknownMarkers := []string{
		"eof",
		"timeout",
		"tls handshake timeout",
		"connection reset",
		"connection refused",
		"broken pipe",
		"server closed idle connection",
		"unexpected eof",
		"client.timeout exceeded",
		"bad gateway",
		"gateway timeout",
		"service unavailable",
		"no such host",
	}
	for _, marker := range unknownMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func (p *Manager) isL2TxVisible(txId string) bool {
	if txId == "" {
		return false
	}
	_, err := p.GetIndexerRPCClient_SatsNet().GetRawTx(txId)
	return err == nil
}

func (p *Manager) handleChannelActionTimeout(id int64, action string) {
	switch action {
	case RESV_TYPE_OPEN:
		resv, ok := p.GetFundingReservation(id)
		if !ok {
			return
		}
		if shouldIgnoreFailedChannelActionResult(action, resv, resv.Channel) {
			return
		}
		p.DelResvWithId(resv.Id)
		_ = DeleteReservation(p.db, resv.GetType(), resv.Id)
	case RESV_TYPE_CLOSE:
		resv, ok := p.GetClosingReservation(id)
		if !ok {
			return
		}
		if shouldIgnoreFailedChannelActionResult(action, resv, resv.Channel) {
			return
		}
		if channel, err := p.LoadBackupChannel(resv.ChannelId); err == nil {
			_ = p.SaveChannelToDB(channel)
			p.EnableChannel(channel)
		} else {
			Log.Errorf("can't restore backup channel %s, %v", resv.ChannelId, err)
			return
		}
		p.DelResvWithId(resv.Id)
		_ = DeleteReservation(p.db, resv.GetType(), resv.Id)
	case RESV_TYPE_PAYMENT:
		resv, ok := p.GetPaymentReservation(id)
		if !ok {
			return
		}
		if shouldIgnoreFailedChannelActionResult(action, resv, resv.Channel) {
			return
		}
		if channel, err := p.LoadBackupChannel(resv.ChannelId); err == nil {
			_ = p.SaveChannelToDB(channel)
			p.EnableChannel(channel)
		} else {
			Log.Errorf("can't restore backup channel %s, %v", resv.ChannelId, err)
			return
		}
		p.DelResvWithId(resv.Id)
		_ = DeleteReservation(p.db, resv.GetType(), resv.Id)
	case RESV_TYPE_SPLICING:
		resv, ok := p.GetSplicingReservation(id)
		if !ok {
			return
		}
		if shouldIgnoreFailedChannelActionResult(action, resv, resv.Channel) {
			return
		}
		if channel, err := p.LoadBackupChannel(resv.ChannelId); err == nil {
			_ = p.SaveChannelToDB(channel)
			p.EnableChannel(channel)
		} else {
			Log.Errorf("can't restore backup channel %s, %v", resv.ChannelId, err)
			return
		}
		p.DelResvWithId(resv.Id)
		_ = DeleteReservation(p.db, resv.GetType(), resv.Id)
	}
}

func shouldIgnoreFailedChannelActionResult(action string, resv Reservation, channel *Channel) bool {
	if resv == nil {
		return false
	}
	status := resv.GetStatus()
	if status <= RS_CLOSED || status == RS_CONFIRMED {
		return true
	}
	switch action {
	case RESV_TYPE_OPEN:
		return channel != nil && channel.Status >= CS_FUNDING_BROADCASTED
	case RESV_TYPE_CLOSE:
		return channel != nil && channel.Status >= CS_CLOSING_STARTED && channel.Status <= CS_CLOSE_FORCELY_SWEEP_CONFIRMED
	case RESV_TYPE_PAYMENT:
		return status >= RS_PAYMENT_STARTED
	case RESV_TYPE_SPLICING:
		return (status >= RS_SPLICINGIN_STARTED && status <= RS_SPLICINGIN_ANCHOR_BROADCASTED) ||
			(status >= RS_SPLICINGOUT_STARTED && status <= RS_SPLICINGOUT_BROADCASTED)
	default:
		return false
	}
}

func (p *Manager) HandleChannelReservationStatus(sendTxInL1 bool) {
	now := time.Now().UnixMicro()

	if sendTxInL1 {
		for _, resv := range p.GetFundingReservations() {
			if resv == nil || resv.Channel == nil {
				continue
			}
			if resv.Channel.ResvId == resv.Id && IsTimeout(now, resv.Id) {
				Log.Warnf("funding timeout %d", resv.Id)
				p.handleChannelActionTimeout(resv.Id, RESV_TYPE_OPEN)
				continue
			}
			switch resv.Channel.Status {
			case CS_FUNDING_BROADCASTED, CS_FUNDING_CONFIRMED:
				txId := resv.Channel.GetChanPoint().TxID()
				if p.GetIndexerRPCClient().IsTxConfirmed(txId) {
					Log.Infof("L1 Tx confirmed: %s", txId)
					_ = p.HandleChannelConfirmed(resv)
				} else if resv.Channel.Status == CS_FUNDING_BROADCASTED && resv.NeedSendFundingTx && resv.FundingTx != nil {
					_, _ = p.BroadcastTxsIrreversibleL1([]*wire.MsgTx{resv.FundingTx}, "open funding retry")
				}
			}
		}

		for _, resv := range p.GetSplicingReservations() {
			if resv == nil || resv.Channel == nil {
				continue
			}
			if resv.Channel.ResvId == resv.Id && IsTimeout(now, resv.Id) {
				Log.Warnf("splicing timeout %d", resv.Id)
				p.handleChannelActionTimeout(resv.Id, RESV_TYPE_SPLICING)
				continue
			}
			switch resv.Status {
			case RS_SPLICINGIN_STARTED:
				_ = p.HandleSplicingInStarted(resv)
			case RS_SPLICINGIN_BROADCASTED, RS_SPLICINGIN_CONFIRMED:
				if !resv.NeedSendSplicingTx {
					_ = p.HandleSplicingInConfirmed(resv)
				} else {
					txId := resv.SplicingTx.TxID()
					if p.GetIndexerRPCClient().IsTxConfirmed(txId) {
						Log.Infof("L1 Tx confirmed: %s", txId)
						_ = p.HandleSplicingInConfirmed(resv)
					}
				}
			case RS_SPLICINGOUT_BROADCASTED:
				txId := resv.SplicingTx.TxID()
				if p.GetIndexerRPCClient().IsTxConfirmed(txId) {
					Log.Infof("L1 Tx confirmed: %s", txId)
					_ = p.HandleSplicingOutChannelReady(resv)
				}
			}
		}

		for _, resv := range p.GetClosingReservations() {
			if resv == nil || resv.Channel == nil {
				continue
			}
			if resv.Channel.ResvId == resv.Id && IsTimeout(now, resv.Id) {
				Log.Warnf("closing timeout %d", resv.Id)
				p.handleChannelActionTimeout(resv.Id, RESV_TYPE_CLOSE)
				continue
			}
			switch resv.Channel.Status {
			case CS_CLOSING_STARTED:
				_ = p.HandleClosingStarted(resv)
			case CS_CLOSING_BROADCASTED, CS_CLOSING_CONFIRMED:
				txId := resv.Channel.ClosingTx.TxID()
				if p.GetIndexerRPCClient().IsTxConfirmed(txId) {
					Log.Infof("L1 Tx confirmed: %s", txId)
					_ = p.HandleChannelClosed(resv)
				}
			case CS_CLOSE_FORCELY_BROADCASTED:
				if resv.DeAnchorTx != nil && !p.GetIndexerRPCClient_SatsNet().IsTxConfirmed(resv.DeAnchorTx.TxID()) {
					_, _ = p.BroadcastTx_SatsNet(resv.DeAnchorTx)
				}
				txId := resv.Channel.ClosingTx.TxID()
				if p.GetIndexerRPCClient().IsTxConfirmed(txId) {
					Log.Infof("L1 Tx confirmed: %s", txId)
					_ = p.HandleChannelForceCloseConfirmed(resv)
				} else if resv.Channel.LocalCommitment.CommitTx != nil {
					txs := append([]*wire.MsgTx{}, resv.Channel.LocalCommitment.PrevTxs...)
					txs = append(txs, resv.Channel.LocalCommitment.CommitTx)
					txs = append(txs, resv.Channel.LocalCommitment.NextTxs...)
					_, _ = p.BroadcastTxsIrreversibleL1(txs, "force close retry")
				}
			case CS_CLOSE_FORCELY_CONFIRMED:
				height := p.GetIndexerRPCClient().GetSyncHeight()
				if height > 0 {
					_ = p.HandleChannelForceCloseWaitToSweep(resv, height)
				}
			case CS_CLOSE_FORCELY_SWEEP_BROADCASTED:
				txId := resv.Channel.ClosingTx.TxID()
				if p.GetIndexerRPCClient().IsTxConfirmed(txId) {
					Log.Infof("L1 Tx confirmed: %s", txId)
					_ = p.HandleChannelForceCloseSweepConfirmed(resv)
				}
			}
		}
		return
	}

	for _, resv := range p.GetFundingReservations() {
		if resv == nil || resv.Channel == nil {
			continue
		}
		if resv.Channel.ResvId == resv.Id && IsTimeout(now, resv.Id) {
			Log.Warnf("open timeout %d", resv.Id)
			p.handleChannelActionTimeout(resv.Id, RESV_TYPE_OPEN)
			continue
		}
		switch resv.Channel.Status {
		case CS_ANCHOR_BROADCASTED:
			txId := resv.AnchorTx.TxID()
			if p.GetIndexerRPCClient_SatsNet().IsTxConfirmed(txId) {
				Log.Infof("L2 Tx confirmed: %s", txId)
				_ = p.HandleChannelReady(resv)
			} else {
				_, _ = p.BroadcastTx_SatsNet(resv.AnchorTx)
			}
		case CS_READY:
			resv.Status = RS_CLOSED
			_ = p.SaveWalletReservation(resv)
			p.DelResvWithId(resv.Id)
		}
	}

	for _, resv := range p.GetPaymentReservations() {
		if resv == nil || resv.Channel == nil {
			continue
		}
		if resv.Channel.ResvId == resv.Id && IsTimeout(now, resv.Id) {
			Log.Warnf("unlock/lock timeout %d", resv.Id)
			p.handleChannelActionTimeout(resv.Id, RESV_TYPE_PAYMENT)
			continue
		}
		switch resv.Status {
		case RS_PAYMENT_STARTED:
			_ = p.HandlePaymentStarted(resv)
		case RS_PAYMENT_BROADCASTED:
			txId := resv.Channel.LastPaymentTxId
			if p.GetIndexerRPCClient_SatsNet().IsTxConfirmed(txId) {
				Log.Infof("L2 Tx confirmed: %s", txId)
				_ = p.HandlePaymentFinished(resv)
			} else {
				_, _ = p.BroadcastTx_SatsNet(resv.PaymentTx)
			}
		}
	}

	for _, resv := range p.GetSplicingReservations() {
		if resv == nil || resv.Channel == nil {
			continue
		}
		if resv.Channel.ResvId == resv.Id && IsTimeout(now, resv.Id) {
			Log.Warnf("splicing timeout %d", resv.Id)
			p.handleChannelActionTimeout(resv.Id, RESV_TYPE_SPLICING)
			continue
		}
		switch resv.Status {
		case RS_SPLICINGIN_ANCHOR_BROADCASTED:
			txId := resv.AnchorTxId()
			if txId == "" {
				continue
			}
			if p.GetIndexerRPCClient_SatsNet().IsTxConfirmed(txId) {
				Log.Infof("L2 Tx confirmed: %s", txId)
				_ = p.HandleSplicingInChannelReady(resv)
			} else if resv.AnchorTx != nil {
				_, _ = p.BroadcastTx_SatsNet(resv.AnchorTx)
			}
		case RS_SPLICINGOUT_STARTED:
			_ = p.HandleSplicingOutStarted(resv)
		case RS_SPLICINGOUT_ANCHOR_BROADCASTED, RS_SPLICINGOUT_ANCHOR_CONFIRMED:
			txId := resv.AnchorTx.TxID()
			if p.GetIndexerRPCClient_SatsNet().IsTxConfirmed(txId) {
				Log.Infof("L2 Tx confirmed: %s", txId)
				_ = p.HandleSplicingOutDeAnchorConfirmed(resv)
			} else {
				_, _ = p.BroadcastTx_SatsNet(resv.AnchorTx)
			}
		}
	}

	for _, resv := range p.GetClosingReservations() {
		if resv == nil || resv.Channel == nil {
			continue
		}
		if resv.Channel.ResvId == resv.Id && IsTimeout(now, resv.Id) {
			Log.Warnf("closing timeout %d", resv.Id)
			p.handleChannelActionTimeout(resv.Id, RESV_TYPE_CLOSE)
			continue
		}
		switch resv.Channel.Status {
		case CS_CLOSING_DEANCHOR_BROADCASTED, CS_ANCHOR_CONFIRMED:
			txId := resv.Channel.DeAnchorTx.TxID()
			if p.GetIndexerRPCClient_SatsNet().IsTxConfirmed(txId) {
				Log.Infof("L2 Tx confirmed: %s", txId)
				_ = p.HandleClosingConfirmed(resv)
			} else if resv.DeAnchorTx != nil {
				_, _ = p.BroadcastTx_SatsNet(resv.DeAnchorTx)
			}
		}
	}
}

func (p *Manager) HandleChannelConfirmed(resv *FundingReservation) error {
	shortChannelID, err := p.GetIndexerRPCClient().GetUtxoId(resv.Channel.ChanPoint.OutPointStr)
	if err != nil {
		Log.Warnf("GetUtxoId %s failed. %v", resv.Channel.ChanPoint.OutPointStr, err)
		return err
	}
	resv.Channel.ShortChannelID = shortChannelID
	resv.Channel.Status = CS_FUNDING_CONFIRMED
	resv.Status = ResvStatus(resv.Channel.Status)
	if err := p.SaveWalletReservation(resv); err != nil {
		return err
	}
	if err := p.SaveChannelToDB(resv.Channel); err != nil {
		return err
	}
	if resv.SkipOpeningAnchorTx {
		return p.HandleChannelReady(resv)
	}
	if _, err := p.BroadcastTx_SatsNet(resv.AnchorTx); err != nil {
		Log.Errorf("BroadCastTx anchorTx failed. %v", err)
		return err
	}
	resv.Channel.Status = CS_ANCHOR_BROADCASTED
	resv.Status = ResvStatus(resv.Channel.Status)
	if err := p.SaveWalletReservation(resv); err != nil {
		return err
	}
	return p.SaveChannelToDB(resv.Channel)
}

func (p *Manager) HandleChannelReady(resv *FundingReservation) error {
	channel := resv.Channel
	Log.Infof("handleChannelReady channel ready: %s", channel.ChannelId)
	channel.Status = CS_READY
	if err := p.SaveChannelToDB(channel); err != nil {
		return err
	}
	p.EnableChannel(channel)
	p.SendMessageToUpper(MSG_CHANNEL_OPENED, channel.ChannelId)
	if resv.IsInitiator && p.serverNode != nil && p.serverNode.RPCClient() != nil {
		_ = p.serverNode.RPCClient().SendActionResultNfty(resv.Id, RESV_TYPE_OPEN, 0, "")
	}
	resv.Status = RS_CLOSED
	if err := p.SaveWalletReservation(resv); err != nil {
		return err
	}
	p.DelResvWithId(channel.FundingTime)
	p.notifyChannelStatus(&ActionStatusEvent{
		Event:    ACTION_STATUS_EVENT_COMPLETED,
		Resv:     resv,
		ResvType: RESV_TYPE_OPEN,
		Status:   resv.Status,
	})
	return nil
}

func (p *Manager) HandlePaymentStarted(resv *PaymentReservation) error {
	Log.Infof("resv started: %s", resv.ChannelId)
	if resv.NeedSendLockTx {
		if _, err := p.BroadcastTx_SatsNet(resv.PaymentTx); err != nil {
			Log.Infof("BroadCastTx_SatsNet %s failed. %v", resv.ChannelId, err)
			return err
		}
		p.enableUtxosInChannel(resv.ChannelId, resv.PaymentTx.TxID(), true)
		resv.Status = RS_PAYMENT_BROADCASTED
		return p.SaveWalletReservation(resv)
	}
	return p.HandlePaymentFinished(resv)
}

func (p *Manager) HandlePaymentFinished(resv *PaymentReservation) error {
	Log.Infof("resv finished: %s", resv.ChannelId)
	resv.Status = RS_CLOSED
	if err := p.SaveWalletReservation(resv); err != nil {
		return err
	}
	p.SendMessageToUpper(MSG_UTXO_UNLOCKED_LOCKED, resv.ChannelId)
	if resv.IsInitiator && resv.Channel != nil && resv.Channel.PeerRPC != nil {
		_ = resv.Channel.PeerRPC.SendActionResultNfty(resv.Id, RESV_TYPE_PAYMENT, 0, "")
	}
	p.DelResvWithId(resv.Id)
	p.notifyChannelStatus(&ActionStatusEvent{
		Event:    ACTION_STATUS_EVENT_COMPLETED,
		Resv:     resv,
		ResvType: RESV_TYPE_PAYMENT,
		Status:   resv.Status,
	})
	return nil
}

func (p *Manager) HandleSplicingInStarted(resv *SplicingReservation) error {
	if resv.NeedSendSplicingTx {
		broadcasted, err := p.BroadcastTxsIrreversibleL1(append(resv.PreTxs, resv.SplicingTx), "splicing-in")
		if err != nil {
			Log.Errorf("BroadCastTx fundingTX failed. %v", err)
			return err
		}
		if !broadcasted {
			return nil
		}
		resv.Status = RS_SPLICINGIN_BROADCASTED
		return p.SaveWalletReservation(resv)
	}
	if resv.RecoverAscended {
		resv.Status = RS_SPLICINGIN_ANCHOR_CONFIRMED
		if err := p.SaveWalletReservation(resv); err != nil {
			return err
		}
		return p.HandleSplicingInChannelReady(resv)
	}
	resv.Status = RS_SPLICINGIN_CONFIRMED
	if err := p.SaveWalletReservation(resv); err != nil {
		return err
	}
	if _, err := p.BroadcastTx_SatsNet(resv.AnchorTx); err != nil {
		Log.Errorf("BroadCastTx anchorTx failed. %v", err)
		return err
	}
	resv.Status = RS_SPLICINGIN_ANCHOR_BROADCASTED
	return p.SaveWalletReservation(resv)
}

func (p *Manager) HandleSplicingInConfirmed(resv *SplicingReservation) error {
	resv.Status = RS_SPLICINGIN_CONFIRMED
	if err := p.SaveWalletReservation(resv); err != nil {
		return err
	}
	if resv.RecoverAscended {
		resv.Status = RS_SPLICINGIN_ANCHOR_CONFIRMED
		if err := p.SaveWalletReservation(resv); err != nil {
			return err
		}
		return p.HandleSplicingInChannelReady(resv)
	}
	if _, err := p.BroadcastTx_SatsNet(resv.AnchorTx); err != nil {
		Log.Errorf("BroadCastTx anchorTx failed. %v", err)
		return err
	}
	p.enableUtxosInChannel(resv.ChannelId, resv.AnchorTx.TxID(), true)
	p.ChanPointSpent(resv.Channel, resv.OldChanPoint)
	resv.Status = RS_SPLICINGIN_ANCHOR_BROADCASTED
	return p.SaveWalletReservation(resv)
}

func (p *Manager) HandleSplicingInChannelReady(resv *SplicingReservation) error {
	Log.Infof("handleSplicingInChannelReady channel ready: %s", resv.ChannelId)
	resv.Status = RS_CLOSED
	if err := p.SaveWalletReservation(resv); err != nil {
		return err
	}
	if resv.NeedSendSplicingTx {
		p.SendMessageToUpper(MSG_SPLICING_IN, resv.ChannelId)
	} else {
		p.SendMessageToUpper(MSG_EXPANDED, resv.ChannelId)
	}
	if resv.IsInitiator && resv.Channel != nil && resv.Channel.PeerRPC != nil {
		_ = resv.Channel.PeerRPC.SendActionResultNfty(resv.Id, RESV_TYPE_SPLICING, 0, "")
	}
	p.DelResvWithId(resv.Id)
	p.notifyChannelStatus(&ActionStatusEvent{
		Event:    ACTION_STATUS_EVENT_COMPLETED,
		Resv:     resv,
		ResvType: RESV_TYPE_SPLICING,
		Status:   resv.Status,
	})
	return nil
}

func (p *Manager) HandleSplicingOutStarted(resv *SplicingReservation) error {
	if _, err := p.BroadcastTx_SatsNet(resv.AnchorTx); err != nil {
		Log.Errorf("BroadCastTx_SatsNet splicingout deanchor tx failed. %v", err)
		return err
	}
	p.enableUtxosInChannel(resv.ChannelId, resv.AnchorTx.TxID(), true)
	resv.Status = RS_SPLICINGOUT_ANCHOR_BROADCASTED
	return p.SaveWalletReservation(resv)
}

func (p *Manager) HandleSplicingOutDeAnchorConfirmed(resv *SplicingReservation) error {
	resv.Status = RS_SPLICINGOUT_ANCHOR_CONFIRMED
	if err := p.SaveWalletReservation(resv); err != nil {
		return err
	}
	broadcasted, err := p.BroadcastTxsIrreversibleL1(append(resv.PreTxs, resv.SplicingTx), "splicing-out")
	if err != nil {
		Log.Errorf("BroadCastTx splicing failed. %v", err)
		return err
	}
	if !broadcasted {
		return nil
	}
	p.FundingUtxoSpent(resv.Channel, resv.SplicingTx)
	p.ChanPointSpent(resv.Channel, resv.OldChanPoint)
	resv.Status = RS_SPLICINGOUT_BROADCASTED
	return p.SaveWalletReservation(resv)
}

func (p *Manager) HandleSplicingOutChannelReady(resv *SplicingReservation) error {
	Log.Infof("handleSplicingOutChannelReady channel ready: %s", resv.ChannelId)
	resv.Status = RS_CLOSED
	if err := p.SaveWalletReservation(resv); err != nil {
		return err
	}
	p.SendMessageToUpper(MSG_SPLICING_OUT, resv.ChannelId)
	if resv.IsInitiator && resv.Channel != nil && resv.Channel.PeerRPC != nil {
		_ = resv.Channel.PeerRPC.SendActionResultNfty(resv.Id, RESV_TYPE_SPLICING, 0, "")
	}
	p.DelResvWithId(resv.Id)
	p.notifyChannelStatus(&ActionStatusEvent{
		Event:    ACTION_STATUS_EVENT_COMPLETED,
		Resv:     resv,
		ResvType: RESV_TYPE_SPLICING,
		Status:   resv.Status,
	})
	return nil
}

func (p *Manager) HandleClosingStarted(resv *ClosingReservation) error {
	txID, err := p.BroadcastTx_SatsNet(resv.DeAnchorTx)
	if err != nil {
		return err
	}
	Log.Infof("deanchor Tx broadcasted: %s", txID)
	resv.Channel.Status = CS_CLOSING_DEANCHOR_BROADCASTED
	return p.SaveChannelToDB(resv.Channel)
}

func (p *Manager) HandleClosingConfirmed(resv *ClosingReservation) error {
	resv.Channel.Status = CS_ANCHOR_CONFIRMED
	if err := p.SaveChannelToDB(resv.Channel); err != nil {
		return err
	}
	broadcasted, err := p.BroadcastTxsIrreversibleL1(append(resv.PreTxs, resv.Channel.ClosingTx), "cooperative close")
	if err != nil {
		Log.Errorf("BroadCastTx closingTx failed. %v", err)
		return err
	}
	if !broadcasted {
		return nil
	}
	resv.Channel.Status = CS_CLOSING_BROADCASTED
	return p.SaveChannelToDB(resv.Channel)
}

func (p *Manager) HandleChannelClosed(resv *ClosingReservation) error {
	Log.Infof("channel closed: %s", resv.ChannelId)
	p.DelResvWithId(resv.Id)
	resv.Channel.Status = CS_CLOSED
	if err := p.SaveChannelToDB(resv.Channel); err != nil {
		return err
	}
	p.SendMessageToUpper(MSG_CHANNEL_CLOSED, resv.ChannelId)
	if resv.IsInitiator && resv.Channel != nil && resv.Channel.PeerRPC != nil {
		_ = resv.Channel.PeerRPC.SendActionResultNfty(resv.Id, RESV_TYPE_CLOSE, 0, "")
	}
	p.notifyChannelStatus(&ActionStatusEvent{
		Event:    ACTION_STATUS_EVENT_COMPLETED,
		Resv:     resv,
		ResvType: RESV_TYPE_CLOSE,
		Status:   resv.Status,
	})
	return nil
}
