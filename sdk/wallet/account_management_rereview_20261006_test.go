package wallet

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestAccountRepublishMissingTemporaryState(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	for _, all := range []bool{false, true} {
		t.Run(fmt.Sprintf("all=%t", all), func(t *testing.T) {
			manager, _, remote := reviewAccountDevices(t)
			before := manager.GetWalletCatalog()
			packageID := manager.accountProfile.PackageID
			values, err := manager.listAccountLocal(manager.accountProfile.AccountID)
			require.NoError(t, err)
			original := make(map[string][]byte)
			remote.mu.Lock()
			for _, value := range values {
				if !all && value.Provider != "wallets" {
					continue
				}
				record := remote.records[value.Key]
				require.NotNil(t, record)
				original[value.Key] = bytes.Clone(record.Value)
				delete(remote.records, value.Key)
				delete(remote.changedAt, value.Key)
				prefix, err := dkvsindexer.CollectionPathForKey(value.Key)
				require.NoError(t, err)
				remote.generations[prefix]++
			}
			remote.mu.Unlock()
			require.NoError(t, manager.SyncAccountManagementState(context.Background()))
			require.Equal(t, packageID, manager.accountProfile.PackageID, "TTL recovery must not activate a new package")
			require.Equal(t, before, manager.GetWalletCatalog())
			remote.mu.Lock()
			defer remote.mu.Unlock()
			for key, encoded := range original {
				require.NotNil(t, remote.records[key])
				require.Equal(t, encoded, remote.records[key].Value, "TTL retry must retain original ciphertext")
			}
		})
	}
}

