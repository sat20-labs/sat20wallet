package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/sat20-labs/rgb11/invoicing"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/stretchr/testify/require"
)

// A page reload constructs a fresh Manager against the same persisted wallet.
func TestAccountPWAColdStartupReadFailurePreservesSelection(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprintf("decode=%t", corrupt), func(t *testing.T) {
			original := newAccountManagementAutoTestManager(t)
			_, _, err := original.CreateWallet("password")
			require.NoError(t, err)
			before, err := original.db.Read([]byte(DB_KEY_STATUS))
			require.NoError(t, err)
			fault := &accountColdUnlockStatusDB{KVDB: original.db, status: before}
			if corrupt {
				fault.status = []byte("invalid status encoding")
			} else {
				fault.err = errors.New("PWA startup status read failed")
			}
			reopened := newAccountManagementAutoTestManager(t)
			reopened.db = fault
			require.Error(t, reopened.initDB())
			after, err := original.db.Read([]byte(DB_KEY_STATUS))
			require.NoError(t, err)
			require.True(t, bytes.Equal(before, after), "failed startup overwrote the selected wallet")
			reopened.db = original.db
			require.NoError(t, reopened.initDB())
			id, err := reopened.UnlockWallet("password")
			require.NoError(t, err)
			require.Equal(t, original.GetCurrentWalletId(), id)
		})
	}
}

func TestPWAImportScopeIsolation(t *testing.T) {
	old := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = old })
	for _, origin := range []string{"restore", "remote-apply"} {
		t.Run(origin, func(t *testing.T) {
			remote := newRGB11MemoryDKVSHTTP()
			source, sourceProvider, _ := newImportSource(t, remote)
			target, provider, _ := newImportGroup(t, remote)
			want := []byte("A")
			if origin == "remote-apply" {
				require.NoError(t, restoreImportGroup(t, target, source))
				want = []byte("new independent-device data")
				stageManagedImportTestPayload(t, source, sourceProvider, want)
				require.NoError(t, source.SyncAccountManagementState(nil))
			}
			beforeRemote := snapshotRGB11MemoryRecords(remote)
			interrupted := errors.New("PWA provider import interrupted")
			provider.importErr = interrupted
			imports := provider.imports
			if origin == "restore" {
				require.NoError(t, restoreImportGroup(t, target, source), "healthy wallets survive an independent module failure")
			} else {
				err := target.SyncAccountManagementState(nil)
				require.ErrorIs(t, err, interrupted)
				require.True(t, accountModuleOnly(err))
			}
			require.Greater(t, provider.imports, imports, "the real provider fault must be reached")
			targetCopy := importModuleTarget(t, target, provider.id, want)
			_, err := target.captureWalletIdentity()
			require.NoError(t, err, "an unrelated provider must not disable the healthy wallet")
			require.NoError(t, target.SwitchAccount(0))
			_, err = target.UnlockWallet("password")
			require.NoError(t, err)
			current := importModuleTarget(t, target, provider.id, want)
			require.Equal(t, targetCopy.Value, current.Value)
			require.Equal(t, beforeRemote, snapshotRGB11MemoryRecords(remote))
			provider.importErr = nil
			require.NoError(t, target.SyncAccountManagementState(nil))
			assertImportComplete(t, target, provider, want)
			require.Equal(t, beforeRemote, snapshotRGB11MemoryRecords(remote), "retry must import the target without republishing stale local data")
		})
	}
}

