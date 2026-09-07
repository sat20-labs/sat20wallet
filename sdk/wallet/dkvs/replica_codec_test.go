package dkvs

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func TestDKVSSubscriptionStateCompactCodec(t *testing.T) {
	state := &SubscriptionState{
		EndpointID: "endpoint", Prefixes: []string{"/account/z/path", "/account/a/path"},
		Generations: map[string]uint64{"/account/a/path": 0},
		ViewHeight:  123, LastSyncAtMS: 456, Status: DKVSSubscriptionReady,
		LastErrorCode: string(dkvsindexer.ErrorCodeResetRequired),
	}
	encoded, err := encodeDKVSSubscriptionState(state)
	if err != nil {
		t.Fatal(err)
	}
	again, err := encodeDKVSSubscriptionState(state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, again) || json.Valid(encoded) {
		t.Fatal("subscription state is not deterministic compact binary")
	}
	decoded, err := decodeDKVSSubscriptionState(encoded)
	if err != nil {
		t.Fatal(err)
	}
	want := *state
	want.Prefixes = []string{"/account/a/path", "/account/z/path"}
	if !reflect.DeepEqual(*decoded, want) {
		t.Fatalf("decoded=%+v want=%+v", decoded, want)
	}
	jsonEncoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) >= len(jsonEncoded) {
		t.Fatalf("compact=%d json=%d", len(encoded), len(jsonEncoded))
	}
	if _, err := decodeDKVSSubscriptionState(jsonEncoded); !errors.Is(err, dkvsindexer.ErrInvalidRecord) {
		t.Fatalf("legacy JSON accepted err=%v", err)
	}
}

func TestDKVSLocalKeyStateCompactCodec(t *testing.T) {
	etag := chainhash.DoubleHashH([]byte("etag")).String()
	state := LocalKeyState{Key: "codec/item", Seq: 9, ETag: etag, ExpiryHeight: 999,
		StorageMode: dkvsindexer.StorageModeAutopay}
	encoded, err := encodeDKVSLocalKeyState(state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeDKVSLocalKeyState(encoded, state.Key)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*decoded, state) {
		t.Fatalf("decoded=%+v want=%+v", decoded, state)
	}
	if _, err := decodeDKVSLocalKeyState(append(encoded, 0), state.Key); !errors.Is(err, dkvsindexer.ErrInvalidRecord) {
		t.Fatalf("trailing data err=%v", err)
	}
}

func TestDKVSOutboxCompactCodec(t *testing.T) {
	record := &swire.DKVSRecord{Version: dkvsindexer.Version, Key: "codec/item", Value: []byte("value"), Seq: 1}
	recordBytes, err := dkvsindexer.MarshalRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	entry := &BatchOutboxEntry{
		RequestID: "request", Namespace: "test:chain", EndpointID: "endpoint",
		Mutations: []PersistedMutation{{Record: recordBytes, ExpectAbsent: true}},
		State:     DKVSOutboxPending, Attempts: 2, CreatedAtMS: 10, UpdatedAtMS: 20,
		OriginKey: record.Key, OriginDomain: "account", OriginGeneration: 3,
		PreservePrefixGenerations: true,
	}
	encoded, err := encodeDKVSOutboxEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeDKVSOutboxEntry(encoded, entry.Namespace, entry.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, entry) {
		t.Fatalf("decoded=%+v want=%+v", decoded, entry)
	}
	jsonEncoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if json.Valid(encoded) || len(encoded) >= len(jsonEncoded) {
		t.Fatalf("compact=%d json=%d", len(encoded), len(jsonEncoded))
	}
	if _, err := decodeDKVSOutboxEntry(jsonEncoded, entry.Namespace, entry.RequestID); !errors.Is(err, dkvsindexer.ErrInvalidRecord) {
		t.Fatalf("legacy JSON accepted err=%v", err)
	}
}
