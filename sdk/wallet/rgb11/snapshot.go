package rgb11wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type SnapshotRecord struct {
	Key   string `json:"key"`
	Value []byte `json:"value"`
}

// SnapshotProofUpdate changes only the chain-derived projection fields for an
// existing allocation proof. It is used to prepare a recovery/refresh snapshot
// before any of its records are published to the wallet database.
type SnapshotProofUpdate struct {
	OutPoint      string
	AssetName     indexer.AssetName
	Status        string
	Confirmations int64
}

// ProofsFromProjectionSnapshot decodes the proof records without consulting a
// live ProjectionStore. The returned proof values are independent copies.
func ProofsFromProjectionSnapshot(records []SnapshotRecord) ([]*AllocationProof, error) {
	proofs := make([]*AllocationProof, 0)
	for _, record := range records {
		if !strings.HasPrefix(record.Key, "proof-") {
			continue
		}
		proof := new(AllocationProof)
		if err := decode(record.Value, proof); err != nil || proof.OutPoint == "" ||
			proof.AssetName.Protocol != Protocol ||
			record.Key != "proof-"+proof.OutPoint+"-"+proof.AssetName.String() {
			return nil, ErrInvalidProof
		}
		proofs = append(proofs, proof)
	}
	return proofs, nil
}

// OutputsFromProjectionSnapshot decodes independently owned output records so
// recovery can compare cached carrier value/script and asset amounts with
// fresh validation evidence before publishing a candidate.
func OutputsFromProjectionSnapshot(records []SnapshotRecord) (map[string]*indexer.TxOutput, error) {
	outputs := make(map[string]*indexer.TxOutput)
	for _, record := range records {
		if !strings.HasPrefix(record.Key, "output-") {
			continue
		}
		outpoint := strings.TrimPrefix(record.Key, "output-")
		output := new(indexer.TxOutput)
		if err := decode(record.Value, output); err != nil || output.OutPointStr != outpoint || outpoint == "" {
			return nil, ErrProjectionMismatch
		}
		if _, exists := outputs[outpoint]; exists {
			return nil, ErrProjectionMismatch
		}
		outputs[outpoint] = output.Clone()
	}
	return outputs, nil
}

// ValidationReceiptsFromProjectionSnapshot decodes the receipt records that
// accompany a projection snapshot. Each receipt remains bound to its storage
// key and committed consignment hash.
func ValidationReceiptsFromProjectionSnapshot(records []SnapshotRecord) (map[string]*ValidationReceipt, error) {
	receipts := make(map[string]*ValidationReceipt)
	for _, record := range records {
		if !strings.HasPrefix(record.Key, "validation-") {
			continue
		}
		hash := strings.TrimPrefix(record.Key, "validation-")
		receipt := new(ValidationReceipt)
		if err := decode(record.Value, receipt); err != nil || receipt.validate(hash) != nil {
			return nil, ErrValidationReceipt
		}
		receipts[hash] = receipt
	}
	return receipts, nil
}

// TransfersFromProjectionSnapshot decodes the operation states from the same
// candidate snapshot used for proofs and objects. It intentionally avoids
// reading live projection/reservation stores while a cold import is being
// resolved.
func TransfersFromProjectionSnapshot(records []SnapshotRecord) ([]*TransferState, error) {
	type transferKey struct {
		direction  string
		transferID string
	}
	byID := make(map[transferKey]*TransferState)
	for _, record := range records {
		switch {
		case strings.HasPrefix(record.Key, "pending-"):
			var pending PendingTransfer
			if err := decode(record.Value, &pending); err != nil || pending.State.TransferID == "" ||
				record.Key != "pending-"+pending.State.TransferID {
				return nil, ErrRGB11Inconsistent
			}
			state := pending.State
			byID[transferKey{direction: state.Direction, transferID: state.TransferID}] = &state
		case strings.HasPrefix(record.Key, "transfer-"):
			var state TransferState
			if err := decode(record.Value, &state); err != nil || state.TransferID == "" ||
				record.Key != "transfer-"+state.TransferID {
				return nil, ErrRGB11Inconsistent
			}
			key := transferKey{direction: state.Direction, transferID: state.TransferID}
			if _, exists := byID[key]; !exists {
				copy := state
				byID[key] = &copy
			}
		}
	}
	states := make([]*TransferState, 0, len(byID))
	for _, state := range byID {
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool {
		if states[i].TransferID != states[j].TransferID {
			return states[i].TransferID < states[j].TransferID
		}
		return states[i].Direction < states[j].Direction
	})
	return states, nil
}

