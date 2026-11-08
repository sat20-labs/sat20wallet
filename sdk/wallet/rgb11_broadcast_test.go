package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type controlledRGB11BroadcastEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	mu          sync.Mutex
	txID        string
	err         error
	broadcast   int
	accepted    int
	onBroadcast func()
	onAccepted  func([]byte, string) error
}

func (e *controlledRGB11BroadcastEvidence) Broadcast(raw []byte) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.broadcast++
	if e.err == nil && e.txID != "" && e.onAccepted != nil {
		e.accepted++
		if err := e.onAccepted(raw, e.txID); err != nil {
			return e.txID, err
		}
	}
	if e.onBroadcast != nil {
		e.onBroadcast()
	}
	return e.txID, e.err
}

func (e *controlledRGB11BroadcastEvidence) calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.broadcast
}

type failNthFlushDB struct {
	indexer.KVDB
	mu     sync.Mutex
	count  int
	failAt int
	hits   int
}

type failNthFlushBatch struct {
	indexer.WriteBatch
	owner *failNthFlushDB
}

func (db *failNthFlushDB) NewWriteBatch() indexer.WriteBatch {
	return &failNthFlushBatch{WriteBatch: db.KVDB.NewWriteBatch(), owner: db}
}

func (batch *failNthFlushBatch) Flush() error {
	batch.owner.mu.Lock()
	batch.owner.count++
	fail := batch.owner.count == batch.owner.failAt
	if fail {
		batch.owner.hits++
	}
	batch.owner.mu.Unlock()
	if fail {
		return errors.New("injected RGB11 post-broadcast persistence failure")
	}
	return batch.WriteBatch.Flush()
}

func setRGB11BroadcastTestStore(t testing.TB, manager *Manager, db indexer.KVDB) *rgb11wallet.ProjectionStore {
	t.Helper()
	store := rgb11wallet.NewProjectionStore(db, manager.utxoLockerL1)
	if err := store.SetScope(rgb11StorageScope(manager.status.CurrentWallet, manager.status.CurrentAccount)); err != nil {
		t.Fatal(err)
	}
	manager.rgbManager.projectionStore = store
	return store
}

// Broadcast-response tests begin after authorization and signing, using a real
// Bitcoin signature. The expired-cancel fixture itself remains unsigned.
func newRGB11BroadcastFixture(t *testing.T) *expiredCancelFixture {
	t.Helper()
	return signRGBBroadcastFixture(t, newExpiredCancelFixture(t, 1))
}

