package wallet

import (
	"encoding/json"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func TestSatsNetDKVSClientWalletRecoveryBackup(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	pubKey := priv.PubKey().SerializeCompressed()
	path := WalletRecoveryPath("primary wallet")
	key, err := dkvsindexer.PersonalKey(pubKey, path)
	if err != nil {
		t.Fatal(err)
	}
	value, err := encodeDKVSRecoveryBackup(DKVSWalletRecoveryBackup{
		Version:         dkvsAppValueVersion,
		WalletID:        "primary wallet",
		EncryptedBackup: []byte("ciphertext"),
		Metadata:        map[string]string{"device": "phone"},
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewDKVSSignedRecord(dkvsTestWalletFromPriv(t, priv), key, value, dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	http := &fakeDKVSHTTPClient{
		getResp: map[string][]byte{
			"testnet/v3/dkvs/records": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": record}),
		},
		postResp: map[string][]byte{
			"testnet/v3/dkvs/records": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": record}),
		},
		deleteResp: map[string][]byte{},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)

	if _, err := client.PutWalletRecoveryBackup(dkvsTestWalletFromPriv(t, priv), "primary wallet", []byte("ciphertext"), map[string]string{"device": "phone"}, dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100}); err != nil {
		t.Fatal(err)
	}
	var posted swire.DKVSRecord
	if err := json.Unmarshal(http.lastBody, &posted); err != nil {
		t.Fatal(err)
	}
	if posted.Key != key {
		t.Fatalf("backup key=%s want=%s", posted.Key, key)
	}
	if len(posted.Value) == 0 || posted.Value[0] == '{' {
		t.Fatalf("backup was not compact: %q", posted.Value)
	}
	postedBackup, err := decodeDKVSRecoveryBackup(posted.Value)
	if err != nil {
		t.Fatal(err)
	}
	if postedBackup.WalletID != "primary wallet" || string(postedBackup.EncryptedBackup) != "ciphertext" {
		t.Fatalf("backup=%#v", postedBackup)
	}
	backup, gotRecord, err := client.GetWalletRecoveryBackup(pubKey, "primary wallet")
	if err != nil {
		t.Fatal(err)
	}
	if gotRecord.Key != key || backup.Metadata["device"] != "phone" || http.lastGet.Query["key"] != key {
		t.Fatalf("backup=%#v record=%s query=%v", backup, gotRecord.Key, http.lastGet.Query)
	}
	if _, err := client.RenewWalletRecoveryBackup(dkvsTestWalletFromPriv(t, priv), "primary wallet", dkvsindexer.RecordOptions{TTL: 120_000, ExpiryHeight: 200}); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(http.lastBody, &posted); err != nil {
		t.Fatal(err)
	}
	if posted.Key != key || posted.Seq != record.Seq || posted.ExpiryHeight != 200 {
		t.Fatalf("backup renewal posted=%#v", posted)
	}
	if string(posted.Value) != string(record.Value) {
		t.Fatalf("backup renewal changed value")
	}
	if _, err := client.PutWalletRecoveryBackup(nil, "primary wallet", []byte("ciphertext"), nil, dkvsindexer.RecordOptions{}); err != dkvsindexer.ErrInvalidSignature {
		t.Fatalf("nil signer err=%v", err)
	}
}

func TestSatsNetDKVSClientGuardianShareAndOfflineMessage(t *testing.T) {
	ownerPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	senderPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	ownerPubKey := ownerPriv.PubKey().SerializeCompressed()
	mailboxID := dkvsindexer.AccountID(ownerPubKey)
	shareKey, err := dkvsindexer.MailShareKey(mailboxID, dkvsindexer.NormalizeNameID("recovery package"), dkvsindexer.NormalizeNameID("share one"))
	if err != nil {
		t.Fatal(err)
	}
	shareValue, err := encodeDKVSGuardianShare(DKVSGuardianShare{
		Version:    dkvsAppValueVersion,
		PackageID:  "recovery package",
		ShareID:    "share one",
		Ciphertext: []byte("share ciphertext"),
	})
	if err != nil {
		t.Fatal(err)
	}
	shareRecord, err := NewDKVSSignedRecord(dkvsTestWalletFromPriv(t, ownerPriv), shareKey, shareValue, dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	msgValue, err := encodeDKVSOfflineMessage(DKVSOfflineMessage{
		Version:          dkvsAppValueVersion,
		FromPubKey:       senderPriv.PubKey().SerializeCompressed(),
		ToMailboxID:      mailboxID,
		MessageID:        "msg one",
		EncryptedMessage: []byte("message ciphertext"),
	})
	if err != nil {
		t.Fatal(err)
	}
	senderID := dkvsindexer.AccountID(senderPriv.PubKey().SerializeCompressed())
	msgKey, err := dkvsindexer.MailMsgKey(mailboxID, senderID, dkvsindexer.NormalizeNameID("msg one"))
	if err != nil {
		t.Fatal(err)
	}
	msgRecord, err := NewDKVSSignedRecord(dkvsTestWalletFromPriv(t, senderPriv), msgKey, msgValue, dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	http := &fakeDKVSHTTPClient{
		getResp: map[string][]byte{
			"testnet/v3/dkvs/records/prefix": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "total": 1, "data": []*swire.DKVSRecord{shareRecord}}),
		},
		postResp: map[string][]byte{
			"testnet/v3/dkvs/records": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": shareRecord}),
		},
		deleteResp: map[string][]byte{},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)

	if _, err := client.PutGuardianShare(dkvsTestWalletFromPriv(t, ownerPriv), "recovery package", "share one", []byte("share ciphertext"), nil, dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100}); err != nil {
		t.Fatal(err)
	}
	var posted swire.DKVSRecord
	if err := json.Unmarshal(http.lastBody, &posted); err != nil {
		t.Fatal(err)
	}
	if posted.Key != shareKey {
		t.Fatalf("share key=%s want=%s", posted.Key, shareKey)
	}
	shares, records, total, err := client.ReadGuardianShares(ownerPubKey, "recovery package", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	wantSharePrefix := "/mail/" + mailboxID + "/share/" + dkvsindexer.NormalizeNameID("recovery package")
	if total != 1 || len(shares) != 1 || len(records) != 1 || shares[0].ShareID != "share one" || http.lastGet.Query["prefix"] != wantSharePrefix {
		t.Fatalf("shares=%#v total=%d query=%v", shares, total, http.lastGet.Query)
	}

	http.getResp["testnet/v3/dkvs/records/prefix"] = mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "total": 1, "data": []*swire.DKVSRecord{msgRecord}})
	if _, err := client.SendOfflineMessage(dkvsTestWalletFromPriv(t, senderPriv), ownerPubKey, "msg one", []byte("message ciphertext"), nil, dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100}); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(http.lastBody, &posted); err != nil {
		t.Fatal(err)
	}
	if posted.Key != msgKey {
		t.Fatalf("message key=%s want=%s", posted.Key, msgKey)
	}
	if _, err := client.SendOfflineMessageWithAutopay(
		dkvsTestWalletFromPriv(t, senderPriv), ownerPubKey, "paid message", []byte("paid ciphertext"), nil,
		dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100},
		DKVSAutopayOptions{AddressParams: &chaincfg.TestNetParams, PoolContract: "tc1pofflineautopay"},
	); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(http.lastBody, &posted); err != nil {
		t.Fatal(err)
	}
	wantPaidMsgKey, err := dkvsindexer.MailMsgKey(mailboxID, senderID, dkvsindexer.NormalizeNameID("paid message"))
	if err != nil {
		t.Fatal(err)
	}
	if posted.Key != wantPaidMsgKey {
		t.Fatalf("paid offline message key=%s want=%s", posted.Key, wantPaidMsgKey)
	}
	proof, err := dkvsindexer.ParseFeeProof(posted.FeeProof)
	if err != nil || proof.Mode != dkvsindexer.FeeModeAutopay {
		t.Fatalf("paid offline proof=%#v err=%v", proof, err)
	}
	messages, msgRecords, total, err := client.ReadOfflineMessages(ownerPubKey, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(messages) != 1 || len(msgRecords) != 1 ||
		messages[0].MessageID != "msg one" ||
		string(messages[0].EncryptedMessage) != "message ciphertext" {
		t.Fatalf("messages=%#v total=%d", messages, total)
	}
}

