package wallet

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	indexer "github.com/sat20-labs/indexer/common"
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

	inscriptionDataList := make([]InscriptionData, 0)
	inscriptionDataList = append(inscriptionDataList, InscriptionData{
		ContentType: "text/plain;charset=utf-8",
		Body:        []byte(`{"p":"brc-20","op":"mint","tick":"xcvb","amt":"100"}`),
		
	})
	inscriptionDataList = append(inscriptionDataList, InscriptionData{
		ContentType: "text/plain;charset=utf-8",
		Body:        []byte(`{"p":"brc-20","op":"mint","tick":"xcvb","amt":"10"}`),
		
	})
	inscriptionDataList = append(inscriptionDataList, InscriptionData{
		ContentType: "text/plain;charset=utf-8",
		Body:        []byte(`{"p":"brc-20","op":"mint","tick":"xcvb","amt":"10000"}`),
		
	})
	inscriptionDataList = append(inscriptionDataList, InscriptionData{
		ContentType: "text/plain;charset=utf-8",
		Body:        []byte(`{"p":"brc-20","op":"mint","tick":"xcvb","amt":"1"}`),
		
	})

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
		CommitFeeRate:          2,
		RevealFeeRate:          2,
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
	fmt.Printf("%v\n", txs)
	txsBytes, err := json.Marshal(txs)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("%s\n", string(txsBytes))
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

