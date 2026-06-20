package wallet

import (
	"github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

// AddCommitmentPlainAndStubInputs appends shared plain funding inputs plus any
// remaining stub inputs needed by channel commitment-like transactions.
// It returns the extra plain sats introduced by stub inputs.
func AddCommitmentPlainAndStubInputs(tx *wire.MsgTx, channel *Channel, extraStubs []*TxOutput, weightEstimate *utils.TxWeightEstimator) int64 {
	for _, utxo := range channel.GetFundingOutputs(&PLAIN_ASSET) {
		tx.AddTxIn(utxo.TxIn())
		weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
	}

	inputMap := make(map[string]bool)
	for _, txIn := range tx.TxIn {
		inputMap[txIn.PreviousOutPoint.String()] = true
	}

	var extraPlainSats int64
	for _, stub := range channel.GetStubUtxoList() {
		if inputMap[stub.OutPointStr] {
			continue
		}
		tx.AddTxIn(stub.TxIn())
		weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
		extraPlainSats += stub.Value()
	}

	for _, stub := range extraStubs {
		tx.AddTxIn(stub.TxIn())
		weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
		extraPlainSats += stub.Value()
	}

	return extraPlainSats
}
