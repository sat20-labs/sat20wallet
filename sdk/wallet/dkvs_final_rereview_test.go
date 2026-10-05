package wallet

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	p2p "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs/p2p"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ordinary SDK tests, without a build tag or a subprocess runner. Reuse the
// existing real Indexer/SDK transport and production P2P serving lifecycle.
// Identity/payment inputs are fixtures; these are not public-network tests.
func TestDKVSFinalReReview(t *testing.T) {
	newClient := func(t *testing.T) (*Manager, *SatsNetDKVSClient, *satoshinetDKVSTestTransport) {
		t.Helper()
		privateKey, err := btcec.NewPrivateKey()
		require.NoError(t, err)
		manager := newRGB11MultiDeviceManager(t, privateKey, 908)
		transport := newSatoshiNetDKVSTestTransport()
		configureRGB11DKVSTestManager(manager, transport)
		client, err := manager.GetDKVSClient()
		require.NoError(t, err)
		return manager, client, transport
	}

	t.Run("QuotaRejectionReturnsErrorAndReleasesTransport", func(t *testing.T) {
		manager, client, transport := newClient(t)
		// This transport returns a structured business error, not HTTP 429.
		// HTTP 429 has a separate transient classification; the HTTP regression
		// below checks a real non-transient business refusal over NetClient.
		transport.indexer = dkvs.New(newMemoryKVDB(), dkvs.Config{
			EndpointID: "test-core-node", AllowFreeLocal: true,
			FreeLocalCache: dkvs.FreeLocalCachePolicy{
				Enabled: true, MaxTTL: 1000, MaxRecordsPerSigner: 1,
				MaxBytesPerSigner: 1 << 20, MaxTotalRecords: 100, MaxTotalBytes: 64 << 20,
			},
			CurrentHeight: func() uint64 { return transport.height },
		})
		firstKey := accountTestKey(t, manager, "rereview-quota/first")
		secondKey := accountTestKey(t, manager, "rereview-quota/second")
		options := dkvs.RecordOptions{IssueHeight: 1, TTL: 100}
		first, err := client.PutSignedRecordFreeLocal(manager.wallet, firstKey, []byte("first"), options)
		require.NoError(t, err)

		// Control: the real Indexer refuses the same mutation through a raw
		// signer client, without committing the rejected key.
		raw := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", transport).WithWriteSigner(manager.wallet)
		_, err = raw.PutSignedRecordFreeLocal(manager.wallet, secondKey, []byte("second"), options)
		require.True(t, IsDKVSErrorCode(err, dkvs.ErrorCodeQuotaExceeded), "expected real server quota rejection, got %v", err)

		var writeErr error
		var panicked any
		func() {
			defer func() { panicked = recover() }()
			_, writeErr = client.PutSignedRecordFreeLocal(manager.wallet, secondKey, []byte("second"), options)
		}()
		manager.dkvs.runMu.Lock()
		busy := manager.dkvs.runActive
		manager.dkvs.runMu.Unlock()
		t.Logf("quota rejection: panic=%v returned_error=%v transport_busy=%t", panicked, writeErr, busy)
		assert.Nil(t, panicked, "a normal structured quota rejection must return to the SDK caller")
		assert.True(t, IsDKVSErrorCode(writeErr, dkvs.ErrorCodeQuotaExceeded), "the caller must receive the quota error")
		assert.False(t, busy, "the rejected request must release the transport coordinator")
		_, err = raw.GetRecordDirect(secondKey)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		actual, err := raw.GetRecordDirect(firstKey)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(first), dkvs.RecordHash(actual))
	})

	t.Run("HTTPStorageDowngradeRefusalReleasesTransport", func(t *testing.T) {
		manager, client, transport := newClient(t)
		transport.indexer.SetFeeVerifier(pwaReviewFeeVerifier{
			JSONFeeVerifier: dkvs.JSONFeeVerifier{AllowFreeLocal: true}, height: &transport.height,
		})
		// Serve the existing real Indexer dispatcher over loopback HTTP. Only
		// JSON routing/status mapping is fixture code; this is not the full
		// production router or chain-backed binding admission.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			var payload []byte
			var err error
			if at := strings.Index(path, "/v3/dkvs/"); at >= 0 {
				path = path[at:]
				if r.Method == http.MethodGet {
					query := make(map[string]string)
					for key := range r.URL.Query() { query[key] = r.URL.Query().Get(key) }
					payload, err = transport.SendDKVSGet(path, query)
				} else {
					body, readErr := io.ReadAll(r.Body)
					if readErr != nil { http.Error(w, readErr.Error(), http.StatusBadRequest); return }
					payload, err = transport.SendDKVSPost(path, body)
				}
			} else {
				payload, err = transport.SendGetRequest(&URL{Path: path})
			}
			if err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }
			var envelope struct { Code int `json:"code"`; ErrorCode string `json:"error_code"` }
			if err := json.Unmarshal(payload, &envelope); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError); return
			}
			status := http.StatusOK
			if envelope.Code != 0 {
				switch dkvs.ErrorCode(envelope.ErrorCode) {
				case dkvs.ErrorCodeStorageModeDowngrade: status = http.StatusConflict
				case dkvs.ErrorCodeQuotaExceeded: status = http.StatusTooManyRequests
				default: status = http.StatusBadRequest
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write(payload)
		}))
		t.Cleanup(server.Close)
		client.Scheme, client.Host = "http", strings.TrimPrefix(server.URL, "http://")
		client.Http = &NetClient{Client: server.Client()}
		raw := NewSatsNetDKVSClient(client.Scheme, client.Host, client.Proxy, client.Http).WithWriteSigner(manager.wallet)
		key := accountTestKey(t, manager, "rereview-http/value")
		paid, err := client.PutSignedRecordWithAutopay(manager.wallet, key, []byte("paid"),
			dkvs.RecordOptions{IssueHeight: 1}, DKVSAutopayOptions{
				PoolContract: "review-fixture-pool", AddressParams: &chaincfg.TestNetParams,
			})
		require.NoError(t, err)
		options := dkvs.RecordOptions{IssueHeight: 1, TTL: 100}
		_, err = raw.PutSignedRecordFreeLocal(manager.wallet, key, []byte("downgrade"), options)
		require.ErrorIs(t, err, dkvs.ErrStorageModeDowngrade)
		var httpError *HTTPResponseError
		require.ErrorAs(t, err, &httpError)
		require.Equal(t, http.StatusConflict, httpError.StatusCode)

		var panicked any
		var writeErr error
		func() {
			defer func() { panicked = recover() }()
			_, writeErr = client.PutSignedRecordFreeLocal(manager.wallet, key, []byte("downgrade"), options)
		}()
		manager.dkvs.runMu.Lock()
		busy := manager.dkvs.runActive
		manager.dkvs.runMu.Unlock()
		t.Logf("HTTP 409 business refusal: panic=%v returned_error=%v transport_busy=%t", panicked, writeErr, busy)
		assert.Nil(t, panicked, "rejecting an invalid storage transition must not panic a managed SDK request")
		assert.ErrorIs(t, writeErr, dkvs.ErrStorageModeDowngrade)
		assert.False(t, busy, "even a recovered panic must not leave the shared coordinator occupied")
		actual, err := raw.GetRecordDirect(key)
		require.NoError(t, err)
		require.Equal(t, dkvs.RecordHash(paid), dkvs.RecordHash(actual))
	})

	t.Run("DirectorySnapshotsPreserveAllNotifySubscriptions", func(t *testing.T) {
		manager, _, source := newClient(t)
		source.indexer.SetFeeVerifier(pwaReviewFeeVerifier{
			JSONFeeVerifier: dkvs.JSONFeeVerifier{AllowFreeLocal: true}, height: &source.height,
		})
		client := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", source).WithWriteSigner(manager.wallet)
		keys := []string{
			accountTestKey(t, manager, "rereview-subscription-a/value"),
			accountTestKey(t, manager, "rereview-subscription-b/value"),
		}
		var prefixes []string
		var records []*wire.DKVSRecord
		for _, key := range keys {
			prefix, err := dkvs.CollectionPathForKey(key)
			require.NoError(t, err)
			prefixes = append(prefixes, prefix)
			record, err := client.PutSignedRecordWithAutopay(manager.wallet, key, []byte("initial"),
				dkvs.RecordOptions{IssueHeight: 1}, DKVSAutopayOptions{
					PoolContract: "review-fixture-pool", AddressParams: &chaincfg.TestNetParams,
				})
			require.NoError(t, err)
			records = append(records, record)
		}
		validator, err := btcec.NewPrivateKey()
		require.NoError(t, err)
		peer := &p2p.PeerState{}
		t.Cleanup(peer.Close)
		var responses []*wire.MsgDKVSSyncResponse
		serving := p2p.Handler{
			Store: pwaReviewPeerStore{source.indexer}, Peer: peer, Node: &p2p.NodeState{},
			Net: chaincfg.TestNetParams.Net, MirrorAuthority: true,
			Sign: func(payload []byte) ([]byte, error) {
				return ecdsa.Sign(validator, chainhash.HashB(payload)).Serialize(), nil
			},
			Send: func(message wire.Message) {
				if response, ok := message.(*wire.MsgDKVSSyncResponse); ok {
					responses = append(responses, response)
				}
			},
		}
		serve := func(request *wire.MsgDKVSSyncRequest) {
			t.Helper()
			before := len(responses)
			serving.OnSyncRequest(request)
			require.Len(t, responses, before+1)
			response := responses[len(responses)-1]
			require.True(t, response.Done)
			require.True(t, p2p.VerifySyncSignature(serving.Net,
				hex.EncodeToString(validator.PubKey().SerializeCompressed()), request.Cursor, request.Filters, response))
		}
		// A non-mining node subscribes to both collections during discovery.
		serve(&wire.MsgDKVSSyncRequest{SessionID: 1, Limit: 256, Filters: []wire.DKVSSyncFilter{
			{Type: "prefix", Target: prefixes[0]}, {Type: "prefix", Target: prefixes[1]},
		}})
		for _, record := range records {
			require.True(t, peer.WantsNotify(p2p.NotifyForRecord(record), false))
		}
		// Production discovery is followed by independent current snapshots.
		// These temporary session filters must not replace the subscriptions.
		for n, prefix := range prefixes {
			serve(&wire.MsgDKVSSyncRequest{SessionID: uint64(n + 2), Limit: 256,
				Filters: []wire.DKVSSyncFilter{{Type: "path", Target: prefix}}})
		}
		for n, key := range keys {
			updated, err := client.PutSignedRecordWithAutopay(manager.wallet, key, []byte("updated"),
				dkvs.RecordOptions{IssueHeight: 1}, DKVSAutopayOptions{
				PoolContract: "review-fixture-pool", AddressParams: &chaincfg.TestNetParams,
				})
			require.NoError(t, err)
			notify := p2p.NotifyForRecord(updated)
			require.NotNil(t, notify)
			t.Logf("post-sync notify: collection=%d allowed=%t", n, peer.WantsNotify(notify, false))
			assert.True(t, peer.WantsNotify(notify, false), "snapshot of another collection must not unsubscribe %s", prefixes[n])
		}
	})
}
