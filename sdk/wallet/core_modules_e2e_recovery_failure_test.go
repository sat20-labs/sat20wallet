package wallet

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Delegate export/validation and successful imports to the actual RGB11
// provider. Only the first import returns a local module failure; the recovery
// bundle still comes from real issuance, native proofs and paid DKVS records.
type coreRGBImportFailureOnce struct {
	AccountManagedDataProvider
	failure     error
	imports     int
	failedScope string
}

func (p *coreRGBImportFailureOnce) Import(catalog AccountManagedDataCatalog, payloads []AccountManagedDataPayload) error {
	p.imports++
	if p.imports == 1 {
		p.failedScope = payloads[0].Scope
		return p.failure
	}
	return p.AccountManagedDataProvider.Import(catalog, payloads)
}

func coreRGBPartialRecovery(t *testing.T, target *Manager, accountID, scope string) *accountLocalKV {
	t.Helper()
	require.NotEmpty(t, scope)
	target.mutex.RLock()
	value, err := target.readAccountLocal(accountID, rgb11AccountManagedProviderID, scope)
	unlocked := target.wallet != nil
	target.mutex.RUnlock()
	require.NoError(t, err)
	require.NotNil(t, value)
	require.True(t, value.Restoring, "failed scope must remain retryable, never ready")
	require.NotEmpty(t, value.LastError, "partial restore must expose the module failure")
	require.Error(t, target.CheckAccountWrite(rgb11AccountManagedProviderID, scope), "failed RGB scope must reject new business")
	if unlocked {
		found := false
		for _, module := range target.GetAccountManagementStatus().Modules {
			if module.Provider == rgb11AccountManagedProviderID && module.Scope == scope {
				found = true
				require.False(t, module.WriteAllowed)
				require.Contains(t, []string{"restoring", "error"}, module.BackupState)
				require.Equal(t, value.LastError, module.LastError)
			}
		}
		require.True(t, found, "successful wallet restore must still report its partial RGB recovery")
	}
	return value
}

func coreRGBCompleteRecovery(t *testing.T, target *Manager, accountID string) {
	t.Helper()
	target.mutex.RLock()
	values, err := target.listAccountLocal(accountID)
	target.mutex.RUnlock()
	require.NoError(t, err)
	count := 0
	for _, value := range values {
		if value.Provider != rgb11AccountManagedProviderID || value.Deleted {
			continue
		}
		count++
		require.False(t, value.Restoring, "retry did not finish the RGB module import")
		require.False(t, value.Conflict)
		require.Empty(t, value.LastError)
		require.NotEmpty(t, value.Value, "successful retry must retain the complete recovery bytes")
	}
	require.GreaterOrEqual(t, count, 3)
}

func coreAssertRGBHealth(t *testing.T, label string, view coreRGBView, refresh *RGB11RefreshResult) {
	t.Helper()
	require.NotNil(t, refresh, "%s refresh returned no result", label)
	require.Empty(t, refresh.Inconsistent, "%s refresh found inconsistent evidence", label)
	require.Zero(t, refresh.Unresolved, "%s refresh left RGB business state unresolved", label)
	require.True(t, view.Initialized, "%s RGB view is not initialized", label)
	require.Equal(t, "idle", view.SyncStatus, "%s RGB refresh did not settle", label)
	require.Equal(t, "ok", view.ConsistencyStatus,
		"%s consistency must be healthy after a resolved chain refresh", label)
}

