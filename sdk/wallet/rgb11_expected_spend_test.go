package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type expectedSpendEvidence struct {
	status    *rgb11wallet.BitcoinTxStatus
	statusErr error
	raw       []byte
	rawErr    error
}

func (e expectedSpendEvidence) GetUTXO(string) (*rgb11wallet.BitcoinUTXO, error) {
	return nil, nil
}
func (e expectedSpendEvidence) GetRawTx(string) ([]byte, error) { return e.raw, e.rawErr }
func (e expectedSpendEvidence) GetTxStatus(string) (*rgb11wallet.BitcoinTxStatus, error) {
	return e.status, e.statusErr
}
func (e expectedSpendEvidence) GetOutspend(string) (*rgb11wallet.BitcoinOutspend, error) {
	return &rgb11wallet.BitcoinOutspend{Spent: true}, nil
}
func (e expectedSpendEvidence) GetTip() (*rgb11wallet.BitcoinTip, error) { return nil, nil }
func (e expectedSpendEvidence) Broadcast([]byte) (string, error)         { return "", nil }

type delayedSpendEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	mu                  sync.Mutex
	txid, outpoint      string
	statusCalls         int
	postAcceptCalls     int
	accepted            bool
	firstStatusUnknown  bool
	firstWorkerAttempt  uint64
	firstUnknownEntered chan struct{}
	firstUnknownRelease <-chan struct{}
	firstUnknownOnce    sync.Once
	statusOverride      bool
	fallbackStatus      *rgb11wallet.BitcoinTxStatus
	fallbackStatusErr   error
	rawOverride         bool
	fallbackRaw         []byte
	fallbackRawErr      error
	rawCalls            int
	targetOutspendCalls int
	hiddenOutputCalls   int
	hiddenOutputs       map[string]bool
	onAccepted          func([]byte, string) error
	workerAttempts      func() (bool, uint64)
	pauseEntered        chan struct{}
	pauseRelease        <-chan struct{}
	pauseOnce           sync.Once
	retryConfirmed      bool
	statusTraceMu       sync.Mutex
	statusTrace         []delayedSpendStatusObservation
}

type delayedSpendStatusObservation struct {
	Call          int
	Err           bool
	Nil           bool
	TxIDMatches   bool
	Confirmed     bool
	InMempool     bool
	Confirmations int64
	ExpectedSpend bool
	Resolve       bool
	Candidate     bool
	VerifyStable  bool
}

// statusReadStages classifies frames only for diagnostics; callers never use
// these flags to select evidence, inject faults, or change returned values.
func statusReadStages() (expectedSpend, resolve, candidate, verifyStable bool) {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(4, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		name := frame.Function
		expectedSpend = expectedSpend || strings.Contains(name, "verifyRGB11ExpectedSpend")
		resolve = resolve || strings.Contains(name, "resolveRGB11RecoveryProjection") || strings.Contains(name, "resolveRGB11Local")
		candidate = candidate || strings.Contains(name, "buildRGB11RefreshCandidate")
		verifyStable = verifyStable || strings.Contains(name, "verifyStable")
		if !more {
			break
		}
	}
	return
}

func (e *delayedSpendEvidence) statusObservations() []delayedSpendStatusObservation {
	e.statusTraceMu.Lock()
	defer e.statusTraceMu.Unlock()
	return append([]delayedSpendStatusObservation(nil), e.statusTrace...)
}

func (e *delayedSpendEvidence) recordStatusObservation(call int, status *rgb11wallet.BitcoinTxStatus, err error) {
	expectedSpend, resolve, candidate, verifyStable := statusReadStages()
	observation := delayedSpendStatusObservation{
		Call: call, Err: err != nil, Nil: status == nil,
		ExpectedSpend: expectedSpend, Resolve: resolve, Candidate: candidate, VerifyStable: verifyStable,
	}
	if status != nil {
		observation.TxIDMatches = status.TxID == e.txid
		observation.Confirmed = status.Confirmed
		observation.InMempool = status.InMempool
		observation.Confirmations = status.Confirmations
	}
	e.statusTraceMu.Lock()
	e.statusTrace = append(e.statusTrace, observation)
	e.statusTraceMu.Unlock()
}

func (e *delayedSpendEvidence) GetUTXO(outpoint string) (*rgb11wallet.BitcoinUTXO, error) {
	e.mu.Lock()
	if e.accepted && e.hiddenOutputs[outpoint] {
		e.hiddenOutputCalls++
		delete(e.hiddenOutputs, outpoint)
		e.mu.Unlock()
		return nil, nil
	}
	e.mu.Unlock()
	return e.BitcoinEvidenceProvider.GetUTXO(outpoint)
}

