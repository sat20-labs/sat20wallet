package dkvs

import (
	"encoding/hex"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Bounded read payloads contain active records and matching active key states
// only. They cannot convey deletions or invent sequence floors. Managed sync
// uses InstallActiveState, which verifies the complete source root and removes
// omissions only after full current-set reconciliation.
// VerifyPrefixPayload validates every server-returned current record before it
// is exposed outside the transport boundary. This includes the author
// signature/identity check performed by VerifyRecordForClient plus the
// record-to-key-state hash/sequence relationship.
func VerifyPrefixPayload(prefix string, height uint64, records []*swire.DKVSRecord,
	states []dkvsindexer.DKVSKeyState) error {
	_, err := validatePrefixPayload(prefix, height, records, states)
	return err
}

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
		if err := dkvsindexer.VerifyRecordForClient(record, dkvsindexer.RecordVerificationOptions{ExpectedKey: record.Key, Height: height}); err != nil {
			return nil, err
		}
		byKey[record.Key] = record
	}
	seen := make(map[string]struct{}, len(states))
	for _, state := range states {
		if _, err := dkvsindexer.ParseKey(state.Key); err != nil || !walletSubscriptionMatches(prefix, state.Key) || state.Seq == 0 || state.Status != dkvsindexer.KeyStateActive {
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
		if record == nil || record.Seq != state.Seq || dkvsindexer.RecordHash(record).String() != state.ETag {
			return nil, dkvsindexer.ErrInvalidSnapshot
		}
		if state.Record != nil && dkvsindexer.RecordHash(state.Record) != dkvsindexer.RecordHash(record) {
			return nil, dkvsindexer.ErrInvalidSnapshot
		}
	}
	if len(seen) != len(records) {
		return nil, dkvsindexer.ErrInvalidSnapshot
	}
	return byKey, nil
}