// PendingTransfersFromProjectionSnapshot returns the private operation records
// from one recovery candidate. It is used only to derive their existing
// reservation locks before the candidate is published.
func PendingTransfersFromProjectionSnapshot(records []SnapshotRecord) ([]*PendingTransfer, error) {
	result := make([]*PendingTransfer, 0)
	seen := make(map[string]bool)
	for _, record := range records {
		if !strings.HasPrefix(record.Key, "pending-") {
			continue
		}
		pending := new(PendingTransfer)
		if err := decode(record.Value, pending); err != nil || pending.State.TransferID == "" ||
			record.Key != "pending-"+pending.State.TransferID || seen[pending.State.TransferID] {
			return nil, ErrRGB11Inconsistent
		}
		seen[pending.State.TransferID] = true
		copy := *pending
		result = append(result, &copy)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].State.TransferID < result[j].State.TransferID
	})
	return result, nil
}

// ReceiveReservationsFromProjectionSnapshot decodes the receive-side lock
// records in the same candidate used to build the projection and owner plan.
func ReceiveReservationsFromProjectionSnapshot(records []SnapshotRecord) ([]*ReceiveReservation, error) {
	result := make([]*ReceiveReservation, 0)
	seen := make(map[string]bool)
	for _, record := range records {
		if !strings.HasPrefix(record.Key, "receive-reservation-") {
			continue
		}
		reservation := new(ReceiveReservation)
		if err := decode(record.Value, reservation); err != nil || reservation.RequestID == "" ||
			record.Key != "receive-reservation-"+reservation.RequestID || seen[reservation.RequestID] {
			return nil, ErrRGB11Inconsistent
		}
		seen[reservation.RequestID] = true
		copy := *reservation
		result = append(result, &copy)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RequestID < result[j].RequestID })
	return result, nil
}

