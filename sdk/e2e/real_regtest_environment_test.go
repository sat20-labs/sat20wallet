package e2e

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	sindexercommon "github.com/sat20-labs/satoshinet/indexer/common"
	sindexerwire "github.com/sat20-labs/satoshinet/indexer/rpcserver/wire"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Keep the default fixture endpoints while allowing an isolated local run.
func sdkPWARegtestSatoshiNetPortBase(t *testing.T) int {
	t.Helper()
	const defaultPortBase = 30250
	portBase := defaultPortBase
	if value := os.Getenv("SAT20_REAL_SATOSHINET_PORT_BASE"); value != "" {
		var err error
		portBase, err = strconv.Atoi(value)
		require.NoError(t, err, "SAT20_REAL_SATOSHINET_PORT_BASE must be an integer")
		require.True(t, portBase >= 1024 && portBase <= 65511,
			"SAT20_REAL_SATOSHINET_PORT_BASE must leave room for all node ports (1024..65511)")
	}
	if os.Getenv("SAT20_REAL_SATOSHINET_ATTACH") != "" {
		require.Equal(t, defaultPortBase, portBase,
			"the existing SSH attach fixture requires the default SatoshiNet ports")
	}
	return portBase
}

// This uses the existing plugin/node runner and real L1 outputs. The separate
// nodes-anchor directory preserves the earlier empty-template diagnostic DB.
func prepareSDKPWARegtestNodes(t *testing.T) *realSatoshiNet {
	t.Helper()
	portBase := sdkPWARegtestSatoshiNetPortBase(t)
	raw := os.Getenv("SAT20_REAL_L1_INDEXER")
	if raw == "" {
		t.Skip("requires the existing Bitcoin Core 31.0 and real regtest indexer")
	}
	l1, err := url.Parse(raw)
	require.NoError(t, err)
	ip := net.ParseIP(l1.Hostname())
	require.True(t, l1.Scheme == "http" && ip != nil && ip.IsLoopback() && l1.Port() != "" && l1.User == nil)
	require.Equal(t, "/btc/regtest", strings.TrimRight(l1.Path, "/"))
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(strings.TrimRight(raw, "/") + "/health")
	require.NoError(t, err)
	var health struct{ Status string }
	err = json.NewDecoder(resp.Body).Decode(&health)
	require.NoError(t, resp.Body.Close())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "ok", health.Status)
	if attached := os.Getenv("SAT20_REAL_SATOSHINET_ATTACH"); attached != "" {
		return attachSDKPWARegtestNodes(t, attached)
	}

	seeds := readSDKPWARegtestMnemonics(t)
	runtimeDir := os.Getenv("SAT20_REAL_REGTEST_RUNTIME")
	require.NotEmpty(t, runtimeDir, "provide the persistent local acceptance directory")
	require.True(t, filepath.IsAbs(runtimeDir))
	require.NotEmpty(t, seeds["bootstrap"], "missing fixed bootstrap role")
	bootstrapKey := keyFromMnemonic(t, seeds["bootstrap"], 0)
	bootstrapPubkey := hex.EncodeToString(bootstrapKey.PubKey().SerializeCompressed())
	localBootstrapPeer := fmt.Sprintf("b@%s@http://127.0.0.1:%d/regtest", bootstrapPubkey, portBase+3)
	artifacts := satoshinetCoreBuildArtifacts(t)
	start := func(role string, port int) *testHarness {
		mnemonic := seeds[role]
		require.NotEmpty(t, mnemonic, "missing fixed operator role %s", role)
		key := keyFromMnemonic(t, mnemonic, 0)
		pubkey := hex.EncodeToString(key.PubKey().SerializeCompressed())
		nodeDir := filepath.Join(runtimeDir, "nodes-anchor", role)
		require.NoError(t, os.MkdirAll(nodeDir, 0o700))
		exe := filepath.Join(nodeDir, filepath.Base(artifacts.coreExecutable))
		copySatoshiNetRuntimeFile(t, artifacts.coreExecutable, exe)
		copySatoshiNetRuntimeFile(t, artifacts.corePlugin, filepath.Join(nodeDir, "stpd.so"))
		stageSatoshiNetNodeConfig(t, nodeDir)
		addr := func(offset int) string { return fmt.Sprintf("127.0.0.1:%d", port+offset) }
		p2p, rpc, stp, management := addr(0), addr(1), addr(3), addr(4)
		conf := fmt.Sprintf(satoshinetTestConf, satoshinetSTPMode(role), l1.Host, addr(2), stp, management, mnemonic)
		conf = strings.Replace(conf, "env: test", "env: dev", 1)
		conf = strings.Replace(conf, "chain: testnet", "chain: regtest", 1)
		conf = strings.ReplaceAll(conf, "proxy: testnet", "proxy: regtest")
		conf = strings.Replace(conf, "proxy: regtest", "proxy: btc/regtest", 1)
		// Both embedded SDKs use this independent regtest bootstrap. Explicit
		// peers prevent the SDK default from contacting the public testnet.
		require.NotContains(t, conf, "\npeers:")
		conf += fmt.Sprintf("\npeers:\n  - %q\n", localBootstrapPeer)
		conf += fmt.Sprintf("\npublic_rpc:\n  browser_allowed_origins: [%q, %q]\n",
			sdkPWARegtestBrowserOrigin(t), "http://127.0.0.1:3006")
		require.NoError(t, os.WriteFile(filepath.Join(nodeDir, "conf.yaml"), []byte(conf), 0o600))
		args := []string{"--regtest", "--notls", "--nodnsseed", "--txindex", "--generate", "--miningpubkey=" + pubkey,
			"--listen=" + p2p, "--rpclisten=" + rpc, "--rpcuser=user", "--rpcpass=pass",
			"--datadir=" + filepath.Join(nodeDir, "data"), "--logdir=" + filepath.Join(nodeDir, "logs"),
			"--indexerscheme=http", "--indexerhost=" + l1.Host, "--indexerproxy=btc/regtest"}
		env := []string{"SATOSHINET_RPCTEST_NODE_ROLE=" + role, "SATOSHINET_RPCTEST_STP_MNEMONIC=" + mnemonic, "SATOSHINET_RPCTEST_STP_PASSWORD=rpctest"}
		h := newTestHarness(t, exe, nodeDir, p2p, rpc, args, env)
		h.role, h.nodePubKey, h.stpAddr, h.managementAddr = role, pubkey, stp, management
		require.NoError(t, os.Chmod(h.logFile, 0o600))
		// Register after newTestHarness so this runs before its generic
		// SIGKILL cleanup and lets ffldb close and flush the persistent DB.
		t.Cleanup(func() {
			if h.Client == nil {
				return
			}
			stopCtx, cancel := context.WithTimeout(context.Background(), rpcReadyTimeout)
			defer cancel()
			if err := posPWAStopNode(stopCtx, h); err != nil {
				t.Errorf("normally stop persistent %s node: %v", h.role, err)
			}
		})
		return h
	}
	bootstrap, core := start("bootstrap", portBase), start("core", portBase+10)
	require.NoError(t, connectNode(core, bootstrap))
	network := &realSatoshiNet{Bootstrap: bootstrap, Core: core, Nodes: []*testHarness{bootstrap, core}}
	require.NoError(t, joinBlocks(network.Nodes))
	for _, node := range network.Nodes {
		waitSDKPWARegtestManagerHealth(t, node)
		var mining bool
		mining, err = node.Client.GetGenerate()
		require.NoError(t, err)
		require.True(t, mining, "regtest nodes must start with the existing scheduled miner; empty templates are rejected")
		genesis, err := node.Client.GetBlockHash(0)
		require.NoError(t, err)
		require.Equal(t, *chaincfg.RegressionNetParams.GenesisHash, *genesis)
	}
	waitSDKPWARegtestIndexes(t, network)
	return network
}

