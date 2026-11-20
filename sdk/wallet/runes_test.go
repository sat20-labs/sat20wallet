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
	payload, err := GetRunesEstimatePayload(nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("len: %d\n %s\n", len(payload), hex.EncodeToString(payload))
}

func TestRuneMintPayload(t *testing.T) {
	runeId := runestone.RuneId{
		Block: 1,
		Tx:    1,
	}
	{
		payload, err := EncipherRuneMintPayload(&runeId, nil)
		if err != nil {
			t.Fatal(err)
		}
		stone := runestone.Runestone{}
		result, err := stone.DecipherFromPkScript(payload)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("mint: %v %v\n", result.Runestone.Mint, result.Runestone.Pointer)
	}

	{
		pointer := uint32(1)
		payload, err := EncipherRuneMintPayload(&runeId, &pointer)
		if err != nil {
			t.Fatal(err)
		}
		stone := runestone.Runestone{}
		result, err := stone.DecipherFromPkScript(payload)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("mint: %v %d\n", result.Runestone.Mint, *result.Runestone.Pointer)
	}
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

	hexPayload := "6a5d1b020104baa392f5d488e9d1cc9a130100038811058d0b0680c2d72f"
	payload, _ := hex.DecodeString(hexPayload)

	if !bytes.Equal(nullData, payload) {
		t.Fatalf("runestone encoding mismatch: got %x want %x", nullData, payload)
	}

	result, err := stone.DecipherFromPkScript(payload)
	if err != nil {
		t.Fatal(err)
	}

	printEtching(result.Runestone.Etching)

}

func TestRuneEtchingWithTerms(t *testing.T) {
	selfMintEtching, err := GenEtchingWithTerms("MMMM•SELF•HHHH•MMMM", 'S', 100000000, 0, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if selfMintEtching.Premine == nil || selfMintEtching.Premine.Lo != 100000000 {
		t.Fatalf("unexpected self-mint premine: %+v", selfMintEtching.Premine)
	}
	if selfMintEtching.Terms != nil {
		t.Fatalf("self-mint etching should not have terms: %+v", selfMintEtching.Terms)
	}

	openMintEtching, err := GenEtchingWithTerms("MMMM•OPEN•HHHH•MMMM", 'O', 100000000, 10000, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if openMintEtching.Premine != nil {
		t.Fatalf("open-mint etching should not have premine: %+v", openMintEtching.Premine)
	}
	if openMintEtching.Terms == nil || openMintEtching.Terms.Amount == nil || openMintEtching.Terms.Cap == nil {
		t.Fatalf("open-mint etching should have amount and cap: %+v", openMintEtching.Terms)
	}
	if openMintEtching.Terms.Amount.Lo != 10000 || openMintEtching.Terms.Cap.Lo != 10000 {
		t.Fatalf("unexpected open-mint terms: %+v", openMintEtching.Terms)
	}

	stone := runestone.Runestone{Etching: openMintEtching}
	payload, err := stone.Encipher()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := stone.DecipherFromPkScript(payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Runestone.Etching.Terms == nil ||
		decoded.Runestone.Etching.Terms.Amount.Lo != 10000 ||
		decoded.Runestone.Etching.Terms.Cap.Lo != 10000 {
		t.Fatalf("decoded open-mint terms mismatch: %+v", decoded.Runestone.Etching.Terms)
	}
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
