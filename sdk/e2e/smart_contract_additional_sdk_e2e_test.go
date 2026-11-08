package e2e

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Restart only a node created by this test harness, in its existing temporary
// directory. No production process, wallet database, or external configuration
// is read or copied. Business operations before/after restart still use SDK APIs.
func sdkReviewRestartCore(t *testing.T, f *sdkContractReviewFixture) {
	t.Helper()
	node := f.network.Core
	require.NotNil(t, node.cmd)
	relative, err := filepath.Rel(os.TempDir(), node.nodeDir)
	require.NoError(t, err)
	require.False(t, relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)))
	require.Equal(t, node.nodeDir, node.cmd.Dir)
	args := append([]string(nil), node.cmd.Args...)
	env := append([]string(nil), node.cmd.Env...)
	extraFiles := append([]*os.File(nil), node.cmd.ExtraFiles...)
	require.NotEmpty(t, args)
	require.NoError(t, node.TearDown())
	file, err := os.OpenFile(node.logFile, os.O_WRONLY|os.O_APPEND, 0600)
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = node.nodeDir, env, file, file
	cmd.ExtraFiles = extraFiles
	require.NoError(t, cmd.Start())
	node.cmd = cmd
	node.Client = waitForRPCClient(t, node.rpcAddr)
	peers, err := node.Client.GetPeerInfo()
	require.NoError(t, err)
	connected := false
	for _, peer := range peers {
		if peer.Addr == f.network.Bootstrap.p2pAddr {
			connected = true
		}
	}
	if !connected {
		require.NoError(t, connectNode(node, f.network.Bootstrap))
	}
	require.NoError(t, joinBlocks(f.network.Nodes))
}

func TestSDKSmartContractsAdditional(t *testing.T) {
	f := newSDKContractReviewFixture(t)
	runSDKSmartContractsAdditional(t, f)
}

