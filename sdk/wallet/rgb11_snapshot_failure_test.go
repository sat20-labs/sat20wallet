package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Fail only the full snapshot scan, leaving ordinary list/read APIs usable.
type rgb11SnapshotReadFailureDB struct {
	indexer.KVDB
	prefix  []byte
	failure error
	armed   bool
	hits    int
}

func (d *rgb11SnapshotReadFailureDB) BatchRead(prefix []byte, reverse bool, visit func([]byte, []byte) error) error {
	if d.armed && bytes.Equal(prefix, d.prefix) {
		d.hits++
		return d.failure
	}
	return d.KVDB.BatchRead(prefix, reverse, visit)
}

// Preserve the real atomic backend; this wrapper changes reads only.
func (d *rgb11SnapshotReadFailureDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return d.KVDB.(indexer.AtomicWriteBatchDB).NewAtomicWriteBatch()
}

// rgb11SnapshotProofReadFailureDB injects one proof-view fault without changing
// durable bytes. Disabling the wrapper restores the same runtime's normal DB view.
type rgb11SnapshotProofReadFailureDB struct {
	indexer.KVDB
	outpoint, validationHash string
	enabled                  bool
	hits                     int
}

func (d *rgb11SnapshotProofReadFailureDB) BatchRead(prefix []byte, reverse bool, visit func([]byte, []byte) error) error {
	needle := []byte("proof-" + d.outpoint)
	validationHash := []byte(d.validationHash)
	return d.KVDB.BatchRead(prefix, reverse, func(key, value []byte) error {
		if d.enabled && bytes.Contains(key, needle) && bytes.Count(value, validationHash) == 1 {
			value = bytes.Replace(value, validationHash, bytes.Repeat([]byte{'0'}, len(validationHash)), 1)
			d.hits++
		}
		return visit(key, value)
	})
}

// Preserve atomic writes while the proof wrapper changes only reads.
func (d *rgb11SnapshotProofReadFailureDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return d.KVDB.(indexer.AtomicWriteBatchDB).NewAtomicWriteBatch()
}

// rgb11SnapshotRawDBRecords captures local scoped bytes for an immutable-data
// audit. It is read-only and is never used as an export/import candidate.
func rgb11SnapshotRawDBRecords(t *testing.T, database indexer.KVDB, scope string) map[string][]byte {
	t.Helper()
	rows := make(map[string][]byte)
	for _, prefix := range [][]byte{
		[]byte("rgb11-" + scope + "-"),
		[]byte("rgb11-engine-" + scope + "-"),
	} {
		err := database.BatchRead(prefix, false, func(key, value []byte) error {
			if !bytes.HasPrefix(key, prefix) {
				return errors.New("RGB11 database read returned a key outside its requested scope")
			}
			name := string(key)
			if _, exists := rows[name]; exists {
				return errors.New("RGB11 database read returned a duplicate key")
			}
			rows[name] = append([]byte(nil), value...)
			return nil
		})
		if err != nil {
			t.Fatalf("read scoped RGB11 database records: %v", err)
		}
	}
	return rows
}

