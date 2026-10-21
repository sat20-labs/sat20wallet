package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
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
func TestRGB11RefreshSpentChangeHistory(t *testing.T) {
	for _, originalStatus := range []string{"settled", "pending"} {
		t.Run(originalStatus, func(t *testing.T) {
			// Use a real seeded carrier with matching raw transaction and confirmed
			// producer status. The generic NIA-vector fixture only stubs its source
			// outpoint and cannot support whole-history chain reconciliation.
			sender, recipient, imported, evidence, rpc := newRGB11ValidContractSpendFixture(t)
			sender.rgbManager.scopeStates.stopReconciliations()
			recipient.rgbManager.scopeStates.stopReconciliations()
			ctx := context.Background()
			makeConfirmed := func(amount string) *rgb11wallet.PendingTransfer {
				t.Helper()
				request, err := recipient.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{
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
			var originalSpentProof *rgb11wallet.AllocationProof
			knownProofs, err := sender.rgbManager.projectionStore.ListProofs()
			if err != nil {
				t.Fatal(err)
			}
			for _, input := range second.State.InputOutPoints {
				for _, proof := range knownProofs {
					if input == proof.OutPoint && proof.WitnessTxID == first.State.WitnessTxID && proof.AssetName == imported.AssetName {
						spentChange = input
						originalSpentProof = proof
					}
				}
			}
			if spentChange == "" || originalSpentProof == nil {
				t.Fatal("second transaction did not consume first change")
			}
			if evidence.spendingTx[spentChange] != second.State.WitnessTxID {
				t.Fatal("wrong spending witness")
			}

			before, err := sender.GetRGB11AssetBalance(&imported.AssetName)
			if err != nil {
				t.Fatal(err)
			}
			// Simulate a lost response after confirmation by restoring only the old
			// mutable journal phase. The signed transaction and immutable recovery
			// material remain intact, so refresh must re-derive the confirmed state.
			first.State.Status = originalStatus
			if err := sender.rgbManager.projectionStore.SavePendingTransferState(first); err != nil {
				t.Fatal(err)
			}
			spentLock := sender.utxoLockerL1.GetLockedUtxoList()[spentChange]
			if originalStatus == "settled" {
				if err := sender.rgbManager.rebuildRGB11Locks(); err != nil {
					t.Fatal(err)
				}
				spentLock = sender.utxoLockerL1.GetLockedUtxoList()[spentChange]
				if spentLock != nil {
					t.Fatalf("settled successor retained active input lock: %+v", spentLock)
				}
				expected, err := sender.rgbManager.rgb11ExpectedInputs()
				if err != nil || expected[spentChange] != second.State.WitnessTxID {
					t.Fatalf("settled successor lost expected spend: txid=%q err=%v",
						expected[spentChange], err)
				}
			} else if spentLock == nil || spentLock.ReservationID != second.ReservationID {
				t.Fatalf("pending history lost successor ownership: %+v", spentLock)
			}
			if originalStatus == "settled" {
				t.Run("unknown_spender_with_confirmed_local_successor", func(t *testing.T) {
					// Public outspend evidence can report spent without a spender ID.
					// A confirmed raw transaction still proves its exact input spend.
					fault := &historicalErrorEvidence{BitcoinEvidenceProvider: evidence, outpoint: spentChange, unknown: true}
					if _, ok := verifyRGB11ExpectedSpend(fault, spentChange, second.State.WitnessTxID); !ok {
						t.Fatal("fixture lacks independently verified successor spend")
					}
					sender.rgbManager.evidence = fault
					defer func() { sender.rgbManager.evidence = evidence }()
					result, err := sender.rgbManager.RefreshRGB11State(ctx)
					if err != nil || sender.GetRGB11ConsistencyStatus() != "ok" {
						t.Fatalf("confirmed local successor left history unresolved: result=%+v consistency=%s err=%v", result, sender.GetRGB11ConsistencyStatus(), err)
					}
				})
				t.Run("unknown_successor_boundaries", func(t *testing.T) {
					testRGB11UnknownHistoricalSuccessor(t, sender, rpc, evidence, first, second, spentChange)
				})
				t.Run("refresh_error_classification", func(t *testing.T) {
					testRGB11RefreshHistoricalErrors(t, sender, evidence, first, second, spentChange)
				})
				t.Run("rebuild_after_restart", func(t *testing.T) {
					manager := sender
					for attempt := 0; attempt < 3; attempt++ {
						if attempt == 1 {
							// New runtime/locker, same persisted DB; no exported snapshot or
							// copied in-memory lock state may hide a restart regression.
							manager = &Manager{
								db: sender.db, wallet: sender.wallet,
								status:        &Status{CurrentWallet: sender.status.CurrentWallet, CurrentAccount: sender.status.CurrentAccount},
								walletInfoMap: sender.walletInfoMap,
								utxoLockerL1:  NewUtxoLocker(sender.db, rpc, L1_NETWORK_BITCOIN),
							}
							manager.utxoLockerL1.Init()
							var err error
							manager.rgbManager, err = newRGB11Manager(manager, manager.db, manager.utxoLockerL1, evidence)
							if err != nil {
								t.Fatal(err)
							}
							if err := manager.rgbManager.selectRGB11Scope(); err != nil {
								t.Fatal(err)
							}
						}
						if err := manager.rgbManager.rebuildRGB11Locks(); err != nil {
							t.Fatalf("rebuild attempt=%d failed error_type=%T reservation_owner_mismatch=%t consistency=%s", attempt, err, strings.Contains(err.Error(), "UTXO reservation owner mismatch"), manager.rgbManager.consistencyStatus)
						}
						for _, id := range []string{first.State.TransferID, second.State.TransferID} {
							stored, err := manager.rgbManager.projectionStore.LoadPendingTransfer(id)
							if err != nil || stored.State.Status != "settled" {
								t.Fatalf("restart changed settled history: attempt=%d load_error=%t settled=%t", attempt, err != nil, err == nil && stored.State.Status == "settled")
							}
						}
						lock := manager.utxoLockerL1.GetLockedUtxoList()[spentChange]
						if lock != nil {
							t.Fatalf("rebuild revived settled successor input lock: %+v", lock)
						}
						for _, outpoint := range rgb11PendingChangeOutpoints(second) {
							current := manager.utxoLockerL1.GetLockedUtxoList()[outpoint]
							if current == nil || current.Reason != rgb11wallet.LockReasonRGB || current.ReservationID != "" {
								t.Fatalf("current unspent carrier not finalized: %+v", current)
							}
						}
						proofs, err := manager.rgbManager.projectionStore.ListProofs()
						if err != nil {
							t.Fatal(err)
						}
						found := false
						for _, proof := range proofs {
							if proof.OutPoint == spentChange {
								found = true
								if proof.Status != "spending" {
									t.Fatalf("spent proof revived: %s", proof.Status)
								}
							}
						}
						if !found {
							t.Fatal("spent history disappeared")
						}
						after, err := manager.GetRGB11AssetBalance(&imported.AssetName)
						if err != nil || after.Cmp(before) != 0 || manager.rgbManager.consistencyStatus != "ok" {
							t.Fatalf("rebuild balance/consistency changed: balance=%v err=%v consistency=%s", after, err, manager.rgbManager.consistencyStatus)
						}
						if len(evidence.broadcasted) != 0 {
							t.Fatal("rebuild broadcast a transaction")
						}
					}
				})
				t.Run("rebuild_rejects_invalid_ownership", func(t *testing.T) {
					for _, kind := range []string{"unknown_owner", "multiple_owners", "wrong_input_binding", "wrong_change_binding"} {
						t.Run(kind, func(t *testing.T) {
							originalFirst, err := sender.rgbManager.projectionStore.LoadPendingTransfer(first.State.TransferID)
							if err != nil {
								t.Fatal(err)
							}
							originalSecond, err := sender.rgbManager.projectionStore.LoadPendingTransfer(second.State.TransferID)
							if err != nil {
								t.Fatal(err)
							}
							oldLocks := sender.utxoLockerL1.GetLockedUtxoList()
							oldConsistency := sender.rgbManager.consistencyStatus
							writeLocks := func(locks map[string]*LockedUtxo, deletes ...string) {
								t.Helper()
								sender.utxoLockerL1.mutex.Lock()
								err := sender.utxoLockerL1.persistReservationChangesLocked(locks, deletes)
								sender.utxoLockerL1.mutex.Unlock()
								if err != nil {
									t.Fatal(err)
								}
							}
							t.Cleanup(func() {
								if err := sender.rgbManager.projectionStore.SavePendingTransferStates([]*rgb11wallet.PendingTransfer{originalFirst, originalSecond}); err != nil {
									t.Fatal(err)
								}
								writeLocks(oldLocks, spentChange)
								sender.rgbManager.consistencyStatus = oldConsistency
							})
							bad, err := sender.rgbManager.projectionStore.LoadPendingTransfer(second.State.TransferID)
							if err != nil {
								t.Fatal(err)
							}
							switch kind {
							case "unknown_owner":
								writeLocks(map[string]*LockedUtxo{spentChange: {
									LockedTime: time.Now().Unix(), Reason: rgb11wallet.LockReasonPending,
									ReservationID: "unknown-owner",
								}})
							case "multiple_owners":
								// Two distinct syntactically bound transactions now claim the
								// same input; reject the journals before touching any lock.
								tx := wire.NewMsgTx(wire.TxVersion)
								if err := tx.Deserialize(bytes.NewReader(bad.SignedTx)); err != nil {
									t.Fatal(err)
								}
								tx.LockTime++
								var raw bytes.Buffer
								if err := tx.Serialize(&raw); err != nil {
									t.Fatal(err)
								}
								bad.State.TransferID = originalFirst.State.TransferID
								bad.ReservationID = originalFirst.ReservationID
								bad.State.WitnessTxID = tx.TxHash().String()
								bad.SignedTx = raw.Bytes()
								bad.State.OutputOutPoints = nil
								for vout := range tx.TxOut {
									bad.State.OutputOutPoints = append(bad.State.OutputOutPoints, fmt.Sprintf("%s:%d", bad.State.WitnessTxID, vout))
								}
							case "wrong_input_binding":
								bad.State.InputOutPoints = append([]string(nil), originalFirst.State.InputOutPoints...)
							case "wrong_change_binding":
								bad.ChangeSeals[0].Vout = ^uint32(0)
							}
							if kind != "unknown_owner" {
								if err := sender.rgbManager.projectionStore.SavePendingTransferState(bad); err != nil {
									// A framework resv now rejects rebinding an existing transfer to
									// another tx before a corrupt ownership journal can be persisted.
									if kind == "multiple_owners" && errors.Is(err, ErrRGB11InvoiceMismatch) {
										stored, loadErr := sender.rgbManager.projectionStore.LoadPendingTransfer(originalFirst.State.TransferID)
										if loadErr != nil || stored.State.WitnessTxID != originalFirst.State.WitnessTxID {
											t.Fatal("failed rebind changed original transfer")
										}
										return
									}
									t.Fatal(err)
								}
							}
							lockSnapshot := func() map[string][]byte {
								t.Helper()
								result := make(map[string][]byte)
								for _, prefix := range []string{DB_KEY_LOCKEDUTXO, DB_KEY_LOCK_LASTTIME} {
									if err := sender.db.BatchRead([]byte(GetDBKeyPrefix()+prefix), false, func(key, value []byte) error {
										result[string(key)] = append([]byte(nil), value...)
										return nil
									}); err != nil {
										t.Fatal(err)
									}
								}
								return result
							}
							beforeLocks := lockSnapshot()
							err = sender.rgbManager.rebuildRGB11Locks()
							wantError := ErrRGB11Inconsistent
							if kind == "unknown_owner" {
								wantError = ErrUtxoReservationOwner
							}
							if !errors.Is(err, wantError) || sender.rgbManager.consistencyStatus != "broken" {
								t.Fatalf("invalid ownership accepted: kind=%s err=%v consistency=%s", kind, err, sender.rgbManager.consistencyStatus)
							}
							if !reflect.DeepEqual(beforeLocks, lockSnapshot()) {
								t.Fatal("failed rebuild changed persisted locks or refresh marker")
							}
						})
					}
				})
			}
			t.Run("refresh_twice", func(t *testing.T) {
				for attempt := 0; attempt < 2; attempt++ {
					_, refreshErr := sender.rgbManager.RefreshRGB11State(ctx)
					if refreshErr != nil {
						t.Fatalf("refresh attempt=%d: %v", attempt, refreshErr)
					}
					got, err := sender.rgbManager.projectionStore.LoadPendingTransfer(first.State.TransferID)
					if err != nil {
						t.Fatal(err)
					}
					if got.State.Status != "settled" {
						t.Errorf("historical %s was not re-derived to settled after spent change refresh: %s", originalStatus, got.State.Status)
					}
					gotSecond, err := sender.rgbManager.projectionStore.LoadPendingTransfer(second.State.TransferID)
					if err != nil || gotSecond.State.Status != "settled" {
						t.Fatalf("second transfer changed: %+v, %v", gotSecond, err)
					}
					after, err := sender.GetRGB11AssetBalance(&imported.AssetName)
					if err != nil || after.Cmp(before) != 0 {
						t.Fatalf("balance changed: before=%v after=%v err=%v", before, after, err)
					}
					proofs, err := sender.rgbManager.projectionStore.ListProofs()
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, proof := range proofs {
						if proof.OutPoint == spentChange {
							found = true
							if !reflect.DeepEqual(originalSpentProof, proof) {
								t.Error("refresh changed the original consumed proof")
							}
							if proof.Status != "spending" {
								t.Errorf("spent carrier revived: %s", proof.Status)
							}
						}
					}
					if !found {
						t.Error("spent change history disappeared")
					}
					lock := sender.utxoLockerL1.GetLockedUtxoList()[spentChange]
					if lock != nil {
						t.Fatalf("refresh retained settled successor input lock: %+v", lock)
					}
					if len(evidence.broadcasted) != 0 {
						t.Fatal("refresh broadcast a transaction")
					}
				}
			})
			if originalStatus == "settled" {
				t.Run("history_binding_negatives", func(t *testing.T) {
					receipt, err := sender.rgbManager.loadRGB11HistoricalReceipt(first)
					if err != nil {
						t.Fatal(err)
					}
					var allocation *rgb11wallet.ValidatedAllocation
					for i := range receipt.Allocations {
						if receipt.Allocations[i].OutPoint == spentChange {
							allocation = &receipt.Allocations[i]
							break
						}
					}
					if allocation == nil {
						t.Fatal("missing change allocation")
					}
					check := func(spender string) error {
						return sender.rgbManager.validateRGB11SpentChangeHistory(ctx, first, receipt, *allocation, spender)
					}
					if err := check(second.State.WitnessTxID); err != nil {
						t.Fatalf("valid history: %v", err)
					}
					for _, wrong := range []string{first.State.WitnessTxID, fmt.Sprintf("%064x", 99)} {
						if err := check(wrong); err == nil {
							t.Errorf("accepted wrong spender %q", wrong)
						}
					}
					// Missing spender remains unsafe when the confirmed raw successor
					// cannot independently establish the exact input relationship.
					sender.rgbManager.evidence = &historicalErrorEvidence{BitcoinEvidenceProvider: evidence, successor: second.State.WitnessTxID, rawUnavailable: true}
					for _, unknown := range []string{"", "unknown"} {
						if err := check(unknown); err == nil {
							t.Errorf("accepted unverified spender %q", unknown)
						}
					}
					sender.rgbManager.evidence = evidence
					second.State.Status = "pending"
					if err := sender.rgbManager.projectionStore.SavePendingTransferState(second); err != nil {
						t.Fatal(err)
					}
					if err := check(second.State.WitnessTxID); err == nil {
						t.Error("accepted nonsettled successor")
					}
					second.State.Status = "settled"
					if err := sender.rgbManager.projectionStore.SavePendingTransferState(second); err != nil {
						t.Fatal(err)
					}
					evidence.statusMu.Lock()
					evidence.statuses[second.State.WitnessTxID] = &rgb11wallet.BitcoinTxStatus{TxID: second.State.WitnessTxID, InMempool: true}
					evidence.statusMu.Unlock()
					if err := check(second.State.WitnessTxID); err == nil {
						t.Error("accepted unconfirmed successor")
					}
					evidence.statusMu.Lock()
					evidence.statuses[second.State.WitnessTxID] = &rgb11wallet.BitcoinTxStatus{TxID: second.State.WitnessTxID, Confirmed: true, Confirmations: 6}
					evidence.statusMu.Unlock()
					t.Run("storage_rejects_mismatched_proof", func(t *testing.T) {
						for _, proof := range knownProofs {
							if proof.OutPoint != spentChange {
								continue
							}
							invalid := *proof
							invalid.ConsignmentHash = fmt.Sprintf("%064x", 77)
							if err := sender.rgbManager.projectionStore.SaveProofState(&invalid); err == nil {
								t.Fatal("storage accepted mismatched historical proof")
							}
						}
						if err := check(second.State.WitnessTxID); err != nil {
							t.Fatalf("rejected write changed valid history: %v", err)
						}
					})
					t.Run("helper_rejects_mismatched_allocation", func(t *testing.T) {
						invalid := *allocation
						invalid.OperationID = "wrong-operation"
						if err := sender.rgbManager.validateRGB11SpentChangeHistory(ctx, first, receipt, invalid, second.State.WitnessTxID); err == nil {
							t.Fatal("helper accepted allocation not bound to historical proof")
						}
					})
				})
				t.Run("three_hop_confirmed_history_unknown_spenders", func(t *testing.T) {
					third := makeConfirmed("1000")
					testRGB11ThreeHopHistory(t, sender, evidence, first, second, third)
				})
			}
		})
	}
}
