package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	core "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// A real node commits the PWA rename, then the HTTP connection closes halfway
// through its response body. Use the production NetClient, not an injected
// error value: native HTTP and browser Fetch both report body-read failures.
func TestSDKHTTPBodyLoss(t *testing.T) {
	network := newDKVSNoPluginUnfundedNetwork(t)
	waitForDKVSPeerReady(t, network)
	f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 214), true)
	f.activate(t)
	target, err := url.Parse("http://" + f.location.Host)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(target)
	var armed atomic.Bool
	var lost atomic.Bool
	var mu sync.Mutex
	var requests []core.BatchCASRequest
	proxy.ModifyResponse = func(response *http.Response) error {
		if response.Request.Header.Get("X-E2E-Account-State") != "1" ||
			response.StatusCode != http.StatusOK || !armed.Swap(false) {
			return nil
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			return err
		}
		// Keep the declared full length; returning only half forces the actual
		// client response reader to see the premature connection close.
		response.Body = io.NopCloser(bytes.NewReader(body[:len(body)/2]))
		response.ContentLength = int64(len(body))
		response.Header.Set("Content-Length", strconv.Itoa(len(body)))
		lost.Store(true)
		return nil
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/records/batch-cas") {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "read request", http.StatusBadRequest)
				return
			}
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			var batch core.BatchCASRequest
			if json.Unmarshal(body, &batch) == nil {
				for _, mutation := range batch.Mutations {
					if mutation.Record != nil && strings.HasSuffix(mutation.Record.Key, "/account/state") {
						r.Header.Set("X-E2E-Account-State", "1")
						mu.Lock()
						requests = append(requests, batch)
						mu.Unlock()
						break
					}
				}
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	defer server.Close()
	proxyURL, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := wallet.NewHTTPClient().(*wallet.NetClient)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client.Client.Transport = transport
	f.manager.SetDKVSHttpClient(client)
	require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
	armed.Store(true)
	require.NoError(t, f.manager.UpdateWalletName(f.manager.GetAccountManagementStatus().RootWalletID,
		"Rename across interrupted response"))
	require.Eventually(t, lost.Load, 10*time.Second, 20*time.Millisecond, "the real HTTP response must be truncated")
	if !waitBackupRecovery(t, f.manager) {
		return
	}
	mu.Lock()
	captured := append([]core.BatchCASRequest(nil), requests...)
	mu.Unlock()
	require.GreaterOrEqual(t, len(captured), 2, "unknown commit outcome must replay the durable request")
	original, retry := captured[0], captured[1]
	require.Equal(t, original.RequestID, retry.RequestID)
	require.Equal(t, original.EndpointID, retry.EndpointID)
	require.Equal(t, original.Authorization, retry.Authorization)
	require.Len(t, retry.Mutations, len(original.Mutations))
	for i, mutation := range original.Mutations {
		require.Equal(t, dkvs.RecordHash(mutation.Record), dkvs.RecordHash(retry.Mutations[i].Record))
		require.Equal(t, mutation.ExpectedETag, retry.Mutations[i].ExpectedETag)
		require.Equal(t, mutation.ExpectAbsent, retry.Mutations[i].ExpectAbsent)
		committed := waitForDKVSRecord(t, network.Core, mutation.Record.Key)
		require.Equal(t, dkvs.RecordHash(mutation.Record), dkvs.RecordHash(committed),
			"replay must not create another server revision")
	}
}

func waitBackupRecovery(t *testing.T, manager *wallet.Manager) bool {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		status := manager.GetAccountManagementStatus()
		if status.BackupState == "backed_up" && !status.ManagedDataDirty {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	status := manager.GetAccountManagementStatus()
	t.Errorf("backup must recover: backup=%s dirty=%t error=%s", status.BackupState,
		status.ManagedDataDirty, status.LastDKVSSyncError)
	return false
}
