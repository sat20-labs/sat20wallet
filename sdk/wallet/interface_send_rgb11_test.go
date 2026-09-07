package wallet

import (
	"context"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

// An L1 indexer sees an issued RGB carrier as ordinary BTC. Exercise real
// issuance and the shared selectors rather than putting RGB in the indexer.
func TestSendSelectorsProtectIssuedRGBWithoutDerivedLock(t *testing.T) {
	local := NewInternalWalletWithMnemonic("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "", &chaincfg.TestNet4Params)
	script, err := AddrToPkScript(local.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	carrier := fmt.Sprintf("%064x:0", 81001)
	plain := fmt.Sprintf("%064x:0", 81002)
	rpc := &rgb11FlowIndexer{outputs: map[string]*TxOutput{}, plain: []*indexerwire.TxOutputInfo{{OutPoint: carrier, Value: 10000, PkScript: script}}}
	evidence := &rgb11FlowEvidence{utxos: map[string]*rgb11wallet.BitcoinUTXO{carrier: {OutPoint: carrier, Value: 10000, PkScript: script, Confirmations: 6}}, rawTx: map[string][]byte{}, spendingTx: map[string]string{}}
	for _, point := range []string{carrier, plain} {
		out := indexer.NewTxOutput(10000)
		out.OutPointStr = point
		out.OutValue.PkScript = script
		rpc.outputs[point] = out
	}
	manager := newRGB11FlowManager(t, local, rpc, evidence, 81001)
	issued, err := manager.IssueRGB11Asset(context.Background(), RGB11IssueRequest{Schema: "NIA", Ticker: "SAFE", Name: "Send protection", Amounts: []uint64{100}})
	if err != nil {
		t.Fatal(err)
	}
	if len(issued.OutPoints) != 1 || issued.OutPoints[0] != carrier {
		t.Fatal("wrong issuance carrier")
	}
	if err := manager.utxoLockerL1.UnlockUtxo(carrier); err != nil {
		t.Fatal(err)
	}
	if manager.utxoLockerL1.IsLocked(carrier) {
		t.Fatal("test requires a missing derived lock")
	}
	if !manager.isL1SendInputProtected(carrier) {
		t.Fatal("RGB carrier became spendable as BTC")
	}
	rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{OutPoint: plain, Value: 10000, PkScript: script})
	for _, inChannel := range []bool{false, true} {
		tx := wire.NewMsgTx(2)
		estimate := &utils.TxWeightEstimator{}
		_, _, _, _, _, err = manager.SelectUtxosForPlainSats(local.GetAddress(), nil, 1000, 1, tx, estimate, false, inChannel, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(tx.TxIn) != 1 || tx.TxIn[0].PreviousOutPoint.String() != plain {
			t.Fatalf("inChannel=%v selected RGB: %v", inChannel, tx.TxIn)
		}
		_, _, _, _, _, err = manager.SelectUtxosForGarbage(local.GetAddress(), nil, []string{carrier}, 1000, 1, wire.NewMsgTx(2), &utils.TxWeightEstimator{}, false, inChannel)
		if err == nil {
			t.Fatalf("inChannel=%v garbage accepted an RGB carrier", inChannel)
		}
	}
	// BRC-20 transfers first create an inscription. Explicit default inputs
	// must not bypass the same protection used by automatic fee selection.
	if _, err := manager.inscribeV2(NewUtxoMgr(local.GetAddress(), rpc), local.GetAddress(),
		map[string]bool{}, `{"p":"brc-20","op":"transfer"}`, 1,
		[]*TxOutput{rpc.outputs[carrier]}, true, nil, nil, 0, nil, false); err == nil {
		t.Fatal("BRC-20 transfer inscription accepted an RGB carrier")
	}
	balance, err := manager.GetRGB11AssetBalance(&issued.AssetName)
	if err != nil || balance == nil || balance.Value.Uint64() != 100 {
		t.Fatalf("RGB balance changed: %v %v", balance, err)
	}
}
