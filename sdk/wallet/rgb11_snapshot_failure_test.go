package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Fail only the full snapshot scan, leaving ordinary list/read APIs usable.
type rgb11SnapshotReadFailureDB struct {
	indexer.KVDB
	prefix  []byte
	failure error
	hits    int
}

func (d *rgb11SnapshotReadFailureDB) BatchRead(prefix []byte, reverse bool, visit func([]byte, []byte) error) error {
	if bytes.Equal(prefix, d.prefix) {
		d.hits++
		return d.failure
	}
	return d.KVDB.BatchRead(prefix, reverse, visit)
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
					if _, err := manager.RefreshRGB11State(ctx); err != nil {
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
					rootWallet, err := manager.accountManagementRootWallet()
					if err != nil {
						t.Fatal(err)
					}
					stateKey, err := manager.accountManagedStateKey(rootWallet)
					if err != nil {
						t.Fatal(err)
					}
					dataKey, err := manager.accountManagedDataBlobKey(rootWallet)
					if err != nil {
						t.Fatal(err)
					}
					recoveryRecords := func() []byte {
						remote.mu.Lock()
						defer remote.mu.Unlock()
						records := make(map[string]any)
						for _, key := range []string{stateKey, dataKey} {
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
					beforeLocks := manager.utxoLockerL1.GetLockedUtxoList()
					beforeBalance, err := originalRuntime.GetRGB11AssetBalance(&imported.AssetName)
					if err != nil {
						t.Fatal(err)
					}
					proofs, err := originalRuntime.projectionStore.ListProofs()
					if err != nil || len(proofs) == 0 {
						t.Fatal("missing original proof")
					}
					wantErr := error(rgb11wallet.ErrInvalidProof)
					hits := func() int { return 0 }
					if kind == "invalid_proof" {
						fault := &historicalProofReadDB{KVDB: originalDB, outpoint: proofs[0].OutPoint, validationHash: proofs[0].ValidationHash}
						manager.db = fault
						hits = func() int { return fault.hits }
					} else {
						wantErr = errors.New("temporary local RGB snapshot read unavailable")
						fault := &rgb11SnapshotReadFailureDB{KVDB: originalDB, prefix: []byte("rgb11-" + rgb11StorageScope(manager.status.CurrentWallet, manager.status.CurrentAccount) + "-"), failure: wantErr}
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
					if hits() == 0 || !errors.Is(refreshErr, wantErr) {
						t.Fatalf("wrong local failure: hits=%d err=%v", hits(), refreshErr)
					}
					if result != nil {
						t.Fatal("failed snapshot read returned a published refresh result")
					}
					if path == "public" && !strings.Contains(refreshErr.Error(), "account-managed baseline") {
						t.Fatalf("public failure did not exercise the account precondition: %v", refreshErr)
					}
					failedState, stateErr := manager.GetRGB11State()
					if stateErr != nil || failedState == nil {
						t.Fatalf("history read failed: %v", stateErr)
					}
					t.Logf("local failure path=%s kind=%s available_count=%d readiness=%t consistency=%s hits=%d", path, kind, len(failedState.AvailableAssets), manager.rgbManager.rgb11ProjectionAvailable(), failedState.ConsistencyStatus, hits())
					if len(failedState.AvailableAssets) != 0 || manager.rgbManager.rgb11ProjectionAvailable() {
						t.Fatal("snapshot failure left live ownership available")
					}
					if path == "internal" && failedState.ConsistencyStatus != "broken" {
						t.Fatalf("internal failure status=%s", failedState.ConsistencyStatus)
					}
					// The fault changes reads, not stored bytes. Restore the healthy view
					// before auditing durable data; the closed RAM gate was checked above.
					manager.db = originalDB
					manager.rgbManager = originalRuntime
					originalRuntime.evidence = evidence
					if !reflect.DeepEqual(beforeLocks, manager.utxoLockerL1.GetLockedUtxoList()) {
						t.Fatal("snapshot failure changed carrier locks")
					}
					after, _, err := originalRuntime.exportRGB11WalletSnapshot(walletID)
					if err != nil || !reflect.DeepEqual(before, after) {
						t.Fatalf("snapshot failure changed durable recovery data: %v", err)
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
					if _, err := manager.RefreshRGB11State(ctx); err != nil {
						t.Fatalf("healthy retry: %v", err)
					}
					recovered, err := manager.GetRGB11State()
					if err != nil || recovered == nil || recovered.ConsistencyStatus != "ok" || len(recovered.AvailableAssets) == 0 {
						t.Fatalf("healthy retry did not recover: %v", err)
					}
				})
			}
		})
	}
}
