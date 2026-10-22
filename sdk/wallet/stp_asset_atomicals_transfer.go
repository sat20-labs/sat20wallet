package wallet

import (
	"bytes"
	"fmt"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

func (p *Manager) authenticatedAtomicalsInput(name *AssetName, selected *TxOutput) (*TxOutput, error) {
	if selected == nil {
		return nil, fmt.Errorf("missing ARC20 selected input")
	}
	full, err := p.l1IndexerClient.GetTxOutput(selected.OutPointStr)
	if err != nil {
		return nil, fmt.Errorf("read complete ARC20 input facts: %w", err)
	}
	if err := (atomicalsSTPProtocol{}).VerifyInput(name, full); err != nil {
		return nil, err
	}
	if full.Value() != selected.Value() || !bytes.Equal(full.OutValue.PkScript, selected.OutValue.PkScript) || full.GetAsset(&name.AssetName).Cmp(selected.GetAsset(&name.AssetName)) != 0 {
		return nil, fmt.Errorf("ARC20 selected input facts changed")
	}
	return full.Clone(), nil
}

func (protocol atomicalsSTPProtocol) BuildTransfer(p *Manager, req STPAssetTransferRequest) (*STPAssetPlan, error) {
	if req.Asset == nil || req.Asset.N != 1 || len(req.Destinations) == 0 {
		return nil, fmt.Errorf("missing ARC20 transfer request")
	}
	if err := protocol.ValidateAsset(&req.Asset.AssetName, STPOpTransfer); err != nil {
		return nil, err
	}
	if !IsValidNullData(req.Memo) || req.FeeRate <= 0 {
		return nil, fmt.Errorf("invalid ARC20 memo or fee rate")
	}
	id, err := p.atomicalsIdentity(req.Asset)
	if err != nil {
		return nil, err
	}
	evidence, err := p.newAtomicalsEvidence()
	if err != nil {
		return nil, err
	}
	localWallet, excluded := p.batchSendSourceContext(req.Wallet, req.Address, true)
	address := req.Address
	if address == "" && localWallet != nil {
		address = localWallet.GetAddress()
	}
	sourceScript, err := GetPkScriptFromAddress(address)
	if err != nil {
		return nil, err
	}
	var required int64
	var payments []atomicalsPayment
	var extraOutputs []*wire.TxOut
	for _, destination := range req.Destinations {
		if destination == nil || (destination.AssetName != nil && *destination.AssetName != req.Asset.AssetName) {
			return nil, fmt.Errorf("ARC20 destination asset mismatch")
		}
		amount, err := atomicalsAmount(destination.AssetAmt)
		if err != nil || amount <= 0 || destination.Value < 0 || (destination.Value > 0 && destination.Value < 330) {
			return nil, fmt.Errorf("invalid ARC20 destination quantity or plain value")
		}
		required, err = atomicalsAdd(required, amount)
		if err != nil {
			return nil, err
		}
		script, err := GetPkScriptFromAddress(destination.Address)
		if err != nil {
			return nil, err
		}
		payments = append(payments, atomicalsPayment{script: script, amount: amount})
		if destination.Value > 0 {
			extraOutputs = append(extraOutputs, wire.NewTxOut(destination.Value, script))
		}
	}
	var weight utils.TxWeightEstimator
	selected, totalAsset, _, err := p.selectUtxosForAssetWithHeight(address, excluded, &req.Asset.AssetName,
		indexer.NewDefaultDecimal(required), &weight, req.ExcludeRecentBlock, true, req.MaxConfirmedInputHeight)
	if err != nil {
		return nil, err
	}
	totalAmount, err := atomicalsAmount(totalAsset)
	if err != nil || totalAmount < required {
		return nil, fmt.Errorf("insufficient ARC20 input assets")
	}
	if totalAmount > required {
		payments = append(payments, atomicalsPayment{script: sourceScript, amount: totalAmount - required})
	}
	payments, err = atomicalsPaymentOutputs(payments)
	if err != nil {
		return nil, err
	}
	tx := wire.NewMsgTx(2)
	plan := &STPAssetPlan{Tx: tx, PrevFetcher: txscript.NewMultiPrevOutFetcher(nil), ProtocolData: evidence}
	var inputValue, outputValue int64
	for _, selectedInput := range selected {
		input, err := p.authenticatedAtomicalsInput(req.Asset, selectedInput)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(input.OutValue.PkScript, sourceScript) {
			return nil, fmt.Errorf("ARC20 input does not belong to transfer source")
		}
		if err := evidence.addInput(input, id, input.GetAsset(&req.Asset.AssetName).Int64()); err != nil {
			return nil, err
		}
		inputValue, err = atomicalsAdd(inputValue, input.Value())
		if err != nil {
			return nil, err
		}
		tx.AddTxIn(input.TxIn())
		plan.PrevFetcher.AddPrevOut(*input.OutPoint(), &input.OutValue)
		excluded[input.OutPointStr] = true
	}
	for _, payment := range payments {
		output := wire.NewTxOut(payment.value, payment.script)
		tx.AddTxOut(output)
		weight.AddTxOutput(output)
		evidence.expected[output] = map[string]int64{id: payment.amount}
		outputValue, err = atomicalsAdd(outputValue, output.Value)
		if err != nil {
			return nil, err
		}
	}
	for _, output := range extraOutputs {
		tx.AddTxOut(output)
		weight.AddTxOutput(output)
		outputValue, err = atomicalsAdd(outputValue, output.Value)
		if err != nil {
			return nil, err
		}
	}
	if len(req.Memo) > 0 {
		weight.AddOutput(req.Memo)
	}
	feeAddress, feeScript := address, sourceScript
	feeInChannel := true
	if req.PayFeeByCurrentAddress {
		if localWallet == nil {
			return nil, fmt.Errorf("missing ARC20 fee wallet")
		}
		feeAddress = localWallet.GetAddress()
		feeScript, err = GetP2TRpkScript(localWallet.GetPaymentPubKey())
		if err != nil {
			return nil, err
		}
		feeInChannel = false
	}
	assetInputCount := len(tx.TxIn)
	fee, err := p.buildBatchSendTxV3FeeWithHeight(NewUtxoMgr(feeAddress, p.l1IndexerClient), excluded,
		req.ExcludeRecentBlock, feeInChannel, tx, &weight, plan.PrevFetcher,
		inputValue-outputValue, req.FeeRate, feeScript, req.MaxConfirmedInputHeight)
	if err != nil {
		return nil, err
	}
	// A plain-sats listing is not sufficient evidence: re-read every selected
	// fee UTXO before allowing it to enter the native transaction verifier.
	for _, input := range tx.TxIn[assetInputCount:] {
		fact, err := p.l1IndexerClient.GetTxOutput(input.PreviousOutPoint.String())
		if err != nil {
			return nil, fmt.Errorf("read complete ARC20 fee input facts: %w", err)
		}
		if err := atomicalsPlainInput(fact); err != nil {
			return nil, err
		}
		prev := plan.PrevFetcher.FetchPrevOutput(input.PreviousOutPoint)
		if prev == nil || prev.Value != fact.Value() || !bytes.Equal(prev.PkScript, fact.OutValue.PkScript) || !bytes.Equal(fact.OutValue.PkScript, feeScript) {
			return nil, fmt.Errorf("ARC20 fee input facts or owner mismatch")
		}
		if err := evidence.addInput(fact, "", 0); err != nil {
			return nil, err
		}
	}
	if len(req.Memo) > 0 {
		tx.AddTxOut(wire.NewTxOut(0, req.Memo))
	}
	plan.Fee = fee
	snapshotSTPAssetLayout(tx, nil, nil, false, plan)
	if err := protocol.Verify(STPAssetVerification{Operation: STPOpTransfer, Tx: tx, Plan: plan}); err != nil {
		return nil, err
	}
	return plan, nil
}
