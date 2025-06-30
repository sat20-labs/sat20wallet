package wallet

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/runes/runestone"
)

func TestRunePayload(t *testing.T) {
	hexPayload := "6a5d0800a0900564904e02"
	paylaod, _ := hex.DecodeString(hexPayload)

	stone := runestone.Runestone{}
	result, err := stone.DecipherFromPkScript(paylaod)
	if err != nil {
		t.Fatal(err)
	}

	fmt.Printf("result: %v\n", result)

	fmt.Printf("edicts:\n")
	for _, v := range result.Runestone.Edicts {
		fmt.Printf("%v\n", v)
	}
}



func TestParseRuneEtching(t *testing.T) {
	// hexPayload := "6a5d18020104eb8385e294b4e2f718033005964e0680e8e6a78e06" // RUNESXBITCOIN
	hexPayload := "6a5d2102010487a1c3f0c0ebf7fb9d01010503d4040595e80706808084fea6dee1111601"  // DOGGOTOTHEMOON
	paylaod, _ := hex.DecodeString(hexPayload)

	stone := runestone.Runestone{}
	result, err := stone.DecipherFromPkScript(paylaod)
	if err != nil {
		t.Fatal(err)
	}

	printEtching(result.Runestone.Etching)
	fmt.Printf("Etching: %v\n", result.Runestone.Etching)

	fmt.Printf("Mint:\n %v", result.Runestone.Mint)
	
	fmt.Printf("edicts:\n")
	for _, v := range result.Runestone.Edicts {
		fmt.Printf("%v\n", v)
	}

	fmt.Printf("Pointer :%v\n", *result.Runestone.Pointer )  // output index
}

func printEtching(etching *runestone.Etching) {
	fmt.Printf("Runes: %s\n", etching.Rune.String())
	fmt.Printf("Commit: %s\n", hex.EncodeToString(etching.Rune.Commitment()))
	fmt.Printf("Symbol: %s\n", string(*etching.Symbol))
	fmt.Printf("Spacers: %d\n", *etching.Spacers)
	if etching.Premine != nil {
		fmt.Printf("Premine: %s\n", etching.Premine.String())
	}
	if etching.Divisibility != nil {
		fmt.Printf("Divisibility: %d\n", *etching.Divisibility)
	}
	if etching.Terms != nil {
		fmt.Printf("Terms: %v", *etching.Terms)
	}
	fmt.Printf("Turbo: %v\n", etching.Turbo)
	
}

func TestRuneEtching(t *testing.T) {
	
	// symbol: UTF-16BE	
	etching, err := GenEtching("MMMM•TEST•HHHH•MMMM", 0x058D, 100000000)
	//etching, err := GenEtching("DOG•GO•TO•THE•MOON", 0x058D, 100000000)
	if err != nil {
		t.Fatal(err.Error())
	}

	printEtching(etching)

	// check 
	runeCommit := etching.Rune.Commitment()
	rune := runestone.NewRune(indexer.ParseRunesName(runeCommit))
	fmt.Printf("%s\n", rune.String())
	fmt.Printf("%s\n", etching.Rune.String())
	if rune.String() != etching.Rune.String() {
		t.Fatal("")
	}

	stone := runestone.Runestone{
		Etching: etching,
	}
	nullData, err := stone.Encipher()
	if err != nil {
		t.Fatal(err)
	}


	hexPayload := "6a5d19020104baa392f5d488e9d1cc9a13038811058d0b0680c2d72f" 
	payload, _ := hex.DecodeString(hexPayload)

	if !bytes.Equal(nullData, payload) {
		t.Fatal(err)
	}

	result, err := stone.DecipherFromPkScript(payload)
	if err != nil {
		t.Fatal(err)
	}

	printEtching(result.Runestone.Etching)
	
}

/*
全部预挖
{
    "runestone": {
        "pointer": 1,
        "edicts": [],
        "etching": {
            "symbol": "🐕",
            "premine": 10000000000000000,
            "spacers": 596,
            "turbo": false,
            "divisibility": 5,
            "rune": "DOGGOTOTHEMOON"
        }
    }
}

https://www.oklink.com/zh-hans/bitcoin/tx/7d0a2dd897222913d58fc957b0429526117a0a61c964642fe93b077f328ccec1
部分预挖，总量= premine + amount*cap = 95000000000 + 50000 * 100000
{
    "runestone": {
        "pointer": 2,
        "edicts": [],
        "etching": {
            "symbol": "💥",
            "premine": 95000000000,
            "terms": {
                "amount": 50000,
                "cap": 100000,
                "offset": [
                    null,
                    null
                ],
                "height": [
                    null,
                    null
                ]
            },
            "spacers": 2184,
            "turbo": true,
            "divisibility": 2,
            "rune": "EPICEPICEPICEPIC"
        }
    }
}

https://www.oklink.com/zh-hans/bitcoin/tx/9327998a4aee68a6792db8b00540976ebf81b32ef3c0fd52a43d4ce1e3c5cf11
无预挖
{
    "runestone": {
        "edicts": [],
        "etching": {
            "symbol": "♨",
            "terms": {
                "amount": 210,
                "cap": 100000000,
                "offset": [
                    4032,
                    8064
                ],
                "height": [
                    null,
                    null
                ]
            },
            "spacers": 72,
            "turbo": true,
            "rune": "COOKTHEMEMPOOL"
        }
    }
}



"etching": {
            "symbol": "✖",
            "premine": 210000000000,
            "spacers": 48,
            "turbo": false,
            "rune": "RUNESXBITCOIN"
}

{
    "runestone": {
        "mint": "1:0",
        "edicts": []
    }
}

{
    "runestone": {
        "edicts": [
            {
                "output": 1,
                "amount": 61158500000000,
                "id": "840000:35"
            }
        ]
    }
}

*/

func TestParseOrdinals(t *testing.T) {

	witnessHex := "20db8fe1cebdd4720fedf41695e42e92f4a205d581ea90abce5ceae951d955a02aac0063036f7264010118746578742f706c61696e3b636861727365743d7574662d38004ca97b2270223a226f726478222c226f70223a226465706c6f79222c227469636b223a2274657374646f67222c226d6178223a223231303030222c226c696d223a223231303030222c226e223a22313030222c2273656c66223a22313030222c22646573223a22303336376632366166323364633430666461643036373532633338323634666536323162376262616662316434316162343336623837646564313932663133333665227d68" 
	witness, _ := hex.DecodeString(witnessHex)


	inscriptions, err := indexer.ParseInscription([][]byte{witness})
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range inscriptions {
		fmt.Printf("%d\n", k)
		for field, content := range v {
			fmt.Printf("field %d\n", field)
			if field == 11 {
				fmt.Printf("inscriptionId: %s\n", indexer.ParseInscriptionId(content)) 
			} else if field == 13 {
				runes := runestone.NewRune(indexer.ParseRunesName(content))
				fmt.Printf("runes name: %s\n", runes.String()) // 9df7df5c0e10d087
			} else {
				fmt.Printf("content: %s\n", string(content))
			}
		}
	}
	fmt.Printf("\n")
}

