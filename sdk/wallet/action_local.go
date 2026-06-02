package wallet

import (
	"fmt"
	"time"
)

func NewLocalActionPerformData(id int64, action string, actionParam any,
	feeRate int64, reqPubKey []byte) (*LocalActionPerformData, error) {

	resv := &LocalActionPerformData{
		ReservationBase: newReservationBase(id, RS_INIT, nil),
		Action:          action,
		ActionParam:     actionParam,
		FeeRate:         feeRate,
		ReqTime:         time.Now().Unix(),
		ReqPubKey:       reqPubKey,
	}

	switch action {
	case LOCAL_ACTION_CONFIRM_TX:
		txId, ok := actionParam.(string)
		if !ok {
			return nil, fmt.Errorf("parameter is not string")
		}
		resv.TxId = txId
		resv.IsL1Tx = true
		resv.Status = RS_PERFORM_ACTION_TX_BROADCASTED

	case LOCAL_ACTION_CONFIRM_TX_L2:
		txId, ok := actionParam.(string)
		if !ok {
			return nil, fmt.Errorf("parameter is not string")
		}
		resv.TxId = txId
		resv.IsL1Tx = false
		resv.Status = RS_PERFORM_ACTION_TX_BROADCASTED

	case LOCAL_ACTION_UNSTAKE_MINER:
		if _, ok := actionParam.(*LocalActionParam_UnstakeMiner); !ok {
			return nil, fmt.Errorf("parameter is not LocalActionParam_UnstakeMiner")
		}

	default:
		return nil, fmt.Errorf("local action %s requires STP manager", action)
	}

	return resv, nil
}

func CompleteLocalActionAfterTxConfirmed(action string) (ResvStatus, error) {
	switch action {
	case LOCAL_ACTION_CONFIRM_TX, LOCAL_ACTION_CONFIRM_TX_L2:
		return RS_PERFORM_ACTION_COMPLETED, nil
	case LOCAL_ACTION_UNSTAKE_MINER:
		return RS_PERFORM_ACTION_TX_CONFIRMED, nil
	default:
		return RS_CLOSED, fmt.Errorf("local action %s requires STP manager", action)
	}
}

func (p *Manager) PerformLocalAction(action string, actionParam any,
	feeRate int64) (string, int64, error) {

	Log.Infof("PerformLocalAction %s", action)
	if p.wallet == nil {
		return "", -1, fmt.Errorf("wallet is not created/unlocked")
	}
	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	resv, err := NewLocalActionPerformData(p.GenerateNewResvId(), action, actionParam,
		feeRate, p.wallet.GetPaymentPubKey().SerializeCompressed())
	if err != nil {
		return "", -1, err
	}
	resv.localWallet = p.wallet.Clone()
	resv.WalletId = p.wallet.GetWalletId()

	if action == LOCAL_ACTION_UNSTAKE_MINER {
		if err := p.localActionUnstakeMinerStart(resv); err != nil {
			return "", -1, err
		}
	}

	p.addResv(resv)
	if err := p.SaveWalletReservation(resv); err != nil {
		return "", -1, err
	}

	return resv.TxId, resv.Id, nil
}

func (p *Manager) GetLocalAction(id int64) *LocalActionPerformData {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.localActionPerformMap[id]
}

