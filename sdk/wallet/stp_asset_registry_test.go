package wallet

import (
	"fmt"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

func TestSTPAssetRegistryRejectsInvalidRegistration(t *testing.T) {
	for _, protocols := range [][]STPAssetProtocol{
		{nil},
		{btcSTPProtocol{}, btcSTPProtocol{}},
		{ordxSTPProtocol{}, runesSTPProtocol{}, ordxSTPProtocol{}},
	} {
		if _, err := NewSTPAssetRegistry(protocols...); err == nil {
			t.Fatal("invalid asset registration was accepted")
		}
	}
}

func TestSTPAssetRegistryRejectsUnsupportedCapabilities(t *testing.T) {
	registry := newBuiltinSTPAssetRegistry()
	unknown := indexer.AssetName{Protocol: "unregistered", Type: indexer.ASSET_TYPE_FT, Ticker: "token"}
	if _, err := registry.Resolve(&unknown, STPOpSpliceIn); err == nil {
		t.Fatal("an unregistered asset entered STP")
	}
	if _, err := registry.Resolve(nil, STPOpCommit); err == nil {
		t.Fatal("a missing asset entered STP")
	}
	rgb := indexer.AssetName{Protocol: "rgb11", Type: indexer.ASSET_TYPE_FT, Ticker: "contract-id"}
	if _, err := registry.Resolve(&rgb, STPOpSpliceIn); err == nil {
		t.Fatal("a noncanonical RGB contract ID entered STP")
	}
	rgb.Ticker = strings.Repeat("ab", 32)
	if _, err := registry.Resolve(&rgb, STPOpSpliceIn); err != nil {
		t.Fatalf("canonical RGB FT must resolve to its proof-verifying adapter: %v", err)
	}
	rgb.Type = indexer.ASSET_TYPE_NFT
	if _, err := registry.Resolve(&rgb, STPOpSpliceIn); err == nil {
		t.Fatal("an unsupported RGB asset schema entered STP")
	}
	rune := indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_RUNES, Type: indexer.ASSET_TYPE_FT, Ticker: "TEST•RUNE"}
	if _, err := registry.Resolve(&rune, STPAssetOperation("unimplemented")); err == nil {
		t.Fatal("an unimplemented operation was accepted")
	}
	rune.Type = indexer.ASSET_TYPE_NFT
	if _, err := registry.Resolve(&rune, STPOpSpliceIn); err == nil {
		t.Fatal("an unsupported asset class was accepted")
	}
}

func newSTPAssetLayoutTestPlan(fixedIndex bool) (*wire.MsgTx, *STPAssetPlan) {
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: 0}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(1000, []byte{0x51}))
	beforeInputs := append([]*wire.TxIn(nil), tx.TxIn...)
	beforeOutputs := append([]*wire.TxOut(nil), tx.TxOut...)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: 1}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(330, []byte{0x51, 0x51}))
	plan := &STPAssetPlan{Tx: tx}
	snapshotSTPAssetLayout(tx, beforeInputs, beforeOutputs, fixedIndex, plan)
	return tx, plan
}

func TestSTPAssetLayoutRejectsMutationsBeforeSigning(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*wire.MsgTx)
	}{
		{"asset-value", func(tx *wire.MsgTx) { tx.TxOut[1].Value-- }},
		{"asset-script", func(tx *wire.MsgTx) { tx.TxOut[1].PkScript[0] = 0x52 }},
		{"removed-asset-output", func(tx *wire.MsgTx) { tx.TxOut = tx.TxOut[:1] }},
		{"removed-asset-input", func(tx *wire.MsgTx) { tx.TxIn = tx.TxIn[:1] }},
		{"changed-asset-input", func(tx *wire.MsgTx) { tx.TxIn[1].PreviousOutPoint.Index = 2 }},
		{"duplicate-input", func(tx *wire.MsgTx) {
			outpoint := tx.TxIn[0].PreviousOutPoint
			tx.AddTxIn(wire.NewTxIn(&outpoint, nil, nil))
		}},
		{"negative-untracked-output", func(tx *wire.MsgTx) { tx.TxOut[0].Value = -1 }},
		{"nil-input", func(tx *wire.MsgTx) { tx.TxIn = append(tx.TxIn, nil) }},
		{"fixed-output-index", func(tx *wire.MsgTx) {
			tx.TxOut = append([]*wire.TxOut{wire.NewTxOut(330, []byte{0x51})}, tx.TxOut...)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx, plan := newSTPAssetLayoutTestPlan((runesSTPProtocol{}).Policy(nil).FixedOutputIndexes)
			request := STPAssetVerification{Operation: STPOpCommit, Tx: tx, Plan: plan}
			if err := verifySTPAssetLayout(request); err != nil {
				t.Fatalf("valid initial plan rejected: %v", err)
			}
			test.mutate(tx)
			if err := verifySTPAssetLayout(request); err == nil {
				t.Fatal("unsafe final transaction layout was accepted")
			}
		})
	}
}

