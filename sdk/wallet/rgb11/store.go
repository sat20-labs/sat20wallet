package rgb11wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	indexer "github.com/sat20-labs/indexer/common"
)

var (
	ErrProjectionMismatch = errors.New("RGB11 allocation proof does not match output projection")
	ErrInvalidProof       = errors.New("RGB11 allocation proof is not valid")
	ErrWalletScope        = errors.New("RGB11 wallet storage scope is not selected")
)

type Locker interface {
	LockUtxo(utxo, reason string) error
}

type ProjectionStore struct {
	db     indexer.KVDB
	locker Locker
	mu     sync.RWMutex
	scope  string
}

type ProjectionReplacement struct {
	Output *indexer.TxOutput
	Asset  *indexer.AssetInfo
	Proof  *AllocationProof
}

func (s *ProjectionStore) SetScope(scope string) error {
	if scope == "" || strings.ContainsAny(scope, "/:") {
		return ErrWalletScope
	}
	s.mu.Lock()
	s.scope = scope
	s.mu.Unlock()
	return nil
}

func (s *ProjectionStore) scopedPrefix(prefix string) ([]byte, error) {
	s.mu.RLock()
	scope := s.scope
	s.mu.RUnlock()
	if scope == "" {
		return nil, ErrWalletScope
	}
	return []byte("rgb11-" + scope + "-" + prefix), nil
}

func NewProjectionStore(db indexer.KVDB, locker Locker) *ProjectionStore {
	return &ProjectionStore{db: db, locker: locker}
}

func encode(value any) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(value); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decode(data []byte, target any) error {
	return gob.NewDecoder(bytes.NewReader(data)).Decode(target)
}

func (s *ProjectionStore) outputKey(outpoint string) ([]byte, error) {
	prefix, err := s.scopedPrefix("output-")
	return append(prefix, []byte(outpoint)...), err
}
func (s *ProjectionStore) proofKey(outpoint string, name indexer.AssetName) ([]byte, error) {
	prefix, err := s.scopedPrefix("proof-")
	return append(prefix, []byte(outpoint+"-"+name.String())...), err
}

func (s *ProjectionStore) validationKey(consignmentHash string) ([]byte, error) {
	prefix, err := s.scopedPrefix("validation-")
	return append(prefix, []byte(consignmentHash)...), err
}

func (s *ProjectionStore) objectKey(consignmentHash string) ([]byte, error) {
	prefix, err := s.scopedPrefix("object-")
	return append(prefix, []byte(consignmentHash)...), err
}

func (s *ProjectionStore) pendingKey(transferID string) ([]byte, error) {
	prefix, err := s.scopedPrefix("pending-")
	return append(prefix, []byte(transferID)...), err
}

func (s *ProjectionStore) transferKey(transferID string) ([]byte, error) {
	prefix, err := s.scopedPrefix("transfer-")
	return append(prefix, []byte(transferID)...), err
}

// LocalMetadata is deliberately outside the portable snapshot prefix. It is
// used for device-local ordering cursors such as the last wallet head and is
// never treated as RGB contract state.
func (s *ProjectionStore) localMetadataKey(name string) ([]byte, error) {
	if name == "" || strings.ContainsAny(name, "/:") {
		return nil, ErrWalletScope
	}
	s.mu.RLock()
	scope := s.scope
	s.mu.RUnlock()
	if scope == "" {
		return nil, ErrWalletScope
	}
	return []byte("rgb11-local-" + scope + "-" + name), nil
}

func (s *ProjectionStore) SaveLocalMetadata(name string, value []byte) error {
	if s == nil || s.db == nil || len(value) == 0 {
		return ErrWalletScope
	}
	key, err := s.localMetadataKey(name)
	if err != nil {
		return err
	}
	return s.db.Write(key, append([]byte(nil), value...))
}

func (s *ProjectionStore) LoadLocalMetadata(name string) ([]byte, error) {
	if s == nil || s.db == nil {
		return nil, ErrWalletScope
	}
	key, err := s.localMetadataKey(name)
	if err != nil {
		return nil, err
	}
	value, err := s.db.Read(key)
	return append([]byte(nil), value...), err
}

