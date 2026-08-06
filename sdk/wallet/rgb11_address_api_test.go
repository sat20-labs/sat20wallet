package wallet

import (
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

func TestConfiguredRGB11AddressRetentionIsAlwaysFiniteLocal(t *testing.T) {
	manager := &Manager{}
	manager.rgbManager = &rgb11Manager{Manager: manager}
	temporary := dkvsindexer.RecordOptions{}
	manager.rgbManager.configureRGB11AddressCapabilityRetention(nil, &temporary)
	if temporary.TTL != rgb11AddressTemporaryTTL {
		t.Fatalf("temporary retention=%+v", temporary)
	}

	explicit := dkvsindexer.RecordOptions{TTL: 123}
	manager.rgbManager.configureRGB11AddressCapabilityRetention(nil, &explicit)
	if explicit.TTL != 123 {
		t.Fatalf("explicit finite TTL was changed: %+v", explicit)
	}

	transient := dkvsindexer.RecordOptions{}
	manager.rgbManager.configureRGB11AddressTransientRetention(nil, &transient)
	if transient.TTL != rgb11AddressTemporaryTTL {
		t.Fatalf("transient retention=%+v", transient)
	}
	policy := rgb11AddressStoragePolicy(transient)
	if !policy.FreeLocal || policy.Autopay != nil || policy.TTL == 0 {
		t.Fatalf("RGB transport policy is not finite FREE_LOCAL: %+v", policy)
	}
}
