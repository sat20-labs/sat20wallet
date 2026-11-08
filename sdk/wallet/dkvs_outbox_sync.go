package wallet

import (
	"context"
	"errors"
	"fmt"
	"net"

	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type dkvsTerminalOutboxError struct {
	RequestID string
	Code      string
	Message   string
}

type dkvsLocalOutboxReadError struct{ cause error }

func (e *dkvsLocalOutboxReadError) Error() string {
	return "read local DKVS outbox: " + e.cause.Error()
}
func (e *dkvsLocalOutboxReadError) Unwrap() error { return e.cause }

// Each signed account request owns exactly one key. Dependencies between
// directory membership and module publication are ordered by the SDK caller.
func dkvsOutboxDependencyKey(key string) string { return key }

func dkvsOutboxEntryDependencies(entry *DKVSBatchOutboxEntry) (map[string]struct{}, error) {
	mutations, err := entry.DecodeMutations()
	if err != nil {
		return nil, err
	}
	dependencies := make(map[string]struct{}, len(mutations))
	for _, mutation := range mutations {
		if mutation.Record == nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		dependencies[dkvsOutboxDependencyKey(mutation.Record.Key)] = struct{}{}
	}
	return dependencies, nil
}

func dkvsOutboxDependenciesOverlap(left, right map[string]struct{}) bool {
	for dependency := range left {
		if _, found := right[dependency]; found {
			return true
		}
	}
	return false
}

func dkvsOutboxBlockDependencies(blocked, dependencies map[string]struct{}) {
	for dependency := range dependencies {
		blocked[dependency] = struct{}{}
	}
}

var ErrAccountAutopayFundingRequired = errors.New("account AUTOPAY funding is required")

func (e *dkvsTerminalOutboxError) Error() string {
	if e == nil {
		return "DKVS outbox is terminal"
	}
	if e.Message != "" {
		return fmt.Sprintf("DKVS outbox %s is terminal (%s): %s", e.RequestID, e.Code, e.Message)
	}
	return fmt.Sprintf("DKVS outbox %s is terminal (%s)", e.RequestID, e.Code)
}

func (e *dkvsTerminalOutboxError) Unwrap() error {
	if e == nil {
		return nil
	}
	return &DKVSError{Code: dkvsindexer.ErrorCode(e.Code), Message: e.Message}
}

func dkvsOutboxEntryError(entry *DKVSBatchOutboxEntry) error {
	if entry == nil {
		return dkvsindexer.ErrInvalidRecord
	}
	return &DKVSError{Code: dkvsindexer.ErrorCode(entry.LastErrorCode), Message: entry.LastError}
}

// Replay the exact signed operation context. Reconnecting is not consent to
// obtain a new prefix generation and reauthorize a historical operation.
func (p *Manager) flushDKVSBatchOutbox(client *SatsNetDKVSClient, store *dkvsReplicaStore) (bool, error) {
	if p == nil || client == nil || store == nil || client.replicaNamespace == "" {
		return false, ErrDKVSPathNotSynced
	}
	entries, err := store.LoadOutbox(client.replicaNamespace)
	if err != nil {
		if errors.Is(err, dkvsindexer.ErrInvalidRecord) || errors.Is(err, dkvsindexer.ErrBatchTooLarge) {
			panic(permanentDKVSOutboxError(nil, err))
		}
		// A transient local read failure cannot establish an empty outbox.
		// Preserve the original signed requests and retry without granting a
		// fresh write session or waiting for an unrelated remote watch event.
		return false, &dkvsLocalOutboxReadError{cause: err}
	}
	var endpointID string
	blocked := make(map[string]struct{})
	var blockedErr error
	filtered := entries[:0]
	for _, entry := range entries {
		if accountSendCanRebase(entry) {
			if err := store.DiscardOutbox(entry); err != nil {
				return false, err
			}
			continue
		}
		if entry.OriginDomain == accountKVOutboxDomain && (entry.State == DKVSOutboxConflict || entry.State == DKVSOutboxTerminal) {
			if blockedErr == nil {
				blockedErr = p.accountSendFailure(entry)
			}
			continue
		}
		filtered = append(filtered, entry)
	}
	entries = filtered
	for _, entry := range entries {
		_, err := dkvsOutboxEntryDependencies(entry)
		if err != nil {
			panic(permanentDKVSOutboxError(entry, err))
		}
		switch entry.State {
		case DKVSOutboxTerminal:
			panic(&dkvsTerminalOutboxError{RequestID: entry.RequestID, Code: entry.LastErrorCode, Message: entry.LastError})
		case DKVSOutboxConflict:
			if blockedErr == nil {
				blockedErr = fmt.Errorf("DKVS outbox %s requires reconciliation: %w", entry.RequestID, dkvsOutboxEntryError(entry))
			}
			continue
		}
		if entry.EndpointID != "" {
			if endpointID == "" {
				config, err := client.GetDKVSClientConfig()
				if err != nil {
					if classifyDKVSOutboxError(err) == dkvsOutboxPermanent {
						panic(permanentDKVSOutboxError(entry, err))
					}
					return false, err
				}
				endpointID = config.EndpointID
			}
			if entry.EndpointID != endpointID {
				err := dkvsindexer.ErrLocalOnlyEndpointMismatch
				if updateErr := store.UpdateOutboxState(entry, DKVSOutboxConflict, err); updateErr != nil {
					return false, errors.Join(err, updateErr)
				}
				if blockedErr == nil {
					blockedErr = err
				}
			}
		}
	}
	submitted := false
	var cleanupErr error
	for _, entry := range entries {
		dependencies, err := dkvsOutboxEntryDependencies(entry)
		if err != nil {
			panic(permanentDKVSOutboxError(entry, err))
		}
		if entry.State == DKVSOutboxConflict || dkvsOutboxDependenciesOverlap(dependencies, blocked) {
			// A skipped atomic batch can connect another key to the blocked
			// dependency; later requests for that key must wait as well.
			dkvsOutboxBlockDependencies(blocked, dependencies)
			continue
		}
		mutations, err := entry.DecodeMutations()
		if err != nil {
			panic(permanentDKVSOutboxError(entry, err))
		}
		if entry.LastError != "" || entry.LastErrorCode != "" {
			p.revokeAccountOutboxWrite(entry)
		}
		if entry.State == DKVSOutboxAcknowledged {
			if err := p.dkvs.finishAcknowledgedWrite(client, store, entry); err != nil {
				if !accountACKCleanupOnly(err) {
					return submitted, err
				}
				cleanupErr = err
			}
			submitted = true
			continue
		}
		if entry.Authorization == nil && !(len(mutations) == 1 && dkvsindexer.IsAccountMappingBindingKey(mutations[0].Record.Key)) {
			// An unsigned draft has never been authorized for network submission.
			// Keep it separate from confirmed KV until its owner explicitly signs.
			err := dkvsindexer.ErrPermissionDenied
			_ = store.UpdateOutboxState(entry, DKVSOutboxConflict, err)
			return submitted, err
		}
		if err := store.UpdateOutboxState(entry, DKVSOutboxInflight, nil); err != nil {
			// The signed request is still durable. A local write failure must
			// not crash the runtime or authorize a replacement request.
			return submitted, &DKVSRequestPendingError{RequestID: entry.RequestID, Cause: err}
		}
		result, err := client.submitDKVSBatch(mutations, entry.EndpointID, entry.RequestID, entry.Authorization, entry)
		if err != nil {
			err = p.accountAutopaySubmissionFailure(mutations, err)
			if errors.Is(err, ErrAccountAutopayFundingRequired) {
				state := DKVSOutboxPending
				if knownDKVSRefusal(err) {
					state = DKVSOutboxConflict
				}
				_ = store.UpdateOutboxState(entry, state, err)
				dkvsOutboxBlockDependencies(blocked, dependencies)
				if blockedErr == nil {
					blockedErr = err
				}
				continue
			}
			switch classifyDKVSOutboxError(err) {
			case dkvsOutboxConflict:

				if markErr := store.UpdateOutboxState(entry, DKVSOutboxConflict, err); markErr != nil {
					return submitted, errors.Join(err, markErr)
				}
				dkvsOutboxBlockDependencies(blocked, dependencies)
				if blockedErr == nil {
					blockedErr = err
					if entry.OriginDomain == accountKVOutboxDomain {
						blockedErr = p.accountSendFailure(entry)
					}
				}
				continue
			case dkvsOutboxPermanent:
				if entry.OriginDomain == accountKVOutboxDomain {
					if markErr := store.UpdateOutboxState(entry, DKVSOutboxTerminal, err); markErr != nil {
						return submitted, markErr
					}
					return submitted, p.accountSendFailure(entry)
				}
				panic(permanentDKVSOutboxError(entry, err))
			default:
				if markErr := store.UpdateOutboxState(entry, DKVSOutboxPending, err); markErr != nil {
					return submitted, errors.Join(err, markErr)
				}
				if isDKVSOutboxGlobalTransportFailure(err) {
					return submitted, err
				}
				dkvsOutboxBlockDependencies(blocked, dependencies)
				if blockedErr == nil {
					blockedErr = err
				}
				continue
			}
		}
		if err := store.ApplyWriteResultAndAck(entry, result); err != nil {
			p.revokeAccountOutboxWrite(entry)
			var persistence *dkvscore.AckPersistenceError
			if errors.As(err, &persistence) {
				return submitted, &DKVSRequestPendingError{RequestID: entry.RequestID, Committed: true, Cause: err}
			}
			return submitted, err
		}
		if err := p.dkvs.finishAcknowledgedWrite(client, store, entry); err != nil {
			if !accountACKCleanupOnly(err) {
				return submitted, err
			}
			cleanupErr = err
		}
		submitted = true
	}
	if blockedErr != nil {
		return submitted, blockedErr
	}
	return submitted, cleanupErr
}

func batchContainsAutopay(mutations []dkvsindexer.CASMutation) bool {
	for _, mutation := range mutations {
		if mutation.Record == nil {
			continue
		}
		proof, err := dkvsindexer.ParseFeeProof(mutation.Record.FeeProof)
		if err == nil && proof.Mode == dkvsindexer.FeeModeAutopay {
			return true
		}
	}
	return false
}

func (p *Manager) accountAutopaySubmissionFailure(mutations []dkvsindexer.CASMutation, submissionErr error) error {
	var pending *DKVSRequestPendingError
	if errors.As(submissionErr, &pending) {
		return submissionErr
	}
	if p == nil || !batchContainsAutopay(mutations) ||
		(!IsDKVSErrorCode(submissionErr, dkvsindexer.ErrorCodeInvalidRecord) && !IsDKVSErrorCode(submissionErr, dkvsindexer.ErrorCodeQuotaExceeded) &&
			!errors.Is(submissionErr, dkvsindexer.ErrInvalidFeeProof) && !errors.Is(submissionErr, dkvsindexer.ErrFeeCapacityExceeded)) {
		return submissionErr
	}
	status, err := p.GetAccountAutopayFundingStatus()
	return accountAutopaySubmissionDecision(submissionErr, status, err)
}

func accountAutopaySubmissionDecision(submissionErr error, status *AccountAutopayFundingStatus, statusErr error) error {
	if statusErr != nil {
		if isDKVSNetworkFailure(statusErr) {
			return statusErr
		}
		return submissionErr
	}
	if status != nil && status.Required && status.NeedsFunding && status.CanFund {
		return fmt.Errorf("%w: %s: %w", ErrAccountAutopayFundingRequired, status.Message, submissionErr)
	}
	return submissionErr
}

type dkvsOutboxErrorClass uint8

const (
	dkvsOutboxTransient dkvsOutboxErrorClass = iota
	dkvsOutboxConflict
	dkvsOutboxPermanent
)

func classifyDKVSOutboxError(err error) dkvsOutboxErrorClass {
	if IsDKVSErrorCode(err, dkvsindexer.ErrorCodeHeightTooLow) {
		return dkvsOutboxTransient
	}
	if knownDKVSRefusal(err) {
		return dkvsOutboxConflict
	}
	var pending *DKVSRequestPendingError
	if errors.As(err, &pending) {
		return dkvsOutboxTransient
	}
	if err == nil {
		return dkvsOutboxTransient
	}
	if isDKVSConflictError(err) || isDKVSRebaseError(err) {
		return dkvsOutboxConflict
	}
	if isDKVSNetworkFailure(err) {
		return dkvsOutboxTransient
	}
	// A definite business refusal stops this intent. Bare HTTP 429/5xx keep
	// their transient handling; structured refusals were identified above.
	if errors.Is(err, dkvsindexer.ErrStorageModeDowngrade) ||
		errors.Is(err, dkvsindexer.ErrFreeLocalQuotaExceeded) ||
		errors.Is(err, dkvsindexer.ErrFeeCapacityExceeded) || errors.Is(err, dkvsindexer.ErrMailboxFull) ||
		errors.Is(err, dkvsindexer.ErrRecordNotFound) {
		return dkvsOutboxConflict
	}
	var rejection *DKVSError
	if errors.As(err, &rejection) && rejection != nil {
		switch rejection.Code {
		case dkvsindexer.ErrorCodeInvalidRecord, dkvsindexer.ErrorCodeRecordNotFound,
			dkvsindexer.ErrorCodeStorageModeDowngrade, dkvsindexer.ErrorCodeQuotaExceeded:
			return dkvsOutboxConflict
		}
	}
	return dkvsOutboxPermanent
}

func knownDKVSRefusal(err error) bool {
	var refusal *DKVSError
	if !errors.As(err, &refusal) || refusal == nil {
		return false
	}
	switch refusal.Code {
	case dkvsindexer.ErrorCodeWriteConflict, dkvsindexer.ErrorCodeStaleGeneration,
		dkvsindexer.ErrorCodeStaleEndpoint, dkvsindexer.ErrorCodeEndpointMismatch,
		dkvsindexer.ErrorCodeResetRequired, dkvsindexer.ErrorCodePermissionDenied,
		dkvsindexer.ErrorCodeInvalidSequence, dkvsindexer.ErrorCodePathDiverged,
		dkvsindexer.ErrorCodeLocalOnlyEndpointMismatch, dkvsindexer.ErrorCodeStorageModeDowngrade,
		dkvsindexer.ErrorCodeQuotaExceeded, dkvsindexer.ErrorCodeInvalidRecord,
		dkvsindexer.ErrorCodeExpiredRecord, dkvsindexer.ErrorCodeRecordNotFound,
		dkvsindexer.ErrorCodeHeightTooLow, dkvsindexer.ErrorCodeInvalidBlockHeight:
		return true
	}
	return false
}

func isDKVSNetworkFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, dkvsindexer.ErrStorageUnavailable) {
		return true
	}
	if response, ok := err.(*HTTPResponseError); ok && response != nil {
		if knownDKVSRefusal(response.Unwrap()) {
			return false
		}
		if response.StatusCode == 408 || response.StatusCode == 425 || response.StatusCode == 429 ||
			(response.StatusCode >= 500 && response.StatusCode <= 599) {
			return true
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if _, ok := err.(net.Error); ok {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, cause := range joined.Unwrap() {
			if isDKVSNetworkFailure(cause) {
				return true
			}
		}
		return false
	}
	if cause, ok := err.(interface{ Unwrap() error }); ok {
		return isDKVSNetworkFailure(cause.Unwrap())
	}
	return false
}

// A response for one request can fail while the authority still serves other
// independent keys. Only loss of the transport itself revokes the session.
func isDKVSOutboxGlobalTransportFailure(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}
func permanentDKVSOutboxError(entry *DKVSBatchOutboxEntry, err error) error {
	requestID := "unknown"
	if entry != nil && entry.RequestID != "" {
		requestID = entry.RequestID
	}
	return fmt.Errorf("DKVS permanent outbox failure request=%s: %w", requestID, err)
}
func markDKVSOutboxSubmissionFailure(store *dkvsReplicaStore, entry *DKVSBatchOutboxEntry, err error) error {
	switch classifyDKVSOutboxError(err) {
	case dkvsOutboxPermanent:
		if entry != nil && entry.OriginDomain == accountKVOutboxDomain {
			return store.UpdateOutboxState(entry, DKVSOutboxTerminal, err)
		}
		panic(permanentDKVSOutboxError(entry, err))
	case dkvsOutboxConflict:
		return store.UpdateOutboxState(entry, DKVSOutboxConflict, err)
	default:
		return store.UpdateOutboxState(entry, DKVSOutboxPending, err)
	}
}
func isDKVSRebaseError(err error) bool {
	return errors.Is(err, dkvsindexer.ErrWriteConflict) || errors.Is(err, dkvsindexer.ErrInvalidSequence) ||
		errors.Is(err, dkvsindexer.ErrStaleGeneration) || errors.Is(err, dkvsindexer.ErrExpiredRecord) ||
		IsDKVSErrorCode(err, dkvsindexer.ErrorCodeWriteConflict) ||
		IsDKVSErrorCode(err, dkvsindexer.ErrorCodeInvalidSequence) ||
		IsDKVSErrorCode(err, dkvsindexer.ErrorCodeStaleGeneration) || IsDKVSErrorCode(err, dkvsindexer.ErrorCodeExpiredRecord)
}
func isDKVSConflictError(err error) bool {
	return errors.Is(err, dkvsindexer.ErrWriteConflict) || errors.Is(err, dkvsindexer.ErrEndpointMismatch) ||
		errors.Is(err, dkvsindexer.ErrStaleEndpoint) || errors.Is(err, dkvsindexer.ErrStaleGeneration) ||
		errors.Is(err, dkvsindexer.ErrPermissionDenied) ||
		errors.Is(err, dkvsindexer.ErrResetRequired) || errors.Is(err, dkvsindexer.ErrLocalOnlyEndpointMismatch) ||
		IsDKVSErrorCode(err, dkvsindexer.ErrorCodeWriteConflict) || IsDKVSErrorCode(err, dkvsindexer.ErrorCodeEndpointMismatch) ||
		IsDKVSErrorCode(err, dkvsindexer.ErrorCodeStaleEndpoint) || IsDKVSErrorCode(err, dkvsindexer.ErrorCodeStaleGeneration) ||
		IsDKVSErrorCode(err, dkvsindexer.ErrorCodePermissionDenied) ||
		IsDKVSErrorCode(err, dkvsindexer.ErrorCodeResetRequired) || IsDKVSErrorCode(err, dkvsindexer.ErrorCodeLocalOnlyEndpointMismatch)
}
