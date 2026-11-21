//go:build pwa

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	scommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"github.com/tyler-smith/go-bip39"
)

// Uses the existing SDK network and PWA runner. Only L1 indexer evidence is
// controlled: nodes, POS, STP, WASM, signatures, L2 indexing and browser stores
// run their production implementations. No public endpoint or wallet is used.
func TestSDKWalletPWAConnectedBrowser(t *testing.T) {
	// Reuse the existing Node runner's strict case selector for targeted retries.
	// An empty setting retains the full gate; unknown names fail in that runner.
	var cases []string
	if selection := os.Getenv("SAT20_PWA_E2E_CASES"); selection != "" {
		require.NoError(t, json.Unmarshal([]byte(selection), &cases))
		require.NotEmpty(t, cases, "an explicit case selection must not run an empty gate")
	}
	runSDKWalletPWAIsolatedGroups(t, cases)
}

func runSDKWalletPWAIsolatedGroups(t *testing.T, selected []string) {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	pwa := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "pwa"))
	cmd := exec.Command("node", "--input-type=module", "-e", "import {walletPWAGroups,walletPWARuntimeCases} from './scripts/verify/wallet-pwa-groups.mjs'; process.stdout.write(JSON.stringify({groups:walletPWAGroups,runtime:walletPWARuntimeCases}))")
	cmd.Dir = pwa
	output, err := cmd.Output()
	require.NoError(t, err, "read existing PWA group registry")
	type pwaGroup struct {
		Name     string   `json:"name"`
		Cases    []string `json:"cases"`
		Optional []string `json:"optional"`
	}
	var registry struct {
		Groups  []pwaGroup `json:"groups"`
		Runtime []string   `json:"runtime"`
	}
	require.NoError(t, json.Unmarshal(output, &registry))
	groups := registry.Groups
	require.NotEmpty(t, groups, "zero PWA groups must fail")
	known := make(map[string]bool)
	for _, group := range groups {
		for _, name := range append(append([]string{}, group.Cases...), group.Optional...) {
			require.False(t, known[name], "duplicate registered case: %s", name)
			known[name] = true
		}
	}
	for _, name := range selected {
		require.True(t, known[name], "unknown selected case: %s", name)
	}
	executed := 0
	for _, group := range groups {
		cases := append([]string{}, group.Cases...)
		if len(selected) != 0 {
			cases = nil
			for _, name := range append(append([]string{}, group.Cases...), group.Optional...) {
				for _, requested := range selected {
					if name == requested {
						cases = append(cases, name)
					}
				}
			}
		}
		if len(cases) == 0 {
			continue
		}
		t.Run(group.Name, func(t *testing.T) {
			executed++
			defer failUnexpectedPWASkip(t)
			started := time.Now()
			defer func() { t.Logf("PWA isolated group %s elapsed=%s", group.Name, time.Since(started)) }()
			if group.Name == "pos" && len(selected) == 0 {
				t.Setenv("SAT20_PWA_COMPLETE_POS_GROUP", "1")
			}
			runSDKWalletPWABrowser(t, append(cases, registry.Runtime...)...)
		})
	}
	require.Positive(t, executed, "zero selected PWA groups must fail")
}

func runSDKWalletPWABrowser(t *testing.T, cases ...string) {
	runSDKWalletBrowserMode(t, false, cases...)
}

// Direct WASM calls share the existing real-node/browser fixture. No second
// server, node topology or exported production API is introduced.
func TestSDKWASMInterfacesE2E(t *testing.T) {
	runSDKWalletBrowserMode(t, true)
}

func TestSDKWalletPWAForceCloseCSV(t *testing.T) {
	runSDKWalletPWABrowser(t,
		"POS PWA: opening confirms the signed Anchor after pending reload",
		"POS PWA: BTC splicing-in survives closing and reopening the page",
		"Escape PWA: confirmed force close waits for CSV and returns exact wallet funds after reload")
}

