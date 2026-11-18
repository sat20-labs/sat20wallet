package e2e

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSDKStorageHeaderLoss(t *testing.T) {
	n := newDKVSNoPluginUnfundedNetwork(t)
	waitForDKVSPeerReady(t, n)
	core, err := dkvsClientForNode(t, n.Core).GetDKVSClientConfig()
	require.NoError(t, err)
	require.NotEmpty(t, core.EndpointID)
	require.True(t, core.FreeLocal.Enabled)
	require.Positive(t, core.FreeLocal.MaxTTL)
	cfg, _ := accountReviewConfig(t, n)
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: cfg.IndexerL2.Scheme, Host: cfg.IndexerL2.Host})
	var configs, mode atomic.Int64
	proxy.ModifyResponse = func(r *http.Response) error {
		if r.StatusCode == http.StatusOK && strings.HasSuffix(r.Request.URL.Path, "/v3/dkvs/config") {
			configs.Add(1)
		}
		return nil
	}
	const beforeHeaders, hold, clientTimer = -1, -2, -3
	block, timed := newStoreReplyGate(), newStoreReplyGate()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/btc/block/bestblockheight") {
			switch m := mode.Load(); m {
			case hold, clientTimer:
				g := block
				if m == clientTimer {
					g = timed
				}
				g.seen <- storeWireReply{ctx: r.Context()}
				select {
				case <-r.Context().Done():
				case <-g.release:
				}
				fallthrough
			case beforeHeaders:
				// Standard net/http abort: no status, body or headers sent.
				panic(http.ErrAbortHandler)
			case 401, 403, 404:
				http.Error(w, http.StatusText(int(m)), int(m))
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(block.open)
	t.Cleanup(timed.open)
	addr, err := url.Parse(server.URL)
	require.NoError(t, err)
	cfg.IndexerL2.Scheme, cfg.IndexerL2.Host = addr.Scheme, addr.Host
	d := newNativeUIDevice(t, cfg, "")
	wire := &storeContextWire{nativeUIHTTP: d.http}
	d.mgr.SetDKVSHttpClient(wire)
	_, err = d.mgr.ImportWallet(accountSyncMnemonic(t, 248), d.password)
	require.NoError(t, err)
	requireE2EInactive(t, d.mgr, d.password)
	s, err := d.mgr.GetDKVSSubscriptionStatus()
	require.NoError(t, err)
	require.True(t, s.EndpointID == core.EndpointID)
	require.Equal(t, core.FreeLocal.MaxTTL, nativeUIStorage(t, d.mgr, "temporary", 100).TTLBlocks)

	for _, m := range []int{beforeHeaders, 401, 403, 404, hold, clientTimer} {
		name := strconv.Itoa(m)
		switch m {
		case beforeHeaders:
			name = "GETBeforeHeadersEOF"
		case hold:
			name = "CallerCancelsBeforeHeaders"
		case clientTimer:
			name = "ClientTimeoutBeforeHeaders"
		}
		t.Run(name, func(t *testing.T) {
			active := wire
			var caller context.Context
			var cancel context.CancelFunc
			if m == clientTimer {
				// A private native client, not a mutation of the shared or caller
				// timeout. Confirm its actual primary while height is still live.
				short := &http.Client{Transport: server.Client().Transport, Timeout: time.Second}
				active = &storeContextWire{nativeUIHTTP: &nativeUIHTTP{inner: &wallet.NetClient{Client: short}}}
				d.mgr.SetDKVSHttpClient(active)
				t.Cleanup(func() { d.mgr.SetDKVSHttpClient(wire) })
				require.Equal(t, core.FreeLocal.MaxTTL, nativeUIStorage(t, d.mgr, "temporary", 100).TTLBlocks)
				require.Positive(t, active.heights.Load())
				caller, cancel = context.WithTimeout(context.Background(), 15*time.Second)
			} else {
				caller, cancel = context.WithCancel(context.Background())
			}
			mode.Store(int64(m))
			defer mode.Store(0)
			g := newStoreReplyGate()
			g.open() // observe this request's actual NetClient error, without delaying it
			active.height.Store(g)
			before := configs.Load()
			var q storeQuery
			q.start(caller, d.mgr)
			t.Cleanup(func() {
				cancel()
				if m == hold {
					block.open()
				} else if m == clientTimer {
					timed.open()
				}
				awaitStoreEvent(t, q.done, "finished before teardown")
			})
			if m == hold {
				req := awaitStoreEvent(t, block.seen, "height GET waiting, no headers sent")
				cancel()
				awaitStoreEvent(t, req.ctx.Done(), "request cancellation reached the server")
			} else if m == clientTimer {
				req := awaitStoreEvent(t, timed.seen, "height GET entered on the private client")
				awaitStoreEvent(t, req.ctx.Done(), "native client timeout reached the server")
			}
			awaitStoreEvent(t, q.done, "storage preview completed")
			actual := awaitStoreEvent(t, g.seen, "same SDK height read returned")
			require.Greater(t, configs.Load(), before, "fresh policy came from the real current core")
			storeNoReadEffects(t, d, wallet.ErrAccountStorageAuthorizationMissing)
			switch m {
			case beforeHeaders, clientTimer:
				var u *url.Error
				require.ErrorAs(t, actual.err, &u)
				require.True(t, strings.EqualFold(u.Op, http.MethodGet))
				if m == clientTimer {
					require.True(t, u.Timeout())
					require.ErrorIs(t, u.Err, context.DeadlineExceeded)
					require.NoError(t, caller.Err(), "only the shorter HTTP timeout expired")
					assert.Zero(t, active.broadcasts.Load())
				} else {
					require.ErrorIs(t, u.Err, io.EOF)
				}
				assert.NoError(t, q.err, "a transport failure is not a policy refusal")
				var offered bool
				for _, o := range q.options {
					offered = offered || o.ID == "temporary" && o.Available && o.TTLBlocks == core.FreeLocal.MaxTTL
				}
				assert.True(t, offered, "retain only the verified current node's TTL")
			case hold:
				assert.ErrorIs(t, actual.err, context.Canceled)
				assert.ErrorIs(t, q.err, context.Canceled)
				assert.Zero(t, len(q.options))
			default:
				var refusal *wallet.HTTPResponseError
				require.ErrorAs(t, actual.err, &refusal)
				require.Equal(t, m, refusal.StatusCode)
				assert.Error(t, q.err, "an authoritative refusal is not a transport outage")
				assert.Zero(t, len(q.options))
			}
		})
	}
}
