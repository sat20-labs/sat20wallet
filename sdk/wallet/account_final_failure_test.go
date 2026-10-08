package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"reflect"
	"testing"

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

func TestAccountPWAIncompleteImportBlocksWalletUse(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	for _, origin := range []string{"restore", "remote-apply"} {
		t.Run(origin, func(t *testing.T) {
			remote := newRGB11MemoryDKVSHTTP()
			source, sourceProvider := managedImportTestSource(t, remote)
			target, provider := managedImportTestManager(t, remote)
			if origin == "remote-apply" {
				require.NoError(t, restoreManagedImportTestWallet(t, target, source))
				sourceProvider.payloads[0].Payload = []byte("new independent-device data")
				require.NoError(t, source.SyncAccountManagementState(nil))
			}
			interrupted := errors.New("PWA provider import interrupted")
			provider.importErr = interrupted
			if origin == "restore" {
				require.ErrorIs(t, restoreManagedImportTestWallet(t, target, source), interrupted)
			} else {
				require.ErrorIs(t, target.SyncAccountManagementState(nil), interrupted)
			}
			_, err := target.captureWalletIdentity()
			require.ErrorIs(t, err, ErrAccountManagedDataImportIncomplete, "PSBT approval must remain closed")
			require.True(t, target.isL1RGBInputProtected("0000000000000000000000000000000000000000000000000000000000000001:0"), "missing projection must not authorize BTC/fee selection")
			require.ErrorIs(t, target.SwitchAccount(0), ErrAccountManagedDataImportIncomplete)
			_, err = target.UnlockWallet("password")
			require.ErrorIs(t, err, ErrAccountManagedDataImportIncomplete, "PWA must not open a wallet session before import completes")
			provider.importErr = nil
			if origin == "restore" {
				require.NoError(t, restoreManagedImportTestWallet(t, target, source))
			} else {
				require.NoError(t, target.SyncAccountManagementState(nil))
			}
			_, err = target.UnlockWallet("password")
			require.NoError(t, err)
			_, err = target.captureWalletIdentity()
			require.NoError(t, err)
		})
	}
}

// A real PWA reload loses runtime objects, but keeps unfinished channel work.
// Authenticating to resume remote-apply must recover that work even though the
// UI remains locked until the provider import completes.
func TestAccountPWAColdRemoteImportPreservesPendingChannelRuntime(t *testing.T) {
	old := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = old })
	for _, closing := range []bool{false, true} {
		t.Run(fmt.Sprintf("closing=%t", closing), func(t *testing.T) {
			remote := newRGB11MemoryDKVSHTTP()
			source, sourceProvider := managedImportTestSource(t, remote)
			target, provider := managedImportTestManager(t, remote)
			require.NoError(t, restoreManagedImportTestWallet(t, target, source))
			channelID, err := GetP2WSHaddress(target.wallet.GetPaymentPubKey().SerializeCompressed(), target.wallet.GetPaymentPubKey().SerializeCompressed())
			require.NoError(t, err)
			const reservationID int64 = 9301
			stored := savePendingFundingFixture(t, target.db.(*memoryKVDB), target.wallet.(*InternalWallet), channelID, reservationID)
			if closing {
				require.NoError(t, DeleteReservation(target.db, RESV_TYPE_OPEN, reservationID))
				stored.Status = CS_CLOSING_DEANCHOR_BROADCASTED
				stored.UpdateTime = reservationID
				stored.StaticMerkleRoot = stored.CalcStaticMerkleRoot()
				require.NoError(t, SaveChannelInDB(target.db, stored))
				require.NoError(t, SaveReservation(target.db, &ClosingReservation{ClosingDataInDB: ClosingDataInDB{
					ReservationBase: NewReservationBase(reservationID, true, ResvStatus(CS_CLOSING_DEANCHOR_BROADCASTED), target.wallet),
					ChannelId:       channelID,
				}}))
			}
			before, err := target.db.Read([]byte(GetChannelKey(channelID)))
			require.NoError(t, err)
			sourceProvider.payloads[0].Payload = []byte("independent device updated ownership")
			require.NoError(t, source.SyncAccountManagementState(nil))
			interrupted := errors.New("PWA remote provider persistence interrupted")
			provider.importErr = interrupted
			require.ErrorIs(t, target.SyncAccountManagementState(nil), interrupted)

			reopened, resumedProvider := managedImportTestManager(t, remote)
			reopened.db = target.db
			reopened.initResvMap()
			require.NoError(t, reopened.initDB())
			reopened.serverNode = NewNode(&channelHeartbeatTestClient{}, "test", SERVER_NODE, target.wallet.GetPaymentPubKey(), target.wallet.GetNodePubKey())
			resumedProvider.importErr = interrupted
			_, err = reopened.UnlockWallet("password")
			require.ErrorIs(t, err, ErrAccountManagedDataImportIncomplete)
			resumedProvider.importErr = nil
			require.NoError(t, reopened.SyncAccountManagementState(nil))
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
		})
	}
}

