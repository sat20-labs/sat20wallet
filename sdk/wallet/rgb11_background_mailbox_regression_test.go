package wallet

import (
	"context"
	"errors"
	"reflect"
	"testing"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func TestRGB11BackgroundMailboxWithoutRecoveryPreservesPublicGate(t *testing.T) {
	manager, _ := finalDKVSTestManager(t)
	if manager.accountManagedRecoveryConfigured() {
		t.Fatal("fixture unexpectedly configured recovery")
	}
	walletID, err := manager.rgbManager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.syncRootRGB11AddressMailbox(context.Background(),
		dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
	if err != nil || !reflect.DeepEqual(result, &RGB11AddressMailboxSyncResult{}) {
		t.Fatalf("unconfigured background RGB polling obstructed ordinary mailbox sync: %v", err)
	}
	if _, err := manager.SyncConfiguredRGB11AddressMailbox(context.Background(),
		dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{}); !errors.Is(err, ErrDKVSPathNotSynced) {
		t.Fatalf("public RGB operation bypassed the recovery barrier: %v", err)
	}
	after, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("unconfigured polling changed RGB recovery state")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.syncRootRGB11AddressMailbox(ctx,
		dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled background polling accepted: %v", err)
	}
}
