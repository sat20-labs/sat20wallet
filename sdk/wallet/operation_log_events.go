package wallet

import (
	"bytes"
	"strconv"
)

func operationLogCompletionMessage(event *ActionStatusEvent) (string, map[string]string) {
	details := make(map[string]string)
	if event == nil {
		return "Operation completed", details
	}
	if event.Resv != nil {
		details["reservation_id"] = strconv.FormatInt(event.Resv.GetId(), 10)
	}
	switch event.ResvType {
	case RESV_TYPE_OPEN:
		if resv, ok := event.Resv.(*FundingReservation); ok && resv != nil && resv.Channel != nil {
			details["channel_id"] = resv.Channel.ChannelId
		}
		return "Channel is ready", details
	case RESV_TYPE_CLOSE:
		if resv, ok := event.Resv.(*ClosingReservation); ok && resv != nil {
			details["channel_id"] = resv.ChannelId
			if resv.CloseTx != nil {
				details["txid"] = resv.CloseTx.TxID()
			}
		}
		return "Channel closed", details
	case RESV_TYPE_PAYMENT:
		if resv, ok := event.Resv.(*PaymentReservation); ok && resv != nil {
			details["channel_id"] = resv.ChannelId
			if resv.PaymentTx != nil {
				details["txid"] = resv.PaymentTx.TxID()
			}
			if resv.IsUnlock {
				return "Assets sent from channel", details
			}
			return "Assets locked into channel", details
		}
		return "Channel transfer completed", details
	case RESV_TYPE_SPLICING:
		if resv, ok := event.Resv.(*SplicingReservation); ok && resv != nil {
			details["channel_id"] = resv.ChannelId
			if resv.SplicingTx != nil {
				details["txid"] = resv.SplicingTx.TxID()
			}
			if !resv.NeedSendSplicingTx {
				return "Channel expansion completed", details
			}
		}
		return "Channel splice completed", details
	case RESV_TYPE_LOCALACTION:
		switch event.Action {
		case LOCAL_ACTION_LOCK_WITH_EXPAND:
			return "Channel expansion completed", details
		case LOCAL_ACTION_UNSTAKE_MINER:
			return "Miner unstake completed", details
		case LOCAL_ACTION_CONFIRM_TX, LOCAL_ACTION_CONFIRM_TX_L2:
			return "Transaction confirmed", details
		default:
			return "Wallet action completed", details
		}
	case RESV_TYPE_REMOTEACTION:
		if resv, ok := event.Resv.(*RemoteActionPerformReservation); ok && resv != nil {
			if resv.FeeTxId != "" {
				details["txid"] = resv.FeeTxId
			}
			if len(resv.ActionResult) != 0 && len(resv.ActionResult) <= 1024 {
				details["result"] = string(resv.ActionResult)
			}
		}
		switch event.Action {
		case REMOTE_ACTION_DEPLOY_CONTRACT:
			return "Contract deployment completed", details
		case REMOTE_ACTION_DEPLOY_RUNES:
			return "Runes deployment completed", details
		case REMOTE_ACTION_ASCEND:
			return "Asset ascend completed", details
		default:
			return "Remote wallet action completed", details
		}
	default:
		return "Operation completed", details
	}
}

func (p *Manager) handleOperationLogActionStatusEvent(event *ActionStatusEvent) {
	if event == nil || event.Resv == nil {
		return
	}
	reservationType := event.ResvType
	if reservationType == "" {
		reservationType = event.Resv.GetType()
	}
	reservationID := event.Resv.GetId()
	if reservationType == "" || reservationID == 0 {
		return
	}

	switch event.Event {
	case ACTION_STATUS_EVENT_COMPLETED:
		message, details := operationLogCompletionMessage(event)
		p.updateOperationLogByReservationBestEffort(reservationType, reservationID, OperationLogUpdate{
			Status:  OperationLogSucceeded,
			Message: message,
			Details: details,
			Result:  details,
			TxID:    details["txid"],
		})
	case ACTION_STATUS_EVENT_FAILED:
		details := map[string]string{"reservation_id": strconv.FormatInt(reservationID, 10)}
		message := "Operation failed"
		if event.Err != nil {
			message = event.Err.Error()
			details["error"] = event.Err.Error()
		}
		p.updateOperationLogByReservationBestEffort(reservationType, reservationID, OperationLogUpdate{
			Status:  OperationLogFailed,
			Message: message,
			Details: details,
		})
	}
}

// reconcileReadyOpenChannelOperationLogs repairs only the display log for an
// already READY channel. It does not recreate reservations, recheck L1, or
// rebroadcast transactions.
func (p *Manager) reconcileReadyOpenChannelOperationLogs() {
	logs, err := p.GetOperationLogs()
	if err != nil {
		Log.Warnf("load operation logs for ready-channel reconciliation failed: %v", err)
		return
	}
	channels, err := p.LoadAllChannelInDBFromDB()
	if err != nil {
		Log.Warnf("load channels for operation-log reconciliation failed: %v", err)
		return
	}

	p.mutex.RLock()
	currentWallet := p.wallet
	p.mutex.RUnlock()
	if currentWallet == nil || currentWallet.GetPaymentPubKey() == nil {
		return
	}
	currentWalletID := currentWallet.GetWalletId().Id
	currentPaymentKey := currentWallet.GetPaymentPubKey().SerializeCompressed()

	for _, record := range logs {
		if record == nil || record.Action != "open_channel" ||
			(record.Status != OperationLogPending && record.Status != OperationLogRunning) ||
			record.ReservationType != RESV_TYPE_OPEN || record.ReservationID == 0 {
			continue
		}
		for _, channel := range channels {
			if channel == nil || channel.Status != CS_READY || channel.FundingTime != record.ReservationID {
				continue
			}
			if channel.LocalWalletId != 0 {
				if channel.LocalWalletId != currentWalletID {
					continue
				}
			} else if channel.LocalChanCfg.PaymentKey == nil ||
				!bytes.Equal(channel.LocalChanCfg.PaymentKey.SerializeCompressed(), currentPaymentKey) {
				continue
			}
			p.updateOperationLogBestEffort(record.ID, OperationLogUpdate{
				Status:  OperationLogSucceeded,
				Message: "Channel is ready",
				Details: map[string]string{
					"reservation_id": strconv.FormatInt(record.ReservationID, 10),
					"channel_id":     channel.ChannelId,
				},
				Result: map[string]string{
					"reservation_id": strconv.FormatInt(record.ReservationID, 10),
					"channel_id":     channel.ChannelId,
				},
			})
			break
		}
	}
}
