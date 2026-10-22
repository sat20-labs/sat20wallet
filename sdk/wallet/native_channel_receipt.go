package wallet

import (
	"context"
	"fmt"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/seals"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
)

// ExportNativeChannelSettlementReceipt exports only currently backed public
// inventory. Private channel packages, entropy, revocation material and wallet
// recovery state never cross this boundary.
func (p *Manager) ExportNativeChannelSettlementReceipt(ctx context.Context, local common.Wallet,
	channelID, outpoint string) (*wwire.NativeChannelSettlementReceipt, error) {
	source, err := p.loadNativeChannelSource(channelID, outpoint)
	if err != nil {
		return nil, err
	}
	if err := p.ValidateNativeChannelReceipt(ctx, local, channelID, source); err != nil {
		return nil, err
	}
	return &wwire.NativeChannelSettlementReceipt{Version: 1,
		PrivateWitness: append([]byte(nil), source.PrivateWitness...), SourceOutpoint: source.Output.OutPointStr,
		PublicProof: append([]byte(nil), source.PublicProof...), DescendTxIDs: append([]string(nil), source.DescendTxIDs...)}, nil
}

// RegisterNativeChannelSettlementReceipt independently reconstructs the exact
// source from canonical chain facts and RGB core's validated allocation report.
// A receiving pool owner does not trust the sender's amount, indexer balances,
// acknowledgement, or assertions about which private states were released.
func (p *Manager) RegisterNativeChannelSettlementReceipt(ctx context.Context, local common.Wallet,
	channelID string, receipt *wwire.NativeChannelSettlementReceipt) error {
	source, err := p.nativeChannelSourceFromReceipt(ctx, local, channelID, receipt)
	if err != nil {
		return err
	}
	if err := p.ValidateNativeChannelReceipt(ctx, local, channelID, source); err != nil {
		return err
	}
	return p.RegisterNativeChannelSettlementSource(ctx, local, channelID, source)
}

func (p *Manager) nativeChannelSourceFromReceipt(ctx context.Context, local common.Wallet,
	channelID string, receipt *wwire.NativeChannelSettlementReceipt) (*NativeChannelSource, error) {
	if receipt == nil || receipt.Version != 1 || local == nil || len(receipt.PublicProof) == 0 ||
		len(receipt.PrivateWitness) > 10_000 || len(receipt.DescendTxIDs) > 4096 {
		return nil, fmt.Errorf("invalid native settlement receipt")
	}
	point, err := wire.NewOutPointFromString(receipt.SourceOutpoint)
	if err != nil || point.String() != receipt.SourceOutpoint {
		return nil, fmt.Errorf("noncanonical native receipt source")
	}
	chains, err := p.newNativeSettlementChains(ctx, local)
	if err != nil {
		return nil, err
	}
	tx, err := chains.bitcoinTx(point.Hash.String(), true)
	if err != nil {
		return nil, err
	}
	if uint64(point.Index) >= uint64(len(tx.TxOut)) || tx.TxOut[point.Index] == nil {
		return nil, fmt.Errorf("native receipt carrier is absent from its Bitcoin transaction")
	}
	material, err := nativeproof.Decode(receipt.PublicProof)
	if err != nil {
		return nil, err
	}
	reveals := make([]seals.GraphBlindSeal, 0, len(material.Reveals))
	for _, raw := range material.Reveals {
		seal, err := seals.DecodeGraphBlindSeal(raw)
		if err != nil {
			return nil, err
		}
		reveals = append(reveals, seal)
	}
	history, err := rgb11wallet.NewNativeConsensusValidatorWithReveals(reveals...).ValidateHistoricalConsignmentReport(ctx,
		material.Consignment, chains.bitcoin)
	if err != nil || history == nil || history.MaterialReceipt == nil {
		return nil, fmt.Errorf("native receipt has no validated historical allocations: %w", errRGB11ProjectionEvidence(err))
	}
	var asset *indexer.AssetInfo
	for _, item := range history.Allocations {
		allocation := item.Allocation
		if allocation.OutPoint != receipt.SourceOutpoint {
			continue
		}
		if allocation.AssignmentType != 4000 || allocation.StateClass != "fungible" || allocation.AssetName.Protocol != rgb11wallet.Protocol || allocation.Amount.Validate() != nil {
			return nil, fmt.Errorf("native receipt source contains an unsupported control allocation")
		}
		if asset == nil {
			asset = &indexer.AssetInfo{Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0}
		} else if asset.Name != allocation.AssetName || asset.Amount.Precision != allocation.Amount.Precision {
			return nil, fmt.Errorf("native receipt source has incompatible allocations")
		} else {
			asset.Amount = *asset.Amount.Add(&allocation.Amount)
		}
	}
	if asset == nil || asset.Amount.Sign() <= 0 {
		return nil, fmt.Errorf("native receipt has no exact fungible source amount")
	}
	output := indexer.GenerateTxOutput(tx, int(point.Index))
	output.Assets = indexer.TxAssets{*asset}
	if err := chains.stable(); err != nil {
		return nil, err
	}
	return &NativeChannelSource{Version: 1, ChannelID: channelID, Output: output,
		PublicProof: append([]byte(nil), receipt.PublicProof...), PrivateWitness: append([]byte(nil), receipt.PrivateWitness...),
		DescendTxIDs: append([]string(nil), receipt.DescendTxIDs...)}, nil
}
