package wallet

import (
	"context"
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

// These expensive checks are explicitly invoked through the existing focused
// test runner. They neither alter the runner nor overwrite deployed WASM files.
func TestReviewScopedValidation(t *testing.T) {
	if !strings.Contains(flag.Lookup("test.run").Value.String(), "TestReviewScopedValidation") {
		t.Skip("opt-in race detector and temporary WASM compilation")
	}
	walletDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	moduleDir := filepath.Dir(walletDir)
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	results := make(map[string]any)
	results["generated_at"] = time.Now().Format(time.RFC3339)
	defer func() {
		raw, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		dir := filepath.Join(moduleDir, "review-evidence")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Error(err)
			return
		}
		if err := os.WriteFile(filepath.Join(dir, "scoped-validation-20260930.json"), append(raw, '\n'), 0600); err != nil {
			t.Error(err)
		}
	}()
	baseEnv := make([]string, 0)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "SAT20WALLET_RUN_LIVE_NETWORK_TESTS=") ||
			strings.HasPrefix(entry, "GOOS=") || strings.HasPrefix(entry, "GOARCH=") {
			continue
		}
		baseEnv = append(baseEnv, entry)
	}
	run := func(t *testing.T, name string, args []string, extraEnv ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 420*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, goBinary, args...)
		command.Dir = moduleDir
		command.Env = append(append([]string(nil), baseEnv...), extraEnv...)
		started := time.Now()
		output, err := command.CombinedOutput()
		code := 0
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				code = -1
			}
		}
		results[name] = map[string]any{
			"exit_code": code, "seconds": time.Since(started).Seconds(),
			"timed_out": ctx.Err() != nil, "command": args,
			"output": string(output),
		}
		if err != nil || ctx.Err() != nil {
			t.Errorf("%s failed (exit=%d): %v\n%s", name, code, err, output)
		}
	}
	regressions := []string{
		"TestReviewAccountMergeAppliesRemoteIncrement",
		"TestReviewRGB11BaselineRechecksOperationPreconditions",
		"TestReviewRGB11BaselineImportsActiveAfterLocalPublication",
		"TestReviewStorageAuthorizationOperationOwnership",
		"TestReviewStorageCancelInvalidatesInflightConfirmation",
		"TestReviewRGB11ReadDoesNotMixAtomicInvoiceGenerations",
		"TestAccountManagedPutAckPreservesChangesDuringNetworkWait",
		"TestAccountPaidActivationSelectionChangeDoesNotChangeRootIdentity",
		"TestRGB11ManagedBaselineNetworkWaitDoesNotBlockRGBState",
		"TestRGB11ManagedOperationWaitDoesNotBlockReadOnlyAPIs",
		"TestRGB11ManagedOperationKeepsScopeChangeBlocked",
		"TestRGB11DirectReservationResumesAfterSDKRestart",
		"TestAccountStorageAuthorizationOwnedByRootSDKSession",
		"TestPendingAccountStorageAuthorizationIsRootBoundNotSelectionBound",
		"TestReviewStorageAuthorizationRejectsForeignEndpoint",
	}
	results["regression_entry_points"] = len(regressions)
	t.Run("race_regressions", func(t *testing.T) {
		pattern := "^(" + strings.Join(regressions, "|") + ")$"
		run(t, "race_regressions", []string{"test", "-race", "-count=1", "-timeout=300s", "./wallet", "-run", pattern})
	})
	t.Run("wasm_build_without_deployment", func(t *testing.T) {
		output := filepath.Join(t.TempDir(), "sat20wallet.wasm")
		run(t, "wasm_build", []string{"build", "-o", output, "./wasm"}, "GOOS=js", "GOARCH=wasm")
		if info, err := os.Stat(output); err != nil || info.Size() == 0 {
			t.Error(fmt.Errorf("temporary WASM artifact is unavailable: %v", err))
		}
	})
}
