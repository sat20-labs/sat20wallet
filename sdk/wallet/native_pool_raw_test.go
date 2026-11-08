package wallet

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type nativeRawFixture struct {
	manager          *Manager
	evidence         *rgb11AddressEvidence
	asset            *RGB11ImportResult
	pending          *rgb11wallet.PendingTransfer
	walletID, source string
}

func newNativeRawFixture(t *testing.T, prepareSend bool) *nativeRawFixture {
	t.Helper()
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	sender, receiver, asset, evidence, _ := newRGB11ValidContractSpendFixture(t)
	// This unit fixture owns its verification lifecycle. The three actual
	// contract regressions keep their ordinary production monitors running.
	sender.rgbManager.scopeStates.stopReconciliations()
	receiver.rgbManager.scopeStates.stopReconciliations()
	if _, ok := sender.db.(indexer.AtomicWriteBatchDB); !ok {
		t.Fatal("fixture lacks a real atomic wallet backend")
	}
	if !prepareSend {
		// Strict-export fault controls need a real carrier whose quarantine can
		// commit. Prepared-owner conflicts are a separate observed failure.
		walletID, err := sender.rgbManager.RGB11WalletID()
		if err != nil {
			t.Fatal(err)
		}
		return &nativeRawFixture{manager: sender, evidence: evidence, asset: asset,
			walletID: walletID, source: rgb11GenericFixtureSource(t, sender, asset)}
	}
	request, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: asset.ContractID,
		AmountRaw: "10000", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: request.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.State.Status != "prepared" || len(pending.RecipientConsignment) == 0 ||
		len(pending.LocalConsignment) == 0 || len(pending.SignedTx) == 0 || len(pending.SignedPSBT) == 0 {
		t.Fatal("public preparation did not persist complete resumable pending material")
	}
	for hash, body := range map[string][]byte{
		pending.RecipientObjectHash: pending.RecipientConsignment,
		pending.LocalObjectHash:     pending.LocalConsignment,
	} {
		stored, err := sender.rgbManager.projectionStore.LoadObject(hash)
		if err != nil || !bytes.Equal(stored, body) {
			t.Fatal("pending material does not match its real immutable object")
		}
	}
	walletID, err := sender.rgbManager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	return &nativeRawFixture{manager: sender, evidence: evidence, asset: asset,
		pending: pending, walletID: walletID,
		source: rgb11GenericFixtureSource(t, sender, asset)}
}

