package wallet

import (
	"context"
	"errors"
	btcwire "github.com/btcsuite/btcd/wire"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This test-only decorator keeps the built-in RGB provider's identity,
// validation and export. Only its Import fails, after production recovery has
// written the real durable Restoring marker. No production hook is added.
type nativeUIRGBImportFault struct {
	AccountManagedDataProvider
	refused atomic.Int64
}

func (f *nativeUIRGBImportFault) Import(AccountManagedDataCatalog, []AccountManagedDataPayload) error {
	f.refused.Add(1)
	return errors.New("native UI: persistent RGB module import failure")
}
func nativeUIInstallRGBFault(t *testing.T, m *Manager) *nativeUIRGBImportFault {
	t.Helper()
	m.managedDataMu.Lock()
	defer m.managedDataMu.Unlock()
	actual := m.managedDataProviders[rgb11AccountManagedProviderID]
	require.NotNil(t, actual)
	fault := &nativeUIRGBImportFault{AccountManagedDataProvider: actual}
	m.managedDataProviders[rgb11AccountManagedProviderID] = fault
	return fault
}

// Unexpected submission is counted and refused; ordinary reads still use the
// existing fixture backend. A gate error alone is not the broadcast oracle.
type nativeUINoSubmit struct {
	IndexerRPCClient
	calls atomic.Int64
}

func (g *nativeUINoSubmit) BroadCastTx(*btcwire.MsgTx) (string, error) {
	g.calls.Add(1)
	return "", errors.New("unexpected broadcast during partial recovery")
}
func (g *nativeUINoSubmit) BroadCastTxs([]*btcwire.MsgTx) error {
	g.calls.Add(1)
	return errors.New("unexpected broadcast during partial recovery")
}
func (g *nativeUINoSubmit) BroadCastTxsContext(context.Context, []*btcwire.MsgTx) error {
	g.calls.Add(1)
	return errors.New("unexpected broadcast during partial recovery")
}

// SDK UI scenario 11. Actual Manager APIs, production RGB provider and a
// physically reopened temporary DB use the existing signed in-memory DKVS
// service fixture. The browser-only assertion that partial recovery never
// displays a full-success page remains TestPWAPartialRestore under -tags=pwa.
func TestSDKPartialRestore(t *testing.T) {
	ctx := context.Background()
	source, remote, service, closeSource := newAccountGroup(t)
	source.http = remote
	root, err := source.ImportWallet(accountRootWrapperTestMnemonic, "password")
	require.NoError(t, err)
	require.NoError(t, source.InitializeAccountManagement("password"))
	discoverInactiveAccount(t, source)
	options, err := source.GetStorageOptions(ctx, 100)
	require.NoError(t, err)
	var selected *StorageOption
	for i := range options {
		if options[i].ID == AccountStorageTemporary {
			selected = &options[i]
		}
	}
	require.NotNil(t, selected)
	require.True(t, selected.Available)
	storage, err := source.ConfirmStorage(ctx, *selected)
	require.NoError(t, err)
	require.Equal(t, "ready", storage.Status)
	request := RecoverySetup{Password: "password", WalletIDs: []int64{root}, Mode: "2of2"}
	factors := RecoveryFactors{}
	for _, q := range coreQuestions() {
		request.Questions = append(request.Questions, QuestionInput{Question: Question{
			ID: q.Question.ID, Prompt: q.Question.Prompt}, Answer: q.Answer, Confirmation: q.Confirmation})
		factors.Answers = append(factors.Answers, Answer{QuestionID: q.Question.ID, Text: q.Answer})
	}
	created, err := source.CreateRecovery(ctx, request)
	require.NoError(t, err)
	require.True(t, created.CanActivate)
	for _, file := range created.Files {
		if file.Name == "user-share.txt" {
			factors.UserShare = string(file.Data)
		}
	}
	require.NotEmpty(t, factors.UserShare)
	active, err := source.ActivateRecovery(ctx, created.SessionID, ActivationInput{
		Password: "password", SavedLocator: created.Locator, Factors: factors})
	require.NoError(t, err)
	require.True(t, active.Active)
	require.NoError(t, source.SyncAccountManagementState(ctx))
	wait, cancel := context.WithTimeout(ctx, 20*time.Second)
	require.NoError(t, source.WaitAccountBackup(wait, "", ""))
	cancel()
	catalog := source.GetWalletCatalog()
	closeSource()

	target, _, _, closeTarget := newAccountGroup(t, remote)
	target.http = remote
	fault := nativeUIInstallRGBFault(t, target)
	loaded, err := target.LoadRecovery(ctx, created.Locator, false)
	require.NoError(t, err)
	preview, err := target.PreviewRecovery(ctx, loaded.SessionID, factors)
	require.NoError(t, err)
	require.True(t, preview.CanCommit)
	require.NotNil(t, preview.Summary)
	require.Len(t, preview.Summary.Wallets, len(catalog))
	result, err := target.CommitRecovery(ctx, loaded.SessionID, "restored-password")
	require.NoError(t, err)
	require.False(t, result.Complete, "successful wallet import cannot imply complete RGB restoration")
	require.Equal(t, []ModuleView{{Name: "rgb11", ErrorCode: "RESTORE_INCOMPLETE"}}, result.FailedModules)
	require.Equal(t, active.AccountID, result.AccountID)
	recoveredCatalog := target.GetWalletCatalog()
	require.Len(t, recoveredCatalog, len(catalog))
	for i := range catalog {
		require.Equal(t, catalog[i].Fingerprint, recoveredCatalog[i].Fingerprint)
		require.Equal(t, catalog[i].Name, recoveredCatalog[i].Name)
		require.Equal(t, catalog[i].Accounts, recoveredCatalog[i].Accounts)
	}
	require.Positive(t, fault.refused.Load(), "the real RGB import entry must have refused")
	_, err = target.CommitRecovery(ctx, loaded.SessionID, "restored-password")
	require.EqualError(t, err, "recovery session expired")
	nativeUIRequireRGBPartial(t, target)
	cfg := *target.cfg
	closeTarget()

	target, closeTarget = openAccountGroup(t, &cfg, remote, service)
	target.http = remote
	fault = nativeUIInstallRGBFault(t, target)
	// Inspect durable failure before unlocking can launch a startup retry.
	target.mutex.RLock()
	modules, err := target.listAccountLocal(active.AccountID)
	target.mutex.RUnlock()
	require.NoError(t, err)
	marked := false
	for _, module := range modules {
		if module.Provider == "rgb11" && !module.Deleted {
			require.True(t, module.Restoring)
			require.Contains(t, module.LastError, "persistent RGB module import failure")
			marked = true
		}
	}
	require.True(t, marked)
	_, err = target.UnlockWallet("restored-password")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_ = target.SyncAccountManagementState(ctx)
		return fault.refused.Load() > 0
	}, 20*time.Second, 100*time.Millisecond, "startup/retry must invoke the same failed RGB importer")
	nativeUIRequireRGBPartial(t, target)
	_, err = target.CommitRecovery(ctx, loaded.SessionID, "restored-password")
	require.EqualError(t, err, "recovery session expired")
	// Removing this test-only outage enables the existing retry path, never a
	// second recovery commit and never a fabricated successful module marker.
	target.StopDKVSSync()
	target.managedDataMu.Lock()
	target.managedDataProviders[rgb11AccountManagedProviderID] = fault.AccountManagedDataProvider
	target.managedDataMu.Unlock()
	require.NoError(t, target.SyncAccountManagementState(ctx))
	require.NoError(t, target.StartDKVSSync())
	require.Eventually(t, func() bool {
		view, readErr := target.RefreshAccount(ctx)
		if readErr != nil {
			return false
		}
		for _, module := range view.Modules {
			if module.Name == "rgb11" {
				return module.CanWrite && module.ErrorCode == ""
			}
		}
		return false
	}, 20*time.Second, 100*time.Millisecond)
	target.mutex.RLock()
	modules, err = target.listAccountLocal(active.AccountID)
	target.mutex.RUnlock()
	require.NoError(t, err)
	for _, module := range modules {
		if module.Provider == "rgb11" && !module.Deleted {
			require.False(t, module.Restoring)
			require.Empty(t, module.LastError)
		}
	}
	closeTarget()
}

