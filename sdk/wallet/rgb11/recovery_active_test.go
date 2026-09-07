package rgb11wallet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
)

func TestRecoverySeparatesStableOwnershipFromActiveTransition(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	defer database.Close()
	store := NewProjectionStore(database, &recordingLocker{})
	if err := store.SetScope("active-recovery-source"); err != nil {
		t.Fatal(err)
	}
	name := indexer.AssetName{Protocol: Protocol, Type: indexer.ASSET_TYPE_FT, Ticker: "active"}
	amount := indexer.NewDefaultDecimal(25)
	outpoint := strings.Repeat("11", 32) + ":0"
	output := indexer.NewTxOutput(1_000)
	output.OutPointStr = outpoint
	output.OutValue.PkScript = []byte{0x51}
	raw := []byte("active recovery validation object")
	digest := sha256.Sum256(raw)
	receipt, err := store.ValidateAndStoreConsignment(context.Background(), testValidator{receipt: &ValidationReceipt{
		Version: 1, EngineBuildID: NativeEngineBuildID,
		ConsignmentHash: hex.EncodeToString(digest[:]), ContractID: "active-contract",
		SchemaID: "active-schema", StateHash: [32]byte{1}, Status: "valid",
		Allocations: []ValidatedAllocation{{
			OutPoint: outpoint, AssetName: name, Amount: *amount.Clone(),
			OperationID: "active-operation", AssignmentType: 4000,
			StateClass: "fungible", SealDisclosure: []byte{1},
		}},
	}}, testEvidence{}, raw)
	if err != nil {
		t.Fatal(err)
	}
	receiptHash, err := receipt.Hash()
	if err != nil {
		t.Fatal(err)
	}
	proof := &AllocationProof{
		OutPoint: outpoint, AssetName: name, OperationID: "active-operation",
		AssignmentType: 4000, StateClass: "fungible", SealDisclosure: []byte{1},
		ConsignmentHash: receipt.ConsignmentHash, ValidationHash: receiptHash,
		WitnessTxID: strings.Repeat("22", 32), Status: "settled", Confirmations: 6,
	}
	if err := store.CommitProjection(output, &indexer.AssetInfo{
		Name: name, Amount: *amount.Clone(),
	}, proof); err != nil {
		t.Fatal(err)
	}
	proof.Status = "spending"
	if err := store.SaveProofState(proof); err != nil {
		t.Fatal(err)
	}
	recipient := []byte("active recipient consignment")
	recipientHash := sha256.Sum256(recipient)
	if err := store.SavePendingTransfer(&PendingTransfer{
		State: TransferState{
			TransferID: "active-transfer", Direction: "send", Status: "prepared",
			InputOutPoints: []string{outpoint}, WitnessTxID: strings.Repeat("33", 32),
			ConsignmentHash: hex.EncodeToString(recipientHash[:]),
		},
		RecipientConsignment: recipient, LocalConsignment: []byte("active local consignment"),
		SignedTx: []byte("signed transaction"), SignedPSBT: []byte("signed psbt"),
	}); err != nil {
		t.Fatal(err)
	}
	records, err := store.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: "active-wallet",
		EngineBuildID: NativeEngineBuildID, ProjectionRecords: records,
	}

	stable, err := RecoveryPackageFromSnapshot(snapshot, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stable.ProjectionRecords) != 0 {
		t.Fatalf("unfinished spending proof leaked into stable recovery: %+v", stable.ProjectionRecords)
	}

	active, err := ActiveRecoveryPackageFromSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !ActiveRecoveryPackageHasTransition(active) {
		t.Fatal("active transition was not detected")
	}
	if err := ValidateRecoveryPackage(&RecoveryPackage{
		Version: RecoveryPackageVersion, WalletID: snapshot.WalletID,
		EngineBuildID:     NativeEngineBuildID,
		ProjectionRecords: cloneRecoveryRecords(active.ProjectionRecords),
	}); err == nil {
		t.Fatal("durable recovery accepted active transition records")
	}
	encoded, err := EncodeActiveRecoveryPackage(active)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeActiveRecoveryPackage(encoded)
	if err != nil {
		t.Fatal(err)
	}
	restoredSnapshot, err := decoded.WalletSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	targetDB := indexerdb.NewKVDB(t.TempDir())
	defer targetDB.Close()
	target := NewProjectionStore(targetDB, nil)
	if err := target.SetScope("active-recovery-target"); err != nil {
		t.Fatal(err)
	}
	if err := target.ImportSnapshot(restoredSnapshot.ProjectionRecords); err != nil {
		t.Fatal(err)
	}
	pending, err := target.LoadPendingTransfer("active-transfer")
	if err != nil {
		t.Fatal(err)
	}
	if string(pending.SignedTx) != "signed transaction" ||
		string(pending.RecipientConsignment) != string(recipient) ||
		string(pending.LocalConsignment) != "active local consignment" {
		t.Fatalf("active transition did not round-trip: %+v", pending)
	}
}
