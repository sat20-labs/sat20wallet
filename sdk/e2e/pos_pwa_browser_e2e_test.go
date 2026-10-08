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
	"sync"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcjson"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	scommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/rpcclient"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"github.com/tyler-smith/go-bip39"
)

const posPWAActivationHeight int32 = 32

// Uses the existing SDK network and PWA runner. Only L1 indexer evidence is
// controlled: nodes, POS, STP, WASM, signatures, L2 indexing and browser stores
// run their production implementations. No public endpoint or wallet is used.
func TestSDKWalletPWAConnectedBrowser(t *testing.T) {
	runSDKWalletPWABrowser(t)
}

func TestSDKWalletPWAReviewRegression(t *testing.T) {
	runSDKWalletPWABrowser(t,
		"POS PWA: opening confirms the signed Anchor after pending reload",
		"POS PWA: v2 asset splicing-in preserves quantities and signed outpoints",
		"POS PWA: v2 BTC deposit reaches the wallet through the public channel",
		"Funds PWA: public BTC withdrawal returns confirmed Bitcoin funds",
		"Escape PWA: cooperative close returns confirmed BTC and ORDX to Bitcoin",
		"Funds PWA: Bitcoin advanced ORDX Send creates two reviewed asset outputs",
		"Wallet PWA: create through the page and unlock the same identity after reload",
		"Mint PWA: partial and remainder amounts survive rechecks and reach review",
		"Mining PWA: real WASM worker submits independently verified fake-L1 proof of work",
		"Mining PWA: stopping halts the worker and reload preserves its configuration",
		"Node PWA: Core stake signs real L1 funding and appears in real node indexing",
		"Node PWA: Miner stake signs real L1 funding and appears in real node indexing",
		"RGB PWA: issue a real IFA with separately committed inflation rights",
		"RGB PWA: issue a real indivisible UDA")
}

func TestSDKWalletPWAReviewDirectRegression(t *testing.T) {
	runSDKWalletPWABrowser(t,
		"Funds PWA: Bitcoin advanced ORDX Send creates two reviewed asset outputs",
		"Wallet PWA: create through the page and unlock the same identity after reload",
		"Mint PWA: partial and remainder amounts survive rechecks and reach review",
		"Mining PWA: real WASM worker submits independently verified fake-L1 proof of work",
		"Mining PWA: stopping halts the worker and reload preserves its configuration",
		"Node PWA: Core stake signs real L1 funding and appears in real node indexing",
		"Node PWA: Miner stake signs real L1 funding and appears in real node indexing",
		"RGB PWA: issue a real IFA with separately committed inflation rights",
		"RGB PWA: issue a real indivisible UDA")
}

func TestSDKWalletPWAMintReviewRegression(t *testing.T) {
	runSDKWalletPWABrowser(t, "Mint PWA: partial and remainder amounts survive rechecks and reach review")
}

func TestSDKWalletPWAChannelReadRegression(t *testing.T) {
	// Only the hung splicing case and its real opening/activation prerequisites.
	runSDKWalletPWABrowser(t,
		"POS PWA: opening confirms the signed Anchor after pending reload",
		"POS PWA: a drained activation preserves the existing private channel",
		"POS PWA: v2 asset splicing-in preserves quantities and signed outpoints")
}

func TestSDKWalletPWAReviewRemainingRegression(t *testing.T) {
	// Opening, activation and asset splicing establish the real channel needed
	// by the three funds cases. The other five cases are the review checks.
	runSDKWalletPWABrowser(t,
		"POS PWA: opening confirms the signed Anchor after pending reload",
		"POS PWA: a drained activation preserves the existing private channel",
		"POS PWA: v2 asset splicing-in preserves quantities and signed outpoints",
		"POS PWA: v2 BTC deposit reaches the wallet through the public channel",
		"Funds PWA: public BTC withdrawal returns confirmed Bitcoin funds",
		"Escape PWA: cooperative close returns confirmed BTC and ORDX to Bitcoin",
		"Node PWA: Core stake signs real L1 funding and appears in real node indexing",
		"Node PWA: Miner stake signs real L1 funding and appears in real node indexing")
}

