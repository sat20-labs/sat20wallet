package rgb11wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	corewallet "github.com/sat20-labs/rgb11/wallet"
)

// AddValidatedConsignmentProjectionSnapshot adds one already core-validated
// consignment and its wallet-controlled allocation projections to an in-memory
// candidate. It does not access a store or persist any bytes.
func AddValidatedConsignmentProjectionSnapshot(records []SnapshotRecord, raw []byte,
	receipt *ValidationReceipt, replacements []ProjectionReplacement) ([]SnapshotRecord, error) {
	if len(raw) == 0 || receipt == nil {
		return nil, ErrValidationReceipt
	}
	objectHash := sha256.Sum256(raw)
	hash := hex.EncodeToString(objectHash[:])
	if err := receipt.validate(hash); err != nil {
		return nil, err
	}
	if err := validateProjectionSnapshot(records); err != nil {
		return nil, err
	}
	byKey := snapshotRecordMap(records)
	objectKey := "object-" + hash
	if previous, ok := byKey[objectKey]; ok && !bytes.Equal(previous.Value, raw) {
		return nil, ErrValidationReceipt
	}
	byKey[objectKey] = SnapshotRecord{Key: objectKey, Value: append([]byte(nil), raw...)}

	receiptCopy := *receipt
	receiptKey := "validation-" + hash
	if previous, ok := byKey[receiptKey]; ok {
		var existing ValidationReceipt
		if decode(previous.Value, &existing) != nil || existing.validate(hash) != nil {
			return nil, ErrValidationReceipt
		}
		receiptCopy.ValidatedAt = existing.ValidatedAt
		previousBytes, err := encode(&existing)
		if err != nil {
			return nil, err
		}
		candidateBytes, err := encode(&receiptCopy)
		if err != nil || !bytes.Equal(previousBytes, candidateBytes) {
			return nil, fmt.Errorf("%w: immutable consignment receipt changed", ErrValidationReceipt)
		}
		byKey[receiptKey] = previous
	} else {
		encoded, err := encode(&receiptCopy)
		if err != nil {
			return nil, err
		}
		byKey[receiptKey] = SnapshotRecord{Key: receiptKey, Value: encoded}
	}

	receiptHash, err := receiptCopy.Hash()
	if err != nil {
		return nil, err
	}
	outputs := make(map[string]*indexer.TxOutput)
	seenProofs := make(map[string]bool, len(replacements))
	for _, replacement := range replacements {
		if replacement.Output == nil || replacement.Asset == nil || replacement.Proof == nil ||
			replacement.Output.OutPointStr == "" || replacement.Output.OutPointStr != replacement.Proof.OutPoint ||
			replacement.Asset.Name != replacement.Proof.AssetName || replacement.Asset.Name.Protocol != Protocol ||
			replacement.Proof.ConsignmentHash != hash || replacement.Proof.ValidationHash != receiptHash {
			return nil, ErrProjectionMismatch
		}
		proofKey := "proof-" + replacement.Proof.OutPoint + "-" + replacement.Proof.AssetName.String()
		if seenProofs[proofKey] {
			return nil, fmt.Errorf("%w: duplicate candidate proof %s", ErrInvalidProof, proofKey)
		}
		seenProofs[proofKey] = true
		proof := replacement.Proof
		if proof.Status != "valid" && proof.Status != "settled" && proof.Status != "spending" &&
			proof.Status != "inconsistent" && proof.Status != "orphaned" {
			return nil, ErrInvalidProof
		}
		matched := false
		for _, allocation := range receiptCopy.Allocations {
			if allocation.OutPoint == proof.OutPoint && allocation.AssetName == proof.AssetName &&
				allocation.OperationID == proof.OperationID && allocation.AssignmentType == proof.AssignmentType &&
				allocation.AssignmentIndex == proof.AssignmentIndex && allocation.StateClass == proof.StateClass &&
				bytes.Equal(allocation.StateData, proof.StateData) &&
				bytes.Equal(allocation.SealDisclosure, proof.SealDisclosure) &&
				allocation.Amount.Cmp(&replacement.Asset.Amount) == 0 {
				matched = true
				break
			}
		}
		if !matched {
			return nil, ErrInvalidProof
		}
		if previous, exists := byKey[proofKey]; exists {
			var existing AllocationProof
			if decode(previous.Value, &existing) != nil || !sameAllocationProofIdentity(existing, *proof) {
				return nil, fmt.Errorf("%w: immutable candidate proof changed: %s", ErrInvalidProof, proofKey)
			}
		}

		outpoint := replacement.Output.OutPointStr
		projected := outputs[outpoint]
		if projected == nil {
			outputKey := "output-" + outpoint
			if previous, ok := byKey[outputKey]; ok {
				projected = new(indexer.TxOutput)
				if decode(previous.Value, projected) != nil || projected.OutPointStr != outpoint ||
					projected.OutValue.Value != replacement.Output.OutValue.Value ||
					!bytes.Equal(projected.OutValue.PkScript, replacement.Output.OutValue.PkScript) {
					return nil, ErrProjectionMismatch
				}
				projected = projected.Clone()
			} else {
				projected = replacement.Output.Clone()
				projected.Assets = nil
			}
			outputs[outpoint] = projected
		}
		if existingAsset := projected.GetAsset(&replacement.Asset.Name); existingAsset != nil {
			if existingAsset.Cmp(&replacement.Asset.Amount) != 0 || replacement.Asset.BindingSat != 0 {
				return nil, ErrProjectionMismatch
			}
		} else if err := projected.Assets.Add(replacement.Asset.Clone()); err != nil {
			return nil, err
		}
		encodedProof, err := encode(proof)
		if err != nil {
			return nil, err
		}
		byKey[proofKey] = SnapshotRecord{Key: proofKey, Value: encodedProof}
	}
	for outpoint, output := range outputs {
		encoded, err := encode(output)
		if err != nil {
			return nil, err
		}
		key := "output-" + outpoint
		byKey[key] = SnapshotRecord{Key: key, Value: encoded}
	}
	result := snapshotRecordValues(byKey)
	if err := validateProjectionSnapshot(result); err != nil {
		return nil, err
	}
	return result, nil
}

