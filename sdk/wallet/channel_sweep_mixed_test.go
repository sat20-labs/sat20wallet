package wallet

import (
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

func mixedRecoveryFixture(t *testing.T, feeValue int64) (*Manager, *ChannelCommitment, map[string]*TxOutput, []int, []byte) {
	t.Helper()
	atomicalsTestEnvironment(t)
	manager := safetyTestManager(t, &Channel{ChannelInDB: *NewChannelInDB()})
	atomicalsTestConfigure(manager, atomicalsTestName())
	ours := []byte{txscript.OP_TRUE}
	other := []byte{txscript.OP_2}
	commit := NewChannelCommitment()
	commit.LocalBalance[*atomicalsTestName()] = indexer.NewDefaultDecimal(1000)
	brc := AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_BRC20, Type: indexer.ASSET_TYPE_FT, Ticker: "ordi"}}
	commit.LocalBalance[brc] = indexer.NewDefaultDecimal(100)
	commit.CommitTx = minimalPersistedCommitmentTx(101)
	// ARC precedes the BRC account carrier. The delayed owner's plain reserve
	// is penultimate, as it is after the server commitment script rebalance.
	commit.CommitTx.TxOut = []*wire.TxOut{
		wire.NewTxOut(1000, ours), wire.NewTxOut(330, ours),
		wire.NewTxOut(feeValue, ours), wire.NewTxOut(10_000, other),
	}
	commit.RecoveryFeeIndexes = []uint32{2, 3}
	predecessor := minimalPersistedCommitmentTx(102)
	reveal := wire.NewMsgTx(2)
	point := wire.OutPoint{Hash: predecessor.TxHash(), Index: 0}
	reveal.AddTxIn(wire.NewTxIn(&point, nil, nil))
	reveal.AddTxOut(wire.NewTxOut(330, ours))
	commit.NextTxs = []*wire.MsgTx{predecessor, reveal}
	facts := make(map[string]*TxOutput)
	for i := range commit.CommitTx.TxOut {
		output := indexer.GenerateTxOutput(commit.CommitTx, i)
		if i == 0 {
			output.Assets = indexer.TxAssets{{Name: atomicalsTestName().AssetName, Amount: *indexer.NewDefaultDecimal(1000), BindingSat: 1}}
		}
		facts[output.OutPointStr] = output
	}
	transfer := indexer.GenerateTxOutput(reveal, 0)
	facts[transfer.OutPointStr] = transfer
	script, err := GetP2TRpkScript(manager.wallet.GetPaymentPubKey())
	if err != nil {
		t.Fatal(err)
	}
	return manager, commit, facts, []int{0, 1, 2}, script
}

func assertMixedRecoveryColoring(t *testing.T, manager *Manager, commit *ChannelCommitment, tx *wire.MsgTx, facts map[string]*TxOutput) {
	t.Helper()
	if tx == nil || len(tx.TxIn) != 4 || len(tx.TxOut) < 3 {
		t.Fatalf("incomplete mixed recovery transaction: %+v", tx)
	}
	if tx.TxIn[0].PreviousOutPoint.Hash != commit.CommitTx.TxHash() || tx.TxIn[0].PreviousOutPoint.Index != 0 ||
		tx.TxIn[1].PreviousOutPoint.Index != 1 || tx.TxOut[0].Value != 1000 || tx.TxOut[1].Value != 330 || tx.TxOut[2].Value != 330 {
		t.Fatal("recovery consumed or reordered ARC/BRC carriers as fee inputs")
	}
	var inputs, outputs []*TxOutput
	for _, in := range tx.TxIn {
		fact := facts[in.PreviousOutPoint.String()]
		if fact == nil {
			t.Fatalf("missing recovery fact %s", in.PreviousOutPoint)
		}
		inputs = append(inputs, fact)
	}
	for index := range tx.TxOut {
		outputs = append(outputs, indexer.GenerateTxOutput(tx, index))
	}
	if err := manager.ClassifySTPAtomicalsOutputs(tx, inputs, outputs); err != nil {
		t.Fatalf("native regular coloring rejected mixed recovery: %v", err)
	}
	if outputs[0].GetAsset(&atomicalsTestName().AssetName).Int64() != 1000 {
		t.Fatal("ARC20 recovery changed the native quantity or recipient")
	}
	for _, output := range outputs[1:] {
		if output.GetAsset(&atomicalsTestName().AssetName).Sign() != 0 {
			t.Fatal("ARC20 colored the BRC transfer or plain fee change")
		}
	}
}

func TestMixedARCAndBRCRecoveryPreservesNativeCarriers(t *testing.T) {
	for _, branch := range []string{"sweep", "punish"} {
		t.Run(branch, func(t *testing.T) {
			manager, commit, facts, selected, script := mixedRecoveryFixture(t, 10_000)
			var tx *wire.MsgTx
			var err error
			if branch == "sweep" {
				tx, _, _, err = manager.CreateSweepTx(commit, selected, script, SCRIPT_TYPE_SWEEP, 4, 900_000, []byte{txscript.OP_TRUE}, 1)
			} else {
				key, _ := btcec.PrivKeyFromBytes([]byte{1})
				tx, _, err = CreatePunishmentTx(commit, key, manager.wallet.GetPaymentPubKey(), selected, []byte{txscript.OP_TRUE}, 1)
			}
			if err != nil {
				t.Fatal(err)
			}
			assertMixedRecoveryColoring(t, manager, commit, tx, facts)
		})
	}
}

func TestMixedPunishmentCannotSpendCarriersToCoverMissingFees(t *testing.T) {
	manager, commit, _, selected, _ := mixedRecoveryFixture(t, 1)
	key, _ := btcec.PrivKeyFromBytes([]byte{1})
	if _, _, err := CreatePunishmentTx(commit, key, manager.wallet.GetPaymentPubKey(), selected, []byte{txscript.OP_TRUE}, 1); err == nil {
		t.Fatal("fee-short punishment silently consumed ARC/BRC asset carrier values")
	}
}