func runSDKWalletPWABrowser(t *testing.T, cases ...string) {
	t.Helper()
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
	artifactDir := t.TempDir()
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
	coreStakeWallet, minerStakeWallet := identity("core-stake-wallet"), identity("miner-stake-wallet")
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
			return fmt.Sprintf("\npeers: %s\npublic_rpc:\n  browser_allowed_origins: [%q]\n", encoded, browserOrigin)
		}
	}
	boot := startDKVSNoPluginNodeWithArgs(t, l1.NodeFixture(), "bootstrap", bootstrapMnemonic, nil, stpConfig())
	bootstrapPeer := "b@" + pub(bootKey) + "@http://" + boot.stpAddr + "/testnet"
	core := startDKVSNoPluginNodeWithArgs(t, l1.NodeFixture(), "core", coreMnemonic, nil, stpConfig(bootstrapPeer))
	require.NoError(t, connectNode(core, boot))
	network := &realSatoshiNet{Bootstrap: boot, Core: core, Nodes: []*testHarness{boot, core}, fakeL1: l1.NodeFixture()}
	require.NoError(t, joinBlocks(network.Nodes))
	stake := indexercommon.GetStakeAssetNameWithHeightL2(1)
	stakeAmount := indexercommon.GetStakeAssetAmtWithHeightL2(1)
	require.Equal(t, stake, stakeL1, "virtual L1/L2 must use the same stake-asset era")
	seedAnchor := func(label string, first, second *btcec.PrivateKey, value int64, name string, amount int64) *wire.MsgTx {
		witness, script, err := getP2WSHScript(first.PubKey().SerializeCompressed(), second.PubKey().SerializeCompressed())
		require.NoError(t, err)
		funding := templateLockedOutPoint("pwa-pos:"+label, 0)
		binding := 0
		assets := txAsset(name, amount)
		if name == stake {
			binding = 1
			assets[0].BindingSat = 1
		}
		l1.SeedUTXO(t, &indexercommon.AssetsInUtxo{OutPoint: funding, Value: value, PkScript: script,
			Assets: []*indexercommon.DisplayAsset{displayAssetWithMeta(name, fmt.Sprint(amount), 0, binding)}})
		tx := buildAnchorTx(t, funding, value, assets,
			fmt.Sprintf("%s-%d-0-%d", name, amount, binding), witness, first, script)
		network.sendAndMine(t, tx, 1)
		return tx
	}
	seedAnchor("core-stake", bootKey, coreKey, 200000, stake, stakeAmount)
	seedAnchor("miner-stake", coreKey, minerKey, 200000, stake, stakeAmount)
	gas := contractcommon.GetGasAssetName()
	gasAnchor := seedAnchor("heartbeat", bootKey, coreKey, 20_000_000, gas, 100_000_000)
	heartbeatActor := newDKVSKeyPathActor(t, keyFromMnemonic(t, bootstrapMnemonic, 7))
	witness, _, err := getP2WSHScript(bootKey.PubKey().SerializeCompressed(), coreKey.PubKey().SerializeCompressed())
	require.NoError(t, err)
	heartbeat := fundDKVSActorFromChannel(t, network, gasAnchor, witness, bootKey, coreKey, heartbeatActor.PkScript)
	miner := startDKVSNoPluginNodeWithArgs(t, l1.NodeFixture(), "miner", minerMnemonic,
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
		seedL2.AddTxOut(wire.NewTxOut(1_000_000, txAsset(gas, 25_000_000), script))
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
	needsPublic := len(cases) == 0
	for _, name := range cases {
		needsPublic = needsPublic || strings.HasPrefix(name, "POS PWA:") ||
			name == "Funds PWA: public BTC withdrawal returns confirmed Bitcoin funds" ||
			name == "Escape PWA: cooperative close returns confirmed BTC and ORDX to Bitcoin"
	}
	if needsPublic {
		publicContracts = posPWADeployPublicContracts(t, ctx, control, []string{"::", asset})
	}
	server := httptest.NewServer(http.HandlerFunc(control.serveHTTP))
	t.Cleanup(server.Close)
	config, _ := accountReviewConfig(t, network)
	config.IndexerL1 = &sdkcommon.Indexer{Scheme: "http", Host: l1.NodeFixture().host(), Proxy: "testnet"}
	evmSource, err := os.ReadFile(filepath.Join(sdk, "e2e", "testdata", "contracts", "SDKReviewProbe.sol"))
	require.NoError(t, err)
	fixture := map[string]any{
		"browserPort": browserPort,
		"config":      config, "control_url": server.URL, "activation_height": posPWAActivationHeight,
		"mnemonic": owner["mnemonic"], "password": owner["password"], "address": owner["address"], "pk_script": owner["pk_script"],
		"recipient": recipient, "basicWallet": basic,
		"coreStakeWallet": coreStakeWallet, "minerStakeWallet": minerStakeWallet,
		"stake":           map[string]string{"asset": stakeL1, "amount": fmt.Sprint(stakeL1Amount)},
		"publicContracts": publicContracts,
		"tools":           map[string]string{"evmSource": string(evmSource), "evmContractName": "SDKReviewProbe", "gasAsset": gas},
		"asset":           map[string]string{"key": asset, "type": "ORDX", "ticker": "pwapos"},
		"amounts": map[string]string{"opening": "1000000", "splicing_btc": "100000", "splicing_asset": "700",
			"deposit_btc": "10000", "deposit_asset": "600"},
		"wasmPath": wasmPath, "wasmRuntimePath": wasmRuntimePath,
	}
	encoded, err := json.Marshal(fixture)
	require.NoError(t, err)
	fixturePath := filepath.Join(artifactDir, "pwa-fixture.json")
	require.NoError(t, os.WriteFile(fixturePath, encoded, 0600))
	args := []string{"scripts/verify/account-management-e2e.mjs", "--pos-fixture", fixturePath}
	if len(cases) > 0 {
		args = append(args, "--wallet-cases")
		args = append(args, cases...)
	}
	command := exec.CommandContext(ctx, "node", args...)
	command.Dir = pwa
	var output bytes.Buffer
	command.Stdout = io.MultiWriter(&output, os.Stdout)
	command.Stderr = command.Stdout
	err = command.Run()
	require.NoError(t, err, "PWA wallet gate failed:\n%s", output.String())
	control.mu.Lock()
	defer control.mu.Unlock()
	require.Empty(t, control.failures, "node-side acceptance checks failed")
	if len(cases) > 0 {
		return
	}
	require.True(t, control.activationChecked, "H-1/H/H+1 activation scenario was not run")
	require.True(t, control.rotationChecked, "complete three-node rotation was not run")
	require.True(t, control.substitutionChecked, "real offline-Miner/Core-substitution scenario was not run")
	require.True(t, control.restartChecked, "postactivation Core restart was not run")
}

