package wallet

import (
	"errors"
	"sort"

	indexer "github.com/sat20-labs/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// applyLocalWrite atomically replaces the endpoint-local replica with the
// records echoed by a successful FREE_LOCAL batch write.
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
	var viewHeight uint64
	for _, record := range result.Records {
		if record == nil {
			return dkvsindexer.ErrInvalidRecord
		}
		recordPath, pathErr := dkvsindexer.CollectionPathForKey(record.Key)
		if pathErr != nil || recordPath != path {
			continue
		}
		if record.IssueHeight > viewHeight {
			viewHeight = record.IssueHeight
		}
		if dkvsindexer.IsTombstone(record.Flags) {
			delete(byKey, record.Key)
		} else {
			byKey[record.Key] = record
		}
	}
	records := make([]*swire.DKVSRecord, 0, len(byKey))
	for _, record := range byKey {
		if record != nil && !dkvsindexer.IsExpired(record, viewHeight) {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(a, b int) bool { return records[a].Key < records[b].Key })

	batch := s.db.NewWriteBatch()
	defer batch.Close()
	if err := s.stageConfirmedReplacement(batch, scope, path, records,
		dkvsindexer.RecordVerificationOptions{Height: viewHeight}); err != nil {
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
	return batch.Flush()
}
