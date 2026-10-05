package wallet

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	core "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	"github.com/sat20-labs/satoshinet/btcec"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These regressions use ordinary SDK tests and existing endpoint/replica
// fixtures. HTTP 403 comes from production wallet binding admission.
func TestDKVSPWAReviewBindingRefusalReturnsErrorAndKeepsReceiving(t *testing.T) {
	for _, mode := range []string{"foreground", "outbox-replay"} {
		t.Run(mode, func(t *testing.T) {
			manager, remote := finalDKVSTestManager(t)
			identity, err := btcec.NewPrivateKey()
			require.NoError(t, err)
			remote.endpointID = hex.EncodeToString(identity.PubKey().SerializeCompressed())
			node := dkvs.New(newMemoryKVDB(), dkvs.Config{EndpointID: remote.endpointID,
				AllowFreeLocal: true, FreeLocalCache: remote.freeLocal, CurrentHeight: func() uint64 { return 1 }})
			bindingKey, err := dkvs.AccountMappingKey(GetChainParam().Name, manager.wallet.GetAddress())
			require.NoError(t, err)
			admission := &dkvs.WalletRPCAdmission{Indexer: node, IsCoreNode: func() bool { return true },
				CurrentBinding: func(string) (*wire.DKVSRecord, error) { return node.Get(bindingKey) }}
			var submissions atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := r.URL.Path
				if at := strings.Index(path, "/v3/dkvs/"); at >= 0 {
					path = path[at:]
				}
				var payload []byte
				var err error
				if path == "/v3/dkvs/records/batch-cas" {
					var request DKVSBatchCASRequest
					if err = json.NewDecoder(r.Body).Decode(&request); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					if len(request.Mutations) != 1 || !request.Mutations[0].ExpectAbsent {
						http.Error(w, "fixture requires an absent-key write", http.StatusBadRequest)
						return
					}
					submissions.Add(1)
					_, rejected := admission.PutRecords([]dkvs.CASMutation{{Record: request.Mutations[0].Record,
						Precondition: dkvs.WritePrecondition{ExpectAbsent: true}}},
						dkvs.BatchCASOptions{EndpointID: request.EndpointID, RequestID: request.RequestID}, request.Authorization)
					if dkvs.ErrorCodeOf(rejected) != dkvs.ErrorCodePermissionDenied {
						http.Error(w, "binding admission did not reject this request", http.StatusInternalServerError)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					_ = json.NewEncoder(w).Encode(map[string]any{"code": -1,
						"error_code": dkvs.ErrorCodeOf(rejected), "msg": rejected.Error()})
					return
				} else if r.Method == http.MethodPost {
					body, readErr := io.ReadAll(r.Body)
					if readErr != nil {
						http.Error(w, readErr.Error(), http.StatusBadRequest)
						return
					}
					payload, err = remote.SendDKVSPostContext(r.Context(), path, body)
				} else if strings.HasPrefix(path, "/v3/dkvs/") {
					query := make(map[string]string)
					for key := range r.URL.Query() {
						query[key] = r.URL.Query().Get(key)
					}
					payload, err = remote.SendDKVSGetContext(r.Context(), path, query)
				} else {
					payload, err = remote.SendGetRequest(&URL{Path: path})
				}
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(payload)
			}))
			t.Cleanup(server.Close)
			manager.cfg.IndexerL2.Scheme = "http"
			manager.cfg.IndexerL2.Host = strings.TrimPrefix(server.URL, "http://")
			manager.http = &NetClient{Client: server.Client()}
			client, err := manager.GetDKVSClient()
			require.NoError(t, err)
			key := accountTestKey(t, manager, "pwa-cycle-receive/value")
			prefix, err := dkvs.CollectionPathForKey(key)
			require.NoError(t, err)
			seed := finalDKVSSeedClient(manager, remote)
			_, err = seed.PutRecord(freeLocalRecord(t, manager, key, 1, "v1"))
			require.NoError(t, err)
			require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
			require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
			pendingKey := accountTestKey(t, manager, "pwa-cycle-refused/value")
			var entry *core.BatchOutboxEntry
			if mode == "outbox-replay" {
				mutations := []dkvs.CASMutation{{Record: freeLocalRecord(t, manager, pendingKey, 1, "original"),
					Precondition: dkvs.WritePrecondition{ExpectAbsent: true}}}
				entry, err = core.NewBatchOutboxEntry(client.replicaNamespace, mutations, remote.endpointID, core.OutboxOrigin{})
				require.NoError(t, err)
				entry.Authorization, err = client.WithWriteSigner(manager.wallet).prepareWriteAuthorization(mutations, entry.EndpointID, entry.RequestID)
				require.NoError(t, err)
				require.NoError(t, core.NewReplicaStore(manager.db).QueueOutbox(entry))
			}
			updated, err := seed.PutRecord(freeLocalRecord(t, manager, key, 2, "v2"))
			require.NoError(t, err)
			var returned error
			panicked := captureDKVSOutboxPanic(func() {
				if mode == "foreground" {
					_, returned = client.PutSignedRecordFreeLocal(manager.wallet, pendingKey, []byte("original"),
						dkvs.RecordOptions{IssueHeight: 1, TTL: testRGB11FreeLocalTTL})
				} else {
					_, returned = manager.syncDKVSOnce()
				}
			})
			t.Logf("binding refusal: panic=%v returned=%v submissions=%d", panicked, returned, submissions.Load())
			require.Nil(t, panicked, "a normal binding refusal must not panic foreground operations or the receiving worker")
			require.ErrorIs(t, returned, dkvs.ErrPermissionDenied)
			var response *HTTPResponseError
			require.ErrorAs(t, returned, &response)
			require.Equal(t, http.StatusForbidden, response.StatusCode)
			replica := core.NewReplicaStore(manager.db)
			entries, err := replica.LoadOutbox(client.replicaNamespace)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.Equal(t, core.DKVSOutboxConflict, entries[0].State)
			require.Equal(t, string(dkvs.ErrorCodePermissionDenied), entries[0].LastErrorCode)
			if entry != nil {
				require.Equal(t, entry.RequestID, entries[0].RequestID)
				require.Equal(t, entry.Authorization, entries[0].Authorization)
			}
			_, _, receiveErr := manager.syncDKVSOnceResult()
			require.NoError(t, receiveErr)
			current, err := replica.LoadSubscriptionRecord(client.replicaNamespace, key)
			require.NoError(t, err)
			require.Equal(t, dkvs.RecordHash(updated), dkvs.RecordHash(current))
			require.Equal(t, int32(1), submissions.Load(), "a refused original request must not be automatically reauthorized or resubmitted")
			manager.dkvs.start()
			defer manager.dkvs.stopAndWait()
			updated, err = seed.PutRecord(freeLocalRecord(t, manager, key, 3, "v3"))
			require.NoError(t, err)
			require.Eventually(t, func() bool {
				current, err := replica.LoadSubscriptionRecord(client.replicaNamespace, key)
				return err == nil && dkvs.RecordHash(current) == dkvs.RecordHash(updated)
			}, 3*time.Second, 10*time.Millisecond, "binding refusal must leave ongoing receiving active")
			require.Equal(t, int32(1), submissions.Load())
		})
	}
}

