package wallet

import (
	"errors"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// flushDKVSBatchOutbox replays exact signed requests created by the v1 manager.
// It never rebuilds or re-signs records. A conflict remains persisted and is
// returned to the caller for explicit reconciliation.
func (p *Manager) flushDKVSBatchOutbox(client *SatsNetDKVSClient,
	store *dkvsReplicaStore) (bool, error) {
	if p == nil || client == nil || store == nil || client.replicaNamespace == "" {
		return false, ErrDKVSPathNotSynced
	}
	entries, err := store.loadBatchOutbox(client.replicaNamespace)
	if err != nil {
		return false, err
	}
	submitted := false
	for _, entry := range entries {
		if err := verifyOutboxEntryIdentity(entry); err != nil {
			_ = store.markOutboxFailure(entry, err)
			return submitted, err
		}
		if entry.State == dkvsSessionConflict {
			return submitted, dkvsindexer.ErrWriteConflict
		}
		mutations, conditions, err := entry.decode()
		if err != nil {
			_ = store.markOutboxFailure(entry, err)
			return submitted, err
		}
		if err := store.updateBatchOutboxState(entry, dkvsSessionInflight, nil); err != nil {
			return submitted, err
		}
		result, err := client.putRecordBatchCASV1Raw(mutations, conditions, entry.EndpointID)
		if err != nil {
			_ = store.markOutboxFailure(entry, err)
			return submitted, err
		}
		if err := store.applyWriteResultAndAck(entry, result); err != nil {
			_ = store.markOutboxFailure(entry, err)
			return submitted, err
		}
		submitted = true
		paths, pathErr := mutationPaths(mutations)
		if pathErr == nil && p.dkvs != nil {
			for _, path := range paths {
				p.dkvs.markReady(pathReplicaScope(client, path))
			}
			p.dkvs.notifyObservers(paths)
		}
	}
	return submitted, nil
}

func isDKVSConflictError(err error) bool {
	return errors.Is(err, dkvsindexer.ErrWriteConflict) ||
		errors.Is(err, dkvsindexer.ErrStaleGeneration) ||
		errors.Is(err, dkvsindexer.ErrPathDiverged)
}
