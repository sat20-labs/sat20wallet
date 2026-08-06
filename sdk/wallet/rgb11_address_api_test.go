package wallet

import (
	"errors"
	"strings"
	"testing"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func TestRGB11AddressMessageIDIsBoundedAndDomainSeparated(t *testing.T) {
	first, err := rgb11AddressMessageID("canonical-rgb-transfer-id")
	if err != nil {
		t.Fatal(err)
	}
	second, err := rgb11AddressMessageID("canonical-rgb-transfer-id")
	if err != nil {
		t.Fatal(err)
	}
	other, err := rgb11AddressMessageID("other-canonical-rgb-transfer-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 64 || first != strings.ToLower(first) || first != second || first == other {
		t.Fatalf("message IDs first=%q second=%q other=%q", first, second, other)
	}
	if _, err := rgb11AddressMessageID(""); err == nil {
		t.Fatal("empty transfer ID produced a mailbox message ID")
	}
}

func TestConfiguredRGB11AddressRetentionUsesServiceNodePolicy(t *testing.T) {
	remote := newRGB11MemoryDKVSHTTP()
	remote.freeLocal.MaxTTL = 777
	client := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	store := &dkvsStore{client: client}
	manager := &Manager{}
	manager.rgbManager = &rgb11Manager{Manager: manager}

	for _, initial := range []uint64{0, 123, 9999} {
		options := dkvsindexer.RecordOptions{TTL: initial}
		if err := manager.rgbManager.configureRGB11AddressCapabilityRetention(store, &options); err != nil {
			t.Fatal(err)
		}
		if options.TTL != remote.freeLocal.MaxTTL {
			t.Fatalf("initial=%d retention=%d want=%d", initial, options.TTL, remote.freeLocal.MaxTTL)
		}
	}

	transient := dkvsindexer.RecordOptions{}
	if err := manager.rgbManager.configureRGB11AddressTransientRetention(store, &transient); err != nil {
		t.Fatal(err)
	}
	policy := rgb11AddressStoragePolicy(transient)
	if !policy.FreeLocal || policy.Autopay != nil || policy.TTL != remote.freeLocal.MaxTTL {
		t.Fatalf("RGB transport policy=%+v", policy)
	}

	remote.freeLocal.Enabled = false
	if err := manager.rgbManager.configureRGB11AddressTransientRetention(store,
		&dkvsindexer.RecordOptions{}); !errors.Is(err, dkvsindexer.ErrFreeLocalDisabled) {
		t.Fatalf("disabled FREE_LOCAL err=%v", err)
	}
}
