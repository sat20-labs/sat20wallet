package wallet

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func testDKVSPathReplicaState() *dkvsPathReplicaState {
	root := chainhash.DoubleHashH([]byte("state-root"))
	floorHash := chainhash.DoubleHashH([]byte("delete-floor"))
	return &dkvsPathReplicaState{
		Version: dkvsReplicaStateVersion,
		Path:    "/personal/00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff/account",
		PathMeta: &dkvsindexer.PathMeta{
			Version: 3, Path: "/personal/00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff/account",
			Generation: 7, StateRoot: root, ActiveRecords: 3, ActiveTotalSize: 4096,
			MinExpiryHeight: 900, ViewHeight: 800,
		},
		DeleteFloors: []dkvsindexer.DeleteFloor{{
			Key:      "/personal/00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff/account/old",
			FloorSeq: 4, PathGeneration: 7, PubKey: []byte{2, 3, 4}, EffectiveHash: floorHash,
		}},
		LocalDeleteFloors: []dkvsindexer.DeleteFloor{{
			Key:      "/personal/00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff/account/local",
			FloorSeq: 5, PathGeneration: 0, PubKey: []byte{5, 6, 7}, EffectiveHash: floorHash,
		}},
		ServerTimeMS:  1786600000123,
		EndpointID:    "node-a",
		HasLocalOnly:  true,
		SessionState:  dkvsSessionConfirmed,
		LastErrorCode: "",
		UpdatedAtMS:   1786600000999,
	}
}

func TestDKVSPathReplicaStateBinaryRoundTrip(t *testing.T) {
	state := testDKVSPathReplicaState()
	encoded, err := encodeDKVSPathReplicaState(state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(encoded, []byte(dkvsPathReplicaStateMagic)) {
		t.Fatalf("path state is not DKPS binary: %x", encoded[:min(len(encoded), 8)])
	}
	var decoded dkvsPathReplicaState
	if err := decodeDKVSPathReplicaState(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != state.Version || decoded.Path != state.Path || decoded.PathMeta == nil ||
		decoded.PathMeta.Generation != state.PathMeta.Generation || decoded.PathMeta.StateRoot != state.PathMeta.StateRoot ||
		decoded.PathMeta.ViewHeight != state.PathMeta.ViewHeight || decoded.ServerTimeMS != state.ServerTimeMS ||
		decoded.EndpointID != state.EndpointID || !decoded.HasLocalOnly || decoded.SessionState != state.SessionState ||
		len(decoded.DeleteFloors) != 1 || decoded.DeleteFloors[0].EffectiveHash != state.DeleteFloors[0].EffectiveHash ||
		len(decoded.LocalDeleteFloors) != 1 || decoded.LocalDeleteFloors[0].FloorSeq != state.LocalDeleteFloors[0].FloorSeq {
		t.Fatalf("decoded path state mismatch: %#v", decoded)
	}
}

func TestDKVSPathReplicaStateMigratesLegacyJSONKey(t *testing.T) {
	database := newMemoryKVDB()
	store := newDKVSReplicaStore(database)
	scope := "test-scope"
	state := testDKVSPathReplicaState()
	legacy, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Write(dkvsLegacyPathStateKey(scope), legacy); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.loadPathState(scope)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Path != state.Path || loaded.PathMeta == nil || loaded.PathMeta.Generation != state.PathMeta.Generation {
		t.Fatalf("legacy state not loaded: %#v", loaded)
	}
	batch := database.NewWriteBatch()
	defer batch.Close()
	if err := putPathStateBatch(batch, scope, loaded); err != nil {
		t.Fatal(err)
	}
	if err := batch.Flush(); err != nil {
		t.Fatal(err)
	}
	current, err := database.Read(dkvsPathStateKey(scope))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(current, []byte(dkvsPathReplicaStateMagic)) {
		t.Fatalf("migrated state is not DKPS binary: %x", current[:min(len(current), 8)])
	}
	if _, err := database.Read(dkvsLegacyPathStateKey(scope)); !errors.Is(err, indexercommon.ErrKeyNotFound) {
		t.Fatalf("legacy path-state key survived migration: %v", err)
	}
}