// ValidateAndStoreConsignment runs the configured consensus validator before
// atomically storing the raw object and its immutable receipt.
func (s *ProjectionStore) ValidateAndStoreConsignment(ctx context.Context, validator ConsensusValidator, evidence BitcoinEvidenceProvider, raw []byte) (*ValidationReceipt, error) {
	if s == nil || s.db == nil {
		return nil, ErrValidationReceipt
	}
	receipt, err := ValidateWith(ctx, validator, raw, evidence)
	if err != nil {
		return nil, err
	}
	encodedReceipt, err := encode(receipt)
	if err != nil {
		return nil, err
	}
	validationKey, err := s.validationKey(receipt.ConsignmentHash)
	if err != nil {
		return nil, err
	}
	objectKey, err := s.objectKey(receipt.ConsignmentHash)
	if err != nil {
		return nil, err
	}
	batch := s.db.NewWriteBatch()
	if batch == nil {
		return nil, errors.New("RGB11 KVDB returned nil write batch")
	}
	defer batch.Close()
	if err := batch.Put(objectKey, append([]byte(nil), raw...)); err != nil {
		return nil, err
	}
	if err := batch.Put(validationKey, encodedReceipt); err != nil {
		return nil, err
	}
	if err := batch.Flush(); err != nil {
		return nil, err
	}
	return receipt, nil
}

func (s *ProjectionStore) LoadValidationReceipt(consignmentHash string) (*ValidationReceipt, error) {
	if decoded, err := hex.DecodeString(consignmentHash); err != nil || len(decoded) != sha256.Size {
		return nil, ErrValidationReceipt
	}
	key, err := s.validationKey(consignmentHash)
	if err != nil {
		return nil, err
	}
	data, err := s.db.Read(key)
	if err != nil {
		return nil, err
	}
	var receipt ValidationReceipt
	if err := decode(data, &receipt); err != nil {
		return nil, err
	}
	if err := receipt.validate(consignmentHash); err != nil {
		return nil, err
	}
	return &receipt, nil
}

func (s *ProjectionStore) LoadObject(consignmentHash string) ([]byte, error) {
	if decoded, err := hex.DecodeString(consignmentHash); err != nil || len(decoded) != sha256.Size {
		return nil, ErrValidationReceipt
	}
	key, err := s.objectKey(consignmentHash)
	if err != nil {
		return nil, err
	}
	value, err := s.db.Read(key)
	return append([]byte(nil), value...), err
}

// DiscardValidatedObject removes a consignment and its immutable validation
// receipt when receiver policy refuses it before projection. No spendable
// wallet history may refer to such an object.
func (s *ProjectionStore) DiscardValidatedObject(consignmentHash string) error {
	if decoded, err := hex.DecodeString(consignmentHash); err != nil || len(decoded) != sha256.Size {
		return ErrValidationReceipt
	}
	validationKey, err := s.validationKey(consignmentHash)
	if err != nil {
		return err
	}
	objectKey, err := s.objectKey(consignmentHash)
	if err != nil {
		return err
	}
	batch := s.db.NewWriteBatch()
	if batch == nil {
		return errors.New("RGB11 KVDB returned nil write batch")
	}
	defer batch.Close()
	if err := batch.Delete(validationKey); err != nil {
		return err
	}
	if err := batch.Delete(objectKey); err != nil {
		return err
	}
	return batch.Flush()
}

// SavePendingTransfer atomically persists private seal data, the signed
// witness transaction and both recipient/local consignments before relay or
// broadcast is allowed.
func (s *ProjectionStore) SavePendingTransfer(pending *PendingTransfer) error {
	return s.SavePendingTransfers([]*PendingTransfer{pending})
}

