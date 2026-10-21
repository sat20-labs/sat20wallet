package rgb11wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/invoicing"
	corewallet "github.com/sat20-labs/rgb11/wallet"
)

func TestStableRecoveryContainsInvoiceAndPendingOperationClosure(t *testing.T) {
	sourceDB := indexerdb.NewKVDB(t.TempDir())
	defer sourceDB.Close()
	projection := NewProjectionStore(sourceDB, nil)
	engineStore := NewEngineStore(sourceDB)
	const sourceScope = "recovery-closure-source"
	if err := projection.SetScope(sourceScope); err != nil {
		t.Fatal(err)
	}
	if err := engineStore.SetScope(sourceScope); err != nil {
		t.Fatal(err)
	}
	engine, err := corewallet.NewEngine(engineStore)
	if err != nil {
		t.Fatal(err)
	}
	witnessScript := append([]byte{0x00, 0x20}, make([]byte, 32)...)
	for index := 2; index < len(witnessScript); index++ {
		witnessScript[index] = byte(index)
	}
	request, err := engine.CreateReceive(corewallet.ReceiveParams{
		Mode: corewallet.ReceiveWitness, Network: invoicing.BitcoinTestnet4,
		RecipientID: "recipient", WitnessVout: 1, WitnessScript: witnessScript,
		Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := projection.SaveReceiveKey(&ReceiveKey{
		Version: 1, RequestID: request.RequestID, ScopeIndex: 0, Change: 1, Index: 7,
		LogicalAddress: "tb1ptest-recovery", WitnessScript: append([]byte(nil), request.WitnessScript...),
		InternalPubKey: make([]byte, 32),
	}); err != nil {
		t.Fatal(err)
	}
	const receiveTransferID = "invoice-before-consignment"
	if err := projection.SavePreparedReceive(receiveTransferID, request.RequestID); err != nil {
		t.Fatal(err)
	}
	if err := projection.SaveReceiveReservation(&ReceiveReservation{
		Version: 1, RequestID: request.RequestID,
		OutPoint:      strings.Repeat("11", 32) + ":0",
		ReservationID: "receive-input-owner", Expiry: request.Expiry,
	}); err != nil {
		t.Fatal(err)
	}
	if err := projection.SaveTransferState(&TransferState{
		TransferID: receiveTransferID, Direction: "receive", Status: "awaiting_broadcast",
		ReceiveRequestID: request.RequestID, Invoice: request.Invoice,
	}); err != nil {
		t.Fatal(err)
	}

	recipientConsignment := []byte("recipient-consignment")
	recipientHash := sha256.Sum256(recipientConsignment)
	if err := projection.SavePendingTransfer(&PendingTransfer{
		State: TransferState{
			TransferID: "send-before-broadcast", Direction: "send", Status: "prepared",
			InputOutPoints:  []string{strings.Repeat("22", 32) + ":1"},
			ConsignmentHash: hex.EncodeToString(recipientHash[:]),
		},
		RecipientConsignment: recipientConsignment,
		LocalConsignment:     []byte("consumer-change-consignment"),
		SignedTx:             []byte("signed-transaction"),
		SignedPSBT:           []byte("signed-psbt"),
		ReservationID:        "send-input-owner",
	}); err != nil {
		t.Fatal(err)
	}

	projectionRecords, err := projection.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	engineRecords, err := engineStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: "recovery-closure-wallet",
		EngineBuildID:     NativeEngineBuildID,
		ProjectionRecords: projectionRecords, EngineRecords: engineRecords,
	}
	recovery, err := RecoveryPackageFromSnapshot(snapshot, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	encoded, rawBytes, err := EncodeRecoveryPackageWithSize(recovery)
	if err != nil {
		t.Fatal(err)
	}
	encodedRaw, err := encodeRecoveryRaw(recovery)
	if err != nil {
		t.Fatal(err)
	}
	if rawBytes != len(encodedRaw) || rawBytes > rgb11RecoveryMaxRawSize {
		t.Fatalf("reported raw recovery bytes=%d, encoded raw=%d, limit=%d",
			rawBytes, len(encodedRaw), rgb11RecoveryMaxRawSize)
	}
	decoded, err := DecodeRecoveryPackage(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := EncodeRecoveryPackage(decoded)
	if err != nil || !bytes.Equal(encoded, reencoded) {
		t.Fatalf("stable recovery codec is not deterministic after decode: err=%v", err)
	}

	restoredSnapshot, err := decoded.WalletSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	wantProjection := map[string]bool{
		"pending-send-before-broadcast":               false,
		"transfer-invoice-before-consignment":         false,
		"prepared-receive-invoice-before-consignment": false,
		"receive-reservation-" + request.RequestID:    false,
		"receive-key-": false,
	}
	for _, record := range restoredSnapshot.ProjectionRecords {
		if _, ok := wantProjection[record.Key]; ok {
			wantProjection[record.Key] = true
		}
		if strings.HasPrefix(record.Key, "receive-key-") {
			wantProjection["receive-key-"] = true
			var recoveredKey ReceiveKey
			if decode(record.Value, &recoveredKey) != nil || recoveredKey.RequestID != request.RequestID ||
				recoveredKey.Change != 1 || recoveredKey.Index != 7 ||
				!bytes.Equal(recoveredKey.WitnessScript, request.WitnessScript) {
				t.Fatal("stable recovery package changed or omitted the invoice receive-key derivation")
			}
		}
	}
	for key, found := range wantProjection {
		if !found {
			t.Errorf("stable recovery package omitted required record %q", key)
		}
	}
	if len(restoredSnapshot.EngineRecords) != 1 {
		t.Fatalf("stable recovery engine records=%d, want invoice reveal/request", len(restoredSnapshot.EngineRecords))
	}

	targetDB := indexerdb.NewKVDB(t.TempDir())
	defer targetDB.Close()
	targetProjection := NewProjectionStore(targetDB, nil)
	targetEngineStore := NewEngineStore(targetDB)
	const targetScope = "recovery-closure-target"
	if err := targetProjection.SetScope(targetScope); err != nil {
		t.Fatal(err)
	}
	if err := targetEngineStore.SetScope(targetScope); err != nil {
		t.Fatal(err)
	}
	if err := targetProjection.ImportSnapshot(restoredSnapshot.ProjectionRecords); err != nil {
		t.Fatal(err)
	}
	if err := targetEngineStore.ImportSnapshot(restoredSnapshot.EngineRecords); err != nil {
		t.Fatal(err)
	}
	restoredEngine, err := corewallet.NewEngine(targetEngineStore)
	if err != nil {
		t.Fatal(err)
	}
	restoredRequest, err := restoredEngine.LoadReceive(request.RequestID)
	if err != nil || restoredRequest.Invoice != request.Invoice || restoredRequest.Seal != request.Seal {
		t.Fatalf("cold-restored invoice request=%+v err=%v", restoredRequest, err)
	}
	if _, err := targetProjection.LoadPendingTransfer("send-before-broadcast"); err != nil {
		t.Fatalf("cold-restored pending send is unavailable: %v", err)
	}
	if _, err := targetProjection.LoadReceiveReservation(request.RequestID); err != nil {
		t.Fatalf("cold-restored invoice UTXO reservation is unavailable: %v", err)
	}
	recoveredKey, err := targetProjection.LoadReceiveKey(request.WitnessScript)
	if err != nil || recoveredKey.RequestID != request.RequestID || recoveredKey.Change != 1 || recoveredKey.Index != 7 {
		t.Fatalf("cold-restored invoice receive-key derivation is unavailable or changed: err=%v", err)
	}
}

func TestRecoveryPreservesZeroBalanceContractAlias(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	defer database.Close()
	store := NewProjectionStore(database, nil)
	if err := store.SetScope("recovery-zero-balance-contract"); err != nil {
		t.Fatal(err)
	}
	const contractID = "rgb:Ar4ouaLv-b7f7Dc_-z5EMvtu-FA5KNh1-nlae~jk-8xMBo7E"
	name, err := NewContractAssetKey(contractID, indexer.ASSET_TYPE_FT)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("zero-balance-contract-consignment")
	digest := sha256.Sum256(raw)
	consignmentHash := hex.EncodeToString(digest[:])
	_, err = store.ValidateAndStoreConsignment(context.Background(), testValidator{receipt: &ValidationReceipt{
		Version: 1, EngineBuildID: NativeEngineBuildID,
		ConsignmentHash: consignmentHash, ContractID: contractID,
		SchemaID: "zero-balance-schema", StateHash: [32]byte{1}, Status: "valid",
		Allocations: []ValidatedAllocation{{
			OutPoint: strings.Repeat("33", 32) + ":0", AssetName: name,
			Amount: *indexer.NewDefaultDecimal(7), OperationID: "historical-genesis",
			AssignmentType: 4000, StateClass: "fungible", SealDisclosure: []byte{1},
		}},
	}}, testEvidence{}, raw)
	if err != nil {
		t.Fatal(err)
	}
	const alias = "held-for-reference"
	if err := store.SaveLocalAssetName(contractID, alias); err != nil {
		t.Fatal(err)
	}
	projectionRecords, err := store.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: "zero-balance-wallet",
		EngineBuildID: NativeEngineBuildID, ProjectionRecords: projectionRecords,
	}
	recovery, err := RecoveryPackageFromSnapshot(snapshot, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovery.EngineRecords) != 0 {
		t.Fatalf("zero-balance alias unexpectedly exported engine tasks: %d", len(recovery.EngineRecords))
	}
	seen := make(map[string]bool)
	for _, record := range recovery.ProjectionRecords {
		seen[record.Key] = true
	}
	for _, key := range []string{
		"local-name-" + name.Ticker,
		"validation-" + consignmentHash,
		"object-" + consignmentHash,
	} {
		if !seen[key] {
			t.Fatalf("zero-balance recovery closure omitted %s", key)
		}
	}
	if seen["proof-"+strings.Repeat("33", 32)+":0-"+name.String()] ||
		seen["output-"+strings.Repeat("33", 32)+":0"] {
		t.Fatal("zero-balance contract unexpectedly gained spendable projection data")
	}

	restored, err := recovery.WalletSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	targetDB := indexerdb.NewKVDB(t.TempDir())
	defer targetDB.Close()
	target := NewProjectionStore(targetDB, nil)
	if err := target.SetScope("recovery-zero-balance-target"); err != nil {
		t.Fatal(err)
	}
	if err := target.ImportSnapshot(restored.ProjectionRecords); err != nil {
		t.Fatal(err)
	}
	if got, err := target.LoadLocalAssetName(contractID); err != nil || got != alias {
		t.Fatalf("cold-restored zero-balance alias=%q err=%v", got, err)
	}
}

// A new, valid encoding of the same contract must not displace the receipt
// bound by the existing contract-object reference merely because its hash sorts
// first. An older device still owns that exact recovery responsibility.
func TestRecoveryKeepsReferencedContractValidationWhenAnotherReceiptSortsFirst(t *testing.T) {
	raw, err := os.ReadFile("../../../../rgb11/testvectors/rc11/nia-example.rgba")
	if err != nil {
		t.Fatal(err)
	}
	container, err := coreconsignment.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	file, err := coreconsignment.EncodeFile(container)
	if err != nil {
		t.Fatal(err)
	}
	older, newer := raw, file
	oldHash, newHash := sha256.Sum256(older), sha256.Sum256(newer)
	if bytes.Compare(oldHash[:], newHash[:]) < 0 {
		older, newer, oldHash, newHash = newer, older, newHash, oldHash
	}
	if bytes.Compare(newHash[:], oldHash[:]) >= 0 {
		t.Fatal("fixture encodings need distinct ordered object hashes")
	}
	oldKey := hex.EncodeToString(oldHash[:])
	db := indexerdb.NewKVDB(t.TempDir())
	defer db.Close()
	store := NewProjectionStore(db, nil)
	if err := store.SetScope("referenced-contract-receipt"); err != nil {
		t.Fatal(err)
	}
	first, err := store.ValidateAndStoreConsignment(context.Background(), NewNativeConsensusValidator(), nativeGenesisEvidence{}, older)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveContractObjectReference(first.ContractID, oldKey); err != nil {
		t.Fatal(err)
	}
	snapshot := func() *RGB11WalletSnapshot {
		t.Helper()
		records, err := store.ExportSnapshot()
		if err != nil {
			t.Fatal(err)
		}
		return &RGB11WalletSnapshot{Version: WalletSnapshotVersion, WalletID: "referenced-contract-wallet",
			EngineBuildID: NativeEngineBuildID, ProjectionRecords: records}
	}
	current := snapshot()
	initial, err := RecoveryPackageFromSnapshot(current, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initial.WalletSnapshot(); err != nil {
		t.Fatal(err)
	}
	second, err := store.ValidateAndStoreConsignment(context.Background(), NewNativeConsensusValidator(), nativeGenesisEvidence{}, newer)
	if err != nil {
		t.Fatal(err)
	}
	if first.ContractID != second.ContractID {
		t.Fatal("fixture changed contract identity")
	}
	selected, err := RecoveryPackageFromSnapshot(snapshot(), 0)
	if err != nil {
		t.Fatal(err)
	}
	target, err := selected.WalletSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("referenced_receipt_remains_in_published_closure", func(t *testing.T) {
		found := false
		for _, record := range target.ProjectionRecords {
			if record.Key == "validation-"+oldKey {
				found = true
			}
		}
		if !found {
			t.Fatal("new valid contract encoding displaced the receipt bound by the unchanged contract-object reference")
		}
	})
	t.Run("older_device_can_apply_complete_newer_closure", func(t *testing.T) {
		if _, err := MergeSelectedRecoverySnapshot(current, target); err != nil {
			t.Fatalf("unchanged contract reference cannot adopt a newer valid closure: %v", err)
		}
	})
	t.Run("derived_contract_without_own_receipt_stays_recoverable", func(t *testing.T) {
		// Receiving a transfer derives the full contract object from validated
		// history; that derived object need not have a separate receipt.
		source := snapshot()
		derived := *source
		derived.ProjectionRecords = nil
		for _, record := range source.ProjectionRecords {
			if record.Key != "validation-"+oldKey {
				derived.ProjectionRecords = append(derived.ProjectionRecords, record)
			}
		}
		pkg, err := RecoveryPackageFromSnapshot(&derived, 0)
		if err != nil {
			t.Fatalf("validated history cannot recover its derived contract object: %v", err)
		}
		if _, err := pkg.WalletSnapshot(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestMergeSelectedRecoveryAdoptsCleanTransferPhase(t *testing.T) {
	assetName := indexer.AssetName{Protocol: Protocol, Type: indexer.ASSET_TYPE_FT, Ticker: "phase"}
	raw := []byte("selected transfer immutable consignment")
	digest := sha256.Sum256(raw)
	consignmentHash := hex.EncodeToString(digest[:])
	baseState := TransferState{
		TransferID: "selected-phase", Direction: "send", Status: "prepared",
		Asset:       indexer.AssetInfo{Name: assetName, Amount: *indexer.NewDefaultDecimal(7)},
		RecipientID: "recipient-a", InputOutPoints: []string{strings.Repeat("44", 32) + ":0"},
		ConsignmentHash: consignmentHash, WitnessTxID: strings.Repeat("cd", 32),
	}
	base := &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: "selected-phase-wallet",
		EngineBuildID: NativeEngineBuildID,
		ProjectionRecords: []SnapshotRecord{
			{Key: "object-" + consignmentHash, Value: raw},
			{Key: "transfer-selected-phase", Value: mustEncodeRecoveryTest(t, &baseState)},
		},
	}
	selectedState := baseState
	selectedState.Status, selectedState.AckStatus = "settled", "accepted"
	selected := &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: base.WalletID,
		EngineBuildID: NativeEngineBuildID,
		ProjectionRecords: []SnapshotRecord{
			{Key: "object-" + consignmentHash, Value: raw},
			{Key: "transfer-selected-phase", Value: mustEncodeRecoveryTest(t, &selectedState)},
		},
	}
	merged, err := MergeSelectedRecoverySnapshot(base, selected)
	if err != nil {
		t.Fatal(err)
	}
	var got TransferState
	found := false
	for _, record := range merged.ProjectionRecords {
		if record.Key != "transfer-selected-phase" {
			continue
		}
		if err := decode(record.Value, &got); err != nil {
			t.Fatal(err)
		}
		found = true
	}
	if !found {
		t.Fatal("selected transfer record is missing")
	}
	if got.Status != "settled" || got.AckStatus != "accepted" {
		t.Fatalf("selected account lifecycle was not adopted: status=%q ack=%q", got.Status, got.AckStatus)
	}

	conflicting := *selected
	conflicting.ProjectionRecords = cloneRecoveryRecords(selected.ProjectionRecords)
	var changed TransferState
	for index, record := range conflicting.ProjectionRecords {
		if record.Key != "transfer-selected-phase" {
			continue
		}
		if err := decode(record.Value, &changed); err != nil {
			t.Fatal(err)
		}
		changed.RecipientID = "recipient-b"
		conflicting.ProjectionRecords[index].Value = mustEncodeRecoveryTest(t, &changed)
	}
	if _, err := MergeSelectedRecoverySnapshot(base, &conflicting); err == nil {
		t.Fatal("selected account changed transfer recipient under the same operation identity")
	}
}

func TestSelectedRecoveryCannotClearBoundTransferIdentity(t *testing.T) {
	bound := TransferState{
		TransferID: "identity-bound", Direction: "send", Status: "prepared",
		RecipientID: "recipient-a", ReceiveRequestID: "request-a",
		ConsignmentHash: strings.Repeat("a1", 32), WitnessTxID: strings.Repeat("b2", 32),
		InputOutPoints:  []string{strings.Repeat("c3", 32) + ":0"},
		OutputOutPoints: []string{strings.Repeat("d4", 32) + ":1"},
		Expiry:          1234, MinConfirmations: 2,
	}
	cases := []struct {
		name   string
		mutate func(*TransferState)
	}{
		{name: "recipient", mutate: func(state *TransferState) { state.RecipientID = "" }},
		{name: "receive request", mutate: func(state *TransferState) { state.ReceiveRequestID = "" }},
		{name: "consignment", mutate: func(state *TransferState) { state.ConsignmentHash = "" }},
		{name: "witness transaction", mutate: func(state *TransferState) { state.WitnessTxID = "" }},
		{name: "input set removed", mutate: func(state *TransferState) { state.InputOutPoints = nil }},
		{name: "input changed", mutate: func(state *TransferState) { state.InputOutPoints[0] = strings.Repeat("e5", 32) + ":0" }},
		{name: "output set removed", mutate: func(state *TransferState) { state.OutputOutPoints = nil }},
		{name: "expiry removed", mutate: func(state *TransferState) { state.Expiry = 0 }},
		{name: "minimum confirmations removed", mutate: func(state *TransferState) { state.MinConfirmations = 0 }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			selected := bound
			selected.InputOutPoints = append([]string(nil), bound.InputOutPoints...)
			selected.OutputOutPoints = append([]string(nil), bound.OutputOutPoints...)
			testCase.mutate(&selected)
			if sameTransferIntent(bound, selected) {
				t.Fatal("selected operation silently erased or replaced a bound identity field")
			}
		})
	}

	partial := TransferState{TransferID: bound.TransferID, Direction: bound.Direction}
	completed := bound
	if !sameTransferIntent(partial, completed) {
		t.Fatal("selected complete operation could not add identity fields absent from an older local record")
	}
}

func TestSelectedRecoveryConstrainsPendingPrivateSigningIntent(t *testing.T) {
	unsigned := wire.NewMsgTx(wire.TxVersion)
	var previousHash [32]byte
	previousHash[0] = 0x5a
	unsigned.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: previousHash, Index: 3}, nil, nil))
	unsigned.AddTxOut(wire.NewTxOut(1200, []byte{0x51}))
	var unsignedBytes bytes.Buffer
	if err := unsigned.Serialize(&unsignedBytes); err != nil {
		t.Fatal(err)
	}

	signed := unsigned.Copy()
	signed.TxIn[0].Witness = wire.TxWitness{[]byte{0x30, 0x01}, []byte{0x02, 0x03}}
	var signedBytes bytes.Buffer
	if err := signed.Serialize(&signedBytes); err != nil {
		t.Fatal(err)
	}
	context := &ChannelSendData{
		ChannelID: "channel-a", WitnessScript: []byte{0x51}, PeerPubKey: []byte{0x02},
		Reason: "rgb11", MoreData: []byte{0x01},
	}
	base := PendingTransfer{
		State: TransferState{TransferID: "channel-signing", Direction: "send", Status: "prepared",
			WitnessTxID: unsigned.TxHash().String()},
		SignedTx:   append([]byte(nil), unsignedBytes.Bytes()...),
		SignedPSBT: []byte{0x01}, ChannelSend: context,
	}
	cosigned := base
	cosigned.SignedTx = append([]byte(nil), signedBytes.Bytes()...)
	cosigned.SignedPSBT = append([]byte(nil), base.SignedPSBT...)
	channel := *context
	channel.Signed = true
	cosigned.ChannelSend = &channel
	if !samePendingIntent(base, cosigned) {
		t.Fatal("valid channel cosign transition with the same unsigned transaction was rejected")
	}

	changedChannel := cosigned
	changedChannel.ChannelSend = &channel
	changedChannel.ChannelSend.ChannelID = "channel-b"
	if samePendingIntent(base, changedChannel) {
		t.Fatal("selected operation changed the fixed channel signing identity")
	}

	changedTransaction := cosigned
	changedTransaction.SignedTx = append([]byte(nil), cosigned.SignedTx...)
	changedTransaction.SignedTx[len(changedTransaction.SignedTx)-1] ^= 0x01
	if samePendingIntent(base, changedTransaction) {
		t.Fatal("selected channel cosign changed bytes outside the same unsigned transaction")
	}

	normal := base
	normal.ChannelSend = nil
	changedNormal := normal
	changedNormal.SignedTx = []byte("different signed transaction")
	if samePendingIntent(normal, changedNormal) {
		t.Fatal("selected operation replaced an already bound non-channel signed transaction")
	}
}

func TestSelectedRecoveryOnlyReleasesTerminalReservationOwner(t *testing.T) {
	base := PendingTransfer{
		State:         TransferState{TransferID: "reservation-release", Direction: "send", Status: "broadcast-attempted"},
		ReservationID: "original-owner",
	}
	for _, status := range []string{"broadcast-attempted", "pending"} {
		selected := base
		selected.State.Status = status
		selected.ReservationID = ""
		if samePendingIntent(base, selected) {
			t.Fatalf("selected %q operation silently released an unresolved reservation", status)
		}
	}
	for _, status := range []string{"rejected", "settled", "orphaned"} {
		selected := base
		selected.State.Status = status
		selected.ReservationID = ""
		if !samePendingIntent(base, selected) {
			t.Fatalf("selected terminal %q phase did not permit its existing owner cleanup", status)
		}
	}
}

func TestMergeSelectedRecoveryAcceptsCompactedRejectedSend(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	defer database.Close()
	store := NewProjectionStore(database, nil)
	if err := store.SetScope("selected-rejected-compaction"); err != nil {
		t.Fatal(err)
	}

	const transferID = "cancelled-before-broadcast"
	recipient := []byte("recipient-consignment-before-cancel")
	local := []byte("consumer-consignment-before-cancel")
	recipientDigest := sha256.Sum256(recipient)
	consignmentHash := hex.EncodeToString(recipientDigest[:])
	state := TransferState{
		TransferID: transferID, Direction: "send", Status: "prepared",
		InputOutPoints:  []string{strings.Repeat("33", 32) + ":1"},
		ConsignmentHash: consignmentHash, WitnessTxID: strings.Repeat("44", 32),
	}
	pending := &PendingTransfer{
		State: state, RecipientConsignment: recipient, LocalConsignment: local,
		SignedTx: []byte("prepared-witness-tx"), SignedPSBT: []byte("prepared-psbt"),
		ReservationID: "cancelled-input-owner",
	}
	if err := store.SavePendingTransfer(pending); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTransferState(&state); err != nil {
		t.Fatal(err)
	}

	currentRecords, err := store.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	currentPackage, err := RecoveryPackageFromSnapshot(&RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: "selected-cancel-wallet",
		EngineBuildID: NativeEngineBuildID, ProjectionRecords: currentRecords,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	current, err := currentPackage.WalletSnapshot()
	if err != nil {
		t.Fatal(err)
	}

	pending.State.Status = "rejected"
	state.Status = "rejected"
	if err := store.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTransferState(&state); err != nil {
		t.Fatal(err)
	}
	if err := store.CompactRejectedTransfers([]string{transferID}); err != nil {
		t.Fatal(err)
	}
	selectedRecords, err := store.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	selectedPackage, err := RecoveryPackageFromSnapshot(&RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: current.WalletID,
		EngineBuildID: NativeEngineBuildID, ProjectionRecords: selectedRecords,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := selectedPackage.WalletSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range selected.ProjectionRecords {
		if strings.HasPrefix(record.Key, "object-") {
			t.Fatalf("rejected-send compaction retained object %s", record.Key)
		}
	}

	merged, err := MergeSelectedRecoverySnapshot(current, selected)
	if err != nil {
		t.Fatalf("clean selected cancellation was rejected: %v", err)
	}
	mergedRecords := mapSnapshotRecords(merged.ProjectionRecords)
	var restored PendingTransfer
	if record, ok := mergedRecords["pending-"+transferID]; !ok || decode(record.Value, &restored) != nil ||
		restored.State.Status != "rejected" || restored.RecipientObjectHash != "" ||
		restored.LocalObjectHash != "" || len(restored.SignedTx) != 0 {
		t.Fatalf("selected cancellation did not replace prepared operation: %+v", restored.State)
	}
}

func TestMergeSelectedRecoveryRejectsDroppedLocalPending(t *testing.T) {
	current := &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: "selected-pending-wallet",
		EngineBuildID: NativeEngineBuildID,
		ProjectionRecords: []SnapshotRecord{{
			Key: "pending-local-only",
			Value: mustEncodeRecoveryTest(t, &PendingTransfer{
				State: TransferState{TransferID: "local-only", Direction: "send", Status: "prepared"},
			}),
		}},
	}
	selected := &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: current.WalletID,
		EngineBuildID: NativeEngineBuildID,
	}
	if _, err := MergeSelectedRecoverySnapshot(current, selected); err == nil {
		t.Fatal("selected account silently dropped a locally prepared operation")
	}
}

func TestMergeSelectedRecoveryAcceptsCompletedReceiveReplacingReservation(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	defer database.Close()
	projection := NewProjectionStore(database, nil)
	engineStore := NewEngineStore(database)
	const currentScope = "selected-receive-current"
	if err := projection.SetScope(currentScope); err != nil {
		t.Fatal(err)
	}
	if err := engineStore.SetScope(currentScope); err != nil {
		t.Fatal(err)
	}
	engine, err := corewallet.NewEngine(engineStore)
	if err != nil {
		t.Fatal(err)
	}
	request, err := engine.CreateReceive(corewallet.ReceiveParams{
		Network: invoicing.BitcoinTestnet4, RecipientID: "receive-recipient",
		WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	const transferID = "completed-receive"
	consignment := []byte("completed receive consignment")
	digest := sha256.Sum256(consignment)
	consignmentHash := hex.EncodeToString(digest[:])
	if err := projection.SavePreparedObject(consignmentHash, consignment); err != nil {
		t.Fatal(err)
	}
	if err := projection.SavePreparedReceive(transferID, request.RequestID); err != nil {
		t.Fatal(err)
	}
	if err := projection.SaveReceiveReservation(&ReceiveReservation{
		Version: 1, RequestID: request.RequestID,
		OutPoint: strings.Repeat("55", 32) + ":0", ReservationID: "invoice-reservation",
		Expiry: request.Expiry,
	}); err != nil {
		t.Fatal(err)
	}
	if err := projection.SaveTransferState(&TransferState{
		TransferID: transferID, Direction: "receive", Status: "awaiting_broadcast",
		ReceiveRequestID: request.RequestID, ConsignmentHash: consignmentHash,
	}); err != nil {
		t.Fatal(err)
	}
	currentProjection, err := projection.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	currentEngine, err := engineStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	current := &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: "selected-receive-wallet",
		EngineBuildID: NativeEngineBuildID, ProjectionRecords: currentProjection,
		EngineRecords: currentEngine,
	}

	if err := engine.MarkRelayAcknowledged(request.RequestID, transferID, consignmentHash, strings.Repeat("66", 32)); err != nil {
		t.Fatal(err)
	}
	if err := projection.DeletePreparedReceive(transferID); err != nil {
		t.Fatal(err)
	}
	if err := projection.DeleteReceiveReservation(request.RequestID); err != nil {
		t.Fatal(err)
	}
	if err := projection.SaveTransferState(&TransferState{
		TransferID: transferID, Direction: "receive", Status: "pending",
		ReceiveRequestID: request.RequestID, ConsignmentHash: consignmentHash,
	}); err != nil {
		t.Fatal(err)
	}
	selectedProjection, err := projection.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	selectedEngine, err := engineStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	selected := &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: current.WalletID,
		EngineBuildID: NativeEngineBuildID, ProjectionRecords: selectedProjection,
		EngineRecords: selectedEngine,
	}
	merged, err := MergeSelectedRecoverySnapshot(current, selected)
	if err != nil {
		t.Fatalf("accepted receive did not safely replace prepare/reservation records: %v", err)
	}
	for _, record := range merged.ProjectionRecords {
		if record.Key == "prepared-receive-"+transferID || record.Key == "receive-reservation-"+request.RequestID {
			t.Fatalf("completed receive retained obsolete preparation record %s", record.Key)
		}
	}
}

func mustEncodeRecoveryTest(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := encode(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
