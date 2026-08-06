package rgb11wallet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/rgb11/invoicing"
	corewallet "github.com/sat20-labs/rgb11/wallet"
)

func TestEngineSnapshotRestoresPendingInvoiceAcrossLocalScopes(t *testing.T) {
	sourceDB := indexerdb.NewKVDB(t.TempDir())
	defer sourceDB.Close()
	source := NewEngineStore(sourceDB)
	if err := source.SetScope("wallet-1-account-3"); err != nil {
		t.Fatal(err)
	}
	engine, err := corewallet.NewEngine(source)
	if err != nil {
		t.Fatal(err)
	}
	request, err := engine.CreateReceive(corewallet.ReceiveParams{
		Network: invoicing.BitcoinTestnet3, RecipientID: "recipient",
		WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	records, err := source.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}

	targetDB := indexerdb.NewKVDB(t.TempDir())
	defer targetDB.Close()
	target := NewEngineStore(targetDB)
	if err := target.SetScope("wallet-99-account-3"); err != nil {
		t.Fatal(err)
	}
	if err := target.ImportSnapshot(records); err != nil {
		t.Fatal(err)
	}
	restoredEngine, err := corewallet.NewEngine(target)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := restoredEngine.LoadReceive(request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Invoice != request.Invoice || restored.Seal != request.Seal || restored.RelayKey != request.RelayKey || restored.AckKey != request.AckKey {
		t.Fatalf("restored request differs: %#v", restored)
	}
}

func TestProjectionSnapshotRestoresPreparedReceive(t *testing.T) {
	sourceDB := indexerdb.NewKVDB(t.TempDir())
	defer sourceDB.Close()
	source := NewProjectionStore(sourceDB, nil)
	if err := source.SetScope("wallet-1-account-0"); err != nil {
		t.Fatal(err)
	}
	const transferID = "transfer-before-broadcast"
	requestID := strings.Repeat("01", 32)
	if err := source.SavePreparedReceive(transferID, requestID); err != nil {
		t.Fatal(err)
	}
	records, err := source.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}

	targetDB := indexerdb.NewKVDB(t.TempDir())
	defer targetDB.Close()
	target := NewProjectionStore(targetDB, nil)
	if err := target.SetScope("wallet-2-account-0"); err != nil {
		t.Fatal(err)
	}
	if err := target.ImportSnapshot(records); err != nil {
		t.Fatal(err)
	}
	restored, err := target.LoadPreparedReceive(transferID)
	if err != nil || restored != requestID {
		t.Fatalf("restored request id=%q err=%v", restored, err)
	}
}

func TestProjectionSnapshotRejectsInvalidPreparedReceive(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	defer database.Close()
	store := NewProjectionStore(database, nil)
	if err := store.SetScope("wallet-1-account-0"); err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateSnapshot([]SnapshotRecord{{
		Key: "prepared-receive-transfer-1", Value: []byte("not-a-request-id"),
	}}); err == nil {
		t.Fatal("invalid prepared receive snapshot was accepted")
	}
}

func TestWalletSnapshotRejectsPreparedReceiveWithoutEngineRequest(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	defer database.Close()
	projection := NewProjectionStore(database, nil)
	if err := projection.SetScope("wallet-1-account-0"); err != nil {
		t.Fatal(err)
	}
	requestID := strings.Repeat("01", 32)
	if err := projection.SavePreparedReceive("transfer-1", requestID); err != nil {
		t.Fatal(err)
	}
	records, err := projection.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateWalletSnapshot(&RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: "wallet-1",
		ProjectionRecords: records,
	}); err == nil {
		t.Fatal("wallet snapshot accepted a prepared receive without its engine request")
	}
}

