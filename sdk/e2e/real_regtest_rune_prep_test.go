package e2e

import (
	"bytes"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/runes/runestone"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

func TestSDKPWARegtestMixedRuneInscriptionBuild(t *testing.T) {
	privateKey, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{1}, 32))
	address, err := btcutil.NewAddressTaproot(
		schnorr.SerializePubKey(txscript.ComputeTaprootKeyNoScript(privateKey.PubKey())),
		&chaincfg.RegressionNetParams,
	)
	require.NoError(t, err)
	pkScript, err := txscript.PayToAddrScript(address)
	require.NoError(t, err)

	etching, err := wallet.GenEtchingWithTerms("SAT•ACCEPTANCE•MIXED•T", 'T', 10000, 0, true, 2)
	require.NoError(t, err)
	commitment := etching.Rune.Commitment()
	pointer := uint32(0)
	runestonePayload, err := (&runestone.Runestone{Etching: etching, Pointer: &pointer}).Encipher()
	require.NoError(t, err)

	request := &wallet.InscriptionRequest{
		CommitTxPrevOutputList: wallet.PrevOutputs{{
			OutPointStr: "0000000000000000000000000000000000000000000000000000000000000001:0",
			OutValue:    wire.TxOut{Value: 100000, PkScript: pkScript},
		}},
		CommitFeeRate:  2,
		RevealFeeRate:  2,
		RevealOutValue: 660,
		MinChangeValue: 330,
		InscriptionData: wallet.InscriptionData{
			ContentType:      "application/octet-stream",
			Body:             commitment,
			RevealTxNullData: runestonePayload,
		},
		DestAddress:   address.EncodeAddress(),
		ChangeAddress: address.EncodeAddress(),
		Signer: func(tx *wire.MsgTx, prevOutputFetcher txscript.PrevOutputFetcher) error {
			hashes := txscript.NewTxSigHashes(tx, prevOutputFetcher)
			for i, input := range tx.TxIn {
				prevOutput := prevOutputFetcher.FetchPrevOutput(input.PreviousOutPoint)
				witness, err := txscript.TaprootWitnessSignature(tx, hashes, i,
					prevOutput.Value, prevOutput.PkScript, txscript.SigHashDefault, privateKey)
				if err != nil {
					return err
				}
				input.Witness = witness
			}
			return nil
		},
	}
	reservation, err := wallet.Inscribe(&chaincfg.RegressionNetParams, request, 1)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	require.Equal(t, int64(660), reservation.RevealTx.TxOut[0].Value)
	require.Len(t, reservation.RevealTx.TxOut, 2)
	require.Equal(t, runestonePayload, reservation.RevealTx.TxOut[1].PkScript)

	fields, _, err := indexercommon.ParseInscription(reservation.RevealTx.TxIn[0].Witness)
	require.NoError(t, err)
	require.Len(t, fields, 1)
	require.Equal(t, []byte("application/octet-stream"), fields[0][indexercommon.FIELD_CONTENT_TYPE])
	require.Equal(t, commitment, fields[0][indexercommon.FIELD_CONTENT])
	require.Nil(t, fields[0][indexercommon.FIELD_RUNE_NAME], "the fixture body carries the Rune commitment without using the known-bad RuneName tag combination")
	tapscript := reservation.RevealTx.TxIn[0].Witness[len(reservation.RevealTx.TxIn[0].Witness)-2]
	pushes, err := txscript.PushedData(tapscript)
	require.NoError(t, err)
	commitmentPushes := 0
	for _, pushed := range pushes {
		if bytes.Equal(pushed, commitment) {
			commitmentPushes++
		}
	}
	require.Equal(t, 1, commitmentPushes, "tapscript must contain exactly one complete Rune commitment data push")

	artifact, err := (&runestone.Runestone{}).DecipherFromTx(reservation.RevealTx)
	require.NoError(t, err)
	require.NotNil(t, artifact)
	require.Nil(t, artifact.Cenotaph)
	require.NotNil(t, artifact.Runestone)
	require.NotNil(t, artifact.Runestone.Etching)
	require.NotNil(t, artifact.Runestone.Etching.Rune)
	require.True(t, sdkPWARegtestIsMixedRuneEtching(artifact.Runestone.Etching))
	require.Equal(t, etching.Rune.Value, artifact.Runestone.Etching.Rune.Value)
	require.NotNil(t, artifact.Runestone.Pointer)
	require.Equal(t, uint32(0), *artifact.Runestone.Pointer)
}
