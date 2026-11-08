package wallet

import (
	"bytes"
	"context"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestAccountColdUnlockResumesPersistedMetadata(t *testing.T) {
	source, remote, _, closeSource := newAccountGroup(t)
	id, _, err := source.CreateWallet("password")
	require.NoError(t, err)
	activateSyncFixture(t, source, remote, "password")
	syncImportGroup(t, source, nil)
	require.NoError(t, source.UpdateAccountMetadata(id, 0, "Saved before reload", "cold.btc"))
	require.Positive(t, source.GetAccountManagementStatus().PendingChanges)
	cfg := source.cfg
	closeSource()
	coldClient := newRGB11MessageNodeClient(remote)
	cold, _ := openAccountGroup(t, cfg, remote, coldClient)
	_, err = cold.UnlockWallet("password")
	require.NoError(t, err)
	cold.dkvs.mu.Lock()
	_, queued := cold.dkvs.jobs[accountManagedStateJobID]
	cold.dkvs.mu.Unlock()
	require.True(t, queued, "cold unlock lost the persistent pending changes' sync task")
	// Binding belongs to the shared Core service. A cold client reuses the
	// existing signed mapping; it need not publish a new binding of its own.
	require.True(t, coldClient.accountBound(cold.GetAccountManagementStatus().AccountID),
		"cold backup must start with the root bound to its current CoreNode")
	require.NoError(t, cold.StartDKVSSync())
	t.Cleanup(cold.StopDKVSSync)
	ctx, cancel := context.WithTimeout(context.Background(), accountManagedBackupQuiet+5*time.Second)
	defer cancel()
	require.NoError(t, cold.WaitAccountManagedDataReady(ctx))
	status := cold.GetAccountManagementStatus()
	require.Zero(t, status.PendingChanges)
	require.False(t, status.ManagedDataDirty)
	key := accountWalletModuleKey(t, cold)
	store, err := cold.accountDKVSStore()
	require.NoError(t, err)
	value, err := store.GetAuthoritative(key)
	require.NoError(t, err)
	require.NotNil(t, value)
	require.NotNil(t, value.record)
	require.NoError(t, dkvsindexer.VerifySignature(value.record))
	require.NotZero(t, value.Seq)
	state, err := account.OpenManagedState(cold.accountSecret, status.AccountID, value.Value)
	require.NoError(t, err)
	defer func() {
		for i := range state.Wallets {
			zeroBytes(state.Wallets[i].Secret)
		}
	}()
	require.Equal(t, "Saved before reload", state.Wallets[0].SubAccounts[0].Name)
	require.Equal(t, "cold.btc", state.Wallets[0].SubAccounts[0].DID)
	require.True(t, coldClient.accountBound(status.AccountID), "cold backup lost the root binding to its current CoreNode")
}

func TestAccountFirstWalletSchedulesInitialBackup(t *testing.T) {
	manager, remote, client, _ := newAccountGroup(t)
	_, _, err := manager.CreateWallet("password")
	require.NoError(t, err)
	manager.dkvs.mu.Lock()
	_, queued := manager.dkvs.jobs[accountManagedStateJobID]
	if queued {
		manager.dkvs.jobDue[accountManagedStateJobID] = time.Now().Add(-time.Millisecond)
	}
	manager.dkvs.mu.Unlock()
	require.True(t, queued, "initial local edit did not schedule its background check")
	_, err = manager.syncDKVSOnce()
	require.NoError(t, err, "real online discovery precedes the inactive background check")
	manager.dkvs.mu.Lock()
	_, queued = manager.dkvs.jobs[accountManagedStateJobID]
	manager.dkvs.mu.Unlock()
	require.False(t, queued, "unactivated background check did not finish")
	require.True(t, manager.GetAccountManagementStatus().LocalOnly)
	require.False(t, client.accountBound(manager.accountProfile.AccountID))
	remote.mu.Lock()
	remoteCount := len(remote.records)
	remote.mu.Unlock()
	require.Zero(t, remoteCount, "unactivated wallet uploaded account data")
	activateSyncFixture(t, manager, remote, "password")
	require.NoError(t, manager.StartDKVSSync())
	t.Cleanup(manager.StopDKVSSync)
	ctx, cancel := context.WithTimeout(context.Background(), accountManagedBackupQuiet+5*time.Second)
	defer cancel()
	require.NoError(t, manager.WaitAccountManagedDataReady(ctx))
	require.False(t, manager.GetAccountManagementStatus().ManagedDataDirty)
	value, err := manager.readAccountLocal(manager.accountProfile.AccountID, "wallets", AccountManagedDataGlobalScope)
	require.NoError(t, err)
	require.NotNil(t, value)
	require.NotNil(t, value.Baseline)
	require.NoError(t, dkvsindexer.VerifySignature(value.Baseline))
	require.NotZero(t, value.Baseline.Seq)
	require.Equal(t, value.Generation, value.Confirmed)
	require.True(t, client.accountBound(manager.accountProfile.AccountID), "initial backup ran before the root was bound to its CoreNode")
}

func TestAccountBackupBindingPreservesOtherCore(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	remote := newRGB11MemoryDKVSHTTP()
	client := newRGB11MessageNodeClient(remote)
	manager := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(manager, remote)
	manager.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
	_, _, err := manager.CreateWallet("password")
	require.NoError(t, err)
	require.NoError(t, manager.ensureAccountCoreBindingForSync())
	key, err := dkvsindexer.AccountMappingKey(GetChainParam().Name, manager.wallet.GetAddress())
	require.NoError(t, err)
	store, err := manager.accountDKVSStore()
	require.NoError(t, err)
	current, err := store.client.GetRecordDirect(key)
	require.NoError(t, err)
	require.NoError(t, manager.ensureAccountCoreBindingForSync())
	same, err := store.client.GetRecordDirect(key)
	require.NoError(t, err)
	require.Equal(t, dkvsindexer.RecordHash(current), dkvsindexer.RecordHash(same))
	_, _, descriptor, err := dkvsindexer.ValidateAccountMappingBindingRecord(current)
	require.NoError(t, err)
	otherPrefix := "02"
	if descriptor.CoreNodeID[:2] == otherPrefix {
		otherPrefix = "03"
	}
	descriptor.CoreNodeID = otherPrefix + descriptor.CoreNodeID[2:]
	value, err := dkvsindexer.EncodeAccountServiceDescriptor(*descriptor)
	require.NoError(t, err)
	other, err := NewDKVSAccountSignedRecord(manager.wallet, key, value,
		dkvsindexer.RecordOptions{Seq: current.Seq + 1, IssueHeight: current.IssueHeight})
	require.NoError(t, err)
	remote.mu.Lock()
	remote.records[key] = other
	remote.mu.Unlock()
	require.ErrorIs(t, manager.ensureAccountCoreBindingForSync(), dkvsindexer.ErrEndpointMismatch)
	unchanged, err := store.client.GetRecordDirect(key)
	require.NoError(t, err)
	require.Equal(t, dkvsindexer.RecordHash(other), dkvsindexer.RecordHash(unchanged))
}

func TestAccountSubaccountConcurrentFieldsRequireReconciliation(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	for _, edit := range []string{"local_name", "local_did", "clear_did"} {
		t.Run(edit, func(t *testing.T) {
			local, other, remote := reviewAccountDevices(t)
			localID, otherID := local.GetAccountManagementStatus().RootWalletID, other.GetAccountManagementStatus().RootWalletID
			require.NoError(t, local.UpdateAccountMetadata(localID, 0, "Baseline", "base.btc"))
			require.NoError(t, local.SyncAccountManagementState(context.Background()))
			require.NoError(t, other.SyncAccountManagementState(context.Background()))
			name, did := "Remote name", "local.btc"
			if edit == "local_name" {
				name, did = "Local name", "remote.btc"
				require.NoError(t, other.UpdateAccountMetadata(otherID, 0, "Baseline", did))
				require.NoError(t, local.UpdateAccountMetadata(localID, 0, name, "base.btc"))
			} else {
				if edit == "clear_did" {
					did = ""
				}
				require.NoError(t, other.UpdateAccountMetadata(otherID, 0, name, "base.btc"))
				require.NoError(t, local.UpdateAccountMetadata(localID, 0, "Baseline", did))
			}
			require.NoError(t, other.SyncAccountManagementState(context.Background()))
			require.NoError(t, local.SyncAccountManagementState(context.Background()))
			require.NoError(t, other.SyncAccountManagementState(context.Background()))
			cold := reviewColdCopy(t, local, remote)
			fingerprint := local.GetAccountManagementStatus().RootFingerprint
			for _, manager := range []*Manager{local, other, cold} {
				entry := reviewCatalogWallet(t, manager, fingerprint)
				require.NotNil(t, entry)
				require.Len(t, entry.Accounts, 1)
				require.Equal(t, name, entry.Accounts[0].Name)
				require.Equal(t, did, entry.Accounts[0].DID, "an intentional clear must not restore the baseline DID")
			}
			if edit == "local_name" {
				// The same field with two different edits still cannot be reconciled.
				require.NoError(t, local.UpdateWalletName(localID, "Conflicting local name"))
				require.NoError(t, other.UpdateWalletName(otherID, "Conflicting remote name"))
				require.NoError(t, other.SyncAccountManagementState(context.Background()))
				assertReviewConflict(t, local)
				require.Equal(t, "Conflicting local name", reviewCatalogWallet(t, local, fingerprint).Name)
				require.Equal(t, "Conflicting remote name", reviewCatalogWallet(t, other, fingerprint).Name)
			}
		})
	}
}

// Two independently persisted SDK instances share only the in-memory service.
// The second device starts with the same root, secret and confirmed baseline;
// all changes and subsequent synchronization use production SDK entry points.
func reviewAccountDevices(t *testing.T) (*Manager, *Manager, *rgb11MemoryDKVSHTTP) {
	t.Helper()
	first, remote, _ := newInactiveAccountGroup(t, accountRootWrapperTestMnemonic, "password")
	pkg, auth, secret := rereviewRecoveryPackage(t, first)
	require.NoError(t, first.ActivateAccountManagement(secret, "password", *auth,
		pkg.Envelope.Locator, "account://"+pkg.Envelope.Locator.PackageID))
	syncImportGroup(t, first, nil)
	second, _, _, _ := newAccountGroup(t, remote)
	require.NoError(t, restoreImportGroup(t, second, first))
	syncImportGroup(t, second, nil)
	return first, second, remote
}

func reviewColdCopy(t *testing.T, source *Manager, remote *rgb11MemoryDKVSHTTP, providers ...string) *Manager {
	t.Helper()
	store, err := source.accountDKVSStore()
	require.NoError(t, err)
	key := accountWalletModuleKey(t, source)
	value, err := store.GetAuthoritative(key)
	require.NoError(t, err)
	require.NotNil(t, value)
	require.NotNil(t, value.record)
	require.NoError(t, dkvsindexer.VerifySignature(value.record))
	state, err := account.OpenManagedState(source.accountSecret, source.accountProfile.AccountID, value.Value)
	require.NoError(t, err)
	defer func() {
		for i := range state.Wallets {
			zeroBytes(state.Wallets[i].Secret)
		}
	}()
	catalog := source.GetWalletCatalog()
	require.Equal(t, len(catalog), len(state.Wallets))
	for _, item := range catalog {
		wallet := findManagedWallet(&state, item.Fingerprint)
		require.NotNil(t, wallet)
		require.False(t, wallet.Deleted)
		require.Equal(t, item.Name, wallet.Name)
		require.Len(t, wallet.SubAccounts, len(item.Accounts))
		for i, sub := range item.Accounts {
			require.Equal(t, sub.Index, wallet.SubAccounts[i].Index)
			require.Equal(t, sub.Name, wallet.SubAccounts[i].Name)
			require.Equal(t, sub.DID, wallet.SubAccounts[i].DID)
		}
	}
	recovered, _, _, close := newAccountGroup(t, remote)
	recovered.rgbManager.evidence = source.rgbManager.evidence
	for _, id := range providers {
		require.NoError(t, recovered.RegisterAccountManagedDataProvider(&reviewDurableProvider{id: id, db: recovered.db}))
	}
	require.NoError(t, restoreImportGroup(t, recovered, source))
	syncImportGroup(t, recovered, nil)
	cfg := recovered.cfg
	close()
	cold, _ := openAccountGroup(t, cfg, remote, newRGB11MessageNodeClient(remote))
	cold.rgbManager.evidence = source.rgbManager.evidence
	for _, id := range providers {
		require.NoError(t, cold.RegisterAccountManagedDataProvider(&reviewDurableProvider{id: id, db: cold.db}))
	}
	_, err = cold.UnlockWallet("password")
	require.NoError(t, err)
	syncImportGroup(t, cold, nil)
	return cold
}

func stageReviewProvider(t *testing.T, manager *Manager, provider *reviewDurableProvider, value []byte) {
	t.Helper()
	batch, err := NewAtomicWriteBatch(manager.db)
	require.NoError(t, err)
	defer batch.Close()
	done, err := manager.StageAccountChange(batch, provider.id, AccountManagedDataGlobalScope)
	require.NoError(t, err)
	defer done(false)
	require.NoError(t, batch.Put(provider.key(), value))
	require.NoError(t, batch.Flush())
	done(true)
}

func reviewCatalogWallet(t *testing.T, manager *Manager, fingerprint string) *WalletCatalogEntry {
	t.Helper()
	for _, item := range manager.GetWalletCatalog() {
		if item.Fingerprint == fingerprint {
			copyItem := item
			return &copyItem
		}
	}
	return nil
}

// A real persisted provider, not a recording-only mock: a later Export reads
// precisely what the application would see after Import or after a restart.
type reviewDurableProvider struct {
	id string
	db indexer.KVDB
}

func (p *reviewDurableProvider) ID() string  { return p.id }
func (p *reviewDurableProvider) key() []byte { return []byte("review-provider-" + p.id) }
func (p *reviewDurableProvider) Export(AccountManagedDataCatalog) ([]AccountManagedDataPayload, error) {
	value, err := p.db.Read(p.key())
	if err != nil {
		return nil, err
	}
	return []AccountManagedDataPayload{{Scope: AccountManagedDataGlobalScope, Payload: value}}, nil
}
func (p *reviewDurableProvider) Validate(AccountManagedDataCatalog, []AccountManagedDataPayload) error {
	return nil
}
func (p *reviewDurableProvider) Import(_ AccountManagedDataCatalog, values []AccountManagedDataPayload) error {
	for _, value := range values {
		if value.Scope == AccountManagedDataGlobalScope {
			return p.db.Write(p.key(), value.Payload)
		}
	}
	return p.db.Delete(p.key())
}

func TestReviewAccountConcurrentBundlesRequireReconciliation(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	t.Run("remote_wallet_and_local_rename", func(t *testing.T) {
		local, other, remote := reviewAccountDevices(t)
		rootID := local.GetAccountManagementStatus().RootWalletID
		childID, _, err := other.CreateWallet("password")
		require.NoError(t, err)
		childFingerprint := walletFingerprint(other.walletInfoMap[childID].Wallet)
		require.NoError(t, other.SyncAccountManagementState(context.Background()))
		require.NoError(t, local.UpdateWalletName(rootID, "Local root rename"))
		require.NoError(t, local.SyncAccountManagementState(context.Background()))
		require.NoError(t, other.SyncAccountManagementState(context.Background()))
		cold := reviewColdCopy(t, local, remote)
		for _, manager := range []*Manager{local, other, cold} {
			require.NotNil(t, reviewCatalogWallet(t, manager, childFingerprint), "merge lost the remotely created wallet")
			root := reviewCatalogWallet(t, manager, local.GetAccountManagementStatus().RootFingerprint)
			require.NotNil(t, root)
			require.Equal(t, "Local root rename", root.Name)
		}
	})
	t.Run("remote_subaccount_and_local_rename_same_wallet", func(t *testing.T) {
		local, other, remote := reviewAccountDevices(t)
		rootID := local.GetAccountManagementStatus().RootWalletID
		require.NoError(t, other.EnsureAccount(other.GetAccountManagementStatus().RootWalletID, 1, "Remote savings", "did:savings"))
		require.NoError(t, other.SyncAccountManagementState(context.Background()))
		require.NoError(t, local.UpdateWalletName(rootID, "Local renamed root"))
		require.NoError(t, local.SyncAccountManagementState(context.Background()))
		require.NoError(t, other.SyncAccountManagementState(context.Background()))
		cold := reviewColdCopy(t, local, remote)
		for _, manager := range []*Manager{local, other, cold} {
			entry := reviewCatalogWallet(t, manager, local.GetAccountManagementStatus().RootFingerprint)
			require.NotNil(t, entry)
			require.Equal(t, "Local renamed root", entry.Name)
			require.Len(t, entry.Accounts, 2)
			require.Equal(t, "Remote savings", entry.Accounts[1].Name)
			require.Equal(t, "did:savings", entry.Accounts[1].DID)
		}
	})
	t.Run("independent_remote_and_local_provider_edits", func(t *testing.T) {
		local, other, remote := reviewAccountDevices(t)
		providers := make(map[*Manager]map[string]*reviewDurableProvider)
		for _, manager := range []*Manager{local, other} {
			providers[manager] = make(map[string]*reviewDurableProvider)
			for _, id := range []string{"review.alpha", "review.beta"} {
				provider := &reviewDurableProvider{id: id, db: manager.db}
				require.NoError(t, provider.db.Write(provider.key(), []byte("base")))
				require.NoError(t, manager.RegisterAccountManagedDataProvider(provider))
				providers[manager][id] = provider
			}
		}
		require.NoError(t, local.SyncAccountManagementState(context.Background()))
		require.NoError(t, other.SyncAccountManagementState(context.Background()))
		stageReviewProvider(t, other, providers[other]["review.alpha"], []byte("remote-alpha"))
		require.NoError(t, other.SyncAccountManagementState(context.Background()))
		stageReviewProvider(t, local, providers[local]["review.beta"], []byte("local-beta"))
		require.NoError(t, local.SyncAccountManagementState(context.Background()))
		require.NoError(t, other.SyncAccountManagementState(context.Background()))
		cold := reviewColdCopy(t, local, remote, "review.alpha", "review.beta")
		for _, manager := range []*Manager{local, other, cold} {
			for id, want := range map[string]string{"review.alpha": "remote-alpha", "review.beta": "local-beta"} {
				provider := &reviewDurableProvider{id: id, db: manager.db}
				got, err := manager.db.Read(provider.key())
				require.NoError(t, err)
				require.Equal(t, want, string(got), "independent module synchronization lost durable provider data")
				row, err := manager.readAccountLocal(manager.accountProfile.AccountID, id, AccountManagedDataGlobalScope)
				require.NoError(t, err)
				require.NotNil(t, row)
				require.NotNil(t, row.Baseline)
				require.NoError(t, dkvsindexer.VerifySignature(row.Baseline))
				entry, err := accountEntry(row.AccountID, row.Provider, row.Scope)
				require.NoError(t, err)
				payload, err := openAccountPayload(manager.accountSecret, row.AccountID, manager.cfg.Chain, entry, row.Baseline.Value)
				require.NoError(t, err)
				require.Equal(t, want, string(payload))
				zeroBytes(payload)
			}
		}
	})
}

func assertReviewConflict(t *testing.T, manager *Manager) {
	t.Helper()
	catalog := manager.GetWalletCatalog()
	persisted, err := loadAllWalletFromDB(manager.db)
	require.NoError(t, err)
	before := make(map[int64][]byte)
	for id := range persisted {
		before[id], err = manager.db.Read([]byte(getWalletDBKey(id)))
		require.NoError(t, err)
	}
	store, err := manager.accountDKVSStore()
	require.NoError(t, err)
	stateKey := accountWalletModuleKey(t, manager)
	indexKey, err := account.ManagedIndexKey(manager.accountProfile.AccountID)
	require.NoError(t, err)
	remoteBefore := make(map[string]*dkvsValue)
	for _, key := range []string{stateKey, indexKey} {
		remoteBefore[key], err = store.GetAuthoritative(key)
		require.NoError(t, err)
		require.NotNil(t, remoteBefore[key])
	}
	for attempt := 0; attempt < 2; attempt++ {
		require.ErrorIs(t, manager.SyncAccountManagementState(context.Background()), dkvsindexer.ErrWriteConflict)
		require.Equal(t, catalog, manager.GetWalletCatalog())
		after, err := loadAllWalletFromDB(manager.db)
		require.NoError(t, err)
		require.Len(t, after, len(before))
		for id, raw := range before {
			actual, err := manager.db.Read([]byte(getWalletDBKey(id)))
			require.NoError(t, err)
			require.True(t, bytes.Equal(raw, actual), "unresolved conflict changed a durable wallet record")
		}
		for key, original := range remoteBefore {
			actual, err := store.GetAuthoritative(key)
			require.NoError(t, err)
			require.NotNil(t, actual)
			require.Equal(t, original.Hash, actual.Hash)
			require.Equal(t, original.Seq, actual.Seq)
			require.True(t, bytes.Equal(original.Value, actual.Value), "unresolved conflict overwrote remote data")
		}
		status := manager.GetAccountManagementStatus()
		require.True(t, status.PendingChanges > 0 || status.ManagedDataDirty, "unresolved local intent must not be cleared")
	}
}
