package rgb11wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	corewallet "github.com/sat20-labs/rgb11/wallet"
)

type SnapshotRecord struct {
	Key   string `json:"key"`
	Value []byte `json:"value"`
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

	// Consignments are represented once by object-<hash> records in DKVS
	// snapshots. Runtime pending records are rehydrated on import. A recipient
	// consignment kept only for address mailbox redelivery is transient cache
	// and its object is excluded after delivery.
	deliveryObjects := make(map[string]struct{})
	canonicalObjects := make(map[string]struct{})
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
			if pending.State.AddressMode && pending.State.Status != "prepared" {
				deliveryObjects[pending.RecipientObjectHash] = struct{}{}
			}
		}
		if len(pending.LocalConsignment) > 0 {
			hash := sha256.Sum256(pending.LocalConsignment)
			pending.LocalObjectHash = hex.EncodeToString(hash[:])
			canonicalObjects[pending.LocalObjectHash] = struct{}{}
		}
		pending.RecipientConsignment = nil
		pending.LocalConsignment = nil
		if pending.State.Status != "prepared" {
			pending.SignedPSBT = nil
		}
		encoded, encodeErr := encode(&pending)
		if encodeErr != nil {
			return nil, encodeErr
		}
		records[index].Value = encoded
	}
	filtered := records[:0]
	for _, record := range records {
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
	records = filtered
	sort.Slice(records, func(i, j int) bool { return records[i].Key < records[j].Key })
	if err := s.ValidateSnapshot(records); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *ProjectionStore) ImportSnapshot(records []SnapshotRecord) error {
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
	existing := make([][]byte, 0)
	if err := s.db.BatchRead(prefix, false, func(key, _ []byte) error {
		existing = append(existing, append([]byte(nil), key...))
		return nil
	}); err != nil {
		return err
	}
	batch := s.db.NewWriteBatch()
	if batch == nil {
		return ErrValidationReceipt
	}
	defer batch.Close()
	for _, key := range existing {
		if err := batch.Delete(key); err != nil {
			return err
		}
	}
	for _, record := range records {
		key := append(append([]byte(nil), prefix...), []byte(record.Key)...)
		if err := batch.Put(key, append([]byte(nil), record.Value...)); err != nil {
			return err
		}
	}
	return batch.Flush()
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
	seen := make(map[string]bool)
	for _, record := range records {
		if record.Key == "" || len(record.Value) == 0 || seen[record.Key] {
			return ErrValidationReceipt
		}
		seen[record.Key] = true
		switch {
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
	for hash := range receipts {
		if objects[hash] == nil {
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
		if pending.State.Status == "prepared" && pending.RecipientObjectHash != "" &&
			objects[pending.RecipientObjectHash] == nil {
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

// TickerRefsFromProjectionSnapshot derives contract metadata references from
// validation receipts. The refs are never serialized into the wallet snapshot.
func TickerRefsFromProjectionSnapshot(records []SnapshotRecord) ([]SnapshotTickerRef, error) {
	refs := make(map[string]SnapshotTickerRef)
	for _, record := range records {
		if !strings.HasPrefix(record.Key, "validation-") {
			continue
		}
		hash := strings.TrimPrefix(record.Key, "validation-")
		var receipt ValidationReceipt
		if decode(record.Value, &receipt) != nil || receipt.validate(hash) != nil || len(receipt.Allocations) == 0 {
			return nil, ErrValidationReceipt
		}
		assetName := receipt.Allocations[0].AssetName.String()
		for _, allocation := range receipt.Allocations[1:] {
			if allocation.AssetName.String() != assetName {
				return nil, ErrValidationReceipt
			}
		}
		ref := SnapshotTickerRef{ContractID: receipt.ContractID, AssetName: assetName}
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
		matched := false
		for _, allocation := range receipt.Allocations {
			if allocation.AssetName.String() == ref.AssetName {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		raw := objects[receipt.ConsignmentHash]
		if len(raw) == 0 {
			return nil, nil, ErrValidationReceipt
		}
		actual := sha256.Sum256(raw)
		if receipt.ConsignmentHash != hex.EncodeToString(actual[:]) {
			return nil, nil, ErrValidationReceipt
		}
		return append([]byte(nil), raw...), &receipt, nil
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
	if err := s.ValidateSnapshot(records); err != nil {
		return err
	}
	prefix, err := s.snapshotPrefix()
	if err != nil {
		return err
	}
	existing := make([][]byte, 0)
	if err := s.db.BatchRead(prefix, false, func(key, _ []byte) error {
		existing = append(existing, append([]byte(nil), key...))
		return nil
	}); err != nil {
		return err
	}
	batch := s.db.NewWriteBatch()
	if batch == nil {
		return ErrWalletScope
	}
	defer batch.Close()
	for _, key := range existing {
		if err := batch.Delete(key); err != nil {
			return err
		}
	}
	for _, record := range records {
		key := append(append([]byte(nil), prefix...), []byte(record.Key)...)
		if err := batch.Put(key, append([]byte(nil), record.Value...)); err != nil {
			return err
		}
	}
	return batch.Flush()
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
			request.RelayKey == request.AckKey {
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
