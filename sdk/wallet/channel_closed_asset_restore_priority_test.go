package wallet

import (
	"context"
	"fmt"
	"sort"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
)

func TestClosedAssetRestoreSummaryOrderCannotChangeAssetPriority(t *testing.T) {
	orders := [][]int{{0, 1, 2, 3}, {3, 2, 1, 0}, {2, 0, 3, 1}, {1, 3, 0, 2}, {3, 1, 2, 0}, {0, 2, 3, 1}}
	for i, order := range orders {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			manager, channel, ordx, source, peer, base := closedAssetPipelineFixture(t, true)
			brc := indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_BRC20, Type: indexer.ASSET_TYPE_FT, Ticker: "ordi"}
			runes := indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_RUNES, Type: indexer.ASSET_TYPE_FT, Ticker: "TEST•FIRST•TEST"}
			names := []indexer.AssetName{ASSET_PLAIN_SAT, ordx.AssetName, runes, brc}
			items := make([]*indexer.AssetInfo, len(names))
			outputs := map[string][]*indexerwire.TxOutputInfo{}
			for n, name := range names {
				amount := int64(100)
				if n == 0 {
					amount = 660
				}
				items[n] = &indexer.AssetInfo{Name: name, Amount: *indexer.NewDefaultDecimal(amount)}
				if n != 0 {
					out := source.Clone()
					out.OutPointStr = fmt.Sprintf("%s:%d", source.OutPointStr[:64], n)
					out.Assets[0].Name = name
					if n != 1 {
						out.Assets[0].BindingSat = 0
					}
					outputs[name.String()] = []*indexerwire.TxOutputInfo{out.ToAssetsInUtxo()}
				}
			}
			summary := &indexerwire.AssetSummary{}
			for _, n := range order {
				summary.Data = append(summary.Data, items[n])
			}
			summary.Data = append(summary.Data, nil)
			rpc := &closedAssetInventoryIndexer{closedAssetPipelineIndexer: base, summary: summary, byAsset: outputs}
			manager.l1IndexerClient.SetMaster(rpc)
			middle := []string{ordx.String(), runes.String()}
			sort.Strings(middle)
			expected := append([]string{brc.String()}, middle...)
			expected = append(expected, ASSET_PLAIN_SAT.String())
			for stage, want := range expected {
				name, point, err := closedAssetDriverAPI(t, manager).nextClosedChannelAssetRestoreSource(context.Background(), peer.captured, channel.ChannelId)
				wantPoint := base.stubs[0].OutPointStr
				if stage < 3 {
					wantPoint = outputs[want][0].OutPoint
				}
				if err != nil || name != want || point != wantPoint {
					t.Fatalf("summary order %v stage %d: got asset=%s point=%s err=%v want asset=%s point=%s", order, stage, name, point, err, want, wantPoint)
				}
				if stage < 3 {
					// Advance only this read-only selection model. No signature, funding,
					// broadcast, controlled-output injection or protocol mutation occurs.
					delete(outputs, want)
					for _, item := range items {
						if item.Name.String() == want {
							item.Amount = *indexer.NewDefaultDecimal(0)
						}
					}
				}
			}
			if len(base.stubs) != 2 || base.stubs[0].Value() != 330 || base.stubs[1].Value() != 330 || base.walletQueries != 0 || base.broadcasts != 0 || peer.requests != 0 {
				t.Fatal("source selection consumed existing stubs or performed funding/signing")
			}
		})
	}
}