func TestRGB11SnapshotFailureRevokesLiveReadiness(t *testing.T) {
	for _, path := range []string{"internal", "public"} {
		t.Run(path, func(t *testing.T) {
			for _, kind := range []string{"invalid_proof", "read_unavailable"} {
				t.Run(kind, func(t *testing.T) {
					manager, recipient, imported, evidence, _ := newRGB11GenericSendFixture(t)
					manager.rgbManager.scopeStates.stopReconciliations()
					recipient.rgbManager.scopeStates.stopReconciliations()
					ctx := context.Background()
					if !manager.accountManagedRecoveryConfigured() {
						t.Fatal("public fixture is not recovery-configured")
					}
					if err := manager.SyncAccountManagementState(ctx); err != nil {
						t.Fatalf("complete healthy RGB module backup before the fault: %v", err)
					}
					healthyResult, err := manager.RefreshRGB11State(ctx)
					if err != nil {
						t.Fatalf("healthy public refresh: %v", err)
					}
					state, err := manager.GetRGB11State()
					if err != nil || state == nil || state.ConsistencyStatus != "ok" || len(state.AvailableAssets) == 0 {
						t.Fatalf("healthy state is not available: state_present=%t err=%v", state != nil, err)
					}
					remote, ok := manager.http.(*rgb11MemoryDKVSHTTP)
					if !ok {
						t.Fatal("fixture lost its existing DKVS transport")
					}
					indexKey, err := account.ManagedIndexKey(manager.accountProfile.AccountID)
					if err != nil {
						t.Fatal(err)
					}
					walletEntry, err := accountEntry(manager.accountProfile.AccountID, "wallets", AccountManagedDataGlobalScope)
					if err != nil {
						t.Fatal(err)
					}
					recoveryRecords := func() []byte {
						remote.mu.Lock()
						defer remote.mu.Unlock()
						records := make(map[string]any)
						for _, key := range []string{walletEntry.Key, indexKey, rgb11LocalModuleForTest(t, manager).Key} {
							record := remote.records[key]
							if record == nil {
								t.Fatal("missing existing account recovery record")
							}
							records[key] = record
						}
						encoded, err := json.Marshal(records)
						if err != nil {
							t.Fatal(err)
						}
						return encoded
					}
					beforeRemote := recoveryRecords()
					originalRuntime, originalDB := manager.rgbManager, manager.db
					walletID, err := originalRuntime.RGB11WalletID()
					if err != nil {
						t.Fatal(err)
					}
					before, _, err := originalRuntime.exportRGB11WalletSnapshot(walletID)
					if err != nil {
						t.Fatal(err)
					}
					scope := rgb11StorageScope(manager.status.CurrentWallet, manager.status.CurrentAccount)
					beforeRawDBRows := rgb11SnapshotRawDBRecords(t, originalDB, scope)
					beforeProjectionRows, err := originalRuntime.projectionStore.ReadLocalRecords()
					if err != nil {
						t.Fatalf("read baseline local projection rows: %v", err)
					}
					beforeEngineRows, err := originalRuntime.engineStore.ExportSnapshot()
					if err != nil {
						t.Fatalf("read baseline typed engine rows: %v", err)
					}
					beforeOutputs, err := originalRuntime.projectionStore.ListOutputs()
					if err != nil {
						t.Fatal(err)
					}
					beforeTransfers, err := originalRuntime.projectionStore.ListTransfers()
					if err != nil {
						t.Fatal(err)
					}
					beforeLocks := manager.utxoLockerL1.GetLockedUtxoList()
					beforeBalance, err := originalRuntime.GetRGB11AssetBalance(&imported.AssetName)
					if err != nil {
						t.Fatal(err)
					}
					proofs, err := originalRuntime.projectionStore.ListProofs()
					if err != nil || len(proofs) == 0 {
						t.Fatal("missing original proof")
					}
					badOutpoint := proofs[0].OutPoint
					if kind == "invalid_proof" {
						healthyProofLock := beforeLocks[badOutpoint]
						if healthyProofLock == nil || healthyProofLock.Reason != rgb11wallet.LockReasonRGB || healthyProofLock.ReservationID != "" {
							t.Fatalf("fixture proof carrier is not at a healthy RGB lock baseline: outpoint=%s lock=%+v", badOutpoint, healthyProofLock)
						}
					}
					wantErr := error(rgb11wallet.ErrInvalidProof)
					hits := func() int { return 0 }
					var snapshotFault *rgb11SnapshotReadFailureDB
					var proofFault *rgb11SnapshotProofReadFailureDB
					if kind == "invalid_proof" {
						fault := &rgb11SnapshotProofReadFailureDB{KVDB: originalDB, outpoint: proofs[0].OutPoint, validationHash: proofs[0].ValidationHash}
						proofFault = fault
						manager.db = fault
						hits = func() int { return fault.hits }
					} else {
						wantErr = errors.New("temporary local RGB snapshot read unavailable")
						fault := &rgb11SnapshotReadFailureDB{KVDB: originalDB, prefix: []byte("rgb11-" + rgb11StorageScope(manager.status.CurrentWallet, manager.status.CurrentAccount) + "-"), failure: wantErr}
						snapshotFault = fault
						manager.db = fault
						hits = func() int { return fault.hits }
					}
					faultEvidence := &historicalErrorEvidence{BitcoinEvidenceProvider: evidence}
					manager.rgbManager, err = newRGB11Manager(manager, manager.db, manager.utxoLockerL1, faultEvidence)
					if err != nil {
						t.Fatal(err)
					}
					manager.rgbManager.scopeStates = originalRuntime.scopeStates
					manager.rgbManager.consistencyStatus = originalRuntime.consistencyStatus
					if err := manager.rgbManager.selectRGB11Scope(); err != nil {
						t.Fatal(err)
					}
					faultRuntime := manager.rgbManager
					if snapshotFault != nil {
						// Arm only after manager/scope setup so the injection targets refresh.
						snapshotFault.armed = true
						t.Cleanup(func() { snapshotFault.armed = false })
					}
					if proofFault != nil {
						// Arm only after manager/scope setup so the injection targets refresh.
						proofFault.enabled = true
						t.Cleanup(func() { proofFault.enabled = false })
					}
					defer func() {
						manager.db = originalDB
						manager.rgbManager = originalRuntime
						originalRuntime.evidence = evidence
					}()
					var result *RGB11RefreshResult
					var refreshErr error
					if path == "public" {
						result, refreshErr = manager.RefreshRGB11State(ctx)
					} else {
						result, refreshErr = manager.rgbManager.RefreshRGB11State(ctx)
					}
					if snapshotFault != nil {
						// State inspection reads intact records while preserving the closed RAM gate.
						snapshotFault.armed = false
					}
					if hits() == 0 || !errors.Is(refreshErr, wantErr) {
						t.Fatalf("wrong local failure: hits=%d err=%v", hits(), refreshErr)
					}
					if kind == "invalid_proof" {
						if result == nil {
							t.Fatal("invalid proof lost its partial refresh diagnostic")
						}
						if !containsString(result.Inconsistent, proofs[0].OutPoint) {
							t.Fatalf("partial refresh did not identify the damaged proof: outpoint=%s inconsistent=%v", proofs[0].OutPoint, result.Inconsistent)
						}
						if result.Settled != healthyResult.Settled || result.Pending != healthyResult.Pending ||
							result.Reorged != healthyResult.Reorged || result.Conflicted != healthyResult.Conflicted ||
							result.Unresolved != healthyResult.Unresolved {
							t.Fatalf("invalid proof fabricated transfer counters: result=%+v healthy=%+v", result, healthyResult)
						}
					} else if result != nil {
						t.Fatalf("unavailable full snapshot unexpectedly returned a refresh result: %+v", result)
					}
					// Both paths must expose the actual local snapshot failure;
					// a background backup is no longer a business precondition.
					failedState, stateErr := manager.GetRGB11State()
					if stateErr != nil || failedState == nil {
						t.Fatalf("history read failed: %v", stateErr)
					}
					t.Logf("local failure path=%s kind=%s available_count=%d readiness=%t consistency=%s hits=%d", path, kind, len(failedState.AvailableAssets), manager.rgbManager.rgb11ProjectionAvailable(), failedState.ConsistencyStatus, hits())
					if len(failedState.AvailableAssets) != 0 || manager.rgbManager.rgb11ProjectionAvailable() {
						t.Fatal("snapshot failure left live ownership available")
					}
					wantConsistency := "warning"
					if kind == "invalid_proof" {
						wantConsistency = "broken"
					}
					if failedState.ConsistencyStatus != wantConsistency {
						t.Fatalf("failure status=%s want=%s", failedState.ConsistencyStatus, wantConsistency)
					}
					// A broken scope intentionally blocks complete backup export. Audit
					// underlying bytes and typed records without reopening readiness.
					afterRawDBRows := rgb11SnapshotRawDBRecords(t, originalDB, scope)
					afterProjectionRows, err := originalRuntime.projectionStore.ReadLocalRecords()
					if err != nil {
						t.Fatalf("read local projection rows during fault: %v", err)
					}
					afterEngineRows, err := originalRuntime.engineStore.ExportSnapshot()
					if err != nil {
						t.Fatalf("read typed engine rows during fault: %v", err)
					}
					afterOutputs, err := originalRuntime.projectionStore.ListOutputs()
					if err != nil {
						t.Fatal(err)
					}
					afterProofs, err := originalRuntime.projectionStore.ListProofs()
					if err != nil {
						t.Fatal(err)
					}
					afterTransfers, err := originalRuntime.projectionStore.ListTransfers()
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(beforeRawDBRows, afterRawDBRows) ||
						!reflect.DeepEqual(beforeProjectionRows, afterProjectionRows) ||
						!reflect.DeepEqual(beforeEngineRows, afterEngineRows) {
						t.Fatalf("snapshot fault changed immutable scoped recovery records: raw_rows=%d/%d projection_rows=%d/%d engine_rows=%d/%d",
							len(beforeRawDBRows), len(afterRawDBRows), len(beforeProjectionRows), len(afterProjectionRows),
							len(beforeEngineRows), len(afterEngineRows))
					}
					if !reflect.DeepEqual(proofs, afterProofs) || !reflect.DeepEqual(beforeOutputs, afterOutputs) ||
						!reflect.DeepEqual(beforeTransfers, afterTransfers) {
						t.Fatal("snapshot fault changed typed proof/output/transfer records")
					}
					if manager.rgbManager.rgb11ProjectionAvailable() {
						t.Fatal("read-only recovery audit reopened the faulted readiness gate")
					}
					if kind == "invalid_proof" {
						hitsBeforeBackup := hits()
						backupSnapshot, _, backupErr := originalRuntime.exportRGB11WalletSnapshot(walletID)
						if hits() <= hitsBeforeBackup || backupSnapshot != nil ||
							!errors.Is(backupErr, rgb11wallet.ErrInvalidProof) {
							t.Fatalf("complete backup did not reject injected invalid proof %s: hits=%d->%d snapshot_present=%t err=%v",
								proofs[0].OutPoint, hitsBeforeBackup, hits(), backupSnapshot != nil, backupErr)
						}
						if manager.rgbManager.rgb11ProjectionAvailable() {
							t.Fatal("blocked backup check reopened the faulted readiness gate")
						}
					} else {
						after, _, backupErr := originalRuntime.exportRGB11WalletSnapshot(walletID)
						if backupErr != nil || !reflect.DeepEqual(before, after) {
							t.Fatalf("read failure changed a healthy complete backup: %v", backupErr)
						}
					}
					// Keep the fault runtime installed; only remove the injected read error.
					if proofFault != nil {
						proofFault.enabled = false
					}
					if snapshotFault != nil {
						snapshotFault.armed = false
					}
					afterLocks := manager.utxoLockerL1.GetLockedUtxoList()
					if kind == "invalid_proof" {
						for outpoint, beforeLock := range beforeLocks {
							if outpoint != badOutpoint && !reflect.DeepEqual(beforeLock, afterLocks[outpoint]) {
								t.Fatalf("invalid proof changed unrelated carrier lock: %s", outpoint)
							}
						}
						for outpoint, afterLock := range afterLocks {
							if outpoint != badOutpoint && !reflect.DeepEqual(afterLock, beforeLocks[outpoint]) {
								t.Fatalf("invalid proof added unrelated carrier lock: %s", outpoint)
							}
						}
						quarantineLock := afterLocks[badOutpoint]
						if quarantineLock == nil || (quarantineLock.Reason != rgb11wallet.LockReasonRGB && quarantineLock.Reason != rgb11wallet.LockReasonPending) {
							t.Fatalf("damaged proof carrier is not quarantined from BTC selection: lock=%+v", quarantineLock)
						}
					} else if !reflect.DeepEqual(beforeLocks, afterLocks) {
						t.Fatal("unavailable snapshot read changed carrier locks")
					}
					afterBalance, err := originalRuntime.GetRGB11AssetBalance(&imported.AssetName)
					if err != nil || beforeBalance.Cmp(afterBalance) != 0 {
						t.Fatalf("snapshot failure changed total balance: %v", err)
					}
					if !bytes.Equal(beforeRemote, recoveryRecords()) {
						t.Fatal("local snapshot failure changed remote account recovery records")
					}
					if faultEvidence.broadcasts != 0 {
						t.Fatal("snapshot failure broadcast a transaction")
					}
					if manager.rgbManager != faultRuntime {
						t.Fatal("fault runtime was replaced before the healthy retry")
					}
					if manager.rgbManager.consistencyStatus != wantConsistency || manager.rgbManager.rgb11ProjectionAvailable() {
						t.Fatalf("disarming the read fault changed the pre-retry failure state: consistency=%s gate=%t",
							manager.rgbManager.consistencyStatus, manager.rgbManager.rgb11ProjectionAvailable())
					}
					hitsBeforeRetry := hits()
					retryResult, err := manager.RefreshRGB11State(ctx)
					if err != nil {
						t.Fatalf("healthy retry on the same runtime: %v", err)
					}
					if hits() != hitsBeforeRetry {
						t.Fatalf("healthy retry still hit the disabled read fault: hits=%d->%d", hitsBeforeRetry, hits())
					}
					if manager.rgbManager != faultRuntime {
						t.Fatal("healthy retry replaced the fault runtime")
					}
					recovered, err := manager.GetRGB11State()
					if err != nil || recovered == nil || recovered.ConsistencyStatus != "ok" ||
						len(recovered.AvailableAssets) == 0 || !manager.rgbManager.rgb11ProjectionAvailable() {
						t.Fatalf("healthy retry did not recover the same runtime: state=%+v err=%v gate=%t",
							recovered, err, manager.rgbManager.rgb11ProjectionAvailable())
					}
					if retryResult == nil || len(retryResult.Inconsistent) != 0 ||
						retryResult.Settled != healthyResult.Settled || retryResult.Pending != healthyResult.Pending ||
						retryResult.Reorged != healthyResult.Reorged || retryResult.Conflicted != healthyResult.Conflicted ||
						retryResult.Unresolved != healthyResult.Unresolved {
						t.Fatalf("healthy retry changed transfer diagnostics: result=%+v healthy=%+v", retryResult, healthyResult)
					}
					recoveredRawDBRows := rgb11SnapshotRawDBRecords(t, originalDB, scope)
					recoveredProjectionRows, err := manager.rgbManager.projectionStore.ReadLocalRecords()
					if err != nil {
						t.Fatalf("read projection rows after healthy retry: %v", err)
					}
					recoveredEngineRows, err := manager.rgbManager.engineStore.ExportSnapshot()
					if err != nil {
						t.Fatalf("read engine rows after healthy retry: %v", err)
					}
					recoveredProofs, err := manager.rgbManager.projectionStore.ListProofs()
					if err != nil {
						t.Fatal(err)
					}
					recoveredOutputs, err := manager.rgbManager.projectionStore.ListOutputs()
					if err != nil {
						t.Fatal(err)
					}
					recoveredTransfers, err := manager.rgbManager.projectionStore.ListTransfers()
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(beforeRawDBRows, recoveredRawDBRows) ||
						!reflect.DeepEqual(beforeProjectionRows, recoveredProjectionRows) ||
						!reflect.DeepEqual(beforeEngineRows, recoveredEngineRows) ||
						!reflect.DeepEqual(proofs, recoveredProofs) || !reflect.DeepEqual(beforeOutputs, recoveredOutputs) ||
						!reflect.DeepEqual(beforeTransfers, recoveredTransfers) {
						t.Fatal("same-runtime recovery changed existing scoped RGB11 records")
					}
					recoveredSnapshot, _, snapshotErr := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
					if snapshotErr != nil || !reflect.DeepEqual(before, recoveredSnapshot) {
						t.Fatalf("same-runtime recovery changed complete local backup: %v", snapshotErr)
					}
					recoveredBalance, err := manager.rgbManager.GetRGB11AssetBalance(&imported.AssetName)
					if err != nil || beforeBalance.Cmp(recoveredBalance) != 0 {
						t.Fatalf("same-runtime recovery changed total balance: %v", err)
					}
					if !bytes.Equal(beforeRemote, recoveryRecords()) {
						t.Fatal("same-runtime recovery changed remote account recovery records")
					}
					if faultEvidence.broadcasts != 0 {
						t.Fatal("snapshot recovery broadcast a transaction")
					}
					recoveredLocks := manager.utxoLockerL1.GetLockedUtxoList()
					if kind == "read_unavailable" {
						if !reflect.DeepEqual(beforeLocks, recoveredLocks) {
							t.Fatal("healthy retry after a temporary read fault changed carrier locks")
						}
					} else {
						var recoveredTargetProof *rgb11wallet.AllocationProof
						for _, proof := range recoveredProofs {
							if proof.OutPoint == badOutpoint && proof.AssetName == proofs[0].AssetName {
								recoveredTargetProof = proof
								break
							}
						}
						if recoveredTargetProof == nil || !rgb11ProofIsAvailable(recoveredTargetProof, rgb11ProofConfirmationRequirements(recoveredTransfers)) {
							t.Fatalf("healthy retry did not restore the target proof to an available state: outpoint=%s proof=%+v", badOutpoint, recoveredTargetProof)
						}
						expectedTargetLock := cloneLockedUtxo(beforeLocks[badOutpoint])
						recoveredTargetLock := recoveredLocks[badOutpoint]
						if recoveredTargetLock == nil || recoveredTargetLock.Reason != rgb11wallet.LockReasonRGB ||
							!reflect.DeepEqual(recoveredTargetLock, expectedTargetLock) {
							t.Fatalf("healthy retry did not restore the target carrier lock and preserve its reservation/owner/value/assets: outpoint=%s want=%+v got=%+v",
								badOutpoint, expectedTargetLock, recoveredTargetLock)
						}
						for outpoint, beforeLock := range beforeLocks {
							if outpoint != badOutpoint && !reflect.DeepEqual(beforeLock, recoveredLocks[outpoint]) {
								t.Fatalf("same-runtime recovery changed unrelated carrier lock: %s", outpoint)
							}
						}
						for outpoint, afterLock := range recoveredLocks {
							if outpoint != badOutpoint && !reflect.DeepEqual(afterLock, beforeLocks[outpoint]) {
								t.Fatalf("same-runtime recovery added unrelated carrier lock: %s", outpoint)
							}
						}
					}
				})
			}
		})
	}
}
