package wallet

import (
	"context"
	"errors"
	"testing"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Explicit account synchronization restores remote recovery before local work.
// The subsequent business operation must not perform another backup transaction.
func TestReviewRGB11ExplicitSyncImportsActiveAlongsideLocalCatalogEdits(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	for _, localEdit := range []bool{false, true} {
		name := "unchanged_baseline_control"
		if localEdit {
			name = "local_publication_must_not_skip_remote_active"
		}
		t.Run(name, func(t *testing.T) {
			local, other, remote := reviewAccountDevices(t)
			if !local.accountManagedRecoveryConfigured() || !other.accountManagedRecoveryConfigured() {
				t.Fatal("paired devices did not establish real account recovery")
			}
			root, err := other.accountManagementRootWallet()
			if err != nil {
				t.Fatal(err)
			}
			if err := other.bindAccountToCurrentCoreNode(root); err != nil {
				t.Fatal(err)
			}
			for _, manager := range []*Manager{local, other} {
				if manager.rgbManager.scopeStates != nil {
					manager.rgbManager.scopeStates.stopReconciliations()
				}
				manager.rgbManager.evidence = newCoreE2EChain()
			}
			if err := other.rgbManager.projectionStore.SaveTransferState(&rgb11wallet.TransferState{
				TransferID: "review-active-before-local-put", Direction: "receive",
				Status: "pending", AddressMode: true,
			}); err != nil {
				t.Fatal(err)
			}
			if err := other.SyncAccountManagementState(context.Background()); err != nil {
				t.Fatalf("publish remote stable recovery fixture: %v", err)
			}
			if active, err := local.hasAccountManagedRGB11Transition(); err != nil || active {
				t.Fatalf("local fixture unexpectedly active: %v %v", active, err)
			}
			if localEdit {
				if err := local.UpdateWalletName(local.GetAccountManagementStatus().RootWalletID,
					"Local edit requiring baseline PUT"); err != nil {
					t.Fatal(err)
				}
			}
			// A local wallet-name edit and remote RGB recovery are independent modules.
			if err := local.SyncAccountManagementState(context.Background()); err != nil {
				t.Fatalf("explicit synchronization before local work: %v", err)
			}
			if localEdit {
				entry := reviewCatalogWallet(t, local, local.GetAccountManagementStatus().RootFingerprint)
				if entry == nil || entry.Name != "Local edit requiring baseline PUT" {
					t.Fatal("importing remote RGB recovery lost the local wallet name")
				}
			}
			called := false
			_, err = runRGB11ManagedOperation(local, context.Background(), rgb11ManagedOperationNew,
				func(manager *rgb11Manager) (struct{}, error) {
					called = true
					state, readErr := manager.projectionStore.LoadTransferState("review-active-before-local-put")
					if readErr != nil {
						return struct{}{}, readErr
					}
					if state == nil || state.Direction != "receive" || state.Status != "pending" {
						return struct{}{}, errors.New("remote pending state was not imported before operation callback")
					}
					return struct{}{}, nil
				})
			if !called || err != nil {
				t.Errorf("baseline did not restore remote pending before unrelated work: called=%v err=%v", called, err)
			}
			if active, err := local.hasAccountManagedRGB11Transition(); err != nil || !active {
				t.Errorf("remote active recovery was not imported after baseline publication: active=%v err=%v", active, err)
			}
			if localEdit {
				if err := other.SyncAccountManagementState(context.Background()); err != nil {
					t.Fatal(err)
				}
				cold := reviewColdCopy(t, local, remote)
				state, err := cold.rgbManager.projectionStore.LoadTransferState("review-active-before-local-put")
				if err != nil || state == nil || state.Direction != "receive" || state.Status != "pending" {
					t.Fatalf("cold recovery lost the imported pending RGB transfer: %v", err)
				}
				fingerprint := local.GetAccountManagementStatus().RootFingerprint
				for _, manager := range []*Manager{other, cold} {
					entry := reviewCatalogWallet(t, manager, fingerprint)
					if entry == nil || entry.Name != "Local edit requiring baseline PUT" {
						t.Fatal("remote/cold recovery lost the local wallet name")
					}
				}
			}
		})
	}
}
