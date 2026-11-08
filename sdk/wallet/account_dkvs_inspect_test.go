package wallet

import (
	"context"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

type inspectionSnapshotMemoryDB struct {
	*memoryKVDB
	onScan func()
}

type inspectionMemoryView struct {
	data   *memoryKVDB
	onScan func()
}

func (db inspectionSnapshotMemoryDB) NewReadView() (indexer.DBReadView, error) {
	db.mu.RLock()
	copyDB := newMemoryKVDB()
	for key, value := range db.data {
		copyDB.data[key] = append([]byte(nil), value...)
	}
	db.mu.RUnlock()
	return &inspectionMemoryView{data: copyDB, onScan: db.onScan}, nil
}

func (view *inspectionMemoryView) Read(key []byte) ([]byte, error) {
	return view.data.Read(key)
}

func (view *inspectionMemoryView) Scan(options indexer.ScanOptions, visit func([]byte, []byte) error) error {
	if view.onScan != nil {
		callback := view.onScan
		view.onScan = nil
		callback()
	}
	return view.data.Scan(options, visit)
}

func (view *inspectionMemoryView) Close() error { return nil }

func TestAccountDKVSInspectionUsesVerifiedCacheAndOneSnapshot(t *testing.T) {
	previousChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previousChain })
	manager := newAccountManagementAutoTestManager(t)
	_, err := manager.ImportWallet(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"password",
	)
	require.NoError(t, err)
	require.NoError(t, manager.InitializeAccountManagement("password"))
	remote := manager.http.(*rgb11MemoryDKVSHTTP)
	plainKey := accountTestKey(t, manager, "viewer/plain")
	secretKey := accountTestKey(t, manager, "viewer/secret")
	opaqueKey := accountTestKey(t, manager, "viewer/opaque")
	seed := finalDKVSSeedClient(manager, remote)
	plain := freeLocalRecord(t, manager, plainKey, 1, "hello")
	secret := freeLocalRecord(t, manager, secretKey, 1, "password=example-fixture")
	opaque := freeLocalRecord(t, manager, opaqueKey, 1, string([]byte{0xff, 0x00, 0x81}))
	_, err = seed.PutRecord(plain)
	require.NoError(t, err)
	_, err = seed.PutRecord(secret)
	require.NoError(t, err)
	_, err = seed.PutRecord(opaque)
	require.NoError(t, err)
	prefix, err := dkvsindexer.CollectionPathForKey(plainKey)
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	client, err := manager.GetDKVSClient()
	require.NoError(t, err)
	require.NoError(t, manager.dkvs.ensureCurrentSubscription(client))
	manager.db = inspectionSnapshotMemoryDB{memoryKVDB: manager.db.(*memoryKVDB)}

	first, err := manager.InspectCurrentAccountDKVS(AccountDKVSInspectOptions{Limit: 1})
	require.NoError(t, err)
	require.NotEmpty(t, first.Snapshot.ID)
	require.False(t, first.Snapshot.RemoteCurrent, "a local cache read cannot assert remote freshness")
	require.NotEmpty(t, first.NextCursor)
	views := append([]AccountDKVSInspectionRecord(nil), first.Records...)
	cursor := first.NextCursor
	for cursor != "" {
		page, err := manager.InspectCurrentAccountDKVS(AccountDKVSInspectOptions{Cursor: cursor, Limit: 1})
		require.NoError(t, err)
		require.Equal(t, first.Snapshot.ID, page.Snapshot.ID)
		views = append(views, page.Records...)
		cursor = page.NextCursor
	}
	var rawView, hiddenView, opaqueView *AccountDKVSInspectionRecord
	for i := range views {
		switch views[i].Key {
		case plainKey:
			rawView = &views[i]
		case secretKey:
			hiddenView = &views[i]
		case opaqueKey:
			opaqueView = &views[i]
		}
	}
	require.NotNil(t, rawView)
	require.True(t, rawView.RawHidden)
	require.Empty(t, rawView.RawHex)
	require.Equal(t, dkvsindexer.RecordHash(plain).String(), rawView.RecordHash)
	require.Equal(t, "raw_hidden", rawView.Decoded.Status)
	require.Equal(t, "unverified_value_type", rawView.RawHiddenReason)
	require.NotNil(t, hiddenView)
	require.True(t, hiddenView.RawHidden)
	require.Empty(t, hiddenView.RawHex)
	require.Equal(t, "raw_hidden", hiddenView.Decoded.Status)
	require.NotNil(t, opaqueView)
	require.True(t, opaqueView.RawHidden)
	require.Empty(t, opaqueView.RawHex)

	// A page token is bound to both the unlocked account and the exact cached
	// generation. Updating the replica makes the previous cursor unusable.
	thirdKey := accountTestKey(t, manager, "viewer/third")
	_, err = seed.PutRecord(freeLocalRecord(t, manager, thirdKey, 1, "new"))
	require.NoError(t, err)
	require.NoError(t, manager.dkvs.ensureCurrentSubscription(client))
	_, err = manager.InspectCurrentAccountDKVS(AccountDKVSInspectOptions{Cursor: first.NextCursor, Limit: 1})
	require.ErrorIs(t, err, ErrDKVSPathNotSynced)

	manager.mutex.Lock()
	accountID := manager.accountProfile.AccountID
	manager.accountProfile.AccountID = "different-account"
	manager.mutex.Unlock()
	_, err = manager.InspectCurrentAccountDKVS(AccountDKVSInspectOptions{})
	require.ErrorIs(t, err, ErrAccountManagementWalletUnavailable)
	manager.mutex.Lock()
	manager.accountProfile.AccountID = accountID
	manager.mutex.Unlock()
}

