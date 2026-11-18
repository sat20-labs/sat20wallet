package wallet

import (
	"encoding/hex"
	"errors"
	"github.com/btcsuite/btcd/wire"
	swire "github.com/sat20-labs/satoshinet/wire"
	"sort"
	"strings"
)

// These records describe commands, not confirmation of an on-chain transaction.
// Logging is best effort and never changes a command's result.
func (p *Manager) beginWalletLog(category, action, title string, fields ...map[string]string) string {
	if p == nil || p.accountDBFailure() != nil {
		return ""
	}
	input := OperationLogCreate{Category: category, Action: action, Title: title, Summary: title}
	if len(fields) > 0 {
		input.Parameters = fields[0]
	}
	if category == "account" {
		input.WalletID = p.GetAccountManagementStatus().RootWalletID
		if manager := p.operationLogManager(); manager != nil && input.WalletID != 0 {
			record, err := manager.Create(input)
			if err != nil {
				Log.Warnf("begin account operation log: %v", err)
				return ""
			}
			return record.ID
		}
	}
	return p.beginOperationLogBestEffort(input)
}

func (p *Manager) finishWalletLog(id string, err error, result any) {
	if p == nil || id == "" || p.accountDBFailure() != nil {
		return
	}
	update := OperationLogUpdate{Status: OperationLogSucceeded, Message: "Command completed"}
	if err != nil {
		update.Status, update.Message = OperationLogFailed, err.Error()
		update.Details = map[string]string{"error": err.Error()}
		var pending *AccountAutopayPendingError
		if errors.As(err, &pending) {
			update.Status, update.TxID = OperationLogPending, pending.TransactionID
		}
	} else {
		switch value := result.(type) {
		case string:
			if raw, e := hex.DecodeString(value); e == nil && len(raw) == 32 {
				update.TxID = value
			}
		case *wire.MsgTx:
			if value != nil {
				update.TxID = value.TxID()
			}
		case *swire.MsgTx:
			if value != nil {
				update.TxID = value.TxID()
			}
		case *InscribeResv:
			if value != nil {
				update.Result = map[string]string{}
				if value.CommitTx != nil {
					update.TxID = value.CommitTx.TxID()
				}
				if value.RevealTx != nil {
					update.Result["reveal_txid"] = value.RevealTx.TxID()
				}
			}
		case *RGBTransferView:
			if value != nil {
				update.Result = map[string]string{"rgb_id": value.ID}
				update.Status = rgbLogStatus(value.Status)
				update.TxID = value.TxID
			}
		case *StorageView:
			if value != nil {
				update.TxID = value.TxID
				if value.Status == "pending" {
					update.Status = OperationLogPending
				}
			}
		case *RGBAssetView:
			if value != nil {
				update.Result = map[string]string{"asset": value.Asset}
			}
		case *RGBReceiveView:
			if value != nil {
				update.TxID = value.TxID
			}
		case *ContractTxResult:
			if value != nil {
				update.TxID = value.TxID
			}
		case *PunishBroadcastResult:
			if value != nil && len(value.PunishTxIds) > 0 {
				update.TxID = value.PunishTxIds[0]
			}
		case *SweepBuildResult:
			if value != nil {
				update.TxID = value.SweepTxId
			}
		}
	}
	p.updateOperationLogBestEffort(id, update)
}

