package wallet

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)


func TestAssets(t *testing.T) {
	var assets0 swire.TxAssets

	assets1 := swire.TxAssets{}

	assets2 := swire.TxAssets{
		{
			Name: indexer.AssetName{
				Protocol: "ordx",
				Type: "f",
				Ticker: "pearl",
			},
			Amount: *indexer.NewDefaultDecimal(10000),
			BindingSat: 1,
		},

		{
			Name: indexer.AssetName{
				Protocol: "ordx",
				Type: "f",
				Ticker: "pizza",
			},
			Amount: *indexer.NewDefaultDecimal(5000),
			BindingSat: 1,
		},
	}

	aa := []swire.TxAssets{assets0, assets1, assets2}

	for _, assets := range aa {
		buf0, err := swire.SerializeTxAssets(&assets)
		if err != nil {
			t.Fail()
		}
		var assets00 swire.TxAssets
		err = swire.DeserializeTxAssets(&assets00, buf0)
		if err != nil {
			t.Fail()
		}
	}
}


func TestAnchorScript(t *testing.T) {
	assets := swire.TxAssets{
		{
			Name: indexer.AssetName{
				Protocol: "ordx",
				Type: "f",
				Ticker: "pearl",
			},
			Amount: *indexer.NewDefaultDecimal(10000),
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

	anchorPkScript, err := sindexer.StandardAnchorScriptWithSig(txOutput.OutPointStr, witnessScript[:],
		txOutput.Value(), txOutput.Assets, []byte("signature"))
	if err != nil {
		t.Fatalf("")
	}

	data, err := ParseStandardAnchorScript(anchorPkScript)
	if err != nil {
		t.Fatalf("")
	}
	if data.Utxo != chanpoint {
		t.Fatalf("")
	}
	if !bytes.Equal(data.WitnessScript, witnessScript[:]) {
		t.Fatalf("")
	}
	if data.Value != txOutput.Value() {
		t.Fatalf("")
	}
	if !data.Assets.Equal(txOutput.Assets) {
		t.Fatalf("")
	}
}


func TestParseUtxoId(t *testing.T) {
	
	h, txIndex, vout := indexer.FromUtxoId(113284057661440)
	fmt.Printf("%d %d %d\n", h, txIndex, vout)

}
