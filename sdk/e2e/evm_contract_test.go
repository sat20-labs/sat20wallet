package e2e

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcutil"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestRealSatoshiNetHarnessAscendAndEVMContract(t *testing.T) {
	oldEnableTesting := indexercommon.ENABLE_TESTING
	indexercommon.ENABLE_TESTING = true
	t.Cleanup(func() {
		indexercommon.ENABLE_TESTING = oldEnableTesting
	})

	const (
		lockedUtxo          = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:0"
		lockedValue         = int64(50000)
		ascendedGasAsset    = int64(1000000)
		deployGasLimit      = contractcommon.DeployBaseGas + 300000
		invokeGasLimit      = int64(100000)
		invokeInputGasAsset = int64(100000)
	)
	gasAsset := wallet.GetGasAssetName()
	bootstrapKey := keyFromMnemonic(t, bootstrapMnemonic, 0)
	coreKey := keyFromMnemonic(t, coreMnemonic, 0)
	callerKeys := []*btcec.PrivateKey{
		keyFromMnemonic(t, bootstrapMnemonic, 1),
		keyFromMnemonic(t, bootstrapMnemonic, 2),
		keyFromMnemonic(t, bootstrapMnemonic, 3),
	}
	require.Equal(t, indexercommon.GetBootstrapPubKey(),
		hex.EncodeToString(bootstrapKey.PubKey().SerializeCompressed()))

	witnessScript, lockedPkScript, err := getP2WSHScript(
		bootstrapKey.PubKey().SerializeCompressed(),
		coreKey.PubKey().SerializeCompressed(),
	)
	require.NoError(t, err)

	fakeL1 := newFakeL1Indexer(t, hex.EncodeToString(bootstrapKey.PubKey().SerializeCompressed()), lockedPkScript,
		[]fakeL1Asset{{
			Utxo:  lockedUtxo,
			Value: lockedValue,
			Assets: map[string]string{
				gasAsset: fmt.Sprintf("%d", ascendedGasAsset),
			},
		}})
	network := newRealSatoshiNet(t, fakeL1)
	spendScript, spendAddress, redeemScript, controlBlock := callerTaprootScript(t, callerKeys[0])

	anchorTx := buildAnchorTx(t, lockedUtxo, lockedValue,
		txAsset(gasAsset, ascendedGasAsset), fmt.Sprintf("%s-%d-0-1", gasAsset, ascendedGasAsset),
		witnessScript, bootstrapKey, spendScript)
	network.sendAndMine(t, anchorTx, 1)

	artifact, err := CompileSolidityFile(filepath.Join("testdata", "contracts", "Counter.sol"), "Counter", SolidityCompileOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, artifact.ABI)
	require.NotEmpty(t, artifact.Bytecode)
	deployTx, invokeTxs, contract := buildCounterContractTxs(t, anchorTx, callerKeys,
		spendScript, spendAddress, redeemScript, controlBlock,
		ascendedGasAsset, deployGasLimit, invokeInputGasAsset, invokeGasLimit,
		artifact.Bytecode, SolidityFunctionSelector("inc()"))
	network.sendAndMine(t, deployTx, 2)
	for _, invokeTx := range invokeTxs {
		hash, err := network.Bootstrap.Client.SendRawTransaction(invokeTx, true)
		require.NoError(t, err)
		require.Equal(t, invokeTx.TxHash(), *hash)
	}
	network.waitForTx(t, invokeTxs[len(invokeTxs)-1], 3)

	bestHash, bestHeight, err := network.Bootstrap.Client.GetBestBlock()
	require.NoError(t, err)
	require.GreaterOrEqual(t, bestHeight, int32(3))
	for _, node := range network.Nodes[1:] {
		nodeHash, nodeHeight, err := node.Client.GetBestBlock()
		require.NoError(t, err)
		require.Equal(t, bestHeight, nodeHeight)
		require.Equal(t, bestHash, nodeHash)
	}

	deployHash := deployTx.TxHash()
	deployVerbose, err := network.Bootstrap.Client.GetRawTransactionVerbose(&deployHash)
	require.NoError(t, err)
	require.GreaterOrEqual(t, deployVerbose.Confirmations, uint64(1))
	for _, invokeTx := range invokeTxs {
		invokeHash := invokeTx.TxHash()
		invokeVerbose, err := network.Bootstrap.Client.GetRawTransactionVerbose(&invokeHash)
		require.NoError(t, err)
		require.GreaterOrEqual(t, invokeVerbose.Confirmations, uint64(1))
	}

	block, err := network.Bootstrap.Client.GetBlock(bestHash)
	require.NoError(t, err)
	root, found, err := findEVMStateRoot(block.Transactions[0])
	require.NoError(t, err)
	require.True(t, found)
	require.NotEqual(t, [32]byte{}, root)
	require.Equal(t, contractcommon.TestnetContractPrefix, contract.Prefix())
}

