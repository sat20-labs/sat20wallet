package wallet

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Explicit adapters for fixed MCP test profiles. These execute tests/builds
// only, never change production sources, runtime configuration or Git state.
var smartContractFinalSources = []string{
	"sat20wallet/sdk/wallet/interface_contract_unified.go",
	"sat20wallet/sdk/wallet/contract_funding_safety.go",
	"sat20wallet/sdk/wallet/contract_funding_safety_test.go",
	"sat20wallet/sdk/wallet/contract_template_unified_test.go",
	"sat20wallet/sdk/wallet/contract_agent_value_safety_test.go",
	"satoshinet/mempool/mempool.go",
	"satoshinet/mempool/contract_admission.go",
	"satoshinet/contract/funding_safety.go",
	"satoshinet/contract/evm/backend.go",
	"satoshinet/contract/evm/runtime.go",
	"satoshinet/contract/template/backend.go",
	"satoshinet/contract/framework/gas_config.go",
	"satoshinet/contract/framework/executor.go",
	"satoshinet/blockchain/validate.go",
	"transcend/stp/contract_deploy.go",
	"transcend/stp/contract_invoke.go",
	"sat20wallet/sdk/e2e/smart_contract_admission_sdk_e2e_test.go",
	"sat20wallet/sdk/e2e/smart_contract_evm_sdk_e2e_test.go",
	"sat20wallet/sdk/e2e/smart_contract_template_sdk_e2e_test.go",
	"sat20wallet/sdk/e2e/smart_contract_additional_sdk_e2e_test.go",
	"sat20wallet/sdk/e2e/smart_contract_sdk_helpers_test.go",
	"sat20wallet/sdk/e2e/testdata/contracts/SDKReviewProbe.sol",
}

func smartContractEvidencePaths(t *testing.T) (sdk, root, evidence string) {
	t.Helper()
	if !strings.Contains(flag.Lookup("test.run").Value.String(), t.Name()) {
		t.Skip("explicit smart-contract validation or evidence inspection")
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate SDK module")
	}
	sdk = filepath.Dir(filepath.Dir(source))
	root = filepath.Dir(filepath.Dir(sdk))
	evidence = filepath.Join(sdk, "review-evidence")
	return
}

func smartContractSourceHashes(t *testing.T, root string) map[string]string {
	t.Helper()
	hashes := make(map[string]string, len(smartContractFinalSources))
	for _, name := range smartContractFinalSources {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		hashes[name] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	return hashes
}

func smartContractSaveEvidence(t *testing.T, directory, name string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path+".tmp", append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Fatal(err)
	}
}

type smartContractValidationEvent struct {
	Action  string
	Package string
	Test    string
	Elapsed float64
}

func TestSmartContractScopedFinalValidation(t *testing.T) {
	sdk, root, evidence := smartContractEvidencePaths(t)
	before := smartContractSourceHashes(t, root)
	started := time.Now()
	results := make(map[string]any)
	defer func() {
		after := smartContractSourceHashes(t, root)
		changed := []string{}
		for path, hash := range before {
			if after[path] != hash {
				changed = append(changed, path)
			}
		}
		if len(changed) != 0 {
			t.Errorf("sources changed during validation: %v", changed)
		}
		smartContractSaveEvidence(t, evidence, "smart-contract-scoped-final.json", map[string]any{
			"started_at": started.Format(time.RFC3339), "finished_at": time.Now().Format(time.RFC3339),
			"passed": !t.Failed(), "checks": results, "source_sha256": before,
			"changed_sources_during_run": changed,
		})
	}()
	env := []string{}
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "SAT20WALLET_RUN_LIVE_NETWORK_TESTS=") || strings.HasPrefix(entry, "GOOS=") || strings.HasPrefix(entry, "GOARCH=") {
			continue
		}
		env = append(env, entry)
	}
	run := func(name, directory string, args []string, extra ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), args...)
		cmd.Dir = directory
		cmd.Env = append(append([]string(nil), env...), extra...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		when := time.Now()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		verdicts := []smartContractValidationEvent{}
		diagnostics := []string{}
		scanner := bufio.NewScanner(pipe)
		scanner.Buffer(make([]byte, 65536), 4<<20)
		for scanner.Scan() {
			var item struct {
				smartContractValidationEvent
				Output string
			}
			if json.Unmarshal(scanner.Bytes(), &item) != nil {
				continue
			}
			if item.Action == "pass" || item.Action == "fail" || item.Action == "skip" {
				verdicts = append(verdicts, item.smartContractValidationEvent)
			}
			line := strings.TrimSpace(item.Output)
			lower := strings.ToLower(line)
			if len(line) < 1000 && !strings.Contains(lower, "private") && !strings.Contains(lower, "mnemonic") && !strings.Contains(lower, "password") && (strings.Contains(line, "WARNING: DATA RACE") || strings.HasPrefix(line, "panic:") || strings.Contains(line, "test timed out") || strings.HasPrefix(line, "Error:")) && len(diagnostics) < 30 {
				diagnostics = append(diagnostics, line)
			}
		}
		if scanner.Err() != nil {
			cancel()
		}
		err = cmd.Wait()
		code := 0
		if err != nil {
			code = -1
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			}
		}
		for _, line := range strings.Split(stderr.String(), "\n") {
			if len(line) < 1000 && strings.Contains(line, ".go:") && len(diagnostics) < 30 {
				diagnostics = append(diagnostics, line)
			}
		}
		results[name] = map[string]any{"args": args, "exit_code": code, "timed_out": ctx.Err() != nil,
			"seconds": time.Since(when).Seconds(), "verdicts": verdicts, "diagnostics": diagnostics}
		if code != 0 || scanner.Err() != nil || ctx.Err() != nil {
			t.Errorf("%s failed: exit=%d timeout=%v scan=%v", name, code, ctx.Err(), scanner.Err())
		}
	}
	run("node_mempool_and_contract_packages", filepath.Join(root, "satoshinet"), []string{
		"test", "-json", "./mempool", "./contract/...", "-count=1", "-timeout=3m",
	})
	run("sdk_funding_race_repeat", sdk, []string{
		"test", "-json", "-race", "./wallet", "-run",
		"^(TestContractFundingSafety|TestContractAgentValueFundingSafety|TestEVMDeployRejectsLowGas|TestQueryAgentInvokeFeeIncludesResultFeeButNotBetAmount)$", "-count=3", "-timeout=3m",
	})
	run("temporary_WASM_compile", sdk, []string{
		"build", "-o", filepath.Join(t.TempDir(), "sat20wallet.wasm"), "./wasm",
	}, "GOOS=js", "GOARCH=wasm")
}

