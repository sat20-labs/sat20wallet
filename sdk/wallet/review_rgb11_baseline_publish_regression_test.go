package wallet

import (
	"context"
	"errors"
	"testing"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// A baseline PUT used to return before active-mailbox import. Rechecking only
// local transition state is insufficient when that publication fast path runs.
func TestReviewRGB11BaselineImportsActiveAfterLocalPublication(t *testing.T) {
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
			reviewEnableRecovery(t, local)
			reviewEnableRecovery(t, other)
			client := newRGB11MessageNodeClient(remote)
			other.serverNode = NewNode(client, "message.test", SERVER_NODE,
				client.CoreNodePubKey(), client.CoreNodePubKey())
			root, err := other.accountManagementRootWallet()
			if err != nil { t.Fatal(err) }
			if err := other.bindAccountToCurrentCoreNode(root); err != nil { t.Fatal(err) }
			for _, manager := range []*Manager{local, other} {
				if manager.rgbManager.scopeStates != nil {
					manager.rgbManager.scopeStates.stopReconciliations()
				}
				manager.rgbManager.scopeStates = nil
			}
			if err := other.rgbManager.projectionStore.SaveTransferState(&rgb11wallet.TransferState{
				TransferID: "review-active-before-local-put", Direction: "receive",
				Status: "pending", AddressMode: true,
			}); err != nil { t.Fatal(err) }
			if err := other.syncAccountManagedActiveData(rgb11AccountManagedProviderID); err != nil {
				t.Fatalf("publish remote active fixture: %v", err)
			}
			if active, err := local.hasAccountManagedRGB11Transition(); err != nil || active {
				t.Fatalf("local fixture unexpectedly active: %v %v", active, err)
			}
			if localEdit {
				if err := local.UpdateWalletName(local.GetAccountManagementStatus().RootWalletID,
					"Local edit requiring baseline PUT"); err != nil { t.Fatal(err) }
			}
			called := false
			_, err = runRGB11ManagedOperation(local, context.Background(), rgb11ManagedOperationNew,
				func(*rgb11Manager) (struct{}, error) {
					called = true
					return struct{}{}, nil
				})
			if called || !errors.Is(err, ErrRGB11ManagedOperationActive) {
				t.Errorf("baseline publication bypassed remote active recovery: called=%v err=%v", called, err)
			}
			if active, err := local.hasAccountManagedRGB11Transition(); err != nil || !active {
				t.Errorf("remote active recovery was not imported after baseline publication: active=%v err=%v", active, err)
			}
		})
	}
}
