package rgb11wallet

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/seals"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
	"github.com/stretchr/testify/require"
)

type channelPackageFacts struct {
	transactions map[string]*wire.MsgTx
	spent        map[string]string
}

func (p *channelPackageFacts) GetRawTx(id string) ([]byte, error) {
	tx := p.transactions[id]
	if tx == nil {
		return nil, fmt.Errorf("unknown transaction")
	}
	var raw bytes.Buffer
	err := tx.Serialize(&raw)
	return raw.Bytes(), err
}
func (p *channelPackageFacts) GetTxStatus(id string) (*BitcoinTxStatus, error) {
	if p.transactions[id] == nil {
		return &BitcoinTxStatus{TxID: id}, nil
	}
	return &BitcoinTxStatus{TxID: id, Confirmed: true, BlockHeight: 10, BlockHash: strings.Repeat("1", 64), Confirmations: 2}, nil
}
func (p *channelPackageFacts) GetOutspend(point string) (*BitcoinOutspend, error) {
	return &BitcoinOutspend{Spent: p.spent[point] != "", SpendingTx: p.spent[point]}, nil
}
func (p *channelPackageFacts) GetUTXO(point string) (*BitcoinUTXO, error) {
	out, err := wire.NewOutPointFromString(point)
	if err != nil {
		return nil, err
	}
	tx := p.transactions[out.Hash.String()]
	if tx == nil || uint64(out.Index) >= uint64(len(tx.TxOut)) || p.spent[point] != "" {
		return nil, nil
	}
	return &BitcoinUTXO{OutPoint: point, Value: tx.TxOut[out.Index].Value, PkScript: tx.TxOut[out.Index].PkScript, Confirmations: 2}, nil
}
func (*channelPackageFacts) GetTip() (*BitcoinTip, error) {
	return &BitcoinTip{Height: 11, BlockHash: strings.Repeat("1", 64)}, nil
}
func (*channelPackageFacts) Broadcast([]byte) (string, error) {
	return "", fmt.Errorf("construction cannot broadcast")
}

func issuedChannelFunding(t *testing.T) ([]ChannelFunding, *channelPackageFacts) {
	t.Helper()
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: 42}, nil, nil))
	script := append([]byte{0, 32}, bytes.Repeat([]byte{2}, 32)...)
	tx.AddTxOut(wire.NewTxOut(1000, script))
	tx.AddTxOut(wire.NewTxOut(1000, script))
	hash := tx.TxHash()
	var allocations []issuance.Allocation
	for i, amount := range []uint64{1000, 2000, 4000} {
		seal, err := seals.NewGraphBlindSeal(hash[:], uint32(i/2), uint64(i+21))
		require.NoError(t, err)
		allocations = append(allocations, issuance.Allocation{Seal: seal, Amount: amount})
	}
	issued, err := issuance.Issue(issuance.Spec{Kind: schemas.NIA, Network: issuance.BitcoinRegtest, Ticker: "STP", Name: "Channel package", Precision: 2, Allocations: allocations, Timestamp: 1_700_000_001})
	require.NoError(t, err)
	proof, err := nativeproof.BuildRGB11([]byte(issued.Armor), nil)
	require.NoError(t, err)
	decoded, err := nativeproof.Decode(proof)
	require.NoError(t, err)
	asset, err := NewContractAssetKey(issued.ContractID, indexer.ASSET_TYPE_FT)
	require.NoError(t, err)
	var sources []ChannelFunding
	for i, amount := range []int64{3000, 4000} {
		sources = append(sources, ChannelFunding{OutPoint: fmt.Sprintf("%s:%d", tx.TxID(), i), Value: 1000, PkScript: script, AssetName: asset, Amount: *indexer.NewDecimalWithScale(amount, 2), Consignment: decoded.Consignment})
	}
	return sources, &channelPackageFacts{transactions: map[string]*wire.MsgTx{tx.TxID(): tx}, spent: make(map[string]string)}
}