func TestAccountPWARootMnemonicRetryResumesProviderImport(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	remote := newRGB11MemoryDKVSHTTP()
	source, _ := managedImportTestSource(t, remote)
	require.NoError(t, source.SyncAccountRootWrapper(context.Background()))
	target, provider := managedImportTestManager(t, remote)
	root := source.wallet
	wrapperKey, err := accountRootWrapperKey(root)
	require.NoError(t, err)
	stateKey, err := source.accountManagedStateKey(root)
	require.NoError(t, err)
	dataKey, err := source.accountManagedDataBlobKey(root)
	require.NoError(t, err)
	store, err := target.accountDKVSStore()
	require.NoError(t, err)
	require.NoError(t, store.Refresh(wrapperKey, stateKey, dataKey))
	interrupted := errors.New("root mnemonic provider interruption")
	provider.importErr = interrupted
	_, err = target.RecoverAccountManagementFromRootMnemonic(context.Background(), accountRootWrapperTestMnemonic, "password")
	require.ErrorIs(t, err, interrupted)
	before := target.GetWalletCatalog()
	// Reload the PWA: discard the runtime secret and rebuild from the same DB.
	target.accountBackgroundWG.Wait()
	reopened, reopenedProvider := managedImportTestManager(t, remote)
	reopened.db = target.db
	require.NoError(t, reopened.initDB())
	target, provider = reopened, reopenedProvider
	_, err = target.RecoverAccountManagementFromRootMnemonic(context.Background(), accountRootWrapperTestMnemonic, "password")
	require.NoError(t, err, "same PWA mnemonic import entry must resume its committed target")
	require.Equal(t, before, target.GetWalletCatalog())
	require.NoError(t, target.checkAccountManagedDataImport())
	require.Equal(t, "A", string(provider.payloads[0].Payload))
}

func TestAccountPWARecoveryPreviewUsesInterruptedTargetBeforeNewRemoteState(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	remote := newRGB11MemoryDKVSHTTP()
	source, _ := managedImportTestSource(t, remote)
	target, provider := managedImportTestManager(t, remote)
	original := managedImportRecoveryValue(t, source)
	provider.importErr = errors.New("PWA restore interruption")
	require.Error(t, restoreManagedImportTestWallet(t, target, source))
	target.accountBackgroundWG.Wait()
	reopened, _ := managedImportTestManager(t, remote)
	reopened.db = target.db
	require.NoError(t, reopened.initDB())
	target = reopened
	require.NoError(t, source.UpdateWalletName(source.GetCurrentWalletId(), "Other device advanced the state"))
	require.NoError(t, source.SyncAccountManagementState(nil))
	location, err := source.AccountIndexerLocation()
	require.NoError(t, err)
	locator := account.Locator{AccountID: source.accountProfile.AccountID}
	preview, err := target.LoadAccountManagementStateForRecovery(location, locator, source.accountSecret, accountRootWrapperTestMnemonic)
	require.NoError(t, err)
	require.Equal(t, original.Hash, preview.Hash, "retry must finish the original authenticated target before adopting R+1")
	_, err = target.RestoreAccountManagementState(*preview, source.accountSecret, "password", locator,
		AccountManagementRestoreOptions{StorageMode: AccountStorageTemporary, RecordTTL: testRGB11FreeLocalTTL})
	require.NoError(t, err)
	require.NoError(t, target.SyncAccountManagementState(nil))
	require.Equal(t, "Other device advanced the state", target.GetWalletCatalog()[0].Name)
}

