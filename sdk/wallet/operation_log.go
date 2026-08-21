package wallet

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	db "github.com/sat20-labs/indexer/common"
)

const (
	DB_KEY_OPERATION_LOG          = "oplog-r-"
	DB_KEY_OPERATION_LOG_RELATION = "oplog-x-"
)

type OperationLogStatus string

const (
	OperationLogPending   OperationLogStatus = "pending"
	OperationLogRunning   OperationLogStatus = "running"
	OperationLogSucceeded OperationLogStatus = "succeeded"
	OperationLogFailed    OperationLogStatus = "failed"
	OperationLogCancelled OperationLogStatus = "cancelled"
)

// OperationLogEvent is deliberately semantic and human-facing. Details should
// contain only fields useful to a wallet user (txid, amount, channel, error,
// etc.), not serialized internal state-machine snapshots.
type OperationLogEvent struct {
	Timestamp int64              `json:"timestamp"`
	Status    OperationLogStatus `json:"status,omitempty"`
	Message   string             `json:"message"`
	Details   map[string]string  `json:"details,omitempty"`
}

// OperationLogRecord represents one user-visible wallet operation. A record
// may be linked to a reservation for later semantic updates, but reservation
// persistence never generates log content automatically.
type OperationLogRecord struct {
	ID              string              `json:"id"`
	Category        string              `json:"category"`
	Action          string              `json:"action"`
	Title           string              `json:"title"`
	Summary         string              `json:"summary"`
	Status          OperationLogStatus  `json:"status"`
	CreatedAt       int64               `json:"created_at"`
	UpdatedAt       int64               `json:"updated_at"`
	ReservationID   int64               `json:"reservation_id,omitempty"`
	ReservationType string              `json:"reservation_type,omitempty"`
	TxID            string              `json:"txid,omitempty"`
	Parameters      map[string]string   `json:"parameters,omitempty"`
	Result          map[string]string   `json:"result,omitempty"`
	History         []OperationLogEvent `json:"history"`
}

type OperationLogCreate struct {
	Category   string
	Action     string
	Title      string
	Summary    string
	Parameters map[string]string
}

type OperationLogUpdate struct {
	Status  OperationLogStatus
	Message string
	Details map[string]string
	Result  map[string]string
	TxID    string
}

// OperationLogManager is an independent module accessed and owned through the
// wallet Manager API. It does not participate in wallet state-machine logic.
type OperationLogManager struct {
	db db.KVDB
}

var operationLogMu sync.Mutex
var lastOperationLogTimestamp int64

func NewOperationLogManager(kvdb db.KVDB) *OperationLogManager {
	return &OperationLogManager{db: kvdb}
}

func cloneOperationLogStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneOperationLogRecord(src *OperationLogRecord) *OperationLogRecord {
	if src == nil {
		return nil
	}
	clone := *src
	clone.Parameters = cloneOperationLogStringMap(src.Parameters)
	clone.Result = cloneOperationLogStringMap(src.Result)
	clone.History = make([]OperationLogEvent, len(src.History))
	for i, event := range src.History {
		clone.History[i] = event
		clone.History[i].Details = cloneOperationLogStringMap(event.Details)
	}
	return &clone
}

func nextOperationLogTime(previous int64) int64 {
	now := time.Now().UnixMilli()
	if now <= previous {
		return previous + 1
	}
	return now
}

func nextOperationLogID() (string, int64) {
	now := time.Now().UnixMilli()
	if now <= lastOperationLogTimestamp {
		now = lastOperationLogTimestamp + 1
	}
	lastOperationLogTimestamp = now
	return fmt.Sprintf("op-%d", now), now
}

func normalizeOperationLogStatus(status OperationLogStatus) OperationLogStatus {
	switch status {
	case OperationLogPending, OperationLogRunning, OperationLogSucceeded,
		OperationLogFailed, OperationLogCancelled:
		return status
	default:
		return OperationLogRunning
	}
}

