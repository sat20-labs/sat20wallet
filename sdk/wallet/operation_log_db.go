package wallet

import (
	"fmt"

	db "github.com/sat20-labs/indexer/common"
)

func operationLogRecordKey(id string) string {
	return GetDBKeyPrefix() + DB_KEY_OPERATION_LOG + id
}

func operationLogRecordPrefix() []byte {
	return []byte(GetDBKeyPrefix() + DB_KEY_OPERATION_LOG)
}

func operationLogRelationKey(reservationType string, reservationID int64) string {
	return fmt.Sprintf("%s%s%s-%d", GetDBKeyPrefix(), DB_KEY_OPERATION_LOG_RELATION, reservationType, reservationID)
}

func operationLogRelationPrefix() []byte {
	return []byte(GetDBKeyPrefix() + DB_KEY_OPERATION_LOG_RELATION)
}

func (m *OperationLogManager) saveLocked(record *OperationLogRecord) error {
	if m == nil || m.db == nil || record == nil {
		return nil
	}
	encoded, err := EncodeToBytes(record)
	if err != nil {
		return err
	}
	return m.db.Write([]byte(operationLogRecordKey(record.ID)), encoded)
}

func (m *OperationLogManager) getLocked(id string) (*OperationLogRecord, error) {
	if m == nil || m.db == nil || id == "" {
		return nil, nil
	}
	encoded, err := m.db.Read([]byte(operationLogRecordKey(id)))
	if err != nil {
		if err == db.ErrKeyNotFound {
			return nil, nil
		}
		return nil, err
	}
	var record OperationLogRecord
	if err := DecodeFromBytes(encoded, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func (m *OperationLogManager) saveRelationLocked(reservationType string, reservationID int64, logID string) error {
	if m == nil || m.db == nil {
		return nil
	}
	return m.db.Write([]byte(operationLogRelationKey(reservationType, reservationID)), []byte(logID))
}

func (m *OperationLogManager) findRelation(reservationType string, reservationID int64) (string, error) {
	if m == nil || m.db == nil || reservationType == "" || reservationID == 0 {
		return "", nil
	}
	encoded, err := m.db.Read([]byte(operationLogRelationKey(reservationType, reservationID)))
	if err != nil {
		if err == db.ErrKeyNotFound {
			return "", nil
		}
		return "", err
	}
	return string(encoded), nil
}
