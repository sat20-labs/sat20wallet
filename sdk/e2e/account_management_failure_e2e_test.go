package e2e

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errAccountE2EProfileWrite = errors.New("account E2E: injected profile write failure")

// Fault only local persistence. Account records, signing, RPC and recovery
// continue to use the real node and SDK. Establish the restored device's
// actual online baseline before enabling a local persistence fault.
type accountE2EProfileWriteDB struct {
	indexercommon.KVDB
	fail            atomic.Bool
	key             string
	failRead        atomic.Bool
	writes          atomic.Int64
	failures        atomic.Int64
	failFlush       bool
	moduleReceipt   bool
	cancelOnProfile context.CancelFunc
}

func (d *accountE2EProfileWriteDB) Write(key, value []byte) error {
	d.writes.Add(1)
	if d.fail.Load() && d.matchesWrite(key, value) {
		d.failures.Add(1)
		return errAccountE2EProfileWrite
	}
	return d.KVDB.Write(key, value)
}

func (d *accountE2EProfileWriteDB) matches(key []byte) bool {
	name := d.key
	if name == "" {
		name = wallet.GetDBKeyPrefix() + "account-management-profile-v1"
	}
	return string(key) == name
}

// The module completion receipt replaced the redundant profile write. Fault
// that same durable completion boundary without adding a production test API.
func (d *accountE2EProfileWriteDB) matchesWrite(key, value []byte) bool {
	if !d.moduleReceipt {
		return d.matches(key)
	}
	if !strings.HasPrefix(string(key), wallet.GetDBKeyPrefix()+"account/") {
		return false
	}
	var row struct {
		Provider  string
		Scope     string
		Restoring bool
	}
	return wallet.DecodeFromBytes(value, &row) == nil && row.Provider == "wallets" &&
		row.Scope == wallet.AccountManagedDataGlobalScope && !row.Restoring
}

func (d *accountE2EProfileWriteDB) Read(key []byte) ([]byte, error) {
	if d.failRead.Load() && d.matches(key) {
		return nil, errAccountE2EProfileWrite
	}
	return d.KVDB.Read(key)
}

func (d *accountE2EProfileWriteDB) NewWriteBatch() indexercommon.WriteBatch {
	return &accountE2EProfileWriteBatch{WriteBatch: d.KVDB.NewWriteBatch(), database: d, bulk: true}
}

// Keep the fault hook on the real underlying atomic transaction.
func (d *accountE2EProfileWriteDB) NewAtomicWriteBatch() indexercommon.WriteBatch {
	atomicDB, ok := d.KVDB.(indexercommon.AtomicWriteBatchDB)
	if !ok {
		return nil
	}
	batch := atomicDB.NewAtomicWriteBatch()
	if batch == nil {
		return nil
	}
	return &accountE2EProfileWriteBatch{WriteBatch: batch, database: d}
}

type accountE2EProfileWriteBatch struct {
	indexercommon.WriteBatch
	database *accountE2EProfileWriteDB
	profile  bool
	bulk     bool
}

func (b *accountE2EProfileWriteBatch) Put(key, value []byte) error {
	b.database.writes.Add(1)
	matched := b.database.matchesWrite(key, value)
	b.profile = b.profile || matched
	if matched && b.database.cancelOnProfile != nil {
		if err := b.WriteBatch.Put(key, value); err != nil {
			return err
		}
		// Exercise the permitted bulk rollover on this exact row, using the
		// real database rather than depending on its machine-specific size limit.
		if b.bulk {
			if err := b.WriteBatch.Flush(); err != nil {
				return err
			}
			b.WriteBatch.Close()
			b.WriteBatch = b.database.KVDB.NewWriteBatch()
		}
		b.database.cancelOnProfile()
		return nil
	}
	if b.database.fail.Load() && !b.database.failFlush && matched {
		b.database.failures.Add(1)
		return errAccountE2EProfileWrite
	}
	return b.WriteBatch.Put(key, value)
}

func (b *accountE2EProfileWriteBatch) Flush() error {
	b.database.writes.Add(1)
	if b.database.fail.Load() && b.database.failFlush && b.profile {
		b.database.failures.Add(1)
		return errAccountE2EProfileWrite
	}
	return b.WriteBatch.Flush()
}

