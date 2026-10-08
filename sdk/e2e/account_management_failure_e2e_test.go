package e2e

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errAccountE2EProfileWrite = errors.New("account E2E: injected profile write failure")

// Fault only local persistence. Account records, signing, RPC and recovery
// continue to use the real node and SDK. Do not start background SDK workers.
type accountE2EProfileWriteDB struct {
	indexercommon.KVDB
	fail     atomic.Bool
	key      string
	failRead atomic.Bool
}

func (d *accountE2EProfileWriteDB) Write(key, value []byte) error {
	if d.fail.Load() && d.matches(key) {
		return errAccountE2EProfileWrite
	}
	return d.KVDB.Write(key, value)
}

func (d *accountE2EProfileWriteDB) matches(key []byte) bool {
	name := d.key
	if name == "" {
		name = wallet.GetDBKeyPrefix() + "account-management-profile-v2"
	}
	return string(key) == name
}

func (d *accountE2EProfileWriteDB) Read(key []byte) ([]byte, error) {
	if d.failRead.Load() && d.matches(key) {
		return nil, errAccountE2EProfileWrite
	}
	return d.KVDB.Read(key)
}

func (d *accountE2EProfileWriteDB) NewWriteBatch() indexercommon.WriteBatch {
	return &accountE2EProfileWriteBatch{WriteBatch: d.KVDB.NewWriteBatch(), database: d}
}

type accountE2EProfileWriteBatch struct {
	indexercommon.WriteBatch
	database *accountE2EProfileWriteDB
}

func (b *accountE2EProfileWriteBatch) Put(key, value []byte) error {
	if b.database.fail.Load() && b.database.matches(key) {
		return errAccountE2EProfileWrite
	}
	return b.WriteBatch.Put(key, value)
}

func accountFailureRestore(t *testing.T, f *accountReviewFixture) *wallet.Manager {
	t.Helper()
	device, recovered := f.recover(t)
	_, err := device.RestoreAccountManagementState(*recovered, f.secret, accountReviewPassword,
		f.pkg.Envelope.Locator, f.restoreOptions())
	require.NoError(t, err)
	return device
}

// Each subtest has a new root/account namespace and independent databases.
// A red regression must not prevent the remaining failure cases from running.
// Assert the intended behavior, never the current defect or an expected failure.
func TestSDKAccountFailureRegressions(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)

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
		key := []byte(wallet.GetDBKeyPrefix() + "account-management-profile-v2")
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
			root := device.GetCurrentWalletId()
			if selection == "wallet" {
				_, _, err = device.CreateWallet(accountReviewPassword)
			} else {
				err = device.EnsureAccount(root, 1, "Existing", "")
			}
			require.NoError(t, err)
			require.NoError(t, device.SyncAccountManagementState(context.Background()))
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

	t.Run("RemoteApplyProfileFailureCanRetry", func(t *testing.T) {
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
		before := device.GetWalletCatalog()
		require.NoError(t, f.manager.UpdateWalletName(f.manager.GetAccountManagementStatus().RootWalletID, "Atomic remote name"))
		require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
		faultDB.fail.Store(true)
		err = device.SyncAccountManagementState(context.Background())
		faultDB.fail.Store(false)
		require.ErrorIs(t, err, errAccountE2EProfileWrite)
		assert.Equal(t, before, device.GetWalletCatalog())
		require.NoError(t, device.SyncAccountManagementState(context.Background()))
		assert.Equal(t, "Atomic remote name", device.GetWalletCatalog()[0].Name)
		assert.Zero(t, device.GetAccountManagementStatus().PendingChanges)
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

	t.Run("MetadataAdmissionIncludesPublishedDataReference", func(t *testing.T) {
		f := prepareAccountReviewWithMnemonic(t, network, "", true)
		f.activate(t)
		rootID := f.manager.GetAccountManagementStatus().RootWalletID
		require.NoError(t, f.manager.UpdateAccountMetadata(rootID, 0,
			strings.Repeat("a", account.MaxManagedStateString), strings.Repeat("b", account.MaxManagedStateString)))
		require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
		_, baseline := f.recover(t)
		require.NotZero(t, baseline.State.DataRevision)
		require.Len(t, baseline.State.DataHash, 64)
		candidate := accountReviewClone(t, baseline).State
		candidate.Revision++
		candidate.DataRevision++
		candidate.Wallets[0].Revision++
		length := 0
		for size := 1; size <= account.MaxManagedStateString; size++ {
			candidate.Wallets[0].SubAccounts[1].Name = strings.Repeat("c", size)
			if account.ValidateManagedState(candidate) == nil {
				continue
			}
			// This candidate passes the initial-state admission model but not
			// the real published state with its authenticated data reference.
			initial := accountReviewClone(t, baseline).State
			initial.Revision, initial.DataRevision, initial.DataHash = 1, 0, ""
			initial.Wallets[0].Revision = 1
			initial.Wallets[0].SubAccounts[1].Name = strings.Repeat("c", size)
			require.NoError(t, account.ValidateManagedState(initial))
			length = size
			break
		}
		require.Positive(t, length, "fixture must reach the actual codec boundary")
		before := f.manager.GetWalletCatalog()
		err := f.manager.UpdateAccountMetadata(rootID, 1, strings.Repeat("c", length), "")
		assert.Error(t, err, "metadata that cannot be published must be rejected before changing the catalog")
		assert.Equal(t, before, f.manager.GetWalletCatalog(), "failed admission must be atomic")
		assert.NoError(t, f.manager.SyncAccountManagementState(context.Background()), "a catalog API must not poison subsequent backup synchronization")
		_, remote := f.recover(t)
		assert.Equal(t, baseline.Hash, remote.Hash, "rejected metadata must preserve the recoverable record")
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

	t.Run("IndependentWalletCreationMustConverge", func(t *testing.T) {
		f := prepareAccountReviewWithMnemonic(t, network, "", true)
		f.activate(t)
		second := accountFailureRestore(t, f)
		firstID, _, err := f.manager.CreateWallet(accountReviewPassword)
		require.NoError(t, err)
		secondID, _, err := second.CreateWallet(accountReviewPassword)
		require.NoError(t, err)
		var fingerprints []string
		for _, pair := range []struct {
			manager *wallet.Manager
			id      int64
		}{{f.manager, firstID}, {second, secondID}} {
			for _, entry := range pair.manager.GetWalletCatalog() {
				if entry.ID == pair.id {
					fingerprints = append(fingerprints, entry.Fingerprint)
				}
			}
		}
		require.Len(t, fingerprints, 2)
		require.NotEqual(t, fingerprints[0], fingerprints[1])
		require.NoError(t, second.SyncAccountManagementState(context.Background()))
		firstSync := f.manager.SyncAccountManagementState(context.Background())
		secondSync := second.SyncAccountManagementState(context.Background())
		retry := f.manager.SyncAccountManagementState(context.Background())
		assert.NoError(t, firstSync, "independently generated default names must not permanently block convergence")
		assert.NoError(t, secondSync)
		assert.NoError(t, retry)
		if firstSync != nil || secondSync != nil || retry != nil {
			return
		}
		third := accountFailureRestore(t, f)
		for _, device := range []*wallet.Manager{f.manager, second, third} {
			assert.Len(t, device.GetWalletCatalog(), 3)
			for _, fingerprint := range fingerprints {
				accountReviewFind(t, device, fingerprint)
			}
			assert.Zero(t, device.GetAccountManagementStatus().PendingChanges)
		}
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
