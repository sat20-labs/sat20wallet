package rgb11wallet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
)

type testEvidence struct{}

func (testEvidence) GetUTXO(string) (*BitcoinUTXO, error)         { return nil, nil }
func (testEvidence) GetRawTx(string) ([]byte, error)              { return nil, nil }
func (testEvidence) GetTxStatus(string) (*BitcoinTxStatus, error) { return nil, nil }
func (testEvidence) GetOutspend(string) (*BitcoinOutspend, error) { return nil, nil }
func (testEvidence) GetTip() (*BitcoinTip, error)                 { return nil, nil }
func (testEvidence) Broadcast([]byte) (string, error)             { return "", nil }

type testValidator struct{ receipt *ValidationReceipt }

func (v testValidator) ValidateConsignment(context.Context, []byte, BitcoinEvidenceProvider) (*ValidationReceipt, error) {
	copy := *v.receipt
	copy.Allocations = append([]ValidatedAllocation(nil), v.receipt.Allocations...)
	return &copy, nil
}

type recordingLocker struct {
	utxo   string
	reason string
}

func (l *recordingLocker) LockUtxo(utxo, reason string) error {
	l.utxo, l.reason = utxo, reason
	return nil
}

func TestProjectionAndProofAreStoredTogether(t *testing.T) {
	db := indexerdb.NewKVDB(t.TempDir())
	defer db.Close()
	locker := &recordingLocker{}
	store := NewProjectionStore(db, locker)
	if err := store.SetScope("wallet-1-account-0"); err != nil {
		t.Fatal(err)
	}
	official := "rgb:Ar4ouaLv-b7f7Dc_-z5EMvtu-FA5KNh1-nlae~jk-8xMBo7E"
	amount, err := indexer.NewDecimalFromString("42.50", 2)
	if err != nil {
		t.Fatal(err)
	}
	asset, err := NewAssetInfo(official, indexer.ASSET_TYPE_FT, amount)
	if err != nil {
		t.Fatal(err)
	}
	output := indexer.NewTxOutput(1000)
	output.OutPointStr = "0000000000000000000000000000000000000000000000000000000000000001:0"
	raw := []byte("consensus-validated-consignment")
	objectHash := sha256.Sum256(raw)
	receipt, err := store.ValidateAndStoreConsignment(context.Background(), testValidator{receipt: &ValidationReceipt{
		Version: 1, EngineBuildID: "test-engine", ConsignmentHash: hex.EncodeToString(objectHash[:]),
		ContractID: official, SchemaID: "schema", StateHash: [32]byte{1}, Status: "valid",
		Allocations: []ValidatedAllocation{{OutPoint: output.OutPointStr, AssetName: asset.Name, Amount: *amount.Clone(), OperationID: "op", AssignmentType: 4000, StateClass: "fungible", SealDisclosure: []byte{1}}},
	}}, testEvidence{}, raw)
	if err != nil {
		t.Fatal(err)
	}
	receiptHash, err := receipt.Hash()
	if err != nil {
		t.Fatal(err)
	}
	proof := &AllocationProof{OutPoint: output.OutPointStr, AssetName: asset.Name, OperationID: "op", AssignmentType: 4000, StateClass: "fungible", SealDisclosure: []byte{1}, Status: "valid", ConsignmentHash: receipt.ConsignmentHash, ValidationHash: receiptHash}
	if err := store.CommitProjection(output, asset, proof); err != nil {
		t.Fatal(err)
	}
	if locker.utxo != output.OutPointStr || locker.reason != LockReasonRGB {
		t.Fatalf("carrier not locked: %+v", locker)
	}
	if err := store.AssertConsistent(output.OutPointStr, asset.Name); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadOutput(output.OutPointStr)
	if err != nil {
		t.Fatal(err)
	}
	projected := loaded.GetAsset(&asset.Name)
	if projected == nil || projected.Cmp(amount) != 0 {
		t.Fatalf("bad projected amount %v", projected)
	}
	balance, err := store.Balance(asset.Name)
	if err != nil || balance.Cmp(amount) != 0 {
		t.Fatalf("bad rebuilt balance %v err=%v", balance, err)
	}

	snapshot, err := store.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	restoredDB := indexerdb.NewKVDB(t.TempDir())
	defer restoredDB.Close()
	restored := NewProjectionStore(restoredDB, &recordingLocker{})
	if err := restored.SetScope("different-local-wallet-id-account-0"); err != nil {
		t.Fatal(err)
	}
	if err := restored.ImportSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := restored.AssertConsistent(output.OutPointStr, asset.Name); err != nil {
		t.Fatalf("restored projection is inconsistent: %v", err)
	}
	restoredBalance, err := restored.Balance(asset.Name)
	if err != nil || restoredBalance.Cmp(amount) != 0 {
		t.Fatalf("restored balance %v err=%v", restoredBalance, err)
	}
}

