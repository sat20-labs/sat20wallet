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

// This explicit evidence replay compiles the two original deployment branches
// with Go's overlay. It never rewrites production sources or Git state. The
// normal untagged E2E uses the fixed implementation and must remain green.
func TestSmartContractDeploymentRefundRedEvidence(t *testing.T) {
	if !strings.Contains(flag.Lookup("test.run").Value.String(), t.Name()) {
		t.Skip("explicit baseline evidence replay")
	}
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate SDK")
	}
	sdk := filepath.Dir(filepath.Dir(here))
	root := filepath.Dir(filepath.Dir(sdk))
	evidence := filepath.Join(sdk, "review-evidence")
	baseline := filepath.Join(evidence, "release-fix-sources-20261002T001718")
	expected := map[string]string{
		"satoshinet/contract/evm/backend.go": "6c7afe525078fb5c0f6ae854f1e372ff40c6d0160f44adce21cee9ff5a11de90",
		"satoshinet/contract/template/backend.go": "6f18205c8ceb40e54289b5ea320fae2df748c1c0e6fe652fd7725b51c5661aef",
	}
	replacements, before := map[string]string{}, map[string]string{}
	for relative, hash := range expected {
		oldPath := filepath.Join(baseline, strings.ReplaceAll(relative, "/", "__")+".before")
		old, err := os.ReadFile(oldPath)
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(old)) != hash {
			t.Fatalf("baseline snapshot missing or changed: %s", relative)
		}
		path := filepath.Join(root, relative)
		current, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = fmt.Sprintf("%x", sha256.Sum256(current))
		replacements[path] = oldPath
	}
	overlay, err := json.Marshal(map[string]any{"Replace": replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(t.TempDir(), "baseline-overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	args := []string{"test", "-json", "-mod=readonly", "./e2e", "-run", "^TestSDKSmartContractDeployFailureSettlement$", "-count=1", "-timeout=5m"}
	cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), args...)
	cmd.Dir = sdk
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOFLAGS=") && !strings.HasPrefix(entry, "SAT20WALLET_RUN_LIVE_NETWORK_TESTS=") && !strings.HasPrefix(entry, "SAT20WALLET_CORE_E2E_CONFIG=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	// GOFLAGS is inherited by the temporary node/plugin builds as well.
	cmd.Env = append(cmd.Env, "GOFLAGS=-overlay="+overlayPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	failed := map[string]bool{}
	seenCause := map[string]bool{}
	diagnostics := []string{}
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 65536), 4<<20)
	for scanner.Scan() {
		var item struct{ Action, Test, Output string }
		if json.Unmarshal(scanner.Bytes(), &item) != nil {
			continue
		}
		if item.Action == "fail" && strings.Contains(item.Test, "/") {
			failed[item.Test] = true
		}
		line := strings.TrimSpace(item.Output)
		if len(line) < 1800 && strings.Contains(line, "deployment has insufficient Result gas") {
			if len(diagnostics) < 12 {
				diagnostics = append(diagnostics, line)
			}
			if strings.Contains(line, "EVM deployment") {
				seenCause["evm"] = true
			}
			if strings.Contains(line, "template deployment") {
				seenCause["template"] = true
			}
		}
	}
	if scanner.Err() != nil {
		cancel()
	}
	runErr := cmd.Wait()
	code := 0
	if runErr != nil {
		code = -1
		if exit, ok := runErr.(*exec.ExitError); ok {
			code = exit.ExitCode()
		}
	}
	unchanged := true
	for path, hash := range before {
		current, err := os.ReadFile(path)
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(current)) != hash {
			unchanged = false
		}
	}
	verified := code == 1 && scanner.Err() == nil && ctx.Err() == nil && unchanged && len(failed) == 2 &&
		failed["TestSDKSmartContractDeployFailureSettlement/evm"] && failed["TestSDKSmartContractDeployFailureSettlement/template"] &&
		seenCause["evm"] && seenCause["template"]
	report := map[string]any{
		"started_at": started.Format(time.RFC3339), "finished_at": time.Now().Format(time.RFC3339),
		"expected_red_verified": verified, "child_exit_code": code, "timed_out": ctx.Err() != nil,
		"failed_leaf_tests": failed, "failure_causes": seenCause, "diagnostics": diagnostics,
		"baseline_source_sha256": expected, "worktree_source_sha256": before, "worktree_unchanged": unchanged,
		"scope": "Only EVM/template backend files restored in compile overlay; same current raw SDK E2E and new asset-validation rules",
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidence, "smart-contract-refund-red-evidence.json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if !verified {
		t.Fatalf("baseline did not reproduce both expected business failures: exit=%d cases=%v causes=%v", code, failed, seenCause)
	}
}
