package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	defer e2ePhase(t, "core_total")()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	module := filepath.Dir(filepath.Dir(file))
	childRun := os.Getenv("SAT20WALLET_CORE_E2E_CHILD_RUN")
	if childRun == "" {
		childRun = "^TestSDKCoreModulesConnectedE2E$"
	}
	prebuiltBundle, bundleErr := readE2EArtifactBundle()
	require.NoError(t, bundleErr)
	if prebuiltBundle != nil {
		require.False(t, coreChildNeedsWASM(childRun), "artifact mode requires the explicit native Core selector; browser/PWA runs remain separate")
		var err error
		module, err = os.Getwd()
		require.NoError(t, err)
	}
	segments := coreE2ESegments(childRun)
	// One budget covers every planned environment, preparation and child build.
	// The outer context leaves the existing five-minute shutdown margin.
	deadline := time.Now().Add(85 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()
	var reports []map[string]any
	defer func() {
		if time.Until(deadline) <= 0 && !t.Failed() {
			t.Error("shared Core budget exhausted during segment shutdown")
		}
		report := mergeCoreE2EReports(reports, ctx.Err())
		report["started_by"] = "TestSDKCoreModulesE2E"
		report["finished_at"] = time.Now().Format(time.RFC3339)
		report["child_run"] = childRun
		report["segments"] = reports
		report["shared_budget_seconds"] = 85 * 60
		report["budget_exhausted"] = time.Until(deadline) <= 0
		if t.Failed() || len(reports) != len(segments) {
			report["passed"] = false
			report["child_exit_code"] = 1
		}
		raw, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		dir := filepath.Join(module, "review-evidence")
		if output := flag.Lookup("test.outputdir"); output != nil && output.Value.String() != "" {
			dir = output.Value.String()
		}
		require.NoError(t, os.MkdirAll(dir, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "core-modules-e2e-latest.json"), append(raw, '\n'), 0600))
	}()
	for _, segment := range segments {
		if ctx.Err() != nil || time.Until(deadline) <= 0 {
			t.Errorf("shared Core budget exhausted before segment %s", segment.Name)
			break
		}
		var report map[string]any
		passed := t.Run(segment.Name, func(t *testing.T) {
			runCoreE2ESegment(t, ctx, deadline, module, segment.Run, &report)
		})
		// t.Run waits for this segment's child and every node Close/wait
		// cleanup before the next segment can reuse the compiled runtime.
		if report == nil {
			report = map[string]any{"child_run": segment.Run, "passed": false}
			t.Errorf("Core segment %s did not produce a child report", segment.Name)
		}
		report["name"] = segment.Name
		report["passed"] = passed && report["passed"] == true
		reports = append(reports, report)
		if !passed {
			break
		}
	}
}

type coreE2ESegment struct {
	Name string
	Run  string
}

func coreE2ESegments(selector string) []coreE2ESegment {
	const root = "^TestSDKCoreModulesConnectedE2E$"
	ordinary := []string{
		"dkvs_real_paid_cross_instance_prefix_and_CAS",
		"backup_multiple_wallets_and_subaccounts_control",
		"RGB11_self_send_cold_restore_and_respend",
		"RGB11_asset_name_and_transcend_registration_descriptor",
		"RGB_issuance_in_multiple_subaccounts",
		"invoice_transfer_every_paid_recovery_boundary",
		"direct_transfer_every_paid_recovery_boundary",
		"RGB11_same_root_three_device_cas_cancel_broadcast_and_respend",
		"RGB11_recovery_identity_batch_and_hop_lifecycles",
	}
	history := []string{"RGB11_fullsend_history_cold_recovery_reorg_and_competing_intent"}
	recovery := []string{
		"RGB11_R11_stable_only_cold_restore_ignores_valid_and_corrupt_legacy_cache",
		"RGB11_R23_temporary_TTL_expiry_cold_restore_and_respend",
		"RGB11_R09_same_root_real_CAS_loser_retains_private_intent_and_tests_recovery",
		"RGB11_recovery_capacity_boundaries",
		"RGB11_account_recovery_failure_boundaries",
		"guardian_two_of_three_and_wrong_knowledge",
	}
	selectGroups := func(groups []string) string { return root + "/^(" + strings.Join(groups, "|") + ")$" }
	all := append(append(append([]string(nil), ordinary...), history...), recovery...)
	if selector != root && selector != selectGroups(all) {
		// Focused group/leaf selectors retain their exact shared-parent flow.
		return []coreE2ESegment{{Name: "selected", Run: selector}}
	}
	if selector == root {
		// The normal browser journey keeps its original position and fixtures
		// in the ordinary segment; native-only runners never build its WASM.
		ordinary = append(ordinary[:5:5], append([]string{"PWA_RGB11_nonempty_recovery_and_failure_retry"}, ordinary[5:]...)...)
	}
	if selector == selectGroups(all) {
		// Ordinary and recovery cases keep their source order while reusing one
		// account/network fixture. Keep the 20-hop history case on a fresh
		// network: measured shared history was substantially slower.
		ordinaryRecovery := append(append([]string(nil), ordinary...), recovery...)
		return []coreE2ESegment{
			{Name: "ordinary-recovery", Run: selectGroups(ordinaryRecovery)},
			{Name: "long-history", Run: selectGroups(history)},
		}
	}
	return []coreE2ESegment{
		{Name: "ordinary", Run: selectGroups(ordinary)},
		{Name: "long-history", Run: selectGroups(history)},
		{Name: "recovery", Run: selectGroups(recovery)},
	}
}