func TestAccountPWARootRestoreCommitFailureLeavesNoImportMarker(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	remote := newRGB11MemoryDKVSHTTP()
	source, _ := managedImportTestSource(t, remote)
	require.NoError(t, source.SyncAccountRootWrapper(context.Background()))
	target, _ := managedImportTestManager(t, remote)
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
	dataKey, err := source.accountManagedDataBlobKey(source.wallet)
	require.NoError(t, err)
	require.NoError(t, store.Refresh(wrapperKey, stateKey, dataKey))
	before := target.GetWalletCatalog()
	database := target.db
	fault := &accountPersistenceReviewDB{KVDB: database, profileKey: accountManagementProfileKey()}
	fault.armed.Store(true)
	target.db = fault
	_, err = target.RecoverAccountManagementFromRootMnemonic(context.Background(), accountRootWrapperTestMnemonic, "password")
	require.ErrorIs(t, err, errAccountPersistenceReview)
	fault.armed.Store(false)
	target.db = database
	require.Equal(t, before, target.GetWalletCatalog())
	marker, err := target.readAccountManagedDataImportMarker()
	require.NoError(t, err)
	require.Nil(t, marker, "failed local commit must not strand a previously imported root")
	_, err = target.RecoverAccountManagementFromRootMnemonic(context.Background(), accountRootWrapperTestMnemonic, "password")
	require.NoError(t, err)
}

func TestFinalReviewIncompleteRestoreRejectsCatalogMutation(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	remote := newRGB11MemoryDKVSHTTP()
	source, _ := managedImportTestSource(t, remote)
	target, provider := managedImportTestManager(t, remote)
	injected := errors.New("review provider interruption")
	provider.importErr = injected
	if err := restoreManagedImportTestWallet(t, target, source); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	provider.importErr = nil
	renameErr := target.UpdateWalletName(target.GetAccountManagementStatus().RootWalletID, "Edited during incomplete restore")
	retryErr := restoreManagedImportTestWallet(t, target, source)
	syncErr := target.SyncAccountManagementState(nil)
	t.Logf("rename=%v pending=%d restore_retry=%v sync_retry=%v", renameErr, target.GetAccountManagementStatus().PendingChanges, retryErr, syncErr)
	if renameErr == nil {
		t.Error("catalog mutation succeeded while restore is incomplete")
	}
	if retryErr != nil {
		t.Errorf("same-snapshot recovery is now blocked: %v", retryErr)
	}
}

