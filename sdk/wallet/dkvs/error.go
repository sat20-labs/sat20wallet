package dkvs

import (
	"strings"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// DKVSError preserves the node's stable machine-readable error code.
type DKVSError struct {
	Code    dkvsindexer.ErrorCode
	Message string
}

func (e *DKVSError) Error() string {
	if e == nil {
		return "DKVS request failed"
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return string(e.Code)
}

func (e *DKVSError) Unwrap() error {
	if e == nil {
		return nil
	}
	switch e.Code {
	case dkvsindexer.ErrorCodeWriteConflict:
		return dkvsindexer.ErrWriteConflict
	case dkvsindexer.ErrorCodeStaleGeneration:
		return dkvsindexer.ErrStaleGeneration
	case dkvsindexer.ErrorCodeStaleEndpoint:
		return dkvsindexer.ErrStaleEndpoint
	case dkvsindexer.ErrorCodePermissionDenied:
		return dkvsindexer.ErrPermissionDenied
	case dkvsindexer.ErrorCodeInvalidSequence:
		return dkvsindexer.ErrInvalidSequence
	case dkvsindexer.ErrorCodePathDiverged:
		return dkvsindexer.ErrPathDiverged
	case dkvsindexer.ErrorCodeLocalOnlyEndpointMismatch:
		return dkvsindexer.ErrLocalOnlyEndpointMismatch
	case dkvsindexer.ErrorCodeQuotaExceeded:
		return dkvsindexer.ErrFreeLocalQuotaExceeded
	case dkvsindexer.ErrorCodeRecordNotFound:
		return dkvsindexer.ErrRecordNotFound
	default:
		return dkvsindexer.ErrInvalidRecord
	}
}