func (m *OperationLogManager) Create(input OperationLogCreate) (*OperationLogRecord, error) {
	if m == nil || m.db == nil {
		return nil, fmt.Errorf("operation log manager is unavailable")
	}
	input.Action = strings.TrimSpace(input.Action)
	input.Title = strings.TrimSpace(input.Title)
	if input.Action == "" {
		return nil, fmt.Errorf("operation action is empty")
	}
	if input.Title == "" {
		return nil, fmt.Errorf("operation title is empty")
	}

	operationLogMu.Lock()
	defer operationLogMu.Unlock()
	id, now := nextOperationLogID()
	summary := strings.TrimSpace(input.Summary)
	if summary == "" {
		summary = input.Title
	}
	record := &OperationLogRecord{
		ID:         id,
		Category:   strings.TrimSpace(input.Category),
		Action:     input.Action,
		Title:      input.Title,
		Summary:    summary,
		Status:     OperationLogRunning,
		CreatedAt:  now,
		UpdatedAt:  now,
		Parameters: cloneOperationLogStringMap(input.Parameters),
		History: []OperationLogEvent{{
			Timestamp: now,
			Status:    OperationLogRunning,
			Message:   summary,
		}},
	}
	if err := m.saveLocked(record); err != nil {
		return nil, err
	}
	return cloneOperationLogRecord(record), nil
}

func (m *OperationLogManager) Update(id string, update OperationLogUpdate) (*OperationLogRecord, error) {
	if m == nil || m.db == nil {
		return nil, fmt.Errorf("operation log manager is unavailable")
	}
	operationLogMu.Lock()
	defer operationLogMu.Unlock()

	record, err := m.getLocked(strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, fmt.Errorf("operation log %s not found", id)
	}

	status := update.Status
	if status == "" {
		status = record.Status
	} else {
		status = normalizeOperationLogStatus(status)
	}
	message := strings.TrimSpace(update.Message)
	if message == "" {
		message = record.Summary
	}
	now := nextOperationLogTime(record.UpdatedAt)
	record.Status = status
	record.Summary = message
	record.UpdatedAt = now
	if strings.TrimSpace(update.TxID) != "" {
		record.TxID = strings.TrimSpace(update.TxID)
	}
	if update.Result != nil {
		record.Result = cloneOperationLogStringMap(update.Result)
	}
	record.History = append(record.History, OperationLogEvent{
		Timestamp: now,
		Status:    status,
		Message:   message,
		Details:   cloneOperationLogStringMap(update.Details),
	})
	if err := m.saveLocked(record); err != nil {
		return nil, err
	}
	return cloneOperationLogRecord(record), nil
}

func (m *OperationLogManager) BindReservation(id, reservationType string, reservationID int64) error {
	if reservationID == 0 || strings.TrimSpace(reservationType) == "" {
		return fmt.Errorf("invalid reservation relation")
	}
	operationLogMu.Lock()
	defer operationLogMu.Unlock()

	record, err := m.getLocked(strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if record == nil {
		return fmt.Errorf("operation log %s not found", id)
	}
	record.ReservationType = strings.TrimSpace(reservationType)
	record.ReservationID = reservationID
	if err := m.saveLocked(record); err != nil {
		return err
	}
	return m.saveRelationLocked(record.ReservationType, reservationID, record.ID)
}

func (m *OperationLogManager) UpdateByReservation(reservationType string, reservationID int64, update OperationLogUpdate) (*OperationLogRecord, error) {
	logID, err := m.findRelation(strings.TrimSpace(reservationType), reservationID)
	if err != nil {
		return nil, err
	}
	if logID == "" {
		return nil, nil
	}
	return m.Update(logID, update)
}

func (m *OperationLogManager) Get(id string) (*OperationLogRecord, error) {
	operationLogMu.Lock()
	defer operationLogMu.Unlock()
	record, err := m.getLocked(strings.TrimSpace(id))
	return cloneOperationLogRecord(record), err
}

func (m *OperationLogManager) List() ([]*OperationLogRecord, error) {
	if m == nil || m.db == nil {
		return nil, nil
	}
	operationLogMu.Lock()
	defer operationLogMu.Unlock()

	result := make([]*OperationLogRecord, 0)
	err := m.db.BatchRead(operationLogRecordPrefix(), false, func(_, value []byte) error {
		var record OperationLogRecord
		if err := DecodeFromBytes(value, &record); err != nil {
			Log.Warnf("skip invalid operation log: %v", err)
			return nil
		}
		result = append(result, cloneOperationLogRecord(&record))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].UpdatedAt == result[j].UpdatedAt {
			return result[i].ID > result[j].ID
		}
		return result[i].UpdatedAt > result[j].UpdatedAt
	})
	return result, nil
}

