package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func TestRGB11ReceivedFullSend(t *testing.T) {
	for _, direct := range []bool{false, true} {
		name := "standard"
		if direct {
			name = "direct"
		}
		t.Run(name, func(t *testing.T) {
			donor, receiver, imported, evidence, rpc := newRGB11GenericSendFixture(t)
			donor.rgbManager.scopeStates.stopReconciliations()
			receiver.rgbManager.scopeStates.stopReconciliations()
			ctx := context.Background()
			confirm := func(pending *rgb11wallet.PendingTransfer) {
				t.Helper()
				tx := wire.NewMsgTx(wire.TxVersion)
				if err := tx.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
					t.Fatal(err)
				}
				txid := tx.TxHash().String()
				evidence.mu.Lock()
				evidence.rawTx[txid] = append([]byte(nil), pending.SignedTx...)
				for _, in := range tx.TxIn {
					op := in.PreviousOutPoint.String()
					evidence.spendingTx[op] = txid
					delete(evidence.utxos, op)
					delete(rpc.outputs, op)
				}
				for vout, out := range tx.TxOut {
					op := fmt.Sprintf("%s:%d", txid, vout)
					evidence.utxos[op] = &rgb11wallet.BitcoinUTXO{OutPoint: op, Value: out.Value, PkScript: out.PkScript, Confirmations: 6}
					output := indexer.NewTxOutput(out.Value)
					output.OutPointStr = op
					output.OutValue.PkScript = out.PkScript
					rpc.outputs[op] = output
				}
				evidence.mu.Unlock()
				evidence.statusMu.Lock()
				evidence.statuses[txid] = &rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 6}
				evidence.statusMu.Unlock()
			}
			// Genuine donor consignment accepted by the receiver creates the receive
			// journal and proof; no transfer/lock history is manually constructed.
			invoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID, AmountRaw: "20000", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix()})
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := donor.rgbManager.PrepareRGB11Transfer(ctx, RGB11SendRequest{Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1})
			if err != nil {
				t.Fatal(err)
			}
			pending, err := donor.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
			if err != nil {
				t.Fatal(err)
			}
			confirm(pending)
			if _, err := receiver.AcceptRGB11Consignment(ctx, invoice.RequestID, []byte(prepared.RecipientConsignment)); err != nil {
				t.Fatal(err)
			}
			if _, err := receiver.rgbManager.RefreshRGB11State(ctx); err != nil {
				t.Fatalf("settle actual receive: %v", err)
			}
			received, err := receiver.GetRGB11State()
			if err != nil {
				t.Fatal(err)
			}
			if len(received.Transfers) != 1 || received.Transfers[0].Direction != "receive" || received.Transfers[0].Status != "settled" {
				t.Fatal("actual receive did not settle")
			}
			if len(received.Proofs) != 1 || len(received.Assets) != 1 || received.ConsistencyStatus != "ok" {
				t.Fatal("receive-only projection is not spendable")
			}
			source := received.Proofs[0].OutPoint
			if lock := receiver.utxoLockerL1.GetLockedUtxoList()[source]; lock == nil || lock.Reason != rgb11wallet.LockReasonRGB || lock.ReservationID != "" {
				t.Fatal("receive-only carrier must retain its ownerless RGB lock")
			}
			// Indexer provides a plain fee UTXO controlled by this receiver.
			script, err := AddrToPkScript(receiver.wallet.GetAddress(), &chaincfg.TestNet4Params)
			if err != nil {
				t.Fatal(err)
			}
			feeOP := fmt.Sprintf("%064x:0", 9999)
			fee := indexer.NewTxOutput(100000)
			fee.OutPointStr = feeOP
			fee.OutValue.PkScript = script
			rpc.outputs[feeOP] = fee
			rpc.plain = []*indexerwire.TxOutputInfo{{OutPoint: feeOP, Value: 100000, PkScript: script}}
			evidence.mu.Lock()
			evidence.utxos[feeOP] = &rgb11wallet.BitcoinUTXO{OutPoint: feeOP, Value: 100000, PkScript: script, Confirmations: 6}
			evidence.mu.Unlock()
			var sent *RGB11PreparedTransfer
			if direct {
				endpoint, err := donor.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{RecordOptions: dkvsindexer.RecordOptions{TTL: testRGB11FreeLocalTTL}})
				if err != nil {
					t.Fatal(err)
				}
				target, err := mailboxSubscriptionTarget(endpoint.AccountID)
				if err != nil {
					t.Fatal(err)
				}
				if err := donor.SubscribeDKVSPrefix(target); err != nil {
					t.Fatal(err)
				}
				sent, err = receiver.rgbManager.prepareRGB11AddressBatch(ctx, []RGB11AddressSendRequest{{ReceiverAddress: donor.wallet.GetAddress(), AssetName: imported.AssetName, AmountRaw: "20000", FeeRate: 2, MinConfirmations: 1}}, dkvsindexer.RecordVerificationOptions{})
			} else {
				invoice2, invoiceErr := donor.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID, AmountRaw: "20000", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix()})
				if invoiceErr != nil {
					t.Fatal(invoiceErr)
				}
				sent, err = receiver.rgbManager.PrepareRGB11Transfer(ctx, RGB11SendRequest{Invoice: invoice2.Invoice, FeeRate: 2, MinConfirmations: 1})
			}
			if err != nil {
				t.Fatal(err)
			}
			sendPending, err := receiver.rgbManager.projectionStore.LoadPendingTransfer(sent.State.TransferID)
			if err != nil {
				t.Fatal(err)
			}
			if len(sendPending.ChangeSeals) != 0 {
				t.Fatal("expected full-balance send without change")
			}
			// Receive refresh must preserve the successor's active reservation.
			if _, err := receiver.rgbManager.RefreshRGB11State(ctx); err != nil {
				t.Fatal(err)
			}
			if lock := receiver.utxoLockerL1.GetLockedUtxoList()[source]; lock == nil || lock.ReservationID != sendPending.ReservationID || lock.Reason != rgb11wallet.LockReasonPending {
				t.Fatal("receive refresh changed active send ownership")
			}
			// Invalid journals must fail before a receive-history skip can bypass them.
			signed := sendPending.SignedTx
			sendPending.SignedTx = nil
			if err := receiver.rgbManager.projectionStore.SavePendingTransferState(sendPending); err != nil {
				t.Fatal(err)
			}
			if _, err := receiver.rgbManager.RefreshRGB11State(ctx); !errors.Is(err, ErrRGB11Inconsistent) {
				t.Fatalf("invalid journal accepted: %v", err)
			}
			if lock := receiver.utxoLockerL1.GetLockedUtxoList()[source]; lock == nil || lock.ReservationID != sendPending.ReservationID {
				t.Fatal("invalid journal changed ownership")
			}
			sendPending.SignedTx = signed
			if err := receiver.rgbManager.projectionStore.SavePendingTransferState(sendPending); err != nil {
				t.Fatal(err)
			}
			if direct {
				if _, err := receiver.rgbManager.deliverRGB11AddressTransferStore(mustRGB11ConfiguredStore(t, receiver), sent.State.TransferID, RGB11AddressDeliveryOptions{}); err != nil {
					t.Fatal(err)
				}
				result, err := donor.SyncConfiguredRGB11AddressMailbox(ctx, dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
				if err != nil || result.Invalid != 0 || result.Received != 1 {
					t.Fatalf("direct receive=%+v err=%v", result, err)
				}
				if _, err := receiver.SyncConfiguredRGB11AddressMailbox(ctx, dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{}); err != nil {
					t.Fatal(err)
				}
				if _, err := receiver.BroadcastRGB11AddressTransfer(sent.State.TransferID); err != nil {
					t.Fatal(err)
				}
				sendPending, err = receiver.rgbManager.projectionStore.LoadPendingTransfer(sent.State.TransferID)
				if err != nil {
					t.Fatal(err)
				}
				if !sendPending.State.DeliveryAcknowledged {
					t.Fatal("Direct send lacks genuine ACK")
				}
			} else {
				sendPending.State.Status = "broadcast"
				sendPending.State.AckStatus = "accepted-out-of-band"
				if err := receiver.rgbManager.projectionStore.SavePendingTransferState(sendPending); err != nil {
					t.Fatal(err)
				}
			}
			confirm(sendPending)
			if _, err := receiver.rgbManager.RefreshRGB11State(ctx); err != nil {
				t.Fatalf("first settle refresh: %v", err)
			}
			if lock := receiver.utxoLockerL1.GetLockedUtxoList()[source]; lock != nil {
				t.Fatal("settlement did not consume source reservation")
			}
			if err := receiver.rgbManager.rebuildRGB11Locks(); err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 3; attempt++ {
				if _, err := receiver.rgbManager.RefreshRGB11State(ctx); err != nil {
					t.Fatalf("refresh %d: %v", attempt, err)
				}
				if err := receiver.rgbManager.rebuildRGB11Locks(); err != nil {
					t.Fatalf("rebuild %d: %v", attempt, err)
				}
				if receiver.utxoLockerL1.GetLockedUtxoList()[source] != nil {
					t.Fatal("historical receive re-locked spent carrier")
				}
				state, err := receiver.GetRGB11State()
				if err != nil {
					t.Fatal(err)
				}
				if state.ConsistencyStatus != "ok" || len(state.Proofs) != 1 || state.Proofs[0].Status != "spending" || len(state.Outputs) != 1 || len(state.Transfers) != 2 || len(state.Assets) != 0 {
					t.Fatal("settled full-send projection changed")
				}
				for _, transfer := range state.Transfers {
					if transfer.Status != "settled" {
						t.Fatal("settled lifecycle changed")
					}
				}
			}
			// The fix prevents recreating locks; it must not erase existing wrong owners.
			for _, owner := range []string{"foreign-owner", ""} {
				lock := &LockedUtxo{LockedTime: time.Now().Unix(), Reason: rgb11wallet.LockReasonRGB, ReservationID: owner}
				receiver.utxoLockerL1.mutex.Lock()
				err := receiver.utxoLockerL1.persistReservationChangesLocked(map[string]*LockedUtxo{source: lock}, nil)
				receiver.utxoLockerL1.mutex.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := receiver.rgbManager.RefreshRGB11State(ctx); !errors.Is(err, ErrUtxoReservationOwner) {
					t.Fatalf("owner %q was not rejected: %v", owner, err)
				}
				if err := receiver.rgbManager.rebuildRGB11Locks(); !errors.Is(err, ErrUtxoReservationOwner) {
					t.Fatalf("rebuild accepted owner %q: %v", owner, err)
				}
				if got := receiver.utxoLockerL1.GetLockedUtxoList()[source]; got == nil || got.ReservationID != owner || got.Reason != lock.Reason {
					t.Fatal("wrong owner was modified")
				}
				// Remove only this test's deliberate corruption before the next control.
				receiver.utxoLockerL1.mutex.Lock()
				err = receiver.utxoLockerL1.persistReservationChangesLocked(nil, []string{source})
				receiver.utxoLockerL1.mutex.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				if err := receiver.rgbManager.rebuildRGB11Locks(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
