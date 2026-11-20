package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// Exercise the public SDK entry points used by PWA. Recovery must discover the
// root wrapper; these cases do not inject a recovered secret or decoded state.
func TestSDKAccountPWAEntryPoints(t *testing.T) {
	network := newDKVSNoPluginUnfundedNetwork(t)
	waitForDKVSPeerReady(t, network)
	t.Run("UnavailableDiscoveryCannotAuthorizeNewAccountSecret", func(t *testing.T) {
		config, _ := accountReviewConfig(t, network)
		target, err := url.Parse("http://" + config.IndexerL2.Host)
		require.NoError(t, err)
		proxy := httputil.NewSingleHostReverseProxy(target)
		var unavailable atomic.Bool
		var requests atomic.Int32
		unavailable.Store(true)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/active/sync") {
				requests.Add(1)
				if unavailable.Load() {
					http.Error(w, "controlled first discovery unavailable", http.StatusServiceUnavailable)
					return
				}
			}
			proxy.ServeHTTP(w, r)
		}))
		t.Cleanup(server.Close)
		config.IndexerL2.Host = strings.TrimPrefix(server.URL, "http://")
		db := indexerdb.NewKVDB(t.TempDir())
		require.NotNil(t, db)
		manager := wallet.NewManager(config, db)
		t.Cleanup(func() { manager.Close(); db.Close() })
		mnemonic := accountSyncMnemonic(t, 207)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// This covers the SDK discovery boundary. The real PWA import-page
		// test separately observes that its orchestrator never calls ImportWallet
		// while discovery is pending; raw ImportWallet is not that orchestrator.
		for attempt := 0; attempt < 2; attempt++ {
			before := requests.Load()
			_, err = manager.RecoverAccountManagementFromRootMnemonic(ctx, mnemonic, accountReviewPassword)
			require.ErrorIs(t, err, wallet.ErrRootAccountDiscoveryPending)
			require.NotErrorIs(t, err, wallet.ErrRootAccountNotFound)
			require.Greater(t, requests.Load(), before, "each unfinished attempt must contact the same node")
			require.Empty(t, manager.GetWalletCatalog())
			status := manager.GetAccountManagementStatus()
			require.False(t, status.Active)
			require.False(t, status.RecoveryConfigured)
			require.False(t, status.ActivationPending)
			require.False(t, status.DKVSWriteAllowed)
			_, err = manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
			require.Error(t, err, "unfinished first discovery must not authorize an account secret")
			pending, err := manager.PendingAccountStorageAuthorization()
			require.ErrorIs(t, err, wallet.ErrAccountStorageAuthorizationMissing)
			require.Nil(t, pending)
			require.Empty(t, manager.GetWalletCatalog())
		}
		unavailable.Store(false)
		before := requests.Load()
		_, err = manager.RecoverAccountManagementFromRootMnemonic(ctx, mnemonic, accountReviewPassword)
		require.ErrorIs(t, err, wallet.ErrRootAccountNotFound)
		require.Greater(t, requests.Load(), before, "not-found requires one successful online confirmation")
		require.Empty(t, manager.GetWalletCatalog())
		confirmed := requests.Load()
		walletID, err := manager.ImportWallet(mnemonic, accountReviewPassword)
		require.NoError(t, err)
		require.Equal(t, walletID, manager.GetAccountManagementStatus().RootWalletID)
		require.False(t, manager.GetAccountManagementStatus().RecoveryConfigured)
		require.Equal(t, confirmed, requests.Load(), "import must reuse the completed discovery")
		_, err = manager.RefreshAccount(ctx)
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			return requests.Load() > confirmed && manager.GetAccountManagementStatus().DiscoveryStatus == "inactive"
		}, 5*time.Second, 20*time.Millisecond, "explicit retry must verify the previously absent root online")
	})

	t.Run("OfflineWalletIsReadOnly", func(t *testing.T) {
		config, _ := accountReviewConfig(t, network)
		target, err := url.Parse("http://" + config.IndexerL2.Host)
		require.NoError(t, err)
		proxy := httputil.NewSingleHostReverseProxy(target)
		var offline atomic.Bool
		var offlineRequests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if offline.Load() {
				if strings.Contains(r.URL.Path, "/v3/dkvs/") {
					offlineRequests.Add(1)
				}
				http.Error(w, "controlled node unavailable after confirmation", http.StatusServiceUnavailable)
				return
			}
			proxy.ServeHTTP(w, r)
		}))
		t.Cleanup(server.Close)
		config.IndexerL2.Host = strings.TrimPrefix(server.URL, "http://")
		dir := t.TempDir()
		db := indexerdb.NewKVDB(dir)
		require.NotNil(t, db)
		manager := wallet.NewManager(config, db)
		t.Cleanup(func() { manager.Close(); db.Close() })
		mnemonic := accountSyncMnemonic(t, 210)
		_, err = manager.RecoverAccountManagementFromRootMnemonic(context.Background(), mnemonic, accountReviewPassword)
		require.ErrorIs(t, err, wallet.ErrRootAccountNotFound)
		walletID, err := manager.ImportWallet(mnemonic, accountReviewPassword)
		require.NoError(t, err)
		require.True(t, manager.GetAccountManagementStatus().LocalOnly)
		identity := manager.GetAccountManagementStatus().AccountID
		before := manager.GetWalletCatalog()
		// A second root lookup uses the same node. Its real transport failure
		// must revoke the running wallet's previously confirmed local access.
		offline.Store(true)
		_, err = manager.RecoverAccountManagementFromRootMnemonic(context.Background(), accountSyncMnemonic(t, 211), accountReviewPassword)
		require.ErrorIs(t, err, wallet.ErrRootAccountDiscoveryPending)
		require.Positive(t, offlineRequests.Load())
		require.False(t, manager.GetAccountManagementStatus().WriteAllowed)
		require.ErrorIs(t, manager.UpdateWalletName(walletID, "offline rename"), wallet.ErrRootAccountDiscoveryPending)
		require.Equal(t, before, manager.GetWalletCatalog())
		offline.Store(false)
		requireE2EInactive(t, manager, accountReviewPassword)
		manager.Close()
		db.Close()
		offlineRequests.Store(0)
		offline.Store(true)
		db = indexerdb.NewKVDB(dir)
		require.NotNil(t, db)
		manager = wallet.NewManager(config, db)
		require.NotNil(t, manager)
		manager.Start()
		_, err = manager.UnlockWallet(accountReviewPassword)
		require.NoError(t, err)
		require.Equal(t, before, manager.GetWalletCatalog())
		status := manager.GetAccountManagementStatus()
		require.True(t, status.Active, "minimum local identity is not service activation")
		require.Equal(t, walletID, status.RootWalletID)
		require.Equal(t, identity, status.AccountID)
		require.False(t, status.LocalOnly)
		require.False(t, status.WriteAllowed)
		require.False(t, status.RecoveryConfigured)
		require.False(t, status.ActivationPending)
		require.False(t, status.DKVSWriteAllowed)
		view, err := manager.GetAccountView(context.Background())
		require.NoError(t, err)
		require.Equal(t, "notConfigured", view.BackupRisk, "offline local use must retain the missing-backup warning")
		require.ErrorIs(t, manager.UpdateWalletName(walletID, "offline rename"), wallet.ErrRootAccountDiscoveryPending)
		require.Equal(t, before, manager.GetWalletCatalog())
		require.ErrorIs(t, manager.SyncAccountManagementState(context.Background()), wallet.ErrDKVSPathNotSynced)
		require.Eventually(t, func() bool { return offlineRequests.Load() > 0 }, 5*time.Second, 20*time.Millisecond)
		_, err = manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
		require.Error(t, err, "unavailable storage policy must not prepare an activation or remote write")
		pending, err := manager.PendingAccountStorageAuthorization()
		require.ErrorIs(t, err, wallet.ErrAccountStorageAuthorizationMissing)
		require.Nil(t, pending)
		require.Equal(t, before, manager.GetWalletCatalog(), "rejected operations must not replace local identity/catalog")
		after := manager.GetAccountManagementStatus()
		require.Equal(t, status.AccountID, after.AccountID)
		require.False(t, after.RecoveryConfigured)
		require.False(t, after.ActivationPending)
		offline.Store(false)
		requireE2EInactive(t, manager, accountReviewPassword)
		require.NoError(t, manager.UpdateWalletName(walletID, "online rename"))
		require.Equal(t, "online rename", manager.GetWalletCatalog()[0].Name)
	})

	t.Run("CreateFirstWalletAutomaticallyInitializesAccount", func(t *testing.T) {
		manager, _ := accountReviewDevice(t, network, "")
		id, mnemonic, err := manager.CreateWallet(accountReviewPassword)
		require.NoError(t, err)
		require.NotEmpty(t, mnemonic)
		status := manager.GetAccountManagementStatus()
		require.True(t, status.Active)
		require.False(t, status.RecoveryConfigured)
		require.Equal(t, id, status.RootWalletID)
		require.Equal(t, dkvs.AccountID(manager.GetWallet().GetPubKey().SerializeCompressed()), status.AccountID)
		_, err = manager.AccountPreflight("wrong-password", nil)
		require.Error(t, err)
		preflight, err := manager.AccountPreflight(accountReviewPassword, nil)
		require.NoError(t, err)
		require.Equal(t, status.AccountID, preflight.AccountID)
	})

	t.Run("AuthoritativeNotFoundThenImportInitializesOnlyThatRoot", func(t *testing.T) {
		manager, _ := accountReviewDevice(t, network, "")
		mnemonic := accountSyncMnemonic(t, 201)
		_, err := manager.RecoverAccountManagementFromRootMnemonic(context.Background(), mnemonic, accountReviewPassword)
		require.ErrorIs(t, err, wallet.ErrRootAccountNotFound)
		require.Empty(t, manager.GetWalletCatalog())
		require.False(t, manager.GetAccountManagementStatus().Active)
		require.ErrorIs(t, manager.StartDKVSSync(), wallet.ErrDKVSPathNotSynced,
			"authoritative NotFound has no configured backup worker; discovery is explicit")
		require.Eventually(t, func() bool {
			_, err = manager.RecoverAccountManagementFromRootMnemonic(context.Background(), mnemonic, accountReviewPassword)
			return errors.Is(err, wallet.ErrRootAccountNotFound)
		}, 30*time.Second, 100*time.Millisecond)
		id, err := manager.ImportWallet(mnemonic, accountReviewPassword)
		require.NoError(t, err)
		require.True(t, manager.GetAccountManagementStatus().Active)
		require.Equal(t, id, manager.GetAccountManagementStatus().RootWalletID)
	})
	if t.Failed() {
		return
	}

	f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 202), true)
	f.activate(t)
	rootID := f.manager.GetAccountManagementStatus().RootWalletID
	childID, _, err := f.manager.CreateWallet(accountReviewPassword)
	require.NoError(t, err)
	require.NoError(t, f.manager.UpdateWalletName(childID, "Created after activation"))
	require.NoError(t, f.manager.EnsureAccount(childID, 1, "Before rename", "did:pwa:child"))
	require.NoError(t, f.manager.UpdateAccountMetadata(childID, 1, "Travel account", "did:pwa:travel"))
	require.NoError(t, f.manager.UpdateWalletName(rootID, "Latest root"))
	require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))

	t.Run("RootMnemonicRestoresLatestCatalogWithNewLocalPassword", func(t *testing.T) {
		fresh, _ := accountReviewDevice(t, network, "")
		const newPassword = "pwa-new-device-password"
		requireAccountPWARootRecovery(t, fresh, f.rootMnemonic, newPassword)
		// Explicit root recovery configures this account before its ordinary
		// backup worker starts.
		require.NoError(t, fresh.StartDKVSSync())
		require.Equal(t, f.manager.GetWalletCatalog()[0].Fingerprint, fresh.GetWalletCatalog()[0].Fingerprint)
		require.Equal(t, "Latest root", fresh.GetWalletCatalog()[0].Name)
		require.Len(t, fresh.GetWalletCatalog(), 2)
		child := accountReviewFind(t, fresh, f.manager.GetWalletCatalog()[1].Fingerprint)
		require.Equal(t, "Created after activation", child.Name)
		require.Equal(t, "Travel account", child.Accounts[1].Name)
		require.Equal(t, "did:pwa:travel", child.Accounts[1].DID)
		require.Equal(t, f.manager.GetAccountManagementStatus().AccountID, fresh.GetAccountManagementStatus().AccountID)
		for _, entry := range fresh.GetWalletCatalog() {
			require.Empty(t, fresh.GetMnemonic(entry.ID, accountReviewPassword))
			require.NotEmpty(t, fresh.GetMnemonic(entry.ID, newPassword))
		}
	})

	t.Run("CurrentWalletDiscoveryUsesRootDespiteChildSelection", func(t *testing.T) {
		before := f.manager.GetWalletCatalog()
		_, err := f.manager.RecoverAccountManagementFromCurrentWallet(context.Background(), "wrong-password")
		require.Error(t, err)
		require.Equal(t, before, f.manager.GetWalletCatalog())
		_, err = f.manager.RecoverAccountManagementFromCurrentWallet(context.Background(), accountReviewPassword)
		require.NoError(t, err)
		require.Equal(t, rootID, f.manager.GetAccountManagementStatus().RootWalletID)
		require.Equal(t, before, f.manager.GetWalletCatalog())
		require.NoError(t, f.manager.UpdateAccountMetadata(childID, 1, "Pending rename", "did:pwa:pending"))
		_, err = f.manager.RecoverAccountManagementFromCurrentWallet(context.Background(), accountReviewPassword)
		require.NoError(t, err)
		require.Equal(t, "Pending rename", accountReviewFind(t, f.manager, before[1].Fingerprint).Accounts[1].Name)
		require.Zero(t, f.manager.GetAccountManagementStatus().PendingChanges)
	})

	t.Run("PublicLocatorAuthenticatesStorageAndNetwork", func(t *testing.T) {
		locator := wallet.AccountPublicLocator{Version: account.Version, Network: "testnet",
			StorageLocation: f.location, StorageMode: f.authorization.Mode, RecordTTL: f.authorization.RecordOptions.TTL,
			Locator: f.pkg.Envelope.Locator}
		require.NoError(t, f.manager.SignAccountPublicLocator(&locator))
		encoded, err := wallet.EncodeAccountPublicLocator(locator, "testnet")
		require.NoError(t, err)
		decoded, err := wallet.DecodeAccountPublicLocator(encoded, "testnet")
		require.NoError(t, err)
		require.Equal(t, locator, decoded)
		_, err = wallet.DecodeAccountPublicLocator(encoded, "mainnet")
		require.Error(t, err)
		locator.StorageLocation.Host = "different-core"
		require.Error(t, wallet.VerifyAccountPublicLocator(locator, "testnet"))
	})
	t.Run("ActiveAccountCannotBeReplacedByAnotherRoot", func(t *testing.T) {
		other := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 206), true)
		other.activate(t)
		otherRoot := keyFromMnemonic(t, other.rootMnemonic, 0)
		wrapperKey, err := dkvs.PersonalKey(otherRoot.PubKey().SerializeCompressed(), "account/root")
		require.NoError(t, err)
		_, err = dkvsClientForNode(t, network.Core).GetRecordDirect(wrapperKey)
		require.NoError(t, err, "the other account really exists; an unsynchronized local directory cannot establish not-found")
		before := f.manager.GetWalletCatalog()
		identity := f.manager.GetAccountManagementStatus().AccountID
		_, err = f.manager.RecoverAccountManagementFromRootMnemonic(context.Background(), other.rootMnemonic, accountReviewPassword)
		require.ErrorIs(t, err, wallet.ErrRootAccountWrapperInvalid)
		require.NotErrorIs(t, err, wallet.ErrRootAccountNotFound)
		require.Equal(t, identity, f.manager.GetAccountManagementStatus().AccountID)
		require.Equal(t, before, f.manager.GetWalletCatalog())
	})
}