func TestCoreE2ESegmentsNativeKeepsHistoryIsolated(t *testing.T) {
	const root = "^TestSDKCoreModulesConnectedE2E$"
	groups := []string{
		"dkvs_real_paid_cross_instance_prefix_and_CAS",
		"backup_multiple_wallets_and_subaccounts_control",
		"RGB11_self_send_cold_restore_and_respend",
		"RGB11_asset_name_and_transcend_registration_descriptor",
		"RGB_issuance_in_multiple_subaccounts",
		"invoice_transfer_every_paid_recovery_boundary",
		"direct_transfer_every_paid_recovery_boundary",
		"RGB11_same_root_three_device_cas_cancel_broadcast_and_respend",
		"RGB11_recovery_identity_batch_and_hop_lifecycles",
		"RGB11_fullsend_history_cold_recovery_reorg_and_competing_intent",
		"RGB11_R11_stable_only_cold_restore_ignores_valid_and_corrupt_legacy_cache",
		"RGB11_R23_temporary_TTL_expiry_cold_restore_and_respend",
		"RGB11_R09_same_root_real_CAS_loser_retains_private_intent_and_tests_recovery",
		"RGB11_recovery_capacity_boundaries",
		"RGB11_account_recovery_failure_boundaries",
		"guardian_two_of_three_and_wrong_knowledge",
	}
	require.Len(t, groups, 16)
	selector := root + "/^(" + strings.Join(groups, "|") + ")$"
	ordinaryRecovery := append(append([]string(nil), groups[:9]...), groups[10:]...)
	expected := []coreE2ESegment{
		{Name: "ordinary-recovery", Run: root + "/^(" + strings.Join(ordinaryRecovery, "|") + ")$"},
		{Name: "long-history", Run: root + "/^(" + groups[9] + ")$"},
	}
	require.Equal(t, expected, coreE2ESegments(selector))
	require.False(t, coreChildNeedsWASM(selector))
	require.True(t, coreChildNeedsWASM(root+"/^PWA_RGB11_nonempty_recovery_and_failure_retry$"))
	require.True(t, coreChildNeedsWASM(root))
	require.True(t, coreChildNeedsWASM(root+"/^[d]kvs_real_paid_cross_instance_prefix_and_CAS$"))
}

