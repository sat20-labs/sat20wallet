package rgb11wallet

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ActiveRecoveryPackageVersion intentionally remains independent from the
// durable RecoveryPackage version. Neither format has been publicly released,
// but keeping the two domains explicit prevents active transaction state from
// accidentally becoming durable ownership state.
const ActiveRecoveryPackageVersion = uint32(1)

// ActiveRecoveryPackageMaxSize is below the MessageManager Direct payload
// limit. The account-message envelope and encryption overhead still have room;
// an oversized transition is rejected before an irreversible broadcast/ACK.
const ActiveRecoveryPackageMaxSize = 700 * 1024

type ActiveRecoveryPackage struct {
	Version           uint32           `json:"version"`
	WalletID          string           `json:"wallet_id"`
	AccountIndex      uint32           `json:"account_index"`
	EngineBuildID     string           `json:"engine_build_id"`
	ProjectionRecords []SnapshotRecord `json:"projection_records"`
	EngineRecords     []SnapshotRecord `json:"engine_records"`
}

func activeRGB11TransferStatus(status string) bool {
	switch status {
	case "prepared", "delivered", "relayed", "broadcast-attempted", "broadcast",
		"pending", "awaiting_broadcast":
		return true
	default:
		return false
	}
}

func ActiveRecoveryPackageHasTransition(value *ActiveRecoveryPackage) bool {
	if value == nil {
		return false
	}
	for _, record := range value.ProjectionRecords {
		if strings.HasPrefix(record.Key, "pending-") ||
			strings.HasPrefix(record.Key, "transfer-") ||
			strings.HasPrefix(record.Key, "proof-") {
			return true
		}
	}
	return false
}

func cloneSnapshotRecord(record SnapshotRecord) SnapshotRecord {
	return SnapshotRecord{Key: record.Key, Value: append([]byte(nil), record.Value...)}
}

// ActiveRecoveryPackageFromSnapshot selects only state needed to safely resume
// unfinished RGB operations. Stable settled ownership is deliberately absent
// unless it is a dependency of an active input/output.
func ActiveRecoveryPackageFromSnapshot(snapshot *RGB11WalletSnapshot) (*ActiveRecoveryPackage, error) {
	if err := ValidateWalletSnapshot(snapshot); err != nil {
		return nil, err
	}
	records := make(map[string]SnapshotRecord, len(snapshot.ProjectionRecords))
	proofsByOutpoint := make(map[string][]AllocationProof)
	activeOutpoints := make(map[string]struct{})
	activeHashes := make(map[string]struct{})
	keep := make(map[string]struct{})

	for _, record := range snapshot.ProjectionRecords {
		records[record.Key] = cloneSnapshotRecord(record)
		switch {
		case strings.HasPrefix(record.Key, "pending-"):
			var pending PendingTransfer
			if decode(record.Value, &pending) != nil {
				return nil, ErrValidationReceipt
			}
			if !activeRGB11TransferStatus(pending.State.Status) {
				continue
			}
			keep[record.Key] = struct{}{}
			for _, outpoint := range append(append([]string(nil), pending.State.InputOutPoints...), pending.State.OutputOutPoints...) {
				activeOutpoints[outpoint] = struct{}{}
			}
			for _, hash := range []string{pending.State.ConsignmentHash, pending.RecipientObjectHash, pending.LocalObjectHash} {
				if hash != "" {
					activeHashes[hash] = struct{}{}
				}
			}
		case strings.HasPrefix(record.Key, "transfer-"):
			var state TransferState
			if decode(record.Value, &state) != nil {
				return nil, ErrValidationReceipt
			}
			if !activeRGB11TransferStatus(state.Status) {
				continue
			}
			keep[record.Key] = struct{}{}
			for _, outpoint := range append(append([]string(nil), state.InputOutPoints...), state.OutputOutPoints...) {
				activeOutpoints[outpoint] = struct{}{}
			}
			if state.ConsignmentHash != "" {
				activeHashes[state.ConsignmentHash] = struct{}{}
			}
		case strings.HasPrefix(record.Key, "proof-"):
			var proof AllocationProof
			if decode(record.Value, &proof) != nil {
				return nil, ErrInvalidProof
			}
			proofsByOutpoint[proof.OutPoint] = append(proofsByOutpoint[proof.OutPoint], proof)
			if proof.Status == "valid" {
				activeOutpoints[proof.OutPoint] = struct{}{}
			}
		case strings.HasPrefix(record.Key, "prepared-receive-"),
			strings.HasPrefix(record.Key, "receive-key-"),
			strings.HasPrefix(record.Key, "receive-reservation-"):
			keep[record.Key] = struct{}{}
		}
	}

	for outpoint := range activeOutpoints {
		keep["output-"+outpoint] = struct{}{}
		for _, proof := range proofsByOutpoint[outpoint] {
			keep["proof-"+proof.OutPoint+"-"+proof.AssetName.String()] = struct{}{}
			keep["validation-"+proof.ConsignmentHash] = struct{}{}
			keep["object-"+proof.ConsignmentHash] = struct{}{}
		}
	}
	for hash := range activeHashes {
		keep["object-"+hash] = struct{}{}
	}

	projection := make([]SnapshotRecord, 0, len(keep))
	for key := range keep {
		if record, ok := records[key]; ok {
			projection = append(projection, cloneSnapshotRecord(record))
		}
	}
	sort.Slice(projection, func(i, j int) bool { return projection[i].Key < projection[j].Key })
	engine := cloneRecoveryRecords(snapshot.EngineRecords)
	if len(projection) == 0 && len(engine) == 0 {
		return &ActiveRecoveryPackage{
			Version: ActiveRecoveryPackageVersion, WalletID: snapshot.WalletID,
			AccountIndex: snapshot.AccountIndex, EngineBuildID: snapshot.EngineBuildID,
		}, nil
	}
	value := &ActiveRecoveryPackage{
		Version: ActiveRecoveryPackageVersion, WalletID: snapshot.WalletID,
		AccountIndex: snapshot.AccountIndex, EngineBuildID: snapshot.EngineBuildID,
		ProjectionRecords: projection, EngineRecords: engine,
	}
	if err := ValidateActiveRecoveryPackage(value); err != nil {
		return nil, err
	}
	return value, nil
}

