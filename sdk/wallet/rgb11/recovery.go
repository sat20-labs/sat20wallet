package rgb11wallet

import (
	"fmt"
	"sort"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
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
	_ = now // Kept in the API for callers of the version-1 package format.
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
	requiredOutputAssets := make(map[string]map[indexer.AssetName]struct{})

	// Only settled current allocation proofs and their canonical validation
	// chains are permanent recovery data. A valid-but-unconfirmed proof and a
	// spending proof belong to the active transition package instead: publishing
	// either here would replace the last known-stable ownership snapshot before
	// the Bitcoin transition is final.
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
		if proof.Status != "settled" || proof.Confirmations <= 0 {
			continue
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

	// Account-managed recovery contains only durable ownership evidence.
	// Pending sends, receive requests, signed transactions, reservations and
	// transport state remain in the wallet-local transaction database.
	projectionRecords := make([]SnapshotRecord, 0, len(keep))
	for key := range keep {
		record, ok := projection[key]
		if !ok {
			return nil, ErrValidationReceipt
		}
		projectionRecords = append(projectionRecords, record)
	}
	sort.Slice(projectionRecords, func(i, j int) bool { return projectionRecords[i].Key < projectionRecords[j].Key })
	packageValue := &RecoveryPackage{
		Version: RecoveryPackageVersion, WalletID: snapshot.WalletID,
		AccountIndex: snapshot.AccountIndex, EngineBuildID: snapshot.EngineBuildID,
		ProjectionRecords: projectionRecords,
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
	if len(packageValue.EngineRecords) != 0 {
		return fmt.Errorf("%w: local RGB11 transaction tasks are not account recovery data", ErrRGB11Inconsistent)
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
	proofAssets := make(map[string]map[indexer.AssetName]struct{})
	for _, record := range packageValue.ProjectionRecords {
		switch {
		case strings.HasPrefix(record.Key, "proof-"):
			var proof AllocationProof
			if decode(record.Value, &proof) != nil {
				return ErrInvalidProof
			}
			if proof.Status != "settled" || proof.Confirmations <= 0 {
				return fmt.Errorf("%w: recovery proof %s is not settled", ErrRGB11Inconsistent, record.Key)
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
			return fmt.Errorf("%w: pending transfer is wallet-local", ErrRGB11Inconsistent)
		case strings.HasPrefix(record.Key, "transfer-"):
			return fmt.Errorf("%w: transfer state is wallet-local", ErrRGB11Inconsistent)
		case strings.HasPrefix(record.Key, "prepared-receive-"):
			return fmt.Errorf("%w: receive task is wallet-local", ErrRGB11Inconsistent)
		case strings.HasPrefix(record.Key, "receive-key-"):
			return fmt.Errorf("%w: receive key is wallet-local", ErrRGB11Inconsistent)
		case strings.HasPrefix(record.Key, "receive-reservation-"):
			return fmt.Errorf("%w: receive reservation is wallet-local", ErrRGB11Inconsistent)
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

// MergeRecoverySnapshot updates account-managed durable ownership while
// preserving the device-local transaction database. Recovery snapshots are
// deliberately incomplete and must never replace pending transfers, transfer
// history, signed transactions, reservations, or receive tasks.
func MergeRecoverySnapshot(current, recovery *RGB11WalletSnapshot) (*RGB11WalletSnapshot, error) {
	if current == nil || recovery == nil || current.WalletID != recovery.WalletID ||
		current.AccountIndex != recovery.AccountIndex || current.EngineBuildID != recovery.EngineBuildID {
		return nil, ErrRGB11Inconsistent
	}
	if err := ValidateWalletSnapshot(current); err != nil {
		return nil, err
	}
	if len(recovery.EngineRecords) != 0 {
		return nil, ErrRGB11Inconsistent
	}
	if len(recovery.ProjectionRecords) != 0 {
		if err := ValidateWalletSnapshot(recovery); err != nil {
			return nil, err
		}
	}

	projection := make(map[string]SnapshotRecord, len(current.ProjectionRecords)+len(recovery.ProjectionRecords))
	for _, record := range current.ProjectionRecords {
		projection[record.Key] = SnapshotRecord{Key: record.Key, Value: append([]byte(nil), record.Value...)}
	}
	for _, record := range recovery.ProjectionRecords {
		if strings.HasPrefix(record.Key, "output-") {
			if existing, ok := projection[record.Key]; ok {
				var localOutput, durableOutput indexer.TxOutput
				if decode(existing.Value, &localOutput) != nil || decode(record.Value, &durableOutput) != nil ||
					localOutput.OutPointStr != durableOutput.OutPointStr {
					return nil, ErrRGB11Inconsistent
				}
				for i := range durableOutput.Assets {
					asset := durableOutput.Assets[i]
					localOutput.RemoveAsset(&asset.Name)
					if err := localOutput.Assets.Add(asset.Clone()); err != nil {
						return nil, err
					}
				}
				encoded, err := encode(&localOutput)
				if err != nil {
					return nil, err
				}
				projection[record.Key] = SnapshotRecord{Key: record.Key, Value: encoded}
				continue
			}
		}
		projection[record.Key] = SnapshotRecord{Key: record.Key, Value: append([]byte(nil), record.Value...)}
	}

	merged := &RGB11WalletSnapshot{
		Version: current.Version, WalletID: current.WalletID,
		AccountIndex: current.AccountIndex, EngineBuildID: current.EngineBuildID,
		EngineRecords:     cloneRecoveryRecords(current.EngineRecords),
		ProjectionRecords: make([]SnapshotRecord, 0, len(projection)),
	}
	for _, record := range projection {
		merged.ProjectionRecords = append(merged.ProjectionRecords, record)
	}
	sort.Slice(merged.ProjectionRecords, func(i, j int) bool {
		return merged.ProjectionRecords[i].Key < merged.ProjectionRecords[j].Key
	})
	if err := ValidateWalletSnapshot(merged); err != nil {
		return nil, err
	}
	return merged, nil
}
