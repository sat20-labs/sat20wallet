package wallet

import (
	"bytes"
	"testing"

	"github.com/btcsuite/btcd/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Match the producer's raw bytes, transaction identity, status and UTXO. A
// hand-written outpoint cannot stand in for producer evidence during recovery.
func seedRGB11FlowEvidenceUTXO(t *testing.T, evidence *rgb11FlowEvidence,
	script []byte, value int64, marker byte) string {
	t.Helper()
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: wire.MaxPrevOutIndex}, []byte{1, marker}, nil))
	tx.AddTxOut(wire.NewTxOut(value, append([]byte(nil), script...)))
	var raw bytes.Buffer
	if err := tx.Serialize(&raw); err != nil {
		t.Fatal(err)
	}
	txid := tx.TxHash().String()
	outpoint := txid + ":0"
	if evidence.txStatuses == nil {
		evidence.txStatuses = make(map[string]*rgb11wallet.BitcoinTxStatus)
	}
	evidence.rawTx[txid] = append([]byte(nil), raw.Bytes()...)
	evidence.txStatuses[txid] = &rgb11wallet.BitcoinTxStatus{
		TxID: txid, Confirmed: true, Confirmations: 6, BlockHeight: 1, BlockHash: "test-tip",
	}
	evidence.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
		OutPoint: outpoint, Value: value, PkScript: append([]byte(nil), script...), Confirmations: 6,
	}
	return outpoint
}
