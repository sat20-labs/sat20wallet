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
	inputs := make([]*TxOutput, 0, len(tx.TxIn))
	knownRGB := make(map[indexer.AssetName]bool)
	for _, input := range tx.TxIn {
		output, err := p.l1IndexerClient.GetTxOutput(input.PreviousOutPoint.String())
		if err != nil || output == nil {
			return nil, nil, fmt.Errorf("resolve RGB11 signing input: %w", err)
		}
		for _, asset := range output.Assets {
			if !indexer.IsPlainAsset(&asset.Name) {
				return nil, nil, ErrRGB11AssetPreservation
			}
		}
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
		inputs = append(inputs, output.Clone())
	}
	outputs := make([]*TxOutput, len(tx.TxOut))
	txid := tx.TxID()
	for i, output := range tx.TxOut {
		outputs[i] = indexer.NewTxOutput(output.Value)
		outputs[i].OutPointStr = fmt.Sprintf("%s:%d", txid, i)
		outputs[i].OutValue.PkScript = append([]byte(nil), output.PkScript...)
	}
	projected := 0
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
		delete(knownRGB, allocation.AssetName)
	}
	if projected == 0 {
		return nil, nil, fmt.Errorf("RGB11 proof does not authorize this transaction")
	}
	if len(knownRGB) != 0 {
		return nil, nil, ErrRGB11AssetPreservation
	}
	return inputs, outputs, nil
}