func TestRealSatoshiNetEVMSignedAssetVault(t *testing.T) {
	f := newTemplateFixture(t, map[string]int64{"ordx:f:e2evault": 1000})
	const (
		vaultAsset       = "ordx:f:e2evault"
		unitAmount       = uint64(25)
		deployGasLimit   = int64(10000000)
		withdrawGasLimit = int64(1000000)
	)
	gas := wallet.GetGasAssetName()

	gasOuts := f.split(t, f.A, f.gasAnchor, gas,
		[]int64{2000000, 30000, 2000000, 2000000, 2000000}, []int64{1000, 1000, 1000, 1000, 1000},
		[]*templateActor{f.A, f.A, f.B, f.B, f.B})
	assetOuts := f.split(t, f.A, f.assetAnchors[vaultAsset], vaultAsset,
		[]int64{500, 500}, []int64{1000, 1000},
		[]*templateActor{f.A, f.A})

	artifact, err := CompileSolidityFile(filepath.Join("testdata", "contracts", "SignedAssetVault.sol"),
		"SignedAssetVault", SolidityCompileOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, artifact.Bytecode)

	deployerEVM, err := evmEthAddressFromPrivateKey(f.A.Key)
	require.NoError(t, err)
	initCode := appendSolidityConstructorArgs(artifact.Bytecode,
		evmABIString(vaultAsset),
		evmABIUint(unitAmount),
		evmABIAddress(deployerEVM),
	)

	caller := evmAddressFromAddressString(f.A.Address)
	contract, err := contractcommon.DeriveEVMCreateContractAddress(contractcommon.TestnetContractPrefix, caller, 1)
	require.NoError(t, err)
	deployPlan, err := EVMDeployGasPlan(deployGasLimit, 2, 2000000)
	require.NoError(t, err)
	deployChange := wire.NewTxOut(0, txAsset(gas, deployPlan.ChangeGasAsset), f.A.PkScript)
	deployTx, builtContract, err := contractcommon.BuildDeployTx(contractcommon.DeployTxBuildRequest{
		ContractPrefix:  contractcommon.TestnetContractPrefix,
		Type:            contractcommon.ContractTypeEVM,
		Deployer:        caller.String(),
		GasLimit:        deployPlan.GasLimit,
		DeployNonce:     1,
		ContractContent: initCode,
		Funding: wire.TxOut{
			Value:  1000,
			Assets: txAsset(gas, deployPlan.ContractFundingGasAsset),
		},
		Inputs:       []wire.OutPoint{gasOuts[0]},
		ExtraOutputs: []*wire.TxOut{deployChange},
	})
	require.NoError(t, err)
	require.True(t, contract.Equal(builtContract))
	signTaprootInputs(t, deployTx, f.A.Key, f.A.RedeemScript, f.A.ControlBlock)
	f.Network.sendAndMine(t, deployTx, 0)

	fundTx := buildEVMDefaultInvokeFor(t, f.A, contract, []wire.OutPoint{assetOuts[0]}, wire.TxOut{
		Value:  0,
		Assets: txAssets(templateFunding(t, vaultAsset, 500)),
	})
	require.False(t, txHasContractOpReturn(fundTx))
	f.Network.sendAndMine(t, fundTx, 0)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, contract.MustEncode(), vaultAsset, "500")

	firstKey := int64(1717300000000000)
	secondKey := int64(1717300000000001)
	firstWithdraw := buildSignedVaultWithdrawTx(t, f.B, contract, f.B.Address, f.A.Key,
		firstKey, 2, 1, withdrawGasLimit, 4, 2000000, gasOuts[2])
	f.Network.sendAndMine(t, firstWithdraw, 0)
	requireAssetSummaryAtLeast(t, f.Network.Bootstrap, f.B.Address, vaultAsset, "50")

	replayWithdraw := buildSignedVaultWithdrawTx(t, f.B, contract, f.B.Address, f.A.Key,
		firstKey, 2, 2, withdrawGasLimit, 6, 2000000, gasOuts[3])
	f.Network.sendAndMine(t, replayWithdraw, 0)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, f.B.Address, vaultAsset, "50")
	requireAssetSummaryAmount(t, f.Network.Bootstrap, contract.MustEncode(), vaultAsset, "450")

	secondWithdraw := buildSignedVaultWithdrawTx(t, f.B, contract, f.B.Address, f.A.Key,
		secondKey, 3, 3, withdrawGasLimit, 8, 2000000, gasOuts[4])
	f.Network.sendAndMine(t, secondWithdraw, 0)
	requireAssetSummaryAtLeast(t, f.Network.Bootstrap, f.B.Address, vaultAsset, "125")
	requireAssetSummaryAmount(t, f.Network.Bootstrap, contract.MustEncode(), vaultAsset, "375")
	f.requireSynced(t)
}

func TestRealSatoshiNetEVMAssetApps(t *testing.T) {
	const (
		appAsset       = "ordx:f:e2eapps"
		deployGasLimit = int64(10_000_000)
		invokeGasLimit = int64(1_000_000)
		closeGasLimit  = int64(1_800_000)
		inputGasAsset  = int64(2_000_000)
	)
	f := newTemplateFixture(t, map[string]int64{appAsset: 5_000})
	gas := wallet.GetGasAssetName()

	gasOuts := f.split(t, f.A, f.gasAnchor, gas,
		[]int64{
			inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset,
			inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset,
			inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset,
			inputGasAsset,
		},
		[]int64{1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000},
		[]*templateActor{f.A, f.A, f.A, f.A, f.A, f.A, f.B, f.B, f.B, f.C, f.C, f.A, f.A, f.A, f.A, f.A, f.B, f.B, f.C})
	assetOuts := f.split(t, f.A, f.assetAnchors[appAsset], appAsset,
		[]int64{1000, 300, 400, 1000, 100}, []int64{1000, 1000, 1000, 1000, 1000},
		[]*templateActor{f.A, f.B, f.C, f.A, f.B})

	source := filepath.Join("testdata", "contracts", "AssetApps.sol")
	ownerEVM := evmAddressFromAddressString(f.A.Address)

	escrowArtifact, err := CompileSolidityFile(source, "Escrow", SolidityCompileOptions{})
	require.NoError(t, err)
	escrowInit := appendSolidityConstructorArgs(escrowArtifact.Bytecode,
		evmABIString(appAsset),
		evmABIAddress(ownerEVM),
		evmABIString(f.A.Address),
	)
	escrowDeploy, escrow, _ := buildEVMDeployTxFor(t, f.A, escrowInit, 101, deployGasLimit, 2, inputGasAsset, gasOuts[0])
	f.Network.sendAndMine(t, escrowDeploy, 0)
	requireEVMContractMetadata(t, f.Network.Bootstrap, escrow.MustEncode(), "escrow", []string{appAsset})
	escrowFund := buildEVMDefaultInvokeFor(t, f.A, escrow, []wire.OutPoint{assetOuts[0]}, wire.TxOut{
		Assets: txAssets(templateFunding(t, appAsset, 1000)),
	})
	f.Network.sendAndMine(t, escrowFund, 0)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, escrow.MustEncode(), appAsset, "1000")

	escrowRelease, _ := buildEVMInvokeTxFor(t, f.A, escrow, "call",
		solidityCall("release(string,uint256)", evmABIString(f.E.Address), evmABIUint(250)),
		1, invokeGasLimit, 4, inputGasAsset, gasOuts[1])
	f.Network.sendAndMine(t, escrowRelease, 0)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, f.E.Address, appAsset, "250")
	requireAssetSummaryAmount(t, f.Network.Bootstrap, escrow.MustEncode(), appAsset, "750")

	escrowReject, _ := buildEVMInvokeTxFor(t, f.B, escrow, "call",
		solidityCall("release(string,uint256)", evmABIString(f.E.Address), evmABIUint(100)),
		1, invokeGasLimit, 5, inputGasAsset, gasOuts[6])
	f.Network.sendAndMine(t, escrowReject, 0)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, f.E.Address, appAsset, "250")
	requireAssetSummaryAmount(t, f.Network.Bootstrap, escrow.MustEncode(), appAsset, "750")

	escrowOverRelease, _ := buildEVMInvokeTxFor(t, f.A, escrow, "call",
		solidityCall("release(string,uint256)", evmABIString(f.E.Address), evmABIUint(800)),
		2, invokeGasLimit, 6, inputGasAsset, gasOuts[15])
	f.Network.sendAndMine(t, escrowOverRelease, 0)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, f.E.Address, appAsset, "250")
	requireAssetSummaryAmount(t, f.Network.Bootstrap, escrow.MustEncode(), appAsset, "750")

	escrowClose, _ := buildEVMCloseTxFor(t, f.A, escrow, 3, closeGasLimit, 7, inputGasAsset, gasOuts[11])
	f.Network.sendAndMine(t, escrowClose, 0)
	requireAssetSummaryZero(t, f.Network.Bootstrap, escrow.MustEncode(), appAsset)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, f.A.Address, appAsset, "1750")

	crowdArtifact, err := CompileSolidityFile(source, "Crowdfund", SolidityCompileOptions{})
	require.NoError(t, err)
	crowdInit := appendSolidityConstructorArgs(crowdArtifact.Bytecode,
		evmABIString(appAsset),
		evmABIUint(700),
		evmABIAddress(ownerEVM),
	)
	crowdDeploy, crowd, _ := buildEVMDeployTxFor(t, f.A, crowdInit, 102, deployGasLimit, 8, inputGasAsset, gasOuts[2])
	f.Network.sendAndMine(t, crowdDeploy, 0)
	requireEVMContractMetadata(t, f.Network.Bootstrap, crowd.MustEncode(), "crowdfund", []string{appAsset})
	crowdPledgeB, _ := buildEVMInvokeTxWithFundingFor(t, f.B, crowd, "call",
		solidityCall("pledge()"), 1, invokeGasLimit, 10,
		inputGasAsset, gasOuts[7], []wire.OutPoint{assetOuts[1]}, 0,
		txAssets(templateFunding(t, appAsset, 300)))
	f.Network.sendAndMine(t, crowdPledgeB, 0)

	crowdPledgeC, _ := buildEVMInvokeTxWithFundingFor(t, f.C, crowd, "call",
		solidityCall("pledge()"), 1, invokeGasLimit, 12,
		inputGasAsset, gasOuts[9], []wire.OutPoint{assetOuts[2]}, 0,
		txAssets(templateFunding(t, appAsset, 400)))
	f.Network.sendAndMine(t, crowdPledgeC, 0)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, crowd.MustEncode(), appAsset, "700")

	crowdClaim, _ := buildEVMInvokeTxFor(t, f.A, crowd, "call",
		solidityCall("claim(string)", evmABIString(f.D.Address)),
		1, invokeGasLimit, 13, inputGasAsset, gasOuts[3])
	f.Network.sendAndMine(t, crowdClaim, 0)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, f.D.Address, appAsset, "700")
	requireAssetSummaryZero(t, f.Network.Bootstrap, crowd.MustEncode(), appAsset)

	crowdClose, _ := buildEVMCloseTxFor(t, f.A, crowd, 2, closeGasLimit, 14, inputGasAsset, gasOuts[12])
	f.Network.sendAndMine(t, crowdClose, 0)
	requireAssetSummaryZero(t, f.Network.Bootstrap, crowd.MustEncode(), appAsset)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, f.D.Address, appAsset, "700")

	f.requireSynced(t)
}

