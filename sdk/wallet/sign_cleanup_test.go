package wallet

import (
	"testing"

	"github.com/btcsuite/btcd/btcutil/psbt"
	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
)

func TestClearPsbtSigningData(t *testing.T) {
	partialSig := []byte{1, 2, 3}
	taprootKeySig := []byte{4, 5, 6}
	taprootScriptSig := []byte{7, 8, 9}
	finalScriptSig := []byte{10, 11, 12}
	finalWitness := []byte{13, 14, 15}

	packet := &psbt.Packet{
		Inputs: []psbt.PInput{
			{
				PartialSigs: []*psbt.PartialSig{
					{PubKey: []byte{2}, Signature: partialSig},
				},
				TaprootKeySpendSig: taprootKeySig,
				TaprootScriptSpendSig: []*psbt.TaprootScriptSpendSig{
					{Signature: taprootScriptSig},
				},
				FinalScriptSig:     finalScriptSig,
				FinalScriptWitness: finalWitness,
			},
		},
	}

	clearPsbtSigningData(packet)

	input := packet.Inputs[0]
	if input.PartialSigs != nil {
		t.Fatalf("partial signatures were not cleared")
	}
	if input.TaprootKeySpendSig != nil {
		t.Fatalf("taproot key spend signature was not cleared")
	}
	if input.TaprootScriptSpendSig != nil {
		t.Fatalf("taproot script spend signatures were not cleared")
	}
	if input.FinalScriptSig != nil {
		t.Fatalf("final script sig was not cleared")
	}
	if input.FinalScriptWitness != nil {
		t.Fatalf("final script witness was not cleared")
	}

	assertZeroed(t, partialSig)
	assertZeroed(t, taprootKeySig)
	assertZeroed(t, taprootScriptSig)
	assertZeroed(t, finalScriptSig)
	assertZeroed(t, finalWitness)
}

func TestClearPsbtSigningDataSatsNet(t *testing.T) {
	partialSig := []byte{1, 2, 3}
	taprootKeySig := []byte{4, 5, 6}
	taprootScriptSig := []byte{7, 8, 9}
	finalScriptSig := []byte{10, 11, 12}
	finalWitness := []byte{13, 14, 15}

	packet := &spsbt.Packet{
		Inputs: []spsbt.PInput{
			{
				PartialSigs: []*spsbt.PartialSig{
					{PubKey: []byte{2}, Signature: partialSig},
				},
				TaprootKeySpendSig: taprootKeySig,
				TaprootScriptSpendSig: []*spsbt.TaprootScriptSpendSig{
					{Signature: taprootScriptSig},
				},
				FinalScriptSig:     finalScriptSig,
				FinalScriptWitness: finalWitness,
			},
		},
	}

	clearPsbtSigningData_SatsNet(packet)

	input := packet.Inputs[0]
	if input.PartialSigs != nil {
		t.Fatalf("partial signatures were not cleared")
	}
	if input.TaprootKeySpendSig != nil {
		t.Fatalf("taproot key spend signature was not cleared")
	}
	if input.TaprootScriptSpendSig != nil {
		t.Fatalf("taproot script spend signatures were not cleared")
	}
	if input.FinalScriptSig != nil {
		t.Fatalf("final script sig was not cleared")
	}
	if input.FinalScriptWitness != nil {
		t.Fatalf("final script witness was not cleared")
	}

	assertZeroed(t, partialSig)
	assertZeroed(t, taprootKeySig)
	assertZeroed(t, taprootScriptSig)
	assertZeroed(t, finalScriptSig)
	assertZeroed(t, finalWitness)
}

func assertZeroed(t *testing.T, data []byte) {
	t.Helper()

	for i, b := range data {
		if b != 0 {
			t.Fatalf("byte %d was not zeroed", i)
		}
	}
}