func accountFailureRestore(t *testing.T, f *accountReviewFixture) *wallet.Manager {
	t.Helper()
	device, recovered := f.recover(t)
	_, err := device.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword,
		f.pkg.Envelope.Locator, f.restoreOptions())
	require.NoError(t, err)
	return device
}

func accountFailureReady(t *testing.T, device *wallet.Manager) {
	t.Helper()
	require.NoError(t, device.StartDKVSSync())
	t.Cleanup(device.StopDKVSSync)
	requireE2EFirstBackup(t, device)
	status := device.GetAccountManagementStatus()
	require.Equal(t, "active", status.DiscoveryStatus)
	require.True(t, status.WriteAllowed, "fault injection requires actual account write eligibility")
}

// Each subtest has a new root/account namespace and independent databases.
// A red regression must not prevent the remaining failure cases from running.
// Assert the intended behavior, never the current defect or an expected failure.
func TestSDKAccountFailureRegressions(t *testing.T) {
	network := newDKVSNoPluginUnfundedNetwork(t)
	waitForDKVSPeerReady(t, network)

	t.Run("StoppedDBBlocksFirstImport", func(t *testing.T) {
		source := prepareAccountReviewWithMnemonic(t, network, "", true)
		source.activate(t)
		cfg, _ := accountReviewConfig(t, network)
		phrase := accountSyncMnemonic(t, 233)
		root, err := wallet.ValidateMnemonicForCurrentNetwork(phrase, "")
		require.NoError(t, err)
		key, err := dkvs.AccountPersonalKey(root.AccountID, "account/root")
		require.NoError(t, err)
		proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: cfg.IndexerL2.Scheme, Host: cfg.IndexerL2.Host})
		var down atomic.Bool
		down.Store(true)
		var rejected atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if down.Load() && strings.HasSuffix(r.URL.Path, "/dkvs/active/sync") {
				body, err := io.ReadAll(r.Body)
				r.Body.Close()
				if err != nil {
					http.Error(w, "read root query", http.StatusBadRequest)
					return
				}
				if bytes.Contains(body, []byte(key)) {
					rejected.Add(1)
					http.Error(w, "temporary outage", http.StatusServiceUnavailable)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
			}
			proxy.ServeHTTP(w, r)
		}))
		t.Cleanup(server.Close)
		cfg.IndexerL2.Host = strings.TrimPrefix(server.URL, "http://")
		dir := t.TempDir()
		db := indexerdb.NewKVDB(dir)
		require.NotNil(t, db)
		fault := &accountE2EProfileWriteDB{KVDB: db}
		device := wallet.NewManager(cfg, fault)
		t.Cleanup(func() { device.Close(); db.Close() })
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		waiting := make(chan error, 1)
		go func() {
			_, err := device.WaitForImportRoot(ctx, phrase, accountReviewPassword)
			waiting <- err
		}()
		require.Eventually(t, func() bool { return rejected.Load() > 0 }, 5*time.Second, 10*time.Millisecond)
		fault.fail.Store(true)
		_, err = device.WaitForImportRoot(ctx, source.rootMnemonic, accountReviewPassword)
		require.ErrorIs(t, err, wallet.ErrAccountDBStopped)
		require.Positive(t, fault.failures.Load())
		fault.fail.Store(false)
		down.Store(false)
		select {
		case err := <-waiting:
			require.ErrorIs(t, err, wallet.ErrAccountDBStopped)
			require.ErrorIs(t, err, errAccountE2EProfileWrite)
		case <-ctx.Done():
			t.Fatal("first discovery continued after the database stopped")
		}
		_, err = device.WaitForImportRoot(ctx, phrase, accountReviewPassword)
		require.ErrorIs(t, err, wallet.ErrAccountDBStopped)
		require.ErrorIs(t, err, errAccountE2EProfileWrite)
		_, err = device.ImportVerifiedRoot(ctx, phrase, accountReviewPassword)
		require.ErrorIs(t, err, wallet.ErrAccountDBStopped, "a stopped database cannot accept a different first wallet")
		require.ErrorIs(t, err, errAccountE2EProfileWrite)
		require.Empty(t, device.GetWalletCatalog())
		device.Close()
		db.Close()
		db = indexerdb.NewKVDB(dir)
		require.NotNil(t, db)
		device = wallet.NewManager(cfg, db)
		require.Empty(t, device.GetWalletCatalog())
		_, err = db.Read([]byte(wallet.GetDBKeyPrefix() + "account-management-profile-v1"))
		require.ErrorIs(t, err, indexercommon.ErrKeyNotFound)
	})

	for _, found := range []bool{false, true} {
		name := "CancelAbsentBeforeCommit"
		if found {
			name = "CancelFoundBeforeCommit"
		}
		t.Run(name, func(t *testing.T) {
			phrase := accountSyncMnemonic(t, 232)
			if found {
				source := prepareAccountReviewWithMnemonic(t, network, "", true)
				source.activate(t)
				phrase = source.rootMnemonic
			}
			cfg, _ := accountReviewConfig(t, network)
			dir := t.TempDir()
			db := indexerdb.NewKVDB(dir)
			require.NotNil(t, db)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			device := wallet.NewManager(cfg, &accountE2EProfileWriteDB{KVDB: db, cancelOnProfile: cancel})
			t.Cleanup(func() { device.Close(); db.Close() })
			_, err := device.WaitForImportRoot(ctx, phrase, accountReviewPassword)
			if !found {
				require.ErrorIs(t, err, wallet.ErrRootAccountNotFound)
				_, err = device.ImportVerifiedRoot(ctx, phrase, accountReviewPassword)
			}
			require.ErrorIs(t, ctx.Err(), context.Canceled, "profile was queued in the real batch")
			require.ErrorIs(t, err, context.Canceled)
			require.Empty(t, device.GetWalletCatalog())
			device.Close()
			db.Close()
			db = indexerdb.NewKVDB(dir)
			require.NotNil(t, db)
			device = wallet.NewManager(cfg, db)
			require.Empty(t, device.GetWalletCatalog())
			_, err = db.Read([]byte(wallet.GetDBKeyPrefix() + "account-management-profile-v1"))
			require.ErrorIs(t, err, indexercommon.ErrKeyNotFound)
		})
	}

	for _, scenario := range []struct {
		name   string
		mutate func(*wallet.Manager, int64) error
	}{
		{"RenameProfileFailure", func(m *wallet.Manager, id int64) error { return m.UpdateWalletName(id, "Atomic rename") }},
		{"EnsureAccountProfileFailure", func(m *wallet.Manager, id int64) error {
			return m.EnsureAccount(id, 3, "Atomic account", "did:atomic:3")
		}},
		{"MetadataProfileFailure", func(m *wallet.Manager, id int64) error {
			return m.UpdateAccountMetadata(id, 0, "Atomic metadata", "did:atomic:0")
		}},
		{"CreateWalletProfileFailure", func(m *wallet.Manager, _ int64) error { _, _, err := m.CreateWallet(accountReviewPassword); return err }},
		{"ImportWalletProfileFailure", func(m *wallet.Manager, _ int64) error {
			_, err := m.ImportWallet(coreMnemonic, accountReviewPassword)
			return err
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := prepareAccountReviewWithMnemonic(t, network, "", true)
			f.activate(t)
			config, _ := accountReviewConfig(t, network)
			database := indexerdb.NewKVDB(t.TempDir())
			require.NotNil(t, database)
			faultDB := &accountE2EProfileWriteDB{KVDB: database}
			device := wallet.NewManager(config, faultDB)
			require.NotNil(t, device)
			t.Cleanup(func() { device.Close(); database.Close() })
			recovered, err := device.LoadAccountManagementStateForRecovery(f.location, f.pkg.Envelope.Locator, f.secret, f.rootMnemonic)
			require.NoError(t, err)
			_, err = device.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword, f.pkg.Envelope.Locator, f.restoreOptions())
			require.NoError(t, err)
			accountFailureReady(t, device)
			before := device.GetWalletCatalog()
			selected, subAccount := device.GetCurrentWalletId(), device.GetCurrentAccountId()
			faultDB.fail.Store(true)
			err = scenario.mutate(device, device.GetAccountManagementStatus().RootWalletID)
			faultDB.fail.Store(false)
			require.ErrorIs(t, err, errAccountE2EProfileWrite)
			assert.Equal(t, before, device.GetWalletCatalog())
			assert.Equal(t, selected, device.GetCurrentWalletId())
			assert.Equal(t, subAccount, device.GetCurrentAccountId())
			assert.Zero(t, device.GetAccountManagementStatus().PendingChanges)
			require.NoError(t, device.SyncAccountManagementState(context.Background()))
			_, latest := f.recover(t)
			assert.Equal(t, recovered.State, latest.State, "failed local operation changed remote state")
			require.NoError(t, scenario.mutate(device, device.GetAccountManagementStatus().RootWalletID))
			require.NoError(t, device.SyncAccountManagementState(context.Background()))
			_, latest = f.recover(t)
			assert.Greater(t, latest.Seq, recovered.Seq, "healthy retry was not published")
		})
	}

	t.Run("ProfileReadFailureCannotReplaceAccountSecret", func(t *testing.T) {
		f := prepareAccountReviewWithMnemonic(t, network, "", true)
		f.activate(t)
		config, _ := accountReviewConfig(t, network)
		database := indexerdb.NewKVDB(t.TempDir())
		require.NotNil(t, database)
		t.Cleanup(func() { database.Close() })
		fault := &accountE2EProfileWriteDB{KVDB: database}
		device := wallet.NewManager(config, fault)
		require.NotNil(t, device)
		state, err := device.LoadAccountManagementStateForRecovery(f.location, f.pkg.Envelope.Locator, f.secret, f.rootMnemonic)
		require.NoError(t, err)
		_, err = device.RestoreAccountManagementState(*state, f.secret, accountReviewPassword, f.pkg.Envelope.Locator, f.restoreOptions())
		require.NoError(t, err)
		key := []byte(wallet.GetDBKeyPrefix() + "account-management-profile-v1")
		before, err := database.Read(key)
		require.NoError(t, err)
		device.Close()
		fault.failRead.Store(true)
		failed := wallet.NewManager(config, fault)
		if failed != nil {
			failed.Close()
			t.Fatal("manager initialized over unreadable existing profile")
		}
		fault.failRead.Store(false)
		after, err := database.Read(key)
		require.NoError(t, err)
		assert.Equal(t, before, after, "failed initialization must preserve account credentials")
		reopened := wallet.NewManager(config, fault)
		require.NotNil(t, reopened)
		t.Cleanup(func() { reopened.Close() })
		_, err = reopened.UnlockWallet(accountReviewPassword)
		require.NoError(t, err)
		assert.Equal(t, f.pkg.Envelope.Locator.AccountID, reopened.GetAccountManagementStatus().AccountID)
		require.NoError(t, reopened.SyncAccountManagementState(context.Background()))
	})

	for _, selection := range []string{"wallet", "account"} {
		t.Run("SelectionStatusFailure/"+selection, func(t *testing.T) {
			f := prepareAccountReviewWithMnemonic(t, network, "", true)
			f.activate(t)
			config, _ := accountReviewConfig(t, network)
			database := indexerdb.NewKVDB(t.TempDir())
			require.NotNil(t, database)
			fault := &accountE2EProfileWriteDB{KVDB: database}
			device := wallet.NewManager(config, fault)
			require.NotNil(t, device)
			t.Cleanup(func() { device.Close(); database.Close() })
			state, err := device.LoadAccountManagementStateForRecovery(f.location, f.pkg.Envelope.Locator, f.secret, f.rootMnemonic)
			require.NoError(t, err)
			_, err = device.RestoreAccountManagementState(*state, f.secret, accountReviewPassword, f.pkg.Envelope.Locator, f.restoreOptions())
			require.NoError(t, err)
			accountFailureReady(t, device)
			root := device.GetCurrentWalletId()
			if selection == "wallet" {
				_, _, err = device.CreateWallet(accountReviewPassword)
			} else {
				err = device.EnsureAccount(root, 1, "Existing", "")
			}
			require.NoError(t, err)
			require.NoError(t, device.SyncAccountManagementState(context.Background()))
			requireE2EFirstBackup(t, device)
			beforeWallet, beforeAccount := device.GetCurrentWalletId(), device.GetCurrentAccountId()
			fault.key = "wallet-status"
			fault.fail.Store(true)
			if selection == "wallet" {
				err = device.SwitchWallet(root, accountReviewPassword)
			} else {
				err = device.SwitchAccount(1)
			}
			fault.fail.Store(false)
			require.ErrorIs(t, err, errAccountE2EProfileWrite)
			assert.Equal(t, beforeWallet, device.GetCurrentWalletId())
			assert.Equal(t, beforeAccount, device.GetCurrentAccountId())
			assert.Equal(t, beforeAccount, device.GetWallet().GetSubAccount())
			if selection == "wallet" {
				err = device.SwitchWallet(root, accountReviewPassword)
			} else {
				err = device.SwitchAccount(1)
			}
			require.NoError(t, err)
		})
	}

	t.Run("RemoteApplyReceiptFailureCanRetry", func(t *testing.T) {
		for _, failure := range []string{"put", "flush"} {
			t.Run(failure, func(t *testing.T) {
				f := prepareAccountReviewWithMnemonic(t, network, "", true)
				f.activate(t)
				config, _ := accountReviewConfig(t, network)
				dir := t.TempDir()
				database := indexerdb.NewKVDB(dir)
				require.NotNil(t, database)
				faultDB := &accountE2EProfileWriteDB{KVDB: database, failFlush: failure == "flush", moduleReceipt: true}
				device := wallet.NewManager(config, faultDB)
				require.NotNil(t, device)
				t.Cleanup(func() { device.Close(); database.Close() })
				recovered, err := device.LoadAccountManagementStateForRecovery(f.location, f.pkg.Envelope.Locator, f.secret, f.rootMnemonic)
				require.NoError(t, err)
				_, err = device.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword, f.pkg.Envelope.Locator, f.restoreOptions())
				require.NoError(t, err)
				before := device.GetWalletCatalog()
				require.NoError(t, f.manager.UpdateWalletName(f.manager.GetAccountManagementStatus().RootWalletID, "Atomic remote name"))
				require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
				index, remoteBefore := readAccountE2EIndex(t, network.Core, f.secret, f.pkg.Envelope.Locator.AccountID)
				_, modulesBefore := readAccountE2EModules(t, network.Core, f.secret, f.pkg.Envelope.Locator.AccountID, index)
				faultDB.fail.Store(true)
				err = device.SyncAccountManagementState(context.Background())
				faultDB.fail.Store(false)
				require.ErrorIs(t, err, errAccountE2EProfileWrite)
				require.ErrorIs(t, err, wallet.ErrAccountDBStopped)
				// Scheme A preserves the already committed wallet import, but its
				// durable completion marker must keep it unavailable until recovery.
				expected := append([]wallet.WalletCatalogEntry(nil), before...)
				expected[0].Name = "Atomic remote name"
				assert.Equal(t, expected, device.GetWalletCatalog(), "only the committed remote rename should be visible")
				require.ErrorIs(t, device.CheckAccountRestore("wallets", wallet.AccountManagedDataGlobalScope), wallet.ErrAccountManagedDataImportIncomplete)
				status := device.GetAccountManagementStatus()
				require.False(t, status.WriteAllowed)
				require.Equal(t, "ACCOUNT_DB_WRITE_FAILED", status.LastDKVSSyncErrorCode)
				require.Contains(t, status.LastDKVSSyncError, "close the wallet")
				require.Contains(t, status.LastDKVSSyncError, "do not delete wallet or website data")
				writes := faultDB.writes.Load()
				require.ErrorIs(t, device.UpdateWalletName(before[0].ID, "must remain stopped"), wallet.ErrAccountDBStopped)
				require.ErrorIs(t, device.SyncAccountManagementState(context.Background()), wallet.ErrAccountDBStopped)
				require.ErrorIs(t, device.RetryDKVSSync(), wallet.ErrAccountDBStopped)
				require.ErrorIs(t, device.StartDKVSSync(), wallet.ErrAccountDBStopped)
				require.Equal(t, writes, faultDB.writes.Load(), "a failed storage session must not attempt more account writes")

				device.Close()
				require.NoError(t, database.Close())
				database = indexerdb.NewKVDB(dir)
				require.NotNil(t, database)
				device = wallet.NewManager(config, database)
				_, err = device.UnlockWallet(accountReviewPassword)
				require.NoError(t, err)
				require.NoError(t, device.StartDKVSSync())
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				tick := time.NewTicker(20 * time.Millisecond)
				defer tick.Stop()
				for device.CheckAccountRestore("wallets", wallet.AccountManagedDataGlobalScope) != nil ||
					device.CheckAccountWrite("wallets", wallet.AccountManagedDataGlobalScope) != nil {
					select {
					case <-ctx.Done():
						t.Fatal("a fresh Manager did not resume the durable import without explicit synchronization")
					case <-tick.C:
					}
				}
				assert.Equal(t, expected, device.GetWalletCatalog())
				assert.Zero(t, device.GetAccountManagementStatus().PendingChanges)
				_, remoteAfter := readAccountE2EIndex(t, network.Core, f.secret, f.pkg.Envelope.Locator.AccountID)
				require.Equal(t, remoteBefore, remoteAfter, "local recovery must not republish the directory")
				_, modulesAfter := readAccountE2EModules(t, network.Core, f.secret, f.pkg.Envelope.Locator.AccountID, index)
				require.Equal(t, modulesBefore, modulesAfter, "local recovery must not republish stale module contents")
			})
		}
	})

	t.Run("FailedDeleteMustNotPublishTombstone", func(t *testing.T) {
		f := prepareAccountReviewWithMnemonic(t, network, "", true)
		childID, err := f.manager.ImportWallet(coreMnemonic, accountReviewPassword)
		require.NoError(t, err)
		child := f.manager.GetWalletCatalog()[1]
		require.Equal(t, childID, child.ID)
		f.activate(t)

		config, _ := accountReviewConfig(t, network)
		database := indexerdb.NewKVDB(t.TempDir())
		require.NotNil(t, database)
		faultDB := &accountE2EProfileWriteDB{KVDB: database}
		device := wallet.NewManager(config, faultDB)
		require.NotNil(t, device)
		t.Cleanup(func() { device.Close(); database.Close() })
		recovered, err := device.LoadAccountManagementStateForRecovery(f.location, f.pkg.Envelope.Locator, f.secret, f.rootMnemonic)
		require.NoError(t, err)
		_, err = device.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword, f.pkg.Envelope.Locator, f.restoreOptions())
		require.NoError(t, err)
		accountFailureReady(t, device)
		localChild := accountReviewFind(t, device, child.Fingerprint)
		before := device.GetWalletCatalog()
		pending := device.GetAccountManagementStatus().PendingChanges
		faultDB.fail.Store(true)
		err = device.DeleteWallet(localChild.ID)
		faultDB.fail.Store(false)
		require.ErrorIs(t, err, errAccountE2EProfileWrite, "fault must reach the intended persistence boundary")
		assert.Equal(t, before, device.GetWalletCatalog(), "rejected deletion must retain local wallets")
		assert.Equal(t, pending, device.GetAccountManagementStatus().PendingChanges, "rejected deletion must not enqueue a future remote mutation")
		require.NoError(t, device.SyncAccountManagementState(context.Background()))
		// Local ACK handling alone can conceal this bug. Read the authenticated
		// remote record and restore another device to prove remote preservation.
		third, latest := f.recover(t)
		for _, entry := range latest.State.Wallets {
			if entry.Fingerprint == child.Fingerprint {
				assert.False(t, entry.Deleted, "a failed API call published a remote deletion")
			}
		}
		_, err = third.RestoreAccountManagementState(*latest, f.secret, accountReviewPassword, f.pkg.Envelope.Locator, f.restoreOptions())
		require.NoError(t, err)
		assert.Len(t, third.GetWalletCatalog(), len(before), "recovery lost a wallet whose deletion was rejected")
		accountReviewFind(t, third, child.Fingerprint)
	})

	t.Run("PrivateKeyImportCannotDisableMnemonicBackup", func(t *testing.T) {
		f := prepareAccountReviewWithMnemonic(t, network, "", true)
		f.activate(t)
		before := f.manager.GetWalletCatalog()
		pending := f.manager.GetAccountManagementStatus().PendingChanges
		// Existing SDK test key; this fixture uses only temporary nodes.
		const privateKey = "1d5da8898fa894a056473e19e18bb2fa907172d25424cea6a0894312b2801bcc"
		_, importErr := f.manager.ImportWalletWithPrivateKey(privateKey, accountReviewPassword)
		if importErr != nil {
			assert.Equal(t, before, f.manager.GetWalletCatalog())
			assert.Equal(t, pending, f.manager.GetAccountManagementStatus().PendingChanges)
		}
		require.NoError(t, f.manager.SyncAccountManagementState(context.Background()), "accepted private-key import must not disable an existing account backup")
		device := accountFailureRestore(t, f)
		assert.Equal(t, len(f.manager.GetWalletCatalog()), len(device.GetWalletCatalog()), "accepted catalog entries must remain recoverable")
		accountReviewFind(t, device, before[0].Fingerprint)
	})

	t.Run("SwitchAccountCannotBypassRecoveryLimit", func(t *testing.T) {
		f := prepareAccountReviewWithMnemonic(t, network, "", true)
		f.activate(t)
		rootID := f.manager.GetAccountManagementStatus().RootWalletID
		beforeCount := f.manager.GetAllWallets()[rootID]
		beforeIndex := f.manager.GetCurrentAccountId()
		require.Error(t, f.manager.EnsureAccount(rootID, account.MaxManagedStateItems, "invalid", ""))
		f.manager.SwitchAccount(account.MaxManagedStateItems)
		assert.Equal(t, beforeCount, f.manager.GetAllWallets()[rootID], "legacy switch must not bypass catalog admission")
		assert.Equal(t, beforeIndex, f.manager.GetCurrentAccountId(), "rejected index must preserve the selected identity")
		assert.NoError(t, f.manager.SyncAccountManagementState(context.Background()), "invalid selection must not poison backup synchronization")
		third := accountFailureRestore(t, f)
		assert.Len(t, third.GetWalletCatalog()[0].Accounts, beforeCount)
	})

	t.Run("RemoteDeletionSurvivesReauthenticationAndColdRestart", func(t *testing.T) {
		f := prepareAccountReviewWithMnemonic(t, network, "", true)
		childID, _, err := f.manager.CreateWallet(accountReviewPassword)
		require.NoError(t, err)
		require.NoError(t, f.manager.EnsureAccount(childID, 1, "Savings", ""))
		child := f.manager.GetWalletCatalog()[1]
		f.activate(t)

		config, _ := accountReviewConfig(t, network)
		dir := t.TempDir()
		database := indexerdb.NewKVDB(dir)
		require.NotNil(t, database)
		device := wallet.NewManager(config, database)
		require.NotNil(t, device)
		t.Cleanup(func() { device.Close(); database.Close() })
		_, recovered := f.recover(t)
		_, err = device.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword, f.pkg.Envelope.Locator, f.restoreOptions())
		require.NoError(t, err)
		selected := accountReviewFind(t, device, child.Fingerprint)
		require.NoError(t, device.SwitchWallet(selected.ID, ""))
		device.SwitchAccount(1)
		require.NoError(t, f.manager.DeleteWallet(childID))
		require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
		require.NoError(t, device.SyncAccountManagementState(context.Background()))
		require.Len(t, device.GetWalletCatalog(), 1, "deletion must reach the replica before testing unlock")
		root := device.GetWalletCatalog()[0]
		_, err = device.UnlockWallet(accountReviewPassword)
		require.NoError(t, err)
		assert.Len(t, device.GetWalletCatalog(), 1, "reauthentication resurrected a deleted wallet")
		assert.Equal(t, root.ID, device.GetCurrentWalletId())
		assert.Zero(t, device.GetCurrentAccountId())

		device.Close()
		database.Close()
		database = indexerdb.NewKVDB(dir)
		require.NotNil(t, database)
		device = wallet.NewManager(config, database)
		require.NotNil(t, device)
		// No account synchronization between reopening and observation: prove
		// that deletion was durable locally, rather than repaired by a new pull.
		assert.Len(t, device.GetWalletCatalog(), 1, "cold restart loaded an already deleted wallet")
		_, err = device.UnlockWallet(accountReviewPassword)
		require.NoError(t, err)
		assert.Len(t, device.GetWalletCatalog(), 1)
		assert.Equal(t, root.ID, device.GetCurrentWalletId())
		assert.Zero(t, device.GetCurrentAccountId())
		assert.Equal(t, root.Accounts[0].Address, device.GetWallet().GetAddress())
	})
}
