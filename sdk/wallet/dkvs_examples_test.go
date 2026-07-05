package wallet

import (
	"encoding/json"
	"fmt"

	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func ExampleSatsNetDKVSClient_walletRecoveryBackup() {
	priv := exampleDKVSPriv(1)
	pubKey := priv.PubKey().SerializeCompressed()
	key, _ := dkvsindexer.PersonalKey(pubKey, WalletRecoveryPath("wallet-1"))
	value := exampleDKVSJSON(DKVSWalletRecoveryBackup{
		Version:         dkvsAppValueVersion,
		WalletID:        "wallet-1",
		EncryptedBackup: []byte("ciphertext"),
	})
	record, _ := NewDKVSSignedRecord(exampleDKVSWallet(priv), key, value, dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
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

	_, _ = client.PutWalletRecoveryBackup(exampleDKVSWallet(priv), "wallet-1", []byte("ciphertext"), nil, dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
	backup, _, _ := client.GetWalletRecoveryBackup(pubKey, "wallet-1")
	_, _ = client.RenewWalletRecoveryBackup(exampleDKVSWallet(priv), "wallet-1", dkvsindexer.RecordOptions{TTL: 120_000, ExpiryHeight: 200})
	var renewal swire.DKVSRecord
	_ = json.Unmarshal(http.lastBody, &renewal)

	fmt.Println(backup.WalletID, string(backup.EncryptedBackup), renewal.ExpiryHeight)
	// Output: wallet-1 ciphertext 200
}

func ExampleSatsNetDKVSClient_guardianShare() {
	ownerPriv := exampleDKVSPriv(2)
	ownerPubKey := ownerPriv.PubKey().SerializeCompressed()
	mailboxID := dkvsindexer.AccountID(ownerPubKey)
	key, _ := dkvsindexer.MailShareKey(mailboxID, dkvsindexer.NormalizeNameID("recovery-package"), "share-1")
	value := exampleDKVSJSON(DKVSGuardianShare{
		Version:    dkvsAppValueVersion,
		PackageID:  "recovery-package",
		ShareID:    "share-1",
		Ciphertext: []byte("share-ciphertext"),
	})
	record, _ := NewDKVSSignedRecord(exampleDKVSWallet(ownerPriv), key, value, dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
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

	_, _ = client.PutGuardianShare(exampleDKVSWallet(ownerPriv), "recovery-package", "share-1", []byte("share-ciphertext"), nil, dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
	shares, _, total, _ := client.ReadGuardianShares(ownerPubKey, "recovery-package", 0, 10)

	fmt.Println(total, shares[0].PackageID, shares[0].ShareID, string(shares[0].Ciphertext))
	// Output: 1 recovery-package share-1 share-ciphertext
}

func ExampleSatsNetDKVSClient_offlineMessage() {
	recipientPriv := exampleDKVSPriv(3)
	senderPriv := exampleDKVSPriv(4)
	recipientPubKey := recipientPriv.PubKey().SerializeCompressed()
	mailboxID := dkvsindexer.AccountID(recipientPubKey)
	key, _ := dkvsindexer.MailMsgKey(mailboxID, dkvsindexer.NormalizeNameID("msg-1"))
	value := exampleDKVSJSON(DKVSOfflineMessage{
		Version:          dkvsAppValueVersion,
		FromPubKey:       senderPriv.PubKey().SerializeCompressed(),
		ToMailboxID:      mailboxID,
		MessageID:        "msg-1",
		EncryptedMessage: []byte("message-ciphertext"),
	})
	record, _ := NewDKVSSignedRecord(exampleDKVSWallet(senderPriv), key, value, dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
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

	_, _ = client.SendOfflineMessage(exampleDKVSWallet(senderPriv), recipientPubKey, "msg-1", []byte("message-ciphertext"), nil, dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
	messages, _, total, _ := client.ReadOfflineMessages(recipientPubKey, 0, 10)

	fmt.Println(total, messages[0].MessageID, string(messages[0].EncryptedMessage))
	// Output: 1 msg-1 message-ciphertext
}

func ExampleSatsNetDKVSClient_serviceAuthenticity() {
	priv := exampleDKVSPriv(5)
	key, _ := dkvsindexer.ServiceKey("wallet", ServiceAuthenticityPath("desktop", "1.0.0"))
	value := exampleDKVSJSON(DKVSServiceAuthenticity{
		Version:      dkvsAppValueVersion,
		ServiceName:  "wallet",
		AppID:        "desktop",
		Release:      "1.0.0",
		ArtifactHash: "sha256:abcd",
		DownloadURL:  "https://example.invalid/wallet",
	})
	record, _ := NewDKVSSignedRecord(exampleDKVSWallet(priv), key, value, dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
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

	_, _ = client.PublishServiceAuthenticity(exampleDKVSWallet(priv), "wallet", "desktop", "1.0.0", "sha256:abcd", "https://example.invalid/wallet", nil, dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
	authenticity, _, _ := client.GetServiceAuthenticity("wallet", "desktop", "1.0.0")

	fmt.Println(authenticity.ServiceName, authenticity.AppID, authenticity.Release, authenticity.ArtifactHash)
	// Output: wallet desktop 1.0.0 sha256:abcd
}

func ExampleSatsNetDKVSClient_ResolveNameRecord() {
	priv := exampleDKVSPriv(6)
	key, _ := dkvsindexer.NameKey("Alice Name")
	record, _ := NewDKVSSignedRecord(exampleDKVSWallet(priv), key, []byte("profile"), dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
	http := &fakeDKVSHTTPClient{
		getResp: map[string][]byte{
			"testnet/v3/dkvs/records": exampleDKVSJSON(map[string]interface{}{"code": 0, "msg": "ok", "data": record}),
		},
		postResp:   map[string][]byte{},
		deleteResp: map[string][]byte{},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)

	resolution, _ := client.ResolveNameRecord("Alice Name")

	fmt.Println(resolution.NameID == dkvsindexer.NormalizeNameID("Alice Name"), resolution.Record.Key == key)
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
