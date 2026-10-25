package wallet

import (
	"context"
	"encoding/json"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
)

func closedAssetDiscoveryExpectation(t *testing.T, manager *Manager, owner *LocalActionPerformData, asset AssetName, amount *Decimal) {
	t.Helper()
	raw, ok := owner.ActionParam.(string)
	if !ok {
		t.Fatal("owner has no JSON ActionParam")
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatal(err)
	}
	expectation, err := json.Marshal([]struct {
		Asset  AssetName
		Amount *Decimal
	}{{asset, amount}})
	if err != nil {
		t.Fatal(err)
	}
	envelope["AssetExpectations"] = expectation
	next, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	owner.ActionParam = string(next)
	if err := manager.SaveWalletReservation(owner); err != nil {
		t.Fatal(err)
	}
}

// A public BRC20 address summary can omit already-ascended/invalid old sources.
// Retaining just the old ticker and exact expected client amount must force
// current invalid-inclusive discovery; old spent outpoints are not inputs.
func TestClosedAssetRestoreFindsOnlyInvalidLegacyBRCWithoutNewDeposit(t *testing.T) {
	manager, channel, _, output, peer, base := closedAssetPipelineFixture(t, true)
	asset := AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_BRC20, Type: indexer.ASSET_TYPE_FT, Ticker: "ordi"}}
	output.Assets = indexer.TxAssets{{Name: asset.AssetName, Amount: *indexer.NewDefaultDecimal(80)}}
	output.Invalids = map[indexer.AssetName]bool{asset.AssetName: true}
	closedAssetDiscoveryExpectation(t, manager, peer.owner, asset, indexer.NewDefaultDecimal(80))
	rpc := &closedAssetInventoryIndexer{closedAssetPipelineIndexer: base, summary: &indexerwire.AssetSummary{}, byAsset: map[string][]*indexerwire.TxOutputInfo{
		asset.String(): {output.ToAssetsInUtxo()},
	}}
	manager.l1IndexerClient.SetMaster(rpc)
	name, point, err := closedAssetDriverAPI(t, manager).nextClosedChannelAssetRestoreSource(context.Background(), peer.captured, channel.ChannelId)
	if err != nil || name != asset.String() || point != output.OutPointStr {
		t.Fatalf("only-invalid old asset was omitted: asset=%s point=%s err=%v", name, point, err)
	}
}

func TestClosedAssetRestoreMissingExpectedLegacyQuantityCannotFinish(t *testing.T) {
	manager, channel, _, _, peer, base := closedAssetPipelineFixture(t, true)
	asset := AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_BRC20, Type: indexer.ASSET_TYPE_FT, Ticker: "ordi"}}
	closedAssetDiscoveryExpectation(t, manager, peer.owner, asset, indexer.NewDefaultDecimal(80))
	manager.l1IndexerClient.SetMaster(&closedAssetInventoryIndexer{closedAssetPipelineIndexer: base, summary: &indexerwire.AssetSummary{}, byAsset: map[string][]*indexerwire.TxOutputInfo{}})
	_, point, err := closedAssetDriverAPI(t, manager).nextClosedChannelAssetRestoreSource(context.Background(), peer.captured, channel.ChannelId)
	if err == nil || point != "" {
		t.Fatalf("missing expected client quantity was treated as all-restored: point=%s err=%v", point, err)
	}
}
