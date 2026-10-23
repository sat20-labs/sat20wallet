package wallet

import (
	"bytes"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/stretchr/testify/require"
)

// ACK fixes the unsigned body. Only the matching, fully authorized Bitcoin
// spend may become a contract input, even if an evidence provider claims that
// a malformed transaction is confirmed. Spent prevouts come from raw history.
func TestNativeContractDepositObservedAuthorization(t *testing.T) {
	key, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{7}, 32))
	script, err := txscript.PayToTaprootScript(txscript.ComputeTaprootKeyNoScript(key.PubKey()))
	require.NoError(t, err)
	parent := wire.NewMsgTx(2)
	parent.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: 0xffffffff}, []byte{1, 1}, nil))
	parent.AddTxOut(wire.NewTxOut(10000, script))
	point := wire.OutPoint{Hash: parent.TxHash(), Index: 0}
	fixed := wire.NewMsgTx(2)
	fixed.AddTxIn(wire.NewTxIn(&point, nil, nil))
	fixed.AddTxOut(wire.NewTxOut(9000, script))
	encode := func(tx *wire.MsgTx) []byte {
		var raw bytes.Buffer
		require.NoError(t, tx.Serialize(&raw))
		return raw.Bytes()
	}
	deposit := &NativeContractDeposit{SignedTx: encode(fixed)}
	sign := func(tx *wire.MsgTx, hashType txscript.SigHashType) {
		prev := txscript.NewMultiPrevOutFetcher(map[wire.OutPoint]*wire.TxOut{point: parent.TxOut[0]})
		sig, err := txscript.RawTxInTaprootSignature(tx, txscript.NewTxSigHashes(tx, prev), 0,
			10000, script, nil, hashType, key)
		require.NoError(t, err)
		tx.TxIn[0].Witness = wire.TxWitness{sig}
	}
	for _, tc := range []struct {
		name             string
		hashType         txscript.SigHashType
		mutate           func(*wire.MsgTx)
		mempool          bool
		requireConfirmed bool
		missingParent    bool
		wrongParent      bool
		accept           bool
		wantError        string
	}{
		{name: "confirmed-default", requireConfirmed: true, accept: true},
		{name: "confirmed-all", hashType: txscript.SigHashAll, requireConfirmed: true, accept: true},
		{name: "mempool-recovery", mempool: true, accept: true},
		{name: "mempool-cannot-mint", mempool: true, requireConfirmed: true, wantError: "required confirmation state"},
		{name: "unsigned-cannot-mint", requireConfirmed: true, mutate: func(tx *wire.MsgTx) { tx.TxIn[0].Witness = nil }, wantError: "does not commit all outputs"},
		{name: "forged-signature", requireConfirmed: true, mutate: func(tx *wire.MsgTx) { tx.TxIn[0].Witness[0][0] ^= 1 }},
		{name: "sighash-single", hashType: txscript.SigHashSingle, requireConfirmed: true, wantError: "does not commit all outputs"},
		{name: "sighash-anyonecanpay", hashType: txscript.SigHashAll | txscript.SigHashAnyOneCanPay, requireConfirmed: true, wantError: "does not commit all outputs"},
		{name: "changed-output", requireConfirmed: true, mutate: func(tx *wire.MsgTx) { tx.TxOut[0].Value-- }, wantError: "differs from the acknowledged transaction"},
		{name: "missing-parent", requireConfirmed: true, missingParent: true, wantError: "unknown test transaction"},
		{name: "wrong-parent", requireConfirmed: true, wrongParent: true, wantError: "input transaction evidence mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := fixed.Copy()
			sign(tx, tc.hashType)
			if tc.mutate != nil {
				tc.mutate(tx)
			}
			evidence := &rgb11FlowEvidence{
				rawTx: map[string][]byte{fixed.TxID(): encode(tx), parent.TxID(): encode(parent)},
				txStatuses: map[string]*rgb11wallet.BitcoinTxStatus{fixed.TxID(): {
					TxID: fixed.TxID(), Confirmed: !tc.mempool, InMempool: tc.mempool,
				}},
			}
			if tc.missingParent {
				delete(evidence.rawTx, parent.TxID())
			}
			if tc.wrongParent {
				changed := parent.Copy()
				changed.TxOut[0].Value++
				evidence.rawTx[parent.TxID()] = encode(changed)
			}
			err := validateObservedNativeContractDeposit(evidence, deposit, tc.requireConfirmed)
			if tc.accept {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				if tc.wantError != "" {
					require.ErrorContains(t, err, tc.wantError)
				}
			}
		})
	}
}