// Only these broadcast-response groups establish an account baseline. The
// original fixture remains unchanged for callers testing another boundary.
func newRGBBroadcastGroup(t *testing.T, state accountGroupState) *expiredCancelFixture {
	t.Helper()
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	fixture, seeds := buildExpiredCancelFixture(t, 1)
	if state == accountGroupReady {
		info := fixture.manager.walletInfoMap[fixture.manager.status.CurrentWallet]
		if info == nil {
			t.Fatal("active broadcast group has no original wallet metadata")
		}
		if info.Name == "" {
			info.Name = "RGB broadcast"
		}
		prepareRGBGroup(t, fixture.manager, state,
			"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "password")
	} else {
		prepareRGBGroup(t, fixture.manager, state)
	}
	seedExpiredCancelFixture(t, fixture, seeds)
	return signRGBBroadcastFixture(t, fixture)
}

func signRGBBroadcastFixture(t *testing.T, fixture *expiredCancelFixture) *expiredCancelFixture {
	t.Helper()
	runtime := fixture.manager.rgbManager
	pending, err := runtime.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	packet, err := psbt.NewFromRawBytes(bytes.NewReader(pending.SignedPSBT), false)
	if err != nil {
		t.Fatal(err)
	}
	script, err := AddrToPkScript(fixture.manager.wallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	packet.Inputs[0].WitnessUtxo = wire.NewTxOut(100_000, script)
	var prepared bytes.Buffer
	if err := packet.Serialize(&prepared); err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.wallet.SignPsbt(packet); err != nil {
		t.Fatal(err)
	}
	if err := psbt.MaybeFinalizeAll(packet); err != nil {
		t.Fatal(err)
	}
	tx, err := psbt.Extract(packet)
	if err != nil {
		t.Fatal(err)
	}
	var signed bytes.Buffer
	if err := tx.Serialize(&signed); err != nil {
		t.Fatal(err)
	}
	pending.SignedTx, pending.SignedPSBT = signed.Bytes(), prepared.Bytes()
	pending.State.Status, pending.State.AckStatus = "delivered", "accepted"
	pending.State.DeliveryAcknowledged = true
	pending.State.DeliveryRecordKey, pending.State.DeliveryRecordHash = "/mail/test/msg", "durable-record-hash"
	if err := runtime.projectionStore.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestRGB11BroadcastUnknownPersistsIrreversibleIntent(t *testing.T) {
	fixture := newRGBBroadcastGroup(t, accountGroupInactive)
	evidence := &controlledRGB11BroadcastEvidence{
		BitcoinEvidenceProvider: fixture.evidence,
		err:                     context.DeadlineExceeded,
	}
	runtime := fixture.manager.rgbManager
	runtime.evidence = evidence
	runtime.scopeStates = nil // keep the test deterministic; reconciliation is inspected explicitly below.
	pending, err := runtime.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
	if err != nil {
		t.Fatal(err)
	}

	txID, err := runtime.broadcastRGB11PendingBatch([]*rgb11wallet.PendingTransfer{pending}, nil)
	if !errors.Is(err, ErrRGB11BroadcastResultUnknown) || txID != fixture.witnessTxID {
		t.Fatalf("unexpected ambiguous broadcast result: txid=%s err=%v", txID, err)
	}
	if evidence.calls() != 1 {
		t.Fatalf("ambiguous-response fixture never reached broadcast: calls=%d", evidence.calls())
	}
	stored, err := runtime.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if stored.State.Status != rgb11StatusBroadcastAttempted ||
		!rgb11TransferKeepsInputsLocked(&stored.State) {
		t.Fatalf("ambiguous broadcast was not retained fail-closed: %+v", stored.State)
	}
	needsReconciliation, err := runtime.hasPendingRGB11ChainReconciliation()
	if err != nil || !needsReconciliation {
		t.Fatalf("broadcast intent was invisible to reconciliation: pending=%v err=%v", needsReconciliation, err)
	}
	if cancelErr := fixture.manager.rgbManager.CancelExpiredRGB11Transfer(fixture.transferIDs[0]); cancelErr == nil {
		t.Fatal("ambiguous broadcast intent remained cancellable")
	}
}

func TestRGB11BroadcastEmptyBackendTxIDRemainsUnknown(t *testing.T) {
	fixture := newRGBBroadcastGroup(t, accountGroupInactive)
	evidence := &controlledRGB11BroadcastEvidence{
		BitcoinEvidenceProvider: fixture.evidence,
		// A successful response without the expected txid is not proof that
		// the backend accepted this signed transaction.
		txID: "",
	}
	runtime := fixture.manager.rgbManager
	runtime.evidence = evidence
	runtime.scopeStates = nil
	pending, err := runtime.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
	if err != nil {
		t.Fatal(err)
	}

	txID, err := runtime.broadcastRGB11PendingBatch([]*rgb11wallet.PendingTransfer{pending}, nil)
	if !errors.Is(err, ErrRGB11BroadcastResultUnknown) || txID != fixture.witnessTxID {
		t.Fatalf("empty backend txid was treated as confirmed: txid=%s err=%v", txID, err)
	}
	if evidence.calls() != 1 {
		t.Fatalf("broadcast calls=%d, want 1", evidence.calls())
	}
	stored, err := runtime.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if stored.State.Status != rgb11StatusBroadcastAttempted ||
		!rgb11TransferKeepsInputsLocked(&stored.State) {
		t.Fatalf("empty-txid response did not retain the unresolved intent and input locks: %+v", stored.State)
	}
	if cancelErr := fixture.manager.rgbManager.CancelExpiredRGB11Transfer(fixture.transferIDs[0]); cancelErr == nil {
		t.Fatal("empty-txid response made the broadcast intent cancellable")
	}
}

func TestRGB11BroadcastPersistenceFailureRecoversFromBitcoinEvidence(t *testing.T) {
	sender, recipient, imported, bitcoinEvidence, rpc := newRGB11GenericSendFixture(t)
	sourceOutpoint := rgb11GenericFixtureSource(t, sender, imported)
	request, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID,
		AmountRaw: "20000", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
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
	observed := newRGB11AcceptedEvidence(bitcoinEvidence, rpc)
	evidence := &controlledRGB11BroadcastEvidence{
		BitcoinEvidenceProvider: observed,
		txID:                    prepared.TxID,
		onAccepted: func(raw []byte, txid string) error {
			return acceptRGB11FlowTx(bitcoinEvidence, rpc, raw, txid)
		},
	}
	runtime := sender.rgbManager
	runtime.evidence = evidence
	// Keep the real verified source frontier; stop only this test's workers so
	// the armed post-broadcast flush is not consumed by background activity.
	runtime.scopeStates.stopReconciliations()
	sourceProof, err := runtime.projectionStore.LoadProof(sourceOutpoint, imported.AssetName)
	if err != nil || sourceProof == nil || sourceProof.OutPoint != sourceOutpoint ||
		!runtime.rgb11CarrierAvailable(sourceOutpoint) {
		t.Fatalf("prepared original source is not verified: proof_present=%t err=%v", sourceProof != nil, err)
	}
	failingDB := &failNthFlushDB{KVDB: sender.db}
	evidence.onBroadcast = func() {
		failingDB.mu.Lock()
		failingDB.failAt = failingDB.count + 1
		failingDB.mu.Unlock()
	}
	store := setRGB11BroadcastTestStore(t, sender, failingDB)
	pending, err := store.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	// The public OOB entry records the explicitly reviewed recipient summary
	// before reaching this lower broadcast/persistence boundary.
	pending.State.AckStatus = "accepted-out-of-band"
	if err := store.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}

	logRGB11BroadcastState(t, "before-persistence", sender, prepared.State.TransferID, observed)
	txID, err := runtime.broadcastRGB11PendingBatch([]*rgb11wallet.PendingTransfer{pending}, nil)
	logRGB11BroadcastState(t, "after-persistence", sender, prepared.State.TransferID, observed)
	evidence.mu.Lock()
	calls, acceptedCalls := evidence.broadcast, evidence.accepted
	evidence.mu.Unlock()
	failingDB.mu.Lock()
	armed, hit, flushes := failingDB.failAt > 0, failingDB.hits, failingDB.count
	failingDB.mu.Unlock()
	t.Logf("rgb-broadcast-diag controlled_calls=%d accepted_entries=%d fault_armed=%t fault_hits=%d flushes=%d unknown=%t persistence=%t inconsistent=%t",
		calls, acceptedCalls, armed, hit, flushes, errors.Is(err, ErrRGB11BroadcastResultUnknown),
		errors.Is(err, ErrRGB11BroadcastPersistence), errors.Is(err, ErrRGB11Inconsistent))
	if !errors.Is(err, ErrRGB11BroadcastPersistence) || txID != prepared.TxID {
		t.Fatalf("unexpected persistence result: txid=%s err=%v", txID, err)
	}
	if evidence.calls() != 1 {
		t.Fatalf("broadcast calls=%d, want 1", evidence.calls())
	}
	failingDB.mu.Lock()
	failedFlush := failingDB.failAt > 0 && failingDB.count == failingDB.failAt
	failingDB.mu.Unlock()
	if !failedFlush {
		t.Fatal("post-broadcast local persistence fault did not hit its armed flush")
	}

	// Re-open the durable projection view as a restarted process would. The
	// pre-broadcast intent must have survived even though the final state write
	// failed after the backend accepted the transaction.
	stableStore := setRGB11BroadcastTestStore(t, sender, sender.db)
	stored, err := stableStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State.Status != rgb11StatusBroadcastAttempted {
		t.Fatalf("durable intent status=%s, want %s", stored.State.Status, rgb11StatusBroadcastAttempted)
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(stored.SignedTx)); err != nil {
		t.Fatal(err)
	}
	bitcoinEvidence.mu.Lock()
	bitcoinEvidence.rawTx[prepared.TxID] = append([]byte(nil), stored.SignedTx...)
	for _, input := range tx.TxIn {
		outpoint := input.PreviousOutPoint.String()
		bitcoinEvidence.spendingTx[outpoint] = prepared.TxID
		delete(bitcoinEvidence.utxos, outpoint)
		delete(rpc.outputs, outpoint)
	}
	for vout, output := range tx.TxOut {
		outpoint := fmt.Sprintf("%s:%d", prepared.TxID, vout)
		bitcoinEvidence.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: output.Value, PkScript: append([]byte(nil), output.PkScript...),
		}
		item := indexer.NewTxOutput(output.Value)
		item.OutPointStr, item.OutValue.PkScript = outpoint, append([]byte(nil), output.PkScript...)
		rpc.outputs[outpoint] = item
	}
	bitcoinEvidence.mu.Unlock()
	bitcoinEvidence.setStatus(prepared.TxID, rgb11wallet.BitcoinTxStatus{
		TxID: prepared.TxID, InMempool: true,
	})
	runtime.scopeStates = newRGB11ScopeStateRegistry()
	result, refreshErr := runtime.RefreshRGB11State(context.Background())
	if refreshErr != nil {
		t.Fatalf("reconcile durable broadcast intent: result=%+v err=%v", result, refreshErr)
	}
	recovered, err := stableStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State.Status != "pending" {
		t.Fatalf("recovered status=%s, want pending", recovered.State.Status)
	}
}

func TestRGB11AddressBroadcastUsesDurableIntent(t *testing.T) {
	fixture := newRGBBroadcastGroup(t, accountGroupInactive)
	evidence := &controlledRGB11BroadcastEvidence{
		BitcoinEvidenceProvider: fixture.evidence,
		err:                     context.DeadlineExceeded,
	}
	runtime := fixture.manager.rgbManager
	runtime.evidence = evidence
	runtime.scopeStates.stopReconciliations()
	pending, err := runtime.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	pending.State.Status = "delivered"
	pending.State.AckStatus = "accepted"
	pending.State.DeliveryAcknowledged = true
	pending.State.DeliveryRecordHash = "durable-record-hash"
	pending.State.DeliveryRecordKey = "/mail/test/msg"
	if err := runtime.projectionStore.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}
	txID, err := runtime.BroadcastRGB11AddressTransfer(fixture.transferIDs[0])
	if !errors.Is(err, ErrRGB11BroadcastResultUnknown) || txID != fixture.witnessTxID {
		t.Fatalf("address broadcast did not preserve ambiguous txid: txid=%s err=%v", txID, err)
	}
	stored, err := runtime.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if stored.State.Status != rgb11StatusBroadcastAttempted {
		t.Fatalf("address broadcast status=%s", stored.State.Status)
	}
}

