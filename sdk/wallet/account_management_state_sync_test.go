package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func syncTestWallet(fingerprint, name string) account.ManagedWallet {
	return account.ManagedWallet{
		Fingerprint: fingerprint, Name: name,
		Type: account.WalletMnemonic, Secret: []byte("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"),
		AccountCount: 1, SubAccounts: []account.SubAccount{{Index: 0, Name: "Account 1"}},
	}
}

func activateSyncFixture(t *testing.T, manager *Manager, remote *rgb11MemoryDKVSHTTP, password string) {
	t.Helper()
	client := newRGB11MessageNodeClient(remote)
	manager.serverNode = NewNode(client, "message.test", SERVER_NODE,
		client.CoreNodePubKey(), client.CoreNodePubKey())
	pkg, auth, secret := rereviewRecoveryPackage(t, manager, password)
	require.NoError(t, manager.ActivateAccountManagement(secret, password, *auth,
		pkg.Envelope.Locator, "account://"+pkg.Envelope.Locator.PackageID))
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
	activateSyncFixture(t, manager, remote, "password")
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
	manager, provider := managedImportTestSource(t, newRGB11MemoryDKVSHTTP())
	imports := provider.imports
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	require.Equal(t, imports, provider.imports, "unchanged sync must not reimport a module")
	values, err := manager.listAccountLocal(manager.accountProfile.AccountID)
	require.NoError(t, err)
	for _, value := range values {
		require.False(t, value.Restoring, "unchanged sync retained a module restore marker")
	}
}

func TestAccountManagedPutAckPreservesChangesDuringNetworkWait(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	remote := newRGB11MemoryDKVSHTTP()
	manager := newAccountManagementAutoTestManager(t)
	audit := &accountACKReadAuditDB{KVDB: manager.db, remote: remote, starts: make(map[string]int)}
	manager.db = audit
	configureRGB11DKVSTestManager(manager, remote)
	if _, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	if err := manager.InitializeAccountManagement("password"); err != nil {
		t.Fatal(err)
	}
	activateSyncFixture(t, manager, remote, "password")
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	rootWalletID := manager.status.CurrentWallet
	walletID, _, err := manager.CreateWallet("password")
	if err != nil {
		t.Fatal(err)
	}
	if len(manager.accountProfile.Pending) != 1 {
		t.Fatalf("wallet creation did not queue one mutation: %+v", manager.accountProfile)
	}
	oldMutationID := manager.accountProfile.Pending[0].ID
	oldDataGeneration := manager.accountProfile.ManagedDataGeneration
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
	// Signing/snapshot preparation is slower under -race; this bound waits
	// for the fixture gate, not for the network response.
	deadline := time.Now().Add(10 * time.Second)
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
	audit.namespace = client.replicaNamespace
	audit.armed.Store(true)

	ensureDone := make(chan error, 1)
	go func() {
		ensureDone <- manager.EnsureAccount(walletID, 1, "Savings", "did:savings")
	}()
	select {
	case ensureErr := <-ensureDone:
		if ensureErr != nil {
			t.Fatal(ensureErr)
		}
	case <-time.After(2 * time.Second):
		close(gate)
		<-done
		t.Fatal("local wallet mutation blocked behind remote PUT")
	}
	if len(manager.accountProfile.Pending) != 1 || manager.accountProfile.Pending[0].ID == oldMutationID ||
		manager.accountProfile.ManagedDataGeneration <= oldDataGeneration {
		t.Fatal("concurrent edit did not version the pending add and dirty generation")
	}
	selectionDone := make(chan error, 1)
	go func() {
		if err := manager.SwitchWallet(rootWalletID, "password"); err != nil {
			selectionDone <- err
			return
		}
		if err := manager.SwitchWallet(walletID, "password"); err != nil {
			selectionDone <- err
			return
		}
		manager.SwitchAccount(1)
		selectionDone <- nil
	}()
	select {
	case err := <-selectionDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		close(gate)
		<-done
		t.Fatal("wallet/account selection blocked behind remote PUT")
	}
	manager.markAccountManagedDataDirtyDeferred(rgb11AccountManagedProviderID)
	newDataGeneration := manager.accountProfile.ManagedDataGeneration
	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if manager.walletInfoMap[walletID].Accounts != 2 ||
		manager.walletInfoMap[walletID].AccountNames[1] != "Savings" {
		t.Fatalf("PUT ACK overwrote the concurrent wallet mutation: %+v", manager.walletInfoMap[walletID])
	}
	if len(manager.accountProfile.Pending) != 1 || !manager.accountProfile.ManagedDataDirty {
		t.Fatalf("PUT ACK cleared the concurrent overlay: %+v", manager.accountProfile)
	}
	if manager.status.CurrentWallet != walletID || manager.status.CurrentAccount != 1 ||
		manager.accountProfile.ManagedDataGeneration != newDataGeneration {
		t.Fatal("PUT ACK overwrote current selection or provider generation")
	}
	var stored accountManagementProfile
	raw, err := manager.db.Read(accountManagementProfileKey())
	if err != nil {
		t.Fatal(err)
	}
	if err := DecodeFromBytes(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Pending) != 1 || stored.Pending[0].ID != manager.accountProfile.Pending[0].ID ||
		!stored.ManagedDataDirty || stored.ManagedDataGeneration != newDataGeneration {
		t.Fatal("ACK persisted a different pending overlay or data generation")
	}
	if _, err := manager.db.Read(accountManagedDataImportKey()); err == nil {
		t.Fatal("own PUT ACK created an import marker")
	}
	firstACKs := audit.checks.Load()
	require.Positive(t, firstACKs, "the real first ACK completion window must be observed")
	require.False(t, audit.extra.Load(), "ACK completion downloaded a snapshot")
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(manager.accountProfile.Pending) != 0 || manager.accountProfile.ManagedDataDirty {
		t.Fatalf("second serialized PUT did not clear pending: %+v", manager.accountProfile)
	}
	walletModule, err := accountEntry(manager.accountProfile.AccountID, "wallets", AccountManagedDataGlobalScope)
	if err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	moduleRecord := cloneRGB11DKVSRecord(remote.records[walletModule.Key])
	remote.mu.Unlock()
	require.Greater(t, audit.checks.Load(), firstACKs, "the real second ACK completion window must be observed")
	require.False(t, audit.extra.Load(), "second ACK completion downloaded a snapshot")
	if moduleRecord == nil {
		t.Fatal("server wallet module is missing")
	}
	state, err := account.OpenManagedState(manager.accountSecret,
		manager.accountProfile.AccountID, moduleRecord.Value)
	if err != nil {
		t.Fatal(err)
	}
	remoteWallet := findManagedWallet(&state, fingerprint)
	if remoteWallet == nil || remoteWallet.AccountCount != 2 ||
		len(remoteWallet.SubAccounts) != 2 || remoteWallet.SubAccounts[1].Name != "Savings" {
		t.Fatalf("server did not reach the latest local state: %+v", remoteWallet)
	}
}