// The POS continuation shares the existing nodes-anchor root and ordinary-1
// mnemonic, but owns a separate Miner-plugin DB. The caller closes the
// ordinary-1 Manager first, then this returned close function releases the
// preseeded account-1 Manager before the plugin opens the same DB.
func preseedSDKPWARegtestMinerWallet(t *testing.T, network *realSatoshiNet, nodeDir, mnemonic string) (
	*wallet.Manager, string, func()) {
	t.Helper()
	portBase := sdkPWARegtestSatoshiNetPortBase(t)
	require.NotNil(t, network)
	require.NotEmpty(t, mnemonic)
	endpoint, err := url.Parse(os.Getenv("SAT20_REAL_L1_INDEXER"))
	require.NoError(t, err)
	require.Equal(t, "/btc/regtest", strings.TrimRight(endpoint.Path, "/"))
	bootstrapKey := keyFromMnemonic(t, readSDKPWARegtestMnemonics(t)["bootstrap"], 0)
	bootstrapPubkey := hex.EncodeToString(bootstrapKey.PubKey().SerializeCompressed())
	peers := []string{
		fmt.Sprintf("b@%s@http://127.0.0.1:%d/regtest", bootstrapPubkey, portBase+3),
		fmt.Sprintf("s@%s@http://127.0.0.1:%d/regtest", network.Core.nodePubKey, portBase+13),
	}
	cfg := &sdkcommon.Config{Env: "dev", Chain: "regtest", Mode: "client", Peers: peers,
		IndexerL1: &sdkcommon.Indexer{Scheme: endpoint.Scheme, Host: endpoint.Host, Proxy: "btc/regtest"},
		IndexerL2: &sdkcommon.Indexer{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", portBase+12), Proxy: "regtest"}}
	// btcd's default home is ./data under newTestHarness's nodeDir cwd.
	// LoadSTP stores plugin state beneath that home, so seed the exact DB the
	// Miner process will open rather than a parallel nodeDir/db tree.
	dbDir := filepath.Join(nodeDir, "data", "db", "stp", "regtest")
	require.NoError(t, os.MkdirAll(dbDir, 0o700))
	database := indexerdb.NewKVDB(dbDir)
	require.NotNil(t, database)
	manager := wallet.NewManager(cfg, database)
	require.NotNil(t, manager)
	closed := false
	closeManager := func() {
		if closed {
			return
		}
		manager.Close()
		require.NoError(t, database.Close())
		closed = true
	}
	t.Cleanup(closeManager)
	const password = "rpctest"
	if len(manager.GetWalletCatalog()) == 0 {
		_, err = manager.ImportWallet(mnemonic, password)
		require.NoError(t, err, "preseed Miner plugin wallet")
	} else {
		_, err = manager.UnlockWallet(password)
		require.NoError(t, err, "unlock preseeded Miner plugin wallet")
	}
	require.NoError(t, manager.SwitchAccount(1), "persist Miner account 1 before plugin startup")
	require.Equal(t, uint32(1), manager.GetCurrentAccountId())
	expected := keyFromMnemonic(t, mnemonic, 1).PubKey().SerializeCompressed()
	actualWallet := manager.GetWallet()
	require.NotNil(t, actualWallet)
	require.Equal(t, expected, actualWallet.GetPubKey().SerializeCompressed(), "SDK account 1 miner key")
	require.Equal(t, expected, actualWallet.GetPaymentPubKey().SerializeCompressed(), "Miner plugin GetPubKey payment key")
	manager.Start()
	require.Eventually(t, manager.IsReady, rpcReadyTimeout, 200*time.Millisecond,
		"A1 SDK Manager must be ready before a real StakeToBeMinner call")
	return manager, hex.EncodeToString(expected), closeManager
}

// Registration uses the existing SDK stake path and a confirmed real L1
// anchor. It deliberately fails if the A1 wallet has not been supplied the
// current staking asset through the ordinary SDK mint/confirm path.
func registerSDKPWARegtestMiner(t *testing.T, manager *wallet.Manager, network *realSatoshiNet) string {
	t.Helper()
	require.NotNil(t, manager)
	require.NotNil(t, network)
	minerPubkey := hex.EncodeToString(manager.GetWallet().GetPubKey().SerializeCompressed())
	minerPubkeyBytes, err := hex.DecodeString(minerPubkey)
	require.NoError(t, err)
	if info, infoErr := manager.GetIndexerRPCClient_SatsNet().GetMinerInfo(minerPubkeyBytes); infoErr == nil && info != nil {
		corePubkey, decodeErr := hex.DecodeString(network.Core.nodePubKey)
		require.NoError(t, decodeErr)
		coreInfo, coreErr := manager.GetIndexerRPCClient_SatsNet().GetCoreNodeInfo(corePubkey)
		require.NoError(t, coreErr, "read existing Core parent for A1 registration")
		require.NotNil(t, coreInfo)
		child := coreInfo.ChildMiners[minerPubkey]
		require.NotNil(t, child, "existing A1 registration must appear under the selected Core")
		require.Equal(t, network.Core.nodePubKey, info.ServerNode, "reuse only the existing A1 registration under this Core")
		require.Greater(t, info.AscendHeight, 0, "existing A1 registration must have a confirmed AscendHeight")
		require.NotEmpty(t, info.AscendUtxo, "existing A1 registration must retain its real staking outpoint")
		require.NotEmpty(t, info.AnchorTxId, "existing A1 registration must retain its real anchor")
		require.Equal(t, info.AscendHeight, child.AscendHeight)
		require.Equal(t, info.AscendUtxo, child.AscendUtxo)
		return minerPubkey
	} else {
		require.Error(t, infoErr)
		require.EqualError(t, infoErr, "not found", "only an explicit absent registration may enter the real stake path")
	}
	assetName := indexercommon.GetStakeAssetName(manager.GetSyncHeightL1())
	asset := wallet.ParseAssetString(assetName)
	require.NotNil(t, asset)
	amount := indexercommon.GetStakeAssetAmt(manager.GetSyncHeightL1())
	balance := manager.GetAssetBalance("", asset)
	if balance == nil || balance.Cmp(indexercommon.NewDecimal(amount, 0)) < 0 {
		// The reviewed DOGCOIN ticker already exists. Create only the A1 stake
		// amount through the SDK, then confirm it in the existing Bitcoin Core;
		// no ticker, UTXO, balance, or L2 registration is inserted by the test.
		ticker := manager.GetTickerInfoV2(assetName)
		require.NotNil(t, ticker, "the current %s stake ticker must already exist", assetName)
		minted, err := manager.MintAsset_ordx(manager.GetWallet().GetAddress(), ticker, amount, nil, 1)
		require.NoError(t, err, "SDK mint current stake asset for A1")
		require.NotNil(t, minted)
		require.NotNil(t, minted.CommitTx)
		require.NotNil(t, minted.RevealTx)
		mineSDKPWARegtestL1(t, manager, manager.GetWallet().GetAddress())
		require.Eventually(t, func() bool {
			balance = manager.GetAssetBalance("", asset)
			return balance != nil && balance.Cmp(indexercommon.NewDecimal(amount, 0)) >= 0
		}, rpcReadyTimeout, time.Second, "real L1 indexer did not recognize A1's SDK-minted stake asset")
	}
	txid, _, err := manager.StakeToBeMinner(false, 1)
	require.NoError(t, err, "SDK StakeToBeMinner(false) must register A1 under the live Core")
	require.Len(t, txid, 64)
	var pending []string
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getrawmempool"), &pending))
	require.Contains(t, pending, txid, "SDK registration Anchor must be accepted by the real Bitcoin Core")
	mineSDKPWARegtestL1(t, manager, manager.GetWallet().GetAddress())
	var l1Height int
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getblockcount"), &l1Height))
	require.Eventually(t, func() bool { return manager.GetSyncHeightL1() >= l1Height }, rpcReadyTimeout, time.Second,
		"real L1 indexer did not confirm Miner registration")
	corePubkey, err := hex.DecodeString(network.Core.nodePubKey)
	require.NoError(t, err)
	var minerInfo *sindexerwire.MinerInfo
	require.Eventually(t, func() bool {
		minerInfo, err = manager.GetIndexerRPCClient_SatsNet().GetMinerInfo(minerPubkeyBytes)
		return err == nil && minerInfo != nil && minerInfo.ServerNode == network.Core.nodePubKey && minerInfo.AscendHeight > 0
	}, rpcReadyTimeout, 300*time.Millisecond, "SatoshiNet indexer did not confirm A1's real Core-parent registration")
	var coreInfo *sindexercommon.CoreNodeInfo
	require.Eventually(t, func() bool {
		coreInfo, err = manager.GetIndexerRPCClient_SatsNet().GetCoreNodeInfo(corePubkey)
		if err != nil || coreInfo == nil {
			return false
		}
		child := coreInfo.ChildMiners[minerPubkey]
		return child != nil && child.AscendHeight == minerInfo.AscendHeight
	}, rpcReadyTimeout, 300*time.Millisecond, "Core child list does not contain A1's confirmed AscendHeight")
	require.Equal(t, minerInfo.ServerNode, network.Core.nodePubKey)
	return minerPubkey
}

// prepareSDKPWARegtestCoreDogcoinStake establishes the real Core collateral
// required by the public-contract route. It only uses SDK deploy, mint, and
// StakeToBeMinner(true) paths, and it refuses partial or ambiguous prior state
// rather than repeating a transaction.
func prepareSDKPWARegtestCoreDogcoinStake(t *testing.T, issuer *wallet.Manager,
	network *realSatoshiNet, mine func(string, int)) map[string]string {
	t.Helper()
	require.NotNil(t, issuer)
	require.NotNil(t, network)
	require.NotNil(t, network.Core)
	require.NotNil(t, network.Bootstrap)
	require.NotNil(t, mine)
	require.NotNil(t, issuer.GetWallet())

	seeds := readSDKPWARegtestMnemonics(t)
	coreKey := keyFromMnemonic(t, seeds["core"], 0)
	bootstrapKey := keyFromMnemonic(t, seeds["bootstrap"], 0)
	corePub := coreKey.PubKey().SerializeCompressed()
	bootstrapPub := bootstrapKey.PubKey().SerializeCompressed()
	corePubHex := hex.EncodeToString(corePub)
	require.Equal(t, network.Core.nodePubKey, corePubHex, "use the fixed built-in Core identity")
	require.Equal(t, indexercommon.GetBootstrapPubKey(), hex.EncodeToString(bootstrapPub),
		"use the fixed built-in Bootstrap identity")

	coreWallet := wallet.NewInternalWalletWithMnemonic(seeds["core"], "", wallet.GetChainParam())
	require.NotNil(t, coreWallet)
	coreAddress := wallet.PublicKeyToP2TRAddress(coreKey.PubKey())
	require.Equal(t, coreWallet.GetAddress(), coreAddress, "derive Core's fixed account-0 Bitcoin address")
	stakeAddress, err := indexercommon.GetCoreNodeChannelAddress(corePub, wallet.GetChainParam())
	require.NoError(t, err)
	l2StakeAddress, err := wallet.GetP2WSHaddress(corePub, bootstrapPub)
	require.NoError(t, err)
	require.Equal(t, stakeAddress, l2StakeAddress,
		"the actual Core/Bootstrap collateral and Anchor must identify the same channel address")

	startL1Height := issuer.GetSyncHeightL1()
	assetText := indexercommon.GetStakeAssetName(startL1Height)
	assetAmount := indexercommon.GetStakeAssetAmt(startL1Height)
	require.Equal(t, "ordx:f:dogcoin", assetText,
		"this fixture must prepare the live regtest staking asset, not ordinary sats")
	require.Equal(t, int64(1000), assetAmount, "this fixture must meet the live Core staking threshold")
	asset := wallet.ParseAssetString(assetText)
	require.NotNil(t, asset)

	// Fail before issuance or staking if this is not a clean or already-complete
	// isolated fixture. In particular, do not send a second ASCEND action when
	// an earlier stake output, anchor, or live action reservation exists.
	stakeOutputsBefore := issuer.GetIndexerClient().GetUtxoListWithTicker(stakeAddress, asset)
	require.Empty(t, stakeOutputsBefore,
		"the fixed Core/Bootstrap collateral address must be empty before the one authorized stake action")
	require.False(t, issuer.HasStaked(corePub), "Core must not already satisfy the staking threshold")

	l1CoreInfoBefore, err := issuer.GetIndexerRPCClient_SatsNet().GetCoreNodeInfo(corePub)
	require.NoError(t, err, "read the pre-existing built-in Core registry entry")
	require.NotNil(t, l1CoreInfoBefore)
	isCoreBefore, err := issuer.GetIndexerRPCClient_SatsNet().IsCoreNode(corePub)
	require.NoError(t, err)
	require.True(t, isCoreBefore, "preserve the built-in Core registry membership")
	coreRegistryBefore, err := json.Marshal(l1CoreInfoBefore)
	require.NoError(t, err)

	feeRate := issuer.GetFeeRate()
	require.Positive(t, feeRate, "use the live Bitcoin fee rate for the SDK inscription quotes")
	plain := wallet.ParseAssetString(wallet.PLAIN_ASSET.String())
	require.NotNil(t, plain)
	issuerAddress := issuer.GetWallet().GetAddress()
	issuerPlainAvailable, issuerPlainLocked := issuer.GetAssetAmount(issuerAddress, plain, nil)
	require.NotNil(t, issuerPlainAvailable)
	require.NotNil(t, issuerPlainLocked)
	issuerPlainUtxos := issuer.GetIndexerClient().GetUtxoListWithTicker(issuerAddress, plain)
	issuerLocks, err := issuer.GetLockedUtxoList(issuerAddress)
	require.NoError(t, err)
	spendableInputCount := 0
	var spendableInputTotal int64
	for _, output := range issuerPlainUtxos {
		if output == nil || issuerLocks[output.OutPoint] != nil {
			continue
		}
		spendableInputCount++
		spendableInputTotal += output.Value
	}
	require.Positive(t, spendableInputCount, "ordinary-2 needs real unlocked L1 fee inputs")
	require.Equal(t, issuerPlainAvailable.Int64(), spendableInputTotal,
		"fee preflight must use only confirmed, unlocked ordinary-2 plain-sat inputs")
	deployQuote := wallet.EstimatedDeployFee(spendableInputCount, feeRate)
	mintQuote := wallet.EstimatedMintFee(spendableInputCount, feeRate, assetAmount)
	issuerFeeBudget := deployQuote + mintQuote
	require.GreaterOrEqual(t, spendableInputTotal, issuerFeeBudget,
		"ordinary-2's existing unlocked L1 funds must cover both DOGCOIN inscription quotes before broadcasting")

	ticker := issuer.GetTickerInfoV2(assetText)
	coreAssetBefore := issuer.GetAssetBalance(coreAddress, asset)
	coreAssetBeforeAmt := int64(0)
	if coreAssetBefore != nil {
		coreAssetBeforeAmt = coreAssetBefore.Int64()
	}
	if ticker == nil {
		require.Zero(t, coreAssetBeforeAmt, "no Core DOGCOIN may predate this isolated issuance")
		deployed, deployErr := issuer.DeployTicker_ordx("dogcoin", assetAmount, assetAmount, 1, feeRate)
		require.NoError(t, deployErr, "deploy the live Core stake asset through the real SDK")
		require.NotNil(t, deployed)
		require.NotNil(t, deployed.CommitTx)
		require.NotNil(t, deployed.RevealTx)
		deployTxIDs := []string{deployed.CommitTx.TxID(), deployed.RevealTx.TxID()}
		confirmSDKPWARegtestL1Transactions(t, issuer, deployTxIDs, mine)
		require.Eventually(t, func() bool {
			ticker = issuer.GetTickerInfoV2(assetText)
			return ticker != nil
		}, rpcReadyTimeout, 200*time.Millisecond, "real indexer did not recognize the DOGCOIN deploy inscription")
		t.Logf("real Core stake ticker deployed: commit=%s reveal=%s L1_height=%d fee_rate=%d deploy_quote=%d",
			deployTxIDs[0], deployTxIDs[1], issuer.GetSyncHeightL1(), feeRate, deployQuote)
	}
	require.NotNil(t, ticker)
	require.Equal(t, "ordx", ticker.Protocol)
	require.Equal(t, "f", ticker.Type)
	require.Equal(t, "dogcoin", ticker.Ticker)
	require.Equal(t, int64(1), int64(ticker.N), "DOGCOIN must bind one satoshi per token")
	require.Equal(t, strconv.FormatInt(assetAmount, 10), ticker.Limit)
	require.Equal(t, strconv.FormatInt(assetAmount, 10), ticker.MaxSupply)

	totalMinted, err := strconv.ParseInt(ticker.TotalMinted, 10, 64)
	require.NoError(t, err, "read the exact indexed DOGCOIN supply before minting")
	switch {
	case totalMinted == 0 && coreAssetBeforeAmt == 0:
		minted, mintErr := issuer.MintAsset_ordx(coreAddress, ticker, assetAmount, nil, feeRate)
		require.NoError(t, mintErr, "mint the exact Core stake amount through the real SDK")
		require.NotNil(t, minted)
		require.NotNil(t, minted.CommitTx)
		require.NotNil(t, minted.RevealTx)
		mintTxIDs := []string{minted.CommitTx.TxID(), minted.RevealTx.TxID()}
		confirmSDKPWARegtestL1Transactions(t, issuer, mintTxIDs, mine)
		mintReveal := minted.RevealTx
		mintAddressScript, scriptErr := wallet.GetPkScriptFromAddress(coreAddress)
		require.NoError(t, scriptErr)
		mintVout := -1
		for index, output := range mintReveal.TxOut {
			if !bytes.Equal(output.PkScript, mintAddressScript) {
				continue
			}
			require.Equal(t, assetAmount, output.Value,
				"the exact 1,000 DOGCOIN mint must create its bound Core carrier")
			require.Equal(t, -1, mintVout, "mint reveal must create only one Core asset carrier")
			mintVout = index
		}
		require.NotEqual(t, -1, mintVout, "mint reveal must pay the fixed Core account-0 address")
		mintOutpoint := fmt.Sprintf("%s:%d", mintReveal.TxID(), mintVout)
		mintOutput, outputErr := issuer.GetIndexerClient().GetTxOutput(mintOutpoint)
		require.NoError(t, outputErr, "L1 indexer must expose the exact Core DOGCOIN carrier")
		require.NotNil(t, mintOutput)
		require.Equal(t, mintOutpoint, mintOutput.OutPointStr)
		require.Equal(t, assetAmount, mintOutput.OutValue.Value)
		require.Equal(t, mintAddressScript, mintOutput.OutValue.PkScript)
		mintAsset, assetErr := mintOutput.Assets.Find(asset)
		require.NoError(t, assetErr)
		require.Equal(t, assetAmount, mintAsset.Amount.Int64())
		require.Equal(t, uint32(1), mintAsset.BindingSat)
		require.Eventually(t, func() bool {
			ticker = issuer.GetTickerInfoV2(assetText)
			return ticker != nil && ticker.TotalMinted == strconv.FormatInt(assetAmount, 10)
		}, rpcReadyTimeout, 200*time.Millisecond, "real L1 indexer did not confirm the exact DOGCOIN supply")
		t.Logf("real Core stake DOGCOIN minted: commit=%s reveal=%s outpoint=%s amount=%d binding_sat=1 L1_height=%d fee_rate=%d mint_quote=%d",
			mintTxIDs[0], mintTxIDs[1], mintOutpoint, assetAmount, issuer.GetSyncHeightL1(), feeRate, mintQuote)
	case totalMinted == assetAmount && coreAssetBeforeAmt == assetAmount:
		t.Logf("reusing verified complete DOGCOIN issuance without a duplicate mint: amount=%d", assetAmount)
	default:
		require.FailNow(t, fmt.Sprintf("refusing to repeat or repair partial DOGCOIN issuance: totalMinted=%d Core=%d", totalMinted, coreAssetBeforeAmt))
	}

	ticker = issuer.GetTickerInfoV2(assetText)
	require.NotNil(t, ticker)
	require.Equal(t, strconv.FormatInt(assetAmount, 10), ticker.TotalMinted,
		"the real indexed supply must exactly equal the Core staking threshold")
	coreAvailable, coreLocked := issuer.GetAssetAmount(coreAddress, asset, nil)
	require.NotNil(t, coreAvailable, "Core must have exactly the minted DOGCOIN available")
	require.Equal(t, assetAmount, coreAvailable.Int64())
	if coreLocked != nil {
		require.Zero(t, coreLocked.Sign(), "Core's newly minted DOGCOIN must not be reserved or locked")
	}
	coreDogcoinOutputs := issuer.GetIndexerClient().GetUtxoListWithTicker(coreAddress, asset)
	require.NotEmpty(t, coreDogcoinOutputs, "Core must own a real indexed DOGCOIN output before staking")
	coreDogcoinTotal := int64(0)
	for _, output := range coreDogcoinOutputs {
		require.NotNil(t, output)
		indexed, outputErr := issuer.GetIndexerClient().GetTxOutput(output.OutPoint)
		require.NoError(t, outputErr)
		require.NotNil(t, indexed)
		assetInfo, findErr := indexed.Assets.Find(asset)
		require.NoError(t, findErr)
		require.Equal(t, uint32(1), assetInfo.BindingSat)
		coreDogcoinTotal += assetInfo.Amount.Int64()
	}
	require.Equal(t, assetAmount, coreDogcoinTotal,
		"Core's real DOGCOIN carriers must conserve the exact 1,000-unit issuance")

	coreFeeAvailable, coreFeeLocked := issuer.GetAssetAmount(coreAddress, plain, nil)
	require.NotNil(t, coreFeeAvailable)
	if coreFeeLocked != nil {
		require.Zero(t, coreFeeLocked.Sign(), "Core's L1 fee inputs must not be locked before the one stake action")
	}
 stakeFeeQuote := wallet.CalcFee_SendTx(1, 3, 1, asset, indexercommon.NewDecimal(assetAmount, 0), feeRate, false)
	require.GreaterOrEqual(t, coreFeeAvailable.Int64(), stakeFeeQuote,
		"the fixed Core wallet must have enough real unlocked L1 plain sats for the exact stake send quote")

	// Save the built-in Core registry record on both nodes. The built-in Core
	// must remain a Core; adding its stake must not replace its original entry.
	indexers := make([]*wallet.IndexerClient, 0, len(network.Nodes))
	coreRegistryJSON := make([]string, 0, len(network.Nodes))
	for _, node := range network.Nodes {
		client := sdkPWARegtestL2Indexer(t, node)
		indexers = append(indexers, client)
		isCore, checkErr := client.IsCoreNode(corePub)
		require.NoError(t, checkErr)
		require.True(t, isCore, "%s must preserve built-in Core registry membership", node.role)
		info, infoErr := client.GetCoreNodeInfo(corePub)
		require.NoError(t, infoErr)
		require.NotNil(t, info)
		encoded, marshalErr := json.Marshal(info)
		require.NoError(t, marshalErr)
		coreRegistryJSON = append(coreRegistryJSON, string(encoded))
	}
	require.Equal(t, coreRegistryJSON[0], coreRegistryJSON[1],
		"both SatoshiNet nodes must begin with the same built-in Core record")
	require.Equal(t, string(coreRegistryBefore), coreRegistryJSON[1],
		"issuer's view must agree with both nodes' pre-stake Core membership")

	// Ensure there is no active ASCEND reservation in the original Core wallet.
	// The Core process must release its DB before this test opens that same DB.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	err = posPWAStopNode(ctx, network.Core)
	cancel()
	require.NoError(t, err, "normally stop the original Core before opening its wallet DB")
	coreStopped := true
	coreRestored := false
	defer func() {
		if coreRestored || !coreStopped || network.Core.Client != nil {
			return
		}
		restartCtx, restartCancel := context.WithTimeout(context.Background(), rpcReadyTimeout)
		defer restartCancel()
		if restartErr := posPWARestartNode(restartCtx, network.Core); restartErr != nil {
			t.Errorf("restart original Core after stake preparation: %v", restartErr)
		}
	}()

	portBase := sdkPWARegtestSatoshiNetPortBase(t)
	l1URL, err := url.Parse(os.Getenv("SAT20_REAL_L1_INDEXER"))
	require.NoError(t, err)
	bootstrapIndexerURL, err := network.Bootstrap.IndexerURL("regtest")
	require.NoError(t, err)
	bootstrapIndexer, err := url.Parse(bootstrapIndexerURL)
	require.NoError(t, err)
	coreDBDir := filepath.Join(network.Core.nodeDir, "data", "db", "stp", "regtest")
	info, statErr := os.Stat(coreDBDir)
	require.NoError(t, statErr, "open only the original Core STP wallet DB")
	require.True(t, info.IsDir())
	database := indexerdb.NewKVDB(coreDBDir)
	require.NotNil(t, database)
	coreCfg := &sdkcommon.Config{Env: "dev", Chain: "regtest", Mode: "client",
		Peers: []string{fmt.Sprintf("b@%s@http://127.0.0.1:%d/regtest", hex.EncodeToString(bootstrapPub), portBase+3)},
		IndexerL1: &sdkcommon.Indexer{Scheme: l1URL.Scheme, Host: l1URL.Host, Proxy: "btc/regtest"},
		// The original Core indexer is offline during this exclusive session.
		// Read the already-verified canonical L2 state from Bootstrap instead.
		IndexerL2: &sdkcommon.Indexer{Scheme: bootstrapIndexer.Scheme, Host: bootstrapIndexer.Host, Proxy: "regtest"}}
	coreManager := wallet.NewManager(coreCfg, database)
	require.NotNil(t, coreManager)
	coreManagerClosed := false
	defer func() {
		if coreManagerClosed {
			return
		}
		coreManager.Close()
		if closeErr := database.Close(); closeErr != nil {
			t.Errorf("close original Core wallet DB: %v", closeErr)
		}
	}()
	require.NotEmpty(t, coreManager.GetWalletCatalog(),
		"original Core wallet must exist; never import or replace the fixed Core identity")
	_, err = coreManager.UnlockWallet("rpctest")
	require.NoError(t, err, "unlock the existing Core account")
	require.NoError(t, coreManager.SwitchAccount(0), "select the fixed Core account-0 identity")
	require.Equal(t, uint32(0), coreManager.GetCurrentAccountId())
	actualCorePub := coreManager.GetWallet().GetPubKey().SerializeCompressed()
	require.Equal(t, corePub, actualCorePub, "exclusive Core Manager must use original account 0")
	require.Equal(t, coreAddress, coreManager.GetWallet().GetAddress())
	coreManager.Start()
	require.Eventually(t, coreManager.IsReady, rpcReadyTimeout, 200*time.Millisecond,
		"Core's original SDK Manager must be ready against the Bootstrap peer")
	require.True(t, coreManager.ServerIsBootstrapNode(), "StakeToBeMinner(true) must target Bootstrap")
	require.GreaterOrEqual(t, coreManager.GetSyncHeightL1(), issuer.GetSyncHeightL1(),
		"exclusive Core Manager must sync the confirmed DOGCOIN issuance")

	for id, reservation := range coreManager.GetRemoteActionReservations() {
		action, ok := reservation.(*wallet.RemoteActionPerformReservation)
		if ok && action.Action == wallet.REMOTE_ACTION_ASCEND {
			require.FailNow(t, fmt.Sprintf("refusing to repeat active Core ASCEND reservation %d (id=%d status=%d)",
				action.Id, id, action.GetStatus()))
		}
	}
	require.False(t, coreManager.HasStaked(corePub),
		"Core must be genuinely unstaked before the one SDK stake action")
	coreStakeBalance, coreStakeLocked := coreManager.GetAssetAmount(coreAddress, asset, nil)
	require.NotNil(t, coreStakeBalance)
	require.Equal(t, assetAmount, coreStakeBalance.Int64(), "Core account 0 must spend exactly its real DOGCOIN")
	if coreStakeLocked != nil {
		require.Zero(t, coreStakeLocked.Sign())
	}
	stakeOutputsBefore = coreManager.GetIndexerClient().GetUtxoListWithTicker(stakeAddress, asset)
	require.Empty(t, stakeOutputsBefore,
		"no earlier Core/Bootstrap DOGCOIN stake output may be reused or double-registered")

	// One invocation only. If this request has an uncertain result, fail and
	// inspect its records; never repeat it from this fixture.
	stakeTxID, reservationID, stakeErr := coreManager.StakeToBeMinner(true, feeRate)
	require.NoError(t, stakeErr, "fixed Core account 0 must stake through the real SDK Bootstrap path")
	require.Len(t, stakeTxID, 64)
	require.Greater(t, reservationID, int64(0), "Core stake action must persist its real reservation id")
	if active, ok := coreManager.GetRemoteActionReservation(reservationID); ok {
		action, actionOK := active.(*wallet.RemoteActionPerformReservation)
		require.True(t, actionOK)
		require.Equal(t, wallet.REMOTE_ACTION_ASCEND, action.Action)
		require.True(t, action.SendTxInL1)
		require.True(t, action.SendToBootstrapNode)
		require.Equal(t, stakeTxID, action.FeeTxId)
	}
	coreManager.Close()
	require.NoError(t, database.Close())
	coreManagerClosed = true

	restartCtx, restartCancel := context.WithTimeout(context.Background(), rpcReadyTimeout)
	err = posPWARestartNode(restartCtx, network.Core)
	restartCancel()
	require.NoError(t, err, "restart the original Core node with its original wallet DB")
	coreRestored = true
	waitSDKPWARegtestManagerHealth(t, network.Core)
	require.NoError(t, connectNode(network.Core, network.Bootstrap),
		"reconnect Core to its original Bootstrap peer")
	require.NoError(t, joinBlocks(network.Nodes), "both nodes must rejoin the canonical SatoshiNet chain")
	waitSDKPWARegtestIndexes(t, network)

	// The SDK's returned transaction is the Core's actual L1 DOGCOIN transfer.
	// Confirm the exact transaction before inspecting outputs; do not reissue if
	// the result is missing from both mempool and indexer.
	var stakeMempool []string
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getrawmempool"), &stakeMempool))
	if !containsString(stakeMempool, stakeTxID) {
		stakeInfo, lookupErr := issuer.GetIndexerClient().GetTxInfo(stakeTxID)
		require.NoError(t, lookupErr, "look up the one submitted Core stake transaction")
		require.NotNil(t, stakeInfo)
		require.Greater(t, stakeInfo.Confirmations, uint64(0),
			"stake tx missing from mempool must already have a confirmed indexer record; never resend")
	} else {
		mine(issuer.GetWallet().GetAddressByIndex(1), 1)
	}
	require.Eventually(t, func() bool {
		info, lookupErr := issuer.GetIndexerClient().GetTxInfo(stakeTxID)
		return lookupErr == nil && info != nil && info.Confirmations > 0
	}, rpcReadyTimeout, 200*time.Millisecond, "L1 indexer did not confirm the single Core stake transaction")
	stakeInfo, err := issuer.GetIndexerClient().GetTxInfo(stakeTxID)
	require.NoError(t, err)
	require.NotNil(t, stakeInfo)
	require.Greater(t, stakeInfo.BlockHeight, int64(0))
	coreBlockHash := strings.TrimSpace(string(realRegtestBitcoinCLI(t, "getblockhash", strconv.FormatInt(stakeInfo.BlockHeight, 10))))
	require.Len(t, coreBlockHash, 64)
	indexedBlockHash, err := issuer.GetIndexerClient().GetBlockHash(int(stakeInfo.BlockHeight))
	require.NoError(t, err)
	require.Equal(t, strings.ToLower(coreBlockHash), strings.ToLower(indexedBlockHash),
		"L1 indexer confirmation must identify the same Bitcoin Core block")

	stakeRaw, err := issuer.GetIndexerClient().GetRawTx(stakeTxID)
	require.NoError(t, err)
	stakeTx, err := wallet.DecodeMsgTx(stakeRaw)
	require.NoError(t, err)
	require.Equal(t, stakeTxID, stakeTx.TxHash().String())
	stakeScript, err := wallet.GetPkScriptFromAddress(stakeAddress)
	require.NoError(t, err)
	stakeVout := -1
	for i, output := range stakeTx.TxOut {
		if !bytes.Equal(output.PkScript, stakeScript) {
			continue
		}
		require.Equal(t, assetAmount, output.Value,
			"Core must create the exact 1,000-sat DOGCOIN collateral output")
		require.Equal(t, -1, stakeVout, "Core stake tx must create one collateral output")
		stakeVout = i
	}
	require.NotEqual(t, -1, stakeVout, "Core stake transaction must pay the fixed Core/Bootstrap P2WSH")
	fundingOutpoint := fmt.Sprintf("%s:%d", stakeTxID, stakeVout)
	indexedStakeOutput, err := issuer.GetIndexerClient().GetTxOutput(fundingOutpoint)
	require.NoError(t, err, "real L1 indexer must expose the exact DOGCOIN collateral output")
	require.NotNil(t, indexedStakeOutput)
	require.Equal(t, fundingOutpoint, indexedStakeOutput.OutPointStr)
	require.Equal(t, assetAmount, indexedStakeOutput.OutValue.Value)
	require.Equal(t, stakeScript, indexedStakeOutput.OutValue.PkScript)
	stakedAsset, err := indexedStakeOutput.Assets.Find(asset)
	require.NoError(t, err, "Core collateral output must carry the live staking asset")
	require.Equal(t, assetAmount, stakedAsset.Amount.Int64())
	require.Equal(t, uint32(1), stakedAsset.BindingSat)
	unspentValue, unspentScript, unspent := bitcoinUnspentAt(t, stakeTxID, uint32(stakeVout))
	require.True(t, unspent, "the exact Core DOGCOIN stake output must remain unspent")
	require.Equal(t, assetAmount, unspentValue)
	require.Equal(t, stakeScript, unspentScript)
	require.True(t, issuer.HasStaked(corePub), "HasStaked must pass from the actual confirmed DOGCOIN UTXO")

	// Validate the actual confirmed L2 Anchor, not the REST AscendData.Assets
	// field (which is intentionally omitted by that endpoint).
	var bootstrapAscend, coreAscend *sindexercommon.AscendData
	require.Eventually(t, func() bool {
		bootstrapAscend, err = indexers[0].GetAscendData(fundingOutpoint)
		if err != nil || bootstrapAscend == nil {
			return false
		}
		coreAscend, err = indexers[1].GetAscendData(fundingOutpoint)
		return err == nil && coreAscend != nil
	}, rpcReadyTimeout, 250*time.Millisecond,
		"both SatoshiNet indexers must confirm the real Core stake Anchor")
	for _, ascend := range []*sindexercommon.AscendData{bootstrapAscend, coreAscend} {
		require.Equal(t, fundingOutpoint, ascend.FundingUtxo)
		require.Equal(t, stakeAddress, ascend.Address)
		require.Greater(t, ascend.Height, 0)
		require.NotEmpty(t, ascend.AnchorTxId)
		require.Equal(t, assetAmount, ascend.Value)
		pairMatches := (bytes.Equal(ascend.PubA, corePub) && bytes.Equal(ascend.PubB, bootstrapPub)) ||
			(bytes.Equal(ascend.PubA, bootstrapPub) && bytes.Equal(ascend.PubB, corePub))
		require.True(t, pairMatches, "Anchor must contain the fixed Core and Bootstrap participants")
	}
	require.Equal(t, *bootstrapAscend, *coreAscend,
		"both nodes must agree on all stake Anchor fields")
	anchorTxID := bootstrapAscend.AnchorTxId
	anchorRaw, err := indexers[0].GetRawTx(anchorTxID)
	require.NoError(t, err, "Bootstrap indexer must retain the confirmed SatoshiNet Anchor")
	anchorTx, err := wallet.DecodeMsgTx_SatsNet(anchorRaw)
	require.NoError(t, err)
	require.Equal(t, anchorTxID, anchorTx.TxID())
	anchorScript, err := wallet.GetPkScriptFromAddress_SatsNet(stakeAddress)
	require.NoError(t, err)
	anchorAssetTotal := int64(0)
	anchorAssetOutputs := 0
	for _, output := range anchorTx.TxOut {
		if output == nil {
			continue
		}
		assetInfo, findErr := output.Assets.Find(asset)
		if findErr != nil || assetInfo == nil {
			continue
		}
		anchorAssetOutputs++
		require.Equal(t, stakeScript, output.PkScript,
			"the actual L2 DOGCOIN Anchor must target the fixed Core/Bootstrap channel")
		require.Equal(t, assetAmount, output.Value)
		require.Equal(t, assetAmount, assetInfo.Amount.Int64())
		require.Equal(t, uint32(1), assetInfo.BindingSat)
		anchorAssetTotal += assetInfo.Amount.Int64()
	}
	require.Equal(t, 1, anchorAssetOutputs, "the Anchor must contain one exact DOGCOIN output")
	require.Equal(t, assetAmount, anchorAssetTotal,
		"the actual Anchor transaction must conserve all 1,000 DOGCOIN")

	var anchorBlockHash string
	for i, client := range indexers {
		info, infoErr := client.GetTxInfo(anchorTxID)
		require.NoError(t, infoErr, "%s indexer must confirm the exact Anchor tx", network.Nodes[i].role)
		require.NotNil(t, info)
		require.Equal(t, anchorTxID, info.TxID)
		require.GreaterOrEqual(t, info.Confirmations, uint64(1))
		require.Equal(t, int64(bootstrapAscend.Height), info.BlockHeight)
		blockHash, hashErr := client.GetBlockHash(bootstrapAscend.Height)
		require.NoError(t, hashErr)
		if i == 0 {
			anchorBlockHash = blockHash
		} else {
			require.Equal(t, anchorBlockHash, blockHash,
				"Core and Bootstrap must confirm the Anchor in the same canonical block")
		}
		raw, rawErr := client.GetRawTx(anchorTxID)
		require.NoError(t, rawErr)
		require.Equal(t, strings.ToLower(anchorRaw), strings.ToLower(raw),
			"both nodes must retain identical signed Anchor bytes")
		confirmedTx, decodeErr := wallet.DecodeMsgTx_SatsNet(raw)
		require.NoError(t, decodeErr)
		require.Equal(t, anchorTx.TxHash(), confirmedTx.TxHash())
	}
	for _, client := range indexers {
		isCore, checkErr := client.IsCoreNode(corePub)
		require.NoError(t, checkErr)
		require.True(t, isCore, "real stake must preserve the built-in Core registry entry")
		info, infoErr := client.GetCoreNodeInfo(corePub)
		require.NoError(t, infoErr)
		require.NotNil(t, info)
		encoded, marshalErr := json.Marshal(info)
		require.NoError(t, marshalErr)
		require.Equal(t, coreRegistryJSON[0], string(encoded),
			"staking must not replace the built-in Core registry identity")
	}

	var l1Height int
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getblockcount"), &l1Height))
	currentStakeAsset := indexercommon.GetStakeAssetName(issuer.GetSyncHeightL1())
	currentStakeAmount := indexercommon.GetStakeAssetAmt(issuer.GetSyncHeightL1())
	require.Equal(t, assetText, currentStakeAsset, "the fixture must remain in the same live stake era")
	require.Equal(t, assetAmount, currentStakeAmount)
	evidence := map[string]string{
		"asset": assetText,
		"amount": strconv.FormatInt(assetAmount, 10),
		"asset_n": strconv.Itoa(ticker.N),
		"total_minted": ticker.TotalMinted,
		"core_address": coreAddress,
		"stake_address": stakeAddress,
		"stake_txid": stakeTxID,
		"stake_outpoint": fundingOutpoint,
		"l1_height": strconv.Itoa(l1Height),
		"l1_block_hash": strings.ToLower(coreBlockHash),
		"anchor_txid": anchorTxID,
		"anchor_height": strconv.Itoa(bootstrapAscend.Height),
		"anchor_block_hash": strings.ToLower(anchorBlockHash),
		"action_reservation_id": strconv.FormatInt(reservationID, 10),
	}
	t.Logf("verified real Core DOGCOIN stake: asset=%s amount=%d core=%s stake_address=%s stake_txid=%s outpoint=%s L1_height=%d L1_block=%s anchor_txid=%s L2_height=%d L2_block=%s dual_node=true",
		assetText, assetAmount, coreAddress, stakeAddress, stakeTxID, fundingOutpoint, l1Height, coreBlockHash,
		anchorTxID, bootstrapAscend.Height, anchorBlockHash)
	return evidence
}

func confirmSDKPWARegtestL1Transactions(t *testing.T, manager *wallet.Manager, txids []string,
	mine func(string, int)) {
	t.Helper()
	require.NotNil(t, manager)
	require.NotEmpty(t, txids)
	require.NotNil(t, mine)
	var mempool []string
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getrawmempool"), &mempool))
	for _, txid := range txids {
		require.Contains(t, mempool, txid,
			"both SDK inscription transactions must be in the real Bitcoin Core mempool before mining")
	}
	mine(manager.GetWallet().GetAddressByIndex(1), 1)
	for _, txid := range txids {
		require.Eventually(t, func() bool {
			info, err := manager.GetIndexerClient().GetTxInfo(txid)
			return err == nil && info != nil && info.Confirmations > 0
		}, rpcReadyTimeout, 200*time.Millisecond, "real L1 indexer did not confirm SDK transaction %s", txid)
	}
}

func mineSDKPWARegtestL1(t *testing.T, manager *wallet.Manager, address string) {
	t.Helper()
	var hashes []string
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "generatetoaddress", "1", address), &hashes))
	require.Len(t, hashes, 1, "confirm only the actual L1 transaction using the existing regtest Core")
	var height int
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getblockcount"), &height))
	require.Eventually(t, func() bool { return manager.GetSyncHeightL1() >= height }, rpcReadyTimeout, time.Second,
		"real L1 indexer did not reach the confirmed Bitcoin Core height")
}