// Failure-only observations: do not retry synchronization, validate a different
// candidate, or expose recovery bytes. Journal matches are local metadata, not
// a claim that a complete RGB consumer graph has been validated.
func coreLogRGBRefreshFailure(t *testing.T, label string, manager *Manager,
	chain *coreE2EChain, result *RGB11RefreshResult, refreshErr error) {
	t.Helper()
	if refreshErr == nil {
		return
	}
	scope, scopeErr := manager.rgbManager.rgb11AccountManagedScopeID()
	t.Logf("core-e2e: RGB refresh failure: label=%q actor=%d scope_tag=%s account=%d scope_error=%t error_type=%T result_present=%t",
		label, manager.debugID, coreCASTag(scope), manager.GetCurrentAccountId(), scopeErr != nil, refreshErr, result != nil)
	if result != nil {
		t.Logf("core-e2e: RGB refresh failure result: inconsistent=%d unresolved=%d", len(result.Inconsistent), result.Unresolved)
	}
	for _, module := range manager.GetAccountManagementStatus().Modules {
		if module.Provider == rgb11AccountManagedProviderID && module.Scope == scope {
			t.Logf("core-e2e: RGB refresh failure backup: state=%q writable=%t has_error=%t", module.BackupState, module.WriteAllowed, module.LastError != "")
		}
	}
	release := manager.rgbManager.beginRGB11ReservationRead()
	records, readErr := manager.rgbManager.projectionStore.ReadLocalRecords()
	release()
	proofs, proofErr := rgb11wallet.ProofsFromProjectionSnapshot(records)
	transfers, transferErr := rgb11wallet.TransfersFromProjectionSnapshot(records)
	t.Logf("core-e2e: RGB refresh failure local: read_error=%t proof_error=%t transfer_error=%t proofs=%d transfers=%d",
		readErr != nil, proofErr != nil, transferErr != nil, len(proofs), len(transfers))
	if readErr != nil || proofErr != nil || transferErr != nil {
		return
	}
	logged := 0
	for _, proof := range proofs {
		if proof == nil || proof.Status != "inconsistent" && (result == nil || !containsString(result.Inconsistent, proof.OutPoint)) {
			continue
		}
		chain.mu.Lock()
		spender := chain.spent[proof.OutPoint]
		status, statusKnown := chain.status[spender]
		chain.mu.Unlock()
		inputJournals, witnessJournals := 0, 0
		for _, transfer := range transfers {
			if transfer != nil && containsString(transfer.InputOutPoints, proof.OutPoint) {
				inputJournals++
				if spender != "" && transfer.WitnessTxID == spender {
					witnessJournals++
				}
			}
		}
		t.Logf("core-e2e: RGB refresh failure carrier: point_tag=%s asset_tag=%s status=%q spent=%t spender_tag=%s status_known=%t confirmed=%t mempool=%t input_journals=%d spender_journals=%d",
			coreCASTag(proof.OutPoint), coreCASTag(proof.AssetName.String()), proof.Status, spender != "", coreCASTag(spender),
			statusKnown, status.Confirmed, status.InMempool, inputJournals, witnessJournals)
		logged++
		if logged == 16 {
			t.Log("core-e2e: RGB refresh failure carrier diagnostics limited to 16 records")
			break
		}
	}
}

// Log only local RGB transfer metadata when RefreshRGB11State reports unresolved
// work. The refresh API exposes a count rather than the responsible transfer;
// projectionStore.ListTransfers is the existing local-state source and performs
// no additional chain/network queries. Values that can carry secrets are omitted.
func coreLogRGBUnresolved(t *testing.T, label string, manager *Manager,
	result *RGB11RefreshResult) {
	t.Helper()
	if result == nil || result.Unresolved == 0 {
		return
	}
	if manager == nil || manager.rgbManager == nil || manager.rgbManager.projectionStore == nil {
		t.Logf("core-e2e: unresolved RGB local transfer view unavailable: label=%q unresolved=%d", label, result.Unresolved)
		return
	}
	transfers, err := manager.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		t.Logf("core-e2e: unresolved RGB local transfer read failed: label=%q manager=%d unresolved=%d error_type=%T",
			label, manager.debugID, result.Unresolved, err)
		return
	}
	t.Logf("core-e2e: unresolved RGB local transfer candidates: label=%q manager=%d unresolved=%d inconsistent_count=%d transfer_count=%d",
		label, manager.debugID, result.Unresolved, len(result.Inconsistent), len(transfers))
	for _, transfer := range transfers {
		if transfer == nil {
			continue
		}
		t.Logf("core-e2e: unresolved RGB local transfer candidate: label=%q manager=%d transfer_tag=%s request_tag=%s direction=%q status=%q ack=%q address_mode=%t transport=%q witness_present=%t input_count=%d output_count=%d",
			label, manager.debugID, coreCASTag(transfer.TransferID), coreCASTag(transfer.ReceiveRequestID),
			transfer.Direction, transfer.Status, transfer.AckStatus, transfer.AddressMode, transfer.TransportMode,
			transfer.WitnessTxID != "", len(transfer.InputOutPoints), len(transfer.OutputOutPoints))
	}
	for _, point := range result.Inconsistent {
		t.Logf("core-e2e: unresolved RGB inconsistent point tag: label=%q manager=%d point_tag=%s",
			label, manager.debugID, coreCASTag(point))
	}
}