func runSDKSmartContractsAdditional(t *testing.T, f *sdkContractReviewFixture) {
	t.Helper()
	artifact, err := LoadSolidityArtifact("testdata/contracts/SDKReviewProbe.sol", "SDKReviewProbe")
	require.NoError(t, err)
	code := appendSolidityConstructorArgs(artifact.Bytecode, evmABIString(reviewAssetA), evmABIString(reviewAssetB))
	request := &wallet.ContractDeployRequest{ContractType: wallet.ContractTypeEVM,
		ContractContent: hex.EncodeToString(code), ContentEncoding: "hex", DeployNonce: 700001,
		GasLimit: 5000000, FundingValue: 1000}
	first, err := f.owner.DeployUnifiedContract(request)
	require.NoError(t, err)
	work, block := f.mined(t, first)
	require.Equal(t, contract.ResultStatusSuccess, sdkReviewResultStatus(t, work, block))
	address := first.ContractAddress
	f.invoke(t, f.owner, sdkReviewEVMCall(address, solidityCall("inc()")), contract.ResultStatusSuccess)

	t.Run("duplicate_deploy_nonce_does_not_overwrite_existing_runtime", func(t *testing.T) {
		duplicate := *request
		duplicate.Assets = []wallet.ContractFundingAsset{{AssetName: reviewAssetB, Amount: "6"}}
		result, err := f.owner.DeployUnifiedContract(&duplicate)
		if err != nil {
			require.Nil(t, result)
			return
		}
		require.Equal(t, address, result.ContractAddress)
		tx, mined := f.mined(t, result)
		require.NotEqual(t, contract.ResultStatusSuccess, sdkReviewResultStatus(t, tx, mined))
		sdkReviewRequireCounter(t, f.reader, address, 1)
		require.EqualValues(t, 6, sdkReviewReturnedAsset(mined, f.owner.GetWallet().GetAddress(), reviewAssetB))
	})
	t.Run("node_restart_preserves_runtime_and_accepts_next_SDK_call", func(t *testing.T) {
		before := sdkReviewProbeState(t, f.owner, address)
		sdkReviewRestartCore(t, f)
		require.Equal(t, before, sdkReviewProbeState(t, f.owner, address))
		f.invoke(t, f.owner, sdkReviewEVMCall(address, solidityCall("inc()")), contract.ResultStatusSuccess)
		sdkReviewRequireCounter(t, f.reader, address, 2)
	})
	t.Run("AMM_default_swap_uses_real_pool_and_exact_asset_conservation", func(t *testing.T) {
		c := wallet.NewAmmContract()
		c.AssetName = *indexer.NewAssetNameFromString(reviewAssetB)
		c.AssetAmt, c.SatValue, c.K = "1000", 10000, "10000000"
		pool := f.deployTemplate(t, contract.TemplateAMM, string(c.Content()), 10000,
			[]wallet.ContractFundingAsset{{AssetName: reviewAssetB, Amount: "1000"}})
		req := &wallet.ContractInvokeRequest{ContractType: wallet.ContractTypeTemplate, SubType: contract.TemplateAMM,
			ContractAddress: pool, DefaultInvoke: true, Value: 1000, GasLimit: 100000}
		_, mined := f.invoke(t, f.other, req, contract.ResultStatusSuccess)
		paid := sdkReviewReturnedAsset(mined, f.other.GetWallet().GetAddress(), reviewAssetB)
		require.Positive(t, paid)
		require.Less(t, paid, int64(1000))
		remaining := sdkReviewOptionalAmount(t, sdkReviewQueryState(t, f.reader, pool), "assetAInPool")
		require.EqualValues(t, 1000, remaining+paid, "contract-review: AMM default swap minted/lost inventory")
		require.Greater(t, sdkReviewOptionalAmount(t, sdkReviewQueryState(t, f.reader, pool), "assetBInPool"), int64(10000))
	})
	t.Run("AUTOPAY_cancel_parameter_template_matches_supported_actions", func(t *testing.T) {
		require.True(t, contract.IsTemplateInvokeActionSupported(contract.TemplateAutopay, contract.TemplateInvokeAPICancel))
		_, err := f.owner.QueryParamForInvokeUnifiedContract(wallet.ContractTypeTemplate, contract.TemplateAutopay, contract.TemplateInvokeAPICancel)
		require.NoError(t, err, "contract-review: SDK omits a supported AUTOPAY cancel parameter template")
	})
	t.Run("AUTOPAY_raw_SDK_cancel_runtime_control_and_replay", func(t *testing.T) {
		content := contract.TemplateAutopayContract{ServiceName: "sdk-review-cancel", Recipient: f.other.GetWallet().GetAddress(),
			FeeAssetName: reviewAssetA, MinAmountPerBlock: "1"}
		pool := f.deployTemplate(t, contract.TemplateAutopay, content, 0,
			[]wallet.ContractFundingAsset{{AssetName: reviewAssetA, Amount: "100"}})
		// This is an explicit supported SDK encoding, not a raw node call.
		// It separates the JSON encoder defect from the on-chain cancel path.
		param := contract.TemplateCloseInvokeParam{}
		encoded, err := param.Encode()
		require.NoError(t, err)
		req := &wallet.ContractInvokeRequest{ContractType: wallet.ContractTypeTemplate, SubType: contract.TemplateAutopay,
			ContractAddress: pool, Action: contract.TemplateInvokeAPICancel, ParamEncoding: "base64",
			Param: base64.StdEncoding.EncodeToString(encoded), GasLimit: contract.InvokeBaseGas}
		_, mined := f.invoke(t, f.owner, req, contract.ResultStatusSuccess)
		require.EqualValues(t, 100, sdkReviewReturnedAsset(mined, f.owner.GetWallet().GetAddress(), reviewAssetA))
		_, replay := f.invoke(t, f.owner, req, contract.ResultStatusSuccess)
		require.Zero(t, sdkReviewReturnedAsset(replay, f.owner.GetWallet().GetAddress(), reviewAssetA))
	})
}

