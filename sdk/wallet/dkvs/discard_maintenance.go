//go:build rgb11discard

package dkvs

import (
	"errors"
	"fmt"

	indexercommon "github.com/sat20-labs/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// DiscardExactReplica removes one already-confirmed remote deletion from the
// local subscription replica. It is compiled only into the maintenance build.
func (s *ReplicaStore) DiscardExactReplica(namespace, key, wantHash string) error {
	if s == nil || s.db == nil || namespace == "" || key == "" || wantHash == "" {
		return errors.New("exact replica discard requires namespace, key, and hash")
	}
	record, recordErr := s.LoadSubscriptionRecord(namespace, key)
	if recordErr == nil {
		if got := dkvsindexer.RecordHash(record).String(); got != wantHash {
			return fmt.Errorf("local replica record hash mismatch: got %s", got)
		}
	} else if !errors.Is(recordErr, indexercommon.ErrKeyNotFound) {
		return recordErr
	}
	_, stateErr := s.LoadLocalKeyState(namespace, key)
	if stateErr != nil && !errors.Is(stateErr, indexercommon.ErrKeyNotFound) {
		return stateErr
	}
	if errors.Is(recordErr, indexercommon.ErrKeyNotFound) &&
		errors.Is(stateErr, indexercommon.ErrKeyNotFound) {
		return nil
	}
	batch := s.db.NewWriteBatch()
	defer batch.Close()
	if err := batch.Delete(dkvsSubscriptionRecordKey(namespace, key)); err != nil {
		return err
	}
	if err := batch.Delete(dkvsSubscriptionKeyStateKey(namespace, key)); err != nil {
		return err
	}
	return batch.Flush()
}
