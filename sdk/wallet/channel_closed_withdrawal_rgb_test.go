package wallet

import (
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// These are structural boundary controls, not a replacement for real RGB
// consensus/burn validation or the cold-restart lifecycle integration test.
func rgbClosedWithdrawalBoundaryFixture(t *testing.T) (*Channel, *wire.MsgTx) {
	t.Helper()
	manager, channel := newSignedChannelSyncState(t, &channelHeartbeatTestClient{})
	channel.localWallet = manager.wallet.Clone()
	channel.IsInitiator = true
	channel.AssetEntropy = [32]byte{1}
	parent := channel.RemoteCommitment.CommitTx.Copy()
	child := wire.NewMsgTx(2)
	point := wire.OutPoint{Hash: parent.TxHash(), Index: 0}
	child.AddTxIn(wire.NewTxIn(&point, nil, nil))
	child.AddTxOut(wire.NewTxOut(1, []byte{0x51}))
	ctx := rgb11wallet.ChannelContext{ChannelID: channel.ChannelId, Operation: string(STPOpCommit),
		Branch: "server", Height: uint64(channel.CommitHeight), Entropy: channel.AssetEntropy, BitcoinNetwork: GetChainParam().Name}
	pkg := &rgb11wallet.ChannelPackage{Version: 1, Funding: []rgb11wallet.ChannelFunding{{Amount: *indexer.NewDecimalWithScale(1, 0)}}, Steps: []rgb11wallet.ChannelStep{{Context: ctx}}}
	mainProof, err := rgb11wallet.EncodeChannelPackage(pkg)
	if err != nil {
		t.Fatal(err)
	}
	ctx.Operation, ctx.Branch = string(STPOpForceClose), rgbClientRecoveryBranch
	pkg.Steps = append(pkg.Steps, rgb11wallet.ChannelStep{Context: ctx})
	childProof, err := rgb11wallet.EncodeChannelPackage(pkg)
	if err != nil {
		t.Fatal(err)
	}
	channel.AssetFunding = map[string][]byte{rgb11wallet.Protocol: {1}}
	channel.ClosingAssetProofs = nil
	channel.LocalCommitment.AssetProofs = nil
	channel.RemoteCommitment.CommitTx = parent.Copy()
	channel.RemoteCommitment.AssetProofs = map[string][]byte{rgb11wallet.Protocol: mainProof}
	channel.RemoteCommitment.RecoveryProofs = nil
	channel.RemoteCommitment.Others, channel.RemoteCommitment.NextTxs = nil, nil
	channel.RemoteCommitment.DeAnchorTx = swire.NewMsgTx(2)
	channel.RemoteCommitment.RGBClientRecovery = &CommitmentTx{CommitTx: child,
		AssetProofs: map[string][]byte{rgb11wallet.Protocol: childProof}}
	return channel, parent
}

func TestRGBClosedWithdrawalStructuralFixtureIsReadOnly(t *testing.T) {
	channel, parent := rgbClosedWithdrawalBoundaryFixture(t)
	if _, _, err := rgbClosedWithdrawalPackage(channel, parent); err != nil {
		t.Fatalf("structural fixture is not reaching the intended boundary: %v", err)
	}
	// This fixture deliberately has no actual RGB proof or full child
	// signatures. Full preflight is exercised by the real lifecycle tests.
	if len(channel.manager.GetClosingReservations()) != 0 || len(channel.manager.GetLocalActionReservations()) != 0 {
		t.Fatal("structural inspection persisted a withdrawal")
	}
}

func TestRGBClosedWithdrawalPackageRejectsMissingOrForeignMaterial(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Channel, *wire.MsgTx)
	}{
		{"missing_child", func(c *Channel, _ *wire.MsgTx) { c.RemoteCommitment.RGBClientRecovery = nil }},
		{"missing_child_proof", func(c *Channel, _ *wire.MsgTx) { c.RemoteCommitment.RGBClientRecovery.AssetProofs = nil }},
		{"missing_main_proof", func(c *Channel, _ *wire.MsgTx) { c.RemoteCommitment.AssetProofs = nil }},
		{"missing_l2_burn", func(c *Channel, _ *wire.MsgTx) { c.RemoteCommitment.DeAnchorTx = nil }},
		{"foreign_parent", func(_ *Channel, p *wire.MsgTx) { p.LockTime++ }},
		{"foreign_account", func(c *Channel, _ *wire.MsgTx) { c.LocalChanCfg.WalletId++ }},
		{"unknown_mixed_proof", func(c *Channel, _ *wire.MsgTx) { c.RemoteCommitment.AssetProofs["unknown"] = []byte{1} }},
		{"unknown_mixed_funding", func(c *Channel, _ *wire.MsgTx) { c.AssetFunding["unknown"] = []byte{1} }},
		{"other_branch", func(c *Channel, _ *wire.MsgTx) {
			c.RemoteCommitment.Others = []*CommitmentTx{{CommitTx: wire.NewMsgTx(2)}}
		}},
		{"nil_input", func(c *Channel, _ *wire.MsgTx) { c.RemoteCommitment.RGBClientRecovery.CommitTx.TxIn[0] = nil }},
		{"foreign_input", func(c *Channel, _ *wire.MsgTx) {
			c.RemoteCommitment.RGBClientRecovery.CommitTx.TxIn[0].PreviousOutPoint.Hash[0] ^= 1
		}},
		{"missing_dependency", func(c *Channel, _ *wire.MsgTx) {
			c.RemoteCommitment.RGBClientRecovery.CommitTx.TxIn[0].PreviousOutPoint.Index = 100000
		}},
		{"expanded_child", func(c *Channel, _ *wire.MsgTx) {
			c.RemoteCommitment.RGBClientRecovery.NextTxs = []*wire.MsgTx{wire.NewMsgTx(2)}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			channel, parent := rgbClosedWithdrawalBoundaryFixture(t)
			test.mutate(channel, parent)
			if _, _, err := rgbClosedWithdrawalPackage(channel, parent); err == nil {
				t.Fatal("incomplete or foreign RGB package passed structural preparation")
			}
		})
	}
}

