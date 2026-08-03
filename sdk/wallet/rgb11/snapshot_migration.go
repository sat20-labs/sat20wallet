package rgb11wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/sat20-labs/rgb11/seals"
	strict "github.com/sat20-labs/rgb11/strict_encoding"
)

// DecodeLegacyWalletSnapshotForMigration decodes the short-lived snapshot
// layout that appended derivable ticker references after the two state record
// sets. It is intentionally separate from the runtime decoder.
func DecodeLegacyWalletSnapshotForMigration(payload []byte) (*RGB11WalletSnapshot, bool, error) {
	if snapshot, err := DecodeWalletSnapshotPayload(payload); err == nil {
		if err := ValidateWalletSnapshot(snapshot); err != nil {
			return nil, false, err
		}
		return snapshot, false, nil
	}
	raw, err := inflateRGB11Snapshot(payload)
	if err != nil {
		return nil, false, err
	}
	reader := bytes.NewReader(raw)
	decoder := strict.NewDecoder(reader)
	magic, err := decoder.Raw(uint64(len(rgb11SnapshotPayloadMagic)))
	if err != nil || string(magic) != rgb11SnapshotPayloadMagic {
		return nil, false, ErrRGB11Inconsistent
	}
	codecVersion, err := decoder.U8()
	if err != nil || codecVersion != rgb11SnapshotCodecVersion {
		return nil, false, ErrRGB11Inconsistent
	}
	version, err := decoder.U32()
	if err != nil || version != WalletSnapshotVersion {
		return nil, false, ErrRGB11Inconsistent
	}
	walletID, err := decoder.String(1, 128)
	if err != nil {
		return nil, false, err
	}
	accountIndex, err := decoder.U32()
	if err != nil {
		return nil, false, err
	}
	buildID, err := decoder.String(0, 1024)
	if err != nil {
		return nil, false, err
	}
	projection, err := decodeRGB11SnapshotRecords(decoder)
	if err != nil {
		return nil, false, err
	}
	engine, err := decodeRGB11SnapshotRecords(decoder)
	if err != nil {
		return nil, false, err
	}
	if reader.Len() == 0 {
		return nil, false, ErrRGB11Inconsistent
	}
	refCount, err := decoder.Length(rgb11SnapshotMaxRecords)
	if err != nil {
		return nil, false, err
	}
	legacyRefs := make([]SnapshotTickerRef, 0, refCount)
	for index := uint64(0); index < refCount; index++ {
		contractID, decodeErr := decoder.String(1, 128)
		if decodeErr != nil {
			return nil, false, decodeErr
		}
		assetName, decodeErr := decoder.String(1, 1024)
		if decodeErr != nil {
			return nil, false, decodeErr
		}
		legacyRefs = append(legacyRefs, SnapshotTickerRef{
			ContractID: contractID,
			AssetName:  assetName,
		})
	}
	if reader.Len() != 0 {
		return nil, false, ErrRGB11Inconsistent
	}
	projection, err = normalizeLegacyProjectionSnapshot(projection)
	if err != nil {
		return nil, false, err
	}
	derivedRefs, err := TickerRefsFromProjectionSnapshot(projection)
	if err != nil || !equalSnapshotTickerRefs(legacyRefs, derivedRefs) {
		return nil, false, ErrRGB11Inconsistent
	}
	snapshot := &RGB11WalletSnapshot{
		Version: version, WalletID: walletID, AccountIndex: accountIndex, EngineBuildID: buildID,
		ProjectionRecords: projection, EngineRecords: engine,
	}
	if err := ValidateWalletSnapshot(snapshot); err != nil {
		return nil, false, err
	}
	return snapshot, true, nil
}

