package wallet

import (
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
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
	commitTxPrevOutputList = append(commitTxPrevOutputList, &PrevOutput{
		TxId:       "453aa6dd39f31f06cd50b72a8683b8c0402ab36f889d96696317503a025a21b5",
		VOut:       0,
		Amount:     546,
		PkScript:   pkScript,
	})
	commitTxPrevOutputList = append(commitTxPrevOutputList, &PrevOutput{
		TxId:       "22c8a4869f2aa9ee5994959c0978106130290cda53f6e933a8dda2dcb82508d4",
		VOut:       0,
		Amount:     546,
		PkScript:   pkScript,
	})
	commitTxPrevOutputList = append(commitTxPrevOutputList, &PrevOutput{
		TxId:       "aa09fa48dda0e2b7de1843c3db8d3f2d7f2cbe0f83331a125b06516a348abd26",
		VOut:       4,
		Amount:     1142196,
		PkScript:   pkScript,
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
		InscriptionData:        InscriptionData{
			ContentType: "text/plain;charset=utf-8",
			Body:        []byte(`{"p":"brc-20","op":"mint","tick":"xcvb","amt":"10000"}`),
		},
		DestAddress:            address,
		ChangeAddress:          address,
		Signer:                 wallet.SignTx,
		PublicKey:              wallet.GetPaymentPubKey(),
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
		TxId:       "aa09fa48dda0e2b7de1843c3db8d3f2d7f2cbe0f83331a125b06516a348abd26",
		VOut:       4,
		Amount:     1142196,
		PkScript:   pkScript,
	})

	request := &InscriptionRequest{
		CommitTxPrevOutputList: commitTxPrevOutputList,
		CommitFeeRate:          1,
		RevealFeeRate:          1,
		RevealOutValue:         330,
		InscriptionData:    InscriptionData{
			ContentType: "text/plain;charset=utf-8",
			Body:        []byte(`{"p":"brc-20","op":"transfer","tick":"ordi","amt":"100"}`),
		},
		DestAddress:            address,
		ChangeAddress:          address,
		Signer:                 wallet.SignTx,
		PublicKey:              wallet.GetPaymentPubKey(),
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


func TestCalcFeeOfInscribe(t *testing.T) {
	// InscriptionData:    InscriptionData{
	// 	ContentType: "text/plain;charset=utf-8",
	// 	Body:        []byte(`{"p":"brc-20","op":"transfer","tick":"ordi","amt":"100"}`),
	// },
	//commitFee := 154
	commitTx := "0200000000010126bd8a346a51065b121a33830fbe2c7f2d3f8ddbc34318deb7e2a0dd48fa09aa0400000000fdffffff02e201000000000000225120a52e2c7d31a14534152abc37bf381e47159ff1745d33c2011b9048d4826ee5bc386b1100000000002251208c4a6b130077db156fb22e7946711377c06327298b4c7e6e19a6eaa808d19eba0140764b082f8fe4dca317c4acd43cec9d21979fd42ec2fc86e41b152afda7f28d7fef6648cdd659a65c4dd27e42714145aca2692e26f1f8e20d2844fd903998fae700000000"
	tx, err := DecodeMsgTx(commitTx)
	if err != nil {
		t.Fatal(err)
	}
	var weightEstimate utils.TxWeightEstimator
	for i, txIn := range tx.TxIn {
		var size int64
		for _, w := range txIn.Witness {
			size += int64(len(w))
		}
		size += int64(len(txIn.Witness))
		fmt.Printf("input:%d %d %d\n", i, txIn.SerializeSize(), size)
		weightEstimate.AddWitnessInput(size)
	}
	for i, txOut := range tx.TxOut {
		fmt.Printf("output:%d %d\n", i, len(txOut.PkScript))
		weightEstimate.AddOutput(txOut.PkScript)
	}
	fmt.Printf("fee: %d\n", weightEstimate.Fee(1))

	vsize := GetTxVirtualSize2(tx)
	fmt.Printf("weight:%d \n", vsize)

	//revealFee := 152
	revealTx := "020000000001016ec542b95d32b1e145cf22bca04fcc11c1005751cb45367a11a6caf8a7933e800000000000fdffffff014a010000000000002251208c4a6b130077db156fb22e7946711377c06327298b4c7e6e19a6eaa808d19eba0340848c4917c935543378b9d0f102894d9898d02b4df562fa78890e7282dd10da04283a80d0c7841014034fc9a6597603f6191b91ace8529c07f345c5408c46c9f67e2018ade4f7d34cfe73eba54fb94d07b95e6540e825d9e74afc45b7ff2cc41e93eeac0063036f7264010118746578742f706c61696e3b636861727365743d7574662d3800387b2270223a226272632d3230222c226f70223a227472616e73666572222c227469636b223a226f726469222c22616d74223a22313030227d6821c018ade4f7d34cfe73eba54fb94d07b95e6540e825d9e74afc45b7ff2cc41e93ee00000000"

	tx2, err := DecodeMsgTx(revealTx)
	if err != nil {
		t.Fatal(err)
	}
	var weightEstimate2 utils.TxWeightEstimator
	for _, txIn := range tx2.TxIn {
		// var size int64
		// size = 1
		// for _, w := range txIn.Witness {
		// 	size += int64(len(w))
		// }
		// size += int64(len(txIn.Witness))
		// fmt.Printf("input:%d %d %d\n", i, txIn.Witness.SerializeSize(), size)
		
		//size := txIn.Witness.SerializeSize()
		weightEstimate2.AddWitnessInput(int64(txIn.Witness.SerializeSize()))
	}
	for i, txOut := range tx2.TxOut {
		fmt.Printf("output:%d %d\n", i, len(txOut.PkScript))
		weightEstimate2.AddOutput(txOut.PkScript)
	}
	fmt.Printf("fee: %d\n", weightEstimate2.Fee(1))

	vsize2 := GetTxVirtualSize2(tx2)
	fmt.Printf("weight:%d \n", vsize2)
}


func TestParseInscription(t *testing.T) {

	witnessHex := "205639fe2c85b8f63034bfe9aa69a1f3dc113046284c72f761c36478076054f453ac0063036f7264010118746578742f706c61696e3b636861727365743d7574662d38004ca77b2270223a226f726478222c226f70223a226465706c6f79222c227469636b223a227a657573222c226d6178223a223231303030222c226c696d223a223231303030222c226e223a2231303030222c2273656c66223a22313030222c22646573223a22303336376632366166323364633430666461643036373532633338323634666536323162376262616662316434316162343336623837646564313932663133333665227d68" 
	witness, _ := hex.DecodeString(witnessHex)

	inscriptions, err := indexer.ParseInscription([][]byte{witness})
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range inscriptions {
		fmt.Printf("%d\n", k)
		for field, content := range v {
			if field == indexer.FIELD_CONTENT {
				fmt.Printf("content: %s\n", string(content)) 
			} else if field == indexer.FIELD_DELEGATE {
				fmt.Printf("inscriptionId: %s\n", indexer.ParseInscriptionId(content)) 
			} else if field == indexer.FIELD_CONTENT_TYPE {
				fmt.Printf("content type: %s\n", string(content))
			} else {
				fmt.Printf("%d: %s\n", field, string(content))
			}
		}
	}
	fmt.Printf("\n")
}

