package wallet

import (
	"encoding/json"
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

	case LOCAL_ACTION_LOCK_WITH_EXPAND:
		if _, ok := actionParam.(*LocalActionParam_Expand); !ok {
			return nil, fmt.Errorf("parameter is not LocalActionParam_Expand")
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
	} else if action == LOCAL_ACTION_LOCK_WITH_EXPAND {
		if err := p.localActionLockWithExpandStart(resv); err != nil {
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
	resv, ok := p.localActionPerformMap[id].(*LocalActionPerformData)
	if !ok {
		return nil
	}
	return resv
}

func (p *Manager) HandleLocalActionStatus(sendTxInL1 bool) ([]*LocalActionPerformData, []*LocalActionPerformData) {
	p.mutex.RLock()
	localActionMap := make(map[int64]*LocalActionPerformData, len(p.localActionPerformMap))
	for id, resv := range p.localActionPerformMap {
		action, ok := resv.(*LocalActionPerformData)
		if ok {
			localActionMap[id] = action
		}
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
			p.DelResvWithId(resv.Id)
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
	case LOCAL_ACTION_LOCK_WITH_EXPAND:
		if err := p.localActionInnerStatusLockWithExpand(resv); err != nil {
			return err
		}
		return p.SaveWalletReservation(resv)
	default:
		return fmt.Errorf("local action %s requires STP manager", resv.Action)
	}
}

func (p *Manager) localActionLockWithExpandStart(resv *LocalActionPerformData) error {
	param, ok := resv.ActionParam.(*LocalActionParam_Expand)
	if !ok {
		return fmt.Errorf("invalid parameter LocalActionParam_Expand")
	}

	channel := p.GetActiveChannel()
	if param.ChannelId != "" {
		channel = p.FindChannel(param.ChannelId)
	}
	if channel == nil {
		return fmt.Errorf("no channel")
	}
	param.ChannelId = channel.ChannelId

	remoteAmt := channel.GetCommitRemoteValue(param.AssetName)
	if remoteAmt.Cmp(param.Amt) >= 0 {
		return fmt.Errorf("no need to expand channel")
	}

	amtToLock := remoteAmt.Clone()
	amtToExpand := param.Amt.Sub(remoteAmt)

	tickInfo := p.GetTickerInfo(&param.AssetName.AssetName)
	if tickInfo == nil {
		return fmt.Errorf("can't find ticker info %s", param.AssetName.String())
	}

	contractAddr := ExtractChannelId(param.ContractURL)
	total := p.GetAssetBalance(contractAddr, &param.AssetName.AssetName)
	if total.Cmp(amtToExpand) < 0 {
		return fmt.Errorf("no enough asset %s in contract %s, require %s but only %s",
			param.AssetName.String(), param.ContractURL, amtToExpand, total.String())
	}

	txId, resvId, err := p.LockToChannel(channel.ChannelId,
		param.AssetName.String(), amtToLock.String(),
		nil, nil, []byte(LOCAL_ACTION_LOCK_WITH_EXPAND))
	if err != nil {
		return err
	}

	resv.ActionResvs = append(resv.ActionResvs, &SubActionInfo{
		ActionType: "lock",
		ResvId:     resvId,
		MoreData:   amtToExpand,
	})
	resv.TxId = txId
	resv.IsL1Tx = false
	resv.Status = RS_PERFORM_ACTION_TX_BROADCASTED
	return nil
}

func localActionExpandAmount(v any) (*Decimal, error) {
	switch amt := v.(type) {
	case *Decimal:
		return amt, nil
	case Decimal:
		return amt.Clone(), nil
	default:
		return nil, fmt.Errorf("invalid parameter Decimal amtToExpand")
	}
}

func (p *Manager) localActionInnerStatusLockWithExpand(resv *LocalActionPerformData) error {
	if len(resv.ActionResvs) == 0 {
		return fmt.Errorf("no sub actions")
	}

	param, ok := resv.ActionParam.(*LocalActionParam_Expand)
	if !ok {
		return fmt.Errorf("invalid parameter LocalActionParam_Expand")
	}
	channel := p.FindChannel(param.ChannelId)
	if channel == nil {
		return fmt.Errorf("can't find channel %s", param.ChannelId)
	}

	currResv := resv.ActionResvs[len(resv.ActionResvs)-1]
	switch currResv.ActionType {
	case "lock":
		amtToExpand, err := localActionExpandAmount(currResv.MoreData)
		if err != nil {
			return err
		}
		txId, err := p.WithdrawWithContract(channel.ChannelId,
			param.AssetName.String(), amtToExpand.String(), resv.FeeRate)
		if err != nil {
			return err
		}

		resv.ActionResvs = append(resv.ActionResvs, &SubActionInfo{
			ActionType: "withdraw",
			TxId:       txId,
			MoreData:   amtToExpand,
		})
		resv.TxId = txId
		resv.IsL1Tx = false
		resv.Status = RS_PERFORM_ACTION_TX_BROADCASTED
		return nil

	case "withdraw":
		if !resv.IsL1Tx {
			url := param.ContractURL
			if url == "" {
				var err error
				url, err = p.GetTranscendContractWithAssetNameInServer(param.AssetName.String())
				if err != nil {
					return err
				}
				param.ContractURL = url
			}

			itemStr, err := p.GetInvokeItemByInUtxoInContract(url, resv.TxId+":0")
			if err != nil {
				return err
			}
			var item InvokeItem
			if err := json.Unmarshal([]byte(itemStr), &item); err != nil {
				return err
			}
			if item.OutTxId == "" {
				return fmt.Errorf("withdraw invoke is not finished, %s", resv.TxId+":0")
			}

			resv.TxId = item.OutTxId
			resv.IsL1Tx = true
			resv.Status = RS_PERFORM_ACTION_TX_BROADCASTED
			return nil
		}

		utxo, err := p.GetUtxoWithAddressFromTx(resv.TxId, channel.Address)
		if err != nil {
			return err
		}
		txId, _, id, err := p.ExpandChannel(channel.ChannelId,
			param.AssetName.String(), utxo, "", nil)
		if err != nil {
			return err
		}

		resv.ActionResvs = append(resv.ActionResvs, &SubActionInfo{
			ActionType: "expand",
			ResvId:     id,
		})
		resv.TxId = txId
		resv.IsL1Tx = false
		resv.Status = RS_PERFORM_ACTION_TX_BROADCASTED
		return nil

	case "expand":
		resv.Status = RS_PERFORM_ACTION_COMPLETED
		return nil
	default:
		return fmt.Errorf("invalid action %s", currResv.ActionType)
	}
}

func (p *Manager) HasLocalAction(action string) bool {
	return p.hasLocalAction(action)
}

func (p *Manager) hasLocalAction(action string) bool {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	for _, resv := range p.localActionPerformMap {
		actionResv, ok := resv.(*LocalActionPerformData)
		if !ok {
			continue
		}
		if actionResv.Action == action && actionResv.Status != RS_CLOSED && actionResv.Status != RS_PERFORM_ACTION_COMPLETED {
			return true
		}
	}
	return false
}
