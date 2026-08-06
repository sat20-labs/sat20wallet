package rgb11wallet

import (
	"fmt"
	"sort"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	corewallet "github.com/sat20-labs/rgb11/wallet"
)

// RecoveryPackageVersion identifies the minimum cross-device state set. It is
// independent from the complete local snapshot used for diagnostics.
const RecoveryPackageVersion = uint32(1)

type RecoveryPackage struct {
	Version           uint32
	WalletID          string
	AccountIndex      uint32
	EngineBuildID     string
	ProjectionRecords []SnapshotRecord
	EngineRecords     []SnapshotRecord
}

func recoverableTransferStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "settled", "rejected", "conflicted":
		return false
	default:
		return true
	}
}

func recoverableReceive(request *corewallet.ReceiveRequest, now int64) bool {
	if request == nil {
		return false
	}
	switch request.Status {
	case corewallet.ReceiveSettled, corewallet.ReceiveFailed:
		return false
	}
	return request.Expiry == 0 || request.Expiry > now
}

func activeReceiveForRecovery(request *corewallet.ReceiveRequest) bool {
	if request == nil {
		return false
	}
	return request.Status != corewallet.ReceiveSettled && request.Status != corewallet.ReceiveFailed
}

func minimalRecoveryOutput(record SnapshotRecord, assets map[indexer.AssetName]struct{}) (SnapshotRecord, error) {
	var output indexer.TxOutput
	if decode(record.Value, &output) != nil || output.OutPointStr == "" ||
		output.OutValue.Value < 0 || len(output.OutValue.PkScript) == 0 {
		return SnapshotRecord{}, ErrProjectionMismatch
	}
	minimal := indexer.NewTxOutput(output.OutValue.Value)
	minimal.OutPointStr = output.OutPointStr
	minimal.OutValue.PkScript = append([]byte(nil), output.OutValue.PkScript...)
	for name := range assets {
		asset, err := output.Assets.Find(&name)
		if err != nil || asset == nil || asset.Name.Protocol != Protocol {
			return SnapshotRecord{}, ErrProjectionMismatch
		}
		if err := (&minimal.Assets).Add(asset.Clone()); err != nil {
			return SnapshotRecord{}, err
		}
	}
	encoded, err := encode(minimal)
	if err != nil {
		return SnapshotRecord{}, err
	}
	return SnapshotRecord{Key: record.Key, Value: encoded}, nil
}