func TestSTPChannelPackageRealDeterministicTransitions(t *testing.T) {
	sources, facts := issuedChannelFunding(t)
	tx := wire.NewMsgTx(2)
	for _, source := range sources {
		point, err := wire.NewOutPointFromString(source.OutPoint)
		require.NoError(t, err)
		tx.AddTxIn(wire.NewTxIn(point, nil, nil))
	}
	_, err := ReserveChannelDBC(tx, 0)
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(700, sources[0].PkScript))
	tx.AddTxOut(wire.NewTxOut(800, sources[0].PkScript))
	assignments := []ChannelAssignment{{OutputIndex: 1, Amount: *indexer.NewDecimalWithScale(2500, 2)}, {OutputIndex: 2, Amount: *indexer.NewDecimalWithScale(4500, 2)}}
	ctx := ChannelContext{ChannelID: "channel", Operation: "commit", Branch: "client", Height: 2, Entropy: [32]byte{1, 2, 3}, BitcoinNetwork: "regtest"}
	step, err := BuildChannelStep(context.Background(), sources, tx, 0, assignments, ctx, facts)
	require.NoError(t, err)
	other, err := BuildChannelStep(context.Background(), sources, tx.Copy(), 0, assignments, ctx, facts)
	require.NoError(t, err)
	require.Equal(t, step, other, "both peers must produce byte-identical actual transitions")
	pkg := &ChannelPackage{Version: 1, Funding: sources, Steps: []ChannelStep{*step}}
	report, err := ValidateChannelPackage(context.Background(), pkg, tx, facts)
	require.NoError(t, err)
	require.Len(t, report.TerminalAllocations, 2)
	encoded, err := EncodeChannelPackage(pkg)
	require.NoError(t, err)
	decoded, err := DecodeChannelPackage(encoded)
	require.NoError(t, err)
	require.Equal(t, pkg, decoded)
	// Two allocations on the first source and one on its sibling are all
	// consumed by real RGB Opouts, not a synthetic one-state-per-carrier map.
	childFunding, err := ChannelFundingFromStep(*step, 1, assignments[0].Amount)
	require.NoError(t, err)
	child := wire.NewMsgTx(2)
	point, err := wire.NewOutPointFromString(childFunding.OutPoint)
	require.NoError(t, err)
	child.AddTxIn(wire.NewTxIn(point, nil, nil))
	_, err = ReserveChannelDBC(child, 0)
	require.NoError(t, err)
	child.AddTxOut(wire.NewTxOut(500, sources[0].PkScript))
	childContext := ctx
	childContext.Operation = "force-close"
	childContext.Branch = "sweep"
	childStep, err := BuildChannelStep(context.Background(), []ChannelFunding{childFunding}, child, 0, []ChannelAssignment{{OutputIndex: 1, Amount: assignments[0].Amount}}, childContext, facts)
	require.NoError(t, err)
	pkg.Steps = append(pkg.Steps, *childStep)
	_, err = ValidateChannelPackage(context.Background(), pkg, child, facts)
	require.NoError(t, err)
	// Changing a valid but unrelated root cannot authorize a descendant.
	pkg.Funding[0].Value++
	_, err = ValidateChannelPackage(context.Background(), pkg, child, facts)
	require.ErrorContains(t, err, "authenticated inventory")
}

func TestSTPChannelPackageRejectsTamperingAndPrematureAuthority(t *testing.T) {
	sources, facts := issuedChannelFunding(t)
	tx := wire.NewMsgTx(2)
	for _, source := range sources {
		point, _ := wire.NewOutPointFromString(source.OutPoint)
		tx.AddTxIn(wire.NewTxIn(point, nil, nil))
	}
	_, err := ReserveChannelDBC(tx, 0)
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(1500, sources[0].PkScript))
	ctx := ChannelContext{ChannelID: "channel", Operation: "splice-in", Branch: "funding", Height: 1, Entropy: [32]byte{6}, BitcoinNetwork: "regtest"}
	assignments := []ChannelAssignment{{OutputIndex: 1, Amount: *indexer.NewDecimalWithScale(7000, 2)}}
	step, err := BuildChannelStep(context.Background(), sources, tx, 0, assignments, ctx, facts)
	require.NoError(t, err)
	pkg := &ChannelPackage{Version: 1, Funding: sources, Steps: []ChannelStep{*step}}
	funding, err := ChannelFundingFromStep(*step, 1, assignments[0].Amount)
	require.NoError(t, err)
	_, err = ValidateChannelFunding(context.Background(), funding, facts, nativeproof.Prepared, "regtest")
	require.NoError(t, err)
	_, err = ValidateChannelFunding(context.Background(), funding, facts, nativeproof.Current, "regtest")
	require.Error(t, err)
	bad := tx.Copy()
	bad.TxOut[1].Value--
	_, err = ValidateChannelPackage(context.Background(), pkg, bad, facts)
	require.ErrorContains(t, err, "another Bitcoin transaction")
	pkg.Steps[0].Context.Branch = "server"
	_, err = ValidateChannelPackage(context.Background(), pkg, tx, facts)
	require.Error(t, err)
	// Once independently observed, normal current validation can authorize
	// its exact output; spent unrelated roots do not erase valid history.
	facts.transactions[tx.TxID()] = tx
	for _, source := range sources {
		facts.spent[source.OutPoint] = tx.TxID()
	}
	_, err = ValidateChannelFunding(context.Background(), funding, facts, nativeproof.Current, "regtest")
	require.NoError(t, err)
}

