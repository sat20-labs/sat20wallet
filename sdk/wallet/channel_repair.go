package wallet

import (
	"sort"

	indexer "github.com/sat20-labs/indexer/common"
)

func (p *ChannelInDB) ManagedL1UtxoMap() map[string]*indexer.TxOutput {
	result := make(map[string]*indexer.TxOutput)
	if p.ChanPoint != nil {
		result[p.ChanPoint.OutPointStr] = p.ChanPoint
	}
	for _, outputs := range p.FundingUtxos {
		for _, output := range outputs {
			if output != nil {
				result[output.OutPointStr] = output
			}
		}
	}
	for _, outputs := range p.StubUtxos {
		for _, output := range outputs {
			if output != nil {
				result[output.OutPointStr] = output
			}
		}
	}
	for _, output := range p.PendingUtxos {
		if output != nil {
			result[output.OutPointStr] = output
		}
	}
	return result
}

func (p *ChannelInDB) CommitmentInputMap() map[string]bool {
	result := make(map[string]bool)
	addInputs := func(commitment *ChannelCommitment) {
		if commitment == nil || commitment.CommitTx == nil {
			return
		}
		for _, in := range commitment.CommitTx.TxIn {
			result[in.PreviousOutPoint.String()] = true
		}
	}
	addInputs(p.LocalCommitment)
	addInputs(p.RemoteCommitment)
	return result
}

func (p *ChannelInDB) RemoveManagedL1Utxos(stale map[string]bool) []string {
	removed := make([]string, 0)
	if p.ChanPoint != nil && stale[p.ChanPoint.OutPointStr] {
		removed = append(removed, p.ChanPoint.OutPointStr)
		p.ChanPoint = nil
	}
	for name, outputs := range p.FundingUtxos {
		filtered, r := filterTxOutputs(outputs, stale)
		if len(filtered) == 0 {
			delete(p.FundingUtxos, name)
		} else {
			p.FundingUtxos[name] = filtered
		}
		removed = append(removed, r...)
	}
	for name, outputs := range p.StubUtxos {
		filtered, r := filterTxOutputs(outputs, stale)
		if len(filtered) == 0 {
			delete(p.StubUtxos, name)
		} else {
			p.StubUtxos[name] = filtered
		}
		removed = append(removed, r...)
	}
	toDelete := make([]string, 0)
	for utxo := range p.PendingUtxos {
		if stale[utxo] {
			removed = append(removed, utxo)
			toDelete = append(toDelete, utxo)
		}
	}
	for _, utxo := range toDelete {
		delete(p.PendingUtxos, utxo)
	}
	
	sort.Strings(removed)
	return removed
}

func filterTxOutputs(outputs []*indexer.TxOutput, stale map[string]bool) ([]*indexer.TxOutput, []string) {
	filtered := make([]*indexer.TxOutput, 0, len(outputs))
	removed := make([]string, 0)
	for _, output := range outputs {
		if output == nil {
			continue
		}
		if stale[output.OutPointStr] {
			removed = append(removed, output.OutPointStr)
			continue
		}
		filtered = append(filtered, output)
	}
	return filtered, removed
}
