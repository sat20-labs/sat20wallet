package wallet

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type historicalErrorEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	outpoint        string
	failure         error
	hits            int
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
	if outpoint == e.outpoint && (e.hits == 0 || e.unknown) {
		e.hits++
		if e.unknown {
			return &rgb11wallet.BitcoinOutspend{Spent: true}, nil
		}
		return nil, e.failure
	}
	return e.BitcoinEvidenceProvider.GetOutspend(outpoint)
}

func (e *historicalErrorEvidence) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if txid == e.unconfirmed {
		e.unconfirmedHits++
		return &rgb11wallet.BitcoinTxStatus{TxID: txid, InMempool: true}, nil
	}
	if txid == e.successor {
		return &rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 12}, nil
	}
	return e.BitcoinEvidenceProvider.GetTxStatus(txid)
}

func (e *historicalErrorEvidence) GetRawTx(txid string) ([]byte, error) {
	if e.rawUnavailable && txid == e.successor {
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
}

func (d *historicalProofReadDB) BatchRead(prefix []byte, reverse bool, visit func([]byte, []byte) error) error {
	return d.KVDB.BatchRead(prefix, reverse, func(key, value []byte) error {
		if strings.Contains(string(key), "proof-"+d.outpoint) {
			if bytes.Count(value, []byte(d.validationHash)) == 1 {
				value = bytes.Replace(value, []byte(d.validationHash), []byte(strings.Repeat("0", len(d.validationHash))), 1)
				d.hits++
			}
		}
		return visit(key, value)
	})
}

func testRGB11RefreshHistoricalErrors(t *testing.T, manager *Manager, evidence rgb11wallet.BitcoinEvidenceProvider,
	first, second *rgb11wallet.PendingTransfer, spentChange string) {
	t.Helper()
	for _, kind := range []string{"settled_history_evidence_error", "unknown_spender", "invalid_binding", "inconsistent_parent_mempool_child_confirmed"} {
		t.Run(kind, func(t *testing.T) {
			originalRuntime := manager.rgbManager
			originalDB := manager.db
			if kind == "invalid_binding" {
				if _, err := originalRuntime.RefreshRGB11State(context.Background()); err != nil {
					t.Fatalf("healthy baseline refresh: %v", err)
				}
				healthy, err := manager.GetRGB11State()
				if err != nil || healthy == nil || healthy.ConsistencyStatus != "ok" || len(healthy.AvailableAssets) == 0 {
					t.Fatalf("invalid proof fault requires a healthy spendable baseline: state_present=%t err=%v", healthy != nil, err)
				}
			}
			before, err := manager.rgbManager.projectionStore.LoadPendingTransfer(first.State.TransferID)
			if err != nil || before.State.Status != "settled" {
				t.Fatalf("invalid settled fixture: %v", err)
			}
			lock := manager.utxoLockerL1.GetLockedUtxoList()[spentChange]
			if lock != nil {
				t.Fatalf("settled successor fixture retained active input lock: %+v", lock)
			}
			sentinel := errors.New("historical outspend evidence temporarily unavailable")
			fault := &historicalErrorEvidence{BitcoinEvidenceProvider: evidence, outpoint: spentChange, failure: sentinel, successor: second.State.WitnessTxID}
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
			t.Cleanup(func() {
				manager.db = originalDB
				manager.rgbManager = originalRuntime
				originalRuntime.evidence = evidence
				originalRuntime.consistencyStatus = "ok"
				if err := manager.rgbManager.projectionStore.SavePendingTransferState(before); err != nil {
					t.Fatal(err)
				}
				cleanupFirst, firstErr := originalRuntime.projectionStore.LoadPendingTransfer(first.State.TransferID)
				cleanupSecond, secondErr := originalRuntime.projectionStore.LoadPendingTransfer(second.State.TransferID)
				cleanupProofs, proofErr := originalRuntime.projectionStore.ListProofs()
				cleanupBalance, balanceErr := originalRuntime.GetRGB11AssetBalance(&first.State.Asset.Name)
				cleanupLocks := manager.utxoLockerL1.GetLockedUtxoList()
				cleanupBalanceEqual := balanceErr == nil && balance != nil && cleanupBalance != nil && balance.Cmp(cleanupBalance) == 0
				t.Logf("post-cleanup diagnostic kind=%s first_load_error=%t first_equal=%t second_load_error=%t second_settled=%t lock_set_equal=%t target_lock_present=%t proof_load_error=%t proofs_equal=%t proof_counts_equal=%t balance_error=%t balance_equal=%t",
					kind, firstErr != nil, firstErr == nil && reflect.DeepEqual(before, cleanupFirst), secondErr != nil, secondErr == nil && cleanupSecond.State.Status == "settled",
					reflect.DeepEqual(beforeLocks, cleanupLocks), cleanupLocks[spentChange] != nil, proofErr != nil, proofErr == nil && reflect.DeepEqual(oldProofs, cleanupProofs),
					proofErr == nil && len(oldProofs) == len(cleanupProofs), balanceErr != nil, cleanupBalanceEqual)
			})
			refreshRuntime := manager.rgbManager
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
			if refreshErr != nil {
				t.Logf("bounded error context: %.700s", refreshErr.Error())
			}
			if kind == "unknown_spender" {
				if fault.rawHits != 1 || !errors.Is(refreshErr, coreconsignment.ErrWitnessUnresolved) || !strings.Contains(refreshErr.Error(), rawUnavailableErr.Error()) {
					t.Fatalf("raw witness failure classification mismatch: raw_hits=%d", fault.rawHits)
				}
			} else if kind == "invalid_binding" {
				if proofFault.hits == 0 || !errors.Is(refreshErr, rgb11wallet.ErrInvalidProof) {
					t.Fatalf("binding classification mismatch: proof_hits=%d err=%v", proofFault.hits, refreshErr)
				}
			} else if kind == "inconsistent_parent_mempool_child_confirmed" {
				if fault.unconfirmedHits == 0 {
					t.Fatal("mempool parent status was not queried")
				}
			} else if fault.hits != 1 {
				t.Fatal("target evidence fault did not fire exactly once")
			}
			if after.State.Status != "settled" || refreshErr == nil {
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
			wantConsistency := "warning"
			if kind == "invalid_binding" || kind == "inconsistent_parent_mempool_child_confirmed" {
				wantConsistency = "broken"
			}
			if kind == "invalid_binding" {
				// Reads can retain history, but known-invalid ownership cannot remain available.
				state, stateErr := manager.GetRGB11State()
				if stateErr != nil || state == nil || state.ConsistencyStatus != "broken" || len(state.AvailableAssets) != 0 || refreshRuntime.rgb11ProjectionAvailable() || refreshRuntime.rgb11ProjectionStableConfirmed() {
					t.Fatalf("invalid proof remained usable: state_present=%t err=%v readiness=%t", state != nil, stateErr, refreshRuntime.rgb11ProjectionAvailable())
				}
			} else if candidateConsistency != wantConsistency {
				t.Fatalf("consistency=%s", candidateConsistency)
			}
			// Rejected atomic candidates need not invent an unresolved transfer
			// count. This contradictory-evidence case is about rejecting all
			// lifecycle/proof/lock changes, which the full assertions below retain.
			if (kind == "settled_history_evidence_error" || kind == "unknown_spender" || kind == "invalid_binding") && resultPresent &&
				(result.Settled != 0 || result.Pending != 0 || result.Reorged != 0 || result.Conflicted != 0 || result.Unresolved != 0 || len(result.Inconsistent) != 0) {
				t.Fatal("rejected historical evidence candidate claimed a published transition")
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("failed candidate result/snapshot mismatch: result_present=%t unresolved=%d pending_equal=%t", resultPresent, unresolved, reflect.DeepEqual(before, after))
			}
			currentLock := manager.utxoLockerL1.GetLockedUtxoList()[spentChange]
			if currentLock != nil {
				t.Fatalf("failed check revived settled successor lock: %+v", currentLock)
			}
			if !reflect.DeepEqual(beforeLocks, manager.utxoLockerL1.GetLockedUtxoList()) {
				t.Fatal("failed check changed lock set")
			}
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
			// Restore the normal evidence view, then prove a retry resolves the
			// candidate failure without changing the already-settled history.
			manager.db = originalDB
			manager.rgbManager = originalRuntime
			originalRuntime.evidence = evidence
			if _, err := originalRuntime.RefreshRGB11State(context.Background()); err != nil {
				t.Fatalf("recovered retry: %v", err)
			}
			if originalRuntime.consistencyStatus != "ok" {
				t.Fatal("retry did not clear error")
			}
			if kind == "invalid_binding" {
				recovered, err := manager.GetRGB11State()
				if err != nil || recovered == nil || recovered.ConsistencyStatus != "ok" || len(recovered.AvailableAssets) == 0 || !originalRuntime.rgb11ProjectionAvailable() {
					t.Fatalf("healthy retry did not restore usable ownership: state_present=%t err=%v", recovered != nil, err)
				}
			}
			for _, id := range []string{first.State.TransferID, second.State.TransferID} {
				stored, err := originalRuntime.projectionStore.LoadPendingTransfer(id)
				if err != nil || stored.State.Status != "settled" {
					t.Fatalf("retry changed history: %s err=%v", id, err)
				}
			}
			newBalance, err := originalRuntime.GetRGB11AssetBalance(&first.State.Asset.Name)
			if err != nil || newBalance.Cmp(balance) != 0 {
				t.Fatal("balance changed")
			}
		})
	}
	// Run one bounded background iteration; shared sync_status must expose the
	// same error even though the worker uses its own manager consistency field.
	t.Run("background_error_visible", func(t *testing.T) {
		// The deterministic transaction tests stop their protocol-only manager's
		// reconciliation registry. Use a fresh runtime over the same persisted
		// DB/evidence so this test starts exactly one controlled worker.
		originalRuntime := manager.rgbManager
		backgroundManager, err := newRGB11Manager(manager, manager.db, manager.utxoLockerL1, evidence)
		if err != nil {
			t.Fatal(err)
		}
		manager.rgbManager = backgroundManager
		defer func() { manager.rgbManager = originalRuntime }()
		if err := backgroundManager.selectRGB11Scope(); err != nil {
			t.Fatal(err)
		}
		fault := &historicalErrorEvidence{BitcoinEvidenceProvider: evidence, outpoint: spentChange, failure: errors.New("background historical evidence unavailable")}
		backgroundManager.evidence = fault
		scope := backgroundManager.rgb11ScopeKey()
		registry := backgroundManager.scopeStates
		registry.mu.Lock()
		registry.retryDelay = time.Hour
		registry.mu.Unlock()
		worker, started := registry.startReconciliation(scope)
		if !started {
			t.Fatal("worker already running")
		}
		done := make(chan struct{})
		go func() { defer close(done); backgroundManager.runRGB11ChainReconciliation(scope, worker) }()
		defer func() { registry.stopReconciliations(); <-done }()
		deadline := time.After(5 * time.Second)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-deadline:
				t.Fatal("background error was not observable")
			case <-ticker.C:
				state, err := backgroundManager.GetRGB11State()
				if err == nil && state.SyncStatus == "error" {
					return
				}
			}
		}
	})
}