// SavePendingTransfers atomically persists every recipient state belonging to
// one Bitcoin batch before any relay record can be published.
func (s *ProjectionStore) SavePendingTransfers(pendingList []*PendingTransfer) error {
	if len(pendingList) == 0 {
		return ErrValidationReceipt
	}
	batch := s.db.NewWriteBatch()
	if batch == nil {
		return errors.New("RGB11 KVDB returned nil write batch")
	}
	defer batch.Close()
	seen := make(map[string]struct{}, len(pendingList))
	for _, pending := range pendingList {
		if pending == nil || pending.State.TransferID == "" || len(pending.RecipientConsignment) == 0 ||
			len(pending.LocalConsignment) == 0 || len(pending.SignedTx) == 0 || len(pending.SignedPSBT) == 0 {
			return ErrValidationReceipt
		}
		if _, ok := seen[pending.State.TransferID]; ok {
			return ErrValidationReceipt
		}
		seen[pending.State.TransferID] = struct{}{}
		recipientHash := sha256.Sum256(pending.RecipientConsignment)
		if pending.State.ConsignmentHash != hex.EncodeToString(recipientHash[:]) {
			return ErrValidationReceipt
		}
		localHash := sha256.Sum256(pending.LocalConsignment)
		encodedPending, err := encode(pending)
		if err != nil {
			return err
		}
		pendingKey, err := s.pendingKey(pending.State.TransferID)
		if err != nil {
			return err
		}
		recipientKey, err := s.objectKey(hex.EncodeToString(recipientHash[:]))
		if err != nil {
			return err
		}
		localKey, err := s.objectKey(hex.EncodeToString(localHash[:]))
		if err != nil {
			return err
		}
		if err := batch.Put(recipientKey, append([]byte(nil), pending.RecipientConsignment...)); err != nil {
			return err
		}
		if err := batch.Put(localKey, append([]byte(nil), pending.LocalConsignment...)); err != nil {
			return err
		}
		if err := batch.Put(pendingKey, encodedPending); err != nil {
			return err
		}
	}
	return batch.Flush()
}

func (s *ProjectionStore) LoadPendingTransfer(transferID string) (*PendingTransfer, error) {
	key, err := s.pendingKey(transferID)
	if err != nil {
		return nil, err
	}
	raw, err := s.db.Read(key)
	if err != nil {
		return nil, err
	}
	var pending PendingTransfer
	if err := decode(raw, &pending); err != nil {
		return nil, err
	}
	return &pending, nil
}

func (s *ProjectionStore) SavePendingTransferState(pending *PendingTransfer) error {
	return s.SavePendingTransferStates([]*PendingTransfer{pending})
}

// SavePendingTransferStates atomically advances all recipient lifecycle
// states belonging to one already-persisted Bitcoin batch.
func (s *ProjectionStore) SavePendingTransferStates(pendingList []*PendingTransfer) error {
	if len(pendingList) == 0 {
		return ErrValidationReceipt
	}
	batch := s.db.NewWriteBatch()
	if batch == nil {
		return errors.New("RGB11 KVDB returned nil write batch")
	}
	defer batch.Close()
	seen := make(map[string]struct{}, len(pendingList))
	for _, pending := range pendingList {
		if pending == nil || pending.State.TransferID == "" {
			return ErrValidationReceipt
		}
		if _, ok := seen[pending.State.TransferID]; ok {
			return ErrValidationReceipt
		}
		seen[pending.State.TransferID] = struct{}{}
		encoded, err := encode(pending)
		if err != nil {
			return err
		}
		key, err := s.pendingKey(pending.State.TransferID)
		if err != nil {
			return err
		}
		if err := batch.Put(key, encoded); err != nil {
			return err
		}
	}
	return batch.Flush()
}

