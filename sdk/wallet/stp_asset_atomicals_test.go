package wallet

import (
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	"github.com/stretchr/testify/require"
)

type atomicalsTestIndexer struct {
	IndexerRPCClient
	outputs map[string]*TxOutput
	listed  []*TxOutput
}

func (*atomicalsTestIndexer) GetSyncHeight() int { return 900_000 }

func (client *atomicalsTestIndexer) GetTxOutput(outpoint string) (*TxOutput, error) {
	output := client.outputs[outpoint]
	if output == nil {
		return nil, fmt.Errorf("unknown ARC20 test outpoint %s", outpoint)
	}
	return output.Clone(), nil
}

func (client *atomicalsTestIndexer) GetUtxoListWithTicker(_ string, name *indexer.AssetName) []*indexer.AssetsInUtxo {
	var outputs []*indexer.AssetsInUtxo
	for _, input := range client.listed {
		if indexer.IsPlainAsset(name) {
			if len(input.Assets) == 0 {
				outputs = append(outputs, input.ToAssetsInUtxo())
			}
		} else if input.GetAsset(name).Sign() > 0 {
			outputs = append(outputs, input.ToAssetsInUtxo())
		}
	}
	return outputs
}

func atomicalsTestEnvironment(t *testing.T) {
	t.Helper()
	previousChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previousChain })
	if _, registered := stpAssetProtocols.byName[indexer.PROTOCOL_NAME_ATOM]; !registered {
		previousRegistry := stpAssetProtocols
		protocols := append([]STPAssetProtocol{atomicalsSTPProtocol{}}, previousRegistry.ordered...)
		var err error
		stpAssetProtocols, err = NewSTPAssetRegistry(protocols...)
		require.NoError(t, err)
		t.Cleanup(func() { stpAssetProtocols = previousRegistry })
	}
}

func atomicalsTestName() *AssetName {
	return &AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ATOM, Type: indexer.ASSET_TYPE_FT, Ticker: "quark"}, N: 1}
}

func atomicalsTestInput(position int, name *AssetName, amount, value int64, script []byte) *TxOutput {
	output := feeBudgetReviewPlainInput(position, value, script)
	if name != nil {
		output.Assets = indexer.TxAssets{{Name: name.AssetName, Amount: *indexer.NewDefaultDecimal(amount), BindingSat: 1}}
	}
	return output
}

func atomicalsTestConfigure(manager *Manager, names ...*AssetName) *atomicalsTestIndexer {
	client := &atomicalsTestIndexer{outputs: make(map[string]*TxOutput)}
	manager.l1IndexerClient = &IndexerRPCClientMgr{active: client}
	manager.utxoLockerL1 = NewUtxoLocker(newMemoryKVDB(), client, L1_NETWORK_BITCOIN)
	if manager.tickerInfoMap == nil {
		manager.tickerInfoMap = make(map[string]*indexer.TickerInfo)
	}
	for position, name := range names {
		manager.tickerInfoMap[name.String()] = &indexer.TickerInfo{
			AssetName: name.AssetName, InscriptionId: fmt.Sprintf("%064xi0", position+1), N: 1, Divisibility: 0,
		}
	}
	return client
}

func atomicalsTestTransfer(t *testing.T, total, bitcoin int64, amounts ...int64) (*Manager, *STPAssetPlan, *atomicalsTestIndexer) {
	t.Helper()
	manager := safetyTestManager(t, &Channel{ChannelInDB: *NewChannelInDB()})
	name := atomicalsTestName()
	client := atomicalsTestConfigure(manager, name)
	address := manager.wallet.GetAddress()
	script, err := GetPkScriptFromAddress(address)
	require.NoError(t, err)
	asset := atomicalsTestInput(11, name, total, bitcoin, script)
	fee := atomicalsTestInput(12, nil, 0, 10_000, script)
	client.listed = []*TxOutput{asset, fee}
	client.outputs[asset.OutPointStr], client.outputs[fee.OutPointStr] = asset, fee
	var destinations []*SendAssetInfo
	for _, amount := range amounts {
		destinations = append(destinations, &SendAssetInfo{Address: address, AssetName: &name.AssetName, AssetAmt: indexer.NewDefaultDecimal(amount)})
	}
	plan, err := (atomicalsSTPProtocol{}).BuildTransfer(manager, STPAssetTransferRequest{
		Address: address, Asset: name, Destinations: destinations, FeeRate: 1,
	})
	require.NoError(t, err)
	return manager, plan, client
}

