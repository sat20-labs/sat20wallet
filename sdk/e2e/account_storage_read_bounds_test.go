package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func storeNoReadEffects(t *testing.T, d *nativeUIDevice, want error) {
	t.Helper()
	auth, err := d.mgr.PendingAccountStorageAuthorization()
	assert.ErrorIs(t, err, want)
	assert.True(t, auth == nil, "a preview must not grant storage")
	assert.Zero(t, d.http.broadcasts.Load(), "a preview must not broadcast")
}

func TestSDKStorageReadBounds(t *testing.T) {
	n := newDKVSNoPluginUnfundedNetwork(t)
	waitForDKVSPeerReady(t, n)
	core, err := dkvsClientForNode(t, n.Core).GetDKVSClientConfig()
	require.NoError(t, err)
	require.True(t, core.FreeLocal.Enabled)
	require.Positive(t, core.FreeLocal.MaxTTL)
	require.NotEmpty(t, core.EndpointID)

	t.Run("NodePolicyOnStartup", func(t *testing.T) {
		cfg, _ := accountReviewConfig(t, n)
		proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: cfg.IndexerL2.Scheme, Host: cfg.IndexerL2.Host})
		var ttl atomic.Uint64
		ttl.Store(core.FreeLocal.MaxTTL)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/v3/dkvs/config") {
				policy := *core
				policy.FreeLocal.MaxTTL = ttl.Load()
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": policy})
				return
			}
			proxy.ServeHTTP(w, r)
		}))
		t.Cleanup(server.Close)
		addr, err := url.Parse(server.URL)
		require.NoError(t, err)
		cfg.IndexerL2.Scheme, cfg.IndexerL2.Host = addr.Scheme, addr.Host
		d := newNativeUIInactive(t, cfg, accountSyncMnemonic(t, 249))
		start := func(want uint64) {
			t.Helper()
			d.mgr.Start()
			require.Eventually(t, func() bool {
				var saved dkvs.ClientConfig
				err := d.db.BatchRead([]byte("dkvs/node-config/"), false, func(_, raw []byte) error {
					return wallet.DecodeFromBytes(raw, &saved)
				})
				return err == nil && saved.EndpointID == core.EndpointID && saved.FreeLocal.MaxTTL == want
			}, 10*time.Second, 50*time.Millisecond)
		}
		start(core.FreeLocal.MaxTTL)
		require.Greater(t, core.FreeLocal.MaxTTL, uint64(1))
		ttl.Store(core.FreeLocal.MaxTTL - 1)
		plan := nativeUIStorage(t, d.mgr, "temporary", 100)
		require.Equal(t, core.FreeLocal.MaxTTL, plan.TTLBlocks)
		granted, err := d.mgr.ConfirmStorage(context.Background(), plan)
		require.NoError(t, err)
		require.Equal(t, plan.TTLBlocks, granted.Option.TTLBlocks)

		d.close()
		d.open(t)
		readTTL := func() uint64 {
			return nativeUIStorage(t, d.mgr, "temporary", 100).TTLBlocks
		}
		require.Equal(t, plan.TTLBlocks, readTTL(), "reopened database retains the last startup policy")
		_, err = d.mgr.UnlockWallet(d.password)
		require.NoError(t, err)
		requireE2EInactive(t, d.mgr, d.password)
		stale := nativeUIStorage(t, d.mgr, "temporary", 100)
		start(ttl.Load())
		_, err = d.mgr.ConfirmStorage(context.Background(), stale)
		require.ErrorIs(t, err, wallet.ErrStorageTTLChanged)
		storeNoReadEffects(t, d, wallet.ErrAccountStorageAuthorizationMissing)
		current := nativeUIStorage(t, d.mgr, "temporary", 100)
		require.Equal(t, ttl.Load(), current.TTLBlocks)
		granted, err = d.mgr.ConfirmStorage(context.Background(), current)
		require.NoError(t, err)
		require.Equal(t, current.TTLBlocks, granted.Option.TTLBlocks)
		_, err = d.mgr.ConfirmStorage(context.Background(), stale)
		require.ErrorIs(t, err, wallet.ErrStorageTTLChanged)
	})

	for i, branch := range []string{"SameURLPrimary", "ManagerStop", "CallerDeadline"} {
		t.Run(branch, func(t *testing.T) {
			cfg, _ := accountReviewConfig(t, n)
			d := newNativeUIDevice(t, cfg, "")
			w := &storeContextWire{nativeUIHTTP: d.http}
			d.mgr.SetDKVSHttpClient(w)
			id, err := d.mgr.ImportWallet(accountSyncMnemonic(t, byte(244+i)), d.password)
			require.NoError(t, err)
			requireE2EInactive(t, d.mgr, d.password)
			require.Equal(t, core.FreeLocal.MaxTTL, nativeUIStorage(t, d.mgr, "temporary", 100).TTLBlocks)
			location, err := d.mgr.AccountIndexerLocation()
			require.NoError(t, err)

			var caller context.Context
			var cancel context.CancelFunc
			if branch == "CallerDeadline" {
				caller, cancel = context.WithTimeout(context.Background(), 3*time.Second)
			} else {
				caller, cancel = context.WithCancel(context.Background())
			}
			g := newStoreReplyGate()
			w.height.Store(g)
			var query storeQuery
			query.start(caller, d.mgr)
			var changed chan struct{}
			t.Cleanup(func() {
				cancel()
				g.open()
				awaitStoreEvent(t, query.done, "foreground finished before device teardown")
				if changed != nil {
					awaitStoreEvent(t, changed, "lifecycle operation finished before teardown")
				}
			})
			height := awaitStoreEvent(t, g.seen, "foreground consumed config and reached real height")
			require.NoError(t, height.err)
			require.NoError(t, caller.Err())
			wantAuth := wallet.ErrAccountStorageAuthorizationMissing
			var replacement *storeContextWire
			if branch == "CallerDeadline" {
				awaitStoreEvent(t, caller.Done(), "real caller deadline")
				select {
				case <-height.ctx.Done():
				case <-time.After(2 * time.Second):
					t.Error("caller deadline did not interrupt the held read")
				}
			} else {
				if branch == "SameURLPrimary" {
					// Public setter installs a different primary client, even at
					// precisely the same URL and with the same genuine backend ID.
					replacement = &storeContextWire{nativeUIHTTP: d.http}
				} else {
					wantAuth = wallet.ErrAccountStorageRuntimeStopped
				}
				changed = make(chan struct{})
				read := make(chan bool, 1)
				go func(m *wallet.Manager) {
					defer close(changed)
					// Separate local API, while the same real reply is held.
					s, e := m.GetWalletCatalogSnapshot()
					read <- e == nil && s.CurrentWalletID == id && len(s.Wallets) == 1 && s.RootAccountID != ""
					if replacement != nil {
						m.SetDKVSHttpClient(replacement)
					} else {
						m.Stop()
					}
				}(d.mgr)
				require.True(t, awaitStoreEvent(t, read, "catalog available while read is held"))
				awaitStoreEvent(t, changed, "primary replacement or Manager.Stop")
				here, err := d.mgr.AccountIndexerLocation()
				require.NoError(t, err)
				require.Equal(t, location, here)
				require.NoError(t, caller.Err(), "not a caller cancellation")
			}
			g.open()
			awaitStoreEvent(t, query.done, "late quote")
			if branch == "CallerDeadline" {
				assert.ErrorIs(t, query.err, context.DeadlineExceeded)
			} else {
				assert.Error(t, query.err, "a changed source or stopped manager must discard the whole quote")
			}
			assert.Zero(t, len(query.options), "no live quote from the old caller, source or runtime")
			storeNoReadEffects(t, d, wantAuth)
			if replacement != nil {
				require.Equal(t, core.FreeLocal.MaxTTL, nativeUIStorage(t, d.mgr, "temporary", 100).TTLBlocks)
				assert.Positive(t, replacement.heights.Load(), "fresh quote used the installed primary")
				storeNoReadEffects(t, d, wantAuth)
			}
		})
	}

	t.Run("PolicySource", func(t *testing.T) {
		peer, err := dkvsClientForNode(t, n.Bootstrap).GetDKVSClientConfig()
		require.NoError(t, err)
		require.NotEmpty(t, peer.EndpointID)
		require.True(t, peer.EndpointID != core.EndpointID, "need independent real policy authorities")
		require.True(t, peer.FreeLocal.Enabled, "a disabled policy would not exercise the source boundary")
		require.Positive(t, peer.FreeLocal.MaxTTL)
		proxyTo := func(node *testHarness) *httputil.ReverseProxy {
			raw, err := node.IndexerURL("testnet")
			require.NoError(t, err)
			target, err := url.Parse(raw)
			require.NoError(t, err)
			target.Path = "" // keep the SDK's existing /testnet/... path
			return httputil.NewSingleHostReverseProxy(target)
		}
		a, b := proxyTo(n.Core), proxyTo(n.Bootstrap)
		var other, outage atomic.Bool
		var rejected, peerConfigs atomic.Int64
		b.ModifyResponse = func(r *http.Response) error {
			if r.StatusCode == http.StatusOK && strings.HasSuffix(r.Request.URL.Path, "/v3/dkvs/config") {
				peerConfigs.Add(1)
			}
			return nil
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if outage.Load() && r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/btc/block/bestblockheight") {
				rejected.Add(1)
				http.Error(w, "temporary height outage", http.StatusServiceUnavailable)
				return
			}
			if other.Load() {
				b.ServeHTTP(w, r)
			} else {
				a.ServeHTTP(w, r)
			}
		}))
		t.Cleanup(server.Close)
		addr, err := url.Parse(server.URL)
		require.NoError(t, err)
		cfg, _ := accountReviewConfig(t, n)
		cfg.IndexerL2.Scheme, cfg.IndexerL2.Host = addr.Scheme, addr.Host
		d := newNativeUIDevice(t, cfg, "")
		w := &storeContextWire{nativeUIHTTP: d.http}
		d.mgr.SetDKVSHttpClient(w)
		_, err = d.mgr.ImportWallet(accountSyncMnemonic(t, 247), d.password)
		require.NoError(t, err)
		requireE2EInactive(t, d.mgr, d.password)
		baseline := func(t *testing.T) {
			t.Helper()
			s, err := d.mgr.GetDKVSSubscriptionStatus()
			require.NoError(t, err)
			require.True(t, s.EndpointID == core.EndpointID, "confirmed subscription must stay attached to core")
		}
		baseline(t)
		rawExpiry := func(t *testing.T) uint64 {
			t.Helper()
			raw, err := d.mgr.GetAccountStorageOptions()
			require.NoError(t, err)
			for _, o := range raw {
				if o.ID == "temporary" {
					require.True(t, o.Available)
					require.Equal(t, core.FreeLocal.MaxTTL, o.TTLBlocks)
					require.Positive(t, o.EstimatedExpiryHeight)
					return o.EstimatedExpiryHeight
				}
			}
			t.Fatal("raw temporary plan missing")
			return 0
		}
		before := w.heights.Load()
		expiry := rawExpiry(t) // only the legacy API is expected to retain a verified height
		require.Greater(t, w.heights.Load(), before)
		outage.Store(true)
		t.Run("SameSourceHeight503", func(t *testing.T) {
			hits := rejected.Load()
			assert.Equal(t, expiry, rawExpiry(t), "legacy same-source estimate retains its verified height")
			require.Greater(t, rejected.Load(), hits)
			hits = rejected.Load()
			// The UI displays TTL only, not a height or an expiry estimate.
			assert.Equal(t, core.FreeLocal.MaxTTL, nativeUIStorage(t, d.mgr, "temporary", 100).TTLBlocks)
			require.Greater(t, rejected.Load(), hits, "real 503 on the UI read")
			baseline(t)
			storeNoReadEffects(t, d, wallet.ErrAccountStorageAuthorizationMissing)
		})
		t.Run("DifferentSource", func(t *testing.T) {
			other.Store(true) // no fabricated endpoint ID, config, height or replacement of the SDK primary
			defer other.Store(false)
			opts, err := d.mgr.GetStorageOptions(context.Background(), 100)
			require.Positive(t, peerConfigs.Load(), "real peer returned its config while height is unavailable")
			assert.ErrorIs(t, err, dkvs.ErrEndpointMismatch, "the real peer is not the confirmed authority")
			assert.Zero(t, len(opts), "reject the entire quote, including paid options")
			baseline(t)
			storeNoReadEffects(t, d, wallet.ErrAccountStorageAuthorizationMissing)
		})
	})
}