func TestAccountPWAColdRemoteImportPreservesPendingChannelRuntime(t *testing.T) {
	old := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = old })
	for _, closing := range []bool{false, true} {
		t.Run(fmt.Sprintf("closing=%t", closing), func(t *testing.T) {
			remote := newRGB11MemoryDKVSHTTP()
			source, sourceProvider, _ := newImportSource(t, remote)
			target, provider, closeTarget := newImportGroup(t, remote)
			require.NoError(t, restoreImportGroup(t, target, source))
			channelID, err := target.GetChannelAddress()
			require.NoError(t, err)
			const reservationID int64 = 9301
			// Reuse the existing channel shape, then persist it through the real DB.
			stored := savePendingFundingFixture(t, newMemoryKVDB(), target.wallet.(*InternalWallet), channelID, reservationID)
			stored.RemoteChanCfg.PaymentKey = target.serverNode.Pubkey
			stored.StaticMerkleRoot = stored.CalcStaticMerkleRoot()
			stored.ChannelHash = stored.CalcHash()
			require.NoError(t, SaveChannelInDB(target.db, stored))
			require.NoError(t, SaveReservation(target.db, &FundingReservation{FundingDataInDB: FundingDataInDB{
				ReservationBase: NewReservationBase(reservationID, true, ResvStatus(RS_FUNDING_BROADCASTED), target.wallet), ChannelId: channelID,
			}}))
			if closing {
				require.NoError(t, DeleteReservation(target.db, RESV_TYPE_OPEN, reservationID))
				stored.Status = CS_CLOSING_DEANCHOR_BROADCASTED
				stored.UpdateTime = reservationID
				stored.StaticMerkleRoot = stored.CalcStaticMerkleRoot()
				stored.ChannelHash = stored.CalcHash()
				require.NoError(t, SaveChannelInDB(target.db, stored))
				require.NoError(t, SaveReservation(target.db, &ClosingReservation{ClosingDataInDB: ClosingDataInDB{
					ReservationBase: NewReservationBase(reservationID, true, ResvStatus(CS_CLOSING_DEANCHOR_BROADCASTED), target.wallet), ChannelId: channelID,
				}}))
			}
			before, err := target.db.Read([]byte(GetChannelKey(channelID)))
			require.NoError(t, err)
			want := []byte("independent device updated ownership")
			stageManagedImportTestPayload(t, source, sourceProvider, want)
			require.NoError(t, source.SyncAccountManagementState(nil))
			beforeRemote := snapshotRGB11MemoryRecords(remote)
			interrupted := errors.New("PWA remote provider persistence interrupted")
			provider.importErr = interrupted
			imports := provider.imports
			require.ErrorIs(t, target.SyncAccountManagementState(nil), interrupted)
			require.Greater(t, provider.imports, imports)
			targetCopy := importModuleTarget(t, target, provider.id, want)
			cfg := target.cfg
			closeTarget()
			reopened, resumedProvider, _ := openImportGroup(t, cfg, remote)
			resumedProvider.importErr = interrupted
			_, err = reopened.UnlockWallet("password")
			require.NoError(t, err, "a failed independent provider must not prevent channel rehydration")
			current := importModuleTarget(t, reopened, resumedProvider.id, want)
			require.Equal(t, targetCopy.Value, current.Value)
			require.ErrorIs(t, reopened.SyncAccountManagementState(nil), interrupted)
			require.Positive(t, resumedProvider.imports)
			resumedProvider.importErr = nil
			require.NoError(t, reopened.SyncAccountManagementState(nil))
			assertImportComplete(t, reopened, resumedProvider, want)
			_, err = reopened.UnlockWallet("password")
			require.NoError(t, err)
			var channel *Channel
			if closing {
				resv := reopened.GetClosingReservations()[reservationID]
				require.NotNil(t, resv)
				require.NotNil(t, resv.LocalWallet())
				channel = resv.Channel
			} else {
				resv := reopened.GetFundingReservations()[reservationID]
				require.NotNil(t, resv)
				require.NotNil(t, resv.LocalWallet())
				channel = resv.Channel
			}
			require.NotNil(t, channel, "successful PWA unlock lost the unfinished channel runtime")
			require.Same(t, channel, reopened.GetCurrentChannel())
			require.Equal(t, stored.Status, channel.Status)
			_, err = reopened.UnlockWallet("password")
			require.NoError(t, err)
			require.Same(t, channel, reopened.GetCurrentChannel(), "UI reauthentication rebuilt a running channel")
			after, err := reopened.db.Read([]byte(GetChannelKey(channelID)))
			require.NoError(t, err)
			require.Equal(t, before, after, "runtime recovery rewrote the persisted channel")
			require.Equal(t, beforeRemote, snapshotRGB11MemoryRecords(remote))
		})
	}
}

func TestAccountPWARootMnemonicRetryResumesProviderImport(t *testing.T) {
	old := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = old })
	remote := newRGB11MemoryDKVSHTTP()
	source, _, _ := newImportSource(t, remote)
	require.NoError(t, source.SyncAccountRootWrapper(context.Background()))
	target, provider, closeTarget := newImportGroup(t, remote)
	interrupted := errors.New("root mnemonic provider interruption")
	provider.importErr = interrupted
	_, err := target.RecoverAccountManagementFromRootMnemonic(context.Background(), accountRootWrapperTestMnemonic, "password")
	require.NoError(t, err, "root recovery keeps healthy wallets when a module import fails")
	require.Positive(t, provider.imports)
	targetCopy := importModuleTarget(t, target, provider.id, []byte("A"))
	before := target.GetWalletCatalog()
	beforeRemote := snapshotRGB11MemoryRecords(remote)
	cfg := target.cfg
	closeTarget()
	target, provider, _ = openImportGroup(t, cfg, remote)
	_, err = target.RecoverAccountManagementFromRootMnemonic(context.Background(), accountRootWrapperTestMnemonic, "password")
	require.NoError(t, err, "same PWA mnemonic entry must resume its committed module target")
	require.Equal(t, before, target.GetWalletCatalog())
	require.Positive(t, provider.imports)
	assertImportComplete(t, target, provider, []byte("A"))
	current, err := target.readAccountLocal(target.accountProfile.AccountID, provider.id, AccountManagedDataGlobalScope)
	require.NoError(t, err)
	require.Equal(t, targetCopy.Value, current.Value)
	require.Equal(t, beforeRemote, snapshotRGB11MemoryRecords(remote))
}