func TestSTPChannelPackagePreservesDisclosedSiblingHistory(t *testing.T) {
	sources, facts := issuedChannelFunding(t)
	tx := wire.NewMsgTx(2)
	for _, source := range sources {
		point, err := wire.NewOutPointFromString(source.OutPoint)
		require.NoError(t, err)
		tx.AddTxIn(wire.NewTxIn(point, nil, nil))
	}
	_, err := ReserveChannelDBC(tx, 0)
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(700, sources[0].PkScript))
	tx.AddTxOut(wire.NewTxOut(800, sources[0].PkScript))
	ctx := ChannelContext{ChannelID: "public-history", Operation: "splice-out", Branch: "funding", Height: 2,
		Entropy: [32]byte{17, 23}, BitcoinNetwork: "regtest"}
	assignments := []ChannelAssignment{{OutputIndex: 1, Amount: *indexer.NewDecimalWithScale(2500, 2)},
		{OutputIndex: 2, Amount: *indexer.NewDecimalWithScale(4500, 2)}}
	step, err := BuildChannelStep(context.Background(), sources, tx, 0, assignments, ctx, facts)
	require.NoError(t, err)
	facts.transactions[tx.TxID()] = tx
	for _, source := range sources {
		facts.spent[source.OutPoint] = tx.TxID()
	}
	material, err := PublicChannelConsignment(step.Consignment, step.Reveals)
	require.NoError(t, err)
	again, err := PublicChannelConsignment(material, step.Reveals)
	require.NoError(t, err)
	require.Equal(t, material, again, "public material must normalize deterministically")

	// An ordinary wallet owns only output 1. Its durable projection and later
	// funding request must not claim the sibling, but they must retain the
	// already public disclosure that proves the earlier full channel return.
	owned, err := ChannelFundingFromStep(*step, 1, assignments[0].Amount)
	require.NoError(t, err)
	owned.Consignment, owned.Reveals = material, nil
	for _, raw := range step.Reveals {
		seal, err := seals.DecodeGraphBlindSeal(raw)
		require.NoError(t, err)
		if seal.Vout == 1 && (seal.TxID == nil || *seal.TxID == [32]byte(tx.TxHash())) {
			owned.Reveals = append(owned.Reveals, raw)
		}
	}
	require.Len(t, owned.Reveals, 1)
	child := wire.NewMsgTx(2)
	point, err := wire.NewOutPointFromString(owned.OutPoint)
	require.NoError(t, err)
	child.AddTxIn(wire.NewTxIn(point, nil, nil))
	_, err = ReserveChannelDBC(child, 0)
	require.NoError(t, err)
	child.AddTxOut(wire.NewTxOut(500, sources[0].PkScript))
	ctx.Operation, ctx.Height = "splice-in", 3
	childStep, err := BuildChannelStep(context.Background(), []ChannelFunding{owned}, child, 0,
		[]ChannelAssignment{{OutputIndex: 1, Amount: assignments[0].Amount}}, ctx, facts)
	require.NoError(t, err)
	funding, err := ChannelFundingFromStep(*childStep, 1, assignments[0].Amount)
	require.NoError(t, err)
	validated, err := ValidateChannelFunding(context.Background(), funding, facts, nativeproof.Prepared, "regtest")
	require.NoError(t, err)
	var historicalTotal uint64
	for _, state := range validated.History.HistoricalStates {
		if state.Outpoint.TxID == tx.TxHash() && state.Reference.AssignmentType == 4000 {
			historicalTotal += state.State.Amount
		}
	}
	require.Equal(t, uint64(7000), historicalTotal, "a child proof must preserve both disclosed outputs of its historical parent")
	require.Equal(t, int64(2500), validated.Asset.Amount.Value.Int64(), "the child still owns only its exact allocation")
}