func (m *OperationLogManager) DeleteAll() error {
	if m == nil || m.db == nil {
		return nil
	}
	operationLogMu.Lock()
	defer operationLogMu.Unlock()
	if _, err := DeleteAllKeysWithPrefix(m.db, operationLogRecordPrefix()); err != nil {
		return err
	}
	_, err := DeleteAllKeysWithPrefix(m.db, operationLogRelationPrefix())
	return err
}

func (p *Manager) operationLogManager() *OperationLogManager {
	if p == nil || p.db == nil {
		return nil
	}
	return NewOperationLogManager(p.db)
}

// BeginOperationLog is the explicit insertion API used by wallet operations.
// Logging failures are returned to callers that need diagnostics, but wallet
// business operations should normally use the best-effort helpers below so an
// audit-display failure cannot alter transaction execution semantics.
func (p *Manager) BeginOperationLog(input OperationLogCreate) (*OperationLogRecord, error) {
	manager := p.operationLogManager()
	if manager == nil {
		return nil, fmt.Errorf("operation log manager is unavailable")
	}
	return manager.Create(input)
}

func (p *Manager) UpdateOperationLog(id string, update OperationLogUpdate) (*OperationLogRecord, error) {
	manager := p.operationLogManager()
	if manager == nil {
		return nil, fmt.Errorf("operation log manager is unavailable")
	}
	return manager.Update(id, update)
}

func (p *Manager) BindOperationLogReservation(id, reservationType string, reservationID int64) error {
	manager := p.operationLogManager()
	if manager == nil {
		return fmt.Errorf("operation log manager is unavailable")
	}
	return manager.BindReservation(id, reservationType, reservationID)
}

func (p *Manager) UpdateOperationLogByReservation(reservationType string, reservationID int64, update OperationLogUpdate) (*OperationLogRecord, error) {
	manager := p.operationLogManager()
	if manager == nil {
		return nil, fmt.Errorf("operation log manager is unavailable")
	}
	return manager.UpdateByReservation(reservationType, reservationID, update)
}

func (p *Manager) GetOperationLogs() ([]*OperationLogRecord, error) {
	manager := p.operationLogManager()
	if manager == nil {
		return nil, nil
	}
	return manager.List()
}

func (p *Manager) GetOperationLog(id string) (*OperationLogRecord, error) {
	manager := p.operationLogManager()
	if manager == nil {
		return nil, nil
	}
	return manager.Get(id)
}

func (p *Manager) DeleteAllOperationLogs() error {
	manager := p.operationLogManager()
	if manager == nil {
		return nil
	}
	return manager.DeleteAll()
}

func (p *Manager) beginOperationLogBestEffort(input OperationLogCreate) string {
	record, err := p.BeginOperationLog(input)
	if err != nil {
		Log.Warnf("begin operation log %s failed: %v", input.Action, err)
		return ""
	}
	return record.ID
}

func (p *Manager) updateOperationLogBestEffort(id string, update OperationLogUpdate) {
	if id == "" {
		return
	}
	if _, err := p.UpdateOperationLog(id, update); err != nil {
		Log.Warnf("update operation log %s failed: %v", id, err)
	}
}

func (p *Manager) bindOperationLogReservationBestEffort(id, reservationType string, reservationID int64) {
	if id == "" || reservationID == 0 {
		return
	}
	if err := p.BindOperationLogReservation(id, reservationType, reservationID); err != nil {
		Log.Warnf("bind operation log %s to reservation %s/%d failed: %v", id, reservationType, reservationID, err)
	}
}

func (p *Manager) updateOperationLogByReservationBestEffort(reservationType string, reservationID int64, update OperationLogUpdate) {
	if reservationID == 0 {
		return
	}
	if _, err := p.UpdateOperationLogByReservation(reservationType, reservationID, update); err != nil {
		Log.Warnf("update operation log for reservation %s/%d failed: %v", reservationType, reservationID, err)
	}
}
