package wallet

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type rgb11ConflictFixture struct {
	sender        *Manager
	evidence      *rgb11AddressEvidence
	rpc           *rgb11FlowIndexer
	pending       *rgb11wallet.PendingTransfer
	asset         indexer.AssetName
	carrier       string
	rival         *wire.MsgTx
	originals     map[string]*rgb11wallet.BitcoinUTXO
	before        *RGB11WalletSnapshot
	beforePrepare *RGB11WalletSnapshot
}

func newRGB11ConflictFixture(t *testing.T, feeOnly bool) *rgb11ConflictFixture {
	t.Helper()
	sender, recipient, imported, evidence, rpc := newRGB11GenericSendFixtureWithCarrier(t, 330)
	sender.rgbManager.scopeStates.stopReconciliations()
	recipient.rgbManager.scopeStates.stopReconciliations()
	invoice, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID, AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	walletID, err := sender.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	beforePrepare, _, err := sender.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	pending.State.Status, pending.State.AckStatus = "broadcast", "accepted"
	if err = sender.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}
	f := &rgb11ConflictFixture{sender: sender, evidence: evidence, rpc: rpc, pending: pending, asset: imported.AssetName, carrier: rgb11GenericFixtureSource(t, sender, imported), originals: map[string]*rgb11wallet.BitcoinUTXO{}}
	expected := wire.NewMsgTx(wire.TxVersion)
	if err = expected.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	if len(expected.TxIn) < 2 {
		t.Fatal("fixture must exercise RGB and actual fee inputs")
	}
	f.rival = wire.NewMsgTx(wire.TxVersion)
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	total := int64(0)
	for _, in := range expected.TxIn {
		point := in.PreviousOutPoint.String()
		prev, err := evidence.GetUTXO(point)
		if err != nil || prev == nil {
			t.Fatal("missing authentic original input")
		}
		f.originals[point] = prev
		if feeOnly && point == f.carrier {
			continue
		}
		f.rival.AddTxIn(wire.NewTxIn(&in.PreviousOutPoint, nil, nil))
		fetcher.AddPrevOut(in.PreviousOutPoint, wire.NewTxOut(prev.Value, prev.PkScript))
		total += prev.Value
	}
	if len(f.rival.TxIn) == 0 {
		t.Fatal("no actual rival inputs")
	}
	script, err := AddrToPkScript(sender.wallet.GetAddress(), GetChainParam())
	if err != nil {
		t.Fatal(err)
	}
	f.rival.AddTxOut(wire.NewTxOut(total-1000, script))
	if err = sender.wallet.(*InternalWallet).SignTx(f.rival, fetcher); err != nil {
		t.Fatal(err)
	}
	hashes := txscript.NewTxSigHashes(f.rival, fetcher)
	for i, in := range f.rival.TxIn {
		prev := fetcher.FetchPrevOutput(in.PreviousOutPoint)
		engine, err := txscript.NewEngine(prev.PkScript, f.rival, i, txscript.StandardVerifyFlags, nil, hashes, prev.Value, fetcher)
		if err != nil {
			t.Fatal(err)
		}
		if err = engine.Execute(); err != nil {
			t.Fatal("rival signature invalid")
		}
	}
	evidence.setStatus(pending.State.WitnessTxID, rgb11wallet.BitcoinTxStatus{TxID: pending.State.WitnessTxID})
	f.beforePrepare = beforePrepare
	f.before = f.snapshot(t)
	return f
}
func (f *rgb11ConflictFixture) snapshot(t *testing.T) *RGB11WalletSnapshot {
	t.Helper()
	id, err := f.sender.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := f.sender.rgbManager.exportRGB11WalletSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func (f *rgb11ConflictFixture) publish(t *testing.T, confirmed bool) {
	t.Helper()
	var raw bytes.Buffer
	if err := f.rival.Serialize(&raw); err != nil {
		t.Fatal(err)
	}
	id := f.rival.TxHash().String()
	f.evidence.mu.Lock()
	f.evidence.rawTx[id] = raw.Bytes()
	for _, in := range f.rival.TxIn {
		point := in.PreviousOutPoint.String()
		f.evidence.spendingTx[point] = id
		delete(f.evidence.utxos, point)
		delete(f.rpc.outputs, point)
	}
	f.evidence.mu.Unlock()
	f.evidence.setStatus(id, rgb11wallet.BitcoinTxStatus{TxID: id, Confirmed: confirmed, InMempool: !confirmed, Confirmations: map[bool]int64{true: 6, false: 0}[confirmed]})
}
func (f *rgb11ConflictFixture) restoreInputs(t *testing.T) {
	t.Helper()
	f.evidence.mu.Lock()
	for point, prev := range f.originals {
		f.evidence.utxos[point] = prev
		delete(f.evidence.spendingTx, point)
		view := indexer.NewTxOutput(prev.Value)
		view.OutPointStr = point
		view.OutValue.PkScript = append([]byte(nil), prev.PkScript...)
		f.rpc.outputs[point] = view
	}
	f.evidence.mu.Unlock()
	f.evidence.setStatus(f.rival.TxHash().String(), rgb11wallet.BitcoinTxStatus{TxID: f.rival.TxHash().String()})
}
func (f *rgb11ConflictFixture) journal(t *testing.T) *rgb11wallet.PendingTransfer {
	t.Helper()
	p, err := f.sender.rgbManager.projectionStore.LoadPendingTransfer(f.pending.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func (f *rgb11ConflictFixture) assertProtected(t *testing.T) {
	t.Helper()
	locks := f.sender.utxoLockerL1.GetLockedUtxoList()
	for point := range f.originals {
		lock := locks[point]
		if lock == nil || lock.Reason != rgb11wallet.LockReasonPending || lock.ReservationID != f.pending.ReservationID {
			t.Fatal("input reservation was released")
		}
	}
}
func (f *rgb11ConflictFixture) assertImmutable(t *testing.T) {
	t.Helper()
	before := map[string][]byte{}
	after := map[string][]byte{}
	for _, r := range f.before.ProjectionRecords {
		if len(r.Key) >= 7 && r.Key[:7] == "object-" {
			before[r.Key] = r.Value
		}
	}
	for _, r := range f.snapshot(t).ProjectionRecords {
		if len(r.Key) >= 7 && r.Key[:7] == "object-" {
			after[r.Key] = r.Value
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("immutable recovery objects changed")
	}
}

func TestRGB11ConflictCandidateConfirmed(t *testing.T) {
	f := newRGB11ConflictFixture(t, false)
	f.publish(t, true)
	result, err := f.sender.RefreshRGB11State(context.Background())
	if !errors.Is(err, ErrRGB11Inconsistent) || result == nil || result.Conflicted != 1 || !reflect.DeepEqual(result.Inconsistent, []string{f.carrier}) {
		t.Fatalf("confirmed conflict not published correctly: result=%+v err=%v", result, err)
	}
	if p := f.journal(t); p.State.Status != "conflicted" || p.State.AckStatus != "invalidated" {
		t.Fatal("confirmed conflict journal not committed")
	}
	proof, err := f.sender.rgbManager.projectionStore.LoadProof(f.carrier, f.asset)
	if err != nil || proof.Status != "inconsistent" {
		t.Fatal("spent carrier not quarantined")
	}
	if f.sender.GetRGB11ConsistencyStatus() != "broken" {
		t.Fatal("consistency overwritten")
	}
	f.assertImmutable(t)
	result, err = f.sender.RefreshRGB11State(context.Background())
	if !errors.Is(err, ErrRGB11Inconsistent) || result == nil || result.Conflicted != 0 || len(result.Inconsistent) != 1 {
		t.Fatal("repeat refresh lost quarantine")
	}
}
func TestRGB11ConflictCandidateFeeOnly(t *testing.T) {
	f := newRGB11ConflictFixture(t, true)
	f.publish(t, true)
	result, err := f.sender.RefreshRGB11State(context.Background())
	if err != nil || result == nil || result.Conflicted != 1 || len(result.Inconsistent) != 0 {
		t.Fatalf("fee-only result=%+v err=%v", result, err)
	}
	if f.journal(t).State.Status != "conflicted" {
		t.Fatal("fee-only journal not terminal")
	}
	proof, err := f.sender.rgbManager.projectionStore.LoadProof(f.carrier, f.asset)
	if err != nil || proof.Status == "inconsistent" || proof.Status == "spending" {
		t.Fatal("unspent RGB was classified as spent")
	}
	f.assertImmutable(t)
}
func TestRGB11ConflictCandidateMempool(t *testing.T) {
	f := newRGB11ConflictFixture(t, true)
	f.publish(t, false)
	_, _ = f.sender.RefreshRGB11State(context.Background())
	if f.journal(t).State.Status == "conflicted" {
		t.Fatal("mempool rival permanently invalidated journal")
	}
	f.assertProtected(t)
	f.restoreInputs(t)
	_, err := f.sender.RefreshRGB11State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.assertProtected(t)
	f.assertImmutable(t)
}
func TestRGB11ConflictCandidateRecoveryAndReorg(t *testing.T) {
	f := newRGB11ConflictFixture(t, false)
	f.publish(t, true)
	if _, err := f.sender.RefreshRGB11State(context.Background()); !errors.Is(err, ErrRGB11Inconsistent) {
		t.Fatal("confirmed conflict setup not classified")
	}
	encoded := exportRGB11RecoveryForTest(t, f.sender)
	pkg, err := rgb11wallet.DecodeRecoveryPackage(encoded)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := pkg.WalletSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	proofs, err := rgb11wallet.ProofsFromProjectionSnapshot(snap.ProjectionRecords)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range proofs {
		if p.Status != "" || p.Confirmations != 0 {
			t.Fatal("recovery must not persist chain-derived status")
		}
	}
	cold := newRGB11FlowManager(t, f.sender.wallet.Clone(), f.rpc, f.evidence, 9901)
	cold.rgbManager.scopeStates.stopReconciliations()
	if err = cold.rgbManager.importSelectedRGB11AccountRecoverySnapshot(snap); err != nil {
		t.Fatalf("safe quarantine import must succeed: %v", err)
	}
	proof, err := cold.rgbManager.projectionStore.LoadProof(f.carrier, f.asset)
	if err != nil || proof.Status != "inconsistent" || cold.GetRGB11ConsistencyStatus() != "broken" {
		t.Fatal("cold import did not rederive quarantine")
	}
	f.restoreInputs(t)
	_, err = f.sender.RefreshRGB11State(context.Background())
	if err != nil {
		t.Fatalf("positive reorg recovery: %v", err)
	}
	if p := f.journal(t); p.State.Status == "conflicted" || p.State.AckStatus == "accepted" {
		t.Fatal("journal not reevaluated or ACK fabricated")
	}
	f.assertProtected(t)
	f.assertImmutable(t)
}