// CompactSettledRecipientConsignments removes the sender-side delivery copy
// after every recipient sharing the Bitcoin transaction has settled. The
// local change consignment is retained because it is part of the wallet's
// spendable RGB history.
func (s *ProjectionStore) CompactSettledRecipientConsignments(transferIDs []string) error {
	if len(transferIDs) == 0 {
		return ErrValidationReceipt
	}
	pendingList := make([]*PendingTransfer, 0, len(transferIDs))
	var recipientHash string
	keepObject := false
	for _, transferID := range transferIDs {
		pending, err := s.LoadPendingTransfer(transferID)
		if err != nil {
			return err
		}
		if pending.State.Status != "settled" {
			return nil
		}
		if recipientHash == "" {
			recipientHash = pending.State.ConsignmentHash
		} else if pending.State.ConsignmentHash != recipientHash {
			return ErrValidationReceipt
		}
		localHash := sha256.Sum256(pending.LocalConsignment)
		if hex.EncodeToString(localHash[:]) == recipientHash {
			keepObject = true
		}
		pending.RecipientConsignment = nil
		pendingList = append(pendingList, pending)
	}
	batch := s.db.NewWriteBatch()
	if batch == nil {
		return errors.New("RGB11 KVDB returned nil write batch")
	}
	defer batch.Close()
	for _, pending := range pendingList {
		encoded, err := encode(pending)
		if err != nil {
			return err
		}
		key, err := s.pendingKey(pending.State.TransferID)
		if err != nil {
			return err
		}
		if err := batch.Put(key, encoded); err != nil {
			return err
		}
	}
	if !keepObject {
		key, err := s.objectKey(recipientHash)
		if err != nil {
			return err
		}
		if err := batch.Delete(key); err != nil {
			return err
		}
	}
	return batch.Flush()
}

// CompactRejectedTransfers drops unbroadcast transaction and consignment
// payloads after a terminal batch refusal while retaining the small lifecycle
// record for UI/history. RGB carrier history is never deleted by this method.
func (s *ProjectionStore) CompactRejectedTransfers(transferIDs []string) error {
	if len(transferIDs) == 0 {
		return ErrValidationReceipt
	}
	pendingList := make([]*PendingTransfer, 0, len(transferIDs))
	objectHashes := make(map[string]struct{})
	for _, transferID := range transferIDs {
		pending, err := s.LoadPendingTransfer(transferID)
		if err != nil {
			return err
		}
		if pending.State.Status != "rejected" {
			return ErrValidationReceipt
		}
		for _, raw := range [][]byte{pending.RecipientConsignment, pending.LocalConsignment} {
			if len(raw) > 0 {
				hash := sha256.Sum256(raw)
				objectHashes[hex.EncodeToString(hash[:])] = struct{}{}
			}
		}
		pending.RecipientConsignment = nil
		pending.LocalConsignment = nil
		pending.SignedTx = nil
		pending.SignedPSBT = nil
		pending.ChangeSeals = nil
		pendingList = append(pendingList, pending)
	}
	batch := s.db.NewWriteBatch()
	if batch == nil {
		return errors.New("RGB11 KVDB returned nil write batch")
	}
	defer batch.Close()
	for _, pending := range pendingList {
		encoded, err := encode(pending)
		if err != nil {
			return err
		}
		key, err := s.pendingKey(pending.State.TransferID)
		if err != nil {
			return err
		}
		if err := batch.Put(key, encoded); err != nil {
			return err
		}
	}
	for hash := range objectHashes {
		key, err := s.objectKey(hash)
		if err != nil {
			return err
		}
		if err := batch.Delete(key); err != nil {
			return err
		}
	}
	return batch.Flush()
}

// SaveTransferState persists lifecycle history which has no sender-side
// private PSBT payload, such as an incoming transfer accepted by this wallet.
func (s *ProjectionStore) SaveTransferState(state *TransferState) error {
	if state == nil || state.TransferID == "" || state.Direction == "" || state.Status == "" {
		return ErrValidationReceipt
	}
	encoded, err := encode(state)
	if err != nil {
		return err
	}
	key, err := s.transferKey(state.TransferID)
	if err != nil {
		return err
	}
	return s.db.Write(key, encoded)
}