func TestAccountPWARecoveryPreviewUsesInterruptedTargetBeforeNewRemoteState(t *testing.T) {
	old := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = old })
	remote := newRGB11MemoryDKVSHTTP()
	source, _, _ := newImportSource(t, remote)
	target, provider, closeTarget := newImportGroup(t, remote)
	failure := errors.New("PWA restore interruption")
	provider.importErr = failure
	require.NoError(t, restoreImportGroup(t, target, source))
	require.Positive(t, provider.imports)
	original := importModuleTarget(t, target, provider.id, []byte("A"))
	cfg := target.cfg
	closeTarget()
	target, provider, _ = openImportGroup(t, cfg, remote)
	provider.importErr = failure
	require.NoError(t, source.UpdateWalletName(source.GetCurrentWalletId(), "Other device advanced the state"))
	require.NoError(t, source.SyncAccountManagementState(nil))
	locator, location := importGroupMaterial(t, source)
	remoteBefore := snapshotRGB11MemoryRecords(remote)
	preview, err := target.LoadAccountManagementStateForRecovery(location, locator, source.accountSecret, accountRootWrapperTestMnemonic)
	require.NoError(t, err)
	require.Equal(t, "Other device advanced the state", preview.State.Wallets[0].Name,
		"independent wallet metadata need not freeze on a failed business module")
	local, err := target.readAccountLocal(locator.AccountID, provider.id, AccountManagedDataGlobalScope)
	require.NoError(t, err)
	require.True(t, local.Restoring)
	require.Equal(t, original.Value, local.Value, "preview must not replace the interrupted module target")
	_, err = target.RestoreAccountManagementState(*preview, source.accountSecret, "password", locator,
		AccountManagementRestoreOptions{Location: location, StorageMode: AccountStorageTemporary, RecordTTL: testRGB11FreeLocalTTL})
	require.NoError(t, err)
	require.Equal(t, "Other device advanced the state", target.GetWalletCatalog()[0].Name)
	current := importModuleTarget(t, target, provider.id, []byte("A"))
	require.Equal(t, original.Value, current.Value)
	provider.importErr = nil
	require.NoError(t, target.SyncAccountManagementState(nil))
	assertImportComplete(t, target, provider, []byte("A"))
	require.Equal(t, remoteBefore, snapshotRGB11MemoryRecords(remote))
}

func TestAccountPWARootRestoreCommitFailureLeavesNoImportMarker(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	remote := newRGB11MemoryDKVSHTTP()
	source, _ := managedImportTestSource(t, remote)
	require.NoError(t, source.SyncAccountRootWrapper(context.Background()))
	target, provider := managedImportTestManager(t, remote)
	// PWA can have imported its root while discovery was unavailable, then
	// restore the discovered backup when connectivity returns.
	_, err := target.ImportWallet(accountRootWrapperTestMnemonic, "password")
	require.NoError(t, err)
	store, err := target.accountDKVSStore()
	require.NoError(t, err)
	wrapperKey, err := accountRootWrapperKey(source.wallet)
	require.NoError(t, err)
	stateKey, err := source.accountManagedStateKey(source.wallet)
	require.NoError(t, err)
	dataKey, err := account.ManagedIndexKey(source.accountProfile.AccountID)
	require.NoError(t, err)
	require.NoError(t, store.Refresh(wrapperKey, stateKey, dataKey))
	before := target.GetWalletCatalog()
	beforeProfile, err := target.db.Read(accountManagementProfileKey())
	require.NoError(t, err)
	beforeCopies, err := target.listAccountLocal(source.accountProfile.AccountID)
	require.NoError(t, err)
	database := target.db
	fault := &accountPersistenceReviewDB{KVDB: database, profileKey: accountManagementProfileKey()}
	fault.armed.Store(true)
	target.db = fault
	_, err = target.RecoverAccountManagementFromRootMnemonic(context.Background(), accountRootWrapperTestMnemonic, "password")
	require.ErrorIs(t, err, errAccountPersistenceReview)
	require.Positive(t, fault.hits.Load(), "root recovery must reach the real profile commit fault")
	fault.armed.Store(false)
	target.db = database
	require.Equal(t, before, target.GetWalletCatalog())
	afterProfile, err := database.Read(accountManagementProfileKey())
	require.NoError(t, err)
	require.Equal(t, beforeProfile, afterProfile)
	afterCopies, err := target.listAccountLocal(source.accountProfile.AccountID)
	require.NoError(t, err)
	require.Equal(t, beforeCopies, afterCopies, "failed core commit must not strand partial module copies")
	require.Zero(t, provider.imports)
	_, err = target.RecoverAccountManagementFromRootMnemonic(context.Background(), accountRootWrapperTestMnemonic, "password")
	require.ErrorIs(t, err, ErrAccountDBStopped,
		"restoring storage availability must not revive the failed Manager")
	service := target.serverNode
	target.Close()
	target = newAccountManagementAutoTestManager(t)
	target.db, target.serverNode = database, service
	configureRGB11DKVSTestManager(target, remote)
	require.NoError(t, target.initDB())
	provider = managedImportProvider(t, target)
	target.managedDataProviders = map[string]AccountManagedDataProvider{provider.id: provider}
	_, err = target.UnlockWallet("password")
	require.NoError(t, err)
	require.Equal(t, before, target.GetWalletCatalog(), "restart must load the pre-failure wallet catalog")
	_, err = target.RecoverAccountManagementFromRootMnemonic(context.Background(), accountRootWrapperTestMnemonic, "password")
	require.NoError(t, err)
	assertImportComplete(t, target, provider, []byte("A"))
}