func (e *delayedSpendEvidence) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if txid != e.txid {
		return e.BitcoinEvidenceProvider.GetTxStatus(txid)
	}
	e.mu.Lock()
	e.statusCalls++
	call := e.statusCalls
	accepted := e.accepted
	if !accepted {
		e.mu.Unlock()
		status, err := e.BitcoinEvidenceProvider.GetTxStatus(txid)
		e.recordStatusObservation(call, status, err)
		return status, err
	}
	e.postAcceptCalls++
	postAcceptCall := e.postAcceptCalls
	workerAttempts := e.workerAttempts
	statusOverride := e.statusOverride
	fallbackStatus := e.fallbackStatus
	if fallbackStatus != nil {
		copy := *fallbackStatus
		fallbackStatus = &copy
	}
	fallbackStatusErr := e.fallbackStatusErr
	e.mu.Unlock()
	running, attempt := false, uint64(0)
	if workerAttempts != nil {
		running, attempt = workerAttempts()
	}
	var status *rgb11wallet.BitcoinTxStatus
	var err error
	switch {
	case postAcceptCall == 1:
		e.mu.Lock()
		e.firstStatusUnknown = true
		if running {
			e.firstWorkerAttempt = attempt
		}
		e.mu.Unlock()
		if running && e.firstUnknownEntered != nil {
			e.firstUnknownOnce.Do(func() { close(e.firstUnknownEntered) })
			<-e.firstUnknownRelease
		}
		status = &rgb11wallet.BitcoinTxStatus{TxID: txid}
	case postAcceptCall == 2 && statusOverride:
		err = fallbackStatusErr
		if err == nil && fallbackStatus != nil {
			status = fallbackStatus
		}
	default:
		status, err = e.BitcoinEvidenceProvider.GetTxStatus(txid)
	}
	if running && postAcceptCall > 1 && e.pauseEntered != nil {
		e.mu.Lock()
		firstAttempt := e.firstWorkerAttempt
		e.mu.Unlock()
		if firstAttempt > 0 && attempt > firstAttempt && err == nil && status != nil && status.Confirmed {
			e.mu.Lock()
			e.retryConfirmed = true
			e.mu.Unlock()
			e.pauseOnce.Do(func() { close(e.pauseEntered) })
			<-e.pauseRelease
		}
	}
	e.recordStatusObservation(call, status, err)
	return status, err
}

func (e *delayedSpendEvidence) Broadcast(raw []byte) (string, error) {
	txid, err := e.BitcoinEvidenceProvider.Broadcast(raw)
	if err != nil {
		return "", err
	}
	e.mu.Lock()
	onAccepted := e.onAccepted
	e.mu.Unlock()
	if onAccepted != nil {
		if err := onAccepted(raw, txid); err != nil {
			return "", err
		}
	}
	e.mu.Lock()
	e.accepted = true
	e.mu.Unlock()
	return txid, nil
}

func (e *delayedSpendEvidence) GetRawTx(txid string) ([]byte, error) {
	e.mu.Lock()
	if e.accepted && txid == e.txid {
		e.rawCalls++
		if e.rawOverride {
			raw, err := append([]byte(nil), e.fallbackRaw...), e.fallbackRawErr
			e.mu.Unlock()
			return raw, err
		}
	}
	e.mu.Unlock()
	return e.BitcoinEvidenceProvider.GetRawTx(txid)
}

func (e *delayedSpendEvidence) GetOutspend(outpoint string) (*rgb11wallet.BitcoinOutspend, error) {
	e.mu.Lock()
	if e.accepted && outpoint == e.outpoint {
		e.targetOutspendCalls++
		e.mu.Unlock()
		return &rgb11wallet.BitcoinOutspend{Spent: true, SpendingTx: "unknown"}, nil
	}
	e.mu.Unlock()
	return e.BitcoinEvidenceProvider.GetOutspend(outpoint)
}

type expectedRawState struct {
	snapshot   *RGB11WalletSnapshot
	pending    *rgb11wallet.PendingTransfer
	transfer   *rgb11wallet.TransferState
	proofs     []*rgb11wallet.AllocationProof
	locks      map[string]*LockedUtxo
	broadcasts int
}

func expectedRawSendTransferView(t *testing.T, store *rgb11wallet.ProjectionStore,
	transferID string) *rgb11wallet.TransferState {
	t.Helper()
	transfers, err := store.ListTransfers()
	if err != nil {
		t.Fatalf("list sender transfer views: %v", err)
		return nil
	}
	var found *rgb11wallet.TransferState
	for _, state := range transfers {
		if state == nil || state.TransferID != transferID || state.Direction != "send" {
			continue
		}
		if found != nil {
			t.Fatalf("duplicate sender transfer view for %s", transferID)
			return nil
		}
		copy := *state
		found = &copy
	}
	if found == nil {
		t.Fatalf("sender transfer view missing from pending/history list: %s", transferID)
		return nil
	}
	return found
}

