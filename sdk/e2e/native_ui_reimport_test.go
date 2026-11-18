package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

func runNativeReimport(t *testing.T, f *nativeIfaceChain, pending bool) {
	ctx := context.Background()
	seed := byte(235)
	if !pending {
		seed = 237
	}
	owner := newNativeUIDevice(t, f.cfg, accountSyncMnemonic(t, seed))
	root := owner.mgr.GetCurrentWalletId()
	nativeUIFund(t, f, owner.mgr.GetWallet().GetAddress())
	asset := nativeUIIssue(t, owner, "NIA", "UIREVIEW")
	contract := nativeUIContract(t, owner, asset, ".rgb")
	mnemonic := accountSyncMnemonic(t, seed+1)
	child, err := owner.mgr.ImportWallet(mnemonic, owner.password)
	require.NoError(t, err)
	require.NoError(t, owner.mgr.UpdateWalletName(child, "Deleted wallet metadata"))
	require.NoError(t, owner.mgr.UpdateAccountMetadata(child, 0, "Deleted account metadata", "did:deleted:0"))
	require.NoError(t, owner.mgr.EnsureAccount(child, 1, "Deleted second account", "did:deleted:1"))
	require.NoError(t, owner.mgr.SwitchWallet(child, owner.password))
	childEntry := owner.mgr.GetWalletCatalog()[1]
	require.Equal(t, child, childEntry.ID)
	nativeUIFund(t, f, owner.mgr.GetWallet().GetAddress())
	_, err = owner.mgr.ImportRGBContract(ctx, contract)
	require.NoError(t, err)
	require.NoError(t, owner.mgr.SwitchWallet(root, owner.password))
	created := nativeUILocalBackup(t, owner)
	if pending {
		owner.http.rejectUploads(t)
	}
	refused := owner.http.refused.Load()
	require.NoError(t, owner.mgr.SwitchWallet(child, owner.password))
	original := nativeUIInvoice(t, owner, asset, "1", "witness", "")
	require.NoError(t, owner.mgr.SwitchWallet(root, owner.password))
	if !pending {
		nativeUIBackup(t, owner.mgr)
	}
	require.NoError(t, owner.mgr.DeleteWallet(child))
	for _, entry := range owner.mgr.GetWalletCatalog() {
		require.NotEqual(t, childEntry.Fingerprint, entry.Fingerprint)
	}
	if pending {
		_, err = owner.mgr.RefreshAccount(ctx)
		require.NoError(t, err)
		require.Eventually(t, func() bool { return owner.http.refused.Load() > refused }, 30*time.Second, 100*time.Millisecond,
			"pending case must actually refuse a deletion upload")
	} else {
		nativeUIDeletionBackup(t, owner.mgr)
		owner.reopen(t)
		nativeUIReady(t, owner.mgr)
	}
	newID, err := owner.mgr.ImportWallet(mnemonic, owner.password)
	require.NoError(t, err)
	owner.http.writeRefusal.Store(nil)
	require.NotEqual(t, child, newID)
	returned := accountReviewFind(t, owner.mgr, childEntry.Fingerprint)
	nativeUICleanWallet(t, returned)
	require.NoError(t, owner.mgr.SwitchWallet(newID, owner.password))
	nativeUIReady(t, owner.mgr)
	nativeUIEmptyRGB(t, owner.mgr)
	nativeUIDeletionBackup(t, owner.mgr)
	owner.reopen(t)
	nativeUIReady(t, owner.mgr)
	nativeUIEmptyRGB(t, owner.mgr)
	require.Len(t, accountReviewFind(t, owner.mgr, childEntry.Fingerprint).Accounts, 1)
	_, err = owner.mgr.ImportRGBContract(ctx, contract)
	require.NoError(t, err)
	next := nativeUIInvoice(t, owner, asset, "2", "witness", "")
	require.NotEqual(t, original.ID, next.ID)
	nativeUIBackup(t, owner.mgr)
	owner.close()
	restored := newNativeUIDevice(t, f.cfg, "")
	result := nativeUIRestore(t, restored, created)
	require.True(t, result.Complete)
	restored.reopen(t)
	nativeUIReady(t, restored.mgr)
	entry := accountReviewFind(t, restored.mgr, childEntry.Fingerprint)
	nativeUICleanWallet(t, entry)
	require.Equal(t, returned.Name, entry.Name)
	require.NoError(t, restored.mgr.SwitchWallet(entry.ID, restored.password))
	nativeUIReady(t, restored.mgr)
	view, err := restored.mgr.GetRGBView()
	require.NoError(t, err)
	foundAsset, foundReceive := false, false
	for _, a := range view.Assets {
		foundAsset = foundAsset || a.ContractID == asset.ContractID
	}
	for _, r := range view.Receives {
		require.NotEqual(t, original.ID, r.ID)
		foundReceive = foundReceive || r.ID == next.ID
	}
	require.True(t, foundAsset, "new explicit contract import must survive old cleanup and cold restore")
	require.True(t, foundReceive, "new invoice must survive old cleanup and cold restore")
}

func nativeUIDeletionBackup(t *testing.T, m *wallet.Manager) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := m.RefreshAccount(ctx)
	require.NoError(t, err)
	require.NoError(t, m.WaitAccountBackup(ctx, "", ""))
	// Directory confirmation can precede removal of old blobs and ACKs. Only
	// observe the worker from here, within the same original timeout.
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		view, err := m.GetAccountView(ctx)
		require.NoError(t, err)
		status := m.GetAccountManagementStatus()
		module := ""
		for _, v := range status.Modules {
			if v.BackupState != "backed_up" {
				module = v.Provider + ":" + v.BackupState
				break
			}
		}
		if view.BackupState == "backed_up" && view.BackupRisk == "" && status.BackupState == "backed_up" &&
			!status.ManagedDataDirty && status.PendingChanges == 0 && module == "" {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("deletion backup: UI=%s risk=%s state=%s dirty=%t pending=%d module=%s code=%s",
				view.BackupState, view.BackupRisk, status.BackupState, status.ManagedDataDirty, status.PendingChanges, module, status.LastDKVSSyncErrorCode)
		case <-tick.C:
		}
	}
}

func nativeUICleanWallet(t *testing.T, entry wallet.WalletCatalogEntry) {
	t.Helper()
	require.NotEqual(t, "Deleted wallet metadata", entry.Name)
	require.Len(t, entry.Accounts, 1)
	require.NotEqual(t, "Deleted account metadata", entry.Accounts[0].Name)
	require.Empty(t, entry.Accounts[0].DID)
	require.False(t, entry.Accounts[0].SPEnabled)
}
func nativeUIEmptyRGB(t *testing.T, m *wallet.Manager) {
	t.Helper()
	view, err := m.GetRGBView()
	require.NoError(t, err)
	require.Empty(t, view.Assets, "late deletion ACK must not resurrect a deleted contract")
	require.Empty(t, view.Receives, "late deletion ACK must not resurrect old invoices")
}