func TestDKVSPWAReviewSelectedRecoverySourceDoesNotChangeReplicaHeight(t *testing.T) {
	for _, kind := range []string{"authoritative-recovery", "on-demand-record"} {
		t.Run(kind, func(t *testing.T) {
			manager, source := finalDKVSTestManager(t)
			client, err := manager.GetDKVSClient()
			require.NoError(t, err)
			key := accountTestKey(t, manager, "pwa-cycle-main/value")
			prefix, err := dkvs.CollectionPathForKey(key)
			require.NoError(t, err)
			_, err = finalDKVSSeedClient(manager, source).PutRecord(freeLocalRecord(t, manager, key, 1, "main-source"))
			require.NoError(t, err)
			require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
			require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
			before, known := manager.dkvs.endpointVerificationHeight()
			require.True(t, known)
			other := newRGB11MemoryDKVSHTTP()
			other.endpointID = "selected-recovery-source"
			other.bestHeight = 2 * int64(testRGB11FreeLocalTTL)
			otherKey := accountTestKey(t, manager, "pwa-cycle-recovery/package")
			record, err := newDKVSAccountSignedRecordWithFreeLocal(manager.wallet, otherKey, []byte("recovery-material"),
				dkvs.RecordOptions{Seq: 1, IssueHeight: uint64(other.bestHeight), TTL: testRGB11FreeLocalTTL})
			require.NoError(t, err)
			_, err = finalDKVSSeedClient(manager, other).PutRecord(record)
			require.NoError(t, err)
			// PWA recovery and guardian reads select their location via this
			// production store entry; a direct read must not rebind the replica.
			store, err := manager.ensureDKVSManager().storeFor("http", "recovery-source.test", "testnet", other)
			require.NoError(t, err)
			config, err := store.client.GetDKVSClientConfig()
			require.NoError(t, err)
			require.NotEqual(t, source.endpointID, config.EndpointID)
			var value *dkvsValue
			if kind == "authoritative-recovery" {
				value, err = store.GetAuthoritative(otherKey)
			} else {
				value, err = store.Get(otherKey)
			}
			require.NoError(t, err, "explicit read-only recovery must validate the selected source at its own height")
			require.Equal(t, record.Value, value.Value)
			height, known := manager.dkvs.endpointVerificationHeight()
			assert.True(t, known)
			assert.Equal(t, before, height, "another endpoint's read height must not become the confirmed replica's authority")
			local, err := client.GetRecord(key)
			assert.NoError(t, err, "reading a recovery source must not falsely expire the main endpoint's confirmed account data")
			if local != nil {
				assert.Equal(t, "main-source", string(local.Value))
			}
		})
	}
}