// Observe the actual local ACK window: durable ACK installation through its
// outbox deletion. Normal receiving before the next PUT is outside this window.
type accountACKReadAuditDB struct {
	indexercommon.KVDB
	remote    *rgb11MemoryDKVSHTTP
	namespace string
	armed     atomic.Bool
	checks    atomic.Int32
	extra     atomic.Bool
	mu        sync.Mutex
	starts    map[string]int
}

func (d *accountACKReadAuditDB) capture(keys []string) {
	if !d.armed.Load() || len(keys) == 0 {
		return
	}
	entries, err := newDKVSReplicaStore(d.KVDB).LoadOutbox(d.namespace)
	if err != nil {
		d.extra.Store(true)
		return
	}
	d.remote.mu.Lock()
	count := d.remote.snapshotCalls
	d.remote.mu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, entry := range entries {
		if entry.State != DKVSOutboxAcknowledged || entry.OriginDomain != accountKVOutboxDomain {
			continue
		}
		key := string(dkvsOutboxKey(d.namespace, entry.RequestID))
		for _, written := range keys {
			if key == written {
				if _, exists := d.starts[key]; !exists {
					d.starts[key] = count
				}
			}
		}
	}
}

func (d *accountACKReadAuditDB) Write(key, value []byte) error {
	err := d.KVDB.Write(key, value)
	if err == nil && bytes.HasPrefix(key, dkvsOutboxPrefix) {
		d.capture([]string{string(key)})
	}
	return err
}

func (d *accountACKReadAuditDB) Delete(key []byte) error {
	d.mu.Lock()
	before, seen := d.starts[string(key)]
	delete(d.starts, string(key))
	d.mu.Unlock()
	if seen {
		d.remote.mu.Lock()
		after := d.remote.snapshotCalls
		d.remote.mu.Unlock()
		if after != before {
			d.extra.Store(true)
		}
		d.checks.Add(1)
	}
	return d.KVDB.Delete(key)
}

type accountACKReadAuditBatch struct {
	indexercommon.WriteBatch
	owner *accountACKReadAuditDB
	keys  []string
}

