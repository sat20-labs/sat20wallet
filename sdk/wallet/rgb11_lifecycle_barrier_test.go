package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func TestRGB11PreparedTransferDoesNotExposeBroadcastableSignedPSBT(t *testing.T) {
	preparedType := reflect.TypeOf(RGB11PreparedTransfer{})
	if _, exists := preparedType.FieldByName("SignedPSBT"); exists {
		t.Fatal("ordinary prepared-transfer DTO exposes signed PSBT that callers can broadcast before the durable intent barrier")
	}
	encoded, err := json.Marshal(RGB11PreparedTransfer{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"signed_psbt"`)) {
		t.Fatalf("ordinary prepared-transfer JSON exposes signed PSBT: %s", encoded)
	}

	resumeType := reflect.TypeOf(RGB11PreparedTransferPackage{})
	if _, exists := resumeType.FieldByName("SignedPSBT"); exists {
		t.Fatal("resume transport DTO must remain safe and must not expose signed PSBT")
	}
}

func TestRGB11BroadcastIntentSurvivesSettledRecipientCompaction(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager := configuredRGB11ManagedOperationTestManager(t)
	const transferID = "lifecycle-settled-recipient-compaction"
	saveRGB11StatusTransfer(t, manager, transferID, "prepared")
	before, err := manager.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		t.Fatal(err)
	}
	settled := *before
	settled.State.Status = "settled"
	if err := manager.rgbManager.projectionStore.SavePendingTransferState(&settled); err != nil {
		t.Fatal(err)
	}
	if err := manager.rgbManager.projectionStore.CompactSettledRecipientConsignments([]string{transferID}); err != nil {
		t.Fatal(err)
	}
	after, err := manager.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.RecipientConsignment) == 0 || len(after.RecipientConsignment) != 0 {
		t.Fatalf("fixture did not compact recipient cache: before=%d after=%d",
			len(before.RecipientConsignment), len(after.RecipientConsignment))
	}
	if !sameRGB11BroadcastIntent(before, after) {
		t.Fatal("terminal status or duplicate recipient bytes changed immutable broadcast intent identity")
	}
}

func TestRGB11AccountManagedSyncPublishesActiveRecoveryClosure(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager := configuredRGB11ManagedOperationTestManager(t)
	const transferID = "lifecycle-active-transfer-in-stable-closure"
	saveRGB11StatusTransfer(t, manager, transferID, "prepared")

	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatalf("stable account sync must be able to publish active RGB recovery material: %v", err)
	}
	payloads, err := manager.currentAccountManagedProviderPayloads(rgb11AccountManagedProviderID)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := manager.accountManagedDataCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Scopes) != 1 {
		t.Fatalf("unexpected test account scope count: %d", len(catalog.Scopes))
	}
	payload, ok := payloads[catalog.Scopes[0].ID()]
	if !ok {
		t.Fatalf("stable recovery payload omitted scope %q", catalog.Scopes[0].ID())
	}
	recovery, err := rgb11wallet.DecodeRecoveryPackage(payload.Payload)
	if err != nil {
		t.Fatalf("decode stable RGB11 recovery package: %v", err)
	}
	found := false
	for _, record := range recovery.ProjectionRecords {
		if record.Key == "pending-"+transferID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("stable recovery package omitted active transfer %q", transferID)
	}
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatalf("repeated stable closure publication must be idempotent: %v", err)
	}
}

func TestRGB11ManagedNewDoesNotUseActiveTransitionAsAccountWideBusyGate(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager := configuredRGB11ManagedOperationTestManager(t)
	manager.rgbManager.scopeStates.stopReconciliations()
	saveRGB11StatusTransfer(t, manager, "unrelated-pending-transfer", "prepared")
	called := false
	_, err := runRGB11ManagedOperation(manager, context.Background(), rgb11ManagedOperationNew,
		func(*rgb11Manager) (struct{}, error) {
			called = true
			return struct{}{}, nil
		})
	if err != nil || !called {
		t.Fatalf("an unrelated pending RGB operation blocked new scoped work: called=%t err=%v", called, err)
	}
	if _, err := manager.rgbManager.projectionStore.LoadPendingTransfer("unrelated-pending-transfer"); err != nil {
		t.Fatalf("existing operation was lost while allowing unrelated work: %v", err)
	}
}