// RebindValidatedConsignmentReceiptSnapshot replaces one current-view receipt
// in a candidate and updates only proof receipt hashes that still match the
// newly validated full Opouts. The caller must supply a receipt returned by
// the consensus validator for the same raw consignment. This is used when a
// reorg reveals additional current states and the old receipt's allocation
// list was a strict subset; proofs for states absent from the new report make
// the replacement fail closed.
func RebindValidatedConsignmentReceiptSnapshot(records []SnapshotRecord,
	existing, fresh *ValidationReceipt) ([]SnapshotRecord, error) {
	if existing == nil || fresh == nil || existing.Version != fresh.Version ||
		existing.ConsignmentHash != fresh.ConsignmentHash || existing.ContractID != fresh.ContractID ||
		existing.SchemaID != fresh.SchemaID || existing.TransferID != fresh.TransferID ||
		existing.Status != fresh.Status {
		return nil, ErrValidationReceipt
	}
	oldHash, err := existing.Hash()
	if err != nil {
		return nil, err
	}
	newHash, err := fresh.Hash()
	if err != nil {
		return nil, err
	}
	outputs, err := OutputsFromProjectionSnapshot(records)
	if err != nil {
		return nil, err
	}
	validationKey := "validation-" + existing.ConsignmentHash
	foundReceipt := false
	result := make([]SnapshotRecord, 0, len(records))
	for _, record := range records {
		if record.Key == validationKey {
			var stored ValidationReceipt
			if foundReceipt || decode(record.Value, &stored) != nil || stored.validate(existing.ConsignmentHash) != nil {
				return nil, ErrValidationReceipt
			}
			storedHash, hashErr := stored.Hash()
			existingBytes, encodeErr := encode(existing)
			if hashErr != nil || encodeErr != nil || storedHash != oldHash ||
				!bytes.Equal(record.Value, existingBytes) {
				return nil, ErrValidationReceipt
			}
			foundReceipt = true
			continue
		}
		if !strings.HasPrefix(record.Key, "proof-") {
			result = append(result, record)
			continue
		}
		var proof AllocationProof
		if err := decode(record.Value, &proof); err != nil {
			return nil, ErrInvalidProof
		}
		if proof.ConsignmentHash != existing.ConsignmentHash {
			result = append(result, record)
			continue
		}
		if proof.ValidationHash != oldHash || !proofMatchesReceiptAllocation(&proof, outputs[proof.OutPoint], fresh) {
			return nil, ErrValidationReceipt
		}
		proof.ValidationHash = newHash
		encoded, err := encode(&proof)
		if err != nil {
			return nil, err
		}
		result = append(result, SnapshotRecord{Key: record.Key, Value: encoded})
	}
	if !foundReceipt {
		return nil, ErrValidationReceipt
	}
	encodedFresh, err := encode(fresh)
	if err != nil {
		return nil, err
	}
	result = append(result, SnapshotRecord{Key: validationKey, Value: encodedFresh})
	return result, nil
}

