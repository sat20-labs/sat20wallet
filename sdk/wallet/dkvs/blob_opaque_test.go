package dkvs

import (
	"bytes"
	"testing"
)

func TestBlobCodecKeepsOpaquePayloadUnchanged(t *testing.T) {
	payload := bytes.Repeat([]byte("already-owned-by-application-codec|"), 4096)
	encoded, err := EncodeBlobValue(payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, payload) {
		t.Fatal("generic Blob codec compressed or transformed the opaque payload")
	}
	// The generic Blob envelope may add framing, but it must not replace the
	// application bytes with a hidden compressed representation.
	decoded, err := DecodeBlobValue(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.Data, payload) {
		t.Fatal("opaque Blob payload changed")
	}
}
