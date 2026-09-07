package dkvs

import (
	"encoding/json"
	"strings"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type ApplicationResponse struct {
	Code      int    `json:"code"`
	Msg       string `json:"msg"`
	ErrorCode string `json:"error_code,omitempty"`
}

func DecodeApplicationResponse(raw []byte, out interface{}) error {
	var base ApplicationResponse
	if err := json.Unmarshal(raw, &base); err != nil {
		return err
	}
	if base.Code != 0 {
		return &DKVSError{Code: dkvsindexer.ErrorCode(base.ErrorCode), Message: base.Msg}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func ErrorFromResponseBody(body []byte) error {
	if len(body) == 0 {
		return nil
	}
	var payload ApplicationResponse
	if err := json.Unmarshal(body, &payload); err != nil || payload.ErrorCode == "" {
		return nil
	}
	return &DKVSError{
		Code: dkvsindexer.ErrorCode(payload.ErrorCode), Message: payload.Msg,
	}
}

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
	case dkvsindexer.ErrorCodeEndpointMismatch:
		return dkvsindexer.ErrEndpointMismatch
	case dkvsindexer.ErrorCodeResetRequired:
		return dkvsindexer.ErrResetRequired
	case dkvsindexer.ErrorCodePermissionDenied:
		return dkvsindexer.ErrPermissionDenied
	case dkvsindexer.ErrorCodeInvalidSequence:
		return dkvsindexer.ErrInvalidSequence
	case dkvsindexer.ErrorCodePathDiverged:
		return dkvsindexer.ErrPathDiverged
	case dkvsindexer.ErrorCodeLocalOnlyEndpointMismatch:
		return dkvsindexer.ErrLocalOnlyEndpointMismatch
	case dkvsindexer.ErrorCodeStorageModeDowngrade:
		return dkvsindexer.ErrStorageModeDowngrade
	case dkvsindexer.ErrorCodeQuotaExceeded:
		return dkvsindexer.ErrFreeLocalQuotaExceeded
	case dkvsindexer.ErrorCodeRecordNotFound:
		return dkvsindexer.ErrRecordNotFound
	default:
		return dkvsindexer.ErrInvalidRecord
	}
}
