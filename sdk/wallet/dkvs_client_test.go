package wallet

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type fakeDKVSHTTPClient struct {
	getResp    map[string][]byte
	postResp   map[string][]byte
	deleteResp map[string][]byte
	lastGet    *URL
	lastPost   *URL
	lastDelete *URL
	lastBody   []byte
}

func (c *fakeDKVSHTTPClient) SendGetRequest(url *URL) ([]byte, error) {
	c.lastGet = url
	resp, ok := c.getResp[url.Path]
	if !ok {
		return nil, fmt.Errorf("missing get response for %s", url.Path)
	}
	return resp, nil
}

func (c *fakeDKVSHTTPClient) SendPostRequest(url *URL, body []byte) ([]byte, error) {
	c.lastPost = url
	c.lastBody = append([]byte{}, body...)
	resp, ok := c.postResp[url.Path]
	if !ok {
		return nil, fmt.Errorf("missing post response for %s", url.Path)
	}
	return resp, nil
}

func (c *fakeDKVSHTTPClient) SendDeleteRequest(url *URL, body []byte) ([]byte, error) {
	c.lastDelete = url
	c.lastBody = append([]byte{}, body...)
	resp, ok := c.deleteResp[url.Path]
	if !ok {
		return nil, fmt.Errorf("missing delete response for %s", url.Path)
	}
	return resp, nil
}

