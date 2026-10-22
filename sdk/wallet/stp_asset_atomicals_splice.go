package wallet

import (
	"bytes"
	"fmt"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

func (protocol atomicalsSTPProtocol) SplitOutputs(inputs []*TxOutput, name *AssetName, value int64, amount *Decimal) (*TxOutput, *TxOutput, error) {
	requested, err := atomicalsAmount(amount)
	if err != nil || requested <= 0 || name == nil || name.N != 1 {
		return nil, nil, fmt.Errorf("invalid ARC20 output allocation")
	}
	var assetValue, totalAmount int64
	seen := make(map[wire.OutPoint]bool)
	for _, input := range inputs {
		var inputAmount int64
		if input != nil && len(input.Assets) > 0 {
			if err := protocol.VerifyInput(name, input); err != nil {
				return nil, nil, err
			}
			inputAmount = input.GetAsset(&name.AssetName).Int64()
			assetValue, err = atomicalsAdd(assetValue, input.Value())
			if err != nil {
				return nil, nil, err
			}
		} else if err := atomicalsPlainInput(input); err != nil {
			return nil, nil, err
		}
		point := input.TxIn().PreviousOutPoint
		if seen[point] {
			return nil, nil, fmt.Errorf("duplicate ARC20 allocation input")
		}
		seen[point] = true
		totalAmount, err = atomicalsAdd(totalAmount, inputAmount)
		if err != nil {
			return nil, nil, err
		}
	}
	if requested > totalAmount {
		return nil, nil, fmt.Errorf("insufficient ARC20 allocation")
	}
	if value == 0 {
		value = requested
		if value < 330 {
			value = 330
		}
	}
	if value < requested || value < 330 || (requested < totalAmount && value != requested) {
		return nil, nil, fmt.Errorf("regular ARC20 partial transfer must put exact token value in its first output")
	}
	output := indexer.NewTxOutput(value)
	output.Assets = indexer.TxAssets{{Name: name.AssetName, Amount: *indexer.NewDefaultDecimal(requested), BindingSat: 1}}
	var change *TxOutput
	if totalAmount > requested {
		// Plain fee inputs have their own change output. Only the remaining
		// native carrier belongs here; otherwise splice-in would persist a
		// change value containing the entire fee budget rather than vout 1.
		// The complete transaction may top up this carrier from those fees.
		changeValue := assetValue - value
		if changeValue < 330 {
			changeValue = 330
		}
		if totalAmount-requested > changeValue {
			return nil, nil, fmt.Errorf("ARC20 change exceeds its Bitcoin capacity")
		}
		change = indexer.NewTxOutput(changeValue)
		change.Assets = indexer.TxAssets{{Name: name.AssetName, Amount: *indexer.NewDefaultDecimal(totalAmount - requested), BindingSat: 1}}
	}
	return output, change, nil
}

func (protocol atomicalsSTPProtocol) PlanSplice(p *Manager, resv *SplicingReservation, candidates, fees []*TxOutput, needStub int, stubValue int64, operation STPAssetOperation) error {
	if resv == nil || resv.Channel == nil || resv.AssetName == nil || resv.FeeRate <= 0 || (operation != STPOpSpliceIn && operation != STPOpSpliceOut) {
		return fmt.Errorf("invalid ARC20 splice request")
	}
	if err := protocol.ValidateAsset(&resv.AssetName.AssetName, operation); err != nil {
		return err
	}
	if _, err := p.atomicalsIdentity(resv.AssetName); err != nil {
		return err
	}
	if _, err := p.newAtomicalsEvidence(); err != nil {
		return err
	}
	amount, err := atomicalsAmount(resv.Amt)
	if err != nil || amount <= 0 || needStub < 0 || stubValue < 0 {
		return fmt.Errorf("invalid ARC20 splice quantity")
	}
	if resv.StubUtxo != nil || resv.StubIsSet {
		return fmt.Errorf("ARC20 splicing does not use an ordinal input stub")
	}
	channel := resv.Channel
	channelScript := channel.GetChannelPkScript()
	owner := channel.GetLocalPkScript()
	if !resv.IsInitiator {
		owner = channel.GetRemotePkScript()
	}
	var serviceFee int64
	if operation == STPOpSpliceOut {
		if channel.FeeCfg == nil {
			return fmt.Errorf("missing ARC20 channel fee policy")
		}
		fromInitiator := resv.IsInitiator == channel.IsInitiator
		if err := channel.HasEnoughAsset(resv.AssetName, resv.Amt, len(fees) != 0, fromInitiator); err != nil {
			return err
		}
		serviceFee = CalcSplicingOutServiceFee(resv.Amt, fromInitiator, channel.FeeCfg)
		needStub = 0
	}
	if len(fees) == 0 {
		return fmt.Errorf("ARC20 splice requires explicit plain fee inputs")
	}
	var selected []*TxOutput
	var inputAmount, inputValue, feeValue int64
	seen := make(map[wire.OutPoint]bool)
	var weight utils.TxWeightEstimator
	weight.AddWitnessInput(utils.MultiSigWitnessSize) // existing plain chanpoint
	weight.AddP2WSHOutput()
	for _, input := range candidates {
		if err := protocol.VerifyInput(resv.AssetName, input); err != nil {
			return err
		}
		expectedOwner := owner
		if operation == STPOpSpliceOut {
			expectedOwner = channelScript
		}
		if !bytes.Equal(input.OutValue.PkScript, expectedOwner) {
			return fmt.Errorf("ARC20 splice asset input owner mismatch")
		}
		point := input.TxIn().PreviousOutPoint
		if seen[point] {
			return fmt.Errorf("duplicate ARC20 splice input")
		}
		seen[point] = true
		inputAmount, err = atomicalsAdd(inputAmount, input.GetAsset(&resv.AssetName.AssetName).Int64())
		if err != nil {
			return err
		}
		inputValue, err = atomicalsAdd(inputValue, input.Value())
		if err != nil {
			return err
		}
		selected = append(selected, input.Clone())
		if operation == STPOpSpliceOut {
			weight.AddWitnessInput(utils.MultiSigWitnessSize)
		} else {
			weight.AddTaprootKeySpendInput(txscript.SigHashDefault)
		}
		if operation == STPOpSpliceOut && inputAmount >= amount {
			break
		}
	}
	if inputAmount < amount {
		return fmt.Errorf("insufficient ARC20 splice assets")
	}
	if inputAmount > amount && amount < 330 {
		return fmt.Errorf("regular ARC20 splice vout 0 cannot carry a sub-dust partial amount")
	}
	var selectedFees []*TxOutput
	for _, input := range fees {
		if err := atomicalsPlainInput(input); err != nil {
			return fmt.Errorf("invalid ARC20 splice fee input: %w", err)
		}
		if !bytes.Equal(input.OutValue.PkScript, owner) {
			return fmt.Errorf("ARC20 fee input does not belong to action initiator")
		}
		point := input.TxIn().PreviousOutPoint
		if seen[point] {
			return fmt.Errorf("duplicate ARC20 splice fee input")
		}
		seen[point] = true
		feeValue, err = atomicalsAdd(feeValue, input.Value())
		if err != nil {
			return err
		}
		selectedFees = append(selectedFees, input.Clone())
		weight.AddTaprootKeySpendInput(txscript.SigHashDefault)
	}
	chanpoint := channel.GetChanPoint()
	if err := atomicalsPlainInput(chanpoint); err != nil {
		return fmt.Errorf("ARC20 splice requires a plain channel point: %w", err)
	}
	if seen[chanpoint.TxIn().PreviousOutPoint] {
		return fmt.Errorf("ARC20 splice channel point duplicated in asset or fee inputs")
	}
	mainValue := amount
	if mainValue < 330 {
		mainValue = 330
	}
	var changeValue, bonus int64
	if inputAmount > amount {
		changeValue = inputValue - mainValue
		if changeValue < 330 {
			changeValue = 330
		}
	}
	if operation == STPOpSpliceIn {
		weight.AddP2WSHOutput()
		for range needStub {
			weight.AddP2WSHOutput()
		}
		feeValue += inputValue - mainValue - changeValue - int64(needStub)*stubValue
	} else {
		weight.AddP2TROutput()
		if inputAmount == amount && inputValue > mainValue {
			bonus = inputValue - mainValue
			if bonus+serviceFee < 330 {
				mainValue += bonus
				bonus = 0
			}
		}
		feeValue += inputValue - mainValue - changeValue - bonus
		if serviceFee+bonus >= 330 {
			weight.AddP2TROutput()
		}
	}
	if changeValue > 0 {
		if operation == STPOpSpliceOut {
			weight.AddP2WSHOutput()
		} else {
			weight.AddP2TROutput()
		}
	}
	requiredFee := weight.Fee(resv.FeeRate) + serviceFee
	if feeValue < requiredFee {
		return fmt.Errorf("insufficient plain ARC20 splice fee/padding budget: require %d, have %d", requiredFee, feeValue)
	}
	weight.AddP2TROutput()
	withChangeFee := weight.Fee(resv.FeeRate) + serviceFee
	var feeChange int64
	if feeValue-withChangeFee >= 330 {
		requiredFee = withChangeFee
		feeChange = feeValue - requiredFee
	}
	resv.SplicingAmt = resv.Amt.Clone()
	resv.SplicingInputs = selected
	resv.Fees = selectedFees
	resv.SplicingValue = mainValue
	resv.SplicingChange = changeValue
	resv.RequiredFee = requiredFee
	resv.ServiceFee = serviceFee
	resv.FeeChange = feeChange
	resv.Bonus = bonus
	return nil
}

func (protocol atomicalsSTPProtocol) BindSplice(p *Manager, req STPAssetSpliceRequest) error {
	resv := req.Reservation
	if resv == nil || req.Tx == nil || len(req.Tx.TxOut) == 0 {
		return fmt.Errorf("missing ARC20 splice transaction")
	}
	id, err := p.atomicalsIdentity(resv.AssetName)
	if err != nil {
		return err
	}
	evidence, err := p.newAtomicalsEvidence()
	if err != nil {
		return err
	}
	plan := &STPAssetPlan{Tx: req.Tx, ProtocolData: evidence, PrevFetcher: txscript.NewMultiPrevOutFetcher(nil)}
	var total int64
	for _, input := range resv.SplicingInputs {
		if err := protocol.VerifyInput(resv.AssetName, input); err != nil {
			return err
		}
		amount := input.GetAsset(&resv.AssetName.AssetName).Int64()
		total, err = atomicalsAdd(total, amount)
		if err != nil {
			return err
		}
		if err := evidence.addInput(input, id, amount); err != nil {
			return err
		}
		plan.PrevFetcher.AddPrevOut(*input.OutPoint(), &input.OutValue)
	}
	for _, input := range append(append([]*TxOutput{}, resv.Fees...), resv.Channel.GetChanPoint()) {
		if err := atomicalsPlainInput(input); err != nil {
			return err
		}
		if err := evidence.addInput(input, "", 0); err != nil {
			return err
		}
		plan.PrevFetcher.AddPrevOut(*input.OutPoint(), &input.OutValue)
	}
	amount, err := atomicalsAmount(resv.SplicingAmt)
	if err != nil || amount <= 0 || amount > total {
		return fmt.Errorf("invalid final ARC20 splice amount")
	}
	evidence.expected[req.Tx.TxOut[0]] = map[string]int64{id: amount}
	if total > amount {
		if len(req.Tx.TxOut) < 2 || resv.SplicingChangeOutput == nil || resv.SplicingChangeOutput.GetAsset(&resv.AssetName.AssetName).Int64() != total-amount {
			return fmt.Errorf("missing ARC20 splice change allocation")
		}
		evidence.expected[req.Tx.TxOut[1]] = map[string]int64{id: total - amount}
	}
	snapshotSTPAssetLayout(req.Tx, nil, nil, false, plan)
	return protocol.Verify(STPAssetVerification{Operation: req.Operation, Tx: req.Tx, Plan: plan})
}
