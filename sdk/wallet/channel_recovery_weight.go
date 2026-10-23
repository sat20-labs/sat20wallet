package wallet

import "github.com/btcsuite/btcd/wire"

const stpMaxECDSAWitnessSignatureSize = 73 // maximum DER signature plus sighash byte

// Serialize the exact native P2WSH spending branch. A bare script length is
// not a witness size, and these outputs have no nested P2SH scriptSig overhead.
func stpRecoveryWitnessSize(script []byte, operation STPAssetOperation, coSigned bool) int64 {
	signature := make([]byte, stpMaxECDSAWitnessSignatureSize)
	if operation == STPOpPunish {
		return int64(wire.TxWitness{signature, []byte{1}, script}.SerializeSize())
	}
	var peer []byte
	if coSigned {
		peer = []byte{1} // select the same dual-signature shape as the signer
	}
	witness := delayedPathWitness(script, peer)
	if coSigned {
		witness[1], witness[2] = signature, signature
	} else {
		witness[0] = signature
	}
	return int64(witness.SerializeSize())
}

func stpMultisigWitnessSize(script []byte) int64 {
	signature := make([]byte, stpMaxECDSAWitnessSignatureSize)
	return int64(wire.TxWitness{nil, signature, signature, script}.SerializeSize())
}
