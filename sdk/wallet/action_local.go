package wallet

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/common"
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

	logID := p.beginOperationLogBestEffort(localActionOperationLogCreate(action, actionParam, feeRate))
	resv, err := NewLocalActionPerformData(p.GenerateNewResvId(), action, actionParam,
		feeRate, p.wallet.GetPaymentPubKey().SerializeCompressed())
	if err != nil {
		p.updateOperationLogBestEffort(logID, OperationLogUpdate{
			Status: OperationLogFailed, Message: err.Error(), Details: map[string]string{"error": err.Error()},
		})
		return "", -1, err
	}
	resv.localWallet = p.wallet.Clone()
	resv.WalletId = p.wallet.GetWalletId()
	p.bindOperationLogReservationBestEffort(logID, RESV_TYPE_LOCALACTION, resv.Id)

	if action == LOCAL_ACTION_UNSTAKE_MINER {
		if err := p.localActionUnstakeMinerStart(resv); err != nil {
			p.updateOperationLogBestEffort(logID, OperationLogUpdate{
				Status: OperationLogFailed, Message: err.Error(), Details: map[string]string{"error": err.Error()},
			})
			return "", -1, err
		}
	} else if action == LOCAL_ACTION_LOCK_WITH_EXPAND {
		if err := p.localActionLockWithExpandStart(resv); err != nil {
			p.updateOperationLogBestEffort(logID, OperationLogUpdate{
				Status: OperationLogFailed, Message: err.Error(), Details: map[string]string{"error": err.Error()},
			})
			return "", -1, err
		}
	}

	p.addResv(resv)
	if err := p.SaveWalletReservation(resv); err != nil {
		p.updateOperationLogBestEffort(logID, OperationLogUpdate{
			Status: OperationLogFailed, Message: err.Error(), Details: map[string]string{"error": err.Error()},
		})
		return "", -1, err
	}

	if resv.TxId != "" && action != LOCAL_ACTION_LOCK_WITH_EXPAND {
		p.updateOperationLogBestEffort(logID, OperationLogUpdate{
			Status:  OperationLogRunning,
			Message: "Transaction submitted; waiting for confirmation",
			TxID:    resv.TxId,
			Details: map[string]string{"txid": resv.TxId},
		})
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

// ResumeLockWithExpandFromL1Tx moves an interrupted lock-with-expand action
// from its contract-withdraw stage to the existing L1 carrier.  It does not
// broadcast or create a new withdrawal; the normal monitor will verify the L1
// confirmation and run ExpandChannel on its next tick.
func (p *Manager) ResumeLockWithExpandFromL1Tx(id int64, l1TxId string) error {
	if p.wallet == nil {
		return fmt.Errorf("wallet is not created/unlocked")
	}
	if raw, err := hex.DecodeString(l1TxId); err != nil || len(raw) != 32 {
		return fmt.Errorf("invalid L1 transaction id %s", l1TxId)
	}

	resv := p.GetLocalAction(id)
	if resv == nil {
		return fmt.Errorf("local action %d not found", id)
	}
	resv.Lock()
	defer resv.Unlock()

	if resv.Action != LOCAL_ACTION_LOCK_WITH_EXPAND {
		return fmt.Errorf("local action %d is not lock-with-expand", id)
	}
	if !p.localActionBelongsToCurrentWallet(resv) {
		return fmt.Errorf("local action %d belongs to another wallet account", id)
	}
	if len(resv.ActionResvs) == 0 || resv.ActionResvs[len(resv.ActionResvs)-1].ActionType != "withdraw" {
		return fmt.Errorf("local action %d is not waiting for a withdraw carrier", id)
	}

	resv.TxId = l1TxId
	resv.IsL1Tx = true
	resv.Status = RS_PERFORM_ACTION_TX_BROADCASTED
	p.updateOperationLogByReservationBestEffort(RESV_TYPE_LOCALACTION, resv.Id, OperationLogUpdate{
		Status:  OperationLogRunning,
		Message: "Bitcoin carrier transaction attached; waiting for confirmation",
		TxID:    l1TxId,
		Details: map[string]string{"txid": l1TxId},
	})
	return p.SaveWalletReservation(resv)
}

func (p *Manager) HandleLocalActionStatus(sendTxInL1 bool) ([]*LocalActionPerformData, []*LocalActionPerformData) {
	// Local actions can be restored from disk before the wallet is unlocked.
	// They must remain pending until the runtime wallet and channel identity
	// are available; otherwise a monitor tick can terminate the WASM process.
	if p.wallet == nil {
		return nil, nil
	}

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
		if !p.localActionBelongsToCurrentWallet(resv) {
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
		} else if resv.Status < RS_CLOSED &&
			resv.Status != RS_PERFORM_ACTION_TX_CONFIRMED &&
			resv.Status != RS_PERFORM_ACTION_RUN_STARTED {
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

func (p *Manager) localActionBelongsToCurrentWallet(resv *LocalActionPerformData) bool {
	if resv == nil || p.wallet == nil {
		return false
	}
	paymentPubKey := p.wallet.GetPaymentPubKey()
	if len(resv.ReqPubKey) != 0 {
		// Wallet IDs are local database identities and change when the same
		// mnemonic is deleted and imported again.  The payment key is the
		// stable owner identity for a local action and also distinguishes
		// subaccounts, so prefer it whenever it was persisted.
		return paymentPubKey != nil && bytes.Equal(paymentPubKey.SerializeCompressed(), resv.ReqPubKey)
	}

	current := p.wallet.GetWalletId()
	return resv.WalletId == (common.WalletId{}) || resv.WalletId == current
}

func (p *Manager) handleLocalActionStatus(resv *LocalActionPerformData, sendTxInL1 bool) error {
	resv.Mutex().RLock()
	expectedStatus := resv.Status
	expectedTxID := resv.TxId
	expectedIsL1 := resv.IsL1Tx
	resv.Mutex().RUnlock()

	if expectedStatus == RS_PERFORM_ACTION_TX_BROADCASTED {
		if expectedIsL1 != sendTxInL1 {
			return nil
		}
		if expectedTxID == "" {
			return nil
		}
		confirmed := false
		if sendTxInL1 {
			confirmed = p.l1IndexerClient.IsTxConfirmed(expectedTxID)
		} else {
			confirmed = p.l2IndexerClient.IsTxConfirmed(expectedTxID)
		}
		if !confirmed {
			return nil
		}
		Log.Infof("local action tx confirmed: %s", expectedTxID)
		_, err := p.handleLocalActionTxConfirmed(resv.Id, expectedStatus,
			expectedTxID, expectedIsL1)
		return err
	}

	if expectedStatus >= RS_PERFORM_ACTION_TX_CONFIRMED &&
		expectedStatus < RS_PERFORM_ACTION_COMPLETED {
		_, err := p.handleLocalActionTxConfirmed(resv.Id, expectedStatus,
			expectedTxID, expectedIsL1)
		return err
	}

	return nil
}

// HandleLocalActionTxConfirmed preserves the existing direct-call API while
// still making the transition conditional on one locked state snapshot.
func (p *Manager) HandleLocalActionTxConfirmed(id int64) error {
	resv := p.GetLocalAction(id)
	if resv == nil {
		return fmt.Errorf("local action %d not found", id)
	}
	resv.Mutex().RLock()
	expectedStatus := resv.Status
	expectedTxID := resv.TxId
	expectedIsL1 := resv.IsL1Tx
	resv.Mutex().RUnlock()
	_, err := p.handleLocalActionTxConfirmed(id, expectedStatus, expectedTxID, expectedIsL1)
	return err
}

// handleLocalActionTxConfirmed advances only the transaction state observed by
// the monitor. A delayed tick must not confirm a replacement transaction after
// an action switches chain or starts its next stage.
func (p *Manager) handleLocalActionTxConfirmed(id int64, expectedStatus ResvStatus,
	expectedTxID string, expectedIsL1 bool) (bool, error) {
	resv := p.GetLocalAction(id)
	if resv == nil {
		return false, fmt.Errorf("local action %d not found", id)
	}
	resv.Lock()
	defer resv.Unlock()

	if resv.Status != expectedStatus || resv.TxId != expectedTxID ||
		resv.IsL1Tx != expectedIsL1 {
		return false, nil
	}
	if expectedStatus == RS_PERFORM_ACTION_TX_BROADCASTED {
		resv.Status = RS_PERFORM_ACTION_TX_CONFIRMED
		p.updateOperationLogByReservationBestEffort(RESV_TYPE_LOCALACTION, resv.Id, OperationLogUpdate{
			Status:  OperationLogRunning,
			Message: "Transaction confirmed; continuing wallet action",
			TxID:    resv.TxId,
			Details: map[string]string{"txid": resv.TxId},
		})
	}
	switch resv.Action {
	case LOCAL_ACTION_CONFIRM_TX, LOCAL_ACTION_CONFIRM_TX_L2:
		status, err := CompleteLocalActionAfterTxConfirmed(resv.Action)
		if err != nil {
			return true, err
		}
		resv.Status = status
		return true, p.SaveWalletReservation(resv)
	case LOCAL_ACTION_UNSTAKE_MINER:
		if err := p.localActionInnerStatusUnstakeMiner(resv); err != nil {
			return true, err
		}
		return true, p.SaveWalletReservation(resv)
	case LOCAL_ACTION_LOCK_WITH_EXPAND:
		if err := p.localActionInnerStatusLockWithExpand(resv); err != nil {
			return true, err
		}
		return true, p.SaveWalletReservation(resv)
	default:
		return true, fmt.Errorf("local action %s requires STP manager", resv.Action)
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

	// When the peer has no balance for this asset there is nothing to lock
	// locally.  Start with the contract withdrawal and let the existing
	// withdraw -> expand state machine continue from there.  Calling
	// LockToChannel with a zero amount makes AllowLock reject the operation
	// before the paid expansion can begin.
	if amtToLock.Sign() == 0 {
		txId, err := p.withdrawWithContract(channel.ChannelId,
			param.AssetName.String(), amtToExpand.String(), resv.FeeRate, param.ContractURL)
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
		p.updateOperationLogByReservationBestEffort(RESV_TYPE_LOCALACTION, resv.Id, OperationLogUpdate{
			Status:  OperationLogRunning,
			Message: "Contract withdrawal submitted; waiting for SatoshiNet confirmation",
			TxID:    txId,
			Details: map[string]string{"txid": txId, "stage": "withdraw"},
		})
		return nil
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
	p.updateOperationLogByReservationBestEffort(RESV_TYPE_LOCALACTION, resv.Id, OperationLogUpdate{
		Status:  OperationLogRunning,
		Message: "Initial channel lock submitted; waiting for SatoshiNet confirmation",
		TxID:    txId,
		Details: map[string]string{"txid": txId, "stage": "lock"},
	})
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

	currResv := resv.ActionResvs[len(resv.ActionResvs)-1]
	// A confirmed L2 withdrawal may still be waiting for the contract to
	// create its L1 output.  This stage does not need a channel runtime yet;
	// keep polling the item instead of trying to load a locked/stale channel.
	if currResv.ActionType == "withdraw" && !resv.IsL1Tx {
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
			return nil
		}

		resv.TxId = item.OutTxId
		resv.IsL1Tx = true
		resv.Status = RS_PERFORM_ACTION_TX_BROADCASTED
		p.updateOperationLogByReservationBestEffort(RESV_TYPE_LOCALACTION, resv.Id, OperationLogUpdate{
			Status:  OperationLogRunning,
			Message: "Contract withdrawal produced Bitcoin carrier; waiting for confirmation",
			TxID:    item.OutTxId,
			Details: map[string]string{"txid": item.OutTxId, "stage": "bitcoin_carrier"},
		})
		return nil
	}

	channel := p.FindChannel(param.ChannelId)
	if channel == nil {
		return fmt.Errorf("can't find channel %s", param.ChannelId)
	}
	if p.GetChannel(channel.ChannelId) == nil {
		if err := p.EnableChannel(channel); err != nil {
			return err
		}
	}

	switch currResv.ActionType {
	case "lock":
		amtToExpand, err := localActionExpandAmount(currResv.MoreData)
		if err != nil {
			return err
		}
		txId, err := p.withdrawWithContract(channel.ChannelId,
			param.AssetName.String(), amtToExpand.String(), resv.FeeRate, param.ContractURL)
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
		p.updateOperationLogByReservationBestEffort(RESV_TYPE_LOCALACTION, resv.Id, OperationLogUpdate{
			Status:  OperationLogRunning,
			Message: "Channel lock confirmed; contract withdrawal submitted",
			TxID:    txId,
			Details: map[string]string{"txid": txId, "stage": "withdraw"},
		})
		return nil

	case "withdraw":
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
		p.updateOperationLogByReservationBestEffort(RESV_TYPE_LOCALACTION, resv.Id, OperationLogUpdate{
			Status:  OperationLogRunning,
			Message: "Bitcoin carrier confirmed; channel expansion submitted",
			TxID:    txId,
			Details: map[string]string{"txid": txId, "stage": "expand"},
		})
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