func TestAccountDKVSInspectionPinsRecordsAndStateDuringConcurrentCacheWrite(t *testing.T) {
	previousChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previousChain })
	manager := newAccountManagementAutoTestManager(t)
	_, err := manager.ImportWallet(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "password")
	require.NoError(t, err)
	require.NoError(t, manager.InitializeAccountManagement("password"))
	remote := manager.http.(*rgb11MemoryDKVSHTTP)
	key := accountTestKey(t, manager, "viewer/snapshot")
	seed := finalDKVSSeedClient(manager, remote)
	_, err = seed.PutRecord(freeLocalRecord(t, manager, key, 1, "snapshot-value"))
	require.NoError(t, err)
	prefix, err := dkvsindexer.CollectionPathForKey(key)
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	client, err := manager.GetDKVSClient()
	require.NoError(t, err)
	require.NoError(t, manager.dkvs.ensureCurrentSubscription(client))
	base := manager.db.(*memoryKVDB)
	manager.db = inspectionSnapshotMemoryDB{memoryKVDB: base, onScan: func() {
		require.NoError(t, base.DropPrefix([]byte("dkvs:subscription-record:")))
	}}
	inspection, err := manager.InspectCurrentAccountDKVS(AccountDKVSInspectOptions{})
	require.NoError(t, err)
	found := false
	for _, record := range inspection.Records {
		if record.Key == key {
			found = true
			require.True(t, record.RawHidden)
		}
	}
	require.True(t, found, "the record must come from the pinned view")
}

func TestAccountDKVSInspectionShowsOnlyAuthenticatedEncryptedRaw(t *testing.T) {
	previousChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previousChain })
	manager, _, _, _, keys := accountInspectModuleFixture(t, func(db indexer.KVDB) indexer.KVDB {
		return inspectionSnapshotMemoryDB{memoryKVDB: db.(*memoryKVDB)}
	})
	inspection, err := manager.InspectCurrentAccountDKVS(AccountDKVSInspectOptions{Limit: 50})
	require.NoError(t, err)
	seen := make(map[string]AccountDKVSInspectionRecord)
	for _, record := range inspection.Records {
		for _, key := range keys {
			if record.Key != key {
				continue
			}
			seen[key] = record
			require.False(t, record.RawHidden,
				"authenticated encrypted account record %s should expose its original ciphertext (reason=%s status=%s)",
				record.Key, record.RawHiddenReason, record.Decoded.Status)
			require.NotEmpty(t, record.RawHex)
			require.Equal(t, "verified", record.Decoded.Status)
		}
	}
	for _, key := range keys {
		_, ok := seen[key]
		require.True(t, ok, "missing authenticated account record %s", key)
	}
}

