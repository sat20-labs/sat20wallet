package wallet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/btcsuite/btcd/chaincfg"
	indexer "github.com/sat20-labs/indexer/common"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"strings"
	"testing"
	"time"
)

// These tests exercise local reservation transactions. Account synchronization
// is covered by the public API gate tests; prepare through the existing local
// manager so failures reach the reservation boundary being tested.
func TestRGB11ReceiveReservationUsesExactRequestID(t *testing.T) {
	w := NewInternalWalletWithMnemonic("comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params)
	source := newRGB11FlowManager(t, w, &rgb11FlowIndexer{}, &rgb11FlowEvidence{}, 815)
	req, err := source.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: "out-of-band", AmountRaw: "2", Expiry: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	state := &rgb11wallet.TransferState{TransferID: "exact-request", Direction: "receive", Status: "pending", Invoice: req.Invoice}
	t.Run("no invoice lookup compatibility", func(t *testing.T) {
		if err := source.rgbManager.projectionStore.SaveTransferState(state); !errors.Is(err, ErrRGB11Inconsistent) {
			t.Fatalf("receive state without request ID accepted: %v", err)
		}
	})
	state.ReceiveRequestID = req.RequestID
	if err := source.rgbManager.projectionStore.SaveTransferState(state); err != nil {
		t.Fatal(err)
	}
	engineRecords, err := source.rgbManager.engineStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	projectionRecords, err := source.rgbManager.projectionStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	target := newRGB11FlowManager(t, w, &rgb11FlowIndexer{}, &rgb11FlowEvidence{}, 816)
	if err := target.rgbManager.importRGB11ReservationSnapshot(&RGB11WalletSnapshot{EngineRecords: engineRecords, ProjectionRecords: projectionRecords}); err != nil {
		t.Fatalf("fresh scope must bind the incoming snapshot request, not the old local engine: %v", err)
	}
	reservations, err := target.rgbManager.loadRGB11Reservations()
	if err != nil || len(reservations) != 1 || reservations[0].RequestID != req.RequestID || reservations[0].State == nil || reservations[0].State.TransferID != state.TransferID {
		t.Fatalf("snapshot lost exact receive binding: %+v %v", reservations, err)
	}
}

func TestRGB11InvoiceUsesReservationFramework(t *testing.T) {
	w := NewInternalWalletWithMnemonic("comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params)
	mgr := newRGB11FlowManager(t, w, &rgb11FlowIndexer{}, &rgb11FlowEvidence{}, 810)
	req, err := mgr.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: "out-of-band", AmountRaw: "42", Expiry: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	all, err := LoadAllResvFromDB(mgr.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("invoice %s has no durable framework resv: %d", req.RequestID, len(all))
	}
	for _, r := range all {
		if r.GetType() != "rgb11" {
			t.Fatalf("wrong resv type %s", r.GetType())
		}
	}
}

func TestRGB11ReservationExpiryProtectsKnownTransfer(t *testing.T) {
	for _, used := range []bool{false, true} {
		t.Run(fmt.Sprint(used), func(t *testing.T) {
			w := NewInternalWalletWithMnemonic("comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params)
			mgr := newRGB11FlowManager(t, w, &rgb11FlowIndexer{}, &rgb11FlowEvidence{}, 811)
			req, err := mgr.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: "out-of-band", AmountRaw: "2", Expiry: time.Now().Add(time.Hour).Unix()})
			if err != nil {
				t.Fatal(err)
			}
			outpoint := strings.Repeat("ab", 32) + ":0"
			if err := mgr.utxoLockerL1.TryReserve([]string{outpoint}, rgb11wallet.LockReasonPending, "receive-owner"); err != nil {
				t.Fatal(err)
			}
			if err := mgr.rgbManager.projectionStore.SaveReceiveReservation(&rgb11wallet.ReceiveReservation{Version: 1, RequestID: req.RequestID, OutPoint: outpoint, ReservationID: "receive-owner", Expiry: req.Expiry}); err != nil {
				t.Fatal(err)
			}
			if used {
				// Crash boundary: validated tx is durable before engine ACK is persisted.
				if err := mgr.rgbManager.projectionStore.SaveTransferState(&rgb11wallet.TransferState{TransferID: "transfer", Direction: "receive", Status: "awaiting_broadcast", AckStatus: "accepted", Invoice: req.Invoice, ReceiveRequestID: req.RequestID, WitnessTxID: strings.Repeat("cd", 32)}); err != nil {
					t.Fatal(err)
				}
			}
			if err := mgr.rgbManager.releaseExpiredRGB11ReceiveReservations(req.Expiry + 1); err != nil {
				t.Fatal(err)
			}
			locked := mgr.utxoLockerL1.GetLockedUtxoList()[outpoint] != nil
			if locked != used {
				t.Fatalf("used=%v lock=%v", used, locked)
			}
		})
	}
}

func TestRGB11ReservationBatchFailureDoesNotPublishInvoice(t *testing.T) {
	w := NewInternalWalletWithMnemonic("comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params)
	mgr := newRGB11FlowManager(t, w, &rgb11FlowIndexer{}, &rgb11FlowEvidence{}, 812)
	failed := &failNthFlushDB{KVDB: mgr.db, failAt: 1}
	mgr.db = failed // Atomic publication uses the account owner DB.
	replacement, err := newRGB11Manager(mgr, failed, mgr.utxoLockerL1, &rgb11FlowEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	mgr.rgbManager = replacement
	if err := replacement.selectRGB11Scope(); err != nil {
		t.Fatal(err)
	}
	req, err := mgr.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: "out-of-band", AmountRaw: "2", Expiry: time.Now().Add(time.Hour).Unix()})
	if err == nil || req != nil {
		t.Fatalf("failed batch published invoice: %+v %v", req, err)
	}
	if failed.count != 1 {
		t.Fatalf("invoice did not reach the injected atomic flush: count=%d", failed.count)
	}
	records, err := replacement.engineStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	all, err := LoadAllResvFromDB(mgr.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 || len(all) != 0 || len(mgr.GetAllResv()) != 0 {
		t.Fatalf("partial invoice/resv survived batch failure")
	}
}

// Called by the native-consensus out-of-band and standard proxy virtual
// Bitcoin flows. Restart uses the durable DB and the shared resv loader.
func assertRGB11FrameworkRecovery(t *testing.T, managers ...*Manager) {
	t.Helper()
	for _, mgr := range managers {
		before, err := mgr.rgbManager.loadRGB11Reservations()
		if err != nil {
			t.Fatal(err)
		}
		transfers, err := mgr.rgbManager.projectionStore.ListTransfers()
		if err != nil {
			t.Fatal(err)
		}
		for _, state := range transfers {
			count := 0
			for _, r := range before {
				if r.State != nil && r.State.Direction == state.Direction && r.State.TransferID == state.TransferID && r.State.WitnessTxID == state.WitnessTxID {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("transfer %s/%s has %d resvs", state.Direction, state.TransferID, count)
			}
		}
		restarted, err := newRGB11Manager(mgr, mgr.db, mgr.utxoLockerL1, mgr.rgbManager.evidence)
		if err != nil {
			t.Fatal(err)
		}
		mgr.rgbManager = restarted
		if err := restarted.selectRGB11Scope(); err != nil {
			t.Fatal(err)
		}
		after, err := restarted.loadRGB11Reservations()
		if err != nil {
			t.Fatal(err)
		}
		a, _ := json.Marshal(before)
		b, _ := json.Marshal(after)
		if !bytes.Equal(a, b) {
			t.Fatalf("resv changed across restart")
		}
		for _, r := range after {
			persisted, err := LoadReservation(mgr.db, nil, RESV_TYPE_RGB11, r.Id)
			if err != nil {
				t.Fatal(err)
			}
			if persisted.GetType() != RESV_TYPE_RGB11 {
				t.Fatal("not loaded by common framework")
			}
		}
		public, err := restarted.rgb11ReservationViews()
		if err != nil {
			t.Fatal(err)
		}
		b, _ = json.Marshal(public)

		for _, secret := range []string{"seal_disclosure", "signed_tx", "blinding", "witness_script"} {
			if bytes.Contains(b, []byte(secret)) {
				t.Fatalf("secret in public resv view: %s", secret)
			}
		}
	}
}

func TestRGB11ReservationSnapshotFailureKeepsPreviousGeneration(t *testing.T) {
	w := NewInternalWalletWithMnemonic("comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params)
	mgr := newRGB11FlowManager(t, w, &rgb11FlowIndexer{}, &rgb11FlowEvidence{}, 813)
	req, err := mgr.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: "out-of-band", AmountRaw: "2", Expiry: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	original, err := mgr.rgbManager.loadRGB11Reservations()
	if err != nil {
		t.Fatal(err)
	}
	failed := &failNthFlushDB{KVDB: mgr.db, failAt: 1}
	mgr.db = failed
	replacement, err := newRGB11Manager(mgr, failed, mgr.utxoLockerL1, &rgb11FlowEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	mgr.rgbManager = replacement
	if err := replacement.selectRGB11Scope(); err != nil {
		t.Fatal(err)
	}
	if err := replacement.importRGB11ReservationSnapshot(&RGB11WalletSnapshot{}); err == nil {
		t.Fatal("snapshot flush unexpectedly succeeded")
	}
	if _, err := replacement.engine.LoadReceive(req.RequestID); err != nil {
		t.Fatalf("old receive was lost: %v", err)
	}
	after, err := replacement.loadRGB11Reservations()
	if err != nil || len(after) != 1 || after[0].Id != original[0].Id {
		t.Fatalf("old resv was lost: %+v %v", after, err)
	}
	if err := replacement.importRGB11ReservationSnapshot(&RGB11WalletSnapshot{}); err != nil {
		t.Fatal(err)
	}
	after, err = replacement.loadRGB11Reservations()
	if err != nil || len(after) != 0 {
		t.Fatalf("successful empty snapshot retained old resv: %+v %v", after, err)
	}
}

func TestRGB11ReservationPublicViewIsReadOnlyAndRedacted(t *testing.T) {
	w := NewInternalWalletWithMnemonic("comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params)
	mgr := newRGB11FlowManager(t, w, &rgb11FlowIndexer{}, &rgb11FlowEvidence{}, 814)
	req, err := mgr.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: "out-of-band", AmountRaw: "2", Expiry: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	state := &rgb11wallet.TransferState{Invoice: req.Invoice, ReceiveRequestID: req.RequestID, Asset: indexer.AssetInfo{Amount: *indexer.NewDefaultDecimal(2)}, TransferID: "imported-transfer", Direction: "receive", Status: "pending", WitnessTxID: "tx", ReceiveCapabilityKey: "SECRET-capability", RelayRecordKey: "SECRET-relay", AckRecordKey: "SECRET-ack", DKVSOperationID: "SECRET-operation"}
	if err := mgr.rgbManager.projectionStore.SaveTransferState(state); err != nil {
		t.Fatal(err)
	}
	readsOnly := &failNthFlushDB{KVDB: mgr.db, failAt: 1}
	mgr.db = readsOnly
	replacement, err := newRGB11Manager(mgr, readsOnly, mgr.utxoLockerL1, &rgb11FlowEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	mgr.rgbManager = replacement
	if err := replacement.selectRGB11Scope(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		result, err := mgr.GetRGB11State()
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Reservations) != 1 {
			t.Fatalf("active receive resv omitted")
		}
		raw, err := json.Marshal(result.Reservations)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("SECRET")) || bytes.Contains(raw, []byte("receive_capability_key")) || bytes.Contains(raw, []byte("ack_record_key")) || bytes.Contains(raw, []byte("relay_record_key")) {
			t.Fatalf("public resv leaked private keys")
		}
	}
	if readsOnly.count != 0 {
		t.Fatalf("GetRGB11State performed %d writes", readsOnly.count)
	}
}

func TestRGB11MissingFrameworkReservationFailsClosed(t *testing.T) {
	w := NewInternalWalletWithMnemonic("comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params)
	mgr := newRGB11FlowManager(t, w, &rgb11FlowIndexer{}, &rgb11FlowEvidence{}, 815)

	// Simulate an impossible partial write by deliberately bypassing the new
	// reservation transaction hook. There is no released legacy format to accept:
	// once the normal hook is restored, the SDK must fail closed.
	mgr.rgbManager.engineStore.SetReservationPersistence(nil)
	witnessScript, err := AddrToPkScript(w.GetAddress(), GetChainParam())
	if err != nil {
		t.Fatal(err)
	}
	var internalXOnly [32]byte
	copy(internalXOnly[:], w.GetPubKey().SerializeCompressed()[1:])
	amount := uint64(2)
	req, err := mgr.rgbManager.engine.CreateReceive(corewallet.ReceiveParams{
		Mode: corewallet.ReceiveWitness, Network: rgb11InvoiceNetwork(GetChainParam()),
		RecipientID: "missing-framework-reservation", Amount: &amount, WitnessVout: 1,
		WitnessScript: witnessScript, InternalXOnly: &internalXOnly,
		Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.rgbManager.engineStore.SetReservationPersistence(mgr.rgbManager)
	if _, err := mgr.rgbManager.engine.LoadReceive(req.RequestID); err != nil {
		t.Fatalf("fixture receive request missing: %v", err)
	}
	if _, err := mgr.GetRGB11State(); !errors.Is(err, ErrRGB11Inconsistent) {
		t.Fatalf("missing framework reservation was accepted: %v", err)
	}
}

type rgb11FailReceiveKeyDB struct {
	indexer.KVDB
	rejected int
}

type rgb11FailReceiveKeyBatch struct {
	indexer.WriteBatch
	owner *rgb11FailReceiveKeyDB
}

func (d *rgb11FailReceiveKeyDB) NewWriteBatch() indexer.WriteBatch {
	return &rgb11FailReceiveKeyBatch{WriteBatch: d.KVDB.NewWriteBatch(), owner: d}
}

func (b *rgb11FailReceiveKeyBatch) Put(key, value []byte) error {
	if bytes.Contains(key, []byte("-receive-key-")) {
		b.owner.rejected++
		return errors.New("injected receive key write failure")
	}
	return b.WriteBatch.Put(key, value)
}

func (d *rgb11FailReceiveKeyDB) Write(key, value []byte) error {
	if bytes.Contains(key, []byte("-receive-key-")) {
		d.rejected++
		return errors.New("injected receive key write failure")
	}
	return d.KVDB.Write(key, value)
}

func TestRGB11ReservationInvoicePostCreateFailureRollsBack(t *testing.T) {
	w := NewInternalWalletWithMnemonic("comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params)
	mgr := newRGB11FlowManager(t, w, &rgb11FlowIndexer{}, &rgb11FlowEvidence{}, 816)
	failed := &rgb11FailReceiveKeyDB{KVDB: mgr.db}
	mgr.db = failed
	replacement, err := newRGB11Manager(mgr, failed, mgr.utxoLockerL1, &rgb11FlowEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	mgr.rgbManager = replacement
	if err := replacement.selectRGB11Scope(); err != nil {
		t.Fatal(err)
	}
	req, err := mgr.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: RGB11ProxyTransport, TransportEndpoints: []string{testRGB11ProxyTransport}, AmountRaw: "2", Expiry: time.Now().Add(time.Hour).Unix()})
	if err == nil || req != nil {
		t.Fatalf("failed invoice was returned")
	}
	if failed.rejected != 1 {
		t.Fatalf("invoice did not reach the injected receive-key write: count=%d", failed.rejected)
	}
	records, err := replacement.engineStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	all, err := LoadAllResvFromDB(mgr.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 || len(all) != 0 || len(mgr.GetAllResv()) != 0 {
		t.Fatal("unpublished invoice survived auxiliary write failure")
	}
}

func TestRGB11FrameworkRejectsPartialStoreSnapshotImport(t *testing.T) {
	w := NewInternalWalletWithMnemonic("comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params)
	mgr := newRGB11FlowManager(t, w, &rgb11FlowIndexer{}, &rgb11FlowEvidence{}, 816)

	req, err := mgr.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", AmountRaw: "2",
		Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if req == nil {
		t.Fatal("invoice request missing")
	}
	projection, err := mgr.rgbManager.projectionStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	engine, err := mgr.rgbManager.engineStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.rgbManager.projectionStore.ImportSnapshot(projection); !errors.Is(err, ErrRGB11Inconsistent) {
		t.Fatalf("partial projection snapshot import bypassed common reservation: %v", err)
	}
	if err := mgr.rgbManager.engineStore.ImportSnapshot(engine); !errors.Is(err, ErrRGB11Inconsistent) {
		t.Fatalf("partial engine snapshot import bypassed common reservation: %v", err)
	}
}

// An unused invoice expires normally; restoring its exact snapshot must keep
// history readable without recreating an actionable receive reservation.
func TestRGB11ExpiredUnusedInvoiceSnapshotReadback(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired=%v", expired), func(t *testing.T) {
			w := NewInternalWalletWithMnemonic("comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params)
			mgr := newRGB11FlowManager(t, w, &rgb11FlowIndexer{}, &rgb11FlowEvidence{}, 861)
			expiry := time.Now().Add(time.Hour).Unix()
			if expired {
				expiry = time.Now().Unix() + 3
			}
			req, err := mgr.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: "out-of-band", AmountRaw: "2", Expiry: expiry})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := mgr.GetRGB11State(); err != nil {
				t.Fatalf("new invoice readback: %v", err)
			}
			snapshot, _, err := mgr.rgbManager.exportRGB11WalletSnapshot("expiry-readback")
			if err != nil {
				t.Fatal(err)
			}
			if expired {
				for time.Now().Unix() <= expiry {
					time.Sleep(10 * time.Millisecond)
				}
			}
			if err := mgr.rgbManager.importRGB11WalletSnapshot(snapshot); err != nil {
				t.Fatalf("restore exact snapshot: %v", err)
			}
			preserved, err := mgr.rgbManager.engine.LoadReceive(req.RequestID)
			if err != nil || preserved.Invoice != req.Invoice || preserved.Expiry != expiry {
				t.Fatalf("invoice history changed on restore: %v", err)
			}
			reservations, err := mgr.rgbManager.loadRGB11Reservations()
			if err != nil {
				t.Fatal(err)
			}
			if expired && len(reservations) != 0 {
				t.Fatalf("expired unused invoice recreated reservation: %d", len(reservations))
			}
			state, err := mgr.GetRGB11State()
			if err != nil {
				t.Fatalf("normal invoice expiry made wallet state unreadable: %v", err)
			}
			if expired && len(state.Reservations) != 0 {
				t.Fatal("expired unused invoice exposed as actionable")
			}
			if !expired && len(state.Reservations) != 1 {
				t.Fatal("active invoice lost reservation")
			}
		})
	}
}

// This exception control guards the skip: a known receive must still fail
// closed if its common reservation is missing, even after invoice expiry.
func TestRGB11ExpiredUsedInvoiceMissingReservationFailsClosed(t *testing.T) {
	w := NewInternalWalletWithMnemonic("comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params)
	mgr := newRGB11FlowManager(t, w, &rgb11FlowIndexer{}, &rgb11FlowEvidence{}, 862)
	expiry := time.Now().Unix() + 3
	req, err := mgr.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: "out-of-band", AmountRaw: "2", Expiry: expiry})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.rgbManager.projectionStore.SaveTransferState(&rgb11wallet.TransferState{TransferID: "known-receive", Direction: "receive", Status: "awaiting_broadcast", AckStatus: "accepted", Invoice: req.Invoice, ReceiveRequestID: req.RequestID, WitnessTxID: strings.Repeat("cd", 32)}); err != nil {
		t.Fatal(err)
	}
	reservations, err := mgr.rgbManager.loadRGB11Reservations()
	if err != nil {
		t.Fatal(err)
	}
	removed := 0
	for _, r := range reservations {
		if r.RequestID == req.RequestID {
			if err := mgr.db.Delete([]byte(GetResvKey(RESV_TYPE_RGB11, r.Id))); err != nil {
				t.Fatal(err)
			}
			removed++
		}
	}
	if removed == 0 {
		t.Fatal("control did not remove matching reservation")
	}
	for time.Now().Unix() <= expiry {
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := mgr.GetRGB11State(); !errors.Is(err, ErrRGB11Inconsistent) {
		t.Fatalf("expired used receive without reservation was accepted: %v", err)
	}
}