func nativeUIRequireRGBPartial(t *testing.T, m *Manager) {
	t.Helper()
	ctx := context.Background()
	actual := m.l1IndexerClient.getActiveIndexer()
	guard := &nativeUINoSubmit{IndexerRPCClient: actual}
	m.SetIndexerHttpClient(guard)
	defer m.SetIndexerHttpClient(actual)
	view, err := m.GetAccountView(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, view.Modules)
	found := false
	for _, module := range view.Modules {
		if module.Name == "rgb11" {
			require.False(t, module.CanWrite)
			require.NotEmpty(t, module.ErrorCode)
			found = true
		}
	}
	require.True(t, found)
	_, err = m.RefreshRGBState(ctx)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrAccountManagedDataImportIncomplete) || errors.Is(err, ErrDKVSPathNotSynced), "actual synchronization gate required: %v", err)
	_, err = m.DeployTicker_brc20("gate", 10000, 100, 0, 2)
	require.True(t, errors.Is(err, ErrAccountManagedDataImportIncomplete) || errors.Is(err, ErrDKVSPathNotSynced), "BRC20 must be blocked before broadcast: %v", err)
	_, _, err = m.BatchSendPlainSats(m.GetWallet().GetAddress(), 1000, 1, 2, nil)
	require.True(t, errors.Is(err, ErrAccountManagedDataImportIncomplete) || errors.Is(err, ErrDKVSPathNotSynced), "L1 send must be blocked before broadcast: %v", err)
	require.Zero(t, guard.calls.Load(), "failed module must not reach any broadcast backend")
	before := m.GetWalletCatalog()
	_, err = m.GetRGBView()
	require.NoError(t, err)
	log, err := m.BeginOperationLog(OperationLogCreate{Category: "wallet", Action: "e2e_partial_local", Title: "Local log during incomplete restore"})
	require.NoError(t, err)
	_, err = m.UpdateOperationLog(log.ID, OperationLogUpdate{Status: OperationLogSucceeded})
	require.NoError(t, err)
	require.NoError(t, m.DeleteOperationHistory())
	require.Equal(t, before, m.GetWalletCatalog())
}
