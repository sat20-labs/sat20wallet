package wallet

import (
	"testing"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

func recoveryFeeSelectionFixture() (*ChannelCommitment, []*TxOutput, []int) {
	commit := NewChannelCommitment()
	commit.CommitTx = wire.NewMsgTx(2)
	commit.CommitTx.AddTxOut(wire.NewTxOut(0, []byte{txscript.OP_RETURN}))
	for _, value := range []int64{330, 700, 9_000, 2_000, 8_000} {
		commit.CommitTx.AddTxOut(wire.NewTxOut(value, []byte{txscript.OP_TRUE}))
	}
	views := make([]*TxOutput, len(commit.CommitTx.TxOut))
	for i := range views {
		views[i] = indexer.GenerateTxOutput(commit.CommitTx, i)
	}
	views[1].Assets = indexer.TxAssets{{
		Name:   indexer.AssetName{Protocol: "rgb11", Type: indexer.ASSET_TYPE_FT, Ticker: "native-recovery-test"},
		Amount: *indexer.NewDecimal(1250, 2),
	}}
	views[2].Assets = indexer.TxAssets{{
		Name:       indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ATOM, Type: indexer.ASSET_TYPE_FT, Ticker: "quark"},
		Amount:     *indexer.NewDefaultDecimal(700),
		BindingSat: 1,
	}}
	commit.AssetProofs = map[string][]byte{"rgb11": {1}}
	commit.RecoveryFeeIndexes = []uint32{3, 5}
	// A BRC20 follow-up owns output 4. Its apparent lack of indexer assets
	// must not make it eligible to pay the independent recovery transaction.
	next := wire.NewMsgTx(2)
	hash := commit.CommitTx.TxHash()
	next.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&hash, 4), nil, nil))
	commit.NextTxs = []*wire.MsgTx{next}
	return commit, views, []int{1, 2, 3}
}

func TestCommitmentRecoveryFeeSelectionUsesOnlyExplicitPlainReserves(t *testing.T) {
	commit, views, selected := recoveryFeeSelectionFixture()
	fees, err := commitmentRecoveryFeeIndexes(commit, selected, views)
	if err != nil || len(fees) != 1 || !fees[3] {
		t.Fatalf("expected branch reserve 3, got %v: %v", fees, err)
	}
	// The other branch's reserve is valid but cannot be consumed by this
	// branch, even though its index is last in the whole transaction.
	if fees[5] || fees[1] || fees[2] || fees[4] {
		t.Fatal("recovery selected a native carrier, follow-up reserve, or other branch's reserve")
	}
}

func TestCommitmentRecoveryFeeSelectionRejectsAssetAndConflictingReserves(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ChannelCommitment, []*TxOutput, *[]int)
	}{
		{"rgb_carrier", func(c *ChannelCommitment, _ []*TxOutput, _ *[]int) { c.RecoveryFeeIndexes = []uint32{1} }},
		{"atomicals_carrier", func(c *ChannelCommitment, _ []*TxOutput, _ *[]int) { c.RecoveryFeeIndexes = []uint32{2} }},
		{"duplicate_reserve", func(c *ChannelCommitment, _ []*TxOutput, _ *[]int) { c.RecoveryFeeIndexes = []uint32{3, 3} }},
		{"consumed_brc_reserve", func(c *ChannelCommitment, _ []*TxOutput, _ *[]int) { c.RecoveryFeeIndexes = []uint32{4} }},
		{"op_return", func(c *ChannelCommitment, _ []*TxOutput, _ *[]int) { c.RecoveryFeeIndexes = []uint32{0} }},
		{"missing_output", func(c *ChannelCommitment, _ []*TxOutput, _ *[]int) { c.RecoveryFeeIndexes = []uint32{99} }},
		{"missing_native_markers", func(c *ChannelCommitment, _ []*TxOutput, _ *[]int) { c.RecoveryFeeIndexes = nil }},
		{"changed_verified_value", func(_ *ChannelCommitment, v []*TxOutput, _ *[]int) { v[3].OutValue.Value++ }},
		{"changed_verified_script", func(_ *ChannelCommitment, v []*TxOutput, _ *[]int) {
			v[3].OutValue.PkScript = []byte{txscript.OP_FALSE}
		}},
		{"missing_verified_output", func(_ *ChannelCommitment, v []*TxOutput, _ *[]int) { v[3] = nil }},
		{"reordered_selection", func(_ *ChannelCommitment, _ []*TxOutput, s *[]int) { *s = []int{3, 1, 2} }},
		{"duplicate_selection", func(_ *ChannelCommitment, _ []*TxOutput, s *[]int) { *s = []int{1, 2, 3, 3} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commit, views, selected := recoveryFeeSelectionFixture()
			test.mutate(commit, views, &selected)
			if _, err := commitmentRecoveryFeeIndexes(commit, selected, views); err == nil {
				t.Fatal("unsafe recovery fee selection was accepted")
			}
		})
	}
}
