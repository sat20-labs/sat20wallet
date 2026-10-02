package wallet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewBaselinePublicationEvidence(t *testing.T) {
	if !strings.Contains(flag.Lookup("test.run").Value.String(), "TestReviewBaselinePublicationEvidence") {
		t.Skip("opt-in baseline publication negative control")
	}
	walletDir, err := os.Getwd()
	if err != nil { t.Fatal(err) }
	moduleDir := filepath.Dir(walletDir)
	sourcePath := filepath.Join(walletDir, "rgb11_managed_operation.go")
	original, err := os.ReadFile(sourcePath)
	if err != nil { t.Fatal(err) }
	modified := reviewReplaceIf(t, string(original), "err != nil",
		"confirm RGB11 active account recovery", "")
	temporary := t.TempDir()
	backing := filepath.Join(temporary, "control.go")
	if err := os.WriteFile(backing, []byte(modified), 0600); err != nil { t.Fatal(err) }
	mapping, err := json.Marshal(map[string]any{"Replace": map[string]string{sourcePath: backing}})
	if err != nil { t.Fatal(err) }
	overlay := filepath.Join(temporary, "overlay.json")
	if err := os.WriteFile(overlay, mapping, 0600); err != nil { t.Fatal(err) }
	entry := reviewEvidenceCase{
		Test: "TestReviewRGB11BaselineImportsActiveAfterLocalPublication",
		Control: "Remove only the post-baseline active-mailbox confirmation; preserve the earlier source and local-active rechecks. This restores the implementation on which the publication regression failed before its fix.",
	}
	markers := []string{"baseline publication bypassed remote active recovery", "remote active recovery was not imported after baseline publication"}
	entry.Red = reviewRunEvidenceChild(t, moduleDir, entry.Test, overlay, markers)
	entry.Red.Expected = "behavioral failure"
	if entry.Red.ExitCode == 0 || entry.Red.TestsFailed == 0 || len(entry.Red.AssertionEvidence) != 2 {
		t.Fatalf("publication negative control did not reproduce both assertions: %+v", entry.Red)
	}
	entry.Green = reviewRunEvidenceChild(t, moduleDir, entry.Test, "", markers)
	entry.Green.Expected = "pass"
	if entry.Green.ExitCode != 0 || !entry.Green.RootPassed || entry.Green.TestsFailed != 0 {
		t.Fatalf("fixed publication path did not pass: %+v", entry.Green)
	}
	current, err := os.ReadFile(sourcePath)
	if err != nil || string(current) != string(original) { t.Fatal("worktree source changed during evidence collection") }
	entry.Verified = true
	digest := sha256.Sum256(original)
	report := reviewEvidenceReport{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Scope: "Synthetic SDK instances and in-memory service; no real wallet or chain operation. The unchanged-baseline positive control remains enabled in both builds.",
		Sources: map[string]string{"rgb11_managed_operation.go": hex.EncodeToString(digest[:])},
		Cases: []reviewEvidenceCase{entry}, WorktreeUnchanged: true,
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil { t.Fatal(err) }
	dir := filepath.Join(moduleDir, "review-evidence")
	if err := os.MkdirAll(dir, 0700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(dir, "baseline-publication-red-green-20260930.json"), append(raw, '\n'), 0600); err != nil { t.Fatal(err) }
}
