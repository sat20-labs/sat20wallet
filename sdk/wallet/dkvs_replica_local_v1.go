package wallet

import (
	"errors"
	"sort"

	indexer "github.com/sat20-labs/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// applyLocalWrite is the compatibility bridge for callers that still submit
// one FREE_LOCAL write without a batch outbox. The v1 manager path uses
// applyWriteResultAndAck so replica replacement and acknowledgement are atomic.
func (s *dkvsReplicaStore) applyLocalWrite(scope, path string,
	result *dkvsindexer.WriteResult) error {
	if s == nil || s.db == nil || scope == "" || path == "" || result == nil ||
		!result.LocalOnly || result.EndpointID == "" {
		return dkvsindexer.ErrInvalidRecord
	}
	byKey, err := s.loadConfirmedByKey(scope)
	if err != nil {
		if !errors.Is(err, indexer.ErrKeyNotFound) {
			return err
		}
		byKey = make(map[string]*swire.DKVSRecord)
	}
	for _, record := range result.Records {
		if record == nil {
			return dkvsindexer.ErrInvalidRecord
		}
		recordPath, pathErr := dkvsindexer.CollectionPathForKey(record.Key)
		if pathErr != nil || recordPath != path {
			continue
		}
		if dkvsindexer.IsTombstone(record.Flags) {
			delete(byKey, record.Key)
		} else {
			byKey[record.Key] = record
		}
	}
	records := make([]*swire.DKVSRecord, 0, len(byKey))
	for _, record := range byKey {
		records = append(records, record)
	}
	sort.Slice(records, func(a, b int) bool { return records[a].Key < records[b].Key })
	batch := s.db.NewWriteBatch()
	defer batch.Close()
	if err := s.stageConfirmedReplacement(batch, scope, path, records,
		dkvsindexer.RecordVerificationOptions{Now: result.ServerTimeMS}); err != nil {
		return err
	}
	state, stateErr := s.loadPathState(scope)
	if stateErr != nil {
		if !errors.Is(stateErr, indexer.ErrKeyNotFound) {
			return stateErr
		}
		state = &dkvsPathReplicaState{Path: path}
	}
	state.ServerTimeMS = result.ServerTimeMS
	state.EndpointID = result.EndpointID
	state.HasLocalOnly = true
	state.SessionState = dkvsSessionConfirmed
	state.LastErrorCode = ""
	if err := putPathStateBatch(batch, scope, state); err != nil {
		return err
	}
	for _, record := range result.Records {
		if record == nil {
			continue
		}
		recordPath, pathErr := dkvsindexer.CollectionPathForKey(record.Key)
		if pathErr == nil && recordPath == path {
			_ = batch.Delete(dkvsReplicaRecordKey(dkvsReplicaOutboxPrefix, scope, record.Key))
		}
	}
	return batch.Flush()
}
