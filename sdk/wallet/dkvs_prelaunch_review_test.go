package wallet

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/assert"
)

// A complete read includes the configuration lookup preceding its pages. Use
// real HTTP and the production NetClient, with no chain or payment fixtures.
// The blocked handler is always released, including when an assertion fails.
func TestDKVSPrelaunchReadContextIncludesConfiguration(t *testing.T) {
	for _, operation := range []string{"directory-deadline", "active-sync-cancellation"} {
		t.Run(operation, func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			var enteredOnce, releaseOnce sync.Once
			releaseRequest := func() { releaseOnce.Do(func() { close(release) }) }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/v3/dkvs/config") {
					t.Errorf("unexpected request before configuration completed: %s", r.URL.Path)
					http.Error(w, "unexpected request", http.StatusBadRequest)
					return
				}
				enteredOnce.Do(func() { close(entered) })
				select {
				case <-r.Context().Done():
					return
				case <-release:
					http.Error(w, "released blocked config", http.StatusServiceUnavailable)
				}
			}))
			t.Cleanup(server.Close)
			t.Cleanup(releaseRequest)
			client := NewSatsNetDKVSClient("http", strings.TrimPrefix(server.URL, "http://"), "",
				&NetClient{Client: server.Client()})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			started := time.Now()
			go func() {
				if operation == "directory-deadline" {
					_, _, err := client.ListRecords("/svc/review", 0, 0)
					done <- err
					return
				}
				_, err := client.SyncActiveScope(ctx, core.NewReplicaStore(newMemoryKVDB()),
					"prelaunch-context", dkvs.ActiveScope{Prefix: "/svc/review"}, true)
				done <- err
			}()
			select {
			case <-entered:
			case err := <-done:
				t.Fatalf("request returned before reaching the configuration handler: %v", err)
			case <-time.After(2 * time.Second):
				t.Fatal("configuration request did not reach the HTTP handler")
			}
			wait := dkvsUnmanagedReadTimeout + time.Second
			wanted := error(context.DeadlineExceeded)
			if operation == "active-sync-cancellation" {
				cancel()
				wait = 500 * time.Millisecond
				wanted = context.Canceled
			}
			var result error
			select {
			case result = <-done:
			case <-time.After(wait):
				t.Errorf("configuration HTTP request ignored the operation's context; elapsed=%s", time.Since(started))
				releaseRequest()
				select {
				case result = <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("request did not finish after the blocked handler was released")
				}
			}
			t.Logf("operation=%s elapsed=%s result=%v", operation, time.Since(started), result)
			assert.ErrorIs(t, result, wanted, "the caller's deadline/cancellation must cover config and all subsequent pages")
		})
	}
}
