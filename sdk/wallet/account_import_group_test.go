package wallet

import (
	"context"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/stretchr/testify/require"
)

// Add only a provider boundary to the normal production Manager. Production
// providers remain registered, and every device owns its real database.
func newImportGroup(t *testing.T, remote *rgb11MemoryDKVSHTTP) (*Manager, *managedImportTestProvider, func()) {
	t.Helper()
	manager, _, _, close := newAccountGroup(t, remote)
	manager.http = remote
	provider := managedImportProvider(t, manager)
	if err := manager.RegisterAccountManagedDataProvider(provider); err != nil {
		t.Fatal(err)
	}
	return manager, provider, close
}

func openImportGroup(t *testing.T, cfg *common.Config, remote *rgb11MemoryDKVSHTTP) (*Manager, *managedImportTestProvider, func()) {
	t.Helper()
	service := newRGB11MessageNodeClient(remote)
	manager, close := openAccountGroup(t, cfg, remote, service)
	manager.http = remote
	provider := managedImportProvider(t, manager)
	if err := manager.RegisterAccountManagedDataProvider(provider); err != nil {
		t.Fatal(err)
	}
	return manager, provider, close
}

func newImportSource(t *testing.T, remote *rgb11MemoryDKVSHTTP) (*Manager, *managedImportTestProvider, func()) {
	t.Helper()
	source, provider, close := newImportGroup(t, remote)
	if _, err := source.ImportWallet(accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	if err := source.InitializeAccountManagement("password"); err != nil {
		t.Fatal(err)
	}
	provider.payloads = []AccountManagedDataPayload{{Scope: AccountManagedDataGlobalScope, Payload: []byte("A")}}
	pkg, auth, secret := rereviewRecoveryPackage(t, source)
	if err := source.ActivateAccountManagement(secret, "password", *auth,
		pkg.Envelope.Locator, "account://"+pkg.Envelope.Locator.PackageID); err != nil {
		t.Fatal(err)
	}
	if err := source.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	return source, provider, close
}

func importGroupMaterial(t *testing.T, source *Manager) (account.Locator, AccountIndexerLocation) {
	t.Helper()
	status := source.GetAccountManagementStatus()
	if !status.RecoveryConfigured || status.PackageID == "" || status.RecoveryMode == "" {
		t.Fatal("source has no published recovery identity")
	}
	location, err := source.AccountIndexerLocation()
	if err != nil {
		t.Fatal(err)
	}
	return account.Locator{Version: account.Version, AccountID: status.AccountID,
		PackageID: status.PackageID, RecoveryMode: account.RecoveryMode(status.RecoveryMode)}, location
}

func restoreImportGroup(t *testing.T, target, source *Manager) error {
	t.Helper()
	locator, location := importGroupMaterial(t, source)
	value, err := target.LoadAccountManagementStateForRecovery(location, locator,
		source.accountSecret, accountRootWrapperTestMnemonic)
	if err != nil {
		return err
	}
	_, err = target.RestoreAccountManagementState(*value, source.accountSecret, "password", locator,
		AccountManagementRestoreOptions{Location: location, StorageMode: AccountStorageTemporary,
			RecordTTL: testRGB11FreeLocalTTL, PublicLocator: source.GetAccountManagementStatus().PublicLocator})
	return err
}

// Restoring copies does not complete a new device's online write baseline.
// A failed independent provider stays blocked while healthy wallets qualify.
func syncImportGroup(t *testing.T, manager *Manager, wantErr error) {
	t.Helper()
	err := manager.SyncAccountManagementState(context.Background())
	if wantErr == nil {
		require.NoError(t, err)
	} else {
		require.ErrorIs(t, err, wantErr)
		require.True(t, accountModuleOnly(err))
	}
	require.NoError(t, manager.CheckAccountWrite("wallets", AccountManagedDataGlobalScope))
}

// Read the real per-module target; no whole-account import marker is created.
func importModuleTarget(t *testing.T, manager *Manager, provider string, want []byte) *accountLocalKV {
	t.Helper()
	assertManagedImportModuleProtected(t, manager, provider)
	value, err := manager.readAccountLocal(manager.accountProfile.AccountID, provider, AccountManagedDataGlobalScope)
	require.NoError(t, err)
	require.NotNil(t, value)
	require.NotNil(t, value.Baseline)
	require.Equal(t, value.Baseline.Value, value.Value)
	entry := account.ManagedEntry{Provider: value.Provider, Scope: value.Scope, Key: value.Key, Codec: value.Codec}
	payload, err := openAccountPayload(manager.accountSecret, value.AccountID, manager.cfg.Chain, entry, value.Value)
	require.NoError(t, err)
	defer zeroBytes(payload)
	require.Equal(t, want, payload)
	return value
}

func assertImportComplete(t *testing.T, manager *Manager, provider *managedImportTestProvider, want []byte) {
	t.Helper()
	require.NoError(t, manager.CheckAccountRestore(provider.id, AccountManagedDataGlobalScope))
	value, err := manager.readAccountLocal(manager.accountProfile.AccountID, provider.id, AccountManagedDataGlobalScope)
	require.NoError(t, err)
	require.NotNil(t, value)
	require.False(t, value.Restoring)
	require.Empty(t, value.LastError)
	require.Len(t, provider.payloads, 1)
	require.Equal(t, want, provider.payloads[0].Payload)
}