func proofMatchesReceiptAllocation(proof *AllocationProof, output *indexer.TxOutput,
	receipt *ValidationReceipt) bool {
	if proof == nil || output == nil || receipt == nil || output.OutPointStr != proof.OutPoint {
		return false
	}
	asset := output.GetAsset(&proof.AssetName)
	if asset == nil {
		return false
	}
	for _, allocation := range receipt.Allocations {
		if allocation.OutPoint != proof.OutPoint || allocation.AssetName != proof.AssetName ||
			allocation.OperationID != proof.OperationID || allocation.AssignmentType != proof.AssignmentType ||
			allocation.AssignmentIndex != proof.AssignmentIndex || allocation.StateClass != proof.StateClass ||
			!bytes.Equal(allocation.StateData, proof.StateData) ||
			!bytes.Equal(allocation.SealDisclosure, proof.SealDisclosure) ||
			allocation.Amount.Cmp(asset) != 0 {
			continue
		}
		commitment := consensus.TaggedHash(consensus.SecretSealCommitmentTag, allocation.SealDisclosure)
		return proof.SealCommitment == hex.EncodeToString(commitment[:])
	}
	return false
}

// AddContractObjectSnapshotReference adds the immutable full-contract object
// used by the existing ticker/export index. The reference is derived from the
// actual contract consignment and does not create another authority record.
func AddContractObjectSnapshotReference(records []SnapshotRecord, contractID string,
	raw []byte) ([]SnapshotRecord, error) {
	if contractID == "" || len(raw) == 0 {
		return nil, ErrValidationReceipt
	}
	container, err := coreconsignment.Decode(raw)
	if err != nil || container == nil || container.Armor == nil ||
		container.Armor.Type != "contract" || container.ContractID != contractID {
		return nil, ErrValidationReceipt
	}
	if err := validateProjectionSnapshot(records); err != nil {
		return nil, err
	}
	objectDigest := sha256.Sum256(raw)
	objectHash := hex.EncodeToString(objectDigest[:])
	contractDigest := sha256.Sum256([]byte(contractID))
	contractKey := "contract-object-" + hex.EncodeToString(contractDigest[:])
	byKey := snapshotRecordMap(records)
	if previous, ok := byKey[contractKey]; ok && string(previous.Value) != objectHash {
		return nil, ErrValidationReceipt
	}
	if err := addSnapshotObject(byKey, objectHash, raw); err != nil {
		return nil, err
	}
	byKey[contractKey] = SnapshotRecord{Key: contractKey, Value: []byte(objectHash)}
	result := snapshotRecordValues(byKey)
	if err := validateProjectionSnapshot(result); err != nil {
		return nil, err
	}
	return result, nil
}

// AddPreparedConsignmentObjectSnapshot preserves exact pre-broadcast bytes
// without creating a validated receipt or spendable projection.
func AddPreparedConsignmentObjectSnapshot(records []SnapshotRecord, raw []byte) ([]SnapshotRecord, error) {
	if len(raw) == 0 {
		return nil, ErrValidationReceipt
	}
	if err := validateProjectionSnapshot(records); err != nil {
		return nil, err
	}
	hash := sha256.Sum256(raw)
	objectHash := hex.EncodeToString(hash[:])
	byKey := snapshotRecordMap(records)
	if err := addSnapshotObject(byKey, objectHash, raw); err != nil {
		return nil, err
	}
	result := snapshotRecordValues(byKey)
	if err := validateProjectionSnapshot(result); err != nil {
		return nil, err
	}
	return result, nil
}