func TestRealSatoshiNetEVMStandardApps(t *testing.T) {
	const (
		appAsset       = "ordx:f:e2estandard"
		deployGasLimit = int64(10_000_000)
		invokeGasLimit = int64(1_000_000)
		closeGasLimit  = int64(1_800_000)
		inputGasAsset  = int64(2_000_000)
	)
	f := newTemplateFixture(t, map[string]int64{appAsset: 20_000})
	gas := wallet.GetGasAssetName()
	gasOuts := f.split(t, f.A, f.gasAnchor, gas,
		[]int64{
			inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset,
			inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset,
			inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset,
			inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset,
			inputGasAsset,
		},
		[]int64{1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000},
		[]*templateActor{f.A, f.A, f.B, f.B, f.A, f.A, f.A, f.A, f.B, f.A, f.C, f.C, f.B, f.C, f.A, f.B, f.A})
	assetOuts := f.split(t, f.A, f.assetAnchors[appAsset], appAsset,
		[]int64{1000, 100, 600, 50}, []int64{1000, 1000, 1000, 1000},
		[]*templateActor{f.A, f.B, f.A, f.B})

	source := filepath.Join("testdata", "contracts", "StandardApps.sol")
	ammArtifact, err := CompileSolidityFile(source, "ConstantProductAMM", SolidityCompileOptions{Timeout: 2 * time.Minute})
	require.NoError(t, err)
	ammInit := appendSolidityConstructorArgs(ammArtifact.Bytecode, evmABIString(appAsset))
	ammDeploy, amm, _ := buildEVMDeployTxFor(t, f.A, ammInit, 301, deployGasLimit, 2, inputGasAsset, gasOuts[0])
	f.Network.sendAndMine(t, ammDeploy, 0)
	requireEVMContractMetadata(t, f.Network.Bootstrap, amm.MustEncode(), "amm", []string{appAsset, "::"})

	addLiquidity, _ := buildEVMInvokeTxWithFundingFor(t, f.A, amm, "call",
		solidityCall("addLiquidity(uint256)", evmABIUint(1000)),
		1, invokeGasLimit, 4, inputGasAsset, gasOuts[1], []wire.OutPoint{assetOuts[0]}, 1000,
		txAssets(templateFunding(t, appAsset, 1000)))
	f.Network.sendAndMine(t, addLiquidity, 0)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, amm.MustEncode(), appAsset, "1000")

	swapSatForAsset, _ := buildEVMInvokeTxWithFundingFor(t, f.B, amm, "call",
		solidityCall("swapSatForAsset(string)", evmABIString("90")),
		1, invokeGasLimit, 6, inputGasAsset, gasOuts[2], nil, 100, nil)
	swapSatBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{swapSatForAsset}, 0)
	swapSatResult := requireSingleResultTx(t, swapSatBlock)
	requireTxOutputAssetAmount(t, swapSatResult, f.B.Address, appAsset, "90")
	requireAssetSummaryAmount(t, f.Network.Bootstrap, amm.MustEncode(), appAsset, "910")

	swapAssetForSat, _ := buildEVMInvokeTxWithFundingFor(t, f.B, amm, "call",
		solidityCall("swapAssetForSat(uint256)", evmABIUint(108)),
		2, invokeGasLimit, 8, inputGasAsset, gasOuts[3], []wire.OutPoint{assetOuts[1]}, 0,
		txAssets(templateFunding(t, appAsset, 100)))
	swapAssetBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{swapAssetForSat}, 0)
	swapAssetResult := requireSingleResultTx(t, swapAssetBlock)
	requireTxOutputValueAmount(t, swapAssetResult, f.B.Address, 108)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, amm.MustEncode(), appAsset, "1010")

	removeLiquidity, _ := buildEVMInvokeTxFor(t, f.A, amm, "call",
		solidityCall("removeLiquidity(uint256,string,uint256)", evmABIUint(100), evmABIString("100"), evmABIUint(99)),
		2, invokeGasLimit, 10, inputGasAsset, gasOuts[4])
	removeBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{removeLiquidity}, 0)
	removeResult := requireSingleResultTx(t, removeBlock)
	requireTxOutputAssetAmount(t, removeResult, f.A.Address, appAsset, "101")
	requireTxOutputValueAmount(t, removeResult, f.A.Address, 99)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, amm.MustEncode(), appAsset, "909")

	closeAMM, _ := buildEVMCloseTxFor(t, f.A, amm, 3, closeGasLimit, 11, inputGasAsset, gasOuts[14])
	closeAMMBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{closeAMM}, 0)
	closeAMMResult := requireSingleResultTx(t, closeAMMBlock)
	requireTxOutputAssetAmount(t, closeAMMResult, f.A.Address, appAsset, "909")
	requireTxOutputValueAtLeast(t, closeAMMResult, f.A.Address, 893)
	requireAssetSummaryZero(t, f.Network.Bootstrap, amm.MustEncode(), appAsset)

	orderArtifact, err := CompileSolidityFile(source, "LimitOrderBook", SolidityCompileOptions{Timeout: 2 * time.Minute})
	require.NoError(t, err)
	orderInit := appendSolidityConstructorArgs(orderArtifact.Bytecode, evmABIString(appAsset))
	orderDeploy, orderBook, _ := buildEVMDeployTxFor(t, f.A, orderInit, 302, deployGasLimit, 12, inputGasAsset, gasOuts[5])
	f.Network.sendAndMine(t, orderDeploy, 0)
	requireEVMContractMetadata(t, f.Network.Bootstrap, orderBook.MustEncode(), "limitorder", []string{appAsset, "::"})
	requireEVMLimitOrderView(t, f.Network.Bootstrap, orderBook.MustEncode(), "1", nil)

	createSellAssetOrder, _ := buildEVMInvokeTxWithFundingFor(t, f.A, orderBook, "call",
		solidityCall("createOrder(string,string,string)", evmABIString(appAsset), evmABIString("::"), evmABIString("300")),
		1, invokeGasLimit, 14, inputGasAsset, gasOuts[6], []wire.OutPoint{assetOuts[2]}, 0,
		txAssets(templateFunding(t, appAsset, 600)))
	f.Network.sendAndMine(t, createSellAssetOrder, 0)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, orderBook.MustEncode(), appAsset, "600")
	requireEVMLimitOrderView(t, f.Network.Bootstrap, orderBook.MustEncode(), "2", []evmLimitOrderViewRow{{
		OrderID:       "1",
		Maker:         f.A.Address,
		SellAsset:     appAsset,
		BuyAsset:      "::",
		SellRemaining: "600",
		BuyRemaining:  "300",
	}})

	fillSellAssetOrder, _ := buildEVMInvokeTxWithFundingFor(t, f.B, orderBook, "call",
		solidityCall("fillOrder(uint256)", evmABIUint(1)),
		1, invokeGasLimit, 16, inputGasAsset, gasOuts[8], nil, 100, nil)
	fillSellAssetBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{fillSellAssetOrder}, 0)
	fillSellAssetResult := requireSingleResultTx(t, fillSellAssetBlock)
	requireTxOutputAssetAmount(t, fillSellAssetResult, f.B.Address, appAsset, "200")
	requireTxOutputValueAmount(t, fillSellAssetResult, f.A.Address, 100)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, orderBook.MustEncode(), appAsset, "400")
	requireEVMLimitOrderView(t, f.Network.Bootstrap, orderBook.MustEncode(), "2", []evmLimitOrderViewRow{{
		OrderID:       "1",
		Maker:         f.A.Address,
		SellAsset:     appAsset,
		BuyAsset:      "::",
		SellRemaining: "400",
		BuyRemaining:  "200",
	}})

	cancelSellAssetOrder, _ := buildEVMInvokeTxFor(t, f.A, orderBook, "call",
		solidityCall("cancelOrder(uint256)", evmABIUint(1)),
		2, invokeGasLimit, 18, inputGasAsset, gasOuts[9])
	cancelSellAssetBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{cancelSellAssetOrder}, 0)
	cancelSellAssetResult := requireSingleResultTx(t, cancelSellAssetBlock)
	requireTxOutputAssetAmount(t, cancelSellAssetResult, f.A.Address, appAsset, "400")
	requireAssetSummaryZero(t, f.Network.Bootstrap, orderBook.MustEncode(), appAsset)
	requireEVMLimitOrderView(t, f.Network.Bootstrap, orderBook.MustEncode(), "2", nil)

	createSellSatOrder, _ := buildEVMInvokeTxWithFundingFor(t, f.C, orderBook, "call",
		solidityCall("createOrder(string,string,string)", evmABIString("::"), evmABIString(appAsset), evmABIString("150")),
		1, invokeGasLimit, 20, inputGasAsset, gasOuts[10], nil, 300, nil)
	f.Network.sendAndMine(t, createSellSatOrder, 0)
	requireEVMLimitOrderView(t, f.Network.Bootstrap, orderBook.MustEncode(), "3", []evmLimitOrderViewRow{{
		OrderID:       "2",
		Maker:         f.C.Address,
		SellAsset:     "::",
		BuyAsset:      appAsset,
		SellRemaining: "300",
		BuyRemaining:  "150",
	}})

	fillSellSatOrder, _ := buildEVMInvokeTxWithFundingFor(t, f.B, orderBook, "call",
		solidityCall("fillOrder(uint256)", evmABIUint(2)),
		1, invokeGasLimit, 22, inputGasAsset, gasOuts[12], []wire.OutPoint{assetOuts[3]}, 0,
		txAssets(templateFunding(t, appAsset, 50)))
	fillSellSatBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{fillSellSatOrder}, 0)
	fillSellSatResult := requireSingleResultTx(t, fillSellSatBlock)
	requireTxOutputValueAmount(t, fillSellSatResult, f.B.Address, 100)
	requireTxOutputAssetAmount(t, fillSellSatResult, f.C.Address, appAsset, "50")
	requireEVMLimitOrderView(t, f.Network.Bootstrap, orderBook.MustEncode(), "3", []evmLimitOrderViewRow{{
		OrderID:       "2",
		Maker:         f.C.Address,
		SellAsset:     "::",
		BuyAsset:      appAsset,
		SellRemaining: "200",
		BuyRemaining:  "100",
	}})

	cancelSellSatOrder, _ := buildEVMInvokeTxFor(t, f.C, orderBook, "call",
		solidityCall("cancelOrder(uint256)", evmABIUint(2)),
		2, invokeGasLimit, 24, inputGasAsset, gasOuts[13])
	cancelSellSatBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{cancelSellSatOrder}, 0)
	cancelSellSatResult := requireSingleResultTx(t, cancelSellSatBlock)
	requireTxOutputValueAmount(t, cancelSellSatResult, f.C.Address, 200)
	requireEVMLimitOrderView(t, f.Network.Bootstrap, orderBook.MustEncode(), "3", nil)

	createCloseRefundOrder, _ := buildEVMInvokeTxWithFundingFor(t, f.B, orderBook, "call",
		solidityCall("createOrder(string,string,string)", evmABIString("::"), evmABIString(appAsset), evmABIString("25")),
		2, invokeGasLimit, 26, inputGasAsset, gasOuts[15], nil, 50, nil)
	f.Network.sendAndMine(t, createCloseRefundOrder, 0)
	requireEVMLimitOrderView(t, f.Network.Bootstrap, orderBook.MustEncode(), "4", []evmLimitOrderViewRow{{
		OrderID:       "3",
		Maker:         f.B.Address,
		SellAsset:     "::",
		BuyAsset:      appAsset,
		SellRemaining: "50",
		BuyRemaining:  "25",
	}})

	closeOrderBook, _ := buildEVMCloseTxFor(t, f.A, orderBook, 3, closeGasLimit, 28, inputGasAsset, gasOuts[16])
	closeOrderBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{closeOrderBook}, 0)
	closeOrderResult := requireSingleResultTx(t, closeOrderBlock)
	requireTxOutputValueAmount(t, closeOrderResult, f.B.Address, 50)
	f.requireSynced(t)
}

