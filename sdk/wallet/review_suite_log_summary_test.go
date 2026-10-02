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

// Summarize only test names and package verdicts from the approved local test
// runner's log. Do not copy verbose fixture keys, mnemonics or RGB payloads into
// the review report. This utility never runs or changes a wallet.
func TestReviewFullSuiteLogSummary(t *testing.T) {
	if !strings.Contains(flag.Lookup("test.run").Value.String(), "TestReviewFullSuiteLogSummary") {
		t.Skip("opt-in sanitized test-log inspection")
	}
	const runID = "20260930T222301-19054-all"
	const root = "/Users/yingfeng/mcp/local_access/logs/test-runs/sat20wallet-sdk-test"
	file, err := os.Open(filepath.Join(root, runID+".log"))
	if err != nil { t.Fatal(err) }
	defer file.Close()
	failed := make([]string, 0)
	packages := make([]string, 0)
	failureName := regexp.MustCompile(`^\s*--- FAIL: ([^ ]+)`)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 65536), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if match := failureName.FindStringSubmatch(line); match != nil {
			failed = append(failed, match[1])
		}
		if (strings.HasPrefix(line, "ok  ") || strings.HasPrefix(line, "FAIL\t") || strings.HasPrefix(line, "?   ")) && strings.Contains(line, "github.com/sat20-labs/sat20wallet/sdk") {
			packages = append(packages, line)
		}
	}
	if err := scanner.Err(); err != nil { t.Fatal(err) }
	status, err := os.ReadFile(filepath.Join(root, runID+".status"))
	if err != nil { t.Fatal(err) }
	report := map[string]any{"run_id": runID, "failed_tests": failed, "package_verdicts": packages, "status": string(status)}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil { t.Fatal(err) }
	cwd, err := os.Getwd()
	if err != nil { t.Fatal(err) }
	directory := filepath.Join(filepath.Dir(cwd), "review-evidence")
	if err := os.MkdirAll(directory, 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(directory, "full-suite-first-pass-20260930.json"), append(encoded, '\n'), 0600); err != nil { t.Fatal(err) }
}
