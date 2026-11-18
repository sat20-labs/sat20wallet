package wallet

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"sort"

	indexer "github.com/sat20-labs/indexer/common"
)

type rgb11ContractStateAppender interface {
	AppendRGB11ContractState(indexer.WriteBatch, ContractDeployResvIF, ContractRuntime) error
}

func rgb11SettlementSwap(runtime ContractRuntime) (*SwapContractRuntime, error) {
	switch runtime := runtime.(type) {
	case *SwapContractRuntime:
		return runtime, nil
	case *TranscendContractRuntime:
		return &runtime.SwapContractRuntime, nil
	case *AmmContractRuntime:
		return &runtime.SwapContractRuntime, nil
	case *FaucetContractRuntime:
		return &runtime.SwapContractRuntime, nil
	default:
		return nil, fmt.Errorf("unsupported RGB11 withdrawal runtime %T", runtime)
	}
}

func rgb11SettlementAddValue(value, delta int64) (int64, error) {
	if value < 0 || delta < 0 || value > math.MaxInt64-delta {
		return 0, fmt.Errorf("RGB11 withdrawal counter overflow or negative value")
	}
	return value + delta, nil
}

// applyRGB11WithdrawalAtomic consumes an already authenticated,
// confirmed settlement. Network verification is performed without locks;
// this local commit serializes against selection changes and other monitors.
// No live ledger state changes until the single transaction commits.
func (p *SwapContractRuntime) applyRGB11WithdrawalAtomic(deal *DealInfo, pendingKey, settledValue []byte) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if !p.rgb11PublicContract() || p.stp == nil || p.resv == nil || deal == nil ||
		!canonicalSettlementTxID(deal.TxId) || deal.Reason != INVOKE_RESULT_WITHDRAW ||
		deal.AssetName == nil || *deal.AssetName != *p.GetAssetName() ||
		deal.TotalAmt == nil || deal.TotalAmt.Sign() <= 0 || deal.TotalValue < 0 || deal.Fee < 0 ||
		deal.Height < 0 || deal.InvokeCount < 0 || len(deal.ItemIDs) == 0 ||
		len(pendingKey) == 0 || len(settledValue) == 0 {
		return fmt.Errorf("invalid RGB11 withdrawal settlement")
	}
	database := p.stp.GetDB()
	if database == nil {
		return fmt.Errorf("RGB11 withdrawal database unavailable")
	}
	settledKey := rgb11ContractWithdrawKey("settled", deal.TxId)
	resultKey := []byte(GetContractInvokeResultKey(p.URL(), deal.TxId))
	if bytes.Equal(pendingKey, settledKey) || bytes.Equal(pendingKey, resultKey) {
		return fmt.Errorf("invalid RGB11 withdrawal pending key")
	}
	if existing, err := database.Read(settledKey); err == nil {
		if !bytes.Equal(existing, settledValue) {
			return fmt.Errorf("RGB11 withdrawal settlement conflicts with committed record")
		}
		return nil
	} else if !errors.Is(err, indexer.ErrKeyNotFound) {
		return err
	}
	// A slow chain response must not settle or delete a different pending
	// selection installed while the network request was in flight.
	pending, err := p.loadRGB11ContractPending()
	if err != nil {
		return err
	}
	if pending == nil || !bytes.Equal(pendingKey, p.rgb11WithdrawalPendingKey()) ||
		pending.Deal.TxId != deal.TxId {
		return fmt.Errorf("RGB11 withdrawal pending identity changed during verification")
	}
	expected, err := rgb11WithdrawalSelection(pending.Deal)
	if err != nil {
		return err
	}
	actual, err := rgb11WithdrawalSelection(deal)
	if err != nil {
		return err
	}
	if !bytes.Equal(expected, actual) {
		return fmt.Errorf("RGB11 withdrawal pending selection changed during verification")
	}
	if _, err := database.Read(resultKey); err == nil {
		return fmt.Errorf("RGB11 withdrawal result exists without an atomic settlement")
	} else if !errors.Is(err, indexer.ErrKeyNotFound) {
		return err
	}
	appender, ok := p.stp.(rgb11ContractStateAppender)
	if !ok {
		return fmt.Errorf("RGB11 contract reservation cannot join the atomic settlement")
	}

	ids := append([]int64(nil), deal.ItemIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	selected := make(map[int64]*SwapHistoryItem, len(ids))
	for position, id := range ids {
		if id < 0 || (position > 0 && ids[position-1] == id) {
			return fmt.Errorf("invalid or duplicate RGB11 withdrawal item ID")
		}
		selected[id] = nil
	}
	for owner, items := range p.withdrawMap {
		for id, item := range items {
			old, wanted := selected[id]
			if !wanted {
				continue
			}
			if old != nil || item == nil || item.Id != id || item.Address != owner || owner == "" {
				return fmt.Errorf("ambiguous RGB11 withdrawal item %d", id)
			}
			selected[id] = item
		}
	}
	var total *Decimal
	var totalValue int64
	recipients := make(map[string]*SendAssetInfo)
	items := make(map[int64]*SwapHistoryItem, len(ids))
	traders := make(map[string]*TraderStatus)
	for _, id := range ids {
		item := selected[id]
		if item == nil || item.OrderType != ORDERTYPE_WITHDRAW || item.Finished() ||
			item.Reason != INVOKE_REASON_NORMAL || item.FromL1 || !item.ToL1 ||
			item.AssetName != deal.AssetName.String() || item.RemainingAmt == nil ||
			item.RemainingAmt.Sign() <= 0 || item.RemainingValue < 0 || item.OutTxId != "" {
			return fmt.Errorf("RGB11 withdrawal item %d is not pending", id)
		}
		height, _, _ := indexer.FromUtxoId(item.UtxoId)
		if height > deal.Height || item.Id >= deal.InvokeCount {
			return fmt.Errorf("RGB11 withdrawal item %d is outside the signed snapshot", id)
		}
		cached := p.history[item.InUtxo]
		if cached == nil || cached.Id != id || cached.Finished() ||
			cached.RemainingAmt.Cmp(item.RemainingAmt) != 0 || cached.RemainingValue != item.RemainingValue {
			return fmt.Errorf("RGB11 withdrawal history disagrees with item %d", id)
		}
		destination := item.Address
		if len(item.Padded) != 0 {
			destination = string(item.Padded)
		}
		recipient := recipients[destination]
		if recipient == nil {
			recipient = &SendAssetInfo{Address: destination, AssetName: deal.AssetName}
			recipients[destination] = recipient
		}
		recipient.AssetAmt = recipient.AssetAmt.Add(item.RemainingAmt)
		var err error
		recipient.Value, err = rgb11SettlementAddValue(recipient.Value, item.RemainingValue)
		if err != nil {
			return err
		}
		total = total.Add(item.RemainingAmt)
		totalValue, err = rgb11SettlementAddValue(totalValue, item.RemainingValue)
		if err != nil {
			return err
		}
		clone := cloneSwapHistoryItem(item)
		clone.OutTxId, clone.OutAmt, clone.OutValue = deal.TxId, clone.RemainingAmt.Clone(), clone.RemainingValue
		clone.RemainingAmt, clone.RemainingValue, clone.Done = nil, 0, ITEM_STATUS_DEALT
		items[id] = clone
		trader := traders[item.Address]
		if trader == nil {
			original := p.traderInfoMap[item.Address]
			if original == nil {
				loaded, err := loadContractInvokerStatus(database, p.URL(), item.Address)
				if err != nil {
					return err
				}
				original, ok = loaded.(*TraderStatus)
				if !ok {
					return fmt.Errorf("invalid RGB11 withdrawal trader state")
				}
			}
			if original.GetKey() != item.Address {
				return fmt.Errorf("RGB11 withdrawal trader identity mismatch")
			}
			copy := *original
			copy.WithdrawAmt = original.WithdrawAmt.Clone()
			trader = &copy
			traders[item.Address] = trader
		}
		trader.WithdrawAmt = trader.WithdrawAmt.Add(clone.OutAmt)
		trader.WithdrawValue, err = rgb11SettlementAddValue(trader.WithdrawValue, clone.OutValue)
		if err != nil {
			return err
		}
	}
	if total.Cmp(deal.TotalAmt) != 0 || totalValue != deal.TotalValue || len(recipients) != len(deal.SendInfo) {
		return fmt.Errorf("RGB11 withdrawal totals do not match its frozen items")
	}
	for address, expected := range recipients {
		actual := deal.SendInfo[address]
		if actual == nil || actual.Address != address || actual.AssetName == nil ||
			*actual.AssetName != *deal.AssetName || actual.AssetAmt.Cmp(expected.AssetAmt) != 0 ||
			actual.Value != expected.Value {
			return fmt.Errorf("RGB11 withdrawal recipient differs from its frozen items")
		}
	}

	raw, err := EncodeToBytes(p.runtime)
	if err != nil {
		return err
	}
	candidate, err := ContractRuntimeUnMarsh(p.stp, p.runtime.GetTemplateName(), raw)
	if err != nil {
		return err
	}
	candidate.GetRuntimeBase().runtime = candidate
	work, err := rgb11SettlementSwap(candidate)
	if err != nil {
		return err
	}
	work.TotalWithdrawAssets = work.TotalWithdrawAssets.Add(deal.TotalAmt)
	work.TotalOutputAssets = work.TotalOutputAssets.Add(deal.TotalAmt)
	outputValue, err := rgb11SettlementAddValue(deal.TotalValue, deal.Fee)
	if err != nil {
		return err
	}
	for _, update := range []struct {
		value *int64
		delta int64
	}{
		{&work.TotalWithdrawSats, deal.TotalValue}, {&work.TotalWithdrawTxFee, deal.Fee},
		{&work.TotalOutputSats, outputValue},
	} {
		*update.value, err = rgb11SettlementAddValue(*update.value, update.delta)
		if err != nil {
			return err
		}
	}
	if work.TotalWithdrawTx < 0 || work.TotalWithdrawTx == math.MaxInt {
		return fmt.Errorf("RGB11 withdrawal transaction counter overflow")
	}
	work.TotalWithdrawTx++
	// New invocations can arrive while the two settlement transactions confirm.
	// An older signed request must not roll back a subsequently verified point.
	if deal.InvokeCount >= work.CheckPoint && deal.Height >= work.CheckPointBlock {
		work.CheckPoint, work.CheckPointBlock = deal.InvokeCount, deal.Height
		work.AssetMerkleRoot = append([]byte(nil), deal.RuntimeMerkleRoot...)
	}
	batch, err := NewAtomicWriteBatch(database)
	if err != nil {
		return err
	}
	defer batch.Close()
	putValue := func(key string, value any) error {
		raw, err := EncodeToBytes(value)
		if err != nil {
			return err
		}
		return batch.Put([]byte(key), raw)
	}
	for _, id := range ids {
		item := items[id]
		if err := putValue(GetContractInvokeHistoryKey(p.URL(), item.GetKey()), item); err != nil {
			return err
		}
		if err := batch.Put([]byte(GetContractInvokeHistoryKey2(p.URL(), item.InUtxo)), []byte(item.GetKey())); err != nil {
			return err
		}
	}
	addresses := make([]string, 0, len(traders))
	for address := range traders {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	for _, address := range addresses {
		if err := putValue(GetContractInvokerStatusKey(p.URL(), address), traders[address]); err != nil {
			return err
		}
	}
	if err := putValue(GetContractRuntimeKey(p.URL()), candidate); err != nil {
		return err
	}
	if err := appender.AppendRGB11ContractState(batch, p.resv, candidate); err != nil {
		return err
	}
	if err := batch.Put(resultKey, []byte(INVOKE_RESULT_WITHDRAW)); err != nil {
		return err
	}
	if err := batch.Put(settledKey, settledValue); err != nil {
		return err
	}
	if err := batch.Delete(pendingKey); err != nil {
		return err
	}
	if err := batch.Flush(); err != nil {
		return err
	}

	p.SwapContractRunningData = work.SwapContractRunningData
	p.CheckPoint, p.CheckPointBlock = work.CheckPoint, work.CheckPointBlock
	p.AssetMerkleRoot = work.AssetMerkleRoot
	if p.traderInfoMap == nil {
		p.traderInfoMap = make(map[string]*TraderStatus)
	}
	for _, address := range addresses {
		if existing := p.traderInfoMap[address]; existing != nil {
			*existing = *traders[address]
		} else {
			p.traderInfoMap[address] = traders[address]
		}
	}
	for _, id := range ids {
		item := selected[id]
		*item = *items[id]
		delete(p.history, item.InUtxo)
		delete(p.withdrawMap[item.Address], id)
		if len(p.withdrawMap[item.Address]) == 0 {
			delete(p.withdrawMap, item.Address)
		}
	}
	p.refreshTime = 0
	return nil
}
