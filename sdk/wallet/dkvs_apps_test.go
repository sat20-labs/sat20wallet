package wallet

import (
	"errors"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func TestSatsNetDKVSClientOfflineMessageRequiresMessageManager(t *testing.T) {
	ownerPriv, _ := btcec.NewPrivateKey()
	senderPriv, _ := btcec.NewPrivateKey()
	ownerPubKey := ownerPriv.PubKey().SerializeCompressed()
	const firstID int64 = 1786600000123
	const secondID int64 = firstID + 1
	remote := newRGB11MemoryDKVSHTTP()
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", remote)

	if _, err := client.SendOfflineMessage(dkvsTestWalletFromPriv(t, senderPriv), ownerPubKey, firstID,
		[]byte("message ciphertext"), nil,
		dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 1, TTL: 100}); !errors.Is(err, ErrDKVSMessageManagerRequired) {
		t.Fatalf("low-level offline send err=%v", err)
	}
	if _, _, _, err := client.ReadOfflineMessages(ownerPubKey, 0, 10); !errors.Is(err, ErrDKVSMessageManagerRequired) {
		t.Fatalf("low-level offline read err=%v", err)
	}
	firstKey, _ := offlineMessageKeyID(firstID)
	secondKey, _ := offlineMessageKeyID(secondID)
	if firstKey >= secondKey {
		t.Fatalf("message key order is not chronological: %s >= %s", firstKey, secondKey)
	}
}

func TestSatsNetDKVSClientServiceAuthenticity(t *testing.T) {
	priv, _ := btcec.NewPrivateKey()
	serviceKey, err := dkvsindexer.ServiceKey("wallet", ServiceAuthenticityPath("pwa", "1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	serviceKeyNext, err := dkvsindexer.ServiceKey("wallet", ServiceAuthenticityPath("pwa", "2.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if serviceKey != "/svc/wallet/authenticity/pwa" || serviceKeyNext != serviceKey {
		t.Fatalf("unexpected stable PWA key: %s next=%s", serviceKey, serviceKeyNext)
	}
	remote := newRGB11MemoryDKVSHTTP()
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", remote)
	if _, err := client.PublishServiceAuthenticity(dkvsTestWalletFromPriv(t, priv), "wallet", "pwa", "1.0.0",
		"sha256:abc", "https://wallet.example.invalid/", map[string]string{"channel": "stable"},
		dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 1, TTL: 100}); err != nil {
		t.Fatal(err)
	}
	authenticity, record, err := client.GetServiceAuthenticity("wallet", "pwa", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if record.Key != serviceKey || authenticity.AppID != "pwa" || authenticity.Release != "1.0.0" ||
		authenticity.ArtifactHash != "sha256:abc" || authenticity.Metadata["channel"] != "stable" {
		t.Fatalf("authenticity=%#v record=%s", authenticity, record.Key)
	}
}