func TestRealSatoshiNetEVMAMMGasAsset(t *testing.T) {
	const (
		deployGasLimit = int64(10_000_000)
		invokeGasLimit = int64(1_000_000)
		closeGasLimit  = int64(1_800_000)
		inputGasAsset  = int64(2_100_000)
	)
	f := newTemplateFixture(t, nil)
	gas := wallet.GetGasAssetName()
	gasOuts := f.split(t, f.A, f.gasAnchor, gas,
		[]int64{inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset},
		[]int64{1000, 1000, 1000, 1000, 1000, 1000},
		[]*templateActor{f.A, f.A, f.B, f.C, f.B, f.A})

	source := filepath.Join("testdata", "contracts", "StandardApps.sol")
	ammArtifact, err := CompileSolidityFile(source, "ConstantProductAMM", SolidityCompileOptions{})
	require.NoError(t, err)
	ammInit := appendSolidityConstructorArgs(ammArtifact.Bytecode, evmABIString(gas))
	ammDeploy, amm, _ := buildEVMDeployTxFor(t, f.A, ammInit, 201, deployGasLimit, 2, inputGasAsset, gasOuts[0])
	f.Network.sendAndMine(t, ammDeploy, 0)

	addLiquidity, _ := buildEVMInvokeTxWithFundingFor(t, f.A, amm, "call",
		solidityCall("addLiquidity(uint256)", evmABIUint(900)),
		1, invokeGasLimit, 4, inputGasAsset, gasOuts[1], nil, 900, txAsset(gas, 1000))
	f.Network.sendAndMine(t, addLiquidity, 0)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, amm.MustEncode(), gas, "1000")

	swapSatForGas, _ := buildEVMInvokeTxWithFundingFor(t, f.B, amm, "call",
		solidityCall("swapSatForAsset(string)", evmABIString("80")),
		1, invokeGasLimit, 6, inputGasAsset, gasOuts[2], nil, 90, nil)
	swapSatBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{swapSatForGas}, 0)
	swapSatResult := requireSingleResultTx(t, swapSatBlock)
	requireTxOutputAssetPositive(t, swapSatResult, f.B.Address, gas)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, amm.MustEncode(), gas, "910")

	swapGasForSat, _ := buildEVMInvokeTxWithFundingFor(t, f.C, amm, "call",
		solidityCall("swapAssetForSat(uint256)", evmABIUint(70)),
		1, invokeGasLimit, 8, inputGasAsset, gasOuts[3], nil, 0, txAsset(gas, 100))
	swapGasBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{swapGasForSat}, 0)
	swapGasResult := requireSingleResultTx(t, swapGasBlock)
	requireTxOutputValueAtLeast(t, swapGasResult, f.C.Address, 70)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, amm.MustEncode(), gas, "1010")

	rejectedSwap, _ := buildEVMInvokeTxWithFundingFor(t, f.B, amm, "call",
		solidityCall("swapAssetForSat(uint256)", evmABIUint(1000000)),
		2, invokeGasLimit, 9, inputGasAsset, gasOuts[4], nil, 0, txAsset(gas, 50))
	rejectedBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{rejectedSwap}, 0)
	rejectedResults := contractResultTxs(rejectedBlock)
	require.Len(t, rejectedResults, 1)
	rejectedPayload := requireResultPayload(t, rejectedResults[0])
	require.NotEqual(t, contractcommon.ResultStatusSuccess, rejectedPayload.Status)
	rejectedResult := rejectedResults[0]
	requireTxOutputAssetPositive(t, rejectedResult, f.B.Address, gas)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, amm.MustEncode(), gas, "1010")

	closeTx, _ := buildEVMCloseTxFor(t, f.A, amm, 2, closeGasLimit, 10, inputGasAsset, gasOuts[5])
	closeBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{closeTx}, 0)
	closeResult := requireSingleResultTx(t, closeBlock)
	requireTxOutputAssetPositive(t, closeResult, f.A.Address, gas)
	requireAssetSummaryZero(t, f.Network.Bootstrap, amm.MustEncode(), gas)
	f.requireSynced(t)
}