func startSDKPWARegtestMinerNode(t *testing.T, network *realSatoshiNet, mnemonic, minerPubkey string) *testHarness {
	t.Helper()
	portBase := sdkPWARegtestSatoshiNetPortBase(t)
	require.NotNil(t, network)
	require.NotNil(t, network.Bootstrap)
	require.NotNil(t, network.Core)
	require.Len(t, minerPubkey, 66)
	endpoint, err := url.Parse(os.Getenv("SAT20_REAL_L1_INDEXER"))
	require.NoError(t, err)
	artifacts := satoshinetBuildArtifacts(t)
	runtimeDir := os.Getenv("SAT20_REAL_REGTEST_RUNTIME")
	require.True(t, filepath.IsAbs(runtimeDir))
	nodeDir := filepath.Join(runtimeDir, "nodes-anchor", "miner")
	require.NoError(t, os.MkdirAll(nodeDir, 0o700))
	exe := filepath.Join(nodeDir, filepath.Base(artifacts.minerExecutable))
	copySatoshiNetRuntimeFile(t, artifacts.minerExecutable, exe)
	copySatoshiNetRuntimeFile(t, artifacts.minerPlugin, filepath.Join(nodeDir, "wallet.so"))
	stageSatoshiNetNodeConfig(t, nodeDir)
	port := portBase + 20
	addr := func(offset int) string { return fmt.Sprintf("127.0.0.1:%d", port+offset) }
	p2p, rpc, stp, management := addr(0), addr(1), addr(3), addr(4)
	conf := fmt.Sprintf(satoshinetTestConf, satoshinetSTPMode("miner"), endpoint.Host, addr(2), stp, management, mnemonic)
	conf = strings.Replace(conf, "env: test", "env: dev", 1)
	conf = strings.Replace(conf, "chain: testnet", "chain: regtest", 1)
	conf = strings.ReplaceAll(conf, "proxy: testnet", "proxy: regtest")
	conf = strings.Replace(conf, "proxy: regtest", "proxy: btc/regtest", 1)
	seeds := readSDKPWARegtestMnemonics(t)
	bootstrapPubkey := hex.EncodeToString(keyFromMnemonic(t, seeds["bootstrap"], 0).PubKey().SerializeCompressed())
	conf += fmt.Sprintf("\npeers:\n  - %q\n  - %q\n",
		fmt.Sprintf("b@%s@http://127.0.0.1:%d/regtest", bootstrapPubkey, portBase+3),
		fmt.Sprintf("s@%s@http://127.0.0.1:%d/regtest", network.Core.nodePubKey, portBase+13))
	conf += fmt.Sprintf("\npublic_rpc:\n  browser_allowed_origins: [%q]\n", sdkPWARegtestBrowserOrigin(t))
	require.NoError(t, os.WriteFile(filepath.Join(nodeDir, "conf.yaml"), []byte(conf), 0o600))
	args := []string{"--regtest", "--notls", "--nodnsseed", "--txindex", "--generate",
		"--miningpubkey=" + minerPubkey, "--serverpubkey=" + network.Core.nodePubKey,
		"--listen=" + p2p, "--rpclisten=" + rpc, "--rpcuser=user", "--rpcpass=pass",
		"--datadir=" + filepath.Join(nodeDir, "data"), "--logdir=" + filepath.Join(nodeDir, "logs"),
		"--indexerscheme=http", "--indexerhost=" + endpoint.Host, "--indexerproxy=btc/regtest"}
	env := []string{"SATOSHINET_RPCTEST_NODE_ROLE=miner", "SATOSHINET_RPCTEST_STP_MNEMONIC=" + mnemonic,
		"SATOSHINET_RPCTEST_STP_PASSWORD=rpctest"}
	h := newTestHarness(t, exe, nodeDir, p2p, rpc, args, env)
	h.role, h.nodePubKey, h.stpAddr, h.managementAddr = "miner", minerPubkey, stp, management
	require.NoError(t, os.Chmod(h.logFile, 0o600))
	t.Cleanup(func() {
		if h.Client == nil {
			return
		}
		stopCtx, cancel := context.WithTimeout(context.Background(), rpcReadyTimeout)
		defer cancel()
		if err := posPWAStopNode(stopCtx, h); err != nil {
			t.Errorf("normally stop persistent Miner node: %v", err)
		}
	})
	require.NoError(t, connectNode(h, network.Core))
	require.NoError(t, connectNode(h, network.Bootstrap))
	network.Miner = h
	network.Nodes = append(network.Nodes, h)
	require.NoError(t, joinBlocks(network.Nodes))
	waitSDKPWARegtestManagerHealth(t, h)
	waitSDKPWARegtestIndexes(t, network)
	mining, err := h.Client.GetGenerate()
	require.NoError(t, err)
	require.True(t, mining, "the registered A1 Miner must use the existing scheduled producer")
	return h
}