func operationView(record *OperationLogRecord) OperationView {
	view := OperationView{ID: record.ID, Kind: record.Action, Status: string(record.Status), CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, Amounts: []AssetAmount{}, TxIDs: []string{}, History: []OperationEvent{}}
	if record.TxID != "" {
		view.TxIDs = append(view.TxIDs, record.TxID)
	}
	if id := record.Result["reveal_txid"]; id != "" && id != record.TxID {
		view.TxIDs = append(view.TxIDs, id)
	}
	if amount := record.Parameters["amount"]; amount != "" {
		view.Amounts = append(view.Amounts, AssetAmount{Asset: record.Parameters["asset"], Amount: amount})
	}
	for _, h := range record.History {
		code := h.Details["error_code"]
		if code == "" {
			code = h.Details["error"]
		}
		view.History = append(view.History, OperationEvent{Time: h.Timestamp, Status: string(h.Status), ErrorCode: code})
		view.ErrorCode = code
	}
	return view
}
func (p *Manager) visibleOperationLogs() ([]*OperationLogRecord, error) {
	manager := p.operationLogManager()
	if manager == nil {
		return nil, nil
	}
	records, err := manager.ListForIdentity(p.GetCurrentWalletId(), p.GetCurrentAccountId())
	if err != nil {
		return nil, err
	}
	rootID := p.GetAccountManagementStatus().RootWalletID
	if rootID == p.GetCurrentWalletId() && p.GetCurrentAccountId() == 0 {
		return records, nil
	}
	rootRecords, err := manager.ListForIdentity(rootID, 0)
	if err != nil {
		return nil, err
	}
	for _, r := range rootRecords {
		if r.Category == "account" {
			records = append(records, r)
		}
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].UpdatedAt > records[j].UpdatedAt })
	return records, nil
}
func rgbLogStatus(status string) OperationLogStatus {
	switch status {
	case "completed":
		return OperationLogSucceeded
	case "failed", "conflicted", "rejected":
		return OperationLogFailed
	case "cancelled":
		return OperationLogCancelled
	default:
		return OperationLogRunning
	}
}
func (p *Manager) GetOperationViews() ([]OperationView, error) {
	records, err := p.visibleOperationLogs()
	if err != nil {
		return nil, err
	}
	views := make([]OperationView, 0, len(records))
	var state *RGB11State
	for _, record := range records {
		view := operationView(record)
		if record.Action == "inscribe_name" && record.Status == OperationLogSucceeded && record.Result["reveal_txid"] != "" {
			// Until the name index knows the registration, this record is also
			// the submission guard. Clearing history must not allow another mint.
			// An unavailable index or another network cannot confirm completion.
			view.Status = string(OperationLogPending)
			name := strings.ToLower(strings.TrimSpace(record.Parameters["name"]))
			if name != "" && strings.TrimSpace(record.Parameters["network"]) == p.GetChain() {
				if info, lookupErr := p.l1IndexerClient.GetNameInfo(name); lookupErr == nil && info != nil {
					view.Status = string(OperationLogSucceeded)
				}
			}
		}
		if id := record.Result["rgb_id"]; id != "" && record.WalletID == p.GetCurrentWalletId() && record.AccountIndex == p.GetCurrentAccountId() {
			if state == nil {
				state, err = p.GetRGB11State()
				if err != nil {
					return nil, err
				}
			}
			transfer, err := p.rgbTransferView(state, id)
			if err != nil {
				return nil, err
			}
			view.Status = string(rgbLogStatus(transfer.Status))
			view.Amounts = []AssetAmount{}
			for _, recipient := range transfer.Recipients {
				view.Amounts = append(view.Amounts, recipient.Amount)
			}
			view.TxIDs = []string{}
			if transfer.TxID != "" {
				view.TxIDs = append(view.TxIDs, transfer.TxID)
			}
			if transfer.ErrorCode != "" {
				view.ErrorCode = transfer.ErrorCode
			}
		}
		views = append(views, view)
	}
	return views, nil
}
func (p *Manager) DeleteOperationHistory() error {
	views, err := p.GetOperationViews()
	if err != nil {
		return err
	}
	manager := p.operationLogManager()
	if manager == nil {
		return nil
	}
	ids := make(map[string]bool, len(views))
	for _, view := range views {
		switch OperationLogStatus(view.Status) {
		case OperationLogSucceeded, OperationLogFailed, OperationLogCancelled:
			ids[view.ID] = true
		}
	}
	return manager.deleteFinishedLogs(ids)
}

// Continue and cancel update the existing operation; direct sends begin logging only after broadcast.
func (p *Manager) finishRGBLog(id string, err error, view *RGBTransferView) {
	records, readErr := p.GetOperationLogs()
	if readErr != nil {
		Log.Warnf("read RGB operation logs: %v", readErr)
		return
	}
	for _, record := range records {
		if record.Result["rgb_id"] == id {
			p.finishWalletLog(record.ID, err, view)
			return
		}
	}
	// Direct previews create no operation log. Create their first record only
	// once the existing sender reports a broadcast result, not on preparation.
	if err == nil && view != nil && view.TxID != "" &&
		(view.Status == "pending_chain" || view.Status == "completed") {
		logID := p.beginWalletLog("rgb", "rgb_send", "Send RGB asset")
		p.finishWalletLog(logID, nil, view)
	}
}

func (m *OperationLogManager) deleteFinishedLogs(ids map[string]bool) error {
	operationLogMu.Lock()
	defer operationLogMu.Unlock()
	batch := m.db.NewWriteBatch()
	if batch == nil {
		return errors.New("operation log database returned nil write batch")
	}
	defer batch.Close()
	removed := make(map[string]bool)
	err := m.db.BatchRead(operationLogRecordPrefix(), false, func(key, value []byte) error {
		var record OperationLogRecord
		if err := DecodeFromBytes(value, &record); err != nil {
			return err
		}
		if !ids[record.ID] || accountAutopayLogPending(&record) {
			return nil
		}
		if record.Result["rgb_id"] == "" {
			switch record.Status {
			case OperationLogSucceeded, OperationLogFailed, OperationLogCancelled:
			default:
				return nil
			}
		}
		removed[record.ID] = true
		return batch.Delete(key)
	})
	if err != nil {
		return err
	}
	if err := m.db.BatchRead(operationLogRelationPrefix(), false, func(key, value []byte) error {
		if removed[string(value)] {
			return batch.Delete(key)
		}
		return nil
	}); err != nil {
		return err
	}
	return batch.Flush()
}

var _ OperationAPI = (*Manager)(nil)

// The original transaction builders remain unchanged; these wallet entry points
// own the operation record that was previously written by PWA.
func (p *Manager) BuildL2SellOrder(utxos []string, address, network string) (result string, err error) {
	id := p.beginWalletLog("transaction", "build_sell_order_l2", "Build L2 sell order")
	defer func() { p.finishWalletLog(id, err, nil) }()
	return BuildBatchSellOrder_SatsNet(utxos, address, network)
}
func (p *Manager) FinalizeL2SellOrder(psbtHex string, utxos []string, buyer, server, network string, serviceFee, networkFee int64) (result string, err error) {
	id := p.beginWalletLog("transaction", "finalize_sell_order_l2", "Finalize L2 sell order")
	defer func() { p.finishWalletLog(id, err, nil) }()
	return FinalizeSellOrder_SatsNet(psbtHex, utxos, buyer, server, network, serviceFee, networkFee)
}
