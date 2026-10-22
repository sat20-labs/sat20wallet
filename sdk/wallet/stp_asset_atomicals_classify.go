package wallet

import (
	"bytes"
	"fmt"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/atom/coloring"
)

func (atomicalsSTPProtocol) ClassifyOutputs(p *Manager, tx *wire.MsgTx, inputs, outputs []*TxOutput) error {
	return p.ClassifySTPAtomicalsOutputs(tx, inputs, outputs)
}

// ClassifySTPAtomicalsOutputs restores native ARC20 allocations after the
// ordinal protocols have classified their outputs. The complete prevout facts
// must be retained in transaction input order; token quantities are never
// reconstructed from ordinal offsets or from output Bitcoin values alone.
func (p *Manager) ClassifySTPAtomicalsOutputs(tx *wire.MsgTx, inputs, outputs []*TxOutput) error {
	hasAtomicals := false
	for _, input := range inputs {
		if input == nil {
			return fmt.Errorf("missing previous output for native classification")
		}
		for _, asset := range input.Assets {
			if asset.Name.Protocol == indexer.PROTOCOL_NAME_ATOM {
				hasAtomicals = true
			}
		}
	}
	if !hasAtomicals {
		return nil
	}
	if tx == nil || len(inputs) != len(tx.TxIn) || len(outputs) != len(tx.TxOut) {
		return fmt.Errorf("ARC20 classification requires complete transaction facts")
	}
	evidence, err := p.newAtomicalsEvidence()
	if err != nil {
		return err
	}
	facts := make([]coloring.Input, len(inputs))
	names := make(map[string]indexer.AssetName)
	var inputValue int64
	for position, input := range inputs {
		if tx.TxIn[position] == nil || input.OutPoint() == nil || *input.OutPoint() != tx.TxIn[position].PreviousOutPoint {
			return fmt.Errorf("ARC20 classification input identity mismatch")
		}
		facts[position].Value = input.Value()
		inputValue, err = atomicalsAdd(inputValue, input.Value())
		if err != nil {
			return err
		}
		for _, asset := range input.Assets {
			if asset.Name.Protocol != indexer.PROTOCOL_NAME_ATOM {
				continue
			}
			name := &AssetName{AssetName: asset.Name, N: 1}
			if err := (atomicalsSTPProtocol{}).VerifyInput(name, input); err != nil {
				return err
			}
			id, err := p.atomicalsIdentity(name)
			if err != nil {
				return err
			}
			if other, exists := names[id]; exists && other != asset.Name {
				return fmt.Errorf("ARC20 native identity maps to inconsistent tickers")
			}
			names[id] = asset.Name
			facts[position].Assets = append(facts[position].Assets, coloring.Asset{AtomicalID: id, Amount: asset.Amount.Int64()})
		}
	}
	for position, output := range outputs {
		if output == nil || tx.TxOut[position] == nil || output.Value() != tx.TxOut[position].Value || !bytes.Equal(output.OutValue.PkScript, tx.TxOut[position].PkScript) {
			return fmt.Errorf("ARC20 classification output identity mismatch")
		}
		if output.Value() < 0 || output.Value() > inputValue {
			return fmt.Errorf("ARC20 classification has an invalid Bitcoin output budget")
		}
		inputValue -= output.Value()
		for _, asset := range output.Assets {
			if asset.Name.Protocol == indexer.PROTOCOL_NAME_ATOM {
				return fmt.Errorf("ARC20 output was already classified")
			}
		}
	}
	result, err := coloring.ValidateRegularTransfer(tx, facts, evidence.height, evidence.heights)
	if err != nil {
		return err
	}
	// Compute into copies so a failure cannot leave a partially classified view.
	classified := make([]indexer.TxAssets, len(outputs))
	for position, output := range outputs {
		classified[position] = output.Assets.Clone()
	}
	for _, id := range result.Order {
		for _, allocation := range result.Assignments[id] {
			asset := indexer.AssetInfo{Name: names[id], Amount: *indexer.NewDefaultDecimal(allocation.Amount), BindingSat: 1}
			if err := classified[allocation.OutputIndex].Add(&asset); err != nil {
				return err
			}
		}
	}
	for position, assets := range classified {
		outputs[position].Assets = assets
	}
	return nil
}