func (f *nativeRawFixture) local(t *testing.T) *RGB11WalletSnapshot {
	t.Helper()
	runtime := f.manager.rgbManager
	release := runtime.beginRGB11ReservationRead()
	defer release()
	snapshot, err := runtime.readRGB11LocalLocked(f.walletID)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func (f *nativeRawFixture) strict(t *testing.T) *RGB11WalletSnapshot {
	t.Helper()
	snapshot, encoded, err := f.manager.rgbManager.readRGB11StrictSnapshot(f.walletID, true)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == nil || encoded != nil {
		t.Fatal("strict raw read must return the local baseline, not a backup body")
	}
	return snapshot
}

// Include the account profile/generation when checking that a read stays read-only.
func nativeRawAllRows(t *testing.T, manager *Manager) map[string][]byte {
	t.Helper()
	rows := make(map[string][]byte)
	if err := manager.db.BatchRead([]byte{}, false, func(key, value []byte) error {
		rows[string(key)] = append([]byte(nil), value...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return rows
}

func (f *nativeRawFixture) immutable(t *testing.T) {
	t.Helper()
	if f.pending != nil {
		stored, err := f.manager.rgbManager.projectionStore.LoadPendingTransfer(f.pending.State.TransferID)
		if err != nil || !reflect.DeepEqual(stored, f.pending) {
			t.Fatal("candidate changed pending amount, lifecycle, fixed tx/PSBT or consignment material")
		}
	}
	f.evidence.mu.Lock()
	broadcasts := f.evidence.broadcastCount
	f.evidence.mu.Unlock()
	if broadcasts != 0 {
		t.Fatal("snapshot/CAS regression broadcast a transaction")
	}
}

// Pause only the first read of this actual carrier. Other local operations
// keep using the same genuine evidence provider and may finish before release.
type nativeRawReadGate struct {
	rgb11wallet.BitcoinEvidenceProvider
	point            string
	ctx              context.Context
	chosen           atomic.Bool
	entered, release chan struct{}
}

func (g *nativeRawReadGate) GetOutspend(point string) (*rgb11wallet.BitcoinOutspend, error) {
	if point == g.point && g.chosen.CompareAndSwap(false, true) {
		close(g.entered)
		select {
		case <-g.release:
		case <-g.ctx.Done():
			return nil, g.ctx.Err()
		}
	}
	return g.BitcoinEvidenceProvider.GetOutspend(point)
}

func TestNativePoolRawCAS(t *testing.T) {
	for _, mode := range []string{"quiet", "newer_receive", "strict_read", "proof_fault"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeRawFixture(t, mode != "proof_fault")
			runtime := f.manager.rgbManager
			current := f.strict(t)
			if !reflect.DeepEqual(current, f.local(t)) {
				t.Fatal("strict raw baseline differs from the real local records")
			}
			switch mode {
			case "quiet":
				locks := f.manager.utxoLockerL1.GetLockedUtxoList()
				rows := nativeRawAllRows(t, f.manager)
				backup, _, err := runtime.exportRGB11WalletSnapshot(f.walletID)
				if err != nil {
					t.Fatal(err)
				}
				pending, err := rgb11wallet.PendingTransfersFromProjectionSnapshot(backup.ProjectionRecords)
				if err != nil || len(pending) != 1 || len(pending[0].RecipientConsignment) != 0 ||
					len(pending[0].LocalConsignment) != 0 || pending[0].RecipientObjectHash != f.pending.RecipientObjectHash ||
					pending[0].LocalObjectHash != f.pending.LocalObjectHash {
					t.Fatal("backup did not retain its original reference-only representation")
				}
				if !rgb11SnapshotChanged(backup, current) {
					t.Fatal("fixture did not expose the original encoding mismatch")
				}
				reread := f.strict(t)
				if !reflect.DeepEqual(reread, current) || !reflect.DeepEqual(rows,
					nativeRawAllRows(t, f.manager)) {
					t.Fatal("strict raw read changed persisted RGB records")
				}
				// A backup-shaped baseline must remain rejected by the raw CAS.
				// The fix supplies the right baseline; it does not weaken CAS.
				if _, err = runtime.applyRGB11CandidateSnapshot(context.Background(), backup, backup); !errors.Is(err, errRGB11StaleLocal) || !strings.Contains(err.Error(), "publish RGB11 candidate projection") {
					t.Fatalf("normalized baseline bypassed raw comparison: %v", err)
				}
				for attempt := 0; attempt < 2; attempt++ {
					fresh := f.strict(t)
					if _, err := runtime.applyRGB11CandidateSnapshot(context.Background(), fresh, fresh); err != nil {
						t.Fatalf("unchanged real pending failed atomic publication: attempt=%d err=%v", attempt, err)
					}
					if !reflect.DeepEqual(current, f.local(t)) || !reflect.DeepEqual(locks,
						f.manager.utxoLockerL1.GetLockedUtxoList()) {
						t.Fatal("quiet publication changed an existing record, amount or carrier owner")
					}
				}
			case "newer_receive":
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				gate := &nativeRawReadGate{BitcoinEvidenceProvider: f.evidence,
					point: f.source, ctx: ctx, entered: make(chan struct{}), release: make(chan struct{})}
				runtime.evidence = gate
				defer func() { runtime.evidence = f.evidence }()
				var once sync.Once
				release := func() { once.Do(func() { close(gate.release) }) }
				done := make(chan error, 1)
				joined := false
				go func() {
					_, err := runtime.applyRGB11CandidateSnapshot(ctx, current, current)
					done <- err
				}()
				defer func() {
					release()
					cancel()
					if !joined {
						select {
						case <-done:
						case <-time.After(3 * time.Second):
							t.Error("old candidate did not exit after gate release/cancel")
						}
					}
				}()
				select {
				case <-gate.entered:
				case err := <-done:
					joined = true
					t.Fatalf("old candidate missed exact carrier read: %v", err)
				case <-ctx.Done():
					t.Fatal("old candidate did not reach its carrier evidence read")
				}
				type receiveResult struct {
					id  string
					err error
				}
				received := make(chan receiveResult, 1)
				receiveJoined := false
				go func() {
					request, err := f.manager.CreateRGB11Invoice(RGB11InvoiceRequest{
						Mode: "witness", TransportMode: "out-of-band", ContractID: f.asset.ContractID,
						AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
					})
					result := receiveResult{err: err}
					if request != nil {
						result.id = request.RequestID
					}
					received <- result
				}()
				defer func() {
					release()
					if !receiveJoined {
						select {
						case <-received:
						case <-time.After(3 * time.Second):
							t.Error("new receive did not exit after gate release")
						}
					}
				}()
				var added receiveResult
				select {
				case added = <-received:
					receiveJoined = true
				case <-ctx.Done():
					t.Fatal("public receive waited for another candidate's remote evidence")
				}
				if added.err != nil || added.id == "" {
					t.Fatalf("public receive failed: %v", added.err)
				}
				select {
				case <-gate.release:
					t.Fatal("receive completed only after evidence release")
				default:
				}
				next := f.local(t)
				if !rgb11SnapshotChanged(current, next) {
					t.Fatal("public receive did not change the persisted baseline")
				}
				id := rgb11TransferResvID(runtime.rgb11ScopeKey(), "receive:"+added.id)
				if f.manager.GetResv(id) == nil {
					t.Fatal("new receive missing from owner RAM")
				}
				if _, err := LoadReservation(f.manager.db, nil, RESV_TYPE_RGB11, id); err != nil {
					t.Fatal("new receive missing from durable reservations")
				}
				locks := f.manager.utxoLockerL1.GetLockedUtxoList()
				release()
				select {
				case err := <-done:
					joined = true
					if !errors.Is(err, errRGB11StaleLocal) || !strings.Contains(err.Error(), "publish RGB11 candidate projection") {
						t.Fatalf("new receive did not invalidate old candidate: %v", err)
					}
				case <-ctx.Done():
					t.Fatal("old candidate did not finish after evidence release")
				}
				if !reflect.DeepEqual(next, f.local(t)) || !reflect.DeepEqual(locks,
					f.manager.utxoLockerL1.GetLockedUtxoList()) {
					t.Fatal("rejected old candidate overwrote a new receive, record or carrier owner")
				}
				if _, err := runtime.engine.LoadReceive(added.id); err != nil {
					t.Fatal(err)
				}
				fresh := f.strict(t)
				if _, err := runtime.applyRGB11CandidateSnapshot(ctx, fresh, fresh); err != nil {
					t.Fatalf("fresh rebuilt baseline did not publish: %v", err)
				}
				if !reflect.DeepEqual(next, f.local(t)) {
					t.Fatal("fresh publication discarded local receive data")
				}
			case "strict_read":
				sentinel := errors.New("strict snapshot read unavailable")
				fault := &rgb11SnapshotReadFailureDB{KVDB: f.manager.db, armed: true,
					prefix: []byte("rgb11-" + runtime.rgb11ScopeKey() + "-"), failure: sentinel}
				original := runtime.projectionStore
				runtime.projectionStore = rgb11wallet.NewProjectionStore(fault, f.manager.utxoLockerL1)
				defer func() { runtime.projectionStore = original }()
				if err := runtime.projectionStore.SetScope(runtime.rgb11ScopeKey()); err != nil {
					t.Fatal(err)
				}
				for _, rawLocal := range []bool{false, true} {
					snapshot, _, err := runtime.readRGB11StrictSnapshot(f.walletID, rawLocal)
					if snapshot != nil || !errors.Is(err, sentinel) {
						t.Fatalf("raw=%t bypassed strict read failure: %v", rawLocal, err)
					}
				}
				if fault.hits == 0 {
					t.Fatal("strict snapshot read failure was not exercised")
				}
				runtime.projectionStore = original
				if !reflect.DeepEqual(current, f.local(t)) {
					t.Fatal("strict read failure changed durable records")
				}
			case "proof_fault":
				carrierLock := f.manager.utxoLockerL1.GetLockedUtxoList()[f.source]
				if f.pending != nil || carrierLock == nil || carrierLock.ReservationID != "" || carrierLock.Reason != rgb11wallet.LockReasonRGB {
					t.Fatal("proof-fault control needs its real RGB carrier without a pending owner")
				}
				sentinel := errors.New("actual carrier evidence unavailable")
				fault := &historicalErrorEvidence{BitcoinEvidenceProvider: f.evidence,
					outpoint: f.source, failure: sentinel, persistent: true, keepStatus: true}
				runtime.evidence = fault
				_, refreshErr := runtime.RefreshRGB11State(context.Background())
				runtime.evidence = f.evidence
				faultState := runtime.rgb11ScopeState()
				targetFault := false
				for _, item := range faultState.ProofFaults {
					targetFault = targetFault || (item.OutPoint == f.source && item.AssetName == f.asset.AssetName && errors.Is(item.Cause, sentinel))
				}
				lock := f.manager.utxoLockerL1.GetLockedUtxoList()[f.source]
				errorText := "<nil>"
				if refreshErr != nil {
					errorText = strings.ReplaceAll(refreshErr.Error(), f.source, "<target>")
				}
				t.Logf("proof fault setup: refresh_error_nil=%t error_type=%T evidence_cause=%t owner_conflict=%t target_hits=%d proof_faults=%d target_fault=%t target_locked=%t pending_owner=%t error=%s",
					refreshErr == nil, refreshErr, errors.Is(refreshErr, sentinel), errors.Is(refreshErr, ErrUtxoReservationOwner), fault.hits,
					len(faultState.ProofFaults), targetFault, lock != nil, lock != nil && f.pending != nil && lock.ReservationID == f.pending.ReservationID, errorText)
				if refreshErr == nil || fault.hits == 0 || len(faultState.ProofFaults) == 0 || !targetFault {
					t.Fatalf("real refresh did not establish the requested proof fault: refresh_error_nil=%t target_hits=%d proof_faults=%d error=%s",
						refreshErr == nil, fault.hits, len(faultState.ProofFaults), errorText)
				}
				faultRows := f.local(t)
				for _, rawLocal := range []bool{false, true} {
					snapshot, _, err := runtime.readRGB11StrictSnapshot(f.walletID, rawLocal)
					if snapshot != nil || !errors.Is(err, ErrRGB11Inconsistent) || !errors.Is(err, sentinel) {
						t.Fatalf("raw=%t bypassed the real proof-fault gate: %v", rawLocal, err)
					}
				}
				if !reflect.DeepEqual(faultRows, f.local(t)) {
					t.Fatal("proof-fault rejection changed durable records")
				}
			}
			f.immutable(t)
		})
	}
}