func captureExpectedRawState(t *testing.T, f rgb11ExpectedRawSpendFallbackFixture) expectedRawState {
	t.Helper()
	store := f.sender.rgbManager.projectionStore
	pending, err := store.LoadPendingTransfer(f.pending.State.TransferID)
	if err != nil || pending == nil {
		t.Fatalf("load durable pending send: pending=%+v err=%v", pending, err)
	}
	walletID, err := f.sender.rgbManager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := f.sender.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	transfer := expectedRawSendTransferView(t, store, f.pending.State.TransferID)
	proofs, err := store.ListProofs()
	if err != nil {
		t.Fatal(err)
	}
	sourceProof, sourceInput := false, false
	for _, proof := range proofs {
		if proof != nil && proof.OutPoint == f.source && proof.AssetName == f.imported.AssetName {
			sourceProof = true
		}
	}
	for _, input := range pending.State.InputOutPoints {
		if input == f.source {
			sourceInput = true
		}
	}
	if !sourceProof || !sourceInput {
		t.Fatalf("source proof/input mismatch: proof=%t input=%t source=%s", sourceProof, sourceInput, f.source)
	}
	if pending.RecipientObjectHash == "" || pending.LocalObjectHash == "" {
		t.Fatal("accepted transfer is missing immutable consignment identity")
	}
	locks := f.sender.utxoLockerL1.GetLockedUtxoList()
	for _, input := range pending.State.InputOutPoints {
		lock := locks[input]
		if lock == nil || lock.Reason != rgb11wallet.LockReasonPending || lock.ReservationID != pending.ReservationID {
			t.Fatalf("accepted input reservation missing: input=%s lock=%+v", input, lock)
		}
	}
	f.evidence.mu.Lock()
	broadcasts := f.evidence.broadcastCount
	f.evidence.mu.Unlock()
	return expectedRawState{snapshot: snapshot, pending: pending, transfer: transfer,
		proofs: proofs, locks: locks, broadcasts: broadcasts}
}

func assertExpectedRawState(t *testing.T, f rgb11ExpectedRawSpendFallbackFixture,
	before expectedRawState, wantStatus string) {
	t.Helper()
	store := f.sender.rgbManager.projectionStore
	wantPending := *before.pending
	wantPending.State.Status = wantStatus
	gotPending, err := store.LoadPendingTransfer(f.pending.State.TransferID)
	if err != nil || !reflect.DeepEqual(gotPending, &wantPending) {
		t.Fatalf("pending intent or ACK changed: got=%+v err=%v", gotPending, err)
	}
	wantTransfer := *before.transfer
	wantTransfer.Status = wantStatus
	gotTransfer := expectedRawSendTransferView(t, store, f.pending.State.TransferID)
	if !reflect.DeepEqual(gotTransfer, &wantTransfer) {
		t.Fatalf("sender transfer view changed outside status: got=%+v", gotTransfer)
	}
	walletID, err := f.sender.rgbManager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	after, _, err := f.sender.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	stable := func(snapshot *RGB11WalletSnapshot) []rgb11wallet.SnapshotRecord {
		result := make([]rgb11wallet.SnapshotRecord, 0, len(snapshot.ProjectionRecords))
		for _, record := range snapshot.ProjectionRecords {
			if record.Key == "pending-"+f.pending.State.TransferID ||
				record.Key == "proof-"+f.source+"-"+f.imported.AssetName.String() {
				continue
			}
			result = append(result, record)
		}
		return result
	}
	if !sameRGB11SnapshotRecords(stable(before.snapshot), stable(after)) ||
		!sameRGB11SnapshotRecords(before.snapshot.EngineRecords, after.EngineRecords) {
		t.Fatal("unrelated projection or engine records changed")
	}
	wantProofs := make([]*rgb11wallet.AllocationProof, len(before.proofs))
	wantSourceSpending := make([]*rgb11wallet.AllocationProof, len(before.proofs))
	sourceProofCount := 0
	for i, proof := range before.proofs {
		copy := *proof
		spendingCopy := copy
		if proof.OutPoint == f.source && proof.AssetName == f.imported.AssetName {
			sourceProofCount++
			spendingCopy.Status = "spending"
		}
		wantProofs[i] = &copy
		wantSourceSpending[i] = &spendingCopy
	}
	if sourceProofCount != 1 {
		t.Fatalf("expected exactly one source proof, got %d", sourceProofCount)
	}
	if wantStatus == "settled" {
		wantProofs = wantSourceSpending
	}
	proofs, err := store.ListProofs()
	proofsMatch := err == nil && reflect.DeepEqual(proofs, wantProofs)
	if err == nil && wantStatus == "broadcast" {
		proofsMatch = proofsMatch || reflect.DeepEqual(proofs, wantSourceSpending)
	}
	if err != nil || !proofsMatch {
		changes := []string{"proof records unavailable"}
		if err == nil {
			changes = expectedRawProofDiffs(before.proofs, proofs, f.source, f.imported.AssetName)
		}
		t.Fatalf("proof records changed outside the expected source transition: err=%v fields=%v", err, changes)
	}
	wantLocks := before.locks
	if wantStatus == "settled" {
		wantLocks = make(map[string]*LockedUtxo, len(before.locks))
		inputs := make(map[string]bool, len(before.pending.State.InputOutPoints))
		for _, input := range before.pending.State.InputOutPoints {
			inputs[input] = true
		}
		for point, lock := range before.locks {
			if !inputs[point] {
				wantLocks[point] = lock
			}
		}
	}
	if locks := f.sender.utxoLockerL1.GetLockedUtxoList(); !reflect.DeepEqual(locks, wantLocks) {
		t.Fatal("settlement changed a reservation outside the transfer inputs")
	}
	f.evidence.mu.Lock()
	broadcasts := f.evidence.broadcastCount
	f.evidence.mu.Unlock()
	if broadcasts != before.broadcasts || f.sender.GetRGB11ConsistencyStatus() != "ok" {
		t.Fatalf("broadcasts=%d want=%d consistency=%s", broadcasts, before.broadcasts, f.sender.GetRGB11ConsistencyStatus())
	}
}

