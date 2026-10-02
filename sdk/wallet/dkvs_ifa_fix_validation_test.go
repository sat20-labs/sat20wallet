package wallet

import (
	"bufio"
	"bytes"
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

// Explicit evidence runner. Normal go test ./... executes the business tests
// directly, not nested recursive package invocations.
func TestDKVSIFAFixValidation(t *testing.T) {
	if !strings.Contains(flag.Lookup("test.run").Value.String(), "TestDKVSIFAFixValidation") {
		t.Skip("explicit DKVS/IFA regression and race verification")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok { t.Fatal("locate validation source") }
	sdk := filepath.Dir(filepath.Dir(file))
	type event struct {
		Action string
		Package string
		Test string
		Output string
		Elapsed float64
	}
	type result struct {
		Name string `json:"name"`
		Args []string `json:"args"`
		Passed bool `json:"passed"`
		TimedOut bool `json:"timed_out"`
		Verdicts []event `json:"verdicts"`
		Failures []string `json:"failure_names"`
		Elapsed float64 `json:"elapsed_seconds"`
	}
	groups := []struct{name string; args []string}{
		{"replica_and_RGB_packages", []string{"test", "-json", "./wallet/dkvs", "./wallet/rgb11", "-count=1", "-timeout=4m"}},
		{"deletion_and_IFA_race_repeat", []string{"test", "-json", "-race", "./wallet", "-run", "^(TestDKVSTombstoneRoundTrip|TestDKVSDeletedPayloadValidationIsAtomic|TestIFARecoveryPreservesControlAssignments|TestIFATickerObjectTamperingRejected|TestRGB11AccountManagedProviderRestoresDurableOwnershipWithoutLocalTasks)$", "-count=3", "-timeout=4m"}},
	}
	var results []result
	for _, group := range groups {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute+30*time.Second)
		cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), group.args...)
		cmd.Dir = sdk
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "SAT20WALLET_RUN_LIVE_NETWORK_TESTS=") &&
				!strings.HasPrefix(entry, "SAT20WALLET_CORE_E2E_CONFIG=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		started := time.Now()
		raw, err := cmd.CombinedOutput()
		value := result{Name: group.name, Args: group.args, Passed: err == nil, TimedOut: ctx.Err() != nil, Elapsed: time.Since(started).Seconds()}
		cancel()
		scanner := bufio.NewScanner(bytes.NewReader(raw))
		scanner.Buffer(make([]byte, 65536), 4<<20)
		for scanner.Scan() {
			var item event
			if json.Unmarshal(scanner.Bytes(), &item) != nil { continue }
			if item.Action == "pass" || item.Action == "fail" || item.Action == "skip" {
				item.Output = ""
				value.Verdicts = append(value.Verdicts, item)
				if item.Action == "fail" { value.Failures = append(value.Failures, item.Package+"/"+item.Test) }
			}
		}
		if scanner.Err() != nil || len(value.Verdicts) == 0 { value.Passed = false }
		results = append(results, value)
		if !value.Passed { t.Errorf("validation group %s failed: tests=%v timeout=%t", group.name, value.Failures, value.TimedOut) }
	}
	report := map[string]any{"finished_at": time.Now().Format(time.RFC3339), "groups": results}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil { t.Fatal(err) }
	dir := filepath.Join(sdk, "review-evidence")
	if err := os.MkdirAll(dir, 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(dir, "dkvs-ifa-fix-validation-20261001.json"), append(encoded, '\n'), 0600); err != nil { t.Fatal(err) }
}
