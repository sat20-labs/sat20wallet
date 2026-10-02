package wallet

import (
	"bufio"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// This opt-in reader exports only test identifiers, package verdicts and
// selected assertion diagnostics from the fixed SDK test runner log. It never
// copies fixture mnemonics, keys, wallet databases or transaction payloads.
func TestSmartContractResumeEvidence(t *testing.T) {
	if !strings.Contains(flag.Lookup("test.run").Value.String(), "TestSmartContractResumeEvidence") {
		t.Skip("explicit sanitized smart-contract test-log inspection")
	}
	const runID = "20261001T175726-40341-all"
	const root = "/Users/yingfeng/mcp/local_access/logs/test-runs/sat20wallet-sdk-test"
	file, err := os.Open(filepath.Join(root, runID+".log"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	failureName := regexp.MustCompile(`^\s*--- FAIL: ([^ ]+)`)
	location := regexp.MustCompile(`^\s+[A-Za-z0-9_]+_test\.go:[0-9]+:`)
	failed, packages, diagnostics := []string{}, []string{}, []string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 65536), 4*1024*1024)
	failureSection := false
	for scanner.Scan() {
		line := scanner.Text()
		if match := failureName.FindStringSubmatch(line); match != nil {
			failed = append(failed, match[1])
			failureSection = true
		}
		if (strings.HasPrefix(line, "ok  ") || strings.HasPrefix(line, "FAIL\t") || strings.HasPrefix(line, "?   ")) && strings.Contains(line, "github.com/sat20-labs/sat20wallet/sdk") {
			packages = append(packages, line)
		}
		lower := strings.ToLower(line)
		if failureSection && (location.MatchString(line) || strings.HasPrefix(strings.TrimSpace(line), "Error:") || strings.HasPrefix(strings.TrimSpace(line), "Messages:") || strings.Contains(line, "gas limit")) && len(line) <= 800 && !strings.Contains(lower, "mnemonic") && !strings.Contains(lower, "private") && !strings.Contains(lower, "password") {
			if len(diagnostics) < 80 {
				diagnostics = append(diagnostics, strings.TrimSpace(line))
			}
		}
		if strings.HasPrefix(line, "2026-") || strings.HasPrefix(line, "PASS") {
			failureSection = false
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	status, err := os.ReadFile(filepath.Join(root, runID+".status"))
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(filepath.Dir(cwd), "review-evidence")
	type verdict struct {
		Action string
		Test string
	}
	var module struct {
		Passed bool `json:"passed"`
		FinishedAt string `json:"finished_at"`
		Diagnostics []string `json:"diagnostics"`
		Verdicts []verdict `json:"verdicts"`
	}
	data, err := os.ReadFile(filepath.Join(directory, "smart-contract-review-latest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &module); err != nil {
		t.Fatal(err)
	}
	leafCounts := map[string]int{}
	moduleFailures := []string{}
	for _, item := range module.Verdicts {
		if item.Test == "" {
			continue
		}
		parent := false
		for _, other := range module.Verdicts {
			if strings.HasPrefix(other.Test, item.Test+"/") {
				parent = true
				break
			}
		}
		if parent {
			continue
		}
		leafCounts[item.Action]++
		if item.Action == "fail" {
			moduleFailures = append(moduleFailures, item.Test)
		}
	}
	report := map[string]any{
		"run_id": runID, "failed_tests": failed, "package_verdicts": packages,
		"status": string(status), "assertion_diagnostics": diagnostics,
		"module_passed": module.Passed, "module_finished_at": module.FinishedAt,
		"module_leaf_verdicts": leafCounts, "module_failed_tests": moduleFailures,
		"module_diagnostics": module.Diagnostics,
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "smart-contract-resume-status.json"), append(encoded, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("full-suite failures=%v; module leaf verdicts=%v", failed, leafCounts)
}