func TestImportFailureKeepsCatalog(t *testing.T) {
	old := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = old })
	remote := newRGB11MemoryDKVSHTTP()
	source, _, _ := newImportSource(t, remote)
	target, provider, _ := newImportGroup(t, remote)
	injected := errors.New("review provider interruption")
	provider.importErr = injected
	require.NoError(t, restoreImportGroup(t, target, source))
	require.Positive(t, provider.imports)
	syncImportGroup(t, target, injected)
	before := importModuleTarget(t, target, provider.id, []byte("A"))
	require.NoError(t, target.UpdateWalletName(target.GetAccountManagementStatus().RootWalletID, "Edited while independent module restores"))
	require.ErrorIs(t, target.SyncAccountManagementState(nil), injected)
	after := importModuleTarget(t, target, provider.id, []byte("A"))
	require.Equal(t, before.Value, after.Value)
	require.Equal(t, "Edited while independent module restores", target.GetWalletCatalog()[0].Name)
	provider.importErr = nil
	require.NoError(t, restoreImportGroup(t, target, source))
	assertImportComplete(t, target, provider, []byte("A"))
	require.NoError(t, target.SyncAccountManagementState(nil))
	require.Equal(t, "Edited while independent module restores", target.GetWalletCatalog()[0].Name)
}

func TestFinalReviewRemoteImportCanResumeAfterProviderRecovers(t *testing.T) {
	old := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = old })
	remote := newRGB11MemoryDKVSHTTP()
	source, sourceProvider := managedImportTestSource(t, remote)
	target, provider := managedImportTestManager(t, remote)
	require.NoError(t, restoreManagedImportTestWallet(t, target, source))
	want := []byte("new data")
	stageManagedImportTestPayload(t, source, sourceProvider, want)
	require.NoError(t, source.SyncAccountManagementState(nil))
	beforeRemote := snapshotRGB11MemoryRecords(remote)
	injected := errors.New("review provider interruption")
	provider.importErr = injected
	imports := provider.imports
	err := target.SyncAccountManagementState(nil)
	require.ErrorIs(t, err, injected)
	require.True(t, accountModuleOnly(err))
	require.Greater(t, provider.imports, imports)
	importModuleTarget(t, target, provider.id, want)
	require.Equal(t, beforeRemote, snapshotRGB11MemoryRecords(remote))
	provider.importErr = nil
	require.NoError(t, target.SyncAccountManagementState(nil))
	assertImportComplete(t, target, provider, want)
	require.Equal(t, beforeRemote, snapshotRGB11MemoryRecords(remote))
}

func TestFinalReviewSwitchWalletStatusFailureIsReported(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	m, _, _ := reviewAccountDevices(t)
	root := m.GetAccountManagementStatus().RootWalletID
	if _, _, err := m.CreateWallet("password"); err != nil {
		t.Fatal(err)
	}
	m.accountBackgroundWG.Wait()
	db := m.db
	before := m.GetCurrentWalletId()
	fault := &accountPersistenceReviewDB{KVDB: db, profileKey: []byte(DB_KEY_STATUS)}
	fault.armed.Store(true)
	m.db = fault
	err := m.SwitchWallet(root, "password")
	fault.armed.Store(false)
	m.db = db
	durable := loadStatusFromDB(db).CurrentWallet
	t.Logf("error=%v hits=%d selected_changed=%t durable_old=%t", err, fault.hits.Load(), m.GetCurrentWalletId() != before, durable == before)
	if err == nil {
		t.Error("switch returned success despite failed status persistence")
	}
	if m.GetCurrentWalletId() != durable {
		t.Error("live and durable selection diverged")
	}
}

