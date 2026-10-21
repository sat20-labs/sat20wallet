package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	txid, outpoint      string
	statusCalls         int
	firstStatusUnknown  bool
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
}

func (e *delayedSpendEvidence) GetUTXO(outpoint string) (*rgb11wallet.BitcoinUTXO, error) {
	if e.hiddenOutputs[outpoint] {
		e.hiddenOutputCalls++
		delete(e.hiddenOutputs, outpoint)
		return nil, nil
	}
	return e.BitcoinEvidenceProvider.GetUTXO(outpoint)
}

func (e *delayedSpendEvidence) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if txid == e.txid {
		e.statusCalls++
		if e.statusCalls == 1 {
			e.firstStatusUnknown = true
			return &rgb11wallet.BitcoinTxStatus{TxID: txid}, nil
		}
		if e.statusCalls == 2 && e.statusOverride {
			if e.fallbackStatusErr != nil {
				return nil, e.fallbackStatusErr
			}
			if e.fallbackStatus == nil {
				return nil, nil
			}
			status := *e.fallbackStatus
			return &status, nil
		}
	}
	return e.BitcoinEvidenceProvider.GetTxStatus(txid)
}

func (e *delayedSpendEvidence) GetRawTx(txid string) ([]byte, error) {
	if txid == e.txid {
		e.rawCalls++
		if e.rawOverride {
			return append([]byte(nil), e.fallbackRaw...), e.fallbackRawErr
		}
	}
	return e.BitcoinEvidenceProvider.GetRawTx(txid)
}

func (e *delayedSpendEvidence) GetOutspend(outpoint string) (*rgb11wallet.BitcoinOutspend, error) {
	if outpoint == e.outpoint {
		e.targetOutspendCalls++
		return &rgb11wallet.BitcoinOutspend{Spent: true, SpendingTx: "unknown"}, nil
	}
	return e.BitcoinEvidenceProvider.GetOutspend(outpoint)
}

func TestRGB11ExpectedRawSpend(t *testing.T) {
	fixture := newRGB11ExpectedRawSpendFallbackFixture(t)
	result, refreshErr := fixture.sender.RefreshRGB11State(context.Background())
	if refreshErr != nil || result.Settled != 1 || fixture.sender.GetRGB11ConsistencyStatus() != "ok" {
		t.Fatalf("verified expected spend unresolved: result=%+v err=%v consistency=%s",
			result, refreshErr, fixture.sender.GetRGB11ConsistencyStatus())
	}
	if fixture.fault.statusCalls < 2 {
		t.Fatalf("expected status fallback was not exercised: calls=%d", fixture.fault.statusCalls)
	}
	stored, err := fixture.sender.rgbManager.projectionStore.LoadPendingTransfer(fixture.pending.State.TransferID)
	if err != nil || stored.State.Status != "settled" {
		t.Fatalf("transfer=%+v err=%v", stored, err)
	}
}

type rgb11ExpectedRawSpendFallbackFixture struct {
	sender   *Manager
	imported *RGB11ImportResult
	pending  *rgb11wallet.PendingTransfer
	evidence *rgb11FlowEvidence
	fault    *delayedSpendEvidence
	source   string
}

func newRGB11ExpectedRawSpendFallbackFixture(t *testing.T) rgb11ExpectedRawSpendFallbackFixture {
	t.Helper()
	sender, recipient, imported, evidence, rpc := newRGB11GenericSendFixture(t)
	ctx := context.Background()
	balance, err := sender.GetRGB11AssetBalance(&imported.AssetName)
	if err != nil || balance == nil || balance.Value.Sign() <= 0 {
		t.Fatalf("balance=%v err=%v", balance, err)
	}
	source := rgb11GenericFixtureSource(t, sender, imported)
	invoice, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID,
		AmountRaw: balance.Value.String(), WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := sender.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	pending.State.Status, pending.State.AckStatus = "broadcast", "accepted"
	if err := sender.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	txid := tx.TxHash().String()
	target := pending.State.InputOutPoints[0]
	evidence.mu.Lock()
	evidence.rawTx[txid] = append([]byte(nil), pending.SignedTx...)
	for _, input := range tx.TxIn {
		outpoint := input.PreviousOutPoint.String()
		evidence.spendingTx[outpoint] = "unknown"
		delete(evidence.utxos, outpoint)
		delete(rpc.outputs, outpoint)
	}
	rpc.plain = nil
	for vout, output := range tx.TxOut {
		outpoint := fmt.Sprintf("%s:%d", txid, vout)
		evidence.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: output.Value,
			PkScript: append([]byte(nil), output.PkScript...), Confirmations: 6,
		}
		view := indexer.NewTxOutput(output.Value)
		view.OutPointStr = outpoint
		view.OutValue.PkScript = append([]byte(nil), output.PkScript...)
		rpc.outputs[outpoint] = view
		rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{
			OutPoint: outpoint, Value: output.Value, PkScript: output.PkScript,
		})
	}
	evidence.mu.Unlock()
	evidence.setStatus(txid, rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 6})
	hiddenOutputs := make(map[string]bool, len(pending.State.OutputOutPoints))
	for _, outpoint := range pending.State.OutputOutPoints {
		hiddenOutputs[outpoint] = true
	}
	fault := &delayedSpendEvidence{
		BitcoinEvidenceProvider: evidence, txid: txid, outpoint: target,
		hiddenOutputs: hiddenOutputs,
	}
	sender.rgbManager.evidence = fault
	return rgb11ExpectedRawSpendFallbackFixture{
		sender: sender, imported: imported, pending: pending,
		evidence: evidence.rgb11FlowEvidence, fault: fault, source: source,
	}
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
