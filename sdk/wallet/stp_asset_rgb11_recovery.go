package wallet

import (
	"context"
	"fmt"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

func (rgb11STPProtocol) PrepareOtherSweep(_ *Manager, req STPAssetOtherSweepRequest) (*STPAssetOtherSweepPlan, error) {
	plan := &STPAssetOtherSweepPlan{FeeInputs: req.FeeInputs}
	for _, output := range req.Outputs {
		if output == nil || output.Amt == nil || output.Amt.Sign() <= 0 || output.Index < 0 || output.Index >= len(req.CommitTx.TxOut) {
			return nil, fmt.Errorf("invalid RGB11 other-sweep allocation")
		}
		input := indexer.GenerateTxOutput(req.CommitTx, output.Index)
		input.Assets = indexer.TxAssets{{Name: output.AssetName.AssetName, Amount: *output.Amt.Clone(), BindingSat: 0}}
		plan.Transfers = append(plan.Transfers, input)
	}
	return plan, nil
}

// BuildChannelRecovery spends real terminal states of an authenticated
// commitment. Other protocols keep their input/output sat order; RGB adds only
// a zero-value DBC and its native transition. Fee inputs are consumed once.
func (protocol rgb11STPProtocol) BuildChannelRecovery(p *Manager, req STPAssetRecoveryRequest) (*STPAssetRecoveryPlan, error) {
	if req.Commitment == nil || req.Commitment.CommitTx == nil || len(req.RecipientScript) == 0 || req.FeeRate <= 0 {
		return nil, fmt.Errorf("incomplete RGB11 recovery request")
	}
	raw := req.Commitment.AssetProofs[rgb11wallet.Protocol]
	if len(raw) == 0 {
		return nil, fmt.Errorf("RGB11 recovery is missing its native commitment proof")
	}
	pkg, err := rgb11wallet.DecodeChannelPackage(raw)
	if err != nil {
		return nil, err
	}
	views, err := protocol.VerifyChannelProof(p, req.Channel, req.Commitment.CommitTx, raw, req.Operation)
	if err != nil {
		return nil, err
	}
	last := pkg.Steps[len(pkg.Steps)-1]
	byPoint := make(map[string]rgb11wallet.ChannelFunding)
	for _, assignment := range last.Assignments {
		source, err := rgb11wallet.ChannelFundingFromStep(last, assignment.OutputIndex, assignment.Amount)
		if err != nil {
			return nil, err
		}
		byPoint[source.OutPoint] = source
	}
	inputs := append([]*TxOutput(nil), req.Inputs...)
	fees := append([]*TxOutput(nil), req.FeeInputs...)
	if inputs == nil {
		indexes, err := sortedSTPRGB11Indexes(req.OutputIndexes)
		if err != nil || len(indexes) == 0 {
			return nil, fmt.Errorf("invalid native recovery selection")
		}
		recovery, err := stpRecoveryInputs(req.Commitment, req.Commitment.LocalBalance, req.Operation)
		if err != nil {
			return nil, err
		}
		feeIndexes, err := commitmentRecoveryFeeIndexes(req.Commitment, indexes, views)
		if err != nil {
			return nil, err
		}
		for _, index := range indexes {
			if index >= len(views) || txscript.IsUnspendable(views[index].OutValue.PkScript) {
				return nil, fmt.Errorf("invalid native recovery output")
			}
			input := views[index]
			_, native := byPoint[input.OutPointStr]
			if feeIndexes[index] {
				if native {
					return nil, fmt.Errorf("RGB11 allocation is marked as a recovery fee reserve")
				}
				fees = append(fees, input)
			} else {
				inputs = append(inputs, input)
			}
		}
		inputs = append(inputs, recovery.Transfers...)
	}
	var sources []rgb11wallet.ChannelFunding
	for _, input := range inputs {
		if input == nil {
			return nil, fmt.Errorf("nil native recovery input")
		}
		if source, exists := byPoint[input.OutPointStr]; exists {
			sources = append(sources, source)
		}
	}
	if len(sources) == 0 {
		return &STPAssetRecoveryPlan{NoNativeAssets: true}, nil
	}
	tx := wire.NewMsgTx(2)
	tx.LockTime = req.Height
	dbc, err := rgb11wallet.ReserveChannelDBC(tx, 0)
	if err != nil {
		return nil, err
	}
	var weight utils.TxWeightEstimator
	weight.AddTxOutput(dbc)
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	used := make(map[string]bool)
	addInput := func(output *TxOutput) error {
		if output == nil || used[output.OutPointStr] {
			return fmt.Errorf("duplicate native recovery input")
		}
		point, err := wire.NewOutPointFromString(output.OutPointStr)
		if err != nil {
			return err
		}
		used[output.OutPointStr] = true
		sequence := req.CSVDelay
		if req.Operation == STPOpPunish {
			sequence = wire.MaxTxInSequenceNum
		}
		tx.AddTxIn(&wire.TxIn{PreviousOutPoint: *point, Sequence: sequence})
		fetcher.AddPrevOut(*point, output.TxOut())
		if req.Branch == "other-sweep" {
			weight.AddWitnessInput(utils.MultiSigWitnessSize)
		} else {
			weight.AddNestedP2WSHInput(int64(len(req.CommitmentScript)))
		}
		return nil
	}
	var assignments []rgb11wallet.ChannelAssignment
	for _, input := range inputs {
		if err := addInput(input); err != nil {
			return nil, err
		}
		output := wire.NewTxOut(input.Value(), append([]byte(nil), req.RecipientScript...))
		if source, exists := byPoint[input.OutPointStr]; exists {
			if source.Value != input.Value() {
				return nil, fmt.Errorf("RGB11 recovery changed a native carrier")
			}
			assignments = append(assignments, rgb11wallet.ChannelAssignment{OutputIndex: uint32(len(tx.TxOut)), Amount: *source.Amount.Clone()})
		}
		tx.AddTxOut(output)
		weight.AddTxOutput(output)
	}
	var feeValue int64
	for _, input := range fees {
		if input == nil || input.HasAsset() {
			return nil, fmt.Errorf("native recovery fee input carries assets")
		}
		if _, exists := byPoint[input.OutPointStr]; exists {
			return nil, fmt.Errorf("RGB11 carrier cannot pay native recovery fees")
		}
		if err := addInput(input); err != nil {
			return nil, err
		}
		feeValue += input.Value()
	}
	fee, err := addSweepFeeChange(tx, &weight, feeValue, req.FeeRate, req.RecipientScript)
	if err != nil {
		return nil, err
	}
	channelContext := last.Context
	channelContext.Operation, channelContext.Branch = string(req.Operation), req.Branch
	if channelContext.Branch == "" {
		channelContext.Branch = "recovery"
	}
	evidence, err := stpRGB11Evidence(p)
	if err != nil {
		return nil, err
	}
	step, err := rgb11wallet.BuildChannelStep(context.Background(), sources, tx, 0, assignments, channelContext, evidence)
	if err != nil {
		return nil, err
	}
	child := &rgb11wallet.ChannelPackage{Version: pkg.Version, Funding: pkg.Funding,
		Steps: append(append([]rgb11wallet.ChannelStep(nil), pkg.Steps...), *step)}
	if _, err := rgb11wallet.ValidateChannelPackage(context.Background(), child, tx, evidence); err != nil {
		return nil, err
	}
	proof, err := rgb11wallet.EncodeChannelPackage(child)
	if err != nil {
		return nil, err
	}
	return &STPAssetRecoveryPlan{Tx: tx, PrevFetcher: fetcher, Fee: fee, NativeProofs: map[string][]byte{rgb11wallet.Protocol: proof}}, nil
}