func TestDKVSPWAReviewKnownSourceMismatchCannotRefreshReplicaHeight(t *testing.T) {
	manager, source := finalDKVSTestManager(t)
	client, err := manager.GetDKVSClient()
	require.NoError(t, err)
	key := accountTestKey(t, manager, "pwa-cycle-height/value")
	prefix, err := dkvs.CollectionPathForKey(key)
	require.NoError(t, err)
	_, err = finalDKVSSeedClient(manager, source).PutRecord(freeLocalRecord(t, manager, key, 1, "source-a"))
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	before, known := manager.dkvs.endpointVerificationHeight()
	require.True(t, known)
	other := newRGB11MemoryDKVSHTTP()
	other.endpointID, other.bestHeight = "source-b", 2*int64(testRGB11FreeLocalTTL)
	store, err := manager.dkvs.storeFor("http", "source-b.test", "testnet", other)
	require.NoError(t, err)
	_, _, _, err = store.ConfigWithVerificationHeight()
	assert.ErrorIs(t, err, dkvs.ErrEndpointMismatch, "a storage estimate cannot combine source B's policy/height with source A's confirmed replica")
	height, known := manager.dkvs.endpointVerificationHeight()
	assert.True(t, known)
	assert.Equal(t, before, height)
	_, err = client.GetRecord(key)
	assert.NoError(t, err)
}

func TestDKVSPWAReviewStorageFallbackKeepsSourceBoundary(t *testing.T) {
	for _, origin := range []string{"same-source", "other-source"} {
		for _, failure := range []string{"config-refresh", "height-query"} {
			t.Run(origin+"/"+failure, func(t *testing.T) {
				manager, source := finalDKVSTestManager(t)
				client, err := manager.GetDKVSClient()
				require.NoError(t, err)
				key := accountTestKey(t, manager, "pwa-cycle-estimate/value")
				prefix, err := dkvs.CollectionPathForKey(key)
				require.NoError(t, err)
				_, err = finalDKVSSeedClient(manager, source).PutRecord(freeLocalRecord(t, manager, key, 1, "confirmed-source"))
				require.NoError(t, err)
				require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
				require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
				before, known := manager.dkvs.endpointVerificationHeight()
				require.True(t, known)
				config, err := source.DKVSClientConfig()
				require.NoError(t, err)
				if origin == "other-source" {
					config.EndpointID = "selected-estimate-source"
					config.FreeLocal.MaxTTL *= 2
				}
				var configQueries atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/v3/dkvs/config") {
						if configQueries.Add(1) > 1 && failure == "config-refresh" {
							http.Error(w, "temporary config failure", http.StatusServiceUnavailable)
							return
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": config})
						return
					}
					http.Error(w, "temporary height failure", http.StatusServiceUnavailable)
				}))
				t.Cleanup(server.Close)
				store, err := manager.dkvs.storeFor("http", strings.TrimPrefix(server.URL, "http://"), "testnet",
					&NetClient{Client: server.Client()})
				require.NoError(t, err)
				policy, height, heightKnown, err := store.ConfigWithVerificationHeight()
				if origin == "same-source" {
					require.NoError(t, err, "temporary online failure may retain the same confirmed source's height")
					require.NotNil(t, policy)
					assert.Equal(t, before, height)
					assert.True(t, heightKnown)
				} else {
					assert.ErrorIs(t, err, dkvs.ErrEndpointMismatch, "a refresh failure cannot authorize mixing source B's policy with source A's height")
					assert.Nil(t, policy)
					assert.Zero(t, height)
					assert.False(t, heightKnown)
				}
				height, heightKnown = manager.dkvs.endpointVerificationHeight()
				assert.Equal(t, before, height)
				assert.True(t, heightKnown)
				_, err = client.GetRecord(key)
				assert.NoError(t, err)
			})
		}
	}
}

