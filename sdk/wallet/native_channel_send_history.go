package wallet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
)

// A mapped public source carries the complete public history, including
// already disclosed sibling returns/burns. Its ordinary wallet projection can
// legitimately retain an older immutable encoding used by a managed send.
// Build new public transfers from the independently authenticated inventory
// material without changing that earlier transfer's object or proof hashes.
func (p *rgb11Manager) mergeNativePublicSpendHistories(selected []rgb11SpendAllocation) (*coreconsignment.Container, string, error) {
	if p.channelSend == nil || !p.channelSend.preserveChannelValue || len(selected) == 0 {
		return nil, "", ErrRGB11Inconsistent
	}
	byPoint := make(map[string]rgb11SpendAllocation, len(selected))
	for _, allocation := range selected {
		if allocation.proof == nil || allocation.asset == nil || byPoint[allocation.proof.OutPoint].proof != nil {
			return nil, "", rgb11wallet.ErrInvalidProof
		}
		byPoint[allocation.proof.OutPoint] = allocation
	}
	points := make([]string, 0, len(byPoint))
	for point := range byPoint {
		points = append(points, point)
	}
	sort.Strings(points)
	owner := p.accountManagementOwner()
	ctx := context.Background()
	containers := make([]*coreconsignment.Container, 0, len(points))
	stateHasher := sha256.New()
	for _, point := range points {
		selected := byPoint[point]
		source, err := owner.loadNativeChannelSource(p.channelSend.data.ChannelID, point)
		if err != nil {
			return nil, "", err
		}
		validated, err := owner.validateNativeChannelSource(ctx, p.wallet, source.ChannelID, source, nativeproof.Current)
		if err != nil {
			return nil, "", err
		}
		if validated.Asset.Name != selected.asset.Name || validated.Asset.Amount.Cmp(&selected.asset.Amount) != 0 {
			return nil, "", fmt.Errorf("selected public native amount differs from its complete authenticated carrier")
		}
		if len(source.PrivateWitness) != 0 || len(source.DescendTxIDs) != 0 {
			if err := owner.ValidateNativeChannelReceipt(ctx, p.wallet, source.ChannelID, source); err != nil {
				return nil, "", err
			}
		}
		material, err := nativeproof.Decode(source.PublicProof)
		if err != nil {
			return nil, "", err
		}
		raw, err := rgb11wallet.PublicChannelConsignment(material.Consignment, material.Reveals)
		if err != nil {
			return nil, "", err
		}
		history, err := rgb11wallet.NewNativeConsensusValidator().ValidateHistoricalConsignmentReport(ctx, raw, p.evidence)
		if err != nil || history == nil || history.MaterialReceipt == nil {
			return nil, "", fmt.Errorf("native public history is not independently validated: %w", errRGB11ProjectionEvidence(err))
		}
		retained, err := p.projectionStore.LoadValidationReceipt(selected.proof.ConsignmentHash)
		if err != nil {
			return nil, "", err
		}
		output, err := p.projectionStore.LoadOutput(point)
		if err != nil {
			return nil, "", err
		}
		if retained.ContractID != history.MaterialReceipt.ContractID || retained.SchemaID != history.MaterialReceipt.SchemaID ||
			!rgb11ProofMatchesHistoricalReceipt(selected.proof, retained, output, history) {
			return nil, "", fmt.Errorf("native public inventory does not match the selected exact Opout")
		}
		container, err := coreconsignment.Decode(raw)
		if err != nil {
			return nil, "", err
		}
		containers = append(containers, container)
		stateHasher.Write(history.MaterialReceipt.StateHash[:])
	}
	merged, err := coreconsignment.MergeHistories(containers...)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrRGB11HistoryMerge, err)
	}
	return merged, hex.EncodeToString(stateHasher.Sum(nil)), nil
}