// UpsertProjectionSnapshotPending replaces one sender lifecycle record in an
// in-memory candidate and carries its exact recipient/local bytes into the
// content-addressed object records before stripping runtime copies.
func UpsertProjectionSnapshotPending(records []SnapshotRecord, pending *PendingTransfer) ([]SnapshotRecord, error) {
	if pending == nil || pending.State.TransferID == "" {
		return nil, ErrValidationReceipt
	}
	if err := validateProjectionSnapshot(records); err != nil {
		return nil, fmt.Errorf("validate base projection before pending upsert: %w", err)
	}
	byKey := snapshotRecordMap(records)
	copyPending := *pending
	if len(copyPending.RecipientConsignment) > 0 {
		hash := sha256.Sum256(copyPending.RecipientConsignment)
		objectHash := hex.EncodeToString(hash[:])
		if copyPending.RecipientObjectHash != "" && copyPending.RecipientObjectHash != objectHash {
			return nil, ErrValidationReceipt
		}
		if err := addSnapshotObject(byKey, objectHash, copyPending.RecipientConsignment); err != nil {
			return nil, err
		}
		copyPending.RecipientObjectHash = objectHash
	}
	if len(copyPending.LocalConsignment) > 0 {
		hash := sha256.Sum256(copyPending.LocalConsignment)
		objectHash := hex.EncodeToString(hash[:])
		if copyPending.LocalObjectHash != "" && copyPending.LocalObjectHash != objectHash {
			return nil, ErrValidationReceipt
		}
		if err := addSnapshotObject(byKey, objectHash, copyPending.LocalConsignment); err != nil {
			return nil, err
		}
		copyPending.LocalObjectHash = objectHash
	}
	copyPending.RecipientConsignment = nil
	copyPending.LocalConsignment = nil
	key := "pending-" + copyPending.State.TransferID
	if previous, ok := byKey[key]; ok {
		var old PendingTransfer
		if decodeErr := decode(previous.Value, &old); decodeErr != nil {
			return nil, fmt.Errorf("decode existing pending row: %w", decodeErr)
		}
		if !samePendingIdentity(old, copyPending) {
			return nil, fmt.Errorf("pending row %s immutable identity changed: %w", copyPending.State.TransferID, ErrValidationReceipt)
		}
	}
	encoded, err := encode(&copyPending)
	if err != nil {
		return nil, err
	}
	byKey[key] = SnapshotRecord{
		Key: key, Value: encoded,
	}
	result := snapshotRecordValues(byKey)
	if err := validateProjectionSnapshot(result); err != nil {
		return nil, fmt.Errorf("validate candidate projection after pending upsert: %w", err)
	}
	return result, nil
}

// UpsertProjectionSnapshotTransfer replaces one receiver lifecycle record in
// the same candidate that holds its projection and receive request.
func UpsertProjectionSnapshotTransfer(records []SnapshotRecord, state *TransferState) ([]SnapshotRecord, error) {
	if state == nil || state.TransferID == "" || state.Direction == "" || state.Status == "" {
		return nil, ErrValidationReceipt
	}
	if err := validateProjectionSnapshot(records); err != nil {
		return nil, err
	}
	encoded, err := encode(state)
	if err != nil {
		return nil, err
	}
	byKey := snapshotRecordMap(records)
	key := "transfer-" + state.TransferID
	if previous, ok := byKey[key]; ok {
		var old TransferState
		if decode(previous.Value, &old) != nil || !sameTransferIdentity(old, *state) {
			return nil, ErrValidationReceipt
		}
	}
	byKey[key] = SnapshotRecord{Key: key, Value: encoded}
	result := snapshotRecordValues(byKey)
	if err := validateProjectionSnapshot(result); err != nil {
		return nil, err
	}
	return result, nil
}

