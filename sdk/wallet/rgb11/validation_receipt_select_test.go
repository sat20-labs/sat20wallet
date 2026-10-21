package rgb11wallet

import (
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
)

func TestSelectHistoricalValidationReceiptKeepsOnlyRequiredCoreAllocations(t *testing.T) {
	asset := indexer.AssetName{Protocol: Protocol, Type: indexer.ASSET_TYPE_FT, Ticker: "history-select"}
	allocation := func(outpoint, operation string, amount int64, disclosure byte) ValidatedAllocation {
		return ValidatedAllocation{
			OutPoint: outpoint, AssetName: asset, Amount: *indexer.NewDefaultDecimal(amount),
			OperationID: operation, AssignmentType: 4000, AssignmentIndex: 0,
			StateClass: "fungible", SealDisclosure: []byte{disclosure},
		}
	}
	first := allocation("1111111111111111111111111111111111111111111111111111111111111111:0", "op-a", 10, 1)
	needed := allocation("2222222222222222222222222222222222222222222222222222222222222222:1", "op-b", 20, 2)
	unneeded := allocation("3333333333333333333333333333333333333333333333333333333333333333:2", "op-c", 30, 3)
	material := &ValidationReceipt{
		Version: 1, EngineBuildID: "core", ConsignmentHash: "raw-hash", ContractID: "contract",
		SchemaID: "schema", TransferID: "transfer", ValidatedAt: 200, Status: "valid",
		Allocations: []ValidatedAllocation{unneeded, needed, first},
	}
	material.StateHash, _ = hashAllocations(material.ContractID, material.SchemaID, material.Allocations)
	base := cloneValidationReceipt(material)
	base.EngineBuildID = "original-engine"
	base.ValidatedAt = 100
	base.Allocations = []ValidatedAllocation{first}
	base.StateHash, _ = hashAllocations(base.ContractID, base.SchemaID, base.Allocations)

	selected, err := SelectHistoricalValidationReceipt(&HistoricalValidation{MaterialReceipt: material}, base,
		[]ValidatedAllocation{needed})
	if err != nil {
		t.Fatalf("select narrow receipt: %v", err)
	}
	if len(selected.Allocations) != 2 || selected.ValidatedAt != base.ValidatedAt ||
		selected.EngineBuildID != base.EngineBuildID {
		t.Fatalf("selection did not retain the original receipt metadata and only the requested allocations: %+v",
			selected)
	}
	if allocationOpout(selected.Allocations[0]) != allocationOpout(first) ||
		allocationOpout(selected.Allocations[1]) != allocationOpout(needed) {
		t.Fatalf("selected allocations are not deterministically ordered by full Opout: %+v", selected.Allocations)
	}
	if _, err := SelectHistoricalValidationReceipt(&HistoricalValidation{MaterialReceipt: material}, base,
		[]ValidatedAllocation{unneeded}); err != nil {
		t.Fatalf("core-validated required allocation was rejected: %v", err)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*ValidatedAllocation)
	}{
		{name: "amount", mutate: func(value *ValidatedAllocation) {
			value.Amount = *indexer.NewDefaultDecimal(21)
		}},
		{name: "disclosure", mutate: func(value *ValidatedAllocation) {
			value.SealDisclosure = []byte{9}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := cloneValidatedAllocation(needed)
			tc.mutate(&changed)
			if _, err := SelectHistoricalValidationReceipt(&HistoricalValidation{MaterialReceipt: material}, base,
				[]ValidatedAllocation{changed}); err == nil {
				t.Fatal("selector accepted an allocation that differs from the core-validated full Opout")
			}
		})
	}

	t.Run("same carrier and asset with distinct full Opout", func(t *testing.T) {
		collision := allocation(first.OutPoint, "op-other", 1, 4)
		collision.AssignmentIndex = 1
		collisionOnly := cloneValidationReceipt(material)
		collisionOnly.Allocations = append(collisionOnly.Allocations, collision)
		collisionOnly.StateHash, _ = hashAllocations(collisionOnly.ContractID, collisionOnly.SchemaID,
			collisionOnly.Allocations)
		if _, err := SelectHistoricalValidationReceipt(&HistoricalValidation{MaterialReceipt: collisionOnly}, base,
			[]ValidatedAllocation{collision}); err == nil {
			t.Fatal("selector accepted two full Opouts sharing one projected outpoint+asset key")
		}
	})

	t.Run("duplicate full Opout in core report", func(t *testing.T) {
		duplicate := cloneValidationReceipt(material)
		duplicate.Allocations = append(duplicate.Allocations, needed)
		duplicate.StateHash, _ = hashAllocations(duplicate.ContractID, duplicate.SchemaID, duplicate.Allocations)
		if _, err := SelectHistoricalValidationReceipt(&HistoricalValidation{MaterialReceipt: duplicate}, base,
			[]ValidatedAllocation{needed}); err == nil {
			t.Fatal("selector accepted duplicate full Opout entries in the core report")
		}
	})
}