func TestSatsNetDKVSClientRecords(t *testing.T) {
	record := &swire.DKVSRecord{Version: 1, Key: "/tmp/test", Value: []byte("value")}
	http := &fakeDKVSHTTPClient{
		getResp: map[string][]byte{
			"testnet/v3/dkvs/records":        mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": record}),
			"testnet/v3/dkvs/records/prefix": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "start": 2, "total": 1, "data": []*swire.DKVSRecord{record}}),
		},
		postResp: map[string][]byte{
			"testnet/v3/dkvs/records":   mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": record}),
			"testnet/v3/dkvs/tombstone": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": record}),
		},
		deleteResp: map[string][]byte{},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)

	put, err := client.PutRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if put.Key != record.Key || http.lastPost.Path != "testnet/v3/dkvs/records" {
		t.Fatalf("put=%#v path=%s", put, http.lastPost.Path)
	}

	got, err := client.GetRecord(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != record.Key || http.lastGet.Query["key"] != record.Key {
		t.Fatalf("got=%#v query=%v", got, http.lastGet.Query)
	}

	list, total, err := client.ListRecords("/tmp", 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(list) != 1 || http.lastGet.Query["prefix"] != "/tmp" || http.lastGet.Query["start"] != "2" {
		t.Fatalf("list=%#v total=%d query=%v", list, total, http.lastGet.Query)
	}

	if _, err := client.Tombstone(record); err != nil {
		t.Fatal(err)
	}
	if http.lastPost.Path != "testnet/v3/dkvs/tombstone" {
		t.Fatalf("tombstone path=%s", http.lastPost.Path)
	}
}

func TestSatsNetDKVSClientGetVerifiedRecord(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := dkvsindexer.PersonalKey(priv.PubKey().SerializeCompressed(), "profile")
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewDKVSSignedRecord(dkvsTestWalletFromPriv(t, priv), key, []byte("value"), dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	http := &fakeDKVSHTTPClient{
		getResp: map[string][]byte{
			"testnet/v3/dkvs/records": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": record}),
		},
		postResp:   map[string][]byte{},
		deleteResp: map[string][]byte{},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)
	hash := dkvsindexer.RecordHash(record)
	got, err := client.GetVerifiedRecord(key, dkvsindexer.RecordVerificationOptions{ExpectedHash: hash, CheckHash: true, Height: 1, Now: record.IssueTime})
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != key {
		t.Fatalf("got=%#v", got)
	}
	if _, err := client.GetVerifiedRecord(key, dkvsindexer.RecordVerificationOptions{CheckHash: true}); err != dkvsindexer.ErrInvalidRecord {
		t.Fatalf("bad hash err=%v", err)
	}

	gotByHash, err := client.GetRecordByHash(hash)
	if err != nil {
		t.Fatal(err)
	}
	if gotByHash.Key != key || http.lastGet.Query["hash"] != hash.String() {
		t.Fatalf("got by hash=%#v query=%v", gotByHash, http.lastGet.Query)
	}
	verifiedByHash, err := client.GetVerifiedRecordByHash(hash, dkvsindexer.RecordVerificationOptions{Height: 1, Now: record.IssueTime})
	if err != nil {
		t.Fatal(err)
	}
	if verifiedByHash.Key != key {
		t.Fatalf("verified by hash=%#v", verifiedByHash)
	}
	if _, err := client.GetVerifiedRecordByHash(chainhash.Hash{}, dkvsindexer.RecordVerificationOptions{}); err != dkvsindexer.ErrInvalidRecord {
		t.Fatalf("wrong hash query err=%v", err)
	}
}

func TestSatsNetDKVSClientPutSignedRecordWithAutopay(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := dkvsindexer.PersonalKey(priv.PubKey().SerializeCompressed(), "profile")
	if err != nil {
		t.Fatal(err)
	}
	http := &fakeDKVSHTTPClient{
		getResp: map[string][]byte{},
		postResp: map[string][]byte{
			"testnet/v3/dkvs/records":   mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok"}),
			"testnet/v3/dkvs/tombstone": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok"}),
		},
		deleteResp: map[string][]byte{},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)
	autopay := DKVSAutopayOptions{
		AddressParams: &chaincfg.TestNetParams,
		PoolContract:  "tc1ptestautopay",
	}
	if _, err := client.PutSignedRecordWithAutopay(dkvsTestWalletFromPriv(t, priv), key, []byte("value"),
		dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100}, autopay); err != nil {
		t.Fatal(err)
	}
	var posted swire.DKVSRecord
	if err := json.Unmarshal(http.lastBody, &posted); err != nil {
		t.Fatal(err)
	}
	if err := dkvsindexer.VerifySignature(&posted); err != nil {
		t.Fatalf("record signature invalid after fee proof attach: %v", err)
	}
	proof, err := dkvsindexer.ParseFeeProof(posted.FeeProof)
	if err != nil {
		t.Fatal(err)
	}
	if proof.Mode != dkvsindexer.FeeModeAutopay ||
		proof.PoolContract != autopay.PoolContract {
		t.Fatalf("bad autopay proof=%+v record=%+v", proof, posted)
	}

	if _, err := client.TombstonePersonalRecordWithAutopay(dkvsTestWalletFromPriv(t, priv), "profile",
		dkvsindexer.RecordOptions{Seq: 2, TTL: 60_000, ExpiryHeight: 100}, autopay); err != nil {
		t.Fatal(err)
	}
	var tombstone swire.DKVSRecord
	if err := json.Unmarshal(http.lastBody, &tombstone); err != nil {
		t.Fatal(err)
	}
	if !dkvsindexer.IsTombstone(tombstone.Flags) || len(tombstone.Value) != 0 {
		t.Fatalf("bad tombstone flags=%d value=%x", tombstone.Flags, tombstone.Value)
	}
	if err := dkvsindexer.VerifySignature(&tombstone); err != nil {
		t.Fatalf("tombstone signature invalid: %v", err)
	}
}

func TestSatsNetDKVSClientListVerifiedRecords(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	prefix := "/personal/" + dkvsindexer.AccountID(priv.PubKey().SerializeCompressed())
	record, err := NewDKVSSignedRecord(dkvsTestWalletFromPriv(t, priv), prefix+"/profile", []byte("value"), dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	http := &fakeDKVSHTTPClient{
		getResp: map[string][]byte{
			"testnet/v3/dkvs/records/prefix": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "start": 0, "total": 1, "data": []*swire.DKVSRecord{record}}),
		},
		postResp:   map[string][]byte{},
		deleteResp: map[string][]byte{},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)
	records, total, err := client.ListVerifiedRecords(prefix, 0, 10, dkvsindexer.RecordVerificationOptions{Height: 1, Now: record.IssueTime})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(records) != 1 || records[0].Key != record.Key || http.lastGet.Query["prefix"] != prefix {
		t.Fatalf("records=%#v total=%d query=%v", records, total, http.lastGet.Query)
	}

	other, err := NewDKVSSignedRecord(dkvsTestWalletFromPriv(t, priv), "/tmp/random", []byte("tmp"), dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	http.getResp["testnet/v3/dkvs/records/prefix"] = mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "start": 0, "total": 1, "data": []*swire.DKVSRecord{other}})
	if _, _, err := client.ListVerifiedRecords(prefix, 0, 10, dkvsindexer.RecordVerificationOptions{}); err != dkvsindexer.ErrInvalidKey {
		t.Fatalf("out-of-prefix err=%v", err)
	}
}

