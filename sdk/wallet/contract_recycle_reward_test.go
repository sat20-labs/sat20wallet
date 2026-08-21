package wallet

import "testing"

func TestRecycleRewardItemRemovedAfterSuccessfulSend(t *testing.T) {
	const (
		address = "reward-address"
		itemID  = int64(17)
		txID    = "reward-tx"
	)

	item := &InvokeItem{
		InvokeHistoryItemBase: InvokeHistoryItemBase{
			Id:   itemID,
			Done: ITEM_STATUS_INIT,
		},
		Address:        address,
		RemainingValue: 1000,
	}
	runtime := &RecycleContractRunTime{
		rewardMap: map[string]map[int64]*InvokeItem{
			address: {itemID: item},
		},
	}

	selected := runtime.findRewardItemByID(itemID)
	if selected != item {
		t.Fatal("reward lookup did not return the in-memory item")
	}
	markRewardItemDealtState(selected, txID)
	runtime.pruneFinishedRewardItems()

	if !item.Finished() || item.Done != ITEM_STATUS_DEALT {
		t.Fatalf("reward item was not marked dealt: %+v", item)
	}
	if item.OutTxId != txID || !item.ToL1 || item.RemainingValue != 0 {
		t.Fatalf("reward item completion state is incomplete: %+v", item)
	}
	if len(runtime.rewardMap) != 0 {
		t.Fatalf("finished reward item remains queued: %+v", runtime.rewardMap)
	}
}
