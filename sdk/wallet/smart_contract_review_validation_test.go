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
	"runtime"
	"strings"
	"testing"
	"time"
)

// Opt-in adapters for the existing fixed MCP focused-test profile. Normal
// go test ./... runs business E2Es directly, never recursively through here.
func TestSmartContractModuleValidation(t *testing.T) {
	runSmartContractModuleValidation(t, "^TestSDKSmartContracts(EVM|OutOfGas|Templates|Additional|Funding|Admission)$")
}

func TestSmartContractAdmissionValidation(t *testing.T) {
	runSmartContractModuleValidation(t, "^TestSDKSmartContractsAdmission$")
}

func runSmartContractModuleValidation(t *testing.T, pattern string) {
	t.Helper()
	if !strings.Contains(flag.Lookup("test.run").Value.String(), t.Name()) {
		t.Skip("explicit SDK smart-contract E2E validation entry")
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok { t.Fatal("locate SDK review test") }
	sdk := filepath.Dir(filepath.Dir(source))
	repoRoot := filepath.Dir(filepath.Dir(sdk))
	dir := filepath.Join(sdk, "review-evidence")
	if err := os.MkdirAll(dir, 0700); err != nil { t.Fatal(err) }
	hashes := make(map[string]string)
	paths := []string{
		"sat20wallet/sdk/wallet/interface_contract_unified.go", "sat20wallet/sdk/wallet/sign.go", "sat20wallet/sdk/wallet/restclient_contract.go",
		"satoshinet/contract/evm/runtime.go", "satoshinet/contract/evm/backend.go", "satoshinet/contract/evm/precompile.go",
		"satoshinet/contract/template/backend.go", "satoshinet/contract/template/autopay.go",
		"satoshinet/contract/framework/failure.go", "satoshinet/contract/framework/executor.go", "satoshinet/contract/framework/canonical_result.go",
		"satoshinet/mempool/mempool.go", "satoshinet/mining/mining.go", "satoshinet/rpcserver.go",
		"transcend/stp/contract_deploy.go", "transcend/stp/contract_invoke.go", "transcend/stp/contractmgr.go",
		"sat20wallet/sdk/e2e/smart_contract_evm_sdk_e2e_test.go", "sat20wallet/sdk/e2e/smart_contract_template_sdk_e2e_test.go",
		"sat20wallet/sdk/e2e/smart_contract_additional_sdk_e2e_test.go", "sat20wallet/sdk/e2e/smart_contract_admission_sdk_e2e_test.go",
		"sat20wallet/sdk/e2e/smart_contract_sdk_helpers_test.go", "sat20wallet/sdk/e2e/testdata/contracts/SDKReviewProbe.sol",
	}
	for _, relative := range paths {
		data, err := os.ReadFile(filepath.Join(repoRoot, relative))
		if err != nil { t.Fatal(err) }
		hashes[relative] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	for _, relative := range []string{"sat20wallet/sdk/wallet/contract_funding_safety.go", "satoshinet/mempool/contract_admission.go"} {
		data, err := os.ReadFile(filepath.Join(repoRoot, relative))
		if os.IsNotExist(err) { hashes[relative] = "absent"; continue }
		if err != nil { t.Fatal(err) }
		hashes[relative] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	args := []string{"test", "-json", "./e2e", "-run", pattern, "-count=1", "-timeout=8m"}
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), args...)
	cmd.Dir = sdk
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "SAT20WALLET_RUN_LIVE_NETWORK_TESTS=") && !strings.HasPrefix(entry, "SAT20WALLET_CORE_E2E_CONFIG=") { cmd.Env = append(cmd.Env, entry) }
	}
	pipe, err := cmd.StdoutPipe()
	if err != nil { t.Fatal(err) }
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	started := time.Now()
	type event struct { Action, Package, Test, Output string; Elapsed float64 }
	var verdicts []event
	var diagnostics []string
	var actualRunNames []string
	innerTimeout := false
	save := func(filename string, value any) error {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil { return err }
		path := filepath.Join(dir, filename)
		if err := os.WriteFile(path+".tmp", append(data, '\n'), 0600); err != nil { return err }
		return os.Rename(path+".tmp", path)
	}
	progress := func(current string) error {
		return save("smart-contract-review-progress.json", map[string]any{
			"started_at": started.Format(time.RFC3339), "observed_at": time.Now().Format(time.RFC3339),
			"current_test": current, "executed_tests": actualRunNames, "verdicts": verdicts, "diagnostics": diagnostics})
	}
	if err := progress("compiling"); err != nil { t.Fatal(err) }
	if err := cmd.Start(); err != nil { t.Fatal(err) }
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 65536), 4<<20)
	for scanner.Scan() {
		var item event
		if json.Unmarshal(scanner.Bytes(), &item) != nil { continue }
		line := strings.TrimSpace(item.Output)
		innerTimeout = innerTimeout || strings.Contains(line, "test timed out")
		if strings.Contains(line, "contract-review:") || strings.Contains(line, "Error Trace:") || strings.Contains(line, "Error:") || strings.Contains(line, "expected:") || strings.Contains(line, "actual  :") || strings.Contains(line, "panic:") {
			if len(line) > 1800 { line = line[:1800] }
			if len(diagnostics) < 120 { diagnostics = append(diagnostics, line) }
		}
		if item.Action == "run" { actualRunNames = append(actualRunNames, item.Test) }
		finished := item.Action == "pass" || item.Action == "fail" || item.Action == "skip"
		if finished { item.Output = ""; verdicts = append(verdicts, item) }
		if item.Action == "run" || finished {
			if err := progress(item.Test); err != nil { cancel(); _ = cmd.Wait(); t.Fatal(err) }
		}
	}
	if scanner.Err() != nil { cancel() }
	runErr := cmd.Wait()
	if runErr != nil && len(stderr.Bytes()) < 12000 {
		for _, line := range strings.Split(stderr.String(), "\n") {
			if strings.Contains(line, ".go:") || strings.HasPrefix(line, "# ") { diagnostics = append(diagnostics, line) }
		}
	}
	changedSources := []string{}
	for relative, hash := range hashes {
		data, err := os.ReadFile(filepath.Join(repoRoot, relative))
		if os.IsNotExist(err) && hash == "absent" { continue }
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(data)) != hash { changedSources = append(changedSources, relative) }
	}
	passed := runErr == nil && scanner.Err() == nil && len(actualRunNames) > 0 && len(changedSources) == 0
	report := map[string]any{"started_at": started.Format(time.RFC3339), "finished_at": time.Now().Format(time.RFC3339),
		"passed": passed, "timed_out": ctx.Err() != nil || innerTimeout, "args": args,
		"verdicts": verdicts, "executed_tests": actualRunNames, "diagnostics": diagnostics, "source_sha256": hashes,
		"changed_sources_during_run": changedSources,
		"boundary": "SDK public APIs -> real isolated SatoshiNet nodes with Transcend/SDK plugins; initial L1 provenance and funding are fixtures; malformed policy cases use SDK signing/broadcast APIs; restart is test-node only"}
	suffix := started.Format("20060102T150405")
	for _, filename := range []string{"smart-contract-review-latest.json", "smart-contract-review-" + suffix + ".json"} {
		if err := save(filename, report); err != nil { t.Fatal(err) }
	}
	if !passed {
		for i, line := range diagnostics { if i < 32 { t.Log(line) } }
		t.Fatalf("contract-review: SDK E2E failed; timeout=%t process=%v scanner=%v started=%d changed=%v", ctx.Err() != nil || innerTimeout, runErr, scanner.Err(), len(actualRunNames), changedSources)
	}
}