func TestDKVSPWAReviewLowerRecoveryHeightCannotReviveExpiredReplica(t *testing.T) {
	manager, source := finalDKVSTestManager(t)
	client, err := manager.GetDKVSClient()
	require.NoError(t, err)
	key := accountTestKey(t, manager, "pwa-cycle-expired/value")
	prefix, err := dkvs.CollectionPathForKey(key)
	require.NoError(t, err)
	_, err = finalDKVSSeedClient(manager, source).PutRecord(freeLocalRecord(t, manager, key, 1, "expired-at-main-source"))
	require.NoError(t, err)
	require.NoError(t, manager.SubscribeDKVSPrefix(prefix))
	require.NoError(t, manager.dkvs.forceCurrentPrefixes(client, []string{prefix}))
	source.bestHeight = 2 * int64(testRGB11FreeLocalTTL)
	height, err := manager.dkvs.refreshVerificationBestHeight(client)
	require.NoError(t, err)
	_, err = client.GetRecord(key)
	require.ErrorIs(t, err, dkvs.ErrExpiredRecord)
	other := newRGB11MemoryDKVSHTTP()
	other.endpointID = "lower-recovery-source"
	otherKey := accountTestKey(t, manager, "pwa-cycle-lower/package")
	_, err = finalDKVSSeedClient(manager, other).PutRecord(freeLocalRecord(t, manager, otherKey, 1, "valid-at-recovery-source"))
	require.NoError(t, err)
	store, err := manager.dkvs.storeFor("http", "lower-source.test", "testnet", other)
	require.NoError(t, err)
	_, err = store.GetAuthoritative(otherKey)
	require.NoError(t, err)
	observed, known := manager.dkvs.endpointVerificationHeight()
	assert.True(t, known)
	assert.Equal(t, height, observed)
	_, err = client.GetRecord(key)
	assert.ErrorIs(t, err, dkvs.ErrExpiredRecord, "an explicit recovery read cannot revive expired main-source records")
}

func TestDKVSPWAReviewDirectReadHeightDeadlineAndZero(t *testing.T) {
	for _, mode := range []string{"authoritative", "on-demand"} {
		for _, heightCase := range []string{"zero", "blocked"} {
			t.Run(mode+"/"+heightCase, func(t *testing.T) {
				manager, remote := finalDKVSTestManager(t)
				key := accountTestKey(t, manager, "pwa-cycle-direct/value")
				record, err := newDKVSAccountSignedRecordWithFreeLocal(manager.wallet, key, []byte("zero-height-valid"),
					dkvs.RecordOptions{Seq: 1, TTL: testRGB11FreeLocalTTL})
				require.NoError(t, err)
				_, err = finalDKVSSeedClient(manager, remote).PutRecord(record)
				require.NoError(t, err)
				release := make(chan struct{})
				var once sync.Once
				releaseHeight := func() { once.Do(func() { close(release) }) }
				defer releaseHeight()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/btc/block/bestblockheight") {
						if heightCase == "blocked" {
							select {
							case <-release:
							case <-r.Context().Done():
								return
							}
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": 0})
						return
					}
					path := r.URL.Path[strings.Index(r.URL.Path, "/v3/dkvs/"):]
					query := make(map[string]string)
					for key := range r.URL.Query() {
						query[key] = r.URL.Query().Get(key)
					}
					payload, err := remote.SendDKVSGetContext(r.Context(), path, query)
					if err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
					_, _ = w.Write(payload)
				}))
				t.Cleanup(server.Close)
				store, err := manager.ensureDKVSManager().storeFor("http", strings.TrimPrefix(server.URL, "http://"), "testnet",
					&NetClient{Client: server.Client()})
				require.NoError(t, err)
				done := make(chan error, 1)
				go func() {
					var err error
					if mode == "authoritative" {
						_, err = store.GetAuthoritative(key)
					} else {
						_, err = store.Get(key)
					}
					done <- err
				}()
				select {
				case err = <-done:
				case <-time.After(dkvsUnmanagedReadTimeout + time.Second):
					t.Error("selected-source height query outlived the existing bounded direct-read request")
					releaseHeight()
					err = <-done
				}
				if heightCase == "blocked" {
					assert.ErrorIs(t, err, context.DeadlineExceeded)
				} else {
					require.NoError(t, err, "actual source height zero must remain a known verification height")
				}
				_, known := manager.dkvs.endpointVerificationHeight()
				assert.False(t, known, "direct read height is request-local, including height zero")
			})
		}
	}
}