func (s *ProjectionStore) LoadTransferState(transferID string) (*TransferState, error) {
	key, err := s.transferKey(transferID)
	if err != nil {
		return nil, err
	}
	raw, err := s.db.Read(key)
	if err != nil {
		return nil, err
	}
	var state TransferState
	if err := decode(raw, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *ProjectionStore) ListTransfers() ([]*TransferState, error) {
	byID := make(map[string]*TransferState)
	prefix, err := s.scopedPrefix("pending-")
	if err != nil {
		return nil, err
	}
	err = s.db.BatchRead(prefix, false, func(_, value []byte) error {
		var pending PendingTransfer
		if err := decode(value, &pending); err != nil {
			return err
		}
		state := pending.State
		byID[state.TransferID] = &state
		return nil
	})
	if err != nil {
		return nil, err
	}
	prefix, err = s.scopedPrefix("transfer-")
	if err != nil {
		return nil, err
	}
	if err := s.db.BatchRead(prefix, false, func(_, value []byte) error {
		var state TransferState
		if err := decode(value, &state); err != nil {
			return err
		}
		if _, exists := byID[state.TransferID]; !exists {
			copy := state
			byID[state.TransferID] = &copy
		}
		return nil
	}); err != nil {
		return nil, err
	}
	states := make([]*TransferState, 0, len(byID))
	for _, state := range byID {
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].TransferID < states[j].TransferID })
	return states, nil
}

// CommitProjection writes the generic TxOutput and its RGB proof sidecar in
// one KVDB batch. The outpoint is locked first, so any storage failure is
// fail-safe: it may leave an extra lock but can never expose an unlocked RGB
// carrier without its proof.
func (s *ProjectionStore) CommitProjection(output *indexer.TxOutput, asset *indexer.AssetInfo, proof *AllocationProof) error {
	if s == nil || s.db == nil || s.locker == nil || output == nil || asset == nil || proof == nil {
		return ErrProjectionMismatch
	}
	if output.OutPointStr == "" || proof.OutPoint != output.OutPointStr || proof.AssetName != asset.Name ||
		asset.Name.Protocol != Protocol || asset.BindingSat != 0 {
		return ErrProjectionMismatch
	}
	if proof.Status != "valid" && proof.Status != "settled" {
		return ErrInvalidProof
	}
	receipt, err := s.LoadValidationReceipt(proof.ConsignmentHash)
	if err != nil {
		return ErrInvalidProof
	}
	receiptHash, err := receipt.Hash()
	if err != nil {
		return err
	}
	if proof.ValidationHash != receiptHash {
		return ErrInvalidProof
	}
	matched := false
	for _, allocation := range receipt.Allocations {
		if allocation.OutPoint == proof.OutPoint && allocation.AssetName == proof.AssetName &&
			allocation.OperationID == proof.OperationID && allocation.AssignmentType == proof.AssignmentType &&
			allocation.AssignmentIndex == proof.AssignmentIndex && allocation.StateClass == proof.StateClass &&
			bytes.Equal(allocation.StateData, proof.StateData) && bytes.Equal(allocation.SealDisclosure, proof.SealDisclosure) &&
			allocation.Amount.Cmp(&asset.Amount) == 0 {
			matched = true
			break
		}
	}
	if !matched {
		return ErrInvalidProof
	}
	if err := s.locker.LockUtxo(output.OutPointStr, LockReasonRGB); err != nil {
		return err
	}
	projected := output.Clone()
	projected.RemoveAsset(&asset.Name)
	if err := projected.Assets.Add(asset); err != nil {
		return err
	}
	encodedOutput, err := encode(projected)
	if err != nil {
		return err
	}
	encodedProof, err := encode(proof)
	if err != nil {
		return err
	}
	outputDBKey, err := s.outputKey(output.OutPointStr)
	if err != nil {
		return err
	}
	proofDBKey, err := s.proofKey(output.OutPointStr, asset.Name)
	if err != nil {
		return err
	}
	batch := s.db.NewWriteBatch()
	if batch == nil {
		return errors.New("RGB11 KVDB returned nil write batch")
	}
	defer batch.Close()
	if err := batch.Put(outputDBKey, encodedOutput); err != nil {
		return err
	}
	if err := batch.Put(proofDBKey, encodedProof); err != nil {
		return err
	}
	return batch.Flush()
}