func expectedRawProofDiffs(before, after []*rgb11wallet.AllocationProof,
	source string, asset indexer.AssetName) []string {
	identity := func(proof *rgb11wallet.AllocationProof) string {
		if proof == nil {
			return ""
		}
		return proof.OutPoint + "\x00" + proof.AssetName.String()
	}
	afterByIdentity := make(map[string]*rgb11wallet.AllocationProof, len(after))
	for _, proof := range after {
		afterByIdentity[identity(proof)] = proof
	}
	changes := make([]string, 0)
	if len(before) != len(after) {
		changes = append(changes, fmt.Sprintf("proof_count:%d->%d", len(before), len(after)))
	}
	for index, original := range before {
		label := fmt.Sprintf("proof[%d]", index)
		if original != nil && original.OutPoint == source && original.AssetName == asset {
			label = "source-proof"
		}
		current := afterByIdentity[identity(original)]
		if original == nil || current == nil {
			changes = append(changes, label+":missing-or-nil")
			continue
		}
		left, right := reflect.ValueOf(*original), reflect.ValueOf(*current)
		typeOfProof := left.Type()
		fields := make([]string, 0)
		for fieldIndex := 0; fieldIndex < left.NumField(); fieldIndex++ {
			if reflect.DeepEqual(left.Field(fieldIndex).Interface(), right.Field(fieldIndex).Interface()) {
				continue
			}
			field := typeOfProof.Field(fieldIndex).Name
			switch field {
			case "Status":
				fields = append(fields, fmt.Sprintf("Status:%s->%s", original.Status, current.Status))
			case "Confirmations":
				fields = append(fields, fmt.Sprintf("Confirmations:%d->%d", original.Confirmations, current.Confirmations))
			default:
				fields = append(fields, field)
			}
		}
		if len(fields) != 0 {
			changes = append(changes, label+":"+strings.Join(fields, ","))
		}
		delete(afterByIdentity, identity(original))
	}
	if len(afterByIdentity) != 0 {
		changes = append(changes, fmt.Sprintf("unmatched-after:%d", len(afterByIdentity)))
	}
	return changes
}

func TestRGB11ExpectedRawSpend(t *testing.T) {
	f := newRGB11ExpectedRawSpendFallbackFixture(t)
	before := captureExpectedRawState(t, f)
	if before.pending.State.Status != "broadcast" || before.pending.State.AckStatus != "accepted" {
		t.Fatalf("fixture did not use the public accepted-send path: %+v", before.pending.State)
	}
	first, err := f.sender.RefreshRGB11State(context.Background())
	fault := f.fault
	fault.mu.Lock()
	unknown, calls := fault.firstStatusUnknown, fault.postAcceptCalls
	fault.mu.Unlock()
	t.Logf("target status reads: %+v", fault.statusObservations())
	if err != nil || first == nil || first.Settled != 0 || first.Conflicted != 0 || len(first.Inconsistent) != 0 {
		t.Fatalf("first unknown observation changed the transfer: result=%+v err=%v", first, err)
	}
	if !unknown || calls < 2 {
		t.Fatalf("unknown-status fallback was not exercised: unknown=%t post_accept_reads=%d", unknown, calls)
	}
	assertExpectedRawState(t, f, before, "broadcast")

	second, err := f.sender.RefreshRGB11State(context.Background())
	if err != nil || second == nil || second.Settled != 1 || second.Conflicted != 0 ||
		second.Unresolved != 0 || len(second.Inconsistent) != 0 {
		t.Fatalf("independent healthy refresh did not settle exactly once: result=%+v err=%v", second, err)
	}
	assertExpectedRawState(t, f, before, "settled")
	balance, err := f.sender.GetRGB11AssetBalance(&f.imported.AssetName)
	if err != nil || balance != nil && balance.Sign() != 0 {
		t.Fatalf("fully sent source asset still has spendable balance: balance=%v err=%v", balance, err)
	}
	third, err := f.sender.RefreshRGB11State(context.Background())
	if err != nil || third == nil || third.Settled != 0 || third.Conflicted != 0 || len(third.Inconsistent) != 0 {
		t.Fatalf("settled refresh was not idempotent: result=%+v err=%v", third, err)
	}
	assertExpectedRawState(t, f, before, "settled")
}

func expectedRawWorkerStats(manager *rgb11Manager) (bool, uint64) {
	if manager == nil || manager.scopeStates == nil {
		return false, 0
	}
	registry := manager.scopeStates
	registry.mu.RLock()
	item := registry.reconciliations[manager.rgb11ScopeKey()]
	if item == nil {
		registry.mu.RUnlock()
		return false, 0
	}
	running, attempts := item.running, item.attempts
	registry.mu.RUnlock()
	return running, attempts
}

