package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
)

// Hold only account backup submissions. Discovery/current-set reads retain
// their real transport, so the PWA's local commit precedes a backup failure.
type accountStateHold struct {
	committed bool
	entered   chan struct{}
	release   chan struct{}
	once      sync.Once
}

func newAccountStateHold(committed bool) *accountStateHold {
	return &accountStateHold{committed: committed, entered: make(chan struct{}), release: make(chan struct{})}
}
func (h *accountStateHold) unblock() { h.once.Do(func() { close(h.release) }) }

type accountBackupHTTP struct {
	inner   wallet.HttpClient
	offline atomic.Bool
	armed   atomic.Bool
	mu      sync.Mutex
	holds   []*accountStateHold
}

var _ wallet.ContextHttpClient = (*accountBackupHTTP)(nil)

func (h *accountBackupHTTP) SendGetRequest(u *wallet.URL) ([]byte, error) {
	return h.SendGetRequestContext(context.Background(), u)
}

func (h *accountBackupHTTP) SendGetRequestContext(ctx context.Context, u *wallet.URL) ([]byte, error) {
	if h.offline.Load() {
		return nil, &net.DNSError{Err: "controlled offline account transport", IsTimeout: true}
	}
	inner, ok := h.inner.(wallet.ContextHttpClient)
	if !ok {
		return nil, errors.New("account backup test requires a context-aware inner client")
	}
	return inner.SendGetRequestContext(ctx, u)
}
func (h *accountBackupHTTP) SendPostRequest(u *wallet.URL, body []byte) ([]byte, error) {
	return h.SendPostRequestContext(context.Background(), u, body)
}

// Preserve the real worker's wake/deadline cancellation on active long polls.
// Offline faults and both before/after-server hold boundaries stay unchanged.
func (h *accountBackupHTTP) SendPostRequestContext(ctx context.Context, u *wallet.URL, body []byte) ([]byte, error) {
	if h.offline.Load() {
		return nil, &net.DNSError{Err: "controlled offline account transport", IsTimeout: true}
	}
	inner, ok := h.inner.(wallet.ContextHttpClient)
	if !ok {
		return nil, errors.New("account backup test requires a context-aware inner client")
	}
	var hold *accountStateHold
	if h.armed.Load() && strings.HasSuffix(u.Path, "/records/batch-cas") {
		var batch struct {
			Mutations []struct{ Record struct{ Key string } }
		}
		if json.Unmarshal(body, &batch) == nil {
			for _, m := range batch.Mutations {
				if strings.HasSuffix(m.Record.Key, "/account/state") {
					h.mu.Lock()
					if len(h.holds) > 0 {
						hold = h.holds[0]
						h.holds = h.holds[1:]
					}
					h.mu.Unlock()
					break
				}
			}
		}
	}
	if hold == nil {
		return inner.SendPostRequestContext(ctx, u, body)
	}
	var raw []byte
	var err error
	if hold.committed {
		raw, err = inner.SendPostRequestContext(ctx, u, body)
	}
	close(hold.entered)
	select {
	case <-hold.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(20 * time.Second):
		return nil, context.DeadlineExceeded
	}
	if h.offline.Load() {
		return nil, &net.DNSError{Err: "controlled backup failure after local commit", IsTimeout: true}
	}
	if hold.committed {
		return raw, err
	}
	return inner.SendPostRequestContext(ctx, u, body)
}
