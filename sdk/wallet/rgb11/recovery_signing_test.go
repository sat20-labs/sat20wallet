package rgb11wallet

import (
	"bytes"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

func TestRecoveryOrdinarySigningRequiresExactBodyAndValidWitness(t *testing.T) {
	key, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{7}, 32))
	script, err := txscript.PayToTaprootScript(txscript.ComputeTaprootKeyNoScript(key.PubKey()))
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(2)
	point := wire.OutPoint{Index: 1}
	point.Hash[0] = 99
	tx.AddTxIn(wire.NewTxIn(&point, nil, nil))
	tx.AddTxOut(wire.NewTxOut(9000, []byte{txscript.OP_TRUE}))
	packet, err := psbt.NewFromUnsignedTx(tx)
	if err != nil {
		t.Fatal(err)
	}
	packet.Inputs[0].WitnessUtxo = wire.NewTxOut(10000, script)
	var psbtRaw, unsignedRaw bytes.Buffer
	if err := packet.Serialize(&psbtRaw); err != nil {
		t.Fatal(err)
	}
	if err := tx.Serialize(&unsignedRaw); err != nil {
		t.Fatal(err)
	}
	prev := txscript.NewMultiPrevOutFetcher(nil)
	prev.AddPrevOut(point, packet.Inputs[0].WitnessUtxo)
	sig, err := txscript.RawTxInTaprootSignature(tx, txscript.NewTxSigHashes(tx, prev), 0, 10000, script, nil, txscript.SigHashDefault, key)
	if err != nil {
		t.Fatal(err)
	}
	tx.TxIn[0].Witness = wire.TxWitness{sig}
	var signedRaw bytes.Buffer
	if err := tx.Serialize(&signedRaw); err != nil {
		t.Fatal(err)
	}
	base := PendingTransfer{State: TransferState{TransferID: "ordinary-signing", Direction: "send", Status: "prepared", WitnessTxID: tx.TxHash().String()}, SignedTx: unsignedRaw.Bytes(), SignedPSBT: psbtRaw.Bytes()}
	signed := base
	signed.State.Status = "broadcast-attempted"
	signed.SignedTx = signedRaw.Bytes()
	if !samePendingIntent(base, signed) {
		t.Fatal("valid delayed signature was rejected by strict recovery merge")
	}
	for _, name := range []string{"invalid-signature", "changed-body", "changed-psbt", "wrong-phase", "signature-replacement", "removed-psbt"} {
		t.Run(name, func(t *testing.T) {
			left, right := base, signed
			switch name {
			case "invalid-signature":
				bad := tx.Copy()
				bad.TxIn[0].Witness[0] = append([]byte(nil), sig...)
				bad.TxIn[0].Witness[0][0] ^= 1
				var raw bytes.Buffer
				if err := bad.Serialize(&raw); err != nil {
					t.Fatal(err)
				}
				right.SignedTx = raw.Bytes()
			case "changed-body":
				bad := tx.Copy()
				bad.TxOut[0].Value--
				var raw bytes.Buffer
				if err := bad.Serialize(&raw); err != nil {
					t.Fatal(err)
				}
				right.SignedTx = raw.Bytes()
			case "changed-psbt":
				right.SignedPSBT = append(append([]byte(nil), base.SignedPSBT...), 0)
			case "wrong-phase":
				right.State.Status = "prepared"
			case "signature-replacement":
				left = signed
				right.SignedTx = unsignedRaw.Bytes()
			case "removed-psbt":
				right.SignedPSBT = nil
			}
			if samePendingIntent(left, right) {
				t.Fatal("unsafe witness transition was accepted")
			}
		})
	}
}