func (p *Manager) HandleLocalActionStatus(sendTxInL1 bool) ([]*LocalActionPerformData, []*LocalActionPerformData) {
	p.mutex.RLock()
	localActionMap := make(map[int64]*LocalActionPerformData, len(p.localActionPerformMap))
	for id, resv := range p.localActionPerformMap {
		localActionMap[id] = resv
	}
	p.mutex.RUnlock()

	completed := make([]*LocalActionPerformData, 0)
	failed := make([]*LocalActionPerformData, 0)
	for _, resv := range localActionMap {
		if resv == nil || resv.Status <= RS_CLOSED {
			continue
		}
		if err := p.handleLocalActionStatus(resv, sendTxInL1); err != nil {
			Log.Errorf("handleLocalActionStatus %d failed. %v", resv.Id, err)
			if resv.Status < RS_CLOSED {
				failed = append(failed, resv)
				p.notifyActionStatus(&ActionStatusEvent{
					Event:      ACTION_STATUS_EVENT_FAILED,
					Resv:       resv,
					ResvType:   RESV_TYPE_LOCALACTION,
					Action:     resv.Action,
					Status:     resv.Status,
					Err:        err,
					SendTxInL1: sendTxInL1,
				})
			}
			continue
		}
		if resv.Status == RS_PERFORM_ACTION_COMPLETED {
			completed = append(completed, resv)
			p.notifyActionStatus(&ActionStatusEvent{
				Event:      ACTION_STATUS_EVENT_COMPLETED,
				Resv:       resv,
				ResvType:   RESV_TYPE_LOCALACTION,
				Action:     resv.Action,
				Status:     RS_PERFORM_ACTION_COMPLETED,
				SendTxInL1: sendTxInL1,
			})
			resv.Status = RS_CLOSED
			if err := p.SaveWalletReservation(resv); err != nil {
				Log.Errorf("SaveWalletReservation %d failed. %v", resv.Id, err)
			}
		} else if resv.Status < RS_CLOSED {
			failed = append(failed, resv)
			p.notifyActionStatus(&ActionStatusEvent{
				Event:      ACTION_STATUS_EVENT_FAILED,
				Resv:       resv,
				ResvType:   RESV_TYPE_LOCALACTION,
				Action:     resv.Action,
				Status:     resv.Status,
				SendTxInL1: sendTxInL1,
			})
		}
	}

	return completed, failed
}

func (p *Manager) handleLocalActionStatus(resv *LocalActionPerformData, sendTxInL1 bool) error {
	if resv.Status == RS_PERFORM_ACTION_TX_BROADCASTED {
		if resv.IsL1Tx != sendTxInL1 {
			return nil
		}
		txId := resv.TxId
		if txId == "" {
			return nil
		}
		confirmed := false
		if sendTxInL1 {
			confirmed = p.l1IndexerClient.IsTxConfirmed(txId)
		} else {
			confirmed = p.l2IndexerClient.IsTxConfirmed(txId)
		}
		if !confirmed {
			return nil
		}
		Log.Infof("local action tx confirmed: %s", txId)
		return p.HandleLocalActionTxConfirmed(resv.Id)
	}

	if resv.Status >= RS_PERFORM_ACTION_TX_CONFIRMED && resv.Status < RS_PERFORM_ACTION_COMPLETED {
		return p.HandleLocalActionTxConfirmed(resv.Id)
	}

	return nil
}

func (p *Manager) HandleLocalActionTxConfirmed(id int64) error {
	resv := p.GetLocalAction(id)
	if resv == nil {
		return fmt.Errorf("local action %d not found", id)
	}
	resv.Lock()
	defer resv.Unlock()

	resv.Status = RS_PERFORM_ACTION_TX_CONFIRMED
	switch resv.Action {
	case LOCAL_ACTION_CONFIRM_TX, LOCAL_ACTION_CONFIRM_TX_L2:
		status, err := CompleteLocalActionAfterTxConfirmed(resv.Action)
		if err != nil {
			return err
		}
		resv.Status = status
		return p.SaveWalletReservation(resv)
	case LOCAL_ACTION_UNSTAKE_MINER:
		if err := p.localActionInnerStatusUnstakeMiner(resv); err != nil {
			return err
		}
		return p.SaveWalletReservation(resv)
	default:
		return fmt.Errorf("local action %s requires STP manager", resv.Action)
	}
}

func (p *Manager) HasLocalAction(action string) bool {
	return p.hasLocalAction(action)
}

func (p *Manager) hasLocalAction(action string) bool {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	for _, resv := range p.localActionPerformMap {
		if resv.Action == action && resv.Status != RS_CLOSED && resv.Status != RS_PERFORM_ACTION_COMPLETED {
			return true
		}
	}
	return false
}
