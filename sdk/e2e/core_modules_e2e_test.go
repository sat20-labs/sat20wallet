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
		map[string]int64{defaults.AutopayFeeAssetName: 200000}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, f.Network)
	const receiverMnemonic = "comfort very add tuition senior run eight snap burst appear exile dutch"
	// Use the dedicated public SDK BindAccount flow before the child submits
	// any KV writes. Binding is account-scoped and shared by its new devices.
	bindDKVSReviewWallet(t, f.Network.Core, dkvsClientMnemonic)
	bindDKVSReviewWallet(t, f.Network.Core, receiverMnemonic)
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	receiver := newDKVSKeyPathActor(t, keyFromMnemonic(t, receiverMnemonic, 0))
	require.Equal(t, defaults.AutopayDeployer, owner.Address)
	gas := contractcommon.GetGasAssetName()
	gasOuts := splitToDKVSKeyPathActors(t, f, f.gasAnchor, gas,
		[]int64{300000, 300000, 300000, 300000}, []int64{10000, 10000, 10000, 10000},
		[]*dkvsKeyPathActor{owner, owner, receiver, owner})
	feeOuts := splitToDKVSKeyPathActors(t, f, f.assetAnchors[defaults.AutopayFeeAssetName],
		defaults.AutopayFeeAssetName, []int64{90000, 90000}, []int64{10000, 10000},
		[]*dkvsKeyPathActor{owner, receiver})
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
	ownerParam, err := (&contractcommon.TemplateAutopayConfigInvokeParam{AmountPerBlock: "100", GasFundingAmount: "280000"}).Encode()
	require.NoError(t, err)
	receiverParam, err := (&contractcommon.TemplateAutopayConfigInvokeParam{AmountPerBlock: "100"}).Encode()
	require.NoError(t, err)
	configs := []*wire.MsgTx{
		buildDKVSKeyPathTemplateInvoke(t, owner, contract, 1, contractcommon.TemplateInvokeAPIConfig, ownerParam,
			[]dkvsPrevOut{gasOuts[1]}, wire.TxOut{Value: 9000, Assets: txAsset(gas, 290000)}),
		buildDKVSKeyPathTemplateInvoke(t, receiver, contract, 1, contractcommon.TemplateInvokeAPIConfig, receiverParam,
			[]dkvsPrevOut{gasOuts[2]}, wire.TxOut{Value: 9000, Assets: txAsset(gas, 290000)}),
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
	location := func(node *testHarness) wallet.AccountIndexerLocation {
		raw, err := node.IndexerURL("testnet")
		require.NoError(t, err)
		u, err := url.Parse(raw)
		require.NoError(t, err)
		return wallet.AccountIndexerLocation{Scheme: u.Scheme, Host: u.Host, Proxy: strings.Trim(u.Path, "/")}
	}
	config, err := json.Marshal(map[string]any{
		"core": location(f.Network.Core), "bootstrap": location(f.Network.Bootstrap),
		"core_peer":      "s@" + f.Network.Core.nodePubKey + "@http://" + f.Network.Core.stpAddr + "/testnet",
		"bootstrap_peer": "b@" + f.Network.Bootstrap.nodePubKey + "@http://" + f.Network.Bootstrap.stpAddr + "/testnet",
		"contract":       contract.MustEncode(),
	})
	require.NoError(t, err)
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	module := filepath.Dir(filepath.Dir(file))
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"),
		"test", "-json", "./wallet", "-run", "^TestSDKCoreModulesConnectedE2E$", "-count=1", "-timeout=11m")
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
		if strings.Contains(entry.Output, "core-e2e:") {
			failures = append(failures, strings.TrimSpace(entry.Output))
		}
	}
	report := map[string]any{
		"started_by": "TestSDKCoreModulesE2E", "finished_at": time.Now().Format(time.RFC3339),
		"real_components":     []string{"SatoshiNet nodes", "AUTOPAY settlement", "bound CoreNode KV RPC", "current-state DKVS sync", "CoreNode message service", "RGB native validation/signing", "independent wallet databases"},
		"controlled_boundary": "Bitcoin UTXO/transaction confirmation evidence; no public network transactions",
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
		// temporary directory rather than the user-facing evidence report.
		diagnostic := filepath.Join(t.TempDir(), "child.log")
		_ = os.WriteFile(diagnostic, output, 0600)
		t.Fatalf("core-e2e: wallet lifecycle suite failed: %v; redacted evidence in review-evidence/core-modules-e2e-latest.json; diagnostic=%s", runErr, diagnostic)
	}
}