// RecoveryPackageFromSnapshot keeps only data whose loss may prevent proving
// or safely completing ownership. Derived balances, completed history and
// presentation/index caches are intentionally excluded.
func RecoveryPackageFromSnapshot(snapshot *RGB11WalletSnapshot, now int64) (*RecoveryPackage, error) {
	if now == 0 {
		now = time.Now().Unix()
	}
	if err := ValidateWalletSnapshot(snapshot); err != nil {
		return nil, err
	}
	projection := make(map[string]SnapshotRecord, len(snapshot.ProjectionRecords))
	for _, record := range snapshot.ProjectionRecords {
		projection[record.Key] = SnapshotRecord{Key: record.Key, Value: append([]byte(nil), record.Value...)}
	}
	keep := make(map[string]struct{})
	keepObject := func(hash string) {
		if hash != "" {
			keep["object-"+hash] = struct{}{}
		}
	}
	activeRequests := make(map[string]struct{})
	requiredOutputAssets := make(map[string]map[indexer.AssetName]struct{})

	// Current allocation proofs and their canonical validation chains are
	// permanent recovery data. Output projections are reduced to the chain
	// carrier and the exact live RGB assets required by those proofs.
	for _, record := range snapshot.ProjectionRecords {
		if !strings.HasPrefix(record.Key, "proof-") {
			continue
		}
		var proof AllocationProof
		if decode(record.Value, &proof) != nil {
			return nil, ErrInvalidProof
		}
		keep[record.Key] = struct{}{}
		keep["output-"+proof.OutPoint] = struct{}{}
		keep["validation-"+proof.ConsignmentHash] = struct{}{}
		keepObject(proof.ConsignmentHash)
		if requiredOutputAssets[proof.OutPoint] == nil {
			requiredOutputAssets[proof.OutPoint] = make(map[indexer.AssetName]struct{})
		}
		requiredOutputAssets[proof.OutPoint][proof.AssetName] = struct{}{}
	}
	for outpoint, assets := range requiredOutputAssets {
		key := "output-" + outpoint
		record, ok := projection[key]
		if !ok {
			return nil, ErrProjectionMismatch
		}
		minimal, err := minimalRecoveryOutput(record, assets)
		if err != nil {
			return nil, err
		}
		projection[key] = minimal
	}

	// Only operations that may still require delivery, cancellation, broadcast,
	// confirmation or reorg handling are retained.
	for _, record := range snapshot.ProjectionRecords {
		if !strings.HasPrefix(record.Key, "pending-") {
			continue
		}
		var pending PendingTransfer
		if decode(record.Value, &pending) != nil {
			return nil, ErrValidationReceipt
		}
		if !recoverableTransferStatus(pending.State.Status) {
			continue
		}
		keep[record.Key] = struct{}{}
		keepObject(pending.LocalObjectHash)
		if pending.State.Status == "prepared" {
			keepObject(pending.RecipientObjectHash)
		}
	}
	for _, record := range snapshot.ProjectionRecords {
		if !strings.HasPrefix(record.Key, "transfer-") {
			continue
		}
		var state TransferState
		if decode(record.Value, &state) != nil {
			return nil, ErrValidationReceipt
		}
		if recoverableTransferStatus(state.Status) {
			keep[record.Key] = struct{}{}
			if state.ConsignmentHash != "" {
				keep["object-"+state.ConsignmentHash] = struct{}{}
				if _, ok := projection["validation-"+state.ConsignmentHash]; ok {
					keep["validation-"+state.ConsignmentHash] = struct{}{}
				}
			}
		}
	}

	engineRecords := make([]SnapshotRecord, 0, len(snapshot.EngineRecords))
	for _, record := range snapshot.EngineRecords {
		request, err := corewallet.DecodeReceiveRequest(record.Value)
		if err != nil {
			return nil, err
		}
		if !recoverableReceive(request, now) {
			continue
		}
		activeRequests[request.RequestID] = struct{}{}
		engineRecords = append(engineRecords, SnapshotRecord{
			Key: record.Key, Value: append([]byte(nil), record.Value...),
		})
	}
	for _, record := range snapshot.ProjectionRecords {
		switch {
		case strings.HasPrefix(record.Key, "prepared-receive-"):
			if _, ok := activeRequests[string(record.Value)]; ok {
				keep[record.Key] = struct{}{}
			}
		case strings.HasPrefix(record.Key, "receive-key-"):
			var key ReceiveKey
			if decode(record.Value, &key) != nil {
				return nil, ErrValidationReceipt
			}
			if _, ok := activeRequests[key.RequestID]; ok {
				keep[record.Key] = struct{}{}
			}
		case strings.HasPrefix(record.Key, "receive-reservation-"):
			var reservation ReceiveReservation
			if decode(record.Value, &reservation) != nil {
				return nil, ErrValidationReceipt
			}
			if _, ok := activeRequests[reservation.RequestID]; ok {
				keep[record.Key] = struct{}{}
			}
		}
	}

	projectionRecords := make([]SnapshotRecord, 0, len(keep))
	for key := range keep {
		record, ok := projection[key]
		if !ok {
			return nil, ErrValidationReceipt
		}
		projectionRecords = append(projectionRecords, record)
	}
	sort.Slice(projectionRecords, func(i, j int) bool { return projectionRecords[i].Key < projectionRecords[j].Key })
	sort.Slice(engineRecords, func(i, j int) bool { return engineRecords[i].Key < engineRecords[j].Key })
	packageValue := &RecoveryPackage{
		Version: RecoveryPackageVersion, WalletID: snapshot.WalletID,
		AccountIndex: snapshot.AccountIndex, EngineBuildID: snapshot.EngineBuildID,
		ProjectionRecords: projectionRecords, EngineRecords: engineRecords,
	}
	if err := ValidateRecoveryPackage(packageValue); err != nil {
		return nil, err
	}
	return packageValue, nil
}

