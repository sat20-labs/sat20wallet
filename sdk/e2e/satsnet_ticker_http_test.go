package e2e

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	btc "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	l2 "github.com/sat20-labs/satoshinet/indexer/common"
	routes "github.com/sat20-labs/satoshinet/indexer/rpcserver/indexer"
	share "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

type tickerHTTPIndex struct {
	share.Indexer
	items map[btc.AssetName]*l2.TickerInfo
}

func (i tickerHTTPIndex) GetTickerInfo(name *btc.AssetName) *l2.TickerInfo {
	return i.items[*name]
}

// This fixture checks ticker serialization without optional RGB registry metadata.
func (tickerHTTPIndex) GetDKVSRecord(string) (*swire.DKVSRecord, error) {
	return nil, btc.ErrKeyNotFound
}

// Use the actual SatoshiNet handler and wallet decoder: returning a Go ticker
// from an in-memory client bypasses the HTTP contract that broke RGB withdrawal.
func TestSatsNetTickerHTTP(t *testing.T) {
	rgb := btc.AssetName{Protocol: "rgb11", Type: "f", Ticker: "ticker@abab"}
	limit, err := btc.NewDecimalFromString("1000.25", 2)
	require.NoError(t, err)
	items := map[btc.AssetName]*l2.TickerInfo{
		rgb:                 {AssetName: rgb, MaxSupply: limit, Divisibility: 2},
		btc.ASSET_PLAIN_SAT: {AssetName: btc.ASSET_PLAIN_SAT, N: 1, MaxSupply: btc.NewDefaultDecimal(21000000000000000)},
	}
	router := gin.New()
	routes.NewService(tickerHTTPIndex{items: items}).InitRouter(router, "/regtest")
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	client := wallet.NewIndexerClient("http", strings.TrimPrefix(server.URL, "http://"), "regtest", &wallet.NetClient{Client: server.Client()})
	for name, want := range items {
		got := client.GetTickInfo(&name)
		require.NotNil(t, got, "wallet cannot read L2 ticker %s", name.String())
		require.Equal(t, name, got.AssetName)
		require.Equal(t, want.MaxSupply.String(), got.MaxSupply)
		require.Equal(t, want.Divisibility, got.Divisibility)
		require.Equal(t, want.N, got.N)
	}
}
