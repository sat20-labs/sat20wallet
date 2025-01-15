package wallet

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/wire"
	swire "github.com/sat20-labs/satsnet_btcd/wire"
)


func TestAssets(t *testing.T) {
	var assets0 swire.TxAssets

	assets1 := swire.TxAssets{}

	assets2 := swire.TxAssets{
		{
			Name: AssetName{
				Protocol: "ordx",
				Type: "f",
				Ticker: "pearl",
			},
			Amount: 10000,
			BindingSat: 1,
		},

		{
			Name: AssetName{
				Protocol: "ordx",
				Type: "f",
				Ticker: "pizza",
			},
			Amount: 5000,
			BindingSat: 1,
		},
	}

	aa := []swire.TxAssets{assets0, assets1, assets2}

	for _, assets := range aa {
		buf0, err := assets.Serialize()
		if err != nil {
			t.Fail()
		}
		var assets00 swire.TxAssets
		err = assets00.Deserialize(buf0)
		if err != nil {
			t.Fail()
		}
	}
}


func TestAnchorScript(t *testing.T) {
	assets := swire.TxAssets{
		{
			Name: AssetName{
				Protocol: "ordx",
				Type: "f",
				Ticker: "pearl",
			},
			Amount: 10000,
			BindingSat: 1,
		},
	}
	chanpoint := "5ac156337fb9bc5fb8fd760b3abc72d3e50515857d3e42282c5705a49fd5b850:0"
	txOutput := TxOutput{
		OutPointStr: chanpoint,
		OutValue: wire.TxOut{Value: 1000},
		Assets: assets,
	}
	var witnessScript [71]byte

	anchorPkScript, err := StandardAnchorScript(txOutput.OutPointStr, witnessScript[:],
		txOutput.Value(), txOutput.Assets)
	if err != nil {
		t.Fatalf("")
	}

	utxo, witnessScript2, value, assets, err := ParseStandardAnchorScript(anchorPkScript)
	if err != nil {
		t.Fatalf("")
	}
	if utxo != chanpoint {
		t.Fatalf("")
	}
	if !bytes.Equal(witnessScript2, witnessScript[:]) {
		t.Fatalf("")
	}
	if value != txOutput.Value() {
		t.Fatalf("")
	}
	if !assets.Equal(&txOutput.Assets) {
		t.Fatalf("")
	}
}


func TestParseAnchorScript(t *testing.T) {
	
	anchorPkScript := "42363564623638323264653835656135646630366366323331386565656239623933326633633265353466653264376662353432336561373838646661636564663a304752210367f26af23dc40fdad06752c38264fe621b7bbafb1d41ab436b87ded192f1336e2103e1b100115fb667b374734510b76ddcc937fbdf7e8b238258be79c00f08b6401e52ae02942000"
	by, _ := hex.DecodeString(anchorPkScript)
	utxo, witnessScript2, value, assets, err := ParseStandardAnchorScript(by)
	if err != nil {
		t.Fatalf("")
	}
	fmt.Printf("%s, %v, %d, %v", utxo, witnessScript2, value, assets)

}
