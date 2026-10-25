package wallet

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
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
	"github.com/stretchr/testify/require"
)

// These are Bitcoin observations, not a replacement for RGB validation. Every
// RGB proof below is issued, constructed and validated by the native code.
type rgb11RecoverySelectionFacts struct {
	transactions map[string]*wire.MsgTx
	spent        map[string]string
}

var _ rgb11wallet.BitcoinEvidenceProvider = (*rgb11RecoverySelectionFacts)(nil)

func (f *rgb11RecoverySelectionFacts) GetRawTx(id string) ([]byte, error) {
	tx := f.transactions[id]
	if tx == nil {
		return nil, fmt.Errorf("unknown recovery-selection transaction %s", id)
	}
	var raw bytes.Buffer
	err := tx.Serialize(&raw)
	return raw.Bytes(), err
}

func (f *rgb11RecoverySelectionFacts) GetTxStatus(id string) (*rgb11wallet.BitcoinTxStatus, error) {
	if f.transactions[id] == nil {
		return &rgb11wallet.BitcoinTxStatus{TxID: id}, nil
	}
	return &rgb11wallet.BitcoinTxStatus{TxID: id, Confirmed: true, BlockHeight: 10,
		BlockHash: strings.Repeat("1", 64), Confirmations: 2}, nil
}

func (f *rgb11RecoverySelectionFacts) GetOutspend(point string) (*rgb11wallet.BitcoinOutspend, error) {
	return &rgb11wallet.BitcoinOutspend{Spent: f.spent[point] != "", SpendingTx: f.spent[point]}, nil
}

func (f *rgb11RecoverySelectionFacts) GetUTXO(point string) (*rgb11wallet.BitcoinUTXO, error) {
	out, err := wire.NewOutPointFromString(point)
	if err != nil {
		return nil, err
	}
	tx := f.transactions[out.Hash.String()]
	if tx == nil || uint64(out.Index) >= uint64(len(tx.TxOut)) || f.spent[point] != "" {
		return nil, nil
	}
	output := tx.TxOut[out.Index]
	return &rgb11wallet.BitcoinUTXO{OutPoint: point, Value: output.Value,
		PkScript: append([]byte(nil), output.PkScript...), Confirmations: 2}, nil
}

func (*rgb11RecoverySelectionFacts) GetTip() (*rgb11wallet.BitcoinTip, error) {
	return &rgb11wallet.BitcoinTip{Height: 11, BlockHash: strings.Repeat("1", 64)}, nil
}

func (*rgb11RecoverySelectionFacts) Broadcast([]byte) (string, error) {
	return "", fmt.Errorf("recovery-selection construction must not broadcast")
}

// Adapted from rgb11.issuedChannelFunding: three real genesis allocations on
// two carriers become one real commitment allocation plus a plain fee reserve.
func newRGB11RecoverySelectionFixture(t *testing.T) (*Manager, STPAssetRecoveryRequest, *rgb11RecoverySelectionFacts) {
	t.Helper()
	script := append([]byte{0, 32}, bytes.Repeat([]byte{2}, 32)...)
	fundingTx := wire.NewMsgTx(2)
	fundingTx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: 42}, nil, nil))
	fundingTx.AddTxOut(wire.NewTxOut(20_000, script))
	fundingTx.AddTxOut(wire.NewTxOut(20_000, script))
	hash := fundingTx.TxHash()
	var allocations []issuance.Allocation
	for i, amount := range []uint64{1000, 2000, 4000} {
		seal, err := seals.NewGraphBlindSeal(hash[:], uint32(i/2), uint64(i+21))
		require.NoError(t, err)
		allocations = append(allocations, issuance.Allocation{Seal: seal, Amount: amount})
	}
	issued, err := issuance.Issue(issuance.Spec{Kind: schemas.NIA,
		Network: rgb11IssuanceNetwork(GetChainParam()), Ticker: "STP", Name: "Recovery selection",
		Precision: 2, Allocations: allocations, Timestamp: 1_700_000_001})
	require.NoError(t, err)
	proof, err := nativeproof.BuildRGB11([]byte(issued.Armor), nil)
	require.NoError(t, err)
	decoded, err := nativeproof.Decode(proof)
	require.NoError(t, err)
	asset, err := rgb11wallet.NewContractAssetKey(issued.ContractID, indexer.ASSET_TYPE_FT)
	require.NoError(t, err)
	var sources []rgb11wallet.ChannelFunding
	for i, amount := range []int64{3000, 4000} {
		sources = append(sources, rgb11wallet.ChannelFunding{
			OutPoint: fmt.Sprintf("%s:%d", fundingTx.TxID(), i), Value: 20_000,
			PkScript: script, AssetName: asset, Amount: *indexer.NewDecimalWithScale(amount, 2),
			Consignment: decoded.Consignment,
		})
	}
	facts := &rgb11RecoverySelectionFacts{transactions: map[string]*wire.MsgTx{
		fundingTx.TxID(): fundingTx,
	}, spent: make(map[string]string)}
	commitTx := wire.NewMsgTx(2)
	for _, source := range sources {
		point, err := wire.NewOutPointFromString(source.OutPoint)
		require.NoError(t, err)
		commitTx.AddTxIn(wire.NewTxIn(point, nil, nil))
	}
	_, err = rgb11wallet.ReserveChannelDBC(commitTx, 0)
	require.NoError(t, err)
	commitTx.AddTxOut(wire.NewTxOut(1500, script))
	commitTx.AddTxOut(wire.NewTxOut(30_000, script))
	amount := indexer.NewDecimalWithScale(7000, 2)
	channelContext := rgb11wallet.ChannelContext{ChannelID: "rgb11-recovery-selection",
		Operation: string(STPOpCommit), Branch: "local", Height: 1,
		Entropy: [32]byte{6, 21}, BitcoinNetwork: GetChainParam().Name}
	step, err := rgb11wallet.BuildChannelStep(context.Background(), sources, commitTx, 0,
		[]rgb11wallet.ChannelAssignment{{OutputIndex: 1, Amount: *amount.Clone()}}, channelContext, facts)
	require.NoError(t, err)
	pkg := &rgb11wallet.ChannelPackage{Version: 1, Funding: sources, Steps: []rgb11wallet.ChannelStep{*step}}
	report, err := rgb11wallet.ValidateChannelPackage(context.Background(), pkg, commitTx, facts)
	require.NoError(t, err)
	require.Len(t, report.TerminalAllocations, 1)
	encoded, err := rgb11wallet.EncodeChannelPackage(pkg)
	require.NoError(t, err)
	inventory, err := rgb11wallet.EncodeChannelInventory(sources)
	require.NoError(t, err)
	commitment := NewChannelCommitment()
	commitment.CommitTx = commitTx
	commitment.AssetProofs = map[string][]byte{rgb11wallet.Protocol: encoded}
	commitment.RecoveryFeeIndexes = []uint32{2}
	commitment.LocalBalance[AssetName{AssetName: asset, N: 2}] = amount
	channel := &Channel{ChannelInDB: ChannelInDB{ChannelId: channelContext.ChannelID,
		AssetEntropy: channelContext.Entropy, CommitHeight: 1,
		AssetFunding: map[string][]byte{rgb11wallet.Protocol: inventory}}}
	// The commitment is independently observed before recovery. Its roots are
	// spent, while its native output and designated plain fee output are not.
	facts.transactions[commitTx.TxID()] = commitTx
	for _, source := range sources {
		facts.spent[source.OutPoint] = commitTx.TxID()
	}
	manager := &Manager{}
	manager.rgbManager = &rgb11Manager{Manager: manager, accountOwner: manager, evidence: facts}
	request := STPAssetRecoveryRequest{Channel: channel, Commitment: commitment,
		Operation: STPOpForceClose, Branch: "sweep", RecipientScript: script,
		CommitmentScript: []byte{0x51}, CSVDelay: 1, Height: 11, FeeRate: 1}
	return manager, request, facts
}

