package wallet

import (
	"context"
	"errors"
	"testing"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func reviewEnableRecovery(t *testing.T, manager *Manager) {
	t.Helper()
	manager.mutex.Lock()
	manager.accountProfile.RecoveryConfigured = true
	err := manager.saveAccountManagementProfileLocked()
	manager.mutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func TestReviewRGB11BaselineRechecksOperationPreconditions(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	t.Run("deleted_source_must_not_execute_on_root", func(t *testing.T) {
		local, other, _ := reviewAccountDevices(t)
		childID, _, err := other.CreateWallet("password")
		if err != nil {
			t.Fatal(err)
		}
		fingerprint := walletFingerprint(other.walletInfoMap[childID].Wallet)
		if err := other.SyncAccountManagementState(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := local.SyncAccountManagementState(context.Background()); err != nil {
			t.Fatal(err)
		}
		child := reviewCatalogWallet(t, local, fingerprint)
		if child == nil {
			t.Fatal("fixture did not apply the remote child wallet")
		}
		if err := local.SwitchWallet(child.ID, "password"); err != nil {
			t.Fatal(err)
		}
		reviewEnableRecovery(t, local)
		if err := other.DeleteWallet(childID); err != nil {
			t.Fatal(err)
		}
		if err := other.SyncAccountManagementState(context.Background()); err != nil {
			t.Fatal(err)
		}
		called := false
		executedFingerprint := ""
		_, err = runRGB11ManagedOperation(local, context.Background(), rgb11ManagedOperationNew,
			func(manager *rgb11Manager) (struct{}, error) {
				called = true
				executedFingerprint = walletFingerprint(manager.wallet)
				return struct{}{}, nil
			})
		if reviewCatalogWallet(t, local, fingerprint) != nil {
			t.Fatal("fixture did not apply the remote deletion during baseline sync")
		}
		if called || err == nil {
			t.Fatalf("source changed during baseline sync but operation executed: called=%v requested=%s executed=%s err=%v",
				called, fingerprint, executedFingerprint, err)
		}
	})

	t.Run("baseline_imports_pending_before_new_operation", func(t *testing.T) {
		local, other, remote := reviewAccountDevices(t)
		reviewEnableRecovery(t, local)
		reviewEnableRecovery(t, other)
		client := newRGB11MessageNodeClient(remote)
		other.serverNode = NewNode(client, "message.test", SERVER_NODE,
			client.CoreNodePubKey(), client.CoreNodePubKey())
		root, err := other.accountManagementRootWallet()
		if err != nil {
			t.Fatal(err)
		}
		if err := other.bindAccountToCurrentCoreNode(root); err != nil {
			t.Fatal(err)
		}
		// Preserve the registry needed by recovery while stopping background reconciliation.
		for _, manager := range []*Manager{local, other} {
			manager.rgbManager.scopeStates.stopReconciliations()
			manager.rgbManager.evidence = newCoreE2EChain()
		}
		if err := other.rgbManager.projectionStore.SaveTransferState(&rgb11wallet.TransferState{
			TransferID: "review-remote-active", Direction: "receive", Status: "pending", AddressMode: true,
		}); err != nil {
			t.Fatal(err)
		}
		if err := other.SyncAccountManagementState(context.Background()); err != nil {
			t.Fatalf("publish stable recovery fixture: %v", err)
		}
		if active, err := local.hasAccountManagedRGB11Transition(); err != nil || active {
			t.Fatalf("local fixture must start without active state: active=%v err=%v", active, err)
		}
		called := false
		_, err = runRGB11ManagedOperation(local, context.Background(), rgb11ManagedOperationNew,
			func(manager *rgb11Manager) (struct{}, error) {
				called = true
				state, readErr := manager.projectionStore.LoadTransferState("review-remote-active")
				if readErr != nil {
					return struct{}{}, readErr
				}
				if state == nil || state.Direction != "receive" || state.Status != "pending" {
					return struct{}{}, errors.New("remote pending state was not imported before operation callback")
				}
				return struct{}{}, nil
			})
		if active, inspectErr := local.hasAccountManagedRGB11Transition(); inspectErr != nil || !active {
			t.Fatalf("baseline did not import the remote active transition: active=%v err=%v operationErr=%v", active, inspectErr, err)
		}
		if !called || err != nil {
			t.Fatalf("baseline did not restore remote pending before unrelated work: called=%v err=%v", called, err)
		}
	})
}