func TestAtomicalsTransferUsesNativeColoringAndPreservesFees(t *testing.T) {
	atomicalsTestEnvironment(t)
	for _, spec := range []struct {
		name     string
		total    int64
		bitcoin  int64
		payments []int64
	}{
		{"one_unit_full", 1, 330, []int64{1}},
		{"one_hundred_full_padded_input", 100, 600, []int64{100}},
		{"small_recipient_after_change", 1000, 1200, []int64{1}},
		{"small_recipient_and_large_recipient", 1000, 1300, []int64{100, 400}},
		{"small_asset_change", 800, 850, []int64{700}},
	} {
		t.Run(spec.name, func(t *testing.T) {
			manager, plan, client := atomicalsTestTransfer(t, spec.total, spec.bitcoin, spec.payments...)
			var totalIn, totalOut int64
			var facts []*TxOutput
			for _, input := range plan.Tx.TxIn {
				fact := client.outputs[input.PreviousOutPoint.String()]
				require.NotNil(t, fact)
				facts = append(facts, fact)
				totalIn += fact.Value()
			}
			var outputs []*TxOutput
			for index, output := range plan.Tx.TxOut {
				require.GreaterOrEqual(t, output.Value, int64(330))
				totalOut += output.Value
				outputs = append(outputs, indexer.GenerateTxOutput(plan.Tx, index))
			}
			require.Equal(t, plan.Fee, totalIn-totalOut)
			require.Positive(t, plan.Fee)
			require.NoError(t, manager.ClassifySTPAtomicalsOutputs(plan.Tx, facts, outputs))
			var actual int64
			for _, output := range outputs {
				actual += output.GetAsset(&atomicalsTestName().AssetName).Int64()
				require.Empty(t, output.Offsets, "native ARC20 amounts must never become ordinal ranges")
			}
			require.Equal(t, spec.total, actual)
			signed, err := SignTxWithWallet(manager.wallet, plan.Tx, plan.PrevFetcher)
			require.NoError(t, err)
			require.NoError(t, VerifySignedTx(signed, plan.PrevFetcher))
			require.Equal(t, plan.Tx.TxHash(), signed.TxHash())
			// Signing returns a copy; the transient layout plan intentionally
			// retains output pointers. Reattach only the signatures to check the
			// final witness against native operation-envelope restrictions.
			for position, input := range plan.Tx.TxIn {
				input.Witness = signed.TxIn[position].Witness
			}
			require.NoError(t, (atomicalsSTPProtocol{}).Verify(STPAssetVerification{Operation: STPOpTransfer, Tx: plan.Tx, Plan: plan}))
		})
	}
}

func TestAtomicalsRebuildTransferOutputsUsesNativeClassifier(t *testing.T) {
	atomicalsTestEnvironment(t)
	manager, plan, client := atomicalsTestTransfer(t, 1000, 1400, 100, 400)
	inputs, outputs, err := manager.RebuildTxOutput(plan.Tx, client.outputs)
	require.NoError(t, err)
	require.Len(t, inputs, len(plan.Tx.TxIn))
	require.Len(t, outputs, len(plan.Tx.TxOut))
	var amount int64
	for _, output := range outputs {
		amount += output.GetAsset(&atomicalsTestName().AssetName).Int64()
		require.Empty(t, output.Offsets[atomicalsTestName().AssetName])
	}
	require.EqualValues(t, 1000, amount)
	// Reconstructing a sign request must leave the authenticated prevout view
	// intact; stripping ARC20 for ordinal accounting is transaction-local.
	for _, input := range inputs {
		require.Equal(t, client.outputs[input.OutPointStr].Assets, input.Assets)
	}
	broken := plan.Tx.Copy()
	broken.TxOut = broken.TxOut[:1]
	_, _, err = manager.RebuildTxOutput(broken, client.outputs)
	require.Error(t, err, "a withdrawal sign request that burns ARC20 must be rejected")
}

