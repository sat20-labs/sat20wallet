package rgb11wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	strict "github.com/sat20-labs/rgb11/strict_encoding"
)

func encodeLegacyPendingTransferForTest(t *testing.T, pending *PendingTransfer) []byte {
	t.Helper()
	var buf bytes.Buffer
	encoder := strict.NewEncoder(&buf)
	if err := encoder.Raw([]byte(rgb11StoreMagic)); err != nil {
		t.Fatal(err)
	}
	if err := encoder.U8(rgb11StoreCodecVersion); err != nil {
		t.Fatal(err)
	}
	if err := encoder.U8(rgb11RecordPending); err != nil {
		t.Fatal(err)
	}
	if err := encodeTransferState(encoder, &pending.State); err != nil {
		t.Fatal(err)
	}
	for _, value := range [][]byte{
		pending.RecipientConsignment, pending.LocalConsignment,
		pending.SignedTx, pending.SignedPSBT,
	} {
		if err := encodeBlob(encoder, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := encoder.Length(uint64(len(pending.ChangeSeals)), rgb11StoreMaxRecords); err != nil {
		t.Fatal(err)
	}
	for _, seal := range pending.ChangeSeals {
		encoded, err := seal.StrictBytes()
		if err != nil {
			t.Fatal(err)
		}
		if err := encoder.Bytes(encoded, 13, 45); err != nil {
			t.Fatal(err)
		}
	}
	if err := encoder.U64(uint64(pending.CreatedAt)); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func legacySnapshotPayloadForTest(t *testing.T, snapshot *RGB11WalletSnapshot, refs []SnapshotTickerRef) []byte {
	t.Helper()
	raw, err := encodeCompactRGB11WalletSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var tail bytes.Buffer
	encoder := strict.NewEncoder(&tail)
	if err := encoder.Length(uint64(len(refs)), rgb11SnapshotMaxRecords); err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		if err := encoder.String(ref.ContractID, 1, 128); err != nil {
			t.Fatal(err)
		}
		if err := encoder.String(ref.AssetName, 1, 1024); err != nil {
			t.Fatal(err)
		}
	}
	raw = append(raw, tail.Bytes()...)
	payload, err := deflateRGB11Snapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestDecodeLegacyWalletSnapshotForMigration(t *testing.T) {
	snapshot := &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: "rgb11-test", EngineBuildID: NativeEngineBuildID,
	}
	payload := legacySnapshotPayloadForTest(t, snapshot, nil)
	decoded, migrated, err := DecodeLegacyWalletSnapshotForMigration(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated || decoded.WalletID != snapshot.WalletID {
		t.Fatalf("migrated=%t snapshot=%+v", migrated, decoded)
	}
	canonical, err := EncodeWalletSnapshotPayload(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, migrated, err = DecodeLegacyWalletSnapshotForMigration(canonical); err != nil || migrated {
		t.Fatalf("canonical migrated=%t err=%v", migrated, err)
	}
}

func TestDecodeLegacyWalletSnapshotRejectsUnprovenTickerRef(t *testing.T) {
	snapshot := &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: "rgb11-test", EngineBuildID: NativeEngineBuildID,
	}
	payload := legacySnapshotPayloadForTest(t, snapshot, []SnapshotTickerRef{{
		ContractID: "rgb:test", AssetName: "rgb11:f:test_deadbeef",
	}})
	if _, _, err := DecodeLegacyWalletSnapshotForMigration(payload); err == nil {
		t.Fatal("accepted a ticker ref that is not derivable from the projection")
	}
}

func TestDecodeLegacyWalletSnapshotCanonicalizesPendingObjects(t *testing.T) {
	transferID := "rgb:csg:test"
	consignment := []byte("shared consignment")
	pending := &PendingTransfer{
		State:                TransferState{TransferID: transferID, Direction: "send", Status: "broadcast"},
		RecipientConsignment: consignment,
		LocalConsignment:     consignment,
		SignedTx:             []byte("signed transaction"),
		SignedPSBT:           []byte("obsolete psbt"),
		CreatedAt:            42,
	}
	snapshot := &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: "rgb11-test", EngineBuildID: NativeEngineBuildID,
		ProjectionRecords: []SnapshotRecord{{
			Key: "pending-" + transferID, Value: encodeLegacyPendingTransferForTest(t, pending),
		}},
	}
	decoded, migrated, err := DecodeLegacyWalletSnapshotForMigration(
		legacySnapshotPayloadForTest(t, snapshot, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated || len(decoded.ProjectionRecords) != 2 {
		t.Fatalf("migrated=%t records=%d", migrated, len(decoded.ProjectionRecords))
	}
	hash := sha256.Sum256(consignment)
	hashText := hex.EncodeToString(hash[:])
	var canonical PendingTransfer
	objectCount := 0
	for _, record := range decoded.ProjectionRecords {
		switch {
		case record.Key == "pending-"+transferID:
			if err := decode(record.Value, &canonical); err != nil {
				t.Fatal(err)
			}
		case record.Key == "object-"+hashText:
			objectCount++
			if !bytes.Equal(record.Value, consignment) {
				t.Fatal("canonical object changed")
			}
		}
	}
	if objectCount != 1 || canonical.RecipientObjectHash != hashText ||
		canonical.LocalObjectHash != hashText || len(canonical.RecipientConsignment) != 0 ||
		len(canonical.LocalConsignment) != 0 || !bytes.Equal(canonical.SignedTx, pending.SignedTx) ||
		len(canonical.SignedPSBT) != 0 {
		t.Fatalf("object_count=%d pending=%+v", objectCount, canonical)
	}
	current, err := EncodeWalletSnapshotPayload(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, migrated, err := DecodeLegacyWalletSnapshotForMigration(current); err != nil || migrated {
		t.Fatalf("current migrated=%t err=%v", migrated, err)
	}
}

func TestDecodeLegacyWalletSnapshotRejectsMalformedPending(t *testing.T) {
	snapshot := &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: "rgb11-test", EngineBuildID: NativeEngineBuildID,
		ProjectionRecords: []SnapshotRecord{{
			Key: "pending-rgb:csg:test", Value: []byte("not a pending record"),
		}},
	}
	if _, _, err := DecodeLegacyWalletSnapshotForMigration(
		legacySnapshotPayloadForTest(t, snapshot, nil),
	); err == nil || !strings.Contains(err.Error(), ErrRGB11Inconsistent.Error()) {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestDecodeLegacyWalletSnapshotRejectsMismatchedObject(t *testing.T) {
	transferID := "rgb:csg:test"
	consignment := []byte("canonical consignment")
	hash := sha256.Sum256(consignment)
	pending := &PendingTransfer{
		State:            TransferState{TransferID: transferID, Direction: "send", Status: "broadcast"},
		LocalConsignment: consignment,
	}
	snapshot := &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: "rgb11-test", EngineBuildID: NativeEngineBuildID,
		ProjectionRecords: []SnapshotRecord{
			{Key: "object-" + hex.EncodeToString(hash[:]), Value: []byte("tampered")},
			{Key: "pending-" + transferID, Value: encodeLegacyPendingTransferForTest(t, pending)},
		},
	}
	if _, _, err := DecodeLegacyWalletSnapshotForMigration(
		legacySnapshotPayloadForTest(t, snapshot, nil),
	); err == nil {
		t.Fatal("accepted a mismatched canonical object")
	}
}