func TestFinalReviewRGBDirtyWriteFailureReleasesManagerLock(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	m := configuredRGB11ManagedOperationTestManager(t)
	m.accountBackgroundWG.Wait()
	db := m.db
	beforeProfile, err := db.Read(accountManagementProfileKey())
	require.NoError(t, err)
	beforeGeneration := m.accountProfile.ManagedDataGeneration
	scope, err := m.rgbManager.rgb11AccountManagedScopeID()
	require.NoError(t, err)
	moduleKey := accountLocalKey(m.accountProfile.AccountID, rgb11AccountManagedProviderID, scope)
	beforeModule, err := db.Read(moduleKey)
	require.NoError(t, err)
	beforeRGBRecords := make(map[string][]byte)
	require.NoError(t, db.BatchRead([]byte("rgb11-engine-"), false, func(key, value []byte) error {
		beforeRGBRecords[string(key)] = bytes.Clone(value)
		return nil
	}))
	fault := &accountPersistenceReviewDB{KVDB: db, profileKey: moduleKey}
	m.db = fault
	t.Cleanup(func() { fault.armed.Store(false); m.db = db })
	_, err = runRGB11ManagedOperation(m, nil, rgb11ManagedOperationNew, func(rgb *rgb11Manager) (struct{}, error) {
		fault.armed.Store(true)
		_, createErr := rgb.engine.CreateReceive(corewallet.ReceiveParams{
			Network: invoicing.BitcoinTestnet4, RecipientID: "dirty-write-failure",
			WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
		})
		return struct{}{}, createErr
	})
	acquired := m.mutex.TryLock()
	if acquired {
		m.mutex.Unlock()
	}
	fault.armed.Store(false)
	m.db = db
	t.Logf("atomic RGB fault result=%v module_fault_hits=%d", err, fault.hits.Load())
	if fault.hits.Load() == 0 {
		t.Fatal("RGB module StageAccountChange fault was not exercised")
	}
	if !errors.Is(err, errAccountPersistenceReview) {
		t.Errorf("persistence failure was not propagated: %v", err)
	}
	if !acquired {
		t.Fatal("manager mutex remains locked")
	}
	m.rgbManagedOperationMu.Lock()
	active := m.rgbManagedOperationActive
	m.rgbManagedOperationMu.Unlock()
	if active {
		t.Fatal("managed RGB operation remained active")
	}
	afterProfile, readErr := db.Read(accountManagementProfileKey())
	if readErr != nil || !bytes.Equal(beforeProfile, afterProfile) ||
		m.accountProfile.ManagedDataGeneration != beforeGeneration || m.accountProfile.ManagedDataDirty {
		t.Fatal("failed business+dirty transaction changed the durable account profile")
	}
	afterModule, err := db.Read(moduleKey)
	require.NoError(t, err)
	require.True(t, bytes.Equal(beforeModule, afterModule), "failed business transaction advanced the RGB module generation or receipt")
	afterRGBRecords := make(map[string][]byte)
	require.NoError(t, db.BatchRead([]byte("rgb11-engine-"), false, func(key, value []byte) error {
		afterRGBRecords[string(key)] = bytes.Clone(value)
		return nil
	}))
	require.True(t, reflect.DeepEqual(beforeRGBRecords, afterRGBRecords), "failed business+dirty transaction changed RGB receive records")
	if err := m.SyncAccountManagementState(nil); err != nil {
		t.Fatalf("healthy retry: %v", err)
	}
}

type finalReviewProfileReadFault struct{ *accountPersistenceReviewDB }

func (d *finalReviewProfileReadFault) Read(key []byte) ([]byte, error) {
	if d.armed.Load() && string(key) == string(d.profileKey) {
		d.hits.Add(1)
		return nil, errAccountPersistenceReview
	}
	return d.KVDB.Read(key)
}
func TestFinalReviewProfileReadErrorDoesNotReinitializeSecret(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	m, _, _ := reviewAccountDevices(t)
	m.accountBackgroundWG.Wait()
	db := m.db
	secret := append([]byte(nil), m.accountSecret...)
	defer zeroBytes(secret)
	before, err := db.Read(accountManagementProfileKey())
	if err != nil {
		t.Fatal(err)
	}
	fault := &finalReviewProfileReadFault{&accountPersistenceReviewDB{KVDB: db, profileKey: accountManagementProfileKey()}}
	fault.armed.Store(true)
	m.db = fault
	m.mutex.Lock()
	readErr := m.loadAccountManagementProfileLocked()
	absent := m.accountProfile == nil
	m.mutex.Unlock()
	fault.armed.Store(false)
	m.db = db
	initErr := m.InitializeAccountManagement("password")
	after, err := db.Read(accountManagementProfileKey())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("read_error=%v fault_hits=%d treated_as_absent=%t initialize_error=%v secret_changed=%t profile_overwritten=%t", readErr, fault.hits.Load(), absent, initErr, string(secret) != string(m.accountSecret), string(before) != string(after))
	if !errors.Is(readErr, errAccountPersistenceReview) {
		t.Error("profile read failure was swallowed")
	}
	if string(secret) != string(m.accountSecret) {
		t.Error("subsequent initialization replaced the existing account secret")
	}
}