func TestAtomicalsTransferRejectsHiddenAssetsAndImpossibleDustSplits(t *testing.T) {
	atomicalsTestEnvironment(t)
	protocol := atomicalsSTPProtocol{}
	name := atomicalsTestName()
	t.Run("two_sub_dust_recipients", func(t *testing.T) {
		_, err := atomicalsPaymentOutputs([]atomicalsPayment{{script: []byte{0x51}, amount: 1}, {script: []byte{0x51}, amount: 100}})
		require.ErrorContains(t, err, "two sub-dust")
	})
	t.Run("input_with_hidden_protocol_asset", func(t *testing.T) {
		manager, _, client := atomicalsTestTransfer(t, 1000, 1300, 500)
		input := client.listed[0]
		selected := input.Clone()
		input.Assets = append(input.Assets, indexer.AssetInfo{
			Name: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_RUNES, Type: "f", Ticker: "HIDDEN"}, Amount: *indexer.NewDefaultDecimal(1),
		})
		_, err := manager.authenticatedAtomicalsInput(name, selected)
		require.ErrorContains(t, err, "unrelated or invalid")
	})
	t.Run("fee_listing_is_not_authoritative", func(t *testing.T) {
		manager, _, client := atomicalsTestTransfer(t, 100, 330, 100)
		listedFee := client.listed[1]
		fullFee := listedFee.Clone()
		fullFee.Assets = indexer.TxAssets{{Name: name.AssetName, Amount: *indexer.NewDefaultDecimal(1), BindingSat: 1}}
		client.outputs[listedFee.OutPointStr] = fullFee
		_, err := protocol.BuildTransfer(manager, STPAssetTransferRequest{Address: manager.wallet.GetAddress(), Asset: name, FeeRate: 1,
			Destinations: []*SendAssetInfo{{Address: manager.wallet.GetAddress(), AssetAmt: indexer.NewDefaultDecimal(100)}}})
		require.ErrorContains(t, err, "asset-free")
	})
	t.Run("native_id_is_required", func(t *testing.T) {
		manager, _, _ := atomicalsTestTransfer(t, 100, 600, 100)
		manager.tickerInfoMap[name.String()].InscriptionId = "quark"
		_, err := manager.atomicalsIdentity(name)
		require.ErrorContains(t, err, "canonical native identity")
	})
	t.Run("no_ordinal_offsets", func(t *testing.T) {
		input := atomicalsTestInput(1, name, 100, 600, []byte{0x51})
		input.Offsets[name.AssetName] = indexer.AssetOffsets{{Start: 0, End: 100}}
		require.ErrorContains(t, protocol.VerifyInput(name, input), "native ARC20")
	})
}

func TestAtomicalsVerifierDetectsColorChangesBeyondItsLayout(t *testing.T) {
	atomicalsTestEnvironment(t)
	protocol := atomicalsSTPProtocol{}
	_, plan, _ := atomicalsTestTransfer(t, 1000, 1400, 500)
	// A later protocol must not prepend a spendable output: even though every
	// ARC20 output pointer/value remains present, native allocation would move.
	plan.Tx.TxOut = append([]*wire.TxOut{wire.NewTxOut(330, []byte{0x51})}, plan.Tx.TxOut...)
	require.ErrorContains(t, protocol.Verify(STPAssetVerification{Operation: STPOpCommit, Tx: plan.Tx, Plan: plan}), "allocation")
}

type atomicalsTestNode struct{ NodeRPCClient }

func (*atomicalsTestNode) GetDeployedContractsReq() ([]string, error) { return nil, nil }

