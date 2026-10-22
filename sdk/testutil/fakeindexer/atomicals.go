package fakeindexer

import (
	"fmt"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/atom/coloring"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
)

// atomicalsAllocationsLocked derives ARC20 output state from the same native
// coloring rules used by the production indexer. Initial fixture holdings are
// explicit SeedOutput allocations; ordinary broadcasts cannot create units.
// The caller holds Network.Mutex, and this preflight never mutates the model.
func (p *Client) atomicalsAllocationsLocked(tx *wire.MsgTx) (map[int]indexer.TxAssets, error) {
	if !p.Network.IsBitcoinNet() || len(tx.TxIn) == 0 || string(tx.TxIn[0].SignatureScript) == "genesis" {
		return nil, nil
	}
	hasNative := false
	for _, input := range tx.TxIn {
		if input == nil {
			return nil, fmt.Errorf("nil fixture transaction input")
		}
		if position, ok := p.Network.UtxoIndex[input.PreviousOutPoint.String()]; ok {
			for _, asset := range p.Network.UtxoAssets[position] {
				hasNative = hasNative || asset.Name.Protocol == indexer.PROTOCOL_NAME_ATOM
			}
		}
	}
	if !hasNative {
		return nil, nil
	}
	heights, err := coloring.HeightsForNetwork(wallet.GetChainParam().Net)
	if err != nil {
		return nil, err
	}
	facts := make([]coloring.Input, len(tx.TxIn))
	names := make(map[string]indexer.AssetName)
	var inputValue int64
	for vin, input := range tx.TxIn {
		outpoint := input.PreviousOutPoint.String()
		position, ok := p.Network.UtxoIndex[outpoint]
		if !ok || p.Network.UtxoUsed[outpoint] != "" {
			return nil, fmt.Errorf("ARC20 fixture requires unspent native prevout %s", outpoint)
		}
		facts[vin].Value = p.Network.UtxoValue[position]
		if facts[vin].Value < 0 || facts[vin].Value > 21_000_000*100_000_000-inputValue {
			return nil, fmt.Errorf("invalid ARC20 fixture Bitcoin input budget")
		}
		inputValue += facts[vin].Value
		for _, asset := range p.Network.UtxoAssets[position] {
			if asset.Name.Protocol != indexer.PROTOCOL_NAME_ATOM {
				continue
			}
			metadata := p.Network.State.Tickers[asset.Name.String()]
			if metadata == nil || metadata.AssetName != asset.Name || metadata.N != 1 || metadata.Divisibility != 0 || asset.Name.Type != indexer.ASSET_TYPE_FT || asset.BindingSat != 1 {
				return nil, fmt.Errorf("invalid ARC20 fixture ticker metadata")
			}
			if len(p.Network.Offsets[position][asset.Name]) != 0 || p.Network.Invalids[outpoint][asset.Name] || asset.Amount.Value == nil || asset.Amount.Precision != 0 || !asset.Amount.Value.IsInt64() {
				return nil, fmt.Errorf("invalid ARC20 fixture allocation facts")
			}
			if other, exists := names[metadata.InscriptionId]; exists && other != asset.Name {
				return nil, fmt.Errorf("inconsistent ARC20 fixture native identity")
			}
			names[metadata.InscriptionId] = asset.Name
			facts[vin].Assets = append(facts[vin].Assets, coloring.Asset{AtomicalID: metadata.InscriptionId, Amount: asset.Amount.Int64()})
		}
	}
	result, err := coloring.ValidateRegularTransfer(tx, facts, p.Network.Height, heights)
	if err != nil {
		return nil, err
	}
	for _, output := range tx.TxOut {
		if output.Value < 0 || output.Value > inputValue {
			return nil, fmt.Errorf("invalid ARC20 fixture Bitcoin output budget")
		}
		inputValue -= output.Value
	}
	allocations := make(map[int]indexer.TxAssets)
	for _, id := range result.Order {
		for _, allocation := range result.Assignments[id] {
			assets := allocations[allocation.OutputIndex]
			asset := indexer.AssetInfo{Name: names[id], Amount: *indexer.NewDefaultDecimal(allocation.Amount), BindingSat: 1}
			if err := assets.Add(&asset); err != nil {
				return nil, err
			}
			allocations[allocation.OutputIndex] = assets
		}
	}
	return allocations, nil
}

func atomicalsOrdinalAssets(assets indexer.TxAssets) indexer.TxAssets {
	var ordinal indexer.TxAssets
	for _, asset := range assets {
		if asset.Name.Protocol != indexer.PROTOCOL_NAME_ATOM {
			ordinal = append(ordinal, *asset.Clone())
		}
	}
	return ordinal
}
