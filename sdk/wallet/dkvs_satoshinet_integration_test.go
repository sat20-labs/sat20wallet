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
	indexer *dkvsindexer.Indexer
	height  uint64
}

func newSatoshiNetDKVSTestTransport() *satoshinetDKVSTestTransport {
	db := newMemoryKVDB()
	indexer := dkvsindexer.New(db, dkvsindexer.Config{
		EndpointID:     "test-core-node",
		AllowFreeLocal: true,
		FreeLocalCache: dkvsindexer.FreeLocalCachePolicy{
			Enabled: true, MaxTTL: 1000,
			MaxRecordsPerSigner: 100, MaxBytesPerSigner: 1 << 20,
			MaxTotalRecords: 10000, MaxTotalBytes: 64 << 20,
		},
		CurrentHeight: func() uint64 { return 1 },
	})
	return &satoshinetDKVSTestTransport{indexer: indexer, height: 1}
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
		return json.Marshal(map[string]interface{}{
			"code": 0, "msg": "ok", "data": record,
			"etag": dkvsindexer.RecordHash(record).String(),
		})
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

func (t *satoshinetDKVSTestTransport) SendDKVSPost(path string, body []byte) ([]byte, error) {
	switch path {
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
		result, err := t.indexer.PutLocalBatchCASResultWithOptions(mutations, dkvsindexer.BatchCASOptions{
			EndpointID: request.EndpointID, RequestID: request.RequestID,
		})
		return satoshinetTestResponse(result, err)
	case "/v3/dkvs/prefixes/snapshot":
		var request struct {
			Prefix string `json:"prefix"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		result, err := t.indexer.PrefixSnapshot(request.Prefix)
		return satoshinetTestResponse(result, err)
	case "/v3/dkvs/prefixes/status":
		var request struct {
			EndpointID string                         `json:"endpoint_id"`
			Prefixes   []dkvsindexer.PrefixGeneration `json:"prefixes"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		result, err := t.indexer.PrefixStatus(request.EndpointID, request.Prefixes)
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
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return t.SendGetRequest(url)
	}
}

func (t *satoshinetDKVSTestTransport) SendDKVSGetContext(ctx context.Context, path string,
	query map[string]string) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return t.SendDKVSGet(path, query)
	}
}

func (t *satoshinetDKVSTestTransport) SendDKVSPostContext(ctx context.Context, path string, body []byte) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return t.SendDKVSPost(path, body)
	}
}

func (t *satoshinetDKVSTestTransport) SendPostRequestContext(ctx context.Context, url *URL, body []byte) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return t.SendPostRequest(url, body)
	}
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
	message := &swire.DKVSRecord{
		Version: dkvsindexer.Version, Key: key, Value: []byte("ciphertext"),
		Seq: 1, IssueHeight: 1, TTL: 100,
	}
	message.FeeProof, err = dkvsindexer.EncodeFeeProof(&dkvsindexer.FeeProof{
		Mode: dkvsindexer.FeeModeFreeLocal,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := dkvsindexer.ParseKey(message.Key)
	if err != nil || parsed.Namespace != "mail" || len(parsed.Segments) != 4 {
		t.Fatalf("invalid mailbox fixture key: parsed=%+v err=%v", parsed, err)
	}
	proof, err := dkvsindexer.ParseFeeProof(message.FeeProof)
	if err != nil || proof.Mode != dkvsindexer.FeeModeFreeLocal {
		t.Fatalf("invalid mailbox fixture fee proof: proof=%+v err=%v", proof, err)
	}
	if dkvsindexer.IsExpired(message, 1) {
		t.Fatal("mailbox fixture is already expired")
	}
	if _, err := transport.indexer.PutInternalMailbox(message); err != nil {
		t.Fatalf("internal mailbox append rejected: %v", err)
	}

	// A mailbox tombstone is owned by the recipient account. Signing it with
	// the original sender must fail against the recipient-derived account key.
	tombstone, err := dkvsindexer.NewAccountRecord(key, nil, dkvsindexer.RecordOptions{
		Seq: 2, IssueHeight: 1, TTL: 100, Flags: dkvsindexer.FlagTombstone,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := SignDKVSRecord(dkvsTestWalletFromPriv(t, sender), tombstone); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.indexer.DeleteInternalMailbox(tombstone); err == nil ||
		(!errors.Is(err, dkvsindexer.ErrInvalidSignature) && !errors.Is(err, dkvsindexer.ErrPermissionDenied)) {
		t.Fatalf("sender-signed mailbox delete err=%v", err)
	}
}
