package wallet

import (
	"context"
	"errors"
	"fmt"
	"net"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type dkvsTerminalOutboxError struct {
	RequestID string
	Code      string
	Message   string
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

// flushDKVSBatchOutbox replays exact signed requests only through the CoreNode
// recorded when the entry was created.
func (p *Manager) flushDKVSBatchOutbox(client *SatsNetDKVSClient,
	store *dkvsReplicaStore) (bool, error) {
	if p == nil || client == nil || store == nil || client.replicaNamespace == "" {
		return false, ErrDKVSPathNotSynced
	}
	entries, err := store.LoadOutbox(client.replicaNamespace)
	if err != nil {
		panic(permanentDKVSOutboxError(nil, err))
	}
	var endpointID string
	for _, entry := range entries {
		if _, err := entry.DecodeMutations(); err != nil {
			panic(permanentDKVSOutboxError(entry, err))
		}
		switch entry.State {
		case DKVSOutboxTerminal:
			panic(&dkvsTerminalOutboxError{
				RequestID: entry.RequestID, Code: entry.LastErrorCode, Message: entry.LastError,
			})
		case DKVSOutboxConflict:
			return false, fmt.Errorf("DKVS outbox %s requires reconciliation: %w",
				entry.RequestID, dkvsindexer.ErrWriteConflict)
		}
		if entry.EndpointID != "" {
			if endpointID == "" {
				config, configErr := client.GetDKVSClientConfig()
				if configErr != nil {
					if classifyDKVSOutboxError(configErr) == dkvsOutboxPermanent {
						panic(permanentDKVSOutboxError(entry, configErr))
					}
					return false, configErr
				}
				endpointID = config.EndpointID
			}
			if entry.EndpointID != endpointID {
				err := dkvsindexer.ErrLocalOnlyEndpointMismatch
				_ = store.UpdateOutboxState(entry, DKVSOutboxConflict, err)
				return false, err
			}
		}
	}
	submitted := false
	for _, entry := range entries {
		mutations, err := entry.DecodeMutations()
		if err != nil {
			panic(permanentDKVSOutboxError(entry, err))
		}
		if err := store.UpdateOutboxState(entry, DKVSOutboxInflight, nil); err != nil {
			panic(permanentDKVSOutboxError(entry, err))
		}
		result, err := client.putRecordBatchCASRaw(mutations, entry.EndpointID, entry.RequestID)
		if err != nil {
			err = p.accountAutopaySubmissionFailure(mutations, err)
			if errors.Is(err, ErrAccountAutopayFundingRequired) {
				_ = store.UpdateOutboxState(entry, DKVSOutboxPending, err)
				return submitted, err
			}
			switch classifyDKVSOutboxError(err) {
			case dkvsOutboxConflict:
				if isDKVSRebaseError(err) {
					if discardErr := store.DiscardOutbox(entry); discardErr != nil {
						return submitted, errors.Join(err, discardErr)
					}
					return submitted, err
				}
				if markErr := store.UpdateOutboxState(entry, DKVSOutboxConflict, err); markErr != nil {
					return submitted, errors.Join(err, markErr)
				}
				return submitted, err
			case dkvsOutboxPermanent:
				panic(permanentDKVSOutboxError(entry, err))
			default:
				_ = store.UpdateOutboxState(entry, DKVSOutboxPending, err)
				return submitted, err
			}
		}
		if err := store.ApplyWriteResultAndAck(entry, result); err != nil {
			panic(permanentDKVSOutboxError(entry, err))
		}
		submitted = true
	}
	return submitted, nil
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

// accountAutopaySubmissionFailure turns only a live, independently verified
// payment expiry into a paused outbox. An invalid paid record with a healthy
// AUTOPAY delegate remains a permanent invariant failure and still panics.
func (p *Manager) accountAutopaySubmissionFailure(mutations []dkvsindexer.CASMutation,
	submissionErr error) error {

	if p == nil || !batchContainsAutopay(mutations) ||
		(!IsDKVSErrorCode(submissionErr, dkvsindexer.ErrorCodeInvalidRecord) &&
			!IsDKVSErrorCode(submissionErr, dkvsindexer.ErrorCodeQuotaExceeded) &&
			!errors.Is(submissionErr, dkvsindexer.ErrInvalidFeeProof) &&
			!errors.Is(submissionErr, dkvsindexer.ErrFeeCapacityExceeded)) {
		return submissionErr
	}
	status, err := p.GetAccountAutopayFundingStatus()
	return accountAutopaySubmissionDecision(submissionErr, status, err)
}

func accountAutopaySubmissionDecision(submissionErr error,
	status *AccountAutopayFundingStatus, statusErr error) error {
	if statusErr != nil {
		if isDKVSNetworkFailure(statusErr) {
			return statusErr
		}
		return submissionErr
	}
	if status != nil && status.Required && status.NeedsFunding && status.CanFund {
		return fmt.Errorf("%w: %s", ErrAccountAutopayFundingRequired, status.Message)
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
	if err == nil {
		return dkvsOutboxTransient
	}
	if isDKVSConflictError(err) || errors.Is(err, dkvsindexer.ErrInvalidSequence) ||
		IsDKVSErrorCode(err, dkvsindexer.ErrorCodeInvalidSequence) {
		return dkvsOutboxConflict
	}
	if isDKVSNetworkFailure(err) {
		return dkvsOutboxTransient
	}
	return dkvsOutboxPermanent
}

func isDKVSNetworkFailure(err error) bool {
	// An HTTP response can still represent a transient transport/service failure.
	// Keep the exact signed outbox request for retry instead of killing its host.
	var response *HTTPResponseError
	if errors.As(err, &response) && response != nil {
		if response.StatusCode == 408 || response.StatusCode == 425 || response.StatusCode == 429 ||
			(response.StatusCode >= 500 && response.StatusCode <= 599) {
			return true
		}
	}
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

func markDKVSOutboxSubmissionFailure(store *dkvsReplicaStore,
	entry *DKVSBatchOutboxEntry, err error) error {
	switch classifyDKVSOutboxError(err) {
	case dkvsOutboxPermanent:
		panic(permanentDKVSOutboxError(entry, err))
	case dkvsOutboxConflict:
		if isDKVSRebaseError(err) {
			return store.DiscardOutbox(entry)
		}
		return store.UpdateOutboxState(entry, DKVSOutboxConflict, err)
	default:
		return store.UpdateOutboxState(entry, DKVSOutboxPending, err)
	}
}

func isDKVSRebaseError(err error) bool {
	return errors.Is(err, dkvsindexer.ErrWriteConflict) ||
		errors.Is(err, dkvsindexer.ErrInvalidSequence) ||
		IsDKVSErrorCode(err, dkvsindexer.ErrorCodeWriteConflict) ||
		IsDKVSErrorCode(err, dkvsindexer.ErrorCodeInvalidSequence)
}

func isDKVSConflictError(err error) bool {
	return errors.Is(err, dkvsindexer.ErrWriteConflict) ||
		errors.Is(err, dkvsindexer.ErrEndpointMismatch) ||
		errors.Is(err, dkvsindexer.ErrResetRequired) ||
		errors.Is(err, dkvsindexer.ErrLocalOnlyEndpointMismatch) ||
		IsDKVSErrorCode(err, dkvsindexer.ErrorCodeWriteConflict) ||
		IsDKVSErrorCode(err, dkvsindexer.ErrorCodeEndpointMismatch) ||
		IsDKVSErrorCode(err, dkvsindexer.ErrorCodeResetRequired) ||
		IsDKVSErrorCode(err, dkvsindexer.ErrorCodeLocalOnlyEndpointMismatch)
}