func TestSatsNetDKVSClientSnapshotAndSubscriptions(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	mailboxID := dkvsindexer.AccountID(priv.PubKey().SerializeCompressed())
	mailRecord, err := NewDKVSSignedRecord(dkvsTestWalletFromPriv(t, priv), "/mail/"+mailboxID+"/msg/msg-1", []byte("message"), dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	cp := &dkvsindexer.Checkpoint{Height: 10, ActiveRecordCount: 1, NamespaceRoots: map[string]string{"tmp": "root"}, ActiveRecordRoot: "root"}
	snapshot := &dkvsindexer.Snapshot{Checkpoint: cp, CreatedAt: 20}
	sub := dkvsindexer.Subscription{Type: dkvsindexer.SubscriptionMailbox, Target: mailboxID}
	usage := &dkvsindexer.Usage{Prefix: "/tmp", ActiveRecords: 2, ActiveTotalSize: 200}
	http := &fakeDKVSHTTPClient{
		getResp: map[string][]byte{
			"testnet/v3/dkvs/checkpoint":    mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": cp}),
			"testnet/v3/dkvs/snapshot":      mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": snapshot}),
			"testnet/v3/dkvs/usage":         mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": usage}),
			"testnet/v3/dkvs/subscriptions": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "total": 1, "subscriptions": []dkvsindexer.Subscription{sub}}),
		},
		postResp: map[string][]byte{
			"testnet/v3/dkvs/snapshot":      mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "applied": 3}),
			"testnet/v3/dkvs/prune":         mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "pruned": 2}),
			"testnet/v3/dkvs/subscriptions": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "total": 1, "data": []*swire.DKVSRecord{mailRecord}}),
		},
		deleteResp: map[string][]byte{
			"testnet/v3/dkvs/subscriptions": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "total": 0, "subscriptions": []dkvsindexer.Subscription{}}),
		},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)

	gotCP, err := client.GetCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	if gotCP.Height != cp.Height {
		t.Fatalf("checkpoint=%#v", gotCP)
	}
	gotSnapshot, err := client.GetSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if gotSnapshot.CreatedAt != snapshot.CreatedAt {
		t.Fatalf("snapshot=%#v", gotSnapshot)
	}
	gotUsage, err := client.GetUsage("/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if gotUsage.ActiveRecords != usage.ActiveRecords || gotUsage.ActiveTotalSize != usage.ActiveTotalSize ||
		http.lastGet.Query["prefix"] != "/tmp" {
		t.Fatalf("usage=%#v query=%v", gotUsage, http.lastGet.Query)
	}
	applied, err := client.ApplySnapshot(snapshot)
	if err != nil || applied != 3 {
		t.Fatalf("applied=%d err=%v", applied, err)
	}
	pruned, err := client.PruneExpired()
	if err != nil || pruned != 2 {
		t.Fatalf("pruned=%d err=%v", pruned, err)
	}
	if _, _, err := client.Subscribe(sub); err != nil {
		t.Fatal(err)
	}
	records, total, err := client.SubscribeVerified(sub, dkvsindexer.RecordVerificationOptions{Height: 1, Now: mailRecord.IssueTime})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(records) != 1 || records[0].Key != mailRecord.Key {
		t.Fatalf("subscribe verified records=%#v total=%d", records, total)
	}
	subs, err := client.ListSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0] != sub {
		t.Fatalf("subs=%#v", subs)
	}
	subs, err = client.Unsubscribe(sub)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 0 || http.lastDelete.Path != "testnet/v3/dkvs/subscriptions" {
		t.Fatalf("subs=%#v path=%s", subs, http.lastDelete.Path)
	}
}

