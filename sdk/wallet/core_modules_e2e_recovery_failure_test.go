package wallet

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Delegate export/validation and successful imports to the actual RGB11
// provider. Only the first import returns a local module failure; the recovery
// bundle still comes from real issuance, native proofs and paid DKVS records.
type coreRGBImportFailureOnce struct {
	AccountManagedActiveDataProvider
	failure error
	imports int
}

func (p *coreRGBImportFailureOnce) Import(catalog AccountManagedDataCatalog, payloads []AccountManagedDataPayload) error {
	p.imports++
	if p.imports == 1 {
		return p.failure
	}
	return p.AccountManagedActiveDataProvider.Import(catalog, payloads)
}

func coreRGBRecoveryFailuresE2E(t *testing.T, source *Manager, cfg coreE2EConfig,
	chain *coreE2EChain, material *coreRecoveryMaterial) {
	t.Helper()
	load := func(t *testing.T) (*Manager, *RecoveredAccountManagementState) {
		t.Helper()
		target := coreNewE2EManager(t, cfg, chain, "")
		state, err := target.LoadAccountManagementStateForRecovery(material.auth.Location,
			material.locator, source.accountSecret, coreE2ESenderMnemonic)
		coreRequire(t, "read authenticated RGB recovery bundle from paid DKVS", err)
		rgbItems := 0
		for _, item := range state.ManagedData.Items {
			if item.Provider == rgb11AccountManagedProviderID && len(item.Payload) > 0 {
				rgbItems++
			}
		}
		coreAssert(t, rgbItems >= 3, "failure fixture must include actual RGB ownership in root and child accounts")
		coreVerifyPaidRecords(t, source, cfg)
		return target, state
	}
	options := AccountManagementRestoreOptions{Location: material.auth.Location,
		StorageMode: AccountStoragePaid, AutopayContract: material.auth.Autopay.PoolContract}

	for _, scenario := range []struct {
		name   string
		mutate func(*RecoveredAccountManagementState)
	}{
		{"MissingRGB11BundleRejectedWithoutInstallingWallets", func(state *RecoveredAccountManagementState) { state.ManagedDataEnvelope = nil }},
		{"CorruptRGB11BundleRejectedWithoutInstallingWallets", func(state *RecoveredAccountManagementState) {
			state.ManagedDataEnvelope = append([]byte(nil), state.ManagedDataEnvelope...)
			state.ManagedDataEnvelope[len(state.ManagedDataEnvelope)-1] ^= 1
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			target, state := load(t)
			beforeBroadcasts := chain.broadcastCount()
			scenario.mutate(state)
			_, err := target.RestoreAccountManagementState(*state, source.accountSecret, coreE2EPassword, material.locator, options)
			assert.Error(t, err, "missing/corrupt nonempty RGB backup must fail authentication")
			assert.Empty(t, target.GetWalletCatalog(), "failed validation must not install wallet keys")
			assert.False(t, target.GetAccountManagementStatus().Active)
			assert.Equal(t, beforeBroadcasts, chain.broadcastCount(), "failed recovery must not broadcast")
		})
	}

	t.Run("RGB11ProviderFailureMustAllowSameDatabaseRetry", func(t *testing.T) {
		target, state := load(t)
		failure := errors.New("core E2E: transient RGB11 provider import failure")
		target.managedDataMu.Lock()
		provider := target.managedDataProviders[rgb11AccountManagedProviderID]
		active, ok := provider.(AccountManagedActiveDataProvider)
		if !ok {
			target.managedDataMu.Unlock()
			t.Fatal("core-e2e: actual RGB11 provider must preserve its active-data interface")
		}
		fault := &coreRGBImportFailureOnce{AccountManagedActiveDataProvider: active, failure: failure}
		target.managedDataProviders[rgb11AccountManagedProviderID] = fault
		target.managedDataMu.Unlock()
		require.NotNil(t, provider)
		beforeBroadcasts := chain.broadcastCount()
		remoteBefore := coreRemoteStableRGB(t, source, cfg)
		_, err := target.RestoreAccountManagementState(*state, source.accountSecret, coreE2EPassword, material.locator, options)
		require.ErrorIs(t, err, failure, "must exercise the actual provider import boundary")
		require.Equal(t, 1, fault.imports)
		beforeCatalog := target.GetWalletCatalog()
		require.ErrorIs(t, target.UpdateWalletName(target.GetAccountManagementStatus().RootWalletID, "Blocked during RGB restore"), ErrAccountManagedDataImportIncomplete)
		assert.Equal(t, beforeCatalog, target.GetWalletCatalog())
		assert.Zero(t, target.GetAccountManagementStatus().PendingChanges)
		require.ErrorIs(t, target.SyncAccountManagementState(context.Background()), ErrAccountManagedDataImportIncomplete,
			"an incomplete restore must not publish partial RGB data")
		assert.Equal(t, coreDigest(remoteBefore), coreDigest(coreRemoteStableRGB(t, source, cfg)))
		assert.Equal(t, beforeBroadcasts, chain.broadcastCount())

		// No new device, DB clearing or replacement recovery material. The
		// transient fault is gone; retry must restore the complete original data.
		_, err = target.RestoreAccountManagementState(*state, source.accountSecret, coreE2EPassword, material.locator, options)
		coreRequire(t, "retry RGB import on the same database after transient failure", err)
		require.Equal(t, 2, fault.imports)
		coreSynchronizeRestoredAccount(t, target)
		assert.Equal(t, coreDigest(coreCanonicalCatalog(source)), coreDigest(coreCanonicalCatalog(target)))
		selectedWallet, selectedAccount := source.GetCurrentWalletId(), source.GetCurrentAccountId()
		defer func() {
			_ = source.SwitchWallet(selectedWallet, coreE2EPassword)
			source.SwitchAccount(selectedAccount)
		}()
		for _, entry := range source.GetWalletCatalog() {
			var restoredID int64
			for _, restored := range target.GetWalletCatalog() {
				if restored.Fingerprint == entry.Fingerprint {
					restoredID = restored.ID
				}
			}
			require.NotZero(t, restoredID)
			coreRequire(t, "select source wallet after retry", source.SwitchWallet(entry.ID, coreE2EPassword))
			coreRequire(t, "select recovered wallet after retry", target.SwitchWallet(restoredID, coreE2EPassword))
			for _, sub := range entry.Accounts {
				source.SwitchAccount(sub.Index)
				target.SwitchAccount(sub.Index)
				assert.Equal(t, coreDigest(coreRGBSemantic(t, source)), coreDigest(coreRGBSemantic(t, target)),
					"RGB balances/proofs/locks must survive the retry in every wallet/account")
			}
		}
		assert.Equal(t, beforeBroadcasts, chain.broadcastCount(), "recovery retry must not broadcast")
		assert.Equal(t, coreDigest(remoteBefore), coreDigest(coreRemoteStableRGB(t, source, cfg)))
	})
	for _, rebase := range []bool{false, true} {
		name := "RGB11RemoteApplyProviderFailureCanResume"
		if rebase {
			name = "RGB11RebaseProviderFailureCanResume"
		}
		t.Run(name, func(t *testing.T) {
			target, state := load(t)
			_, err := target.RestoreAccountManagementState(*state, source.accountSecret, coreE2EPassword, material.locator, options)
			require.NoError(t, err)
			sourceRoot := source.GetAccountManagementStatus().RootWalletID
			targetRoot := target.GetAccountManagementStatus().RootWalletID
			if rebase {
				require.NoError(t, target.UpdateAccountMetadata(targetRoot, 0, "Local RGB merge", "did:rgb:merge"))
			}
			require.NoError(t, source.UpdateWalletName(sourceRoot, name))
			require.NoError(t, source.SyncAccountManagementState(context.Background()))
			remoteBefore := coreRemoteStableRGB(t, source, cfg)
			failure := errors.New("core E2E: remote RGB provider import interrupted")
			provider := target.managedDataProviders[rgb11AccountManagedProviderID].(AccountManagedActiveDataProvider)
			fault := &coreRGBImportFailureOnce{AccountManagedActiveDataProvider: provider, failure: failure}
			target.managedDataProviders[rgb11AccountManagedProviderID] = fault
			broadcasts := chain.broadcastCount()
			require.ErrorIs(t, target.SyncAccountManagementState(context.Background()), failure)
			marker, err := target.readAccountManagedDataImportMarker()
			require.NoError(t, err)
			require.NotNil(t, marker)
			require.Equal(t, accountManagedImportOriginRemoteApply, marker.Origin)
			if rebase {
				require.NotEmpty(t, marker.ReplayStateEnvelope)
				require.NotEmpty(t, marker.ReplayDataEnvelope)
			}
			require.NoError(t, target.SyncAccountManagementState(context.Background()))
			require.Equal(t, 2, fault.imports)
			require.NoError(t, target.checkAccountManagedDataImport())
			assert.Zero(t, target.GetAccountManagementStatus().PendingChanges)
			coreSynchronizeRestoredAccount(t, target)
			coreSynchronizeRestoredAccount(t, source)
			assert.Equal(t, coreDigest(coreCanonicalCatalog(source)), coreDigest(coreCanonicalCatalog(target)))
			assert.Equal(t, coreDigest(remoteBefore), coreDigest(coreRemoteStableRGB(t, target, cfg)))
			for _, entry := range source.GetWalletCatalog() {
				var restoredID int64
				for _, restored := range target.GetWalletCatalog() {
					if restored.Fingerprint == entry.Fingerprint {
						restoredID = restored.ID
					}
				}
				require.NotZero(t, restoredID)
				require.NoError(t, source.SwitchWallet(entry.ID, coreE2EPassword))
				require.NoError(t, target.SwitchWallet(restoredID, coreE2EPassword))
				for _, sub := range entry.Accounts {
					require.NoError(t, source.SwitchAccount(sub.Index))
					require.NoError(t, target.SwitchAccount(sub.Index))
					assert.Equal(t, coreDigest(coreRGBSemantic(t, source)), coreDigest(coreRGBSemantic(t, target)))
				}
			}
			assert.Equal(t, broadcasts, chain.broadcastCount(), "remote import retry must not broadcast")
			require.NoError(t, source.SwitchWallet(sourceRoot, coreE2EPassword))
			require.NoError(t, source.SwitchAccount(0))
		})
	}

}