func TestRGB11RawSpendBGRetry(t *testing.T) {
	f := newRawSpendBGFixture(t)
	select {
	case <-f.firstUnknownEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("existing worker did not reach its first unknown-status barrier")
	}
	f.fault.mu.Lock()
	firstAttempt, unknown := f.fault.firstWorkerAttempt, f.fault.firstStatusUnknown
	f.fault.mu.Unlock()
	running, attempts := expectedRawWorkerStats(f.sender.rgbManager)
	if !unknown || firstAttempt == 0 || !running || attempts != firstAttempt {
		t.Fatalf("first worker barrier: unknown=%t first=%d running=%t attempts=%d",
			unknown, firstAttempt, running, attempts)
	}
	// This is the pre-failure baseline: the public broadcast and accepted
	// state are already durable, while the worker has not returned its first
	// unknown status to candidate construction.
	before := captureExpectedRawState(t, f)
	if before.pending.State.Status != "broadcast" || before.pending.State.AckStatus != "accepted" {
		t.Fatalf("pre-unknown state is not the accepted public send: %+v", before.pending.State)
	}
	f.releaseFirstUnknown()
	select {
	case <-f.pauseEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("existing worker did not reach its confirmed retry barrier")
	}
	f.fault.mu.Lock()
	firstAttempt, unknown, confirmed := f.fault.firstWorkerAttempt, f.fault.firstStatusUnknown, f.fault.retryConfirmed
	f.fault.mu.Unlock()
	running, attempts = expectedRawWorkerStats(f.sender.rgbManager)
	if !unknown || !confirmed || firstAttempt == 0 || !running || attempts != firstAttempt+1 {
		t.Fatalf("worker retry barrier: unknown=%t confirmed=%t first=%d running=%t attempts=%d",
			unknown, confirmed, firstAttempt, running, attempts)
	}
	assertExpectedRawState(t, f, before, "broadcast")
	f.releaseGate()
	deadline := time.Now().Add(5 * time.Second)
	for {
		running, attempts = expectedRawWorkerStats(f.sender.rgbManager)
		pending, err := f.sender.rgbManager.projectionStore.LoadPendingTransfer(f.pending.State.TransferID)
		if err != nil {
			t.Fatal(err)
		}
		if !running && pending.State.Status == "settled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("existing worker did not commit its healthy retry: running=%t attempts=%d status=%s",
				running, attempts, pending.State.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if attempts != firstAttempt+1 {
		t.Fatalf("settlement required unexpected worker passes: got=%d want=%d", attempts, firstAttempt+1)
	}
	assertExpectedRawState(t, f, before, "settled")
	balance, err := f.sender.GetRGB11AssetBalance(&f.imported.AssetName)
	if err != nil || balance != nil && balance.Sign() != 0 {
		t.Fatalf("fully sent source asset still has spendable balance: balance=%v err=%v", balance, err)
	}
}

type rgb11ExpectedRawSpendFallbackFixture struct {
	sender              *Manager
	imported            *RGB11ImportResult
	pending             *rgb11wallet.PendingTransfer
	evidence            *rgb11FlowEvidence
	fault               *delayedSpendEvidence
	source              string
	pauseEntered        chan struct{}
	firstUnknownEntered chan struct{}
	releaseFirstUnknown func()
	releaseGate         func()
}

func newRGB11ExpectedRawSpendFallbackFixture(t *testing.T) rgb11ExpectedRawSpendFallbackFixture {
	return newExpectedRawSpendFixture(t, false)
}

func newRawSpendBGFixture(t *testing.T) rgb11ExpectedRawSpendFallbackFixture {
	return newExpectedRawSpendFixture(t, true)
}

func recordAcceptedRawSpend(chain *rgb11AddressEvidence, rpc *rgb11FlowIndexer,
	pending *rgb11wallet.PendingTransfer, raw []byte, txid string, ownedScript []byte) error {
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil || tx.TxHash().String() != txid ||
		txid != pending.State.WitnessTxID {
		return fmt.Errorf("accepted broadcast did not match the prepared witness")
	}
	base := chain.rgb11FlowEvidence
	base.mu.Lock()
	base.rawTx[txid] = append([]byte(nil), raw...)
	spent := make(map[string]bool, len(tx.TxIn))
	for _, input := range tx.TxIn {
		point := input.PreviousOutPoint.String()
		spent[point] = true
		base.spendingTx[point] = txid
		delete(base.utxos, point)
		delete(rpc.outputs, point)
	}
	for vout, output := range tx.TxOut {
		point := fmt.Sprintf("%s:%d", txid, vout)
		base.utxos[point] = &rgb11wallet.BitcoinUTXO{
			OutPoint: point, Value: output.Value,
			PkScript: append([]byte(nil), output.PkScript...), Confirmations: 6,
		}
		if bytes.Equal(output.PkScript, ownedScript) {
			view := indexer.NewTxOutput(output.Value)
			view.OutPointStr = point
			view.OutValue.PkScript = append([]byte(nil), output.PkScript...)
			rpc.outputs[point] = view
		}
	}
	base.mu.Unlock()
	plain := rpc.plain[:0]
	for _, output := range rpc.plain {
		if output != nil && !spent[output.OutPoint] {
			plain = append(plain, output)
		}
	}
	for vout, output := range tx.TxOut {
		if !bytes.Equal(output.PkScript, ownedScript) {
			continue
		}
		plain = append(plain, &indexerwire.TxOutputInfo{
			OutPoint: fmt.Sprintf("%s:%d", txid, vout), Value: output.Value,
			PkScript: append([]byte(nil), output.PkScript...),
		})
	}
	rpc.plain = plain
	chain.setStatus(txid, rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 6})
	return nil
}