func (d *accountACKReadAuditDB) NewAtomicWriteBatch() indexercommon.WriteBatch {
	return &accountACKReadAuditBatch{WriteBatch: d.KVDB.(indexercommon.AtomicWriteBatchDB).NewAtomicWriteBatch(), owner: d}
}
func (b *accountACKReadAuditBatch) Put(key, value []byte) error {
	if bytes.HasPrefix(key, dkvsOutboxPrefix) {
		b.keys = append(b.keys, string(key))
	}
	return b.WriteBatch.Put(key, value)
}
func (b *accountACKReadAuditBatch) Flush() error {
	err := b.WriteBatch.Flush()
	if err == nil {
		b.owner.capture(b.keys)
	}
	return err
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
	state := account.ManagedState{
		Version: account.ManagedStateVersion, RootFingerprint: fingerprint,
		Wallets: []account.ManagedWallet{remoteWallet},
	}

	manager.utxoLockerL1 = NewUtxoLocker(manager.db, nil, L1_NETWORK_BITCOIN)
	rgb, err := newRGB11Manager(manager, manager.db, manager.utxoLockerL1, nil)
	require.NoError(t, err)
	manager.rgbManager = rgb
	err = manager.importAccountWallets(state)
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
	database := newMemoryKVDB()
	manager := &Manager{
		db: database, wallet: childWallet,
		status:       &Status{CurrentWallet: childID, CurrentAccount: 0},
		utxoLockerL1: NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN),
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
	rgbManager, err := newRGB11Manager(manager, database, manager.utxoLockerL1, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager.rgbManager = rgbManager
	state := account.ManagedState{
		Version: account.ManagedStateVersion, RootFingerprint: rootFingerprint,
		Wallets: []account.ManagedWallet{
			syncTestWallet(rootFingerprint, "Root"),
			{Fingerprint: childFingerprint, Deleted: true},
		},
	}

	err = manager.importAccountWallets(state)
	if err != nil {
		t.Fatal(err)
	}
	if manager.wallet != rootWallet || manager.status.CurrentWallet != rootID {
		t.Fatalf("background wallet switch did not select root wallet: wallet=%d",
			manager.status.CurrentWallet)
	}
}

// Public SDK operations and the existing signed-record transport fixture.
func accountTTLNoopFixture(t *testing.T, wrapDB ...func(indexercommon.KVDB) indexercommon.KVDB) (*Manager, *rgb11MemoryDKVSHTTP, *dkvsStore, []string) {
	t.Helper()
	return accountTTLWithSeed(t, nil, wrapDB...)
}

func accountTTLWithSeed(t *testing.T, seed func(*Manager, *rgb11MemoryDKVSHTTP),
	wrapDB ...func(indexercommon.KVDB) indexercommon.KVDB) (*Manager, *rgb11MemoryDKVSHTTP, *dkvsStore, []string) {
	t.Helper()
	remote := newRGB11MemoryDKVSHTTP()
	manager := newAccountManagementAutoTestManager(t)
	for _, wrap := range wrapDB {
		manager.db = wrap(manager.db)
	}
	configureRGB11DKVSTestManager(manager, remote)
	client := newRGB11MessageNodeClient(remote)
	manager.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
	_, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password")
	require.NoError(t, err)
	require.NoError(t, manager.InitializeAccountManagement("password"))
	// Discover verified absence before allowing pre-activation local edits.
	require.NoError(t, manager.refreshDKVSRegistrations())
	_, discoveryErr := manager.syncDKVSOnce()
	require.NoError(t, discoveryErr)
	if seed != nil {
		seed(manager, remote)
	}
	pkg, auth, secret := rereviewRecoveryPackage(t, manager)
	before := manager.GetAccountManagementStatus()
	err = manager.ActivateAccountManagement(secret, "password", *auth, pkg.Envelope.Locator, "account://"+pkg.Envelope.Locator.PackageID)
	if err != nil {
		after := manager.GetAccountManagementStatus()
		t.Fatalf("activate local-only account: %v (before local=%t pending=%t; after local=%t pending=%t dkvs=%s)",
			err, before.LocalOnly, before.ActivationPending, after.LocalOnly, after.ActivationPending, after.DKVSStatus)
	}
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	// Activation can add mailbox/service prefixes after the initial account
	// state sync. Complete their online baseline before managed writes.
	dkvsClient, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	require.NoError(t, manager.dkvs.ensureCurrentSubscription(dkvsClient))
	id := manager.GetCurrentWalletId()
	if err := manager.UpdateWalletName(id, "A"); err != nil {
		status := manager.GetAccountManagementStatus()
		t.Fatalf("update after activation: %v (dkvs=%s writable=%t dirty=%t pending=%d)",
			err, status.DKVSStatus, status.DKVSWriteAllowed, status.ManagedDataDirty, status.DKVSPendingRequests)
	}
	require.NoError(t, manager.UpdateAccountMetadata(id, 0, "A", "did:A"))
	store, err := manager.accountDKVSStore()
	require.NoError(t, err)
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	keys := accountLocalRecordKeys(t, manager)
	require.NotEmpty(t, keys)
	require.Contains(t, keys, accountWalletModuleKey(t, manager))
	for _, key := range keys {
		value, err := store.GetAuthoritative(key)
		require.NoError(t, err)
		require.NotNil(t, value)
	}
	return manager, remote, store, keys
}

func accountWalletModuleKey(t *testing.T, manager *Manager) string {
	t.Helper()
	entry, err := accountEntry(manager.accountProfile.AccountID, "wallets", AccountManagedDataGlobalScope)
	require.NoError(t, err)
	require.NotEmpty(t, entry.Key)
	return entry.Key
}

func accountLocalRecordKeys(t *testing.T, manager *Manager) []string {
	t.Helper()
	values, err := manager.listAccountLocal(manager.accountProfile.AccountID)
	require.NoError(t, err)
	available := make(map[string]bool, len(values))
	for _, value := range values {
		if value.Key != "" && value.Baseline != nil {
			available[value.Key] = true
		}
	}
	root, err := manager.accountManagementRootWallet()
	require.NoError(t, err)
	wrapperKey, err := accountRootWrapperKey(root)
	require.NoError(t, err)
	indexKey, err := account.ManagedIndexKey(manager.accountProfile.AccountID)
	require.NoError(t, err)
	keys := make([]string, 0, len(available))
	for _, key := range []string{accountWalletModuleKey(t, manager), indexKey, wrapperKey} {
		if available[key] {
			keys = append(keys, key)
			delete(available, key)
		}
	}
	others := make([]string, 0, len(available))
	for key := range available {
		others = append(others, key)
	}
	sort.Strings(others)
	keys = append(keys, others...)
	return keys
}

type accountProfileWriteCountDB struct {
	indexercommon.KVDB
	writes atomic.Int32
}

func (d *accountProfileWriteCountDB) Write(key, value []byte) error {
	if bytes.Equal(key, accountManagementProfileKey()) {
		d.writes.Add(1)
	}
	return d.KVDB.Write(key, value)
}
func TestAccountTemporaryTTLPolicyChangeConverges(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, remote, store, keys := accountTTLNoopFixture(t)
	locator := manager.GetAccountManagementStatus().PublicLocator
	counter := &accountProfileWriteCountDB{KVDB: manager.db}
	manager.db = counter
	remote.mu.Lock()
	remote.freeLocal.MaxTTL *= 2
	ttl := remote.freeLocal.MaxTTL
	remote.mu.Unlock()
	require.Greater(t, ttl, uint64(144), "the test server ceiling must increase beyond the user's confirmed policy")
	require.NoError(t, manager.UpdateWalletName(manager.GetCurrentWalletId(), "After TTL change"))
	counter.writes.Store(0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, manager.SyncAccountManagementState(ctx))
	require.LessOrEqual(t, counter.writes.Load(), int32(2), "same configuration repeatedly persisted")
	require.Equal(t, uint64(144), manager.accountProfile.RecordTTL,
		"a larger server MaxTTL is a ceiling change, not authorization to rewrite the user's retention policy")
	walletModule, err := manager.readAccountLocal(manager.GetAccountManagementStatus().AccountID,
		"wallets", AccountManagedDataGlobalScope)
	require.NoError(t, err)
	require.NotNil(t, walletModule)
	require.Equal(t, walletModule.Generation, walletModule.Confirmed)
	require.NotNil(t, walletModule.Baseline)
	for _, key := range keys {
		value, err := store.GetAuthoritative(key)
		require.NoError(t, err)
		require.Equal(t, uint64(144), value.record.TTL,
			"the previously confirmed user retention must remain unchanged")
	}
	require.Equal(t, locator, manager.GetAccountManagementStatus().PublicLocator)
	require.NoError(t, manager.SyncAccountRootWrapper(context.Background()))
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	require.Equal(t, uint64(144), manager.accountProfile.RecordTTL)
}

func TestAccountRevertedMetadataClearsPending(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	for _, kind := range []string{"wallet-name", "account-name", "DID"} {
		t.Run(kind, func(t *testing.T) {
			manager, _, store, keys := accountTTLNoopFixture(t)
			id := manager.GetCurrentWalletId()
			mutate := func(value string) error {
				switch kind {
				case "wallet-name":
					return manager.UpdateWalletName(id, value)
				case "account-name":
					return manager.UpdateAccountMetadata(id, 0, value, "did:A")
				default:
					return manager.UpdateAccountMetadata(id, 0, "A", "did:"+value)
				}
			}
			before := map[string]*dkvsValue{}
			for _, key := range keys {
				value, err := store.GetAuthoritative(key)
				require.NoError(t, err)
				before[key] = value
			}
			walletModule, err := manager.readAccountLocal(manager.GetAccountManagementStatus().AccountID,
				"wallets", AccountManagedDataGlobalScope)
			require.NoError(t, err)
			require.NotNil(t, walletModule)
			require.NotNil(t, walletModule.Baseline)
			require.NoError(t, mutate("B"))
			require.NoError(t, mutate("A"))
			pendingModule, err := manager.readAccountLocal(manager.GetAccountManagementStatus().AccountID,
				"wallets", AccountManagedDataGlobalScope)
			require.NoError(t, err)
			require.Greater(t, pendingModule.Generation, pendingModule.Confirmed,
				"reverted local edits remain visible as an unconfirmed module generation until sync")
			require.Equal(t, walletModule.Baseline.Value, pendingModule.Value,
				"reverting the edit must restore the actual module payload")
			require.NoError(t, manager.SyncAccountManagementState(context.Background()))
			confirmedModule, err := manager.readAccountLocal(manager.GetAccountManagementStatus().AccountID,
				"wallets", AccountManagedDataGlobalScope)
			require.NoError(t, err)
			require.Equal(t, confirmedModule.Generation, confirmedModule.Confirmed)
			require.NotNil(t, confirmedModule.Baseline)
			require.Equal(t, walletModule.Baseline.Seq, confirmedModule.Baseline.Seq,
				"a reverted module payload must not advance its remote sequence")
			require.Equal(t, walletModule.Baseline.Value, confirmedModule.Baseline.Value)
			for key, value := range before {
				after, err := store.GetAuthoritative(key)
				require.NoError(t, err)
				require.Equal(t, value.Seq, after.Seq, "reverted metadata must not republish an unchanged value")
				require.Equal(t, value.Hash, after.Hash)
				require.Equal(t, value.Value, after.Value)
			}
			reopened := newAccountManagementAutoTestManager(t)
			reopened.db = manager.db
			require.NoError(t, reopened.initDB())
			_, err = reopened.UnlockWallet("password")
			require.NoError(t, err)
			reopenedModule, err := reopened.readAccountLocal(reopened.GetAccountManagementStatus().AccountID,
				"wallets", AccountManagedDataGlobalScope)
			require.NoError(t, err)
			require.Equal(t, reopenedModule.Generation, reopenedModule.Confirmed)
			require.NotNil(t, reopenedModule.Baseline)
			require.Equal(t, walletModule.Baseline.Seq, reopenedModule.Baseline.Seq)
			catalog := reopened.GetWalletCatalog()
			require.Equal(t, "A", catalog[0].Name)
			require.Equal(t, "A", catalog[0].Accounts[0].Name)
			require.Equal(t, "did:A", catalog[0].Accounts[0].DID)
		})
	}
}

type accountCacheReadBarrierDB struct {
	indexercommon.KVDB
	atomicDB indexercommon.AtomicWriteBatchDB
	mu       sync.RWMutex
	key      []byte
	armed    atomic.Bool
	entered  chan struct{}
	release  chan struct{}
}

func (db *accountCacheReadBarrierDB) NewAtomicWriteBatch() indexercommon.WriteBatch {
	return db.atomicDB.NewAtomicWriteBatch()
}

func (db *accountCacheReadBarrierDB) Read(key []byte) ([]byte, error) {
	db.mu.RLock()
	match := bytes.Equal(key, db.key)
	db.mu.RUnlock()
	if match && db.armed.CompareAndSwap(true, false) {
		close(db.entered)
		<-db.release
	}
	return db.KVDB.Read(key)
}

func TestAccountEditDuringCacheRead(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	entered, release := make(chan struct{}), make(chan struct{})
	var barrier *accountCacheReadBarrierDB
	manager, _, _, _ := accountTTLNoopFixture(t, func(db indexercommon.KVDB) indexercommon.KVDB {
		atomicDB, ok := db.(indexercommon.AtomicWriteBatchDB)
		if !ok {
			t.Fatal("account cache-read fixture requires the underlying atomic batch capability")
		}
		barrier = &accountCacheReadBarrierDB{KVDB: db, atomicDB: atomicDB, entered: entered, release: release}
		return barrier
	})
	id := manager.GetCurrentWalletId()
	require.NoError(t, manager.UpdateWalletName(id, "B"))
	require.NoError(t, manager.UpdateWalletName(id, "A"))
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	store, err := manager.accountDKVSStore()
	require.NoError(t, err)
	walletKey := accountWalletModuleKey(t, manager)
	// dkvsSubscriptionRecordKey is private to wallet/dkvs. Match its exact
	// encoding: prefix + trimmed namespace + ':' + storage key.
	recordKey := append([]byte("dkvs:subscription-record:"+strings.TrimSpace(store.client.replicaNamespace)+":"),
		[]byte(walletKey)...)
	barrier.mu.Lock()
	barrier.key = recordKey
	barrier.mu.Unlock()
	barrier.armed.Store(true)
	syncCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	syncDone := make(chan error, 1)
	go func() { syncDone <- manager.SyncAccountManagementState(syncCtx) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("account sync did not reach the held confirmed subscription-record read")
	}
	editDone := make(chan error, 1)
	go func() { editDone <- manager.UpdateWalletName(id, "C") }()
	select {
	case err := <-editDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("local wallet edit blocked behind an account read")
	}
	unblock()
	require.NoError(t, <-syncDone)
	require.Equal(t, "C", manager.GetWalletCatalog()[0].Name)
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	module, err := manager.readAccountLocal(manager.accountProfile.AccountID, "wallets", AccountManagedDataGlobalScope)
	require.NoError(t, err)
	require.Equal(t, module.Generation, module.Confirmed)
	require.NotNil(t, module.Baseline)
	require.Equal(t, module.Key, module.Baseline.Key)
	store, err = manager.accountDKVSStore()
	require.NoError(t, err)
	confirmed, err := store.GetAuthoritative(module.Key)
	require.NoError(t, err)
	state, err := account.OpenManagedState(manager.accountSecret, manager.accountProfile.AccountID, confirmed.Value)
	require.NoError(t, err)
	require.Equal(t, "C", state.Wallets[0].Name)
}

func TestAccountNoopProfileFailureCanRetry(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	var fault *accountPersistenceReviewDB
	manager, backend, store, _ := accountTTLNoopFixture(t, func(db indexercommon.KVDB) indexercommon.KVDB {
		fault = &accountPersistenceReviewDB{KVDB: db, profileKey: accountManagementProfileKey()}
		return fault
	})
	id := manager.GetCurrentWalletId()
	moduleKey := accountWalletModuleKey(t, manager)
	baseline, err := store.GetAuthoritative(moduleKey)
	require.NoError(t, err)
	require.NotNil(t, baseline)
	require.NoError(t, manager.UpdateWalletName(id, "B"))
	require.NoError(t, manager.UpdateWalletName(id, "A"))
	before, err := manager.db.Read(accountManagementProfileKey())
	require.NoError(t, err)
	entry, err := accountEntry(manager.accountProfile.AccountID, "wallets", AccountManagedDataGlobalScope)
	require.NoError(t, err)
	catalog, err := manager.accountManagedDataCatalog()
	require.NoError(t, err)
	localPayload, err := manager.exportAccountPayload(entry, catalog)
	require.NoError(t, err)
	defer zeroBytes(localPayload)
	remotePayload, err := openAccountPayload(manager.accountSecret, manager.accountProfile.AccountID,
		manager.cfg.Chain, entry, baseline.Value)
	require.NoError(t, err)
	defer zeroBytes(remotePayload)
	require.Equal(t, remotePayload, localPayload, "B then A must restore the actual wallet module content")
	fault.armed.Store(true)
	firstErr := manager.SyncAccountManagementState(context.Background())
	require.ErrorIs(t, firstErr, errAccountPersistenceReview)
	require.Equal(t, int32(1), fault.hits.Load())
	require.NotEmpty(t, manager.accountProfile.Pending)
	require.True(t, manager.accountProfile.ManagedDataDirty)
	after, err := fault.KVDB.Read(accountManagementProfileKey())
	require.NoError(t, err)
	require.Equal(t, before, after)
	unchanged, err := store.GetAuthoritative(moduleKey)
	require.NoError(t, err)
	require.Equal(t, baseline.Seq, unchanged.Seq)
	require.Equal(t, baseline.Hash, unchanged.Hash)
	require.Equal(t, baseline.Value, unchanged.Value)
	entries, err := newDKVSReplicaStore(manager.db).LoadOutbox(store.client.replicaNamespace)
	require.NoError(t, err)
	require.Empty(t, entries, "an observed no-op must not create a remote write")

	fault.armed.Store(false)
	require.ErrorIs(t, manager.SyncAccountManagementState(context.Background()), ErrAccountDBStopped)
	database, service := manager.db, manager.serverNode
	manager.Close()
	manager = newAccountManagementAutoTestManager(t)
	manager.db, manager.serverNode = database, service
	configureRGB11DKVSTestManager(manager, backend)
	require.NoError(t, manager.initDB())
	_, err = manager.UnlockWallet("password")
	require.NoError(t, err)
	store, err = manager.accountDKVSStore()
	require.NoError(t, err)
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	require.Empty(t, manager.accountProfile.Pending)
	require.False(t, manager.accountProfile.ManagedDataDirty)
	remote, err := store.GetAuthoritative(moduleKey)
	require.NoError(t, err)
	require.Equal(t, baseline.Seq, remote.Seq, "no-op retry must not republish unchanged module content")
	require.Equal(t, baseline.Hash, remote.Hash)
	require.Equal(t, baseline.Value, remote.Value)
	require.Equal(t, "A", manager.GetWalletCatalog()[0].Name)
}

type noopFinalProvider struct {
	db  indexercommon.KVDB
	key []byte
}

func (p *noopFinalProvider) ID() string { return "test.secondary" }

func (p *noopFinalProvider) Export(AccountManagedDataCatalog) ([]AccountManagedDataPayload, error) {
	payload, err := p.db.Read(p.key)
	if err != nil {
		return nil, err
	}
	return []AccountManagedDataPayload{{Scope: AccountManagedDataGlobalScope, Payload: payload}}, nil
}

func (p *noopFinalProvider) Validate(AccountManagedDataCatalog, []AccountManagedDataPayload) error {
	return nil
}

func (p *noopFinalProvider) Import(AccountManagedDataCatalog, []AccountManagedDataPayload) error {
	return nil
}

func stageNoopPayload(t *testing.T, manager *Manager, provider *noopFinalProvider, payload []byte) {
	t.Helper()
	batch, err := NewAtomicWriteBatch(manager.db)
	require.NoError(t, err)
	defer batch.Close()
	finish, err := manager.StageAccountChange(batch, provider.ID(), AccountManagedDataGlobalScope)
	require.NoError(t, err)
	defer finish(false)
	require.NoError(t, batch.Put(provider.key, bytes.Clone(payload)))
	require.NoError(t, batch.Flush())
	finish(true)
}

func TestAccountNoopFinalizationWindowPreservesUpdates(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	for _, kind := range []string{"metadata", "module-generation"} {
		t.Run(kind, func(t *testing.T) {
			manager, _, store, _ := accountTTLNoopFixture(t)
			var provider *noopFinalProvider
			if kind == "module-generation" {
				provider = &noopFinalProvider{db: manager.db, key: []byte("no-op-finalization/provider-state")}
				require.NoError(t, manager.RegisterAccountManagedDataProvider(provider))
				stageNoopPayload(t, manager, provider, []byte("provider-before"))
				require.NoError(t, manager.SyncAccountManagementState(context.Background()))
			}
			id := manager.GetCurrentWalletId()
			require.NoError(t, manager.UpdateWalletName(id, "B"))
			require.NoError(t, manager.UpdateWalletName(id, "A"))
			accountID := manager.accountProfile.AccountID
			captured, err := manager.readAccountLocal(accountID, "wallets", AccountManagedDataGlobalScope)
			require.NoError(t, err)
			require.NotNil(t, captured)
			captured, err = manager.captureAccountValue(accountID, captured.Provider, captured.Scope, captured.Key, captured.Codec, captured.Value, captured.Generation)
			require.NoError(t, err)
			store, err = manager.accountDKVSStore()
			require.NoError(t, err)
			// This controlled finalization test does not run the background
			// receiver. Receive this module's ACK before constructing the no-op
			// input; another prefix's sync must not refresh it as a side effect.
			require.NoError(t, store.SyncCurrent(captured.Key))
			remote, err := store.Get(captured.Key)
			require.NoError(t, err)
			require.Equal(t, captured.Value, remote.Value, "the no-op completion must start from equal captured and received content")
			// Arrange the exact finalization input, then use public local edits
			// in the last window before the completion helper takes its lock.
			if kind == "metadata" {
				require.NoError(t, manager.UpdateWalletName(id, "C"))
			} else {
				// Commit a second provider's actual payload and module generation
				// in one local batch while the wallet no-op completion is stale.
				stageNoopPayload(t, manager, provider, []byte("provider-after"))
			}
			require.NoError(t, manager.recordAccountEffect(captured, remote))
			live, err := manager.readAccountLocal(accountID, "wallets", AccountManagedDataGlobalScope)
			require.NoError(t, err)
			require.NotNil(t, live)
			if kind == "metadata" {
				require.Greater(t, live.Generation, live.Confirmed,
					"completion for the captured generation must not confirm the newer wallet-module edit")
				require.Equal(t, "C", manager.GetWalletCatalog()[0].Name)
			} else {
				require.Equal(t, live.Generation, live.Confirmed,
					"the wallet no-op receipt should complete its own captured module")
				otherModule, readErr := manager.readAccountLocal(accountID, provider.ID(), AccountManagedDataGlobalScope)
				require.NoError(t, readErr)
				require.NotNil(t, otherModule)
				require.Greater(t, otherModule.Generation, otherModule.Confirmed,
					"wallet no-op completion must preserve the other provider's dirty generation")
				payloads, exportErr := provider.Export(AccountManagedDataCatalog{})
				require.NoError(t, exportErr)
				require.Len(t, payloads, 1)
				require.Equal(t, []byte("provider-after"), payloads[0].Payload)
				require.Equal(t, "A", manager.GetWalletCatalog()[0].Name)
			}
			require.NoError(t, manager.SyncAccountManagementState(context.Background()))
			converged, err := manager.readAccountLocal(accountID, "wallets", AccountManagedDataGlobalScope)
			require.NoError(t, err)
			require.Equal(t, converged.Generation, converged.Confirmed)
			require.Equal(t, live.Generation, converged.Generation)
			if kind == "metadata" {
				published, getErr := store.GetAuthoritative(converged.Key)
				require.NoError(t, getErr)
				require.NotNil(t, converged.Baseline)
				require.Equal(t, converged.Baseline.Seq, published.Seq)
				state, openErr := account.OpenManagedState(manager.accountSecret, accountID, published.Value)
				require.NoError(t, openErr)
				require.Equal(t, "C", state.Wallets[0].Name)
			} else {
				otherModule, readErr := manager.readAccountLocal(accountID, provider.ID(), AccountManagedDataGlobalScope)
				require.NoError(t, readErr)
				require.NotNil(t, otherModule)
				require.Equal(t, otherModule.Generation, otherModule.Confirmed)
				entry, entryErr := accountEntry(accountID, provider.ID(), AccountManagedDataGlobalScope)
				require.NoError(t, entryErr)
				published, getErr := store.GetAuthoritative(entry.Key)
				require.NoError(t, getErr)
				require.NotNil(t, published)
				payload, openErr := openAccountPayload(manager.accountSecret, accountID, manager.cfg.Chain, entry, published.Value)
				require.NoError(t, openErr)
				require.Equal(t, []byte("provider-after"), payload)
				zeroBytes(payload)
			}
		})
	}
}

func sameFrozenAccountCASRequest(before, after DKVSBatchCASRequest) bool {
	if before.RequestID == "" || before.RequestID != after.RequestID || before.EndpointID != after.EndpointID ||
		!reflect.DeepEqual(before.Authorization, after.Authorization) || len(before.Mutations) != len(after.Mutations) {
		return false
	}
	for i := range before.Mutations {
		left, right := before.Mutations[i], after.Mutations[i]
		if left.ExpectedETag != right.ExpectedETag || left.ExpectAbsent != right.ExpectAbsent ||
			left.Record == nil || right.Record == nil {
			return false
		}
		leftBytes, leftErr := swire.SerializeDKVSRecord(left.Record)
		rightBytes, rightErr := swire.SerializeDKVSRecord(right.Record)
		if leftErr != nil || rightErr != nil || !bytes.Equal(leftBytes, rightBytes) {
			return false
		}
	}
	return true
}

func TestAccountTTLCommittedLostACKColdRetry(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, remote, service, closeSource := newAccountGroup(t)
	config := *manager.cfg
	_, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password")
	require.NoError(t, err)
	require.NoError(t, manager.InitializeAccountManagement("password"))
	discoverInactiveAccount(t, manager)
	activateSyncFixture(t, manager, remote, "password")
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	moduleKey := accountWalletModuleKey(t, manager)
	// Raising the server ceiling does not change the user's confirmed policy.
	ttl := manager.accountProfile.RecordTTL
	require.NotZero(t, ttl)
	remote.mu.Lock()
	remote.freeLocal.MaxTTL *= 2
	ceiling := remote.freeLocal.MaxTTL
	remote.mu.Unlock()
	require.Less(t, ttl, ceiling)
	var targetRequest atomic.Value
	var originalRequest atomic.Value
	var attempts atomic.Int32
	var inject atomic.Bool
	var notify sync.Once
	published := make(chan DKVSBatchCASRequest, 1)
	transport := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	transport.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if path != "/v3/dkvs/records/batch-cas" {
			return remote.SendDKVSPostContext(ctx, path, body)
		}
		var request DKVSBatchCASRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		selected := false
		for _, mutation := range request.Mutations {
			if mutation.Record != nil && mutation.Record.Key == moduleKey {
				selected = true
				break
			}
		}
		if selected && inject.Load() && targetRequest.Load() == nil {
			targetRequest.Store(request.RequestID)
			originalRequest.Store(request)
		}
		id, _ := targetRequest.Load().(string)
		original := id != "" && request.RequestID == id
		if original {
			attempts.Add(1)
			frozen, _ := originalRequest.Load().(DKVSBatchCASRequest)
			if !sameFrozenAccountCASRequest(frozen, request) {
				t.Errorf("same-request retry changed the signed record, authorization, endpoint, or CAS precondition")
			}
		}
		response, err := remote.SendDKVSPostContext(ctx, path, body)
		if original && err == nil {
			notify.Do(func() {
				select {
				case published <- request:
				default:
				}
			})
			return nil, &HTTPResponseError{StatusCode: 503}
		}
		return response, err
	}
	manager.SetDKVSHttpClient(transport)
	// Establish the replacement client's actual source/session before arming
	// the response loss; this preparation cannot consume the target fault.
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	store, err := manager.accountDKVSStore()
	require.NoError(t, err)
	baseline, err := store.GetAuthoritative(moduleKey)
	require.NoError(t, err)
	require.NotNil(t, baseline)
	inject.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, manager.UpdateWalletName(manager.GetCurrentWalletId(), "After lost ACK"))
	require.Error(t, manager.SyncAccountManagementState(ctx))
	var request DKVSBatchCASRequest
	select {
	case request = <-published:
	case <-time.After(5 * time.Second):
		t.Fatal("lost ACK was not injected after the wallet module committed")
	}
	require.Len(t, request.Mutations, 1)
	require.NotNil(t, request.Mutations[0].Record)
	require.Equal(t, moduleKey, request.Mutations[0].Record.Key)
	committed, err := store.GetAuthoritative(moduleKey)
	require.NoError(t, err)
	require.Equal(t, ttl, committed.record.TTL)
	require.LessOrEqual(t, committed.record.TTL, ceiling)
	require.Equal(t, request.Mutations[0].Record.Seq, committed.Seq)
	require.Equal(t, request.Mutations[0].Record.Value, committed.Value)
	require.NotEqual(t, baseline.Hash, committed.Hash)
	entries, err := newDKVSReplicaStore(manager.db).LoadOutbox(store.client.replicaNamespace)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	ack := entries[0]
	require.Equal(t, targetRequest.Load(), ack.RequestID)
	require.Equal(t, accountKVOutboxDomain, ack.OriginDomain)
	require.Equal(t, moduleKey, ack.OriginKey)
	require.NotZero(t, ack.OriginGeneration)
	t.Logf("lost-ACK module request state=%s attempts=%d captured_generation=%d",
		ack.State, ack.Attempts, ack.OriginGeneration)
	observed := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote, post: func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if path != "/v3/dkvs/records/batch-cas" {
			return remote.SendDKVSPostContext(ctx, path, body)
		}
		var replay DKVSBatchCASRequest
		if decodeErr := json.Unmarshal(body, &replay); decodeErr != nil {
			t.Errorf("decode cold batch request: %v", decodeErr)
			return remote.SendDKVSPostContext(ctx, path, body)
		}
		if replay.RequestID == ack.RequestID {
			attempts.Add(1)
			original, _ := originalRequest.Load().(DKVSBatchCASRequest)
			if !sameFrozenAccountCASRequest(original, replay) {
				t.Errorf("cold retry changed the signed record, authorization, endpoint, or CAS precondition")
			}
		}
		return remote.SendDKVSPostContext(ctx, path, body)
	}}
	// Close the original runtime and its DB before opening the same durable
	// path through the production constructor. No live DB or identity is copied.
	cancel()
	closeSource()
	reopened, _ := openAccountGroup(t, &config, remote, service)
	reopened.SetDKVSHttpClient(observed)
	_, err = reopened.UnlockWallet("password")
	require.NoError(t, err)
	require.NoError(t, reopened.StartDKVSSync())
	syncCtx, syncCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer syncCancel()
	require.NoError(t, reopened.WaitAccountManagedDataReady(syncCtx))
	deadline, _ := syncCtx.Deadline()
	require.Eventually(t, func() bool {
		entries, readErr := newDKVSReplicaStore(reopened.db).LoadOutbox(reopened.dkvsReplicaNamespace())
		status := reopened.GetAccountManagementStatus()
		return readErr == nil && len(entries) == 0 && status.DKVSWriteAllowed && !status.ManagedDataDirty && status.PendingChanges == 0
	}, time.Until(deadline), 25*time.Millisecond, "cold manager must reconcile the committed module ACK without replaying its signed request")
	require.Equal(t, "After lost ACK", reopened.GetWalletCatalog()[0].Name)
	coldStore, err := reopened.accountDKVSStore()
	require.NoError(t, err)
	current, err := coldStore.GetAuthoritative(moduleKey)
	require.NoError(t, err)
	require.Equal(t, committed.Seq, current.Seq, "cold receipt reconciliation republished the module")
	require.Equal(t, committed.Hash, current.Hash)
	require.Equal(t, committed.Value, current.Value)
	require.Equal(t, ttl, reopened.accountProfile.RecordTTL)
	require.Equal(t, ttl, current.record.TTL)
	require.Equal(t, int32(2), attempts.Load(), "expected one initial request and exactly one same-RequestID retry")
	confirmedModule, err := reopened.readAccountLocal(reopened.accountProfile.AccountID,
		"wallets", AccountManagedDataGlobalScope)
	require.NoError(t, err)
	require.Equal(t, confirmedModule.Generation, confirmedModule.Confirmed)
	require.NotNil(t, confirmedModule.Baseline)
	require.Equal(t, current.Seq, confirmedModule.Baseline.Seq)
	require.Equal(t, current.Value, confirmedModule.Baseline.Value)
	state, err := account.OpenManagedState(reopened.accountSecret, reopened.accountProfile.AccountID, current.Value)
	require.NoError(t, err)
	require.Equal(t, "After lost ACK", state.Wallets[0].Name)
}