func TestLocalMetadataIsScopedAndExcludedFromPortableSnapshot(t *testing.T) {
	db := indexerdb.NewKVDB(t.TempDir())
	defer db.Close()
	store := NewProjectionStore(db, nil)
	if err := store.SetScope("wallet-1-account-0"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLocalMetadata("wallet-head", []byte("head-7")); err != nil {
		t.Fatal(err)
	}
	value, err := store.LoadLocalMetadata("wallet-head")
	if err != nil || string(value) != "head-7" {
		t.Fatalf("load local metadata: %q err=%v", value, err)
	}
	records, err := store.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Key == "wallet-head" {
			t.Fatal("device-local wallet head leaked into portable RGB state")
		}
	}
	if err := store.SetScope("wallet-2-account-0"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadLocalMetadata("wallet-head"); err == nil {
		t.Fatal("wallet head leaked across local wallet scopes")
	}
}

func TestSettledBatchDropsOnlySenderDeliveryConsignment(t *testing.T) {
	db := indexerdb.NewKVDB(t.TempDir())
	defer db.Close()
	store := NewProjectionStore(db, nil)
	if err := store.SetScope("wallet-batch-account-0"); err != nil {
		t.Fatal(err)
	}
	recipient := []byte("shared-recipient-consignment")
	recipientDigest := sha256.Sum256(recipient)
	recipientHash := hex.EncodeToString(recipientDigest[:])
	ids := []string{"recipient-1", "recipient-2"}
	pending := make([]*PendingTransfer, 0, len(ids))
	for _, id := range ids {
		pending = append(pending, &PendingTransfer{
			State: TransferState{
				TransferID: id, Direction: "send", Status: "pending", ConsignmentHash: recipientHash,
				BatchID: "batch", BatchSize: len(ids), BatchTransferIDs: append([]string(nil), ids...),
			},
			RecipientConsignment: append([]byte(nil), recipient...),
			LocalConsignment:     []byte("wallet-change-consignment"),
			SignedTx:             []byte("signed-tx"), SignedPSBT: []byte("signed-psbt"),
		})
	}
	if err := store.SavePendingTransfers(pending); err != nil {
		t.Fatal(err)
	}
	if err := store.CompactSettledRecipientConsignments(ids); err != nil {
		t.Fatal(err)
	}
	stillPending, err := store.LoadPendingTransfer(ids[0])
	if err != nil || len(stillPending.RecipientConsignment) == 0 {
		t.Fatalf("unsettled delivery copy was removed: pending=%+v err=%v", stillPending, err)
	}
	for _, item := range pending {
		item.State.Status = "settled"
	}
	if err := store.SavePendingTransferStates(pending); err != nil {
		t.Fatal(err)
	}
	if err := store.CompactSettledRecipientConsignments(ids); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		compacted, err := store.LoadPendingTransfer(id)
		if err != nil || len(compacted.RecipientConsignment) != 0 || len(compacted.LocalConsignment) == 0 {
			t.Fatalf("compacted transfer %s=%+v err=%v", id, compacted, err)
		}
	}
	if _, err := store.LoadObject(recipientHash); err == nil {
		t.Fatal("sender delivery object remained after all recipients settled")
	}
}
