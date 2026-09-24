package wallet

import (
	"bytes"

	"github.com/btcsuite/btcd/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func verifyRGB11ExpectedSpend(evidence rgb11wallet.BitcoinEvidenceProvider,
	outpoint, expectedTxID string) (*rgb11wallet.BitcoinTxStatus, bool) {
	if evidence == nil || outpoint == "" || expectedTxID == "" {
		return nil, false
	}
	status, err := evidence.GetTxStatus(expectedTxID)
	if err != nil || status == nil || status.TxID != expectedTxID || !status.Confirmed || status.Confirmations < 1 {
		return nil, false
	}
	raw, err := evidence.GetRawTx(expectedTxID)
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil || tx.TxHash().String() != expectedTxID {
		return nil, false
	}
	matches := 0
	for _, input := range tx.TxIn {
		if input.PreviousOutPoint.String() == outpoint {
			matches++
		}
	}
	if matches != 1 {
		return nil, false
	}
	copyStatus := *status
	return &copyStatus, true
}
