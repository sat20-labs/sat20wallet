package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type satoshinetDKVSTestTransport struct {
	indexer          *dkvsindexer.Indexer
	height           uint64
	snapshotCalls    int
	deltaCalls       int
	lastDeltaRecords int
}

func newSatoshiNetDKVSTestTransport() *satoshinetDKVSTestTransport {
	db := newMemoryKVDB()
	transport := &satoshinetDKVSTestTransport{height: 1}
	indexer := dkvsindexer.New(db, dkvsindexer.Config{
		EndpointID: "test-core-node", AllowFreeLocal: true,
		FreeLocalCache: dkvsindexer.FreeLocalCachePolicy{
			Enabled: true, MaxTTL: 1000, MaxRecordsPerSigner: 100, MaxBytesPerSigner: 1 << 20,
			MaxTotalRecords: 10000, MaxTotalBytes: 64 << 20,
		},
		CurrentHeight: func() uint64 { return transport.height },
	})
	transport.indexer = indexer
	return transport
}
func (t *satoshinetDKVSTestTransport) DKVSClientConfig() (*dkvsindexer.ClientConfig, error) {
	config := t.indexer.ClientConfig()
	return &config, nil
}
func (t *satoshinetDKVSTestTransport) SendGetRequest(url *URL) ([]byte, error) {
	if strings.HasSuffix(url.Path, "/btc/block/bestblockheight") {
		return rgb11DKVSResponse(0, "ok", int64(t.height), "", 0)
	}
	return nil, fmt.Errorf("unexpected generic GET %s", url.Path)
}
func (t *satoshinetDKVSTestTransport) SendPostRequest(url *URL, _ []byte) ([]byte, error) {
	return nil, fmt.Errorf("unexpected generic POST %s", url.Path)
}
func satoshinetTestResponse(data interface{}, err error) ([]byte, error) {
	if err == nil {
		return rgb11DKVSResponse(0, "ok", data, "", 0)
	}
	return rgb11DKVSResponse(-1, err.Error(), data, string(dkvsindexer.ErrorCodeOf(err)), 0)
}
func (t *satoshinetDKVSTestTransport) SendDKVSGet(path string, query map[string]string) ([]byte, error) {
	switch path {
	case "/v3/dkvs/config":
		config := t.indexer.ClientConfig()
		return satoshinetTestResponse(&config, nil)
	case "/v3/dkvs/record":
		record, err := t.indexer.Get(query["key"])
		if err != nil {
			return satoshinetTestResponse(nil, err)
		}
		return json.Marshal(map[string]interface{}{"code": 0, "msg": "ok", "data": record, "etag": dkvsindexer.RecordHash(record).String()})
	case "/v3/dkvs/key-state":
		state, err := t.indexer.GetKeyState(query["key"])
		if err != nil {
			return satoshinetTestResponse(nil, err)
		}
		return satoshinetTestResponse(&state, nil)
	default:
		return nil, fmt.Errorf("unexpected DKVS GET %s", path)
	}
}

