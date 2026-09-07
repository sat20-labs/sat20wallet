package wallet

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
	"time"

	swire "github.com/sat20-labs/satoshinet/wire"
)

func TestAccountMessagePayloadRoundTrip(t *testing.T) {
	encoded, err := encodeAccountMessagePayload(AccountMessageKindGeneric, "msg-1", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeAccountMessagePayload(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ApplicationID != "msg-1" || decoded.Kind != AccountMessageKindGeneric || string(decoded.Body) != "hello" {
		t.Fatalf("unexpected payload %#v", decoded)
	}
}

func TestAccountMessageIDContainsUnixMicrosecondsAndEntropy(t *testing.T) {
	before := time.Now().UnixMicro()
	first, err := newAccountMessageID()
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now().UnixMicro()
	second, err := newAccountMessageID()
	if err != nil {
		t.Fatal(err)
	}
	if !swire.ValidMessageID(first) || !swire.ValidMessageID(second) || first == second {
		t.Fatalf("message IDs first=%q second=%q", first, second)
	}
	raw, err := hex.DecodeString(first)
	if err != nil {
		t.Fatal(err)
	}
	generatedAt := int64(binary.BigEndian.Uint64(raw[:8]))
	if generatedAt < before || generatedAt > after {
		t.Fatalf("message timestamp=%d range=[%d,%d]", generatedAt, before, after)
	}
}
