package wallet

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func TestRGB11PrivateCancelRequiresExactUnpublishedRecoveryDelta(t *testing.T) {
	sender, receiver, imported, _, _ := newRGB11GenericSendFixture(t)
	scope, err := sender.captureRGB11NetworkScope(false)
	if err != nil {
		t.Fatal(err)
	}
	scopeID, err := scope.rgb11AccountManagedScopeID()
	if err != nil {
		t.Fatal(err)
	}
	payloads, err := sender.currentAccountManagedProviderPayloads(rgb11AccountManagedProviderID)
	if err != nil {
		t.Fatal(err)
	}
	base, err := rgb11wallet.DecodeRecoveryPackage(payloads[scopeID].Payload)
	if err != nil {
		t.Fatal(err)
	}
	invoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID, AmountRaw: "100", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	// Stop before the account CAS, exactly as a failed prepare retains its
	// private signed material. The public result would be withheld at this point.
	scope.Manager.beginRGB11ManagedOperationState()
	prepared, err := scope.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1})
	scope.Manager.endRGB11ManagedOperationState()
	if err != nil {
		t.Fatal(err)
	}
	walletID, err := scope.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := scope.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	local, err := rgb11wallet.RecoveryPackageFromSnapshot(current, 0)
	if err != nil {
		t.Fatal(err)
	}
	if batch, err := unpublishedRGB11Batch(local, base, prepared.State.TransferID); err != nil || len(batch) != 1 || len(batch[0].SignedTx) == 0 {
		t.Fatalf("exact private prepared delta rejected: batch=%d err=%v", len(batch), err)
	}
	if _, err := unpublishedRGB11Batch(local, local, prepared.State.TransferID); err == nil {
		t.Fatal("already published batch treated as private")
	}
	for _, phase := range []string{"accepted", "broadcast-attempted"} {
		t.Run(phase, func(t *testing.T) {
			pending, err := scope.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
			if err != nil {
				t.Fatal(err)
			}
			switch phase {
			case "accepted":
				pending.State.AckStatus = "accepted"
			case "broadcast-attempted":
				pending.State.Status = rgb11StatusBroadcastAttempted
			}
			records, err := rgb11wallet.UpsertProjectionSnapshotPending(local.ProjectionRecords, pending)
			if err != nil {
				t.Fatal(err)
			}
			candidate := *local
			candidate.ProjectionRecords = records
			if _, err := unpublishedRGB11Batch(&candidate, base, prepared.State.TransferID); err == nil {
				t.Fatal("unsafe private cancellation permitted")
			}
		})
	}
	if err := scope.projectionStore.SaveLocalAssetName(imported.ContractID, "unpublished unrelated label"); err != nil {
		t.Fatal(err)
	}
	changed, _, err := scope.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := rgb11wallet.RecoveryPackageFromSnapshot(changed, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unpublishedRGB11Batch(other, base, prepared.State.TransferID); err == nil {
		t.Fatal("private cancellation discarded unrelated recovery changes")
	}
}

func TestRGB11PrivateCancelPreservesUncertainPublication(t *testing.T) {
	sender, receiver, imported, _, _ := newRGB11GenericSendFixture(t)
	invoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID, AmountRaw: "100", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	remote, ok := sender.http.(*rgb11MemoryDKVSHTTP)
	if !ok {
		t.Fatal("fixture lost its shared DKVS transport")
	}
	fault := &rgb11RejectDirtyAccountCASHTTP{rgb11MemoryDKVSHTTP: remote, owner: sender, transportFailure: true}
	sender.http = fault
	sender.dkvs.mu.Lock()
	for _, client := range sender.dkvs.clients {
		client.Http = fault
	}
	sender.dkvs.mu.Unlock()
	prepared, prepareErr := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1})
	if prepareErr == nil || prepared != nil {
		t.Fatal("unconfirmed publication exposed a prepared transfer")
	}
	sender.http = remote
	sender.dkvs.mu.Lock()
	for _, client := range sender.dkvs.clients {
		client.Http = remote
	}
	sender.dkvs.mu.Unlock()
	states, err := sender.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		t.Fatal(err)
	}
	var pending *rgb11wallet.PendingTransfer
	for _, state := range states {
		if state.Invoice == invoice.Invoice {
			pending, err = sender.rgbManager.projectionStore.LoadPendingTransfer(state.TransferID)
			break
		}
	}
	if err != nil || pending == nil || len(pending.SignedTx) == 0 {
		t.Fatalf("uncertain publication lacks private recovery material: %v", err)
	}
	store, err := sender.accountDKVSStore()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := newDKVSReplicaStore(sender.db).LoadOutbox(store.client.replicaNamespace)
	if err != nil || len(entries) == 0 {
		t.Fatalf("uncertain publication lost its durable outbox: %v", err)
	}
	if err := sender.cancelUnpublishedRGB11Transfer(pending.State.TransferID); !errors.Is(err, ErrDKVSPathNotSynced) {
		t.Fatalf("private cancel ignored uncertain account publication: %v", err)
	}
	after, err := sender.rgbManager.projectionStore.LoadPendingTransfer(pending.State.TransferID)
	if err != nil || !bytes.Equal(after.SignedTx, pending.SignedTx) || after.State.Status != "prepared" {
		t.Fatalf("uncertain publication recovery material changed: %v", err)
	}
	for _, point := range pending.State.InputOutPoints {
		if lock := sender.utxoLockerL1.GetLockedUtxoList()[point]; lock == nil || lock.ReservationID != pending.ReservationID {
			t.Fatal("uncertain publication released an input owner")
		}
	}
	afterEntries, err := newDKVSReplicaStore(sender.db).LoadOutbox(store.client.replicaNamespace)
	if err != nil || len(afterEntries) != len(entries) || afterEntries[0].RequestID != entries[0].RequestID {
		t.Fatal("private cancel discarded the uncertain publication outbox")
	}
}
