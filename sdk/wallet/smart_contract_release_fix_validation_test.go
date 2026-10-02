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

// These adapters collect evidence for fixed MCP profiles. All business cases
// are untagged and run directly under normal go test ./....
func TestSmartContractReleaseFixValidation(t *testing.T) {
	runSmartContractReleaseFixValidation(t, false)
}

func TestSmartContractRawEscrowValidation(t *testing.T) {
	runSmartContractReleaseFixValidation(t, true)
}

func runSmartContractReleaseFixValidation(t *testing.T, rawOnly bool) {
	t.Helper()
	if !strings.Contains(flag.Lookup("test.run").Value.String(), t.Name()) {
		t.Skip("explicit evidence capture")
	}
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate SDK")
	}
	sdk := filepath.Dir(filepath.Dir(here))
	root := filepath.Dir(filepath.Dir(sdk))
	evidence := filepath.Join(sdk, "review-evidence")
	started := time.Now()
	run := started.Format("20060102T150405")
	sources := []string{
		"sat20wallet/sdk/go.mod", "sat20wallet/sdk/go.sum",
		"sat20wallet/sdk/wallet/interface_contract_unified.go",
		"satoshinet/blockchain/validate.go", "satoshinet/blockchain/asset_validation.go",
		"satoshinet/contract/evm/backend.go", "satoshinet/contract/template/backend.go",
		"satoshinet/contract/funding_safety.go", "satoshinet/mempool/contract_admission.go",
		"sat20wallet/sdk/e2e/smart_contract_release_gate_e2e_test.go",
		"sat20wallet/sdk/e2e/smart_contract_deploy_escrow_e2e_test.go",
	}
	before := map[string]string{}
	snapshotDir := filepath.Join(evidence, "release-fix-sources-"+run)
	if err := os.MkdirAll(snapshotDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range sources {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = fmt.Sprintf("%x", sha256.Sum256(data))
		if err := os.WriteFile(filepath.Join(snapshotDir, strings.ReplaceAll(name, "/", "__")+".before"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// The additional untagged blockchain imports use the existing pinned x/net
	// version. The original entries must not be silently upgraded.
	data, err := os.ReadFile(filepath.Join(sdk, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	old := strings.ReplaceAll(string(data), "golang.org/x/net v0.47.0 h1:Mx+4dIFzqraBXUugkia1OOvlD6LemFo1ALMHjrXDOhY=\n", "")
	old = strings.ReplaceAll(old, "golang.org/x/net v0.47.0/go.mod h1:/jNxtkgq5yWUGYkaZGqo27cfGZ1c5Nen03aYrrKpVRU=\n", "")
	if fmt.Sprintf("%x", sha256.Sum256([]byte(old))) != "4a2aff569f7d40034c45e050b483ce33155086b03264c498a94fef4175041f17" {
		t.Fatal("unrelated checksum entry changed")
	}
	type event struct {
		Action, Package, Test string
		Elapsed               float64
	}
	type commandSpec struct {
		name string
		args []string
	}
	commands := []commandSpec{
		{"SDK_budget", []string{"test", "-json", "-mod=readonly", "./wallet", "-run", "^TestContractDeploymentEscrowBudget$", "-count=1", "-timeout=1m"}},
		{"release_E2E", []string{"test", "-json", "-mod=readonly", "./e2e", "-run", "^TestSDKSmartContract(Release|TemplateRelease|DeployFailure)", "-count=1", "-timeout=6m"}},
	}
	prefix := "smart-contract-release-fix"
	if rawOnly {
		prefix = "smart-contract-raw-escrow"
		commands = []commandSpec{{"raw_escrow_E2E", []string{"test", "-json", "-mod=readonly", "./e2e", "-run", "^TestSDKSmartContractDeployFailureSettlement$", "-count=1", "-timeout=4m"}}}
	}
	checks := map[string]any{}
	for _, command := range commands {
		ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
		cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), command.args...)
		cmd.Dir = sdk
		for _, item := range os.Environ() {
			if !strings.HasPrefix(item, "SAT20WALLET_RUN_LIVE_NETWORK_TESTS=") && !strings.HasPrefix(item, "SAT20WALLET_CORE_E2E_CONFIG=") && !strings.HasPrefix(item, "GOFLAGS=") {
				cmd.Env = append(cmd.Env, item)
			}
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			cancel()
			t.Fatal(err)
		}
		verdicts, diagnostics := []event{}, []string{}
		scanner := bufio.NewScanner(pipe)
		scanner.Buffer(make([]byte, 65536), 4<<20)
		continuation := 0
		for scanner.Scan() {
			var item struct {
				event
				Output string
			}
			if json.Unmarshal(scanner.Bytes(), &item) != nil {
				continue
			}
			if item.Action == "pass" || item.Action == "fail" || item.Action == "skip" {
				verdicts = append(verdicts, item.event)
			}
			line := strings.TrimSpace(item.Output)
			lower := strings.ToLower(line)
			relevant := strings.Contains(line, "release-review:") || strings.Contains(line, "contract-review:") || strings.HasPrefix(line, "Error:") || strings.Contains(line, "Error Trace:") || strings.HasPrefix(line, "panic:") || strings.Contains(line, "test timed out") || item.Action == "build-output"
			// A testify diagnostic places the actual error on the next output
			// line. Retain only two bounded, non-sensitive continuation lines.
			if len(line) < 1800 && len(diagnostics) < 120 && !strings.Contains(lower, "mnemonic") && !strings.Contains(lower, "private") && !strings.Contains(lower, "password") && (relevant || continuation > 0) {
				diagnostics = append(diagnostics, line)
			}
			if continuation > 0 {
				continuation--
			}
			if strings.HasPrefix(line, "Error:") {
				continuation = 2
			}
		}
		if scanner.Err() != nil {
			cancel()
		}
		runErr := cmd.Wait()
		timedOut := ctx.Err() != nil
		cancel()
		for _, line := range strings.Split(stderr.String(), "\n") {
			if len(line) < 1500 && (strings.Contains(line, ".go:") || strings.Contains(line, "go.sum") || strings.Contains(line, "go.mod")) {
				diagnostics = append(diagnostics, line)
			}
		}
		code := 0
		if runErr != nil {
			code = -1
			if exit, ok := runErr.(*exec.ExitError); ok {
				code = exit.ExitCode()
			}
		}
		checks[command.name] = map[string]any{"args": command.args, "exit_code": code, "timed_out": timedOut, "verdicts": verdicts, "diagnostics": diagnostics}
		if runErr != nil || scanner.Err() != nil || timedOut {
			t.Errorf("%s failed: exit=%d timeout=%v", command.name, code, timedOut)
		}
	}
	changed := []string{}
	for name, hash := range before {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(data)) != hash {
			changed = append(changed, name)
		}
	}
	if len(changed) != 0 {
		t.Errorf("source changed during run: %v", changed)
	}
	report := map[string]any{"run_id": run, "started_at": started.Format(time.RFC3339), "finished_at": time.Now().Format(time.RFC3339), "passed": !t.Failed(), "checks": checks, "source_sha256": before, "changed_sources": changed, "source_snapshot_directory": snapshotDir}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{prefix + "-latest.json", prefix + "-" + run + ".json"} {
		if err := os.WriteFile(filepath.Join(evidence, name), append(encoded, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