// This read-only waiter avoids rapid UI polling. It binds to one already
// launched run and saves only package verdicts and test names, not raw logs.
func TestSmartContractAwaitFullSuite(t *testing.T) {
	_, root, evidence := smartContractEvidencePaths(t)
	const logs = "/Users/yingfeng/mcp/local_access/logs/test-runs/sat20wallet-sdk-test"
	raw, err := os.ReadFile(filepath.Join(logs, "latest.run"))
	if err != nil {
		t.Fatal(err)
	}
	runID := strings.TrimSpace(string(raw))
	if !regexp.MustCompile(`^[0-9]{8}T[0-9]{6}-[0-9]+-all$`).MatchString(runID) {
		t.Fatalf("unexpected full-suite run id %q", runID)
	}
	parse := func(data []byte) map[string]string {
		result := map[string]string{}
		for _, line := range strings.Split(string(data), "\n") {
			key, value, ok := strings.Cut(line, "=")
			if ok {
				result[key] = value
			}
		}
		return result
	}
	deadline := time.Now().Add(7 * time.Minute)
	var state map[string]string
	for {
		raw, err = os.ReadFile(filepath.Join(logs, runID+".status"))
		if err != nil {
			t.Fatal(err)
		}
		state = parse(raw)
		if state["run_id"] != runID || state["variant"] != "all" {
			t.Fatal("runner identity changed")
		}
		if state["state"] != "RUNNING" || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Second)
	}
	file, err := os.Open(filepath.Join(logs, runID+".log"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	failed, packages := []string{}, []string{}
	failure := regexp.MustCompile(`^\s*--- FAIL: ([^ ]+)`)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 65536), 4<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if match := failure.FindStringSubmatch(line); match != nil {
			failed = append(failed, match[1])
		}
		if (strings.HasPrefix(line, "ok  ") || strings.HasPrefix(line, "FAIL\t") || strings.HasPrefix(line, "?   ")) && strings.Contains(line, "github.com/sat20-labs/sat20wallet/sdk") {
			packages = append(packages, line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	smartContractSaveEvidence(t, evidence, "smart-contract-full-suite-final.json", map[string]any{
		"run_id": runID, "observed_at": time.Now().Format(time.RFC3339), "state": state,
		"passed": state["state"] == "PASSED" && state["exit_code"] == "0" && len(failed) == 0,
		"failed_tests": failed, "package_verdicts": packages, "observed_source_sha256": smartContractSourceHashes(t, root),
	})
	if state["state"] == "RUNNING" {
		t.Logf("run %s still active; this waiter did not start or stop any process", runID)
		return
	}
	if state["state"] != "PASSED" || state["exit_code"] != "0" || len(failed) != 0 {
		t.Fatalf("full suite %s state=%s exit=%s failures=%v", runID, state["state"], state["exit_code"], failed)
	}
}
