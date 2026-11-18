package e2e

import (
	"encoding/json"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Reuses the real Bitcoin controller and byte-preserving RGB relay. Neither
// service supplies fake wallet, indexer, proof validation or contract state.
func setupSDKUITransport(t *testing.T, fixture map[string]any, configPath string, cases []sdkUICase) {
	t.Helper()
	needsL1 := false
	for _, item := range cases {
		if strings.Contains(item.journey, "RGB") || strings.Contains(item.journey, "Direct") || strings.Contains(item.journey, "DID") {
			needsL1 = true
		}
	}
	if !needsL1 {
		return
	}
	var cfg sdkcommon.Config
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &cfg))
	require.Equal(t, "regtest", cfg.Chain)
	client := wallet.NewIndexerClient(cfg.IndexerL1.Scheme, cfg.IndexerL1.Host, cfg.IndexerL1.Proxy, &wallet.NetClient{Client: &http.Client{Timeout: 10 * time.Second}})
	address, ok := fixture["miningAddress"].(string)
	require.True(t, ok)
	require.True(t, strings.HasPrefix(address, "bcrt1"))
	mine := func(address string, blocks int) {
		realRegtestBitcoinCLI(t, "generatetoaddress", strconv.Itoa(blocks), address)
		var height int64
		require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getblockcount"), &height))
		require.Eventually(t, func() bool { return client.GetBestHeight() == height }, rpcReadyTimeout, 200*time.Millisecond, "real L1 indexer must confirm the mined block")
	}
	fixture["control_url"] = startSDKPWARegtestBitcoinController(t, address, mine)
	fixture["realBitcoinControl"] = true
	relay := newRGB11RegtestProxy()
	if statePath, ok := fixture["uiProxyState"].(string); ok {
		data, err := os.ReadFile(statePath)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(data, &relay.objects))
	}
	// Preserve actual SDK-produced relay bytes when a journey fails. A resume
	// reuses these bytes; it never invents a consignment or recipient ACK.
	t.Cleanup(func() {
		relay.mu.Lock()
		defer relay.mu.Unlock()
		data, err := json.Marshal(relay.objects)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(os.Getenv("SAT20_REAL_REGTEST_RUNTIME"), "rgb-proxy.private.json"), data, 0600))
	})
	server := httptest.NewUnstartedServer(http.HandlerFunc(relay.serveBrowserHTTP))
	if address, ok := fixture["uiProxyAddress"].(string); ok {
		host, _, err := net.SplitHostPort(address)
		require.NoError(t, err)
		require.Equal(t, "127.0.0.1", host)
		require.NoError(t, server.Listener.Close())
		server.Listener, err = net.Listen("tcp", address)
		require.NoError(t, err)
	}
	server.Start()
	t.Cleanup(server.Close)
	fixture["rgbProxy"] = "rpc://" + strings.TrimPrefix(server.URL, "http://") + "/0.2/json-rpc"
}

func (p *rgb11RegtestProxy) serveBrowserHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	p.serveHTTP(w, r)
}

func startPwaRGBProxy(t *testing.T) string {
	t.Helper()
	relay := newRGB11RegtestProxy()
	server := httptest.NewServer(http.HandlerFunc(relay.serveBrowserHTTP))
	t.Cleanup(server.Close)
	return "rpc://" + strings.TrimPrefix(server.URL, "http://") + "/0.2/json-rpc"
}
