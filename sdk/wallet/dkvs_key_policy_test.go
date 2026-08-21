package wallet

import (
	"encoding/hex"
	"testing"

	"github.com/sat20-labs/rgb11/baid64"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func TestRGB11AddressMessageIDUsesCanonicalTransferIdentity(t *testing.T) {
	var raw [32]byte
	raw[0] = 1
	raw[31] = 2
	transferID, err := baid64.Encode32(raw, baid64.ConsignmentIDOptions())
	if err != nil {
		t.Fatal(err)
	}
	messageID, err := rgb11AddressMessageID(transferID)
	if err != nil {
		t.Fatal(err)
	}
	want := hex.EncodeToString(raw[:])
	if messageID != want {
		t.Fatalf("message ID=%q want canonical transfer ID bytes %q", messageID, want)
	}
	if _, err := rgb11AddressMessageID("transfer-123"); err == nil {
		t.Fatal("non-canonical transfer ID was accepted or replaced by an implicit derived ID")
	}
}

func TestDKVSKeyBuildersRejectInsteadOfHashingInvalidIDs(t *testing.T) {
	if got := dkvsindexer.NormalizeNameID("Alice Name"); got != "alice_name" {
		t.Fatalf("unexpected reversible name normalization: %q", got)
	}
	if key, err := dkvsindexer.NameKey("Alice Name"); err != nil || key != "/name/alice_name" {
		t.Fatalf("normalized name key=%q err=%v", key, err)
	}
	if key, err := dkvsindexer.ServiceKey("Wallet Service", "authenticity/pwa"); err != nil || key != "/svc/wallet_service/authenticity/pwa" {
		t.Fatalf("normalized service key=%q err=%v", key, err)
	}
	if _, err := dkvsindexer.NameKey("alice/name"); err == nil {
		t.Fatal("illegal name ID was silently replaced instead of rejected")
	}
}

func TestServiceAuthenticityKeyIgnoresReleaseVersion(t *testing.T) {
	first, err := dkvsindexer.ServiceKey("wallet", ServiceAuthenticityPath("pwa", "1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := dkvsindexer.ServiceKey("wallet", ServiceAuthenticityPath("pwa", "2.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("release version changed logical DKVS key: %q != %q", first, second)
	}
	if first != "/svc/wallet/authenticity/pwa" {
		t.Fatalf("unexpected service authenticity key: %q", first)
	}
}
