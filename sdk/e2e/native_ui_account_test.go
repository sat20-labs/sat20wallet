package e2e

import (
	"context"
	"testing"
	"time"

	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func runNativeTwoFactor(t *testing.T, cfg *sdkcommon.Config) {
	ctx := context.Background()
	owner := newNativeUIDevice(t, cfg, accountSyncMnemonic(t, 221))
	root := owner.mgr.GetCurrentWalletId()
	require.NoError(t, owner.mgr.EnsureAccount(root, 1, "Backup account 1", ""))
	_, _, err := owner.mgr.CreateWallet(owner.password)
	require.NoError(t, err)
	require.NoError(t, owner.mgr.SwitchWallet(root, owner.password))
	request := nativeUISetupReq(owner.mgr, owner.password)
	require.Len(t, request.WalletIDs, 2)
	_, err = owner.mgr.PreflightRecovery("wrong-password", request.WalletIDs)
	require.Error(t, err)
	nativeUIConfirm(t, owner.mgr, nativeUIStorage(t, owner.mgr, "temporary", 100))
	require.NoError(t, owner.mgr.CancelStorageSetup())
	view, err := owner.mgr.GetAccountView(ctx)
	require.NoError(t, err)
	require.True(t, view.Storage == nil || !view.Storage.CanCancel)
	_, err = owner.mgr.CreateRecovery(ctx, request)
	require.ErrorIs(t, err, wallet.ErrAccountStorageAuthorizationMissing)
	nativeUIConfirm(t, owner.mgr, nativeUIStorage(t, owner.mgr, "temporary", 100))
	abandoned, err := owner.mgr.CreateRecovery(ctx, request)
	require.NoError(t, err)
	require.NoError(t, owner.mgr.AbortRecovery(abandoned.SessionID))
	_, err = owner.mgr.ActivateRecovery(ctx, abandoned.SessionID, wallet.ActivationInput{
		Password: owner.password, SavedLocator: abandoned.Locator, Factors: nativeUIFactors(t, abandoned)})
	require.EqualError(t, err, "activation session expired")
	created := nativeUILocalBackup(t, owner)
	view, err = owner.mgr.GetAccountView(ctx)
	require.NoError(t, err)
	require.Empty(t, view.BackupRisk)
	accountID := view.AccountID
	// Refuse uploads only: successful receiving must retain local write
	// qualification. The separate NoACK cases cover a full DKVS outage.
	refused := owner.http.refused.Load()
	owner.http.rejectUploads(t)
	require.NoError(t, owner.mgr.EnsureAccount(root, 2, "Created while upload offline", ""))
	view, err = owner.mgr.GetAccountView(ctx)
	require.NoError(t, err)
	require.Equal(t, "unconfirmed", view.BackupRisk)
	_, err = owner.mgr.RefreshAccount(ctx)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		v, readErr := owner.mgr.GetAccountView(ctx)
		return readErr == nil && v.ErrorCode != "" && owner.http.refused.Load() > refused
	}, 30*time.Second, 100*time.Millisecond, "the background worker must really encounter the outage")
	view, err = owner.mgr.GetAccountView(ctx)
	require.NoError(t, err)
	require.True(t, view.Modules[0].CanWrite, "failed upload does not close a synchronized local account")
	require.NoError(t, owner.mgr.EnsureAccount(root, 3, "After upload failure", ""))
	// Check the read side over the same faulted transport, without helping
	// the manager synchronize or advancing its write qualification.
	key, err := dkvs.AccountPersonalKey(accountID, "account/state")
	require.NoError(t, err)
	node := cfg.IndexerL2
	read := wallet.NewSatsNetDKVSClient(node.Scheme, node.Host, node.Proxy, owner.http)
	record, err := read.GetRecordDirectContext(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, record)
	require.Equal(t, key, record.Key)
	require.NoError(t, dkvs.VerifySignature(record))
	owner.http.writeRefusal.Store(nil)
	nativeUIBackup(t, owner.mgr)

	owner.http.dkvsOffline.Store(true)
	owner.reopen(t)
	view, err = owner.mgr.GetAccountView(ctx)
	require.NoError(t, err)
	require.False(t, view.Modules[0].CanWrite, "first receive is required on a cold runtime")
	nativeUIGate(t, owner)
	nativeUIGateError(t, owner.mgr.EnsureAccount(root, 4, "After initial sync", ""))
	_, err = owner.mgr.BatchSendAssets_SatsNet(owner.mgr.GetWallet().GetAddress(), "::", "1000", 1)
	nativeUIGateError(t, err)
	nativeUILocalViews(t, owner.mgr)
	owner.http.dkvsOffline.Store(false)
	nativeUIReady(t, owner.mgr)
	require.NoError(t, owner.mgr.EnsureAccount(root, 4, "After initial sync", ""))
	_, err = owner.mgr.RefreshRGBState(ctx)
	require.NoError(t, err)
	nativeUIBackup(t, owner.mgr)
	before := owner.mgr.GetWalletCatalog()
	owner.close()

	target := newNativeUIDevice(t, cfg, "")
	_, err = target.mgr.LoadRecovery(ctx, "invalid recovery package", false)
	require.Error(t, err)
	loaded, err := target.mgr.LoadRecovery(ctx, created.Locator, false)
	require.NoError(t, err)
	_, err = target.mgr.CommitRecovery(ctx, loaded.SessionID, target.password)
	require.EqualError(t, err, "account recovery preview is required")
	factors := nativeUIFactors(t, created)
	bad := nativeUIFactors(t, created)
	for i := range bad.Answers {
		bad.Answers[i].Text = "wrong answer"
	}
	_, err = target.mgr.PreviewRecovery(ctx, loaded.SessionID, bad)
	require.Error(t, err)
	preview, err := target.mgr.PreviewRecovery(ctx, loaded.SessionID, factors)
	require.NoError(t, err)
	require.True(t, preview.CanCommit)
	require.Len(t, preview.Summary.Wallets, 2)
	require.Empty(t, target.mgr.GetWalletCatalog(), "preview must not import wallets")
	require.NoError(t, target.mgr.AbortRecovery(loaded.SessionID))
	_, err = target.mgr.CommitRecovery(ctx, loaded.SessionID, target.password)
	require.EqualError(t, err, "recovery session expired")
	loaded, err = target.mgr.LoadRecovery(ctx, created.Locator, false)
	require.NoError(t, err)
	_, err = target.mgr.PreviewRecovery(ctx, loaded.SessionID, factors)
	require.NoError(t, err)
	target.reopen(t)
	_, err = target.mgr.CommitRecovery(ctx, loaded.SessionID, target.password)
	require.EqualError(t, err, "recovery session expired")
	target.password += "-restored"
	result := nativeUIRestore(t, target, created)
	require.True(t, result.Complete)
	require.Empty(t, result.FailedModules)
	require.Equal(t, accountID, result.AccountID)
	target.reopen(t)
	nativeUIReady(t, target.mgr)
	nativeUICatalog(t, target.mgr, before)
	view, err = target.mgr.GetAccountView(ctx)
	require.NoError(t, err)
	require.Equal(t, accountID, view.AccountID)
}

