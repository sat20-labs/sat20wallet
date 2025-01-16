package wallet

import (
	
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	stxscript "github.com/sat20-labs/satsnet_btcd/txscript"
	swire "github.com/sat20-labs/satsnet_btcd/wire"

)

func (p *Manager) SignTx(tx *wire.MsgTx,
	prevFetcher txscript.PrevOutputFetcher) error {

	// privKey := p.wallet.GetPaymentPrivKey()
	// sigHashes := txscript.NewTxSigHashes(tx, prevFetcher)
	// for i, in := range tx.TxIn {
	// 	preOut := prevFetcher.FetchPrevOutput(in.PreviousOutPoint)
	// 	if preOut == nil {
	// 		Log.Errorf("can't find outpoint %s", in.PreviousOutPoint)
	// 		return fmt.Errorf("can't find outpoint %s", in.PreviousOutPoint)
	// 	}

	// 	scriptType := GetPkScriptType(preOut.PkScript)
	// 	switch scriptType {
	// 	case txscript.WitnessV1TaprootTy: // p2tr
	// 		witness, err := txscript.TaprootWitnessSignature(tx, sigHashes, i,
	// 			preOut.Value, preOut.PkScript,
	// 			txscript.SigHashDefault, privKey)
	// 		if err != nil {
	// 			Log.Errorf("TaprootWitnessSignature failed. %v", err)
	// 			return err
	// 		}
	// 		tx.TxIn[i].Witness = witness

	// 	default:
	// 		Log.Errorf("not support type %d", scriptType)
	// 		return fmt.Errorf("not support type %d", scriptType)
	// 	}
	// }

	// return nil
	return p.wallet.SignTx(tx, prevFetcher)
}

func (p *Manager) SignTx_SatsNet(tx *swire.MsgTx,
	prevFetcher stxscript.PrevOutputFetcher) error {

	// privKey := p.wallet.GetPaymentPrivKey()
	// sigHashes := stxscript.NewTxSigHashes(tx, prevFetcher)
	// for i, in := range tx.TxIn {
	// 	preOut := prevFetcher.FetchPrevOutput(in.PreviousOutPoint)
	// 	if preOut == nil {
	// 		Log.Errorf("can't find outpoint %s", in.PreviousOutPoint)
	// 		return fmt.Errorf("can't find outpoint %s", in.PreviousOutPoint)
	// 	}

	// 	scriptType := GetPkScriptType_SatsNet(preOut.PkScript)
	// 	switch scriptType {
	// 	case stxscript.WitnessV1TaprootTy: // p2tr
	// 		witness, err := stxscript.TaprootWitnessSignature(tx, sigHashes, i,
	// 			preOut.Value, preOut.Assets, preOut.PkScript,
	// 			stxscript.SigHashDefault, privKey)
	// 		if err != nil {
	// 			Log.Errorf("TaprootWitnessSignature failed. %v", err)
	// 			return err
	// 		}
	// 		tx.TxIn[i].Witness = witness

	// 	default:
	// 		Log.Errorf("not support type %d", scriptType)
	// 		return fmt.Errorf("not support type %d", scriptType)
	// 	}
	// }

	// return nil
	return p.wallet.SignTx_SatsNet(tx, prevFetcher)
}
