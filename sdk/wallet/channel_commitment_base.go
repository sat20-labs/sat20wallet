package wallet

import (
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

// WitnessScriptDesc holds the output script and the witness script for p2wsh outputs.
type WitnessScriptDesc struct {
	OutputScript  []byte
	WitnessScript []byte
}

func (w *WitnessScriptDesc) PkScript() []byte {
	return w.OutputScript
}

func (w *WitnessScriptDesc) WitnessScriptToSign() []byte {
	return w.WitnessScript
}

func (w *WitnessScriptDesc) WitnessScriptForPath(_ utils.ScriptPath) ([]byte, error) {
	return w.WitnessScript, nil
}

func DeriveCommitmentKeys(commitPoint *btcec.PublicKey, whoseCommit int, bootstrapKey *secp256k1.PublicKey, revealKey *secp256k1.PrivateKey, channel *Channel) *CommitmentKeyRing {
	// The key ring is derived from the per-commitment point plus both parties'
	// base points. whoseCommit selects the owner of this commitment tx, so the
	// meaning of to-local/to-remote flips between local and remote views.
	keyRing := &CommitmentKeyRing{
		CommitPoint:  commitPoint,
		BootstrapKey: bootstrapKey,
	}

	var (
		toLocalBasePoint    *btcec.PublicKey
		toRemoteBasePoint   *btcec.PublicKey
		revocationBasePoint *btcec.PublicKey
	)
	if whoseCommit == 0 {
		toLocalBasePoint = channel.LocalChanCfg.PaymentKey
		toRemoteBasePoint = channel.RemoteChanCfg.PaymentKey
		revocationBasePoint = channel.RemoteChanCfg.RevocationBasePoint
	} else {
		toLocalBasePoint = channel.RemoteChanCfg.PaymentKey
		toRemoteBasePoint = channel.LocalChanCfg.PaymentKey
		revocationBasePoint = channel.LocalChanCfg.RevocationBasePoint
	}

	keyRing.ToLocalKey = toLocalBasePoint
	keyRing.ToRemoteKey = toRemoteBasePoint
	// The revocation key uses the opposite party's revocation base point. If
	// this commitment is later revoked, the opposite party can combine the
	// revealed commitment secret with that base point to spend the delayed
	// output immediately.
	keyRing.RevocationKey = utils.DeriveRevocationPubkey(revocationBasePoint, commitPoint)
	keyRing.RevealPrivKey = revealKey

	return keyRing
}

func CommitDelayScriptForClient(selfKey, revokeKey *btcec.PublicKey, csvDelay uint32) (utils.ScriptDescriptor, error) {
	// The delayed to-local output has two paths: the owner can spend after CSV,
	// or the counterparty can spend immediately with the revocation key if this
	// state has been revoked.
	toLocalRedeemScript, err := utils.CommitScriptToSelf(csvDelay, selfKey, revokeKey)
	if err != nil {
		return nil, err
	}

	toLocalScriptHash, err := utils.WitnessScriptHash(toLocalRedeemScript)
	if err != nil {
		return nil, err
	}

	return &WitnessScriptDesc{
		OutputScript:  toLocalScriptHash,
		WitnessScript: toLocalRedeemScript,
	}, nil
}

func CommitDelayScriptForServer(selfKey, bootstrapKey, revokeKey *btcec.PublicKey, csvDelay uint32) (utils.ScriptDescriptor, error) {
	// Server delayed outputs additionally require the bootstrap key on the
	// normal delayed path, but keep the same revocation breach path.
	toLocalRedeemScript, err := utils.CommitScriptToSelf2(csvDelay, selfKey, bootstrapKey, revokeKey)
	if err != nil {
		return nil, err
	}

	toLocalScriptHash, err := utils.WitnessScriptHash(toLocalRedeemScript)
	if err != nil {
		return nil, err
	}

	return &WitnessScriptDesc{
		OutputScript:  toLocalScriptHash,
		WitnessScript: toLocalRedeemScript,
	}, nil
}

func CommitDirectScriptForClient(remoteKey *btcec.PublicKey) (utils.ScriptDescriptor, uint32, error) {
	// The to-remote output has no CSV delay. In the client case it is a direct
	// P2TR payment to the counterparty key.
	pkScript, err := GetP2TRpkScript(remoteKey)
	if err != nil {
		return nil, 0, err
	}

	return &WitnessScriptDesc{
		OutputScript:  pkScript,
		WitnessScript: pkScript,
	}, 0, nil
}

func CommitDirectScriptForServer(remoteKey, bootstrapKey *btcec.PublicKey) (utils.ScriptDescriptor, uint32, error) {
	// Server to-remote outputs go through a 2-of-2 script with bootstrap so the
	// service-side flow can coordinate settlement before funds leave the channel.
	witnessScript, err := utils.GenMultiSigScript(remoteKey.SerializeCompressed(), bootstrapKey.SerializeCompressed())
	if err != nil {
		return nil, 0, err
	}
	pkScript, err := utils.WitnessScriptHash(witnessScript)
	if err != nil {
		return nil, 0, err
	}

	return &WitnessScriptDesc{
		OutputScript:  pkScript,
		WitnessScript: witnessScript,
	}, 0, nil
}

func CreateCommitTx(whoseCommit int, isInitiator bool, fundingUtxos []*TxOutput, keyRing *CommitmentKeyRing, csvDelay uint16, amountToLocal, amountToRemote int64, feeRate int64) (*wire.MsgTx, error) {
	// Original plain-sats commitment constructor. Asset-aware commitments use
	// CreateCommitTx3, but this function documents the basic two-output shape:
	// delayed to-local, immediate to-remote, version 2 for CSV, and dust outputs
	// omitted.
	toLocalScript, err := CommitDelayScriptForClient(keyRing.ToLocalKey, keyRing.RevocationKey, uint32(csvDelay))
	if err != nil {
		return nil, err
	}

	toRemoteScript, _, err := CommitDirectScriptForClient(keyRing.ToRemoteKey)
	if err != nil {
		return nil, err
	}

	var weightEstimate utils.TxWeightEstimator

	commitTx := wire.NewMsgTx(2)
	for _, utxo := range fundingUtxos {
		commitTx.AddTxIn(utxo.TxIn())
		weightEstimate.AddWitnessInput(utils.MultiSigWitnessSize)
	}

	var txOut1, txOut2 *wire.TxOut
	localOutput := amountToLocal >= 330
	if localOutput {
		txOut1 = &wire.TxOut{
			PkScript: toLocalScript.PkScript(),
			Value:    amountToLocal,
		}
		weightEstimate.AddTxOutput(txOut1)
	}

	remoteOutput := amountToRemote >= 330
	if remoteOutput {
		txOut2 = &wire.TxOut{
			PkScript: toRemoteScript.PkScript(),
			Value:    amountToRemote,
		}
		weightEstimate.AddTxOutput(txOut2)
	}

	vSize := weightEstimate.VSize()
	requiredFee := weightEstimate.Fee(feeRate)

	if whoseCommit == 0 {
		if isInitiator {
			if localOutput {
				commitTx.AddTxOut(txOut1)
			}
			amountToRemote -= requiredFee
			remoteOutput = amountToRemote >= 330
			if remoteOutput {
				txOut2.Value = amountToRemote
				commitTx.AddTxOut(txOut2)
			}
		} else {
			if remoteOutput {
				commitTx.AddTxOut(txOut2)
			}
			amountToLocal -= requiredFee
			localOutput = amountToLocal >= 330
			if localOutput {
				txOut1.Value = amountToLocal
				commitTx.AddTxOut(txOut1)
			}
		}
	} else {
		if isInitiator {
			if remoteOutput {
				commitTx.AddTxOut(txOut2)
			}
			amountToLocal -= requiredFee
			localOutput = amountToLocal >= 330
			if localOutput {
				txOut1.Value = amountToLocal
				commitTx.AddTxOut(txOut1)
			}
		} else {
			if localOutput {
				commitTx.AddTxOut(txOut1)
			}
			amountToRemote -= requiredFee
			remoteOutput = amountToRemote >= 330
			if remoteOutput {
				txOut2.Value = amountToRemote
				commitTx.AddTxOut(txOut2)
			}
		}
	}

	Log.Infof("commitTx(%d->%d): vsize=%d feeRate=%d, requiredFee=%d", len(commitTx.TxIn), len(commitTx.TxOut), vSize, feeRate, requiredFee)
	return commitTx, nil
}