func TestWalletSnapshotRejectsReceiveMetadataWithoutEngineRequest(t *testing.T) {
	requestID := strings.Repeat("01", 32)
	receiveKey, err := encode(&ReceiveKey{
		Version: 1, RequestID: requestID, Change: 1, LogicalAddress: "tb1ptest",
		WitnessScript: []byte{0x51}, InternalPubKey: make([]byte, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := encode(&ReceiveReservation{
		Version: 1, RequestID: requestID, ReservationID: "orphan-receive-owner",
		OutPoint: strings.Repeat("02", 32) + ":0", Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	keyHash := sha256.Sum256([]byte{0x51})
	for _, record := range []SnapshotRecord{
		{Key: "receive-key-" + hex.EncodeToString(keyHash[:]), Value: receiveKey},
		{Key: "receive-reservation-" + requestID, Value: reservation},
	} {
		if err := ValidateWalletSnapshot(&RGB11WalletSnapshot{
			Version: WalletSnapshotVersion, WalletID: "wallet-1",
			ProjectionRecords: []SnapshotRecord{record},
		}); err == nil {
			t.Fatalf("wallet snapshot accepted orphaned %s", record.Key)
		}
	}
}

func TestProjectionSnapshotRejectsRGBAssetWithoutProof(t *testing.T) {
	assetName := indexer.AssetName{Protocol: Protocol, Type: indexer.ASSET_TYPE_FT, Ticker: "unproven"}
	output := indexer.NewTxOutput(1_000)
	output.OutPointStr = strings.Repeat("11", 32) + ":0"
	output.Assets = append(output.Assets, indexer.AssetInfo{
		Name: assetName, Amount: *indexer.NewDefaultDecimal(1), BindingSat: 0,
	})
	encoded, err := encode(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateProjectionSnapshot([]SnapshotRecord{{
		Key: "output-" + output.OutPointStr, Value: encoded,
	}}); err == nil {
		t.Fatal("projection snapshot accepted an RGB asset without an allocation proof")
	}
}

func TestProjectionSnapshotRoundTripsEveryPortableRecordType(t *testing.T) {
	sourceDB := indexerdb.NewKVDB(t.TempDir())
	defer sourceDB.Close()
	source := NewProjectionStore(sourceDB, &recordingLocker{})
	if err := source.SetScope("wallet-all-records-account-0"); err != nil {
		t.Fatal(err)
	}

	assetName := indexer.AssetName{Protocol: Protocol, Type: indexer.ASSET_TYPE_FT, Ticker: "snapshot-asset"}
	amount := indexer.NewDefaultDecimal(25)
	asset := &indexer.AssetInfo{Name: assetName, Amount: *amount, BindingSat: 0}
	output := indexer.NewTxOutput(1_000)
	output.OutPointStr = strings.Repeat("11", 32) + ":0"
	raw := []byte("validated snapshot consignment")
	digest := sha256.Sum256(raw)
	receipt, err := source.ValidateAndStoreConsignment(context.Background(), testValidator{receipt: &ValidationReceipt{
		Version: 1, EngineBuildID: "snapshot-test", ConsignmentHash: hex.EncodeToString(digest[:]),
		ContractID: "rgb:Ar4ouaLv-b7f7Dc_-z5EMvtu-FA5KNh1-nlae~jk-8xMBo7E",
		SchemaID:   "schema", StateHash: [32]byte{1}, Status: "valid",
		Allocations: []ValidatedAllocation{{
			OutPoint: output.OutPointStr, AssetName: assetName, Amount: *amount.Clone(),
			OperationID: "op", AssignmentType: 4000, StateClass: "fungible", SealDisclosure: []byte{1},
		}},
	}}, testEvidence{}, raw)
	if err != nil {
		t.Fatal(err)
	}
	receiptHash, err := receipt.Hash()
	if err != nil {
		t.Fatal(err)
	}
	if err := source.CommitProjection(output, asset, &AllocationProof{
		OutPoint: output.OutPointStr, AssetName: assetName, OperationID: "op",
		AssignmentType: 4000, StateClass: "fungible", SealDisclosure: []byte{1},
		ConsignmentHash: receipt.ConsignmentHash, ValidationHash: receiptHash, Status: "valid",
	}); err != nil {
		t.Fatal(err)
	}

	requestID := strings.Repeat("22", 32)
	if err := source.SaveReceiveKey(&ReceiveKey{
		Version: 1, RequestID: requestID, Change: 1, Index: 7,
		LogicalAddress: "tb1ptest", WitnessScript: []byte{0x51, 0x20, 0x01},
		InternalPubKey: make([]byte, 32),
	}); err != nil {
		t.Fatal(err)
	}
	reservationID := strings.Repeat("33", 32)
	if err := source.SaveReceiveReservation(&ReceiveReservation{
		Version: 1, RequestID: reservationID, ReservationID: "receive-reservation-owner",
		OutPoint: strings.Repeat("44", 32) + ":1", Expiry: time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := source.SavePreparedReceive("prepared-transfer", requestID); err != nil {
		t.Fatal(err)
	}
	if err := source.SaveTransferState(&TransferState{
		TransferID: "received-transfer", Direction: "receive", Status: "pending",
	}); err != nil {
		t.Fatal(err)
	}
	recipient := []byte("recipient consignment")
	recipientHash := sha256.Sum256(recipient)
	if err := source.SavePendingTransfer(&PendingTransfer{
		State: TransferState{
			TransferID: "pending-transfer", Direction: "send", Status: "prepared",
			ConsignmentHash: hex.EncodeToString(recipientHash[:]),
		},
		RecipientConsignment: recipient, LocalConsignment: []byte("local consignment"),
		SignedTx: []byte("signed tx"), SignedPSBT: []byte("signed psbt"),
		ReservationID: "pending-transfer-owner",
	}); err != nil {
		t.Fatal(err)
	}

	records, err := source.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{
		"object-": false, "validation-": false, "output-": false, "proof-": false,
		"pending-": false, "transfer-": false, "prepared-receive-": false,
		"receive-key-": false, "receive-reservation-": false,
	}
	for _, record := range records {
		for prefix := range wanted {
			if strings.HasPrefix(record.Key, prefix) {
				wanted[prefix] = true
			}
		}
	}
	for prefix, found := range wanted {
		if !found {
			t.Fatalf("portable snapshot does not contain %s record", prefix)
		}
	}

	targetDB := indexerdb.NewKVDB(t.TempDir())
	defer targetDB.Close()
	target := NewProjectionStore(targetDB, nil)
	if err := target.SetScope("wallet-restored-account-0"); err != nil {
		t.Fatal(err)
	}
	if err := target.ImportSnapshot(records); err != nil {
		t.Fatal(err)
	}
	restored, err := target.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(records, restored) {
		t.Fatalf("projection snapshot changed after round trip")
	}
	restoredReservation, err := target.LoadReceiveReservation(reservationID)
	if err != nil || restoredReservation.ReservationID != "receive-reservation-owner" {
		t.Fatalf("receive reservation owner=%q err=%v", restoredReservation.ReservationID, err)
	}
	restoredPending, err := target.LoadPendingTransfer("pending-transfer")
	if err != nil || restoredPending.ReservationID != "pending-transfer-owner" {
		t.Fatalf("pending reservation owner=%q err=%v", restoredPending.ReservationID, err)
	}
}

func TestProjectionSnapshotRejectsInvalidReceiveReservation(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	defer database.Close()
	store := NewProjectionStore(database, nil)
	if err := store.SetScope("wallet-1-account-0"); err != nil {
		t.Fatal(err)
	}
	encoded, err := encode(&ReceiveReservation{
		Version: 1, RequestID: strings.Repeat("55", 32), ReservationID: "invalid-key-owner",
		OutPoint: strings.Repeat("66", 32) + ":0", Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateSnapshot([]SnapshotRecord{{
		Key: "receive-reservation-" + strings.Repeat("77", 32), Value: encoded,
	}}); err == nil {
		t.Fatal("receive reservation with mismatched request id was accepted")
	}
}

func TestSnapshotExportRejectsUnknownPortableRecords(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	defer database.Close()
	projection := NewProjectionStore(database, nil)
	if err := projection.SetScope("wallet-1-account-0"); err != nil {
		t.Fatal(err)
	}
	prefix, err := projection.snapshotPrefix()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Write(append(prefix, []byte("unknown-record")...), []byte("value")); err != nil {
		t.Fatal(err)
	}
	if _, err := projection.ExportSnapshot(); err == nil {
		t.Fatal("projection export accepted an unknown portable record")
	}

	engine := NewEngineStore(database)
	if err := engine.SetScope("wallet-1-account-0"); err != nil {
		t.Fatal(err)
	}
	tx, err := engine.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put([]byte("unknown-record"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ExportSnapshot(); err == nil {
		t.Fatal("engine export accepted an unknown portable record")
	}
}