func TestRealSatoshiNetEVMInternalERC20(t *testing.T) {
	const (
		deployGasLimit = int64(10_000_000)
		invokeGasLimit = int64(1_000_000)
		inputGasAsset  = int64(2_000_000)
	)
	f := newTemplateFixture(t, nil)
	gas := wallet.GetGasAssetName()
	gasOuts := f.split(t, f.A, f.gasAnchor, gas,
		[]int64{
			inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset,
			inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset, inputGasAsset,
			inputGasAsset,
		},
		[]int64{1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000},
		[]*templateActor{f.A, f.A, f.A, f.A, f.A, f.A, f.A, f.A, f.B, f.B, f.B, f.B, f.A})

	source := filepath.Join("testdata", "contracts", "AssetApps.sol")
	artifact, err := CompileSolidityFile(source, "InternalERC20", SolidityCompileOptions{})
	require.NoError(t, err)
	initCode := appendSolidityConstructorArgs(artifact.Bytecode,
		evmABIString("Internal Demo Token"),
		evmABIString("IDEMO"),
		evmABIUint(8),
	)
	deployTx, token, _ := buildEVMDeployTxFor(t, f.A, initCode, 201, deployGasLimit, 2, inputGasAsset, gasOuts[0])
	f.Network.sendAndMine(t, deployTx, 0)
	requireEVMContractMetadata(t, f.Network.Bootstrap, token.MustEncode(), "erc20", nil)

	defaultDeposit := buildEVMValueDepositFor(t, f.A, token, gasOuts[1], 10, inputGasAsset)
	f.Network.sendAndMine(t, defaultDeposit, 0)

	owner := evmAddressFromAddressString(f.A.Address)
	spender := evmAddressFromAddressString(f.B.Address)
	recipient := evmAddressFromAddressString(f.C.Address)

	mintTx, _ := buildEVMInvokeTxFor(t, f.A, token, "call",
		solidityCall("mint(address,uint256)", evmABIAddress(owner), evmABIUint(1000)),
		1, invokeGasLimit, 4, inputGasAsset, gasOuts[2])
	f.Network.sendAndMine(t, mintTx, 0)
	assertOwner1000, _ := buildEVMInvokeTxFor(t, f.A, token, "call",
		solidityCall("assertBalance(address,uint256)", evmABIAddress(owner), evmABIUint(1000)),
		2, invokeGasLimit, 5, inputGasAsset, gasOuts[3])
	f.Network.sendAndMine(t, assertOwner1000, 0)

	transferTx, _ := buildEVMInvokeTxFor(t, f.A, token, "call",
		solidityCall("transfer(address,uint256)", evmABIAddress(spender), evmABIUint(250)),
		3, invokeGasLimit, 6, inputGasAsset, gasOuts[4])
	f.Network.sendAndMine(t, transferTx, 0)
	assertSpender250, _ := buildEVMInvokeTxFor(t, f.B, token, "call",
		solidityCall("assertBalance(address,uint256)", evmABIAddress(spender), evmABIUint(250)),
		1, invokeGasLimit, 7, inputGasAsset, gasOuts[8])
	f.Network.sendAndMine(t, assertSpender250, 0)

	approveTx, _ := buildEVMInvokeTxFor(t, f.A, token, "call",
		solidityCall("approve(address,uint256)", evmABIAddress(spender), evmABIUint(100)),
		4, invokeGasLimit, 8, inputGasAsset, gasOuts[5])
	f.Network.sendAndMine(t, approveTx, 0)
	transferFromTx, _ := buildEVMInvokeTxFor(t, f.B, token, "call",
		solidityCall("transferFrom(address,address,uint256)", evmABIAddress(owner), evmABIAddress(recipient), evmABIUint(80)),
		2, invokeGasLimit, 9, inputGasAsset, gasOuts[9])
	f.Network.sendAndMine(t, transferFromTx, 0)
	assertAllowance20, _ := buildEVMInvokeTxFor(t, f.B, token, "call",
		solidityCall("assertAllowance(address,address,uint256)", evmABIAddress(owner), evmABIAddress(spender), evmABIUint(20)),
		3, invokeGasLimit, 10, inputGasAsset, gasOuts[10])
	f.Network.sendAndMine(t, assertAllowance20, 0)

	burnTx, _ := buildEVMInvokeTxFor(t, f.A, token, "call",
		solidityCall("burn(uint256)", evmABIUint(50)),
		5, invokeGasLimit, 11, inputGasAsset, gasOuts[6])
	f.Network.sendAndMine(t, burnTx, 0)
	assertFinalOwner, _ := buildEVMInvokeTxFor(t, f.A, token, "call",
		solidityCall("assertBalance(address,uint256)", evmABIAddress(owner), evmABIUint(620)),
		6, invokeGasLimit, 12, inputGasAsset, gasOuts[7])
	f.Network.sendAndMine(t, assertFinalOwner, 0)
	assertRecipient80, _ := buildEVMInvokeTxFor(t, f.B, token, "call",
		solidityCall("assertBalance(address,uint256)", evmABIAddress(recipient), evmABIUint(80)),
		4, invokeGasLimit, 13, inputGasAsset, gasOuts[11])
	f.Network.sendAndMine(t, assertRecipient80, 0)

	closeTx, _ := buildEVMCloseTxFor(t, f.A, token, 7, invokeGasLimit, 14, inputGasAsset, gasOuts[12])
	closeBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{closeTx}, 0)
	closeResult := requireSingleResultTx(t, closeBlock)
	requireTxOutputValueAtLeast(t, closeResult, f.A.Address, 6)
	f.requireSynced(t)
}

