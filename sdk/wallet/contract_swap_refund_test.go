package wallet

import (
	"fmt"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestSwapRefundResultDoesNotConsumeUnrelatedItems(t *testing.T) {
	database := NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB failed")
	}
	defer database.Close()

	manager := &Manager{db: database}
	runtime := NewSwapContractRuntime(manager)
	runtime.ChannelAddr = "test-channel"
	runtime.InvokeCount = 15
	runtime.Contract.GetContractBase().AssetName = wire.AssetName{
		Protocol: indexer.PROTOCOL_NAME_BRC20,
		Type:     indexer.ASSET_TYPE_FT,
		Ticker:   "ordi",
	}

	newRefundItem := func(id int64, height int, address string, value int64, orderType int) *SwapHistoryItem {
		return &SwapHistoryItem{
			InvokeHistoryItemBase: InvokeHistoryItemBase{
				Id:     id,
				Reason: INVOKE_REASON_REFUND,
				Done:   ITEM_STATUS_INIT,
			},
			OrderType:      orderType,
			UtxoId:         indexer.ToUtxoId(height, 0, 0),
			Address:        address,
			InUtxo:         fmt.Sprintf("item-%d:0", id),
			RemainingValue: value,
		}
	}

	items := []*SwapHistoryItem{
		newRefundItem(9, 20, "account-1", 40, ORDERTYPE_BUY),
		newRefundItem(12, 32, "account-1", 0, ORDERTYPE_REFUND),
		newRefundItem(7, 19, "account-2", 45, ORDERTYPE_BUY),
		newRefundItem(10, 27, "account-2", 45, ORDERTYPE_BUY),
		newRefundItem(13, 33, "account-2", 0, ORDERTYPE_REFUND),
		newRefundItem(11, 21, "account-3", 11, ORDERTYPE_BUY),
		newRefundItem(14, 34, "account-3", 0, ORDERTYPE_REFUND),
	}
	for _, item := range items {
		runtime.insertBuck(item)
		addItemToMap(item, runtime.refundMap)
		runtime.traderInfoMap[item.Address] = NewTraderStatus(item.Address, 0)
	}

	first, err := runtime.genRefundInfoForItemIDs([]int64{9, 12}, 32)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.SendInfo) != 1 || first.SendInfo["account-1"] == nil ||
		first.SendInfo["account-1"].Value != 40 {
		t.Fatalf("unexpected first refund: %+v", first.SendInfo)
	}

	first.TxId = "refund-40"
	first.InvokeCount = 13
	runtime.updateWithDealInfo_refund(first)

	if _, ok := runtime.refundMap["account-1"]; ok {
		t.Fatal("settled account-1 refund items were not removed")
	}
	for _, id := range []int64{7, 10, 13} {
		item := runtime.refundMap["account-2"][id]
		if item == nil || item.Finished() {
			t.Fatalf("unrelated account-2 refund item %d was consumed", id)
		}
	}
	for _, id := range []int64{11, 14} {
		item := runtime.refundMap["account-3"][id]
		if item == nil || item.Finished() {
			t.Fatalf("unrelated account-3 refund item %d was consumed", id)
		}
	}

	second, err := runtime.genRefundInfoForItemIDs([]int64{7, 10, 13}, 33)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.SendInfo) != 1 || second.SendInfo["account-2"] == nil ||
		second.SendInfo["account-2"].Value != 90 {
		t.Fatalf("unexpected second refund: %+v", second.SendInfo)
	}
}

func TestSwapRefundEmptyItemIDsSelectAllAndBecomeIdle(t *testing.T) {
	database := NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB failed")
	}
	defer database.Close()

	manager := &Manager{db: database}
	runtime := NewSwapContractRuntime(manager)
	runtime.ChannelAddr = "test-channel"
	runtime.Contract.GetContractBase().AssetName = wire.AssetName{
		Protocol: indexer.PROTOCOL_NAME_BRC20,
		Type:     indexer.ASSET_TYPE_FT,
		Ticker:   "ordi",
	}

	newItem := func(id int64, height int, value int64, orderType int) *SwapHistoryItem {
		return &SwapHistoryItem{
			InvokeHistoryItemBase: InvokeHistoryItemBase{
				Id:     id,
				Reason: INVOKE_REASON_NORMAL,
				Done:   ITEM_STATUS_INIT,
			},
			OrderType:      orderType,
			UtxoId:         indexer.ToUtxoId(height, 0, 0),
			Address:        "account-1",
			InUtxo:         fmt.Sprintf("item-%d:0", id),
			RemainingValue: value,
		}
	}

	items := []*SwapHistoryItem{
		newItem(9, 20, 40, ORDERTYPE_BUY),
		newItem(10, 22, 60, ORDERTYPE_BUY),
	}
	for _, item := range items {
		runtime.insertBuck(item)
		addItemToMap(item, runtime.swapMap)
		runtime.history[item.InUtxo] = item
	}
	runtime.traderInfoMap["account-1"] = NewTraderStatus("account-1", 0)

	command := newItem(12, 32, 0, ORDERTYPE_REFUND)
	runtime.insertBuck(command)
	runtime.history[command.InUtxo] = command
	runtime.addRefundItem(command, false)

	if _, ok := runtime.swapMap["account-1"]; ok {
		t.Fatal("refund command left an empty address in swapMap")
	}

	refund, err := runtime.genRefundInfoForItemIDs(nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []int64{9, 10, 12}
	if len(refund.ItemIDs) != len(wantIDs) {
		t.Fatalf("unexpected item IDs: %v", refund.ItemIDs)
	}
	for i := range wantIDs {
		if refund.ItemIDs[i] != wantIDs[i] {
			t.Fatalf("unexpected item IDs: %v", refund.ItemIDs)
		}
	}
	if info := refund.SendInfo["account-1"]; info == nil || info.Value != 100 {
		t.Fatalf("unexpected refund outputs: %+v", refund.SendInfo)
	}

	// Exercise the apply path with the wire-level empty-list meaning.
	refund.ItemIDs = nil
	refund.TxId = "refund-all"
	runtime.updateWithDealInfo_refund(refund)

	if len(refund.ItemIDs) != len(wantIDs) {
		t.Fatalf("empty item list was not frozen to concrete IDs: %v", refund.ItemIDs)
	}
	if !runtime.IsIdle() {
		t.Fatalf("runtime is not idle: swap=%d refund=%d", len(runtime.swapMap), len(runtime.refundMap))
	}
}
