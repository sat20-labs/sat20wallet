package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitAccountManagedDataReadyCancelsAndConverges(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	manager, remote, _, keys := accountTTLNoopFixture(t)
	walletModuleKey := accountWalletModuleKey(t, manager)
	matchedWalletKey := false
	for _, key := range keys {
		if key == walletModuleKey {
			matchedWalletKey = true
			break
		}
	}
	if !matchedWalletKey {
		t.Fatalf("wallet module key %q is not part of the synchronized baseline", walletModuleKey)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var armed atomic.Bool
	transport := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
	transport.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
		if path == "/v3/dkvs/records/batch-cas" && armed.Load() {
			var request DKVSBatchCASRequest
			if err := json.Unmarshal(body, &request); err != nil {
				return nil, err
			}
			selectedModule := false
			for _, mutation := range request.Mutations {
				if mutation.Record != nil && mutation.Record.Key == walletModuleKey {
					selectedModule = true
					break
				}
			}
			if selectedModule && armed.CompareAndSwap(true, false) {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		}
		return remote.SendDKVSPostContext(ctx, path, body)
	}
	manager.SetDKVSHttpClient(transport)
	// Replacing the client invalidates its online qualification. Complete its
	// real receive/baseline pass before holding a subsequent signed publication.
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatalf("synchronize the replacement client before arming publication: %v", err)
	}
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := manager.WaitAccountManagedDataReady(setupCtx); err != nil {
		setupCancel()
		t.Fatalf("wait for the replacement client's confirmed baseline: %v", err)
	}
	setupCancel()
	if err := manager.StartDKVSSync(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.StopDKVSSync)
	if err := manager.RetryDKVSSync(); err != nil {
		t.Fatalf("wake the existing worker for a fresh account sync: %v", err)
	}
	readyDeadline := time.Now().Add(10 * time.Second)
	for !manager.GetAccountManagementStatus().DKVSWriteAllowed && time.Now().Before(readyDeadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !manager.GetAccountManagementStatus().DKVSWriteAllowed {
		t.Fatal("the existing worker did not restore real write qualification after Start")
	}
	// Only hold the tested wallet-module publication. Setup sync and unrelated
	// module requests pass through before and after this explicit arm point.
	armed.Store(true)
	id := manager.GetCurrentWalletId()
	if err := manager.UpdateWalletName(id, "pending backup"); err != nil {
		t.Fatal(err)
	}
	// This case explicitly requests publication; normal local edits retain
	// their coalescing interval.
	if err := manager.RetryDKVSSync(); err != nil {
		t.Fatalf("request the held publication through the existing worker: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		unblock()
		t.Fatal("real account backup did not reach its blocked publication")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	waitErr := manager.WaitAccountManagedDataReady(ctx)
	cancel()
	if !errors.Is(waitErr, context.DeadlineExceeded) {
		unblock()
		t.Fatalf("bounded wait error=%v, want context deadline while publication is held", waitErr)
	}
	if got := manager.GetWalletCatalog()[0].Name; got != "pending backup" {
		unblock()
		t.Fatalf("canceled wait changed the real local edit: name=%q", got)
	}
	unblock()
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := manager.WaitAccountManagedDataReady(ctx); err != nil {
		t.Fatalf("real account backup did not converge after publication resumed: %v", err)
	}
	status := manager.GetAccountManagementStatus()
	if !status.DKVSWriteAllowed || status.ManagedDataDirty || status.PendingChanges != 0 {
		t.Fatalf("completed real backup left an unready account: %+v", status)
	}
}

func TestValidateAccountActivationSecretLocked(t *testing.T) {
	secret := make([]byte, 32)
	secret[0] = 1
	other := append([]byte(nil), secret...)
	other[0] = 2

	if err := (&Manager{}).validateAccountActivationSecretLocked(secret); err != nil {
		t.Fatalf("empty profile rejected recovered secret: %v", err)
	}
	manager := &Manager{
		accountProfile: &accountManagementProfile{},
		accountSecret:  append([]byte(nil), secret...),
	}
	if err := manager.validateAccountActivationSecretLocked(secret); err != nil {
		t.Fatalf("same active secret rejected: %v", err)
	}
	if err := manager.validateAccountActivationSecretLocked(other); !errors.Is(err, ErrAccountManagementSecretConflict) {
		t.Fatalf("different active secret error=%v", err)
	}
	manager.accountSecret = nil
	if err := manager.validateAccountActivationSecretLocked(secret); !errors.Is(err, ErrAccountManagementSecretConflict) {
		t.Fatalf("profile without an unlocked secret did not fail closed: %v", err)
	}
}

type accountManagedDataProviderStub struct {
	id          string
	payloads    []AccountManagedDataPayload
	validateErr error
	importErr   error
	validations int
	imports     int
	catalogs    []AccountManagedDataCatalog
	imported    [][]AccountManagedDataPayload
}

func (p *accountManagedDataProviderStub) ID() string { return p.id }

func (p *accountManagedDataProviderStub) Export(catalog AccountManagedDataCatalog) ([]AccountManagedDataPayload, error) {
	p.catalogs = append(p.catalogs, catalog)
	result := make([]AccountManagedDataPayload, len(p.payloads))
	for index, payload := range p.payloads {
		result[index] = AccountManagedDataPayload{Scope: payload.Scope, Payload: append([]byte(nil), payload.Payload...)}
	}
	return result, nil
}

func (p *accountManagedDataProviderStub) Validate(catalog AccountManagedDataCatalog,
	payloads []AccountManagedDataPayload) error {
	p.validations++
	p.catalogs = append(p.catalogs, catalog)
	return p.validateErr
}

func (p *accountManagedDataProviderStub) Import(catalog AccountManagedDataCatalog,
	payloads []AccountManagedDataPayload) error {
	p.imports++
	p.catalogs = append(p.catalogs, catalog)
	copyValues := make([]AccountManagedDataPayload, len(payloads))
	for index, payload := range payloads {
		copyValues[index] = AccountManagedDataPayload{Scope: payload.Scope, Payload: append([]byte(nil), payload.Payload...)}
	}
	p.imported = append(p.imported, copyValues)
	return p.importErr
}

func TestAccountManagedDataProviderRegistration(t *testing.T) {
	manager := &Manager{}
	provider := &accountManagedDataProviderStub{id: "example.module"}
	if err := manager.RegisterAccountManagedDataProvider(provider); err != nil {
		t.Fatal(err)
	}
	if err := manager.RegisterAccountManagedDataProvider(provider); err == nil {
		t.Fatal("duplicate provider registration was accepted")
	}
	if err := manager.RegisterAccountManagedDataProvider(&accountManagedDataProviderStub{id: "Invalid/ID"}); err == nil {
		t.Fatal("invalid provider id was accepted")
	}
	providers := manager.accountManagedDataProviders()
	if len(providers) != 1 || providers[0].ID() != provider.id {
		t.Fatalf("providers=%v", providers)
	}
}

func TestAccountManagedDataCatalogIncludesEveryMnemonicWalletAccount(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	first := NewInternalWalletWithMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "", GetChainParam())
	second := NewInternalWalletWithMnemonic(
		"legal winner thank year wave sausage worth useful legal winner thank yellow", "", GetChainParam())
	if first == nil || second == nil {
		t.Fatal("create catalog wallets")
	}
	manager := &Manager{
		walletInfoMap: map[int64]*WalletInfo{
			first.GetId(): {
				WalletInDB: WalletInDB{Id: first.GetId(), Accounts: 2, Type: WALLET_TYPE_MNEMONIC}, Wallet: first,
			},
			second.GetId(): {
				WalletInDB: WalletInDB{Id: second.GetId(), Accounts: 3, Type: WALLET_TYPE_MNEMONIC}, Wallet: second,
			},
		},
		accountProfile: &accountManagementProfile{AccountID: "account"},
	}
	catalog, err := manager.accountManagedDataCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if catalog.AccountID != "account" || catalog.Network != "testnet" || len(catalog.Scopes) != 5 {
		t.Fatalf("catalog=%+v", catalog)
	}
	seen := make(map[string]struct{}, len(catalog.Scopes))
	counts := make(map[int64]int)
	for _, scope := range catalog.Scopes {
		if scope.WalletID == 0 || scope.WalletFingerprint == "" || scope.Network != "testnet" {
			t.Fatalf("invalid scope=%+v", scope)
		}
		if _, duplicate := seen[scope.ID()]; duplicate {
			t.Fatalf("duplicate scope=%s", scope.ID())
		}
		seen[scope.ID()] = struct{}{}
		counts[scope.WalletID]++
	}
	if counts[first.GetId()] != 2 || counts[second.GetId()] != 3 {
		t.Fatalf("scope counts=%v", counts)
	}
}
