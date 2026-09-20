//go:build rgb11repair

package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/rgb11/seals"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type orphanReceiptEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
}

type orphanReceiptValidator struct {
	receipt rgb11wallet.ValidationReceipt
}

func (v orphanReceiptValidator) ValidateConsignment(context.Context, []byte,
	rgb11wallet.BitcoinEvidenceProvider) (*rgb11wallet.ValidationReceipt, error) {
	receipt := v.receipt
	return &receipt, nil
}

// Build two genuine consignments/transactions against the existing in-memory
// evidence fixture. Confirm each transaction individually before exercising the
// whole-history refresh, so the regression cannot corrupt the setup itself.
func TestRGB11SingleKeyRepairTool(t *testing.T) {
	for _, originalStatus := range []string{"pending"} {
		t.Run(originalStatus, func(t *testing.T) {
			sender, recipient, imported, evidence, rpc := newRGB11GenericSendFixture(t)
			ctx := context.Background()
			makeConfirmed := func(amount string) *rgb11wallet.PendingTransfer {
				t.Helper()
				request, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
					Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID,
					AmountRaw: amount, WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
				})
				if err != nil {
					t.Fatal(err)
				}
				prepared, err := sender.rgbManager.PrepareRGB11Transfer(ctx, RGB11SendRequest{
					Invoice: request.Invoice, FeeRate: 2, MinConfirmations: 1,
				})
				if err != nil {
					t.Fatal(err)
				}
				pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
				if err != nil {
					t.Fatal(err)
				}
				tx := wire.NewMsgTx(wire.TxVersion)
				if err := tx.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
					t.Fatal(err)
				}
				txid := tx.TxHash().String()
				evidence.mu.Lock()
				evidence.rawTx[txid] = append([]byte(nil), pending.SignedTx...)
				for _, input := range tx.TxIn {
					outpoint := input.PreviousOutPoint.String()
					evidence.spendingTx[outpoint] = txid
					delete(evidence.utxos, outpoint)
					delete(rpc.outputs, outpoint)
				}
				rpc.plain = nil
				senderScript, err := AddrToPkScript(sender.wallet.GetAddress(), GetChainParam())
				if err != nil {
					evidence.mu.Unlock()
					t.Fatal(err)
				}
				for vout, output := range tx.TxOut {
					outpoint := fmt.Sprintf("%s:%d", txid, vout)
					evidence.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{OutPoint: outpoint, Value: output.Value, PkScript: output.PkScript, Confirmations: 6}
					if bytes.Equal(output.PkScript, senderScript) {
						item := indexer.NewTxOutput(output.Value)
						item.OutPointStr, item.OutValue.PkScript = outpoint, output.PkScript
						rpc.outputs[outpoint] = item
					}
				}
				for outpoint, output := range rpc.outputs {
					rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{OutPoint: outpoint, Value: output.OutValue.Value, PkScript: output.OutValue.PkScript})
				}
				evidence.mu.Unlock()
				status := &rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 6}
				evidence.statusMu.Lock()
				evidence.statuses[txid] = status
				evidence.statusMu.Unlock()
				if err := sender.rgbManager.applyRGB11LocalChange(ctx, pending, status); err != nil {
					t.Fatal(err)
				}
				pending.State.Status, pending.State.AckStatus = "settled", "accepted-out-of-band"
				if err := sender.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
					t.Fatal(err)
				}
				if err := sender.rgbManager.finalizeRGB11PendingChangeReservation(pending); err != nil {
					t.Fatal(err)
				}
				return pending
			}
			first := makeConfirmed("20000")
			second := makeConfirmed("10000")
			var spentChange string
			knownProofs, err := sender.rgbManager.projectionStore.ListProofs()
			if err != nil {
				t.Fatal(err)
			}
			for _, input := range second.State.InputOutPoints {
				for _, proof := range knownProofs {
					if input == proof.OutPoint && proof.WitnessTxID == first.State.WitnessTxID && proof.AssetName == imported.AssetName {
						spentChange = input
					}
				}
			}
			if spentChange == "" {
				t.Fatal("second transaction did not consume first change")
			}
			if evidence.spendingTx[spentChange] != second.State.WitnessTxID {
				t.Fatal("wrong spending witness")
			}

			// Existing corrupted historical pending records must require the separate
			// targeted repair tool; production refresh must not silently repair them.
			first.State.Status = originalStatus
			if err := sender.rgbManager.projectionStore.SavePendingTransferState(first); err != nil {
				t.Fatal(err)
			}
			records, err := sender.rgbManager.projectionStore.ExportSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			value := rgb11wallet.RepairExport{Identity: "prd|testnet|trusted-root|101|0|trusted-pubkey", Prefix: "rgb11-wallet-101-account-0-rgb11v2-"}
			for _, r := range records {
				for _, kind := range []string{"pending-", "transfer-", "proof-", "validation-", "object-"} {
					if strings.HasPrefix(r.Key, kind) {
						value.Records = append(value.Records, r)
						break
					}
				}
			}
			target := rgb11wallet.SingleKeyRepairTarget{Identity: value.Identity, Prefix: value.Prefix, Fingerprint: rgb11wallet.RepairExportFingerprint(value), TransferID: first.State.TransferID, Witness: first.State.WitnessTxID, Change: spentChange, Successor: second.State.WitnessTxID}
			patch, err := rgb11wallet.PrepareSingleKeyRepair(ctx, value, target, evidence)
			if err != nil {
				t.Fatal(err)
			}
			if patch.Key != value.Prefix+"pending-"+first.State.TransferID || bytes.Equal(patch.Before, patch.After) {
				t.Fatal("not an exact single-key patch")
			}
			old, err := sender.rgbManager.projectionStore.LoadPendingTransfer(first.State.TransferID)
			if err != nil || old.State.Status != "pending" {
				t.Fatal("dry-run wrote runtime")
			}
			for _, kind := range []string{"scope", "fingerprint", "target", "successor"} {
				t.Run(kind, func(t *testing.T) {
					bad := target
					switch kind {
					case "scope":
						bad.Identity = "wrong"
					case "fingerprint":
						bad.Fingerprint = "wrong"
					case "target":
						bad.TransferID = "wrong"
					case "successor":
						bad.Successor = first.State.WitnessTxID
					}
					if _, err := rgb11wallet.PrepareSingleKeyRepair(ctx, value, bad, evidence); err == nil {
						t.Fatal("bad binding accepted")
					}
				})
			}
			repaired := value
			repaired.Records = append([]rgb11wallet.SnapshotRecord(nil), value.Records...)
			for i := range repaired.Records {
				if repaired.Records[i].Key == "pending-"+first.State.TransferID {
					repaired.Records[i].Value = patch.After
				}
			}
			repeated := target
			repeated.Fingerprint = rgb11wallet.RepairExportFingerprint(repaired)
			if _, err := rgb11wallet.PrepareSingleKeyRepair(ctx, repaired, repeated, evidence); err == nil {
				t.Fatal("repeat accepted")
			}
			if err := sender.rgbManager.projectionStore.SaveTransferState(&first.State); err != nil {
				t.Fatal(err)
			}
			records, err = sender.rgbManager.projectionStore.ExportSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			value.Records = nil
			for _, r := range records {
				for _, kind := range []string{"pending-", "transfer-", "proof-", "validation-", "object-"} {
					if strings.HasPrefix(r.Key, kind) {
						value.Records = append(value.Records, r)
						break
					}
				}
			}
			target.Fingerprint = rgb11wallet.RepairExportFingerprint(value)
			if _, err := rgb11wallet.PrepareSingleKeyRepair(ctx, value, target, evidence); err == nil {
				t.Fatal("two-key repair accepted")
			}
			if len(evidence.broadcasted) != 0 {
				t.Fatal("tool broadcast")
			}
		})
	}
}