func waitSDKPWARegtestManagerHealth(t *testing.T, node *testHarness) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	require.Eventually(t, func() bool {
		resp, err := client.Get("http://" + node.managementAddr + "/health")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		var managerHealth struct{ Ready bool }
		return resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&managerHealth) == nil && managerHealth.Ready
	}, rpcReadyTimeout, 200*time.Millisecond, "fixed %s STP manager not ready", node.role)
}

func waitSDKPWARegtestIndexes(t *testing.T, network *realSatoshiNet) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	for _, node := range network.Nodes {
		if node.Client == nil {
			continue
		}
		_, want, err := node.Client.GetBestBlock()
		require.NoError(t, err)
		endpoint, err := node.IndexerURL("regtest")
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			resp, err := client.Get(endpoint + "/bestheight")
			if err != nil {
				return false
			}
			defer resp.Body.Close()
			var status struct {
				Code int
				Data struct{ Height int32 }
			}
			return resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&status) == nil &&
				status.Code == 0 && status.Data.Height == want
		}, rpcReadyTimeout, 200*time.Millisecond, "%s L2 indexer did not reach height %d", node.role, want)
	}
}

// Used only by the POS continuation after ordinary-1's Manager and DB have
// been closed before A1 opens that same mnemonic in the Miner plugin DB.
func mineSDKPWARegtestL1WithoutWalletManager(t *testing.T, address string, blocks int) {
	t.Helper()
	var hashes []string
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "generatetoaddress", strconv.Itoa(blocks), address), &hashes))
	require.Len(t, hashes, blocks)
	var height int
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getblockcount"), &height))
	wantHash := strings.TrimSpace(string(realRegtestBitcoinCLI(t, "getblockhash", strconv.Itoa(height))))
	require.Len(t, wantHash, 64)
	l1URL, err := url.Parse(os.Getenv("SAT20_REAL_L1_INDEXER"))
	require.NoError(t, err)
	esploraURL, err := url.Parse(os.Getenv("SAT20_REAL_ESPLORA"))
	require.NoError(t, err)
	client := &http.Client{Timeout: 5 * time.Second}
	require.Eventually(t, func() bool {
		resp, err := client.Get(strings.TrimRight(l1URL.String(), "/") + "/bestheight")
		if err != nil {
			return false
		}
		var indexed struct {
			Code int `json:"code"`
			Data struct {
				Height int `json:"height"`
			} `json:"data"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&indexed)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || decodeErr != nil || indexed.Code != 0 || indexed.Data.Height < height {
			return false
		}
		resp, err = client.Get(strings.TrimRight(esploraURL.String(), "/") + "/blocks/tip/hash")
		if err != nil {
			return false
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return err == nil && resp.StatusCode == http.StatusOK && strings.TrimSpace(string(body)) == wantHash
	}, rpcReadyTimeout, time.Second, "actual L1 indexers did not reach Core height %d after confirmation", height)
}

func blockAtHeight(t *testing.T, node *testHarness, height int32) *wire.MsgBlock {
	t.Helper()
	hash, err := node.Client.GetBlockHash(int64(height))
	require.NoError(t, err)
	block, err := node.Client.GetBlock(hash)
	require.NoError(t, err)
	require.Equal(t, *hash, block.BlockHash())
	return block
}

func findAnchorTx(t *testing.T, block *wire.MsgBlock) *wire.MsgTx {
	t.Helper()
	for _, candidate := range block.Transactions {
		if len(candidate.TxIn) == 0 {
			continue
		}
		anchor, err := wallet.ParseStandardAnchorScript(candidate.TxIn[0].SignatureScript)
		if err == nil && anchor.Utxo != "" {
			return candidate
		}
	}
	require.FailNow(t, "anchor transaction not found in block")
	return nil
}

func findAnchorSpend(t *testing.T, block *wire.MsgBlock, anchor *wire.MsgTx) *wire.MsgTx {
	t.Helper()
	for _, candidate := range block.Transactions {
		for _, input := range candidate.TxIn {
			if input.PreviousOutPoint.Hash == anchor.TxHash() && input.PreviousOutPoint.Index == 0 {
				return candidate
			}
		}
	}
	require.FailNow(t, "anchor output 0 spend not found in block")
	return nil
}

func findAnchorTxInMempool(t *testing.T, node *testHarness, outpoint string) *wire.MsgTx {
	t.Helper()
	pool, err := node.Client.GetRawMempool()
	require.NoError(t, err)
	for _, txid := range pool {
		candidate, err := node.Client.GetRawTransaction(txid)
		if err != nil {
			continue
		}
		msg := candidate.MsgTx()
		if len(msg.TxIn) == 0 {
			continue
		}
		anchor, err := wallet.ParseStandardAnchorScript(msg.TxIn[0].SignatureScript)
		if err == nil && anchor.Utxo == outpoint {
			return msg
		}
	}
	return nil
}

func findAnchorSpendInMempool(t *testing.T, node *testHarness, anchor *wire.MsgTx) *wire.MsgTx {
	t.Helper()
	pool, err := node.Client.GetRawMempool()
	require.NoError(t, err)
	for _, txid := range pool {
		candidate, err := node.Client.GetRawTransaction(txid)
		if err != nil {
			continue
		}
		msg := candidate.MsgTx()
		for _, input := range msg.TxIn {
			if input.PreviousOutPoint.Hash == anchor.TxHash() && input.PreviousOutPoint.Index == 0 {
				return msg
			}
		}
	}
	return nil
}

func requireMempoolTx(t *testing.T, node *testHarness, txid string) {
	t.Helper()
	want, err := chainhash.NewHashFromStr(txid)
	require.NoError(t, err)
	// SDK broadcasts through the core indexer. Propagation to the bootstrap
	// is asynchronous; observe the same tx without regenerating or resending.
	require.Eventually(t, func() bool {
		pool, err := node.Client.GetRawMempool()
		if err != nil {
			return false
		}
		for _, got := range pool {
			if got != nil && *got == *want {
				return true
			}
		}
		return false
	}, rpcReadyTimeout, 200*time.Millisecond, "expected transaction did not reach %s mempool: %s", node.role, txid)
}

func mineExpectedSatsNetTx(t *testing.T, network *realSatoshiNet, txid string, expectedHeight int32) string {
	t.Helper()
	bootstrap := network.Bootstrap.Client
	requireMempoolTx(t, network.Bootstrap, txid)
	_, beforeHeight, err := bootstrap.GetBestBlock()
	require.NoError(t, err)
	require.Equal(t, expectedHeight-1, beforeHeight, "transaction must advance exactly one expected block")
	hashes, err := bootstrap.Generate(1)
	require.NoError(t, err, "Generate must include a pending expected transaction")
	require.Len(t, hashes, 1)
	block, err := bootstrap.GetBlock(hashes[0])
	require.NoError(t, err)
	found := false
	for _, candidate := range block.Transactions {
		if candidate.TxID() == txid {
			found = true
			break
		}
	}
	require.True(t, found, "generated block must contain the expected transaction %s", txid)
	require.Equal(t, *hashes[0], block.BlockHash())
	require.NoError(t, joinBlocks(network.Nodes))
	waitSDKPWARegtestIndexes(t, network)
	bestHash, height, err := bootstrap.GetBestBlock()
	require.NoError(t, err)
	require.Equal(t, expectedHeight, height)
	require.Equal(t, *hashes[0], *bestHash)
	t.Logf("confirmed SatoshiNet transaction txid=%s height=%d blockhash=%s", txid, height, bestHash.String())
	return bestHash.String()
}

func requireSDKPWARegtestTxIndexed(t *testing.T, node *testHarness, txid string, height int32) {
	t.Helper()
	endpoint, err := node.IndexerURL("regtest")
	require.NoError(t, err)
	parsed, err := url.Parse(endpoint)
	require.NoError(t, err)
	indexer := wallet.NewIndexerClient(parsed.Scheme, parsed.Host, "regtest", wallet.NewHTTPClient())
	info, err := indexer.GetTxInfo(txid)
	require.NoError(t, err, "%s indexer must return the confirmed transaction", node.role)
	require.NotNil(t, info)
	require.Equal(t, txid, info.TxID)
	require.GreaterOrEqual(t, info.Confirmations, uint64(1))
	require.Equal(t, int64(height), info.BlockHeight)
}

func waitForScheduledSatsNetTx(t *testing.T, network *realSatoshiNet, txid string, submittedAt time.Time,
	baselineHeight int32, baselineHash *chainhash.Hash, baselineTime time.Time) (string, int32, time.Duration, time.Duration) {
	t.Helper()
	hash, err := chainhash.NewHashFromStr(txid)
	require.NoError(t, err)
	deadline := time.Now().Add(90 * time.Second)
	lastStatus := "not yet visible"
	for time.Now().Before(deadline) {
		verbose, lookupErr := network.Bootstrap.Client.GetRawTransactionVerbose(hash)
		if lookupErr == nil && verbose.Confirmations > 0 && verbose.BlockHash != "" {
			confirmedHash, parseErr := chainhash.NewHashFromStr(verbose.BlockHash)
			require.NoError(t, parseErr)
			confirmedBlock, blockErr := network.Bootstrap.Client.GetBlock(confirmedHash)
			require.NoError(t, blockErr)
			containsTx := false
			for _, candidate := range confirmedBlock.Transactions {
				if candidate.TxID() == txid {
					containsTx = true
					break
				}
			}
			require.True(t, containsTx, "scheduled confirmation block must contain transaction %s", txid)
			require.NotEqual(t, *baselineHash, *confirmedHash, "scheduled confirmation must advance beyond the baseline tip")
			require.NoError(t, joinBlocks(network.Nodes))
			waitSDKPWARegtestIndexes(t, network)
			_, bestHeight, bestErr := network.Bootstrap.Client.GetBestBlock()
			require.NoError(t, bestErr)
			confirmedHeight := int32(-1)
			for height := baselineHeight + 1; height <= bestHeight; height++ {
				block := blockAtHeight(t, network.Bootstrap, height)
				require.Greater(t, len(block.Transactions), 1, "scheduled regtest must not mine empty blocks at height %d", height)
				for _, candidate := range block.Transactions {
					if candidate.TxID() == txid {
						confirmedHeight = height
						require.Equal(t, *confirmedHash, block.BlockHash())
					}
				}
			}
			require.Greater(t, confirmedHeight, baselineHeight, "confirmed transaction must be after the baseline tip")
			requireSDKPWARegtestTxIndexed(t, network.Bootstrap, txid, confirmedHeight)
			requireSDKPWARegtestTxIndexed(t, network.Core, txid, confirmedHeight)
			elapsed := time.Since(submittedAt)
			blockInterval := confirmedBlock.Header.Timestamp.Sub(baselineTime)
			t.Logf("scheduled SDK self-send confirmed txid=%s height=%d blockhash=%s submit_to_confirm=%s block_timestamp_interval=%s", txid, confirmedHeight, confirmedHash.String(), elapsed, blockInterval)
			return confirmedHash.String(), confirmedHeight, elapsed, blockInterval
		}
		if lookupErr != nil {
			lastStatus = lookupErr.Error()
		} else {
			lastStatus = fmt.Sprintf("confirmations=%d block=%s", verbose.Confirmations, verbose.BlockHash)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("scheduled transaction %s was not confirmed within 90 seconds; last status: %s", txid, lastStatus)
	return "", 0, 0, 0
}

func buildRealAnchorSpend(t *testing.T, anchor *wire.MsgTx, witnessScript, recipient []byte,
	first, second *btcec.PrivateKey) *wire.MsgTx {
	t.Helper()
	output := anchor.TxOut[0]
	require.Greater(t, output.Value, int64(1000))
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: anchor.TxHash(), Index: 0}})
	tx.AddTxOut(wire.NewTxOut(output.Value-1000, output.Assets.Clone(), recipient))
	fetcher := txscript.NewCannedPrevOutputFetcher(output.PkScript, output.Value, output.Assets)
	hashes := txscript.NewTxSigHashes(tx, fetcher)
	firstKey, secondKey := first, second
	if bytes.Compare(firstKey.PubKey().SerializeCompressed(), secondKey.PubKey().SerializeCompressed()) > 0 {
		firstKey, secondKey = secondKey, firstKey
	}
	a, err := txscript.RawTxInWitnessSignature(tx, hashes, 0, output.Value, output.Assets, witnessScript, txscript.SigHashAll, firstKey)
	require.NoError(t, err)
	b, err := txscript.RawTxInWitnessSignature(tx, hashes, 0, output.Value, output.Assets, witnessScript, txscript.SigHashAll, secondKey)
	require.NoError(t, err)
	tx.TxIn[0].Witness = wire.TxWitness{nil, a, b, witnessScript}
	engine, err := txscript.NewEngine(output.PkScript, tx, 0, txscript.StandardVerifyFlags, nil, hashes,
		output.Value, output.Assets, fetcher)
	require.NoError(t, err)
	require.NoError(t, engine.Execute())
	return tx
}

func bitcoinUnspentAt(t *testing.T, txid string, vout uint32) (int64, []byte, bool) {
	t.Helper()
	var output *struct {
		Value         float64 `json:"value"`
		Confirmations int     `json:"confirmations"`
		ScriptPubKey  struct {
			Hex string `json:"hex"`
		} `json:"scriptPubKey"`
	}
	raw := realRegtestBitcoinCLI(t, "gettxout", txid, strconv.FormatUint(uint64(vout), 10), "true")
	if strings.TrimSpace(string(raw)) == "null" {
		return 0, nil, false
	}
	require.NoError(t, json.Unmarshal(raw, &output), "decode Bitcoin Core gettxout response")
	require.NotNil(t, output)
	script, err := hex.DecodeString(output.ScriptPubKey.Hex)
	require.NoError(t, err)
	return int64(output.Value*100000000 + 0.5), script, output.Confirmations > 0
}

func existingAnchorOutpoint(t *testing.T, address string, pkScript []byte) (string, uint32, bool) {
	t.Helper()
	descriptors, err := json.Marshal([]string{"addr(" + address + ")"})
	require.NoError(t, err)
	var scan struct {
		Success  bool `json:"success"`
		Unspents []struct {
			TxID         string  `json:"txid"`
			Vout         uint32  `json:"vout"`
			Amount       float64 `json:"amount"`
			ScriptPubKey string  `json:"scriptPubKey"`
		} `json:"unspents"`
	}
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "scantxoutset", "start", string(descriptors)), &scan))
	require.True(t, scan.Success)
	wantScript := hex.EncodeToString(pkScript)
	for _, output := range scan.Unspents {
		value := int64(output.Amount*100000000 + 0.5)
		if strings.EqualFold(output.ScriptPubKey, wantScript) && value == 1000000 {
			return output.TxID, output.Vout, true
		}
	}
	return "", 0, false
}

func pendingAnchorOutpoint(t *testing.T, address string, pkScript []byte) (string, uint32, bool) {
	t.Helper()
	var txids []string
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getrawmempool"), &txids))
	wantScript := hex.EncodeToString(pkScript)
	for _, txid := range txids {
		var tx struct {
			Vout []struct {
				N            uint32  `json:"n"`
				Value        float64 `json:"value"`
				ScriptPubKey struct {
					Hex string `json:"hex"`
				} `json:"scriptPubKey"`
			} `json:"vout"`
		}
		require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getrawtransaction", txid, "true"), &tx))
		for _, output := range tx.Vout {
			value := int64(output.Value*100000000 + 0.5)
			if strings.EqualFold(output.ScriptPubKey.Hex, wantScript) && value == 1000000 {
				return txid, output.N, true
			}
		}
	}
	return "", 0, false
}

func confirmRealAnchorOutput(t *testing.T, manager *wallet.Manager, mineL1 func(address string, blocks int),
	address string, pkScript []byte) (string, string, *wallet.TxOutput) {
	t.Helper()
	txid, vout, found := existingAnchorOutpoint(t, address, pkScript)
	if !found {
		txid, vout, found = pendingAnchorOutpoint(t, address, pkScript)
		if found {
			t.Logf("reusing pending real L1 anchor funding txid=%s", txid)
		}
	}
	if !found {
		var fee int64
		var err error
		txid, fee, err = manager.BatchSendPlainSats(address, 1000000, 1, 2, nil)
		require.NoError(t, err, "SDK real L1 anchor funding transaction")
		require.NotEmpty(t, txid)
		t.Logf("broadcast real L1 funding txid=%s fee=%d", txid, fee)
		vout = 0
	}
	value, script, unspent := bitcoinUnspentAt(t, txid, vout)
	if !unspent {
		mineL1(manager.GetWallet().GetAddressByIndex(1), 1)
		value, script, unspent = bitcoinUnspentAt(t, txid, vout)
	}
	require.True(t, unspent, "real Bitcoin Core must report the anchor output unspent and confirmed")
	require.Equal(t, int64(1000000), value)
	require.Equal(t, pkScript, script)
	point := fmt.Sprintf("%s:%d", txid, vout)
	output, err := manager.GetIndexerClient().GetTxOutput(point)
	require.NoError(t, err, "real SDK indexer lookup for %s", point)
	require.NotNil(t, output)
	require.Equal(t, point, output.OutPointStr)
	require.Equal(t, value, output.OutValue.Value)
	require.Equal(t, pkScript, output.OutValue.PkScript)
	return txid, point, output
}

func verifyAnchorHistory(t *testing.T, network *realSatoshiNet, ordinaryA *wallet.Manager,
	witnessScript []byte, fundingPoint string, fundingOutput *wallet.TxOutput) (*wire.MsgTx, *wire.MsgTx, string, string) {
	t.Helper()
	anchorBlock := blockAtHeight(t, network.Bootstrap, 1)
	anchorTx := findAnchorTx(t, anchorBlock)
	anchorData, err := wallet.ParseStandardAnchorScript(anchorTx.TxIn[0].SignatureScript)
	require.NoError(t, err)
	require.Equal(t, fundingPoint, anchorData.Utxo)
	require.Equal(t, witnessScript, anchorData.WitnessScript)
	require.Equal(t, fundingOutput.OutValue.Value, anchorData.Value)
	require.Equal(t, fundingOutput.OutValue.PkScript, anchorTx.TxOut[0].PkScript)
	anchorBlockHash := anchorBlock.BlockHash().String()
	t.Logf("H1 anchor txid=%s blockhash=%s", anchorTx.TxID(), anchorBlockHash)

	spendBlock := blockAtHeight(t, network.Bootstrap, 2)
	spendTx := findAnchorSpend(t, spendBlock, anchorTx)
	require.Len(t, spendTx.TxIn, 1)
	require.Len(t, spendTx.TxOut, 1)
	require.Equal(t, fundingOutput.OutValue.Value-1000, spendTx.TxOut[0].Value)
	recipientScript, err := wallet.GetPkScriptFromAddress(ordinaryA.GetWallet().GetAddress())
	require.NoError(t, err)
	require.Equal(t, recipientScript, spendTx.TxOut[0].PkScript)
	require.Len(t, spendTx.TxIn[0].Witness, 4)
	require.Equal(t, witnessScript, spendTx.TxIn[0].Witness[3])
	require.NotEmpty(t, spendTx.TxIn[0].Witness[1])
	require.NotEmpty(t, spendTx.TxIn[0].Witness[2])
	spendBlockHash := spendBlock.BlockHash().String()
	t.Logf("H2 anchor:0 double-sign spend txid=%s blockhash=%s", spendTx.TxID(), spendBlockHash)
	return anchorTx, spendTx, anchorBlockHash, spendBlockHash
}

func requireBIP9Active(t *testing.T, network *realSatoshiNet) {
	t.Helper()
	for _, node := range network.Nodes {
		info, err := node.Client.GetBlockChainInfo()
		require.NoError(t, err)
		require.Equal(t, "regtest", info.Chain)
		require.GreaterOrEqual(t, info.Blocks, int32(3))
		for _, deployment := range []string{"csv", "segwit", "taproot"} {
			require.NotNil(t, info.SoftForks)
			require.NotNil(t, info.SoftForks.Bip9SoftForks[deployment], deployment)
			require.Equal(t, "active", info.SoftForks.Bip9SoftForks[deployment].Status, deployment)
		}
	}
}

func TestSDKPWARealRegtestNodeReadiness(t *testing.T) {
	network := prepareSDKPWARegtestNodes(t)
	// H1/H2/H3 are deliberately generated with their expected transactions.
	// The shared node starter enables scheduled mining for business journeys;
	// pause it here before preparing or broadcasting the bootstrap transactions.
	for _, node := range network.Nodes {
		require.NoError(t, node.Client.SetGenerate(false, 1))
		mining, err := node.Client.GetGenerate()
		require.NoError(t, err)
		require.False(t, mining, "discrete bootstrap generation requires scheduled mining to be stopped")
	}
	ordinaryA, _, mineL1 := prepareSDKPWARegtestAssets(t)
	seeds := readSDKPWARegtestMnemonics(t)
	bootstrapKey := keyFromMnemonic(t, seeds["bootstrap"], 0)
	coreKey := keyFromMnemonic(t, seeds["core"], 0)
	bootstrapWallet := wallet.NewInternalWalletWithMnemonic(seeds["bootstrap"], "", wallet.GetChainParam())
	require.NotNil(t, bootstrapWallet)
	require.Equal(t, bootstrapKey.PubKey().SerializeCompressed(), bootstrapWallet.GetPaymentPubKey().SerializeCompressed())
	bootstrapPub := bootstrapKey.PubKey().SerializeCompressed()
	corePub := coreKey.PubKey().SerializeCompressed()
	witnessScript, pkScript, err := wallet.GetP2WSHscript(bootstrapPub, corePub)
	require.NoError(t, err)
	anchorAddress, err := wallet.GetP2WSHaddressFromScript(pkScript)
	require.NoError(t, err)

	_, height, err := network.Bootstrap.Client.GetBestBlock()
	require.NoError(t, err)
	require.GreaterOrEqual(t, height, int32(0))
	var fundingTxID, fundingPoint string
	var fundingOutput *wallet.TxOutput
	if height == 0 {
		fundingTxID, fundingPoint, fundingOutput = confirmRealAnchorOutput(t, ordinaryA, mineL1, anchorAddress, pkScript)
		bootstrapPending := findAnchorTxInMempool(t, network.Bootstrap, fundingPoint)
		corePending := findAnchorTxInMempool(t, network.Core, fundingPoint)
		if bootstrapPending != nil && corePending != nil {
			require.Equal(t, bootstrapPending.TxID(), corePending.TxID(), "both node mempools must agree on the pending real-L1 anchor")
		}
		var anchorTx *wire.MsgTx
		var anchorTxID string
		if bootstrapPending != nil {
			anchorTx = bootstrapPending
			anchorTxID = bootstrapPending.TxID()
			t.Logf("reusing existing bootstrap-mempool anchor txid=%s", anchorTxID)
		} else if corePending != nil {
			anchorTx = corePending
			anchorTxID = corePending.TxID()
			t.Logf("reusing existing core-mempool anchor txid=%s; waiting for bootstrap relay", anchorTxID)
			requireMempoolTx(t, network.Bootstrap, anchorTxID)
		} else {
			ticker := ordinaryA.GetTickerInfoV2(wallet.PLAIN_ASSET.String())
			require.NotNil(t, ticker)
			bindOutputs, err := ordinaryA.AnchorOutputsActive()
			require.NoError(t, err)
			anchorTx, err = wallet.CreateAnchorTx(fundingOutput, &wallet.PLAIN_ASSET, ticker, witnessScript, nil, nil, bindOutputs)
			require.NoError(t, err)
			_, err = ordinaryA.SignAnchorTx(anchorTx, bootstrapWallet.SignMessage)
			require.NoError(t, err)
			anchorTxID = anchorTx.TxID()
			broadcastID, err := ordinaryA.BroadcastTx_SatsNet(anchorTx)
			require.NoError(t, err, "SDK broadcast of real-L1-funded anchor")
			require.Equal(t, anchorTxID, broadcastID)
		}
		h1Hash := mineExpectedSatsNetTx(t, network, anchorTxID, 1)
		require.NotEmpty(t, h1Hash)
	} else {
		t.Logf("resuming existing nodes-anchor chain at height=%d without a new L1 deposit or H1 anchor", height)
	}

	// A persistent H1 already carries its real L1 outpoint. Reuse it and check
	// Core plus the SDK's L1 indexer instead of issuing another deposit.
	anchorTx := findAnchorTx(t, blockAtHeight(t, network.Bootstrap, 1))
	anchorData, err := wallet.ParseStandardAnchorScript(anchorTx.TxIn[0].SignatureScript)
	require.NoError(t, err)
	fundingPoint = anchorData.Utxo
	parts := strings.Split(fundingPoint, ":")
	require.Len(t, parts, 2)
	vout64, err := strconv.ParseUint(parts[1], 10, 32)
	require.NoError(t, err)
	value, script, unspent := bitcoinUnspentAt(t, parts[0], uint32(vout64))
	require.True(t, unspent, "H1 anchor must reference an unspent real Bitcoin output")
	require.Equal(t, anchorData.Value, value)
	require.Equal(t, pkScript, script)
	fundingOutput, err = ordinaryA.GetIndexerClient().GetTxOutput(fundingPoint)
	require.NoError(t, err)
	require.NotNil(t, fundingOutput)
	require.Equal(t, value, fundingOutput.OutValue.Value)
	require.Equal(t, pkScript, fundingOutput.OutValue.PkScript)
	fundingTxID = parts[0]
	t.Logf("real L1 funding txid=%s outpoint=%s value=%d", fundingTxID, fundingPoint, value)

	_, height, err = network.Bootstrap.Client.GetBestBlock()
	require.NoError(t, err)
	var h2Hash string
	if height < 2 {
		require.Equal(t, int32(1), height, "H2 must follow the confirmed H1 anchor")
		recipient, err := wallet.GetPkScriptFromAddress(ordinaryA.GetWallet().GetAddress())
		require.NoError(t, err)
		spendTx := findAnchorSpendInMempool(t, network.Bootstrap, anchorTx)
		if spendTx == nil {
			spendTx = buildRealAnchorSpend(t, anchorTx, witnessScript, recipient, bootstrapKey, coreKey)
			hash, err := network.Bootstrap.Client.SendRawTransaction(spendTx, true)
			require.NoError(t, err, "broadcast the two-operator-signed anchor:0 spend")
			require.Equal(t, spendTx.TxHash(), *hash)
		}
		h2Hash = mineExpectedSatsNetTx(t, network, spendTx.TxID(), 2)
	} else {
		_, _, _, h2Hash = verifyAnchorHistory(t, network, ordinaryA, witnessScript, fundingPoint, fundingOutput)
	}

	_, height, err = network.Bootstrap.Client.GetBestBlock()
	require.NoError(t, err)
	var h3Hash string
	if height < 3 {
		require.Equal(t, int32(2), height, "H3 must follow the confirmed H2 channel spend")
		balance := ordinaryA.GetAssetBalance_SatsNet("", wallet.ParseAssetString(wallet.PLAIN_ASSET.String()))
		require.NotNil(t, balance, "ordinary-1 must receive the H2 anchor spend")
		require.Greater(t, balance.Sign(), 0)
		sendTx, err := ordinaryA.SendAssets_SatsNet(ordinaryA.GetWallet().GetAddress(), wallet.PLAIN_ASSET.String(), "10000", nil)
		require.NoError(t, err, "ordinary-1 SDK self-send for H3")
		h3Hash = mineExpectedSatsNetTx(t, network, sendTx.TxID(), 3)
	} else {
		_, _, _, h2Hash = verifyAnchorHistory(t, network, ordinaryA, witnessScript, fundingPoint, fundingOutput)
		h3Hash = blockAtHeight(t, network.Bootstrap, 3).BlockHash().String()
		t.Logf("existing H3 blockhash=%s; issuing one ordinary-1 SDK self-send for the persistent-chain reuse check", h3Hash)
		balance := ordinaryA.GetAssetBalance_SatsNet("", wallet.ParseAssetString(wallet.PLAIN_ASSET.String()))
		require.NotNil(t, balance)
		require.Greater(t, balance.Sign(), 0)
		sendTx, err := ordinaryA.SendAssets_SatsNet(ordinaryA.GetWallet().GetAddress(), wallet.PLAIN_ASSET.String(), "10000", nil)
		require.NoError(t, err, "ordinary-1 SDK self-send on the existing anchor chain")
		_, beforeHeight, err := network.Bootstrap.Client.GetBestBlock()
		require.NoError(t, err)
		h3Hash = mineExpectedSatsNetTx(t, network, sendTx.TxID(), beforeHeight+1)
	}
	t.Logf("L1 funding txid=%s; L2 anchor txid=%s; H1/H2/H3 blockhashes=%s/%s/%s", fundingTxID, anchorTx.TxID(),
		blockAtHeight(t, network.Bootstrap, 1).BlockHash().String(), h2Hash, h3Hash)
	requireBIP9Active(t, network)

	baselineHash, baselineHeight, err := network.Bootstrap.Client.GetBestBlock()
	require.NoError(t, err)
	baselineBlock := blockAtHeight(t, network.Bootstrap, baselineHeight)
	require.Equal(t, *baselineHash, baselineBlock.BlockHash())
	for _, node := range network.Nodes {
		require.NoError(t, node.Client.SetGenerate(true, 1), "enable scheduled mining after transaction-gated H1/H2/H3")
		var mining bool
		require.Eventually(t, func() bool {
			var err error
			mining, err = node.Client.GetGenerate()
			return err == nil && mining
		}, rpcReadyTimeout, 200*time.Millisecond, "%s scheduled miner did not start", node.role)
		require.True(t, mining)
	}
	submittedAt := time.Now()
	scheduledSend, err := ordinaryA.SendAssets_SatsNet(ordinaryA.GetWallet().GetAddress(), wallet.PLAIN_ASSET.String(), "10000", nil)
	require.NoError(t, err, "ordinary-1 SDK self-send through scheduled mining")
	scheduledTxID := scheduledSend.TxID()
	scheduledBlockHash, scheduledHeight, submitToConfirm, blockTimestampInterval := waitForScheduledSatsNetTx(t,
		network, scheduledTxID, submittedAt, baselineHeight, baselineHash, baselineBlock.Header.Timestamp)
	t.Logf("scheduled mining evidence baseline_height=%d baseline_hash=%s confirmed_height=%d confirmed_hash=%s submit_to_confirm=%s block_timestamp_interval=%s",
		baselineHeight, baselineHash.String(), scheduledHeight, scheduledBlockHash, submitToConfirm, blockTimestampInterval)

	before, beforeHeight, err := network.Core.Client.GetBestBlock()
	require.NoError(t, err)
	restartCtx, cancelRestart := context.WithTimeout(context.Background(), rpcReadyTimeout)
	defer cancelRestart()
	require.NoError(t, posPWAStopNode(restartCtx, network.Core), "stop Core normally before same-DB restart")
	require.NoError(t, posPWARestartNode(restartCtx, network.Core), "restart Core with its existing database")
	after, afterHeight, err := network.Core.Client.GetBestBlock()
	require.NoError(t, err)
	require.Equal(t, beforeHeight, afterHeight, "same-DB Core restart must preserve the immediate best-block height before peer reconnection")
	require.Equal(t, *before, *after, "same-DB Core restart must preserve the immediate best-block hash before peer reconnection")
	t.Logf("same-DB normal restart tip before_height=%d before_hash=%s after_height=%d after_hash=%s", beforeHeight, before, afterHeight, after)
	require.NoError(t, connectNode(network.Core, network.Bootstrap))
	require.NoError(t, joinBlocks(network.Nodes))
	waitSDKPWARegtestIndexes(t, network)
	waitSDKPWARegtestManagerHealth(t, network.Core)
	var mining bool
	require.Eventually(t, func() bool {
		var err error
		mining, err = network.Core.Client.GetGenerate()
		return err == nil && mining
	}, rpcReadyTimeout, 200*time.Millisecond, "scheduled miner did not resume after Core restart")
	require.True(t, mining, "scheduled miner must be enabled again after restart")
}
