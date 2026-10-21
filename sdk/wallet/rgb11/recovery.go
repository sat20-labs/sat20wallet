package rgb11wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/invoicing"
	corewallet "github.com/sat20-labs/rgb11/wallet"
)

// RecoveryPackageVersion identifies the complete cross-device recovery
// closure; it is independent from the complete local snapshot used for
// diagnostics and runtime caches.
const RecoveryPackageVersion = uint32(2)

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

// RecoveryPackageFromSnapshot exports the complete RGB11 recovery closure.
// Projection status and balances are retained as evidence/cache, but a cold
// restore must rederive current ownership from these materials and fresh chain
// evidence before making any allocation spendable.
func RecoveryPackageFromSnapshot(snapshot *RGB11WalletSnapshot, now int64) (*RecoveryPackage, error) {
	_ = now // Kept in the API for callers of the version-1 package format.
	if err := ValidateWalletSnapshot(snapshot); err != nil {
		return nil, fmt.Errorf("validate source RGB11 snapshot: %w", err)
	}
	projectionRecords, err := buildRecoveryProjectionRecords(snapshot)
	if err != nil {
		return nil, fmt.Errorf("build RGB11 recovery closure: %w", err)
	}
	projectionRecords, err = canonicalizeRecoveryProjectionRecords(projectionRecords)
	if err != nil {
		return nil, fmt.Errorf("canonicalize RGB11 recovery closure: %w", err)
	}
	packageValue := &RecoveryPackage{
		Version: RecoveryPackageVersion, WalletID: snapshot.WalletID,
		AccountIndex: snapshot.AccountIndex, EngineBuildID: snapshot.EngineBuildID,
		ProjectionRecords: projectionRecords,
		EngineRecords:     cloneRecoveryRecords(snapshot.EngineRecords),
	}
	if err := ValidateRecoveryPackage(packageValue); err != nil {
		return nil, fmt.Errorf("validate RGB11 recovery package: %w", err)
	}
	return packageValue, nil
}

func recoveryProjectionRoots() []string {
	return []string{
		"proof-", "pending-", "transfer-", "prepared-receive-",
		"receive-key-", "receive-reservation-", "local-name-",
		"registered-name-", "contract-object-",
	}
}

func recoverySnapshotIndex(records []SnapshotRecord) map[string]SnapshotRecord {
	result := make(map[string]SnapshotRecord, len(records))
	for _, record := range records {
		result[record.Key] = cloneSnapshotRecord(record)
	}
	return result
}

// recoveryClosureKeys returns the exact projection record set required by the
// roots present in a wallet snapshot. Active operations retain their original
// consignment object; terminal transfer rows and old proofs remain as evidence
// until a separately approved retention rule can safely remove them.
func recoveryClosureKeys(snapshot *RGB11WalletSnapshot,
	projection map[string]SnapshotRecord) (map[string]struct{}, map[string]map[indexer.AssetName]struct{}, error) {
	keep := make(map[string]struct{})
	assetsByOutpoint := make(map[string]map[indexer.AssetName]struct{})
	add := func(key string) { keep[key] = struct{}{} }
	addObject := func(hash string) {
		if hash != "" {
			add("object-" + hash)
		}
	}
	addValidation := func(hash string) {
		if hash != "" {
			add("validation-" + hash)
			addObject(hash)
		}
	}
	contractsNamed := make(map[string]struct{})
	contractObjects := make(map[string]string)

	for _, record := range snapshot.ProjectionRecords {
		for _, prefix := range recoveryProjectionRoots() {
			if strings.HasPrefix(record.Key, prefix) {
				add(record.Key)
				break
			}
		}
		switch {
		case strings.HasPrefix(record.Key, "proof-"):
			var proof AllocationProof
			if decode(record.Value, &proof) != nil {
				return nil, nil, ErrInvalidProof
			}
			add("output-" + proof.OutPoint)
			addValidation(proof.ConsignmentHash)
			if assetsByOutpoint[proof.OutPoint] == nil {
				assetsByOutpoint[proof.OutPoint] = make(map[indexer.AssetName]struct{})
			}
			assetsByOutpoint[proof.OutPoint][proof.AssetName] = struct{}{}
		case strings.HasPrefix(record.Key, "pending-"):
			var pending PendingTransfer
			if decode(record.Value, &pending) != nil {
				return nil, nil, ErrValidationReceipt
			}
			if pending.State.ConsignmentHash != "" && pending.RecipientObjectHash != "" &&
				pending.State.ConsignmentHash != pending.RecipientObjectHash {
				return nil, nil, ErrValidationReceipt
			}
			addObject(pending.LocalObjectHash)
			if pending.RecipientObjectHash != "" {
				key := "object-" + pending.RecipientObjectHash
				if _, exists := projection[key]; !exists {
					return nil, nil, fmt.Errorf("%w: transfer %s is missing recipient consignment %s",
						ErrValidationReceipt, pending.State.TransferID, pending.RecipientObjectHash)
				}
				add(key)
			}
		case strings.HasPrefix(record.Key, "transfer-"):
			var state TransferState
			if decode(record.Value, &state) != nil {
				return nil, nil, ErrValidationReceipt
			}
			if state.ConsignmentHash != "" {
				objectKey := "object-" + state.ConsignmentHash
				_, hasObject := projection[objectKey]
				if hasObject {
					add(objectKey)
					if _, hasReceipt := projection["validation-"+state.ConsignmentHash]; hasReceipt {
						addValidation(state.ConsignmentHash)
					}
				} else if activeRGB11TransferStatus(state.Status) {
					return nil, nil, fmt.Errorf("%w: active transfer %s is missing consignment %s",
						ErrValidationReceipt, state.TransferID, state.ConsignmentHash)
				}
			}
		case strings.HasPrefix(record.Key, "contract-object-"):
			addObject(string(record.Value))
			contractObjects[strings.TrimPrefix(record.Key, "contract-object-")] = string(record.Value)
		}
	}

	// Aliases, verified registrations, and contract imports are user-visible
	// contract intent. Keep one matching validation receipt and its consignment
	// for every named/imported ContractID, including contracts with no balance.
	for _, record := range snapshot.ProjectionRecords {
		if strings.HasPrefix(record.Key, "local-name-") {
			contractsNamed[strings.TrimPrefix(record.Key, "local-name-")] = struct{}{}
		}
		if strings.HasPrefix(record.Key, "registered-name-") {
			contractsNamed[strings.TrimPrefix(record.Key, "registered-name-")] = struct{}{}
		}
	}
	for _, record := range snapshot.ProjectionRecords {
		if !strings.HasPrefix(record.Key, "validation-") {
			continue
		}
		var receipt ValidationReceipt
		if decode(record.Value, &receipt) != nil {
			return nil, nil, ErrValidationReceipt
		}
		aliasKey := ""
		if len(contractsNamed) != 0 {
			identity, identityErr := NewContractAssetKey(receipt.ContractID, indexer.ASSET_TYPE_FT)
			if identityErr != nil {
				return nil, nil, ErrValidationReceipt
			}
			aliasKey = identity.Ticker
		}
		contractHash := sha256.Sum256([]byte(receipt.ContractID))
		contractObjectKey := hex.EncodeToString(contractHash[:])
		_, aliased := contractsNamed[aliasKey]
		referencedHash, imported := contractObjects[contractObjectKey]
		_, hasReferencedReceipt := projection["validation-"+referencedHash]
		if imported && hasReferencedReceipt && referencedHash != strings.TrimPrefix(record.Key, "validation-") {
			continue
		}
		if aliased || imported {
			addValidation(strings.TrimPrefix(record.Key, "validation-"))
			delete(contractsNamed, aliasKey)
			delete(contractObjects, contractObjectKey)
		}
	}
	if len(contractsNamed) != 0 || len(contractObjects) != 0 {
		return nil, nil, fmt.Errorf("%w: named contract has no matching validated object", ErrValidationReceipt)
	}

	for outpoint, assets := range assetsByOutpoint {
		key := "output-" + outpoint
		record, ok := projection[key]
		if !ok {
			return nil, nil, ErrProjectionMismatch
		}
		minimal, err := minimalRecoveryOutput(record, assets)
		if err != nil {
			return nil, nil, err
		}
		projection[key] = minimal
	}

	return keep, assetsByOutpoint, nil
}