// A public deposit is routed by the real STP Transcend runtime. Bootstrap/Core
// must first negotiate and confirm its deployment; seeding an Anchor alone
// does not create that runtime. This uses Core's existing loopback management
// API in its temporary test directory, then checks both peers' public state.
func posPWADeployPublicContracts(t *testing.T, parent context.Context, f *posPWAControl, assets []string) map[string]string {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	witness, _, err := getP2WSHScript(f.keys[0].PubKey().SerializeCompressed(), f.keys[1].PubKey().SerializeCompressed())
	require.NoError(t, err)
	witnessHash := sha256.Sum256(witness)
	channel, err := btcutil.NewAddressWitnessScriptHash(witnessHash[:], &chaincfg.TestNetParams)
	require.NoError(t, err)
	contracts := make(map[string]string)
	for _, asset := range assets {
		contract := wallet.NewTranscendContract()
		contract.AssetName = *indexercommon.NewAssetNameFromString(asset)
		content, err := json.Marshal(map[string]any{"index": 0, "template": wallet.TEMPLATE_CONTRACT_TRANSCEND,
			"content": contract.Content(), "feeRate": 1, "local": true})
		require.NoError(t, err)
		request, err := http.NewRequestWithContext(ctx, http.MethodPost,
			"http://"+f.network.Core.managementAddr+"/local/contract/deploy", bytes.NewReader(content))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
		require.NoError(t, err)
		var deployed struct {
			wwire.BaseResp
			TxID          string `json:"txId"`
			ReservationID int64  `json:"resvId"`
		}
		err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&deployed)
		response.Body.Close()
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode)
		require.Zero(t, deployed.Code, "deploy public %s: %s", asset, deployed.Msg)
		require.Positive(t, deployed.ReservationID)
		txid, err := chainhash.NewHashFromStr(deployed.TxID)
		require.NoError(t, err, "public deployment must broadcast its actual signed fee transaction")
		contractURL := wallet.GenerateContractURl(channel.EncodeAddress(), asset, wallet.TEMPLATE_CONTRACT_TRANSCEND)
		for {
			ready := true
			targetHeight := int32(0)
			for _, node := range []*testHarness{f.network.Bootstrap, f.network.Core} {
				var response wwire.ContractStatusResp
				readErr := posPWAReadJSON("http://"+node.stpAddr+"/testnet"+wwire.QUERY_INFO_CONTRACT+"/"+url.PathEscape(contractURL), &response)
				var state struct {
					Status int `json:"status"`
					Height int `json:"currentBlock"`
					Enable int `json:"enableBlock"`
				}
				if readErr != nil || response.Code != 0 || json.Unmarshal([]byte(response.Status), &state) != nil {
					ready = false
					continue
				}
				if state.Status < wallet.CONTRACT_STATUS_READY || state.Status >= wallet.CONTRACT_STATUS_CLOSING || state.Height < state.Enable {
					ready = false
				}
				if state.Enable != wallet.INIT_ENABLE_BLOCK && state.Enable > int(targetHeight) {
					targetHeight = int32(state.Enable)
				}
			}
			if ready {
				mined, err := f.network.Bootstrap.Client.GetRawTransactionVerbose(txid)
				require.NoError(t, err)
				require.Positive(t, mined.Confirmations, "public contract must be backed by a canonical deployment transaction")
				break
			}
			_, height, err := f.network.Bootstrap.Client.GetBestBlock()
			require.NoError(t, err)
			require.Less(t, height, posPWAActivationHeight-6, "public setup must leave room for the PWA pre-H workflows")
			if targetHeight > height {
				require.NoError(t, f.produce(ctx))
				continue
			}
			select {
			case <-ctx.Done():
				t.Fatalf("public %s never became active on both STP peers: %v", asset, ctx.Err())
			case <-time.After(200 * time.Millisecond):
			}
		}
		contracts[asset] = contractURL
	}
	require.NoError(t, f.waitSynced(ctx))
	return contracts
}