func TestAtomicalsCommitmentProtectsNativeAndOrdinalPrefixes(t *testing.T) {
	atomicalsTestEnvironment(t)
	manager := safetyTestManager(t, &Channel{ChannelInDB: *NewChannelInDB()})
	manager.serverNode = &Node{client: &atomicalsTestNode{}}
	name := atomicalsTestName()
	atomicalsTestConfigure(manager, name)
	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	input := atomicalsTestInput(21, name, 1000, 1400, []byte{0x51})
	channel.FundingUtxos[*name] = []*TxOutput{input}
	tx := wire.NewMsgTx(2)
	marker := wire.NewTxOut(0, []byte{txscript.OP_RETURN, 1, 42})
	tx.AddTxOut(marker)
	var weight utils.TxWeightEstimator
	plan, err := (atomicalsSTPProtocol{}).BuildCommitment(manager, STPAssetCommitmentRequest{
		Tx: tx, Channel: channel, LocalBalance: map[AssetName]*Decimal{*name: indexer.NewDefaultDecimal(900)},
		RemoteBalance: map[AssetName]*Decimal{*name: indexer.NewDefaultDecimal(100)},
		LocalScript:   []byte{0x51}, RemoteScript: []byte{0x52}, Weight: &weight,
	})
	require.NoError(t, err)
	require.True(t, plan.FixedPrefix)
	require.Same(t, marker, tx.TxOut[0], "RGB reserves the first OP_RETURN before native allocation")
	require.EqualValues(t, 900, tx.TxOut[1].Value)
	require.EqualValues(t, 500, tx.TxOut[2].Value, "all excess native carrier sats remain in its segment")
	snapshotSTPAssetLayout(tx, nil, []*wire.TxOut{marker}, false, plan)
	// An independently authenticated later protocol starts at equal Bitcoin
	// input/output offsets. Its prevout contributes no fabricated ARC20 state.
	later := atomicalsTestInput(22, nil, 0, 800, []byte{0x53})
	tx.AddTxIn(later.TxIn())
	tx.AddTxOut(wire.NewTxOut(800, []byte{0x54}))
	plan.PrevFetcher.AddPrevOut(*later.OutPoint(), &later.OutValue)
	require.NoError(t, (atomicalsSTPProtocol{}).Verify(STPAssetVerification{Operation: STPOpCommit, Tx: tx, Plan: plan}))
	inputs := []*TxOutput{input, later}
	outputs := []*TxOutput{indexer.GenerateTxOutput(tx, 0), indexer.GenerateTxOutput(tx, 1), indexer.GenerateTxOutput(tx, 2), indexer.GenerateTxOutput(tx, 3)}
	require.NoError(t, manager.ClassifySTPAtomicalsOutputs(tx, inputs, outputs))
	require.EqualValues(t, 900, outputs[1].GetAsset(&name.AssetName).Int64())
	require.EqualValues(t, 100, outputs[2].GetAsset(&name.AssetName).Int64())
	require.Empty(t, outputs[0].Assets)
	require.Empty(t, outputs[3].Assets)
}

func TestAtomicalsCommitmentRetiresOnlyEmptyAllocations(t *testing.T) {
	atomicalsTestEnvironment(t)
	manager := safetyTestManager(t, &Channel{ChannelInDB: *NewChannelInDB()})
	manager.serverNode = &Node{client: &atomicalsTestNode{}}
	name := atomicalsTestName()
	atomicalsTestConfigure(manager, name)
	for _, test := range []struct {
		name    string
		funded  bool
		balance int64
		valid   bool
	}{
		{"full_exit", false, 0, true},
		{"live_funding_without_balance", true, 0, false},
		{"balance_without_funding", false, 1000, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			channel := &Channel{ChannelInDB: *NewChannelInDB()}
			channel.FundingUtxos[*name] = nil
			if test.funded {
				channel.FundingUtxos[*name] = []*TxOutput{atomicalsTestInput(21, name, 1000, 1400, []byte{0x51})}
			}
			tx := wire.NewMsgTx(2)
			marker := wire.NewTxOut(0, []byte{txscript.OP_RETURN, 1, 42})
			tx.AddTxOut(marker)
			plan, err := (atomicalsSTPProtocol{}).BuildCommitment(manager, STPAssetCommitmentRequest{
				Tx: tx, Channel: channel,
				LocalBalance: map[AssetName]*Decimal{*name: indexer.NewDefaultDecimal(test.balance)},
				LocalScript:  []byte{0x51}, RemoteScript: []byte{0x52},
			})
			if !test.valid {
				require.Error(t, err, "an inconsistent live allocation must not be silently retired")
				return
			}
			require.NoError(t, err)
			require.Empty(t, tx.TxIn)
			require.Len(t, tx.TxOut, 1)
			require.Same(t, marker, tx.TxOut[0])
			require.Nil(t, plan.ProtocolData, "an empty allocation needs no native coloring evidence")
			require.False(t, plan.FixedPrefix)
		})
	}
}

func TestAtomicalsClassifierRejectsBurnAndMismatchedFacts(t *testing.T) {
	atomicalsTestEnvironment(t)
	manager := safetyTestManager(t, &Channel{ChannelInDB: *NewChannelInDB()})
	name := atomicalsTestName()
	atomicalsTestConfigure(manager, name)
	input := atomicalsTestInput(1, name, 1000, 1200, []byte{0x51})
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(input.TxIn())
	tx.AddTxOut(wire.NewTxOut(900, []byte{0x51}))
	output := indexer.GenerateTxOutput(tx, 0)
	require.Error(t, manager.ClassifySTPAtomicalsOutputs(tx, []*TxOutput{input}, []*TxOutput{output}))
	require.Empty(t, output.Assets, "a failed native verification must not partially modify views")
	tx.TxOut[0].Value = 1200
	output = indexer.GenerateTxOutput(tx, 0)
	input.OutPointStr = fmt.Sprintf("%064x:1", 1)
	require.ErrorContains(t, manager.ClassifySTPAtomicalsOutputs(tx, []*TxOutput{input}, []*TxOutput{output}), "identity mismatch")
	require.NoError(t, (&Manager{}).ClassifySTPAtomicalsOutputs(nil, nil, nil), "no-ARC classification must need no indexer")
}