func TestFinalReviewCleanRemoteImportResume(t *testing.T) {
	old := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = old })
	for _, cold := range []bool{false, true} {
		t.Run(fmt.Sprintf("cold=%t", cold), func(t *testing.T) {
			remote := newRGB11MemoryDKVSHTTP()
			source, sourceProvider, _ := newImportSource(t, remote)
			target, provider, closeTarget := newImportGroup(t, remote)
			require.NoError(t, restoreImportGroup(t, target, source))
			require.NoError(t, source.UpdateWalletName(source.GetAccountManagementStatus().RootWalletID, "Remote updated name"))
			want := []byte("Remote updated data")
			stageManagedImportTestPayload(t, source, sourceProvider, want)
			require.NoError(t, source.SyncAccountManagementState(nil))
			beforeRemote := snapshotRGB11MemoryRecords(remote)
			failure := errors.New("interrupt remote import provider import")
			provider.importErr = failure
			imports := provider.imports
			require.ErrorIs(t, target.SyncAccountManagementState(nil), failure)
			require.Greater(t, provider.imports, imports)
			original := importModuleTarget(t, target, provider.id, want)
			require.Zero(t, target.GetAccountManagementStatus().PendingChanges)
			if cold {
				cfg := target.cfg
				closeTarget()
				target, provider, _ = openImportGroup(t, cfg, remote)
				provider.importErr = failure
				_, err := target.UnlockWallet("password")
				require.NoError(t, err, "wallet authentication is independent of the failed provider")
				current := importModuleTarget(t, target, provider.id, want)
				require.Equal(t, original.Value, current.Value)
			}
			provider.importErr = nil
			require.NoError(t, target.SyncAccountManagementState(nil))
			assertImportComplete(t, target, provider, want)
			require.Equal(t, "Remote updated name", target.GetWalletCatalog()[0].Name)
			require.Zero(t, target.GetAccountManagementStatus().PendingChanges)
			require.Equal(t, beforeRemote, snapshotRGB11MemoryRecords(remote))
			require.NoError(t, source.SyncAccountManagementState(nil))
			require.Equal(t, "Remote updated name", source.GetWalletCatalog()[0].Name)
		})
	}
}

func TestReviewImportCopyIntegrity(t *testing.T) {
	old := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = old })
	for _, corrupt := range []string{"version", "account", "network", "scope", "ciphertext"} {
		t.Run(corrupt, func(t *testing.T) {
			remote := newRGB11MemoryDKVSHTTP()
			source, sourceProvider := managedImportTestSource(t, remote)
			target, provider := managedImportTestManager(t, remote)
			require.NoError(t, restoreManagedImportTestWallet(t, target, source))
			want := []byte("new data")
			stageManagedImportTestPayload(t, source, sourceProvider, want)
			require.NoError(t, source.SyncAccountManagementState(nil))
			provider.importErr = errors.New("stop import")
			imports := provider.imports
			require.ErrorIs(t, target.SyncAccountManagementState(nil), provider.importErr)
			require.Greater(t, provider.imports, imports)
			local := importModuleTarget(t, target, provider.id, want)
			key := accountLocalKey(local.AccountID, local.Provider, local.Scope)
			original, err := target.db.Read(key)
			require.NoError(t, err)
			switch corrupt {
			case "version":
				local.Version++
			case "account":
				local.AccountID = "another-account"
			case "network":
				local.Network = "mainnet"
			case "scope":
				local.Scope = "another-scope"
			case "ciphertext":
				local.Value[len(local.Value)-1] ^= 1
			}
			encoded, err := EncodeToBytes(local)
			require.NoError(t, err)
			require.NoError(t, target.db.Write(key, encoded))
			beforeRemote := snapshotRGB11MemoryRecords(remote)
			beforeCatalog := target.GetWalletCatalog()
			provider.importErr = nil
			imports = provider.imports
			err = target.SyncAccountManagementState(nil)
			if corrupt == "ciphertext" {
				// With no unconfirmed local edit, normal sync may repair damaged bytes
				// from the independently authenticated remote module. It must never
				// pass the damaged target to the provider or republish it.
				require.NoError(t, err)
				require.Greater(t, provider.imports, imports)
				for _, attempt := range provider.imported[imports:] {
					require.Len(t, attempt, 1)
					require.Equal(t, want, attempt[0].Payload, "only authenticated replacement bytes may reach the provider")
				}
				assertImportComplete(t, target, provider, want)
			} else {
				require.Error(t, err, "invalid module identity/version must remain fail closed")
				require.Equal(t, imports, provider.imports, "invalid local metadata reached the provider")
				require.Error(t, target.CheckAccountRestore(provider.id, AccountManagedDataGlobalScope))
				require.Error(t, target.CheckAccountWrite(provider.id, AccountManagedDataGlobalScope))
				require.NoError(t, target.db.Write(key, original))
				require.NoError(t, target.SyncAccountManagementState(nil))
				assertImportComplete(t, target, provider, want)
			}
			require.Equal(t, beforeCatalog, target.GetWalletCatalog())
			require.Equal(t, beforeRemote, snapshotRGB11MemoryRecords(remote), "bad local data must never replace a verified remote value")
		})
	}
}