type posPWANodeState struct {
	Role      string `json:"role"`
	Height    int32  `json:"height"`
	Hash      string `json:"hash"`
	APIHeight int32  `json:"api_height"`
}

type posPWAAnchorOutput struct {
	N        int           `json:"n"`
	Value    int64         `json:"value"`
	Assets   wire.TxAssets `json:"assets"`
	PkScript string        `json:"pk_script"`
}

type posPWAAnchorEvidence struct {
	TxID            string               `json:"txid"`
	Height          int32                `json:"height"`
	FundingOutpoint string               `json:"funding_outpoint"`
	Version         int32                `json:"version"`
	LockTime        uint32               `json:"lock_time"`
	Sequence        uint32               `json:"sequence"`
	Outputs         []posPWAAnchorOutput `json:"outputs"`
}

type posPWASnapshot struct {
	Height       int32                  `json:"height"`
	Hash         string                 `json:"hash"`
	Nodes        []posPWANodeState      `json:"nodes"`
	L1           posPWAL1Snapshot       `json:"l1"`
	Anchors      []posPWAAnchorEvidence `json:"anchors"`
	MempoolTxIDs []string               `json:"mempool_txids"`
	POSBlocks    []posPWABlockEvidence  `json:"pos_blocks"`
}

type posPWABlockEvidence struct {
	Height       int32  `json:"height"`
	Hash         string `json:"hash"`
	Slot         string `json:"slot"`
	Producer     string `json:"producer"`
	RewardScript string `json:"reward_script"`
}

// This mutex only serializes this test's fixed actions. It is not a
// production control endpoint or a general network orchestration protocol.
type posPWAControl struct {
	mu                                                                      sync.Mutex
	network                                                                 *realSatoshiNet
	l1                                                                      *posPWAL1Indexer
	heartbeat                                                               *wire.MsgTx
	actor                                                                   *dkvsKeyPathActor
	keys                                                                    []*btcec.PrivateKey
	rewards                                                                 [][]byte
	proofs                                                                  []posPWABlockEvidence
	failures                                                                []string
	offlineHeight                                                           int32
	activationChecked, rotationChecked, substitutionChecked, restartChecked bool
}

