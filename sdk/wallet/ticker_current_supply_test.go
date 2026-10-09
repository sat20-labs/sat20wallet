package wallet

import (
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
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

func TestPublicTickerQueryReadsCurrentSupplyWithoutChangingLocalMetadata(t *testing.T) {
	for _, protocol := range []string{"ordx", "brc20"} {
		t.Run(protocol, func(t *testing.T) {
			name := indexer.AssetName{Protocol: protocol, Type: indexer.ASSET_TYPE_FT, Ticker: "supply"}
			cached := &indexer.TickerInfo{AssetName: name, TotalMinted: "0", MaxSupply: "12000", N: 1}
			client := &currentSupplyIndexer{info: cached}
			manager := &Manager{db: newMemoryKVDB(), tickerInfoMap: map[string]*indexer.TickerInfo{name.String(): cached}, l1IndexerClient: &IndexerRPCClientMgr{active: client}}
			require.NoError(t, saveTickerInfo(manager.db, cached))
			require.Equal(t, "0", manager.GetTickerInfoV2(name.String()).TotalMinted)
			current := *cached
			current.TotalMinted = "20"
			client.info = &current
			require.Equal(t, "20", manager.GetTickerInfoV2(name.String()).TotalMinted)
			require.Equal(t, 2, client.reads)
			require.Equal(t, "0", cached.TotalMinted)
			stored, err := loadTickerInfo(manager.db, &name)
			require.NoError(t, err)
			require.Equal(t, "0", stored.TotalMinted)
			client.info = nil
			require.Nil(t, manager.GetTickerInfoV2(name.String()), "unavailable current supply must not be presented as cached current data")
		})
	}
	t.Run("rgb remains local", func(t *testing.T) {
		name := indexer.AssetName{Protocol: "rgb11", Type: indexer.ASSET_TYPE_FT, Ticker: "local"}
		info := &indexer.TickerInfo{AssetName: name, TotalMinted: "1000"}
		client := &currentSupplyIndexer{}
		manager := &Manager{db: newMemoryKVDB(), tickerInfoMap: map[string]*indexer.TickerInfo{name.String(): info}, l1IndexerClient: &IndexerRPCClientMgr{active: client}}
		require.Same(t, info, manager.GetTickerInfoV2(name.String()))
		require.Zero(t, client.reads)
		require.NotNil(t, manager.GetTickerInfoV2("::"))
		require.Zero(t, client.reads)
	})
}