func newExpectedRawSpendFixture(t *testing.T, background bool) rgb11ExpectedRawSpendFallbackFixture {
	t.Helper()
	proxy := &rgb11ProxyTestServer{}
	server := httptest.NewServer(http.HandlerFunc(proxy.serveHTTP))
	t.Cleanup(server.Close)
	sender, recipient, imported, chain, rpc := newRGB11GenericSendFixture(t)
	if background {
		t.Cleanup(func() {
			sender.rgbManager.scopeStates.stopReconciliations()
			recipient.rgbManager.scopeStates.stopReconciliations()
		})
		recipient.rgbManager.scopeStates.stopReconciliations()
	} else {
		sender.rgbManager.scopeStates.stopReconciliations()
		recipient.rgbManager.scopeStates.stopReconciliations()
	}
	balance, err := sender.GetRGB11AssetBalance(&imported.AssetName)
	if err != nil || balance == nil || balance.Sign() <= 0 {
		t.Fatalf("balance=%v err=%v", balance, err)
	}
	source := rgb11GenericFixtureSource(t, sender, imported)
	ownedScript, err := AddrToPkScript(sender.wallet.GetAddress(), GetChainParam())
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "rpc://" + strings.TrimPrefix(server.URL, "http://")
	request, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: RGB11ProxyTransport,
		TransportEndpoints: []string{endpoint}, ContractID: imported.ContractID,
		AmountRaw: balance.Value.String(), WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
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
	if err != nil || pending == nil || len(pending.ChangeSeals) != 0 {
		t.Fatalf("load full-send preparation: pending=%+v err=%v", pending, err)
	}
	fault := &delayedSpendEvidence{
		BitcoinEvidenceProvider: chain, txid: pending.State.WitnessTxID,
		outpoint: source, hiddenOutputs: make(map[string]bool, len(pending.State.OutputOutPoints)),
	}
	for _, output := range pending.State.OutputOutPoints {
		fault.hiddenOutputs[output] = true
	}
	fault.onAccepted = func(raw []byte, txid string) error {
		return recordAcceptedRawSpend(chain, rpc, pending, raw, txid, ownedScript)
	}
	sender.rgbManager.evidence = fault
	fixture := rgb11ExpectedRawSpendFallbackFixture{
		sender: sender, imported: imported, evidence: chain.rgb11FlowEvidence,
		fault: fault, source: source,
	}
	if background {
		registry := sender.rgbManager.scopeStates
		registry.mu.Lock()
		registry.retryDelay = 25 * time.Millisecond
		registry.mu.Unlock()
		fixture.pauseEntered = make(chan struct{})
		release := make(chan struct{})
		var releaseOnce sync.Once
		fixture.releaseGate = func() { releaseOnce.Do(func() { close(release) }) }
		fault.pauseEntered, fault.pauseRelease = fixture.pauseEntered, release
		fixture.firstUnknownEntered = make(chan struct{})
		unknownRelease := make(chan struct{})
		var unknownReleaseOnce sync.Once
		fixture.releaseFirstUnknown = func() { unknownReleaseOnce.Do(func() { close(unknownRelease) }) }
		fault.firstUnknownEntered, fault.firstUnknownRelease = fixture.firstUnknownEntered, unknownRelease
		fault.workerAttempts = func() (bool, uint64) { return expectedRawWorkerStats(sender.rgbManager) }
		t.Cleanup(func() {
			fixture.releaseFirstUnknown()
			fixture.releaseGate()
		})
	}
	firstDelivery, err := sender.DeliverAndBroadcastRGB11ProxyTransfer(context.Background(), []string{prepared.State.TransferID})
	if err != nil || firstDelivery == nil || !firstDelivery.AwaitingACK || firstDelivery.Broadcast {
		t.Fatalf("public proxy delivery did not await recipient ACK: result=%+v err=%v", firstDelivery, err)
	}
	received, err := recipient.ReceiveRGB11ProxyConsignment(context.Background(), request.RequestID)
	if err != nil || received == nil || !received.AckPosted || !received.AwaitingBroadcast ||
		received.TxID != pending.State.WitnessTxID {
		t.Fatalf("recipient did not validate and post a real ACK: result=%+v err=%v", received, err)
	}
	proxy.mu.Lock()
	ack := proxy.ack
	proxy.mu.Unlock()
	if ack == nil || !*ack {
		t.Fatal("proxy did not persist the positive recipient ACK")
	}
	delivery, err := sender.DeliverAndBroadcastRGB11ProxyTransfer(context.Background(), []string{prepared.State.TransferID})
	if err != nil || delivery == nil || !delivery.Broadcast || delivery.TxID != pending.State.WitnessTxID {
		t.Fatalf("public sender did not broadcast after ACK: result=%+v err=%v", delivery, err)
	}
	pending, err = sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil || pending == nil || pending.State.Status != "broadcast" || pending.State.AckStatus != "accepted" {
		t.Fatalf("public send/ACK flow did not persist the accepted state: pending=%+v err=%v", pending, err)
	}
	fixture.pending = pending
	return fixture
}