func ValidateActiveRecoveryPackage(value *ActiveRecoveryPackage) error {
	if value == nil || value.Version != ActiveRecoveryPackageVersion || value.WalletID == "" ||
		value.EngineBuildID != NativeEngineBuildID {
		return ErrRGB11Inconsistent
	}
	snapshot := &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: value.WalletID,
		AccountIndex: value.AccountIndex, EngineBuildID: value.EngineBuildID,
		ProjectionRecords: cloneRecoveryRecords(value.ProjectionRecords),
		EngineRecords:     cloneRecoveryRecords(value.EngineRecords),
	}
	if len(snapshot.ProjectionRecords) != 0 || len(snapshot.EngineRecords) != 0 {
		if err := ValidateWalletSnapshot(snapshot); err != nil {
			return err
		}
	}
	return nil
}

func EncodeActiveRecoveryPackage(value *ActiveRecoveryPackage) ([]byte, error) {
	if err := ValidateActiveRecoveryPackage(value); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(encoded) == 0 || len(encoded) > ActiveRecoveryPackageMaxSize {
		return nil, fmt.Errorf("%w: active recovery package size %d exceeds %d",
			ErrRGB11Inconsistent, len(encoded), ActiveRecoveryPackageMaxSize)
	}
	return encoded, nil
}

func DecodeActiveRecoveryPackage(encoded []byte) (*ActiveRecoveryPackage, error) {
	if len(encoded) == 0 || len(encoded) > ActiveRecoveryPackageMaxSize {
		return nil, ErrRGB11Inconsistent
	}
	var value ActiveRecoveryPackage
	if err := json.Unmarshal(encoded, &value); err != nil {
		return nil, err
	}
	if err := ValidateActiveRecoveryPackage(&value); err != nil {
		return nil, err
	}
	return &value, nil
}

func (p *ActiveRecoveryPackage) WalletSnapshot() (*RGB11WalletSnapshot, error) {
	if err := ValidateActiveRecoveryPackage(p); err != nil {
		return nil, err
	}
	return &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: p.WalletID,
		AccountIndex: p.AccountIndex, EngineBuildID: p.EngineBuildID,
		ProjectionRecords: cloneRecoveryRecords(p.ProjectionRecords),
		EngineRecords:     cloneRecoveryRecords(p.EngineRecords),
	}, nil
}

// MergeActiveRecoverySnapshot overlays the latest validated transition state
// on the stable/local snapshot without deleting unrelated local history.
func MergeActiveRecoverySnapshot(current, active *RGB11WalletSnapshot) (*RGB11WalletSnapshot, error) {
	if current == nil || active == nil || current.WalletID != active.WalletID ||
		current.AccountIndex != active.AccountIndex || current.EngineBuildID != active.EngineBuildID {
		return nil, ErrRGB11Inconsistent
	}
	if err := ValidateWalletSnapshot(current); err != nil {
		return nil, err
	}
	if len(active.ProjectionRecords) != 0 || len(active.EngineRecords) != 0 {
		if err := ValidateWalletSnapshot(active); err != nil {
			return nil, err
		}
	}
	projection := make(map[string]SnapshotRecord, len(current.ProjectionRecords)+len(active.ProjectionRecords))
	for _, record := range current.ProjectionRecords {
		projection[record.Key] = cloneSnapshotRecord(record)
	}
	for _, record := range active.ProjectionRecords {
		projection[record.Key] = cloneSnapshotRecord(record)
	}
	engine := make(map[string]SnapshotRecord, len(current.EngineRecords)+len(active.EngineRecords))
	for _, record := range current.EngineRecords {
		engine[record.Key] = cloneSnapshotRecord(record)
	}
	for _, record := range active.EngineRecords {
		engine[record.Key] = cloneSnapshotRecord(record)
	}
	merged := &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: current.WalletID,
		AccountIndex: current.AccountIndex, EngineBuildID: current.EngineBuildID,
	}
	for _, record := range projection {
		merged.ProjectionRecords = append(merged.ProjectionRecords, record)
	}
	for _, record := range engine {
		merged.EngineRecords = append(merged.EngineRecords, record)
	}
	sort.Slice(merged.ProjectionRecords, func(i, j int) bool { return merged.ProjectionRecords[i].Key < merged.ProjectionRecords[j].Key })
	sort.Slice(merged.EngineRecords, func(i, j int) bool { return merged.EngineRecords[i].Key < merged.EngineRecords[j].Key })
	if err := ValidateWalletSnapshot(merged); err != nil {
		return nil, err
	}
	return merged, nil
}
