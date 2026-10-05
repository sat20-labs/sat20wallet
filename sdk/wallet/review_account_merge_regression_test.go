package wallet

import (
	"context"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
)

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
