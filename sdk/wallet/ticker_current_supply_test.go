package wallet

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/stretchr/testify/require"
)

type currentSupplyIndexer struct {
	IndexerRPCClient
	info  *indexer.TickerInfo
	reads int
}

func (c *currentSupplyIndexer) GetTickInfo(_ *indexer.AssetName) *indexer.TickerInfo {
	c.reads++
	if c.info == nil {
		return nil
	}
	copy := *c.info
	return &copy
}

func TestTickerMintRefreshUsesCacheAndHonorsCompletion(t *testing.T) {
	for _, protocol := range []string{"ordx", "brc20"} {
		t.Run(protocol, func(t *testing.T) {
			name := indexer.AssetName{Protocol: protocol, Type: indexer.ASSET_TYPE_FT, Ticker: "supply"}
			cached := &indexer.TickerInfo{AssetName: name, TotalMinted: "0", MaxSupply: "12000", N: 1}
			if protocol == "ordx" {
				// Unlimited ORDX may report its current total as MaxSupply.
				cached.MaxSupply = cached.TotalMinted
			}
			client := &currentSupplyIndexer{}
			manager := &Manager{db: newMemoryKVDB(), l1IndexerClient: &IndexerRPCClientMgr{active: client}}
			require.NoError(t, saveTickerInfo(manager.db, cached))
			require.Equal(t, "0", manager.GetTickerInfo(&name).TotalMinted)
			upper := name
			upper.Ticker = strings.ToUpper(name.Ticker)
			require.Equal(t, "0", manager.GetTickerInfo(&upper).TotalMinted)
			require.Zero(t, client.reads, "ordinary calls use disk, then memory, without RPC")

			current := *cached
			current.TotalMinted = "20"
			client.info = &current
			require.Equal(t, "20", manager.RefreshTickerMintInfo(&name).TotalMinted)
			require.Equal(t, "20", manager.RefreshTickerMintInfo(&upper).TotalMinted)
			require.Equal(t, 1, client.reads, "repeated explicit requests are rate limited")
			require.Equal(t, "20", manager.GetTickerInfo(&name).TotalMinted)

			expire := func() {
				manager.tickerMintRefresh[name.String()] = tickerMintRefresh{checkedAt: time.Now().Add(-2 * tickerMintRefreshInterval)}
			}
			expire()
			client.info = nil
			require.Nil(t, manager.RefreshTickerMintInfo(&name), "failed refresh must not return stale supply as fresh")
			require.Nil(t, manager.RefreshTickerMintInfo(&name))
			require.Equal(t, "20", manager.GetTickerInfo(&name).TotalMinted)
			require.Equal(t, 2, client.reads, "failed refreshes are also rate limited")

			expire()
			current.TotalMinted = "30"
			current.Status = indexer.TICKER_STATUS_MINT_COMPLETED
			client.info = &current
			require.Equal(t, "30", manager.RefreshTickerMintInfo(&name).TotalMinted)
			expire()
			require.Equal(t, "30", manager.RefreshTickerMintInfo(&name).TotalMinted)
			require.Equal(t, 3, client.reads, "the final total no longer needs refresh")
			stored, err := loadTickerInfo(manager.db, &name)
			require.NoError(t, err)
			require.Equal(t, "30", stored.TotalMinted)

			manager.RemoveAllTickerInfo()
			reorged := *cached
			reorged.TotalMinted = "10"
			client.info = &reorged
			require.Equal(t, "10", manager.RefreshTickerMintInfo(&name).TotalMinted)
			require.Equal(t, 4, client.reads, "clearing the cache also invalidates the completed snapshot")
		})
	}
	t.Run("rgb remains local", func(t *testing.T) {
		name := indexer.AssetName{Protocol: "rgb11", Type: indexer.ASSET_TYPE_FT, Ticker: "local@cccc"}
		id := strings.Repeat("c", 64)
		content, err := json.Marshal(rgb11wallet.TickerExt{AssetName: name, ContractID: id, Ticker: "LOCAL"})
		require.NoError(t, err)
		info := &indexer.TickerInfo{AssetName: name, ContractID: id, TotalMinted: "1000", Content: content}
		client := &currentSupplyIndexer{}
		manager := &Manager{db: newMemoryKVDB(), tickerInfoMap: map[string]*indexer.TickerInfo{name.String(): info}, l1IndexerClient: &IndexerRPCClientMgr{active: client}}
		require.NoError(t, saveTickerInfo(manager.db, info))
		got := manager.RefreshTickerMintInfo(&name)
		require.NotNil(t, got)
		require.Equal(t, name, got.AssetName)
		require.Equal(t, id, got.ContractID)
		require.Equal(t, "1000", got.TotalMinted)
		require.Zero(t, client.reads)
		require.NotNil(t, manager.RefreshTickerMintInfo(&ASSET_PLAIN_SAT))
		require.Zero(t, client.reads)
	})
}

func TestTickerInfoReusesRuneIDCache(t *testing.T) {
	name := indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_RUNES, Type: indexer.ASSET_TYPE_FT, Ticker: "CACHE•RUNE"}
	id := name
	id.Ticker = "840000:1"
	for _, first := range []indexer.AssetName{name, id} {
		t.Run(first.Ticker, func(t *testing.T) {
			client := &currentSupplyIndexer{info: &indexer.TickerInfo{AssetName: name, DisplayName: id.Ticker}}
			manager := &Manager{db: newMemoryKVDB(), l1IndexerClient: &IndexerRPCClientMgr{active: client}}
			got := manager.GetTickerInfo(&first)
			require.NotNil(t, got)
			require.Equal(t, name, got.AssetName)
			require.Same(t, got, manager.GetTickerInfo(&id))
			require.Same(t, got, manager.GetTickerInfoFromRuneId(id.Ticker))
			require.Same(t, got, manager.GetTickerInfo(&name))
			require.Equal(t, 1, client.reads)
		})
	}
}