func TestSTPRGB11RecoveryDistinguishesNilAndEmptyInputs(t *testing.T) {
	manager, request, facts := newRGB11RecoverySelectionFixture(t)
	protocol := rgb11STPProtocol{}

	t.Run("explicit_empty_without_indexes_has_no_native_assets", func(t *testing.T) {
		req := request
		req.Inputs = make([]*TxOutput, 0)
		require.NotNil(t, req.Inputs)
		// An explicit empty selection must not fall back to OutputIndexes.
		// append([]*TxOutput(nil), req.Inputs...) loses this distinction.
		plan, err := protocol.BuildChannelRecovery(manager, req)
		require.NoError(t, err)
		require.NotNil(t, plan)
		require.True(t, plan.NoNativeAssets)
		require.Nil(t, plan.Tx)
		require.Empty(t, plan.NativeProofs)
	})

	t.Run("nil_with_indexes_recovers_native_allocation", func(t *testing.T) {
		req := request
		require.Nil(t, req.Inputs)
		req.OutputIndexes = []int{1, 2}
		plan, err := protocol.BuildChannelRecovery(manager, req)
		require.NoError(t, err)
		require.NotNil(t, plan)
		require.False(t, plan.NoNativeAssets)
		require.NotNil(t, plan.Tx)
		require.Len(t, plan.Tx.TxIn, 2)
		require.Equal(t, fmt.Sprintf("%s:1", req.Commitment.CommitTx.TxID()), plan.Tx.TxIn[0].PreviousOutPoint.String())
		require.Equal(t, fmt.Sprintf("%s:2", req.Commitment.CommitTx.TxID()), plan.Tx.TxIn[1].PreviousOutPoint.String())
		require.Equal(t, int64(1500), plan.Tx.TxOut[1].Value, "native carrier must not pay recovery fees")
		require.Positive(t, plan.Fee)
		recoveryPackage, err := rgb11wallet.DecodeChannelPackage(plan.NativeProofs[rgb11wallet.Protocol])
		require.NoError(t, err)
		require.Len(t, recoveryPackage.Steps, 2)
		validated, err := rgb11wallet.ValidateChannelPackage(context.Background(), recoveryPackage, plan.Tx, facts)
		require.NoError(t, err)
		require.Len(t, validated.TerminalAllocations, 1)
		allocation := validated.TerminalAllocations[0]
		require.Equal(t, fmt.Sprintf("%s:1", plan.Tx.TxID()), allocation.OutPoint)
		require.Equal(t, recoveryPackage.Funding[0].AssetName, allocation.AssetName)
		require.Zero(t, allocation.Amount.Cmp(indexer.NewDecimalWithScale(7000, 2)))
	})

	t.Run("nil_without_indexes_is_rejected", func(t *testing.T) {
		req := request
		require.Nil(t, req.Inputs)
		require.Empty(t, req.OutputIndexes)
		plan, err := protocol.BuildChannelRecovery(manager, req)
		require.ErrorContains(t, err, "invalid native recovery selection")
		require.Nil(t, plan)
	})
}
