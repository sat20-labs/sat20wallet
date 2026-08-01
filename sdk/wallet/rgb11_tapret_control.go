package wallet

import (
	"bytes"

	"github.com/btcsuite/btcd/txscript"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
)

// rgb11AllocationControlledByWallet verifies that the validated allocation's
// carrier output is controlled by the active wallet. OP_RETURN commitments use
// the ordinary BIP86 output key; Tapret commitments additionally tweak the
// internal key with the consensus-validated Tapret root.
func rgb11AllocationControlledByWallet(wallet common.Wallet,
	allocation *rgb11wallet.ValidatedAllocation, pkScript []byte) bool {

	if wallet == nil || allocation == nil {
		return false
	}
	pubKey := wallet.GetPubKey()
	if pubKey == nil {
		return false
	}
	outputKey := txscript.ComputeTaprootKeyNoScript(pubKey)
	switch allocation.CommitmentMethod {
	case "opret1st":
		// OP_RETURN carries the RGB commitment; the recipient output remains
		// the wallet's ordinary BIP86 P2TR output.
	case "tapret1st":
		if len(allocation.CarrierInternalKey) != schnorr.PubKeyBytesLen ||
			len(allocation.TapretRoot) != 32 ||
			!bytes.Equal(allocation.CarrierInternalKey, schnorr.SerializePubKey(pubKey)) {
			return false
		}
		outputKey = txscript.ComputeTaprootOutputKey(pubKey, allocation.TapretRoot)
	default:
		return false
	}
	expectedScript, err := txscript.PayToTaprootScript(outputKey)
	return err == nil && bytes.Equal(expectedScript, pkScript)
}