func normalizeLegacyProjectionSnapshot(records []SnapshotRecord) ([]SnapshotRecord, error) {
	result := make([]SnapshotRecord, len(records))
	objects := make(map[string][]byte)
	seen := make(map[string]struct{}, len(records))
	for index, record := range records {
		if record.Key == "" || len(record.Value) == 0 {
			return nil, ErrValidationReceipt
		}
		if _, exists := seen[record.Key]; exists {
			return nil, ErrValidationReceipt
		}
		seen[record.Key] = struct{}{}
		result[index] = SnapshotRecord{Key: record.Key, Value: append([]byte(nil), record.Value...)}
		if strings.HasPrefix(record.Key, "object-") {
			objects[strings.TrimPrefix(record.Key, "object-")] = append([]byte(nil), record.Value...)
		}
	}

	deliveryObjects := make(map[string]struct{})
	canonicalObjects := make(map[string]struct{})
	for index := range result {
		if !strings.HasPrefix(result[index].Key, "pending-") {
			continue
		}
		var pending PendingTransfer
		if err := decode(result[index].Value, &pending); err != nil {
			legacy, legacyErr := decodeLegacyPendingTransferRecord(result[index].Value)
			if legacyErr != nil {
				return nil, legacyErr
			}
			pending = *legacy
		}
		if pending.State.TransferID == "" ||
			pending.State.TransferID != strings.TrimPrefix(result[index].Key, "pending-") {
			return nil, ErrValidationReceipt
		}
		if len(pending.RecipientConsignment) != 0 {
			hash := sha256.Sum256(pending.RecipientConsignment)
			pending.RecipientObjectHash = hex.EncodeToString(hash[:])
			if pending.State.AddressMode && pending.State.Status != "prepared" {
				deliveryObjects[pending.RecipientObjectHash] = struct{}{}
			} else if err := addLegacySnapshotObject(objects, pending.RecipientObjectHash,
				pending.RecipientConsignment); err != nil {
				return nil, err
			}
		}
		if len(pending.LocalConsignment) != 0 {
			hash := sha256.Sum256(pending.LocalConsignment)
			pending.LocalObjectHash = hex.EncodeToString(hash[:])
			canonicalObjects[pending.LocalObjectHash] = struct{}{}
			if err := addLegacySnapshotObject(objects, pending.LocalObjectHash,
				pending.LocalConsignment); err != nil {
				return nil, err
			}
		}
		pending.RecipientConsignment = nil
		pending.LocalConsignment = nil
		if pending.State.Status != "prepared" {
			pending.SignedPSBT = nil
		}
		encoded, err := encode(&pending)
		if err != nil {
			return nil, err
		}
		result[index].Value = encoded
	}

	filtered := result[:0]
	for _, record := range result {
		if strings.HasPrefix(record.Key, "object-") {
			hash := strings.TrimPrefix(record.Key, "object-")
			_, delivery := deliveryObjects[hash]
			_, canonical := canonicalObjects[hash]
			if delivery && !canonical {
				continue
			}
		}
		filtered = append(filtered, record)
	}
	for hash, raw := range objects {
		key := "object-" + hash
		if _, exists := seen[key]; exists {
			continue
		}
		if _, delivery := deliveryObjects[hash]; delivery {
			if _, canonical := canonicalObjects[hash]; !canonical {
				continue
			}
		}
		filtered = append(filtered, SnapshotRecord{Key: key, Value: append([]byte(nil), raw...)})
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Key < filtered[j].Key })
	return filtered, nil
}

func addLegacySnapshotObject(objects map[string][]byte, hash string, raw []byte) error {
	if !validSnapshotObjectHash(hash) || len(raw) == 0 {
		return ErrValidationReceipt
	}
	actual := sha256.Sum256(raw)
	if hash != hex.EncodeToString(actual[:]) {
		return ErrValidationReceipt
	}
	if existing := objects[hash]; existing != nil && !bytes.Equal(existing, raw) {
		return ErrValidationReceipt
	}
	objects[hash] = append([]byte(nil), raw...)
	return nil
}

func decodeLegacyPendingTransferRecord(data []byte) (*PendingTransfer, error) {
	if len(data) == 0 || len(data) > rgb11StoreMaxBytes {
		return nil, ErrRGB11Inconsistent
	}
	reader := bytes.NewReader(data)
	decoder := strict.NewDecoder(reader)
	magic, err := decoder.Raw(uint64(len(rgb11StoreMagic)))
	if err != nil || string(magic) != rgb11StoreMagic {
		return nil, ErrRGB11Inconsistent
	}
	version, err := decoder.U8()
	if err != nil || version != rgb11StoreCodecVersion {
		return nil, ErrRGB11Inconsistent
	}
	kind, err := decoder.U8()
	if err != nil || kind != rgb11RecordPending {
		return nil, ErrRGB11Inconsistent
	}
	pending := &PendingTransfer{}
	if err := decodeTransferState(decoder, &pending.State); err != nil {
		return nil, ErrRGB11Inconsistent
	}
	for _, target := range []*[]byte{
		&pending.RecipientConsignment, &pending.LocalConsignment,
		&pending.SignedTx, &pending.SignedPSBT,
	} {
		if *target, err = decodeBlob(decoder); err != nil {
			return nil, ErrRGB11Inconsistent
		}
	}
	count, err := decoder.Length(rgb11StoreMaxRecords)
	if err != nil {
		return nil, ErrRGB11Inconsistent
	}
	pending.ChangeSeals = make([]seals.GraphBlindSeal, count)
	for index := range pending.ChangeSeals {
		encoded, decodeErr := decoder.Bytes(13, 45)
		if decodeErr != nil {
			return nil, ErrRGB11Inconsistent
		}
		pending.ChangeSeals[index], decodeErr = seals.DecodeGraphBlindSeal(encoded)
		if decodeErr != nil {
			return nil, ErrRGB11Inconsistent
		}
	}
	createdAt, err := decoder.U64()
	if err != nil || reader.Len() != 0 {
		return nil, ErrRGB11Inconsistent
	}
	pending.CreatedAt = int64(createdAt)
	return pending, nil
}

func equalSnapshotTickerRefs(left, right []SnapshotTickerRef) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