func TestSatsNetDKVSClientServiceAuthenticity(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	serviceKey, err := dkvsindexer.ServiceKey("wallet", ServiceAuthenticityPath("desktop app", "1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := encodeDKVSServiceAuthenticity(DKVSServiceAuthenticity{
		Version:      dkvsAppValueVersion,
		ServiceName:  "wallet",
		AppID:        "desktop app",
		Release:      "1.0.0",
		ArtifactHash: "sha256:abc",
		DownloadURL:  "https://example.invalid/wallet",
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewDKVSSignedRecord(dkvsTestWalletFromPriv(t, priv), serviceKey, value, dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	http := &fakeDKVSHTTPClient{
		getResp: map[string][]byte{
			"testnet/v3/dkvs/records": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": record}),
		},
		postResp: map[string][]byte{
			"testnet/v3/dkvs/records": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": record}),
		},
		deleteResp: map[string][]byte{},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)

	if _, err := client.PublishServiceAuthenticity(dkvsTestWalletFromPriv(t, priv), "wallet", "desktop app", "1.0.0", "sha256:abc", "https://example.invalid/wallet", nil, dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100}); err != nil {
		t.Fatal(err)
	}
	var posted swire.DKVSRecord
	if err := json.Unmarshal(http.lastBody, &posted); err != nil {
		t.Fatal(err)
	}
	if posted.Key != serviceKey {
		t.Fatalf("service authenticity key=%s want=%s", posted.Key, serviceKey)
	}
	authenticity, gotRecord, err := client.GetServiceAuthenticity("wallet", "desktop app", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if gotRecord.Key != serviceKey || authenticity.ArtifactHash != "sha256:abc" || http.lastGet.Query["key"] != serviceKey {
		t.Fatalf("authenticity=%#v record=%s query=%v", authenticity, gotRecord.Key, http.lastGet.Query)
	}
	if _, err := client.PublishServiceAuthenticity(dkvsTestWalletFromPriv(t, priv), "wallet", "desktop app", "1.0.0", "", "", nil, dkvsindexer.RecordOptions{}); err != dkvsindexer.ErrInvalidRecord {
		t.Fatalf("empty artifact hash err=%v", err)
	}
}
