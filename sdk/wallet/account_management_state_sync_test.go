package wallet

import (
	"strings"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

func syncTestWallet(fingerprint, name string) account.ManagedWallet {
	return account.ManagedWallet{
		Fingerprint: fingerprint, Revision: 1, Name: name,
		Mnemonic:     "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		AccountCount: 1, SubAccounts: []account.SubAccount{{Index: 0, Name: "Account 1"}},
	}
}

func TestBuildAccountManagedStateTargetUsesPendingLocalMetadata(t *testing.T) {
	root := strings.Repeat("a", 64)
	remoteWallet := syncTestWallet(root, "Remote")
	localWallet := syncTestWallet(root, "Local")
	snapshot := &accountManagementSyncSnapshot{
		profile: accountManagementProfile{RootFingerprint: root},
		wallets: map[string]account.ManagedWallet{root: localWallet},
		pending: []accountManagementMutation{{ID: "m1", Type: accountMutationWalletName, Fingerprint: root}},
	}
	target, changed, err := buildAccountManagedStateTarget(account.ManagedState{
		Version: account.ManagedStateVersion, RootFingerprint: root, Revision: 1,
		Wallets: []account.ManagedWallet{remoteWallet},
	}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || target.Revision != 2 || target.Wallets[0].Name != "Local" {
		t.Fatalf("pending metadata was not merged: %+v", target)
	}
}

func TestBuildAccountManagedStateTargetDoesNotResurrectRemoteDeletion(t *testing.T) {
	root, child := strings.Repeat("a", 64), strings.Repeat("b", 64)
	snapshot := &accountManagementSyncSnapshot{
		profile: accountManagementProfile{RootFingerprint: root},
		wallets: map[string]account.ManagedWallet{
			root: syncTestWallet(root, "Root"), child: syncTestWallet(child, "Child"),
		},
	}
	state := account.ManagedState{Version: account.ManagedStateVersion, RootFingerprint: root, Revision: 3,
		Wallets: []account.ManagedWallet{syncTestWallet(root, "Root"), {Fingerprint: child, Revision: 3, Deleted: true}}}
	target, changed, err := buildAccountManagedStateTarget(state, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if changed || !findManagedWallet(&target, child).Deleted {
		t.Fatalf("remote deletion was incorrectly resurrected: %+v", target)
	}
}

func TestBuildAccountManagedStateTargetMetadataThenDeleteDoesNotRequireLocalWallet(t *testing.T) {
	root, child := strings.Repeat("a", 64), strings.Repeat("b", 64)
	remote := account.ManagedState{
		Version: account.ManagedStateVersion, RootFingerprint: root, Revision: 4,
		Wallets: []account.ManagedWallet{
			syncTestWallet(root, "Root"), syncTestWallet(child, "Child"),
		},
	}
	snapshot := &accountManagementSyncSnapshot{
		profile: accountManagementProfile{RootFingerprint: root},
		wallets: map[string]account.ManagedWallet{
			root: syncTestWallet(root, "Root"),
		},
		pending: []accountManagementMutation{
			{ID: "rename", Type: accountMutationWalletName, Fingerprint: child, Name: "Renamed"},
			{ID: "delete", Type: accountMutationDeleteWallet, Fingerprint: child},
		},
	}
	target, changed, err := buildAccountManagedStateTarget(remote, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	deleted := findManagedWallet(&target, child)
	if !changed || target.Revision != 5 || deleted == nil || !deleted.Deleted {
		t.Fatalf("metadata-then-delete was not collapsed to a tombstone: %+v", target)
	}
}

func TestPendingAfterCommittedSnapshotPreservesConcurrentEdit(t *testing.T) {
	original := accountManagementMutation{ID: "m1", Type: accountMutationMetadata, Fingerprint: "f", Name: "old"}
	updated := original
	updated.Name = "new"
	remaining := pendingAfterCommittedSnapshot([]accountManagementMutation{updated}, []accountManagementMutation{original})
	if len(remaining) != 1 || remaining[0].Name != "new" {
		t.Fatalf("concurrent mutation was cleared: %+v", remaining)
	}
}

func TestBuildAccountManagedStateTargetPreservesRemoteAccountDuringLocalRename(t *testing.T) {
	root := strings.Repeat("a", 64)
	remote := syncTestWallet(root, "Remote")
	remote.AccountCount = 4
	remote.SubAccounts = []account.SubAccount{
		{Index: 0, Name: "Account 1"},
		{Index: 1, Name: "Account 2"},
		{Index: 2, Name: "Cold Savings", DID: "did:root:2"},
		{Index: 3, Name: "Travel", DID: "did:root:3"},
	}
	local := syncTestWallet(root, "Primary Vault Renamed")
	local.AccountCount = 3
	local.SubAccounts = []account.SubAccount{
		{Index: 0, Name: "Account 1"},
		{Index: 1, Name: "Account 2"},
		{Index: 2, Name: "Cold Savings", DID: "did:root:2"},
	}
	snapshot := &accountManagementSyncSnapshot{
		profile: accountManagementProfile{RootFingerprint: root},
		wallets: map[string]account.ManagedWallet{root: local},
		pending: []accountManagementMutation{{ID: "rename", Type: accountMutationWalletName, Fingerprint: root}},
	}
	target, changed, err := buildAccountManagedStateTarget(account.ManagedState{
		Version: account.ManagedStateVersion, RootFingerprint: root, Revision: 3,
		Wallets: []account.ManagedWallet{remote},
	}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	merged := findManagedWallet(&target, root)
	if !changed || target.Revision != 4 || merged == nil || merged.Name != "Primary Vault Renamed" ||
		merged.AccountCount != 4 || len(merged.SubAccounts) != 4 || merged.SubAccounts[3].Name != "Travel" {
		t.Fatalf("remote account was overwritten by local rename: %+v", target)
	}
}

func TestCommitAccountManagedStateKeepsStatusPointerStable(t *testing.T) {
	const mnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	walletValue := NewInternalWalletWithMnemonic(mnemonic, "", GetChainParam())
	if walletValue == nil {
		t.Fatal("create test wallet")
	}
	fingerprint := walletFingerprint(walletValue)
	walletID := walletValue.GetId()
	originalStatus := &Status{
		SoftwareVer: SOFTWARE_VERSION, DBver: DB_VERSION, CurrentChain: "testnet",
		CurrentWallet: walletID, CurrentAccount: 0,
		BlockHashMapL1: map[int]string{1: "l1"}, BlockHashMapL2: map[int]string{2: "l2"},
	}
	profile := &accountManagementProfile{
		AccountID: "test-account", RootFingerprint: fingerprint, ManagedDataGeneration: 1,
	}
	manager := &Manager{
		db: newMemoryKVDB(), status: originalStatus, wallet: walletValue,
		walletInfoMap: map[int64]*WalletInfo{walletID: {
			WalletInDB: WalletInDB{
				Id: walletID, Accounts: 1, Type: WALLET_TYPE_MNEMONIC, Name: "Local",
				AccountNames: map[uint32]string{0: "Account 1"}, AccountDIDs: map[uint32]string{},
			},
			Wallet: walletValue,
		}},
		accountProfile: profile,
	}
	remoteWallet := syncTestWallet(fingerprint, "Remote")
	snapshot := &accountManagementSyncSnapshot{
		profile: *profile,
		wallets: map[string]account.ManagedWallet{fingerprint: syncTestWallet(fingerprint, "Local")},
	}
	state := account.ManagedState{
		Version: account.ManagedStateVersion, RootFingerprint: fingerprint, Revision: 2,
		Wallets: []account.ManagedWallet{remoteWallet},
	}

	manager.mutex.Lock()
	_, _, err := manager.commitAccountManagedStateLocked(state, snapshot, []byte("state"), nil)
	manager.mutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if manager.status != originalStatus {
		t.Fatal("account-management sync replaced the live status pointer")
	}
	if manager.status.TotalWallet != 1 || manager.walletInfoMap[walletID].Name != "Remote" {
		t.Fatalf("account-management state was not applied: status=%+v wallet=%+v",
			manager.status, manager.walletInfoMap[walletID])
	}
}
