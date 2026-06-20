package wallet

import (
	"bytes"
	"fmt"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func SignAndVerifyFundingTx(localWallet common.Wallet, fundingTx *wire.MsgTx,
	prevFetcher txscript.PrevOutputFetcher) (*wire.MsgTx, error) {
	return SignTxWithWallet(localWallet, fundingTx, prevFetcher)
}

func PartialSignCommitTx(channel *Channel, tx *wire.MsgTx,
	inscribes, next []*InscribeResv) ([][]byte, [][][]byte, [][][]byte, error) {

	prevFetcher := channel.GetCommitmentPrefetchor()

	var preTxSigs, nextTxSigs [][][]byte
	if len(inscribes) != 0 {
		preTxSigs = make([][][]byte, 0)
		for _, insc := range inscribes {
			sig, err := PartialSignTxWithWallet(channel.LocalWallet(), insc.CommitTx, insc.GetCommitPrevOutputFetcher(),
				channel.RedeemScript, false, channel.RemoteChanCfg.PaymentKey.SerializeCompressed())
			if err != nil {
				return nil, nil, nil, err
			}
			preTxSigs = append(preTxSigs, sig)
			preTxSigs = append(preTxSigs, nil)

			output := indexer.GenerateTxOutput(insc.RevealTx, 0)
			prevFetcher.AddPrevOut(*output.OutPoint(), output.TxOut())
			change := insc.GetChangeOutput()
			if change != nil {
				prevFetcher.AddPrevOut(*change.OutPoint(), change.TxOut())
			}
		}
	}
	if len(next) != 0 {
		nextTxSigs = make([][][]byte, 0)
		for _, insc := range next {
			nextTxSigs = append(nextTxSigs, nil)
			nextTxSigs = append(nextTxSigs, nil)

			output := indexer.GenerateTxOutput(insc.RevealTx, 0)
			prevFetcher.AddPrevOut(*output.OutPoint(), output.TxOut())
			change := insc.GetChangeOutput()
			if change != nil {
				prevFetcher.AddPrevOut(*change.OutPoint(), change.TxOut())
			}
		}
	}

	commitSig, err := PartialSignTxWithWallet(channel.LocalWallet(), tx, prevFetcher, channel.RedeemScript, false,
		channel.RemoteChanCfg.PaymentKey.SerializeCompressed())
	if err != nil {
		return nil, nil, nil, err
	}
	return commitSig, preTxSigs, nextTxSigs, nil
}

func PartialSignCommitTxWithOthers(channel *Channel, tx *wire.MsgTx,
	inscribes, next []*InscribeResv, others []*CommitmentTx) (
	[][]byte, [][][]byte, [][][]byte, [][][][]byte, [][][]byte, error) {

	commitSig, preTxSigs, nextTxSigs, err := PartialSignCommitTx(channel, tx, inscribes, next)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	otherPrevTxSigs, otherTxSigs, err := PartialSignCommitmentOthers(channel, tx, others)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	return commitSig, preTxSigs, nextTxSigs, otherPrevTxSigs, otherTxSigs, nil
}

func PartialSignCommitmentOthers(channel *Channel, parentTx *wire.MsgTx,
	others []*CommitmentTx) ([][][][]byte, [][][]byte, error) {

	if len(others) == 0 {
		return nil, nil, nil
	}

	otherPrevTxSigs := make([][][][]byte, 0, len(others))
	otherTxSigs := make([][][]byte, 0, len(others))
	for _, other := range others {
		prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
		addTxOutputsToPrevFetcher(prevFetcher, parentTx)

		prevSigs := make([][][]byte, 0, len(other.PrevTxs))
		for _, prevTx := range other.PrevTxs {
			if hasChannelInput(prevTx, prevFetcher, channel.GetChannelPkScript()) {
				sig, err := PartialSignTxWithWallet(channel.LocalWallet(), prevTx, prevFetcher, channel.RedeemScript, false,
					channel.RemoteChanCfg.PaymentKey.SerializeCompressed())
				if err != nil {
					return nil, nil, err
				}
				prevSigs = append(prevSigs, sig)
			} else {
				prevSigs = append(prevSigs, nil)
			}
			addTxOutputsToPrevFetcher(prevFetcher, prevTx)
		}

		sig, err := PartialSignTxWithWallet(channel.LocalWallet(), other.CommitTx, prevFetcher, channel.RedeemScript, false,
			channel.RemoteChanCfg.PaymentKey.SerializeCompressed())
		if err != nil {
			return nil, nil, err
		}
		otherPrevTxSigs = append(otherPrevTxSigs, prevSigs)
		otherTxSigs = append(otherTxSigs, sig)
	}
	return otherPrevTxSigs, otherTxSigs, nil
}

func GetSignFromSignedTx(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
	localPubKey []byte) ([][]byte, error) {

	p2trPkScript, err := HexPubKeyToP2TRPkScript(localPubKey)
	if err != nil {
		return nil, err
	}

	result := make([][]byte, 0)
	for _, in := range tx.TxIn {
		preOut := prevFetcher.FetchPrevOutput(in.PreviousOutPoint)
		if preOut == nil {
			Log.Errorf("can't find outpoint %s", in.PreviousOutPoint)
			return nil, fmt.Errorf("can't find outpoint %s", in.PreviousOutPoint)
		}

		if !bytes.Equal(preOut.PkScript, p2trPkScript) {
			result = append(result, nil)
			continue
		}

		switch GetPkScriptType(preOut.PkScript) {
		case txscript.WitnessV1TaprootTy:
			result = append(result, in.Witness[0])
		}
	}
	return result, nil
}

func GetSignFromChannelSignedTx(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
	localPubKey, peerPubKey []byte) ([][]byte, error) {

	p2trPkScript, err := HexPubKeyToP2TRPkScript(localPubKey)
	if err != nil {
		return nil, err
	}

	_, mulpkScript, err := GetP2WSHscript(localPubKey, peerPubKey)
	if err != nil {
		return nil, err
	}
	pos := GetCurrSignPosition2(localPubKey, peerPubKey)

	result := make([][]byte, 0)
	for _, in := range tx.TxIn {
		preOut := prevFetcher.FetchPrevOutput(in.PreviousOutPoint)
		if preOut == nil {
			Log.Errorf("can't find outpoint %s", in.PreviousOutPoint)
			return nil, fmt.Errorf("can't find outpoint %s", in.PreviousOutPoint)
		}

		if !bytes.Equal(preOut.PkScript, p2trPkScript) &&
			!bytes.Equal(preOut.PkScript, mulpkScript) {
			result = append(result, nil)
			continue
		}

		switch GetPkScriptType(preOut.PkScript) {
		case txscript.WitnessV1TaprootTy:
			result = append(result, in.Witness[0])
		case txscript.WitnessV0ScriptHashTy:
			result = append(result, in.Witness[pos+1])
		}
	}
	return result, nil
}

func PartialSignTxWithChannel_SatsNet(channel *Channel, tx *swire.MsgTx,
	prevFetcher stxscript.PrevOutputFetcher) ([][]byte, error) {
	return PartialSignTxWithWallet_SatsNet(channel.LocalWallet(), tx, prevFetcher, channel.RedeemScript,
		channel.GetRemotePubKey().SerializeCompressed())
}

func SignAndVerifyTxWithChannel(channel *Channel, tx *wire.MsgTx,
	prevFetcher txscript.PrevOutputFetcher, theirSig [][]byte) ([][]byte, error) {

	sigs, err := FinalSignTxWithWallet(channel.LocalWallet(), tx, prevFetcher, channel.RedeemScript, false,
		channel.GetRemotePubKey().SerializeCompressed(), theirSig)
	if err != nil {
		return nil, err
	}

	if err := VerifySignedTx(tx, prevFetcher); err != nil {
		return nil, err
	}
	return sigs, nil
}

func SignAndVerifyTxWithChannel_SatsNet(channel *Channel, tx *swire.MsgTx,
	prevFetcher stxscript.PrevOutputFetcher, theirSig [][]byte) ([][]byte, error) {

	sigs, err := FinalSignTxWithWallet_SatsNet(channel.LocalWallet(), tx, prevFetcher, channel.RedeemScript,
		channel.GetRemotePubKey().SerializeCompressed(), theirSig)
	if err != nil {
		return nil, err
	}

	if err := VerifySignedTx_SatsNet(tx, prevFetcher); err != nil {
		return nil, err
	}
	return sigs, nil
}

func VerifyTxWithChannel_SatsNet(channel *Channel, tx *swire.MsgTx,
	prevFetcher stxscript.PrevOutputFetcher, theirSig [][]byte) error {
	return VerifyTx_SatsNet(channel.RedeemScript, channel.GetLocalPubKey().SerializeCompressed(),
		channel.GetRemotePubKey().SerializeCompressed(), tx, prevFetcher, theirSig)
}

func VerifyTxWithChannel(channel *Channel, tx *wire.MsgTx,
	prevFetcher txscript.PrevOutputFetcher, theirSig [][]byte) error {
	return VerifyTx(channel.RedeemScript, channel.GetLocalPubKey().SerializeCompressed(),
		channel.GetRemotePubKey().SerializeCompressed(), tx, prevFetcher, theirSig)
}

func addTxOutputsToPrevFetcher(prevFetcher *txscript.MultiPrevOutFetcher, tx *wire.MsgTx) {
	if tx == nil {
		return
	}
	hash := tx.TxHash()
	for i, txOut := range tx.TxOut {
		prevFetcher.AddPrevOut(wire.OutPoint{
			Hash:  hash,
			Index: uint32(i),
		}, txOut)
	}
}

func AddTxOutputsToPrevFetcher(prevFetcher *txscript.MultiPrevOutFetcher, tx *wire.MsgTx) {
	addTxOutputsToPrevFetcher(prevFetcher, tx)
}

func hasChannelInput(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher, channelPkScript []byte) bool {
	for _, txIn := range tx.TxIn {
		prevOut := prevFetcher.FetchPrevOutput(txIn.PreviousOutPoint)
		if prevOut != nil && bytes.Equal(prevOut.PkScript, channelPkScript) {
			return true
		}
	}
	return false
}

func HasChannelInput(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher, channelPkScript []byte) bool {
	return hasChannelInput(tx, prevFetcher, channelPkScript)
}
