package rgb11wallet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// HistoricalRepairTarget identifies one offline snapshot and one damaged record.
// This API has no DB handle and is never called by startup or chain refresh.
// The trusted offline adapter must verify network/root identity and current
// Bitcoin/consignment evidence in verify; these cannot be inferred from status.
type HistoricalRepairTarget struct {
	WalletID     string
	AccountIndex uint32
	SnapshotHash string
	TransferID   string
	WitnessTxID  string
}

type HistoricalRepairPlan struct {
	BeforeHash  string
	AfterHash   string
	TransferID  string
	ChangedKeys []string
}

func HistoricalRepairSnapshotHash(snapshot *RGB11WalletSnapshot) (string, error) {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// PlanHistoricalRepair is a pure, default dry-run transform. It returns an
// independent complete candidate snapshot; it never applies it to a database.
// Both lifecycle records are changed together, or no candidate is returned.
func PlanHistoricalRepair(snapshot *RGB11WalletSnapshot, target HistoricalRepairTarget,
	verify func(*PendingTransfer) error) (*HistoricalRepairPlan, *RGB11WalletSnapshot, error) {
	if snapshot == nil || verify == nil || target.TransferID == "" || target.WitnessTxID == "" ||
		target.WalletID == "" || snapshot.WalletID != target.WalletID || snapshot.AccountIndex != target.AccountIndex {
		return nil, nil, fmt.Errorf("historical repair scope or target mismatch")
	}
	before, err := HistoricalRepairSnapshotHash(snapshot)
	if err != nil || target.SnapshotHash == "" || before != target.SnapshotHash {
		return nil, nil, fmt.Errorf("historical repair fingerprint mismatch")
	}
	if err := ValidateWalletSnapshot(snapshot); err != nil {
		return nil, nil, err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, nil, err
	}
	var candidate RGB11WalletSnapshot
	if err := json.Unmarshal(raw, &candidate); err != nil {
		return nil, nil, err
	}
	pendingIndex, transferIndex := -1, -1
	for i, record := range candidate.ProjectionRecords {
		if record.Key == "pending-"+target.TransferID {
			pendingIndex = i
		}
		if record.Key == "transfer-"+target.TransferID {
			transferIndex = i
		}
	}
	if pendingIndex < 0 {
		return nil, nil, fmt.Errorf("historical repair pending record missing")
	}
	var pending PendingTransfer
	if err := decode(candidate.ProjectionRecords[pendingIndex].Value, &pending); err != nil {
		return nil, nil, err
	}
	if pending.State.TransferID != target.TransferID || pending.State.WitnessTxID != target.WitnessTxID ||
		pending.State.Direction != "send" || pending.State.Status != "pending" {
		return nil, nil, fmt.Errorf("historical repair expected exact pending sender")
	}
	// Snapshot pending payloads are externalized. Rehydrate a separate copy for
	// verification; preserve the original encoding/layout in the output candidate.
	hydrated, err := rehydratePendingSnapshotRecords(candidate.ProjectionRecords)
	if err != nil {
		return nil, nil, err
	}
	var verificationPending PendingTransfer
	for _, record := range hydrated {
		if record.Key == "pending-"+target.TransferID {
			if err := decode(record.Value, &verificationPending); err != nil {
				return nil, nil, err
			}
		}
	}
	if err := verify(&verificationPending); err != nil {
		return nil, nil, err
	}
	pending.State.Status = "settled"
	encoded, err := encode(&pending)
	if err != nil {
		return nil, nil, err
	}
	candidate.ProjectionRecords[pendingIndex].Value = encoded
	changed := []string{"pending-" + target.TransferID}
	// A missing redundant transfer record is not synthesized. If present, its
	// identity and old status must agree; never overwrite unrelated state.
	if transferIndex >= 0 {
		var state TransferState
		if err := decode(candidate.ProjectionRecords[transferIndex].Value, &state); err != nil {
			return nil, nil, err
		}
		if state.TransferID != target.TransferID || state.WitnessTxID != target.WitnessTxID || state.Direction != "send" || (state.Status != "pending" && state.Status != "settled") {
			return nil, nil, fmt.Errorf("historical repair paired state mismatch")
		}
		wasPending := state.Status == "pending"
		state.Status = "settled"
		encoded, err := encode(&state)
		if err != nil {
			return nil, nil, err
		}
		candidate.ProjectionRecords[transferIndex].Value = encoded
		if wasPending {
			changed = append(changed, "transfer-"+target.TransferID)
		}
	}
	if err := ValidateWalletSnapshot(&candidate); err != nil {
		return nil, nil, err
	}
	after, err := HistoricalRepairSnapshotHash(&candidate)
	if err != nil {
		return nil, nil, err
	}
	return &HistoricalRepairPlan{BeforeHash: before, AfterHash: after, TransferID: target.TransferID, ChangedKeys: changed}, &candidate, nil
}
