package wallet

import (
	"errors"
	"testing"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func TestHTTPResponseErrorUnwrapsDKVSErrorCode(t *testing.T) {
	err := &HTTPResponseError{
		StatusCode: 409,
		Body: []byte(`{"code":-1,"msg":"changed text","error_code":"DKVS_STALE_GENERATION"}`),
	}
	if !errors.Is(err, dkvsindexer.ErrStaleGeneration) {
		t.Fatalf("HTTP DKVS error did not preserve typed code: %v", err)
	}
	var remote *DKVSError
	if !errors.As(err, &remote) || remote.Code != dkvsindexer.ErrorCodeStaleGeneration {
		t.Fatalf("HTTP DKVS error did not expose DKVSError: %#v", remote)
	}
}
