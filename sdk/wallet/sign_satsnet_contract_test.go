package wallet

import (
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func TestSignContractTxSatsNetAllowsOnlyGasAssetBurn(t *testing.T) {
	w := NewInternalWalletWithMnemonic("inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", GetChainParam())
	pkScript, err := GetP2TRpkScript(w.GetPaymentPubKey())
	if err != nil {
		t.Fatal(err)
	}

	gasName := "brc20:f:sgas"
	gasAsset := swire.NewAssetNameFromString(gasName)
	if gasAsset == nil {
		t.Fatalf("invalid gas asset %s", gasName)
	}
	prev := swire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}
	inputAssets := swire.TxAssets{{
		Name:   *gasAsset,
		Amount: *indexer.NewDefaultDecimal(100),
	}}
	outputAssets := swire.TxAssets{{
		Name:   *gasAsset,
		Amount: *indexer.NewDefaultDecimal(90),
	}}
	tx := swire.NewMsgTx(swire.TxVersion)
	tx.AddTxIn(swire.NewTxIn(&prev, nil, nil))
	tx.AddTxOut(swire.NewTxOut(0, outputAssets, pkScript))
	prevFetcher := stxscript.NewMultiPrevOutFetcher(nil)
	prevFetcher.AddPrevOut(prev, swire.NewTxOut(0, inputAssets, pkScript))

	if _, err := SignTxWithWallet_SatsNet(w, tx, prevFetcher); err == nil || !strings.Contains(err.Error(), "some assets spent to miner") {
		t.Fatalf("strict signing should reject gas asset burn, got %v", err)
	}
	allowedBurn := swire.TxAssets{{
		Name:   *gasAsset,
		Amount: *indexer.NewDefaultDecimal(10),
	}}
	if _, err := SignTxWithWalletAllowAssetBurn_SatsNet(w, tx, prevFetcher, allowedBurn); err != nil {
		t.Fatalf("contract signing should allow declared gas asset burn: %v", err)
	}

	tooSmallBurn := swire.TxAssets{{
		Name:   *gasAsset,
		Amount: *indexer.NewDefaultDecimal(9),
	}}
	if _, err := SignTxWithWalletAllowAssetBurn_SatsNet(w, tx, prevFetcher, tooSmallBurn); err == nil {
		t.Fatalf("contract signing should reject undeclared asset burn")
	}
}
