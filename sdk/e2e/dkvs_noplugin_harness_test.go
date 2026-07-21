package e2e

import (
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

var (
	dkvsNoPluginBuildMu       sync.Mutex
	dkvsNoPluginTestArtifacts satoshinetArtifacts
)

// dkvsNoPluginBuildArtifacts builds public test binaries that do not depend on
// the private Transcend STP plugin. DKVS and template AUTOPAY behavior are
// provided by SatoshiNet itself; only the miner wallet plugin is required.
func dkvsNoPluginBuildArtifacts(t *testing.T) satoshinetArtifacts {
	t.Helper()
	dkvsNoPluginBuildMu.Lock()
	defer dkvsNoPluginBuildMu.Unlock()
	if dkvsNoPluginTestArtifacts.coreExecutable != "" {
		return dkvsNoPluginTestArtifacts
	}

	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	workspace := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	satoshinetDir := filepath.Join(workspace, "satoshinet")
	require.FileExists(t, filepath.Join(satoshinetDir, "go.mod"))
	sdkPluginDir := filepath.Join(workspace, "sat20wallet", "sdk", "plugin")
	require.FileExists(t, filepath.Join(sdkPluginDir, "main.go"))

	outputDir := filepath.Join(os.TempDir(), "sat20wallet-satoshinet-dkvs-rpctest")
	require.NoError(t, os.MkdirAll(outputDir, 0o755))

	artifacts := satoshinetArtifacts{
		coreExecutable:  filepath.Join(outputDir, "satoshinet-core-dkvs-rpctest"),
		minerExecutable: filepath.Join(outputDir, "satoshinet-miner-dkvs-rpctest"),
		minerPlugin:     filepath.Join(outputDir, "wallet.so"),
	}
	if runtime.GOOS == "windows" {
		artifacts.coreExecutable += ".exe"
		artifacts.minerExecutable += ".exe"
	}

	cmd := exec.Command("go", "build", "-buildmode=plugin", "-o", artifacts.minerPlugin, "main.go")
	cmd.Dir = sdkPluginDir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))

	cmd = exec.Command("go", "build", "-tags=rpctest,wallet_plugin", "-o", artifacts.minerExecutable,
		"github.com/sat20-labs/satoshinet")
	cmd.Dir = satoshinetDir
	output, err = cmd.CombinedOutput()
	require.NoError(t, err, string(output))

	cmd = exec.Command("go", "build", "-tags=rpctest", "-o", artifacts.coreExecutable,
		"github.com/sat20-labs/satoshinet")
	cmd.Dir = satoshinetDir
	output, err = cmd.CombinedOutput()
	require.NoError(t, err, string(output))

	dkvsNoPluginTestArtifacts = artifacts
	return dkvsNoPluginTestArtifacts
}

func stageDKVSNoPluginNodeRuntime(t *testing.T, role, mnemonic, l1IndexerHost, l2IndexerHost, rpcHost,
	managementHost, nodeDir string) string {
	t.Helper()
	artifacts := dkvsNoPluginBuildArtifacts(t)
	executable := artifacts.coreExecutable
	if role == "miner" {
		executable = artifacts.minerExecutable
		copySatoshiNetRuntimeFile(t, artifacts.minerPlugin, filepath.Join(nodeDir, "wallet.so"))
	}

	stagedExecutable := filepath.Join(nodeDir, filepath.Base(executable))
	copySatoshiNetRuntimeFile(t, executable, stagedExecutable)
	require.NoError(t, os.WriteFile(filepath.Join(nodeDir, "conf.yaml"), []byte(fmt.Sprintf(satoshinetTestConf,
		l1IndexerHost, l2IndexerHost, rpcHost, managementHost, mnemonic)), 0o600))
	return stagedExecutable
}

func startDKVSNoPluginNodeWithArgs(t *testing.T, fakeL1 *fakeL1Indexer, role, mnemonic string,
	extraArgs []string) *testHarness {
	t.Helper()

	nodeKey := keyFromMnemonic(t, mnemonic, 0)
	nodePubKey := hex.EncodeToString(nodeKey.PubKey().SerializeCompressed())
	p2pAddr, rpcAddr, managementAddr := nextNodeAddresses(t)
	nodeDir := t.TempDir()
	dataDir := filepath.Join(nodeDir, "data")
	logDir := filepath.Join(nodeDir, "logs")
	args := []string{
		"--notls",
		"--nocheckpoints",
		"--nodnsseed",
		"--testnet",
		"--listen=" + p2pAddr,
		"--rpclisten=" + rpcAddr,
		"--rpcuser=user",
		"--rpcpass=pass",
		"--datadir=" + dataDir,
		"--logdir=" + logDir,
		"--indexerscheme=http",
		"--indexerhost=" + fakeL1.host(),
		"--indexerproxy=testnet",
	}
	if role != "miner" {
		args = append(args, "--generate", "--miningpubkey="+nodePubKey)
	}
	args = append(args, extraArgs...)
	env := []string{
		"SATOSHINET_RPCTEST_NODE_ROLE=" + role,
		"SATOSHINET_RPCTEST_STP_MNEMONIC=" + mnemonic,
		"SATOSHINET_RPCTEST_STP_PASSWORD=rpctest",
	}
	for _, name := range []string{
		"SATOSHINET_POS_MINER_INTERVAL",
		"SATOSHINET_POS_PREWARNING_INTERVAL",
		"SATOSHINET_POS_CHECKING_INTERVAL",
	} {
		if value := os.Getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}

	harness := newTestHarness(t, stageDKVSNoPluginNodeRuntime(t, role, mnemonic, fakeL1.host(),
		l2IndexerHost(t, rpcAddr), rpcAddr, managementAddr, nodeDir), nodeDir, p2pAddr, rpcAddr, args, env)
	t.Logf("started public DKVS %s node: pid=%d rpc=%s p2p=%s log=%s", role, harness.NodePID(),
		harness.RPCAddress(), harness.P2PAddress(), harness.LogFile())
	return harness
}

