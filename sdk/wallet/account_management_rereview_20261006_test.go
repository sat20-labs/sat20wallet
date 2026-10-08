package wallet

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func TestAccountRepublishMissingTemporaryState(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	for _, scenario := range []struct{ missingState, localAhead bool }{{true, false}, {false, false}, {false, true}} {
		missingState := scenario.missingState
		name := "missing-blob-fails-closed"
		if missingState {
			name = "all-temporary-records-missing"
		}
		if scenario.localAhead {
			name = "missing-blob-with-newer-local-state-fails-closed"
		}
		t.Run(name, func(t *testing.T) {
			manager, _, remote := reviewAccountDevices(t)
			before := manager.GetWalletCatalog()
			root, err := manager.accountManagementRootWallet()
			if err != nil {
				t.Fatal(err)
			}
			stateKey, _ := manager.accountManagedStateKey(root)
			dataKey, _ := manager.accountManagedDataBlobKey(root)
			remote.mu.Lock()
			for _, key := range []string{stateKey, dataKey} {
				if !missingState && key == stateKey {
					continue
				}
				delete(remote.records, key)
				delete(remote.changedAt, key)
				prefix, err := dkvsindexer.CollectionPathForKey(key)
				if err != nil {
					remote.mu.Unlock()
					t.Fatal(err)
				}
				remote.generations[prefix]++
			}
			remote.mu.Unlock()
			if scenario.localAhead {
				manager.mutex.Lock()
				profile := manager.accountProfile
				state, openErr := account.OpenManagedState(manager.accountSecret, profile.AccountID, profile.StateEnvelope)
				if openErr == nil {
					state.Revision++
					profile.StateEnvelope, openErr = account.SealManagedState(manager.accountSecret, profile.AccountID, state, nil)
					profile.StateSeq = state.Revision
					profile.StateHash = accountStateDigest(profile.StateEnvelope)
				}
				manager.mutex.Unlock()
				if openErr != nil {
					t.Fatal(openErr)
				}
			}
			err = manager.SyncAccountManagementState(context.Background())
			if !missingState {
				if !errors.Is(err, ErrDKVSRecordNotFound) {
					t.Fatalf("existing remote state with a missing blob must reject that reference: %v", err)
				}
				remote.mu.Lock()
				blob := remote.records[dataKey]
				remote.mu.Unlock()
				if blob != nil {
					t.Fatal("corrupt remote baseline was repaired from local data")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(manager.GetWalletCatalog()) != len(before) {
				t.Fatal("republish changed the wallet catalog")
			}
			remote.mu.Lock()
			state, data := remote.records[stateKey], remote.records[dataKey]
			remote.mu.Unlock()
			if state == nil || data == nil {
				t.Fatal("state and data were not republished together")
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

	t.Run("IndependentWalletCreationMustConverge", func(t *testing.T) {
		first, second, _ := reviewAccountDevices(t)
		firstID, _, err := first.CreateWallet("password")
		if err != nil {
			t.Fatal(err)
		}
		secondID, _, err := second.CreateWallet("password")
		if err != nil {
			t.Fatal(err)
		}
		firstName := first.walletInfoMap[firstID].Name
		secondName := second.walletInfoMap[secondID].Name
		firstFingerprint := walletFingerprint(first.walletInfoMap[firstID].Wallet)
		secondFingerprint := walletFingerprint(second.walletInfoMap[secondID].Wallet)
		if firstFingerprint == secondFingerprint {
			t.Fatal("fixture did not create distinct wallets")
		}
		if err := second.SyncAccountManagementState(context.Background()); err != nil {
			t.Fatal(err)
		}
		firstSync := first.SyncAccountManagementState(context.Background())
		secondSync := first.SyncAccountManagementState(context.Background())
		t.Logf("rereview: independent names=%q/%q first_sync=%v retry=%v pending=%d", firstName,
			secondName, firstSync, secondSync, first.GetAccountManagementStatus().PendingChanges)
		if firstSync != nil || secondSync != nil {
			t.Errorf("valid independent wallet additions must converge without a permanent naming conflict")
			return
		}
		if err := second.SyncAccountManagementState(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, device := range []*Manager{first, second} {
			if reviewCatalogWallet(t, device, firstFingerprint) == nil || reviewCatalogWallet(t, device, secondFingerprint) == nil {
				t.Error("converged catalog lost an independently created wallet")
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