func buildRecoveryProjectionRecords(snapshot *RGB11WalletSnapshot) ([]SnapshotRecord, error) {
	projection := recoverySnapshotIndex(snapshot.ProjectionRecords)
	keep, _, err := recoveryClosureKeys(snapshot, projection)
	if err != nil {
		return nil, err
	}
	result := make([]SnapshotRecord, 0, len(keep))
	for key := range keep {
		record, ok := projection[key]
		if !ok {
			return nil, fmt.Errorf("%w: missing recovery dependency %s", ErrValidationReceipt, key)
		}
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, nil
}

// canonicalizeRecoveryProjectionRecords removes projection/policy timestamps
// from the account recovery representation. Those fields are recomputed from
// fresh chain/policy evidence and must not cause a stable bundle rewrite on
// every confirmation refresh or receipt revalidation.
func canonicalizeRecoveryProjectionRecords(records []SnapshotRecord) ([]SnapshotRecord, error) {
	result := cloneRecoveryRecords(records)
	receipts := make(map[string]*ValidationReceipt)
	receiptHashes := make(map[string]string)
	for index := range result {
		record := &result[index]
		if !strings.HasPrefix(record.Key, "validation-") {
			continue
		}
		hash := strings.TrimPrefix(record.Key, "validation-")
		var receipt ValidationReceipt
		if decode(record.Value, &receipt) != nil || receipt.validate(hash) != nil {
			return nil, ErrValidationReceipt
		}
		if !CompatibleEngineBuildID(receipt.EngineBuildID) {
			return nil, ErrValidationReceipt
		}
		receipt.EngineBuildID = NativeEngineBuildID
		receipt.ValidatedAt = 1
		receiptBytes, err := encode(&receipt)
		if err != nil {
			return nil, err
		}
		canonicalHash, err := receipt.Hash()
		if err != nil {
			return nil, err
		}
		record.Value = receiptBytes
		receipts[hash] = &receipt
		receiptHashes[hash] = canonicalHash
	}
	for index := range result {
		record := &result[index]
		if !strings.HasPrefix(record.Key, "proof-") {
			continue
		}
		var proof AllocationProof
		if decode(record.Value, &proof) != nil {
			return nil, ErrInvalidProof
		}
		canonicalHash := receiptHashes[proof.ConsignmentHash]
		if canonicalHash == "" || receipts[proof.ConsignmentHash] == nil {
			return nil, ErrValidationReceipt
		}
		proof.ValidationHash = canonicalHash
		proof.Status, proof.Confirmations = "", 0
		proof.PolicyStatus, proof.PolicyReason = "", ""
		proofBytes, err := encode(&proof)
		if err != nil {
			return nil, err
		}
		record.Value = proofBytes
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, nil
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
		!CompatibleEngineBuildID(value.EngineBuildID) {
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
	expected, proofAssets, err := recoveryClosureKeys(snapshot, projection)
	if err != nil {
		return err
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
	for _, record := range packageValue.ProjectionRecords {
		if strings.HasPrefix(record.Key, "validation-") {
			var receipt ValidationReceipt
			if decode(record.Value, &receipt) != nil || receipt.ValidatedAt != 1 ||
				receipt.EngineBuildID != NativeEngineBuildID {
				return fmt.Errorf("%w: non-canonical recovery receipt %s", ErrRGB11Inconsistent, record.Key)
			}
		}
		if strings.HasPrefix(record.Key, "proof-") {
			var proof AllocationProof
			if decode(record.Value, &proof) != nil || proof.Status != "" || proof.Confirmations != 0 ||
				proof.PolicyStatus != "" || proof.PolicyReason != "" {
				return fmt.Errorf("%w: non-canonical recovery proof %s", ErrRGB11Inconsistent, record.Key)
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

// MergeRecoverySnapshot merges complete recovery closures only when overlapping
// records are byte-compatible. It has no authority to select a newer lifecycle
// phase; callers importing a CAS-selected account payload must use
// MergeSelectedRecoverySnapshot.
func MergeRecoverySnapshot(current, recovery *RGB11WalletSnapshot) (*RGB11WalletSnapshot, error) {
	if current == nil || recovery == nil || current.WalletID != recovery.WalletID ||
		current.AccountIndex != recovery.AccountIndex ||
		!CompatibleEngineBuildID(current.EngineBuildID) || !CompatibleEngineBuildID(recovery.EngineBuildID) {
		return nil, ErrRGB11Inconsistent
	}
	if err := ValidateWalletSnapshot(current); err != nil {
		return nil, err
	}
	if err := ValidateRecoveryPackage(&RecoveryPackage{
		Version: RecoveryPackageVersion, WalletID: recovery.WalletID,
		AccountIndex: recovery.AccountIndex, EngineBuildID: recovery.EngineBuildID,
		ProjectionRecords: recovery.ProjectionRecords, EngineRecords: recovery.EngineRecords,
	}); err != nil {
		return nil, err
	}
	currentPackage, err := RecoveryPackageFromSnapshot(current, 0)
	if err != nil {
		return nil, err
	}
	currentRecords := recoverySnapshotIndex(currentPackage.ProjectionRecords)
	recoveryRecords := recoverySnapshotIndex(recovery.ProjectionRecords)
	for key := range currentRecords {
		if _, ok := recoveryRecords[key]; !ok {
			return nil, fmt.Errorf("%w: recovery omitted local record %s", ErrRGB11Inconsistent, key)
		}
	}
	recoveryEngineRecords := mapSnapshotRecords(recovery.EngineRecords)
	for _, record := range current.EngineRecords {
		if _, ok := recoveryEngineRecords[record.Key]; !ok {
			return nil, fmt.Errorf("%w: recovery omitted local receive request %s",
				ErrRGB11Inconsistent, record.Key)
		}
	}

	projection := make(map[string]SnapshotRecord, len(current.ProjectionRecords)+len(recovery.ProjectionRecords))
	for _, record := range current.ProjectionRecords {
		projection[record.Key] = cloneSnapshotRecord(record)
	}
	for _, record := range recovery.ProjectionRecords {
		existing, ok := projection[record.Key]
		if !ok {
			projection[record.Key] = cloneSnapshotRecord(record)
			continue
		}
		merged, err := mergeRecoveryProjectionRecord(existing, record)
		if err != nil {
			return nil, fmt.Errorf("merge RGB11 recovery record %s: %w", record.Key, err)
		}
		projection[record.Key] = merged
	}
	engine := make(map[string]SnapshotRecord, len(current.EngineRecords)+len(recovery.EngineRecords))
	for _, record := range current.EngineRecords {
		engine[record.Key] = cloneSnapshotRecord(record)
	}
	for _, record := range recovery.EngineRecords {
		if existing, ok := engine[record.Key]; ok {
			if !bytes.Equal(existing.Value, record.Value) {
				return nil, fmt.Errorf("%w: conflicting receive request %s",
					ErrRGB11Inconsistent, record.Key)
			}
			continue
		}
		engine[record.Key] = cloneSnapshotRecord(record)
	}

	merged := &RGB11WalletSnapshot{
		Version: current.Version, WalletID: current.WalletID,
		AccountIndex: current.AccountIndex, EngineBuildID: NativeEngineBuildID,
		EngineRecords:     make([]SnapshotRecord, 0, len(engine)),
		ProjectionRecords: make([]SnapshotRecord, 0, len(projection)),
	}
	for _, record := range engine {
		merged.EngineRecords = append(merged.EngineRecords, record)
	}
	for _, record := range projection {
		merged.ProjectionRecords = append(merged.ProjectionRecords, record)
	}
	sort.Slice(merged.EngineRecords, func(i, j int) bool {
		return merged.EngineRecords[i].Key < merged.EngineRecords[j].Key
	})
	sort.Slice(merged.ProjectionRecords, func(i, j int) bool {
		return merged.ProjectionRecords[i].Key < merged.ProjectionRecords[j].Key
	})
	if err := ValidateWalletSnapshot(merged); err != nil {
		return nil, err
	}
	return merged, nil
}

// MergeSelectedRecoverySnapshot applies an account payload that has already
// been selected by the account provider/scope three-way CAS. Its lifecycle
// values are authoritative for matching operation identities. The selected
// complete closure replaces the local closure; this function never unions an
// old pending record back into a completed/cancelled selected state.
func MergeSelectedRecoverySnapshot(current, selected *RGB11WalletSnapshot) (*RGB11WalletSnapshot, error) {
	if current == nil || selected == nil || current.WalletID != selected.WalletID ||
		current.AccountIndex != selected.AccountIndex ||
		!CompatibleEngineBuildID(current.EngineBuildID) || !CompatibleEngineBuildID(selected.EngineBuildID) {
		return nil, ErrRGB11Inconsistent
	}
	if err := ValidateWalletSnapshot(current); err != nil {
		return nil, err
	}
	selectedPackage := &RecoveryPackage{
		Version: RecoveryPackageVersion, WalletID: selected.WalletID,
		AccountIndex: selected.AccountIndex, EngineBuildID: selected.EngineBuildID,
		ProjectionRecords: selected.ProjectionRecords, EngineRecords: selected.EngineRecords,
	}
	if err := ValidateRecoveryPackage(selectedPackage); err != nil {
		return nil, err
	}
	currentPackage, err := RecoveryPackageFromSnapshot(current, 0)
	if err != nil {
		return nil, err
	}
	currentSnapshot, err := currentPackage.WalletSnapshot()
	if err != nil {
		return nil, err
	}
	selectedSnapshot, err := selectedPackage.WalletSnapshot()
	if err != nil {
		return nil, err
	}
	currentProjection := recoverySnapshotIndex(currentSnapshot.ProjectionRecords)
	selectedProjection := recoverySnapshotIndex(selectedSnapshot.ProjectionRecords)
	currentEngine := mapSnapshotRecords(currentSnapshot.EngineRecords)
	selectedEngine := mapSnapshotRecords(selectedSnapshot.EngineRecords)

	for key, record := range currentProjection {
		incoming, exists := selectedProjection[key]
		if !exists {
			if strings.HasPrefix(key, "object-") &&
				selectedMayOmitCompactedRejectedObject(currentSnapshot, selectedSnapshot,
					strings.TrimPrefix(key, "object-")) {
				continue
			}
			if strings.HasPrefix(key, "prepared-receive-") &&
				selectedHasAcceptedReceiveForPrepared(selectedSnapshot, record) {
				continue
			}
			if strings.HasPrefix(key, "receive-reservation-") &&
				(selectedHasAcceptedReceiveForReservation(selectedSnapshot, key) ||
					selectedMayReleaseExpiredReceive(currentSnapshot, selectedSnapshot, record, time.Now().Unix())) {
				continue
			}
			return nil, fmt.Errorf("%w: selected recovery omitted local responsibility %s",
				ErrRGB11Inconsistent, key)
		}
		if err := compatibleSelectedRecoveryRecord(record, incoming); err != nil {
			return nil, fmt.Errorf("selected RGB11 recovery identity %s: %w", key, err)
		}
	}
	for key, record := range currentEngine {
		incoming, exists := selectedEngine[key]
		if !exists {
			return nil, fmt.Errorf("%w: selected recovery omitted local receive request %s",
				ErrRGB11Inconsistent, key)
		}
		if !compatibleSelectedReceiveRequest(record, incoming) {
			return nil, fmt.Errorf("%w: selected recovery changed receive request identity %s",
				ErrRGB11Inconsistent, key)
		}
	}
	selectedSnapshot.EngineBuildID = NativeEngineBuildID
	return selectedSnapshot, nil
}

// selectedMayOmitCompactedRejectedObject permits the existing rejected-send
// compaction only when every current reference is explicitly replaced by the
// same selected rejected operation. Proof, receipt, contract, and receive
// request references keep an object in the recovery closure regardless of the
// sender-side compaction state.
func selectedMayOmitCompactedRejectedObject(current, selected *RGB11WalletSnapshot,
	hash string) bool {
	if current == nil || selected == nil || hash == "" {
		return false
	}
	selectedProjection := mapSnapshotRecords(selected.ProjectionRecords)
	foundOwner := false
	for _, record := range current.ProjectionRecords {
		switch {
		case strings.HasPrefix(record.Key, "proof-"):
			var proof AllocationProof
			if decode(record.Value, &proof) != nil || proof.ConsignmentHash == hash {
				return false
			}
		case strings.HasPrefix(record.Key, "validation-"):
			if strings.TrimPrefix(record.Key, "validation-") == hash {
				return false
			}
		case strings.HasPrefix(record.Key, "contract-object-"):
			if string(record.Value) == hash {
				return false
			}
		case strings.HasPrefix(record.Key, "transfer-"):
			var left TransferState
			if decode(record.Value, &left) != nil {
				return false
			}
			if left.ConsignmentHash != hash {
				continue
			}
			foundOwner = true
			incoming, ok := selectedProjection[record.Key]
			var right TransferState
			if !ok || decode(incoming.Value, &right) != nil || right.Status != "rejected" ||
				!sameTransferIntent(left, right) {
				return false
			}
		case strings.HasPrefix(record.Key, "pending-"):
			var left PendingTransfer
			if decode(record.Value, &left) != nil {
				return false
			}
			if left.RecipientObjectHash != hash && left.LocalObjectHash != hash {
				continue
			}
			foundOwner = true
			incoming, ok := selectedProjection[record.Key]
			var right PendingTransfer
			if !ok || decode(incoming.Value, &right) != nil || right.State.Status != "rejected" ||
				!samePendingIntent(left, right) || right.RecipientObjectHash == hash ||
				right.LocalObjectHash == hash {
				return false
			}
		}
	}
	if !foundOwner {
		return false
	}
	// A receive request can independently retain the same immutable object.
	// Keep this check conservative: rejected sender compaction never proves that
	// an RGB11 engine request has finished consuming its copy.
	for _, record := range current.EngineRecords {
		request, err := corewallet.DecodeReceiveRequest(record.Value)
		if err != nil || request.ObjectHash == hash {
			return false
		}
	}
	return true
}

func mapSnapshotRecords(records []SnapshotRecord) map[string]SnapshotRecord {
	result := make(map[string]SnapshotRecord, len(records))
	for _, record := range records {
		result[record.Key] = cloneSnapshotRecord(record)
	}
	return result
}

func compatibleSelectedRecoveryRecord(current, selected SnapshotRecord) error {
	if current.Key != selected.Key {
		return ErrRGB11Inconsistent
	}
	switch {
	case strings.HasPrefix(current.Key, "proof-"):
		var left, right AllocationProof
		if decode(current.Value, &left) != nil || decode(selected.Value, &right) != nil {
			return ErrInvalidProof
		}
		left.ValidationHash, right.ValidationHash = "", ""
		left.Status, right.Status = "", ""
		left.Confirmations, right.Confirmations = 0, 0
		left.PolicyStatus, right.PolicyStatus = "", ""
		left.PolicyReason, right.PolicyReason = "", ""
		leftBytes, leftErr := encode(&left)
		rightBytes, rightErr := encode(&right)
		if leftErr != nil || rightErr != nil || !bytes.Equal(leftBytes, rightBytes) {
			return ErrInvalidProof
		}
		return nil
	case strings.HasPrefix(current.Key, "output-"):
		var left, right indexer.TxOutput
		if decode(current.Value, &left) != nil || decode(selected.Value, &right) != nil ||
			left.OutPointStr != right.OutPointStr || left.OutValue.Value != right.OutValue.Value ||
			!bytes.Equal(left.OutValue.PkScript, right.OutValue.PkScript) {
			return ErrProjectionMismatch
		}
		return nil
	case strings.HasPrefix(current.Key, "validation-"):
		var left, right ValidationReceipt
		if decode(current.Value, &left) != nil || decode(selected.Value, &right) != nil {
			return ErrValidationReceipt
		}
		// This is an identity/subset check, not validation of new facts. The
		// wallet importer authenticates expanded receipts against native history
		// before the candidate can be committed.
		selected, err := SelectHistoricalValidationReceipt(&HistoricalValidation{MaterialReceipt: &right},
			&left, right.Allocations)
		if err != nil || len(selected.Allocations) != len(right.Allocations) {
			return ErrValidationReceipt
		}
		selected.EngineBuildID, right.EngineBuildID = "", ""
		selected.ValidatedAt, right.ValidatedAt = 0, 0
		leftBytes, leftErr := encode(selected)
		rightBytes, rightErr := encode(&right)
		if leftErr != nil || rightErr != nil || !bytes.Equal(leftBytes, rightBytes) {
			return ErrValidationReceipt
		}
		return nil
	case strings.HasPrefix(current.Key, "transfer-"):
		var left, right TransferState
		if decode(current.Value, &left) != nil || decode(selected.Value, &right) != nil ||
			!sameTransferIntent(left, right) {
			return ErrRGB11Inconsistent
		}
		return nil
	case strings.HasPrefix(current.Key, "pending-"):
		var left, right PendingTransfer
		if decode(current.Value, &left) != nil || decode(selected.Value, &right) != nil ||
			!samePendingIntent(left, right) {
			return ErrRGB11Inconsistent
		}
		return nil
	case strings.HasPrefix(current.Key, "receive-reservation-"):
		var left, right ReceiveReservation
		if decode(current.Value, &left) != nil || decode(selected.Value, &right) != nil ||
			left.RequestID != right.RequestID || left.OutPoint != right.OutPoint {
			return ErrRGB11Inconsistent
		}
		return nil
	case strings.HasPrefix(current.Key, "local-name-"):
		// A same-contract local alias is user preference, so a clean selected
		// account rename is authoritative for this key.
		return nil
	case strings.HasPrefix(current.Key, "registered-name-"):
		// Package validation verifies the selected signed registration; the
		// account-selected record may legitimately refresh its signature.
		return nil
	default:
		if !bytes.Equal(current.Value, selected.Value) {
			return ErrRGB11Inconsistent
		}
		return nil
	}
}

func sameTransferIntent(left, right TransferState) bool {
	if left.TransferID != right.TransferID || left.Direction != right.Direction ||
		!sameOptionalString(left.BatchID, right.BatchID) ||
		!sameOptionalString(left.TransportMode, right.TransportMode) ||
		!sameOptionalString(left.RecipientID, right.RecipientID) ||
		!sameOptionalString(left.ReceiveRequestID, right.ReceiveRequestID) ||
		!sameOptionalString(left.ConsignmentHash, right.ConsignmentHash) ||
		!sameOptionalString(left.WitnessTxID, right.WitnessTxID) ||
		!sameOptionalString(left.ParentStateHash, right.ParentStateHash) ||
		!sameOptionalString(left.DKVSOperationID, right.DKVSOperationID) ||
		!sameOptionalString(left.RelayRecordKey, right.RelayRecordKey) ||
		!sameOptionalString(left.AckRecordKey, right.AckRecordKey) ||
		!sameOptionalString(left.AddressMessageID, right.AddressMessageID) ||
		!sameOptionalString(left.SenderAccountID, right.SenderAccountID) ||
		!sameOptionalString(left.ReceiverAccountID, right.ReceiverAccountID) ||
		!sameOptionalString(left.ReceiverAddress, right.ReceiverAddress) ||
		!sameOptionalString(left.ReceiveCapabilityKey, right.ReceiveCapabilityKey) ||
		!sameOptionalString(left.ReceiveCapabilityHash, right.ReceiveCapabilityHash) ||
		!sameOptionalString(left.DeliveryObjectID, right.DeliveryObjectID) ||
		!sameOptionalString(left.DeliveryRecordKey, right.DeliveryRecordKey) ||
		!sameOptionalString(left.DeliveryRecordHash, right.DeliveryRecordHash) ||
		!sameOptionalString(left.NetworkBackupRef, right.NetworkBackupRef) ||
		!sameOptionalInt(left.BatchSize, right.BatchSize) ||
		!sameOptionalUint32(left.RecipientVout, right.RecipientVout) ||
		!sameOptionalUint8(left.MinConfirmations, right.MinConfirmations) ||
		!sameOptionalInt64(left.Expiry, right.Expiry) ||
		!sameOptionalInt64(left.RelayExpiry, right.RelayExpiry) ||
		!sameOptionalUint64(left.DeliveryIssueHeight, right.DeliveryIssueHeight) ||
		!sameOptionalUint64(left.DeliveryTTL, right.DeliveryTTL) ||
		left.AddressMode != right.AddressMode || left.DeliveryTemporary != right.DeliveryTemporary ||
		!sameStringSlice(left.BatchTransferIDs, right.BatchTransferIDs) ||
		!sameStringSlice(left.InputOutPoints, right.InputOutPoints) ||
		!sameStringSlice(left.OutputOutPoints, right.OutputOutPoints) ||
		!sameOptionalAsset(left.Asset, right.Asset) {
		return false
	}
	if left.Invoice != right.Invoice &&
		!(left.Invoice == "" && left.SyntheticInvoiceRemoved) &&
		!(right.Invoice == "" && right.SyntheticInvoiceRemoved) {
		return false
	}
	return true
}

func samePendingIntent(left, right PendingTransfer) bool {
	if !sameTransferIntent(left.State, right.State) ||
		!sameOptionalString(left.RecipientObjectHash, right.RecipientObjectHash) &&
			!(right.RecipientObjectHash == "" && right.State.Status == "rejected") ||
		!sameOptionalString(left.LocalObjectHash, right.LocalObjectHash) &&
			!(right.LocalObjectHash == "" && right.State.Status == "rejected") ||
		!sameOptionalString(left.ReservationID, right.ReservationID) &&
			!(left.ReservationID != "" && right.ReservationID == "" && recoveryReservationReleased(right.State.Status)) ||
		!sameOptionalInt64(left.CreatedAt, right.CreatedAt) ||
		!samePendingSigningIntent(left, right) ||
		!samePendingChangeSeals(left, right) {
		return false
	}
	return true
}

func recoveryReservationReleased(status string) bool {
	switch status {
	case "rejected", "settled", "orphaned":
		return true
	default:
		return false
	}
}

func samePendingSigningIntent(left, right PendingTransfer) bool {
	if (left.ChannelSend == nil) != (right.ChannelSend == nil) {
		return false
	}
	if left.ChannelSend != nil {
		if left.ChannelSend.ChannelID != right.ChannelSend.ChannelID ||
			left.ChannelSend.Reason != right.ChannelSend.Reason ||
			left.ChannelSend.PayFeeByLocal != right.ChannelSend.PayFeeByLocal ||
			!bytes.Equal(left.ChannelSend.WitnessScript, right.ChannelSend.WitnessScript) ||
			!bytes.Equal(left.ChannelSend.PeerPubKey, right.ChannelSend.PeerPubKey) ||
			!bytes.Equal(left.ChannelSend.MoreData, right.ChannelSend.MoreData) ||
			left.ChannelSend.Signed && !right.ChannelSend.Signed {
			return false
		}
	}

	if !sameOptionalBytes(left.SignedPSBT, right.SignedPSBT) &&
		!(len(right.SignedPSBT) == 0 && right.State.Status != "prepared" &&
			(right.ChannelSend == nil || right.ChannelSend.Signed || right.State.Status == "rejected")) {
		return false
	}
	if bytes.Equal(left.SignedTx, right.SignedTx) {
		return true
	}
	if len(left.SignedTx) == 0 {
		if len(right.SignedTx) != 0 && right.State.WitnessTxID != "" {
			_, ok := recoveryWitnessTx(right.SignedTx, right.State.WitnessTxID)
			return ok
		}
		return true
	}
	if len(right.SignedTx) == 0 && right.State.Status == "rejected" {
		return true
	}
	if left.ChannelSend == nil || right.ChannelSend == nil || left.ChannelSend.Signed ||
		!right.ChannelSend.Signed || left.State.WitnessTxID == "" ||
		left.State.WitnessTxID != right.State.WitnessTxID {
		return false
	}
	unsigned, ok := recoveryWitnessTx(left.SignedTx, left.State.WitnessTxID)
	if !ok {
		return false
	}
	signed, ok := recoveryWitnessTx(right.SignedTx, right.State.WitnessTxID)
	if !ok {
		return false
	}
	var unsignedBase, signedBase bytes.Buffer
	return unsigned.SerializeNoWitness(&unsignedBase) == nil && signed.SerializeNoWitness(&signedBase) == nil &&
		bytes.Equal(unsignedBase.Bytes(), signedBase.Bytes())
}

func recoveryWitnessTx(raw []byte, txID string) (*wire.MsgTx, bool) {
	if len(raw) == 0 || txID == "" {
		return nil, false
	}
	reader := bytes.NewReader(raw)
	tx := wire.NewMsgTx(wire.TxVersion)
	if tx.Deserialize(reader) != nil || reader.Len() != 0 || tx.TxHash().String() != txID {
		return nil, false
	}
	return tx, true
}

func samePendingChangeSeals(left, right PendingTransfer) bool {
	if len(left.ChangeSeals) == 0 || len(left.ChangeSeals) == len(right.ChangeSeals) {
		if len(left.ChangeSeals) == 0 {
			return true
		}
		for index := range left.ChangeSeals {
			leftBytes, leftErr := left.ChangeSeals[index].StrictBytes()
			rightBytes, rightErr := right.ChangeSeals[index].StrictBytes()
			if leftErr != nil || rightErr != nil || !bytes.Equal(leftBytes, rightBytes) {
				return false
			}
		}
		return true
	}
	return len(right.ChangeSeals) == 0 && right.State.Status == "rejected"
}

func sameOptionalString(left, right string) bool { return left == "" || left == right }
func sameOptionalInt(left, right int) bool       { return left == 0 || left == right }
func sameOptionalInt64(left, right int64) bool   { return left == 0 || left == right }
func sameOptionalUint32(left, right uint32) bool {
	return left == 0 || left == right
}
func sameOptionalUint8(left, right uint8) bool { return left == 0 || left == right }
func sameOptionalUint64(left, right uint64) bool {
	return left == 0 || left == right
}
func sameStringSlice(left, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return len(left) == 0
	}
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
func sameOptionalAsset(left, right indexer.AssetInfo) bool {
	if left.Name.String() == "" {
		return true
	}
	return left.Name == right.Name && left.BindingSat == right.BindingSat &&
		left.Amount.Cmp(&right.Amount) == 0
}

func sameOptionalBytes(left, right []byte) bool {
	return len(left) == 0 || bytes.Equal(left, right)
}

func compatibleSelectedReceiveRequest(current, selected SnapshotRecord) bool {
	left, err := corewallet.DecodeReceiveRequest(current.Value)
	if err != nil {
		return false
	}
	right, err := corewallet.DecodeReceiveRequest(selected.Value)
	if err != nil {
		return false
	}
	left.Status, right.Status = corewallet.ReceivePrepared, corewallet.ReceivePrepared
	left.TransferID, right.TransferID = "", ""
	left.ObjectHash, right.ObjectHash = "", ""
	left.WitnessTxID, right.WitnessTxID = "", ""
	left.FailureCode, right.FailureCode = "", ""
	leftBytes, leftErr := corewallet.EncodeReceiveRequest(left)
	rightBytes, rightErr := corewallet.EncodeReceiveRequest(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}

// Expiry retires only an unused exact blind-invoice reservation. The request
// remains in both closures; ACKs and even partially persisted receive work
// prevent expiry from discarding its responsibility.
func selectedMayReleaseExpiredReceive(current, selected *RGB11WalletSnapshot,
	record SnapshotRecord, now int64) bool {
	var reservation ReceiveReservation
	if decode(record.Value, &reservation) != nil || reservation.Version != 1 ||
		record.Key != "receive-reservation-"+reservation.RequestID || reservation.ReservationID == "" {
		return false
	}
	key := "wallet/receive/" + reservation.RequestID
	left, leftOK := mapSnapshotRecords(current.EngineRecords)[key]
	right, rightOK := mapSnapshotRecords(selected.EngineRecords)[key]
	if !leftOK || !rightOK || !compatibleSelectedReceiveRequest(left, right) {
		return false
	}
	for _, item := range []struct {
		snapshot *RGB11WalletSnapshot
		record   SnapshotRecord
	}{{current, left}, {selected, right}} {
		request, err := corewallet.DecodeReceiveRequest(item.record.Value)
		if err != nil || request.RequestID != reservation.RequestID || request.Mode != corewallet.ReceiveBlind ||
			(request.Status != corewallet.ReceivePrepared && request.Status != corewallet.ReceiveRelayed) ||
			request.TransferID != "" || request.ObjectHash != "" || request.WitnessTxID != "" || request.FailureCode != "" ||
			request.Expiry <= 0 || request.Expiry > now || request.Expiry != reservation.Expiry || request.Seal.TxID == nil {
			return false
		}
		point := wire.OutPoint{Hash: chainhash.Hash(*request.Seal.TxID), Index: request.Seal.Vout}
		invoice, err := invoicing.Parse(request.Invoice)
		concealed, sealErr := request.Seal.Conceal()
		if err != nil || sealErr != nil || point.String() != reservation.OutPoint ||
			invoice.Expiry == nil || *invoice.Expiry != request.Expiry ||
			invoice.Beneficiary.Kind != invoicing.BeneficiaryBlindedSeal ||
			!bytes.Equal(invoice.Beneficiary.BlindedSeal[:], concealed[:]) {
			return false
		}
		for _, responsibility := range item.snapshot.ProjectionRecords {
			switch {
			case strings.HasPrefix(responsibility.Key, "prepared-receive-"):
				if string(responsibility.Value) == request.RequestID {
					return false
				}
			case strings.HasPrefix(responsibility.Key, "transfer-"):
				var state TransferState
				if decode(responsibility.Value, &state) != nil || state.ReceiveRequestID == request.RequestID ||
					state.Invoice == request.Invoice {
					return false
				}
			}
		}
	}
	return true
}

func selectedHasAcceptedReceiveForPrepared(selected *RGB11WalletSnapshot, prepared SnapshotRecord) bool {
	requestID := string(prepared.Value)
	transferID := strings.TrimPrefix(prepared.Key, "prepared-receive-")
	return selectedHasAcceptedReceive(selected, transferID, requestID)
}

func selectedHasAcceptedReceiveForReservation(selected *RGB11WalletSnapshot, reservationKey string) bool {
	requestID := strings.TrimPrefix(reservationKey, "receive-reservation-")
	requests := mapSnapshotRecords(selected.EngineRecords)
	requestRecord, ok := requests["wallet/receive/"+requestID]
	if !ok {
		return false
	}
	request, err := corewallet.DecodeReceiveRequest(requestRecord.Value)
	if err != nil || request.RequestID != requestID || request.TransferID == "" ||
		(request.Status != corewallet.ReceiveAcknowledged && request.Status != corewallet.ReceiveAccepted &&
			request.Status != corewallet.ReceiveSettled) {
		return false
	}
	return selectedHasAcceptedReceive(selected, request.TransferID, requestID)
}

func selectedHasAcceptedReceive(selected *RGB11WalletSnapshot, transferID, requestID string) bool {
	if selected == nil || transferID == "" || requestID == "" {
		return false
	}
	requests := mapSnapshotRecords(selected.EngineRecords)
	requestRecord, ok := requests["wallet/receive/"+requestID]
	if !ok {
		return false
	}
	request, err := corewallet.DecodeReceiveRequest(requestRecord.Value)
	if err != nil || request.TransferID != transferID ||
		(request.Status != corewallet.ReceiveAcknowledged && request.Status != corewallet.ReceiveAccepted &&
			request.Status != corewallet.ReceiveSettled) {
		return false
	}
	transfers := mapSnapshotRecords(selected.ProjectionRecords)
	transferRecord, ok := transfers["transfer-"+transferID]
	if !ok {
		return false
	}
	var state TransferState
	if decode(transferRecord.Value, &state) != nil || state.Direction != "receive" ||
		state.ReceiveRequestID != requestID || state.ConsignmentHash == "" ||
		request.ObjectHash != "" && request.ObjectHash != state.ConsignmentHash ||
		(state.Status != "awaiting_broadcast" && state.Status != "pending" &&
			state.Status != "settled" && state.Status != "orphaned") {
		return false
	}
	return true
}

func mergeRecoveryProjectionRecord(current, incoming SnapshotRecord) (SnapshotRecord, error) {
	if current.Key != incoming.Key {
		return SnapshotRecord{}, ErrRGB11Inconsistent
	}
	switch {
	case strings.HasPrefix(current.Key, "proof-"):
		var left, right AllocationProof
		if decode(current.Value, &left) != nil || decode(incoming.Value, &right) != nil {
			return SnapshotRecord{}, ErrInvalidProof
		}
		// Status/confirmations and issuer policy decisions are caches. The import
		// candidate recalculates chain state and send authorization rechecks policy.
		left.Status, left.Confirmations, left.PolicyStatus, left.PolicyReason = "", 0, "", ""
		right.Status, right.Confirmations, right.PolicyStatus, right.PolicyReason = "", 0, "", ""
		leftBytes, leftErr := encode(&left)
		rightBytes, rightErr := encode(&right)
		if leftErr != nil || rightErr != nil || !bytes.Equal(leftBytes, rightBytes) {
			return SnapshotRecord{}, ErrInvalidProof
		}
		// Retain one immutable identity record; fresh evidence replaces the
		// derived fields before any restored allocation becomes spendable.
		return cloneSnapshotRecord(current), nil
	case strings.HasPrefix(current.Key, "output-"):
		var left, right indexer.TxOutput
		if decode(current.Value, &left) != nil || decode(incoming.Value, &right) != nil ||
			left.OutPointStr != right.OutPointStr || left.OutValue.Value != right.OutValue.Value ||
			!bytes.Equal(left.OutValue.PkScript, right.OutValue.PkScript) {
			return SnapshotRecord{}, ErrProjectionMismatch
		}
		for i := range right.Assets {
			asset := right.Assets[i]
			local := left.GetAsset(&asset.Name)
			if local != nil {
				if local.Cmp(&asset.Amount) != 0 {
					return SnapshotRecord{}, ErrProjectionMismatch
				}
				continue
			}
			if err := left.Assets.Add(asset.Clone()); err != nil {
				return SnapshotRecord{}, err
			}
		}
		encoded, err := encode(&left)
		if err != nil {
			return SnapshotRecord{}, err
		}
		return SnapshotRecord{Key: current.Key, Value: encoded}, nil
	default:
		if !bytes.Equal(current.Value, incoming.Value) {
			return SnapshotRecord{}, fmt.Errorf("%w: conflicting record identity", ErrRGB11Inconsistent)
		}
		return cloneSnapshotRecord(current), nil
	}
}
