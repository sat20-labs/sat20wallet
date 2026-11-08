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
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type historicalErrorEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	outpoint        string
	failure         error
	hits            int
	persistent      bool
	disabled        bool
	keepStatus      bool
	unknown         bool
	unconfirmed     string
	unconfirmedHits int
	successor       string
	broadcasts      int
	rawUnavailable  bool
	rawFailure      error
	rawHits         int
}

func (e *historicalErrorEvidence) GetOutspend(outpoint string) (*rgb11wallet.BitcoinOutspend, error) {
	if !e.disabled && outpoint == e.outpoint && (e.hits == 0 || e.unknown || e.persistent) {
		e.hits++
		if e.unknown {
			return &rgb11wallet.BitcoinOutspend{Spent: true}, nil
		}
		return nil, e.failure
	}
	return e.BitcoinEvidenceProvider.GetOutspend(outpoint)
}

func (e *historicalErrorEvidence) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if !e.disabled && txid == e.unconfirmed {
		e.unconfirmedHits++
		return &rgb11wallet.BitcoinTxStatus{TxID: txid, InMempool: true}, nil
	}
	if !e.disabled && !e.keepStatus && txid == e.successor {
		return &rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 12}, nil
	}
	return e.BitcoinEvidenceProvider.GetTxStatus(txid)
}

func (e *historicalErrorEvidence) GetRawTx(txid string) ([]byte, error) {
	if !e.disabled && e.rawUnavailable && txid == e.successor {
		e.rawHits++
		if e.rawFailure != nil {
			return nil, e.rawFailure
		}
		return nil, errors.New("successor raw transaction unavailable")
	}
	return e.BitcoinEvidenceProvider.GetRawTx(txid)
}

func (e *historicalErrorEvidence) Broadcast(raw []byte) (string, error) {
	e.broadcasts++
	return e.BitcoinEvidenceProvider.Broadcast(raw)
}

// Substitute only the read view of one proof; do not persist invalid fixture
// bytes. The public store still performs its real decode and history binding.
type historicalProofReadDB struct {
	indexer.KVDB
	outpoint, validationHash string
	hits                     int
	disabled                 bool
}

func (d *historicalProofReadDB) BatchRead(prefix []byte, reverse bool, visit func([]byte, []byte) error) error {
	return d.KVDB.BatchRead(prefix, reverse, func(key, value []byte) error {
		if !d.disabled && strings.Contains(string(key), "proof-"+d.outpoint) {
			if bytes.Count(value, []byte(d.validationHash)) == 1 {
				value = bytes.Replace(value, []byte(d.validationHash), []byte(strings.Repeat("0", len(d.validationHash))), 1)
				d.hits++
			}
		}
		return visit(key, value)
	})
}

// Preserve the real atomic backend; this wrapper changes reads only.
func (d *historicalProofReadDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return d.KVDB.(indexer.AtomicWriteBatchDB).NewAtomicWriteBatch()
}

