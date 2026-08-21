package wallet

import (
	"testing"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

func sweepFeeTestScript() []byte {
	return append([]byte{txscript.OP_1, 0x20}, make([]byte, 32)...)
}

func sweepFeeTestTx(outputValue int64) (*wire.MsgTx, *utils.TxWeightEstimator) {
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{1}}, nil, nil))
	txOut := wire.NewTxOut(outputValue, sweepFeeTestScript())
	tx.AddTxOut(txOut)
	var estimate utils.TxWeightEstimator
	estimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
	estimate.AddTxOutput(txOut)
	return tx, &estimate
}

func transactionOutputValue(tx *wire.MsgTx) int64 {
	var value int64
	for _, output := range tx.TxOut {
		value += output.Value
	}
	return value
}

func TestAddSweepFeeChangeReportsActualFee(t *testing.T) {
	const (
		assetOutputValue = int64(1_000)
		feeRate         = int64(5)
	)
	tx, estimate := sweepFeeTestTx(assetOutputValue)
	withChange := *estimate
	withChange.AddTxOutput(wire.NewTxOut(0, sweepFeeTestScript()))
	feeValue := withChange.Fee(feeRate) + 500

	fee, err := addSweepFeeChange(tx, estimate, feeValue, feeRate, sweepFeeTestScript())
	if err != nil {
		t.Fatalf("addSweepFeeChange: %v", err)
	}
	if len(tx.TxOut) != 2 || tx.TxOut[1].Value != 500 {
		t.Fatalf("unexpected sweep change outputs: %+v", tx.TxOut)
	}
	actualFee := assetOutputValue + feeValue - transactionOutputValue(tx)
	if fee != actualFee || fee != withChange.Fee(feeRate) {
		t.Fatalf("reported fee=%d actual=%d expected=%d", fee, actualFee, withChange.Fee(feeRate))
	}
}

func TestAddSweepFeeChangeUsesDustAsActualFee(t *testing.T) {
	const (
		assetOutputValue = int64(1_000)
		feeRate         = int64(5)
	)
	tx, estimate := sweepFeeTestTx(assetOutputValue)
	withChange := *estimate
	withChange.AddTxOutput(wire.NewTxOut(0, sweepFeeTestScript()))
	feeValue := withChange.Fee(feeRate) + 329
	if feeValue < estimate.Fee(feeRate) {
		t.Fatal("test fixture does not cover a valid no-change transaction")
	}

	fee, err := addSweepFeeChange(tx, estimate, feeValue, feeRate, sweepFeeTestScript())
	if err != nil {
		t.Fatalf("addSweepFeeChange: %v", err)
	}
	if len(tx.TxOut) != 1 {
		t.Fatalf("dust change was emitted: %+v", tx.TxOut)
	}
	actualFee := assetOutputValue + feeValue - transactionOutputValue(tx)
	if fee != feeValue || fee != actualFee {
		t.Fatalf("reported no-change fee=%d actual=%d feeValue=%d", fee, actualFee, feeValue)
	}
}

func TestAddSweepFeeChangeRejectsInsufficientFee(t *testing.T) {
	tx, estimate := sweepFeeTestTx(1_000)
	feeValue := estimate.Fee(5) - 1
	if _, err := addSweepFeeChange(tx, estimate, feeValue, 5, sweepFeeTestScript()); err == nil {
		t.Fatal("insufficient sweep fee was accepted")
	}
}