func (f *posPWAControl) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Minute)
	defer cancel()
	var err error
	switch r.URL.Path {
	case "/transaction":
		var request struct {
			TxID string `json:"txid"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&request); err != nil {
			http.Error(w, "invalid transaction request", http.StatusBadRequest)
			return
		}
		hash, parseHashErr := chainhash.NewHashFromStr(request.TxID)
		if parseHashErr != nil || len(request.TxID) != 64 {
			http.Error(w, "invalid txid", http.StatusBadRequest)
			return
		}
		transaction, lookupErr := f.network.Bootstrap.Client.GetRawTransactionVerbose(hash)
		if lookupErr != nil {
			// PWA has just submitted to Core; gossip to Bootstrap can still be
			// in flight. The caller must keep polling until actual confirmation.
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": lookupErr.Error()})
			return
		}
		result := struct {
			*btcjson.TxRawResult
			Results []*btcjson.TxRawResult `json:"results"`
		}{TxRawResult: transaction, Results: []*btcjson.TxRawResult{}}
		if transaction.BlockHash != "" {
			blockHash, parseErr := chainhash.NewHashFromStr(transaction.BlockHash)
			if parseErr != nil {
				err = parseErr
				break
			}
			block, blockErr := f.network.Bootstrap.Client.GetBlock(blockHash)
			if blockErr != nil {
				err = blockErr
				break
			}
			for _, candidate := range contractResultTxs(block) {
				spendsWork := false
				for _, input := range candidate.TxIn {
					if input.PreviousOutPoint.Hash == *hash {
						spendsWork = true
						break
					}
				}
				if !spendsWork {
					continue
				}
				resultHash := candidate.TxHash()
				actual, readErr := f.network.Bootstrap.Client.GetRawTransactionVerbose(&resultHash)
				if readErr != nil {
					err = readErr
					break
				}
				result.Results = append(result.Results, actual)
			}
			if err != nil {
				break
			}
		}
		_ = json.NewEncoder(w).Encode(result)
		return
	case "/snapshot":
	case "/confirm-l1":
		options := struct {
			WaitAnchors *bool `json:"wait_anchors"`
		}{}
		if r.Body != nil {
			decodeErr := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&options)
			if decodeErr != nil && decodeErr != io.EOF {
				err = decodeErr
				break
			}
		}
		txids := f.l1.ConfirmPending()
		if options.WaitAnchors == nil || *options.WaitAnchors {
			if len(txids) == 0 {
				err = fmt.Errorf("no pending L1 funding transaction to confirm")
				break
			}
			err = f.waitAnchors(ctx, txids)
		}
	case "/activate":
		err = f.activate(ctx)
	case "/rotation":
		if !f.activationChecked {
			err = fmt.Errorf("activation must complete first")
			break
		}
		start := len(f.proofs)
		seen := make(map[string]bool)
		for len(seen) < 3 && err == nil {
			err = f.produce(ctx)
			for _, proof := range f.proofs[start:] {
				if proof.Slot == proof.Producer {
					seen[proof.Producer] = true
				}
			}
		}
		f.rotationChecked = err == nil
	case "/miner-offline":
		err = f.stopMinerAtItsSlot(ctx)
	case "/restore-miner":
		err = f.restoreMiner(ctx)
	case "/restart-core":
		if !f.activationChecked {
			err = fmt.Errorf("activation must complete first")
			break
		}
		err = f.restartCore(ctx)
		f.restartChecked = err == nil
	case "/mine":
		err = f.produce(ctx)
	default:
		http.Error(w, "unknown test action", http.StatusNotFound)
		return
	}
	var snapshot *posPWASnapshot
	if err == nil {
		snapshot, err = f.snapshot()
	}
	if err != nil {
		f.failures = append(f.failures, r.URL.Path+": "+err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(snapshot)
}

func (f *posPWAControl) onlineNodes() []*testHarness {
	nodes := make([]*testHarness, 0, 3)
	for _, node := range f.network.Nodes {
		if node.Client != nil {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func posPWAReadJSON(target string, output any) error {
	response, err := (&http.Client{Timeout: 5 * time.Second}).Get(target)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", target, response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(output)
}

func (f *posPWAControl) nodeStates() ([]posPWANodeState, error) {
	states := make([]posPWANodeState, 0, 3)
	for _, node := range f.onlineNodes() {
		hash, height, err := node.Client.GetBestBlock()
		if err != nil {
			return nil, err
		}
		base, err := node.IndexerURL("testnet")
		if err != nil {
			return nil, err
		}
		var indexed struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data struct {
				Height int32 `json:"height"`
			} `json:"data"`
		}
		if err := posPWAReadJSON(base+"/bestheight", &indexed); err != nil {
			return nil, err
		}
		if indexed.Code != 0 {
			return nil, fmt.Errorf("%s indexer: %s", node.role, indexed.Msg)
		}
		states = append(states, posPWANodeState{Role: node.role, Height: height, Hash: hash.String(), APIHeight: indexed.Data.Height})
	}
	return states, nil
}

func (f *posPWAControl) waitSynced(ctx context.Context) error {
	var last string
	for {
		states, err := f.nodeStates()
		if err == nil && len(states) >= 2 {
			matched := true
			for _, state := range states {
				if state.Height != states[0].Height || state.Hash != states[0].Hash || state.APIHeight != state.Height {
					matched = false
				}
			}
			if matched {
				return nil
			}
			last = fmt.Sprint(states)
		} else {
			last = fmt.Sprint(err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("canonical/indexer convergence: %s: %w", last, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (f *posPWAControl) anchors(height int32) ([]posPWAAnchorEvidence, error) {
	anchors := make([]posPWAAnchorEvidence, 0)
	for h := int32(1); h <= height; h++ {
		hash, err := f.network.Bootstrap.Client.GetBlockHash(int64(h))
		if err != nil {
			return nil, err
		}
		block, err := f.network.Bootstrap.Client.GetBlock(hash)
		if err != nil {
			return nil, err
		}
		for _, tx := range block.Transactions {
			if !blockchain.IsAnchorTx(tx) {
				continue
			}
			info, err := anchortx.ParseAnchorScript(tx.TxIn[0].SignatureScript)
			if err != nil {
				return nil, err
			}
			item := posPWAAnchorEvidence{TxID: tx.TxHash().String(), Height: h, FundingOutpoint: info.Utxo,
				Version: tx.Version, LockTime: tx.LockTime, Sequence: tx.TxIn[0].Sequence, Outputs: make([]posPWAAnchorOutput, 0, len(tx.TxOut))}
			for n, output := range tx.TxOut {
				item.Outputs = append(item.Outputs, posPWAAnchorOutput{N: n, Value: output.Value,
					Assets: output.Assets.Clone(), PkScript: hex.EncodeToString(output.PkScript)})
			}
			anchors = append(anchors, item)
		}
	}
	return anchors, nil
}

func (f *posPWAControl) snapshot() (*posPWASnapshot, error) {
	states, err := f.nodeStates()
	if err != nil {
		return nil, err
	}
	if len(states) == 0 {
		return nil, fmt.Errorf("no online nodes")
	}
	anchors, err := f.anchors(states[0].Height)
	if err != nil {
		return nil, err
	}
	mempool, err := f.network.Bootstrap.Client.GetRawMempool()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(mempool))
	for _, id := range mempool {
		ids = append(ids, id.String())
	}
	return &posPWASnapshot{Height: states[0].Height, Hash: states[0].Hash, Nodes: states,
		L1: f.l1.Snapshot(), Anchors: anchors, MempoolTxIDs: ids, POSBlocks: append([]posPWABlockEvidence{}, f.proofs...)}, nil
}

func (f *posPWAControl) waitAnchors(ctx context.Context, fundingTxIDs []string) error {
	var last string
	for {
		snapshot, err := f.snapshot()
		if err != nil {
			return err
		}
		found := make(map[string]posPWAAnchorEvidence)
		for _, anchor := range snapshot.Anchors {
			for _, txid := range fundingTxIDs {
				if strings.HasPrefix(anchor.FundingOutpoint, txid+":") {
					found[txid] = anchor
				}
			}
		}
		if len(found) == len(fundingTxIDs) {
			if err := f.waitSynced(ctx); err != nil {
				return err
			}
			indexedAll := true
			// Check actual AIDX records on every online node. The public indexer
			// API exposes height plus Anchor identity, not an independent head hash.
			for _, node := range f.onlineNodes() {
				base, err := node.IndexerURL("testnet")
				if err != nil {
					return err
				}
				for _, anchor := range found {
					var indexed struct {
						Code int                 `json:"code"`
						Msg  string              `json:"msg"`
						Data *scommon.AscendData `json:"data"`
					}
					if err := posPWAReadJSON(base+"/v3/ascend/"+anchor.FundingOutpoint, &indexed); err != nil {
						indexedAll = false
						last = fmt.Sprintf("%s Anchor API: %v", node.role, err)
						continue
					}
					if indexed.Code != 0 || indexed.Data == nil || indexed.Data.AnchorTxId != anchor.TxID || indexed.Data.Height != int(anchor.Height) {
						indexedAll = false
						last = fmt.Sprintf("%s AIDX has not indexed Anchor %s at height %d: %+v", node.role, anchor.TxID, anchor.Height, indexed)
					}
				}
			}
			if indexedAll {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("L1 funding %v did not become indexed Anchors (%s): %w", fundingTxIDs, last, ctx.Err())
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func (f *posPWAControl) checkBlock(height int32, block *wire.MsgBlock, producer int) error {
	if len(block.Transactions) == 0 || len(block.Transactions[0].TxIn) != 1 {
		return fmt.Errorf("H%d missing coinbase", height)
	}
	coinbase := block.Transactions[0]
	if err := scommon.VerifyStandardCoinbaseScript(coinbase.TxIn[0].SignatureScript, f.keys[producer].PubKey().SerializeCompressed()); err != nil {
		return fmt.Errorf("H%d producer=%d: %w", height, producer, err)
	}
	if height < posPWAActivationHeight {
		if len(coinbase.TxIn[0].Witness) != 1 {
			return fmt.Errorf("H%d must retain legacy witness", height)
		}
		return nil
	}
	slot := int((height - posPWAActivationHeight) % 3)
	if len(coinbase.TxOut) == 0 || !bytes.Equal(coinbase.TxOut[0].PkScript, f.rewards[slot]) {
		return fmt.Errorf("H%d slot=%d paid wrong reward channel", height, slot)
	}
	if len(coinbase.TxIn[0].Witness) != 2 {
		return fmt.Errorf("H%d missing POS approval", height)
	}
	signature, err := ecdsa.ParseDERSignature(coinbase.TxIn[0].Witness[1])
	if err != nil {
		return err
	}
	if !anchortx.VerifyMessage(f.keys[0].PubKey(), scommon.POSApprovalMessage(chaincfg.TestNetParams.Net, height, block.BlockHash()), signature) {
		return fmt.Errorf("H%d approval does not bind network/height/hash", height)
	}
	if err := blockchain.ValidatePOSWitnessCommitment(btcutil.NewBlock(block), false); err != nil {
		return err
	}
	roles := []string{"bootstrap", "core", "miner"}
	f.proofs = append(f.proofs, posPWABlockEvidence{Height: height, Hash: block.BlockHash().String(),
		Slot: roles[slot], Producer: roles[producer], RewardScript: hex.EncodeToString(coinbase.TxOut[0].PkScript)})
	return nil
}

func (f *posPWAControl) produce(ctx context.Context) error {
	previousHash, previousHeight, err := f.network.Bootstrap.Client.GetBestBlock()
	if err != nil {
		return err
	}
	previous := f.heartbeat.TxOut[0]
	if previous.Value < 2000 {
		return fmt.Errorf("heartbeat funding exhausted")
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: f.heartbeat.TxHash(), Index: 0}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(previous.Value-1000, previous.Assets.Clone(), f.actor.PkScript))
	// Sign through the production SDK; this key-path spend needs its real
	// private key and cannot pass through the older OP_TRUE fixture script.
	fetcher := txscript.NewCannedPrevOutputFetcher(previous.PkScript, previous.Value, previous.Assets)
	if err := wallet.SignTxIn_P2TR(tx, 0, f.actor.Key, fetcher); err != nil {
		return err
	}
	hash, err := f.network.Bootstrap.Client.SendRawTransaction(tx, true)
	if err != nil {
		return err
	}
	if *hash != tx.TxHash() {
		return fmt.Errorf("heartbeat txid changed")
	}
	for {
		verbose, err := f.network.Bootstrap.Client.GetRawTransactionVerbose(hash)
		if err == nil && verbose.BlockHash != "" {
			blockHash, err := chainhash.NewHashFromStr(verbose.BlockHash)
			if err != nil {
				return err
			}
			currentHash, currentHeight, err := f.network.Bootstrap.Client.GetBestBlock()
			if err != nil {
				return err
			}
			if currentHeight <= previousHeight {
				return fmt.Errorf("confirmed heartbeat did not extend H%d", previousHeight)
			}
			parent := *previousHash
			found := false
			// Normal wallet activity may submit another transaction while this
			// heartbeat waits. Check every resulting canonical slot instead of
			// requiring one block for one test request.
			for height := previousHeight + 1; height <= currentHeight; height++ {
				canonical, err := f.network.Bootstrap.Client.GetBlockHash(int64(height))
				if err != nil {
					return err
				}
				block, err := f.network.Bootstrap.Client.GetBlock(canonical)
				if err != nil {
					return err
				}
				if block.Header.PrevBlock != parent {
					return fmt.Errorf("H%d does not extend the previous canonical block", height)
				}
				producer := 0
				if height >= posPWAActivationHeight {
					producer = int((height - posPWAActivationHeight) % 3)
					if producer == 2 && f.network.Miner.Client == nil {
						producer = 1
					}
				}
				if err := f.checkBlock(height, block, producer); err != nil {
					return err
				}
				if *canonical == *blockHash {
					found = true
				}
				parent = *canonical
			}
			if !found || parent != *currentHash {
				return fmt.Errorf("heartbeat block is outside the canonical extension")
			}
			f.heartbeat = tx
			return f.waitSynced(ctx)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("heartbeat %s not confirmed: %w", hash, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (f *posPWAControl) activate(ctx context.Context) error {
	if f.activationChecked {
		return fmt.Errorf("activation scenario already ran")
	}
	if len(f.l1.Snapshot().PendingTxIDs) != 0 {
		return fmt.Errorf("drain all L1 funding before H")
	}
	pool, err := f.network.Bootstrap.Client.GetRawMempool()
	if err != nil {
		return err
	}
	if len(pool) != 0 {
		return fmt.Errorf("drain L2 mempool before H")
	}
	_, height, err := f.network.Bootstrap.Client.GetBestBlock()
	if err != nil {
		return err
	}
	if height >= posPWAActivationHeight {
		return fmt.Errorf("preactivation scenarios already crossed H")
	}
	for height < posPWAActivationHeight-1 {
		if err := f.produce(ctx); err != nil {
			return err
		}
		_, height, err = f.network.Bootstrap.Client.GetBestBlock()
		if err != nil {
			return err
		}
	}
	if err := f.restartCore(ctx); err != nil {
		return err
	}
	for i := 0; i < 2; i++ {
		if err := f.produce(ctx); err != nil {
			return err
		}
	}
	f.activationChecked = true
	return nil
}

func (f *posPWAControl) stopMinerAtItsSlot(ctx context.Context) error {
	if !f.rotationChecked || f.network.Miner.Client == nil {
		return fmt.Errorf("complete online rotation before stopping Miner")
	}
	for {
		_, height, err := f.network.Bootstrap.Client.GetBestBlock()
		if err != nil {
			return err
		}
		if (height+1-posPWAActivationHeight)%3 == 2 {
			f.offlineHeight = height + 1
			break
		}
		if err := f.produce(ctx); err != nil {
			return err
		}
	}
	return posPWAStopNode(ctx, f.network.Miner)
}

func (f *posPWAControl) restoreMiner(ctx context.Context) error {
	if f.offlineHeight == 0 || f.network.Miner.Client != nil {
		return fmt.Errorf("Miner was not stopped for its slot")
	}
	_, height, err := f.network.Bootstrap.Client.GetBestBlock()
	if err != nil {
		return err
	}
	if height < f.offlineHeight {
		return fmt.Errorf("PWA deposit has not confirmed during Miner outage")
	}
	hash, err := f.network.Bootstrap.Client.GetBlockHash(int64(f.offlineHeight))
	if err != nil {
		return err
	}
	block, err := f.network.Bootstrap.Client.GetBlock(hash)
	if err != nil {
		return err
	}
	if err := f.checkBlock(f.offlineHeight, block, 1); err != nil {
		return err
	}
	if err := posPWARestartNode(ctx, f.network.Miner); err != nil {
		return err
	}
	if err := connectNode(f.network.Miner, f.network.Core); err != nil {
		return err
	}
	if err := connectNode(f.network.Miner, f.network.Bootstrap); err != nil {
		return err
	}
	if err := f.waitSynced(ctx); err != nil {
		return err
	}
	start := len(f.proofs)
	for {
		if err := f.produce(ctx); err != nil {
			return err
		}
		restored := false
		for _, proof := range f.proofs[start:] {
			if proof.Slot == "miner" && proof.Producer == "miner" {
				restored = true
			}
		}
		if restored {
			break
		}
	}
	f.substitutionChecked = true
	return nil
}

func (f *posPWAControl) restartCore(ctx context.Context) error {
	hash, height, err := f.network.Bootstrap.Client.GetBestBlock()
	if err != nil {
		return err
	}
	if err := posPWAStopNode(ctx, f.network.Core); err != nil {
		return err
	}
	if err := posPWARestartNode(ctx, f.network.Core); err != nil {
		return err
	}
	if err := connectNode(f.network.Core, f.network.Bootstrap); err != nil {
		return err
	}
	if err := f.waitSynced(ctx); err != nil {
		return err
	}
	got, gotHeight, err := f.network.Core.Client.GetBestBlock()
	if err != nil {
		return err
	}
	if gotHeight != height || *got != *hash {
		return fmt.Errorf("normal restart changed canonical tip")
	}
	return nil
}

// Unlike TearDown/restartTestHarness, this acceptance path waits for normal
// RPC stop and a zero process exit. A timeout fails the case before cleanup.
func posPWAStopNode(ctx context.Context, node *testHarness) error {
	if node.Client == nil || node.cmd == nil {
		return fmt.Errorf("%s is not running", node.role)
	}
	if _, err := node.Client.RawRequest("stop", nil); err != nil {
		return err
	}
	node.Client.Shutdown()
	node.Client.WaitForShutdown()
	node.Client = nil
	wait := make(chan error, 1)
	go func() { wait <- node.cmd.Wait() }()
	select {
	case err := <-wait:
		if err != nil {
			return fmt.Errorf("%s normal stop: %w", node.role, err)
		}
		return nil
	case <-ctx.Done():
		_ = node.cmd.Process.Kill()
		<-wait
		node.cmd = nil
		return ctx.Err()
	case <-time.After(45 * time.Second):
		_ = node.cmd.Process.Kill()
		<-wait
		node.cmd = nil
		return fmt.Errorf("%s did not stop normally in 45 seconds", node.role)
	}
}

func posPWARestartNode(ctx context.Context, node *testHarness) error {
	if node.Client != nil || node.cmd == nil {
		return fmt.Errorf("%s is not stopped", node.role)
	}
	previous := node.cmd
	logFile, err := os.OpenFile(node.logFile, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	command := exec.Command(previous.Path, previous.Args[1:]...)
	command.Dir, command.Env = previous.Dir, append([]string(nil), previous.Env...)
	command.Stdout, command.Stderr = logFile, logFile
	if satoshinetRuntimeLock != nil {
		command.ExtraFiles = []*os.File{satoshinetRuntimeLock}
	}
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	node.cmd = command
	node.extraLogFiles = append(node.extraLogFiles, logFile)
	for {
		client, err := rpcclient.New(&rpcclient.ConnConfig{Host: node.rpcAddr, Endpoint: "ws", User: "user", Pass: "pass",
			DisableTLS: true, DisableAutoReconnect: true}, &rpcclient.NotificationHandlers{})
		if err == nil {
			if _, _, err := client.GetBestBlock(); err == nil {
				node.Client = client
				return nil
			}
			client.Shutdown()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s restart RPC: %w", node.role, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
