package wallet

import (
	"errors"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

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

func TestAccountManagedDataImportValidatesAllProvidersBeforeMutation(t *testing.T) {
	manager := &Manager{}
	first := &accountManagedDataProviderStub{id: "a"}
	second := &accountManagedDataProviderStub{id: "b", validateErr: errors.New("invalid b")}
	if err := manager.RegisterAccountManagedDataProvider(first); err != nil {
		t.Fatal(err)
	}
	if err := manager.RegisterAccountManagedDataProvider(second); err != nil {
		t.Fatal(err)
	}
	catalog := AccountManagedDataCatalog{
		AccountID: "account", Network: "testnet",
		Scopes: []AccountManagedDataScope{{WalletFingerprint: "wallet", AccountIndex: 0, Network: "testnet"}},
	}
	scope := catalog.Scopes[0].ID()
	bundle := account.ManagedDataBundle{
		Version: account.ManagedDataBundleVersion, Revision: 1,
		Items: []account.ManagedDataItem{
			{Provider: "a", Scope: AccountManagedDataGlobalScope, Payload: []byte("a")},
			{Provider: "b", Scope: scope, Payload: []byte("b")},
		},
	}
	if err := manager.importAccountManagedData(catalog, bundle); err == nil {
		t.Fatal("invalid provider bundle was imported")
	}
	if first.validations != 1 || second.validations != 1 || first.imports != 0 || second.imports != 0 {
		t.Fatalf("first validate/import=%d/%d second=%d/%d",
			first.validations, first.imports, second.validations, second.imports)
	}
}

func TestAccountManagedDataImportRejectsUnknownProviderBeforeMutation(t *testing.T) {
	manager := &Manager{}
	known := &accountManagedDataProviderStub{id: "known"}
	if err := manager.RegisterAccountManagedDataProvider(known); err != nil {
		t.Fatal(err)
	}
	catalog := AccountManagedDataCatalog{AccountID: "account", Network: "testnet"}
	bundle := account.ManagedDataBundle{
		Version: account.ManagedDataBundleVersion, Revision: 1,
		Items: []account.ManagedDataItem{{
			Provider: "unknown", Scope: AccountManagedDataGlobalScope, Payload: []byte("value"),
		}},
	}
	if err := manager.importAccountManagedData(catalog, bundle); err == nil {
		t.Fatal("unknown provider bundle was imported")
	}
	if known.validations != 0 || known.imports != 0 {
		t.Fatalf("known provider was touched: validate=%d import=%d", known.validations, known.imports)
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

func TestMergeAccountManagedDataBundlesMergesIndependentProviderScopes(t *testing.T) {
	scopeA := AccountManagedDataScope{WalletFingerprint: "wallet-a", AccountIndex: 0, Network: "testnet"}.ID()
	scopeB := AccountManagedDataScope{WalletFingerprint: "wallet-b", AccountIndex: 0, Network: "testnet"}.ID()
	catalog := AccountManagedDataCatalog{Network: "testnet", Scopes: []AccountManagedDataScope{
		{WalletFingerprint: "wallet-a", AccountIndex: 0, Network: "testnet"},
		{WalletFingerprint: "wallet-b", AccountIndex: 0, Network: "testnet"},
	}}
	base := account.ManagedDataBundle{Version: account.ManagedDataBundleVersion, Revision: 1, Items: []account.ManagedDataItem{
		{Provider: "alpha", Scope: scopeA, Payload: []byte("alpha-base")},
		{Provider: "beta", Scope: scopeB, Payload: []byte("beta-base")},
	}}
	remote := account.ManagedDataBundle{Version: account.ManagedDataBundleVersion, Revision: 2, Items: []account.ManagedDataItem{
		{Provider: "alpha", Scope: scopeA, Payload: []byte("alpha-remote")},
		{Provider: "beta", Scope: scopeB, Payload: []byte("beta-base")},
	}}
	local := account.ManagedDataBundle{Version: account.ManagedDataBundleVersion, Revision: 2, Items: []account.ManagedDataItem{
		{Provider: "alpha", Scope: scopeA, Payload: []byte("alpha-base")},
		{Provider: "beta", Scope: scopeB, Payload: []byte("beta-local")},
	}}
	merged, err := mergeAccountManagedDataBundles(base, remote, local, catalog, 3)
	if err != nil {
		t.Fatal(err)
	}
	values := accountManagedDataItemMap(merged)
	if string(values["alpha\x00"+scopeA].Payload) != "alpha-remote" ||
		string(values["beta\x00"+scopeB].Payload) != "beta-local" {
		t.Fatalf("merged=%+v", merged)
	}
}

func TestMergeAccountManagedDataBundlesDropsRemovedScope(t *testing.T) {
	removedScope := AccountManagedDataScope{WalletFingerprint: "removed", AccountIndex: 0, Network: "testnet"}.ID()
	item := account.ManagedDataItem{Provider: "module", Scope: removedScope, Payload: []byte("old")}
	base := account.ManagedDataBundle{Version: account.ManagedDataBundleVersion, Revision: 1, Items: []account.ManagedDataItem{item}}
	remote := account.ManagedDataBundle{Version: account.ManagedDataBundleVersion, Revision: 2, Items: []account.ManagedDataItem{item}}
	local := account.ManagedDataBundle{Version: account.ManagedDataBundleVersion, Revision: 2, Items: []account.ManagedDataItem{item}}
	merged, err := mergeAccountManagedDataBundles(base, remote, local,
		AccountManagedDataCatalog{Network: "testnet"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Items) != 0 {
		t.Fatalf("removed scope survived merge: %+v", merged.Items)
	}
}