func TestRGB11ExpectedRawSpendFallbackRejectsUnverified(t *testing.T) {
	tests := []struct {
		name              string
		fallbackStatus    *rgb11wallet.BitcoinTxStatus
		fallbackStatusErr error
		fallbackRaw       []byte
		fallbackRawErr    error
		rawOverride       bool
		wantRawCalls      int
	}{
		{
			name: "mempool_only",
			fallbackStatus: &rgb11wallet.BitcoinTxStatus{
				InMempool: true,
			},
		},
		{
			name:              "status_read_error",
			fallbackStatusErr: errors.New("injected status read failure"),
		},
		{
			name: "raw_read_error",
			fallbackStatus: &rgb11wallet.BitcoinTxStatus{
				Confirmed: true, Confirmations: 6,
			},
			rawOverride:    true,
			fallbackRawErr: errors.New("injected raw transaction read failure"),
			wantRawCalls:   1,
		},
		{
			name: "raw_hash_mismatch",
			fallbackStatus: &rgb11wallet.BitcoinTxStatus{
				Confirmed: true, Confirmations: 6,
			},
			rawOverride:  true,
			wantRawCalls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRGB11ExpectedRawSpendFallbackFixture(t)
			fault := fixture.fault
			fault.statusOverride = true
			fault.fallbackStatusErr = test.fallbackStatusErr
			if test.fallbackStatus != nil {
				status := *test.fallbackStatus
				status.TxID = fault.txid
				fault.fallbackStatus = &status
			}
			fault.rawOverride = test.rawOverride
			fault.fallbackRaw = append([]byte(nil), test.fallbackRaw...)
			fault.fallbackRawErr = test.fallbackRawErr
			if test.name == "raw_hash_mismatch" {
				tx := wire.NewMsgTx(wire.TxVersion)
				if err := tx.Deserialize(bytes.NewReader(fixture.pending.SignedTx)); err != nil {
					t.Fatal(err)
				}
				tx.LockTime++
				var raw bytes.Buffer
				if err := tx.Serialize(&raw); err != nil {
					t.Fatal(err)
				}
				fault.fallbackRaw = raw.Bytes()
				if tx.TxHash().String() == fault.txid {
					t.Fatal("hash mismatch fixture did not change the transaction id")
				}
			}

			if len(fixture.pending.State.InputOutPoints) == 0 {
				t.Fatal("prepared transfer has no pending inputs")
			}
			locksBefore := fixture.sender.utxoLockerL1.GetLockedUtxoList()
			reservationIDs := make(map[string]string, len(fixture.pending.State.InputOutPoints))
			for _, input := range fixture.pending.State.InputOutPoints {
				lock := locksBefore[input]
				if lock == nil || lock.Reason != rgb11wallet.LockReasonPending {
					t.Fatalf("prepared input lock=%+v for %s", lock, input)
				}
				reservationIDs[input] = lock.ReservationID
			}
			if fixture.source == "" {
				t.Fatal("fixture has no original RGB proof")
			}
			proofIsPendingInput := false
			for _, input := range fixture.pending.State.InputOutPoints {
				if input == fixture.source {
					proofIsPendingInput = true
					break
				}
			}
			if !proofIsPendingInput {
				t.Fatalf("original proof input %s is not protected by the pending transfer", fixture.source)
			}
			fixture.evidence.mu.Lock()
			broadcastsBefore := fixture.evidence.broadcastCount
			fixture.evidence.mu.Unlock()

			// Exercise the production candidate builder with real Prepare material.
			// The full public success path is covered by TestRGB11ExpectedRawSpend;
			// keeping this probe at the builder isolates the fallback from later
			// whole-history resolver reads of the deliberately faulty provider.
			walletID, err := fixture.sender.rgbManager.RGB11WalletID()
			if err != nil {
				t.Fatal(err)
			}
			current, _, err := fixture.sender.rgbManager.exportRGB11WalletSnapshot(walletID)
			if err != nil {
				t.Fatal(err)
			}
			candidate, result, refreshErr := fixture.sender.rgbManager.buildRGB11RefreshCandidate(context.Background(), current, time.Now().Unix())
			if refreshErr != nil || candidate == nil {
				t.Fatalf("build unresolved expected spend: result=%+v err=%v", result, refreshErr)
			}
			candidatePendings, err := rgb11wallet.PendingTransfersFromProjectionSnapshot(candidate.ProjectionRecords)
			if err != nil {
				t.Fatal(err)
			}
			foundCandidatePending := false
			for _, candidatePending := range candidatePendings {
				if candidatePending.State.TransferID != fixture.pending.State.TransferID {
					continue
				}
				foundCandidatePending = true
				if candidatePending.State.Status != "broadcast" || candidatePending.State.AckStatus != "accepted" {
					t.Fatalf("unverified fallback changed candidate journal: %+v", candidatePending.State)
				}
			}
			if !foundCandidatePending {
				t.Fatal("unverified fallback dropped candidate journal")
			}
			if result.Settled != 0 || result.Conflicted != 0 || result.Unresolved != 1 {
				t.Fatalf("unverified expected spend changed pending state: result=%+v", result)
			}
			if !fault.firstStatusUnknown || fault.statusCalls < 2 ||
				fault.hiddenOutputCalls != len(fixture.pending.State.OutputOutPoints) || fault.targetOutspendCalls == 0 {
				t.Fatalf("fallback path not exercised: firstUnknown=%t statusCalls=%d hiddenOutputs=%d/%d outspendCalls=%d",
					fault.firstStatusUnknown, fault.statusCalls, fault.hiddenOutputCalls,
					len(fixture.pending.State.OutputOutPoints), fault.targetOutspendCalls)
			}
			if fault.rawCalls != test.wantRawCalls {
				t.Fatalf("raw verification calls=%d want %d", fault.rawCalls, test.wantRawCalls)
			}
			stored, err := fixture.sender.rgbManager.projectionStore.LoadPendingTransfer(fixture.pending.State.TransferID)
			if err != nil || stored == nil {
				t.Fatalf("load unchanged pending: %v", err)
			}
			if stored.State.Status != "broadcast" || stored.State.AckStatus != "accepted" {
				t.Fatalf("unverified spend changed pending transfer: state=%+v err=%v", stored.State, err)
			}
			locksAfter := fixture.sender.utxoLockerL1.GetLockedUtxoList()
			for _, input := range fixture.pending.State.InputOutPoints {
				lock := locksAfter[input]
				if lock == nil || lock.Reason != rgb11wallet.LockReasonPending || lock.ReservationID != reservationIDs[input] {
					t.Fatalf("unverified spend released or replaced pending input lock: input=%s lock=%+v", input, lock)
				}
			}
			if reserveErr := fixture.sender.utxoLockerL1.TryReserve(
				[]string{fixture.source}, rgb11wallet.LockReasonPending, "unverified-spend-competitor"); !errors.Is(reserveErr, ErrUtxoReserved) {
				t.Fatalf("original RGB proof became available after unverified spend: reserve error=%v", reserveErr)
			}
			proofs, err := fixture.sender.rgbManager.projectionStore.ListProofs()
			if err != nil {
				t.Fatal(err)
			}
			for _, proof := range proofs {
				if proof.OutPoint == fixture.source && locksAfter[proof.OutPoint] == nil {
					t.Fatalf("original RGB proof was restored without its pending lock: %s", proof.OutPoint)
				}
			}
			fixture.evidence.mu.Lock()
			broadcastsAfter := fixture.evidence.broadcastCount
			fixture.evidence.mu.Unlock()
			if broadcastsAfter != broadcastsBefore {
				t.Fatalf("refresh broadcast count changed: before=%d after=%d", broadcastsBefore, broadcastsAfter)
			}
		})
	}
}

