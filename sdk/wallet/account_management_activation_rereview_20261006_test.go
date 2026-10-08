package wallet

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

func rereviewRecoveryPackage(t *testing.T, manager *Manager) (*account.RecoveryPackage, *AccountStorageAuthorization, []byte) {
	t.Helper()
	auth, err := manager.ConfirmAccountStorage(AccountStorageTemporary, 0)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := manager.ExportAccountBackupForPWA("password", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clearAccountBackup(&backup)
	bootstrap, err := account.RootBootstrapBackup(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer clearAccountBackup(&bootstrap)
	questions := []account.QuestionAnswer{
		{Question: account.KnowledgeQuestion{ID: "one", Prompt: "one"}, Answer: "answer one", Confirmation: "answer one"},
		{Question: account.KnowledgeQuestion{ID: "two", Prompt: "two"}, Answer: "answer two", Confirmation: "answer two"},
		{Question: account.KnowledgeQuestion{ID: "three", Prompt: "three"}, Answer: "answer three", Confirmation: "answer three"},
	}
	pkg, err := manager.CreateAccountRecoveryPackage(account.CreateOptions{
		AccountID: manager.GetAccountManagementStatus().AccountID, Backup: bootstrap,
		RecoveryMode: account.RecoveryMode2Of2, Questions: questions,
	})
	if err != nil {
		t.Fatal(err)
	}
	share, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle,
		[]account.AnswerAttempt{{QuestionID: "one", Answer: "answer one"}, {QuestionID: "two", Answer: "answer two"}})
	if err != nil {
		t.Fatal(err)
	}
	recovered, secret, err := account.RecoverAccount(pkg.Envelope, pkg.UserShare, share)
	clearAccountBackup(&recovered)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { zeroBytes(secret) })
	return pkg, auth, secret
}

// The transport refresh models DKVS finishing before the account-domain job.
// It reads real signed records from the fixture, never injects a wallet state.
func TestAccountReconfigurationPreservesNewerRemoteWallet20261006(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	remote := newRGB11MemoryDKVSHTTP()
	client := newRGB11MessageNodeClient(remote)
	first := newAccountManagementAutoTestManager(t)
	second := newAccountManagementAutoTestManager(t)
	for _, manager := range []*Manager{first, second} {
		configureRGB11DKVSTestManager(manager, remote)
		manager.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
	}
	if _, err := first.ImportWallet(accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	if err := first.InitializeAccountManagement("password"); err != nil {
		t.Fatal(err)
	}
	pkg, auth, secret := rereviewRecoveryPackage(t, first)
	if err := first.ActivateAccountManagement(secret, "password", *auth, pkg.Envelope.Locator, "account://"+pkg.Envelope.Locator.PackageID); err != nil {
		t.Fatal(err)
	}
	initial, err := second.LoadAccountManagementStateForRecovery(auth.Location, pkg.Envelope.Locator, secret, accountRootWrapperTestMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	_, err = second.RestoreAccountManagementState(*initial, secret, "password", pkg.Envelope.Locator,
		AccountManagementRestoreOptions{Location: auth.Location, StorageMode: AccountStorageTemporary, RecordTTL: auth.RecordOptions.TTL})
	if err != nil {
		t.Fatal(err)
	}
	initialRevision := first.GetAccountManagementStatus().StateSeq
	childID, _, err := second.CreateWallet("password")
	if err != nil {
		t.Fatal(err)
	}
	childFingerprint := walletFingerprint(second.walletInfoMap[childID].Wallet)
	if err := second.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	remoteRevision := second.GetAccountManagementStatus().StateSeq
	if remoteRevision <= initialRevision {
		t.Fatal("fixture has no newer remote revision")
	}

	// Refresh only the DKVS replica, deliberately leaving the first device's
	// account-domain snapshot stale, as happens between two background jobs.
	store, err := first.accountDKVSStore()
	if err != nil {
		t.Fatal(err)
	}
	root, err := first.accountManagementRootWallet()
	if err != nil {
		t.Fatal(err)
	}
	stateKey, err := first.accountManagedStateKey(root)
	if err != nil {
		t.Fatal(err)
	}
	dataKey, err := first.accountManagedDataBlobKey(root)
	if err != nil {
		t.Fatal(err)
	}
	wrapperKey, err := accountRootWrapperKey(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Refresh(stateKey, dataKey, wrapperKey); err != nil {
		t.Fatal(err)
	}

	beforeCatalog := first.GetWalletCatalog()
	beforeStatus := first.GetAccountManagementStatus()
	beforeRemote := map[string]*dkvsValue{}
	for _, key := range []string{stateKey, dataKey, wrapperKey} {
		value, err := store.GetAuthoritative(key)
		if err != nil {
			t.Fatal(err)
		}
		beforeRemote[key] = value
	}
	newPackage, newAuth, newSecret := rereviewRecoveryPackage(t, first)
	activationErr := first.ActivateAccountManagement(newSecret, "password", *newAuth,
		newPackage.Envelope.Locator, "account://"+newPackage.Envelope.Locator.PackageID)
	if activationErr != nil {
		if !errors.Is(activationErr, errAccountSnapshotChanged) {
			t.Fatalf("unexpected activation error: %v", activationErr)
		}
		if !reflect.DeepEqual(beforeCatalog, first.GetWalletCatalog()) || !reflect.DeepEqual(beforeStatus, first.GetAccountManagementStatus()) {
			t.Fatal("stale reconfiguration changed local state")
		}
		for key, before := range beforeRemote {
			value, err := store.GetAuthoritative(key)
			if err != nil {
				t.Fatal(err)
			}
			if before.Hash != value.Hash || before.Seq != value.Seq || !bytes.Equal(before.Value, value.Value) {
				t.Fatal("rejected reconfiguration changed remote state")
			}
		}
		if err := first.SyncAccountManagementState(context.Background()); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range first.GetWalletCatalog() {
			if item.Fingerprint == childFingerprint {
				found = true
			}
		}
		if !found {
			t.Fatal("explicit synchronization lost the newer remote wallet")
		}
		newPackage, newAuth, newSecret = rereviewRecoveryPackage(t, first)
		if err := first.ActivateAccountManagement(newSecret, "password", *newAuth, newPackage.Envelope.Locator, "account://"+newPackage.Envelope.Locator.PackageID); err != nil {
			t.Fatalf("synchronized retry failed: %v", err)
		}
	}
	restored, err := first.LoadAccountManagementStateForRecovery(newAuth.Location,
		newPackage.Envelope.Locator, newSecret, accountRootWrapperTestMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range restored.State.Wallets {
		if item.Fingerprint == childFingerprint && !item.Deleted {
			found = true
		}
	}
	t.Logf("rereview: stale_revision=%d remote_before=%d after_activation=%d remote_wallets_after=%d child_preserved=%t",
		initialRevision, remoteRevision, restored.Seq, len(restored.State.Wallets), found)
	if !found {
		t.Error("successful recovery reconfiguration removed a newer remotely backed-up wallet")
	}
}