// UpdateProjectionSnapshotProofs returns a validated copy of a projection
// snapshot with the requested proof statuses applied. Every update must match
// exactly one existing proof; callers cannot accidentally publish a partial
// candidate after a missing or duplicate proof identity.
func UpdateProjectionSnapshotProofs(records []SnapshotRecord, updates []SnapshotProofUpdate) ([]SnapshotRecord, error) {
	if len(updates) == 0 {
		result := make([]SnapshotRecord, len(records))
		for i, record := range records {
			result[i] = SnapshotRecord{Key: record.Key, Value: append([]byte(nil), record.Value...)}
		}
		return result, nil
	}
	byKey := make(map[string]SnapshotProofUpdate, len(updates))
	for _, update := range updates {
		if update.OutPoint == "" || update.AssetName.Protocol != Protocol ||
			(update.Status != "valid" && update.Status != "settled" && update.Status != "spending" &&
				update.Status != "inconsistent" && update.Status != "orphaned") {
			return nil, ErrInvalidProof
		}
		key := "proof-" + update.OutPoint + "-" + update.AssetName.String()
		if _, exists := byKey[key]; exists {
			return nil, ErrInvalidProof
		}
		byKey[key] = update
	}
	result := make([]SnapshotRecord, len(records))
	seen := make(map[string]bool, len(updates))
	for i, record := range records {
		result[i] = SnapshotRecord{Key: record.Key, Value: append([]byte(nil), record.Value...)}
		update, ok := byKey[record.Key]
		if !ok {
			continue
		}
		if seen[record.Key] {
			return nil, ErrInvalidProof
		}
		proof := new(AllocationProof)
		if err := decode(record.Value, proof); err != nil || proof.OutPoint != update.OutPoint ||
			proof.AssetName != update.AssetName {
			return nil, ErrInvalidProof
		}
		proof.Status = update.Status
		proof.Confirmations = update.Confirmations
		encoded, err := encode(proof)
		if err != nil {
			return nil, err
		}
		result[i].Value = encoded
		seen[record.Key] = true
	}
	if len(seen) != len(byKey) {
		return nil, ErrInvalidProof
	}
	if err := validateProjectionSnapshot(result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *ProjectionStore) snapshotPrefix() ([]byte, error) {
	s.mu.RLock()
	scope := s.scope
	s.mu.RUnlock()
	if scope == "" {
		return nil, ErrWalletScope
	}
	return []byte("rgb11-" + scope + "-"), nil
}

func (s *ProjectionStore) ExportSnapshot() ([]SnapshotRecord, error) {
	prefix, err := s.snapshotPrefix()
	if err != nil {
		return nil, err
	}
	records := make([]SnapshotRecord, 0)
	err = s.db.BatchRead(prefix, false, func(key, value []byte) error {
		if !bytes.HasPrefix(key, prefix) {
			return ErrValidationReceipt
		}
		records = append(records, SnapshotRecord{
			Key: string(append([]byte(nil), key[len(prefix):]...)), Value: append([]byte(nil), value...),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Consignments are represented once by immutable object-<hash> records in
	// snapshots. Runtime pending records are rehydrated on import. Keep the
	// recipient object even after delivery: it may be the only exact copy of the
	// consumer history or the body needed to retry after an uncertain ACK.
	for index := range records {
		if !strings.HasPrefix(records[index].Key, "pending-") {
			continue
		}
		var pending PendingTransfer
		if decode(records[index].Value, &pending) != nil {
			continue
		}
		if len(pending.RecipientConsignment) > 0 {
			hash := sha256.Sum256(pending.RecipientConsignment)
			pending.RecipientObjectHash = hex.EncodeToString(hash[:])
		}
		if len(pending.LocalConsignment) > 0 {
			hash := sha256.Sum256(pending.LocalConsignment)
			pending.LocalObjectHash = hex.EncodeToString(hash[:])
		}
		pending.RecipientConsignment = nil
		pending.LocalConsignment = nil
		encoded, encodeErr := encode(&pending)
		if encodeErr != nil {
			return nil, encodeErr
		}
		records[index].Value = encoded
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Key < records[j].Key })
	if err := s.ValidateSnapshot(records); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *ProjectionStore) ImportSnapshot(records []SnapshotRecord) error {
	if s == nil || s.reservations != nil {
		return ErrRGB11Inconsistent
	}
	batch := s.db.NewWriteBatch()
	if batch == nil {
		return ErrValidationReceipt
	}
	defer batch.Close()
	if err := s.StageSnapshotImport(batch, records); err != nil {
		return err
	}
	return batch.Flush()
}

// StageSnapshotImport validates and stages a scope replacement in the caller's
// shared wallet transaction. The caller owns Flush and Close.
func (s *ProjectionStore) StageSnapshotImport(batch indexer.WriteBatch, records []SnapshotRecord) error {
	if err := s.ValidateSnapshot(records); err != nil {
		return err
	}
	records, err := rehydratePendingSnapshotRecords(records)
	if err != nil {
		return err
	}
	prefix, err := s.snapshotPrefix()
	if err != nil {
		return err
	}
	return stageSnapshotRecords(s.db, batch, prefix, records)
}

func (s *ProjectionStore) ValidateSnapshot(records []SnapshotRecord) error {
	return validateProjectionSnapshot(records)
}

func validateProjectionSnapshot(records []SnapshotRecord) error {
	objects := make(map[string][]byte)
	receipts := make(map[string]*ValidationReceipt)
	outputs := make(map[string]*indexer.TxOutput)
	proofs := make([]*AllocationProof, 0)
	pendings := make([]*PendingTransfer, 0)
	contractObjects := make(map[string]string)
	seen := make(map[string]bool)
	namingContracts := make(map[string]bool)
	for _, record := range records {
		if record.Key == "" || len(record.Value) == 0 || seen[record.Key] {
			return ErrValidationReceipt
		}
		seen[record.Key] = true
		switch {
		case strings.HasPrefix(record.Key, "local-name-"):
			id := strings.TrimPrefix(record.Key, "local-name-")
			if raw, err := hex.DecodeString(id); err != nil || len(raw) != 32 || id != strings.ToLower(id) {
				return ErrValidationReceipt
			}
			if _, err := (LocalNameMetadata{}).Rename(string(record.Value)); err != nil {
				return ErrValidationReceipt
			}
			namingContracts[id] = true
		case strings.HasPrefix(record.Key, "registered-name-"):
			id := strings.TrimPrefix(record.Key, "registered-name-")
			signed, err := dkvsindexer.UnmarshalRecord(record.Value)
			if err != nil {
				return ErrValidationReceipt
			}
			if _, err := VerifyRGB11RegistrationForClient(signed, id, rgb11RegistryAuthority()); err != nil {
				return ErrValidationReceipt
			}
			namingContracts[id] = true
		case strings.HasPrefix(record.Key, "contract-object-"):
			contractHash := strings.TrimPrefix(record.Key, "contract-object-")
			objectHash := string(record.Value)
			if decoded, err := hex.DecodeString(contractHash); err != nil || len(decoded) != sha256.Size ||
				!validSnapshotObjectHash(objectHash) {
				return ErrValidationReceipt
			}
			contractObjects[contractHash] = objectHash
		case strings.HasPrefix(record.Key, "object-"):
			hash := strings.TrimPrefix(record.Key, "object-")
			actual := sha256.Sum256(record.Value)
			if decoded, err := hex.DecodeString(hash); err != nil || len(decoded) != sha256.Size || hash != hex.EncodeToString(actual[:]) {
				return ErrValidationReceipt
			}
			objects[hash] = record.Value
		case strings.HasPrefix(record.Key, "validation-"):
			hash := strings.TrimPrefix(record.Key, "validation-")
			var receipt ValidationReceipt
			if decode(record.Value, &receipt) != nil || receipt.validate(hash) != nil {
				return ErrValidationReceipt
			}
			receipts[hash] = &receipt
		case strings.HasPrefix(record.Key, "output-"):
			outpoint := strings.TrimPrefix(record.Key, "output-")
			var output indexer.TxOutput
			if decode(record.Value, &output) != nil || output.OutPointStr != outpoint {
				return ErrProjectionMismatch
			}
			outputs[outpoint] = &output
		case strings.HasPrefix(record.Key, "proof-"):
			var proof AllocationProof
			if decode(record.Value, &proof) != nil || proof.OutPoint == "" || proof.AssetName.Protocol != Protocol ||
				strings.TrimPrefix(record.Key, "proof-") != proof.OutPoint+"-"+proof.AssetName.String() {
				return ErrInvalidProof
			}
			proofs = append(proofs, &proof)
		case strings.HasPrefix(record.Key, "pending-"):
			var pending PendingTransfer
			if decode(record.Value, &pending) != nil || pending.State.TransferID == "" ||
				pending.State.TransferID != strings.TrimPrefix(record.Key, "pending-") {
				return ErrValidationReceipt
			}
			if !validSnapshotObjectHash(pending.RecipientObjectHash) ||
				!validSnapshotObjectHash(pending.LocalObjectHash) {
				return ErrValidationReceipt
			}
			pendings = append(pendings, &pending)
		case strings.HasPrefix(record.Key, "transfer-"):
			var state TransferState
			if decode(record.Value, &state) != nil || state.TransferID == "" || state.Direction == "" || state.Status == "" ||
				state.TransferID != strings.TrimPrefix(record.Key, "transfer-") {
				return ErrValidationReceipt
			}
		case strings.HasPrefix(record.Key, "prepared-receive-"):
			transferID := strings.TrimPrefix(record.Key, "prepared-receive-")
			requestID, err := hex.DecodeString(string(record.Value))
			if transferID == "" || err != nil || len(requestID) != sha256.Size {
				return ErrValidationReceipt
			}
		case strings.HasPrefix(record.Key, "receive-key-"):
			var key ReceiveKey
			hash := strings.TrimPrefix(record.Key, "receive-key-")
			if decode(record.Value, &key) != nil || key.RequestID == "" {
				return ErrValidationReceipt
			}
			actual := sha256.Sum256(key.WitnessScript)
			if hash != hex.EncodeToString(actual[:]) {
				return ErrValidationReceipt
			}
		case strings.HasPrefix(record.Key, "receive-reservation-"):
			var reservation ReceiveReservation
			requestID := strings.TrimPrefix(record.Key, "receive-reservation-")
			if decode(record.Value, &reservation) != nil || requestID == "" ||
				reservation.RequestID != requestID {
				return ErrValidationReceipt
			}
		default:
			return ErrValidationReceipt
		}
	}
	// Alias referential checks apply only to snapshots that contain aliases.
	// Ordinary ownership snapshots retain the existing receipt validation below.
	if len(namingContracts) != 0 {
		for _, receipt := range receipts {
			identity, err := NewContractAssetKey(receipt.ContractID, indexer.ASSET_TYPE_FT)
			if err != nil {
				return ErrValidationReceipt
			}
			delete(namingContracts, identity.Ticker)
		}
		if len(namingContracts) != 0 {
			return ErrValidationReceipt
		}
	}
	for hash := range receipts {
		if objects[hash] == nil {
			return ErrValidationReceipt
		}
	}
	for contractHash, objectHash := range contractObjects {
		raw := objects[objectHash]
		if raw == nil {
			return ErrValidationReceipt
		}
		container, err := coreconsignment.Decode(raw)
		if err != nil || container.Armor == nil || container.Armor.Type != "contract" {
			return ErrValidationReceipt
		}
		actual := sha256.Sum256([]byte(container.ContractID))
		if contractHash != hex.EncodeToString(actual[:]) {
			return ErrValidationReceipt
		}
	}
	for _, proof := range proofs {
		output := outputs[proof.OutPoint]
		receipt := receipts[proof.ConsignmentHash]
		if output == nil || receipt == nil || output.GetAsset(&proof.AssetName) == nil {
			return ErrProjectionMismatch
		}
		receiptHash, err := receipt.Hash()
		if err != nil || receiptHash != proof.ValidationHash {
			return ErrInvalidProof
		}
		projected := output.GetAsset(&proof.AssetName)
		matched := false
		for _, allocation := range receipt.Allocations {
			if allocation.OutPoint == proof.OutPoint && allocation.AssetName == proof.AssetName &&
				allocation.OperationID == proof.OperationID && allocation.AssignmentType == proof.AssignmentType &&
				allocation.AssignmentIndex == proof.AssignmentIndex && allocation.StateClass == proof.StateClass &&
				bytes.Equal(allocation.StateData, proof.StateData) && bytes.Equal(allocation.SealDisclosure, proof.SealDisclosure) &&
				allocation.Amount.Cmp(projected) == 0 {
				matched = true
				break
			}
		}
		if !matched {
			return ErrInvalidProof
		}
	}
	for _, pending := range pendings {
		if pending.LocalObjectHash != "" && objects[pending.LocalObjectHash] == nil {
			return ErrValidationReceipt
		}
		if pending.RecipientObjectHash != "" && objects[pending.RecipientObjectHash] == nil {
			return ErrValidationReceipt
		}
	}
	proofIndex := make(map[string]struct{}, len(proofs))
	for _, proof := range proofs {
		proofIndex[proof.OutPoint+"|"+proof.AssetName.String()] = struct{}{}
	}
	for outpoint, output := range outputs {
		for _, asset := range output.Assets {
			if asset.Name.Protocol != Protocol {
				continue
			}
			if _, ok := proofIndex[outpoint+"|"+asset.Name.String()]; !ok {
				return ErrProjectionMismatch
			}
		}
	}
	return nil
}

func validSnapshotObjectHash(value string) bool {
	if value == "" {
		return true
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func rehydratePendingSnapshotRecords(records []SnapshotRecord) ([]SnapshotRecord, error) {
	objects := make(map[string][]byte)
	for _, record := range records {
		if strings.HasPrefix(record.Key, "object-") {
			objects[strings.TrimPrefix(record.Key, "object-")] = record.Value
		}
	}
	result := make([]SnapshotRecord, len(records))
	for index, record := range records {
		result[index] = SnapshotRecord{Key: record.Key, Value: append([]byte(nil), record.Value...)}
		if !strings.HasPrefix(record.Key, "pending-") {
			continue
		}
		var pending PendingTransfer
		if decode(record.Value, &pending) != nil {
			return nil, ErrValidationReceipt
		}
		if pending.RecipientObjectHash != "" {
			pending.RecipientConsignment = append([]byte(nil), objects[pending.RecipientObjectHash]...)
		}
		if pending.LocalObjectHash != "" {
			pending.LocalConsignment = append([]byte(nil), objects[pending.LocalObjectHash]...)
		}
		encoded, err := encode(&pending)
		if err != nil {
			return nil, err
		}
		result[index].Value = encoded
	}
	return result, nil
}

type SnapshotTickerRef struct {
	ContractID string
	AssetName  string
}

// TickerRefsFromProjectionSnapshot derives one contract presentation identity
// from each receipt's committed genesis. Control allocations retain their own
// asset type in the proof ledger and are not merged into transferable amounts.
func TickerRefsFromProjectionSnapshot(records []SnapshotRecord) ([]SnapshotTickerRef, error) {
	objects := make(map[string][]byte)
	for _, record := range records {
		if strings.HasPrefix(record.Key, "object-") {
			objects[strings.TrimPrefix(record.Key, "object-")] = record.Value
		}
	}
	refs := make(map[string]SnapshotTickerRef)
	for _, record := range records {
		if !strings.HasPrefix(record.Key, "validation-") {
			continue
		}
		hash := strings.TrimPrefix(record.Key, "validation-")
		var receipt ValidationReceipt
		if decode(record.Value, &receipt) != nil || receipt.validate(hash) != nil {
			return nil, ErrValidationReceipt
		}
		ref, err := snapshotTickerRef(objects, &receipt)
		if err != nil {
			return nil, err
		}
		if existing, ok := refs[receipt.ContractID]; ok && existing != ref {
			return nil, ErrValidationReceipt
		}
		refs[receipt.ContractID] = ref
	}
	result := make([]SnapshotTickerRef, 0, len(refs))
	for _, ref := range refs {
		result = append(result, ref)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ContractID < result[j].ContractID })
	return result, nil
}

func ContractObjectForTickerRef(records []SnapshotRecord, ref SnapshotTickerRef) ([]byte, *ValidationReceipt, error) {
	objects := make(map[string][]byte)
	for _, record := range records {
		if strings.HasPrefix(record.Key, "object-") {
			objects[strings.TrimPrefix(record.Key, "object-")] = record.Value
		}
	}
	for _, record := range records {
		if !strings.HasPrefix(record.Key, "validation-") {
			continue
		}
		hash := strings.TrimPrefix(record.Key, "validation-")
		var receipt ValidationReceipt
		if decode(record.Value, &receipt) != nil || receipt.validate(hash) != nil {
			return nil, nil, ErrValidationReceipt
		}
		if receipt.ContractID != ref.ContractID {
			continue
		}
		derived, err := snapshotTickerRef(objects, &receipt)
		if err != nil {
			return nil, nil, err
		}
		if derived != ref {
			continue
		}
		return append([]byte(nil), objects[receipt.ConsignmentHash]...), &receipt, nil
	}
	return nil, nil, ErrValidationReceipt
}

func (s *EngineStore) snapshotPrefix() ([]byte, error) {
	s.mu.RLock()
	scope := s.scope
	s.mu.RUnlock()
	if scope == "" {
		return nil, ErrWalletScope
	}
	return []byte("rgb11-engine-" + scope + "-"), nil
}

func (s *EngineStore) ExportSnapshot() ([]SnapshotRecord, error) {
	prefix, err := s.snapshotPrefix()
	if err != nil {
		return nil, err
	}
	records := make([]SnapshotRecord, 0)
	err = s.db.BatchRead(prefix, false, func(key, value []byte) error {
		if !bytes.HasPrefix(key, prefix) {
			return ErrWalletScope
		}
		records = append(records, SnapshotRecord{
			Key: string(append([]byte(nil), key[len(prefix):]...)), Value: append([]byte(nil), value...),
		})
		return nil
	})
	sort.Slice(records, func(i, j int) bool { return records[i].Key < records[j].Key })
	if err != nil {
		return nil, err
	}
	if err := s.ValidateSnapshot(records); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *EngineStore) ImportSnapshot(records []SnapshotRecord) error {
	if s == nil || s.reservations != nil {
		return ErrRGB11Inconsistent
	}
	batch := s.db.NewWriteBatch()
	if batch == nil {
		return ErrWalletScope
	}
	defer batch.Close()
	if err := s.StageSnapshotImport(batch, records); err != nil {
		return err
	}
	return batch.Flush()
}

// StageSnapshotImport joins the engine records to a caller-owned transaction.
func (s *EngineStore) StageSnapshotImport(batch indexer.WriteBatch, records []SnapshotRecord) error {
	if err := s.ValidateSnapshot(records); err != nil {
		return err
	}
	prefix, err := s.snapshotPrefix()
	if err != nil {
		return err
	}
	return stageSnapshotRecords(s.db, batch, prefix, records)
}

// Stage only the byte differences. Keep reservation rebuilding in the original
// atomic batch even when the engine/projection record itself is unchanged.
func stageSnapshotRecords(database indexer.KVDB, batch indexer.WriteBatch, prefix []byte, records []SnapshotRecord) error {
	existing := make(map[string][]byte)
	if err := database.BatchRead(prefix, false, func(key, value []byte) error {
		existing[string(key)] = append([]byte(nil), value...)
		return nil
	}); err != nil {
		return err
	}
	for _, record := range records {
		key := append(append([]byte(nil), prefix...), []byte(record.Key)...)
		previous, found := existing[string(key)]
		delete(existing, string(key))
		if found && bytes.Equal(previous, record.Value) {
			if reservations, ok := batch.(*reservationBatch); ok {
				if err := reservations.stageReservation(key, record.Value); err != nil {
					return err
				}
			}
			continue
		}
		if err := batch.Put(key, append([]byte(nil), record.Value...)); err != nil {
			return err
		}
	}
	for key := range existing {
		if err := batch.Delete([]byte(key)); err != nil {
			return err
		}
	}
	return nil
}

func (s *EngineStore) ValidateSnapshot(records []SnapshotRecord) error {
	return validateEngineSnapshot(records)
}

func validateEngineSnapshot(records []SnapshotRecord) error {
	seen := make(map[string]bool)
	for _, record := range records {
		if !strings.HasPrefix(record.Key, "wallet/receive/") || len(record.Value) == 0 || seen[record.Key] {
			return corewallet.ErrInvalidReceive
		}
		seen[record.Key] = true
		request, err := corewallet.DecodeReceiveRequest(record.Value)
		if err != nil || request.Version != corewallet.ReceiveVersion ||
			request.RequestID == "" || record.Key != "wallet/receive/"+request.RequestID ||
			request.RelayKey != "" || request.AckKey != "" {
			return corewallet.ErrInvalidReceive
		}
	}
	return nil
}

// ValidateWalletSnapshot checks both stores and their cross-store references
// before any imported records replace the active wallet state.
func ValidateWalletSnapshot(snapshot *RGB11WalletSnapshot) error {
	if snapshot == nil || snapshot.Version != WalletSnapshotVersion || snapshot.WalletID == "" {
		return ErrRGB11Inconsistent
	}
	if err := validateProjectionSnapshot(snapshot.ProjectionRecords); err != nil {
		return err
	}
	if err := validateEngineSnapshot(snapshot.EngineRecords); err != nil {
		return err
	}
	receiveRequests := make(map[string]*corewallet.ReceiveRequest, len(snapshot.EngineRecords))
	for _, record := range snapshot.EngineRecords {
		request, err := corewallet.DecodeReceiveRequest(record.Value)
		if err != nil {
			return err
		}
		receiveRequests[request.RequestID] = request
	}
	for _, record := range snapshot.ProjectionRecords {
		switch {
		case strings.HasPrefix(record.Key, "prepared-receive-"):
			if receiveRequests[string(record.Value)] == nil {
				return ErrValidationReceipt
			}
		case strings.HasPrefix(record.Key, "receive-key-"):
			var key ReceiveKey
			if decode(record.Value, &key) != nil {
				return ErrValidationReceipt
			}
			request := receiveRequests[key.RequestID]
			if request == nil || !bytes.Equal(request.WitnessScript, key.WitnessScript) {
				return ErrValidationReceipt
			}
		case strings.HasPrefix(record.Key, "receive-reservation-"):
			var reservation ReceiveReservation
			if decode(record.Value, &reservation) != nil ||
				receiveRequests[reservation.RequestID] == nil {
				return ErrValidationReceipt
			}
		}
	}
	return nil
}