func buildCounterContractTxs(t *testing.T, anchorTx *wire.MsgTx,
	callerKeys []*btcec.PrivateKey, spendScript []byte, spendAddress string,
	redeemScript, controlBlock []byte, deployInputGasAsset int64, deployGasLimit int64,
	invokeInputGasAsset int64, invokeGasLimit int64, initCode, invokeCalldata []byte) (*wire.MsgTx, []*wire.MsgTx, contractcommon.ContractAddress) {

	t.Helper()
	require.Len(t, callerKeys, 3)
	require.NotEmpty(t, initCode)
	require.NotEmpty(t, invokeCalldata)
	const deployNonce = uint64(1)
	caller := evmAddressFromAddressString(spendAddress)
	contract, err := contractcommon.DeriveEVMCreateContractAddress(contractcommon.TestnetContractPrefix, caller, deployNonce)
	require.NoError(t, err)
	deployPlan, err := EVMDeployGasPlan(deployGasLimit, 2, deployInputGasAsset)
	require.NoError(t, err)
	invokePlans := make([]EVMGasPlan, 0, len(callerKeys))
	for i := range callerKeys {
		plan, err := EVMInvokeGasPlan(invokeGasLimit, uint64(3+i), true, invokeInputGasAsset)
		require.NoError(t, err)
		invokePlans = append(invokePlans, plan)
	}
	invokeReserve := invokeInputGasAsset * int64(len(callerKeys))
	require.GreaterOrEqual(t, deployPlan.ChangeGasAsset, invokeReserve)

	deployExtraOutputs := make([]*wire.TxOut, 0, len(callerKeys)+1)
	for range callerKeys {
		deployExtraOutputs = append(deployExtraOutputs,
			wire.NewTxOut(1000, txAsset(wallet.GetGasAssetName(), invokeInputGasAsset), spendScript))
	}
	if rest := deployPlan.ChangeGasAsset - invokeReserve; rest > 0 {
		deployExtraOutputs = append(deployExtraOutputs,
			wire.NewTxOut(1000, txAsset(wallet.GetGasAssetName(), rest), spendScript))
	}
	deployTx, builtContract, err := contractcommon.BuildDeployTx(contractcommon.DeployTxBuildRequest{
		ContractPrefix:  contractcommon.TestnetContractPrefix,
		Type:            contractcommon.ContractTypeEVM,
		Deployer:        caller.String(),
		GasLimit:        deployPlan.GasLimit,
		DeployNonce:     deployNonce,
		ContractContent: initCode,
		Funding: wire.TxOut{
			Value:  1000,
			Assets: txAsset(wallet.GetGasAssetName(), deployPlan.ContractFundingGasAsset),
		},
		Inputs:       []wire.OutPoint{{Hash: anchorTx.TxHash(), Index: 0}},
		ExtraOutputs: deployExtraOutputs,
	})
	require.NoError(t, err)
	require.True(t, contract.Equal(builtContract))
	signTaprootInputs(t, deployTx, callerKeys[0], redeemScript, controlBlock)
	deployChangeOuts := collectSpendableOutPoints(t, deployTx, deployExtraOutputs)

	invokeTxs := make([]*wire.MsgTx, 0, len(callerKeys))
	for i, key := range callerKeys {
		plan := invokePlans[i]
		tx, err := contractcommon.BuildInvokeTx(contractcommon.InvokeTxBuildRequest{
			Contract:  contract,
			GasLimit:  plan.GasLimit,
			CallNonce: uint64(i + 1),
			Action:    "call",
			Param:     invokeCalldata,
			Funding: wire.TxOut{
				Assets: txAsset(wallet.GetGasAssetName(), plan.ContractFundingGasAsset),
			},
			Inputs: []wire.OutPoint{deployChangeOuts[i]},
			ExtraOutputs: []*wire.TxOut{
				wire.NewTxOut(1000, txAsset(wallet.GetGasAssetName(), plan.ChangeGasAsset), spendScript),
			},
		})
		require.NoError(t, err)
		signTaprootInputs(t, tx, key, redeemScript, controlBlock)
		invokeTxs = append(invokeTxs, tx)
	}
	return deployTx, invokeTxs, contract
}

