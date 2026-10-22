package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	indexer "github.com/sat20-labs/indexer/common"
)

type nativeContractPendingWithdrawal struct {
	Version     uint32    `json:"version"`
	ContractURL string    `json:"contract_url"`
	Deal        *DealInfo `json:"deal"`
	Settled     bool      `json:"settled,omitempty"`
}

var nativeContractSettlementMu sync.Mutex

func (p *SwapContractRuntime) nativeWithdrawalPendingKey() []byte {
	return nativeContractWithdrawKey("pending", nativeContractDealID([]byte(p.URL())))
}

func (p *SwapContractRuntime) loadNativeContractPending() (*nativeContractPendingWithdrawal, error) {
	raw, err := p.stp.GetDB().Read(p.nativeWithdrawalPendingKey())
	if errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pending nativeContractPendingWithdrawal
	if json.Unmarshal(raw, &pending) != nil || pending.Version != 1 || pending.ContractURL != p.URL() || pending.Deal == nil || len(pending.Deal.ItemIDs) == 0 || pending.Deal.Reason != INVOKE_RESULT_WITHDRAW || pending.Settled {
		return nil, fmt.Errorf("invalid native contract withdrawal journal")
	}
	return &pending, nil
}

func nativeWithdrawalSelection(deal *DealInfo) ([]byte, error) {
	copy := *deal
	copy.PreOutputs = nil
	copy.TxId = ""
	copy.Fee = 0
	// Ledger roots are a synchronization context, not the selected obligation.
	// They may advance while the fixed Bitcoin transaction is being retried.
	copy.InvokeCount = 0
	copy.StaticMerkleRoot = nil
	copy.RuntimeMerkleRoot = nil
	copy.ItemIDs = append([]int64(nil), deal.ItemIDs...)
	sort.Slice(copy.ItemIDs, func(i, j int) bool { return copy.ItemIDs[i] < copy.ItemIDs[j] })
	return json.Marshal(copy)
}

// Persist selection before any peer is asked to sign. A restart resumes this
// same set of requests even if later blocks contain additional withdrawals.
func (p *SwapContractRuntime) stageNativeContractWithdrawal(deal *DealInfo) error {
	if !p.nativePublicContract() || deal == nil || deal.Reason != INVOKE_RESULT_WITHDRAW || len(deal.ItemIDs) == 0 {
		return fmt.Errorf("invalid native withdrawal selection")
	}
	copy := *deal
	copy.PreOutputs = nil
	copy.ItemIDs = append([]int64(nil), deal.ItemIDs...)
	sort.Slice(copy.ItemIDs, func(i, j int) bool { return copy.ItemIDs[i] < copy.ItemIDs[j] })
	pending := nativeContractPendingWithdrawal{Version: 1, ContractURL: p.URL(), Deal: &copy}
	nativeChannelInventoryMu.Lock()
	defer nativeChannelInventoryMu.Unlock()
	if copy.TxId != "" {
		raw, err := p.stp.GetDB().Read(nativeContractWithdrawKey("settled", copy.TxId))
		if err == nil {
			var settled nativeContractPendingWithdrawal
			if json.Unmarshal(raw, &settled) != nil || settled.Version != 1 || !settled.Settled || settled.ContractURL != p.URL() || settled.Deal == nil || settled.Deal.TxId != copy.TxId {
				return fmt.Errorf("invalid committed native withdrawal")
			}
			a, err := nativeWithdrawalSelection(settled.Deal)
			if err != nil {
				return err
			}
			b, err := nativeWithdrawalSelection(&copy)
			if err != nil {
				return err
			}
			if !bytes.Equal(a, b) {
				return fmt.Errorf("native withdrawal conflicts with committed selection")
			}
			return nil // A delayed peer result must not recreate a settled intent.
		}
		if !errors.Is(err, indexer.ErrKeyNotFound) {
			return err
		}
	}
	old, err := p.loadNativeContractPending()
	if err != nil {
		return err
	}
	if old != nil {
		a, e := nativeWithdrawalSelection(old.Deal)
		if e != nil {
			return e
		}
		b, e := nativeWithdrawalSelection(&copy)
		if e != nil {
			return e
		}
		if !bytes.Equal(a, b) || (old.Deal.TxId != "" && copy.TxId != "" && old.Deal.TxId != copy.TxId) {
			return fmt.Errorf("native withdrawal selection is immutable")
		}
		if old.Deal.TxId != "" || copy.TxId == "" {
			return nil
		}
		old.Deal.TxId = copy.TxId
		pending = *old
	}
	raw, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	return p.stp.GetDB().Write(p.nativeWithdrawalPendingKey(), raw)
}

