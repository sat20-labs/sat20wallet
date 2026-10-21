package rgb11wallet

import (
	"bytes"

	indexer "github.com/sat20-labs/indexer/common"
)

type validatedOpout struct {
	operationID     string
	assignmentType  uint32
	assignmentIndex uint32
}

type validatedCarrierAsset struct {
	outpoint string
	asset    indexer.AssetName
}

// SelectHistoricalValidationReceipt builds a narrow receipt from a successful
// core history report. It retains the allocations already bound by base and
// adds only required allocations, matching every selected item to the report's
// exact full Opout and immutable allocation facts. Historical allocations not
// selected here remain transient and do not inflate persisted receipts.
func SelectHistoricalValidationReceipt(history *HistoricalValidation, base *ValidationReceipt,
	required []ValidatedAllocation) (*ValidationReceipt, error) {
	if history == nil || history.MaterialReceipt == nil {
		return nil, ErrValidationReceipt
	}
	material := cloneValidationReceipt(history.MaterialReceipt)
	if !receiptAllocationsHaveAmounts(material) {
		return nil, ErrValidationReceipt
	}
	if err := material.validateStatus(material.ConsignmentHash, "valid"); err != nil {
		return nil, err
	}
	if base != nil {
		base = cloneValidationReceipt(base)
		if !receiptAllocationsHaveAmounts(base) {
			return nil, ErrValidationReceipt
		}
		if err := base.validateStatus(base.ConsignmentHash, "valid"); err != nil ||
			!sameReceiptMaterialIdentity(base, material) {
			return nil, ErrValidationReceipt
		}
	}

	verified := make(map[validatedOpout]ValidatedAllocation, len(material.Allocations))
	for _, allocation := range material.Allocations {
		key := allocationOpout(allocation)
		if key.operationID == "" {
			return nil, ErrValidationReceipt
		}
		if _, exists := verified[key]; exists {
			return nil, ErrValidationReceipt
		}
		verified[key] = allocation
	}

	selected := make(map[validatedOpout]ValidatedAllocation, len(material.Allocations))
	add := func(candidate ValidatedAllocation) error {
		key := allocationOpout(candidate)
		core, exists := verified[key]
		if !exists || !sameValidatedAllocationFacts(core, candidate) {
			return ErrValidationReceipt
		}
		if previous, exists := selected[key]; exists {
			if !sameValidatedAllocationFacts(previous, core) {
				return ErrValidationReceipt
			}
			return nil
		}
		selected[key] = cloneValidatedAllocation(core)
		return nil
	}
	if base != nil {
		for _, allocation := range base.Allocations {
			if err := add(allocation); err != nil {
				return nil, err
			}
		}
	}
	for _, allocation := range required {
		if err := add(allocation); err != nil {
			return nil, err
		}
	}

	carrierAssets := make(map[validatedCarrierAsset]validatedOpout, len(selected))
	allocations := make([]ValidatedAllocation, 0, len(selected))
	for key, allocation := range selected {
		carrierKey := validatedCarrierAsset{outpoint: allocation.OutPoint, asset: allocation.AssetName}
		if previous, exists := carrierAssets[carrierKey]; exists && previous != key {
			return nil, ErrValidationReceipt
		}
		carrierAssets[carrierKey] = key
		allocations = append(allocations, allocation)
	}
	sortValidatedAllocations(allocations)

	result := material
	if base != nil {
		result = base
	}
	result.Allocations = allocations
	stateHash, err := hashAllocations(result.ContractID, result.SchemaID, allocations)
	if err != nil {
		return nil, err
	}
	result.StateHash = stateHash
	if err := result.validateStatus(result.ConsignmentHash, "valid"); err != nil {
		return nil, err
	}
	return result, nil
}

func allocationOpout(allocation ValidatedAllocation) validatedOpout {
	return validatedOpout{operationID: allocation.OperationID,
		assignmentType: allocation.AssignmentType, assignmentIndex: allocation.AssignmentIndex}
}

func sameReceiptMaterialIdentity(left, right *ValidationReceipt) bool {
	return left != nil && right != nil && left.Version == right.Version &&
		left.ConsignmentHash == right.ConsignmentHash && left.ContractID == right.ContractID &&
		left.SchemaID == right.SchemaID && left.TransferID == right.TransferID && left.Status == right.Status
}

func sameValidatedAllocationFacts(left, right ValidatedAllocation) bool {
	if left.OutPoint != right.OutPoint || left.AssetName != right.AssetName ||
		left.Amount.Precision != right.Amount.Precision || left.Amount.Value == nil || right.Amount.Value == nil ||
		left.Amount.Value.Cmp(right.Amount.Value) != 0 || left.OperationID != right.OperationID ||
		left.AssignmentType != right.AssignmentType || left.AssignmentIndex != right.AssignmentIndex ||
		left.StateClass != right.StateClass || !bytes.Equal(left.StateData, right.StateData) ||
		!bytes.Equal(left.SealDisclosure, right.SealDisclosure) || left.SealBlinding != right.SealBlinding ||
		left.WitnessTxPtr != right.WitnessTxPtr || left.CommitmentMethod != right.CommitmentMethod ||
		!bytes.Equal(left.CarrierInternalKey, right.CarrierInternalKey) ||
		!bytes.Equal(left.TapretRoot, right.TapretRoot) || !bytes.Equal(left.TapretProof, right.TapretProof) {
		return false
	}
	return left.WitnessTxID == "" || right.WitnessTxID == "" || left.WitnessTxID == right.WitnessTxID
}

func cloneValidationReceipt(receipt *ValidationReceipt) *ValidationReceipt {
	if receipt == nil {
		return nil
	}
	copy := *receipt
	copy.Allocations = make([]ValidatedAllocation, len(receipt.Allocations))
	for index, allocation := range receipt.Allocations {
		copy.Allocations[index] = cloneValidatedAllocation(allocation)
	}
	return &copy
}

func cloneValidatedAllocation(allocation ValidatedAllocation) ValidatedAllocation {
	copy := allocation
	if allocation.Amount.Value != nil {
		copy.Amount = *allocation.Amount.Clone()
	}
	copy.StateData = append([]byte(nil), allocation.StateData...)
	copy.SealDisclosure = append([]byte(nil), allocation.SealDisclosure...)
	copy.CarrierInternalKey = append([]byte(nil), allocation.CarrierInternalKey...)
	copy.TapretRoot = append([]byte(nil), allocation.TapretRoot...)
	copy.TapretProof = append([]byte(nil), allocation.TapretProof...)
	return copy
}

func receiptAllocationsHaveAmounts(receipt *ValidationReceipt) bool {
	if receipt == nil {
		return false
	}
	for _, allocation := range receipt.Allocations {
		if allocation.Amount.Value == nil {
			return false
		}
	}
	return true
}
