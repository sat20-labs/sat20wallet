package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Real temporary SatoshiNet nodes, AUTOPAY, SDK replicas and CoreNode messages.
// Only Bitcoin evidence is controlled; no public network transaction is sent.
func TestSDKCoreModulesE2E(t *testing.T) {
	defaults := dkvsindexer.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	f := newDKVSNoPluginTemplateFixtureWithArgs(t,
		map[string]int64{defaults.AutopayFeeAssetName: 240000}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, f.Network)
	const receiverMnemonic = "comfort very add tuition senior run eight snap burst appear exile dutch"
	// Independent payer has no paid account wrapper: its PWA can genuinely
	// start configured as temporary, then reuse this fixture's paid delegate.
	const maintenanceMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	// Use the dedicated public SDK BindAccount flow before the child submits
	// any KV writes. Binding is account-scoped and shared by its new devices.
	bindDKVSReviewWallet(t, f.Network.Core, dkvsClientMnemonic)
	bindDKVSReviewWallet(t, f.Network.Core, receiverMnemonic)
	bindDKVSReviewWallet(t, f.Network.Core, maintenanceMnemonic)
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	receiver := newDKVSKeyPathActor(t, keyFromMnemonic(t, receiverMnemonic, 0))
	maintenance := newDKVSKeyPathActor(t, keyFromMnemonic(t, maintenanceMnemonic, 0))
	require.Equal(t, defaults.AutopayDeployer, owner.Address)
	gas := contractcommon.GetGasAssetName()
	gasOuts := splitToDKVSKeyPathActors(t, f, f.gasAnchor, gas,
		[]int64{300000, 300000, 300000, 300000, 300000}, []int64{10000, 10000, 10000, 10000, 10000},
		[]*dkvsKeyPathActor{owner, owner, receiver, owner, maintenance})
	feeOuts := splitToDKVSKeyPathActors(t, f, f.assetAnchors[defaults.AutopayFeeAssetName],
		defaults.AutopayFeeAssetName, []int64{90000, 90000, 10000, 40000}, []int64{10000, 10000, 10000, 10000},
		[]*dkvsKeyPathActor{owner, receiver, maintenance, maintenance})
	content, err := defaults.AutopayContent()
	require.NoError(t, err)
	assets := txAsset(gas, 290000)
	require.NoError(t, assets.Merge(txAsset(defaults.AutopayFeeAssetName, 90000)))
	deploy, contract := buildDKVSKeyPathTemplateDeploy(t, owner, contractcommon.TemplateAutopay,
		content, owner.Address, defaults.AutopayDeployNonce, []dkvsPrevOut{gasOuts[0], feeOuts[0]},
		wire.TxOut{Value: 10000, Assets: assets})
	f.Network.sendManyAndMine(t, []*wire.MsgTx{deploy}, 0)
	fundReceiver := buildDKVSKeyPathTemplateDefaultInvoke(t, receiver, contract, []dkvsPrevOut{feeOuts[1]},
		wire.TxOut{Value: 10000, Assets: txAsset(defaults.AutopayFeeAssetName, 90000)})
	f.Network.sendManyAndMine(t, []*wire.MsgTx{fundReceiver}, 0)
	fundMaintenance := buildDKVSKeyPathTemplateDefaultInvoke(t, maintenance, contract, []dkvsPrevOut{feeOuts[2]},
		wire.TxOut{Value: 10000, Assets: txAsset(defaults.AutopayFeeAssetName, 10000)})
	f.Network.sendManyAndMine(t, []*wire.MsgTx{fundMaintenance}, 0)
	f.Network.sendManyAndMine(t, []*wire.MsgTx{buildDKVSKeyPathAssetTransfer(t, maintenance, feeOuts[3],
		defaults.AutopayFeeAssetName, 40000, 9000, maintenance)}, 0)
	ownerParam, err := (&contractcommon.TemplateAutopayConfigInvokeParam{AmountPerBlock: "100", GasFundingAmount: "280000"}).Encode()
	require.NoError(t, err)
	receiverParam, err := (&contractcommon.TemplateAutopayConfigInvokeParam{AmountPerBlock: "100"}).Encode()
	require.NoError(t, err)
	maintenanceParam, err := (&contractcommon.TemplateAutopayConfigInvokeParam{AmountPerBlock: "10"}).Encode()
	require.NoError(t, err)
	configs := []*wire.MsgTx{
		buildDKVSKeyPathTemplateInvoke(t, owner, contract, 1, contractcommon.TemplateInvokeAPIConfig, ownerParam,
			[]dkvsPrevOut{gasOuts[1]}, wire.TxOut{Value: 9000, Assets: txAsset(gas, 290000)}),
		buildDKVSKeyPathTemplateInvoke(t, receiver, contract, 1, contractcommon.TemplateInvokeAPIConfig, receiverParam,
			[]dkvsPrevOut{gasOuts[2]}, wire.TxOut{Value: 9000, Assets: txAsset(gas, 290000)}),
		buildDKVSKeyPathTemplateInvoke(t, maintenance, contract, 1, contractcommon.TemplateInvokeAPIConfig, maintenanceParam,
			[]dkvsPrevOut{gasOuts[4]}, wire.TxOut{Value: 9000, Assets: txAsset(gas, 290000)}),
	}
	f.Network.sendManyAndMine(t, configs, 0)
	f.Network.sendManyAndMine(t, []*wire.MsgTx{buildDKVSKeyPathAssetTransfer(t, owner, gasOuts[3], gas, 290000, 9000, owner)}, 0)
	state := fetchTemplateAutopayView(t, f.Network.Core, contract.MustEncode())
	for _, actor := range []*dkvsKeyPathActor{owner, receiver} {
		delegate, ok := state.Delegates[actor.Address]
		require.True(t, ok)
		require.Equal(t, "100", delegate.AmountPerBlock)
		require.GreaterOrEqual(t, delegate.LastPayHeight, state.CurrentBlock)
	}
	maintenanceDelegate, ok := state.Delegates[maintenance.Address]
	require.True(t, ok)
	require.Equal(t, "10", maintenanceDelegate.AmountPerBlock)
	require.GreaterOrEqual(t, maintenanceDelegate.LastPayHeight, state.CurrentBlock)
	location := func(node *testHarness) wallet.AccountIndexerLocation {
		raw, err := node.IndexerURL("testnet")
		require.NoError(t, err)
		u, err := url.Parse(raw)
		require.NoError(t, err)
		return wallet.AccountIndexerLocation{Scheme: u.Scheme, Host: u.Host, Proxy: strings.Trim(u.Path, "/")}
	}
	config, err := json.Marshal(map[string]any{
		"core": location(f.Network.Core), "bootstrap": location(f.Network.Bootstrap),
		"core_peer":            "s@" + f.Network.Core.nodePubKey + "@http://" + f.Network.Core.stpAddr + "/testnet",
		"bootstrap_peer":       "b@" + f.Network.Bootstrap.nodePubKey + "@http://" + f.Network.Bootstrap.stpAddr + "/testnet",
		"contract":             contract.MustEncode(),
		"maintenance_mnemonic": maintenanceMnemonic,
	})
	require.NoError(t, err)
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	module := filepath.Dir(filepath.Dir(file))
	ctx, cancel := context.WithTimeout(context.Background(), 27*time.Minute)
	defer cancel()
	wasmPath, runtimePath := buildPWAWalletRuntime(t, ctx, module)
	t.Setenv("SAT20_PWA_E2E_WASM", wasmPath)
	t.Setenv("SAT20_PWA_E2E_WASM_RUNTIME", runtimePath)
	command := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"),
		"test", "-json", "./wallet", "-run", "^TestSDKCoreModulesConnectedE2E$", "-count=1", "-timeout=26m")
	command.Dir = module
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "SAT20WALLET_RUN_LIVE_NETWORK_TESTS=") && !strings.HasPrefix(entry, "SAT20WALLET_CORE_E2E_CONFIG=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "SAT20WALLET_CORE_E2E_CONFIG="+string(config))
	output, runErr := command.CombinedOutput()
	type event struct {
		Action, Test, Output string
		Elapsed              float64
	}
	var verdicts []event
	var failures []string
	var browserVerdicts []map[string]any
	var browserOutput strings.Builder
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 65536), 4<<20)
	for scanner.Scan() {
		var entry event
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}
		if entry.Test != "" && (entry.Action == "pass" || entry.Action == "fail" || entry.Action == "skip") {
			entry.Output = ""
			verdicts = append(verdicts, entry)
		}
		// go test -json can split one long output line across several events.
		// Reassemble the stream before parsing the browser's JSON lines.
		browserOutput.WriteString(entry.Output)
		if strings.Contains(entry.Output, "core-e2e:") {
			failures = append(failures, strings.TrimSpace(entry.Output))
		}
	}
	// Preserve only structured browser verdicts. Raw child diagnostics can
	// contain synthetic secrets and must not be copied into shared evidence.
	for _, line := range strings.Split(browserOutput.String(), "\n") {
		if start := strings.Index(line, `{"case":`); start >= 0 {
			var verdict map[string]any
			if json.Unmarshal([]byte(strings.TrimSpace(line[start:])), &verdict) == nil {
				browserVerdicts = append(browserVerdicts, verdict)
				t.Logf("browser verdict: %s", strings.TrimSpace(line[start:]))
			}
		}
	}
	report := map[string]any{
		"started_by": "TestSDKCoreModulesE2E", "finished_at": time.Now().Format(time.RFC3339),
		"real_components":     []string{"SatoshiNet nodes", "AUTOPAY settlement", "bound CoreNode KV RPC", "current-state DKVS sync", "CoreNode message service", "RGB native validation/signing", "independent wallet databases"},
		"controlled_boundary": "Bitcoin UTXO/transaction confirmation evidence; no public network transactions",
		"browser_verdicts":    browserVerdicts,
		"passed":              runErr == nil, "timed_out": ctx.Err() != nil, "verdicts": verdicts, "failures": failures,
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	dir := filepath.Join(module, "review-evidence")
	require.NoError(t, os.MkdirAll(dir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "core-modules-e2e-latest.json"), append(raw, '\n'), 0600))
	require.NoError(t, scanner.Err())
	require.NotEmpty(t, verdicts, "child must execute tests, not merely compile")
	for _, failure := range failures {
		t.Log(failure)
	}
	if runErr != nil {
		// Raw child logs can include synthetic secrets; keep them in the test's
		// private temporary area, outside t.TempDir's automatic cleanup, so the
		// failure can actually be diagnosed after go test exits.
		diagnostic := "unavailable"
		if file, err := os.CreateTemp("", "sat20wallet-core-e2e-child-*.log"); err == nil {
			diagnostic = file.Name()
			_, _ = file.Write(output)
			_ = file.Close()
		}
		t.Fatalf("core-e2e: wallet lifecycle suite failed: %v; redacted evidence in review-evidence/core-modules-e2e-latest.json; diagnostic=%s", runErr, diagnostic)
	}
}