func (p *SwapContractRuntime) reconcileNativeContractWithdrawals() (bool, error) {
	if !p.nativePublicContract() {
		return false, nil
	}
	nativeContractSettlementMu.Lock()
	defer nativeContractSettlementMu.Unlock()
	pending, err := p.loadNativeContractPending()
	if err != nil || pending == nil {
		return pending != nil, err
	}
	if pending.Deal.TxId == "" {
		return true, nil
	}
	if err := p.stp.GetWalletMgr().resumeNativeContractBroadcast(context.Background(), p.localWallet, p.URL(), pending.Deal.TxId); err != nil {
		return true, err
	}
	confirmed, err := p.stp.GetWalletMgr().LoadConfirmedNativeContractWithdrawal(context.Background(), p.localWallet, pending.Deal.TxId)
	if err != nil {
		return true, err
	}
	if confirmed.ContractURL != p.URL() || confirmed.Amount.Cmp(pending.Deal.TotalAmt) != 0 {
		return true, fmt.Errorf("native confirmed pair differs from frozen withdrawal")
	}
	pending.Deal.Fee = confirmed.Fee
	pending.Settled = true
	raw, err := json.Marshal(pending)
	if err != nil {
		return true, err
	}
	if err := p.applyNativeContractWithdrawalAtomic(pending.Deal, p.nativeWithdrawalPendingKey(), raw); err != nil {
		return true, err
	}
	return false, nil
}

func (p *ContractRuntimeBase) reconcileNativeContractBlock() {
	if !p.nativePublicContract() {
		return
	}
	if runtime, ok := p.runtime.(interface{ reconcileNativeContractWithdrawals() (bool, error) }); ok {
		if _, err := runtime.reconcileNativeContractWithdrawals(); err != nil {
			Log.Debugf("native contract %s awaiting canonical settlement: %v", p.URL(), err)
		}
	}
}

func (p *SwapContractRuntime) withdrawNativeContract() error {
	if _, err := p.reconcileNativeContractWithdrawals(); err != nil {
		Log.Debugf("native contract withdrawal pending: %v", err)
	}
	if !p.resv.LocalIsInitiator() {
		return nil
	}
	pending, err := p.loadNativeContractPending()
	if err != nil {
		return err
	}
	if pending == nil {
		p.mutex.RLock()
		height := p.CurrBlock
		p.mutex.RUnlock()
		deal := p.genWithdrawInfo(height)
		if len(deal.SendInfo) == 0 {
			return nil
		}
		if err := p.stageNativeContractWithdrawal(deal); err != nil {
			return err
		}
		pending, err = p.loadNativeContractPending()
		if err != nil {
			return err
		}
	}
	txid, fee, _, err := p.sendTx(pending.Deal, INVOKE_RESULT_WITHDRAW, true, true, 0)
	if err != nil {
		return err
	}
	pending.Deal.TxId, pending.Deal.Fee = txid, fee
	if err := p.stageNativeContractWithdrawal(pending.Deal); err != nil {
		return err
	}
	_, err = p.reconcileNativeContractWithdrawals()
	if err != nil {
		Log.Debugf("native withdrawal %s broadcast, waiting for both chains: %v", txid, err)
	}
	return nil
}

func (p *SwapContractRuntime) genNativeWithdrawInfo(height int, ids []int64) (*DealInfo, error) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	selected := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if selected[id] {
			return nil, fmt.Errorf("duplicate native withdrawal item")
		}
		selected[id] = true
	}
	var items []*SwapHistoryItem
	var highest int64
	for _, group := range p.withdrawMap {
		for _, item := range group {
			h, _, _ := indexer.FromUtxoId(item.UtxoId)
			if h > height || item.Finished() || item.Reason != INVOKE_REASON_NORMAL {
				continue
			}
			if len(ids) > 0 && !selected[item.Id] {
				continue
			}
			items = append(items, item)
			highest = max(highest, item.UnitPrice.Int64())
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Id < items[j].Id })
	deal := &DealInfo{SendInfo: make(map[string]*SendAssetInfo), AssetName: p.GetAssetName(), Reason: INVOKE_RESULT_WITHDRAW, InvokeCount: p.InvokeCount, StaticMerkleRoot: append([]byte(nil), p.StaticMerkleRoot...), RuntimeMerkleRoot: append([]byte(nil), p.CurrAssetMerkleRoot...), FeeRate: highest}
	for _, item := range items {
		if len(ids) == 0 && item.UnitPrice.Int64() < highest {
			continue
		}
		dest := item.Address
		if len(item.Padded) > 0 {
			dest = string(item.Padded)
		}
		if deal.SendInfo[dest] == nil && len(deal.SendInfo) >= 9 {
			if len(ids) > 0 {
				return nil, fmt.Errorf("too many native recipients")
			}
			break
		}
		if deal.SendInfo[dest] == nil {
			deal.SendInfo[dest] = &SendAssetInfo{Address: dest, AssetName: p.GetAssetName()}
		}
		info := deal.SendInfo[dest]
		info.AssetAmt = info.AssetAmt.Add(item.RemainingAmt)
		info.Value += item.RemainingValue
		deal.ItemIDs = append(deal.ItemIDs, item.Id)
		deal.TotalAmt = deal.TotalAmt.Add(item.RemainingAmt)
		deal.TotalValue += item.RemainingValue
		h, _, _ := indexer.FromUtxoId(item.UtxoId)
		deal.Height = max(deal.Height, h)
	}
	if len(ids) > 0 && len(deal.ItemIDs) != len(ids) {
		return nil, fmt.Errorf("native withdrawal selects unavailable items")
	}
	return deal, nil
}
