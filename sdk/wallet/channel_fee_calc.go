package wallet

import (
	"github.com/btcsuite/btcd/txscript"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

func CalcFee_SplicingOut(inputLen, feeLen int, assetName *AssetName,
	amt *Decimal, feeRate int64, fromInitiator bool, feeCfg *ChannelFeeConfig) int64 {
	var weightEstimate utils.TxWeightEstimator

	weightEstimate.AddP2TROutput()
	weightEstimate.AddP2TROutput()

	for i := 0; i < feeLen; i++ {
		weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
	}

	if NeedStubUtxoForInputAsset(assetName, amt) {
		weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
	}

	for i := 0; i < inputLen; i++ {
		weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
	}

	weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
	weightEstimate.AddP2WSHOutput()
	weightEstimate.AddP2WSHOutput()
	weightEstimate.AddP2TROutput()

	requiredFee := weightEstimate.Fee(feeRate)
	switch assetName.Protocol {
	case indexer.PROTOCOL_NAME_BRC20:
		requiredFee += STUB_VALUE_BRC20
	case indexer.PROTOCOL_NAME_RUNES:
		var payload [txscript.MaxDataCarrierSize]byte
		weightEstimate.AddOutput(payload[:])
		requiredFee = weightEstimate.Fee(feeRate)
		requiredFee += 330
	}

	requiredFee += CalcSplicingOutServiceFee(amt, fromInitiator, feeCfg)
	return requiredFee
}

func CalcSplicingOutServiceFee(amt *Decimal, fromInitiator bool, feeCfg *ChannelFeeConfig) int64 {
	if !fromInitiator {
		return 0
	}
	return feeCfg.SplicingOutFee
}