func coreLogRGBViewDiff(t *testing.T, source, target coreRGBView) {
	t.Helper()
	t.Logf("core-e2e: RGB view status initialized=%t/%t sync=%q/%q consistency=%q/%q",
		source.Initialized, target.Initialized, source.SyncStatus, target.SyncStatus,
		source.ConsistencyStatus, target.ConsistencyStatus)
	for _, field := range []struct {
		name           string
		source, target map[string]string
	}{
		{"assets", source.Assets, target.Assets},
		{"available", source.Available, target.Available},
		{"pending", source.Pending, target.Pending},
		{"outputs", source.Outputs, target.Outputs},
		{"proofs", source.Proofs, target.Proofs},
		{"tasks", source.Tasks, target.Tasks},
		{"invoices", source.Invoices, target.Invoices},
		{"locks", source.Locked, target.Locked},
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
		t.Logf("core-e2e: RGB view field=%s equal=%t count=%d/%d missing=%d extra=%d changed=%d",
			field.name, coreDigest(field.source) == coreDigest(field.target),
			len(field.source), len(field.target), missing, extra, changed)
	}
}

// Ensure the isolated recovery-failure group can run without depending on the
// wallet/catalog and issuance groups that are normally visited earlier in the
// parent suite. It only adds real catalog entries or RGB ownership when the
// exact root/child scope is missing, so the full-suite path reuses its existing
// assets and does not reinitialize or replace the account.
func corePrepRGBFailureScopes(t *testing.T, source *Manager,
	cfg coreE2EConfig, chain *coreE2EChain) {
	t.Helper()
	originalWallet, originalAccount := source.GetCurrentWalletId(), source.GetCurrentAccountId()
	defer func() {
		coreRequire(t, "restore recovery fixture wallet selection", source.SwitchWallet(originalWallet, coreE2EPassword))
		coreRequire(t, "restore recovery fixture account selection", source.SwitchAccount(originalAccount))
	}()

	rootID := source.GetAccountManagementStatus().RootWalletID
	coreRequire(t, "select recovery fixture root wallet", source.SwitchWallet(rootID, coreE2EPassword))
	coreRequire(t, "select recovery fixture root account", source.SwitchAccount(0))

	childWallet := NewInternalWalletWithMnemonic(coreE2EChildMnemonic, "", GetChainParam())
	coreAssert(t, childWallet != nil, "construct the existing child wallet identity")
	childFingerprint := walletFingerprint(childWallet)
	var childID int64
	var childEntry *WalletCatalogEntry
	for _, entry := range source.GetWalletCatalog() {
		if entry.Fingerprint == childFingerprint {
			childID = entry.ID
			entryCopy := entry
			childEntry = &entryCopy
			break
		}
	}
	catalogBefore := coreDigest(coreCanonicalCatalog(source))
	if childID == 0 {
		var err error
		childID, err = source.ImportWallet(coreE2EChildMnemonic, coreE2EPassword)
		coreRequire(t, "import existing RGB recovery child wallet", err)
		for _, entry := range source.GetWalletCatalog() {
			if entry.ID == childID {
				entryCopy := entry
				childEntry = &entryCopy
				break
			}
		}
	}
	coreAssert(t, childID != 0, "child wallet identity is missing")
	coreAssert(t, childEntry != nil, "child wallet catalog entry is missing")
	hasAccount2 := false
	for _, entryAccount := range childEntry.Accounts {
		if entryAccount.Index == 2 {
			hasAccount2 = true
			break
		}
	}
	if !hasAccount2 {
		coreRequire(t, "create missing child recovery accounts", source.EnsureAccount(childID, 2, "Child vault", "did:child:2"))
	}
	if coreDigest(coreCanonicalCatalog(source)) != catalogBefore {
		coreRequire(t, "publish recovery fixture wallet catalog", source.SyncAccountManagementState(context.Background()))
		coreWaitPrivateBackup(t, source)
	}

	type scopeFixture struct {
		walletID       int64
		account        uint32
		schema, ticker string
		name           string
		amounts        []uint64
		inflation      []uint64
		funding        int
		projected      int
	}
	scopes := []scopeFixture{
		{walletID: rootID, account: 0, schema: "NIA", ticker: "RFR0", name: "E2E recovery root asset", amounts: []uint64{321}, projected: 1},
		{walletID: childID, account: 1, schema: "UDA", ticker: "RFU1", name: "E2E recovery child UDA", funding: 5, projected: 1},
		{walletID: childID, account: 2, schema: "IFA", ticker: "RFI2", name: "E2E recovery child IFA", amounts: []uint64{1000}, inflation: []uint64{9000}, funding: 5, projected: 2},
	}
	for _, scope := range scopes {
		coreRequire(t, "select recovery fixture wallet", source.SwitchWallet(scope.walletID, coreE2EPassword))
		coreRequire(t, "select recovery fixture account", source.SwitchAccount(scope.account))
		state, err := source.GetRGB11State()
		coreRequire(t, "inspect real recovery fixture RGB ownership", err)
		require.NotNil(t, state)
		if len(state.Assets) != 0 {
			continue
		}

		wallet := source.GetWallet()
		coreAssert(t, wallet != nil, "selected recovery fixture wallet is unavailable")
		if scope.funding != 0 {
			chain.fund(t, wallet.GetAddress(), scope.funding)
		}
		issued, err := source.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
			Schema: scope.schema, Ticker: scope.ticker, Name: scope.name,
			Amounts: scope.amounts, InflationAmounts: scope.inflation,
		})
		coreRequire(t, "issue real recovery fixture RGB asset", err)
		require.NotNil(t, issued)
		coreAssert(t, issued.Projected == scope.projected, "recovery fixture issuance did not project its actual allocation")
		coreWaitPrivateBackup(t, source)
	}
	for _, scope := range scopes {
		coreRequire(t, "verify recovery fixture wallet selection", source.SwitchWallet(scope.walletID, coreE2EPassword))
		coreRequire(t, "verify recovery fixture account selection", source.SwitchAccount(scope.account))
		state, err := source.GetRGB11State()
		coreRequire(t, "read prepared recovery fixture RGB scope", err)
		require.NotNil(t, state)
		require.NotEmpty(t, state.Assets, "root/child recovery scope must own a real issued RGB asset")
	}
	coreWaitPrivateBackup(t, source)
	coreVerifyPaidRecords(t, source, cfg)
}