func TestFinalReviewSwitchExistingAccountStatusFailure(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	m, _, _ := reviewAccountDevices(t)
	if err := m.EnsureAccount(m.GetCurrentWalletId(), 1, "Existing", ""); err != nil {
		t.Fatal(err)
	}
	m.accountBackgroundWG.Wait()
	database := m.db
	fault := &accountPersistenceReviewDB{KVDB: database, profileKey: []byte(DB_KEY_STATUS)}
	fault.armed.Store(true)
	m.db = fault
	err := m.SwitchAccount(1)
	m.db = database
	if !errors.Is(err, errAccountPersistenceReview) || fault.hits.Load() != 1 {
		t.Fatalf("status failure was hidden: %v", err)
	}
	if m.GetCurrentAccountId() != 0 || m.wallet.GetSubAccount() != 0 || loadStatusFromDB(database).CurrentAccount != 0 {
		t.Fatal("failed selection changed live or durable identity")
	}
	if err := m.SwitchAccount(1); err != nil {
		t.Fatal(err)
	}
	if m.GetCurrentAccountId() != 1 || loadStatusFromDB(database).CurrentAccount != 1 {
		t.Fatal("healthy selection did not persist")
	}
}

func TestFinalReviewRemoteImportCompletionWriteCanRetry(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	remote := newRGB11MemoryDKVSHTTP()
	source, sourceProvider := managedImportTestSource(t, remote)
	target, provider := managedImportTestManager(t, remote)
	if err := restoreManagedImportTestWallet(t, target, source); err != nil {
		t.Fatal(err)
	}
	stageManagedImportTestPayload(t, source, sourceProvider, []byte("committed data"))
	if err := source.SyncAccountManagementState(nil); err != nil {
		t.Fatal(err)
	}
	beforeRemote := snapshotRGB11MemoryRecords(remote)
	database := target.db
	failure := errors.New("module completion write failed")
	fault := &managedImportFaultDB{KVDB: database,
		key: accountLocalKey(target.accountProfile.AccountID, provider.id, AccountManagedDataGlobalScope)}
	target.db = fault
	beforeImport := provider.beforeImport
	provider.beforeImport = func() { beforeImport(); fault.writeErr = failure }
	if err := target.SyncAccountManagementState(nil); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	target.db = database
	if fault.hits.Load() == 0 {
		t.Fatal("completion persistence fault was not reached")
	}
	if string(provider.payloads[0].Payload) != "committed data" {
		t.Fatal("failure was not after import")
	}
	if err := target.CheckAccountRestore(provider.id, AccountManagedDataGlobalScope); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
		t.Fatal("missing module restore marker")
	}
	importModuleTarget(t, target, provider.id, []byte("committed data"))
	if !reflect.DeepEqual(beforeRemote, snapshotRGB11MemoryRecords(remote)) {
		t.Fatal("completion failure changed remote records")
	}
	require.ErrorIs(t, target.SyncAccountManagementState(nil), ErrAccountDBStopped)
	service := target.serverNode
	target.Close()
	target = newAccountManagementAutoTestManager(t)
	target.db, target.serverNode = database, service
	configureRGB11DKVSTestManager(target, remote)
	require.NoError(t, target.initDB())
	provider = managedImportProvider(t, target)
	target.managedDataProviders = map[string]AccountManagedDataProvider{provider.id: provider}
	_, err := target.UnlockWallet("password")
	require.NoError(t, err)
	imports := provider.imports
	if err := target.SyncAccountManagementState(nil); err != nil {
		t.Fatal(err)
	}
	if provider.imports <= imports {
		t.Fatal("retry did not idempotently import exact target")
	}
	assertImportComplete(t, target, provider, []byte("committed data"))
	if !reflect.DeepEqual(beforeRemote, snapshotRGB11MemoryRecords(remote)) {
		t.Fatal("completion retry changed remote records")
	}
}

func TestFinalReviewIncompleteRestoreOnlyBlocksFailedModule(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	remote := newRGB11MemoryDKVSHTTP()
	source, _ := managedImportTestSource(t, remote)
	target, provider := managedImportTestManager(t, remote)
	provider.importErr = errors.New("incomplete restore")
	if err := restoreManagedImportTestWallet(t, target, source); err != nil {
		t.Fatalf("failed independent module prevented wallet restoration: %v", err)
	}
	if err := target.CheckAccountRestore(provider.id, AccountManagedDataGlobalScope); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
		t.Fatalf("failed module escaped durable protection: %v", err)
	}
	if err := target.CheckAccountRestore("wallets", AccountManagedDataGlobalScope); err != nil {
		t.Fatalf("independent failed module froze healthy catalog: %v", err)
	}
	before := target.GetWalletCatalog()
	provider.importErr = nil
	if err := restoreManagedImportTestWallet(t, target, source); err != nil {
		t.Fatal(err)
	}
	if err := target.CheckAccountRestore(provider.id, AccountManagedDataGlobalScope); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, target.GetWalletCatalog()) {
		t.Fatal("module retry replaced the healthy wallet catalog")
	}
}

