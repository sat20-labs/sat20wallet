package wallet

import (
	"encoding/json"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func TestSatsNetDKVSClientOfflineMessage(t *testing.T) {
	ownerPriv, _ := btcec.NewPrivateKey()
	senderPriv, _ := btcec.NewPrivateKey()
	ownerPubKey := ownerPriv.PubKey().SerializeCompressed()
	mailboxID := dkvsindexer.AccountID(ownerPubKey)
	senderID := dkvsindexer.AccountID(senderPriv.PubKey().SerializeCompressed())
	const msgID int64 = 1786600000123
	stableMsgID, err := offlineMessageKeyID(msgID)
	if err != nil {
		t.Fatal(err)
	}
	msgValue, err := encodeDKVSOfflineMessage(DKVSOfflineMessage{
		Version: dkvsAppValueVersion, FromPubKey: senderPriv.PubKey().SerializeCompressed(),
		ToMailboxID: mailboxID, MessageID: msgID, EncryptedMessage: []byte("message ciphertext"),
	})
	if err != nil {
		t.Fatal(err)
	}
	msgKey, err := dkvsindexer.MailMsgKey(mailboxID, senderID, stableMsgID)
	if err != nil {
		t.Fatal(err)
	}
	msgRecord, err := NewDKVSSignedRecord(dkvsTestWalletFromPriv(t, senderPriv), msgKey, msgValue,
		dkvsindexer.RecordOptions{Seq: 1, TTL: 100})
	if err != nil {
		t.Fatal(err)
	}
	http := &fakeDKVSHTTPClient{
		getResp: map[string][]byte{
			"testnet/v3/dkvs/records/prefix": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "total": 1, "data": []*swire.DKVSRecord{msgRecord}}),
		},
		postResp: map[string][]byte{
			"testnet/v3/dkvs/records": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": msgRecord}),
		}, deleteResp: map[string][]byte{},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)
	var posted swire.DKVSRecord
	if _, err := client.SendOfflineMessage(dkvsTestWalletFromPriv(t, senderPriv), ownerPubKey, msgID,
		[]byte("message ciphertext"), nil, dkvsindexer.RecordOptions{Seq: 1, TTL: 100}); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(http.lastBody, &posted); err != nil {
		t.Fatal(err)
	}
	if posted.Key != msgKey {
		t.Fatalf("message key=%s want=%s", posted.Key, msgKey)
	}
	const paidID int64 = msgID + 1
	if _, err := client.SendOfflineMessageWithAutopay(dkvsTestWalletFromPriv(t, senderPriv), ownerPubKey, paidID,
		[]byte("paid ciphertext"), nil, dkvsindexer.RecordOptions{Seq: 1, TTL: 100},
		DKVSAutopayOptions{AddressParams: &chaincfg.TestNetParams, PoolContract: "tc1pofflineautopay"}); err != nil {
		t.Fatal(err)
	}
	messages, _, total, err := client.ReadOfflineMessages(ownerPubKey, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(messages) != 1 || messages[0].MessageID != msgID {
		t.Fatalf("messages=%#v total=%d", messages, total)
	}
	first, _ := offlineMessageKeyID(msgID)
	second, _ := offlineMessageKeyID(paidID)
	if first >= second {
		t.Fatalf("message key order is not chronological: %s >= %s", first, second)
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
	value, err := encodeDKVSServiceAuthenticity(DKVSServiceAuthenticity{
		Version: dkvsAppValueVersion, ServiceName: "wallet", AppID: "pwa", Release: "1.0.0",
		ArtifactHash: "sha256:abc", DownloadURL: "https://wallet.example.invalid/",
		Metadata: map[string]string{"channel": "stable"},
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewDKVSSignedRecord(dkvsTestWalletFromPriv(t, priv), serviceKey, value,
		dkvsindexer.RecordOptions{Seq: 1, TTL: 100})
	if err != nil {
		t.Fatal(err)
	}
	http := &fakeDKVSHTTPClient{
		getResp:    map[string][]byte{"testnet/v3/dkvs/records": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": record})},
		postResp:   map[string][]byte{"testnet/v3/dkvs/records": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": record})},
		deleteResp: map[string][]byte{},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)
	if _, err := client.PublishServiceAuthenticity(dkvsTestWalletFromPriv(t, priv), "wallet", "pwa", "1.0.0",
		"sha256:abc", "https://wallet.example.invalid/", map[string]string{"channel": "stable"},
		dkvsindexer.RecordOptions{Seq: 1, TTL: 100}); err != nil {
		t.Fatal(err)
	}
	authenticity, gotRecord, err := client.GetServiceAuthenticity("wallet", "pwa", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if gotRecord.Key != serviceKey || authenticity.AppID != "pwa" || authenticity.Release != "1.0.0" ||
		authenticity.ArtifactHash != "sha256:abc" || authenticity.Metadata["channel"] != "stable" {
		t.Fatalf("authenticity=%#v record=%s", authenticity, gotRecord.Key)
	}
}