func newRGB11HistoryErrorFixture(t *testing.T) (*Manager, *RGB11ImportResult, *rgb11AddressEvidence,
	*rgb11wallet.PendingTransfer, *rgb11wallet.PendingTransfer, string) {
	t.Helper()
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
		prepared, err := sender.rgbManager.PrepareRGB11Transfer(ctx, RGB11SendRequest{Invoice: request.Invoice, FeeRate: 2, MinConfirmations: 1})
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
	first, second := makeConfirmed("20000"), makeConfirmed("10000")
	proofs, err := sender.rgbManager.projectionStore.ListProofs()
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range second.State.InputOutPoints {
		for _, proof := range proofs {
			if input == proof.OutPoint && proof.WitnessTxID == first.State.WitnessTxID && proof.AssetName == imported.AssetName {
				if err := sender.rgbManager.rebuildRGB11Locks(); err != nil {
					t.Fatalf("rebuild settled history locks: %v", err)
				}
				if lock := sender.utxoLockerL1.GetLockedUtxoList()[input]; lock != nil {
					t.Fatalf("settled successor retained active input lock: %+v", lock)
				}
				expectedInputs, err := sender.rgbManager.rgb11ExpectedInputs()
				if err != nil || expectedInputs[input] != second.State.WitnessTxID {
					t.Fatalf("settled successor lost expected spend: txid=%q err=%v", expectedInputs[input], err)
				}
				controlBefore, controlBeforeErr := rgb11HistReadSnapshot(sender.rgbManager)
				controlLocks := sender.utxoLockerL1.GetLockedUtxoList()
				balanceBefore, err := sender.GetRGB11AssetBalance(&imported.AssetName)
				if err != nil {
					t.Fatal(err)
				}
				controlResult, controlRefreshErr := sender.rgbManager.RefreshRGB11State(ctx)
				controlAfter, controlAfterErr := rgb11HistReadSnapshot(sender.rgbManager)
				if controlBeforeErr != nil || controlAfterErr != nil {
					t.Logf("healthy refresh audit read error: before=%t after=%t", controlBeforeErr != nil, controlAfterErr != nil)
				} else {
					t.Logf("healthy refresh audit result=%+v err=%v snapshot_diff=%s lock_diff=%s",
						controlResult, controlRefreshErr, rgb11HistJSON(rgb11HistSnapshotDiff(controlBefore, controlAfter)),
						rgb11HistJSON(rgb11HistLockDiff(controlLocks, sender.utxoLockerL1.GetLockedUtxoList(), input, controlBefore, first, second)))
				}
				if controlRefreshErr != nil || sender.rgbManager.consistencyStatus != "ok" {
					t.Fatalf("healthy whole-history refresh failed: consistency=%s err=%v", sender.rgbManager.consistencyStatus, controlRefreshErr)
				}
				for _, pending := range []*rgb11wallet.PendingTransfer{first, second} {
					stored, err := sender.rgbManager.projectionStore.LoadPendingTransfer(pending.State.TransferID)
					if err != nil || stored.State.Status != "settled" {
						t.Fatalf("healthy whole-history refresh changed settled transfer: id=%s err=%v", pending.State.TransferID, err)
					}
				}
				balanceAfter, err := sender.GetRGB11AssetBalance(&imported.AssetName)
				if err != nil || balanceAfter == nil || balanceBefore == nil || balanceAfter.Cmp(balanceBefore) != 0 {
					t.Fatalf("healthy whole-history refresh changed balance: err=%v", err)
				}
				if lock := sender.utxoLockerL1.GetLockedUtxoList()[input]; lock != nil {
					t.Fatalf("healthy whole-history refresh revived settled input lock: %+v", lock)
				}
				return sender, imported, evidence, first, second, input
			}
		}
	}
	t.Fatal("second transaction did not consume first change")
	return nil, nil, nil, nil, nil, ""
}

// The three locally owned carriers are derived from the original business
// transactions, not from the refresh result or its proposed lock plan.
func rgb11HistLockTargets(t *testing.T, snapshot *RGB11WalletSnapshot,
	first, second *rgb11wallet.PendingTransfer, target string) map[string]bool {
	t.Helper()
	owned, err := rgb11wallet.OutputsFromProjectionSnapshot(snapshot.ProjectionRecords)
	if err != nil {
		t.Fatal(err)
	}
	targets := map[string]bool{target: true}
	for _, point := range first.State.InputOutPoints {
		if owned[point] != nil {
			targets[point] = true
		}
	}
	for _, point := range second.State.OutputOutPoints {
		if owned[point] != nil {
			targets[point] = true
		}
	}
	closure, _, valid := rgb11HistDependency(snapshot, target)
	if !valid || len(targets) != 3 || !containsString(first.State.OutputOutPoints, target) || !containsString(second.State.InputOutPoints, target) {
		t.Fatal("history fixture lacks the three connected owned carriers")
	}
	for point := range targets {
		if owned[point] == nil || !closure[point] {
			t.Fatal("history fixture carrier is outside the original owned dependency chain")
		}
	}
	return targets
}