func TestAtomicalsSpliceUsesNativeAllocationAndExactChange(t *testing.T) {
	atomicalsTestEnvironment(t)
	for _, operation := range []STPAssetOperation{STPOpSpliceIn, STPOpSpliceOut} {
		for _, spec := range []struct {
			name                   string
			amount, total, bitcoin int64
		}{
			{"full_small", 100, 100, 600},
			{"partial_with_small_change", 700, 800, 850},
			{"partial_padded_input", 700, 1000, 1500},
		} {
			t.Run(string(operation)+"_"+spec.name, func(t *testing.T) {
				manager, channel := feeBudgetReviewChannel(t)
				name := atomicalsTestName()
				atomicalsTestConfigure(manager, name)
				channel.FeeCfg.SplicingOutFee = 0
				assetScript := channel.GetLocalPkScript()
				if operation == STPOpSpliceOut {
					assetScript = channel.GetChannelPkScript()
				}
				asset := atomicalsTestInput(101, name, spec.total, spec.bitcoin, assetScript)
				fees := []*TxOutput{atomicalsTestInput(102, nil, 0, 10_000, channel.GetLocalPkScript())}
				channel.FundingUtxos[*name] = []*TxOutput{asset}
				channel.LocalCommitment.LocalBalance[*name] = indexer.NewDefaultDecimal(spec.total)
				resv := feeBudgetReviewReservation(name, spec.amount, channel)
				resv.DestAddr = manager.wallet.GetAddress()
				stubs, stubValue := channel.NeedStubUtxo(name)
				var err error
				if operation == STPOpSpliceIn {
					err = manager.AllowSplicingInV2(resv, []*TxOutput{asset}, fees, stubs, stubValue)
				} else {
					err = manager.AllowSplicingOutV2(resv, fees)
				}
				require.NoError(t, err)
				require.False(t, NeedStubUtxoForInputAsset(name, resv.Amt))
				var tx *wire.MsgTx
				var fetcher txscript.PrevOutputFetcher
				if operation == STPOpSpliceIn {
					tx, fetcher, err = manager.CreateSplicingInTx(resv)
				} else {
					tx, fetcher, err = manager.CreateSplicingOutTx(resv)
				}
				require.NoError(t, err)
				require.EqualValues(t, spec.amount, resv.SplicingOutput.GetAsset(&name.AssetName).Int64())
				if spec.total > spec.amount {
					require.EqualValues(t, spec.total-spec.amount, resv.SplicingChangeOutput.GetAsset(&name.AssetName).Int64())
					require.Equal(t, tx.TxOut[1].Value, resv.SplicingChangeOutput.Value(), "persisted native change must match its actual carrier, excluding fee budget")
				}
				var inputValue, outputValue int64
				for _, input := range tx.TxIn {
					inputValue += fetcher.FetchPrevOutput(input.PreviousOutPoint).Value
				}
				for _, output := range tx.TxOut {
					outputValue += output.Value
				}
				require.Equal(t, resv.RequiredFee, inputValue-outputValue)
			})
		}
	}
}

func TestAtomicalsSpliceRejectsSubDustPartialBeforeMutation(t *testing.T) {
	atomicalsTestEnvironment(t)
	manager, channel := feeBudgetReviewChannel(t)
	name := atomicalsTestName()
	atomicalsTestConfigure(manager, name)
	asset := atomicalsTestInput(101, name, 1000, 1200, channel.GetLocalPkScript())
	fee := atomicalsTestInput(102, nil, 0, 10_000, channel.GetLocalPkScript())
	resv := feeBudgetReviewReservation(name, 100, channel)
	require.ErrorContains(t, manager.AllowSplicingInV2(resv, []*TxOutput{asset}, []*TxOutput{fee}, 2, 330), "sub-dust partial")
	require.Empty(t, resv.SplicingInputs)
	require.Empty(t, resv.Fees)
}

var _ STPAssetProtocol = atomicalsSTPProtocol{}
var _ STPAssetOutputClassifier = atomicalsSTPProtocol{}
var _ STPAssetOutputSplitter = atomicalsSTPProtocol{}
var _ STPAssetSplicePlanner = atomicalsSTPProtocol{}
