package rgb11wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	sort.Slice(records, func(i, j int) bool { return records[i].Key < records[j].Key })
	return records, err
}

func (s *ProjectionStore) ImportSnapshot(records []SnapshotRecord) error {
	if err := validateProjectionSnapshot(records); err != nil {
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

func validateProjectionSnapshot(records []SnapshotRecord) error {
	objects := make(map[string][]byte)
	receipts := make(map[string]*ValidationReceipt)
	outputs := make(map[string]*indexer.TxOutput)
	proofs := make([]*AllocationProof, 0)
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
		case strings.HasPrefix(record.Key, "transfer-"):
			var state TransferState
			if decode(record.Value, &state) != nil || state.TransferID == "" || state.Direction == "" || state.Status == "" ||
				state.TransferID != strings.TrimPrefix(record.Key, "transfer-") {
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
	return nil
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
	return records, err
}

func (s *EngineStore) ImportSnapshot(records []SnapshotRecord) error {
	seen := make(map[string]bool)
	for _, record := range records {
		if !strings.HasPrefix(record.Key, "wallet/receive/") || len(record.Value) == 0 || seen[record.Key] {
			return corewallet.ErrInvalidReceive
		}
		seen[record.Key] = true
		var request corewallet.ReceiveRequest
		if json.Unmarshal(record.Value, &request) != nil || request.Version != corewallet.ReceiveVersion ||
			request.RequestID == "" || record.Key != "wallet/receive/"+request.RequestID ||
			request.RelayKey == request.AckKey {
			return corewallet.ErrInvalidReceive
		}
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