func TestSDKSmartContractsFunding(t *testing.T) {
	base := newTemplateFixtureWithProfiles(t, sdkSmartContractFundingProfiles())
	owner, _ := newWalletManagerForNode(t, base.Network.Core, dkvsClientMnemonic)
	reader, _ := newWalletManagerForNode(t, base.Network.Bootstrap, "")
	runSDKSmartContractsFunding(t, base, owner, reader, false)
}

func sdkSmartContractFundingProfiles() []templateAssetProfile {
	return []templateAssetProfile{
		{Name: "bound", Asset: "ordx:f:sdkreviewbound", BindingSat: 1, Supply: "20000"},
		{Name: "decimal", Asset: "runes:f:SDKREVIEWDECIMAL", Precision: 2, Supply: "20000"},
	}
}

func runSDKSmartContractsFunding(t *testing.T, base *templateFixture, owner, reader *wallet.Manager, gasAlreadyFunded bool) {
	t.Helper()
	const boundAsset = "ordx:f:sdkreviewbound"
	const decimalAsset = "runes:f:SDKREVIEWDECIMAL"
	key := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	require.Equal(t, key.Address, owner.GetWallet().GetAddress())
	if !gasAlreadyFunded {
		splitToDKVSKeyPathActors(t, base, base.gasAnchor, wallet.GetGasAssetName(),
			[]int64{50000000, 50000000}, []int64{1000000, 1000000}, []*dkvsKeyPathActor{key, key})
	}
	script, err := wallet.GetP2TRpkScript(owner.GetWallet().GetPaymentPubKey())
	require.NoError(t, err)
	recipient := &templateActor{PkScript: script}
	for _, profile := range sdkSmartContractFundingProfiles() {
		base.splitProfile(t, base.A, base.assetAnchors[profile.Asset], profile,
			[]string{"20000"}, []int64{100000}, []*templateActor{recipient})
	}
	f := &sdkContractReviewFixture{network: base.Network, owner: owner, reader: reader}
	t.Run("template_bound_asset_funding_preserves_BindingSat_and_carrier", func(t *testing.T) {
		c := wallet.NewContract(wallet.TEMPLATE_CONTRACT_LIMITORDER)
		c.GetContractBase().AssetName = *indexer.NewAssetNameFromString(boundAsset)
		pool := f.deployTemplate(t, contract.TemplateLimitOrder, string(c.Content()), 0, nil)
		req := sdkReviewTemplateCall(t, contract.TemplateLimitOrder, pool, contract.TemplateInvokeAPISwap,
			contract.TemplateLimitOrderInvokeParam{OrderType: contract.OrderTypeSell, AssetName: boundAsset, Amt: "10", UnitPrice: "10"})
		req.Assets = []wallet.ContractFundingAsset{{AssetName: boundAsset, Amount: "10"}}
		tx, _ := f.invoke(t, owner, req, contract.ResultStatusSuccess)
		require.EqualValues(t, 10, sdkReviewAssetAmount(tx, pool, boundAsset))
		for _, output := range tx.TxOut {
			addr, err := wallet.AddrFromPkScript_SatsNet(output.PkScript)
			if err != nil || addr != pool {
				continue
			}
			for _, asset := range output.Assets {
				if asset.Name.String() == boundAsset {
					require.EqualValues(t, 1, asset.BindingSat, "contract-review: SDK discarded bound asset metadata")
					require.GreaterOrEqual(t, output.Value, int64(10), "contract-review: SDK omitted carrier sats")
				}
			}
		}
		require.Equal(t, float64(1), sdkReviewStateField(t, reader, pool, "activeSellCount"))
	})
	t.Run("template_decimal_funding_preserves_exact_fraction", func(t *testing.T) {
		c := wallet.NewContract(wallet.TEMPLATE_CONTRACT_LIMITORDER)
		c.GetContractBase().AssetName = *indexer.NewAssetNameFromString(decimalAsset)
		pool := f.deployTemplate(t, contract.TemplateLimitOrder, string(c.Content()), 0, nil)
		req := sdkReviewTemplateCall(t, contract.TemplateLimitOrder, pool, contract.TemplateInvokeAPISwap,
			contract.TemplateLimitOrderInvokeParam{OrderType: contract.OrderTypeSell, AssetName: decimalAsset, Amt: "1.25", UnitPrice: "100"})
		req.Assets = []wallet.ContractFundingAsset{{AssetName: decimalAsset, Amount: "1.25"}}
		tx, _ := f.invoke(t, owner, req, contract.ResultStatusSuccess)
		found := false
		for _, output := range tx.TxOut {
			addr, err := wallet.AddrFromPkScript_SatsNet(output.PkScript)
			if err != nil || addr != pool {
				continue
			}
			for _, asset := range output.Assets {
				if asset.Name.String() == decimalAsset {
					found = true
					require.Equal(t, "1.25", asset.Amount.String())
				}
			}
		}
		require.True(t, found)
		require.Equal(t, float64(1), sdkReviewStateField(t, reader, pool, "activeSellCount"))
	})
}

