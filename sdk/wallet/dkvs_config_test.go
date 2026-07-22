package wallet

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSatsNetDKVSClientGetConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.True(t, strings.HasSuffix(r.URL.Path, "/v3/dkvs/config"), r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("content-type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"msg":  "ok",
			"data": map[string]any{
				"enabled":                true,
				"max_ttl_ms":             60000,
				"max_records_per_signer": 10,
				"max_bytes_per_signer":   4096,
				"max_total_records":      100,
				"max_total_bytes":        65536,
			},
		}))
	}))
	defer server.Close()

	client := NewSatsNetDKVSClient("http", server.Listener.Addr().String(), "", nil)
	policy, err := client.GetConfig()
	require.NoError(t, err)
	require.True(t, policy.Enabled)
	require.Equal(t, uint64(60000), policy.MaxTTL)
	require.Equal(t, uint64(10), policy.MaxRecordsPerSigner)
}