func (s *ProjectionStore) LoadOutput(outpoint string) (*indexer.TxOutput, error) {
	key, err := s.outputKey(outpoint)
	if err != nil {
		return nil, err
	}
	data, err := s.db.Read(key)
	if err != nil {
		return nil, err
	}
	var output indexer.TxOutput
	if err := decode(data, &output); err != nil {
		return nil, err
	}
	return &output, nil
}

func (s *ProjectionStore) LoadProof(outpoint string, name indexer.AssetName) (*AllocationProof, error) {
	key, err := s.proofKey(outpoint, name)
	if err != nil {
		return nil, err
	}
	data, err := s.db.Read(key)
	if err != nil {
		return nil, err
	}
	var proof AllocationProof
	if err := decode(data, &proof); err != nil {
		return nil, err
	}
	return &proof, nil
}

// SaveProofState updates chain-derived lifecycle fields only. Consensus-bound
// proof identity is checked against the currently stored sidecar.
func (s *ProjectionStore) SaveProofState(proof *AllocationProof) error {
	if proof == nil {
		return ErrInvalidProof
	}
	current, err := s.LoadProof(proof.OutPoint, proof.AssetName)
	if err != nil {
		return err
	}
	if current.OperationID != proof.OperationID || current.AssignmentType != proof.AssignmentType ||
		current.AssignmentIndex != proof.AssignmentIndex || current.ConsignmentHash != proof.ConsignmentHash ||
		current.ValidationHash != proof.ValidationHash || !bytes.Equal(current.SealDisclosure, proof.SealDisclosure) {
		return ErrInvalidProof
	}
	encoded, err := encode(proof)
	if err != nil {
		return err
	}
	key, err := s.proofKey(proof.OutPoint, proof.AssetName)
	if err != nil {
		return err
	}
	return s.db.Write(key, encoded)
}

// ReplaceProjections atomically switches consumed carriers to locally
// validated change allocations. Every replacement is locked and receipt-
// checked before the write batch can delete any old projection.
func (s *ProjectionStore) ReplaceProjections(consumedOutpoints []string, replacements []ProjectionReplacement) error {
	if s == nil || s.db == nil || s.locker == nil || len(consumedOutpoints) == 0 {
		return ErrProjectionMismatch
	}
	outputs := make(map[string]*indexer.TxOutput)
	proofs := make([]*AllocationProof, 0, len(replacements))
	for _, replacement := range replacements {
		if replacement.Output == nil || replacement.Asset == nil || replacement.Proof == nil ||
			replacement.Output.OutPointStr == "" || replacement.Output.OutPointStr != replacement.Proof.OutPoint ||
			replacement.Asset.Name != replacement.Proof.AssetName || replacement.Asset.Name.Protocol != Protocol ||
			(replacement.Proof.Status != "valid" && replacement.Proof.Status != "settled") {
			return ErrProjectionMismatch
		}
		receipt, err := s.LoadValidationReceipt(replacement.Proof.ConsignmentHash)
		if err != nil {
			return ErrInvalidProof
		}
		receiptHash, err := receipt.Hash()
		if err != nil || receiptHash != replacement.Proof.ValidationHash {
			return ErrInvalidProof
		}
		matched := false
		for _, allocation := range receipt.Allocations {
			if allocation.OutPoint == replacement.Proof.OutPoint && allocation.AssetName == replacement.Proof.AssetName &&
				allocation.OperationID == replacement.Proof.OperationID && allocation.AssignmentType == replacement.Proof.AssignmentType &&
				allocation.AssignmentIndex == replacement.Proof.AssignmentIndex && allocation.StateClass == replacement.Proof.StateClass &&
				bytes.Equal(allocation.StateData, replacement.Proof.StateData) &&
				bytes.Equal(allocation.SealDisclosure, replacement.Proof.SealDisclosure) &&
				allocation.Amount.Cmp(&replacement.Asset.Amount) == 0 {
				matched = true
				break
			}
		}
		if !matched {
			return ErrInvalidProof
		}
		if err := s.locker.LockUtxo(replacement.Output.OutPointStr, LockReasonRGB); err != nil {
			return err
		}
		projected := outputs[replacement.Output.OutPointStr]
		if projected == nil {
			projected = replacement.Output.Clone()
			projected.Assets = nil
			outputs[replacement.Output.OutPointStr] = projected
		}
		if projected.GetAsset(&replacement.Asset.Name) != nil {
			return fmt.Errorf("%w: duplicate replacement asset", ErrProjectionMismatch)
		}
		if err := projected.Assets.Add(replacement.Asset); err != nil {
			return err
		}
		proofs = append(proofs, replacement.Proof)
	}
	oldProofs, err := s.ListProofs()
	if err != nil {
		return err
	}
	consumed := make(map[string]bool, len(consumedOutpoints))
	for _, outpoint := range consumedOutpoints {
		consumed[outpoint] = true
	}
	batch := s.db.NewWriteBatch()
	if batch == nil {
		return errors.New("RGB11 KVDB returned nil write batch")
	}
	defer batch.Close()
	for outpoint := range consumed {
		key, err := s.outputKey(outpoint)
		if err != nil {
			return err
		}
		if err := batch.Delete(key); err != nil {
			return err
		}
	}
	for _, proof := range oldProofs {
		if !consumed[proof.OutPoint] {
			continue
		}
		key, err := s.proofKey(proof.OutPoint, proof.AssetName)
		if err != nil {
			return err
		}
		if err := batch.Delete(key); err != nil {
			return err
		}
	}
	for outpoint, output := range outputs {
		encoded, err := encode(output)
		if err != nil {
			return err
		}
		key, err := s.outputKey(outpoint)
		if err != nil {
			return err
		}
		if err := batch.Put(key, encoded); err != nil {
			return err
		}
	}
	for _, proof := range proofs {
		encoded, err := encode(proof)
		if err != nil {
			return err
		}
		key, err := s.proofKey(proof.OutPoint, proof.AssetName)
		if err != nil {
			return err
		}
		if err := batch.Put(key, encoded); err != nil {
			return err
		}
	}
	return batch.Flush()
}