type sdkSmartContractSharedSnapshot struct {
	height int32
	tip    string
	assets map[string]string
}

func TestSDKSmartContractsReviewSharedNetworkGroup(t *testing.T) {
	setupDone := e2ePhase(t, "smart_contract_review_shared_network_setup")
	profiles := append([]templateAssetProfile{
		{Name: reviewAssetA, Asset: reviewAssetA, Supply: "20000"},
		{Name: reviewAssetB, Asset: reviewAssetB, Supply: "20000"},
	}, sdkSmartContractFundingProfiles()...)
	base := newTemplateFixtureWithProfilesAndArgsAndLockedValueAndGasSupply(
		t, profiles, nil, nil, nil, 20000000, 200000000)
	// Both gas anchors are mined before Additional. Its Core restart reuses the
	// existing node directory and database; it does not reset this chain state.
	sharedGas := splitSDKSmartContractSharedGas(t, base)

	reviewBase := *base
	reviewBase.gasAnchor = sharedGas
	additional := newSDKContractReviewFixtureForTemplate(t, &reviewBase)

	fundingBase := *base
	fundingBase.gasAnchor = sharedGas
	setupDone()

	before := captureSDKSmartContractSharedSnapshot(t, additional.network, additional.owner)
	passed := t.Run("Additional", func(t *testing.T) {
		runSDKSmartContractsAdditional(t, additional)
	})
	if !passed {
		t.Fatalf("stopped smart-contract shared group after Additional failed; rerun its original isolated root with -run '^TestSDKSmartContractsAdditional$' or rerun the shared case with -run '^TestSDKSmartContractsReviewSharedNetworkGroup$/^Additional$'; rerun the whole fresh group with -run '^TestSDKSmartContractsReviewSharedNetworkGroup$'")
	}
	after := captureSDKSmartContractSharedSnapshot(t, additional.network, additional.owner)
	requireSDKSmartContractSnapshotAdvanced(t, "Additional", before, after)

	before = captureSDKSmartContractSharedSnapshot(t, additional.network, additional.owner)
	passed = t.Run("Funding", func(t *testing.T) {
		runSDKSmartContractsFunding(t, &fundingBase, additional.owner, additional.reader, true)
	})
	if !passed {
		t.Fatalf("stopped smart-contract shared group after Funding failed; rerun its original isolated root with -run '^TestSDKSmartContractsFunding$' or rerun the shared case with -run '^TestSDKSmartContractsReviewSharedNetworkGroup$/^Funding$'; rerun the whole fresh group with -run '^TestSDKSmartContractsReviewSharedNetworkGroup$'")
	}
	after = captureSDKSmartContractSharedSnapshot(t, additional.network, additional.owner)
	requireSDKSmartContractSnapshotAdvanced(t, "Funding", before, after)
}

