package wallet

import dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"

// Unwrap exposes a stable DKVS error when an HTTP endpoint returned a JSON
// business error with error_code. Non-DKVS HTTP errors remain ordinary HTTP
// response errors.
func (e *HTTPResponseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return dkvscore.ErrorFromResponseBody(e.Body)
}
