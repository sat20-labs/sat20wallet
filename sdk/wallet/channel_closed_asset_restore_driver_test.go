package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type closedAssetRestoreDriverBoundary interface {
	nextClosedChannelAssetRestoreSource(context.Context, common.Wallet, string) (string, string, error)
	driveClosedChannelAssetRestoreForWallet(context.Context, common.Wallet, NodeRPCClient, string) (bool, error)
}

type closedAssetInventoryIndexer struct {
	*closedAssetPipelineIndexer
	summary *indexerwire.AssetSummary
	byAsset map[string][]*indexerwire.TxOutputInfo
}

func (c *closedAssetInventoryIndexer) GetAssetSummaryWithAddress(string) *indexerwire.AssetSummary {
	return c.summary
}
func (c *closedAssetInventoryIndexer) GetUtxoListWithTicker(address string, asset *swire.AssetName) []*indexerwire.TxOutputInfo {
	if !indexer.IsPlainAsset(asset) {
		return c.byAsset[asset.String()]
	}
	return c.closedAssetPipelineIndexer.GetUtxoListWithTicker(address, asset)
}
func (c *closedAssetInventoryIndexer) GetUtxoListWithBRC20Ticker(_ string, asset *swire.AssetName, _ bool) []*indexerwire.TxOutputInfo {
	return c.byAsset[asset.String()]
}

func closedAssetDriverAPI(t *testing.T, manager *Manager) closedAssetRestoreDriverBoundary {
	t.Helper()
	api, ok := any(manager).(closedAssetRestoreDriverBoundary)
	if !ok {
		t.Fatal("missing exact-item closed-channel heartbeat driver")
	}
	return api
}

func TestClosedAssetRestoreInventoryPreservesBRCFirstOrder(t *testing.T) {
	manager, channel, asset, output, _, base := closedAssetPipelineFixture(t, true)
	brc := indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_BRC20, Type: indexer.ASSET_TYPE_FT, Ticker: "ordi"}
	brcOutput := output.Clone()
	brcOutput.OutPointStr = output.OutPointStr[:64] + ":1"
	brcOutput.Assets = indexer.TxAssets{{Name: brc, Amount: *indexer.NewDefaultDecimal(10)}}
	rpc := &closedAssetInventoryIndexer{closedAssetPipelineIndexer: base, summary: &indexerwire.AssetSummary{Data: []*indexer.AssetInfo{
		{Name: asset.AssetName, Amount: *indexer.NewDefaultDecimal(100)}, {Name: brc, Amount: *indexer.NewDefaultDecimal(10)},
	}}, byAsset: map[string][]*indexerwire.TxOutputInfo{asset.String(): {output.ToAssetsInUtxo()}, brc.String(): {brcOutput.ToAssetsInUtxo()}}}
	manager.l1IndexerClient.SetMaster(rpc)
	name, point, err := closedAssetDriverAPI(t, manager).nextClosedChannelAssetRestoreSource(context.Background(), channel.LocalWallet(), channel.ChannelId)
	if err != nil || name != brc.String() || point != brcOutput.OutPointStr {
		t.Fatalf("BRC-first exact selection: asset=%s point=%s err=%v", name, point, err)
	}
}

func TestClosedAssetRestoreInventoryUncertaintyCannotFinishAll(t *testing.T) {
	for _, mode := range []string{"missing-summary", "incomplete-list", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			manager, channel, asset, output, _, base := closedAssetPipelineFixture(t, true)
			rpc := &closedAssetInventoryIndexer{closedAssetPipelineIndexer: base, byAsset: map[string][]*indexerwire.TxOutputInfo{}}
			if mode != "missing-summary" {
				name := asset.AssetName
				if mode == "unsupported" {
					name.Protocol = "unknown-protocol"
					output.Assets[0].Name = name
					rpc.byAsset[name.String()] = []*indexerwire.TxOutputInfo{output.ToAssetsInUtxo()}
				}
				rpc.summary = &indexerwire.AssetSummary{Data: []*indexer.AssetInfo{{Name: name, Amount: *indexer.NewDefaultDecimal(100)}}}
			}
			manager.l1IndexerClient.SetMaster(rpc)
			_, point, err := closedAssetDriverAPI(t, manager).nextClosedChannelAssetRestoreSource(context.Background(), channel.LocalWallet(), channel.ChannelId)
			if err == nil || point != "" {
				t.Fatalf("uncertain inventory was treated as finished: point=%s err=%v", point, err)
			}
		})
	}
}

func TestClosedAssetRestoreDriverKeepsSingleAssetUnknownPending(t *testing.T) {
	manager, channel, asset, output, peer, base := closedAssetPipelineFixture(t, true)
	rpc := &closedAssetInventoryIndexer{closedAssetPipelineIndexer: base,
		summary: &indexerwire.AssetSummary{Data: []*indexer.AssetInfo{{Name: asset.AssetName, Amount: *indexer.NewDefaultDecimal(100)}}},
		byAsset: map[string][]*indexerwire.TxOutputInfo{asset.String(): {output.ToAssetsInUtxo()}},
	}
	manager.l1IndexerClient.SetMaster(rpc)
	restored := 0
	manager.RegisterCallback(func(event string, _ interface{}) {
		if event == MSG_CHANNEL_RESTORED {
			restored++
		}
	})
	api := closedAssetDriverAPI(t, manager)
	done, err := api.driveClosedChannelAssetRestoreForWallet(context.Background(), peer.captured, peer, channel.ChannelId)
	if done || !errors.Is(err, errClosedAssetPipelineObserved) || peer.requests != 1 || restored != 0 {
		t.Fatalf("single unknown outcome was swallowed: done=%t err=%v req=%d restored=%d", done, err, peer.requests, restored)
	}
	done, err = api.driveClosedChannelAssetRestoreForWallet(context.Background(), peer.captured, peer, channel.ChannelId)
	if done || err == nil || peer.requests != 1 || restored != 0 {
		t.Fatalf("unknown initial request was recreated: done=%t err=%v req=%d restored=%d", done, err, peer.requests, restored)
	}
}

func TestClosedAssetRestoreFundingEventIsCapturedAndDeduplicated(t *testing.T) {
	manager, channel, _, _ := closedAssetRestoreFixture(t)
	captured := channel.LocalWallet().Clone()
	manager.wallet.SetSubAccount(1)
	legacy, dedicated := 0, 0
	manager.RegisterCallback(func(event string, data interface{}) {
		if event == MSG_OPERATION_FAILED {
			legacy++
		}
		if event != "channelrebuildfundingrequired" {
			return
		}
		dedicated++
		raw, ok := data.(string)
		if !ok {
			t.Error("funding event is not a portable JSON string")
			return
		}
		var notice struct {
			WalletID           int64  `json:"walletId"`
			AccountIndex       uint32 `json:"accountIndex"`
			ChannelID, Message string
		}
		if json.Unmarshal([]byte(raw), &notice) != nil || notice.WalletID != captured.GetId() || notice.AccountIndex != captured.GetSubAccount() || notice.ChannelID != channel.ChannelId || notice.Message == "" {
			t.Errorf("wrong captured funding event: %s", raw)
		}
	})
	for i := 0; i < 2; i++ {
		manager.notifyClosedChannelFundingApproval(captured, channel.ChannelId, closedChannelFundingApprovalRequired(channel.ChannelId))
	}
	if legacy != 1 || dedicated != 1 || manager.wallet.GetSubAccount() != 1 {
		t.Fatalf("funding event duplicated or changed account: legacy=%d dedicated=%d", legacy, dedicated)
	}
}
