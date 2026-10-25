package wallet

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
)

// This interface is the intended private production boundary, not a test-only
// bridge. The unchanged Stage A baseline must compile and fail this contract.
type closedAssetRestoreBoundary interface {
	startClosedChannelAssetRestoreForWallet(context.Context, common.Wallet, string, string, string) (int64, error)
	waitClosedChannelAssetRestoreForWallet(context.Context, common.Wallet, string, string, string, int64) error
}

func closedAssetRestoreAPI(t *testing.T, manager *Manager) closedAssetRestoreBoundary {
	t.Helper()
	api, ok := any(manager).(closedAssetRestoreBoundary)
	if !ok {
		t.Fatal("missing private captured-wallet, exact-UTXO closed-channel asset restoration boundary")
	}
	return api
}

func closedAssetRestoreFixture(t *testing.T) (*Manager, *Channel, *AssetName, *TxOutput) {
	t.Helper()
	manager, channel := newSignedChannelSyncState(t, &channelHeartbeatTestClient{})
	asset := &AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: indexer.ASSET_TYPE_FT, Ticker: "restore"}, N: 1}
	output := channel.ChanPoint.Clone()
	output.OutPointStr = fmt.Sprintf("%064x:0", 8421)
	output.OutValue.Value = 1000
	output.Assets = indexer.TxAssets{{Name: asset.AssetName, Amount: *indexer.NewDefaultDecimal(100), BindingSat: 1}}
	return manager, channel, asset, output
}

func TestClosedAssetRestorePrivateBoundary(t *testing.T) {
	manager, _, _, _ := closedAssetRestoreFixture(t)
	_ = closedAssetRestoreAPI(t, manager)
}

func TestClosedAssetRestoreCanceledEntryHasNoChild(t *testing.T) {
	manager, channel, asset, output := closedAssetRestoreFixture(t)
	api := closedAssetRestoreAPI(t, manager)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	id, err := api.startClosedChannelAssetRestoreForWallet(ctx, channel.LocalWallet(), channel.ChannelId, asset.String(), output.OutPointStr)
	if !errors.Is(err, context.Canceled) || id != 0 || len(manager.GetSplicingReservations()) != 0 {
		t.Fatalf("canceled recovery created work: id=%d err=%v children=%d", id, err, len(manager.GetSplicingReservations()))
	}
}

func TestClosedAssetRestoreRejectsOtherAccount(t *testing.T) {
	manager, channel, asset, output := closedAssetRestoreFixture(t)
	api := closedAssetRestoreAPI(t, manager)
	other := channel.LocalWallet().Clone()
	other.SetSubAccount(other.GetSubAccount() + 1)
	id, err := api.startClosedChannelAssetRestoreForWallet(context.Background(), other, channel.ChannelId, asset.String(), output.OutPointStr)
	if err == nil || id != 0 || len(manager.GetSplicingReservations()) != 0 {
		t.Fatalf("other account adopted channel: id=%d err=%v", id, err)
	}
}

func TestClosedAssetRestoreMissingChildIsNotSuccess(t *testing.T) {
	manager, channel, asset, output := closedAssetRestoreFixture(t)
	api := closedAssetRestoreAPI(t, manager)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := api.waitClosedChannelAssetRestoreForWallet(ctx, channel.LocalWallet(), channel.ChannelId, asset.String(), output.OutPointStr, 8422); err == nil {
		t.Fatal("missing child was treated as restored")
	}
}

func TestClosedAssetRestoreWaitCanceledDoesNotBlockAnotherAccount(t *testing.T) {
	manager, channel, asset, output := closedAssetRestoreFixture(t)
	api := closedAssetRestoreAPI(t, manager)
	resv := &SplicingReservation{SplicingDataInDB: SplicingDataInDB{
		ReservationBase: NewReservationBase(8423, true, RS_SPLICINGIN_ANCHOR_BROADCASTED, channel.LocalWallet()),
		ChannelId:       channel.ChannelId, AssetName: asset, Amt: indexer.NewDefaultDecimal(100), SplicingAmt: indexer.NewDefaultDecimal(100), SplicingValue: output.Value(),
	}, SplicingInputs: []*TxOutput{output}}
	resv.Channel = channel
	manager.AddResv(resv)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- api.waitClosedChannelAssetRestoreForWallet(ctx, channel.LocalWallet(), channel.ChannelId, asset.String(), output.OutPointStr, resv.Id)
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("wait cancellation=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("asset wait did not release on context cancellation")
	}
	manager.channelIdentityMu.Lock()
	manager.mutex.Lock()
	manager.wallet.SetSubAccount(1)
	manager.mutex.Unlock()
	manager.channelIdentityMu.Unlock()
	if manager.wallet.GetSubAccount() != 1 {
		t.Fatal("asset wait retained the old account selection")
	}
}

func TestClosedAssetRestoreWrongChildAssetIsNotSuccess(t *testing.T) {
	manager, channel, asset, output := closedAssetRestoreFixture(t)
	api := closedAssetRestoreAPI(t, manager)
	other := *asset
	other.Ticker = "other"
	resv := &SplicingReservation{SplicingDataInDB: SplicingDataInDB{
		ReservationBase: NewReservationBase(8424, true, RS_CLOSED, channel.LocalWallet()),
		ChannelId:       channel.ChannelId, AssetName: &other,
	}}
	manager.AddResv(resv)
	if err := api.waitClosedChannelAssetRestoreForWallet(context.Background(), channel.LocalWallet(), channel.ChannelId, asset.String(), output.OutPointStr, resv.Id); err == nil {
		t.Fatal("another asset child was treated as restored")
	}
}
