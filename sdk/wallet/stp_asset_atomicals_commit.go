package wallet

import (
	"fmt"
	"sort"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/atom/coloring"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

func (protocol atomicalsSTPProtocol) BuildCommitment(p *Manager, req STPAssetCommitmentRequest) (*STPAssetPlan, error) {
	plan := &STPAssetPlan{Tx: req.Tx}
	groups := req.Channel.GetFundingOutputWithProtocol(indexer.PROTOCOL_NAME_ATOM)
	if len(groups) == 0 {
		return plan, nil
	}
	if len(req.Tx.TxIn) != 0 {
		return nil, fmt.Errorf("ARC20 must build the first commitment input/output segment")
	}
	for _, output := range req.Tx.TxOut {
		if output == nil || output.Value != 0 || !coloring.IsUnspendable(output) {
			return nil, fmt.Errorf("ARC20 must precede all spendable commitment outputs")
		}
	}
	evidence, err := p.newAtomicalsEvidence()
	if err != nil {
		return nil, err
	}
	ids := make(map[AssetName]string, len(groups))
	for _, group := range groups {
		id, err := p.atomicalsIdentity(group.AssetName)
		if err != nil {
			return nil, err
		}
		ids[*group.AssetName] = id
	}
	// Choose the same native order before and after FIFO activation. Each
	// asset's inputs are appended together, and no human ticker orders colors.
	sort.Slice(groups, func(i, j int) bool {
		return coloring.CompareAtomicalIDs(ids[*groups[i].AssetName], ids[*groups[j].AssetName]) < 0
	})
	plan.PrevFetcher = txscript.NewMultiPrevOutFetcher(nil)
	for _, group := range groups {
		name := group.AssetName
		if p.isControlByContract(req.Channel.ChannelId, name.String()) {
			continue
		}
		local, err := atomicalsAmount(req.LocalBalance[*name])
		if err != nil {
			return nil, err
		}
		remote, err := atomicalsAmount(req.RemoteBalance[*name])
		if err != nil {
			return nil, err
		}
		required, err := atomicalsAdd(local, remote)
		// A full splice-out keeps the asset key with an empty funding slice.
		// That retired allocation contributes no inputs to the next commitment.
		// Do not skip a live allocation merely because its balances disappeared.
		if err == nil && required == 0 && len(group.Outputs) == 0 {
			continue
		}
		if err != nil || required == 0 {
			return nil, fmt.Errorf("missing ARC20 commitment balances")
		}
		var inputValue, inputAmount int64
		addInput := func(input *TxOutput, amount int64) error {
			var err error
			inputValue, err = atomicalsAdd(inputValue, input.Value())
			if err != nil {
				return err
			}
			inputAmount, err = atomicalsAdd(inputAmount, amount)
			if err != nil {
				return err
			}
			id := ids[*name]
			if amount == 0 {
				id = ""
			}
			if err := evidence.addInput(input, id, amount); err != nil {
				return err
			}
			req.Tx.AddTxIn(input.TxIn())
			plan.PrevFetcher.AddPrevOut(*input.OutPoint(), &input.OutValue)
			if req.Weight != nil {
				req.Weight.AddWitnessInput(utils.MultiSigWitnessSize)
			}
			return nil
		}
		for _, input := range group.Outputs {
			if err := protocol.VerifyInput(name, input); err != nil {
				return nil, err
			}
			if err := addInput(input, input.GetAsset(&name.AssetName).Int64()); err != nil {
				return nil, err
			}
		}
		if inputAmount != required {
			return nil, fmt.Errorf("ARC20 commitment balances do not match funding allocations")
		}
		var payments []atomicalsPayment
		if local > 0 {
			payments = append(payments, atomicalsPayment{script: req.LocalScript, amount: local})
		}
		if remote > 0 {
			payments = append(payments, atomicalsPayment{script: req.RemoteScript, amount: remote})
		}
		payments, err = atomicalsPaymentOutputs(payments)
		if err != nil {
			return nil, err
		}
		var outputValue int64
		for _, payment := range payments {
			outputValue, err = atomicalsAdd(outputValue, payment.value)
			if err != nil {
				return nil, err
			}
		}
		for _, stub := range req.Channel.GetStubUtxoForAsset(name) {
			if inputValue >= outputValue {
				break
			}
			if err := atomicalsPlainInput(stub); err != nil {
				return nil, err
			}
			if err := addInput(stub, 0); err != nil {
				return nil, err
			}
		}
		if inputValue < outputValue {
			return nil, fmt.Errorf("ARC20 commitment lacks plain padding for a dust-safe final output")
		}
		// Keep the native input segment's complete Bitcoin value in its output
		// segment. Following ORDX inputs consequently retain ordinal alignment.
		payments[len(payments)-1].value += inputValue - outputValue
		if evidence.height < evidence.heights.CustomColoring && payments[len(payments)-1].value != payments[len(payments)-1].amount {
			return nil, fmt.Errorf("partial ARC20 output coloring is not active")
		}
		for _, payment := range payments {
			output := wire.NewTxOut(payment.value, payment.script)
			req.Tx.AddTxOut(output)
			evidence.expected[output] = map[string]int64{ids[*name]: payment.amount}
			if req.Weight != nil {
				req.Weight.AddTxOutput(output)
			}
		}
	}
	if len(evidence.inputs) > 0 {
		plan.ProtocolData = evidence
		plan.FixedPrefix = true
	}
	return plan, nil
}