func TestFinalReviewRemoteImportCanResumeAfterProviderRecovers(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	remote := newRGB11MemoryDKVSHTTP()
	source, sourceProvider := managedImportTestSource(t, remote)
	target, provider := managedImportTestManager(t, remote)
	if err := restoreManagedImportTestWallet(t, target, source); err != nil {
		t.Fatal(err)
	}
	sourceProvider.payloads[0].Payload = []byte("new data")
	if err := source.SyncAccountManagementState(nil); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("review provider interruption")
	provider.importErr = injected
	if err := target.SyncAccountManagementState(nil); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	provider.importErr = nil
	syncErr := target.SyncAccountManagementState(nil)
	marker, markerErr := target.readAccountManagedDataImportMarker()
	if markerErr != nil || marker != nil {
		t.Errorf("retry left import marker: %v, %v", marker, markerErr)
	}
	if syncErr != nil {
		t.Errorf("ordinary sync cannot resume: %v", syncErr)
	}
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
	fault := &accountPersistenceReviewDB{KVDB: db, profileKey: accountManagementProfileKey()}
	m.db = fault
	_, err := runRGB11ManagedOperation(m, nil, rgb11ManagedOperationNew, func(rgb *rgb11Manager) (struct{}, error) {
		fault.armed.Store(true)
		return struct{}{}, rgb.autoBackupRGB11AfterMutation()
	})
	acquired := m.mutex.TryLock()
	if acquired {
		m.mutex.Unlock()
	}
	fault.armed.Store(false)
	m.db = db
	if fault.hits.Load() == 0 {
		t.Fatal("profile fault was not exercised")
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
	if !m.accountProfile.ManagedDataDirty {
		t.Fatal("failed persistence reported account data as clean")
	}
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

func TestFinalReviewRemoteRebaseResume(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	for _, cold := range []bool{false, true} {
		t.Run(fmt.Sprintf("cold=%t", cold), func(t *testing.T) {
			remote := newRGB11MemoryDKVSHTTP()
			source, sourceProvider := managedImportTestSource(t, remote)
			target, provider := managedImportTestManager(t, remote)
			if err := restoreManagedImportTestWallet(t, target, source); err != nil {
				t.Fatal(err)
			}
			root := target.GetAccountManagementStatus().RootWalletID
			if err := target.UpdateWalletName(root, "Local merged name"); err != nil {
				t.Fatal(err)
			}
			sourceProvider.payloads[0].Payload = []byte("Remote merged data")
			if err := source.SyncAccountManagementState(nil); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("interrupt rebase provider import")
			provider.importErr = failure
			if err := target.SyncAccountManagementState(nil); !errors.Is(err, failure) {
				t.Fatalf("rebase fault: %v", err)
			}
			marker, err := target.readAccountManagedDataImportMarker()
			if err != nil || marker == nil || len(marker.ReplayStateEnvelope) == 0 || len(marker.ReplayDataEnvelope) == 0 {
				t.Fatalf("merge target was not persisted: %v", err)
			}
			if target.GetAccountManagementStatus().PendingChanges != 1 {
				t.Fatal("unacknowledged local edit was lost")
			}
			provider.importErr = nil
			if cold {
				target.accountBackgroundWG.Wait()
				restarted, restartProvider := managedImportTestManager(t, remote)
				restarted.db = target.db
				if err := restarted.initDB(); err != nil {
					t.Fatal(err)
				}
				if _, err := restarted.UnlockWallet("password"); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
					t.Fatalf("cold authentication must keep PWA closed until import completes: %v", err)
				}
				target, provider = restarted, restartProvider
			}
			if err := target.SyncAccountManagementState(nil); err != nil {
				t.Fatalf("rebase resume: %v", err)
			}
			if len(provider.payloads) != 1 || string(provider.payloads[0].Payload) != "Remote merged data" {
				t.Fatal("rebase resume lost remote provider data")
			}
			if target.GetWalletCatalog()[0].Name != "Local merged name" {
				t.Fatal("rebase resume lost local catalog edit")
			}
			if target.GetAccountManagementStatus().PendingChanges != 0 {
				t.Fatal("resumed merge was not ACKed")
			}
			if err := source.SyncAccountManagementState(nil); err != nil {
				t.Fatal(err)
			}
			if source.GetWalletCatalog()[0].Name != "Local merged name" {
				t.Fatal("merged catalog was not published")
			}
		})
	}
}

func TestFinalReviewRemoteResumeRejectsTamperedMarker(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	for _, corrupt := range []string{"version", "state-hash", "data-reference", "catalog", "ciphertext"} {
		t.Run(corrupt, func(t *testing.T) {
			remote := newRGB11MemoryDKVSHTTP()
			source, sourceProvider := managedImportTestSource(t, remote)
			target, provider := managedImportTestManager(t, remote)
			if err := restoreManagedImportTestWallet(t, target, source); err != nil {
				t.Fatal(err)
			}
			if err := target.UpdateWalletName(target.GetCurrentWalletId(), "merged"); err != nil {
				t.Fatal(err)
			}
			sourceProvider.payloads[0].Payload = []byte("new data")
			if err := source.SyncAccountManagementState(nil); err != nil {
				t.Fatal(err)
			}
			provider.importErr = errors.New("stop import")
			if err := target.SyncAccountManagementState(nil); !errors.Is(err, provider.importErr) {
				t.Fatal(err)
			}
			marker, err := target.readAccountManagedDataImportMarker()
			if err != nil || marker == nil {
				t.Fatal("missing marker")
			}
			switch corrupt {
			case "version":
				marker.Version++
			case "state-hash":
				marker.TargetStateHash = "wrong"
			case "data-reference":
				marker.TargetDataRevision++
			case "catalog":
				target.walletInfoMap[target.GetCurrentWalletId()].Name = "tampered"
			case "ciphertext":
				marker.ReplayDataEnvelope[len(marker.ReplayDataEnvelope)-1] ^= 1
			}
			if err := target.writeAccountManagedDataImportMarker(*marker); err != nil {
				t.Fatal(err)
			}
			before := map[string]string{}
			for key, record := range remote.records {
				before[key] = dkvsindexer.RecordHash(record).String()
			}
			provider.importErr = nil
			imports := provider.imports
			if err := target.SyncAccountManagementState(nil); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
				t.Fatalf("corrupted target resumed: %v", err)
			}
			if provider.imports != imports {
				t.Fatal("invalid target reached provider")
			}
			for key, hash := range before {
				if dkvsindexer.RecordHash(remote.records[key]).String() != hash {
					t.Fatal("invalid target overwrote server")
				}
			}
			if err := target.checkAccountManagedDataImport(); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
				t.Fatal("invalid marker was cleared")
			}
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

func TestFinalReviewRemoteImportMarkerDeleteCanRetry(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	remote := newRGB11MemoryDKVSHTTP()
	source, sourceProvider := managedImportTestSource(t, remote)
	target, provider := managedImportTestManager(t, remote)
	if err := restoreManagedImportTestWallet(t, target, source); err != nil {
		t.Fatal(err)
	}
	sourceProvider.payloads[0].Payload = []byte("committed data")
	if err := source.SyncAccountManagementState(nil); err != nil {
		t.Fatal(err)
	}
	database := target.db
	failure := errors.New("marker delete failed")
	target.db = &managedImportFaultDB{KVDB: database, deleteErr: failure}
	if err := target.SyncAccountManagementState(nil); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	target.db = database
	if string(provider.payloads[0].Payload) != "committed data" {
		t.Fatal("failure was not after import")
	}
	if err := target.checkAccountManagedDataImport(); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
		t.Fatal("missing marker")
	}
	imports := provider.imports
	if err := target.SyncAccountManagementState(nil); err != nil {
		t.Fatal(err)
	}
	if provider.imports != imports+1 || string(provider.payloads[0].Payload) != "committed data" {
		t.Fatal("retry did not idempotently import exact target")
	}
}

func TestFinalReviewIncompleteRestoreFreezesCatalog(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	remote := newRGB11MemoryDKVSHTTP()
	source, _ := managedImportTestSource(t, remote)
	target, provider := managedImportTestManager(t, remote)
	provider.importErr = errors.New("incomplete restore")
	if err := restoreManagedImportTestWallet(t, target, source); !errors.Is(err, provider.importErr) {
		t.Fatal(err)
	}
	before := target.GetWalletCatalog()
	root := target.GetAccountManagementStatus().RootWalletID
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{"create", func() error { _, _, err := target.CreateWallet("password"); return err }},
		{"import", func() error {
			_, err := target.ImportWallet("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "password")
			return err
		}},
		{"rename", func() error { return target.UpdateWalletName(root, "changed") }},
		{"ensure-account", func() error { return target.EnsureAccount(root, 1, "changed", "") }},
		{"metadata", func() error { return target.UpdateAccountMetadata(root, 0, "changed", "") }},
		{"switch-account", func() error { return target.SwitchAccount(1) }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
				t.Fatalf("mutation escaped protection: %v", err)
			}
			if !reflect.DeepEqual(before, target.GetWalletCatalog()) || target.GetAccountManagementStatus().PendingChanges != 0 {
				t.Fatal("rejected mutation changed catalog or pending")
			}
		})
	}
	provider.importErr = nil
	if err := restoreManagedImportTestWallet(t, target, source); err != nil {
		t.Fatal(err)
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

func TestFinalReviewRebasePartialProviderResumePreservesBothDevices(t *testing.T) {
	old := _chain
	_chain = "testnet"
	defer func() { _chain = old }()
	remote := newRGB11MemoryDKVSHTTP()
	source, remoteProvider := managedImportTestSource(t, remote)
	secondSource := &managedImportTestProvider{accountManagedDataProviderStub: accountManagedDataProviderStub{id: "ztest", payloads: []AccountManagedDataPayload{{Scope: AccountManagedDataGlobalScope, Payload: []byte("base")}}}}
	if err := source.RegisterAccountManagedDataProvider(secondSource); err != nil {
		t.Fatal(err)
	}
	if err := source.SyncAccountManagementState(nil); err != nil {
		t.Fatal(err)
	}
	target, firstTarget := managedImportTestManager(t, remote)
	secondTarget := &managedImportTestProvider{accountManagedDataProviderStub: accountManagedDataProviderStub{id: "ztest"}}
	if err := target.RegisterAccountManagedDataProvider(secondTarget); err != nil {
		t.Fatal(err)
	}
	if err := restoreManagedImportTestWallet(t, target, source); err != nil {
		t.Fatal(err)
	}
	secondTarget.payloads[0].Payload = []byte("local change")
	if err := target.UpdateWalletName(target.GetCurrentWalletId(), "Local name"); err != nil {
		t.Fatal(err)
	}
	remoteProvider.payloads[0].Payload = []byte("remote change")
	if err := source.SyncAccountManagementState(nil); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("second provider failed after first import")
	secondTarget.importErr = failure
	if err := target.SyncAccountManagementState(nil); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if string(firstTarget.payloads[0].Payload) != "remote change" {
		t.Fatal("test did not partially import first provider")
	}
	target.accountBackgroundWG.Wait()
	restarted, firstRestart := managedImportTestManager(t, remote)
	secondRestart := &managedImportTestProvider{accountManagedDataProviderStub: accountManagedDataProviderStub{id: "ztest"}}
	if err := restarted.RegisterAccountManagedDataProvider(secondRestart); err != nil {
		t.Fatal(err)
	}
	restarted.db = target.db
	if err := restarted.initDB(); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.UnlockWallet("password"); !errors.Is(err, ErrAccountManagedDataImportIncomplete) {
		t.Fatalf("PWA session opened before merged provider import completed: %v", err)
	}
	if err := restarted.SyncAccountManagementState(nil); err != nil {
		t.Fatal(err)
	}
	if string(firstRestart.payloads[0].Payload) != "remote change" || string(secondRestart.payloads[0].Payload) != "local change" {
		t.Fatal("resume lost remote or local provider data")
	}
	if err := source.SyncAccountManagementState(nil); err != nil {
		t.Fatal(err)
	}
	if string(secondSource.payloads[0].Payload) != "local change" || string(remoteProvider.payloads[0].Payload) != "remote change" {
		t.Fatal("merged data did not converge")
	}
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
