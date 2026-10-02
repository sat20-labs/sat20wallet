package e2e

import (
	"encoding/hex"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// These negative cases intentionally bypass the high-level request encoder,
// but signing and submission still use public SDK APIs. Otherwise a wallet
// rejection would say nothing about the node's mempool admission checks.
func sdkAdmissionInputs(t *testing.T, f *sdkContractReviewFixture, name string, amount int64) ([]wire.OutPoint, wire.TxOut, *txscript.MultiPrevOutFetcher) {
	t.Helper()
	asset := wire.NewAssetNameFromString(name)
	require.NotNil(t, asset)
	assets, fees, err := f.owner.GetUtxosWithAssetV2_SatsNet(f.owner.GetWallet().GetAddress(), 1000,
		indexer.NewDefaultDecimal(amount), asset, nil)
	require.NoError(t, err)
	selected := append(append([]string(nil), assets...), fees...)
	seen := make(map[wire.OutPoint]bool)
	inputs := make([]wire.OutPoint, 0, len(selected))
	prev := txscript.NewMultiPrevOutFetcher(nil)
	var total wire.TxOut
	for _, text := range selected {
		outpoint, err := wire.NewOutPointFromString(text)
		require.NoError(t, err)
		if seen[*outpoint] { continue }
		seen[*outpoint] = true
		raw, err := f.network.Bootstrap.Client.GetRawTransaction(&outpoint.Hash)
		require.NoError(t, err)
		require.Less(t, int(outpoint.Index), len(raw.MsgTx().TxOut))
		output := raw.MsgTx().TxOut[outpoint.Index]
		total.Value += output.Value
		require.NoError(t, total.Assets.Merge(output.Assets))
		prev.AddPrevOut(*outpoint, output)
		inputs = append(inputs, *outpoint)
	}
	require.NotEmpty(t, inputs)
	return inputs, total, prev
}

func sdkAdmissionOrdinaryTx(t *testing.T, f *sdkContractReviewFixture, name string) (*wire.MsgTx, *txscript.MultiPrevOutFetcher) {
	t.Helper()
	inputs, total, prev := sdkAdmissionInputs(t, f, name, 1)
	require.Greater(t, total.Value, int64(1000))
	script, err := wallet.GetP2TRpkScript(f.owner.GetWallet().GetPaymentPubKey())
	require.NoError(t, err)
	tx := wire.NewMsgTx(wire.TxVersion)
	for _, input := range inputs { in := input; tx.AddTxIn(wire.NewTxIn(&in, nil, nil)) }
	tx.AddTxOut(wire.NewTxOut(total.Value-1000, total.Assets.Clone(), script))
	return tx, prev
}

func sdkAdmissionMustReject(t *testing.T, f *sdkContractReviewFixture, tx *wire.MsgTx, prev *txscript.MultiPrevOutFetcher, reason string) {
	t.Helper()
	signed, err := f.owner.SignContractTx_SatsNet(tx, prev, "", 0)
	require.NoError(t, err, "contract-review: malformed-policy fixture must still have valid signatures")
	txid, err := f.owner.BroadcastTx_SatsNet(signed)
	if err == nil {
		// Restore a clean test UTXO view even on the red implementation. None
		// of these ordinary transactions should block contract execution.
		f.mined(t, &wallet.ContractTxResult{TxID: txid})
		t.Errorf("contract-review: node accepted %s through public SDK broadcast", reason)
		return
	}
	require.Regexp(t, "(?i)(asset|BindingSat|carrier|satoshi|sats|TxAssets)", err.Error(),
		"contract-review: rejection must be the intended policy, not missing funds or a transport failure")
	inPool, poolErr := f.network.Bootstrap.Client.GetRawMempool()
	require.NoError(t, poolErr)
	for _, hash := range inPool { require.NotEqual(t, signed.TxHash(), *hash) }
}

func TestSDKSmartContractsAdmission(t *testing.T) {
	const bound = "ordx:f:admissionbound"
	base := newTemplateFixtureWithProfiles(t, []templateAssetProfile{
		{Name: "bound", Asset: bound, BindingSat: 1, Supply: "10000"},
	})
	key := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	splitToDKVSKeyPathActors(t, base, base.gasAnchor, wallet.GetGasAssetName(),
		[]int64{50000000, 50000000}, []int64{1000000, 1000000}, []*dkvsKeyPathActor{key, key})
	owner, _ := newWalletManagerForNode(t, base.Network.Core, dkvsClientMnemonic)
	reader, _ := newWalletManagerForNode(t, base.Network.Bootstrap, "")
	script, err := wallet.GetP2TRpkScript(owner.GetWallet().GetPaymentPubKey())
	require.NoError(t, err)
	profile := templateAssetProfile{Name: "bound", Asset: bound, BindingSat: 1, Supply: "10000"}
	base.splitProfile(t, base.A, base.assetAnchors[bound], profile, []string{"10000"}, []int64{100000}, []*templateActor{{PkScript: script}})
	f := &sdkContractReviewFixture{network: base.Network, owner: owner, reader: reader}

	t.Run("ordinary_value_only_transfer_control", func(t *testing.T) {
		tx, prev := sdkAdmissionOrdinaryTx(t, f, wallet.GetGasAssetName())
		signed, err := owner.SignContractTx_SatsNet(tx, prev, "", 0)
		require.NoError(t, err)
		id, err := owner.BroadcastTx_SatsNet(signed)
		require.NoError(t, err)
		f.mined(t, &wallet.ContractTxResult{TxID: id})
	})
	t.Run("TxAssets_plain_satoshi_alias_is_rejected_even_at_zero", func(t *testing.T) {
		tx, prev := sdkAdmissionOrdinaryTx(t, f, wallet.GetGasAssetName())
		// Zero defeats a mere inventory-subtraction check; the reserved name
		// itself must be forbidden in the serialized TxAssets collection.
		require.NoError(t, tx.TxOut[0].Assets.Add(&wire.AssetInfo{Name: wire.AssetName{}, Amount: *indexer.NewDefaultDecimal(0)}))
		sdkAdmissionMustReject(t, f, tx, prev, "reserved :: asset in TxAssets")
	})
	t.Run("bound_asset_metadata_cannot_change", func(t *testing.T) {
		tx, prev := sdkAdmissionOrdinaryTx(t, f, bound)
		name := wire.NewAssetNameFromString(bound)
		asset, err := tx.TxOut[0].Assets.Find(name)
		require.NoError(t, err)
		require.EqualValues(t, 1, asset.BindingSat)
		asset.BindingSat = 2
		sdkAdmissionMustReject(t, f, tx, prev, "changed BindingSat metadata")
	})
	t.Run("bound_asset_cannot_drop_carrier_sats", func(t *testing.T) {
		tx, prev := sdkAdmissionOrdinaryTx(t, f, bound)
		name := wire.NewAssetNameFromString(bound)
		asset, err := tx.TxOut[0].Assets.Find(name)
		require.NoError(t, err)
		// A previous red metadata case may have changed the fixture metadata.
		// Reuse the exact confirmed value; this test isolates carrier checks.
		require.Positive(t, asset.BindingSat)
		value := tx.TxOut[0].Value
		tx.TxOut[0].Value = 0
		tx.AddTxOut(wire.NewTxOut(value, nil, script))
		sdkAdmissionMustReject(t, f, tx, prev, "insufficient carrier sats")
	})

	// Gas admission is on a separate funded contract and is last: the red
	// implementation can poison mining, but cannot contaminate earlier cases.
	t.Run("raw_below_base_gas_is_rejected_by_node_not_only_encoder", func(t *testing.T) {
		artifact, err := CompileSolidityFile("testdata/contracts/SDKReviewProbe.sol", "SDKReviewProbe", SolidityCompileOptions{})
		require.NoError(t, err)
		address := f.deployEVM(t, appendSolidityConstructorArgs(artifact.Bytecode, evmABIString(bound), evmABIString(bound)))
		inputs, total, prev := sdkAdmissionInputs(t, f, wallet.GetGasAssetName(), 10000)
		height, err := f.network.Bootstrap.Client.GetBlockCount(); require.NoError(t, err)
		baseFee, err := contract.GasFeeAtHeight(contract.InvokeBaseGas, uint64(height+1)); require.NoError(t, err)
		gasName := wire.NewAssetNameFromString(wallet.GetGasAssetName())
		fundingAssets := wire.TxAssets{{Name: *gasName, Amount: *indexer.NewDefaultDecimal(1000)}}
		changeAssets := total.Assets.Clone()
		require.NoError(t, changeAssets.Split(wire.TxAssets{{Name: *gasName, Amount: *indexer.NewDefaultDecimal(1000+baseFee)}}))
		contractAddress, err := contract.DecodeContractAddress(address); require.NoError(t, err)
		tx, err := contract.BuildInvokeTx(contract.InvokeTxBuildRequest{
			Contract: contractAddress, GasLimit: contract.InvokeBaseGas-1, CallNonce: 99001, Action: "call",
			Param: solidityCall("inc()"), Funding: *wire.NewTxOut(100, fundingAssets, nil), Inputs: inputs,
			ExtraOutputs: []*wire.TxOut{wire.NewTxOut(total.Value-100, changeAssets, script)},
		})
		require.NoError(t, err)
		signed, err := owner.SignContractTx_SatsNet(tx, prev, wallet.GetGasAssetName(), baseFee); require.NoError(t, err)
		_, err = owner.BroadcastTx_SatsNet(signed)
		require.Error(t, err, "contract-review: node admitted below-floor gas after bypassing the SDK request encoder")
		require.Regexp(t, "(?i)gas", err.Error())
		// A normal SDK call must still mine and update the runtime after rejection.
		req := &wallet.ContractInvokeRequest{ContractType: wallet.ContractTypeEVM, ContractAddress: address,
			Action: "call", ParamEncoding: "hex", Param: hex.EncodeToString(solidityCall("inc()")), GasLimit: 1000000}
		f.invoke(t, owner, req, contract.ResultStatusSuccess)
		sdkReviewRequireCounter(t, reader, address, 1)
	})
}
