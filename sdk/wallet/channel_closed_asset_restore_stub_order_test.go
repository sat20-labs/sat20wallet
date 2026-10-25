package wallet

import (
	"context"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
)

// A deterministic plain-first indexer response must not let automatic BTC
// expansion adopt the exact existing stubs still needed by later assets.
// Explicit/public expansion policy and its original order are not changed.
func TestClosedAssetRestorePlainCannotConsumeFutureAssetStubs(t *testing.T) {
	for _, protocol := range []string{indexer.PROTOCOL_NAME_ORDX, indexer.PROTOCOL_NAME_RUNES} {
		t.Run(protocol, func(t *testing.T) {
			manager, channel, asset, output, peer, base := closedAssetPipelineFixture(t, true)
			if protocol == indexer.PROTOCOL_NAME_RUNES {
				asset.Protocol, asset.Ticker, asset.N = protocol, "TEST•FIRST•TEST", 0
				output.Assets[0].Name, output.Assets[0].BindingSat = asset.AssetName, 0
			}
			rpc := &closedAssetInventoryIndexer{closedAssetPipelineIndexer: base,
				summary: &indexerwire.AssetSummary{Data: []*indexer.AssetInfo{
					{Name: ASSET_PLAIN_SAT, Amount: *indexer.NewDefaultDecimal(660)},
					{Name: asset.AssetName, Amount: *indexer.NewDefaultDecimal(100)},
				}},
				byAsset: map[string][]*indexerwire.TxOutputInfo{asset.String(): {output.ToAssetsInUtxo()}},
			}
			manager.l1IndexerClient.SetMaster(rpc)
			name, point, err := closedAssetDriverAPI(t, manager).nextClosedChannelAssetRestoreSource(context.Background(), peer.captured, channel.ChannelId)
			if err != nil || name != asset.String() || point != output.OutPointStr {
				t.Fatalf("plain was selected before asset %s and can consume its existing330-sat stubs: asset=%s point=%s err=%v", asset, name, point, err)
			}
			if base.walletQueries != 0 || base.broadcasts != 0 || peer.requests != 0 {
				t.Fatal("source selection performed a wallet funding or peer-signing action")
			}
		})
	}
}