func cloneRecoveryRecords(records []SnapshotRecord) []SnapshotRecord {
	result := make([]SnapshotRecord, len(records))
	for index, record := range records {
		result[index] = SnapshotRecord{Key: record.Key, Value: append([]byte(nil), record.Value...)}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}

func normalizeRecoveryPackage(value *RecoveryPackage) (*RecoveryPackage, error) {
	if value == nil {
		return nil, fmt.Errorf("%w: recovery package is nil", ErrRGB11Inconsistent)
	}
	if value.Version != RecoveryPackageVersion || value.WalletID == "" ||
		value.EngineBuildID != NativeEngineBuildID {
		return nil, fmt.Errorf("%w: header version=%d wallet=%q engine=%q want_engine=%q",
			ErrRGB11Inconsistent, value.Version, value.WalletID, value.EngineBuildID, NativeEngineBuildID)
	}
	result := &RecoveryPackage{
		Version: RecoveryPackageVersion, WalletID: value.WalletID,
		AccountIndex: value.AccountIndex, EngineBuildID: value.EngineBuildID,
		ProjectionRecords: cloneRecoveryRecords(value.ProjectionRecords),
		EngineRecords:     cloneRecoveryRecords(value.EngineRecords),
	}
	return result, nil
}

func ValidateRecoveryPackage(value *RecoveryPackage) error {
	packageValue, err := normalizeRecoveryPackage(value)
	if err != nil {
		return err
	}
	snapshot := &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: packageValue.WalletID,
		AccountIndex: packageValue.AccountIndex, EngineBuildID: packageValue.EngineBuildID,
		ProjectionRecords: packageValue.ProjectionRecords, EngineRecords: packageValue.EngineRecords,
	}
	if len(snapshot.ProjectionRecords) != 0 || len(snapshot.EngineRecords) != 0 {
		if err := ValidateWalletSnapshot(snapshot); err != nil {
			return err
		}
	}

	projection := make(map[string]SnapshotRecord, len(packageValue.ProjectionRecords))
	for _, record := range packageValue.ProjectionRecords {
		if _, exists := projection[record.Key]; exists {
			return fmt.Errorf("%w: duplicate projection key %s", ErrRGB11Inconsistent, record.Key)
		}
		projection[record.Key] = record
	}
	expected := make(map[string]struct{})
	activeRequests := make(map[string]struct{})
	proofAssets := make(map[string]map[indexer.AssetName]struct{})

	for _, record := range packageValue.EngineRecords {
		request, err := corewallet.DecodeReceiveRequest(record.Value)
		if err != nil || !activeReceiveForRecovery(request) {
			return ErrRGB11Inconsistent
		}
		activeRequests[request.RequestID] = struct{}{}
	}
	for _, record := range packageValue.ProjectionRecords {
		switch {
		case strings.HasPrefix(record.Key, "proof-"):
			var proof AllocationProof
			if decode(record.Value, &proof) != nil {
				return ErrInvalidProof
			}
			expected[record.Key] = struct{}{}
			expected["output-"+proof.OutPoint] = struct{}{}
			expected["validation-"+proof.ConsignmentHash] = struct{}{}
			expected["object-"+proof.ConsignmentHash] = struct{}{}
			if proofAssets[proof.OutPoint] == nil {
				proofAssets[proof.OutPoint] = make(map[indexer.AssetName]struct{})
			}
			proofAssets[proof.OutPoint][proof.AssetName] = struct{}{}
		case strings.HasPrefix(record.Key, "pending-"):
			var pending PendingTransfer
			if decode(record.Value, &pending) != nil || !recoverableTransferStatus(pending.State.Status) {
				return ErrRGB11Inconsistent
			}
			expected[record.Key] = struct{}{}
			if pending.LocalObjectHash != "" {
				expected["object-"+pending.LocalObjectHash] = struct{}{}
			}
			if pending.State.Status == "prepared" && pending.RecipientObjectHash != "" {
				expected["object-"+pending.RecipientObjectHash] = struct{}{}
			}
		case strings.HasPrefix(record.Key, "transfer-"):
			var state TransferState
			if decode(record.Value, &state) != nil || !recoverableTransferStatus(state.Status) {
				return ErrRGB11Inconsistent
			}
			expected[record.Key] = struct{}{}
			if state.ConsignmentHash != "" {
				expected["object-"+state.ConsignmentHash] = struct{}{}
				if _, ok := projection["validation-"+state.ConsignmentHash]; ok {
					expected["validation-"+state.ConsignmentHash] = struct{}{}
				}
			}
		case strings.HasPrefix(record.Key, "prepared-receive-"):
			if _, ok := activeRequests[string(record.Value)]; !ok {
				return ErrRGB11Inconsistent
			}
			expected[record.Key] = struct{}{}
		case strings.HasPrefix(record.Key, "receive-key-"):
			var key ReceiveKey
			if decode(record.Value, &key) != nil {
				return ErrRGB11Inconsistent
			}
			if _, ok := activeRequests[key.RequestID]; !ok {
				return ErrRGB11Inconsistent
			}
			expected[record.Key] = struct{}{}
		case strings.HasPrefix(record.Key, "receive-reservation-"):
			var reservation ReceiveReservation
			if decode(record.Value, &reservation) != nil {
				return ErrRGB11Inconsistent
			}
			if _, ok := activeRequests[reservation.RequestID]; !ok {
				return ErrRGB11Inconsistent
			}
			expected[record.Key] = struct{}{}
		}
	}
	for key := range projection {
		if _, ok := expected[key]; !ok {
			return fmt.Errorf("%w: unexpected recovery projection %s", ErrRGB11Inconsistent, key)
		}
	}
	for key := range expected {
		if _, ok := projection[key]; !ok {
			return fmt.Errorf("%w: missing recovery projection %s", ErrRGB11Inconsistent, key)
		}
	}
	for outpoint, assets := range proofAssets {
		var output indexer.TxOutput
		if decode(projection["output-"+outpoint].Value, &output) != nil || output.UtxoId != indexer.INVALID_ID ||
			len(output.Offsets) != 0 || len(output.SatBindingMap) != 0 || len(output.Invalids) != 0 ||
			len(output.Assets) != len(assets) {
			return fmt.Errorf("%w: non-minimal output %s utxo=%d offsets=%d bindings=%d invalids=%d assets=%d want_assets=%d",
				ErrRGB11Inconsistent, outpoint, output.UtxoId, len(output.Offsets), len(output.SatBindingMap),
				len(output.Invalids), len(output.Assets), len(assets))
		}
		for _, asset := range output.Assets {
			if asset.Name.Protocol != Protocol {
				return ErrRGB11Inconsistent
			}
			if _, ok := assets[asset.Name]; !ok {
				return ErrRGB11Inconsistent
			}
		}
	}
	return nil
}

func (p *RecoveryPackage) WalletSnapshot() (*RGB11WalletSnapshot, error) {
	packageValue, err := normalizeRecoveryPackage(p)
	if err != nil {
		return nil, err
	}
	if err := ValidateRecoveryPackage(packageValue); err != nil {
		return nil, err
	}
	return &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: packageValue.WalletID,
		AccountIndex: packageValue.AccountIndex, EngineBuildID: packageValue.EngineBuildID,
		ProjectionRecords: packageValue.ProjectionRecords, EngineRecords: packageValue.EngineRecords,
	}, nil
}
