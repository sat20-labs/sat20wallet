package wallet

import (
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

func TestInscribe(t *testing.T) {
	network := &chaincfg.TestNet3Params
	mnemonic := "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire"
	wallet := NewInternalWalletWithMnemonic(mnemonic, "", GetChainParam())
	if wallet == nil {
		t.Fatal("NewWalletWithMnemonic failed")
	}

	pkScript, _ := GetP2TRpkScript(wallet.GetPaymentPubKey())
	address := wallet.GetAddress()

	commitTxPrevOutputList := make([]*PrevOutput, 0)
	commitTxPrevOutputList = append(commitTxPrevOutputList, &TxOutput{
		OutPointStr: "453aa6dd39f31f06cd50b72a8683b8c0402ab36f889d96696317503a025a21b5:0",
		OutValue: wire.TxOut{
			Value:    546,
			PkScript: pkScript,
		},
	})
	commitTxPrevOutputList = append(commitTxPrevOutputList, &PrevOutput{
		OutPointStr: "22c8a4869f2aa9ee5994959c0978106130290cda53f6e933a8dda2dcb82508d4:0",
		OutValue: wire.TxOut{
			Value:    546,
			PkScript: pkScript,
		},
	})
	commitTxPrevOutputList = append(commitTxPrevOutputList, &PrevOutput{
		OutPointStr: "aa09fa48dda0e2b7de1843c3db8d3f2d7f2cbe0f83331a125b06516a348abd26:4",
		OutValue: wire.TxOut{
			Value:    546,
			PkScript: pkScript,
		},
	})

	// inscriptionDataList := make([]InscriptionData, 0)
	// inscriptionDataList = append(inscriptionDataList, InscriptionData{
	// 	ContentType: "text/plain;charset=utf-8",
	// 	Body:        []byte(`{"p":"brc-20","op":"mint","tick":"xcvb","amt":"100"}`),

	// })
	// inscriptionDataList = append(inscriptionDataList, InscriptionData{
	// 	ContentType: "text/plain;charset=utf-8",
	// 	Body:        []byte(`{"p":"brc-20","op":"mint","tick":"xcvb","amt":"10"}`),

	// })
	// inscriptionDataList = append(inscriptionDataList, InscriptionData{
	// 	ContentType: "text/plain;charset=utf-8",
	// 	Body:        []byte(`{"p":"brc-20","op":"mint","tick":"xcvb","amt":"10000"}`),

	// })
	// inscriptionDataList = append(inscriptionDataList, InscriptionData{
	// 	ContentType: "text/plain;charset=utf-8",
	// 	Body:        []byte(`{"p":"brc-20","op":"mint","tick":"xcvb","amt":"1"}`),

	// })

	request := &InscriptionRequest{
		CommitTxPrevOutputList: commitTxPrevOutputList,
		CommitFeeRate:          2,
		RevealFeeRate:          2,
		RevealOutValue:         330,
		InscriptionData: InscriptionData{
			ContentType: "text/plain;charset=utf-8",
			Body:        []byte(`{"p":"brc-20","op":"mint","tick":"xcvb","amt":"10000"}`),
		},
		DestAddress:   address,
		ChangeAddress: address,
		Signer:        wallet.SignTx,
	}

	txs, err := Inscribe(network, request, 0)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("%v\n", txs)

	fmt.Printf("commit Fee: %d\n", txs.CommitTxFee)
	fmt.Printf("reveal Fee: %d\n", txs.RevealTxFee)
	fmt.Printf("fee: %d\n", EstimatedInscribeFee(len(commitTxPrevOutputList),
		len(request.InscriptionData.Body), 2, 0))
}

func TestInscribeTransfer(t *testing.T) {
	network := &chaincfg.TestNet3Params
	mnemonic := "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire"
	wallet := NewInternalWalletWithMnemonic(mnemonic, "", GetChainParam())
	if wallet == nil {
		t.Fatal("NewWalletWithMnemonic failed")
	}

	pkScript, _ := GetP2TRpkScript(wallet.GetPaymentPubKey())
	address := wallet.GetAddress()

	commitTxPrevOutputList := make([]*PrevOutput, 0)
	commitTxPrevOutputList = append(commitTxPrevOutputList, &PrevOutput{
		OutPointStr: "aa09fa48dda0e2b7de1843c3db8d3f2d7f2cbe0f83331a125b06516a348abd26:4",
		OutValue: wire.TxOut{
			Value:    1142196,
			PkScript: pkScript,
		},
	})

	request := &InscriptionRequest{
		CommitTxPrevOutputList: commitTxPrevOutputList,
		CommitFeeRate:          1,
		RevealFeeRate:          1,
		RevealOutValue:         330,
		InscriptionData: InscriptionData{
			ContentType: "text/plain;charset=utf-8",
			Body:        []byte(`{"p":"brc-20","op":"transfer","tick":"ordi","amt":"100"}`),
		},
		DestAddress:   address,
		ChangeAddress: address,
		Signer:        wallet.SignTx,
	}

	txs, err := Inscribe(network, request, 0)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("commit Fee: %d\n", txs.CommitTxFee)
	rawTx, err := EncodeMsgTx(txs.CommitTx)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("commit Tx: %s\n", rawTx)

	fmt.Printf("reveal Fee: %d\n", txs.RevealTxFee)
	rawTx2, err := EncodeMsgTx(txs.RevealTx)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("reveal Tx: %s\n", rawTx2)

	fmt.Printf("commit vsize: %d\n", GetTxVirtualSize2(txs.CommitTx))
	fmt.Printf("reveal vsize: %d\n", GetTxVirtualSize2(txs.RevealTx))
	fmt.Printf("length of body: %d\n", len(request.InscriptionData.Body))

	fmt.Printf("fee: %d\n", EstimatedInscribeFee(1, len(request.InscriptionData.Body), 1, 0))
}

// 涉及引导节点
func testCommitDelayScriptForServer(selfKey, bootstrapKey, revokeKey *btcec.PublicKey, csvDelay uint32) (
	[]byte, []byte, error) {

	toLocalRedeemScript, err := utils.CommitScriptToSelf2(
		csvDelay, selfKey, bootstrapKey, revokeKey,
	)
	if err != nil {
		return nil, nil, err
	}

	toLocalScriptHash, err := utils.WitnessScriptHash(
		toLocalRedeemScript,
	)
	if err != nil {
		return nil, nil, err
	}

	return toLocalScriptHash,
		toLocalRedeemScript,
		nil

}

func testCommitDelayScriptForClient(selfKey, revokeKey *btcec.PublicKey, csvDelay uint32) (
	[]byte, []byte, error) {

	toLocalRedeemScript, err := utils.CommitScriptToSelf(
		csvDelay, selfKey, revokeKey,
	)
	if err != nil {
		return nil, nil, err
	}

	toLocalScriptHash, err := utils.WitnessScriptHash(
		toLocalRedeemScript,
	)
	if err != nil {
		return nil, nil, err
	}

	return toLocalScriptHash,
		toLocalRedeemScript,
		nil

}

// 涉及引导节点
func testCommitDirectScriptForServer(remoteKey *btcec.PublicKey, bootstrapKey *btcec.PublicKey) (
	[]byte, []byte, error) {
	// First, create the 2-of-2 multi-sig script itself.
	witnessScript, err := utils.GenMultiSigScript(remoteKey.SerializeCompressed(), bootstrapKey.SerializeCompressed())
	if err != nil {
		return nil, nil, err
	}

	// With the 2-of-2 script in had, generate a p2wsh script which pays
	// to the funding script.
	pkScript, err := utils.WitnessScriptHash(witnessScript)
	if err != nil {
		return nil, nil, err
	}

	// Since this is a regular P2WKH, the WitnessScipt and PkScript
	// should both be set to the script hash.
	return pkScript,
		witnessScript,
		nil

}

func testCommitDirectScriptForClient(remoteKey *btcec.PublicKey) ([]byte, error) {

	pkScriptA, err := GetP2TRpkScript(remoteKey)
	if err != nil {
		return nil, err
	}

	return pkScriptA, nil
}

func TestCalcTransferFee(t *testing.T) {
	src := "tb1p62gjhywssq42tp85erlnvnumkt267ypndrl0f3s4sje578cgr79sekhsua"
	dest := "tb1qw86hsm7etf4jcqqg556x94s6ska9z0239ahl0tslsuvr5t5kd0nq7vh40m" // channel
	assetName := &indexer.AssetName{
		Protocol: indexer.PROTOCOL_NAME_BRC20,
		Type:     indexer.ASSET_TYPE_FT,
		Ticker:   "ordi",
	}
	amt := indexer.NewDecimal(100000, 18)

	fmt.Printf("deploy\n")
	for i := 1; i < 3; i++ {
		fee, err := CalcFeeForDeployTicker_brc20(i, src, dest, assetName.Ticker, 10000000000, 10000000000, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("%d: fee = %d\n", i, fee-330)
	}

	fmt.Printf("\nmint\n")
	for i := 1; i < 3; i++ {
		fee, err := CalcFeeForMintAsset_brc20(i, src, dest, assetName, amt, 1)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("%d: fee = %d\n", i, fee-330)
	}

	wallet1, _, _ := NewInteralWallet(&chaincfg.TestNet3Params)
	wallet2, _, _ := NewInteralWallet(&chaincfg.TestNet3Params)
	wallet3, _, _ := NewInteralWallet(&chaincfg.TestNet3Params)

	_, serverDelayWitness, _ := testCommitDelayScriptForServer(wallet1.GetPubKey(), wallet2.GetPubKey(), wallet3.GetPubKey(), 1440)
	_, serverDirectWitness, _ := testCommitDirectScriptForServer(wallet1.GetPubKey(), wallet2.GetPubKey())
	_, clientDelayWitness, _ := testCommitDelayScriptForClient(wallet1.GetPubKey(), wallet3.GetPubKey(), 1440)
	clientDirectWitness, _ := testCommitDirectScriptForClient(wallet1.GetPubKey())

	fmt.Printf("serverDelayWitness length %d\n", len(serverDelayWitness))
	fmt.Printf("serverDirectWitness length %d\n", len(serverDirectWitness))
	fmt.Printf("clientDelayWitness length %d\n", len(clientDelayWitness))
	fmt.Printf("clientDirectWitness length %d\n", len(clientDirectWitness))

	type cases struct {
		st      int
		witness []byte
	}

	witnesV := []*cases{
		{SCRIPT_TYPE_TAPROOTKEYSPEND, nil},
		{SCRIPT_TYPE_CHANNEL, nil},
		{SCRIPT_TYPE_PUNISH, serverDelayWitness},
		{SCRIPT_TYPE_PUNISH, serverDirectWitness},
		{SCRIPT_TYPE_PUNISH, clientDelayWitness},
		{SCRIPT_TYPE_PUNISH, clientDirectWitness},

		{SCRIPT_TYPE_SWEEP, serverDelayWitness},
		{SCRIPT_TYPE_SWEEP, serverDirectWitness},
		{SCRIPT_TYPE_SWEEP, clientDelayWitness},
		{SCRIPT_TYPE_SWEEP, clientDirectWitness},
	}

	fmt.Printf("\nmint-transfer\n")
	for i, c := range witnesV {
		fee, err := CalcFeeForMintTransfer(1, src, dest, c.st, c.witness, assetName, amt, 1)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("%d %d:  fee = %d\n", i, c.st, fee-330)
	}
}
