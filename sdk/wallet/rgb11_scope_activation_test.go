package wallet

import (
	"errors"
	"strings"
	"testing"

	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func TestNewManagerLeavesRGB11UnscopedUntilWalletActivation(t *testing.T) {
	database := newMemoryKVDB()
	manager := NewManager(&sdkcommon.Config{
		Env: "test", Chain: "testnet", Mode: CLIENT_NODE,
		IndexerL1: &sdkcommon.Indexer{Scheme: "http", Host: "l1.test", Proxy: "btc/testnet"},
		IndexerL2: &sdkcommon.Indexer{Scheme: "http", Host: "l2.test", Proxy: "satsnet/testnet"},
	}, database)
	if manager == nil {
		t.Fatal("NewManager returned nil")
	}
	t.Cleanup(func() {
		if manager.rgbManager != nil && manager.rgbManager.scopeStates != nil {
			manager.rgbManager.scopeStates.stopReconciliations()
		}
	})

	if _, err := manager.rgbManager.projectionStore.ExportSnapshot(); !errors.Is(err, rgb11wallet.ErrWalletScope) {
		t.Fatalf("locked manager RGB11 scope err=%v", err)
	}
	for key := range database.data {
		if strings.HasPrefix(key, "rgb11-wallet-0-account-") ||
			strings.HasPrefix(key, "rgb11-engine-wallet-0-account-") {
			t.Fatalf("constructor created invalid RGB11 key %q", key)
		}
	}
}

func TestInvalidWalletIdentityClearsPreviousRGB11Scope(t *testing.T) {
	manager := newAccountManagementAutoTestManager(t)
	if err := manager.rgbManager.projectionStore.SetScope("wallet-stale-account-0"); err != nil {
		t.Fatal(err)
	}
	if err := manager.rgbManager.engineStore.SetScope("wallet-stale-account-0"); err != nil {
		t.Fatal(err)
	}

	if err := manager.rgbManager.selectRGB11Scope(); !errors.Is(err, rgb11wallet.ErrWalletScope) {
		t.Fatalf("invalid identity scope err=%v", err)
	}
	if _, err := manager.rgbManager.projectionStore.ExportSnapshot(); !errors.Is(err, rgb11wallet.ErrWalletScope) {
		t.Fatalf("projection retained stale scope: %v", err)
	}
	if _, err := manager.rgbManager.engineStore.ExportSnapshot(); !errors.Is(err, rgb11wallet.ErrWalletScope) {
		t.Fatalf("engine retained stale scope: %v", err)
	}
}

func TestAccountRecoverySelectsRealRGB11WalletScope(t *testing.T) {
	remote := newRGB11MemoryDKVSHTTP()
	source, _ := managedImportTestSource(t, remote)
	target, _ := managedImportTestManager(t, remote)
	if target.status.CurrentWallet != 0 || target.wallet != nil {
		t.Fatalf("target unexpectedly had an active wallet: id=%d wallet=%v",
			target.status.CurrentWallet, target.wallet)
	}

	if err := restoreManagedImportTestWallet(t, target, source); err != nil {
		t.Fatal(err)
	}
	walletID := target.status.CurrentWallet
	if walletID == 0 || target.wallet == nil {
		t.Fatalf("recovery did not activate a real wallet: id=%d wallet=%v", walletID, target.wallet)
	}
	if err := target.rgbManager.projectionStore.SaveLocalMetadata("scope-probe", []byte("ok")); err != nil {
		t.Fatal(err)
	}

	database := target.db.(*memoryKVDB)
	want := "rgb11-local-" + rgb11StorageScope(walletID, 0) + "-scope-probe"
	if got := string(database.data[want]); got != "ok" {
		t.Fatalf("restored RGB11 scope key %q=%q", want, got)
	}
	for key := range database.data {
		if strings.HasPrefix(key, "rgb11-wallet-0-account-") ||
			strings.HasPrefix(key, "rgb11-engine-wallet-0-account-") {
			t.Fatalf("recovery created invalid RGB11 key %q", key)
		}
	}
}
