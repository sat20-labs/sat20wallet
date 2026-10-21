package wallet

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type rgb11ScopeWaitEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (e *rgb11ScopeWaitEvidence) wait() { e.once.Do(func() { close(e.started); <-e.release }) }
func (e *rgb11ScopeWaitEvidence) GetUTXO(s string) (*rgb11wallet.BitcoinUTXO, error) {
	e.wait()
	return e.BitcoinEvidenceProvider.GetUTXO(s)
}
func (e *rgb11ScopeWaitEvidence) GetRawTx(s string) ([]byte, error) {
	e.wait()
	return e.BitcoinEvidenceProvider.GetRawTx(s)
}
func (e *rgb11ScopeWaitEvidence) GetTxStatus(s string) (*rgb11wallet.BitcoinTxStatus, error) {
	e.wait()
	return e.BitcoinEvidenceProvider.GetTxStatus(s)
}
func (e *rgb11ScopeWaitEvidence) GetOutspend(s string) (*rgb11wallet.BitcoinOutspend, error) {
	e.wait()
	return e.BitcoinEvidenceProvider.GetOutspend(s)
}
func (e *rgb11ScopeWaitEvidence) GetTip() (*rgb11wallet.BitcoinTip, error) {
	e.wait()
	return e.BitcoinEvidenceProvider.GetTip()
}

