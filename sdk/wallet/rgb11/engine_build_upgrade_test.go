package rgb11wallet

import (
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/rgb11/schemas"
)

func TestRecoveryRequiresCurrentEngineBuild(t *testing.T) {
	for _, build := range []string{RGB11EngineBuildID, "rgb11-go-0.11.1-rc.11+sat20.1", "rgb11-go-unknown"} {
		t.Run(build, func(t *testing.T) {
			stable := &RecoveryPackage{Version: RecoveryPackageVersion, WalletID: "current-engine-wallet", EngineBuildID: build}
			encoded, stableErr := EncodeRecoveryPackage(stable)
			if build != RGB11EngineBuildID {
				if stableErr == nil {
					t.Fatal("an obsolete or unknown engine build was accepted")
				}
				return
			}
			if stableErr != nil {
				t.Fatalf("current engine rejected: %v", stableErr)
			}
			if _, err := DecodeRecoveryPackage(encoded); err != nil {
				t.Fatal(err)
			}
		})
	}
	// Stored receipts are a live projection authority, not just backup data.
	// Seed the real store directly to represent a receipt persisted by an old
	// engine, then exercise its read and projection-consumption boundaries.
	for _, build := range []string{RGB11EngineBuildID, "rgb11-go-0.11.1+sat20.1", "rgb11-go-0.11.1-rc.11+sat20.1", "rgb11-go-unknown"} {
		t.Run("stored-receipt/"+build, func(t *testing.T) {
			db := indexerdb.NewKVDB(t.TempDir())
			defer db.Close()
			store := NewProjectionStore(db, &recordingLocker{})
			if err := store.SetScope("engine-build-check"); err != nil {
				t.Fatal(err)
			}
			contract := "rgb:Ar4ouaLv-b7f7Dc_-z5EMvtu-FA5KNh1-nlae~jk-8xMBo7E"
			name, err := NewContractAssetKey(contract, indexer.ASSET_TYPE_FT)
			if err != nil {
				t.Fatal(err)
			}
			schema, err := schemas.ByKind(schemas.NIA)
			if err != nil {
				t.Fatal(err)
			}
			amount := indexer.NewDefaultDecimal(5)
			point := strings.Repeat("1", 64) + ":0"
			receipt := &ValidationReceipt{Version: 1, EngineBuildID: build,
				ConsignmentHash: strings.Repeat("a", 64), ContractID: contract, SchemaID: schema.SchemaID,
				Status: "valid", ValidatedAt: 1,
				Allocations: []ValidatedAllocation{{OutPoint: point, AssetName: name, Amount: *amount,
					OperationID: "operation", AssignmentType: 4000, StateClass: "fungible", SealDisclosure: []byte{1}}}}
			encoded, err := encode(receipt)
			if err != nil {
				t.Fatal(err)
			}
			key, err := store.validationKey(receipt.ConsignmentHash)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Write(key, encoded); err != nil {
				t.Fatal(err)
			}
			_, loadErr := store.LoadValidationReceipt(receipt.ConsignmentHash)
			_, catalogErr := store.ListKnownAssetNames()
			hash, err := receipt.Hash()
			if err != nil {
				t.Fatal(err)
			}
			output := indexer.NewTxOutput(1000)
			output.OutPointStr = point
			proof := &AllocationProof{OutPoint: point, AssetName: name, OperationID: "operation",
				AssignmentType: 4000, StateClass: "fungible", SealDisclosure: []byte{1}, Status: "valid",
				ConsignmentHash: receipt.ConsignmentHash, ValidationHash: hash}
			stageErr := store.StageProjections([]ProjectionReplacement{{Output: output,
				Asset: &indexer.AssetInfo{Name: name, Amount: *amount}, Proof: proof}})
			if build == RGB11EngineBuildID {
				if loadErr != nil || catalogErr != nil || stageErr != nil {
					t.Fatalf("current receipt rejected: load=%v catalog=%v stage=%v", loadErr, catalogErr, stageErr)
				}
			} else if loadErr == nil || catalogErr == nil || stageErr == nil {
				t.Fatalf("stale receipt remains usable: load=%v catalog=%v stage=%v", loadErr, catalogErr, stageErr)
			}
		})
	}
}
