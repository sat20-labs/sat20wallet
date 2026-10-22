package wallet

import (
	"context"
	"errors"
	"testing"
	"time"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Delegate export/validation and successful imports to the actual RGB11
// provider. Only the first import returns a local module failure; the recovery
// bundle still comes from real issuance, native proofs and paid DKVS records.
type coreRGBImportFailureOnce struct {
	AccountManagedDataProvider
	failure error
	imports int
}

func (p *coreRGBImportFailureOnce) Import(catalog AccountManagedDataCatalog, payloads []AccountManagedDataPayload) error {
	p.imports++
	if p.imports == 1 {
		return p.failure
	}
	return p.AccountManagedDataProvider.Import(catalog, payloads)
}

func coreRGBRecoveryFailuresE2E(t *testing.T, source *Manager, cfg coreE2EConfig,
	chain *coreE2EChain, material *coreRecoveryMaterial) {
	t.Helper()
	// Earlier cases may publish through an independent manager of this root.
	// Adopt that authoritative revision before taking the recovery baseline.
	coreSynchronizeRestoredAccount(t, source)
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
	t.Run("RGB11ProviderFailureColdDatabaseRestartResumes", func(t *testing.T) {
		var samples []map[string]any
		coreLogE2EMetrics(t, "rgb_cold_database_recovery", &samples)
		dir := t.TempDir()
		db := indexerdb.NewKVDB(dir)
		target := coreNewE2EManagerWithDB(t, cfg, chain, db, "")
		t.Cleanup(func() { target.Close(); _ = db.Close() })
		state, err := target.LoadAccountManagementStateForRecovery(material.auth.Location,
			material.locator, source.accountSecret, coreE2ESenderMnemonic)
		require.NoError(t, err)
		rgbItems := 0
		for _, item := range state.ManagedData.Items {
			if item.Provider == rgb11AccountManagedProviderID && len(item.Payload) > 0 {
				rgbItems++
			}
		}
		require.GreaterOrEqual(t, rgbItems, 3, "must restore RGB recovery packages for root and child scopes")
		failure := errors.New("core E2E: transient RGB import before physical DB restart")
		target.managedDataMu.Lock()
		provider := target.managedDataProviders[rgb11AccountManagedProviderID]
		fault := &coreRGBImportFailureOnce{AccountManagedDataProvider: provider, failure: failure}
		target.managedDataProviders[rgb11AccountManagedProviderID] = fault
		target.managedDataMu.Unlock()
		require.NotNil(t, provider)
		broadcasts := chain.broadcastCount()
		remoteBefore, _ := coreCommittedRecords(t, source, cfg)
		closureBefore := coreRemoteStableRGB(t, source, cfg)
		started := time.Now()
		_, err = target.RestoreAccountManagementState(*state, source.accountSecret, coreE2EPassword, material.locator, options)
		samples = append(samples, coreE2EDuration("interrupted_import", started))
		require.ErrorIs(t, err, failure)
		require.Equal(t, 1, fault.imports)
		marker, err := target.readAccountManagedDataImportMarker()
		require.NoError(t, err)
		require.NotNil(t, marker)
		restoredRootID := target.GetAccountManagementStatus().RootWalletID
		require.NotZero(t, restoredRootID)
		require.ErrorIs(t, target.UpdateWalletName(restoredRootID, "Must remain blocked"), ErrAccountManagedDataImportIncomplete)
		require.ErrorIs(t, target.SyncAccountManagementState(context.Background()), ErrAccountManagedDataImportIncomplete)
		require.Equal(t, broadcasts, chain.broadcastCount())
		remoteFailed, _ := coreCommittedRecords(t, source, cfg)
		require.Equal(t, remoteBefore, remoteFailed)
		reopen := func(index int) {
			started := time.Now()
			target.Close()
			require.NoError(t, db.Close())
			db = indexerdb.NewKVDB(dir)
			target = coreNewE2EManagerWithDB(t, cfg, chain, db, "")
			sample := coreE2EDuration("physical_reopen_construct", started)
			sample["sample_index"] = index
			samples = append(samples, sample)
		}
		reopen(0)
		marker, err = target.readAccountManagedDataImportMarker()
		require.NoError(t, err)
		require.NotNil(t, marker, "incomplete marker must survive physical reopen")
		started = time.Now()
		_, err = target.UnlockWallet(coreE2EPassword)
		samples = append(samples, coreE2EDuration("unlock_with_import_marker", started))
		require.ErrorIs(t, err, ErrAccountManagedDataImportIncomplete, "an interrupted restore must not open a wallet session")
		markerAfterUnlock, err := target.readAccountManagedDataImportMarker()
		require.NoError(t, err)
		require.Equal(t, marker, markerAfterUnlock, "rejected unlock must retain the durable import marker")
		require.ErrorIs(t, target.UpdateWalletName(restoredRootID, "Must remain blocked after reopen"), ErrAccountManagedDataImportIncomplete)
		require.ErrorIs(t, target.SyncAccountManagementState(context.Background()), ErrAccountManagedDataImportIncomplete)
		started = time.Now()
		_, err = target.RestoreAccountManagementState(*state, source.accountSecret, coreE2EPassword, material.locator, options)
		samples = append(samples, coreE2EDuration("resume_authenticated_import", started))
		require.NoError(t, err)
		marker, err = target.readAccountManagedDataImportMarker()
		require.NoError(t, err)
		require.Nil(t, marker)
		coreSynchronizeRestoredAccount(t, target)
		nonemptyScopes := 0
		verify := func(cold bool) {
			started := time.Now()
			nonemptyScopes = 0
			require.Equal(t, coreCanonicalCatalog(source), coreCanonicalCatalog(target))
			coreAssertMapEqual(t, "local full RGB closure before fresh evidence", coreLocalRGBBundleDigests(t, target), closureBefore)
			selectedWallet, selectedAccount := source.GetCurrentWalletId(), source.GetCurrentAccountId()
			defer func() {
				_ = source.SwitchWallet(selectedWallet, coreE2EPassword)
				_ = source.SwitchAccount(selectedAccount)
			}()
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
					sourceView, targetView := coreRGBSemantic(t, source), coreRGBSemantic(t, target)
					if len(sourceView.Assets) > 0 {
						nonemptyScopes++
					}
					var refreshResult *RGB11RefreshResult
					if cold {
						// Readiness is intentionally RAM-only. With automatic workers
						// stopped, cold ownership must remain unspendable until the
						// public refresh path has obtained fresh Bitcoin evidence.
						require.False(t, target.rgbManager.rgb11ProjectionAvailable())
						require.Empty(t, targetView.Available)
						require.Equal(t, targetView.Assets, targetView.Pending)
						require.Equal(t, sourceView.Assets, targetView.Assets)
						require.Equal(t, sourceView.Outputs, targetView.Outputs)
						require.Equal(t, sourceView.Proofs, targetView.Proofs)
						require.Equal(t, sourceView.Tasks, targetView.Tasks)
						require.Equal(t, sourceView.Invoices, targetView.Invoices)
						require.Equal(t, sourceView.Locked, targetView.Locked)
						refreshStarted := time.Now()
						var refreshErr error
						refreshResult, refreshErr = target.RefreshRGB11State(context.Background())
						sample := coreE2EDuration("fresh_chain_reconciliation", refreshStarted)
						sample["account_index"] = sub.Index
						samples = append(samples, sample)
						require.NoError(t, refreshErr)
						targetView = coreRGBSemantic(t, target)
						// Compare derived runtime status after the same public chain pass.
						// Run the source control only after the cold target path completed.
						_, sourceRefreshErr := source.RefreshRGB11State(context.Background())
						require.NoError(t, sourceRefreshErr)
						sourceView = coreRGBSemantic(t, source)
					}
					if coreDigest(sourceView) != coreDigest(targetView) {
						t.Logf("core-e2e: cold recovery mismatch cold=%t account=%d source_wallet=%d target_wallet=%d initialized=%t/%t sync=%q/%q consistency=%q/%q projection_available=%t/%t",
							cold, sub.Index, entry.ID, restoredID,
							sourceView.Initialized, targetView.Initialized, sourceView.SyncStatus, targetView.SyncStatus,
							sourceView.ConsistencyStatus, targetView.ConsistencyStatus,
							source.rgbManager.rgb11ProjectionAvailable(), target.rgbManager.rgb11ProjectionAvailable())
						for _, field := range []struct {
							name           string
							source, target map[string]string
						}{
							{"assets", sourceView.Assets, targetView.Assets},
							{"available", sourceView.Available, targetView.Available},
							{"pending", sourceView.Pending, targetView.Pending},
							{"outputs", sourceView.Outputs, targetView.Outputs},
							{"proofs", sourceView.Proofs, targetView.Proofs},
							{"tasks", sourceView.Tasks, targetView.Tasks},
							{"invoices", sourceView.Invoices, targetView.Invoices},
							{"locks", sourceView.Locked, targetView.Locked},
						} {
							missing, extra, changed := 0, 0, 0
							for key, value := range field.source {
								other, exists := field.target[key]
								if !exists {
									missing++
								} else if other != value {
									changed++
								}
							}
							for key := range field.target {
								if _, exists := field.source[key]; !exists {
									extra++
								}
							}
							t.Logf("core-e2e: cold recovery field=%s equal=%t count=%d/%d missing=%d extra=%d changed=%d",
								field.name, coreDigest(field.source) == coreDigest(field.target),
								len(field.source), len(field.target), missing, extra, changed)
						}
						if refreshResult != nil {
							t.Logf("core-e2e: cold recovery refresh settled=%d pending=%d reorged=%d conflicted=%d unresolved=%d inconsistent=%d",
								refreshResult.Settled, refreshResult.Pending, refreshResult.Reorged,
								refreshResult.Conflicted, refreshResult.Unresolved, len(refreshResult.Inconsistent))
						}
					}
					require.Equal(t, coreDigest(sourceView), coreDigest(targetView), "every scope's proofs, ownership, readiness and locks must survive restart and fresh verification")
				}
			}
			require.GreaterOrEqual(t, nonemptyScopes, 3, "fixture must actually own RGB assets in at least three scopes")
			require.Equal(t, closureBefore, coreRemoteStableRGB(t, target, cfg))
			coreAssertMapEqual(t, "reopened local full RGB recovery closure", coreLocalRGBBundleDigests(t, target), closureBefore)
			remoteAfter, _ := coreCommittedRecords(t, source, cfg)
			require.Equal(t, remoteBefore, remoteAfter, "restore must not republish recovery records")
			require.Equal(t, broadcasts, chain.broadcastCount())
			samples = append(samples, coreE2EDuration("complete_closure_verification", started))
		}
		verify(false)
		reopen(1)
		marker, err = target.readAccountManagedDataImportMarker()
		require.NoError(t, err)
		require.Nil(t, marker, "marker cleanup must be durable")
		started = time.Now()
		_, err = target.UnlockWallet(coreE2EPassword)
		samples = append(samples, coreE2EDuration("clean_cold_unlock", started))
		require.NoError(t, err)
		verify(true)
		samples = append(samples, map[string]any{"phase": "scenario_counts", "counts": map[string]int{"broadcast": chain.broadcastCount() - broadcasts}, "workload": map[string]int{"wallets": len(source.GetWalletCatalog()), "rgb_scopes": rgbItems, "nonempty_rgb_scopes": nonemptyScopes, "physical_reopens": 2}})
	})

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
		if provider == nil {
			target.managedDataMu.Unlock()
			t.Fatal("core-e2e: actual durable RGB11 provider is not registered")
		}
		fault := &coreRGBImportFailureOnce{AccountManagedDataProvider: provider, failure: failure}
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
			target.managedDataMu.RLock()
			provider := target.managedDataProviders[rgb11AccountManagedProviderID]
			target.managedDataMu.RUnlock()
			require.NotNil(t, provider, "actual durable RGB11 provider must stay registered")
			fault := &coreRGBImportFailureOnce{AccountManagedDataProvider: provider, failure: failure}
			target.managedDataMu.Lock()
			target.managedDataProviders[rgb11AccountManagedProviderID] = fault
			target.managedDataMu.Unlock()
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
