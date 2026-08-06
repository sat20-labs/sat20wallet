package wallet

import (
	"testing"
	"time"

	"github.com/sat20-labs/rgb11/invoicing"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func addRGB11RecoveryReceive(t *testing.T, manager *Manager, account localRGB11Account,
	recipient string) {
	t.Helper()
	scoped, err := manager.newScopedRGB11Manager(account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scoped.engine.CreateReceive(corewallet.ReceiveParams{
		Network: invoicing.BitcoinTestnet4, RecipientID: recipient,
		WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRGB11AccountManagedProviderExportsAllNonEmptyWalletAccountScopes(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager := newAccountManagementAutoTestManager(t)
	firstID, err := manager.ImportWallet(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"password",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.EnsureAccount(firstID, 1, "first-1", "did:first:1"); err != nil {
		t.Fatal(err)
	}
	secondID, err := manager.ImportWallet(
		"legal winner thank year wave sausage worth useful legal winner thank yellow",
		"password",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.EnsureAccount(secondID, 2, "second-2", "did:second:2"); err != nil {
		t.Fatal(err)
	}
	catalog, err := manager.accountManagedDataCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Scopes) != 5 {
		t.Fatalf("catalog scopes=%d want=5", len(catalog.Scopes))
	}
	provider := &rgb11AccountManagedDataProvider{owner: manager}
	accounts, err := provider.accountsByScope()
	if err != nil {
		t.Fatal(err)
	}
	selected := []AccountManagedDataScope{catalog.Scopes[0], catalog.Scopes[len(catalog.Scopes)-1]}
	for index, scope := range selected {
		accountValue, ok := accounts[scope.ID()]
		if !ok {
			t.Fatalf("missing local account for scope %s", scope.ID())
		}
		addRGB11RecoveryReceive(t, manager, accountValue, "managed-scope-"+string(rune('a'+index)))
	}
	payloads, err := provider.Export(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(payloads) != len(selected) {
		t.Fatalf("RGB11 payloads=%d want=%d", len(payloads), len(selected))
	}
	seen := make(map[string]struct{}, len(payloads))
	for _, payload := range payloads {
		seen[payload.Scope] = struct{}{}
		packageValue, err := rgb11wallet.DecodeRecoveryPackage(payload.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if len(packageValue.EngineRecords) != 1 || len(packageValue.ProjectionRecords) != 0 {
			t.Fatalf("scope=%s recovery=%+v", payload.Scope, packageValue)
		}
	}
	for _, scope := range selected {
		if _, ok := seen[scope.ID()]; !ok {
			t.Fatalf("non-empty RGB11 scope was not exported: %s", scope.ID())
		}
	}
	if err := provider.Validate(catalog, payloads); err != nil {
		t.Fatal(err)
	}
}

func TestRGB11AccountManagedProviderMissingPayloadClearsStaleScope(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager := newAccountManagementAutoTestManager(t)
	_, err := manager.ImportWallet(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"password",
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := manager.accountManagedDataCatalog()
	if err != nil {
		t.Fatal(err)
	}
	provider := &rgb11AccountManagedDataProvider{owner: manager}
	accounts, err := provider.accountsByScope()
	if err != nil {
		t.Fatal(err)
	}
	scope := catalog.Scopes[0]
	accountValue := accounts[scope.ID()]
	addRGB11RecoveryReceive(t, manager, accountValue, "stale-receive")
	payloads, err := provider.Export(catalog)
	if err != nil || len(payloads) != 1 {
		t.Fatalf("initial payloads=%d err=%v", len(payloads), err)
	}
	if err := provider.Import(catalog, nil); err != nil {
		t.Fatal(err)
	}
	scoped, err := manager.newScopedRGB11Manager(accountValue)
	if err != nil {
		t.Fatal(err)
	}
	walletID, err := scoped.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	full, _, err := scoped.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.EngineRecords) != 0 || len(full.ProjectionRecords) != 0 {
		t.Fatalf("stale scope survived authoritative empty import: %+v", full)
	}
}