func rgb11HistAssertLocks(t *testing.T, before, after map[string]*LockedUtxo,
	targets map[string]bool, started int64) {
	t.Helper()
	want := make(map[string]*LockedUtxo, len(before)+len(targets))
	for point, lock := range before {
		want[point] = cloneLockedUtxo(lock)
	}
	for point := range targets {
		lock := cloneLockedUtxo(before[point])
		if lock == nil {
			observed := after[point]
			if observed == nil || observed.LockedTime < started || observed.LockedTime > time.Now().Unix() {
				t.Fatal("quarantined history carrier lacks a new bounded lock timestamp")
			}
			lock = &LockedUtxo{LockedTime: observed.LockedTime}
		} else if lock.Reason != rgb11wallet.LockReasonRGB {
			t.Fatal("healthy history fixture carrier was already reserved")
		}
		lock.Reason = rgb11wallet.LockReasonPending
		want[point] = lock
	}
	// Includes all unrelated locks and every owner/value/asset/reservation field.
	// Newly quarantined carriers have only a timestamp and pending reason.
	if !reflect.DeepEqual(want, after) {
		t.Fatal("history quarantine changed locks beyond the three exact carrier markers")
	}
}

func rgb11HistAssertData(t *testing.T, runtime *rgb11Manager,
	before *RGB11WalletSnapshot, balance *Decimal, asset *indexer.AssetName) {
	t.Helper()
	walletID, err := runtime.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	// Read the actual local records under the normal reservation read lock.
	// Do not call a resolver or alter verification/error state while auditing.
	release := runtime.beginRGB11ReservationRead()
	after, err := runtime.readRGB11LocalLocked(walletID)
	release()
	if err != nil || before == nil || after == nil || before.Version != after.Version || rgb11SnapshotChanged(before, after) {
		t.Fatalf("history lost or rewrote durable recovery records: read_error=%t", err != nil)
	}
	current, err := runtime.GetRGB11AssetBalance(asset)
	if err != nil || balance == nil || current == nil || current.Cmp(balance) != 0 {
		t.Fatalf("history changed the original asset amount: read_error=%t", err != nil)
	}
}

