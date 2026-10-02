package wallet

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Read-only inspection of this approved full-suite run. It deliberately
// exports names/verdicts/hashes, not verbose fixture wallet data or payloads.
func TestDKVSIFAFinalSuiteEvidence(t *testing.T) {
	if !strings.Contains(flag.Lookup("test.run").Value.String(), "TestDKVSIFAFinalSuiteEvidence") {
		t.Skip("explicit sanitized suite evidence")
	}
	const runID = "20261001T112153-90248-all"
	const logRoot = "/Users/yingfeng/mcp/local_access/logs/test-runs/sat20wallet-sdk-test"
	_, source, _, ok := runtime.Caller(0)
	if !ok { t.Fatal("locate evidence source") }
	sdk := filepath.Dir(filepath.Dir(source))
	workspace := filepath.Dir(filepath.Dir(sdk))
	file, err := os.Open(filepath.Join(logRoot, runID+".log"))
	if err != nil { t.Fatal(err) }
	defer file.Close()
	failed := []string{}
	packages := []string{}
	failureName := regexp.MustCompile(`^\s*--- FAIL: ([^ ]+)`)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 65536), 4<<20)
	panicFound := false
	for scanner.Scan() {
		line := scanner.Text()
		if match := failureName.FindStringSubmatch(line); match != nil { failed = append(failed, match[1]) }
		if strings.HasPrefix(line, "panic:") || strings.HasPrefix(line, "fatal error:") { panicFound = true }
		if (strings.HasPrefix(line, "ok  ") || strings.HasPrefix(line, "FAIL\t") || strings.HasPrefix(line, "?   ")) &&
			strings.Contains(line, "github.com/sat20-labs/sat20wallet/sdk") { packages = append(packages, line) }
	}
	if err := scanner.Err(); err != nil { t.Fatal(err) }
	status, err := os.ReadFile(filepath.Join(logRoot, runID+".status"))
	if err != nil { t.Fatal(err) }
	hashes := make(map[string]string)
	paths := []string{
		"satoshinet/indexer/indexer/dkvs/delete.go",
		"satoshinet/indexer/indexer/dkvs/batch_cas.go",
		"satoshinet/indexer/indexer/dkvs/path_snapshot_apply.go",
		"satoshinet/indexer/indexer/dkvs/prefix_sync.go",
		"satoshinet/indexer/indexer/dkvs/prefix_delete_sync.go",
		"satoshinet/indexer/indexer/dkvs/prefix_delete_sync_test.go",
		"sat20wallet/sdk/wallet/dkvs/replica.go",
		"sat20wallet/sdk/wallet/dkvs/prefix_payload.go",
		"sat20wallet/sdk/wallet/rgb11/snapshot.go",
		"sat20wallet/sdk/wallet/rgb11/snapshot_ticker.go",
		"sat20wallet/sdk/wallet/dkvs_tombstone_roundtrip_test.go",
		"sat20wallet/sdk/wallet/dkvs_deleted_payload_validation_test.go",
		"sat20wallet/sdk/wallet/ifa_recovery_regression_test.go",
		"sat20wallet/sdk/wallet/ifa_ticker_integrity_test.go",
		"sat20wallet/sdk/wallet/core_modules_e2e_pending_policy_test.go",
		"sat20wallet/sdk/wallet/core_modules_e2e_transfer_test.go",
	}
	for _, path := range paths {
		raw, err := os.ReadFile(filepath.Join(workspace, path))
		if err != nil { t.Fatal(err) }
		digest := sha256.Sum256(raw)
		hashes[path] = hex.EncodeToString(digest[:])
	}
	report := map[string]any{
		"run_id": runID, "observed_at": time.Now().Format(time.RFC3339),
		"status": string(status), "failed_tests": failed, "panic_found": panicFound,
		"package_verdicts": packages, "source_sha256": hashes,
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil { t.Fatal(err) }
	dir := filepath.Join(sdk, "review-evidence")
	if err := os.MkdirAll(dir, 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(dir, "dkvs-ifa-full-suite-20261001.json"), append(encoded, '\n'), 0600); err != nil { t.Fatal(err) }
}
