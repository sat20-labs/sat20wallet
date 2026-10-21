package wallet

import (
	"errors"
	"math"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/rgb11/strict_types"
)

// rgb11DiscardStrictTx reconstructs the Bitcoin transaction fields represented
// by a strictly decoded RGB witness. Witness data is omitted because Bitcoin
// txids commit to the stripped transaction serialization.
func rgb11DiscardStrictTx(value strict_types.Value) (*wire.MsgTx, error) {
	versionValue, versionOK := value.Field("version")
	inputsValue, inputsOK := value.Field("inputs")
	outputsValue, outputsOK := value.Field("outputs")
	lockTimeValue, lockTimeOK := value.Field("lockTime")
	version, versionNumberOK := discardSigned(versionValue)
	lockTime, lockTimeNumberOK := discardUnsigned(lockTimeValue)
	inputs, outputs := inputsValue.Unwrap(), outputsValue.Unwrap()
	if !versionOK || !inputsOK || !outputsOK || !lockTimeOK || !versionNumberOK || !lockTimeNumberOK ||
		version < math.MinInt32 || version > math.MaxInt32 || lockTime > math.MaxUint32 ||
		inputs.Kind != strict_types.ValueList || outputs.Kind != strict_types.ValueList {
		return nil, errors.New("invalid embedded witness transaction")
	}
	tx := wire.NewMsgTx(int32(version))
	tx.LockTime = uint32(lockTime)
	for _, item := range inputs.Items {
		item = item.Unwrap()
		previous, previousOK := item.Field("prevOutput")
		scriptValue, scriptOK := item.Field("sigScript")
		sequenceValue, sequenceOK := item.Field("sequence")
		txidValue, txidOK := previous.Unwrap().Field("txid")
		voutValue, voutOK := previous.Unwrap().Field("vout")
		txid, bytesOK := txidValue.Bytes()
		vout, voutNumberOK := discardUnsigned(voutValue)
		sequence, sequenceNumberOK := discardUnsigned(sequenceValue)
		script, scriptBytesOK := scriptValue.Bytes()
		if !previousOK || !scriptOK || !sequenceOK || !txidOK || !voutOK || !bytesOK || len(txid) != 32 ||
			!voutNumberOK || vout > math.MaxUint32 || !sequenceNumberOK || sequence > math.MaxUint32 || !scriptBytesOK {
			return nil, errors.New("invalid embedded witness input")
		}
		var hash chainhash.Hash
		copy(hash[:], txid)
		input := wire.NewTxIn(&wire.OutPoint{Hash: hash, Index: uint32(vout)}, script, nil)
		input.Sequence = uint32(sequence)
		tx.AddTxIn(input)
	}
	for _, item := range outputs.Items {
		item = item.Unwrap()
		amountValue, amountOK := item.Field("value")
		scriptValue, scriptOK := item.Field("scriptPubkey")
		amount, amountNumberOK := discardUnsigned(amountValue)
		script, scriptBytesOK := scriptValue.Bytes()
		if !amountOK || !scriptOK || !amountNumberOK || amount > math.MaxInt64 || !scriptBytesOK {
			return nil, errors.New("invalid embedded witness output")
		}
		tx.AddTxOut(wire.NewTxOut(int64(amount), script))
	}
	return tx, nil
}

func discardUnsigned(value strict_types.Value) (uint64, bool) {
	value = value.Unwrap()
	if value.Kind == strict_types.ValueUnion && value.Inner != nil {
		return discardUnsigned(*value.Inner)
	}
	return value.Uint64()
}

func discardSigned(value strict_types.Value) (int64, bool) {
	value = value.Unwrap()
	if value.Signed != nil {
		return *value.Signed, true
	}
	if value.Unsigned != nil && *value.Unsigned <= math.MaxInt64 {
		return int64(*value.Unsigned), true
	}
	return 0, false
}