func TestAccountDKVSInspectionHidesEncryptedBlobWithPlaintextMetadata(t *testing.T) {
	previousChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previousChain })
	manager, remote, _, moduleKey, _ := accountInspectModuleFixture(t, func(db indexer.KVDB) indexer.KVDB {
		return inspectionSnapshotMemoryDB{memoryKVDB: db.(*memoryKVDB)}
	})
	seed := finalDKVSSeedClient(manager, remote)
	previous, err := seed.GetRecordDirect(moduleKey)
	require.NoError(t, err)
	require.NotNil(t, previous)
	blob, err := DecodeDKVSBlobValue(previous.Value)
	require.NoError(t, err)
	require.Empty(t, blob.Metadata)
	value, err := EncodeDKVSBlobValue(blob.Data, []byte("plain-metadata-secret"))
	require.NoError(t, err)
	withMetadata, err := newDKVSAccountSignedRecordWithFreeLocal(manager.wallet, moduleKey, value,
		dkvsindexer.RecordOptions{Seq: previous.Seq + 1, IssueHeight: previous.IssueHeight, TTL: previous.TTL})
	require.NoError(t, err)
	_, err = seed.PutRecord(withMetadata)
	require.NoError(t, err)
	client, err := manager.GetDKVSClient()
	require.NoError(t, err)
	require.NoError(t, manager.dkvs.ensureCurrentSubscription(client))
	inspection, err := manager.InspectCurrentAccountDKVS(AccountDKVSInspectOptions{Limit: 50})
	require.NoError(t, err)
	found := false
	for _, record := range inspection.Records {
		if record.Key != moduleKey {
			continue
		}
		found = true
		require.Equal(t, dkvsindexer.RecordHash(withMetadata).String(), record.RecordHash)
		require.True(t, record.RawHidden)
		require.Empty(t, record.RawHex)
		require.Equal(t, "unverified_value_type", record.RawHiddenReason)
	}
	require.True(t, found)
}

func accountInspectModuleFixture(t *testing.T,
	wrapDB ...func(indexer.KVDB) indexer.KVDB) (*Manager, *rgb11MemoryDKVSHTTP, *dkvsStore, string, []string) {
	t.Helper()
	manager, remote, store, _ := accountTTLNoopFixture(t, wrapDB...)
	catalog, err := manager.accountManagedDataCatalog()
	require.NoError(t, err)
	require.NotEmpty(t, catalog.Scopes)
	scope := catalog.Scopes[0].ID()
	provider := &accountManagedDataProviderStub{
		id:       "inspection",
		payloads: []AccountManagedDataPayload{{Scope: scope, Payload: []byte("encrypted module fixture")}},
	}
	require.NoError(t, manager.RegisterAccountManagedDataProvider(provider))
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	client, err := manager.ensureDKVSManager().primaryClient()
	require.NoError(t, err)
	require.NoError(t, manager.dkvs.ensureCurrentSubscription(client))

	accountID := manager.GetAccountManagementStatus().AccountID
	indexKey, err := account.ManagedIndexKey(accountID)
	require.NoError(t, err)
	indexValue, err := manager.readAccountLocal(accountID, "account", indexKey)
	require.NoError(t, err)
	require.NotNil(t, indexValue)
	require.NotNil(t, indexValue.Baseline)
	manager.mutex.RLock()
	secret := append([]byte(nil), manager.accountSecret...)
	network := manager.cfg.Chain
	manager.mutex.RUnlock()
	defer zeroBytes(secret)
	index, err := account.OpenManagedIndex(secret, accountID, network, indexValue.Baseline.Value)
	require.NoError(t, err)
	moduleKey, err := account.ManagedModuleKey(accountID, provider.ID(), scope)
	require.NoError(t, err)
	foundModule := false
	keys := []string{indexKey}
	walletModule, err := accountEntry(accountID, "wallets", AccountManagedDataGlobalScope)
	require.NoError(t, err)
	walletModuleKey := walletModule.Key
	root, err := manager.accountManagementRootWallet()
	require.NoError(t, err)
	wrapperKey, err := accountRootWrapperKey(root)
	require.NoError(t, err)
	keys = append(keys, wrapperKey)
	for _, entry := range index.Entries {
		if entry.Deleted {
			continue
		}
		keys = append(keys, entry.Key)
		if entry.Provider == provider.ID() && entry.Scope == scope && entry.Key == moduleKey {
			foundModule = true
		}
	}
	require.True(t, foundModule, "the authenticated account index must bind the module record")
	require.Contains(t, keys, walletModuleKey)
	module, err := store.GetAuthoritative(moduleKey)
	require.NoError(t, err)
	require.NotNil(t, module)
	return manager, remote, store, moduleKey, keys
}
