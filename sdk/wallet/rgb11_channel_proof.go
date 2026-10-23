package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/seals"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

// RebuildRGB11TxOutput derives channel-signing amounts from a fully validated
// RGB transition. Bitcoin sat-range allocation cannot describe RGB outputs.
// This read-only operation neither imports assets nor changes channel state.
func (p *Manager) RebuildRGB11TxOutput(tx *wire.MsgTx, proof *wwire.RGB11SigningProof) ([]*TxOutput, []*TxOutput, error) {
	return p.rebuildRGB11TxOutput(tx, proof, false)
}

// Mixed custody sweeps may carry other protocols on separate inputs. Their
// assignments must be reconstructed independently, never inferred from RGB.
// Ordinary single-asset sends retain their stricter preservation boundary.
func (p *Manager) rebuildRGB11TxOutput(tx *wire.MsgTx, proof *wwire.RGB11SigningProof, mixedSweep bool) ([]*TxOutput, []*TxOutput, error) {
	if p == nil || p.l1IndexerClient == nil || tx == nil || proof == nil || len(proof.Consignment) == 0 ||
		len(proof.Consignment) > 4*1024*1024 || len(proof.ChangeSeals) > 1024 {
		return nil, nil, ErrRGB11Inconsistent
	}
	reveals := make([]seals.GraphBlindSeal, len(proof.ChangeSeals))
	for i, raw := range proof.ChangeSeals {
		seal, err := seals.DecodeGraphBlindSeal(raw)
		if err != nil {
			return nil, nil, err
		}
		reveals[i] = seal
	}
	evidence := newIndexerBitcoinEvidenceProvider(p.l1IndexerClient)
	var provider rgb11wallet.BitcoinEvidenceProvider = evidence
	if p.rgbManager != nil && p.rgbManager.evidence != nil {
		provider = p.rgbManager.evidence
	}
	validator := rgb11wallet.NewNativeConsensusValidatorWithReveals(reveals...)
	prepared, err := validator.ValidatePreparedConsignment(context.Background(), proof.Consignment, provider)
	if err != nil {
		return nil, nil, err
	}
	if prepared == nil || prepared.Receipt == nil {
		return nil, nil, ErrRGB11Inconsistent
	}
	history, err := validator.ValidatePreparedHistoryConsignmentReport(context.Background(), proof.Consignment, provider)
	if err != nil {
		return nil, nil, err
	}
	// Only the core's validated Opout-to-transition edges identify what this
	// particular Bitcoin transaction consumes. The Bitcoin indexer has no RGB
	// input balances, and a terminal allocation alone does not prove its inputs.
	consumed := make(map[string]bool)
	for _, edge := range history.Consumers {
		if edge.WitnessTxID == tx.TxID() {
			consumed[nativeAllocationID(edge.InputOperationID, edge.AssignmentType, edge.AssignmentIndex)] = true
		}
	}
	nativeInputs := make(map[string]indexer.TxAssets)
	for _, historical := range history.HistoricalAllocations {
		allocation := historical.Allocation
		id := nativeAllocationID(allocation.OperationID, allocation.AssignmentType, allocation.AssignmentIndex)
		if !consumed[id] {
			continue
		}
		if allocation.AssetName.Protocol != rgb11wallet.Protocol || allocation.AssetName.Type != indexer.ASSET_TYPE_FT || allocation.Amount.Sign() <= 0 {
			return nil, nil, ErrRGB11AssetPreservation
		}
		assets := nativeInputs[allocation.OutPoint]
		if err := assets.Add(&indexer.AssetInfo{Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0}); err != nil {
			return nil, nil, err
		}
		nativeInputs[allocation.OutPoint] = assets
		delete(consumed, id)
	}
	if len(consumed) != 0 || len(nativeInputs) == 0 {
		return nil, nil, fmt.Errorf("RGB11 signing proof has incomplete consumed allocation evidence")
	}
	inputs := make([]*TxOutput, 0, len(tx.TxIn))
	publicInputs := make(map[string]*TxOutput, len(tx.TxIn))
	knownRGB := make(map[indexer.AssetName]bool)
	inputTotals := make(map[indexer.AssetName]*indexer.Decimal)
	for _, input := range tx.TxIn {
		output, err := p.l1IndexerClient.GetTxOutput(input.PreviousOutPoint.String())
		if err != nil || output == nil {
			return nil, nil, fmt.Errorf("resolve RGB11 signing input: %w", err)
		}
		for _, asset := range output.Assets {
			if !indexer.IsPlainAsset(&asset.Name) &&
				(!mixedSweep || len(nativeInputs[input.PreviousOutPoint.String()]) != 0 || asset.Name.Protocol == rgb11wallet.Protocol) {
				return nil, nil, ErrRGB11AssetPreservation
			}
		}
		publicInputs[input.PreviousOutPoint.String()] = output.Clone()
		// The indexer cannot see RGB allocations. Include the local validated
		// view so a proof for one contract cannot hide another on its carrier.
		if p.rgbManager != nil && p.rgbManager.projectionStore != nil {
			projected, err := p.rgbManager.projectionStore.LoadOutput(input.PreviousOutPoint.String())
			if err != nil && !errors.Is(err, indexer.ErrKeyNotFound) {
				return nil, nil, err
			}
			if projected != nil {
				for _, asset := range projected.Assets {
					if asset.Name.Protocol != rgb11wallet.Protocol {
						continue
					}
					if err := p.rgbManager.projectionStore.AssertConsistent(input.PreviousOutPoint.String(), asset.Name); err != nil {
						return nil, nil, ErrRGB11AssetPreservation
					}
					knownRGB[asset.Name] = true
				}
			}
		}
		bitcoin, err := provider.GetUTXO(input.PreviousOutPoint.String())
		if err != nil || bitcoin == nil || bitcoin.Value != output.Value() || !bytes.Equal(bitcoin.PkScript, output.OutValue.PkScript) {
			return nil, nil, fmt.Errorf("RGB11 signing input carrier is not the current exact Bitcoin output: %w", err)
		}
		resolved := output.Clone()
		resolved.Assets = nativeInputs[input.PreviousOutPoint.String()]
		for _, asset := range resolved.Assets {
			inputTotals[asset.Name] = inputTotals[asset.Name].Add(&asset.Amount)
		}
		delete(nativeInputs, input.PreviousOutPoint.String())
		inputs = append(inputs, resolved)
	}
	if len(nativeInputs) != 0 {
		return nil, nil, fmt.Errorf("RGB11 consumer graph does not match Bitcoin inputs")
	}
	outputs := make([]*TxOutput, len(tx.TxOut))
	txid := tx.TxID()
	for i, output := range tx.TxOut {
		outputs[i] = indexer.NewTxOutput(output.Value)
		outputs[i].OutPointStr = fmt.Sprintf("%s:%d", txid, i)
		outputs[i].OutValue.PkScript = append([]byte(nil), output.PkScript...)
	}
	projected := 0
	outputTotals := make(map[indexer.AssetName]*indexer.Decimal)
	for _, allocation := range prepared.Receipt.Allocations {
		point, err := wire.NewOutPointFromString(allocation.OutPoint)
		if err != nil {
			return nil, nil, err
		}
		if point.Hash.String() != txid {
			continue
		}
		if uint64(point.Index) >= uint64(len(outputs)) {
			return nil, nil, ErrRGB11Inconsistent
		}
		bitcoin := prepared.Outputs[allocation.OutPoint]
		if bitcoin == nil {
			bitcoin, err = provider.GetUTXO(allocation.OutPoint)
		}
		if err != nil || bitcoin == nil || bitcoin.Value != tx.TxOut[point.Index].Value ||
			!bytes.Equal(bitcoin.PkScript, tx.TxOut[point.Index].PkScript) {
			return nil, nil, ErrRGB11Inconsistent
		}
		if err := outputs[point.Index].Assets.Add(&indexer.AssetInfo{Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0}); err != nil {
			return nil, nil, err
		}
		projected++
		outputTotals[allocation.AssetName] = outputTotals[allocation.AssetName].Add(&allocation.Amount)
		delete(knownRGB, allocation.AssetName)
	}
	if projected == 0 {
		return nil, nil, fmt.Errorf("RGB11 proof does not authorize this transaction")
	}
	if len(knownRGB) != 0 {
		return nil, nil, ErrRGB11AssetPreservation
	}
	if len(inputTotals) != len(outputTotals) {
		return nil, nil, ErrRGB11AssetPreservation
	}
	for name, amount := range inputTotals {
		if amount.Cmp(outputTotals[name]) != 0 {
			return nil, nil, ErrRGB11AssetPreservation
		}
	}
	if mixedSweep {
		publicIn, publicOut, err := p.RebuildTxOutput(tx, publicInputs)
		if err != nil {
			return nil, nil, err
		}
		if len(publicIn) != len(inputs) || len(publicOut) != len(outputs) {
			return nil, nil, ErrRGB11AssetPreservation
		}
		for i, input := range inputs {
			if publicIn[i].OutPointStr != input.OutPointStr {
				return nil, nil, ErrRGB11AssetPreservation
			}
			for _, asset := range input.Assets {
				if err := publicIn[i].Assets.Add(&asset); err != nil {
					return nil, nil, err
				}
			}
		}
		for i, output := range outputs {
			for _, asset := range output.Assets {
				if err := publicOut[i].Assets.Add(&asset); err != nil {
					return nil, nil, err
				}
			}
		}
		return publicIn, publicOut, nil
	}
	return inputs, outputs, nil
}