func TestSTPAssetLayoutAllowsCarrierRelocationAndPlainFeeAdjustment(t *testing.T) {
	for _, protocol := range []STPAssetProtocol{ordxSTPProtocol{}, brc20STPProtocol{}} {
		t.Run(protocol.Protocol(), func(t *testing.T) {
			tx, plan := newSTPAssetLayoutTestPlan(protocol.Policy(nil).FixedOutputIndexes)
			// Runes inserts its carrier before earlier protocols' asset outputs.
			// STP can also adjust the plain fee output, which is outside this plan.
			tx.TxOut[0].Value -= 200
			tx.TxOut = append([]*wire.TxOut{wire.NewTxOut(330, []byte{0x51})}, tx.TxOut...)
			if err := protocol.Verify(STPAssetVerification{Operation: STPOpCommit, Tx: tx, Plan: plan}); err != nil {
				t.Fatalf("safe final layout was rejected: %v", err)
			}
		})
	}
}

func TestSTPBRC20RecoveryRequiresPreparedTransfers(t *testing.T) {
	name := AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_BRC20, Type: indexer.ASSET_TYPE_FT, Ticker: "ordi"}}
	balances := map[AssetName]*Decimal{name: indexer.NewDefaultDecimal(100)}
	predecessor := wire.NewMsgTx(2)
	predecessor.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: 0}, nil, nil))
	predecessor.AddTxOut(wire.NewTxOut(1000, []byte{0x51}))
	reveal := wire.NewMsgTx(2)
	predecessorHash := predecessor.TxHash()
	reveal.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&predecessorHash, 0), nil, nil))
	reveal.AddTxOut(wire.NewTxOut(330, []byte{0x51}))
	protocol := brc20STPProtocol{}
	for _, commit := range []*ChannelCommitment{
		nil,
		{},
		{ChannelCommitmentV1: ChannelCommitmentV1{NextTxs: []*wire.MsgTx{predecessor}}},
		{ChannelCommitmentV1: ChannelCommitmentV1{NextTxs: []*wire.MsgTx{predecessor, nil}}},
		{ChannelCommitmentV1: ChannelCommitmentV1{NextTxs: []*wire.MsgTx{predecessor, wire.NewMsgTx(2)}}},
	} {
		if _, err := protocol.RecoveryInputs(commit, balances); err == nil {
			t.Fatal("BRC20 recovery accepted missing transfer material")
		}
	}
	inputs, err := protocol.RecoveryInputs(&ChannelCommitment{ChannelCommitmentV1: ChannelCommitmentV1{NextTxs: []*wire.MsgTx{predecessor, reveal}}}, balances)
	if err != nil {
		t.Fatal(err)
	}
	if inputs.LeadingPlainOutputs != 1 || len(inputs.Transfers) != 1 {
		t.Fatalf("unexpected recovery input allocation: %+v", inputs)
	}
	wantOutpoint := fmt.Sprintf("%s:0", reveal.TxID())
	if inputs.Transfers[0].OutPointStr != wantOutpoint || inputs.Transfers[0].Value() != 330 {
		t.Fatalf("recovery must spend the reveal output, got %+v", inputs.Transfers[0])
	}
}

func TestSTPAssetInputVerificationPreservesUnrelatedAssets(t *testing.T) {
	for _, protocol := range []STPAssetProtocol{ordxSTPProtocol{}, runesSTPProtocol{}, brc20STPProtocol{}} {
		t.Run(protocol.Protocol(), func(t *testing.T) {
			name := AssetName{AssetName: indexer.AssetName{Protocol: protocol.Protocol(), Type: indexer.ASSET_TYPE_FT, Ticker: "main"}}
			input := &TxOutput{OutPointStr: "funding:0", OutValue: *wire.NewTxOut(660, []byte{0x51}), Assets: indexer.TxAssets{
				{Name: name.AssetName, Amount: *indexer.NewDefaultDecimal(10)},
			}}
			if err := protocol.VerifyInput(&name, input); err != nil {
				t.Fatalf("single-asset input rejected: %v", err)
			}
			other := name.AssetName
			other.Ticker = "other"
			input.Assets = append(input.Assets, indexer.AssetInfo{Name: other, Amount: *indexer.NewDefaultDecimal(20)})
			if err := protocol.VerifyInput(&name, input); err == nil {
				t.Fatal("unrelated asset would be silently discarded")
			}
			if err := (btcSTPProtocol{}).VerifyInput(&PLAIN_ASSET, input); err == nil {
				t.Fatal("asset-bearing UTXO was accepted as plain bitcoin")
			}
		})
	}
}
