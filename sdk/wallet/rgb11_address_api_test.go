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

func TestConfiguredRGB11AddressRetentionDefaults(t *testing.T) {
	manager := &Manager{}
	temporary := dkvsindexer.RecordOptions{}
	var noAutopay *DKVSAutopayOptions
	manager.configureRGB11AddressRetention(&temporary, &noAutopay)
	if temporary.TTL != rgb11AddressTemporaryTTL || noAutopay != nil {
		t.Fatalf("temporary retention=%+v autopay=%+v", temporary, noAutopay)
	}

	persistent := dkvsindexer.RecordOptions{}
	explicitAutopay := &DKVSAutopayOptions{}
	manager.configureRGB11AddressRetention(&persistent, &explicitAutopay)
	if persistent.TTL != rgb11AddressAutopayTTL || explicitAutopay == nil {
		t.Fatalf("autopay retention=%+v autopay=%+v", persistent, explicitAutopay)
	}

	explicit := dkvsindexer.RecordOptions{TTL: 12345}
	manager.configureRGB11AddressRetention(&explicit, &noAutopay)
	if explicit.TTL != 12345 {
		t.Fatalf("explicit TTL was changed: %+v", explicit)
	}
}