func runCoreE2ESegment(t *testing.T, ctx context.Context, deadline time.Time,
	module, childRun string, result *map[string]any) {
	needsBrowser := coreChildNeedsWASM(childRun)
	nodesReady := e2ePhase(t, "core_node_fixture")
	defaults := dkvsindexer.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	f := newDKVSNoPluginTemplateFixtureWithArgs(t,
		map[string]int64{defaults.AutopayFeeAssetName: 240000}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, f.Network)
	nodesReady()
	autopayReady := e2ePhase(t, "core_autopay_fixture")
	const receiverMnemonic = "comfort very add tuition senior run eight snap burst appear exile dutch"
	// Independent payer has no paid account wrapper: its PWA can genuinely
	// start configured as temporary, then reuse this fixture's paid delegate.
	const maintenanceMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	// Use the dedicated public SDK BindAccount flow before the child submits
	// any KV writes. Binding is account-scoped and shared by its new devices.
	bindDKVSReviewWallet(t, f.Network.Core, dkvsClientMnemonic)
	bindDKVSReviewWallet(t, f.Network.Core, receiverMnemonic)
	if needsBrowser {
		bindDKVSReviewWallet(t, f.Network.Core, maintenanceMnemonic)
	}
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	receiver := newDKVSKeyPathActor(t, keyFromMnemonic(t, receiverMnemonic, 0))
	var maintenance, gasLastRecipient *dkvsKeyPathActor
	var feeAmounts, feeValues []int64
	var feeRecipients []*dkvsKeyPathActor
	if needsBrowser {
		maintenance = newDKVSKeyPathActor(t, keyFromMnemonic(t, maintenanceMnemonic, 0))
		gasLastRecipient = maintenance
		feeAmounts = []int64{90000, 90000, 10000, 40000}
		feeValues = []int64{10000, 10000, 10000, 10000}
		feeRecipients = []*dkvsKeyPathActor{owner, receiver, maintenance, maintenance}
	} else {
		// Return unused funding to the existing fixture script, without another wallet.
		// This recipient is only an output script, not a key-path signer.
		change := &dkvsKeyPathActor{PkScript: f.A.PkScript}
		gasLastRecipient = change
		feeAmounts = []int64{90000, 90000, 50000}
		feeValues = []int64{10000, 10000, 20000}
		feeRecipients = []*dkvsKeyPathActor{owner, receiver, change}
	}
	require.Equal(t, defaults.AutopayDeployer, owner.Address)
	gas := contractcommon.GetGasAssetName()
	gasOuts := splitToDKVSKeyPathActors(t, f, f.gasAnchor, gas,
		[]int64{300000, 300000, 300000, 300000, 300000}, []int64{10000, 10000, 10000, 10000, 10000},
		[]*dkvsKeyPathActor{owner, owner, receiver, owner, gasLastRecipient})
	feeOuts := splitToDKVSKeyPathActors(t, f, f.assetAnchors[defaults.AutopayFeeAssetName],
		defaults.AutopayFeeAssetName, feeAmounts, feeValues, feeRecipients)
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
	if needsBrowser {
		fundMaintenance := buildDKVSKeyPathTemplateDefaultInvoke(t, maintenance, contract, []dkvsPrevOut{feeOuts[2]},
			wire.TxOut{Value: 10000, Assets: txAsset(defaults.AutopayFeeAssetName, 10000)})
		f.Network.sendManyAndMine(t, []*wire.MsgTx{fundMaintenance}, 0)
		f.Network.sendManyAndMine(t, []*wire.MsgTx{buildDKVSKeyPathAssetTransfer(t, maintenance, feeOuts[3],
			defaults.AutopayFeeAssetName, 40000, 9000, maintenance)}, 0)
	}
	ownerParam, err := (&contractcommon.TemplateAutopayConfigInvokeParam{
		AmountPerBlock: "100", GasFundingAmount: "280000", BlobKeyLimit: 10,
	}).Encode()
	require.NoError(t, err)
	receiverParam, err := (&contractcommon.TemplateAutopayConfigInvokeParam{
		AmountPerBlock: "100", BlobKeyLimit: 4,
	}).Encode()
	require.NoError(t, err)
	configs := []*wire.MsgTx{
		buildDKVSKeyPathTemplateInvoke(t, owner, contract, 1, contractcommon.TemplateInvokeAPIConfig, ownerParam,
			[]dkvsPrevOut{gasOuts[1]}, wire.TxOut{Value: 9000, Assets: txAsset(gas, 290000)}),
		buildDKVSKeyPathTemplateInvoke(t, receiver, contract, 1, contractcommon.TemplateInvokeAPIConfig, receiverParam,
			[]dkvsPrevOut{gasOuts[2]}, wire.TxOut{Value: 9000, Assets: txAsset(gas, 290000)}),
	}
	if needsBrowser {
		// The paid upgrade reuses one RGB module key for each of two subaccounts.
		maintenanceParam, err := (&contractcommon.TemplateAutopayConfigInvokeParam{AmountPerBlock: "10", BlobKeyLimit: 2}).Encode()
		require.NoError(t, err)
		configs = append(configs, buildDKVSKeyPathTemplateInvoke(t, maintenance, contract, 1,
			contractcommon.TemplateInvokeAPIConfig, maintenanceParam,
			[]dkvsPrevOut{gasOuts[4]}, wire.TxOut{Value: 9000, Assets: txAsset(gas, 290000)}))
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
	require.EqualValues(t, 10, state.Delegates[owner.Address].BlobKeyLimit,
		"the real AUTOPAY sender must cover the indexed account records")
	require.EqualValues(t, 4, state.Delegates[receiver.Address].BlobKeyLimit,
		"the receiver's on-chain capacity is intentionally independent")
	if needsBrowser {
		maintenanceDelegate, ok := state.Delegates[maintenance.Address]
		require.True(t, ok)
		require.Equal(t, "10", maintenanceDelegate.AmountPerBlock)
		require.EqualValues(t, 2, maintenanceDelegate.BlobKeyLimit)
		require.GreaterOrEqual(t, maintenanceDelegate.LastPayHeight, state.CurrentBlock)
	} else {
		require.Len(t, state.Delegates, 2, "native Core fixture must fund only owner and receiver")
	}
	t.Logf("core-e2e: AUTOPAY fixture maintenance=%t delegates=%d", needsBrowser, len(state.Delegates))
	autopayReady()
	// DKVS/P2P readiness precedes asynchronous STP public RPC startup. RGB
	// registration and Direct messages need that service before the child runs.
	publicReady := e2ePhase(t, "core_public_rpc_ready")
	healthClient := &http.Client{Timeout: time.Second}
	require.Eventually(t, func() bool {
		response, err := healthClient.Get("http://" + f.Network.Core.stpAddr + "/health")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusOK
	}, 15*time.Second, 100*time.Millisecond, "CoreNode STP public service must be ready before starting the SDK child")
	publicReady()
	location := func(node *testHarness) wallet.AccountIndexerLocation {
		raw, err := node.IndexerURL("testnet")
		require.NoError(t, err)
		u, err := url.Parse(raw)
		require.NoError(t, err)
		return wallet.AccountIndexerLocation{Scheme: u.Scheme, Host: u.Host, Proxy: strings.Trim(u.Path, "/")}
	}
	childConfig := map[string]any{
		"core": location(f.Network.Core), "bootstrap": location(f.Network.Bootstrap),
		"core_peer":      "s@" + f.Network.Core.nodePubKey + "@http://" + f.Network.Core.stpAddr + "/testnet",
		"bootstrap_peer": "b@" + f.Network.Bootstrap.nodePubKey + "@http://" + f.Network.Bootstrap.stpAddr + "/testnet",
		"contract":       contract.MustEncode(),
	}
	if needsBrowser {
		childConfig["maintenance_mnemonic"] = maintenanceMnemonic
	}
	config, err := json.Marshal(childConfig)
	require.NoError(t, err)
	if needsBrowser {
		wasmPath, runtimePath := buildPWAWalletRuntime(t, ctx, module)
		t.Setenv("SAT20_PWA_E2E_WASM", wasmPath)
		t.Setenv("SAT20_PWA_E2E_WASM_RUNTIME", runtimePath)
	} else {
		t.Log("core-e2e: explicit native child selector; wallet WASM build not needed")
	}
	remaining := time.Until(deadline)
	require.Greater(t, remaining, time.Duration(0), "shared Core budget exhausted during preparation")
	prebuiltBundle, bundleErr := readE2EArtifactBundle()
	require.NoError(t, bundleErr)
	var command *exec.Cmd
	var prebuiltConverter string
	if prebuiltBundle == nil {
		command = exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"),
			"test", "-json", "-failfast", "./wallet", "-run", childRun, "-count=1", "-timeout="+remaining.String())
	} else {
		child, artifactErr := prebuiltBundle.file("wallet.test")
		require.NoError(t, artifactErr)
		prebuiltConverter, artifactErr = prebuiltBundle.file("test2json")
		require.NoError(t, artifactErr)
		command = exec.CommandContext(ctx, child, "-test.v=test2json", "-test.run="+childRun,
			"-test.count=1", "-test.parallel=1", "-test.failfast", "-test.timeout="+remaining.String())
	}
	command.Dir = module
	if prebuiltBundle != nil {
		command.Dir = filepath.Join(module, "wallet")
		require.NoError(t, os.MkdirAll(command.Dir, 0700))
	}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "SAT20WALLET_RUN_LIVE_NETWORK_TESTS=") && !strings.HasPrefix(entry, "SAT20WALLET_CORE_E2E_CONFIG=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "SAT20WALLET_CORE_E2E_CONFIG="+string(config))
	// Keep every failed child's original log, including later Core segments.
	// Large node/database retention still follows the existing one-case limit.
	childLog, err := os.CreateTemp(e2eRuntimeDirectory(t), "sat20wallet-core-e2e-child-*.log")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = childLog.Close()
		if t.Failed() {
			runtimeDirsMu.Lock()
			runtimeKeepPaths = append(runtimeKeepPaths, childLog.Name())
			runtimeDirsMu.Unlock()
			t.Logf("failed child diagnostic retained in place: %s", childLog.Name())
			return
		}
		require.NoError(t, os.Remove(childLog.Name()))
	})
	t.Logf("core-e2e: child diagnostic=%s", childLog.Name())
	command.Stdout, command.Stderr = childLog, childLog
	childFinished := e2ePhase(t, "core_wallet_child_build_and_run")
	var runErr error
	if prebuiltBundle == nil {
		runErr = command.Run()
	} else {
		runErr = runPrebuiltCoreChild(ctx, command, prebuiltConverter, childLog)
	}
	childFinished()
	defer e2ePhase(t, "core_child_report")()
	require.NoError(t, childLog.Close())
	output, err := os.ReadFile(childLog.Name())
	require.NoError(t, err)
	type event struct {
		Output string
	}
	childSummary, childScanErr := collectCoreE2EChildEvents(bytes.NewReader(output))
	var browserVerdicts []map[string]any
	var metrics []map[string]any
	var browserOutput strings.Builder
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 65536), 4<<20)
	for scanner.Scan() {
		var entry event
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}
		// go test -json can split one long output line across several events.
		// Reassemble the stream before parsing the browser's JSON lines.
		browserOutput.WriteString(entry.Output)
	}
	// Preserve only structured browser verdicts. Raw child diagnostics can
	// contain synthetic secrets and must not be copied into shared evidence.
	for _, line := range strings.Split(browserOutput.String(), "\n") {
		metric, verdict := parseCoreE2EStructuredLine(line)
		if metric != nil {
			metrics = append(metrics, metric)
			start := strings.Index(line, "core-e2e-metric: ")
			t.Logf("core metric: %s", strings.TrimSpace(line[start+len("core-e2e-metric: "):]))
		}
		if verdict != nil {
			browserVerdicts = append(browserVerdicts, verdict)
			start := strings.Index(line, `{"case":`)
			t.Logf("browser verdict: %s", strings.TrimSpace(line[start:]))
		}
	}
	report := map[string]any{
		"started_by": "TestSDKCoreModulesE2E", "finished_at": time.Now().Format(time.RFC3339),
		"child_run":           childRun,
		"real_components":     []string{"SatoshiNet nodes", "AUTOPAY settlement", "bound CoreNode KV RPC", "current-state DKVS sync", "CoreNode message service", "RGB native validation/signing", "independent wallet databases"},
		"controlled_boundary": "Bitcoin UTXO/transaction confirmation evidence; no public network transactions",
		"browser_verdicts":    browserVerdicts,
		"metrics":             metrics,
	}
	var childExitCode *int
	if command.ProcessState != nil {
		code := command.ProcessState.ExitCode()
		childExitCode = &code
	}
	for key, value := range childSummary.reportFields(runErr, childExitCode, ctx.Err()) {
		report[key] = value
	}
	report["child_output_bytes"] = len(output)
	if runErr != nil {
		report["child_run_error"] = runErr.Error()
	}
	report["maintenance_prepared"] = needsBrowser
	report["autopay_delegate_count"] = len(state.Delegates)
	report["node_rpc"] = f.Network.Core.RPCAddress()
	*result = report
	require.Greater(t, time.Until(deadline), time.Duration(0), "shared Core budget exhausted")
	require.NoError(t, scanner.Err())
	require.NoError(t, childScanErr)
	if runErr != nil {
		t.Fatalf("core-e2e: wallet lifecycle suite failed: %v; redacted evidence in review-evidence/core-modules-e2e-latest.json; diagnostic=%s", runErr, childLog.Name())
	}
	require.NotEmpty(t, childSummary.Verdicts, "child must execute tests, not merely compile")
}

func coreChildNeedsWASM(selector string) bool {
	// Fixed native runners use a slash-separated, anchored parent/group
	// selector. Keep the default and ambiguous regexes on the full browser path.
	parts := strings.Split(selector, "/")
	if len(parts) < 2 || parts[0] != "^TestSDKCoreModulesConnectedE2E$" ||
		strings.ContainsAny(selector, "[\\") {
		return true
	}
	groups, err := regexp.Compile(parts[1])
	return err != nil || groups.MatchString("PWA_RGB11_nonempty_recovery_and_failure_retry")
}

func parseCoreE2EStructuredLine(line string) (metric, verdict map[string]any) {
	if start := strings.Index(line, "core-e2e-metric: "); start >= 0 {
		if json.Unmarshal([]byte(strings.TrimSpace(line[start+len("core-e2e-metric: "):])), &metric) != nil {
			metric = nil
		}
		return metric, nil
	}
	if start := strings.Index(line, `{"case":`); start >= 0 {
		if json.Unmarshal([]byte(strings.TrimSpace(line[start:])), &verdict) != nil {
			verdict = nil
		}
	}
	return metric, verdict
}