// UpsertProjectionSnapshotReceiveKey adds the wallet-derived key reveal that
// closes a witness receive request. Its key is the existing witness-script
// hash identity used by ProjectionStore.
func UpsertProjectionSnapshotReceiveKey(records []SnapshotRecord, key *ReceiveKey) ([]SnapshotRecord, error) {
	if key == nil || key.RequestID == "" || len(key.WitnessScript) == 0 {
		return nil, ErrValidationReceipt
	}
	if err := validateProjectionSnapshot(records); err != nil {
		return nil, err
	}
	hash := sha256.Sum256(key.WitnessScript)
	storageKey := "receive-key-" + hex.EncodeToString(hash[:])
	byKey := snapshotRecordMap(records)
	if previous, ok := byKey[storageKey]; ok {
		var existing ReceiveKey
		if decode(previous.Value, &existing) != nil || !reflect.DeepEqual(existing, *key) {
			return nil, ErrValidationReceipt
		}
	}
	encoded, err := encode(key)
	if err != nil {
		return nil, err
	}
	byKey[storageKey] = SnapshotRecord{Key: storageKey, Value: encoded}
	result := snapshotRecordValues(byKey)
	if err := validateProjectionSnapshot(result); err != nil {
		return nil, err
	}
	return result, nil
}

// UpsertProjectionSnapshotReceiveReservation stages the existing receive
// reservation row in the same candidate as the engine request and carrier
// lock plan.
func UpsertProjectionSnapshotReceiveReservation(records []SnapshotRecord,
	reservation *ReceiveReservation) ([]SnapshotRecord, error) {
	if reservation == nil || reservation.RequestID == "" || reservation.ReservationID == "" {
		return nil, ErrValidationReceipt
	}
	if err := validateProjectionSnapshot(records); err != nil {
		return nil, err
	}
	key := "receive-reservation-" + reservation.RequestID
	byKey := snapshotRecordMap(records)
	if previous, ok := byKey[key]; ok {
		var existing ReceiveReservation
		if decode(previous.Value, &existing) != nil || !reflect.DeepEqual(existing, *reservation) {
			return nil, ErrValidationReceipt
		}
	}
	encoded, err := encode(reservation)
	if err != nil {
		return nil, err
	}
	byKey[key] = SnapshotRecord{Key: key, Value: encoded}
	result := snapshotRecordValues(byKey)
	if err := validateProjectionSnapshot(result); err != nil {
		return nil, err
	}
	return result, nil
}

// UpsertProjectionSnapshotPreparedReceive stores the existing transfer to
// request association used while a remote witness is not yet current.
func UpsertProjectionSnapshotPreparedReceive(records []SnapshotRecord,
	transferID, requestID string) ([]SnapshotRecord, error) {
	if transferID == "" {
		return nil, ErrValidationReceipt
	}
	decoded, err := hex.DecodeString(requestID)
	if err != nil || len(decoded) != sha256.Size {
		return nil, ErrValidationReceipt
	}
	if err := validateProjectionSnapshot(records); err != nil {
		return nil, err
	}
	key := "prepared-receive-" + transferID
	byKey := snapshotRecordMap(records)
	if previous, ok := byKey[key]; ok && string(previous.Value) != requestID {
		return nil, ErrValidationReceipt
	}
	byKey[key] = SnapshotRecord{Key: key, Value: []byte(requestID)}
	result := snapshotRecordValues(byKey)
	if err := validateProjectionSnapshot(result); err != nil {
		return nil, err
	}
	return result, nil
}

// RemoveProjectionSnapshotKeys removes only the listed exact record identities
// from an in-memory candidate. Callers must validate the resulting snapshot.
func RemoveProjectionSnapshotKeys(records []SnapshotRecord, keys ...string) ([]SnapshotRecord, error) {
	if err := validateProjectionSnapshot(records); err != nil {
		return nil, err
	}
	byKey := snapshotRecordMap(records)
	for _, key := range keys {
		if key == "" || strings.Contains(key, "/") {
			return nil, ErrValidationReceipt
		}
		delete(byKey, key)
	}
	result := snapshotRecordValues(byKey)
	if err := validateProjectionSnapshot(result); err != nil {
		return nil, err
	}
	return result, nil
}

