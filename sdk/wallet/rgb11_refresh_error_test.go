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
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type historicalErrorEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	outpoint    string
	failure     error
	hits        int
	unknown     bool
	unconfirmed string
	successor   string
	broadcasts  int
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
		return &rgb11wallet.BitcoinTxStatus{TxID: txid, InMempool: true}, nil
	}
	if txid == e.successor {
		return &rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 12}, nil
	}
	return e.BitcoinEvidenceProvider.GetTxStatus(txid)
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
	for _, kind := range []string{"settled_history_evidence_error", "unknown_spender", "invalid_binding", "reliable_mempool_downgrade"} {
		t.Run(kind, func(t *testing.T) {
			originalRuntime := manager.rgbManager
			before, err := manager.rgbManager.projectionStore.LoadPendingTransfer(first.State.TransferID)
			if err != nil || before.State.Status != "settled" {
				t.Fatalf("invalid settled fixture: %v", err)
			}
			lock := manager.utxoLockerL1.GetLockedUtxoList()[spentChange]
			if lock == nil || lock.ReservationID != second.ReservationID {
				t.Fatal("fixture does not bind old change to successor")
			}
			sentinel := errors.New("historical outspend evidence temporarily unavailable")
			fault := &historicalErrorEvidence{BitcoinEvidenceProvider: evidence, outpoint: spentChange, failure: sentinel, successor: second.State.WitnessTxID}
			if kind == "unknown_spender" {
				fault.unknown = true
			}
			if kind == "reliable_mempool_downgrade" {
				fault.unconfirmed = first.State.WitnessTxID
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
				manager.rgbManager, err = newRGB11Manager(manager, proofFault, manager.utxoLockerL1, evidence)
				if err != nil {
					t.Fatal(err)
				}
				if err := manager.rgbManager.selectRGB11Scope(); err != nil {
					t.Fatal(err)
				}
				fault.outpoint = "" // Real history binding failure, no evidence failure.
			}
			manager.rgbManager.evidence = fault
			beforeLocks := manager.utxoLockerL1.GetLockedUtxoList()
			balance, err := manager.GetRGB11AssetBalance(&first.State.Asset.Name)
			if err != nil {
				t.Fatal(err)
			}
			oldProofs, err := originalRuntime.projectionStore.ListProofs()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				manager.rgbManager = originalRuntime
				originalRuntime.evidence = evidence
				originalRuntime.consistencyStatus = "ok"
				if err := manager.rgbManager.projectionStore.SavePendingTransferState(before); err != nil {
					t.Fatal(err)
				}
			})
			result, refreshErr := manager.rgbManager.RefreshRGB11State(context.Background())
			after, err := manager.rgbManager.projectionStore.LoadPendingTransfer(first.State.TransferID)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("fault_hits=%d before=%s after=%s refresh_err=%v result=%+v", fault.hits, before.State.Status, after.State.Status, refreshErr, result)
			if (kind == "unknown_spender" && fault.hits < 2) || (kind != "invalid_binding" && kind != "unknown_spender" && fault.hits != 1) {
				t.Fatal("target evidence fault did not fire exactly once")
			}
			wantStatus := "settled"
			if kind == "reliable_mempool_downgrade" {
				wantStatus = "pending"
			}
			if after.State.Status != wantStatus || refreshErr == nil {
				t.Fatalf("historical evidence error corrupted lifecycle or disappeared: status=%s error=%v", after.State.Status, refreshErr)
			}
			if kind == "settled_history_evidence_error" && !errors.Is(refreshErr, sentinel) {
				t.Fatal("sentinel cause lost")
			}
			if !strings.Contains(refreshErr.Error(), first.State.TransferID) || !strings.Contains(refreshErr.Error(), first.State.WitnessTxID) {
				t.Fatal("error lost transfer/witness context")
			}
			wantConsistency := "warning"
			if kind == "invalid_binding" {
				wantConsistency = "broken"
				if proofFault.hits == 0 || !errors.Is(refreshErr, ErrRGB11Inconsistent) {
					t.Fatalf("binding classification: hits=%d err=%v", proofFault.hits, refreshErr)
				}
			}
			if manager.rgbManager.consistencyStatus != wantConsistency {
				t.Fatalf("consistency=%s", manager.rgbManager.consistencyStatus)
			}
			if kind != "reliable_mempool_downgrade" && (result.Unresolved == 0 || !reflect.DeepEqual(before, after)) {
				t.Fatal("failed check changed historical journal or hid unresolved result")
			}
			currentLock := manager.utxoLockerL1.GetLockedUtxoList()[spentChange]
			if !reflect.DeepEqual(lock, currentLock) {
				t.Fatal("failed check changed successor lock")
			}
			if !reflect.DeepEqual(beforeLocks, manager.utxoLockerL1.GetLockedUtxoList()) {
				t.Fatal("failed check changed lock set")
			}
			newProofs, err := originalRuntime.projectionStore.ListProofs()
			if err != nil {
				t.Fatal(err)
			}
			for _, old := range oldProofs {
				if old.OutPoint == spentChange {
					for _, newProof := range newProofs {
						if newProof.OutPoint == spentChange && !reflect.DeepEqual(old, newProof) {
							t.Fatal("failed check changed spent proof")
						}
					}
				}
			}
			successorAdvanced := false
			for _, proof := range newProofs {
				if proof.WitnessTxID == second.State.WitnessTxID && proof.Confirmations == 12 && proof.Status == "settled" {
					successorAdvanced = true
				}
			}
			if !successorAdvanced {
				t.Fatal("historical failure starved independent successor refresh")
			}
			if fault.broadcasts != 0 {
				t.Fatal("refresh broadcast a transaction")
			}
			successor, err := originalRuntime.projectionStore.LoadPendingTransfer(second.State.TransferID)
			if err != nil || successor.State.Status != "settled" {
				t.Fatal("independent successor changed")
			}
			// Restore the normal view and lifecycle only for the explicit mempool
			// fixture, then prove a retry can resolve the failure without repair.
			manager.rgbManager = originalRuntime
			originalRuntime.evidence = evidence
			if kind == "reliable_mempool_downgrade" {
				return
			}
			if _, err := originalRuntime.RefreshRGB11State(context.Background()); err != nil {
				t.Fatalf("recovered retry: %v", err)
			}
			if originalRuntime.consistencyStatus != "ok" {
				t.Fatal("retry did not clear error")
			}
			for _, id := range []string{first.State.TransferID, second.State.TransferID} {
				stored, err := originalRuntime.projectionStore.LoadPendingTransfer(id)
				if err != nil || stored.State.Status != "settled" {
					t.Fatalf("retry changed history: %s err=%v", id, err)
				}
			}
			newBalance, err := manager.GetRGB11AssetBalance(&first.State.Asset.Name)
			if err != nil || newBalance.Cmp(balance) != 0 {
				t.Fatal("balance changed")
			}
		})
	}
	// Run one bounded background iteration; shared sync_status must expose the
	// same error even though the worker uses its own manager consistency field.
	t.Run("background_error_visible", func(t *testing.T) {
		fault := &historicalErrorEvidence{BitcoinEvidenceProvider: evidence, outpoint: spentChange, failure: errors.New("background historical evidence unavailable")}
		manager.rgbManager.evidence = fault
		defer func() { manager.rgbManager.evidence = evidence }()
		scope := manager.rgbManager.rgb11ScopeKey()
		registry := manager.rgbManager.scopeStates
		registry.mu.Lock()
		registry.retryDelay = time.Hour
		registry.mu.Unlock()
		worker, started := registry.startReconciliation(scope)
		if !started {
			t.Fatal("worker already running")
		}
		done := make(chan struct{})
		go func() { defer close(done); manager.rgbManager.runRGB11ChainReconciliation(scope, worker) }()
		defer func() { registry.stopReconciliations(); <-done }()
		deadline := time.After(5 * time.Second)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-deadline:
				t.Fatal("background error was not observable")
			case <-ticker.C:
				state, err := manager.GetRGB11State()
				if err == nil && state.SyncStatus == "error" {
					return
				}
			}
		}
	})
}
