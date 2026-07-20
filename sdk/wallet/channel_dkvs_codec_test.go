package wallet

import (
	"bytes"
	"testing"
)

func TestChannelDKVSValueUsesVersionedCompressedPayload(t *testing.T) {
	raw := bytes.Repeat([]byte("prepared channel snapshot"), 256)
	encoded, err := EncodeChannelDKVSValue(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(encoded, []byte(channelDKVSMagic)) || len(encoded) >= len(raw) {
		t.Fatalf("channel value is not compact: encoded=%d raw=%d", len(encoded), len(raw))
	}
	decoded, err := DecodeChannelDKVSValue(encoded)
	if err != nil || !bytes.Equal(decoded, raw) {
		t.Fatalf("channel value round trip err=%v", err)
	}
	if _, err := DecodeChannelDKVSValue(raw); err == nil {
		t.Fatal("legacy uncompressed channel value unexpectedly accepted")
	}
}

func TestPunishTxDKVSValueStoresRawTransactionBytes(t *testing.T) {
	encoded, err := EncodePunishTxDKVSValue("001122aabb")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, append(append([]byte(punishTxDKVSMagic), channelDKVSVersion), 0x00, 0x11, 0x22, 0xaa, 0xbb)) {
		t.Fatalf("unexpected punish value %x", encoded)
	}
	if _, err := EncodePunishTxDKVSValue("invalid"); err == nil {
		t.Fatal("invalid hex unexpectedly accepted")
	}
}
