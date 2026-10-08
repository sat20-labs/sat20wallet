package wallet

import (
	"bytes"
	"fmt"

	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Validate the whole packet before signing any input. Witness-only inputs are
// supported, but their claimed chain data cannot be authenticated here.
func checkedSatsNetPSBTPrevouts(packet *spsbt.Packet) (*stxscript.MultiPrevOutFetcher, error) {
	if packet == nil || packet.UnsignedTx == nil || len(packet.Inputs) != len(packet.UnsignedTx.TxIn) {
		return nil, fmt.Errorf("invalid SatoshiNet PSBT inputs")
	}
	fetcher := stxscript.NewMultiPrevOutFetcher(nil)
	for i, input := range packet.Inputs {
		txIn := packet.UnsignedTx.TxIn[i]
		if txIn == nil {
			return nil, fmt.Errorf("missing PSBT input %d", i)
		}
		prev := input.WitnessUtxo
		if input.NonWitnessUtxo != nil {
			nonWitness := input.NonWitnessUtxo
			if nonWitness.TxHash() != txIn.PreviousOutPoint.Hash {
				return nil, fmt.Errorf("PSBT input %d non-witness txid mismatch", i)
			}
			if uint64(txIn.PreviousOutPoint.Index) >= uint64(len(nonWitness.TxOut)) {
				return nil, fmt.Errorf("PSBT input %d prevout index out of range", i)
			}
			prev = nonWitness.TxOut[txIn.PreviousOutPoint.Index]
			if prev == nil {
				return nil, fmt.Errorf("missing PSBT prevout %d", i)
			}
			if input.WitnessUtxo != nil {
				var a, b bytes.Buffer
				if err := swire.WriteTxOut(&a, 0, 0, prev); err != nil {
					return nil, err
				}
				if err := swire.WriteTxOut(&b, 0, 0, input.WitnessUtxo); err != nil {
					return nil, err
				}
				if !bytes.Equal(a.Bytes(), b.Bytes()) {
					return nil, fmt.Errorf("PSBT input %d witness/non-witness prevout mismatch", i)
				}
			}
		}
		if prev == nil {
			return nil, fmt.Errorf("missing PSBT prevout %d", i)
		}
		copyOut := *prev
		copyOut.PkScript = bytes.Clone(prev.PkScript)
		copyOut.Assets = append(swire.TxAssets(nil), prev.Assets...)
		for j := range copyOut.Assets {
			if err := copyOut.Assets[j].Amount.Validate(); err != nil {
				return nil, err
			}
			copyOut.Assets[j].Amount = *copyOut.Assets[j].Amount.Clone()
		}
		fetcher.AddPrevOut(txIn.PreviousOutPoint, &copyOut)
	}
	return fetcher, nil
}
