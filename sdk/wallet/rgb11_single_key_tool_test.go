//go:build rgb11repair

package wallet

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

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
