package wallet

import (
	"context"
	"errors"
	"fmt"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type dkvsTerminalOutboxError struct {
	Key     string
	Code    string
	Message string
}

func (e *dkvsTerminalOutboxError) Error() string {
	if e == nil {
		return "DKVS outbox is terminal"
	}
	if e.Message != "" {
		return fmt.Sprintf("DKVS outbox %s is terminal (%s): %s", e.Key, e.Code, e.Message)
	}
	return fmt.Sprintf("DKVS outbox %s is terminal (%s)", e.Key, e.Code)
}

// flushDKVSBatchOutbox replays exact signed requests created by the DKVS manager.
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
	// Preflight the complete outbox before submitting anything. A malformed,
	// terminal or conflicting entry makes this synchronization attempt fail;
	// later entries must not hide or partially advance past the bad state.
	for _, entry := range entries {
		mutations, _, err := entry.decode()
		if err != nil {
			return false, err
		}
		if _, err := dkvsindexer.ParseKey(entry.Key); err != nil {
			return false, err
		}
		if err := verifyOutboxMutationKeys(mutations); err != nil {
			return false, err
		}
		switch entry.State {
		case dkvsSessionTerminal:
			return false, &dkvsTerminalOutboxError{
				Key: entry.Key, Code: entry.LastErrorCode, Message: entry.LastError,
			}
		case dkvsSessionConflict:
			return false, fmt.Errorf("DKVS outbox %s requires reconciliation: %w",
				entry.Key, dkvsindexer.ErrWriteConflict)
		}
	}
	submitted := false
	for _, entry := range entries {
		mutations, conditions, err := entry.decode()
		if err != nil {
			return submitted, err
		}
		if err := store.updateBatchOutboxState(entry, dkvsSessionInflight, nil); err != nil {
			return submitted, err
		}
		result, err := client.putRecordBatchCASV1Raw(mutations, conditions, entry.EndpointID)
		if err != nil {
			switch classifyDKVSOutboxError(err) {
			case dkvsOutboxConflict:
				if markErr := store.markOutboxFailure(entry, err); markErr != nil {
					return submitted, errors.Join(err, markErr)
				}
				return submitted, err
			case dkvsOutboxPermanent:
				if markErr := store.markOutboxTerminal(entry, err); markErr != nil {
					return submitted, errors.Join(err, markErr)
				}
				return submitted, err
			default:
				_ = store.markOutboxFailure(entry, err)
				return submitted, err
			}
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
		}
	}
	return submitted, nil
}

type dkvsOutboxErrorClass uint8

const (
	dkvsOutboxTransient dkvsOutboxErrorClass = iota
	dkvsOutboxConflict
	dkvsOutboxPermanent
)

func classifyDKVSOutboxError(err error) dkvsOutboxErrorClass {
	if err == nil {
		return dkvsOutboxTransient
	}
	if isDKVSConflictError(err) || errors.Is(err, dkvsindexer.ErrInvalidSequence) {
		return dkvsOutboxConflict
	}
	var remote *DKVSError
	if errors.As(err, &remote) {
		switch remote.Code {
		case dkvsindexer.ErrorCodeInvalidRecord,
			dkvsindexer.ErrorCodePermissionDenied:
			return dkvsOutboxPermanent
		}
	}
	if errors.Is(err, dkvsindexer.ErrInvalidRecord) ||
		errors.Is(err, dkvsindexer.ErrInvalidKey) ||
		errors.Is(err, dkvsindexer.ErrInvalidSignature) ||
		errors.Is(err, dkvsindexer.ErrExpiredRecord) ||
		errors.Is(err, dkvsindexer.ErrRecordTooLarge) ||
		errors.Is(err, dkvsindexer.ErrPermissionDenied) {
		return dkvsOutboxPermanent
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return dkvsOutboxTransient
	}
	return dkvsOutboxTransient
}

func markDKVSOutboxSubmissionFailure(store *dkvsReplicaStore,
	entry *dkvsBatchOutboxEntry, err error) error {
	if classifyDKVSOutboxError(err) == dkvsOutboxPermanent {
		return store.markOutboxTerminal(entry, err)
	}
	return store.markOutboxFailure(entry, err)
}

func isDKVSConflictError(err error) bool {
	return errors.Is(err, dkvsindexer.ErrWriteConflict) ||
		errors.Is(err, dkvsindexer.ErrStaleGeneration) ||
		errors.Is(err, dkvsindexer.ErrPathDiverged)
}
