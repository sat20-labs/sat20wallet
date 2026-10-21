package rgb11wallet

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/rgb11/invoicing"
	"github.com/sat20-labs/rgb11/seals"
	corewallet "github.com/sat20-labs/rgb11/wallet"
)

func TestR1SelectedExpiredBlindCleanupGuards(t *testing.T) {
	db := indexerdb.NewKVDB(t.TempDir())
	defer db.Close()
	engineStore := NewEngineStore(db)
	if err := engineStore.SetScope("r1-expiry"); err != nil {
		t.Fatal(err)
	}
	projection := NewProjectionStore(db, nil)
	if err := projection.SetScope("r1-expiry"); err != nil {
		t.Fatal(err)
	}
	engine, err := corewallet.NewEngine(engineStore)
	if err != nil {
		t.Fatal(err)
	}
	txid := [32]byte{1}
	seal := seals.GraphBlindSeal{TxID: &txid, Vout: 2, Blinding: 77}
	req, err := engine.CreateReceive(corewallet.ReceiveParams{Mode: corewallet.ReceiveBlind, BlindSeal: &seal, Network: invoicing.BitcoinTestnet4, RecipientID: "test-recipient", Expiry: time.Now().Unix() + 2, StandardOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	point := wire.OutPoint{Hash: chainhash.Hash(txid), Index: 2}.String()
	if err = projection.SaveReceiveReservation(&ReceiveReservation{Version: 1, RequestID: req.RequestID, OutPoint: point, ReservationID: "exact-owner", Expiry: req.Expiry}); err != nil {
		t.Fatal(err)
	}
	records, err := projection.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	engineRecords, err := engineStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	base := &RGB11WalletSnapshot{Version: WalletSnapshotVersion, WalletID: "r1-expiry-wallet", EngineBuildID: NativeEngineBuildID, ProjectionRecords: records, EngineRecords: engineRecords}
	pkg, err := RecoveryPackageFromSnapshot(base, 0)
	if err != nil {
		t.Fatal(err)
	}
	current, err := pkg.WalletSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func(src *RGB11WalletSnapshot) *RGB11WalletSnapshot {
		v := *src
		v.ProjectionRecords, err = RemoveProjectionSnapshotKeys(src.ProjectionRecords, "receive-reservation-"+req.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		return &v
	}
	selected := cleanup(current)
	if _, err = MergeSelectedRecoverySnapshot(current, selected); err == nil {
		t.Fatal("not-yet-expired reservation was omitted")
	}
	for time.Now().Unix() <= req.Expiry {
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = MergeSelectedRecoverySnapshot(current, selected); err != nil {
		t.Fatalf("expired unused exact blind reservation must be removable: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*RGB11WalletSnapshot, *RGB11WalletSnapshot)
	}{
		{"ACK", func(a, b *RGB11WalletSnapshot) {
			request := *req
			request.Status = corewallet.ReceiveAcknowledged
			a.EngineRecords, _ = UpsertEngineSnapshotReceive(a.EngineRecords, &request)
		}},
		{"bound transfer", func(a, b *RGB11WalletSnapshot) {
			request := *req
			request.TransferID = "bound"
			a.EngineRecords, _ = UpsertEngineSnapshotReceive(a.EngineRecords, &request)
		}},
		{"changed seal", func(a, b *RGB11WalletSnapshot) {
			request := *req
			request.Seal.Blinding++
			encoded, err := corewallet.EncodeReceiveRequest(&request)
			if err != nil {
				t.Fatal(err)
			}
			b.EngineRecords = []SnapshotRecord{{Key: "wallet/receive/" + request.RequestID, Value: encoded}}
		}},
		{"prepared responsibility", func(a, b *RGB11WalletSnapshot) {
			a.ProjectionRecords, _ = UpsertProjectionSnapshotPreparedReceive(a.ProjectionRecords, "prepared-owner", req.RequestID)
			b.ProjectionRecords, _ = UpsertProjectionSnapshotPreparedReceive(b.ProjectionRecords, "prepared-owner", req.RequestID)
		}},
		{"transfer responsibility", func(a, b *RGB11WalletSnapshot) {
			state := &TransferState{TransferID: "linked", Direction: "receive", Status: "awaiting_broadcast", ReceiveRequestID: req.RequestID, Invoice: req.Invoice}
			a.ProjectionRecords, _ = UpsertProjectionSnapshotTransfer(a.ProjectionRecords, state)
			b.ProjectionRecords, _ = UpsertProjectionSnapshotTransfer(b.ProjectionRecords, state)
		}},
		{"reservation expiry differs", func(a, b *RGB11WalletSnapshot) {
			value, err := encode(&ReceiveReservation{Version: 1, RequestID: req.RequestID, OutPoint: point, ReservationID: "exact-owner", Expiry: req.Expiry - 1})
			if err != nil {
				t.Fatal(err)
			}
			a.ProjectionRecords = []SnapshotRecord{{Key: "receive-reservation-" + req.RequestID, Value: value}}
		}},
		{"reservation outpoint differs", func(a, b *RGB11WalletSnapshot) {
			value, err := encode(&ReceiveReservation{Version: 1, RequestID: req.RequestID, OutPoint: fmt.Sprintf("%s:3", chainhash.Hash(txid).String()), ReservationID: "exact-owner", Expiry: req.Expiry})
			if err != nil {
				t.Fatal(err)
			}
			a.ProjectionRecords = []SnapshotRecord{{Key: "receive-reservation-" + req.RequestID, Value: value}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := *current, *selected
			tc.mutate(&a, &b)
			before := append([]SnapshotRecord(nil), a.ProjectionRecords...)
			if _, err := MergeSelectedRecoverySnapshot(&a, &b); err == nil {
				t.Fatal("unsafe expiry cleanup accepted")
			}
			if !reflect.DeepEqual(before, a.ProjectionRecords) {
				t.Fatal("merge failure mutated input")
			}
		})
	}
}

func TestR1SelectedReceiptExtensionGuards(t *testing.T) {
	asset := indexer.AssetName{Protocol: Protocol, Type: indexer.ASSET_TYPE_FT, Ticker: "extension"}
	first := ValidatedAllocation{OutPoint: "1111111111111111111111111111111111111111111111111111111111111111:0", AssetName: asset, Amount: *indexer.NewDefaultDecimal(10), OperationID: "op-a", AssignmentType: 4000, StateClass: "fungible", SealDisclosure: []byte{1}, WitnessTxID: "witness-a"}
	second := cloneValidatedAllocation(first)
	second.OperationID = "op-b"
	second.OutPoint = "2222222222222222222222222222222222222222222222222222222222222222:1"
	second.SealDisclosure = []byte{2}
	base := &ValidationReceipt{Version: 1, EngineBuildID: NativeEngineBuildID, ConsignmentHash: "raw", ContractID: "contract", SchemaID: "schema", TransferID: "canonical", Status: "valid", ValidatedAt: 1, Allocations: []ValidatedAllocation{first}}
	record := func(receipt *ValidationReceipt) SnapshotRecord {
		t.Helper()
		receipt.StateHash, _ = hashAllocations(receipt.ContractID, receipt.SchemaID, receipt.Allocations)
		value, err := encode(receipt)
		if err != nil {
			t.Fatal(err)
		}
		return SnapshotRecord{Key: "validation-raw", Value: value}
	}
	left := record(cloneValidationReceipt(base))
	expanded := cloneValidationReceipt(base)
	expanded.Allocations = append(expanded.Allocations, second)
	if err := compatibleSelectedRecoveryRecord(left, record(expanded)); err != nil {
		t.Fatalf("structural extension rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ValidationReceipt)
	}{
		{"delete old", func(r *ValidationReceipt) { r.Allocations = r.Allocations[1:] }},
		{"amount", func(r *ValidationReceipt) { r.Allocations[0].Amount = *indexer.NewDefaultDecimal(11) }},
		{"seal", func(r *ValidationReceipt) { r.Allocations[0].SealDisclosure = []byte{9} }},
		{"operation", func(r *ValidationReceipt) { r.Allocations[0].OperationID = "changed" }},
		{"assignment index", func(r *ValidationReceipt) { r.Allocations[0].AssignmentIndex++ }},
		{"witness pointer", func(r *ValidationReceipt) { r.Allocations[0].WitnessTxPtr = !r.Allocations[0].WitnessTxPtr }},
		{"contract", func(r *ValidationReceipt) { r.ContractID = "other" }},
		{"canonical transfer", func(r *ValidationReceipt) { r.TransferID = "other" }},
		{"duplicate", func(r *ValidationReceipt) { r.Allocations = append(r.Allocations, r.Allocations[0]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := cloneValidationReceipt(expanded)
			tc.mutate(changed)
			if err := compatibleSelectedRecoveryRecord(left, record(changed)); err == nil {
				t.Fatal("changed immutable receipt facts accepted")
			}
		})
	}
}
