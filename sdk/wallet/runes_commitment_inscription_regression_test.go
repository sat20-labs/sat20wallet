package wallet

import (
	"bytes"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

func TestRuneOnlyCommitmentDoesNotCreateOrdinalInscription(t *testing.T) {
	privateKey, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{1}, 32))
	etching, err := GenEtchingWithTerms("SAT•ACCEPTANCE•TEST", 'T', 10000, 0, true, 2)
	if err != nil {
		t.Fatal(err)
	}
	commitment := etching.Rune.Commitment()
	context, err := newInscriptionTxCtxData(&chaincfg.RegressionNetParams,
		&InscriptionRequest{InscriptionData: InscriptionData{RuneName: commitment}}, privateKey.PubKey())
	if err != nil {
		t.Fatal(err)
	}
	pushes, err := txscript.PushedData(context.InscriptionScript)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, push := range pushes {
		found = found || bytes.Equal(push, commitment)
	}
	if !found {
		t.Fatal("official Rune commitment data push is missing")
	}
	assertRuneScriptHasNoInscriptionEnvelope(t, context.InscriptionScript)
}

func assertRuneScriptHasNoInscriptionEnvelope(t *testing.T, script []byte) {
	t.Helper()
	tokenizer := txscript.MakeScriptTokenizer(0, script)
	for tokenizer.Next() {
		if tokenizer.Opcode() == txscript.OP_IF {
			t.Fatal("pure Rune commitment must not create an inscription envelope, even when its name bytes equal ord")
		}
	}
	if err := tokenizer.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestRuneCommitmentOrdinalMarkerCollision(t *testing.T) {
	privateKey, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{1}, 32))
	context, err := newInscriptionTxCtxData(&chaincfg.RegressionNetParams,
		&InscriptionRequest{InscriptionData: InscriptionData{RuneName: []byte("ord")}}, privateKey.PubKey())
	if err != nil {
		t.Fatal(err)
	}
	assertRuneScriptHasNoInscriptionEnvelope(t, context.InscriptionScript)
}

func TestRuneOnlyRevealExecutesTaprootCommitment(t *testing.T) {
	privateKey, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{1}, 32))
	payer, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(txscript.ComputeTaprootKeyNoScript(privateKey.PubKey())), &chaincfg.RegressionNetParams)
	if err != nil {
		t.Fatal(err)
	}
	pkScript, err := txscript.PayToAddrScript(payer)
	if err != nil {
		t.Fatal(err)
	}
	request := &InscriptionRequest{
		CommitTxPrevOutputList: []*PrevOutput{{OutPointStr: "0000000000000000000000000000000000000000000000000000000000000001:0", OutValue: wire.TxOut{Value: 10000, PkScript: pkScript}}},
		CommitFeeRate:          2, RevealFeeRate: 2, RevealOutValue: 660,
		RevealPrivateKey: bytes.Repeat([]byte{2}, 32),
		InscriptionData:  InscriptionData{RuneName: []byte("ord")},
		DestAddress:      payer.EncodeAddress(), ChangeAddress: payer.EncodeAddress(),
		Signer: func(tx *wire.MsgTx, fetcher txscript.PrevOutputFetcher) error {
			hashes := txscript.NewTxSigHashes(tx, fetcher)
			for i, input := range tx.TxIn {
				previous := fetcher.FetchPrevOutput(input.PreviousOutPoint)
				witness, err := txscript.TaprootWitnessSignature(tx, hashes, i, previous.Value, previous.PkScript, txscript.SigHashDefault, privateKey)
				if err != nil {
					return err
				}
				input.Witness = witness
			}
			return nil
		},
	}
	tool, err := NewInscriptionTool(&chaincfg.RegressionNetParams, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySignedTx(tool.CommitTx, tool.CommitTxPrevOutputFetcher); err != nil {
		t.Fatal(err)
	}
	if err := VerifySignedTx(tool.RevealTx, tool.RevealTxPrevOutputFetcher); err != nil {
		t.Fatal(err)
	}
	assertRuneScriptHasNoInscriptionEnvelope(t, tool.InscriptionTxCtxData.InscriptionScript)
}

func TestRuneCommitmentKeepsOrdinaryInscriptionBytes(t *testing.T) {
	privateKey, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{1}, 32))
	for _, runeName := range [][]byte{nil, {0x12, 0x34}} {
		request := &InscriptionRequest{InscriptionData: InscriptionData{
			ContentType: "text/plain;charset=utf-8", Body: []byte(`{"p":"brc-20","op":"mint","tick":"test","amt":"1"}`), RuneName: runeName,
		}}
		context, err := newInscriptionTxCtxData(&chaincfg.RegressionNetParams, request, privateKey.PubKey())
		if err != nil {
			t.Fatal(err)
		}
		expected := txscript.NewScriptBuilder().AddData(schnorr.SerializePubKey(privateKey.PubKey())).
			AddOp(txscript.OP_CHECKSIG).AddOp(txscript.OP_FALSE).AddOp(txscript.OP_IF).
			AddData([]byte(OrdPrefix)).AddOp(txscript.OP_DATA_1).
			AddOp(indexer.FIELD_CONTENT_TYPE).AddData([]byte(request.InscriptionData.ContentType))
		if len(runeName) > 0 {
			expected.AddOp(indexer.FIELD_RUNE_NAME).AddData(runeName)
		}
		want, err := expected.AddOp(txscript.OP_0).AddFullData(request.InscriptionData.Body).Script()
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, txscript.OP_ENDIF)
		if !bytes.Equal(want, context.InscriptionScript) {
			t.Fatal("ordinary inscription script bytes changed")
		}
	}
}

func TestRunePremineMixedCarrierRemainsProtected(t *testing.T) {
	runeName := indexer.AssetName{Protocol: "runes", Type: "f", Ticker: "SAT•ACCEPTANCE•TEST"}
	output := &TxOutput{Assets: indexer.TxAssets{{Name: runeName, Amount: *indexer.NewDecimal(10000, 2)}}}
	if HasMultiAsset(output, &runeName) {
		t.Fatal("pure Rune carrier was incorrectly rejected")
	}
	output.Assets = append(output.Assets, indexer.AssetInfo{
		Name: indexer.AssetName{Protocol: "ordx", Type: "o", Ticker: "13"}, Amount: *indexer.NewDecimal(1, 0), BindingSat: 1,
	})
	if !HasMultiAsset(output, &runeName) {
		t.Fatal("mixed Rune/NFT carrier must remain protected from sending only the Rune")
	}
}