// UpsertEngineSnapshotReceive replaces one receive request while refusing to
// change its immutable invoice, recipient, seal, script or expiry identity.
func UpsertEngineSnapshotReceive(records []SnapshotRecord, request *corewallet.ReceiveRequest) ([]SnapshotRecord, error) {
	if request == nil || request.RequestID == "" {
		return nil, corewallet.ErrInvalidReceive
	}
	if err := validateEngineSnapshot(records); err != nil {
		return nil, err
	}
	key := "wallet/receive/" + request.RequestID
	for _, record := range records {
		if record.Key != key {
			continue
		}
		previous, err := corewallet.DecodeReceiveRequest(record.Value)
		if err != nil {
			return nil, corewallet.ErrInvalidReceive
		}
		previousSeal, previousSealErr := previous.Seal.StrictBytes()
		requestSeal, requestSealErr := request.Seal.StrictBytes()
		if previousSealErr != nil || requestSealErr != nil ||
			previous.Version != request.Version || previous.Mode != request.Mode ||
			previous.RequestID != request.RequestID || previous.RecipientID != request.RecipientID ||
			!bytes.Equal(previousSeal, requestSeal) || !bytes.Equal(previous.WitnessScript, request.WitnessScript) ||
			previous.Invoice != request.Invoice || previous.RelayKey != request.RelayKey ||
			previous.AckKey != request.AckKey || previous.CreatedAt != request.CreatedAt ||
			previous.Expiry != request.Expiry {
			return nil, corewallet.ErrInvalidReceive
		}
		break
	}
	encoded, err := corewallet.EncodeReceiveRequest(request)
	if err != nil {
		return nil, err
	}
	byKey := snapshotRecordMap(records)
	byKey[key] = SnapshotRecord{Key: key, Value: encoded}
	result := snapshotRecordValues(byKey)
	if err := validateEngineSnapshot(result); err != nil {
		return nil, err
	}
	return result, nil
}

func addSnapshotObject(records map[string]SnapshotRecord, hash string, raw []byte) error {
	actual := sha256.Sum256(raw)
	if hash == "" || hex.EncodeToString(actual[:]) != hash {
		return ErrValidationReceipt
	}
	key := "object-" + hash
	if existing, ok := records[key]; ok && !bytes.Equal(existing.Value, raw) {
		return ErrValidationReceipt
	}
	records[key] = SnapshotRecord{Key: key, Value: append([]byte(nil), raw...)}
	return nil
}

func snapshotRecordMap(records []SnapshotRecord) map[string]SnapshotRecord {
	byKey := make(map[string]SnapshotRecord, len(records))
	for _, record := range records {
		byKey[record.Key] = cloneSnapshotRecord(record)
	}
	return byKey
}

func snapshotRecordValues(records map[string]SnapshotRecord) []SnapshotRecord {
	result := make([]SnapshotRecord, 0, len(records))
	for _, record := range records {
		result = append(result, cloneSnapshotRecord(record))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}

func sameTransferIdentity(left, right TransferState) bool {
	left.Status, right.Status = "", ""
	left.AckStatus, right.AckStatus = "", ""
	left.RejectReason, right.RejectReason = "", ""
	left.RejectedOpouts, right.RejectedOpouts = nil, nil
	return reflect.DeepEqual(left, right)
}

func sameAllocationProofIdentity(left, right AllocationProof) bool {
	left.Status, right.Status = "", ""
	left.Confirmations, right.Confirmations = 0, 0
	left.PolicyStatus, right.PolicyStatus = "", ""
	left.PolicyReason, right.PolicyReason = "", ""
	leftRaw, leftErr := encode(&left)
	rightRaw, rightErr := encode(&right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftRaw, rightRaw)
}

func samePendingIdentity(left, right PendingTransfer) bool {
	left.RecipientConsignment, right.RecipientConsignment = nil, nil
	left.LocalConsignment, right.LocalConsignment = nil, nil
	left.State.Status, right.State.Status = "", ""
	left.State.AckStatus, right.State.AckStatus = "", ""
	left.State.RejectReason, right.State.RejectReason = "", ""
	left.State.RejectedOpouts, right.State.RejectedOpouts = nil, nil
	return reflect.DeepEqual(left, right)
}
