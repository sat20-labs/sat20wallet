package wallet

import (
	"encoding/json"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// Unwrap exposes a stable DKVS error when an HTTP endpoint returned a JSON
// business error with error_code. Non-DKVS HTTP errors remain ordinary HTTP
// response errors.
func (e *HTTPResponseError) Unwrap() error {
	if e == nil || len(e.Body) == 0 {
		return nil
	}
	var payload struct {
		Message   string `json:"msg"`
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(e.Body, &payload); err != nil || payload.ErrorCode == "" {
		return nil
	}
	return &DKVSError{
		Code:    dkvsindexer.ErrorCode(payload.ErrorCode),
		Message: payload.Message,
	}
}
