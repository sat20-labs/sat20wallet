package wallet

import (
	"context"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestAccountColdUnlockResumesPersistedMetadata(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	remote := newRGB11MemoryDKVSHTTP()
	client := newRGB11MessageNodeClient(remote)
	source := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(source, remote)
	id, _, err := source.CreateWallet("password")
	require.NoError(t, err)
	require.NoError(t, source.UpdateAccountMetadata(id, 0, "Saved before reload", "cold.btc"))
	require.Positive(t, source.GetAccountManagementStatus().PendingChanges)
	// One PWA closes and reopens its persisted database. Runtime jobs and
	// credentials are not copied, and there is no remote record to notify it.
	cold := newAccountManagementAutoTestManager(t)
	cold.db = source.db
	configureRGB11DKVSTestManager(cold, remote)
	cold.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
	cold.status = loadStatusFromDB(cold.db)
	cold.walletInfoMap, err = loadAllWalletFromDB(cold.db)
	require.NoError(t, err)
	require.NoError(t, cold.loadAccountManagementProfileLocked())
	_, err = cold.UnlockWallet("password")
	require.NoError(t, err)
	cold.dkvs.mu.Lock()
	_, queued := cold.dkvs.jobs[accountManagedStateJobID]
	cold.dkvs.mu.Unlock()
	require.True(t, queued, "cold unlock lost the persistent pending changes' sync task")
	cold.dkvs.start()
	defer cold.dkvs.stopAndWait()
	require.Eventually(t, func() bool {
		status := cold.GetAccountManagementStatus()
		return status.PendingChanges == 0 && !status.ManagedDataDirty && status.ManagedDataRevision > 0
	}, 5*time.Second, 20*time.Millisecond)
	key, err := cold.accountManagedStateKey(cold.wallet)
	require.NoError(t, err)
	store, err := cold.accountDKVSStore()
	require.NoError(t, err)
	value, err := store.GetAuthoritative(key)
	require.NoError(t, err)
	require.NotNil(t, value)
	state, err := account.OpenManagedState(cold.accountSecret, cold.accountProfile.AccountID, value.Value)
	require.NoError(t, err)
	require.Equal(t, "Saved before reload", state.Wallets[0].SubAccounts[0].Name)
	require.Equal(t, "cold.btc", state.Wallets[0].SubAccounts[0].DID)
	require.True(t, client.accountBound(cold.accountProfile.AccountID), "cold backup ran before the root was bound to its CoreNode")
}

func TestAccountFirstWalletSchedulesInitialBackup(t *testing.T) {
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
	manager.dkvs.mu.Lock()
	_, queued := manager.dkvs.jobs[accountManagedStateJobID]
	manager.dkvs.mu.Unlock()
	require.True(t, queued, "first wallet's dirty state has no initial backup task")
	require.False(t, client.accountBound(manager.accountProfile.AccountID))
	manager.dkvs.start()
	defer manager.dkvs.stopAndWait()
	require.Eventually(t, func() bool {
		status := manager.GetAccountManagementStatus()
		return !status.ManagedDataDirty && status.ManagedDataRevision > 0
	}, 5*time.Second, 20*time.Millisecond)
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

func TestAccountSubaccountIndependentFieldsMerge(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	for _, edit := range []string{"local_name", "local_did", "clear_did"} {
		t.Run(edit, func(t *testing.T) {
			local, other, _ := reviewAccountDevices(t)
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
			for _, manager := range []*Manager{local, other} {
				entry := reviewCatalogWallet(t, manager, manager.GetAccountManagementStatus().RootFingerprint)
				require.NotNil(t, entry)
				require.Equal(t, name, entry.Accounts[0].Name)
				require.Equal(t, did, entry.Accounts[0].DID)
				stored, err := loadAllWalletFromDB(manager.db)
				require.NoError(t, err)
				require.Len(t, stored, 1)
				require.Equal(t, name, stored[entry.ID].AccountNames[0])
				require.Equal(t, did, stored[entry.ID].AccountDIDs[0])
				require.Zero(t, manager.GetAccountManagementStatus().PendingChanges)
			}
		})
	}
}

// Two independently persisted SDK instances share only the in-memory service.
// The second device starts with the same root, secret and confirmed baseline;
// all changes and subsequent synchronization use production SDK entry points.
func reviewAccountDevices(t *testing.T) (*Manager, *Manager, *rgb11MemoryDKVSHTTP) {
	t.Helper()
	remote := newRGB11MemoryDKVSHTTP()
	// Establish the paired CoreNode identity before either device caches HTTP
	// configuration. Adding a message service later must not switch endpoints.
	newRGB11MessageNodeClient(remote)
	first := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(first, remote)
	if _, err := first.ImportWallet(accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	if err := first.InitializeAccountManagement("password"); err != nil {
		t.Fatal(err)
	}
	if err := first.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(second, remote)
	if _, err := second.ImportWallet(accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	first.mutex.RLock()
	encoded, err := EncodeToBytes(first.accountProfile)
	secret := append([]byte(nil), first.accountSecret...)
	first.mutex.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(secret)
	var profile accountManagementProfile
	if err := DecodeFromBytes(encoded, &profile); err != nil {
		t.Fatal(err)
	}
	second.mutex.Lock()
	second.accountProfile = &profile
	second.accountSecret = append([]byte(nil), secret...)
	second.accountPassword = "password"
	second.bumpAccountGenerationLocked()
	err = second.saveAccountManagementProfileLocked()
	second.mutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := second.refreshDKVSRegistrations(); err != nil {
		t.Fatal(err)
	}
	if err := second.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	return first, second, remote
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

func TestReviewAccountMergeAppliesRemoteIncrement(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	t.Run("remote_wallet_and_local_rename", func(t *testing.T) {
		local, other, _ := reviewAccountDevices(t)
		rootID := local.GetAccountManagementStatus().RootWalletID
		childID, _, err := other.CreateWallet("password")
		if err != nil {
			t.Fatal(err)
		}
		childFingerprint := walletFingerprint(other.walletInfoMap[childID].Wallet)
		if err := other.SyncAccountManagementState(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := local.UpdateWalletName(rootID, "Local root rename"); err != nil {
			t.Fatal(err)
		}
		for pass := 1; pass <= 2; pass++ {
			if err := local.SyncAccountManagementState(context.Background()); err != nil {
				t.Fatalf("sync %d: %v", pass, err)
			}
			if reviewCatalogWallet(t, local, childFingerprint) == nil {
				t.Errorf("sync %d returned success but the remote wallet is absent from the live catalog", pass)
			}
			if got := local.walletInfoMap[rootID].Name; got != "Local root rename" {
				t.Errorf("sync %d overwrote the local rename: %q", pass, got)
			}
		}
		stored, err := loadAllWalletFromDB(local.db)
		if err != nil || len(stored) != 2 {
			t.Errorf("remote wallet was not durably applied: wallets=%d err=%v", len(stored), err)
		}
	})

	t.Run("remote_subaccount_and_local_rename_same_wallet", func(t *testing.T) {
		local, other, _ := reviewAccountDevices(t)
		rootID := local.GetAccountManagementStatus().RootWalletID
		if err := other.EnsureAccount(other.GetAccountManagementStatus().RootWalletID, 1, "Remote savings", "did:savings"); err != nil {
			t.Fatal(err)
		}
		if err := other.SyncAccountManagementState(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := local.UpdateWalletName(rootID, "Local renamed root"); err != nil {
			t.Fatal(err)
		}
		for pass := 1; pass <= 2; pass++ {
			if err := local.SyncAccountManagementState(context.Background()); err != nil {
				t.Fatal(err)
			}
			entry := reviewCatalogWallet(t, local, local.GetAccountManagementStatus().RootFingerprint)
			if entry == nil || len(entry.Accounts) != 2 || entry.Accounts[1].Name != "Remote savings" || entry.Accounts[1].DID != "did:savings" {
				t.Errorf("sync %d returned success but omitted the remote subaccount: %+v", pass, entry)
			}
			if local.walletInfoMap[rootID].Name != "Local renamed root" {
				t.Error("remote apply overwrote the local wallet rename")
			}
		}
	})

	t.Run("independent_remote_and_local_provider_edits", func(t *testing.T) {
		local, other, _ := reviewAccountDevices(t)
		providers := make(map[*Manager]map[string]*reviewDurableProvider)
		for _, manager := range []*Manager{local, other} {
			providers[manager] = make(map[string]*reviewDurableProvider)
			for _, id := range []string{"review.alpha", "review.beta"} {
				provider := &reviewDurableProvider{id: id, db: manager.db}
				if err := provider.db.Write(provider.key(), []byte("base")); err != nil {
					t.Fatal(err)
				}
				if err := manager.RegisterAccountManagedDataProvider(provider); err != nil {
					t.Fatal(err)
				}
				providers[manager][id] = provider
			}
		}
		for _, manager := range []*Manager{local, other} {
			if err := manager.SyncAccountManagementState(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		alpha := providers[other]["review.alpha"]
		if err := alpha.db.Write(alpha.key(), []byte("remote-alpha")); err != nil {
			t.Fatal(err)
		}
		other.markAccountManagedDataDirtyDeferred(alpha.id)
		if err := other.SyncAccountManagementState(context.Background()); err != nil {
			t.Fatal(err)
		}
		beta := providers[local]["review.beta"]
		if err := beta.db.Write(beta.key(), []byte("local-beta")); err != nil {
			t.Fatal(err)
		}
		local.markAccountManagedDataDirtyDeferred(beta.id)
		for pass := 1; pass <= 2; pass++ {
			if err := local.SyncAccountManagementState(context.Background()); err != nil {
				t.Fatalf("sync %d: %v", pass, err)
			}
			for id, want := range map[string]string{"review.alpha": "remote-alpha", "review.beta": "local-beta"} {
				provider := providers[local][id]
				got, err := provider.db.Read(provider.key())
				if err != nil || string(got) != want {
					t.Errorf("sync %d provider %s was not applied: got=%q want=%q err=%v", pass, id, got, want, err)
				}
			}
		}
	})
}
