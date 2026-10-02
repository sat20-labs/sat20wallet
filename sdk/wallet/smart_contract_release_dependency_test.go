package wallet

import (
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

// Optional tooling, not a substitute for the ordinary untagged E2E tests.
// Resolve the extra node-validation test imports in a temporary module file;
// report the minimal dependency changes without writing the real manifests.
func TestSmartContractReleaseDependencyResolution(t *testing.T) {
	if !strings.Contains(flag.Lookup("test.run").Value.String(), t.Name()) {
		t.Skip("explicit dependency inspection")
	}
	_, here, _, ok := runtime.Caller(0)
	if !ok { t.Fatal("locate module") }
	sdk := filepath.Dir(filepath.Dir(here))
	root := filepath.Dir(filepath.Dir(sdk))
	mod, err := os.ReadFile(filepath.Join(sdk, "go.mod")); if err != nil { t.Fatal(err) }
	sum, err := os.ReadFile(filepath.Join(sdk, "go.sum")); if err != nil { t.Fatal(err) }
	tmp := filepath.Join(t.TempDir(), "testdeps.mod")
	resolved := strings.ReplaceAll(string(mod), "=> ../../", "=> "+filepath.ToSlash(root)+"/")
	if err := os.WriteFile(tmp, []byte(resolved), 0600); err != nil { t.Fatal(err) }
	if err := os.WriteFile(strings.TrimSuffix(tmp, ".mod")+".sum", sum, 0600); err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute); defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "list", "-mod=mod", "-modfile="+tmp, "-deps", "-test", "./e2e")
	cmd.Dir = sdk
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil { t.Fatalf("test dependency resolution: %v: %.1500s", err, stderr.String()) }
	proposed, err := os.ReadFile(tmp); if err != nil { t.Fatal(err) }
	proposedSum, err := os.ReadFile(strings.TrimSuffix(tmp, ".mod")+".sum"); if err != nil { t.Fatal(err) }
	proposed = []byte(strings.ReplaceAll(string(proposed), "=> "+filepath.ToSlash(root)+"/", "=> ../../"))
	known := map[string]bool{}
	for _, line := range strings.Split(string(sum), "\n") { known[line] = true }
	added := []string{}
	for _, line := range strings.Split(string(proposedSum), "\n") { if line != "" && !known[line] { added = append(added, line) } }
	report := map[string]any{"original_mod_sha256": fmt.Sprintf("%x", sha256.Sum256(mod)), "original_sum_sha256": fmt.Sprintf("%x", sha256.Sum256(sum)), "proposed_mod": string(proposed), "added_sum_lines": added}
	encoded, err := json.MarshalIndent(report, "", "  "); if err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(sdk, "review-evidence", "smart-contract-test-dependencies.json"), append(encoded, '\n'), 0600); err != nil { t.Fatal(err) }
	current, err := os.ReadFile(filepath.Join(sdk, "go.mod")); if err != nil || !bytes.Equal(current, mod) { t.Fatal("module changed during inspection") }
	current, err = os.ReadFile(filepath.Join(sdk, "go.sum")); if err != nil || !bytes.Equal(current, sum) { t.Fatal("sums changed during inspection") }
}
