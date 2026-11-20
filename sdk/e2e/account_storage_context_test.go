package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type storeWireReply struct {
	ctx context.Context
	err error
}

// Each barrier holds exactly one real response after the loopback node has
// answered, not a replacement configuration, height or synchronization result.
type storeReplyGate struct {
	seen    chan storeWireReply
	release chan struct{}
	once    sync.Once
}

func newStoreReplyGate() *storeReplyGate {
	return &storeReplyGate{seen: make(chan storeWireReply, 1), release: make(chan struct{})}
}

func (g *storeReplyGate) open() { g.once.Do(func() { close(g.release) }) }

type storeContextWire struct {
	*nativeUIHTTP
	config  atomic.Pointer[storeReplyGate]
	height  atomic.Pointer[storeReplyGate]
	heights atomic.Uint64
}

func (w *storeContextWire) SendGetRequestContext(ctx context.Context, u *wallet.URL) ([]byte, error) {
	var g *storeReplyGate
	height := strings.HasSuffix(u.Path, "/btc/block/bestblockheight")
	if height {
		g = w.height.Swap(nil)
	} else if strings.HasSuffix(u.Path, "/v3/dkvs/config") {
		g = w.config.Swap(nil)
	}
	raw, err := w.nativeUIHTTP.SendGetRequestContext(ctx, u)
	if g != nil {
		g.seen <- storeWireReply{ctx, err}
		if err == nil {
			select {
			case <-ctx.Done():
			case <-g.release:
			}
			err = ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	if height {
		var rsp struct {
			Code *int   `json:"code"`
			Data *int64 `json:"data"`
		}
		if json.Unmarshal(raw, &rsp) == nil && rsp.Code != nil && *rsp.Code == 0 && rsp.Data != nil && *rsp.Data >= 0 {
			w.heights.Add(1)
		}
	}
	return raw, nil
}

func awaitStoreEvent[T any](t *testing.T, event <-chan T, stage string) T {
	t.Helper()
	select {
	case value := <-event:
		return value
	case <-time.After(10 * time.Second):
		t.Fatalf("no storage event: %s", stage)
		var zero T
		return zero
	}
}

type storeQuery struct {
	done    chan struct{}
	options []wallet.StorageOption
	err     error
}

func (q *storeQuery) start(ctx context.Context, m *wallet.Manager) {
	q.done = make(chan struct{})
	go func() {
		defer close(q.done)
		q.options, q.err = m.GetStorageOptions(ctx, 100)
	}()
}

func TestSDKStorageContext(t *testing.T) {
	network := newDKVSNoPluginUnfundedNetwork(t)
	waitForDKVSPeerReady(t, network)
	cfg, _ := accountReviewConfig(t, network)
	policy, err := dkvsClientForNode(t, network.Core).GetFreeLocalCachePolicy()
	require.NoError(t, err)
	require.True(t, policy.Enabled)
	require.Positive(t, policy.MaxTTL)

	for _, branch := range []string{"DiscoveryFinishes", "CallerCancelsHeight"} {
		t.Run(branch, func(t *testing.T) {
			d := newNativeUIDevice(t, cfg, "")
			w := &storeContextWire{nativeUIHTTP: d.http}
			d.mgr.SetDKVSHttpClient(w)
			seed := byte(242)
			if branch == "CallerCancelsHeight" {
				seed = 243
			}
			_, err := d.mgr.ImportWallet(accountSyncMnemonic(t, seed), d.password)
			require.NoError(t, err)
			discovery, foreground := newStoreReplyGate(), newStoreReplyGate()
			caller, cancel := context.WithCancel(context.Background())
			var query storeQuery
			t.Cleanup(func() {
				cancel()
				discovery.open()
				foreground.open()
				if query.done != nil {
					awaitStoreEvent(t, query.done, "query finished before device teardown")
				}
			})

			if branch == "DiscoveryFinishes" {
				// Hold discovery until the foreground quote has reached the wire.
				w.config.Store(discovery)
				_, err := d.mgr.RefreshAccount(context.Background())
				require.NoError(t, err)
				background := awaitStoreEvent(t, discovery.seen, "background config from real core")
				require.NoError(t, background.err)
				// The synchronous policy read uses the worker context directly.
				// Discovery's config request has a shorter, per-request context.
				probe := newStoreReplyGate()
				probe.open()
				w.config.Store(probe)
				_, err = d.mgr.GetAccountStorageOptions()
				require.NoError(t, err)
				worker := awaitStoreEvent(t, probe.seen, "worker context from policy read")
				require.NoError(t, worker.err)
				require.NoError(t, worker.ctx.Err())
				w.config.Store(foreground)
				query.start(caller, d.mgr)
				first := awaitStoreEvent(t, foreground.seen, "foreground config from real core")
				require.NoError(t, first.err)
				discovery.open()
				// Observe the discovery lifetime ending naturally. Never cancel
				// the worker, stop the manager, or start another discovery.
				awaitStoreEvent(t, worker.ctx.Done(), "normal completion of discovery")
				require.ErrorIs(t, worker.ctx.Err(), context.Canceled)
				require.NoError(t, first.ctx.Err())
				status := d.mgr.GetAccountManagementStatus()
				require.Equal(t, "inactive", status.DiscoveryStatus)
				require.True(t, status.LocalOnly)
				require.Empty(t, status.LastDKVSSyncErrorCode)
				require.NoError(t, caller.Err())
				before := w.heights.Load()
				foreground.open()
				awaitStoreEvent(t, query.done, "foreground quote after discovery")
				assert.NoError(t, query.err)
				var ready bool
				for _, o := range query.options {
					if o.ID == "temporary" {
						ready = o.Available && o.TTLBlocks == policy.MaxTTL
					}
				}
				assert.True(t, ready, "discovery cleanup must not hide the real temporary storage policy")
				assert.Greater(t, w.heights.Load(), before, "confirm height through the live endpoint")
			} else {
				requireE2EInactive(t, d.mgr, d.password)
				before := w.heights.Load()
				warm, err := d.mgr.GetStorageOptions(context.Background(), 100)
				require.NoError(t, err)
				require.Greater(t, w.heights.Load(), before, "prime a genuine height from this authority")
				var paidAvailable bool
				for _, o := range warm {
					paidAvailable = paidAvailable || o.ID == "paid" && o.Available
				}
				require.True(t, paidAvailable, "uncanceled control offers the test network's paid plan")
				w.height.Store(foreground)
				query.start(caller, d.mgr)
				height := awaitStoreEvent(t, foreground.seen, "foreground real height response")
				require.NoError(t, height.err)
				cancel()
				// A caller cancellation must interrupt a held read. If not,
				// release its real response to also check the SDK's returned
				// error and options; a cached height must not mask cancellation.
				select {
				case <-height.ctx.Done():
					assert.ErrorIs(t, height.ctx.Err(), context.Canceled)
				case <-time.After(5 * time.Second):
					t.Error("caller cancellation did not interrupt the foreground height read")
				}
				foreground.open()
				awaitStoreEvent(t, query.done, "caller-canceled query")
				assert.ErrorIs(t, query.err, context.Canceled)
				for _, o := range query.options {
					assert.False(t, o.ID == "paid" && o.Available, "a canceled quote must not offer a payable plan")
				}
			}
			auth, err := d.mgr.PendingAccountStorageAuthorization()
			assert.ErrorIs(t, err, wallet.ErrAccountStorageAuthorizationMissing)
			assert.True(t, auth == nil, "no storage authorization was issued")
			assert.Zero(t, d.http.broadcasts.Load(), "getting options must not broadcast")
		})
	}
}