func coreRGBRecoveryFailuresE2E(t *testing.T, source *Manager, cfg coreE2EConfig,
	chain *coreE2EChain, material *coreRecoveryMaterial) {
	t.Helper()
	// Adopt earlier independent-manager publications before this fixture adds
	// any real catalog or RGB ownership changes.
	coreSynchronizeRestoredAccount(t, source)
	corePrepRGBFailureScopes(t, source, cfg, chain)
	load := func(t *testing.T) (*Manager, *RecoveredAccountManagementState) {
		t.Helper()
		target := coreNewE2EManager(t, cfg, chain, "")
		state, err := target.LoadAccountManagementStateForRecovery(material.auth.Location,
			material.locator, source.accountSecret, coreE2ESenderMnemonic)
		coreRequire(t, "read authenticated RGB recovery bundle from paid DKVS", err)
		rgbItems := 0
		for _, item := range state.Copies {
			if item.Entry.Provider == rgb11AccountManagedProviderID {
				payload, err := openAccountPayload(source.accountSecret, material.locator.AccountID, source.cfg.Chain, item.Entry, item.Value)
				require.NoError(t, err)
				require.NotEmpty(t, payload)
				zeroBytes(payload)
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
		sourceWallet, sourceAccount := source.GetCurrentWalletId(), source.GetCurrentAccountId()
		// Register first so the cold target closes before the original device
		// receives again. This case tests device recovery, not two live writers.
		t.Cleanup(func() {
			coreRequire(t, "restore recovery source wallet", source.SwitchWallet(sourceWallet, coreE2EPassword))
			coreRequire(t, "restore recovery source account", source.SwitchAccount(sourceAccount))
			coreRequire(t, "resume source after cold target closed", source.StartDKVSSync())
			coreSynchronizeRestoredAccount(t, source)
			coreWaitPrivateBackup(t, source)
		})
		preparedAt := time.Now()
		prepareBroadcasts := chain.broadcastCount()
		coreSuspendRGBDevice(t, source)
		for _, entry := range source.GetWalletCatalog() {
			coreRequire(t, "select source recovery baseline wallet", source.SwitchWallet(entry.ID, coreE2EPassword))
			for _, sub := range entry.Accounts {
				coreRequire(t, "select source recovery baseline account", source.SwitchAccount(sub.Index))
				refresh, err := source.RefreshRGB11State(context.Background())
				coreLogRGBRefreshFailure(t, "source recovery baseline", source, chain, refresh, err)
				coreRequire(t, "refresh source recovery baseline", err)
				coreAssertRGBHealth(t, "source recovery baseline", coreRGBSemantic(t, source), refresh)
			}
		}
		// Publish these fresh per-scope facts before loading the fixed remote
		// closure. The source stays offline while the cold target takes over.
		coreSuspendRGBDevice(t, source)
		coreRGBCompleteRecovery(t, source, material.locator.AccountID)
		require.Equal(t, prepareBroadcasts, chain.broadcastCount(), "source handoff must not broadcast")
		samples = append(samples, coreE2EDuration("source_handoff_baseline", preparedAt))
		dir := t.TempDir()
		db := indexerdb.NewKVDB(dir)
		target := coreNewE2EManagerWithDB(t, cfg, chain, db, "")
		t.Cleanup(func() { target.Close(); _ = db.Close() })
		state, err := target.LoadAccountManagementStateForRecovery(material.auth.Location,
			material.locator, source.accountSecret, coreE2ESenderMnemonic)
		require.NoError(t, err)
		rgbItems := 0
		for _, item := range state.Copies {
			if item.Entry.Provider == rgb11AccountManagedProviderID {
				payload, err := openAccountPayload(source.accountSecret, material.locator.AccountID, source.cfg.Chain, item.Entry, item.Value)
				require.NoError(t, err)
				require.NotEmpty(t, payload)
				zeroBytes(payload)
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
		require.NoError(t, err, "module failure must not discard independently restored wallets")
		require.Equal(t, rgbItems, fault.imports, "other RGB scopes must still import")
		failed := coreRGBPartialRecovery(t, target, material.locator.AccountID, fault.failedScope)
		require.Contains(t, failed.LastError, failure.Error())
		restoredRootID := target.GetAccountManagementStatus().RootWalletID
		require.NotZero(t, restoredRootID)
		require.Equal(t, coreCanonicalCatalog(source), coreCanonicalCatalog(target))
		require.NoError(t, target.CheckAccountRestore("wallets", AccountManagedDataGlobalScope))
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
		reopened := coreRGBPartialRecovery(t, target, material.locator.AccountID, fault.failedScope)
		require.Equal(t, failed, reopened, "failed module and retry bytes must survive physical reopen")
		started = time.Now()
		_, err = target.UnlockWallet(coreE2EPassword)
		samples = append(samples, coreE2EDuration("unlock_with_partial_module", started))
		require.NoError(t, err, "one failed RGB scope must not lock independent wallets")
		require.Equal(t, failed, coreRGBPartialRecovery(t, target, material.locator.AccountID, fault.failedScope))
		require.True(t, bytes.Equal(source.accountSecret, target.accountSecret), "partial recovery must preserve account identity")
		require.NoError(t, target.CheckAccountRestore("wallets", AccountManagedDataGlobalScope))
		started = time.Now()
		_, err = target.RestoreAccountManagementState(*state, source.accountSecret, coreE2EPassword, material.locator, options)
		samples = append(samples, coreE2EDuration("resume_authenticated_import", started))
		require.NoError(t, err)
		coreRGBCompleteRecovery(t, target, material.locator.AccountID)
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
					}
					// Compare runtime health only after both managers reconcile the same
					// selected scope against current chain evidence. This avoids comparing
					// a previously derived source status with a newly restored target;
					// refresh errors and unresolved results remain explicit assertions.
					refreshStarted := time.Now()
					targetRefresh, targetRefreshErr := target.RefreshRGB11State(context.Background())
					require.NoError(t, targetRefreshErr)
					require.NotNil(t, targetRefresh)
					sourceRefresh, sourceRefreshErr := source.RefreshRGB11State(context.Background())
					require.NoError(t, sourceRefreshErr)
					require.NotNil(t, sourceRefresh)
					sample := coreE2EDuration("fresh_chain_reconciliation", refreshStarted)
					sample["account_index"] = sub.Index
					sample["source_unresolved"] = sourceRefresh.Unresolved
					sample["target_unresolved"] = targetRefresh.Unresolved
					samples = append(samples, sample)
					sourceView, targetView = coreRGBSemantic(t, source), coreRGBSemantic(t, target)
					coreLogRGBUnresolved(t, "source", source, sourceRefresh)
					coreLogRGBUnresolved(t, "recovered target", target, targetRefresh)
					coreAssertRGBHealth(t, "source", sourceView, sourceRefresh)
					coreAssertRGBHealth(t, "recovered target", targetView, targetRefresh)
					require.Equal(t, sourceRefresh.Unresolved, targetRefresh.Unresolved,
						"fresh source and recovered target must agree on unresolved chain evidence")
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
						t.Logf("core-e2e: cold recovery refresh settled=%d pending=%d reorged=%d conflicted=%d unresolved=%d inconsistent=%d",
							targetRefresh.Settled, targetRefresh.Pending, targetRefresh.Reorged,
							targetRefresh.Conflicted, targetRefresh.Unresolved, len(targetRefresh.Inconsistent))
						t.Logf("core-e2e: cold recovery source refresh settled=%d pending=%d reorged=%d conflicted=%d unresolved=%d inconsistent=%d",
							sourceRefresh.Settled, sourceRefresh.Pending, sourceRefresh.Reorged,
							sourceRefresh.Conflicted, sourceRefresh.Unresolved, len(sourceRefresh.Inconsistent))
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
		coreRGBCompleteRecovery(t, target, material.locator.AccountID)
		started = time.Now()
		_, err = target.UnlockWallet(coreE2EPassword)
		samples = append(samples, coreE2EDuration("clean_cold_unlock", started))
		require.NoError(t, err)
		verify(true)
		samples = append(samples, map[string]any{"phase": "scenario_counts", "counts": map[string]int{"broadcast": chain.broadcastCount() - broadcasts}, "workload": map[string]int{"wallets": len(source.GetWalletCatalog()), "rgb_scopes": rgbItems, "nonempty_rgb_scopes": nonemptyScopes, "physical_reopens": 2}})
	})

	for _, corrupt := range []bool{false, true} {
		name := "MissingRGB11ModuleIsolatedAndRetryable"
		if corrupt {
			name = "CorruptRGB11ModuleIsolatedAndRetryable"
		}
		t.Run(name, func(t *testing.T) {
			target, state := load(t)
			beforeBroadcasts := chain.broadcastCount()
			remoteBefore, _ := coreCommittedRecords(t, source, cfg)
			closureBefore := coreRemoteStableRGB(t, source, cfg)
			failedScope := ""
			for i := range state.Copies {
				item := &state.Copies[i]
				if item.Entry.Provider != rgb11AccountManagedProviderID {
					continue
				}
				failedScope = item.Entry.Scope
				// A missing/unusable module copy has no verified record. Keep
				// the authenticated directory and all independent copies intact.
				item.Record = nil
				if corrupt {
					item.Value = append([]byte(nil), item.Value...)
					item.Value[len(item.Value)-1] ^= 1
				} else {
					item.Value = nil
				}
				break
			}
			require.NotEmpty(t, failedScope)
			_, err := target.RestoreAccountManagementState(*state, source.accountSecret, coreE2EPassword, material.locator, options)
			require.NoError(t, err, "independent authenticated wallets must remain recoverable")
			failed := coreRGBPartialRecovery(t, target, material.locator.AccountID, failedScope)
			require.Equal(t, coreCanonicalCatalog(source), coreCanonicalCatalog(target))
			require.True(t, bytes.Equal(source.accountSecret, target.accountSecret), "partial recovery must preserve account identity")
			require.NoError(t, target.CheckAccountRestore("wallets", AccountManagedDataGlobalScope))
			remoteFailed, _ := coreCommittedRecords(t, source, cfg)
			require.Equal(t, remoteBefore, remoteFailed, "partial recovery must not overwrite the remote module")
			assert.Equal(t, beforeBroadcasts, chain.broadcastCount())

			// Retry against the original authentic paid records, without replacing
			// the database or account identity. The failed bytes remain available
			// until that real recovery succeeds.
			require.Equal(t, failed, coreRGBPartialRecovery(t, target, material.locator.AccountID, failedScope))
			repaired, err := target.LoadAccountManagementStateForRecovery(material.auth.Location,
				material.locator, source.accountSecret, coreE2ESenderMnemonic)
			require.NoError(t, err)
			_, err = target.RestoreAccountManagementState(*repaired, source.accountSecret, coreE2EPassword, material.locator, options)
			require.NoError(t, err)
			coreRGBCompleteRecovery(t, target, material.locator.AccountID)
			require.Equal(t, coreCanonicalCatalog(source), coreCanonicalCatalog(target))
			coreAssertMapEqual(t, "repaired complete RGB closure", coreLocalRGBBundleDigests(t, target), closureBefore)
			remoteAfter, _ := coreCommittedRecords(t, source, cfg)
			require.Equal(t, remoteBefore, remoteAfter)
			assert.Equal(t, beforeBroadcasts, chain.broadcastCount(), "module retry must not replay transactions")
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
		require.NoError(t, err)
		require.NotEmpty(t, fault.failedScope, "must exercise the actual provider import boundary")
		firstImports := fault.imports
		failed := coreRGBPartialRecovery(t, target, material.locator.AccountID, fault.failedScope)
		require.Contains(t, failed.LastError, failure.Error())
		beforeCatalog := target.GetWalletCatalog()
		require.Equal(t, coreCanonicalCatalog(source), coreCanonicalCatalog(target))
		require.True(t, bytes.Equal(source.accountSecret, target.accountSecret), "partial recovery must preserve account identity")
		require.NoError(t, target.CheckAccountRestore("wallets", AccountManagedDataGlobalScope))
		_, err = target.UnlockWallet(coreE2EPassword)
		require.NoError(t, err)
		assert.Equal(t, beforeCatalog, target.GetWalletCatalog())
		require.Equal(t, failed, coreRGBPartialRecovery(t, target, material.locator.AccountID, fault.failedScope))
		assert.Equal(t, coreDigest(remoteBefore), coreDigest(coreRemoteStableRGB(t, source, cfg)))
		assert.Equal(t, beforeBroadcasts, chain.broadcastCount())

		// No new device, DB clearing or replacement recovery material. The
		// transient fault is gone; retry must restore the complete original data.
		_, err = target.RestoreAccountManagementState(*state, source.accountSecret, coreE2EPassword, material.locator, options)
		coreRequire(t, "retry RGB import on the same database after transient failure", err)
		require.Equal(t, firstImports+1, fault.imports, "retry only the failed module")
		coreRGBCompleteRecovery(t, target, material.locator.AccountID)
		coreAssertMapEqual(t, "same-database retry local full RGB recovery closure",
			coreLocalRGBBundleDigests(t, target), remoteBefore)
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
				coreRequire(t, "select source account after retry", source.SwitchAccount(sub.Index))
				coreRequire(t, "select recovered account after retry", target.SwitchAccount(sub.Index))
				targetRefresh, targetRefreshErr := target.RefreshRGB11State(context.Background())
				coreLogRGBRefreshFailure(t, "same-database retry target", target, chain, targetRefresh, targetRefreshErr)
				require.NoError(t, targetRefreshErr)
				sourceRefresh, sourceRefreshErr := source.RefreshRGB11State(context.Background())
				coreLogRGBRefreshFailure(t, "same-database retry source", source, chain, sourceRefresh, sourceRefreshErr)
				require.NoError(t, sourceRefreshErr)
				sourceView, targetView := coreRGBSemantic(t, source), coreRGBSemantic(t, target)
				coreLogRGBUnresolved(t, "same-database retry source", source, sourceRefresh)
				coreLogRGBUnresolved(t, "same-database retry target", target, targetRefresh)
				coreAssertRGBHealth(t, "same-database retry source", sourceView, sourceRefresh)
				coreAssertRGBHealth(t, "same-database retry target", targetView, targetRefresh)
				assert.Equal(t, sourceRefresh.Unresolved, targetRefresh.Unresolved,
					"source and recovered target must agree on unresolved chain evidence")
				if coreDigest(sourceView) != coreDigest(targetView) {
					coreLogRGBViewDiff(t, sourceView, targetView)
				}
				assert.Equal(t, coreDigest(sourceView), coreDigest(targetView),
					"RGB balances/proofs/locks must survive the retry in every wallet/account")
			}
		}
		assert.Equal(t, beforeBroadcasts, chain.broadcastCount(), "recovery retry must not broadcast")
		assert.Equal(t, coreDigest(remoteBefore), coreDigest(coreRemoteStableRGB(t, source, cfg)))
	})
	for _, rebase := range []bool{false, true} {
		name := "WalletRemoteApplyDoesNotReimportUnchangedRGB"
		if rebase {
			name = "WalletRebaseDoesNotReimportUnchangedRGB"
		}
		t.Run(name, func(t *testing.T) {
			target, state := load(t)
			_, err := target.RestoreAccountManagementState(*state, source.accountSecret, coreE2EPassword, material.locator, options)
			require.NoError(t, err)
			coreSynchronizeRestoredAccount(t, target)
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
			require.NoError(t, target.SyncAccountManagementState(context.Background()))
			require.Zero(t, fault.imports, "wallet-only apply/rebase must not import unchanged RGB modules")
			coreRGBCompleteRecovery(t, target, material.locator.AccountID)
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
					sourceView, targetView := coreRGBSemantic(t, source), coreRGBSemantic(t, target)
					if coreDigest(sourceView) != coreDigest(targetView) {
						coreLogRGBViewDiff(t, sourceView, targetView)
					}
					assert.Equal(t, coreDigest(sourceView), coreDigest(targetView))
				}
			}
			assert.Equal(t, broadcasts, chain.broadcastCount(), "remote import retry must not broadcast")
			require.NoError(t, source.SwitchWallet(sourceRoot, coreE2EPassword))
			require.NoError(t, source.SwitchAccount(0))
		})
	}

}