func newSDKContractReviewFixtureForTemplate(t *testing.T, f *templateFixture) *sdkContractReviewFixture {
	t.Helper()
	owner, _ := newWalletManagerForNode(t, f.Network.Core, dkvsClientMnemonic)
	other, _ := newWalletManagerForNode(t, f.Network.Core, messageTopicMemberMnemonic)
	reader, _ := newWalletManagerForNode(t, f.Network.Bootstrap, "")
	ownerKey := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	otherKey := newDKVSKeyPathActor(t, keyFromMnemonic(t, messageTopicMemberMnemonic, 0))
	var amounts, values []int64
	var actors []*dkvsKeyPathActor
	for i := 0; i < 10; i++ {
		amounts = append(amounts, 10000000)
		values = append(values, 1000000)
		if i < 6 {
			actors = append(actors, ownerKey)
		} else {
			actors = append(actors, otherKey)
		}
	}
	splitToDKVSKeyPathActors(t, f, f.gasAnchor, wallet.GetGasAssetName(), amounts, values, actors)
	for _, asset := range []string{reviewAssetA, reviewAssetB} {
		splitToDKVSKeyPathActors(t, f, f.assetAnchors[asset], asset,
			[]int64{10000, 10000}, []int64{100000, 100000}, []*dkvsKeyPathActor{ownerKey, otherKey})
	}
	require.Equal(t, ownerKey.Address, owner.GetWallet().GetAddress())
	require.Equal(t, otherKey.Address, other.GetWallet().GetAddress())
	return &sdkContractReviewFixture{network: f.Network, owner: owner, other: other, reader: reader}
}

func splitSDKSmartContractSharedGas(t *testing.T, f *templateFixture) *wire.MsgTx {
	t.Helper()
	const (
		reviewGasSupply  = int64(100000000)
		fundingGasSupply = int64(50000000)
	)
	ownerKey := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	outputs := []*wire.TxOut{
		wire.NewTxOut(15000000, txAsset(wallet.GetGasAssetName(), reviewGasSupply), f.A.PkScript),
		wire.NewTxOut(1000000, txAsset(wallet.GetGasAssetName(), fundingGasSupply), ownerKey.PkScript),
		wire.NewTxOut(1000000, txAsset(wallet.GetGasAssetName(), fundingGasSupply), ownerKey.PkScript),
	}
	split := wire.NewMsgTx(2)
	split.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: f.gasAnchor.TxHash(), Index: 0}})
	for _, output := range outputs {
		split.AddTxOut(output)
	}
	signTaprootInputs(t, split, f.A.Key, f.A.RedeemScript, f.A.ControlBlock)
	f.Network.sendAndMine(t, split, 1)
	outpoints := collectSpendableOutPoints(t, split, outputs)
	require.Len(t, outpoints, 3)
	return split
}

func captureSDKSmartContractSharedSnapshot(t *testing.T, network *realSatoshiNet, owner *wallet.Manager) sdkSmartContractSharedSnapshot {
	t.Helper()
	tip, height, err := network.Bootstrap.Client.GetBestBlock()
	require.NoError(t, err)
	assets, err := fetchAssetSummary(network.Core, owner.GetWallet().GetAddress())
	require.NoError(t, err)
	return sdkSmartContractSharedSnapshot{height: height, tip: tip.String(), assets: assets}
}

func requireSDKSmartContractSnapshotAdvanced(t *testing.T, name string,
	before, after sdkSmartContractSharedSnapshot) {
	t.Helper()
	require.Greater(t, after.height, before.height, "%s must advance the shared chain", name)
	require.NotEqual(t, before.tip, after.tip, "%s must change the shared tip", name)
	beforeAssets, err := json.Marshal(before.assets)
	require.NoError(t, err)
	afterAssets, err := json.Marshal(after.assets)
	require.NoError(t, err)
	require.NotEqual(t, string(beforeAssets), string(afterAssets), "%s must change the funded owner's asset state", name)
	t.Logf("smart-contract-shared-case=%s tip=%d:%s->%d:%s assets=%s->%s", name,
		before.height, before.tip, after.height, after.tip, beforeAssets, afterAssets)
}
