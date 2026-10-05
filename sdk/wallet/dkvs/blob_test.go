package dkvs

import (
	"bytes"
	"testing"
)

func TestBlobCodecPreservesOpaqueBytes(t *testing.T) {
	data := []byte("opaque encrypted or compressed bytes")
	encoded, err := EncodeBlobValue(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != len(data)+8 || !bytes.Equal(encoded[8:], data) {
		t.Fatalf("opaque bytes were not preserved inside the fixed envelope")
	}
	decoded, err := DecodeBlobValue(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.Data, data) || len(decoded.Metadata) != 0 {
		t.Fatalf("decoded blob=%+v", decoded)
	}
}

func TestBlobCodecMetadataEnvelope(t *testing.T) {
	data, metadata := []byte("data"), []byte("metadata")
	encoded, err := EncodeBlobValue(data, metadata)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBlobValue(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.Data, data) || !bytes.Equal(decoded.Metadata, metadata) {
		t.Fatalf("decoded blob=%+v", decoded)
	}
}