func testRGB11RefreshHistoricalErrors(t *testing.T, manager *Manager, evidence rgb11wallet.BitcoinEvidenceProvider,
	first, second *rgb11wallet.PendingTransfer, spentChange string) {
	t.Helper()
	for _, kind := range []string{"settled_history_evidence_error", "unknown_spender", "invalid_binding", "inconsistent_parent_mempool_child_confirmed"} {
		t.Run(kind, func(t *testing.T) {
			manager, _, evidence, first, second, spentChange := newRGB11HistoryErrorFixture(t)
			originalRuntime := manager.rgbManager
			if kind == "invalid_binding" {
				if _, err := originalRuntime.RefreshRGB11State(context.Background()); err != nil {
					t.Fatalf("healthy baseline refresh: %v", err)
				}
				healthy, err := manager.GetRGB11State()
				if err != nil || healthy == nil || healthy.ConsistencyStatus != "ok" || len(healthy.AvailableAssets) == 0 {
					t.Fatalf("invalid proof fault requires a healthy spendable baseline: state_present=%t err=%v", healthy != nil, err)
				}
			}
			beforeSnapshot, beforeSnapshotErr := rgb11HistReadSnapshot(originalRuntime)
			if beforeSnapshotErr != nil {
				t.Fatal(beforeSnapshotErr)
			}
			targets := rgb11HistLockTargets(t, beforeSnapshot, first, second, spentChange)
			before, err := manager.rgbManager.projectionStore.LoadPendingTransfer(first.State.TransferID)
			if err != nil || before.State.Status != "settled" {
				t.Fatalf("invalid settled fixture: %v", err)
			}
			lock := manager.utxoLockerL1.GetLockedUtxoList()[spentChange]
			if lock != nil {
				t.Fatalf("settled successor fixture retained active input lock: %+v", lock)
			}
			sentinel := errors.New("historical outspend evidence temporarily unavailable")
			fault := &historicalErrorEvidence{BitcoinEvidenceProvider: evidence, outpoint: spentChange, failure: sentinel, successor: second.State.WitnessTxID, persistent: true, keepStatus: true}
			rawUnavailableErr := errors.New("successor raw transaction unavailable")
			if kind == "unknown_spender" {
				fault.unknown = true
				fault.rawUnavailable = true // No independent raw witness can identify the spender.
				fault.rawFailure = rawUnavailableErr
			}
			if kind == "inconsistent_parent_mempool_child_confirmed" {
				fault.unconfirmed = first.State.WitnessTxID
				fault.outpoint = "" // Keep the confirmed child facts; isolate the contradictory parent status.
			}
			var proofFault *historicalProofReadDB
			if kind == "invalid_binding" {
				proofs, err := manager.rgbManager.projectionStore.ListProofs()
				if err != nil {
					t.Fatal(err)
				}
				for _, proof := range proofs {
					if proof.OutPoint == spentChange {
						proofFault = &historicalProofReadDB{KVDB: manager.db, outpoint: spentChange, validationHash: proof.ValidationHash}
					}
				}
				if proofFault == nil {
					t.Fatal("missing old proof")
				}
				manager.db = proofFault // Fixed network scopes are created from the owning Manager DB.
				manager.rgbManager, err = newRGB11Manager(manager, proofFault, manager.utxoLockerL1, evidence)
				if err != nil {
					t.Fatal(err)
				}
				// Change the DB read provider only; retain the live verification state.
				manager.rgbManager.scopeStates = originalRuntime.scopeStates
				manager.rgbManager.consistencyStatus = originalRuntime.consistencyStatus
				if err := manager.rgbManager.selectRGB11Scope(); err != nil {
					t.Fatal(err)
				}
				fault.outpoint = "" // Real history binding failure, no evidence failure.
			}
			manager.rgbManager.evidence = fault
			beforeLocks := manager.utxoLockerL1.GetLockedUtxoList()
			balance, err := originalRuntime.GetRGB11AssetBalance(&first.State.Asset.Name)
			if err != nil {
				t.Fatal(err)
			}
			oldProofs, err := originalRuntime.projectionStore.ListProofs()
			if err != nil {
				t.Fatal(err)
			}
			if kind == "unknown_spender" {
				// Assert the precise raw-witness type at its real emitting API. The
				// aggregate refresh may first localize the missing consumer edge.
				proofs, err := rgb11wallet.ProofsFromProjectionSnapshot(beforeSnapshot.ProjectionRecords)
				if err != nil {
					t.Fatal(err)
				}
				for index, proof := range proofs {
					t.Logf("history proof order index=%d target=%t first_input=%t second_output=%t", index, proof.OutPoint == spentChange,
						containsString(first.State.InputOutPoints, proof.OutPoint), containsString(second.State.OutputOutPoints, proof.OutPoint))
				}
				validator := rgb11wallet.NewNativeConsensusValidatorWithReveals(second.ChangeSeals...)
				if err := validator.ValidateHistoricalConsignment(context.Background(), second.LocalConsignment, evidence); err != nil {
					t.Fatalf("healthy successor history control: %v", err)
				}
				rawProbe := &historicalErrorEvidence{BitcoinEvidenceProvider: evidence,
					successor: second.State.WitnessTxID, rawUnavailable: true, rawFailure: rawUnavailableErr, keepStatus: true}
				rawErr := validator.ValidateHistoricalConsignment(context.Background(), second.LocalConsignment, rawProbe)
				if rawProbe.rawHits == 0 || !errors.Is(rawErr, coreconsignment.ErrWitnessUnresolved) || !strings.Contains(rawErr.Error(), rawUnavailableErr.Error()) || rawProbe.broadcasts != 0 {
					t.Fatalf("raw witness typed control mismatch: raw_hits=%d err=%v", rawProbe.rawHits, rawErr)
				}
				t.Logf("raw witness typed control witness_unresolved=%t raw_hits=%d broadcasts=%d",
					errors.Is(rawErr, coreconsignment.ErrWitnessUnresolved), rawProbe.rawHits, rawProbe.broadcasts)
			}
			t.Cleanup(func() {
				// Remove only the test read faults; never rewrite data or readiness.
				fault.disabled = true
				if proofFault != nil {
					proofFault.disabled = true
				}
			})
			refreshRuntime := manager.rgbManager
			liveDB, liveRegistry := manager.db, refreshRuntime.scopeStates
			faultStarted := time.Now().Unix()
			result, refreshErr := refreshRuntime.RefreshRGB11State(context.Background())
			after, err := refreshRuntime.projectionStore.LoadPendingTransfer(first.State.TransferID)
			if err != nil {
				t.Fatal(err)
			}
			afterLocks := manager.utxoLockerL1.GetLockedUtxoList()
			newProofs, err := originalRuntime.projectionStore.ListProofs()
			if err != nil {
				t.Fatal(err)
			}
			afterSnapshot, afterSnapshotErr := rgb11HistReadSnapshot(originalRuntime)
			failedBalance, balanceErr := originalRuntime.GetRGB11AssetBalance(&first.State.Asset.Name)
			balanceEqual := balanceErr == nil && balance != nil && failedBalance != nil && balance.Cmp(failedBalance) == 0
			candidateConsistency := refreshRuntime.consistencyStatus
			resultPresent := result != nil
			unresolved := -1
			if resultPresent {
				unresolved = result.Unresolved
			}
			proofHits := 0
			if proofFault != nil {
				proofHits = proofFault.hits
			}
			t.Logf("refresh diagnostic kind=%s error_type=%T error_present=%t sentinel=%t raw_unavailable=%t inconsistent=%t result_present=%t unresolved=%d consistency=%s pending_equal=%t state_equal=%t status_equal=%t witness_equal=%t receive_request_equal=%t consignment_equal=%t inputs_equal=%t outputs_equal=%t signed_tx_equal=%t signed_psbt_equal=%t change_seals_equal=%t reservation_equal=%t recipient_object_equal=%t local_object_equal=%t lock_set_equal=%t target_lock_present=%t proofs_equal=%t proof_counts_equal=%t balance_error=%t balance_equal=%t outspend_hits=%d raw_hits=%d proof_hits=%d mempool_hits=%d broadcasts=%d",
				kind, refreshErr, refreshErr != nil, errors.Is(refreshErr, sentinel), errors.Is(refreshErr, rawUnavailableErr), errors.Is(refreshErr, ErrRGB11Inconsistent), resultPresent, unresolved, candidateConsistency,
				reflect.DeepEqual(before, after), reflect.DeepEqual(before.State, after.State), before.State.Status == after.State.Status, before.State.WitnessTxID == after.State.WitnessTxID,
				before.State.ReceiveRequestID == after.State.ReceiveRequestID, before.State.ConsignmentHash == after.State.ConsignmentHash,
				reflect.DeepEqual(before.State.InputOutPoints, after.State.InputOutPoints), reflect.DeepEqual(before.State.OutputOutPoints, after.State.OutputOutPoints),
				reflect.DeepEqual(before.SignedTx, after.SignedTx), reflect.DeepEqual(before.SignedPSBT, after.SignedPSBT), reflect.DeepEqual(before.ChangeSeals, after.ChangeSeals),
				before.ReservationID == after.ReservationID, before.RecipientObjectHash == after.RecipientObjectHash, before.LocalObjectHash == after.LocalObjectHash,
				reflect.DeepEqual(beforeLocks, afterLocks), afterLocks[spentChange] != nil, reflect.DeepEqual(oldProofs, newProofs), len(oldProofs) == len(newProofs),
				balanceErr != nil, balanceEqual, fault.hits, fault.rawHits, proofHits, fault.unconfirmedHits, fault.broadcasts)
			if beforeSnapshotErr != nil || afterSnapshotErr != nil {
				t.Logf("snapshot audit read error kind=%s before=%t after=%t", kind, beforeSnapshotErr != nil, afterSnapshotErr != nil)
			} else {
				t.Logf("snapshot audit kind=%s diff=%s", kind,
					rgb11HistJSON(rgb11HistSnapshotDiff(beforeSnapshot, afterSnapshot)))
			}
			t.Logf("lock outpoint audit kind=%s diff=%s", kind,
				rgb11HistJSON(rgb11HistLockDiff(beforeLocks, afterLocks, spentChange, beforeSnapshot, first, second)))
			t.Logf("error type audit kind=%s witness_unresolved=%t invalid_proof=%t",
				kind, errors.Is(refreshErr, coreconsignment.ErrWitnessUnresolved), errors.Is(refreshErr, rgb11wallet.ErrInvalidProof))
			if result != nil {
				closure, _, valid := rgb11HistDependency(beforeSnapshot, spentChange)
				allInClosure, targetReported := valid, false
				for _, point := range result.Inconsistent {
					allInClosure = allInClosure && closure[point]
					targetReported = targetReported || point == spentChange
				}
				t.Logf("result count audit kind=%s settled=%d pending=%d reorged=%d conflicted=%d unresolved=%d inconsistent_count=%d dependency_decoded=%t inconsistent_in_closure=%t target_reported=%t",
					kind, result.Settled, result.Pending, result.Reorged, result.Conflicted, result.Unresolved, len(result.Inconsistent), valid, allInClosure, targetReported)
			}
			if refreshErr != nil {
				t.Logf("bounded error context: %.700s", refreshErr.Error())
			}
			if kind == "unknown_spender" {
				if fault.rawHits == 0 {
					t.Fatal("refresh did not query the unavailable raw successor")
				}
				if errors.Is(refreshErr, coreconsignment.ErrWitnessUnresolved) {
					if !strings.Contains(refreshErr.Error(), rawUnavailableErr.Error()) {
						t.Fatal("witness-unresolved refresh lost its raw failure context")
					}
				} else {
					var unavailable rgb11wallet.ProofFault
					wantCause := fmt.Sprintf("RGB11 allocation %s has an unidentified spender", spentChange)
					if fault.hits == 0 || !errors.As(refreshErr, &unavailable) || unavailable.OutPoint != spentChange || unavailable.AssetName != first.State.Asset.Name || unavailable.Cause == nil || unavailable.Cause.Error() != wantCause {
						t.Fatalf("unknown spender lacks the exact localized consumer failure: raw_hits=%d err=%v", fault.rawHits, refreshErr)
					}
				}
			} else if kind == "invalid_binding" {
				if proofFault.hits == 0 || !errors.Is(refreshErr, rgb11wallet.ErrInvalidProof) {
					t.Fatalf("binding classification mismatch: proof_hits=%d err=%v", proofFault.hits, refreshErr)
				}
			} else if kind == "inconsistent_parent_mempool_child_confirmed" {
				if fault.unconfirmedHits == 0 {
					t.Fatal("mempool parent status was not queried")
				}
			} else if fault.hits == 0 {
				t.Fatal("target evidence fault did not fire")
			}
			if after.State.Status != "settled" || !errors.Is(refreshErr, ErrRGB11Inconsistent) {
				t.Fatalf("atomic historical validation failure changed lifecycle or disappeared: status=%s", after.State.Status)
			}
			if kind == "settled_history_evidence_error" && !errors.Is(refreshErr, sentinel) {
				t.Fatal("sentinel cause lost")
			}
			if kind == "settled_history_evidence_error" {
				if !strings.Contains(refreshErr.Error(), spentChange) {
					t.Fatal("error lost the failing producer outpoint context")
				}
			} else if kind == "inconsistent_parent_mempool_child_confirmed" &&
				!strings.Contains(refreshErr.Error(), first.State.WitnessTxID) && !strings.Contains(refreshErr.Error(), spentChange) {
				t.Fatal("error lost the inconsistent producer transaction context")
			}
			if candidateConsistency != "broken" {
				t.Fatalf("consistency=%s", candidateConsistency)
			}
			state, stateErr := manager.GetRGB11State()
			if stateErr != nil || state == nil || state.ConsistencyStatus != "broken" || len(state.AvailableAssets) != 0 || refreshRuntime.rgb11ProjectionAvailable() || refreshRuntime.rgb11ProjectionStableConfirmed() {
				t.Fatalf("quarantined history remained usable: state_present=%t err=%v", state != nil, stateErr)
			}
			// Partial diagnostics are distinct from committed lifecycle changes.
			if result == nil || result.Settled != 0 || result.Pending != 0 || result.Reorged != 0 || result.Conflicted != 0 || result.Unresolved != 0 || len(result.Inconsistent) == 0 {
				t.Fatal("history fault lacks partial diagnostics or claims a lifecycle change")
			}
			closure, _, valid := rgb11HistDependency(beforeSnapshot, spentChange)
			seen := make(map[string]bool)
			for _, point := range result.Inconsistent {
				if !valid || !closure[point] || seen[point] {
					t.Fatal("partial diagnostic escaped the original dependency closure or repeated a carrier")
				}
				seen[point] = true
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("failed candidate result/snapshot mismatch: result_present=%t unresolved=%d pending_equal=%t", resultPresent, unresolved, reflect.DeepEqual(before, after))
			}
			rgb11HistAssertLocks(t, beforeLocks, afterLocks, targets, faultStarted)
			rgb11HistAssertData(t, originalRuntime, beforeSnapshot, balance, &first.State.Asset.Name)
			if !reflect.DeepEqual(oldProofs, newProofs) {
				t.Fatal("failed candidate changed the proof set")
			}
			if fault.broadcasts != 0 {
				t.Fatal("refresh broadcast a transaction")
			}
			successor, err := originalRuntime.projectionStore.LoadPendingTransfer(second.State.TransferID)
			if err != nil || successor.State.Status != "settled" {
				t.Fatal("independent successor changed")
			}
			if balanceErr != nil || !balanceEqual {
				t.Fatal("failed candidate changed wallet balance")
			}
			// Remove only the fault. Retry with this exact live runtime/DB/registry.
			fault.disabled = true
			if proofFault != nil {
				proofFault.disabled = true
			}
			if _, err := refreshRuntime.RefreshRGB11State(context.Background()); err != nil {
				t.Fatalf("recovered retry: %v", err)
			}
			if manager.rgbManager != refreshRuntime || manager.db != liveDB || refreshRuntime.scopeStates != liveRegistry || refreshRuntime.consistencyStatus != "ok" || len(refreshRuntime.rgb11ScopeState().ProofFaults) != 0 {
				t.Fatal("same-runtime retry did not clear the real error")
			}
			recovered, err := manager.GetRGB11State()
			if err != nil || recovered == nil || recovered.ConsistencyStatus != "ok" || len(recovered.AvailableAssets) == 0 || !refreshRuntime.rgb11ProjectionAvailable() || !refreshRuntime.rgb11ProjectionStableConfirmed() {
				t.Fatalf("healthy retry did not restore usable ownership: state_present=%t err=%v", recovered != nil, err)
			}
			rgb11HistAssertData(t, refreshRuntime, beforeSnapshot, balance, &first.State.Asset.Name)
			if !reflect.DeepEqual(beforeLocks, manager.utxoLockerL1.GetLockedUtxoList()) || fault.broadcasts != 0 {
				t.Fatal("healthy retry changed the original locks or broadcast a transaction")
			}
			for _, id := range []string{first.State.TransferID, second.State.TransferID} {
				stored, err := refreshRuntime.projectionStore.LoadPendingTransfer(id)
				if err != nil || stored.State.Status != "settled" {
					t.Fatalf("retry changed history: %s err=%v", id, err)
				}
			}
			newBalance, err := refreshRuntime.GetRGB11AssetBalance(&first.State.Asset.Name)
			if err != nil || newBalance.Cmp(balance) != 0 {
				t.Fatal("balance changed")
			}
		})
	}
	// A single failed observation can recover before publication. Keep this
	// separate from the cases that require a continuously observable error.
	t.Run("transient_evidence_recovers", func(t *testing.T) {
		manager, imported, evidence, _, _, spentChange := newRGB11HistoryErrorFixture(t)
		runtime := manager.rgbManager
		before, err := rgb11HistReadSnapshot(runtime)
		if err != nil {
			t.Fatal(err)
		}
		locks := manager.utxoLockerL1.GetLockedUtxoList()
		balance, err := runtime.GetRGB11AssetBalance(&imported.AssetName)
		if err != nil {
			t.Fatal(err)
		}
		fault := &historicalErrorEvidence{BitcoinEvidenceProvider: evidence, outpoint: spentChange,
			failure: errors.New("one historical observation unavailable"), keepStatus: true}
		runtime.evidence = fault
		for attempt := 0; attempt < 2; attempt++ {
			result, err := runtime.RefreshRGB11State(context.Background())
			if err != nil || result == nil || result.Settled != 0 || result.Pending != 0 || result.Reorged != 0 || result.Conflicted != 0 || result.Unresolved != 0 || len(result.Inconsistent) != 0 || fault.hits != 1 || runtime.consistencyStatus != "ok" || !runtime.rgb11ProjectionAvailable() || !runtime.rgb11ProjectionStableConfirmed() || len(runtime.rgb11ScopeState().ProofFaults) != 0 {
				t.Fatalf("single observation did not recover safely: attempt=%d hits=%d err=%v", attempt, fault.hits, err)
			}
			rgb11HistAssertData(t, runtime, before, balance, &imported.AssetName)
			if manager.rgbManager != runtime || !reflect.DeepEqual(locks, manager.utxoLockerL1.GetLockedUtxoList()) || fault.broadcasts != 0 {
				t.Fatal("transient recovery changed runtime, locks, or broadcast a transaction")
			}
		}
	})
	// The worker must publish a sustained evidence fault and recover through a
	// later real iteration on the same runtime, without resetting its registry.
	t.Run("background_error_visible", func(t *testing.T) {
		manager, imported, evidence, first, second, spentChange := newRGB11HistoryErrorFixture(t)
		before, err := rgb11HistReadSnapshot(manager.rgbManager)
		if err != nil {
			t.Fatal(err)
		}
		locks := manager.utxoLockerL1.GetLockedUtxoList()
		balance, err := manager.rgbManager.GetRGB11AssetBalance(&imported.AssetName)
		if err != nil {
			t.Fatal(err)
		}
		targets := rgb11HistLockTargets(t, before, first, second, spentChange)
		// The fixture stopped its original registry. Retain this one fresh runtime
		// for both the faulted and recovered worker, over the original real DB.
		backgroundManager, err := newRGB11Manager(manager, manager.db, manager.utxoLockerL1, evidence)
		if err != nil {
			t.Fatal(err)
		}
		manager.rgbManager = backgroundManager
		if err := backgroundManager.selectRGB11Scope(); err != nil {
			t.Fatal(err)
		}
		fault := &historicalErrorEvidence{BitcoinEvidenceProvider: evidence, outpoint: spentChange,
			failure: errors.New("background historical evidence unavailable"), persistent: true, keepStatus: true}
		backgroundManager.evidence = fault
		scope := backgroundManager.rgb11ScopeKey()
		registry := backgroundManager.scopeStates
		registry.mu.Lock()
		registry.retryDelay = time.Hour
		registry.mu.Unlock()
		var workerDone []<-chan struct{}
		t.Cleanup(func() {
			registry.stopReconciliations()
			// The registry joins the worker body; also join each wrapper's final
			// close(done) before fixture cleanup can close its DB or inspect data.
			for _, done := range workerDone {
				<-done
			}
		})
		runWorker := func() <-chan struct{} {
			worker, started := registry.startReconciliation(scope)
			if !started {
				t.Fatal("worker already running or registry stopped")
			}
			done := make(chan struct{})
			workerDone = append(workerDone, done)
			go func() { defer close(done); backgroundManager.runRGB11ChainReconciliation(scope, worker) }()
			return done
		}
		waitWorker := func(done <-chan struct{}) {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("bounded history worker did not finish")
			}
		}
		started := time.Now().Unix()
		done := runWorker()
		// The localized persistent fault returns ErrInconsistent, which ends this
		// iteration naturally. Join before inspecting counters or removing faults.
		waitWorker(done)
		state, err := backgroundManager.GetRGB11State()
		if err != nil || state == nil || state.SyncStatus != "error" || state.ConsistencyStatus != "broken" || len(state.AvailableAssets) != 0 || fault.hits == 0 {
			t.Fatalf("background fault was not observable: state_present=%t hits=%d err=%v", state != nil, fault.hits, err)
		}
		rgb11HistAssertData(t, backgroundManager, before, balance, &imported.AssetName)
		rgb11HistAssertLocks(t, locks, manager.utxoLockerL1.GetLockedUtxoList(), targets, started)
		if fault.broadcasts != 0 {
			t.Fatal("background fault broadcast a transaction")
		}
		fault.disabled = true
		waitWorker(runWorker())
		state, err = backgroundManager.GetRGB11State()
		if err != nil || state == nil || state.SyncStatus != "idle" || state.ConsistencyStatus != "ok" || len(state.AvailableAssets) == 0 || !backgroundManager.rgb11ProjectionAvailable() || !backgroundManager.rgb11ProjectionStableConfirmed() || len(backgroundManager.rgb11ScopeState().ProofFaults) != 0 {
			t.Fatalf("same-runtime background retry did not recover: state_present=%t err=%v", state != nil, err)
		}
		rgb11HistAssertData(t, backgroundManager, before, balance, &imported.AssetName)
		if manager.rgbManager != backgroundManager || backgroundManager.scopeStates != registry || !reflect.DeepEqual(locks, manager.utxoLockerL1.GetLockedUtxoList()) || fault.broadcasts != 0 {
			t.Fatal("background retry replaced runtime/registry, changed original locks, or broadcast")
		}
	})
}
