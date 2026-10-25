package wallet

import (
	"context"
	"fmt"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type closedAssetCurrentPlainInventory struct {
	*closedAssetInventoryIndexer
	plain []*indexerwire.TxOutputInfo
}

func (c *closedAssetCurrentPlainInventory) GetUtxoListWithTicker(address string, asset *swire.AssetName) []*indexerwire.TxOutputInfo {
	if indexer.IsPlainAsset(asset) {
		return c.plain
	}
	return c.closedAssetInventoryIndexer.GetUtxoListWithTicker(address, asset)
}

// Once a BRC carrier has ascended, the current Bitcoin plain summary/list
// classifies its sats as plain. Its exact existing controlled snapshot still
// retains invalid BRC metadata and the330-sat historical inscription footprint.
// Terminal inventory must reconcile those views, never expand the carrier
// twice or keep the correctly restored account pending forever.
func TestClosedAssetRestoreCurrentPlainListingAccountsControlledInvalidBRC(t *testing.T) {
	for _, mode := range []string{"current-invalid-carrier", "missing-current-carrier", "changed-listed-value", "duplicate-current-carrier"} {
		t.Run(mode, func(t *testing.T) {
			manager, channel, _, _, peer, base := closedAssetPipelineFixture(t, false)
			brc := &AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_BRC20, Type: indexer.ASSET_TYPE_FT, Ticker: "ordi"}}
			carrier := channel.ChanPoint.Clone()
			carrier.OutPointStr = fmt.Sprintf("%064x:0", 13071)
			carrier.OutValue.Value = 330
			carrier.Assets = indexer.TxAssets{{Name: brc.AssetName, Amount: *indexer.NewDefaultDecimal(5)}}
			carrier.Invalids = map[indexer.AssetName]bool{brc.AssetName: true}
			carrier.Offsets = map[indexer.AssetName]indexer.AssetOffsets{brc.AssetName: {&indexer.OffsetRange{Start: 0, End: 1}}}
			carrier.SatBindingMap = map[int64]*indexer.AssetInfo{0: {Name: brc.AssetName, Amount: *indexer.NewDefaultDecimal(5)}}
			if carrier.HasAsset() || carrier.GetPlainSat() != 0 {
				t.Fatal("fixture does not retain invalid carrier inscription footprint")
			}
			if err := channel.AddFundingOutput(carrier, brc); err != nil {
				t.Fatal(err)
			}
			plainCarrier := carrier.ToAssetsInUtxo()
			plainCarrier.Assets = nil // Actual includeInvalid=false list uses valid assets only.
			root := channel.ChanPoint.ToAssetsInUtxo()
			wanted := root.Value + 330
			listed := []*indexerwire.TxOutputInfo{root, plainCarrier}
			switch mode {
			case "missing-current-carrier":
				listed = listed[:1]
			case "changed-listed-value":
				plainCarrier.Value++
				wanted++
			case "duplicate-current-carrier":
				listed = append(listed, plainCarrier)
				wanted += 330
			}
			rpc := &closedAssetCurrentPlainInventory{closedAssetInventoryIndexer: &closedAssetInventoryIndexer{closedAssetPipelineIndexer: base, summary: &indexerwire.AssetSummary{Data: []*indexer.AssetInfo{{Name: ASSET_PLAIN_SAT, Amount: *indexer.NewDefaultDecimal(wanted)}}}}, plain: listed}
			manager.l1IndexerClient.SetMaster(rpc)
			asset, point, err := closedAssetDriverAPI(t, manager).nextClosedChannelAssetRestoreSource(context.Background(), peer.captured, channel.ChannelId)
			if mode == "current-invalid-carrier" {
				if err != nil || asset != "" || point != "" {
					t.Fatalf("controlled invalid carrier was falsely missing or selected twice: asset=%s point=%s err=%v", asset, point, err)
				}
			} else if err == nil || point != "" {
				t.Fatalf("uncertain current plain list allowed all-restored: mode=%s point=%s err=%v", mode, point, err)
			}
			if peer.requests != 0 || base.walletQueries != 0 || base.broadcasts != 0 {
				t.Fatal("terminal inventory performed a funding/signing action")
			}
		})
	}
}