func requireAccountPWARootRecovery(t *testing.T, manager *wallet.Manager, mnemonic, password string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Recovery uses the manager's database. Keep each attempt on the test
	// goroutine so cleanup cannot close the database while a timed-out
	// Eventually condition is still recovering in the background.
	var lastErr error
	for ctx.Err() == nil {
		_, lastErr = manager.RecoverAccountManagementFromRootMnemonic(ctx, mnemonic, password)
		if lastErr == nil {
			return
		}
		if ctx.Err() != nil {
			break
		}
		if !errors.Is(lastErr, wallet.ErrRootAccountDiscoveryPending) {
			t.Fatalf("root discovery failed: %v", lastErr)
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	t.Fatalf("root discovery did not complete: %v (last error: %v)", ctx.Err(), lastErr)
}

func TestSDKAccountPWAOfflineRestart(t *testing.T) {
	network := newDKVSNoPluginUnfundedNetwork(t)
	waitForDKVSPeerReady(t, network)
	f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 203), true)
	f.activate(t)
	config, _ := accountReviewConfig(t, network)
	target, err := url.Parse("http://" + config.IndexerL2.Host)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(target)
	accountPrefix := "/personal/" + f.manager.GetAccountManagementStatus().AccountID + "/account"
	var accountSyncRequests atomic.Int32
	var accountPrefixSyncs atomic.Int32
	var otherPrefixSyncs atomic.Int32
	var unreadableSyncRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/v3/dkvs/active/sync") {
			accountSyncRequests.Add(1)
			body, readErr := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if readErr != nil {
				unreadableSyncRequests.Add(1)
			} else {
				var request struct {
					Scope struct {
						Prefix string `json:"prefix"`
					} `json:"scope"`
				}
				if json.Unmarshal(body, &request) != nil {
					unreadableSyncRequests.Add(1)
				} else if request.Scope.Prefix == accountPrefix {
					accountPrefixSyncs.Add(1)
				} else {
					otherPrefixSyncs.Add(1)
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	config.IndexerL2.Host = strings.TrimPrefix(server.URL, "http://")
	dir := t.TempDir()
	db := indexerdb.NewKVDB(dir)
	require.NotNil(t, db)
	manager := wallet.NewManager(config, db)
	t.Cleanup(func() { manager.Close(); db.Close() })
	hold := newAccountStateHold(false)
	t.Cleanup(hold.unblock)
	transport := &accountBackupHTTP{inner: wallet.NewHTTPClient(), holds: []*accountStateHold{hold}}
	manager.SetDKVSHttpClient(transport)
	var coldRestartDiagActive atomic.Bool
	var beforeRuntimeRestartSyncRequests, beforeRuntimeRestartPrefixSyncs int32
	logRestartStatus := func(stage string) {
		status := manager.GetAccountManagementStatus()
		t.Logf("configured-account restart status: stage=%s account_state=%s active=%t recovery_configured=%t activation_pending=%t write_allowed=%t dkvs_write_allowed=%t dkvs_status=%s discovery_status=%s sync_error_code=%s pending_changes=%d pending_requests=%d",
			stage, status.AccountState, status.Active, status.RecoveryConfigured, status.ActivationPending,
			status.WriteAllowed, status.DKVSWriteAllowed, status.DKVSStatus, status.DiscoveryStatus,
			status.LastDKVSSyncErrorCode, status.PendingChanges, status.DKVSPendingRequests)
	}
	t.Cleanup(func() {
		if !coldRestartDiagActive.Load() || !t.Failed() {
			return
		}
		status := manager.GetAccountManagementStatus()
		t.Logf("configured-account cold-unlock failure status: account_state=%s active=%t recovery_configured=%t activation_pending=%t write_allowed=%t dkvs_write_allowed=%t dkvs_status=%s discovery_status=%s sync_error_code=%s pending_changes=%d pending_requests=%d",
			status.AccountState, status.Active, status.RecoveryConfigured, status.ActivationPending,
			status.WriteAllowed, status.DKVSWriteAllowed, status.DKVSStatus, status.DiscoveryStatus,
			status.LastDKVSSyncErrorCode, status.PendingChanges, status.DKVSPendingRequests)
		t.Logf("configured-account cold-unlock sync requests: total=%d matching_account_prefix=%d other_prefix=%d unreadable=%d; delta_total=%d delta_matching_prefix=%d",
			accountSyncRequests.Load(), accountPrefixSyncs.Load(), otherPrefixSyncs.Load(), unreadableSyncRequests.Load(),
			accountSyncRequests.Load()-beforeRuntimeRestartSyncRequests,
			accountPrefixSyncs.Load()-beforeRuntimeRestartPrefixSyncs)
	})
	requireAccountPWARootRecovery(t, manager, f.rootMnemonic, accountReviewPassword)
	require.True(t, manager.GetAccountManagementStatus().RecoveryConfigured,
		"the backup worker starts only after explicit root recovery configures this account")
	require.NoError(t, manager.StartDKVSSync())
	require.Eventually(t, func() bool { return manager.GetAccountManagementStatus().DKVSWriteAllowed }, 30*time.Second, 50*time.Millisecond)
	// No pending edit or remote change may be needed to restore account write
	// permission after restarting either the sync worker or the whole runtime.
	requireE2EFirstBackup(t, manager)
	recoveryAccountID := manager.GetAccountManagementStatus().AccountID
	require.NoError(t, manager.SwitchAccount(2))
	require.Equal(t, uint32(2), manager.GetCurrentAccountId(), "the existing sub-account must remain selected")
	require.Equal(t, recoveryAccountID, manager.GetAccountManagementStatus().AccountID,
		"switching sub-accounts must retain the root-derived account identity")
	require.Equal(t, "/personal/"+recoveryAccountID+"/account", accountPrefix,
		"the sync prefix is determined by the root account, not its selected sub-account")
	reconnectCatalog := manager.GetWalletCatalog()
	manager.StopDKVSSync()
	require.NoError(t, manager.StartDKVSSync())
	require.NoError(t, manager.StartDKVSSync())
	require.Eventually(t, func() bool {
		return manager.GetAccountManagementStatus().WriteAllowed
	}, 30*time.Second, 50*time.Millisecond, "sync restart must recover account permission without a manual refresh")
	require.Equal(t, reconnectCatalog, manager.GetWalletCatalog())
	requireE2EFirstBackup(t, manager)
	beforeRuntimeRestartSyncRequests = accountSyncRequests.Load()
	beforeRuntimeRestartPrefixSyncs = accountPrefixSyncs.Load()
	logRestartStatus("before_runtime_stop")
	coldRestartDiagActive.Store(true)
	manager.Stop()
	manager.Start()
	require.Equal(t, "locked", manager.GetAccountManagementStatus().AccountState)
	require.False(t, manager.GetAccountManagementStatus().WriteAllowed)
	logRestartStatus("after_start_before_unlock")
	_, resumeErr := manager.UnlockWallet(accountReviewPassword)
	require.NoError(t, resumeErr)
	logRestartStatus("immediately_after_unlock")
	require.Eventually(t, func() bool {
		return manager.GetAccountManagementStatus().WriteAllowed
	}, 30*time.Second, 50*time.Millisecond,
		"runtime restart must recover configured-account permission")
	require.Eventually(t, func() bool {
		return accountPrefixSyncs.Load() > beforeRuntimeRestartPrefixSyncs
	}, 30*time.Second, 50*time.Millisecond,
		"configured-account cold unlock must align its /personal/<accountID>/account prefix")
	require.Equal(t, reconnectCatalog, manager.GetWalletCatalog())
	coldRestartDiagActive.Store(false)
	transport.armed.Store(true)
	root := manager.GetWalletCatalog()[0]
	require.NoError(t, manager.UpdateWalletName(root.ID, "Unsynced before restart"))
	require.NoError(t, manager.UpdateAccountMetadata(root.ID, 2, "Offline savings", "did:pwa:offline"))
	require.Greater(t, manager.GetAccountManagementStatus().PendingChanges, 0)
	select {
	case <-hold.entered:
	case <-time.After(20 * time.Second):
		t.Fatal("local edit never reached the backup submission")
	}
	transport.offline.Store(true)
	hold.unblock()
	manager.StopDKVSSync()
	manager.Close()
	db.Close()
	db = indexerdb.NewKVDB(dir)
	require.NotNil(t, db)
	manager = wallet.NewManager(config, db)
	manager.SetDKVSHttpClient(transport)
	require.Nil(t, manager.GetWallet())
	_, err = manager.UnlockWallet(accountReviewPassword)
	require.NoError(t, err)
	require.Equal(t, "Unsynced before restart", manager.GetWalletCatalog()[0].Name)
	require.Greater(t, manager.GetAccountManagementStatus().PendingChanges, 0)
	before := manager.GetWalletCatalog()
	require.Error(t, manager.UpdateWalletName(before[0].ID, "must remain read only"))
	require.Equal(t, before, manager.GetWalletCatalog())
	transport.offline.Store(false)
	require.NoError(t, manager.StartDKVSSync())
	require.Eventually(t, func() bool { return manager.GetAccountManagementStatus().DKVSWriteAllowed }, 30*time.Second, 50*time.Millisecond)
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
	require.Equal(t, "Unsynced before restart", f.manager.GetWalletCatalog()[0].Name)
	require.Equal(t, "Offline savings", f.manager.GetWalletCatalog()[0].Accounts[2].Name)
	require.Equal(t, "did:pwa:offline", f.manager.GetWalletCatalog()[0].Accounts[2].DID)
	require.Zero(t, manager.GetAccountManagementStatus().PendingChanges)
}

// The proxy forwards the production request to the real node and loses only
// its first successful ACK. Reopening the SDK must retry the original signed
// request, including its RequestID, authorization and CAS preconditions.
func TestSDKAccountPWACommitResponseLossRestart(t *testing.T) {
	network := newDKVSNoPluginUnfundedNetwork(t)
	waitForDKVSPeerReady(t, network)
	f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 208), true)
	f.activate(t)
	config, _ := accountReviewConfig(t, network)
	target, err := url.Parse("http://" + config.IndexerL2.Host)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(target)
	var loseACK atomic.Bool
	ackHeld, ackRelease := make(chan struct{}), make(chan struct{})
	ackCanceled := make(chan struct{})
	var releaseOnce sync.Once
	releaseACK := func() { releaseOnce.Do(func() { close(ackRelease) }) }
	t.Cleanup(releaseACK)
	var requestMu sync.Mutex
	var requests [][]byte
	proxy.ModifyResponse = func(response *http.Response) error {
		if strings.HasSuffix(response.Request.URL.Path, "/records/batch-cas") && response.StatusCode == http.StatusOK &&
			response.Request.Header.Get("X-E2E-Account-State") == "1" && loseACK.Swap(false) {
			close(ackHeld)
			select {
			case <-ackRelease:
			case <-response.Request.Context().Done():
				close(ackCanceled)
				return response.Request.Context().Err()
			case <-time.After(20 * time.Second):
				return context.DeadlineExceeded
			}
			response.Body.Close()
			response.StatusCode = http.StatusServiceUnavailable
			body := []byte(`{"code":-1,"msg":"controlled ACK loss after real commit"}`)
			response.Body = io.NopCloser(bytes.NewReader(body))
			response.ContentLength = int64(len(body))
			response.Header.Del("Content-Length")
		}
		return nil
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/records/batch-cas") {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "read request", http.StatusBadRequest)
				return
			}
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			var batch struct {
				Mutations []struct{ Record struct{ Key string } }
			}
			if json.Unmarshal(body, &batch) == nil {
				for _, mutation := range batch.Mutations {
					if strings.HasSuffix(mutation.Record.Key, "/account/state") {
						r.Header.Set("X-E2E-Account-State", "1")
						requestMu.Lock()
						requests = append(requests, bytes.Clone(body))
						requestMu.Unlock()
						break
					}
				}
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	defer server.Close()
	config.IndexerL2.Host = strings.TrimPrefix(server.URL, "http://")
	dir := t.TempDir()
	db := indexerdb.NewKVDB(dir)
	require.NotNil(t, db)
	manager := wallet.NewManager(config, db)
	t.Cleanup(func() { manager.Close(); db.Close() })
	requireAccountPWARootRecovery(t, manager, f.rootMnemonic, accountReviewPassword)
	require.NoError(t, manager.StartDKVSSync())
	require.Eventually(t, func() bool { return manager.GetAccountManagementStatus().DKVSWriteAllowed }, 30*time.Second, 50*time.Millisecond)
	requestMu.Lock()
	requests = nil
	requestMu.Unlock()
	loseACK.Store(true)
	require.NoError(t, manager.UpdateWalletName(manager.GetWalletCatalog()[0].ID, "Committed without client ACK"))
	select {
	case <-ackHeld:
	case <-time.After(20 * time.Second):
		t.Fatal("real server commit did not reach the held ACK")
	}
	// Stop after the business edit and server commit, while only its response
	// is held. This prevents a warm retry from replacing the cold-start proof.
	stopped := make(chan struct{})
	go func() { manager.StopDKVSSync(); close(stopped) }()
	select {
	case <-ackCanceled:
	case <-time.After(5 * time.Second):
		t.Fatal("stopping account sync did not cancel its held HTTP request")
	}
	releaseACK()
	select {
	case <-stopped:
	case <-time.After(20 * time.Second):
		t.Fatal("account worker did not stop after ACK loss")
	}
	require.False(t, loseACK.Load(), "the node committed before ACK loss")
	stateKey, err := dkvs.PersonalKey(manager.GetWallet().GetPubKey().SerializeCompressed(), "account/state")
	require.NoError(t, err)
	committed := waitForDKVSRecord(t, network.Core, stateKey)
	requestMu.Lock()
	committedRequest := bytes.Clone(requests[0])
	requestMu.Unlock()
	var committedBatch dkvscore.BatchCASRequest
	require.NoError(t, json.Unmarshal(committedRequest, &committedBatch))
	for _, mutation := range committedBatch.Mutations {
		if mutation.Record.Key == stateKey {
			require.Equal(t, dkvs.RecordHash(mutation.Record), dkvs.RecordHash(committed), "the lost response must follow a committed state")
		}
	}
	manager.Close()
	db.Close()
	db = indexerdb.NewKVDB(dir)
	require.NotNil(t, db)
	manager = wallet.NewManager(config, db)
	_, err = manager.UnlockWallet(accountReviewPassword)
	require.NoError(t, err)
	require.NoError(t, manager.StartDKVSSync())
	require.Eventually(t, func() bool {
		return manager.SyncAccountManagementState(context.Background()) == nil &&
			manager.GetAccountManagementStatus().PendingChanges == 0
	}, 30*time.Second, 100*time.Millisecond)
	requestMu.Lock()
	captured := append([][]byte(nil), requests...)
	requestMu.Unlock()
	require.GreaterOrEqual(t, len(captured), 2)
	var original, retry dkvscore.BatchCASRequest
	require.NoError(t, json.Unmarshal(captured[0], &original))
	require.NoError(t, json.Unmarshal(captured[1], &retry))
	require.Equal(t, original.RequestID, retry.RequestID)
	require.Equal(t, original.EndpointID, retry.EndpointID)
	require.Equal(t, original.Authorization, retry.Authorization)
	require.Len(t, retry.Mutations, len(original.Mutations))
	for i, mutation := range original.Mutations {
		require.NotNil(t, mutation.Record)
		require.NotNil(t, retry.Mutations[i].Record)
		// Binary record hash includes the signature. Empty byte slices may
		// encode as null or "" in JSON after durable binary decoding.
		require.Equal(t, dkvs.RecordHash(mutation.Record), dkvs.RecordHash(retry.Mutations[i].Record))
		require.Equal(t, mutation.ExpectedETag, retry.Mutations[i].ExpectedETag)
		require.Equal(t, mutation.ExpectAbsent, retry.Mutations[i].ExpectAbsent)
	}
	current := waitForDKVSRecord(t, network.Core, stateKey)
	require.Equal(t, dkvs.RecordHash(committed), dkvs.RecordHash(current), "retry must not create another revision (Seq %d -> %d)", committed.Seq, current.Seq)
	require.Zero(t, manager.GetAccountManagementStatus().PendingChanges)
	require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
	require.Equal(t, "Committed without client ACK", f.manager.GetWalletCatalog()[0].Name)
}