func runSDKWalletBrowserMode(t *testing.T, sdkWASM bool, cases ...string) {
	t.Helper()
	fixtureStarted := time.Now()
	var teardownStarted time.Time
	var ownedNetwork *realSatoshiNet
	t.Cleanup(func() {
		if ownedNetwork != nil {
			for _, node := range ownedNetwork.Nodes {
				require.Nil(t, node.cmd, "owned node command must be waited before group cleanup")
				require.Nil(t, node.Client, "owned RPC client must close")
			}
		}
		t.Logf("PWA phase cleanup=%s", time.Since(teardownStarted))
	})
	defer func() { teardownStarted = time.Now() }()
	t.Setenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT", strconv.Itoa(int(posPWAActivationHeight)))
	if os.Getenv("SATOSHINET_POS_MINER_INTERVAL") == "" {
		t.Setenv("SATOSHINET_POS_MINER_INTERVAL", "3")
	}
	if os.Getenv("SATOSHINET_POS_PREWARNING_INTERVAL") == "" {
		t.Setenv("SATOSHINET_POS_PREWARNING_INTERVAL", "3")
	}
	configureFastPOSTimers(t)
	previousTesting := indexercommon.ENABLE_TESTING
	indexercommon.ENABLE_TESTING = true
	t.Cleanup(func() { indexercommon.ENABLE_TESTING = previousTesting })

	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	sdk := filepath.Dir(filepath.Dir(source))
	pwa := filepath.Join(sdk, "..", "pwa")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	if deadline, ok := t.Deadline(); ok {
		// Cancel the child before testing's process-wide alarm, leaving time for
		// the existing node/browser cleanup and failure evidence to complete.
		var deadlineCancel context.CancelFunc
		ctx, deadlineCancel = context.WithDeadline(ctx, deadline.Add(-30*time.Second))
		defer deadlineCancel()
	}
	artifactDir := testCaseDirectory(t)
	// Pick the PWA origin before starting STP, which permits only explicit browser
	// origins. Strict Vite binding below makes a port collision fail visibly.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	browserPort := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	browserOrigin := fmt.Sprintf("http://127.0.0.1:%d", browserPort)
	wasmPath, wasmRuntimePath := buildPWAWalletRuntime(t, ctx, sdk, posPWAActivationHeight)

	key := func(mnemonic string) *btcec.PrivateKey { return keyFromMnemonic(t, mnemonic, 0) }
	pub := func(k *btcec.PrivateKey) string { return hex.EncodeToString(k.PubKey().SerializeCompressed()) }
	bootKey, coreKey, minerKey := key(bootstrapMnemonic), key(coreMnemonic), key(minerMnemonic)
	l1 := newPOSPWAL1Indexer(t, pub(bootKey), map[string]string{pub(coreKey): pub(bootKey), pub(minerKey): pub(coreKey)})
	identity := func(label string) map[string]string {
		entropy := sha256.Sum256([]byte("sat20wallet-pwa-e2e:" + label))
		mnemonic, err := bip39.NewMnemonic(entropy[:16])
		require.NoError(t, err)
		actor := newDKVSKeyPathActor(t, key(mnemonic))
		return map[string]string{"mnemonic": mnemonic, "address": actor.Address,
			"pk_script": hex.EncodeToString(actor.PkScript), "password": "pwa-local-" + label}
	}
	owner, recipient, basic := identity("pos-owner"), identity("recipient"), identity("basic-wallet")
	// Independent protocol actors keep failures from spending the next case's
	// funds. These are initial holdings, never fabricated operation results.
	protocolWallets := make(map[string]map[string]any)
	for _, spec := range []struct {
		id, name, balance, send, label string
		precision                      int
		invalid                        bool
	}{
		{"brc_transfer", "brc20:f:pwbt", "250", "250", "BRC20", 0, false},
		{"brc_balance", "brc20:f:pwbb", "1000", "125", "BRC20", 0, true},
		{"runes", "runes:f:PWA•ACCEPTANCE", "100", "12.34", "Runes", 2, false},
	} {
		from, to := identity(spec.id+"-sender"), identity(spec.id+"-receiver")
		for _, actor := range []map[string]string{from, to} {
			for i := 0; i < 8; i++ {
				l1.FundAddress(t, actor["address"], 1_000_000, nil)
			}
		}
		allocation := displayAssetWithMeta(spec.name, spec.balance, spec.precision, 0)
		if spec.label == "BRC20" {
			allocation.Offsets = indexercommon.AssetOffsets{{Start: 0, End: 1}}
			allocation.OffsetToAmts = []*indexercommon.OffsetToAmount{{Offset: 0, Amount: spec.balance}}
			allocation.Invalid = spec.invalid
		}
		point := l1.FundAddress(t, from["address"], 10_000, []*indexercommon.DisplayAsset{allocation})
		protocolWallets[spec.id] = map[string]any{"sender": from, "recipient": to,
			"asset": spec.name, "balance": spec.balance, "amount": spec.send,
			"precision": spec.precision, "label": spec.label, "seed_outpoint": point}
	}
	dappWallet, dappRecipient := identity("dapp-transactions"), identity("dapp-transactions-recipient")
	directSender, directRecipient := identity("rgb-direct-sender"), identity("rgb-direct-recipient")
	for _, actor := range []map[string]string{dappWallet, dappRecipient, directSender, directRecipient} {
		for i := 0; i < 8; i++ {
			l1.FundAddress(t, actor["address"], 1_000_000, nil)
		}
	}
	coreStakeWallet, minerStakeWallet := identity("core-stake-wallet"), identity("miner-stake-wallet")
	coreNodeActor := newDKVSKeyPathActor(t, coreKey)
	coreNodeWallet := map[string]string{"mnemonic": coreMnemonic, "address": coreNodeActor.Address,
		"pk_script": hex.EncodeToString(coreNodeActor.PkScript), "password": "pwa-local-core-node-guard"}
	stakeL1 := indexercommon.GetStakeAssetName(int(l1.Snapshot().Height))
	stakeL1Amount := indexercommon.GetStakeAssetAmt(int(l1.Snapshot().Height))
	for _, actor := range []map[string]string{coreStakeWallet, minerStakeWallet} {
		for i := 0; i < 8; i++ {
			l1.FundAddress(t, actor["address"], 1_000_000, nil)
		}
		l1.FundAddress(t, actor["address"], stakeL1Amount*2,
			[]*indexercommon.DisplayAsset{displayAssetWithMeta(stakeL1, fmt.Sprint(stakeL1Amount*2), 0, 1)})
	}
	const asset = "ordx:f:pwapos"
	for _, identity := range []map[string]string{owner, recipient, basic} {
		count := 20
		if identity["address"] == basic["address"] {
			count = 100
		}
		for i := 0; i < count; i++ {
			l1.FundAddress(t, identity["address"], 1_000_000, nil)
		}
		for i := 0; i < 8; i++ {
			l1.FundAddress(t, identity["address"], 10000,
				[]*indexercommon.DisplayAsset{displayAssetWithMeta(asset, "5000", 0, 1)})
		}
	}
	// STP signs its own side of opening/splicing using independent, controlled
	// L1 inputs. These are setup funds, never fabricated operation results.
	for _, k := range []*btcec.PrivateKey{bootKey, coreKey} {
		actor := newDKVSKeyPathActor(t, k)
		for i := 0; i < 20; i++ {
			l1.FundAddress(t, actor.Address, 1_000_000, nil)
		}
	}
	gas := contractcommon.GetGasAssetName()
	heartbeatWitness, heartbeatScript, err := getP2WSHScript(
		bootKey.PubKey().SerializeCompressed(), coreKey.PubKey().SerializeCompressed())
	require.NoError(t, err)
	heartbeatHash := sha256.Sum256(heartbeatWitness)
	heartbeatAddress, err := btcutil.NewAddressWitnessScriptHash(heartbeatHash[:], &chaincfg.TestNetParams)
	require.NoError(t, err)
	heartbeatAsset := displayAssetWithMeta(gas, "100000000", 0, 0)
	if heartbeatAsset.Protocol == indexercommon.PROTOCOL_NAME_BRC20 {
		heartbeatAsset.Offsets = indexercommon.AssetOffsets{{Start: 0, End: 1}}
		heartbeatAsset.OffsetToAmts = []*indexercommon.OffsetToAmount{{Offset: 0, Amount: "100000000"}}
	}
	heartbeatFunding := l1.FundAddress(t, heartbeatAddress.EncodeAddress(), 20_000_000,
		[]*indexercommon.DisplayAsset{heartbeatAsset})
	stakeFund := func(first, second *btcec.PrivateKey) (string, []byte) {
		witness, script, err := getP2WSHScript(
			first.PubKey().SerializeCompressed(), second.PubKey().SerializeCompressed())
		require.NoError(t, err)
		hash := sha256.Sum256(witness)
		address, err := btcutil.NewAddressWitnessScriptHash(hash[:], &chaincfg.TestNetParams)
		require.NoError(t, err)
		asset := displayAssetWithMeta(stakeL1, fmt.Sprint(stakeL1Amount), 0, 1)
		if asset.Protocol == indexercommon.PROTOCOL_NAME_BRC20 {
			asset.Offsets = indexercommon.AssetOffsets{{Start: 0, End: 1}}
			asset.OffsetToAmts = []*indexercommon.OffsetToAmount{{Offset: 0, Amount: fmt.Sprint(stakeL1Amount)}}
		}
		funding := l1.FundAddress(t, address.EncodeAddress(), 200000,
			[]*indexercommon.DisplayAsset{asset})
		return funding, script
	}
	coreStakeFunding, coreStakeScript := stakeFund(bootKey, coreKey)
	minerStakeFunding, minerStakeScript := stakeFund(coreKey, minerKey)

	stpConfig := func(peers ...string) func(string) string {
		return func(publicRPC string) string {
			if len(peers) == 0 {
				peers = []string{"b@" + pub(bootKey) + "@http://" + publicRPC + "/testnet"}
			}
			for _, peer := range peers {
				parts := strings.Split(peer, "@")
				require.Len(t, parts, 3)
				endpoint, err := url.Parse(parts[2])
				require.NoError(t, err)
				require.Equal(t, "http", endpoint.Scheme)
				require.Equal(t, "127.0.0.1", endpoint.Hostname(), "STP must stay inside the isolated network")
			}
			encoded, err := json.Marshal(peers)
			require.NoError(t, err)
			// Admission only: the candidate still needs real signed stake funding
			// and its confirmed Anchor before the indexer recognizes a Core node.
			return fmt.Sprintf("\npeers: %s\ncorenodes: [%q, %q]\npublic_rpc:\n  browser_allowed_origins: [%q]\n",
				encoded, pub(coreKey), pub(key(coreStakeWallet["mnemonic"])), browserOrigin)
		}
	}
	boot := startDKVSNoPluginNodeWithArgs(t, l1.NodeFixture(), "bootstrap", bootstrapMnemonic, nil, stpConfig())
	bootstrapPeer := "b@" + pub(bootKey) + "@http://" + boot.stpAddr + "/testnet"
	core := startDKVSNoPluginNodeWithArgs(t, l1.NodeFixture(), "core", coreMnemonic, nil, stpConfig(bootstrapPeer))
	require.NoError(t, connectNode(core, boot))
	network := &realSatoshiNet{Bootstrap: boot, Core: core, Nodes: []*testHarness{boot, core}, fakeL1: l1.NodeFixture()}
	ownedNetwork = network
	require.NoError(t, joinBlocks(network.Nodes))
	stake := indexercommon.GetStakeAssetNameWithHeightL2(1)
	stakeAmount := indexercommon.GetStakeAssetAmtWithHeightL2(1)
	require.Equal(t, stake, stakeL1, "virtual L1/L2 must use the same stake-asset era")
	seedAnchor := func(label string, first, second *btcec.PrivateKey, value int64, name string, amount int64) *wire.MsgTx {
		witness, script, err := getP2WSHScript(first.PubKey().SerializeCompressed(), second.PubKey().SerializeCompressed())
		require.NoError(t, err)
		var funding string
		binding := 0
		assets := txAsset(name, amount)
		if name == stake {
			binding = 1
			assets[0].BindingSat = 1
		}
		switch label {
		case "heartbeat":
			funding = heartbeatFunding
			require.Equal(t, heartbeatScript, script)
		case "core-stake":
			funding = coreStakeFunding
			require.Equal(t, coreStakeScript, script)
		case "miner-stake":
			funding = minerStakeFunding
			require.Equal(t, minerStakeScript, script)
		default:
			t.Fatalf("unknown POS anchor fixture %q", label)
		}
		tx := buildAnchorTx(t, funding, value, assets,
			fmt.Sprintf("%s-%d-0-%d", name, amount, binding), witness, first, script)
		network.sendAndMine(t, tx, 1)
		return tx
	}
	seedAnchor("core-stake", bootKey, coreKey, 200000, stake, stakeAmount)
	seedAnchor("miner-stake", coreKey, minerKey, 200000, stake, stakeAmount)
	gasAnchor := seedAnchor("heartbeat", bootKey, coreKey, 20_000_000, gas, 100_000_000)
	heartbeatActor := newDKVSKeyPathActor(t, keyFromMnemonic(t, bootstrapMnemonic, 7))
	witness, _, err := getP2WSHScript(bootKey.PubKey().SerializeCompressed(), coreKey.PubKey().SerializeCompressed())
	require.NoError(t, err)
	heartbeat := fundDKVSActorFromChannel(t, network, gasAnchor, witness, bootKey, coreKey, heartbeatActor.PkScript)
	miner := startDKVSNoPluginNodeWithArgs(t, l1.NodeFixtureForParent(t, pub(coreKey)), "miner", minerMnemonic,
		[]string{"--generate", "--miningpubkey=" + pub(minerKey), "--serverpubkey=" + pub(coreKey)},
		stpConfig(bootstrapPeer, "s@"+pub(coreKey)+"@http://"+core.stpAddr+"/testnet"))
	network.Miner = miner
	network.Nodes = append(network.Nodes, miner)
	require.NoError(t, connectNode(miner, core))
	require.NoError(t, connectNode(miner, boot))
	waitForDKVSPeerReady(t, network)
	require.NoError(t, joinBlocks(network.Nodes))
	// Give the independent basic-wallet cases real L2 gas and fee funds. These
	// outputs spend the confirmed Anchor through an ordinary SDK-signed tx;
	// no L2 balance or UTXO is inserted directly into an index/database.
	seedL2 := wire.NewMsgTx(wire.TxVersion)
	seedL2.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: heartbeat.TxHash(), Index: 0}, nil, nil))
	seedL2.AddTxOut(wire.NewTxOut(heartbeat.TxOut[0].Value-3_002_000, txAsset(gas, 50_000_000), heartbeatActor.PkScript))
	for _, identity := range []map[string]string{basic, recipient} {
		script, err := hex.DecodeString(identity["pk_script"])
		require.NoError(t, err)
		if sdkWASM && identity["address"] == basic["address"] {
			// Two ordinary confirmed outputs let the existing WASM gate cover
			// multi-input orders without introducing synthetic signing prevouts.
			seedL2.AddTxOut(wire.NewTxOut(500_000, txAsset(gas, 12_500_000), script))
			seedL2.AddTxOut(wire.NewTxOut(500_000, txAsset(gas, 12_500_000), script))
		} else {
			seedL2.AddTxOut(wire.NewTxOut(1_000_000, txAsset(gas, 25_000_000), script))
		}
	}
	// STP's public Transcend contracts have a real two-party deployment fee
	// transaction. Fund Core's signing key from the same legitimate Anchor.
	seedL2.AddTxOut(wire.NewTxOut(1_000_000, nil, newDKVSKeyPathActor(t, coreKey).PkScript))
	// Give the funds wallet a real, independently indexed L2 fee output. Its
	// first deposit must start from a successful balance query, not an unknown
	// address response interpreted as zero. This spends the same signed Anchor.
	ownerScript, err := hex.DecodeString(owner["pk_script"])
	require.NoError(t, err)
	seedL2.AddTxOut(wire.NewTxOut(1_000, nil, ownerScript))
	seedFetcher := txscript.NewCannedPrevOutputFetcher(heartbeat.TxOut[0].PkScript, heartbeat.TxOut[0].Value, heartbeat.TxOut[0].Assets)
	require.NoError(t, wallet.SignTxIn_P2TR(seedL2, 0, heartbeatActor.Key, seedFetcher))
	network.sendAndMine(t, seedL2, 1)
	heartbeat = seedL2
	bootAddress, err := scommon.PublicKeyToTaprootAddress(bootKey.PubKey(), &chaincfg.TestNetParams)
	require.NoError(t, err)
	bootReward, err := txscript.PayToAddrScript(bootAddress)
	require.NoError(t, err)
	_, coreReward, err := getP2WSHScript(bootKey.PubKey().SerializeCompressed(), coreKey.PubKey().SerializeCompressed())
	require.NoError(t, err)
	_, minerReward, err := getP2WSHScript(coreKey.PubKey().SerializeCompressed(), minerKey.PubKey().SerializeCompressed())
	require.NoError(t, err)
	control := &posPWAControl{network: network, l1: l1, heartbeat: heartbeat, actor: heartbeatActor,
		keys: []*btcec.PrivateKey{bootKey, coreKey, minerKey}, rewards: [][]byte{bootReward, coreReward, minerReward}}
	require.NoError(t, control.waitSynced(ctx))
	for _, node := range []*testHarness{boot, core} {
		// Chain and peer readiness precede STP's asynchronous public RPC start.
		// Wait for its listener before asserting the original browser CORS policy.
		require.Eventually(t, func() bool {
			connection, err := net.DialTimeout("tcp", node.stpAddr, time.Second)
			if err != nil {
				return false
			}
			return connection.Close() == nil
		}, 30*time.Second, 100*time.Millisecond, "STP public RPC did not start at %s", node.stpAddr)
		request, err := http.NewRequestWithContext(ctx, http.MethodOptions,
			"http://"+node.stpAddr+"/testnet"+wwire.STP_ACTION_NFTY, nil)
		require.NoError(t, err)
		request.Header.Set("Origin", browserOrigin)
		request.Header.Set("Access-Control-Request-Method", http.MethodPost)
		request.Header.Set("Access-Control-Request-Headers", "content-type")
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
		require.NoError(t, err)
		response.Body.Close()
		require.Equal(t, http.StatusNoContent, response.StatusCode, "STP browser preflight failed")
		require.Equal(t, browserOrigin, response.Header.Get("Access-Control-Allow-Origin"))
	}
	publicContracts := map[string]string{}
	needsPublic := !sdkWASM && len(cases) == 0
	needsFaucet := needsPublic
	for _, name := range cases {
		needsFaucet = needsFaucet || name == "Tools PWA: Faucet sends sats and receives actual contract-settled GAS"
		needsPublic = needsPublic || strings.HasPrefix(name, "POS PWA:") ||
			name == "Tools PWA: direct WASM reconciles Exchange index queries and deployed service status" ||
			name == "Funds PWA: public BTC withdrawal returns confirmed Bitcoin funds" ||
			name == "Escape PWA: cooperative close returns confirmed BTC and ORDX to Bitcoin"
	}
	if needsPublic {
		publicContracts = posPWADeployPublicContracts(t, ctx, control, []string{"::", asset})
	}
	var faucetAddress string
	if needsFaucet {
		faucet := wallet.NewFaucetContract()
		faucet.AssetName = *indexercommon.NewAssetNameFromString(gas)
		faucet.Price = "0.001"
		faucetAddress = wallet.ExtractChannelId(posPWADeployContract(t, ctx, control, faucet, 1))
		require.NotEmpty(t, faucetAddress)
	}
	server := httptest.NewServer(http.HandlerFunc(control.serveHTTP))
	t.Cleanup(server.Close)
	config, _ := accountReviewConfig(t, network)
	pwaL1 := l1.NodeFixtureForParent(t, pub(coreKey))
	config.IndexerL1 = &sdkcommon.Indexer{Scheme: "http", Host: pwaL1.host(), Proxy: "testnet"}
	evmArtifact, err := LoadSolidityArtifact(filepath.Join(sdk, "e2e", "testdata", "contracts", "SDKReviewProbe.sol"), "SDKReviewProbe")
	require.NoError(t, err)
	evmInitCode := appendSolidityConstructorArgs(evmArtifact.Bytecode, evmABIString(gas), evmABIString(asset))
	fixture := map[string]any{
		"browserPort": browserPort,
		"config":      config, "control_url": server.URL, "activation_height": posPWAActivationHeight,
		"mnemonic": owner["mnemonic"], "password": owner["password"], "address": owner["address"], "pk_script": owner["pk_script"],
		"recipient": recipient, "basicWallet": basic,
		"protocolWallets": protocolWallets, "dappWallet": dappWallet, "dappRecipient": dappRecipient,
		"directWallets":   map[string]any{"sender": directSender, "recipient": directRecipient},
		"coreStakeWallet": coreStakeWallet, "minerStakeWallet": minerStakeWallet, "coreNodeWallet": coreNodeWallet,
		"stake":           map[string]string{"asset": stakeL1, "amount": fmt.Sprint(stakeL1Amount)},
		"publicContracts": publicContracts,
		"coreSTPURL":      "http://" + core.stpAddr + "/testnet",
		"tools": map[string]string{"evmInitCode": "0x" + hex.EncodeToString(evmInitCode),
			"evmContractName": "SDKReviewProbe", "gasAsset": gas, "faucetAddress": faucetAddress},
		"asset": map[string]string{"key": asset, "type": "ORDX", "ticker": "pwapos"},
		"amounts": map[string]string{"opening": "1000000", "splicing_btc": "100000", "splicing_asset": "700",
			"deposit_btc": "10000", "deposit_asset": "600"},
		"wasmPath": wasmPath, "wasmRuntimePath": wasmRuntimePath,
	}
	fixture["rgbProxy"] = startPwaRGBProxy(t)
	if sdkWASM {
		fixture["sdkPSBT"] = posPWASDKPSBTFixture(t, seedL2, basic, recipient, gas)
	}
	encoded, err := json.Marshal(fixture)
	require.NoError(t, err)
	fixturePath := filepath.Join(artifactDir, "pwa-fixture.json")
	require.NoError(t, os.WriteFile(fixturePath, encoded, 0600))
	args := []string{"scripts/verify/account-management-e2e.mjs", "--pos-fixture", fixturePath}
	if sdkWASM {
		args = append(args, "--sdk-wasm")
	}
	if len(cases) > 0 {
		args = append(args, "--wallet-cases")
		args = append(args, cases...)
	}
	command := exec.CommandContext(ctx, "node", args...)
	closeCancellation := configurePWACancellation(t, command)
	defer func() {
		if err := closeCancellation(); err != nil {
			t.Errorf("PWA cleanup: %v", err)
		}
	}()
	command.Dir = pwa
	var output bytes.Buffer
	command.Stdout = io.MultiWriter(&output, os.Stdout)
	command.Stderr = command.Stdout
	t.Logf("PWA phase setup=%s", time.Since(fixtureStarted))
	businessStarted := time.Now()
	err = command.Run()
	t.Logf("PWA phase browser=%s", time.Since(businessStarted))
	require.NoError(t, err, "PWA wallet gate failed:\n%s", output.String())
	control.mu.Lock()
	defer control.mu.Unlock()
	require.Empty(t, control.failures, "node-side acceptance checks failed")
	if sdkWASM || (len(cases) > 0 && os.Getenv("SAT20_PWA_COMPLETE_POS_GROUP") != "1") {
		return
	}
	require.True(t, control.activationChecked, "H-1/H/H+1 activation scenario was not run")
	require.True(t, control.rotationChecked, "complete three-node rotation was not run")
	require.True(t, control.substitutionChecked, "real offline-Miner/Core-substitution scenario was not run")
	require.True(t, control.restartChecked, "postactivation Core restart was not run")
}

func TestSDKPWARealRegtestSDKWASMInterfacesBrowser(t *testing.T) {
	runSDKPWARealRegtestBrowser(t, "sdk-wasm")
}