func TestRGBClosedWithdrawalNeverFallsBackToOrdinaryOther(t *testing.T) {
	manager, current, _, client := closedWithdrawalBoundsFixture(t, false)
	current.AssetFunding = map[string][]byte{rgb11wallet.Protocol: {1}}
	current.RemoteCommitment.RGBClientRecovery = nil
	if err := manager.SaveChannelToDB(current); err != nil {
		t.Fatal(err)
	}
	if id, err := manager.WithdrawClosedChannel(current.ChannelId); err == nil {
		t.Fatalf("missing RGB material fell back to a legacy success: %d", id)
	}
	if client.broadcasts != 0 || len(manager.GetClosingReservations()) != 0 || len(manager.GetLocalActionReservations()) != 0 {
		t.Fatal("rejected RGB package broadcast or persisted a withdrawal")
	}
	if manager.GetChannel(current.ChannelId) != current {
		t.Fatal("rejected RGB package retired the ordinary cached channel")
	}
}

func TestRGBClosedWithdrawalRejectsForeignProofContextAndPrefix(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*rgb11wallet.ChannelPackage)
	}{
		{"channel", func(pkg *rgb11wallet.ChannelPackage) { pkg.Steps[1].Context.ChannelID = "foreign" }},
		{"entropy", func(pkg *rgb11wallet.ChannelPackage) { pkg.Steps[1].Context.Entropy[0] ^= 1 }},
		{"branch", func(pkg *rgb11wallet.ChannelPackage) { pkg.Steps[1].Context.Branch = "other-sweep" }},
		{"operation", func(pkg *rgb11wallet.ChannelPackage) { pkg.Steps[1].Context.Operation = string(STPOpPunish) }},
		{"main_prefix", func(pkg *rgb11wallet.ChannelPackage) { pkg.Steps[0].Context.Branch = "client" }},
		{"funding_root", func(pkg *rgb11wallet.ChannelPackage) { pkg.Funding[0].Value++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			channel, parent := rgbClosedWithdrawalBoundaryFixture(t)
			recovery := channel.RemoteCommitment.RGBClientRecovery
			pkg, err := rgb11wallet.DecodeChannelPackage(recovery.AssetProofs[rgb11wallet.Protocol])
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(pkg)
			raw, err := rgb11wallet.EncodeChannelPackage(pkg)
			if err != nil {
				t.Fatal(err)
			}
			recovery.AssetProofs[rgb11wallet.Protocol] = raw
			if _, _, err := rgbClosedWithdrawalPackage(channel, parent); err == nil {
				t.Fatal("foreign RGB proof passed structural preparation")
			}
		})
	}
}
