package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func (f *rgb11ConflictFixture) exposeSignedWitness(t *testing.T, raw []byte, confirmations int64) string {
	t.Helper()
	var tx wire.MsgTx
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	id := tx.TxHash().String()
	f.evidence.mu.Lock()
	f.evidence.rawTx[id] = append([]byte(nil), raw...)
	for _, in := range tx.TxIn {
		point := in.PreviousOutPoint.String()
		f.evidence.spendingTx[point] = id
		delete(f.evidence.utxos, point)
		delete(f.rpc.outputs, point)
	}
	for vout, out := range tx.TxOut {
		point := fmt.Sprintf("%s:%d", id, vout)
		f.evidence.utxos[point] = &rgb11wallet.BitcoinUTXO{OutPoint: point, Value: out.Value, PkScript: out.PkScript, Confirmations: confirmations}
		view := indexer.NewTxOutput(out.Value)
		view.OutPointStr = point
		view.OutValue.PkScript = out.PkScript
		f.rpc.outputs[point] = view
	}
	f.evidence.mu.Unlock()
	f.evidence.setStatus(id, rgb11wallet.BitcoinTxStatus{TxID: id, Confirmed: confirmations > 0, InMempool: confirmations == 0, Confirmations: confirmations})
	return id
}
func (f *rgb11ConflictFixture) hideSignedWitness(t *testing.T) {
	t.Helper()
	f.evidence.setStatus(f.pending.State.WitnessTxID, rgb11wallet.BitcoinTxStatus{TxID: f.pending.State.WitnessTxID})
	f.evidence.mu.Lock()
	for point := range f.evidence.utxos {
		if strings.HasPrefix(point, f.pending.State.WitnessTxID+":") {
			delete(f.evidence.utxos, point)
			delete(f.rpc.outputs, point)
		}
	}
	f.evidence.mu.Unlock()
}
func TestRGB11ConflictCandidatePreparedChange(t *testing.T) {
	f := newRGB11ConflictFixture(t, false)
	f.exposeSignedWitness(t, f.pending.SignedTx, 0)
	if _, err := f.sender.RefreshRGB11State(context.Background()); err != nil {
		t.Fatalf("stage genuine mempool change: %v", err)
	}
	proofs, err := f.sender.rgbManager.projectionStore.ListProofs()
	if err != nil {
		t.Fatal(err)
	}
	staged := false
	for _, p := range proofs {
		if strings.HasPrefix(p.OutPoint, f.pending.State.WitnessTxID+":") {
			staged = true
		}
	}
	if !staged {
		t.Fatal("no real staged change proof")
	}
	f.hideSignedWitness(t)
	f.publish(t, true)
	result, err := f.sender.RefreshRGB11State(context.Background())
	if !errors.Is(err, ErrRGB11Inconsistent) || result == nil {
		t.Fatalf("prepared branch conflict: %v", err)
	}
	proofs, err = f.sender.rgbManager.projectionStore.ListProofs()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range proofs {
		if strings.HasPrefix(p.OutPoint, f.pending.State.WitnessTxID+":") && p.Status != "orphaned" {
			t.Fatal("absent child became available")
		}
	}
	f.assertImmutable(t)
}
func TestRGB11ConflictCandidateOriginalWitnessReturns(t *testing.T) {
	f := newRGB11ConflictFixture(t, false)
	f.publish(t, true)
	if _, err := f.sender.RefreshRGB11State(context.Background()); !errors.Is(err, ErrRGB11Inconsistent) {
		t.Fatal("conflict setup")
	}
	f.restoreInputs(t)
	f.exposeSignedWitness(t, f.pending.SignedTx, 6)
	if _, err := f.sender.RefreshRGB11State(context.Background()); err != nil {
		t.Fatalf("original witness after rival reorg: %v", err)
	}
	if p := f.journal(t); p.State.Status != "settled" || p.State.AckStatus == "accepted" {
		t.Fatal("original witness not rederived or ACK fabricated")
	}
	if f.sender.GetRGB11ConsistencyStatus() != "ok" {
		t.Fatal("valid original witness retained broken")
	}
}
func TestRGB11ConflictCandidateFlushAtomic(t *testing.T) {
	f := newRGB11ConflictFixture(t, false)
	f.publish(t, true)
	ctx := context.Background()
	current := f.snapshot(t)
	candidate, _, err := f.sender.rgbManager.buildRGB11RefreshCandidate(ctx, current, 0)
	if err != nil {
		t.Fatal(err)
	}
	fail := &rgb11ProjectionFailOnceDB{KVDB: f.sender.db}
	f.sender.db = fail
	defer func() { f.sender.db = fail.KVDB }()
	_, err = f.sender.rgbManager.applyRGB11CandidateSnapshot(ctx, current, candidate)
	if !errors.Is(err, errRGB11ProjectionInjectedFlushFailure) || fail.flushCount() != 1 {
		t.Fatalf("atomic failure not exercised: %v", err)
	}
	if !reflect.DeepEqual(current.ProjectionRecords, f.snapshot(t).ProjectionRecords) {
		t.Fatal("failed commit changed projection")
	}
	f.assertProtected(t)
	touches := fail.failedBatchTouches()
	if touches.projection == 0 || touches.carrierLock == 0 {
		t.Fatal("fault did not cover common projection/lock batch")
	}
}
