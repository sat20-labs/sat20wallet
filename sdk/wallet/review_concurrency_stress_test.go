package wallet

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestReviewConcurrencyStress(t *testing.T) {
	if !strings.Contains(flag.Lookup("test.run").Value.String(), "TestReviewConcurrencyStress") {
		t.Skip("opt-in repeated race-detector regression")
	}
	walletDir, err := os.Getwd()
	if err != nil { t.Fatal(err) }
	moduleDir := filepath.Dir(walletDir)
	ctx, cancel := context.WithTimeout(context.Background(), 420*time.Second)
	defer cancel()
	pattern := "^(TestReviewStorageAuthorizationOperationOwnership|TestReviewStorageCancelInvalidatesInflightConfirmation|TestReviewRGB11ReadDoesNotMixAtomicInvoiceGenerations)$"
	args := []string{"test", "-race", "-count=5", "-timeout=360s", "./wallet", "-run", pattern}
	command := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), args...)
	command.Dir = moduleDir
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "SAT20WALLET_RUN_LIVE_NETWORK_TESTS=") {
			command.Env = append(command.Env, entry)
		}
	}
	started := time.Now()
	output, runErr := command.CombinedOutput()
	code := 0
	if runErr != nil {
		if exit, ok := runErr.(*exec.ExitError); ok { code = exit.ExitCode() } else { code = -1 }
	}
	report := map[string]any{
		"started_at": started.Format(time.RFC3339), "exit_code": code,
		"seconds": time.Since(started).Seconds(), "timed_out": ctx.Err() != nil,
		"command": args, "output": string(output), "leaf_scenarios_per_iteration": 6,
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil { t.Fatal(err) }
	directory := filepath.Join(moduleDir, "review-evidence")
	if err := os.MkdirAll(directory, 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(directory, "concurrency-stress-20260930.json"), append(raw, '\n'), 0600); err != nil { t.Fatal(err) }
	if runErr != nil || ctx.Err() != nil {
		t.Fatalf("repeated concurrency regression failed (exit=%d): %v\n%s", code, runErr, output)
	}
}