func (s *ProjectionStore) ListOutputs() ([]*indexer.TxOutput, error) {
	outputs := make([]*indexer.TxOutput, 0)
	prefix, err := s.scopedPrefix("output-")
	if err != nil {
		return nil, err
	}
	err = s.db.BatchRead(prefix, false, func(_, value []byte) error {
		var output indexer.TxOutput
		if err := decode(value, &output); err != nil {
			return err
		}
		outputs = append(outputs, &output)
		return nil
	})
	return outputs, err
}

func (s *ProjectionStore) ListProofs() ([]*AllocationProof, error) {
	proofs := make([]*AllocationProof, 0)
	prefix, err := s.scopedPrefix("proof-")
	if err != nil {
		return nil, err
	}
	err = s.db.BatchRead(prefix, false, func(_, value []byte) error {
		var proof AllocationProof
		if err := decode(value, &proof); err != nil {
			return err
		}
		proofs = append(proofs, &proof)
		return nil
	})
	return proofs, err
}

func (s *ProjectionStore) Balance(name indexer.AssetName) (*indexer.Decimal, error) {
	if name.Protocol != Protocol {
		return nil, ErrInvalidRGB11Asset
	}
	outputs, err := s.ListOutputs()
	if err != nil {
		return nil, err
	}
	var total *indexer.Decimal
	for _, output := range outputs {
		amount := output.GetAsset(&name)
		if amount == nil {
			continue
		}
		if total == nil {
			total = amount.Clone()
		} else {
			total = total.AddAlignPrecision(amount)
		}
	}
	return total, nil
}

func (s *ProjectionStore) AssertConsistent(outpoint string, name indexer.AssetName) error {
	output, err := s.LoadOutput(outpoint)
	if err != nil {
		return err
	}
	proof, err := s.LoadProof(outpoint, name)
	if err != nil {
		return err
	}
	if proof.OutPoint != outpoint || proof.AssetName != name || output.GetAsset(&name) == nil {
		return fmt.Errorf("%w: %s", ErrProjectionMismatch, outpoint)
	}
	return nil
}