func TestRGB11ExpectedSpendChecks(t *testing.T) {
	inputHash := chainhash.Hash{1}
	outpoint := wire.OutPoint{Hash: inputHash, Index: 1}
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(wire.NewTxIn(&outpoint, nil, nil))
	tx.AddTxOut(wire.NewTxOut(1, []byte{0x51}))
	var encoded bytes.Buffer
	if err := tx.Serialize(&encoded); err != nil {
		t.Fatal(err)
	}
	txid := tx.TxHash().String()
	confirmed := &rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 6}
	valid := expectedSpendEvidence{status: confirmed, raw: encoded.Bytes()}
	if status, ok := verifyRGB11ExpectedSpend(valid, outpoint.String(), txid); !ok || status.TxID != txid {
		t.Fatal("confirmed exact raw spend was rejected")
	}
	tests := []struct {
		name     string
		evidence expectedSpendEvidence
		point    string
	}{
		{name: "unconfirmed", evidence: expectedSpendEvidence{
			status: &rgb11wallet.BitcoinTxStatus{TxID: txid, InMempool: true}, raw: encoded.Bytes()}},
		{name: "status_error", evidence: expectedSpendEvidence{statusErr: errors.New("offline"), raw: encoded.Bytes()}},
		{name: "raw_error", evidence: expectedSpendEvidence{status: confirmed, rawErr: errors.New("offline")}},
		{name: "raw_mismatch", evidence: expectedSpendEvidence{status: confirmed, raw: []byte{0x01}}},
		{name: "wrong_input", evidence: valid, point: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			point := test.point
			if point == "" {
				point = outpoint.String()
			}
			if _, ok := verifyRGB11ExpectedSpend(test.evidence, point, txid); ok {
				t.Fatal("unverified expected spend was accepted")
			}
		})
	}
}