// Dispatch only: the real Indexer performs generation filtering, paging,
// current-state roots and notification waits. Binding admission has separate
// production HTTP E2Es; this unit transport supplies a fixture write context.
func (t *satoshinetDKVSTestTransport) sendActive(ctx context.Context, path string, body []byte) ([]byte, error) {
	if path == "/v3/dkvs/active/watch" {
		var request dkvsindexer.ActiveWatchRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		result, err := t.indexer.WatchActive(ctx, request)
		// Match a canceled HTTP request: the caller receives the context
		// error, rather than a JSON application error from the handler.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return satoshinetTestResponse(result, err)
	}
	var request dkvsindexer.ActiveSyncRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, err
	}
	if request.Full {
		t.snapshotCalls++
	} else {
		t.deltaCalls++
	}
	result, err := t.indexer.ActiveSyncPage(ctx, request)
	if result != nil && !request.Full {
		t.lastDeltaRecords = len(result.Records)
	}
	return satoshinetTestResponse(result, err)
}
func (t *satoshinetDKVSTestTransport) SendDKVSPost(path string, body []byte) ([]byte, error) {
	switch path {
	case "/v3/dkvs/active/sync", "/v3/dkvs/active/watch":
		return t.sendActive(context.Background(), path, body)
	case "/v3/dkvs/records/batch-cas":
		var request DKVSBatchCASRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		mutations := make([]dkvsindexer.CASMutation, 0, len(request.Mutations))
		for _, item := range request.Mutations {
			condition := dkvsindexer.WritePrecondition{ExpectAbsent: item.ExpectAbsent}
			if item.ExpectedETag != "" {
				hash, err := chainhash.NewHashFromStr(item.ExpectedETag)
				if err != nil {
					return satoshinetTestResponse(nil, dkvsindexer.ErrInvalidRecord)
				}
				condition.ExpectedHash = hash
			}
			mutations = append(mutations, dkvsindexer.CASMutation{Record: item.Record, Precondition: condition})
		}
		if err := t.indexer.ValidateBatchEndpointID(mutations, request.EndpointID); err != nil {
			return satoshinetTestResponse(nil, err)
		}
		result, err := t.indexer.PutLocalBatchCASResultWithOptions(mutations, dkvsindexer.BatchCASOptions{EndpointID: request.EndpointID, RequestID: request.RequestID})
		return satoshinetTestResponse(result, err)
	case "/v3/dkvs/prefixes/read":
		var request struct {
			Prefix string `json:"prefix"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		result, err := t.indexer.ReadPrefix(request.Prefix)
		return satoshinetTestResponse(result, err)
	default:
		return nil, fmt.Errorf("unexpected DKVS POST %s", path)
	}
}
func (t *satoshinetDKVSTestTransport) SendGetRequestContext(ctx context.Context, url *URL) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return t.SendGetRequest(url)
}
func (t *satoshinetDKVSTestTransport) SendDKVSGetContext(ctx context.Context, path string, query map[string]string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return t.SendDKVSGet(path, query)
}
func (t *satoshinetDKVSTestTransport) SendDKVSPostContext(ctx context.Context, path string, body []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if path == "/v3/dkvs/active/sync" || path == "/v3/dkvs/active/watch" {
		return t.sendActive(ctx, path, body)
	}
	return t.SendDKVSPost(path, body)
}
func (t *satoshinetDKVSTestTransport) SendPostRequestContext(ctx context.Context, url *URL, body []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return t.SendPostRequest(url, body)
}

func TestDKVSFinalWalletToSatoshiNetIntegration(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manager := newRGB11MultiDeviceManager(t, priv, 901)
	transport := newSatoshiNetDKVSTestTransport()
	configureRGB11DKVSTestManager(manager, transport)
	client, err := manager.ensureDKVSManager().primaryClient()
	if err != nil {
		t.Fatal(err)
	}
	key := accountTestKey(t, manager, "wallet/integration")
	prefix, _, err := dkvsManagedPathForKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SubscribeDKVSPrefix(prefix); err != nil {
		t.Fatal(err)
	}
	if err := manager.dkvs.forceCurrentPrefixes(client, []string{prefix}); err != nil {
		t.Fatal(err)
	}
	record := freeLocalRecord(t, manager, key, 1, "integration")
	if _, err := client.PutRecord(record); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	store := &dkvsStore{manager: manager.dkvs, client: client}
	value, err := store.Get(key)
	if err != nil || value.Seq != 1 || string(value.Value) != "integration" {
		t.Fatalf("wallet replica value=%+v err=%v", value, err)
	}
	state, err := transport.indexer.GetKeyState(key)
	if err != nil || state.Status != dkvsindexer.KeyStateActive || state.Seq != 1 {
		t.Fatalf("server key state=%+v err=%v", state, err)
	}
}

func TestDKVSFreeLocalRenewalWalletToSatoshiNetIntegration(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manager := newRGB11MultiDeviceManager(t, priv, 901)
	transport := newSatoshiNetDKVSTestTransport()
	transport.height = 100
	configureRGB11DKVSTestManager(manager, transport)
	client, err := manager.ensureDKVSManager().primaryClient()
	if err != nil {
		t.Fatal(err)
	}
	key := accountTestKey(t, manager, "wallet/renew-integration")
	original, err := client.PutSignedRecordFreeLocal(manager.wallet, key, []byte("lease"), dkvsindexer.RecordOptions{IssueHeight: 100, TTL: 20})
	if err != nil {
		t.Fatal(err)
	}
	oldExpiry := dkvsindexer.RecordExpiryHeight(original)
	transport.height = 110
	renewed, err := client.RenewPersonalRecord(manager.wallet, "wallet/renew-integration", dkvsindexer.RecordOptions{IssueHeight: 110, TTL: 40})
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Seq != original.Seq+1 || dkvsindexer.RecordExpiryHeight(renewed) <= oldExpiry {
		t.Fatalf("renewed record=%+v old_expiry=%d", renewed, oldExpiry)
	}
	transport.height = oldExpiry + 1
	actual, err := client.GetRecordDirect(key)
	if err != nil || dkvsindexer.RecordHash(actual) != dkvsindexer.RecordHash(renewed) {
		t.Fatalf("renewed did not survive old expiry: actual=%+v err=%v", actual, err)
	}
	transport.height = dkvsindexer.RecordExpiryHeight(renewed)
	if _, err := client.GetRecordDirect(key); !errors.Is(err, dkvsindexer.ErrRecordNotFound) {
		t.Fatalf("renewed active at new expiry: %v", err)
	}
}

func TestDKVSIncrementalSyncLateAcceptedRecordEndToEnd(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manager := newRGB11MultiDeviceManager(t, priv, 901)
	transport := newSatoshiNetDKVSTestTransport()
	transport.height = 100
	configureRGB11DKVSTestManager(manager, transport)
	client, err := manager.ensureDKVSManager().primaryClient()
	if err != nil {
		t.Fatal(err)
	}
	firstKey := accountTestKey(t, manager, "wallet/first")
	lateKey := accountTestKey(t, manager, "wallet/late")
	prefix, _, err := dkvsManagedPathForKey(firstKey)
	if err != nil {
		t.Fatal(err)
	}
	makeRecord := func(key, value string) *dkvsindexer.Record {
		record, signErr := newDKVSAccountSignedRecordWithFreeLocal(manager.wallet, key, []byte(value), dkvsindexer.RecordOptions{Seq: 1, IssueHeight: 100, TTL: 1000})
		if signErr != nil {
			t.Fatal(signErr)
		}
		return record
	}
	late := makeRecord(lateKey, "late")
	if _, err := client.PutRecord(makeRecord(firstKey, "first")); err != nil {
		t.Fatal(err)
	}
	if err := manager.SubscribeDKVSPrefix(prefix); err != nil {
		t.Fatal(err)
	}
	if err := manager.dkvs.forceCurrentPrefixes(client, []string{prefix}); err != nil {
		t.Fatal(err)
	}
	initialSnapshots := transport.snapshotCalls
	transport.height = 105
	// This fixture models replication of an old, still valid record. The
	// wallet RPC height window is separately enforced by the real HTTP tests.
	if _, err := transport.indexer.PutLocalCAS(late, dkvsindexer.WritePrecondition{ExpectAbsent: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	if transport.snapshotCalls != initialSnapshots || transport.deltaCalls == 0 || transport.lastDeltaRecords != 1 {
		t.Fatalf("snapshots=%d initial=%d deltas=%d records=%d", transport.snapshotCalls, initialSnapshots, transport.deltaCalls, transport.lastDeltaRecords)
	}
	store := &dkvsStore{manager: manager.dkvs, client: client}
	for key, want := range map[string]string{firstKey: "first", lateKey: "late"} {
		value, err := store.Get(key)
		if err != nil || string(value.Value) != want {
			t.Fatalf("key=%s value=%+v err=%v", key, value, err)
		}
	}
}

func TestDKVSFinalSatoshiNetRejectsMailboxDeleteSignedBySender(t *testing.T) {
	transport := newSatoshiNetDKVSTestTransport()
	recipient, _ := btcec.NewPrivateKey()
	sender, _ := btcec.NewPrivateKey()
	mailboxID := dkvsindexer.AccountID(recipient.PubKey().SerializeCompressed())
	senderID := dkvsindexer.AccountID(sender.PubKey().SerializeCompressed())
	key, err := dkvsindexer.MailMsgKey(mailboxID, senderID, "0000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	message := &swire.DKVSRecord{Version: dkvsindexer.Version, Key: key, Value: []byte("ciphertext"), Seq: 1, IssueHeight: 1, TTL: 100}
	message.FeeProof, err = dkvsindexer.EncodeFeeProof(&dkvsindexer.FeeProof{Mode: dkvsindexer.FeeModeFreeLocal})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := dkvsindexer.ParseKey(message.Key)
	if err != nil || parsed.Namespace != "mail" || len(parsed.Segments) != 4 {
		t.Fatalf("mailbox fixture: parsed=%+v err=%v", parsed, err)
	}
	proof, err := dkvsindexer.ParseFeeProof(message.FeeProof)
	if err != nil || proof.Mode != dkvsindexer.FeeModeFreeLocal {
		t.Fatalf("mailbox proof=%+v err=%v", proof, err)
	}
	if dkvsindexer.IsExpired(message, 1) {
		t.Fatal("mailbox fixture already expired")
	}
	if _, err := transport.indexer.PutInternalMailbox(message); err != nil {
		t.Fatalf("internal append: %v", err)
	}
	command, err := dkvsindexer.DeleteCommand(message, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := SignDKVSRecord(dkvsTestWalletFromPriv(t, sender), command); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.indexer.DeleteInternalMailbox(command); err == nil || (!errors.Is(err, dkvsindexer.ErrInvalidSignature) && !errors.Is(err, dkvsindexer.ErrPermissionDenied)) {
		t.Fatalf("sender-signed delete err=%v", err)
	}
}