func buildEVMDeployTxFor(t *testing.T, actor *templateActor, initCode []byte, nonce uint64,
	gasLimit int64, height uint64, inputGasAsset int64, input wire.OutPoint) (*wire.MsgTx, contractcommon.ContractAddress, wire.OutPoint) {

	t.Helper()
	caller := evmAddressFromAddressString(actor.Address)
	contract, err := contractcommon.DeriveEVMCreateContractAddress(contractcommon.TestnetContractPrefix, caller, nonce)
	require.NoError(t, err)
	plan, err := EVMDeployGasPlan(gasLimit, height, inputGasAsset)
	require.NoError(t, err)
	changeOutput := wire.NewTxOut(0, txAsset(wallet.GetGasAssetName(), plan.ChangeGasAsset), actor.PkScript)
	tx, builtContract, err := contractcommon.BuildDeployTx(contractcommon.DeployTxBuildRequest{
		ContractPrefix:  contractcommon.TestnetContractPrefix,
		Type:            contractcommon.ContractTypeEVM,
		Deployer:        caller.String(),
		GasLimit:        plan.GasLimit,
		DeployNonce:     nonce,
		ContractContent: initCode,
		Funding: wire.TxOut{
			Value:  1000,
			Assets: txAsset(wallet.GetGasAssetName(), plan.ContractFundingGasAsset),
		},
		Inputs:       []wire.OutPoint{input},
		ExtraOutputs: []*wire.TxOut{changeOutput},
	})
	require.NoError(t, err)
	require.True(t, contract.Equal(builtContract))
	signTaprootInputs(t, tx, actor.Key, actor.RedeemScript, actor.ControlBlock)
	changes := collectSpendableOutPoints(t, tx, []*wire.TxOut{changeOutput})
	return tx, contract, changes[0]
}

func buildEVMInvokeTxFor(t *testing.T, actor *templateActor, contract contractcommon.ContractAddress,
	action string, param []byte, nonce uint64, gasLimit int64, height uint64, inputGasAsset int64,
	input wire.OutPoint) (*wire.MsgTx, wire.OutPoint) {

	return buildEVMInvokeTxWithFundingFor(t, actor, contract, action, param, nonce, gasLimit,
		height, inputGasAsset, input, nil, 0, nil)
}

func buildEVMInvokeTxWithFundingFor(t *testing.T, actor *templateActor, contract contractcommon.ContractAddress,
	action string, param []byte, nonce uint64, gasLimit int64, height uint64, inputGasAsset int64,
	gasInput wire.OutPoint, businessInputs []wire.OutPoint, fundingValue int64,
	fundingAssets wire.TxAssets) (*wire.MsgTx, wire.OutPoint) {

	t.Helper()
	plan, err := EVMInvokeGasPlan(gasLimit, height, true, inputGasAsset)
	require.NoError(t, err)
	gasAsset := wallet.GetGasAssetName()
	businessGasAsset := int64(0)
	for _, asset := range fundingAssets {
		if asset.Name.String() == gasAsset {
			businessGasAsset += asset.Amount.Int64()
		}
	}
	inputValue := int64(1000)
	for range businessInputs {
		inputValue += 1000
	}
	require.GreaterOrEqual(t, inputValue, fundingValue)
	require.GreaterOrEqual(t, plan.ChangeGasAsset, businessGasAsset)
	changeOutput := wire.NewTxOut(inputValue-fundingValue, txAsset(gasAsset, plan.ChangeGasAsset-businessGasAsset), actor.PkScript)
	assets := txAsset(gasAsset, plan.ContractFundingGasAsset)
	assets = append(assets, fundingAssets...)
	assets = txAssets(assets...)
	inputs := append([]wire.OutPoint{gasInput}, businessInputs...)
	tx, err := contractcommon.BuildInvokeTx(contractcommon.InvokeTxBuildRequest{
		Contract:  contract,
		GasLimit:  plan.GasLimit,
		CallNonce: nonce,
		Action:    action,
		Param:     param,
		Funding: wire.TxOut{
			Value:  fundingValue,
			Assets: assets,
		},
		Inputs:       inputs,
		ExtraOutputs: []*wire.TxOut{changeOutput},
	})
	require.NoError(t, err)
	signTaprootInputs(t, tx, actor.Key, actor.RedeemScript, actor.ControlBlock)
	changes := collectSpendableOutPoints(t, tx, []*wire.TxOut{changeOutput})
	return tx, changes[0]
}

func buildEVMCloseTxFor(t *testing.T, actor *templateActor, contract contractcommon.ContractAddress,
	nonce uint64, gasLimit int64, height uint64, inputGasAsset int64, input wire.OutPoint) (*wire.MsgTx, wire.OutPoint) {

	t.Helper()
	plan, err := EVMInvokeGasPlan(gasLimit, height, true, inputGasAsset)
	require.NoError(t, err)
	changeOutput := wire.NewTxOut(1000, txAsset(wallet.GetGasAssetName(), plan.ChangeGasAsset), actor.PkScript)
	tx, err := contractcommon.BuildInvokeTx(contractcommon.InvokeTxBuildRequest{
		Contract:  contract,
		GasLimit:  plan.GasLimit,
		CallNonce: nonce,
		Action:    contractcommon.ContractInvokeAPIClose,
		Funding: wire.TxOut{
			Assets: txAsset(wallet.GetGasAssetName(), plan.ContractFundingGasAsset),
		},
		Inputs:       []wire.OutPoint{input},
		ExtraOutputs: []*wire.TxOut{changeOutput},
	})
	require.NoError(t, err)
	signTaprootInputs(t, tx, actor.Key, actor.RedeemScript, actor.ControlBlock)
	changes := collectSpendableOutPoints(t, tx, []*wire.TxOut{changeOutput})
	return tx, changes[0]
}

