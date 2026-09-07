package wallet

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func TestAccountManagedCommitRejectsStaleLocalGeneration(t *testing.T) {
	manager := &Manager{
		accountProfile:    &accountManagementProfile{AccountID: "account"},
		accountGeneration: 7,
	}
	snapshot := &accountManagementSyncSnapshot{
		profile:    accountManagementProfile{AccountID: "account"},
		generation: 7,
	}
	manager.mutex.Lock()
	manager.bumpAccountGenerationLocked()
	_, _, err := manager.commitPreparedAccountManagedStateLocked(
		&accountManagedStateCommit{}, snapshot, false)
	manager.mutex.Unlock()
	if !errors.Is(err, errAccountSnapshotChanged) {
		t.Fatalf("stale commit error=%v", err)
	}
}

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

func TestFinalizePublishedAccountManagedStateClearsAcknowledgedPending(t *testing.T) {
	pending := accountManagementMutation{
		ID: "add", Type: accountMutationAddWallet, Fingerprint: "wallet",
	}
	profile := &accountManagementProfile{
		AccountID: "account", RootFingerprint: "root", Pending: []accountManagementMutation{pending},
		ManagedDataDirty: true, ManagedDataGeneration: 3,
	}
	manager := &Manager{db: newMemoryKVDB(), accountProfile: profile, accountGeneration: 7}
	snapshot := &accountManagementSyncSnapshot{
		profile: *profile, generation: 7, pending: []accountManagementMutation{pending},
	}
	snapshot.profile.RecordTTL = 777
	managed := &accountManagedDataSnapshot{
		Bundle: account.ManagedDataBundle{Revision: 4}, Hash: "managed-hash", Envelope: []byte("managed"),
	}
	followUp, err := manager.finalizePublishedAccountManagedState(
		account.ManagedState{Revision: 5}, snapshot, []byte("state"), managed)
	if err != nil {
		t.Fatal(err)
	}
	if followUp || len(manager.accountProfile.Pending) != 0 || manager.accountProfile.ManagedDataDirty {
		t.Fatalf("acknowledged state remained dirty: followUp=%v profile=%+v", followUp, manager.accountProfile)
	}
	if manager.accountProfile.RecordTTL != 777 || manager.accountProfile.StateSeq != 5 ||
		manager.accountProfile.ManagedDataRevision != 4 ||
		manager.accountProfile.ManagedDataHash != "managed-hash" {
		t.Fatalf("confirmed metadata was not stored: %+v", manager.accountProfile)
	}
	var stored accountManagementProfile
	raw, err := manager.db.Read(accountManagementProfileKey())
	if err != nil {
		t.Fatal(err)
	}
	if err := DecodeFromBytes(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.RecordTTL != snapshot.profile.RecordTTL {
		t.Fatalf("confirmed retention was not persisted: profile=%+v", stored)
	}
}

func TestInitialTemporaryAccountPublishUsesConfirmedTTLForRootWrapper(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	remote := newRGB11MemoryDKVSHTTP()
	manager := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(manager, remote)
	if _, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	if err := manager.InitializeAccountManagement("password"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	if manager.accountProfile.RecordTTL == 0 {
		t.Fatal("confirmed temporary retention was not committed to the active profile")
	}
	client, err := manager.ensureDKVSManager().primaryClient()
	if err != nil {
		t.Fatal(err)
	}
	store := &dkvsStore{manager: manager.dkvs, client: client}
	if err := manager.syncAccountRootWrapper(store); err != nil {
		t.Fatalf("root wrapper background job: %v", err)
	}
	root, err := manager.accountManagementRootWallet()
	if err != nil {
		t.Fatal(err)
	}
	wrapperKey, err := accountRootWrapperKey(root)
	if err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	wrapper := cloneRGB11DKVSRecord(remote.records[wrapperKey])
	remote.mu.Unlock()
	if wrapper == nil || wrapper.TTL != manager.accountProfile.RecordTTL {
		t.Fatalf("root wrapper retention mismatch: record=%+v profile=%+v", wrapper, manager.accountProfile)
	}
	proof, err := dkvsindexer.ParseFeeProof(wrapper.FeeProof)
	if err != nil || proof.Mode != dkvsindexer.FeeModeFreeLocal {
		t.Fatalf("root wrapper is not FREE_LOCAL: proof=%+v err=%v", proof, err)
	}
	payload, err := openAccountRootWrapper(root, _chain,
		manager.accountProfile.AccountID, wrapper.Value)
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(payload.Secret)
	if !bytes.Equal(payload.Secret, manager.accountSecret) ||
		!accountRootWrapperMetadataMatchesProfile(payload, *manager.accountProfile) {
		t.Fatal("root wrapper does not contain the confirmed temporary profile")
	}
	if code, message, _ := manager.dkvs.lastSyncErrorStatus(); code != "" || message != "" {
		t.Fatalf("root wrapper left a background sync error: code=%q message=%q", code, message)
	}
}

func TestUnchangedAccountManagedSyncDoesNotCreateImportMarker(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	remote := newRGB11MemoryDKVSHTTP()
	manager := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(manager, remote)
	if _, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	if err := manager.InitializeAccountManagement("password"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	originalDB := manager.db
	markerWrite := errors.New("unchanged sync wrote recovery marker")
	manager.db = &managedImportFaultDB{KVDB: originalDB, writeErr: markerWrite}
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatalf("unchanged sync failed: %v", err)
	}
	manager.db = originalDB
	if err := manager.checkAccountManagedDataImport(); err != nil {
		t.Fatalf("unchanged sync retained recovery marker: %v", err)
	}
}

func TestFinalizePublishedAccountManagedStateClearsACKedMutationAndKeepsProviderDirty(t *testing.T) {
	pending := accountManagementMutation{
		ID: "add", Type: accountMutationAddWallet, Fingerprint: "wallet",
	}
	profile := &accountManagementProfile{
		AccountID: "account", RootFingerprint: "root", Pending: []accountManagementMutation{pending},
		ManagedDataDirty: true, ManagedDataGeneration: 4,
	}
	localWallet := &WalletInfo{WalletInDB: WalletInDB{Accounts: 2}}
	manager := &Manager{
		db: newMemoryKVDB(), accountProfile: profile, accountGeneration: 8,
		walletInfoMap: map[int64]*WalletInfo{1: localWallet},
	}
	snapshotProfile := *profile
	snapshotProfile.ManagedDataGeneration = 3
	snapshot := &accountManagementSyncSnapshot{
		profile: snapshotProfile, generation: 7, pending: []accountManagementMutation{pending},
	}
	managed := &accountManagedDataSnapshot{
		Bundle: account.ManagedDataBundle{Revision: 4}, Hash: "managed-hash", Envelope: []byte("managed"),
	}
	followUp, err := manager.finalizePublishedAccountManagedState(
		account.ManagedState{Revision: 5}, snapshot, []byte("state"), managed)
	if err != nil {
		t.Fatal(err)
	}
	if !followUp || len(manager.accountProfile.Pending) != 0 || !manager.accountProfile.ManagedDataDirty {
		t.Fatalf("ACK bookkeeping mismatch: followUp=%v profile=%+v", followUp, manager.accountProfile)
	}
	if manager.walletInfoMap[1] != localWallet || manager.walletInfoMap[1].Accounts != 2 {
		t.Fatalf("publish ACK replaced live wallet state: %+v", manager.walletInfoMap[1])
	}
	var stored accountManagementProfile
	raw, readErr := manager.db.Read(accountManagementProfileKey())
	if readErr != nil {
		t.Fatal(readErr)
	}
	if err := DecodeFromBytes(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Pending) != 0 || !stored.ManagedDataDirty || stored.StateSeq != 5 {
		t.Fatalf("persisted ACK state is incorrect: %+v", stored)
	}
}

func TestAccountManagedApplicationGateQueuesChangesUntilPutAck(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	remote := newRGB11MemoryDKVSHTTP()
	manager := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(manager, remote)
	if _, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	if err := manager.InitializeAccountManagement("password"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	walletID, _, err := manager.CreateWallet("password")
	if err != nil {
		t.Fatal(err)
	}
	if len(manager.accountProfile.Pending) != 1 {
		t.Fatalf("wallet creation did not queue one mutation: %+v", manager.accountProfile)
	}
	fingerprint := walletFingerprint(manager.walletInfoMap[walletID].Wallet)
	gate := make(chan struct{})
	remote.mu.Lock()
	remote.postGate = gate
	remote.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		done <- manager.SyncAccountManagementState(context.Background())
	}()
	client, err := manager.ensureDKVSManager().primaryClient()
	if err != nil {
		t.Fatal(err)
	}
	store := newDKVSReplicaStore(manager.db)
	deadline := time.Now().Add(3 * time.Second)
	for {
		select {
		case syncErr := <-done:
			t.Fatalf("account PUT finished before the gate: %v", syncErr)
		default:
		}
		entries, loadErr := store.LoadOutbox(client.replicaNamespace)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if len(entries) == 1 && entries[0].State == DKVSOutboxInflight {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("account PUT did not enter the outbox")
		}
		time.Sleep(time.Millisecond)
	}
	remote.mu.Lock()
	snapshotsAtInflight := remote.snapshotCalls
	remote.mu.Unlock()

	ensureDone := make(chan error, 1)
	go func() {
		ensureDone <- manager.EnsureAccount(walletID, 1, "Savings", "did:savings")
	}()
	select {
	case ensureErr := <-ensureDone:
		t.Fatalf("wallet mutation bypassed the application sync gate: %v", ensureErr)
	case <-time.After(50 * time.Millisecond):
	}
	if manager.walletInfoMap[walletID].Accounts != 1 {
		t.Fatalf("wallet changed before PUT ACK: %+v", manager.walletInfoMap[walletID])
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-ensureDone; err != nil {
		t.Fatal(err)
	}
	if manager.walletInfoMap[walletID].Accounts != 2 ||
		manager.walletInfoMap[walletID].AccountNames[1] != "Savings" {
		t.Fatalf("queued wallet mutation was not applied after ACK: %+v", manager.walletInfoMap[walletID])
	}
	if len(manager.accountProfile.Pending) != 1 || !manager.accountProfile.ManagedDataDirty {
		t.Fatalf("PUT ACK cleared the concurrent overlay: %+v", manager.accountProfile)
	}
	if _, err := manager.db.Read(accountManagedDataImportKey()); err == nil {
		t.Fatal("own PUT ACK created an import marker")
	}
	remote.mu.Lock()
	if remote.snapshotCalls != snapshotsAtInflight {
		t.Fatalf("own PUT downloaded a snapshot: before=%d after=%d",
			snapshotsAtInflight, remote.snapshotCalls)
	}
	remote.mu.Unlock()

	remote.mu.Lock()
	snapshotsBeforeSecondPut := remote.snapshotCalls
	remote.mu.Unlock()
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(manager.accountProfile.Pending) != 0 || manager.accountProfile.ManagedDataDirty {
		t.Fatalf("second serialized PUT did not clear pending: %+v", manager.accountProfile)
	}
	root, err := manager.accountManagementRootWallet()
	if err != nil {
		t.Fatal(err)
	}
	stateKey, err := manager.accountManagedStateKey(root)
	if err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	stateRecord := cloneRGB11DKVSRecord(remote.records[stateKey])
	if remote.snapshotCalls != snapshotsBeforeSecondPut {
		remote.mu.Unlock()
		t.Fatalf("second own PUT downloaded a snapshot: before=%d after=%d",
			snapshotsBeforeSecondPut, remote.snapshotCalls)
	}
	remote.mu.Unlock()
	if stateRecord == nil {
		t.Fatal("server account state is missing")
	}
	state, err := account.OpenManagedState(manager.accountSecret,
		manager.accountProfile.AccountID, stateRecord.Value)
	if err != nil {
		t.Fatal(err)
	}
	remoteWallet := findManagedWallet(&state, fingerprint)
	if remoteWallet == nil || remoteWallet.AccountCount != 2 ||
		len(remoteWallet.SubAccounts) != 2 || remoteWallet.SubAccounts[1].Name != "Savings" {
		t.Fatalf("server did not reach the latest local state: %+v", remoteWallet)
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

func TestCommitAccountManagedStateSelectsRootWhenCurrentWalletIsDeleted(t *testing.T) {
	rootWallet := NewInternalWalletWithMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"", GetChainParam())
	childWallet := NewInternalWalletWithMnemonic(
		"legal winner thank year wave sausage worth useful legal winner thank yellow",
		"", GetChainParam())
	if rootWallet == nil || childWallet == nil {
		t.Fatal("create test wallets")
	}
	rootID, childID := rootWallet.GetId(), childWallet.GetId()
	rootFingerprint, childFingerprint := walletFingerprint(rootWallet), walletFingerprint(childWallet)
	profile := &accountManagementProfile{AccountID: "test-account", RootFingerprint: rootFingerprint}
	manager := &Manager{
		db: newMemoryKVDB(), wallet: childWallet,
		status: &Status{CurrentWallet: childID, CurrentAccount: 0},
		walletInfoMap: map[int64]*WalletInfo{
			rootID: {
				WalletInDB: WalletInDB{Id: rootID, Accounts: 1, Type: WALLET_TYPE_MNEMONIC,
					Name: "Root", AccountNames: map[uint32]string{0: "Account 1"}, AccountDIDs: map[uint32]string{}},
				Wallet: rootWallet,
			},
			childID: {
				WalletInDB: WalletInDB{Id: childID, Accounts: 1, Type: WALLET_TYPE_MNEMONIC,
					Name: "Child", AccountNames: map[uint32]string{0: "Account 1"}, AccountDIDs: map[uint32]string{}},
				Wallet: childWallet,
			},
		},
		accountProfile: profile,
	}
	snapshot := &accountManagementSyncSnapshot{
		profile: *profile,
		wallets: map[string]account.ManagedWallet{
			rootFingerprint:  syncTestWallet(rootFingerprint, "Root"),
			childFingerprint: syncTestWallet(childFingerprint, "Child"),
		},
	}
	state := account.ManagedState{
		Version: account.ManagedStateVersion, RootFingerprint: rootFingerprint, Revision: 2,
		Wallets: []account.ManagedWallet{
			syncTestWallet(rootFingerprint, "Root"),
			{Fingerprint: childFingerprint, Revision: 2, Deleted: true},
		},
	}

	_, _, err := manager.commitAccountManagedStateForSync(state, snapshot, []byte("state"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if manager.wallet != rootWallet || manager.status.CurrentWallet != rootID {
		t.Fatalf("background wallet switch did not select root wallet: wallet=%d",
			manager.status.CurrentWallet)
	}
}