func nativeUIGate(t *testing.T, d *nativeUIDevice) {
	t.Helper()
	before := d.http.broadcasts.Load()
	_, _, err := d.mgr.BatchSendPlainSats(d.mgr.GetWallet().GetAddress(), 1000, 1, 2, nil)
	nativeUIGateError(t, err)
	_, err = d.mgr.DeployTicker_brc20("gate", 10000, 100, 0, 2)
	nativeUIGateError(t, err)
	_, err = d.mgr.RefreshRGBState(context.Background())
	nativeUIGateError(t, err)
	require.Equal(t, before, d.http.broadcasts.Load(), "write gates must not call the broadcast backend")
}
func nativeUIGateError(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	require.Contains(t, []string{"DKVS managed path is not synchronized", "account-managed data import is incomplete; restore the wallet before uploading"}, err.Error())
}
func nativeUILocalViews(t *testing.T, m *wallet.Manager) {
	t.Helper()
	before := m.GetWalletCatalog()
	_, err := m.GetAccountView(context.Background())
	require.NoError(t, err)
	_, err = m.GetRGBView()
	require.NoError(t, err)
	entry, err := m.BeginOperationLog(wallet.OperationLogCreate{Category: "wallet", Action: "e2e_local_log", Title: "Local log during synchronization"})
	require.NoError(t, err)
	_, err = m.UpdateOperationLog(entry.ID, wallet.OperationLogUpdate{Status: wallet.OperationLogSucceeded})
	require.NoError(t, err)
	_, err = m.GetOperationViews()
	require.NoError(t, err)
	require.NoError(t, m.DeleteOperationHistory())
	require.Equal(t, before, m.GetWalletCatalog())
}
