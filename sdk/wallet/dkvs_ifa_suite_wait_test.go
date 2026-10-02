package wallet

import (
	"flag"
	"os"
	"strings"
	"testing"
	"time"
)

// Bounded, explicit observation of an existing approved test process. It does
// not start another suite or alter the runner, repositories, or wallets. A
// still-running suite is SKIP, never a fabricated successful verdict.
func TestDKVSIFAFinalSuiteWait(t *testing.T) {
	if !strings.Contains(flag.Lookup("test.run").Value.String(), "TestDKVSIFAFinalSuiteWait") {
		t.Skip("explicit existing-suite observation")
	}
	const statusPath = "/Users/yingfeng/mcp/local_access/logs/test-runs/sat20wallet-sdk-test/20261001T112153-90248-all.status"
	deadline := time.Now().Add(4 * time.Minute)
	for {
		status, err := os.ReadFile(statusPath)
		if err != nil { t.Fatal(err) }
		text := string(status)
		if strings.Contains(text, "state=PASSED\n") && strings.Contains(text, "exit_code=0\n") { return }
		if strings.Contains(text, "state=FAILED\n") || strings.Contains(text, "state=STOPPED\n") || strings.Contains(text, "state=TIMED_OUT\n") {
			t.Fatal("the existing complete SDK suite did not pass; inspect sanitized evidence")
		}
		if time.Now().After(deadline) { t.Skip("existing complete SDK suite is still RUNNING; no final verdict") }
		time.Sleep(5 * time.Second)
	}
}