func buildSignedVaultWithdrawTx(t *testing.T, actor *templateActor, contract contractcommon.ContractAddress,
	recipient string, deployerKey *btcec.PrivateKey, key int64, n, nonce uint64, gasLimit int64, height uint64,
	inputGasAsset int64, input wire.OutPoint) *wire.MsgTx {

	t.Helper()
	digest := signedVaultDigest(recipient, key, n)
	signature, err := signSolidityDigest(deployerKey, digest)
	require.NoError(t, err)
	authorization := evmABIEncode(evmABIString(recipient), evmABIInt64(key), evmABIBytes(signature))
	calldata := solidityCall("withdraw(uint256,bytes)", evmABIUint(n), evmABIBytes(authorization))

	plan, err := EVMInvokeGasPlan(gasLimit, height, true, inputGasAsset)
	require.NoError(t, err)
	tx, err := contractcommon.BuildInvokeTx(contractcommon.InvokeTxBuildRequest{
		Contract:  contract,
		GasLimit:  plan.GasLimit,
		CallNonce: nonce,
		Action:    "call",
		Param:     calldata,
		Funding: wire.TxOut{
			Assets: txAsset(wallet.GetGasAssetName(), plan.ContractFundingGasAsset),
		},
		Inputs: []wire.OutPoint{input},
		ExtraOutputs: []*wire.TxOut{
			wire.NewTxOut(1000, txAsset(wallet.GetGasAssetName(), plan.ChangeGasAsset), actor.PkScript),
		},
	})
	require.NoError(t, err)
	signTaprootInputs(t, tx, actor.Key, actor.RedeemScript, actor.ControlBlock)
	return tx
}

func buildEVMDefaultInvokeFor(t *testing.T, actor *templateActor, contract contractcommon.ContractAddress,
	inputs []wire.OutPoint, funding wire.TxOut) *wire.MsgTx {

	t.Helper()
	pkScript, err := contractcommon.ContractPkScript(contract)
	require.NoError(t, err)
	funding.PkScript = pkScript
	tx := wire.NewMsgTx(wire.TxVersion)
	for _, input := range inputs {
		tx.AddTxIn(wire.NewTxIn(&input, nil, nil))
	}
	tx.AddTxOut(&funding)
	signTaprootInputs(t, tx, actor.Key, actor.RedeemScript, actor.ControlBlock)
	return tx
}

func buildEVMValueDepositFor(t *testing.T, actor *templateActor, contract contractcommon.ContractAddress,
	input wire.OutPoint, value int64, inputGasAsset int64) *wire.MsgTx {

	t.Helper()
	require.Positive(t, value)
	require.GreaterOrEqual(t, int64(1000), value)
	pkScript, err := contractcommon.ContractPkScript(contract)
	require.NoError(t, err)
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(wire.NewTxIn(&input, nil, nil))
	tx.AddTxOut(wire.NewTxOut(value, nil, pkScript))
	tx.AddTxOut(wire.NewTxOut(1000-value, txAsset(wallet.GetGasAssetName(), inputGasAsset), actor.PkScript))
	signTaprootInputs(t, tx, actor.Key, actor.RedeemScript, actor.ControlBlock)
	return tx
}

func requireEVMContractMetadata(t *testing.T, node *testHarness, contract, name string, assets []string) {
	t.Helper()
	param, err := json.Marshal(contract)
	require.NoError(t, err)
	raw, err := node.Client.RawRequest("getcontractstate", []json.RawMessage{param})
	require.NoError(t, err)
	var result struct {
		Details struct {
			Contract struct {
				Name   string   `json:"name"`
				Assets []string `json:"assets"`
			} `json:"contract"`
		} `json:"details"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Equal(t, name, result.Details.Contract.Name)
	require.ElementsMatch(t, assets, result.Details.Contract.Assets)
}

type evmLimitOrderViewRow struct {
	OrderID       any    `json:"orderId"`
	Maker         string `json:"maker"`
	SellAsset     string `json:"sellAsset"`
	BuyAsset      string `json:"buyAsset"`
	SellRemaining string `json:"sellRemaining"`
	BuyRemaining  string `json:"buyRemaining"`
}

func requireEVMLimitOrderView(t *testing.T, node *testHarness, contract, nextOrderID string, rows []evmLimitOrderViewRow) {
	t.Helper()
	param, err := json.Marshal(contract)
	require.NoError(t, err)
	raw, err := node.Client.RawRequest("getcontractstate", []json.RawMessage{param})
	require.NoError(t, err)
	var result struct {
		State struct {
			Custom struct {
				NextOrderID      any                    `json:"nextOrderId"`
				ActiveOrderCount any                    `json:"activeOrderCount"`
				ActiveOrders     []evmLimitOrderViewRow `json:"activeOrders"`
			} `json:"custom"`
		} `json:"state"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	require.NoError(t, decoder.Decode(&result))
	require.Equal(t, nextOrderID, fmt.Sprint(result.State.Custom.NextOrderID))
	for i := range result.State.Custom.ActiveOrders {
		result.State.Custom.ActiveOrders[i].OrderID = fmt.Sprint(result.State.Custom.ActiveOrders[i].OrderID)
	}
	if rows == nil {
		rows = []evmLimitOrderViewRow{}
	}
	require.Equal(t, fmt.Sprint(len(rows)), fmt.Sprint(result.State.Custom.ActiveOrderCount))
	require.Equal(t, rows, result.State.Custom.ActiveOrders)
}

func findEVMStateRoot(tx *wire.MsgTx) ([32]byte, bool, error) {
	var root [32]byte
	var found bool
	for _, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		txType, content, err := contractcommon.ReadNullDataScript(txOut.PkScript)
		if err != nil || txType != contractcommon.TxTypeCoinbaseStateRoot {
			continue
		}
		payload, err := contractcommon.DecodeStateRootPayload(content)
		if err != nil {
			return [32]byte{}, false, err
		}
		if found {
			return [32]byte{}, false, fmt.Errorf("multiple EVM state roots in coinbase")
		}
		root = payload.StateRoot
		found = true
	}
	return root, found, nil
}

func evmAddressFromAddressString(address string) contractcommon.EVMAddress {
	var out contractcommon.EVMAddress
	hash := btcutil.Hash160([]byte(address))
	copy(out[:], hash)
	return out
}