// A real validation may finish after UI selection changes. Its immutable
// receipt belongs only to the scope which requested it, never the new account.
func TestRGB11NetworkValidationKeepsCapturedAccount(t *testing.T) {
	sender, receiver, _, evidence, _ := newRGB11GenericSendFixture(t)
	sender.rgbManager.scopeStates.stopReconciliations()
	receiver.rgbManager.scopeStates.stopReconciliations()
	proofs, err := sender.rgbManager.projectionStore.ListProofs()
	if err != nil || len(proofs) == 0 {
		t.Fatalf("setup proofs: %v", err)
	}
	raw, err := sender.rgbManager.projectionStore.LoadObject(proofs[0].ConsignmentHash)
	if err != nil {
		t.Fatal(err)
	}
	account, err := receiver.rgbManager.fixedRGB11ScopeAccount()
	if err != nil {
		t.Fatal(err)
	}
	original, err := receiver.newScopedRGB11Manager(account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = original.projectionStore.LoadValidationReceipt(proofs[0].ConsignmentHash); !errors.Is(err, indexer.ErrKeyNotFound) {
		t.Fatalf("setup: recipient already has receipt: %v", err)
	}
	wait := &rgb11ScopeWaitEvidence{BitcoinEvidenceProvider: evidence, started: make(chan struct{}), release: make(chan struct{})}
	receiver.rgbManager.evidence = wait
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(wait.release) }) }
	defer release()
	validated := make(chan error, 1)
	go func() { _, err := receiver.ValidateRGB11Consignment(context.Background(), raw); validated <- err }()
	select {
	case <-wait.started:
	case err := <-validated:
		t.Fatalf("setup never reached evidence: %v", err)
	case <-time.After(4 * time.Second):
		t.Fatal("setup evidence timeout")
	}
	switched := make(chan error, 1)
	go func() { switched <- receiver.SwitchAccount(1) }()
	switchedBeforeReturn := false
	select {
	case err := <-switched:
		if err != nil {
			t.Errorf("switch: %v", err)
		}
		switchedBeforeReturn = true
	case <-time.After(2 * time.Second):
		t.Error("account switch waited for remote validation")
	}
	release()
	select {
	case err := <-validated:
		if err != nil {
			t.Errorf("validation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("validation cleanup timeout")
	}
	if !switchedBeforeReturn {
		select {
		case err := <-switched:
			if err != nil {
				t.Errorf("switch cleanup: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("switch cleanup timeout")
		}
	}
	if receiver.status.CurrentAccount != 1 {
		t.Fatalf("selected account=%d", receiver.status.CurrentAccount)
	}
	if _, err := original.projectionStore.LoadValidationReceipt(proofs[0].ConsignmentHash); err != nil {
		t.Errorf("original account lost validation receipt: %v", err)
	}
	if _, err := receiver.rgbManager.projectionStore.LoadValidationReceipt(proofs[0].ConsignmentHash); !errors.Is(err, indexer.ErrKeyNotFound) {
		t.Errorf("validation leaked into newly selected account: %v", err)
	}
}

// An unlocked network phase must never publish a candidate over a newer local
// change. Both writers use the existing reservation commit gate.
func TestRGB11CandidateRejectsChangedLocalBaseline(t *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "unchanged-control"
		if changed {
			name = "newer-local-write"
		}
		t.Run(name, func(t *testing.T) {
			manager, _, imported, _, _ := newRGB11GenericSendFixture(t)
			manager.rgbManager.scopeStates.stopReconciliations()
			walletID, err := manager.rgbManager.RGB11WalletID()
			if err != nil {
				t.Fatal(err)
			}
			before, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				if err := manager.rgbManager.projectionStore.SaveLocalAssetName(imported.ContractID, "New local label"); err != nil {
					t.Fatal(err)
				}
			}
			_, err = manager.rgbManager.applyRGB11CandidateSnapshot(context.Background(), before, before)
			if !changed {
				if err != nil {
					t.Errorf("unchanged local baseline: %v", err)
				}
				return
			}
			if !errors.Is(err, errAccountSnapshotChanged) {
				t.Errorf("stale candidate accepted or wrong error: %v", err)
			}
			alias, loadErr := manager.rgbManager.projectionStore.LoadLocalAssetName(imported.ContractID)
			if loadErr != nil || alias != "New local label" {
				t.Errorf("newer local metadata overwritten: alias=%q err=%v", alias, loadErr)
			}
		})
	}
}

// A fixed view shares persistent reservations with the real owner. Closing or
// discarding an invoice must publish to that owner's RAM as well as the DB.
func TestRGB11CapturedScopePublishesReservationCleanupToOwner(t *testing.T) {
	for _, action := range []string{"expire", "discard"} {
		t.Run(action, func(t *testing.T) {
			manager := newRGB11MultiDeviceManager(t, newRGB11StatusPrivateKey(t), 905)
			scoped, err := manager.captureRGB11NetworkScope(false)
			if err != nil {
				t.Fatal(err)
			}
			requestID := createRGB11MultiDeviceInvoice(t, scoped.Manager, "captured-cleanup")
			id := rgb11TransferResvID(scoped.rgb11ScopeKey(), "receive:"+requestID)
			if manager.GetResv(id) == nil {
				t.Fatal("setup did not publish the invoice reservation to its owner")
			}
			if action == "discard" {
				if err := scoped.discardUnpublishedRGB11Receive(requestID); err != nil {
					t.Fatal(err)
				}
				if manager.GetResv(id) != nil {
					t.Fatal("discard left the unpublished reservation in owner RAM")
				}
				if _, err := LoadReservation(manager.db, nil, RESV_TYPE_RGB11, id); !errors.Is(err, indexer.ErrKeyNotFound) {
					t.Fatalf("discard retained the reservation in DB: %v", err)
				}
				return
			}
			request, err := scoped.engine.LoadReceive(requestID)
			if err != nil {
				t.Fatal(err)
			}
			if err := scoped.releaseExpiredRGB11ReceiveReservations(request.Expiry + 1); err != nil {
				t.Fatal(err)
			}
			stored, err := LoadReservation(manager.db, nil, RESV_TYPE_RGB11, id)
			if err != nil || stored.(*RGB11TransferReservation).Status != RS_CLOSED {
				t.Fatalf("expired invoice was not closed in DB: %v", err)
			}
			if manager.GetResv(id).(*RGB11TransferReservation).Status != RS_CLOSED {
				t.Fatal("expired invoice remained open in owner RAM")
			}
		})
	}
}