func TestSatsNetDKVSClientVerifiedSnapshot(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := dkvsindexer.PersonalKey(priv.PubKey().SerializeCompressed(), "profile")
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewDKVSSignedRecord(dkvsTestWalletFromPriv(t, priv), key, []byte("value"), dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := dkvsindexer.CheckpointFromRecords([]*swire.DKVSRecord{record}, 10)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &dkvsindexer.Snapshot{Checkpoint: checkpoint, Records: []*swire.DKVSRecord{record}, CreatedAt: 20}
	http := &fakeDKVSHTTPClient{
		getResp: map[string][]byte{
			"testnet/v3/dkvs/snapshot": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": snapshot}),
		},
		postResp:   map[string][]byte{},
		deleteResp: map[string][]byte{},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)
	verified, err := client.GetVerifiedSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if verified.Checkpoint.ActiveRecordRoot != checkpoint.ActiveRecordRoot {
		t.Fatalf("verified snapshot=%#v", verified.Checkpoint)
	}
	snapshot.Checkpoint.ActiveRecordRoot = "bad"
	http.getResp["testnet/v3/dkvs/snapshot"] = mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": snapshot})
	if _, err := client.GetVerifiedSnapshot(); err != dkvsindexer.ErrInvalidSnapshot {
		t.Fatalf("bad verified snapshot err=%v", err)
	}
}

func TestSatsNetDKVSClientSignedPersonalAndSubscriptions(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	pubKey := priv.PubKey().SerializeCompressed()
	personalKey, err := dkvsindexer.PersonalKey(pubKey, "profile")
	if err != nil {
		t.Fatal(err)
	}
	record := &swire.DKVSRecord{Version: 1, Key: personalKey, Value: []byte("profile")}
	http := &fakeDKVSHTTPClient{
		getResp: map[string][]byte{
			"testnet/v3/dkvs/records": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": record}),
		},
		postResp: map[string][]byte{
			"testnet/v3/dkvs/records":       mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": record}),
			"testnet/v3/dkvs/tombstone":     mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": record}),
			"testnet/v3/dkvs/subscriptions": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "total": 1, "data": []*swire.DKVSRecord{record}}),
		},
		deleteResp: map[string][]byte{
			"testnet/v3/dkvs/subscriptions": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "total": 0, "subscriptions": []dkvsindexer.Subscription{}}),
		},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)

	if _, err := client.PutSignedRecord(dkvsTestWalletFromPriv(t, priv), personalKey, []byte("profile"), dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100}); err != nil {
		t.Fatal(err)
	}
	var posted swire.DKVSRecord
	if err := json.Unmarshal(http.lastBody, &posted); err != nil {
		t.Fatal(err)
	}
	if posted.Key != personalKey || string(posted.Value) != "profile" || len(posted.Signature) == 0 {
		t.Fatalf("posted=%#v", posted)
	}
	if err := dkvsindexer.VerifySignature(&posted); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PutPersonalRecord(dkvsTestWalletFromPriv(t, priv), "profile", []byte("profile2"), dkvsindexer.RecordOptions{Seq: 2, TTL: 60_000, ExpiryHeight: 100}); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(http.lastBody, &posted); err != nil {
		t.Fatal(err)
	}
	if posted.Key != personalKey || string(posted.Value) != "profile2" {
		t.Fatalf("personal posted=%#v", posted)
	}
	got, err := client.GetPersonalRecord(pubKey, "profile")
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != personalKey || http.lastGet.Query["key"] != personalKey {
		t.Fatalf("got=%#v query=%v", got, http.lastGet.Query)
	}
	if _, err := client.TombstonePersonalRecord(dkvsTestWalletFromPriv(t, priv), "profile", dkvsindexer.RecordOptions{Seq: 3, TTL: 60_000, ExpiryHeight: 100}); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(http.lastBody, &posted); err != nil {
		t.Fatal(err)
	}
	if posted.Key != personalKey || posted.Flags&dkvsindexer.FlagTombstone == 0 {
		t.Fatalf("tombstone posted=%#v", posted)
	}
	existingRenewal, err := NewDKVSSignedRecord(dkvsTestWalletFromPriv(t, priv), personalKey, []byte("profile2"), dkvsindexer.RecordOptions{Seq: 4, TTL: 60_000, ExpiryHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RenewRecord(dkvsTestWalletFromPriv(t, priv), existingRenewal, dkvsindexer.RecordOptions{TTL: 120_000, ExpiryHeight: 200}); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(http.lastBody, &posted); err != nil {
		t.Fatal(err)
	}
	if posted.Key != personalKey || posted.Seq != existingRenewal.Seq ||
		string(posted.Value) != "profile2" || posted.ExpiryHeight != 200 {
		t.Fatalf("renewal posted=%#v", posted)
	}
	if err := dkvsindexer.VerifySignature(&posted); err != nil {
		t.Fatal(err)
	}
	http.getResp["testnet/v3/dkvs/records"] = mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": existingRenewal})
	if _, err := client.RenewPersonalRecord(dkvsTestWalletFromPriv(t, priv), "profile", dkvsindexer.RecordOptions{TTL: 180_000, ExpiryHeight: 300}); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(http.lastBody, &posted); err != nil {
		t.Fatal(err)
	}
	if posted.Key != personalKey || posted.Seq != existingRenewal.Seq ||
		posted.TTL != 180_000 || posted.ExpiryHeight != 300 {
		t.Fatalf("personal renewal posted=%#v", posted)
	}
	if _, _, err := client.SubscribeKey(personalKey); err != nil {
		t.Fatal(err)
	}
	var sub dkvsindexer.Subscription
	if err := json.Unmarshal(http.lastBody, &sub); err != nil {
		t.Fatal(err)
	}
	if sub.Type != dkvsindexer.SubscriptionKey || sub.Target != personalKey {
		t.Fatalf("key sub=%#v", sub)
	}
	prefix := "/personal/" + dkvsindexer.AccountID(pubKey)
	if _, _, err := client.SubscribePrefix(prefix); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(http.lastBody, &sub); err != nil {
		t.Fatal(err)
	}
	if sub.Type != dkvsindexer.SubscriptionPrefix || sub.Target != prefix {
		t.Fatalf("prefix sub=%#v", sub)
	}
	if _, err := client.UnsubscribeKey(personalKey); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UnsubscribePrefix(prefix); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PutPersonalRecord(nil, "profile", nil, dkvsindexer.RecordOptions{}); err != dkvsindexer.ErrInvalidSignature {
		t.Fatalf("nil personal signer err=%v", err)
	}
	if _, _, err := client.SubscribeKey("/bad/key"); err == nil {
		t.Fatalf("expected invalid key subscription error")
	}
}

func TestSatsNetDKVSClientBlob(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manifestRecord, chunkRecords, err := BuildDKVSSignedBlobRecords(dkvsTestWalletFromPriv(t, priv), "object", [][]byte{[]byte("hello"), []byte(" world")}, nil, dkvsindexer.RecordOptions{
		Seq:          1,
		TTL:          60_000,
		ExpiryHeight: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	http := &fakeDKVSHTTPClient{
		getResp: map[string][]byte{
			"testnet/v3/dkvs/records":        mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": manifestRecord}),
			"testnet/v3/dkvs/records/prefix": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "total": len(chunkRecords), "data": chunkRecords}),
		},
		postResp: map[string][]byte{
			"testnet/v3/dkvs/records": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": manifestRecord}),
		},
		deleteResp: map[string][]byte{},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)
	if err := client.PutBlobRecords(manifestRecord, chunkRecords); err != nil {
		t.Fatal(err)
	}
	if http.lastPost.Path != "testnet/v3/dkvs/records" {
		t.Fatalf("put blob path=%s", http.lastPost.Path)
	}
	manifest, content, err := client.GetBlob("object", dkvsindexer.BlobPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ChunkCount != 2 || string(content) != "hello world" {
		t.Fatalf("manifest=%#v content=%q", manifest, string(content))
	}
	if http.lastGet.Query["prefix"] != "/blob/object/chunk/" {
		t.Fatalf("chunk query=%v", http.lastGet.Query)
	}

	objectID, putManifest, putChunks, err := client.PutBlob(dkvsTestWalletFromPriv(t, priv), []byte("hello world"), nil, dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("hello world"))
	wantObjectID := fmt.Sprintf("%x", sum[:])
	if objectID != wantObjectID || putManifest.Key != "/blob/"+wantObjectID+"/manifest" || len(putChunks) != 1 {
		t.Fatalf("object=%s manifest=%s chunks=%d", objectID, putManifest.Key, len(putChunks))
	}
	if _, _, err := client.PutChunkedBlob(dkvsTestWalletFromPriv(t, priv), "object-2", [][]byte{[]byte("a"), []byte("b")}, nil, dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100}); err != nil {
		t.Fatal(err)
	}
	if _, content, err := client.GetChunkedBlob("object", dkvsindexer.BlobPolicy{}); err != nil || string(content) != "hello world" {
		t.Fatalf("get chunked content=%q err=%v", content, err)
	}
}

func TestSatsNetDKVSClientMailbox(t *testing.T) {
	ownerPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	senderPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	mailboxID := dkvsindexer.AccountID(ownerPriv.PubKey().SerializeCompressed())
	msgKey, err := dkvsindexer.MailMsgKey(mailboxID, "msg-1")
	if err != nil {
		t.Fatal(err)
	}
	msgRecord, err := NewDKVSSignedRecord(dkvsTestWalletFromPriv(t, senderPriv), msgKey, []byte("hello"), dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	shareKey, err := dkvsindexer.MailShareKey(mailboxID, "pkg", "share-1")
	if err != nil {
		t.Fatal(err)
	}
	shareRecord, err := NewDKVSSignedRecord(dkvsTestWalletFromPriv(t, ownerPriv), shareKey, []byte("share"), dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	tombstone, err := NewDKVSSignedTombstone(dkvsTestWalletFromPriv(t, ownerPriv), msgKey, dkvsindexer.RecordOptions{Seq: 2, TTL: 60_000, ExpiryHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	sub := dkvsindexer.Subscription{Type: dkvsindexer.SubscriptionMailbox, Target: "/mail/" + mailboxID}
	http := &fakeDKVSHTTPClient{
		getResp: map[string][]byte{
			"testnet/v3/dkvs/records/prefix": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "total": 1, "data": []*swire.DKVSRecord{msgRecord}}),
		},
		postResp: map[string][]byte{
			"testnet/v3/dkvs/records":       mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": msgRecord}),
			"testnet/v3/dkvs/tombstone":     mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": tombstone}),
			"testnet/v3/dkvs/subscriptions": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "total": 1, "data": []*swire.DKVSRecord{msgRecord}}),
		},
		deleteResp: map[string][]byte{
			"testnet/v3/dkvs/subscriptions": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "total": 0, "subscriptions": []dkvsindexer.Subscription{}}),
		},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)

	createdMailboxID, err := client.CreateMailbox(ownerPriv.PubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	if createdMailboxID != mailboxID {
		t.Fatalf("mailbox=%s want=%s", createdMailboxID, mailboxID)
	}
	if _, err := client.SendMailboxMessage(msgRecord); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SendSignedMailboxMessage(dkvsTestWalletFromPriv(t, senderPriv), mailboxID, "msg-2", []byte("encrypted"), dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100}); err != nil {
		t.Fatal(err)
	}
	var sent swire.DKVSRecord
	if err := json.Unmarshal(http.lastBody, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Key != "/mail/"+mailboxID+"/msg/msg-2" {
		t.Fatalf("signed message key=%s", sent.Key)
	}
	if _, err := client.PutMailboxShare(shareRecord); err != nil {
		t.Fatal(err)
	}
	records, total, err := client.ReadMailboxMessages(mailboxID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(records) != 1 || http.lastGet.Query["prefix"] != "/mail/"+mailboxID+"/msg" {
		t.Fatalf("messages total=%d records=%d query=%v", total, len(records), http.lastGet.Query)
	}
	if _, _, err := client.ReadMailboxShares(mailboxID, 0, 10); err != nil {
		t.Fatal(err)
	}
	if http.lastGet.Query["prefix"] != "/mail/"+mailboxID+"/share" {
		t.Fatalf("share query=%v", http.lastGet.Query)
	}
	if _, err := client.DeleteMailboxRecord(tombstone); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DeleteMessage(dkvsTestWalletFromPriv(t, ownerPriv), mailboxID, "msg-1", dkvsindexer.RecordOptions{Seq: 3, TTL: 60_000, ExpiryHeight: 100}); err != nil {
		t.Fatal(err)
	}
	var deleted swire.DKVSRecord
	if err := json.Unmarshal(http.lastBody, &deleted); err != nil {
		t.Fatal(err)
	}
	if deleted.Key != msgKey || deleted.Flags&dkvsindexer.FlagTombstone == 0 {
		t.Fatalf("delete message key=%s flags=%d", deleted.Key, deleted.Flags)
	}
	if _, _, err := client.SubscribeMailbox(mailboxID); err != nil {
		t.Fatal(err)
	}
	var gotSub dkvsindexer.Subscription
	if err := json.Unmarshal(http.lastBody, &gotSub); err != nil {
		t.Fatal(err)
	}
	if gotSub != sub {
		t.Fatalf("sub=%#v want=%#v", gotSub, sub)
	}
	if _, err := client.UnsubscribeMailbox(mailboxID); err != nil {
		t.Fatal(err)
	}
	if http.lastDelete.Path != "testnet/v3/dkvs/subscriptions" {
		t.Fatalf("unsubscribe path=%s", http.lastDelete.Path)
	}
	tmp := &swire.DKVSRecord{Version: 1, Key: "/tmp/not-mail"}
	if _, err := client.SendMailboxMessage(tmp); err != dkvsindexer.ErrInvalidKey {
		t.Fatalf("non-mail send err=%v", err)
	}
	if _, err := client.DeleteMailboxRecord(msgRecord); err != dkvsindexer.ErrInvalidRecord {
		t.Fatalf("non-tombstone delete err=%v", err)
	}
}

func TestSatsNetDKVSClientNameAndService(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	nameKey, err := dkvsindexer.NameKey("Alice Name")
	if err != nil {
		t.Fatal(err)
	}
	nameRecord, err := NewDKVSSignedRecord(dkvsTestWalletFromPriv(t, priv), nameKey, []byte("profile"), dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	serviceKey, err := dkvsindexer.ServiceKey("wallet", "config")
	if err != nil {
		t.Fatal(err)
	}
	serviceRecord, err := NewDKVSSignedRecord(dkvsTestWalletFromPriv(t, priv), serviceKey, []byte("config"), dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	serviceSub := dkvsindexer.Subscription{Type: dkvsindexer.SubscriptionService, Target: "/svc/wallet"}
	http := &fakeDKVSHTTPClient{
		getResp: map[string][]byte{
			"testnet/v3/dkvs/records":        mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": nameRecord}),
			"testnet/v3/dkvs/records/prefix": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "total": 1, "data": []*swire.DKVSRecord{serviceRecord}}),
		},
		postResp: map[string][]byte{
			"testnet/v3/dkvs/records":       mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "data": serviceRecord}),
			"testnet/v3/dkvs/subscriptions": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "total": 1, "data": []*swire.DKVSRecord{serviceRecord}}),
		},
		deleteResp: map[string][]byte{
			"testnet/v3/dkvs/subscriptions": mustJSON(t, map[string]interface{}{"code": 0, "msg": "ok", "total": 0, "subscriptions": []dkvsindexer.Subscription{}}),
		},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)

	if _, err := client.PutNameRecord(nameRecord); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PutSignedNameRecord(dkvsTestWalletFromPriv(t, priv), "Alice Name", []byte("profile2"), dkvsindexer.RecordOptions{Seq: 2, TTL: 60_000, ExpiryHeight: 100}); err != nil {
		t.Fatal(err)
	}
	var signedName swire.DKVSRecord
	if err := json.Unmarshal(http.lastBody, &signedName); err != nil {
		t.Fatal(err)
	}
	if signedName.Key != nameKey {
		t.Fatalf("signed name key=%s want=%s", signedName.Key, nameKey)
	}
	if err := dkvsindexer.VerifySignature(&signedName); err != nil {
		t.Fatal(err)
	}
	gotName, err := client.GetNameRecord("Alice Name")
	if err != nil {
		t.Fatal(err)
	}
	if gotName.Key != nameKey || http.lastGet.Query["key"] != nameKey {
		t.Fatalf("name=%#v query=%v", gotName, http.lastGet.Query)
	}
	resolved, err := client.ResolveNameRecord("Alice Name")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.CanonicalName != "Alice Name" ||
		resolved.NameID != dkvsindexer.NormalizeNameID("Alice Name") ||
		resolved.Record.Key != nameKey ||
		http.lastGet.Query["key"] != nameKey {
		t.Fatalf("resolved=%#v query=%v", resolved, http.lastGet.Query)
	}
	if _, err := client.PutServiceRecord(serviceRecord); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PutSignedServiceRecord(dkvsTestWalletFromPriv(t, priv), "wallet", "config", []byte("config2"), dkvsindexer.RecordOptions{Seq: 2, TTL: 60_000, ExpiryHeight: 100}); err != nil {
		t.Fatal(err)
	}
	var signedService swire.DKVSRecord
	if err := json.Unmarshal(http.lastBody, &signedService); err != nil {
		t.Fatal(err)
	}
	if signedService.Key != serviceKey {
		t.Fatalf("signed service key=%s want=%s", signedService.Key, serviceKey)
	}
	if err := dkvsindexer.VerifySignature(&signedService); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetServiceRecord("wallet", "config"); err != nil {
		t.Fatal(err)
	}
	if http.lastGet.Query["key"] != serviceKey {
		t.Fatalf("service query=%v", http.lastGet.Query)
	}
	records, total, err := client.ListServiceRecords("wallet", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(records) != 1 || http.lastGet.Query["prefix"] != "/svc/wallet" {
		t.Fatalf("service list total=%d records=%d query=%v", total, len(records), http.lastGet.Query)
	}
	if _, _, err := client.SubscribeService("wallet"); err != nil {
		t.Fatal(err)
	}
	var gotSub dkvsindexer.Subscription
	if err := json.Unmarshal(http.lastBody, &gotSub); err != nil {
		t.Fatal(err)
	}
	if gotSub != serviceSub {
		t.Fatalf("sub=%#v want=%#v", gotSub, serviceSub)
	}
	if _, err := client.UnsubscribeService("wallet"); err != nil {
		t.Fatal(err)
	}
	if http.lastDelete.Path != "testnet/v3/dkvs/subscriptions" {
		t.Fatalf("unsubscribe path=%s", http.lastDelete.Path)
	}
	tmp := &swire.DKVSRecord{Version: 1, Key: "/tmp/not-name"}
	if _, err := client.PutNameRecord(tmp); err != dkvsindexer.ErrInvalidKey {
		t.Fatalf("non-name err=%v", err)
	}
	if _, err := client.PutServiceRecord(tmp); err != dkvsindexer.ErrInvalidKey {
		t.Fatalf("non-service err=%v", err)
	}
}

func TestSatsNetDKVSClientResponseError(t *testing.T) {
	http := &fakeDKVSHTTPClient{
		getResp: map[string][]byte{
			"testnet/v3/dkvs/records": mustJSON(t, map[string]interface{}{"code": 1, "msg": "missing"}),
		},
		postResp:   map[string][]byte{},
		deleteResp: map[string][]byte{},
	}
	client := NewSatsNetDKVSClient("http", "127.0.0.1:8334", "testnet", http)
	if _, err := client.GetRecord("/tmp/missing"); err == nil {
		t.Fatalf("expected response error")
	}
}

func mustJSON(t *testing.T, value interface{}) []byte {
	t.Helper()
	out, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