func TestFinalReviewRGBOperationPanicClearsRuntimeState(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	m := configuredRGB11ManagedOperationTestManager(t)
	m.accountBackgroundWG.Wait()
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = runRGB11ManagedOperation(m, nil, rgb11ManagedOperationNew, func(rgb *rgb11Manager) (struct{}, error) {
			panic("operation panic")
		})
	}()
	if recovered == nil {
		t.Fatal("test did not panic")
	}
	if m.rgbManagedOperationActive || m.accountOperationActive() {
		t.Fatal("panic leaked operation state")
	}
	m.accountBackgroundWG.Wait()
	if !m.mutex.TryLock() {
		t.Fatal("panic leaked manager mutex")
	}
	m.mutex.Unlock()
	_, err := runRGB11ManagedOperation(m, nil, rgb11ManagedOperationNew, func(rgb *rgb11Manager) (struct{}, error) { return struct{}{}, nil })
	if err != nil {
		t.Fatal(err)
	}
}

func TestFinalReviewCleanPartialProviderResumePreservesBothProviders(t *testing.T) {
	old := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = old })
	remote := newRGB11MemoryDKVSHTTP()
	source, remoteProvider, _ := newImportSource(t, remote)
	secondSource := importProviderWithID(t, source, "ztest")
	secondSource.payloads = []AccountManagedDataPayload{{Scope: AccountManagedDataGlobalScope, Payload: []byte("base")}}
	require.NoError(t, source.RegisterAccountManagedDataProvider(secondSource))
	require.NoError(t, source.SyncAccountManagementState(nil))
	target, firstTarget, closeTarget := newImportGroup(t, remote)
	secondTarget := importProviderWithID(t, target, "ztest")
	require.NoError(t, target.RegisterAccountManagedDataProvider(secondTarget))
	require.NoError(t, restoreImportGroup(t, target, source))
	stageManagedImportTestPayload(t, source, secondSource, []byte("second remote change"))
	stageManagedImportTestPayload(t, source, remoteProvider, []byte("remote change"))
	require.NoError(t, source.SyncAccountManagementState(nil))
	beforeRemote := snapshotRGB11MemoryRecords(remote)
	failure := errors.New("second provider failed after first import")
	secondTarget.importErr = failure
	imports := secondTarget.imports
	require.ErrorIs(t, target.SyncAccountManagementState(nil), failure)
	require.Greater(t, secondTarget.imports, imports)
	assertImportComplete(t, target, firstTarget, []byte("remote change"))
	original := importModuleTarget(t, target, secondTarget.id, []byte("second remote change"))
	cfg := target.cfg
	closeTarget()
	restarted, firstRestart, _ := openImportGroup(t, cfg, remote)
	secondRestart := importProviderWithID(t, restarted, "ztest")
	secondRestart.importErr = failure
	require.NoError(t, restarted.RegisterAccountManagedDataProvider(secondRestart))
	_, err := restarted.UnlockWallet("password")
	require.NoError(t, err)
	assertImportComplete(t, restarted, firstRestart, []byte("remote change"))
	current := importModuleTarget(t, restarted, secondRestart.id, []byte("second remote change"))
	require.Equal(t, original.Value, current.Value)
	secondRestart.importErr = nil
	require.NoError(t, restarted.SyncAccountManagementState(nil))
	assertImportComplete(t, restarted, firstRestart, []byte("remote change"))
	assertImportComplete(t, restarted, secondRestart, []byte("second remote change"))
	require.NoError(t, source.SyncAccountManagementState(nil))
	require.Equal(t, []byte("second remote change"), secondSource.payloads[0].Payload)
	require.Equal(t, []byte("remote change"), remoteProvider.payloads[0].Payload)
	require.Equal(t, beforeRemote, snapshotRGB11MemoryRecords(remote))
}

func TestFinalReviewLockedAccountSwitchCannotCreateCatalogEntry(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	m, _, _ := reviewAccountDevices(t)
	m.accountBackgroundWG.Wait()
	before := accountPersistenceReviewCatalog(t, m.db)
	status := loadStatusFromDB(m.db)
	m.wallet = nil
	for _, info := range m.walletInfoMap {
		info.Wallet = nil
	}
	if err := m.SwitchAccount(2); !errors.Is(err, ErrAccountManagementWalletUnavailable) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, accountPersistenceReviewCatalog(t, m.db)) || !reflect.DeepEqual(status, loadStatusFromDB(m.db)) || m.GetAccountManagementStatus().PendingChanges != 0 {
		t.Fatal("locked selection mutated catalog or status")
	}
}
