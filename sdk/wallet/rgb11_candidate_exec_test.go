package wallet

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// A PWA receive can finish while the ordinary L1 monitor is checking the
// prepared receive against Bitcoin. Only the local candidate may be rebuilt.
type rgb11FirstStatusGate struct {
	rgb11wallet.BitcoinEvidenceProvider
	mu      sync.Mutex
	blocked bool
	entered chan struct{}
	release chan struct{}
}

// Check the SDK context boundary after Bitcoin evidence was read but before
// the local commit gate. The PWA refresh bridge currently uses Background.
func TestRGBCancelBeforeCommit(t *testing.T) {
	f := newRGB11ConflictFixture(t, true)
	f.publish(t, true)
	current := f.snapshot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	candidate, _, err := f.sender.rgbManager.buildRGB11RefreshCandidate(ctx, current, 0)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := f.sender.rgbManager.resolveRGB11RecoveryProjection(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.sender.rgbManager.rgb11SnapshotLockPlan(current, resolved.snapshot, resolved.lockReasons)
	if err != nil {
		t.Fatal(err)
	}
	check := func() error {
		if err := resolved.checkEvidence(); err != nil {
			return err
		}
		cancel()
		return nil
	}
	err = f.sender.rgbManager.importRGB11ReservationSnapshotWithBaseline(ctx,
		resolved.snapshot, resolved.tickerInfos, resolved.lockReasons, &plan, check, current)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled refresh reached commit gate: %v", err)
	}
	if !reflect.DeepEqual(current.ProjectionRecords, f.snapshot(t).ProjectionRecords) {
		t.Fatal("canceled refresh changed durable projection")
	}
}

func (g *rgb11FirstStatusGate) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	g.mu.Lock()
	wait := !g.blocked
	if wait {
		g.blocked = true
	}
	g.mu.Unlock()
	if wait {
		close(g.entered)
		<-g.release
	}
	return g.BitcoinEvidenceProvider.GetTxStatus(txid)
}

func TestRGBReceiveRefreshOverlap(t *testing.T) {
	sender, recipient, asset, evidence, rpc := newRGB11GenericSendFixture(t)
	sender.rgbManager.scopeStates.stopReconciliations()
	recipient.rgbManager.scopeStates.stopReconciliations()
	ctx := context.Background()
	invoice, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: asset.ContractID,
		AmountRaw: "10000", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
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
	if _, err := recipient.PrepareRGB11Consignment(ctx, invoice.RequestID,
		[]byte(prepared.RecipientConsignment)); err != nil {
		t.Fatal(err)
	}
	confirmRGB11ProjectionTestTransfer(t, sender, evidence, rpc, prepared.State.TransferID)
	gate := &rgb11FirstStatusGate{BitcoinEvidenceProvider: evidence,
		entered: make(chan struct{}), release: make(chan struct{})}
	recipient.rgbManager.evidence = gate
	var once sync.Once
	unblock := func() { once.Do(func() { close(gate.release) }) }
	defer unblock()
	done := make(chan error, 1)
	go func() {
		_, err := recipient.RefreshRGB11State(ctx)
		done <- err
	}()
	select {
	case <-gate.entered:
	case err := <-done:
		t.Fatalf("monitor did not inspect prepared receive: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("monitor did not reach Bitcoin status lookup")
	}
	if _, err := recipient.AcceptRGB11Consignment(ctx, invoice.RequestID,
		[]byte(prepared.RecipientConsignment)); err != nil {
		t.Fatalf("PWA receive did not commit while monitor read evidence: %v", err)
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("monitor did not rebuild after local receive commit: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("monitor did not finish after receive commit")
	}
	balance, err := recipient.GetRGB11AssetBalance(&asset.AssetName)
	if err != nil || balance == nil || balance.Value == nil || balance.Value.Uint64() != 10000 {
		t.Fatalf("receive balance after monitor overlap: balance=%v err=%v", balance, err)
	}
	if evidence.broadcastCount != 1 {
		t.Fatalf("receive/monitor overlap rebroadcast the witness: count=%d", evidence.broadcastCount)
	}
}

// Business candidates compare persisted raw rows, while recovery tests retain
// their strict normalized-export fixture. Never substitute a backup copy for
// the local CAS baseline.
func localConflictSnap(t *testing.T, f *rgb11ConflictFixture) *RGB11WalletSnapshot {
	t.Helper()
	id, err := f.sender.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.sender.rgbManager.readRGB11Local(id)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