func TestRGB11BroadcastResultPreservesNewerLocalState(t *testing.T) {
	for _, progress := range []bool{false, true} {
		name := "unchanged-control"
		if progress {
			name = "newer-settled-state"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newRGBBroadcastGroup(t, accountGroupInactive)
			manager := fixture.manager.rgbManager
			evidence := &controlledRGB11BroadcastEvidence{BitcoinEvidenceProvider: fixture.evidence, txID: fixture.witnessTxID}
			manager.evidence = evidence
			manager.scopeStates = nil
			pending, err := manager.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			var progressErr error
			if progress {
				evidence.onBroadcast = func() {
					current, err := manager.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
					if err != nil {
						progressErr = err
						return
					}
					current.State.Status = "settled"
					progressErr = manager.projectionStore.SavePendingTransferState(current)
				}
			}
			txid, broadcastErr := manager.broadcastRGB11PendingBatch([]*rgb11wallet.PendingTransfer{pending}, nil)
			if progressErr != nil {
				t.Fatalf("setup concurrent local commit: %v", progressErr)
			}
			if evidence.calls() != 1 || txid != fixture.witnessTxID {
				t.Fatalf("broadcast calls=%d txid=%s err=%v", evidence.calls(), txid, broadcastErr)
			}
			stored, err := manager.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			if progress {
				if stored.State.Status != "settled" {
					t.Errorf("late broadcast response overwrote newer state: %s", stored.State.Status)
				}
			} else if broadcastErr != nil || stored.State.Status != "broadcast" {
				t.Errorf("unchanged intent not advanced: status=%s err=%v", stored.State.Status, broadcastErr)
			}
		})
	}
}

// Preserve the real atomic backend while retaining this fixture's fault hook.
func (db *failNthFlushDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return &failNthFlushBatch{WriteBatch: db.KVDB.(indexer.AtomicWriteBatchDB).NewAtomicWriteBatch(), owner: db}
}
