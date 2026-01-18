package wallet

import (
	"bytes"
	"fmt"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func (p *Manager) SignTx(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher) (*wire.MsgTx, error) {
	return SignTxWithWallet(p.wallet, tx, prevFetcher)
}


func SignTxWithWallet(localWallet common.Wallet, tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher) (*wire.MsgTx, error) {
	packet, err := CreatePsbt(tx, prevFetcher, nil)
	if err != nil {
		Log.Errorf("wallet.CreatePsbt failed, %v", err)
		return nil, err
	}
	err = localWallet.SignPsbt(packet)
	if err != nil {
		Log.Errorf("SignPsbt failed, %v", err)
		return nil, err
	}

	err = psbt.MaybeFinalizeAll(packet)
	if err != nil {
		Log.Errorf("MaybeFinalizeAll failed, %v", err)
		return nil, err
	}

	finalTx, err := psbt.Extract(packet)
	if err != nil {
		Log.Errorf("Extract failed, %v", err)
		return nil, err
	}

	err = VerifySignedTx(finalTx, prevFetcher)
	if err != nil {
		return nil, err
	}

	return finalTx, nil
}

func (p *Manager) SignTxV2(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher) (error) {

	signedTx, err := p.SignTx(tx, prevFetcher)
	if err != nil {
		return err
	}
	tx.TxIn = signedTx.TxIn

	return nil
}

func (p *Manager) SignTx_SatsNet(tx *swire.MsgTx,
	prevFetcher stxscript.PrevOutputFetcher) (*swire.MsgTx, error) {
	return SignTxWithWallet_SatsNet(p.wallet, tx, prevFetcher)
}

func SignTxWithWallet_SatsNet(localWallet common.Wallet, tx *swire.MsgTx,
	prevFetcher stxscript.PrevOutputFetcher) (*swire.MsgTx, error) {

	packet, err := CreatePsbt_SatsNet(tx, prevFetcher, nil)
	if err != nil {
		Log.Errorf("wallet.CreatePsbt failed, %v", err)
		return nil, err
	}
	err = localWallet.SignPsbt_SatsNet(packet)
	if err != nil {
		Log.Errorf("SignPsbt failed, %v", err)
		return nil, err
	}

	err = spsbt.MaybeFinalizeAll(packet)
	if err != nil {
		Log.Errorf("MaybeFinalizeAll failed, %v", err)
		return nil, err
	}

	finalTx, err := spsbt.Extract(packet)
	if err != nil {
		Log.Errorf("Extract failed, %v", err)
		return nil, err
	}

	err = VerifySignedTx_SatsNet(finalTx, prevFetcher)
	if err != nil {
		return nil, err
	}

	return finalTx, nil
}

func (p *Manager) SignAndVerifyFundingTx(fundingTx *wire.MsgTx,
	prevFetcher txscript.PrevOutputFetcher) (*wire.MsgTx, error) {

	return p.SignTx(fundingTx, prevFetcher)
}

func (p *Manager) PartialSignTx(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
	witnessScript []byte, hasSelectingPath bool, peerPubKey []byte) ([][]byte, error) {
	return PartialSignTxWithWallet(p.wallet, tx, prevFetcher, witnessScript, hasSelectingPath, peerPubKey)
}

func PartialSignTxWithWallet(localWallet common.Wallet, tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
	witnessScript []byte, hasSelectingPath bool, peerPubKey []byte) ([][]byte, error) {
	packet, err := CreatePsbt(tx, prevFetcher, witnessScript)
	if err != nil {
		Log.Errorf("CreatePsbt failed, %v", err)
		return nil, err
	}
	err = localWallet.SignPsbt(packet)
	if err != nil {
		Log.Errorf("SignPsbt failed, %v", err)
		return nil, err
	}

	pubkey := localWallet.GetPaymentPubKey().SerializeCompressed()
	pos := GetCurrSignPosition2(pubkey, peerPubKey)
	result := make([][]byte, 0)
	for i, txIn := range tx.TxIn {
		input := &packet.Inputs[i]
		if input.TaprootKeySpendSig != nil {
			txIn.Witness = wire.TxWitness{input.TaprootKeySpendSig}
			result = append(result, input.TaprootKeySpendSig)
		} else if len(input.PartialSigs) > 0 {
			hasSig := false
			for _, sig := range input.PartialSigs {
				if txIn.Witness == nil {
					if hasSelectingPath {
						if peerPubKey == nil {
							// sweep tx for client
							txIn.Witness = wire.TxWitness{
								nil,
								[]byte{}, // empty to choose the delayed path
								witnessScript}
						} else {
							// sweep tx for server
							txIn.Witness = wire.TxWitness{nil, nil, nil,
								[]byte{}, // empty to choose the delayed path
								witnessScript}
						}
					} else {
						// channel redeem
						txIn.Witness = wire.TxWitness{nil, nil, nil, witnessScript}
					}
				}

				if bytes.Equal(sig.PubKey, pubkey) {
					result = append(result, sig.Signature)
					hasSig = true

					if peerPubKey == nil {
						// sweep tx for client
						txIn.Witness[0] = sig.Signature
					} else {
						txIn.Witness[pos+1] = sig.Signature
					}
					break
				}
			}

			if !hasSig {
				result = append(result, []byte{})
			}
		} else {
			result = append(result, []byte{})
		}
	}
	return result, nil
}

func (p *Manager) PartialSignTx_SatsNet(tx *swire.MsgTx, prevFetcher stxscript.PrevOutputFetcher,
	witnessScript []byte, peerPubKey []byte) ([][]byte, error) {
	return PartialSignTxWithWallet_SatsNet(p.wallet, tx, prevFetcher, witnessScript, peerPubKey)
}

func PartialSignTxWithWallet_SatsNet(localWallet common.Wallet, tx *swire.MsgTx, prevFetcher stxscript.PrevOutputFetcher,
	witnessScript []byte, peerPubKey []byte) ([][]byte, error) {
	packet, err := CreatePsbt_SatsNet(tx, prevFetcher, witnessScript)
	if err != nil {
		Log.Errorf("wallet.CreatePsbt failed, %v", err)
		return nil, err
	}
	err = localWallet.SignPsbt_SatsNet(packet)
	if err != nil {
		Log.Errorf("SignPsbt failed, %v", err)
		return nil, err
	}

	// 保存签名结果在TX中，并且返回签名结果
	pubkey := localWallet.GetPaymentPubKey().SerializeCompressed()
	result := make([][]byte, 0)
	for i, txIn := range tx.TxIn {
		input := &packet.Inputs[i]
		if input.TaprootKeySpendSig != nil {
			txIn.Witness = swire.TxWitness{input.TaprootKeySpendSig}
			result = append(result, input.TaprootKeySpendSig)
		} else if len(input.PartialSigs) > 0 {
			hasSig := false
			for _, sig := range input.PartialSigs {
				if bytes.Equal(sig.PubKey, pubkey) {
					result = append(result, sig.Signature)
					hasSig = true

					if peerPubKey == nil {
						// sweep tx for client
						txIn.Witness = swire.TxWitness{sig.Signature,
							[]byte{}, // empty to choose the delayed path
							witnessScript}
					} else {
						// channel redeem script
						pos := GetCurrSignPosition2(sig.PubKey, peerPubKey)
						if txIn.Witness == nil {
							txIn.Witness = swire.TxWitness{nil, nil, nil, witnessScript}
						}
						txIn.Witness[pos+1] = sig.Signature
						break
					}
				}
			}

			if !hasSig {
				result = append(result, []byte{})
			}
		} else {
			result = append(result, []byte{})
		}
	}
	return result, nil
}

func (p *Manager) FinalSignTx(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
	witnessScript []byte, hasSelectingPath bool,
	peerPubKey []byte, peerSigs [][]byte) ([][]byte, error) {
	return FinalSignTxWithWallet(p.wallet, tx, prevFetcher, witnessScript, hasSelectingPath, peerPubKey, peerSigs)
}

func FinalSignTxWithWallet(localWallet common.Wallet, tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
	witnessScript []byte, hasSelectingPath bool,
	peerPubKey []byte, peerSigs [][]byte) ([][]byte, error) {
	packet, err := CreatePsbtWithPeer(tx, prevFetcher, witnessScript, peerPubKey, peerSigs)
	if err != nil {
		Log.Errorf("CreatePsbt failed, %v", err)
		return nil, err
	}
	err = localWallet.SignPsbt(packet)
	if err != nil {
		Log.Errorf("SignPsbt failed, %v", err)
		return nil, err
	}

	pubkey := localWallet.GetPaymentPubKey()
	p2trPkScript, err := GetP2TRpkScript(pubkey)
	if err != nil {
		return nil, err
	}
	myPubKeyBytes := pubkey.SerializeCompressed()
	pos := GetCurrSignPosition2(myPubKeyBytes, peerPubKey)
	result := make([][]byte, 0)
	for i, txIn := range tx.TxIn {
		input := &packet.Inputs[i]
		if input.TaprootKeySpendSig != nil {
			txIn.Witness = wire.TxWitness{input.TaprootKeySpendSig}
			if bytes.Equal(input.WitnessUtxo.PkScript, p2trPkScript) {
				result = append(result, input.TaprootKeySpendSig)
			} else {
				result = append(result, []byte{})
			}
		} else if len(input.PartialSigs) > 0 {
			hasSig := false
			for _, sig := range input.PartialSigs {

				if txIn.Witness == nil {
					if hasSelectingPath {
						if peerPubKey == nil {
							// sweep tx for client
							txIn.Witness = wire.TxWitness{
								nil,
								[]byte{}, // empty to choose the delayed path
								witnessScript}
						} else {
							// sweep tx for server
							txIn.Witness = wire.TxWitness{nil, nil, nil,
								[]byte{}, // empty to choose the delayed path
								witnessScript}
						}
					} else {
						// channel redeem
						txIn.Witness = wire.TxWitness{nil, nil, nil, witnessScript}
					}
				}

				if bytes.Equal(sig.PubKey, myPubKeyBytes) {
					result = append(result, sig.Signature)
					hasSig = true

					if hasSelectingPath {
						if peerPubKey == nil {
							txIn.Witness[0] = sig.Signature
						} else {
							txIn.Witness[pos+1] = sig.Signature
						}
					} else {
						txIn.Witness[pos+1] = sig.Signature
					}

				} else if bytes.Equal(sig.PubKey, peerPubKey) {
					if pos == 0 {
						txIn.Witness[pos+2] = sig.Signature
					} else {
						txIn.Witness[pos] = sig.Signature
					}
				}
			}

			if !hasSig {
				result = append(result, []byte{})
			}
		} else {
			result = append(result, []byte{})
		}
	}
	return result, nil
}

func (p *Manager) FinalSignTx_SatsNet(tx *swire.MsgTx, prevFetcher stxscript.PrevOutputFetcher,
	witnessScript []byte, peerPubKey []byte, peerSigs [][]byte) ([][]byte, error) {
	return FinalSignTxWithWallet_SatsNet(p.wallet, tx, prevFetcher, witnessScript, peerPubKey, peerSigs)
}

func FinalSignTxWithWallet_SatsNet(localWallet common.Wallet, tx *swire.MsgTx, prevFetcher stxscript.PrevOutputFetcher,
	witnessScript []byte, peerPubKey []byte, peerSigs [][]byte) ([][]byte, error) {
	packet, err := CreatePsbtWithPeer_SatsNet(tx, prevFetcher, witnessScript, peerPubKey, peerSigs)
	if err != nil {
		Log.Errorf("CreatePsbt failed, %v", err)
		return nil, err
	}
	err = localWallet.SignPsbt_SatsNet(packet)
	if err != nil {
		Log.Errorf("SignPsbt failed, %v", err)
		return nil, err
	}

	pubkey := localWallet.GetPaymentPubKey()
	p2trPkScript, err := GetP2TRpkScript(pubkey)
	if err != nil {
		return nil, err
	}
	myPubKeyBytes := pubkey.SerializeCompressed()
	pos := GetCurrSignPosition2(myPubKeyBytes, peerPubKey)
	result := make([][]byte, 0)
	for i, txIn := range tx.TxIn {
		input := &packet.Inputs[i]
		if input.TaprootKeySpendSig != nil {
			txIn.Witness = swire.TxWitness{input.TaprootKeySpendSig}
			if bytes.Equal(input.WitnessUtxo.PkScript, p2trPkScript) {
				result = append(result, input.TaprootKeySpendSig)
			} else {
				result = append(result, []byte{})
			}
		} else if len(input.PartialSigs) > 0 {
			hasSig := false
			for _, sig := range input.PartialSigs {
				if bytes.Equal(sig.PubKey, myPubKeyBytes) {
					result = append(result, sig.Signature)
					hasSig = true

					if txIn.Witness == nil {
						txIn.Witness = swire.TxWitness{nil, nil, nil, witnessScript}
					}
					txIn.Witness[pos+1] = sig.Signature
				} else if bytes.Equal(sig.PubKey, peerPubKey) {
					if txIn.Witness == nil {
						txIn.Witness = swire.TxWitness{nil, nil, nil, witnessScript}
					}
					if pos == 0 {
						txIn.Witness[pos+2] = sig.Signature
					} else {
						txIn.Witness[pos] = sig.Signature
					}
				}
			}

			if !hasSig {
				result = append(result, []byte{})
			}
		} else {
			result = append(result, []byte{})
		}
	}
	return result, nil
}

func (p *Manager) FinalTxWithPeerSig(redeemScript, localPubkey, peerPubkey []byte, tx *wire.MsgTx,
	prevFetcher txscript.PrevOutputFetcher, theirSig [][]byte) error {

	multiSignScript, mulpkScript, err := GetP2WSHscript(localPubkey, peerPubkey)
	if err != nil {
		Log.Errorf("GetP2WSHScriptFromChannel failed. %v", err)
		return err
	}
	pos := GetCurrSignPosition2(localPubkey, peerPubkey)

	pubkey := p.wallet.GetPaymentPubKey()
	p2trPkScript, err := GetP2TRpkScript(pubkey)
	if err != nil {
		Log.Errorf("CreatePkScriptForP2TR failed. %v", err)
		return err
	}

	for i, in := range tx.TxIn {
		preOut := prevFetcher.FetchPrevOutput(in.PreviousOutPoint)
		if preOut == nil {
			Log.Errorf("can't find outpoint %s", in.PreviousOutPoint)
			return fmt.Errorf("can't find outpoint %s", in.PreviousOutPoint)
		}

		if !bytes.Equal(preOut.PkScript, p2trPkScript) &&
			!bytes.Equal(preOut.PkScript, mulpkScript) {
			tx.TxIn[i].Witness = wire.TxWitness{theirSig[i]}
			continue
		}

		scriptType := GetPkScriptType(preOut.PkScript)
		switch scriptType {
		case txscript.WitnessV1TaprootTy: // p2tr

		case txscript.WitnessV0ScriptHashTy: //"P2WSH":
			if tx.TxIn[i].Witness == nil {
				tx.TxIn[i].Witness = wire.TxWitness{nil, nil, nil, multiSignScript}
			}

			if pos == 0 {
				tx.TxIn[i].Witness[pos+2] = theirSig[i]
			} else {
				tx.TxIn[i].Witness[pos] = theirSig[i]
			}
		}
	}

	return nil
}

func (p *Manager) FinalTxWithPeerSig_SatsNet(redeemScript, localPubkey, peerPubkey []byte, tx *swire.MsgTx,
	prevFetcher stxscript.PrevOutputFetcher, theirSig [][]byte) error {

	multiSignScript, mulpkScript, err := GetP2WSHscript(localPubkey, peerPubkey)
	if err != nil {
		Log.Errorf("GetP2WSHScriptFromChannel failed. %v", err)
		return err
	}
	pos := GetCurrSignPosition2(localPubkey, peerPubkey)

	pubkey := p.wallet.GetPaymentPubKey()
	p2trPkScript, err := GetP2TRpkScript(pubkey)
	if err != nil {
		Log.Errorf("CreatePkScriptForP2TR failed. %v", err)
		return err
	}

	for i, in := range tx.TxIn {
		preOut := prevFetcher.FetchPrevOutput(in.PreviousOutPoint)
		if preOut == nil {
			Log.Errorf("can't find outpoint %s", in.PreviousOutPoint)
			return fmt.Errorf("can't find outpoint %s", in.PreviousOutPoint)
		}

		if !bytes.Equal(preOut.PkScript, p2trPkScript) &&
			!bytes.Equal(preOut.PkScript, mulpkScript) {
			tx.TxIn[i].Witness = swire.TxWitness{theirSig[i]}
			continue
		}

		scriptType := GetPkScriptType_SatsNet(preOut.PkScript)
		switch scriptType {
		case stxscript.WitnessV1TaprootTy: // p2tr

		case stxscript.WitnessV0ScriptHashTy: //"P2WSH":
			if tx.TxIn[i].Witness == nil {
				tx.TxIn[i].Witness = swire.TxWitness{nil, nil, nil, multiSignScript}
			}

			if pos == 0 {
				tx.TxIn[i].Witness[pos+2] = theirSig[i]
			} else {
				tx.TxIn[i].Witness[pos] = theirSig[i]
			}
		}
	}

	return nil
}

func (p *Manager) CleanPeerSig(redeemScript, localPubkey, peerPubkey []byte, tx *wire.MsgTx,
	prevFetcher txscript.PrevOutputFetcher) error {

	multiSignScript, mulpkScript, err := GetP2WSHscript(localPubkey, peerPubkey)
	if err != nil {
		Log.Errorf("GetP2WSHScriptFromChannel failed. %v", err)
		return err
	}
	pos := GetCurrSignPosition2(localPubkey, peerPubkey)

	pubkey := p.wallet.GetPaymentPubKey()
	p2trPkScript, err := GetP2TRpkScript(pubkey)
	if err != nil {
		Log.Errorf("CreatePkScriptForP2TR failed. %v", err)
		return err
	}

	for i, in := range tx.TxIn {
		preOut := prevFetcher.FetchPrevOutput(in.PreviousOutPoint)
		if preOut == nil {
			Log.Errorf("can't find outpoint %s", in.PreviousOutPoint)
			return fmt.Errorf("can't find outpoint %s", in.PreviousOutPoint)
		}

		if !bytes.Equal(preOut.PkScript, p2trPkScript) &&
			!bytes.Equal(preOut.PkScript, mulpkScript) {
			tx.TxIn[i].Witness = nil
			continue
		}

		scriptType := GetPkScriptType(preOut.PkScript)
		switch scriptType {
		case txscript.WitnessV1TaprootTy: // p2tr

		case txscript.WitnessV0ScriptHashTy: //"P2WSH":
			if tx.TxIn[i].Witness == nil {
				tx.TxIn[i].Witness = wire.TxWitness{nil, nil, nil, multiSignScript}
			}

			if pos == 0 {
				tx.TxIn[i].Witness[pos+2] = nil
			} else {
				tx.TxIn[i].Witness[pos] = nil
			}
		}
	}

	return nil
}

func (p *Manager) CleanPeerSig_SatsNet(redeemScript, localPubkey, peerPubkey []byte, tx *swire.MsgTx,
	prevFetcher stxscript.PrevOutputFetcher) error {

	multiSignScript, mulpkScript, err := GetP2WSHscript(localPubkey, peerPubkey)
	if err != nil {
		Log.Errorf("GetP2WSHScriptFromChannel failed. %v", err)
		return err
	}
	pos := GetCurrSignPosition2(localPubkey, peerPubkey)

	pubkey := p.wallet.GetPaymentPubKey()
	p2trPkScript, err := GetP2TRpkScript(pubkey)
	if err != nil {
		Log.Errorf("CreatePkScriptForP2TR failed. %v", err)
		return err
	}

	for i, in := range tx.TxIn {
		preOut := prevFetcher.FetchPrevOutput(in.PreviousOutPoint)
		if preOut == nil {
			Log.Errorf("can't find outpoint %s", in.PreviousOutPoint)
			return fmt.Errorf("can't find outpoint %s", in.PreviousOutPoint)
		}

		if !bytes.Equal(preOut.PkScript, p2trPkScript) &&
			!bytes.Equal(preOut.PkScript, mulpkScript) {
			tx.TxIn[i].Witness = nil
			continue
		}

		scriptType := GetPkScriptType_SatsNet(preOut.PkScript)
		switch scriptType {
		case stxscript.WitnessV1TaprootTy: // p2tr

		case stxscript.WitnessV0ScriptHashTy: //"P2WSH":
			if tx.TxIn[i].Witness == nil {
				tx.TxIn[i].Witness = swire.TxWitness{nil, nil, nil, multiSignScript}
			}

			if pos == 0 {
				tx.TxIn[i].Witness[pos+2] = nil
			} else {
				tx.TxIn[i].Witness[pos] = nil
			}
		}
	}

	return nil
}


func (p *Manager) SignAndVerifyTx(redeemScript, peerPubkey []byte, tx *wire.MsgTx,
	prevFetcher txscript.PrevOutputFetcher, theirSig [][]byte) ([][]byte, error) {

	sigs, err := p.FinalSignTx(tx, prevFetcher, redeemScript, false,
		peerPubkey, theirSig)
	if err != nil {
		return nil, err
	}

	err = VerifySignedTx(tx, prevFetcher)
	if err != nil {
		return nil, err
	}

	return sigs, nil
}

func (p *Manager) SignAndVerifyTx_SatsNet(redeemScript, peerPubkey []byte, tx *swire.MsgTx,
	prevFetcher stxscript.PrevOutputFetcher, theirSig [][]byte) ([][]byte, error) {

	sigs, err := p.FinalSignTx_SatsNet(tx, prevFetcher, redeemScript,
		peerPubkey, theirSig)
	if err != nil {
		return nil, err
	}

	err = VerifySignedTx_SatsNet(tx, prevFetcher)
	if err != nil {
		return nil, err
	}

	return sigs, nil
}

func (p *Manager) VerifyTx_SatsNet(redeemScript, localPubkey, peerPubkey []byte, tx *swire.MsgTx,
	prevFetcher stxscript.PrevOutputFetcher, theirSig [][]byte) error {

	err := p.FinalTxWithPeerSig_SatsNet(redeemScript, localPubkey, peerPubkey, tx, prevFetcher, theirSig)
	if err != nil {
		return err
	}

	err = VerifySignedTx_SatsNet(tx, prevFetcher)
	if err != nil {
		return err
	}

	return nil
}

func (p *Manager) VerifyTx(redeemScript, localPubkey, peerPubkey []byte, tx *wire.MsgTx,
	prevFetcher txscript.PrevOutputFetcher, theirSig [][]byte) error {

	err := p.FinalTxWithPeerSig(redeemScript, localPubkey, peerPubkey, tx, prevFetcher, theirSig)
	if err != nil {
		return err
	}

	err = VerifySignedTx(tx, prevFetcher)
	if err != nil {
		return err
	}

	return nil
}

func SignMessageWithWallet(wallet common.Wallet, msg []byte) ([]byte, error) {
	sig, err := wallet.SignMessage(msg)
	if err != nil {
		return nil, err
	}
	return sig, nil
}

func VerifySignOfMessage(msg, sig, pubkey []byte) error {
	key, err := utils.BytesToPublicKey(pubkey)
	if err != nil {
		return err
	}
	if !VerifyMessage(key, msg, sig) {
		return fmt.Errorf("VerifyMessage failed")
	}
	return nil
}

func (p *Manager) TestAcceptance(txs []*wire.MsgTx) error {
	if len(txs) == 0 {
		return nil
	}

	txsHex := make([]string, 0, len(txs))
	for _, t := range txs {
		if t == nil {
			continue
		}
		hexTx, err := EncodeMsgTx(t)
		if err != nil {
			Log.Warnf("EncodeMsgTx failed. %v", err)
			return err
		}
		txsHex = append(txsHex, hexTx)
	}
	
	// 承诺交易不会马上广播，所以提前检查非常重要
	// 所有前置TX都需要加入一起检查
	err := p.l1IndexerClient.TestRawTx(txsHex)
	if err != nil {
		Log.Errorf("TestRawTx failed, %v", err)
		return err
	}
	return nil
}

func (p *Manager) TestAcceptance_SatsNet(txs []*swire.MsgTx) error {
	if len(txs) == 0 {
		return nil
	}

	txsHex := make([]string, 0, len(txs))
	for _, t := range txs {
		if t == nil {
			continue
		}
		hexTx, err := EncodeMsgTx_SatsNet(t)
		if err != nil {
			Log.Warnf("EncodeMsgTx failed. %v", err)
			return err
		}
		txsHex = append(txsHex, hexTx)
		break // 聪网只支持检查一个
	}
	
	// 所有前置TX都需要加入一起检查
	err := p.l2IndexerClient.TestRawTx(txsHex)
	if err != nil {
		// parts := strings.Split(err.Error(), ":")
		// if len(parts) == 2 && parts[0] != "0" && parts[1] == "missing-inputs" {
		// 	// TODO 如果多个tx之间有相互依赖关系，聪网会返回"missing-inputs" 的错误，所以目前只能检查第一个
		// 	Log.Warningf("L2 TestRawTx %v", err)
		// 	return nil
		// }
		Log.Errorf("TestRawTx failed, %v", err)
		return err
	}
	return nil
}