func TestRGB11OrphanSelfReceiveRepairDryRunAndApply(t *testing.T) {
	database := newMemoryKVDB()
	projection := rgb11wallet.NewProjectionStore(database, nil)
	engine := rgb11wallet.NewEngineStore(database)
	if err := projection.SetScope("repair-account"); err != nil {
		t.Fatal(err)
	}
	if err := engine.SetScope("repair-account"); err != nil {
		t.Fatal(err)
	}
	requestID := strings.Repeat("11", 32)
	transferID := strings.Repeat("22", 32)
	witnessTxID := strings.Repeat("33", 32)
	consignment := []byte("missing self-receive consignment")
	consignmentHashBytes := sha256.Sum256(consignment)
	consignmentHash := hex.EncodeToString(consignmentHashBytes[:])
	receipt := rgb11wallet.ValidationReceipt{
		Version: 1, EngineBuildID: "repair-test", ConsignmentHash: consignmentHash,
		ContractID: "rgb:repair-test", SchemaID: "rgb11", TransferID: transferID,
		ValidatedAt: 1, Status: "valid",
	}
	if _, err := projection.ValidateAndStoreConsignment(context.Background(),
		orphanReceiptValidator{receipt: receipt}, &orphanReceiptEvidence{}, consignment); err != nil {
		t.Fatal(err)
	}
	receiveRequest := &corewallet.ReceiveRequest{
		Version: corewallet.ReceiveVersion, Mode: corewallet.ReceiveWitness,
		RequestID: requestID, RecipientID: "recipient", Seal: seals.NewWitnessBlindSeal(1, 7),
		WitnessScript: []byte{0x51}, Invoice: "invoice", CreatedAt: 1, Expiry: 2,
		Status: corewallet.ReceiveAcknowledged, TransferID: transferID,
		ObjectHash: consignmentHash, WitnessTxID: witnessTxID,
	}
	encodedRequest, err := corewallet.EncodeReceiveRequest(receiveRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.ImportSnapshot([]rgb11wallet.SnapshotRecord{{
		Key: "wallet/receive/" + requestID, Value: encodedRequest,
	}}); err != nil {
		t.Fatal(err)
	}
	pending := &rgb11wallet.PendingTransfer{
		State: rgb11wallet.TransferState{
			TransferID: transferID, Direction: "send", Status: "prepared",
			WitnessTxID: witnessTxID, ConsignmentHash: consignmentHash,
		},
		RecipientConsignment: consignment, LocalConsignment: []byte("local consignment"),
		SignedTx: []byte{1}, SignedPSBT: []byte{2},
	}
	if err := projection.SavePendingTransfer(pending); err != nil {
		t.Fatal(err)
	}
	receiveState := &rgb11wallet.TransferState{
		TransferID: transferID, Direction: "receive", Status: "awaiting_broadcast",
		WitnessTxID: witnessTxID, ConsignmentHash: consignmentHash,
	}
	if err := projection.SaveTransferState(receiveState); err != nil {
		t.Fatal(err)
	}
	if err := projection.SavePreparedReceive(transferID, requestID); err != nil {
		t.Fatal(err)
	}
	pending.State.Status = "rejected"
	pending.State.RejectReason = "user-rejected"
	if err := projection.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}
	if err := projection.CompactRejectedTransfers([]string{transferID}); err != nil {
		t.Fatal(err)
	}
	projectionRecords, err := projection.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	damaged := make([]rgb11wallet.SnapshotRecord, 0, len(projectionRecords))
	for _, record := range projectionRecords {
		if record.Key != "object-"+consignmentHash {
			damaged = append(damaged, record)
		}
	}
	engineRecords, err := engine.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &rgb11wallet.RGB11WalletSnapshot{
		Version: rgb11wallet.WalletSnapshotVersion, WalletID: "rgb11-repair-wallet",
		AccountIndex: 0, EngineBuildID: "test", ProjectionRecords: damaged, EngineRecords: engineRecords,
	}
	fingerprint, err := rgb11wallet.HistoricalRepairSnapshotHash(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	target := rgb11wallet.OrphanReceiveRepairTarget{
		WalletID: snapshot.WalletID, AccountIndex: 0, SnapshotHash: fingerprint,
		RequestID: requestID, TransferID: transferID, WitnessTxID: witnessTxID, ConsignmentHash: consignmentHash,
	}
	verified := 0
	verifyAbsent := func(sender *rgb11wallet.PendingTransfer) error {
		verified++
		if sender == nil || sender.State.WitnessTxID != witnessTxID {
			return fmt.Errorf("wrong witness")
		}
		return nil
	}
	plan, candidate, err := rgb11wallet.PlanOrphanReceiveRepair(snapshot, target, verifyAbsent)
	if err != nil {
		t.Fatal(err)
	}
	if plan.BeforeHash != fingerprint || plan.AfterHash == fingerprint || verified != 1 {
		t.Fatalf("invalid dry-run plan: %+v verified=%d", plan, verified)
	}
	if unchanged, _ := rgb11wallet.HistoricalRepairSnapshotHash(snapshot); unchanged != fingerprint {
		t.Fatal("dry-run mutated source snapshot")
	}
	if err := rgb11wallet.ValidateWalletSnapshot(candidate); err != nil {
		t.Fatal(err)
	}
	requestAfter, err := corewallet.DecodeReceiveRequest(candidate.EngineRecords[0].Value)
	if err != nil || requestAfter.Status != corewallet.ReceivePrepared || requestAfter.TransferID != "" ||
		requestAfter.ObjectHash != "" || requestAfter.WitnessTxID != "" {
		t.Fatalf("request not safely reset: %+v err=%v", requestAfter, err)
	}
	for _, record := range candidate.ProjectionRecords {
		if record.Key == "transfer-"+transferID || record.Key == "prepared-receive-"+transferID ||
			record.Key == "validation-"+consignmentHash {
			t.Fatalf("orphan receive record survived: %s", record.Key)
		}
	}
	foundReceiptDelete := false
	for _, change := range plan.Changes {
		if change.Store == "projection" && change.Key == "validation-"+consignmentHash && change.Delete {
			foundReceiptDelete = true
		}
	}
	if !foundReceiptDelete {
		t.Fatal("orphan validation receipt deletion is missing from the approved plan")
	}
	approved, err := rgb11wallet.ApplyOrphanReceiveRepair(snapshot, plan, verifyAbsent)
	if err != nil {
		t.Fatal(err)
	}
	approvedHash, _ := rgb11wallet.HistoricalRepairSnapshotHash(approved)
	if approvedHash != plan.AfterHash {
		t.Fatalf("apply hash=%s want=%s", approvedHash, plan.AfterHash)
	}
	tampered := *plan
	tampered.AfterHash = "tampered"
	if _, err := rgb11wallet.ApplyOrphanReceiveRepair(snapshot, &tampered, verifyAbsent); err == nil {
		t.Fatal("tampered approval was accepted")
	}
	if _, _, err := rgb11wallet.PlanOrphanReceiveRepair(snapshot, target, func(*rgb11wallet.PendingTransfer) error {
		return fmt.Errorf("witness lookup unavailable")
	}); err == nil {
		t.Fatal("unknown witness state was accepted")
	}
	// A rejected sender which still has any broadcast-capable payload is not an
	// orphan: the repair tool must leave it for normal recovery.
	pending.RecipientConsignment = nil
	pending.LocalConsignment = nil
	pending.RecipientObjectHash = ""
	pending.LocalObjectHash = ""
	pending.SignedTx = []byte{1}
	pending.SignedPSBT = nil
	if err := projection.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}
	unsafeProjection, err := projection.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	unsafeSnapshot := &rgb11wallet.RGB11WalletSnapshot{
		Version: rgb11wallet.WalletSnapshotVersion, WalletID: snapshot.WalletID,
		AccountIndex: snapshot.AccountIndex, EngineBuildID: snapshot.EngineBuildID,
		ProjectionRecords: unsafeProjection, EngineRecords: engineRecords,
	}
	unsafeHash, err := rgb11wallet.HistoricalRepairSnapshotHash(unsafeSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	unsafeTarget := target
	unsafeTarget.SnapshotHash = unsafeHash
	if _, _, err := rgb11wallet.PlanOrphanReceiveRepair(unsafeSnapshot, unsafeTarget, verifyAbsent); err == nil {
		t.Fatal("sender payload capable of recovery was accepted")
	}

	// A receipt at the target key must itself be bound to the missing target
	// object and transfer. The tool must not hide unrelated receipt corruption by
	// deleting whatever bytes happen to occupy validation-<target hash>.
	otherDB := newMemoryKVDB()
	otherProjection := rgb11wallet.NewProjectionStore(otherDB, nil)
	if err := otherProjection.SetScope("other-receipt"); err != nil {
		t.Fatal(err)
	}
	otherRaw := []byte("unrelated consignment")
	otherHashBytes := sha256.Sum256(otherRaw)
	otherHash := hex.EncodeToString(otherHashBytes[:])
	otherReceipt := receipt
	otherReceipt.ConsignmentHash = otherHash
	otherReceipt.TransferID = strings.Repeat("44", 32)
	if _, err := otherProjection.ValidateAndStoreConsignment(context.Background(),
		orphanReceiptValidator{receipt: otherReceipt}, &orphanReceiptEvidence{}, otherRaw); err != nil {
		t.Fatal(err)
	}
	otherRecords, err := otherProjection.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	var unrelatedReceipt []byte
	for _, record := range otherRecords {
		if record.Key == "validation-"+otherHash {
			unrelatedReceipt = append([]byte(nil), record.Value...)
		}
	}
	if len(unrelatedReceipt) == 0 {
		t.Fatal("unrelated validation receipt fixture missing")
	}
	mismatched := *snapshot
	mismatched.ProjectionRecords = append([]rgb11wallet.SnapshotRecord(nil), snapshot.ProjectionRecords...)
	for index := range mismatched.ProjectionRecords {
		if mismatched.ProjectionRecords[index].Key == "validation-"+consignmentHash {
			mismatched.ProjectionRecords[index].Value = unrelatedReceipt
		}
	}
	mismatchedHash, err := rgb11wallet.HistoricalRepairSnapshotHash(&mismatched)
	if err != nil {
		t.Fatal(err)
	}
	mismatchedTarget := target
	mismatchedTarget.SnapshotHash = mismatchedHash
	if _, _, err := rgb11wallet.PlanOrphanReceiveRepair(&mismatched, mismatchedTarget, verifyAbsent); err == nil {
		t.Fatal("validation receipt for another object and transfer was deleted")
	}
}