func newDKVSNoPluginNetworkWithArgs(t *testing.T, fakeL1 *fakeL1Indexer, bootstrapArgs, coreArgs,
	minerArgs []string) *realSatoshiNet {
	t.Helper()
	configureFastPOSTimers(t)

	bootstrap := startDKVSNoPluginNodeWithArgs(t, fakeL1, "bootstrap", bootstrapMnemonic, bootstrapArgs)
	core := startDKVSNoPluginNodeWithArgs(t, fakeL1, "core", coreMnemonic, coreArgs)
	miner := startDKVSNoPluginNodeWithArgs(t, fakeL1, "miner", minerMnemonic, minerArgs)

	require.NoError(t, connectNode(core, bootstrap))
	require.NoError(t, connectNode(miner, bootstrap))
	nodes := []*testHarness{bootstrap, core, miner}
	require.NoError(t, joinBlocks(nodes))
	return &realSatoshiNet{Bootstrap: bootstrap, Core: core, Miner: miner, Nodes: nodes, fakeL1: fakeL1}
}

func newDKVSNoPluginTemplateFixtureWithArgs(t *testing.T, assets map[string]int64, bootstrapArgs,
	coreArgs, minerArgs []string) *templateFixture {
	t.Helper()
	profiles := make([]templateAssetProfile, 0, len(assets))
	for _, asset := range sortedAssetNames(assets) {
		profiles = append(profiles, templateAssetProfile{Name: asset, Asset: asset, Precision: 0,
			Supply: fmt.Sprintf("%d", assets[asset])})
	}
	return newDKVSNoPluginTemplateFixtureWithProfilesAndArgs(t, profiles, bootstrapArgs, coreArgs, minerArgs)
}

func newDKVSNoPluginTemplateFixtureWithProfilesAndArgs(t *testing.T, profiles []templateAssetProfile,
	bootstrapArgs, coreArgs, minerArgs []string) *templateFixture {
	t.Helper()
	oldEnableTesting := indexercommon.ENABLE_TESTING
	indexercommon.ENABLE_TESTING = true
	t.Cleanup(func() { indexercommon.ENABLE_TESTING = oldEnableTesting })

	const lockedValue = int64(20_000_000)
	gas := contractcommon.GetGasAssetName()
	bootstrapKey := keyFromMnemonic(t, bootstrapMnemonic, 0)
	coreKey := keyFromMnemonic(t, coreMnemonic, 0)
	actorA := newTemplateActor(t, keyFromMnemonic(t, bootstrapMnemonic, 1))
	actorB := newTemplateActor(t, keyFromMnemonic(t, bootstrapMnemonic, 2))
	actorC := newTemplateActor(t, keyFromMnemonic(t, bootstrapMnemonic, 3))
	actorD := newTemplateActor(t, keyFromMnemonic(t, bootstrapMnemonic, 4))
	actorE := newTemplateActor(t, keyFromMnemonic(t, bootstrapMnemonic, 5))
	actorF := newTemplateActor(t, keyFromMnemonic(t, bootstrapMnemonic, 6))

	witnessScript, lockedPkScript, err := getP2WSHScript(bootstrapKey.PubKey().SerializeCompressed(),
		coreKey.PubKey().SerializeCompressed())
	require.NoError(t, err)

	l1Assets := []fakeL1Asset{{
		Utxo: templateLockedOutPoint("gas", 0), Value: lockedValue,
		Assets: map[string]string{gas: "100000000"},
	}}
	profileMap := make(map[string]templateAssetProfile, len(profiles))
	for i, profile := range sortedProfiles(profiles) {
		profileMap[profile.Asset] = profile
		l1Assets = append(l1Assets, fakeL1Asset{
			Utxo: templateLockedOutPoint(profile.Asset, i+1), Value: lockedValue,
			Assets: map[string]string{profile.Asset: profile.Supply},
			Metadata: map[string]fakeL1AssetMeta{profile.Asset: {
				Precision: profile.Precision, BindingSat: profile.BindingSat,
			}},
		})
	}
	fakeL1 := newFakeL1Indexer(t, hex.EncodeToString(bootstrapKey.PubKey().SerializeCompressed()),
		lockedPkScript, l1Assets)
	network := newDKVSNoPluginNetworkWithArgs(t, fakeL1, bootstrapArgs, coreArgs, minerArgs)

	gasAnchor := buildAnchorTx(t, templateLockedOutPoint("gas", 0), lockedValue,
		txAsset(gas, 100000000), gas+"-100000000-0-0", witnessScript, bootstrapKey, actorA.PkScript)
	network.sendAndMine(t, gasAnchor, 1)

	assetAnchors := make(map[string]*wire.MsgTx)
	for i, profile := range sortedProfiles(profiles) {
		anchor := buildAnchorTx(t, templateLockedOutPoint(profile.Asset, i+1), lockedValue,
			txAssetProfile(t, profile, profile.Supply), fmt.Sprintf("%s-%s-%d-%d", profile.Asset,
				profile.Supply, profile.Precision, profile.BindingSat), witnessScript, bootstrapKey, actorA.PkScript)
		network.sendAndMine(t, anchor, 1)
		assetAnchors[profile.Asset] = anchor
	}

	return &templateFixture{Network: network, A: actorA, B: actorB, C: actorC, D: actorD, E: actorE,
		F: actorF, gasAnchor: gasAnchor, assetAnchors: assetAnchors, profiles: profileMap}
}