func TestAccountRecoveryRejectsOwnGuardian(t *testing.T) {
	manager := newAccountManagementAutoTestManager(t)
	if _, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	if err := manager.InitializeAccountManagement("password"); err != nil {
		t.Fatal(err)
	}
	identity, err := manager.GetOrCreateAccountGuardianIdentity("password")
	if err != nil {
		t.Fatal(err)
	}
	backup, err := manager.ExportAccountBackup("password", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clearAccountBackup(&backup)
	_, key, err := account.GenerateGuardianKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	options := account.CreateOptions{AccountID: identity.MailboxID, Backup: backup,
		RecoveryMode: account.RecoveryMode2Of3, Questions: coreQuestions(),
		GuardianMailboxID: identity.MailboxID, GuardianPublicKey: key}
	if _, err := manager.CreateAccountRecoveryPackage(options); err == nil {
		t.Fatal("own root guardian was accepted")
	}
	options.GuardianMailboxID = strings.ToUpper(identity.MailboxID)
	if _, err := manager.CreateAccountRecoveryPackage(options); err == nil {
		t.Fatal("case variant of own root guardian was accepted")
	}
}

// Public SDK operations over the existing deterministic two-device fixture.
// These are account integration regressions, not substitutes for real-node E2E.
// No opt-in tags, production wallets, real funds or network endpoints are used.
func TestAccountManagementRereview20261006(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	t.Run("ConcurrentWalletCreationMerges", func(t *testing.T) {
		first, second, remote := reviewAccountDevices(t)
		initialCount := len(first.GetWalletCatalog())
		firstID, _, err := first.CreateWallet("password")
		if err != nil {
			t.Fatal(err)
		}
		secondID, _, err := second.CreateWallet("password")
		if err != nil {
			t.Fatal(err)
		}
		firstFingerprint := walletFingerprint(first.walletInfoMap[firstID].Wallet)
		secondFingerprint := walletFingerprint(second.walletInfoMap[secondID].Wallet)
		if firstFingerprint == secondFingerprint {
			t.Fatal("fixture did not create distinct wallets")
		}
		if err := second.SyncAccountManagementState(context.Background()); err != nil {
			t.Fatal(err)
		}
		require.NoError(t, first.SyncAccountManagementState(context.Background()))
		require.NoError(t, second.SyncAccountManagementState(context.Background()))
		for _, device := range []*Manager{first, second} {
			require.Len(t, device.GetWalletCatalog(), initialCount+2)
			require.NotNil(t, reviewCatalogWallet(t, device, firstFingerprint))
			require.NotNil(t, reviewCatalogWallet(t, device, secondFingerprint))
		}

		// Read and explicitly verify the remote record through a synchronized
		// device before a separate device enters normal account recovery.
		store, err := first.accountDKVSStore()
		require.NoError(t, err)
		stateKey := accountWalletModuleKey(t, first)
		value, err := store.GetAuthoritative(stateKey)
		require.NoError(t, err)
		require.NotNil(t, value)
		require.NotNil(t, value.record)
		require.NoError(t, dkvsindexer.VerifySignature(value.record))
		require.Equal(t, stateKey, value.record.Key)
		require.NotZero(t, value.record.Seq)
		state, err := account.OpenManagedState(first.accountSecret, first.accountProfile.AccountID, value.Value)
		require.NoError(t, err)
		defer func() {
			for i := range state.Wallets {
				zeroBytes(state.Wallets[i].Secret)
			}
		}()
		require.Equal(t, initialCount+2, len(state.Wallets))
		for _, fingerprint := range []string{firstFingerprint, secondFingerprint} {
			wallet := findManagedWallet(&state, fingerprint)
			require.NotNil(t, wallet)
			require.False(t, wallet.Deleted)
		}
		recovered, _, _, _ := newAccountGroup(t, remote)
		require.NoError(t, restoreImportGroup(t, recovered, first))
		require.Len(t, recovered.GetWalletCatalog(), initialCount+2)
		require.NotNil(t, reviewCatalogWallet(t, recovered, firstFingerprint))
		require.NotNil(t, reviewCatalogWallet(t, recovered, secondFingerprint))
	})

	t.Run("SequentialWalletCreationPublishesBoth", func(t *testing.T) {
		first, second, _ := reviewAccountDevices(t)
		firstID, _, err := first.CreateWallet("password")
		if err != nil {
			t.Fatal(err)
		}
		firstFingerprint := walletFingerprint(first.walletInfoMap[firstID].Wallet)
		if err := first.SyncAccountManagementState(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := second.SyncAccountManagementState(context.Background()); err != nil {
			t.Fatal(err)
		}
		secondID, _, err := second.CreateWallet("password")
		if err != nil {
			t.Fatal(err)
		}
		secondFingerprint := walletFingerprint(second.walletInfoMap[secondID].Wallet)
		if err := second.SyncAccountManagementState(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := first.SyncAccountManagementState(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, device := range []*Manager{first, second} {
			if reviewCatalogWallet(t, device, firstFingerprint) == nil || reviewCatalogWallet(t, device, secondFingerprint) == nil {
				t.Fatal("sequential publication lost a wallet")
			}
		}
	})

	t.Run("SwitchAccountCannotBypassRecoveryLimit", func(t *testing.T) {
		manager, _, _ := reviewAccountDevices(t)
		rootID := manager.GetAccountManagementStatus().RootWalletID
		before := manager.GetAllWallets()[rootID]
		if err := manager.EnsureAccount(rootID, account.MaxManagedStateItems, "invalid", ""); err == nil {
			t.Fatal("EnsureAccount did not enforce its documented bound")
		}
		manager.SwitchAccount(account.MaxManagedStateItems)
		after := manager.GetAllWallets()[rootID]
		persisted, err := loadWallet(manager.db, rootID)
		if err != nil {
			t.Fatal(err)
		}
		syncErr := manager.SyncAccountManagementState(context.Background())
		t.Logf("rereview: switch index=%d before=%d after=%d persisted=%d sync=%v",
			account.MaxManagedStateItems, before, after, persisted.Accounts, syncErr)
		if after != before || persisted.Accounts != before {
			t.Error("SwitchAccount persisted an account count rejected by the recovery codec")
		}
		if syncErr != nil {
			t.Errorf("invalid switch poisoned ordinary account synchronization: %v", syncErr)
		}
	})

	t.Run("PrivateKeyImportCannotDisableExistingAccountBackup", func(t *testing.T) {
		manager, _, _ := reviewAccountDevices(t)
		const privateKey = "1d5da8898fa894a056473e19e18bb2fa907172d25424cea6a0894312b2801bcc"
		before := len(manager.GetAllWallets())
		_, importErr := manager.ImportWalletWithPrivateKey(privateKey, "password")
		if importErr != nil {
			if len(manager.GetAllWallets()) != before {
				t.Error("rejected import changed the catalog")
			}
			return
		}
		syncErr := manager.SyncAccountManagementState(context.Background())
		t.Logf("rereview: private-key import accepted wallets=%d sync=%v", len(manager.GetAllWallets()), syncErr)
		if syncErr != nil {
			t.Errorf("accepted private-key import disabled the existing mnemonic account backup: %v", syncErr)
		}
	})
}
