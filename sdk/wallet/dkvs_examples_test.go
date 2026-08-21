package wallet

import (
	"encoding/json"
	"fmt"

	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func ExampleSatsNetDKVSClient_offlineMessage() {
	recipientPriv := exampleDKVSPriv(3)
	senderPriv := exampleDKVSPriv(4)
	recipientPubKey := recipientPriv.PubKey().SerializeCompressed()
	mailboxID := dkvsindexer.AccountID(recipientPubKey)
	senderID := dkvsindexer.AccountID(senderPriv.PubKey().SerializeCompressed())
	const msgID int64 = 1786600000123
	keyID, _ := offlineMessageKeyID(msgID)
	key, _ := dkvsindexer.MailMsgKey(mailboxID, senderID, keyID)
	value, _ := encodeDKVSOfflineMessage(DKVSOfflineMessage{
		Version:          dkvsAppValueVersion,
		FromPubKey:       senderPriv.PubKey().SerializeCompressed(),
		ToMailboxID:      mailboxID,
		MessageID:        msgID,
		EncryptedMessage: []byte("message-ciphertext"),
	})
	record, _ := NewDKVSSignedRecord(exampleDKVSWallet(senderPriv), key, value, dkvsindexer.RecordOptions{Seq: 1, TTL: 100})
	http := &fakeDKVSHTTPClient{
		getResp: map[string][]byte{
			"testnet/v3/dkvs/records/prefix": exampleDKVSJSON(map[string]interface{}{"code": 0, "msg": "ok", "total": 1, "data": []*swire.DKVSRecord{record}}),
		},
		postResp: map[string][]byte{
			"testnet/v3/dkvs/records": exampleDKVSJSON(map[string]interface{}{"code": 0, "msg": "ok", "data": record}),
		},
		deleteResp: map[string][]byte{},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)

	_, _ = client.SendOfflineMessage(exampleDKVSWallet(senderPriv), recipientPubKey, msgID, []byte("message-ciphertext"), nil, dkvsindexer.RecordOptions{Seq: 1, TTL: 100})
	messages, _, total, _ := client.ReadOfflineMessages(recipientPubKey, 0, 10)

	fmt.Println(total, messages[0].MessageID, string(messages[0].EncryptedMessage))
	// Output: 1 1786600000123 message-ciphertext
}

func ExampleSatsNetDKVSClient_serviceAuthenticity() {
	priv := exampleDKVSPriv(5)
	key, _ := dkvsindexer.ServiceKey("wallet", ServiceAuthenticityPath("pwa", "1.0.0"))
	value, _ := encodeDKVSServiceAuthenticity(DKVSServiceAuthenticity{
		Version:      dkvsAppValueVersion,
		ServiceName:  "wallet",
		AppID:        "pwa",
		Release:      "1.0.0",
		ArtifactHash: "sha256:abcd",
		DownloadURL:  "https://example.invalid/wallet",
	})
	record, _ := NewDKVSSignedRecord(exampleDKVSWallet(priv), key, value, dkvsindexer.RecordOptions{Seq: 1, TTL: 100})
	http := &fakeDKVSHTTPClient{
		getResp: map[string][]byte{
			"testnet/v3/dkvs/records": exampleDKVSJSON(map[string]interface{}{"code": 0, "msg": "ok", "data": record}),
		},
		postResp: map[string][]byte{
			"testnet/v3/dkvs/records": exampleDKVSJSON(map[string]interface{}{"code": 0, "msg": "ok", "data": record}),
		},
		deleteResp: map[string][]byte{},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)

	_, _ = client.PublishServiceAuthenticity(exampleDKVSWallet(priv), "wallet", "pwa", "1.0.0", "sha256:abcd", "https://example.invalid/wallet", nil, dkvsindexer.RecordOptions{Seq: 1, TTL: 100})
	authenticity, _, _ := client.GetServiceAuthenticity("wallet", "pwa", "1.0.0")

	fmt.Println(authenticity.ServiceName, authenticity.AppID, authenticity.Release, authenticity.ArtifactHash)
	// Output: wallet pwa 1.0.0 sha256:abcd
}

func ExampleSatsNetDKVSClient_ResolveNameRecord() {
	priv := exampleDKVSPriv(6)
	key, _ := dkvsindexer.NameKey("alice-name")
	record, _ := NewDKVSSignedRecord(exampleDKVSWallet(priv), key, []byte("profile"), dkvsindexer.RecordOptions{Seq: 1, TTL: 100})
	http := &fakeDKVSHTTPClient{
		getResp: map[string][]byte{
			"testnet/v3/dkvs/records": exampleDKVSJSON(map[string]interface{}{"code": 0, "msg": "ok", "data": record}),
		},
		postResp:   map[string][]byte{},
		deleteResp: map[string][]byte{},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)

	resolution, _ := client.ResolveNameRecord("alice-name")

	fmt.Println(resolution.NameID == "alice-name", resolution.Record.Key == key)
	// Output: true true
}

func exampleDKVSPriv(seed byte) *btcec.PrivateKey {
	key := make([]byte, 32)
	key[31] = seed
	priv, _ := btcec.PrivKeyFromBytes(key)
	return priv
}

func exampleDKVSWallet(priv *btcec.PrivateKey) *InternalWallet {
	w, _, err := NewInternalWalletWithPrivKey(priv.Serialize(), GetChainParam())
	if err != nil {
		panic(err)
	}
	return w
}

func exampleDKVSJSON(value interface{}) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
