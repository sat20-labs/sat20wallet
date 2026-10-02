package dkvs

import (
	"encoding/hex"
	"errors"
	"sort"

	indexercommon "github.com/sat20-labs/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Records carries active values only. KeyStates is the union of those values
// and explicit deletion floors. Mere omission from an endpoint cache is not a
// deletion: only an authenticated endpoint's explicit deleted state removes a
// previously materialized value. No cursor can commit until the whole payload
// has been checked and staged successfully.
func validatePrefixPayload(prefix string, height uint64, records []*swire.DKVSRecord,
	states []dkvsindexer.DKVSKeyState) (map[string]*swire.DKVSRecord, error) {

	if len(records) > dkvsindexer.MaxPrefixReadRecords || len(states) > dkvsindexer.MaxPrefixReadRecords {
		return nil, dkvsindexer.ErrBatchTooLarge
	}
	byKey := make(map[string]*swire.DKVSRecord, len(records))
	totalBytes := 0
	for _, record := range records {
		if record == nil || !walletSubscriptionMatches(prefix, record.Key) || dkvsindexer.IsTombstone(record.Flags) {
			return nil, dkvsindexer.ErrInvalidSnapshot
		}
		if _, duplicate := byKey[record.Key]; duplicate {
			return nil, dkvsindexer.ErrInvalidSnapshot
		}
		size := dkvsindexer.RecordSize(record)
		if size > dkvsindexer.MaxPrefixReadBytes-totalBytes {
			return nil, dkvsindexer.ErrBatchTooLarge
		}
		totalBytes += size
		if err := dkvsindexer.VerifyRecordForClient(record, dkvsindexer.RecordVerificationOptions{
			ExpectedKey: record.Key, Height: height,
		}); err != nil {
			return nil, err
		}
		byKey[record.Key] = record
	}
	seen := make(map[string]struct{}, len(states))
	active := 0
	for _, state := range states {
		if _, err := dkvsindexer.ParseKey(state.Key); err != nil || !walletSubscriptionMatches(prefix, state.Key) || state.Seq == 0 {
			return nil, dkvsindexer.ErrInvalidSnapshot
		}
		if _, duplicate := seen[state.Key]; duplicate {
			return nil, dkvsindexer.ErrInvalidSnapshot
		}
		seen[state.Key] = struct{}{}
		hash, err := hex.DecodeString(state.ETag)
		if err != nil || len(hash) != 32 {
			return nil, dkvsindexer.ErrInvalidSnapshot
		}
		size := len(state.Key) + len(state.ETag) + 128
		if size > dkvsindexer.MaxPrefixReadBytes-totalBytes {
			return nil, dkvsindexer.ErrBatchTooLarge
		}
		totalBytes += size
		record := byKey[state.Key]
		switch state.Status {
		case dkvsindexer.KeyStateActive:
			if record == nil || record.Seq != state.Seq || dkvsindexer.RecordHash(record).String() != state.ETag {
				return nil, dkvsindexer.ErrInvalidSnapshot
			}
			active++
		case dkvsindexer.KeyStateDeleted:
			if record != nil || state.Record != nil {
				return nil, dkvsindexer.ErrInvalidSnapshot
			}
		default:
			return nil, dkvsindexer.ErrInvalidSnapshot
		}
	}
	if active != len(records) {
		return nil, dkvsindexer.ErrInvalidSnapshot
	}
	return byKey, nil
}

func (s *ReplicaStore) stagePrefixPayload(batch batchWriter, namespace string,
	records map[string]*swire.DKVSRecord, states []dkvsindexer.DKVSKeyState) ([]string, error) {

	changed := make([]string, 0, len(states))
	for _, serverState := range states {
		old, err := s.LoadSubscriptionRecord(namespace, serverState.Key)
		if err != nil && !errors.Is(err, indexercommon.ErrKeyNotFound) {
			return nil, err
		}
		local, err := s.LoadLocalKeyState(namespace, serverState.Key)
		if err != nil && !errors.Is(err, indexercommon.ErrKeyNotFound) {
			return nil, err
		}
		// Retain the monotonic sequence floor, including after deletion. An
		// older snapshot must not resurrect a key already deleted locally.
		if local != nil && (local.Seq > serverState.Seq ||
			(local.Seq == serverState.Seq && local.ETag != serverState.ETag)) {
			return nil, dkvsindexer.ErrStaleGeneration
		}
		deleted := serverState.Status == dkvsindexer.KeyStateDeleted
		different := local == nil || local.Seq != serverState.Seq || local.ETag != serverState.ETag || local.Deleted != deleted
		if deleted {
			different = different || old != nil
			if err := batch.Delete(dkvsSubscriptionRecordKey(namespace, serverState.Key)); err != nil {
				return nil, err
			}
		} else {
			record := records[serverState.Key]
			different = different || old == nil || dkvsindexer.RecordHash(old) != dkvsindexer.RecordHash(record)
			encoded, err := dkvsindexer.MarshalRecord(record)
			if err != nil {
				return nil, err
			}
			if err := batch.Put(dkvsSubscriptionRecordKey(namespace, serverState.Key), encoded); err != nil {
				return nil, err
			}
		}
		if err := putLocalKeyStateBatch(batch, namespace, localStateFromServer(serverState)); err != nil {
			return nil, err
		}
		if different {
			changed = append(changed, serverState.Key)
		}
	}
	sort.Strings(changed)
	return changed, nil
}
